package generator

import (
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// An enum union is an anyOf or a oneOf whose branches only list strings, with
// const or enum, inline or through a $ref, such as
//
//	{"anyOf": [{"$ref": "#/$defs/Severity"}, {"$ref": "#/$defs/Channel"}]}
//
// or the documented-enum pattern without a type,
//
//	{"oneOf": [{"const": "a", "description": "..."}, {"const": "b", "description": "..."}]}
//
// It admits the strings that a branch of the anyOf lists, or that just one
// branch of the oneOf lists, so it is generated as the enum of those strings.
// A branch that admits only null adds null, which the field holds as a nil
// pointer. Before, such an anyOf was merged into interface{} with no check, and
// such a oneOf was not compiled.
//
// A schema of type string whose branches are all inline is a primitive
// composition, which keeps the string type and checks its branches on the
// value, as before; see isPrimitiveComposition.

// asEnumUnion returns the enum that t is generated as, when t is an enum union,
// and t otherwise. The enum is made once for t, so every place that generates t
// declares the same type.
func (g *schemaGenerator) asEnumUnion(t *schemas.Type) *schemas.Type {
	if t == nil || len(t.AnyOf)+len(t.OneOf) == 0 {
		return t
	}

	union, decided := g.enumUnions[t]
	if !decided {
		union = g.enumUnion(t)
		g.enumUnions[t] = union
	}

	if union == nil {
		return t
	}

	return union
}

// enumUnion returns the enum of the values that t admits, when t is an enum
// union: an anyOf or a oneOf, not both, of two branches or more, on a schema
// that declares besides it no more than `type: string` and annotations, with
// at least two branches that list strings and the others admitting only null.
// It returns nil for any other schema.
func (g *schemaGenerator) enumUnion(t *schemas.Type) *schemas.Type {
	branches, isOneOf := t.AnyOf, false
	if len(t.OneOf) > 0 {
		branches, isOneOf = t.OneOf, true
	}

	if (len(t.AnyOf) > 0 && len(t.OneOf) > 0) || len(branches) < 2 || isPrimitiveComposition(t) {
		return nil
	}

	rest := t.WithoutMarks()
	rest.AnyOf, rest.OneOf = nil, nil

	if len(rest.Type) == 1 && rest.Type[0] == schemas.TypeNameString {
		rest.Type = nil
	}

	if constrains(rest) {
		return nil
	}

	var (
		lists [][]string
		nulls int
	)

	for _, branch := range branches {
		values, null, ok := g.unionBranch(branch)
		if !ok {
			return nil
		}

		if null {
			nulls++

			continue
		}

		lists = append(lists, values)
	}

	if len(lists) < 2 {
		return nil
	}

	values := unionValues(lists, isOneOf)
	if len(values) == 0 {
		return nil
	}

	union := &schemas.Type{
		Title:         t.Title,
		Description:   t.Description,
		Deprecated:    t.Deprecated,
		Default:       t.Default,
		XEnumVarnames: t.XEnumVarnames,
	}

	for _, v := range values {
		union.Enum = append(union.Enum, v)
	}

	// A null passes an anyOf with a branch that admits only null, and a oneOf
	// with one such branch: the others list strings. With null listed, the
	// enum takes its type from the strings, as `"enum": ["a", null]` does.
	if nulls > 0 && (!isOneOf || nulls == 1) {
		union.Enum = append(union.Enum, nil)
	} else {
		union.Type = schemas.TypeList{schemas.TypeNameString}
	}

	return union
}

// unionBranch reads a branch of an enum union, following its $refs without
// generating anything: it returns the strings the branch lists, or null when
// the branch admits only null. ok is false for any other branch.
func (g *schemaGenerator) unionBranch(branch *schemas.Type) ([]string, bool, bool) {
	branch, ok := g.followRefsQuietly(branch)
	if !ok {
		return nil, false, false
	}

	if admitsOnlyNull(branch) {
		return nil, true, !constrains(withoutNullOnly(branch))
	}

	values, ok := listedStrings(branch)

	return values, false, ok
}

// withoutNullOnly returns a copy of t, a schema that admits only null, without
// the keyword that says so: `type: null`, or else an enum or const of null.
func withoutNullOnly(t *schemas.Type) *schemas.Type {
	rest := *t

	if len(rest.Type) > 0 {
		rest.Type = nil
	} else {
		rest.Enum, rest.Const, rest.ConstExact, rest.ConstIsSet = nil, nil, nil, false
	}

	return &rest
}

// unionValues returns the strings an anyOf admits whose branches list the
// strings in lists, in the order they first appear: every string of every
// branch. For a oneOf, it returns those that only one branch lists, since a
// string that two branches list matches both, which the oneOf rejects.
func unionValues(lists [][]string, isOneOf bool) []string {
	var (
		order    []string
		branches = map[string]int{}
	)

	for _, list := range lists {
		seen := map[string]struct{}{}

		for _, v := range list {
			if _, again := seen[v]; again {
				continue
			}

			seen[v] = struct{}{}

			if branches[v] == 0 {
				order = append(order, v)
			}

			branches[v]++
		}
	}

	if !isOneOf {
		return order
	}

	values := make([]string, 0, len(order))

	for _, v := range order {
		if branches[v] == 1 {
			values = append(values, v)
		}
	}

	return values
}

// followRefsQuietly follows t's $refs, from the document being generated,
// without generating anything, as evaluatesStatically does: at most
// maxNullCheckRefHops of them, and none with a keyword beside it that may
// constrain a value, which generation would ignore. It returns false where a
// $ref leads nowhere or past those bounds.
func (g *schemaGenerator) followRefsQuietly(t *schemas.Type) (*schemas.Type, bool) {
	t, _, _, ok := g.followRefsQuietlyIn(t)

	return t, ok
}

// followRefsQuietlyIn is followRefsQuietly that also returns the document the
// schema it ends at is in, with that document's file, which that schema's own
// $refs are read in.
func (g *schemaGenerator) followRefsQuietlyIn(t *schemas.Type) (*schemas.Type, *schemas.Schema, string, bool) {
	doc, docFile := g.schema, g.schemaFileName

	for hops := 0; t != nil && t.Ref != ""; hops++ {
		var ok bool

		if hops == maxNullCheckRefHops || hasRefSiblings(t) {
			return nil, nil, "", false
		}

		if t, doc, docFile, ok = g.refTargetIn(t, doc, docFile); !ok {
			return nil, nil, "", false
		}
	}

	return t, doc, docFile, t != nil
}
