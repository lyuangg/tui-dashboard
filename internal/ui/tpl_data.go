package ui

import (
	"time"

	"tui-dashboard/internal/source"
)

// Unified template data layer. Data sources are used in widgets only; row/section titles
// see a different set:
//
//	Location                      What is visible
//	────────────────────────────  ────────────────────────────────────────────────────
//	widget title / body / format  built-in .date/.weekday/.time + each source by name
//	                              + own source fields flattened
//	row / section title           built-in .date/.weekday/.time only (see titleData)
//
// Builds one global frame per frame: frame = tplData(…) (the three built-in keys plus
// every source exposed by name); a widget merges its own doc onto it at render time with
// mergeTpl (own top-level keys override same-named frame keys, but never touch
// date/weekday/time). Precedence:
//
//	built-in keys(date/weekday/time) > own source doc top-level keys > other sources injected by name
//
// Row titles go through titleData(frame), which keeps only those three built-in keys, so
// {{.host}}/{{.qps}} resolve to nothing in a row title — deliberate (live data belongs in
// that row's widget body). A mistake here draws a startup hint, see RowTitleSourceRefs.
//
// Known semantic boundary: if the own source doc yields top-level keys named
// date/weekday/time, the built-in clock overrides them; to read a cross-source field from
// the own source, write .source.field.

// templateReserved holds the field keys reserved across all templates (date/weekday/time):
// a same-named source is skipped when injected (both tplData and mergeTpl let the built-in
// always win), so a source cannot override the current moment.
var templateReserved = map[string]bool{"date": true, "weekday": true, "time": true}

// tplData assembles the unified data used for all template interpolation: .date (2006-01-02),
// .weekday (English weekday name), .time (15:04:05), plus each data source in views exposed
// by source name (rules in sourceValue).
func tplData(t time.Time, views map[string]source.SourceView) map[string]any {
	h := map[string]any{
		"date":    t.Format("2006-01-02"),
		"weekday": weekdayName(t.Weekday()),
		"time":    t.Format("15:04:05"),
	}
	for name, v := range views {
		if templateReserved[name] {
			continue // reserved keys win: a same-named source cannot override the current time
		}
		h[name] = sourceValue(v)
	}
	return h
}

// titleData extracts the part of the template data a row/section title can see: only the
// built-in clock fields, no data sources. {{.host}}/{{.qps}} in a row title resolve to
// nothing — a single-level field renders empty (RenderTpl strips <no value>, see
// template.go), a multi-level field ({{.svc.value}}) makes template execution fail and the
// whole title falls back verbatim. Live data belongs in that row's widget (startup hint in
// RowTitleSourceRefs).
func titleData(frame map[string]any) map[string]any {
	out := make(map[string]any, len(templateReserved))
	for k := range templateReserved {
		if v, ok := frame[k]; ok {
			out[k] = v
		}
	}
	return out
}

// sourceValue maps a source snapshot to a value templates can reference directly; the shape
// follows the type the source declares:
//
//	map          → that object itself, so {{.mem.used_pct}} still dots into fields by source name
//	number       → that float64, {{.mycount}} is the number itself
//	array        → []float64 (the value at each point)
//	timeseries   → []float64 (same; the time axis is the chart's business, templates want numbers)
//	table        → an array of the rows
//	text / logs  → the single-line original text with whitespace collapsed
//
// A source with no data, and the text type, fall back to the original text either way, so
// writes like {{.host}} are unaffected. This is a different thing from the
// value-resolution layer source.Resolve: that one resolves a single value along a value:
// path, this one is "what the source looks like in an arbitrary template".
func sourceValue(v source.SourceView) any {
	switch v.V.Type {
	case source.TypeMap:
		return v.V.Map
	case source.TypeNumber:
		return v.V.Num
	case source.TypeArray, source.TypeTimeseries:
		vals := make([]float64, len(v.V.Points))
		for i, p := range v.V.Points {
			vals[i] = p.V
		}
		return vals
	case source.TypeTable:
		rows := make([]any, len(v.V.Rows))
		for i := range v.V.Rows {
			rows[i] = v.V.Rows[i]
		}
		return rows
	}
	if t := collapseSpace(v.Text); t != "" {
		return t
	}
	return ""
}

// mergeTpl layers a widget/row's own doc (own) onto the global frame (frame) and returns
// the merged new map: own top-level keys override same-named keys in the frame, but
// date/weekday/time always keep the built-in values. An empty frame uses own directly; an
// empty own copies frame.
func mergeTpl(frame, own map[string]any) map[string]any {
	if len(frame) == 0 {
		return own
	}
	if len(own) == 0 {
		out := make(map[string]any, len(frame))
		for k, v := range frame {
			out[k] = v
		}
		return out
	}
	out := make(map[string]any, len(frame)+len(own))
	for k, v := range frame {
		out[k] = v
	}
	for k, v := range own {
		if templateReserved[k] {
			continue // the built-in always wins
		}
		out[k] = v
	}
	return out
}

// withFrame merges the global frame into a widget source snapshot's doc for the renderer to
// use directly. A non-empty frame returns a shallow copy of v with Doc =
// mergeTpl(frame, v.Doc) (Hist/Logs/Text still come from the original v); an empty frame (a
// unit test calling the renderer directly, say) returns v unchanged.
func withFrame(frame map[string]any, v source.SourceView) source.SourceView {
	if len(frame) == 0 {
		return v
	}
	v.Doc = mergeTpl(frame, v.Doc)
	return v
}
