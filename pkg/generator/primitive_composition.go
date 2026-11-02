package generator

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// A primitive composition is a schema of one primitive type, other than null,
// whose anyOf or oneOf holds branches that only check a value of that type:
// the documented-enum pattern
//
//	{"type": "string", "oneOf": [{"const": "a", "description": "..."}, ...]}
//
// or branches of patterns, lengths or bounds. It is generated as its type, and
// primitiveCompositionValidator checks the composition on the decoded value.
// Before, an anyOf of this kind was merged into interface{}, which lost the
// type as well, and a oneOf kept the type but checked none of its branches.

// isPrimitiveComposition reports whether t is a primitive composition: of type
// string, number, integer or boolean, with no enum, $ref or allOf of its own,
// and no format that gives a string another Go type, such as date-time; and
// whose branches have no $ref, composition or conditional, and types that
// either admit every value of t's type or none of them.
func isPrimitiveComposition(t *schemas.Type) bool {
	if t == nil || len(t.Type) != 1 || len(t.AnyOf)+len(t.OneOf) == 0 {
		return false
	}

	switch t.Type[0] {
	case schemas.TypeNameString:
		if formatTypesString(t.Format) {
			return false
		}

	case schemas.TypeNameNumber, schemas.TypeNameInteger, schemas.TypeNameBoolean:

	default:
		return false
	}

	if t.Enum != nil || t.Ref != "" || len(t.AllOf) > 0 ||
		(t.GoJSONSchemaExtension != nil && t.GoJSONSchemaExtension.Type != nil) {
		return false
	}

	for _, b := range slices.Concat(t.AnyOf, t.OneOf) {
		if b == nil || b.Ref != "" || len(b.AllOf)+len(b.AnyOf)+len(b.OneOf) > 0 ||
			b.Not != nil || b.If != nil || b.Then != nil || b.Else != nil ||
			branchTypeMatchOf(t.Type[0], b) == branchTypeSome {
			return false
		}
	}

	return true
}

// formatTypesString reports whether format gives a string a Go type other than
// string, as date-time gives time.Time.
func formatTypesString(format string) bool {
	t, err := codegen.PrimitiveTypeFromJSONSchemaType(schemas.TypeNameString, format, false, false, nil, nil, nil, nil)
	_, isString := t.(codegen.PrimitiveType)

	return err != nil || !isString
}

// branchTypeMatch is how many of the values of a primitive's type a branch's
// own type admits.
type branchTypeMatch int

const (
	// branchTypeAll is a branch with no type, or with one that every value of
	// the primitive's type has.
	branchTypeAll branchTypeMatch = iota
	// branchTypeNone is a branch whose type no value of the primitive's type
	// has.
	branchTypeNone
	// branchTypeSome is an integer branch of a number, which only some of its
	// values match.
	branchTypeSome
)

func branchTypeMatchOf(primitive string, b *schemas.Type) branchTypeMatch {
	switch {
	case len(b.Type) == 0, slices.Contains(b.Type, primitive):
		return branchTypeAll

	case primitive == schemas.TypeNameInteger && slices.Contains(b.Type, schemas.TypeNameNumber):
		return branchTypeAll

	case primitive == schemas.TypeNameNumber && slices.Contains(b.Type, schemas.TypeNameInteger):
		return branchTypeSome
	}

	return branchTypeNone
}

// compositionValue is a value that a branch lists, with const or enum: as a Go
// literal of the composition's Go type, which is also what tells two values
// apart, and as JSON, for errors.
type compositionValue struct {
	literal string
	json    string
}

// compositionBranch is a branch of a primitive composition, as the checks it
// makes on a value.
type compositionBranch struct {
	// never marks a branch that no value of the type matches.
	never bool
	// listed marks a branch that lists the values it admits, with const or
	// enum, and values holds those that the Go type can hold.
	listed bool
	values []compositionValue
	checks []elementCheck
}

// always reports whether every value of the type matches the branch.
func (b *compositionBranch) always() bool {
	return !b.never && !b.listed && len(b.checks) == 0
}

// onlyLists reports whether the branch checks nothing but the values it lists.
func (b *compositionBranch) onlyLists() bool {
	return b.never || (b.listed && len(b.checks) == 0)
}

// primitiveCompositionValidator checks the anyOf or the oneOf of a primitive
// composition on the decoded value. Each branch's checks run in a function of
// their own, and the branches that pass are counted: an anyOf needs one, and a
// oneOf exactly one. Where every branch only lists values, the value must be
// one that a branch of the anyOf lists, or that just one branch of the oneOf
// does.
type primitiveCompositionValidator struct {
	checkTarget

	jsonName   string
	fieldName  string
	isNillable bool
	keyword    string
	branches   []compositionBranch
	// goType is the Go type of the value.
	goType string
}

// primitiveCompositionValidators returns the checks of the anyOf and the oneOf
// of field f's schema, when that is a primitive composition, whose values have
// Go type goType.
func (g *schemaGenerator) primitiveCompositionValidators(
	f codegen.StructField,
	goType string,
	isNillable bool,
) []validator {
	s := f.SchemaType
	if !isPrimitiveComposition(s) {
		return nil
	}

	var validators []validator

	for _, c := range []struct {
		keyword  string
		branches []*schemas.Type
	}{{keywordAnyOf, s.AnyOf}, {keywordOneOf, s.OneOf}} {
		if v := g.primitiveCompositionValidator(f, goType, isNillable, c.keyword, c.branches); v != nil {
			validators = append(validators, v)
		}
	}

	return validators
}

// primitiveCompositionValidator returns the check of the composition keyword
// of field f's schema, whose branches are branches, or nil if it has none to
// make. A composition with a branch that cannot be checked here is left
// unchecked, with a warning, and so is an anyOf that a branch makes hold for
// every value.
func (g *schemaGenerator) primitiveCompositionValidator(
	f codegen.StructField,
	goType string,
	isNillable bool,
	keyword string,
	branches []*schemas.Type,
) validator {
	if len(branches) == 0 {
		return nil
	}

	v := &primitiveCompositionValidator{
		jsonName:   f.JSONName,
		fieldName:  f.Name,
		isNillable: isNillable,
		keyword:    keyword,
		goType:     goType,
	}

	for i, b := range branches {
		branch, reason := g.compositionBranchOf(f.SchemaType.Type[0], goType, b, f.JSONName)
		if reason != "" {
			g.warner(fmt.Sprintf("schema fidelity: %s of %q is not checked: its branch %d %s",
				keyword, f.JSONName, i, reason))

			return nil
		}

		if keyword == keywordAnyOf && branch.always() {
			return nil
		}

		v.branches = append(v.branches, branch)
	}

	return v
}

// compositionBranchOf returns branch b of a primitive composition of type
// primitive, whose values have Go type goType, as the checks it makes, or the
// reason it cannot be checked. owner names the composition's property in
// warnings and errors.
func (g *schemaGenerator) compositionBranchOf(
	primitive, goType string,
	b *schemas.Type,
	owner string,
) (compositionBranch, string) {
	if branchTypeMatchOf(primitive, b) == branchTypeNone {
		return compositionBranch{never: true}, ""
	}

	var branch compositionBranch

	branch.values, branch.listed = listedValues(primitive, goType, b)
	if branch.listed && len(branch.values) == 0 {
		return compositionBranch{never: true}, ""
	}

	switch primitive {
	case schemas.TypeNameString:
		pattern := g.compilablePattern(b.Pattern, owner)
		if b.Pattern != "" && pattern == "" {
			return branch, "has a pattern that Go's regexp cannot compile"
		}

		if b.MinLength != 0 || b.HasMaxLength() || pattern != "" {
			branch.checks = append(branch.checks, &stringValidator{
				jsonName:     owner,
				minLength:    b.MinLength,
				maxLength:    b.MaxLength,
				maxLengthSet: b.HasMaxLength(),
				pattern:      pattern,
			})
		}

		switch {
		case b.Format == "":

		case isKnownFormatKeyword(b.Format) && g.config.FormatValidation.shouldValidate(b.Format):
			branch.checks = append(branch.checks, &formatValidator{jsonName: owner, format: b.Format})

		case formatTypesString(b.Format):
			// A field of the format has its Go type check it, which a
			// branch of a string cannot have.
			return branch, fmt.Sprintf("has format %q, which is checked only through a field's Go type", b.Format)
		}

	case schemas.TypeNameNumber, schemas.TypeNameInteger:
		numeric := newNumericValidator(codegen.StructField{SchemaType: b, JSONName: owner}, goType, false)

		// The values the branch lists are checked with the others.
		numeric.constVal = nil

		if numeric.checksSomething() {
			branch.checks = append(branch.checks, numeric)
		}
	}

	return branch, ""
}

// listedValues returns the values of the primitive's type that branch b
// lists, with const and enum, those both list if it has both, as Go type
// goType holds them; and whether b lists any values at all.
func listedValues(primitive, goType string, b *schemas.Type) ([]compositionValue, bool) {
	var lists [][]any

	if b.Const != nil || b.ConstIsSet {
		lists = append(lists, []any{preciseConst(b)})
	}

	if b.Enum != nil {
		lists = append(lists, b.Enum)
	}

	if len(lists) == 0 {
		return nil, false
	}

	var values []compositionValue

	for i, list := range lists {
		var these []compositionValue

		for _, x := range list {
			if cv, ok := compositionValueOf(primitive, goType, x); ok && !containsValue(these, cv) {
				these = append(these, cv)
			}
		}

		if i == 0 {
			values = these

			continue
		}

		values = slices.DeleteFunc(values, func(cv compositionValue) bool { return !containsValue(these, cv) })
	}

	return values, true
}

func containsValue(values []compositionValue, cv compositionValue) bool {
	return slices.ContainsFunc(values, func(o compositionValue) bool { return o.literal == cv.literal })
}

// compositionValueOf returns x, a value that a branch lists, as a value of the
// primitive's type that Go type goType holds, or false if it is none.
func compositionValueOf(primitive, goType string, x any) (compositionValue, bool) {
	switch primitive {
	case schemas.TypeNameString:
		s, ok := x.(string)
		if !ok {
			return compositionValue{}, false
		}

		encoded, err := json.Marshal(s)
		if err != nil {
			return compositionValue{}, false
		}

		return compositionValue{literal: strconv.Quote(s), json: string(encoded)}, true

	case schemas.TypeNameBoolean:
		b, ok := x.(bool)
		if !ok {
			return compositionValue{}, false
		}

		return compositionValue{literal: strconv.FormatBool(b), json: strconv.FormatBool(b)}, true

	case schemas.TypeNameNumber:
		f, ok := floatValue(x)
		if !ok {
			return compositionValue{}, false
		}

		// JSON Schema counts -0 as 0, and Go's constants have no -0, so
		// written as such it would be the same case twice.
		if f == 0 {
			f = 0
		}

		text := strconv.FormatFloat(f, 'g', -1, 64)

		return compositionValue{literal: text, json: text}, true

	case schemas.TypeNameInteger:
		n, ok := integerValue(x)
		if !ok {
			return compositionValue{}, false
		}

		if lo, hi := integerRange(goType); n.Cmp(lo) < 0 || n.Cmp(hi) > 0 {
			return compositionValue{}, false
		}

		return compositionValue{literal: n.String(), json: n.String()}, true
	}

	return compositionValue{}, false
}

// floatValue returns the number x as a float64, which is how a value of a
// number is decoded.
func floatValue(x any) (float64, bool) {
	switch n := x.(type) {
	case float64:
		return n, true

	case json.Number:
		f, err := strconv.ParseFloat(string(n), 64)

		return f, err == nil
	}

	return 0, false
}

// integerValue returns the number x as an integer, or false if it has a
// fraction.
func integerValue(x any) (*big.Int, bool) {
	switch n := x.(type) {
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) || n != math.Trunc(n) {
			return nil, false
		}

		i, _ := big.NewFloat(n).Int(nil)

		return i, true

	case json.Number:
		r, ok := boundValue(string(n))
		if !ok || !r.IsInt() {
			return nil, false
		}

		return r.Num(), true
	}

	return nil, false
}

// listedOnly returns, when every branch only lists values, the values that the
// composition admits: those that a branch of an anyOf lists, or that just one
// branch of a oneOf does.
func (v *primitiveCompositionValidator) listedOnly() ([]compositionValue, bool) {
	var all []compositionValue

	branches := map[string]int{}

	for _, b := range v.branches {
		if !b.onlyLists() {
			return nil, false
		}

		for _, cv := range b.values {
			if branches[cv.literal] == 0 {
				all = append(all, cv)
			}

			branches[cv.literal]++
		}
	}

	if v.keyword == keywordOneOf {
		all = slices.DeleteFunc(all, func(cv compositionValue) bool { return branches[cv.literal] > 1 })
	}

	return all, true
}

func (v *primitiveCompositionValidator) generate(out *codegen.Emitter, format string) error {
	value := v.value(v.fieldName)
	name := v.name(fmt.Sprintf(`"%s"`, goQuotedBody(v.jsonName)))
	allowed, listedOnly := v.listedOnly()
	blocks := 0

	if v.isNillable {
		out.Printlnf("if %s != nil {", value)
		out.Indent(1)

		blocks++

		value = "*" + value
	}

	if listedOnly {
		if len(allowed) == 0 {
			// No value of the type matches the composition.
			out.Printlnf("switch %s {", value)
			out.Printlnf("default:")
			out.Indent(1)
			out.Printlnf(`return fmt.Errorf("field %%s: no value of its type matches the %s", %s)`, v.keyword, name)
			out.Indent(-1)
			out.Printlnf("}")
		} else {
			generateListedCheck(out, v.switchOn(value), name, allowed)
		}
	} else {
		// The count is local to the check.
		if blocks == 0 {
			out.Printlnf("{")
			out.Indent(1)

			blocks++
		}

		if err := v.generateCount(out, format, value, name); err != nil {
			return err
		}
	}

	for ; blocks > 0; blocks-- {
		out.Indent(-1)
		out.Printlnf("}")
	}

	return nil
}

// switchOn returns the expression that a check of listed values switches on:
// value, or for an int or a uint, value as an int64 or a uint64. An int has 32
// bits on some platforms, where a listed value that needs 64 would not compile
// as a case of an int.
func (v *primitiveCompositionValidator) switchOn(value string) string {
	switch v.goType {
	case "int":
		return "int64(" + value + ")"

	case "uint":
		return "uint64(" + value + ")"
	}

	return value
}

// generateListedCheck writes the check that value, named name in its error, is
// one of allowed.
func generateListedCheck(out *codegen.Emitter, value, name string, allowed []compositionValue) {
	literals := make([]string, len(allowed))
	encoded := make([]string, len(allowed))

	for i, cv := range allowed {
		literals[i] = cv.literal
		encoded[i] = cv.json
	}

	out.Printlnf("switch %s {", value)
	out.Printlnf("case %s:", strings.Join(literals, ", "))
	out.Printlnf("default:")
	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: must be one of %%s", %s, %s)`,
		name, strconv.Quote("["+strings.Join(encoded, ",")+"]"))
	out.Indent(-1)
	out.Printlnf("}")
}

// generateCount writes the count of the branches that value, named name in
// errors, matches, and the check of the count.
func (v *primitiveCompositionValidator) generateCount(out *codegen.Emitter, format, value, name string) error {
	counter := v.keyword + "Matches"
	errs := v.keyword + "Errs"

	out.Printlnf("%s := 0", counter)
	out.Printlnf("var %s []error", errs)

	for _, b := range v.branches {
		if b.never {
			continue
		}

		if b.always() {
			out.Printlnf("%s++", counter)

			continue
		}

		out.Printlnf("if err := func() error {")
		out.Indent(1)

		for _, c := range b.checks {
			*c.target() = checkTarget{valueExpr: value, nameExpr: name}

			if err := c.generate(out, format); err != nil {
				return fmt.Errorf("cannot generate %s branch check: %w", v.keyword, err)
			}
		}

		if b.listed {
			generateListedCheck(out, v.switchOn(value), name, b.values)
		}

		out.Printlnf("return nil")
		out.Indent(-1)
		out.Printlnf("}(); err != nil {")
		out.Indent(1)
		out.Printlnf("%s = append(%s, err)", errs, errs)
		out.Indent(-1)
		out.Printlnf("} else {")
		out.Indent(1)
		out.Printlnf("%s++", counter)
		out.Indent(-1)
		out.Printlnf("}")
	}

	out.Printlnf("if %s == 0 {", counter)
	out.Indent(1)

	if v.keyword == keywordAnyOf {
		out.Printlnf(`return fmt.Errorf("field %%s: must match at least one anyOf branch: %%w", %s, errors.Join(%s...))`,
			name, errs)
	} else {
		out.Printlnf(
			`return fmt.Errorf("field %%s: must match exactly one oneOf branch, matched none: %%w", %s, errors.Join(%s...))`,
			name, errs)
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if %s > 1 {", counter)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("field %%s: must match exactly one oneOf branch, matched %%d", %s, %s)`,
			name, counter)
	}

	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

func (v *primitiveCompositionValidator) desc() *validatorDesc {
	d := &validatorDesc{hasError: true}

	if _, listedOnly := v.listedOnly(); !listedOnly {
		d.imports = append(d.imports, packageImport{qualifiedName: "errors"})
	}

	for _, b := range v.branches {
		for _, c := range b.checks {
			cd := c.desc()
			d.imports = append(d.imports, cd.imports...)
			d.decls = append(d.decls, cd.decls...)

			// The field checks have these imports added where they are made.
			switch check := c.(type) {
			case *stringValidator:
				if check.pattern != "" {
					d.imports = append(d.imports, packageImport{qualifiedName: "regexp"})
				}

			case *numericValidator:
				if check.multipleOf != nil && !check.roundToInt {
					d.imports = append(d.imports, packageImport{qualifiedName: "math"})
				}
			}
		}
	}

	return d
}
