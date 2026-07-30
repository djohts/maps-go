package generator

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestRunMapWritesGeoJSON(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := t.TempDir()

	mustWriteJSON(t, filepath.Join(inputDir, "usa-cities.json"), []map[string]any{
		{
			"token":        "city_1",
			"name":         "Test City",
			"countryToken": "country_1",
			"x":            1000,
			"y":            2000,
			"areas": []map[string]any{
				{"hidden": false, "width": 10, "height": 20},
			},
		},
	})
	mustWriteJSON(t, filepath.Join(inputDir, "usa-countries.json"), []map[string]any{
		{"token": "country_1", "name": "Country One", "code": "US", "x": 1200, "y": 2200},
	})
	mustWriteJSON(t, filepath.Join(inputDir, "usa-nodes.json"), []map[string]any{
		{"uid": "1", "x": 0, "y": 0, "rotation": 0},
		{"uid": "2", "x": 100, "y": 0, "rotation": 0},
	})
	mustWriteJSON(t, filepath.Join(inputDir, "usa-roads.json"), []map[string]any{
		{"uid": "10", "startNodeUid": "1", "endNodeUid": "2", "roadLookToken": "road.local", "dlcGuard": 0, "length": 100, "x": 50, "y": 0},
	})
	mustWriteJSON(t, filepath.Join(inputDir, "usa-roadLooks.json"), []map[string]any{
		{"token": "road.local", "lanesLeft": []any{"lane"}, "lanesRight": []any{"lane"}},
	})

	err := Run([]string{"map", "-i", inputDir, "-o", outputDir, "-t", "geojson"})
	if err != nil {
		t.Fatalf("Run(map) error = %v", err)
	}

	geojsonPath := filepath.Join(outputDir, "ats.geojson")
	data, err := os.ReadFile(geojsonPath)
	if err != nil {
		t.Fatalf("read ats.geojson: %v", err)
	}

	var fc FeatureCollection
	if err := json.Unmarshal(data, &fc); err != nil {
		t.Fatalf("unmarshal ats.geojson: %v", err)
	}
	if len(fc.Features) == 0 {
		t.Fatalf("expected features in ats.geojson")
	}
}

func TestRunSpritesheetWritesFiles(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := t.TempDir()
	iconsDir := filepath.Join(inputDir, "icons")
	if err := os.MkdirAll(iconsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mustWriteJSON(t, filepath.Join(inputDir, "usa-pois.json"), []map[string]any{
		{"type": "company", "icon": "shop", "x": 0, "y": 0},
	})
	writeTestPNG(t, filepath.Join(iconsDir, "shop.png"))

	err := Run([]string{"spritesheet", "-i", inputDir, "-o", outputDir})
	if err != nil {
		t.Fatalf("Run(spritesheet) error = %v", err)
	}

	for _, name := range []string{"sprites.png", "sprites@2x.png", "sprites.json", "sprites@2x.json"} {
		if _, err := os.Stat(filepath.Join(outputDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
}

func TestRunRequiresKnownCommand(t *testing.T) {
	if err := Run([]string{"unknown"}); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func mustWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := generateBuiltinIcon("dot")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
