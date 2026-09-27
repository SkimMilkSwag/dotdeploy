package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffEmptyWhenInSync(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".zshrc"), "export PATH=...")
	target := filepath.Join(dst, ".zshrc")
	if err := os.Symlink(filepath.Join(src, ".zshrc"), target); err != nil {
		t.Fatal(err)
	}

	names, err := Diff(src, dst)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("in-sync dirs should diff empty, got %v", names)
	}
}

func TestDiffReportsMissingAndStale(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".zshrc"), "new zsh")
	writeFile(t, filepath.Join(src, ".vimrc"), "set number")

	// .zshrc is missing from dst entirely -> planned as symlink
	// .vimrc exists at dst as a stale symlink to elsewhere -> re-planned
	other := filepath.Join(t.TempDir(), "old")
	if err := os.WriteFile(other, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(dst, ".vimrc")); err != nil {
		t.Fatal(err)
	}

	names, err := Diff(src, dst)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("want both files reported as changed, got %v", names)
	}
	// sorted alphabetically: .vimrc before .zshrc
	if names[0] != ".vimrc" || names[1] != ".zshrc" {
		t.Errorf("names not sorted as expected: %v", names)
	}
}

func TestDiffIgnoresSubdirs(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, ".config", "nested"), "y")

	names, err := Diff(src, dst)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("subdirs should be ignored in v1, got %v", names)
	}
}
