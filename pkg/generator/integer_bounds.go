package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/plheide/go-jsonschema/pkg/codegen"
)

// The bounds of an integer are numbers, which a schema may write with a
// fraction or an exponent, and of any size. A check on an integer reads them
// from the text the schema gives, so that a bound beyond 2^53 isn't rounded,
// and turns them into the inclusive bounds they put on an integer: a maximum
// of 2.5 lets 2 through, and an exclusive minimum of 2.5 lets 3 through. A
// bound that every value of the Go integer type meets needs no check.

// boundDigitsLimit is the length of the longest number that a bound is read
// from as written. A longer one is read from its float64.
const boundDigitsLimit = 400

// boundBeyond returns a number that stands for a bound past any Go integer.
func boundBeyond() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(40), nil)
}

// newNumericValidator returns the check of the bounds, multipleOf and const of
// the struct field f, whose value is of Go type goType.
func newNumericValidator(f codegen.StructField, goType string, isNillable bool) *numericValidator {
	s := f.SchemaType

	return &numericValidator{
		jsonName:             f.JSONName,
		fieldName:            f.Name,
		isNillable:           isNillable,
		multipleOf:           s.MultipleOf,
		maximum:              s.Maximum,
		exclusiveMaximum:     s.ExclusiveMaximum,
		minimum:              s.Minimum,
		exclusiveMinimum:     s.ExclusiveMinimum,
		constVal:             preciseConst(s),
		roundToInt:           strings.Contains(goType, "int"),
		goType:               goType,
		multipleOfText:       boundText(s.MultipleOf, s.MultipleOfExact),
		maximumText:          boundText(s.Maximum, s.MaximumExact),
		exclusiveMaximumText: exclusiveBoundText(s.ExclusiveMaximum, s.ExclusiveMaximumExact),
		minimumText:          boundText(s.Minimum, s.MinimumExact),
		exclusiveMinimumText: exclusiveBoundText(s.ExclusiveMinimum, s.ExclusiveMinimumExact),
	}
}

// boundText returns the number text of a bound, whose float64 is f: exact, if
// it holds the same number, or else f, written out.
func boundText(f *float64, exact json.Number) string {
	if f == nil {
		return ""
	}

	if g, err := strconv.ParseFloat(string(exact), 64); err == nil && g == *f {
		return string(exact)
	}

	return strconv.FormatFloat(*f, 'g', -1, 64)
}

// exclusiveBoundText returns the number text of an exclusive bound, as
// boundText does, or "" for a draft-04 boolean.
func exclusiveBoundText(x *any, exact json.Number) string {
	if x == nil {
		return ""
	}

	if f, ok := (*x).(float64); ok {
		return boundText(&f, exact)
	}

	return ""
}

// isTrue reports whether x is a draft-04 exclusive flag that is set.
func isTrue(x *any) bool {
	if x == nil {
		return false
	}

	b, ok := (*x).(bool)

	return ok && b
}

// boundValue returns the value of the decimal number text: exactly while its
// integer part has at most 40 digits and it is not closer to zero than
// 10^-40, and otherwise as ±10^40 or ±10^-41, which put the same bounds on an
// integer. It reports false for text that is not a decimal number.
func boundValue(text string) (*big.Rat, bool) {
	if len(text) > boundDigitsLimit {
		return nil, false
	}

	neg := strings.HasPrefix(text, "-")
	s := strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+")

	mantissa, exp := s, "0"
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exp = s[:i], s[i+1:]
	}

	intPart, frac, _ := strings.Cut(mantissa, ".")

	digits := intPart + frac
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return nil, false
	}

	e, err := strconv.Atoi(exp)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, false
	}

	e = max(-1e9, min(e, 1e9))

	significant := strings.TrimLeft(digits, "0")
	if significant == "" {
		return new(big.Rat), true
	}

	// The value is 0.significant × 10^point.
	point := len(intPart) - (len(digits) - len(significant)) + e

	var r *big.Rat

	switch {
	case point > 40:
		r = new(big.Rat).SetInt(boundBeyond())
	case point < -40:
		r = new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Mul(boundBeyond(), big.NewInt(10)))
	default:
		n, _ := new(big.Int).SetString(significant, 10)
		r = new(big.Rat).SetInt(n)

		shift := point - len(significant)
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(max(shift, -shift))), nil)

		if shift >= 0 {
			r.Mul(r, new(big.Rat).SetInt(scale))
		} else {
			r.Quo(r, new(big.Rat).SetInt(scale))
		}
	}

	if neg {
		r.Neg(r)
	}

	return r, true
}

// integerBound returns the inclusive bound that the number text puts on an
// integer, as a maximum when upper is set and a minimum otherwise, and as an
// exclusive one when exclusive is set.
func integerBound(text string, upper, exclusive bool) (*big.Int, bool) {
	r, ok := boundValue(text)
	if !ok {
		return nil, false
	}

	one := big.NewInt(1)

	if upper {
		// Euclidean division by the positive denominator rounds down.
		b := new(big.Int).Div(r.Num(), r.Denom())
		if exclusive && r.IsInt() {
			b.Sub(b, one)
		}

		return b, true
	}

	b := new(big.Int).Div(new(big.Int).Neg(r.Num()), r.Denom())
	b.Neg(b)

	if exclusive && r.IsInt() {
		b.Add(b, one)
	}

	return b, true
}

// integerRange returns the smallest and the largest value of the Go integer
// type goType. An int or a uint is taken to have 64 bits, as everywhere the
// generator sizes integers.
func integerRange(goType string) (*big.Int, *big.Int) {
	bits := 64

	switch strings.TrimPrefix(goType, "u") {
	case "int8":
		bits = 8
	case "int16":
		bits = 16
	case "int32":
		bits = 32
	}

	one := big.NewInt(1)

	if strings.HasPrefix(goType, "u") {
		return new(big.Int), new(big.Int).Sub(new(big.Int).Lsh(one, uint(bits)), one)
	}

	limit := new(big.Int).Lsh(one, uint(bits-1))

	return new(big.Int).Neg(limit), new(big.Int).Sub(limit, one)
}

// integerBoundCandidate is a bound on an integer as a schema writes it.
type integerBoundCandidate struct {
	text      string
	exclusive bool
}

// tightestIntegerBound returns the tightest bound that the validator's
// maximums put on an integer, when upper is set, or its minimums, as an
// inclusive bound and as written, or nil when it has none.
func (v *numericValidator) tightestIntegerBound(upper bool) (*big.Int, integerBoundCandidate) {
	candidates := []integerBoundCandidate{
		{v.minimumText, isTrue(v.exclusiveMinimum)},
		{v.exclusiveMinimumText, true},
	}
	if upper {
		candidates = []integerBoundCandidate{
			{v.maximumText, isTrue(v.exclusiveMaximum)},
			{v.exclusiveMaximumText, true},
		}
	}

	var (
		best      *big.Int
		bestBound integerBoundCandidate
	)

	for _, c := range candidates {
		if c.text == "" {
			continue
		}

		b, ok := integerBound(c.text, upper, c.exclusive)
		if !ok {
			continue
		}

		if best == nil || (upper && b.Cmp(best) < 0) || (!upper && b.Cmp(best) > 0) {
			best, bestBound = b, c
		}
	}

	return best, bestBound
}

// checksIntegerBound reports whether the validator's maximums, when upper is
// set, or its minimums need a check: whether some value of the Go integer
// type does not meet them.
func (v *numericValidator) checksIntegerBound(upper bool) bool {
	best, _ := v.tightestIntegerBound(upper)
	if best == nil {
		return false
	}

	lo, hi := integerRange(v.goType)
	if upper {
		return best.Cmp(hi) < 0
	}

	return best.Cmp(lo) > 0
}

// checksSomething reports whether the validator writes any check.
func (v *numericValidator) checksSomething() bool {
	if v.constVal != nil {
		return true
	}

	if v.roundToInt {
		_, multipleOf := v.integerMultipleOf()

		return multipleOf || v.checksIntegerBound(true) || v.checksIntegerBound(false)
	}

	if v.multipleOf != nil {
		return true
	}

	return v.maximum != nil || v.exclusiveMaximum != nil || v.minimum != nil || v.exclusiveMinimum != nil
}

// genIntegerBoundary writes the check of the bound on an integer that the
// validator's maximums put on it, when upper is set, or its minimums: the
// tightest of them.
func (v *numericValidator) genIntegerBoundary(
	out *codegen.Emitter, checkPointer, pointerPrefix, value, name string, upper bool,
) {
	best, bestBound := v.tightestIntegerBound(upper)
	if best == nil {
		return
	}

	lo, hi := integerRange(v.goType)
	sign, comp := ">=", ">"

	if upper {
		sign, comp = "<=", "<"
	}

	if bestBound.exclusive {
		sign = sign[:1]
	}

	// The error names the bound as written, or as an integer, if it is one.
	written, _ := boundValue(bestBound.text)

	message := strconv.Quote(bestBound.text)
	if written.IsInt() && written.Num().IsInt64() {
		message = written.Num().String()
	}

	switch {
	case upper && best.Cmp(hi) >= 0, !upper && best.Cmp(lo) <= 0:
		// Every value of the type meets the bound.
		return

	case upper && best.Cmp(lo) < 0, !upper && best.Cmp(hi) > 0:
		// No value of the type meets it: compare as a float64, which holds a
		// bound that the type can't.
		out.Printlnf(`if %sfloat64(%s%s) %s %s {`, checkPointer, pointerPrefix, value, mirror(comp), best)

	case written.IsInt() && written.Num().Cmp(lo) >= 0 && written.Num().Cmp(hi) <= 0:
		// An integer bound the type holds, compared as written.
		if bestBound.exclusive {
			comp += "="
		}

		out.Printlnf(`if %s%s %s %s%s {`, checkPointer, written.Num(), comp, pointerPrefix, value)

	default:
		out.Printlnf(`if %s%s %s %s%s {`, checkPointer, best, comp, pointerPrefix, value)
	}

	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: must be %s %%v", %s, %s)`, sign, name, message)
	out.Indent(-1)
	out.Printlnf("}")
}

// mirror returns the comparison that holds with its operands swapped.
func mirror(comp string) string {
	return map[string]string{"<": ">", ">": "<"}[comp]
}

// integerMultipleOf returns the number that the validator's multipleOf holds
// an integer to a multiple of, and false when it holds an integer to nothing.
// An integer is a multiple of p/q, in lowest terms, when it is a multiple of
// p: a multipleOf of 2.5 holds an integer to multiples of 5, and one of 0.5,
// or of any 1/q, holds it to nothing. JSON Schema requires a multipleOf
// greater than 0.
func (v *numericValidator) integerMultipleOf() (*big.Int, bool) {
	if v.multipleOf == nil {
		return nil, false
	}

	r, ok := boundValue(v.multipleOfText)
	if !ok || r.Sign() <= 0 || r.Num().Cmp(big.NewInt(1)) == 0 {
		return nil, false
	}

	return r.Num(), true
}

// genIntegerMultipleOf writes the check of the validator's multipleOf on an
// integer.
func (v *numericValidator) genIntegerMultipleOf(
	out *codegen.Emitter, checkPointer, pointerPrefix, value, name string,
) {
	p, ok := v.integerMultipleOf()
	if !ok {
		return
	}

	// The error names an int64 multipleOf as it always has, and any other as
	// written.
	message := fmt.Sprintf("%f", *v.multipleOf)
	if r, _ := boundValue(v.multipleOfText); !r.IsInt() || !r.Num().IsInt64() {
		message = strconv.Quote(v.multipleOfText)
	}

	if _, hi := integerRange(v.goType); p.Cmp(hi) > 0 {
		// Of the values of the type, only 0 is a multiple of a number past
		// them all.
		out.Printlnf(`if %s%s%s != 0 {`, checkPointer, pointerPrefix, value)
	} else {
		out.Printlnf(`if %s %s%s %% %s != 0 {`, checkPointer, pointerPrefix, value, p)
	}

	out.Indent(1)
	out.Printlnf(`return fmt.Errorf("field %%s: must be a multiple of %%v", %s, %s)`, name, message)
	out.Indent(-1)
	out.Printlnf("}")
}
