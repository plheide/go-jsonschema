package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	testNarrowedRef "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/narrowedRef"
	testAnyOfRefs "github.com/plheide/go-jsonschema/tests/data/enumUnion/anyOfRefs"
	testNullable "github.com/plheide/go-jsonschema/tests/data/enumUnion/nullable"
	testOneOfConsts "github.com/plheide/go-jsonschema/tests/data/enumUnion/oneOfConsts"
	testOneOfOverlap "github.com/plheide/go-jsonschema/tests/data/enumUnion/oneOfOverlap"
	testEnumUnionCrossFile "github.com/plheide/go-jsonschema/tests/data/enumUnionCrossFile"
)

// enumInvalid begins the error an enum gives for a value it does not list.
const enumInvalid = "invalid value (expected one of"

// TestEnumUnionDecode decodes into the enums that anyOfs and oneOfs of string
// branches are generated as: each string the composition admits is accepted,
// and any other is rejected, from JSON and from YAML.
func TestEnumUnionDecode(t *testing.T) {
	t.Parallel()

	t.Run("an anyOf of $refs, inline, as items, declared and typed", func(t *testing.T) {
		t.Parallel()

		checkDecode[testAnyOfRefs.AnyOfRefs](t, []decodeCase{
			{`{"level": "warning"}`, ""},
			{`{"level": "debug"}`, ""},
			{`{"level": "fatal"}`, enumInvalid},
			{`{}`, "field level in AnyOfRefs: required"},
			{`{"level": "info", "levels": ["error", "info"]}`, ""},
			{`{"level": "info", "levels": ["error", "verbose"]}`, enumInvalid},
			{`{"level": "info", "severity": "error"}`, ""},
			{`{"level": "info", "severity": "loud"}`, enumInvalid},
			{`{"level": "info", "typed": "trace"}`, ""},
			{`{"level": "info", "typed": "info"}`, enumInvalid},
		})
	})

	t.Run("a oneOf of documented consts and enums", func(t *testing.T) {
		t.Parallel()

		checkDecode[testOneOfConsts.OneOfConsts](t, []decodeCase{
			{`{"mode": "fast"}`, ""},
			{`{"mode": "dry-run"}`, ""},
			{`{"mode": "slow"}`, enumInvalid},
			{`{}`, ""},
		})
	})

	t.Run("a string two branches list", func(t *testing.T) {
		t.Parallel()

		checkDecode[testOneOfOverlap.OneOfOverlap](t, []decodeCase{
			{`{"exclusive": "left", "inclusive": "left"}`, ""},
			{`{"exclusive": "right"}`, ""},
			{`{"exclusive": "shared"}`, enumInvalid},
			{`{"inclusive": "shared"}`, ""},
		})
	})

	t.Run("a branch that admits only null", func(t *testing.T) {
		t.Parallel()

		checkDecode[testNullable.Nullable](t, []decodeCase{
			{`{"anyLevel": "warning", "oneLevel": "info", "typedLevel": "debug"}`, ""},
			{`{"anyLevel": null, "oneLevel": null, "typedLevel": "info"}`, ""},
			{`{"anyLevel": "loud", "oneLevel": null, "typedLevel": "info"}`, enumInvalid},
			{`{"anyLevel": null, "oneLevel": "quiet", "typedLevel": "info"}`, enumInvalid},
			{`{"oneLevel": null, "typedLevel": "info"}`, "field anyLevel in Nullable: required"},
			{`{"anyLevel": null, "oneLevel": null, "twoNulls": "error", "typedLevel": "info"}`, ""},
		})

		// The type of string beside the composition rejects null. Only JSON
		// says so: yaml.v3 decodes a null into the zero value without asking
		// the type.
		var v testNullable.Nullable

		require.ErrorContains(t, json.Unmarshal(
			[]byte(`{"anyLevel": null, "oneLevel": null, "typedLevel": null}`), &v,
		), enumInvalid)
	})

	t.Run("branches that $ref another file", func(t *testing.T) {
		t.Parallel()

		checkDecode[testEnumUnionCrossFile.CrossFile](t, []decodeCase{
			{`{"level": "debug"}`, ""},
			{`{"level": "verbose"}`, enumInvalid},
		})
	})
}

// TestNarrowedByRef checks a then that narrows a property by a $ref to an enum,
// or by an enum union, as one that narrows it with an inline enum is checked.
func TestNarrowedByRef(t *testing.T) {
	t.Parallel()

	checkDecode[testNarrowedRef.NarrowedRef](t, []decodeCase{
		{`{"kind": "compare", "operator": "GT"}`, ""},
		{
			`{"kind": "compare", "operator": "AND"}`,
			"field operator in NarrowedRef (when kind='compare'): must be one of [EQ, GT]",
		},
		{`{"kind": "combine", "operator": "OR"}`, ""},
		{
			`{"kind": "combine", "operator": "EQ"}`,
			"field operator in NarrowedRef (when kind='combine'): must be one of [AND, OR]",
		},
		{`{"kind": "either", "operator": "EQ"}`, ""},
		{`{"kind": "either", "operator": "OR"}`, ""},
		{
			`{"kind": "either", "operator": "XOR"}`,
			"field operator in NarrowedRef (when kind='either'): must be one of [EQ, GT, AND, OR]",
		},
		{`{"kind": "compare"}`, ""},
	})
}
