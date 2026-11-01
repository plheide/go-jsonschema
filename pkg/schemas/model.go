// Package schemas defines JSON schema types.
//
// Code borrowed from https://github.com/alecthomas/jsonschema/
//
// # Copyright (C) 2014 Alec Thomas
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
// of the Software, and to permit persons to whom the Software is furnished to do
// so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"dario.cat/mergo"
)

var (
	ErrCannotMergeTypes = fmt.Errorf("cannot merge types")
	ErrEmptyTypesList   = fmt.Errorf("types list is empty")
	// ErrNullPropertyDependency is returned for a draft-07 property
	// dependency whose array contains null, which cannot name a property.
	ErrNullPropertyDependency = fmt.Errorf("null is not a valid property name")
	// ErrNullNotASchema is returned where JSON Schema requires a schema
	// (an object or a boolean) but the document supplies null.
	ErrNullNotASchema = fmt.Errorf("null is not a valid schema")
	// ErrCountNotInteger is returned for a count keyword, such as minItems,
	// whose value is a number that holds no integer.
	ErrCountNotInteger = fmt.Errorf("count is not an integer")
)

// Schema is the root schema.
type Schema struct {
	*ObjectAsType

	ID          string      `json:"$id"` // RFC draft-wright-json-schema-01, section-9.2.
	LegacyID    string      `json:"id"`  // RFC draft-wright-json-schema-00, section 4.5.
	Definitions Definitions `json:"$defs,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler for Schema struct.
func (s *Schema) UnmarshalJSON(data []byte) error {
	// The root carries the same keywords as any subschema, so decode them
	// through Type, whose pre-processing must not stop at the root. Decoding
	// straight into the embedded ObjectAsType bypassed it: dual-form
	// `dependencies` at the root, for one, never reached DependentRequired or
	// DependentSchemas.
	var typ Type
	if err := json.Unmarshal(data, &typ); err != nil {
		return fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	var ids struct {
		ID       string `json:"$id"`
		LegacyID string `json:"id"`
	}

	// A boolean schema, `true` or `false`, has no identifiers.
	if !isJSONBoolean(data) {
		if err := json.Unmarshal(data, &ids); err != nil {
			return fmt.Errorf("failed to unmarshal schema: %w", err)
		}
	}

	// Fall back to id if $id is not present.
	if ids.ID == "" {
		ids.ID = ids.LegacyID
	}

	// Type has already folded the legacy `definitions` into its $defs. At the
	// root they belong to Schema, whose field shadows Type's, so move them
	// across rather than leave a second copy behind.
	definitions := typ.Definitions
	typ.Definitions = nil

	*s = Schema{
		ObjectAsType: (*ObjectAsType)(&typ),
		ID:           ids.ID,
		LegacyID:     ids.LegacyID,
		Definitions:  definitions,
	}

	return nil
}

type ObjectAsType Type

// TypeList is a list of type names.
type TypeList []string

// UnmarshalJSON implements json.Unmarshaler.
func (t *TypeList) UnmarshalJSON(value []byte) error {
	if len(value) > 0 && value[0] == '[' {
		var s []string
		if err := json.Unmarshal(value, &s); err != nil {
			return fmt.Errorf("failed to unmarshal type list: %w", err)
		}

		*t = s

		return nil
	}

	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return fmt.Errorf("failed to unmarshal type list: %w", err)
	}

	if s != "" {
		*t = []string{s}
	} else {
		*t = nil
	}

	return nil
}

func (t *TypeList) Equals(b TypeList) bool {
	if t == nil {
		return false
	}

	if len(*t) != len(b) {
		return false
	}

	for i := range *t {
		if (*t)[i] != b[i] {
			return false
		}
	}

	return true
}

func (t *TypeList) Add(s string) {
	if !slices.Contains(*t, s) {
		*t = append(*t, s)
	}
}

// Deprecation is the value of the deprecated keyword. JSON Schema defines it
// as a boolean. Schemas generated from TypeScript give it the text of a
// @deprecated tag instead, which marks the instance as deprecated too. Any
// other value is ignored, so that an annotation never stops a schema from
// loading.
type Deprecation struct {
	// Marked is true when the keyword is true or a string.
	Marked bool
	// Message is the keyword's text, when it is a string.
	Message string
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Deprecation) UnmarshalJSON(value []byte) error {
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return fmt.Errorf("failed to unmarshal deprecated: %w", err)
	}

	switch v := v.(type) {
	case bool:
		*d = Deprecation{Marked: v}
	case string:
		*d = Deprecation{Marked: true, Message: v}
	default:
		*d = Deprecation{}
	}

	return nil
}

// Definitions hold schema definitions.
// http://json-schema.org/latest/json-schema-validation.html#rfc.section.5.26
// RFC draft-wright-json-schema-validation-00, section 5.26.
type Definitions map[string]*Type

type SubSchemaType string

// extensionPrefix marks the vendor-extension keywords collected into
// Type.Extensions. JSON Schema reserves nothing under it, and the OpenAPI
// ecosystem has settled on it for vendor data.
const extensionPrefix = "x-"

const (
	SubSchemaTypeAllOf SubSchemaType = "allOf"
	SubSchemaTypeAnyOf SubSchemaType = "anyOf"
	SubSchemaTypeOneOf SubSchemaType = "oneOf"
	SubSchemaTypeNot   SubSchemaType = "not"
)

// Type represents a JSON Schema object type.
type Type struct {
	// RFC draft-wright-json-schema-00.
	Version string `json:"$schema,omitempty"` // Section 6.1.
	Ref     string `json:"$ref,omitempty"`    // Section 7.
	// RFC draft-wright-json-schema-validation-00, section 5.
	MultipleOf       *float64 `json:"multipleOf,omitempty"`       // Section 5.1.
	Maximum          *float64 `json:"maximum,omitempty"`          // Section 5.2.
	ExclusiveMaximum *any     `json:"exclusiveMaximum,omitempty"` // Section 5.3. Changed in draft 4.
	Minimum          *float64 `json:"minimum,omitempty"`          // Section 5.4.
	ExclusiveMinimum *any     `json:"exclusiveMinimum,omitempty"` // Section 5.5. Changed in draft 4.
	MaxLength        int      `json:"maxLength,omitempty"`        // Section 5.6.
	MinLength        int      `json:"minLength,omitempty"`        // Section 5.7.
	// MultipleOfExact, MaximumExact, ExclusiveMaximumExact, MinimumExact
	// and ExclusiveMinimumExact are the numeric keywords as written, as
	// json.Number, or "" for one that is absent or, for an exclusive bound,
	// a draft-04 boolean. The float64 fields round an integer beyond 2^53 and
	// most fractions. Set by Type's custom UnmarshalJSON.
	MultipleOfExact       json.Number `json:"-"`
	MaximumExact          json.Number `json:"-"`
	ExclusiveMaximumExact json.Number `json:"-"`
	MinimumExact          json.Number `json:"-"`
	ExclusiveMinimumExact json.Number `json:"-"`
	Pattern               string      `json:"pattern,omitempty"`         // Section 5.8.
	AdditionalItems       *Type       `json:"additionalItems,omitempty"` // Section 5.9.
	Items                 *Type       `json:"items,omitempty"`           // Section 5.9.
	// TupleItems holds the draft-07 tuple form of `items` (an array of
	// schemas, one per position). The single-schema form stays in Items;
	// exactly one of the two is ever populated. Not a wire field — it is
	// filled in by Type.UnmarshalJSON.
	TupleItems           []*Type          `json:"-"`
	MaxItems             int              `json:"maxItems,omitempty"`             // Section 5.10.
	MinItems             int              `json:"minItems,omitempty"`             // Section 5.11.
	UniqueItems          bool             `json:"uniqueItems,omitempty"`          // Section 5.12.
	MaxProperties        int              `json:"maxProperties,omitempty"`        // Section 5.13.
	MinProperties        int              `json:"minProperties,omitempty"`        // Section 5.14.
	Required             []string         `json:"required,omitempty"`             // Section 5.15.
	Properties           map[string]*Type `json:"properties,omitempty"`           // Section 5.16.
	PatternProperties    map[string]*Type `json:"patternProperties,omitempty"`    // Section 5.17.
	AdditionalProperties *Type            `json:"additionalProperties,omitempty"` // Section 5.18.
	Enum                 []any            `json:"enum,omitempty"`                 // Section 5.20.
	Type                 TypeList         `json:"type,omitempty"`                 // Section 5.21.
	Const                any              `json:"const,omitempty"`
	// ConstIsSet distinguishes an explicit `"const": null` (which decodes to
	// Const == nil) from an absent `const` field (which also decodes to nil).
	// Set by Type's custom UnmarshalJSON via a separate raw-key probe.
	ConstIsSet bool `json:"-"`
	// MaxLengthIsSet, MaxItemsIsSet and MaxPropertiesIsSet tell a maximum of
	// 0, which the document declares, from an absent one. Set by Type's custom
	// UnmarshalJSON; see HasMaxLength.
	MaxLengthIsSet     bool `json:"-"`
	MaxItemsIsSet      bool `json:"-"`
	MaxPropertiesIsSet bool `json:"-"`
	// ConstExact is Const with its numbers as written, as json.Number. Const
	// holds them as float64, which rounds an integer beyond 2^53. Set by the
	// same probe.
	ConstExact any `json:"-"`
	// RFC draft-bhutton-json-schema-01, section 10.
	AllOf []*Type `json:"allOf,omitempty"` // Section 10.2.1.1.
	AnyOf []*Type `json:"anyOf,omitempty"` // Section 10.2.1.2.
	OneOf []*Type `json:"oneOf,omitempty"` // Section 10.2.1.3.
	Not   *Type   `json:"not,omitempty"`   // Section 10.2.1.4.
	// Conditional subschemas (RFC draft-bhutton-json-schema-01, section 10.2.2).
	// Preserved here so generators can detect their presence and warn or
	// compile them rather than silently dropping the keywords during JSON
	// parsing. The legacy generator does not interpret these by default;
	// downstream code that wants to honor conditional subschemas should
	// detect (If != nil) and route accordingly.
	If   *Type `json:"if,omitempty"`   // Section 10.2.2.1.
	Then *Type `json:"then,omitempty"` // Section 10.2.2.2.
	Else *Type `json:"else,omitempty"` // Section 10.2.2.3.
	// RFC draft-wright-json-schema-validation-00, section 6, 7.
	Title       string `json:"title,omitempty"`       // Section 6.1.
	Description string `json:"description,omitempty"` // Section 6.1.
	Default     any    `json:"default,omitempty"`     // Section 6.2.
	Format      string `json:"format,omitempty"`      // Section 7.
	// RFC draft-wright-json-schema-hyperschema-00, section 4.
	Media          *Type  `json:"media,omitempty"`          // Section 4.3.
	BinaryEncoding string `json:"binaryEncoding,omitempty"` // Section 4.3.
	// RFC draft-handrews-json-schema-validation-02, section 6.
	DependentRequired map[string][]string `json:"dependentRequired,omitempty"` // Section 6.5.4.
	// RFC draft-handrews-json-schema-validation-02, appendix A.
	Definitions      Definitions      `json:"$defs,omitempty"`
	DependentSchemas map[string]*Type `json:"dependentSchemas,omitempty"`

	// Keywords of draft 06 and later. minContains and maxContains are counts,
	// held as numbers so that one written as 2.0 parses, as JSON Schema counts
	// it as an integer.
	PropertyNames         *Type    `json:"propertyNames,omitempty"`
	Contains              *Type    `json:"contains,omitempty"`
	MinContains           *float64 `json:"minContains,omitempty"`
	MaxContains           *float64 `json:"maxContains,omitempty"`
	UnevaluatedProperties *Type    `json:"unevaluatedProperties,omitempty"`
	UnevaluatedItems      *Type    `json:"unevaluatedItems,omitempty"`

	// TODO: add correct section where "readOnly" is mentioned in the spec
	//       I'm not sure which section I should put here, but I did notice in the 2020-12 validation schema changelog,
	//       under the "draft-handrews-json-schema-validation-00" item it mentions "readOnly" as having been moved
	//       from hyper-schema to validation meta-data...
	ReadOnly bool `json:"readOnly,omitempty"`

	// Deprecated is the meta-data keyword of draft 2019-09, section 9.3,
	// read in every draft: the instance may be removed in the future.
	Deprecated Deprecation `json:"deprecated,omitzero"`

	// ExtGoCustomType is the name of a (qualified or not) custom Go type
	// to use for the field.
	GoJSONSchemaExtension *GoJSONSchemaExtension `json:"goJSONSchema,omitempty"` //nolint:tagliatelle // breaking change

	// XEnumVarnames names the Go constants generated for `enum`, one per
	// value in declaration order. It is an OpenAPI-ecosystem extension
	// rather than a JSON Schema keyword, and is modelled as a typed field
	// rather than through a generic extension map because it changes
	// generated identifiers and so has to be validated, not passed through.
	XEnumVarnames []string `json:"x-enum-varnames,omitempty"` //nolint:tagliatelle // name fixed by the OpenAPI ecosystem

	// Extensions holds every `x-`-prefixed keyword declared alongside this
	// schema, kept verbatim. JSON Schema reserves nothing under that
	// prefix, and the OpenAPI ecosystem uses it for vendor data that would
	// otherwise be dropped on the floor by a generator that models only the
	// keywords it knows. Populated by UnmarshalJSON, not by a struct tag,
	// since the keys are open-ended.
	Extensions map[string]any `json:"-"`

	// SubSchemaType marks the type as being a subschema type.
	subSchemaType     SubSchemaType `json:"-"`
	subSchemasCount   int           `json:"-"`
	subSchemaTypeElem bool          `json:"-"`

	// Flags.
	Dereferenced bool `json:"-"` // Marks that his type has been dereferenced.
}

func (value *Type) IsEmptyObject() bool {
	return len(value.Properties) == 0 && value.AdditionalProperties == nil
}

func (value *Type) SetSubSchemaType(sst SubSchemaType) {
	value.subSchemaType = sst
}

func (value *Type) GetSubSchemaType() SubSchemaType {
	return value.subSchemaType
}

func (value *Type) SetSubSchemasCount(ssc int) {
	value.subSchemasCount = ssc
}

func (value *Type) GetSubSchemasCount() int {
	return value.subSchemasCount
}

func (value *Type) IsSubSchemaTypeElem() bool {
	return value.subSchemaTypeElem
}

func (value *Type) SetSubSchemaTypeElem() {
	value.subSchemaTypeElem = true
}

func (value *Type) ConvertAllRefs(absolutePath string) error {
	val := reflect.ValueOf(value).Elem()

	return updateAllRefsValues(&val, absolutePath)
}

// isJSONArray reports whether raw is a JSON array, ignoring leading
// whitespace. Used to tell draft-07 `dependencies` property dependencies
// (arrays of property names) from schema dependencies without relying on a
// speculative decode.
func isJSONArray(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			return true
		default:
			return false
		}
	}

	return false
}

// splitTupleItems detects the draft-07 tuple form of `items` (an array of
// schemas). When present it returns raw with the `items` key removed plus the
// raw array; otherwise it returns raw unchanged and a nil tuple. The map
// round-trip is only paid when a tuple is actually present.
//
// Returns (effective, tuple, error): `effective` is what should be decoded into
// ObjectAsType, `tuple` is the raw `items` array or nil.
func splitTupleItems(raw []byte) ([]byte, []byte, error) {
	if !isJSONObject(raw) {
		return raw, nil, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		// Let the regular decode below report the error in context.
		return raw, nil, nil //nolint:nilerr // deliberate: defer to the main decode
	}

	items, ok := fields["items"]
	if !ok {
		return raw, nil, nil
	}

	// `null` is not a schema. Left to the regular decode it lands in Items as
	// nil, which is indistinguishable from an absent `items` — so an invalid
	// schema would silently generate an unconstrained array. Rejected here for
	// the same reason `items: [null]` and `dependencies: null` are.
	if isJSONNull(items) {
		return nil, nil, fmt.Errorf("items: %w", ErrNullNotASchema)
	}

	if !isJSONArray(items) {
		return raw, nil, nil
	}

	delete(fields, "items")

	rest, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to re-encode type without tuple items: %w", err)
	}

	return rest, items, nil
}

// isJSONNull reports whether raw is the JSON literal null, ignoring
// surrounding whitespace.
func isJSONNull(raw []byte) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// readCounts returns raw, a schema object, with each count keyword written
// with a fraction or an exponent, such as `"minItems": 2.0`, written as the
// integer it holds, which JSON Schema counts it as, and the count keywords
// that raw declares a number for. A count that holds no integer is an error
// that names its keyword. One that is no number is left to the decode of raw,
// which rejects it as before.
func readCounts(raw []byte) ([]byte, map[string]bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw, nil, nil //nolint:nilerr // deliberate: defer to the main decode
	}

	declared := map[string]bool{}
	changed := false

	for _, key := range []string{"maxLength", "minLength", "maxItems", "minItems", "maxProperties", "minProperties"} {
		value, ok := fields[key]
		text := string(bytes.TrimSpace(value))

		if !ok || text == "" || text == "null" || text[0] == '"' {
			continue
		}

		declared[key] = true

		if !strings.ContainsAny(text, ".eE") {
			continue
		}

		// A count beyond 2^53 is no count a schema means.
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
			return nil, nil, fmt.Errorf("%s: %w: %s", key, ErrCountNotInteger, text)
		}

		fields[key] = json.RawMessage(strconv.FormatInt(int64(f), 10))
		changed = true
	}

	if !changed {
		return raw, declared, nil
	}

	written, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write counts: %w", err)
	}

	return written, declared, nil
}

// HasMaxLength reports whether t declares a maxLength, 0 included.
func (t *Type) HasMaxLength() bool {
	return t.MaxLengthIsSet || t.MaxLength != 0
}

// HasMaxItems reports whether t declares a maxItems, 0 included.
func (t *Type) HasMaxItems() bool {
	return t.MaxItemsIsSet || t.MaxItems != 0
}

// HasMaxProperties reports whether t declares a maxProperties, 0 included.
func (t *Type) HasMaxProperties() bool {
	return t.MaxPropertiesIsSet || t.MaxProperties != 0
}

// isJSONBoolean reports whether raw is the JSON literal true or false.
func isJSONBoolean(raw []byte) bool {
	s := string(bytes.TrimSpace(raw))

	return s == "true" || s == "false"
}

// nullSubschema names the first composition entry, property, pattern
// property, dependent schema or definition of obj that is null, or returns ""
// when there is none. Unlike `items` and `dependencies`, these never reach a
// decoder hook: encoding/json leaves a null list entry or map value as a nil
// *Type, which the generator crashed on or silently ignored.
// legacyDefinitions are the draft-07 `definitions`, not yet merged into obj.
func nullSubschema(obj *ObjectAsType, legacyDefinitions Definitions) string {
	for _, list := range []struct {
		keyword string
		members []*Type
	}{
		{"allOf", obj.AllOf},
		{"anyOf", obj.AnyOf},
		{"oneOf", obj.OneOf},
	} {
		if i := slices.Index(list.members, nil); i >= 0 {
			return fmt.Sprintf("%s[%d]", list.keyword, i)
		}
	}

	for _, set := range []struct {
		keyword string
		members map[string]*Type
	}{
		{"properties", obj.Properties},
		{"patternProperties", obj.PatternProperties},
		{"dependentSchemas", obj.DependentSchemas},
		{"$defs", obj.Definitions},
		{"definitions", legacyDefinitions},
	} {
		for _, name := range slices.Sorted(maps.Keys(set.members)) {
			if set.members[name] == nil {
				return fmt.Sprintf("%s[%q]", set.keyword, name)
			}
		}
	}

	return ""
}

// isJSONObject reports whether raw is a JSON object, ignoring leading
// whitespace.
func isJSONObject(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			return true
		default:
			return false
		}
	}

	return false
}

// UnmarshalJSON accepts booleans as schemas where `true` is equivalent to `{}`
// and `false` is equivalent to `{"not": {}}`.
func (value *Type) UnmarshalJSON(raw []byte) error {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			*value = Type{}
		} else {
			*value = Type{Not: &Type{}}
		}

		return nil
	}

	// `items` is dual-form in draft-07: a single schema, or a tuple (array)
	// of schemas positionally matched against the array. ObjectAsType models
	// only the single-schema form, so lift a tuple out before decoding and
	// stash it on TupleItems, otherwise the array fails the whole parse.
	effectiveRaw, tupleRaw, err := splitTupleItems(raw)
	if err != nil {
		return err
	}

	effectiveRaw, counts, err := readCounts(effectiveRaw)
	if err != nil {
		return err
	}

	var obj ObjectAsType
	if err := json.Unmarshal(effectiveRaw, &obj); err != nil {
		return fmt.Errorf("failed to unmarshal type: %w", err)
	}

	obj.MaxLengthIsSet = counts["maxLength"]
	obj.MaxItemsIsSet = counts["maxItems"]
	obj.MaxPropertiesIsSet = counts["maxProperties"]

	if tupleRaw != nil {
		var tuple []*Type
		if err := json.Unmarshal(tupleRaw, &tuple); err != nil {
			return fmt.Errorf("failed to unmarshal tuple items: %w", err)
		}

		for i, member := range tuple {
			if member == nil {
				return fmt.Errorf("items[%d]: %w", i, ErrNullNotASchema)
			}
		}

		obj.TupleItems = tuple
	}

	// Take care of legacy fields from older RFC versions.
	legacyObj := struct {
		// RFC draft-wright-json-schema-validation-00, section 5.
		// draft-07 `dependencies` is dual-form: each value is either a
		// schema (a schema dependency) or an array of property names (a
		// property dependency, split out as `dependentRequired` in
		// 2019-09). Decode lazily so the array form does not fail the
		// whole parse.
		Dependencies map[string]json.RawMessage `json:"dependencies,omitempty"`
		Definitions  Definitions                `json:"definitions,omitempty"` // Section 5.26.
	}{}
	if err := json.Unmarshal(raw, &legacyObj); err != nil {
		return fmt.Errorf("failed to unmarshal type: %w", err)
	}

	// A null where a schema belongs is rejected, as for `items`. The draft-07
	// `definitions` are checked before they are merged, so the error names
	// the keyword the document used.
	if at := nullSubschema(&obj, legacyObj.Definitions); at != "" {
		return fmt.Errorf("%s: %w", at, ErrNullNotASchema)
	}

	var (
		legacyDependentSchemas  map[string]*Type
		legacyDependentRequired map[string][]string
	)

	for name, rawDep := range legacyObj.Dependencies {
		// Dispatch on the JSON shape rather than on whether a []string
		// decode happens to succeed: `null` unmarshals into []string
		// without error (yielding nil), and `[null]` yields [""], so a
		// try-and-fall-back approach would misclassify both instead of
		// leaving them to the schema branch / rejecting them.
		if isJSONArray(rawDep) {
			var members []*string
			if err := json.Unmarshal(rawDep, &members); err != nil {
				return fmt.Errorf("failed to unmarshal dependencies[%q]: %w", name, err)
			}

			required := make([]string, 0, len(members))

			for i, member := range members {
				if member == nil {
					return fmt.Errorf("dependencies[%q][%d]: %w", name, i, ErrNullPropertyDependency)
				}

				required = append(required, *member)
			}

			if legacyDependentRequired == nil {
				legacyDependentRequired = map[string][]string{}
			}

			legacyDependentRequired[name] = required

			continue
		}

		// Reject an explicit null before decoding: Type.UnmarshalJSON
		// probes for a bool first, and `null` unmarshals into a bool
		// without error (leaving false), so a null would silently become
		// the "false schema" that rejects every instance.
		if isJSONNull(rawDep) {
			return fmt.Errorf("dependencies[%q]: %w", name, ErrNullNotASchema)
		}

		var dep Type
		if err := json.Unmarshal(rawDep, &dep); err != nil {
			return fmt.Errorf("failed to unmarshal dependencies[%q]: %w", name, err)
		}

		if legacyDependentSchemas == nil {
			legacyDependentSchemas = map[string]*Type{}
		}

		legacyDependentSchemas[name] = &dep
	}

	if legacyObj.Definitions != nil && obj.Definitions == nil {
		obj.Definitions = legacyObj.Definitions
	}

	if legacyDependentSchemas != nil && obj.DependentSchemas == nil {
		obj.DependentSchemas = legacyDependentSchemas
	}

	if legacyDependentRequired != nil && obj.DependentRequired == nil {
		obj.DependentRequired = legacyDependentRequired
	}

	if len(obj.Type) == 0 && (len(obj.Properties) > 0 || obj.AdditionalProperties != nil) {
		obj.Type = TypeList{"object"}
	}

	obj.Extensions = extractExtensions(raw)

	*value = Type(obj)

	value.ConstIsSet, value.ConstExact = probeConst(raw)
	value.probeNumbers(raw)

	return nil
}

// probeNumbers sets the numeric keywords that raw, a schema object, declares,
// as written: multipleOf and the bounds. One that is absent, a draft-04
// boolean or anything else but a number is left "".
func (value *Type) probeNumbers(raw []byte) {
	var probe struct {
		MultipleOf       json.RawMessage `json:"multipleOf"`
		Maximum          json.RawMessage `json:"maximum"`
		ExclusiveMaximum json.RawMessage `json:"exclusiveMaximum"`
		Minimum          json.RawMessage `json:"minimum"`
		ExclusiveMinimum json.RawMessage `json:"exclusiveMinimum"`
	}

	if json.Unmarshal(raw, &probe) != nil {
		return
	}

	number := func(raw json.RawMessage) json.Number {
		var n json.Number
		if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &n) != nil {
			return ""
		}

		return n
	}

	value.MultipleOfExact = number(probe.MultipleOf)
	value.MaximumExact = number(probe.Maximum)
	value.ExclusiveMaximumExact = number(probe.ExclusiveMaximum)
	value.MinimumExact = number(probe.Minimum)
	value.ExclusiveMinimumExact = number(probe.ExclusiveMinimum)
}

// probeConst probes raw, a schema object, for an explicit "const" key so
// callers can distinguish absent (Const == nil, ConstIsSet == false) from
// explicit null (Const == nil, ConstIsSet == true), and returns its value with
// its numbers exact. Failure to parse here is non-fatal — the standard
// unmarshal has already succeeded, so a probe error just leaves both at their
// zero values.
func probeConst(raw []byte) (bool, any) {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return false, nil
	}

	constRaw, ok := probe["const"]
	if !ok {
		return false, nil
	}

	var exact any

	dec := json.NewDecoder(bytes.NewReader(constRaw))
	dec.UseNumber()

	_ = dec.Decode(&exact)

	return true, exact
}

// extractExtensions collects the `x-`-prefixed keywords declared on a schema
// object. Errors are not surfaced: anything that failed to decode as an object
// has already been rejected by the caller, and a schema is never invalid for
// the sake of a vendor extension.
func extractExtensions(raw []byte) map[string]any {
	// UseNumber rather than a plain Unmarshal: decoding into `any` turns
	// every JSON number into a float64, which silently rounds anything past
	// 2^53. An extension value is carried through to generated output
	// verbatim, so 9007199254740993 must not become ...992 on the way.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return nil
	}

	var extensions map[string]any

	for key, val := range fields {
		if !strings.HasPrefix(key, extensionPrefix) {
			continue
		}

		if extensions == nil {
			extensions = map[string]any{}
		}

		extensions[key] = val
	}

	return extensions
}

func AllOf(types []*Type, baseType *Type) (*Type, error) {
	typ, err := MergeTypes(types, baseType)
	if err != nil {
		return nil, err
	}

	typ.subSchemaType = SubSchemaTypeAllOf

	return typ, nil
}

// AnyOf merges the branches of an anyOf and the schema it is declared on into
// the schema of the struct that holds an instance. The struct holds what any
// branch can, but binds every instance, so it has every property, and requires
// only what the parent requires and what every branch requires, at every depth
// of the properties the parent declares; see anyOfProperty. A property that
// only branches declare is the first such branch's; see anyOfBranchProperty.
// The merge works on copies, so it changes neither the parent nor a branch.
// MergeTypes would write the branches' keywords into the schemas of the
// parent's properties, and so bind every instance to them.
func AnyOf(types []*Type, baseType *Type) (*Type, error) {
	if len(types) == 0 {
		return nil, ErrEmptyTypesList
	}

	if isPrimitiveTypeList(types, nil) {
		return MergeTypes(types, baseType)
	}

	// The properties are merged on their own, below.
	withoutProperties := func(t *Type) *Type {
		c := *t
		c.Properties = nil

		return &c
	}

	branches := make([]*Type, len(types))
	for i, t := range types {
		branches[i] = withoutProperties(t)
	}

	typ, err := mergeIsolated(branches, withoutProperties(baseType))
	if err != nil {
		return nil, err
	}

	if properties := anyOfProperties(types, baseType); len(properties) > 0 {
		typ.Properties = properties
	}

	typ.Required = mergeRequiredUnion(types, baseType)
	typ.subSchemaType = SubSchemaTypeAnyOf
	typ.subSchemasCount = len(types)

	return typ, nil
}

// anyOfProperties returns the properties of the struct that an anyOf merges
// into: each that the parent declares, as anyOfProperty gives it, and each
// that only branches declare, as anyOfBranchProperty does.
func anyOfProperties(types []*Type, baseType *Type) map[string]*Type {
	properties := map[string]*Type{}

	for name, p := range baseType.Properties {
		properties[name] = anyOfProperty(p, declarations(types, name), len(types), nil)
	}

	for _, t := range types {
		for name := range t.Properties {
			if _, ok := properties[name]; !ok {
				properties[name] = anyOfBranchProperty(declarations(types, name), len(types), nil)
			}
		}
	}

	return properties
}

// anyOfProperty returns the schema of a property that the parent of an anyOf
// declares, in the struct that the anyOf merges into: the parent's, whose
// keywords bind every instance, as mergedProperty makes it. declared are the
// schemas that branches declare for the property, of branchCount branches,
// and merging the merges of the properties that hold it.
func anyOfProperty(parent *Type, declared []*Type, branchCount int, merging []propertyMerge) *Type {
	if parent == nil || len(declared) == 0 || parent.Ref != "" {
		return parent
	}

	return mergedProperty(parent, declared, branchCount, true, merging)
}

// anyOfBranchProperty returns the schema of a property that only branches of
// an anyOf declare, in the struct that the anyOf merges into. Declared by one
// branch, it is that branch's schema, which the branch's own type was made
// from. Declared by more, it is the first of them, as mergedProperty makes it:
// it requires what the first requires, so that where it holds nothing more,
// the type declared for the first is its type. A document that another branch
// accepts, with a value there that the first refuses, is rejected, as the
// README describes.
func anyOfBranchProperty(declared []*Type, branchCount int, merging []propertyMerge) *Type {
	first := declared[0]
	if len(declared) == 1 || first.Ref != "" {
		return first
	}

	return mergedProperty(first, declared, branchCount, false, merging)
}

// propertyMerge is a call of mergedProperty.
type propertyMerge struct {
	base     *Type
	declared []*Type
	binds    bool
}

func (m propertyMerge) equal(other propertyMerge) bool {
	return m.base == other.base && m.binds == other.binds && slices.Equal(m.declared, other.declared)
}

// mergedProperty returns base made to hold the properties that declared
// declare in it as well, at every depth, and to require what base requires and
// what every one of branchCount branches requires. binds tells whether base is
// the parent's, whose keywords bind every instance, or the first of declared.
// A branch's other keywords on the property stay with the branch's own type.
// Where that changes nothing, it is base itself, so that the type already
// declared for base is the property's type here as well. merging are the
// merges of the properties that hold it.
func mergedProperty(base *Type, declared []*Type, branchCount int, binds bool, merging []propertyMerge) *Type {
	// A schema can hold itself, as one that the merge of a recursive allOf
	// has written into can. A merge that comes back to itself would never
	// end, so base stays as it is there.
	call := propertyMerge{base: base, declared: declared, binds: binds}
	if slices.ContainsFunc(merging, call.equal) {
		return base
	}

	merging = append(merging, call)

	var properties map[string]*Type

	changed := false

	for name, p := range base.Properties {
		if properties == nil {
			properties = map[string]*Type{}
		}

		if binds {
			properties[name] = anyOfProperty(p, declarations(declared, name), branchCount, merging)
		} else {
			properties[name] = anyOfBranchProperty(declarations(declared, name), branchCount, merging)
		}

		changed = changed || properties[name] != p
	}

	for _, d := range declared {
		for name := range d.Properties {
			if _, ok := properties[name]; ok {
				continue
			}

			if properties == nil {
				properties = map[string]*Type{}
			}

			properties[name] = anyOfBranchProperty(declarations(declared, name), branchCount, merging)
			changed = true
		}
	}

	// A branch that doesn't declare the property requires nothing in it.
	required := slices.Clone(base.Required)
	if len(declared) == branchCount {
		for _, r := range declared[0].Required {
			if !slices.Contains(required, r) &&
				!slices.ContainsFunc(declared, func(d *Type) bool { return !slices.Contains(d.Required, r) }) {
				required = append(required, r)
			}
		}
	}

	if !changed && slices.Equal(required, base.Required) {
		return base
	}

	merged := *base
	merged.Properties = properties
	merged.Required = required

	if len(merged.Type) == 0 && len(merged.Properties) > 0 {
		merged.Type = TypeList{TypeNameObject}
	}

	// Where base gives the property no title, description or deprecation,
	// the first branch that does names and documents it.
	for _, d := range declared {
		if merged.Title == "" {
			merged.Title = d.Title
		}

		if merged.Description == "" {
			merged.Description = d.Description
		}

		if !merged.Deprecated.Marked {
			merged.Deprecated = d.Deprecated
		}
	}

	return &merged
}

// declarations returns the schemas that types declare for the property name.
func declarations(types []*Type, name string) []*Type {
	var declared []*Type

	for _, t := range types {
		if p, ok := t.Properties[name]; ok && p != nil {
			declared = append(declared, p)
		}
	}

	return declared
}

// mergeIsolated merges as MergeTypes does, but into copies: before each
// schema is merged in, the result gets its own copy of whatever that schema
// holds as well, at every depth, so that the merge writes into nothing that
// the schemas share.
func mergeIsolated(types []*Type, baseType *Type) (*Type, error) {
	result := &Type{}

	opts := []func(*mergo.Config){
		mergo.WithAppendSlice,
		mergo.WithTransformers(typeListTransformer{}),
	}

	for _, t := range append([]*Type{baseType}, types...) {
		isolate(reflect.ValueOf(result).Elem(), reflect.ValueOf(t).Elem())

		if err := mergo.Merge(result, t, opts...); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCannotMergeTypes, err)
		}
	}

	return result, nil
}

// isolate gives dst a copy of each map, slice and pointer that src holds as
// well, and of the values in them, at every depth at which both hold one, so
// that a merge of src into dst writes into the copies alone.
func isolate(dst, src reflect.Value) {
	isolateIn(dst, src, map[isolation]reflect.Value{})
}

// isolation is a map or a pointer that dst holds, with the one that src holds
// in its place.
type isolation struct {
	typ      reflect.Type
	dst, src uintptr
}

// isolateIn is isolate within the maps and pointers whose copies copying
// holds. A schema can hold itself, as one that the merge of a recursive allOf
// has written into can. Where dst comes back to a map or a pointer it is
// copying, for the same one of src, it gets that copy, which then holds itself
// as the schema does, and which the merge then writes into once.
func isolateIn(dst, src reflect.Value, copying map[isolation]reflect.Value) {
	// A merge writes src into dst only where both have the same type, such as
	// two JSON objects, and keeps dst where they differ.
	if !src.IsValid() || dst.Type() != src.Type() {
		return
	}

	switch dst.Kind() { //nolint:exhaustive // Other kinds are values, which a merge sets, not writes into.
	case reflect.Struct:
		for i := range dst.NumField() {
			if dst.Field(i).CanSet() {
				isolateIn(dst.Field(i), src.Field(i), copying)
			}
		}

	case reflect.Pointer:
		if dst.IsNil() || src.IsNil() {
			return
		}

		key := isolation{typ: dst.Type(), dst: dst.Pointer(), src: src.Pointer()}
		if c, ok := copying[key]; ok {
			dst.Set(c)

			return
		}

		c := reflect.New(dst.Elem().Type())
		c.Elem().Set(dst.Elem())
		dst.Set(c)

		copying[key] = c
		defer delete(copying, key)

		isolateIn(dst.Elem(), src.Elem(), copying)

	case reflect.Map:
		if dst.IsNil() || src.IsNil() {
			return
		}

		key := isolation{typ: dst.Type(), dst: dst.Pointer(), src: src.Pointer()}
		if c, ok := copying[key]; ok {
			dst.Set(c)

			return
		}

		c := reflect.MakeMapWithSize(dst.Type(), dst.Len())

		copying[key] = c
		defer delete(copying, key)

		for it := dst.MapRange(); it.Next(); {
			value := reflect.New(it.Value().Type()).Elem()
			value.Set(it.Value())

			if s := src.MapIndex(it.Key()); s.IsValid() {
				isolateIn(value, s, copying)
			}

			c.SetMapIndex(it.Key(), value)
		}

		dst.Set(c)

	case reflect.Slice:
		if dst.Len() == 0 || src.Len() == 0 {
			return
		}

		c := reflect.MakeSlice(dst.Type(), dst.Len(), dst.Len())
		reflect.Copy(c, dst)
		dst.Set(c)

	case reflect.Interface:
		if dst.IsNil() || src.IsNil() {
			return
		}

		// A JSON value, such as a default, whose maps a merge writes into.
		value := reflect.New(dst.Elem().Type()).Elem()
		value.Set(dst.Elem())
		isolateIn(value, src.Elem(), copying)
		dst.Set(value)
	}
}

func MergeTypes(types []*Type, baseType *Type) (*Type, error) {
	if len(types) == 0 {
		return nil, ErrEmptyTypesList
	}

	result := &Type{}

	if isPrimitiveTypeList(types, result.Type) {
		return result, nil
	}

	opts := []func(*mergo.Config){
		mergo.WithAppendSlice,
		mergo.WithTransformers(typeListTransformer{}),
	}

	if err := mergo.Merge(result, baseType, opts...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCannotMergeTypes, err)
	}

	for _, t := range types {
		if err := mergo.Merge(result, t, opts...); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCannotMergeTypes, err)
		}
	}

	return result, nil
}

func mergeRequiredUnion(types []*Type, baseType *Type) []string {
	required := make([]string, len(baseType.Required))
	copy(required, baseType.Required)

	for _, r := range types[0].Required {
		valid := true

		for _, t := range types {
			if !slices.Contains(t.Required, r) {
				valid = false

				break
			}
		}

		if valid && !slices.Contains(required, r) {
			required = append(required, r) //nolint:makezero
		}
	}

	return required
}

func updateAllRefsValues(structValue *reflect.Value, refPath string) error {
	switch structValue.Kind() { //nolint:exhaustive
	case reflect.Struct:
		for i := range structValue.NumField() {
			field := structValue.Field(i)
			name := structValue.Type().Field(i).Name

			switch field.Kind() { //nolint:exhaustive
			case reflect.String:
				fieldVal := field.String()
				if name == "Ref" && fieldVal != "" && field.CanSet() {
					if strings.HasPrefix(fieldVal, "#") {
						field.SetString(refPath + fieldVal)
					}
				}

			default:
				if err := updateAllRefsValues(&field, refPath); err != nil {
					return fmt.Errorf("struct error: %w", err)
				}
			}
		}

	case reflect.Ptr: //nolint:govet // typical inlining false positive
		elem := structValue.Elem()
		if !structValue.IsNil() {
			if err := updateAllRefsValues(&elem, refPath); err != nil {
				return fmt.Errorf("ptr error: %w", err)
			}
		}

	case reflect.Map:
		for _, key := range structValue.MapKeys() {
			val := structValue.MapIndex(key)
			if err := updateAllRefsValues(&val, refPath); err != nil {
				return fmt.Errorf("map error: %w", err)
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range structValue.Len() {
			field := structValue.Index(i)
			if err := updateAllRefsValues(&field, refPath); err != nil {
				return fmt.Errorf("slice error: %w", err)
			}
		}
	}

	return nil
}

type typeListTransformer struct{}

func (t typeListTransformer) Transformer(typ reflect.Type) func(dst, src reflect.Value) error {
	if typ == reflect.TypeFor[TypeList]() {
		return func(dst, src reflect.Value) error {
			return nil
		}
	}

	return nil
}

type GoJSONSchemaExtension struct {
	Type       *string           `json:"type,omitempty"`
	Identifier *string           `json:"identifier,omitempty"`
	Nillable   bool              `json:"nillable,omitempty"`
	Pointer    *bool             `json:"pointer,omitempty"`
	Imports    []string          `json:"imports,omitempty"`
	ExtraTags  map[string]string `json:"extraTags,omitempty"`
}
