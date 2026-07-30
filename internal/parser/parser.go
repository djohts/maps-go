package parser

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Options struct {
	GameDir               string
	ModsDir               string
	GameLog               string
	ModOrderFile          string
	ModOrder              string
	EnabledMods           string
	DisabledMods          string
	StrictModDependencies bool
	ModConflictPolicy     ModConflictPolicy
	OutputDir             string
	IncludeDlc            bool
	OnlyDefs              bool
	DryRun                bool
	Debug                 bool
}

func Run(args []string, stdout io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	gameArchivePaths, err := collectGameArchivePaths(opts.GameDir, opts.IncludeDlc)
	if err != nil {
		return err
	}
	if opts.Debug {
		_, _ = fmt.Fprintf(stdout, "game archives: %d\n", len(gameArchivePaths))
	}

	gameLogOrder := []string{}
	if opts.GameLog != "" {
		gameLogOrder, err = getLoadOrder(opts.GameLog)
		if err != nil {
			return err
		}
	}
	explicitModOrder := parseModList(opts.ModOrder)
	if opts.ModOrderFile != "" {
		orderFromFile, err := getLoadOrderFromFile(opts.ModOrderFile)
		if err != nil {
			return err
		}
		explicitModOrder = append(explicitModOrder, orderFromFile...)
	}
	enabledMods := parseModList(opts.EnabledMods)
	disabledMods := parseModList(opts.DisabledMods)

	modArchivePaths := []string{}
	if opts.ModsDir != "" {
		mods, warnings, err := indexModsFromDirectory(opts.ModsDir)
		if err != nil {
			return err
		}
		for _, warning := range warnings {
			_, _ = fmt.Fprintln(os.Stderr, warning)
		}

		resolved, err := resolveModLoadOrder(mods, ResolveModsOptions{
			ExplicitOrder:      explicitModOrder,
			GameLogOrder:       gameLogOrder,
			EnabledMods:        enabledMods,
			DisabledMods:       disabledMods,
			StrictDependencies: opts.StrictModDependencies,
			ConflictPolicy:     opts.ModConflictPolicy,
		})
		if err != nil {
			return err
		}
		for _, warning := range resolved.Warnings {
			_, _ = fmt.Fprintln(os.Stderr, warning)
		}

		for _, mod := range resolved.OrderedMods {
			modArchivePaths = append(modArchivePaths, mod.ArchivePath)
		}
		if opts.Debug {
			for idx, mod := range resolved.OrderedMods {
				_, _ = fmt.Fprintf(stdout, "%03d %s => %s\n", idx, mod.CanonicalName, mod.ArchivePath)
			}
		}
	}

	result, err := parseArchivesMinimal(gameArchivePaths, modArchivePaths, opts.OnlyDefs)
	if err != nil {
		return err
	}

	if opts.DryRun {
		_, _ = fmt.Fprintln(stdout, "dry run complete.")
		return nil
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return err
	}

	data := result.MapData
	keys := mapDataKeys
	if result.OnlyDefs {
		data = result.DefData
		keys = defDataKeys
	}
	if !hasCollectionData(data, keys) {
		return errors.New("no parser data was extracted from the selected archives; refusing to write empty output JSON files")
	}
	for _, key := range keys {
		collection, ok := data[key]
		if !ok {
			collection = []any{}
		}
		filename := fmt.Sprintf("%s-%s.json", result.MapName, key)
		filePath := filepath.Join(opts.OutputDir, filename)
		if err := writeJSONArray(filePath, collection); err != nil {
			return err
		}
	}

	if !result.OnlyDefs {
		iconsDir := filepath.Join(opts.OutputDir, "icons")
		if err := os.MkdirAll(iconsDir, 0o755); err != nil {
			return err
		}
		for name, data := range result.Icons {
			if err := os.WriteFile(filepath.Join(iconsDir, name+".png"), data, 0o644); err != nil {
				return err
			}
		}
	}

	_, _ = fmt.Fprintln(stdout, "done.")
	return nil
}

func parseArgs(args []string) (Options, error) {
	fs := flag.NewFlagSet("parser", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := Options{
		IncludeDlc:        true,
		ModConflictPolicy: ModConflictWarn,
	}
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
	policy := string(ModConflictWarn)
	fs.StringVar(&policy, "modConflictPolicy", string(ModConflictWarn), "mod conflict policy")
	fs.StringVar(&opts.OutputDir, "o", "", "output dir")
	fs.StringVar(&opts.OutputDir, "outputDir", "", "output dir")
	fs.BoolVar(&opts.IncludeDlc, "includeDlc", true, "include dlc")
	fs.BoolVar(&opts.OnlyDefs, "onlyDefs", false, "only defs")
	fs.BoolVar(&opts.DryRun, "dryRun", false, "dry run")
	fs.BoolVar(&opts.Debug, "debug", false, "debug")

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	if fs.NArg() != 0 {
		return Options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.GameDir == "" {
		return Options{}, errors.New("-g/--gameDir is required")
	}
	if opts.OutputDir == "" {
		return Options{}, errors.New("-o/--outputDir is required")
	}

	opts.GameDir = untildify(opts.GameDir)
	opts.ModsDir = untildify(opts.ModsDir)
	opts.GameLog = untildify(opts.GameLog)
	opts.ModOrderFile = untildify(opts.ModOrderFile)
	opts.OutputDir = untildify(opts.OutputDir)

	switch ModConflictPolicy(policy) {
	case ModConflictWarn, ModConflictError, ModConflictIgnore:
		opts.ModConflictPolicy = ModConflictPolicy(policy)
	default:
		return Options{}, fmt.Errorf("invalid --modConflictPolicy: %s", policy)
	}

	return opts, nil
}

func untildify(v string) string {
	if !strings.HasPrefix(v, "~") {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return v
	}
	if v == "~" {
		return home
	}
	if strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "~\\") {
		return filepath.Join(home, v[2:])
	}
	return v
}

func writeJSONArray(path string, values []any) error {
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func hasCollectionData(collections map[string][]any, keys []string) bool {
	for _, key := range keys {
		if len(collections[key]) > 0 {
			return true
		}
	}
	return false
}
