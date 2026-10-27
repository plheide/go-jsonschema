package tests_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	testCondDiscElseNot "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/elseNotRequired"
	testCondDiscNot "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/notRequired"
	testCondDiscOptional "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/optionalDiscriminator"
	testCondDiscOwnAndAllOf "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/ownAndAllOf"
	testCondDiscOwn "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/ownConditional"
	testCondDiscOwnOtherKey "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/ownOtherKey"
	testCondDiscRefEnum "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/refEnum"
	testCondDiscRefEnumChain "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/refEnumChain"
	testCondDiscVacuousIf "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/vacuousIf"
)

// TestConditionalDiscriminatorOptionalAndNot covers two extensions of the
// conditional discriminator: an optional discriminator that every `if`
// requires, so that its absence makes each `if` false and applies each
// `else`; and a `not` of `required` in a `then` or `else`, which forbids
// properties. Each input is decoded as JSON and as YAML.
func TestConditionalDiscriminatorOptionalAndNot(t *testing.T) {
	t.Parallel()

	t.Run("an absent optional discriminator applies the else", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscOptional.OptionalDiscriminator](t, []decodeCase{
			// A null passes, as it passes the required checks.
			{`null`, ""},
			{`{"unit": "m"}`, ""},
			{`{}`, "field unit in OptionalDiscriminator (when kind is absent): required"},
			{`{"kind": "float", "unit": "m"}`, ""},
			{`{"kind": "float"}`, "field unit in OptionalDiscriminator (when kind!='enum'): required"},
			{`{"kind": "enum", "values": ["a"]}`, ""},
			{`{"kind": "enum"}`, "field values in OptionalDiscriminator (when kind='enum'): required"},
		})
	})

	// JSON Schema reads an `if` whose discriminator is absent as true, unless
	// the `if` requires it: `properties` holds vacuously for an absent key.
	t.Run("an absent optional discriminator applies the then of an if that does not require it", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscVacuousIf.VacuousIf](t, []decodeCase{
			{`null`, ""},
			{`{}`, "field values in VacuousIf (when kind is absent): required"},
			{`{"values": []}`, "field label in VacuousIf (when kind is absent): required"},
			{`{"values": [], "label": "x"}`, ""},
			{`{"kind": "float", "unit": "m"}`, ""},
			{`{"kind": "float"}`, "field unit in VacuousIf (when kind='float'): required"},
			{`{"kind": "enum", "values": ["a"], "label": "x"}`, ""},
			{`{"kind": "enum", "values": ["a"]}`, "field label in VacuousIf (when kind!='float'): required"},
			{`{"kind": "enum", "label": "x"}`, "field values in VacuousIf (when kind='enum'): required"},
		})
	})

	t.Run("a not of required forbids one property, two together, or any of several", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscNot.NotRequired](t, []decodeCase{
			{`{"kind": "fixed", "value": 1}`, ""},
			{
				`{"kind": "fixed", "value": 1, "ref": "x"}`,
				"field ref in NotRequired (when kind='fixed'): must not be present",
			},
			{`{"kind": "live", "ref": "x", "value": 1}`, ""},
			{
				`{"kind": "stream", "ref": "x", "value": 1, "rate": 2}`,
				"fields value, rate in NotRequired (when kind='stream'): must not all be present",
			},
			{`{"kind": "off"}`, ""},
			{`{"kind": "off", "rate": 2}`, ""},
			{`{"kind": "off", "ref": "x"}`, "field ref in NotRequired (when kind='off'): must not be present"},
		})
	})

	t.Run("an else may forbid what the then requires", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscElseNot.ElseNotRequired](t, []decodeCase{
			{`{}`, ""},
			{`{"transform": {}}`, "field transform in ElseNotRequired (when space is absent): must not be present"},
			{`{"space": "physical"}`, ""},
			{
				`{"space": "physical", "transform": {}}`,
				"field transform in ElseNotRequired (when space!='normalized'): must not be present",
			},
			{`{"space": "normalized", "transform": {}}`, ""},
			{`{"space": "normalized"}`, "field transform in ElseNotRequired (when space='normalized'): required"},
		})
	})
}

// TestConditionalDiscriminatorOwn covers an if/then/else on the object itself
// rather than under allOf, which JSON Schema reads as one more allOf element.
func TestConditionalDiscriminatorOwn(t *testing.T) {
	t.Parallel()

	t.Run("at the root, in a definition and in an inline object", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscOwn.OwnConditional](t, []decodeCase{
			{`{}`, ""},
			{`{"state": "done"}`, ""},
			{`{"state": "skipped", "skip": "x"}`, ""},
			{`{"state": "skipped"}`, "field skip in OwnConditional (when state='skipped'): required"},
			{`{"last": {"state": "skipped"}}`, "field skip in OwnConditionalLast (when state='skipped'): required"},
		})

		checkDecode[testCondDiscOwn.Event](t, []decodeCase{
			{`{"kind": "start"}`, ""},
			{`{"kind": "stop", "reason": "x"}`, ""},
			{`{"kind": "stop"}`, "field reason in Event (when kind='stop'): required"},
			{`{"kind": "start", "reason": "x"}`, "field reason in Event (when kind!='stop'): must not be present"},
		})
	})

	t.Run("beside an allOf on the same discriminator", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscOwnAndAllOf.OwnAndAllOf](t, []decodeCase{
			{`{"kind": "a", "x": "1"}`, ""},
			{`{"kind": "a"}`, "field x in OwnAndAllOf (when kind='a'): required"},
			{`{"kind": "b"}`, "field y in OwnAndAllOf (when kind='b'): required"},
			{`{"kind": "c"}`, "field z in OwnAndAllOf (when kind='c'): required"},
		})
	})

	t.Run("on another property than the allOf's, it is left to its warning", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscOwnOtherKey.OwnOtherKey](t, []decodeCase{
			{`{"kind": "a", "mode": "fast"}`, "field x in OwnOtherKey (when kind='a'): required"},
			// The own conditional on mode is not compiled.
			{`{"kind": "b", "mode": "slow"}`, ""},
		})
	})

	t.Run("on a discriminator declared through a $ref", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscRefEnum.RefEnum](t, []decodeCase{
			{`{"state": "done"}`, ""},
			{`{"state": "skipped"}`, "field reason in RefEnum (when state='skipped'): required"},
			{
				`{"state": "done", "event": {"state": "skipped"}}`,
				"field skip in Event (when state='skipped'): required",
			},
		})

		// However long the chain of $refs is.
		checkDecode[testCondDiscRefEnumChain.RefEnumChain](t, []decodeCase{
			{`{"state": "done"}`, ""},
			{`{"state": "skipped"}`, "field reason in RefEnumChain (when state='skipped'): required"},
		})
	})

	t.Run("is reported only when it is not compiled", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			fixture string
			want    []string
		}{
			{"ownConditional", nil},
			{"ownAndAllOf", nil},
			{"refEnum", nil},
			{"refEnumChain", nil},
			{"ownOtherKey", []string{
				"schema fidelity: type OwnOtherKey does not enforce all it declares: if/then/else not compiled",
			}},
			// The not beside the compiled if/then is reported, once.
			{"ownWithNot", []string{
				`schema fidelity: type OwnWithNot does not enforce all it declares: "not" keyword not compiled`,
			}},
		} {
			warnings := generateWithWarnerCapture(t,
				"./data/conditionalDiscriminator/"+tc.fixture+"/"+tc.fixture+".json")

			assert.ElementsMatch(t, tc.want, warnings, "%s:\n%s", tc.fixture, strings.Join(warnings, "\n"))
		}
	})
}
