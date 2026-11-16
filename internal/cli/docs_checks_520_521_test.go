package cli

// Checks 520 and 521 — the vision-model work's owner-run checks — as the
// register writes them.
//
// The work (docs/vision-model/implementation-plan.md, its "Checks 520–521")
// writes both numbers: 520 is a repository whose coder is priced without an
// image rate and whose `agent.models.vision` is set, running a task that names a
// console `__screenshots__` PNG, with every coder-role stage timing and the
// record's cost split naming the vision model; 521 is the same repository with
// no slot at all, refused at bootstrap before any stage, with a sentence that
// names the slot to set and the price member that prices it. Both are task V8's,
// both ship as one script each, and neither has been run.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use: a section without its row is a check nothing points at, and a row
// without its section is a promise of steps that are not written. The backstop
// that would assert the pairing register-wide is deliberately absent
// (docs_compute_backend_test.go asserts its absence), so each work's own docs
// tests carry it for its own numbers. This file carries it for 520 and 521.
//
// The red step is this file run against a register that has neither half: the
// first failure names the row that is missing, the second the section.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	visionPlan = "docs/vision-model/implementation-plan.md"
	// The plan's own reserved block, whose two written numbers are these
	// (522–529 stay reserved and unused).
	visionCheck520 = "520"
	visionCheck521 = "521"
	// The invocation each row and section documents, argument for argument: 520
	// refuses to launch the run it measures without `--yes`, so a register that
	// names `check520.sh` alone documents a command that prints the plan and
	// stops. The two halves carry the same string, and folding the section's
	// whitespace is what lets one constant pin both.
	visionRun520 = "bash scripts/live-checks/check520.sh --sandbox <a sandbox repository checkout> --yes"
	visionRun521 = "bash scripts/live-checks/check521.sh --sandbox <a sandbox repository checkout>"
	// The release the work ships in, from docs/release-streams.yaml (current
	// 0.23.0, so 0.24.0 is next). Both scripts read it as their `RELEASE` knob's
	// default and stop with it named on a build that predates it.
	visionRelease = "0.24.0"
)

// visionChecks are the two numbers this work writes: the number, the command the
// owner types, and the sibling test that drives the script against fake tools.
var visionChecks = []struct{ number, run, script, scriptTest string }{
	{visionCheck520, visionRun520, "scripts/live-checks/check520.sh", "scripts/live-checks/check520_test.sh"},
	{visionCheck521, visionRun521, "scripts/live-checks/check521.sh", "scripts/live-checks/check521_test.sh"},
}

// TestDocsChecks520521AreRegistered: each of the two has both halves of its
// registration, and both say the check has not been run, that the owner runs it,
// that it is never pointed at `belong`, and that no agent runs any step of it.
// Each section also names the one command the owner types, the sibling test that
// drives it against fakes and the task that wrote it, because a check that ships
// as a script is registered by naming it: a section whose steps are a second,
// hand-kept list is how the register and the script come to disagree.
func TestDocsChecks520521AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range visionChecks {
		row := bitbucketWorkspaceCheckRowRE(c.number).FindString(register)
		if row == "" {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		} else {
			// Lowercased the way docs_checks_180_181_test.go reads its own rows:
			// the register writes the marker in capitals (`**OWNER-RUN, NOT
			// RUN.**`) and the pins below are the sentence, not its case.
			flatRow := strings.ToLower(flattenDoc(row))
			for _, want := range []string{
				// The register's own marker for an owner-run check that has not
				// been run: a row that lost either half reads as something an
				// agent did, on a sandbox.
				"**owner-run, not run.**",
				"no agent runs any step of it",
				// The installation this check must never touch.
				"never `belong`",
				// Where the owner's one command and its sibling test live: the
				// row is the half a reader scans, so the script has to be named
				// there and not only in the section below — and named with the
				// argument it refuses to run without.
				c.run,
				c.scriptTest,
				// The task that wrote it and the plan it comes from.
				"v8",
				"vision-model",
			} {
				if !strings.Contains(flatRow, want) {
					t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under and the script you type", c.number, want)
				}
			}
		}

		heading := "\n## Check " + c.number + ":"
		i := strings.Index(register, heading)
		if i < 0 {
			t.Errorf("%s has no `## Check %s:` section: the row above points at steps that have to be there", liveChecksDoc, c.number)
			continue
		}
		// Bounded by the next `## Check` heading, so a check written after this
		// one does not widen what is read here.
		section := register[i+1:]
		if end := strings.Index(section, "\n## Check "); end >= 0 {
			section = section[:end]
		}
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			"never `belong`",
			c.run,
			c.scriptTest,
			"v8",
			visionPlan,
			visionRelease,
			// The command to run it again is one line, not a list of steps: the
			// script is the whole check.
			"**run it:**",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", c.number, want)
			}
		}

		// The scripts and their sibling tests are really there: a row naming a
		// file nobody wrote is a promise.
		for _, p := range []string{c.script, c.scriptTest} {
			if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
				t.Errorf("Check %s's section names %s and there is no such file: %v", c.number, p, err)
			}
		}
		// The Go runner in scripts/live_checks_test.go drives every check's
		// sibling test; a check added without its entry is a test no job runs.
		runner := readDoc(t, "scripts/live_checks_test.go")
		if !strings.Contains(runner, `{"`+filepath.Base(c.script)+`", "`+filepath.Base(c.scriptTest)+`"}`) {
			t.Errorf("scripts/live_checks_test.go does not list {%q, %q}: every check's sibling test is driven from that table, and a check missing from it is one no job runs", filepath.Base(c.script), filepath.Base(c.scriptTest))
		}
	}

	// The two run in number order, so a reader who follows the table top to
	// bottom meets the sections in the same order.
	at := 0
	for _, c := range visionChecks {
		i := strings.Index(register[at:], "\n## Check "+c.number+":")
		if i < 0 {
			t.Errorf("%s writes Check %s's section out of order: the block reads in the order the numbers give", liveChecksDoc, c.number)
			continue
		}
		at += i
	}
}

// TestDocsCheck520StatesWhatOnlyTheRunSettles: the section has to carry the
// readings the check exists for, in the words the owner follows, because V8
// writes the script and no other document holds its steps. Each sentence is one
// no offline run can produce: which state V7 rewrites, that every coder-role
// stage timing names the vision model, that the record's cost split carries it,
// and that the diagnosis prints the same split.
func TestDocsCheck520StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := visionSection(t, visionCheck520)
	for _, want := range []string{
		// The state the work is about, and the plan's own sentence for it,
		// which names the other one: a coder whose price row carries an image
		// rate reads the picture itself (D2016) and the slot is never reached.
		"priced **without** an image rate",
		"D2016",
		"the plan's slip",
		// The feature gate both checks carry.
		"`fugaro version`",
		"`fugaro models use --help`",
		visionRelease,
		// The premise, read through the command that resolves it.
		"`fugaro doctor --json`",
		"`vision-coder`",
		// The two model IDs, and what the vision one is looked for in.
		"`fugaro models ls --json`",
		"`agent.models.vision`",
		// The run, its follow, and the record read from the bucket at the key
		// the listing's own row gives.
		"`fugaro run --json`",
		"`fugaro runs ls --json --limit 1`",
		"result.json",
		"one path segment",
		// The reading the issue is about: the stage timings and the split.
		"`implement`",
		"`cost.model_by`",
		// The command an owner reads a run with, and the same split in it.
		"`fugaro diagnose <run> --json`",
		// What the check does not do: it closes no pull request.
		"the `restore` row names them",
		// The target, and the guard that keeps it off the dev installation.
		"never `belong`",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 520's section does not say %q: the check is the only place these facts are ever measured, and V8 writes it", want)
		}
	}
}

// TestDocsCheck521StatesWhatOnlyTheRunSettles: 521 is the early refusal and the
// sentence an owner acts on. The section has to say where the refusal comes
// from, what the record has to show, and the four things the sentence names —
// a sentence missing one of them is the finding the check reports.
func TestDocsCheck521StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := visionSection(t, visionCheck521)
	for _, want := range []string{
		// The decisions the refusal and its sentence are written by.
		"V5",
		"D2005",
		"D2006",
		"D2001",
		// The state this check measures, which is Check 520's complement: no
		// slot at all.
		"`agent.models.vision` **unset**",
		"Check 520's state",
		// The task's own text is what triggers the refusal, so a task that
		// names no image measures nothing.
		"the two spellings the run's own check matches",
		// The run is followed, and it is cancelled rather than left behind:
		// nothing this check launches outlives it.
		"`fugaro runs ls --json`",
		"`fugaro cancel <run> --now`",
		"exit trap",
		// The record's own reading of the ending.
		"result.json",
		"`config_invalid`",
		// The four parts of the sentence an owner acts on.
		"the coder can't read an image the task names",
		"`model_prices`' `image_tokens`",
		"the file the task names",
		// Early, and free.
		"no stage at all",
		"**zero**",
		// No `--yes`, because the run costs nothing.
		"passes no `--yes`",
		// The target, and the guard that keeps it off the dev installation.
		"never `belong`",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 521's section does not say %q: the check is the only place these facts are ever measured, and V8 writes it", want)
		}
	}
}

// TestDocsLiveChecks520521NameEveryKnobTheyRead: a section's knob list is what
// the owner reads before running the check, and a knob the script reads while
// the section omits it is one they cannot look up — the omission is invisible,
// because the check runs without it. The script is the source of truth for the
// names: every `CHECK520_*`/`CHECK521_*` spelling it reads has to appear in its
// own section.
func TestDocsLiveChecks520521NameEveryKnobTheyRead(t *testing.T) {
	for _, c := range visionChecks {
		t.Run("check "+c.number, func(t *testing.T) {
			section := visionSection(t, c.number)
			knobs := liveCheckKnobs(readDoc(t, c.script), "CHECK"+c.number+"_")
			// The scan is a regular expression over a shell script, so a list
			// this short says the scan broke, not that the check lost its knobs.
			if len(knobs) < 10 {
				t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", c.script, len(knobs))
			}
			for _, knob := range knobs {
				if !strings.Contains(section, knob) {
					t.Errorf("Check %s's section does not name %s, which %s reads: the owner cannot look up a knob the register leaves out", c.number, knob, c.script)
				}
			}
		})
	}
}

// TestDocsLiveChecks520521HaveTheirOwnExitCodes: the block is what the owner
// pastes back, so the section has to say what the three endings mean and that a
// key the run never reached reads `FAIL — not reached` — a block whose rows lie
// about their own status is worse than no block.
func TestDocsLiveChecks520521HaveTheirOwnExitCodes(t *testing.T) {
	for _, c := range visionChecks {
		section := visionSection(t, c.number)
		for _, want := range []string{
			"CHECK " + c.number + " RESULT",
			"**Exit `0`**",
			"**`1`**",
			"**`2`**",
			"`FAIL — not reached`",
		} {
			if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
				t.Errorf("Check %s's section does not say %q: the owner reads the block against these", c.number, want)
			}
		}
	}
}

// visionSection is the register's text for one of this work's checks, flattened
// and folded, in the case it is written in: the pins above are the register's own
// tokens — a decision number, a command's flag, a marker — so the case is part of
// the claim.
func visionSection(t *testing.T, number string) string {
	t.Helper()
	register := readDoc(t, liveChecksDoc)
	i := strings.Index(register, "\n## Check "+number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the test above is the one that says it is missing", liveChecksDoc, number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## Check "); end >= 0 {
		section = section[:end]
	}
	return flattenDoc(section)
}
