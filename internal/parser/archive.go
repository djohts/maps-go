package parser

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var requiredGameArchives = map[string]bool{
	"base.scs":       true,
	"base_map.scs":   true,
	"base_share.scs": true,
	"core.scs":       true,
	"def.scs":        true,
	"locale.scs":     true,
	"version.scs":    true,
}

func collectGameArchivePaths(gameDir string, includeDlc bool) ([]string, error) {
	entries, err := os.ReadDir(gameDir)
	if err != nil {
		return nil, err
	}
	archivePaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".scs") {
			continue
		}
		if requiredGameArchives[name] || (includeDlc && strings.HasPrefix(strings.ToLower(name), "dlc")) {
			archivePaths = append(archivePaths, filepath.Join(gameDir, name))
		}
	}
	sort.Strings(archivePaths)
	return archivePaths, nil
}

func parseArchivesMinimal(gameArchivePaths, modArchivePaths []string, onlyDefs bool) (parseResult, error) {
	allArchivePaths := append([]string{}, gameArchivePaths...)
	allArchivePaths = append(allArchivePaths, modArchivePaths...)

	entries, warnings := readSIIEntries(allArchivePaths)
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, warning)
	}

	mapName := detectMap(entries)

	countries := parseCountries(entries)
	cities := parseCities(entries)
	companyDefs := parseCompanyDefs(entries)

	defData := newCollectionMap(defDataKeys)
	defData["countries"] = countries
	defData["companyDefs"] = companyDefs
	defData["roadLooks"] = []any{}
	defData["prefabDescriptions"] = []any{}
	defData["modelDescriptions"] = []any{}
	defData["signDescriptions"] = []any{}
	defData["achievements"] = []any{}
	defData["routes"] = []any{}
	defData["mileageTargets"] = []any{}

	if onlyDefs {
		return parseResult{
			MapName:  mapName,
			OnlyDefs: true,
			DefData:  defData,
			MapData:  map[string][]any{},
			Icons:    map[string][]byte{},
		}, nil
	}

	mapData := newCollectionMap(mapDataKeys)
	for key, values := range defData {
		if _, ok := mapData[key]; ok {
			mapData[key] = values
		}
	}
	mapData["cities"] = cities
	mapData["companies"] = []any{}
	mapData["dividers"] = []any{}
	mapData["ferries"] = []any{}
	mapData["mapAreas"] = []any{}
	mapData["models"] = []any{}
	mapData["nodes"] = []any{}
	mapData["elevation"] = []any{}
	mapData["pois"] = []any{}
	mapData["prefabs"] = []any{}
	mapData["roads"] = []any{}
	mapData["trajectories"] = []any{}
	mapData["triggers"] = []any{}
	mapData["cutscenes"] = []any{}

	return parseResult{
		MapName:  mapName,
		OnlyDefs: false,
		DefData:  defData,
		MapData:  mapData,
		Icons:    map[string][]byte{},
	}, nil
}

func newCollectionMap(keys []string) map[string][]any {
	m := make(map[string][]any, len(keys))
	for _, key := range keys {
		m[key] = []any{}
	}
	return m
}

func readSIIEntries(archivePaths []string) (map[string]string, []string) {
	entries := map[string]string{}
	warnings := make([]string, 0)
	for _, archivePath := range archivePaths {
		r, err := openArchiveReader(archivePath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("warning: unable to open archive %s: %v", archivePath, err))
			continue
		}
		for _, name := range r.FileNames() {
			if !strings.HasSuffix(name, ".sii") {
				continue
			}
			content, err := r.ReadFile(name)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: unable to read file %s in %s: %v", name, archivePath, err))
				continue
			}
			entries[name] = string(content)
		}
		_ = r.Close()
	}
	return entries, warnings
}

func detectMap(entries map[string]string) string {
	versionText, ok := entries["version.sii"]
	if ok {
		application := extractSIIString(versionText, []string{"application"})
		switch strings.ToLower(application) {
		case "eut2", "ets2", "europe":
			return "europe"
		case "ats", "usa":
			return "usa"
		}
	}

	for path := range entries {
		if strings.Contains(path, "map/europe/") {
			return "europe"
		}
	}
	return "usa"
}

var fileTypeRegex = regexp.MustCompile(`^[a-z0-9_/.-]+$`)

func parseCountries(entries map[string]string) []any {
	countriesByToken := map[string]map[string]any{}
	paths := sortedEntryPaths(entries)
	for _, filePath := range paths {
		if !strings.HasPrefix(filePath, "def/country/") || !strings.HasSuffix(filePath, ".sii") {
			continue
		}
		if !fileTypeRegex.MatchString(filePath) {
			continue
		}
		content := entries[filePath]
		token := strings.TrimSuffix(filepath.Base(filePath), ".sii")
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			continue
		}
		name := extractSIIString(content, []string{"country_name", "name"})
		if name == "" {
			name = token
		}
		code := extractSIIString(content, []string{"country_code", "code"})
		if code == "" {
			code = strings.ToUpper(token)
		}
		x := extractSIINumber(content, []string{"map_x", "x"})
		y := extractSIINumber(content, []string{"map_z", "map_y", "y"})

		countriesByToken[token] = map[string]any{
			"token": token,
			"name":  name,
			"code":  code,
			"x":     x,
			"y":     y,
		}
	}
	return toOrderedAnySlice(countriesByToken)
}

func parseCities(entries map[string]string) []any {
	citiesByToken := map[string]map[string]any{}
	paths := sortedEntryPaths(entries)
	for _, filePath := range paths {
		if !strings.HasPrefix(filePath, "def/city/") || !strings.HasSuffix(filePath, ".sii") {
			continue
		}
		content := entries[filePath]
		token := strings.TrimSuffix(filepath.Base(filePath), ".sii")
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			continue
		}
		name := extractSIIString(content, []string{"city_name", "name"})
		if name == "" {
			name = token
		}
		countryToken := extractSIICountryToken(content)
		x := extractSIINumber(content, []string{"map_x", "x"})
		y := extractSIINumber(content, []string{"map_z", "map_y", "y"})

		citiesByToken[token] = map[string]any{
			"token":        token,
			"name":         name,
			"countryToken": countryToken,
			"x":            x,
			"y":            y,
			"areas": []map[string]any{
				{
					"hidden": false,
					"width":  0,
					"height": 0,
				},
			},
		}
	}
	return toOrderedAnySlice(citiesByToken)
}

func parseCompanyDefs(entries map[string]string) []any {
	companiesByToken := map[string]map[string]any{}
	paths := sortedEntryPaths(entries)
	for _, filePath := range paths {
		if !strings.HasPrefix(filePath, "def/company/") || !strings.HasSuffix(filePath, ".sii") {
			continue
		}
		content := entries[filePath]
		token := strings.TrimSuffix(filepath.Base(filePath), ".sii")
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			continue
		}
		name := extractSIIString(content, []string{"name", "company_name", "title"})
		if name == "" {
			name = token
		}
		cityTokens := extractSiiCityTokenList(content)
		companiesByToken[token] = map[string]any{
			"token":          token,
			"name":           name,
			"cityTokens":     cityTokens,
			"cargoInTokens":  []string{},
			"cargoOutTokens": []string{},
		}
	}
	return toOrderedAnySlice(companiesByToken)
}

func sortedEntryPaths(entries map[string]string) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func toOrderedAnySlice(values map[string]map[string]any) []any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func extractSIIString(content string, keys []string) string {
	lines := normalizeSIILines(content)
	keySet := map[string]bool{}
	for _, key := range keys {
		keySet[strings.ToLower(key)] = true
	}
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		key = strings.TrimSuffix(key, "[]")
		if !keySet[key] {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"")
		if value != "" {
			return value
		}
	}
	return ""
}

func extractSIINumber(content string, keys []string) float64 {
	lines := normalizeSIILines(content)
	keySet := map[string]bool{}
	for _, key := range keys {
		keySet[strings.ToLower(key)] = true
	}
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		if !keySet[key] {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"")
		if num, err := strconv.ParseFloat(value, 64); err == nil {
			return num
		}
	}
	return 0
}

var countryRefRegex = regexp.MustCompile(`(?i)country\s*:\s*\.?(?:country\.)?([a-z0-9_]+)`)

func extractSIICountryToken(content string) string {
	if matches := countryRefRegex.FindStringSubmatch(content); len(matches) > 1 {
		return strings.ToLower(strings.TrimSpace(matches[1]))
	}
	return ""
}

var cityTokenRegex = regexp.MustCompile(`(?i)city(?:_token)?(?:\[[0-9]+\]|\[\])?\s*:\s*\.?(?:city\.)?([a-z0-9_]+)`)

func extractSiiCityTokenList(content string) []string {
	matches := cityTokenRegex.FindAllStringSubmatch(content, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		token := strings.ToLower(strings.TrimSpace(match[1]))
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func normalizeSIILines(content string) []string {
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

func readAll(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	var buf bytes.Buffer
	_, err := io.Copy(&buf, rc)
	return buf.Bytes(), err
}
