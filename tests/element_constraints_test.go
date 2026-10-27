package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testElements "github.com/plheide/go-jsonschema/tests/data/elementConstraints/elements"
)

// TestElementConstraints covers the keywords of the values that arrays and maps
// hold, at every depth, each checked against its own schema.
func TestElementConstraints(t *testing.T) {
	t.Parallel()

	checkDecode[testElements.Elements](t, []decodeCase{
		{`{}`, ""},
		{`{"names": ["a", "abc", "äöü"], "codes": ["ab"], "fixed": ["x"], "flags": [true]}`, ""},
		{`{"ratios": [0, 0.5], "halves": [1.5, -2], "counts": [4, 10]}`, ""},
		{`{"anything": [{"a": [1, "b"]}, {"a": [1.0, "b"]}], "tag": "v1"}`, ""},
		{`{"grid": [["a"], ["b", "c"]], "optional": [null, "ab"], "labels": {"k": "v"}, "ids": ["ab"]}`, ""},
		{`{"extra": "abcd"}`, ""},

		// Strings: the length counts runes.
		{`{"names": ["a", ""]}`, "field names[1] length: must be >= 1"},
		{`{"names": ["äöüß"]}`, "field names[0] length: must be <= 3"},
		{`{"codes": ["ab", "AB"]}`, "field codes[1] pattern match: must match ^[a-z]+$"},
		{`{"fixed": ["y"]}`, "field fixed[0]: must be equal to x"},

		// Numbers and booleans.
		{`{"ratios": [-0.1]}`, "field ratios[0]: must be >= 0"},
		{`{"ratios": [1]}`, "field ratios[0]: must be < 1"},
		{`{"halves": [0.3]}`, "field halves[0]: must be a multiple of 0.5"},
		{`{"counts": [12]}`, "field counts[0]: must be <= 10"},
		{`{"counts": [3]}`, "field counts[0]: must be a multiple of 2"},
		{`{"flags": [false]}`, "field flags[0]: must be equal to true"},

		// A const of no declared type is compared as JSON.
		{`{"anything": [{"a": [1, "c"]}]}`, `field anything[0]: must be equal to {"a":[1,"b"]}`},
		{`{"anything": [null]}`, `field anything[0]: must be equal to {"a":[1,"b"]}`},
		{`{"tag": "v2"}`, `field tag: must be equal to "v1"`},
		{`{"tag": null}`, `field tag: must be equal to "v1"`},

		// A nested array has its own bounds, and only the field has the
		// field's: an inner array of one item passes the outer minItems.
		{`{"grid": [["a"]]}`, "field grid length: must be >= 2"},
		{`{"grid": [["a"], ["b", "c", "d"]]}`, "field grid[1] length: must be <= 2"},
		{`{"grid": [["a"], ["b", ""]]}`, "field grid[1][1] length: must be >= 1"},
		{`{"loose": [[1], [2]]}`, ""},
		{`{"loose": [[1]]}`, "field loose length: must be >= 2"},

		// A null element is not a string, so its length is not checked.
		{`{"optional": [null, "a"]}`, "field optional[1] length: must be >= 2"},

		// Map values, those of a declared map and those of the object's own
		// additionalProperties, and the elements of a declared array.
		{`{"labels": {"k": ""}}`, `field ElementsLabels["k"] length: must be >= 1`},
		{`{"extra": "abcde"}`, `field ["extra"] length: must be <= 4`},
		{`{"ids": ["ab", "a"]}`, "field Ids[1] length: must be >= 2"},
	})

	// encoding/json assigns a key that differs only in case to the field, so
	// the JSON check reads it too. yaml.v3 does not, and there it is an
	// additional property.
	t.Run("a key that differs in case", func(t *testing.T) {
		t.Parallel()

		var fromJSON, fromYAML testElements.Elements

		require.ErrorContains(t, json.Unmarshal([]byte(`{"TAG": "v2"}`), &fromJSON), `field tag: must be equal to "v1"`)
		require.NoError(t, yamlv3.Unmarshal([]byte(`{"TAG": "v2"}`), &fromYAML))
		assert.Equal(t, map[string]string{"TAG": "v2"}, fromYAML.AdditionalProperties)
	})
}
