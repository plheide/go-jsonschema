package generator

import (
	"slices"
	"sort"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

const additionalProperties = "AdditionalProperties"

func sortedKeys[T any](props map[string]T) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func sortDefinitionsByName(defs schemas.Definitions) []string {
	names := make([]string, 0, len(defs))

	for name := range defs {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// namedDecl returns the declaration t names, directly or through a pointer, or
// nil when t is not a named type.
func namedDecl(t codegen.Type) *codegen.TypeDecl {
	switch x := t.(type) {
	case *codegen.NamedType:
		return x.Decl

	case *codegen.PointerType:
		if nt, ok := x.Type.(*codegen.NamedType); ok {
			return nt.Decl
		}
	}

	return nil
}

// resolvesToRef reports whether generateType renders t as the type its $ref
// points to, rather than as an enum or a goJSONSchema type override.
func resolvesToRef(t *schemas.Type) bool {
	if t.Ref == "" || t.Enum != nil {
		return false
	}

	ext := t.GoJSONSchemaExtension

	return ext == nil || ext.Type == nil
}

func isNamedType(t codegen.Type) bool {
	switch x := t.(type) {
	case *codegen.NamedType:
		return true

	case *codegen.PointerType:
		if _, ok := x.Type.(*codegen.NamedType); ok {
			return true
		}
	}

	return false
}

func isMapType(t codegen.Type) bool {
	_, isMapType := t.(*codegen.MapType)

	return isMapType
}

// mergesIntoObject reports whether merging parent's anyOf branches yields an
// object: the merge keeps the first type list it meets, the parent's before
// the branches'.
func mergesIntoObject(parent *schemas.Type, branches []*schemas.Type) bool {
	if len(parent.Type) > 0 {
		return slices.Contains(parent.Type, schemas.TypeNameObject)
	}

	for _, b := range branches {
		if len(b.Type) > 0 {
			return slices.Contains(b.Type, schemas.TypeNameObject)
		}
	}

	return false
}

// mixesObjectAndNonObject reports whether some of parent's anyOf branches
// admit objects while others admit a primitive or an array.
func mixesObjectAndNonObject(parent *schemas.Type, branches []*schemas.Type) bool {
	var object, other bool

	for _, b := range branches {
		o, n := branchAdmits(parent, b)
		object = object || o
		other = other || n
	}

	return object && other
}

// branchAdmits reports whether a resolved anyOf branch admits objects and
// whether it admits anything besides objects and null. A branch without a type
// takes its parent's, as generateAnyOfType gives it. Without either, object
// keywords make it an object, while the empty schema, a bare not, and an enum
// or const of non-objects admit other values too; anything else, such as a
// branch listing only required, counts as neither.
func branchAdmits(parent, b *schemas.Type) (bool, bool) {
	types := b.Type
	if len(types) == 0 {
		types = parent.Type
	}

	if len(types) > 0 {
		var object, other bool

		for _, name := range types {
			switch name {
			case schemas.TypeNameObject:
				object = true

			case schemas.TypeNameNull:

			default:
				other = true
			}
		}

		return object, other
	}

	if len(b.Properties) > 0 || len(b.PatternProperties) > 0 || b.AdditionalProperties != nil {
		return true, false
	}

	rest := *b
	rest.Not = nil

	if isAnnotationOnly(&rest) {
		return false, true
	}

	values := make([]any, 0, len(b.Enum)+1)
	values = append(values, b.Enum...)

	if b.Const != nil {
		values = append(values, b.Const)
	}

	for _, v := range values {
		if _, isObject := v.(map[string]any); !isObject && v != nil {
			return false, true
		}
	}

	return false, false
}
