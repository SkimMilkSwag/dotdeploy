package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates a file with content at path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readLink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Readlink(%s): %v", path, err)
	}
	return target
}

func TestPlanSymlinksMissingFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".zshrc"), "export PATH=...")
	writeFile(t, filepath.Join(src, ".vimrc"), "set number")

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	for _, it := range items {
		if it.Kind != "symlink" {
			t.Errorf("%s: missing file should be symlinked, got %q", it.Name, it.Kind)
		}
	}
}

func TestPlanIgnoresSubdirectories(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".zshrc"), "x")
	if err := os.MkdirAll(filepath.Join(src, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, ".config", "nested"), "y")

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, it := range items {
		if it.Name == ".config" {
			t.Errorf("subdirectory should be skipped in v1, got item %+v", it)
		}
	}
	if len(items) != 1 || items[0].Name != ".zshrc" {
		t.Fatalf("want exactly the .zshrc item, got %+v", items)
	}
}

// Regression: when the src dir is reached through a symlinked path component,
// an existing link created with the resolved spelling must still be detected
// as up to date (EvalSymlinks on both sides), and a new link should be
// planned for files that are missing.
func TestPlanWithSymlinkedSrcDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "dotfiles")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()

	fileReal := filepath.Join(real, ".zshrc")
	writeFile(t, fileReal, "export PATH=...")
	// pre-existing link created with the resolved spelling
	existing := filepath.Join(dst, ".zshrc")
	if err := os.Symlink(fileReal, existing); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(link, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, it := range items {
		if it.Name == ".zshrc" {
			t.Errorf("link created via resolved path should be detected as up to date; got %+v", it)
		}
	}
	if len(items) != 0 {
		t.Fatalf("want no actions, got %+v", items)
	}

	// a missing file is still planned, with the (unresolved) requested src path
	writeFile(t, filepath.Join(real, ".vimrc"), "set number")
	items, err = Plan(link, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 1 || items[0].Name != ".vimrc" {
		t.Fatalf("want only the missing file planned, got %+v", items)
	}
	if err := Execute(items); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, ".vimrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "set number" {
		t.Errorf("readthrough = %q", data)
	}
}

func TestPlanExistingSymlinkPointingAtSourceIsSkipped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	fileSrc := filepath.Join(src, ".gitconfig")
	writeFile(t, fileSrc, "[core]")
	target := filepath.Join(dst, ".gitconfig")
	if err := os.Symlink(fileSrc, target); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("correct link should be skipped, got %+v", items)
	}
}

func TestPlanExistingSymlinkPointingElsewhereNeedsUpdate(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	fileSrc := filepath.Join(src, ".bashrc")
	writeFile(t, fileSrc, "x")
	other := filepath.Join(t.TempDir(), "old")
	if err := os.WriteFile(other, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dst, ".bashrc")
	if err := os.Symlink(other, target); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 1 || items[0].Name != ".bashrc" || items[0].Kind != "symlink" {
		t.Fatalf("stale link should be re-planned as symlink, got %+v", items)
	}
}

func TestPlanExistingRegularFileNeedsCopy(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".profile"), "new content")
	// pre-existing regular file at the destination (user-local edits)
	if err := os.WriteFile(filepath.Join(dst, ".profile"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 1 || items[0].Kind != "copy" {
		t.Fatalf("existing regular file should be planned as copy, got %+v", items)
	}
}

func TestExecuteSymlinkCreatesLink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	fileSrc := filepath.Join(src, ".tmux.conf")
	writeFile(t, fileSrc, "set -g default-terminal xterm-256color")

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := Execute(items); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := readLink(t, filepath.Join(dst, ".tmux.conf"))
	if got != fileSrc {
		t.Errorf("link target = %q, want %q", got, fileSrc)
	}
	// reading through the link returns the source content
	data, err := os.ReadFile(filepath.Join(dst, ".tmux.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "xterm-256color") {
		t.Errorf("readthrough = %q", data)
	}
}

func TestExecuteCopyOverwritesExistingFile(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".bash_profile"), "new profile")
	if err := os.WriteFile(filepath.Join(dst, ".bash_profile"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := Execute(items); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, ".bash_profile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new profile" {
		t.Errorf("dst content = %q, want updated copy", data)
	}
	// destination must be a regular file, not a symlink
	info, err := os.Lstat(filepath.Join(dst, ".bash_profile"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("copy mode should produce a regular file")
	}
}

func TestExecuteIdempotentWhenAlreadyLinked(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	fileSrc := filepath.Join(src, ".gitignore_global")
	writeFile(t, fileSrc, "*.swp")
	target := filepath.Join(dst, ".gitignore_global")
	if err := os.Symlink(fileSrc, target); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(src, dst, "symlink", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := Execute(items); err != nil {
		t.Fatalf("Execute on empty plan: %v", err)
	}
	got := readLink(t, target)
	if got != fileSrc {
		t.Errorf("link changed to %q", got)
	}
}

func TestLinkErrorFormatting(t *testing.T) {
	le := &LinkError{Op: "symlink", Path: "/home/u/.zshrc", Err: os.ErrNotExist}
	want := "symlink /home/u/.zshrc: file does not exist"
	if le.Error() != want {
		t.Errorf("Error() = %q, want %q", le.Error(), want)
	}
}

func TestPlanRecursiveMissingDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".config/nvim/init.lua"), "local ok = true")

	items, err := Plan(src, dst, "symlink", true)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 1 || !items[0].Dir || items[0].Kind != "symlink" {
		t.Fatalf("want one symlink dir item for .config, got %+v", items)
	}

	if err := Execute(items); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := readLink(t, filepath.Join(dst, ".config"))
	if got != filepath.Join(src, ".config") {
		t.Errorf("link target = %q, want %q", got, filepath.Join(src, ".config"))
	}
	data, err := os.ReadFile(filepath.Join(dst, ".config/nvim/init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local ok = true" {
		t.Errorf("readthrough = %q", data)
	}

	// idempotent: a second Plan sees the tree is already linked
	items, err = Plan(src, dst, "symlink", true)
	if err != nil {
		t.Fatalf("Plan (second): %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("already-linked dir should be skipped, got %+v", items)
	}
}

func TestPlanRecursiveCopyModeCopiesTree(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".config/ok"), "nested file")
	// nested symlink inside the tree: copy mode should materialize its target
	realTarget := filepath.Join(t.TempDir(), "shared.conf")
	writeFile(t, realTarget, "shared content")
	if err := os.Symlink(realTarget, filepath.Join(src, ".config/link")); err != nil {
		t.Fatal(err)
	}

	items, err := Plan(src, dst, "copy", true)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(items) != 1 || !items[0].Dir || items[0].Kind != "copy" {
		t.Fatalf("want one copy dir item, got %+v", items)
	}
	if err := Execute(items); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Lstat(filepath.Join(dst, ".config"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("copy mode should produce a real directory, not a symlink")
	}
	data, err := os.ReadFile(filepath.Join(dst, ".config/ok"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nested file" {
		t.Errorf("nested file = %q", data)
	}
	// the nested symlink must have been replaced by a regular file with the target's content
	linkInfo, err := os.Lstat(filepath.Join(dst, ".config/link"))
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		t.Error("nested symlink should be materialized as a regular file")
	}
	data, err = os.ReadFile(filepath.Join(dst, ".config/link"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "shared content" {
		t.Errorf("materialized link = %q", data)
	}

	// second run: dst is now a directory that matches src content-wise -> copy planned again
	// (copy mode always overwrites regular files; that is expected, not an error)
	items, err = Plan(src, dst, "copy", true)
	if err != nil {
		t.Fatalf("Plan (second): %v", err)
	}
	if len(items) != 1 || !items[0].Dir || items[0].Kind != "copy" {
		t.Fatalf("directory at dst should be re-planned as copy, got %+v", items)
	}
}

func TestPlanRecursiveFileInTheWayOfDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, ".config/nested"), "y")
	if err := os.WriteFile(filepath.Join(dst, ".config"), []byte("a file, not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Plan(src, dst, "symlink", true)
	if err == nil {
		t.Fatal("want error when a regular file blocks the destination directory")
	}
	if !strings.Contains(err.Error(), "file in the way") {
		t.Errorf("error = %q, want mention of file in the way", err)
	}
}

func TestPrintPlanOutput(t *testing.T) {
	var sb strings.Builder
	items := []PlanItem{
		{Name: ".zshrc", Src: "/a/.zshrc", Dst: "/h/.zshrc", Kind: "symlink"},
		{Name: ".profile", Src: "/a/.profile", Dst: "/h/.profile", Kind: "copy"},
	}
	PrintPlan(&sb, items)
	out := sb.String()
	if !strings.Contains(out, "symlink") || !strings.Contains(out, "copy") {
		t.Errorf("plan output missing kinds: %q", out)
	}
	var empty strings.Builder
	PrintPlan(&empty, nil)
	if got := empty.String(); got != "up to date\n" {
		t.Errorf("empty plan = %q", got)
	}
	_ = io.Discard
}
