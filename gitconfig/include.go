package gitconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxIncludeDepth is git's own limit on how deeply includes may nest.
const MaxIncludeDepth = 10

var (
	// ErrIncludeDepth reports that includes nested deeper than git reads them.
	ErrIncludeDepth = errors.New("exceeded maximum include depth")
	// ErrIncludeCycle reports that a file includes itself through some chain.
	ErrIncludeCycle = errors.New("include cycle")
)

// Outside is one include that resolved to a file outside the checkout.
type Outside struct {
	// From is the file that carried the directive, and Line its line.
	From string
	Line int
	// Path is the file the directive resolved to.
	Path string
}

// Report is what following the includes of one checkout had to say.
type Report struct {
	// Files are the configuration files that were read, in the order they were
	// read, starting with the one Load was asked for.
	Files []string
	// Missing are the paths that named no readable file; git skips those.
	Missing []string
	// IncludesOutside are the includes that resolved outside the checkout,
	// which is to say outside both the work tree and the git directory.
	IncludesOutside []Outside
}

// Load reads <gitDir>/config and every file it includes, deciding each
// condition for this checkout: gitdir:, gitdir/i:, onbranch: and
// hasconfig:remote.*.url:. Entries come back in the order git applies them,
// with an included file's entries spliced in where its directive stood, and
// each entry keeps the file and line it was declared in. A path that names no
// readable file is skipped, as git skips it. Nesting deeper than git reads
// yields ErrIncludeDepth, and a file that includes itself through any chain
// yields ErrIncludeCycle. Paths are resolved and conditions are evaluated; no
// value is ever run.
func Load(gitDir, worktree string) ([]Entry, *Report, error) {
	l := &loader{
		gitDir:   realPath(gitDir),
		worktree: realPath(worktree),
		branch:   headBranch(gitDir),
		root:     filepath.Join(gitDir, "config"),
		report:   &Report{},
	}
	if err := l.read(l.root, 0); err != nil {
		return nil, nil, err
	}
	return l.entries, l.report, nil
}

type loader struct {
	gitDir   string
	worktree string
	branch   string
	root     string
	report   *Report
	entries  []Entry
	// chain holds the files currently open, innermost last.
	chain   []string
	urls    []string
	gotURLs bool
	// prepass is set while remote URLs are being collected, when a hasconfig
	// condition is taken to hold so that the question cannot ask itself.
	prepass bool
}

func (l *loader) read(file string, depth int) error {
	if depth > MaxIncludeDepth {
		return fmt.Errorf("%s: %w (%d)", file, ErrIncludeDepth, MaxIncludeDepth)
	}
	real := realPath(file)
	for _, open := range l.chain {
		if open == real {
			return fmt.Errorf("%s: %w", file, ErrIncludeCycle)
		}
	}
	entries, err := parseFile(file)
	if err != nil {
		return err
	}
	l.report.Files = append(l.report.Files, file)
	l.chain = append(l.chain, real)
	defer func() { l.chain = l.chain[:len(l.chain)-1] }()
	for _, e := range entries {
		l.entries = append(l.entries, e)
		path, ok := l.includePath(e)
		if !ok {
			continue
		}
		if !readable(path) {
			l.report.Missing = append(l.report.Missing, path)
			continue
		}
		if l.outside(path) {
			l.report.IncludesOutside = append(l.report.IncludesOutside, Outside{From: file, Line: e.Line, Path: path})
		}
		if err := l.read(path, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func parseFile(file string) ([]Entry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, file)
}

func readable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	return err == nil && !st.IsDir()
}

// includePath answers whether an entry is an include that applies to this
// checkout, and the file it names.
func (l *loader) includePath(e Entry) (string, bool) {
	if e.Key != "path" || e.Value == "" {
		return "", false
	}
	switch e.Section {
	case "include":
		if e.Subsection != "" {
			return "", false
		}
	case "includeif":
		if !l.holds(e.Subsection, e.File) {
			return "", false
		}
	default:
		return "", false
	}
	return resolveInclude(e.Value, e.File), true
}

// resolveInclude expands an include path the way git does: ~ for the home
// directory, and anything relative to the directory of the file that named it.
func resolveInclude(path, from string) string {
	if expanded, ok := expandHome(path); ok {
		path = expanded
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(from), path)
}

// holds decides one includeIf condition. A condition this reader does not know
// is false, as it is for git.
func (l *loader) holds(cond, from string) bool {
	switch {
	case strings.HasPrefix(cond, "gitdir:"):
		return l.gitdirMatches(cond[len("gitdir:"):], from, false)
	case strings.HasPrefix(cond, "gitdir/i:"):
		return l.gitdirMatches(cond[len("gitdir/i:"):], from, true)
	case strings.HasPrefix(cond, "onbranch:"):
		return l.branchMatches(cond[len("onbranch:"):])
	case strings.HasPrefix(cond, "hasconfig:remote.*.url:"):
		return l.remoteURLMatches(cond[len("hasconfig:remote.*.url:"):])
	}
	return false
}

func (l *loader) gitdirMatches(pattern, from string, icase bool) bool {
	if l.gitDir == "" || pattern == "" {
		return false
	}
	return wildmatch(gitdirPattern(pattern, from), l.gitDir, icase)
}

// gitdirPattern prepares a gitdir: pattern the way git does: ~ expansion, a
// leading ./ taken as the directory of the including file, any other relative
// pattern matched in any parent directory, and a trailing slash standing for
// everything below it.
func gitdirPattern(pattern, from string) string {
	if expanded, ok := expandHome(pattern); ok {
		pattern = expanded
	}
	switch {
	case strings.HasPrefix(pattern, "./"):
		pattern = filepath.Dir(realPath(from)) + pattern[1:]
	case !filepath.IsAbs(pattern) && !strings.HasPrefix(pattern, "**"):
		pattern = "**/" + pattern
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	return pattern
}

func (l *loader) branchMatches(pattern string) bool {
	if l.branch == "" || pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	return wildmatch(pattern, l.branch, false)
}

func (l *loader) remoteURLMatches(pattern string) bool {
	if l.prepass {
		return true
	}
	if !l.gotURLs {
		l.urls, l.gotURLs = l.remoteURLs(), true
	}
	for _, u := range l.urls {
		if wildmatch(pattern, u, false) {
			return true
		}
	}
	return false
}

// remoteURLs walks the same files once more for every remote.<name>.url, as
// git does before it can answer a hasconfig condition: the URL a condition
// asks about may be declared after it, or in a file it pulls in.
func (l *loader) remoteURLs() []string {
	pre := &loader{
		gitDir:   l.gitDir,
		worktree: l.worktree,
		branch:   l.branch,
		root:     l.root,
		report:   &Report{},
		prepass:  true,
	}
	_ = pre.read(pre.root, 0)
	var urls []string
	for _, e := range pre.entries {
		if e.Section == "remote" && e.Subsection != "" && e.Key == "url" {
			urls = append(urls, e.Value)
		}
	}
	return urls
}

// outside reports whether a path lies outside the work tree and the git
// directory both.
func (l *loader) outside(path string) bool {
	real := realPath(path)
	for _, root := range []string{l.worktree, l.gitDir} {
		if root != "" && within(root, real) {
			return false
		}
	}
	return true
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func realPath(path string) string {
	if path == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// expandHome replaces a leading ~ with the home directory, keeping a trailing
// slash, which the trailing-** rule still needs.
func expandHome(path string) (string, bool) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return strings.TrimSuffix(home, "/") + path[1:], true
}

// headBranch reads the branch HEAD points at, empty when HEAD is detached or
// unreadable.
func headBranch(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: ")
	if !ok {
		return ""
	}
	name, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return ""
	}
	return name
}

// wildmatch matches a path against a condition pattern the way git's own
// matcher does for these conditions: * and ? stop at a slash, a ** standing as
// a whole path component crosses slashes, a character class behaves as in a
// shell, and a backslash escapes the character after it. icase folds both
// sides.
func wildmatch(pattern, text string, icase bool) bool {
	if icase {
		pattern, text = strings.ToLower(pattern), strings.ToLower(text)
	}
	return match(pattern, text)
}

func match(p, t string) bool {
	for i := 0; i < len(p); {
		switch p[i] {
		case '*':
			stars := 1
			for i+stars < len(p) && p[i+stars] == '*' {
				stars++
			}
			rest := p[i+stars:]
			double := stars > 1 && (i == 0 || p[i-1] == '/') && (rest == "" || rest[0] == '/')
			// A **/ may stand for no directory at all.
			if double && strings.HasPrefix(rest, "/") && match(rest[1:], t) {
				return true
			}
			for j := 0; ; j++ {
				if match(rest, t[j:]) {
					return true
				}
				if j >= len(t) {
					return false
				}
				if t[j] == '/' && !double {
					return false
				}
			}
		case '?':
			if t == "" || t[0] == '/' {
				return false
			}
			t = t[1:]
			i++
		case '[':
			if t == "" {
				return false
			}
			n, ok := matchClass(p[i:], t[0])
			if !ok {
				return false
			}
			t = t[1:]
			i += n
		case '\\':
			if i+1 >= len(p) {
				return false
			}
			i++
			fallthrough
		default:
			if t == "" || t[0] != p[i] {
				return false
			}
			t = t[1:]
			i++
		}
	}
	return t == ""
}

// matchClass matches one character against the bracket expression at the start
// of p and reports how much of p the expression took. A class never matches a
// slash, and an unterminated one matches nothing.
func matchClass(p string, c byte) (int, bool) {
	i := 1
	negated := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		negated = true
		i++
	}
	found, first := false, true
	for i < len(p) && (p[i] != ']' || first) {
		first = false
		if strings.HasPrefix(p[i:], "[:") {
			if end := strings.Index(p[i:], ":]"); end > 0 {
				if inPosixClass(p[i+2:i+end], c) {
					found = true
				}
				i += end + 2
				continue
			}
		}
		lo := p[i]
		if lo == '\\' && i+1 < len(p) {
			i++
			lo = p[i]
		}
		i++
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			hi := p[i+1]
			i += 2
			if lo <= c && c <= hi {
				found = true
			}
			continue
		}
		if c == lo {
			found = true
		}
	}
	if i >= len(p) {
		return 0, false
	}
	if c == '/' {
		return i + 1, false
	}
	return i + 1, found != negated
}

func inPosixClass(name string, c byte) bool {
	switch name {
	case "alpha":
		return isAlpha(c)
	case "digit":
		return isDigit(c)
	case "alnum":
		return isAlpha(c) || isDigit(c)
	case "lower":
		return c >= 'a' && c <= 'z'
	case "upper":
		return c >= 'A' && c <= 'Z'
	case "space":
		return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
	case "xdigit":
		return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'f')
	case "punct":
		return c > ' ' && c < 0x7f && !isAlpha(c) && !isDigit(c)
	}
	return false
}
