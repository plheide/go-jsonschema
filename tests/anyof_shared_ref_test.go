package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testAnyOf "github.com/plheide/go-jsonschema/tests/data/core/anyOf"
	testNullTypes "github.com/plheide/go-jsonschema/tests/data/validateNullTypes"
)

// TestAnyOfSharedRef covers anyOf branches that are $refs to definitions other
// properties use too. The anyOf reads a branch with null, or with its parent's
// type, and the definition stays as it is.
func TestAnyOfSharedRef(t *testing.T) {
	t.Parallel()

	for _, input := range []string{`{"either": null}`, `{"either": {"y": "b"}}`, `{"word": null}`} {
		var fromJSON, fromYAML testNullTypes.NullSharedRef

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	var fromJSON, fromYAML testNullTypes.NullSharedRef

	require.ErrorContains(t, json.Unmarshal([]byte(`{"foo": null}`), &fromJSON), "field foo in NullSharedRef: must not be null")
	require.ErrorContains(t, yamlv3.Unmarshal([]byte(`{"foo": null}`), &fromYAML), "field foo in NullSharedRef: must not be null")

	// A branch with no type decodes as its parent's type.
	var pairJSON, pairYAML testAnyOf.AnyOfTypelessRef

	require.NoError(t, json.Unmarshal([]byte(`{"pair": {"y": 1}}`), &pairJSON))
	require.NoError(t, yamlv3.Unmarshal([]byte(`{"pair": {"y": 1}}`), &pairYAML))
	require.Equal(t, pairJSON, pairYAML)
}
