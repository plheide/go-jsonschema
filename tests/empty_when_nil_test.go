package tests_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"

	testEmptyWhenNil "github.com/plheide/go-jsonschema/tests/data/emptyWhenNil/required"
)

// TestEmptyWhenNil covers required arrays and objects left nil, which are
// written as [] and {}, as the schema requires, where encoding/json writes
// null. A nullable or optional one is left as it is.
func TestEmptyWhenNil(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(testEmptyWhenNil.Required{Name: "x"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name": "x", "items": [], "ids": [], "labels": {}, "nullable": null}`, string(out))

	// The output decodes again.
	var back testEmptyWhenNil.Required
	require.NoError(t, json.Unmarshal(out, &back))

	// A pointer marshals the same way, and a set field is left as it is.
	out, err = json.Marshal(&testEmptyWhenNil.Required{Name: "x", Items: []string{"a"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name": "x", "items": ["a"], "ids": [], "labels": {}, "nullable": null}`, string(out))

	// yaml.v3 writes a nil slice and a nil map as [] and {} itself.
	out, err = yamlv3.Marshal(testEmptyWhenNil.Required{Name: "x"})
	require.NoError(t, err)
	assert.Contains(t, string(out), "items: []")
	assert.Contains(t, string(out), "labels: {}")
}
