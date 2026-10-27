package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testMapDefaults "github.com/plheide/go-jsonschema/tests/data/mapDefaults/defaults"
)

// TestMapDefaults covers a map property with a default, which starts as an
// empty value of its own Go type, however its elements are typed.
func TestMapDefaults(t *testing.T) {
	t.Parallel()

	var v testMapDefaults.Defaults

	require.NoError(t, json.Unmarshal([]byte(`{}`), &v))
	assert.Equal(t, testMapDefaults.DefaultsHeaders{}, v.Headers)
	assert.NotNil(t, v.Headers)
	assert.Equal(t, testMapDefaults.DefaultsCounts{}, v.Counts)
	assert.Equal(t, testMapDefaults.DefaultsSettings{}, v.Settings)

	// The map starts empty, as before: the default's members are not copied
	// into it.
	assert.Equal(t, testMapDefaults.DefaultsLabels{}, v.Labels)

	// A value given in the input is kept.
	require.NoError(t, json.Unmarshal([]byte(`{"labels": {"x": "y"}}`), &v))
	assert.Equal(t, testMapDefaults.DefaultsLabels{"x": "y"}, v.Labels)

	// A null is defaulted as an absent key is, and YAML is decoded alike.
	var fromNull, fromYAML testMapDefaults.Defaults

	require.NoError(t, json.Unmarshal([]byte(`{"headers": null}`), &fromNull))
	assert.Equal(t, testMapDefaults.DefaultsHeaders{}, fromNull.Headers)
	assert.NotNil(t, fromNull.Headers)

	require.NoError(t, yamlv3.Unmarshal([]byte(`{}`), &fromYAML))
	assert.Equal(t, testMapDefaults.DefaultsHeaders{}, fromYAML.Headers)
	assert.NotNil(t, fromYAML.Headers)
	assert.Equal(t, testMapDefaults.DefaultsSettings{}, fromYAML.Settings)
}
