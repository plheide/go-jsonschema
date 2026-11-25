package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testRefAllOfVariant "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/refAllOfVariant"
	testRefClosedAdditional "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/refClosedAdditional"
	testRefClosedOverlap "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/refClosedOverlap"
	testRefClosedParent "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/refClosedParent"
	testRefNoDiscriminator "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/refNoDiscriminator"
	testRefRecursive "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/refRecursive"
	testVariantNot "github.com/plheide/go-jsonschema/tests/data/oneOfDiscriminated/variantNot"
)

// TestOneOfTryEachRefVariants decodes oneOfs of objects that $refs name and no
// const tells apart. Each variant is tried, and an input is accepted only when
// exactly one matches, from JSON and from YAML; the holder's field for that
// variant is the named type.
func TestOneOfTryEachRefVariants(t *testing.T) {
	t.Parallel()

	t.Run("plain objects", func(t *testing.T) {
		t.Parallel()

		checkDecode[testRefNoDiscriminator.Schedule](t, []decodeCase{
			{`{"at": "09:00"}`, ""},
			{`{"every": 5}`, ""},
			{`{}`, "Schedule: no oneOf variant matched"},
			{`{"at": "09:00", "every": 5}`, "Schedule: no oneOf variant matched"},
		})

		var v testRefNoDiscriminator.RefNoDiscriminator

		require.NoError(t, json.Unmarshal([]byte(`{"schedule": {"every": 5}, "inline": {"at": "09:00"}}`), &v))
		require.NotNil(t, v.Schedule.Variant1)
		assert.Equal(t, 5, v.Schedule.Variant1.Every)
		require.NotNil(t, v.Inline.Variant0)
		assert.Equal(t, "09:00", v.Inline.Variant0.At)
	})

	t.Run("a variant that is an allOf", func(t *testing.T) {
		t.Parallel()

		checkDecode[testRefAllOfVariant.Source](t, []decodeCase{
			{`{"value": 1.5}`, ""},
			{`{"name": "flow", "sensor": "s1"}`, ""},
			{`{"name": "flow", "sensor": "s1", "unit": "m3/d"}`, ""},
			{`{"name": "flow"}`, "Source: no oneOf variant matched"},
		})

		var v testRefAllOfVariant.Source

		require.NoError(t, json.Unmarshal([]byte(`{"name": "flow", "sensor": "s1"}`), &v))
		require.NotNil(t, v.Variant1)
		assert.Equal(t, "s1", v.Variant1.Sensor)
	})

	t.Run("a variant whose items refer back to the oneOf", func(t *testing.T) {
		t.Parallel()

		checkDecode[testRefRecursive.Condition](t, []decodeCase{
			{`{"after": 10}`, ""},
			{`{"join": "all", "conditions": [{"after": 10}, {"join": "any", "conditions": [{"above": 2}]}]}`, ""},
			{`{"join": "all", "conditions": [{"below": 2}]}`, "no oneOf variant matched"},
		})

		var v testRefRecursive.Condition

		require.NoError(t, json.Unmarshal(
			[]byte(`{"join": "all", "conditions": [{"after": 10}, {"join": "any", "conditions": [{"above": 2}]}]}`), &v,
		))
		require.NotNil(t, v.Variant2)
		require.Len(t, v.Variant2.Conditions, 2)
		require.NotNil(t, v.Variant2.Conditions[0].Variant0)
		require.NotNil(t, v.Variant2.Conditions[1].Variant2)
		require.NotNil(t, v.Variant2.Conditions[1].Variant2.Conditions[0].Variant1)
	})

	t.Run("variants closed with unevaluatedProperties", func(t *testing.T) {
		t.Parallel()

		checkDecode[testRefClosedOverlap.RefClosedOverlap](t, []decodeCase{
			{`{"message": {"code": "c"}}`, ""},
			{`{"message": {"code": "c", "detail": "d"}}`, ""},
		})

		var v testRefClosedOverlap.RefClosedOverlap

		require.NoError(t, json.Unmarshal([]byte(`{"message": {"code": "c", "detail": "d"}}`), &v))
		require.NotNil(t, v.Message.Variant1)
		assert.Nil(t, v.Message.Variant0)
	})

	t.Run("the parent's properties beside variants closed with unevaluatedProperties", func(t *testing.T) {
		t.Parallel()

		// A variant's unevaluatedProperties sees only what the variant
		// evaluates, so the parent's channel matches neither variant.
		checkDecode[testRefClosedParent.RefClosedParent](t, []decodeCase{
			{`{"message": {"code": "c"}}`, ""},
			{`{"message": {"code": "c", "detail": "d"}}`, ""},
			{`{"message": {"code": "c", "channel": "z"}}`, "RefClosedParentMessage: no oneOf variant matched"},
		})
	})

	t.Run("a variant closed by its own additionalProperties", func(t *testing.T) {
		t.Parallel()

		// A variant's additionalProperties sees only the properties of the
		// schema that declares it, so the parent's channel, and the id of an
		// allOf variant's other element, match no variant.
		checkDecode[testRefClosedAdditional.RefClosedAdditional](t, []decodeCase{
			{`{"message": {"code": "c"}}`, ""},
			{`{"message": {"code": "c", "detail": "d"}}`, ""},
			{`{"message": {"text": "t"}}`, ""},
			{`{"message": {"code": "c", "channel": "z"}}`, "RefClosedAdditionalMessage: no oneOf variant matched"},
			{`{"message": {"code": "c", "detail": "d", "channel": "z"}}`, "RefClosedAdditionalMessage: no oneOf variant matched"},
			{`{"message": {"text": "t", "id": "i"}}`, "RefClosedAdditionalMessage: no oneOf variant matched"},
		})
	})

	t.Run("a variant's not, tried only where its type checks it", func(t *testing.T) {
		t.Parallel()

		checkDecode[testVariantNot.VariantNotChecked](t, []decodeCase{
			{`{"file": "x"}`, ""},
			{`{"note": "n"}`, ""},
			{`{"file": "x", "note": "n"}`, "VariantNotChecked: no oneOf variant matched"},
		})

		// Not tried, the others take every value, the one each variant
		// takes among them.
		checkDecode[testVariantNot.VariantNot](t, []decodeCase{
			{`{"inline": {"file": "x"}, "merged": {"file": "x"}}`, ""},
			{`{"inline": {"note": "n"}, "merged": {"name": "n"}}`, ""},
		})
	})
}
