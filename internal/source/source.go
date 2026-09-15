package source

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"tui-dashboard/internal/config"
)

// histValue is the series key of single-value sources in Hist: both number and
// timeseries use it, and it is also the lookup path of the template view
// {{.value}}. Each numeric field of a map source uses its field name as the key.
const histValue = "value"

// SourceView is a read-only view of one fetch for the UI (shallow copy, safe for concurrent use).
type SourceView struct {
	TS      time.Time          // time of the last successful fetch; zero means no data yet
	Type    Type               // type declared by the source (type: in config), same value as V.Type; usable before the first frame too, and the capability table gate reads it
	V       Value              // current value; V.Has() == false means no data yet (or this frame did not match and the previous value is kept)
	Hist    map[string][]Point // rolling window accumulated across frames: one "value" series for number and timeseries, one per numeric field for map (chart data)
	Logs    []LogLine          // rolling log buffer of this source: data lines of type: logs and framework diagnostic WARNs
	Text    string             // raw script output, present for every type; the text widget's last-resort fallback
	LastErr error              // error of the last run; when non-nil V holds the previous successful value
	Doc     map[string]any     // template view only: V shaped so a field can be looked up by name (see Value.templateDoc); the value-resolution layer does not use it and always goes through V
}

const maxOutput = 1 << 20 // upper bound on script stdout: 1MiB

// sourceState is the state of a single source.
type sourceState struct {
	name string
	cfg  config.Source
	opts Opts // parse config, fixed once at construction time from cfg (type/header)

	mu      sync.RWMutex
	ts      time.Time
	val     Value
	doc     map[string]any // val.templateDoc(), updated together with val
	hist    map[string][]Point
	logs    []LogLine
	text    string
	lastErr error
}

// Manager manages all sources: one goroutine per source fetches in a loop.
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	mu     sync.RWMutex
	st     map[string]*sourceState
	dir    string // working directory shared by every cmd; empty means inherit the process CWD; fixed once at construction time
}

// New starts an independent goroutine for each source. dir is the working directory of every cmd
// (usually the directory of the config file actually loaded, see main.scriptDir); an empty string
// means cmd.Dir is not set and the process CWD is inherited.
//
// dir is written before the goroutines start, so the cwd of a given source is constant across
// frames: re-resolving it every frame would let the cwd drift with whether the script is in place.
// The cost is that a script created or moved after the process starts is only picked up on restart.
//
// The validity of type: is checked by main before construction; unrecognized types fall back to
// TypeText here, i.e. are displayed as raw text.
func New(cfgs []config.Source, dir string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel, st: map[string]*sourceState{}, dir: dir}
	for _, c := range cfgs {
		typ, ok := ParseType(c.Type)
		if !ok {
			typ = TypeText
		}
		header := true
		if c.Header != nil {
			header = *c.Header
		}
		st := &sourceState{
			name: c.Name, cfg: c, hist: map[string][]Point{},
			opts: Opts{Type: typ, Header: header},
		}
		m.st[c.Name] = st
		m.wg.Add(1)
		go m.run(st)
	}
	return m
}

// run is the loop of a single source: fetch one frame immediately, then fetch periodically at
// interval. A failure does not break the loop, interval is the retry cadence; it exits when ctx is
// canceled.
func (m *Manager) run(st *sourceState) {
	defer m.wg.Done()
	iv := st.cfg.Interval.Duration
	ticker := time.NewTicker(iv)
	defer ticker.Stop()
	for {
		st.apply(m.ctx, m.dir)
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// apply runs the script once to fetch data and stores the result into the state.
func (st *sourceState) apply(ctx context.Context, dir string) {
	ts := time.Now()

	out, err := runOnce(ctx, st.cfg, dir)
	if err != nil {
		st.store(Parsed{}, ts, err)
		return
	}
	parsed, perr := ParseOutput(out, ts, st.opts)
	if perr != nil {
		st.store(Parsed{}, ts, perr)
		return
	}
	if !parsed.Has && parsed.Text == "" && len(parsed.Logs) == 0 {
		return // empty output: nothing new this frame, keep the previous data
	}
	st.store(parsed, ts, nil)
}

// store saves one fetch result. On failure or mismatch it always keeps the previous value and only
// records the raw text and a WARN.
func (st *sourceState) store(p Parsed, ts time.Time, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.ts = ts
	st.lastErr = err
	if err != nil {
		st.logs = appendLog(st.logs, []LogLine{{
			Time: ts, Level: "WARN",
			Msg: fmt.Sprintf("%s: fetch failed: %v", st.name, err),
		}}, st.cfg.LogCap)
		return
	}
	if p.Text != "" {
		st.text = p.Text
	}
	// Framework diagnostics and the script log lines of a logs source flow into the same rolling
	// buffer and share one timeline.
	if len(p.Logs) > 0 {
		st.logs = appendLog(st.logs, p.Logs, st.cfg.LogCap)
	}
	if p.Has && p.Val.Type == TypeLogs {
		st.logs = appendLog(st.logs, p.Val.Logs, st.cfg.LogCap)
	}
	if !p.Has {
		return // mismatch: keep the previous successful value
	}

	// The value and its views are updated together: Doc is computed from Val on the spot and hist is
	// appended, so the three are never half stale.
	st.val = p.Val
	st.doc = p.Val.templateDoc()
	st.accumulate(p.Val, ts)
}

// accumulate merges this frame's value into the rolling window Hist. Types that enter the window:
//
//	number      one point per frame, timestamp generated by the program
//	map         one series per numeric field, timestamp generated by the program
//	timeseries  the script carries its own timestamps; merged by time and deduped
//	others      text/array/table/logs do not enter Hist: they express what this
//	            frame looks like, not a timeline
func (st *sourceState) accumulate(v Value, ts time.Time) {
	cap := st.cfg.HistoryCap
	switch v.Type {
	case TypeNumber:
		st.hist[histValue] = appendPoint(st.hist[histValue], Point{TS: ts, V: v.Num}, cap)

	case TypeTimeseries:
		st.hist[histValue] = mergePoints(st.hist[histValue], v.Points, cap)

	case TypeMap:
		for k, raw := range v.Map {
			if f, ok := toFloat(raw); ok {
				st.hist[k] = appendPoint(st.hist[k], Point{TS: ts, V: f}, cap)
			}
		}
	}
}

// runOnce runs the script through sh -c, carrying a timeout, process-group termination, stderr
// capture and an output cap.
func runOnce(parent context.Context, cfg config.Source, dir string) ([]byte, error) {
	timeout := cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = cfg.Interval.Duration
		if timeout > 5*time.Second {
			timeout = 5 * time.Second
		}
	}
	rctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(rctx, "sh", "-c", cfg.Cmd)
	if dir != "" {
		cmd.Dir = dir // directory holding the config file (see New); empty = unset, inherit the process CWD
	}
	// Separate process group: the whole group is terminated on timeout or exit, so a child process
	// started by the script never becomes an orphan
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	var out cappedBuffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return nil, fmt.Errorf("%v (stderr: %s)", err, msg)
		}
		return nil, err
	}
	if out.overflow {
		return nil, fmt.Errorf("script output exceeds %d bytes (truncated)", maxOutput)
	}
	return out.b.Bytes(), nil
}

// cappedBuffer is a stdout buffer with a capture limit: the excess is dropped silently instead of
// sending SIGPIPE to the child, recorded by the overflow flag and reported as an error once the run
// has finished.
type cappedBuffer struct {
	b        bytes.Buffer
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := maxOutput - c.b.Len()
	if room <= 0 {
		c.overflow = true
		return len(p), nil
	}
	if len(p) > room {
		c.b.Write(p[:room])
		c.overflow = true
		return len(p), nil
	}
	return c.b.Write(p)
}

var _ io.Writer = (*cappedBuffer)(nil)

// Snapshot returns a read-only view of one source. ok=false when name does not exist.
func (m *Manager) Snapshot(name string) (SourceView, bool) {
	m.mu.RLock()
	st, ok := m.st[name]
	m.mu.RUnlock()
	if !ok {
		return SourceView{}, false
	}

	st.mu.RLock()
	defer st.mu.RUnlock()
	return SourceView{
		TS:      st.ts,
		Type:    st.opts.Type,
		V:       st.val,
		Doc:     st.doc,
		Text:    st.text,
		LastErr: st.lastErr,
		Hist:    copyHist(st.hist),
		Logs:    append([]LogLine(nil), st.logs...),
	}, true
}

// Snapshots returns one read-only snapshot of every source (name → SourceView). Template data
// takes the full view: a source may be referenced only by the title or body of another widget and
// never appear in the layout itself.
func (m *Manager) Snapshots() map[string]SourceView {
	m.mu.RLock()
	names := make([]string, 0, len(m.st))
	for n := range m.st {
		names = append(names, n)
	}
	m.mu.RUnlock()
	out := make(map[string]SourceView, len(names))
	for _, n := range names {
		if v, ok := m.Snapshot(n); ok {
			out[n] = v
		}
	}
	return out
}

// HasData reports whether any source has produced a first frame (used for the "initializing"
// placeholder in the UI).
func (m *Manager) HasData() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, st := range m.st {
		st.mu.RLock()
		ok := !st.ts.IsZero()
		st.mu.RUnlock()
		if ok {
			return true
		}
	}
	return false
}

// Close cancels every source goroutine and waits for it to finish; idempotent.
func (m *Manager) Close() {
	m.once.Do(func() {
		m.cancel()
		m.wg.Wait()
	})
}

// Errors returns the errors currently present in every source, for display in the UI footer.
//
// The result is sorted by source name: the iteration is over a map, whose order is random every
// round, and the footer is redrawn every frame and takes only the first 3 entries (see model.View);
// unsorted, the errors would jump position every frame and even which 3 are shown would be unstable.
func (m *Manager) Errors() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for name, st := range m.st {
		st.mu.RLock()
		err := st.lastErr
		st.mu.RUnlock()
		if err != nil {
			out = append(out, fmt.Sprintf("⚠ %s: %v", name, err))
		}
	}
	sort.Strings(out)
	return out
}

func appendLog(buf []LogLine, add []LogLine, cap int) []LogLine {
	buf = append(buf, add...)
	if cap > 0 && len(buf) > cap {
		buf = buf[len(buf)-cap:]
	}
	return buf
}

func appendPoint(seq []Point, p Point, cap int) []Point {
	seq = append(seq, p)
	return trimPoints(seq, cap)
}

// mergePoints merges freshly sampled points into an existing series: dedupe by timestamp (the
// later point overwrites the earlier one), sort, and trim the tail to the capacity.
//
// Overwriting by timestamp makes both kinds of timeseries script correct: one that emits only new
// points every frame and one that emits a rolling window (say the last 60 seconds) every frame both
// yield the same timeline after the append.
func mergePoints(old, add []Point, cap int) []Point {
	byTS := make(map[int64]Point, len(old)+len(add))
	for _, p := range old {
		byTS[p.TS.UnixNano()] = p
	}
	for _, p := range add {
		byTS[p.TS.UnixNano()] = p
	}
	seq := make([]Point, 0, len(byTS))
	for _, p := range byTS {
		seq = append(seq, p)
	}
	sort.Slice(seq, func(i, j int) bool { return seq[i].TS.Before(seq[j].TS) })
	return trimPoints(seq, cap)
}

func trimPoints(seq []Point, cap int) []Point {
	if cap > 0 && len(seq) > cap {
		seq = seq[len(seq)-cap:]
	}
	return seq
}

func copyHist(h map[string][]Point) map[string][]Point {
	out := make(map[string][]Point, len(h))
	for k, v := range h {
		out[k] = append([]Point(nil), v...)
	}
	return out
}
