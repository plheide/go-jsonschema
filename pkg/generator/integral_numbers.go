package generator

import (
	"fmt"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

// JSON Schema counts any number with a zero fractional part as an integer,
// however it is written: 2.0, 2e0 and 1e2 are integers. encoding/json refuses
// them for a Go integer. The JSON decoder of a type that holds an integer
// therefore retries a failed decode once, with each such number written as an
// integer. The retry runs only on failure, so input that encoding/json accepts
// decodes exactly as before. yaml.v3 accepts these numbers already.
//
// A type that holds an integer and has no decoder gets a JSON one that only
// retries, and under --use-number so does one that holds an untyped value,
// whose numbers only a generated decoder keeps exact. It is added once every
// schema is generated, since a decoder may be added to a type after its
// declaration, with a YAML one beside it.

// integralHelperBase names the helper that retries such a decode. It is
// declared once in each generated file that needs it, suffixed with the name
// of the first type whose decoder calls it. A type's name is unique in its
// package, so files of one package, generated in separate runs or written to
// standard output one at a time, never declare the helper twice.
const integralHelperBase = "unmarshalIntegral_"

// holdsInteger reports whether a value of t holds a Go integer that t's own
// decoder decodes: t itself, the element of a slice, a map or a pointer, or a
// struct field. A named type decodes its own integers. The AdditionalProperties
// field is filled from the raw map, not by encoding/json.
func holdsInteger(t codegen.Type) bool {
	return holds(t, func(leaf codegen.Type) bool {
		switch lt := leaf.(type) {
		case codegen.PrimitiveType:
			return isGoInteger(lt.Type)
		case *codegen.PrimitiveType:
			return isGoInteger(lt.Type)
		}

		return false
	})
}

// holdsUntyped reports whether a value of t holds an interface{} that t's own
// decoder decodes, as holdsInteger does for an integer. A type declared as an
// interface{} counts: it can have no decoder of its own.
func holdsUntyped(t codegen.Type) bool {
	return holds(t, func(leaf codegen.Type) bool {
		switch lt := leaf.(type) {
		case codegen.EmptyInterfaceType, *codegen.EmptyInterfaceType:
			return true
		case *codegen.NamedType:
			return lt.Decl != nil && isEmptyInterfaceDecl(lt)
		case codegen.NamedType:
			return lt.Decl != nil && isEmptyInterfaceDecl(&lt)
		}

		return false
	})
}

// holds reports whether t, the element of a slice, a map or a pointer, or a
// struct field, is a leaf type for which leaf holds. A named type is a leaf,
// which decodes its own values unless leaf says otherwise, and the
// AdditionalProperties field is left to the raw map.
func holds(t codegen.Type, leaf func(codegen.Type) bool) bool {
	switch tt := t.(type) {
	case codegen.PointerType:
		return holds(tt.Type, leaf)
	case *codegen.PointerType:
		return holds(tt.Type, leaf)
	case codegen.ArrayType:
		return holds(tt.Type, leaf)
	case *codegen.ArrayType:
		return holds(tt.Type, leaf)
	case codegen.MapType:
		return holds(tt.ValueType, leaf)
	case *codegen.MapType:
		return holds(tt.ValueType, leaf)
	case *codegen.StructType:
		for _, f := range tt.Fields {
			if f.Name != additionalProperties && holds(f.Type, leaf) {
				return true
			}
		}

		return false
	}

	return leaf(t)
}

// canHaveMethods reports whether a type declared as t can have methods: Go
// gives none to a pointer type or an interface type.
func canHaveMethods(t codegen.Type) bool {
	switch t.(type) {
	case codegen.PointerType, *codegen.PointerType, codegen.EmptyInterfaceType, *codegen.EmptyInterfaceType:
		return false
	}

	return true
}

// isGoInteger reports whether name is one of the Go integer types the
// generator emits.
func isGoInteger(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}

	return false
}

// addJSONDecoders gives each type of o that holds an integer, or under
// --use-number an untyped value, and has no decoder, a JSON one. A type
// declared as a pointer or an interface can have no methods.
func (g *Generator) addJSONDecoders(o *output) {
	if g.config.OnlyModels {
		return
	}

	jf := &jsonFormatter{useNumber: g.config.UseNumber}

	for _, d := range o.file.Package.Decls {
		decl, ok := d.(*codegen.TypeDecl)
		if !ok || !canHaveMethods(decl.Type) ||
			(!holdsInteger(decl.Type) && (!g.config.UseNumber || !holdsUntyped(decl.Type))) {
			continue
		}

		if _, done := o.unmarshallersByTypeDecl[decl]; done {
			continue
		}

		o.unmarshallersByTypeDecl[decl] = true

		jf.addImport(o.file, decl)
		o.file.Package.AddDecl(&codegen.Method{
			Impl: jf.generate(o, decl, nil),
			Name: decl.GetName() + "_validator_" + jf.getName(),
		})
	}
}

// integralHelper returns the name of the file's helper that retries a JSON
// decode with integral numbers written as integers. The first call declares
// it, named after typeName, the type whose decoder calls it.
func (o *output) integralHelper(typeName string) string {
	if o.integralHelperName != "" {
		return o.integralHelperName
	}

	name := integralHelperBase + typeName
	for n := 1; o.declaresName(name) || o.importsName(name); n++ {
		name = fmt.Sprintf("%s%s_%d", integralHelperBase, typeName, n)
	}

	o.integralHelperName = name

	for _, pkg := range []string{bytesPackage, jsonPackage, "strconv", "strings"} {
		o.file.Package.AddImport(pkg, "")
	}

	o.file.Package.AddDecl(&codegen.Method{Name: name, Impl: emitIntegralHelper(name, o.useNumber)})

	return name
}

// emitIntegralHelper writes the helper named name, and the function it calls
// to rewrite one number. The rewrite works on the number's digits, so a huge
// exponent costs nothing: a number whose integer has more than 20 digits fits
// no Go integer and is left as it is.
func emitIntegralHelper(name string, useNumber bool) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		number := name + "_number"

		out.Commentf("%s decodes data into v again, as json.Unmarshal does, after a decode that "+
			"failed with err. It first writes each number that holds an integer, such as 2.0 or "+
			"1e2, as an integer, which JSON Schema counts it as and encoding/json requires for a "+
			"Go integer. It returns err when no number needs that, and otherwise the error of the "+
			"second decode, which names a value that is still wrong. Every decoder in this file "+
			"calls it; it is named after the first, so that no other file of the package declares it.",
			name)
		out.Printlnf("func %s(data []byte, v interface{}, err error) error {", name)
		out.Indent(1)
		out.Printlnf("if !json.Valid(data) {")
		out.Printlnf("return err")
		out.Printlnf("}")
		out.Printlnf("dec := json.NewDecoder(bytes.NewReader(data))")
		out.Printlnf("dec.UseNumber()")
		out.Printlnf("var doc interface{}")
		out.Printlnf("if dec.Decode(&doc) != nil {")
		out.Printlnf("return err")
		out.Printlnf("}")
		out.Printlnf("changed := false")
		out.Printlnf("var walk func(interface{}) interface{}")
		out.Printlnf("walk = func(n interface{}) interface{} {")
		out.Indent(1)
		out.Printlnf("switch n := n.(type) {")
		out.Printlnf("case json.Number:")
		out.Indent(1)
		out.Printlnf("if s, ok := %s(string(n)); ok && s != string(n) {", number)
		out.Indent(1)
		out.Printlnf("changed = true")
		out.Printlnf("return json.Number(s)")
		out.Indent(-1)
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("case map[string]interface{}:")
		out.Indent(1)
		out.Printlnf("for k, e := range n {")
		out.Printlnf("n[k] = walk(e)")
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("case []interface{}:")
		out.Indent(1)
		out.Printlnf("for i, e := range n {")
		out.Printlnf("n[i] = walk(e)")
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("return n")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("doc = walk(doc)")
		out.Printlnf("if !changed {")
		out.Printlnf("return err")
		out.Printlnf("}")
		out.Printlnf("normalized, merr := json.Marshal(doc)")
		out.Printlnf("if merr != nil {")
		out.Printlnf("return err")
		out.Printlnf("}")

		// As the decode it retries, which keeps the numbers of untyped values
		// exact under --use-number.
		if useNumber {
			out.Printlnf("redec := json.NewDecoder(bytes.NewReader(normalized))")
			out.Printlnf("redec.UseNumber()")
			out.Printlnf("return redec.Decode(v)")
		} else {
			out.Printlnf("return json.Unmarshal(normalized, v)")
		}

		out.Indent(-1)
		out.Printlnf("}")
		out.Newline()

		out.Commentf("%s returns the integer that the JSON number s holds, written without a "+
			"fraction or an exponent, or false when s holds no integer, or one of more than 20 "+
			"digits, which fits no Go integer.", number)
		out.Printlnf("func %s(s string) (string, bool) {", number)
		out.Indent(1)
		out.Printlnf(`neg := strings.HasPrefix(s, "-")`)
		out.Printlnf(`s = strings.TrimPrefix(s, "-")`)
		out.Printlnf("mantissa, exp := s, 0")
		out.Printlnf(`if i := strings.IndexAny(s, "eE"); i >= 0 {`)
		out.Indent(1)
		out.Printlnf("e, err := strconv.Atoi(s[i+1:])")
		out.Printlnf("if err != nil || e > 40 || e < -40 {")
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Printlnf("mantissa, exp = s[:i], e")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf(`intPart, frac, _ := strings.Cut(mantissa, ".")`)
		out.Printlnf("digits := intPart + frac")
		out.Printlnf("point := len(intPart) + exp")
		out.Printlnf("start := point")
		out.Printlnf("if start < 0 {")
		out.Printlnf("start = 0")
		out.Printlnf("}")
		out.Printlnf("for i := start; i < len(digits); i++ {")
		out.Indent(1)
		out.Printlnf("if digits[i] != '0' {")
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("whole := \"\"")
		out.Printlnf("switch {")
		out.Printlnf("case point >= len(digits):")
		out.Printlnf(`whole = digits + strings.Repeat("0", point-len(digits))`)
		out.Printlnf("case point > 0:")
		out.Printlnf("whole = digits[:point]")
		out.Printlnf("}")
		out.Printlnf(`whole = strings.TrimLeft(whole, "0")`)
		out.Printlnf(`if whole == "" {`)
		out.Printlnf(`return "0", true`)
		out.Printlnf("}")
		out.Printlnf("if len(whole) > 20 {")
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Printlnf("if neg {")
		out.Printlnf(`whole = "-" + whole`)
		out.Printlnf("}")
		out.Printlnf("return whole, true")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}
