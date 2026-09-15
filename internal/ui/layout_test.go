package ui

import (
	"fmt"
	"strings"
	"testing"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// TestWidgetHeight verifies that max_lines constrains the total height of the panel box: a row
// stretched open hands the remaining height to its panels, so a panel written with max_lines: 6
// is 6 content lines plus the top and bottom borders. Limiting the content without limiting the
// box would stretch the panel to the height of its neighbors.
func TestWidgetHeight(t *testing.T) {
	cases := []struct {
		name string
		w    config.Widget
		ch   int
		want int
	}{
		{"被撑开的 logs 写了上限:收到 上限+边框", config.Widget{Type: "logs", MaxLines: 6}, 20, 8},
		{"被撑开的 text 同理", config.Widget{Type: "text", MaxLines: 7}, 40, 9},
		{"撑开的行比上限还矮:原样用行高", config.Widget{Type: "logs", MaxLines: 6}, 5, 5},
		{"没写上限:整行都归它", config.Widget{Type: "logs"}, 20, 20},
		{"行没被撑开(ch=0):交回内容自适应", config.Widget{Type: "logs", MaxLines: 6}, 0, 0},
		{"其它类型不认 max_lines", config.Widget{Type: "table", MaxLines: 6}, 20, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := widgetHeight(c.w, c.ch); got != c.want {
				t.Errorf("widgetHeight(%s, ch=%d) = %d, 期望 %d", c.w.Type, c.ch, got, c.want)
			}
		})
	}
}

// TestRenderRowMaxLinesShrinksPanel verifies that within one row a stretched-open panel fills the
// whole row height, while a panel written with max_lines: 6 occupies only 8 lines (6 content
// lines plus borders) and the rest is left blank.
func TestRenderRowMaxLinesShrinksPanel(t *testing.T) {
	var logs []source.LogLine
	for i := 0; i < 30; i++ {
		logs = append(logs, source.LogLine{Time: logAt("10:00:00"), Level: "INFO",
			Msg: fmt.Sprintf("msg-%02d", i)})
	}
	views := map[string]source.SourceView{"lg": {Type: source.TypeLogs, Logs: logs}}
	row := []config.Widget{
		{Type: "logs", Source: "lg", Title: "实时日志"},
		{Type: "logs", Source: "lg", Title: "限 6 行", MaxLines: 6},
	}

	lines := strings.Split(stripANSI(renderRow(row, views, 120, 20, nil)), "\n")
	if len(lines) != 20 {
		t.Fatalf("行高应仍是 20(留白由 JoinHorizontal 补), got %d 行", len(lines))
	}
	// Each panel is 60 columns: the right half is taken from the 60th character on. The box
	// drawing characters are multi-byte, so slicing by byte would cut into the middle of a rune.
	right := make([]string, len(lines))
	for i, l := range lines {
		right[i] = string([]rune(l)[60:])
	}
	// The right panel is 8 lines tall, with its bottom border on line 8 (index 7); the left panel
	// spans the whole row, with its bottom border on the last line.
	if got := strings.Count(lines[7], "╰"); got != 1 {
		t.Errorf("限 6 行的面板应在第 8 行收底(下边框 1 个), got %d:\n%s", got, lines[7])
	}
	if got := strings.Count(lines[19], "╰"); got != 1 {
		t.Errorf("整行高的面板应画到最后一行(下边框 1 个), got %d:\n%s", got, lines[19])
	}
	// After the panel closes, the right half is blank, with no further border or log lines.
	for i := 8; i < len(right); i++ {
		if strings.TrimSpace(right[i]) != "" {
			t.Fatalf("限 6 行的面板第 %d 行之后不该再画东西: %q", i, strings.TrimRight(right[i], " "))
		}
	}
	// The content is the last 6 lines.
	if !strings.Contains(right[6], "msg-29") || strings.Contains(right[1], "msg-23") {
		t.Errorf("限 6 行的面板应显示 msg-24..msg-29:\n%s\n%s", right[1], right[6])
	}
}

// TestComposeWideFixedRowHeight verifies that height in the config applies to the panel itself
// rather than only counting toward the height budget: the panel height equals the row height and
// the content contracts accordingly, while a row with Height 0 still auto-sizes to its content.
func TestComposeWideFixedRowHeight(t *testing.T) {
	var logs []source.LogLine
	for i := 0; i < 30; i++ {
		logs = append(logs, source.LogLine{Time: logAt("10:00:00"), Level: "INFO",
			Msg: fmt.Sprintf("msg-%02d", i)})
	}
	views := map[string]source.SourceView{
		"lg": {Type: source.TypeLogs, Logs: logs},
		"tb": tableView([]map[string]any{{"name": "api"}, {"name": "web"}}),
	}

	// (a) fixed-height logs row: the panel is 10 tall and the content contracts to the row
	// height (10 - 2 = 8 lines)
	row := []config.Row{{Title: "Logs", Height: 10,
		Widgets: []config.Widget{{Type: "logs", Source: "lg", Title: "Live Logs"}}}}
	out := stripANSI(composeWide(row, views, 60, 40, nil))
	lines := strings.Split(out, "\n")
	if len(lines) != 11 { // 10 panel lines + 1 row-title line
		t.Fatalf("定高 10 的行应占 11 行(含标题), got %d:\n%s", len(lines), out)
	}
	if got := strings.Count(lines[10], "╰"); got != 1 {
		t.Errorf("下边框应落在第 10 行(定高生效), got %d:\n%s", got, lines[10])
	}
	if !strings.Contains(lines[2], "msg-22") || strings.Contains(out, "msg-21") {
		t.Errorf("应只显示最后 8 行 msg-22..msg-29:\n%s", out)
	}

	// (b) a table in the same row is also drawn at the row height: 2 content lines, padded to 10
	row[0].Widgets = []config.Widget{{Type: "table", Source: "tb", Title: "服务"}}
	out = stripANSI(composeWide(row, views, 60, 40, nil))
	lines = strings.Split(out, "\n")
	if len(lines) != 11 {
		t.Fatalf("定高行里的表格应补白到 10 行(含标题 11 行), got %d:\n%s", len(lines), out)
	}
	if got := strings.Count(lines[10], "╰"); got != 1 {
		t.Errorf("表格下边框应落在第 10 行, got %d:\n%s", got, lines[10])
	}

	// (c) a row with Height 0 auto-sizes to its content (header + 2 data lines + borders = 5
	// panel lines)
	row[0].Height = 0
	out = stripANSI(composeWide(row, views, 60, 40, nil))
	if got := strings.Count(out, "\n") + 1; got != 6 { // 5 panel lines + 1 title line
		t.Errorf("不定高的行应按内容自适应, want 6, got %d:\n%s", got, out)
	}
}

// TestRowTitleSourceRefs verifies that row titles accept only the built-ins .date/.weekday/.time
// and that every other field name is recorded as one startup hint. That hint does not block
// startup: a field that resolves to nothing shows up at run time as an empty title or a full
// fallback.
func TestRowTitleSourceRefs(t *testing.T) {
	cfg := func(title string) *config.Config {
		return &config.Config{
			Sources: []config.Source{{Name: "weather"}, {Name: "cpu"}, {Name: "date"}},
			Layout:  []config.Row{{Title: title}},
		}
	}
	for _, c := range []struct {
		name  string
		title string
		warn  bool
	}{
		// Should warn: a source reference or a non-built-in field
		{"按源名引用数据源", "概览 {{.weather}}", true},
		{"行内 widget 自身源平铺也不再可用", "{{.qps}}", true},
		{"未知字段同样取不到", "{{.nope}}", true},
		{"想点源里字段", "{{.cpu.used_pct}}", true},

		// Should not warn: the three built-ins, template functions, plain text
		{"内置日期", "{{.date}}", false},
		{"内置三键齐上", "{{.date}} {{.weekday}} {{.time}}", false},
		{"时间函数不是字段", `{{now "01-02 15:04:05"}}`, false},
		{"纯静态标题", "概览", false},
		{"函数包着内置字段", `{{upper .weekday}}`, false},
		{"有数据源但没引用", "概览 {{.date}}", false},
		// A source sharing a built-in key's name: the built-in always wins, so .date in a row
		// title is the built-in date
		{"与内置同名的源不算数据源", "{{.date}}", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := RowTitleSourceRefs(cfg(c.title))
			if (len(got) > 0) != c.warn {
				t.Fatalf("提示=%v, 期望有提示=%v", got, c.warn)
			}
			if c.warn && !strings.Contains(got[0], "layout[0]") {
				t.Errorf("提示应点出位置, got: %s", got[0])
			}
		})
	}

	// Writing the same field twice reports one hint only
	dup := cfg("{{.weather}} {{.weather}}")
	if got := RowTitleSourceRefs(dup); len(got) != 1 {
		t.Errorf("同一行写同一个字段两遍应只报一条, got %v", got)
	}
	// Dedupe applies to repeats only; two different fields report one hint each
	two := cfg("{{.weather}} {{.qps}}")
	if got := RowTitleSourceRefs(two); len(got) != 2 {
		t.Errorf("两个不同字段应各报一条, got %v", got)
	}
	// A conforming config returns nothing
	if got := RowTitleSourceRefs(cfg("概览 {{.date}}")); got != nil {
		t.Errorf("合规配置应无提示, got %v", got)
	}
}
