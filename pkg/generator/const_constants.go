package generator

import (
	"fmt"
	"math"
	"slices"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// constProperty is a struct property whose schema declares a primitive
// `const`, which an exported constant names.
type constProperty struct {
	typeName  string
	fieldName string
	jsonName  string
	value     any
}

// recordConstProperties records each property of struct tt, declared as
// typeName, whose schema declares a const of a string, a number, an integer or
// a boolean. Its constant is declared once every type is, so that no type
// declared later can take its name.
func (o *output) recordConstProperties(typeName string, tt *codegen.StructType) {
	for _, f := range tt.Fields {
		if f.Name == additionalProperties || f.SchemaType == nil {
			continue
		}

		if value, ok := constConstantValue(f.SchemaType); ok {
			o.constProperties = append(o.constProperties, constProperty{
				typeName:  typeName,
				fieldName: f.Name,
				jsonName:  f.JSONName,
				value:     value,
			})
		}
	}
}

// constConstantValue returns the value of s's const as the constant writes
// it, or false when it is not a string, a number, an integer or a boolean. An
// integer is written without a fraction.
func constConstantValue(s *schemas.Type) (any, bool) {
	switch v := s.Const.(type) {
	case string, bool:
		return v, true

	case int:
		return int64(v), true

	case float64:
		if slices.Contains(s.Type, schemas.TypeNameInteger) && v == math.Trunc(v) &&
			v >= math.MinInt64 && v < math.MaxInt64 {
			return int64(v), true
		}

		return v, true
	}

	return nil, false
}

// declareConstConstants declares an exported, untyped constant for each
// recorded property, named after its struct and field, such as ShapeKind. A
// name another declaration has taken is suffixed, as an enum constant's is, and
// reported.
func (g *Generator) declareConstConstants(o *output) {
	for _, p := range o.constProperties {
		name := p.typeName + p.fieldName

		candidate := name
		for n := 1; o.declaresName(candidate); n++ {
			candidate = fmt.Sprintf("%s_%d", name, n)
		}

		if candidate != name {
			g.warner(fmt.Sprintf(
				"Const of %s property %q: constant name %q is already declared in this package; declaring it as %q instead",
				p.typeName, p.jsonName, name, candidate,
			))
		}

		o.file.Package.AddDecl(&codegen.Constant{Name: candidate, Value: p.value})
	}

	o.constProperties = nil
}
