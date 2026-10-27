package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	yamlv3 "gopkg.in/yaml.v3"

	testAnyOfRequiredOnly "github.com/plheide/go-jsonschema/tests/data/presenceCount/anyOfRequiredOnly"
	testOneOfRequiredOnly "github.com/plheide/go-jsonschema/tests/data/presenceCount/oneOfRequiredOnly"
	testOverlapping "github.com/plheide/go-jsonschema/tests/data/presenceCount/overlapping"
)

// decodeCase is an input, decoded both as JSON and as YAML, and the error it
// must give: "" when it must be accepted.
type decodeCase struct {
	input   string
	wantErr string
}

// checkDecode decodes each case into a new T, from JSON and from YAML, which
// reads the same text.
func checkDecode[T any](t *testing.T, cases []decodeCase) {
	t.Helper()

	for _, tc := range cases {
		var fromJSON, fromYAML T

		for format, err := range map[string]error{
			"json": json.Unmarshal([]byte(tc.input), &fromJSON),
			"yaml": yamlv3.Unmarshal([]byte(tc.input), &fromYAML),
		} {
			if tc.wantErr == "" {
				assert.NoError(t, err, "%s %s", format, tc.input)
			} else {
				assert.ErrorContains(t, err, tc.wantErr, "%s %s", format, tc.input)
			}
		}
	}
}

// TestPresenceCount covers a oneOf or anyOf whose branches only list required.
// JSON Schema reads it as a count of the branches whose properties are all
// present: exactly one for a oneOf, at least one for an anyOf.
func TestPresenceCount(t *testing.T) {
	t.Parallel()

	t.Run("oneOf: exactly one branch", func(t *testing.T) {
		t.Parallel()

		const each = "OneOfRequiredOnly: exactly one of [value], [ref] must be present"

		checkDecode[testOneOfRequiredOnly.OneOfRequiredOnly](t, []decodeCase{
			// A null passes, as it passes the required checks.
			{`null`, ""},
			{`{"kind": "line", "value": 0}`, ""},
			{`{"kind": "area", "ref": "x"}`, ""},
			{`{"kind": "line"}`, each + ", got 0"},
			{`{"kind": "line", "value": 0, "ref": "x"}`, each + ", got 2"},
		})
	})

	t.Run("anyOf: at least one branch", func(t *testing.T) {
		t.Parallel()

		checkDecode[testAnyOfRequiredOnly.AnyOfRequiredOnly](t, []decodeCase{
			{`null`, ""},
			{`{"email": "a@example.com"}`, ""},
			{`{"phone": "1"}`, ""},
			{`{"email": "a@example.com", "phone": "1"}`, ""},
			{`{"name": "a"}`, "AnyOfRequiredOnly: at least one of [email], [phone] must be present"},
		})
	})

	t.Run("overlapping lists count branch by branch", func(t *testing.T) {
		t.Parallel()

		const each = "Overlapping: exactly one of [a], [a, b], [50%] must be present"

		checkDecode[testOverlapping.Overlapping](t, []decodeCase{
			{`{"a": "x"}`, ""},
			{`{"50%": "x"}`, ""},
			{`{"b": "x"}`, each + ", got 0"},
			// Both [a] and [a, b] are present.
			{`{"a": "x", "b": "y"}`, each + ", got 2"},
			{`{"a": "x", "50%": "y"}`, each + ", got 2"},
		})
	})
}
