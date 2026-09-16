package ui

import (
	"fmt"
	"image/color"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// —— test fixtures ——

// mapView builds a snapshot of a map source: doc is the document, hist is the across-frames
// sequence for each numeric field. Keys are sorted in lexicographic order to keep assertions
// stable.
func mapView(doc map[string]any, hist map[string][]source.Point) source.SourceView {
	// Iterating a Go map literal is unordered; sorting keeps the assertion stable.
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return source.SourceView{
		Type: source.TypeMap,
		V:    source.Value{Type: source.TypeMap, Map: doc, Keys: keys},
		Doc:  doc, Hist: hist,
	}
}

// series builds a timestamped across-frames sequence, one point per second. With timestamps
// the renderer draws a real time axis; for a point sequence on the ordinal axis (array) use
// ordinal.
func series(vals ...float64) []source.Point {
	base := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	pts := make([]source.Point, len(vals))
	for i, v := range vals {
		pts[i] = source.Point{TS: base.Add(time.Duration(i) * time.Second), V: v}
	}
	return pts
}

// ordinal builds a point sequence without timestamps (the storage shape of array): TS is all
// zero, and the renderer draws an ordinal axis from that.
func ordinal(vals ...float64) []source.Point {
	pts := make([]source.Point, len(vals))
	for i, v := range vals {
		pts[i] = source.Point{V: v}
	}
	return pts
}

// logAt builds the timestamp of a log line, with the date fixed at 2026-09-08; the panel
// shows only down to seconds by default, so assertions write strings such as "10:00:01".
func logAt(hms string) time.Time {
	t, err := time.ParseInLocation("15:04:05", hms, time.Local)
	if err != nil {
		panic("logAt: " + hms)
	}
	return time.Date(2026, 9, 8, t.Hour(), t.Minute(), t.Second(), 0, time.Local)
}

// tableView builds a snapshot of a table source, with Doc laid out as {"rows": […]} in the
// shape of templateDoc.
func tableView(rows []map[string]any) source.SourceView {
	rs := make([]any, len(rows))
	for i := range rows {
		rs[i] = rows[i]
	}
	return source.SourceView{
		Type: source.TypeTable,
		V:    source.Value{Type: source.TypeTable, Rows: rows, Cols: []string{"name", "status"}},
		Doc:  map[string]any{"rows": rs},
	}
}

// viewOf builds a snapshot declaring the given type, giving one canonical value per type;
// every capability-gate test case goes through it. logs is the only type not read from Value:
// the panel reads SourceView.Logs.
func viewOf(t source.Type) source.SourceView {
	v := source.Value{Type: t}
	var logs []source.LogLine
	switch t {
	case source.TypeNumber:
		v.Num = 1
	case source.TypeArray:
		v.Points = ordinal(1, 2, 3)
	case source.TypeTimeseries:
		v.Points = series(1, 2, 3)
	case source.TypeMap:
		v.Map = map[string]any{"a": 1}
		v.Keys = []string{"a"}
	case source.TypeTable:
		v.Rows = []map[string]any{{"a": 1}}
		v.Cols = []string{"a"}
	case source.TypeText:
		v.Text = "hello"
	case source.TypeLogs:
		v.Logs = []source.LogLine{{Time: logAt("10:00:00"), Level: "INFO", Msg: "日志一行"}}
		logs = v.Logs
	}
	return source.SourceView{Type: t, V: v, Logs: logs}
}

// —— capability gate ——

// TestCapabilityGate verifies that when the type declared by the source does not match the
// types the widget accepts, the panel renders the default look and states the reason (source,
// declared type, type the panel needs), neither blank nor panicking.
func TestCapabilityGate(t *testing.T) {
	for _, c := range []struct {
		widget string
		vt     source.Type
		ok     bool
	}{
		{"stat", source.TypeNumber, true},
		{"stat", source.TypeArray, true},
		{"stat", source.TypeTimeseries, true},
		{"stat", source.TypeMap, true},
		{"stat", source.TypeTable, false},
		{"stat", source.TypeText, false},
		{"stat", source.TypeLogs, false},

		{"gauge", source.TypeArray, true},
		{"gauge", source.TypeTable, false},

		{"chart", source.TypeNumber, true},
		{"chart", source.TypeArray, true},
		{"chart", source.TypeMap, true},
		{"chart", source.TypeTable, false},
		{"chart", source.TypeLogs, false},

		{"bar", source.TypeTimeseries, true},
		{"bar", source.TypeText, false},

		{"heatmap", source.TypeTimeseries, true},
		{"heatmap", source.TypeArray, false},
		{"heatmap", source.TypeMap, false},
		{"heatmap", source.TypeNumber, false},
		{"heatmap", source.TypeText, false},

		{"table", source.TypeTable, true},
		{"table", source.TypeMap, false},
		{"table", source.TypeNumber, false},
		{"table", source.TypeLogs, false},

		{"logs", source.TypeLogs, true},
		{"logs", source.TypeMap, false},
		{"logs", source.TypeText, false},

		// text reads raw text or a text: template, so any type works
		{"text", source.TypeLogs, true},
		{"text", source.TypeTable, true},
		{"text", source.TypeNumber, true},
	} {
		t.Run(fmt.Sprintf("%s×%s", c.widget, c.vt), func(t *testing.T) {
			w := config.Widget{Type: c.widget, Source: "s", Title: "面板"}
			out := stripANSI(renderWidget(w, viewOf(c.vt), 60, 0))
			gated := strings.Contains(out, "panel needs")
			if gated == c.ok {
				t.Errorf("闸门生效=%v, 期望 %v:\n%s", gated, !c.ok, out)
			}
			if !c.ok && !strings.Contains(out, "is "+c.vt.String()) {
				t.Errorf("默认效果应写明源声明的类型:\n%s", out)
			}
		})
	}
}

// TestCapabilityGateSurvivesFrame verifies that after the global template frame is merged,
// the gate still decides by the type: declared by the source, not by which keys happen to be
// in the document.
func TestCapabilityGateSurvivesFrame(t *testing.T) {
	frame := map[string]any{"date": "2026-09-10", "mem": map[string]any{"x": 1}}
	w := config.Widget{Type: "logs", Source: "cpu", Title: "日志"}
	out := stripANSI(renderWidget(w, withFrame(frame, viewOf(source.TypeMap)), 60, 0))
	if !strings.Contains(out, "panel needs") {
		t.Errorf("合并帧后闸门仍应生效:\n%s", out)
	}
}

// TestNoValueMsg verifies that the four cases where the type matches but no value can be read
// this time each get a distinct message, telling the reasons apart by content rather than
// leaving everything blank.
func TestNoValueMsg(t *testing.T) {
	// ① the source has not produced a value yet
	w := config.Widget{Type: "stat", Source: "cpu"}
	if s := stripANSI(noValueMsg(w, source.SourceView{Type: source.TypeNumber})); !strings.Contains(s, "waiting for data") {
		t.Errorf("还没数据时应说等待: %q", s)
	}
	// ② the script output does not match the declared type: copy that diagnostic
	bad := source.SourceView{
		Type: source.TypeMap,
		Logs: []source.LogLine{{Time: logAt("15:30:00"), Level: "WARN",
			Msg: "type: map does not match this output: does not match the canonical format for this type (see README: source types); raw text kept"}},
	}
	if s := stripANSI(noValueMsg(w, bad)); !strings.Contains(s, "does not match this output") {
		t.Errorf("没对上时应抄下诊断,而不是说等待数据: %q", s)
	}
	// with only INFO lines printed by the script itself, it still counts as "no data yet"
	quiet := source.SourceView{
		Type: source.TypeMap,
		Logs: []source.LogLine{{Time: logAt("15:30:00"), Level: "INFO", Msg: "started"}},
	}
	if s := stripANSI(noValueMsg(w, quiet)); !strings.Contains(s, "waiting for data") {
		t.Errorf("没有诊断时不该当成报错: %q", s)
	}
	// ③ the map source names no value:
	v := mapView(map[string]any{"cpu": 1.0}, nil)
	if s := stripANSI(noValueMsg(w, v)); !strings.Contains(s, "name a field with value:") {
		t.Errorf("map 源不点名应说清: %q", s)
	}
	// ④ the path resolves to nothing: state which source and which expression
	w.Value = "{{.nope}}"
	if s := stripANSI(noValueMsg(w, v)); !strings.Contains(s, "has no {{.nope}}") {
		t.Errorf("路径取不到应说清表达式: %q", s)
	}
}

// —— text ——

// TestTextWrap verifies over-wide lines in text: truncated with … by default, wrapped with
// wrap: true, and every wrapped line stays within the content width without losing a
// character.
func TestTextWrap(t *testing.T) {
	const cw = 30                   // panel width including the border; the content width is cw-4
	long := strings.Repeat("外", 40) // 40 double-width characters = 80 columns
	v := source.SourceView{}
	w := config.Widget{Type: "text", Text: long, MaxLines: 10}

	// content returns the panel's content lines (border and side padding removed)
	content := func(w config.Widget) []string {
		lines := strings.Split(stripANSI(renderWidget(w, v, cw, 0)), "\n")
		out := make([]string, 0, len(lines))
		for _, l := range lines[1 : len(lines)-1] {
			l = strings.TrimSuffix(strings.TrimPrefix(l, "│ "), "│")
			out = append(out, strings.TrimRight(l, " "))
		}
		return out
	}

	// By default it truncates to one line and appends …
	def := content(w)
	if len(def) != 1 || !strings.HasSuffix(def[0], "…") {
		t.Errorf("默认应截断成一行带 …: %q", def)
	}

	// wrap: wraps into multiple lines
	w.Wrap = true
	lines := content(w)
	if len(lines) < 2 {
		t.Fatalf("wrap 应折成多行: %q", lines)
	}
	if got := strings.Join(lines, ""); got != long {
		t.Errorf("折行丢了内容: %d 字 → %d 字", len([]rune(long)), len([]rune(got)))
	}
	for i, l := range lines {
		if got := cellWidth(l); got > cw-4 {
			t.Errorf("第 %d 行超宽: %d 列 > %d: %q", i, got, cw-4, l)
		}
	}
}

// TestRenderTextStatic verifies that a static text in the config takes precedence over the
// source output.
func TestRenderTextStatic(t *testing.T) {
	w := config.Widget{Type: "text", Title: "状态", Text: "🎉 一切正常"}
	if s := renderText(source.SourceView{}, w, 40, 0); !strings.Contains(s, "🎉 一切正常") {
		t.Errorf("静态 text 未渲染, got %q", s)
	}
	v := source.SourceView{Text: "来自源的数据"}
	if s := renderText(v, w, 40, 0); strings.Contains(s, "来自源") {
		t.Errorf("静态 text 应优先于 source 输出: %q", s)
	}
}

// TestRenderTextNoFallbackToRaw verifies that with text: configured only the template is
// rendered, and an empty render shows a placeholder instead of falling back to the whole raw
// text — falling back would make "a misspelled field name / missing data" look like "the panel
// has content".
func TestRenderTextNoFallbackToRaw(t *testing.T) {
	v := mapView(map[string]any{"qps": float64(7)}, nil)
	v.Text = "qps: 7" // every source keeps raw text, the last-resort fallback for a text panel

	miss := config.Widget{Type: "text", Source: "sim", Title: "甲", Text: "{{.nope}}"}
	if out := stripANSI(renderWidget(miss, v, 30, 0)); strings.Contains(out, "qps") {
		t.Errorf("模板渲染为空时不应回落到原文:\n%s", out)
	}

	// With no text: configured, it takes the source's raw text
	raw := config.Widget{Type: "text", Source: "sim", Title: "乙"}
	if out := stripANSI(renderWidget(raw, v, 30, 0)); !strings.Contains(out, "qps") {
		t.Errorf("未配 text: 应展示该源原文:\n%s", out)
	}
}

// TestTextTplWaitsForSource verifies that a template in text: shows a placeholder while the
// source has no value yet, rather than rendering half the data — at that point tplData
// injects an empty string under the source name, and feeding it into {{printf "%.0f" .cpu}}
// renders a formatting error string such as %!f(string=). It also verifies that a static text
// still displays and that without text: the raw text is still taken.
func TestTextTplWaitsForSource(t *testing.T) {
	w := config.Widget{Type: "text", Source: "cpu", Title: "模板演示",
		Text: `CPU {{printf "%.0f" .cpu}}%`}
	// frameOf builds the template data layer used at render time: it injects each source's
	// value under the source name, and a source without a value becomes an empty string.
	frameOf := func(v source.SourceView) map[string]any {
		return tplData(time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local),
			map[string]source.SourceView{"cpu": v})
	}
	draw := func(w config.Widget, v source.SourceView) string {
		return stripANSI(renderWidget(w, withFrame(frameOf(v), v), 40, 0))
	}

	// (a) the source has not produced a value yet: a placeholder should appear; first confirm
	// that feeding an empty string to printf indeed yields %!f.
	bare := source.SourceView{Type: source.TypeMap}
	if got := RenderTpl(w.Text, withFrame(frameOf(bare), bare).Doc); !strings.Contains(got, "%!f(") {
		t.Fatalf("前提变了:空串喂进 printf 本该得 %%!f, got %q", got)
	}
	out := draw(w, bare)
	if !strings.Contains(out, "waiting for data") {
		t.Errorf("首帧前该摆等待占位:\n%s", out)
	}
	if strings.Contains(out, "%!") {
		t.Errorf("首帧前不该出现格式化错误串:\n%s", out)
	}

	// (b) the source does not match the declared type: copy that diagnostic, render no template,
	// and show no waiting placeholder
	bad := source.SourceView{Type: source.TypeMap, Text: "这不是键值对",
		Logs: []source.LogLine{{Time: logAt("10:00:00"), Level: "WARN",
			Msg: "type: map does not match this output: first line is not a key/value pair; raw text kept"}}}
	out = draw(w, bad)
	if !strings.Contains(out, "does not match") {
		t.Errorf("源没对上时该抄下那条诊断:\n%s", out)
	}
	if strings.Contains(out, "waiting for data") {
		t.Errorf("源没对上 ≠ 数据还没到,不该显示等待占位:\n%s", out)
	}
	if strings.Contains(out, "%!") || strings.Contains(out, "这不是键值对") {
		t.Errorf("源没对上时不该渲染模板、也不该拿原文当模板数据:\n%s", out)
	}

	// (c) the source has a value: renders as usual
	ok := mapView(map[string]any{"cpu": float64(21)}, nil)
	if out = draw(w, ok); !strings.Contains(out, "CPU 21%") {
		t.Errorf("有值时该照常渲染模板:\n%s", out)
	}

	// (d) with no text: configured: the raw text displays as usual even when the source has no
	// value
	raw := config.Widget{Type: "text", Source: "cpu", Title: "看原文"}
	if out = draw(raw, source.SourceView{Type: source.TypeMap, Text: "原文一行"}); !strings.Contains(out, "原文一行") {
		t.Errorf("没配 text: 时该显示原文:\n%s", out)
	}

	// (e) a static text (no {{) attached to a source: displays as usual
	st := config.Widget{Type: "text", Source: "cpu", Title: "静态", Text: "🎉 一切正常"}
	if out = draw(st, bare); !strings.Contains(out, "🎉 一切正常") {
		t.Errorf("静态 text 不该被闸门挡住:\n%s", out)
	}
}

// —— title and color scheme ——

// TestWidgetTitleAlign verifies that the top-border title is laid out per title_align: left
// flushes against the left corner, right sits at the right edge, and the default is centered.
func TestWidgetTitleAlign(t *testing.T) {
	base := config.Widget{Type: "stat", Source: "sim", Value: "{{.qps}}",
		Title: "总 QPS", Format: "%.0f /s"}
	v := mapView(map[string]any{"qps": float64(9)}, nil)

	const cw = 40 // panel width (border included)
	topOf := func(w config.Widget) string {
		top, _, _ := strings.Cut(stripANSI(renderWidget(w, v, cw, 0)), "\n")
		return top
	}
	// colOf returns the display column (not a byte index) where sub starts in line.
	colOf := func(line, sub string) int {
		i := strings.Index(line, sub)
		if i < 0 {
			return -1
		}
		return cellWidth(line[:i])
	}

	l := topOf(func() config.Widget { w := base; w.TitleAlign = "left"; return w }())
	if !strings.HasPrefix(l, "╭─总 QPS") {
		t.Errorf("title_align=left 标题应贴左角: %q", l)
	}

	r := topOf(func() config.Widget { w := base; w.TitleAlign = "right"; return w }())
	ri := colOf(r, "总 QPS")
	if ri < cw/2 || !strings.HasSuffix(r, "╮") {
		t.Errorf("title_align=right 标题应靠右, 起点列 %d: %q", ri, r)
	}

	c := topOf(base) // default center
	ci := colOf(c, "总 QPS")
	if ci < 0 {
		t.Fatalf("center 标题缺失: %q", c)
	}
	// The padding columns on both sides should be nearly equal (1 column of error allowed).
	leftD, rightD := ci, cw-(ci+cellWidth("总 QPS"))
	if d := leftD - rightD; d < -1 || d > 1 || ci < 6 {
		t.Errorf("center 标题应居中, 左留 %d 右留 %d: %q", leftD, rightD, c)
	}
}

// TestWidgetColorOverride verifies that widget.color overrides the border and title colors,
// and that the default still follows the per-type default.
func TestWidgetColorOverride(t *testing.T) {
	v := mapView(map[string]any{}, nil)
	// default: the border color for type text is 240
	def := renderWidget(config.Widget{Type: "text", Source: "sim", Title: "甲"}, v, 24, 0)
	if !strings.Contains(def, "38;5;240") {
		t.Errorf("text 缺省边框应为 240, got:\n%q", def)
	}
	// overridden to 196: both the center and left paths should take effect
	for _, align := range []string{"", "left"} {
		w := config.Widget{Type: "text", Source: "sim", Title: "乙", Color: "196", TitleAlign: align}
		out := renderWidget(w, v, 24, 0)
		if !strings.Contains(out, "38;5;196") {
			t.Errorf("color=196 未生效(align=%q):\n%q", align, out)
		}
	}
}

// —— gauge ——

// TestGaugePercentTextNotTruncated verifies that at every panel width the percentage text at
// the end of the gauge bar is complete and not truncated.
func TestGaugePercentTextNotTruncated(t *testing.T) {
	v := mapView(map[string]any{"used": float64(73.5)}, nil)
	w := config.Widget{Type: "gauge", Source: "sim", Title: "磁盘", Value: "{{.used}}"}
	for _, cw := range []int{40, 32, 24, 16, 12} {
		out := stripANSI(renderWidget(w, v, cw, 0))
		found := false
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "…") {
				t.Errorf("cw=%d: 某行出现截断 …:\n%q\n%s", cw, ln, out)
			}
			// content inside the vertical borders ends with %, i.e. the row's percentage is complete
			inside := strings.Trim(ln, " │─╭╮╰╯")
			if strings.HasSuffix(inside, "%") {
				found = true
			}
		}
		if !found {
			t.Errorf("cw=%d: 百分比文字被截断:\n%s", cw, out)
		}
	}
}

// TestGaugeRatioColorAndZero verifies that a ratio outside the range is clamped to 100%, and
// that no data does not panic.
func TestGaugeRatioColorAndZero(t *testing.T) {
	v := mapView(map[string]any{"used": float64(150)}, nil)
	w := config.Widget{Type: "gauge", Source: "sim", Title: "盘", Value: "{{.used}}"}
	out := renderWidget(w, v, 20, 0) // ratio above 1
	if !strings.Contains(stripANSI(out), "100") {
		t.Errorf("ratio>1 应夹到 100%%, got:\n%s", stripANSI(out))
	}
}

// —— the value path reaching each widget ——

// TestWidgetValueFromRecords verifies that a field projection over an array of records can
// feed chart and stat; a numeric array fed to stat takes the last sample.
func TestWidgetValueFromRecords(t *testing.T) {
	v := mapView(map[string]any{"services": []any{
		map[string]any{"name": "api", "qps": 800},
		map[string]any{"name": "web", "qps": 950},
	}}, nil)

	// chart: takes a field from the array of records and draws the series
	chart := config.Widget{Type: "chart", Source: "sim", Value: "{{.services.qps}}",
		Title: "每服务 QPS", Height: 6}
	if out := stripANSI(renderWidget(chart, v, 60, 0)); strings.Contains(out, "waiting for data") {
		t.Errorf("chart 应能画记录数组投影出的序列:\n%s", out)
	}

	// stat: the same path takes the last sample
	stat := config.Widget{Type: "stat", Source: "sim", Value: "{{.services.qps}}", Title: "末个服务"}
	if out := stripANSI(renderWidget(stat, v, 40, 0)); !strings.Contains(out, "950") {
		t.Errorf("stat 应取投影序列的末个 950:\n%s", out)
	}

	// a numeric array fed to stat: takes the last one instead of resolving to nothing
	arr := mapView(map[string]any{"values": []any{int64(1), int64(2), int64(3)}}, nil)
	s2 := config.Widget{Type: "stat", Source: "sim", Value: "{{.values}}", Title: "末采样"}
	if out := stripANSI(renderWidget(s2, arr, 40, 0)); !strings.Contains(out, "3") {
		t.Errorf("stat 取数值数组应得末个 3:\n%s", out)
	}
}

// —— table ——

// TestTableColsFollowValue verifies that the column order of the auto columns follows the
// value: the column order the value carries comes first, and the keys it misses are appended
// in lexicographic order.
func TestTableColsFollowValue(t *testing.T) {
	v := source.SourceView{
		Type: source.TypeTable,
		V: source.Value{Type: source.TypeTable,
			Rows: []map[string]any{{"zeta": 9, "b": 2, "a": 1}},
			Cols: []string{"b", "a"}}, // the column order carried by the value, without zeta
	}
	w := config.Widget{Type: "table", Source: "s", Title: "T"}
	hdr := strings.Split(stripANSI(renderWidget(w, v, 60, 0)), "\n")[1]
	bi, ai, zi := strings.Index(hdr, "b"), strings.Index(hdr, "a"), strings.Index(hdr, "zeta")
	if bi < 0 || ai < 0 || zi < 0 || !(bi < ai && ai < zi) {
		t.Errorf("列序应为 b,a,zeta(值给的列序优先,漏的补末尾): %q", hdr)
	}
}

// TestTableRejectsMapSource verifies that a table panel on a map source draws the default
// look and states the reason, instead of forcing the object into a single row.
func TestTableRejectsMapSource(t *testing.T) {
	v := mapView(map[string]any{"qps": 1254, "state": "up"}, nil)
	w := config.Widget{Type: "table", Source: "svc", Title: "对象"}
	out := stripANSI(renderWidget(w, v, 60, 0))
	if !strings.Contains(out, "panel needs table") {
		t.Errorf("应画默认效果并写明原因(源是 map、table 只吃 table):\n%s", out)
	}
	for _, leaked := range []string{"1254", "state"} {
		if strings.Contains(out, leaked) {
			t.Errorf("map 源不该再被渲染成一行表(%q 泄漏进面板):\n%s", leaked, out)
		}
	}
}

// TestTableFromValueProjection verifies that an explicit value: pointing at an array of
// records still resolves that table.
func TestTableFromValueProjection(t *testing.T) {
	v := tableView([]map[string]any{{"name": "a", "status": "UP"}})
	w := config.Widget{Type: "table", Source: "svc", Value: "{{.rows}}", Title: "T"}
	out := stripANSI(renderWidget(w, v, 60, 0))
	for _, kw := range []string{"name", "status", "a", "UP"} {
		if !strings.Contains(out, kw) {
			t.Errorf("value: '{{.rows}}' 应取出那张表,缺 %q:\n%s", kw, out)
		}
	}
}

// —— bar ——

// TestRenderBarHorizontalRowsAndValues verifies that a horizontal bar gives one row per
// sample (height rows in total), oldest on top and newest at the bottom, with the value at
// the end of the row.
func TestRenderBarHorizontalRowsAndValues(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"errs": series(1, 4, 2)})
	w := config.Widget{Type: "bar", Source: "sim", Title: "错误", Value: "{{.errs}}", Height: 6}
	out := stripANSI(renderWidget(w, v, 40, 0))
	lines := strings.Split(out, "\n")
	if len(lines) != 8 { // 2 border rows + 6 content rows
		t.Fatalf("面板行数 = %d(期望 8):\n%s", len(lines), out)
	}
	// when the history is short of rows, the top is left blank
	for i := 1; i <= 3; i++ {
		inside := strings.Trim(lines[i], " │─╭╮╰╯")
		if inside != "" {
			t.Errorf("历史未攒满时应顶部留空, 第 %d 行=%q", i, lines[i])
		}
	}
	// oldest→newest is 1,4,2, the newest at the bottom; the row end should be the value
	want := []struct {
		idx int
		dig string
	}{{4, "1"}, {5, "4"}, {6, "2"}}
	for _, c := range want {
		inside := strings.Trim(lines[c.idx], " │─╭╮╰╯")
		if !strings.HasSuffix(inside, c.dig) {
			t.Errorf("第 %d 行应以数值 %s 结尾: %q", c.idx, c.dig, lines[c.idx])
		}
	}
}

// TestRenderBarAutoScaleNoPanic verifies that without max set the scale follows the window
// peak automatically, and that an all-zero series does not panic.
func TestRenderBarAutoScaleNoPanic(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"e": series(0, 0, 0)})
	w := config.Widget{Type: "bar", Source: "sim", Title: "e", Value: "{{.e}}", Height: 3}
	out := renderWidget(w, v, 30, 0)
	if !strings.Contains(out, "0") {
		t.Errorf("全零序列应正常出 0 值:\n%s", out)
	}
}

// TestBarFormat verifies that with format: configured it goes through Sprintf (units written
// in directly), and that without it the automatic format is kept (integers without a decimal
// point, decimals with 1 digit); the row-end label and the vbar vertical axis tick share the
// same one.
func TestBarFormat(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"rx": series(1240)})
	base := config.Widget{Type: "bar", Source: "net", Title: "下行",
		Value: "{{.rx}}", Height: 1}

	// no format: integers carry no decimal point
	def := stripANSI(renderWidget(base, v, 40, 0))
	if !strings.Contains(def, "1240") || strings.Contains(def, "1240.0") {
		t.Errorf("缺省 format 应给 1240(不带小数点):\n%s", def)
	}

	w := base
	w.Format = "%.0f KB/s"
	got := stripANSI(renderWidget(w, v, 40, 0))
	if !strings.Contains(got, "1240 KB/s") {
		t.Errorf("format 应生效为 %q:\n%s", "1240 KB/s", got)
	}

	// the vbar vertical axis tick shares the same format
	vw := base
	vw.Style, vw.Height, vw.Max, vw.Format = "vbar", 5, 2000, "%.0f KB/s"
	if axis := stripANSI(renderWidget(vw, v, 40, 0)); !strings.Contains(axis, "2000 KB/s") {
		t.Errorf("vbar 纵轴刻度(tick 1.0 × max)应带单位:\n%s", axis)
	}
}

// TestBarStyles verifies the glyphs of the three horizontal-bar styles: default = █ plus ░
// headroom, solid with no ░, and vbar as vertical columns.
func TestBarStyles(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"e": series(30, 55, 40, 80, 60)})
	render := func(style string) string {
		w := config.Widget{Type: "bar", Source: "sim", Style: style, Title: "B", Value: "{{.e}}", Height: 6, Max: 100}
		return stripANSI(renderWidget(w, v, 40, 0))
	}
	if hbar := render(""); !strings.Contains(hbar, "░") || !strings.Contains(hbar, "█") {
		t.Errorf("默认横向条应有 █ 与 ░ 余量:\n%s", hbar)
	}
	if solid := render("solid"); strings.Contains(solid, "░") || !strings.Contains(solid, "█") {
		t.Errorf("solid 应只有实心 █(无 ░ 余量):\n%s", solid)
	}
	if vbar := render("vbar"); strings.Contains(vbar, "░") || !strings.Contains(vbar, "█") {
		t.Errorf("vbar 竖向柱不应有 ░ 余量且应画出柱形:\n%s", vbar)
	}
}

// TestBarVScaleLabels verifies the vertical axis ticks of the vertical columns: the first row
// is the top tick with the columns reaching the top, the last row is the baseline 0 with the
// column bottoms; content is not truncated.
func TestBarVScaleLabels(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"m": series(0, 50, 100, 25, 75, 60)})
	w := config.Widget{Type: "bar", Source: "sim", Style: "vbar",
		Value: "{{.m}}", Height: 6, Max: 100}
	out := stripANSI(renderWidget(w, v, 40, 0))
	lines := strings.Split(out, "\n")
	if len(lines) != 8 { // 2 border rows + 6 content rows
		t.Fatalf("行数 = %d, 期望 8:\n%s", len(lines), out)
	}
	if strings.Contains(out, "░") || strings.Contains(out, "…") {
		t.Errorf("竖向柱不应有余量 ░ 或截断 …:\n%s", out)
	}
	// once the border verticals are removed: the first row is the column top of 100, the last
	// row is the baseline 0
	top := strings.Trim(strings.Trim(lines[1], "│"), " ")
	bot := strings.Trim(strings.Trim(lines[6], "│"), " ")
	if !strings.HasPrefix(top, "100") || !strings.Contains(top, "█") {
		t.Errorf("首行应为刻度 100 且柱顶到顶: %q", lines[1])
	}
	if !strings.Contains(bot, "0") || !strings.Contains(bot, "█") {
		t.Errorf("末行应是基线 0 且含柱底: %q", lines[6])
	}
}

// TestBarVScaleAuto verifies that vbar without max configured scales automatically, with the
// peak becoming the top tick.
func TestBarVScaleAuto(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"e": series(0.5, 2.5, 1)})
	w := config.Widget{Type: "bar", Source: "sim", Style: "vbar", Value: "{{.e}}", Height: 5}
	out := stripANSI(renderWidget(w, v, 30, 0))
	if !strings.Contains(out, "2.5") { // the peak becomes the top tick
		t.Errorf("自动定标应把峰值 2.5 显示成刻度:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("窄面板自动定标不应截断:\n%s", out)
	}
}

// barRow returns row i of the panel's content area (0 is the first content row, skipping the
// top border), with the border verticals and the whitespace at both ends removed.
func barRow(panel string, i int) string {
	lines := strings.Split(strings.TrimRight(panel, "\n"), "\n")
	if i+1 >= len(lines) {
		return ""
	}
	return strings.Trim(strings.Trim(lines[i+1], "│"), " ")
}

// TestBarLabels verifies that with labels configured (and a series without timestamps) the
// horizontal bar gains a name column at the row start: one value per row, name and value on
// the same row in the same order; when height is short or fewer names are written, values
// without a name fall back to the ordinal.
func TestBarLabels(t *testing.T) {
	arr := func(vals ...float64) source.SourceView {
		return source.SourceView{Type: source.TypeArray, V: source.Value{Type: source.TypeArray,
			Points: ordinal(vals...)}}
	}
	render := func(w config.Widget, v source.SourceView, cw int) string {
		return stripANSI(renderWidget(w, v, cw, 0))
	}

	// name at the row start, value at the row end
	w := config.Widget{Type: "bar", Source: "s", Title: "分片", Height: 3,
		Labels: []string{"cpu", "mem", "disk"}}
	out := render(w, arr(10, 20, 30), 40)
	for i, c := range []struct{ name, val string }{{"cpu", "10"}, {"mem", "20"}, {"disk", "30"}} {
		ln := barRow(out, i)
		if !strings.HasPrefix(ln, c.name) || !strings.HasSuffix(ln, c.val) {
			t.Errorf("第 %d 行应以 %s 开头、以 %s 结尾: %q", i, c.name, c.val, ln)
		}
	}

	// height below the number of values: keep only the last height values, and the names are
	// cut from the same stretch
	w.Height = 2
	out = render(w, arr(10, 20, 30, 40), 40)
	if ln := barRow(out, 0); !strings.HasPrefix(ln, "disk") || !strings.HasSuffix(ln, "30") {
		t.Errorf("截断后首行应是 disk 对 30(名跟着值切): %q", ln)
	}
	if ln := barRow(out, 1); !strings.HasPrefix(ln, "3") || !strings.HasSuffix(ln, "40") {
		t.Errorf("截断后末行应是序号 3 对 40: %q", ln)
	}

	// fewer names written: values not covered fall back to the ordinal
	w.Height, w.Labels = 3, []string{"cpu"}
	out = render(w, arr(10, 20, 30), 40)
	if ln := barRow(out, 0); !strings.HasPrefix(ln, "cpu") {
		t.Errorf("第 1 行该用配的名字: %q", ln)
	}
	for i, idx := range []string{"1", "2"} {
		if ln := barRow(out, i+1); !strings.HasPrefix(ln, idx) {
			t.Errorf("没名字的第 %d 行该退回序号 %s: %q", i+2, idx, ln)
		}
	}

	// when a narrow panel does not fit the names the whole column gives up, and the values
	// still show
	narrow := config.Widget{Type: "bar", Source: "s", Title: "分片", Height: 3,
		Labels: []string{"shard-0", "shard-1", "shard-2"}}
	out = render(narrow, arr(10, 20, 30), 14)
	if strings.Contains(out, "shard") {
		t.Errorf("窄到放不下时应整列让掉名字:\n%s", out)
	}
	if !strings.Contains(out, "30") {
		t.Errorf("让掉名字后值仍应显示:\n%s", out)
	}
}

// TestBarLabelsIgnored verifies that names are drawn only on the horizontal bar's ordinal
// series: a timestamped series draws none (names would be mismatched to a point in time), and
// vbar draws none (one sample takes only 2 columns and does not fit).
func TestBarLabelsIgnored(t *testing.T) {
	timed := mapView(map[string]any{}, map[string][]source.Point{"c": series(10, 20, 30)})
	w := config.Widget{Type: "bar", Source: "sim", Value: "{{.c}}", Height: 3,
		Labels: []string{"shard-0", "shard-1", "shard-2"}}
	if out := stripANSI(renderWidget(w, timed, 40, 0)); strings.Contains(out, "shard") {
		t.Errorf("带时间的序列上不该画名字:\n%s", out)
	}

	vbar := w
	vbar.Style, vbar.Max = "vbar", 40
	arr := source.SourceView{Type: source.TypeArray, V: source.Value{Type: source.TypeArray,
		Points: ordinal(10, 20, 30)}}
	if out := stripANSI(renderWidget(vbar, arr, 40, 0)); strings.Contains(out, "shard") {
		t.Errorf("vbar 上不该画名字:\n%s", out)
	}
}

// —— heatmap ——

// heatPanelW is the narrowest panel that shows the whole grid without clipping: 2 border
// columns, 2 padding columns, the 4-column weekday gutter and heatWeeks cells.
const heatPanelW = 4 + 4 + heatWeeks*heatCellW

// TestHeatWindow pins the window's first day: the Sunday heatWeeks-1 weeks before the week
// enclosing ref, so the window's last column is ref's own week and the grid spans 52*7 days.
func TestHeatWindow(t *testing.T) {
	// 2026-09-15 is a Tuesday; the window's first Sunday is 51 weeks earlier.
	ref := time.Date(2026, 9, 15, 10, 30, 0, 0, time.Local)
	start := heatWindow(ref)
	want := time.Date(2025, 9, 21, 0, 0, 0, 0, time.Local)
	if !start.Equal(want) {
		t.Errorf("heatWindow(%s) = %s, 期望 %s",
			ref.Format(time.DateOnly), start.Format(time.DateOnly), want.Format(time.DateOnly))
	}
	if start.Weekday() != time.Sunday {
		t.Errorf("窗口首列应为周日, got %s", start.Weekday())
	}
	if got, want := dayNo(ref)-dayNo(start), int64(ref.Weekday())+heatRows*(heatWeeks-1); got != want {
		t.Errorf("ref 落在第 %d 天, 期望 %d", got, want)
	}
	// the day, not the instant, decides the window
	for _, h := range []int{0, 23} {
		if got := heatWindow(time.Date(2026, 9, 15, h, 59, 0, 0, time.Local)); !got.Equal(start) {
			t.Errorf("同一天的 %02d:59 应得同一窗口, got %s", h, got.Format(time.DateOnly))
		}
	}
}

// TestHeatSums pins how points land on cells: the index counts from the window's first Sunday
// (week*7 + weekday), points sharing a day are summed, and anything outside the window or
// without a timestamp is dropped.
func TestHeatSums(t *testing.T) {
	start := time.Date(2026, 1, 4, 0, 0, 0, 0, time.Local) // a Sunday: the window's first column
	at := func(day, hour int) time.Time {
		return time.Date(2026, 1, 4+day, hour, 0, 0, 0, time.Local)
	}
	pts := []source.Point{
		{TS: at(0, 3), V: 1},                // the window's first day → index 0
		{TS: at(1, 9), V: 2},                // Monday → index 1
		{TS: at(1, 21), V: 3},               // the same Monday again → summed
		{TS: at(363, 12), V: 4},             // the window's last day → index 363
		{TS: at(364, 12), V: 8},             // one day past the window → dropped
		{TS: start.AddDate(0, 0, -1), V: 8}, // one day before the window → dropped
		{V: 8},                              // no timestamp → dropped
	}
	sums, peak := heatSums(pts, start)
	if len(sums) != heatWeeks*heatRows {
		t.Fatalf("格数 = %d, 期望 %d", len(sums), heatWeeks*heatRows)
	}
	for _, c := range []struct {
		i    int
		want float64
	}{{0, 1}, {1, 5}, {363, 4}} {
		if sums[c.i] != c.want {
			t.Errorf("sums[%d] = %v, 期望 %v", c.i, sums[c.i], c.want)
		}
	}
	total := 0.0
	for _, s := range sums {
		total += s
	}
	if total != 10 {
		t.Errorf("窗口内合计 = %v, 期望 10(窗口外与无时间戳的点应被丢弃)", total)
	}
	if peak != 5 {
		t.Errorf("peak = %v, 期望 5(窗口内最大的一天)", peak)
	}
}

// TestHeatLevel pins the ramp bands: two equal bands of the scale, a non-zero value always
// visible (at least band 1), and band 0 for a day with nothing on it or without a scale.
func TestHeatLevel(t *testing.T) {
	for _, c := range []struct {
		v, scale float64
		want     int
	}{
		{0, 100, 0}, {1, 100, 1}, {50, 100, 1}, {51, 100, 2},
		{100, 100, 2}, {250, 100, 2}, {5, 0, 0}, {-1, 100, 0},
	} {
		if got := heatLevel(c.v, c.scale); got != c.want {
			t.Errorf("heatLevel(%v, %v) = %d, 期望 %d", c.v, c.scale, got, c.want)
		}
	}
}

// TestRenderHeatmap pins the panel's shape and what the grid says: a day whose total is the
// window's peak is a full cell, a day without data is the no-data glyph, and the month row
// names the current month. The renderer takes the current week as the window's right edge, so
// the fixture is timed from the clock as well.
func TestRenderHeatmap(t *testing.T) {
	now := time.Now()
	// a day inside the window: two weeks back at noon local, away from any day boundary
	day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location()).AddDate(0, 0, -14)
	v := source.SourceView{
		Type: source.TypeTimeseries,
		V: source.Value{Type: source.TypeTimeseries, Points: []source.Point{
			{TS: day, V: 40}, {TS: day.Add(6 * time.Hour), V: 60}, // the same day → summed to 100
		}},
	}
	w := config.Widget{Type: "heatmap", Source: "daily_year", Title: "活动"}
	out := stripANSI(renderWidget(w, v, heatPanelW, 0))
	lines := strings.Split(out, "\n")
	if len(lines) != 10 { // 2 border rows + the month row + 7 weekday rows
		t.Fatalf("面板行数 = %d(期望 10):\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], now.Format("Jan")) {
		t.Errorf("月份标签行应含 %s: %q", now.Format("Jan"), lines[1])
	}
	// the row of that weekday: every cell carries the same glyph — the level shows in the color
	// alone, so no other block character appears in it — and every glyph has an air column
	row := lines[2+int(day.Weekday())]
	if !strings.Contains(row, string(heatCell)) {
		t.Errorf("有数据的日子应画格子 %c: %q", heatCell, row)
	}
	for _, g := range []string{"█", "▓", "▒", "░", "·", "▪"} {
		if strings.Contains(row, g) {
			t.Errorf("格子样式应一致, 不该出现 %q: %q", g, row)
		}
	}
	if strings.Contains(row, string(heatCell)+string(heatCell)) {
		t.Errorf("格子之间应留出空隙: %q", row)
	}
	// the grid is the 4-column weekday gutter plus heatWeeks cells of heatCellW columns each
	if got := visibleWidth(lines[1]); got != heatPanelW {
		t.Errorf("面板宽 = %d(期望 %d, 内容 %d 列):\n%s", got, heatPanelW, heatPanelW-4, out)
	}
	// days after today have not happened: every row below today's weekday ends blank, the rows
	// above it still carry the current week's cell
	for r := 0; r < heatRows; r++ {
		row := []rune(strings.Trim(lines[2+r], "│"))
		// from the left: 1 column of padding, the 4-column gutter, then the last cell's column
		last := row[1+4+heatCellW*(heatWeeks-1)]
		if want := r > int(now.Weekday()); want != (last == ' ') {
			t.Errorf("第 %d 行末列留空 = %v, 期望 %v: %q", r, last == ' ', want, string(row))
		}
	}
}

// TestHeatShades pins the ramp's premise: heatLevels distinct colors, from an empty cell to the
// heatmap accent, since every cell shares one glyph and the color is the only signal. The empty
// end sits near the theme's background rather than at the dim grey every other panel uses.
func TestHeatShades(t *testing.T) {
	sh := heatShades()
	if len(sh) != heatLevels {
		t.Fatalf("色阶档数 = %d, 期望 %d", len(sh), heatLevels)
	}
	seen := map[string]bool{}
	for i, c := range sh {
		key := fmt.Sprintf("%v", c)
		if seen[key] {
			t.Errorf("第 %d 档与前面某档同色(%s), 档位不可分", i, key)
		}
		seen[key] = true
	}
	// the empty end is the theme's dim grey moved away from the text colors, the full end is
	// the theme's own accent; the blend returns both as RGB, so compare channels
	guide := lipgloss.Color(cur.Guide)
	if sameColor(sh[0], guide) {
		t.Errorf("空白天那一档不该与主题暗灰 %v 同色, 应更靠近背景", guide)
	}
	if got := lightness(sh[0]); got >= lightness(guide) {
		t.Errorf("深色主题下空白天应更暗: 亮度和 = %d, 主题暗灰 = %d", got, lightness(guide))
	}
	if got, want := sh[heatLevels-1], widgetColor("heatmap"); !sameColor(got, want) {
		t.Errorf("最高档应为 heatmap 主色 %v, got %v", want, got)
	}
}

// TestHeatEmptyLightTheme pins the empty-cell shift: it stays strictly between the theme's guide
// and that theme's background. Equal to the guide would leave the no-data day as bright as every
// other panel's no-data grey (no recession); equal to the background would erase those days from
// the grid. A pale guide means a pale background, so the direction flips with the theme.
func TestHeatEmptyLightTheme(t *testing.T) {
	before := cur
	t.Cleanup(func() { UseTheme(before.Name) })

	for _, c := range []struct {
		theme  string
		darker bool
	}{
		{"", true},       // default: a dark background
		{"nord", true},   // the other dark presets
		{"light", false}, // a light background
	} {
		if !UseTheme(c.theme) {
			t.Fatalf("UseTheme(%q) 失败", c.theme)
		}
		guide := lipgloss.Color(cur.Guide)
		got := heatEmpty()
		if sameColor(got, guide) {
			t.Errorf("%s: 空白天不该就是主题暗灰 %v, 应更靠近背景", c.theme, guide)
		}
		if c.darker && lightness(got) >= lightness(guide) {
			t.Errorf("%s: 空白天应更暗, 亮度和 = %d, 暗灰 = %d", c.theme, lightness(got), lightness(guide))
		}
		if !c.darker && lightness(got) <= lightness(guide) {
			t.Errorf("%s: 空白天应更浅, 亮度和 = %d, 暗灰 = %d", c.theme, lightness(got), lightness(guide))
		}
		bg := uint32(0) // the background's lightness sum: black, or white on a light theme
		if !c.darker {
			bg = 3 * 0xffff
		}
		if lightness(got) == bg {
			t.Errorf("%s: 空白天压到了背景上, 与背景不可分, 空格子会看不见", c.theme)
		}
	}
}

// lightness is the sum of a color's channels: enough to tell which of two colors is the paler.
func lightness(c color.Color) uint32 {
	r, g, b, _ := c.RGBA()
	return r + g + b
}

// sameColor compares two colors by their channels, so it holds across representations (a
// 256-color index and the RGB value it was blended into are the same color).
func sameColor(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}

// TestHeatmapMaxFixesScale pins that max: fixes the top of the ramp: the same day that is a
// full cell with the automatic scale sits one band up with a max well above it.
func TestHeatmapMaxFixesScale(t *testing.T) {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location()).AddDate(0, 0, -14)
	v := source.SourceView{
		Type: source.TypeTimeseries,
		V:    source.Value{Type: source.TypeTimeseries, Points: []source.Point{{TS: day, V: 100}}},
	}
	w := config.Widget{Type: "heatmap", Source: "daily_year", Title: "活动", Max: 400}
	out := renderWidget(w, v, heatPanelW, 0)

	// the cell is a run of its own (its neighbours have no data), so the exact styled run shows
	// which band it took: max 400 puts a value of 100 in the first of the two bands
	sh := heatShades()
	if want := heatRun(sh, 1); !strings.Contains(out, want) {
		t.Errorf("max: 400 下 100 应为一档: 面板中找不到 %q", want)
	}
	peak := heatRun(sh, heatLevels-1)
	if strings.Contains(out, peak) {
		t.Errorf("max: 400 下 100 不该画成满格")
	}
}

// heatRun is the styled run a single cell of the given level renders as; a cell whose
// neighbours are all of another level becomes a run of its own, so the exact string pins
// which band a day landed in.
func heatRun(sh []color.Color, level int) string {
	return lipgloss.NewStyle().Foreground(sh[level]).
		Render(string(heatCell) + strings.Repeat(" ", heatCellW-1))
}

// TestHeatmapNoValue pins that a heatmap whose source has produced nothing draws the shared
// message panel rather than an empty grid.
func TestHeatmapNoValue(t *testing.T) {
	w := config.Widget{Type: "heatmap", Source: "daily_year", Title: "活动"}
	out := stripANSI(renderWidget(w, source.SourceView{Type: source.TypeTimeseries}, 60, 0))
	if !strings.Contains(out, "waiting for data") {
		t.Errorf("还没数据时应走 msgPanel:\n%s", out)
	}
	if strings.Contains(out, string(heatCell)) {
		t.Errorf("没数据时不该画网格:\n%s", out)
	}
}

// —— chart ——

// hasBraille reports whether the text contains braille characters (U+2800..28FF, including
// the blank dot U+2800).
func hasBraille(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

// TestChartStyles verifies the glyphs of the three line-chart styles: default = braille
// polyline (no ●), dots = braille line plus ● sampling points, line = ASCII pixel line (no
// braille).
func TestChartStyles(t *testing.T) {
	v := mapView(map[string]any{}, map[string][]source.Point{"s": series(10, 30, 20, 45, 25, 60, 35, 50, 28, 40)})
	render := func(style string) string {
		w := config.Widget{Type: "chart", Source: "sim", Style: style, Title: "C", Value: "{{.s}}", Height: 6}
		return stripANSI(renderWidget(w, v, 60, 0))
	}
	def, dots, line := render(""), render("dots"), render("line")
	if !hasBraille(def) {
		t.Errorf("默认应画盲文折线:\n%s", def)
	}
	if !hasBraille(dots) || !strings.Contains(dots, "●") {
		t.Errorf("dots 应有盲文线 + ● 采样点:\n%s", dots)
	}
	if hasBraille(line) {
		t.Errorf("line 应画 ASCII 像素线(不含盲文):\n%s", line)
	}
}

// TestXAxis verifies that the x-axis is decided by the points themselves: timestamped points
// draw a real time axis (coordinates are seconds relative to the first point, labels are times
// of day), points without timestamps go by ordinal; a single point does not let the range
// collapse to 0.
func TestXAxis(t *testing.T) {
	base := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	timed := []source.Point{
		{TS: base, V: 1},
		{TS: base.Add(20 * time.Second), V: 2},
		{TS: base.Add(30 * time.Second), V: 3},
	}
	xmin, xmax, at, label := xAxis(timed, config.Widget{XFormat: "15:04:05"})
	if xmin != 0 || xmax != 30 {
		t.Errorf("时间轴范围应为 [0 30] 秒, got [%v %v]", xmin, xmax)
	}
	if at(1) != 20 {
		t.Errorf("第 1 个点的 x 应是 20 秒, got %v", at(1))
	}
	if got, want := label(0, 20), base.Add(20*time.Second).Format("15:04:05"); got != want {
		t.Errorf("轴标签应按 x_format 格式化: got %q, 期望 %q", got, want)
	}

	// points without timestamps: the ordinal axis
	_, xmax, at, label = xAxis([]source.Point{{V: 1}, {V: 2}, {V: 3}}, config.Widget{})
	if xmax != 2 || at(2) != 2 {
		t.Errorf("序号轴应为 0..2, got xmax=%v at(2)=%v", xmax, at(2))
	}
	if got := label(0, 2); got != "2" {
		t.Errorf("序号轴标签应是序号本身, got %q", got)
	}

	// with category names configured: the upstream ticks are suppressed and drawn by
	// drawXLabels
	_, xmax, at, label = xAxis([]source.Point{{V: 1}, {V: 2}, {V: 3}},
		config.Widget{Labels: []string{"cpu", "mem", "disk"}})
	if xmax != 2 || at(2) != 2 {
		t.Errorf("分类轴的范围与坐标不该变, got xmax=%v at(2)=%v", xmax, at(2))
	}
	if got := label(0, 2); got != "" {
		t.Errorf("配了 labels 后上游刻度应被压空, got %q", got)
	}

	// a single point: the range must not collapse to 0, or nothing can be drawn
	if _, xmax, _, _ = xAxis([]source.Point{{TS: base, V: 1}}, config.Widget{}); xmax < 1 {
		t.Errorf("单点的时间轴范围至少为 1, got %v", xmax)
	}
	if _, xmax, _, _ = xAxis([]source.Point{{V: 1}}, config.Widget{}); xmax < 1 {
		t.Errorf("单点的序号轴范围至少为 1, got %v", xmax)
	}
}

// TestTimeLayoutFor verifies that the longer the window span, the coarser the automatically
// chosen time format.
func TestTimeLayoutFor(t *testing.T) {
	for _, c := range []struct {
		sec  float64
		want string
	}{
		{5, "15:04:05"},
		{299, "15:04:05"},
		{300, "15:04"},
		{86399, "15:04"},
		{86400, "01-02 15:04"},
	} {
		if got := timeLayoutFor(c.sec); got != c.want {
			t.Errorf("timeLayoutFor(%v) = %q, 期望 %q", c.sec, got, c.want)
		}
	}
}

// clockRe matches a time of day such as "15:04:05".
var clockRe = regexp.MustCompile(`\d\d:\d\d:\d\d`)

// TestChartTimeAxisOnScreen verifies that the timeseries panel's x-axis draws real times of
// day while the array panel draws ordinals.
func TestChartTimeAxisOnScreen(t *testing.T) {
	w := config.Widget{Type: "chart", Source: "s", Title: "C", Height: 8}

	ts := source.SourceView{Type: source.TypeTimeseries, V: source.Value{Type: source.TypeTimeseries,
		Points: []source.Point{
			{TS: time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local), V: 10},
			{TS: time.Date(2026, 9, 8, 15, 30, 10, 0, time.Local), V: 30},
			{TS: time.Date(2026, 9, 8, 15, 30, 20, 0, time.Local), V: 20},
		}}}
	if out := stripANSI(renderWidget(w, ts, 60, 0)); !clockRe.MatchString(out) {
		t.Errorf("timeseries 的 x 轴应是时刻:\n%s", out)
	}

	arr := source.SourceView{Type: source.TypeArray, V: source.Value{Type: source.TypeArray,
		Points: ordinal(10, 30, 20)}}
	if out := stripANSI(renderWidget(w, arr, 60, 0)); clockRe.MatchString(out) {
		t.Errorf("array 的 x 轴应是序号(不该出现时刻):\n%s", out)
	}
}

// TestChartXFormat verifies that x_format overrides the automatically chosen time format.
func TestChartXFormat(t *testing.T) {
	v := source.SourceView{Type: source.TypeTimeseries, V: source.Value{Type: source.TypeTimeseries,
		Points: []source.Point{
			{TS: time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local), V: 10},
			{TS: time.Date(2026, 9, 8, 15, 30, 20, 0, time.Local), V: 30},
		}}}
	w := config.Widget{Type: "chart", Source: "s", Title: "C", Height: 8, XFormat: "15:04"}
	out := stripANSI(renderWidget(w, v, 60, 0))
	if !strings.Contains(out, "15:30") {
		t.Errorf("x_format=15:04 应生效:\n%s", out)
	}
	if clockRe.MatchString(out) {
		t.Errorf("x_format 应覆盖自动格式(不该再出现秒):\n%s", out)
	}
}

// TestChartLabels verifies that category names on the ordinal axis spread left to right in
// point order and align with the points' columns; fewer names falls back to the ordinal;
// wide-character names are not truncated.
func TestChartLabels(t *testing.T) {
	w := config.Widget{Type: "chart", Source: "s", Title: "C", Height: 8,
		Labels: []string{"cpu", "mem", "disk"}}
	arr := source.SourceView{Type: source.TypeArray, V: source.Value{Type: source.TypeArray,
		Points: ordinal(10, 30, 20)}}

	out := stripANSI(renderWidget(w, arr, 60, 0))
	row := labelRow(out)
	at := make([]int, 3)
	for i, name := range []string{"cpu", "mem", "disk"} {
		if at[i] = strings.Index(row, name); at[i] < 0 {
			t.Fatalf("x 轴上应有分类名 %q,刻度行是 %q:\n%s", name, row, out)
		}
	}
	if !(at[0] < at[1] && at[1] < at[2]) {
		t.Errorf("分类名应按点序从左到右排开, got cpu=%d mem=%d disk=%d, 行 %q", at[0], at[1], at[2], row)
	}
	// first name at the left, last name at the right
	if at[0] > 15 || at[2] < 40 {
		t.Errorf("名应与点的列位对齐(首名靠左、末名靠右), got cpu=%d disk=%d, 行 %q", at[0], at[2], row)
	}

	// with timestamped points the axis still shows times of day, and no category names are
	// drawn
	tsv := source.SourceView{Type: source.TypeTimeseries, V: source.Value{Type: source.TypeTimeseries,
		Points: []source.Point{
			{TS: time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local), V: 10},
			{TS: time.Date(2026, 9, 8, 15, 30, 20, 0, time.Local), V: 30},
			{TS: time.Date(2026, 9, 8, 15, 30, 40, 0, time.Local), V: 20},
		}}}
	tout := stripANSI(renderWidget(w, tsv, 60, 0))
	if strings.Contains(tout, "disk") {
		t.Errorf("时间轴不该画分类名:\n%s", tout)
	}
	if !clockRe.MatchString(labelRow(tout)) {
		t.Errorf("时间轴该照常画时刻,刻度行是 %q:\n%s", labelRow(tout), tout)
	}

	// fewer names written: points not covered fall back to the ordinal
	w.Labels = []string{"cpu"}
	row = labelRow(stripANSI(renderWidget(w, arr, 60, 0)))
	for _, s := range []string{"cpu", "1", "2"} {
		if !strings.Contains(row, s) {
			t.Errorf("labels 没覆盖到的点应退回序号,刻度行 %q 缺 %q", row, s)
		}
	}

	// Han characters are wide: one rune takes one cell on the canvas but two in the terminal,
	// so a tick row given no column width is truncated as over-wide
	w.Labels = []string{"磁盘", "内存", "网络"}
	out = stripANSI(renderWidget(w, arr, 60, 0))
	row = labelRow(out)
	if strings.Contains(row, "…") {
		t.Errorf("宽字符名不该被截断,刻度行 %q:\n%s", row, out)
	}
	for _, s := range []string{"磁盘", "内存", "网络"} {
		if !strings.Contains(row, s) {
			t.Errorf("刻度行 %q 缺 %q", row, s)
		}
	}
}

// labelRow returns the panel's second-to-last line: the chart's last line is the x-axis tick
// row, and the next one is the bottom border.
func labelRow(panel string) string {
	lines := strings.Split(strings.TrimRight(panel, "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}
	return lines[len(lines)-2]
}

// —— logs ——

// TestRenderLogsAbsorbsHeight verifies that the logs panel holds more log lines for a given
// height.
func TestRenderLogsAbsorbsHeight(t *testing.T) {
	var logs []source.LogLine
	for i := 0; i < 30; i++ {
		logs = append(logs, source.LogLine{Time: logAt("10:00:00"), Level: "INFO", Msg: "line"})
	}
	v := source.SourceView{Type: source.TypeLogs, Logs: logs}
	w := config.Widget{Title: "日志", Type: "logs"}

	auto := renderLogs(v, w, 60, 0)
	if got := strings.Count(auto, "line"); got != 12 {
		t.Errorf("自适应高度应显示 12 条日志, got %d", got)
	}

	tall := renderLogs(v, w, 60, 20)
	if got := strings.Count(tall, "line"); got != 18 {
		t.Errorf("ch=20 时应显示 18 条日志, got %d", got)
	}
}

// TestLineLimit verifies that max_lines is a cap rather than a last-resort fallback when no
// height is given: it takes effect even when the panel is stretched (ch very large).
func TestLineLimit(t *testing.T) {
	cases := []struct {
		name              string
		ch, maxLines, def int
		want              int
	}{
		{"面板够高但写了上限:取上限", 20, 6, 12, 6},
		{"面板矮于上限:取面板给的", 5, 6, 12, 3},
		{"面板高度与上限相等", 8, 6, 12, 6},
		{"没写上限:装多少显示多少", 20, 0, 12, 18},
		{"没高度但有上限:取上限", 0, 6, 12, 6},
		{"都没有:兜底", 0, 0, 12, 12},
		{"高度只够边框:当没给", 2, 0, 12, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lineLimit(c.ch, c.maxLines, c.def); got != c.want {
				t.Errorf("lineLimit(ch=%d, max=%d, 兜底 %d) = %d, 期望 %d",
					c.ch, c.maxLines, c.def, got, c.want)
			}
		})
	}
}

// TestRenderLogsMaxLinesCaps verifies that a logs panel with max_lines shows only that many
// lines, taken from the tail.
func TestRenderLogsMaxLinesCaps(t *testing.T) {
	var logs []source.LogLine
	for i := 0; i < 30; i++ {
		logs = append(logs, source.LogLine{Time: logAt("10:00:00"), Level: "INFO",
			Msg: fmt.Sprintf("msg-%02d", i)})
	}
	v := source.SourceView{Type: source.TypeLogs, Logs: logs}
	w := config.Widget{Type: "logs", Title: "日志", MaxLines: 6}

	out := stripANSI(renderLogs(v, w, 60, 20))
	if got := strings.Count(out, "msg-"); got != 6 {
		t.Errorf("max_lines: 6 应只显示 6 行, got %d:\n%s", got, out)
	}
	for _, want := range []string{"msg-29", "msg-24"} {
		if !strings.Contains(out, want) {
			t.Errorf("应显示最后 6 行(含 %s):\n%s", want, out)
		}
	}
	if strings.Contains(out, "msg-23") {
		t.Errorf("第 7 行不该显示:\n%s", out)
	}
}

// TestRenderLogsKeepsDiagnostics verifies that when the logs source fails to fetch, that WARN
// is still visible on the panel.
func TestRenderLogsKeepsDiagnostics(t *testing.T) {
	v := source.SourceView{Type: source.TypeLogs, Logs: []source.LogLine{
		{Time: logAt("10:00:00"), Level: "WARN", Msg: "lg: fetch failed: exit status 1"},
	}}
	w := config.Widget{Type: "logs", Source: "lg", Title: "日志"}
	out := stripANSI(renderWidget(w, v, 60, 0))
	if !strings.Contains(out, "fetch failed") {
		t.Errorf("故障应在日志面板上看得见:\n%s", out)
	}
}

// TestLogsTimeFormat verifies that the time column format comes from the logs widget's
// time_format (a Go time layout), defaulting to the second-resolution 15:04:05; the column
// width follows the layout rather than the content — following the content would make the
// whole column jump horizontally when the format changes or the day rolls over.
func TestLogsTimeFormat(t *testing.T) {
	v := source.SourceView{Type: source.TypeLogs, Logs: []source.LogLine{
		{Time: logAt("10:00:01"), Level: "INFO", Msg: "aaa"},
		{Time: logAt("10:00:02"), Level: "WARN", Msg: "aaaaaaaaaaaa"},
	}}
	// msgCol returns the display column where the message body starts in the panel (counted by
	// display width, not a byte index)
	msgCol := func(panel string) []int {
		var out []int
		for _, ln := range strings.Split(panel, "\n") {
			if i := strings.Index(ln, "aaa"); i >= 0 {
				out = append(out, cellWidth(ln[:i]))
			}
		}
		return out
	}
	w := config.Widget{Type: "logs", Title: "日志"}

	def := stripANSI(renderLogs(v, w, 60, 0))
	if !strings.Contains(def, "10:00:01") || strings.Contains(def, "09-08") {
		t.Errorf("默认应仍只到秒:\n%s", def)
	}
	if cols := msgCol(def); len(cols) != 2 || cols[0] != cols[1] {
		t.Errorf("同一格式下正文起始列应一致(与内容长短无关): %v\n%s", cols, def)
	}

	w.TimeFormat = "01-02 15:04:05"
	dated := stripANSI(renderLogs(v, w, 60, 0))
	if !strings.Contains(dated, "09-08 10:00:01") {
		t.Errorf("写了 time_format 就该按它显示:\n%s", dated)
	}
	dc := msgCol(dated)
	if len(dc) != 2 || dc[0] != dc[1] {
		t.Errorf("换格式后正文起始列仍应一致: %v\n%s", dc, dated)
	}
	// the layout is 6 columns longer, so the time column width and the message start column
	// both shift right by 6
	if got, want := dc[0]-msgCol(def)[0], 6; got != want {
		t.Errorf("时间列应随布局变宽 %d 列, got %d:\n%s", want, got, dated)
	}
}

// —— other ——

// TestRenderWidgetUnknown verifies that an unknown widget type renders placeholder text.
func TestRenderWidgetUnknown(t *testing.T) {
	w := config.Widget{Type: "nope", Title: "占位"}
	s := renderWidget(w, source.SourceView{}, 60, 0)
	if !strings.Contains(s, "unknown widget type") {
		t.Errorf("未知类型应显示占位, got %q", s)
	}
}
