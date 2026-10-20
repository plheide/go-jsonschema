package tests_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	"github.com/plheide/go-jsonschema/pkg/types"
	testOneOfInField "github.com/plheide/go-jsonschema/tests/data/oneOfPrimitive/inField"
	testOneOfTemporal "github.com/plheide/go-jsonschema/tests/data/oneOfPrimitive/temporalVariant"
	testOneOfWithNull "github.com/plheide/go-jsonschema/tests/data/oneOfPrimitive/withNull"
)

// TestWrapperConstructors builds primitive-union wrappers with their
// constructors, the only way code outside the package can set their unexported
// fields. Each one marshals as its value, reads back through its accessor, and
// equals the wrapper that decoding the same value gives.
func TestWrapperConstructors(t *testing.T) {
	t.Parallel()

	t.Run("string, number and bool", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			built testOneOfInField.InFieldValue
			json  string
		}{
			{testOneOfInField.NewInFieldValueString("x"), `"x"`},
			{testOneOfInField.NewInFieldValueNumber(1.5), `1.5`},
			{testOneOfInField.NewInFieldValueBool(true), `true`},
		} {
			got, err := json.Marshal(tc.built)
			require.NoError(t, err)
			assert.JSONEq(t, tc.json, string(got))

			var decoded testOneOfInField.InFieldValue
			require.NoError(t, json.Unmarshal([]byte(tc.json), &decoded))
			assert.Equal(t, decoded, tc.built, tc.json)
		}

		v := testOneOfInField.NewInFieldValueString("x")
		s, ok := v.AsString()
		assert.True(t, ok)
		assert.Equal(t, "x", s)
	})

	t.Run("in a struct, through JSON and YAML", func(t *testing.T) {
		t.Parallel()

		doc := testOneOfInField.InField{Name: "a", Value: testOneOfInField.NewInFieldValueNumber(2)}

		out, err := json.Marshal(doc)
		require.NoError(t, err)
		assert.JSONEq(t, `{"name": "a", "value": 2}`, string(out))

		var back testOneOfInField.InField
		require.NoError(t, json.Unmarshal(out, &back))
		assert.Equal(t, doc, back)

		y, err := yamlv3.Marshal(doc)
		require.NoError(t, err)
		assert.Equal(t, "name: a\nvalue: 2\n", string(y))

		var fromYAML testOneOfInField.InField
		require.NoError(t, yamlv3.Unmarshal(y, &fromYAML))
		assert.Equal(t, doc, fromYAML)
	})

	t.Run("an explicit null", func(t *testing.T) {
		t.Parallel()

		null := testOneOfWithNull.NewWithNullValueNull()
		assert.True(t, null.IsNull())

		got, err := json.Marshal(null)
		require.NoError(t, err)
		assert.Equal(t, "null", string(got))

		var decoded testOneOfWithNull.WithNullValue
		require.NoError(t, json.Unmarshal([]byte("null"), &decoded))
		assert.Equal(t, decoded, null)
	})

	t.Run("temporal kinds", func(t *testing.T) {
		t.Parallel()

		when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		dt := testOneOfTemporal.NewTemporalVariantTimeOrNumberDateTime(when)

		got, err := json.Marshal(dt)
		require.NoError(t, err)
		assert.Equal(t, `"2024-01-02T03:04:05Z"`, string(got))

		back, ok := dt.AsDateTime()
		assert.True(t, ok)
		assert.True(t, when.Equal(back))

		day := testOneOfTemporal.NewTemporalVariantDayDate(
			types.SerializableDate{Time: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)})

		got, err = json.Marshal(day)
		require.NoError(t, err)
		assert.Equal(t, `"2024-01-02"`, string(got))
	})

	t.Run("a taken name is suffixed and reported", func(t *testing.T) {
		t.Parallel()

		// The schema defines types named like wrapper constructors. Whichever
		// of the two is declared second takes the next free name rather than
		// vanishing, so the golden file compiles.
		warnings := generateWithWarnerCapture(t,
			"./data/oneOfPrimitive/constructorNameCollision/constructorNameCollision.json")

		// The types come first, so the root wrapper's constructors move.
		for _, kind := range []string{"String", "Number"} {
			base := "NewConstructorNameCollisionValue" + kind
			assert.Contains(t, warnings, "Wrapper ConstructorNameCollisionValue: constructor name "+
				`"`+base+`" is already declared in this package; declaring it as "`+base+`_1" instead`)
		}

		// Choice's constructor comes first, so the type moves.
		assert.Contains(t, warnings,
			`Multiple types map to the name "NewChoiceString"; declaring duplicate as "NewChoiceString_1" instead`)
	})
}
