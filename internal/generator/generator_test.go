package generator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunWritesCommandJSON(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "out")

	err := Run([]string{"map", "-i", tmp, "-o", out, "-m", "usa", "-m", "europe"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "map.json")); err != nil {
		t.Fatalf("expected output file: %v", err)
	}
}

func TestRunRequiresKnownCommand(t *testing.T) {
	if err := Run([]string{"unknown"}); err == nil {
		t.Fatal("expected error for unknown command")
	}
}
