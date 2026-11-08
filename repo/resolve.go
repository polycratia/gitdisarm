// Package repo settles where a repository that arrived as files keeps its git
// directory, and which hooks a git run inside it would execute. Pointer files
// are read as data: one that leads out of the checkout refuses the repository
// instead of being followed.
package repo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/polycratia/gitdisarm/gitconfig"
)

// ErrEscapesCheckout reports that a pointer file resolved outside the checkout
// it belongs to.
var ErrEscapesCheckout = errors.New("escapes the checkout")

// EscapeError names the pointer that left the checkout and where it led.
type EscapeError struct {
	// Pointer is the file that named the path: the .git directory, symlink or
	// file itself, or one of the pointer files inside the git directory.
	Pointer string
	// Field is what the pointer stands for: "gitdir", "commondir" or
	// "worktree".
	Field string
	// Path is where it resolved to, after symlinks when those are what left.
	Path string
	// Root is the checkout it had to stay inside.
	Root string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%s: %s %s %s at %s", e.Pointer, e.Field, e.Path, ErrEscapesCheckout, e.Root)
}

func (e *EscapeError) Unwrap() error { return ErrEscapesCheckout }

// Layout is the resolved shape of one checkout, every path absolute and with
// its symlinks resolved.
type Layout struct {
	// Root is the checkout root.
	Root string
	// GitDir is the git directory .git led to.
	GitDir string
	// CommonDir is the directory refs, objects and hooks are shared from,
	// equal to GitDir unless a commondir pointer says otherwise.
	CommonDir string
	// Worktree is the top of the work tree, the checkout root unless a
	// worktree pointer says otherwise.
	Worktree string
}

// Resolve reads the pointers of the checkout at root: .git as a directory, as
// a symlink, or as a file carrying a gitdir: line taken relative to the
// directory holding it, then the commondir and gitdir pointers a linked
// worktree leaves in the git directory, each taken relative to the git
// directory. A path that leaves the checkout yields an *EscapeError naming the
// pointer file; the question is asked lexically and again after
// filepath.EvalSymlinks.
func Resolve(root string) (*Layout, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	l := &Layout{Root: real}

	dot := filepath.Join(real, ".git")
	info, err := os.Stat(dot)
	if err != nil {
		return nil, err
	}
	pointed := dot
	if !info.IsDir() {
		target, err := readGitdirFile(dot)
		if err != nil {
			return nil, err
		}
		pointed = against(real, target)
	}
	if l.GitDir, err = l.inside(dot, "gitdir", pointed); err != nil {
		return nil, err
	}

	l.CommonDir = l.GitDir
	common := filepath.Join(l.GitDir, "commondir")
	if path, ok, err := readPointerFile(common); err != nil {
		return nil, err
	} else if ok {
		if l.CommonDir, err = l.inside(common, "commondir", against(l.GitDir, path)); err != nil {
			return nil, err
		}
	}

	l.Worktree = real
	back := filepath.Join(l.GitDir, "gitdir")
	if path, ok, err := readPointerFile(back); err != nil {
		return nil, err
	} else if ok {
		// The pointer names the .git file of the linked worktree; the work tree
		// is the directory holding it.
		if l.Worktree, err = l.inside(back, "worktree", filepath.Dir(against(l.GitDir, path))); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// Hooks is the directory git would take its hooks from, and what sits in it.
type Hooks struct {
	Dir string
	// FromConfig is the core.hooksPath entry that chose the directory, nil
	// when git would take its default.
	FromConfig *gitconfig.Entry
	// Outside reports that Dir lies outside the checkout, which the
	// configuration may ask for where a pointer file may not.
	Outside bool
	// Executables are the names of the executable regular files in Dir, in the
	// order the directory lists them.
	Executables []string
}

// Hooks answers which directory the given entries make effective —
// core.hooksPath when one is set, relative to the top of the work tree, and
// the common directory's hooks otherwise — and lists every executable regular
// file in it. A directory that is absent, or is no directory at all, runs
// nothing.
func (l *Layout) Hooks(entries []gitconfig.Entry) (*Hooks, error) {
	h := &Hooks{Dir: filepath.Join(l.CommonDir, "hooks")}
	for i := range entries {
		e := &entries[i]
		if e.Section == "core" && e.Subsection == "" && e.Key == "hookspath" && e.Value != "" {
			h.FromConfig = e
			h.Dir = against(l.Worktree, e.Value)
		}
	}
	h.Outside = !within(l.Root, h.Dir) || !within(l.Root, realExisting(h.Dir))
	names, err := executables(h.Dir)
	if err != nil {
		return nil, err
	}
	h.Executables = names
	return h, nil
}

func executables(dir string) ([]string, error) {
	st, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range ents {
		// Stat, not the entry's own type: a symlink to an executable file is
		// one more program git would start.
		info, err := os.Stat(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			names = append(names, ent.Name())
		}
	}
	return names, nil
}

// inside answers the resolved path a pointer named, or refuses it: lexically
// first, so a path that never existed is still refused, and again once
// symlinks have been followed as far as the filesystem allows.
func (l *Layout) inside(pointer, field, path string) (string, error) {
	clean := filepath.Clean(path)
	if !within(l.Root, clean) {
		return "", &EscapeError{Pointer: pointer, Field: field, Path: clean, Root: l.Root}
	}
	real := realExisting(clean)
	if !within(l.Root, real) {
		return "", &EscapeError{Pointer: pointer, Field: field, Path: real, Root: l.Root}
	}
	return real, nil
}

// readGitdirFile reads the gitdir: line a .git file carries.
func readGitdirFile(file string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	path, ok := strings.CutPrefix(firstLine(string(b)), "gitdir:")
	if !ok {
		return "", fmt.Errorf("%s: is a file and carries no gitdir: line", file)
	}
	if path = strings.TrimSpace(path); path == "" {
		return "", fmt.Errorf("%s: its gitdir: line names no path", file)
	}
	return path, nil
}

// readPointerFile reads a pointer file holding nothing but a path, reporting
// whether it is there at all.
func readPointerFile(file string) (string, bool, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	path := firstLine(string(b))
	if path == "" {
		return "", false, fmt.Errorf("%s: names no path", file)
	}
	return path, true, nil
}

func firstLine(s string) string {
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[:nl]
	}
	return strings.TrimSpace(strings.TrimSuffix(s, "\r"))
}

// against resolves a pointer's path the way git does: an absolute path stands
// on its own, a relative one is taken against the directory git takes it
// against.
func against(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// realExisting follows the symlinks of the longest part of a path that exists
// and keeps the rest as it was given.
func realExisting(path string) string {
	clean := filepath.Clean(path)
	rest := ""
	for p := clean; ; {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return clean
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
