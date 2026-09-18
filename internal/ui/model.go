package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// Provider is ui's abstraction over data sources, so tests can inject a stub.
type Provider interface {
	// Snapshot returns the latest read-only view of one source.
	Snapshot(name string) (source.SourceView, bool)
	// Snapshots returns one read-only view of every source (a template may reference any
	// source by name, so the full set is taken rather than narrowing to the layout).
	Snapshots() map[string]source.SourceView
	// HasData reports whether any source has produced a first frame yet (used by the
	// placeholder page).
	HasData() bool
	// Errors returns the errors currently present across all sources, shown in the footer.
	Errors() []string
}

// tickMsg is the periodic re-render signal.
type tickMsg time.Time

// model is the Bubble Tea root Model: it takes snapshots on a timer and renders the
// layout. Data is refreshed continuously by the goroutine behind provider, and a tick
// does not fetch data. The whole-page layout result is cached on this object, so plain
// scrolling/paging only slices a window out of the cache.
type model struct {
	provider Provider
	layout   []config.Row
	poll     time.Duration

	width, height int

	scroll   int  // current scrolled line offset
	maxOff   int  // scrollable limit from the last layout (0 = no overflow, scroll keys inert)
	pageRows int  // lines per page = lines in the visible scrolling area
	help     bool // whether the keyboard-shortcut help overlay is shown (toggled by ?)

	paused    bool          // whether UI refresh is paused (toggled by space)
	savedPoll time.Duration // original poll interval saved when paused

	// Whole-page content cache: plain scrolling/paging only slices a window; only a tick, a
	// window-size change or a change in the error count sets dirty and recomposes.
	have    bool   // whether the cache is valid (empty content is legitimate, so emptiness is no test)
	dirty   bool   // set by a tick / window-size change, recomposes the whole page next frame
	content string // whole page at the display width; on overflow, width-1 yields the scrollbar column
	rowCap  int    // scrolling-area line count when cached (rows changed → automatically invalid)

	reload ReloadFunc // rebuilds the dashboard from the config file (r); nil = r is inert
	notice string     // why the last config reload failed ("": none)
}

// ReloadFunc rebuilds the dashboard from its source of truth, the config file: a fresh provider,
// layout and re-render interval, or an error. An error leaves the running dashboard untouched and
// is only reported, so a config that does not parse cannot take the dashboard down with it.
//
// ui stays ignorant of the config file; main supplies the closure. The model closes the provider
// it replaces (see reloadConfig), so the closure only builds the new one.
type ReloadFunc func() (Provider, []config.Row, time.Duration, error)

// New creates the Bubble Tea program. provider supplies each source's snapshot; layout
// defines the dashboard structure; poll is the UI re-render interval, and <=0 falls back
// to config.DefaultPollInterval. reload rebuilds all three from the config file on the reload
// key; nil makes that key inert.
func New(p Provider, layout []config.Row, poll time.Duration, reload ReloadFunc) *tea.Program {
	if poll <= 0 {
		poll = config.DefaultPollInterval
	}
	m := &model{provider: p, layout: layout, poll: poll, reload: reload}
	return tea.NewProgram(m)
}

// Init returns the initial command: wait for the first tick (data is fetched by the
// source manager on its own per-interval schedule).
func (m *model) Init() tea.Cmd {
	return m.nextTick()
}

// nextTick schedules the next re-render.
func (m *model) nextTick() tea.Cmd {
	return tea.Tick(m.poll, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update handles: a tick (re-render only), window-size changes, scroll keys / wheel, and
// the quit keys.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.dirty = true // data may have changed, recompose next frame
		return m, m.nextTick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.dirty = true
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseWheelMsg:
		return m.handleWheel(msg)
	}
	return m, nil
}

// handleKey handles the help toggle, the config reload, scrolling and quit keys. Scrolling only
// takes effect when the content overflows (maxOff>0): ↑/k and ↓/j move 1 line, PgUp/PgDn page,
// Ctrl+B/F page up/down, Ctrl+D/U half a page up/down, Home/End jump to top/bottom.
// ? toggles the help overlay (while it is open, scroll keys do not pass through); q or
// Ctrl+C quits; r reloads the config file (see reloadConfig).
func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// A Ctrl combination is reported differently under legacy (the letter in Code) and
	// kitty (the letter in Text), so both are normalized to a lowercase letter first.
	c := ctrlLetterOf(msg)
	if msg.String() == "q" || msg.String() == "ctrl+c" || c == 'c' {
		return m, tea.Quit
	}
	// In a real terminal '?' is composed from shift+/: it may be reported as Code '?' or
	// Text "?", so both are accepted
	helpKey := msg.String() == "?" || msg.String() == "shift+/" ||
		msg.Code == '?' || msg.Text == "?"
	if m.help {
		if helpKey || msg.String() == "esc" {
			m.help = false
		}
		return m, nil
	}
	if helpKey {
		m.help = true
		return m, nil
	}
	// vim paging: Ctrl+B/F page up/down, Ctrl+D/U half a page up/down
	if c != 0 {
		half := m.pageRows / 2
		if half < 1 {
			half = 1
		}
		switch c {
		case 'd':
			m.scroll = clampScroll(m.scroll+half, m.maxOff)
		case 'u':
			m.scroll = clampScroll(m.scroll-half, m.maxOff)
		case 'f':
			m.scroll = clampScroll(m.scroll+m.pageRows, m.maxOff)
		case 'b':
			m.scroll = clampScroll(m.scroll-m.pageRows, m.maxOff)
		}
		return m, nil
	}
	switch msg.String() {
	case "r":
		// Below the help return and the Ctrl block: a typed r lands here under either terminal
		// protocol (the rule j/k/q rely on), while Ctrl+R stays inert.
		m.reloadConfig()
	case "space":
		m.paused = !m.paused
		if m.paused {
			m.savedPoll = m.poll
			m.poll = 24 * time.Hour // effectively stop updates
			return m, nil
		}
		m.poll = m.savedPoll // restore original interval
		return m, m.nextTick() // reschedule tick immediately
	case "up", "k":
		m.scroll = clampScroll(m.scroll-1, m.maxOff)
	case "down", "j":
		m.scroll = clampScroll(m.scroll+1, m.maxOff)
	case "pgup":
		m.scroll = clampScroll(m.scroll-m.pageRows, m.maxOff)
	case "pgdown":
		m.scroll = clampScroll(m.scroll+m.pageRows, m.maxOff)
	case "home":
		m.scroll = 0
	case "end":
		m.scroll = m.maxOff
	}
	return m, nil
}

// reloadConfig rebuilds the dashboard from the config file. Success swaps provider, layout and
// re-render interval and recomposes the page; failure leaves everything as it is and reports why.
//
// Only a failure says anything: a reload that works shows itself in the new layout and colors, and
// the empty footer clears a previous failure.
//
// The replaced provider is closed here, since the model owns whichever one it holds (see
// ReloadFunc); Close is idempotent for a source.Manager.
func (m *model) reloadConfig() {
	if m.reload == nil {
		return // no reload callback
	}
	p, layout, poll, err := m.reload()
	if err != nil {
		m.notice = "reload failed: " + err.Error()
		m.dirty = true // the footer gained a line: the scroll area shrinks, so recompose
		return
	}
	if c, ok := m.provider.(interface{ Close() }); ok {
		c.Close() // reap the old sources' goroutines
	}
	m.provider, m.layout = p, layout
	if poll > 0 {
		if m.paused {
			m.savedPoll = poll // save for when we unpause
		} else {
			m.poll = poll // nextTick re-reads it, so this applies from the next tick
		}
	}
	m.scroll = 0 // the layout may be entirely different
	m.notice = ""
	m.dirty = true
}

// ctrlLetterOf returns the letter pressed in a Ctrl combination (lowercase); 0 for a
// non-Ctrl combination or a non-letter. A legacy terminal decodes Ctrl+D as Code='d' +
// ModCtrl, while the kitty protocol may put the character in Text with Code set to
// 0/KeyExtended, so both are checked.
func ctrlLetterOf(msg tea.KeyPressMsg) rune {
	if msg.Mod&tea.ModCtrl == 0 {
		return 0
	}
	c := msg.Code
	if c == 0 || c == tea.KeyExtended {
		if msg.Text != "" {
			r, _ := utf8.DecodeRuneInString(msg.Text)
			c = r
		} else if msg.BaseCode != 0 {
			c = msg.BaseCode
		}
	}
	lower := unicode.ToLower(c)
	if lower >= 'a' && lower <= 'z' {
		return lower
	}
	return 0
}

// handleWheel handles the mouse wheel: 3 lines per notch. While the help overlay is open
// it does not scroll the content underneath.
func (m *model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if m.help {
		return m, nil
	}
	switch msg.Mouse().Button {
	case tea.MouseWheelUp:
		m.scroll = clampScroll(m.scroll-3, m.maxOff)
	case tea.MouseWheelDown:
		m.scroll = clampScroll(m.scroll+3, m.maxOff)
	}
	return m, nil
}

// View renders the whole screen per the config. When the content overflows the whole page
// scrolls vertically; the footer banners (source errors, at most 3, plus a failed reload) are
// pinned to the bottom and do not enter the scrolling area. The whole-page layout is cached on
// model and scrolling only clips a window, see recompose.
func (m *model) View() tea.View {
	// Every source snapshot is taken once at the top of View, shared by widget layout and
	// all templates.
	views := m.provider.Snapshots()

	if !m.provider.HasData() {
		// A failed reload is reported on this page too: it is what is on screen while the sources
		// are still silent. Source errors keep their existing behaviour of not appearing here.
		noticeRows := 0
		if m.notice != "" {
			noticeRows = 1
		}
		if m.paused {
			noticeRows++
		}
		var sb strings.Builder
		if m.help {
			sb.WriteString(helpOverlay(m.width, m.bodyRows(noticeRows)))
		} else {
			sb.WriteString(Faint.Render(" ⏳ loading data sources…"))
		}
		if m.notice != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(errorBanner(m.notice, m.width))
		}
		if m.paused {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(pausedBanner("⏸ refresh paused (space to resume)", m.width))
		}
		return m.fullView(sb.String())
	}

	banners := m.footerLines()
	rows := m.bodyRows(len(banners)) // scroll area lines (room already left for the bottom banners)

	// Data (tick) / window size / a change in the error count, or no cache yet → the whole
	// page recomposes; plain scroll input reuses the cache.
	if !m.have || m.dirty || m.rowCap != rows {
		m.recompose(views, rows)
	}

	// Clip a window of exactly rows lines out of the cache. If it fits, the missing lines
	// are padded with blanks so the error banners sit flush at the bottom; if it overflows,
	// each line gets the rightmost scrollbar column appended (the thumb rows are filled with
	// a color block) and scroll is clamped.
	var window string
	if m.help {
		window = helpOverlay(m.width, rows)
	} else {
		switch {
		case rows <= 0: // unknown height (auto-size/test): send the whole string, no scrolling
			window = m.content
		case m.maxOff == 0:
			if m.content != "" {
				window = strings.Join(padLines(strings.Split(m.content, "\n"), rows), "\n")
			}
		default:
			window, m.scroll = clipScroll(m.content, m.scroll, rows, m.width)
		}
	}

	var sb strings.Builder
	if window != "" {
		sb.WriteString(window)
	}
	for i, b := range banners {
		if window != "" || i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(errorBanner(b, m.width))
	}
	if m.paused {
		if window != "" || len(banners) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(pausedBanner("⏸ refresh paused (space to resume)", m.width))
	}
	return m.fullView(sb.String())
}

// footerLines is the footer's display order: every source error (capped at 3, as before), then why
// the last config reload failed. Appending after the cap keeps 3 errors from pushing the reload
// line off.
//
// Built fresh rather than appended onto provider.Errors(), whose spare capacity would be written
// into.
func (m *model) footerLines() []string {
	errs := m.provider.Errors()
	if len(errs) > 3 {
		errs = errs[:3]
	}
	out := make([]string, 0, len(errs)+1)
	out = append(out, errs...)
	if m.notice != "" {
		out = append(out, m.notice)
	}
	return out
}

// recompose lays the whole page out into the content cache and computes the scroll
// geometry maxOff/pageRows. One version is laid out at the full width first to measure the
// height: if it fits, that version is cached as is; if it overflows, the rightmost column
// is given up to draw the scrollbar and the cache is recomposed at width-1, so the panels'
// right border is not overwritten.
func (m *model) recompose(views map[string]source.SourceView, rows int) {
	m.have, m.dirty = true, false
	m.rowCap = rows

	// Both versions reuse the same frame (now is taken only once), keeping .date/.time
	// consistent across a second boundary.
	now := time.Now()
	frame := tplData(now, views)

	content := composeView(m.layout, views, m.width, rows, frame)
	max := scrollMax(contentLines(content), rows)

	switch {
	case rows <= 0: // unknown height: no scrolling
		m.maxOff, m.pageRows = 0, 0
	case max == 0: // it fits
		m.maxOff, m.pageRows = 0, rows
	default: // overflows
		if m.width > 2 {
			content = composeView(m.layout, views, m.width-1, rows, frame)
		}
		m.maxOff, m.pageRows = max, rows
	}
	m.content = content
	if m.scroll > m.maxOff {
		m.scroll = m.maxOff
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// fullView builds the full-screen view. The mouse mode is set to CellMotion, without
// which wheel events never reach Update.
func (m *model) fullView(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// The shared interpolation data for all templates is in tpl_data.go: tplData carries the
// built-in .date/.weekday/.time and exposes each source by name, shared by row titles and
// widget templates.

// bodyRows is the scrolling content area's line count = total height - number of bottom
// footer banners. When the height is unknown (≤0) or the banners do not fit it returns 0,
// meaning no scrolling and the content is sent as one block.
func (m *model) bodyRows(errCount int) int {
	if m.height <= 0 {
		return 0
	}
	if n := m.height - errCount; n > 0 {
		return n
	}
	return 0
}
