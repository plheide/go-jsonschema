package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/sosodev/duration"
	"gopkg.in/yaml.v3"
)

var (
	ErrDurationNotISO8601  = errors.New("not an ISO 8601 duration")
	ErrDurationOutOfRange  = errors.New("duration out of range")
	ErrDurationNotJSON     = errors.New("cannot parse a JSON value that is neither a string nor a number as a duration")
	ErrDurationNotYAMLText = errors.New("cannot parse a YAML value that is not a string as a duration")
)

// Duration is a time.Duration held as an ISO 8601 duration, such as "PT1H30M",
// which is what JSON Schema's format "duration" describes.
//
// JSON gives it as such a string, or as a number of nanoseconds, the way
// encoding/json reads a time.Duration. YAML gives it as such a string, or in
// Go's syntax, such as "1h30m", the way yaml.v3 reads a time.Duration. A year
// counts as 365 days and a month as a twelfth of that. It is written as an ISO
// 8601 duration of hours, minutes and seconds, which hold it exactly, such as
// "PT36H" for a day and a half.
type Duration struct {
	time.Duration
}

// jsonNull is the JSON null, which a value's UnmarshalJSON leaves the value
// unchanged for, as encoding/json does.
const jsonNull = "null"

func (d Duration) MarshalJSON() ([]byte, error) {
	// The duration holds no character that JSON escapes.
	return []byte(`"` + formatISO8601(d.Duration) + `"`), nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	if string(data) == jsonNull {
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, err := parseISO8601(text)
		if err != nil {
			return err
		}

		d.Duration = parsed

		return nil
	}

	var nanoseconds int64
	if err := json.Unmarshal(data, &nanoseconds); err != nil {
		return fmt.Errorf("%w: %s", ErrDurationNotJSON, data)
	}

	d.Duration = time.Duration(nanoseconds)

	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return formatISO8601(d.Duration), nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode || value.ShortTag() != "!!str" {
		return fmt.Errorf("%w: %s", ErrDurationNotYAMLText, value.Value)
	}

	parsed, err := parseISO8601(value.Value)
	if errors.Is(err, ErrDurationNotISO8601) {
		goSyntax, goErr := time.ParseDuration(value.Value)
		if goErr != nil {
			return fmt.Errorf("%w, nor one in Go's syntax: %q", ErrDurationNotISO8601, value.Value)
		}

		parsed, err = goSyntax, nil
	}

	if err != nil {
		return err
	}

	d.Duration = parsed

	return nil
}

// Hours of the units an ISO 8601 duration counts in, as sosodev/duration
// converts them.
const (
	hoursPerDay   = 24
	hoursPerWeek  = 7 * hoursPerDay
	hoursPerYear  = 365 * hoursPerDay
	hoursPerMonth = hoursPerYear / 12
)

// parseISO8601 reads text as an ISO 8601 duration.
func parseISO8601(text string) (time.Duration, error) {
	parsed, err := duration.Parse(text)

	// Parse takes "P" and "PT" on their own, which give no duration at all.
	if err != nil || !strings.ContainsAny(text, "0123456789") {
		return 0, fmt.Errorf("%w: %q", ErrDurationNotISO8601, text)
	}

	hours := parsed.Years*hoursPerYear + parsed.Months*hoursPerMonth + parsed.Weeks*hoursPerWeek +
		parsed.Days*hoursPerDay + parsed.Hours + parsed.Minutes/60 + parsed.Seconds/3600

	// Converting a float64 beyond an int64 to one gives no defined value.
	if hours*float64(time.Hour) >= math.MaxInt64 {
		return 0, fmt.Errorf("%w: %q", ErrDurationOutOfRange, text)
	}

	return parsed.ToTimeDuration(), nil
}

// formatISO8601 writes d as an ISO 8601 duration of hours, minutes and
// seconds, which hold it exactly: PT0S for zero, and a minus sign before a
// negative one.
func formatISO8601(d time.Duration) string {
	if d == 0 {
		return "PT0S"
	}

	var b strings.Builder

	// The magnitude of the smallest duration is past an int64, so it is
	// negated as a uint64, in two's complement.
	magnitude := uint64(d) //nolint:gosec // A negative d is negated below.
	if d < 0 {
		b.WriteString("-")

		magnitude = -magnitude
	}

	b.WriteString("PT")

	hours, rest := magnitude/uint64(time.Hour), magnitude%uint64(time.Hour)
	minutes, rest := rest/uint64(time.Minute), rest%uint64(time.Minute)
	seconds, nanoseconds := rest/uint64(time.Second), rest%uint64(time.Second)

	if hours > 0 {
		b.WriteString(strconv.FormatUint(hours, 10) + "H")
	}

	if minutes > 0 {
		b.WriteString(strconv.FormatUint(minutes, 10) + "M")
	}

	if seconds > 0 || nanoseconds > 0 {
		b.WriteString(strconv.FormatUint(seconds, 10))

		if nanoseconds > 0 {
			b.WriteString("." + strings.TrimRight(fmt.Sprintf("%09d", nanoseconds), "0"))
		}

		b.WriteString("S")
	}

	return b.String()
}
