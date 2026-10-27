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

	// An integer beyond 2^53 is written as the schema writes it, which a
	// float64 would round to 9007199254740992.
	assert.Equal(t, 9007199254740993, testConsts.ConstsBuild)

	// A value made of the constants passes the const checks.
	out, err := json.Marshal(testConsts.Consts{
		Schema:  ptr(testConsts.ConstsSchema),
		Version: ptr(testConsts.ConstsVersion),
		Ratio:   ptr(testConsts.ConstsRatio),
		Enabled: ptr(testConsts.ConstsEnabled),
		Untyped: testConsts.ConstsUntyped,
		Build:   ptr(testConsts.ConstsBuild),
		Label:   ptr(testConsts.ConstsLabel_1),
	})
	require.NoError(t, err)

	var back testConsts.Consts
	require.NoError(t, json.Unmarshal(out, &back))

	// The type ConstsLabel has the constant's name, which is suffixed, and
	// reported. A number that no Go value holds exactly has no constant.
	warnings := strings.Join(generateWithWarnerCapture(t, "./data/constConstants/consts/consts.json"), "\n")

	assert.Equal(t, "x", testConsts.ConstsLabel_1)
	assert.Contains(t, warnings,
		`constant name "ConstsLabel" is already declared in this package; declaring it as "ConstsLabel_1" instead`)
	assert.Contains(t, warnings, `Const of Consts property "precise": no Go value holds `+
		`0.1000000000000000000001 exactly, so no constant is declared for it`)
}
