# maps-go

Go rewrite of `djohts/maps` (branch `copilot/merge-upstream-commits`) scoped to only two CLIs:

- `parser`
- `generator`

## Build

```bash
go build ./cmd/generator
go build ./cmd/parser
```

## Test

```bash
go test ./...
```

## Parser CLI

Parses game/mod archives, resolves mod load order/dependencies, and writes parser JSON output files.

```bash
go run ./cmd/parser -g /path/to/game -m /path/to/mods -o /path/to/output
```

Supported parser options mirror the source CLI surface, including:

- `--gameDir/-g`, `--modsDir/-m`, `--outputDir/-o`
- `--gameLog`, `--modOrderFile`, `--modOrder`
- `--enabledMods`, `--disabledMods`
- `--strictModDependencies`, `--modConflictPolicy`
- `--includeDlc`, `--onlyDefs`, `--dryRun`, `--debug`

## Generator CLI

Generates map artifacts from parser JSON files.

```bash
go run ./cmd/generator <command> [options]
```

Supported commands:

- `map`
- `prefab-curves`
- `cities`
- `ets2-villages`
- `footprints`
- `contours`
- `achievements`
- `spritesheet`
- `graph`
