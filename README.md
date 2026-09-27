# dotdeploy

A tiny Go CLI that deploys your dotfiles from a source directory to a
destination (usually `$HOME`) using **symlinks** or **plain copies**.

Instead of maintaining a pile of shell functions and `ln -s` one-liners,
point dotdeploy at a directory full of dotfiles and it will:

- create symlinks for files that don't exist yet,
- re-point symlinks that target something stale,
- copy over existing regular files (your local edits get replaced — use
  symlink mode if you edit in place),
- skip files that are already correctly linked.

## Install

```bash
go install github.com/SkimMilkSwag/dotdeploy/cmd/dotdeploy@latest
```

or build from a checkout:

```bash
make build   # or: go build -o bin/dotdeploy ./cmd/dotdeploy
```

Requires Go 1.24+. No runtime dependencies beyond the standard library.

## Usage

```bash
# Deploy everything in ~/.dotfiles into $HOME as symlinks (default mode)
dotdeploy -src ~/.dotfiles ~

# Same thing, but show what would change without touching anything
dotdeploy -src ~/.dotfiles ~ --dry-run

# Copy files instead of linking (good for read-only home dirs or CI images)
dotdeploy -mode copy -src ~/.dotfiles ~/target
```

With no `dst-dir` argument the source directory is treated as its own deploy
target, which is handy for checking an already-deployed tree for drift:

```bash
dotdeploy -src ~/  # plan against itself; prints "up to date" when clean
```

### Flags

| flag      | default     | description                                          |
|-----------|-------------|------------------------------------------------------|
| `-mode`   | `symlink`   | link strategy: `symlink` or `copy`                   |
| `-dry-run`| `false`     | print the plan and exit without modifying anything   |
| `-src`    | `~/.dotfiles` | source directory containing the dotfiles          |

## Behavior notes

- **Flat layout only (v1).** Subdirectories inside the source are ignored;
  nested layouts like `.config/nvim` come in a later release.
- A destination entry that is a **symlink pointing at the source file** is
  considered up to date and skipped.
- A symlink pointing somewhere else gets re-pointed (removed and recreated).
- A **regular file** at the destination is always planned as a copy (it is
  overwritten on execute). If you want that file symlinked instead, remove it
  once and let the next run link it — or run with `-dry-run` to preview.
- File permissions from the source are preserved for copies.

## Development

```bash
go test ./...    # unit tests (stdlib `testing`, temp dirs)
go vet ./...
```
