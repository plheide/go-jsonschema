package tests_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/plheide/go-jsonschema/pkg/generator"
	"github.com/plheide/go-jsonschema/tests/harness"
	"github.com/plheide/go-jsonschema/tests/harness/driver"
)

const (
	fixturesDir        = "data"
	differentialGolden = "testdata/differential/fixtures.txt"
)

// generatableKeywords are those that give a schema's root a type of its own, as the generator decides it.
var generatableKeywords = []string{
	"type", "$ref", "enum", "const", "oneOf", "anyOf", "allOf", "properties", "additionalProperties",
}

// plainName matches the definition names a JSON pointer fragment can carry unescaped. The test leaves out the few
// others, rather than depend on how the generator and the reference validator each unescape a fragment.
var plainName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// diffInput is one schema the differential test decodes documents into: a fixture's root, or one of its top-level
// definitions.
type diffInput struct {
	name    string
	label   string
	file    string
	pointer string
	docs    []mutant
}

// TestDifferentialFixtures runs documents derived from every fixture under tests/data, and from each fixture's
// top-level definitions, through the generated code and through a reference validator, santhosh-tekuri/jsonschema.
// The documents are seeds built from each schema, and mutants that each change one node of a seed. The golden report
// groups the disagreements per schema, so a change shows in its diff; regenerate it with
// OVERWRITE_EXPECTED_GO_FILE=true.
func TestDifferentialFixtures(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("builds a module of generated code")
	}

	inputs := fixtureInputs(t, fixturesDir)

	list := make([]harness.Schema, len(inputs))

	for i, in := range inputs {
		docs := make([]json.RawMessage, len(in.docs))
		for j, m := range in.docs {
			docs[j] = m.doc
		}

		list[i] = harness.Schema{
			Name: in.name, File: in.file, Pointer: in.pointer, Documents: docs, Oracle: true, Draft7: true,
		}
	}

	// The conformance test's options, with formats checked too, as the reference validator is told to.
	cfg := basicConfig
	cfg.ValidateNullTypes = true
	cfg.StrictAdditionalProperties = generator.StrictAdditionalPropertiesRespectSchema
	cfg.FormatValidation = generator.FormatValidationConfig{Enabled: true}

	outcomes, err := harness.Run(t.Context(), t.TempDir(), cfg, list)
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, differentialGolden, differentialReport(inputs, outcomes))
}

// fixtureInputs lists, in path order, every fixture that is not a *.FAIL.json, followed each time by its top-level
// definitions, with the documents for each.
func fixtureInputs(t *testing.T, dir string) []diffInput {
	t.Helper()

	var paths []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".FAIL.json") {
			paths = append(paths, path)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(paths)

	files := newSchemaFiles()

	var inputs []diffInput

	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}

		f, err := files.load(abs)
		if err != nil {
			t.Fatal(err)
		}

		rel := filepath.ToSlash(strings.TrimPrefix(path, dir+string(filepath.Separator)))

		add := func(pointer string, schema any) {
			inputs = append(inputs, diffInput{
				name:    fmt.Sprintf("d%04d", len(inputs)),
				label:   rel + pointer,
				file:    abs,
				pointer: pointer,
				docs:    documentsFor(files, f, schema),
			})
		}

		root, _ := f.root.(map[string]any)

		// A root with nothing to generate gets no type, so there is nothing to decode into; a schema that refers to
		// it would get a type the generator never gives its users.
		if slices.ContainsFunc(generatableKeywords, func(k string) bool {
			_, ok := root[k]

			return ok
		}) {
			add("", f.root)
		}

		for _, key := range []string{"definitions", "$defs"} {
			defs, _ := root[key].(map[string]any)

			for _, name := range slices.Sorted(maps.Keys(defs)) {
				if plainName.MatchString(name) {
					add("#/"+key+"/"+name, defs[name])
				}
			}
		}
	}

	return inputs
}

// diffGroup counts one kind of disagreement for one schema, and keeps the first example.
type diffGroup struct {
	count   int
	example string
}

// diffCounts tallies one format's verdicts against the reference validator's.
type diffCounts struct {
	agree, missing, silent, wrong, panics int
}

func (c diffCounts) String() string {
	return fmt.Sprintf("%d agree, %d missing checks (%d silent), %d wrong rejections, %d panics",
		c.agree, c.missing, c.silent, c.wrong, c.panics)
}

// differentialReport renders the golden report: totals first, then each schema with a disagreement, one line per
// format, direction, keyword and kind of document, with a count and the first example.
func differentialReport(inputs []diffInput, outcomes map[string]*harness.Outcome) string {
	var (
		body                     strings.Builder
		counts                   = map[string]*diffCounts{"json": {}, "yaml": {}}
		documents, skipped, defs int
	)

	for _, in := range inputs {
		if in.pointer != "" {
			defs++
		}

		o := outcomes[in.name]

		var lines []string

		switch {
		case o.GenerateError != "":
			lines = []string{"skipped: generation failed: " + oneLine(o.GenerateError)}
		case o.CompileError != "":
			lines = []string{"skipped: compilation failed: " + oneLine(o.CompileError)}
		case o.OracleError != "":
			lines = []string{"skipped: the reference validator can't compile it: " + oneLine(o.OracleError)}
		default:
			documents += len(o.Results)
			lines = disagreements(in, o, len(o.Warnings) == 0, counts)
		}

		if len(lines) > 0 && strings.HasPrefix(lines[0], "skipped: ") {
			skipped++
		}

		if len(lines) == 0 {
			continue
		}

		fmt.Fprintf(&body, "\n== %s\n", in.label)

		for _, line := range lines {
			body.WriteString(line + "\n")
		}
	}

	var out strings.Builder

	out.WriteString("Documents derived from the fixtures under tests/data, decoded into the generated types and validated\n")
	out.WriteString("by santhosh-tekuri/jsonschema v6, which reads draft-07 unless $schema says otherwise and checks formats.\n")
	out.WriteString("The documents are seeds built from each schema, and mutants that each change one node of a seed.\n")
	out.WriteString("Each line counts one kind of disagreement for one schema: a missing check accepts a document the\n")
	out.WriteString("validator rejects, for the keyword it names; a wrong rejection rejects one it accepts. A missing\n")
	out.WriteString("check is silent when the generator gave no warning for the schema.\n")
	out.WriteString("Regenerate this file with OVERWRITE_EXPECTED_GO_FILE=true.\n\n")
	fmt.Fprintf(&out, "schemas: %d (%d fixtures, %d definitions), %d skipped; documents: %d\n",
		len(inputs), len(inputs)-defs, defs, skipped, documents)
	fmt.Fprintf(&out, "json: %s\nyaml: %s\n", counts["json"], counts["yaml"])
	out.WriteString(body.String())

	return out.String()
}

// disagreements counts one schema's verdicts and returns a sorted line per group of disagreements.
func disagreements(in diffInput, o *harness.Outcome, silent bool, counts map[string]*diffCounts) []string {
	groups := map[string]*diffGroup{}

	note := func(key, example string) {
		g, ok := groups[key]
		if !ok {
			g = &diffGroup{example: example}
			groups[key] = g
		}

		g.count++
	}

	for i, r := range o.Results {
		if r.Oracle == nil {
			continue
		}

		m := in.docs[i]

		for _, format := range []string{"json", "yaml"} {
			v, c := r.JSON, counts[format]
			if format == "yaml" {
				v = r.YAML
			}

			switch {
			case v.Panic != "":
				c.panics++

				note(format+" panic: "+m.kind, m.example+": "+oneLine(v.Panic))
			case v.Accepted == r.Oracle.Valid:
				c.agree++
			case v.Accepted:
				c.missing++

				mark := ""

				if silent {
					c.silent++
					mark = " (silent)"
				}

				note(fmt.Sprintf("%s missing check%s %s: %s", format, mark, oracleKeyword(r.Oracle), m.kind), m.example)
			default:
				c.wrong++

				note(format+" wrong rejection: "+m.kind, m.example+": "+oneLine(v.Error))
			}
		}
	}

	lines := make([]string, 0, len(groups))
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		g := groups[key]
		lines = append(lines, fmt.Sprintf("%s ×%d, e.g. %s", key, g.count, g.example))
	}

	return lines
}

func oracleKeyword(v *driver.OracleVerdict) string {
	if v.Keyword == "" {
		return "(no keyword)"
	}

	return v.Keyword
}
