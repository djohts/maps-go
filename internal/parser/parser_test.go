package parser

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindGameArchives(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"base.scs", "def.scs", "dlc_test.scs", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := findGameArchives(tmp, true)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		filepath.Join(tmp, "base.scs"),
		filepath.Join(tmp, "def.scs"),
		filepath.Join(tmp, "dlc_test.scs"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findGameArchives() = %v, want %v", got, want)
	}
}

func TestOrderAndFilterMods(t *testing.T) {
	mods := []string{"/mods/b.scs", "/mods/a.scs", "/mods/c.zip"}
	got := orderAndFilterMods(mods, []string{"a"}, []string{"a", "c"}, []string{"c"})
	want := []string{"/mods/a.scs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderAndFilterMods() = %v, want %v", got, want)
	}
}

func TestRunDryRun(t *testing.T) {
	gameDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(gameDir, "base.scs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")

	var buf bytes.Buffer
	err := Run([]string{"-g", gameDir, "-o", outDir, "--dryRun"}, &buf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "parser-report.json")); !os.IsNotExist(err) {
		t.Fatalf("parser-report.json should not exist on dry-run")
	}
}
