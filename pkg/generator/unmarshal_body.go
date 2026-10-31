package generator

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

var errAdditionalPropertiesType = errors.New("AdditionalProperties field is neither a map nor an interface{}")

// hasAdditionalPropertiesField reports whether the generated struct will
// carry the synthetic `AdditionalProperties` catch-all field. Centralised so
// the formatters and the shared body all check the same predicate; a
// drift between the two caused real bugs in older revisions.
func hasAdditionalPropertiesField(declType *codegen.TypeDecl) bool {
	return additionalPropertiesField(declType) != nil
}

// additionalPropertiesField returns the synthetic `AdditionalProperties`
// field of the generated struct, or nil when it has none.
func additionalPropertiesField(declType *codegen.TypeDecl) *codegen.StructField {
	structType, ok := declType.Type.(*codegen.StructType)
	if !ok {
		return nil
	}

	for i := range structType.Fields {
		if isAdditionalPropertiesField(&structType.Fields[i]) {
			return &structType.Fields[i]
		}
	}

	return nil
}

// isAdditionalPropertiesField reports whether f is the synthetic
// `AdditionalProperties` catch-all field. A property the schema declares
// under the name additionalProperties gets a field of that name too, which
// is told apart by its JSON name.
func isAdditionalPropertiesField(f *codegen.StructField) bool {
	return f.Name == additionalProperties && f.JSONName == ""
}

// varNameNumbers holds the input decoded with its numbers kept exact, for the
// checks that declare requiresNumbers.
const varNameNumbers = "numbers"

// unmarshalContext captures the format-specific bits that distinguish
// UnmarshalJSON generation from UnmarshalYAML generation. Everything else
// in the body is identical across formats.
type unmarshalContext struct {
	// formatName is the lowercase name passed to validator.generate (e.g. "json", "yaml").
	formatName string
	// methodSuffix is the uppercase suffix used in the function name and doc
	// comment, e.g. "JSON" -> UnmarshalJSON / "json.Unmarshaler".
	methodSuffix string
	// paramDecl is the parameter declaration for the Unmarshal method,
	// e.g. "value []byte" or "value *yaml.Node".
	paramDecl string
	// decodeCall builds the format-specific decode expression that consumes
	// source, the method's input or a value of it, into the named target. The
	// result is wrapped in the standard `if err := <expr>; err != nil { return
	// err }` line.
	decodeCall func(source, target string) string
	// typedDecodeCall, when set, is decodeCall for the typed decodes, of the
	// Plain struct and of the additional properties, which may differ from the
	// raw map's.
	typedDecodeCall func(source, target string) string
	// rawValueType is the type that holds one value of the input as it is:
	// json.RawMessage or yaml.Node.
	rawValueType string
	// valueSource writes what an additional property's value is decoded
	// from, the value at k in the map named additional of rawValueType, and
	// returns its expression.
	valueSource func(out *codegen.Emitter) string
	// integralHelper declares, and names, the output's helper that writes
	// integral numbers as integers for the format, for typeName's decoder.
	// See integral_numbers.go.
	integralHelper func(typeName string) string
	// retries marks a helper that retries a decode that failed, as the JSON
	// one does, rather than one that decodes in its place.
	retries bool
}

// typedDecode returns the decode call for the typed decodes.
func (ctx *unmarshalContext) typedDecode() func(source, target string) string {
	if ctx.typedDecodeCall != nil {
		return ctx.typedDecodeCall
	}

	return ctx.decodeCall
}

// decoder is how a value of one type is decoded: helper and paths name the
// helper that writes integral numbers as integers and the variable that holds
// the type's integer paths, when it holds an integer, and retryType is the
// type that a JSON retry decodes into anew, or "" to decode into the same
// value again.
type decoder struct {
	helper, paths, retryType string
}

// pendingDecoder is the handling of integers in a type's decoder for one
// format. A type's fields can be filled in after its decoders are made, when a
// $ref leads back to the type while it is generated, so it is worked out only
// once every schema is generated, by output.resolveDecoders.
type pendingDecoder struct {
	decl   *codegen.TypeDecl
	format string
	// integralHelper declares, and names, the format's helper.
	integralHelper func(typeName string) string
	// plain decodes the Plain struct, and additional each additional
	// property.
	plain, additional decoder
	// err reports an AdditionalProperties field of a type the decoder can't
	// fill.
	err error
}

// resolveDecoders works out the handling of integers in each decoder of o,
// now that every type is complete, and declares the helpers and paths that
// they use, and the imports of an additional properties' decode.
func (o *output) resolveDecoders() {
	for _, pd := range o.pendingDecoders {
		pd.plain = pd.decoderOf(o, pd.decl.Name, pd.decl.Type)

		field := additionalPropertiesField(pd.decl)
		if field == nil {
			continue
		}

		for _, pkg := range []string{"reflect", "sort", "strings"} {
			o.file.Package.AddImport(pkg, "")
		}

		mapType, ok := additionalPropertiesMap(field)
		if !ok {
			pd.err = fmt.Errorf("%w: %s", errAdditionalPropertiesType, pd.decl.Name)

			continue
		}

		pd.additional = pd.decoderOf(o, pd.decl.Name+"_"+additionalProperties, mapType.ValueType)
	}

	o.pendingDecoders = nil
}

// decoderOf returns how pd decodes a value of t. owner names the variable of
// t's integer paths, when they are not a leaf's.
func (pd *pendingDecoder) decoderOf(o *output, owner string, t codegen.Type) decoder {
	p := integerPaths(t, pd.format)
	if p == nil {
		return decoder{}
	}

	return decoder{helper: pd.integralHelper(pd.decl.Name), paths: o.integerPathsVar(owner, p)}
}

// emitDecode writes the decode of source into target, a variable that d
// decodes, and a return from the decoder with wrap, an error expression, when
// it fails.
func (ctx *unmarshalContext) emitDecode(out *codegen.Emitter, d decoder, source, target, wrap string) {
	decode := ctx.typedDecode()(source, target)

	switch {
	case d.helper == "":
		out.Printlnf("if err := %s; err != nil {", decode)
		out.Indent(1)
		out.Printlnf("return %s", wrap)
		out.Indent(-1)
		out.Printlnf("}")

	case !ctx.retries:
		out.Printlnf("if err := %s(%s, &%s, %s); err != nil {", d.helper, source, target, d.paths)
		out.Indent(1)
		out.Printlnf("return %s", wrap)
		out.Indent(-1)
		out.Printlnf("}")

	default:
		out.Printlnf("if err := %s; err != nil {", decode)
		out.Indent(1)

		into := target

		if d.retryType != "" {
			into = "retry"

			out.Printlnf("var retry %s", d.retryType)
		}

		out.Printlnf("if err = %s(%s, &%s, err, %s); err != nil {", d.helper, source, into, d.paths)
		out.Printlnf("return %s", wrap)
		out.Printlnf("}")

		if d.retryType != "" {
			out.Printlnf("%s = retry", target)
		}

		out.Indent(-1)
		out.Printlnf("}")
	}
}

// generateUnmarshalBody produces the body of an Unmarshal{JSON,YAML} method.
// It is the shared implementation behind jsonFormatter.generate and
// yamlFormatter.generate; the two formatters differ only by the unmarshalContext
// they pass in.
func generateUnmarshalBody(
	ctx unmarshalContext,
	output *output,
	declType *codegen.TypeDecl,
	validators []validator,
) func(*codegen.Emitter) error {
	var (
		// firstValidators decode the input themselves, so they run before the
		// raw map, which only an object can be decoded into.
		firstValidators  []validator
		beforeValidators []validator
		afterValidators  []validator
		// apValidators check AdditionalProperties' values, so they run once
		// that map is decoded.
		apValidators []validator
		forceBefore  bool
		// numbers is set when a check reads the input with exact numbers.
		numbers bool
	)

	for _, v := range validators {
		d := v.desc()
		numbers = numbers || d.requiresNumbers

		switch {
		case d.beforeRawDecode:
			firstValidators = append(firstValidators, v)

		case d.beforeJSONUnmarshal:
			beforeValidators = append(beforeValidators, v)

		case d.afterAdditionalProperties:
			apValidators = append(apValidators, v)

		default:
			afterValidators = append(afterValidators, v)
			forceBefore = forceBefore || d.requiresRawAfter
		}
	}

	// An integer written 2.0 or 1e2 is written as an integer for the typed
	// decodes. See integral_numbers.go.
	pending := &pendingDecoder{decl: declType, format: ctx.formatName, integralHelper: ctx.integralHelper}
	output.pendingDecoders = append(output.pendingDecoders, pending)

	return func(out *codegen.Emitter) error {
		if pending.err != nil {
			return pending.err
		}

		additionalField := additionalPropertiesField(declType)

		out.Commentf("Unmarshal%s implements %s.Unmarshaler.", ctx.methodSuffix, ctx.formatName)
		out.Printlnf("func (j *%s) Unmarshal%s(%s) error {", declType.Name, ctx.methodSuffix, ctx.paramDecl)
		out.Indent(1)

		for _, v := range firstValidators {
			if err := v.generate(out, ctx.formatName); err != nil {
				return fmt.Errorf("cannot generate first validators: %w", err)
			}
		}

		if forceBefore || len(beforeValidators) != 0 || additionalField != nil {
			out.Printlnf("var %s map[string]interface{}", varNameRawMap)
			out.Printlnf("if err := %s; err != nil {", ctx.decodeCall("value", varNameRawMap))
			out.Indent(1)
			out.Printlnf(`return fmt.Errorf("unmarshal raw %s: %%w", err)`, declType.Name)
			out.Indent(-1)
			out.Printlnf("}")
		}

		if numbers && ctx.formatName == formatJSON {
			out.Printlnf("var %s map[string]interface{}", varNameNumbers)
			out.Printlnf("{")
			out.Indent(1)
			out.Printlnf("dec := json.NewDecoder(bytes.NewReader(value))")
			out.Printlnf("dec.UseNumber()")
			// The raw map, decoded above, reports an input that is not an
			// object; one that is cannot fail here.
			out.Printlnf("_ = dec.Decode(&%s)", varNameNumbers)
			out.Indent(-1)
			out.Printlnf("}")
		}

		for _, v := range beforeValidators {
			if err := v.generate(out, ctx.formatName); err != nil {
				return fmt.Errorf("cannot generate before validators: %w", err)
			}
		}

		// Guard against `type Plain Plain` self-reference when the user's own
		// type happens to be named `Plain`, AND against collisions with any
		// other registered name. The previous form gated the rename loop on
		// `tp == declType.Name`, which silently passed `tp == "Plain"` through
		// when "Plain" was unique-by-name but identical to declType.Name.
		tp := typePlain
		for i := 0; (tp == declType.Name || !output.isUniqueTypeName(tp)) && i < math.MaxInt; i++ {
			tp = fmt.Sprintf("%s_%d", typePlain, i)
		}

		plain := pending.plain
		plain.retryType = tp

		out.Printlnf("type %s %s", tp, declType.Name)
		out.Printlnf("var %s %s", varNamePlainStruct, tp)
		ctx.emitDecode(out, plain, "value", varNamePlainStruct,
			fmt.Sprintf(`fmt.Errorf("unmarshal %s: %%w", err)`, declType.Name))

		for _, v := range afterValidators {
			if err := v.generate(out, ctx.formatName); err != nil {
				return fmt.Errorf("cannot generate after validators: %w", err)
			}
		}

		if additionalField != nil {
			if err := emitAdditionalProperties(out, ctx, declType, additionalField, pending.additional, tp); err != nil {
				return err
			}
		}

		for _, v := range apValidators {
			if err := v.generate(out, ctx.formatName); err != nil {
				return fmt.Errorf("cannot generate additional-properties validators: %w", err)
			}
		}

		out.Printlnf("*j = %s(%s)", declType.Name, varNamePlainStruct)
		out.Printlnf("return nil")
		out.Indent(-1)
		out.Printlnf("}")

		return nil
	}
}

// emitAdditionalProperties writes the decode of the input's additional
// properties into the AdditionalProperties map: the keys of the raw map that
// no field binds, each decoded from its own value, as d decodes a value of
// the map, so that it is checked and kept exact as a declared property is.
// They are decoded in key order, so that an error names the first that fails.
func emitAdditionalProperties(
	out *codegen.Emitter,
	ctx unmarshalContext,
	declType *codegen.TypeDecl,
	field *codegen.StructField,
	d decoder,
	tp string,
) error {
	// Strip declared properties from raw, so that it keeps the additional
	// ones. We use only the active format's wire name so that, e.g., a JSON
	// additional-property key whose name happens to match a Go identifier or
	// an unrelated tag isn't silently dropped. The synthetic
	// AdditionalProperties field carries no meaningful tag and must be
	// skipped — otherwise `delete(raw, "AdditionalProperties")` would eat that
	// input key. A field whose whole tag is `json:"-"` (or `yaml:"-"`) is
	// ignored by the decoder, so it is skipped here too and a legitimate
	// additional-property key literally named "-" is preserved. The test is on
	// the whole tag, not the name before the comma: a property literally named
	// "-" is tagged `json:"-,"`, which the decoder does bind, so its key must
	// be pruned like any other declared one.
	//
	// Match the way the decoder for this format actually binds keys to
	// fields, so pruning removes exactly what the decoder consumed:
	//
	//   - encoding/json matches field names case-INsensitively, so a payload
	//     like {"foo": ..., "FOO": ...} where `foo` is declared binds one of
	//     them to the field. Prune case-insensitively, or the unbound variant
	//     leaks into AdditionalProperties. A field with no json tag binds
	//     under its Go name.
	//   - yaml.v3 matches case-SENSITIVELY: given a declared `foo`, the key
	//     `FOO` is left unbound and is a genuine additional property. Pruning
	//     it case-insensitively would silently drop it, so the YAML path
	//     compares exactly. A field with no yaml tag (every field, when Tags
	//     leaves yaml out) binds under its LOWERCASED Go name, so `Foo` takes
	//     the key `foo`.
	tagName := formatJSON
	matchExpr := `strings.EqualFold(k, name)`
	untaggedName := "f.Name"

	if ctx.formatName == formatYAML {
		tagName = formatYAML
		matchExpr = `k == name`
		untaggedName = "strings.ToLower(f.Name)"
	}

	out.Printlnf("st := reflect.TypeOf(%s{})", tp)
	out.Printlnf("for i := 0; i < st.NumField(); i++ {")
	out.Indent(1)
	out.Printlnf("f := st.Field(i)")
	out.Printlnf(`if f.Name == %q { continue }`, additionalProperties)
	out.Printlnf(`tag := f.Tag.Get(%q)`, tagName)
	out.Printlnf(`if tag == "-" { continue }`)
	out.Printlnf(`name := strings.Split(tag, ",")[0]`)
	out.Printlnf(`if name == "" { name = %s }`, untaggedName)
	out.Printlnf("for k := range %s {", varNameRawMap)
	out.Indent(1)
	out.Printlnf(`if %s { delete(%s, k) }`, matchExpr, varNameRawMap)
	out.Indent(-1)
	out.Printlnf("}")
	out.Indent(-1)
	out.Printlnf("}")

	mapType, ok := additionalPropertiesMap(field)
	if !ok {
		return fmt.Errorf("%w: %s", errAdditionalPropertiesType, declType.Name)
	}

	out.Printlnf("{")
	out.Indent(1)
	out.Printlnf("var additional map[string]%s", ctx.rawValueType)
	out.Printlnf("if err := %s; err != nil {", ctx.decodeCall("value", "additional"))
	out.Printlnf(`return fmt.Errorf("decode additional properties for %s: %%w", err)`, declType.Name)
	out.Printlnf("}")
	out.Printlnf("keys := make([]string, 0, len(%s))", varNameRawMap)
	out.Printlnf("for k := range %s {", varNameRawMap)
	out.Printlnf("keys = append(keys, k)")
	out.Printlnf("}")
	out.Printlnf("sort.Strings(keys)")
	out.Printf("values := make(")

	if err := mapType.Generate(out); err != nil {
		return fmt.Errorf("cannot generate additional properties type: %w", err)
	}

	out.Printlnf(", len(keys))")
	out.Printlnf("for _, k := range keys {")
	out.Indent(1)
	out.Printf("var v ")

	if err := mapType.ValueType.Generate(out); err != nil {
		return fmt.Errorf("cannot generate additional properties type: %w", err)
	}

	out.Newline()

	source := ctx.valueSource(out)
	ctx.emitDecode(out, d, source, "v",
		fmt.Sprintf(`fmt.Errorf("decode additional property %%q for %s: %%w", k, err)`, declType.Name))
	out.Printlnf("values[k] = v")
	out.Indent(-1)
	out.Printlnf("}")
	out.Printlnf("%s.%s = values", varNamePlainStruct, additionalProperties)
	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

// additionalPropertiesMap returns the map type that holds the additional
// properties of field: its own, or one of interface{} values for a field that
// is an interface{}, as `additionalProperties: true` gives.
func additionalPropertiesMap(field *codegen.StructField) (codegen.MapType, bool) {
	switch t := field.Type.(type) {
	case codegen.MapType:
		return t, true
	case *codegen.MapType:
		return *t, true
	case codegen.EmptyInterfaceType, *codegen.EmptyInterfaceType:
		return codegen.MapType{KeyType: stringTypeVal, ValueType: emptyInterfaceTypeVal}, true
	}

	return codegen.MapType{}, false
}

// trimAddress returns the variable that source takes the address of, or
// source itself.
func trimAddress(source string) string {
	return strings.TrimPrefix(source, "&")
}
