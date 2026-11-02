package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testPrimitiveComposition "github.com/plheide/go-jsonschema/tests/data/validation/primitiveComposition"
)

// TestPrimitiveComposition covers a primitive type whose anyOf or oneOf
// branches check its values: they keep the type, and a value must match at
// least one branch of an anyOf, and exactly one branch of a oneOf. That holds
// for a definition, for properties, and for the elements of an array.
func TestPrimitiveComposition(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"level": "low"}`,
		`{"level": "high"}`,
		`{"mode": "fast"}`,
		`{"mode": "off"}`,
		`{"mode": "say \"hi\"\\"}`,
		`{"code": "abc"}`,
		`{"code": "A1"}`,
		`{"code": "ab"}`,
		`{"id": "abcd"}`,
		`{"id": "xyz"}`,
		`{"port": 80}`,
		`{"port": 8080}`,
		`{"step": 4}`,
		`{"step": 9}`,
		`{"count": 1}`,
		`{"count": 3}`,
		`{"big": 5000000000}`,
		`{"big": 1}`,
		`{"ratio": 0}`,
		`{"ratio": -0}`,
		`{"ratio": 0.5}`,
		`{"sign": 2}`,
		`{"tags": ["xa", "y"]}`,
	} {
		var fromJSON, fromYAML testPrimitiveComposition.PrimitiveComposition

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"level": "mid"}`, `field Level: must be one of ["low","high"]`},
		{`{"mode": "medium"}`, `field mode: must be one of ["fast","slow","off","say \"hi\"\\"]`},
		{`{"code": "ABC"}`, "field code: must match at least one anyOf branch"},
		{`{"id": "abc"}`, "field id: must match exactly one oneOf branch, matched 2"},
		{`{"id": "bcde"}`, "field id: must match exactly one oneOf branch, matched none"},
		{`{"port": 5000}`, "field port: must match at least one anyOf branch"},
		{`{"step": 6}`, "field step: must match exactly one oneOf branch, matched 2"},
		{`{"step": 5}`, "field step: must match exactly one oneOf branch, matched none"},
		{`{"count": 2}`, "field count: must be one of [1,3,4]"},
		{`{"big": 2}`, "field big: must be one of [5000000000,1]"},
		{`{"ratio": 2}`, "field ratio: must be one of [0,0.5,1]"},
		// 0 and -0 are the same number, which matches two branches.
		{`{"sign": 0}`, "field sign: must be one of [2]"},
		{`{"sign": -0}`, "field sign: must be one of [2]"},
		{`{"tags": ["y", "z"]}`, "field tags[1]: must match at least one anyOf branch"},
	} {
		var fromJSON, fromYAML testPrimitiveComposition.PrimitiveComposition

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}
