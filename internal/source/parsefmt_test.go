package source

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// topScriptOut is a sample of the script's output after reshaping: tab separated and holding only
// the process table, with the spaces inside COMMAND kept within the cell ("Google Chrome He" is one
// cell).
const topScriptOut = "PID\tCOMMAND\t%CPU\tMEM\n" +
	"155\tWindowServer\t23.4\t521M+\n" +
	"646\t0dcloud\t16.8\t242M+\n" +
	"99215\tGoogle Chrome He\t0.0\t189M\n" +
	"0\tkernel_task\t13.3\t53M-\n"

// topRawOut is the raw output of macOS top and the upstream of topScriptOut: the summary section
// first, the fixed-width aligned process table after. It is not any supported input.
const topRawOut = `Processes: 625 total, 4 running, 621 sleeping, 6185 threads
2026/09/10 20:25:36
Load Avg: 4.09, 4.57, 4.66
CPU usage: 14.28% user, 18.66% sys, 67.4% idle
SharedLibs: 455M resident, 127M data, 74M linkedit.
MemRegions: 816902 total, 3668M resident, 128M private, 1744M shared.
PhysMem: 15G used (3029M wired, 6628M compressor), 76M unused.
VM: 302T vsize, 5702M framework vsize, 43248459(0) swapins, 50807681(0) swapouts.
Networks: packets: 355655290/220G in, 278087391/137G out.
Disks: 870093982/13T read, 575040250/8801G written.

PID    COMMAND          %CPU MEM
99859  cloudd           0.0  11M
99215  Google Chrome He 0.0  189M
98829  Docker Desktop H 0.0  14M `

// tableOpts returns the parse config of a table source, with the first row as header.
func tableOpts() Opts { return Opts{Type: TypeTable, Header: true} }

// TestScriptShapedTableParses a tab-separated table reshaped by the script parses as table: the
// column order comes from the header and the spaces inside COMMAND are preserved.
func TestScriptShapedTableParses(t *testing.T) {
	p, err := ParseOutput([]byte(topScriptOut), time.Now(), tableOpts())
	if err != nil || !p.Has {
		t.Fatalf("应解析出表: err=%v has=%v logs=%v", err, p.Has, p.Logs)
	}
	if len(p.Logs) != 0 {
		t.Errorf("不该告警: %v", p.Logs)
	}
	if want := []string{"PID", "COMMAND", "%CPU", "MEM"}; !reflect.DeepEqual(p.Val.Cols, want) {
		t.Fatalf("列序 = %v, 期望 %v", p.Val.Cols, want)
	}
	rows := p.Val.Rows
	if len(rows) != 4 {
		t.Fatalf("应得 4 行进程, got %d 行", len(rows))
	}
	for i, want := range []struct {
		pid  float64
		comm string
	}{
		{155, "WindowServer"},
		{646, "0dcloud"},
		{99215, "Google Chrome He"}, // spaces inside the command name are preserved
		{0, "kernel_task"},
	} {
		pid, _ := GetFloat(rows[i], "PID")
		comm, _ := GetString(rows[i], "COMMAND")
		if pid != want.pid || comm != want.comm {
			t.Errorf("第 %d 行 = {PID:%v COMMAND:%q}, 期望 {%v %q}", i, pid, comm, want.pid, want.comm)
		}
	}
	// numeric cells become numbers automatically (%CPU is read as 0, not as the string "0.0")
	if f, ok := GetFloat(rows[2], "%CPU"); !ok || f != 0 {
		t.Errorf("%%CPU 应转成数字 0, got %v,%v", f, ok)
	}
	// the raw text is always kept: the text widget's last-resort fallback
	if p.Text != strings.TrimSpace(topScriptOut) {
		t.Error("原文应恒为该次输出的原样")
	}
}

// TestRawTopOutputYieldsFakeTable raw top output fed to type: table neither errors nor counts as a
// mismatch; it produces a fake table: the commas in the summary line are taken as CSV separators,
// the column name becomes "Processes: 625 total", and not one real process row gets in. This test
// guards the convention that the script must reshape its own output into canonical form.
//
// A failure means the separator priority or the fixture changed, not that the parse layer is
// expected to understand top.
func TestRawTopOutputYieldsFakeTable(t *testing.T) {
	p, err := ParseOutput([]byte(topRawOut), time.Now(), tableOpts())
	if err != nil {
		t.Fatalf("不该报错(解不成也是 Has=false,不是 error): %v", err)
	}
	if rowsHaveKey(p.Val.Rows, "COMMAND") {
		t.Errorf("原样 top 输出不该认出进程表(该由脚本先转成规范格式):\n%+v", p.Val.Cols)
	}
	if len(p.Val.Cols) > 0 && p.Val.Cols[0] != "Processes: 625 total" {
		t.Errorf("夹具或分隔符优先级变了,假表的列名不再是概览行的逗号切法: %v", p.Val.Cols)
	}
}

// rowsHaveKey reports whether any row in rows carries that key.
func rowsHaveKey(rows []map[string]any, key string) bool {
	for _, r := range rows {
		if _, ok := r[key]; ok {
			return true
		}
	}
	return false
}

// TestJSONIsNotAnInputChannel valid JSON fed to a source declared as another type is treated as a
// mismatch: no value is produced, the raw text is kept and a WARN is recorded. A source's output is
// decided solely by the declared type:, and JSON is not a second input channel.
func TestJSONIsNotAnInputChannel(t *testing.T) {
	cases := []struct {
		typ Type
		in  string
	}{
		{TypeMap, `{"a": 1, "b": 2}`},
		{TypeTable, `[{"a":1,"b":2},{"a":3,"b":4}]`},
		{TypeArray, `[1.5, 2, 3.25]`},
	}
	for _, c := range cases {
		p, err := ParseOutput([]byte(c.in), time.Now(), opts(c.typ))
		if err != nil {
			t.Fatalf("%v: 不应报错: %v", c.typ, err)
		}
		if p.Has {
			t.Errorf("%v: JSON 不该被解成值: %+v", c.typ, p.Val)
		}
		if p.Text != c.in {
			t.Errorf("%v: 原文应原样保留, got %q", c.typ, p.Text)
		}
		if len(p.Logs) != 1 || p.Logs[0].Level != "WARN" {
			t.Errorf("%v: 应记一条没对上的 WARN: %v", c.typ, p.Logs)
		}
	}
}

// TestTextNeverParsed declaring text means no parsing: content that looks like JSON is taken as is.
func TestTextNeverParsed(t *testing.T) {
	p, err := ParseOutput([]byte(`{"a": 1}`), time.Now(), Opts{Type: TypeText})
	if err != nil || !p.Has {
		t.Fatalf("text 恒有值: err=%v has=%v", err, p.Has)
	}
	if p.Val.Text != `{"a": 1}` {
		t.Errorf("text 应是原文: %q", p.Val.Text)
	}
}

// TestObjectAsNumberIsMismatch an object is not a number: a mismatched shape counts as a mismatch
// and the raw text is kept.
func TestObjectAsNumberIsMismatch(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	p, err := ParseOutput([]byte(`{"a": 1}`), ts, Opts{Type: TypeNumber})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if p.Has {
		t.Errorf("对象不是 number: %+v", p.Val)
	}
	if len(p.Logs) != 1 || !strings.Contains(p.Logs[0].Msg, "does not match") {
		t.Errorf("应留一条没对上的 WARN: %v", p.Logs)
	}
	if !strings.Contains(p.Text, `{"a": 1}`) {
		t.Errorf("原文应保留: %q", p.Text)
	}
}
