// Package gitoracle runs a real git binary as an oracle for the configuration
// parser: it asks git which keys and values it reads from a file. It only ever
// lists. No value found in a configuration is used as a program.
package gitoracle

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EnvGit names the environment variable that pins the git binary to use.
const EnvGit = "GITDISARM_GIT"

// ErrNoGit reports that no binary was pinned and none was found on PATH.
var ErrNoGit = errors.New("no git binary")

// Entry is one key git listed for a configuration file, in git's own order.
type Entry struct {
	// Origin is the origin git reports, for example "file:/tmp/x/config".
	Origin string
	// Name is the full key name: lowercased section and key, subsection as
	// written.
	Name  string
	Value string
	// HasValue is false when git listed a key that has no value at all.
	HasValue bool
}

// Oracle is a located git binary together with the throwaway home directory
// its invocations see.
type Oracle struct {
	Path    string
	Version string
	Home    string
}

// RefusedError reports that git exited with a status instead of listing.
type RefusedError struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("git %s: exit status %d: %s", strings.Join(e.Args, " "), e.Code, e.Stderr)
}

// Find locates git, preferring $GITDISARM_GIT over PATH, and records its
// version. When nothing is pinned and PATH has no git, the error matches
// ErrNoGit; a pinned binary that cannot be used is an error of its own, never
// a fallback. Close releases the temporary home directory.
func Find() (*Oracle, error) {
	pinned := os.Getenv(EnvGit)
	wanted := pinned
	if wanted == "" {
		wanted = "git"
	}
	path, err := exec.LookPath(wanted)
	if err != nil {
		if pinned != "" {
			return nil, fmt.Errorf("%s=%q: %w", EnvGit, pinned, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrNoGit, err)
	}
	home, err := os.MkdirTemp("", "gitoracle-home-")
	if err != nil {
		return nil, err
	}
	o := &Oracle{Path: path, Home: home}
	version, err := o.run("--version")
	if err != nil {
		o.Close()
		return nil, err
	}
	o.Version = strings.TrimSpace(string(version))
	return o, nil
}

// Close removes the temporary home directory.
func (o *Oracle) Close() error { return os.RemoveAll(o.Home) }

// List reports the keys git reads from the configuration file at an absolute
// path. A file git refuses yields a *RefusedError.
func (o *Oracle) List(file string) ([]Entry, error) {
	out, err := o.run("config", "--file", file, "--list", "--show-origin", "--null")
	if err != nil {
		return nil, err
	}
	return parseList(out)
}

func (o *Oracle) run(args ...string) ([]byte, error) {
	cmd := exec.Command(o.Path, args...)
	// Running from the throwaway home keeps repository discovery away from
	// whatever tree the caller happens to sit in.
	cmd.Dir = o.Home
	cmd.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + o.Home,
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"PATH=" + os.Getenv("PATH"),
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, &RefusedError{
				Args:   args,
				Code:   exit.ExitCode(),
				Stderr: strings.TrimSpace(stderr.String()),
			}
		}
		return nil, fmt.Errorf("%s %s: %w", o.Path, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// parseList reads the output of --list --show-origin --null: an origin and a
// key, each terminated by NUL, with a newline between key and value when the
// key has one.
func parseList(out []byte) ([]Entry, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("git output does not end with NUL")
	}
	fields := strings.Split(string(out[:len(out)-1]), "\x00")
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("git printed %d fields, want pairs of origin and key", len(fields))
	}
	var entries []Entry
	for i := 0; i < len(fields); i += 2 {
		e := Entry{Origin: fields[i], Name: fields[i+1]}
		if nl := strings.IndexByte(e.Name, '\n'); nl >= 0 {
			e.Name, e.Value, e.HasValue = e.Name[:nl], e.Name[nl+1:], true
		}
		entries = append(entries, e)
	}
	return entries, nil
}
