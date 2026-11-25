package tests_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	testPresenceDeps07 "github.com/plheide/go-jsonschema/tests/data/presenceConditional/dependenciesDraft07"
	testPresenceDependent "github.com/plheide/go-jsonschema/tests/data/presenceConditional/dependentSchemas"
	testPresenceIf "github.com/plheide/go-jsonschema/tests/data/presenceConditional/ifRequired"
)

// TestPresenceConditional decodes objects whose subschemas apply where some of
// their properties are present: dependentSchemas, a draft-07 dependencies and
// an if that only lists required. Each check reads the input as written, from
// JSON and from YAML.
func TestPresenceConditional(t *testing.T) {
	t.Parallel()

	t.Run("dependentSchemas", func(t *testing.T) {
		t.Parallel()

		checkDecode[testPresenceDependent.DependentSchemas](t, []decodeCase{
			// A null passes, as it passes the required checks.
			{`null`, ""},
			{`{}`, ""},
			{`{"card": "c", "billing": "b"}`, ""},
			{`{"card": "c"}`, "field billing in DependentSchemas (when card is present): required"},
			{`{"billing": "b"}`, ""},
			{`{"quiet": true, "level": "low"}`, "field level in DependentSchemas (when quiet is present): must not be present"},
			{`{"mode": "m", "level": "low"}`, ""},
			{`{"mode": "m", "level": "mid"}`, "field level in DependentSchemas (when mode is present): must be one of [low, high]"},
			{`{"level": "mid"}`, ""},
			{`{"retries": 1, "mode": "m"}`, "field mode in DependentSchemas (when retries is present): must not be present"},
			{`{"unique": true, "repeatable": true}`, ""},
			{`{"unique": true, "repeatable": false}`, "field repeatable in DependentSchemas (when unique is present): must be true"},
			{`{"repeatable": false}`, ""},
			{`{"code": "abc"}`, ""},
			{`{"code": "ab"}`, "field code in DependentSchemas (when code is present) length: must be >= 3"},
			{`{"code": "ABC"}`, "field code in DependentSchemas (when code is present) pattern match: must match ^[a-z]+$"},
			{`{"proxy": "p", "host": "h", "port": 8080}`, ""},
			{`{"proxy": "p"}`, "field host in DependentSchemas (when proxy is present): required"},
			{`{"proxy": "p", "host": "h", "port": 0}`, "field port in DependentSchemas (when proxy is present): must be >= 1"},
			{`{"port": 0}`, ""},
			{`{"token": "t", "user": "u"}`, ""},
			{
				`{"token": "t", "user": "u", "host": "h"}`,
				"DependentSchemas (when token is present): exactly one of [user], [host] must be present, got 2",
			},
			{`{"token": "t"}`, "DependentSchemas (when token is present): exactly one of [user], [host] must be present, got 0"},
			{`{"label": "l"}`, "field code in DependentSchemas (when label is present): required"},
		})
	})

	// The end of a session is a duration, whose value may be 0; where a
	// session has one, the entry keyed on it asks for at least 1.
	t.Run("a nested bound on a property whose own type allows less", func(t *testing.T) {
		t.Parallel()

		checkDecode[testPresenceDependent.DependentSchemas](t, []decodeCase{
			{`{"end": {"value": 1}}`, ""},
			{`{"end": {"value": 2.5, "unit": "s"}}`, ""},
			{`{"end": {"value": 0}}`, "field end.value in DependentSchemas (when end is present): must be >= 1"},
			{`{"end": {"value": 0.5}}`, "field end.value in DependentSchemas (when end is present): must be >= 1"},
			{`{"end": {"value": "x"}}`, "field end.value in DependentSchemas (when end is present): must be of type number"},
		})

		// The duration itself, outside the entry, allows 0.
		checkDecode[testPresenceDependent.Duration](t, []decodeCase{
			{`{"value": 0}`, ""},
		})
	})

	t.Run("a draft-07 dependencies", func(t *testing.T) {
		t.Parallel()

		checkDecode[testPresenceDeps07.DependenciesDraft07](t, []decodeCase{
			{`{"a": "x", "b": "y"}`, ""},
			{`{"a": "x"}`, "field b in DependenciesDraft07: required when a is present"},
			{`{"b": "y", "c": 1.5}`, ""},
			{`{"b": "y", "c": 0}`, "field c in DependenciesDraft07 (when b is present): must be > 0"},
			{`{"b": "y", "c": 1.25}`, "field c in DependenciesDraft07 (when b is present): must be a multiple of"},
			{`{"c": 0}`, ""},
			{`{"d": "z"}`, "field d in DependenciesDraft07 (when d is present): must not be present"},
		})
	})

	t.Run("an if that only lists required", func(t *testing.T) {
		t.Parallel()

		checkDecode[testPresenceIf.IfRequired](t, []decodeCase{
			{`{"kind": "url", "anonymous": true}`, ""},
			{`{"kind": "url"}`, "field anonymous in IfRequired (when user, password are not all present): required"},
			{
				`{"kind": "url", "user": "u", "anonymous": false}`,
				"field anonymous in IfRequired (when user, password are not all present): must be true",
			},
			{`{"kind": "url", "user": "u", "password": "p"}`, ""},
			{
				`{"kind": "url", "user": "u", "password": "p", "anonymous": true}`,
				"field anonymous in IfRequired (when user, password are present): must be false",
			},
			{`{"kind": "file", "anonymous": true}`, "field path in IfRequired (when kind='file'): required"},
			{`{"kind": "url", "anonymous": true, "mirror": "m", "timeout": 2}`, ""},
			{`{"kind": "url", "anonymous": true, "mirror": "m"}`, "field timeout in IfRequired (when mirror is present): required"},
			{
				`{"kind": "url", "anonymous": true, "mirror": "m", "timeout": 0}`,
				"field timeout in IfRequired (when mirror is present): must be >= 1",
			},
		})
	})
}

// TestPresenceConditionalWarnings covers what the presence-keyed checks report:
// nothing for an entry they check in full, the keywords they leave out of one
// they check in part, and the entry they check nothing of, by name.
func TestPresenceConditionalWarnings(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		want    []string
	}{
		{"dependentSchemas", []string{
			"schema fidelity: type DependentSchemas does not enforce all it declares: dependentSchemas (extra) not checked",
			"schema fidelity: type DependentSchemas does not enforce all it declares: " +
				"dependentSchemas (label): minProperties not checked",
		}},
		{"dependenciesDraft07", nil},
		{"ifRequired", nil},
	} {
		warnings := generateWithWarnerCapture(t,
			"./data/presenceConditional/"+tc.fixture+"/"+tc.fixture+".json")

		assert.ElementsMatch(t, tc.want, filterFidelityWarnings(warnings), tc.fixture)
	}
}
