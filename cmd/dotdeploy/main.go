package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PlanItem is a single planned file action.
type PlanItem struct {
	Name string // dotfile name, e.g. ".zshrc"
	Src  string // absolute source path
	Dst  string // absolute destination path (or existing link)
	Kind string // "copy" or "symlink"
	Dir  bool   // true when the item is a directory tree
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
// are reported but do not need an action. With recursive enabled, subdirectories
// under srcDir are planned as single items (see copyTree/symlinkTree) instead
// of being skipped. mode selects the link strategy for new entries: "symlink"
// or "copy".
func Plan(srcDir, dstDir string, mode string, recursive bool) ([]PlanItem, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}
	var items []PlanItem
	for _, e := range entries {
		name := e.Name()
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)

		if e.IsDir() {
			if !recursive {
				continue // flat layout when -recursive is not set
			}
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
					items = append(items, PlanItem{Name: name, Src: src, Dst: dst, Kind: "symlink", Dir: true})
				case info.IsDir():
					items = append(items, PlanItem{Name: name, Src: src, Dst: dst, Kind: "copy", Dir: true})
				default:
					return nil, fmt.Errorf("%s: file in the way of directory %s", dst, name)
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			} else {
				items = append(items, PlanItem{Name: name, Src: src, Dst: dst, Kind: mode, Dir: true})
			}
			continue
		}

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

// ExecuteOptions controls how Execute applies the planned actions.
type ExecuteOptions struct {
	// BackupDir, when non-empty, receives a timestamped subdirectory that
	// existing destination entries are moved into before being overwritten
	// or re-linked. Empty disables backups (entries are removed as before).
	BackupDir string
}

// Execute applies the planned actions. In symlink mode any existing file or
// link at the destination is removed before creating the new one; with
// BackupDir set, the displaced entry is moved into a timestamped backup
// directory instead of being deleted.
func Execute(items []PlanItem, opts ExecuteOptions) error {
	var backupSub string
	for i, it := range items {
		if i == 0 && opts.BackupDir != "" {
			sub, err := newBackupSub(opts.BackupDir)
			if err != nil {
				return err
			}
			backupSub = sub
		}
		var err error
		switch it.Kind {
		case "copy":
			if backupSub != "" && it.Name != "" {
				// a regular file at dst is about to be overwritten: preserve it
				if _, lerr := os.Lstat(it.Dst); lerr == nil {
					if berr := moveBackup(backupSub, it.Dst, it.Name); berr != nil {
						return berr
					}
				} else if !os.IsNotExist(lerr) {
					return &LinkError{Op: "stat", Path: it.Dst, Err: lerr}
				}
			}
			if it.Dir {
				err = copyTree(it.Src, it.Dst)
			} else {
				err = copyFile(it.Src, it.Dst)
			}
		case "symlink":
			if _, lerr := os.Lstat(it.Dst); lerr == nil {
				if backupSub != "" && it.Name != "" {
					if berr := moveBackup(backupSub, it.Dst, it.Name); berr != nil {
						return berr
					}
				} else if rerr := os.Remove(it.Dst); rerr != nil {
					return &LinkError{Op: "remove", Path: it.Dst, Err: rerr}
				}
			} else if !os.IsNotExist(lerr) {
				return &LinkError{Op: "stat", Path: it.Dst, Err: lerr}
			}
			if it.Dir {
				err = symlinkTree(it.Src, it.Dst)
			} else {
				err = os.Symlink(it.Src, it.Dst)
			}
		default:
			err = fmt.Errorf("unknown kind %q", it.Kind)
		}
		if err != nil {
			return &LinkError{Op: it.Kind, Path: it.Dst, Err: err}
		}
	}
	return nil
}

// newBackupSub creates (and returns) a timestamped subdirectory under
// backupDir for holding displaced entries. The name includes the process ID
// so back-to-back runs in the same second don't collide.
func newBackupSub(backupDir string) (string, error) {
	sub := filepath.Join(backupDir, fmt.Sprintf("dotdeploy-%s", time.Now().Format("20060102-150405"))+"-"+strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", &LinkError{Op: "makedirs", Path: sub, Err: err}
	}
	return sub, nil
}

// moveBackup moves the destination entry at dst (a file or a symlink) into
// backupSub under name.
func moveBackup(backupSub, dst, name string) error {
	target := filepath.Join(backupSub, name)
	if err := os.Rename(dst, target); err != nil {
		return &LinkError{Op: "backup", Path: dst, Err: err}
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

// copyTree recursively copies the directory tree src to dst, creating dst if
// it does not exist. Existing files under dst are overwritten; nested
// symlinks encountered in src are replaced with regular copies of their
// targets so the deployed tree is self-contained.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		info, err := os.Lstat(s)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, rerr := filepath.EvalSymlinks(s)
			if rerr != nil {
				return &LinkError{Op: "copy", Path: s, Err: rerr}
			}
			if err := copyFile(resolved, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

// symlinkTree replaces dst with a single symlink pointing at the src directory.
func symlinkTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(src, dst)
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

// Diff compares srcDir and dstDir and reports which dotfiles differ between
// the two. A dotfile counts as "different" when it is missing from dstDir,
// or when the destination content does not match the source content (for
// symlinks, the link target must resolve to the source file). Returns names
// sorted alphabetically. With recursive enabled, subdirectories are included
// in the comparison. mode selects the link strategy for new entries.
func Diff(srcDir, dstDir string, mode string, recursive bool) ([]string, error) {
	items, err := Plan(srcDir, dstDir, mode, recursive)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	sort.Strings(names)
	return names, nil
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
	diffOnly := fs.Bool("diff", false, "list only the dotfiles that would change, one per line")
	recursive := fs.Bool("recursive", false, "deploy subdirectories (e.g. .config/nvim) as whole trees, not just flat files")
	backups := fs.String("backup", "", "move displaced destination entries into a timestamped dir under this path instead of deleting them")
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

	items, err := Plan(srcDirAbs, dstAbs, *mode, *recursive)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dotdeploy:", err)
		return 1
	}
	if *diffOnly {
		names, err := Diff(srcDirAbs, dstAbs, *mode, *recursive)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dotdeploy:", err)
			return 1
		}
		for _, n := range names {
			fmt.Fprintln(os.Stdout, n)
		}
		if len(names) == 0 {
			fmt.Fprintln(os.Stdout, "up to date")
		}
		return 0
	}
	if *dry {
		PrintPlan(os.Stdout, items)
		return 0
	}
	if err := Execute(items, ExecuteOptions{BackupDir: *backups}); err != nil {
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
