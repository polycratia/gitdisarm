# gitdisarm

Before an agent or a person runs `git` in a repository that arrived as plain files, gitdisarm lists every program that repository's own git configuration would execute. For each finding it prints the invocation that neutralizes it, so the tree can be inspected without running anything it brought along.

## Package `gitconfig`

The first piece is a parser that reads a git configuration file the way git does, or refuses it:

- section and key names are case-insensitive and reported lowercased;
- a quoted subsection keeps its case and its escapes, while the deprecated `[section.sub]` form lowercases the subsection;
- values honour double quotes, backslash escapes (`\n`, `\t`, `\b`, `\\`, `\"`), trailing-backslash line continuations, and `#`/`;` comments outside quotes;
- anything malformed — an unterminated quote, an unknown escape, a bad key character — returns a `*ParseError` naming the file and line, and never a partial result.

```go
entries, err := gitconfig.Parse(f, ".git/config")
```

Each entry carries its section, subsection, key, value, file and line, in the order the file declared it, so a later pass can decide which keys name a program.

## Package `catalog`

The second piece is the catalog of keys that make git run something, held as data: one row per key pattern, with the family it belongs to, the earliest git version that reads it, and the override that disarms it. The catalog carries its own `Version` and the git version its rows were read from.

```go
f := catalog.Classify(entry)
if f.Class == catalog.Executes {
	fmt.Printf("%s runs %q — disarm with: git %s …\n", catalog.Name(entry), entry.Value, f.Disarm)
}
```

`Classify` answers `executes`, `inert` or `unknown`. Rows cover `core.fsmonitor`, `core.sshCommand`, `core.pager`, `pager.<cmd>`, `core.editor`, `sequence.editor`, `diff.external`, `diff.<driver>.command` and `.textconv`, `filter.<driver>.clean`, `.smudge` and `.process`, `merge.<driver>.driver`, `credential.helper`, `alias.<name>` whose expansion begins with `!`, `gpg.program` and `core.hooksPath` — along with the neighbouring keys that only look dangerous, so a boolean `core.fsmonitor` or a `pager.log = false` is answered `inert` rather than reported.

A key no row covers, inside a family whose drivers are nothing but the programs their keys name — `filter.<driver>.*`, `diff.<driver>.*`, `merge.<driver>.*`, `gpg.*` — is answered `executes` anyway, with an override that empties it. Everything else is `unknown` and says so, rather than being quietly called safe.

`NotCovered` names what can still end in a program being run and what the catalog has no verdict for: `hook.<name>.command`, `include.path`, `core.gitProxy`, `uploadpack.packObjectsHook`, a template directory, the executables already sitting in `.git/hooks`. It is exported so a report can print it, because a short list of findings must never read as a clean repository.

## Command `gitdisarm`

The command puts the two together: it reads a repository's configuration, classifies every key, and prints the override for each one that names a program.

```
$ gitdisarm ./arrived-as-files
arrived-as-files/.git/config: 2 keys
executes  core.pager = "sh -c id"
          the pager git pipes its output into — git 1.5, pager
          disarm: -c core.pager=cat
unknown   user.name = "someone"
          no row covers user.name

keys that name a program: 1; keys this catalog does not decide: 1 (catalog version 1, rows read from git 2.47)

Run git with every override at once:

  git -c core.pager=cat <command>

Not decided here, and still able to end in a program being run:

  hook.<name>.command — …
```

With no argument it reads `.git/config` in the current directory; a directory argument is read the same way, or as a bare repository's own config file, and anything else is read as a configuration file. `-all` reports the covered keys that name no program as well. Overrides that belong to the command rather than to git, such as `--no-ext-diff`, are listed apart from the `-c` ones.

It exits 1 when at least one key names a program, 2 when a file cannot be read or is malformed, and 0 otherwise. Exit 0 is not a claim that the repository is safe, which is why every report ends with the catalog's gaps.

## Checked against git itself

`internal/gitoracle` makes a real git binary the oracle for the parser. It takes the binary from `GITDISARM_GIT`, or from `PATH`, records `git --version` in the test log, and asks it for

```
git config --file <file> --list --show-origin --null
```

with `GIT_CONFIG_NOSYSTEM=1` and `HOME` pointing at a throwaway directory, so the listing can only come from the file under test. The oracle only lists: no value in a configuration is ever run as a program.

The differential test writes synthetic configurations into a temporary directory and requires `Parse` to return exactly the keys and values git reports for them, and to refuse every file git refuses. With no git binary available it skips with an explicit message rather than passing quietly.

Run the tests with `go test ./...`, or pin the binary first: `GITDISARM_GIT=/usr/bin/git go test ./...`.

---

Made by polycratia — https://polycratia.com
