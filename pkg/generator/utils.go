package generator

import (
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
