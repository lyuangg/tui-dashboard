package source

import (
	"math"
	"strings"
	"time"
)

// Type is the kind of data a source produces, declared explicitly by the source with type: in
// the config.
//
// The type a source declares it produces, the one canonical format each type has (see Value)
// and the types a widget accepts are all written on the config surface.
type Type int

const (
	// TypeText is arbitrary text: the raw text only, no structure. It is the zero value and
	// shares it with an empty Value, so an emptiness test always goes through Value.Has or
	// Parsed.Has and never looks at Type.
	TypeText Type = iota
	// TypeNumber is one number. Accumulates across frames: one point per frame, timestamped by
	// the program.
	TypeNumber
	// TypeArray is a batch of numbers per frame with no time axis (the x-axis is the index), not
	// accumulated across frames: it expresses "this frame's N values", not a timeline.
	TypeArray
	// TypeTimeseries is a batch of (timestamp, value) per frame, with the time axis provided by
	// the script. Accumulates across frames.
	TypeTimeseries
	// TypeMap is named fields (a key-value object). Numeric fields each accumulate across
	// frames; a widget names the field with value:.
	TypeMap
	// TypeTable is a table of records: one record per row plus a column order. Not accumulated
	// across frames.
	TypeTable
	// TypeLogs is log lines. Appended across frames into a scrolling buffer (capacity log_cap).
	TypeLogs
)

// TypeNames holds every value that type: accepts; the order is the one shown in the docs and in
// error messages.
var TypeNames = []string{"text", "number", "array", "timeseries", "map", "table", "logs"}

// String returns the type name, for error messages and test assertions.
func (t Type) String() string {
	if int(t) >= 0 && int(t) < len(TypeNames) {
		return TypeNames[t]
	}
	return "unknown"
}

// ParseType converts a name from the config into a Type. Both the empty string and an unknown
// name are invalid: type: is required, with no fallback.
func ParseType(name string) (Type, bool) {
	for i, n := range TypeNames {
		if n == name {
			return Type(i), true
		}
	}
	return TypeText, false
}

// Point is one sampling point on a time series.
type Point struct {
	TS time.Time
	V  float64
}

// Value is a source's value for this frame, its shape decided by Type: only one field is
// meaningful per type, and several fields being non-empty at once is an invalid state that the
// producing side (the readers in parse.go) guarantees never occurs.
//
// array and timeseries share Points; they differ in whether the points carry a timestamp:
// timeseries gets its times from the script, while array's TS is always the zero value, meaning
// "this frame's N values, no time axis". A zero TS is a meaningful marker (the render layer's
// ui.timed uses it to pick the ordinal axis), not a missing field.
type Value struct {
	Type Type

	Num    float64          // number
	Points []Point          // timeseries (script timestamps) / array (TS always zero)
	Map    map[string]any   // map
	Keys   []string         // key order of a map (order of appearance, see keyValueLines)
	Rows   []map[string]any // table
	Cols   []string         // column order of a table
	Text   string           // text
	Logs   []LogLine        // logs
}

// Has reports whether this Value holds a value. TypeText is the zero value, so Type alone cannot
// tell "empty Value" from "empty text" and an emptiness test always goes through here; number is
// the exception and does not look at the payload (the number 0 is a valid value too).
func (v Value) Has() bool {
	return v.Type == TypeNumber || v.Text != "" || v.Points != nil ||
		v.Map != nil || v.Rows != nil || v.Logs != nil
}

// templateDoc lays the value out in a shape templates can reference directly (see ui.tplData):
// a map source is that object itself (readable as .source.field), and every other type gets one
// canonical key. These keys are an internal representation, not a user contract.
//
// Numbers are always output as float64, integral values included: the template
// {{printf "%.0f" .x}} accepts float64 only, and an int64 renders as %!f(int64=83).
func (v Value) templateDoc() map[string]any {
	switch v.Type {
	case TypeMap:
		return v.Map
	case TypeNumber:
		return map[string]any{"value": v.Num}
	case TypeArray:
		return map[string]any{"values": pointsToAny(v.Points)}
	case TypeTable:
		return map[string]any{"rows": recsToAny(v.Rows)}
	default:
		return nil // text / timeseries / logs: no field to name
	}
}

// pointsToAny lays the values of a point series out as []any (the template range and lookupAt go
// through the same any path). Timestamps do not enter the template surface: templates get a run
// of numbers, and the time axis is handled by the render layer.
func pointsToAny(pts []Point) []any {
	if pts == nil {
		return nil
	}
	out := make([]any, len(pts))
	for i, p := range pts {
		out[i] = p.V
	}
	return out
}

// floatsToPoints lays a run of numbers out in array's storage shape: TS is always the zero value
// (see Value). Empty input returns nil, because Has() uses Points != nil to tell whether there
// is a value, and a non-nil empty slice differs in meaning from nil.
func floatsToPoints(fs []float64) []Point {
	if fs == nil {
		return nil
	}
	pts := make([]Point, len(fs))
	for i, f := range fs {
		pts[i] = Point{V: f}
	}
	return pts
}

// parseTimestamp parses the timestamp at the head of a time-series line, supporting these forms:
//
//	Unix seconds (integer or fractional, e.g. 1757584000 / 1757584000.5)
//	Unix milliseconds (integer or fractional, e.g. 1757584000123)
//	"15:04:05" (falls on today)
//	"2006-01-02 15:04:05"
//	RFC3339 and its fractional-second form (a time zone is required: a bare
//	"2006-01-02T15:04:05" is not accepted)
//
// now fills in the forms that lack a date or a time zone (both 15:04:05 and a bare Unix number
// are interpreted in the local time zone).
func parseTimestamp(s string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if f, ok := parseFloat(s); ok {
		// Unix number. The unit follows from the magnitude, and the second and millisecond
		// windows are adjacent without overlapping: 1e11 is both a second value in the year 5138
		// and a millisecond value in 1973, so each gives up one end and both are open intervals.
		//
		// The thresholds keep an ordinary number from being mistaken for a time: timestamps
		// before 1973 do not occur in monitoring, and smaller integers are almost always data in
		// their own right. The millisecond window tops out at 1e14 (the year 5138), and
		// microseconds/nanoseconds are not accepted; the windows of those two (from 1e14 up) are
		// the territory of common numbers such as byte counts and nanosecond latencies.
		switch {
		case f > 1e8 && f < 1e11:
			return unixFloat(f, 1).Local(), true
		case f > 1e11 && f < 1e14:
			return unixFloat(f, 1e3).Local(), true
		}
		return time.Time{}, false
	}
	// A space-separated date and time of day: the first field is the date and is unrecognizable
	// on its own, so the caller must join the two fields before passing them in (see
	// leadingTimestamp).
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, true
		}
	}
	// A time of day without a date: today is filled in. Across midnight the apparent deviation
	// is at most one day, and no rollover inference is made.
	if t, err := time.ParseInLocation("15:04:05", s, now.Location()); err == nil {
		return time.Date(now.Year(), now.Month(), now.Day(),
			t.Hour(), t.Minute(), t.Second(), 0, now.Location()), true
	}
	return time.Time{}, false
}

// unixFloat converts a Unix time in units of unit (unit: 1=seconds, 1e3=milliseconds) into a
// time, keeping the fractional part as nanoseconds.
//
// The integer and fractional parts are split in the original unit before the integer division:
// dividing by unit first and splitting afterwards loses the last digit to float64 precision
// (around 1.7e12 this is enough to turn the .123 of 1757584000123 into .122).
func unixFloat(f, unit float64) time.Time {
	whole := math.Trunc(f)
	frac := f - whole
	sec := int64(whole) / int64(unit)
	nsec := (int64(whole) % int64(unit) * int64(1e9/unit)) +
		int64(math.Round(frac*1e9/unit))
	return time.Unix(sec, nsec)
}
