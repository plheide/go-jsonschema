package tests_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/plheide/go-jsonschema/pkg/generator"
)

// TestFidelityWarningsBehavior verifies that the schema-fidelity warning
// emitted when a schema declares enforcement-implying keywords but degrades
// to interface{} fires for the right schemas and stays silent for genuinely
// open schemas.
//
// Note on the ifThenSilentDrop fixture: its allOf deliberately carries a
// `{"type": "string"}` member alongside the if/then branches. Upstream
// (omissis/go-jsonschema#590) made isPrimitiveTypeList require at least one
// member to contribute a real primitive type, so an allOf of *only* typeless
// conditionals no longer collapses the surrounding object — it now generates a
// proper struct, and this warning correctly stays silent for it. The primitive
// member is what still drives MergeTypes down its primitive short circuit,
// dropping the parent's object shape and producing the interface{} fallback
// this test is about. Do not remove it as redundant: without it the schema
// generates cleanly and the test no longer exercises the warning at all.
func TestFidelityWarningsBehavior(t *testing.T) {
	t.Parallel()

	t.Run("if/then with additionalProperties:false + required emits a warning", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t, "./data/fidelityWarnings/ifThenSilentDrop/ifThenSilentDrop.json")
		joined := strings.Join(warnings, "\n")

		require.NotEmpty(t, warnings, "expected at least one warning for if/then schema with enforcement keywords")
		assert.Contains(t, joined, "schema fidelity:")
		assert.Contains(t, joined, "if/then/else not compiled")
		assert.Contains(t, joined, "additionalProperties: false")
		assert.Contains(t, joined, "required")
		assert.Contains(t, joined, "declared property(ies)")
	})

	t.Run("constrained oneOf variant warns and names the keyword", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t,
			"./data/fidelityWarnings/oneOfConstrainedVariant/oneOfConstrainedVariant.json")
		joined := strings.Join(warnings, "\n")

		// The primitive wrapper dispatches on JSON token kind, so a variant
		// carrying a constraint the wrapper cannot enforce disqualifies the
		// whole schema and it degrades to interface{}. Before this warning
		// existed that was entirely silent, which is how it went unnoticed in
		// production schemas.
		require.NotEmpty(t, warnings)
		assert.Contains(t, joined, "schema fidelity:")
		assert.Contains(t, joined, "StillRejected")
		assert.Contains(t, joined, "primitive wrapper cannot enforce")
		assert.Contains(t, joined, "string length constraint")
	})

	t.Run("a temporal format is a type mapping, not a constraint, and stays silent", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t,
			"./data/fidelityWarnings/oneOfConstrainedVariant/oneOfConstrainedVariant.json")

		// `format: date-time` on a string variant maps the branch to time.Time
		// rather than declaring a constraint the wrapper must enforce, so the
		// schema still compiles to a real wrapper. Guards the boundary against
		// the disqualifying case above: only one of the two degrades.
		for _, w := range filterFidelityWarnings(warnings) {
			assert.NotContains(t, w, "TimeOrNumber",
				"a temporal format compiles to a time.Time branch and must not warn")
		}
	})

	t.Run("an unconstrained oneOf compiles and stays silent", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t,
			"./data/fidelityWarnings/oneOfConstrainedVariant/oneOfConstrainedVariant.json")

		// Same file: the sibling property has no constraints, gets a real
		// wrapper, and must not be reported. One field degrades, not two.
		for _, w := range filterFidelityWarnings(warnings) {
			assert.NotContains(t, w, "Unconstrained",
				"the unconstrained variant compiles to a wrapper and must not warn")
		}
	})

	t.Run("anyOf mixing object and string branches falls back and warns", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t, "./data/core/anyOf/anyOfRefToString.json")
		joined := strings.Join(filterFidelityWarnings(warnings), "\n")

		assert.Contains(t, joined, "anyOf with both object and non-object branches")
		assert.Contains(t, joined, "declared property(ies)")
	})

	t.Run("a pattern Go's regexp cannot compile is reported and left unchecked", func(t *testing.T) {
		t.Parallel()

		warnings := filterFidelityWarnings(
			generateWithWarnerCapture(t, "./data/validation/patternUnsupported/patternUnsupported.json"))
		joined := strings.Join(warnings, "\n")

		for _, owner := range []string{"lookahead", "lookaheadMinLength", "RepeatedCode"} {
			assert.Contains(t, joined, `of "`+owner+`" is not checked: Go's regexp cannot compile it`)
		}

		assert.NotContains(t, joined, `of "checked"`, "a pattern RE2 compiles is checked, not reported")
		assert.Len(t, warnings, 3)
	})

	t.Run("anyOf with the non-object branch first falls back and warns too", func(t *testing.T) {
		t.Parallel()

		// The merge used to take the first branch's kind, so these became
		// *string, []string and []string, and the root a string, each
		// rejecting every object.
		for schema, want := range map[string]int{
			"./data/core/anyOf/anyOfPrimitiveFirst.json":     3,
			"./data/core/anyOf/anyOfPrimitiveFirstRoot.json": 1,
		} {
			var fellBack int

			for _, w := range filterFidelityWarnings(generateWithWarnerCapture(t, schema)) {
				if strings.Contains(w, "anyOf with both object and non-object branches") {
					fellBack++
				}
			}

			assert.Equal(t, want, fellBack, schema)
		}
	})

	t.Run("a oneOf of required-only branches on a struct is checked, not reported", func(t *testing.T) {
		t.Parallel()

		// It is a presence count, which the struct checks (TestPresenceCount).
		warnings := generateWithWarnerCapture(t, "./data/presenceCount/oneOfRequiredOnly/oneOfRequiredOnly.json")

		assert.Empty(t, filterFidelityWarnings(warnings))
	})

	t.Run("a conditional the allOf merge drops is reported", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t,
			"./data/fidelityWarnings/conditionalVacuousIf/conditionalVacuousIf.json")

		assert.Contains(t, strings.Join(filterFidelityWarnings(warnings), "\n"),
			"does not enforce all it declares: if/then/else not compiled (in allOf branch)")
		assert.Contains(t, strings.Join(warnings, "\n"),
			"must be listed in `required`, or in the `required` of every `if`")
	})

	t.Run("a conditional no validator attaches to is reported", func(t *testing.T) {
		t.Parallel()

		// The conditional discriminator's validator attaches only to a
		// declared type's struct. A oneOf holder checks no conditional of its
		// parent's, and the elements of a declared array are generated inline.
		for fixture, want := range map[string][]string{
			"ownConditionalOnOneOf": {
				"schema fidelity: type OwnConditionalOnOneOf does not enforce all it declares: if/then/else not compiled",
			},
			"conditionalInArrayItems": {
				"schema fidelity: type OwnListElem does not enforce all it declares: if/then/else not compiled",
				"schema fidelity: type AllOfListElem does not enforce all it declares: " +
					"if/then/else not compiled (in allOf branch)",
			},
		} {
			warnings := filterFidelityWarnings(generateWithWarnerCapture(t,
				"./data/fidelityWarnings/"+fixture+"/"+fixture+".json"))

			assert.ElementsMatch(t, want, warnings, fixture)
		}
	})

	t.Run("a conditional whose if requires the optional discriminator is checked, not reported", func(t *testing.T) {
		t.Parallel()

		// The if is false when the discriminator is absent, which the
		// conditional discriminator compiles.
		warnings := generateWithWarnerCapture(t,
			"./data/conditionalDiscriminator/requiredInIf/requiredInIf.json")

		assert.Empty(t, warnings)
	})

	t.Run("a $ref branch dropped with its oneOf is named", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t, "./data/fidelityWarnings/oneOfRefOrNull/oneOfRefOrNull.json")
		joined := strings.Join(filterFidelityWarnings(warnings), "\n")

		assert.Contains(t, joined, "falls back to interface{} due to oneOf not compiled")
		assert.Contains(t, joined, "$ref #/definitions/pair")
	})

	t.Run("a oneOf branch whose $ref leads nowhere is reported", func(t *testing.T) {
		t.Parallel()

		// As for an anyOf or allOf, each $ref is reported with the reason a
		// property's $ref fails with: here a missing definition, and a
		// missing file inside a branch's allOf. Before, the oneOf fell back
		// to a map without a word, so a schema whose $ref did not load
		// generated next to nothing, successfully.
		joined := strings.Join(generateWithWarnerCapture(t,
			"./data/fidelityWarnings/oneOfUnresolvableRef/oneOfUnresolvableRef.json"), "\n")

		assert.Contains(t, joined, `Could not resolve ref "#/definitions/Missing": `+
			`definition does not exist in schema: "Missing" (from ref "#/definitions/Missing")`)
		assert.Contains(t, joined, `Could not resolve ref "missing.json#/definitions/B": `+
			`could not follow $ref "missing.json#/definitions/B" to file "missing.json"`)
		assert.Contains(t, joined, "type OneOfUnresolvableRef does not enforce all it declares: "+
			"oneOf of 2 branches not compiled")
	})

	t.Run("a oneOf that falls back to a map is reported", func(t *testing.T) {
		t.Parallel()

		// Under type: object, a oneOf no strategy compiles leaves a map when
		// the schema has no properties of its own. The map checks no branch,
		// any more than the struct does that the same oneOf leaves beside
		// properties.
		const (
			schema = "./data/fidelityWarnings/oneOfObjectMap/oneOfObjectMap.json"
			want   = "schema fidelity: type OneOfObjectMap does not enforce all it declares: " +
				"oneOf of 2 branches not compiled, so a value matching none or several of them passes"
		)

		assert.Contains(t, generateWithWarnerCapture(t, schema), want)

		// Left undeclared, the map is reported all the same.
		cfg := basicConfig
		cfg.DisableCustomTypesForMaps = true

		assert.Contains(t, generateWithConfigAndWarnerCapture(t, cfg, schema), want)
	})

	t.Run("conditionals and negations no code path compiles are reported once each", func(t *testing.T) {
		t.Parallel()

		warnings := filterFidelityWarnings(generateWithWarnerCapture(t,
			"./data/fidelityWarnings/uncompiledConditionals/uncompiledConditionals.json"))

		assert.ElementsMatch(t, []string{
			"schema fidelity: type UncompiledConditionals does not enforce all it declares: " +
				"if/then/else not compiled",
			"schema fidelity: type UncompiledConditionalsChoice_0 does not enforce all it declares: " +
				`"not" keyword not compiled`,
			"schema fidelity: type UncompiledConditionalsCode falls back to interface{} due to " +
				"if/then/else not compiled (in allOf branch); declared but not enforced: if/then/else (in allOf branch)",
			"schema fidelity: type UncompiledConditionalsEither falls back to interface{} due to " +
				`oneOf not compiled; declared but not enforced: "not" (in oneOf branch)`,
			"schema fidelity: type UncompiledConditionalsLegacy does not enforce all it declares: " +
				"false schema not compiled, so any value passes",
			"schema fidelity: type UncompiledConditionalsMerged does not enforce all it declares: " +
				"if/then/else not compiled (in allOf branch)",
			"schema fidelity: type UncompiledConditionalsName does not enforce all it declares: " +
				`"not" keyword not compiled`,
			"schema fidelity: type UncompiledConditionalsStatus does not enforce all it declares: " +
				`"not" keyword not compiled`,
			"schema fidelity: type UncompiledConditionalsValue does not enforce all it declares: " +
				`"not" keyword not compiled (in oneOf branch)`,
			"schema fidelity: type NamesElem does not enforce all it declares: " +
				`"not" keyword not compiled`,
			"schema fidelity: type Shape does not enforce all it declares: " +
				"if/then/else not compiled",
		}, warnings)
	})

	t.Run("only models enforce nothing, so nothing is reported as not enforced", func(t *testing.T) {
		t.Parallel()

		cfg := basicConfig
		cfg.OnlyModels = true

		// A type that falls back to interface{} is still reported: its shape
		// is lost whether or not anything is enforced.
		var notEnforced []string

		for _, w := range filterFidelityWarnings(generateWithConfigAndWarnerCapture(t, cfg,
			"./data/fidelityWarnings/uncompiledConditionals/uncompiledConditionals.json")) {
			if strings.Contains(w, "does not enforce all it declares") {
				notEnforced = append(notEnforced, w)
			}
		}

		assert.Empty(t, notEnforced)
	})

	t.Run("a declined conditional discriminator still reports what goes unchecked", func(t *testing.T) {
		t.Parallel()

		warnings := filterFidelityWarnings(generateWithWarnerCapture(t,
			"./data/conditionalDiscriminator/thenConstraints/thenConstraints.json"))

		assert.Equal(t, []string{
			"schema fidelity: type ThenConstraints does not enforce all it declares: " +
				"if/then/else not compiled (in allOf branch)",
		}, warnings, "the decline says why; the fidelity warning says what goes unchecked")
	})

	t.Run("conditionals that lose nothing stay silent", func(t *testing.T) {
		t.Parallel()

		warnings := filterFidelityWarnings(generateWithWarnerCapture(t,
			"./data/fidelityWarnings/vacuousConditionals/vacuousConditionals.json"))

		assert.Empty(t, warnings, "an if alone, a vacuous then and else, and a compiled discriminator lose nothing")
	})

	t.Run("schema with no enforcement-implying keywords stays silent", func(t *testing.T) {
		t.Parallel()

		warnings := generateWithWarnerCapture(t, "./data/fidelityWarnings/openSchemaNoWarn/openSchemaNoWarn.json")

		fidelityWarnings := filterFidelityWarnings(warnings)
		assert.Empty(t, fidelityWarnings, "expected no fidelity warnings for an open schema, got: %v", fidelityWarnings)
	})
}

// generateWithWarnerCapture runs the generator on a single schema with a
// warner that records every emitted message. Returns the captured slice.
func generateWithWarnerCapture(t *testing.T, schemaPath string) []string {
	t.Helper()

	return generateWithConfigAndWarnerCapture(t, basicConfig, schemaPath)
}

// generateWithConfigAndWarnerCapture is generateWithWarnerCapture with cfg in
// place of basicConfig.
func generateWithConfigAndWarnerCapture(t *testing.T, cfg generator.Config, schemaPath string) []string {
	t.Helper()

	abs, err := filepath.Abs(schemaPath)
	require.NoError(t, err)

	var (
		mu       sync.Mutex
		captured []string
	)

	cfg.Warner = func(msg string) {
		mu.Lock()
		defer mu.Unlock()

		captured = append(captured, msg)
	}

	gen, err := generator.New(cfg)
	require.NoError(t, err)
	require.NoError(t, gen.DoFile(abs))

	mu.Lock()
	defer mu.Unlock()

	out := make([]string, len(captured))
	copy(out, captured)

	return out
}

// filterFidelityWarnings returns only the messages that look like fidelity
// warnings, ignoring incidental warnings that may come from other paths.
func filterFidelityWarnings(msgs []string) []string {
	var out []string

	for _, m := range msgs {
		if strings.HasPrefix(m, "schema fidelity:") {
			out = append(out, m)
		}
	}

	return out
}
