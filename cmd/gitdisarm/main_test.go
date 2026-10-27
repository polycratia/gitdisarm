package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := run(&out, &errOut, args)
	return code, out.String(), errOut.String()
}

func writeRepo(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return dir
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func wantContains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output does not contain %q; output was:\n%s", w, got)
		}
	}
}

func TestRunReportsProgramsAndTheirOverrides(t *testing.T) {
	dir := writeRepo(t, "[core]\n\tpager = sh -c id\n[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\trequired = true\n[user]\n\tname = someone\n")

	code, out, errOut := runCLI(t, dir)
	if code != exitExecutes {
		t.Errorf("run() = %d, want %d", code, exitExecutes)
	}
	if errOut != "" {
		t.Errorf("run() wrote %q to stderr, want nothing", errOut)
	}
	wantContains(t, out,
		"executes  core.pager = \"sh -c id\"",
		"disarm: -c core.pager=cat",
		"executes  filter.lfs.clean = \"git-lfs clean -- %f\"",
		"unknown   user.name = \"someone\"",
		"keys that name a program: 2; keys this catalog does not decide: 1",
		"git -c core.pager=cat -c filter.lfs.clean=cat -c filter.lfs.required=false <command>",
		"hook.<name>.command",
	)
	if strings.Contains(out, "inert") {
		t.Errorf("run() reported an inert key without -all; output was:\n%s", out)
	}
}

func TestRunSeparatesCommandFlagsFromConfigOverrides(t *testing.T) {
	dir := writeRepo(t, "[diff]\n\texternal = sh -c id\n[diff \"odt\"]\n\ttextconv = sh -c id\n")

	code, out, _ := runCLI(t, dir)
	if code != exitExecutes {
		t.Errorf("run() = %d, want %d", code, exitExecutes)
	}
	wantContains(t, out,
		"These belong to the command that honours them rather than to git:",
		"--no-ext-diff --no-textconv",
	)
	if strings.Contains(out, "  git ") {
		t.Errorf("run() printed a git invocation with no -c override; output was:\n%s", out)
	}
}

func TestRunRepeatsNoOverrideTwice(t *testing.T) {
	dir := writeRepo(t, "[core]\n\tpager = sh -c id\n[pager]\n\tlog = sh -c id\n[core]\n\tpager = less\n")

	_, out, _ := runCLI(t, dir)
	if n := strings.Count(out, "git -c core.pager=cat -c pager.log=false <command>"); n != 1 {
		t.Errorf("combined invocation appeared %d times, want once; output was:\n%s", n, out)
	}
}

func TestRunWithoutExecutorsStillPrintsTheGaps(t *testing.T) {
	dir := writeRepo(t, "[core]\n\tfsmonitor = true\n")

	code, out, _ := runCLI(t, dir)
	if code != exitClean {
		t.Errorf("run() = %d, want %d", code, exitClean)
	}
	wantContains(t, out,
		"keys that name a program: 0",
		"Not decided here, and still able to end in a program being run:",
		"hook.<name>.command",
		".git/hooks/<name>",
	)
	if strings.Contains(out, "executes") {
		t.Errorf("run() reported an executor for an inert key; output was:\n%s", out)
	}
}

func TestRunAllReportsInertKeys(t *testing.T) {
	dir := writeRepo(t, "[core]\n\tfsmonitor = true\n")

	code, out, _ := runCLI(t, "-all", dir)
	if code != exitClean {
		t.Errorf("run() = %d, want %d", code, exitClean)
	}
	wantContains(t, out, "inert     core.fsmonitor = \"true\"")
}

func TestRunReadsABareRepositoryAndAPlainFile(t *testing.T) {
	bare := t.TempDir()
	if err := os.WriteFile(filepath.Join(bare, "config"), []byte("[core]\n\teditor = sh -c id\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if code, out, _ := runCLI(t, bare); code != exitExecutes {
		t.Errorf("run() on a bare repository = %d, want %d; output was:\n%s", code, exitExecutes, out)
	}

	file := writeFile(t, "gitconfig", "[core]\n\teditor = sh -c id\n")
	code, out, _ := runCLI(t, file)
	if code != exitExecutes {
		t.Errorf("run() on a file = %d, want %d", code, exitExecutes)
	}
	wantContains(t, out, "disarm: -c core.editor=true")
}

func TestRunRefusals(t *testing.T) {
	malformed := writeRepo(t, "[a]\nb = \"x\n")
	code, out, errOut := runCLI(t, malformed)
	if code != exitError {
		t.Errorf("run() on a malformed file = %d, want %d", code, exitError)
	}
	if out != "" {
		t.Errorf("run() reported findings for a file it refused:\n%s", out)
	}
	wantContains(t, errOut, "unterminated quoted value")

	if code, _, errOut := runCLI(t, filepath.Join(t.TempDir(), "missing")); code != exitError || errOut == "" {
		t.Errorf("run() on a missing path = %d with stderr %q, want %d and a message", code, errOut, exitError)
	}

	empty := t.TempDir()
	if code, _, errOut := runCLI(t, empty); code != exitError || !strings.Contains(errOut, "bare repository") {
		t.Errorf("run() on a directory with no config = %d with stderr %q, want %d and an explanation", code, errOut, exitError)
	}

	if code, _, _ := runCLI(t, "-nosuchflag"); code != exitError {
		t.Errorf("run() with an unknown flag = %d, want %d", code, exitError)
	}
}
