package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Check 124 — the Cloud Batch backend's own owner-run live check — as the
// register writes it.
//
// The opt-in `--provision-batch` shipped in 0.9.5 with the compute module,
// the reaper's invocation, `fugaro doctor`'s Batch half and `fugaro validate`'s
// offline checks behind it, and its live check was never written: the design
// (`docs/design/compute-backend.md`) measures the backend in six hand-made
// scripts (57a) and writes the post-build acceptance run (57b) in a design
// document, not in the register. Check 124 is the register's own, and it is
// written in four parts — 124a the plan and the two read-backs, 124b one
// ordinary sandbox task to a draft PR, 124c a forced preemption and the reaper,
// 124d the check's own cost and its cleanup — because each part is run (and
// restored) on its own.
//
// What these tests hold, and why the register's conventions need each one:
//
//   - **The guard.** Check 43's three hard stops and its project pinning are
//     the register's requirement for every check from 48 onward
//     (docs/plans/2026-10-14-project-centric.md; Check 59's sections say the
//     same in docs/plans/2026-10-15-desktop-console.md Task 4.3). The backstop
//     that would assert it register-wide (`TestChecklistChecksAreGuarded`) is
//     **not in this tree** — internal/cli/docs_compute_backend_test.go:995
//     asserts its absence, because the compute-backend plan's Task 12 is
//     written from it — so each check's own docs tests carry the assertion.
//     This check is *not* local: 124a applies Terraform, 124b submits a job,
//     124c deletes a VM. A section that dropped a stop is the defect the test
//     exists for.
//   - **FACT lines with a command and an expected output.** The four parts
//     assert things no hermetic test can (what a real plan shows, what a real
//     job's report carries, what the reaper prints), so each part's assertions
//     are written as `**FACT:**` lines carrying both the command and the output
//     it must print. The convention is machine-checkable here, which is what
//     stops a later edit from leaving a sentence asserting a property with
//     nothing under it — the finding Check 59's Task 4.3 was rewritten for.
//   - **Never `belong` as a target.** No step of any part may name `belong`,
//     EdgeWeb or EdgeServer as an argument to a command it runs; the only
//     permitted mentions are prose sentences that forbid it. That is the
//     machine-checkable half of the rule the whole register carries.
//   - **A restore step that spares what it did not create.** This is the one
//     that matters here: the check's cleanup must delete the job and the VM it
//     submitted, and must never delete the installation's own provisioning —
//     the preload image family, the two service accounts and their custom
//     roles, the Cloud NAT and its router, the two scheduler jobs (the preload
//     rebuild and the reaper sweep) and the reaper job itself. A restore step
//     that "cleans up the compute resources" would destroy the very
//     provisioning 124a just applied, and would read as thoroughness in review.
//
// Every test below is mutation-proven in the file itself: the mutation is named
// in the test's own comment, and this task's red step is these tests run against
// a register that carries no `## Check 124` section at all.

// check124Letters are the four parts Check 124 is written as, in order.
var check124Letters = []string{"a", "b", "c", "d"}

// check124BelongProhibitionRE is what a prose line mentioning `belong` has to
// say to be permitted: the prohibition itself. A line that mentions it any
// other way — a bare sentence in the middle of a step, a path, a comment — is
// not a prohibition, and the test refuses it rather than reading the sentence
// for intent.
var check124BelongProhibitionRE = regexp.MustCompile(`(?i)never|no step|no agent|does not|do not|is not|forbidden|refuse`)

// check124GuardLines are Check 43's three hard stops, as Check 124 has to carry
// them verbatim (with its own number in the messages): the block ran whole, the
// installation is the `fugaro-dev` one, and the checkout is the sandbox.
var check124GuardLines = []string{
	`: "${CHK124_OWNER_RUN:?export CHK124_OWNER_RUN=yes first`,
	`[ "$P" = fugaro-dev ] && [ -n "$L" ] && [ -n "$R" ] && [ -n "$U" ]`,
	"Check 124 stopped: re-run the whole setup block",
	`fugaro doctor --project fugaro --all | grep -q '^project: .*(GCP fugaro-dev,'`,
	"Check 124 stopped: this is not the fugaro-dev installation",
	`git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$'`,
	"Check 124 stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)",
}

// check124Section returns Check 124<letter>'s text, from its heading to the next
// `## ` heading or the end of the file. The slice is bounded by the heading and
// not by the end of the register, so that a check added after 124 does not
// silently widen what these tests read — the mistake Check 111's helper made
// until this branch bounded it (docs_setup_wizard_checks_test.go).
func check124Section(t *testing.T, letter string) string {
	t.Helper()
	register := readDoc(t, liveChecksDoc)
	head := "\n## Check 124" + letter + ":"
	_, rest, ok := strings.Cut(register, head)
	if !ok {
		t.Fatalf("%s has no `## Check 124%s:` section: Check 124 is written as four parts (124a the plan, 124b the run, 124c the preemption and the reaper, 124d the cost), and this test's marker has moved", liveChecksDoc, letter)
	}
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	if len(rest) < 1500 {
		t.Fatalf("%s's Check 124%s section is %d bytes: a check this short is not the part it is named for, and this test's markers have moved", liveChecksDoc, letter, len(rest))
	}
	return rest
}

// hasAnyPrefix is strings.HasPrefix over a list, for the two `fugaro` commands
// the register's `--project fugaro` rule does not apply to.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// check124FactLineRE counts the section's `**FACT:**` assertions, which start a
// line.
var check124FactLineRE = regexp.MustCompile(`(?m)^\*\*FACT:\*\*`)

// check124FactParagraphs returns the paragraphs that carry a `**FACT:**` line.
// The register states a fact as a sentence that may wrap, followed by the
// `**expected output:**` marker — on the FACT line or on the next one — so the
// marker is asserted per paragraph and not per physical line, and a check
// reading the `**FACT:**` line alone would refuse the register's own shape.
func check124FactParagraphs(section string) []string {
	var facts []string
	for _, para := range strings.Split(section, "\n\n") {
		if check124FactLineRE.MatchString(para) {
			facts = append(facts, para)
		}
	}
	return facts
}

// TestDocsCheck124IsWrittenAsFourPartsWithItsRow: the number is one check
// written as four sections (the 43a-43f and 59a/59b convention), each part
// owner-run and NOT RUN, and the register carries the summary row a reader
// finds it from. It also pins the allocation: 57 is this backend's reserved
// number — its section is the compute-backend plan's Task 12 — so 124 has to
// name the overlap and the plan's pins rather than be a second live check for
// one backend that nobody stated.
//
// Mutation (verified, run and restored): the `| 124 |` register row deleted →
// FAIL (a section with no row is a check no reader of the table finds);
// `## Check 124c:` folded into `## Check 124b:` → FAIL (124c is the part whose
// restore is different: the job is preempted and the VM is deleted by hand);
// the allocation paragraph's opening rewritten so it no longer reads as
// "**Check 57 is this backend's own number, and 124 is a second one" → FAIL
// (that paragraph is the only place the two numbers for one backend are told
// apart).
func TestDocsCheck124IsWrittenAsFourPartsWithItsRow(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !strings.Contains(register, "\n| 124 |") {
		t.Errorf("%s's register has no `| 124 |` row: Check 124 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	for _, letter := range check124Letters {
		section := check124Section(t, letter)
		for _, want := range []string{"**NOT RUN.**", "run by you", "No agent performs any step"} {
			if !strings.Contains(section, want) {
				t.Errorf("Check 124%s does not say %q: the register's owner-run checks say all three, and a check a reader could mistake for something an agent ran is the one claim these sections must not leave open", letter, want)
			}
		}
	}

	// The parts in order, and each heading naming its own subject: a reader
	// runs one part at a time, so the headings are the plan.
	var last int
	for _, letter := range check124Letters {
		i := strings.Index(register, "\n## Check 124"+letter+":")
		if i < last {
			t.Errorf("%s writes Check 124%s out of order: the four parts are one register entry and read top to bottom", liveChecksDoc, letter)
		}
		last = i
	}

	// The claim, and the reading it rests on: the numbers 114 to 123 are taken
	// or reserved, which is why 124 is free.
	claim := "**Check 124 is the Cloud Batch backend's own, and it is the next number free above 123**"
	if !strings.Contains(register, claim) {
		t.Errorf("%s does not claim 124: the register's numbering paragraph has to say which number this work takes and why it is free, or the next check writer cannot re-check the reading", liveChecksDoc)
	}
	_, after, ok := strings.Cut(register, claim)
	if !ok {
		t.Fatal("the claim sentence is gone")
	}
	claimText, _, _ := strings.Cut(after, "\n\n")
	for _, want := range []string{
		"114-118",   // the voice-session work's, numbered and not written
		"#922",      // the mobile telemetry plan's 120, 121 and 122
		"**123**",   // the console-dependency-graph plan's, merged on `main`
		"a9500ef3",  // the commit the reading was taken at
		"renumbers", // the rule that settles two open claims of one number
	} {
		if !strings.Contains(claimText, want) {
			t.Errorf("124's claim paragraph does not name %q: the reading has to be re-checkable from the paragraph alone, and \"the next free number\" cannot be read off `main`'s headings", want)
		}
	}

	// The overlap with Check 57, which is this backend's own number: a second
	// number for one backend is what the register's rule allows only if the
	// allocation is stated, and the plan's pins are what a reader checks it
	// against.
	alloc := "**Check 57 is this backend's own number, and 124 is a second one"
	if !strings.Contains(register, alloc) {
		t.Errorf("%s does not state 124's relation to Check 57: 57 is the compute backend's reserved number and its section is the plan's Task 12, so a 124 written beside it has to say which number is which rather than let a reader find two live checks for one backend", liveChecksDoc)
	}
	_, allocAfter, ok := strings.Cut(register, alloc)
	if !ok {
		t.Fatal("the allocation paragraph's opening is gone")
	}
	allocText, _, _ := strings.Cut(allocAfter, "\n\n")
	for _, want := range []string{
		"Check numbers this plan takes",
		"TestDocsComputeBackendPlanReservesNoCheckNumber",
		"never reused or",
		"Task 12",
		"overlap",
	} {
		if !strings.Contains(allocText, want) {
			t.Errorf("124's allocation paragraph does not name %q: the paragraph exists to state the overlap with 57 and the pins that hold the plan's side of it, and one without those is a disclosure a reader cannot check", want)
		}
	}

	// And the file still says where the register's written sections end, so a
	// reader is not left wondering whether the four parts are the last word.
	if !strings.Contains(register, "**That is the end of the register.**") {
		t.Errorf("%s no longer says where the register's written sections end: Check 124 is the fourth claim paragraph above that sentence, and the marker is what keeps the two apart", liveChecksDoc)
	}
}

// TestDocsCheck124EverySectionCarriesTheGuard: Check 43's three hard stops,
// verbatim, in each of the four parts, plus the two pinning rules — every
// `fugaro` command gets `--project fugaro` (bar `version` and `validate`, which
// take no such flag and are the register's own two exceptions), every `gcloud`
// command gets `--project=` from the guard's own `P`.
//
// The register-wide backstop that would assert this for every check from 48
// onward (`TestChecklistChecksAreGuarded`, the project-centric plan's Task 6.3)
// is not in this tree — docs_compute_backend_test.go:995 asserts exactly that —
// so this check carries its own assertion, as Check 59's sections do.
//
// Mutation (verified, run and restored): 124c's third stop deleted → FAIL (a
// section whose guard is two stops long is a check that can run against
// EdgeWeb's checkout); a `gcloud` line in 124d written without
// `--project="${P:?}"` → FAIL (gcloud resolves an omitted project from the
// operator's ambient configuration, which is a `*-prod` project).
func TestDocsCheck124EverySectionCarriesTheGuard(t *testing.T) {
	for _, letter := range check124Letters {
		section := check124Section(t, letter)
		for _, want := range check124GuardLines {
			if !strings.Contains(section, want) {
				t.Errorf("Check 124%s does not carry %q: the guard is Check 43's three hard stops, re-run from a fresh shell before every part, and a section without them is a section that can run on the wrong installation", letter, want)
			}
		}
		for _, line := range strings.Split(section, "\n") {
			code := strings.TrimSpace(line)
			// `fugaro version` and `fugaro validate` are the two commands with
			// no `--project` flag at all (`fugaro validate --help`); the
			// register says so where Check 43's rule is written out
			// (docs/gcp-live-checklist.md, "Every `fugaro` command that takes
			// `--project` gets `--project fugaro`"), and validate resolves the
			// project from the checkout the guard's third stop pins.
			if exempt := []string{"fugaro version", "fugaro validate"}; strings.HasPrefix(code, "fugaro ") && !hasAnyPrefix(code, exempt) {
				if !strings.Contains(code, "--project fugaro") {
					t.Errorf("Check 124%s runs %q without `--project fugaro`: the installation's name is what pins the command to the fugaro-dev one, and ommitting it resolves from the checkout or the operator's environment", letter, code)
				}
			}
			if strings.HasPrefix(code, "gcloud ") && !strings.Contains(code, "--project=") {
				t.Errorf("Check 124%s runs %q without `--project=`: gcloud would resolve the project from the operator's ambient configuration, which is a `*-prod` project", letter, code)
			}
		}
	}
}

// TestDocsCheck124SaysWhatCannotRunYet: the parts that need a backend this
// build cannot select say so in their own first lines, and part (a) FACT-lines
// the two refusals an owner meets today rather than describing them as passes.
//
// The standing, read off the tree rather than off the prose:
//
//   - `backend: cloud-batch` is refused by internal/localcfg
//     ("backend %q is not enabled yet (this build runs cloud-run)"), which is
//     the enable task's business — docs/plans/2026-10-19-compute-backend.md's
//     Task 10, whose coverage table says "to build". So 124b, 124c and 124d
//     are written and cannot be run yet, the same way Check 57b is.
//   - `fugaro doctor`'s Batch half is warning-shaped today, deliberately
//     (internal/cli/doctor_batch.go: "All of them are WARNINGS, never
//     failures: nothing in this build can select backend: cloud-batch"), and
//     the reading that follows from it is the opposite of the obvious one:
//     printDoctorChecks skips an OK check with no severity
//     (internal/cli/doctor.go), so the seven read checks print nothing at all
//     on a correctly provisioned installation, and the one line that always
//     prints is the `info batch-artifact-boundary` one — Severity "info", which
//     "does not fail doctor, with or without --strict … must not suppress the
//     summary line either". So the pass is that info line plus
//     `fugaro doctor: everything checked is ok`, and a `warning batch-` line is
//     the finding. The FACT has to say which line appears and which do not, and
//     a section that had it the other way round would have an owner record a
//     defect on a healthy installation.
//
// Mutation (verified, run and restored): "**124b, 124c and 124d are written and
// cannot be run yet**" rewritten to say the backend can be selected today →
// FAIL (a reader would run a part that cannot pass); the doctor FACT's expected
// exit of 0 rewritten to 1 → FAIL (the exit code is the half of the reading
// that says a warning is what a non-zero exit means on this setup).
func TestDocsCheck124SaysWhatCannotRunYet(t *testing.T) {
	flat := flattenDoc(check124Section(t, "a"))
	for _, want := range []string{
		"not enabled yet (this build runs cloud-run)",
		"docs/plans/2026-10-19-compute-backend.md",
		"the enable task",
		"124b, 124c and 124d are written and cannot be run yet",
		"WARNINGS, never failures: nothing in this build can select",
		// The healthy reading, in the direction the tree gives it: the one
		// always-printed info line, the summary line that stays, and the
		// non-zero-exit reading a warning is.
		"info batch-artifact-boundary",
		"fugaro doctor: everything checked is ok",
		"exit: 0",
		"warning batch-",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 124a does not say %q: the parts that cannot run yet have to say why in their own text, and the doctor reading has to name the line doctor always prints, the summary line that stays and the line that is the finding", want)
		}
	}

	// The four parts' own headers name their subject, so an owner who reads the
	// register top to bottom can tell what each one is for without reading it.
	for _, want := range []string{
		"## Check 124a:", "the plan", "## Check 124b:", "## Check 124c:", "## Check 124d:",
	} {
		if !strings.Contains(readDoc(t, liveChecksDoc), want) {
			t.Errorf("%s has no %q: the four headings are the plan a reader follows", liveChecksDoc, want)
		}
	}
}

// TestDocsCheck124FactsAreFactLined: every part's assertions are `**FACT:**`
// lines carrying a command and an expected output, and the ones the check's
// claims rest on are all there.
//
// The three that carry the most weight, and why each is a FACT and not a
// sentence:
//
//   - The plan's own output (124a): `fugaro setup --plan-only --provision-batch`
//     is what says the Batch resources are exactly the design's — the two APIs,
//     the run accounts' IAM, the launcher's, reaper's and preload accounts and
//     their custom roles, the Cloud NAT and its router, the preload's scheduled
//     build — and the stage's confirmation text names the same list.
//   - The preemption exit code (124c): the number has **one home**, the tree's
//     `preemptExitCode` (internal/backend/batch/run.go), and it is written
//     deliberately nowhere under `docs/` — docs_compute_backend_test.go's
//     TestDocsComputeBackendSaysWhatTheTreeDisagreesWith walks `docs/` and
//     fails on the literal outside the plan. So the FACT reads the constant out
//     of the file it lives in (`grep -n 'preemptExitCode =' …`) and asserts the
//     job's report carries *that* number, rather than typing a digit that would
//     be a second home for it.
//   - The run record (124c): nothing writes a preempted run's record — the
//     owner's ruling, design §1.3 and the compute-backend plan's open item 10 —
//     so the FACT reads the derived row (`failed (preempted)`, the read side's
//     `DerivedPreempted`/`ReasonPreempted`, internal/runview) and the reaper's
//     own lines, and says what is not shown.
//
// Mutation (verified, run and restored): the preemption FACT's
// `grep -n 'preemptExitCode =' internal/backend/batch/run.go` command replaced
// by the literal `50001` → FAIL, on this test *and* on
// TestDocsComputeBackendSaysWhatTheTreeDisagreesWith (the digit under `docs/`
// is the second home the constant exists to prevent); a FACT line deleted from
// 124d → FAIL (the cost and the "no orphan" read-back are the part's whole
// subject).
func TestDocsCheck124FactsAreFactLined(t *testing.T) {
	wantFacts := map[string][]string{
		"a": {
			"fugaro setup --plan-only --provision-batch --project fugaro",
			`module.compute.google_project_service.this["compute.googleapis.com"]`,
			`module.compute.google_project_service.this["batch.googleapis.com"]`,
			"module.compute.google_compute_router.this",
			"module.compute.google_compute_router_nat.this",
			"module.compute.google_project_iam_custom_role.reaper",
			"module.compute.google_project_iam_custom_role.preload_builder",
			"module.compute.google_service_account.reaper",
			"module.compute.google_service_account.preload",
			`Plan: `,
			"--plan-only: nothing applied; the plan is",
			"fugaro doctor --project fugaro",
			"batch-apis",
			"batch-image-family",
			"batch-network",
			"batch-launcher-iam",
			"fugaro validate",
		},
		"b": {
			"fugaro run",
			`labels.fugaro_run`,
			"fugaro runs ls",
			"draft pull request",
		},
		"c": {
			`grep -n 'preemptExitCode =' internal/backend/batch/run.go`,
			"gcloud batch jobs describe",
			`labels.fugaro_run`,
			"gcloud compute instances delete",
			"FAILED",
			"failed (preempted)",
			"fugaro reaper --dry-run --project fugaro",
			"fugaro reaper --project fugaro",
			"reaped projects/",
		},
		"d": {
			"fugaro budget show --project fugaro",
			"gcloud batch jobs list",
			"gcloud compute instances list",
			"gcloud compute instance-templates list",
			"fugaro runs ls --project fugaro",
		},
	}
	for letter, wants := range wantFacts {
		section := check124Section(t, letter)
		if n := len(check124FactLineRE.FindAllString(section, -1)); n < 4 {
			t.Errorf("Check 124%s carries %d `**FACT:**` line(s): the part's assertions are facts about a real installation, and a section that asserts them in prose instead is the finding Check 59's Task 4.3 was rewritten for", letter, n)
		}
		for _, fact := range check124FactParagraphs(section) {
			if !strings.Contains(fact, "expected output:") {
				first, _, _ := strings.Cut(fact, "\n")
				t.Errorf("Check 124%s carries a FACT line with no expected output: %s", letter, first)
			}
		}
		for _, want := range wants {
			if !strings.Contains(section, want) {
				t.Errorf("Check 124%s does not carry %q: this is one of the commands or outputs the part's claim rests on", letter, want)
			}
		}
	}

	// The digit is not written into the register, deliberately: the preemption
	// exit code has one home in the tree, and a second one under `docs/` is the
	// defect TestDocsComputeBackendSaysWhatTheTreeDisagreesWith exists for.
	all := check124Section(t, "c") + check124Section(t, "d")
	if strings.Contains(all, "50001") {
		t.Errorf("Check 124c or 124d writes the preemption exit code as a literal: the number has one home, internal/backend/batch/run.go's preemptExitCode, and docs_compute_backend_test.go walks `docs/` and fails on the literal outside the compute-backend plan. Read it out of the file instead (`grep -n 'preemptExitCode =' internal/backend/batch/run.go`) and assert the job's report carries that value")
	}

	// And the preemption is forced by a method that says how, with the guard
	// that keeps it on the check's own VM: the VM is selected by the label value
	// the job itself records, never by a name an operator typed.
	c := check124Section(t, "c")
	for _, want := range []string{
		"its own job's",
		`labels.fugaro_run`,
		"refuses to delete a VM whose label does not",
		"sandbox",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("Check 124c does not say %q: the forced preemption deletes a VM by hand, so the step has to select it from the job's own recorded label and refuse anything else", want)
		}
	}

	// What the part does NOT show. The design's §7.2 step 7 (a hand-submitted
	// Spot job, the platform's own reclaim) and its step 11 (quota) are still
	// unrun, and no Fugaro run is Spot on this tree, so the hand-delete is a
	// simulated reclaim rather than the platform's.
	for _, want := range []string{
		"What this part does NOT show",
		"no Fugaro run is Spot today",
		"§7.2",
		"simulated",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("Check 124c does not carry %q: the check has to say what it does not show, or a reader takes a hand-deleted VM for the platform's own reclaim", want)
		}
	}
}

// TestDocsCheck124NeverNamesBelongAsATarget: the string `belong` is never an
// argument to a command the check runs.
//
// The rule is the register's (and the project-centric plan's Global Constraint
// 6, which Check 59's Task 4.3 made machine-checkable): the only permitted
// mentions are prose sentences that forbid it. Unlike Check 59, this check has
// no mutation-control line that has to write the name into a scratch file — it
// touches no installation but the sandbox — so the rule is the stricter one:
// nothing inside a code block mentions it at all, and every prose mention is a
// prohibition.
//
// Mutation (verified, run and restored): a `fugaro --project belong budget
// show` line added to 124d → FAIL (the forbidden way of "verifying" the
// negative); `belong` written into a guard message → FAIL (a stop message is a
// command argument).
func TestDocsCheck124NeverNamesBelongAsATarget(t *testing.T) {
	for _, letter := range check124Letters {
		section := check124Section(t, letter)
		inFence := false
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if !strings.Contains(line, "belong") {
				continue
			}
			if inFence {
				t.Errorf("Check 124%s mentions `belong` inside a code block: %q — every line in a block is a command, a variable or a message a command prints, and no part of this check may name that installation as a target", letter, strings.TrimSpace(line))
				continue
			}
			if !check124BelongProhibitionRE.MatchString(line) {
				t.Errorf("Check 124%s mentions `belong` outside a prohibition: %q — the only permitted mention is a sentence saying no step touches it", letter, strings.TrimSpace(line))
			}
		}
	}

	// And the prohibition is actually stated: a check that never mentions the
	// name at all is a check whose silence a reader cannot tell from an
	// oversight.
	if !strings.Contains(flattenDoc(check124Section(t, "a")), "never `belong`") {
		t.Errorf("Check 124a does not state the prohibition (\"never `belong`\"): the register's owner-run checks name it, and the section that applies Terraform is the one where it matters most")
	}
}

// TestDocsCheck124RestoreStepSparesWhatItDidNotCreate: the four parts' restore
// steps delete what the check created and nothing else — never the
// installation's own provisioning, which 124a's apply just built.
//
// This is the test that matters most in this file. The check applies the
// compute root, so an owner tidying up with "terraform destroy" or with a
// `gcloud … delete` on the family, the accounts, the NAT, the router or the two
// scheduler jobs would destroy the installation's Batch provisioning — the very
// thing 124a was run to prove — and the register's restore rule is the opposite:
// a restore step restores only what the check created (Check 59's sections say
// it in the same words, and the project-centric plan requires it of every check
// from 48 onward).
//
// The deletion that IS permitted is the check's own: the job 124b submitted and
// the VM 124c selected by that job's own `fugaro_run` label value. Both are by a
// recorded name or a label read back off the job, never by a name typed into the
// check, which is what lets the restore be checked against the before-state the
// part recorded.
//
// Mutation (verified, run and restored): 124d's restore given a
// `gcloud compute instance-templates delete fugaro-preload-…` line → FAIL (the
// family the runs boot from, deleted by the cleanup); 124c's
// `gcloud compute instances delete "${VM:?}"` replaced by a literal instance
// name → FAIL (a VM the check did not select from its own job's label).
func TestDocsCheck124RestoreStepSparesWhatItDidNotCreate(t *testing.T) {
	banned := []string{
		"terraform destroy",
		"gcloud projects delete",
		"gcloud iam service-accounts delete",
		"gcloud compute networks delete",
		"gcloud compute routers delete",
		"gcloud compute routers nats delete",
		"gcloud compute firewall-rules delete",
		"gcloud compute instance-templates delete",
		"gcloud scheduler jobs delete",
		"gcloud run jobs delete",
		"gcloud artifacts repositories delete",
		"gcloud storage rm",
		"gcloud compute images delete",
		"fugaro project policy delete",
		"fugaro project rm",
	}
	for _, letter := range check124Letters {
		section := check124Section(t, letter)
		for _, b := range banned {
			if strings.Contains(section, b) {
				t.Errorf("Check 124%s carries %q: the installation's own provisioning is not the check's to destroy, and the restore rule is that it deletes only what the check created", letter, b)
			}
		}

		// The restore deletes by a recorded name or a recorded label value, and
		// the before-state is a FACT rather than an assumption.
		for _, line := range strings.Split(section, "\n") {
			if !strings.Contains(line, "compute instances delete") {
				continue
			}
			if !strings.Contains(line, `"${`) {
				t.Errorf("Check 124%s deletes a VM by a name it did not read out of the job: %q — the VM belongs to the run's own job, and the label value the job records is what says which VM that is", letter, strings.TrimSpace(line))
			}
		}
	}

	// The two records the restore is checked against, and the label the
	// selection is made by.
	for _, want := range []string{"instances-before", "jobs-before"} {
		if !strings.Contains(check124Section(t, "d"), want) {
			t.Errorf("Check 124d does not record %q: \"nothing was left behind\" is only a fact against a before-state, and the restore itself is only safe against one", want)
		}
	}
	for _, letter := range []string{"b", "c", "d"} {
		if !strings.Contains(check124Section(t, letter), "labels.fugaro_run") {
			t.Errorf("Check 124%s does not select by `labels.fugaro_run`: every run's job and its VM carry that label, and it is the only handle the check has on resources it did not name", letter)
		}
	}

	// And the spared list is written down, so a later reader cannot mistake
	// this restore for a licence to clear the installation.
	spared := flattenDoc(check124Section(t, "d"))
	for _, want := range []string{
		"the installation's own, never deleted",
		"the preload image family",
		"the reaper's and the preload's service accounts",
		"the two custom roles",
		"the Cloud NAT and its router",
		"the preload rebuild's and the reaper sweep's scheduler jobs",
		"the reaper job",
	} {
		if !strings.Contains(spared, want) {
			t.Errorf("Check 124d does not name %q among what the restore spares: the list is what stops a follow-the-recipe cleanup from deleting the installation's Batch provisioning", want)
		}
	}
}
