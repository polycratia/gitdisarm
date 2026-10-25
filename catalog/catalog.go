// Package catalog is the versioned list of git configuration keys that make
// git run a program, held as data: one row per key pattern, with the family it
// belongs to, the earliest git version that reads it, and the command-line
// override that disarms it.
package catalog

import (
	"strings"

	"github.com/polycratia/gitdisarm/gitconfig"
)

const (
	// Version is this catalog's own version. It changes whenever a row is
	// added, removed or re-classified.
	Version = 1
	// DocumentedAgainst is the git version whose documentation the rows were
	// read from.
	DocumentedAgainst = "2.47"
)

// Class is the verdict for one configuration entry.
type Class int

const (
	// Unknown means no row covers the key and the catalog does not guess.
	Unknown Class = iota
	// Inert means the key is covered and names no program.
	Inert
	// Executes means git would run a program named by the value.
	Executes
)

func (c Class) String() string {
	switch c {
	case Inert:
		return "inert"
	case Executes:
		return "executes"
	default:
		return "unknown"
	}
}

// Family groups keys git reads through the same mechanism.
type Family string

const (
	FamilyCore       Family = "core"
	FamilyPager      Family = "pager"
	FamilyEditor     Family = "editor"
	FamilyDiff       Family = "diff"
	FamilyFilter     Family = "filter"
	FamilyMerge      Family = "merge"
	FamilyCredential Family = "credential"
	FamilyAlias      Family = "alias"
	FamilySigning    Family = "signing"
	FamilyHooks      Family = "hooks"
)

type subMatch uint8

const (
	withoutSub subMatch = iota
	withSub
)

type valueMatch uint8

const (
	anyValue valueMatch = iota
	boolValue
	bangValue
)

// Rule is one catalog row.
type Rule struct {
	// Pattern names the key the way git's documentation does, with <name>
	// standing for the part that varies: a subsection, or the key itself.
	Pattern string
	Family  Family
	// Since is the earliest git version that reads the key.
	Since string
	// Class is the verdict for an entry this row matches.
	Class Class
	// Disarm is the override that neutralizes the key, with <name> standing
	// for the same part as in Pattern. Empty unless Class is Executes.
	Disarm string
	// Runs says in one line what git runs when the key is set, or what the
	// key is instead when it runs nothing.
	Runs string

	section string
	key     string // empty matches any key, and is what <name> stands for
	sub     subMatch
	value   valueMatch
}

// Rules is the catalog, in the order Classify consults it: a row with a
// condition on the value comes before the unconditional row for the same key.
var Rules = []Rule{
	{
		Pattern: "core.fsmonitor", Family: FamilyCore, Since: "2.37",
		Class:   Inert,
		Runs:    "the file-system monitor built into git, not a program the repository names",
		section: "core", key: "fsmonitor", value: boolValue,
	},
	{
		Pattern: "core.fsmonitor", Family: FamilyCore, Since: "2.16",
		Class:   Executes, Disarm: "-c core.fsmonitor=false",
		Runs:    "the hook git starts to be told which files changed, on nearly every command",
		section: "core", key: "fsmonitor",
	},
	{
		Pattern: "core.sshCommand", Family: FamilyCore, Since: "2.10",
		Class:   Executes, Disarm: "-c core.sshCommand=ssh",
		Runs:    "the command git runs in place of ssh for every transport operation",
		section: "core", key: "sshcommand",
	},
	{
		Pattern: "core.pager", Family: FamilyPager, Since: "1.5",
		Class:   Executes, Disarm: "-c core.pager=cat",
		Runs:    "the pager git pipes its output into",
		section: "core", key: "pager",
	},
	{
		Pattern: "core.editor", Family: FamilyEditor, Since: "1.5",
		Class:   Executes, Disarm: "-c core.editor=true",
		Runs:    "the editor git opens for messages and interactive edits",
		section: "core", key: "editor",
	},
	{
		Pattern: "core.hooksPath", Family: FamilyHooks, Since: "2.9",
		Class:   Executes, Disarm: "-c core.hooksPath=/dev/null",
		Runs:    "the directory git takes its hooks from, so every executable in it",
		section: "core", key: "hookspath",
	},
	{
		Pattern: "sequence.editor", Family: FamilyEditor, Since: "1.7.8",
		Class:   Executes, Disarm: "-c sequence.editor=true",
		Runs:    "the editor git opens for the rebase instruction sheet",
		section: "sequence", key: "editor",
	},
	{
		Pattern: "pager.<name>", Family: FamilyPager, Since: "1.6",
		Class:   Inert,
		Runs:    "a switch for git's default pager on one command, not a program",
		section: "pager", value: boolValue,
	},
	{
		Pattern: "pager.<name>", Family: FamilyPager, Since: "1.6",
		Class:   Executes, Disarm: "-c pager.<name>=false",
		Runs:    "the pager git pipes the output of one command into",
		section: "pager",
	},
	{
		Pattern: "diff.external", Family: FamilyDiff, Since: "1.5",
		Class:   Executes, Disarm: "--no-ext-diff",
		Runs:    "the program git runs in place of its own diff",
		section: "diff", key: "external",
	},
	{
		Pattern: "diff.<name>.command", Family: FamilyDiff, Since: "1.5",
		Class:   Executes, Disarm: "--no-ext-diff",
		Runs:    "the external diff driver for paths carrying this diff attribute",
		section: "diff", key: "command", sub: withSub,
	},
	{
		Pattern: "diff.<name>.textconv", Family: FamilyDiff, Since: "1.6.1",
		Class:   Executes, Disarm: "--no-textconv",
		Runs:    "the program git runs to turn such a file into text before diffing it",
		section: "diff", key: "textconv", sub: withSub,
	},
	{
		Pattern: "diff.<name>.binary", Family: FamilyDiff, Since: "1.6",
		Class:   Inert,
		Runs:    "whether the driver's paths are treated as binary",
		section: "diff", key: "binary", sub: withSub,
	},
	{
		Pattern: "diff.<name>.cachetextconv", Family: FamilyDiff, Since: "1.7.4",
		Class:   Inert,
		Runs:    "whether textconv output is cached",
		section: "diff", key: "cachetextconv", sub: withSub,
	},
	{
		Pattern: "diff.<name>.xfuncname", Family: FamilyDiff, Since: "1.6",
		Class:   Inert,
		Runs:    "the pattern git itself uses to label hunks",
		section: "diff", key: "xfuncname", sub: withSub,
	},
	{
		Pattern: "diff.<name>.wordregex", Family: FamilyDiff, Since: "1.7.2",
		Class:   Inert,
		Runs:    "the pattern git itself uses to split words",
		section: "diff", key: "wordregex", sub: withSub,
	},
	{
		Pattern: "filter.<name>.clean", Family: FamilyFilter, Since: "1.5",
		Class:   Executes, Disarm: "-c filter.<name>.clean=cat -c filter.<name>.required=false",
		Runs:    "the program git runs on every matching file on the way into the index",
		section: "filter", key: "clean", sub: withSub,
	},
	{
		Pattern: "filter.<name>.smudge", Family: FamilyFilter, Since: "1.5",
		Class:   Executes, Disarm: "-c filter.<name>.smudge=cat -c filter.<name>.required=false",
		Runs:    "the program git runs on every matching file on the way into the work tree",
		section: "filter", key: "smudge", sub: withSub,
	},
	{
		Pattern: "filter.<name>.process", Family: FamilyFilter, Since: "2.11",
		Class:   Executes, Disarm: "-c filter.<name>.process= -c filter.<name>.required=false",
		Runs:    "the long-running program git keeps open to filter matching files",
		section: "filter", key: "process", sub: withSub,
	},
	{
		Pattern: "filter.<name>.required", Family: FamilyFilter, Since: "1.7.8",
		Class:   Inert,
		Runs:    "whether a failing filter is an error, not a program",
		section: "filter", key: "required", sub: withSub,
	},
	{
		Pattern: "merge.<name>.driver", Family: FamilyMerge, Since: "1.5",
		Class:   Executes, Disarm: "-c merge.<name>.driver=false",
		Runs:    "the program git runs to merge paths carrying this merge attribute",
		section: "merge", key: "driver", sub: withSub,
	},
	{
		Pattern: "merge.<name>.name", Family: FamilyMerge, Since: "1.5",
		Class:   Inert,
		Runs:    "the description git prints for the driver",
		section: "merge", key: "name", sub: withSub,
	},
	{
		Pattern: "merge.<name>.recursive", Family: FamilyMerge, Since: "1.6",
		Class:   Inert,
		Runs:    "the internal strategy used for an inner merge",
		section: "merge", key: "recursive", sub: withSub,
	},
	{
		Pattern: "credential.helper", Family: FamilyCredential, Since: "1.7.9",
		Class:   Executes, Disarm: "-c credential.helper=",
		Runs:    "the helper git runs to obtain credentials, and hands them to",
		section: "credential", key: "helper",
	},
	{
		Pattern: "credential.<name>.helper", Family: FamilyCredential, Since: "1.7.9",
		Class:   Executes, Disarm: "-c credential.<name>.helper= -c credential.helper=",
		Runs:    "the helper git runs for URLs matching this pattern",
		section: "credential", key: "helper", sub: withSub,
	},
	{
		Pattern: "alias.<name>", Family: FamilyAlias, Since: "1.5",
		Class:   Executes, Disarm: "-c alias.<name>=",
		Runs:    "a shell command, because the expansion begins with '!'",
		section: "alias", value: bangValue,
	},
	{
		Pattern: "alias.<name>", Family: FamilyAlias, Since: "1.5",
		Class:   Inert,
		Runs:    "another git command with its options, which names no program",
		section: "alias",
	},
	{
		Pattern: "gpg.program", Family: FamilySigning, Since: "1.7.9",
		Class:   Executes, Disarm: "-c gpg.program=false -c log.showSignature=false",
		Runs:    "the program git runs to make and to verify signatures",
		section: "gpg", key: "program",
	},
	{
		Pattern: "gpg.<name>.program", Family: FamilySigning, Since: "2.19",
		Class:   Executes, Disarm: "-c gpg.<name>.program=false -c log.showSignature=false",
		Runs:    "the program git runs for signatures of this format",
		section: "gpg", key: "program", sub: withSub,
	},
	{
		Pattern: "gpg.format", Family: FamilySigning, Since: "2.19",
		Class:   Inert,
		Runs:    "which signature format is used, not a program",
		section: "gpg", key: "format",
	},
	{
		Pattern: "gpg.minTrustLevel", Family: FamilySigning, Since: "2.26",
		Class:   Inert,
		Runs:    "the trust level git accepts, not a program",
		section: "gpg", key: "mintrustlevel",
	},
	{
		Pattern: "gpg.<name>.allowedSignersFile", Family: FamilySigning, Since: "2.34",
		Class:   Inert,
		Runs:    "a file of accepted signers, read as data",
		section: "gpg", key: "allowedsignersfile", sub: withSub,
	},
	{
		Pattern: "gpg.<name>.revocationFile", Family: FamilySigning, Since: "2.34",
		Class:   Inert,
		Runs:    "a file of revoked keys, read as data",
		section: "gpg", key: "revocationfile", sub: withSub,
	},
}

// executorFamilies are the sections whose drivers are nothing but the programs
// their keys name, so a key no row covers inside one of them still executes.
var executorFamilies = map[string]Family{
	"filter": FamilyFilter,
	"diff":   FamilyDiff,
	"merge":  FamilyMerge,
	"gpg":    FamilySigning,
}

// Gap is one key or mechanism the catalog does not decide.
type Gap struct {
	// Name is the key or mechanism as git's documentation names it.
	Name string
	// Why says what it can end up running, and why no row covers it.
	Why string
}

// NotCovered names what can still end in a program being run and what this
// catalog has no verdict for. A report prints it, so that a short list of
// findings is never read as a clean repository.
var NotCovered = []Gap{
	{
		Name: "hook.<name>.command",
		Why:  "names a hook in the configuration instead of in the hooks directory; still being settled in git, so no row claims its versions or its override",
	},
	{
		Name: "include.path",
		Why:  "pulls in another configuration file, which may carry any key in this catalog; following includes is a pass of its own",
	},
	{
		Name: "includeIf.<condition>.path",
		Why:  "the conditional form of the same, where whether a key applies depends on the condition git evaluates",
	},
	{
		Name: "core.gitProxy",
		Why:  "names a program git runs to open a git:// connection",
	},
	{
		Name: "core.alternateRefsCommand",
		Why:  "names a program git runs to list the refs of an alternate object store",
	},
	{
		Name: "uploadpack.packObjectsHook",
		Why:  "names a program git runs when it serves a fetch from this repository",
	},
	{
		Name: "trailer.<token>.command",
		Why:  "names a program git runs to produce a trailer value for a commit message",
	},
	{
		Name: "init.templateDir",
		Why:  "a template directory can seed a new repository's hooks with executables",
	},
	{
		Name: ".git/hooks/<name>",
		Why:  "executables already sitting in the hooks directory are files, not configuration, and this catalog reads configuration",
	},
	{
		Name: ".gitattributes",
		Why:  "attributes decide which paths a filter, diff or merge driver applies to; the drivers themselves are configuration and are covered",
	},
}

// Finding is what the catalog has to say about one entry.
type Finding struct {
	Entry gitconfig.Entry
	Class Class
	// Rule is the row that decided it, nil when no row matched.
	Rule   *Rule
	Family Family
	// Disarm is the override with this entry's own name filled in, empty
	// unless Class is Executes.
	Disarm string
	// Reason says why the class is what it is.
	Reason string
}

// Name is the full key name git prints for an entry.
func Name(e gitconfig.Entry) string {
	if e.Subsection == "" {
		return e.Section + "." + e.Key
	}
	return e.Section + "." + e.Subsection + "." + e.Key
}

// Classify decides whether git would run a program for an entry. A key the
// catalog covers is answered by its row; a key it does not cover inside an
// executor family executes anyway; anything else is unknown and says so.
func Classify(e gitconfig.Entry) Finding {
	for i := range Rules {
		r := &Rules[i]
		if !r.matches(e) {
			continue
		}
		f := Finding{Entry: e, Class: r.Class, Rule: r, Family: r.Family, Reason: r.Runs}
		if r.Class == Executes {
			f.Disarm = r.DisarmFor(e)
		}
		return f
	}
	name := Name(e)
	if family, ok := familyOf(e); ok {
		return Finding{
			Entry:  e,
			Class:  Executes,
			Family: family,
			Disarm: "-c " + name + "=",
			Reason: "no row covers " + name + ", and a " + string(family) + " driver is defined by the programs its keys name",
		}
	}
	return Finding{Entry: e, Class: Unknown, Reason: "no row covers " + name}
}

// ClassifyAll classifies entries in the order they were declared.
func ClassifyAll(entries []gitconfig.Entry) []Finding {
	var findings []Finding
	for _, e := range entries {
		findings = append(findings, Classify(e))
	}
	return findings
}

// DisarmFor returns the row's override with <name> replaced by the part this
// entry varies in.
func (r *Rule) DisarmFor(e gitconfig.Entry) string {
	return strings.ReplaceAll(r.Disarm, "<name>", r.nameOf(e))
}

func (r *Rule) nameOf(e gitconfig.Entry) string {
	if r.key == "" {
		return e.Key
	}
	return e.Subsection
}

func (r *Rule) matches(e gitconfig.Entry) bool {
	if e.Section != r.section {
		return false
	}
	if r.key != "" && e.Key != r.key {
		return false
	}
	if r.sub == withSub && e.Subsection == "" {
		return false
	}
	if r.sub == withoutSub && e.Subsection != "" {
		return false
	}
	switch r.value {
	case boolValue:
		return isBool(e.Value)
	case bangValue:
		return strings.HasPrefix(strings.TrimLeft(e.Value, " \t"), "!")
	}
	return true
}

func familyOf(e gitconfig.Entry) (Family, bool) {
	family, ok := executorFamilies[e.Section]
	if !ok {
		return "", false
	}
	// A driver is named by its subsection; the top level of these sections
	// also holds plain settings, which the catalog does not guess about.
	if e.Section != "gpg" && e.Subsection == "" {
		return "", false
	}
	return family, true
}

func isBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "true", "false", "yes", "no", "on", "off", "1", "0":
		return true
	}
	return false
}
