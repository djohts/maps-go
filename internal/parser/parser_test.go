package parser

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveModLoadOrderAppliesDependencies(t *testing.T) {
	mods := []IndexedMod{
		{ArchivePath: "/mods/a.scs", FileStem: "a", CanonicalName: "a", DisplayName: "A"},
		{ArchivePath: "/mods/b.scs", FileStem: "b", CanonicalName: "b", DisplayName: "B", Dependencies: []string{"a"}},
		{ArchivePath: "/mods/c.scs", FileStem: "c", CanonicalName: "c", DisplayName: "C"},
	}

	result, err := resolveModLoadOrder(mods, ResolveModsOptions{ExplicitOrder: []string{"b", "a", "c"}})
	if err != nil {
		t.Fatalf("resolveModLoadOrder() error = %v", err)
	}

	got := []string{result.OrderedMods[0].CanonicalName, result.OrderedMods[1].CanonicalName, result.OrderedMods[2].CanonicalName}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered mods = %v, want %v", got, want)
	}
}

func TestRunWritesMapDataFiles(t *testing.T) {
	gameDir := t.TempDir()
	outputDir := t.TempDir()

	mustWriteZip(t, filepath.Join(gameDir, "version.scs"), map[string]string{
		"version.sii": `SiiNunit\n{\nversion_data : .version {\napplication: "ats"\n}\n}`,
	})
	mustWriteZip(t, filepath.Join(gameDir, "def.scs"), map[string]string{
		"def/country/test_country.sii": `SiiNunit\n{\ncountry_data : test_country {\ncountry_name: "Test Country"\ncountry_code: "TC"\nmap_x: 123\nmap_z: 456\n}\n}`,
		"def/city/test_city.sii":       `SiiNunit\n{\ncity_data : test_city {\ncity_name: "Test City"\ncountry: .country.test_country\nmap_x: 120\nmap_z: 450\n}\n}`,
		"def/company/test_company.sii": `SiiNunit\n{\ncompany_data : test_company {\nname: "Test Company"\ncity[]: .city.test_city\n}\n}`,
	})
	// non-critical required archive names present but empty.
	for _, name := range []string{"base.scs", "base_map.scs", "base_share.scs", "core.scs", "locale.scs"} {
		mustWriteZip(t, filepath.Join(gameDir, name), map[string]string{})
	}

	var buf bytes.Buffer
	err := Run([]string{"-g", gameDir, "-o", outputDir}, &buf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, key := range mapDataKeys {
		path := filepath.Join(outputDir, "usa-"+key+".json")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected output file %s: %v", path, err)
		}
	}
}

func TestRunDryRunDoesNotWriteOutput(t *testing.T) {
	gameDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "out")
	mustWriteZip(t, filepath.Join(gameDir, "version.scs"), map[string]string{
		"version.sii": `SiiNunit\n{\nversion_data : .version {\napplication: "ats"\n}\n}`,
	})
	mustWriteZip(t, filepath.Join(gameDir, "def.scs"), map[string]string{})

	var buf bytes.Buffer
	err := Run([]string{"-g", gameDir, "-o", outputDir, "--dryRun"}, &buf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "usa-cities.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no output files on dryRun")
	}
}

func TestRunWritesEmptyCollections(t *testing.T) {
	gameDir := t.TempDir()
	outputDir := t.TempDir()

	mustWriteZip(t, filepath.Join(gameDir, "version.scs"), map[string]string{
		"version.sii": `SiiNunit\n{\nversion_data : .version {\napplication: "ats"\n}\n}`,
	})
	mustWriteZip(t, filepath.Join(gameDir, "def.scs"), map[string]string{})
	for _, name := range []string{"base.scs", "base_map.scs", "base_share.scs", "core.scs", "locale.scs"} {
		mustWriteZip(t, filepath.Join(gameDir, name), map[string]string{})
	}

	var buf bytes.Buffer
	err := Run([]string{"-g", gameDir, "-o", outputDir}, &buf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, key := range mapDataKeys {
		path := filepath.Join(outputDir, "usa-"+key+".json")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected output file %s: %v", path, err)
		}
	}
}

func mustWriteZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
