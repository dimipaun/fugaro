package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Check 740 — messaging a run that is going — as the register writes it.
//
// The number is the run-messages work's: its plan reserves 740 for its own task
// RM8, whose acceptance is this check (docs/run-messages/implementation-plan.md,
// "### RM8: Check 740, the owner's live check"). RM1 (the `tell` command and its
// access rule), RM4 (the `--now` stop and the resumed session) and RM6 (the
// bucket's message rule) are merged, so the path the check reads is on main;
// what has not happened is the run: the check is the owner's, on an installation
// the owner names, and it is NOT RUN.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide is
// deliberately absent, so each work's own docs tests carry the assertion for its
// own numbers; this file carries it for 740.
//
// What this check exists to settle is what nothing offline can: that the run's
// own record really says a plain message was `delivered` at a coder stage, that
// a `--now` stop found a quiet point and the SAME session went on from it (the
// reconciliation's U2 and U3, whose negatives are the two reasons the runner
// writes — `no quiet point in 10m` and `session lost at forced stop` — read
// against a record that went past the stage the message reached, because an
// empty reason is written before the turn it resumes runs), that the
// REAL job account of a workflow and of a project job is denied
// `storage.objects.create` on a message object (D2941), and that the applied
// bucket carries RM6's `.msg.json` lifecycle rule (U4). The section has to say
// all of that, because the script is the only place those readings are taken.
const check740Number = "740"

// check740Run is the one command the owner types, as both halves of the
// registration write it: the check refuses without an installation and a
// repository, and the repository takes the `owner/name` form `fugaro run --repo`
// takes from anywhere.
const check740Run = "bash scripts/live-checks/check-740-run-messages.sh --installation <name> --repo <owner/name>"

// check740Script and check740ScriptTest are the two files the row and the
// section name. A section telling the owner to run a file nobody wrote is a
// promise, and the sibling test is what drives the script against fake tools.
const (
	check740Script     = "scripts/live-checks/check-740-run-messages.sh"
	check740ScriptTest = "scripts/live-checks/check-740-run-messages_test.sh"
)

// check740RowRE reads the register's summary row for the check.
var check740RowRE = regexp.MustCompile(`(?m)^\|\s*` + check740Number + `\s*\|.*$`)

// check740Section returns the register's `## Check 740:` section, bounded by the
// next `## ` heading so that a check written after it does not widen what is
// read.
func check740Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+check740Number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, check740Number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsCheck740: the check has both halves of its registration, both say it
// has not been run and that no agent runs any step of it, both name the one
// command the owner types and the sibling test that drives it, and the two files
// are really there and really wired into the harness that runs every live
// check's test. The red step of this task is this test run against a register
// that has neither half.
func TestDocsCheck740(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check740RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check740Number, check740Number)
	}
	flatRow := strings.ToLower(flattenDoc(row))
	for _, want := range []string{
		// The register's own marker for an owner-run check that has not been
		// run: a row that lost either half reads as something an agent did.
		"**owner-run, not run.**",
		"no agent runs any step of it",
		// The installation this check must never touch, and where its one
		// command lives.
		"never `belong`",
		check740Run,
		check740ScriptTest,
		// The task that wrote it and the plan it comes from.
		"rm8",
		"run-messages",
	} {
		if !strings.Contains(flatRow, want) {
			t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under and the one command you type", check740Number, want)
		}
	}

	section := check740Section(t, register)
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**not run.**",
		"no agent runs any step of it",
		"never `belong`",
		check740Run,
		check740ScriptTest,
		"rm8",
		// The plan's own path, as the register links it (the register is in
		// docs/, so its links are relative).
		"run-messages/implementation-plan.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", check740Number, want)
		}
	}

	// The two files are really there, and the Go runner in
	// scripts/live_checks_test.go drives the sibling test: a check added without
	// its entry is a test no job runs.
	for _, p := range []string{check740Script, check740ScriptTest} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("Check %s's section names %s and there is no such file: %v", check740Number, p, err)
		}
	}
	runner := readRepoFile(t, "scripts/live_checks_test.go")
	if !strings.Contains(runner, `{"check-740-run-messages.sh", "check-740-run-messages_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not list {\"check-740-run-messages.sh\", \"check-740-run-messages_test.sh\"}: every check's sibling test is driven from that table, and a check missing from it is one no job runs")
	}
}

// TestDocsCheck740StatesWhatOnlyTheRunSettles: the section has to carry the four
// readings the check exists for, in the words the owner follows, because no
// other document holds them — the plain message's delivery, the `--now` stop's
// quiet point and the same session going on (U2, U3, with the two reasons that
// are their negatives), D2941's fence asked about the REAL job accounts, and
// RM6's lifecycle rule as the applied bucket carries it (U4).
func TestDocsCheck740StatesWhatOnlyTheRunSettles(t *testing.T) {
	flat := strings.ToLower(flattenDoc(check740Section(t, readDoc(t, liveChecksDoc))))
	for _, want := range []string{
		// Step 1: the plain message, delivered at a coder stage, read from the
		// run's own record.
		"`delivered`",
		"coder stage",
		"result.json",
		// Step 2: U2 and U3, and the two reasons that are their answer when the
		// answer is no.
		"u2",
		"u3",
		"`--now`",
		"quiet point",
		"no quiet point in 10m",
		"session lost at forced stop",
		"same",
		// What an empty reason needs beside it: the run's own ending, because
		// the entry is written BEFORE the turn it resumes runs, and the
		// session-lost reason is written only for a FORCED stop whose resume
		// failed. A record that ends in implement is not measured, never read
		// as U2 and U3 holding.
		"went **past** implement",
		"retagyield",
		"agent.errnosession",
		"not measured",
		// Step 3: D2941's fence, asked about the installation's own accounts by
		// the Policy Troubleshooter, and why a throwaway account would prove
		// nothing.
		"d2941",
		"iam policy troubleshooter",
		"storage.objects.create",
		"role cannot write **by definition**",
		"roles/storage.objectviewer",
		"not_granted",
		// The object the question is asked about, and the prefix a job account
		// may only read.
		".msg.json",
		// Step 4: U4, the applied bucket's rule and the token it must not
		// select.
		"u4",
		"lifecycle",
		"matches_suffix",
		"messages_days",
		"budget-token",
		"delete",
		// The register's own rule for an owner-run check: what the check puts
		// back, and the row that says whether it did.
		"restore",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is the only place these facts are ever measured, and RM8 writes it", check740Number, want)
		}
	}
}

// TestDocsCheck740NamesEveryBlockKey: the section's "Reading the `CHECK 740
// RESULT` block." paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the script's
// own BLOCK_KEYS rather than a paraphrase of it.
func TestDocsCheck740NamesEveryBlockKey(t *testing.T) {
	keys := blockKeys(t, readRepoFile(t, check740Script))
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", check740Script, len(keys), keys)
	}
	section := check740Section(t, readDoc(t, liveChecksDoc))
	const heading = "**Reading the `CHECK 740 RESULT` block.**"
	i := strings.Index(section, heading)
	if i < 0 {
		t.Fatalf("%s: the Check %s section has no %q paragraph", liveChecksDoc, check740Number, heading)
	}
	para := section[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("%s's Check %s %q paragraph does not name the block key `%s`, which %s's BLOCK_KEYS prints", liveChecksDoc, check740Number, heading, k, check740Script)
		}
	}
}

// TestDocsCheck740NamesEveryKnobItReads: the section's knob list is what the
// owner reads before running the check, and a knob the script reads while the
// section omits it is one they cannot look up — the omission is invisible,
// because the check runs without it.
func TestDocsCheck740NamesEveryKnobItReads(t *testing.T) {
	section := check740Section(t, readDoc(t, liveChecksDoc))
	knobs := liveCheckKnobs(readRepoFile(t, check740Script), "CHECK740_")
	// The scan is a regular expression over a shell script, so a list this short
	// says the scan broke, not that the check lost its knobs.
	if len(knobs) < 10 {
		t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", check740Script, len(knobs))
	}
	for _, knob := range knobs {
		if !strings.Contains(section, knob) {
			t.Errorf("Check %s's section does not name %s, which %s reads: the owner cannot look up a knob the register leaves out", check740Number, knob, check740Script)
		}
	}
}

// TestDocsCheck740ScriptKeepsItsPromises: the section says the check writes
// nothing, reads no credential, refuses `belong` and refuses to run in a coding
// agent's session; these are its own sides of those sentences, read out of the
// script rather than trusted to the prose, so a script that started writing
// would fail here as well as on its own test.
func TestDocsCheck740ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check740Script)
	for _, want := range []string{
		"set -Eeuo pipefail",
		// The guard, by name, on every value it is given and every one it reads.
		"belong",
		// The dry run makes no call at all, which is what its own section says
		// of it.
		"--dry-run",
		// The refusal in a coding agent's session: a check whose readings are
		// about a real run on a real installation is the owner's to take, and an
		// agent's session cannot take them.
		"CLAUDECODE",
		// No trace: a traced run prints the commands, and this check's commands
		// carry the installation's own names.
		"xtrace",
		// The scratch directory, removed from the exit trap.
		"mktemp",
		"trap",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s no longer carries %q, which its own section's sentences rest on", check740Script, want)
		}
	}
	// A write the check MAKES would have to name its command, and the words
	// below are those commands. A row may still TELL the owner about one — the
	// lifecycle row says to read the bucket, and a row that could not measure
	// says which command the owner may run themselves — which is why the ban is
	// on the command rather than on the verb: what the check itself calls is
	// pinned by its own test, whose call log is read for exactly this.
	for _, banned := range []string{
		"gh pr",
		"gcloud run deploy",
		"gcloud run jobs delete",
		"gcloud storage rm",
		"gcloud secrets",
		"gcloud projects add-iam-policy-binding",
		"firebase database",
		"fugaro cancel",
		"fugaro creds",
		"fugaro project policy publish",
		// No credential is read or printed: the check has no token of its own
		// and no secret-shaped literal in it.
		"gh auth token",
		"gcloud auth print-access-token",
		"GITHUB_TOKEN",
		"ANTHROPIC_API_KEY",
		"-----BEGIN",
	} {
		if strings.Contains(script, banned) {
			t.Errorf("%s carries %q: the check is reads only, it reads no credential value and it changes nothing", check740Script, banned)
		}
	}
}

// TestDocsCheck740IsNotClaimedAsRun: the register's own rule for a check nobody
// has run, applied to this one — the words that would read as a result are the
// ones that must not be here. The check is the owner's, after the release and
// `fugaro update`, and the whole of its registration says so.
func TestDocsCheck740IsNotClaimedAsRun(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	row := check740RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row", liveChecksDoc, check740Number)
	}
	for _, banned := range []string{"PASSED", "PASS on", "RAN on", "was run on"} {
		if strings.Contains(row, banned) {
			t.Errorf("Check %s's row says %q: nobody has run the check, and a row that reads as a result is a claim its owner never made", check740Number, banned)
		}
	}
	flat := strings.ToLower(flattenDoc(check740Section(t, register)))
	// The owner runs it after the release and after the upgrade that applies
	// RM6's rule and rebuilds the job images RM2 to RM4 live in: a section that
	// left that out would send the owner at a build with no message channel.
	if !strings.Contains(flat, "fugaro update") {
		t.Errorf("Check %s's section does not say it is run after `fugaro update`: the bucket rule and the job images a message needs arrive with that upgrade", check740Number)
	}
}
