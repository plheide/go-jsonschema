package generator

import (
	"fmt"
	"slices"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

// emptyWhenNilFields returns the fields of struct tt that the schema requires,
// whose type does not admit null, and that hold a slice or a map. encoding/json
// writes such a field as null when it is nil, which the schema rejects.
func (g *schemaGenerator) emptyWhenNilFields(tt *codegen.StructType) []codegen.StructField {
	var fields []codegen.StructField

	for _, f := range tt.Fields {
		if slices.Contains(tt.RequiredJSONFields, f.JSONName) && isSliceOrMap(f.Type) &&
			g.schemaExcludesNullResolved(f.SchemaType) {
			fields = append(fields, f)
		}
	}

	return fields
}

// isSliceOrMap reports whether a value of t is a slice or a map, itself or
// as the type a named type declares, whose zero value is nil.
func isSliceOrMap(t codegen.Type) bool {
	switch tt := t.(type) {
	case codegen.ArrayType, *codegen.ArrayType, codegen.MapType, *codegen.MapType:
		return true

	case *codegen.NamedType:
		return tt.Decl != nil && tt.Decl.Type != nil && isSliceOrMap(tt.Decl.Type)

	case codegen.NamedType:
		return tt.Decl != nil && tt.Decl.Type != nil && isSliceOrMap(tt.Decl.Type)
	}

	return false
}

// generateEmptyWhenNilMarshaler gives decl, a struct, a MarshalJSON that writes
// each of fields as an empty array or object when it is nil, where
// encoding/json writes null. yaml.v3 writes [] and {} already.
func (g *schemaGenerator) generateEmptyWhenNilMarshaler(decl *codegen.TypeDecl, fields []codegen.StructField) {
	if g.config.OnlyModels || len(fields) == 0 {
		return
	}

	g.output.file.Package.AddImport(jsonPackage, "")

	output := g.output

	g.output.file.Package.AddDecl(&codegen.Method{
		Name: decl.GetName() + "_marshal_json",
		Impl: func(out *codegen.Emitter) error {
			// As in the unmarshal body, Plain is renamed where the type or
			// another declaration takes the name.
			tp := typePlain
			for i := 0; tp == decl.Name || !output.isUniqueTypeName(tp); i++ {
				tp = fmt.Sprintf("%s_%d", typePlain, i)
			}

			out.Commentf("MarshalJSON implements json.Marshaler. It writes a required array or object left " +
				"nil as an empty one, where encoding/json would write null, which the schema rejects.")
			out.Printlnf("func (j %s) MarshalJSON() ([]byte, error) {", decl.Name)
			out.Indent(1)
			out.Printlnf("type %s %s", tp, decl.Name)
			out.Printlnf("%s := %s(j)", varNamePlainStruct, tp)

			for _, f := range fields {
				out.Printlnf("if %s.%s == nil {", varNamePlainStruct, f.Name)
				out.Indent(1)
				out.Printf("%s.%s = ", varNamePlainStruct, f.Name)

				if err := f.Type.Generate(out); err != nil {
					return fmt.Errorf("cannot generate the empty value of %s: %w", f.Name, err)
				}

				out.Printlnf("{}")
				out.Indent(-1)
				out.Printlnf("}")
			}

			out.Printlnf("return json.Marshal(%s)", varNamePlainStruct)
			out.Indent(-1)
			out.Printlnf("}")

			return nil
		},
	})
}
