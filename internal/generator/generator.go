package generator

import (
	"errors"
	"fmt"
)

var validCommands = map[string]func([]string) error{
	"map":           runMap,
	"prefab-curves": runPrefabCurves,
	"cities":        runCities,
	"ets2-villages": runEts2Villages,
	"footprints":    runFootprints,
	"contours":      runContours,
	"achievements":  runAchievements,
	"spritesheet":   runSpritesheet,
	"graph":         runGraph,
}

func Run(args []string) error {
	if len(args) == 0 {
		return errors.New("missing command")
	}

	command := args[0]
	handler, ok := validCommands[command]
	if !ok {
		return fmt.Errorf("unknown command: %s", command)
	}
	return handler(args[1:])
}
