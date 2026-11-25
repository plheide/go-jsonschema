package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testUnevaluatedDraft07 "github.com/plheide/go-jsonschema/tests/data/strictAdditionalProperties/unevaluatedDraft07"
	testUnevaluatedFalse "github.com/plheide/go-jsonschema/tests/data/strictAdditionalProperties/unevaluatedFalse"
	testUnevaluatedPresence "github.com/plheide/go-jsonschema/tests/data/strictAdditionalProperties/unevaluatedPresence"
)

// TestUnevaluatedPropertiesBesidePresence covers unevaluatedProperties of false
// beside an anyOf or oneOf whose branches only list required, a not, and an
// if/then that only lists required, under respect-schema: none evaluates a
// property, so a property that the object and its allOf branches don't
// declare is rejected, and the keywords themselves are checked as before. An
// if that declares a property evaluates it when it matches, so beside one the
// keyword is not checked.
func TestUnevaluatedPropertiesBesidePresence(t *testing.T) {
	t.Parallel()

	const notAllowed = "additional properties not allowed"

	checkDecode[testUnevaluatedPresence.UnevaluatedPresence](t, []decodeCase{
		{`{"target": {"name": "n", "component": "c"}}`, ""},
		{`{"target": {"name": "n", "node": "x", "extra": 1}}`, `fields ["extra"] in UnevaluatedPresenceTarget: ` + notAllowed},
		{`{"target": {"name": "n"}}`, "UnevaluatedPresenceTarget: at least one of [component], [node] must be present"},
		{`{"contact": {"email": "e"}}`, ""},
		{`{"contact": {"email": "e", "fax": "f"}}`, `fields ["fax"] in UnevaluatedPresenceContact: ` + notAllowed},
		{`{"window": {"count": 1}}`, ""},
		{`{"window": {"count": 1, "until": "x"}}`, "fields count, until in UnevaluatedPresenceWindow: must not all be present"},
		{`{"window": {"zz": 1}}`, `fields ["zz"] in UnevaluatedPresenceWindow: ` + notAllowed},
		{`{"paired": {"a": "x", "b": "y"}}`, ""},
		{`{"paired": {"c": 1}}`, `fields ["c"] in UnevaluatedPresencePaired: ` + notAllowed},
		{`{"keyed": {"kind": "a", "x": 1, "zz": 2}}`, ""},
	})
}

// TestUnevaluatedPropertiesFalse covers unevaluatedProperties of false under
// StrictAdditionalProperties respect-schema. Where the evaluated properties are
// an object's own and those of its allOf branches, inline or through a $ref, at
// every depth, a property that none of them declares is rejected. Where an
// anyOf, or a branch's additionalProperties, also beside a $ref, evaluates
// properties, nothing is checked.
func TestUnevaluatedPropertiesFalse(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"orderId": "o1"}`,
		`{"orderId": "o1", "header": "h", "version": 2}`,
		`{"orderId": "o1", "plain": {"a": "x"}, "empty": {}}`,
		`{"orderId": "o1", "dynamic": {"kind": "k", "x": 1, "zz": true}}`,
		`{"orderId": "o1", "open": {"x": 1, "zz": 2}}`,
		`{"orderId": "o1", "refSibling": {"version": 1, "zz": 2}}`,
	} {
		var fromJSON, fromYAML testUnevaluatedFalse.UnevaluatedFalse

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"orderId": "o1", "extra": 1}`, `fields ["extra"] in UnevaluatedFalse: additional properties not allowed`},
		{
			`{"orderId": "o1", "plain": {"a": "x", "b": 1}}`,
			`fields ["b"] in UnevaluatedFalsePlain: additional properties not allowed`,
		},
		{`{"orderId": "o1", "empty": {"x": 1}}`, `fields ["x"] in UnevaluatedFalseEmpty: additional properties not allowed`},
	} {
		var fromJSON, fromYAML testUnevaluatedFalse.UnevaluatedFalse

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}

// TestUnevaluatedPropertiesDraft07 covers unevaluatedProperties of false in a
// schema that declares draft-07, which doesn't have the keyword: as a draft-07
// validator does, the generated code accepts a property that nothing declares.
func TestUnevaluatedPropertiesDraft07(t *testing.T) {
	t.Parallel()

	const input = `{"a": "x", "b": "y", "extra": 1}`

	var fromJSON, fromYAML testUnevaluatedDraft07.UnevaluatedDraft07

	require.NoError(t, json.Unmarshal([]byte(input), &fromJSON))
	require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML))
}
