package ui

import (
	"strings"
)

// Whole-page vertical scrolling (keyboard + wheel): when the content overflows, a window
// of exactly the visible line count is clipped and a thumb is drawn in the rightmost
// column. The scrollbar is written by hand rather than using bubbles/viewport (its
// "whole string + one scrollbar" model does not match a layout of "a panel grid filling
// the full width"). Geometry:
//
//	rows visible lines, total content lines → max offset max = total - rows (≤0 = no scrolling).
//	thumb height thumb = max(1, rows*rows/total), window top top = off*(rows-thumb)/max,
//	the thumb occupies [top, top+thumb).
//
// clipScroll contract: when the content overflows the caller has already recomposed at
// contentWidth=width-1 (the rightmost column yielded to the scrollbar); the function pads
// each line to contentWidth, then appends 1 column of slot, filled with a color block on
// thumb lines.

// scrollMax is the maximum line offset the content can scroll down by: it only exists when
// the content overflows (total>rows), otherwise 0 = no scrolling.
func scrollMax(total, rows int) int {
	if rows <= 0 {
		return 0
	}
	if m := total - rows; m > 0 {
		return m
	}
	return 0
}

// clampScroll clamps the line offset to [0,max].
func clampScroll(off, max int) int {
	if off < 0 {
		return 0
	}
	if off > max {
		return max
	}
	return off
}

// contentLines is the number of text lines a string occupies (empty string = 0,
// equivalent to splitting and counting).
func contentLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// padLines pads lines to exactly n lines: short input gets blank lines, long input is
// truncated.
func padLines(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
}

// scrollThumbStyle colors the scrollbar thumb, taking the current theme's Guide grey (see
// style.go).

// clipScroll clips a scrolling window of exactly rows lines, returning the window and the
// clamped offset off.
// Contract: body is already laid out at contentWidth=width-1 (the rightmost column yielded
// to the scrollbar).
// When the content does not overflow (total≤rows) it is returned as is with off=0; when it
// does, each line is padded to contentWidth and 1 column of slot is appended, filled with a
// color block on thumb lines and left blank on the others.
func clipScroll(body string, offset, rows, width int) (view string, off int) {
	if rows <= 0 {
		return body, 0
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	total := len(lines)
	max := scrollMax(total, rows)
	if max == 0 {
		return body, 0
	}

	contentWidth := width - 1
	if contentWidth < 1 {
		contentWidth = 1
	}
	off = clampScroll(offset, max)

	thumb := rows * rows / total
	if thumb < 1 {
		thumb = 1
	}
	top := 0
	if max > 0 {
		top = (rows - thumb) * off / max
	}

	out := make([]string, rows)
	for i := range out {
		var ln string
		if idx := off + i; idx < total {
			ln = lines[idx]
		}
		if cellWidth(ln) > contentWidth {
			ln = truncateVisible(ln, contentWidth)
		} else if d := contentWidth - cellWidth(ln); d > 0 {
			ln += strings.Repeat(" ", d)
		}
		if i >= top && i < top+thumb {
			out[i] = ln + scrollThumbStyle().Render("█")
		} else {
			out[i] = ln + " "
		}
	}
	return strings.Join(out, "\n"), off
}
