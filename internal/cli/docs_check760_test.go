package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Check 760 — how long `runs ls` takes on a real installation — as the register
// writes it.
//
// The number is the runs-ls-speed work's: its plan reserves the block 760–769
// and takes 760 for its own task RL6, whose acceptance is this check
// (docs/runs-ls-speed/implementation-plan.md, "RL6: Check 760, the owner's
// timing check"). RL1, RL3 and RL2B are merged, so the path the check times is
// on main; what has not happened is the run: the check is the owner's, on an
// installation of their own, and it is NOT RUN.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide is
// deliberately absent, so each work's own docs tests carry the assertion for its
// own numbers; this file carries it for 760.
//
// What this check exists to settle is the one thing no offline test can answer:
// whether the `runs ls` the work built is fast enough on a real runs bucket,
// measured against the owner's own readings of 2026-11-19. Its figures are the
// plan's, and the reading that matters most is the default listing's, because
// D2966 gates the optional follow-up — the task that would make that listing
// itself faster — on it: under 8 seconds means the follow-up is never built. The
// section has to say all of that, because the script is the only place those
// readings are ever taken.
const check760Number = "760"

// check760Run is the one command the owner types, as both halves of the
// registration write it: the check refuses without an installation, and the
// pull request it times is named because a pull request outside the 7-day window
// is the interesting case.
const check760Run = "bash scripts/live-checks/check-760-runs-ls-speed.sh --installation <installation> --pr <N>"

// check760Script and check760ScriptTest are the two files the row and the
// section name. A section telling the owner to run a file nobody wrote is a
// promise, and the sibling test is what drives the script against fake tools.
const (
	check760Script     = "scripts/live-checks/check-760-runs-ls-speed.sh"
	check760ScriptTest = "scripts/live-checks/check-760-runs-ls-speed_test.sh"
)

// check760RowRE reads the register's summary row for the check.
var check760RowRE = regexp.MustCompile(`(?m)^\|\s*` + check760Number + `\s*\|.*$`)

// check760Section returns the register's `## Check 760:` section, bounded by the
// next `## ` heading so that a check written after it does not widen what is
// read.
func check760Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+check760Number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, check760Number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsCheck760: the check has both halves of its registration, both say it
// has not been run and that no agent runs any step of it, both name the one
// command the owner types and the sibling test that drives it, and the two files
// are really there and really wired into the harness that runs every live
// check's test. The red step of this task is this test run against a register
// that has neither half.
func TestDocsCheck760(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check760RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check760Number, check760Number)
	}
	flatRow := strings.ToLower(flattenDoc(row))
	for _, want := range []string{
		// The register's own marker for an owner-run check that has not been
		// run: a row that lost either half reads as something an agent did, on
		// an installation it does not own.
		"**owner-run, not run.**",
		"no agent runs any step of it",
		// The installation this check must never touch, and the fact that the
		// one it reads is the owner's to name.
		"never `belong`",
		strings.ToLower(check760Run),
		// Where the owner's one command and its dry driver live.
		check760ScriptTest,
		// The task that wrote it and the plan it comes from.
		"rl6",
		"runs-ls-speed",
	} {
		if !strings.Contains(flatRow, want) {
			t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the command you type and the reading it settles", check760Number, want)
		}
	}

	section := check760Section(t, register)
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**not run.**",
		"no agent runs any step of it",
		"never `belong`",
		strings.ToLower(check760Run),
		check760ScriptTest,
		"rl6",
		// The plan's own path, as the register links it (the register is in
		// docs/, so its links are relative).
		"runs-ls-speed/implementation-plan.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", check760Number, want)
		}
	}

	// The two files are really there, and the Go runner in
	// scripts/live_checks_test.go drives the sibling test: a check added without
	// its entry is a test no job runs.
	for _, p := range []string{check760Script, check760ScriptTest} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("Check %s's section names %s and there is no such file: %v", check760Number, p, err)
		}
	}
	runner := readRepoFile(t, "scripts/live_checks_test.go")
	if !strings.Contains(runner, "{\"check-760-runs-ls-speed.sh\", \"check-760-runs-ls-speed_test.sh\"}") {
		t.Errorf("scripts/live_checks_test.go does not list {\"check-760-runs-ls-speed.sh\", \"check-760-runs-ls-speed_test.sh\"}: every check's sibling test is driven from that table, and a check missing from it is one no job runs")
	}
}

// TestDocsCheck760StatesWhatOnlyTheRunSettles: the section has to carry the
// readings the check exists for, in the words the owner follows, because no
// other document holds them: the four queries and the baselines of 2026-11-19
// they are read against, the plan's two 5 s targets, D2966's 8 s gate on the
// optional follow-up, the sanity reading of two pull requests by count and run
// ids only (and the two paths a `--pr` read can take, the index and the fallback
// scan), and what a slow figure means — which is nothing: a reading, not a
// failure.
func TestDocsCheck760StatesWhatOnlyTheRunSettles(t *testing.T) {
	flat := strings.ToLower(flattenDoc(check760Section(t, readDoc(t, liveChecksDoc))))
	for _, want := range []string{
		// The four queries the check times.
		"`runs ls --state running`",
		"`runs ls --pr n`",
		"`--no-cache`",
		"`runs ls --since 7d`",
		// The plan's own readings of 2026-11-19, each beside the query it is.
		"2026-11-19",
		"20 s",
		"23 s",
		"32 s",
		"15 s",
		"991 rows",
		// The two targets, and the third figure that is a decision rather than a
		// target.
		"under 5 s",
		"d2966",
		"under 8 seconds means never",
		"the optional task is not built",
		"rl4",
		// How the figure is read: three samples each, and their middle.
		"three times each",
		"median",
		// The reading that is not a criterion: two pull requests, by count and
		// run ids, one through the index and one through the fallback scan.
		"count and run ids only",
		"pull-request index",
		"fallback scan",
		"no pull-request index entry",
		// What a slow figure is, and what the exit codes mean.
		"a slow figure is a reading",
		"the readings were taken and are printed",
		"could **not** take",
		// The prerequisite the owner runs first, and the backfill that comes with
		// it.
		"fugaro update",
		"backfill",
		"rl2b",
		// What the run costs and what it never reads.
		"wall clock",
		"no credential value is read",
		// The register's own rule for an owner-run check: the scratch directory
		// goes back.
		"restore",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the check is the only place these readings are ever taken, and RL6 writes it", check760Number, want)
		}
	}
}

// TestDocsCheck760NamesEveryBlockKey: the section's "Reading the `CHECK 760
// RESULT` block." paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the script's
// own BLOCK_KEYS rather than a paraphrase of it.
func TestDocsCheck760NamesEveryBlockKey(t *testing.T) {
	keys := blockKeys(t, readRepoFile(t, check760Script))
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", check760Script, len(keys), keys)
	}
	section := check760Section(t, readDoc(t, liveChecksDoc))
	const heading = "**Reading the `CHECK 760 RESULT` block.**"
	i := strings.Index(section, heading)
	if i < 0 {
		t.Fatalf("%s: the Check %s section has no %q paragraph", liveChecksDoc, check760Number, heading)
	}
	para := section[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("%s's Check %s %q paragraph does not name the block key `%s`, which %s's BLOCK_KEYS prints", liveChecksDoc, check760Number, heading, k, check760Script)
		}
	}
}

// TestDocsCheck760NamesEveryKnobItReads: the section's knob list is what the
// owner reads before running the check, and a knob the script reads while the
// section omits it is one they cannot look up — the omission is invisible,
// because the check runs without it.
func TestDocsCheck760NamesEveryKnobItReads(t *testing.T) {
	section := check760Section(t, readDoc(t, liveChecksDoc))
	knobs := liveCheckKnobs(readRepoFile(t, check760Script), "CHECK760_")
	// The scan is a regular expression over a shell script, so a list this short
	// says the scan broke, not that the check lost its knobs.
	if len(knobs) < 15 {
		t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", check760Script, len(knobs))
	}
	for _, knob := range knobs {
		if !strings.Contains(section, knob) {
			t.Errorf("Check %s's section does not name %s, which %s reads: the owner cannot look up a knob the register leaves out", check760Number, knob, check760Script)
		}
	}
}

// TestDocsCheck760ScriptKeepsItsPromises: the section says the check times a
// real installation without writing to it, reads no credential, refuses `belong`
// and refuses to run in a coding agent's session; these are its own sides of
// those sentences, read out of the script rather than trusted to the prose, so a
// script that started writing would fail here as well as on its own test.
func TestDocsCheck760ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check760Script)
	for _, want := range []string{
		"set -Eeuo pipefail",
		// The guard, by name, on every value it is given and every one it reads.
		"belong",
		// The dry run makes no call at all, which is what its own section says
		// of it.
		"--dry-run",
		// The refusal in a coding agent's session: a check whose figures are
		// about a real installation is the owner's to run, and an agent's session
		// cannot answer for it.
		"CLAUDECODE",
		// No trace: a traced run prints the commands, and this check's commands
		// carry the installation's and the project's own names.
		"xtrace",
		// The scratch directory, removed from the exit trap and refusing a
		// directory that is not its own.
		"mktemp",
		"trap",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s no longer carries %q, which its own section's sentences rest on", check760Script, want)
		}
	}
	// A write the check MAKES would have to name its command, and the words
	// below are those commands. The check's own test pins what it really calls —
	// `expect_only_reads` reads the fake's call log and allows `doctor` and
	// `runs ls` and nothing else — which is why the ban is on the command rather
	// than on the verb.
	for _, banned := range []string{
		"gcloud ",
		"gh pr ",
		"gh label ",
		"fugaro run ",
		"fugaro launch",
		"fugaro cancel",
		"fugaro creds set",
		"fugaro project policy publish",
		"fugaro image refresh",
		"fugaro budget",
		"fugaro setup",
		"firebase database",
		"auth token",
		"print-access-token",
		// No credential is read or printed: the check has no token of its own
		// and no secret-shaped literal in it.
		"GITHUB_TOKEN",
		"ANTHROPIC_API_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"-----BEGIN",
		"curl",
	} {
		if strings.Contains(script, banned) {
			t.Errorf("%s carries %q: the check is reads only, it reads no credential value and it changes nothing", check760Script, banned)
		}
	}
	// The sibling test is what makes the promises above checkable, so it has to
	// be the one that drives the script with fakes and no network.
	harness := readRepoFile(t, check760ScriptTest)
	for _, want := range []string{"expect_only_reads", "env -i", "CHECK_TEST_SHARD"} {
		if !strings.Contains(harness, want) {
			t.Errorf("%s does not carry %q: the script's own test is where every step of it is driven against fake tools", check760ScriptTest, want)
		}
	}
}

// TestDocsCheck760IsNotClaimedAsRun: the register's own rule for a check nobody
// has run, applied to this one — the words that would read as a result are the
// ones that must not be here. The check is the owner's, and the whole of its
// registration says so.
func TestDocsCheck760IsNotClaimedAsRun(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	row := check760RowRE.FindString(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row", liveChecksDoc, check760Number)
	}
	for _, banned := range []string{"PASSED", "PASS on", "RAN on", "was run on"} {
		if strings.Contains(row, banned) {
			t.Errorf("Check %s's row says %q: nobody has run the check, and a row that reads as a result is a claim its owner never made", check760Number, banned)
		}
	}
	// The readings are the whole point of the check and nobody has them yet, so
	// the section must not read as if a figure were in hand.
	flat := strings.ToLower(flattenDoc(check760Section(t, register)))
	for _, banned := range []string{"passed", "we measured", "the readings are", "measured on"} {
		if strings.Contains(flat, banned) {
			t.Errorf("Check %s's section says %q: the check has not been run, and the section is the plan for the reading, not the reading", check760Number, banned)
		}
	}
}
