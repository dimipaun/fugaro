package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Checks 628 to 632 — the model-crafts work's five owner-run live checks — as
// the register writes them.
//
// The numbers are task MC26's: its plan reserves the block 628–637 and takes
// 628 to 632 for its five checks (docs/model-crafts/activation-plan.md, "### MC26
// — the owner-run live checks and the rollout"), one per half of the activation
// and one for their convergence. MC17, MC19, MC21, MC23 and MC24 are merged, so
// the behavior each check measures is on main; what has not happened is the run:
// every one of them is the owner's, on an installation of their own, and none
// has been run.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide is
// deliberately absent, so each work's own docs tests carry the assertion for its
// own numbers; this file carries it for these five.
//
// What the five exist to settle is what nobody can read offline: that a craft's
// model is what a run runs (628), that the published node is what a run is
// served from (629), that one pool per half is refused and converged by `fugaro
// update` (630), that a key credential pasted at the owner's own terminal is
// stored, hidden and served (631), and that the four halves converge in one
// command while a run in flight finishes undisturbed (632). The sections have to
// say each of those, because the scripts are the only place they are ever
// measured.
type modelCraftsCheck struct {
	// number is the register's own number, "628" to "632".
	number string
	// what is the check's subject in one line, for a failure's own words.
	what string
	// settles is the reading the check exists for, as the section has to carry
	// it: tokens the flattened section must hold, in lower case.
	settles []string
}

var modelCraftsChecks = []modelCraftsCheck{
	{
		number: "628",
		what:   "the model half: a craft's model is what a run runs",
		settles: []string{
			"agent.crafts.coder.model",
			"agent.models.coder",
			"allow-list",
			"craft ls",
			"not pinned",
			"stage_accounts",
		},
	},
	{
		number: "629",
		what:   "the publish: the node a run is served from",
		settles: []string{
			"craft_credentials",
			"craft_pools",
			"second",
			"exhausted",
			"project policy publish",
		},
	},
	{
		number: "630",
		what:   "the crossing: one pool per half, and the update that gets you there",
		settles: []string{
			"name two pools, one per half",
			"validatepublishedpools",
			"migratev8tov9",
			"fugaro update",
			"fugaro validate",
		},
	},
	{
		number: "631",
		what:   "the key credential: accounts add at your own terminal, and what serves the run it declares",
		settles: []string{
			"credential-<name>-key",
			"hidden prompt",
			"shell history",
			"serving path",
			"accounts pool rm",
			"credsvc: audit",
		},
	},
	{
		number: "632",
		what:   "convergence and the run in flight",
		settles: []string{
			"in flight",
			"fugaro update",
			"ledger",
			"counters",
			"update waits for none",
		},
	},
}

// modelCraftsRowRE reads a check's summary row out of the register's table.
func modelCraftsRowRE(number string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\|\s*` + number + `\s*\|.*$`)
}

// modelCraftsSection returns the register's `## Check N:` section, bounded by
// the next `## ` heading so that a check written after it does not widen what is
// read.
func modelCraftsSection(t *testing.T, register, number string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsModelCraftsChecks: each of the five has both halves of its
// registration, both say it has not been run, that no agent runs any step of it
// and that the installation it must never touch is `belong`, both name the one
// command the owner types and the sibling test that drives it, and the two files
// are really there and really wired into the harness that runs every live
// check's test. The red step of MC26 is this test run against a register that
// has neither half for these numbers.
func TestDocsModelCraftsChecks(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	runner := readRepoFile(t, "scripts/live_checks_test.go")
	for _, c := range modelCraftsChecks {
		script := "scripts/live-checks/check" + c.number + ".sh"
		scriptTest := "scripts/live-checks/check" + c.number + "_test.sh"
		row := modelCraftsRowRE(c.number).FindString(register)
		if row == "" {
			t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		}
		flatRow := strings.ToLower(flattenDoc(row))
		for _, want := range []string{
			// The register's own marker for an owner-run check that has not been
			// run: a row that lost either half reads as something an agent did.
			"**owner-run, not run.**",
			"no agent runs any step of it",
			// The installation this check must never touch.
			"never `belong`",
			// The task that wrote it and the plan it comes from.
			"mc26",
			"model-crafts/activation-plan.md",
			// Where the owner's one command and its dry driver live.
			script,
			scriptTest,
			"bash " + script + " --sandbox",
		} {
			if !strings.Contains(flatRow, want) {
				t.Errorf("Check %s's row (%s) does not say %q: an owner-run check is registered with the label it was run under and the one command you type", c.number, c.what, want)
			}
		}

		flat := strings.ToLower(flattenDoc(modelCraftsSection(t, register, c.number)))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			"never `belong`",
			"mc26",
			"model-crafts/activation-plan.md",
			script,
			scriptTest,
			"bash " + script + " --sandbox",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section (%s) does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", c.number, c.what, want)
			}
		}

		// The two files are really there, and the Go runner in
		// scripts/live_checks_test.go drives the sibling test: a check added
		// without its entry is a test no job runs.
		for _, p := range []string{script, scriptTest} {
			if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
				t.Errorf("Check %s's section names %s and there is no such file: %v", c.number, p, err)
			}
		}
		if want := `{"check` + c.number + `.sh", "check` + c.number + `_test.sh"}`; !strings.Contains(runner, want) {
			t.Errorf("scripts/live_checks_test.go does not list %s: every check's sibling test is driven from that table, and a check missing from it is one no job runs", want)
		}
	}
}

// TestDocsModelCraftsChecksStatesWhatOnlyTheRunSettles: each section has to
// carry the reading its check exists for, in the words the owner follows,
// because no other document holds it.
func TestDocsModelCraftsChecksStatesWhatOnlyTheRunSettles(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range modelCraftsChecks {
		flat := strings.ToLower(flattenDoc(modelCraftsSection(t, register, c.number)))
		for _, want := range c.settles {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section (%s) does not say %q: the check is the only place that reading is ever taken, and MC26 writes it", c.number, c.what, want)
			}
		}
	}
}

// TestDocsModelCraftsChecksReadThePolicyThroughItsOwnMember: `fugaro project
// policy show --json` prints {"project": ..., "policy": ...} — the document is
// an object whose only members are those two — and each of these checks reads
// the node's blocks (`craft_credentials`, `craft_pools`, `crafts`, and 628's
// allow-list under the policy's budget) out of it. Taken off the top level each
// one is null, so the row is built from nothing: 629's own `node-blocks` row
// then reports a correct installation as one the publish did not write. The
// cascade checks' test (docs_credentials_cascade_checks_test.go, checks 94/95
// and 99) pins the same defect in the sections that quote their reader.
func TestDocsModelCraftsChecksReadThePolicyThroughItsOwnMember(t *testing.T) {
	for _, c := range modelCraftsChecks {
		script := "scripts/live-checks/check" + c.number + ".sh"
		if !strings.Contains(readRepoFile(t, script), `doc.get("policy")`) {
			t.Errorf("%s does not read the policy node through the document's `policy` member, which is where `fugaro project policy show --json` puts it: read at the top level every block of the node is null, and a row built from those nulls calls a correct installation broken", script)
		}
	}
}

// TestDocsModelCraftsChecksNameEveryBlockKey: each section's "Reading the
// `CHECK NNN RESULT` block." paragraph is what the owner diffs a pasted block
// against, so it names every key the block prints, and what the block prints is
// the script's own BLOCK_KEYS rather than a paraphrase of it.
func TestDocsModelCraftsChecksNameEveryBlockKey(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range modelCraftsChecks {
		script := "scripts/live-checks/check" + c.number + ".sh"
		keys := blockKeys(t, readRepoFile(t, script))
		if len(keys) < 10 {
			t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", script, len(keys), keys)
		}
		section := modelCraftsSection(t, register, c.number)
		heading := "**Reading the `CHECK " + c.number + " RESULT` block.**"
		i := strings.Index(section, heading)
		if i < 0 {
			t.Fatalf("%s: the Check %s section has no %q paragraph", liveChecksDoc, c.number, heading)
		}
		para := section[i:]
		if j := strings.Index(para, "\n\n"); j >= 0 {
			para = para[:j]
		}
		for _, k := range keys {
			if !strings.Contains(para, "`"+k+"`") {
				t.Errorf("%s's Check %s %q paragraph does not name the block key `%s`, which %s's BLOCK_KEYS prints", liveChecksDoc, c.number, heading, k, script)
			}
		}
	}
}

// TestDocsModelCraftsChecksNameEveryKnobTheyRead: each section's knob list is
// what the owner reads before running the check, and a knob the script reads
// while the section omits it is one they cannot look up — the omission is
// invisible, because the check runs without it.
func TestDocsModelCraftsChecksNameEveryKnobTheyRead(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range modelCraftsChecks {
		script := "scripts/live-checks/check" + c.number + ".sh"
		flat := flattenDoc(modelCraftsSection(t, register, c.number))
		knobs := liveCheckKnobs(readRepoFile(t, script), "CHECK"+c.number+"_")
		// The scan is a regular expression over a shell script, so a list this
		// short says the scan broke, not that the check lost its knobs.
		if len(knobs) < 5 {
			t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", script, len(knobs))
		}
		for _, k := range knobs {
			if !strings.Contains(flat, "`"+k+"`") {
				t.Errorf("%s's Check %s section does not name the knob `%s`, which the script reads: the owner reads the section before running it", liveChecksDoc, c.number, k)
			}
		}
	}
}
