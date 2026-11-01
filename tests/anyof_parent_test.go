package tests_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	testAnyOfDeclared "github.com/plheide/go-jsonschema/tests/data/anyOfParent/declaredProperty"
	testAnyOfNested "github.com/plheide/go-jsonschema/tests/data/anyOfParent/nested"
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

	// A property that a branch declares as well keeps the parent's keywords
	// in the merge, so the struct checks them, and reports nothing unchecked.
	t.Run("shared property", func(t *testing.T) {
		t.Parallel()

		checkDecode[testAnyOfParent.Parent](t, []decodeCase{
			{`{"kind": "x", "a": "abc", "both": true, "s": "a"}`, "field s length: must be >= 2"},
			{`{"kind": "x", "a": "abc", "both": true, "codes": ["a"]}`, "field codes[0] length: must be >= 2"},
		})

		joined := strings.Join(generateWithWarnerCapture(t, "./data/anyOfParent/parent/parent.json"), "\n")
		assert.NotContains(t, joined, "an anyOf branch declares as well")
	})

	// What one branch requires in a property that the parent declares binds
	// only where that branch matches.
	t.Run("nested", func(t *testing.T) {
		t.Parallel()

		checkDecode[testAnyOfNested.Nested](t, []decodeCase{
			{`{"kind": "x", "b": "y", "meta": {}}`, ""},
			{`{"kind": "x", "a": "y", "meta": {"m": "z"}}`, ""},
			{`{"kind": "x", "b": "y", "s": "a"}`, "field s length: must be >= 2"},
		})
	})

	// A property that the object declares through a definition, and that
	// the branches narrow, has the definition's type and its enum.
	t.Run("declared property", func(t *testing.T) {
		t.Parallel()

		checkDecode[testAnyOfDeclared.DeclaredProperty](t, []decodeCase{
			{`{"thing": {"type": "a"}}`, ""},
			{`{"thing": {"type": "c"}}`, ""},
			{`{"thing": {"type": "d"}}`, "invalid value"},
		})
	})
}
