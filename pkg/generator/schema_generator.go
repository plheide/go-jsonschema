package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/google/go-cmp/cmp"

	"github.com/plheide/go-jsonschema/pkg/cmputil"
	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

var (
	//nolint: gochecknoglobals // global to avoid duplication
	boolTypeVal = codegen.PrimitiveType{Type: "bool"}
	//nolint: gochecknoglobals // global to avoid duplication
	emptyInterfaceTypeVal = codegen.EmptyInterfaceType{}
	//nolint: gochecknoglobals // global to avoid duplication
	intTypeVal = codegen.PrimitiveType{Type: "int"}
	//nolint: gochecknoglobals // global to avoid duplication
	stringTypeVal = codegen.PrimitiveType{Type: "string"}
	//nolint: gochecknoglobals // global to avoid duplication
	arrayTypeVal = codegen.ArrayType{Type: emptyInterfaceTypeVal}

	errEmptyInAnyOf = errors.New("cannot have empty anyOf array")

	// ErrPropertyNameNotRepresentable is returned for a property name that
	// encoding/json will not accept in a struct tag. Such a field cannot be
	// mapped at all — decode leaves it unset and encode emits a truncated key
	// — so the schema is refused rather than generated with a field that
	// silently does not round-trip.
	ErrPropertyNameNotRepresentable = errors.New(
		"property name cannot be represented in a Go struct tag that encoding/json accepts",
	)
)

const float64Type = "float64"

func newSchemaGenerator(
	g *Generator,
	schema *schemas.Schema,
	fileName string,
	output *output,
) *schemaGenerator {
	return &schemaGenerator{
		Generator:        g,
		schema:           schema,
		schemaFileName:   fileName,
		output:           output,
		schemaTypesByRef: make(map[string]*schemas.Type),
	}
}

type schemaGenerator struct {
	*Generator

	output           *output
	schema           *schemas.Schema
	schemaFileName   string
	schemaTypesByRef map[string]*schemas.Type
}

func (g *schemaGenerator) generateRootType() error {
	if g.schema.ObjectAsType == nil {
		return errSchemaHasNoRoot
	}

	for _, name := range sortDefinitionsByName(g.schema.Definitions) {
		def := g.schema.Definitions[name]

		// Under the qualify strategy a definition's scope is rooted at its
		// owning schema's type name, so the identifier depends only on the
		// schema that declares it. Left bare, several files declaring the
		// same definition name compete for one identifier and the winner is
		// whichever is processed first.
		scope := newNameScope(g.caser.Identifierize(name))
		if g.config.CollisionStrategy.qualifiesDefinitions() {
			scope = newRootedNameScope(
				g.getRootTypeName(g.schema, g.schemaFileName),
				g.caser.Identifierize(name),
			)
		}

		_, err := g.generateDeclaredType(def, scope)
		if err != nil {
			return err
		}
	}

	root := (*schemas.Type)(g.schema.ObjectAsType)

	// The root may declare a `type` OR be a pure composition / $ref / enum
	// (no `type`, but oneOf/anyOf/allOf/$ref/enum/const populated). Drop
	// only when the root is structurally empty (e.g. `{}` or a schema that
	// declares only metadata like `$schema`/`$id`).
	if !rootHasGeneratable(root) {
		return nil
	}

	rootTypeName := g.getRootTypeName(g.schema, g.schemaFileName)
	if _, ok := g.output.declsByName[rootTypeName]; ok {
		return nil
	}

	root, err := constRootAsEnum(root)
	if err != nil {
		return err
	}

	_, err = g.generateDeclaredType(root, newNameScope(rootTypeName))

	return err
}

// constRootAsEnum returns a root that is only a primitive `const`, explicit
// null included, as the one-value `enum` it is equivalent to. With no `type`
// to generate from, it would otherwise become an unchecked interface{}; the
// enum path types it from its value and checks it. A root declaring an `enum`
// as well, typed or not, has the enum narrowed to the const (narrowEnumToConst),
// since the enum path would otherwise accept every enum value. An object or
// array const stays as it is, since the enum path takes only primitives, and so
// does any other root.
func constRootAsEnum(root *schemas.Type) (*schemas.Type, error) {
	if (root.Const == nil && !root.ConstIsSet) || !isPrimitiveJSONValue(root.Const) {
		return root, nil
	}

	if root.Enum != nil {
		return narrowEnumToConst(root)
	}

	if len(root.Type) > 0 || root.Ref != "" || len(root.OneOf)+len(root.AnyOf)+len(root.AllOf) > 0 {
		return root, nil
	}

	enumRoot := *root
	enumRoot.Enum = []any{root.Const}
	enumRoot.Const = nil
	enumRoot.ConstIsSet = false

	return &enumRoot, nil
}

// narrowEnumToConst returns root with its enum narrowed to its const: both
// keywords hold, so the const must be one of the enum's values. With none left
// no value matches, and that fails as an empty enum does. A valid
// x-enum-varnames list keeps the name at the const's position.
func narrowEnumToConst(root *schemas.Type) (*schemas.Type, error) {
	i := slices.IndexFunc(root.Enum, func(v any) bool { return reflect.DeepEqual(v, root.Const) })
	if i < 0 {
		return nil, fmt.Errorf("%w: %v", errRootConstNotInEnum, root.Const)
	}

	narrowed := *root
	narrowed.Enum = []any{root.Const}
	narrowed.Const = nil
	narrowed.ConstIsSet = false

	if len(root.XEnumVarnames) == len(root.Enum) {
		narrowed.XEnumVarnames = []string{root.XEnumVarnames[i]}
	}

	return &narrowed, nil
}

// rootHasGeneratable reports whether the root carries any keyword that
// should produce a Go declaration. `len(Type) > 0` already triggered the
// pre-existing path; the additional keywords here are the ones the prior
// `len(Type) == 0` early-out silently dropped.
func rootHasGeneratable(t *schemas.Type) bool {
	return len(t.Type) > 0 ||
		t.Ref != "" ||
		t.Enum != nil ||
		t.Const != nil || t.ConstIsSet ||
		len(t.OneOf) > 0 ||
		len(t.AnyOf) > 0 ||
		len(t.AllOf) > 0
}

func (g *schemaGenerator) generateReferencedType(t *schemas.Type) (codegen.Type, error) {
	defName, fileName, err := g.extractRefNames(t)
	if err != nil {
		return nil, err
	}

	if fileName == "" {
		if schemaOutput, ok := g.outputs[g.schema.ID]; ok {
			if decl, ok := schemaOutput.declsByName[defName]; ok {
				if decl != nil {
					return &codegen.NamedType{Decl: decl}, nil
				}
			}
		}
	}

	if t.Ref == "#" {
		if schemaOutput, ok := g.outputs[g.schema.ID]; ok {
			if decl, ok := schemaOutput.declsBySchema[t]; ok {
				if decl != nil {
					return decl.Type, nil
				}
			}
		}

		return emptyInterfaceTypeVal, nil
	}

	schema := g.schema
	sg := g

	if fileName != "" {
		var serr error

		schema, serr = g.loader.Load(fileName, g.schemaFileName)
		if serr != nil {
			return nil, fmt.Errorf("could not follow $ref %q to file %q: %w", t.Ref, fileName, serr)
		}

		qualified, qerr := schemas.QualifiedFileName(fileName, g.schemaFileName, g.config.ResolveExtensions)
		if qerr != nil {
			return nil, fmt.Errorf("could not resolve qualified file name for %s: %w", fileName, qerr)
		}

		if ferr := g.AddFile(qualified, schema); ferr != nil {
			return nil, ferr
		}

		output, oerr := g.findOutputFileForSchemaID(schema.ID)
		if oerr != nil {
			return nil, oerr
		}

		sg = newSchemaGenerator(g.Generator, schema, qualified, output)
	}

	var def *schemas.Type

	if defName != "" {
		var rerr error

		def, rerr = resolveRefPath(schema.Definitions, defName)
		if rerr != nil {
			return nil, fmt.Errorf("%w: %q (from ref %q)", rerr, defName, t.Ref)
		}

		defName = sg.avoidInProgressName(t, def, g.caser.Identifierize(refPathTypeName(defName)))
	} else {
		def = (*schemas.Type)(schema.ObjectAsType)
		defName = g.getRootTypeName(schema, fileName)

		if len(def.Type) == 0 {
			// Minor hack to make definitions default to being objects.
			def.Type = schemas.TypeList{schemas.TypeNameObject}
		}
	}

	isCycle, cleanupCycle, cycleErr := g.detectCycle(t)
	if cycleErr != nil {
		return nil, cycleErr
	}

	defer cleanupCycle()

	dt, err := sg.generateDeclaredType(def, newNameScope(defName))
	if err != nil {
		return nil, err
	}

	// We need this in order to handle cases when
	// generateDeclaredType outputs a MapType and not a NamedType.
	if isMapType(dt) && g.config.DisableCustomTypesForMaps {
		return dt, nil
	}

	// A nullable definition, such as an anyOf of an object and null, comes
	// back as a pointer to its declared type; references to it keep the
	// pointer.
	nt, err := g.extractPointedType(dt)
	if err != nil {
		return nil, err
	}

	if isCycle {
		g.warner(fmt.Sprintf("Cycle detected; must wrap type %s in pointer", nt.Decl.Name))

		dt = codegen.WrapTypeInPointer(dt)
	}

	if sg.output.file.Package.QualifiedName == g.output.file.Package.QualifiedName {
		return dt, nil
	}

	alias := g.resolveImportAlias(sg.output.file.Package.QualifiedName)

	var imp *codegen.Import

	for _, i := range g.output.file.Package.Imports {
		if i.Name == alias && i.QualifiedName == sg.output.file.Package.QualifiedName {
			imp = &i

			break
		}
	}

	if imp == nil {
		g.output.file.Package.AddImport(sg.output.file.Package.QualifiedName, alias)
	}

	ref := &codegen.NamedType{
		Package: &sg.output.file.Package,
		Decl:    nt.Decl,
		Alias:   alias,
	}

	if _, isPointer := dt.(*codegen.PointerType); isPointer {
		return &codegen.PointerType{Type: ref}, nil
	}

	return ref, nil
}

func (g *schemaGenerator) extractRefNames(t *schemas.Type) (string, string, error) {
	scope := ""
	defName := ""
	fileName := t.Ref

	if i := strings.IndexRune(t.Ref, '#'); i != -1 {
		var prefix string

		fileName, scope = t.Ref[0:i], t.Ref[i+1:]
		lowercaseScope := strings.ToLower(scope)

		for _, currentPrefix := range []string{
			"/$defs/",       // Draft-handrews-json-schema-validation-02.
			"/definitions/", // Legacy.
		} {
			if strings.HasPrefix(lowercaseScope, currentPrefix) {
				prefix = currentPrefix

				break
			}
		}

		if len(prefix) == 0 {
			return "", "", fmt.Errorf(
				"%w: value must point to definition within file: '%s'",
				errCannotGenerateReferencedType,
				t.Ref,
			)
		}

		defName = scope[len(prefix):]
	}

	return defName, fileName, nil
}

// avoidInProgressName returns name unless it is held by a declaration still
// being generated for a different schema - the root, say, while its fields are
// walked. isUniqueTypeName treats an in-progress name as free (the allOf and
// anyOf merges rely on that), so a nested ref named after its path would take
// the name and the other declaration would be silently dropped. A schema
// re-entering itself never needs this: generateDeclaredType answers from
// declsBySchema first. Nor does a name held by ref itself: generateDeclaredType
// reserves a property's scope name for the referencing schema before resolving
// it, and drops that placeholder once the ref resolves to a named type. So a
// property order.shipping referencing #/$defs/orderShipping keeps the name
// OrderShipping for the definition rather than declaring it as OrderShipping_1.
// The suffixed name carries the usual collision warning.
func (g *schemaGenerator) avoidInProgressName(ref, def *schemas.Type, name string) string {
	if _, cached := g.output.declsBySchema[def]; cached {
		return name
	}

	if decl, taken := g.output.declsByName[name]; !taken || decl.Type != nil || decl.SchemaType == ref {
		return name
	}

	for n := 1; ; n++ {
		suffixed := fmt.Sprintf("%s_%d", name, n)
		if _, ok := g.output.declsByName[suffixed]; !ok {
			g.warner(fmt.Sprintf(
				"Multiple types map to the name %q; declaring duplicate as %q instead", name, suffixed,
			))

			return suffixed
		}
	}
}

// generateOneOfComposition routes a `oneOf` to whichever specialised generator
// covers its shape, reporting whether it handled the schema at all. Collecting
// the strategies here keeps generateDeclaredType to a single branch for the
// family, and gives each new strategy one place to slot into.
//
// OnlyModels declines every strategy: each emits a type whose entire API is its
// generated Unmarshal/Marshal methods — the primitive wrapper has only an
// unexported `value` field, and the holders only variant pointers — so without
// those methods the result is unusable. The regular generation path produces
// something consumers can at least construct.
func (g *schemaGenerator) generateOneOfComposition(
	t *schemas.Type,
	scope nameScope,
) (codegen.Type, bool, error) {
	g.warnUnresolvableBranches(t.OneOf)

	if g.config.OnlyModels {
		return nil, false, nil
	}

	if isPrimitiveOneOf(t) {
		g.warnUncompiledVariants(t, scope)

		return g.generateOneOfPrimitive(t, scope), true, nil
	}

	// The multi-type-union form `{"type": ["string", "number"]}` is
	// wire-equivalent to a primitive `oneOf` of single-typed variants and is
	// routed through the same wrapper. Mutually exclusive with the case
	// above: one reads t.OneOf, the other t.Type.
	if isPrimitiveMultiTypeUnion(t) {
		return g.generatePrimitiveMultiTypeUnion(t, scope), true, nil
	}

	if len(t.OneOf) > 1 {
		// A natural discriminator gives a direct dispatch; otherwise fall
		// back to try-each, which checks each variant and accepts only when
		// exactly one succeeds.
		if d := g.detectDiscriminator(t.OneOf); d.ok && !g.variantRedefinesParent(t, d.prop) {
			dt, err := g.generateOneOfDiscriminator(t, scope, d)

			return dt, true, err
		}

		if g.useTryEach(t) {
			dt, err := g.generateOneOfTryEach(t, scope)

			return dt, true, err
		}
	}

	return nil, false, nil
}

// warnUnresolvableBranches reports each $ref of a oneOf's branches that leads
// nowhere, saying why, as resolveRefs does for an anyOf or allOf: a branch's
// own $ref, and those of the allOf a branch is built from, which discriminator
// detection merges. No oneOf strategy can use such a branch, so the oneOf
// falls back to the generic type, and nothing else would say that a file did
// not load or a definition is missing.
func (g *schemaGenerator) warnUnresolvableBranches(branches []*schemas.Type) {
	for _, branch := range branches {
		g.warnUnresolvableRefs(branch)
	}
}

// warnUnresolvableRefs reports t's $ref, or else the $refs of t's allOf, when
// they lead nowhere. A $ref's own allOf is the target's, which generating the
// target reports.
func (g *schemaGenerator) warnUnresolvableRefs(t *schemas.Type) {
	if t == nil {
		return
	}

	if t.Ref != "" {
		if _, err := g.findRef(t); err != nil {
			g.warner(fmt.Sprintf("Could not resolve ref %q: %v", t.Ref, err))
		}

		return
	}

	for _, part := range t.AllOf {
		g.warnUnresolvableRefs(part)
	}
}

// alreadyDeclaredType is the type of t, which declsBySchema holds as decl, for
// use at scope.
func (g *schemaGenerator) alreadyDeclaredType(
	t *schemas.Type,
	decl *codegen.TypeDecl,
	scope nameScope,
) (codegen.Type, error) {
	// A $ref's own declaration is only a placeholder while the ref resolves,
	// dropped as soon as it does. Reached again before then, through a copy
	// of a schema enclosing it, the placeholder would end up named in the
	// output but never declared; the ref is what it stands for.
	//
	// The placeholder is kept, as before, where the ref cannot be resolved
	// here, and where definitions that are only $refs to one another lead
	// back to this ref while it is still being resolved.
	if _, resolving := g.resolvingRefs[t]; decl.Type == nil && resolvesToRef(t) && !resolving {
		g.resolvingRefs[t] = struct{}{}
		defer delete(g.resolvingRefs, t)

		if rt, err := g.generateReferencedType(t); err == nil {
			// A cycle through the ref comes back as a pointer to the type.
			// Callers want the declared type itself, package qualifier
			// included, and add the pointer themselves where their own
			// reference is cyclic.
			if pt, ok := rt.(*codegen.PointerType); ok {
				if nt, ok := pt.Type.(*codegen.NamedType); ok {
					return nt, nil
				}
			}

			return rt, nil
		}
	}

	if t.Dereferenced && decl.Name != scope.string() {
		declAlias := &codegen.AliasType{
			Alias: scope.string(),
			Name:  decl.Name,
		}

		g.generateUnmarshaler(decl, nil)

		g.output.file.Package.AddDecl(declAlias)
	}

	return &codegen.NamedType{Decl: decl}, nil
}

// generateEnumOrOneOf generates t as an enum or as a oneOf composition, which
// return before generateType sees t and check no conditional of t's own, so
// that is reported. handled reports whether t was one of them.
func (g *schemaGenerator) generateEnumOrOneOf(t *schemas.Type, scope nameScope) (codegen.Type, bool, error) {
	if t.Enum != nil {
		g.reportOwnConditional(t, t, scope)

		dt, err := g.generateEnumType(t, scope)

		return dt, true, err
	}

	if dt, handled, err := g.generateOneOfComposition(t, scope); handled {
		g.reportOwnConditional(t, t, scope)

		return dt, true, err
	}

	return nil, false, nil
}

//nolint:gocyclo // todo: reduce cyclomatic complexity
func (g *schemaGenerator) generateDeclaredType(t *schemas.Type, scope nameScope) (codegen.Type, error) {
	if decl, ok := g.output.declsBySchema[t]; ok {
		return g.alreadyDeclaredType(t, decl, scope)
	}

	if !g.output.isUniqueTypeName(scope.string()) {
		odecl := g.output.declsByName[scope.string()]

		if cmp.Equal(odecl.SchemaType, t, cmputil.Opts(*odecl.SchemaType, *t)...) {
			return &codegen.NamedType{Decl: odecl}, nil
		}

		if odecl := g.output.declsBySchema[t]; odecl != nil {
			return &codegen.NamedType{Decl: odecl}, nil
		}

		if odecl := g.output.getDeclByEqualSchema(scope.string(), t); odecl != nil {
			return &codegen.NamedType{Decl: odecl}, nil
		}
	}

	if dt, handled, err := g.generateEnumOrOneOf(t, scope); handled {
		return dt, err
	}

	name, nameCollided := g.output.uniqueTypeName(scope)

	if g.config.StructNameFromTitle && t.Title != "" {
		// The title supplies the name outright, so whatever the scope would
		// have collided with is no longer relevant.
		name = g.caser.Identifierize(t.Title)
		nameCollided = false
	}

	decl := codegen.TypeDecl{
		Name:       name,
		Comment:    withDeprecation(t.Description, t),
		SchemaType: t,
	}
	g.output.declsBySchema[t] = &decl
	g.output.declsByName[decl.Name] = &decl

	// The struct of a declared type is where the conditional discriminator's
	// validator attaches.
	g.declaring[t] = struct{}{}
	theType, err := g.generateType(t, scope)

	delete(g.declaring, t)

	if err != nil {
		return nil, err
	}

	if isNamedType(theType) || (isMapType(theType) && g.config.DisableCustomTypesForMaps) {
		// Don't declare named types under a new name.
		g.output.forgetDecl(t, &decl, theType)
		g.warnUncompiledOneOf(t, theType, scope)

		return theType, nil
	}

	// Only now is the suffixed name known to survive. Warning any earlier
	// describes a `_1` declaration that the branch above may have just thrown
	// away — which is what made `--collision-strategy=qualify` report a
	// collision for every qualified definition while emitting none.
	g.output.warnNameCollision(nameCollided, scope, name)

	decl.Type = theType

	g.warnUncompiledOneOf(t, theType, scope)

	g.output.file.Package.AddDecl(&decl)

	if g.config.OnlyModels {
		return &codegen.NamedType{Decl: &decl}, nil
	}

	var validators []validator

	switch tt := theType.(type) {
	case *codegen.StructType:
		if t.GetSubSchemaType() == schemas.SubSchemaTypeAnyOf {
			validators = append(validators, newAnyOfValidator(decl.Name, t.GetSubSchemasCount(), g.output.anyOfVariants[t]))
			validators = append(validators, presenceCountValidators(t, decl.Name)...)
			g.generateUnmarshaler(&decl, validators)

			return &codegen.NamedType{Decl: &decl}, nil
		}

		// Null checks precede the required ones: those deliberately pass for a
		// null instance (the keyword is vacuous off-type), so the container
		// check is what turns a null into the type error it actually is.
		validators = append(validators, g.nullTypeValidators(t, tt, decl.Name, true)...)

		for _, f := range tt.RequiredJSONFields {
			validators = append(validators, &requiredValidator{f, decl.Name})
		}

		validators = append(validators, g.nullTypeValidators(t, tt, decl.Name, false)...)
		validators = append(validators, g.enumNullValidators(tt, decl.Name)...)

		if v := g.strictFieldsValidatorFor(t, tt, decl.Name); v != nil {
			validators = append(validators, v)
		}

		// Attach the conditional-discriminator validator when the schema
		// matches the allOf+if/then[/else] pattern. Detection is repeated
		// here (also runs in generateType) — the cost is negligible for
		// non-matching schemas, and re-detecting avoids threading state
		// through generateType's return signature.
		if cd, ok := g.detectConditionalDiscriminator(t); ok && !g.config.OnlyModels {
			cd.declName = decl.Name
			validators = append(validators, cd)
		}

		// A oneOf or anyOf whose branches only list required is a count of
		// the branches whose properties are all present.
		validators = append(validators, presenceCountValidators(t, decl.Name)...)

		for _, f := range tt.Fields {
			if f.DefaultValue != nil {
				if f.Name == additionalProperties {
					g.output.file.Package.AddImport("reflect", "")
					g.output.file.Package.AddImport("strings", "")
					g.output.file.Package.AddImport("github.com/go-viper/mapstructure/v2", "")
				}

				dvType := f.Type
				dvIsPointer := false

				if pt, ok := f.Type.(*codegen.PointerType); ok {
					dvType = pt.Type
					dvIsPointer = true
				}

				if v := g.defaultValidatorFor(f, dvType, dvIsPointer); v != nil {
					validators = append(validators, v)
				}
			}

			if !g.config.DisableReadOnlyValidation && f.SchemaType.ReadOnly {
				validators = append(validators, &readOnlyValidator{
					jsonName: f.JSONName,
					declName: decl.Name,
				})
			}

			validators = g.structFieldValidators(validators, f, f.Type, false)
		}

		if t.IsSubSchemaTypeElem() || len(validators) > 0 {
			g.generateUnmarshaler(&decl, validators)
		}

	case codegen.PrimitiveType, *codegen.PrimitiveType:
		// A declared type cannot know which property holds it, so its errors
		// name the type: `field MinStr length: ...`, not `field  length: ...`.
		// JSONName only labels the message; the value read stays `plain`.
		validators = g.structFieldValidators(nil, codegen.StructField{
			Type:       tt,
			SchemaType: t,
			JSONName:   decl.Name,
		}, tt, false)

		if v := g.nonNullValueValidatorFor(t, decl.Name); v != nil {
			validators = append(validators, v)
		}

		if t.IsSubSchemaTypeElem() || len(validators) > 0 {
			g.generateUnmarshaler(&decl, validators)
		}

	case codegen.MapType, *codegen.MapType, codegen.ArrayType, *codegen.ArrayType:
		g.generateContainerUnmarshaler(&decl, t, tt)
	}

	return &codegen.NamedType{Decl: &decl}, nil
}

// generateContainerUnmarshaler gives a declared map or array the unmarshaler it
// needs. A map that is a subschema element always gets one. A declared array
// gets one to check its own `minItems`/`maxItems`, and either kind gets one to
// check the values it holds: a field holding it sees only the named type, so a
// declared array, such as a `$ref`'d definition of `type: array`, is checked
// nowhere else.
func (g *schemaGenerator) generateContainerUnmarshaler(decl *codegen.TypeDecl, t *schemas.Type, tt codegen.Type) {
	validators := []validator{}

	_, isMap := tt.(codegen.MapType)
	if _, ok := tt.(*codegen.MapType); ok {
		isMap = true
	}

	_, isArray := tt.(codegen.ArrayType)
	if _, ok := tt.(*codegen.ArrayType); ok {
		isArray = true
	}

	// Only the array's own bounds. A nested array's bounds belong to its own
	// schema, which the element walk checks.
	if maxItems, hasMax := effectiveMaxItems(t); isArray && (t.MinItems != 0 || hasMax) {
		validators = append(validators, &arrayValidator{
			jsonName:    decl.Name,
			arrayDepth:  1,
			minItems:    t.MinItems,
			maxItems:    maxItems,
			maxItemsSet: hasMax,
		})
	}

	// As for a declared primitive, the errors name the type.
	field := codegen.StructField{Type: tt, SchemaType: t, JSONName: decl.Name}
	if ev := g.elementValidatorFor(field, tt, false); ev != nil {
		validators = append(validators, ev)
	}

	if v := g.nonNullValueValidatorFor(t, decl.Name); v != nil {
		validators = append(validators, v)
	}

	if (isMap && t.IsSubSchemaTypeElem()) || len(validators) > 0 {
		g.generateUnmarshaler(decl, validators)
	}
}

// enumNullValidators rejects a property present with an explicit null where
// its type admits null but its enum does not list it: the type check passes
// and the enum check fails. The field is a pointer, so the null would decode
// to nil unchecked. This is enum validation, so unlike ValidateNullTypes it
// is always on.
func (g *schemaGenerator) enumNullValidators(tt *codegen.StructType, declName string) []validator {
	var out []validator

	for i, f := range tt.Fields {
		s := f.SchemaType
		for hops := 0; s != nil && s.Ref != "" && hops < maxNullCheckRefHops; hops++ {
			s = g.localRefTarget(s)
		}

		if s == nil || s.Enum == nil || enumListsNull(s) {
			continue
		}

		if _, nullable := isTypeNullable(s); !nullable {
			continue
		}

		// The key is matched as nullTypeValidators matches it.
		v := &nonNullValidator{jsonName: f.JSONName, declName: declName}
		v.caseVariants, v.exactOnly = fieldCaseVariants(tt.Fields, i)

		out = append(out, v)
	}

	return out
}

// nullTypeValidators builds the opt-in `type` checks for explicit nulls. The
// container check and the per-property checks are requested separately because
// they bracket the required checks: the container one must run before them, the
// property ones after.
//
// Returns nothing unless ValidateNullTypes is set.
func (g *schemaGenerator) nullTypeValidators(
	t *schemas.Type,
	tt *codegen.StructType,
	declName string,
	container bool,
) []validator {
	if !g.config.ValidateNullTypes {
		return nil
	}

	if container {
		if !schemaExcludesNull(t) {
			return nil
		}

		return []validator{&nonNullContainerValidator{declName}}
	}

	var out []validator

	for i, f := range tt.Fields {
		excludesNull := g.schemaExcludesNullResolved(f.SchemaType)
		forbidden := g.nullForbiddenLevels(f.SchemaType)

		if !excludesNull && len(forbidden) == 0 {
			continue
		}

		// Properties differing only in case split the keys between them as
		// encoding/json does, so the check has to split them the same way.
		caseVariants, exactOnly := fieldCaseVariants(tt.Fields, i)

		if excludesNull {
			out = append(out, &nonNullValidator{
				jsonName:     f.JSONName,
				declName:     declName,
				caseVariants: caseVariants,
				exactOnly:    exactOnly,
			})
		}

		if len(forbidden) > 0 {
			out = append(out, &nonNullElementsValidator{
				jsonName:     f.JSONName,
				declName:     declName,
				forbidden:    forbidden,
				caseVariants: caseVariants,
				exactOnly:    exactOnly,
			})
		}
	}

	return out
}

// nonNullValueValidatorFor builds the opt-in null check for a declared type
// that is not an object, such as a named primitive or array: the value and its
// array elements, where the schema's `type` excludes null. Returns nil unless
// ValidateNullTypes is set and there is something to check.
func (g *schemaGenerator) nonNullValueValidatorFor(t *schemas.Type, declName string) validator {
	if !g.config.ValidateNullTypes {
		return nil
	}

	excludesNull := schemaExcludesNull(t)
	forbidden := g.nullForbiddenLevels(t)

	if !excludesNull && len(forbidden) == 0 {
		return nil
	}

	return &nonNullValueValidator{declName: declName, excludesNull: excludesNull, forbidden: forbidden}
}

// nullForbiddenLevels walks t's nested arrays, through same-document $refs, and
// reports for each level of nesting whether its elements must not be null, up
// to the deepest level where they must not; nil when no level forbids it. The
// draft-07 tuple form of `items` is not walked.
//
// The walk stops where it comes back to a schema it has already walked. The
// elements at that level are of a recursive declared type, and that type's
// own unmarshaler checks the levels below, so walking on would only repeat
// those checks.
func (g *schemaGenerator) nullForbiddenLevels(t *schemas.Type) []bool {
	var levels []bool

	deepest := -1
	walked := make(map[*schemas.Type]bool)

	for depth := range maxNullCheckRefHops {
		t = g.localRefResolved(t)
		if t == nil || t.Items == nil || !slices.Contains(t.Type, schemas.TypeNameArray) || walked[t] {
			break
		}

		walked[t] = true

		forbidden := g.schemaExcludesNullResolved(t.Items)
		if forbidden {
			deepest = depth
		}

		levels = append(levels, forbidden)
		t = t.Items
	}

	return levels[:deepest+1]
}

// localRefResolved follows t's same-document $refs to the schema they end at,
// or returns nil when one cannot be followed.
func (g *schemaGenerator) localRefResolved(t *schemas.Type) *schemas.Type {
	for hops := 0; t != nil && t.Ref != ""; hops++ {
		if hops == maxNullCheckRefHops {
			return nil
		}

		t = g.localRefTarget(t)
	}

	return t
}

// maxNullCheckRefHops bounds the reference chain walked while deciding whether a
// property admits null. A schema may legitimately point a `$ref` at another
// `$ref`, and a cyclic chain must not hang the generator.
const maxNullCheckRefHops = 16

// schemaExcludesNullResolved answers schemaExcludesNull for a property, looking
// through any `$ref` first.
//
// StructField.SchemaType keeps the reference as written, and a reference
// declares no `type` of its own — so unresolved, a `$ref` to
// `{"type": "string"}` looks like a schema that constrains nothing and no check
// is emitted, while the same type written inline gets one.
//
// Resolved by walking this schema's definitions rather than through
// resolveRef, which generates the referenced type as a side effect. A
// reference this cannot follow — into another document, or to a definition
// that is not there — keeps the previous answer rather than forcing one.
func (g *schemaGenerator) schemaExcludesNullResolved(t *schemas.Type) bool {
	for hops := 0; t != nil && t.Ref != "" && hops < maxNullCheckRefHops; hops++ {
		resolved := g.localRefTarget(t)
		if resolved == nil {
			return false
		}

		t = resolved
	}

	return schemaExcludesNull(t)
}

// localRefTarget returns the schema a same-document `$ref` points at, or nil
// when it points elsewhere or cannot be followed. Nothing is generated.
func (g *schemaGenerator) localRefTarget(t *schemas.Type) *schemas.Type {
	defName, fileName, err := g.extractRefNames(t)
	if err != nil || fileName != "" || defName == "" {
		return nil
	}

	def, err := resolveRefPath(g.schema.Definitions, defName)
	if err != nil {
		return nil
	}

	return def
}

// schemaExcludesNull reports whether a schema declares a `type` that does not
// admit null, which is the only situation where an explicit null is invalid.
//
// A schema with no `type` constrains nothing, and a nullable union such as
// `{"type": ["string", "null"]}` admits null deliberately; both must keep
// accepting it.
func schemaExcludesNull(t *schemas.Type) bool {
	if t == nil || len(t.Type) == 0 {
		return false
	}

	return !slices.Contains(t.Type, schemas.TypeNameNull)
}

// strictFieldsValidatorFor returns a strictFieldsValidator when the active
// configuration calls for `additionalProperties: false` enforcement on this
// type, or nil when no such validator should be emitted.
//
// Enforcement is suppressed for objects that have a typed `additionalProperties`
// (a catch-all field is generated instead), and for objects that declare
// `patternProperties` (which would erroneously be rejected; this is a known
// limitation since patternProperties has no first-class generator support).
func (g *schemaGenerator) strictFieldsValidatorFor(
	t *schemas.Type,
	tt *codegen.StructType,
	declName string,
) *strictFieldsValidator {
	// Only the `false` schema forbids every extra property. Any other
	// `not` schema forbids some values and allows the rest, so enforcing
	// on it would reject data the schema accepts.
	schemaSaysFalse := isFalseSchema(t.AdditionalProperties)
	schemaTypedAddl := t.AdditionalProperties != nil && t.AdditionalProperties.Not == nil

	switch g.config.StrictAdditionalProperties {
	case StrictAdditionalPropertiesOff:
		return nil

	case StrictAdditionalPropertiesRespectSchema:
		if !schemaSaysFalse {
			return nil
		}

	case StrictAdditionalPropertiesStrict:
		if schemaTypedAddl {
			return nil
		}
	}

	if len(t.PatternProperties) > 0 {
		g.warner(fmt.Sprintf(
			"strictAdditionalProperties: skipping %s because patternProperties is not supported",
			declName,
		))

		return nil
	}

	known := make([]string, 0, len(tt.Fields))

	for _, f := range tt.Fields {
		if f.Name == additionalProperties {
			continue
		}

		known = append(known, f.JSONName)
	}

	slices.Sort(known)

	return &strictFieldsValidator{
		declName:    declName,
		knownFields: known,
	}
}

//nolint:gocyclo // todo: reduce cyclomatic complexity
func (g *schemaGenerator) structFieldValidators(
	validators []validator,
	f codegen.StructField,
	t codegen.Type,
	isNillable bool,
) []validator {
	switch v := t.(type) {
	case codegen.NullType:
		validators = append(validators, &nullTypeValidator{
			fieldName: f.Name,
			jsonName:  f.JSONName,
		})

	case *codegen.PointerType:
		validators = g.structFieldValidators(validators, f, v.Type, v.IsNillable())

	case codegen.PrimitiveType:
		switch {
		case v.Type == schemas.TypeNameString:
			pattern := g.compilablePattern(f.SchemaType.Pattern, f.JSONName)
			if f.SchemaType.MinLength != 0 || f.SchemaType.MaxLength != 0 || pattern != "" || f.SchemaType.Const != nil {
				var constVal *string

				if f.SchemaType.Const != nil {
					if s, ok := f.SchemaType.Const.(string); ok {
						constVal = &s
					} else {
						g.warner(fmt.Sprintf("Ignoring non string const value: %v", f.SchemaType.Const))
					}
				}

				validators = append(validators, &stringValidator{
					jsonName:   f.JSONName,
					fieldName:  f.Name,
					minLength:  f.SchemaType.MinLength,
					maxLength:  f.SchemaType.MaxLength,
					pattern:    pattern,
					constVal:   constVal,
					isNillable: isNillable,
				})
			}

			if pattern != "" {
				g.output.file.Package.AddImport("regexp", "")
			}

			if format := f.SchemaType.Format; format != "" &&
				isKnownFormatKeyword(format) &&
				g.config.FormatValidation.shouldValidate(format) {
				validators = append(validators, &formatValidator{
					jsonName:   f.JSONName,
					fieldName:  f.Name,
					format:     format,
					isNillable: isNillable,
				})
			}

		case strings.Contains(v.Type, "int") || v.Type == float64Type:
			if f.SchemaType.MultipleOf != nil ||
				f.SchemaType.Maximum != nil ||
				f.SchemaType.ExclusiveMaximum != nil ||
				f.SchemaType.Minimum != nil ||
				f.SchemaType.ExclusiveMinimum != nil ||
				f.SchemaType.Const != nil {
				validators = append(validators, &numericValidator{
					jsonName:         f.JSONName,
					fieldName:        f.Name,
					isNillable:       isNillable,
					multipleOf:       f.SchemaType.MultipleOf,
					maximum:          f.SchemaType.Maximum,
					exclusiveMaximum: f.SchemaType.ExclusiveMaximum,
					minimum:          f.SchemaType.Minimum,
					exclusiveMinimum: f.SchemaType.ExclusiveMinimum,
					constVal:         f.SchemaType.Const,
					roundToInt:       strings.Contains(v.Type, "int"),
				})
			}

			if f.SchemaType.MultipleOf != nil && v.Type == float64Type {
				g.output.file.Package.AddImport("math", "")
			}

		case v.Type == "bool":
			if f.SchemaType.Const != nil {
				var constVal *bool

				if f.SchemaType.Const != nil {
					if b, ok := f.SchemaType.Const.(bool); ok {
						constVal = &b
					} else {
						g.warner(fmt.Sprintf("Ignoring non boolean const value: %v", f.SchemaType.Const))
					}
				}

				validators = append(validators, &booleanValidator{
					jsonName:   f.JSONName,
					fieldName:  f.Name,
					isNillable: isNillable,
					constVal:   constVal,
				})
			}
		}

	case *codegen.ArrayType:
		// Only the field's own bounds: those of a nested array are its own,
		// which the element walk checks.
		if maxItems, hasMax := effectiveMaxItems(f.SchemaType); f.SchemaType.MinItems != 0 || hasMax {
			validators = append(validators, &arrayValidator{
				fieldName:   f.Name,
				jsonName:    f.JSONName,
				arrayDepth:  1,
				minItems:    f.SchemaType.MinItems,
				maxItems:    maxItems,
				maxItemsSet: hasMax,
			})
		}

		arrayDepth := 0
		for v, ok := t.(*codegen.ArrayType); ok; v, ok = t.(*codegen.ArrayType) {
			arrayDepth++
			if _, ok := v.Type.(codegen.NullType); ok {
				validators = append(validators, &nullTypeValidator{
					fieldName:  f.Name,
					jsonName:   f.JSONName,
					arrayDepth: arrayDepth,
				})

				break
			}

			t = v.Type
		}

		if ev := g.elementValidatorFor(f, v, isNillable); ev != nil {
			validators = append(validators, ev)
		}

	case codegen.ArrayType, *codegen.MapType, codegen.MapType:
		if ev := g.elementValidatorFor(f, t, isNillable); ev != nil {
			validators = append(validators, ev)
		}

	case codegen.EmptyInterfaceType:
		if c := g.untypedConstValidatorFor(f.JSONName, f.SchemaType); c != nil {
			c.property = true
			validators = append(validators, c)
		}
	}

	return validators
}

// elementValidatorFor returns the checks on the values an inline array or map
// holds, nested ones included, or nil when there are none. Each value is
// checked against its own schema, so a nested array has its own bounds, not
// the field's. Before, only `format` reached an element, and `items:
// {minLength: 1}` let an empty string through.
//
// The walk stops at a named element type: that type is declared separately and
// checks its own value in its own unmarshaler.
func (g *schemaGenerator) elementValidatorFor(
	f codegen.StructField,
	t codegen.Type,
	isNillable bool,
) validator {
	schema := f.SchemaType

	var levels []elementLevel

	checked := 0

	for schema != nil {
		elem, elemSchema, container, ok := containerElement(t, schema)
		if !ok {
			break
		}

		level := elementLevel{container: container}
		if pt, ok := elem.(*codegen.PointerType); ok {
			elem, level.isElemNillable = pt.Type, true
		}

		if elemSchema != nil {
			level.checks = g.elementChecks(f.JSONName, elem, elemSchema)
		}

		levels = append(levels, level)

		if len(level.checks) > 0 {
			checked = len(levels)
		}

		t, schema = elem, elemSchema
	}

	if checked == 0 {
		return nil
	}

	return &elementsValidator{
		jsonName:   f.JSONName,
		fieldName:  f.Name,
		isNillable: isNillable,
		levels:     levels[:checked],
		// The struct's catch-all map is filled after the other checks run.
		afterAdditionalProperties: f.Name == additionalProperties,
	}
}

// elementChecks returns the checks on a value of Go type t and schema s that
// an array or map holds: a nested array's own bounds, the checks a property of
// t's primitive type gets, or a const of no declared type. owner names the
// property in warnings.
func (g *schemaGenerator) elementChecks(owner string, t codegen.Type, s *schemas.Type) []elementCheck {
	var checks []elementCheck

	switch tt := t.(type) {
	case *codegen.ArrayType, codegen.ArrayType:
		if maxItems, hasMax := effectiveMaxItems(s); s.MinItems != 0 || hasMax {
			checks = append(checks, &arrayValidator{
				jsonName:    owner,
				arrayDepth:  1,
				minItems:    s.MinItems,
				maxItems:    maxItems,
				maxItemsSet: hasMax,
			})
		}

	case codegen.PrimitiveType:
		field := codegen.StructField{Type: tt, SchemaType: s, JSONName: owner}

		for _, v := range g.structFieldValidators(nil, field, tt, false) {
			if c, ok := v.(elementCheck); ok {
				checks = append(checks, c)
			}
		}

	case codegen.EmptyInterfaceType:
		if c := g.untypedConstValidatorFor(owner, s); c != nil {
			checks = append(checks, c)
		}
	}

	return checks
}

// untypedConstValidatorFor returns the check of s's const where s declares no
// type, or nil. A const that cannot be written as JSON is left out with a
// warning.
func (g *schemaGenerator) untypedConstValidatorFor(owner string, s *schemas.Type) *untypedConstValidator {
	if !isUntypedConst(s) {
		return nil
	}

	encoded, err := json.Marshal(s.Const)
	if err != nil {
		g.warner(fmt.Sprintf("schema fidelity: const of %q is not checked: %v", owner, err))

		return nil
	}

	return &untypedConstValidator{jsonName: owner, encoded: string(encoded)}
}

// containerElement steps from an inline array or map to the element it holds,
// together with the element's schema. Both forms of each type occur: inline
// fields hold pointers, while declared types and map values hold values.
func containerElement(t codegen.Type, s *schemas.Type) (codegen.Type, *schemas.Type, containerLevel, bool) {
	switch ct := t.(type) {
	case *codegen.ArrayType:
		return ct.Type, elementSchema(s), containerArray, true

	case codegen.ArrayType:
		return ct.Type, elementSchema(s), containerArray, true

	case *codegen.MapType:
		return ct.ValueType, s.AdditionalProperties, containerMap, true

	case codegen.MapType:
		return ct.ValueType, s.AdditionalProperties, containerMap, true
	}

	return nil, nil, 0, false
}

// isStringType reports whether t is the primitive string type.
func isStringType(t codegen.Type) bool {
	switch pt := t.(type) {
	case codegen.PrimitiveType:
		return pt.Type == schemas.TypeNameString

	case *codegen.PrimitiveType:
		return pt.Type == schemas.TypeNameString
	}

	return false
}

// compilablePattern returns pattern as the generated code writes it, inside a
// raw string literal with JSON's control characters escaped, or "" when there
// is none or Go's regexp cannot compile it. JSON Schema's patterns are
// ECMA-262, and RE2, which Go's regexp implements, has no lookaround or
// backreference. Emitted anyway, the check would reject every value, valid
// ones included, because the compile error is lost when the generated code
// runs. Such a pattern is reported and left unchecked. owner names the field
// or type the pattern constrains, for the warning.
func (g *schemaGenerator) compilablePattern(pattern, owner string) string {
	if pattern == "" {
		return ""
	}

	// Double escape the escape characters so we don't effectively parse the
	// escapes within the value.
	escaped := pattern

	replaceJSONCharactersBy := []string{"\\b", "\\f", "\\n", "\\r", "\\t"}

	replaceJSONCharacters := []string{"\b", "\f", "\n", "\r", "\t"}
	for i, replace := range replaceJSONCharacters {
		escaped = strings.ReplaceAll(escaped, replace, replaceJSONCharactersBy[i])
	}

	if _, err := regexp.Compile(escaped); err != nil {
		g.warner(fmt.Sprintf("schema fidelity: pattern %q of %q is not checked: Go's regexp cannot compile it (%v)",
			pattern, owner, err))

		return ""
	}

	return escaped
}

// defaultValidatorFor sets field f to its default while its key is absent;
// dvType is f's type with any pointer removed.
//
// A primitive union wrapper takes its default the way it takes any value,
// through its own UnmarshalJSON, so the default is stored exactly as a decoded
// one would be. A default of a kind the wrapper does not accept is left out
// with a warning: it could never be decoded, and a default is only an
// annotation.
func (g *schemaGenerator) defaultValidatorFor(
	f codegen.StructField,
	dvType codegen.Type,
	isPointer bool,
) validator {
	if nt, ok := dvType.(*codegen.NamedType); ok {
		if kinds, ok := g.output.primitiveWrapperKinds[nt.Decl]; ok {
			encoded, err := json.Marshal(f.DefaultValue)
			if err != nil || !kinds.has(primitiveKindOf(f.DefaultValue)) {
				g.warner(fmt.Sprintf(
					"default %s for field %q is not a value its type %s accepts; it is left out",
					encoded, f.JSONName, nt.Decl.Name,
				))

				return nil
			}

			return &wrapperDefaultValidator{
				jsonName:    f.JSONName,
				fieldName:   f.Name,
				encoded:     string(encoded),
				acceptsNull: kinds.has(primitiveKindNull),
			}
		}
	}

	return &defaultValidator{
		jsonName:         f.JSONName,
		fieldName:        f.Name,
		defaultValueType: dvType,
		defaultValue:     f.DefaultValue,
		isPointer:        isPointer,
		warner:           g.warner,
	}
}

func (g *schemaGenerator) generateUnmarshaler(decl *codegen.TypeDecl, validators []validator) {
	if g.config.OnlyModels {
		return
	}

	if hasUnmarshallers, ok := g.output.unmarshallersByTypeDecl[decl]; ok || hasUnmarshallers {
		return
	}

	defer func() {
		g.output.unmarshallersByTypeDecl[decl] = true
	}()

	// A check of a property's raw-map entry matches the keys the decoder
	// assigns to the property, which depend on the struct's other fields.
	if st, ok := decl.Type.(*codegen.StructType); ok {
		for _, v := range validators {
			if rv, ok := v.(rawKeyedValidator); ok {
				rv.matchStructKeys(st.Fields)
			}
		}
	}

	for _, v := range validators {
		if _, ok := v.(*anyOfValidator); ok {
			g.output.file.Package.AddImport("errors", "")
		}

		for _, pkg := range v.desc().imports {
			g.output.file.Package.AddImport(pkg.qualifiedName, "")
		}

		for _, decl := range v.desc().decls {
			g.output.file.Package.AddDecl(decl)
		}

		if v.desc().hasError {
			g.output.file.Package.AddImport("fmt", "")
		}
	}

	for _, formatter := range g.formatters {
		formatter.addImport(g.output.file, decl)

		g.output.file.Package.AddDecl(&codegen.Method{
			Impl: formatter.generate(g.output, decl, validators),
			Name: decl.GetName() + "_validator_" + formatter.getName(),
		})
	}
}

// itemsSchema resolves the element schema for an array, covering both draft-07
// forms of `items`. The single-schema form is returned as-is.
//
// For the tuple form, a one-element tuple — the common "a tuple of exactly one"
// idiom, usually paired with minItems/maxItems 1 — maps cleanly onto that
// element's type, so the array is generated as a properly typed slice. A
// heterogeneous tuple has no faithful Go slice representation, so it warns and
// returns nil, leaving the caller to fall back to an untyped array. Positional
// types and `additionalItems` are not otherwise enforced.
func (g *schemaGenerator) itemsSchema(t *schemas.Type, scope nameScope) *schemas.Type {
	if items := elementSchema(t); items != nil || len(t.TupleItems) == 0 {
		return items
	}

	g.warner(fmt.Sprintf(
		"Array %s uses a tuple for items that does not describe a single "+
			"element type; positional types are not modelled and it will be "+
			"represented as an untyped array",
		scope.string(),
	))

	return nil
}

// elementSchema returns the schema every element of the array t follows, or
// nil when there is none. It is itemsSchema without the warning, for callers
// that only read the schema.
func elementSchema(t *schemas.Type) *schemas.Type {
	if len(t.TupleItems) == 0 {
		return t.Items
	}

	// Only a CLOSED one-element tuple describes every element. Left open,
	// `items: [A]` constrains position 0 and leaves positions 1+ to any
	// type, so collapsing it to []A would reject data the schema allows.
	if len(t.TupleItems) == 1 && tupleIsClosed(t) {
		return t.TupleItems[0]
	}

	return nil
}

// tupleIsClosed reports whether a tuple forbids elements beyond the ones it
// lists, either by capping the length with maxItems or by disallowing extras
// with `additionalItems: false`.
func tupleIsClosed(t *schemas.Type) bool {
	if t.MaxItems == len(t.TupleItems) {
		return true
	}

	return isFalseSchema(t.AdditionalItems)
}

// effectiveMaxItems returns the array length cap a schema implies, including
// the one a closed tuple carries without saying so.
//
// `items: [A], additionalItems: false` permits at most len(items) elements, but
// expresses that through `additionalItems` rather than `maxItems`. itemsSchema
// collapses exactly that shape to []A, so without this the generated slice
// would accept more elements than the schema allows — `["a", "b"]` would decode
// cleanly against a tuple that admits one element.
func effectiveMaxItems(t *schemas.Type) (int, bool) {
	// Presence is returned separately from the value because a closed tuple
	// can legitimately cap the array at zero: `items: [], additionalItems:
	// false` leaves every element "additional" and so forbidden, admitting
	// only the empty array. Reporting that as a bare 0 is indistinguishable
	// from "no maximum" and emits no check at all.
	// `additionalItems` only has meaning alongside the tuple form of `items`
	// (draft-07 §6.4.2); with a single-schema `items` it applies to nothing
	// and must not cap the array. TupleItems is non-nil exactly when the
	// tuple form was parsed, which is what distinguishes `items: []` — a
	// closed tuple of zero members — from `items: {...}`.
	tupleClosed := t.TupleItems != nil && isFalseSchema(t.AdditionalItems)

	tupleMax := 0
	if tupleClosed {
		tupleMax = len(t.TupleItems)
	}

	switch {
	case !tupleClosed:
		return t.MaxItems, t.MaxItems != 0

	case t.MaxItems == 0:
		return tupleMax, true

	default:
		// Both cap the array, so the tighter one wins. A schema may well
		// declare `maxItems: 3` beside a one-member closed tuple; the
		// tuple still admits one element, and taking maxItems on its own
		// would let two more through.
		return min(t.MaxItems, tupleMax), true
	}
}

// isFalseSchema reports whether t is the JSON Schema `false`.
//
// Type.UnmarshalJSON decodes `false` as a `not` of the empty (always-true)
// schema, i.e. Type{Not: &Type{}}. Testing merely for a non-nil Not would also
// match a real constraint such as {"not": {"type": "integer"}}, which still
// permits values — treating that as closed would wrongly collapse a tuple to a
// single element type and reject data the schema allows.
func isFalseSchema(t *schemas.Type) bool {
	return t != nil && t.Not != nil && reflect.DeepEqual(*t.Not, schemas.Type{})
}

func (g *schemaGenerator) generateType(t *schemas.Type, scope nameScope) (codegen.Type, error) {
	if ext := t.GoJSONSchemaExtension; ext != nil {
		for _, pkg := range ext.Imports {
			g.output.file.Package.AddImport(pkg, "")
		}

		if ext.Type != nil {
			return &codegen.CustomNameType{Type: *ext.Type, Nillable: ext.Nillable}, nil
		}
	}

	g.warnUncompiledConditional(t, scope, g.isDeclaring(t))

	if t.Enum != nil {
		return g.generateEnumType(t, scope)
	}

	if t.Ref != "" {
		return g.generateReferencedType(t)
	}

	// AnyOf / AllOf must be delegated to their dedicated generators before
	// falling through to determineTypeName + the type switch — otherwise a
	// schema like `{allOf:[{$ref:Base},{type:"object",...}]}` ends up with
	// determineTypeName returning "null" (the variants have mismatched
	// Type slices: an empty one for the $ref and `[object]` for the inline
	// branch) and the schema is silently emitted as `interface{}`. This
	// mirrors the same delegation already done by generateTypeInline and
	// is required for the discriminator-detection path (Phase 5) to emit
	// real variant structs instead of interface{}.
	//
	// On merge failure (e.g. unsupported nested definitions whose refs
	// can't resolve), warn and fall back to interface{} rather than
	// surfacing a hard error — preserves the previous silent-fallback
	// behaviour for schemas that hit unsupported features. A schema that
	// declares `type: object` is the exception: it went through
	// generateStructType before, which returned the merge error, so it
	// still fails rather than becoming an unchecked interface{}.
	//
	// An anyOf that is a presence count has nothing to merge: the struct for
	// t checks it.
	if len(t.AnyOf) > 0 && !isPresenceAnyOf(t) {
		dt, err := g.generateAnyOfType(t, scope)
		if err != nil {
			if slices.Contains(t.Type, schemas.TypeNameObject) {
				return nil, err
			}

			g.warner(fmt.Sprintf("anyOf generation failed for %v; falling back to interface{}: %v", scope, err))

			return codegen.EmptyInterfaceType{}, nil
		}

		return dt, nil
	}

	// Conditional-discriminator pattern: allOf of {if: {<K>: const}, then,
	// else?} keyed on a single discriminator property's const value. Only a
	// declared type takes its validator: elsewhere, such as an element of a
	// declared array, t goes the general way, which reports what it drops.
	// Bypass the AllOf merge (which would lose the parent's struct shape)
	// and emit the struct as a regular object — the per-discriminator
	// conditional checks attach as a runtime validator in
	// generateDeclaredType.
	if cd, ok := g.detectConditionalDiscriminator(t); ok && g.isDeclaring(t) {
		tCopy := *t
		tCopy.AllOf = nil

		// The allOf's conditionals compile into the discriminator, and so
		// does the schema's own if/then/else where it is a branch too; any
		// other conditional of its own does not, and is reported for t.
		if cd.compilesOwn {
			tCopy = *withoutOwnConditional(&tCopy)
		}

		g.reportOwnConditional(t, &tCopy, scope)

		return g.generateStructType(&tCopy, scope)
	}

	if len(t.AllOf) > 0 {
		dt, err := g.generateAllOfType(t, scope)
		if err != nil {
			if slices.Contains(t.Type, schemas.TypeNameObject) {
				return nil, err
			}

			g.warner(fmt.Sprintf("allOf generation failed for %v; falling back to interface{}: %v", scope, err))

			return codegen.EmptyInterfaceType{}, nil
		}

		return dt, nil
	}

	typeName, typePtr := g.determineTypeName(t)

	switch typeName {
	case schemas.TypeNameArray:
		items := g.itemsSchema(t, scope)
		if items == nil {
			return arrayTypeVal, nil
		}

		elemType, err := g.generateType(items, g.singularScope(scope))
		if err != nil {
			return nil, err
		}

		return codegen.ArrayType{Type: elemType}, nil

	case schemas.TypeNameObject:
		return g.generateStructType(t, scope)

	case schemas.TypeNameNull:
		// A composition with no top-level `type` lands here — determineTypeName
		// reports "null" for it. This is the additionalProperties-positioned
		// fallback; warn before degrading so the loss is visible.
		g.warnCompositionFallback(t, scope)

		return emptyInterfaceTypeVal, nil

	default:
		return g.primitiveType(t, typeName, typePtr)
	}
}

// typeFormat is the format to type t's typeName by. An integer's format is an
// OpenAPI one, int32 or int64, since JSON Schema defines none, so it sizes the
// integer only under OpenAPIIntFormats.
func (g *schemaGenerator) typeFormat(t *schemas.Type, typeName string) string {
	if typeName == schemas.TypeNameInteger && !g.config.OpenAPIIntFormats {
		return ""
	}

	return t.Format
}

func (g *schemaGenerator) primitiveType(t *schemas.Type, name string, isNillable bool) (codegen.Type, error) {
	cg, err := codegen.PrimitiveTypeFromJSONSchemaType(
		name,
		g.typeFormat(t, name),
		isNillable,
		g.config.MinSizedInts,
		&t.Minimum,
		&t.Maximum,
		&t.ExclusiveMinimum,
		&t.ExclusiveMaximum,
	)
	if err != nil {
		return nil, fmt.Errorf("invalid type %q: %w", name, err)
	}

	if ncg, ok := cg.(codegen.NamedType); ok {
		for _, imprt := range ncg.Package.Imports {
			g.output.file.Package.AddImport(imprt.QualifiedName, "")
		}

		return ncg, nil
	}

	if pcg, ok := cg.(*codegen.PointerType); ok {
		if ncg, ok := pcg.Type.(codegen.NamedType); ok {
			for _, imprt := range ncg.Package.Imports {
				g.output.file.Package.AddImport(imprt.QualifiedName, "")
			}

			return pcg, nil
		}
	}

	if dcg, ok := cg.(codegen.DurationType); ok {
		g.output.file.Package.AddImport("time", "")

		return dcg, nil
	}

	return cg, nil
}

func (g *schemaGenerator) determineTypeName(t *schemas.Type) (string, bool) {
	if len(t.Type) == 0 {
		if len(t.AnyOf) == 0 && len(t.AllOf) == 0 {
			return schemas.TypeNameNull, false
		}

		if len(t.AnyOf) != 0 {
			refType := t.AnyOf[0]

			for k, v := range t.AnyOf {
				if k == 0 {
					continue
				}

				if !refType.Type.Equals(v.Type) {
					return schemas.TypeNameNull, false
				}
			}

			return g.determineTypeName(refType)
		}

		if len(t.AllOf) != 0 {
			refType := t.AllOf[0]

			for k, v := range t.AllOf {
				if k == 0 {
					continue
				}

				if !refType.Type.Equals(v.Type) {
					return schemas.TypeNameNull, false
				}
			}

			return g.determineTypeName(refType)
		}

		return schemas.TypeNameNull, false
	}

	if len(t.Type) == 1 {
		return t.Type[0], false
	}

	if len(t.Type) == 2 {
		tidx := 0
		isPtr := false

		for k, v := range t.Type {
			if v == "null" {
				isPtr = true

				continue
			}

			tidx = k
		}

		return t.Type[tidx], isPtr
	}

	g.warner("Property has multiple types; will be represented as interface{} with no validation")

	return schemas.TypeNameNull, false
}

func (g *schemaGenerator) generateStructType(t *schemas.Type, scope nameScope) (codegen.Type, error) {
	if len(t.Properties) == 0 && len(t.AllOf) == 0 && len(t.AnyOf) == 0 {
		if len(t.Required) > 0 {
			g.warner("Object type with no properties has required fields; " +
				"skipping validation code for them since we don't know their types")
		}

		// Property-less object schemas that should reject unknown keys cannot
		// be represented as a map (no validator hook). Synthesize an empty
		// struct so the standard strictFieldsValidatorFor path attaches a
		// rejector. Without this, schemas like `{"type":"object",
		// "additionalProperties":false}` silently accept any map under the
		// `RespectSchema` mode, and `Strict` mode misses property-less
		// schemas entirely.
		//
		// Skip the conversion when patternProperties is present: strict
		// enforcement is suppressed in that case (see strictFieldsValidatorFor),
		// so an empty struct would silently drop pattern-matched keys instead
		// of preserving the map behavior.
		// Only the `false` schema forbids every extra property. Any other
		// `not` schema forbids some values and allows the rest, so enforcing
		// on it would reject data the schema accepts.
		schemaSaysFalse := isFalseSchema(t.AdditionalProperties)
		schemaTypedAddl := t.AdditionalProperties != nil && t.AdditionalProperties.Not == nil
		hasPatternProps := len(t.PatternProperties) > 0

		if !hasPatternProps {
			switch g.config.StrictAdditionalProperties {
			case StrictAdditionalPropertiesOff:
				// No enforcement — falls through to the map representation below.

			case StrictAdditionalPropertiesRespectSchema:
				if schemaSaysFalse {
					return &codegen.StructType{}, nil
				}

			case StrictAdditionalPropertiesStrict:
				if !schemaTypedAddl {
					return &codegen.StructType{}, nil
				}
			}
		}

		valueType := codegen.Type(emptyInterfaceTypeVal)

		var err error

		// `false` admits no value, so it has no value type to generate; the
		// map keeps interface{}, and whether its keys are checked is up to
		// StrictAdditionalProperties.
		if t.AdditionalProperties != nil && !schemaSaysFalse {
			if valueType, err = g.generateType(t.AdditionalProperties, scope.add("Value")); err != nil {
				return nil, err
			}
		}

		return &codegen.MapType{
			KeyType:   codegen.PrimitiveType{Type: schemas.TypeNameString},
			ValueType: valueType,
		}, nil
	}

	requiredNames := make(map[string]bool, len(t.Properties))
	for _, r := range t.Required {
		requiredNames[r] = true
	}

	uniqueNames := make(map[string]int, len(t.Properties))

	var structType codegen.StructType

	for _, name := range sortedKeys(t.Properties) {
		if err := g.addStructField(&structType, t, scope, name, uniqueNames, requiredNames); err != nil {
			return nil, err
		}
	}

	if len(t.AnyOf) > 0 && !isPresenceAnyOf(t) {
		return g.generateAnyOfType(t, scope)
	}

	if len(t.AllOf) > 0 {
		return g.generateAllOfType(t, scope)
	}

	// Checking .Not here because `false` is unmarshalled to .Not = Type{}.
	if t.AdditionalProperties != nil && t.AdditionalProperties.Not == nil {
		var (
			defaultValue any          = nil
			fieldType    codegen.Type = emptyInterfaceTypeVal
		)

		if len(t.AdditionalProperties.Type) == 1 {
			switch t.AdditionalProperties.Type[0] {
			case schemas.TypeNameString:
				defaultValue = map[string]string{}
				fieldType = codegen.MapType{
					KeyType:   stringTypeVal,
					ValueType: stringTypeVal,
				}

			case schemas.TypeNameArray:
				defaultValue = map[string][]any{}
				fieldType = codegen.MapType{
					KeyType:   stringTypeVal,
					ValueType: arrayTypeVal,
				}

			case schemas.TypeNameNumber:
				defaultValue = map[string]float64{}
				fieldType = codegen.MapType{
					KeyType:   stringTypeVal,
					ValueType: codegen.PrimitiveType{Type: float64Type},
				}

			case schemas.TypeNameInteger:
				defaultValue = map[string]int{}
				fieldType = codegen.MapType{
					KeyType:   stringTypeVal,
					ValueType: intTypeVal,
				}

			case schemas.TypeNameBoolean:
				defaultValue = map[string]bool{}
				fieldType = codegen.MapType{
					KeyType:   stringTypeVal,
					ValueType: boolTypeVal,
				}

			default:
				defaultValue = map[string]any{}
				fieldType = codegen.MapType{
					KeyType:   stringTypeVal,
					ValueType: emptyInterfaceTypeVal,
				}
			}
		}

		structType.AddField(
			codegen.StructField{
				Name:         additionalProperties,
				DefaultValue: defaultValue,
				// The map's own schema, so its values are checked like any
				// other map's (their `format`, say).
				SchemaType: &schemas.Type{AdditionalProperties: t.AdditionalProperties},
				Type:       fieldType,
				Tags:       "mapstructure:\",remain\"",
			},
		)
	}

	if t.Default != nil {
		structType.DefaultValue = g.defaultPropertyValue(t)
	}

	return &structType, nil
}

func (g *schemaGenerator) addStructField(
	structType *codegen.StructType,
	t *schemas.Type,
	scope nameScope,
	name string,
	uniqueNames map[string]int,
	requiredNames map[string]bool,
) error {
	prop := t.Properties[name]
	isRequired := requiredNames[name]

	fieldName := g.caser.Identifierize(name)

	var extraTags []string

	if ext := prop.GoJSONSchemaExtension; ext != nil {
		for _, pkg := range ext.Imports {
			g.output.file.Package.AddImport(pkg, "")
		}

		if ext.Identifier != nil {
			fieldName = *ext.Identifier
		}

		for tagKey, tagVal := range ext.ExtraTags {
			extraTags = append(extraTags, fmt.Sprintf(`%s:"%s"`, tagKey, tagVal))
		}
	}

	extraTags = append(extraTags, g.extensionTags(prop, name)...)

	slices.Sort(extraTags)

	if count, ok := uniqueNames[fieldName]; ok {
		uniqueNames[fieldName] = count + 1
		fieldName = fmt.Sprintf("%s_%d", fieldName, count+1)
		g.warner(fmt.Sprintf("Field %q maps to a field by the same name declared "+
			"in the same struct; it will be declared as %s", name, fieldName))
	} else {
		uniqueNames[fieldName] = 1
	}

	comment := prop.Description
	if comment == "" {
		comment = fmt.Sprintf("%s corresponds to the JSON schema field %q.", fieldName, name)
	}

	structFieldType, err := g.generateStructFieldType(prop, scope.add(fieldName), isRequired)
	if err != nil {
		return fmt.Errorf("cannot add struct field: %w", err)
	}

	if !jsonTagNameIsRepresentable(name) {
		return fmt.Errorf("%w: %q", ErrPropertyNameNotRepresentable, name)
	}

	structField := codegen.StructField{
		Name:         fieldName,
		Comment:      withDeprecation(comment, prop),
		JSONName:     name,
		SchemaType:   prop,
		Tags:         g.generateStructFieldTags(name, extraTags, isRequired),
		DefaultValue: g.generateStructFieldDefaultValue(prop),
		Type:         structFieldType,
	}

	g.appendStructRequiredJSONFields(&structField, structType, isRequired)

	structType.AddField(structField)

	return nil
}

// jsonTagNameIsRepresentable reports whether name can be carried in a Go struct
// tag that encoding/json will honour.
//
// This mirrors encoding/json's own isValidTag. A name it rejects does not fall
// back to anything usable: the field is left unset on decode and encode emits a
// truncated key, so `a"b` round-trips as `a`. Escaping cannot help — the
// limitation is in what the codec accepts, not in how the tag is written — so
// such a schema is refused rather than generated with a field that silently
// does not map.
func jsonTagNameIsRepresentable(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r):
			// Punctuation encoding/json accepts in a tag name. Quote,
			// backslash and comma are deliberately absent: the first two are
			// reserved and the third separates tag options.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
		default:
			return false
		}
	}

	return true
}

func (g *schemaGenerator) generateStructFieldTags(name string, extraTags []string, isRequired bool) string {
	var (
		tagsBuilder strings.Builder
		omitJson    string
		omitRest    string
	)

	if !isRequired {
		if !g.config.DisableOmitEmpty {
			omitJson += ",omitempty"
			omitRest += ",omitempty"
		}

		if !g.config.DisableOmitZero {
			omitJson += ",omitzero"
			// omitRest is not set as omitzero is supported only by the json package
		}
	}

	// The name is schema data and lands inside a quoted tag value, which
	// reflect parses with strconv.Unquote. Unescaped, `a"b` truncates the
	// value to `a` and `back\slash` makes the tag unparseable, so reflect
	// reports it as absent and the field silently falls back to its Go name.
	quotedName := goQuotedBody(name)

	// encoding/json reads a tag of exactly "-" as "always omit this field", so
	// a property literally named "-" keeps its key only through the trailing
	// comma the package documents for that case. With options after the name
	// ("-,omitempty") it is already a key. yaml.v3 cannot take the same fix: it
	// rejects the empty option at runtime, so the yaml tag keeps "-" and that
	// property stays unbindable in YAML.
	if name == "-" && omitJson == "" {
		omitJson = ","
	}

	for _, tag := range g.config.Tags {
		switch tag {
		case "json":
			fmt.Fprintf(&tagsBuilder, `%s:"%s%s" `, tag, quotedName, omitJson)
		default:
			fmt.Fprintf(&tagsBuilder, `%s:"%s%s" `, tag, quotedName, omitRest)
		}
	}

	for _, tag := range extraTags {
		fmt.Fprintf(&tagsBuilder, `%s `, tag)
	}

	return strings.TrimSpace(tagsBuilder.String())
}

func (g *schemaGenerator) generateStructFieldDefaultValue(schemaType *schemas.Type) any {
	if schemaType.Default != nil {
		return g.defaultPropertyValue(schemaType)
	}

	return nil
}

func (g *schemaGenerator) generateStructFieldType(
	schemaType *schemas.Type,
	scope nameScope,
	isRequired bool,
) (codegen.Type, error) {
	fieldType, err := g.generateTypeInline(schemaType, scope)
	if err != nil {
		return nil, fmt.Errorf("could not generate type with scope '%s': %w", scope.string(), err)
	}

	var (
		shouldForcePtrToTrue  bool
		shouldForcePtrToFalse bool
	)

	if ext := schemaType.GoJSONSchemaExtension; ext != nil && ext.Pointer != nil {
		shouldForcePtrToTrue = *ext.Pointer
		shouldForcePtrToFalse = !*ext.Pointer
	}

	if fieldType.IsNillable() {
		return fieldType, nil
	}

	if shouldForcePtrToTrue {
		return codegen.WrapTypeInPointer(fieldType), nil
	}

	if shouldForcePtrToFalse {
		return fieldType, nil
	}

	if !isRequired && schemaType.Default == nil {
		return codegen.WrapTypeInPointer(fieldType), nil
	}

	return fieldType, nil
}

func (g *schemaGenerator) appendStructRequiredJSONFields(
	structField *codegen.StructField,
	structType *codegen.StructType,
	isRequired bool,
) {
	if isRequired {
		structType.RequiredJSONFields = append(structType.RequiredJSONFields, structField.JSONName)
	}
}

func (g *schemaGenerator) generateAnyOfType(t *schemas.Type, scope nameScope) (codegen.Type, error) {
	if len(t.AnyOf) == 0 {
		return nil, errEmptyInAnyOf
	}

	if g.config.AliasSingleAllOfAnyOfRefs && len(t.AnyOf) == 1 && t.IsEmptyObject() {
		childType := t.AnyOf[0]
		if childType.Ref != "" {
			resolvedType, err := g.resolveRef(childType)
			if err == nil {
				return g.generateTypeInline(resolvedType, scope)
			} else {
				g.warner(fmt.Sprintf("Could not resolve ref %q: %v", childType.Ref, err))
			}
		}
	}

	isCycle := false
	rAnyOf, hasNull := g.resolveRefs(t.AnyOf, false)

	// A union of a single temporal type with null, such as
	// {"anyOf": [{"type": "string", "format": "date-time"}, {"type": "null"}]},
	// is equivalent to {"type": ["string", "null"], "format": "date-time"} and
	// maps directly to a nillable temporal type.
	if hasNull && len(rAnyOf) == 1 && len(rAnyOf[0].Type) == 1 &&
		isTypeTemporal(rAnyOf[0].Type[0], rAnyOf[0].Format) {
		return g.primitiveType(rAnyOf[0], rAnyOf[0].Type[0], true)
	}

	// When an object branch with properties comes first, the anyOf becomes a
	// struct, which can hold only an object, with a validator that decodes
	// every branch into a type of its own. When a primitive or an array comes
	// first, the anyOf becomes that type, which can hold no object at all.
	// Either way, if some branches admit objects and others a primitive or an
	// array, the type would reject values the schema allows, so the anyOf is
	// interface{} instead, whatever the order, and the fidelity warning names
	// what goes unchecked. A merge into a map keeps its type, as upstream's.
	if admitsStruct(t, rAnyOf) && g.mixesObjectAndNonObject(t, rAnyOf) {
		g.warnFallback(t, scope, "anyOf with both object and non-object branches")

		return emptyInterfaceTypeVal, nil
	}

	variantNames := make([]string, len(rAnyOf))

	for i, typ := range rAnyOf {
		// infer type from base if not set
		if len(typ.Type) == 0 {
			typ.Type = append(schemas.TypeList{}, t.Type...)
		}

		typ.SetSubSchemaTypeElem()

		ic, cleanupCycle, cycleErr := g.detectCycle(typ)
		if cycleErr != nil {
			return nil, cycleErr
		}

		defer cleanupCycle()

		if ic {
			isCycle = true

			continue
		}

		if hasNull {
			typ.Type.Add(schemas.TypeNameNull)
		}

		variant, err := g.generateTypeInline(typ, scope.add(fmt.Sprintf("_%d", i)))
		if err != nil {
			return nil, err
		}

		// A branch generated earlier keeps the name it was declared under,
		// which need not be this scope's. So does a branch that is a declared
		// definition generated inline here, such as a $ref to a string.
		if named := namedDecl(variant); named != nil {
			variantNames[i] = named.Name
		} else if decl, ok := g.output.declsBySchema[typ]; ok {
			variantNames[i] = decl.Name
		}
	}

	if isCycle {
		return emptyInterfaceTypeVal, nil
	}

	anyOfType, err := schemas.AnyOf(rAnyOf, t)
	if err != nil {
		return nil, fmt.Errorf("could not merge anyOf types: %w", err)
	}

	anyOfType.AnyOf = nil
	g.output.anyOfVariants[anyOfType] = variantNames

	// The merge carries the conditionals of t, reported with t, and of its
	// branches, reported with each branch's type above.
	g.reportedConditionals[anyOfType] = struct{}{}

	return g.generateTypeInline(anyOfType, scope)
}

func (g *schemaGenerator) generateAllOfType(t *schemas.Type, scope nameScope) (codegen.Type, error) {
	if g.config.AliasSingleAllOfAnyOfRefs && len(t.AllOf) == 1 && t.IsEmptyObject() {
		subType := t.AllOf[0]
		if subType.Ref != "" {
			resolvedType, err := g.resolveRef(subType)
			if err == nil {
				return g.generateTypeInline(resolvedType, scope)
			} else {
				g.warner(fmt.Sprintf("Could not resolve subtype ref %q: %v", subType.Ref, err))
			}
		}
	}

	rAllOf, _ := g.resolveRefs(t.AllOf, true)

	// A recursive allOf, whose merged type leads back to an allOf with the same
	// merge, would merge into a fresh copy at every level and never finish: no
	// cache knows the copies. Its re-entry refers to the merge still being
	// generated instead, through a pointer, the way a $ref cycle is broken.
	key := allOfMergeKey(t, rAllOf)
	if merged, ok := g.mergingAllOf[key]; ok {
		return g.allOfCycleType(merged, scope)
	}

	allOfType, err := schemas.AllOf(rAllOf, t)
	if err != nil {
		return nil, fmt.Errorf("could not merge allOf types: %w", err)
	}

	allOfType.AllOf = nil

	// Detect a silent-loss case: when one or more allOf elements carry a
	// composition keyword we don't compile (if/then/else/not), the merge
	// can drop the parent's structural keywords (Type, Properties,
	// AdditionalProperties, ...) leaving an empty result that downstream
	// emits as interface{}. Surface a fidelity warning citing the
	// originally-declared keywords so the user sees what features became
	// no-ops on this type.
	// Detect lossy merge from EITHER the parent OR any branch perspective —
	// a thin allOf wrapper may carry no constraints at the parent level
	// while individual branches do, in which case a parent-only check
	// misses the silent degradation.
	if mergeWasLossy(t, allOfType) || mergeWasLossyInAnyBranch(rAllOf, allOfType) {
		trigger := "allOf merge produced empty result"

		for _, child := range rAllOf {
			if reason, has := hasUnsupportedComposition(child); has {
				trigger = reason + " (in allOf branch)"

				break
			}
		}

		g.warnFallback(t, scope, trigger)
	} else if reason, has := constrainingComposition(t); has {
		// The merged type keeps its shape, but the conditional or the not
		// it drops goes unchecked. A declined conditional discriminator has
		// said why it declined, but not in these terms, so it is reported
		// here as well.
		if _, reported := g.reportedConditionals[t]; !reported && !g.config.OnlyModels {
			g.reportedConditionals[t] = struct{}{}
			g.warnNotEnforced(scope, reason)
		}
	}

	// The merge carries the conditionals of t and its branches, which are
	// accounted for above.
	g.reportedConditionals[allOfType] = struct{}{}

	g.mergingAllOf[key] = allOfType
	defer delete(g.mergingAllOf, key)

	return g.generateTypeInline(allOfType, scope)
}

// allOfMergeKey identifies what an allOf merges into. An allOf of a single
// schema that declares nothing besides annotations merges into a copy of that
// schema wherever it is written, so it is keyed by that schema: recursion
// through two such allOfs of one schema is then caught at the first repeat,
// not after every combination of them. Any other allOf is keyed by itself.
func allOfMergeKey(t *schemas.Type, resolved []*schemas.Type) *schemas.Type {
	if len(t.AllOf) != 1 || len(resolved) != 1 {
		return t
	}

	rest := *t
	rest.AllOf = nil

	if !isAnnotationOnly(&rest) {
		return t
	}

	return resolved[0]
}

// allOfCycleType is the type of a recursive allOf's re-entry: a pointer to the
// type declared for the merge still being generated. A merged object or oneOf
// registers its declaration before generating its body, so the re-entry finds
// it; a merge that declares itself only afterwards is declared here instead,
// and its own re-entry then finds that.
func (g *schemaGenerator) allOfCycleType(merged *schemas.Type, scope nameScope) (codegen.Type, error) {
	decl, ok := g.output.declsBySchema[merged]
	if !ok {
		dt, err := g.generateDeclaredType(merged, scope)
		if err != nil {
			return nil, err
		}

		return codegen.WrapTypeInPointer(dt), nil
	}

	g.warner(fmt.Sprintf("Cycle detected; must wrap type %s in pointer", decl.Name))

	return codegen.WrapTypeInPointer(&codegen.NamedType{Decl: decl}), nil
}

// mergeWasLossyInAnyBranch is mergeWasLossy applied to each child branch in
// turn. Used so a thin allOf wrapper (parent-level constraints empty,
// child branches carrying them) doesn't silently slip through the parent-
// only check.
func mergeWasLossyInAnyBranch(children []*schemas.Type, merged *schemas.Type) bool {
	for _, child := range children {
		if mergeWasLossy(child, merged) {
			return true
		}
	}

	return false
}

// mergeWasLossy reports whether the merged result has dropped structural
// content that the parent (pre-merge) carried. Used to detect the silent
// degradation of object schemas with allOf branches the generator can't
// compile end-to-end.
func mergeWasLossy(parent, merged *schemas.Type) bool {
	if len(parent.Type) > 0 && len(merged.Type) == 0 {
		return true
	}

	if len(parent.Properties) > 0 && len(merged.Properties) == 0 {
		return true
	}

	if parent.AdditionalProperties != nil && merged.AdditionalProperties == nil {
		return true
	}

	if len(parent.Required) > 0 && len(merged.Required) == 0 {
		return true
	}

	return false
}

func (g *schemaGenerator) defaultPropertyValue(prop *schemas.Type) any {
	if prop.AdditionalProperties != nil {
		if len(prop.AdditionalProperties.Type) == 0 {
			return prop.Default
		}

		if len(prop.AdditionalProperties.Type) != 1 {
			g.warner("Additional property has multiple types; will be represented as an empty interface with no validation")

			return prop.Default
		}

		switch prop.AdditionalProperties.Type[0] {
		case schemas.TypeNameString:
			return map[string]string{}

		case schemas.TypeNameArray:
			return map[string][]any{}

		case schemas.TypeNameNumber:
			return map[string]float64{}

		case schemas.TypeNameInteger:
			return map[string]int{}

		case schemas.TypeNameBoolean:
			return map[string]bool{}

		default:
			return prop.Default
		}
	}

	return prop.Default
}

// needsDeclaredType reports whether a schema must be routed through
// generateDeclaredType rather than inlined.
//
// Every composition form that emits a named wrapper or holder needs a full
// declaration: inlining one would collapse it to interface{} and drop the
// generated methods along with it. Keeping the whole decision here means
// generateTypeInline spends a single branch on the family rather than one per
// form, and later forms extend this predicate instead of adding another branch
// to an already-large function.
func (g *schemaGenerator) needsDeclaredType(t *schemas.Type) bool {
	// A primitive `oneOf` carries no top-level `type`.
	if len(t.OneOf) > 0 && isPrimitiveOneOf(t) {
		return true
	}

	// The multi-type-union form `{"type": ["string", "number"]}` routes to
	// the same wrapper. It declines the `["X", "null"]` shape, which stays
	// with the nullable handling further down, and integer/constrained
	// unions, which fall through to the warn-and-interface{} path.
	if isPrimitiveMultiTypeUnion(t) {
		return true
	}

	// A discriminated object `oneOf`, or one handled by the try-each
	// fallback, emits a holder plus per-variant types; both likewise carry
	// no top-level `type`. Not under OnlyModels: generateDeclaredType builds
	// neither there, and routing the property through it would only turn the
	// inline interface{} into a named one.
	if len(t.OneOf) > 1 && !g.config.OnlyModels {
		if d := g.detectDiscriminator(t.OneOf); d.ok && !g.variantRedefinesParent(t, d.prop) {
			return true
		}

		return g.useTryEach(t)
	}

	return false
}

// generateInlineComposition generates an inline anyOf or allOf, and "X or
// null" as a pointer to X's type. handled reports whether t was one of them.
func (g *schemaGenerator) generateInlineComposition(t *schemas.Type, scope nameScope) (codegen.Type, bool, error) {
	if branch, ok := g.nullableSingleBranch(t); ok {
		dt, err := g.generateNullableBranch(branch, scope)

		return dt, true, err
	}

	if len(t.AnyOf) > 0 {
		// A presence count compiles to a validator that attaches only on a
		// declared type, so it skips the merge.
		if isPresenceAnyOf(t) {
			dt, err := g.generateDeclaredType(t, scope)

			return dt, true, err
		}

		dt, err := g.generateAnyOfType(t, scope)

		return dt, true, err
	}

	if len(t.AllOf) > 0 {
		// The conditional-discriminator pattern compiles to a validator
		// that attaches only on a declared type, so it skips the merge.
		if _, ok := g.detectConditionalDiscriminator(t); ok {
			dt, err := g.generateDeclaredType(t, scope)

			return dt, true, err
		}

		dt, err := g.generateAllOfType(t, scope)

		return dt, true, err
	}

	return nil, false, nil
}

func (g *schemaGenerator) generateTypeInline(t *schemas.Type, scope nameScope) (codegen.Type, error) {
	typeIndex, typeIsNullable := isTypeNullable(t)

	if t.Enum == nil && t.Ref == "" {
		if ext := t.GoJSONSchemaExtension; ext != nil {
			for _, pkg := range ext.Imports {
				g.output.file.Package.AddImport(pkg, "")
			}

			if ext.Type != nil {
				return &codegen.CustomNameType{Type: *ext.Type, Nillable: ext.Nillable}, nil
			}
		}

		// An object, the only schema the conditional discriminator takes, is
		// declared below.
		g.warnUncompiledConditional(t, scope, true)

		if dt, handled, err := g.generateInlineComposition(t, scope); handled {
			return dt, err
		}

		// Composition forms that emit a named wrapper or holder must be
		// routed to generateDeclaredType before the `len(t.Type) == 0`
		// bail-out below, since they carry no top-level `type` and would
		// otherwise be emitted as interface{}. Collected in one predicate so
		// this function keeps a single branch for the whole family.
		if g.needsDeclaredType(t) {
			return g.generateDeclaredType(t, scope)
		}

		if len(t.Type) == 0 {
			g.warnCompositionFallback(t, scope)

			return codegen.EmptyInterfaceType{}, nil
		}

		// A nullable primitive becomes a plain pointer to that primitive
		// rather than a named alias of one.
		//
		// The alias form (`type FooBar *float64`, used as `FooBar`) is a named
		// POINTER type, and Go forbids those as method receivers entirely —
		// `invalid receiver type FooBar (pointer or interface type)`. So the
		// name can never carry UnmarshalJSON, a String(), or validation: it is
		// pure indirection, and taking its address yields a **float64.
		//
		// Upstream already made exactly this change for the temporal formats
		// in #570 and #607; the reasoning was never specific to time.Time, so
		// this generalises it to the rest of the primitives.
		if typeIndex != -1 &&
			typeIsNullable &&
			!t.IsSubSchemaTypeElem() &&
			schemas.IsPrimitiveType(t.Type[typeIndex]) {
			return g.primitiveType(t, t.Type[typeIndex], typeIsNullable)
		}

		if len(t.Type) == 2 && typeIsNullable {
			dt, err := g.generateDeclaredType(t, scope)
			if err != nil {
				return nil, err
			}

			return codegen.WrapTypeInPointer(dt), nil
		}

		if len(t.Type) > 1 && !typeIsNullable {
			g.warner(fmt.Sprintf("Property %v has multiple types; will be represented as interface{} with no validation", scope))

			return emptyInterfaceTypeVal, nil
		}

		if typeIndex != -1 && schemas.IsPrimitiveType(t.Type[typeIndex]) {
			if t.IsSubSchemaTypeElem() {
				return nil, nil //nolint: nilnil // TODO: this should be fixed, but it requires a rework.
			}

			return g.primitiveType(t, t.Type[typeIndex], typeIsNullable)
		}

		if typeIndex != -1 && t.Type[typeIndex] == schemas.TypeNameArray {
			var theType codegen.Type = emptyInterfaceTypeVal

			if items := g.itemsSchema(t, scope); items != nil {
				var err error

				theType, err = g.generateTypeInline(items, g.singularScope(scope))
				if err != nil {
					return nil, err
				}
			}

			return &codegen.ArrayType{Type: theType}, nil
		}

		if typeIndex == -1 {
			return emptyInterfaceTypeVal, nil
		}
	}

	dt, err := g.generateDeclaredType(t, scope)
	if err != nil {
		return nil, err
	}

	if typeIsNullable || enumHoldsNullAsNil(t) {
		return codegen.WrapTypeInPointer(dt), nil
	}

	return dt, nil
}

// singularScope attempts to create a name scope for an element of a collection. If the parent collection
// has a plural name like "Actions", then the singular name for the element will be "Action". If the collection
// is not plural, like "WhateverElse", then the element's name will be "WhateverElseElem".
func (g *Generator) singularScope(scope nameScope) nameScope {
	last, ok := scope.last()
	if g.minimalNames && ok && strings.HasSuffix(last, "s") {
		return scope.add(strings.TrimSuffix(last, "s"))
	}

	return scope.add("Elem")
}

// enumValueType is the JSON type of t's enum values when there is one besides
// null: t's single type, the primitive one other than null in a type list such
// as ["string", "null"], or without a type list, the one type of every value
// that is not null. A null value is then left to the pointer that holds the
// enum. ok is false when the values have several types.
func enumValueType(t *schemas.Type) (string, bool) {
	switch {
	case len(t.Type) == 1:
		return t.Type[0], true

	case len(t.Type) == 2 && slices.Contains(t.Type, schemas.TypeNameNull):
		for _, name := range t.Type {
			if name != schemas.TypeNameNull {
				return name, schemas.IsPrimitiveType(name)
			}
		}

	case len(t.Type) == 0 && enumListsNull(t):
		var name string

		for _, v := range t.Enum {
			var valueType string

			switch v.(type) {
			case nil:
				continue

			case string:
				valueType = schemas.TypeNameString

			case float64:
				valueType = schemas.TypeNameNumber

			case bool:
				valueType = schemas.TypeNameBoolean

			default:
				return "", false
			}

			if name != "" && name != valueType {
				return "", false
			}

			name = valueType
		}

		return name, name != ""
	}

	return "", false
}

// enumHoldsNullAsNil reports whether t is an enum listing null beside values
// of one other type. generateEnumType gives it that type, so it holds null as
// a nil pointer, as a type list admitting null does.
func enumHoldsNullAsNil(t *schemas.Type) bool {
	if t.Enum == nil || !enumListsNull(t) {
		return false
	}

	name, ok := enumValueType(t)

	return ok && name != schemas.TypeNameNull
}

// enumListsNull reports whether null is one of t's enum values.
func enumListsNull(t *schemas.Type) bool {
	return slices.Contains(t.Enum, any(nil))
}

func (g *schemaGenerator) generateEnumType(
	t *schemas.Type, scope nameScope,
) (codegen.Type, error) {
	if len(t.Enum) == 0 {
		return nil, errEnumArrCannotBeEmpty
	}

	var wrapInStruct bool

	var enumType codegen.Type

	if typeName, ok := enumValueType(t); ok {
		var err error
		if enumType, err = codegen.PrimitiveTypeFromJSONSchemaType(
			typeName,
			g.typeFormat(t, typeName),
			false,
			g.config.MinSizedInts,
			&t.Minimum,
			&t.Maximum,
			&t.ExclusiveMinimum,
			&t.ExclusiveMaximum,
		); err != nil {
			return nil, fmt.Errorf("invalid type %q: %w", typeName, err)
		}

		// Enforce integer type for enum values. A null value is left as it is:
		// the field holding the enum is a pointer, so it decodes to nil.
		if typeName == "integer" {
			for i, v := range t.Enum {
				switch v := v.(type) {
				case float64:
					t.Enum[i] = int(v)

				case nil:

				default:
					return nil, fmt.Errorf("%w %v", errEnumNonPrimitiveVal, v)
				}
			}
		}

		wrapInStruct = typeName == schemas.TypeNameNull // Null uses interface{}, which cannot have methods.
	} else {
		if len(t.Type) > 1 {
			// TODO: Support multiple types.
			g.warner("Enum defined with multiple types; ignoring it and using enum values instead")
		}

		var primitiveType string

		for _, v := range t.Enum {
			var valueType string

			if v == nil {
				valueType = interfaceTypeName
			} else {
				switch v.(type) {
				case string:
					valueType = "string"

				case float64:
					valueType = float64Type

				case bool:
					valueType = "bool"

				default:
					return nil, fmt.Errorf("%w %v", errEnumNonPrimitiveVal, v)
				}
			}

			if primitiveType == "" {
				primitiveType = valueType
			} else if primitiveType != valueType {
				primitiveType = interfaceTypeName

				break
			}
		}

		if primitiveType == interfaceTypeName {
			wrapInStruct = true
		}

		enumType = codegen.PrimitiveType{Type: primitiveType}
	}

	if wrapInStruct {
		g.warner("Enum field wrapped in struct in order to store values of multiple types")

		enumType = &codegen.StructType{
			Fields: []codegen.StructField{
				{
					Name: "Value",
					Type: enumType,
				},
			},
		}
	}

	enumName, enumNameCollided := g.output.uniqueTypeName(scope)
	g.output.warnNameCollision(enumNameCollided, scope, enumName)

	enumDecl := codegen.TypeDecl{
		Name:       enumName,
		Comment:    withDeprecation("", t),
		Type:       enumType,
		SchemaType: t,
	}
	g.output.file.Package.AddDecl(&enumDecl)

	g.output.declsByName[enumDecl.Name] = &enumDecl
	g.output.declsBySchema[t] = &enumDecl

	if !g.config.OnlyModels {
		valueConstant := &codegen.Var{
			Name:  schemas.PrefixEnumValue + enumDecl.Name,
			Value: g.enumValueList(enumType, t.Enum, enumDecl.Name),
		}
		g.output.file.Package.AddDecl(valueConstant)

		g.output.file.Package.AddImport("fmt", "")
		g.output.file.Package.AddImport("reflect", "")

		for _, formatter := range g.formatters {
			formatter.addImport(g.output.file, &enumDecl)

			if wrapInStruct {
				g.output.file.Package.AddDecl(&codegen.Method{
					Impl: formatter.enumMarshal(&enumDecl),
					Name: enumDecl.GetName() + "_enum_" + formatter.getName(),
				})
			}

			g.output.file.Package.AddDecl(&codegen.Method{
				Impl: formatter.enumUnmarshal(g.output, enumDecl, enumType, valueConstant, wrapInStruct),
				Name: enumDecl.GetName() + "_enum_unmarshal_" + formatter.getName(),
			})
		}

		g.output.ownUnmarshalers(&enumDecl)
	}

	// TODO: May be aliased string type.
	if prim, ok := enumType.(codegen.PrimitiveType); ok && prim.Type == "string" {
		varnames := g.enumVarnames(t, enumDecl.Name)

		for i, v := range t.Enum {
			if s, ok := v.(string); ok {
				g.output.file.Package.AddDecl(&codegen.Constant{
					Name:  g.uniqueEnumConstantName(varnames, enumDecl.Name, i, s),
					Type:  &codegen.NamedType{Decl: &enumDecl},
					Value: s,
				})
			}
		}
	}

	return &codegen.NamedType{Decl: &enumDecl}, nil
}

func (g *schemaGenerator) resolveRefs(types []*schemas.Type, withNulls bool) ([]*schemas.Type, bool) {
	resolvedTypes := make([]*schemas.Type, 0, len(types))
	hasNulls := false

	for _, typ := range types {
		resolvedType, err := g.resolveRef(typ)
		if err != nil {
			g.warner(fmt.Sprintf("Could not resolve ref %q: %v", typ.Ref, err))

			continue
		}

		if !withNulls && len(resolvedType.Type) == 1 && resolvedType.Type[0] == schemas.TypeNameNull {
			hasNulls = true

			continue
		}

		resolvedTypes = append(resolvedTypes, resolvedType)
	}

	return resolvedTypes, hasNulls
}

func (g *schemaGenerator) resolveRef(t *schemas.Type) (*schemas.Type, error) {
	if t.Ref == "" {
		return t, nil
	}

	if _, ok := g.schemaTypesByRef[t.Ref]; ok {
		return g.schemaTypesByRef[t.Ref], nil
	}

	typ, err := g.generateReferencedType(t)
	if err != nil {
		return nil, err
	}

	ntyp, err := g.extractPointedType(typ)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCannotResolveRef, err)
	}

	// After resolving the ref type we lose info about the original schema
	// so rewrite all nested refs to include the original schema id
	_, fileName, err := g.extractRefNames(t)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errCannotResolveRef, err)
	}

	if fileName != "" {
		err = ntyp.Decl.SchemaType.ConvertAllRefs(fileName)
		if err != nil {
			return nil, fmt.Errorf("convert refs: %w", err)
		}
	}

	ntyp.Decl.SchemaType.Dereferenced = true

	g.schemaTypesByRef[t.Ref] = ntyp.Decl.SchemaType

	return ntyp.Decl.SchemaType, nil
}

func (g *schemaGenerator) extractPointedType(typ codegen.Type) (*codegen.NamedType, error) {
	if rtyp, ok := typ.(*codegen.PointerType); ok {
		if ntyp, ok := rtyp.Type.(*codegen.NamedType); ok {
			return ntyp, nil
		}
	}

	if ntyp, ok := typ.(*codegen.NamedType); ok {
		return ntyp, nil
	}

	return nil, fmt.Errorf("%w: got %T", errExpectedNamedType, typ)
}

func (g *schemaGenerator) detectCycle(t *schemas.Type) (bool, func(), error) {
	defName, filename, err := g.extractRefNames(t)
	if err != nil {
		return false, func() {}, err
	}

	if defName == "" && filename == "" && !t.Dereferenced {
		return false, func() {}, nil
	}

	qual := qualifiedDefinition{
		schema:     g.schema,
		schemaType: t,
		filename:   filename,
		name:       defName,
	}

	_, isCycle := g.inScope[qual]
	if !isCycle {
		g.inScope[qual] = struct{}{}
	}

	return isCycle, func() {
		delete(g.inScope, qual)
	}, nil
}

// isTypeNullable checks if a type is nullable and returns the index of the type array where to find the actual type,
// or -1 if none was found.
func isTypeNullable(t *schemas.Type) (int, bool) {
	if len(t.Type) == 1 && (t.Type[0] == schemas.TypeNameArray || t.Type[0] == schemas.TypeNameNull) {
		return 0, true
	}

	if len(t.Type) == 1 && t.Type[0] != schemas.TypeNameNull {
		return 0, false
	}

	if len(t.Type) == 2 && t.Type[0] == schemas.TypeNameNull {
		return 1, true
	}

	if len(t.Type) == 2 && t.Type[1] == schemas.TypeNameNull {
		return 0, true
	}

	if slices.Contains(t.Type, schemas.TypeNameNull) {
		return -1, true
	}

	return -1, false
}

func isTypeTemporal(typ, format string) bool {
	return typ == schemas.TypeNameString && (format == "date-time" || format == "date" || format == "time")
}

// enumValueList is what an enum's allowed-values variable is emitted with. The
// enum's validator decodes into the underlying type and compares each element
// with reflect.DeepEqual, so the elements have to carry that type. An untyped
// list serves `int` — an untyped integer constant in an []interface{} is an int
// — but for a sized integer, `int32` from `format: int32` or a width
// --min-sized-ints picked, no element ever matched and every value was
// rejected, valid ones included. litter keeps a slice's element type but not an
// interface element's, so a sized integer enum gets a typed slice.
//
// A member the width cannot hold is left out: decoding never produces it, so
// it could not be accepted anyway, and keeping it would mean keeping the
// untyped list, which rejects every member. The schema is contradicting itself
// there, so it is reported.
func (g *schemaGenerator) enumValueList(enumType codegen.Type, values []any, declName string) any {
	prim, ok := enumType.(codegen.PrimitiveType)
	if !ok || prim.Type == typeInt {
		return values
	}

	elem, ok := sizedIntegerType(prim.Type)
	if !ok {
		return values
	}

	list := reflect.MakeSlice(reflect.SliceOf(elem), 0, len(values))

	for _, v := range values {
		n, ok := v.(int)
		if !ok {
			return values
		}

		ev := reflect.New(elem).Elem()

		switch {
		case ev.CanInt() && !ev.OverflowInt(int64(n)):
			ev.SetInt(int64(n))

		case ev.CanUint() && n >= 0 && !ev.OverflowUint(uint64(n)):
			ev.SetUint(uint64(n))

		default:
			g.warner(fmt.Sprintf(
				"Enum %s: value %d does not fit %s, so it can never be decoded; leaving it out of the allowed values",
				declName, n, prim.Type,
			))

			continue
		}

		list = reflect.Append(list, ev)
	}

	return list.Interface()
}

// sizedIntegerType maps a sized Go integer type name to its reflect.Type.
func sizedIntegerType(name string) (reflect.Type, bool) {
	switch name {
	case "int8":
		return reflect.TypeFor[int8](), true
	case "int16":
		return reflect.TypeFor[int16](), true
	case "int32":
		return reflect.TypeFor[int32](), true
	case "int64":
		return reflect.TypeFor[int64](), true
	case "uint":
		return reflect.TypeFor[uint](), true
	case "uint8":
		return reflect.TypeFor[uint8](), true
	case "uint16":
		return reflect.TypeFor[uint16](), true
	case "uint32":
		return reflect.TypeFor[uint32](), true
	case "uint64":
		return reflect.TypeFor[uint64](), true
	default:
		return nil, false
	}
}
