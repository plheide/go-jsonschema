package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testTwoTypes "github.com/plheide/go-jsonschema/tests/data/core/twoTypes"
)

// TestTwoTypes covers a declared type of two types without null, which holds a
// value of either.
func TestTwoTypes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{`1`, `"a"`} {
		var fromJSON, fromYAML testTwoTypes.TwoTypes

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, input := range []string{`[1]`, `{"a": 1}`} {
		var fromJSON, fromYAML testTwoTypes.ListOrMap

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}
}
