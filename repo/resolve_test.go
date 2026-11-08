package repo_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/polycratia/gitdisarm/gitconfig"
	"github.com/polycratia/gitdisarm/repo"
)

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return real
}

func mkdirAll(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	return path
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("Symlink(%q, %q) error = %v", target, path, err)
	}
}

func script(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	writeFile(t, path, "#!/bin/sh\nexit 0\n", mode)
}

func parseConfig(t *testing.T, content string) []gitconfig.Entry {
	t.Helper()
	entries, err := gitconfig.Parse(strings.NewReader(content), "config")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return entries
}

func TestResolveReadsEveryPointerLayout(t *testing.T) {
	cases := []struct {
		name      string
		build     func(t *testing.T, root string)
		gitDir    string
		commonDir string
		worktree  string
	}{
		{
			name:      ".git is a directory",
			build:     func(t *testing.T, root string) { mkdirAll(t, filepath.Join(root, ".git")) },
			gitDir:    ".git",
			commonDir: ".git",
			worktree:  ".",
		},
		{
			name: ".git is a file with a relative gitdir line",
			build: func(t *testing.T, root string) {
				mkdirAll(t, filepath.Join(root, "store"))
				writeFile(t, filepath.Join(root, ".git"), "gitdir: ./store\n", 0o600)
			},
			gitDir:    "store",
			commonDir: "store",
			worktree:  ".",
		},
		{
			name: ".git is a file with an absolute gitdir line",
			build: func(t *testing.T, root string) {
				store := mkdirAll(t, filepath.Join(root, "store"))
				writeFile(t, filepath.Join(root, ".git"), "gitdir: "+store+"\n", 0o600)
			},
			gitDir:    "store",
			commonDir: "store",
			worktree:  ".",
		},
		{
			name: ".git is a symlink to a directory in the checkout",
			build: func(t *testing.T, root string) {
				mkdirAll(t, filepath.Join(root, "real.git"))
				symlink(t, "real.git", filepath.Join(root, ".git"))
			},
			gitDir:    "real.git",
			commonDir: "real.git",
			worktree:  ".",
		},
		{
			name: "a linked worktree points at its common directory and back at its work tree",
			build: func(t *testing.T, root string) {
				wt := mkdirAll(t, filepath.Join(root, "store", "worktrees", "w1"))
				writeFile(t, filepath.Join(root, ".git"), "gitdir: store/worktrees/w1\n", 0o600)
				writeFile(t, filepath.Join(wt, "commondir"), "../..\n", 0o600)
				writeFile(t, filepath.Join(wt, "gitdir"), filepath.Join(root, ".git")+"\n", 0o600)
			},
			gitDir:    filepath.Join("store", "worktrees", "w1"),
			commonDir: "store",
			worktree:  ".",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			c.build(t, root)
			real := realPath(t, root)

			l, err := repo.Resolve(root)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			want := repo.Layout{
				Root:      real,
				GitDir:    filepath.Join(real, c.gitDir),
				CommonDir: filepath.Join(real, c.commonDir),
				Worktree:  filepath.Join(real, c.worktree),
			}
			if !reflect.DeepEqual(*l, want) {
				t.Errorf("Resolve() = %+v, want %+v", *l, want)
			}
		})
	}
}

func TestResolveRefusesPointersThatLeaveTheCheckout(t *testing.T) {
	cases := []struct {
		name    string
		build   func(t *testing.T, root, outside string)
		pointer string
		field   string
	}{
		{
			name: "a gitdir line above the checkout",
			build: func(t *testing.T, root, outside string) {
				writeFile(t, filepath.Join(root, ".git"), "gitdir: ../elsewhere\n", 0o600)
			},
			pointer: ".git",
			field:   "gitdir",
		},
		{
			name: "an absolute gitdir line",
			build: func(t *testing.T, root, outside string) {
				writeFile(t, filepath.Join(root, ".git"), "gitdir: "+outside+"\n", 0o600)
			},
			pointer: ".git",
			field:   "gitdir",
		},
		{
			name: ".git symlinked out of the checkout",
			build: func(t *testing.T, root, outside string) {
				symlink(t, outside, filepath.Join(root, ".git"))
			},
			pointer: ".git",
			field:   "gitdir",
		},
		{
			name: "a commondir above the checkout",
			build: func(t *testing.T, root, outside string) {
				git := mkdirAll(t, filepath.Join(root, ".git"))
				writeFile(t, filepath.Join(git, "commondir"), "../../elsewhere\n", 0o600)
			},
			pointer: filepath.Join(".git", "commondir"),
			field:   "commondir",
		},
		{
			name: "a commondir symlinked out of the checkout",
			build: func(t *testing.T, root, outside string) {
				git := mkdirAll(t, filepath.Join(root, ".git"))
				symlink(t, outside, filepath.Join(git, "common"))
				writeFile(t, filepath.Join(git, "commondir"), "common\n", 0o600)
			},
			pointer: filepath.Join(".git", "commondir"),
			field:   "commondir",
		},
		{
			name: "a worktree pointer out of the checkout",
			build: func(t *testing.T, root, outside string) {
				git := mkdirAll(t, filepath.Join(root, ".git"))
				writeFile(t, filepath.Join(outside, ".git"), "gitdir: x\n", 0o600)
				writeFile(t, filepath.Join(git, "gitdir"), filepath.Join(outside, ".git")+"\n", 0o600)
			},
			pointer: filepath.Join(".git", "gitdir"),
			field:   "worktree",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			c.build(t, root, outside)
			real := realPath(t, root)

			l, err := repo.Resolve(root)
			if err == nil {
				t.Fatalf("Resolve() = %+v, want a refusal", l)
			}
			if !errors.Is(err, repo.ErrEscapesCheckout) {
				t.Fatalf("Resolve() error = %v, want it to wrap ErrEscapesCheckout", err)
			}
			var esc *repo.EscapeError
			if !errors.As(err, &esc) {
				t.Fatalf("Resolve() error = %v, want *EscapeError", err)
			}
			if want := filepath.Join(real, c.pointer); esc.Pointer != want {
				t.Errorf("EscapeError names %q, want the pointer file %q", esc.Pointer, want)
			}
			if esc.Field != c.field {
				t.Errorf("EscapeError field = %q, want %q", esc.Field, c.field)
			}
			if esc.Root != real {
				t.Errorf("EscapeError root = %q, want %q", esc.Root, real)
			}
			if !strings.Contains(esc.Error(), esc.Pointer) {
				t.Errorf("EscapeError message %q does not name the pointer file", esc.Error())
			}
		})
	}
}

func TestResolveRefusesALayoutItCannotRead(t *testing.T) {
	t.Run("no .git at all", func(t *testing.T) {
		if l, err := repo.Resolve(t.TempDir()); err == nil {
			t.Fatalf("Resolve() = %+v, want an error", l)
		}
	})

	t.Run(".git carries no gitdir line", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".git"), "ref: refs/heads/main\n", 0o600)

		_, err := repo.Resolve(root)
		if err == nil {
			t.Fatal("Resolve() accepted a .git file with no gitdir: line")
		}
		if errors.Is(err, repo.ErrEscapesCheckout) {
			t.Errorf("Resolve() error = %v, want a malformed pointer rather than an escape", err)
		}
		if !strings.Contains(err.Error(), "gitdir") {
			t.Errorf("Resolve() error = %v, want it to name the missing gitdir: line", err)
		}
	})

	t.Run("a commondir naming no path", func(t *testing.T) {
		root := t.TempDir()
		git := mkdirAll(t, filepath.Join(root, ".git"))
		writeFile(t, filepath.Join(git, "commondir"), "\n", 0o600)

		if _, err := repo.Resolve(root); err == nil {
			t.Fatal("Resolve() accepted an empty commondir")
		}
	})
}

func TestHooksDefaultToTheCommonDirectory(t *testing.T) {
	root := t.TempDir()
	wt := mkdirAll(t, filepath.Join(root, "store", "worktrees", "w1"))
	writeFile(t, filepath.Join(root, ".git"), "gitdir: store/worktrees/w1\n", 0o600)
	writeFile(t, filepath.Join(wt, "commondir"), "../..\n", 0o600)

	hooks := filepath.Join(root, "store", "hooks")
	script(t, filepath.Join(hooks, "pre-commit"), 0o755)
	script(t, filepath.Join(hooks, "pre-commit.sample"), 0o644)
	mkdirAll(t, filepath.Join(hooks, "a-directory"))
	script(t, filepath.Join(root, "tools", "run"), 0o755)
	symlink(t, filepath.Join(root, "tools", "run"), filepath.Join(hooks, "pre-push"))

	l, err := repo.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	h, err := l.Hooks(nil)
	if err != nil {
		t.Fatalf("Hooks() error = %v", err)
	}
	if want := filepath.Join(realPath(t, root), "store", "hooks"); h.Dir != want {
		t.Errorf("Hooks() dir = %q, want the common directory's hooks %q", h.Dir, want)
	}
	if h.FromConfig != nil {
		t.Errorf("Hooks() claimed the configuration chose the directory: %+v", h.FromConfig)
	}
	if h.Outside {
		t.Error("Hooks() called a directory inside the checkout outside it")
	}
	if want := []string{"pre-commit", "pre-push"}; !reflect.DeepEqual(h.Executables, want) {
		t.Errorf("Hooks() listed %v, want %v", h.Executables, want)
	}
}

func TestHooksFollowHooksPath(t *testing.T) {
	t.Run("a relative path is taken against the work tree, and the last one wins", func(t *testing.T) {
		root := t.TempDir()
		mkdirAll(t, filepath.Join(root, ".git"))
		script(t, filepath.Join(root, "ci", "hooks", "post-checkout"), 0o755)

		l, err := repo.Resolve(root)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		h, err := l.Hooks(parseConfig(t, "[core]\n\thooksPath = ci/first\n\thooksPath = ci/hooks\n"))
		if err != nil {
			t.Fatalf("Hooks() error = %v", err)
		}
		if want := filepath.Join(realPath(t, root), "ci", "hooks"); h.Dir != want {
			t.Errorf("Hooks() dir = %q, want %q", h.Dir, want)
		}
		if h.FromConfig == nil || h.FromConfig.Value != "ci/hooks" {
			t.Errorf("Hooks() came from %+v, want the last core.hooksPath", h.FromConfig)
		}
		if h.Outside {
			t.Error("Hooks() called a directory inside the checkout outside it")
		}
		if want := []string{"post-checkout"}; !reflect.DeepEqual(h.Executables, want) {
			t.Errorf("Hooks() listed %v, want %v", h.Executables, want)
		}
	})

	t.Run("a path out of the checkout is reported as such, not refused", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		mkdirAll(t, filepath.Join(root, ".git"))
		script(t, filepath.Join(outside, "pre-commit"), 0o755)

		l, err := repo.Resolve(root)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		h, err := l.Hooks(parseConfig(t, "[core]\n\thooksPath = "+outside+"\n"))
		if err != nil {
			t.Fatalf("Hooks() error = %v", err)
		}
		if !h.Outside {
			t.Errorf("Hooks() dir = %q, want it reported outside the checkout at %q", h.Dir, l.Root)
		}
		if want := []string{"pre-commit"}; !reflect.DeepEqual(h.Executables, want) {
			t.Errorf("Hooks() listed %v, want %v", h.Executables, want)
		}
	})

	t.Run("a path that is no directory runs nothing", func(t *testing.T) {
		root := t.TempDir()
		mkdirAll(t, filepath.Join(root, ".git"))
		script(t, filepath.Join(root, "ci", "not-a-directory"), 0o755)

		l, err := repo.Resolve(root)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		for _, path := range []string{"ci/not-a-directory", "ci/missing"} {
			h, err := l.Hooks(parseConfig(t, "[core]\n\thooksPath = "+path+"\n"))
			if err != nil {
				t.Fatalf("Hooks() error = %v", err)
			}
			if h.Executables != nil {
				t.Errorf("Hooks() for %q listed %v, want nothing", path, h.Executables)
			}
		}
	})
}
