package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
)

// minWide is the width below which the layout degrades to full-screen vertical stacking:
// each widget occupies a row of its own.
const minWide = 82

// rowTitle renders a row title, doing template interpolation. The template only knows
// the built-in frame fields (.date/.weekday/.time), not data sources and widget fields;
// to show live data, put it into one of that row's widget bodies.
func rowTitle(row config.Row, base map[string]any) string {
	return RenderTpl(row.Title, base)
}

// RowTitleSourceRefs checks whether a row/section title references a top-level field
// other than the built-in .date/.weekday/.time, and returns startup-log hints (empty =
// all valid); hint only, never blocks startup. The criterion is any non-built-in
// top-level field, not just a source name: every such reference resolves to nothing —
// a single-level field ({{.host}}) renders empty, and a multi-level field
// ({{.svc.value}}) makes template execution fail, so the whole title falls back verbatim.
func RowTitleSourceRefs(cfg *config.Config) []string {
	isSource := make(map[string]bool, len(cfg.Sources))
	for _, s := range cfg.Sources {
		isSource[s.Name] = true
	}
	var out []string
	for ri, row := range cfg.Layout {
		seen := map[string]bool{}
		for _, f := range tplFields(row.Title) {
			if seen[f] || templateReserved[f] {
				continue // the three built-in keys, legitimate in a row title; seen dedupes
			}
			seen[f] = true
			if isSource[f] {
				out = append(out, fmt.Sprintf(
					"layout[%d] row title references data source %q — row titles only see the built-in "+
						".date/.weekday/.time and cannot resolve it (it renders empty, or the whole title "+
						"falls back verbatim); to show it, put it in one of that row's widgets "+
						"(panel titles and bodies can both use it)",
					ri, f))
				continue
			}
			out = append(out, fmt.Sprintf(
				"layout[%d] row title references %q — not a built-in field (row titles cannot see data "+
					"sources, nor the per-widget fields of the widgets in that row), so it cannot be "+
					"resolved (it renders empty, or the whole title falls back verbatim)",
				ri, f))
		}
	}
	return out
}

// sectionTitle lays the row-title text out per align as 1 text line. left/empty
// alignment goes through sectionStyle().Render(title) with no padding; center/right pads
// the left by visible width. A title wider than width is returned verbatim, leaving
// truncation to the outer layer.
func sectionTitle(title string, align string, width int) string {
	line := sectionStyle().Render(title)
	if width <= 0 || (align != "center" && align != "right") {
		return line
	}
	space := width - visibleWidth(title)
	if space <= 0 {
		return line
	}
	switch align {
	case "right":
		return strings.Repeat(" ", space) + line
	default: // center
		return strings.Repeat(" ", space/2) + line
	}
}

// rowSection is the row-title entry point: it picks the render path per whether the row
// configured color/bg.
//   - neither set → plain text, no background;
//   - either set → the text color may change; a bg fills the whole row as a color band.
//
// Color values follow the same scheme as a widget color: preset name (follows the theme)
// / color index / #hex; an unknown name falls back to the default.
func rowSection(row config.Row, title, align string, width int) string {
	if row.Color == "" && row.BG == "" {
		return sectionTitle(title, align, width)
	}
	fg := lipgloss.Color(cur.Section)
	if c, ok := resolveSpec(row.Color); ok {
		fg = c
	}
	var bg color.Color
	if c, ok := resolveSpec(row.BG); ok {
		bg = c
	}
	return sectionBand(title, align, width, fg, bg)
}

// sectionBand is the color-band version of a row title: the text is uniformly Bold+fg;
// with a bg set, the whole row (including the whitespace left over by alignment) is
// filled to width. An over-wide title is truncated first, so the band cannot overflow
// onto the next line. With a nil bg only the text color changes, and the spacing rules
// are those of sectionTitle.
func sectionBand(title string, align string, width int, fg, bg color.Color) string {
	st := lipgloss.NewStyle().Bold(true).Foreground(fg)
	if bg != nil {
		st = st.Background(bg)
	}
	if width <= 0 {
		return st.Render(title)
	}
	if visibleWidth(title) > width {
		title = truncateVisible(title, width)
	}
	space := width - visibleWidth(title)
	if space < 0 {
		space = 0
	}
	left := 0
	switch align {
	case "right":
		left = space
	case "center":
		left = space / 2
	}
	line := strings.Repeat(" ", left) + title
	if bg != nil {
		if d := width - cellWidth(line); d > 0 {
			line += strings.Repeat(" ", d) // pad the background out to the full row width
		}
	}
	return st.Render(line)
}

// composeView renders the whole layout into a multi-line string. width/height are the
// content area (the full screen); frame is the template data frame shared by this
// layout pass (see tplData), from which row titles take only the built-in fields (see
// titleData); with a nil frame each widget gets only its own source doc (the direct-call
// test scenario).
func composeView(layout []config.Row, views map[string]source.SourceView, width, height int, frame map[string]any) string {
	if width < minWide {
		return composeNarrow(layout, views, width, frame)
	}
	return composeWide(layout, views, width, height, frame)
}

// composeWide is normal width: each row's widgets are laid out horizontally.
// When height is to spare, the bottom-most un-fixed-height logs row absorbs the
// remaining height; fixed-height rows and panels that wrote max_lines take no part in the
// absorption, rendering at their configured height and max_lines respectively (see
// widgetHeight).
func composeWide(layout []config.Row, views map[string]source.SourceView, width, height int, frame map[string]any) string {
	// row titles see only the built-in fields; extracted once this frame for the row
	// titles and sectionCount
	base := titleData(frame)

	// locate the expandable logs row: bottom-up, skipping explicitly fixed-height rows
	expand := -1
	for i := len(layout) - 1; i >= 0; i-- {
		if layout[i].Height == 0 && rowHasLogs(layout[i].Widgets) {
			expand = i
			break
		}
	}

	// measure each row's natural height (a row with only a title and no widget takes no
	// height: it is a standalone title band)
	rows := make([]int, len(layout))
	used := 0
	for i, row := range layout {
		if len(row.Widgets) == 0 {
			rows[i] = 0
			continue
		}
		if row.Height > 0 {
			rows[i] = row.Height
			used += row.Height
			continue
		}
		rows[i] = measureRow(row.Widgets, views, width, frame)
		used += rows[i]
	}
	if expand >= 0 {
		// a row title takes 1 line each but no panel height budget; subtract them before
		// absorbing the remaining height, or every titled row would take 1 extra line and
		// push content off the screen.
		leftover := height - used - sectionCount(layout, base)
		if leftover > 0 {
			rows[expand] += leftover
		}
	}

	var bodies []string
	for i, row := range layout {
		if t := rowTitle(row, base); t != "" {
			bodies = append(bodies, rowSection(row, t, row.TitleAlign, width))
		}
		if len(row.Widgets) == 0 {
			continue // a title-only row produces no empty panel line
		}
		// The row height lands on the panels. Only the expand row (natural height +
		// leftover) and explicitly fixed-height rows (configured value) pass rows[i] down;
		// for the other auto-sized rows rows[i] is merely the measured natural height, so 0
		// is passed to let the panel compute its own.
		if rows[i] > 0 && (i == expand || row.Height > 0) {
			bodies = append(bodies, renderRow(row.Widgets, views, width, rows[i], frame))
		} else {
			bodies = append(bodies, renderRow(row.Widgets, views, width, 0, frame))
		}
	}
	return strings.Join(bodies, "\n")
}

// composeNarrow is the narrow window: each widget fills a row vertically, top to bottom.
func composeNarrow(layout []config.Row, views map[string]source.SourceView, width int, frame map[string]any) string {
	base := titleData(frame) // row titles see only built-in fields (same as composeWide)
	var bodies []string
	for _, row := range layout {
		if t := rowTitle(row, base); t != "" {
			bodies = append(bodies, rowSection(row, t, row.TitleAlign, width))
		}
		for _, w := range row.Widgets {
			bodies = append(bodies, renderWidget(w, withFrame(frame, views[w.Source]), width, 0))
		}
	}
	return strings.Join(bodies, "\n")
}

// renderRow renders one row of widgets.
// At ch>0 the row is forced to that height (the logs row absorbing the remaining height);
// chart/bar carry their own fixed height and ignore ch.
func renderRow(widgets []config.Widget, views map[string]source.SourceView, width, ch int, frame map[string]any) string {
	widths := rowWidths(widgets, width)
	cells := make([]string, len(widgets))
	for i, w := range widgets {
		h := 0
		if ch > 0 && w.Type != "chart" && w.Type != "bar" {
			h = widgetHeight(w, ch)
		}
		// withFrame: merges the shared data frame into the widget's own source doc, so its
		// template can reference other sources.
		cells[i] = renderWidget(w, withFrame(frame, views[w.Source]), widths[i], h)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
}

// widgetHeight computes the number of panel lines (borders included) a widget gets this
// frame. ch is the line count allotted to this row; the bottom-most logs row is stretched
// by the layout to absorb the remaining height, so ch is often very large.
//
// max_lines bounds the total box height: a panel that wrote max_lines: 6 should be 6
// lines of content + top and bottom borders = 8 lines. It only caps, never grows: when ch
// is below max_lines+2, ch is used as is, and ch<=0 returns 0, handing auto-sizing back to
// the panel. chart/bar carry their own fixed height and never reach here.
func widgetHeight(w config.Widget, ch int) int {
	if w.MaxLines > 0 && (w.Type == "logs" || w.Type == "text") {
		if cap := w.MaxLines + 2; ch > cap { // +2: 1 line each for the top and bottom borders
			return cap
		}
	}
	return ch
}

// measureRow renders one row at auto height and returns the text lines it occupies.
func measureRow(widgets []config.Widget, views map[string]source.SourceView, width int, frame map[string]any) int {
	return strings.Count(renderRow(widgets, views, width, 0, frame), "\n") + 1
}

// rowWidths is the border-inclusive width of each widget in a row: w.Width>0 fixes the
// width, the rest split the remainder elastically.
func rowWidths(widgets []config.Widget, total int) []int {
	n := len(widgets)
	out := make([]int, n)
	fixedSum, fixedN := 0, 0
	for i, w := range widgets {
		if w.Width > 0 {
			out[i] = w.Width
			fixedSum += w.Width
			fixedN++
		}
	}
	elastic := n - fixedN
	if elastic <= 0 {
		if fixedSum <= total {
			return out
		}
		// fixed widths exceed the total: compress proportionally, with a minimum column
		// width as last-resort fallback
		for i := range out {
			out[i] = max(out[i]*total/fixedSum, 12)
		}
		return out
	}
	each := (total - fixedSum) / elastic
	if each < 12 {
		each = 12
	}
	// spread the integer-division remainder over the first columns, so the row total fills
	// exactly total
	remainder := total - fixedSum - each*elastic
	seenElastic := 0
	for i, w := range widgets {
		if w.Width <= 0 {
			out[i] = each
			if seenElastic < remainder {
				out[i]++
			}
			seenElastic++
		}
	}
	return out
}

func rowHasLogs(widgets []config.Widget) bool {
	for _, w := range widgets {
		if w.Type == "logs" {
			return true
		}
	}
	return false
}

// sectionCount is the number of rows in the layout that carry a row title (composeWide
// renders 1 extra line for each row title). base is the data a row title can see (see
// titleData).
func sectionCount(layout []config.Row, base map[string]any) int {
	n := 0
	for _, row := range layout {
		if rowTitle(row, base) != "" {
			n++
		}
	}
	return n
}
