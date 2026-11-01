package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sosodev/duration"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/mathutils"
)

const typeInt = "int"

var (
	ErrDurationIsEmpty                = errors.New("duration default value must not be an empty string")
	ErrCannotConvertISO8601ToGoFormat = errors.New("could not convert duration from ISO8601 to Go format")
	ErrInvalidDefaultValue            = errors.New("invalid default value")
	ErrCannotFindSlideToDump          = errors.New("didn't find a slice to dump")
	ErrDefaultDurationIsNotAString    = errors.New("duration default value must be a string")
)

type validator interface {
	generate(out *codegen.Emitter, format string) error
	desc() *validatorDesc
}

type packageImport struct {
	qualifiedName string
}

type validatorDesc struct {
	hasError            bool
	beforeJSONUnmarshal bool
	requiresRawAfter    bool
	imports             []packageImport
	// decls lists package-level declarations the validator needs (e.g.
	// precompiled regex vars). The orchestrator adds them via
	// Package.AddDecl, which dedupes by name — so multiple validators sharing
	// the same regex emit the var only once per output file.
	decls []codegen.Decl

	// afterAdditionalProperties runs the validator once AdditionalProperties
	// has been decoded. The after validators run before that, while the map
	// is still empty, so a check on its values would check nothing.
	afterAdditionalProperties bool

	// beforeRawDecode runs the validator first, on the method's own input. It
	// decodes what it needs itself, so it also suits a type that is not an
	// object, which the raw map cannot hold.
	beforeRawDecode bool

	// requiresNumbers has the JSON body decode the input once more, into
	// `numbers`, with its numbers kept exact, which the raw map holds as
	// float64. The YAML body reads the raw map, whose integers are exact.
	requiresNumbers bool
}

var (
	_ validator = new(requiredValidator)
	_ validator = new(wrapperDefaultValidator)
	_ validator = new(readOnlyValidator)
	_ validator = new(nullTypeValidator)
	_ validator = new(nonNullValidator)
	_ validator = new(nonNullContainerValidator)
	_ validator = new(nonNullElementsValidator)
	_ validator = new(nonNullValueValidator)
	_ validator = new(defaultValidator)
	_ validator = new(arrayValidator)
	_ validator = new(stringValidator)
	_ validator = new(numericValidator)
	_ validator = new(anyOfValidator)
	_ validator = new(formatValidator)
	_ validator = new(strictFieldsValidator)
	_ validator = new(conditionalDiscriminatorValidator)
	_ validator = new(presenceCountValidator)
	_ validator = new(elementsValidator)
	_ validator = new(untypedConstValidator)
	_ validator = new(uniqueItemsValidator)

	ErrCannotDumpDefaultSlice = errors.New("cannot dump default slice")
)

type requiredValidator struct {
	jsonName string
	declName string
}

func (v *requiredValidator) generate(out *codegen.Emitter, format string) error {
	// The container itself may be null (if the type is ["null", "object"]), in which case
	// the map will be nil and none of the properties are present. This shouldn't fail
	// the validation, though, as that's allowed as long as the container is allowed to be null.
	out.Printlnf(`if _, ok := %s[%q]; %s != nil && !ok {`, varNameRawMap, v.jsonName, varNameRawMap)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %s in %s: required")`, goStringText(v.jsonName), goStringText(v.declName))
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *requiredValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

// nonNullValidator rejects a property that is present with an explicit null
// where the schema constrains its type to something other than null.
//
// This is a `type` assertion, not a `required` one. Per draft-07 §6.5.3,
// `required` tests presence by name — `{"x": null}` satisfies it — and keywords
// are vacuously true for instances of a type they do not target. So the null is
// invalid only because `type` says so, and only when `type` is actually
// declared and excludes null.
//
// Go cannot tell omitted from present-but-null after decoding (both leave the
// zero value), hence the check against the raw map before unmarshalling.
type nonNullValidator struct {
	jsonName string
	declName string
	// caseVariants are the other properties whose names differ from jsonName
	// only in case. encoding/json gives a key spelled exactly like one of them
	// to that property, never to this one.
	caseVariants []string
	// exactOnly is set when a case variant comes earlier in the struct:
	// encoding/json then hands every inexact match to that earlier field, so
	// only the exact key is ever this field's.
	exactOnly bool
}

func (v *nonNullValidator) generate(out *codegen.Emitter, format string) error {
	// Where the payload carries two differently-cased spellings and one is
	// null, this rejects it. encoding/json is no more specific for that
	// input, and refusing an ambiguous payload is the safer answer for a
	// validation flag.
	emitRawKeyMatch(out, format, v.jsonName, v.caseVariants, v.exactOnly, true)

	out.Printlnf(
		`return fmt.Errorf("field %s in %s: must not be null")`,
		goStringText(v.jsonName), goStringText(v.declName),
	)
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *nonNullValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}

	// `strings` is only reached by the JSON branch, but the descriptor is
	// format-agnostic and an unused import would not compile — the JSON
	// unmarshaler is always generated alongside the YAML one. An exact-only
	// check uses the plain map lookup in both branches, so it needs none.
	if !v.exactOnly {
		d.imports = []packageImport{{qualifiedName: "strings"}}
	}

	return d
}

// fieldCaseVariants returns, for field i of fields, the JSON names of the other
// fields that equal its own regardless of case, and whether only its exact
// name binds to it: encoding/json binds a key to the field with exactly that
// name, and only otherwise to the first field whose name matches it regardless
// of case. They are emitRawKeyMatch's caseVariants and exactOnly.
func fieldCaseVariants(fields []codegen.StructField, i int) ([]string, bool) {
	var (
		caseVariants []string
		exactOnly    bool
	)

	for j, other := range fields {
		if j == i || !strings.EqualFold(other.JSONName, fields[i].JSONName) {
			continue
		}

		caseVariants = append(caseVariants, other.JSONName)
		exactOnly = exactOnly || j < i
	}

	return caseVariants, exactOnly
}

// rawKeyMatch is how a check of a property's raw-map entry finds the entries
// the decoder assigns to the property, as for emitRawKeyMatch. A check that
// reads the raw map takes it from the struct's fields once they are known.
type rawKeyMatch struct {
	caseVariants []string
	exactOnly    bool
}

// matchKeysOf sets the key matching of the property jsonName of a struct with
// fields.
func (m *rawKeyMatch) matchKeysOf(fields []codegen.StructField, jsonName string) {
	for i, f := range fields {
		if f.JSONName == jsonName {
			m.caseVariants, m.exactOnly = fieldCaseVariants(fields, i)

			return
		}
	}
}

// imports returns the imports emitRawKeyMatch needs: strings, unless only the
// exact name is matched.
func (m *rawKeyMatch) imports() []packageImport {
	if m.exactOnly {
		return nil
	}

	return []packageImport{{qualifiedName: "strings"}}
}

// rawKeyedValidator is a check of a property's raw-map entry.
type rawKeyedValidator interface {
	validator
	matchStructKeys(fields []codegen.StructField)
}

// emitRawKeyMatch opens a block that runs for the raw-map entry the decoder
// assigns to the property jsonName, with the entry's value in fieldValue; with
// nullOnly, only when that value is null. The caller emits the body, then
// closes the block with out.Indent(-1) and "}". caseVariants and exactOnly are
// as for nonNullValidator.
//
// The key-matching rule has to be the decoder's own, or the check
// disagrees with what was actually assigned.
//
// encoding/json matches a JSON key to a struct field without regard to
// case, so an exact lookup of `age` misses `{"Age": null}` — which the
// decode still assigns, leaving the check that exists to reject it unfired.
//
// yaml.v3 matches case-sensitively, so the same leniency there would
// reject `Age: null` when the field was never assigned at all and the
// document may well be valid.
func emitRawKeyMatch(
	out *codegen.Emitter,
	format, jsonName string,
	caseVariants []string,
	exactOnly, nullOnly bool,
) {
	emitMapKeyMatch(out, varNameRawMap, format, jsonName, caseVariants, exactOnly, nullOnly)
}

// emitMapKeyMatch is emitRawKeyMatch for the entries of the map that mapExpr
// holds, decoded as the raw map is.
func emitMapKeyMatch(
	out *codegen.Emitter,
	mapExpr, format, jsonName string,
	caseVariants []string,
	exactOnly, nullOnly bool,
) {
	if format != formatJSON || exactOnly {
		cond := "ok"
		if nullOnly {
			cond = "ok && fieldValue == nil"
		}

		out.Printlnf(`if fieldValue, ok := %s[%q]; %s {`, mapExpr, jsonName, cond)
		out.Indent(1)

		return
	}

	// `fieldValue` is deliberately not named `value`: that is the
	// UnmarshalJSON parameter, and shadowing it reads as a bug.
	out.Printlnf(`for fieldName, fieldValue := range %s {`, mapExpr)
	out.Indent(1)

	if nullOnly {
		out.Printlnf(`if fieldValue != nil || !strings.EqualFold(fieldName, %q) {`, jsonName)
	} else {
		out.Printlnf(`if !strings.EqualFold(fieldName, %q) {`, jsonName)
	}

	out.Indent(1)
	out.Printlnf("continue")
	out.Indent(-1)
	out.Printlnf("}")

	if len(caseVariants) > 0 {
		quoted := make([]string, len(caseVariants))
		for i, name := range caseVariants {
			quoted[i] = fmt.Sprintf("%q", name)
		}

		out.Printlnf("switch fieldName {")
		out.Printlnf("case %s:", strings.Join(quoted, ", "))
		out.Indent(1)
		out.Printlnf("continue")
		out.Indent(-1)
		out.Printlnf("}")
	}
}

// nonNullElementsValidator rejects an explicit null among the elements of an
// array property, at each level of nesting whose items exclude null, as
// nonNullValidator does for the property itself: decoding the elements into
// their Go type would turn the null into a zero value. It matches the
// property's key the way nonNullValidator does.
type nonNullElementsValidator struct {
	jsonName string
	declName string
	// forbidden holds, for each level of nesting, whether its elements must
	// not be null. The walk ends at its last level, which always forbids it.
	forbidden    []bool
	caseVariants []string
	exactOnly    bool
}

func (v *nonNullElementsValidator) generate(out *codegen.Emitter, format string) error {
	emitRawKeyMatch(out, format, v.jsonName, v.caseVariants, v.exactOnly, false)

	emitNullElementWalk(out, "fieldValue", v.forbidden, func(path string) string {
		return fmt.Sprintf(`fmt.Errorf("field %%s in %%s: must not be null", %s, %q)`, path, v.declName)
	}, v.jsonName)

	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *nonNullElementsValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}

	if !v.exactOnly {
		d.imports = []packageImport{{qualifiedName: "strings"}}
	}

	return d
}

// nonNullValueValidator rejects a null where a declared type that is not an
// object, such as a named primitive or array, excludes it, and a null among
// its array elements where those exclude it. Without it, encoding/json leaves
// a primitive at its zero value for a null and makes an array nil, with no
// error. yaml.v3 does not call UnmarshalYAML for a null node, so in YAML only
// the elements are checked here; a null property is the parent's to reject.
type nonNullValueValidator struct {
	declName string
	// excludesNull reports whether the value itself must not be null.
	excludesNull bool
	// forbidden is as for nonNullElementsValidator.
	forbidden []bool
}

func (v *nonNullValueValidator) generate(out *codegen.Emitter, format string) error {
	out.Printlnf("var rawValue interface{}")

	if format == formatJSON {
		out.Printlnf("if err := json.Unmarshal(value, &rawValue); err != nil {")
	} else {
		out.Printlnf("if err := value.Decode(&rawValue); err != nil {")
	}

	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("unmarshal raw %s: %%w", err)`, v.declName)
	out.Indent(-1)
	out.Printlnf("}")

	if v.excludesNull {
		out.Printlnf("if rawValue == nil {")
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("%s: must not be null")`, v.declName)
		out.Indent(-1)
		out.Printlnf("}")
	}

	if len(v.forbidden) > 0 {
		emitNullElementWalk(out, "rawValue", v.forbidden, func(path string) string {
			return fmt.Sprintf(`fmt.Errorf("%%s: must not be null", %s)`, path)
		}, v.declName)
	}

	return nil
}

func (v *nonNullValueValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:        true,
		beforeRawDecode: true,
	}
}

// emitNullElementWalk emits a walk of value as nested arrays, one level per
// entry of forbidden, returning errorFor(path) for a null element at a level
// that forbids one. path is a Go expression naming the element, such as
// fmt.Sprintf("%s[%d][%d]", name, i0, i1).
func emitNullElementWalk(
	out *codegen.Emitter,
	value string,
	forbidden []bool,
	errorFor func(path string) string,
	name string,
) {
	indexes := make([]string, 0, len(forbidden))

	for level, nullForbidden := range forbidden {
		elements := fmt.Sprintf("elements%d", level)
		index := fmt.Sprintf("i%d", level)
		element := fmt.Sprintf("element%d", level)

		out.Printlnf(`if %s, ok := %s.([]interface{}); ok {`, elements, value)
		out.Indent(1)
		out.Printlnf(`for %s, %s := range %s {`, index, element, elements)
		out.Indent(1)

		indexes = append(indexes, index)

		if nullForbidden {
			path := fmt.Sprintf(`fmt.Sprintf("%%s%s", %q, %s)`,
				strings.Repeat("[%d]", len(indexes)), name, strings.Join(indexes, ", "))

			out.Printlnf("if %s == nil {", element)
			out.Indent(1)
			out.Printlnf("return %s", errorFor(path))
			out.Indent(-1)
			out.Printlnf("}")
		}

		value = element
	}

	for range forbidden {
		out.Indent(-1)
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
	}
}

// nonNullContainerValidator rejects a null instance where the schema's own
// `type` excludes null. The raw map decodes to nil for a `null` payload, which
// is indistinguishable from an empty object after unmarshalling.
//
// This is the container counterpart to nonNullValidator, and likewise a `type`
// assertion: for a null instance the object keywords (`required`,
// `minProperties`) are vacuously true, so `type` is the only thing that can
// reject it.
type nonNullContainerValidator struct {
	declName string
}

func (v *nonNullContainerValidator) generate(out *codegen.Emitter, format string) error {
	out.Printlnf(`if %s == nil {`, varNameRawMap)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("%s: must not be null")`, v.declName)
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *nonNullContainerValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

type readOnlyValidator struct {
	jsonName string
	declName string
}

func (v *readOnlyValidator) generate(out *codegen.Emitter, format string) error {
	// The container itself may be null (if the type is ["null", "object"]), in which case
	// the map will be nil and none of the properties are present. This shouldn't fail
	// the validation, though, as that's allowed as long as the container is allowed to be null.
	out.Printlnf(`if _, ok := %s[%q]; %s != nil && ok {`, varNameRawMap, v.jsonName, varNameRawMap)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %s in %s: read only")`, goStringText(v.jsonName), goStringText(v.declName))
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *readOnlyValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

type nullTypeValidator struct {
	jsonName   string
	fieldName  string
	arrayDepth int
}

func (v *nullTypeValidator) generate(out *codegen.Emitter, format string) error {
	value := getPlainName(v.fieldName)
	fieldName := v.jsonName

	indexes := make([]string, v.arrayDepth)

	for i := range v.arrayDepth {
		index := fmt.Sprintf("i%d", i)
		indexes[i] = index
		out.Printlnf(`for %s := range %s {`, index, value)
		value += fmt.Sprintf("[%s]", index)
		fieldName += "[%d]"

		out.Indent(1)
	}

	fieldName = fmt.Sprintf(`"%s"`, fieldName)
	if len(indexes) > 0 {
		fieldName = fmt.Sprintf(`fmt.Sprintf(%s, %s)`, fieldName, strings.Join(indexes, ", "))
	}

	out.Printlnf(`if %s != nil {`, value)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: must be null", %s)`, fieldName)
	out.Indent(-1)
	out.Printlnf("}")

	for range v.arrayDepth {
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

func (v *nullTypeValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: false,
		requiresRawAfter:    true,
	}
}

type defaultValidator struct {
	// useNumber writes the numbers of an untyped default as json.Number in
	// JSON, as --use-number decodes them.
	useNumber        bool
	jsonName         string
	fieldName        string
	defaultValueType codegen.Type
	defaultValue     any
	isPointer        bool
	// warner reports a default that is left out; warned keeps the report to
	// one, although the default is written once per format.
	warner func(string)
	warned bool
}

func (v *defaultValidator) generate(out *codegen.Emitter, format string) error {
	defaultValue, err := v.dumpDefaultValueAssignment(out, format)
	if err != nil {
		return fmt.Errorf("cannot generate default validator: %w", err)
	}

	if defaultValue == "" {
		return nil
	}

	out.Printlnf(`if v, ok := %s[%q]; !ok || v == nil {`, varNameRawMap, v.jsonName)
	out.Indent(1)
	out.Printlnf("%s", defaultValue)
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *defaultValidator) dumpDefaultValueAssignment(out *codegen.Emitter, format string) (any, error) {
	if _, ok := v.defaultValueType.(codegen.DurationType); ok {
		defaultDurationISO8601, ok := v.defaultValue.(string)

		if !ok {
			return nil, fmt.Errorf("%w: %T given", ErrDefaultDurationIsNotAString, v.defaultValue)
		}

		if defaultDurationISO8601 == "" {
			return nil, ErrDurationIsEmpty
		}

		duration, err := duration.Parse(defaultDurationISO8601)
		if err != nil {
			return nil, ErrCannotConvertISO8601ToGoFormat
		}

		tmpEmitter := codegen.NewEmitter(out.MaxLineLength())

		defaultValue := "defaultDuration"
		goDurationStr := duration.ToTimeDuration().String()

		tmpEmitter.Printlnf("%s, err := time.ParseDuration(\"%s\")", defaultValue, goDurationStr)
		tmpEmitter.Printlnf("if err != nil {")
		tmpEmitter.Indent(1)
		tmpEmitter.Printlnf(
			"return fmt.Errorf(\"failed to parse the \\\"%s\\\" default value for field %s: %%w\", err)",
			goDurationStr,
			v.jsonName,
		)
		tmpEmitter.Indent(-1)
		tmpEmitter.Printlnf("}")

		if v.isPointer {
			tmpEmitter.Printlnf(`%s.%s = &%s`, varNamePlainStruct, v.fieldName, defaultValue)
		} else {
			tmpEmitter.Printlnf(`%s.%s = %s`, varNamePlainStruct, v.fieldName, defaultValue)
		}

		return tmpEmitter.String(), nil
	}

	// Special handling for pointer-to-integer types (e.g., *int or NamedType wrapping *int).
	// We need to create a temp variable and take its address.
	//
	// The temp is declared with the pointee's own type, not left to `:=` on an
	// int literal: once a width can come from `format` or --min-sized-ints the
	// field may be a *int64 or *uint8, and `&` on an int temp would not be
	// assignable to it.
	if elem, ok := pointerToIntegerElem(v.defaultValueType); ok {
		if f, ok := v.defaultValue.(float64); ok {
			typeEmitter := codegen.NewEmitter(out.MaxLineLength())
			if err := elem.Generate(typeEmitter); err == nil {
				tmpVarName := "default" + v.fieldName
				tmpEmitter := codegen.NewEmitter(out.MaxLineLength())
				tmpEmitter.Printlnf(
					"var %s %s = %d", tmpVarName, strings.TrimSpace(typeEmitter.String()), int(f),
				)
				tmpEmitter.Printlnf(`%s = &%s`, getPlainName(v.fieldName), tmpVarName)

				return tmpEmitter.String(), nil
			}
		}
	}

	lit, ok := defaultLiteral(v.defaultValue, v.defaultValueType, v.useNumber && format == formatJSON)
	if !ok {
		v.leaveOut()

		return "", nil
	}

	return v.assignDefault(out, lit), nil
}

// leaveOut reports, once, a default that cannot be written as a value of its
// field's type. Written anyway, it would make the generated code fail to
// compile, and a default is only an annotation, so the field goes without.
func (v *defaultValidator) leaveOut() {
	if v.warned || v.warner == nil {
		return
	}

	v.warned = true

	encoded, err := json.Marshal(v.defaultValue)
	if err != nil {
		encoded = fmt.Appendf(nil, "%v", v.defaultValue)
	}

	typeName := "unknown"
	if v.defaultValueType != nil {
		typeName = typeString(v.defaultValueType)
	}

	v.warner(fmt.Sprintf("default %s for field %q is not a value of Go type %s; it is left out",
		encoded, v.jsonName, typeName))
}

// assignDefault generates the assignment of a default value to the field.
// When the field is a pointer type, it creates a typed temporary variable
// and assigns its address to the field.
func (v *defaultValidator) assignDefault(out *codegen.Emitter, valueExpr string) string {
	if !v.isPointer {
		return fmt.Sprintf(`%s = %s`, getPlainName(v.fieldName), valueExpr)
	}

	tmpVarName := "default" + v.fieldName
	tmpEmitter := codegen.NewEmitter(out.MaxLineLength())

	// Use a var declaration with explicit type so that untyped constants
	// (e.g. 42.0 from JSON) are correctly converted to the target type.
	typeEmitter := codegen.NewEmitter(out.MaxLineLength())

	if err := v.defaultValueType.Generate(typeEmitter); err == nil {
		typeName := strings.TrimSpace(typeEmitter.String())
		tmpEmitter.Printlnf("var %s %s = %s", tmpVarName, typeName, valueExpr)
	} else {
		tmpEmitter.Printlnf("%s := %s", tmpVarName, valueExpr)
	}

	tmpEmitter.Printlnf("%s = &%s", getPlainName(v.fieldName), tmpVarName)

	return strings.TrimRight(tmpEmitter.String(), "\n")
}

// isGoIntegerTypeName reports whether name is one of Go's integer types.
//
// Checking for the literal "int" is not enough: a schema can pin the width
// with `format: int64`, and `--min-sized-ints` derives one from the declared
// bounds. Missing those left integer defaults going through litter.Sdump as
// float64, emitting `plain.Version = 0.0` against an int64 field.
func isGoIntegerTypeName(name string) bool {
	switch name {
	case typeInt, "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true

	default:
		return false
	}
}

// pointerToIntegerElem returns the integer type a pointer-to-integer field
// points at, so a default can be placed in a temp of exactly that type.
func pointerToIntegerElem(t codegen.Type) (codegen.Type, bool) {
	switch tt := t.(type) {
	case codegen.NamedType:
		return pointerToIntegerElem(tt.Decl.Type)

	case *codegen.NamedType:
		return pointerToIntegerElem(tt.Decl.Type)

	case codegen.PointerType:
		if pt, ok := tt.Type.(codegen.PrimitiveType); ok && isGoIntegerTypeName(pt.Type) {
			return pt, true
		}

	case *codegen.PointerType:
		if pt, ok := tt.Type.(codegen.PrimitiveType); ok && isGoIntegerTypeName(pt.Type) {
			return pt, true
		}
	}

	return nil, false
}

func (v *defaultValidator) desc() *validatorDesc {
	var packages []packageImport

	_, ok := v.defaultValueType.(codegen.DurationType)
	if v.defaultValueType != nil && ok {
		defaultDurationISO8601, ok := v.defaultValue.(string)
		if ok && defaultDurationISO8601 != "" {
			packages = []packageImport{
				{qualifiedName: "fmt"},
				{qualifiedName: "time"},
			}
		}
	}

	return &validatorDesc{
		hasError:            false,
		beforeJSONUnmarshal: false,
		requiresRawAfter:    true,
		imports:             packages,
	}
}

type arrayValidator struct {
	checkTarget

	jsonName   string
	fieldName  string
	arrayDepth int
	minItems   int
	maxItems   int

	// maxItemsSet distinguishes a maximum of zero from no maximum. A closed
	// empty tuple admits only the empty array, which is a real cap of 0.
	maxItemsSet bool
}

func (v *arrayValidator) generate(out *codegen.Emitter, format string) error {
	if v.minItems == 0 && !v.maxItemsSet {
		return nil
	}

	value := v.value(v.fieldName)

	// The name is schema data. Keeping it an ARGUMENT rather than splicing it
	// into the format string means a `%` in it is never read as a verb, so
	// only Go-literal escaping is needed — and that is needed, since an
	// unescaped quote in `a"b` would not compile.
	quotedName := v.name(fmt.Sprintf(`"%s"`, goQuotedBody(v.jsonName)))

	var indexes []string

	for i := 1; i < v.arrayDepth; i++ {
		index := fmt.Sprintf("i%d", i)
		indexes = append(indexes, index)
		out.Printlnf(`for %s := range %s {`, index, value)
		value += fmt.Sprintf("[%s]", index)

		out.Indent(1)
	}

	fieldName := quotedName
	if len(indexes) > 0 {
		fieldName = fmt.Sprintf(
			`fmt.Sprintf(%q, %s, %s)`,
			"%s"+strings.Repeat("[%d]", len(indexes)), quotedName, strings.Join(indexes, ", "),
		)
	}

	if v.minItems != 0 {
		out.Printlnf(`if %s != nil && len(%s) < %d {`, value, value, v.minItems)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s length: must be >= %%d", %s, %d)`, fieldName, v.minItems)
		out.Indent(-1)
		out.Printlnf("}")
	}

	if v.maxItemsSet {
		out.Printlnf(`if len(%s) > %d {`, value, v.maxItems)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s length: must be <= %%d", %s, %d)`, fieldName, v.maxItems)
		out.Indent(-1)
		out.Printlnf("}")
	}

	for i := 1; i < v.arrayDepth; i++ {
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

func (v *arrayValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: false,
	}
}

type stringValidator struct {
	checkTarget

	jsonName  string
	fieldName string
	minLength int
	maxLength int
	// maxLengthSet distinguishes a maximum of zero from no maximum.
	maxLengthSet bool
	isNillable   bool
	pattern      string
	constVal     *string
}

func (v *stringValidator) generate(out *codegen.Emitter, format string) error {
	value := v.value(v.fieldName)
	fieldName := v.name(`"` + v.jsonName + `"`)
	checkPointer := ""
	pointerPrefix := ""

	if v.isNillable {
		checkPointer = fmt.Sprintf("%s != nil && ", value)
		pointerPrefix = "*"
	}

	if len(v.pattern) != 0 {
		if v.isNillable {
			out.Printlnf("if %s != nil {", value)
			out.Indent(1)
		}

		out.Printlnf(
			`if matched, _ := regexp.MatchString(`+"`%s`"+`, string(%s%s)); !matched {`,
			v.pattern, pointerPrefix, value,
		)
		// A struct field's pattern error names its Go field, as upstream's
		// does. A declared type has no field; its JSONName carries the type's
		// name, which the length checks print as well.
		patternName := v.fieldName
		if patternName == "" {
			patternName = v.jsonName
		}

		out.Indent(1)
		out.Printlnf(
			`return fmt.Errorf("field %%s pattern match: must match %%s", %s, `+"`%s`"+`)`,
			v.name(`"`+patternName+`"`), v.pattern,
		)
		out.Indent(-1)
		out.Printlnf("}")

		if v.isNillable {
			out.Indent(-1)
			out.Printlnf("}")
		}
	}

	if v.constVal != nil {
		out.Printlnf(`if %s%s%s != "%s" {`, checkPointer, pointerPrefix, value, *v.constVal)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be equal to %%s", %s, "%s")`, fieldName, *v.constVal)
		out.Indent(-1)
		out.Printlnf("}")
	}

	if v.minLength == 0 && !v.maxLengthSet {
		return nil
	}

	if v.minLength != 0 {
		out.Printlnf(`if %sutf8.RuneCountInString(string(%s%s)) < %d {`, checkPointer, pointerPrefix, value, v.minLength)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s length: must be >= %%d", %s, %d)`, fieldName, v.minLength)
		out.Indent(-1)
		out.Printlnf("}")
	}

	if v.maxLengthSet {
		out.Printlnf(`if %sutf8.RuneCountInString(string(%s%s)) > %d {`, checkPointer, pointerPrefix, value, v.maxLength)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s length: must be <= %%d", %s, %d)`, fieldName, v.maxLength)
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

func (v *stringValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: false,
	}

	if v.minLength != 0 || v.maxLengthSet {
		d.imports = append(d.imports, packageImport{qualifiedName: "unicode/utf8"})
	}

	return d
}

type numericValidator struct {
	checkTarget

	jsonName         string
	fieldName        string
	isNillable       bool
	multipleOf       *float64
	maximum          *float64
	exclusiveMaximum *any
	minimum          *float64
	exclusiveMinimum *any
	constVal         any
	roundToInt       bool
	// goType is the Go type of the value, and the texts are its multipleOf
	// and bounds as written, which an integer's check reads. See
	// integer_bounds.go.
	goType               string
	multipleOfText       string
	maximumText          string
	exclusiveMaximumText string
	minimumText          string
	exclusiveMinimumText string
}

func (v *numericValidator) generate(out *codegen.Emitter, format string) error {
	value := v.value(v.fieldName)
	name := v.name(`"` + v.jsonName + `"`)
	checkPointer := ""
	pointerPrefix := ""

	if v.isNillable {
		checkPointer = fmt.Sprintf("%s != nil && ", value)
		pointerPrefix = "*"
	}

	if v.constVal != nil {
		out.Printlnf(`if %s%s%s != %v {`, checkPointer, pointerPrefix, value, v.constVal)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be equal to %%v", %s, %v)`, name, v.constVal)
		out.Indent(-1)
		out.Printlnf("}")
	}

	if v.multipleOf != nil {
		if v.roundToInt {
			v.genIntegerMultipleOf(out, checkPointer, pointerPrefix, value, name)
		} else {
			if v.isNillable {
				out.Printlnf(`if %s != nil {`, value)
			} else {
				out.Printlnf("{")
			}

			out.Indent(1)
			out.Printlnf("remainder := math.Mod(%s%s, %v)", pointerPrefix, value, v.valueOf(*v.multipleOf))
			out.Printlnf(
				`if !(math.Abs(remainder) < 1e-10 || math.Abs(remainder - %v) < 1e-10) {`, v.valueOf(*v.multipleOf),
			)
			out.Indent(1)
			out.Printlnf(`return fmt.Errorf("field %%s: must be a multiple of %%v", %s, %f)`, name, *v.multipleOf)
			out.Indent(-1)
			out.Printlnf("}")

			out.Indent(-1)
			out.Printlnf("}")
		}
	}

	if v.roundToInt {
		v.genIntegerBoundary(out, checkPointer, pointerPrefix, value, name, true)
		v.genIntegerBoundary(out, checkPointer, pointerPrefix, value, name, false)

		return nil
	}

	nMin, nMax, nMinExclusive, nMaxExclusive := mathutils.NormalizeBounds(
		v.minimum, v.maximum, v.exclusiveMinimum, v.exclusiveMaximum,
	)

	v.genBoundary(out, checkPointer, pointerPrefix, value, name, nMax, nMaxExclusive, "<")
	v.genBoundary(out, checkPointer, pointerPrefix, value, name, nMin, nMinExclusive, ">")

	return nil
}

func (v *numericValidator) genBoundary(
	out *codegen.Emitter,
	checkPointer,
	pointerPrefix,
	value,
	name string,
	boundary *float64,
	exclusive bool,
	sign string,
) {
	if boundary == nil {
		return
	}

	// Technically, this should be based on schema version, but that information is lost.
	comp := sign
	if exclusive {
		// We're putting the other number first, so we need the = if it's exclusive.
		comp += "="
	} else {
		sign += "="
	}

	out.Printlnf(`if %s%v %s%s %s {`, checkPointer, v.valueOf(*boundary), comp, pointerPrefix, value)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: must be %s %%v", %s, %v)`, sign, name, v.valueOf(*boundary))
	out.Indent(-1)
	out.Printlnf("}")
}

func (v *numericValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: false,
	}
}

func (v *numericValidator) valueOf(val float64) any {
	if v.roundToInt {
		return int64(val)
	}

	return val
}

type booleanValidator struct {
	checkTarget

	jsonName   string
	fieldName  string
	isNillable bool
	constVal   *bool
}

func (v *booleanValidator) generate(out *codegen.Emitter, unmarshalTemplate string) error {
	value := v.value(v.fieldName)
	fieldName := v.name(`"` + v.jsonName + `"`)
	checkPointer := ""
	pointerPrefix := ""

	if v.isNillable {
		checkPointer = fmt.Sprintf("%s != nil && ", value)
		pointerPrefix = "*"
	}

	if v.constVal != nil {
		out.Printlnf(`if %s%s%s != %t {`, checkPointer, pointerPrefix, value, *v.constVal)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be equal to %%t", %s, %t)`, fieldName, *v.constVal)
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

func (v *booleanValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: false,
	}
}

func getPlainName(fieldName string) string {
	if fieldName == "" {
		return varNamePlainStruct
	}

	return fmt.Sprintf("%s.%s", varNamePlainStruct, fieldName)
}

type anyOfValidator struct {
	fieldName string
	elemCount int
	// typeNames holds the Go type of each branch, by position.
	typeNames []string
}

// newAnyOfValidator checks that a value decodes as at least one of a type's
// elemCount anyOf branches. variants names the types the branches were
// declared as; a branch without a name there is taken to be declared as
// fieldName_<position>.
func newAnyOfValidator(fieldName string, elemCount int, variants []string) *anyOfValidator {
	typeNames := make([]string, elemCount)

	for i := range typeNames {
		if i < len(variants) && variants[i] != "" {
			typeNames[i] = variants[i]
		} else {
			typeNames[i] = fmt.Sprintf("%s_%d", upperFirst(fieldName), i)
		}
	}

	return &anyOfValidator{fieldName: fieldName, elemCount: elemCount, typeNames: typeNames}
}

func (v *anyOfValidator) generate(out *codegen.Emitter, format string) error {
	for i := range v.elemCount {
		out.Printlnf(`var %s_%d %s`, lowerFirst(v.fieldName), i, v.typeNames[i])
	}

	out.Printlnf(`var errs []error`)

	for i := range v.elemCount {
		out.Printlnf(
			`if err := %s_%d.Unmarshal%s(value); err != nil {`,
			lowerFirst(v.fieldName),
			i,
			strings.ToUpper(format),
		)
		out.Indent(1)
		out.Printlnf(`errs = append(errs, err)`)
		out.Indent(-1)
		out.Printlnf(`}`)
	}

	out.Printlnf("if len(errs) == %d {", v.elemCount)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("all validators failed: %%s", errors.Join(errs...))`)
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *anyOfValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

func lowerFirst(s string) string {
	return strings.ToLower(s[:1]) + s[1:]
}

func upperFirst(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}

// goStringText escapes s for embedding inside a Go string literal that is also
// a fmt format string.
//
// Property names come from the schema, so they can contain a quote, a
// backslash or a percent sign. Interpolated raw, the first two end the literal
// early and produce source that does not compile, and the third is read by fmt
// as a verb. Escaping leaves ordinary names byte-for-byte unchanged, so
// generated output only differs where it was previously broken.
func goStringText(s string) string {
	return strings.ReplaceAll(goQuotedBody(s), "%", "%%")
}

// goQuotedBody escapes s for use inside a Go string literal, without the
// surrounding quotes. Struct tag values are parsed with strconv.Unquote, so an
// unescaped quote truncates the value and an unescaped backslash makes the
// whole tag unparseable — reflect then reports the tag as absent.
func goQuotedBody(s string) string {
	quoted := strconv.Quote(s)

	return quoted[1 : len(quoted)-1]
}

// JSON Schema format keywords supported by the built-in formatValidator.
const (
	formatKeywordUUID         = "uuid"
	formatKeywordEmail        = "email"
	formatKeywordURI          = "uri"
	formatKeywordURIReference = "uri-reference"
	formatKeywordHostname     = "hostname"
	formatKeywordRegex        = "regex"
)

// Embedded validation patterns expressed in RE2 (Go's regexp syntax).
const (
	formatRegexUUID     = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	formatRegexHostname = `^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`
	// formatRegexURIRef enforces the RFC 3986 character set for URI references:
	// unreserved + reserved characters and properly formed percent-encoded
	// triplets. Used in addition to net/url.Parse, which accepts almost any
	// string and so cannot reject e.g. whitespace or malformed pct-encoding.
	formatRegexURIRef = `^([A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=]|%[0-9A-Fa-f]{2})*$`
)

// Names of the precompiled regex vars emitted at package scope when a
// generated file uses a regex-backed format validator. Reused across all
// validation call sites so we pay regexp.MustCompile once per pattern per
// generated package, not per call.
const (
	regexpVarNameUUID     = "regexpFormatUUID"
	regexpVarNameHostname = "regexpFormatHostname"
	regexpVarNameURIRef   = "regexpFormatURIRef"
)

// formatRegexpDecl returns the precompiled-regex package-level decl, if any,
// that the named format keyword needs at runtime. Formats with no regex
// dependency (e.g. "email", "regex") return nil.
func formatRegexpDecl(format string) *codegen.RegexpVar {
	switch format {
	case formatKeywordUUID:
		return &codegen.RegexpVar{Name: regexpVarNameUUID, Pattern: formatRegexUUID}
	case formatKeywordHostname:
		return &codegen.RegexpVar{Name: regexpVarNameHostname, Pattern: formatRegexHostname}
	case formatKeywordURI, formatKeywordURIReference:
		return &codegen.RegexpVar{Name: regexpVarNameURIRef, Pattern: formatRegexURIRef}
	}

	return nil
}

// isKnownFormatKeyword reports whether the named format has a built-in
// runtime validator implemented by formatValidator.
func isKnownFormatKeyword(format string) bool {
	switch format {
	case formatKeywordUUID,
		formatKeywordEmail,
		formatKeywordURI,
		formatKeywordURIReference,
		formatKeywordHostname,
		formatKeywordRegex:
		return true
	}

	return false
}

// formatValidatorImports returns the package imports the generated validator
// for the named format requires.
// Stdlib import paths used by the generated format validators. Centralised so
// the per-format dispatch below names them consistently.
const (
	importPathRegexp  = "regexp"
	importPathStrings = "strings"
	importPathNetMail = "net/mail"
	importPathNetURL  = "net/url"
)

func formatValidatorImports(format string) []packageImport {
	switch format {
	case formatKeywordUUID, formatKeywordRegex:
		return []packageImport{{qualifiedName: importPathRegexp}}
	case formatKeywordHostname:
		// strings.TrimSuffix lets the length cap exclude the optional trailing
		// root dot per RFC 1034 §3.1, matching the doc comment on the emit.
		return []packageImport{
			{qualifiedName: importPathRegexp},
			{qualifiedName: importPathStrings},
		}
	case formatKeywordEmail:
		return []packageImport{{qualifiedName: importPathNetMail}}
	case formatKeywordURI, formatKeywordURIReference:
		return []packageImport{
			{qualifiedName: importPathNetURL},
			{qualifiedName: importPathRegexp},
		}
	}

	return nil
}

// formatValidator emits runtime validation for JSON Schema `format` keywords
// on string-typed fields. It mirrors stringValidator's nillable-pointer
// handling and runs after the typed struct has been decoded.
//
// A string held inside inline arrays or maps is reached through elements,
// which lists those containers outermost first. The check is then emitted
// inside one loop per container, and the error names the element, as in
// `ids[1]` or `byName["a"]`.
type formatValidator struct {
	// checkTarget's valueExpr is also set by the primitive-oneOf wrapper,
	// whose decoded value lives in a local inside the generated
	// UnmarshalJSON, not on a Plain struct. Everything else about the check,
	// including the message, stays identical to the struct-field path.
	checkTarget

	jsonName   string
	fieldName  string
	format     string
	isNillable bool
}

// containerLevel is one inline array or map between a field and the value an
// elementsValidator checks.
type containerLevel int

const (
	containerArray containerLevel = iota
	containerMap
)

func (v *formatValidator) generate(out *codegen.Emitter, _ string) error {
	value := v.value(v.fieldName)

	// Quoted like arrayValidator's name: the property name is schema data,
	// so it is escaped as a Go literal rather than spliced in as is.
	name := v.name(fmt.Sprintf(`"%s"`, goQuotedBody(v.jsonName)))
	open := 0

	if v.isNillable {
		out.Printlnf("if %s != nil {", value)
		out.Indent(1)

		open++

		value = "*" + value
	}

	target := value

	switch v.format {
	case formatKeywordUUID:
		out.Printlnf("if !%s.MatchString(string(%s)) {", regexpVarNameUUID, target)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid uuid", %s)`, name)
		out.Indent(-1)
		out.Printlnf("}")

	case formatKeywordHostname:
		// RFC 1123: each label is enforced by the regex (1 + {0,61} + 1 chars);
		// the overall hostname length cap (253 octets, exclusive of any trailing
		// root dot per RFC 1034 §3.1) is checked separately because regex
		// backtracking on the per-label structure would not bound the total
		// length on its own. TrimSuffix excludes the optional trailing dot
		// from the cap so a 253-char hostname plus root dot is still accepted.
		out.Printlnf(
			`if !%s.MatchString(string(%s)) || len(strings.TrimSuffix(string(%s), ".")) > 253 {`,
			regexpVarNameHostname, target, target,
		)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid hostname", %s)`, name)
		out.Indent(-1)
		out.Printlnf("}")

	case formatKeywordEmail:
		// RFC 5321 addr-spec only: reject display-name forms ("Alice <a@b>")
		// and require the parsed address to round-trip exactly. net/mail
		// accepts the broader RFC 5322 syntax by default.
		out.Printlnf(
			`if addr, err := mail.ParseAddress(string(%s));`+
				` err != nil || addr.Name != "" || addr.Address != string(%s) {`,
			target, target,
		)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid email (RFC 5321 addr-spec)", %s)`, name)
		out.Indent(-1)
		out.Printlnf("}")

	case formatKeywordURI:
		out.Printlnf("if u, err := url.Parse(string(%s)); err != nil || !u.IsAbs() {", target)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid absolute uri", %s)`, name)
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if !%s.MatchString(string(%s)) {", regexpVarNameURIRef, target)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid absolute uri", %s)`, name)
		out.Indent(-1)
		out.Printlnf("}")

	case formatKeywordURIReference:
		out.Printlnf("if _, err := url.Parse(string(%s)); err != nil {", target)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid uri reference: %%w", %s, err)`, name)
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if !%s.MatchString(string(%s)) {", regexpVarNameURIRef, target)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid uri reference", %s)`, name)
		out.Indent(-1)
		out.Printlnf("}")

	case formatKeywordRegex:
		// JSON Schema specifies the `regex` format as ECMA-262 (JavaScript)
		// regular expressions, but Go's stdlib only supports RE2 syntax.
		// Patterns valid in ECMA-262 but not RE2 (backreferences, lookaround)
		// will be rejected here. The error message reflects what we actually
		// validate against.
		out.Printlnf("if _, err := regexp.Compile(string(%s)); err != nil {", target)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must be a valid RE2 regular expression: %%w", %s, err)`, name)
		out.Indent(-1)
		out.Printlnf("}")
	}

	for ; open > 0; open-- {
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

func (v *formatValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError: true,
		imports:  formatValidatorImports(v.format),
	}

	if regexpDecl := formatRegexpDecl(v.format); regexpDecl != nil {
		d.decls = append(d.decls, regexpDecl)
	}

	return d
}

// strictFieldsValidator emits a check that rejects any JSON field on the
// container object that is not declared in the schema's `properties`. It
// implements `additionalProperties: false` enforcement at unmarshal time
// and runs against the raw map produced before the typed decode.
type strictFieldsValidator struct {
	declName    string
	knownFields []string // sorted JSON field names declared by the schema
}

func (v *strictFieldsValidator) generate(out *codegen.Emitter, _ string) error {
	// The container itself may legitimately be null (e.g. type ["null","object"]),
	// in which case the raw map is nil and no further checks apply.
	out.Printlnf("if %s != nil {", varNameRawMap)
	out.Indent(1)
	out.Printlnf("var extraKeys []string")
	out.Printlnf("for additionalKey := range %s {", varNameRawMap)
	out.Indent(1)
	out.Printlnf("switch additionalKey {")

	if len(v.knownFields) > 0 {
		quoted := make([]string, len(v.knownFields))
		for i, name := range v.knownFields {
			quoted[i] = fmt.Sprintf("%q", name)
		}

		out.Printlnf("case %s:", strings.Join(quoted, ", "))
	}

	out.Printlnf("default:")
	out.Indent(1)
	out.Printlnf("extraKeys = append(extraKeys, additionalKey)")
	out.Indent(-1)
	out.Printlnf("}")
	out.Indent(-1)
	out.Printlnf("}")
	// Sort so the error message is deterministic — Go map iteration order is
	// randomized, so without this the reported keys would vary across runs.
	out.Printlnf("if len(extraKeys) > 0 {")
	out.Indent(1)
	out.Printlnf("sort.Strings(extraKeys)")
	out.Printlnf(`return fmt.Errorf("fields %%q in %s: additional properties not allowed", extraKeys)`, v.declName)
	out.Indent(-1)
	out.Printlnf("}")
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *strictFieldsValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
		imports:             []packageImport{{qualifiedName: "sort"}},
	}
}

// wrapperDefaultValidator sets a primitive union wrapper field to its default
// while its key is absent, by decoding the default, as JSON, through the
// wrapper's own UnmarshalJSON. A wrapper is a struct with unexported fields,
// so a literal of the default's value cannot be assigned to it.
type wrapperDefaultValidator struct {
	jsonName  string
	fieldName string
	// encoded is the default, encoded as JSON.
	encoded string
	// acceptsNull is set when the wrapper holds null as a value. An explicit
	// null is then kept, and only an absent key takes the default.
	acceptsNull bool
}

func (v *wrapperDefaultValidator) generate(out *codegen.Emitter, format string) error {
	// yaml.v3 does not hand a null node to UnmarshalYAML, so in YAML an
	// explicit null reaches a wrapper that accepts it as the zero value, not
	// as null; it is decoded into the wrapper here instead.
	keepNull := v.acceptsNull && format == formatYAML

	switch {
	case keepNull:
		out.Printlnf(`if v, ok := %s[%q]; !ok {`, varNameRawMap, v.jsonName)
	case v.acceptsNull:
		out.Printlnf(`if _, ok := %s[%q]; !ok {`, varNameRawMap, v.jsonName)
	default:
		out.Printlnf(`if v, ok := %s[%q]; !ok || v == nil {`, varNameRawMap, v.jsonName)
	}

	v.decodeInto(out, v.encoded)

	if keepNull {
		out.Printlnf(`} else if v == nil {`)
		v.decodeInto(out, "null")
	}

	out.Printlnf("}")

	return nil
}

// decodeInto emits the decoding of the JSON text encoded into the field.
func (v *wrapperDefaultValidator) decodeInto(out *codegen.Emitter, encoded string) {
	out.Indent(1)
	out.Printlnf(`if err := json.Unmarshal([]byte(%q), &%s); err != nil {`, encoded, getPlainName(v.fieldName))
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: default: %%w", %q, err)`, v.jsonName)
	out.Indent(-1)
	out.Printlnf("}")
	out.Indent(-1)
}

func (v *wrapperDefaultValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:         true,
		requiresRawAfter: true,
		imports:          []packageImport{{qualifiedName: jsonPackage}},
	}
}

// isEmptyInterfaceDecl reports whether nt names a type declared as interface{}.
func isEmptyInterfaceDecl(nt *codegen.NamedType) bool {
	switch nt.Decl.Type.(type) {
	case codegen.EmptyInterfaceType, *codegen.EmptyInterfaceType:
		return true
	}

	return false
}
