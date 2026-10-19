// Package gitconfig reads git configuration files the way git reads them, or
// refuses them.
package gitconfig

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Entry is a single key/value pair in the order it appeared in the input.
type Entry struct {
	Section    string
	Subsection string
	Key        string
	Value      string
	File       string
	Line       int
}

// ParseError reports the file and line of the construct that was refused.
type ParseError struct {
	File string
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
}

// Parse reads a git configuration file and returns its entries in order. The
// name labels entries and errors. A malformed file yields a *ParseError and no
// entries.
func Parse(r io.Reader, name string) ([]Entry, error) {
	p := &parser{file: name}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		p.lines = append(p.lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p.parse()
}

type parser struct {
	file    string
	lines   []string
	entries []Entry
	section string
	sub     string
}

func (p *parser) parse() ([]Entry, error) {
	if len(p.lines) > 0 {
		p.lines[0] = strings.TrimPrefix(p.lines[0], "\ufeff")
	}
	for i := 0; i < len(p.lines); i++ {
		trimmed := strings.TrimLeft(p.lines[i], " \t")
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}
		if trimmed[0] == '[' {
			if err := p.parseSectionHeader(i); err != nil {
				return nil, err
			}
			continue
		}
		last, err := p.parseEntry(i)
		if err != nil {
			return nil, err
		}
		i = last
	}
	return p.entries, nil
}

func (p *parser) parseSectionHeader(idx int) error {
	line := p.lines[idx]
	pos := strings.IndexByte(line, '[') + 1
	start := pos
	for pos < len(line) && isSectionByte(line[pos]) {
		pos++
	}
	section := strings.ToLower(line[start:pos])
	if section == "" {
		return p.errf(idx, "invalid section name")
	}
	sub := ""
	for pos < len(line) && isSpace(line[pos]) {
		pos++
	}
	if pos < len(line) && line[pos] == '"' {
		pos++
		var b []byte
		closed := false
		for pos < len(line) {
			c := line[pos]
			if c == '\\' {
				pos++
				if pos >= len(line) {
					break
				}
				e := line[pos]
				if e != '"' && e != '\\' {
					return p.errf(idx, "unknown escape sequence \\%c in subsection name", e)
				}
				b = append(b, e)
				pos++
				continue
			}
			pos++
			if c == '"' {
				closed = true
				break
			}
			b = append(b, c)
		}
		if !closed {
			return p.errf(idx, "unterminated subsection name")
		}
		sub = string(b)
		for pos < len(line) && isSpace(line[pos]) {
			pos++
		}
		if pos >= len(line) || line[pos] != ']' {
			return p.errf(idx, "invalid section header")
		}
		pos++
	} else {
		if pos >= len(line) || line[pos] != ']' {
			return p.errf(idx, "invalid section header")
		}
		pos++
		if dot := strings.IndexByte(section, '.'); dot >= 0 {
			sub = section[dot+1:]
			section = section[:dot]
			if section == "" || sub == "" {
				return p.errf(idx, "invalid section name")
			}
		}
	}
	rest := strings.TrimLeft(line[pos:], " \t")
	if rest != "" && rest[0] != '#' && rest[0] != ';' {
		return p.errf(idx, "junk after section header")
	}
	p.section, p.sub = section, sub
	return nil
}

func (p *parser) parseEntry(idx int) (int, error) {
	line := p.lines[idx]
	pos := 0
	for isSpace(line[pos]) {
		pos++
	}
	if p.section == "" {
		return idx, p.errf(idx, "key outside of any section")
	}
	if !isAlpha(line[pos]) {
		return idx, p.errf(idx, "invalid key character %q", string(line[pos]))
	}
	start := pos
	for pos < len(line) && isKeyByte(line[pos]) {
		pos++
	}
	key := strings.ToLower(line[start:pos])
	for pos < len(line) && isSpace(line[pos]) {
		pos++
	}
	if pos >= len(line) || line[pos] == '#' || line[pos] == ';' {
		p.add(key, "true", idx)
		return idx, nil
	}
	if line[pos] != '=' {
		return idx, p.errf(idx, "invalid key character %q", string(line[pos]))
	}
	value, last, err := p.parseValue(idx, pos+1)
	if err != nil {
		return idx, err
	}
	p.add(key, value, idx)
	return last, nil
}

func (p *parser) parseValue(idx, pos int) (string, int, error) {
	line := p.lines[idx]
	for pos < len(line) && isSpace(line[pos]) {
		pos++
	}
	var buf []byte
	// Whitespace outside quotes is only kept when more value follows it, so
	// track how much of buf is still significant.
	keep := 0
	quoted := false
value:
	for {
		if pos >= len(line) {
			if quoted {
				return "", 0, p.errf(idx, "unterminated quoted value")
			}
			break value
		}
		c := line[pos]
		switch {
		case c == '"':
			quoted = !quoted
			pos++
		case c == '\\':
			pos++
			if pos >= len(line) {
				if idx+1 >= len(p.lines) {
					return "", 0, p.errf(idx, "unterminated line continuation")
				}
				idx++
				line = p.lines[idx]
				pos = 0
				continue
			}
			var r byte
			switch e := line[pos]; e {
			case 'n':
				r = '\n'
			case 't':
				r = '\t'
			case 'b':
				r = '\b'
			case '\\':
				r = '\\'
			case '"':
				r = '"'
			default:
				return "", 0, p.errf(idx, "unknown escape sequence \\%c", e)
			}
			buf = append(buf, r)
			keep = len(buf)
			pos++
		case !quoted && (c == '#' || c == ';'):
			break value
		case !quoted && isSpace(c):
			buf = append(buf, c)
			pos++
		default:
			buf = append(buf, c)
			keep = len(buf)
			pos++
		}
	}
	return string(buf[:keep]), idx, nil
}

func (p *parser) add(key, value string, idx int) {
	p.entries = append(p.entries, Entry{
		Section:    p.section,
		Subsection: p.sub,
		Key:        key,
		Value:      value,
		File:       p.file,
		Line:       idx + 1,
	})
}

func (p *parser) errf(idx int, format string, args ...any) *ParseError {
	return &ParseError{File: p.file, Line: idx + 1, Msg: fmt.Sprintf(format, args...)}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isSectionByte(c byte) bool { return isAlpha(c) || isDigit(c) || c == '-' || c == '.' }

func isKeyByte(c byte) bool { return isAlpha(c) || isDigit(c) || c == '-' }
