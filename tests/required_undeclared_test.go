package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testRequiredFields "github.com/plheide/go-jsonschema/tests/data/validation/requiredFields"
)

// TestRequiredUndeclared covers required names that no property declares, so
// that no field holds them: in a struct, in a struct merged from an allOf, and
// in the branches of an anyOf. Their presence is checked all the same.
func TestRequiredUndeclared(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"a": "x", "b": null}`,
		`{"a": "x", "b": 1, "merged": {"y": true}}`,
		`{"a": "x", "b": 1, "either": {"kind": "one", "one": 1}}`,
		`{"a": "x", "b": 1, "either": {"kind": "two", "two": [2]}}`,
	} {
		var fromJSON, fromYAML testRequiredFields.RequiredUndeclared

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"a": "x"}`, "field b in RequiredUndeclared: required"},
		{`{"a": "x", "b": 1, "merged": {"x": "1"}}`, "field y in RequiredUndeclaredMerged: required"},
		{`{"a": "x", "b": 1, "either": {"kind": "one"}}`, "field one in RequiredUndeclaredEither_0: required"},
		{`{"a": "x", "b": 1, "either": {"kind": "two", "one": 1}}`, "field two in RequiredUndeclaredEither_1: required"},
	} {
		var fromJSON, fromYAML testRequiredFields.RequiredUndeclared

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}
