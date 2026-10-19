package gitconfig

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseRules(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Entry
	}{
		{
			name: "sections and keys are lowercased",
			in:   "[Core]\n\tAutoCRLF = Input\n",
			want: []Entry{{Section: "core", Key: "autocrlf", Value: "Input", File: "config", Line: 2}},
		},
		{
			name: "key without a value is true",
			in:   "[core]\nbare\nsymlinks ; trailing comment\n",
			want: []Entry{
				{Section: "core", Key: "bare", Value: "true", File: "config", Line: 2},
				{Section: "core", Key: "symlinks", Value: "true", File: "config", Line: 3},
			},
		},
		{
			name: "quoted subsection keeps its case and its escapes",
			in:   `[remote "Up\\Stream \"X\""]` + "\n\turl = git@example.com:x\n",
			want: []Entry{{Section: "remote", Subsection: `Up\Stream "X"`, Key: "url", Value: "git@example.com:x", File: "config", Line: 2}},
		},
		{
			name: "deprecated section form lowercases the subsection",
			in:   "[Branch.MaIn]\nremote = origin\n",
			want: []Entry{{Section: "branch", Subsection: "main", Key: "remote", Value: "origin", File: "config", Line: 2}},
		},
		{
			name: "quotes keep the whitespace an unquoted value loses",
			in:   "[a]\nb =   x y   \nc = \"  x y  \"\n",
			want: []Entry{
				{Section: "a", Key: "b", Value: "x y", File: "config", Line: 2},
				{Section: "a", Key: "c", Value: "  x y  ", File: "config", Line: 3},
			},
		},
		{
			name: "comment characters end an unquoted value only",
			in:   "[a]\nb = x # tail\nc = \"x ; y\"\n",
			want: []Entry{
				{Section: "a", Key: "b", Value: "x", File: "config", Line: 2},
				{Section: "a", Key: "c", Value: "x ; y", File: "config", Line: 3},
			},
		},
		{
			name: "backslash escapes are decoded",
			in:   "[a]\n" + `b = x\ty\nz\b\\\"` + "\n",
			want: []Entry{{Section: "a", Key: "b", Value: "x\ty\nz\b\\\"", File: "config", Line: 2}},
		},
		{
			name: "line continuations join values",
			in:   "[a]\nb = one \\\n two\nc = \"q \\\n r\"\n",
			want: []Entry{
				{Section: "a", Key: "b", Value: "one  two", File: "config", Line: 2},
				{Section: "a", Key: "c", Value: "q  r", File: "config", Line: 4},
			},
		},
		{
			name: "empty values",
			in:   "[a]\nb =\nc = \"\"\n",
			want: []Entry{
				{Section: "a", Key: "b", Value: "", File: "config", Line: 2},
				{Section: "a", Key: "c", Value: "", File: "config", Line: 3},
			},
		},
		{
			name: "blank lines and comments are skipped and order is preserved",
			in:   "# head\n\n[a]\n; note\nx = 1\n[b \"S\"] # tail\ny = 2\n",
			want: []Entry{
				{Section: "a", Key: "x", Value: "1", File: "config", Line: 5},
				{Section: "b", Subsection: "S", Key: "y", Value: "2", File: "config", Line: 7},
			},
		},
		{
			name: "a section without keys yields nothing",
			in:   "[a]\n",
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(c.in), "config")
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Parse() = %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestParseRefusals(t *testing.T) {
	cases := []struct {
		name string
		in   string
		line int
		msg  string
	}{
		{
			name: "unterminated quoted value",
			in:   "[a]\nb = \"x\n",
			line: 2,
			msg:  "unterminated quoted value",
		},
		{
			name: "unknown escape in a value",
			in:   "[a]\n" + `b = "x\q"` + "\n",
			line: 2,
			msg:  `unknown escape sequence \q`,
		},
		{
			name: "continuation with nothing to continue",
			in:   "[a]\n" + `b = x\`,
			line: 2,
			msg:  "unterminated line continuation",
		},
		{
			name: "bad key character",
			in:   "[a]\nb$d = 1\n",
			line: 2,
			msg:  "invalid key character",
		},
		{
			name: "key must start with a letter",
			in:   "[a]\n1b = 1\n",
			line: 2,
			msg:  "invalid key character",
		},
		{
			name: "key outside of any section",
			in:   "b = 1\n",
			line: 1,
			msg:  "outside of any section",
		},
		{
			name: "unterminated section header",
			in:   "[a\n",
			line: 1,
			msg:  "invalid section header",
		},
		{
			name: "unterminated subsection name",
			in:   "[a \"b\n",
			line: 1,
			msg:  "unterminated subsection name",
		},
		{
			name: "unknown escape in a subsection name",
			in:   `[a "b\n"]` + "\n",
			line: 1,
			msg:  `unknown escape sequence \n in subsection name`,
		},
		{
			name: "bad section character",
			in:   "[a$b]\n",
			line: 1,
			msg:  "invalid section header",
		},
		{
			name: "junk after a section header",
			in:   "[a] junk\n",
			line: 1,
			msg:  "junk after section header",
		},
		{
			name: "empty section name",
			in:   "[]\n",
			line: 1,
			msg:  "invalid section name",
		},
		{
			name: "empty deprecated subsection",
			in:   "[a.]\n",
			line: 1,
			msg:  "invalid section name",
		},
		{
			name: "no partial result before the bad line",
			in:   "[a]\ngood = 1\nb = \"x\n",
			line: 3,
			msg:  "unterminated quoted value",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(c.in), "config")
			if got != nil {
				t.Errorf("Parse() returned %d entries, want none", len(got))
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Parse() error = %v, want *ParseError", err)
			}
			if pe.File != "config" || pe.Line != c.line {
				t.Errorf("Parse() error at %s:%d, want config:%d", pe.File, pe.Line, c.line)
			}
			if !strings.Contains(pe.Msg, c.msg) {
				t.Errorf("Parse() error msg = %q, want it to contain %q", pe.Msg, c.msg)
			}
			if !strings.HasPrefix(pe.Error(), "config:") {
				t.Errorf("Parse() error string = %q, want it to start with the file name", pe.Error())
			}
		})
	}
}
