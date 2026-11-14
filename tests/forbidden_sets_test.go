package tests_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	testForbiddenAllOfNots "github.com/plheide/go-jsonschema/tests/data/forbiddenSets/allOfNots"
	testForbiddenInline "github.com/plheide/go-jsonschema/tests/data/forbiddenSets/inline"
	testForbiddenOwnNot "github.com/plheide/go-jsonschema/tests/data/forbiddenSets/ownNot"
	testForbiddenWithPresence "github.com/plheide/go-jsonschema/tests/data/forbiddenSets/withPresence"
)

// TestForbiddenSets covers a not of required on an object, of its own or in
// its allOf: `not: {required: [a, b]}` forbids having both a and b, and
// `not: {anyOf: [{required: [a]}, {required: [b]}]}` having either. Each input
// is decoded as JSON and as YAML.
func TestForbiddenSets(t *testing.T) {
	t.Parallel()

	t.Run("an object's own not forbids a set of properties", func(t *testing.T) {
		t.Parallel()

		checkDecode[testForbiddenOwnNot.OwnNot](t, []decodeCase{
			// A null passes, as it passes the required checks.
			{`null`, ""},
			{`{"intervalSec": 60}`, ""},
			{`{"intervalSec": 60, "count": 3}`, ""},
			{`{"intervalSec": 60, "untilSec": 600}`, ""},
			{`{"intervalSec": 60, "count": 3, "untilSec": 600}`, "fields count, untilSec in OwnNot: must not all be present"},
		})

		checkDecode[testForbiddenOwnNot.Legacy](t, []decodeCase{
			{`{"name": "x"}`, ""},
			{`{"oldName": "x"}`, "field oldName in Legacy: must not be present"},
		})

		checkDecode[testForbiddenOwnNot.Body](t, []decodeCase{
			{`{}`, ""},
			{`{"source": "s", "encoding": "e"}`, "fields source, encoding in Body: must not all be present"},
			{`{"content": "c", "encoding": "e"}`, ""},
			{`{"source": "s", "content": "c"}`, "fields source, content in Body: must not all be present"},
		})
	})

	t.Run("an allOf of nots forbids each set", func(t *testing.T) {
		t.Parallel()

		checkDecode[testForbiddenAllOfNots.AllOfNots](t, []decodeCase{
			{`{"a": "x", "c": "x"}`, ""},
			{`{"a": "x", "b": "x"}`, "fields a, b in AllOfNots: must not all be present"},
			{`{"c": "x", "d": "x"}`, "fields c, d in AllOfNots: must not all be present"},
			// The third not forbids a value, which is reported, not checked.
			{`{"mode": "off"}`, ""},
		})
	})

	t.Run("a not beside a presence count", func(t *testing.T) {
		t.Parallel()

		checkDecode[testForbiddenWithPresence.WithPresence](t, []decodeCase{
			{`{"id": "1", "tag": "t"}`, ""},
			{`{"id": "1", "tag": "t", "alias": "a"}`, "fields tag, alias in WithPresence: must not all be present"},
			{`{"tag": "t"}`, "WithPresence: exactly one of [id], [name] must be present, got 0"},
		})
	})

	t.Run("an inline object's not", func(t *testing.T) {
		t.Parallel()

		checkDecode[testForbiddenInline.Inline](t, []decodeCase{
			{`{"window": {"count": 3}}`, ""},
			{`{"window": {"count": 3, "untilSec": 600}}`, "fields count, untilSec in InlineWindow: must not all be present"},
		})
	})

	t.Run("nots that the struct does not check stay reported", func(t *testing.T) {
		t.Parallel()

		warnings := filterFidelityWarnings(generateWithWarnerCapture(t,
			"./data/forbiddenSets/stillWarned/stillWarned.json"))

		assert.ElementsMatch(t, []string{
			`schema fidelity: type BranchNot_0 does not enforce all it declares: "not" keyword not compiled`,
			`schema fidelity: type MergedNot does not enforce all it declares: "not" keyword not compiled`,
			`schema fidelity: type ValueNot does not enforce all it declares: "not" keyword not compiled`,
		}, warnings)

		warnings = filterFidelityWarnings(generateWithWarnerCapture(t,
			"./data/forbiddenSets/allOfNots/allOfNots.json"))

		assert.Equal(t, []string{
			`schema fidelity: type AllOfNots does not enforce all it declares: allOf[2]'s "not" not compiled`,
		}, warnings, "only the not that forbids a value is reported")

		for _, schema := range []string{
			"./data/forbiddenSets/ownNot/ownNot.json",
			"./data/forbiddenSets/inline/inline.json",
			"./data/forbiddenSets/withPresence/withPresence.json",
			"./data/conditionalDiscriminator/ownWithNot/ownWithNot.json",
		} {
			assert.Empty(t, filterFidelityWarnings(generateWithWarnerCapture(t, schema)), schema)
		}
	})
}
