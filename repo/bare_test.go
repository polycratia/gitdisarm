package repo_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/polycratia/gitdisarm/internal/gitoracle"
	"github.com/polycratia/gitdisarm/repo"
)

// bareDir builds the three things git looks for in a repository, with the HEAD
// given; an empty text writes no HEAD at all.
func bareDir(t *testing.T, dir, head string) string {
	t.Helper()
	mkdirAll(t, filepath.Join(dir, "objects"))
	mkdirAll(t, filepath.Join(dir, "refs"))
	if head != "" {
		writeFile(t, filepath.Join(dir, "HEAD"), head, 0o600)
	}
	return dir
}

func bareOracle(t *testing.T) *gitoracle.Oracle {
	t.Helper()
	o, err := gitoracle.Find()
	if err != nil {
		if errors.Is(err, gitoracle.ErrNoGit) {
			t.Skipf("skipping: no git binary to check discovery against (%v); set %s to a git executable to run this test", err, gitoracle.EnvGit)
		}
		t.Fatalf("gitoracle.Find() error = %v", err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	t.Logf("oracle: %s (%s)", o.Version, o.Path)
	return o
}

// knowsSafeBareRepository reports whether the oracle is new enough to be asked
// the question: safe.bareRepository arrived in git 2.38.
func knowsSafeBareRepository(version string) bool {
	fields := strings.Fields(version)
	if len(fields) < 3 {
		return false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major > 2 || (major == 2 && minor >= 38)
}

func TestEmbeddedBareReportsWhatDiscoveryAccepts(t *testing.T) {
	root := t.TempDir()
	// The checkout's own git directory is not a repository embedded in it.
	bareDir(t, filepath.Join(root, ".git"), "ref: refs/heads/main\n")

	payload := bareDir(t, filepath.Join(root, "vendor", "payload.git"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(payload, "config"), "[core]\n\tpager = sh -c id\n", 0o600)
	bareDir(t, filepath.Join(root, "deep", "nest", "detached.git"), strings.Repeat("ab", 20)+"\n")
	// A repository found is not walked into, so what sits inside it is its own.
	bareDir(t, filepath.Join(root, "vendor", "payload.git", "inner.git"), "ref: refs/heads/main\n")

	// What git would not accept.
	bareDir(t, filepath.Join(root, "no-head.git"), "")
	bareDir(t, filepath.Join(root, "junk-head.git"), "not a ref at all\n")
	mkdirAll(t, filepath.Join(root, "no-refs.git", "objects"))
	writeFile(t, filepath.Join(root, "no-refs.git", "HEAD"), "ref: refs/heads/main\n", 0o600)
	mkdirAll(t, filepath.Join(root, "no-objects.git", "refs"))
	writeFile(t, filepath.Join(root, "no-objects.git", "HEAD"), "ref: refs/heads/main\n", 0o600)

	l, err := repo.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	got, err := l.EmbeddedBare()
	if err != nil {
		t.Fatalf("EmbeddedBare() error = %v", err)
	}

	real := realPath(t, root)
	want := []repo.Bare{
		{
			Dir: filepath.Join(real, "deep", "nest", "detached.git"),
			Rel: filepath.Join("deep", "nest", "detached.git"),
		},
		{
			Dir:    filepath.Join(real, "vendor", "payload.git"),
			Rel:    filepath.Join("vendor", "payload.git"),
			Config: filepath.Join(real, "vendor", "payload.git", "config"),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EmbeddedBare() = %+v, want %+v", got, want)
	}
}

func TestEmbeddedBareIsWhatGitOpensAndTheOverrideRefuses(t *testing.T) {
	o := bareOracle(t)
	if !knowsSafeBareRepository(o.Version) {
		t.Skipf("skipping: %s predates safe.bareRepository, which arrived in git 2.38", o.Version)
	}

	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, ".git"))
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n", 0o600)
	nested := filepath.Join(root, "vendor", "payload.git")
	if _, err := o.In(root, "init", "--bare", nested); err != nil {
		t.Fatalf("git init --bare error = %v", err)
	}

	l, err := repo.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	found, err := l.EmbeddedBare()
	if err != nil {
		t.Fatalf("EmbeddedBare() error = %v", err)
	}
	if len(found) != 1 || found[0].Rel != filepath.Join("vendor", "payload.git") {
		t.Fatalf("EmbeddedBare() = %+v, want the repository git made at vendor/payload.git", found)
	}

	// Run from inside it, git opens it: that is the discovery this walk reads.
	out, err := o.In(found[0].Dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		t.Fatalf("git refused a repository it made: %v", err)
	}
	if answer := strings.TrimSpace(string(out)); answer != "true" {
		t.Fatalf("git called it a bare repository = %q, want \"true\"", answer)
	}

	// With the override, it refuses to open it at all.
	args := append(strings.Fields(repo.DisarmEmbeddedBare), "rev-parse", "--is-bare-repository")
	_, err = o.In(found[0].Dir, args...)
	var refused *gitoracle.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("git opened the embedded bare repository with %s (err = %v)", repo.DisarmEmbeddedBare, err)
	}
	t.Logf("git refused it: %s", refused.Stderr)
}
