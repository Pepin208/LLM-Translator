package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFilesAcceptsSSA(t *testing.T) {
	dir := t.TempDir()
	ssa := filepath.Join(dir, "episode01.ssa")
	if err := os.WriteFile(ssa, []byte("[Events]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A single .ssa path is accepted.
	got, err := resolveFiles(ssa)
	if err != nil {
		t.Fatalf("single .ssa rejected: %v", err)
	}
	if len(got) != 1 || got[0] != ssa {
		t.Errorf("single = %v, want [%s]", got, ssa)
	}

	// Directory walking picks up the .ssa and ignores the .txt.
	found, err := resolveFiles(dir)
	if err != nil {
		t.Fatalf("dir walk: %v", err)
	}
	if len(found) != 1 || filepath.Base(found[0]) != "episode01.ssa" {
		t.Errorf("walk = %v, want only episode01.ssa", found)
	}
}
