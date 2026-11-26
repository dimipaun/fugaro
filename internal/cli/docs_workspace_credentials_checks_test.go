package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Checks 850 and 851 — the workspace's personal half — as the register writes
// them, and the promises the two scripts make about themselves.
//
// WC12 of docs/workspace-credentials/ writes the two owner-run checks that
// carry the personal half's evidence after the release that ships it: **850**,
// run BY the person under test on a real workspace, where a roster person who
// holds nothing but the two invoke grants stores a token at the workspace's own
// project and is served it in the members whose switch is on; and **851**,
// `fugaro update` on a workspace and on a member both staged at the previous
// release, with a run in flight on each, beside a member that never turns the
// switch on and must see no change. Neither is run by an agent, and neither is
// in this tree's CI: what these tests pin is the registration and the scripts'
// own shape, because a row naming a script nobody wrote, and a script that lost
// the property its section rests on, are the two ways this work rots silently.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and the register-wide backstop that would
// assert the pairing is deliberately absent (docs_compute_backend_test.go
// asserts its absence), so each work's own docs tests carry it for its own
// numbers. WC12's red step is this file's own first assertion: a row that names
// no script fails here, which is the row Check 850 and Check 851 would have
// been with their scripts left unwritten.
const (
	check850Number = "850"
	check851Number = "851"

	check850Script = "scripts/live-checks/check-850.sh"
	check850Test   = "scripts/live-checks/check-850_test.sh"
	check851Script = "scripts/live-checks/check-851.sh"
	check851Test   = "scripts/live-checks/check-851_test.sh"

	// The ONE command each row and each section prints, as the scripts are named.
	check850Run = "bash scripts/live-checks/check-850.sh"
	check851Run = "bash scripts/live-checks/check-851.sh"

	check850Block = "CHECK 850 RESULT"
	check851Block = "CHECK 851 RESULT"

	check85xPlanDoc = "workspace-credentials/implementation-plan.md"

	// The switch the personal half is turned on with, read off the published
	// policy node in both scripts and in both sections: a check of this work
	// that never names it is a check of another half.
	check85xSwitchKey = "personal_workspace_tokens"
)

// check85xNumbers are the two checks, in the order the register takes them.
var check85xNumbers = []string{check850Number, check851Number}

// TestDocsChecks850And851AreRegistered: each check has both halves of its
// registration, each half says it has not been run, that the owner runs it,
// that no agent runs any step of it, that it is never pointed at `belong`, and
// which task wrote it — and each half names the script, the sibling test and
// the plan they come from.
func TestDocsChecks850And851AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range []struct {
		number, run, test, block string
		section                  []string
	}{
		{check850Number, check850Run, check850Test, check850Block, []string{
			// The person under test, and the two grants the whole check rests on.
			"run it as the person under test",
			"check850_person",
			"nothing but the two invoke grants",
			"fugaro secrets user set",
			// The three unverified items and the refusals it settles.
			"u5", "u6", "u10", "r1", "r2", "r3",
			"slip_owner_removed",
			"removed the person from the roster",
			// The boundary U5's accessor is read against, and the two switch
			// readings (on in a member, absent on the one left alone).
			"prefix-conditioned",
			"d3141", "d3146", "d3148", "d2972",
			// What the block is and what its exit statuses mean.
			"exit 2",
		}},
		{check851Number, check851Run, check851Test, check851Block, []string{
			// What it settles, and the rule it comes from.
			"r11",
			"hard rule",
			// The three readings.
			"no warning",
			"in flight: update waits for none and changes nothing they hold",
			"never turns the switch on",
			"credsvc: audit",
			"d2972",
			// Check 773's half of the same subject, named so a reader knows why
			// the check exists twice.
			"check 773",
			// The two updates and the two runs are the owner's, not the script's.
			"this script runs no update",
			"exit 2",
		}},
	} {
		// Lowercased, because the register writes its markers in capitals and the
		// pins below are the sentence and not its case.
		row := strings.ToLower(flattenDoc(registerRow(t, register, c.number)))
		for _, want := range []string{
			// The register's own marker for an owner-run check that has not been
			// run: a row that lost either half reads as something an agent did.
			"**owner-run, not run.**",
			"no agent runs any step of it",
			// The installation and the session this check must never touch.
			"never `belong`",
			strings.ToLower(c.run),
			c.test,
			// The task that wrote it and the plan it comes from.
			"wc12",
			check85xPlanDoc,
			check85xSwitchKey,
		} {
			if !strings.Contains(row, want) {
				t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the command you type, the sibling test beside the script and the plan it comes from", c.number, want)
			}
		}

		// Flattened and folded, because the register's prose wraps: a pin that
		// missed a phrase across a line break would be a pin on the line breaks.
		section := strings.ToLower(flattenDoc(registerSection(t, register, "\n## Check "+c.number+":")))
		for _, want := range append([]string{
			"**not run.**",
			"no agent runs any step of it",
			"never run on `belong`",
			strings.ToLower(c.run),
			c.test,
			"wc12",
			check85xPlanDoc,
			check85xSwitchKey,
			// The block the owner pastes back, both of its ends, and the plan a
			// first run prints instead of measuring anything.
			strings.ToLower(c.block),
			strings.ToLower(c.block) + " end (exit n)",
			"--dry-run",
			"--help",
			// The knobs an owner sets are named where an owner reads them.
			"check" + c.number + "_",
		}, c.section...) {
			if !strings.Contains(section, want) {
				t.Errorf("Check %s's section does not say %q: the section is what the owner reads while running it, and WC12 writes it", c.number, want)
			}
		}
	}
}

// check85xBelongRefusalRE is what a line of one of the two scripts mentioning
// `belong` has to look like to be permitted: a guard that refuses the
// installation by name. The scripts are never pointed at it, and every mention
// is the refusal saying so.
var check85xBelongRefusalRE = regexp.MustCompile(`stop |die |never|not `)

// TestDocsChecks850And851ScriptsKeepTheirPromises: the four files exist, the Go
// runner in scripts/live_checks_test.go drives both sibling tests (a check
// added without its entry is a test no job runs), and each script keeps the
// properties its section and its own header promise ...
func TestDocsChecks850And851ScriptsKeepTheirPromises(t *testing.T) {
	for _, p := range []string{check850Script, check850Test, check851Script, check851Test} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("Check 850/851 names %s and there is no such file: %v", p, err)
		}
	}

	runner := readDoc(t, "scripts/live_checks_test.go")
	for _, pair := range []string{
		`{"check-850.sh", "check-850_test.sh"}`,
		`{"check-851.sh", "check-851_test.sh"}`,
	} {
		if !strings.Contains(runner, pair) {
			t.Errorf("scripts/live_checks_test.go does not carry %s: the runner drives every check's sibling test, and a check added without its entry is a test no job runs", pair)
		}
	}

	for _, c := range []struct {
		number, script, block string
		want                  []string
	}{
		{check850Number, check850Script, check850Block, []string{
			// The shell the check runs under, and the block it prints.
			"set -Eeuo pipefail",
			"CHECK 850 RESULT END (exit",
			"--dry-run",
			"--help",
			// No agent runs it: the guard names the markers a coding agent's
			// session carries, and the header says so.
			"CLAUDECODE",
			"no agent runs any step of it",
			// The person under test is who it is about, and the guard refuses any
			// other account.
			check85xSwitchKey,
			"CHECK850_PERSON",
			"the person under test is",
			// U6's act, U10's read, the two refusals, and the conditioned read.
			"secrets user set",
			"config layer",
			"slip_owner_removed",
			"credsvc: audit",
			"fugaro_u_",
		}},
		{check851Number, check851Script, check851Block, []string{
			"set -Eeuo pipefail",
			"CHECK 851 RESULT END (exit",
			"--dry-run",
			"--help",
			"CLAUDECODE",
			"no agent runs any step of it",
			check85xSwitchKey,
			// The HARD RULE's own sentence, the doctor read on both sides, and
			// the service's lines for the member left alone.
			"in flight: update waits for none and changes nothing they hold",
			"fugaro project policy show",
			"credsvc: audit",
		}},
	} {
		script := readRepoFile(t, c.script)
		for _, want := range c.want {
			if !strings.Contains(script, want) {
				t.Errorf("%s no longer carries %q, which Check %s's section and its own header rest on", c.script, want, c.number)
			}
		}
		// `belong` is never a target: every line naming it is a guard refusing
		// the installation, which is the machine-checkable half of the rule.
		for _, line := range strings.Split(script, "\n") {
			if !strings.Contains(line, "belong") {
				continue
			}
			if !check85xBelongRefusalRE.MatchString(line) {
				t.Errorf("%s names belong on a line that is not a refusal: %q", c.script, strings.TrimSpace(line))
			}
		}
		// A check that ate its own block header would leave the owner with
		// nothing to paste back, and the register's rows point at it.
		if !strings.Contains(script, c.block) {
			t.Errorf("%s does not print %q", c.script, c.block)
		}
	}
}
