package ui

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"
	"time"
)

// RenderTpl interpolates Go templates in scalar string fields (a widget's
// title / unit / label / static text / format / column headers; row titles row.title), filling
// them from doc at render time: a doc key can be written directly as .field (such as
// .cpu). doc comes from the unified template data (see tpl_data.go) and has two layers:
//
//	widget     built-in .date/.weekday/.time + each source by name + own source fields flattened
//	row title  built-in .date/.weekday/.time only (data sources are widget-only, see titleData)
//
// raw is returned verbatim when it does not contain "{{"; a parse or execution failure
// always falls back to the original string, so the border layout is never broken.
func RenderTpl(raw string, doc map[string]any) string {
	if !strings.Contains(raw, "{{") {
		return raw
	}
	t := cachedTpl(raw)
	if t == nil {
		return raw
	}
	var b bytes.Buffer
	if err := t.Execute(&b, doc); err != nil {
		return raw
	}
	s := b.String()
	// Under missingkey=zero a missing key renders as "<no value>"/"<nil>" (the zero value
	// of an interface{} map value is nil), and a widget title is rendered even before the
	// source's first frame, so these literals would leak into the bordered title; they are
	// therefore cleared unconditionally. For a default-value fallback use the def function.
	if strings.Contains(s, "<no value>") || strings.Contains(s, "<nil>") {
		s = strings.ReplaceAll(s, "<no value>", "")
		s = strings.ReplaceAll(s, "<nil>", "")
	}
	return s
}

// tplFields extracts the top-level field names a template's text references: both {{.foo}}
// and {{.foo.bar}} record "foo".
//
// It walks the text/template syntax tree instead of guessing from the string: only real
// field nodes count, so a ".com" or "%.1f" appearing in a literal is not mistaken for a
// field, and function names (printf/def/now) are identifier nodes and do not count. Fields
// inside a variable ($x) or a chained lookup ((index .m "k").f) are not top-level keys;
// the walk only descends to find real fields.
//
// It must go through cachedTpl rather than calling template.New(...).Parse directly:
// parsing has to carry tplFuncs, otherwise every title using now/def/printf fails to parse,
// and those are exactly the titles most often checked, so the check would silently stop
// working. cachedTpl also reuses the cache used for rendering and returns nil on a syntax
// error (the same fallback decision RenderTpl makes).
//
// The result is deduped and sorted by name, so the startup hint is stable on every run.
func tplFields(raw string) []string {
	if !strings.Contains(raw, "{{") {
		return nil
	}
	t := cachedTpl(raw)
	if t == nil || t.Tree == nil {
		return nil
	}
	var (
		out  []string
		seen = map[string]bool{}
	)
	var walk func(parse.Node)
	walkBranch := func(b parse.BranchNode) {
		walk(b.Pipe)
		walk(b.List)
		walk(b.ElseList) // a nil pointer when there is no else; walk nil-checks internally
	}
	walk = func(n parse.Node) {
		switch node := n.(type) {
		case *parse.ListNode:
			if node == nil {
				return
			}
			for _, c := range node.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			if node != nil {
				walk(node.Pipe)
			}
		case *parse.PipeNode:
			if node == nil {
				return
			}
			for _, c := range node.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			if node == nil {
				return
			}
			for _, a := range node.Args {
				walk(a)
			}
		case *parse.FieldNode:
			if node == nil || len(node.Ident) == 0 || seen[node.Ident[0]] {
				return
			}
			seen[node.Ident[0]] = true
			out = append(out, node.Ident[0])
		case *parse.ChainNode:
			if node != nil {
				walk(node.Node) // fields on the chain are not top-level keys; find the chain root only
			}
		case *parse.TemplateNode:
			if node != nil {
				walk(node.Pipe)
			}
		case *parse.IfNode:
			if node != nil {
				walkBranch(node.BranchNode)
			}
		case *parse.RangeNode:
			if node != nil {
				walkBranch(node.BranchNode)
			}
		case *parse.WithNode:
			if node != nil {
				walkBranch(node.BranchNode)
			}
			// VariableNode($x) / IdentifierNode(now) / the literal node kinds: not top-level keys, skipped
		}
	}
	walk(t.Tree.Root)
	sort.Strings(out)
	return out
}

// tplCache caches parsed templates by their raw string; a template depends only on the raw
// text, and the data is fed in at execute time.
var tplCache sync.Map

func cachedTpl(raw string) *template.Template {
	if v, ok := tplCache.Load(raw); ok {
		return v.(*template.Template)
	}
	t, err := template.New("tpl").Funcs(tplFuncs).Option("missingkey=zero").Parse(raw)
	if err != nil {
		return nil // syntax error: the caller falls back to the raw string
	}
	tplCache.Store(raw, t)
	return t
}

// tplFuncs holds the template functions available to interpolation (names avoid Go
// keywords, e.g. def rather than default). now takes the current moment in a Go time layout
// (2006-01-02 15:04:05 and the like), often used in row / section titles.
var tplFuncs = template.FuncMap{
	"printf": fmt.Sprintf,
	"round":  func(f float64) float64 { return math.Round(f) },
	"env":    os.Getenv,
	"def":    def,
	"upper":  strings.ToUpper,
	"lower":  strings.ToLower,
	"now":    func(layout string) string { return time.Now().Format(layout) },
}

// collapseSpace folds any run of whitespace (newlines, multiple spaces included) into a
// single space, so a text source's multi-line output can be embedded into one line of a
// template (see sourceValue).
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// def returns the first non-zero argument (a default-value fallback inside templates). The
// number 0 counts as empty, so the default belongs at the end of the argument list — when
// all are empty the last argument is returned, as in `{{def .cpu 0}}`, which yields 0 both
// when .cpu is missing and when it is 0.
func def(vals ...any) any {
	for _, v := range vals {
		if !isZero(v) {
			return v
		}
	}
	if len(vals) > 0 {
		return vals[len(vals)-1]
	}
	return ""
}

func isZero(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case float64:
		return t == 0
	case int:
		return t == 0
	case bool:
		return !t
	}
	return false
}
