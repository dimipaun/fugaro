package cli

import (
	"os"
	"strings"
	"testing"
)

// Check 601 — the review class reads one repository, signs one verdict, and
// reaches nothing else — is docs/c39-review-identity's owner check: its plan
// task C3.9g2, whose check is §8 of that work's spec, written into the register
// as one section and one row. It ships as one script,
// scripts/live-checks/check-c39.sh, with its own hermetic test beside it, and
// the script is the only form of it an agent runs: the eleven steps are the
// owner's, and the script measures the ones a machine can read and prints a
// prompt for each of the rest.
//
// The register's rule is that each work's own docs tests carry the assertion
// for its own numbers — the register-wide pairing backstop
// (TestChecklistChecksAreGuarded) is deliberately absent, as docs_check600_test.go
// says — so this file is what holds Check 601's two halves together: the row and
// the section, the section's own prose, and the claims that section makes about
// the script, read out of the script and its test rather than trusted to the
// prose.
const (
	check601Number  = "601"
	check601Script  = "scripts/live-checks/check-c39.sh"
	check601Test    = "scripts/live-checks/check-c39_test.sh"
	check601Heading = "## Check 601:"
)

// TestDocsCheck601IsRegistered: the check has both halves of its registration,
// both say it has not been run, and both name the script and the test that are
// the check's runnable form. A row or a section that read as a pass would be a
// live claim nobody made — every step of this check is the owner's, and none
// has been run — and a section telling the owner to run a file nobody wrote is
// a promise.
func TestDocsCheck601IsRegistered(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	row := registerRow(t, doc, check601Number)
	for _, want := range []string{
		"**OWNER-RUN, NOT RUN.**",
		check601Script,
		check601Test,
		"never `belong`",
		// The one command the owner types, with both arguments the check
		// refuses without.
		"bash scripts/live-checks/check-c39.sh --sandbox <a sandbox repository checkout> --run <the review run id>",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: the | %s | row lacks %q: the row is the check's registration and where a reader finds the one command it takes", liveChecksDoc, check601Number, want)
		}
	}
	if !strings.Contains(row, "no agent runs any step of it") {
		t.Errorf("%s: the | %s | row does not say no agent runs any step of it: the check mints ID tokens and reads a live installation, and the row is where that is stated", liveChecksDoc, check601Number)
	}

	for _, path := range []string{check601Script, check601Test} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is named by Check %s and does not exist: %v", path, check601Number, err)
		}
	}
	// The script's own test is wired into the harness that runs every live
	// check's test, which is the only thing that executes it on this tree.
	if got := readRepoFile(t, "scripts/live_checks_test.go"); !strings.Contains(got, `{"check-c39.sh", "check-c39_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not run check-c39.sh's own test: a script whose test is not wired in is a script nothing runs")
	}
}

// TestDocsCheck601SectionSaysWhatItSettles: the section is where the owner
// reads what the check settles, how much of it is theirs and what it costs, so
// the facts a reader acts on are pinned as a whole. The prose wraps, so the
// search is over the flattened section: a pin that missed a phrase across a
// line break would be a pin on the line breaks.
func TestDocsCheck601SectionSaysWhatItSettles(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	section := registerSection(t, doc, check601Heading)
	flat := flattenDoc(section)
	for _, want := range []string{
		"**NOT RUN.**",
		// Who wrote it and what the check is: the plan's task and the spec's
		// section, which are what a reader goes to for the eleven steps.
		"C3.9g2",
		"[c39-review-identity/spec.md](c39-review-identity/spec.md)",
		"[c39-review-identity/live-check.md](c39-review-identity/live-check.md)",
		"**§8**",
		// The division of labour: this section carries no steps of its own, the
		// script measures four of the eleven and prints a prompt for the rest.
		"this section carries no steps of its own",
		"measures steps **1, 2, 6 and 11**",
		"prints a prompt for each of **3, 4, 5, 7, 8, 9 and 10**",
		// The one thing that makes step 8 unrunnable as it stands.
		"Open item 5 is open",
		// The premise the whole check rests on, and the sentence the script has
		// to keep: the installation is the checkout's own, and there is no way
		// to point the check at another one.
		"The project and the region are read from `fugaro doctor --json` in that checkout rather than typed",
		"cannot be pointed at another one",
		// The refusal the standing rule requires, by name.
		"Never `belong`, never `edge-devel-dimi`, never `edgeappinc`",
		// What the owner runs, and that a dry run calls nothing at all.
		"bash scripts/live-checks/check-c39.sh --sandbox <a sandbox repository checkout> --dry-run",
		"prints the guards and the plan and calls nothing at all",
		// The block they paste back, the exit codes they read and the bounds on
		// the one thing this check spends.
		"CHECK 601 RESULT",
		"**Exit `0`**",
		"A key the run never reached reads `FAIL — not reached`",
		"CHECK601_KEEP_SCRATCH",
		// What the class is by construction: the review job carries no
		// credential of its own, which is the row this check exists for.
		"mounts no secret",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s: the Check %s section does not say %q: the section is where the owner reads what the check settles and what is theirs to run", liveChecksDoc, check601Number, want)
		}
	}
	// The knobs the section lists are the script's own, and a knob the script
	// reads but the section does not name would be a setting nobody could find.
	knobs := liveCheckKnobs(readRepoFile(t, check601Script), "CHECK"+check601Number+"_")
	if len(knobs) < 10 {
		t.Fatalf("%s reads %d knobs, too few for the real list: the scan is broken", check601Script, len(knobs))
	}
	for _, knob := range knobs {
		if !strings.Contains(flat, knob) {
			t.Errorf("%s: the Check %s section does not name the knob %s, which %s reads", liveChecksDoc, check601Number, knob, check601Script)
		}
	}
}

// TestDocsCheck601ScriptKeepsItsPromises: the section's claims about the script
// are claims about this tree, so each one is read out of the script rather than
// trusted to the prose — among them the premise the rows rest on, that the
// installation is read IN the sandbox checkout, which is what a check that
// inherited the invoker's own working directory would quietly get wrong.
func TestDocsCheck601ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, check601Script)
	for _, want := range []string{
		"set -Eeuo pipefail",
		// The installation is read where the checkout is: `fugaro doctor` is
		// run from $SANDBOX and from nowhere else, and every later read is run
		// from the same directory.
		`run_bounded "$TIMEOUT" doctor "$SANDBOX"`,
		`cd "$dir" || exit 1; exec "$@"`,
		// The three names the check is never run against, and the guard that
		// refuses a checkout, project, region, bucket or job naming one.
		"*belong*)",
		"*edge-devel-dimi*)",
		"*edgeappinc*)",
		// A dry run calls nothing: the block it prints carries the guards and
		// its own row, and none of the criterion rows.
		"DRY_RUN",
		"BLOCK_KEYS=(fugaro-version xtrace agent-session belong dry-run)",
		// The scratch directory and the tokens in it are removed on every path.
		"trap finish EXIT",
		"trap on_signal INT TERM HUP",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s lacks %q, which the register's Check %s section promises about it", check601Script, want, check601Number)
		}
	}
	for _, banned := range []string{
		"storage cp", "storage rm", "storage mv", "run jobs execute", "run jobs update",
		"run deploy", "secrets versions add", "projects delete",
	} {
		if strings.Contains(script, banned) {
			t.Errorf("%s carries %q: the check creates nothing and deletes nothing, and its own section says so", check601Script, banned)
		}
	}

	// The test that can see where a call was made says so: the fakes log the
	// directory they were run in, and the case holds the reads to the sandbox.
	// Without that reading the section's "cannot be pointed at another one" is
	// a sentence nothing holds the script to.
	test := readRepoFile(t, check601Test)
	for _, want := range []string{
		`printf 'fugaro pwd=%s %s\n' "$PWD" "$*"`,
		`printf 'gcloud pwd=%s %s\n' "$PWD" "$*"`,
		"assert_reads_ran_in_sandbox",
		`assert_reads_ran_in_sandbox "$dir" "happy path"`,
	} {
		if !strings.Contains(test, want) {
			t.Errorf("%s lacks %q: the section says the rows are about the checkout's own installation, and the case that reads which directory a call was made in is what holds it to that", check601Test, want)
		}
	}
}

// TestDocsCheck601NumberIsTheFreeOne: the number is a claim taken under the
// register's own rule, and the section is where the reading is written. It is
// the half a later work moves: whoever merges a section of their own above the
// highest written one renumbers itself to the number free at that merge, so the
// section has to say both which number it took and that it is a claim rather
// than a reservation — and the row and the heading have to carry the same one.
func TestDocsCheck601NumberIsTheFreeOne(t *testing.T) {
	doc := readDoc(t, liveChecksDoc)
	if got := strings.Count(doc, "\n"+check601Heading); got != 1 {
		t.Fatalf("%s carries %d `%s` sections, want exactly one: a check's number is one section and one row", liveChecksDoc, got, check601Heading)
	}
	if got := strings.Count(doc, "\n| "+check601Number+" |"); got != 1 {
		t.Fatalf("%s carries %d `| %s |` rows, want exactly one", liveChecksDoc, got, check601Number)
	}
	section := flattenDoc(registerSection(t, doc, check601Heading))
	for _, want := range []string{
		"**The number this section takes.**",
		"is the number free at it",
		"It is a claim and never a reservation",
		// The number the claim moved from, so a reader of the older text knows
		// which one they are reading.
		"**530**",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s: the Check %s section's own reading of its number does not say %q: the number is a claim settled at this merge, and the section is where that is recorded", liveChecksDoc, check601Number, want)
		}
	}
}
