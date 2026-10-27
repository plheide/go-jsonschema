package tests_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	testAnyOfParent "github.com/plheide/go-jsonschema/tests/data/anyOfParent/parent"
)

// TestAnyOfParent covers a struct merged from an anyOf, which checks the
// keywords of the object the anyOf is declared on as well as the branches.
func TestAnyOfParent(t *testing.T) {
	t.Parallel()

	t.Run("checks", func(t *testing.T) {
		t.Parallel()

		checkDecode[testAnyOfParent.Parent](t, []decodeCase{
			{`{"kind": "x", "a": "abc", "both": true}`, ""},
			{`{"kind": "x", "b": "y", "both": true}`, ""},
			// A branch's own keywords bind only where that branch matches.
			{`{"kind": "x", "b": "y", "both": true, "a": "z"}`, ""},
			{`{"kind": "x", "b": "y", "both": true, "s": "abcd"}`, ""},

			{`{"a": "abc", "both": true}`, "field kind in Parent: required"},
			{`{"kind": "y", "a": "abc", "both": true}`, "field kind: must be equal to x"},
			{`{"kind": "x", "a": "abc", "both": true, "n": 0}`, "field n: must be >= 1"},
			{`{"kind": "x", "a": "abc", "both": true, "tags": ["t", "t"]}`, "field tags: items 0 and 1 are equal"},
			{`{"kind": "x", "a": "abc", "both": true, "tags": [""]}`, "field tags[0] length: must be >= 1"},
			// Every branch requires both, so the object does.
			{`{"kind": "x", "a": "abc"}`, "field both in Parent: required"},
			{`{"kind": "x", "both": true}`, "all validators failed"},
		})
	})

	// Merging the branches writes the first one's maxLength into s, so s's
	// own minLength is no longer the parent's alone, and is reported, as are
	// the keywords of the items of codes.
	t.Run("shared property", func(t *testing.T) {
		t.Parallel()

		joined := strings.Join(generateWithWarnerCapture(t, "./data/anyOfParent/parent/parent.json"), "\n")
		assert.Contains(t, joined, "schema fidelity: type Parent does not enforce all it declares: "+
			"the keywords of properties an anyOf branch declares as well: codes (items.minLength); s (minLength)")
	})
}
