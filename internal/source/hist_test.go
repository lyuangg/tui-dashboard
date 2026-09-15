package source

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"tui-dashboard/internal/config"
)

// newState builds a sourceState without starting a goroutine, for testing store and accumulate in
// isolation.
func newState(mut ...func(*sourceState)) *sourceState {
	st := &sourceState{name: "n", hist: map[string][]Point{}, cfg: config.Source{HistoryCap: 10}}
	for _, f := range mut {
		f(st)
	}
	return st
}

// vals extracts the value of each point in a history series.
func vals(pts []Point) []float64 {
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = p.V
	}
	return out
}

// TestStoreAccumulateNumber a number source records one point per frame, with the timestamp taken
// from the fetch frame.
func TestStoreAccumulateNumber(t *testing.T) {
	st := newState()
	t0 := time.Now()
	st.store(Parsed{Val: Value{Type: TypeNumber, Num: 40}, Has: true}, t0, nil)
	st.store(Parsed{Val: Value{Type: TypeNumber, Num: 42}, Has: true}, t0.Add(time.Second), nil)

	hist := st.hist[histValue]
	if got := vals(hist); !reflect.DeepEqual(got, []float64{40, 42}) {
		t.Fatalf("历史 = %v, 期望 [40 42]", got)
	}
	if !hist[0].TS.Equal(t0) || !hist[1].TS.Equal(t0.Add(time.Second)) {
		t.Errorf("时间戳该是取数帧时间: %v", hist)
	}
}

// TestStoreAccumulateMapFields each numeric field of a map source gets its own series; non-numeric
// fields get none.
func TestStoreAccumulateMapFields(t *testing.T) {
	st := newState()
	t0 := time.Now()
	frame := func(v float64, tag string) Parsed {
		return Parsed{Val: Value{
			Type: TypeMap, Keys: []string{"cpu", "tag"},
			Map: map[string]any{"cpu": v, "tag": tag},
		}, Has: true}
	}
	st.store(frame(9, "a"), t0, nil)
	st.store(frame(11, "b"), t0.Add(time.Second), nil)

	if got := vals(st.hist["cpu"]); !reflect.DeepEqual(got, []float64{9, 11}) {
		t.Errorf("cpu 历史 = %v, 期望 [9 11]", got)
	}
	if _, ok := st.hist["tag"]; ok {
		t.Errorf("字符串字段不该建历史序列: %v", st.hist["tag"])
	}
}

// TestStoreAccumulateTimeseries a timeseries source's timestamps come from the script and are
// merged by time and deduped: emitting new points every frame and emitting a rolling window every
// frame both end as the same timeline.
func TestStoreAccumulateTimeseries(t *testing.T) {
	st := newState()
	t0 := time.Now().Truncate(time.Second)
	at := func(sec int, v float64) Point { return Point{TS: t0.Add(time.Duration(sec) * time.Second), V: v} }
	store := func(pts ...Point) {
		st.store(Parsed{Val: Value{Type: TypeTimeseries, Points: pts}, Has: true}, t0, nil)
	}
	store(at(0, 1), at(1, 2))
	store(at(1, 2), at(2, 3)) // overlaps the previous frame by one point

	hist := st.hist[histValue]
	if got := vals(hist); !reflect.DeepEqual(got, []float64{1, 2, 3}) {
		t.Fatalf("重叠点应去重、成一条时间线, got %v", got)
	}
	if !hist[0].TS.Before(hist[1].TS) || !hist[1].TS.Before(hist[2].TS) {
		t.Errorf("历史应按时间排好序: %v", hist)
	}
}

// TestStoreNoHistoryForFramelessTypes array / table / text / logs describe a frame's shape rather
// than a timeline and do not enter Hist.
func TestStoreNoHistoryForFramelessTypes(t *testing.T) {
	t0 := time.Now()
	for _, v := range []Value{
		{Type: TypeArray, Points: []Point{{V: 1}, {V: 2}, {V: 3}}},
		{Type: TypeTable, Rows: []map[string]any{{"a": 1}}, Cols: []string{"a"}},
		{Type: TypeText, Text: "hello"},
		{Type: TypeLogs, Logs: []LogLine{{Msg: "x"}}},
	} {
		st := newState()
		st.store(Parsed{Val: v, Has: true}, t0, nil)
		if len(st.hist) != 0 {
			t.Errorf("%v 不该进历史序列: %v", v.Type, st.hist)
		}
	}
}

// TestStoreHistoryCap the history is trimmed to HistoryCap, keeping only the most recent points.
func TestStoreHistoryCap(t *testing.T) {
	st := newState(func(st *sourceState) { st.cfg.HistoryCap = 3 })
	t0 := time.Now()
	for _, f := range []float64{1, 2, 3, 4, 5} {
		st.store(Parsed{Val: Value{Type: TypeNumber, Num: f}, Has: true}, t0, nil)
	}
	if got := vals(st.hist[histValue]); !reflect.DeepEqual(got, []float64{3, 4, 5}) {
		t.Errorf("应截到最近 3 个: %v", got)
	}
}

// TestStoreValueAndHistSwapTogether the value, the template document and the history must be
// swapped together in one frame, never only half of them.
func TestStoreValueAndHistSwapTogether(t *testing.T) {
	st := newState()
	t0 := time.Now()

	// the first frame comes from a table source
	st.store(Parsed{Val: Value{Type: TypeTable, Rows: []map[string]any{{"a": 1}}, Cols: []string{"a"}}, Has: true}, t0, nil)
	if st.val.Type != TypeTable || !reflect.DeepEqual(st.val.Cols, []string{"a"}) {
		t.Fatalf("首帧应落盘: %+v", st.val)
	}
	if _, ok := st.doc["rows"]; !ok {
		t.Errorf("table 源的模板面只暴露 rows: %v", st.doc)
	}

	// after the switch to map, all three update together
	st.store(Parsed{Val: Value{Type: TypeMap, Map: map[string]any{"cpu": 1}, Keys: []string{"cpu"}}, Has: true}, t0, nil)
	if st.val.Type != TypeMap || st.doc["cpu"] != 1 {
		t.Errorf("换形状时值与其模板文档应一起换: val=%+v doc=%v", st.val, st.doc)
	}

	// a failed frame leaves the value, doc and history untouched and only records the error
	before := st.val
	st.store(Parsed{}, t0, errors.New("boom"))
	if !reflect.DeepEqual(st.val, before) || st.doc["cpu"] != 1 {
		t.Errorf("失败帧应沿用旧值: val=%+v doc=%v", st.val, st.doc)
	}
	if st.lastErr == nil {
		t.Error("失败帧应记下错误")
	}
	if got := vals(st.hist["cpu"]); !reflect.DeepEqual(got, []float64{1}) {
		t.Errorf("失败帧不该动历史: %v", got)
	}

	// a mismatched frame (Text non-empty, Has false) keeps the value and the shape but updates
	// the raw text
	st.store(Parsed{Text: "一段话"}, t0, nil)
	if !reflect.DeepEqual(st.val, before) {
		t.Errorf("没对上时应沿用旧值: %+v", st.val)
	}
	if st.text != "一段话" {
		t.Errorf("原文该换成这一帧的: %q", st.text)
	}
}
