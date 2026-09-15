package source

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"tui-dashboard/internal/config"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// mkSource builds a script source; mut may override fields. The default timeout of 5s keeps the
// default min(interval, 5s) from killing a normal script in a slow environment; the default type is
// text, and typed overrides it when another type is needed.
func mkSource(interval time.Duration, mut ...func(*config.Source)) config.Source {
	c := config.Source{
		Type:     TypeText.String(),
		Interval: config.Duration{Duration: interval},
		Timeout:  config.Duration{Duration: 5 * time.Second},
	}
	for _, f := range mut {
		f(&c)
	}
	return c
}

// typed declares the source as a given type; what is written into the config is the matching name
// from TypeNames.
func typed(t Type) func(*config.Source) {
	return func(c *config.Source) { c.Type = t.String() }
}

// name and cmd override the source name and command fields respectively.
func name(n string) func(*config.Source) { return func(c *config.Source) { c.Name = n } }
func cmd(s string) func(*config.Source)  { return func(c *config.Source) { c.Cmd = s } }

// one builds a Manager holding a single source and registers close cleanup, returning that Manager
// and the source name.
func one(t *testing.T, interval time.Duration, mut ...func(*config.Source)) (*Manager, string) {
	t.Helper()
	c := mkSource(interval, mut...)
	m := New([]config.Source{c}, "")
	t.Cleanup(m.Close)
	return m, c.Name
}

func TestManagerTypeMap(t *testing.T) {
	script := writeScript(t, `printf '%s\n' 'val: 7' 'tag: x'`)
	m, n := one(t, 20*time.Millisecond, typed(TypeMap), name("s1"), cmd(script))

	waitData(t, m, 2*time.Second)
	v, ok := m.Snapshot(n)
	if !ok {
		t.Fatal("snapshot 失败")
	}
	if v.Type != TypeMap || v.V.Type != TypeMap {
		t.Errorf("Type = %v/%v, 期望 map", v.Type, v.V.Type)
	}
	if f, _ := toFloat(v.V.Map["val"]); f != 7 {
		t.Errorf("Map[val] = %v", v.V.Map["val"])
	}
	if v.LastErr != nil {
		t.Errorf("不应有错误: %v", v.LastErr)
	}
	// each numeric field of a map source enters its own history series (chart/bar data)
	if len(v.Hist["val"]) == 0 {
		t.Error("val 应进入历史序列")
	}
	// no diagnostics means no log lines: logs come only from the script's own output,
	// the parse layer generates none
	if len(v.Logs) != 0 {
		t.Errorf("无诊断时不该有日志行: %v", v.Logs)
	}
}

func TestManagerTypeNumber(t *testing.T) {
	m, n := one(t, 20*time.Millisecond, typed(TypeNumber), name("cpu"), cmd(writeScript(t, `echo 42.5`)))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if v.V.Type != TypeNumber || v.V.Num != 42.5 {
		t.Errorf("Num = %v(type %v), 期望 42.5", v.V.Num, v.V.Type)
	}
	if v.Text != "42.5" {
		t.Errorf("标量输出也应保留原文: got %q, 期望 %q", v.Text, "42.5")
	}
	// the cross-frame history of number lives under a fixed key, and chart reads exactly that key
	if len(v.Hist["value"]) == 0 {
		t.Error("number 源应每帧记一个采样点")
	}
	if !v.Hist["value"][0].TS.After(time.Time{}) {
		t.Error("number 的采样点应带程序打的时间戳")
	}
}

func TestManagerTypeArray(t *testing.T) {
	m, n := one(t, 20*time.Millisecond, typed(TypeArray), name("a"), cmd(writeScript(t, `printf '1\n2\n3\n'`)))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if !reflect.DeepEqual(v.V.Points, []Point{{V: 1}, {V: 2}, {V: 3}}) {
		t.Errorf("Points = %v, 期望 [1 2 3](TS 全零)", v.V.Points)
	}
	// array does not accumulate across frames: it describes one frame's batch of values,
	// with an ordinal x-axis
	if len(v.Hist) != 0 {
		t.Errorf("array 不该进历史序列, got %v", v.Hist)
	}
}

func TestManagerTypeTimeseries(t *testing.T) {
	m, n := one(t, 100*time.Millisecond, typed(TypeTimeseries), name("ts"),
		cmd(writeScript(t, `printf '1757584000 1.5\n1757584001 2.5\n'`)))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if len(v.V.Points) != 2 {
		t.Fatalf("Points = %v, 期望 2 个", v.V.Points)
	}
	if got := v.V.Points[0].TS.Unix(); got != 1757584000 {
		t.Errorf("首点时间戳 = %d, 期望 1757584000(脚本自带)", got)
	}
	if v.V.Points[1].V != 2.5 {
		t.Errorf("次点值 = %v, 期望 2.5", v.V.Points[1].V)
	}
	// deduped by timestamp, so a script emitting the same window every frame does not stack up
	if len(v.Hist["value"]) != 2 {
		t.Errorf("历史 = %v, 期望仍是那 2 个点(不随帧数增长)", v.Hist["value"])
	}
}

func TestManagerTypeLogs(t *testing.T) {
	m, n := one(t, 20*time.Millisecond, typed(TypeLogs), name("lg"),
		cmd(writeScript(t, `printf '15:32:01 INFO  booted\n15:33:02 WARN  hiccup\n'`)))

	// the log buffer appends across frames; wait for two lines to accumulate
	deadline := time.After(2 * time.Second)
	for {
		v, ok := m.Snapshot(n)
		if ok && len(v.Logs) >= 2 {
			if v.Logs[0].Level != "INFO" || !strings.Contains(v.Logs[0].Msg, "booted") {
				t.Errorf("首条 = %+v", v.Logs[0])
			}
			if v.Logs[1].Level != "WARN" {
				t.Errorf("次条级别 = %q, 期望 WARN", v.Logs[1].Level)
			}
			if !strings.Contains(v.Text, "booted") {
				t.Errorf("logs 源同样保留原文: %q", v.Text)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("超时未攒到日志行: %+v", v.Logs)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestManagerTypeText(t *testing.T) {
	m, n := one(t, 20*time.Millisecond, typed(TypeText), name("txt"),
		cmd(writeScript(t, `printf 'hello\n  world\n'`)))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if !strings.Contains(v.Text, "hello") {
		t.Errorf("文本输出应存进 Text, got %q", v.Text)
	}
	// declaring text means no parsing: a multi-line body is not split into structure just
	// because it resembles another format
	if v.V.Type != TypeText || v.V.Map != nil || v.V.Points != nil {
		t.Errorf("text 源不该有结构: %+v", v.V)
	}
}

// TestManagerTextKeptForStructuredSource a structured source keeps the raw text too, so the text
// widget is usable for every source.
func TestManagerTextKeptForStructuredSource(t *testing.T) {
	raw := "val: 7\ntag: x"
	m, n := one(t, 20*time.Millisecond, typed(TypeMap), name("kv"),
		cmd(writeScript(t, `printf '%s\n' 'val: 7' 'tag: x'`)))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if !v.V.Has() {
		t.Fatal("map 源应产出值")
	}
	if v.Doc == nil {
		t.Fatal("模板面应拿得到 Doc")
	}
	if v.Text != raw {
		t.Errorf("结构化源也应保留原文: got %q, 期望 %q", v.Text, raw)
	}
}

// TestManagerTypeMismatch when the declared type does not match the script output, no value is
// produced; the raw text is kept and a WARN is left.
func TestManagerTypeMismatch(t *testing.T) {
	// the script prints one line of comma-separated numbers while the source is declared number
	m, n := one(t, 20*time.Millisecond, typed(TypeNumber), name("s"), cmd(writeScript(t, `printf '1,2,3\n'`)))

	deadline := time.After(2 * time.Second)
	for {
		v, ok := m.Snapshot(n)
		if ok && len(v.Logs) > 0 {
			if v.V.Has() {
				t.Errorf("没对上时不该产出值: %+v", v.V)
			}
			if !strings.Contains(v.Text, "1,2,3") {
				t.Errorf("原文应保留: %q", v.Text)
			}
			if l := v.Logs[0]; l.Level != "WARN" || !strings.Contains(l.Msg, "type: number does not match") {
				t.Errorf("应留一条没对上的 WARN: %+v", l)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("超时未观察到没对上的 WARN")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestManagerScriptFailure(t *testing.T) {
	m, n := one(t, 20*time.Millisecond, typed(TypeText), name("bad"),
		cmd(writeScript(t, `echo "oops boom" >&2; exit 3`)))

	// wait for the failure to take effect
	deadline := time.After(1 * time.Second)
	for {
		v, ok := m.Snapshot(n)
		if ok && v.LastErr != nil {
			if !strings.Contains(v.LastErr.Error(), "oops boom") {
				t.Errorf("错误应带 stderr: %v", v.LastErr)
			}
			// the failure should write a WARN log entry
			found := false
			for _, l := range v.Logs {
				if l.Level == "WARN" && strings.Contains(l.Msg, "fetch failed") {
					found = true
				}
			}
			if !found {
				t.Errorf("失败应 push WARN 日志")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("超时未观察到失败")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestManagerErrorsSorted the footer's error lines are sorted by source name. Sources come from a
// map, so the iteration order is random; unsorted, the errors would jump position every frame, and
// with more than 3 errors even which 3 are shown would be unstable (the footer takes only the first 3).
func TestManagerErrorsSorted(t *testing.T) {
	// names deliberately out of order: the map order is random anyway, which helps expose an
	// unsorted implementation
	names := []string{"zeta", "alpha", "mid", "beta"}
	srcs := make([]config.Source, 0, len(names)+1)
	for _, n := range names {
		srcs = append(srcs, mkSource(20*time.Millisecond, typed(TypeText), name(n),
			cmd(`echo boom >&2; exit 1`))) // a source that fails, should reach the footer
	}
	// a source that fetches successfully must not appear in the footer
	srcs = append(srcs, mkSource(20*time.Millisecond, typed(TypeText), name("ok"), cmd(`echo fine`)))

	m := New(srcs, "")
	t.Cleanup(m.Close)

	deadline := time.After(2 * time.Second)
	for {
		errs := m.Errors()
		if len(errs) == len(names) {
			// read a few more rounds, so a random order that happens to be lexicographic
			// does not slip through
			for range 20 {
				errs = m.Errors()
				if !sort.StringsAreSorted(errs) {
					t.Fatalf("Errors 未按源名排序: %v", errs)
				}
			}
			for _, e := range errs {
				if strings.Contains(e, "⚠ ok:") {
					t.Errorf("取数成功的源不该进页脚: %q", e)
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("超时未观察到 %d 条错误, 得到 %v", len(names), m.Errors())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestManagerScriptTimeout(t *testing.T) {
	m, n := one(t, 30*time.Millisecond, typed(TypeText), name("slow"),
		cmd(writeScript(t, `sleep 30`)),
		func(c *config.Source) { c.Timeout = config.Duration{Duration: 300 * time.Millisecond} })

	deadline := time.After(2 * time.Second)
	for {
		v, ok := m.Snapshot(n)
		if ok && v.LastErr != nil {
			return // the timeout took effect
		}
		select {
		case <-deadline:
			t.Fatal("sleep 脚本应触发超时")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestManagerHistoryCap the history series is trimmed to history_cap.
func TestManagerHistoryCap(t *testing.T) {
	m, n := one(t, 10*time.Millisecond, typed(TypeMap), name("cap"),
		cmd(writeScript(t, `printf 'qps: 1\n'`)),
		func(c *config.Source) { c.HistoryCap = 5 })

	time.Sleep(700 * time.Millisecond)
	v, _ := m.Snapshot(n)
	if got := len(v.Hist["qps"]); got == 0 || got > 5 {
		t.Errorf("历史应被截到 1..5, got %d", got)
	}
}

// TestManagerLogCap the log buffer is trimmed to log_cap, independently of history_cap.
func TestManagerLogCap(t *testing.T) {
	// 4 lines per frame, capacity 3: appended per frame and trimmed by entry, ending with
	// no more than 3
	m, n := one(t, 20*time.Millisecond, typed(TypeLogs), name("cap"),
		cmd(writeScript(t, `printf 'a\nb\nc\nd\n'`)),
		func(c *config.Source) { c.LogCap = 3 })

	time.Sleep(400 * time.Millisecond)
	v, _ := m.Snapshot(n)
	if len(v.Logs) > 3 {
		t.Errorf("日志超 cap: %d", len(v.Logs))
	}
	if len(v.Logs) == 0 {
		t.Error("日志缓冲不该是空的")
	}
}

func TestManagerNotFoundAndHasData(t *testing.T) {
	m, _ := one(t, 10*time.Millisecond, typed(TypeNumber), name("a"), cmd(writeScript(t, `echo 42`)))
	if _, ok := m.Snapshot("nope"); ok {
		t.Error("未知 source 应 ok=false")
	}
	waitData(t, m, 1*time.Second)
	if !m.HasData() {
		t.Error("有数据后 HasData 应为 true")
	}
}

func TestManagerCloseIdempotent(t *testing.T) {
	m := New([]config.Source{mkSource(10*time.Millisecond, typed(TypeNumber), name("a"),
		cmd(writeScript(t, `echo 1`)))}, "")
	m.Close()
	m.Close() // idempotent, must not panic or deadlock
}

// mkScriptDir builds a directory holding ./data.sh (which reads val.txt in the same directory) and
// val.txt, used to verify the execution base of a relative-path script.
func mkScriptDir(t *testing.T, val string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.sh"), []byte("#!/bin/sh\ncat ./val.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "val.txt"), []byte(val+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runRel runs a source with cmd="./data.sh" in the given working directory and returns the fetched
// number.
func runRel(t *testing.T, dir string) float64 {
	t.Helper()
	m := New([]config.Source{mkSource(20*time.Millisecond, typed(TypeNumber),
		name("rel"), cmd("./data.sh"))}, dir)
	defer m.Close()

	waitData(t, m, 1*time.Second)
	v, ok := m.Snapshot("rel")
	if !ok {
		t.Fatal("snapshot 失败")
	}
	if !v.V.Has() {
		t.Fatalf("未取到值, LastErr=%v", v.LastErr)
	}
	return v.V.Num
}

// TestManagerCmdDirConfigDir the config directory serves as the working directory: with the process
// CWD set elsewhere, a relative-path script still runs from the config directory.
func TestManagerCmdDirConfigDir(t *testing.T) {
	work := mkScriptDir(t, "42")
	t.Chdir(t.TempDir())

	if got := runRel(t, work); got != 42 {
		t.Errorf("value = %v, 期望 42(应按配置目录执行)", got)
	}
}

// TestManagerCmdDirConfigDirWins the config directory is always cmd.Dir; a same-named script under
// the process CWD takes no part in the fetch.
func TestManagerCmdDirConfigDirWins(t *testing.T) {
	t.Chdir(mkScriptDir(t, "7"))  // the CWD copy, should be ignored
	other := mkScriptDir(t, "42") // the config-directory copy

	if got := runRel(t, other); got != 42 {
		t.Errorf("value = %v, 期望 42(配置目录优先于 CWD)", got)
	}
}

// TestManagerCmdDirInheritCwd with an empty working directory cmd.Dir is not set and the script
// runs from the process CWD.
func TestManagerCmdDirInheritCwd(t *testing.T) {
	t.Chdir(mkScriptDir(t, "5"))

	if got := runRel(t, ""); got != 5 {
		t.Errorf("value = %v, 期望 5(无配置文件应按 CWD 执行)", got)
	}
}

func waitData(t *testing.T, m *Manager, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		if m.HasData() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("等待首帧数据超时")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestManagerTableFromScript a tab-separated table printed by the script should be a real table
// after execution, parsing and snapshotting.
func TestManagerTableFromScript(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "top.txt")
	if err := os.WriteFile(fixture, []byte(topScriptOut), 0o644); err != nil {
		t.Fatal(err)
	}
	m, n := one(t, 20*time.Millisecond, typed(TypeTable), name("top"),
		cmd(writeScript(t, "cat "+fixture)))

	waitData(t, m, 2*time.Second)
	v, ok := m.Snapshot(n)
	if !ok {
		t.Fatal("snapshot 失败")
	}
	if !rowsHaveKey(v.V.Rows, "COMMAND") {
		t.Fatalf("应解析出进程表, got %v", v.V.Rows)
	}
	if !reflect.DeepEqual(v.V.Cols, []string{"PID", "COMMAND", "%CPU", "MEM"}) {
		t.Errorf("Cols = %v", v.V.Cols)
	}
	if v.Text == "" {
		t.Error("原文应恒存,text widget 才看得到脚本原始输出")
	}
	if v.LastErr != nil {
		t.Errorf("不应有错误: %v", v.LastErr)
	}
}

// TestManagerParseMismatchWarns on a mismatch the raw text is kept and a WARN is left in the
// source's log buffer.
func TestManagerParseMismatchWarns(t *testing.T) {
	// the script prints prose that is not key-value lines while the source is declared map,
	// i.e. a mismatch
	m, n := one(t, 20*time.Millisecond, typed(TypeMap), name("s"),
		cmd(writeScript(t, `printf 'total 3 items ready\n'`)))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if v.V.Has() {
		t.Errorf("没对上时不该产出值: %+v", v.V)
	}
	if !strings.Contains(v.Text, "total 3") {
		t.Errorf("原文应保留: %q", v.Text)
	}
	warned := false
	for _, l := range v.Logs {
		if l.Level == "WARN" && strings.Contains(l.Msg, "does not match") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("应留一条没对上的 WARN: %v", v.Logs)
	}
}

// TestManagerTypeSurvivesBadFrame a mismatched frame only records a warning; the value and its
// views all keep the previous value.
func TestManagerTypeSurvivesBadFrame(t *testing.T) {
	// the first frame prints a valid map, then the output switches to non-map content, so
	// later frames keep mismatching
	flagFile := filepath.Join(t.TempDir(), "flip")
	script := writeScript(t, "if [ -f "+flagFile+
		" ]; then printf 'total 3 items ready\\n'; else printf 'cpu: 31\\n'; fi")
	m, n := one(t, 20*time.Millisecond, typed(TypeMap), name("s1"), cmd(script))

	waitData(t, m, 2*time.Second)
	v, _ := m.Snapshot(n)
	if v.V.Type != TypeMap {
		t.Fatalf("首帧应是 map, got %v", v.V.Type)
	}

	// make every later frame mismatch, then wait for a few frames
	if err := os.WriteFile(flagFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sawWarn := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !sawWarn {
		v, _ = m.Snapshot(n)
		sawWarn = len(v.Logs) > 0 && v.Logs[len(v.Logs)-1].Level == "WARN"
		time.Sleep(20 * time.Millisecond)
	}
	if !sawWarn {
		// without this assertion, a flip that did not take effect would still pass the
		// assertions below
		t.Fatal("没观察到没对上的 WARN,这个用例没验到东西")
	}

	if f, _ := toFloat(v.V.Map["cpu"]); f != 31 {
		t.Errorf("没对上时应沿用旧值, got %+v", v.V.Map)
	}
	if v.V.Type != TypeMap {
		t.Errorf("没对上时应沿用旧形状(不能与旧值脱节), got %v", v.V.Type)
	}
	if len(v.Hist["cpu"]) == 0 {
		t.Error("沿用旧值时历史也不该被清空")
	}
}
