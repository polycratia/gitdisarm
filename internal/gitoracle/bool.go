package gitoracle

import (
	"fmt"
	"strings"
)

// Bool asks git whether it reads the value a configuration file gives a key as
// a boolean, and with which truth:
//
//	git config --type=bool --file <file> <key>
//
// A value git refuses to read as a boolean yields a *RefusedError. It only
// asks: the value is never run.
func (o *Oracle) Bool(file, key string) (bool, error) {
	out, err := o.run("config", "--type=bool", "--file", file, key)
	if err != nil {
		return false, err
	}
	switch s := strings.TrimSpace(string(out)); s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("git printed %q for --type=bool %s, want true or false", s, key)
	}
}
