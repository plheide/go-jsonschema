package types_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/plheide/go-jsonschema/pkg/types"
)

func TestDurationJSON(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{`"PT1H30M"`, 90 * time.Minute},
		{`"PT0.5S"`, 500 * time.Millisecond},
		{`"P1DT12H"`, 36 * time.Hour},
		{`"P1W"`, 7 * 24 * time.Hour},
		{`"PT0S"`, 0},
		{`"-PT1M"`, -time.Minute},
		// A number is nanoseconds, as encoding/json reads a time.Duration.
		{`1500000000`, 1500 * time.Millisecond},
	} {
		var d types.Duration

		require.NoError(t, json.Unmarshal([]byte(tc.input), &d), tc.input)
		require.Equal(t, tc.want, d.Duration, tc.input)
	}

	for _, input := range []string{`"1h30m"`, `"P"`, `"PT"`, `"PT1D"`, `"P1000000Y"`, `"x"`, `true`, `{}`, `1.5`} {
		var d types.Duration

		require.Error(t, json.Unmarshal([]byte(input), &d), input)
	}

	// A null leaves the value as it is, as for the other types here.
	d := types.Duration{Duration: time.Second}
	require.NoError(t, json.Unmarshal([]byte(`null`), &d))
	require.Equal(t, time.Second, d.Duration)
}

func TestDurationYAML(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{`PT1H30M`, 90 * time.Minute},
		{`"PT20S"`, 20 * time.Second},
		// Go's syntax, as yaml.v3 reads a time.Duration.
		{`1h30m`, 90 * time.Minute},
		{`"250ms"`, 250 * time.Millisecond},
	} {
		var d types.Duration

		require.NoError(t, yaml.Unmarshal([]byte(tc.input), &d), tc.input)
		require.Equal(t, tc.want, d.Duration, tc.input)
	}

	// yaml.v3 refuses a number for a time.Duration as well.
	for _, input := range []string{`30`, `1.5`, `true`, `[PT1S]`, `"30"`, `P`} {
		var d types.Duration

		require.Error(t, yaml.Unmarshal([]byte(input), &d), input)
	}
}

func TestDurationWritten(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "PT0S"},
		{90 * time.Minute, "PT1H30M"},
		{36 * time.Hour, "PT36H"},
		{31 * 24 * time.Hour, "PT744H"},
		{1500 * time.Millisecond, "PT1.5S"},
		{time.Nanosecond, "PT0.000000001S"},
		{-time.Minute, "-PT1M"},
		{math.MinInt64, "-PT2562047H47M16.854775808S"},
	} {
		encoded, err := json.Marshal(types.Duration{Duration: tc.d})
		require.NoError(t, err)
		require.JSONEq(t, `"`+tc.want+`"`, string(encoded))

		// What is written reads back as the same duration.
		var back types.Duration

		require.NoError(t, json.Unmarshal(encoded, &back))
		require.Equal(t, tc.d, back.Duration, tc.want)

		yamlEncoded, err := yaml.Marshal(types.Duration{Duration: tc.d})
		require.NoError(t, err)
		require.YAMLEq(t, tc.want, string(yamlEncoded))
	}
}
