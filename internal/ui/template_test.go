package ui

import (
	"testing"
	"time"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// —— Go template interpolation (RenderTpl) ——

func TestRenderTplPassthrough(t *testing.T) {
	// A string without "{{" is returned as-is, even when doc holds a key of the same name
	if got := RenderTpl("CPU 使用率", map[string]any{"cpu": 12.5}); got != "CPU 使用率" {
		t.Errorf("无模板原样返回, 得到 %q", got)
	}
	// An empty string returns empty
	if got := RenderTpl("", nil); got != "" {
		t.Errorf("空串应返回空, 得到 %q", got)
	}
}

func TestRenderTplFieldAndFunc(t *testing.T) {
	doc := map[string]any{"cpu": 12.6}
	// .field interpolation + printf
	if got := RenderTpl(`CPU {{printf "%.0f" .cpu}}%`, doc); got != "CPU 13%" {
		t.Errorf("字段插值失败: %q", got)
	}
	// the upper function
	if got := RenderTpl(`{{upper .tag}}`, map[string]any{"tag": "cpu"}); got != "CPU" {
		t.Errorf("upper 失败: %q", got)
	}
	// Referencing a key that does not exist: the <no value> artifact must be cleared
	if got := RenderTpl("x{{.nope}}y", doc); got != "xy" {
		t.Errorf("missing 键不应泄漏 <no value>, 得到 %q", got)
	}
}

func TestRenderTplMissingNumeric(t *testing.T) {
	// With the field missing, printf taking nil directly emits the %!f(<nil>) artifact, so def
	// supplies the default instead
	if got := RenderTpl(`CPU {{printf "%.0f" (def .cpu 0.0)}}%`, nil); got != "CPU 0%" {
		t.Errorf("def 缺省应填 0, 得到 %q", got)
	}
	// When a value is present, def takes the real value
	if got := RenderTpl(`{{def .cpu "?"}}`, map[string]any{"cpu": 3.0}); got != "3" {
		t.Errorf("def 应取真实值, 得到 %q", got)
	}
}

// TestRenderTplWholeNumberFromSource verifies that when a source yields a whole number, the
// template's {{printf "%.0f" .x}} outputs "83" rather than %!f(int64=83).
//
// The constraint lives in the parse layer: numbers must be stored as float64, since the template's
// %f verb does not accept int64. This case covers the whole chain from parsing to template.
func TestRenderTplWholeNumberFromSource(t *testing.T) {
	p, err := source.ParseOutput([]byte("used_pct: 83.0\nqps: 1088\n"), time.Now(), source.Opts{Type: source.TypeMap})
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if !p.Has {
		t.Fatalf("键值行应解出 map: %+v", p.Val)
	}
	for _, c := range []struct{ raw, want string }{
		{`{{printf "%.0f" .used_pct}}%`, "83%"}, // rather than %!f(int64=83)%
		{`{{printf "%.1f" .qps}}`, "1088.0"},
		{`{{.used_pct}} · {{.qps}}`, "83 · 1088"}, // direct interpolation carries no decimal point either
	} {
		if got := RenderTpl(c.raw, p.Val.Map); got != c.want {
			t.Errorf("RenderTpl(%q) = %q, 期望 %q", c.raw, got, c.want)
		}
	}
}

func TestRenderTplParseErrorFallback(t *testing.T) {
	// A syntax error falls back to the original string, leaving the layout intact
	for _, bad := range []string{"a {{if}} b", "{{.cpu", "{{printf}}"} {
		if got := RenderTpl(bad, map[string]any{"cpu": 1.0}); got != bad {
			t.Errorf("语法错误 %q 应原样返回, 得到 %q", bad, got)
		}
	}
}

func TestRenderTplCached(t *testing.T) {
	// The same raw reuses the cache, while different data each produce their own result
	raw := `CPU {{printf "%.0f" .cpu}}%`
	if a, b := RenderTpl(raw, map[string]any{"cpu": 1.4}), RenderTpl(raw, map[string]any{"cpu": 2.4}); a != "CPU 1%" || b != "CPU 2%" {
		t.Errorf("缓存复用错: %q %q", a, b)
	}
}

// TestCollapseSpace verifies that the multi-line, multi-space output of a text source is merged
// into a single line, ready for a template to embed directly.
func TestCollapseSpace(t *testing.T) {
	cases := map[string]string{
		"北京 ☀️ +26°C":                         "北京 ☀️ +26°C", // no whitespace, returned as-is
		"a\nb\nc":                             "a b c",       // newlines merge into spaces
		"  北京  +26°C\n  晴  ":                  "北京 +26°C 晴",  // leading/trailing spaces, runs of spaces, and newlines are all merged
		"load1: 1.0\nload5: 0.5\nload15: 0.2": "load1: 1.0 load5: 0.5 load15: 0.2",
	}
	for in, want := range cases {
		if got := collapseSpace(in); got != want {
			t.Errorf("collapseSpace(%q) = %q, 期望 %q", in, got, want)
		}
	}
	if got := collapseSpace(""); got != "" {
		t.Errorf("空串应返回空, 得到 %q", got)
	}
}

// TestPanelTitleTemplate verifies that a panel title first takes the localized text and only then
// undergoes template interpolation.
func TestPanelTitleTemplate(t *testing.T) {
	w := config.Widget{Type: "stat", Source: "cpu", Title: `CPU {{printf "%.0f" (def .cpu 0.0)}}%`}
	if got := panelTitle(w, map[string]any{"cpu": 88.7}); got != "CPU 89%" {
		t.Errorf("标题模板插值失败: %q", got)
	}
	// With doc nil, def falls back to 0, so the title leaks neither <no value> nor %!f
	if got := panelTitle(w, nil); got != "CPU 0%" {
		t.Errorf("首帧前标题应回退 0%%: %q", got)
	}
}

// TestTplFields collects the top-level field names a template references, judged from the syntax
// tree — string matching would mistake literals, function names, and dots inside variables for
// fields, producing false startup hints.
func TestTplFields(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  string
		want []string
	}{
		{"单层字段", "{{.weather}}", []string{"weather"}},
		{"多层只记顶层", "{{.a.b}} {{.a}}", []string{"a"}},
		{"内置字段照常列出", `{{now "15:04"}} {{.weekday}}`, []string{"weekday"}},
		{"函数参数里的字段", `{{printf "%.0f" (def .cpu 0.0)}}%`, []string{"cpu"}},
		{"if/else 三个分支", "{{if .x}}{{.y}}{{else}}{{.z}}{{end}}", []string{"x", "y", "z"}},
		{"range 的集合与元素", "{{range .items}}{{.v}}{{end}}", []string{"items", "v"}},
		{"with", "{{with .cfg}}{{.k}}{{end}}", []string{"cfg", "k"}},
		{"变量定义与引用", "{{$x := .a}}{{$x}}", []string{"a"}},
		{"变量上取字段不算顶层", "{{$x := .a}}{{$x.f}}", []string{"a"}},
		{"括号表达式", "{{(.a).b}}", []string{"a"}},
		{"结果去重并排序", "{{.b}} {{.a}} {{.b}}", []string{"a", "b"}},

		// Cases that must not be judged as fields
		{"无模板", "静态标题", nil},
		{"纯字面量里的点号", `{{printf "%.1f" 1}} 见 example.com`, nil},
		{"标识符与变量名不是字段", "{{if .a}}{{end}}", []string{"a"}},
		{"语法错", "{{.a", nil},
		{"空串", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := tplFields(c.raw)
			if len(got) != len(c.want) {
				t.Fatalf("tplFields(%q) = %v, 期望 %v", c.raw, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("tplFields(%q) = %v, 期望 %v", c.raw, got, c.want)
				}
			}
		})
	}
}

// TestTplFieldsParsesWithFuncs verifies that parsing must carry tplFuncs: otherwise functions such
// as now/def/printf count as undefined, Parse fails outright, and tplFields returns nil, leaving
// the check silently broken.
func TestTplFieldsParsesWithFuncs(t *testing.T) {
	got := tplFields(`{{now "15:04"}} {{def .weather "N/A"}}`)
	if len(got) != 1 || got[0] != "weather" {
		t.Errorf("带函数的模板也该解析出字段, got %v", got)
	}
}
