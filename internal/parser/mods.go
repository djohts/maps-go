package parser

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var archiveExtensions = map[string]bool{
	".scs": true,
	".zip": true,
}

var manifestCandidatePaths = []string{
	"manifest.sii",
	"mod_description.sii",
	"mod/manifest.sii",
	"mod/mod_description.sii",
}

var gameLogLoadOrderRegex = regexp.MustCompile(`(?i).*\[mods\] Active (?:local|steam|workshop) mod (.*) \(name:.*`)

func discoverModArchivePaths(modsDir string) ([]string, error) {
	roots := []string{modsDir}
	archives := make([]string, 0)

	for len(roots) > 0 {
		root := roots[len(roots)-1]
		roots = roots[:len(roots)-1]

		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		for _, entry := range entries {
			fullPath := filepath.Join(root, entry.Name())
			if entry.IsDir() {
				roots = append(roots, fullPath)
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if archiveExtensions[ext] {
				archives = append(archives, fullPath)
			}
		}
	}

	sort.Strings(archives)
	return archives, nil
}

func indexModsFromDirectory(modsDir string) ([]IndexedMod, []string, error) {
	archivePaths, err := discoverModArchivePaths(modsDir)
	if err != nil {
		return nil, nil, err
	}
	return indexModArchives(archivePaths)
}

func indexModArchives(archivePaths []string) ([]IndexedMod, []string, error) {
	mods := make([]IndexedMod, 0, len(archivePaths))
	warnings := make([]string, 0)

	for _, archivePath := range archivePaths {
		fileStem := strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))
		canonicalName := fileStem
		displayName := fileStem

		metadata, warning := readManifestMetadata(archivePath)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if metadata != nil {
			if metadata.PackageName != "" {
				canonicalName = metadata.PackageName
			}
			if metadata.DisplayName != "" {
				displayName = metadata.DisplayName
			} else {
				displayName = canonicalName
			}
			mods = append(mods, IndexedMod{
				ArchivePath:   archivePath,
				FileStem:      fileStem,
				CanonicalName: canonicalName,
				DisplayName:   displayName,
				Dependencies:  metadata.Dependencies,
				Incompatible:  metadata.Incompatible,
				ManifestPath:  metadata.ManifestPath,
			})
			continue
		}

		mods = append(mods, IndexedMod{
			ArchivePath:   archivePath,
			FileStem:      fileStem,
			CanonicalName: canonicalName,
			DisplayName:   displayName,
		})
	}

	return mods, warnings, nil
}

type manifestMetadata struct {
	ManifestPath string
	PackageName  string
	DisplayName  string
	Dependencies []string
	Incompatible []string
}

func readManifestMetadata(archivePath string) (*manifestMetadata, string) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		// Not all .scs files are zip-readable; keep mod in index without metadata.
		return nil, ""
	}
	defer r.Close()

	files := map[string]*zip.File{}
	for _, f := range r.File {
		files[strings.ToLower(strings.TrimPrefix(f.Name, "/"))] = f
	}

	for _, candidate := range manifestCandidatePaths {
		f, ok := files[strings.ToLower(candidate)]
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Sprintf("ignoring unreadable manifest %s in %s: %v", candidate, archivePath, err)
		}
		data, err := readAll(rc)
		if err != nil {
			return nil, fmt.Sprintf("ignoring unreadable manifest %s in %s: %v", candidate, archivePath, err)
		}
		md := parseManifestSII(string(data))
		md.ManifestPath = candidate
		return &md, ""
	}

	return nil, ""
}

func parseManifestSII(content string) manifestMetadata {
	md := manifestMetadata{}
	lines := normalizeSiiLines(content)

	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := normalizeManifestKey(parts[0])
		value := trimSiiValue(parts[1])

		switch key {
		case "packagename", "package":
			if md.PackageName == "" {
				md.PackageName = value
			}
		case "displayname", "name", "title":
			if md.DisplayName == "" {
				md.DisplayName = value
			}
		case "dependencies", "dependency", "dep":
			if value != "" {
				md.Dependencies = append(md.Dependencies, value)
			}
		case "incompatible", "incompatibility", "conflicts":
			if value != "" {
				md.Incompatible = append(md.Incompatible, value)
			}
		}
	}

	md.Dependencies = uniqueTrimmed(md.Dependencies)
	md.Incompatible = uniqueTrimmed(md.Incompatible)
	return md
}

func normalizeSiiLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func normalizeManifestKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.TrimSuffix(k, "[]")
	replacer := strings.NewReplacer("_", "", "-", "", ".", "")
	return replacer.Replace(k)
}

func trimSiiValue(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "\"")
	v = strings.TrimPrefix(v, ".")
	v = strings.TrimSpace(v)
	return v
}

func uniqueTrimmed(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func getLoadOrder(gameLogPath string) ([]string, error) {
	data, err := os.ReadFile(gameLogPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	mods := make([]string, 0)
	seen := map[string]bool{}
	for _, line := range lines {
		matches := gameLogLoadOrderRegex.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}
		mod := strings.TrimSpace(matches[1])
		if mod == "" || seen[mod] {
			continue
		}
		seen[mod] = true
		mods = append(mods, mod)
	}
	return mods, nil
}

func getLoadOrderFromFile(loadOrderPath string) ([]string, error) {
	data, err := os.ReadFile(loadOrderPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	mods := make([]string, 0)
	seen := map[string]bool{}
	for _, line := range lines {
		line = stripComment(line)
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		mods = append(mods, line)
	}
	return mods, nil
}

func stripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = line[:idx]
	}
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = line[:idx]
	}
	return line
}

func resolveModLoadOrder(mods []IndexedMod, options ResolveModsOptions) (ResolveModsResult, error) {
	warnings := make([]string, 0)
	policy := options.ConflictPolicy
	if policy == "" {
		policy = ModConflictWarn
	}

	selected := filterMods(mods, options.EnabledMods, options.DisabledMods, &warnings)
	if len(selected) == 0 {
		return ResolveModsResult{OrderedMods: []IndexedMod{}, Warnings: warnings}, nil
	}

	fallbackOrder := make([]string, 0, len(selected))
	for _, mod := range selected {
		fallbackOrder = append(fallbackOrder, mod.ArchivePath)
	}
	sort.Strings(fallbackOrder)
	fallbackRank := map[string]int{}
	for idx, archivePath := range fallbackOrder {
		fallbackRank[archivePath] = idx
	}

	priorityRank := map[string]int{}
	rank := 0
	rank = addOrderRanks(selected, options.ExplicitOrder, "explicit mod order", &warnings, priorityRank, rank)
	addOrderRanks(selected, options.GameLogOrder, "game log mod order", &warnings, priorityRank, rank)

	prioritized := append([]IndexedMod{}, selected...)
	sort.SliceStable(prioritized, func(i, j int) bool {
		a := prioritized[i]
		b := prioritized[j]
		rankA, okA := priorityRank[a.ArchivePath]
		rankB, okB := priorityRank[b.ArchivePath]
		if okA && okB {
			return rankA < rankB
		}
		if okA {
			return true
		}
		if okB {
			return false
		}
		return fallbackRank[a.ArchivePath] < fallbackRank[b.ArchivePath]
	})

	edges := map[string]map[string]bool{}
	for _, mod := range prioritized {
		edges[mod.ArchivePath] = map[string]bool{}
	}

	for _, mod := range prioritized {
		for _, dependency := range mod.Dependencies {
			matched := findModsByReference(prioritized, dependency)
			filtered := make([]IndexedMod, 0, len(matched))
			for _, candidate := range matched {
				if candidate.ArchivePath != mod.ArchivePath {
					filtered = append(filtered, candidate)
				}
			}
			if len(filtered) == 0 {
				message := fmt.Sprintf("%s is missing dependency \"%s\"", mod.DisplayName, dependency)
				if options.StrictDependencies {
					return ResolveModsResult{}, errors.New(message)
				}
				if policy == ModConflictWarn {
					warnings = append(warnings, message)
				}
				continue
			}
			if len(filtered) > 1 {
				names := make([]string, 0, len(filtered))
				for _, m := range filtered {
					names = append(names, m.DisplayName)
				}
				warnings = append(warnings, fmt.Sprintf("%s dependency \"%s\" matched multiple mods: %s", mod.DisplayName, dependency, strings.Join(names, ", ")))
			}
			selectedDependency := filtered[0]
			edges[selectedDependency.ArchivePath][mod.ArchivePath] = true
		}

		for _, incompatible := range mod.Incompatible {
			matched := findModsByReference(prioritized, incompatible)
			filtered := make([]IndexedMod, 0, len(matched))
			for _, candidate := range matched {
				if candidate.ArchivePath != mod.ArchivePath {
					filtered = append(filtered, candidate)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			names := make([]string, 0, len(filtered))
			for _, m := range filtered {
				names = append(names, m.DisplayName)
			}
			message := fmt.Sprintf("%s is incompatible with %s", mod.DisplayName, strings.Join(names, ", "))
			if policy == ModConflictError {
				return ResolveModsResult{}, errors.New(message)
			}
			if policy == ModConflictWarn {
				warnings = append(warnings, message)
			}
		}
	}

	sortedMods := stableTopologicalSort(prioritized, edges)
	if len(sortedMods) != len(prioritized) {
		warnings = append(warnings, "detected cyclic mod dependencies; falling back to non-dependency order")
		return ResolveModsResult{OrderedMods: prioritized, Warnings: warnings}, nil
	}
	return ResolveModsResult{OrderedMods: sortedMods, Warnings: warnings}, nil
}

func normalizeModReference(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".scs")
	value = strings.TrimSuffix(value, ".zip")
	builder := strings.Builder{}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func filterMods(mods []IndexedMod, enabledMods, disabledMods []string, warnings *[]string) []IndexedMod {
	enabled := normalizeReferenceSet(enabledMods)
	disabled := normalizeReferenceSet(disabledMods)

	selected := mods
	if len(enabled) > 0 {
		filtered := make([]IndexedMod, 0, len(mods))
		for _, mod := range mods {
			if hasAnyAlias(mod, enabled) {
				filtered = append(filtered, mod)
			}
		}
		selected = filtered

		for ref := range enabled {
			matched := false
			for _, mod := range mods {
				if hasAlias(mod, ref) {
					matched = true
					break
				}
			}
			if !matched {
				*warnings = append(*warnings, fmt.Sprintf("enabled mod reference \"%s\" did not match any mod", ref))
			}
		}
	}

	if len(disabled) > 0 {
		for ref := range disabled {
			matched := false
			for _, mod := range selected {
				if hasAlias(mod, ref) {
					matched = true
					break
				}
			}
			if !matched {
				*warnings = append(*warnings, fmt.Sprintf("disabled mod reference \"%s\" did not match any mod", ref))
			}
		}

		filtered := make([]IndexedMod, 0, len(selected))
		for _, mod := range selected {
			if !hasAnyAlias(mod, disabled) {
				filtered = append(filtered, mod)
			}
		}
		selected = filtered
	}

	return selected
}

func normalizeReferenceSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		normalized := normalizeModReference(value)
		if normalized != "" {
			set[normalized] = true
		}
	}
	return set
}

func addOrderRanks(mods []IndexedMod, references []string, source string, warnings *[]string, priorityRank map[string]int, initialRank int) int {
	rank := initialRank
	for _, ref := range references {
		matched := findModsByReference(mods, ref)
		if len(matched) == 0 {
			*warnings = append(*warnings, fmt.Sprintf("%s references unknown mod \"%s\"", source, ref))
			continue
		}
		if len(matched) > 1 {
			names := make([]string, 0, len(matched))
			for _, mod := range matched {
				names = append(names, mod.DisplayName)
			}
			*warnings = append(*warnings, fmt.Sprintf("%s reference \"%s\" matched multiple mods: %s", source, ref, strings.Join(names, ", ")))
		}
		for _, mod := range matched {
			if _, ok := priorityRank[mod.ArchivePath]; ok {
				continue
			}
			priorityRank[mod.ArchivePath] = rank
			rank++
		}
	}
	return rank
}

func findModsByReference(mods []IndexedMod, ref string) []IndexedMod {
	normalized := normalizeModReference(ref)
	if normalized == "" {
		return nil
	}
	out := make([]IndexedMod, 0)
	for _, mod := range mods {
		if hasAlias(mod, normalized) {
			out = append(out, mod)
		}
	}
	return out
}

func hasAnyAlias(mod IndexedMod, refs map[string]bool) bool {
	for ref := range refs {
		if hasAlias(mod, ref) {
			return true
		}
	}
	return false
}

func hasAlias(mod IndexedMod, normalizedRef string) bool {
	for _, alias := range getModAliases(mod) {
		if alias == normalizedRef {
			return true
		}
	}
	return false
}

func getModAliases(mod IndexedMod) []string {
	archiveBase := path.Base(mod.ArchivePath)
	aliases := []string{
		normalizeModReference(mod.CanonicalName),
		normalizeModReference(mod.DisplayName),
		normalizeModReference(mod.FileStem),
		normalizeModReference(archiveBase),
	}
	seen := map[string]bool{}
	uniq := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		uniq = append(uniq, alias)
	}
	return uniq
}

func stableTopologicalSort(mods []IndexedMod, edges map[string]map[string]bool) []IndexedMod {
	indexByPath := map[string]int{}
	modByPath := map[string]IndexedMod{}
	indegree := map[string]int{}
	for idx, mod := range mods {
		indexByPath[mod.ArchivePath] = idx
		modByPath[mod.ArchivePath] = mod
		indegree[mod.ArchivePath] = 0
	}
	for _, toSet := range edges {
		for toPath := range toSet {
			indegree[toPath] = indegree[toPath] + 1
		}
	}

	queue := make([]IndexedMod, 0)
	for _, mod := range mods {
		if indegree[mod.ArchivePath] == 0 {
			queue = append(queue, mod)
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		return indexByPath[queue[i].ArchivePath] < indexByPath[queue[j].ArchivePath]
	})

	result := make([]IndexedMod, 0, len(mods))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		for nextPath := range edges[current.ArchivePath] {
			indegree[nextPath]--
			if indegree[nextPath] == 0 {
				if nextMod, ok := modByPath[nextPath]; ok {
					queue = append(queue, nextMod)
				}
			}
		}
		sort.Slice(queue, func(i, j int) bool {
			return indexByPath[queue[i].ArchivePath] < indexByPath[queue[j].ArchivePath]
		})
	}

	return result
}

func parseModList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
