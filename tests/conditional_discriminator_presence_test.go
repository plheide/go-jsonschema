package tests_test

import (
	"testing"

	testCondDiscElseNot "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/elseNotRequired"
	testCondDiscNot "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/notRequired"
	testCondDiscOptional "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/optionalDiscriminator"
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

		checkPresence[testCondDiscOptional.OptionalDiscriminator](t, []presenceCase{
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

	t.Run("a not of required forbids one property, two together, or any of several", func(t *testing.T) {
		t.Parallel()

		checkPresence[testCondDiscNot.NotRequired](t, []presenceCase{
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

		checkPresence[testCondDiscElseNot.ElseNotRequired](t, []presenceCase{
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
