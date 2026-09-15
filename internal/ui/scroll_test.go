package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"

	"tui-dashboard/internal/config"
)

// —— whole-page vertical scrolling: clipScroll geometry and model keys ——

// TestClipScrollNoOverflowUnchanged verifies that content which fits is returned unchanged with
// the offset zeroed, and that rows<=0 zeroes the offset as well.
func TestClipScrollNoOverflowUnchanged(t *testing.T) {
	body := "a\nb\nc"
	view, off := clipScroll(body, 5, 10, 20)
	if view != body || off != 0 {
		t.Errorf("未超高应原样返回, got view=%q off=%d", view, off)
	}
	if _, off := clipScroll(body, -3, 0, 20); off != 0 {
		t.Errorf("rows<=0(高度未知)应 off=0, got %d", off)
	}
}

// TestClipScrollWindowAndClamp verifies that overflowing content clamps the offset, cuts exactly
// rows lines, and reserves the rightmost column as the scrollbar slot.
func TestClipScrollWindowAndClamp(t *testing.T) {
	var ls []string
	for i := 0; i < 12; i++ {
		ls = append(ls, fmt.Sprintf("row%02d", i))
	}
	body := strings.Join(ls, "\n")

	// Top: offset 0, visible from row00 on
	view, off := clipScroll(body, 0, 5, 10)
	if off != 0 || !strings.Contains(view, "row00") {
		t.Errorf("顶部 off=0 应见 row00, got off=%d\n%s", off, view)
	}

	// Clamp: max offset 7, a request of 9 yields 7, and the window is the last 5 lines
	view, off = clipScroll(body, 9, 5, 10)
	if off != 7 {
		t.Errorf("请求 9 应夹到 7, got %d", off)
	}
	lines := strings.Split(view, "\n")
	if len(lines) != 5 {
		t.Fatalf("窗口应为 5 行, got %d", len(lines))
	}
	if !strings.Contains(view, "row11") || !strings.Contains(view, "row07") {
		t.Errorf("底部窗口应含 row07..row11:\n%s", view)
	}
	// Each line is 10 wide: 9 columns of content plus the rightmost scrollbar slot
	for i, ln := range lines {
		if cellWidth(ln) != 10 {
			t.Errorf("第 %d 行宽 %d(期望 10): %q", i, cellWidth(ln), ln)
		}
	}
}

// TestClipScrollThumbMoves verifies that the thumb moves with the offset and sits flush against
// the bottom line of the slot when scrolled to the end.
func TestClipScrollThumbMoves(t *testing.T) {
	var ls []string
	for i := 0; i < 30; i++ {
		ls = append(ls, fmt.Sprintf("line%02d", i))
	}
	body := strings.Join(ls, "\n")
	// rows=10, total=30, max=20; thumb=10*10/30=3; the lower part of the window is padded with █
	view, off := clipScroll(body, 20, 10, 12)
	if off != 20 {
		t.Fatalf("off=%d", off)
	}
	// Scrolled to the end, █ sits on the bottom line of the slot column
	thumbLine := -1
	for i, ln := range strings.Split(view, "\n") {
		if strings.HasSuffix(stripANSI(ln), "█") {
			thumbLine = i
		}
	}
	if thumbLine != 9 {
		t.Errorf("滑到底时滑块应占末行, got %d", thumbLine)
	}
}

// tallScrollLayout builds 40 lines of static text, triggering scrolling inside a 12-line window.
func tallScrollLayout() []config.Row {
	var ls []string
	for i := 0; i < 40; i++ {
		ls = append(ls, fmt.Sprintf("第 %02d 行 · 滚动测试", i))
	}
	return []config.Row{{Widgets: []config.Widget{
		{Type: "text", Title: "长文本", Text: strings.Join(ls, "\n"), MaxLines: 40},
	}}}
}

// TestModelScrollKeysRevealBottom verifies that the first frame excludes the bottom, that End and
// Home reveal the last and first lines respectively, and that neither overshoots.
func TestModelScrollKeysRevealBottom(t *testing.T) {
	m := &model{
		provider: stubProvider{data: true, views: testViews()},
		layout:   tallScrollLayout(),
		poll:     0,
		width:    100, height: 12,
	}

	first := m.View().Content
	if !strings.Contains(first, "第 00 行") {
		t.Errorf("首屏应含顶部内容")
	}
	if strings.Contains(first, "第 39 行") {
		t.Errorf("首屏不应看到尚未滚出的底部:\n%s", first)
	}
	if m.maxOff <= 0 {
		t.Fatalf("超高内容应缓存 maxOff>0, got %d", m.maxOff)
	}

	// End scrolls to the bottom, revealing the tail
	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	nm := mm.(*model)
	if nm.scroll != m.maxOff {
		t.Errorf("End 后 scroll=%d, 期望 %d", nm.scroll, m.maxOff)
	}
	bottom := nm.View().Content
	if !strings.Contains(bottom, "第 39 行") {
		t.Errorf("End 后应看到底部内容:\n%s", bottom)
	}
	if strings.Contains(bottom, "第 00 行") {
		t.Errorf("到底后首屏不应还含顶部")
	}

	// Home returns to the top; neither Home nor End overshoots
	mm, _ = nm.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	nm = mm.(*model)
	if nm.scroll != 0 {
		t.Errorf("Home 后 scroll=%d, 期望 0", nm.scroll)
	}
	top := nm.View().Content
	if !strings.Contains(top, "第 00 行") {
		t.Errorf("Home 后应回到顶部:\n%s", top)
	}
}

// TestModelScrollDownStepping verifies that each j press moves down 1 line and that the offset
// stops overshooting once it accumulates to maxOff.
func TestModelScrollDownStepping(t *testing.T) {
	m := &model{
		provider: stubProvider{data: true, views: testViews()},
		layout:   tallScrollLayout(),
		poll:     0,
		width:    100, height: 12,
	}
	m.View() // first frame, caches maxOff/pageRows

	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	m = nm.(*model)
	if m.scroll != 1 {
		t.Errorf("按 j 应下移 1, got scroll=%d", m.scroll)
	}

	// Repeated moves down clamp at maxOff
	for i := 0; i < 200; i++ {
		nm, _ = m.Update(tea.KeyPressMsg{Code: 'j'})
		m = nm.(*model)
		if m.scroll > m.maxOff {
			t.Fatalf("下移越界: scroll=%d > maxOff=%d", m.scroll, m.maxOff)
		}
	}
	if m.scroll != m.maxOff {
		t.Errorf("反复下移应停在 maxOff=%d, got %d", m.maxOff, m.scroll)
	}
}

// TestContentStartsAtFirstLine verifies that screen line 0 is already the first line of the content
// area, with no fixed top bar; the title is one of the rows in the layout.
func TestContentStartsAtFirstLine(t *testing.T) {
	m := &model{
		provider: stubProvider{views: testViews(), data: true},
		layout:   testLayout(),
		poll:     0,
		width:    120, height: 40,
	}
	lines := strings.Split(stripANSI(m.View().Content), "\n")
	if strings.TrimSpace(lines[0]) == "" || !strings.Contains(lines[0], "关键指标") {
		t.Errorf("第 0 行起就是内容区面板: %q", lines[0])
	}
}
