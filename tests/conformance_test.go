package tests_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/plheide/go-jsonschema/pkg/generator"
	"github.com/plheide/go-jsonschema/pkg/schemas"
	"github.com/plheide/go-jsonschema/tests/harness"
	"github.com/plheide/go-jsonschema/tests/harness/driver"
)

const (
	suiteDir          = "testdata/JSON-Schema-Test-Suite"
	conformanceGolden = "testdata/conformance/draft7.txt"
	// suiteRemoteBase is where the suite's tests expect its remotes directory to be served.
	suiteRemoteBase = "http://localhost:1234/"
)

type suiteFile struct {
	name  string
	cases []suiteCase
}

type suiteCase struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []suiteTest     `json:"tests"`
}

type suiteTest struct {
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	Valid       bool            `json:"valid"`
}

// TestConformanceDraft7 runs the JSON-Schema-Test-Suite's draft-07 tests, the required ones, through generated code.
// Each test's document is decoded into the type generated from its schema, as JSON and as YAML, and each verdict is
// compared with the suite's. The golden report lists every disagreement, so a change in conformance shows in its
// diff; regenerate it with OVERWRITE_EXPECTED_GO_FILE=true.
func TestConformanceDraft7(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("builds a module of generated code")
	}

	files := readSuite(t, filepath.Join(suiteDir, "tests", "draft7"))
	remotes := suiteRemotes(filepath.Join(suiteDir, "remotes"))

	var list []harness.Schema

	for fi, f := range files {
		for ci, c := range f.cases {
			docs := make([]json.RawMessage, len(c.Tests))
			for i, test := range c.Tests {
				docs[i] = test.Data
			}

			list = append(list, harness.Schema{
				Name:      suiteCaseName(fi, ci),
				Source:    c.Schema,
				Documents: docs,
				Cache:     remotes,
			})
		}
	}

	// The options that follow the spec: a null is checked against the type, and additionalProperties: false
	// rejects other properties. Formats stay annotations, as draft-07 has them by default.
	cfg := basicConfig
	cfg.ValidateNullTypes = true
	cfg.StrictAdditionalProperties = generator.StrictAdditionalPropertiesRespectSchema

	outcomes, err := harness.Run(t.Context(), t.TempDir(), cfg, list)
	if err != nil {
		t.Fatal(err)
	}

	commit, err := os.ReadFile(filepath.Join(suiteDir, "COMMIT"))
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, conformanceGolden, conformanceReport(strings.TrimSpace(string(commit)), files, outcomes))
}

func suiteCaseName(file, index int) string {
	return fmt.Sprintf("f%02dc%02d", file, index)
}

func readSuite(t *testing.T, dir string) []suiteFile {
	t.Helper()

	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(names)

	files := make([]suiteFile, 0, len(names))

	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		f := suiteFile{name: filepath.Base(name)}
		if err := json.Unmarshal(data, &f.cases); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		files = append(files, f)
	}

	return files
}

// suiteRemotes returns a loader cache that serves the suite's remotes directory at suiteRemoteBase, as the suite's
// tests expect. Each call parses the files again, so no schema's generation sees another's.
func suiteRemotes(dir string) func() (map[string]*schemas.Schema, error) {
	return func() (map[string]*schemas.Schema, error) {
		cache := map[string]*schemas.Schema{}

		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
				return err
			}

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}

			s, err := schemas.FromJSONFile(path)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}

			cache[suiteRemoteBase+filepath.ToSlash(rel)] = s

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("load the suite's remotes: %w", err)
		}

		return cache, nil
	}
}

// verdictCounts tallies one format's verdicts against the suite's.
type verdictCounts struct {
	agree, missing, silent, wrong, panics, notRun int
}

func (c verdictCounts) String() string {
	return fmt.Sprintf("%d agree, %d missing checks (%d silent), %d wrong rejections, %d panics, %d not run",
		c.agree, c.missing, c.silent, c.wrong, c.panics, c.notRun)
}

// conformanceReport renders the golden report: totals first, then each case that has anything to report, with its
// warnings and failures and one line per test and format whose verdict differs from the suite's.
func conformanceReport(commit string, files []suiteFile, outcomes map[string]*harness.Outcome) string {
	var (
		body                           strings.Builder
		jsonCounts, yamlCounts         verdictCounts
		cases, tests                   int
		genFailed, buildFailed, warned int
	)

	for fi, f := range files {
		fileHeader := false

		for ci, c := range f.cases {
			cases++
			tests += len(c.Tests)

			o := outcomes[suiteCaseName(fi, ci)]
			warnings := slices.Compact(slices.Sorted(slices.Values(o.Warnings)))

			var lines []string

			for _, w := range warnings {
				lines = append(lines, "warning: "+oneLine(w))
			}

			switch {
			case o.GenerateError != "":
				genFailed++

				lines = append(lines, "generation failed: "+oneLine(o.GenerateError))
			case o.CompileError != "":
				buildFailed++

				lines = append(lines, "compilation failed: "+oneLine(o.CompileError))
			}

			for i, test := range c.Tests {
				var result driver.Result
				if i < len(o.Results) {
					result = o.Results[i]
				}

				ran := i < len(o.Results)
				silent := len(warnings) == 0

				lines = appendVerdict(lines, "json", test, result.JSON, ran, silent, &jsonCounts)
				lines = appendVerdict(lines, "yaml", test, result.YAML, ran, silent, &yamlCounts)
			}

			if len(warnings) > 0 {
				warned++
			}

			if len(lines) == 0 {
				continue
			}

			if !fileHeader {
				fmt.Fprintf(&body, "\n== %s\n", f.name)

				fileHeader = true
			}

			fmt.Fprintf(&body, "\n-- %s\n", c.Description)

			for _, line := range lines {
				body.WriteString(line + "\n")
			}
		}
	}

	var out strings.Builder

	fmt.Fprintf(&out, "JSON-Schema-Test-Suite %s, draft7, required tests: %d cases, %d tests.\n\n", commit, cases, tests)
	out.WriteString("Each test's document is decoded into the type generated from its schema, as JSON and as YAML.\n")
	out.WriteString("A root that is only a $ref gets no type of its own, so its documents are decoded as the\n")
	out.WriteString("required property of a schema that refers to it.\n")
	out.WriteString("A missing check accepts a document the suite marks invalid, and a wrong rejection rejects one\n")
	out.WriteString("it marks valid. A missing check is silent when its case has no generator warning.\n")
	out.WriteString("Regenerate this file with OVERWRITE_EXPECTED_GO_FILE=true.\n\n")
	fmt.Fprintf(&out, "json: %s\nyaml: %s\n", jsonCounts, yamlCounts)
	fmt.Fprintf(&out, "cases: %d generation failures, %d compilation failures, %d with warnings\n",
		genFailed, buildFailed, warned)
	out.WriteString(body.String())

	return out.String()
}

// appendVerdict counts one verdict and, when it differs from the suite's, adds a line for it.
func appendVerdict(
	lines []string, format string, test suiteTest, v driver.Verdict, ran, silent bool, counts *verdictCounts,
) []string {
	switch {
	case !ran:
		counts.notRun++

		return lines
	case v.Panic != "":
		counts.panics++

		return append(lines, fmt.Sprintf("%s panic: %s: %s", format, test.Description, oneLine(v.Panic)))
	case v.Accepted == test.Valid:
		counts.agree++

		return lines
	case v.Accepted:
		counts.missing++

		if !silent {
			return append(lines, fmt.Sprintf("%s missing check: %s", format, test.Description))
		}

		counts.silent++

		return append(lines, fmt.Sprintf("%s missing check (silent): %s", format, test.Description))
	default:
		counts.wrong++

		return append(lines, fmt.Sprintf("%s wrong rejection: %s: %s", format, test.Description, oneLine(v.Error)))
	}
}

// oneLine joins a message's lines, so each report entry is one line.
func oneLine(msg string) string {
	return strings.Join(strings.Fields(msg), " ")
}

// checkGolden compares a report with its golden file, or writes the file when OVERWRITE_EXPECTED_GO_FILE=true or
// when it doesn't exist yet, as the generated Go goldens are handled.
func checkGolden(t *testing.T, path, got string) {
	t.Helper()

	want, err := os.ReadFile(path)
	if os.Getenv("OVERWRITE_EXPECTED_GO_FILE") == "true" || errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}

		t.Logf("wrote %s", path)

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s differs (-want +got); if the change is intended, regenerate it with "+
			"OVERWRITE_EXPECTED_GO_FILE=true and review the diff:\n%s", path, diff)
	}
}
