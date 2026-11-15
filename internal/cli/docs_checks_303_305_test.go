package cli

import (
	"strings"
	"testing"
)

// Checks 303, 304 and 305 — the bounded decision agent and the cost of one cycle,
// the handoff between two people, and the MCP state group served (the stateless
// orchestrator plan's O12, O17 and O16b; docs/stateless-orchestrator) — are
// owner-run and ship as ONE script each: scripts/live-checks/check303.sh,
// check304.sh and check305.sh, with their own tests beside them, driven against
// fakes on PATH by scripts/live_checks_test.go.
//
// The register is where those checks were written as steps, so the pin is the
// rule CONTRIBUTING.md states for a converted check: the register's ROW names
// the one command to run — the row is what the owner reads first, and a row that
// says "see below" and names no command is the step list this conversion
// replaced — and the section carries the same command. Check 305's row is also
// where the readings used to be described as hand-run: the script makes them
// itself, so that sentence cannot come back.
const (
	check303Script  = "scripts/live-checks/check303.sh"
	check303Test    = "scripts/live-checks/check303_test.sh"
	check303Heading = "## Check 303: the bounded decision agent"
	check303Row     = "303"
	check303RowRun  = "`bash scripts/live-checks/check303.sh --installation <name> --yes`"
	check303RunLine = "bash scripts/live-checks/check303.sh --installation <name> --yes"

	check304Script  = "scripts/live-checks/check304.sh"
	check304Test    = "scripts/live-checks/check304_test.sh"
	check304Heading = "## Check 304: the handoff between two people"
	check304Row     = "304"
	check304RowRun  = "`bash scripts/live-checks/check304.sh first-person --installation <name> --from <user:address> --to <user:address>`"
	check304RunLine = "bash scripts/live-checks/check304.sh first-person"

	check305Script  = "scripts/live-checks/check305.sh"
	check305Test    = "scripts/live-checks/check305_test.sh"
	check305Heading = "## Check 305: the MCP state group served"
	check305Row     = "305"
	check305RowRun  = "`bash scripts/live-checks/check305.sh --installation <name> [--door <url>]`"
	check305RunLine = "bash scripts/live-checks/check305.sh --installation <name> [--door <url>]"
)

// TestDocsChecks303To305NameTheScriptInTheRowAndTheSection: the row names the one
// command and the section says to run it, for each of the three.
func TestDocsChecks303To305NameTheScriptInTheRowAndTheSection(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	for _, c := range []struct {
		check, heading, row, rowRun, runLine, script, test string
	}{
		{"Check 303", check303Heading, check303Row, check303RowRun, check303RunLine, check303Script, check303Test},
		{"Check 304", check304Heading, check304Row, check304RowRun, check304RunLine, check304Script, check304Test},
		{"Check 305", check305Heading, check305Row, check305RowRun, check305RunLine, check305Script, check305Test},
	} {
		section := registerSection(t, doc, c.heading)
		for _, want := range []string{
			"**Run it:**",
			c.runLine,
			"no agent runs any step of it",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("%s: the section lacks %q", liveChecksDoc, want)
			}
		}

		row := registerRow(t, doc, c.row)
		for _, want := range []string{
			c.rowRun,
			"**no agent runs any step of it**",
			"**USER-RUN, NOT RUN.**",
			// The owner's standing rule for these checks: none of them runs
			// against the owner's own installation.
			"never `belong`",
		} {
			if !strings.Contains(row, want) {
				t.Errorf("%s: the | %s | row lacks %q — the row is where the owner reads which command to run, and a row naming no command is the step list this conversion replaced", liveChecksDoc, c.row, want)
			}
		}
	}
}

// TestDocsCheck305RowNoLongerCallsTheReadingsHandRun: the row described the
// readings as hand-run — a client on the door's own issuer, one `gcloud iam roles
// describe` and the policy read back — and scripts/live-checks/check305.sh makes
// every one of them itself, so the sentence is a claim about a check that is no
// longer the one the owner runs.
func TestDocsCheck305RowNoLongerCallsTheReadingsHandRun(t *testing.T) {
	row := registerRow(t, readDoc(t, liveChecksDoc), check305Row)
	if strings.Contains(row, "hand-run") {
		t.Errorf("%s: the | 305 | row still describes the readings as hand-run; %s makes them itself (its own tokens, its own client, its own gcloud reads)", liveChecksDoc, check305Script)
	}
	for _, want := range []string{"is the whole of it", "signs itself in twice", check305RowRun} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: the | 305 | row lacks %q, which is what it says the script does", liveChecksDoc, want)
		}
	}
}

// TestDocsChecks303To305AreWiredIntoTheHarness: a script whose test is not run by
// scripts/live_checks_test.go is a script nothing checks.
func TestDocsChecks303To305AreWiredIntoTheHarness(t *testing.T) {
	harness := readRepoFile(t, "scripts/live_checks_test.go")
	for _, c := range []struct{ script, test string }{
		{check303Script, check303Test},
		{check304Script, check304Test},
		{check305Script, check305Test},
	} {
		name := c.script[strings.LastIndex(c.script, "/")+1:]
		entry := `{"` + name + `", "` + c.test[strings.LastIndex(c.test, "/")+1:] + `"}`
		if !strings.Contains(harness, entry) {
			t.Errorf("scripts/live_checks_test.go does not run %s (it has no %s entry)", c.test, entry)
		}
	}
}
