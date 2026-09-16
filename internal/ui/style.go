package ui

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// In lipgloss v2, Color is a function, color.Color(s string), not a type; color values
// in this package use image/color.Color uniformly, built via lipgloss.Color(spec).

// Base styles (no color, independent of the theme).
var (
	Faint = lipgloss.NewStyle().Faint(true)
	Bold  = lipgloss.NewStyle().Bold(true)
)

// —— theme funnel: the builders below take colors from the active theme cur (theme.go) ——

// sectionStyle is the style of the row-title section bar ("═══ x ═══").
func sectionStyle() lipgloss.Style {
	return Bold.Foreground(lipgloss.Color(cur.Section))
}

// widgetColor is the default accent color for a panel of the given widget type
// (looks up cur.Widget by widgetType).
func widgetColor(widgetType string) color.Color {
	return lipgloss.Color(cur.Widget[widgetType])
}

// accentOf is the panel border/title color: a color the widget wrote explicitly
// (override) wins, otherwise the per-type default applies.
// override takes three forms:
//   - a preset name (follows the theme, see theme.go) → looked up in the current theme,
//     falling back to the default table, then that spec is used;
//   - a numeric color index / "#hex" → passed to lipgloss verbatim;
//   - any other unknown name → the override is ignored, falling back to the type default.
func accentOf(widgetType, override string) color.Color {
	if c, ok := resolveSpec(override); ok {
		return c
	}
	return widgetColor(widgetType)
}

// resolveSpec resolves one optional color value into a usable color:
//   - a preset name → the current theme's name table, falling back to the default
//     theme's table, then that spec is used;
//   - a numeric color index / "#hex" → verbatim;
//   - an empty string or any other unknown name → (nil, false), which the caller must
//     ignore and fall back to the default (a row title's custom color follows the same
//     rule as a widget color, see accentOf).
func resolveSpec(spec string) (color.Color, bool) {
	if spec == "" {
		return nil, false
	}
	if s, ok := namedColor(spec); ok {
		return lipgloss.Color(s), true
	}
	if isRawSpec(spec) {
		return lipgloss.Color(spec), true
	}
	return nil, false
}

// namedColor looks up the spec for a preset name: the current theme's table wins,
// falling back to the default theme's name table.
func namedColor(name string) (spec string, ok bool) {
	if s, ok := cur.Named[name]; ok {
		return s, true
	}
	s, ok := defaultTheme.Named[name]
	return s, ok
}

// isRawSpec reports whether s is a raw form lipgloss parses directly ("#hex" or a plain
// numeric color index).
func isRawSpec(s string) bool {
	if strings.HasPrefix(s, "#") {
		return true
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

// LevelStyle colors a log level: ERROR uses Bad, WARN uses Warn, everything else Good.
func LevelStyle(level string) lipgloss.Style {
	switch level {
	case "ERROR":
		return Bold.Foreground(lipgloss.Color(cur.Bad))
	case "WARN":
		return Bold.Foreground(lipgloss.Color(cur.Warn))
	default:
		return Bold.Foreground(lipgloss.Color(cur.Good))
	}
}

// StatusStyle colors a service status (UP / DEGRADED / DOWN).
func StatusStyle(st string) lipgloss.Style {
	switch st {
	case "DOWN":
		return Bold.Foreground(lipgloss.Color(cur.Bad))
	case "DEGRADED":
		return Bold.Foreground(lipgloss.Color(cur.Warn))
	default:
		return Bold.Foreground(lipgloss.Color(cur.Good))
	}
}

// gaugeColor shifts a progress bar's color with its ratio: >=0.85 Bad, >=0.6 Warn,
// Good otherwise.
func gaugeColor(ratio float64) lipgloss.Style {
	switch {
	case ratio >= 0.85:
		return Bold.Foreground(lipgloss.Color(cur.Bad))
	case ratio >= 0.6:
		return Bold.Foreground(lipgloss.Color(cur.Warn))
	default:
		return Bold.Foreground(lipgloss.Color(cur.Good))
	}
}

// scrollThumbStyle is the scrollbar thumb style (takes the theme's Guide grey).
func scrollThumbStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(cur.Guide))
}

// errorBanner is the footer error banner style (takes the theme's Bad) and truncates it
// to the screen width. It carries a source's failure and a config reload that did not parse
// alike: either way the line states something went wrong.
//
// Truncation is required: the banner is pinned to the very bottom of the screen and does
// not enter the scrolling area (see model.View), while msg comes from a source's stderr
// and has no upper bound on length; the excess would be soft-wrapped by the terminal onto
// the next line and push other lines off. At width<=0 (unknown size) nothing is
// truncated, and the whole banner is sent.
func errorBanner(msg string, width int) string {
	if width > 0 {
		msg = fitLines(msg, width-2) // -2: the two leading spaces below
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(cur.Bad)).
		Bold(true).
		Render("  " + msg)
}

// Panel draws a panel with a rounded border; a non-empty title is drawn on the top
// border (gotop style), and an empty one leaves a bare border. width/height are the
// overall size including the border; height==0 means the content auto-sizes.
// The border color is the default color scheme for widgetType.
// lipgloss v2's .Width(w) makes the rendered total width exactly w (border and padding
// included), so width is passed straight through here; visible text area = width-4.
func Panel(widgetType, title, body string, width, height int) string {
	return panelColored(accentOf(widgetType, ""), title, body, width, height)
}

// panelColored is the same as Panel, but the border color is given by the caller (used
// for a widget's color override).
func panelColored(accent color.Color, title, body string, width, height int) string {
	st := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1)
	if width > 0 {
		st = st.Width(width)
	}

	content := body
	if width > 0 {
		content = fitLines(content, width-4)
	}

	if height > 0 {
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		// reserve 2 lines for the top and bottom borders
		for len(lines) < height-2 {
			lines = append(lines, "")
		}
		content = strings.Join(lines, "\n")
	}
	box := st.Render(content)
	if title != "" {
		box = borderTitle(box, title, accent)
	}
	return box
}

// borderTitle overlays the title onto the center of the border box's top edge.
// Alignment is done by borderTitleAlign and defaults to center; a renderer that wants
// left/right alignment per widget.title_align calls borderTitleAlign rather than handling
// it here.
// The top edge is rebuilt segment by segment (corner + left divider + bold title + right
// divider + corner) rather than by lipgloss glyph-by-glyph ANSI splicing; an over-wide
// title is truncated first, guaranteeing the top edge stays exactly as wide as the box.
func borderTitle(box, title string, accent color.Color) string {
	return borderTitleAlign(box, title, accent, "center")
}

// borderTitleAlign is the same as borderTitle, but the title is laid out within the top
// edge per align: left = title flush left (1 cell of divider after the corner), right =
// flush right, center = centered.
func borderTitleAlign(box, title string, accent color.Color, align string) string {
	lines := strings.Split(box, "\n")
	if len(lines) == 0 {
		return box
	}
	bw := cellWidth(stripANSI(lines[0]))

	// 1 column of divider on each side, so the title's maximum visible width is
	// box width - 4 (2 corners + 2 spaces)
	maxT := bw - 4
	if maxT < 0 {
		maxT = 0
	}
	// Measured with the visible width (ANSI stripped): truncation appends a \x1b[0m, and
	// a plain cellWidth would count those 3 escape bytes as column width, leaving fill
	// negative and panicking on a negative strings.Repeat count.
	if visibleWidth(title) > maxT {
		title = truncateVisible(title, maxT)
	}

	tw := visibleWidth(title)
	fill := bw - 2 - tw
	left, right := fill/2, fill-fill/2
	switch align {
	case "left":
		left, right = 1, fill-1 // title flush against the left corner, 1 cell of divider
	case "right":
		left, right = fill-1, 1
	}
	if left < 0 {
		left = 0
	}
	if right < 0 {
		right = 0
	}

	edge := lipgloss.NewStyle().Foreground(accent)
	capTitle := lipgloss.NewStyle().Bold(true).Foreground(accent)
	lines[0] = edge.Render("╭"+strings.Repeat("─", left)) +
		capTitle.Render(title) +
		edge.Render(strings.Repeat("─", right)+"╮")
	return strings.Join(lines, "\n")
}

// —— weekday names (used by the built-in template field .weekday, see tpl_data.go) ——

var enWeekday = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// weekdayName is the English name of a weekday (used by the built-in template field
// .weekday, see tpl_data.go).
func weekdayName(wd time.Weekday) string {
	return enWeekday[wd]
}
