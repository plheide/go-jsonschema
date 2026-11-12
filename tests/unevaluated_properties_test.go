package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testUnevaluatedDraft07 "github.com/plheide/go-jsonschema/tests/data/strictAdditionalProperties/unevaluatedDraft07"
	testUnevaluatedFalse "github.com/plheide/go-jsonschema/tests/data/strictAdditionalProperties/unevaluatedFalse"
)

// TestUnevaluatedPropertiesFalse covers unevaluatedProperties of false under
// StrictAdditionalProperties respect-schema. Where the evaluated properties are
// an object's own and those of its allOf branches, inline or through a $ref, at
// every depth, a property that none of them declares is rejected. Where an
// anyOf, or a branch's additionalProperties, evaluates properties, nothing is
// checked.
func TestUnevaluatedPropertiesFalse(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"orderId": "o1"}`,
		`{"orderId": "o1", "header": "h", "version": 2}`,
		`{"orderId": "o1", "plain": {"a": "x"}, "empty": {}}`,
		`{"orderId": "o1", "dynamic": {"kind": "k", "x": 1, "zz": true}}`,
		`{"orderId": "o1", "open": {"x": 1, "zz": 2}}`,
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
