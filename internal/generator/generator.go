package generator

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var validCommands = map[string]bool{
	"map":           true,
	"prefab-curves": true,
	"cities":        true,
	"ets2-villages": true,
	"footprints":    true,
	"contours":      true,
	"achievements":  true,
	"spritesheet":   true,
	"graph":         true,
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type commandOutput struct {
	Command   string   `json:"command"`
	InputDir  string   `json:"inputDir"`
	OutputDir string   `json:"outputDir"`
	Maps      []string `json:"maps,omitempty"`
}

func Run(args []string) error {
	if len(args) == 0 {
		return errors.New("missing command")
	}

	command := args[0]
	if !validCommands[command] {
		return fmt.Errorf("unknown command: %s", command)
	}

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var maps stringList
	inputDir := fs.String("i", "", "input directory")
	fs.StringVar(inputDir, "inputDir", "", "input directory")
	outputDir := fs.String("o", "", "output directory")
	fs.StringVar(outputDir, "outputDir", "", "output directory")
	fs.Var(&maps, "m", "map name")
	fs.Var(&maps, "map", "map name")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *inputDir == "" {
		return errors.New("-i/--inputDir is required")
	}
	if *outputDir == "" {
		return errors.New("-o/--outputDir is required")
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}

	payload := commandOutput{
		Command:   command,
		InputDir:  *inputDir,
		OutputDir: *outputDir,
		Maps:      maps,
	}

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	outputFile := filepath.Join(*outputDir, command+".json")
	return os.WriteFile(outputFile, append(b, '\n'), 0o644)
}
