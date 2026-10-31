package tests_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/plheide/go-jsonschema/tests/harness"
	"github.com/plheide/go-jsonschema/tests/harness/driver"
)

var (
	corpusDir       = flag.String("differential.corpus", "", "directory of schema files for TestDifferentialCorpus")
	corpusGenerator = flag.String("differential.generator", "", "go-jsonschema executable that TestDifferentialCorpus tests")
	corpusBaseline  = flag.String("differential.baseline", "", "go-jsonschema executable it compares that one with")
	corpusReport    = flag.String("differential.report", "", "file TestDifferentialCorpus writes its report to")
)

const (
	corpusTimeout  = 60 * time.Second
	corpusMemoryMB = 3072
)

// corpusArgs are the options the corpus is generated with: TestDifferentialFixtures's.
var corpusArgs = []string{
	"--extra-imports", "--validate-null-types", "--validate-formats=all", "--strict-additional-properties=respect-schema",
}

// TestDifferentialCorpus runs the differential test over a directory of schemas, such as SchemaStore's, with two
// go-jsonschema executables. It fails on what the first gets wrong and the second, the baseline, gets right: a
// disagreement with the reference validator that the baseline doesn't have, or a schema that the baseline generates
// and builds and the first doesn't. Both see the same documents, and fetch the same remote $refs, so what changes
// between them is the generator. A regression counts only if it comes back when its schema runs again. The test runs
// only when -differential.corpus is given.
func TestDifferentialCorpus(t *testing.T) {
	t.Parallel()

	if *corpusDir == "" {
		t.Skip("set -differential.corpus, -differential.generator and -differential.baseline to run it")
	}

	if *corpusGenerator == "" || *corpusBaseline == "" {
		t.Fatal("-differential.corpus needs -differential.generator and -differential.baseline")
	}

	inputs, unreadable := corpusInputs(t, *corpusDir)

	list := make([]harness.Schema, len(inputs))

	for i, in := range inputs {
		docs := make([]json.RawMessage, len(in.docs))
		for j, m := range in.docs {
			docs[j] = m.doc
		}

		list[i] = harness.Schema{Name: in.name, File: in.file, Documents: docs, Oracle: true, Remote: true}
	}

	run := func(path string, list []harness.Schema, timeout time.Duration) map[string]*harness.Outcome {
		bin := harness.Binary{
			Path: path, Args: corpusArgs, Timeout: timeout, MemoryLimitMB: corpusMemoryMB, AssertFormat: true,
		}

		outcomes, err := harness.RunBinary(t.Context(), t.TempDir(), bin, list)
		if err != nil {
			t.Fatal(err)
		}

		return outcomes
	}

	current, baseline := run(*corpusGenerator, list, corpusTimeout), run(*corpusBaseline, list, corpusTimeout)
	currentVersion, baselineVersion := version(t, *corpusGenerator), version(t, *corpusBaseline)

	report, regressed := corpusComparison(inputs, unreadable, currentVersion, baselineVersion, current, baseline, nil)

	// A regression can come from the machine rather than the generator: a schema that needs nearly all the time it
	// has, or a remote $ref that fails to load once. Those schemas run again, with twice the time, and only a
	// regression that comes back counts.
	if len(regressed) > 0 {
		again := map[string]bool{}
		for _, name := range regressed {
			again[name] = true
		}

		retry := slices.DeleteFunc(slices.Clone(list), func(s harness.Schema) bool { return !again[s.Name] })

		maps.Copy(current, run(*corpusGenerator, retry, 2*corpusTimeout))
		maps.Copy(baseline, run(*corpusBaseline, retry, 2*corpusTimeout))

		report, regressed = corpusComparison(inputs, unreadable, currentVersion, baselineVersion, current, baseline, again)
	}

	if *corpusReport != "" {
		if err := os.WriteFile(*corpusReport, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	header, _, _ := strings.Cut(report, "\n\n")
	t.Log(header)

	if len(regressed) > 0 {
		t.Errorf("%d schemas regressed against the baseline; the report lists them", len(regressed))
	}
}

// corpusInputs lists, in name order, every schema file in dir whose root has something to generate, with the
// documents for each. It also lists the files it can't read as JSON, which the generators can't read either. It fails
// the test when dir has nothing to generate, so a wrong directory doesn't pass.
func corpusInputs(t *testing.T, dir string) ([]diffInput, []string) {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(paths)

	files := newSchemaFiles()

	var (
		inputs     []diffInput
		unreadable []string
	)

	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}

		f, err := files.load(abs)
		if err != nil {
			unreadable = append(unreadable, filepath.Base(path)+": "+oneLine(strings.TrimPrefix(err.Error(), abs+": ")))

			continue
		}

		root, _ := f.root.(map[string]any)

		if !slices.ContainsFunc(generatableKeywords, func(k string) bool {
			_, ok := root[k]

			return ok
		}) {
			continue
		}

		inputs = append(inputs, diffInput{
			name:  fmt.Sprintf("s%04d", len(inputs)),
			label: filepath.Base(path),
			file:  abs,
			docs:  documentsFor(files, f, f.root),
		})
	}

	if len(inputs) == 0 {
		t.Fatalf("no schema in %s has anything to generate", dir)
	}

	return inputs, unreadable
}

func version(t *testing.T, path string) string {
	t.Helper()

	out, err := exec.CommandContext(context.Background(), path, "--version").Output()
	if err != nil {
		return filepath.Base(path)
	}

	return strings.TrimSpace(string(out))
}

// corpusFailure says why a schema's documents were not judged, or "" when they were.
func corpusFailure(o *harness.Outcome) string {
	switch {
	case o.GenerateError != "":
		return "generation failed: " + oneLine(o.GenerateError)
	case o.CompileError != "":
		return "compilation failed: " + oneLine(o.CompileError)
	case o.OracleError != "":
		return "the reference validator can't compile it: " + oneLine(o.OracleError)
	default:
		return ""
	}
}

// disagreement names how a verdict differs from the reference validator's, or is "" when it agrees.
func disagreement(format string, v driver.Verdict, oracle *driver.OracleVerdict, kind string) string {
	switch {
	case oracle == nil || (v.Panic == "" && v.Accepted == oracle.Valid):
		return ""
	case v.Panic != "":
		return format + " panic: " + kind
	case v.Accepted:
		return fmt.Sprintf("%s missing check %s: %s", format, oracleKeyword(oracle), kind)
	default:
		return format + " wrong rejection: " + kind
	}
}

// corpusComparison renders the report of a corpus run, and names the schemas with a regression: those that only the
// baseline generates and builds, and those with a disagreement that only the run under test has. The schemas in
// again ran a second time.
func corpusComparison(
	inputs []diffInput, unreadable []string, current, baseline string, a, b map[string]*harness.Outcome,
	again map[string]bool,
) (string, []string) {
	var (
		body                                       strings.Builder
		regressed                                  []string
		broken, newlyWorking, both                 int
		newDisagreements, fixedDisagreements, docs int
	)

	for _, in := range inputs {
		oa, ob := a[in.name], b[in.name]
		fa, fb := corpusFailure(oa), corpusFailure(ob)

		var lines []string

		switch {
		case fa != "" && fb == "":
			broken++

			regressed = append(regressed, in.name)
			lines = []string{"regression: " + fa}
		case fa == "" && fb != "":
			newlyWorking++

			lines = []string{"newly judged; the baseline's " + fb}
		case fa == "" && fb == "":
			both++
			docs += len(oa.Results)

			before := newDisagreements
			groups := map[string]*diffGroup{}

			note := func(key, example string) {
				g, ok := groups[key]
				if !ok {
					g = &diffGroup{example: example}
					groups[key] = g
				}

				g.count++
			}

			for i, m := range in.docs {
				for _, format := range []string{"json", "yaml"} {
					ra, rb := oa.Results[i], ob.Results[i]

					va, vb := ra.JSON, rb.JSON
					if format == "yaml" {
						va, vb = ra.YAML, rb.YAML
					}

					da := disagreement(format, va, ra.Oracle, m.kind)
					db := disagreement(format, vb, rb.Oracle, m.kind)

					switch {
					case da != "" && db == "":
						newDisagreements++

						note("new "+da, m.example+": "+oneLine(va.Error+va.Panic))
					case da == "" && db != "":
						fixedDisagreements++

						note("fixed "+db, m.example)
					}
				}
			}

			if newDisagreements > before {
				regressed = append(regressed, in.name)
			}

			lines = make([]string, 0, len(groups))
			for _, key := range slices.Sorted(maps.Keys(groups)) {
				lines = append(lines, fmt.Sprintf("%s ×%d, e.g. %s", key, groups[key].count, groups[key].example))
			}
		}

		if again[in.name] {
			lines = append(lines, "ran again, with twice the time")
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

	fmt.Fprintf(&out, "Differential corpus run: %s\nagainst the baseline: %s\n", current, baseline)
	fmt.Fprintf(&out, "schemas: %d; unreadable files: %d; judged by both: %d, with %d documents; regressions: %d "+
		"schemas that only the baseline judges, and %d new disagreements; newly judged: %d schemas; fixed "+
		"disagreements: %d; ran again: %d schemas\n\n",
		len(inputs), len(unreadable), both, docs, broken, newDisagreements, newlyWorking, fixedDisagreements, len(again))

	if len(unreadable) > 0 {
		out.WriteString("== unreadable files\n" + strings.Join(unreadable, "\n") + "\n")
	}

	out.WriteString(body.String())

	return out.String(), regressed
}
