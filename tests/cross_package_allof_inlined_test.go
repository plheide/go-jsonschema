package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testCrossPackageAllOfInlined "github.com/plheide/go-jsonschema/tests/data/crossPackageAllOfInlined/schema"
)

// TestCrossPackageAllOfInlinedDecodes covers an allOf whose member is a $ref
// into a package of its own: the merge copies the member's fields and checks,
// and the file compiles, since the member's package, which nothing refers to,
// is not imported. So does an anyOf with such a branch.
func TestCrossPackageAllOfInlinedDecodes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"header": {"traceId": "t1"}, "orderId": "o1", "quantity": 2}`,
		`{"header": {"traceId": "t1"}, "orderId": "o1", "quantity": 1, "amendment": {"note": "late"}}`,
		`{"header": {"traceId": "t1"}, "orderId": "o1", "quantity": 1, "amendment": {"orderId": "o2", "quantity": 3}}`,
	} {
		var fromJSON, fromYAML testCrossPackageAllOfInlined.Schema

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"header": {"traceId": "t1"}, "orderId": "o1", "quantity": 0}`, "field quantity: must be >= 1"},
		{`{"header": {"traceId": "t1"}, "quantity": 1}`, "field orderId in Schema: required"},
		{`{"orderId": "o1", "quantity": 1}`, "field header in Schema: required"},
		{`{"header": {"traceId": "t1"}, "orderId": "o1", "quantity": 1, "amendment": {}}`, "all validators failed"},
	} {
		var fromJSON, fromYAML testCrossPackageAllOfInlined.Schema

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}
