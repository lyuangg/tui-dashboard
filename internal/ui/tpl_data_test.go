package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// —— template data layer: tplData / mergeTpl / withFrame / row-title alignment / cross-source ——

// tplFixtures returns the fixed instant and source snapshots shared by this file.
func tplFixtures() (time.Time, map[string]source.SourceView) {
	t := time.Date(2026, 9, 9, 14, 23, 5, 0, time.Local)
	views := map[string]source.SourceView{
		"mem":     mapView(map[string]any{"used_pct": float64(64), "qps": float64(1234)}, nil),
		"weather": {Text: "北京 ☀️ +26°C\n第二行"},
		"svc":     mapView(map[string]any{"value": float64(42.5)}, nil),
	}
	return t, views
}

func TestTplDataBuiltinsAndSources(t *testing.T) {
	now, views := tplFixtures()
	f := tplData(now, views)

	if f["date"] != "2026-09-09" {
		t.Errorf("date = %v, 期望 2026-09-09", f["date"])
	}
	if f["weekday"] != "Wed" {
		t.Errorf("weekday = %v, 期望 Wed", f["weekday"])
	}
	if f["time"] != "14:23:05" {
		t.Errorf("time = %v, 期望 14:23:05", f["time"])
	}
	// A map source is injected whole as doc under its source name; a text source has its
	// whitespace merged into one line
	if !reflect.DeepEqual(f["mem"], views["mem"].Doc) {
		t.Errorf("mem 源应注入整个 doc, got %#v", f["mem"])
	}
	if f["weather"] != "北京 ☀️ +26°C 第二行" {
		t.Errorf("weather 文本源应归并成单行, got %#v", f["weather"])
	}
	if !reflect.DeepEqual(f["svc"], views["svc"].Doc) {
		t.Errorf("svc 源应注入整个 doc, got %#v", f["svc"])
	}
}

func TestTplDataSkipsReservedSource(t *testing.T) {
	// A source named date/weekday/time is skipped: the built-in value always wins
	now, views := tplFixtures()
	views["date"] = source.SourceView{Text: "冒充日期"}
	views["time"] = source.SourceView{Doc: map[string]any{"x": float64(1)}}
	f := tplData(now, views)
	if f["date"] != "2026-09-09" {
		t.Errorf("date 应仍为内置日期, got %#v", f["date"])
	}
	if f["weekday"] != "Wed" {
		t.Errorf("weekday = %v, 期望 Wed", f["weekday"])
	}
	if f["time"] != "14:23:05" {
		t.Errorf("time 应仍为内置时刻, got %#v", f["time"])
	}
}

// TestWeekdayName verifies that weekday names are always English, which is the value of the
// built-in .weekday.
func TestWeekdayName(t *testing.T) {
	// 2026-09-06 is a Sunday; counting seven days covers the whole table, both ends included
	// (Sunday and Saturday)
	sun := time.Date(2026, 9, 6, 0, 0, 0, 0, time.Local)
	want := [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	for i, w := range want {
		d := sun.AddDate(0, 0, i)
		if got := weekdayName(d.Weekday()); got != w {
			t.Errorf("%s 星期 = %q, 期望 %q", d.Format("2006-01-02"), got, w)
		}
	}
}

func TestMergeTplPrecedence(t *testing.T) {
	frame := map[string]any{
		"date":    "2026-09-09",
		"weather": "☀️", // not a reserved key, so own may override it
		"qps":     float64(1),
	}
	own := map[string]any{
		"date":    "应被内置赢掉",
		"weather": "🌧",
		"qps":     float64(1234), // own doc's top-level key overrides the same-named key in the frame
	}
	m := mergeTpl(frame, own)
	if m["date"] != "2026-09-09" {
		t.Errorf("date 内置应恒赢: %#v", m["date"])
	}
	if m["weather"] != "🌧" {
		t.Errorf("非保留键 own 应覆盖 frame: %#v", m["weather"])
	}
	if m["qps"] != float64(1234) {
		t.Errorf("own 顶层键应覆盖 frame: %#v", m["qps"])
	}
	// mergeTpl does not modify frame
	if frame["weather"] != "☀️" {
		t.Errorf("mergeTpl 不应改 frame: %#v", frame)
	}
	// An empty frame reuses own directly; an empty own copies frame
	back := mergeTpl(nil, own)
	back["mut"] = 1
	if own["mut"] != 1 {
		t.Errorf("frame 空应原样返回 own(复用同一张 map)")
	}
	o := mergeTpl(frame, nil)
	if len(o) != 3 || o["qps"] != float64(1) {
		t.Errorf("own 空应拷贝 frame: %#v", o)
	}
}

func TestWithFrame(t *testing.T) {
	now, views := tplFixtures()
	f := tplData(now, views)

	// frame non-empty: returns a shallow copy whose Doc is a freshly merged map, so modifying it
	// leaves the original map untouched
	v := views["mem"]
	orig := v.Doc
	out := withFrame(f, v)
	out.Doc["mut"] = 1
	if _, seen := orig["mut"]; seen {
		t.Errorf("应返回合并后的新 Doc,而非原 map")
	}
	delete(out.Doc, "mut")
	if out.Doc["used_pct"] != float64(64) {
		t.Errorf("自身源字段应保留: %#v", out.Doc["used_pct"])
	}
	if out.Doc["weather"] != "北京 ☀️ +26°C 第二行" {
		t.Errorf("跨源字段应并入: %#v", out.Doc["weather"])
	}
	if out.Doc["date"] != "2026-09-09" {
		t.Errorf("内置字段应并入: %#v", out.Doc["date"])
	}
	if len(orig) != 2 {
		t.Errorf("原 Doc 不应被改动: %#v", orig)
	}

	// An empty frame returns v as-is
	plain := withFrame(nil, v)
	plain.Doc["mut"] = 1
	if _, seen := orig["mut"]; !seen {
		t.Errorf("frame 空应原样返回 v(复用同一张 map)")
	}
	delete(plain.Doc, "mut")

	// v has no Doc (a plain text source): after merging, Doc is a copy of frame
	noDoc := source.SourceView{Text: "raw"}
	out2 := withFrame(f, noDoc)
	if out2.Doc["date"] != "2026-09-09" || out2.Doc["weather"] == "" {
		t.Errorf("无 doc 的源也应拿到帧: %#v", out2.Doc)
	}
}

func TestSectionTitleLeftByteIdentical(t *testing.T) {
	// The left and empty alignments must be byte-for-byte identical to
	// sectionStyle().Render(title), with no padding
	title := "概览 2026-09-09 周三"
	base := sectionStyle().Render(title)
	for _, align := range []string{"", "left"} {
		if got := sectionTitle(title, align, 120); got != base {
			t.Errorf("sectionTitle(align=%q) 应逐字节等于 sectionStyle().Render, 得到 %q", align, got)
		}
	}
	// With width<=0 even center adds no padding
	if got := sectionTitle(title, "center", 0); got != base {
		t.Errorf("width<=0 不应补空格: %q", got)
	}
}

func TestSectionTitleAlignPositions(t *testing.T) {
	title := "标题" // visible width 2
	vw := visibleWidth(title)
	const width = 40
	idx := func(align string) int {
		line := stripANSI(sectionTitle(title, align, width))
		return strings.Index(line, title)
	}
	if i := idx("center"); i != (width-vw)/2 {
		t.Errorf("center 标题起始列 %d, 期望 %d", i, (width-vw)/2)
	}
	if i := idx("right"); i != width-vw {
		t.Errorf("right 标题起始列 %d, 期望 %d", i, width-vw)
	}
	// A title wider than the content passes through as-is, with no negative padding
	if got := sectionTitle("这是一个很长的行标题标题标题", "center", 4); !strings.HasPrefix(stripANSI(got), "这是一个很长的") {
		t.Errorf("标题超宽应原样放行: %q", got)
	}
}

// TestRowSectionDefaultMatchesSectionTitle verifies that a row title with no color/bg configured
// goes through sectionTitle, byte for byte identically.
func TestRowSectionDefaultMatchesSectionTitle(t *testing.T) {
	row := config.Row{} // both color and bg empty, so it takes the default path
	title := "概览 2026-09-09 周三"
	for _, align := range []string{"", "left", "center", "right"} {
		got := rowSection(row, title, align, 120)
		if want := sectionTitle(title, align, 120); got != want {
			t.Errorf("rowSection 缺省(align=%q) 应逐字节等于 sectionTitle", align)
		}
	}
}

// TestSectionBandWidth verifies that with bg configured the whole line, alignment padding
// included, is filled with a color band; with fg alone the spacing matches sectionTitle and the
// line is not padded to the full width.
func TestSectionBandWidth(t *testing.T) {
	title := "标题" // visible width 2
	vw := visibleWidth(title)
	const width = 40
	fg, _ := resolveSpec("red")

	col := func(align string) int {
		line := stripANSI(sectionBand(title, align, width, fg, lipgloss.Color("52")))
		return strings.Index(line, title)
	}
	for _, c := range []struct {
		align string
		want  int
	}{
		{"left", 0},
		{"center", (width - vw) / 2},
		{"right", width - vw},
	} {
		line := stripANSI(sectionBand(title, c.align, width, fg, lipgloss.Color("52")))
		if got := cellWidth(line); got != width {
			t.Errorf("色带(align=%q) 剥 ANSI 后宽 %d, 期望铺满 %d", c.align, got, width)
		}
		if i := col(c.align); i != c.want {
			t.Errorf("色带(align=%q) 标题起始列 %d, 期望 %d", c.align, i, c.want)
		}
	}

	// Text color only (bg=nil): the spacing matches sectionTitle and the line is not padded to the
	// full width
	colorOnly := stripANSI(sectionBand(title, "center", width, fg, nil))
	if want := stripANSI(sectionTitle(title, "center", width)); colorOnly != want {
		t.Errorf("color-only center 应与 sectionTitle 间距一致: %q vs %q", colorOnly, want)
	}
	if got := cellWidth(colorOnly); got >= width {
		t.Errorf("color-only 不应把行补到整宽(宽 %d)", got)
	}
}

// TestRowSectionBandFromConfig verifies that a row title with color/bg configured takes the band
// path: the text sits according to title_align and the background fills the width.
func TestRowSectionBandFromConfig(t *testing.T) {
	row := config.Row{Color: "yellow", BG: "#2e2e2e"} // an empty TitleAlign means left
	title := "区块"
	line := stripANSI(rowSection(row, title, "", 50))
	if got := cellWidth(line); got != 50 {
		t.Errorf("配 bg 的行标题宽 %d, 期望铺满 50", got)
	}
	if i := strings.Index(line, title); i != 0 {
		t.Errorf("left 行标题应从行首起, 起始列 %d", i)
	}
	// A row with color only changes the text color and does not fill the background
	colorOnly := stripANSI(rowSection(config.Row{Color: "red"}, title, "", 50))
	if cellWidth(colorOnly) != visibleWidth(title) {
		t.Errorf("只配 color 的行标题应只有文字本身, 宽 %d", cellWidth(colorOnly))
	}
}

// TestRowTitleBuiltinsOnly verifies that a row title uses built-in fields only: sources are used
// inside widgets alone, so both {{.weather}} (a source name) and {{.qps}} (the source of a widget
// in the row) in a row title resolve to nothing.
func TestRowTitleBuiltinsOnly(t *testing.T) {
	now, views := tplFixtures()
	f := tplData(now, views)
	row := config.Row{
		// Only single-level fields are written: a multi-level field that resolves to nothing makes
		// the whole title fall back (see TestRowTitleChainedFieldFallsBack)
		Title: "{{.date}} {{.weekday}} {{.weather}} {{.qps}}",
		Widgets: []config.Widget{
			{Type: "stat", Source: "mem", Value: "{{.qps}}", Title: "内存"},
		},
	}
	got := rowTitle(row, titleData(f))
	// Built-in fields resolve as usual
	for _, want := range []string{"2026-09-09", "Wed"} {
		if !strings.Contains(got, want) {
			t.Errorf("行标题应含内置字段 %q: %q", want, got)
		}
	}
	// Sources (by name, or a widget's own source within the row) all resolve to nothing; the row
	// keeps a full widget entry to cover the latter
	for _, ban := range []string{"北京", "☀️", "1234"} {
		if strings.Contains(got, ban) {
			t.Errorf("行标题不该取到数据源的 %q: %q", ban, got)
		}
	}
}

// TestRowTitleChainedFieldFallsBack verifies that when a multi-level field (.source.field)
// resolves to nothing, the whole template execution errors out, RenderTpl falls back as-is, and
// {{.svc.value}} enters the title as a literal.
//
// This is the edge of missingkey=zero: a missing key yields nil, and taking a field from nil then
// errors; the widget side behaves the same way.
func TestRowTitleChainedFieldFallsBack(t *testing.T) {
	now, views := tplFixtures()
	f := tplData(now, views)
	got := rowTitle(config.Row{Title: "概览 {{.svc.value}}"}, titleData(f))
	if !strings.Contains(got, "{{.svc.value}}") {
		t.Errorf("多层字段取不到时应原样回退, got %q", got)
	}
}

// TestTitleData verifies that row-title data holds the three built-ins only, and that a nil frame
// returns an empty map.
func TestTitleData(t *testing.T) {
	now, views := tplFixtures()
	f := tplData(now, views)

	base := titleData(f)
	if len(base) != len(templateReserved) {
		t.Errorf("titleData 应只留内置三键, got %v", base)
	}
	for _, k := range []string{"date", "weekday", "time"} {
		if base[k] != f[k] {
			t.Errorf("内置 %s 应原样带过: %v", k, base[k])
		}
	}
	for _, k := range []string{"mem", "weather", "svc"} {
		if _, ok := base[k]; ok {
			t.Errorf("数据源 %q 不该出现在行标题数据里", k)
		}
	}
	// Modifying the returned value must not affect frame
	base["date"] = "改了"
	if f["date"] != "2026-09-09" {
		t.Errorf("titleData 返回的应是副本, frame 被改到了: %v", f["date"])
	}
	if got := titleData(nil); len(got) != 0 {
		t.Errorf("frame 为 nil 应得到空 map, got %v", got)
	}
	// A frame missing a field does not have that key filled with an empty string
	if got := titleData(map[string]any{"date": "2026-09-09"}); len(got) != 1 || got["date"] != "2026-09-09" {
		t.Errorf("部分帧应只带有的键, got %v", got)
	}
}

// TestComposeWideCrossSourceBuiltin verifies that a row title and a widget title share the same
// global frame but see different data: a widget title may cross sources ({{.svc.value}}) and use
// the built-in weekday, while a row title has built-in fields only.
func TestComposeWideCrossSourceBuiltin(t *testing.T) {
	now, views := tplFixtures()
	f := tplData(now, views)
	layout := []config.Row{{
		Title:      "概览 {{.date}} · {{.weather}}",
		TitleAlign: "center",
		Widgets: []config.Widget{
			{Type: "stat", Source: "mem", Value: "{{.qps}}",
				Title: "{{.svc.value}} {{.weekday}} 内存", Format: "%.0f"},
		},
	}}
	out := composeWide(layout, views, 120, 30, f)
	lines := strings.Split(stripANSI(out), "\n")
	// The row-title line holds the date; the widget-title line holds the cross-source svc.value
	// and the built-in weekday
	var section, wtitle string
	for _, ln := range lines {
		if strings.Contains(ln, "概览") && section == "" {
			section = ln
		}
		if strings.Contains(ln, "内存") {
			wtitle = ln
		}
	}
	// The row title stays whole, with only {{.weather}} rendering empty
	if !strings.Contains(section, "2026-09-09") {
		t.Errorf("行标题应含内置日期: %q", section)
	}
	if strings.Contains(section, "北京") {
		t.Errorf("行标题不该取到数据源(天气): %q", section)
	}
	if !strings.Contains(wtitle, "42.5") || !strings.Contains(wtitle, "Wed") {
		t.Errorf("widget 标题应含跨源值与内置星期: %q", wtitle)
	}
	// The stat value comes from its own source, unaffected by the frame merge
	if !strings.Contains(out, "1234") {
		t.Errorf("stat 数值应照常取自身源: %q", out)
	}
}

// TestSectionCountDropsSourceOnlyTitle verifies that a title built from source fields alone renders
// empty and that sectionCount does not count it as a line — that value feeds composeWide's
// remaining-height distribution.
func TestSectionCountDropsSourceOnlyTitle(t *testing.T) {
	now, views := tplFixtures()
	base := titleData(tplData(now, views))
	layout := []config.Row{
		{Title: "{{.weather}}"},           // entirely from the source, renders empty
		{Title: "{{.date}} {{.weather}}"}, // still has the built-in date
		{Title: "静态标题"},                   // contains no template
	}
	if got := sectionCount(layout, base); got != 2 {
		t.Errorf("sectionCount = %d, 期望 2(纯数据源的那条标题渲染成空、不占行)", got)
	}
}

// TestRenderWidgetFrameNilOwnDoc verifies that calling the renderer directly (without a frame)
// takes the widget's own doc only, with the template applied plainly per its original semantics.
func TestRenderWidgetFrameNilOwnDoc(t *testing.T) {
	w := config.Widget{Type: "stat", Source: "mem", Value: "{{.qps}}",
		Title: "{{.qps}} 内存", Format: "%.0f"}
	v := mapView(map[string]any{"qps": float64(7)}, nil)
	out := renderWidget(w, v, 40, 0)
	if !strings.Contains(stripANSI(out), "7") {
		t.Errorf("直调渲染器仍应取自身 doc 的 .qps: %q", stripANSI(out))
	}
}
