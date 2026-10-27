package tests_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testConsts "github.com/plheide/go-jsonschema/tests/data/constConstants/consts"
)

// TestConstConstants covers the exported constant of each property with a
// primitive const, named after the struct and the field.
func TestConstConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://example.com/consts/v1", testConsts.ConstsSchema)
	assert.Equal(t, 2, testConsts.ConstsVersion)
	assert.InDelta(t, 0.5, testConsts.ConstsRatio, 0)
	assert.True(t, testConsts.ConstsEnabled)
	assert.Equal(t, "fixed", testConsts.ConstsUntyped)

	// A value made of the constants passes the const checks.
	out, err := json.Marshal(testConsts.Consts{
		Schema:  ptr(testConsts.ConstsSchema),
		Version: ptr(testConsts.ConstsVersion),
		Ratio:   ptr(testConsts.ConstsRatio),
		Enabled: ptr(testConsts.ConstsEnabled),
		Untyped: testConsts.ConstsUntyped,
		Label:   ptr(testConsts.ConstsLabel_1),
	})
	require.NoError(t, err)

	var back testConsts.Consts
	require.NoError(t, json.Unmarshal(out, &back))

	// The type ConstsLabel has the constant's name, which is suffixed, and
	// reported.
	assert.Equal(t, "x", testConsts.ConstsLabel_1)
	assert.Contains(t, strings.Join(generateWithWarnerCapture(t, "./data/constConstants/consts/consts.json"), "\n"),
		`constant name "ConstsLabel" is already declared in this package; declaring it as "ConstsLabel_1" instead`)
}
