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

Whether a value is a boolean is git's own question, not a guess: `true`, `yes`, `on`, `false`, `no`, `off` in any case, an empty value, and any integer git takes, including the bases and the `k`/`m`/`g` units it accepts. A `core.fsmonitor` git reads as a boolean selects the monitor built into git, or no monitor at all; anything else is the hook command git starts, and is reported with the text the file gave it.

A key no row covers, inside a family whose drivers are nothing but the programs their keys name — `filter.<driver>.*`, `diff.<driver>.*`, `merge.<driver>.*`, `gpg.*` — is answered `executes` anyway, with an override that empties it. Everything else is `unknown` and says so, rather than being quietly called safe.

`NotCovered` names what can still end in a program being run and what the catalog has no verdict for: `hook.<name>.command`, `include.path`, `core.gitProxy`, `uploadpack.packObjectsHook`, a template directory, the executables already sitting in `.git/hooks`. It is exported so a report can print it, because a short list of findings must never read as a clean repository.

## Package `repo`

A repository that arrived as files decides for itself where its git directory is, and `repo.Resolve` reads that decision as data:

- `.git` as a directory, as a symlink, or as a file carrying a `gitdir:` line, taken relative to the directory holding it;
- the `commondir` pointer a linked worktree leaves in its git directory, taken relative to that directory, naming where refs, objects and hooks are shared from;
- the `gitdir` pointer that leads back to the linked worktree's own `.git` file, whose directory is the top of the work tree.

```go
l, err := repo.Resolve("./arrived-as-files")
h, err := l.Hooks(entries)
```

Every one of those paths has to stay inside the checkout. One that leaves it yields an `*EscapeError` wrapping `ErrEscapesCheckout`, naming the pointer file that said so, the field it stood for, where it led and the root it had to stay inside — and nothing is followed. The question is asked twice: lexically, so that a path which never existed is refused all the same, and again after `filepath.EvalSymlinks` has followed as much of the path as exists, so that a `.git` symlinked out of the tree is refused just as a `gitdir: ../elsewhere` line is.

`Layout.Hooks` answers which directory a git run would take its hooks from — `core.hooksPath` when the configuration sets one, relative to the top of the work tree and with the last declaration winning, and the common directory's `hooks` otherwise — and lists every executable regular file in it. Symlinks are followed when deciding that, because a link to an executable is one more program git would start. A directory that is absent, or is no directory at all, runs nothing. A hooks directory outside the checkout is reported as `Outside` rather than refused: the configuration may ask for what a pointer file may not, and the point is to show it.

### Repositories inside the work tree

A checkout that arrived as files can carry repositories of its own. `Layout.EmbeddedBare` walks the work tree and reports every directory git discovery would open as a repository, asking of it what `setup.c` asks: a HEAD it can read, an `objects` directory and a `refs` directory.

```go
found, err := l.EmbeddedBare()
```

A HEAD it can read is the same question `validate_headref` asks: a symlink is a HEAD on its own, a `ref:` line has to name a ref, and anything else has to be a whole object name followed by nothing but whitespace — so a directory holding `objects`, `refs` and a HEAD full of junk is not reported, because git would not open it either.

The walk leaves out what is not an embedded repository: the checkout's own git and common directories, and any directory named `.git`, which is a checkout's own rather than one sitting in a work tree. A repository that was found is not walked into, because what sits inside it belongs to it. Symlinked directories are not followed, because a walk that followed them need not terminate, and a directory that cannot be read hides what it holds from git as well, so the walk skips it and reports the rest of the tree.

Each one comes back with its resolved directory, its path as it sits in the work tree, and its own `config` when it carries one — which is read and classified like any other. Their presence asks for one override of its own:

```
-c safe.bareRepository=explicit
```

That is `repo.DisarmEmbeddedBare`: it makes git refuse a repository it was not pointed at by name, so one sitting in a work tree cannot become the repository the next command runs in. `repo.EmbeddedBare` walks a directory the same way for a tree whose layout could not be resolved.

## Package `gitattr`

A driver the configuration defines runs nothing until the attributes bind it to a file that is there. `gitattr` reads the attributes a checkout would apply to its own paths — from `core.attributesFile`, from the `.gitattributes` of every directory of the work tree, and from `$GIT_DIR/info/attributes`, each read at the precedence git reads it — and answers which of the drivers the configuration defines those attributes name on files that exist.

```go
a, err := gitattr.Load(l.GitDir, l.Worktree, entries)
for _, d := range a.Report(entries) {
	if d.Armed() {
		fmt.Printf("%s.%s is bound to %v\n", d.Family, d.Name, d.Files)
	}
}
```

`Report` returns one `Driver` per named `filter.<name>`, `diff.<name>` and `merge.<name>` subsection, carrying the keys that defined it and the existing files whose `filter`, `diff` or `merge` attribute names it. A driver the attributes name on no file that exists comes back with no files at all: it is defined, not armed. An attribute a `!name` line left undecided binds nothing.

Patterns are matched as git matches them: one holding a slash is anchored to the directory of the file that declared it, one without is matched against the last component of a path at any depth below that directory, `**` crosses a directory only where it stands as a whole component, a bracket expression never matches a slash, and a pattern ending in `/` can only mean a directory and so names no file. A `[attr]` line defines a macro, and is counted only where git reads one — the top-level file, the git directory's, and the one `core.attributesFile` names — with `binary` defined before any of them, as git defines it. gitattributes has no negative patterns: a line whose pattern begins with `!` decides nothing and is reported in `Ignored`, naming the file and the line that carried it.

`Check` answers every attribute for one path, in the words `git check-attr` prints for it, and `Files` lists what the walk found: the files of the work tree, with the git directory, any directory named `.git` and symlinked directories left out. Patterns are matched and paths are read; no value an attribute carries is ever run.

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

A directory is also walked for the repositories sitting in its work tree. Each one is named by its path, its own configuration is classified alongside the checkout's, the report ends by listing them together, and `-c safe.bareRepository=explicit` joins the invocation that disarms the lot.

It exits 1 when at least one key names a program, 2 when a file cannot be read or is malformed, and 0 otherwise. Exit 0 is not a claim that the repository is safe, which is why every report ends with the catalog's gaps.

## Checked against git itself

`internal/gitoracle` makes a real git binary the oracle for the parser. It takes the binary from `GITDISARM_GIT`, or from `PATH`, records `git --version` in the test log, and asks it for

```
git config --file <file> --list --show-origin --null
```

with `GIT_CONFIG_NOSYSTEM=1` and `HOME` pointing at a throwaway directory, so the listing can only come from the file under test. The oracle only lists: no value in a configuration is ever run as a program.

The differential test writes synthetic configurations into a temporary directory and requires `Parse` to return exactly the keys and values git reports for them, and to refuse every file git refuses. With no git binary available it skips with an explicit message rather than passing quietly.

The same oracle answers how git reads one value as a boolean:

```
git config --type=bool --file <file> <key>
```

A table of `core.fsmonitor` spellings — mixed case, quoted, quotes stitched into the middle of a word, integers in every base and unit git accepts, and the boundary where its integer stops — requires the boolean/command split to accept and refuse exactly what git accepts and refuses, and to read the same truth. So a value git calls a boolean is never reported as a program, and a value git refuses is reported as a command with the text git itself read from the file.

The oracle also answers what git makes of a tree, by being run inside one. A bare repository that git itself created with `git init --bare` inside a synthetic checkout has to be the one the walk reports; git run from inside it has to call it a bare repository, which is the discovery the walk reads; and the same git, run from the same directory with `-c safe.bareRepository=explicit`, has to refuse to open it at all. A binary older than git 2.38, where `safe.bareRepository` arrived, skips that last question with a message naming its version.

And it answers what the attributes of a tree decide. A synthetic checkout whose attributes come from all three places git reads them is handed to `git init`, and

```
git check-attr -a -z -- <every file of the checkout>
```

has to print, path by path, exactly what `gitattr.Check` decides — so the files a driver is reported as armed on are the files git would hand it.

Run the tests with `go test ./...`, or pin the binary first: `GITDISARM_GIT=/usr/bin/git go test ./...`.

---

Made by polycratia — https://polycratia.com
