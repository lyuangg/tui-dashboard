// Package config loads and validates the dashboard's YAML config file.
package config

import (
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration and also accepts "500ms" / "2s" / a bare number of seconds
// in YAML.
type Duration struct {
	time.Duration
}

// UnmarshalYAML lets yaml.v3 parse strings and numbers into Duration.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar, got %d", node.Kind)
	}
	if d0, err := time.ParseDuration(node.Value); err == nil {
		d.Duration = d0
		return nil
	}
	if sec, err := strconv.ParseFloat(node.Value, 64); err == nil {
		d.Duration = time.Duration(sec * float64(time.Second))
		return nil
	}
	return fmt.Errorf("bad duration %q", node.Value)
}

// Config is the whole dashboard's configuration. There is no global top-bar title: the title
// is written on one of the layout rows (see Row).
type Config struct {
	Theme        string   `yaml:"theme"`         // preset color scheme default|dracula|gruvbox|nord|light; empty = default; ui resolves the name
	PollInterval Duration `yaml:"poll_interval"` // UI redraw frequency, default 500ms
	Sources      []Source `yaml:"sources"`
	Layout       []Row    `yaml:"layout"`
}

// Source is one data source: a shell command run periodically at its own interval. A script
// produces one format only; for several, select the output with an argument, or split it
// into multiple sources.
type Source struct {
	Name       string   `yaml:"name"` // unique; referenced by widgets
	Interval   Duration `yaml:"interval"`
	Cmd        string   `yaml:"cmd"`         // arbitrary shell command, run periodically at interval (required)
	Type       string   `yaml:"type"`        // which kind of data it produces (required): text|number|array|timeseries|map|table|logs, each with one canonical format (see the type table in README / example.yaml)
	Header     *bool    `yaml:"header"`      // type: table only: whether the first row is a header (default true); false means every row is data, with columns named col_1..col_N
	Timeout    Duration `yaml:"timeout"`     // script timeout, default min(interval, 5s)
	HistoryCap int      `yaml:"history_cap"` // chart series length, default 60
	LogCap     int      `yaml:"log_cap"`     // log buffer capacity, default 200
}

// Row is one row of the layout: several widgets laid out horizontally.
type Row struct {
	Title      string   `yaml:"title"`
	TitleAlign string   `yaml:"title_align"` // row title alignment left|center|right; empty = left
	Color      string   `yaml:"color"`       // row title text color (preset name / color code / #hex, see "Themes and colors" in the example); empty = follows the theme's Section
	BG         string   `yaml:"bg"`          // row title background, filling the whole row; empty = no background. An unknown name is ignored and falls back to the default
	Height     int      `yaml:"height"`      // fixed-height row: every panel in the row is drawn at this height; 0 = auto-size to content
	Widgets    []Widget `yaml:"widgets"`
}

// Widget is one display control.
type Widget struct {
	Type       string `yaml:"type"`  // stat|chart|table|logs|gauge|bar|text
	Style      string `yaml:"style"` // chart/bar visual style: chart=braille|dots|line, bar=hbar|vbar|solid; empty takes the default
	Color      string `yaml:"color"` // optional: overrides this panel's border and top-edge title color (preset name / color code / #hex, same as Row's color); empty = takes the theme color scheme for the type
	Title      string `yaml:"title"`
	TitleAlign string `yaml:"title_align"` // panel title alignment on the top border, left|center|right, default center
	Source     string `yaml:"source"`      // references sources[].name
	Value      string `yaml:"value"`       // value expression (template): a single path such as '{{.used_pct}}' takes that field, a table takes an array field '{{.rows}}'; omitted, it takes the value for the source's declared type directly. A map source has many fields, so the field must be named

	Format string  `yaml:"format"` // numeric format (Sprintf). stat defaults to %.1f, and the whole string is rendered as a template when it contains {{; bar empty = integers without a decimal point, decimals keep 1 digit; chart uses it for y-axis ticks when there is no y_format
	Label  string  `yaml:"label"`  // stat subtitle
	Min    float64 `yaml:"min"`    // gauge/chart lower bound, default 0
	Max    float64 `yaml:"max"`    // gauge/chart upper bound, default 100 (gauge) / auto (chart)

	XFormat string   `yaml:"x_format"` // chart only: Go time layout for x-axis time labels (e.g. "15:04:05"); empty = the granularity is picked from the window span. Applies only to points that carry a time
	YFormat string   `yaml:"y_format"` // chart only: y-axis tick format (Sprintf); empty = reuse format, and integer if there is none
	Labels  []string `yaml:"labels"`   // category name per value, in order matching the values in the series: chart draws them below the x-axis, bar draws one at the start of each line (except vbar). Applies only to points without a time (such as an array source); where it does not apply, setting it has no effect and is deliberately not validated

	Columns  []Column `yaml:"columns"`   // table columns; the keys are taken automatically when omitted
	MaxLines int      `yaml:"max_lines"` // logs/text maximum number of displayed lines (from the tail); 0 = no limit, show as many as the panel height allows
	Text     string   `yaml:"text"`      // text only: pure static content, an alternative to taking data from source
	Wrap     bool     `yaml:"wrap"`      // text only: over-wide lines are wrapped automatically (default false = truncate with …)

	TimeFormat string `yaml:"time_format"` // logs only: Go time layout for the time column (e.g. "01-02 15:04:05" with a date); empty = "15:04:05". The column width is measured from the layout; other widgets ignore it (no error, same as labels)

	Width  int `yaml:"width"`  // 0 = shared elastically within the row
	Height int `yaml:"height"` // 0 = auto-size to content
}

// Column is one column of a table.
type Column struct {
	Key   string `yaml:"key"`
	Title string `yaml:"title"`
	Width int    `yaml:"width"` // 0 = by content
}

const (
	DefaultPollInterval = 500 * time.Millisecond
	DefaultHistoryCap   = 60
	DefaultLogCap       = 200
)

// Load reads and parses the config file, applies the defaults and then validates strictly.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	cfg, err := FromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return cfg, nil
}

// FromBytes parses the config from bytes, applies the defaults and then validates strictly.
func FromBytes(raw []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.PollInterval.Duration <= 0 {
		cfg.PollInterval = Duration{DefaultPollInterval}
	}
	for i := range cfg.Sources {
		s := &cfg.Sources[i]
		if s.HistoryCap <= 0 {
			s.HistoryCap = DefaultHistoryCap
		}
		if s.LogCap <= 0 {
			s.LogCap = DefaultLogCap
		}
		if s.Header == nil {
			yes := true // a table's first row is a header by default
			s.Header = &yes
		}
	}
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validAlign reports whether s is a legal alignment value (left|center|right); the empty
// string (the default) is also legal.
func validAlign(s string) bool {
	switch s {
	case "", "left", "center", "right":
		return true
	}
	return false
}

func (cfg *Config) validate() error {
	if len(cfg.Sources) == 0 {
		return fmt.Errorf("at least one source is required")
	}
	if len(cfg.Layout) == 0 {
		return fmt.Errorf("layout must not be empty")
	}

	// sources
	seen := map[string]bool{}
	for i := range cfg.Sources {
		s := &cfg.Sources[i]
		if s.Name == "" {
			return fmt.Errorf("source[%d]: name must not be empty", i)
		}
		if !idRe.MatchString(s.Name) {
			return fmt.Errorf("source[%s]: name may only contain letters, digits, underscore and hyphen", s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate source name: %s", s.Name)
		}
		seen[s.Name] = true
		if s.Cmd == "" {
			return fmt.Errorf("source[%s]: cmd is required (every data source is a command; see the bundled example's scripts/sys_*.sh for working ones)", s.Name)
		}
		if s.Interval.Duration <= 0 {
			return fmt.Errorf("source[%s]: interval must be greater than 0", s.Name)
		}
		if s.Timeout.Duration < 0 {
			return fmt.Errorf("source[%s]: timeout must not be negative", s.Name)
		}
		// type: is required. Only whether it was written is checked here; whether the value is
		// legal is checked by main: the table of legal values lives in the source package, and
		// source already imports this package, so the reverse import would be a cycle.
		if s.Type == "" {
			return fmt.Errorf("source[%s]: type is required (text|number|array|timeseries|map|table|logs). "+
				"Which kind of data the source produces is no longer guessed: it decides how the script "+
				"should be written and which widgets can consume it", s.Name)
		}
	}

	// widgets
	validTypes := map[string]bool{
		"stat": true, "chart": true, "table": true,
		"logs": true, "gauge": true, "bar": true, "heatmap": true,
		"text": true,
	}
	// value: is not validated here. Whether it may be omitted depends on the type: declared by
	// the source, and the semantics of a type belong to the source package; the reverse import
	// would be a cycle, so the whole check lives in main.
	for ri := range cfg.Layout {
		row := &cfg.Layout[ri]
		if !validAlign(row.TitleAlign) {
			return fmt.Errorf("layout[%d]: title_align only supports left|center|right, got %q", ri, row.TitleAlign)
		}
		for wi := range row.Widgets {
			w := &row.Widgets[wi]
			if !validTypes[w.Type] {
				return fmt.Errorf("layout[%d] widget[%d]: unknown type %q", ri, wi, w.Type)
			}
			if !validAlign(w.TitleAlign) {
				return fmt.Errorf("layout[%d] widget[%d]: title_align only supports left|center|right, got %q", ri, wi, w.TitleAlign)
			}
			// a purely static text needs no source; every other widget must reference a defined source
			if !seen[w.Source] && !(w.Type == "text" && w.Text != "") {
				return fmt.Errorf("layout[%d] widget[%d]: source %q is not defined", ri, wi, w.Source)
			}
		}
	}
	return nil
}

// —— config resolution: --config first, then the process CWD, then XDG/home, and the bundled example last ——

//go:embed example.yaml
var exampleYAML []byte

//go:embed example.zh-CN.yaml
var exampleZHCN []byte

// configDir is the tui-dashboard user config directory.
const configDir = "tui-dashboard"

// configFileName is the file name shared by every lookup tier; tier 2 uses it as a relative
// path, i.e. the copy under the process CWD.
const configFileName = "config.yaml"

// IsChineseLocale reports whether the environment asks for simplified Chinese, read from
// LC_ALL, then LC_MESSAGES, then LANG (empty = unset). It decides only which bundled example
// --init writes; Locate always falls back to the English one.
func IsChineseLocale() bool {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return isSimplifiedChinese(v)
		}
	}
	return false
}

// isSimplifiedChinese reports whether a POSIX locale value names simplified Chinese: zh_CN,
// zh_SG, zh_Hans* and a bare zh do; zh_TW, zh_HK, zh_MO and zh_Hant* do not, nor does any
// unrecognized zh_* region. The value is cut at the first "." (codeset) and "@" (modifier),
// "-" folds to "_", and the comparison ignores case.
func isSimplifiedChinese(locale string) bool {
	v := strings.ToLower(locale)
	if i := strings.IndexAny(v, ".@"); i >= 0 {
		v = v[:i]
	}
	lang, rest, _ := strings.Cut(strings.ReplaceAll(v, "-", "_"), "_")
	if lang != "zh" {
		return false
	}
	if rest == "" {
		return true // bare zh: the CLDR default for zh is simplified
	}
	return strings.HasPrefix(rest, "hans") || rest == "cn" || rest == "sg"
}

// ExampleFor returns the bundled example --init writes: the Chinese-commented variant (see
// IsChineseLocale) or the English one. The two differ only in comments.
func ExampleFor(zh bool) []byte {
	if zh {
		return exampleZHCN
	}
	return exampleYAML
}

// ResolvePath returns the config file path that would actually be used, with the same
// priority as Locate; the empty string when there is none.
// Priority:
//  1. explicit;
//  2. ./config.yaml under the process CWD — the relative path "config.yaml" is returned,
//     not an absolute path. A caller that needs the directory must call filepath.Abs first,
//     otherwise filepath.Dir yields "." (see main.scriptDir);
//  3. $XDG_CONFIG_HOME/tui-dashboard/config.yaml;
//  4. ~/.config/tui-dashboard/config.yaml;
//  5. none of the above → "" (the bundled example).
func ResolvePath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if fileExists(configFileName) { // a relative path = the copy under the process CWD
		return configFileName
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		if p := filepath.Join(xdg, configDir, configFileName); fileExists(p) {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p := filepath.Join(home, ".config", configDir, configFileName); fileExists(p) {
			return p
		}
	}
	return ""
}

// Locate decides which config is actually loaded and returns its content plus a source
// description (for the UI / logs to display).
// Priority:
//  1. explicit is non-empty: that file is read directly;
//  2. ./config.yaml under the process CWD;
//  3. $XDG_CONFIG_HOME/tui-dashboard/config.yaml (default ~/.config/tui-dashboard/config.yaml);
//  4. the embedded example.yaml; tiers that do not exist are skipped.
func Locate(explicit string) (data []byte, source string, err error) {
	p := ResolvePath(explicit)
	if p == "" {
		return exampleYAML, "bundled example", nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, "", fmt.Errorf("reading config %s: %w", p, err)
	}
	return raw, locateOrigin(explicit, p), nil
}

// locateOrigin builds the source description prefix for Locate ("explicit:"/"cwd:"/"xdg:"/"home:", with the path).
func locateOrigin(explicit, p string) string {
	if explicit != "" {
		return "explicit: " + p
	}
	if p == configFileName { // never a bare config.yaml, so it does not collide with the xdg branch below
		return "cwd: " + p
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" &&
		p == filepath.Join(xdg, configDir, configFileName) {
		return "xdg: " + p
	}
	return "home: " + p
}

// CopyDir copies the tree at srcDir in fsys to dstDir on disk, creating directories as
// needed. Existing targets are skipped rather than overwritten, and every file is given the
// executable bit.
func CopyDir(fsys fs.FS, srcDir, dstDir string) error {
	return fs.WalkDir(fsys, srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == srcDir {
			return os.MkdirAll(dstDir, 0o755)
		}
		rel := strings.TrimPrefix(p, srcDir+"/")
		dest := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if fileExists(dest) {
			return nil // already exists (the user may have edited it); skip without overwriting
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o755)
	})
}

// InitExampleWithScripts is the complete --init action: it writes the bundled example to the
// default config path (see WriteExampleToDefault) and copies the "scripts" tree in fsys to
// scripts/ beside it. Neither overwrites existing files; created reports only whether the
// config was new, while missing scripts are filled in. zh selects the Chinese-commented one.
func InitExampleWithScripts(scriptFS fs.FS, zh bool) (path string, created bool, err error) {
	p, created, err := WriteExampleToDefault(zh)
	if err != nil {
		return p, created, err
	}
	if err := CopyDir(scriptFS, "scripts", filepath.Join(filepath.Dir(p), "scripts")); err != nil {
		return p, created, fmt.Errorf("copying example scripts into the config dir: %w", err)
	}
	return p, created, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// defaultConfigPath returns the user's default config file path: XDG first, then the home
// .config. Unlike Locate, it only considers where a new file should go, not the process CWD.
func defaultConfigPath() (string, bool) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, configDir, configFileName), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config", configDir, configFileName), true
}

// WriteExampleToDefault copies the bundled example to the user's default config path (used by
// --init), creating parent directories as needed. An existing target is not overwritten and
// created=false is returned. zh picks the Chinese-commented variant (see ExampleFor).
func WriteExampleToDefault(zh bool) (path string, created bool, err error) {
	p, ok := defaultConfigPath()
	if !ok {
		return "", false, fmt.Errorf("cannot determine the user config dir ($HOME is not set)")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return p, false, fmt.Errorf("creating config dir %s: %w", filepath.Dir(p), err)
	}
	if fileExists(p) {
		return p, false, nil
	}
	if err := os.WriteFile(p, ExampleFor(zh), 0o644); err != nil {
		return p, false, fmt.Errorf("writing %s: %w", p, err)
	}
	return p, true, nil
}
