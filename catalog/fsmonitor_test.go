package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polycratia/gitdisarm/gitconfig"
	"github.com/polycratia/gitdisarm/internal/gitoracle"
)

// fsmonitorCases are the spellings core.fsmonitor can arrive with: the line a
// file carries, the value the parser reads from it, whether git reads that
// value as a boolean, and the truth it reads when it does.
var fsmonitorCases = []struct {
	line   string
	value  string
	isBool bool
	truth  bool
}{
	{`fsmonitor = true`, "true", true, true},
	{`fsmonitor = TRUE`, "TRUE", true, true},
	{`fsmonitor = TrUe`, "TrUe", true, true},
	{`fsmonitor = yes`, "yes", true, true},
	{`fsmonitor = On`, "On", true, true},
	{`fsmonitor = false`, "false", true, false},
	{`fsmonitor = FALSE`, "FALSE", true, false},
	{`fsmonitor = No`, "No", true, false},
	{`fsmonitor = oFF`, "oFF", true, false},
	{`fsmonitor = 1`, "1", true, true},
	{`fsmonitor = 0`, "0", true, false},
	{`fsmonitor = 2`, "2", true, true},
	{`fsmonitor = -1`, "-1", true, true},
	{`fsmonitor = 010`, "010", true, true},
	{`fsmonitor = 0x2`, "0x2", true, true},
	{`fsmonitor = 1k`, "1k", true, true},
	{`fsmonitor =`, "", true, false},
	{`fsmonitor`, "true", true, true},
	{`fsmonitor = ""`, "", true, false},
	{`fsmonitor = "true"`, "true", true, true},
	{`fsmonitor = "yes"`, "yes", true, true},
	{`fsmonitor = "  true"`, "  true", false, false},
	{`fsmonitor = "true "`, "true ", false, false},
	{`fsmonitor = " "`, " ", false, false},
	{`fsmonitor = tru`, "tru", false, false},
	{`fsmonitor = 2x`, "2x", false, false},
	{`fsmonitor = 0x`, "0x", false, false},
	{`fsmonitor = 4294967296`, "4294967296", false, false},
	{`fsmonitor = yes please`, "yes please", false, false},
	{`fsmonitor = .git/hooks/fsmonitor-watchman`, ".git/hooks/fsmonitor-watchman", false, false},
	{`fsmonitor = "sh -c id"`, "sh -c id", false, false},
}

func parseFsmonitor(t *testing.T, line string) gitconfig.Entry {
	t.Helper()
	in := "[core]\n\t" + line + "\n"
	entries, err := gitconfig.Parse(strings.NewReader(in), "config")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", in, err)
	}
	if len(entries) != 1 {
		t.Fatalf("Parse(%q) returned %d entries, want 1", in, len(entries))
	}
	return entries[0]
}

func TestParseBoolSpellings(t *testing.T) {
	for _, c := range fsmonitorCases {
		t.Run(c.value, func(t *testing.T) {
			truth, ok := parseBool(c.value)
			if ok != c.isBool {
				t.Fatalf("parseBool(%q) read it as a boolean = %t, want %t", c.value, ok, c.isBool)
			}
			if ok && truth != c.truth {
				t.Errorf("parseBool(%q) = %t, want %t", c.value, truth, c.truth)
			}
		})
	}
}

func TestClassifyFsmonitorBooleanOrCommand(t *testing.T) {
	for _, c := range fsmonitorCases {
		t.Run(c.line, func(t *testing.T) {
			e := parseFsmonitor(t, c.line)
			if e.Value != c.value {
				t.Fatalf("Parse() read %q, want %q", e.Value, c.value)
			}
			got := Classify(e)
			if c.isBool {
				if got.Class != Inert {
					t.Errorf("Classify() = %s, want inert for a value git reads as a boolean", got.Class)
				}
				if got.Disarm != "" {
					t.Errorf("Classify() disarm = %q, want none", got.Disarm)
				}
				return
			}
			if got.Class != Executes {
				t.Fatalf("Classify() = %s, want executes for a hook command", got.Class)
			}
			if got.Disarm != "-c core.fsmonitor=false" {
				t.Errorf("Classify() disarm = %q, want %q", got.Disarm, "-c core.fsmonitor=false")
			}
			if got.Entry.Value != c.value {
				t.Errorf("Classify() reported %q, want the command text %q", got.Entry.Value, c.value)
			}
		})
	}
}

func TestFsmonitorSplitAgreesWithGit(t *testing.T) {
	o, err := gitoracle.Find()
	if err != nil {
		if errors.Is(err, gitoracle.ErrNoGit) {
			t.Skipf("skipping: no git binary to check the boolean split against (%v); set %s to a git executable to run this test", err, gitoracle.EnvGit)
		}
		t.Fatalf("gitoracle.Find() error = %v", err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	t.Logf("oracle: %s (%s)", o.Version, o.Path)

	for _, c := range fsmonitorCases {
		t.Run(c.line, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte("[core]\n\t"+c.line+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			truth, err := o.Bool(path, "core.fsmonitor")
			var refused *gitoracle.RefusedError
			if err != nil && !errors.As(err, &refused) {
				t.Fatalf("Bool() error = %v", err)
			}
			gitBool := err == nil
			if !gitBool {
				t.Logf("git refused the value as a boolean: %s", refused.Stderr)
			}
			if gitBool != c.isBool {
				t.Fatalf("git read it as a boolean = %t, while the table says %t", gitBool, c.isBool)
			}

			e := parseFsmonitor(t, c.line)
			class := Classify(e).Class
			if ourBool := class == Inert; ourBool != gitBool {
				t.Fatalf("Classify() = %s, so boolean = %t, while git read it as a boolean = %t", class, ourBool, gitBool)
			}
			if !gitBool {
				return
			}
			if got, _ := parseBool(e.Value); got != truth {
				t.Errorf("parseBool(%q) = %t, want git's %t", e.Value, got, truth)
			}
		})
	}
}
