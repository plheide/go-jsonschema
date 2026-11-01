package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testCounts "github.com/plheide/go-jsonschema/tests/data/validation/counts"
)

// TestCounts covers count keywords written with a fraction or an exponent,
// which JSON Schema counts as integers, and maximums of 0, which are caps.
func TestCounts(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"pair": [1, 2], "word": "a", "empty": "", "none": [], "rows": [[], []]}`,
		`{"word": "abcde"}`,
	} {
		var fromJSON, fromYAML testCounts.Counts

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"pair": [1]}`, "field pair length: must be >= 2"},
		{`{"pair": [1, 2, 3]}`, "field pair length: must be <= 2"},
		{`{"word": ""}`, "field word length: must be >= 1"},
		{`{"word": "abcdef"}`, "field word length: must be <= 5"},
		{`{"empty": "a"}`, "field empty length: must be <= 0"},
		{`{"none": [1]}`, "field none length: must be <= 0"},
		{`{"rows": [[], [1]]}`, "field rows[1] length: must be <= 0"},
	} {
		var fromJSON, fromYAML testCounts.Counts

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}
