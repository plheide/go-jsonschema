package generator

import (
	"strings"

	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// keywordUnevaluatedProperties is the keyword checksUnevaluatedProperties
// decides on, as the fidelity warnings name it.
const keywordUnevaluatedProperties = "unevaluatedProperties"

// checksUnevaluatedProperties reports whether the struct generated for s checks
// its `unevaluatedProperties: false` the way it checks `additionalProperties:
// false`: in the same StrictAdditionalProperties modes, by rejecting each key
// that no property of the struct holds.
//
// That is the keyword's meaning only where the properties s evaluates are known
// without the value: those that s and its allOf branches declare, at every
// depth, which are the struct's fields. evaluatesStatically says when they are.
// Elsewhere the check would reject properties that the schema evaluates, so the
// keyword is left unchecked, and reported. A struct merged from an anyOf gets
// no such check at all.
//
// The keyword came with draft 2019-09. A schema that names an earlier draft in
// its `$schema`, or whose document does, doesn't have it, as a validator of that
// draft doesn't, so it is not checked there. One that names no draft is read as
// the generator reads every other keyword.
//
// The decision for an allOf is made once, from the schema the allOf is
// declared on and its branches as written, and holds for the merged type as
// well, either of which can declare the struct.
func (g *schemaGenerator) checksUnevaluatedProperties(s *schemas.Type) bool {
	if closed, decided := g.unevaluatedClosed[s]; decided {
		return closed
	}

	if g.config.StrictAdditionalProperties == StrictAdditionalPropertiesOff || g.config.OnlyModels ||
		!isFalseSchema(s.UnevaluatedProperties) || s.GetSubSchemaType() == schemas.SubSchemaTypeAnyOf ||
		g.declaresDraftBefore2019(s) {
		return false
	}

	return g.evaluatesStatically(s, g.schema, g.schemaFileName, true, map[*schemas.Type]bool{})
}

// declaresDraftBefore2019 reports whether s, or else the document s is in,
// names in its `$schema` a draft from before 2019-09: draft-03 to draft-07,
// whose URIs all begin json-schema.org/draft-0. Later drafts' URIs are
// json-schema.org/draft/<year>-<month>.
func (g *schemaGenerator) declaresDraftBefore2019(s *schemas.Type) bool {
	version := s.Version
	if version == "" && g.schema.ObjectAsType != nil {
		version = g.schema.Version
	}

	return strings.Contains(version, "json-schema.org/draft-0")
}

// evaluatesStatically reports whether the properties that s evaluates are known
// without the value: s, which root marks as the schema that declares
// unevaluatedProperties, and each of its allOf branches, at every depth,
// evaluate only the properties they declare.
//
// A keyword that evaluates properties depending on the value does not: anyOf,
// oneOf, not, if/then/else and dependentSchemas. Nor does one that evaluates
// every property, additionalProperties and patternProperties, or a branch's own
// unevaluatedProperties, which sees only the branch's. A $ref branch is
// followed into the document that holds it, doc of the file docFile. Beside a
// $ref, generation ignores other keywords, which from draft 2019-09 on apply as
// well, and may evaluate properties the struct has no field for, such as
// additionalProperties: true. So a $ref with any keyword beside it but
// annotations, the root's included, leaves the properties unknown.
func (g *schemaGenerator) evaluatesStatically(
	s *schemas.Type,
	doc *schemas.Schema,
	docFile string,
	root bool,
	visited map[*schemas.Type]bool,
) bool {
	if s == nil || (root && s.Ref != "") {
		return false
	}

	for hops := 0; s.Ref != ""; hops++ {
		var ok bool

		if hops == maxNullCheckRefHops || hasRefSiblings(s) {
			return false
		}

		if s, doc, docFile, ok = g.refTargetIn(s, doc, docFile); !ok {
			return false
		}
	}

	if visited[s] {
		return true
	}

	visited[s] = true

	if s.AdditionalProperties != nil || len(s.PatternProperties) > 0 ||
		len(s.AnyOf) > 0 || len(s.OneOf) > 0 || s.Not != nil ||
		s.If != nil || s.Then != nil || s.Else != nil || len(s.DependentSchemas) > 0 ||
		(!root && s.UnevaluatedProperties != nil) {
		return false
	}

	for _, branch := range s.AllOf {
		if !g.evaluatesStatically(branch, doc, docFile, false, visited) {
			return false
		}
	}

	return true
}

// hasRefSiblings reports whether s declares, beside its $ref, a keyword that
// may constrain a value.
func hasRefSiblings(s *schemas.Type) bool {
	rest := *s
	rest.Ref = ""

	return constrains(&rest)
}
