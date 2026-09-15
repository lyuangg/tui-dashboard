package ui

// Theme converges the whole dashboard's color scheme into one preset that can be swapped
// as a unit; with no theme written, default is used. Color values are all lipgloss spec
// strings (a 256-color index "63" or "#ff79c6"), built into an image/color.Color via
// lipgloss.Color(spec). The active theme lives in the package-level cur, which every
// render function reads per frame (style.go / widgets.go), so UseTheme only has to be
// called once before Program.Run; the theme name is validated on the main side.
type Theme struct {
	Name string // preset name: default|dracula|gruvbox|nord|light

	Section         string // row/section title section bar (the default text color on a color band)
	Good, Warn, Bad string // log level / status UP..DOWN / gauge thresholds / footer error banners
	Axis, Guide     string // chart axes / bar reference line + scrollbar (dim grey)

	// Widget holds the default panel accent color of the 7 widget types (key = type); chart
	// and logs usually share a color, while stat/bar/gauge/table/text are independent.
	Widget map[string]string
	// Named is widget color by preset name: a writable name → spec. Names follow the theme:
	// the same set of keys gets, in each theme, values adapted to that theme's own
	// background (a light theme takes darker color indices); a name a theme does not define
	// falls back to the default table.
	Named map[string]string
}

// canonicalWidget is the seven widget types; a theme must supply them all.
var canonicalWidget = []string{"stat", "chart", "bar", "gauge", "table", "logs", "text"}

// canonicalNamed is the fixed set of named-color names; every theme supplies all of them.
var canonicalNamed = []string{
	"red", "orange", "amber", "yellow", "green", "teal", "cyan",
	"blue", "violet", "magenta", "pink", "grey",
}

// theme is a convenience constructor for Theme.
func theme(name, section, good, warn, bad, axis, guide string,
	widget, named map[string]string) Theme {
	return Theme{
		Name:    name,
		Section: section,
		Good:    good, Warn: warn, Bad: bad,
		Axis: axis, Guide: guide,
		Widget: widget, Named: named,
	}
}

// DefaultTheme is the default theme: these are the values used when no theme is selected.
func DefaultTheme() Theme {
	return theme("default", "212", "10", "214", "196", "242", "240",
		map[string]string{
			"stat": "63", "chart": "33", "bar": "75", "gauge": "36",
			"table": "208", "logs": "33", "text": "240",
		},
		defaultNamed(),
	)
}

// defaultNamed is the default named-color table: name → spec (safe color indices on
// common terminals).
func defaultNamed() map[string]string {
	return map[string]string{
		"red": "196", "orange": "208", "amber": "214", "yellow": "226",
		"green": "40", "teal": "37", "cyan": "45", "blue": "33",
		"violet": "141", "magenta": "205", "pink": "213", "grey": "245",
	}
}

// DraculaTheme is a dark-background, high-saturation theme.
func DraculaTheme() Theme {
	return theme("dracula", "#bd93f9",
		"#50fa7b", "#ffb86c", "#ff5555", "#6272a4", "#44475a",
		map[string]string{
			"stat": "#ff79c6", "chart": "#8be9fd", "bar": "#bd93f9",
			"gauge": "#50fa7b", "table": "#ffb86c", "logs": "#8be9fd",
			"text": "#6272a4",
		},
		map[string]string{
			"red": "#ff5555", "orange": "#ffb86c", "amber": "#f1fa8c", "yellow": "#f1fa8c",
			"green": "#50fa7b", "teal": "#50fa7b", "cyan": "#8be9fd", "blue": "#8be9fd",
			"violet": "#bd93f9", "magenta": "#ff79c6", "pink": "#ff79c6", "grey": "#6272a4",
		},
	)
}

// GruvboxTheme is a dark, warm-background theme.
func GruvboxTheme() Theme {
	return theme("gruvbox", "#fe8019",
		"#b8bb26", "#fabd2f", "#fb4934", "#928374", "#7c6f64",
		map[string]string{
			"stat": "#fe8019", "chart": "#83a598", "bar": "#b8bb26",
			"gauge": "#8ec07c", "table": "#d65d0e", "logs": "#83a598",
			"text": "#928374",
		},
		map[string]string{
			"red": "#fb4934", "orange": "#fe8019", "amber": "#fabd2f", "yellow": "#fabd2f",
			"green": "#b8bb26", "teal": "#8ec07c", "cyan": "#8ec07c", "blue": "#83a598",
			"violet": "#d3869b", "magenta": "#d3869b", "pink": "#d3869b", "grey": "#928374",
		},
	)
}

// NordTheme is a low-saturation blue-grey theme.
func NordTheme() Theme {
	return theme("nord", "#88c0d0",
		"#a3be8c", "#ebcb8b", "#bf616a", "#616e88", "#4c566a",
		map[string]string{
			"stat": "#88c0d0", "chart": "#81a1c1", "bar": "#b48ead",
			"gauge": "#a3be8c", "table": "#d08770", "logs": "#81a1c1",
			"text": "#616e88",
		},
		map[string]string{
			"red": "#bf616a", "orange": "#d08770", "amber": "#ebcb8b", "yellow": "#ebcb8b",
			"green": "#a3be8c", "teal": "#8fbcbb", "cyan": "#8fbcbb", "blue": "#81a1c1",
			"violet": "#b48ead", "magenta": "#b48ead", "pink": "#b48ead", "grey": "#616e88",
		},
	)
}

// LightTheme is a light-background theme: the colors are dark indices, so they stay
// readable on white.
func LightTheme() Theme {
	return theme("light", "#6d28d9",
		"#15803d", "#b45309", "#b91c1c", "#94a3b8", "#cbd5e1",
		map[string]string{
			"stat": "#be185d", "chart": "#1d4ed8", "bar": "#4338ca",
			"gauge": "#047857", "table": "#ea580c", "logs": "#1d4ed8",
			"text": "#64748b",
		},
		map[string]string{
			"red": "#dc2626", "orange": "#ea580c", "amber": "#d97706", "yellow": "#ca8a04",
			"green": "#16a34a", "teal": "#0d9488", "cyan": "#0891b2", "blue": "#2563eb",
			"violet": "#7c3aed", "magenta": "#c026d3", "pink": "#db2777", "grey": "#64748b",
		},
	)
}

// The currently active theme; default until UseTheme is called.
var cur = DefaultTheme()

// defaultTheme keeps a resident default, to which a named color falls back when a theme
// does not define it.
var defaultTheme = DefaultTheme()

// UseTheme makes a preset theme the current theme. Both an empty string and "default" mean
// default; an unknown theme name returns false and leaves things as they are (cur is
// untouched).
func UseTheme(name string) bool {
	var t Theme
	switch name {
	case "", "default":
		t = DefaultTheme()
	case "dracula":
		t = DraculaTheme()
	case "gruvbox":
		t = GruvboxTheme()
	case "nord":
		t = NordTheme()
	case "light":
		t = LightTheme()
	default:
		return false
	}
	cur = t
	return true
}

// ThemeNames lists the available theme names (for error hints / documentation).
func ThemeNames() []string {
	return []string{"default", "dracula", "gruvbox", "nord", "light"}
}
