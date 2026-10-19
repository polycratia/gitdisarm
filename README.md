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

Run the tests with `go test ./...`.

---

Made by polycratia — https://polycratia.com
