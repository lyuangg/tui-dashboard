package ui

import (
	"image/color"
	"reflect"
	"testing"

	"charm.land/lipgloss/v2"
)

// eqColor compares two color.Color values by RGBA, independent of the concrete implementation
// type.
func eqColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// withTheme switches the active theme to the named preset and restores the previous value when
// the test ends.
func withTheme(t *testing.T, name string) {
	t.Helper()
	old := cur
	if !UseTheme(name) {
		t.Fatalf("UseTheme(%q) 失败", name)
	}
	t.Cleanup(func() { cur = old })
}

func TestDefaultThemeValues(t *testing.T) {
	// The default theme's values are the last-resort fallback for every dashboard with no
	// theme configured, so each one is asserted.
	d := DefaultTheme()
	if d.Section != "212" {
		t.Errorf("section = %s, 期望 212", d.Section)
	}
	if d.Good != "10" || d.Warn != "214" || d.Bad != "196" {
		t.Errorf("semantic = %s/%s/%s, 期望 10/214/196", d.Good, d.Warn, d.Bad)
	}
	if d.Axis != "242" || d.Guide != "240" {
		t.Errorf("axis/guide = %s/%s, 期望 242/240", d.Axis, d.Guide)
	}
	wantWidget := map[string]string{
		"stat": "63", "chart": "33", "bar": "75", "gauge": "36",
		"heatmap": "40", "table": "208", "logs": "33", "text": "240",
	}
	if !reflect.DeepEqual(d.Widget, wantWidget) {
		t.Errorf("widget 表 = %v, 期望 %v", d.Widget, wantWidget)
	}
	// The default table is the fallback for every theme, so every preset name must carry a value.
	for _, n := range canonicalNamed {
		if d.Named[n] == "" {
			t.Errorf("default 缺命名色 %q", n)
		}
	}
	if d.Named["red"] != "196" {
		t.Errorf("default red = %s, 期望 196(旧文档 color: red 修好)", d.Named["red"])
	}
}

func TestPresetThemesComplete(t *testing.T) {
	// Every preset must supply all 8 widget colors and all named colors; an identical key set is
	// what allows swapping the skin as a whole.
	for _, name := range ThemeNames() {
		var th Theme
		if name == "default" {
			th = DefaultTheme()
		} else {
			withTheme(t, name)
			th = cur
		}
		for _, wt := range canonicalWidget {
			if th.Widget[wt] == "" {
				t.Errorf("%s 缺 widget 色 %q", name, wt)
			}
		}
		for _, cn := range canonicalNamed {
			if th.Named[cn] == "" {
				t.Errorf("%s 缺命名色 %q", name, cn)
			}
		}
		if th.Name != name {
			t.Errorf("%s.Name = %q", name, th.Name)
		}
	}
}

func TestUseThemeUnknownKeepsCurrent(t *testing.T) {
	before := cur
	if UseTheme("nope") {
		t.Error("未知名应返回 false")
	}
	if !reflect.DeepEqual(cur, before) {
		t.Error("未知名不应改动当前主题")
	}
	if !UseTheme("") {
		t.Error("空名应视为 default 并成功")
	}
	if !reflect.DeepEqual(cur, DefaultTheme()) {
		t.Error("空名应切回 default")
	}
}

func TestAccentOfNamedColorPerTheme(t *testing.T) {
	// color: red follows the theme: default and dracula each take red from their own named-color
	// table.
	withTheme(t, "default")
	got := accentOf("stat", "red")
	if want := lipgloss.Color(DefaultTheme().Named["red"]); !eqColor(got, want) {
		t.Errorf("default accentOf(red) 不匹配 %q", DefaultTheme().Named["red"])
	}
	withTheme(t, "dracula")
	got = accentOf("stat", "red")
	if want := lipgloss.Color(DraculaTheme().Named["red"]); !eqColor(got, want) {
		t.Errorf("dracula accentOf(red) 不匹配 %q", DraculaTheme().Named["red"])
	}
	if eqColor(got, lipgloss.Color(DefaultTheme().Named["red"])) {
		t.Error("dracula 的 red 不应等于 default 的 red(未随主题切换)")
	}
	// The light theme's red uses a darker color number, keeping it readable on a light ground.
	withTheme(t, "light")
	if got := accentOf("stat", "red"); !eqColor(got, lipgloss.Color(LightTheme().Named["red"])) {
		t.Errorf("light accentOf(red) 不匹配 %q", LightTheme().Named["red"])
	}
}

func TestAccentOfUnknownNameFallsBackToType(t *testing.T) {
	// An unknown name (neither a numeric color nor #hex) ignores the override and falls back to
	// the type default color.
	withTheme(t, "default")
	unknown := accentOf("stat", "blurple")
	typeDefault := accentOf("stat", "")
	if !eqColor(unknown, typeDefault) {
		t.Error("未知名应回退 type 默认")
	}
}

func TestAccentOfRawSpecPassthrough(t *testing.T) {
	// Numeric color numbers / "#hex" pass through unchanged.
	withTheme(t, "default")
	if got := accentOf("stat", "196"); !eqColor(got, lipgloss.Color("196")) {
		t.Error("数字色号应原样透传")
	}
	if got := accentOf("stat", "#ff0055"); !eqColor(got, lipgloss.Color("#ff0055")) {
		t.Error("#hex 应原样透传")
	}
}

func TestNamedColorFallsBackToDefaultTable(t *testing.T) {
	// A name the theme does not configure falls back to the default theme's name table.
	if _, ok := namedColor("red"); !ok {
		t.Error("namedColor(red) 应在 default 下命中")
	}
}

func TestResolveSpec(t *testing.T) {
	// The unified color-spec resolver: preset names resolve against the theme, color numbers and
	// #hex hit as-is, and an empty string or an unknown name returns false. accentOf and row-title
	// custom colors share this path.
	withTheme(t, "default")
	if c, ok := resolveSpec("red"); !ok || !eqColor(c, lipgloss.Color(DefaultTheme().Named["red"])) {
		t.Error("预设名字应命中命名色表")
	}
	if c, ok := resolveSpec("196"); !ok || !eqColor(c, lipgloss.Color("196")) {
		t.Error("数字色号应原样透传")
	}
	if c, ok := resolveSpec("#ff0055"); !ok || !eqColor(c, lipgloss.Color("#ff0055")) {
		t.Error("#hex 应原样透传")
	}
	withTheme(t, "dracula")
	if c, ok := resolveSpec("red"); !ok || !eqColor(c, lipgloss.Color(DraculaTheme().Named["red"])) {
		t.Error("预设名字应随当前主题联动")
	}
	for _, bad := range []string{"", "blurple", "not a colour"} {
		if _, ok := resolveSpec(bad); ok {
			t.Errorf("resolveSpec(%q) 应失败", bad)
		}
	}
}
