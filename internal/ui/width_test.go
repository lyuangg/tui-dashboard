package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// —— width consistency: cellWidth must come from the same source as lipgloss, the library that
// actually lays out the boxes ——
//
// lipgloss measures boxes and padding with charmbracelet/x/ansi, which disagrees with
// go-runewidth on EAA ambiguous-width characters (·, —) and on some block characters.
// Inconsistent measurement shifts truncation and padding by one column.

// TestWidthOracleMatchesLipgloss verifies that for content containing ambiguous-width
// characters, emoji, CJK, and ANSI, cellWidth equals lipgloss.Width (the same terminal
// column-width model).
func TestWidthOracleMatchesLipgloss(t *testing.T) {
	colored := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render("CPU 31% · 用户 18%")
	cases := []string{
		"",
		"ASCII 123",
		"用户", "👋", "✅",
		"CPU 31% · 用户 18%", // · (U+00B7, EAA) counts as one column
		"— em dash —",      // — (U+2014, EAA) counts as one column
		"● … 摘要",           // ● … count as one column each
		"█▄▀ 柱状条",          // block drawing characters count as one column each
		"aａfｆ",             // ASCII and full-width letters
		colored,            // ANSI-colored content; measurement must ignore the escapes
		"1m 负载 2.9 · 空闲 61MB · 已用 12%",
	}
	for _, s := range cases {
		if got := cellWidth(s); got != lipgloss.Width(s) {
			t.Errorf("cellWidth(%q) = %d, lipgloss.Width = %d, 宽度模型不一致", s, got, lipgloss.Width(s))
		}
	}
}

// TestRuneWidthConsistentWithCellWidth verifies that the sum of the per-rune widths equals the
// cellWidth of the whole string, keeping truncateVisible's per-rune accumulation on the same
// measure as whole-string measurement.
func TestRuneWidthConsistentWithCellWidth(t *testing.T) {
	for _, s := range []string{"CPU 31% · 用户 18%", "👋 — 你好", "█▄▀ ● …"} {
		sum := 0
		for _, r := range s {
			sum += runeWidth(r)
		}
		if sum != cellWidth(s) {
			t.Errorf("%q: runeWidth 累加=%d, cellWidth=%d", s, sum, cellWidth(s))
		}
	}
}

// TestTruncateVisibleBudget verifies that the truncation result (including the trailing …) is no
// wider than maxW, covering ASCII, CJK, emoji, and colored content.
func TestTruncateVisibleBudget(t *testing.T) {
	colored := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("⚠ 服务异常")
	samples := []string{
		"hello world, this is a long ascii line",
		"这是很长的一段中文内容用来测试截断边界是否恰好贴合宽度",
		"mix 👋 emoji 与中文和 ascii 12345 · mixed",
		"██▄█▀ 一些方块",
		colored + " 后面还有内容",
	}
	for _, s := range samples {
		vw := cellWidth(s)
		for maxW := 1; maxW <= vw+3; maxW++ {
			out := truncateVisible(s, maxW)
			if got := cellWidth(out); got > maxW {
				t.Errorf("truncateVisible(%q,%d) 宽 %d 超界: %q", s, maxW, got, out)
			}
			if maxW < vw && !strings.HasSuffix(stripANSI(out), "…") {
				t.Errorf("truncateVisible(%q,%d) 需截断却无 …: %q", s, maxW, out)
			}
			if maxW >= vw && out != s {
				t.Errorf("truncateVisible(%q,%d) 足够宽却改动: %q", s, maxW, out)
			}
		}
	}
}

// TestPanelWidthTrickyContent verifies that when a panel holds ambiguous-width characters (·,
// —), every line is still exactly the target width.
func TestPanelWidthTrickyContent(t *testing.T) {
	for _, w := range []int{20, 40, 80} {
		body := "CPU 31% · 用户 18% — 摘要"
		r := stripANSI(Panel("text", "模板插值演示", body, w, 0))
		for i, ln := range strings.Split(r, "\n") {
			if got := cellWidth(ln); got != w {
				t.Errorf("Panel(width=%d) 第 %d 行宽 %d(期望 %d): %q", w, i, got, w, ln)
			}
		}
	}
}

// —— width budget of the fixed messages ——
//
// msgPanel leaves the explanatory text a usable width of cw-6 (two leading spaces, fitLines'
// cw-2, panelColored's cw-4, see widgets.go). On a narrow terminal these messages are bound to
// be truncated to "…" by fitLines; that is a deliberate degradation, since wrapping would let
// the panel height vary with the message.
//
// This group of cases budgets for cw=48 (cw-6=42). Source and field names in the cases are kept
// short (cpu/sim); a source name of unbounded length pushing out of the budget is a known
// degradation, the same as for the fetch-failure message.
func TestFixedMessagesFitBudget(t *testing.T) {
	const cw = 48
	const budget = cw - 6

	noValue := func(w config.Widget, v source.SourceView) string { return noValueMsg(w, v) }
	capMsg := func(w config.Widget, v source.SourceView) string {
		ok, msg := capability(w, v)
		if ok {
			t.Fatalf("用例写错:capability(%s/%s) 应当不通过", w.Type, v.Type)
		}
		return msg
	}

	for _, c := range []struct {
		name string
		msg  string
		w    config.Widget
		v    source.SourceView
	}{
		{
			"等待数据", noValue(
				config.Widget{Type: "stat", Source: "sim", Value: "{{.qps}}"},
				source.SourceView{Type: source.TypeNumber}),
			config.Widget{Type: "stat", Source: "sim"},
			source.SourceView{Type: source.TypeNumber},
		},
		{
			"map 源没写 value", noValue(
				config.Widget{Type: "stat", Source: "cpu"},
				mapView(map[string]any{"a": float64(1)}, nil)),
			config.Widget{Type: "stat", Source: "cpu"},
			mapView(map[string]any{"a": float64(1)}, nil),
		},
		{
			"字段取不到", noValue(
				config.Widget{Type: "stat", Source: "sim", Value: "{{.nope}}"},
				mapView(map[string]any{"a": float64(1)}, nil)),
			config.Widget{Type: "stat", Source: "sim", Value: "{{.nope}}"},
			mapView(map[string]any{"a": float64(1)}, nil),
		},
		{
			"能力表对不上", capMsg(
				config.Widget{Type: "table", Source: "cpu"}, viewOf(source.TypeMap)),
			config.Widget{Type: "table", Source: "cpu"},
			viewOf(source.TypeMap),
		},
		{
			"未知 widget 类型", msgUnknownWidget,
			config.Widget{Type: "nope", Source: "sim"}, viewOf(source.TypeNumber),
		},
		{
			"logs 源没有日志", msgNoLogs,
			config.Widget{Type: "logs", Source: "lg"},
			source.SourceView{Type: source.TypeLogs},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, v, msg := c.w, c.v, c.msg
			if got := visibleWidth(msg); got > budget {
				t.Errorf("文案 %d 列, 超出 cw=%d 的预算 %d 列: %q", got, cw, budget, msg)
			}
			// Rendered at cw the message must appear in full; truncation by fitLines would drop the
			// tail and leave an extra …
			out := stripANSI(renderWidget(w, v, cw, 0))
			if !strings.Contains(out, stripANSI(msg)) {
				t.Errorf("cw=%d 渲染后文案没完整出现:\n want %q\n got  %q", cw, stripANSI(msg), out)
			}
		})
	}
}
