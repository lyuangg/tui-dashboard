// Package source manages data sources: one goroutine per source polls a script at its own
// interval and exposes the latest result to the UI in a thread-safe way.
package source

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LogLine is one log line.
type LogLine struct {
	// Time is the moment of this log line: the leading timestamp when the line carries one,
	// otherwise the sampling instant (frame time).
	Time  time.Time
	Level string // INFO / WARN / ERROR / DEBUG
	Msg   string
}

// Parsed is the result of parsing one output of a script.
type Parsed struct {
	// Val is the value produced this time (its shape is decided by Val.Type, see type.go).
	// It is invalid when Has is false.
	Val Value
	// Has reports whether this frame produced a value. There are two cases of false, and the
	// application layer handles both as "keep the previous successful value"; they differ only
	// in whether a log entry is written:
	//   - empty output: the whole Parsed is the zero value, not even Text is set;
	//   - mismatch: Text is set and Logs already carries one WARN.
	Has bool

	Logs []LogLine // log lines produced this time (the data of type: logs, or the WARN of a mismatch)
	Text string    // raw script output (whitespace and BOM trimmed); empty on empty output
}

// Opts is the full configuration needed to parse one output.
type Opts struct {
	Type   Type // data type declared by the source (required); it alone selects the reader
	Header bool // type: table only: is the first line a header (default true, set by config)
}

// numberRe matches bare-number text: optional sign, integer or decimal, optional exponent.
var numberRe = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?\s*$`)

// kvLineRe matches key-value lines: the key starts with a letter or underscore and may contain
// spaces and common punctuation (keys starting with a digit, such as 9:41, are rejected so that
// a log line is not taken for a key-value pair); the colon is followed by the value.
var kvLineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.\- ]*)\s*:\s*(.*)$`)

// ParseOutput parses one output of a script (raw) with the reader set selected by o.Type; there
// is no second input path:
//
//	text        the text is taken verbatim
//	number      the whole body is one number
//	array       one number per line (this frame's batch of values, no time axis)
//	timeseries  "<timestamp> <value>" per line; frame time fills in for bare values
//	map         "key: value" per line (flat scalars only; nested structures need their own source)
//	table       an aligned table, with or without a header
//	logs        one log line per line
//
// Output that is not one of these seven shapes (such as the "overview section + process table"
// of top(1)) must be converted to the canonical format by the script itself; the parse layer
// does no generic recognition and would take a comma that should not be split as a separator,
// producing a bogus table. A mismatch produces no value (Has=false), falls back to the raw text
// and records one WARN in Logs.
//
// Text is always the raw text of that output, and every type can be shown verbatim by a text
// widget. The returned error is currently always nil: recognition failure never reports an
// error; the signature is kept for a future strict validation that must fail hard.
func ParseOutput(raw []byte, ts time.Time, o Opts) (Parsed, error) {
	s := bytes.TrimSpace(raw)
	s = bytes.TrimPrefix(s, []byte("\xef\xbb\xbf")) // strip BOM
	if len(s) == 0 {
		return Parsed{}, nil // empty output: Text unset, so the application layer sees "no new content"
	}
	p, mismatch := parseBody(s, ts, o)
	p.Text = string(s)
	if mismatch != nil {
		// Mismatch: no half-parsed result is kept, so the application layer keeps the previous
		// successful value and only the raw text is swapped for this frame's.
		p.Val, p.Has = Value{}, false
		p.Logs = append(p.Logs, LogLine{
			Time: ts, Level: "WARN",
			Msg: fmt.Sprintf("type: %s does not match this output: %v; raw text kept", o.Type, mismatch),
		})
	}
	return p, nil
}

// parseBody resolves Val / Logs by type. A non-nil mismatch means this output does not match
// the canonical format of the type; Text is filled in by ParseOutput.
func parseBody(s []byte, ts time.Time, o Opts) (Parsed, error) {
	if val, ok := readText(o, string(s), ts); ok {
		return Parsed{Val: val, Has: true}, nil
	}
	return Parsed{}, fmt.Errorf("does not match the canonical format for this type (see README: source types)")
}

// readText is the single entry point for the canonical format of every type: each type has one
// form only, with no last-resort guessing. A failure to parse is handled by the caller as a
// mismatch (fall back to the raw text + WARN).
func readText(o Opts, text string, ts time.Time) (Value, bool) {
	switch o.Type {
	case TypeText:
		return Value{Type: TypeText, Text: text}, true

	case TypeNumber:
		if f, ok := parseFloat(text); ok {
			return Value{Type: TypeNumber, Num: f}, true
		}

	case TypeArray:
		if nums, ok := numberLines(text); ok {
			return Value{Type: TypeArray, Points: floatsToPoints(nums)}, true
		}

	case TypeTimeseries:
		if pts, ok := timestampedLines(text, ts); ok {
			return Value{Type: TypeTimeseries, Points: pts}, true
		}

	case TypeMap:
		if m, keys, ok := keyValueLines(text); ok {
			return Value{Type: TypeMap, Map: m, Keys: keys}, true
		}

	case TypeTable:
		if rows, cols, ok := alignedTable(text, o.Header); ok {
			return Value{Type: TypeTable, Rows: rows, Cols: cols}, true
		}

	case TypeLogs:
		if logs, ok := logLines(text, ts); ok {
			return Value{Type: TypeLogs, Logs: logs}, true
		}
	}
	return Value{}, false
}

// —— per-type readers ——

// numberLines reads one bare number per line (at least 1 line).
func numberLines(text string) ([]float64, bool) {
	var nums []float64
	for _, ln := range strings.Split(text, "\n") {
		if ln = strings.TrimSpace(ln); ln == "" {
			continue
		}
		f, ok := parseFloat(ln)
		if !ok {
			return nil, false
		}
		nums = append(nums, f)
	}
	return nums, len(nums) > 0
}

// timestampedLines reads "<timestamp> <value>" or a bare "<value>" per line; the two forms may
// be mixed: a line with a timestamp uses the time the script gave, a line without one is filled
// in with the frame time, and several untimestamped lines in the same frame are staggered by
// their index in steps of 1ms (so that sampling points do not land on the same x). Timestamp
// stripping is covered by leadingTimestamp; the value must fill the rest of the line.
func timestampedLines(text string, ts time.Time) ([]Point, bool) {
	var pts []Point
	for _, ln := range strings.Split(text, "\n") {
		if ln = strings.TrimSpace(ln); ln == "" {
			continue
		}
		if t, rest, ok := leadingTimestamp(ln, ts); ok {
			f, ok := parseFloat(rest)
			if !ok {
				return nil, false
			}
			pts = append(pts, Point{TS: t, V: f})
			continue
		}
		f, ok := parseFloat(ln)
		if !ok {
			return nil, false
		}
		pts = append(pts, Point{TS: ts.Add(time.Duration(len(pts)) * time.Millisecond), V: f})
	}
	return pts, len(pts) > 0
}

// keyValueLines reads "key: value" per line (at least 1 line). Numeric values (possibly with
// trailing punctuation such as a period) are stored as float64, everything else as a string;
// keys keeps the order of appearance in the script (a map is unordered, so the column order and
// the template order can only be expressed here).
//
// Numbers are always float64, integral values included: the template {{printf "%.0f" .x}}
// accepts float64 only, and an int64 renders as %!f(int64=83). Showing an integer as an integer
// is the job of the display layer.
func keyValueLines(text string) (map[string]any, []string, bool) {
	doc := map[string]any{}
	var keys []string
	for _, ln := range strings.Split(text, "\n") {
		if ln = strings.TrimSpace(ln); ln == "" {
			continue
		}
		m := kvLineRe.FindStringSubmatch(ln)
		if m == nil {
			return nil, nil, false
		}
		key, val := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if key == "" {
			return nil, nil, false
		}
		if _, dup := doc[key]; !dup {
			keys = append(keys, key)
		}
		if f, ok := parseCell(val); ok {
			doc[key] = f
		} else {
			doc[key] = val
		}
	}
	if len(keys) == 0 {
		return nil, nil, false
	}
	return doc, keys, true
}

// logLines reads one log line per line, at least 1 line.
func logLines(text string, ts time.Time) ([]LogLine, bool) {
	var out []LogLine
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, parseLogLine(ln, ts))
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// parseLogLine parses one log line: "<time> <level> <message>", where both the time and the
// level may be omitted. Each prefix is accepted only when the first field really looks like it;
// leading indentation does not count (the cut is made by the field index, see firstToken).
func parseLogLine(ln string, ts time.Time) LogLine {
	at := ts // no timestamp at the head of the line: the sampling instant is used
	rest := ln

	if t, r, ok := leadingTimestamp(rest, ts); ok {
		at, rest = t, r
	}
	level := "INFO"
	if tok, end := firstToken(rest); tok != "" {
		if lv, ok := normalizeLevel(tok); ok {
			level = lv
			rest = strings.TrimLeft(rest[end:], " \t")
		}
	}
	if strings.TrimSpace(rest) == "" {
		rest = ln // the whole line is time/level: the message must not be left empty
	}
	return LogLine{Time: at, Level: level, Msg: rest}
}

// firstToken takes the first whitespace-separated field (without the whitespace itself) and
// returns its end index in the original string. The caller must cut the consumed part by that
// index: leading indentation is skipped here, so cutting by len(tok) would be off by one
// indentation width and would swallow the level that follows.
func firstToken(s string) (tok string, end int) {
	start := len(s) - len(strings.TrimLeft(s, " \t"))
	rest := s[start:]
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		return rest[:i], start + i
	}
	return rest, len(s)
}

// leadingTimestamp strips a timestamp from the head of s, returning it, the rest (whitespace on
// both sides removed) and whether one was stripped; when nothing is stripped, s is returned
// unchanged.
//
// The first field is tried first (Unix seconds/milliseconds, 15:04:05 and RFC3339 are all one
// word); on failure the second field is appended and tried — "2006-01-02 15:04:05" spans two
// words, and the first field alone is unrecognizable (parseTimestamp has no "2006-01-02"
// layout). When the two-field form fails, both fields are left to the caller, so two-column
// numbers such as "42.5 43.5" are not mistaken for a timestamp.
func leadingTimestamp(s string, now time.Time) (time.Time, string, bool) {
	tok, end := firstToken(s)
	if t, ok := parseTimestamp(tok, now); ok {
		return t, strings.TrimLeft(s[end:], " \t"), true
	}
	if tok == "" {
		return time.Time{}, s, false
	}
	tok2, end2 := firstToken(s[end:])
	if t, ok := parseTimestamp(tok+" "+tok2, now); ok {
		return t, strings.TrimLeft(s[end+end2:], " \t"), true
	}
	return time.Time{}, s, false
}

// normalizeLevel normalizes common level spellings to one of the four LogLine values.
func normalizeLevel(tok string) (string, bool) {
	switch strings.ToUpper(tok) {
	case "INFO":
		return "INFO", true
	case "WARN", "WARNING":
		return "WARN", true
	case "ERROR", "ERR":
		return "ERROR", true
	case "DEBUG":
		return "DEBUG", true
	}
	return "", false
}

// —— tables ——

// alignedTable parses an aligned table (comma / tab / space, runs of spaces included).
// With hasHeader true the first line is the header and the column names come from it; with
// false the first line counts as data too, the column count comes from the number of cells in
// the first line, and the column names are col_1..col_N.
//
// Only lines whose cell count matches the column count are data rows; lines that do not line up
// (a trailing note, for instance) are skipped.
func alignedTable(s string, hasHeader bool) ([]map[string]any, []string, bool) {
	lines := make([]string, 0, 16)
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		return nil, nil, false
	}

	// Pick the separator among tab/comma/space that yields enough data rows, in the order
	// tab > comma > space: a tab almost never occurs inside a value, whereas a comma is common
	// (process names, overview lines), so a tab is trusted when both would split. Tab and comma
	// are explicit structure and one data row is enough; space alignment is hard to tell from
	// prose and needs ≥2 data rows.
	for _, sep := range []string{"\t", ",", " "} {
		data := lines
		var cols []string
		if hasHeader {
			cols = splitLine(lines[0], sep)
			data = lines[1:]
		} else {
			// no header: the column count comes from the first line, the names are generated
			width := len(splitLine(lines[0], sep))
			if width < 2 {
				continue
			}
			cols = make([]string, width)
			for i := range cols {
				cols[i] = fmt.Sprintf("col_%d", i+1)
			}
		}
		if len(cols) < 2 {
			continue
		}
		minRows := 1
		if sep == " " {
			minRows = 2
		}
		var recs []map[string]any
		for _, ln := range data {
			cells := splitLine(ln, sep)
			if len(cells) != len(cols) {
				continue
			}
			recs = append(recs, buildRow(cols, cells))
		}
		if len(recs) >= minRows {
			return recs, cols, true
		}
	}
	return nil, nil, false
}

// splitLine cuts a line into cells by sep. A space goes through strings.Fields (any whitespace
// separates); other separators are split, trimmed, and empty cells are dropped.
func splitLine(ln, sep string) []string {
	switch sep {
	case " ":
		return strings.Fields(ln)
	default:
		parts := strings.Split(ln, sep)
		out := parts[:0:0]
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
}

// buildRow turns a header and its cells into one row object. Numeric cells become numbers
// (trailing punctuation allowed); a repeated header gets _2.
func buildRow(header, cells []string) map[string]any {
	rec := make(map[string]any, len(header))
	for i, name := range header {
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("col_%d", i+1)
		}
		if _, dup := rec[name]; dup {
			name += "_2"
		}
		cell := strings.TrimSpace(cells[i])
		if f, ok := parseCell(cell); ok {
			rec[name] = f
		} else {
			rec[name] = cell
		}
	}
	return rec
}

// autoCols is the column order of a headerless table: the union of the keys of all rows in
// lexicographic order (this shape carries no order of its own). A table with a header uses the
// header order and does not come here.
func autoCols(rows []map[string]any) []string {
	seen := map[string]bool{}
	var cols []string
	for _, r := range rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	return cols
}

// rowsFromAny normalizes []any into an array of records (every element must be an object). An
// empty array is valid: "the list is empty" is a legitimate result and is not reported as a
// mismatch.
func rowsFromAny(arr []any) ([]map[string]any, bool) {
	rows := make([]map[string]any, 0, len(arr))
	for _, el := range arr {
		m, ok := el.(map[string]any)
		if !ok {
			return nil, false
		}
		rows = append(rows, m)
	}
	return rows, true
}

func recsToAny(recs []map[string]any) []any {
	out := make([]any, len(recs))
	for i := range recs {
		out[i] = recs[i]
	}
	return out
}

// sortedKeys is the key order of an object: a map is unordered, so lexicographic order keeps it
// stable.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// numberArray normalizes a value into a numeric series: an array is converted element by element
// (one element that will not convert abandons the whole thing), a single number counts as a
// one-element series. An empty array is valid (see rowsFromAny).
func numberArray(v any) ([]float64, bool) {
	if arr, ok := v.([]any); ok {
		out := make([]float64, 0, len(arr))
		for _, el := range arr {
			f, ok := toFloat(el)
			if !ok {
				return nil, false
			}
			out = append(out, f)
		}
		return out, true
	}
	if f, ok := toFloat(v); ok {
		return []float64{f}, true
	}
	return nil, false
}

// —— numbers ——

// parseCell reads a cell as a number: trailing punctuation such as . , ; ) … % is allowed (the
// "3882." of vm_stat, the "38%" of df) and is removed before the test. Thousands separators are
// not expanded, so a string containing a comma stays a string.
func parseCell(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if f, ok := parseFloat(s); ok {
		return f, true
	}
	if t := strings.TrimRight(s, ".,;:)…%"); t != "" && t != s {
		if f, ok := parseFloat(t); ok {
			return f, true
		}
	}
	return 0, false
}

// toFloat normalizes float64 / int / int64 / a numeric string to float64.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		return parseFloat(strings.TrimSpace(t))
	default:
		return 0, false
	}
}

func parseFloat(s string) (float64, bool) {
	if !numberRe.MatchString(s) {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// GetFloat reads a numeric field from a document; float64 / int / a numeric string are supported.
func GetFloat(doc map[string]any, field string) (float64, bool) {
	if doc == nil {
		return 0, false
	}
	v, ok := doc[field]
	if !ok {
		return 0, false
	}
	return toFloat(v)
}

// GetString reads a string field.
func GetString(doc map[string]any, field string) (string, bool) {
	if doc == nil {
		return "", false
	}
	v, ok := doc[field]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
