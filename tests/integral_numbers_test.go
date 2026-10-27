package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testShapes "github.com/plheide/go-jsonschema/tests/data/integralNumbers/shapes"
	testExact "github.com/plheide/go-jsonschema/tests/data/minSizedInts/exactReferences"
)

// TestIntegralNumbers covers integers written with a fraction or an exponent.
// JSON Schema counts 2.0, 2e0 and 1e2 as integers, which encoding/json refuses
// for a Go integer, so each JSON decoder of a type that holds one retries.
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
	})

	// The digits are rewritten, not a float64, so no precision is lost. Only
	// JSON: yaml.v3 decodes the number as a float64.
	t.Run("exact beyond float64", func(t *testing.T) {
		t.Parallel()

		var v testShapes.Shapes

		require.NoError(t, json.Unmarshal([]byte(`{"count": 9.223372036854775807e18}`), &v))
		assert.Equal(t, 9223372036854775807, v.Count)
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

		require.ErrorContains(t, json.Unmarshal([]byte(`1.28e2`), &b8), "cannot unmarshal number 128")
		require.ErrorContains(t, json.Unmarshal([]byte(`256.0`), &u8), "cannot unmarshal number 256")
		require.ErrorContains(t, json.Unmarshal([]byte(`-1.0`), &u8), "cannot unmarshal number -1")
	})

	// Only JSON: yaml.v3 decodes 2.5 into a Go integer as 2, without an error.
	t.Run("rejected", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			input   string
			wantErr string
		}{
			{`{"count": 2.5}`, "cannot unmarshal number 2.5 into Go struct field Plain.count of type int"},
			{`{"count": 1e-1}`, "cannot unmarshal number 1e-1"},
			{`{"count": 1.0000001}`, "cannot unmarshal number 1.0000001"},
			// Integers that no Go int holds.
			{`{"count": 1e19}`, "into Go struct field Plain.count of type int"},
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

			assert.ErrorContains(t, json.Unmarshal([]byte(tc.input), &v), tc.wantErr, "json %s", tc.input)
		}
	})
}
