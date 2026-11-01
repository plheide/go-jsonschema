package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testUseNumber "github.com/plheide/go-jsonschema/tests/data/useNumber/numbers"
)

// TestUseNumber covers --use-number, under which untyped values decoded from
// JSON hold their numbers as json.Number, which keeps them exact.
func TestUseNumber(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T, input string) testUseNumber.Numbers {
		t.Helper()

		var v testUseNumber.Numbers
		require.NoError(t, json.Unmarshal([]byte(input), &v), input)

		return v
	}

	t.Run("untyped values hold json.Number, at any depth", func(t *testing.T) {
		t.Parallel()

		v := decode(t, `{"value": 9007199254740993, "values": [1.5, {"id": 9007199254740993}], "count": 2, "ratio": 0.5}`)
		assert.Equal(t, json.Number("9007199254740993"), v.Value)
		assert.Equal(t, []any{
			json.Number("1.5"),
			map[string]any{"id": json.Number("9007199254740993")},
		}, v.Values)

		// Typed fields are decoded as before.
		assert.Equal(t, 2, *v.Count)
		assert.InDelta(t, 0.5, *v.Ratio, 0)
	})

	// A type declared as an interface{} can have no decoder of its own, so
	// the struct that holds it gets one.
	t.Run("a type declared as interface{} holds json.Number", func(t *testing.T) {
		t.Parallel()

		v := decode(t, `{"holder": {"x": 9007199254740993}}`)
		assert.Equal(t, testUseNumber.Anything(json.Number("9007199254740993")), v.Holder.X)
	})

	// The retry writes only the integer's number again: the untyped one keeps
	// its text.
	t.Run("an integral integer is retried, and the retry keeps json.Number as written", func(t *testing.T) {
		t.Parallel()

		v := decode(t, `{"count": 2.0, "value": 3.0}`)
		assert.Equal(t, 2, *v.Count)
		assert.Equal(t, json.Number("3.0"), v.Value)
	})

	t.Run("an additional property holds json.Number", func(t *testing.T) {
		t.Parallel()

		v := decode(t, `{"other": 1.5, "more": [9007199254740993]}`)
		assert.Equal(t, map[string]any{
			"other": json.Number("1.5"),
			"more":  []any{json.Number("9007199254740993")},
		}, v.AdditionalProperties)
	})

	t.Run("an untyped default holds json.Number", func(t *testing.T) {
		t.Parallel()

		v := decode(t, `{}`)
		assert.Equal(t, json.Number("1.5"), v.Extra)
		assert.Equal(t, map[string]any{
			"a": []any{json.Number("1"), json.Number("2.5")},
		}, v.Nested)
	})

	t.Run("a const is compared by value", func(t *testing.T) {
		t.Parallel()

		decode(t, `{"pinned": 1.0, "pinnedItems": [1, 1.0, 1e0]}`)

		var v testUseNumber.Numbers
		require.ErrorContains(t, json.Unmarshal([]byte(`{"pinned": 2}`), &v), "field pinned: must be equal to 1")
		require.ErrorContains(t, json.Unmarshal([]byte(`{"pinnedItems": [1, 2]}`), &v),
			"field pinnedItems[1]: must be equal to 1")
	})

	// A float64 holds 9007199254740992 for both 9007199254740992 and
	// 9007199254740993, but json.Number tells them apart, as the schema does.
	t.Run("a const beyond 2^53 is compared exactly", func(t *testing.T) {
		t.Parallel()

		decode(t, `{"big": 9007199254740993, "near": 9007199254740992, "bigItems": [9007199254740993]}`)

		for input, want := range map[string]string{
			`{"big": 9007199254740992}`:        "field big: must be equal to 9007199254740993",
			`{"near": 9007199254740993}`:       "field near: must be equal to 9007199254740992",
			`{"bigItems": [9007199254740992]}`: "field bigItems[0]: must be equal to 9007199254740993",
		} {
			var v testUseNumber.Numbers
			require.ErrorContains(t, json.Unmarshal([]byte(input), &v), want, input)
		}
	})

	t.Run("input that is not JSON gets json.Unmarshal's error", func(t *testing.T) {
		t.Parallel()

		var v testUseNumber.Numbers
		require.ErrorContains(t, json.Unmarshal([]byte(`{"value": 1} x`), &v),
			"invalid character 'x' after top-level value")
	})

	// yaml.v3 decodes integers exactly, so YAML is as before.
	t.Run("YAML is unaffected", func(t *testing.T) {
		t.Parallel()

		var v testUseNumber.Numbers
		require.NoError(t, yamlv3.Unmarshal([]byte(`{"value": 9007199254740993}`), &v))
		assert.Equal(t, 9007199254740993, v.Value)
		assert.InDelta(t, 1.5, v.Extra, 0)
	})
}
