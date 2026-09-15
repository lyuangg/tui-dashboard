package source

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// hhmmss formats a log time as "15:04:05", for assertions.
func hhmmss(t time.Time) string { return t.Format("15:04:05") }

// —— how each type is parsed, one test per type ——
//
// Type-independent behavior (raw text kept, mismatch, empty output and BOM) is in parse_test.go;
// JSON is not an input channel is in parsefmt_test.go.

// TestArrayTextFormat one number per line; a non-numeric line mixed in makes the whole frame a
// mismatch.
func TestArrayTextFormat(t *testing.T) {
	p, err := ParseOutput([]byte("1.5\n2\n3.25\n"), time.Now(), opts(TypeArray))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if !p.Has || !reflect.DeepEqual(p.Val.Points, []Point{{V: 1.5}, {V: 2}, {V: 3.25}}) {
		t.Fatalf("应解出 [1.5 2 3.25], got %+v", p.Val)
	}
	// every point's TS must be zero: array and timeseries share the Points field, so "no
	// time axis" can only be expressed by this convention; the renderer picks the ordinal
	// axis or the time axis by whether TS is zero.
	for i, pt := range p.Val.Points {
		if !pt.TS.IsZero() {
			t.Errorf("array 第 %d 个点不该带时间戳: %v", i, pt.TS)
		}
	}
	if want := "1.5\n2\n3.25"; p.Text != want {
		t.Errorf("原文应保留(去首尾空白): got %q, 期望 %q", p.Text, want)
	}
	// a line of text in the middle is a mismatch
	p, _ = ParseOutput([]byte("1\n说明\n3"), time.Now(), opts(TypeArray))
	if p.Has {
		t.Errorf("夹了非数字行就不该产出值: %+v", p.Val)
	}
}

// TestMapTextFormat one "key: value" per line. Numeric values are stored as numbers (a trailing
// punctuation mark is allowed), the rest as strings, and the key order preserves the order of
// appearance in the script.
func TestMapTextFormat(t *testing.T) {
	raw := "load1: 1.25\nPages free: 3882.\nstate: up\n"
	p, err := ParseOutput([]byte(raw), time.Now(), opts(TypeMap))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if !p.Has {
		t.Fatalf("键值行应解出 map: %+v logs=%v", p.Val, p.Logs)
	}
	if !reflect.DeepEqual(p.Val.Keys, []string{"load1", "Pages free", "state"}) {
		t.Errorf("键序应保住脚本里的出现顺序: %v", p.Val.Keys)
	}
	if v, _ := toFloat(p.Val.Map["load1"]); v != 1.25 {
		t.Errorf("load1 = %v, 期望 1.25", p.Val.Map["load1"])
	}
	if v, _ := toFloat(p.Val.Map["Pages free"]); v != 3882 {
		t.Errorf("Pages free = %v, 期望 3882(剥尾句点)", p.Val.Map["Pages free"])
	}
	// integer values are stored as float64 too: int64 would make a template's
	// {{printf "%.0f" .x}} print %!f(int64=21).
	if _, ok := p.Val.Map["Pages free"].(float64); !ok {
		t.Errorf("整数值也应存 float64, got %T(%v)", p.Val.Map["Pages free"], p.Val.Map["Pages free"])
	}
	if s, ok := GetString(p.Val.Map, "state"); !ok || s != "up" {
		t.Errorf("非数值值应存字符串: state = %v", p.Val.Map["state"])
	}
	if p.Text != strings.TrimSpace(raw) {
		t.Errorf("原文应保留(去首尾空白), got %q", p.Text)
	}
}

// TestMapTextFormatNeedsColon a line without a colon is not a key-value line; a log line with a
// time prefix ("10:00:01 INFO …") must not be parsed into the key "10".
func TestMapTextFormatNeedsColon(t *testing.T) {
	for _, in := range []string{
		"hello world\nsecond line",
		"10:00:01 INFO consumer started\n10:00:02 WARN slow",
		"这是一段很长的说明文字,没有冒号,也不像表格,就一行。",
	} {
		p, err := ParseOutput([]byte(in), time.Now(), opts(TypeMap))
		if err != nil {
			t.Fatalf("%q 不应报错: %v", in, err)
		}
		if p.Has {
			t.Errorf("%q 不该被解成 map: %+v", in, p.Val)
		}
		if p.Text != in {
			t.Errorf("%q 应原样落 Text, got %q", in, p.Text)
		}
	}
}

// TestTableTextFormats tables with a header parse in all three of comma, tab and space-aligned
// form.
func TestTableTextFormats(t *testing.T) {
	cases := []string{
		"name,size,cap\n/,466Gi,7%\n/dev,10Gi,10%",       // CSV
		"name\tsize\tcap\n/\t466Gi\t7%\n/dev\t10Gi\t10%", // TSV
		"name size cap\n/ 466Gi 7%\n/dev 10Gi 10%",       // space-aligned
	}
	for _, raw := range cases {
		p, err := ParseOutput([]byte(raw), time.Now(), opts(TypeTable))
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if len(p.Val.Rows) != 2 {
			t.Errorf("%q: rows = %d, 期望 2", raw, len(p.Val.Rows))
			continue
		}
		if !reflect.DeepEqual(p.Val.Cols, []string{"name", "size", "cap"}) {
			t.Errorf("%q: 列序应取表头: %v", raw, p.Val.Cols)
			continue
		}
		first := p.Val.Rows[0]
		if f, _ := toFloat(first["cap"]); f != 7 {
			t.Errorf("%q: cap 应解析成数字 7, got %v", raw, first["cap"])
		}
		if s, ok := GetString(first, "name"); !ok || s != "/" {
			t.Errorf("%q: name = %v", raw, first["name"])
		}
		if p.Text != raw {
			t.Errorf("表格应保留原文 Text")
		}
	}
}

// TestTableNoHeader without a header the column names are generated mechanically as col_1..col_N
// and the first row counts as data.
func TestTableNoHeader(t *testing.T) {
	p, err := ParseOutput([]byte("1,2\n3,4\n5,6"), time.Now(),
		Opts{Type: TypeTable, Header: false})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !reflect.DeepEqual(p.Val.Cols, []string{"col_1", "col_2"}) {
		t.Errorf("列名应机械生成: %v", p.Val.Cols)
	}
	if len(p.Val.Rows) != 3 {
		t.Errorf("首行也算数据: got %d 行, 期望 3", len(p.Val.Rows))
	}
}

// TestTableMismatchedRowsSkipped trailing rows whose column count differs from the header (notes,
// blank lines) are skipped, instead of the whole table being judged a mismatch.
func TestTableMismatchedRowsSkipped(t *testing.T) {
	raw := "a,b\n1,2\n说明一下数据来源\n3,4\n"
	p, err := ParseOutput([]byte(raw), time.Now(), opts(TypeTable))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(p.Val.Rows) != 2 {
		t.Errorf("列数不一致的行应跳过, got %d 行", len(p.Val.Rows))
	}
}

// TestTableSpaceAlignedNeedsTwoRows space separation is hard to distinguish from prose, so the
// space form requires at least 2 data rows; comma and tab are explicit structure and are accepted
// with 1 row.
func TestTableSpaceAlignedNeedsTwoRows(t *testing.T) {
	p, _ := ParseOutput([]byte("hello world\nsecond line"), time.Now(), opts(TypeTable))
	if p.Has {
		t.Errorf("两行两词的空格文本不该被当成表: %+v", p.Val.Cols)
	}
	p, _ = ParseOutput([]byte("a,b\n1,2"), time.Now(), opts(TypeTable))
	if !p.Has || len(p.Val.Rows) != 1 {
		t.Errorf("逗号表 1 行数据就该认: %+v logs=%v", p.Val, p.Logs)
	}
}

// TestTimeseriesTextFormat one "<timestamp> <value>" per line; a line holding only a value gets the
// frame time, and the two forms can be mixed.
func TestTimeseriesTextFormat(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	p, err := ParseOutput([]byte("1757584000 1.5\n2.5\n"), ts, opts(TypeTimeseries))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if len(p.Val.Points) != 2 {
		t.Fatalf("应解出 2 个点: %+v", p.Val.Points)
	}
	if got := p.Val.Points[0].TS.Unix(); got != 1757584000 {
		t.Errorf("带时间戳的行用脚本给的时间: %d", got)
	}
	if !p.Val.Points[1].TS.Equal(ts.Add(time.Millisecond)) {
		t.Errorf("不带时间戳的行用帧时间补(按序号错开 1ms): %v", p.Val.Points[1].TS)
	}
	// several timestamp-less points in the same frame are staggered by 1ms, so they do not land
	// on the same x
	p, _ = ParseOutput([]byte("1\n2\n3"), ts, opts(TypeTimeseries))
	if len(p.Val.Points) != 3 || p.Val.Points[1].TS.Sub(p.Val.Points[0].TS) != time.Millisecond {
		t.Errorf("无时间戳的点应按序号错开: %+v", p.Val.Points)
	}
}

// TestLogsTextFormat time and level may both be omitted, in which case the frame time and INFO are
// used; the body is never mistaken for a time or a level.
func TestLogsTextFormat(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	p, err := ParseOutput([]byte("10:00:01 ERROR boom\n只有正文的一行\nWARN 光有级别\nWARN\n"), ts, opts(TypeLogs))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if len(p.Val.Logs) != 4 {
		t.Fatalf("应解出 4 条: %+v", p.Val.Logs)
	}
	if l := p.Val.Logs[0]; hhmmss(l.Time) != "10:00:01" || l.Level != "ERROR" || l.Msg != "boom" {
		t.Errorf("第 1 条 = %+v", l)
	}
	if l := p.Val.Logs[1]; hhmmss(l.Time) != "15:30:00" || l.Level != "INFO" || l.Msg != "只有正文的一行" {
		t.Errorf("无时间无级别时该用帧时间 + INFO: %+v", l)
	}
	if l := p.Val.Logs[2]; l.Level != "WARN" || l.Msg != "光有级别" {
		t.Errorf("第 3 条 = %+v", l)
	}
	// a line holding only a level keeps the whole line as the body; the content must not be dropped
	if l := p.Val.Logs[3]; l.Level != "WARN" || l.Msg != "WARN" {
		t.Errorf("只剩级别时不该把正文丢空: %+v", l)
	}
}

// TestLogsLinePrefixes the time prefix is stripped by field index, not cut by length. Cutting by
// length would be off by one indent width, shaving the head of the body and losing the level with
// it, so indented lines get their own coverage.
func TestLogsLinePrefixes(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	p, err := ParseOutput([]byte("第一行\n   10:00:01 ERROR 缩进三格\n\t10:00:02 WARN 制表缩进\n  ERROR 缩进两级\n"),
		ts, opts(TypeLogs))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	for i, want := range []struct{ time, level, msg string }{
		{"15:30:00", "INFO", "第一行"},
		{"10:00:01", "ERROR", "缩进三格"},
		{"10:00:02", "WARN", "制表缩进"},
		{"15:30:00", "ERROR", "缩进两级"},
	} {
		if l := p.Val.Logs[i]; hhmmss(l.Time) != want.time || l.Level != want.level || l.Msg != want.msg {
			t.Errorf("第 %d 条 = {Time:%s Level:%s Msg:%q}, 期望 {%s %s %q}",
				i, hhmmss(l.Time), l.Level, l.Msg, want.time, want.level, want.msg)
		}
	}
}

// TestLogsAndTimeseriesSpaceDate a space-separated date and time occupies two fields and cannot be
// recognized from the first field alone ("2026-09-08"). Logs and timeseries both go through
// leadingTimestamp, so both parse.
func TestLogsAndTimeseriesSpaceDate(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)

	p, _ := ParseOutput([]byte("2026-09-08 10:00:01 ERROR 出事了\n"), ts, opts(TypeLogs))
	if l := p.Val.Logs[0]; hhmmss(l.Time) != "10:00:01" || l.Level != "ERROR" || l.Msg != "出事了" {
		t.Errorf("日志行该吃下两段式时间: %+v", l)
	}

	p, _ = ParseOutput([]byte("2026-09-08 10:00:01 42.5\n"), ts, opts(TypeTimeseries))
	if len(p.Val.Points) != 1 {
		t.Fatalf("时间序列该吃下两段式时间: %+v logs=%v", p.Val, p.Logs)
	}
	pt := p.Val.Points[0]
	if !pt.TS.Equal(time.Date(2026, 9, 8, 10, 0, 1, 0, time.Local)) || pt.V != 42.5 {
		t.Errorf("点 = %+v", pt)
	}
}

// TestTimeseriesMillisecond millisecond timestamps share one path with seconds, with the unit
// decided by magnitude. The fractional part of a millisecond value must not be lost: dividing by
// 1000 before splitting introduces float64 error around 1.7e12.
func TestTimeseriesMillisecond(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	p, _ := ParseOutput([]byte("1757584000 1\n1757584000123 2\n"), ts, opts(TypeTimeseries))
	if len(p.Val.Points) != 2 {
		t.Fatalf("应解出 2 个点: %+v logs=%v", p.Val, p.Logs)
	}
	if got := p.Val.Points[1].TS; !got.Equal(time.Unix(1757584000, 123000000).Local()) {
		t.Errorf("毫秒时间戳 = %v, 期望 %v", got, time.Unix(1757584000, 123000000).Local())
	}
	// two columns of numbers must not be taken as a date and a time
	if p, _ := ParseOutput([]byte("42.5 43.5\n"), ts, opts(TypeTimeseries)); p.Has {
		t.Errorf("两列数值不该被当成时间戳: %+v", p.Val)
	}
}
