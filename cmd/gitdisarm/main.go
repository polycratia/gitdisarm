// Command gitdisarm reads the git configuration of a repository that arrived
// as files and prints every program that configuration would make git run,
// each with the override that takes it away.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/polycratia/gitdisarm/catalog"
	"github.com/polycratia/gitdisarm/gitconfig"
)

const usageText = `gitdisarm lists every program a repository's git configuration would run.

usage:
  gitdisarm [flags] [repository-or-config-file ...]

With no argument it reads .git/config in the current directory. A directory is
read the same way, or as a bare repository's own config file; anything else is
read as a configuration file. Values are listed and never run.

exit status:
  0  no key this catalog covers names a program, which is not a claim that the
     repository is safe: every report ends with what the catalog does not decide
  1  at least one key names a program
  2  a file could not be read, or is malformed

flags:
`

const (
	exitClean    = 0
	exitExecutes = 1
	exitError    = 2
)

func main() { os.Exit(run(os.Stdout, os.Stderr, os.Args[1:])) }

func run(out, errOut io.Writer, args []string) int {
	fs := flag.NewFlagSet("gitdisarm", flag.ContinueOnError)
	fs.SetOutput(errOut)
	all := fs.Bool("all", false, "report the covered keys that name no program as well")
	fs.Usage = func() {
		fmt.Fprint(errOut, usageText)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitClean
		}
		return exitError
	}

	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	var r report
	for _, p := range paths {
		file, err := configFile(p)
		if err != nil {
			fmt.Fprintf(errOut, "gitdisarm: %v\n", err)
			return exitError
		}
		entries, err := parse(file)
		if err != nil {
			fmt.Fprintf(errOut, "gitdisarm: %v\n", err)
			return exitError
		}
		r.add(out, file, entries, *all)
	}
	r.summary(out)

	if r.executes > 0 {
		return exitExecutes
	}
	return exitClean
}

// configFile resolves one argument to the configuration file to read.
func configFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return path, nil
	}
	for _, candidate := range []string{filepath.Join(path, ".git", "config"), filepath.Join(path, "config")} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: holds neither .git/config nor a bare repository's config", path)
}

func parse(file string) ([]gitconfig.Entry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return gitconfig.Parse(f, file)
}

type report struct {
	executes  int
	inert     int
	undecided int
	// config holds the -c overrides and flags the ones belonging to the
	// command, each once, in the order they were found.
	config []string
	flags  []string
}

func (r *report) add(out io.Writer, file string, entries []gitconfig.Entry, all bool) {
	fmt.Fprintf(out, "%s: %d keys\n", file, len(entries))
	for _, f := range catalog.ClassifyAll(entries) {
		switch f.Class {
		case catalog.Executes:
			r.executes++
			r.addDisarm(f.Disarm)
		case catalog.Unknown:
			r.undecided++
		default:
			r.inert++
			if !all {
				continue
			}
		}
		printFinding(out, f)
	}
	fmt.Fprintln(out)
}

func (r *report) addDisarm(disarm string) {
	dst := &r.config
	if !strings.HasPrefix(disarm, "-c ") {
		dst = &r.flags
	}
	for _, have := range *dst {
		if have == disarm {
			return
		}
	}
	*dst = append(*dst, disarm)
}

func (r *report) summary(out io.Writer) {
	fmt.Fprintf(out, "keys that name a program: %d; keys this catalog does not decide: %d (catalog version %d, rows read from git %s)\n",
		r.executes, r.undecided, catalog.Version, catalog.DocumentedAgainst)

	if len(r.config) > 0 {
		fmt.Fprintf(out, "\nRun git with every override at once:\n\n  git %s <command>\n", strings.Join(r.config, " "))
	}
	if len(r.flags) > 0 {
		fmt.Fprintf(out, "\nThese belong to the command that honours them rather than to git:\n\n  %s\n", strings.Join(r.flags, " "))
	}

	fmt.Fprint(out, "\nNot decided here, and still able to end in a program being run:\n\n")
	for _, g := range catalog.NotCovered {
		fmt.Fprintf(out, "  %s — %s\n", g.Name, g.Why)
	}
}

func printFinding(out io.Writer, f catalog.Finding) {
	fmt.Fprintf(out, "%-8s  %s = %q\n", f.Class, catalog.Name(f.Entry), f.Entry.Value)
	detail := f.Reason
	if f.Rule != nil {
		detail += " — git " + f.Rule.Since + ", " + string(f.Rule.Family)
	}
	fmt.Fprintf(out, "          %s\n", detail)
	if f.Disarm != "" {
		fmt.Fprintf(out, "          disarm: %s\n", f.Disarm)
	}
}
