package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Check 750 — a review judged against its task — as the register writes it.
//
// The number is the review-against-task work's: the plan reserves 750 for its own
// check (docs/review-against-task/implementation-plan.md, its "## Checks"), run
// by the owner after the release that carries RA10. The work itself is merged —
// every review stage resolves the task a pull request was written for through the
// runs bucket alone (launchSpecForPR), and `fugaro review show` prints what it
// judged against on one line (R8) — so the path the check reads is on main; what
// has not happened is the run: the check is the owner's, on an installation the
// owner names, and it is NOT RUN.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide is
// deliberately absent, so each work's own docs tests carry the assertion for its
// own numbers; this file carries it for 750.
//
// What this check exists to settle is what nothing offline can: that on a real
// installation the launcher resolves the run that opened a real pull request and
// the artifact's own against line names it (with D2933's older job image — a
// review launched before `fugaro update` rebuilt the images — the thing this row
// catches), that a REAL reviewer handed a change which leaves out an item of its
// brief reports it as an `unmet` issue under the verdict `fix first`, and that
// `--code-only` says so in that same line. The section has to say all of that,
// because the script is the only place those readings are taken.
const check750Number = "750"

// check750Run is the one command the owner types, as both halves of the
// registration write it (lower-cased, because the row and the section are read
// folded): the check refuses without an installation and a pull request, and the
// pull request must be one a Fugaro run opened.
const check750Run = "bash scripts/live-checks/check-750-review-against-task.sh --installation <name> --pr <n>"

// check750Script and check750ScriptTest are the two files the row and the
// section name. A section telling the owner to run a file nobody wrote is a
// promise, and the sibling test is what drives the script against a fake fugaro.
const (
	check750Script     = "scripts/live-checks/check-750-review-against-task.sh"
	check750ScriptTest = "scripts/live-checks/check-750-review-against-task_test.sh"
)

// check750RowRE reads the register's summary row for the check.
var check750RowRE = regexp.MustCompile(`(?m)^\|\s*` + check750Number + `\s*\|.*$`)

// check750Section returns the register's `## Check 750:` section, bounded by the
// next `## ` heading so that a check written after it does not widen what is
// read.
func check750Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+check750Number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, check750Number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsCheck750: the check has both halves of its registration, both say it
// has not been run and that no agent runs any step of it, both name the one
// command the owner types and the sibling test that drives it, and the two files
// are really there and really wired into the harness that runs every live
// check's test. The red step of this task is this test run against a register
// that has neither half.
func TestDocsCheck750(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check750RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check750Number, check750Number)
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
		check750Run,
		check750ScriptTest,
		// The task that wrote it and the plan it comes from.
		"ra10",
		"review-against-task",
	} {
		if !strings.Contains(flatRow, want) {
			t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under and the one command you type", check750Number, want)
		}
	}

	section := check750Section(t, register)
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**not run.**",
		"no agent runs any step of it",
		"never `belong`",
		check750Run,
		check750ScriptTest,
		// The plan's own path, as the register links it (the register is in
		// docs/, so its links are relative).
		"review-against-task/implementation-plan.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", check750Number, want)
		}
	}

	// The two files are really there, and the Go runner in
	// scripts/live_checks_test.go drives the sibling test: a check added without
	// its entry is a test no job runs.
	for _, p := range []string{check750Script, check750ScriptTest} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("Check %s's section names %s and there is no such file: %v", check750Number, p, err)
		}
	}
	runner := readRepoFile(t, "scripts/live_checks_test.go")
	if !strings.Contains(runner, `{"check-750-review-against-task.sh", "check-750-review-against-task_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not list {\"check-750-review-against-task.sh\", \"check-750-review-against-task_test.sh\"}: every check's sibling test is driven from that table, and a check missing from it is one no job runs")
	}
}

// TestDocsCheck750StatesWhatOnlyTheRunSettles: the section has to carry the
// three readings the check exists for, in the words the owner follows, because
// no other document holds them — the against line naming the run the rows say
// opened the pull request (and D2933's older job image as what a `none` there
// means), a real reviewer reporting a change that misses an item of its brief as
// `unmet` under `fix first`, and `--code-only` saying so.
func TestDocsCheck750StatesWhatOnlyTheRunSettles(t *testing.T) {
	flat := strings.ToLower(flattenDoc(check750Section(t, readDoc(t, liveChecksDoc))))
	for _, want := range []string{
		// Step 1: the against line, and where the opening run is read from.
		"against: run <bare-run-id>",
		"`against: run <that run>`",
		"fugaro runs ls --pr n --json",
		// The read names its repository: `runs ls --pr` with no `--repo` is
		// refused unless the local config lists exactly one repository, so the
		// section has to say where the one it passes comes from.
		"`--repo .`",
		"launchspecforpr",
		// What a `none` on a pull request a Fugaro run opened means: a launcher
		// that lost the task, or the older job image of D2933 that runs
		// code-only whatever the launcher wrote.
		"d2933",
		"no run found",
		"fugaro update",
		// Step 2: the second pull request, its two artifact words, and the
		// INCOMPLETE the check ends on without it.
		"`--unmet-pr <m>`",
		"`unmet`",
		"`fix first`",
		"unverified",
		"exit 3",
		// Step 3: the flag, and the exact line it must print.
		"`--code-only`",
		"against: none (--code-only)",
		// The register's own rule for an owner-run check: what the check puts
		// back, and the row that says whether it did.
		"restore",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is the only place these facts are ever measured, and the plan's own Checks section writes it", check750Number, want)
		}
	}
}

// TestDocsCheck750NamesEveryBlockKey: the section's "Reading the `CHECK 750
// RESULT` block." paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the script's
// own BLOCK_KEYS rather than a paraphrase of it.
func TestDocsCheck750NamesEveryBlockKey(t *testing.T) {
	keys := blockKeys(t, readRepoFile(t, check750Script))
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", check750Script, len(keys), keys)
	}
	section := check750Section(t, readDoc(t, liveChecksDoc))
	const heading = "**Reading the `CHECK 750 RESULT` block.**"
	i := strings.Index(section, heading)
	if i < 0 {
		t.Fatalf("%s: the Check %s section has no %q paragraph", liveChecksDoc, check750Number, heading)
	}
	para := section[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("%s's Check %s %q paragraph does not name the block key `%s`, which %s's BLOCK_KEYS prints", liveChecksDoc, check750Number, heading, k, check750Script)
		}
	}
}

// TestDocsCheck750NamesEveryKnobItReads: the section's knob list is what the
// owner reads before running the check, and a knob the script reads while the
// section omits it is one they cannot look up — the omission is invisible,
// because the check runs without it.
func TestDocsCheck750NamesEveryKnobItReads(t *testing.T) {
	section := check750Section(t, readDoc(t, liveChecksDoc))
	knobs := liveCheckKnobs(readRepoFile(t, check750Script), "CHECK750_")
	// The scan is a regular expression over a shell script, so a list this short
	// says the scan broke, not that the check lost its knobs.
	if len(knobs) < 8 {
		t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", check750Script, len(knobs))
	}
	for _, knob := range knobs {
		if !strings.Contains(section, knob) {
			t.Errorf("Check %s's section does not name %s, which %s reads: the owner cannot look up a knob the register leaves out", check750Number, knob, check750Script)
		}
	}
}

// TestDocsCheck750ScriptKeepsItsPromises: the section says the check writes
// nothing, reads no credential, refuses `belong` and refuses to run in a coding
// agent's session; these are its own sides of those sentences, read out of the
// script rather than trusted to the prose, so a script that started writing
// would fail here as well as on its own test.
func TestDocsCheck750ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check750Script)
	for _, want := range []string{
		"set -Eeuo pipefail",
		// The guard, by name, on every value it is given and every one it reads.
		"belong",
		// The dry run makes no call at all, which is what its own section says
		// of it.
		"--dry-run",
		// The refusal in a coding agent's session: a check whose readings are
		// about real review jobs on a real installation is the owner's to take,
		// and an agent's session cannot take them.
		"CLAUDECODE",
		// No trace: a traced run prints the commands, and this check's commands
		// carry the installation's own names.
		"xtrace",
		// The scratch directory, removed from the exit trap.
		"mktemp",
		"trap",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s no longer carries %q, which its own section's sentences rest on", check750Script, want)
		}
	}
	// A write the check MAKES would have to name its command, and the words
	// below are those commands. A row may still TELL the owner about one — a row
	// that could not measure says which command the owner may run themselves —
	// which is why the ban is on the command rather than on the verb: what the
	// check itself calls is pinned by its own test, whose call log is read for
	// exactly this.
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
		"fugaro project repos",
		"fugaro secrets",
		"fugaro tell",
		// No credential is read or printed: the check has no token of its own
		// and no secret-shaped literal in it.
		"gh auth token",
		"gcloud auth print-access-token",
		"GITHUB_TOKEN",
		"ANTHROPIC_API_KEY",
		"-----BEGIN",
	} {
		if strings.Contains(script, banned) {
			t.Errorf("%s carries %q: the check is reads only, it reads no credential value and it changes nothing", check750Script, banned)
		}
	}
}

// TestDocsCheck750IsNotClaimedAsRun: the register's own rule for a check nobody
// has run, applied to this one — the words that would read as a result are the
// ones that must not be here. The check is the owner's, after the release and
// `fugaro update`, and the whole of its registration says so.
func TestDocsCheck750IsNotClaimedAsRun(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	row := check750RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row", liveChecksDoc, check750Number)
	}
	for _, banned := range []string{"PASSED", "PASS on", "RAN on", "was run on"} {
		if strings.Contains(row, banned) {
			t.Errorf("Check %s's row says %q: nobody has run the check, and a row that reads as a result is a claim its owner never made", check750Number, banned)
		}
	}
	flat := strings.ToLower(flattenDoc(check750Section(t, register)))
	// The owner runs it after the release and after the upgrade that rebuilds
	// the job images: a review launched on an image built before it runs
	// code-only whatever the launcher wrote (D2933), so a section that left
	// `fugaro update` out would send the owner at a build whose readings are
	// already known.
	if !strings.Contains(flat, "fugaro update") {
		t.Errorf("Check %s's section does not say it is run after `fugaro update`: the job images the against line needs arrive with that upgrade, and an older image runs code-only (D2933)", check750Number)
	}
}
