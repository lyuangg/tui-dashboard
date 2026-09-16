package ui

import "strings"

// Keyboard-shortcut help overlay: ? covers the scrolling content area with a centered
// panel, and ? / Esc dismisses it. No popup library is pulled in: the text is emitted
// directly as rows lines at full width (the alt screen redraws the whole frame, so the
// overlay is "replacing that area's content"); the panel width auto-sizes to the longest
// line, it is centered horizontally and vertically, and lines are padded at the end so the
// old content underneath the overlay is covered.

// helpRows is the help body lines (plain text, not yet laid out).
func helpRows() []string {
	return []string{
		"↑ / k    ↓ / j      scroll 1 line",
		"PgUp / PgDn          page up / down",
		"Ctrl+B               page up (same as PgUp)",
		"Ctrl+F               page down (same as PgDn)",
		"Ctrl+D               half page down",
		"Ctrl+U               half page up",
		"Home / End           jump to top / bottom",
		"mouse wheel          scroll (3 lines)",
		"?                     show / hide this help",
		"r                     reload the config file",
		"q / Ctrl+C           quit",
	}
}

// helpOverlay builds the help overlay of exactly rows lines (full-width text). It returns
// an empty string when rows<=0 or width<=0.
func helpOverlay(width, rows int) string {
	if rows <= 0 || width <= 0 {
		return ""
	}
	texts := helpRows()

	maxW := 0
	for _, ln := range texts {
		if c := cellWidth(ln); c > maxW {
			maxW = c
		}
	}
	boxW := maxW + 4 // border 2 + padding 2
	if boxW > width {
		boxW = width
	}

	box := Panel("text", "Keyboard help", strings.Join(texts, "\n"), boxW, 0)
	boxLines := strings.Split(box, "\n")

	out := make([]string, 0, rows)
	top := (rows - len(boxLines)) / 2
	if top < 0 {
		top = 0
	}
	blank := func() string { return strings.Repeat(" ", width) }
	for i := 0; i < top; i++ {
		out = append(out, blank())
	}
	left := (width - boxW) / 2
	if left < 0 {
		left = 0
	}
	for _, ln := range boxLines {
		row := strings.Repeat(" ", left) + ln
		if d := width - cellWidth(row); d > 0 {
			row += strings.Repeat(" ", d)
		}
		out = append(out, row)
	}
	for len(out) < rows {
		out = append(out, blank())
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return strings.Join(out, "\n")
}
