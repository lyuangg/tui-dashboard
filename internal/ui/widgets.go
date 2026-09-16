package ui

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	"github.com/NimbleMarkets/ntcharts/v2/canvas/runes"
	"github.com/NimbleMarkets/ntcharts/v2/linechart"
	"github.com/charmbracelet/x/ansi"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// panelTitle interpolates the template (e.g. {{.cpu}}) against the latest doc of the
// source this widget references to produce the panel title. Renders empty rather than
// crashing when doc is nil or lacks the field.
func panelTitle(w config.Widget, doc map[string]any) string {
	return RenderTpl(w.Title, doc)
}

// frame draws the panel in this widget's border color: a custom color when color is
// set, otherwise the default scheme for the type.
func frame(w config.Widget, title, body string, cw, ch int) string {
	return panelColored(accentOf(w.Type, w.Color), title, body, cw, ch)
}

// widgetAccepts is the capability table: which source types each widget accepts, matching
// the type: declared by the source in the config (source.TypeNames). The gate lives only
// in renderWidget; a mismatch renders the default-look panel and is never fatal.
//
// stat/gauge/chart/bar all accept map: a multi-field object (each line "key: value")
// combined with value: '{{.used_pct}}' to take one of its fields is the regular usage,
// and whether a number can be resolved is decided by the value-resolution layer.
//
// table accepts only table: it is the renderer for record tables, while a map is one
// object with several fields that does not line up into columns; to inspect one object,
// use text.
//
// heatmap accepts only timeseries: it aggregates points by calendar day, which needs a dated
// observation per point. Only timeseries carries timestamps the script chose — a number / map
// history is stamped once per frame at the sampling interval, so summing it into day totals
// measures the sampling, not the day; an array has no time at all.
//
// text and logs are absent from this table: text accepts every type (reads the raw text
// or a text: template), logs accepts only logs.
var widgetAccepts = map[string][]source.Type{
	"stat":    {source.TypeNumber, source.TypeArray, source.TypeTimeseries, source.TypeMap},
	"gauge":   {source.TypeNumber, source.TypeArray, source.TypeTimeseries, source.TypeMap},
	"chart":   {source.TypeNumber, source.TypeArray, source.TypeTimeseries, source.TypeMap},
	"bar":     {source.TypeNumber, source.TypeArray, source.TypeTimeseries, source.TypeMap},
	"heatmap": {source.TypeTimeseries},
	"table":   {source.TypeTable},
	"logs":    {source.TypeLogs},
}

// The two fixed strings are named separately so the width-budget test (width_test.go)
// can reference them: when the string length changes the test follows, with no duplicate
// literal in the test.
const (
	msgUnknownWidget = "(unknown widget type)"
	msgNoLogs        = "(no logs)"
)

// renderWidget renders one widget, dispatching to the renderer for its type.
// v is the read-only snapshot of the source this widget references; cw/ch are the overall
// dimensions including the border (ch==0 auto-sizes).
func renderWidget(w config.Widget, v source.SourceView, cw, ch int) string {
	// Capability-table gate: the type declared by the source × the types the widget
	// accepts. Only here, so no renderer re-checks types; a mismatch draws the
	// default-look panel with the reason stated on it (which source, what type, what is
	// accepted).
	if ok, msg := capability(w, v); !ok {
		return alignPanelTitle(msgPanel(w, v, LevelStyle("WARN").Render(msg), cw, ch), w, v.Doc)
	}

	var s string
	switch w.Type {
	case "stat":
		s = renderStat(v, w, cw, ch)
	case "chart":
		s = renderChart(v, w, cw, ch)
	case "bar":
		s = renderBar(v, w, cw, ch)
	case "heatmap":
		s = renderHeatmap(v, w, cw, ch)
	case "gauge":
		s = renderGauge(v, w, cw, ch)
	case "table":
		s = renderTable(v, w, cw, ch)
	case "logs":
		s = renderLogs(v, w, cw, ch)
	case "text":
		s = renderText(v, w, cw, ch)
	default:
		s = frame(w, panelTitle(w, v.Doc), Faint.Render(msgUnknownWidget), cw, ch)
	}
	// Every renderer draws the title centered on the top edge by default (borderTitle);
	// when title_align is left/right, the top-edge title is recomposed for that
	// alignment and the other lines stay put.
	return alignPanelTitle(s, w, v.Doc)
}

// capability consults the capability table; when ok=false, msg is the single line shown
// to the user.
func capability(w config.Widget, v source.SourceView) (ok bool, msg string) {
	if Accepts(w.Type, v.Type) {
		return true, ""
	}
	return false, fmt.Sprintf("(source %s is %s; panel needs %s)",
		w.Source, v.Type, AcceptedTypes(w.Type))
}

// Accepts reports whether widget type wt accepts source type t. Exported so main can use
// the same table at startup to hint at configs whose types do not match.
//
// text is always true (reading the raw text or a text: template holds for any type); an
// unknown widget type also returns true (caught by the valid-type table in config).
func Accepts(wt string, t source.Type) bool {
	if wt == "text" {
		return true
	}
	accepted, known := widgetAccepts[wt]
	if !known {
		return true
	}
	for _, a := range accepted {
		if a == t {
			return true
		}
	}
	return false
}

// AcceptedTypes returns a description of the types this widget type accepts ("a/b/c"),
// for the startup log.
func AcceptedTypes(wt string) string {
	if wt == "text" {
		return "all"
	}
	accepted, known := widgetAccepts[wt]
	if !known {
		return "all"
	}
	return typeList(accepted)
}

// typeList joins a set of types into "a/b/c".
func typeList(ts []source.Type) string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.String()
	}
	return strings.Join(names, "/")
}

// msgPanel draws a panel holding a single line of explanation: empty states, default
// looks and failed value resolution all go through it, for a uniform style.
//
// The text is truncated and never wrapped (fitLines): wrapping would make the panel height
// vary with the wording, and auto-sizing computes that height from the content line count,
// so one source running out of data would jump the whole row's height. The fixed strings
// (see noValueMsg / capability) must therefore be kept short and ordered with the most
// important information first; the failure line comes from the source, has no upper bound
// on length, and can only be truncated.
//
// Usable width = cw-6: the two leading spaces here + cw-2 in fitLines + cw-4 in
// panelColored.
func msgPanel(w config.Widget, v source.SourceView, msg string, cw, ch int) string {
	return frame(w, panelTitle(w, v.Doc), fitLines("  "+msg, cw-2), cw, ch)
}

// noValueMsg is the explanation for a type match whose wanted value cannot be read this
// time; four cases:
//
//	script output does not match the declared type → take that diagnostic (distinguishes
//	fixing the script from fixing type:)
//	source has not produced any value yet → "waiting for data…" (not a config problem)
//	map source without value: → one object with several fields, ambiguous unless one is named
//	the rest (wrong path / missing field) → state which source, which expression
//
// All but the waiting case are drawn in the warning color. These lines land in msgPanel,
// whose width is covered in its own comment; keep the fixed strings short.
func noValueMsg(w config.Widget, v source.SourceView) string {
	if !v.V.Has() {
		if diag := lastDiag(v); diag != "" {
			return LevelStyle("WARN").Render("(" + diag + ")")
		}
		return Faint.Render("(waiting for data…)")
	}
	if w.Value == "" && v.Type == source.TypeMap {
		return LevelStyle("WARN").Render(fmt.Sprintf("(%s is a map; name a field with value:)", w.Source))
	}
	return LevelStyle("WARN").Render(fmt.Sprintf("(source %s has no %s)", w.Source, w.Value))
}

// lastDiag returns the last framework diagnostic in this source's log buffer (failed
// value resolution / type mismatch), or an empty string when there is none. A non-logs
// source's buffer holds diagnostics only, so its last entry is the reason for the most
// recent absence of data; this is what lets "output does not match type:" and "data has
// not arrived yet" be shown apart (the two are fixed differently).
func lastDiag(v source.SourceView) string {
	for i := len(v.Logs) - 1; i >= 0; i-- {
		if l := v.Logs[i]; l.Level == "WARN" || l.Level == "ERROR" {
			return l.Msg
		}
	}
	return ""
}

// alignPanelTitle recomposes the panel's top-edge title for that alignment when
// widget.title_align is left|right; with the default center (or an empty title) the box
// is returned as is.
func alignPanelTitle(box string, w config.Widget, doc map[string]any) string {
	if w.TitleAlign != "left" && w.TitleAlign != "right" {
		return box
	}
	if t := panelTitle(w, doc); t != "" {
		return borderTitleAlign(box, t, accentOf(w.Type, w.Color), w.TitleAlign)
	}
	return box
}

// —— stat: a single number ——

// Every widget resolves values through the value-resolution layer of the source package
// (Resolve + ToScalar/ToPoints/ToRecords): it only declares which kind of value it wants
// (scalar / point series / records), while the selection and normalization rules live in
// source/resolve.go.

func renderStat(v source.SourceView, w config.Widget, cw, ch int) string {
	format := w.Format
	if format == "" {
		format = "%.1f"
	}

	val, ok := source.Resolve(v, w.Value)
	var f float64
	if ok {
		f, ok = source.ToScalar(val)
	}
	if !ok {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}

	var s string
	if strings.Contains(format, "{{") {
		// format containing {{ renders the whole string as a template for the final
		// text, skipping Sprintf
		s = RenderTpl(format, v.Doc)
	} else {
		// the unit goes directly into format (e.g. "%.0f KB/s"); there is no separate
		// unit field
		s = fmt.Sprintf(format, f)
	}
	body := Bold.Foreground(widgetColor("stat")).Render(s)
	if label := RenderTpl(w.Label, v.Doc); label != "" {
		body = Faint.Render(label) + "\n\n" + body // Label is the subtitle inside the panel; not drawn by default
	}
	return frame(w, panelTitle(w, v.Doc), fitLines(body, cw-2), cw, ch)
}

// —— chart: an ntcharts line chart over a field's history series ——

// renderChart draws a polyline with ntcharts v2 linechart. style selects the drawing:
//
//	"" / "braille": braille polyline, subpixel-smooth (default)
//	"dots": braille line dimmed + a ● over each sampling point, for readable samples
//	"line": ASCII pixel polyline (─ │ segments, no subpixel)
//
// The x-axis follows from the points themselves (see the convention on source.Point.TS):
//
//   - points carrying time (one point per frame for number, script timestamps for
//     timeseries, numeric fields for map) → a real time axis, faithfully reflecting
//     sampling-interval jitter and dropped frames; labels use x_format (a Go time layout),
//     and without it the granularity is picked from the window span (seconds / minutes /
//     days).
//   - points carrying no time (the array type, and a column projected from an array of
//     records) → an ordinal axis 0..n-1.
//
// y-axis range: fixed when min/max are configured (the same pair of fields as gauge),
// otherwise a 15% margin above and below this window's data (the lower edge does not cross
// 0 when all data is non-negative). Tick labels use y_format, falling back to format, and
// to integers when neither is set.
func renderChart(v source.SourceView, w config.Widget, cw, ch int) string {
	val, ok := source.Resolve(v, w.Value)
	var pts []source.Point
	if ok {
		pts, ok = source.ToPoints(val)
	}
	if !ok {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}

	h := w.Height
	if h <= 0 {
		h = 8
	}
	if h < 5 {
		h = 5
	}
	// The canvas puts one rune per cell while a CJK character occupies two: wide characters
	// in labels make that line wider than the canvas column count by exactly this much, and
	// without reserving it fitLines clips the line as over-wide. Only the tick line needs
	// the reservation; every other line is pure ASCII.
	wide := 0
	if !timed(pts) {
		for _, s := range w.Labels {
			wide += cellWidth(s) - utf8.RuneCountInString(s)
		}
	}
	plotW := cw - 6 - wide
	if plotW < 20 {
		plotW = 20
	}

	ymin, ymax := yRange(pts, w)
	xmin, xmax, xat, xlabel := xAxis(pts, w)

	accent := lipgloss.NewStyle().Foreground(widgetColor("chart"))
	axis := lipgloss.NewStyle().Foreground(lipgloss.Color(cur.Axis)).Faint(true)
	xstep := max(2, plotW/6)
	ystep := max(1, h/4)
	lc := linechart.New(plotW, h, xmin, xmax, ymin, ymax,
		linechart.WithXYSteps(xstep, ystep),
		linechart.WithStyles(axis, axis, accent),
		linechart.WithXLabelFormatter(xlabel),
		linechart.WithYLabelFormatter(yLabel(w)),
	)
	// pt is the i-th sample in data coordinates (x comes from xAxis: real time or ordinal).
	pt := func(i int) canvas.Float64Point {
		return canvas.Float64Point{X: xat(i), Y: pts[i].V}
	}
	switch w.Style {
	case "line":
		if len(pts) == 1 {
			lc.DrawRune(pt(0), '●')
		} else {
			for i := 1; i < len(pts); i++ {
				lc.DrawLine(pt(i-1), pt(i), runes.ThinLineStyle)
			}
		}
	case "dots":
		if len(pts) == 1 {
			lc.DrawRuneWithStyle(pt(0), '●', accent.Bold(true))
		} else {
			for i := 1; i < len(pts); i++ {
				lc.DrawBrailleLineWithStyle(pt(i-1), pt(i), accent.Faint(true))
			}
			for i := 0; i < len(pts); i++ {
				lc.DrawRuneWithStyle(pt(i), '●', accent.Bold(true))
			}
		}
	default: // "" / "braille": braille polyline (default)
		if len(pts) == 1 {
			lc.DrawRune(pt(0), '●')
		} else {
			for i := 1; i < len(pts); i++ {
				lc.DrawBrailleLine(pt(i-1), pt(i))
			}
		}
	}
	lc.DrawXYAxisAndLabel()
	if len(w.Labels) > 0 && !timed(pts) {
		drawXLabels(&lc, len(pts), w.Labels, axis)
	}
	return frame(w, panelTitle(w, v.Doc), fitLines(lc.View(), cw-2), cw, 0)
}

// timed reports whether this batch of points carries a time axis: both endpoints must
// carry time. Points of the array type are zero-value timestamps (expressing "the N values
// of this frame", with an ordinal x-axis); checking both endpoints rejects anomalous input
// where only individual points carry time.
func timed(pts []source.Point) bool {
	if len(pts) == 0 {
		return false
	}
	return !pts[0].TS.IsZero() && !pts[len(pts)-1].TS.IsZero()
}

// xAxis decides the x-axis: its range, the x coordinate of the i-th point, and the axis
// label formatter. Points carrying time use seconds relative to the first point (small
// values, good floating-point precision); points carrying none use the ordinal.
//
// When points without time have category names (labels) configured, an always-empty
// formatter is installed here to suppress the upstream numeric ticks, giving the whole
// tick line to drawXLabels (rationale in drawXLabels).
func xAxis(pts []source.Point, w config.Widget) (xmin, xmax float64, at func(int) float64, label linechart.LabelFormatter) {
	if !timed(pts) {
		xmax = float64(len(pts) - 1)
		if xmax < 1 {
			xmax = 1
		}
		if len(w.Labels) > 0 {
			return 0, xmax, func(i int) float64 { return float64(i) }, func(int, float64) string { return "" }
		}
		return 0, xmax, func(i int) float64 { return float64(i) }, linechart.DefaultLabelFormatter()
	}
	base := pts[0].TS
	at = func(i int) float64 { return pts[i].TS.Sub(base).Seconds() }
	xmax = at(len(pts) - 1)
	if xmax < 1 {
		xmax = 1 // a single point, or all at the same instant: the x range does not collapse to 0
	}
	layout := w.XFormat
	if layout == "" {
		layout = timeLayoutFor(xmax)
	}
	label = func(_ int, sec float64) string {
		return base.Add(time.Duration(sec * float64(time.Second))).Format(layout)
	}
	return 0, xmax, at, label
}

// drawXLabels draws the category names below the x-axis at the points' column positions
// (see config.Widget.Labels), each name centered on its column and flush against the edge
// where it would overflow.
//
// Why linechart's XLabelFormatter is not hooked in: that callback's value is derived back
// from the canvas column number rather than the point ordinal, and its check of width by
// len(s) (byte count) before placing the text drops the rightmost names — and the last
// point sits exactly in the rightmost column.
//
// Column positions use the same formula as DrawRuneWithStyle (graphWidth-1 spreads the
// points, then the y-axis column is skipped), otherwise the names would be offset from the
// points. Points not covered by labels fall back to the ordinal; fewer names simply means
// fewer names drawn.
func drawXLabels(lc *linechart.Model, n int, labels []string, style lipgloss.Style) {
	gw, o := lc.GraphWidth(), lc.Origin()
	if gw <= 0 || n <= 0 {
		return
	}
	span := float64(n - 1)
	left := o.X + 1 // skip the y-axis column, aligning with where points land
	prevEnd := 0    // right edge drawn so far: skip a name that does not fit, draw nothing on overlap
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%d", i)
		if i < len(labels) && labels[i] != "" {
			name = labels[i]
		}
		nw := cellWidth(name)
		x := left
		if span > 0 {
			x += int(math.Round(float64(i) * float64(gw-1) / span))
		}
		if x -= nw / 2; x < left {
			x = left
		}
		if right := lc.Width() - nw; x > right {
			x = right
		}
		// a name pushed back off the edge would sit flush against its left neighbor as
		// "shard-0shard-1"; on overlap it is skipped rather than drawn
		if x <= prevEnd {
			continue
		}
		lc.Canvas.SetStringWithStyle(canvas.Point{X: x, Y: o.Y + 1}, name, style)
		prevEnd = x + nw
	}
}

// timeLayoutFor picks a time format from the window span: seconds for a seconds-scale
// window, hour and minute for a few minutes, with the date beyond a day.
func timeLayoutFor(spanSeconds float64) string {
	switch {
	case spanSeconds < 300:
		return "15:04:05"
	case spanSeconds < 86400:
		return "15:04"
	default:
		return "01-02 15:04"
	}
}

// yRange decides the y-axis range: the configured min/max when set (the same pair of
// fields as gauge), otherwise a 15% margin above and below this window's data; the lower
// edge does not cross 0 when all data is non-negative.
func yRange(pts []source.Point, w config.Widget) (float64, float64) {
	if w.Max > w.Min {
		return w.Min, w.Max
	}
	lo, hi := pts[0].V, pts[0].V
	for _, p := range pts[1:] {
		lo = min(lo, p.V)
		hi = max(hi, p.V)
	}
	pad := (hi - lo) * 0.15
	if pad == 0 {
		pad = 1
	}
	ymin, ymax := lo-pad, hi+pad
	if lo >= 0 && ymin < 0 {
		ymin = 0
	}
	return ymin, ymax
}

// yLabel is the formatter for the y-axis ticks: y_format first, then format reused (the
// numbers on this panel are displayed that way), and integers when neither is set. A
// format containing {{ is a template (the stat usage) and is of no use to ticks, so it
// counts as unset.
func yLabel(w config.Widget) linechart.LabelFormatter {
	f := w.YFormat
	if f == "" {
		f = w.Format
	}
	if f == "" || strings.Contains(f, "{{") {
		return linechart.DefaultLabelFormatter()
	}
	return func(_ int, v float64) string { return fmt.Sprintf(f, v) }
}

// seriesValues reduces a point series to plain numbers (bar draws no time axis and only
// needs the values).
func seriesValues(pts []source.Point) []float64 {
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = p.V
	}
	return out
}

// —— bar: horizontal bars, one row per sample ——

// renderBar draws a field's history series as a bar chart. style selects the drawing:
//
//	"" / "hbar": horizontal bars, one row per sample, bar length ∝ value/scale, a 1-column
//	             gap before the right-aligned value, ░ headroom along the bar body (default)
//	"solid":     the same layout without the ░ headroom, drawing only the solid length
//	"vbar":      vertical columns (time left to right), see renderBarV
//
// vbar is readable only with a fixed max: without one it scales automatically to this
// window's peak, and every column nearly fills the height while the values are steady.
// The default is therefore horizontal bars, leaving vbar for the explicit
// style: vbar + fixed max case (e.g. a 0-100 scale).
// The scale comes from widget.max (reusing gauge's max when >0), otherwise the maximum
// within this segment's window.
//
// With labels configured (and the series carrying no time), hbar/solid gain a name column
// at the start of the row; vbar draws none, see renderBarV.
func renderBar(v source.SourceView, w config.Widget, cw, ch int) string {
	val, ok := source.Resolve(v, w.Value)
	var pts []source.Point
	if ok {
		pts, ok = source.ToPoints(val)
	}
	if !ok {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}
	series := seriesValues(pts)
	if w.Style == "vbar" {
		// a vbar sample occupies only 2 columns (1 column + 1 gap) while a name needs 5~10:
		// it does not fit, so it is not drawn even when configured, and no error is raised.
		return renderBarV(series, w, cw, v.Doc)
	}

	// Names (see config.Widget.Labels) are meaningful only on an ordinal series: the
	// points of an array are the N values of this frame and have no corresponding names in
	// the data; a row of number/timeseries history is one instant, where they are of no use.
	labels := w.Labels
	if timed(pts) {
		labels = nil
	}

	// rows = the latest N samples (fixed at that row height when w.Height>0), 6 by
	// default; with names configured it defaults to the number of values (the names
	// correspond to the whole batch of values)
	rows := w.Height
	if rows <= 0 {
		rows = 6
		if len(labels) > 0 {
			rows = len(series)
		}
	}
	// at = the start index of the kept segment in the original series. Names are taken by
	// original ordinal, so truncation must cut the same segment, otherwise names and values
	// shift as a whole.
	at := 0
	if len(series) > rows {
		at = len(series) - rows // keep only the latest rows entries
		series = series[at:]
	}

	// Scale: an explicit max>0 wins; otherwise the maximum within this segment's window
	// (the highest value is full scale).
	scale := w.Max
	if scale <= 0 {
		for _, x := range series {
			if x > scale {
				scale = x
			}
		}
	}

	// Value column: format every row before measuring widths, so the column is
	// right-aligned and nothing is truncated
	texts := make([]string, len(series))
	vw := 0
	for i, x := range series {
		texts[i] = barValue(x, w.Format)
		if cellWidth(texts[i]) > vw {
			vw = cellWidth(texts[i])
		}
	}

	// Content usable width = cw-4 (border 2 + padding 2); a very narrow panel degrades to
	// cw-2.
	innerW := cw - 4
	if innerW < 2 {
		innerW = cw - 2
	}
	if innerW < 1 {
		innerW = 1
	}
	// Value column width ≤ content width - 2, leaving ≥1 column for the bar (at least a
	// 1-column gap even when it does not fit)
	if vw > innerW-2 {
		vw = max(innerW-2, 1)
	}
	// Name column (present only with names): the widest name + a 1-column gap. The whole
	// column gives up when it would squeeze the bar body below 1 column — the bar is the
	// substance, the name only an annotation.
	nw := 0
	if len(labels) > 0 {
		for _, s := range labels {
			if wd := cellWidth(s); wd > nw {
				nw = wd
			}
		}
		nw++
		if innerW-nw-vw-1 < 1 {
			nw = 0
		}
	}
	trackW := innerW - nw - vw - 1 // the 1-column gap between bar and value counts into the space outside vw
	if trackW < 0 {
		trackW = 0
	}

	accent := lipgloss.NewStyle().Foreground(widgetColor("bar"))
	var lines []string
	pad := rows - len(series) // blank lines at the top while history is not yet full, fixing the panel height
	for p := 0; p < pad; p++ {
		lines = append(lines, "")
	}
	for i, x := range series {
		var bar string
		if trackW > 0 {
			f := 0.0
			if scale > 0 {
				f = x / scale
				if f < 0 {
					f = 0
				}
				if f > 1 {
					f = 1
				}
			}
			filled := int(f * float64(trackW))
			if filled > trackW {
				filled = trackW
			}
			fill := accent.Render(strings.Repeat("█", filled))
			if w.Style == "solid" {
				bar = fill // a solid bar carries no ░ headroom
			} else {
				bar = fill + Faint.Render(strings.Repeat("░", trackW-filled))
			}
		}
		prefix := ""
		if nw > 0 {
			j := at + i
			name := fmt.Sprintf("%d", j) // values not covered by names fall back to the ordinal, as on chart's ordinal axis
			if j < len(labels) && labels[j] != "" {
				name = labels[j]
			}
			prefix = padRight(name, nw-1) + " "
		}
		lines = append(lines, prefix+bar+" "+padLeft(texts[i], vw))
	}
	body := strings.Join(lines, "\n")
	return frame(w, panelTitle(w, v.Doc), fitLines(body, cw-2), cw, 0)
}

// renderBarV draws vertical columns (style: vbar): one column per sample, time left to
// right with the newest on the right, column width 1.
// The left of the plot carries a vertical axis: max is snapped to the nearest reference
// row on a 100/75/50/25/0 scale, each such row drawn as a dim horizontal reference line
// with its value marked at the far left. The scale comes from widget.max (fixed when >0),
// otherwise it is scaled automatically to this window's peak.
// A very narrow panel (fewer than 2 drawable columns) degrades to plain columns with no
// scale.
//
// labels are not drawn here: a sample occupies only 2 columns (1 column + 1 gap) while a
// name needs 5~10, so it does not fit.
func renderBarV(series []float64, w config.Widget, cw int, doc map[string]any) string {
	W := cw - 4 // content usable width (border 2 + padding 2)
	if W < 6 {
		W = 6
	}
	H := w.Height // content line count (including the tick reference lines)
	if H <= 0 {
		H = 8
	}
	if H < 4 {
		H = 4
	}

	scale := w.Max
	if scale <= 0 { // no max configured: scale automatically to the window peak (the ticks follow the peak too)
		for _, x := range series {
			if x > scale {
				scale = x
			}
		}
	}
	if scale <= 0 {
		scale = 1
	}

	// —— vertical axis tick steps: a 100/75/50/25/0 scale, each step snapped to the nearest
	// row and deduped ——
	type tick struct {
		frac float64
		row  int
		text string
	}
	usedRow := make([]bool, H)
	ticks := make([]tick, 0, 5)
	for _, f := range [...]float64{1, 0.75, 0.5, 0.25, 0} {
		r := int((1-f)*float64(H-1) + 0.5) // round to the nearest row
		if r < 0 {
			r = 0
		} else if r > H-1 {
			r = H - 1
		}
		if usedRow[r] {
			continue // same row as a higher step: keep the higher one
		}
		usedRow[r] = true
		ticks = append(ticks, tick{frac: f, row: r, text: barValue(f*scale, w.Format)})
	}

	// Left tick gutter: right-aligned tick text + 1 space separating it from the plot
	gutter := 0
	for _, tk := range ticks {
		if wd := cellWidth(tk.text); wd > gutter {
			gutter = wd
		}
	}
	gutter++
	plotW := W - gutter
	labelAt := func(row int) string { // tick text for that row (empty when none)
		for _, tk := range ticks {
			if tk.row == row {
				return tk.text
			}
		}
		return ""
	}
	if plotW < 2 { // too narrow: drop the ticks and let plain columns fill the width
		gutter, ticks = 0, nil
		plotW = W
	}

	// One sample per 2 columns (1 column + 1 gap); keep the latest segment that fits
	maxBars := plotW / 2
	if maxBars < 1 {
		maxBars = 1
	}
	if len(series) > maxBars {
		series = series[len(series)-maxBars:]
	}

	// Canvas: cell[r][c] starts as a plain space; with ticks (steps >0) a tick row is
	// filled with a dim horizontal reference line; the bar body is drawn over it in the
	// accent color.
	type cell struct {
		ch  rune
		bar bool // bar body (accent color); false = space or reference line
	}
	cells := make([][]cell, H)
	for r := 0; r < H; r++ {
		cells[r] = make([]cell, plotW)
		// background cells default to a space: the zero value of rune is NUL, invisible
		// when written to the terminal, so the line would look hollowed out.
		bg := rune(' ')
		if len(ticks) > 0 && labelAt(r) != "" {
			bg = '─' // a tick reference row is filled with a dim horizontal line
		}
		for c := range cells[r] {
			cells[r][c].ch = bg
		}
	}

	// Columns: stacked from the bottom up, height = value/scale × H (rounded), newest on
	// the right
	for i, x := range series {
		f := x / scale
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		n := int(f*float64(H) + 0.5)
		if n > H {
			n = H
		}
		col := 2 * i
		for k := 0; k < n; k++ {
			cells[H-1-k][col] = cell{ch: '█', bar: true}
		}
	}

	// —— assembly: each line = tick text (right-aligned) + canvas line, colored by
	// space / reference line / bar ——
	accent := lipgloss.NewStyle().Foreground(widgetColor("bar"))
	guide := lipgloss.NewStyle().Foreground(lipgloss.Color(cur.Guide))
	lines := make([]string, 0, H)
	for r := 0; r < H; r++ {
		prefix := strings.Repeat(" ", gutter)
		if lab := labelAt(r); lab != "" {
			prefix = padRight(lab, gutter-1) + " "
		}
		var sb strings.Builder
		for c := 0; c < plotW; {
			bar, ch := cells[r][c].bar, cells[r][c].ch
			j := c + 1
			for j < plotW && cells[r][j].bar == bar && cells[r][j].ch == ch {
				j++
			}
			runes := make([]rune, j-c)
			for k := c; k < j; k++ {
				runes[k-c] = cells[r][k].ch
			}
			switch {
			case bar:
				sb.WriteString(accent.Render(string(runes)))
			case ch == '─':
				sb.WriteString(guide.Render(string(runes)))
			default:
				sb.WriteString(string(runes))
			}
			c = j
		}
		lines = append(lines, prefix+sb.String())
	}
	return frame(w, panelTitle(w, doc), fitLines(strings.Join(lines, "\n"), cw-2), cw, 0)
}

// barValue renders the numeric text: with format configured it goes to Sprintf (the unit
// goes directly into format, e.g. "%.0f KB/s"); otherwise an integer value carries no
// decimal point and a fractional one keeps a single place. The horizontal bar's row end
// and the vertical axis ticks share this one implementation.
func barValue(f float64, format string) string {
	if format != "" {
		return fmt.Sprintf(format, f)
	}
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

// —— heatmap: one cell per day, the last 52 weeks ——

const (
	heatWeeks      = 52   // columns: weeks, oldest on the left
	heatRows       = 7    // rows: the days of a week, in time.Weekday order (Sunday first)
	heatLevels     = 3    // levels of the color ramp: no data, then two steps up to the fullest day
	heatCell       = '■'  // every cell carries the same glyph; the color alone carries the level
	heatCellW      = 2    // columns per cell: the square plus one column of air on its right
	heatEmptyShift = 0.35 // how far an empty cell's color is pushed toward the theme's background
)

// heatMonths are the month labels above the grid (time.Month order).
var heatMonths = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// renderHeatmap draws a series as a calendar heatmap: heatWeeks columns of weeks (oldest on
// the left) × heatRows rows of weekdays, the last column being the week containing today, so
// the window rolls forward with the clock (52 weeks = 364 days, "one year"). One cell per day,
// heatCellW columns wide, its level from that day's value.
//
// Points are aggregated into local calendar days and summed — the panel answers "how much
// happened that day". A script emitting one point per day is used as is; several points a day
// contribute their sum. Points outside the window are dropped. Days after today are left blank
// rather than drawn as "no data": they have not happened, and the current week would otherwise
// look like a gap at the right edge.
//
// The level scale is widget.max (fixed when >0), otherwise the window's largest day is full
// scale — the same rule as bar. Every cell carries the same glyph across heatCellW columns: the
// square fills one of them and the next is air, which brings the gap left and right of a square
// up to roughly the line spacing a font leaves above and below its ink. The level shows in the
// color alone (see heatShades).
//
// The window's points are only as long as the source's history_cap: a year needs about 370 of
// them, so a source feeding this panel raises history_cap (at the default 60 the grid shows
// about two months and the rest stays at no-data).
func renderHeatmap(v source.SourceView, w config.Widget, cw, ch int) string {
	val, ok := source.Resolve(v, w.Value)
	var pts []source.Point
	if ok {
		pts, ok = source.ToPoints(val)
	}
	if !ok {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}

	now := time.Now()
	start := heatWindow(now)
	sums, peak := heatSums(pts, start)
	today := int(dayNo(now) - dayNo(start)) // index of today's cell; later cells are blank
	scale := w.Max
	if scale <= 0 {
		scale = peak
	}

	// Content usable width = cw-4 (border 2 + padding 2), degrading to cw-2 in a very narrow
	// panel (as in bar). The weekday names take the 4 leftmost columns (3 for the name + 1
	// gap); when the panel cannot hold both them and the grid, the names give way first — the
	// grid is the substance.
	innerW := cw - 4
	if innerW < 2 {
		innerW = cw - 2
	}
	gutter := 4
	if innerW < gutter+heatWeeks*heatCellW {
		gutter = 0
	}

	shades := make([]lipgloss.Style, heatLevels)
	for i, c := range heatShades() {
		shades[i] = lipgloss.NewStyle().Foreground(c)
	}

	lines := []string{strings.Repeat(" ", gutter) + heatMonthLabels(start)}
	for r := 0; r < heatRows; r++ {
		// Only every other row carries a name (Sunday-first, so rows 1/3/5 are Mon/Wed/Fri);
		// an unlabeled row keeps the gutter blank.
		prefix := strings.Repeat(" ", gutter)
		if gutter > 0 && r%2 == 1 {
			prefix = padRight(time.Weekday(r).String()[:3], gutter-1) + " "
		}
		// level of the cell in column c; -1 is a day that has not happened yet, drawn blank
		level := func(c int) int {
			i := c*heatRows + r
			if i > today {
				return -1
			}
			return heatLevel(sums[i], scale)
		}
		// Consecutive cells of the same level are written as one styled run, keeping the
		// escape sequences of a 52-week line down to a handful.
		var sb strings.Builder
		for c := 0; c < heatWeeks; {
			lv := level(c)
			j := c + 1
			for j < heatWeeks && level(j) == lv {
				j++
			}
			ch := heatCell
			if lv < 0 {
				ch = ' ' // nothing to color
			}
			unit := string(ch) + strings.Repeat(" ", heatCellW-1)
			run := strings.Repeat(unit, j-c)
			if lv < 0 {
				sb.WriteString(run)
			} else {
				sb.WriteString(shades[lv].Render(run))
			}
			c = j
		}
		lines = append(lines, prefix+sb.String())
	}
	return frame(w, panelTitle(w, v.Doc), fitLines(strings.Join(lines, "\n"), cw-2), cw, 0)
}

// heatShades returns the heatLevels cell colors, from a day with nothing on it up to a full
// day. Every cell carries the same glyph, so the ramp is the only thing telling the levels
// apart; it is a blend from the empty-cell color (see heatEmpty) to the theme's heatmap accent,
// so each theme's background is respected and a color: on the panel shifts the whole ramp.
func heatShades() []color.Color {
	return lipgloss.Blend1D(heatLevels, heatEmpty(), widgetColor("heatmap"))
}

// heatEmpty returns the color of a day with nothing on it: the theme's dim grey — the no-data
// color everywhere else on the dashboard — moved heatEmptyShift of the way to that theme's own
// background, so an empty day recedes instead of reading as a low value. The direction follows the
// guide's own brightness, since a pale guide means a pale background: a light theme needs its
// empty cells paler, not darker. The move is a mix toward black or white rather than the theme
// package's Darken/Lighten, which add a flat fraction of full scale and would saturate a light
// theme's empty cells to pure white.
func heatEmpty() color.Color {
	g := lipgloss.Color(cur.Guide)
	r, gg, b, _ := g.RGBA()
	bg := uint32(0) // the background each channel is mixed toward: black, or white on a light theme
	if (r+gg+b)/3 > 0x8000 {
		bg = 0xffff
	}
	mix := func(c uint32) uint8 {
		return uint8((float64(c) + (float64(bg)-float64(c))*heatEmptyShift) / 257)
	}
	return color.RGBA{R: mix(r), G: mix(gg), B: mix(b), A: 0xff}
}

// heatWindow returns the Sunday the window starts with: the first of heatWeeks columns whose
// last one is the week containing ref. AddDate rather than a duration, so a DST transition
// inside the window cannot shift the day boundaries.
func heatWindow(ref time.Time) time.Time {
	y, m, d := ref.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, ref.Location())
	// back to this week's Sunday, then heatWeeks-1 further weeks (heatRows days each)
	return day.AddDate(0, 0, -int(day.Weekday())-heatRows*(heatWeeks-1))
}

// heatSums aggregates the points into the window's cells (index = week*heatRows + weekday)
// and returns the largest day. Timestamps are folded into local calendar days: the panel
// shows days, and a day is a local concept. A point outside the window is dropped. A point
// without a timestamp cannot be placed on a calendar and is dropped as well.
func heatSums(pts []source.Point, start time.Time) ([]float64, float64) {
	sums := make([]float64, heatWeeks*heatRows)
	base := dayNo(start)
	peak := 0.0
	for _, p := range pts {
		if p.TS.IsZero() {
			continue
		}
		i := int(dayNo(p.TS) - base)
		if i < 0 || i >= len(sums) {
			continue
		}
		sums[i] += p.V
		if sums[i] > peak {
			peak = sums[i]
		}
	}
	return sums, peak
}

// dayNo is the number of the local calendar day a time falls on, counted from the epoch. The
// date is anchored at noon before being converted, so a DST transition inside the day cannot
// push the result onto a neighbour.
func dayNo(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 12, 0, 0, 0, t.Location()).Unix() / 86400
}

// heatLevel maps a day's value onto the ramp index: 0 for a day with nothing on it (or a
// non-positive value), 1..heatLevels-1 by how close it comes to scale. The scale is divided
// into equal bands, so any non-zero value is visible even when it is small next to the peak.
func heatLevel(v, scale float64) int {
	if v <= 0 || scale <= 0 {
		return 0
	}
	return min(max(int(math.Ceil(v/scale*float64(heatLevels-1))), 1), heatLevels-1)
}

// heatMonthLabels lays the month names over the grid's first line: a cell is labeled when the
// week it holds contains the 1st of a month. A name that would run into the previous one is
// dropped; month boundaries are at least four weeks apart, so that only happens at the window's
// left edge. The result is exactly heatWeeks*heatCellW columns wide, a name starting on the
// first column of its week's cell.
func heatMonthLabels(start time.Time) string {
	out := make([]rune, heatWeeks*heatCellW)
	for i := range out {
		out[i] = ' '
	}
	prevEnd := 0 // right edge of the names written so far, in columns
	for c := 0; c < heatWeeks; c++ {
		day := start.AddDate(0, 0, c*heatRows)
		name := ""
		for k := 0; k < heatRows; k++ {
			if d := day.AddDate(0, 0, k); d.Day() == 1 {
				name = heatMonths[d.Month()-1]
				break
			}
		}
		at := c * heatCellW
		if name == "" || at < prevEnd {
			continue
		}
		for k, r := range name {
			if at+k < len(out) {
				out[at+k] = r
			}
		}
		prevEnd = at + len(name) + 1 // +1: at least one blank column between two names
	}
	return string(out)
}

// —— gauge: a text progress bar ——

func renderGauge(v source.SourceView, w config.Widget, cw, ch int) string {
	lo, hi := w.Min, w.Max
	if hi <= lo {
		hi = lo + 100
	}

	val, ok := source.Resolve(v, w.Value)
	var f float64
	if ok {
		f, ok = source.ToScalar(val)
	}
	if !ok {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}
	ratio := (f - lo) / (hi - lo)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	text := fmt.Sprintf("%.1f%%", ratio*100)

	// Content usable width = cw-4 (border 2 + padding 2). The suffix is measured first
	// (2 spaces before text) and the bar width derived from it, so "bar + suffix" equals
	// the inner width exactly and the text is not clipped by the Panel.
	innerW := cw - 4
	if innerW < 2 {
		innerW = cw - 2
	}
	if innerW < 1 {
		innerW = 1
	}
	suffix := "  " + text
	if innerW-cellWidth(suffix) < 4 {
		// too narrow: the percentage drops one place to an integer, leaving room for the bar
		// body
		text = fmt.Sprintf("%.0f%%", ratio*100)
		suffix = "  " + text
	}
	barW := innerW - cellWidth(suffix)
	if barW < 0 {
		barW = 0
	}
	filled := int(ratio * float64(barW))
	if filled > barW {
		filled = barW
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)

	body := gaugeColor(ratio).Render(bar) + suffix
	if label := RenderTpl(w.Label, v.Doc); label != "" {
		body = Faint.Render(label) + "\n" + body // subtitle not drawn by default
	}
	return frame(w, panelTitle(w, v.Doc), fitLines(body, cw-2), cw, ch)
}

// —— table: renders an array field of the document ——

// col is a table column after localization / template interpolation (the title is already
// resolved to a plain string).
type col struct {
	key   string
	title string
	width int
}

// renderTable takes a record table: either a table-typed source (an aligned table with a
// header), or an array of records pointed at by value: ('{{.rows}}'). An array of numbers,
// an array of strings and a single object are not records, and a table cannot line them up
// into columns (give a batch of numbers to chart / bar, and one object to text).
func renderTable(v source.SourceView, w config.Widget, cw, ch int) string {
	val, ok := source.Resolve(v, w.Value)
	var rows []map[string]any
	var order []string
	if ok {
		rows, order, ok = source.ToRecords(val)
	}
	if !ok {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}

	// Column definitions: config wins (localized + interpolated titles); otherwise the
	// column order carried by the value (the header order / the key order of the array of
	// records), with missing keys appended in lexicographic order at the end.
	var cols []col
	if len(w.Columns) > 0 {
		for _, c := range w.Columns {
			cols = append(cols, col{key: c.Key, title: RenderTpl(c.Title, v.Doc), width: c.Width})
		}
	} else {
		cols = autoTableCols(rows, order)
	}

	body := renderTableBody(rows, cols, cw-2)
	return frame(w, panelTitle(w, v.Doc), fitLines(body, cw-2), cw, ch)
}

// autoTableCols derives the column order: first the order carried by the value itself
// (order: the header order, or the union of the keys across the array of records), with
// keys not appearing in it appended in lexicographic order at the end. The column order is
// part of the value (source.Value.Cols) and no copy is kept.
func autoTableCols(maps []map[string]any, order []string) []col {
	seen := map[string]bool{}
	var cols []col
	for _, name := range order {
		if seen[name] || !hasKey(maps, name) {
			continue
		}
		cols = append(cols, col{key: name, title: name})
		seen[name] = true
	}
	keys := []string{}
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		cols = append(cols, col{key: k, title: k})
	}
	return cols
}

func hasKey(maps []map[string]any, k string) bool {
	for _, m := range maps {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func renderTableBody(rows []map[string]any, cols []col, maxW int) string {
	// A configured width fixes the column: content and title wider than it are truncated,
	// narrower ones padded. Without one the width comes from the content — the wider of the
	// values and the title — at least 2.
	widths := make([]int, len(cols))
	for i, c := range cols {
		if c.width > 0 {
			widths[i] = c.width
			continue
		}
		wid := 0
		for _, r := range rows {
			if cw := cellWidth(cellString(r, c.key)); cw > wid {
				wid = cw
			}
		}
		if tw := cellWidth(c.title); tw > wid {
			wid = tw
		}
		if wid < 2 {
			wid = 2
		}
		widths[i] = wid
	}

	// Over the limit, every column gives way — a configured width included — shrinking evenly
	// so the table stays inside the panel rather than being cut off at the right edge.
	total := sum(widths) + len(cols) - 1
	if total > maxW && len(cols) > 0 {
		shrink := (total - maxW) / len(cols)
		for i := range widths {
			widths[i] -= shrink
			if widths[i] < 3 {
				widths[i] = 3
			}
		}
	}

	var sb strings.Builder
	for i, c := range cols {
		sb.WriteString(Bold.Render(padRight(truncateVisible(c.title, widths[i]), widths[i])))
		if i != len(cols)-1 {
			sb.WriteString(" ")
		}
	}
	sb.WriteString("\n")
	for _, r := range rows {
		for i, c := range cols {
			sb.WriteString(colorCell(padRight(truncateVisible(cellString(r, c.key), widths[i]), widths[i])))
			if i != len(cols)-1 {
				sb.WriteString(" ")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// —— logs: a scrolling log buffer ——

// logTimeLayout is the default display format for the log time column (a Go time layout).
// The instant itself is stored as a time.Time (see source.LogLine); picking the format is
// the panel's business, and a widget that sets time_format uses that.
const logTimeLayout = "15:04:05"

// logTimeGap is the headroom the time column width keeps beyond the formatted result.
// Columns are separated by another 2 cells from the format string, and 2 more are added
// here, so a full-width time (e.g. "23:59:59") does not sit flush against the level.
const logTimeGap = 2

// logTimeWidth measures the time column width from the layout. What is measured is Go's
// reference time itself, not the actual times of those log lines — the column width must
// be independent of the content, otherwise the whole column jumps sideways at the moment a
// day or month rolls over and the entire screen of logs shifts with it.
//
// The cost is that the width follows the narrowest rendering of each field in the layout:
// the reference instant's hour is "15", and a 12-hour clock yields the half-width "3"
// rather than "03". A 12-hour clock must therefore be written as the zero-padded
// "03:04:05 PM", not "3:04:05 PM" — the latter measures 7 columns while "11:04:05 PM"
// needs 11, and the wider entries get truncated.
func logTimeWidth(layout string) int {
	ref := time.Date(2006, 1, 2, 15, 4, 5, 0, time.Local)
	return cellWidth(ref.Format(layout)) + logTimeGap
}

// renderLogs displays this source's scrolling log buffer. The buffer holds two kinds of
// entry, scrolling together in time order:
//
//   - log lines produced by the type: logs source (appended per frame, capacity log_cap);
//   - the framework's diagnostic WARNs — a failed value resolution, or output that does not
//     match the declared type.
//
// A logs source whose value resolution has broken still shows that WARN, so it is not
// conflated with "the logs simply are not there".
//
// The capability table already guarantees the source here is of type logs; other types draw
// the default look at the gate in renderWidget.
func renderLogs(v source.SourceView, w config.Widget, cw, ch int) string {
	if len(v.Logs) == 0 {
		return frame(w, panelTitle(w, v.Doc), fitLines(Faint.Render("  "+msgNoLogs), cw-2), cw, ch)
	}
	buf := v.Logs
	if limit := lineLimit(ch, w.MaxLines, 12); len(buf) > limit {
		buf = buf[len(buf)-limit:]
	}
	layout := logTimeLayout
	if w.TimeFormat != "" {
		layout = w.TimeFormat
	}
	// The column width is measured from the layout (see logTimeWidth), not from the content:
	// a width that follows the content makes the whole column jump.
	tw := logTimeWidth(layout)

	lines := make([]string, 0, len(buf))
	for _, l := range buf {
		lines = append(lines, fmt.Sprintf("%s  %s  %s",
			Faint.Render(padRight(l.Time.Format(layout), tw)),
			LevelStyle(l.Level).Render(padRight(l.Level, 5)),
			l.Msg,
		))
	}
	return frame(w, panelTitle(w, v.Doc), fitLines(strings.Join(lines, "\n"), cw-2), cw, ch)
}

// —— text: plain text output shown as is ——

// renderText has two inputs, one or the other, with no fallback between them: an explicit
// text: in the config (which may contain a template), or the source's raw output v.Text.
// Every source has raw text (for a structured source, the one it handed over this time), so
// a text widget serves as "view the raw text" for any source. It does not read value:; to
// reach a field of a structured source, put a template in text: (text: "{{.status}}"), and
// numeric fields render the same way.
// An over-wide line is truncated with … by default; wrap: true switches to soft wrap and
// the panel grows taller accordingly.
// When the source has no value this frame, a placeholder is shown first and the template is
// not rendered (see below).
func renderText(v source.SourceView, w config.Widget, cw, ch int) string {
	// A template panel (text: configured and containing {{) shows a placeholder first when
	// the source has no value this frame, rather than rendering the template: the
	// placeholder injected into tplData (an empty string, or the whole raw text when the
	// source does not match) would be fed to the template and render a formatting error
	// string such as %!f(string=), which is nearly certain to appear before the first frame.
	// Blocking it also gives that panel a diagnostic (failed value resolution / mismatch).
	//
	// Side effect: a template that has a source attached but only writes a built-in clock
	// such as {{.date}} likewise shows the placeholder before the first frame, and heals
	// within one interval. A static text with no source, a text: without {{, and a raw-text
	// view with no text: configured are all unaffected.
	if w.Source != "" && strings.Contains(w.Text, "{{") && !v.V.Has() {
		return msgPanel(w, v, noValueMsg(w, v), cw, ch)
	}
	// With text: configured only the template is rendered, an empty result becomes the
	// placeholder, and there is no fallback to the whole raw text — such a fallback would
	// disguise a wrong field name / missing data as a panel with content. The source's raw
	// text is taken only when text: is unset.
	var s string
	if w.Text != "" {
		s = RenderTpl(w.Text, v.Doc)
	} else {
		s = v.Text
	}
	if s == "" {
		return frame(w, panelTitle(w, v.Doc), fitLines(Faint.Render("  —"), cw-2), cw, ch)
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if w.Wrap {
		// The wrap width is the Panel's content width cw-4 (border 2 + padding 2), not cw-2:
		// panelColored clips again at cw-4, so a larger wrap width would have the wrapped
		// lines' tails truncated.
		lines = wrapLines(lines, cw-4)
	}
	limit := lineLimit(ch, w.MaxLines, 20)
	if len(lines) > limit {
		lines = lines[len(lines)-limit:] // after wrapping take the tail by display line count, so the panel height matches
	}
	body := strings.Join(lines, "\n")
	if !w.Wrap {
		body = fitLines(body, cw-2)
	}
	return frame(w, panelTitle(w, v.Doc), body, cw, ch)
}

// —— general helpers ——

// lineLimit computes how many lines a scrolling panel (logs/text) shows this frame. The
// smaller of two limits wins:
//
//   - ch-2: the content-area line count the panel received this frame; lines that do not
//     fit are clipped by the border even when drawn;
//   - maxLines (max_lines): the line limit set in the config, meaning "how many lines I
//     want to see", independent of how tall the panel is.
//
// Preferring ch amounts to disabling max_lines: the bottom-most logs row is stretched by
// the layout to absorb the remaining height, so ch is often very large.
//
// With neither (an auto-sized layout, no max_lines written) it falls back to fallback: both
// a plain text source (such as tail) and a logs source can be very long streams, and
// without a limit they would break the overall layout.
func lineLimit(ch, maxLines, fallback int) int {
	fit := ch - 2 // ch<=2 counts as no height given: not even the border fits
	if maxLines <= 0 {
		if fit > 0 {
			return fit
		}
		return fallback
	}
	if fit > 0 && fit < maxLines {
		return fit
	}
	return maxLines
}

// runeWidth gives a rune's display width in a monospaced terminal, always according to
// charmbracelet/x/ansi: lipgloss's boxes and padding measure width with it, and a
// discrepancy between the two would make truncation/padding off by one column.
// Genuinely double-width characters such as CJK and emoji return 2.
func runeWidth(r rune) int {
	return ansi.StringWidth(string(r))
}

// cellWidth is the number of columns a string actually occupies in the terminal, measured
// with ansi from the same source as lipgloss; ANSI escapes count as 0 columns
// automatically, so stripANSI need not run first.
func cellWidth(s string) int {
	return ansi.StringWidth(s)
}

// fitLines truncates each line to the visible width maxW. ANSI is stripped before
// measuring, otherwise the escape bytes of colored content (e.g. the \x1b[…m run on each
// line of a braille pie chart) would count as width and the line would be mistruncated.
func fitLines(body string, maxW int) string {
	if maxW < 2 {
		maxW = 2
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	for i := range lines {
		if visibleWidth(lines[i]) > maxW {
			lines[i] = truncateVisible(lines[i], maxW)
		}
	}
	return strings.Join(lines, "\n")
}

// visibleWidth is the terminal display width after stripping ANSI.
func visibleWidth(s string) int {
	return cellWidth(stripANSI(s))
}

// wrapLines wraps each line into several by visible width (wrap: true for the text
// widget): a break occurs once the accumulated width reaches maxW. ANSI escape sequences
// move as whole units and occupy no columns; a single character wider than maxW (e.g. a
// double-width character in a very narrow panel) takes a line of its own. Every returned
// display line is at most maxW wide, so the Panel does not clip again.
func wrapLines(lines []string, maxW int) []string {
	if maxW < 2 {
		maxW = 2
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		start, w := 0, 0
		for i := 0; i < len(line); {
			if n := csiLen(line[i:]); n > 0 {
				i += n
				continue
			}
			r, size := utf8.DecodeRuneInString(line[i:])
			rw := runeWidth(r)
			if w > 0 && w+rw > maxW {
				out = append(out, line[start:i]) // break: escape sequences stay in the segment as they are
				start, w = i, 0
			}
			w += rw
			i += size
		}
		out = append(out, line[start:])
	}
	return out
}

// csiLen returns the length of the ANSI escape sequence at the start of s (0 when there is
// none); a lone ESC counts as 1.
// CSI = ESC [ parameters/intermediate bytes (<0x40) + final byte (0x40–0x7e).
func csiLen(s string) int {
	if len(s) == 0 || s[0] != 0x1b {
		return 0
	}
	if len(s) < 2 || s[1] != '[' {
		return 1
	}
	j := 2
	for j < len(s) && s[j] < 0x40 {
		j++
	}
	if j < len(s) {
		return j + 1
	}
	return len(s)
}

// stripANSI removes CSI escape sequences (ESCAPE [ parameters final byte), yielding plain
// visible text.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] < 0x40 { // parameters and intermediate bytes
				j++
			}
			i = j + 1 // consume the final byte (0x40–0x7e)
			continue
		}
		i++ // lone ESC: discard
	}
	return b.String()
}

// truncateVisible truncates to the visible width and appends …; escape sequences move as
// whole units and are not split.
// When truncation happens, 1 column is reserved for the …, guaranteeing the result is at
// most maxW wide; a truncation inside colored content appends an SGR reset so following
// characters do not inherit the foreground color.
func truncateVisible(s string, maxW int) string {
	if visibleWidth(s) <= maxW {
		return s
	}
	room := maxW - 1 // room at the end for … (width 1)
	w, i := 0, 0
	for i < len(s) {
		if n := csiLen(s[i:]); n > 0 {
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if w+runeWidth(r) > room {
			break
		}
		w += runeWidth(r)
		i += size
	}
	return s[:i] + "…" + "\x1b[0m"
}

func padRight(s string, n int) string {
	if cellWidth(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-cellWidth(s))
}

// padLeft pads on the left with spaces to n columns (for right-aligning a value column).
func padLeft(s string, n int) string {
	if d := n - cellWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func cellString(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return trimFloat(v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%v", v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.2f", f)
}

// colorCell colors a status value (UP/DEGRADED/DOWN in the table).
func colorCell(s string) string {
	switch s {
	case "UP":
		return StatusStyle("UP").Render(s)
	case "DEGRADED":
		return StatusStyle("DEGRADED").Render(s)
	case "DOWN":
		return StatusStyle("DOWN").Render(s)
	default:
		return s
	}
}

func sum(a []int) int {
	t := 0
	for _, v := range a {
		t += v
	}
	return t
}
