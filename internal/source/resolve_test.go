package source

import (
	"reflect"
	"testing"
	"time"
)

// view builds a snapshot of a map source, with doc as that source's document. A nil doc means the
// source has not produced a value yet; snapshots of the other types are built by declared.
func view(doc map[string]any, hist map[string][]Point, text string) SourceView {
	v := Value{Type: TypeMap, Map: doc}
	if doc != nil {
		v.Keys = sortedKeys(doc)
	}
	return SourceView{Type: TypeMap, V: v, Doc: doc, Hist: hist, Text: text}
}

// declared builds a snapshot that yields a value by declared type, for test cases that omit value:.
func declared(v Value, hist map[string][]Point) SourceView {
	return SourceView{Type: v.Type, V: v, Doc: v.templateDoc(), Hist: hist}
}

// —— paths ——

func TestPathSegs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"{{.used_pct}}", []string{"used_pct"}},
		{"{{ .used_pct }}", []string{"used_pct"}}, // tolerant of spaces
		{"{{.mem.used_pct}}", []string{"mem", "used_pct"}},
		{"{{.services.qps}}", []string{"services", "qps"}},
		{"used_pct", []string{"used_pct"}}, // bare field name
		{"a.b", []string{"a", "b"}},        // a bare name goes through the same path handling
		{"{{.}}", nil},                     // no field specified
		{"", nil},
		{`{{printf "%.0f" .x}}`, []string{`{{printf "%.0f" .x}}`}}, // a non-single path keeps the whole string as one segment name
	} {
		if got := pathSegs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("pathSegs(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// —— omitted value::the shape comes from the source's declared type ——

// TestResolveDeclared without value: the value comes from the source's declared type and does not
// depend on any key recorded by the parse layer. Covered once per type.
func TestResolveDeclared(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)

	// text yields the raw text
	if v, ok := Resolve(declared(Value{Type: TypeText, Text: "hello"}, nil), ""); !ok || v.Type != TypeText || v.Text != "hello" {
		t.Errorf("text 源应给原文, got %+v ok=%v", v, ok)
	}
	// number yields the series accumulated across frames, not the current value (stat takes the
	// last point, chart draws the whole line)
	hist := []Point{{TS: ts, V: 40}, {TS: ts.Add(time.Second), V: 42}}
	v, ok := Resolve(declared(Value{Type: TypeNumber, Num: 42}, map[string][]Point{histValue: hist}), "")
	if !ok || v.Type != TypeTimeseries || !reflect.DeepEqual(v.Points, hist) {
		t.Errorf("number 源应给历史序列, got %+v ok=%v", v, ok)
	}
	// before the first frame, with an empty history: fall back to this frame's number
	if v, ok := Resolve(declared(Value{Type: TypeNumber, Num: 7}, nil), ""); !ok || v.Type != TypeNumber || v.Num != 7 {
		t.Errorf("无历史时应退回当帧值, got %+v ok=%v", v, ok)
	}
	// array: one frame's batch of values, an all-zero TS meaning no time axis
	if v, ok := Resolve(declared(Value{Type: TypeArray, Points: []Point{{V: 1}, {V: 2}, {V: 3}}}, nil), ""); !ok || !reflect.DeepEqual(v.Points, []Point{{V: 1}, {V: 2}, {V: 3}}) {
		t.Errorf("array 源应给 Points, got %+v ok=%v", v, ok)
	}
	// timeseries: the run carrying the script's own timestamps
	pts := []Point{{TS: ts, V: 1.5}}
	if v, ok := Resolve(declared(Value{Type: TypeTimeseries, Points: pts}, nil), ""); !ok || !reflect.DeepEqual(v.Points, pts) {
		t.Errorf("timeseries 源应给 Points, got %+v ok=%v", v, ok)
	}
	// map: the whole object; whether it can be used is decided by the widget's capability
	if v, ok := Resolve(declared(Value{Type: TypeMap, Map: map[string]any{"a": 1}}, nil), ""); !ok || v.Type != TypeMap {
		t.Errorf("map 源应给整个对象, got %+v ok=%v", v, ok)
	}
	// table: that table
	rows := []map[string]any{{"a": 1}}
	if v, ok := Resolve(declared(Value{Type: TypeTable, Rows: rows, Cols: []string{"a"}}, nil), ""); !ok || len(v.Rows) != 1 {
		t.Errorf("table 源应给 Rows, got %+v ok=%v", v, ok)
	}
	// logs: the log lines
	if v, ok := Resolve(declared(Value{Type: TypeLogs, Logs: []LogLine{{Msg: "x"}}}, nil), ""); !ok || len(v.Logs) != 1 {
		t.Errorf("logs 源应给日志行, got %+v ok=%v", v, ok)
	}
	// a source that has not produced a value yet: no type can yield one (an empty container is a
	// different matter, the parser must really have produced an empty value)
	if _, ok := Resolve(SourceView{Type: TypeNumber}, ""); ok {
		t.Error("还没数据的源不该给得出值")
	}
}

// TestResolvePath with value: the value is looked up in the document by path; a path that resolves
// to nothing counts as a missing field and is not an error.
func TestResolvePath(t *testing.T) {
	svcs := []any{map[string]any{"qps": 800}, map[string]any{"qps": 950}}
	for _, c := range []struct {
		name string
		doc  map[string]any
		expr string
		want Type
		ok   bool
	}{
		{"顶层字段", map[string]any{"used": 73.5}, "{{.used}}", TypeNumber, true},
		{"数字字符串归一成 number", map[string]any{"used": "73.5"}, "{{.used}}", TypeNumber, true},
		{"普通字符串归一成 text", map[string]any{"used": "73.5%"}, "{{.used}}", TypeText, true},
		{"数值数组", map[string]any{"v": []any{1, 2, 3}}, "{{.v}}", TypeArray, true},
		{"记录数组投影", map[string]any{"svcs": svcs}, "{{.svcs.qps}}", TypeArray, true},
		{"记录数组本体是张表", map[string]any{"svcs": svcs}, "{{.svcs}}", TypeTable, true},
		{"单个对象是张一行的表", map[string]any{"a": map[string]any{"b": 1}}, "{{.a}}", TypeMap, true},
		{"首段是源名前缀·自动跳过", map[string]any{"used": 5}, "{{.mem.used}}", TypeNumber, true},
		{"裸字段名照旧可用", map[string]any{"used": 5}, "used", TypeNumber, true},
		{"路径取不到", map[string]any{"a": 1}, "{{.nope}}", 0, false},
		{"没有文档", nil, "{{.a}}", 0, false},
		{"非单一路径表达式", map[string]any{"a": 1}, `{{printf "%.0f" .a}}`, 0, false},
	} {
		got, ok := Resolve(view(c.doc, nil, ""), c.expr)
		if ok != c.ok || (ok && got.Type != c.want) {
			t.Errorf("%s: Resolve(%q) = %+v,%v, 期望类型 %v,%v", c.name, c.expr, got, ok, c.want, c.ok)
		}
	}
}

// TestResolveHistPreferred when the path lands on a top-level numeric field the history series is
// preferred; the current value is only its last point.
func TestResolveHistPreferred(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	hist := []Point{{TS: ts, V: 10}, {TS: ts.Add(time.Second), V: 30}}
	v, ok := Resolve(view(map[string]any{"cpu": 30}, map[string][]Point{"cpu": hist}, ""), "{{.cpu}}")
	if !ok || v.Type != TypeTimeseries || !reflect.DeepEqual(v.Points, hist) {
		t.Fatalf("顶层字段有历史时应给序列, got %+v ok=%v", v, ok)
	}
	// with the first segment as a source-name prefix, stripping it still lands on the same
	// top-level field and the history is looked up as before
	v, ok = Resolve(view(map[string]any{"cpu": 30}, map[string][]Point{"cpu": hist}, ""), "{{.mem.cpu}}")
	if !ok || v.Type != TypeTimeseries {
		t.Errorf("{{.mem.cpu}} 应同样走历史, got %+v ok=%v", v, ok)
	}
	// no history (the field is not numeric or the first frame has not arrived): the current
	// value itself is returned
	v, ok = Resolve(view(map[string]any{"cpu": 30}, nil, ""), "{{.cpu}}")
	if !ok || v.Type != TypeNumber || v.Num != 30 {
		t.Errorf("无历史时应给当帧值, got %+v ok=%v", v, ok)
	}
}

// TestResolveProjectionNotStolenByHist a path pointing at a field inside an array of records goes
// through projection and is not stolen by the history of a same-named top-level field.
func TestResolveProjectionNotStolenByHist(t *testing.T) {
	ts := time.Now()
	v := view(
		map[string]any{
			"qps":      1,
			"services": []any{map[string]any{"qps": 800}, map[string]any{"qps": 950}},
		},
		// history of the top-level qps, must not be taken by the projection path
		map[string][]Point{"qps": {{TS: ts, V: 1}, {TS: ts, V: 1}}},
		"",
	)
	got, ok := Resolve(v, "{{.services.qps}}")
	if !ok || !reflect.DeepEqual(got.Points, []Point{{V: 800}, {V: 950}}) {
		t.Errorf("投影应取 services[].qps = [800 950], got %+v ok=%v", got, ok)
	}
	if got.Type != TypeArray {
		t.Errorf("投影出的一列是 array, got %v", got.Type)
	}
	// from the same data, the top-level path still goes through the history
	got, ok = Resolve(v, "{{.qps}}")
	if !ok || got.Type != TypeTimeseries || len(got.Points) != 2 {
		t.Errorf("顶层 qps 应走历史, got %+v ok=%v", got, ok)
	}
}

// —— normalize: how a resolved value becomes a Type ——

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  any
		want Type
		ok   bool
	}{
		{"对象 → map", map[string]any{"a": 1}, TypeMap, true},
		{"对象数组 → table", []any{map[string]any{"a": 1}}, TypeTable, true},
		{"空对象数组 → table", []any{}, TypeTable, true},
		{"数值数组 → array", []any{1, 2}, TypeArray, true},
		{"数字字符串 → number", "7", TypeNumber, true},
		{"普通字符串 → text", "hello", TypeText, true},
		{"数值 → number", 7.5, TypeNumber, true},
		{"整数 → number", int64(7), TypeNumber, true},
		{"混入非数的数组既不是表也不是序列", []any{1, "a"}, 0, false},
		{"bool 认不出", true, 0, false},
		{"nil 认不出", nil, 0, false},
	} {
		got, ok := normalize(c.raw)
		if ok != c.ok || (ok && got.Type != c.want) {
			t.Errorf("%s: normalize(%v) = %+v,%v, 期望类型 %v,%v", c.name, c.raw, got, ok, c.want, c.ok)
		}
	}
	// a map literal iterates unordered, so normalization takes the lexicographic key order for
	// stability; a real source's key order is preserved by keyValueLines in the script's order
	// of appearance (see TestMapTextFormat).
	if got, _ := normalize(map[string]any{"b": 1, "a": 2}); !reflect.DeepEqual(got.Keys, []string{"a", "b"}) {
		t.Errorf("map 的键序应稳定, got %v", got.Keys)
	}
}

// —— three readers: read a normalized value as the kind a widget needs ——

func TestToScalar(t *testing.T) {
	ts := time.Now()
	for _, c := range []struct {
		name string
		val  Value
		want float64
		ok   bool
	}{
		{"number", Value{Type: TypeNumber, Num: 42.5}, 42.5, true},
		{"number 的 0 也是值", Value{Type: TypeNumber}, 0, true},
		{"array 取末个", Value{Type: TypeArray, Points: []Point{{V: 1}, {V: 2}, {V: 3}}}, 3, true},
		{"空 array 取不到", Value{Type: TypeArray, Points: []Point{}}, 0, false},
		{"timeseries 取末点", Value{Type: TypeTimeseries, Points: []Point{{TS: ts, V: 1}, {TS: ts, V: 9}}}, 9, true},
		{"空 timeseries 取不到", Value{Type: TypeTimeseries, Points: []Point{}}, 0, false},
		{"text 给不出数", Value{Type: TypeText, Text: "42"}, 0, false},
		{"map 给不出数(不点名就是歧义)", Value{Type: TypeMap, Map: map[string]any{"a": 1}}, 0, false},
		{"table 给不出数", Value{Type: TypeTable, Rows: []map[string]any{{"a": 1}}}, 0, false},
	} {
		got, ok := ToScalar(c.val)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: ToScalar = %v,%v, 期望 %v,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestToPoints the x-axis of a series is decided by the points' TS: array's TS is zero and the
// renderer draws by ordinal; the other types' points carry time and are drawn on a real time axis.
func TestToPoints(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	for _, c := range []struct {
		name string
		val  Value
		want []float64
		axis bool // whether the expected points carry time
		ok   bool
	}{
		{"number 是一点", Value{Type: TypeNumber, Num: 7}, []float64{7}, false, true},
		{"array 按序号", Value{Type: TypeArray, Points: []Point{{V: 1}, {V: 2}, {V: 3}}}, []float64{1, 2, 3}, false, true},
		{"空 array 画不出", Value{Type: TypeArray, Points: []Point{}}, nil, false, false},
		{"timeseries 带脚本时间", Value{Type: TypeTimeseries, Points: []Point{{TS: ts, V: 1}, {TS: ts, V: 2}}}, []float64{1, 2}, true, true},
		{"空 timeseries 画不出", Value{Type: TypeTimeseries, Points: []Point{}}, nil, false, false},
		{"map 画不出(不点名就是歧义)", Value{Type: TypeMap, Map: map[string]any{"a": 1}}, nil, false, false},
		{"table 画不出", Value{Type: TypeTable, Rows: []map[string]any{{"a": 1}}}, nil, false, false},
	} {
		got, ok := ToPoints(c.val)
		if ok != c.ok {
			t.Errorf("%s: ToPoints ok = %v, 期望 %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		vals := make([]float64, len(got))
		for i, p := range got {
			vals[i] = p.V
		}
		if !reflect.DeepEqual(vals, c.want) {
			t.Errorf("%s: ToPoints = %v, 期望 %v", c.name, vals, c.want)
		}
		if c.axis != !got[0].TS.IsZero() {
			t.Errorf("%s: 点的 TS = %v, 期望带时间=%v", c.name, got[0].TS, c.axis)
		}
	}
}

func TestToRecords(t *testing.T) {
	m1 := map[string]any{"name": "a"}
	m2 := map[string]any{"name": "b"}
	for _, c := range []struct {
		name     string
		val      Value
		wantRows int
		wantCols []string
		ok       bool
	}{
		{"table 直接列", Value{Type: TypeTable, Rows: []map[string]any{m1, m2}, Cols: []string{"name"}}, 2, []string{"name"}, true},
		{"table 的空表列不出来", Value{Type: TypeTable, Rows: []map[string]any{}}, 0, nil, false},
		{"map 不是记录(一个对象该给 text)", Value{Type: TypeMap, Map: m1, Keys: []string{"name"}}, 0, nil, false},
		{"array 不是记录", Value{Type: TypeArray, Points: []Point{{V: 1}, {V: 2}}}, 0, nil, false},
		{"number 不是记录", Value{Type: TypeNumber, Num: 7}, 0, nil, false},
		{"text 不是记录", Value{Type: TypeText, Text: "hello"}, 0, nil, false},
	} {
		rows, cols, ok := ToRecords(c.val)
		if ok != c.ok {
			t.Errorf("%s: ToRecords ok = %v, 期望 %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if len(rows) != c.wantRows || !reflect.DeepEqual(cols, c.wantCols) {
			t.Errorf("%s: ToRecords = %d 行 %v, 期望 %d 行 %v", c.name, len(rows), cols, c.wantRows, c.wantCols)
		}
	}
}
