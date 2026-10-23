package gitoracle_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polycratia/gitdisarm/gitconfig"
	"github.com/polycratia/gitdisarm/internal/gitoracle"
)

// kv is the expected view of one key: the name git prints for it and the value
// the parser must report.
type kv struct {
	name  string
	value string
}

func oracle(t *testing.T) *gitoracle.Oracle {
	t.Helper()
	o, err := gitoracle.Find()
	if err != nil {
		if errors.Is(err, gitoracle.ErrNoGit) {
			t.Skipf("skipping: no git binary to check the parser against (%v); set %s to a git executable to run this test", err, gitoracle.EnvGit)
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

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func fullName(e gitconfig.Entry) string {
	if e.Subsection == "" {
		return e.Section + "." + e.Key
	}
	return e.Section + "." + e.Subsection + "." + e.Key
}

func TestParseAgreesWithGit(t *testing.T) {
	o := oracle(t)

	cases := []struct {
		name string
		in   string
		want []kv
	}{
		{
			name: "sections and keys are lowercased",
			in:   "[Core]\n\tAutoCRLF = Input\n",
			want: []kv{{"core.autocrlf", "Input"}},
		},
		{
			name: "a key with no value",
			in:   "[core]\nbare\n\tsymlinks\n",
			want: []kv{{"core.bare", "true"}, {"core.symlinks", "true"}},
		},
		{
			name: "a quoted subsection keeps its case and its escapes",
			in:   `[remote "Up\\Stream \"X\""]` + "\n\turl = git@example.com:x\n",
			want: []kv{{`remote.Up\Stream "X".url`, "git@example.com:x"}},
		},
		{
			name: "the deprecated section form lowercases the subsection",
			in:   "[Branch.MaIn]\nremote = origin\n",
			want: []kv{{"branch.main.remote", "origin"}},
		},
		{
			name: "quotes keep the whitespace an unquoted value loses",
			in:   "[a]\nb =   x y   \nc = \"  x y  \"\n",
			want: []kv{{"a.b", "x y"}, {"a.c", "  x y  "}},
		},
		{
			name: "comment characters end an unquoted value only",
			in:   "[a]\nb = x # tail\nc = \"x ; y\"\n",
			want: []kv{{"a.b", "x"}, {"a.c", "x ; y"}},
		},
		{
			name: "backslash escapes are decoded",
			in:   "[a]\n" + `b = x\ty\nz\b\\\"` + "\n",
			want: []kv{{"a.b", "x\ty\nz\b\\\""}},
		},
		{
			name: "line continuations join values",
			in:   "[a]\nb = one \\\n two\nc = \"q \\\n r\"\n",
			want: []kv{{"a.b", "one  two"}, {"a.c", "q  r"}},
		},
		{
			name: "empty values",
			in:   "[a]\nb =\nc = \"\"\n",
			want: []kv{{"a.b", ""}, {"a.c", ""}},
		},
		{
			name: "repeated keys keep their order",
			in:   "[a]\nb = 1\n[a]\nb = 2\nc = 3\n",
			want: []kv{{"a.b", "1"}, {"a.b", "2"}, {"a.c", "3"}},
		},
		{
			name: "comments and sections without keys are skipped",
			in:   "# head\n\n[empty]\n; note\n[a]\nx = 1\n[b \"S\"] # tail\ny = 2\n",
			want: []kv{{"a.x", "1"}, {"b.S.y", "2"}},
		},
		{
			name: "a file with no keys at all",
			in:   "; nothing here\n[a]\n",
			want: nil,
		},
		{
			name: "carriage returns are not part of a value",
			in:   "[a]\r\nb = 1\r\n",
			want: []kv{{"a.b", "1"}},
		},
		{
			name: "keys that name a program are listed, never run",
			in:   "[core]\n\tpager = less -FRX\n\tsshCommand = \"ssh -o StrictHostKeyChecking=no\"\n[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n",
			want: []kv{
				{"core.pager", "less -FRX"},
				{"core.sshcommand", "ssh -o StrictHostKeyChecking=no"},
				{"filter.lfs.clean", "git-lfs clean -- %f"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeConfig(t, c.in)

			listed, err := o.List(path)
			if err != nil {
				t.Fatalf("git refused a file the parser accepts: %v", err)
			}
			if len(listed) != len(c.want) {
				t.Fatalf("git listed %d keys, want %d: %+v", len(listed), len(c.want), listed)
			}

			parsed, err := gitconfig.Parse(strings.NewReader(c.in), path)
			if err != nil {
				t.Fatalf("Parse() refused a file git accepts: %v", err)
			}
			if len(parsed) != len(c.want) {
				t.Fatalf("Parse() returned %d entries, want %d: %+v", len(parsed), len(c.want), parsed)
			}

			for i, w := range c.want {
				g := listed[i]
				if origin := "file:" + path; g.Origin != origin {
					t.Errorf("key %d came from %q, want %q", i, g.Origin, origin)
				}
				value := g.Value
				if !g.HasValue {
					// git prints such a key bare; the parser reports the
					// implicit truth git's bool reader gives it.
					value = "true"
				}
				if g.Name != w.name || value != w.value {
					t.Errorf("git listed %s=%q, want %s=%q", g.Name, value, w.name, w.value)
				}
				if p := parsed[i]; fullName(p) != w.name || p.Value != w.value {
					t.Errorf("Parse() gave %s=%q, want %s=%q", fullName(p), p.Value, w.name, w.value)
				}
			}
		})
	}
}

func TestParseRefusesWhatGitRefuses(t *testing.T) {
	o := oracle(t)

	cases := []struct {
		name string
		in   string
	}{
		{"unterminated quoted value", "[a]\nb = \"x\n"},
		{"unknown escape in a value", "[a]\n" + `b = "x\q"` + "\n"},
		{"bad key character", "[a]\nb$d = 1\n"},
		{"key must start with a letter", "[a]\n1b = 1\n"},
		{"unterminated section header", "[a\n"},
		{"unterminated subsection name", "[a \"b\n"},
		{"empty section name", "[]\n"},
		{"bad section character", "[a$b]\n"},
		{"nothing survives the bad line", "[a]\ngood = 1\nb = \"x\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeConfig(t, c.in)

			_, err := o.List(path)
			var refused *gitoracle.RefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("git accepted this file (err = %v); it is not a refusal to check against", err)
			}
			t.Logf("git refused it: %s", refused.Stderr)

			got, err := gitconfig.Parse(strings.NewReader(c.in), path)
			if got != nil {
				t.Errorf("Parse() returned %d entries, want none", len(got))
			}
			var pe *gitconfig.ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Parse() error = %v, want *ParseError for a file git refuses", err)
			}
		})
	}
}
