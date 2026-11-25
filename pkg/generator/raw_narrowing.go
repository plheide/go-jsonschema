package generator

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

const (
	// rawNumberHelperBase and rawTypeHelperBase begin the names of the file's
	// helpers that read a value of the raw map: its number, and its JSON type.
	rawNumberHelperBase = "rawNumber_"
	rawTypeHelperBase   = "rawIsType_"

	// maxRawNarrowingDepth bounds how deep a narrowing compiles, so that one
	// that refers to itself through a $ref ends.
	maxRawNarrowingDepth = 8

	// mathPackage is the import path of the math package.
	mathPackage = "math"
)

// rawNarrowing is what the then or else of a presence-keyed clause asks of a
// property's value, where the property is present: the subschema it gives the
// property, compiled as a whole or not at all. The checks read the value in
// the raw map, as JSON or YAML decodes it into an interface{}, so the typed
// field's default or pointer plays no part.
type rawNarrowing struct {
	// types lists the JSON types the value must have one of, or none.
	types []string
	// values lists the values, of const and enum, that the value must equal
	// one of, when listed is set.
	values []any
	listed bool
	// number checks a number's bounds and multipleOf, and str a string's
	// lengths and pattern.
	number *numericValidator
	str    *stringValidator
	// required, forbidden and properties apply to an object: the properties
	// it must have, those it must not have, and what the others ask of those
	// it has.
	required   []string
	forbidden  []string
	properties []namedNarrowing
}

// namedNarrowing is the rawNarrowing of an object's property.
type namedNarrowing struct {
	*rawNarrowing

	name string
}

// checksNothing reports whether n asks nothing of a value.
func (n *rawNarrowing) checksNothing() bool {
	return len(n.types) == 0 && !n.listed && n.number == nil && n.str == nil &&
		len(n.required) == 0 && len(n.forbidden) == 0 && len(n.properties) == 0
}

// usesHelpers reports whether n's checks call the file's raw value helpers.
func (n *rawNarrowing) usesHelpers() bool {
	if len(n.types) > 0 || n.number != nil || slices.ContainsFunc(n.values, isRawNumber) {
		return true
	}

	return slices.ContainsFunc(n.properties, func(p namedNarrowing) bool { return p.usesHelpers() })
}

// imports returns the packages n's checks use, besides fmt.
func (n *rawNarrowing) imports() []packageImport {
	var imports []packageImport

	if n.number != nil && n.number.multipleOf != nil {
		imports = append(imports, packageImport{qualifiedName: mathPackage})
	}

	if n.str != nil {
		if n.str.pattern != "" {
			imports = append(imports, packageImport{qualifiedName: "regexp"})
		}

		if n.str.minLength != 0 || n.str.maxLengthSet {
			imports = append(imports, packageImport{qualifiedName: "unicode/utf8"})
		}
	}

	for _, p := range n.properties {
		imports = append(imports, p.imports()...)
	}

	return imports
}

// compileRawNarrowing compiles s, the subschema that a then or else gives a
// property, which doc, from docFile, holds, as what it asks of the property's
// value. It returns false when s asks something these checks do not check,
// and the property is then left unchecked, as before. A $ref is followed as
// followRefsQuietly follows one, from doc.
//
// A `type: object` alone is not checked: the schema parser infers it from
// `properties`, so it may not be the schema's own, and a value that is no
// object then passes, as the object keywords pass it.
func (g *schemaGenerator) compileRawNarrowing(
	s *schemas.Type, doc *schemas.Schema, docFile string, depth int,
) (*rawNarrowing, bool) {
	if depth > maxRawNarrowingDepth {
		return nil, false
	}

	s, doc, docFile, ok := g.followRefsQuietlyFrom(s, doc, docFile)
	if !ok {
		return nil, false
	}

	var (
		n    = &rawNarrowing{}
		rest = *s
	)

	if len(s.Type) > 0 && (len(s.Type) != 1 || s.Type[0] != schemas.TypeNameObject) {
		n.types = slices.Clone([]string(s.Type))
	}

	rest.Type = nil

	if n.values, n.listed, ok = listedRawValues(s); !ok {
		return nil, false
	}

	rest.Const, rest.ConstIsSet, rest.ConstExact, rest.Enum, rest.XEnumVarnames = nil, false, nil, nil, nil

	numeric := newNumericValidator(codegen.StructField{SchemaType: s}, "float64", false)
	numeric.constVal = nil

	if numeric.checksSomething() {
		n.number = numeric
	}

	rest.MultipleOf, rest.MultipleOfExact = nil, ""
	rest.Minimum, rest.MinimumExact, rest.Maximum, rest.MaximumExact = nil, "", nil, ""
	rest.ExclusiveMinimum, rest.ExclusiveMinimumExact = nil, ""
	rest.ExclusiveMaximum, rest.ExclusiveMaximumExact = nil, ""

	if s.MinLength != 0 || s.HasMaxLength() || s.Pattern != "" {
		pattern, ok := quietPattern(s.Pattern)
		if !ok {
			return nil, false
		}

		n.str = &stringValidator{
			minLength:    s.MinLength,
			maxLength:    s.MaxLength,
			maxLengthSet: s.HasMaxLength(),
			pattern:      pattern,
		}
	}

	rest.MinLength, rest.MaxLength, rest.MaxLengthIsSet, rest.Pattern = 0, 0, false, ""

	n.required, rest.Required = slices.Clone(s.Required), nil

	for _, name := range sortedKeys(s.Properties) {
		p := s.Properties[name]
		if isFalseSchema(p) {
			n.forbidden = append(n.forbidden, name)

			continue
		}

		sub, ok := g.compileRawNarrowing(p, doc, docFile, depth+1)
		if !ok {
			return nil, false
		}

		if !sub.checksNothing() {
			n.properties = append(n.properties, namedNarrowing{rawNarrowing: sub, name: name})
		}
	}

	rest.Properties = nil

	if constrains(&rest) {
		return nil, false
	}

	return n, true
}

// quietPattern returns pattern as the string checks write it, and whether
// Go's regexp compiles it, without the warning compilablePattern gives: a
// narrowing that cannot be checked is reported as a whole. A backtick, which
// ends the raw string the check writes it in, cannot be checked either.
func quietPattern(pattern string) (string, bool) {
	if pattern == "" {
		return "", true
	}

	escaped := pattern
	for i, c := range []string{"\b", "\f", "\n", "\r", "\t"} {
		escaped = strings.ReplaceAll(escaped, c, []string{`\b`, `\f`, `\n`, `\r`, `\t`}[i])
	}

	if strings.Contains(escaped, "`") {
		return "", false
	}

	if _, err := regexp.Compile(escaped); err != nil {
		return "", false
	}

	return escaped, true
}

// listedRawValues returns the values s lists with const and enum, those both
// list where it has both, and whether it lists any. ok is false when one is an
// array or an object, which these checks do not compare.
func listedRawValues(s *schemas.Type) ([]any, bool, bool) {
	var lists [][]any

	if s.ConstIsSet || s.Const != nil {
		lists = append(lists, []any{s.Const})
	}

	if s.Enum != nil {
		lists = append(lists, s.Enum)
	}

	if len(lists) == 0 {
		return nil, false, true
	}

	values := []any{}

	for _, v := range lists[0] {
		if !isRawScalar(v) {
			return nil, false, false
		}

		if slices.ContainsFunc(lists[1:], func(list []any) bool { return !slices.ContainsFunc(list, rawEquals(v)) }) {
			continue
		}

		if !slices.ContainsFunc(values, rawEquals(v)) {
			values = append(values, v)
		}
	}

	return values, true, true
}

// isRawScalar reports whether v, a value a schema lists, is a string, a
// boolean, a number or null.
func isRawScalar(v any) bool {
	switch v.(type) {
	case nil, bool, string:
		return true
	}

	return isRawNumber(v)
}

// isRawNumber reports whether v, a value a schema lists, is a number.
func isRawNumber(v any) bool {
	_, ok := rawNumberOf(v)

	return ok
}

// rawNumberOf returns v, a value a schema lists, as a float64, when it is a
// number.
func rawNumberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	}

	return 0, false
}

// rawEquals returns a test of whether a listed value equals v, as JSON Schema
// compares them: numbers by their value.
func rawEquals(v any) func(any) bool {
	return func(w any) bool {
		if a, ok := rawNumberOf(v); ok {
			b, ok := rawNumberOf(w)

			return ok && a == b
		}

		return v == w
	}
}

// rawNarrowingEmitter writes the checks of rawNarrowings for declName's raw
// map. numberHelper and typeHelper name the file's raw value helpers.
type rawNarrowingEmitter struct {
	declName     string
	numberHelper string
	typeHelper   string
}

// emitProperty writes the checks of n on property name of the raw map, where
// it is present. contextLabel says when they apply, such as "when end is
// present".
func (e rawNarrowingEmitter) emitProperty(out *codegen.Emitter, name string, n *rawNarrowing, contextLabel string) {
	e.emitEntry(out, varNameRawMap, name, name, n, contextLabel, 1)
}

// emitEntry writes the checks of n on key of the object held in holder, where
// it is present. path names the value in errors, and depth numbers the
// variables of this level.
func (e rawNarrowingEmitter) emitEntry(
	out *codegen.Emitter, holder, key, path string, n *rawNarrowing, contextLabel string, depth int,
) {
	value := fmt.Sprintf("v%d", depth)

	out.Printlnf(`if %s, present := %s[%q]; present {`, value, holder, key)
	out.Indent(1)
	e.emitValue(out, value, path, n, contextLabel, depth)
	out.Indent(-1)
	out.Printlnf(`}`)
}

// emitValue writes the checks of n on the value named value.
func (e rawNarrowingEmitter) emitValue(
	out *codegen.Emitter, value, path string, n *rawNarrowing, contextLabel string, depth int,
) {
	field := fmt.Sprintf("field %s in %s (%s)", goStringText(path), e.declName, goStringText(contextLabel))
	nameExpr := strconv.Quote(fmt.Sprintf("%s in %s (%s)", path, e.declName, contextLabel))

	if len(n.types) > 0 {
		quoted := make([]string, len(n.types))
		for i, name := range n.types {
			quoted[i] = strconv.Quote(name)
		}

		out.Printlnf(`if !%s(%s, %s) {`, e.typeHelper, value, strings.Join(quoted, ", "))
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("%s: must be of type %s")`, field, strings.Join(n.types, " or "))
		out.Indent(-1)
		out.Printlnf(`}`)
	}

	if n.listed {
		e.emitListed(out, value, field, n.values, depth)
	}

	if n.number != nil {
		number := fmt.Sprintf("n%d", depth)

		out.Printlnf(`if %s, isNumber := %s(%s); isNumber {`, number, e.numberHelper, value)
		out.Indent(1)

		n.number.checkTarget = checkTarget{valueExpr: number, nameExpr: nameExpr}
		_ = n.number.generate(out, "")

		out.Indent(-1)
		out.Printlnf(`}`)
	}

	if n.str != nil {
		str := fmt.Sprintf("s%d", depth)

		out.Printlnf(`if %s, isString := %s.(string); isString {`, str, value)
		out.Indent(1)

		n.str.checkTarget = checkTarget{valueExpr: str, nameExpr: nameExpr}
		_ = n.str.generate(out, "")

		out.Indent(-1)
		out.Printlnf(`}`)
	}

	if len(n.required) == 0 && len(n.forbidden) == 0 && len(n.properties) == 0 {
		return
	}

	object := fmt.Sprintf("m%d", depth)

	out.Printlnf(`if %s, isObject := %s.(map[string]interface{}); isObject {`, object, value)
	out.Indent(1)

	for _, name := range n.required {
		out.Printlnf(`if _, ok := %s[%q]; !ok {`, object, name)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %s in %s (%s): required")`,
			goStringText(path+"."+name), e.declName, goStringText(contextLabel))
		out.Indent(-1)
		out.Printlnf(`}`)
	}

	for _, name := range n.forbidden {
		out.Printlnf(`if _, ok := %s[%q]; ok {`, object, name)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %s in %s (%s): must not be present")`,
			goStringText(path+"."+name), e.declName, goStringText(contextLabel))
		out.Indent(-1)
		out.Printlnf(`}`)
	}

	for _, p := range n.properties {
		e.emitEntry(out, object, p.name, path+"."+p.name, p.rawNarrowing, contextLabel, depth+1)
	}

	out.Indent(-1)
	out.Printlnf(`}`)
}

// emitListed writes the check that the value named value equals one of
// values, as JSON Schema compares them: a number by its value, which the
// file's number helper reads.
func (e rawNarrowingEmitter) emitListed(out *codegen.Emitter, value, field string, values []any, depth int) {
	var (
		matches []string
		numbers []string
		shown   = make([]string, len(values))
	)

	for i, v := range values {
		shown[i] = goStringText(rawValueText(v))

		if f, ok := rawNumberOf(v); ok {
			numbers = append(numbers, fmt.Sprintf("n%d == %s", depth, strconv.FormatFloat(f, 'g', -1, 64)))

			continue
		}

		switch v := v.(type) {
		case nil:
			matches = append(matches, value+" == nil")
		case bool:
			matches = append(matches, fmt.Sprintf("%s == %t", value, v))
		case string:
			matches = append(matches, fmt.Sprintf("%s == %q", value, v))
		}
	}

	if len(values) == 0 {
		// The const and the enum have no value in common, so none passes.
		out.Printlnf(`return fmt.Errorf("%s: must not be present")`, field)

		return
	}

	verb := "must be " + shown[0]
	if len(shown) != 1 {
		verb = "must be one of [" + strings.Join(shown, ", ") + "]"
	}

	if len(numbers) > 0 {
		matches = append(matches, fmt.Sprintf("(isNumber && (%s))", strings.Join(numbers, " || ")))

		out.Printlnf(`if n%d, isNumber := %s(%s); !(%s) {`, depth, e.numberHelper, value, strings.Join(matches, " || "))
	} else {
		out.Printlnf(`if !(%s) {`, strings.Join(matches, " || "))
	}

	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("%s: %s")`, field, verb)
	out.Indent(-1)
	out.Printlnf(`}`)
}

// rawValueText writes a value a schema lists for an error: a string as it
// is, and anything else as JSON writes it.
func rawValueText(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return v
	}

	if f, ok := rawNumberOf(v); ok {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}

	return fmt.Sprint(v)
}

// rawValueHelpers returns the names of the file's helpers that read a value of
// the raw map: its number, and whether it is of one of some JSON types. The
// first call declares them, named after typeName, the type whose check calls
// them.
func (o *output) rawValueHelpers(typeName string) (string, string) {
	if o.rawNumberHelperName != "" {
		return o.rawNumberHelperName, o.rawTypeHelperName
	}

	number, isType := rawNumberHelperBase+typeName, rawTypeHelperBase+typeName
	for n := 1; o.declaresName(number) || o.importsName(number) || o.declaresName(isType) || o.importsName(isType); n++ {
		number = fmt.Sprintf("%s%s_%d", rawNumberHelperBase, typeName, n)
		isType = fmt.Sprintf("%s%s_%d", rawTypeHelperBase, typeName, n)
	}

	o.rawNumberHelperName, o.rawTypeHelperName = number, isType

	for _, pkg := range []string{jsonPackage, mathPackage} {
		o.file.Package.AddImport(pkg, "")
	}

	o.file.Package.AddDecl(&codegen.Method{Name: number, Impl: emitRawNumberHelper(number)})
	o.file.Package.AddDecl(&codegen.Method{Name: isType, Impl: emitRawTypeHelper(number, isType)})

	return number, isType
}

// emitRawNumberHelper writes the helper named number.
func emitRawNumberHelper(number string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		out.Commentf("%s returns the number v holds, as JSON or YAML decodes one into an interface{}, "+
			"as a float64. Every presence-keyed check in this file calls it; it is named after the first, "+
			"so that no other file of the package declares it.", number)
		out.Printlnf("func %s(v interface{}) (float64, bool) {", number)
		out.Indent(1)
		out.Printlnf("switch n := v.(type) {")
		out.Printlnf("case float64:")
		out.Printlnf("return n, true")
		out.Printlnf("case int:")
		out.Printlnf("return float64(n), true")
		out.Printlnf("case int64:")
		out.Printlnf("return float64(n), true")
		out.Printlnf("case uint64:")
		out.Printlnf("return float64(n), true")
		out.Printlnf("case json.Number:")
		out.Printlnf("f, err := n.Float64()")
		out.Printlnf("return f, err == nil")
		out.Printlnf("}")
		out.Printlnf("return 0, false")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// emitRawTypeHelper writes the helper named isType, which calls the number
// helper named number.
func emitRawTypeHelper(number, isType string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		out.Commentf("%s reports whether v, as JSON or YAML decodes a value into an interface{}, is of one "+
			"of the JSON types names. An integer is a number with no fraction.", isType)
		out.Printlnf("func %s(v interface{}, names ...string) bool {", isType)
		out.Indent(1)
		out.Printlnf("for _, name := range names {")
		out.Indent(1)
		out.Printlnf("switch name {")
		out.Printlnf(`case "null":`)
		out.Printlnf("if v == nil {")
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf(`case "boolean":`)
		out.Printlnf("if _, ok := v.(bool); ok {")
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf(`case "string":`)
		out.Printlnf("if _, ok := v.(string); ok {")
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf(`case "array":`)
		out.Printlnf("if _, ok := v.([]interface{}); ok {")
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf(`case "object":`)
		out.Printlnf("if _, ok := v.(map[string]interface{}); ok {")
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf(`case "number":`)
		out.Printlnf("if _, ok := %s(v); ok {", number)
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf(`case "integer":`)
		out.Printlnf("if n, ok := %s(v); ok && n == math.Trunc(n) && !math.IsInf(n, 0) {", number)
		out.Printlnf("return true")
		out.Printlnf("}")
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("return false")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}
