package source

import "strings"

// The value-resolution layer: the only interface between a data source and a widget.
//
// A source parses the script output into a value according to its declared type (parse.go) and
// does not care who wants what; a widget only declares which kind of value it wants. How the
// value is selected and how it is normalized is decided by Resolve and the three readers
// (ToScalar / ToPoints / ToRecords).
//
// An omitted value: takes the value of the declared type: a source that declared number gives
// that number, one that declared table gives that table, and the output shape is fixed frame to
// frame. A map source cannot omit it — an object has several fields, and not naming one is
// ambiguous; that case is handled as the default look (the panel states the reason) rather than
// picking a field at random.
//
// expr is the value: from the config (the same template syntax as titles), split on "." into
// path segments:
//
//	'{{.used_pct}}'     → ["used_pct"]        top-level field of the source document
//	'{{.mem.used_pct}}' → ["mem","used_pct"]  source name as first segment, skipped (see lookupAt)
//	'{{.services.qps}}' → ["services","qps"]  record-array projection: take the field per row
//	'used_pct'          → ["used_pct"]        a bare field name
//	'{{.}}' / ''        → nil                 nothing specified, see "omitted value:" above
//
// The path walks the document handed to the renderer (this source's own fields merged with the
// global template frame), so a multi-segment path can cross its own fields and can also reach
// another source by source name. A path that does not resolve counts as the field not existing,
// without an error.

// Resolve takes the value to use this time out of a source according to the value: expression,
// and normalizes it into some Type.
//
// An empty expression takes the value of the declared type; a non-empty one takes a field by
// path. When the path lands on a top-level numeric field the value given is preferentially the
// history accumulated across frames — that is the "series" itself, and the current value is only
// its last point. So value: '{{.used_pct}}' fed to a chart draws the trend, and fed to a stat
// yields the current value.
func Resolve(v SourceView, expr string) (Value, bool) {
	segs := pathSegs(expr)
	if len(segs) == 0 {
		return declaredValue(v)
	}
	// History is indexed by top-level field name only (the store accumulates from top-level
	// numeric fields frame by frame), so it is consulted only when the path really lands on a
	// top-level field; otherwise '{{.services.qps}}' would be stolen by a top-level 'qps' of the
	// same name.
	if name := topField(v.Doc, segs); name != "" {
		if h := v.Hist[name]; len(h) > 0 {
			return Value{Type: TypeTimeseries, Points: h}, true
		}
	}
	raw, ok := lookupAt(v.Doc, segs)
	if !ok {
		return Value{}, false
	}
	return normalize(raw)
}

// declaredValue is the resolution when value: is omitted: whatever type the source declared
// gives whatever shape.
//
// number is the exception: the "value" of a number is the series accumulated across frames (one
// point per frame), so Points is given rather than a single Num — stat takes the last point,
// chart draws the whole series; while the history is still empty (the first frame has not
// arrived) it falls back to the current value itself.
func declaredValue(v SourceView) (Value, bool) {
	if v.V.Type == TypeNumber {
		if h := v.Hist[histValue]; len(h) > 0 {
			return Value{Type: TypeTimeseries, Points: h}, true
		}
	}
	if v.V.Has() {
		return v.V, true
	}
	return Value{}, false
}

// normalize normalizes an arbitrary value taken from the template document into some Type; an
// unrecognized shape is treated as resolving to nothing.
//
//	{"a":1}       → map
//	[{…},{…}]     → table (an array of records)
//	[1,2,3]       → array (an array of numbers; a column projected from records is this)
//	"7"           → number (a numeric string)
//	"hello"       → text
//	7 / 7.5       → number
func normalize(raw any) (Value, bool) {
	switch t := raw.(type) {
	case map[string]any:
		return Value{Type: TypeMap, Map: t, Keys: sortedKeys(t)}, true
	case []any:
		if rows, ok := rowsFromAny(t); ok {
			return Value{Type: TypeTable, Rows: rows, Cols: autoCols(rows)}, true
		}
		if nums, ok := numberArray(t); ok {
			return Value{Type: TypeArray, Points: floatsToPoints(nums)}, true
		}
	case string:
		if f, ok := parseFloat(t); ok {
			return Value{Type: TypeNumber, Num: f}, true
		}
		return Value{Type: TypeText, Text: t}, true
	default:
		if f, ok := toFloat(raw); ok {
			return Value{Type: TypeNumber, Num: f}, true
		}
	}
	return Value{}, false
}

// ToScalar reads a normalized value as "the current single number" (stat / gauge). A series
// always yields its last sample (the newest is the current one), so the same value: expression
// resolves to a number on both a scalar source and a series source.
//
// A map cannot give a number: an object has several fields, and not naming one is ambiguous, so
// it resolves to nothing and the caller applies the default look.
func ToScalar(val Value) (float64, bool) {
	switch val.Type {
	case TypeNumber:
		return val.Num, true
	case TypeArray, TypeTimeseries:
		if len(val.Points) > 0 {
			return val.Points[len(val.Points)-1].V, true
		}
	}
	return 0, false
}

// ToPoints reads a normalized value as a drawable point series (chart / bar).
//
// A zero Point.TS means the point has no time axis and the renderer draws by index (array is the
// case in point: it expresses "this frame's N values"); points of every other type carry a time
// (stamped by the program per frame, or given by the script) and the renderer draws a real time
// axis. array's zero TS is stored as such by the producing side (see floatsToPoints); no
// conversion happens here.
func ToPoints(val Value) ([]Point, bool) {
	switch val.Type {
	case TypeNumber:
		return []Point{{V: val.Num}}, true
	case TypeArray, TypeTimeseries:
		return val.Points, len(val.Points) > 0
	}
	return nil, false
}

// ToRecords reads a normalized value as a table of records (table): an array of records is used
// directly (a header table, or a column of records projected by value:). A map is not a record —
// wrapping an object into one row amounts to using field names as column names, which becomes
// unreadable as soon as there are several columns, and seeing what an object looks like goes
// through a text panel. This boundary lives in the value-resolution layer and is complementary
// to the widget capability table: the capability table governs "the type the source declared",
// this governs "the shape of the value itself".
//
// cols is the column order, given by the parse layer (the header order / the union of the keys
// of an array of records).
func ToRecords(val Value) (rows []map[string]any, cols []string, ok bool) {
	if val.Type == TypeTable {
		return val.Rows, val.Cols, len(val.Rows) > 0
	}
	return nil, nil, false
}

// topField returns the top-level field name a path corresponds to, non-empty only when the path
// really points at a top-level field of the document: ["cpu"] does; for ["mem","cpu"], mem is not
// in the document (it is a source name / global frame prefix) and dropping the first segment
// leaves ["cpu"], which does; for ["services","qps"], services is in the document, so this is a
// nested lookup with no top-level field.
func topField(doc map[string]any, segs []string) string {
	switch len(segs) {
	case 1:
		return segs[0]
	case 2:
		// Two segments: the first segment being in the document means a nested lookup
		// ('{{.services.qps}}') with no top-level field; not being in it means the first segment
		// is a source name / global frame prefix ('{{.mem.cpu}}' ≡ '{{.cpu}}') and the second
		// segment is the field.
		if _, ok := doc[segs[0]]; !ok {
			return segs[1]
		}
	}
	return ""
}

// pathSegs splits a value expression into path segments. An expression that is not a plain path
// (such as '{{printf "%.0f" .x}}') is taken whole as one segment name, resolves to no data, and
// is handled as a missing field.
func pathSegs(expr string) []string {
	s := strings.TrimSpace(expr)
	if strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		inner := strings.TrimSpace(s[2 : len(s)-2])
		if !strings.HasPrefix(inner, ".") || strings.ContainsAny(inner, " \t|()\"") {
			return []string{s}
		}
		inner = strings.TrimPrefix(inner, ".")
		if inner == "" {
			return nil // '{{.}}': no field specified
		}
		return strings.Split(inner, ".")
	}
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

// lookupAt resolves a path inside the source's own document. When the path does not resolve and
// has more than one segment, the first segment is dropped and the walk retried — the first
// segment is usually a source name or a global frame prefix, so '{{.mem.used_pct}}' still lands
// on used_pct. When the path does resolve, the middle segments really take effect, which is why
// '{{.services.qps}}' projects the field over an array of records row by row.
func lookupAt(doc map[string]any, segs []string) (any, bool) {
	if doc == nil || len(segs) == 0 {
		return nil, false
	}
	if val, ok := walk(doc, segs); ok {
		return val, true
	}
	if len(segs) > 1 {
		if val, ok := walk(doc, segs[1:]); ok {
			return val, true
		}
	}
	return nil, false
}

// walk walks path segments over an arbitrary value. On reaching an array, the remaining segments
// mean "take that field from every row" (a projection) and the result is still an array — so one
// column of a table can be fed directly to chart / bar / stat.
func walk(val any, segs []string) (any, bool) {
	for _, seg := range segs {
		switch t := val.(type) {
		case map[string]any:
			next, ok := t[seg]
			if !ok {
				return nil, false
			}
			val = next
		case []any:
			out := make([]any, 0, len(t))
			for _, el := range t {
				m, ok := el.(map[string]any)
				if !ok {
					return nil, false
				}
				fv, ok := m[seg]
				if !ok {
					return nil, false
				}
				out = append(out, fv)
			}
			val = out
		default:
			return nil, false
		}
	}
	return val, true
}
