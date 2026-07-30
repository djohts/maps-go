package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var ets2IsoA2 = map[string]string{
	"A":   "AT",
	"B":   "BE",
	"BIH": "BA",
	"EST": "EE",
	"F":   "FR",
	"D":   "DE",
	"H":   "HU",
	"I":   "IT",
	"RKS": "XK",
	"L":   "LU",
	"NMK": "MK",
	"MNE": "ME",
	"N":   "NO",
	"P":   "PT",
	"SRB": "RS",
	"SLO": "SI",
	"E":   "ES",
	"S":   "SE",
}

func readJSONArray[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var arr []T
	dec := json.NewDecoder(f)
	if err := dec.Decode(&arr); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if arr == nil {
		arr = []T{}
	}
	return arr, nil
}

func readJSONArrayFiltered[T any](path string, filter func(T) bool) ([]T, error) {
	arr, err := readJSONArray[T](path)
	if err != nil {
		return nil, err
	}
	if filter == nil {
		return arr, nil
	}
	out := make([]T, 0, len(arr))
	for _, entry := range arr {
		if filter(entry) {
			out = append(out, entry)
		}
	}
	return out, nil
}

func readRawArray(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.UseNumber()
	var raw []map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if raw == nil {
		raw = []map[string]any{}
	}
	return raw, nil
}

func toJSONPath(inputDir, mapName, suffix string) string {
	if strings.HasSuffix(suffix, ".json") {
		return filepath.Join(inputDir, fmt.Sprintf("%s-%s", mapName, suffix))
	}
	return filepath.Join(inputDir, fmt.Sprintf("%s-%s.json", mapName, suffix))
}

func requireDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("expected directory: %s", path)
	}
	return nil
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

func parseSingleMapOption(values []string) (string, error) {
	if len(values) == 0 {
		return "usa", nil
	}
	if len(values) > 1 {
		return "", errors.New("Only one \"map\" option can be specified")
	}
	v := strings.ToLower(strings.TrimSpace(values[0]))
	if v != "usa" && v != "europe" {
		return "", fmt.Errorf("invalid map: %s", values[0])
	}
	return v, nil
}

func parseMultiMapOption(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{"usa"}, nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		v := strings.ToLower(strings.TrimSpace(value))
		if v != "usa" && v != "europe" {
			return nil, fmt.Errorf("invalid map: %s", value)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}

func parseTypeOptions(values []string, defaults []string, allowed map[string]bool) ([]string, error) {
	if len(values) == 0 {
		values = append([]string{}, defaults...)
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		v := strings.ToLower(strings.TrimSpace(value))
		if !allowed[v] {
			return nil, fmt.Errorf("invalid type: %s", value)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}

func parseCoords(raw string) (point2, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return point2{}, errors.New("invalid game coords")
	}
	x, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return point2{}, errors.New("invalid game coords")
	}
	y, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return point2{}, errors.New("invalid game coords")
	}
	return point2{x, y}, nil
}

func buildNodeLookup(nodes []Node) map[string]Node {
	lookup := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		lookup[node.UID.Key()] = node
	}
	return lookup
}

func buildRoadLookLookup(roadLooks []RoadLook) map[string]RoadLook {
	lookup := make(map[string]RoadLook, len(roadLooks))
	for _, rl := range roadLooks {
		lookup[rl.Token] = rl
	}
	return lookup
}

func mapPrefix(mapName string) string {
	if mapName == "europe" {
		return "ets2"
	}
	return "ats"
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	return lines, nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func listPOIFiles(inputDir string) ([]string, error) {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, "-pois.json") {
			files = append(files, filepath.Join(inputDir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

func mustGetString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func mustGetMap(m map[string]any, key string) map[string]any {
	if mm, ok := m[key].(map[string]any); ok {
		return mm
	}
	return nil
}

func mustGetArray(m map[string]any, key string) []any {
	if arr, ok := m[key].([]any); ok {
		return arr
	}
	return nil
}

func toStringSlice(v []any) []string {
	out := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func copyFile(dstPath string, src io.Reader) error {
	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}
