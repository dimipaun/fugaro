package cli

import (
	"strings"
	"testing"
)

// Check 130 — the licence detectors after the open-core skeleton (licensing plan
// L1.10, run by L1.11, the owner's O2 answer of 2026-10-24) — is owner-run, NOT
// RUN, and it ships as ONE script: scripts/live-checks/check130.sh, with its own
// test scripts/live-checks/check130_test.sh driven by a fake curl and no
// network. No agent runs any step of it, and no agent runs its test against
// anything live either.
//
// The register's section is where the check's own text lives — the guard, the
// three steps and the pass/fail criteria — and the script is that text made
// runnable. So the pin is threefold: the section names the one command and
// still says the check has not been run, the register's row agrees, and the
// script keeps the properties the section promises about it — the repository
// from the checkout's origin cross-checked against go.mod, the `belong`
// refusal, the three anonymous GETs and nothing else, the block and its keys,
// the exit trap that removes the scratch directory even after a failed
// assertion, and `--dry-run` fetching nothing.
//
// The script itself is executed by scripts/live_checks_test.go's
// TestLiveCheckScripts against that fake; what is read here is its text, which
// is what a reader of the register is promised.
const (
	check130Script   = "scripts/live-checks/check130.sh"
	check130Test     = "scripts/live-checks/check130_test.sh"
	check130Heading  = "## Check 130: the licence detectors after the open-core skeleton"
	check130RunLine  = "**Run it:** `bash scripts/live-checks/check130.sh`"
	check130Row      = "130"
	check130RowRun   = "`bash scripts/live-checks/check130.sh`"
	check130RowState = "**USER-RUN, NOT RUN.**"
)

// TestDocsCheck130IsOneScriptTheOwnerRuns: the section tells the owner to run
// the script, and it says the check has not been run — the row's status and the
// section's own banner, which move together or not at all.
func TestDocsCheck130IsOneScriptTheOwnerRuns(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	section := registerSection(t, doc, check130Heading)
	for _, want := range []string{
		"**NOT RUN.**",
		check130RunLine,
		"no agent runs any step of it",
		"ONE runnable, idempotent script",
		check130Test,
		"CHECK 130 RESULT: PASS|FAIL",
		"BLOCK_KEYS",
		"MANUAL:",
		"**Cost and duration:**",
		"**Paste back**",
		"**Restore.**",
		"never `belong`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s: the Check 130 section lacks %q", liveChecksDoc, want)
		}
	}

	row := registerRow(t, doc, check130Row)
	for _, want := range []string{check130RowRun, check130Test, check130RowState, "never `belong`"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: the | 130 | row lacks %q", liveChecksDoc, want)
		}
	}
}

// TestDocsCheck130ScriptKeepsItsPromises: the section's claims about the script
// are claims about this tree, so each one is read out of the script rather than
// trusted to the prose.
func TestDocsCheck130ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check130Script)
	for _, want := range []string{
		// The guard the section states: the repository from the checkout's
		// origin, cross-checked against go.mod, and no way to name another one.
		`git -C "$ROOT" remote get-url origin`,
		`[ "$MOD" = "github.com/${REPO}" ]`,
		// The refusal the standing rule requires.
		"*/belong/* | */edgeweb/* | */edgeserver/*",
		// The three anonymous GETs, on api.github.com and nowhere else.
		`https://api.github.com/repos/${REPO}`,
		// The criteria the section's "Passes when" paragraph states.
		`[ "$spdx" = MIT ] && [ "$key" = mit ] && [ "$path" = LICENSE ]`,
		// The block, the verdict and the trap that cleans up on every path.
		"BLOCK_KEYS=(setup repository belong licensing-md links licence-api restore)",
		"CHECK 130 RESULT: %s",
		"trap finish EXIT",
		"remove_scratch",
		// --dry-run and the one manual line.
		"--dry-run",
		"MANUAL: %s",
		// The guards a run may not pass with: a trace, and a missing tool.
		"bash -x",
		"XTRACE_AT_START",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s lacks %q, which the register's section promises about it", check130Script, want)
		}
	}

	// The repository is read from the checkout and from nowhere else: a knob
	// that could point this check at another repository, another branch or
	// another host is exactly what the section says does not exist. Comments
	// are not code — the header is where the script says it has no such knob,
	// and where the `belong` refusal is explained.
	var code []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	runnable := strings.Join(code, "\n")
	if strings.Contains(runnable, "CHECK130_") {
		t.Errorf("%s carries a CHECK130_ variable; the section says the repository comes from the checkout and the host is api.github.com, with no variable that can point it elsewhere", check130Script)
	}
	// `belong` is not in this list: the one line of code that names it is the
	// refusal, asserted above.
	for _, banned := range []string{"gcloud", "fugaro", "--project"} {
		if strings.Contains(runnable, banned) {
			t.Errorf("%s runs a line naming %q: this check touches no cloud project, no installation and no credential", check130Script, banned)
		}
	}

	// Every key the block prints is named by the section, in the order the
	// script prints them: a reader diffing a pasted block against the section
	// must find the same list.
	section := registerSection(t, readDoc(t, liveChecksDoc), check130Heading)
	keys := blockKeys(t, script)
	if len(keys) == 0 {
		t.Fatalf("%s: BLOCK_KEYS parsed empty", check130Script)
	}
	at := 0
	for _, key := range keys {
		i := strings.Index(section[at:], "`"+key+"`")
		if i < 0 {
			t.Errorf("%s: the Check 130 section does not name the block key %q, which %s prints", liveChecksDoc, key, check130Script)
			continue
		}
		at += i
	}

	// The test the register names is the one the Go test runs: a script whose
	// test is not wired in is a script nothing checks.
	if got := readRepoFile(t, "scripts/live_checks_test.go"); !strings.Contains(got, `{"check130.sh", "check130_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not run %s", check130Test)
	}
}
