package catalog

import (
	"strings"
	"testing"

	"github.com/polycratia/gitdisarm/gitconfig"
)

func entry(section, sub, key, value string) gitconfig.Entry {
	return gitconfig.Entry{
		Section:    section,
		Subsection: sub,
		Key:        key,
		Value:      value,
		File:       ".git/config",
		Line:       1,
	}
}

func TestClassifyEveryRow(t *testing.T) {
	cases := []struct {
		pattern string
		in      gitconfig.Entry
		class   Class
		disarm  string
	}{
		{"core.fsmonitor", entry("core", "", "fsmonitor", "true"), Inert, ""},
		{"core.fsmonitor", entry("core", "", "fsmonitor", ".git/hooks/watch"), Executes, "-c core.fsmonitor=false"},
		{"core.sshCommand", entry("core", "", "sshcommand", "sh -c id"), Executes, "-c core.sshCommand=ssh"},
		{"core.pager", entry("core", "", "pager", "sh -c id"), Executes, "-c core.pager=cat"},
		{"core.editor", entry("core", "", "editor", "sh -c id"), Executes, "-c core.editor=true"},
		{"core.hooksPath", entry("core", "", "hookspath", "ci/hooks"), Executes, "-c core.hooksPath=/dev/null"},
		{"sequence.editor", entry("sequence", "", "editor", "sh -c id"), Executes, "-c sequence.editor=true"},
		{"pager.<name>", entry("pager", "", "log", "false"), Inert, ""},
		{"pager.<name>", entry("pager", "", "log", "sh -c id"), Executes, "-c pager.log=false"},
		{"diff.external", entry("diff", "", "external", "sh -c id"), Executes, "--no-ext-diff"},
		{"diff.<name>.command", entry("diff", "odt", "command", "sh -c id"), Executes, "--no-ext-diff"},
		{"diff.<name>.textconv", entry("diff", "odt", "textconv", "sh -c id"), Executes, "--no-textconv"},
		{"diff.<name>.binary", entry("diff", "odt", "binary", "true"), Inert, ""},
		{"diff.<name>.cachetextconv", entry("diff", "odt", "cachetextconv", "true"), Inert, ""},
		{"diff.<name>.xfuncname", entry("diff", "odt", "xfuncname", "^func"), Inert, ""},
		{"diff.<name>.wordregex", entry("diff", "odt", "wordregex", "[A-Za-z]+"), Inert, ""},
		{"filter.<name>.clean", entry("filter", "lfs", "clean", "git-lfs clean -- %f"), Executes, "-c filter.lfs.clean=cat -c filter.lfs.required=false"},
		{"filter.<name>.smudge", entry("filter", "lfs", "smudge", "git-lfs smudge -- %f"), Executes, "-c filter.lfs.smudge=cat -c filter.lfs.required=false"},
		{"filter.<name>.process", entry("filter", "lfs", "process", "git-lfs filter-process"), Executes, "-c filter.lfs.process= -c filter.lfs.required=false"},
		{"filter.<name>.required", entry("filter", "lfs", "required", "true"), Inert, ""},
		{"merge.<name>.driver", entry("merge", "ours", "driver", "sh -c id"), Executes, "-c merge.ours.driver=false"},
		{"merge.<name>.name", entry("merge", "ours", "name", "keep ours"), Inert, ""},
		{"merge.<name>.recursive", entry("merge", "ours", "recursive", "binary"), Inert, ""},
		{"credential.helper", entry("credential", "", "helper", "sh -c id"), Executes, "-c credential.helper="},
		{"credential.<name>.helper", entry("credential", "https://example.com", "helper", "sh -c id"), Executes, "-c credential.https://example.com.helper= -c credential.helper="},
		{"alias.<name>", entry("alias", "", "x", "!sh -c id"), Executes, "-c alias.x="},
		{"alias.<name>", entry("alias", "", "x", "log --oneline"), Inert, ""},
		{"gpg.program", entry("gpg", "", "program", "sh -c id"), Executes, "-c gpg.program=false -c log.showSignature=false"},
		{"gpg.<name>.program", entry("gpg", "ssh", "program", "sh -c id"), Executes, "-c gpg.ssh.program=false -c log.showSignature=false"},
		{"gpg.format", entry("gpg", "", "format", "ssh"), Inert, ""},
		{"gpg.minTrustLevel", entry("gpg", "", "mintrustlevel", "marginal"), Inert, ""},
		{"gpg.<name>.allowedSignersFile", entry("gpg", "ssh", "allowedsignersfile", ".signers"), Inert, ""},
		{"gpg.<name>.revocationFile", entry("gpg", "ssh", "revocationfile", ".revoked"), Inert, ""},
	}

	covered := make([]bool, len(Rules))
	for _, c := range cases {
		got := Classify(c.in)
		for i := range Rules {
			if got.Rule == &Rules[i] {
				covered[i] = true
			}
		}
		t.Run(Name(c.in)+"="+c.in.Value, func(t *testing.T) {
			if got.Rule == nil {
				t.Fatalf("Classify() matched no row, want %s", c.pattern)
			}
			if got.Rule.Pattern != c.pattern {
				t.Errorf("Classify() matched row %s, want %s", got.Rule.Pattern, c.pattern)
			}
			if got.Class != c.class {
				t.Errorf("Classify() = %s, want %s", got.Class, c.class)
			}
			if got.Disarm != c.disarm {
				t.Errorf("Classify() disarm = %q, want %q", got.Disarm, c.disarm)
			}
			if got.Family != got.Rule.Family {
				t.Errorf("Classify() family = %q, want %q", got.Family, got.Rule.Family)
			}
			if got.Reason == "" {
				t.Error("Classify() gave no reason")
			}
		})
	}

	for i, ok := range covered {
		if !ok {
			t.Errorf("no case exercises row %d: %s (%s)", i, Rules[i].Pattern, Rules[i].Class)
		}
	}
}

func TestUnknownKeyInExecutorFamilyExecutes(t *testing.T) {
	cases := []struct {
		in     gitconfig.Entry
		family Family
		disarm string
	}{
		{entry("filter", "lfs", "newthing", "sh -c id"), FamilyFilter, "-c filter.lfs.newthing="},
		{entry("diff", "odt", "newthing", "sh -c id"), FamilyDiff, "-c diff.odt.newthing="},
		{entry("merge", "ours", "newthing", "sh -c id"), FamilyMerge, "-c merge.ours.newthing="},
		{entry("gpg", "", "newthing", "sh -c id"), FamilySigning, "-c gpg.newthing="},
		{entry("gpg", "ssh", "newthing", "sh -c id"), FamilySigning, "-c gpg.ssh.newthing="},
	}

	for _, c := range cases {
		t.Run(Name(c.in), func(t *testing.T) {
			got := Classify(c.in)
			if got.Class != Executes {
				t.Errorf("Classify() = %s, want executes", got.Class)
			}
			if got.Rule != nil {
				t.Errorf("Classify() claimed row %s for a key no row covers", got.Rule.Pattern)
			}
			if got.Family != c.family {
				t.Errorf("Classify() family = %q, want %q", got.Family, c.family)
			}
			if got.Disarm != c.disarm {
				t.Errorf("Classify() disarm = %q, want %q", got.Disarm, c.disarm)
			}
		})
	}
}

func TestClassifyUnknown(t *testing.T) {
	cases := []gitconfig.Entry{
		entry("core", "", "autocrlf", "input"),
		entry("user", "", "name", "someone"),
		entry("diff", "", "algorithm", "histogram"),
		entry("merge", "", "conflictstyle", "diff3"),
		entry("sequence", "", "other", "1"),
	}

	for _, in := range cases {
		t.Run(Name(in), func(t *testing.T) {
			got := Classify(in)
			if got.Class != Unknown {
				t.Errorf("Classify() = %s, want unknown", got.Class)
			}
			if got.Disarm != "" {
				t.Errorf("Classify() disarm = %q, want none", got.Disarm)
			}
			if !strings.Contains(got.Reason, Name(in)) {
				t.Errorf("Classify() reason = %q, want it to name the key", got.Reason)
			}
		})
	}
}

func TestRowsAreWellFormed(t *testing.T) {
	for i := range Rules {
		r := &Rules[i]
		t.Run(r.Pattern+"/"+r.Class.String(), func(t *testing.T) {
			if r.Family == "" || r.Since == "" || r.Runs == "" {
				t.Errorf("row %s is missing a family, a version or a description", r.Pattern)
			}
			if r.Class == Unknown {
				t.Errorf("row %s classifies nothing", r.Pattern)
			}
			if (r.Class == Executes) != (r.Disarm != "") {
				t.Errorf("row %s is %s and its override is %q", r.Pattern, r.Class, r.Disarm)
			}
			if strings.Contains(r.Disarm, "<name>") && !strings.Contains(r.Pattern, "<name>") {
				t.Errorf("row %s fills a name its pattern does not vary", r.Pattern)
			}
			want := r.section
			if r.sub == withSub {
				want += ".<name>"
			}
			if r.key == "" {
				want += ".<name>"
			} else {
				want += "." + r.key
			}
			if got := strings.ToLower(r.Pattern); got != want {
				t.Errorf("row pattern %s does not match what it matches, %s", got, want)
			}
		})
	}
}

func TestNotCoveredIsNamedAndExplained(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range NotCovered {
		if g.Name == "" || g.Why == "" {
			t.Errorf("gap %q has no name or no reason", g.Name)
		}
		if seen[g.Name] {
			t.Errorf("gap %q is listed twice", g.Name)
		}
		seen[g.Name] = true
	}
	if !seen["hook.<name>.command"] {
		t.Error("NotCovered does not name hook.<name>.command")
	}
}

func TestClassNames(t *testing.T) {
	for class, want := range map[Class]string{Unknown: "unknown", Inert: "inert", Executes: "executes"} {
		if got := class.String(); got != want {
			t.Errorf("Class(%d).String() = %q, want %q", class, got, want)
		}
	}
}
