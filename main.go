// tui-dashboard is a config-driven terminal dashboard: data sources are declared under
// sources in the config, each one a shell command run periodically on its own goroutine at
// its own interval, and the display is decided by the widgets in layout. Config search
// order: --config <path> → ./config.yaml → $XDG_CONFIG_HOME/tui-dashboard/config.yaml →
// ~/.config/tui-dashboard/config.yaml → the bundled example.yaml. The cmd working directory
// is the directory of the config file actually loaded (see scriptDir); falling back to the
// bundled example inherits the process CWD, where its relative scripts/ commands need a
// scripts/ tree.
package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"tui-dashboard/internal/config"
	"tui-dashboard/internal/source"
	"tui-dashboard/internal/ui"
)

//go:embed scripts
var scriptFS embed.FS // copied to the config dir by --init along with the example config (see InitExampleWithScripts)

func main() {
	path := flag.String("config", "", "path to the YAML config file (empty = auto-search: ./config.yaml → ~/.config → bundled example)")
	initCfg := flag.Bool("init", false, "copy the bundled example config and scripts/ into the default config path ($XDG_CONFIG_HOME or ~/.config), then exit; existing files are left alone, rerunning only fills in missing scripts")
	verbose := flag.Bool("v", false, "write startup log lines (config origin, cmd working dir, config warnings) to stderr; silent by default")
	flag.Parse()

	say := startupLog{on: *verbose}

	if *initCfg {
		p, created, err := config.InitExampleWithScripts(scriptFS, config.IsChineseLocale())
		if err != nil {
			log.Fatalf("failed to write the default config: %v", err)
		}
		if created {
			log.Printf("wrote the example config: %s\nalong with the example scripts in %s\nEdit it now and run `go run .` to take effect."+
				"\nNote: %q comes earlier in the search chain — if the current directory already has one, it wins over this file.",
				p, filepath.Join(filepath.Dir(p), "scripts"), "config.yaml")
		} else {
			log.Printf("default config already exists, not overwritten: %s\nThe scripts in the same dir have been topped up (only missing ones are copied). Just edit that file."+
				"\nNote: if the current directory already has a ./config.yaml, it wins over this file.", p)
		}
		os.Exit(0)
	}

	data, src, err := config.Locate(*path)
	if err != nil {
		log.Fatalf("failed to locate config: %v", err)
	}
	// Without a config file the bundled example is loaded, and its relative commands
	// (./scripts/*.sh) need a scripts/ tree under the process CWD; without one every panel
	// would only report fetch failures. log.Fatalf, not say.Printf, which is silent by default.
	if config.ResolvePath(*path) == "" && !hasScriptsDir(".") {
		log.Fatalf("no config file found and no scripts/ tree here: the bundled example's commands are relative (./scripts/*.sh)" +
			"\n  --init           write the example config and its scripts into the default config path" +
			"\n  --config <path>  use an existing config file instead")
	}
	cfg, err := config.FromBytes(data)
	if err != nil {
		log.Fatalf("failed to parse config (%s): %v", src, err)
	}
	if cfg.Theme != "" && !ui.UseTheme(cfg.Theme) {
		log.Fatalf("unknown theme %q, available: %s", cfg.Theme, strings.Join(ui.ThemeNames(), " "))
	}
	for _, s := range cfg.Sources {
		if _, ok := source.ParseType(s.Type); !ok {
			log.Fatalf("source %q: unknown type: %q, available: %s (required, see README: source types)",
				s.Name, s.Type, strings.Join(source.TypeNames, " | "))
		}
	}
	// A widget/source type mismatch never blocks startup: that panel draws its default look
	// and states the reason, everything else runs as usual; -v prints one line per mismatch.
	for _, m := range checkWidgetTypes(cfg) {
		say.Printf("⚠ type mismatch (that panel falls back to its default look): %s", m)
	}
	// A data source in a row/section title likewise never blocks startup, but the result is
	// subtler: the field resolves to nothing (a single-level field goes empty, a multi-level
	// one falls back to the whole title verbatim), and a title that ends up empty takes no
	// slot in that row's title band (see composeWide).
	for _, m := range ui.RowTitleSourceRefs(cfg) {
		say.Printf("⚠ a data source cannot be used in a row title: %s", m)
	}
	say.Printf("using config: %s", src)

	// Every source's cmd runs with the config file's directory as its working directory
	// (cmd.Dir), so relative paths like ./scripts/*.sh follow the config; falling back to the
	// bundled example leaves it unset and inherits the process CWD. A config directory with no
	// scripts/ tree of its own falls back the same way when the process CWD has one.
	cfgDir := scriptDir(*path)
	dir := resolveCmdDir(cfgDir)
	switch {
	case dir != "":
		say.Printf("cmd working dir: %s", dir)
	case cfgDir != "":
		say.Printf("cmd working dir: (inherited from the process CWD: no scripts/ tree beside the config)")
	default:
		say.Printf("cmd working dir: (inherited from the process CWD; no config file)")
	}

	// Data sources: one goroutine per source, polling on its own interval
	mgr := source.New(cfg.Sources, dir)
	defer mgr.Close()

	prog := ui.New(mgr, cfg.Layout, cfg.PollInterval.Duration)
	if _, err := prog.Run(); err != nil {
		log.Fatalf("dashboard exited abnormally: %v", err)
	}
}

// startupLog is the outlet for startup log lines: silent by default, written to stderr only
// under -v. Each of these lines (which config is in use, the cmd working dir, which panel has
// a type mismatch) has a counterpart on screen at runtime, and they are printed before the
// alt screen is entered, so setting AltScreen covers them. The output of --init and
// log.Fatalf comes from a direct log call in main and does not go through here.
type startupLog struct{ on bool }

func (l startupLog) Printf(format string, args ...any) {
	if l.on {
		log.Printf(format, args...)
	}
}

// checkWidgetTypes checks each widget's type against the type its source declares, returning
// startup hints for the -v log (empty = all match). Hints only, never blocks startup: a
// mismatched panel draws its default look at runtime (with the reason).
//
// Only the type the source declares is checked, not which fields value: picks out (column
// names are unknown until the script runs). A pure function that touches neither log nor cfg,
// so main_test.go can build a config.Config directly and test it.
func checkWidgetTypes(cfg *config.Config) []string {
	srcType := make(map[string]source.Type, len(cfg.Sources))
	for _, s := range cfg.Sources {
		t, _ := source.ParseType(s.Type) // validity already reported by the fatal above
		srcType[s.Name] = t
	}
	var out []string
	for ri, row := range cfg.Layout {
		for wi, w := range row.Widgets {
			t, ok := srcType[w.Source]
			if !ok || ui.Accepts(w.Type, t) {
				continue // static text with no source, or already matching
			}
			out = append(out, fmt.Sprintf("layout[%d] widget[%d](type=%s, source=%s): source declares %s, this panel takes %s",
				ri, wi, w.Type, w.Source, t, ui.AcceptedTypes(w.Type)))
		}
	}
	return out
}

// scriptDir returns the cmd working directory shared by all sources = the directory of the
// config file actually loaded; when there is no config file (falling back to the bundled
// example) it returns "", which leaves cmd.Dir unset and inherits the process CWD.
//
// It must return an absolute path: a relative cmd.Dir is resolved by os/exec against the
// process CWD at each execution, so after the process chdirs the same source would drift
// elsewhere. For the ./config.yaml case ResolvePath returns the relative path "config.yaml",
// and a direct filepath.Dir yields "." (not the absolute process CWD), hence Abs before Dir.
func scriptDir(explicit string) string {
	p := config.ResolvePath(explicit)
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Dir(p) // unreachable in theory: only Getwd can fail
	}
	return filepath.Dir(abs)
}

// resolveCmdDir applies the fallback to the directory scriptDir returned: when the config file's
// directory has no scripts/ tree of its own but the process CWD has one, every cmd inherits the
// process CWD instead ("" leaves cmd.Dir unset), which is what makes
//
//	go run . --config internal/config/example.yaml
//
// work from the repo root. The config directory wins whenever it has a tree, so a config and its
// scripts sitting together still resolve against themselves.
func resolveCmdDir(cfgDir string) string {
	if cfgDir != "" && !hasScriptsDir(cfgDir) && hasScriptsDir(".") {
		return ""
	}
	return cfgDir
}

// hasScriptsDir reports whether dir holds a scripts/ tree, which the bundled example's
// relative ./scripts/*.sh commands need: with no config file that example is what gets
// loaded, and its cmd.Dir stays unset, so they resolve against the process CWD.
//
// dir is a parameter so tests can point it at a temp directory.
func hasScriptsDir(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "scripts"))
	return err == nil && fi.IsDir()
}
