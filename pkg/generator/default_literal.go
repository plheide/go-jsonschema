package generator

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sanity-io/litter"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

// defaultLiteral writes value, a decoded JSON default, as a Go expression of
// type t, or reports false when it cannot be one: a string for a boolean, an
// object for a list, a key the struct has no field for, or any value for an
// opaque type such as time.Time. Written as it is, such a default would only
// produce code that does not compile.
//
// The value is written for its target's type at every level. A struct's
// fields are found by their JSON names, a pointer field gets an address, and
// a nested struct, map or list gets a literal of its own type rather than of
// the JSON value's. Where the literal written before compiled, it is
// unchanged.
func defaultLiteral(value any, t codegen.Type) (string, bool) {
	// A value the generator made itself, such as the empty map that starts
	// the additionalProperties field, is already of its field's Go type.
	if !isDecodedJSON(value) {
		return litter.Sdump(value), true
	}

	switch tt := t.(type) {
	case codegen.PointerType:
		return pointerLiteral(value, tt.Type)
	case *codegen.PointerType:
		return pointerLiteral(value, tt.Type)
	case codegen.NamedType:
		return namedLiteral(value, &tt)
	case *codegen.NamedType:
		return namedLiteral(value, tt)
	case codegen.PrimitiveType:
		return primitiveLiteral(value, tt.Type)
	case *codegen.PrimitiveType:
		return primitiveLiteral(value, tt.Type)
	case codegen.ArrayType:
		return listLiteral(value, typeString(t), tt.Type)
	case *codegen.ArrayType:
		return listLiteral(value, typeString(t), tt.Type)
	case codegen.MapType:
		return mapLiteral(value, typeString(t), tt)
	case *codegen.MapType:
		return mapLiteral(value, typeString(t), *tt)
	case codegen.EmptyInterfaceType, *codegen.EmptyInterfaceType:
		return litter.Sdump(value), true
	case codegen.CustomNameType, *codegen.CustomNameType:
		// A type named by the goJSONSchema.type extension. Its kind is the
		// user's, so the value is written as it always was.
		return litter.Sdump(value), true
	}

	return "", false
}

// isDecodedJSON reports whether value has one of the dynamic types
// encoding/json decodes a JSON value into.
func isDecodedJSON(value any) bool {
	switch value.(type) {
	case nil, bool, float64, string, []any, map[string]any:
		return true
	}

	return false
}

// pointerLiteral writes value for a pointer to elem. A composite literal can
// have its address taken; any other value goes through a typed variable.
func pointerLiteral(value any, elem codegen.Type) (string, bool) {
	if value == nil {
		return "nil", true
	}

	lit, ok := defaultLiteral(value, elem)
	if !ok {
		return "", false
	}

	if isCompositeType(elem) {
		return "&" + lit, true
	}

	return fmt.Sprintf("func() *%[1]s { var v %[1]s = %[2]s; return &v }()", typeString(elem), lit), true
}

// isCompositeType reports whether t's values are written as composite
// literals: a struct, a map or a list, named or not.
func isCompositeType(t codegen.Type) bool {
	switch tt := t.(type) {
	case codegen.ArrayType, *codegen.ArrayType, codegen.MapType, *codegen.MapType, *codegen.StructType:
		return true
	case codegen.NamedType:
		return tt.Decl.Type != nil && isCompositeType(tt.Decl.Type)
	case *codegen.NamedType:
		return tt.Decl.Type != nil && isCompositeType(tt.Decl.Type)
	}

	return false
}

func namedLiteral(value any, nt *codegen.NamedType) (string, bool) {
	if isEmptyInterfaceDecl(nt) {
		return litter.Sdump(value), true
	}

	name := typeString(nt)

	switch dt := nt.Decl.Type.(type) {
	case *codegen.StructType:
		return structLiteral(value, name, dt)
	case codegen.MapType:
		return mapLiteral(value, name, dt)
	case *codegen.MapType:
		return mapLiteral(value, name, *dt)
	case codegen.ArrayType:
		return listLiteral(value, name, dt.Type)
	case *codegen.ArrayType:
		return listLiteral(value, name, dt.Type)
	case codegen.PrimitiveType:
		return primitiveLiteral(value, dt.Type)
	case *codegen.PrimitiveType:
		return primitiveLiteral(value, dt.Type)
	}

	// No declaration from another package to look into: a type such as
	// time.Time or netip.Addr, which no JSON value is a literal of.
	if nt.Package != nil && nt.Decl.Type == nil {
		return "", false
	}

	// A declaration of this package whose kind is not known here, such as a
	// placeholder the field still points at. The value is written as it
	// always was.
	return litter.Sdump(value), true
}

func structLiteral(value any, name string, st *codegen.StructType) (string, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return "", false
	}

	var b strings.Builder

	b.WriteString(name + "{\n")

	for _, key := range sortedKeys(m) {
		field := structFieldByJSONName(st, key)
		if field == nil {
			return "", false
		}

		lit, ok := defaultLiteral(m[key], field.Type)
		if !ok {
			return "", false
		}

		fmt.Fprintf(&b, "%s: %s,\n", field.Name, lit)
	}

	b.WriteString("}")

	return b.String(), true
}

func structFieldByJSONName(st *codegen.StructType, jsonName string) *codegen.StructField {
	for i := range st.Fields {
		if st.Fields[i].JSONName == jsonName && st.Fields[i].Name != "" {
			return &st.Fields[i]
		}
	}

	return nil
}

func mapLiteral(value any, name string, mt codegen.MapType) (string, bool) {
	m, ok := value.(map[string]any)
	if !ok || !isStringType(mt.KeyType) {
		return "", false
	}

	var b strings.Builder

	b.WriteString(name + "{\n")

	for _, key := range sortedKeys(m) {
		lit, ok := defaultLiteral(m[key], mt.ValueType)
		if !ok {
			return "", false
		}

		fmt.Fprintf(&b, "%s: %s,\n", strconv.Quote(key), lit)
	}

	b.WriteString("}")

	return b.String(), true
}

func listLiteral(value any, name string, elem codegen.Type) (string, bool) {
	l, ok := value.([]any)
	if !ok {
		return "", false
	}

	var b strings.Builder

	b.WriteString(name + "{\n")

	for _, e := range l {
		lit, ok := defaultLiteral(e, elem)
		if !ok {
			return "", false
		}

		b.WriteString(lit + ",\n")
	}

	b.WriteString("}")

	return b.String(), true
}

// primitiveLiteral writes value for a predeclared Go type. An integer type
// takes a JSON number's integer part, as a default always has; one outside
// the type's range would not compile. A type name it does not know is
// written as before.
func primitiveLiteral(value any, name string) (string, bool) {
	switch {
	case name == "bool":
		b, ok := value.(bool)

		return strconv.FormatBool(b), ok

	case name == "string":
		s, ok := value.(string)
		if !ok {
			return "", false
		}

		return litter.Sdump(s), true

	case isGoIntegerTypeName(name):
		f, ok := value.(float64)
		if !ok {
			return "", false
		}

		f = math.Trunc(f)

		lo, end := integerTypeRange(name)
		if f < lo || f >= end {
			return "", false
		}

		// Exact digits: the shortest form that round-trips as a float64,
		// which -1 asks for, is a different integer above 2^53.
		return strconv.FormatFloat(f, 'f', 0, 64), true

	case name == "float64":
		f, ok := value.(float64)
		if !ok {
			return "", false
		}

		return litter.Sdump(f), true
	}

	return litter.Sdump(value), true
}

// integerTypeRange returns the lowest value of a Go integer type and the power
// of two just past its highest, with int and uint taken as 64 bits wide. A
// float64 holds both exactly, where it cannot hold the highest value of a
// 64-bit type: math.MaxInt64 rounds up to 2^63.
func integerTypeRange(name string) (float64, float64) {
	switch name {
	case "int8":
		return math.MinInt8, 1 << 7
	case "int16":
		return math.MinInt16, 1 << 15
	case "int32":
		return math.MinInt32, 1 << 31
	case "uint8":
		return 0, 1 << 8
	case "uint16":
		return 0, 1 << 16
	case "uint32":
		return 0, 1 << 32
	case "uint", "uint64":
		return 0, 1 << 64
	}

	return math.MinInt64, 1 << 63
}

// typeString is t as Go source, qualified by its package where it has one.
func typeString(t codegen.Type) string {
	e := codegen.NewEmitter(0)
	if err := t.Generate(e); err != nil {
		return ""
	}

	return strings.TrimSpace(e.String())
}
