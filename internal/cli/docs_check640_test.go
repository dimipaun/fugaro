package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Check 640 — the status labels on a real pull request — as the register writes
// it.
//
// The number is the forge-labels work's: its plan reserves the block 640–649
// and takes 640 for its own task CK, whose acceptance is this check
// (docs/forge-labels/implementation-plan.md, "### CK: Check 640"). T1 to T9 are
// merged, so the path the check reads is on main; what has not happened is the
// run: the check is the owner's, on the dogfood installation, and it is NOT RUN.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide is
// deliberately absent, so each work's own docs tests carry the assertion for its
// own numbers; this file carries it for 640.
//
// What this check exists to settle is two questions no offline test can answer —
// U1, whether the dogfood installation's GitHub App really holds
// `pull_requests: write` (nothing in this repository can read an App's
// permissions, and a run's own report is not the service's record of the act),
// and U3, what GitHub does with a label name that carries a colon — and the
// readings that depend on them: the running label while the run is in flight,
// the label the pull request is left with once the run ends, and the `spec:`
// label with D2437's colour. The section has to say all of that, because the
// script is the only place those readings are ever taken.
const check640Number = "640"

// check640Run is the one command the owner types, as both halves of the
// registration write it: the check refuses without an installation, and the
// script's own default is the dogfood one.
const check640Run = "bash scripts/live-checks/check640.sh --installation fugaro"

// check640Script and check640ScriptTest are the two files the row and the
// section name. A section telling the owner to run a file nobody wrote is a
// promise, and the sibling test is what drives the script against fake tools.
const (
	check640Script     = "scripts/live-checks/check640.sh"
	check640ScriptTest = "scripts/live-checks/check640_test.sh"
)

// check640RowRE reads the register's summary row for the check.
var check640RowRE = regexp.MustCompile(`(?m)^\|\s*` + check640Number + `\s*\|.*$`)

// check640Section returns the register's `## Check 640:` section, bounded by the
// next `## ` heading so that a check written after it does not widen what is
// read.
func check640Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+check640Number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, check640Number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsCheck640: the check has both halves of its registration, both say it
// has not been run and that no agent runs any step of it, both name the one
// command the owner types and the sibling test that drives it, and the two files
// are really there and really wired into the harness that runs every live
// check's test. The red step of this task is this test run against a register
// that has neither half.
func TestDocsCheck640(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check640RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check640Number, check640Number)
	}
	flatRow := strings.ToLower(flattenDoc(row))
	for _, want := range []string{
		// The register's own marker for an owner-run check that has not been
		// run: a row that lost either half reads as something an agent did, on
		// the dogfood installation.
		"**owner-run, passed 2026-11-22.**",
		"no agent runs any step of it",
		// The installation this check must never touch, and the one it reads.
		"never `belong`",
		"fugaro-dev",
		// Where the owner's one command and its dry driver live.
		check640Run,
		check640ScriptTest,
		// The task that wrote it and the plan it comes from.
		"ck",
		"forge-labels",
	} {
		if !strings.Contains(flatRow, want) {
			t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under and the one command you type", check640Number, want)
		}
	}

	section := check640Section(t, register)
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**pass, run by the owner on 2026-11-22**",
		"no agent runs any step of it",
		"never `belong`",
		check640Run,
		check640ScriptTest,
		"ck",
		// The plan's own path, as the register links it (the register is in
		// docs/, so its links are relative).
		"forge-labels/implementation-plan.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is owner-run and its record names the owner's run, and a section that leaves either half out reads as something an agent did", check640Number, want)
		}
	}

	// The two files are really there, and the Go runner in
	// scripts/live_checks_test.go drives the sibling test: a check added without
	// its entry is a test no job runs.
	for _, p := range []string{check640Script, check640ScriptTest} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("Check %s's section names %s and there is no such file: %v", check640Number, p, err)
		}
	}
	runner := readRepoFile(t, "scripts/live_checks_test.go")
	for _, want := range []string{
		`{"check640-queue-probe.sh", "check640-queue-probe_test.sh"}`,
	} {
		if !strings.Contains(runner, want) {
			t.Errorf("scripts/live_checks_test.go does not list %s: the script that queues Check 640's task is driven from that table too", want)
		}
	}
	if !strings.Contains(runner, `{"check640.sh", "check640_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not list {\"check640.sh\", \"check640_test.sh\"}: every check's sibling test is driven from that table, and a check missing from it is one no job runs")
	}
}

// TestDocsCheck640StatesWhatOnlyTheRunSettles: the section has to carry the
// readings the check exists for, in the words the owner follows, because no
// other document holds them: U1 and U3, the two questions nothing offline can
// answer, the daemon's identity to the credentials service (the F19 premise,
// which is what makes the check stop at one named row instead of failing every
// row below it), the labels of D2437's table and the two decisions that fix how
// they are written (D2439's token, D2440's ensure), and what a failure means.
func TestDocsCheck640StatesWhatOnlyTheRunSettles(t *testing.T) {
	flat := strings.ToLower(flattenDoc(check640Section(t, readDoc(t, liveChecksDoc))))
	for _, want := range []string{
		// U1: the App's permission, and the one conditional owner step the plan
		// writes for it.
		"u1",
		"`pull_requests: write`",
		"the app's settings page",
		// U3: what GitHub does with a name that carries a colon.
		"u3",
		"colon",
		// The labels, from D2437's table, with their colours.
		"`fugaro:running`",
		"`fugaro:failed`",
		"`spec:<name>`",
		"d2437",
		"1d76db",
		"d73a4a",
		"6e7781",
		// The token the credentials service mints for itself (D2439) and the
		// create-on-first-use rule (D2440), which are the two decisions the
		// route's behaviour turns on.
		"d2439",
		"d2440",
		// The prerequisite that is not this task's: with no identity the daemon
		// builds no seams at all, and the check says so in one row and stops.
		"f19",
		"the daemon cannot reach the label route: f19",
		// What the check reads, and that it is the service's own record rather
		// than the run's word.
		"gcloud logging read",
		"audit",
		// The register's own rule for an owner-run check that has not been run.
		"restore",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is the only place these facts are ever measured, and CK writes it", check640Number, want)
		}
	}
}

// TestDocsCheck640NamesEveryBlockKey: the section's "Reading the `CHECK 640
// RESULT` block." paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the script's
// own BLOCK_KEYS rather than a paraphrase of it.
func TestDocsCheck640NamesEveryBlockKey(t *testing.T) {
	keys := blockKeys(t, readRepoFile(t, check640Script))
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", check640Script, len(keys), keys)
	}
	section := check640Section(t, readDoc(t, liveChecksDoc))
	const heading = "**Reading the `CHECK 640 RESULT` block.**"
	i := strings.Index(section, heading)
	if i < 0 {
		t.Fatalf("%s: the Check %s section has no %q paragraph", liveChecksDoc, check640Number, heading)
	}
	para := section[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("%s's Check %s %q paragraph does not name the block key `%s`, which %s's BLOCK_KEYS prints", liveChecksDoc, check640Number, heading, k, check640Script)
		}
	}
}

// TestDocsCheck640NamesEveryKnobItReads: the section's knob list is what the
// owner reads before running the check, and a knob the script reads while the
// section omits it is one they cannot look up — the omission is invisible,
// because the check runs without it.
func TestDocsCheck640NamesEveryKnobItReads(t *testing.T) {
	section := check640Section(t, readDoc(t, liveChecksDoc))
	knobs := liveCheckKnobs(readRepoFile(t, check640Script), "CHECK640_")
	// The scan is a regular expression over a shell script, so a list this short
	// says the scan broke, not that the check lost its knobs.
	if len(knobs) < 15 {
		t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", check640Script, len(knobs))
	}
	for _, knob := range knobs {
		if !strings.Contains(section, knob) {
			t.Errorf("Check %s's section does not name %s, which %s reads: the owner cannot look up a knob the register leaves out", check640Number, knob, check640Script)
		}
	}
}

// TestDocsCheck640ScriptKeepsItsPromises: the section says the check writes
// nothing, reads no credential, refuses `belong` and refuses to run in a coding
// agent's session; these are its own sides of those sentences, read out of the
// script rather than trusted to the prose, so a script that started writing
// would fail here as well as on its own test.
func TestDocsCheck640ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check640Script)
	for _, want := range []string{
		"set -Eeuo pipefail",
		// The guard, by name, on every value it is given and every one it reads.
		"belong",
		// The dry run makes no call at all, which is what its own section says
		// of it.
		"--dry-run",
		// The refusal in a coding agent's session: a check whose rows are about
		// a real pull request is the owner's to run, and an agent's session
		// cannot answer for it.
		"CLAUDECODE",
		// No trace: a traced run prints the commands, and this check's commands
		// carry the installation's own names.
		"xtrace",
		// The scratch directory, removed from the exit trap and refusing a
		// directory that is not its own.
		"mktemp",
		"trap",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s no longer carries %q, which its own section's sentences rest on", check640Script, want)
		}
	}
	// A write the check MAKES would have to name its command, and the words
	// below are those commands. A row may still TELL the owner about one — the
	// colours row says how to delete a label the repository holds in another
	// shade, and the daemon row says to start the daemon — which is why the ban
	// is on the command rather than on the verb: what the check itself calls is
	// pinned by its own test, whose `expect_only_reads` reads the call log.
	for _, banned := range []string{
		"gh label create",
		"gh label edit",
		"gh pr edit",
		"gh pr close",
		"gcloud run deploy",
		"gcloud run services update",
		"gcloud projects add-iam-policy-binding",
		"gcloud secrets",
		"fugaro creds set",
		"fugaro project policy publish",
		"firebase database",
		// No credential is read or printed: the check has no token of its own
		// and no secret-shaped literal in it.
		"gh auth token",
		"gcloud auth print-access-token",
		"GITHUB_TOKEN",
		"ANTHROPIC_API_KEY",
		"-----BEGIN",
	} {
		if strings.Contains(script, banned) {
			t.Errorf("%s carries %q: the check is reads only, it reads no credential value and it changes nothing", check640Script, banned)
		}
	}
}

// TestDocsCheck640RecordsTheOwnersRun: the owner ran the check on 2026-11-22 and
// it passed, so the row says that and says it is theirs: a result is recorded
// only for a run the owner made, never one an agent claims.
func TestDocsCheck640RecordsTheOwnersRun(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	row := check640RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row", liveChecksDoc, check640Number)
	}
	if !strings.Contains(row, "OWNER-RUN, PASSED 2026-11-22") {
		t.Errorf("Check %s's row does not record the owner's run of 2026-11-22", check640Number)
	}
	// The plan's own conditional owner step is the one thing a failure adds,
	// and the section has to say it is the owner's: an agent cannot raise an
	// App's permission.
	flat := strings.ToLower(flattenDoc(check640Section(t, register)))
	if !strings.Contains(flat, "an agent cannot") {
		t.Errorf("Check %s's section does not say an agent cannot raise the App's permission: the conditional step the plan writes is the owner's alone", check640Number)
	}
}

// TestDocsCheck640NamesTheQueueStep: Check 640 launches nothing, so the register
// says which script queues the one approved task it needs, that the approval is
// the owner's typed confirmation, and that the script, its digest tool and its
// test are really there. check640.sh's own usage text points at it too.
func TestDocsCheck640NamesTheQueueStep(t *testing.T) {
	flat := strings.ToLower(flattenDoc(check640Section(t, readDoc(t, liveChecksDoc))))
	for _, want := range []string{
		"bash scripts/live-checks/check640-queue-probe.sh",
		"scripts/live-checks/check640-queue-probe_test.sh",
		"go run ./scripts/orchdigest",
		"you** type the project's name",
		"`--dry-run`",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the owner needs the task queued and approved, and this is the one command that does it", check640Number, want)
		}
	}
	for _, p := range []string{
		"scripts/live-checks/check640-queue-probe.sh",
		"scripts/live-checks/check640-queue-probe_test.sh",
		"scripts/orchdigest/main.go",
		"scripts/orchdigest/main_test.go",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("the register names %s and there is no such file: %v", p, err)
		}
	}
	if !strings.Contains(readRepoFile(t, check640Script), "check640-queue-probe.sh") {
		t.Errorf("%s's usage text does not name check640-queue-probe.sh, the script that queues its task", check640Script)
	}
}
