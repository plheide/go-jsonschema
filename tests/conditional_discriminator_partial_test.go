package tests_test

import (
	"testing"

	testCondDiscNarrowedConst "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/narrowedConst"
	testCondDiscNarrowedEnumMulti "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/narrowedEnumMulti"
	testCondDiscPartlyCompiled "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/partlyCompiled"
	testCondDiscShapes "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/shapes"
	testCondDiscThenConstraints "github.com/plheide/go-jsonschema/tests/data/conditionalDiscriminator/thenConstraints"
)

// TestConditionalDiscriminatorPartlyCompiled covers clauses that compile
// keyword by keyword: a then or else checks its required, a not of required
// and the properties it narrows to some strings, whatever else it declares,
// and a clause that compiles into no check leaves the others checked. Each
// input is decoded as JSON and as YAML.
func TestConditionalDiscriminatorPartlyCompiled(t *testing.T) {
	t.Parallel()

	t.Run("a branch that narrows an enum no longer keeps the others from compiling, and a not is checked", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscShapes.Shapes](t, []decodeCase{
			{`{"rule": {"kind": "delay", "delaySec": 5}, "window": {"intervalSec": 60}}`, ""},
			{`{"rule": {"kind": "compare", "operator": "GT", "value": 1}, "window": {"intervalSec": 60}}`, ""},
			{
				`{"rule": {"kind": "delay"}, "window": {"intervalSec": 60}}`,
				"field delaySec in Rule (when kind='delay'): required",
			},
			{
				`{"rule": {"kind": "compare", "operator": "AND", "value": 1}, "window": {"intervalSec": 60}}`,
				"field operator in Rule (when kind='compare'): must be one of [GT, LT]",
			},
			{
				`{"rule": {"kind": "compare", "value": 1}, "window": {"intervalSec": 60}}`,
				"field operator in Rule (when kind='compare'): required",
			},
			// Only kind=compare narrows the operator.
			{`{"rule": {"kind": "delay", "delaySec": 5, "operator": "OR"}, "window": {"intervalSec": 60}}`, ""},
			// Window's own not forbids count and untilSec together.
			{
				`{"rule": {"kind": "delay", "delaySec": 5}, "window": {"intervalSec": 60, "count": 3, "untilSec": 600}}`,
				"fields count, untilSec in Window: must not all be present",
			},
			{`{"rule": {"kind": "delay", "delaySec": 5}, "window": {"intervalSec": 60, "count": 3}}`, ""},
		})
	})

	t.Run("a then checks its required beside a bound it does not check", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscThenConstraints.ThenConstraints](t, []decodeCase{
			{`{"kind": "a", "x": 7}`, ""},
			{`{"kind": "a"}`, "field x in ThenConstraints (when kind='a'): required"},
			// The bound is reported, not checked.
			{`{"kind": "a", "x": 1}`, ""},
			{`{"kind": "b"}`, ""},
		})
	})

	t.Run("clauses that compile into no check leave the others checked", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscPartlyCompiled.PartlyCompiled](t, []decodeCase{
			{`{"kind": "a", "x": 5}`, ""},
			{`{"kind": "a"}`, "field x in PartlyCompiled (when kind='a'): required"},
			{`{"kind": "c", "y": "v"}`, ""},
			{`{"kind": "c"}`, "field y in PartlyCompiled (when kind='c'): required"},
			{`{"kind": "c", "y": "v", "x": 1}`, "field x in PartlyCompiled (when kind='c'): must not be present"},
			// allOf[1]'s $ref, allOf[2] and allOf[3] are reported, not checked.
			{`{"kind": "b"}`, ""},
			{`{"kind": "a", "x": 10}`, ""},
			{`{"kind": "a", "x": 5, "mode": "fast"}`, ""},
		})
	})

	t.Run("a then and an else narrow a property to a const and to an enum", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscNarrowedConst.NarrowedConst](t, []decodeCase{
			{`null`, ""},
			{`{"kind": "time", "unit": "s"}`, ""},
			{`{"kind": "time"}`, ""},
			{`{"kind": "time", "unit": "ms"}`, "field unit in NarrowedConst (when kind='time'): must be s"},
			{`{"kind": "length", "unit": "km"}`, ""},
			{`{"kind": "length", "unit": "s"}`, "field unit in NarrowedConst (when kind!='time'): must be one of [m, km]"},
			// An absent kind makes the if true, vacuously, so its then applies.
			{`{"unit": "s"}`, ""},
			{`{"unit": "m"}`, "field unit in NarrowedConst (when kind is absent): must be s"},
		})
	})

	t.Run("a branch keyed on several values names the one that matched", func(t *testing.T) {
		t.Parallel()

		checkDecode[testCondDiscNarrowedEnumMulti.NarrowedEnumMulti](t, []decodeCase{
			{`{"kind": "sum", "op": "add"}`, ""},
			{`{"kind": "count", "op": "inc"}`, ""},
			{`{"kind": "count", "op": "cmp"}`, "field op in NarrowedEnumMulti (when kind='count'): must be one of [add, inc]"},
			{`{"kind": "sum"}`, "field op in NarrowedEnumMulti (when kind='sum'): required"},
			{`{"kind": "max", "op": "cmp"}`, ""},
			{`{"kind": "max", "op": "add"}`, "field op in NarrowedEnumMulti (when kind not in [sum, count]): must be cmp"},
		})
	})
}
