package generator

import (
	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// nullableSingleBranch returns X for a oneOf or anyOf of exactly X and
// {"type": "null"}, which says "X or null", when X is a $ref, an object or an
// array. Such a composition is generated as a pointer to X's type instead of
// interface{} or a copy of X.
//
// A primitive X keeps the shape it has: a oneOf wrapper, which tells an absent
// value from an explicit null where a pointer cannot, or for an anyOf the
// pointer it already gets. In a oneOf, X must also exclude null: a null that X
// admits would match both branches, which the oneOf rejects.
func (g *schemaGenerator) nullableSingleBranch(t *schemas.Type) (*schemas.Type, bool) {
	branches, isOneOf := t.AnyOf, false

	if len(t.OneOf) > 0 {
		if len(t.AnyOf) > 0 {
			return nil, false
		}

		branches, isOneOf = t.OneOf, true
	}

	if len(branches) != 2 || len(t.AllOf) > 0 {
		return nil, false
	}

	rest := *t
	rest.AnyOf, rest.OneOf = nil, nil

	if !isAnnotationOnly(&rest) {
		return nil, false
	}

	var (
		branch *schemas.Type
		nulls  int
	)

	for _, b := range branches {
		if isNullOnly(b) {
			nulls++

			continue
		}

		branch = b
	}

	if nulls != 1 || branch == nil {
		return nil, false
	}

	if branch.Ref == "" && !isObjectOrArray(branch) {
		return nil, false
	}

	if isOneOf && !g.schemaExcludesNullResolved(branch) {
		return nil, false
	}

	return branch, true
}

// generateNullableBranch generates "X or null" as a pointer to X's type: the
// type a $ref is declared as, or an object or array declared under scope, the
// name an anyOf of it already gave it.
func (g *schemaGenerator) generateNullableBranch(branch *schemas.Type, scope nameScope) (codegen.Type, error) {
	var (
		dt  codegen.Type
		err error
	)

	if branch.Ref != "" {
		dt, err = g.generateTypeInline(branch, scope)
	} else {
		dt, err = g.generateDeclaredType(branch, scope)
	}

	if err != nil {
		return nil, err
	}

	return codegen.WrapTypeInPointer(dt), nil
}

// isNullOnly reports whether t is {"type": "null"}, annotations aside.
func isNullOnly(t *schemas.Type) bool {
	if t == nil || len(t.Type) != 1 || t.Type[0] != schemas.TypeNameNull {
		return false
	}

	rest := *t
	rest.Type = nil

	return isAnnotationOnly(&rest)
}

// isObjectOrArray reports whether t declares a single type, object or array.
func isObjectOrArray(t *schemas.Type) bool {
	return len(t.Type) == 1 && (t.Type[0] == schemas.TypeNameObject || t.Type[0] == schemas.TypeNameArray)
}
