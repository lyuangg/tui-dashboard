package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validYAML = `
poll_interval: 500ms
sources:
  - name: sim
    cmd: ./scripts/sim_number.sh
    type: number
    interval: 2s
  - name: mem
    cmd: ./scripts/sys_mem.sh
    type: map
    interval: 5s
    history_cap: 90
    log_cap: 300
layout:
  - widgets:
      - title: 关键指标
        type: stat
        source: sim
        value: '{{.qps}}'
        format: "%.0f /s"
      - title: 服务状态
        type: table
        source: mem
        value: '{{.used_pct}}'
`

func TestLoadValid(t *testing.T) {
	cfg, err := Load(write(t, validYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PollInterval.Duration != 500*time.Millisecond {
		t.Errorf("poll_interval = %v", cfg.PollInterval)
	}

	if len(cfg.Sources) != 2 || len(cfg.Layout) != 1 || len(cfg.Layout[0].Widgets) != 2 {
		t.Fatalf("结构不符: sources=%d layout=%d widgets=%d", len(cfg.Sources), len(cfg.Layout), len(cfg.Layout[0].Widgets))
	}
	if cfg.Sources[1].HistoryCap != 90 || cfg.Sources[1].LogCap != 300 {
		t.Errorf("覆盖的 caps 未生效: %+v", cfg.Sources[1])
	}
	if cfg.Sources[0].HistoryCap != DefaultHistoryCap || cfg.Sources[0].LogCap != DefaultLogCap {
		t.Errorf("缺省 caps 未生效")
	}
}

func TestLoadDefaults(t *testing.T) {
	// an omitted poll_interval takes the default; every source is a script, so all carry cmd
	cfg, err := Load(write(t, strings.ReplaceAll(validYAML, "poll_interval: 500ms", "")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PollInterval.Duration != DefaultPollInterval {
		t.Errorf("默认 poll_interval = %v", cfg.PollInterval)
	}
	if cfg.Sources[0].Cmd == "" || cfg.Sources[1].Cmd == "" {
		t.Errorf("source 应都带 cmd(全是脚本源)")
	}
}

func TestDurationForms(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"2s", "2s"}, {"500ms", "500ms"}, {"5", "5s"}, {"1.5", "1.5s"},
	} {
		cfg, err := Load(write(t, strings.Replace(validYAML, "interval: 2s", "interval: "+c.in, 1)))
		if err != nil {
			t.Fatalf("interval=%s: %v", c.in, err)
		}
		if got := cfg.Sources[0].Interval.String(); got != c.want {
			t.Errorf("interval=%s 解析为 %s, 期望 %s", c.in, got, c.want)
		}
	}
}

func TestLoadStaticTextWidget(t *testing.T) {
	// a purely static text needs no source
	cfg, err := Load(write(t, `
sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout:
  - widgets:
      - title: 状态
        type: text
        text: "🎉 一切正常"
      - title: 数字
        type: stat
        source: a
        value: '{{.qps}}'
`))
	if err != nil {
		t.Fatalf("静态 text widget 不应报错: %v", err)
	}
	if cfg.Layout[0].Widgets[0].Text != "🎉 一切正常" {
		t.Errorf("text 字段未解析: %+v", cfg.Layout[0].Widgets[0])
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"无 source": `layout: [{widgets: []}]`,
		"空 layout": `sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]`,
		"重复 source 名": `sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}, {name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout: [{widgets: []}]`,
		"interval 为 0": `sources: [{name: a, cmd: ./a.sh, type: map}]
layout: [{widgets: []}]`,
		"未知 widget type": `sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout: [{widgets: [{type: pizza, source: a}]}]`,
		"widget 引用未定义 source": `sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout: [{widgets: [{type: stat, source: nope, value: '{{.x}}'}]}]`,
		"text 既无 source 也无静态内容": `sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout: [{widgets: [{type: text}]}]`,
	}
	for name, y := range cases {
		if _, err := Load(write(t, y)); err == nil {
			t.Errorf("%s: 期望报错但通过", name)
		}
	}
}

// TestLoadAcceptsMissingValue a stat/table without value: is not an error in config: whether
// it may be omitted depends on the type declared by the source, and the semantics of a type
// belong to the source package; the reverse import would be a cycle, so the decision lives in
// main.checkWidgetTypes (see main_test.go). This only covers config letting it pass.
func TestLoadAcceptsMissingValue(t *testing.T) {
	for name, y := range map[string]string{
		"stat 无 value,源是 map": `sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout: [{widgets: [{type: stat, source: a}]}]`,
		"stat 无 value,源是 number": `sources: [{name: a, cmd: ./a.sh, type: number, interval: 1s}]
layout: [{widgets: [{type: stat, source: a}]}]`,
		"table 无 value": `sources: [{name: a, cmd: ./a.sh, type: table, interval: 1s}]
layout: [{widgets: [{type: table, source: a}]}]`,
	} {
		if _, err := Load(write(t, y)); err != nil {
			t.Errorf("%s: config 该放行(拦截在 main), got %v", name, err)
		}
	}
}

// TestLoadRequiresType type: is required: which kind of data a source produces decides how
// the script is written, how the value is taken and which panels can use it. This only
// covers the missing key; whether a value is legal is checked by main (the table of legal
// values lives in the source package, and the reverse import would be a cycle).
func TestLoadRequiresType(t *testing.T) {
	_, err := Load(write(t, `sources: [{name: a, cmd: ./a.sh, interval: 1s}]
layout: [{widgets: []}]`))
	if err == nil {
		t.Fatal("没写 type: 应报错")
	}
	for _, want := range []string{"source[a]", "type", "number"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错应含 %q, got: %v", want, err)
		}
	}
}

// —— config resolution (Locate: explicit → process CWD → XDG → home → bundled example) ——

func TestLocateExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	p := write(t, "poll_interval: 1s")
	data, src, err := Locate(p)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if src != "explicit: "+p {
		t.Errorf("source = %q", src)
	}
	if !strings.Contains(string(data), "poll_interval") {
		t.Errorf("未读回显式文件内容: %q", data)
	}
}

func TestLocateXDGOverride(t *testing.T) {
	// XDG takes priority over the home directory: when both exist, the XDG copy is used
	noCwdConfig(t)
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	mkConf(t, filepath.Join(xdg, configDir, "config.yaml"), "# xdg-config")
	mkConf(t, filepath.Join(home, ".config", configDir, "config.yaml"), "# home-config")

	data, src, err := Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if !strings.HasPrefix(src, "xdg: ") {
		t.Errorf("应命中 XDG, 得到 source=%q", src)
	}
	if !strings.Contains(string(data), "# xdg-config") {
		t.Errorf("XDG 内容错: %q", data)
	}
}

func TestLocateHomeFallback(t *testing.T) {
	// no XDG, but ~/.config exists: the home directory is hit
	noCwdConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	mkConf(t, filepath.Join(home, ".config", configDir, "config.yaml"), "# home-config")

	data, src, err := Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if !strings.HasPrefix(src, "home: ") {
		t.Errorf("应命中家目录, 得到 source=%q", src)
	}
	if !strings.Contains(string(data), "# home-config") {
		t.Errorf("家目录内容错: %q", data)
	}
}

func TestLocateBundledFallback(t *testing.T) {
	// clean environment (no XDG, no home config): falls back to the bundled example, and the
	// example itself parses
	noCwdConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	data, src, err := Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if src != "bundled example" {
		t.Errorf("应退回内置 example, 得到 source=%q", src)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		t.Errorf("内置 example 为空")
	}
	if _, err := FromBytes(data); err != nil {
		t.Errorf("内置 example 无法解析: %v", err)
	}
}

func TestLocateCwdPrecedence(t *testing.T) {
	// ./config.yaml in the CWD takes priority over XDG and the home directory, and the source
	// string is tagged "cwd: "
	t.Chdir(t.TempDir())
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	mkConf(t, filepath.Join(xdg, configDir, configFileName), "# xdg-config")
	mkConf(t, filepath.Join(home, ".config", configDir, configFileName), "# home-config")
	mkConf(t, configFileName, "# cwd-config")

	data, src, err := Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if want := "cwd: " + configFileName; src != want {
		t.Errorf("source = %q, 期望 %q", src, want)
	}
	if !strings.Contains(string(data), "# cwd-config") {
		t.Errorf("应读到 CWD 那份内容: %q", data)
	}
}

func TestLocateExplicitBeatsCwd(t *testing.T) {
	// explicit takes priority over the copy in the current directory
	t.Chdir(t.TempDir())
	mkConf(t, configFileName, "# cwd-config")
	p := write(t, "# explicit-config")

	data, src, err := Locate(p)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if src != "explicit: "+p {
		t.Errorf("source = %q, 期望 %q", src, "explicit: "+p)
	}
	if !strings.Contains(string(data), "# explicit-config") {
		t.Errorf("应读 explicit 内容: %q", data)
	}
}

func TestFromBytesExampleIsRealData(t *testing.T) {
	// the example demonstrates: script sources, border title templates, template interpolation
	cfg, err := FromBytes(exampleYAML)
	if err != nil {
		t.Fatalf("example.yaml: %v", err)
	}
	// every source in the example carries cmd and references no sim_ fake-data script
	for i, s := range cfg.Sources {
		if s.Cmd == "" {
			t.Errorf("source[%d](%s) 缺 cmd", i, s.Name)
		}
		if strings.Contains(s.Cmd, "sim_") || strings.Contains(s.Name, "sim_") {
			t.Errorf("source[%d](%s) 引用了假数据脚本: %q", i, s.Name, s.Cmd)
		}
	}
	// layout[0] is a pure title row (only title, no widget); layout[1] is the overview panel row
	if len(cfg.Layout[0].Widgets) != 0 {
		t.Errorf("layout[0] 应为纯标题行(无 widget), 得到 %d 个", len(cfg.Layout[0].Widgets))
	}
	// the first widget of the overview row (whole-machine CPU) is of type text: the border
	// title template draws the live {{.cpu}}%, and the body template joins the 1m/5m/15m loads
	// into one line
	w := &cfg.Layout[1].Widgets[0]
	if w.Type != "text" {
		t.Errorf("CPU 面板应为 text 型(实时百分比只画在标题), 得到 %q", w.Type)
	}
	if !strings.Contains(w.Title, "{{") || !strings.Contains(w.Title, ".cpu") {
		t.Errorf("CPU text 标题应带 {{.cpu}} 模板, 得到 %q", w.Title)
	}
	if !strings.Contains(w.Text, "{{") || !strings.Contains(w.Text, ".load5") {
		t.Errorf("CPU text 正文应带 {{.load5}} 模板, 得到 %q", w.Text)
	}
	mem := &cfg.Layout[1].Widgets[1]
	if mem.Title != "Memory" {
		t.Errorf("内存标题 = %q, 期望 Memory", mem.Title)
	}
}

func TestWidgetColorField(t *testing.T) {
	// the color field is decoded verbatim; omitted it is empty (the default color scheme by type)
	raw := []byte(`
sources:
  - {name: a, cmd: "echo 1", type: text, interval: 1s}
layout:
  - widgets:
      - {type: text, source: a, text: "hi", color: "#ff0055"}
      - {type: text, source: a, text: "yo"}
`)
	cfg, err := FromBytes(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Layout[0].Widgets[0].Color; got != "#ff0055" {
		t.Errorf("color = %q, 期望 \"#ff0055\"", got)
	}
	if got := cfg.Layout[0].Widgets[1].Color; got != "" {
		t.Errorf("缺省 color 应为空, 得到 %q", got)
	}
}

func TestRowColorFields(t *testing.T) {
	// the row title color/bg are decoded verbatim; omitted or unknown names raise no error
	// (ignored at render time, falling back to the theme)
	raw := []byte(`
sources:
  - {name: a, cmd: "echo 1", type: text, interval: 1s}
layout:
  - title: 概览
    title_align: center
    color: red
    bg: "#2e2e2e"
    widgets:
      - {type: text, source: a, text: hi}
`)
	cfg, err := FromBytes(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	row := cfg.Layout[0]
	if row.Color != "red" || row.BG != "#2e2e2e" {
		t.Errorf("行 color/bg 未解析: %q/%q", row.Color, row.BG)
	}

	// the default is empty (follows the theme / no background)
	def, err := FromBytes([]byte(validYAML))
	if err != nil {
		t.Fatalf("default parse: %v", err)
	}
	if def.Layout[0].Color != "" || def.Layout[0].BG != "" {
		t.Errorf("行 color/bg 缺省应为空: %q/%q", def.Layout[0].Color, def.Layout[0].BG)
	}

	// unknown names / bad values raise no error (the same lenient rule as widget color)
	bad := []byte(`
sources:
  - {name: a, cmd: "echo 1", type: text, interval: 1s}
layout:
  - title: 概览
    bg: nope
    widgets:
      - {type: text, source: a, text: hi}
`)
	if _, err := FromBytes(bad); err != nil {
		t.Errorf("未知 color/bg 不应报错: %v", err)
	}
}

func TestRowTitleAlignValidation(t *testing.T) {
	base := func(align string) string {
		return fmt.Sprintf(`
sources: [{name: a, cmd: ./a.sh, type: map, interval: 1s}]
layout:
  - title: 概览
    title_align: %s
    widgets: [{type: text, source: a, text: hi}]
`, align)
	}
	cfg, err := FromBytes([]byte(base("center")))
	if err != nil {
		t.Fatalf("center 应通过: %v", err)
	}
	if cfg.Layout[0].TitleAlign != "center" {
		t.Errorf("row title_align 未解析: %+v", cfg.Layout[0])
	}
	for _, bad := range []string{"middle", "up", "Centre"} {
		if _, err := FromBytes([]byte(base(bad))); err == nil {
			t.Errorf("row title_align=%q 应报错", bad)
		}
	}
}

// mkConf writes a file at p (creating parent directories as needed).
func mkConf(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noCwdConfig moves the process CWD to an empty directory with no config.yaml:
// ResolvePath/Locate look at ./config.yaml first, so without moving, the result depends on
// whether that file happens to be in the CWD when the tests run.
func noCwdConfig(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

// —— --init(WriteExampleToDefault)——

func TestWriteExampleToDefaultCreates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	p, created, err := WriteExampleToDefault(false)
	if err != nil {
		t.Fatalf("WriteExampleToDefault: %v", err)
	}
	if !created {
		t.Errorf("首次应创建成功")
	}
	want := filepath.Join(home, ".config", configDir, "config.yaml")
	if p != want {
		t.Errorf("路径 = %q, 期望 %q", p, want)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取生成文件: %v", err)
	}
	if string(got) != string(exampleYAML) {
		t.Errorf("生成内容应与内置 example 一致")
	}
}

func TestWriteExampleToDefaultNoOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	p, created, err := WriteExampleToDefault(false)
	if err != nil || !created {
		t.Fatalf("首次创建失败: created=%v err=%v", created, err)
	}
	// the user edited the content, so a second --init does not overwrite
	if err := os.WriteFile(p, []byte("# 用户自己改过\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, created, err = WriteExampleToDefault(false)
	if err != nil {
		t.Fatalf("第二次调用: %v", err)
	}
	if created {
		t.Errorf("已存在时不应再覆盖")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "# 用户自己改过\n" {
		t.Errorf("已存在的用户配置被覆盖了: %q", got)
	}
}

func TestWriteExampleToDefaultPrefersXDG(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	p, created, err := WriteExampleToDefault(false)
	if err != nil {
		t.Fatalf("WriteExampleToDefault: %v", err)
	}
	if !created {
		t.Errorf("应创建到 XDG 路径")
	}
	if want := filepath.Join(xdg, configDir, "config.yaml"); p != want {
		t.Errorf("路径 = %q, 期望 XDG 路径 %q", p, want)
	}
}

// TestUnknownTopLevelKeysIgnored only theme/poll_interval/sources/layout are recognized at
// the top level; extra keys are ignored as unknown YAML keys and do not affect the rest of
// the parse. It also covers one more point: a source is not created automatically because a
// template references {{.weather}}; it must be declared under sources.
func TestUnknownTopLevelKeysIgnored(t *testing.T) {
	cfg, err := Load(write(t, `
title: '业务监控 {{now "01-02"}} {{.weekday}}'
title_align: middle
subtitle: '{{now "15:04:05"}} · {{.weekday}} —— {{.weather}}'
subtitle_align: right
color: '#f8f8f2'
bg: '#44475a'
sources:
  - {name: sim, cmd: ./scripts/sim.sh, type: map, interval: 1s}
layout:
  - widgets:
      - {type: stat, source: sim, value: '{{.qps}}', title: QPS, title_align: right}
`))
	if err != nil {
		t.Fatalf("未知顶层键应忽略: %v", err)
	}
	if cfg.Layout[0].Widgets[0].TitleAlign != "right" {
		t.Errorf("widget title_align 未解析: %+v", cfg.Layout[0].Widgets[0])
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Name != "sim" {
		t.Fatalf("weather 未声明不应自动补建: %+v", cfg.Sources)
	}
}

func TestLoadWidgetTitleAlignError(t *testing.T) {
	// a top-level title_align is only an unknown key (see the previous case); the widget level
	// is still validated strictly
	y := `
sources: [{name: sim, cmd: ./scripts/sim.sh, type: map, interval: 1s}]
layout:
  - widgets:
      - {type: stat, source: sim, value: '{{.qps}}', title: QPS, title_align: up}
`
	if _, err := Load(write(t, y)); err == nil {
		t.Errorf("widget 非法 title_align 应报错")
	}
}

// TestLoadWidgetLabels labels can only be drawn by chart and bar; set on other widgets they
// have no effect — the same path as unknown keys, deliberately not validated.
func TestLoadWidgetLabels(t *testing.T) {
	body := func(typ, extra string) string {
		return `
sources: [{name: sim, cmd: ./scripts/sim.sh, type: array, interval: 1s}]
layout:
  - widgets:
      - {type: ` + typ + `, source: sim, ` + extra + `}
`
	}
	// the two types that can draw the names and the several that cannot all load successfully
	for _, typ := range []string{"chart", "bar", "stat", "gauge", "table", "logs", "text"} {
		cfg, err := Load(write(t, body(typ, "labels: [cpu, mem, disk]")))
		if err != nil {
			t.Errorf("%s 上的 labels 不该报错(用不上而已): %v", typ, err)
			continue
		}
		if got := cfg.Layout[0].Widgets[0].Labels; len(got) != 3 || got[0] != "cpu" || got[2] != "disk" {
			t.Errorf("%s: labels 未解析: %v", typ, got)
		}
	}
}

// TestLoadWidgetTimeFormat time_format is the Go time layout of the logs time column; set on
// other widgets it has no effect — the same path as labels, deliberately not validated.
func TestLoadWidgetTimeFormat(t *testing.T) {
	const layout = `"01-02 15:04:05"`
	for _, typ := range []string{"logs", "chart", "stat", "text"} {
		y := `
sources: [{name: sim, cmd: ./scripts/sim.sh, type: logs, interval: 1s}]
layout:
  - widgets:
      - {type: ` + typ + `, source: sim, time_format: ` + layout + `}
`
		cfg, err := Load(write(t, y))
		if err != nil {
			t.Errorf("%s 上的 time_format 不该报错(用不上而已): %v", typ, err)
			continue
		}
		if got := cfg.Layout[0].Widgets[0].TimeFormat; got != "01-02 15:04:05" {
			t.Errorf("%s: time_format 未解析: %q", typ, got)
		}
	}
}

// TestLoadIgnoresUnknownKeys unknown keys are silently ignored; YAML strict mode is off: the
// nonexistent keys mixed into the fixture (unit / parse) and the mistyped key (titel) are
// both ignored, while the remaining fields parse as usual.
func TestLoadIgnoresUnknownKeys(t *testing.T) {
	y := `
sources: [{name: sim, cmd: ./scripts/sim.sh, type: map, parse: top, interval: 1s}]
layout:
  - widgets:
      - {type: stat, source: sim, value: '{{.qps}}', format: "%.0f", unit: KB/s, titel: QPS}
`
	cfg, err := Load(write(t, y))
	if err != nil {
		t.Fatalf("未知键应静默忽略, got: %v", err)
	}
	if cfg.Sources[0].Type != "map" || cfg.Layout[0].Widgets[0].Format != "%.0f" {
		t.Errorf("其余字段应照常解析: %+v", cfg.Sources[0])
	}
}

func TestThemeField(t *testing.T) {
	// the top-level theme is an optional string (empty = default); the name is validated by
	// main (ui cannot be imported by config)
	def, err := Load(write(t, validYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if def.Theme != "" {
		t.Errorf("缺省 theme 应为空, 得到 %q", def.Theme)
	}
	themed, err := Load(write(t, "theme: gruvbox\n"+validYAML))
	if err != nil {
		t.Fatalf("Load(theme): %v", err)
	}
	if themed.Theme != "gruvbox" {
		t.Errorf("theme 未解析: %q", themed.Theme)
	}
}

func TestResolvePathPriority(t *testing.T) {
	t.Chdir(t.TempDir()) // default: no config.yaml in the CWD (it is only added at ⑤)
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	// ① explicit always takes priority (even when the default paths exist)
	mkConf(t, filepath.Join(xdg, configDir, configFileName), "# x")
	mkConf(t, filepath.Join(home, ".config", configDir, configFileName), "# h")
	ex := write(t, "# explicit")
	if got := ResolvePath(ex); got != ex {
		t.Errorf("explicit 优先: got %q", got)
	}

	// ② no explicit and no config file in the CWD: XDG takes priority over the home directory
	if got := ResolvePath(""); got != filepath.Join(xdg, configDir, configFileName) {
		t.Errorf("应命中 XDG, got %q", got)
	}

	// ③ no XDG, but the home directory exists
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := ResolvePath(""); got != filepath.Join(home, ".config", configDir, configFileName) {
		t.Errorf("应命中家目录, got %q", got)
	}

	// ④ clean environment: the empty string is returned (meaning a fallback to the bundled example)
	t.Setenv("HOME", t.TempDir())
	if got := ResolvePath(""); got != "" {
		t.Errorf("干净环境应返回空串, got %q", got)
	}

	// ⑤ ./config.yaml is put back in the current directory: it takes priority over XDG/home
	//    and the relative path is returned (not an absolute path; a caller that needs the
	//    directory must Abs it itself, see main.scriptDir)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	mkConf(t, configFileName, "# cwd")
	if got := ResolvePath(""); got != configFileName {
		t.Errorf("CWD 的 ./config.yaml 应最高优先并返回相对路径 %q, got %q", configFileName, got)
	}
}

func TestCopyDirBehavior(t *testing.T) {
	// recursive copy (including subdirectories), skipping existing files, uniform executable bit
	src := fstest.MapFS{
		"scripts/sim.sh":     &fstest.MapFile{Data: []byte("#!/bin/sh\necho hi\n"), Mode: 0o755},
		"scripts/sub/x.sh":   &fstest.MapFile{Data: []byte("echo sub\n"), Mode: 0o644},
		"scripts/sys_cpu.sh": &fstest.MapFile{Data: []byte("#!/bin/sh\necho cpu\n")},
	}
	dst := t.TempDir()

	if err := CopyDir(src, "scripts", dst); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	for _, rel := range []string{"sim.sh", "sub/x.sh", "sys_cpu.sh"} {
		fi, err := os.Stat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("缺少 %s: %v", rel, err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s 应带执行位, mode=%v", rel, fi.Mode())
		}
	}
	// an existing target is skipped, not overwritten (preserving user changes)
	if err := os.WriteFile(filepath.Join(dst, "sim.sh"), []byte("echo 用户版本\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyDir(src, "scripts", dst); err != nil {
		t.Fatalf("CopyDir(2nd): %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "sim.sh"))
	if string(got) != "echo 用户版本\n" {
		t.Errorf("已存在的文件被覆盖了: %q", got)
	}
}

func TestInitExampleWithScripts(t *testing.T) {
	// the complete --init action: example.yaml lands at the default path and scripts/ is
	// copied along into the same directory
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	scriptFS := fstest.MapFS{
		"scripts/sys_cpu.sh": &fstest.MapFile{Data: []byte("#!/bin/sh\necho cpu\n")},
		"scripts/sim.sh":     &fstest.MapFile{Data: []byte("#!/bin/sh\necho sim\n")},
	}

	p, created, err := InitExampleWithScripts(scriptFS, false)
	if err != nil {
		t.Fatalf("InitExampleWithScripts: %v", err)
	}
	want := filepath.Join(home, ".config", configDir, "config.yaml")
	if p != want || !created {
		t.Errorf("p=%q created=%v, 期望 %q true", p, created, want)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(p), "scripts", "sys_cpu.sh")); err != nil {
		t.Errorf("脚本未复制到配置同目录: %v", err)
	}

	// the user edited the config and the script, so a second --init gives created=false and
	// fills in scripts without overwriting
	if err := os.WriteFile(p, []byte("# user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(p), "scripts", "sim.sh"), []byte("# mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, created, err = InitExampleWithScripts(scriptFS, false)
	if err != nil || created {
		t.Fatalf("再次 init 应 created=false, got %v err=%v", created, err)
	}
	cfgGot, _ := os.ReadFile(p)
	if string(cfgGot) != "# user\n" {
		t.Errorf("已有配置被覆盖")
	}
	simGot, _ := os.ReadFile(filepath.Join(filepath.Dir(p), "scripts", "sim.sh"))
	if string(simGot) != "# mine\n" {
		t.Errorf("已存在脚本被覆盖")
	}
}

// —— bundled example variants (--init picks by locale) ——

// TestChineseExampleDiffersOnlyInComments pins the two bundled examples to one config: strip
// the comments from both and the remaining lines must match exactly. That is what lets the
// content assertions on the bundled example (main_test.go's TestBundledExample*) cover the
// Chinese variant too.
func TestChineseExampleDiffersOnlyInComments(t *testing.T) {
	en, zh := configLines(exampleYAML), configLines(exampleZHCN)
	for i := range en {
		if i >= len(zh) {
			t.Fatalf("中文版少了 %d 行: %d 行之后 example.yaml 还有 %q", len(en)-len(zh), i, en[i])
		}
		if en[i] != zh[i] {
			t.Fatalf("去注释后第 %d 行不同:\nexample.yaml:     %s\nexample.zh-CN.yaml: %s", i+1, en[i], zh[i])
		}
	}
	if len(zh) > len(en) {
		t.Fatalf("中文版多了 %d 行,首个多出的行: %q", len(zh)-len(en), zh[len(en)])
	}
}

// configLines returns a YAML document's effective lines: comments and blank lines dropped,
// trailing whitespace trimmed. Leading indentation is kept, so two files that differ in
// nesting still compare unequal.
func configLines(src []byte) []string {
	var out []string
	for line := range strings.SplitSeq(string(src), "\n") {
		if s := strings.TrimRight(stripYAMLComment(line), " \t"); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// stripYAMLComment returns line without its comment. A "#" opens one only at the start of a
// line or after whitespace, never inside a quoted scalar (color: '#f8f8f2').
func stripYAMLComment(line string) string {
	var b strings.Builder
	var quote byte // the quote character of an open scalar; 0 = outside one
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return b.String()
		}
		b.WriteByte(c)
	}
	return b.String()
}

// TestIsSimplifiedChinese pins the locale rule: only simplified Chinese counts, and an
// unrecognized zh_* region does not.
func TestIsSimplifiedChinese(t *testing.T) {
	cases := []struct {
		locale string
		want   bool
	}{
		{"zh_CN.UTF-8", true},
		{"zh_SG.UTF-8", true},
		{"zh-Hans-CN", true},
		{"zh_Hans", true},
		{"zh-Hans", true},
		{"zh", true},
		{"zh.UTF-8", true},
		{"zh_CN.GB18030", true},
		{"ZH_cn.UTF-8", true},
		{"zh_CN@euro", true},

		{"zh_TW.UTF-8", false},
		{"zh_HK", false},
		{"zh_MO", false},
		{"zh-Hant-TW", false},
		{"zh_Hant", false},
		{"zh_XX", false},
		{"zh_TW@euro", false},

		{"en_US.UTF-8", false},
		{"en", false},
		{"C", false},
		{"POSIX", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isSimplifiedChinese(c.locale); got != c.want {
			t.Errorf("isSimplifiedChinese(%q) = %v, 期望 %v", c.locale, got, c.want)
		}
	}
}

// TestExampleFor pins which bytes each variant resolves to.
func TestExampleFor(t *testing.T) {
	if got := ExampleFor(true); !bytes.Equal(got, exampleZHCN) {
		t.Errorf("ExampleFor(true) 应是中文注释版")
	}
	if got := ExampleFor(false); !bytes.Equal(got, exampleYAML) {
		t.Errorf("ExampleFor(false) 应是英文版")
	}
}
