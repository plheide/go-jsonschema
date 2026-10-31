// Package driver is the part of the test harness that runs inside the module the harness builds. It decodes each
// schema's documents into that schema's generated root type, as JSON and as YAML, and reports what each decoder made
// of them.
package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

var errUsage = errors.New("usage: driver <documents.json> <results.json>")

// Verdict is what one decoder made of one document.
type Verdict struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
	Panic    string `json:"panic,omitempty"`
}

// Result holds a document's verdict in each format.
type Result struct {
	JSON Verdict `json:"json"`
	YAML Verdict `json:"yaml"`
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

// Main decodes the documents in the file args[0] names, a JSON object of document lists by schema name. It writes
// their results to the file args[1] names, by schema name and in the same order, and returns the exit code.
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
		return fmt.Errorf("read documents: %w", err)
	}

	var documents map[string][]json.RawMessage
	if err := json.Unmarshal(data, &documents); err != nil {
		return fmt.Errorf("decode documents: %w", err)
	}

	results := make(map[string][]Result, len(documents))

	for name, list := range documents {
		d, ok := r.decoders[name]
		if !ok {
			d = decodersFor[any]()
		}

		results[name] = make([]Result, len(list))
		for i, doc := range list {
			results[name][i] = Result{JSON: decode(d.json, doc), YAML: decode(d.yaml, doc)}
		}
	}

	out, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("encode results: %w", err)
	}

	if err := os.WriteFile(args[1], out, 0o600); err != nil {
		return fmt.Errorf("write results: %w", err)
	}

	return nil
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
