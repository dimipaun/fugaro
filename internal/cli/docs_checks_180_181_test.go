package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Checks 180 and 181 — the Bitbucket workspace-token work's owner-run checks —
// as the register writes them.
//
// The work takes the check block 180–189 (docs/bitbucket-workspace-token/
// implementation-plan.md, "Checks this plan takes"). 180, the one workspace or
// project token serving many repositories, is task T1.3's to write and the
// owner's to run in T1.4, once a **Premium** sandbox workspace exists (U6).
// 181, the shared project job end to end and the allow-list refusal at publish,
// at deploy and in the runner, is task T2.6's, and it lands in this list then:
// T2.6 and T1.3 write the same register and the same test file, and the plan's
// shared-files table says which of them writes it first. Until T2.6's section
// exists there is nothing of 181 to hold, and a pin that asked for it would be a
// pin on unwritten text.
//
// What these tests hold, and why the register needs each one: a check is
// registered by two things — a row in the summary table and a `## Check N`
// section below it — and either half alone is a number a reader cannot use. A
// section without its row is a check nothing points at, and a row without its
// section is a promise of steps that are not written. Nothing in this tree
// asserts that pairing register-wide: the backstop that would
// (`TestChecklistChecksAreGuarded`) is deliberately absent
// (docs_compute_backend_test.go asserts its absence), so each work's own docs
// tests carry the assertion for its own numbers. This file carries it for
// 180–189, and check 180 is the one of them written.
//
// The red step is this file run against a register that has neither half: the
// first failure names the row that is missing, and the second the section.

// bitbucketWorkspaceRegisterChecks are the checks of this block the register
// writes today. 181 joins them with T2.6's section, and no other number of the
// block is owed.
var bitbucketWorkspaceRegisterChecks = []string{"180"}

// bitbucketWorkspaceCheck180Command is the invocation the register documents,
// argument for argument, in the row and in the section below it. The four
// arguments after --installation are required — the script refuses to run
// without the token file, the two repository pairs and the probe repository —
// so a register that names `check180.sh --installation <sandbox>` alone
// documents a command that exits 2 with a usage error before it measures
// anything. The two halves carry the same string, and folding the section's
// whitespace is what lets one constant pin both.
const bitbucketWorkspaceCheck180Command = "scripts/live-checks/check180.sh --installation <sandbox> --token-file <the token file> --repo <owner/name>=<checkout path> --repo <owner/name>=<checkout path> --probe-repo <owner/name>"

// bitbucketWorkspaceCheckRowRE matches a whole register summary row: the line
// whose first cell is the number between two pipes, alone. The `|` after the
// number is what anchors it, so a number cited inside another row's prose is not
// a row of its own. The rest of the line comes with it, because the row is the
// half a reader scans: a check whose script is named only in the section below
// is one a reader has to scroll to find.
func bitbucketWorkspaceCheckRowRE(number string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\|\s*` + number + `\s*\|.*$`)
}

// TestDocsChecks180181AreRegistered: Check 180 has both halves of its
// registration — a row in the summary table and a `## Check 180:` section below
// it — and both say the check has not been run, that the owner runs it and that
// no agent runs any step of it. The section also names the one command the owner
// types and the test that drives it dry, because a check that ships as a script
// is registered by naming it: a section whose steps are a second, hand-kept
// list is how the register and the script come to disagree.
func TestDocsChecks180181AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range bitbucketWorkspaceRegisterChecks {
		row := bitbucketWorkspaceCheckRowRE(number).FindString(register)
		if row == "" {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, number, number)
		} else {
			flatRow := strings.ToLower(flattenDoc(row))
			for _, want := range []string{
				// The register's own marker for an owner-run check that has
				// not been run. A row that lost either half reads as something
				// an agent did, on a sandbox.
				"**user-run, not run.**",
				"no agent runs any step of it",
				// The workspace and installation this check must never touch.
				"never `belong`",
				// Where the owner's one command and its dry driver live: the
				// row is the half a reader scans, so the script has to be
				// named there and not only in the section below — and named
				// with the arguments it refuses to run without.
				bitbucketWorkspaceCheck180Command,
				"scripts/live-checks/check180_test.sh",
			} {
				if !strings.Contains(flatRow, want) {
					t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under and the script you type", number, want)
				}
			}
		}
		heading := "\n## Check " + number + ":"
		i := strings.Index(register, heading)
		if i < 0 {
			t.Errorf("%s has no `## Check %s:` section: the row above points at steps that have to be there", liveChecksDoc, number)
			continue
		}
		// Bounded by the next `## ` heading, so a check written after this one
		// does not widen what is read here.
		section := register[i+1:]
		if end := strings.Index(section, "\n## "); end >= 0 {
			section = section[:end]
		}
		// Flattened and folded, because the register's prose wraps: a section
		// that says "no agent runs any step of\nit" says the same thing as one
		// that fits it on a line, and a pin that missed it would be a pin on the
		// line breaks rather than on the claim.
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			// The register's own rule for an owner-run check: no agent runs any
			// step of it, and the text has to say so.
			"no agent runs any step of it",
			// The command and its dry driver, the command with every argument
			// the script requires: the section is where the owner copies it
			// from, so it is the half where a missing argument is a run that
			// exits 2 instead of measuring.
			bitbucketWorkspaceCheck180Command,
			"scripts/live-checks/check180_test.sh",
			"never `belong`",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", number, want)
			}
		}
	}

	// The rows and the sections run in number order, so a reader who follows the
	// table top to bottom meets the sections in the same order.
	at := 0
	for _, number := range bitbucketWorkspaceRegisterChecks {
		i := strings.Index(register[at:], "\n## Check "+number+":")
		if i < 0 {
			t.Errorf("%s writes Check %s's section out of order: the block reads in the order the numbers give", liveChecksDoc, number)
			continue
		}
		at += i
	}
}

// TestDocsCheck180StatesWhatOnlyATokenSettles: the section has to carry the
// readings the check exists for — U1–U5 and U7 of the reconciliation, none of
// which any hermetic test can make — in the words the operator follows, because
// T1.4 is the owner running this section and no other document holds its steps.
// Each sentence here is one no offline run can produce: what the token's own
// identity is, whether the scopes are enough to approve and merge, what a
// repository added to the Bitbucket project afterwards does, and what a second
// machine without the allow-list entry plans.
func TestDocsCheck180StatesWhatOnlyATokenSettles(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	i := strings.Index(register, "\n## Check 180:")
	if i < 0 {
		t.Fatalf("%s has no `## Check 180:` section: this test reads the section's own claims, and the registration test above is the one that says it is missing", liveChecksDoc)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := flattenDoc(section)
	for _, want := range []string{
		// The credential the check is about (D461), and the scopes it must
		// carry to be enough (F3).
		"a **project access token** with `repository:write` and `pullrequest:write`",
		// The store, and the allow-list behind it: accepted here is what says
		// the local config's `project_secrets.bitbucket-token` entry names the
		// repositories the token is meant for.
		"`fugaro secrets set bitbucket-token --project-wide < <token-file>`",
		// U1: the clone and the push, with the username Bitbucket's own
		// documentation gives for a token.
		"`x-token-auth`",
		// U2, both halves: the run's pull request is authored by the token, and
		// `GET /2.0/user` is what the token answers about itself.
		"`TestLiveBitbucket`",
		"`GET /2.0/user`",
		// U4: approve and merge, which only the scopes settle.
		"approve and merge",
		// How the run's own row is read: `fugaro run` returns at launch, so
		// the script has to wait for a final row rather than read the row the
		// launch left behind — the reading the check exists for is the run's,
		// and an in-flight row has no push and no pull request in it.
		"it polls the listing until the row for the run it just launched",
		// U3: a repository created in the Bitbucket project after the
		// allow-list was written.
		"added to the Bitbucket project afterwards",
		// The rotation (F3), and the reading it turns on.
		"the next run uses the new version",
		// U5 and D467: the second machine, and the command that shows what it
		// would do without the entry.
		"`fugaro update --repo <path> --plan-only`",
		// The second machine's credentials, BY PATH: the step runs the CLI in a
		// scratch home, so an ADC the check did not name would be looked for
		// there and found nowhere -- and the step's row has to say which flag
		// names the directory rather than report a pass it cannot make.
		"`--gcloud-config <dir>`",
		"**no credential is copied** into the scratch home",
		// How the verdict is read. A step that could not verify its criterion
		// has to make the block's last line say so: an UNVERIFIED row inside a
		// block that exits 0 reads as a pass, which is exactly the way this
		// check would lie about the token.
		"only an all-PASS block is a PASS",
		"`CHECK 180 RESULT: PASS (exit 0)`",
		"`INCOMPLETE (exit 3)`",
		"`UNVERIFIED`",
		// U6, the reason the check cannot run today.
		"no Premium sandbox workspace is known",
	} {
		if !strings.Contains(flat, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 180's section does not say %q: the check is the only place these facts are ever measured, and T1.4 runs this section", want)
		}
	}
}
