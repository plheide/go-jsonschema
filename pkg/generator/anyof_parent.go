package generator

import (
	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// anyOfParent is the schema that a struct merged from an anyOf is declared on.
// Its own checks bind every instance, whichever branches match, and no
// branch's type makes them.
type anyOfParent struct {
	schema *schemas.Type
}

// anyOfParentValidators returns the checks that a struct merged from an anyOf
// owes the schema the anyOf is declared on: the properties required by the
// parent, or by every branch, and the keywords of the parent's own
// properties, which the merge keeps the parent's alone. A property that only
// branches declare is theirs to check, through the anyOf validator.
func (g *schemaGenerator) anyOfParentValidators(
	t *schemas.Type,
	tt *codegen.StructType,
	declName string,
) []validator {
	parent, ok := g.output.anyOfParents[t]
	if !ok {
		return nil
	}

	own := func(jsonName string) bool {
		_, declared := parent.schema.Properties[jsonName]

		return declared
	}

	validators := g.nullTypeValidators(parent.schema, tt, declName, true)

	for _, f := range tt.RequiredJSONFields {
		validators = append(validators, &requiredValidator{f, declName})
	}

	nullChecks := g.nullTypeValidators(parent.schema, tt, declName, false)
	nullChecks = append(nullChecks, g.enumNullValidators(tt, declName)...)

	for _, v := range nullChecks {
		switch nv := v.(type) {
		case *nonNullValidator:
			if own(nv.jsonName) {
				validators = append(validators, v)
			}

		case *nonNullElementsValidator:
			if own(nv.jsonName) {
				validators = append(validators, v)
			}
		}
	}

	for _, f := range tt.Fields {
		if own(f.JSONName) {
			validators = g.propertyValidators(validators, f, declName)
		}
	}

	return validators
}
