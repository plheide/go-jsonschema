package schemas_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plheide/go-jsonschema/pkg/schemas"
)

func parseRoot(t *testing.T, doc string) *schemas.Schema {
	t.Helper()

	s, err := schemas.FromJSONReader(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	return s
}

// TestRootSchemaRoutesDualFormDependencies checks that the root goes through
// the same decoding as any subschema. Decoded straight into the embedded
// ObjectAsType, the root skipped the dual-form `dependencies` routing, so both
// forms were silently dropped there.
func TestRootSchemaRoutesDualFormDependencies(t *testing.T) {
	t.Parallel()

	s := parseRoot(t, `{
		"type": "object",
		"dependencies": {
			"a": ["b"],
			"c": {"required": ["d"]}
		}
	}`)

	if got := s.DependentRequired["a"]; !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("DependentRequired[a] = %v, want [b]", got)
	}

	dep := s.DependentSchemas["c"]
	if dep == nil || !reflect.DeepEqual(dep.Required, []string{"d"}) {
		t.Errorf("DependentSchemas[c] = %+v, want a schema requiring d", dep)
	}
}

// TestRootSchemaKeepsIDsAndDefinitions pins what the root decoder still owns:
// the $id/id fallback, and definitions living on Schema rather than also on
// the embedded type.
func TestRootSchemaKeepsIDsAndDefinitions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		doc     string
		wantID  string
		wantDef string
	}{
		{
			name:    "$id and $defs",
			doc:     `{"$id": "https://example.com/a", "$defs": {"x": {"type": "string"}}}`,
			wantID:  "https://example.com/a",
			wantDef: "x",
		},
		{
			name:    "legacy id and definitions",
			doc:     `{"id": "https://example.com/b", "definitions": {"y": {"type": "string"}}}`,
			wantID:  "https://example.com/b",
			wantDef: "y",
		},
		{
			name:   "$id wins over id",
			doc:    `{"$id": "https://example.com/c", "id": "https://example.com/legacy"}`,
			wantID: "https://example.com/c",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := parseRoot(t, tc.doc)

			if s.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", s.ID, tc.wantID)
			}

			if _, ok := s.Definitions[tc.wantDef]; tc.wantDef != "" && !ok {
				t.Errorf("Definitions = %v, want key %q", s.Definitions, tc.wantDef)
			}

			// A root with only ids and definitions still decodes to a type,
			// exactly as it does once it adds $schema or a title.
			if s.ObjectAsType == nil {
				t.Fatal("root decoded without a type")
			}

			if s.ObjectAsType.Definitions != nil {
				t.Errorf("root definitions duplicated onto the embedded type: %v", s.ObjectAsType.Definitions)
			}
		})
	}
}

// TestRootSchemaAcceptsTupleItems covers a draft-07 tuple at the root, which
// failed the whole parse while the root bypassed Type's decoding.
func TestRootSchemaAcceptsTupleItems(t *testing.T) {
	t.Parallel()

	s := parseRoot(t, `{"type": "array", "items": [{"type": "string"}, {"type": "integer"}], "additionalItems": false}`)

	if len(s.TupleItems) != 2 {
		t.Fatalf("TupleItems = %d entries, want 2", len(s.TupleItems))
	}

	if got := s.TupleItems[1].Type; !reflect.DeepEqual(got, schemas.TypeList{"integer"}) {
		t.Errorf("TupleItems[1].Type = %v, want [integer]", got)
	}
}

// TestRootSchemaAcceptsBoolean covers a root schema that is `true`, which every
// value meets, or `false`, which none meets, in JSON and YAML.
func TestRootSchemaAcceptsBoolean(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		input string
		not   bool
	}{
		{"true", false},
		{"false", true},
	} {
		fromJSON, err := schemas.FromJSONReader(strings.NewReader(tc.input))
		if err != nil {
			t.Fatalf("FromJSONReader(%s): %v", tc.input, err)
		}

		fromYAML, err := schemas.FromYAMLReader(strings.NewReader(tc.input + "\n"))
		if err != nil {
			t.Fatalf("FromYAMLReader(%s): %v", tc.input, err)
		}

		for _, s := range []*schemas.Schema{fromJSON, fromYAML} {
			if (s.Not != nil) != tc.not || len(s.Type) != 0 || s.Properties != nil {
				t.Errorf("%s: Not = %v, Type = %v, Properties = %v", tc.input, s.Not, s.Type, s.Properties)
			}
		}
	}
}

// TestAnyOfKeepsUnlikeValues covers an anyOf whose parent and branch hold JSON
// values of different kinds in one place, an object and a string, in an
// extension and in a default. The merge keeps the parent's.
func TestAnyOfKeepsUnlikeValues(t *testing.T) {
	t.Parallel()

	object := map[string]any{"a": 1.0}
	parent := &schemas.Type{
		Type:       schemas.TypeList{schemas.TypeNameObject},
		Default:    object,
		Extensions: map[string]any{"x-meta": object},
		Properties: map[string]*schemas.Type{"p": {Type: schemas.TypeList{schemas.TypeNameString}}},
	}
	branch := &schemas.Type{
		Default:    "text",
		Extensions: map[string]any{"x-meta": "text"},
		Properties: map[string]*schemas.Type{"q": {Type: schemas.TypeList{schemas.TypeNameString}}},
	}

	merged, err := schemas.AnyOf([]*schemas.Type{branch}, parent)
	if err != nil {
		t.Fatalf("AnyOf: %v", err)
	}

	if !reflect.DeepEqual(merged.Default, object) || !reflect.DeepEqual(merged.Extensions["x-meta"], object) {
		t.Errorf("default = %#v, x-meta = %#v, want the parent's %#v", merged.Default, merged.Extensions["x-meta"], object)
	}
}

// TestAnyOfSchemasHoldingThemselves covers an anyOf over schemas that hold
// themselves, in their items and in a property, as the merge of a recursive
// allOf can leave them. The merge ends, and writes into neither.
func TestAnyOfSchemasHoldingThemselves(t *testing.T) {
	t.Parallel()

	holding := func(required, description string) *schemas.Type {
		s := &schemas.Type{Required: []string{required}, Description: description}
		s.Items = s
		s.Properties = map[string]*schemas.Type{"p": s}

		return s
	}

	object := func(items *schemas.Type, properties map[string]*schemas.Type) *schemas.Type {
		return &schemas.Type{Type: schemas.TypeList{schemas.TypeNameObject}, Items: items, Properties: properties}
	}

	a, b := holding("x", ""), holding("y", "b")

	merged, err := schemas.AnyOf(
		[]*schemas.Type{
			object(a, map[string]*schemas.Type{"p": b, "q": a}),
			object(b, map[string]*schemas.Type{"q": b}),
		},
		object(nil, map[string]*schemas.Type{"p": a}),
	)
	if err != nil {
		t.Fatalf("AnyOf: %v", err)
	}

	for _, s := range []*schemas.Type{a, b} {
		if s.Items != s || len(s.Properties) != 1 || s.Properties["p"] != s || len(s.Required) != 1 {
			t.Errorf("merge wrote into %p: items %p, properties %v, required %v", s, s.Items, s.Properties, s.Required)
		}
	}

	if a.Description != "" {
		t.Errorf("merge wrote description %q into a", a.Description)
	}

	if items := merged.Items; items == a || items.Items != items || items.Description != "b" {
		t.Errorf("items = %p, holding %p, described as %q; want a copy of a that holds itself, described as b's",
			items, items.Items, items.Description)
	}
}

// TestRootSchemaRejectsNullItems covers `items: null` at the root. A null never
// reaches a decoder hook, so only the pre-processing Type does can reject it.
func TestRootSchemaRejectsNullItems(t *testing.T) {
	t.Parallel()

	_, err := schemas.FromJSONReader(strings.NewReader(`{"type": "array", "items": null}`))
	if !errors.Is(err, schemas.ErrNullNotASchema) {
		t.Fatalf("err = %v, want %v", err, schemas.ErrNullNotASchema)
	}
}

// TestRejectsNullSubschemas covers a null where a composition entry, a
// property, a pattern property, a dependent schema or a definition must be a
// schema. encoding/json leaves such a member as a nil *Type without calling
// any decoder hook. In a composition, a property or a definition the
// generator then dereferenced it and crashed; the other two were read as
// absent. The parse now rejects each, and names the member.
func TestRejectsNullSubschemas(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		schema string
		at     string
	}{
		{`{"allOf": [{"type": "string"}, null]}`, `allOf[1]`},
		{`{"anyOf": [null]}`, `anyOf[0]`},
		{`{"oneOf": [{"type": "string"}, null]}`, `oneOf[1]`},
		{`{"type": "object", "properties": {"a": {"allOf": [null]}}}`, `allOf[0]`},
		{`{"type": "object", "properties": {"b": {"type": "string"}, "a": null}}`, `properties["a"]`},
		{`{"type": "object", "patternProperties": {"^x": null}}`, `patternProperties["^x"]`},
		{`{"type": "object", "dependentSchemas": {"a": null}}`, `dependentSchemas["a"]`},
		{`{"$defs": {"D": null}}`, `$defs["D"]`},
		{`{"definitions": {"D": null}}`, `definitions["D"]`},
		{`{"$defs": {"D": {"definitions": {"E": null}}}}`, `definitions["E"]`},
	} {
		_, err := schemas.FromJSONReader(strings.NewReader(tc.schema))
		if !errors.Is(err, schemas.ErrNullNotASchema) {
			t.Errorf("%s: err = %v, want %v", tc.schema, err, schemas.ErrNullNotASchema)

			continue
		}

		if !strings.Contains(err.Error(), tc.at+": ") {
			t.Errorf("%s: err = %v, want it to name %s", tc.schema, err, tc.at)
		}
	}

	if _, err := schemas.FromJSONReader(strings.NewReader(
		`{"allOf": [{"type": "string"}], "properties": {"a": {"$ref": "#/$defs/D"}}, "$defs": {"D": {"type": "string"}}}`,
	)); err != nil {
		t.Errorf("valid schema: err = %v", err)
	}
}

// TestDeprecatedKeyword checks the forms deprecated takes in practice: the
// boolean JSON Schema defines, the message that schemas generated from
// TypeScript give it instead, and anything else, which is ignored rather than
// failing the whole schema.
func TestDeprecatedKeyword(t *testing.T) {
	t.Parallel()

	s := parseRoot(t, `{
		"properties": {
			"flag": {"deprecated": true},
			"off": {"deprecated": false},
			"message": {"deprecated": "Use b instead."},
			"number": {"deprecated": 1},
			"null": {"deprecated": null},
			"absent": {}
		}
	}`)

	for name, want := range map[string]schemas.Deprecation{
		"flag":    {Marked: true},
		"off":     {},
		"message": {Marked: true, Message: "Use b instead."},
		"number":  {},
		"null":    {},
		"absent":  {},
	} {
		if got := s.Properties[name].Deprecated; got != want {
			t.Errorf("%s: Deprecated = %+v, want %+v", name, got, want)
		}
	}
}
