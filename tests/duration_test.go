package tests_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testDuration "github.com/plheide/go-jsonschema/tests/data/core/duration"
)

// TestDurationFormat covers a string of format duration, which is read and
// written as an ISO 8601 duration. JSON also gives one as a number of
// nanoseconds, and YAML in Go's syntax, as they give a time.Duration.
func TestDurationFormat(t *testing.T) {
	t.Parallel()

	var fromJSON testDuration.Duration

	require.NoError(t, json.Unmarshal([]byte(`{"myObject": {"withoutDefault": "PT1H30M"}}`), &fromJSON))
	require.Equal(t, 90*time.Minute, fromJSON.MyObject.WithoutDefault.Duration)
	require.Equal(t, 20*time.Second, fromJSON.MyObject.WithDefault.Duration)

	encoded, err := json.Marshal(fromJSON)
	require.NoError(t, err)
	require.JSONEq(t, `{"myObject": {"withDefault": "PT20S", "withoutDefault": "PT1H30M"}}`, string(encoded))

	var fromNumber testDuration.Duration

	require.NoError(t, json.Unmarshal([]byte(`{"myObject": {"withoutDefault": 1500000000}}`), &fromNumber))
	require.Equal(t, 1500*time.Millisecond, fromNumber.MyObject.WithoutDefault.Duration)

	for _, input := range []string{`myObject: {withoutDefault: PT1H30M}`, `myObject: {withoutDefault: 1h30m}`} {
		var fromYAML testDuration.Duration

		require.NoError(t, yamlv3.Unmarshal([]byte(input), &fromYAML), input)
		require.Equal(t, 90*time.Minute, fromYAML.MyObject.WithoutDefault.Duration, input)
		require.Equal(t, 20*time.Second, fromYAML.MyObject.WithDefault.Duration, input)
	}

	for _, input := range []string{`{"myObject": {"withoutDefault": "soon"}}`, `{"myObject": {"withoutDefault": "1h"}}`} {
		var invalid testDuration.Duration

		require.ErrorContains(t, json.Unmarshal([]byte(input), &invalid), "not an ISO 8601 duration", input)
	}
}
