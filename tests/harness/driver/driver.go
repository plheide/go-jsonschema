// Package driver is the part of the test harness that runs inside the module the harness builds. It decodes each
// schema's documents into that schema's generated root type, as JSON and as YAML, and reports what each decoder made
// of them. When asked, it also validates each document with a reference validator, santhosh-tekuri/jsonschema.
package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

var errUsage = errors.New("usage: driver <inputs.json> <outputs.json>")

// Input is what the driver gets for one schema.
type Input struct {
	// Documents are decoded one by one, each into a new value.
	Documents []json.RawMessage `json:"documents"`
	// Wrap puts each document in the value property of an object before the decoders see it, for a type generated
	// from a schema that refers to the real one. The reference validator sees the document as it is.
	Wrap bool `json:"wrap,omitempty"`
	// Oracle, when set, validates each document with the reference validator too.
	Oracle *Oracle `json:"oracle,omitempty"`
}

// Oracle says where the reference validator finds a schema, and how to read it.
type Oracle struct {
	// Location is the schema's file, with a JSON pointer fragment for a subschema.
	Location string `json:"location"`
	// AssertFormat makes format a check rather than an annotation.
	AssertFormat bool `json:"assertFormat,omitempty"`
	// Draft7 reads a schema that declares draft-04 or draft-06 as draft-07, which keeps the meaning of what those
	// drafts have and adds what they lack, such as const. The generator reads every draft that way.
	Draft7 bool `json:"draft7,omitempty"`
}

// Output is what came of one schema's documents.
type Output struct {
	Results []Result `json:"results"`
	// OracleError is the reference validator's error, if it can't compile the schema. Results then carry no
	// oracle verdicts.
	OracleError string `json:"oracleError,omitempty"`
}

// Verdict is what one decoder made of one document.
type Verdict struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
	Panic    string `json:"panic,omitempty"`
}

// OracleVerdict is what the reference validator made of one document.
type OracleVerdict struct {
	Valid bool `json:"valid"`
	// Keyword is the keyword of the error the validator reports first, such as minLength, and Location its keyword
	// location in the schema.
	Keyword  string `json:"keyword,omitempty"`
	Location string `json:"location,omitempty"`
}

// Result holds a document's verdict in each format, and the reference validator's when it was asked.
type Result struct {
	JSON   Verdict        `json:"json"`
	YAML   Verdict        `json:"yaml"`
	Oracle *OracleVerdict `json:"oracle,omitempty"`
}

// Registry holds the decoders of each schema's root type, by schema name.
type Registry struct {
	decoders map[string]decoders
}

type decoders struct {
	json func(data []byte) error
	yaml func(data []byte) error
}

// NewRegistry returns an empty Registry. The documents of a schema it doesn't hold are decoded into interface{},
// which is what a schema with no root type leaves its users.
func NewRegistry() *Registry {
	return &Registry{decoders: map[string]decoders{}}
}

// Register makes the documents of the schema called name decode into new values of T.
func Register[T any](r *Registry, name string) {
	r.decoders[name] = decodersFor[T]()
}

func decodersFor[T any]() decoders {
	return decoders{
		json: func(data []byte) error {
			var v T

			return json.Unmarshal(data, &v)
		},
		yaml: func(data []byte) error {
			var v T

			return yaml.Unmarshal(data, &v)
		},
	}
}

// Main reads the inputs in the file args[0] names, a JSON object of Inputs by schema name. It writes the Outputs to
// the file args[1] names, by the same names, and returns the exit code.
func Main(r *Registry, args []string) int {
	if err := run(r, args); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return 0
}

func run(r *Registry, args []string) error {
	if len(args) != 2 {
		return errUsage
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read inputs: %w", err)
	}

	var inputs map[string]Input
	if err := json.Unmarshal(data, &inputs); err != nil {
		return fmt.Errorf("decode inputs: %w", err)
	}

	outputs := make(map[string]Output, len(inputs))

	for name, in := range inputs {
		d, ok := r.decoders[name]
		if !ok {
			d = decodersFor[any]()
		}

		outputs[name] = decodeInput(d, in)
	}

	out, err := json.Marshal(outputs)
	if err != nil {
		return fmt.Errorf("encode outputs: %w", err)
	}

	if err := os.WriteFile(args[1], out, 0o600); err != nil {
		return fmt.Errorf("write outputs: %w", err)
	}

	return nil
}

func decodeInput(d decoders, in Input) Output {
	var (
		out    Output
		oracle *jsonschema.Schema
	)

	if in.Oracle != nil {
		var err error
		if oracle, err = compileOracle(in.Oracle); err != nil {
			// The reasons after the first " - at " come in no fixed order; the message before them says what
			// failed.
			msg, _, _ := strings.Cut(strings.Join(strings.Fields(err.Error()), " "), " - at ")
			out.OracleError = msg
		}
	}

	out.Results = make([]Result, len(in.Documents))

	for i, doc := range in.Documents {
		decoded := []byte(doc)
		if in.Wrap {
			decoded = slices.Concat([]byte(`{"value": `), doc, []byte(`}`))
		}

		out.Results[i] = Result{JSON: decode(d.json, decoded), YAML: decode(d.yaml, decoded)}

		if oracle != nil {
			out.Results[i].Oracle = validate(oracle, doc)
		}
	}

	return out
}

func compileOracle(o *Oracle) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)

	var files jsonschema.URLLoader = jsonschema.FileLoader{}
	if o.Draft7 {
		files = draft7Loader{}
	}

	c.UseLoader(jsonschema.SchemeURLLoader{"file": files})

	if o.AssertFormat {
		c.AssertFormat()
	}

	sch, err := c.Compile(o.Location)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", o.Location, err)
	}

	return sch, nil
}

// draft7Loader loads schema files as FileLoader does, and makes those that declare draft-04 or draft-06 declare
// draft-07 instead.
type draft7Loader struct{}

func (draft7Loader) Load(url string) (any, error) {
	doc, err := jsonschema.FileLoader{}.Load(url)
	if err != nil {
		return nil, err //nolint:wrapcheck // The compiler reports the loader's error with the URL.
	}

	if m, ok := doc.(map[string]any); ok {
		if s, _ := m["$schema"].(string); strings.Contains(s, "/draft-04/") || strings.Contains(s, "/draft-06/") {
			m["$schema"] = "http://json-schema.org/draft-07/schema#"
		}
	}

	return doc, nil
}

// validate runs the reference validator on one document. Of the innermost errors, the ones that say what failed
// rather than which composition holds them, it names the one with the first keyword location, as the validator
// reports causes in no fixed order.
func validate(sch *jsonschema.Schema, doc []byte) *OracleVerdict {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return &OracleVerdict{Keyword: "(not JSON)"}
	}

	err = sch.Validate(inst)
	if err == nil {
		return &OracleVerdict{Valid: true}
	}

	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return &OracleVerdict{Keyword: "(" + err.Error() + ")"}
	}

	var first *OracleVerdict

	for _, leaf := range leaves(ve) {
		path := leaf.ErrorKind.KeywordPath()

		v := &OracleVerdict{Location: leaf.SchemaURL}
		if len(path) > 0 {
			v.Keyword = path[0]
			v.Location += "/" + strings.Join(path, "/")
		}

		if first == nil || v.Location < first.Location {
			first = v
		}
	}

	return first
}

func leaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}

	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, leaves(c)...)
	}

	return out
}

// decode runs one decoder on one document. A panic is a verdict of its own: generated code must not panic on any
// input.
func decode(decoder func(data []byte) error, doc []byte) Verdict {
	var v Verdict

	func() {
		defer func() {
			if r := recover(); r != nil {
				v = Verdict{Panic: fmt.Sprint(r)}
			}
		}()

		if err := decoder(doc); err != nil {
			v = Verdict{Error: err.Error()}
		} else {
			v = Verdict{Accepted: true}
		}
	}()

	return v
}
