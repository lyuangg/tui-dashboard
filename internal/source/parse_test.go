package source

import (
	"strings"
	"testing"
	"time"
)

// opts is the default parse config of the test cases: the type is text (the zero value), and
// another type is passed in when one is needed.
func opts(t Type) Opts { return Opts{Type: t, Header: true} }

// —— parse-layer behavior common to all types ——
//
// Per-type parsing is in textfmt_test.go; what is covered here is type-independent: the raw text
// always kept, mismatch handling, empty output and BOM, and the storage form of numbers.

// TestParseOutputTypeNumber one number for the whole input: integers, decimals, exponent form and
// surrounding whitespace all parse as float64.
func TestParseOutputTypeNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"42.5", 42.5},
		{"123", 123},
		{"  -3.1\n", -3.1},
		{".5", 0.5},
		{"1e3", 1000},
		{"42.5\n\n", 42.5},
	}
	for _, c := range cases {
		p, err := ParseOutput([]byte(c.in), time.Now(), opts(TypeNumber))
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if !p.Has || p.Val.Type != TypeNumber || p.Val.Num != c.want {
			t.Errorf("%q → %+v, 期望 %v", c.in, p.Val, c.want)
		}
	}
}

// TestParseOutputTypeNumberRejects multiple lines of numbers are not number but array: number
// means one number for the whole input.
func TestParseOutputTypeNumberRejects(t *testing.T) {
	p, err := ParseOutput([]byte("1\n2\n"), time.Now(), opts(TypeNumber))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}
	if p.Has {
		t.Errorf("两行数字不该是 number: %+v", p.Val)
	}
	if len(p.Logs) == 0 || p.Logs[0].Level != "WARN" {
		t.Errorf("没对上应记一条 WARN: %v", p.Logs)
	}
}

// TestParseOutputTypeText text is not parsed: bodies such as numbers, kv, JSON and log lines are
// all taken as Text unchanged.
func TestParseOutputTypeText(t *testing.T) {
	// declaring text means no parsing: bodies such as numbers, kv and JSON are all taken as the
	// body unchanged
	for _, in := range []string{
		"hello world\nsecond line",
		"15:32:24 INFO  started consumer",
		"{broken json",
		`{"a": 1}`,
		"42.5",       // a pure number is not parsed as number either
		"load1: 1.2", // a kv line is not parsed as map either
	} {
		p, err := ParseOutput([]byte(in), time.Now(), opts(TypeText))
		if err != nil {
			t.Fatalf("%q 不应报错: %v", in, err)
		}
		if !p.Has || p.Val.Type != TypeText || p.Val.Text != in {
			t.Errorf("%q 应原样归为 text, got %+v", in, p.Val)
		}
		if p.Text != in {
			t.Errorf("%q 的原文应是它自己, got %q", in, p.Text)
		}
	}
}

// TestParseOutputBOMAndEmpty the BOM is stripped; empty output means nothing new this frame and is
// not an error.
func TestParseOutputBOMAndEmpty(t *testing.T) {
	p, err := ParseOutput([]byte("\xef\xbb\xbfa: 1\n\n"), time.Now(), opts(TypeMap))
	if err != nil || !p.Has || p.Val.Map["a"] == nil {
		t.Fatalf("BOM 处理失败: err=%v val=%+v", err, p.Val)
	}
	p, err = ParseOutput([]byte("  \n\t"), time.Now(), opts(TypeMap))
	if err != nil {
		t.Fatalf("空输出不应报错: %v", err)
	}
	if p.Has || p.Text != "" || len(p.Logs) != 0 {
		t.Errorf("空输出应为空 Parsed, got %+v", p)
	}
}

// TestParseOutputNumberIsFloat integer values are stored as float64, not int64. That document
// serves both the value-resolution layer and templates, and a template's {{printf "%.0f" .x}}
// accepts only float64; int64 would print %!f(int64=83).
func TestParseOutputNumberIsFloat(t *testing.T) {
	p, err := ParseOutput([]byte("123"), time.Now(), opts(TypeNumber))
	if err != nil {
		t.Fatal(err)
	}
	got := p.Val.templateDoc()["value"]
	if _, ok := got.(float64); !ok {
		t.Errorf("整数值也应存 float64, got %T(%v)", got, got)
	}
}

// TestParseOutputMismatchKeepsTextAndWarns on a mismatch no value is produced, the raw text is
// kept, and a WARN is appended to the log buffer.
func TestParseOutputMismatchKeepsTextAndWarns(t *testing.T) {
	ts := time.Date(2026, 9, 8, 15, 30, 0, 0, time.Local)
	p, err := ParseOutput([]byte("hello world"), ts, opts(TypeNumber))
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if p.Has {
		t.Errorf("应没对上: %+v", p.Val)
	}
	if p.Text != "hello world" {
		t.Errorf("原文应保留: %q", p.Text)
	}
	if len(p.Logs) != 1 {
		t.Fatalf("应有且仅有一条 WARN: %v", p.Logs)
	}
	l := p.Logs[0]
	if l.Level != "WARN" || hhmmss(l.Time) != "15:30:00" || !strings.Contains(l.Msg, "type: number") {
		t.Errorf("WARN 内容不对: %+v", l)
	}
}
