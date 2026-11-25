package tests_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	testDepReqBothDraft07 "github.com/plheide/go-jsonschema/tests/data/dependentRequired/bothDraft07"
	testDepReqBothModern "github.com/plheide/go-jsonschema/tests/data/dependentRequired/bothModern"
	testDepReqBothUndeclared "github.com/plheide/go-jsonschema/tests/data/dependentRequired/bothUndeclared"
	testDepReqDraft07 "github.com/plheide/go-jsonschema/tests/data/dependentRequired/draft07"
	testDepReqDraft07Keyword "github.com/plheide/go-jsonschema/tests/data/dependentRequired/draft07Keyword"
	testDepReqModern "github.com/plheide/go-jsonschema/tests/data/dependentRequired/modern"
	testDepReqModernLegacy "github.com/plheide/go-jsonschema/tests/data/dependentRequired/modernLegacy"
	testDepReqUndeclared "github.com/plheide/go-jsonschema/tests/data/dependentRequired/undeclared"
)

// TestDependentRequired covers properties that require others where they are
// present, through dependentRequired and a draft-07 dependencies, each checked
// in the drafts that define it. Each input is decoded as JSON and as YAML.
func TestDependentRequired(t *testing.T) {
	t.Parallel()

	t.Run("dependentRequired", func(t *testing.T) {
		t.Parallel()

		checkDecode[testDepReqModern.Modern](t, []decodeCase{
			// A null passes, as it passes the required checks.
			{`null`, ""},
			{`{}`, ""},
			{`{"name": "n"}`, ""},
			{`{"creditCard": "1", "billingAddress": "a", "street": "s", "city": "c"}`, ""},
			{`{"creditCard": "1"}`, "field billingAddress in Modern: required when creditCard is present"},
			{`{"billingAddress": "a", "street": "s"}`, "field city in Modern: required when billingAddress is present"},
		})
	})

	t.Run("a draft-07 dependencies of property lists", func(t *testing.T) {
		t.Parallel()

		checkDecode[testDepReqDraft07.Draft07](t, []decodeCase{
			{`{"shell": "sh", "run": "x"}`, ""},
			{`{"shell": "sh"}`, "field run in Draft07: required when shell is present"},
			// A schema dependency is checked as a presence-keyed clause.
			{`{"creditCard": "1"}`, "field billingAddress in Draft07 (when creditCard is present): required"},
			{`{"creditCard": "1", "billingAddress": "a"}`, ""},
		})
	})

	t.Run("a document that declares no draft checks both keywords", func(t *testing.T) {
		t.Parallel()

		checkDecode[testDepReqUndeclared.Legacy](t, []decodeCase{
			{`{"shell": "sh"}`, "field run in Legacy: required when shell is present"},
		})

		checkDecode[testDepReqUndeclared.Current](t, []decodeCase{
			{`{"shell": "sh"}`, "field run in Current: required when shell is present"},
		})
	})

	t.Run("an object that declares both keywords", func(t *testing.T) {
		t.Parallel()

		// A document that declares no draft checks both, merged per property.
		checkDecode[testDepReqBothUndeclared.BothUndeclared](t, []decodeCase{
			{`{"shell": "sh", "run": "x", "image": "i"}`, ""},
			{`{"shell": "sh", "image": "i"}`, "field run in BothUndeclared: required when shell is present"},
			{`{"shell": "sh", "run": "x"}`, "field image in BothUndeclared: required when shell is present"},
			{`{"creditCard": "1"}`, "field billingAddress in BothUndeclared: required when creditCard is present"},
		})

		assert.Empty(t, filterFidelityWarnings(generateWithWarnerCapture(t,
			"./data/dependentRequired/bothUndeclared/bothUndeclared.json")))

		// Draft-07 checks its dependencies alone, and 2020-12 dependentRequired.
		checkDecode[testDepReqBothDraft07.BothDraft07](t, []decodeCase{
			{`{"shell": "sh"}`, ""},
			{`{"creditCard": "1"}`, "field billingAddress in BothDraft07: required when creditCard is present"},
		})

		checkDecode[testDepReqBothModern.BothModern](t, []decodeCase{
			{`{"creditCard": "1"}`, ""},
			{`{"shell": "sh"}`, "field run in BothModern: required when shell is present"},
		})
	})

	t.Run("a keyword the declared draft does not define is not checked", func(t *testing.T) {
		t.Parallel()

		checkDecode[testDepReqDraft07Keyword.Draft07Keyword](t, []decodeCase{
			{`{"shell": "sh"}`, ""},
		})

		checkDecode[testDepReqModernLegacy.ModernLegacy](t, []decodeCase{
			{`{"shell": "sh"}`, ""},
		})

		for schema, want := range map[string]string{
			"draft07Keyword": "schema fidelity: type Draft07Keyword does not enforce all it declares: " +
				"dependentRequired (not in draft-07) not checked",
			"modernLegacy": "schema fidelity: type ModernLegacy does not enforce all it declares: " +
				"dependencies (not in 2020-12) not checked",
			// Where its property lists are checked, what a dependencies leaves
			// unchecked is named by property.
			"bothDraft07": "schema fidelity: type BothDraft07 does not enforce all it declares: " +
				"dependentRequired (not in draft-07), dependentSchemas (not in draft-07) not checked",
			"bothModern": "schema fidelity: type BothModern does not enforce all it declares: " +
				"dependencies (not in 2020-12) not checked",
			// A dependencies is named as written where a then leaves it unchecked.
			"inThen": "schema fidelity: type InThen does not enforce all it declares: " +
				"allOf[0]'s then: dependencies not checked",
		} {
			assert.Equal(t, []string{want}, filterFidelityWarnings(generateWithWarnerCapture(t,
				"./data/dependentRequired/"+schema+"/"+schema+".json")), schema)
		}
	})
}
