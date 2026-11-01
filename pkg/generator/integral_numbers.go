package generator

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

// JSON Schema counts any number with a zero fractional part as an integer,
// however it is written: 2.0, 2e0 and 1e2 are integers. encoding/json refuses
// them for a Go integer, and yaml.v3 decodes a float into one by truncating
// it, so that 2.5 becomes 2. The decoders of a type that holds an integer
// therefore write such a number as the integer it holds wherever the type
// holds an integer, at its integer paths:
//
//   - The JSON decoder retries a failed decode once, with each such number
//     written as an integer and every other byte as it was. The retry runs
//     only on failure, so input that encoding/json accepts decodes exactly as
//     before.
//   - The YAML decoder writes each such float as an integer before it
//     decodes, and refuses a float that holds no integer, or one that the Go
//     integer type can't hold.
//
// A type that holds an integer and has no decoder gets a JSON and a YAML one
// that only do this, and under --use-number one that holds an untyped value
// gets a JSON one, whose numbers only a generated decoder keeps exact. They
// are added once every schema is generated, since a decoder may be added to a
// type after its declaration.

// integralHelperBase names the helpers that write such numbers as integers.
// They are declared once in each generated file that needs them, suffixed with
// the name of the first type whose decoder calls them. A type's name is unique
// in its package, so files of one package, generated in separate runs or
// written to standard output one at a time, never declare them twice.
const integralHelperBase = "unmarshalIntegral_"

// integerPathsBase names a variable that holds the integer paths of a
// decoder's type.
const integerPathsBase = "integerPaths_"

// integerPath is where a value of a Go type holds the values that its own
// decoder decodes and a leaf names a kind for: the value itself, or the values
// below it, in its items, its values or its fields. A field is named as the
// format binds it, and a nil one holds none, but has a name that a JSON key
// matching another field's when case is ignored can match too.
type integerPath struct {
	kind   string
	items  *integerPath
	values *integerPath
	fields map[string]*integerPath
}

// integerPaths returns where a value of t holds a Go integer that t's own
// decoder decodes in format, or nil when it holds none.
func integerPaths(t codegen.Type, format string) *integerPath {
	return (&pathFinder{format: format, leaf: integerLeaf, inside: map[*codegen.TypeDecl]bool{}}).paths(t)
}

// holdsInteger reports whether a value of t holds a Go integer that t's own
// decoder decodes.
func holdsInteger(t codegen.Type) bool {
	return integerPaths(t, formatJSON) != nil
}

// holdsUntyped reports whether a value of t holds an interface{} that t's own
// decoder decodes, as holdsInteger does for an integer. A type declared as an
// interface{} counts: it can have no decoder of its own.
func holdsUntyped(t codegen.Type) bool {
	return (&pathFinder{format: formatJSON, leaf: untypedLeaf, inside: map[*codegen.TypeDecl]bool{}}).paths(t) != nil
}

// integerLeaf names the Go integer type that t is, if it is one.
func integerLeaf(t codegen.Type) string {
	switch tt := t.(type) {
	case codegen.PrimitiveType:
		if isGoInteger(tt.Type) {
			return tt.Type
		}
	case *codegen.PrimitiveType:
		if isGoInteger(tt.Type) {
			return tt.Type
		}
	}

	return ""
}

// untypedLeaf names t when it is an interface{}.
func untypedLeaf(t codegen.Type) string {
	switch t.(type) {
	case codegen.EmptyInterfaceType, *codegen.EmptyInterfaceType:
		return interfaceTypeName
	}

	return ""
}

// pathFinder finds where a value of a Go type holds the values that leaf names
// a kind for, as a decoder for format decodes it: in the value itself, the
// target of a pointer, the items of a slice, the values of a map, and the
// fields of a struct but AdditionalProperties, which is decoded on its own. A
// named type decodes its own values, but one declared as a pointer or an
// interface can have no methods, so its values are decoded as its declared
// type's are.
type pathFinder struct {
	format string
	leaf   func(codegen.Type) string
	// inside holds the named types being looked into, which a type declared as
	// a pointer to itself would otherwise enter forever.
	inside map[*codegen.TypeDecl]bool
}

func (f *pathFinder) paths(t codegen.Type) *integerPath {
	if kind := f.leaf(t); kind != "" {
		return &integerPath{kind: kind}
	}

	switch tt := t.(type) {
	case codegen.PointerType:
		return f.paths(tt.Type)
	case *codegen.PointerType:
		return f.paths(tt.Type)
	case codegen.ArrayType:
		return f.items(tt.Type)
	case *codegen.ArrayType:
		return f.items(tt.Type)
	case codegen.MapType:
		return f.values(tt.ValueType)
	case *codegen.MapType:
		return f.values(tt.ValueType)
	case *codegen.StructType:
		return f.fields(tt)
	case codegen.NamedType:
		return f.named(&tt)
	case *codegen.NamedType:
		return f.named(tt)
	}

	return nil
}

func (f *pathFinder) items(t codegen.Type) *integerPath {
	if p := f.paths(t); p != nil {
		return &integerPath{items: p}
	}

	return nil
}

func (f *pathFinder) values(t codegen.Type) *integerPath {
	if p := f.paths(t); p != nil {
		return &integerPath{values: p}
	}

	return nil
}

func (f *pathFinder) fields(st *codegen.StructType) *integerPath {
	fields := map[string]*integerPath{}

	var others []string

	for i := range st.Fields {
		name, bound := boundName(&st.Fields[i], f.format)
		if isAdditionalPropertiesField(&st.Fields[i]) || !bound {
			continue
		}

		if p := f.paths(st.Fields[i].Type); p != nil {
			fields[name] = p
		} else {
			others = append(others, name)
		}
	}

	if len(fields) == 0 {
		return nil
	}

	// encoding/json binds a key to the field of the same name, or else to the
	// first field, in name order, whose name it matches when case is ignored.
	// A field that holds none is kept where such a key could find it instead.
	if f.format == formatJSON {
		names := slices.Collect(maps.Keys(fields))

		for _, name := range others {
			if slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
				fields[name] = nil
			}
		}
	}

	return &integerPath{fields: fields}
}

func (f *pathFinder) named(t *codegen.NamedType) *integerPath {
	if t.Decl == nil || canHaveMethods(t.Decl.Type) || f.inside[t.Decl] {
		return nil
	}

	f.inside[t.Decl] = true
	defer delete(f.inside, t.Decl)

	return f.paths(t.Decl.Type)
}

// boundName returns the key that a decoder for format binds to the struct
// field f, and false when it binds none: the name its tag gives, or else its
// Go name, lowercased for YAML, as encoding/json and yaml.v3 do.
func boundName(f *codegen.StructField, format string) (string, bool) {
	tag := reflect.StructTag(f.Tags).Get(format)
	if tag == "-" {
		return "", false
	}

	if name, _, _ := strings.Cut(tag, ","); name != "" {
		return name, true
	}

	if format == formatYAML {
		return strings.ToLower(f.Name), true
	}

	return f.Name, true
}

// literal writes p as a Go composite literal of pathType, the type of the
// paths in generated code.
func (p *integerPath) literal(pathType string) string {
	if p == nil {
		return "nil"
	}

	var parts []string

	if p.kind != "" {
		parts = append(parts, fmt.Sprintf("kind: %q", p.kind))
	}

	if p.items != nil {
		parts = append(parts, "items: "+p.items.literal(pathType))
	}

	if p.values != nil {
		parts = append(parts, "values: "+p.values.literal(pathType))
	}

	if p.fields != nil {
		fields := make([]string, 0, len(p.fields))
		for _, name := range slices.Sorted(maps.Keys(p.fields)) {
			fields = append(fields, fmt.Sprintf("%q: %s", name, p.fields[name].elementLiteral(pathType)))
		}

		parts = append(parts, fmt.Sprintf("fields: map[string]*%s{%s}", pathType, strings.Join(fields, ", ")))
	}

	return "&" + pathType + "{" + strings.Join(parts, ", ") + "}"
}

// elementLiteral writes p as literal does, as an element of a composite
// literal, which leaves out the type's name.
func (p *integerPath) elementLiteral(pathType string) string {
	return strings.TrimPrefix(p.literal(pathType), "&"+pathType)
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

// addDecoders gives each type of o that holds an integer, and has no
// decoders, a JSON and a YAML one, and under --use-number one that holds an
// untyped value a JSON one. A type declared as a pointer or an interface can
// have no methods.
func (g *Generator) addDecoders(o *output) {
	if g.config.OnlyModels {
		return
	}

	for _, d := range o.file.Package.Decls {
		decl, ok := d.(*codegen.TypeDecl)
		if !ok || !canHaveMethods(decl.Type) {
			continue
		}

		if _, done := o.unmarshallersByTypeDecl[decl]; done {
			continue
		}

		integers := holdsInteger(decl.Type)
		if !integers && (!g.config.UseNumber || !holdsUntyped(decl.Type)) {
			continue
		}

		o.unmarshallersByTypeDecl[decl] = true

		for _, f := range g.formatters {
			if f.getName() != formatJSON && !integers {
				continue
			}

			f.addImport(o.file, decl)
			o.file.Package.AddDecl(&codegen.Method{
				Impl: f.generate(o, decl, nil),
				Name: decl.GetName() + "_validator_" + f.getName(),
			})
		}
	}
}

// integralHelpers returns the base name of the file's helpers that write
// integral numbers as integers. The first call picks it, after typeName, the
// type whose decoder calls them, and declares the type of their paths and the
// functions that read a number.
func (o *output) integralHelpers(typeName string) string {
	if o.integralHelperName != "" {
		return o.integralHelperName
	}

	name := integralHelperBase + typeName
	for n := 1; o.declaresName(name) || o.importsName(name); n++ {
		name = fmt.Sprintf("%s%s_%d", integralHelperBase, typeName, n)
	}

	o.integralHelperName = name

	for _, pkg := range []string{"errors", "strconv", "strings"} {
		o.file.Package.AddImport(pkg, "")
	}

	o.file.Package.AddDecl(&codegen.Method{Name: name + "_path", Impl: emitIntegralPath(name)})
	o.file.Package.AddDecl(&codegen.Method{Name: name + "_number", Impl: emitIntegralNumber(name)})
	o.file.Package.AddDecl(&codegen.Method{Name: name + "_fits", Impl: emitIntegralFits(name)})

	return name
}

// integralHelper returns the name of the file's helper that retries a JSON
// decode with integral numbers written as integers, declaring it on the first
// call.
func (o *output) integralHelper(typeName string) string {
	name := o.integralHelpers(typeName)

	if !o.declaresName(name) {
		for _, pkg := range []string{bytesPackage, jsonPackage} {
			o.file.Package.AddImport(pkg, "")
		}

		o.file.Package.AddDecl(&codegen.Method{Name: name, Impl: emitIntegralHelper(name, o.useNumber)})
	}

	return name
}

// integralYAMLHelper returns the name of the file's helper that decodes YAML
// with integral floats written as integers, declaring it on the first call.
func (o *output) integralYAMLHelper(typeName string) string {
	base := o.integralHelpers(typeName)
	name := base + "_yaml"

	if !o.declaresName(name) {
		o.file.Package.AddImport(YAMLPackage, "yaml")
		o.file.Package.AddImport("fmt", "")
		o.file.Package.AddDecl(&codegen.Method{Name: name, Impl: emitIntegralYAMLHelper(base)})
	}

	return name
}

// integerPathsVar returns the name of the variable that holds p for the
// helpers of the file, declaring it on the first call for such paths. A leaf's
// is named after its Go integer type, and others after owner, the first type
// whose decoder reads them.
func (o *output) integerPathsVar(owner string, p *integerPath) string {
	base := o.integralHelpers(owner)
	pathType := base + "_path"
	literal := p.literal(pathType)

	if name, ok := o.integerPathVars[literal]; ok {
		return name
	}

	name := integerPathsBase + owner
	if p.kind != "" {
		name = base + "_" + p.kind
	}

	for n := 1; o.declaresName(name) || o.importsName(name); n++ {
		name = fmt.Sprintf("%s%s_%d", integerPathsBase, owner, n)
	}

	if o.integerPathVars == nil {
		o.integerPathVars = map[string]string{}
	}

	o.integerPathVars[literal] = name

	o.file.Package.AddDecl(&codegen.Method{Name: name, Impl: func(out *codegen.Emitter) error {
		if p.fields == nil {
			out.Printlnf("var %s = %s", name, literal)

			return nil
		}

		// A struct's fields, one to a line.
		out.Printlnf("var %s = &%s{fields: map[string]*%s{", name, pathType, pathType)
		out.Indent(1)

		for _, field := range slices.Sorted(maps.Keys(p.fields)) {
			out.Printlnf("%q: %s,", field, p.fields[field].elementLiteral(pathType))
		}

		out.Indent(-1)
		out.Printlnf("}}")

		return nil
	}})

	return name
}

// emitIntegralPath writes the type of the paths of base's helpers, and its
// method that follows a key.
func emitIntegralPath(base string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		path := base + "_path"

		out.Commentf("%s is where a value holds Go integers that its own decoder decodes: the value "+
			"itself, whose Go integer type kind names, or the values below it, in its items, its values "+
			"or its fields. A nil field holds none, but has a name that a JSON key can match.", path)
		out.Printlnf("type %s struct {", path)
		out.Indent(1)
		out.Printlnf("kind          string")
		out.Printlnf("items, values *%s", path)
		out.Printlnf("fields        map[string]*%s", path)
		out.Indent(-1)
		out.Printlnf("}")
		out.Newline()
		out.Commentf("at returns the path of the value at key in an object at p. With fold, it also " +
			"finds a field whose name key matches only when case is ignored, the first such in name " +
			"order, as encoding/json does.")
		out.Printlnf("func (p *%s) at(key string, fold bool) *%s {", path, path)
		out.Indent(1)
		out.Printlnf("if p.fields == nil {")
		out.Printlnf("return p.values")
		out.Printlnf("}")
		out.Printlnf("f, ok := p.fields[key]")
		out.Printlnf("if ok || !fold {")
		out.Printlnf("return f")
		out.Printlnf("}")
		out.Printlnf(`match := ""`)
		out.Printlnf("for name, g := range p.fields {")
		out.Indent(1)
		out.Printlnf("if strings.EqualFold(name, key) && (!ok || name < match) {")
		out.Printlnf("match, f, ok = name, g, true")
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("return f")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// emitIntegralNumber writes the function of base's helpers that reads the
// integer a number holds. It works on the number's digits, so a huge exponent
// costs nothing.
func emitIntegralNumber(base string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		number := base + "_number"

		out.Commentf("%s returns the integer that the number s holds, written without a fraction, an "+
			"exponent or a plus sign, or false when s holds none. An integer of more than 20 digits, "+
			"which no Go integer holds, is returned as \"\".", number)
		out.Printlnf("func %s(s string) (string, bool) {", number)
		out.Indent(1)
		out.Printlnf(`neg := strings.HasPrefix(s, "-")`)
		out.Printlnf(`if neg || strings.HasPrefix(s, "+") {`)
		out.Printlnf("s = s[1:]")
		out.Printlnf("}")
		out.Printlnf(`mantissa, exp := s, "0"`)
		out.Printlnf(`if i := strings.IndexAny(s, "eE"); i >= 0 {`)
		out.Printlnf("mantissa, exp = s[:i], s[i+1:]")
		out.Printlnf("}")
		out.Printlnf(`intPart, frac, _ := strings.Cut(mantissa, ".")`)
		out.Printlnf("digits := intPart + frac")
		out.Printlnf(`if digits == "" || strings.Trim(digits, "0123456789") != "" {`)
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Printlnf(`significant := strings.TrimLeft(digits, "0")`)
		out.Printlnf(`if significant == "" {`)
		out.Printlnf(`return "0", true`)
		out.Printlnf("}")
		out.Printlnf("e, err := strconv.Atoi(exp)")
		out.Printlnf("if err != nil && !errors.Is(err, strconv.ErrRange) {")
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Comment("An exponent out of an int's range is read as the largest int; a billion is " +
			"still past any Go integer, and keeps point from overflowing.")
		out.Printlnf("if e > 1e9 {")
		out.Printlnf("e = 1e9")
		out.Printlnf("} else if e < -1e9 {")
		out.Printlnf("e = -1e9")
		out.Printlnf("}")
		out.Printlnf("point := len(intPart) - (len(digits) - len(significant)) + e")
		out.Printlnf(`significant = strings.TrimRight(significant, "0")`)
		out.Printlnf("if point < len(significant) {")
		out.Printlnf(`return "", false`)
		out.Printlnf("}")
		out.Printlnf("if point > 20 {")
		out.Printlnf(`return "", true`)
		out.Printlnf("}")
		out.Printlnf(`whole := significant + strings.Repeat("0", point-len(significant))`)
		out.Printlnf("if neg {")
		out.Printlnf(`whole = "-" + whole`)
		out.Printlnf("}")
		out.Printlnf("return whole, true")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// emitIntegralFits writes the function of base's helpers that checks an
// integer against the range of a Go integer type.
func emitIntegralFits(base string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		fits := base + "_fits"

		out.Commentf("%s reports whether the Go integer type kind holds the integer s.", fits)
		out.Printlnf("func %s(s, kind string) bool {", fits)
		out.Indent(1)
		out.Printlnf("size := strconv.IntSize")
		out.Printlnf(`switch strings.TrimPrefix(kind, "u") {`)

		for _, size := range []int{8, 16, 32, 64} {
			out.Printlnf(`case "int%d":`, size)
			out.Printlnf("size = %d", size)
		}

		out.Printlnf("}")
		out.Printlnf(`if strings.HasPrefix(kind, "u") {`)
		out.Printlnf("_, err := strconv.ParseUint(s, 10, size)")
		out.Printlnf("return err == nil")
		out.Printlnf("}")
		out.Printlnf("_, err := strconv.ParseInt(s, 10, size)")
		out.Printlnf("return err == nil")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// emitIntegralHelper writes the JSON helper named name. It reads the input as
// tokens, to know where each number is, and writes the input again only where
// a number changes.
func emitIntegralHelper(name string, useNumber bool) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		path := name + "_path"
		number := name + "_number"

		out.Commentf("%s decodes data into v again, as json.Unmarshal does, after a decode that "+
			"failed with err. It first writes each number at one of p's integer paths that holds an "+
			"integer, such as 2.0 or 1e2, as that integer, which JSON Schema counts it as and "+
			"encoding/json requires for a Go integer. Every other byte stays as written, and so does a "+
			"number that the Go integer type can't hold, so that the error of the second decode names "+
			"it as written. It returns err when no number needs writing. Every decoder in this file calls "+
			"it; it is named after the first, so that no other file of the package declares it.", name)
		out.Printlnf("func %s(data []byte, v interface{}, err error, p *%s) error {", name, path)
		out.Indent(1)
		out.Printlnf("if !json.Valid(data) {")
		out.Printlnf("return err")
		out.Printlnf("}")
		out.Printlnf("dec := json.NewDecoder(bytes.NewReader(data))")
		out.Printlnf("dec.UseNumber()")
		out.Printlnf("var written []byte")
		out.Printlnf("copied := 0")
		out.Printlnf("var walk func(p *%s) bool", path)
		out.Printlnf("walk = func(p *%s) bool {", path)
		out.Indent(1)
		out.Printlnf("if p == nil {")
		out.Printlnf("var skipped json.RawMessage")
		out.Printlnf("return dec.Decode(&skipped) == nil")
		out.Printlnf("}")
		out.Printlnf("tok, terr := dec.Token()")
		out.Printlnf("if terr != nil {")
		out.Printlnf("return false")
		out.Printlnf("}")
		out.Printlnf("if delim, ok := tok.(json.Delim); ok {")
		out.Indent(1)
		out.Printlnf("for dec.More() {")
		out.Indent(1)
		out.Printlnf("next := p.items")
		out.Printlnf("if delim == '{' {")
		out.Printlnf("key, kerr := dec.Token()")
		out.Printlnf("if kerr != nil {")
		out.Printlnf("return false")
		out.Printlnf("}")
		out.Printlnf("next = p.at(key.(string), true)")
		out.Printlnf("}")
		out.Printlnf("if !walk(next) {")
		out.Printlnf("return false")
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("_, terr = dec.Token()")
		out.Printlnf("return terr == nil")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if n, ok := tok.(json.Number); ok && p.kind != \"\" {")
		out.Indent(1)
		out.Printlnf("s, ok := %s(string(n))", number)
		out.Printlnf(`if ok && s != "" && s != string(n) && %s_fits(s, p.kind) {`, name)
		out.Printlnf("end := int(dec.InputOffset())")
		out.Printlnf("written = append(append(written, data[copied:end-len(n)]...), s...)")
		out.Printlnf("copied = end")
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("return true")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if !walk(p) || written == nil {")
		out.Printlnf("return err")
		out.Printlnf("}")
		out.Printlnf("written = append(written, data[copied:]...)")

		// As the decode it retries, which keeps the numbers of untyped values
		// exact under --use-number.
		if useNumber {
			out.Printlnf("dec = json.NewDecoder(bytes.NewReader(written))")
			out.Printlnf("dec.UseNumber()")
			out.Printlnf("return dec.Decode(v)")
		} else {
			out.Printlnf("return json.Unmarshal(written, v)")
		}

		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// emitIntegralYAMLHelper writes base's YAML helper. It copies only the nodes
// it changes, so the node a decoder is given, which other decoders may read
// too, stays as it is, and it keeps an alias an alias, so that yaml.v3 still
// limits how far a document's aliases expand.
func emitIntegralYAMLHelper(base string) func(*codegen.Emitter) error {
	return func(out *codegen.Emitter) error {
		name := base + "_yaml"
		path := base + "_path"
		number := base + "_number"

		out.Commentf("%s decodes n into v, as n.Decode does, after writing each float at one of p's "+
			"integer paths as the integer it holds, which JSON Schema counts it as: yaml.v3 would "+
			"truncate a float into a Go integer, and round an integer beyond 2^53. A float that holds no "+
			"integer, or one that the Go integer type can't hold, is an error that names where it is. n "+
			"itself stays as it is: a node with a float to write is copied, with the nodes above it.", name)
		out.Printlnf("func %s(n *yaml.Node, v interface{}, p *%s) error {", name, path)
		out.Indent(1)
		out.Printlnf("type alias struct {")
		out.Printlnf("n *yaml.Node")
		out.Printlnf("p *%s", path)
		out.Printlnf("}")
		out.Printlnf("aliases := map[alias]*yaml.Node{}")
		out.Printlnf("var walk func(n *yaml.Node, p *%s, at string) (*yaml.Node, error)", path)
		out.Printlnf("walk = func(n *yaml.Node, p *%s, at string) (*yaml.Node, error) {", path)
		out.Indent(1)
		out.Printlnf("if p == nil {")
		out.Printlnf("return n, nil")
		out.Printlnf("}")
		out.Printlnf("switch n.Kind {")
		out.Printlnf("case yaml.ScalarNode:")
		out.Indent(1)
		out.Printlnf(`if p.kind == "" || n.ShortTag() != "!!float" {`)
		out.Printlnf("return n, nil")
		out.Printlnf("}")
		out.Printlnf(`field := ""`)
		out.Printlnf(`if at != "" {`)
		out.Printlnf(`field = "field " + at + ": "`)
		out.Printlnf("}")
		out.Printlnf("s, ok := %s(n.Value)", number)
		out.Printlnf("if !ok {")
		out.Printlnf(`return nil, fmt.Errorf("%%s%%s is not an integer", field, n.Value)`)
		out.Printlnf("}")
		out.Printlnf(`if s == "" || !%s_fits(s, p.kind) {`, base)
		out.Printlnf(`return nil, fmt.Errorf("%%s%%s is out of range for %%s", field, n.Value, p.kind)`)
		out.Printlnf("}")
		out.Printlnf("c := *n")
		out.Printlnf(`c.Tag, c.Value, c.Style = "!!int", s, 0`)
		out.Printlnf("return &c, nil")
		out.Indent(-1)
		out.Printlnf("case yaml.AliasNode:")
		out.Indent(1)
		out.Comment("Each anchor is walked once for p. It stands for itself while it is walked, " +
			"for an alias inside it, which yaml.v3 refuses.")
		out.Printlnf("key := alias{n.Alias, p}")
		out.Printlnf("t, ok := aliases[key]")
		out.Printlnf("if !ok {")
		out.Indent(1)
		out.Printlnf("aliases[key] = n.Alias")
		out.Printlnf("var err error")
		out.Printlnf("if t, err = walk(n.Alias, p, at); err != nil {")
		out.Printlnf("return nil, err")
		out.Printlnf("}")
		out.Printlnf("aliases[key] = t")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if t == n.Alias {")
		out.Printlnf("return n, nil")
		out.Printlnf("}")
		out.Printlnf("c := *n")
		out.Printlnf("c.Alias = t")
		out.Printlnf("return &c, nil")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("var content []*yaml.Node")
		out.Printlnf("for i, e := range n.Content {")
		out.Indent(1)
		out.Printlnf("next, nextAt := p, at")
		out.Printlnf("switch n.Kind {")
		out.Printlnf("case yaml.SequenceNode:")
		out.Printlnf(`next, nextAt = p.items, fmt.Sprintf("%%s[%%d]", at, i)`)
		out.Printlnf("case yaml.MappingNode:")
		out.Indent(1)
		out.Printlnf("if i%%2 == 0 {")
		out.Printlnf("continue")
		out.Printlnf("}")
		out.Printlnf("k := n.Content[i-1]")
		out.Comment("A merge key's mapping, or each of a sequence of them, holds keys of this one, " +
			"as yaml.v3 tells it.")
		out.Printlnf(`if k.Kind == yaml.ScalarNode && k.Value == "<<" &&`)
		out.Printlnf(`(k.Tag == "" || k.Tag == "!" || k.ShortTag() == "!!merge") {`)
		out.Indent(1)
		out.Printlnf("if e.Kind == yaml.SequenceNode {")
		out.Printlnf("next = &%s{items: p}", path)
		out.Printlnf("}")
		out.Printlnf("break")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("next = p.at(k.Value, false)")
		out.Printlnf("if p.fields == nil {")
		out.Printlnf(`nextAt = fmt.Sprintf("%%s[%%q]", at, k.Value)`)
		out.Printlnf(`} else if at == "" {`)
		out.Printlnf("nextAt = k.Value")
		out.Printlnf("} else {")
		out.Printlnf(`nextAt = at + "." + k.Value`)
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("t, err := walk(e, next, nextAt)")
		out.Printlnf("if err != nil {")
		out.Printlnf("return nil, err")
		out.Printlnf("}")
		out.Printlnf("if t != e {")
		out.Indent(1)
		out.Printlnf("if content == nil {")
		out.Printlnf("content = append([]*yaml.Node(nil), n.Content...)")
		out.Printlnf("}")
		out.Printlnf("content[i] = t")
		out.Indent(-1)
		out.Printlnf("}")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf("if content == nil {")
		out.Printlnf("return n, nil")
		out.Printlnf("}")
		out.Printlnf("c := *n")
		out.Printlnf("c.Content = content")
		out.Printlnf("return &c, nil")
		out.Indent(-1)
		out.Printlnf("}")
		out.Printlnf(`t, err := walk(n, p, "")`)
		out.Printlnf("if err != nil {")
		out.Printlnf("return err")
		out.Printlnf("}")
		out.Printlnf("return t.Decode(v)")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}
