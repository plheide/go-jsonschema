package generator

import (
	"fmt"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

// numbersHelperBase names the helper that decodes JSON with the numbers of
// untyped values kept exact, declared once in each generated file that needs
// it and named after the first type whose decoder calls it, as
// integralHelperBase's is.
const numbersHelperBase = "unmarshalNumbers_"

// numbersHelper returns the name of the file's helper that decodes JSON as
// json.Unmarshal does, with the numbers of untyped values as json.Number. The
// first call declares it, named after typeName, the type whose decoder calls
// it.
func (o *output) numbersHelper(typeName string) string {
	if o.numbersHelperName != "" {
		return o.numbersHelperName
	}

	name := numbersHelperBase + typeName
	for n := 1; o.declaresName(name) || o.importsName(name); n++ {
		name = fmt.Sprintf("%s%s_%d", numbersHelperBase, typeName, n)
	}

	o.numbersHelperName = name

	for _, pkg := range []string{bytesPackage, jsonPackage} {
		o.file.Package.AddImport(pkg, "")
	}

	o.file.Package.AddDecl(&codegen.Method{Name: name, Impl: emitNumbersHelper(name)})

	return name
}

// emitNumbersHelper writes the helper named name. An input that is not valid
// JSON goes to json.Unmarshal, so that its error is json.Unmarshal's; a valid
// one is a single JSON value, which a json.Decoder reads to its end.
func emitNumbersHelper(name string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		out.Commentf("%s decodes data into v as json.Unmarshal does, but with the numbers of "+
			"interface{} values as json.Number, which keeps them exact. Every decoder in this file "+
			"calls it; it is named after the first, so that no other file of the package declares it.",
			name)
		out.Printlnf("func %s(data []byte, v interface{}) error {", name)
		out.Indent(1)
		out.Printlnf("if !json.Valid(data) {")
		out.Indent(1)
		out.Printlnf("return json.Unmarshal(data, v)")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("dec := json.NewDecoder(bytes.NewReader(data))")
		out.Printlnf("dec.UseNumber()")
		out.Printlnf("return dec.Decode(v)")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}
