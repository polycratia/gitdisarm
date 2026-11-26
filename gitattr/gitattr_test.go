package gitattr_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/polycratia/gitdisarm/gitattr"
	"github.com/polycratia/gitdisarm/gitconfig"
	"github.com/polycratia/gitdisarm/internal/gitoracle"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func config(t *testing.T, content string) []gitconfig.Entry {
	t.Helper()
	entries, err := gitconfig.Parse(strings.NewReader(content), "config")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return entries
}

// info is the decision for one path as the words git check-attr prints for it,
// leaving out the attributes nothing decided, which is what -a leaves out too.
func info(attrs map[string]gitattr.Attr) map[string]string {
	out := map[string]string{}
	for name, a := range attrs {
		if a.State == gitattr.Unspecified {
			continue
		}
		out[name] = a.Info()
	}
	return out
}

// fixture builds a checkout whose attributes come from every place git reads
// them, and returns its root together with the file core.attributesFile has to
// name.
func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "attributes")

	write(t, global, `*.c deepness=global
*.c from-global=yes
*.md deepness=global
`)
	write(t, filepath.Join(root, ".gitattributes"), `[attr]bin -diff -merge -text
* text=auto
*.png bin
/only-top anchored=yes
src/*.c in-src=yes
deep/**/*.log deepstar=yes
build/ dironly=yes
?.tmp question=yes
[a-c].list bracket=yes
"quoted name.txt" quoted=yes
*.c deepness=top
`)
	write(t, filepath.Join(root, "src", ".gitattributes"), `*.c near=yes text
a.c -near
*.c deepness=src
`)
	write(t, filepath.Join(root, ".git", "info", "attributes"), `*.c deepness=info
*.md from-info=yes
`)

	for _, f := range []string{
		"src/a.c", "src/b.c", "note.md", "logo.png", "only-top",
		"sub/only-top", "deep/x/y/run.log", "build/keep", "x.tmp",
		"b.list", "quoted name.txt",
	} {
		write(t, filepath.Join(root, filepath.FromSlash(f)), "x\n")
	}
	return root, global
}

// fixtureAnswers is what the attributes of the fixture decide for every file it
// carries, as the words git prints for them.
var fixtureAnswers = map[string]map[string]string{
	".gitattributes":     {"text": "auto"},
	"src/.gitattributes": {"text": "auto"},
	"src/a.c":            {"deepness": "info", "near": "unset", "text": "set", "in-src": "yes", "from-global": "yes"},
	"src/b.c":            {"deepness": "info", "near": "yes", "text": "set", "in-src": "yes", "from-global": "yes"},
	"note.md":            {"deepness": "global", "from-info": "yes", "text": "auto"},
	"logo.png":           {"bin": "set", "diff": "unset", "merge": "unset", "text": "unset"},
	"only-top":           {"anchored": "yes", "text": "auto"},
	"sub/only-top":       {"text": "auto"},
	"deep/x/y/run.log":   {"deepstar": "yes", "text": "auto"},
	"build/keep":         {"text": "auto"},
	"x.tmp":              {"question": "yes", "text": "auto"},
	"b.list":             {"bracket": "yes", "text": "auto"},
	"quoted name.txt":    {"quoted": "yes", "text": "auto"},
}

func TestCheckFollowsGitsPrecedence(t *testing.T) {
	root, global := fixture(t)

	a, err := gitattr.Load(filepath.Join(root, ".git"), root, config(t, "[core]\n\tattributesFile = "+global+"\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	for _, want := range []string{
		global,
		filepath.Join(root, ".gitattributes"),
		filepath.Join(root, "src", ".gitattributes"),
		filepath.Join(root, ".git", "info", "attributes"),
	} {
		found := false
		for _, s := range a.Sources {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Sources = %v, want it to hold %q", a.Sources, want)
		}
	}

	files := a.Files()
	if len(files) != len(fixtureAnswers) {
		t.Errorf("Files() = %v, want the %d files the fixture carries", files, len(fixtureAnswers))
	}
	for _, f := range files {
		want, ok := fixtureAnswers[f]
		if !ok {
			t.Errorf("Files() reported %q, which the fixture does not carry", f)
			continue
		}
		if got := info(a.Check(f)); !reflect.DeepEqual(got, want) {
			t.Errorf("Check(%q) = %v, want %v", f, got, want)
		}
	}
}

func TestNegativePatternsAreReportedAndDecideNothing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitattributes"), `*.c kept=yes
!*.c refused=yes
`)
	write(t, filepath.Join(root, "a.c"), "x\n")

	a, err := gitattr.Load("", root, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := info(a.Check("a.c")), map[string]string{"kept": "yes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Check() = %v, want %v", got, want)
	}
	want := []gitattr.Ignored{{File: filepath.Join(root, ".gitattributes"), Line: 2, Pattern: "!*.c"}}
	if !reflect.DeepEqual(a.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", a.Ignored, want)
	}
}

func TestABangAttributeBlocksWhatIsBelowIt(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitattributes"), "*.c text=auto\n")
	write(t, filepath.Join(root, "src", ".gitattributes"), "*.c !text\n")
	write(t, filepath.Join(root, "src", "a.c"), "x\n")

	a, err := gitattr.Load("", root, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	attrs := a.Check("src/a.c")
	got, ok := attrs["text"]
	if !ok || got.State != gitattr.Unspecified || got.Info() != "unspecified" {
		t.Errorf("Check() decided text = %+v, want it left unspecified", got)
	}
	if words := info(attrs); len(words) != 0 {
		t.Errorf("Check() = %v, want nothing git would print", words)
	}
}

func TestMacrosCountOnlyWhereGitReadsThem(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitattributes"), `[attr]top-macro from-top=yes
*.c top-macro
`)
	write(t, filepath.Join(root, "src", ".gitattributes"), `[attr]deep-macro from-deep=yes
*.c deep-macro
`)
	write(t, filepath.Join(root, "src", "a.c"), "x\n")

	a, err := gitattr.Load("", root, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := map[string]string{"top-macro": "set", "from-top": "yes", "deep-macro": "set"}
	if got := info(a.Check("src/a.c")); !reflect.DeepEqual(got, want) {
		t.Errorf("Check() = %v, want %v", got, want)
	}
}

func TestTheBinaryMacroIsGitsOwn(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitattributes"), "*.png binary\n")
	write(t, filepath.Join(root, "logo.png"), "x\n")

	a, err := gitattr.Load("", root, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := map[string]string{"binary": "set", "diff": "unset", "merge": "unset", "text": "unset"}
	if got := info(a.Check("logo.png")); !reflect.DeepEqual(got, want) {
		t.Errorf("Check() = %v, want %v", got, want)
	}
}

func TestAMacroNamingItselfEnds(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitattributes"), `[attr]loop loop other=yes
*.c loop
`)
	write(t, filepath.Join(root, "a.c"), "x\n")

	a, err := gitattr.Load("", root, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := map[string]string{"loop": "set", "other": "yes"}
	if got := info(a.Check("a.c")); !reflect.DeepEqual(got, want) {
		t.Errorf("Check() = %v, want %v", got, want)
	}
}

func TestReportArmsOnlyDriversBoundToExistingFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitattributes"), `*.bin filter=lfs
missing.txt filter=unused
*.odt diff=odt
*.lock merge=ours
note.md diff=markdown
`)
	write(t, filepath.Join(root, "assets", "a.bin"), "x\n")
	write(t, filepath.Join(root, "x.odt"), "x\n")
	write(t, filepath.Join(root, "note.md"), "x\n")

	entries := config(t, `[filter "lfs"]
	clean = git-lfs clean
	smudge = git-lfs smudge
[filter "unused"]
	clean = sh -c id
[diff "odt"]
	textconv = odt2txt
[merge "ours"]
	driver = false
[user]
	name = someone
`)

	a, err := gitattr.Load("", root, entries)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := a.Report(entries)
	want := []gitattr.Driver{
		{
			Family: "filter",
			Name:   "lfs",
			Keys:   []string{"filter.lfs.clean", "filter.lfs.smudge"},
			Files:  []string{"assets/a.bin"},
		},
		{Family: "filter", Name: "unused", Keys: []string{"filter.unused.clean"}},
		{Family: "diff", Name: "odt", Keys: []string{"diff.odt.textconv"}, Files: []string{"x.odt"}},
		{Family: "merge", Name: "ours", Keys: []string{"merge.ours.driver"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Report() = %+v, want %+v", got, want)
	}
	for _, d := range got {
		if armed := len(d.Files) > 0; d.Armed() != armed {
			t.Errorf("%s.%s Armed() = %t with files %v", d.Family, d.Name, d.Armed(), d.Files)
		}
	}
}

func oracle(t *testing.T) *gitoracle.Oracle {
	t.Helper()
	o, err := gitoracle.Find()
	if err != nil {
		if errors.Is(err, gitoracle.ErrNoGit) {
			t.Skipf("skipping: no git binary to check the attributes against (%v); set %s to a git executable to run this test", err, gitoracle.EnvGit)
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

// checkAttr asks git which attributes it gives every path, in the words it
// prints for them:
//
//	git check-attr -a -z -- <paths>
//
// which leaves out the attributes nothing decided.
func checkAttr(t *testing.T, o *gitoracle.Oracle, root string, paths []string) map[string]map[string]string {
	t.Helper()
	args := append([]string{"check-attr", "-a", "-z", "--"}, paths...)
	out, err := o.In(root, args...)
	if err != nil {
		t.Fatalf("git check-attr error = %v", err)
	}
	fields := strings.Split(string(out), "\x00")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%3 != 0 {
		t.Fatalf("git printed %d fields, want triples of path, attribute and value", len(fields))
	}
	got := map[string]map[string]string{}
	for _, p := range paths {
		got[p] = map[string]string{}
	}
	for i := 0; i < len(fields); i += 3 {
		path, name, value := fields[i], fields[i+1], fields[i+2]
		if got[path] == nil {
			t.Fatalf("git answered for %q, which it was not asked about", path)
		}
		got[path][name] = value
	}
	return got
}

func TestCheckAgreesWithGitCheckAttr(t *testing.T) {
	o := oracle(t)
	root, global := fixture(t)

	if _, err := o.In(root, "init", "."); err != nil {
		t.Fatalf("git init error = %v", err)
	}
	if _, err := o.In(root, "config", "core.attributesFile", global); err != nil {
		t.Fatalf("git config error = %v", err)
	}

	cf, err := os.Open(filepath.Join(root, ".git", "config"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	entries, err := gitconfig.Parse(cf, cf.Name())
	cf.Close()
	if err != nil {
		t.Fatalf("Parse() refused the configuration git wrote: %v", err)
	}

	a, err := gitattr.Load(filepath.Join(root, ".git"), root, entries)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	files := a.Files()
	if len(files) == 0 {
		t.Fatal("Files() found nothing to ask git about")
	}

	want := checkAttr(t, o, root, files)
	for _, f := range files {
		if got := info(a.Check(f)); !reflect.DeepEqual(got, want[f]) {
			t.Errorf("%s: Check() = %v, git check-attr says %v", f, got, want[f])
		}
	}
}
