package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testNullTypesCrossFile "github.com/plheide/go-jsonschema/tests/data/nullTypesCrossFile"
)

// TestNullTypesCrossFile covers --validate-null-types on properties whose $ref
// points into another file: a null is refused where the type that file
// declares excludes it, also through a $ref that file holds, and kept where it
// admits null, as a root with no type and an anyOf with a null branch does.
func TestNullTypesCrossFile(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"name": "a", "alias": "b", "names": ["c"], "whole": {"x": "d"}}`,
		`{"maybe": null}`,
		`{"nullableWhole": null}`,
	} {
		var fromJSON, fromYAML testNullTypesCrossFile.CrossFile

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"name": null}`, "field name in CrossFile: must not be null"},
		{`{"alias": null}`, "field alias in CrossFile: must not be null"},
		{`{"names": ["c", null]}`, "field names[1] in CrossFile: must not be null"},
		{`{"whole": null}`, "field whole in CrossFile: must not be null"},
	} {
		var fromJSON, fromYAML testNullTypesCrossFile.CrossFile

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}
