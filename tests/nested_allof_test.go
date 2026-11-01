package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testAllOf "github.com/plheide/go-jsonschema/tests/data/core/allOf"
)

// TestNestedAllOf covers an allOf written inline inside an allOf, whose
// branches merge as the outer allOf's do.
func TestNestedAllOf(t *testing.T) {
	t.Parallel()

	var fromJSON, fromYAML testAllOf.AllOfNested

	require.NoError(t, json.Unmarshal([]byte(`{"deep": {"e": "x"}}`), &fromJSON))
	require.NotNil(t, fromJSON.Deep)
	require.Equal(t, "x", fromJSON.Deep.E)
	require.NoError(t, yamlv3.Unmarshal([]byte(`{"deep": {"e": "x"}}`), &fromYAML))
	require.Equal(t, fromJSON, fromYAML)

	require.ErrorContains(t, json.Unmarshal([]byte(`{"deep": {}}`), &fromJSON), "field e in AllOfNestedDeep: required")
	require.ErrorContains(t, yamlv3.Unmarshal([]byte(`{"deep": {}}`), &fromYAML), "field e in AllOfNestedDeep: required")
}
