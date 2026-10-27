package generator

import (
	"fmt"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// checkTarget is the value a check reads and the expression its errors name
// that value with. Left empty, they are the struct field, `plain.<Field>`,
// named by its JSON name. The element walk points a check at a value an array
// or a map holds instead, such as `plain.Tags[i1]`, named
// `fmt.Sprintf("%s[%d]", "tags", i1)`.
type checkTarget struct {
	valueExpr string
	nameExpr  string
}

func (c *checkTarget) target() *checkTarget { return c }

// value returns the expression the check reads: the target, or fieldName's
// field of the Plain struct.
func (c *checkTarget) value(fieldName string) string {
	if c.valueExpr != "" {
		return c.valueExpr
	}

	return getPlainName(fieldName)
}

// name returns the expression an error names the value with: the target's,
// or def.
func (c *checkTarget) name(def string) string {
	if c.nameExpr != "" {
		return c.nameExpr
	}

	return def
}

// elementCheck is a check that the element walk can point at a value.
type elementCheck interface {
	validator
	target() *checkTarget
}

// elementLevel is one array or map the element walk enters, with the checks
// on each value it holds.
type elementLevel struct {
	container containerLevel
	// isElemNillable marks a pointer value, such as an element of []*string,
	// which is checked only when set.
	isElemNillable bool
	checks         []elementCheck
}

// elementsValidator checks the values an inline array or map holds, at every
// depth, each against its own schema: the bounds of a nested array, and the
// keywords of a string, a number or a boolean, `format` included. Its errors
// name the value as `tags[1]` or `labels["k"]`.
type elementsValidator struct {
	jsonName   string
	fieldName  string
	isNillable bool
	levels     []elementLevel
	// afterAdditionalProperties marks a check on AdditionalProperties' values,
	// which can only run once that map is decoded.
	afterAdditionalProperties bool
}

func (v *elementsValidator) generate(out *codegen.Emitter, format string) error {
	w := elementWalk{
		out:   out,
		value: getPlainName(v.fieldName),
		name:  newElementName(v.jsonName),
	}

	if v.isNillable {
		w.deref(true)
	}

	for i, level := range v.levels {
		w.enter(level.container)

		if level.isElemNillable {
			w.deref(i < len(v.levels)-1)
		}

		for _, c := range level.checks {
			*c.target() = checkTarget{valueExpr: w.value, nameExpr: w.name.expr()}

			if err := c.generate(out, format); err != nil {
				return fmt.Errorf("cannot generate element check: %w", err)
			}
		}
	}

	w.close()

	return nil
}

func (v *elementsValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:                  true,
		afterAdditionalProperties: v.afterAdditionalProperties,
	}

	for _, level := range v.levels {
		for _, c := range level.checks {
			cd := c.desc()
			d.imports = append(d.imports, cd.imports...)
			d.decls = append(d.decls, cd.decls...)
		}
	}

	return d
}

// elementWalk writes the loops that reach the values an inline array or map
// holds, one level at a time. value is the expression of the value reached,
// and name the name its errors give it.
type elementWalk struct {
	out   *codegen.Emitter
	value string
	name  elementName
	depth int
	open  int
}

// enter loops over the values the reached array or map holds.
func (w *elementWalk) enter(level containerLevel) {
	w.depth++
	iter, verb := levelIndex(level, w.depth)

	w.out.Printlnf("for %s := range %s {", iter, w.value)
	w.out.Indent(1)

	w.open++
	w.value = fmt.Sprintf("%s[%s]", w.value, iter)
	w.name = w.name.add(verb, iter)
}

// deref enters the value the reached pointer points to, where it is set.
// enclosed parenthesizes the result, for a value the walk indexes further.
func (w *elementWalk) deref(enclosed bool) {
	w.out.Printlnf("if %s != nil {", w.value)
	w.out.Indent(1)

	w.open++

	w.value = "*" + w.value
	if enclosed {
		w.value = "(" + w.value + ")"
	}
}

func (w *elementWalk) close() {
	for ; w.open > 0; w.open-- {
		w.out.Indent(-1)
		w.out.Printlnf("}")
	}
}

// levelIndex returns the loop variable that holds the index or the key of a
// value the depth-th array or map holds, and the verb that writes it into an
// error's name.
func levelIndex(level containerLevel, depth int) (string, string) {
	if level == containerMap {
		return fmt.Sprintf("k%d", depth), "[%q]"
	}

	return fmt.Sprintf("i%d", depth), "[%d]"
}

// elementName builds the expression an error names a value with: the
// property's name, then an index or a key per level, as in
// `fmt.Sprintf("%s[%d]", "grid", i1)`.
type elementName struct {
	quoted string
	format string
	args   []string
}

// newElementName names the property jsonName. The name is schema data, so it
// is escaped as a Go literal rather than spliced in, as arrayValidator's is.
func newElementName(jsonName string) elementName {
	return elementName{quoted: fmt.Sprintf(`"%s"`, goQuotedBody(jsonName))}
}

// add appends an index or a key, written by verb from the variable arg.
func (n elementName) add(verb, arg string) elementName {
	return elementName{
		quoted: n.quoted,
		format: n.format + verb,
		args:   append(append([]string(nil), n.args...), arg),
	}
}

func (n elementName) expr() string {
	if len(n.args) == 0 {
		return n.quoted
	}

	return fmt.Sprintf("fmt.Sprintf(%q, %s, %s)", "%s"+n.format, n.quoted, strings.Join(n.args, ", "))
}

// untypedConstValidator checks the `const` of a schema that declares no type,
// whose value decodes into an interface{}. JSON Schema compares the value as
// JSON, so the check compares its JSON encoding with the const's, which
// encoding/json writes with sorted keys. A property is read from the raw map,
// where an absent one is told apart from a null one, under each key the
// decoder assigns to it; an element is read where the element walk reaches
// it.
type untypedConstValidator struct {
	checkTarget
	rawKeyMatch

	jsonName string
	// encoded is the const's JSON encoding.
	encoded string
	// property marks the check on a property, read from the raw map.
	property bool
}

var _ rawKeyedValidator = new(untypedConstValidator)

func (v *untypedConstValidator) matchStructKeys(fields []codegen.StructField) {
	v.matchKeysOf(fields, v.jsonName)
}

func (v *untypedConstValidator) generate(out *codegen.Emitter, format string) error {
	value := v.valueExpr

	if v.property {
		emitRawKeyMatch(out, format, v.jsonName, v.caseVariants, v.exactOnly, false)

		value = "fieldValue"
	}

	out.Printlnf("if b, err := json.Marshal(%s); err != nil || string(b) != %q {", value, v.encoded)
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: must be equal to %%s", %s, %q)`,
		v.name(fmt.Sprintf(`"%s"`, goQuotedBody(v.jsonName))), v.encoded)
	out.Indent(-1)
	out.Printlnf("}")

	if v.property {
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

func (v *untypedConstValidator) desc() *validatorDesc {
	d := &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: v.property,
		imports:             []packageImport{{qualifiedName: jsonPackage}},
	}

	if v.property {
		d.imports = append(d.imports, v.imports()...)
	}

	return d
}

// isUntypedConst reports whether s declares a const and no type, so that its
// value is an interface{} for that reason alone, not for a composition the
// generator fell back from. A const of null cannot be told from no const.
func isUntypedConst(s *schemas.Type) bool {
	return s != nil && s.Const != nil && len(s.Type) == 0 && s.Ref == "" && s.Enum == nil &&
		len(s.OneOf) == 0 && len(s.AnyOf) == 0 && len(s.AllOf) == 0
}
