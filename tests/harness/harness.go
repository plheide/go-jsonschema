// Package harness generates code for many schemas, each into a package of its own in one temporary module, builds
// it, and decodes documents with the generated types, so that a test can compare their verdicts with a reference.
//
// The module requires the tests module and the generator, both replaced by this checkout, and reuses the tests
// module's go.sum, so it builds offline. A driver built from it (package driver) does the decoding, outside the race
// detector.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/plheide/go-jsonschema/pkg/generator"
	"github.com/plheide/go-jsonschema/pkg/schemas"
	"github.com/plheide/go-jsonschema/tests/harness/driver"
)

const (
	modulePath = "harness.local"
	driverPath = "github.com/plheide/go-jsonschema/tests/harness/driver"
	testsPath  = "github.com/plheide/go-jsonschema/tests"
	rootType   = "Root"
	wrapType   = "Wrap"

	// wrapSchema refers to a schema whose root is only a $ref. Such a root gets no type of its own, only the type
	// the $ref names, so its documents are decoded as this schema's required value property.
	wrapSchema = `{"type": "object", "required": ["value"], "properties": {"value": {"$ref": "root.json"}}}`

	// maxErrorLines is how much of a package's build errors an Outcome keeps.
	maxErrorLines = 5
	// buildRounds bounds the builds that drop the packages a build failed on before trying again.
	buildRounds = 3
)

var (
	errNoCaller = errors.New("cannot locate the tests module")
	errBuild    = errors.New("build failed")
	errDriver   = errors.New("driver failed")
)

// Schema is one schema to generate, with the documents to decode into its root type.
type Schema struct {
	// Name names the schema's package. It must be a lower-case Go identifier, unique in the run.
	Name string
	// Source is the schema document. It is written as root.json, so its root type is Root.
	Source []byte
	// Documents are decoded one by one, each into a new value.
	Documents []json.RawMessage
	// Cache, when set, returns the schemas to pre-load the loader with, keyed by the URLs $refs name them by. It is
	// called once per schema, so the generator can't carry changes from one schema into another. The loader reads
	// files and the cache only: a $ref to any other URL fails, so a run never goes to the network.
	Cache func() (map[string]*schemas.Schema, error)
}

// Outcome is what came of one schema.
type Outcome struct {
	// GenerateError is the generator's error, if it failed. The schema's documents are then not decoded.
	GenerateError string
	// Warnings are the generator's warnings, in the order it gave them.
	Warnings []string
	// CompileError holds the first lines of the package's build errors, if it failed to build. The schema's
	// documents are then not decoded.
	CompileError string
	// HasRoot reports whether the generated code declares the root type. Without it, documents decode into
	// interface{}.
	HasRoot bool
	// Wrapped reports that the schema's root is only a $ref, so its documents were decoded as the required value
	// property of a schema that refers to it.
	Wrapped bool
	// Results holds each document's verdicts, in order, when they were decoded.
	Results []driver.Result
}

// Run generates every schema with cfg into a temporary module under dir, builds it, and decodes each schema's
// documents. It returns an Outcome per schema, by name.
func Run(ctx context.Context, dir string, cfg generator.Config, list []Schema) (map[string]*Outcome, error) {
	testsDir, err := testsModuleDir()
	if err != nil {
		return nil, err
	}

	moduleDir := filepath.Join(dir, "module")
	if err := writeModule(ctx, moduleDir, testsDir); err != nil {
		return nil, err
	}

	outcomes := generateAll(moduleDir, cfg, list)

	if err := buildCases(ctx, moduleDir, outcomes); err != nil {
		return nil, err
	}

	if err := decodeAll(ctx, moduleDir, list, outcomes); err != nil {
		return nil, err
	}

	return outcomes, nil
}

func testsModuleDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errNoCaller
	}

	return filepath.Dir(filepath.Dir(file)), nil
}

// goMod is the part of `go mod edit -json` the module needs.
type goMod struct {
	Go      string `json:"Go"` //nolint:tagliatelle // go mod edit -json's names.
	Require []struct {
		Path    string `json:"Path"`    //nolint:tagliatelle // go mod edit -json's names.
		Version string `json:"Version"` //nolint:tagliatelle // go mod edit -json's names.
	} `json:"Require"` //nolint:tagliatelle // go mod edit -json's names.
	Replace []struct {
		Old struct {
			Path string `json:"Path"` //nolint:tagliatelle // go mod edit -json's names.
		} `json:"Old"` //nolint:tagliatelle // go mod edit -json's names.
		New struct {
			Path    string `json:"Path"`    //nolint:tagliatelle // go mod edit -json's names.
			Version string `json:"Version"` //nolint:tagliatelle // go mod edit -json's names.
		} `json:"New"` //nolint:tagliatelle // go mod edit -json's names.
	} `json:"Replace"` //nolint:tagliatelle // go mod edit -json's names.
}

// writeModule writes the module's go.mod, which takes the tests module's requirements and replacements and adds
// the tests module itself, and copies the tests module's go.sum.
func writeModule(ctx context.Context, moduleDir, testsDir string) error {
	if err := os.MkdirAll(moduleDir, 0o750); err != nil {
		return fmt.Errorf("create module: %w", err)
	}

	out, err := goCommand(ctx, testsDir, "mod", "edit", "-json").Output()
	if err != nil {
		return fmt.Errorf("read the tests module's go.mod: %w", err)
	}

	var mod goMod
	if err := json.Unmarshal(out, &mod); err != nil {
		return fmt.Errorf("decode the tests module's go.mod: %w", err)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "module %s\n\ngo %s\n\nrequire %s v0.0.0\n\nrequire (\n", modulePath, mod.Go, testsPath)

	for _, r := range mod.Require {
		fmt.Fprintf(&b, "\t%s %s\n", r.Path, r.Version)
	}

	fmt.Fprintf(&b, ")\n\nreplace %s => %s\n", testsPath, testsDir)

	for _, r := range mod.Replace {
		target := r.New.Path
		if r.New.Version != "" {
			target += " " + r.New.Version
		} else if !filepath.IsAbs(target) {
			target = filepath.Join(testsDir, target)
		}

		fmt.Fprintf(&b, "replace %s => %s\n", r.Old.Path, target)
	}

	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	sum, err := os.ReadFile(filepath.Join(testsDir, "go.sum"))
	if err != nil {
		return fmt.Errorf("read the tests module's go.sum: %w", err)
	}

	//nolint:gosec // The paths are the harness's own: the tests module and its temporary directory.
	if err := os.WriteFile(filepath.Join(moduleDir, "go.sum"), sum, 0o600); err != nil {
		return fmt.Errorf("write go.sum: %w", err)
	}

	return nil
}

func generateAll(moduleDir string, cfg generator.Config, list []Schema) map[string]*Outcome {
	outcomes := make(map[string]*Outcome, len(list))
	for _, s := range list {
		outcomes[s.Name] = &Outcome{}
	}

	var wg sync.WaitGroup

	work := make(chan Schema)

	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for s := range work {
				generate(moduleDir, cfg, s, outcomes[s.Name])
			}
		})
	}

	for _, s := range list {
		work <- s
	}

	close(work)
	wg.Wait()

	return outcomes
}

// generate generates one schema into its package and records how that went. A panic is recorded as a generator
// error, so one schema can't stop the run.
func generate(moduleDir string, cfg generator.Config, s Schema, o *Outcome) {
	dir := filepath.Join(moduleDir, "cases", s.Name)
	clean := func(msg string) string { return strings.ReplaceAll(msg, dir+string(filepath.Separator), "") }

	defer func() {
		if r := recover(); r != nil {
			o.GenerateError = clean(fmt.Sprintf("panic: %v", r))
		}
	}()

	if err := generateInto(dir, cfg, s, o, clean); err != nil {
		o.GenerateError = clean(err.Error())
	}
}

func generateInto(dir string, cfg generator.Config, s Schema, o *Outcome, clean func(string) string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create package: %w", err)
	}

	schemaFile := filepath.Join(dir, "root.json")
	if err := os.WriteFile(schemaFile, s.Source, 0o600); err != nil {
		return fmt.Errorf("write schema: %w", err)
	}

	target, typeName := schemaFile, rootType

	if rootIsRef(s.Source) {
		target, typeName, o.Wrapped = filepath.Join(dir, "wrap.json"), wrapType, true

		if err := os.WriteFile(target, []byte(wrapSchema), 0o600); err != nil {
			return fmt.Errorf("write wrapper: %w", err)
		}
	}

	cache := map[string]*schemas.Schema{}

	if s.Cache != nil {
		var err error
		if cache, err = s.Cache(); err != nil {
			return fmt.Errorf("pre-load schemas: %w", err)
		}
	}

	cfg.DefaultPackageName = modulePath + "/cases/" + s.Name
	cfg.DefaultOutputName = "-"
	cfg.SchemaMappings = nil
	cfg.Warner = func(msg string) { o.Warnings = append(o.Warnings, clean(msg)) }
	cfg.Cache = nil
	cfg.Loader = schemas.NewCachedLoader(
		schemas.MultiLoader{schemas.RefTypeFile: schemas.NewFileLoader(cfg.ResolveExtensions, cfg.YAMLExtensions)},
		cache,
	)

	g, err := generator.New(cfg)
	if err != nil {
		return fmt.Errorf("configure: %w", err)
	}

	if err := g.DoFile(target); err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	sources, err := g.Sources()
	if err != nil {
		return fmt.Errorf("format: %w", err)
	}

	for name, src := range sources {
		file := "root.go"
		if name != "-" {
			file = strings.TrimSuffix(filepath.Base(name), ".go") + ".go"
		}

		if err := os.WriteFile(filepath.Join(dir, file), src, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}

		if !o.HasRoot {
			o.HasRoot = declaresType(src, typeName)
		}
	}

	return nil
}

// rootIsRef reports whether a schema's root is an object with a $ref, which draft-07 reads as the whole schema.
func rootIsRef(source []byte) bool {
	var root map[string]json.RawMessage

	if json.Unmarshal(source, &root) != nil {
		return false
	}

	_, ok := root["$ref"]

	return ok
}

func declaresType(src []byte, name string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return false
	}

	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
				return true
			}
		}
	}

	return false
}

// buildCases builds every generated package and records the build errors of those that fail. A build reports each
// failing package under a "# <import path>" line; those packages are dropped and the rest built again, in case a
// failure hid another.
func buildCases(ctx context.Context, moduleDir string, outcomes map[string]*Outcome) error {
	pending := buildable(moduleDir, outcomes)

	for range buildRounds {
		if len(pending) == 0 {
			return nil
		}

		args := []string{"build"}
		for _, name := range pending {
			args = append(args, "./cases/"+name)
		}

		out, err := goCommand(ctx, moduleDir, args...).CombinedOutput()
		if err == nil {
			return nil
		}

		failed := packageErrors(out)
		if len(failed) == 0 {
			return fmt.Errorf("%w: %s", errBuild, out)
		}

		for name, msg := range failed {
			outcomes[name].CompileError = msg
		}

		pending = slices.DeleteFunc(pending, func(name string) bool { return outcomes[name].CompileError != "" })
	}

	return fmt.Errorf("%w: packages still fail after %d rounds", errBuild, buildRounds)
}

// buildable lists, sorted, the schemas whose generation left Go files to build.
func buildable(moduleDir string, outcomes map[string]*Outcome) []string {
	var names []string

	for name, o := range outcomes {
		if o.GenerateError != "" {
			continue
		}

		if files, _ := filepath.Glob(filepath.Join(moduleDir, "cases", name, "*.go")); len(files) > 0 {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// packageErrors splits a build's output into each failing package's errors, by schema name, keeping the first
// maxErrorLines lines with the package's directory trimmed from their paths.
func packageErrors(out []byte) map[string]string {
	failed := map[string][]string{}
	header := "# " + modulePath + "/cases/"
	current := ""

	for line := range strings.SplitSeq(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, header); ok {
			current = name

			continue
		}

		if current == "" || line == "" {
			continue
		}

		if len(failed[current]) < maxErrorLines {
			line = strings.TrimPrefix(line, "cases/"+current+"/")
			failed[current] = append(failed[current], line)
		}
	}

	msgs := make(map[string]string, len(failed))
	for name, lines := range failed {
		msgs[name] = strings.Join(lines, "\n")
	}

	return msgs
}

// decodeAll writes and builds the driver, which registers the root type of every package that built, and runs it on
// the documents of every schema that built.
func decodeAll(ctx context.Context, moduleDir string, list []Schema, outcomes map[string]*Outcome) error {
	documents := map[string][]json.RawMessage{}

	var main bytes.Buffer

	main.WriteString("// Code generated by the test harness. DO NOT EDIT.\n\npackage main\n\nimport (\n\t\"os\"\n\n")
	fmt.Fprintf(&main, "\t%q\n", driverPath)

	var registered []string

	for _, s := range list {
		o := outcomes[s.Name]
		if o.GenerateError != "" || o.CompileError != "" {
			continue
		}

		documents[s.Name] = s.Documents

		if o.Wrapped {
			documents[s.Name] = wrapped(s.Documents)
		}

		if o.HasRoot {
			fmt.Fprintf(&main, "\t%s %q\n", s.Name, modulePath+"/cases/"+s.Name)
			registered = append(registered, s.Name)
		}
	}

	main.WriteString(")\n\nfunc main() {\n\tr := driver.NewRegistry()\n")

	for _, name := range registered {
		typeName := rootType
		if outcomes[name].Wrapped {
			typeName = wrapType
		}

		fmt.Fprintf(&main, "\tdriver.Register[%s.%s](r, %q)\n", name, typeName, name)
	}

	main.WriteString("\tos.Exit(driver.Main(r, os.Args[1:]))\n}\n")

	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), main.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write driver: %w", err)
	}

	data, err := json.Marshal(documents)
	if err != nil {
		return fmt.Errorf("encode documents: %w", err)
	}

	docsFile := filepath.Join(moduleDir, "documents.json")
	if err := os.WriteFile(docsFile, data, 0o600); err != nil {
		return fmt.Errorf("write documents: %w", err)
	}

	bin := filepath.Join(moduleDir, "driver")
	if out, err := goCommand(ctx, moduleDir, "build", "-o", bin, ".").CombinedOutput(); err != nil {
		return fmt.Errorf("%w: build: %w: %s", errDriver, err, out)
	}

	resultsFile := filepath.Join(moduleDir, "results.json")

	if out, err := exec.CommandContext(ctx, bin, docsFile, resultsFile).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %w: %s", errDriver, err, out)
	}

	data, err = os.ReadFile(resultsFile)
	if err != nil {
		return fmt.Errorf("read results: %w", err)
	}

	var results map[string][]driver.Result
	if err := json.Unmarshal(data, &results); err != nil {
		return fmt.Errorf("decode results: %w", err)
	}

	for name, rs := range results {
		// A decoder's error names Go types with their package, which is the schema's name; drop it, so a message
		// doesn't depend on where the schema sits in the run.
		for i := range rs {
			for _, v := range []*driver.Verdict{&rs[i].JSON, &rs[i].YAML} {
				v.Error = strings.ReplaceAll(v.Error, name+".", "")
				v.Panic = strings.ReplaceAll(v.Panic, name+".", "")
			}
		}

		outcomes[name].Results = rs
	}

	return nil
}

// wrapped puts each document in the value property of an object, for the wrapper schema.
func wrapped(docs []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, len(docs))
	for i, doc := range docs {
		out[i] = slices.Concat([]byte(`{"value": `), doc, []byte(`}`))
	}

	return out
}

// goCommand runs the go command that runs the tests (go test puts its GOROOT/bin first on PATH), offline and
// outside any workspace, with checksums taken from go.sum and the module cache only.
func goCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir

	cmd.Env = append(
		slices.DeleteFunc(os.Environ(), func(kv string) bool {
			for _, name := range []string{"GOFLAGS=", "GOWORK=", "GOPROXY=", "GOSUMDB=", "GOTOOLCHAIN=", "CGO_ENABLED="} {
				if strings.HasPrefix(kv, name) {
					return true
				}
			}

			return false
		}),
		"GOFLAGS=-mod=mod", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0",
	)

	return cmd
}
