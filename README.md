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
