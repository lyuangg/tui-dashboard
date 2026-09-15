package source

import (
	"reflect"
	"testing"
	"time"
)

// TestTypeNamesAndParse verifies that TypeNames agrees with ParseType and String in both
// directions, and covers invalid type names.
func TestTypeNamesAndParse(t *testing.T) {
	want := []string{"text", "number", "array", "timeseries", "map", "table", "logs"}
	if !reflect.DeepEqual(TypeNames, want) {
		t.Fatalf("TypeNames = %v, 期望 %v", TypeNames, want)
	}
	for i, n := range want {
		got, ok := ParseType(n)
		if !ok || got != Type(i) {
			t.Errorf("ParseType(%q) = %v/%v, 期望 %v/true", n, got, ok, Type(i))
		}
		if got.String() != n {
			t.Errorf("Type(%d).String() = %q, 期望 %q", i, got.String(), n)
		}
	}
	// type: is mandatory and has no last-resort name: the empty string and any unknown name are invalid
	for _, bad := range []string{"", "auto", "AUTO", "Text", "list", "float", "log"} {
		if _, ok := ParseType(bad); ok {
			t.Errorf("ParseType(%q) 应不合法(type: 必填,没有兜底的名字)", bad)
		}
	}
}

// TestParsePerType verifies the value shape produced by each type's canonical text format: the
// type is the input and the shape is the uniquely determined output, with no guesswork.
func TestParsePerType(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	for _, c := range []struct {
		typ  Type
		in   string
		want func(t *testing.T, v Value)
	}{
		{TypeText, "随便一段话,没有结构", func(t *testing.T, v Value) {
			if v.Text != "随便一段话,没有结构" {
				t.Errorf("text = %q", v.Text)
			}
		}},
		{TypeNumber, "42.5", func(t *testing.T, v Value) {
			if v.Num != 42.5 {
				t.Errorf("num = %v", v.Num)
			}
		}},
		{TypeArray, "1\n2\n3", func(t *testing.T, v Value) {
			if !reflect.DeepEqual(v.Points, []Point{{V: 1}, {V: 2}, {V: 3}}) {
				t.Errorf("points = %v", v.Points)
			}
		}},
		{TypeTimeseries, "1757584000 1.5\n1757584001 2.5", func(t *testing.T, v Value) {
			if len(v.Points) != 2 || v.Points[0].TS.Unix() != 1757584000 || v.Points[1].V != 2.5 {
				t.Errorf("points = %v", v.Points)
			}
		}},
		{TypeMap, "load1: 1.2\nload5: 1.0", func(t *testing.T, v Value) {
			if !reflect.DeepEqual(v.Keys, []string{"load1", "load5"}) {
				t.Errorf("keys = %v(应保脚本里的顺序)", v.Keys)
			}
			if f, _ := toFloat(v.Map["load1"]); f != 1.2 {
				t.Errorf("map[load1] = %v", v.Map["load1"])
			}
		}},
		{TypeTable, "fs cap\n/dev/disk 7\n/dev/disk2 38", func(t *testing.T, v Value) {
			if !reflect.DeepEqual(v.Cols, []string{"fs", "cap"}) {
				t.Errorf("cols = %v", v.Cols)
			}
			if len(v.Rows) != 2 {
				t.Errorf("rows = %v", v.Rows)
			}
		}},
		{TypeLogs, "10:00:01 INFO up\n10:00:02 ERROR boom", func(t *testing.T, v Value) {
			if len(v.Logs) != 2 || v.Logs[1].Level != "ERROR" {
				t.Errorf("logs = %+v", v.Logs)
			}
		}},
	} {
		p, err := ParseOutput([]byte(c.in), ts, Opts{Type: c.typ, Header: true})
		if err != nil {
			t.Fatalf("%v: 不该返回 error: %v", c.typ, err)
		}
		if !p.Has {
			t.Fatalf("%v: 规范输入应产出值, got logs=%v", c.typ, p.Logs)
		}
		if p.Val.Type != c.typ {
			t.Fatalf("%v: Val.Type = %v", c.typ, p.Val.Type)
		}
		c.want(t, p.Val)
	}
}

// TestValueHasOnlyDeclaredField each type fills only its own single payload field; several
// non-empty fields at once is an invalid state and the producing side is responsible for never
// creating it.
func TestValueHasOnlyDeclaredField(t *testing.T) {
	ts := time.Now()
	bodies := []struct {
		typ Type
		in  string
	}{
		{TypeText, "hi"}, {TypeNumber, "1"}, {TypeArray, "1\n2"},
		{TypeTimeseries, "1757584000 1"}, {TypeMap, "a: 1"},
		{TypeTable, "a b\n1 2\n3 4"}, {TypeLogs, "hello"},
	}
	for _, b := range bodies {
		p, _ := ParseOutput([]byte(b.in), ts, Opts{Type: b.typ, Header: true})
		v := p.Val
		set := map[string]bool{
			"Text": v.Text != "", "Num": v.Type == TypeNumber,
			"Points": v.Points != nil, "Map": v.Map != nil,
			"Rows": v.Rows != nil, "Logs": v.Logs != nil,
		}
		n := 0
		for _, ok := range set {
			if ok {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%v: 应恰好填一个载荷字段, got %v", b.typ, set)
		}
	}
}

// TestValueHas an empty Value and a piece of empty text share TypeText as the zero value, so
// emptiness can only be tested through Has.
func TestValueHas(t *testing.T) {
	var zero Value
	if zero.Has() {
		t.Error("零值 Value 应是空的")
	}
	text := Value{Type: TypeText, Text: "x"}
	if !text.Has() {
		t.Error("有正文的 text 是非空")
	}
	// text with an empty body: likewise "nothing inside" (the script had no output this frame)
	if (Value{Type: TypeText}).Has() {
		t.Error("空文本的 text 应是空的")
	}
	// number is the exception and ignores its payload: the number 0 is a valid value too
	if !(Value{Type: TypeNumber}).Has() {
		t.Error("number 的 0 是有效值")
	}
	// "empty but valid" for each container type
	for _, v := range []Value{
		{Type: TypeArray, Points: []Point{}}, {Type: TypeTable, Rows: []map[string]any{}},
		{Type: TypeLogs, Logs: []LogLine{}}, {Type: TypeTimeseries, Points: []Point{}},
	} {
		if !v.Has() {
			t.Errorf("%v: 空容器是有效值", v.Type)
		}
	}
}

// TestTemplateDoc a map source's template view is that object itself; the other types each provide
// one canonical key (an internal representation, not a user contract).
func TestTemplateDoc(t *testing.T) {
	if d := (Value{Type: TypeMap, Map: map[string]any{"a": 1}}).templateDoc(); d["a"] != 1 {
		t.Errorf("map 源应直接暴露那个对象: %v", d)
	}
	if d := (Value{Type: TypeNumber, Num: 3}).templateDoc(); d["value"] != float64(3) {
		t.Errorf("number → {value}(恒为 float64,不因整数变 int64): %v", d)
	}
	if d := (Value{Type: TypeArray, Points: []Point{{V: 1}, {V: 2}}}).templateDoc(); len(d["values"].([]any)) != 2 {
		t.Errorf("array → {values}: %v", d)
	}
	// array's values is a list of numbers rather than a list of points: timestamps do not enter
	// the template view
	if d := (Value{Type: TypeArray, Points: []Point{{V: 7}}}).templateDoc(); d["values"].([]any)[0] != float64(7) {
		t.Errorf("array 的 values 应是数值: %v", d)
	}
	if d := (Value{Type: TypeTable, Rows: []map[string]any{{"a": 1}}}).templateDoc(); len(d["rows"].([]any)) != 1 {
		t.Errorf("table → {rows}: %v", d)
	}
	// text / timeseries / logs have no field that can be named
	for _, v := range []Value{{Type: TypeText, Text: "x"}, {Type: TypeTimeseries}, {Type: TypeLogs}} {
		if d := v.templateDoc(); d != nil {
			t.Errorf("%v 不该有模板文档: %v", v.Type, d)
		}
	}
}

// TestParseTimestamp verifies the several recognizable forms of a leading line timestamp; an
// unrecognizable one falls back to the frame time, which timestampedLines handles, so only the
// recognizable side is covered here.
func TestParseTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	for _, c := range []struct {
		in   string
		want time.Time
	}{
		{"1757584000", time.Unix(1757584000, 0).Local()},
		{"1757584000.5", time.Unix(1757584000, 500000000).Local()},
		// milliseconds: the window sits directly above seconds (from 1e11); the integer part is
		// split as milliseconds and the fraction keeps its precision
		{"1757584000123", time.Unix(1757584000, 123000000).Local()},
		{"1757584000123.5", time.Unix(1757584000, 123500000).Local()},
		{"15:04:05", time.Date(2026, 9, 8, 15, 4, 5, 0, time.Local)},
		{"2026-09-08 15:04:05", time.Date(2026, 9, 8, 15, 4, 5, 0, time.Local)},
		{"2026-09-08T15:04:05+08:00", time.Date(2026, 9, 8, 15, 4, 5, 0, time.FixedZone("", 8*3600))},
	} {
		got, ok := parseTimestamp(c.in, now)
		if !ok || !got.Equal(c.want) {
			t.Errorf("parseTimestamp(%q) = %v/%v, 期望 %v", c.in, got, ok, c.want)
		}
	}
	// unrecognizable: ordinary numbers that are too small, prose, and RFC3339 without a zone
	// (the time.RFC3339 layout requires Z07:00)
	for _, bad := range []string{"", "abc", "42", "1.5", "0",
		"2026-09-08T15:04:05", "2026-09-08"} {
		if _, ok := parseTimestamp(bad, now); ok {
			t.Errorf("parseTimestamp(%q) 不该认出来", bad)
		}
	}
	// microseconds and nanoseconds are deliberately not recognized: the range from 1e14 up
	// belongs to common data values such as byte counts and nanosecond latencies, and reading
	// them as a time is worse than not reading them at all.
	for _, bad := range []string{"1500000000000000", "100000000000000"} {
		if _, ok := parseTimestamp(bad, now); ok {
			t.Errorf("parseTimestamp(%q) 在微秒档,应有意不认", bad)
		}
	}
}
