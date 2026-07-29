package parser

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type Options struct {
	GameDir               string
	ModsDir               string
	GameLog               string
	ModOrderFile          string
	ModOrder              string
	EnabledMods           string
	DisabledMods          string
	StrictModDependencies bool
	ModConflictPolicy     string
	OutputDir             string
	IncludeDlc            bool
	OnlyDefs              bool
	DryRun                bool
	Debug                 bool
}

type Report struct {
	GameArchives []string `json:"gameArchives"`
	ModArchives  []string `json:"modArchives"`
	OnlyDefs     bool     `json:"onlyDefs"`
}

func Run(args []string, stdout io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	gameArchives, err := findGameArchives(opts.GameDir, opts.IncludeDlc)
	if err != nil {
		return err
	}

	var modArchives []string
	if opts.ModsDir != "" {
		mods, err := findModArchives(opts.ModsDir)
		if err != nil {
			return err
		}

		explicitOrder := append(parseCSV(opts.ModOrder), readLoadOrderFile(opts.ModOrderFile)...)
		modArchives = orderAndFilterMods(mods, explicitOrder, parseCSV(opts.EnabledMods), parseCSV(opts.DisabledMods))
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(stdout, "dry run complete. game archives=%d mod archives=%d\n", len(gameArchives), len(modArchives))
		return nil
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return err
	}

	report := Report{
		GameArchives: gameArchives,
		ModArchives:  modArchives,
		OnlyDefs:     opts.OnlyDefs,
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	output := filepath.Join(opts.OutputDir, "parser-report.json")
	if err := os.WriteFile(output, append(payload, '\n'), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "done.")
	return nil
}

func parseArgs(args []string) (Options, error) {
	fs := flag.NewFlagSet("parser", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := Options{}
	fs.StringVar(&opts.GameDir, "g", "", "game dir")
	fs.StringVar(&opts.GameDir, "gameDir", "", "game dir")
	fs.StringVar(&opts.ModsDir, "m", "", "mods dir")
	fs.StringVar(&opts.ModsDir, "modsDir", "", "mods dir")
	fs.StringVar(&opts.GameLog, "l", "", "game log")
	fs.StringVar(&opts.GameLog, "gameLog", "", "game log")
	fs.StringVar(&opts.ModOrderFile, "modOrderFile", "", "mod order file")
	fs.StringVar(&opts.ModOrder, "modOrder", "", "mod order")
	fs.StringVar(&opts.EnabledMods, "enabledMods", "", "enabled mods")
	fs.StringVar(&opts.DisabledMods, "disabledMods", "", "disabled mods")
	fs.BoolVar(&opts.StrictModDependencies, "strictModDependencies", false, "strict dependencies")
	fs.StringVar(&opts.ModConflictPolicy, "modConflictPolicy", "warn", "mod conflict policy")
	fs.StringVar(&opts.OutputDir, "o", "", "output dir")
	fs.StringVar(&opts.OutputDir, "outputDir", "", "output dir")
	fs.BoolVar(&opts.IncludeDlc, "includeDlc", true, "include dlc")
	fs.BoolVar(&opts.OnlyDefs, "onlyDefs", false, "only defs")
	fs.BoolVar(&opts.DryRun, "dryRun", false, "dry run")
	fs.BoolVar(&opts.Debug, "debug", false, "debug")

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.GameDir == "" {
		return Options{}, errors.New("-g/--gameDir is required")
	}
	if opts.OutputDir == "" {
		return Options{}, errors.New("-o/--outputDir is required")
	}
	switch opts.ModConflictPolicy {
	case "warn", "error", "ignore":
	default:
		return Options{}, fmt.Errorf("invalid --modConflictPolicy: %s", opts.ModConflictPolicy)
	}
	return opts, nil
}

func parseCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func readLoadOrderFile(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func findGameArchives(dir string, includeDlc bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	required := map[string]bool{
		"base.scs":       true,
		"base_map.scs":   true,
		"base_share.scs": true,
		"core.scs":       true,
		"def.scs":        true,
		"locale.scs":     true,
		"version.scs":    true,
	}
	out := make([]string, 0)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		if required[name] || (includeDlc && strings.HasPrefix(name, "dlc")) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	slices.Sort(out)
	return out, nil
}

func findModArchives(dir string) ([]string, error) {
	out := make([]string, 0)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".scs") || strings.HasSuffix(name, ".zip") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

func orderAndFilterMods(mods, explicitOrder, enabled, disabled []string) []string {
	byToken := map[string]string{}
	for _, mod := range mods {
		base := filepath.Base(mod)
		trimmed := strings.TrimSuffix(base, filepath.Ext(base))
		byToken[base] = mod
		byToken[trimmed] = mod
	}

	used := map[string]bool{}
	ordered := make([]string, 0, len(mods))
	for _, token := range explicitOrder {
		if mod, ok := byToken[token]; ok && !used[mod] {
			used[mod] = true
			ordered = append(ordered, mod)
		}
	}
	for _, mod := range mods {
		if !used[mod] {
			ordered = append(ordered, mod)
		}
	}

	enabledSet := setOf(enabled)
	disabledSet := setOf(disabled)
	if len(enabledSet) == 0 && len(disabledSet) == 0 {
		return ordered
	}

	filtered := make([]string, 0, len(ordered))
	for _, mod := range ordered {
		base := filepath.Base(mod)
		trimmed := strings.TrimSuffix(base, filepath.Ext(base))
		if len(enabledSet) > 0 && !(enabledSet[base] || enabledSet[trimmed]) {
			continue
		}
		if disabledSet[base] || disabledSet[trimmed] {
			continue
		}
		filtered = append(filtered, mod)
	}
	return filtered
}

func setOf(values []string) map[string]bool {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	return set
}
