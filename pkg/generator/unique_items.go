package generator

import (
	"errors"
	"fmt"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

var errUniqueKeyHelperUnbound = errors.New("uniqueItems check generated without its key helper")

// uniqueKeyHelperBase names the helper that keys a uniqueItems check's items,
// declared once in each generated file that needs it and named after the
// first type whose check calls it, as integralHelperBase's is.
const uniqueKeyHelperBase = "uniqueItemKey_"

// uniqueItemsSource is where a uniqueItemsValidator reads the array from.
type uniqueItemsSource int

const (
	// uniqueItemsProperty reads a property from the raw map.
	uniqueItemsProperty uniqueItemsSource = iota
	// uniqueItemsAdditional reads the raw map once the declared properties
	// are removed from it, which leaves the additional properties.
	uniqueItemsAdditional
	// uniqueItemsSelf decodes the method's own input, for a declared array or
	// map.
	uniqueItemsSelf
)

// uniqueItemsLevel is one array or map the walk reaches.
type uniqueItemsLevel struct {
	container containerLevel
	// unique marks an array that declares uniqueItems.
	unique bool
}

// uniqueItemsValidator checks `uniqueItems` on an array, and on the arrays it
// holds, at each depth that declares it. It reads the input decoded into
// generic values, because JSON Schema compares items as JSON: decoded into
// their Go type, different items could become equal, by losing undeclared
// properties or taking defaults. The raw map holds JSON numbers as float64,
// which makes integers beyond 2^53 equal, so in JSON the check decodes the
// input again with its numbers kept exact; yaml.v3 decodes integers exactly.
// The file's key helper keys each item by its JSON encoding, which sorts an
// object's keys, with each number written in one form, so equal items get
// equal keys and 1.0 equals 1.
type uniqueItemsValidator struct {
	// rawKeyMatch finds a property under each key the decoder assigns to it.
	rawKeyMatch

	jsonName string
	source   uniqueItemsSource
	levels   []uniqueItemsLevel
	// keyHelper is the file's key helper, given with the struct's fields.
	keyHelper string
}

var (
	_ rawKeyedValidator   = new(uniqueItemsValidator)
	_ fileHelperValidator = new(uniqueItemsValidator)
)

func (v *uniqueItemsValidator) matchStructKeys(fields []codegen.StructField) {
	if v.source == uniqueItemsProperty {
		v.matchKeysOf(fields, v.jsonName)
	}
}

func (v *uniqueItemsValidator) useFileHelpers(o *output, typeName string) {
	v.keyHelper = o.uniqueKeyHelper(typeName)
}

func (v *uniqueItemsValidator) generate(out *codegen.Emitter, format string) error {
	if v.keyHelper == "" {
		return errUniqueKeyHelperUnbound
	}

	name := newElementName(v.jsonName)

	// In JSON, the property or the additional properties are read from the
	// input decoded once more with its numbers exact, which the body does
	// for requiresNumbers; in YAML, from the raw map, whose integers are
	// exact already.
	source := varNameRawMap
	if format == formatJSON {
		source = varNameNumbers
	}

	switch v.source {
	case uniqueItemsProperty:
		emitMapKeyMatch(out, source, format, v.jsonName, v.caseVariants, v.exactOnly, false)
		v.emitLevel(out, "fieldValue", 0, name)
		out.Indent(-1)
		out.Printlnf("}")

	case uniqueItemsAdditional:
		// The raw map holds the additional properties alone by now.
		out.Printlnf("for k1 := range %s {", varNameRawMap)
		out.Indent(1)
		v.emitLevel(out, source+"[k1]", 1, name.add("[%q]", "k1"))
		out.Indent(-1)
		out.Printlnf("}")

	case uniqueItemsSelf:
		out.Printlnf("{")
		out.Indent(1)
		out.Printlnf("var items interface{}")

		if format == formatYAML {
			out.Printlnf("if err := value.Decode(&items); err != nil {")
		} else {
			out.Printlnf("dec := json.NewDecoder(bytes.NewReader(value))")
			out.Printlnf("dec.UseNumber()")
			out.Printlnf("if err := dec.Decode(&items); err != nil {")
		}

		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("unmarshal raw %s: %%w", err)`, goStringText(v.jsonName))
		out.Indent(-1)
		out.Printlnf("}")
		v.emitLevel(out, "items", 0, name)
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

// emitLevel writes the check of the value expr holds, at depth, and of the
// values it holds in turn.
func (v *uniqueItemsValidator) emitLevel(out *codegen.Emitter, expr string, depth int, name elementName) {
	level := v.levels[depth]
	iter, verb := levelIndex(level.container, depth+1)

	container, generic := fmt.Sprintf("a%d", depth), "[]interface{}"
	if level.container == containerMap {
		container, generic = fmt.Sprintf("m%d", depth), "map[string]interface{}"
	}

	out.Printlnf("if %s, ok := %s.(%s); ok {", container, expr, generic)
	out.Indent(1)

	deeper := depth+1 < len(v.levels)

	if level.unique {
		// A block keeps the check's names from the deeper levels' checks.
		if deeper {
			out.Printlnf("{")
			out.Indent(1)
		}

		out.Printlnf("seen := make(map[string]int, len(%s))", container)
		out.Printlnf("for i, item := range %s {", container)
		out.Indent(1)
		out.Printlnf("key, ok := %s(item)", v.keyHelper)
		out.Printlnf("if !ok {")
		out.Indent(1)
		out.Printlnf("continue")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if first, ok := seen[key]; ok {")
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: items %%d and %%d are equal; they must be unique", %s, first, i)`,
			name.expr())
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("seen[key] = i")
		out.Indent(-1)
		out.Printlnf("}")

		if deeper {
			out.Indent(-1)
			out.Printlnf("}")
		}
	}

	if deeper {
		elem := fmt.Sprintf("e%d", depth+1)

		out.Printlnf("for %s, %s := range %s {", iter, elem, container)
		out.Indent(1)
		v.emitLevel(out, elem, depth+1, name.add(verb, iter))
		out.Indent(-1)
		out.Printlnf("}")
	}

	out.Indent(-1)
	out.Printlnf("}")
}

func (v *uniqueItemsValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:                  true,
		beforeJSONUnmarshal:       v.source == uniqueItemsProperty,
		afterAdditionalProperties: v.source == uniqueItemsAdditional,
		beforeRawDecode:           v.source == uniqueItemsSelf,
		requiresNumbers:           v.source != uniqueItemsSelf,
		imports:                   []packageImport{{qualifiedName: jsonPackage}, {qualifiedName: "bytes"}},
	}

	if v.source == uniqueItemsProperty {
		d.imports = append(d.imports, v.imports()...)
	}

	return d
}

// fileHelperValidator is a check that calls a helper declared once in its
// file, which it is given when its type's unmarshaler is generated.
type fileHelperValidator interface {
	validator
	useFileHelpers(o *output, typeName string)
}

// uniqueKeyHelper returns the name of the file's helper that keys a
// uniqueItems check's items. The first call declares it, named after
// typeName, the type whose check calls it.
func (o *output) uniqueKeyHelper(typeName string) string {
	if o.uniqueKeyHelperName != "" {
		return o.uniqueKeyHelperName
	}

	name := uniqueKeyHelperBase + typeName
	for n := 1; o.declaresName(name) || o.importsName(name); n++ {
		name = fmt.Sprintf("%s%s_%d", uniqueKeyHelperBase, typeName, n)
	}

	o.uniqueKeyHelperName = name

	for _, pkg := range []string{jsonPackage, "strconv", "strings"} {
		o.file.Package.AddImport(pkg, "")
	}

	o.file.Package.AddDecl(&codegen.Method{Name: name, Impl: emitUniqueKeyHelper(name)})

	return name
}

// emitUniqueKeyHelper writes the key helper named name, and the function it
// calls to write one number. The number is rewritten on its digits, so it
// keeps its exact value, and a huge exponent costs nothing.
func emitUniqueKeyHelper(name string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		number := name + "_number"

		out.Commentf("%s returns the JSON encoding of v with each number written in one form, so "+
			"that items JSON Schema counts as equal, such as 1 and 1.0, get the same key, and integers "+
			"a float64 cannot tell apart do not. It reports false for a value with no JSON encoding. "+
			"Every uniqueItems check in this file calls it; it is named after the first, so that no "+
			"other file of the package declares it.", name)
		out.Printlnf("func %s(v interface{}) (string, bool) {", name)
		out.Indent(1)
		out.Printlnf("var canon func(interface{}) interface{}")
		out.Printlnf("canon = func(n interface{}) interface{} {")
		out.Indent(1)
		out.Printlnf("switch n := n.(type) {")
		out.Printlnf("case json.Number:")
		out.Printlnf("return %s(string(n))", number)
		out.Printlnf("case float64:")
		out.Printlnf("return %s(strconv.FormatFloat(n, 'g', -1, 64))", number)
		out.Printlnf("case int:")
		out.Printlnf("return %s(strconv.Itoa(n))", number)
		out.Printlnf("case uint64:")
		out.Printlnf("return %s(strconv.FormatUint(n, 10))", number)
		out.Printlnf("case map[string]interface{}:")
		out.Indent(1)
		out.Printlnf("m := make(map[string]interface{}, len(n))")
		out.Printlnf("for k, e := range n {")
		out.Printlnf("m[k] = canon(e)")
		out.Printlnf("}")
		out.Printlnf("return m")
		out.Indent(-1)
		out.Printlnf("case []interface{}:")
		out.Indent(1)
		out.Printlnf("a := make([]interface{}, len(n))")
		out.Printlnf("for i, e := range n {")
		out.Printlnf("a[i] = canon(e)")
		out.Printlnf("}")
		out.Printlnf("return a")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("return n")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("b, err := json.Marshal(canon(v))")
		out.Printlnf("if err != nil {")
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Printlnf("return string(b), true")
		out.Indent(-1)
		out.Printlnf("}")
		out.Newline()

		out.Commentf("%s writes the number s in one form: its significant digits as d.ddd, and the "+
			"exponent, such as 9.007199254740993e15. A number with an exponent beyond a billion is "+
			"left as it is.", number)
		out.Printlnf("func %s(s string) json.Number {", number)
		out.Indent(1)
		out.Printlnf(`neg := strings.HasPrefix(s, "-")`)
		out.Printlnf(`mantissa, exp := strings.TrimPrefix(s, "-"), 0`)
		out.Printlnf(`if i := strings.IndexAny(mantissa, "eE"); i >= 0 {`)
		out.Indent(1)
		out.Printlnf("e, err := strconv.Atoi(mantissa[i+1:])")
		out.Printlnf("if err != nil || e > 1e9 || e < -1e9 {")
		out.Printlnf("return json.Number(s)")
		out.Printlnf("}")
		out.Printlnf("mantissa, exp = mantissa[:i], e")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf(`intPart, frac, _ := strings.Cut(mantissa, ".")`)
		out.Printlnf("all := intPart + frac")
		out.Printlnf(`trimmed := strings.TrimLeft(all, "0")`)
		out.Printlnf(`digits := strings.TrimRight(trimmed, "0")`)
		out.Printlnf(`if digits == "" {`)
		out.Printlnf(`return "0"`)
		out.Printlnf("}")
		out.Comment("The value is 0.digits times 10 to the power of point.")
		out.Printlnf("point := exp + len(intPart) - (len(all) - len(trimmed))")
		out.Printlnf("canonical := digits[:1]")
		out.Printlnf("if len(digits) > 1 {")
		out.Printlnf(`canonical += "." + digits[1:]`)
		out.Printlnf("}")
		out.Printlnf(`canonical += "e" + strconv.Itoa(point-1)`)
		out.Printlnf("if neg {")
		out.Printlnf(`canonical = "-" + canonical`)
		out.Printlnf("}")
		out.Printlnf("return json.Number(canonical)")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// uniqueItemsSourceOf returns where the uniqueItems check of struct field f
// reads it from.
func uniqueItemsSourceOf(f codegen.StructField) uniqueItemsSource {
	if f.Name == additionalProperties {
		return uniqueItemsAdditional
	}

	return uniqueItemsProperty
}

// uniqueItemsValidatorFor returns the uniqueItems check of the array or map
// that f holds, as type t, and of the arrays that holds, or nil when none of
// them declares uniqueItems. As for the element walk, a named type is declared
// separately and checks its own items.
func (g *schemaGenerator) uniqueItemsValidatorFor(
	f codegen.StructField,
	t codegen.Type,
	source uniqueItemsSource,
) validator {
	schema := f.SchemaType

	var levels []uniqueItemsLevel

	checked := 0

	for schema != nil {
		elem, elemSchema, container, ok := containerElement(t, schema)
		if !ok {
			break
		}

		levels = append(levels, uniqueItemsLevel{
			container: container,
			unique:    container == containerArray && schema.UniqueItems,
		})

		if levels[len(levels)-1].unique {
			checked = len(levels)
		}

		if pt, ok := elem.(*codegen.PointerType); ok {
			elem = pt.Type
		}

		t, schema = elem, elemSchema
	}

	if checked == 0 {
		return nil
	}

	return &uniqueItemsValidator{jsonName: f.JSONName, source: source, levels: levels[:checked]}
}
