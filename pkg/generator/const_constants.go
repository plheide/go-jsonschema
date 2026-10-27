package generator

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
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
	schema    *schemas.Type
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

		switch f.SchemaType.Const.(type) {
		case string, bool, int, float64:
			o.constProperties = append(o.constProperties, constProperty{
				typeName:  typeName,
				fieldName: f.Name,
				jsonName:  f.JSONName,
				schema:    f.SchemaType,
			})
		}
	}
}

// constConstantValue returns the value of s's const, a string, a number, an
// integer or a boolean, as the constant writes it. An integer is written
// without a fraction. A number that a float64 rounds, such as an integer
// beyond 2^53, is written as the schema writes it, which an int64 or a uint64
// holds if it is an integer; it returns false for one that is not.
func constConstantValue(s *schemas.Type) (any, bool) {
	switch v := s.Const.(type) {
	case string, bool:
		return v, true

	case int:
		return int64(v), true

	case float64:
		if n, ok := preciseConst(s).(json.Number); ok {
			return exactInteger(n)
		}

		if slices.Contains(s.Type, schemas.TypeNameInteger) && v == math.Trunc(v) &&
			v >= math.MinInt64 && v < math.MaxInt64 {
			return int64(v), true
		}

		return v, true
	}

	return nil, false
}

// exactInteger returns the integer n writes as an int64, or as a uint64 if an
// int64 cannot hold it, or false if n is not an integer either holds.
func exactInteger(n json.Number) (any, bool) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok || !r.IsInt() {
		return nil, false
	}

	switch i := r.Num(); {
	case i.IsInt64():
		return i.Int64(), true

	case i.IsUint64():
		return i.Uint64(), true
	}

	return nil, false
}

// declareConstConstants declares an exported, untyped constant for each
// recorded property, named after its struct and field, such as ShapeKind. A
// name another declaration has taken is suffixed, as an enum constant's is, and
// reported, as is a number that no Go value holds exactly, which has none.
func (g *Generator) declareConstConstants(o *output) {
	for _, p := range o.constProperties {
		value, ok := constConstantValue(p.schema)
		if !ok {
			g.warner(fmt.Sprintf(
				"Const of %s property %q: no Go value holds %v exactly, so no constant is declared for it",
				p.typeName, p.jsonName, p.schema.ConstExact,
			))

			continue
		}

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

		o.file.Package.AddDecl(&codegen.Constant{Name: candidate, Value: value})
	}

	o.constProperties = nil
}
