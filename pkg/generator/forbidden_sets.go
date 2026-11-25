package generator

import (
	"fmt"
	"slices"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// forbiddenSetsValidator checks the property sets that an object's own `not`,
// and the `not`s of its allOf, forbid: `not: {required: [a, b]}` forbids
// having both a and b, and `not: {anyOf: [{required: [a]}, {required: [b]}]}`
// having either. Each set is checked on its own, since every one of the nots
// holds.
//
// It reads the raw map before the typed decode, as the presence counts do,
// since the struct cannot tell an absent property from a zero one.
type forbiddenSetsValidator struct {
	declName string
	sets     [][]string
}

func (v *forbiddenSetsValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

// generate writes a check for each set. A null decodes to a nil raw map,
// which holds no property, so it passes, as it passes the required checks.
func (v *forbiddenSetsValidator) generate(out *codegen.Emitter, _ string) error {
	for _, set := range v.sets {
		emitForbiddenCheck(out, v.declName, set, "", "")
	}

	return nil
}

// allOfChecks is what the struct built from an object alone checks of the
// object's allOf, which then is not merged: the conditional discriminator,
// the property sets the allOf's nots forbid, and what is left unchecked.
type allOfChecks struct {
	conditional *conditionalDiscriminatorValidator
	forbidden   [][]string
	gaps        []string
}

// structChecks returns what the struct built from t alone checks of t's allOf
// and its own if/then/else, when that struct stands for t: when t's allOf
// compiles into checks, as allOfAsChecks decides, or t has none and the
// conditional discriminator compiles its own if/then/else. ok is false
// otherwise, and t goes the general way.
func (g *schemaGenerator) structChecks(t *schemas.Type) (allOfChecks, bool) {
	if len(t.AllOf) > 0 {
		return g.allOfAsChecks(t)
	}

	cd, ok := g.detectConditionalDiscriminator(t)
	if !ok {
		return allOfChecks{}, false
	}

	return allOfChecks{conditional: cd, gaps: cd.gaps}, true
}

// allOfAsChecks decides whether t's allOf compiles into checks on the struct
// built from t alone, instead of being merged: each of its elements is an
// if/then/else or a not, which add no fields, and the conditional
// discriminator compiles, a not forbids property sets, or a conditional keyed
// on presence compiles. A not that forbidden sets do not express, and the
// value-keyed conditionals when the discriminator compiles none, are left
// unchecked, and named in the gaps; what the presence-keyed ones leave is
// reported with the presence validator's gaps.
func (g *schemaGenerator) allOfAsChecks(t *schemas.Type) (allOfChecks, bool) {
	if len(t.AllOf) == 0 || !isObjectWithProperties(t) {
		return allOfChecks{}, false
	}

	var (
		checks     allOfChecks
		conditions []int
	)

	for i, elem := range t.AllOf {
		switch {
		case onlyNot(elem):
			if sets, ok := forbiddenSets(elem.Not); ok && len(sets) > 0 {
				checks.forbidden = append(checks.forbidden, sets...)
			} else {
				checks.gaps = append(checks.gaps, fmt.Sprintf(`allOf[%d]'s "not" not compiled`, i))
			}

		case isPresenceElement(elem):
			// The presence validator checks it; see allOfPresenceClauses.

		case elem.If != nil || elem.Then != nil || elem.Else != nil:
			conditions = append(conditions, i)

		default:
			return allOfChecks{}, false
		}
	}

	// Detection declines an element that declares keywords besides its
	// if/then/else, and says why, which the merge t then goes to does not.
	cd, ok := g.detectConditionalDiscriminator(t)
	if ok {
		checks.conditional = cd
		checks.gaps = append(append([]string(nil), cd.gaps...), checks.gaps...)

		return checks, true
	}

	presence := slices.ContainsFunc(g.allOfPresenceClauses(t), presenceClause.compiles)

	if len(checks.forbidden) == 0 && !presence || slices.ContainsFunc(conditions, func(i int) bool {
		return !onlyConditional(t.AllOf[i])
	}) {
		return allOfChecks{}, false
	}

	// The struct stands for t without the conditionals, which then are
	// reported here, as the merge would report them.
	conditionGaps := make([]string, 0, len(conditions))

	for _, i := range conditions {
		if ownConditional(t.AllOf[i]) == conditionalIfThenElse {
			conditionGaps = append(conditionGaps, fmt.Sprintf("allOf[%d]'s if/then/else not compiled", i))
		}
	}

	checks.gaps = append(conditionGaps, checks.gaps...)

	return checks, true
}

// forbiddenSetsOf returns the property sets that the struct declared for t
// checks are absent together: those of t's own not and of its allOf's nots.
func (g *schemaGenerator) forbiddenSetsOf(t *schemas.Type) [][]string {
	sets, _ := g.ownForbiddenSets(t)

	if len(t.AllOf) > 0 {
		if checks, ok := g.allOfAsChecks(t); ok {
			sets = append(sets, checks.forbidden...)
		}
	}

	return sets
}

// ownForbiddenSets returns the property sets that t's own not forbids, when
// the struct declared for t stands for t, so that it can check them.
func (g *schemaGenerator) ownForbiddenSets(t *schemas.Type) ([][]string, bool) {
	if t.Not == nil || !g.structStandsFor(t) {
		return nil, false
	}

	sets, ok := forbiddenSets(t.Not)

	return sets, ok && len(sets) > 0
}

// structStandsFor reports whether the struct declared for t stands for t, so
// that it can check what t's own keywords say of its raw map: t is an object
// with properties, neither a merge, whose keywords may come from a branch, nor
// a branch itself, and a composition of its own leaves its struct to it: its
// allOf compiles into checks, its anyOf or oneOf is a presence count.
func (g *schemaGenerator) structStandsFor(t *schemas.Type) bool {
	if !isObjectWithProperties(t) || t.GetSubSchemaType() != "" || t.IsSubSchemaTypeElem() {
		return false
	}

	if _, ok := presenceBranches(t, t.AnyOf); len(t.AnyOf) > 0 && !ok {
		return false
	}

	if _, ok := presenceBranches(t, t.OneOf); len(t.OneOf) > 0 && !ok {
		return false
	}

	_, ok := g.allOfAsChecks(t)

	return len(t.AllOf) == 0 || ok
}

// onlyNot reports whether an allOf element declares nothing besides a not and
// annotations.
func onlyNot(elem *schemas.Type) bool {
	if elem == nil || elem.Not == nil {
		return false
	}

	rest := *elem
	rest.Not = nil

	return !constrains(&rest)
}

// isObjectWithProperties reports whether t is an object with properties of its
// own, whose struct checks what this file compiles.
func isObjectWithProperties(t *schemas.Type) bool {
	return len(t.Type) == 1 && t.Type[0] == schemas.TypeNameObject && len(t.Properties) > 0
}
