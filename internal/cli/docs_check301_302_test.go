package cli

// Checks 301 and 302 — the stateless orchestrator's daemon and its cutover —
// registered as ONE script each rather than as a list of steps. Both are
// owner-run on the owner's own machines, both span something a single run
// cannot measure (a reboot and two platforms for 301, a rollback allowed at any
// point inside 14 days for 302), and neither had a script before this work:
// the register's rule of 2026-11-07 is that an owner check ships as a script,
// so what is pinned here is that each section names it, says the phases, and
// points at files that are really there. A row or a section that named a script
// nobody wrote is a promise, and a section that listed steps instead of a script
// is the hand work that rule exists to remove.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// check301Heading and check302Heading are the openings of the two register
// sections: registerSection matches a prefix, so the pin is the number and the
// subject and not the whole parenthetical title.
const (
	check301Heading = "## Check 301: the daemon under launchd and systemd"
	check302Heading = "## Check 302: the cutover and the manual rollback"
	check301RunLine = "**Run it:** `bash scripts/live-checks/check301.sh start`"
	check302RunLine = "**Run it:** `bash scripts/live-checks/check302.sh before --installation <name> --scripts <dir>`"
)

// liveCheckRow returns the register's summary-table row for a check number.
func liveCheckRow(register, number string) string {
	re := regexp.MustCompile(`(?m)^\|\s*` + number + `\s*\|.*$`)
	return re.FindString(register)
}

// TestDocsCheck301IsOneScriptTheOwnerRuns pins Check 301's registration: the
// section names the script and its phases, says the facts the check is for are
// the register's own four readings, says no agent runs any step of it, and the
// script and its test exist. The phases are pinned by name because the check
// spans a reboot: a section that printed one command would send the owner
// looking for the next step in prose, which is what the script is there to
// carry.
func TestDocsCheck301IsOneScriptTheOwnerRuns(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	section := registerSection(t, register, check301Heading)
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		strings.ToLower(check301RunLine),
		// Every phase the script takes, and the window between them: `start`
		// before the reboot, `after-reboot` after it, then T7's half.
		"check301.sh after-reboot",
		"check301.sh upgrade",
		"check301.sh update",
		// The one thing the owner does, and the state the phases share.
		"the reboot",
		"bash scripts/live-checks/check301.sh start",
		"fugaro/check301",
		// The register's own rule for an owner-run check.
		"no agent runs any step of it",
		// The boundary the check measures: an update re-renders a unit that is
		// there and never installs one, which is `setup`'s and `orchestrator
		// install`'s.
		"never **installs** a unit",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 301's section does not say %q: the section is where the owner reads what the check settles and how to run it", want)
		}
	}
	// Exit 2 is a run that could not tell, and the block is what the owner
	// pastes back: a section that does not name the block leaves the readings
	// nowhere to land.
	if !strings.Contains(section, "CHECK 301 RESULT") {
		t.Errorf("Check 301's section does not name the `CHECK 301 RESULT` block it ends with")
	}
	for _, path := range []string{"scripts/live-checks/check301.sh", "scripts/live-checks/check301_test.sh"} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is named by Check 301 and does not exist: %v", path, err)
		}
	}
	row := liveCheckRow(register, "301")
	if row == "" {
		t.Fatalf("%s's register has no `| 301 |` row: Check 301 is written below it", liveChecksDoc)
	}
	if !strings.Contains(row, "USER-RUN, NOT RUN.") {
		t.Errorf("Check 301's row does not read `USER-RUN, NOT RUN.`: the check is the owner's and has not been run")
	}
}

// TestDocsCheck302IsOneScriptTheOwnerRuns pins Check 302's registration: the
// section names the script, its three phases and `--dry-run`, says the rollback
// is manual and no rollback command exists, says it runs on the owner's own
// installation and never on `belong`, and the script and its test exist. The
// "never belong" half is pinned in words because it is the one thing about this
// check a reader could get wrong by reading the register alone: the sandbox an
// agent's session uses is exactly the wrong installation to cut over.
func TestDocsCheck302IsOneScriptTheOwnerRuns(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	section := registerSection(t, register, check302Heading)
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		strings.ToLower(check302RunLine),
		// The three phases and the flag that changes nothing.
		"check302.sh before --installation",
		"cutover --dump <file>",
		"rollback",
		"--dry-run",
		// The state the phases share, and the confirmation each asks for.
		"fugaro/check302",
		"no agent runs any step of it",
		// T1's reading, in the section of the check that verifies it: the
		// rollback is the steps by hand and no command stands in for them.
		"no rollback command exists",
		"the 14 days",
		// The installation this check is for, and the one it never touches:
		// the refusal is `belong` by name, which is what the script's own
		// guard refuses, and the section may not promise a sandbox list the
		// register never wrote and the script does not carry.
		"never `belong`",
		"refuses the sandbox installation `belong`",
		"names no other installation as a sandbox",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 302's section does not say %q: the section is where the owner reads what the check settles and how to run it", want)
		}
	}
	if !strings.Contains(section, "CHECK 302 RESULT") {
		t.Errorf("Check 302's section does not name the `CHECK 302 RESULT` block it ends with")
	}
	for _, path := range []string{"scripts/live-checks/check302.sh", "scripts/live-checks/check302_test.sh"} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is named by Check 302 and does not exist: %v", path, err)
		}
	}
	// The refusal the section describes is the one the script carries: its
	// guard refuses the `belong` family and nothing else, so a section
	// promising a wider refusal — a sandbox allow-list the register never
	// wrote — would describe a reading the owner never gets.
	guard, err := os.ReadFile("../../scripts/live-checks/check302.sh")
	if err != nil {
		t.Fatalf("check302.sh cannot be read: %v", err)
	}
	if !strings.Contains(string(guard), "belong | belong-*") {
		t.Errorf("check302.sh no longer refuses the `belong` family by name, which is the refusal Check 302's section describes")
	}
	row := liveCheckRow(register, "302")
	if row == "" {
		t.Fatalf("%s's register has no `| 302 |` row: Check 302 is written below it", liveChecksDoc)
	}
	if !strings.Contains(row, "USER-RUN, NOT RUN.") {
		t.Errorf("Check 302's row does not read `USER-RUN, NOT RUN.`: the check is the owner's and has not been run")
	}
}

// TestDocsCheck302NeverInventsARollbackCommand: the section may not offer a
// command for the way back. T1 ruled that the rollback is the steps by hand and
// that no `fugaro orchestrator rollback` is built, and the family is checked for
// one too — a section promising an undo this release does not have is how a
// reader ends up typing a command that does not exist with the scripts already
// re-armed. The one place the words may appear is the sentence that says there
// is none, which is why the pin is the phrase and not the string.
func TestDocsCheck302NeverInventsARollbackCommand(t *testing.T) {
	section := flattenDoc(registerSection(t, readDoc(t, liveChecksDoc), check302Heading))
	if !strings.Contains(section, "No rollback command exists") {
		t.Errorf("Check 302's section does not say `No rollback command exists`: the way back is the steps by hand (T1)")
	}
	for _, sub := range newOrchestratorCmd().Commands() {
		if sub.Name() == "rollback" {
			t.Errorf("the orchestrator family has a rollback command, and Check 302's section promises there is none")
		}
	}
}
