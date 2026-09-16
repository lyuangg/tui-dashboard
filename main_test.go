package main

import (
	"bytes"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
	"tui-dashboard/internal/ui"
)

// TestStartupLogQuietByDefault pins that startup log lines are silent by default and written
// to stderr only under -v (they are printed before the alt screen is entered, so setting
// AltScreen covers them; hence silent by default).
func TestStartupLogQuietByDefault(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	startupLog{}.Printf("默认不该出现 %d", 1)
	if buf.Len() != 0 {
		t.Errorf("默认不该有任何输出, got %q", buf.String())
	}

	startupLog{on: true}.Printf("带 -v 该出现 %d", 1)
	if !strings.Contains(buf.String(), "带 -v 该出现 1") {
		t.Errorf("-v 应照常输出, got %q", buf.String())
	}
}

// TestInitCopiesRealScripts verifies what --init actually does: the scripts/ tree embedded at
// the module root is copied next to the config (the real InitExampleWithScripts plus the real
// embed.FS, end to end).
func TestInitCopiesRealScripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	p, created, err := config.InitExampleWithScripts(scriptFS, false)
	if err != nil {
		t.Fatalf("InitExampleWithScripts: %v", err)
	}
	want := filepath.Join(home, ".config", "tui-dashboard", "config.yaml")
	if p != want || !created {
		t.Fatalf("p=%q created=%v, 期望 %q true", p, created, want)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("配置文件未落位: %v", err)
	}

	// The scripts/ tree appears next to the config, and every embedded script is on disk
	// (with its executable bit)
	dst := filepath.Join(filepath.Dir(p), "scripts")
	fs.WalkDir(scriptFS, "scripts", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := filepath.Join(dst, filepath.FromSlash(path[len("scripts/"):]))
		fi, err := os.Stat(rel)
		if err != nil {
			t.Fatalf("缺脚本 %s: %v", rel, err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s 应带执行位, mode=%v", rel, fi.Mode())
		}
		return nil
	})

	// Idempotent: a second init does not overwrite, created=false
	if _, created, err := config.InitExampleWithScripts(scriptFS, false); err != nil || created {
		t.Fatalf("再次 init 应 created=false, got %v err=%v", created, err)
	}
}

// TestBundledExampleScriptsExist pins that every script the bundled example names is one this
// repo ships. The scan is over the raw text, so the commented-out cal/weather blocks count too.
func TestBundledExampleScriptsExist(t *testing.T) {
	ref := regexp.MustCompile(`\./scripts/[A-Za-z0-9_.-]+`)
	for _, c := range []struct {
		name string
		zh   bool
	}{{"example.yaml", false}, {"example.zh-CN.yaml", true}} {
		seen := ref.FindAllString(string(config.ExampleFor(c.zh)), -1)
		if len(seen) == 0 {
			t.Fatalf("%s: 没扫到任何 ./scripts 引用,这条核对形同虚设", c.name)
		}
		for _, s := range seen {
			if _, err := fs.Stat(scriptFS, strings.TrimPrefix(s, "./")); err != nil {
				t.Errorf("%s: 引用的脚本未随仓库分发: %s (%v)", c.name, s, err)
			}
		}
	}
}

// TestScriptDir pins that the only source of the cmd working directory is the directory of
// the config file actually loaded; with no config file (falling back to the bundled example)
// it returns "" (cmd.Dir unset, the process CWD inherited).
//
// Each case first t.Chdirs into its own directory, so the outcome does not drift with where
// the test happens to run.
func TestScriptDir(t *testing.T) {
	// (a) no config file → ""
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())
	if got := scriptDir(""); got != "" {
		t.Errorf("无配置文件应返回空串(继承进程 CWD), got %q", got)
	}

	// (b) ./config.yaml in the CWD → the absolute directory holding it (neither "." nor "")
	if err := os.WriteFile("config.yaml", []byte("sources: []"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The expected value must be Getwd(), not the raw t.TempDir() string: on macOS TMPDIR is
	// /var/…, while getcwd returns the normalized /private/var/…, and that is the one
	// filepath.Abs follows.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := scriptDir(""); got != cwd {
		t.Errorf("CWD 配置应返回其绝对目录 %q, got %q", cwd, got)
	}

	// (c) --config points elsewhere → the absolute directory holding that file
	cfgDir := t.TempDir()
	p := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(p, []byte("sources: []"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := scriptDir(p); got != cfgDir {
		t.Errorf("explicit 应返回 %q, got %q", cfgDir, got)
	}
}

// TestResolveCmdDir pins the fallback from the config file's directory to the process CWD: it
// applies only when the config directory has no scripts/ tree and the CWD has one. The config
// directory wins whenever it has a tree of its own.
func TestResolveCmdDir(t *testing.T) {
	mkScripts := func(dir string) string {
		if err := os.Mkdir(filepath.Join(dir, "scripts"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Chdir(t.TempDir()) // CWD without scripts/
	bare := t.TempDir()
	withScripts := mkScripts(t.TempDir())

	if got := resolveCmdDir(""); got != "" {
		t.Errorf("无配置文件应保持空串, got %q", got)
	}
	if got := resolveCmdDir(withScripts); got != withScripts {
		t.Errorf("配置目录有 scripts/ 应用它自己 %q, got %q", withScripts, got)
	}
	if got := resolveCmdDir(bare); got != bare {
		t.Errorf("两处都没有时维持配置目录 %q, got %q", bare, got)
	}

	// CWD now has a tree: the bare config directory falls back, the other one does not
	t.Chdir(mkScripts(t.TempDir()))
	if got := resolveCmdDir(bare); got != "" {
		t.Errorf("配置目录无 scripts/、CWD 有时应回退到 CWD(空串), got %q", got)
	}
	if got := resolveCmdDir(withScripts); got != withScripts {
		t.Errorf("配置目录有 scripts/ 时不该回退, got %q", got)
	}
}

// TestHasScriptsDir pins what counts as a usable scripts/ tree: only a directory does.
func TestHasScriptsDir(t *testing.T) {
	dir := t.TempDir()
	if hasScriptsDir(dir) {
		t.Errorf("空目录不该算作有 scripts/, got true")
	}
	if err := os.Mkdir(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasScriptsDir(dir) {
		t.Errorf("有 scripts/ 目录应返回 true, got false")
	}

	fileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fileDir, "scripts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if hasScriptsDir(fileDir) {
		t.Errorf("名为 scripts 的普通文件不算目录, got true")
	}

	if hasScriptsDir(filepath.Join(dir, "no-such-dir")) {
		t.Errorf("不存在的路径应返回 false, got true")
	}
}

// TestInitHintCondition pins when startup prints the --init hint: no config tier resolves and
// the process CWD has no scripts/ tree. Either factor going away switches it off — the CWD
// case is what running from the repo root looks like.
func TestInitHintCondition(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())

	fires := func() bool { return config.ResolvePath("") == "" && !hasScriptsDir(".") }
	if !fires() {
		t.Fatalf("无配置文件且无 scripts/ 时应触发提示")
	}

	// a config file in the CWD (tier 2): the bundled example is no longer what gets loaded
	if err := os.WriteFile("config.yaml", []byte("sources: []"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fires() {
		t.Errorf("已有 ./config.yaml 时不该触发")
	}
	if err := os.Remove("config.yaml"); err != nil {
		t.Fatal(err)
	}

	// a scripts/ tree: the example's relative commands resolve, as in a checkout
	if err := os.Mkdir("scripts", 0o755); err != nil {
		t.Fatal(err)
	}
	if fires() {
		t.Errorf("CWD 有 scripts/ 时不该触发")
	}
}

// TestBundledExampleTypes pins that the type: of every source in the bundled example is
// required and valid — the same check main runs fatally at startup, moved forward to go test
// so a typo in the example is not found only at runtime.
func TestBundledExampleTypes(t *testing.T) {
	t.Chdir(t.TempDir()) // clean CWD: does not hit ./config.yaml
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	data, src, err := config.Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if src != "bundled example" {
		t.Fatalf("应命中内置 example, got %q", src)
	}
	cfg, err := config.FromBytes(data)
	if err != nil {
		t.Fatalf("解析内置 example: %v", err)
	}
	for _, s := range cfg.Sources {
		if _, ok := source.ParseType(s.Type); !ok {
			t.Errorf("源 %q 的 type: %q 未知,可用: %s",
				s.Name, s.Type, strings.Join(source.TypeNames, " | "))
		}
	}
}

// —— source-declared type × panel capability ——

// TestCheckWidgetTypes pins this decision table. It only produces one hint line in the
// startup log and never blocks startup — a mismatched panel draws its default look at
// runtime (with the reason) and the rest runs as usual.
func TestCheckWidgetTypes(t *testing.T) {
	build := func(srcType, widget string) *config.Config {
		return &config.Config{
			Sources: []config.Source{{Name: "s", Cmd: "./s.sh", Type: srcType}},
			Layout:  []config.Row{{Widgets: []config.Widget{{Type: widget, Source: "s"}}}},
		}
	}
	for _, c := range []struct {
		name    string
		srcType string
		widget  string
		warn    bool
	}{
		// Matches: no hint expected
		{"map 源喂 stat", "map", "stat", false},
		{"map 源喂 chart", "map", "chart", false},
		{"number 源喂 stat", "number", "stat", false},
		{"number 源喂 chart", "number", "chart", false},
		{"array 源喂 bar", "array", "bar", false},
		{"timeseries 源喂 chart", "timeseries", "chart", false},
		{"timeseries 源喂 heatmap", "timeseries", "heatmap", false},
		{"table 源喂 table", "table", "table", false},
		{"logs 源喂 logs", "logs", "logs", false},
		{"text 面板读什么源都行", "table", "text", false},
		{"无 source 的静态 text", "", "text", false},

		// Mismatch: a hint is expected
		{"number 源喂 table", "number", "table", true},
		{"map 源喂 table", "map", "table", true},
		{"table 源喂 chart", "table", "chart", true},
		{"array 源喂 table", "array", "table", true},
		{"logs 源喂 stat", "logs", "stat", true},
		{"map 源喂 logs", "map", "logs", true},
		{"array 源喂 heatmap", "array", "heatmap", true},
		{"text 源喂 gauge", "text", "gauge", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cf := build(c.srcType, c.widget)
			if c.srcType == "" {
				cf.Layout[0].Widgets[0].Source = ""
			}
			got := checkWidgetTypes(cf)
			if (len(got) > 0) != c.warn {
				t.Fatalf("提示=%v, 期望有提示=%v", got, c.warn)
			}
			if !c.warn {
				return
			}
			// The hint should name the location, the source, the declared type, and the type
			// the panel accepts
			for _, kw := range []string{"layout[0] widget[0]", "source=s", "source declares " + c.srcType, "panel takes"} {
				if !strings.Contains(got[0], kw) {
					t.Errorf("提示应含 %q, got: %s", kw, got[0])
				}
			}
		})
	}
}

// TestBundledExampleWidgetTypes pins that the bundled example has no mismatched panel and
// that every panel shows something; moved forward to go test so a panel silently falling
// back to its default look is not found only when running. Also pins that the example
// demonstrates the form with value: omitted.
func TestBundledExampleWidgetTypes(t *testing.T) {
	t.Chdir(t.TempDir()) // clean CWD: does not hit ./config.yaml
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	data, _, err := config.Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	cfg, err := config.FromBytes(data)
	if err != nil {
		t.Fatalf("解析内置 example: %v", err)
	}
	if msgs := checkWidgetTypes(cfg); len(msgs) > 0 {
		t.Fatalf("内置 example 不该有类型对不上的面板: %v", msgs)
	}
	// Pins that the example demonstrates the form with value: omitted: the table widget's
	// output is that table itself, so the proc panel drops value:.
	sawOmitted := false
	for _, row := range cfg.Layout {
		for _, w := range row.Widgets {
			if w.Source == "proc" && w.Type == "table" && w.Value == "" {
				sawOmitted = true
			}
		}
	}
	if !sawOmitted {
		t.Error("内置 example 应含一个省略了 value: 的 proc table")
	}
}

// TestBundledExampleCoversEveryType pins that the bundled example uses every source type
// and every widget type at least once — miss one and that form has nowhere to be seen in
// the example.
func TestBundledExampleCoversEveryType(t *testing.T) {
	t.Chdir(t.TempDir()) // clean CWD: does not hit ./config.yaml
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	data, _, err := config.Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	cfg, err := config.FromBytes(data)
	if err != nil {
		t.Fatalf("解析内置 example: %v", err)
	}

	seenSrc := map[string]bool{}
	for _, s := range cfg.Sources {
		seenSrc[s.Type] = true
	}
	for _, name := range source.TypeNames {
		if !seenSrc[name] {
			t.Errorf("内置 example 缺一个 type: %s 的源", name)
		}
	}

	// The widget type list matches that of theme.canonicalWidget in internal/ui and
	// validTypes in config (neither is exported); adding a new widget type later turns this
	// test red first.
	seenWidget := map[string]bool{}
	for _, row := range cfg.Layout {
		for _, w := range row.Widgets {
			seenWidget[w.Type] = true
		}
	}
	for _, name := range []string{"stat", "chart", "bar", "gauge", "heatmap", "table", "logs", "text"} {
		if !seenWidget[name] {
			t.Errorf("内置 example 缺一个 type: %s 的面板", name)
		}
	}
}

// TestBundledExampleRowTitles pins that the bundled example's row/section titles do not
// reference a data source — a reference makes the title resolve to nothing. Startup only
// hints and never blocks, hence moved forward to go test.
func TestBundledExampleRowTitles(t *testing.T) {
	t.Chdir(t.TempDir()) // clean CWD: does not hit ./config.yaml
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	data, _, err := config.Locate("")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	cfg, err := config.FromBytes(data)
	if err != nil {
		t.Fatalf("解析内置 example: %v", err)
	}
	if msgs := ui.RowTitleSourceRefs(cfg); len(msgs) > 0 {
		t.Fatalf("内置 example 的行标题不该引用数据源: %v", msgs)
	}
}

// —— 配置装载（启动与重载共用）——

// TestBuildDashboard pins the loader startup and the reload key share: a valid config yields the
// layout, the poll interval and a running manager, and every failure startup used to log.Fatalf on
// comes back as an error — which is what lets a failed reload leave the dashboard alone.
//
// The theme cases read ui.ActiveThemeName rather than rendered colors: the name says what landed,
// while colors depend on the terminal's color profile.
func TestBuildDashboard(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const valid = `
theme: dracula
poll_interval: 2s
sources:
  - name: t
    type: text
    cmd: echo hi
    interval: 1s
layout:
  - title: T
    widgets:
      - type: text
        source: t
`

	t.Run("合法配置", func(t *testing.T) {
		ui.UseTheme("") // known baseline, and restored for whatever test runs next
		t.Cleanup(func() { ui.UseTheme("") })

		mgr, layout, poll, err := buildDashboard(write(t, valid), startupLog{})
		if err != nil {
			t.Fatalf("合法配置不应报错: %v", err)
		}
		defer mgr.Close()

		if len(layout) != 1 || layout[0].Title != "T" {
			t.Errorf("layout 应与文件一致, got %+v", layout)
		}
		if poll != 2*time.Second {
			t.Errorf("poll 应取 poll_interval (2s), got %v", poll)
		}
		if got := ui.ActiveThemeName(); got != "dracula" {
			t.Errorf("配置里的 theme 应被应用, got %q", got)
		}
	})

	// 从有 theme 改回不写 theme 必须真的回到 default: 写成 `cfg.Theme != "" && UseTheme(...)`
	// 的话, 这里会静静地留在上一个主题。
	t.Run("去掉 theme 回到 default", func(t *testing.T) {
		ui.UseTheme("dracula")
		t.Cleanup(func() { ui.UseTheme("") })

		mgr, _, _, err := buildDashboard(write(t, strings.Replace(valid, "theme: dracula\n", "", 1)), startupLog{})
		if err != nil {
			t.Fatalf("不写 theme 不应报错: %v", err)
		}
		defer mgr.Close()

		if got := ui.ActiveThemeName(); got != "default" {
			t.Errorf("配置里不再写 theme 时应回到 default, got %q", got)
		}
	})

	t.Run("未知主题", func(t *testing.T) {
		ui.UseTheme("nord")
		t.Cleanup(func() { ui.UseTheme("") })

		_, _, _, err := buildDashboard(write(t, strings.Replace(valid, "theme: dracula", "theme: nonsense", 1)), startupLog{})
		if err == nil {
			t.Fatal("未知主题应报错, 而不是退出进程")
		}
		if !strings.Contains(err.Error(), "unknown theme") {
			t.Errorf("错误应指明是主题名不认识, got %v", err)
		}
		if got := ui.ActiveThemeName(); got != "nord" {
			t.Errorf("主题校验失败不应改动当前主题, got %q", got)
		}
	})

	t.Run("YAML 写坏", func(t *testing.T) {
		if _, _, _, err := buildDashboard(write(t, "sources: [\n  - name"), startupLog{}); err == nil {
			t.Error("解析失败应报错")
		}
	})

	t.Run("未知源类型", func(t *testing.T) {
		ui.UseTheme("dracula")
		t.Cleanup(func() { ui.UseTheme("") })

		_, _, _, err := buildDashboard(write(t, strings.Replace(valid, "type: text", "type: nonsense", 1)), startupLog{})
		if err == nil {
			t.Fatal("未知源类型应报错")
		}
		if !strings.Contains(err.Error(), "nonsense") {
			t.Errorf("错误应指明是哪个类型, got %v", err)
		}
		// 源类型校验排在主题之前, 所以这条路径同样不能留下改过的主题
		if got := ui.ActiveThemeName(); got != "dracula" {
			t.Errorf("源类型校验失败不应改动当前主题, got %q", got)
		}
	})

	t.Run("无配置文件且无 scripts 目录", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", t.TempDir())
		if _, _, _, err := buildDashboard("", startupLog{}); err == nil {
			t.Error("落到内置示例又没有 scripts/ 树时应报错")
		}
	})
}
