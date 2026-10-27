package generator

import (
	"fmt"
	"slices"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// anyOfParent is the schema that a struct merged from an anyOf is declared on.
// Its own checks bind every instance, whichever branches match, and no
// branch's type makes them.
type anyOfParent struct {
	schema *schemas.Type
	// shared maps each of the parent's properties that a branch declares as
	// well to the keywords the parent declares on it. Merging the branches
	// writes into such a property's schema, so those keywords are no longer
	// the parent's alone, and the struct leaves them unchecked.
	shared map[string][]string
}

// sharedAnyOfProperties finds the properties of parent that a branch declares
// as well, with the keywords parent declares on each. It must run before the
// merge, which writes the branches' keywords into those schemas.
func (g *schemaGenerator) sharedAnyOfProperties(parent *schemas.Type, branches []*schemas.Type) map[string][]string {
	shared := map[string][]string{}

	for name, prop := range parent.Properties {
		if prop == nil {
			continue
		}

		for _, b := range branches {
			if _, ok := b.Properties[name]; ok {
				shared[name] = g.fieldKeywords(prop)

				break
			}
		}
	}

	return shared
}

// fieldKeywords lists, in alphabetical order, the keywords of property schema
// s that generated code checks or applies on the field, and those of the
// values it holds, such as items.minLength, which the element walk checks.
func (g *schemaGenerator) fieldKeywords(s *schemas.Type) []string {
	keywords := g.valueKeywords(s, "", map[*schemas.Type]bool{})
	slices.Sort(keywords)

	return keywords
}

// valueKeywords lists the keywords of s that generated code checks on a value,
// each prefixed with path, and those of the values it holds. visited guards
// against a schema that holds itself.
func (g *schemaGenerator) valueKeywords(s *schemas.Type, path string, visited map[*schemas.Type]bool) []string {
	if s == nil || visited[s] {
		return nil
	}

	visited[s] = true

	var keywords []string

	add := func(present bool, keyword string) {
		if present {
			keywords = append(keywords, path+keyword)
		}
	}

	// A default and readOnly bind the property itself, not a value it holds.
	add(s.Default != nil && path == "", "default")
	add(s.ReadOnly && !g.config.DisableReadOnlyValidation && path == "", "readOnly")

	add(s.Const != nil, "const")
	add(s.Enum != nil, "enum")
	add(s.ExclusiveMaximum != nil, "exclusiveMaximum")
	add(s.ExclusiveMinimum != nil, "exclusiveMinimum")
	add(s.Format != "" && isKnownFormatKeyword(s.Format) && g.config.FormatValidation.shouldValidate(s.Format),
		"format")
	add(s.MaxItems != 0, "maxItems")
	add(s.MaxLength != 0, "maxLength")
	add(s.Maximum != nil, "maximum")
	add(s.MinItems != 0, "minItems")
	add(s.MinLength != 0, "minLength")
	add(s.Minimum != nil, "minimum")
	add(s.MultipleOf != nil, "multipleOf")
	add(s.Pattern != "", "pattern")
	add(s.UniqueItems, "uniqueItems")

	keywords = append(keywords, g.valueKeywords(elementSchema(s), path+"items.", visited)...)

	// An object with properties is a struct type of its own, which checks
	// its additional properties itself.
	if len(s.Properties) == 0 {
		keywords = append(keywords, g.valueKeywords(s.AdditionalProperties, path+"additionalProperties.", visited)...)
	}

	return keywords
}

// anyOfParentValidators returns the checks that a struct merged from an anyOf
// owes the schema the anyOf is declared on: the properties required by the
// parent, or by every branch, and the keywords of the parent's own
// properties. A property that only branches declare is theirs to check,
// through the anyOf validator. The keywords of one that both declare are
// reported as unchecked.
func (g *schemaGenerator) anyOfParentValidators(
	t *schemas.Type,
	tt *codegen.StructType,
	declName string,
	scope nameScope,
) []validator {
	parent, ok := g.output.anyOfParents[t]
	if !ok {
		return nil
	}

	g.warnSharedAnyOfProperties(parent, scope)

	own := func(jsonName string) bool {
		_, declared := parent.schema.Properties[jsonName]
		_, shared := parent.shared[jsonName]

		return declared && !shared
	}

	validators := g.nullTypeValidators(parent.schema, tt, declName, true)

	for _, f := range tt.RequiredJSONFields {
		validators = append(validators, &requiredValidator{f, declName})
	}

	nullChecks := g.nullTypeValidators(parent.schema, tt, declName, false)
	nullChecks = append(nullChecks, g.enumNullValidators(tt, declName)...)

	for _, v := range nullChecks {
		switch nv := v.(type) {
		case *nonNullValidator:
			if own(nv.jsonName) {
				validators = append(validators, v)
			}

		case *nonNullElementsValidator:
			if own(nv.jsonName) {
				validators = append(validators, v)
			}
		}
	}

	for _, f := range tt.Fields {
		if own(f.JSONName) {
			validators = g.propertyValidators(validators, f, declName)
		}
	}

	return validators
}

// warnSharedAnyOfProperties reports the keywords of parent's properties that
// a branch declares as well, which the merged struct leaves unchecked.
func (g *schemaGenerator) warnSharedAnyOfProperties(parent anyOfParent, scope nameScope) {
	var unchecked []string

	for _, name := range sortedKeys(parent.shared) {
		if keywords := parent.shared[name]; len(keywords) > 0 {
			unchecked = append(unchecked, fmt.Sprintf("%s (%s)", name, strings.Join(keywords, ", ")))
		}
	}

	if len(unchecked) > 0 {
		g.warnNotEnforced(scope,
			"the keywords of properties an anyOf branch declares as well: "+strings.Join(unchecked, "; "))
	}
}
