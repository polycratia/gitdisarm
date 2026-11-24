// Package gitattr reads the attributes a repository that arrived as files
// would apply to its own paths — from per-directory .gitattributes, from
// $GIT_DIR/info/attributes and from core.attributesFile, with git's own
// precedence and pattern rules — and answers which of the drivers the
// configuration defines those attributes bind to files that exist. Patterns
// are matched and paths are read; no value is ever run.
package gitattr

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/polycratia/gitdisarm/gitconfig"
)

// MaxMacroDepth bounds macro expansion, so that a macro naming itself ends.
const MaxMacroDepth = 10

// State is what the attributes decided for one attribute on one path.
type State int

const (
	// Unspecified is both an attribute nothing decided and one a `!name` line
	// decided to leave undecided, which is the word git prints for either.
	Unspecified State = iota
	// Set is `name`.
	Set
	// Unset is `-name`.
	Unset
	// Valued is `name=value`.
	Valued
)

// Attr is the decision for one attribute.
type Attr struct {
	State State
	Value string
}

// Info is the word git check-attr prints for the decision: set, unset,
// unspecified, or the value itself.
func (a Attr) Info() string {
	switch a.State {
	case Set:
		return "set"
	case Unset:
		return "unset"
	case Valued:
		return a.Value
	default:
		return "unspecified"
	}
}

// Ignored is a line gitattributes has no meaning for: a negative pattern,
// which git refuses rather than honours.
type Ignored struct {
	File    string
	Line    int
	Pattern string
}

// Driver is one filter, diff or merge driver the configuration defines,
// together with the existing files the attributes bind it to.
type Driver struct {
	// Family is the section that defines it, which is also the attribute that
	// binds it: filter, diff or merge.
	Family string
	// Name is the driver name, the subsection the configuration gave it.
	Name string
	// Keys are the configuration keys that defined it, in order.
	Keys []string
	// Files are the existing files whose attributes name this driver, relative
	// to the top of the work tree and slash-separated.
	Files []string
}

// Armed reports whether the attributes bind the driver to a file that exists.
// A driver bound to nothing is defined, not armed.
func (d Driver) Armed() bool { return len(d.Files) > 0 }

// Attributes is the attribute state of one checkout: every attribute file it
// carries, the macros they define, and the files they can speak about.
type Attributes struct {
	// Root is the top of the work tree, absolute.
	Root string
	// GitDir is the git directory, absolute, empty when none was given.
	GitDir string
	// Sources are the attribute files that were read, in the order they were
	// read.
	Sources []string
	// Ignored are the negative patterns found, which decide nothing.
	Ignored []Ignored

	files  []string
	macros map[string][]assign
	byDir  map[string][]line
	global []line
	info   []line
}

type assign struct {
	name string
	attr Attr
}

type line struct {
	pattern  string
	base     string
	anchored bool
	dirOnly  bool
	attrs    []assign
	file     string
	num      int
}

// builtinBinary is the macro git defines itself.
var builtinBinary = []assign{
	{"diff", Attr{State: Unset}},
	{"merge", Attr{State: Unset}},
	{"text", Attr{State: Unset}},
}

// Load reads every attribute file a checkout has: core.attributesFile when the
// entries set one, the .gitattributes of every directory of the work tree, and
// $GIT_DIR/info/attributes. A file that is not there decides nothing, as it
// decides nothing for git.
func Load(gitDir, worktree string, entries []gitconfig.Entry) (*Attributes, error) {
	root, err := filepath.Abs(worktree)
	if err != nil {
		return nil, err
	}
	a := &Attributes{
		Root:   root,
		byDir:  map[string][]line{},
		macros: map[string][]assign{"binary": builtinBinary},
	}
	if gitDir != "" {
		if a.GitDir, err = filepath.Abs(gitDir); err != nil {
			return nil, err
		}
	}
	if file := attributesFile(entries); file != "" {
		if err := a.read(&a.global, against(root, file), "", true); err != nil {
			return nil, err
		}
	}
	if err := a.walk(); err != nil {
		return nil, err
	}
	if a.GitDir != "" {
		if err := a.read(&a.info, filepath.Join(a.GitDir, "info", "attributes"), "", true); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// Files are the existing files of the work tree, relative to its top and
// slash-separated, in the order the walk found them.
func (a *Attributes) Files() []string { return a.files }

// Check decides every attribute for one path, given relative to the top of the
// work tree. The question is asked the way git asks it: from the highest
// precedence downwards, each line filling only what nothing has decided yet,
// and a macro expanding as soon as its name is set — so an attribute a macro
// filled is not reconsidered by a line of lower precedence.
func (a *Attributes) Check(path string) map[string]Attr {
	path = filepath.ToSlash(path)
	out := map[string]Attr{}
	fill := func(ls []line) {
		for i := len(ls) - 1; i >= 0; i-- {
			if ls[i].matches(path) {
				a.fill(out, ls[i].attrs, 0)
			}
		}
	}
	fill(a.info)
	dirs := ancestors(path)
	for i := len(dirs) - 1; i >= 0; i-- {
		fill(a.byDir[dirs[i]])
	}
	fill(a.global)
	return out
}

// Report lists, for every filter, diff or merge driver the configuration
// defines, the existing files the attributes bind it to. A driver whose
// attribute names it on no file that exists comes back with no files at all,
// which is a driver defined rather than armed. An attribute a `!name` line
// left undecided binds nothing.
func (a *Attributes) Report(entries []gitconfig.Entry) []Driver {
	drivers := definedDrivers(entries)
	if len(drivers) == 0 {
		return nil
	}
	for _, f := range a.files {
		attrs := a.Check(f)
		for i := range drivers {
			d := &drivers[i]
			if v, ok := attrs[d.Family]; ok && v.State == Valued && v.Value == d.Name {
				d.Files = append(d.Files, f)
			}
		}
	}
	return drivers
}

// fill applies one line's assignments, filling only attributes nothing has
// decided, and expands a macro the moment its name is set.
func (a *Attributes) fill(out map[string]Attr, attrs []assign, depth int) {
	for i := len(attrs) - 1; i >= 0; i-- {
		as := attrs[i]
		if _, decided := out[as.name]; decided {
			continue
		}
		out[as.name] = as.attr
		if as.attr.State != Set || depth >= MaxMacroDepth {
			continue
		}
		if macro, ok := a.macros[as.name]; ok {
			a.fill(out, macro, depth+1)
		}
	}
}

// walk reads the .gitattributes of every directory of the work tree and
// records every file that exists. The checkout's own git directory and any
// directory named .git are left out, and symlinked directories are not
// followed, because a walk that followed them need not terminate.
func (a *Attributes) walk() error {
	return filepath.WalkDir(a.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == a.Root {
				return err
			}
			// A directory that cannot be read hides what it holds from git as
			// well; the rest of the tree is still worth reading.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == a.Root {
				return nil
			}
			if d.Name() == ".git" || sameDir(path, a.GitDir) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(a.Root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		a.files = append(a.files, rel)
		if d.Name() != ".gitattributes" {
			return nil
		}
		dir := slashDir(rel)
		var ls []line
		if err := a.read(&ls, path, dir, dir == ""); err != nil {
			return err
		}
		a.byDir[dir] = ls
		return nil
	})
}

// read parses one attribute file, whose patterns are anchored to base. Macro
// definitions are kept only where git reads them: the top-level file, the git
// directory's and the one core.attributesFile names.
func (a *Attributes) read(dst *[]line, file, base string, macros bool) error {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return nil
	}
	a.Sources = append(a.Sources, file)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	num := 0
	for sc.Scan() {
		num++
		p := parseLine(strings.TrimSuffix(sc.Text(), "\r"), base)
		switch {
		case !p.ok:
		case p.neg:
			a.Ignored = append(a.Ignored, Ignored{File: file, Line: num, Pattern: p.line.pattern})
		case p.macro != "":
			if macros {
				a.macros[p.macro] = p.line.attrs
			}
		default:
			p.line.file, p.line.num = file, num
			*dst = append(*dst, p.line)
		}
	}
	return sc.Err()
}

type parsed struct {
	line  line
	macro string
	neg   bool
	ok    bool
}

// parseLine reads one line of an attribute file: a pattern, which may be
// quoted, followed by the attributes it decides. A pattern beginning with ! is
// reported as ignored, because gitattributes has no negative patterns.
func parseLine(text, base string) parsed {
	rest := strings.TrimLeft(text, " \t")
	if rest == "" || rest[0] == '#' {
		return parsed{}
	}
	tok, rest, ok := token(rest)
	if !ok {
		return parsed{}
	}
	if name, isMacro := strings.CutPrefix(tok, "[attr]"); isMacro {
		if !validName(name) {
			return parsed{}
		}
		return parsed{macro: name, line: line{attrs: assignments(rest)}, ok: true}
	}
	if strings.HasPrefix(tok, "!") {
		return parsed{neg: true, line: line{pattern: tok}, ok: true}
	}
	l := line{base: base, attrs: assignments(rest)}
	p := tok
	if strings.HasSuffix(p, "/") {
		// A pattern that can only mean a directory names no file.
		l.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	if strings.HasPrefix(p, "/") {
		l.anchored = true
		p = p[1:]
	}
	if strings.Contains(p, "/") {
		l.anchored = true
	}
	if p == "" {
		return parsed{}
	}
	l.pattern = p
	return parsed{line: l, ok: true}
}

// token reads the first whitespace-delimited word, or the quoted string that
// stands for one, and returns the rest of the line.
func token(s string) (string, string, bool) {
	if s[0] != '"' {
		if i := strings.IndexAny(s, " \t"); i >= 0 {
			return s[:i], s[i:], true
		}
		return s, "", true
	}
	var b []byte
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return string(b), s[i+1:], true
		case '\\':
			i++
			if i >= len(s) {
				return "", "", false
			}
			switch e := s[i]; e {
			case 'n':
				b = append(b, '\n')
			case 't':
				b = append(b, '\t')
			case 'r':
				b = append(b, '\r')
			case '\\', '"':
				b = append(b, e)
			default:
				return "", "", false
			}
		default:
			b = append(b, c)
		}
	}
	return "", "", false
}

// assignments reads the attribute words of a line: name, -name, !name and
// name=value. A word that names nothing valid decides nothing.
func assignments(s string) []assign {
	var out []assign
	for _, tok := range strings.Fields(s) {
		if tok[0] == '#' {
			break
		}
		a := assign{name: tok, attr: Attr{State: Set}}
		switch {
		case tok[0] == '-':
			a = assign{name: tok[1:], attr: Attr{State: Unset}}
		case tok[0] == '!':
			a = assign{name: tok[1:], attr: Attr{State: Unspecified}}
		default:
			if eq := strings.IndexByte(tok, '='); eq >= 0 {
				a = assign{name: tok[:eq], attr: Attr{State: Valued, Value: tok[eq+1:]}}
			}
		}
		if !validName(a.name) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func validName(name string) bool {
	if name == "" || !isAlnum(name[0]) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !isAlnum(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// matches answers whether a line's pattern names a path, given relative to the
// top of the work tree. A pattern holding a slash is matched against the path
// below the directory of the file that declared it; one without is matched
// against the last component alone, at any depth below that directory.
func (l line) matches(path string) bool {
	if l.dirOnly {
		return false
	}
	rel := path
	if l.base != "" {
		if !strings.HasPrefix(path, l.base+"/") {
			return false
		}
		rel = path[len(l.base)+1:]
	}
	if l.anchored {
		return wildmatch(l.pattern, rel, true)
	}
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		base = rel[i+1:]
	}
	return wildmatch(l.pattern, base, false)
}

// wildmatch matches the way git's matcher does. With pathname set, as it is
// for a pattern anchored to a directory, * and ? stop at a slash and only a **
// standing as a whole component crosses one; without it, the text is a single
// name and a * runs to its end. A backslash escapes the character after it,
// and a bracket expression behaves as in a shell but never matches a slash.
func wildmatch(p, t string, pathname bool) bool {
	for i := 0; i < len(p); {
		switch p[i] {
		case '*':
			stars := 1
			for i+stars < len(p) && p[i+stars] == '*' {
				stars++
			}
			rest := p[i+stars:]
			component := stars > 1 && (i == 0 || p[i-1] == '/') && (rest == "" || rest[0] == '/')
			crosses := !pathname || component
			// A **/ may stand for no directory at all.
			if component && strings.HasPrefix(rest, "/") && wildmatch(rest[1:], t, pathname) {
				return true
			}
			for j := 0; ; j++ {
				if wildmatch(rest, t[j:], pathname) {
					return true
				}
				if j >= len(t) {
					return false
				}
				if t[j] == '/' && !crosses {
					return false
				}
			}
		case '?':
			if t == "" || (pathname && t[0] == '/') {
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
// of p and reports how much of p the expression took. An unterminated one
// matches nothing.
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
		lo := p[i]
		if lo == '\\' && i+1 < len(p) {
			i++
			lo = p[i]
		}
		i++
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			hi := p[i+1]
			if hi == '\\' && i+2 < len(p) {
				i++
				hi = p[i+1]
			}
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

// definedDrivers reads the filter, diff and merge drivers out of a
// configuration: one driver per named subsection, with the keys that defined
// it, in the order the configuration declared them.
func definedDrivers(entries []gitconfig.Entry) []Driver {
	var drivers []Driver
	at := map[string]int{}
	for _, e := range entries {
		if e.Subsection == "" {
			continue
		}
		switch e.Section {
		case "filter", "diff", "merge":
		default:
			continue
		}
		key := e.Section + "\x00" + e.Subsection
		i, ok := at[key]
		if !ok {
			i = len(drivers)
			at[key] = i
			drivers = append(drivers, Driver{Family: e.Section, Name: e.Subsection})
		}
		name := e.Section + "." + e.Subsection + "." + e.Key
		if !has(drivers[i].Keys, name) {
			drivers[i].Keys = append(drivers[i].Keys, name)
		}
	}
	return drivers
}

func has(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// attributesFile is the core.attributesFile the entries set, the last
// declaration winning as it does for git.
func attributesFile(entries []gitconfig.Entry) string {
	file := ""
	for _, e := range entries {
		if e.Section == "core" && e.Subsection == "" && e.Key == "attributesfile" && e.Value != "" {
			file = e.Value
		}
	}
	return file
}

// ancestors lists the directories a path lies in, the top first.
func ancestors(path string) []string {
	dirs := []string{""}
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			dirs = append(dirs, path[:i])
		}
	}
	return dirs
}

func slashDir(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// against resolves a configured path: ~ for the home directory, an absolute
// path on its own, and a relative one against the top of the work tree, which
// is where a git run would start.
func against(base, path string) string {
	if expanded, ok := expandHome(path); ok {
		path = expanded
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

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

// sameDir reports whether two paths name the same directory, following
// symlinks when they can be followed.
func sameDir(a, b string) bool {
	if b == "" {
		return false
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea == nil && eb == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
