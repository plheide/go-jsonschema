package generator

import (
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// The keywords a presence count checks.
const (
	presenceOneOf = "oneOf"
	presenceAnyOf = "anyOf"
)

// presenceCountValidator checks a oneOf or anyOf whose branches list only
// required. JSON Schema reads such a composition as a count: a branch matches
// an object that has every property the branch lists, so a oneOf passes when
// exactly one branch matches, and an anyOf when at least one does. Each branch
// counts on its own, overlapping or not: under
// `oneOf: [{required: [a]}, {required: [a, b]}]`, an object with a and b
// matches both branches, and is rejected.
//
// It reads the raw map before the typed decode, as the conditional
// discriminator does, since the struct cannot tell an absent property from a
// zero one.
type presenceCountValidator struct {
	declName string
	keyword  string
	branches [][]string
}

func (v *presenceCountValidator) desc() *validatorDesc {
	return &validatorDesc{
		hasError:            true,
		beforeJSONUnmarshal: true,
	}
}

// generate counts the branches whose properties are all present, then
// compares the count with what the keyword allows.
func (v *presenceCountValidator) generate(out *codegen.Emitter, _ string) error {
	counter := v.keyword + "Matches"

	out.Printlnf("%s := 0", counter)

	for _, names := range v.branches {
		for _, name := range names {
			out.Printlnf("if _, ok := %s[%q]; ok {", varNameRawMap, name)
			out.Indent(1)
		}

		out.Printlnf("%s++", counter)

		for range names {
			out.Indent(-1)
			out.Printlnf("}")
		}
	}

	sets := make([]string, len(v.branches))

	for i, names := range v.branches {
		escaped := make([]string, len(names))
		for j, name := range names {
			escaped[j] = goStringText(name)
		}

		sets[i] = "[" + strings.Join(escaped, ", ") + "]"
	}

	// A null decodes to a nil raw map, which the required checks let
	// through, and so does the count.
	if v.keyword == presenceOneOf {
		out.Printlnf("if %s != nil && %s != 1 {", varNameRawMap, counter)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("%s: exactly one of %s must be present, got %%d", %s)`,
			v.declName, strings.Join(sets, ", "), counter)
	} else {
		out.Printlnf("if %s != nil && %s == 0 {", varNameRawMap, counter)
		out.Indent(1)
		out.Printlnf(`return fmt.Errorf("%s: at least one of %s must be present")`,
			v.declName, strings.Join(sets, ", "))
	}

	out.Indent(-1)
	out.Printlnf("}")

	return nil
}

// presenceBranches returns the properties each of branches requires, when
// branches are t's oneOf or anyOf, every one of them lists only required,
// besides annotations, and t is an object with properties of its own, whose
// struct the check attaches to. Otherwise it returns false, and the
// composition is handled as before.
func presenceBranches(t *schemas.Type, branches []*schemas.Type) ([][]string, bool) {
	if len(branches) == 0 || len(t.Properties) == 0 ||
		len(t.Type) != 1 || t.Type[0] != schemas.TypeNameObject {
		return nil, false
	}

	sets := make([][]string, 0, len(branches))

	for _, branch := range branches {
		if branch == nil || len(branch.Required) == 0 || !onlyRequired(branch) {
			return nil, false
		}

		sets = append(sets, branch.Required)
	}

	return sets, true
}

// isPresenceAnyOf reports whether t's anyOf is a presence count, which the
// struct for t checks, so that the anyOf has no type of its own to merge.
func isPresenceAnyOf(t *schemas.Type) bool {
	_, ok := presenceBranches(t, t.AnyOf)

	return ok
}

// presenceCountValidators returns the checks for t's oneOf and anyOf that are
// presence counts.
func presenceCountValidators(t *schemas.Type, declName string) []validator {
	var validators []validator

	if sets, ok := presenceBranches(t, t.OneOf); ok {
		validators = append(validators, &presenceCountValidator{declName, presenceOneOf, sets})
	}

	if sets, ok := presenceBranches(t, t.AnyOf); ok {
		validators = append(validators, &presenceCountValidator{declName, presenceAnyOf, sets})
	}

	return validators
}
