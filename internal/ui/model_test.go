package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// stubProvider is a Provider that injects a fixed snapshot, for offline tests of the render logic.
type stubProvider struct {
	views map[string]source.SourceView
	data  bool
	errs  []string
}

func (p stubProvider) Snapshot(name string) (source.SourceView, bool) {
	v, ok := p.views[name]
	return v, ok
}
func (p stubProvider) Snapshots() map[string]source.SourceView { return p.views }
func (p stubProvider) HasData() bool                           { return p.data }
func (p stubProvider) Errors() []string                        { return p.errs }

// testLayout is a layout covering all 7 widget types. Each source type matches the widget that
// uses it: the logs panel does not accept a map source, so it uses lg alone.
func testLayout() []config.Row {
	return []config.Row{
		{Widgets: []config.Widget{
			{Type: "stat", Source: "sim", Title: "关键指标", Value: "{{.qps}}", Format: "%.0f /s"},
			{Type: "gauge", Source: "sim", Title: "磁盘占用", Value: "{{.disk}}", Max: 100},
		}},
		{Widgets: []config.Widget{
			{Type: "chart", Source: "sim", Title: "QPS 趋势", Value: "{{.qps}}", Height: 6},
			{Type: "bar", Source: "sim", Title: "错误数", Value: "{{.errors}}", Height: 6},
		}},
		{Widgets: []config.Widget{
			{Type: "table", Source: "svc", Title: "服务状态", Value: "{{.rows}}", Columns: []config.Column{{Key: "name", Title: "服务"}, {Key: "status", Title: "状态"}}},
			{Type: "logs", Source: "lg", MaxLines: 6},
		}},
		{Widgets: []config.Widget{
			{Type: "text", Source: "ptr", Title: "说明", MaxLines: 6},
		}},
	}
}

// testViews builds the snapshot data corresponding to testLayout.
func testViews() map[string]source.SourceView {
	return map[string]source.SourceView{
		// map source: qps/errors each carry a sequence accumulated across frames; stat takes the
		// sequence's last point, 1234, and chart draws the whole series
		"sim": mapView(
			map[string]any{"qps": float64(1234), "disk": float64(73)},
			map[string][]source.Point{"qps": series(1000, 1100, 1234), "errors": series(1, 4, 2)},
		),
		"svc": tableView([]map[string]any{
			{"name": "api-gateway", "status": "UP"},
			{"name": "payment-svc", "status": "DEGRADED"},
		}),
		"lg": {Type: source.TypeLogs, Logs: []source.LogLine{
			{Time: logAt("10:00:01"), Level: "INFO", Msg: "order#1001 创建成功"},
			{Time: logAt("10:00:02"), Level: "ERROR", Msg: "payment#777 timeout"},
		}},
		"ptr": {Type: source.TypeText, V: source.Value{Type: source.TypeText,
			Text: "欢迎使用 dashboard\n数据由脚本提供"},
			Text: "欢迎使用 dashboard\n数据由脚本提供"},
	}
}

// TestViewRendersAllWidgets verifies that View() output contains the key content of every widget.
func TestViewRendersAllWidgets(t *testing.T) {
	m := &model{
		provider: stubProvider{views: testViews(), data: true},
		layout:   testLayout(),
		poll:     100 * time.Millisecond,
		width:    120, height: 40,
	}
	view := m.View().Content
	for _, kw := range []string{
		"关键指标", "1234 /s", "磁盘占用", // stat + gauge
		"QPS 趋势",                                         // chart (ntcharts braille polyline output lines)
		"服务状态", "api-gateway", "payment-svc", "DEGRADED", // table
		"order#1001", "ERROR", "payment#777", // logs
		"欢迎使用 dashboard",
	} {
		if !strings.Contains(view, kw) {
			t.Errorf("View() 缺关键字 %q", kw)
		}
	}
}

// TestViewInitializingPlaceholder verifies that an initialization placeholder is shown while the
// sources are not ready.
func TestViewInitializingPlaceholder(t *testing.T) {
	m := &model{
		provider: stubProvider{data: false},
		layout:   testLayout(),
		width:    120, height: 40,
	}
	if v := m.View().Content; !strings.Contains(v, "loading data sources") {
		t.Errorf("未就绪应显示初始化占位, got %q", v)
	}
}

// TestViewErrorBanner verifies that the footer renders the source error banner.
func TestViewErrorBanner(t *testing.T) {
	m := &model{
		provider: stubProvider{views: testViews(), data: true, errs: []string{"⚠ bad: boom"}},
		layout:   testLayout(),
		width:    120, height: 40,
	}
	if v := m.View().Content; !strings.Contains(v, "boom") {
		t.Errorf("页脚应显示错误条, got %q", v)
	}
}

// TestViewErrorBannerClamped verifies that an overlong error banner is truncated to the screen
// width rather than left to wrap.
//
// That line is fixed at the bottom of the screen and comes entirely from a source (the raw stderr
// length has no upper bound); left too wide, terminal soft wrap would fold it onto the next line
// and push the other lines off.
func TestViewErrorBannerClamped(t *testing.T) {
	const w = 60
	long := "⚠ metrics: fetch failed: exit status 1 (stderr: " + strings.Repeat("boom ", 40) + ")"
	m := &model{
		provider: stubProvider{views: testViews(), data: true, errs: []string{long}},
		layout:   testLayout(),
		width:    w, height: 40,
	}
	out := stripANSI(m.View().Content)
	for i, ln := range strings.Split(out, "\n") {
		if got := cellWidth(ln); got > w {
			t.Errorf("第 %d 行 %d 列, 超出屏宽 %d: %q", i, got, w, ln)
		}
	}
	if !strings.Contains(out, "…") {
		t.Errorf("超长错误条应被截断补 …, got %q", out)
	}
}

// TestViewNarrowWindow verifies that degradation in a narrow window does not panic and that the
// content is still present.
func TestViewNarrowWindow(t *testing.T) {
	m := &model{
		provider: stubProvider{views: testViews(), data: true},
		layout:   testLayout(),
		width:    60, height: 40,
	}
	v := m.View().Content
	for _, kw := range []string{"关键指标", "api-gateway", "order#1001"} {
		if !strings.Contains(v, kw) {
			t.Errorf("窄窗口缺关键字 %q", kw)
		}
	}
}

// TestScrollReusesCachedContent verifies that pure scrolling does not recompose: even though the
// data has already changed between two ticks, the scrolling window still shows the previously
// laid-out content, and only an arriving tick triggers a whole-page recompose.
func TestScrollReusesCachedContent(t *testing.T) {
	st := stubProvider{data: true}
	st.views = map[string]source.SourceView{
		"src": {Text: strings.Repeat("A行\n", 30)}, // far beyond the viewport, necessarily over tall
	}
	m := &model{
		provider: st,
		layout:   []config.Row{{Widgets: []config.Widget{{Type: "text", Source: "src", Title: "T"}}}},
		width:    100, height: 12,
	}
	if v := m.View().Content; !strings.Contains(v, "A行") {
		t.Fatalf("初始应显示 A 内容:\n%s", v)
	}

	// Data swapped to B (without a tick) and then scrolled: the cache must be reused, so the
	// window still holds A
	st.views["src"] = source.SourceView{Text: strings.Repeat("B行\n", 30)}
	for i := 0; i < 3; i++ {
		nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = nm.(*model)
	}
	if v := m.View().Content; strings.Contains(v, "B行") {
		t.Errorf("tick 之间滚动不应重排出新数据:\n%s", v)
	} else if !strings.Contains(v, "A行") {
		t.Errorf("滚动窗口应仍是缓存的 A 内容:\n%s", v)
	}

	// After the tick arrives the page recomposes; returning to the top reveals B
	mm, _ := m.Update(tickMsg(time.Now()))
	m = mm.(*model)
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = nm.(*model)
	if v := m.View().Content; !strings.Contains(v, "B行") {
		t.Errorf("tick 后应重排出新数据 B:\n%s", v)
	}
}

// TestHelpToggle verifies that ? shows or hides the keyboard-help overlay, that scroll keys do not
// pass through while the overlay is open, and that q still quits.
func TestHelpToggle(t *testing.T) {
	m := &model{
		provider: stubProvider{views: testViews(), data: true},
		layout:   testLayout(),
		width:    120, height: 40,
	}
	m.View() // builds the render cache
	if v := m.View().Content; strings.Contains(v, "快捷键") {
		t.Fatalf("初始不应显示帮助:\n%s", v)
	}

	// Pressing ? brings up the overlay
	mm, _ := m.Update(tea.KeyPressMsg{Code: '?'})
	m = mm.(*model)
	v := m.View().Content
	for _, kw := range []string{"Keyboard help", "↑ / k", "Ctrl+B", "Ctrl+F", "Ctrl+D", "Home / End", "q / Ctrl+C", "mouse wheel"} {
		if !strings.Contains(v, kw) {
			t.Errorf("帮助浮层缺关键字 %q", kw)
		}
	}

	// With the overlay open, j does not pass through to scroll
	before := m.scroll
	for i := 0; i < 5; i++ {
		mm, _ = m.Update(tea.KeyPressMsg{Code: 'j'})
		m = mm.(*model)
	}
	if m.scroll != before {
		t.Errorf("帮助浮层下 j 不应滚动下层: scroll %d → %d", before, m.scroll)
	}

	// Pressing ? again dismisses it
	mm, _ = m.Update(tea.KeyPressMsg{Code: '?'})
	m = mm.(*model)
	if v := m.View().Content; strings.Contains(v, "Keyboard help") {
		t.Errorf("再按 ? 应收起帮助:\n%s", v)
	}

	// With the overlay still open, q quits
	mm, _ = m.Update(tea.KeyPressMsg{Code: '?'})
	m = mm.(*model)
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q'}); cmd == nil {
		t.Errorf("帮助浮层打开时按 q 也应退出")
	}
}

// TestModelCtrlPageScroll verifies the vim page keys: Ctrl+B/F page up/down (like PgUp/PgDn) and
// Ctrl+D/U half a page, all clamped without overshooting; d with no Ctrl modifier does not fire;
// and Ctrl+C quits under both the legacy (Code) and kitty (Text) reporting forms.
func TestModelCtrlPageScroll(t *testing.T) {
	st := stubProvider{data: true}
	st.views = map[string]source.SourceView{
		"src": {Text: strings.Repeat("行\n", 60)}, // far beyond the viewport, necessarily over tall
	}
	m := &model{
		provider: st,
		// MaxLines raises the renderer's default 20-line floor; otherwise the content is under one
		// page and maxOff cannot exceed pageRows
		layout: []config.Row{{Widgets: []config.Widget{{Type: "text", Source: "src", Title: "T", MaxLines: 60}}}},
		width:  100, height: 12,
	}
	if v := m.View().Content; !strings.Contains(v, "行") {
		t.Fatalf("初始应显示内容:\n%s", v)
	}
	rows := m.bodyRows(0)
	if m.pageRows != rows {
		t.Fatalf("超高时 pageRows 应为可视行数 %d, got %d", rows, m.pageRows)
	}
	if m.maxOff <= m.pageRows {
		t.Fatalf("超高内容应留出翻页余量 maxOff=%d pageRows=%d", m.maxOff, m.pageRows)
	}
	half := m.pageRows / 2
	press := func(m *model, msg tea.KeyPressMsg) *model {
		nm, _ := m.Update(msg)
		return nm.(*model)
	}
	// Ctrl+F pages down, Ctrl+U pages up half a page, and Ctrl+D halves back down to the start
	m = press(m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if m.scroll != m.pageRows {
		t.Errorf("Ctrl+F 应整页下翻到 %d, got %d", m.pageRows, m.scroll)
	}
	m = press(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.scroll != m.pageRows-half {
		t.Errorf("Ctrl+U 应上翻半页到 %d, got %d", m.pageRows-half, m.scroll)
	}
	m = press(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if m.scroll != m.pageRows {
		t.Errorf("Ctrl+D 应下翻半页回到 %d, got %d", m.pageRows, m.scroll)
	}
	// d with no Ctrl modifier does not scroll and stays put
	m = press(m, tea.KeyPressMsg{Code: 'd'})
	if m.scroll != m.pageRows {
		t.Errorf("纯字母 d 不应滚动, got scroll=%d", m.scroll)
	}
	// Ctrl+B pages up back to the top; pressed again at the top it does not overshoot
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.scroll != 0 {
		t.Errorf("Ctrl+B 应整页上翻回顶, got %d", m.scroll)
	}
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.scroll != 0 {
		t.Errorf("到顶后 Ctrl+B 应停在 0, got %d", m.scroll)
	}
	// Ctrl+D at the bottom and Ctrl+U at the top both clamp without overshooting
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.scroll != m.maxOff {
		t.Fatalf("End 应滚到底 %d, got %d", m.maxOff, m.scroll)
	}
	m = press(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if m.scroll != m.maxOff {
		t.Errorf("到底后 Ctrl+D 应夹在 %d, got %d", m.maxOff, m.scroll)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.scroll != 0 {
		t.Fatalf("Home 应到顶, got %d", m.scroll)
	}
	m = press(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.scroll != 0 {
		t.Errorf("到顶后 Ctrl+U 应停在 0, got %d", m.scroll)
	}
	// Ctrl+C must quit under either reporting form
	for _, msg := range []tea.KeyPressMsg{
		{Code: 'c', Mod: tea.ModCtrl},
		{Text: "c", Mod: tea.ModCtrl, Code: 0, BaseCode: 'c'},
	} {
		if _, cmd := m.Update(msg); cmd == nil {
			t.Errorf("Ctrl+C(%+v) 应退出, 得到 nil cmd", msg)
		}
	}
}

// TestRowWidths verifies that fixed-width columns take precedence and flexible columns split the
// remainder evenly.
func TestRowWidths(t *testing.T) {
	widgets := []config.Widget{
		{Title: "a", Width: 30},
		{Title: "b"},
		{Title: "c"},
	}
	got := rowWidths(widgets, 120)
	want := []int{30, 45, 45}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rowWidths[%d]=%d, 期望 %d (got=%v)", i, got[i], want[i], got)
		}
	}
}

// —— border width: lipgloss .Width(w)'s total width is w+2, so Panel must subtract 2 ——
// The title is drawn on the border; counting by rune undercounts the real column width once CJK is
// present, so every width assertion uses cellWidth.

// TestPanelTotalWidth verifies that Panel renders exactly at the configured width (terminal
// columns) with no extra overflow.
func TestPanelTotalWidth(t *testing.T) {
	for _, w := range []int{40, 60, 119, 121} {
		r := stripANSI(Panel("stat", "标题", "内容", w, 0))
		first := strings.SplitN(r, "\n", 2)[0]
		if got := cellWidth(first); got != w {
			t.Errorf("Panel(width=%d) 渲染宽 %d", w, got)
		}
	}
}

// TestPanelBorderTitleOnTop verifies that the title is drawn on the top border: line 0 is the
// border carrying the title, line 1 is content already, and there is no separate title line inside
// the box.
func TestPanelBorderTitleOnTop(t *testing.T) {
	r := strings.Split(stripANSI(Panel("stat", "标题", "内容", 40, 0)), "\n")
	if len(r) < 2 {
		t.Fatalf("Panel 行数过少: %d", len(r))
	}
	top, body := r[0], r[1]
	if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
		t.Errorf("第 0 行应是顶边框: %q", top)
	}
	if !strings.Contains(top, "标题") {
		t.Errorf("标题应出现在顶边框上: %q", top)
	}
	if strings.Contains(body, "标题") {
		t.Errorf("框内不应再出现标题行: %q", body)
	}
	if !strings.Contains(body, "内容") {
		t.Errorf("内容缺失: %q", body)
	}
}

// TestPanelNoTitlePureBorder verifies that with no title a pure border is rendered and the content
// still appears.
func TestPanelNoTitlePureBorder(t *testing.T) {
	r := strings.Split(stripANSI(Panel("stat", "", "内容", 20, 0)), "\n")
	top := r[0]
	if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
		t.Errorf("无标题也应有边框: %q", top)
	}
	// The content appears in one of the lines
	found := false
	for _, ln := range r {
		if strings.Contains(ln, "内容") {
			found = true
		}
	}
	if !found {
		t.Errorf("内容缺失")
	}
}

// TestPanelBorderTitleLong verifies that a title wider than the box is truncated with an appended
// …, leaving the top edge width unchanged.
func TestPanelBorderTitleLong(t *testing.T) {
	long := strings.Repeat("长", 60) // 120 columns, far wider than the box
	r := stripANSI(Panel("stat", long, "内容", 40, 0))
	top := strings.SplitN(r, "\n", 2)[0]
	if got := cellWidth(top); got != 40 {
		t.Errorf("超长标题应保持盒宽 40, 得到 %d", got)
	}
	if !strings.Contains(top, "…") {
		t.Errorf("超长标题应截断补 …, 得到 %q", top)
	}
}

// TestRenderStatTitle verifies that stat rendering carries the widget-level title through.
func TestRenderStatTitle(t *testing.T) {
	w := config.Widget{
		Type: "stat", Source: "sim",
		Title: "Requests",
		Value: "{{.qps}}", Format: "%.0f /s",
	}
	v := mapView(map[string]any{"qps": float64(9)}, nil)
	if s := renderStat(v, w, 40, 0); !strings.Contains(s, "Requests") {
		t.Errorf("渲染应含标题 %q, got %q", "Requests", s)
	}
}

// TestRowWidthsFill verifies that the flexible columns sum to exactly fill total (the remainder is
// spread over the leading columns).
func TestRowWidthsFill(t *testing.T) {
	got := rowWidths([]config.Widget{{Title: "a"}, {Title: "b"}, {Title: "c"}, {Title: "d"}}, 239)
	sum := 0
	for _, g := range got {
		sum += g
	}
	if sum != 239 {
		t.Errorf("行宽总和 %d, 期望 239 (got=%v)", sum, got)
	}
}

// TestViewRowsFit verifies that a rendered row does not exceed the target width and that the
// rightmost ╮ is complete.
func TestViewRowsFit(t *testing.T) {
	m := &model{
		provider: stubProvider{views: testViews(), data: true},
		layout:   testLayout(),
		poll:     100,
		width:    239, height: 40,
	}
	for _, ln := range strings.Split(stripANSI(m.View().Content), "\n") {
		if strings.HasPrefix(ln, "╭") {
			if cellWidth(ln) > 239 {
				t.Errorf("行超宽 %d > 239", cellWidth(ln))
			}
		}
	}
}
