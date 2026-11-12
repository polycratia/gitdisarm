package repo

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DisarmEmbeddedBare is the override that makes git refuse a bare repository
// it was not pointed at by name, so one sitting in a work tree cannot become
// the repository a command runs in.
const DisarmEmbeddedBare = "-c safe.bareRepository=explicit"

// Bare is one directory inside a work tree that git discovery would accept as
// a repository of its own.
type Bare struct {
	// Dir is the directory, absolute and with its symlinks resolved.
	Dir string
	// Rel is the same directory as it sits in the work tree.
	Rel string
	// Config is its own configuration file, empty when it carries none.
	Config string
}

// EmbeddedBare walks the work tree of a resolved checkout and reports every
// directory git discovery would open as a repository of its own, leaving the
// checkout's own git and common directories out of the walk.
func (l *Layout) EmbeddedBare() ([]Bare, error) {
	return walkBare(l.Worktree, func(dir string) bool {
		return within(l.GitDir, dir) || within(l.CommonDir, dir)
	})
}

// EmbeddedBare walks a directory as a work tree, for a tree whose layout was
// not resolved.
func EmbeddedBare(root string) ([]Bare, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return walkBare(real, nil)
}

// IsGitDirectory reports whether git would accept a directory as a repository,
// asking what setup.c asks of it: a HEAD it can read, an objects directory and
// a refs directory.
func IsGitDirectory(dir string) bool {
	return validHead(filepath.Join(dir, "HEAD")) &&
		isDir(filepath.Join(dir, "objects")) &&
		isDir(filepath.Join(dir, "refs"))
}

// walkBare walks root and reports the directories git would accept. A
// directory named .git is a checkout's own git directory rather than a
// repository embedded in one, and is not walked; neither is the inside of a
// repository that was found, whose contents belong to it. Symlinked
// directories are not followed, because a walk that followed them need not
// terminate.
func walkBare(root string, own func(dir string) bool) ([]Bare, error) {
	var found []Bare
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			// A directory that cannot be read hides what it holds from git
			// as well; the rest of the tree is still worth reporting.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() || path == root {
			return nil
		}
		if d.Name() == ".git" || (own != nil && own(path)) {
			return fs.SkipDir
		}
		if !IsGitDirectory(path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b := Bare{Dir: path, Rel: rel}
		if config := filepath.Join(path, "config"); isFile(config) {
			b.Config = config
		}
		found = append(found, b)
		return fs.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// validHead reads a HEAD the way setup.c's validate_headref does: a symlink is
// a HEAD on its own, a ref: line has to name a ref, and anything else has to
// be a raw object name followed by nothing but whitespace. Only the first
// bytes are read, because that is all git reads.
func validHead(file string) bool {
	st, err := os.Lstat(file)
	if err != nil {
		return false
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if !st.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	head := string(buf[:n])
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		return validRef(firstLine(ref))
	}
	return objectName(head)
}

// validRef is the part of git's refname check this question needs: one
// non-empty name, without the characters git refuses in one.
func validRef(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c == 0x7f || strings.IndexByte("~^:?*[\\", c) >= 0 {
			return false
		}
	}
	return !strings.Contains(name, "..") && !strings.Contains(name, "@{") && !strings.HasSuffix(name, ".lock")
}

// objectName reports whether the text begins with a whole object name, of
// either hash length, followed by nothing but whitespace.
func objectName(s string) bool {
	hex := s
	if i := strings.IndexAny(s, " \t\n\v\f\r"); i >= 0 {
		hex = s[:i]
	}
	if len(hex) != 40 && len(hex) != 64 {
		return false
	}
	for i := 0; i < len(hex); i++ {
		lower := hex[i] | 0x20
		if !(hex[i] >= '0' && hex[i] <= '9') && !(lower >= 'a' && lower <= 'f') {
			return false
		}
	}
	return true
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
