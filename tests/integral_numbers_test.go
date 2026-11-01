package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testBounds "github.com/plheide/go-jsonschema/tests/data/integralNumbers/bounds"
	testCycle "github.com/plheide/go-jsonschema/tests/data/integralNumbers/cycle"
	testMultiples "github.com/plheide/go-jsonschema/tests/data/integralNumbers/multiples"
	testShapes "github.com/plheide/go-jsonschema/tests/data/integralNumbers/shapes"
	testWritten "github.com/plheide/go-jsonschema/tests/data/integralNumbers/written"
	testExact "github.com/plheide/go-jsonschema/tests/data/minSizedInts/exactReferences"
)

// TestIntegralNumbers covers integers written with a fraction or an exponent.
// JSON Schema counts 2.0, 2e0 and 1e2 as integers, which encoding/json refuses
// for a Go integer and yaml.v3 decodes by truncating, so each decoder of a type
// that holds one writes such numbers as integers.
func TestIntegralNumbers(t *testing.T) {
	t.Parallel()

	port := testShapes.Port(80)
	priority := testShapes.ShapesPriority(2)

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			input string
			want  testShapes.Shapes
		}{
			{`{"count": 2}`, testShapes.Shapes{Count: 2}},
			{`{"count": 2.0}`, testShapes.Shapes{Count: 2}},
			{`{"count": 2e0}`, testShapes.Shapes{Count: 2}},
			{`{"count": 1e2}`, testShapes.Shapes{Count: 100}},
			{`{"count": 1E+2}`, testShapes.Shapes{Count: 100}},
			{`{"count": -3.00}`, testShapes.Shapes{Count: -3}},
			{`{"count": 1.5e1}`, testShapes.Shapes{Count: 15}},
			{`{"count": 120e-1}`, testShapes.Shapes{Count: 12}},
			{`{"count": -0.0}`, testShapes.Shapes{Count: 0}},
			{`{"count": 0e5}`, testShapes.Shapes{Count: 0}},
			// Zero, whatever its exponent.
			{`{"count": 0e99}`, testShapes.Shapes{Count: 0}},
			{`{"count": -0.0e-41}`, testShapes.Shapes{Count: 0}},
			{`{"count": 100e-2}`, testShapes.Shapes{Count: 1}},
			{`{"count": 1.000e3}`, testShapes.Shapes{Count: 1000}},
			{`{"count": 1, "limit": 4.0, "level": 1e1}`, testShapes.Shapes{Count: 1, Limit: ptr(4), Level: ptr(10)}},
			// A number, not an integer, is left as it is.
			{`{"count": 1.0, "ratio": 2.50}`, testShapes.Shapes{Count: 1, Ratio: ptr(2.5)}},
			{`{"count": 1, "sizes": [1.0, 2e0, 3]}`, testShapes.Shapes{Count: 1, Sizes: []int{1, 2, 3}}},
			{`{"count": 1, "grid": [[1.0, 2], [3e0]]}`, testShapes.Shapes{Count: 1, Grid: [][]int{{1, 2}, {3}}}},
			{`{"count": 1, "counts": {"a": 1.0}}`, testShapes.Shapes{Count: 1, Counts: testShapes.ShapesCounts{"a": 1}}},
			{`{"count": 1, "inner": {"n": 7.0}}`, testShapes.Shapes{Count: 1, Inner: &testShapes.ShapesInner{N: ptr(7)}}},
			{`{"count": 1, "holder": {"port": 80.0}}`, testShapes.Shapes{Count: 1, Holder: &testShapes.ShapesHolder{Port: &port}}},
			{`{"count": 1, "port": 8e1}`, testShapes.Shapes{Count: 1, Port: &port}},
			{`{"count": 1, "ids": [1.0, 2]}`, testShapes.Shapes{Count: 1, Ids: testShapes.Ids{1, 2}}},
			{`{"count": 1, "priority": 2.0}`, testShapes.Shapes{Count: 1, Priority: &priority}},
		} {
			var fromJSON, fromYAML testShapes.Shapes

			require.NoError(t, json.Unmarshal([]byte(tc.input), &fromJSON), "json %s", tc.input)
			assert.Equal(t, tc.want, fromJSON, "json %s", tc.input)

			require.NoError(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), "yaml %s", tc.input)
			assert.Equal(t, tc.want, fromYAML, "yaml %s", tc.input)
		}

		// encoding/json finds a field under a key whose case differs; yaml.v3
		// does not.
		var v testWritten.Written

		require.NoError(t, json.Unmarshal([]byte(`{"COUNT": 2.0}`), &v))
		assert.Equal(t, ptr(2), v.Count)
	})

	// The digits are written, not a float64, so no precision is lost.
	t.Run("exact beyond float64", func(t *testing.T) {
		t.Parallel()

		var fromJSON, fromYAML testShapes.Shapes

		require.NoError(t, json.Unmarshal([]byte(`{"count": 9.223372036854775807e18}`), &fromJSON))
		assert.Equal(t, 9223372036854775807, fromJSON.Count)
		require.NoError(t, yamlv3.Unmarshal([]byte(`count: 9.223372036854775807e18`), &fromYAML))
		assert.Equal(t, 9223372036854775807, fromYAML.Count)
	})

	// Sized and unsigned integers keep their range.
	t.Run("sized", func(t *testing.T) {
		t.Parallel()

		var (
			b8  testExact.Bound8
			u8  testExact.UBound8
			u64 testExact.UBound64
		)

		require.NoError(t, json.Unmarshal([]byte(`-1.28e2`), &b8))
		assert.Equal(t, testExact.Bound8(-128), b8)
		require.NoError(t, json.Unmarshal([]byte(`255.0`), &u8))
		assert.Equal(t, testExact.UBound8(255), u8)
		require.NoError(t, json.Unmarshal([]byte(`1.8446744073709551615e19`), &u64))
		assert.Equal(t, testExact.UBound64(18446744073709551615), u64)

		// A number the type can't hold is named as written.
		require.ErrorContains(t, json.Unmarshal([]byte(`1.28e2`), &b8), "cannot unmarshal number 1.28e2")
		require.ErrorContains(t, json.Unmarshal([]byte(`256.0`), &u8), "cannot unmarshal number 256.0")
		require.ErrorContains(t, json.Unmarshal([]byte(`-1.0`), &u8), "cannot unmarshal number -1.0")

		require.NoError(t, yamlv3.Unmarshal([]byte(`-1.28e2`), &b8))
		assert.Equal(t, testExact.Bound8(-128), b8)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(`1.28e2`), &b8), "1.28e2 is out of range for int8")
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(`-1.0`), &u8), "-1.0 is out of range for uint8")
	})

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			input   string
			wantErr string
		}{
			{`{"count": 2.5}`, "cannot unmarshal number 2.5 into Go struct field Plain.count of type int"},
			{`{"count": 1e-1}`, "cannot unmarshal number 1e-1"},
			{`{"count": 1.0000001}`, "cannot unmarshal number 1.0000001"},
			// Integers that no Go int holds, named as written.
			{`{"count": 1e19}`, "cannot unmarshal number 1e19 into Go struct field Plain.count of type int"},
			{`{"count": 1e20}`, "cannot unmarshal number 1e20"},
			{`{"count": 1e300}`, "cannot unmarshal number 1e300"},
			// The error names the value that is wrong, not one that was retried.
			{`{"count": 1.0, "sizes": [1.0, 2.5]}`, "cannot unmarshal number 2.5 into Go struct field Plain.sizes"},
			{`{"count": "2"}`, "cannot unmarshal string into Go struct field Plain.count of type int"},
			// Validators still run on the retried value.
			{`{"count": 1, "level": 0.0}`, "field level: must be >= 1"},
			{`{"count": 1, "priority": 4.0}`, "invalid value (expected one of"},
			{`{"count": 1, "port": 80.5}`, "cannot unmarshal number 80.5"},
			// Trailing data stays a syntax error on the retry.
			{`{"count": 2.0} x`, "invalid character 'x' after top-level value"},
		} {
			var v testShapes.Shapes

			require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &v), tc.wantErr, "json %s", tc.input)
		}

		// yaml.v3 would truncate a float into a Go integer, and wrap one past
		// its range.
		for _, tc := range []struct {
			input   string
			wantErr string
		}{
			{`count: 2.5`, "unmarshal Shapes: field count: 2.5 is not an integer"},
			{`count: 1e-1`, "field count: 1e-1 is not an integer"},
			{`count: -.inf`, "field count: -.inf is not an integer"},
			{`count: .nan`, "field count: .nan is not an integer"},
			{`count: 1e19`, "field count: 1e19 is out of range for int"},
			{`count: -9.3e18`, "field count: -9.3e18 is out of range for int"},
			{`{"count": 1.0, "sizes": [1.0, 2.5]}`, "field sizes[1]: 2.5 is not an integer"},
			{`{"count": 1, "grid": [[1], [2, 0.5]]}`, "field grid[1][1]: 0.5 is not an integer"},
			{`{"count": 1, "counts": {"a": 0.5}}`, `unmarshal ShapesCounts: field ["a"]: 0.5 is not an integer`},
			{`{"count": 1, "port": 80.5}`, "unmarshal Port: 80.5 is not an integer"},
			{`{"count": 1, "level": 0.0}`, "field level: must be >= 1"},
		} {
			var v testShapes.Shapes

			require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &v), tc.wantErr, "yaml %s", tc.input)
		}
	})

	// The YAML decoder follows aliases and merge keys, and leaves the node it
	// is given as it is.
	t.Run("yaml nodes", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			input string
			want  testShapes.Shapes
		}{
			{"count: &n 2.0\nlimit: *n", testShapes.Shapes{Count: 2, Limit: ptr(2)}},
			{"count: 1\ninner:\n  <<: {n: 7.0}", testShapes.Shapes{Count: 1, Inner: &testShapes.ShapesInner{N: ptr(7)}}},
			{"count: 1\ninner:\n  <<: [{n: 7.0}]", testShapes.Shapes{Count: 1, Inner: &testShapes.ShapesInner{N: ptr(7)}}},
			{"count: 1\nsizes: &s [1.0, 2e0]\ngrid: [*s, *s]", testShapes.Shapes{
				Count: 1, Sizes: []int{1, 2}, Grid: [][]int{{1, 2}, {1, 2}},
			}},
		} {
			var v testShapes.Shapes

			require.NoError(t, yamlv3.Unmarshal([]byte(tc.input), &v), "yaml %s", tc.input)
			assert.Equal(t, tc.want, v, "yaml %s", tc.input)
		}

		var node yamlv3.Node

		//nolint:musttag // A yaml.Node holds the document as yaml.v3 parses it; it has no fields to tag.
		require.NoError(t, yamlv3.Unmarshal([]byte(`{"count": 2.0, "sizes": [3.0]}`), &node))

		var v testShapes.Shapes

		require.NoError(t, node.Decode(&v))
		assert.Equal(t, testShapes.Shapes{Count: 2, Sizes: []int{3}}, v)

		var again map[string]any

		require.NoError(t, node.Decode(&again))
		assert.Equal(t, map[string]any{"count": 2.0, "sizes": []any{3.0}}, again)
	})

	// Only a number where the type holds an integer is written again: every
	// other value keeps its form.
	t.Run("written in place", func(t *testing.T) {
		t.Parallel()

		var v testWritten.Written

		require.NoError(t, json.Unmarshal([]byte(`{"count": 2.0, "raw": {"b": 1, "a": 3.0}}`), &v))
		assert.Equal(t, ptr(2), v.Count)
		require.NotNil(t, v.Raw)
		assert.Equal(t, `{"b": 1, "a": 3.0}`, string(*v.Raw))
	})

	// A type declared as a pointer to an integer has no decoder of its own, so
	// the decoder of the type that holds it writes its numbers, even when it
	// holds no other integer.
	t.Run("nullable definition", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{`{"nullable": 1.0}`, `{"nullable": 1e0}`} {
			var fromJSON, fromYAML testWritten.WrittenHolder

			require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
			require.NotNil(t, fromJSON.Nullable, "json %s", input)
			assert.Equal(t, 1, *fromJSON.Nullable, "json %s", input)

			require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
			require.NotNil(t, fromYAML.Nullable, "yaml %s", input)
			assert.Equal(t, 1, *fromYAML.Nullable, "yaml %s", input)
		}
	})

	// A type's decoder can be made before the type is finished, when a $ref
	// inside it leads back to it; it decodes the finished type.
	t.Run("decoder made while its type is generated", func(t *testing.T) {
		t.Parallel()

		input := `{"count": 2.0, "limit": 3e0, "extra": 4.0}`
		want := testCycle.Action{Count: ptr(2), Limit: ptr(3), AdditionalProperties: map[string]int{"extra": 4}}

		var fromJSON, fromYAML testCycle.Action

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON))
		assert.Equal(t, want, fromJSON)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML))
		assert.Equal(t, want, fromYAML)
	})

	// Each additional property is decoded from its own value, as a declared
	// one is, so that an integer is exact and a fraction refused.
	t.Run("additional properties", func(t *testing.T) {
		t.Parallel()

		var fromJSON, fromYAML testWritten.Written

		require.NoError(t, json.Unmarshal([]byte(`{"count": 1, "x": 2.0, "y": 3}`), &fromJSON))
		assert.Equal(t, ptr(1), fromJSON.Count)
		assert.Equal(t, map[string]int{"x": 2, "y": 3}, fromJSON.AdditionalProperties)

		require.NoError(t, yamlv3.Unmarshal([]byte("count: 1\nx: 2.0\ny: 3"), &fromYAML))
		assert.Equal(t, ptr(1), fromYAML.Count)
		assert.Equal(t, map[string]int{"x": 2, "y": 3}, fromYAML.AdditionalProperties)

		var exact testWritten.Written

		require.NoError(t, json.Unmarshal([]byte(`{"x": 9.223372036854775807e18}`), &exact))
		assert.Equal(t, map[string]int{"x": 9223372036854775807}, exact.AdditionalProperties)

		for _, tc := range []struct {
			input, wantJSON, wantYAML string
		}{
			{
				`{"x": 2.5}`,
				`decode additional property "x" for Written: json: cannot unmarshal number 2.5`,
				`decode additional property "x" for Written: 2.5 is not an integer`,
			},
			{
				`{"x": 1e300}`,
				`decode additional property "x" for Written: json: cannot unmarshal number 1e300`,
				`decode additional property "x" for Written: 1e300 is out of range for int`,
			},
			// Of the keys that fail, the first in order is named.
			{`{"y": 0.5, "x": 2.5}`, `decode additional property "x"`, `decode additional property "x"`},
		} {
			var fromJSON, fromYAML testWritten.Written

			require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantJSON, "json %s", tc.input)
			require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantYAML, "yaml %s", tc.input)
		}
	})
}

// TestIntegerBounds covers an integer's bounds, which are read as the schema
// writes them: one beyond 2^53 isn't rounded, and one with a fraction bounds
// the integers that it lets through.
func TestIntegerBounds(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"atMost64": 9223372036854775807, "atLeast64": -9223372036854775808, "pastAny": 9223372036854775807}`,
		`{"beyond53": 9007199254740993, "hundred": 100, "below": 9}`,
		`{"halfMax": 2, "halfMin": 3, "halfBelow": 2, "halfAbove": 3}`,
		`{"items": [9223372036854775807, -1]}`,
	} {
		var fromJSON, fromYAML testBounds.Bounds

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
		assert.Equal(t, fromJSON, fromYAML, input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"beyond53": 9007199254740994}`, "field beyond53: must be <= 9007199254740993"},
		{`{"hundred": 101}`, "field hundred: must be <= 100"},
		{`{"below": 10}`, "field below: must be < 10"},
		{`{"halfMax": 3}`, "field halfMax: must be <= 2.5"},
		{`{"halfMin": 2}`, "field halfMin: must be >= 2.5"},
		{`{"halfBelow": 3}`, "field halfBelow: must be < 2.5"},
		{`{"halfAbove": 2}`, "field halfAbove: must be > 2.5"},
		// No int is at most -1e30.
		{`{"none": -9223372036854775808}`, "field none: must be <= -1e30"},
	} {
		var fromJSON, fromYAML testBounds.Bounds

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}

// TestIntegerMultipleOf covers multipleOf on an integer. One with a fraction
// holds an integer to multiples of its numerator, in lowest terms: 2.5 to
// multiples of 5, and 0.5 to nothing.
func TestIntegerMultipleOf(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"fiveHalves": 10, "half": 7, "tiny": 12391239123, "huge": 0, "three": 9, "halves": [1, 2, 3]}`,
		`{"fiveHalves": -5, "three": 0}`,
	} {
		var fromJSON, fromYAML testMultiples.Multiples

		require.NoError(t, json.Unmarshal([]byte(input), &fromJSON), "json %s", input)
		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), "yaml %s", input)
		assert.Equal(t, fromJSON, fromYAML, input)
	}

	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		{`{"fiveHalves": 7}`, "field fiveHalves: must be a multiple of 2.5"},
		{`{"huge": 1}`, "field huge: must be a multiple of 1e30"},
		{`{"three": 4}`, "field three: must be a multiple of 3"},
	} {
		var fromJSON, fromYAML testMultiples.Multiples

		require.ErrorContains(t, json.Unmarshal([]byte(tc.input), &fromJSON), tc.wantErr, "json %s", tc.input)
		require.ErrorContains(t, yamlv3.Unmarshal([]byte(tc.input), &fromYAML), tc.wantErr, "yaml %s", tc.input)
	}
}
