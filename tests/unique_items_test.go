package tests_test

import (
	"testing"

	testUniqueItems "github.com/plheide/go-jsonschema/tests/data/uniqueItems/items"
)

// TestUniqueItems covers uniqueItems, which compares an array's items as JSON.
func TestUniqueItems(t *testing.T) {
	t.Parallel()

	checkDecode[testUniqueItems.Items](t, []decodeCase{
		{`{}`, ""},
		{`{"tags": ["a", "b"], "numbers": [1, 2], "anything": [1, "1", [1], {"a": 1}, null]}`, ""},
		{`{"tags": ["a", "b", "a"]}`, "field tags: items 0 and 2 are equal; they must be unique"},

		// 1 and 1.0 are the same number, as are 1e2 and 100. Numbers are
		// compared exactly, so integers a float64 cannot tell apart differ.
		{`{"numbers": [1, 1.0]}`, "field numbers: items 0 and 1 are equal; they must be unique"},
		{`{"numbers": [1e2, 100]}`, "field numbers: items 0 and 1 are equal"},
		{`{"numbers": [0.5, 5e-1]}`, "field numbers: items 0 and 1 are equal"},
		{`{"numbers": [-0.0, 0]}`, "field numbers: items 0 and 1 are equal"},
		{`{"numbers": [9007199254740992, 9007199254740993]}`, ""},
		{`{"anything": [{"a": 9007199254740993}, {"a": 9007199254740992}]}`, ""},

		// Objects are compared as JSON, keys in any order, including the
		// properties the Go type has no field for.
		{`{"points": [{"x": 1, "y": 1}, {"x": 1, "y": 2}]}`, ""},
		{`{"points": [{"x": 1, "y": [1, 2]}, {"y": [1, 2], "x": 1.0}]}`, "field points: items 0 and 1 are equal"},
		{`{"anything": [{"a": 1}, {"a": 1}]}`, "field anything: items 0 and 1 are equal"},
		{`{"anything": [null, null]}`, "field anything: items 0 and 1 are equal"},

		// A nested array declares its own; the outer one declares none.
		{`{"grid": [[1, 2], [1, 2]]}`, ""},
		{`{"grid": [[1, 2], [3, 3]]}`, "field grid[1]: items 0 and 1 are equal"},

		// encoding/json assigns TAGS to the tags field; yaml.v3 leaves it an
		// additional property, whose items must be unique as well.
		{`{"TAGS": ["a", "a"]}`, "items 0 and 1 are equal"},
		{`{"repeats": ["a", "a"]}`, ""},
		{`{"set": ["a", "a"]}`, "field Set: items 0 and 1 are equal"},
		{`{"extra": ["a", "b"]}`, ""},
		{`{"extra": ["a", "a"]}`, `field ["extra"]: items 0 and 1 are equal`},
	})
}
