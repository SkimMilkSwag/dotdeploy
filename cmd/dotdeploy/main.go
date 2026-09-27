package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PlanItem is a single planned file action.
type PlanItem struct {
	Name string // dotfile name, e.g. ".zshrc"
	Src  string // absolute source path
	Dst  string // absolute destination path (or existing link)
	Kind string // "copy" or "symlink"
}

// LinkError describes one failed action with its underlying cause.
type LinkError struct {
	Op   string
	Path string
	Err  error
}

func (e *LinkError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Op, e.Path, e.Err)
}

// Plan computes the set of actions needed to bring dstDir into sync with the
// dotfiles in srcDir. Existing links that already point at the right target
// are reported but do not need an action.
func Plan(srcDir, dstDir string) ([]PlanItem, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}
	var items []PlanItem
	for _, e := range entries {
		if e.IsDir() {
			continue // flat layout only in v1
		}
		name := e.Name()
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)

		if info, err := os.Lstat(dst); err == nil {
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, terr := os.Readlink(dst)
				if terr != nil {
					return nil, terr
				}
				resolvedSrc, serr := filepath.EvalSymlinks(src)
				if serr != nil {
					return nil, serr
				}
				resolvedTarget, err2 := filepath.EvalSymlinks(target)
				if err2 != nil {
					resolvedTarget = target // broken link: compare as-is
				}
				if resolvedTarget == resolvedSrc {
					continue // already linked
				}
				items = append(items, PlanItem{Name: name, Src: src, Dst: dst, Kind: "symlink"})
			case info.Mode().IsRegular():
				items = append(items, PlanItem{Name: name, Src: src, Dst: dst, Kind: "copy"})
			default:
				return nil, fmt.Errorf("%s: unexpected entry type %v", dst, info.Mode())
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		} else {
			items = append(items, PlanItem{Name: name, Src: src, Dst: dst, Kind: "symlink"})
		}
	}
	return items, nil
}

// Execute applies the planned actions. In symlink mode any existing file or
// link at the destination is removed before creating the new one.
func Execute(items []PlanItem) error {
	for _, it := range items {
		var err error
		switch it.Kind {
		case "copy":
			err = copyFile(it.Src, it.Dst)
		case "symlink":
			if _, lerr := os.Lstat(it.Dst); lerr == nil {
				if rerr := os.Remove(it.Dst); rerr != nil {
					return &LinkError{Op: "remove", Path: it.Dst, Err: rerr}
				}
			} else if !os.IsNotExist(lerr) {
				return &LinkError{Op: "stat", Path: it.Dst, Err: lerr}
			}
			err = os.Symlink(it.Src, it.Dst)
		default:
			err = fmt.Errorf("unknown kind %q", it.Kind)
		}
		if err != nil {
			return &LinkError{Op: it.Kind, Path: it.Dst, Err: err}
		}
	}
	return nil
}

// copyFile copies src to dst, preserving the source file mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ValidateDestDir checks that dst exists and is a directory.
func ValidateDestDir(dst string) error {
	info, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: not a directory", dst)
	}
	return nil
}

// PrintPlan writes a human-readable plan to w.
func PrintPlan(w io.Writer, items []PlanItem) {
	for _, it := range items {
		fmt.Fprintf(w, "%-8s %-24s -> %s\n", it.Kind, it.Name, it.Dst)
	}
	if len(items) == 0 {
		fmt.Fprintln(w, "up to date")
	}
}

func run(args []string) int {
	fs := flag.NewFlagSet("dotdeploy", flag.ContinueOnError)
	mode := fs.String("mode", "symlink", `link strategy: "symlink" or "copy"`)
	dry := fs.Bool("dry-run", false, "print the plan without touching anything")
	srcDir := fs.String("src", "", "directory containing dotfiles (default: $HOME/.dotfiles)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `usage: dotdeploy [flags] [dst-dir]

Deploy dotfiles from a source directory to a destination directory.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *srcDir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			fmt.Fprintln(os.Stderr, "dotdeploy: cannot resolve home dir:", herr)
			return 2
		}
		*srcDir = filepath.Join(home, ".dotfiles")
	}
	srcDirAbs, err := filepath.Abs(*srcDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dotdeploy:", err)
		return 2
	}

	dst := fs.Arg(0)
	if dst == "" {
		dst = *srcDir // no-arg form: src is already the deploy target (in-place check)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dotdeploy:", err)
		return 2
	}

	switch *mode {
	case "symlink", "copy":
	default:
		fmt.Fprintf(os.Stderr, "dotdeploy: unknown mode %q (want symlink or copy)\n", *mode)
		return 2
	}

	items, err := Plan(srcDirAbs, dstAbs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dotdeploy:", err)
		return 1
	}
	if *dry {
		PrintPlan(os.Stdout, items)
		return 0
	}
	if err := Execute(items); err != nil {
		fmt.Fprintln(os.Stderr, "dotdeploy:", err)
		return 1
	}
	if strings.TrimSpace(*mode) == "copy" && len(items) > 0 {
		fmt.Fprintf(os.Stdout, "deployed %d file(s)\n", len(items))
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:]))
}
