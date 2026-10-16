package cli

import (
	"strings"
	"testing"
)

// TestChecklistHasCheck38: docs/gcp-live-checklist.md's Check 38 is the
// owner-run live check for checkout: clone (design generic-tool.md G4, plan
// Tasks 16 to 18). Since 0.7.5 the check must be runnable as written: the
// release's runner reads FUGARO_CHECKOUT and clones blobless, cloneSince is
// "0.7.5" and the sandbox installation's base_images.base is already that
// release image, so the ONLY build the check makes is a local binary from a
// scratch checkout of origin/main with cloneShipped set to true by hand. The
// stale version of this section made the owner build and push a dev base
// image (about 1.5 GB), rewrite base_images.base and delete it again; none of
// that is needed, and a doc that still says so sends the owner to a registry
// write this check does not want.
//
// The second half of the assertions is the review of #446, and they are the
// safety half: the revert on the sandbox's base branch has to name the
// throwaway commit (a revert of HEAD undoes step 1's own checkout: restore
// when the base re-render was merged as a squash or a fast-forward, and
// leaves checkout: clone on the base branch), the records the Restore reads
// have to be the ones the check's own steps wrote, the guard has to reach the
// shell that runs the steps, and the order the owner follows has to be
// written down once rather than inferred from the order of the paragraphs.
//
// Every row of the banned table below was mutated into the section and proven
// red (the row's text inserted, the test run, the insertion reverted), and a
// mutation of each required string was proven red the same way; the strings
// asserted here are the ones a later edit could silently drop.
func TestChecklistHasCheck38(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 38")

	// The shape: user-run, not run, sandbox only, never belong.
	for _, want := range []string{"USER-RUN, NOT RUN", "run by you", "sandbox", "Never `belong`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section does not say %q", want)
		}
	}

	// The guard block, re-run at the start of every step: the project is
	// hard-coded, every fugaro command names its project, doctor is the
	// installation check and the checkout's origin is the sandbox. It also
	// sets S, the record's path, because the Restore and step 4 read $S and
	// the guard is what a fresh shell is given.
	for _, want := range []string{
		"RE-RUN THIS WHOLE BLOCK", "P=fugaro-dev", "${P:?}",
		"fugaro doctor --project fugaro", "--project fugaro --gcp-project",
		"edgeappinc/fugarosandbox", "S=/tmp/check38-state",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's guard block does not carry %q", want)
		}
	}

	// The guard has to reach the shell the steps run in: run with `bash` it
	// is a subshell whose variables, PATH and S are gone the moment it exits,
	// and the later steps then run in a shell that has none of them.
	for _, want := range []string{". ./check38-env.sh", "source"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section does not say to source the guard file (%q)", want)
		}
	}

	// The recorded state: taken once, before the first change, and a leftover
	// record from an earlier attempt refuses the run. No other check in the
	// file carries this rule (Checks 50 and 58 are reserved numbers with no
	// section yet), so the assertions here are the only thing pinning it. The
	// records are written into a temp directory and moved into place only
	// once every read succeeded: a failed doctor used to leave a partial
	// record behind, and the next attempt then refused with a false alarm
	// ("an earlier attempt did not finish its Restore") about an attempt that
	// never changed anything.
	for _, want := range []string{
		"check38-state", "left over", "before the first change", "mktemp -d",
		`mv "$T" "$S"`, "[ -s \"$T/$f\" ]",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's recorded state does not say %q", want)
		}
	}
	// ... and the move happens after every read and its fail-closed check,
	// not merely somewhere in the block: a record could carry the temp
	// directory and still write or check the final path directly, which is
	// the partial-record bug the temp directory is there to prevent.
	if move, checked := strings.Index(section, `mv "$T" "$S"`), strings.Index(section, `[ -s "$T/$f" ]`); move < 0 || checked < 0 || move < checked {
		t.Error("the Check 38 section's recorded state does not move the temp directory into place after the reads that fill it")
	}

	// The recorded base image is re-taken if the prerequisite move had to
	// change it: step 2 and the Restore both judge the installation against
	// $S/base-image, so a record still holding the pre-move ref would fail
	// step 2 on a correct installation and have the Restore undo the fix.
	for _, want := range []string{"$S/base-image", "base-image-moved-from", "re-record"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's base-image record does not say %q", want)
		}
	}

	// The recorded HEAD is informational, and says so: it is the base
	// branch's tip when the record was taken, which is not what anything
	// reverts to — the Setup merge and the Restore's own merge both land on
	// $BASE after that moment — so it must not be named as the revert's
	// target. The revert names $S/base-commit instead.
	for _, want := range []string{"$S/base-head", "informational"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's recorded HEAD does not say %q", want)
		}
	}

	// The order, written once: the Setup paragraph needs step 0's build for
	// its `init`, and it is printed above step 0, so an owner reading the
	// section top to bottom would run it without the build and be refused.
	for _, want := range []string{
		"The order, once",
		"**(a)** take the recorded state",
		"**(b)** edit the sandbox workflow",
		"**(c)** step 0's local build",
		"**(d)** `fugaro init --repo .`",
		"**(e)** step 1's run",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's ordering does not list %q", want)
		}
	}

	// The one build: the local binary, the flag set by hand, never pushed.
	for _, want := range []string{"cloneShipped", "go build", "check38-bin", "never committed", "scratch checkout of `origin/main`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's one build does not say %q", want)
		}
	}

	// The base image is verified, not built: read out of the installation,
	// judged against cloneSince, and fixed with the release binary if it is
	// older. The fix path is the one billable thing in the section, and says
	// so, because `fugaro image refresh` rebuilds what is stale.
	for _, want := range []string{"base_images.base", "cloneSince", "fugaro init --base", "fugaro image refresh"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's base-image step does not name %q", want)
		}
	}
	for _, want := range []string{"$0.21", "billable"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section does not price the prerequisite fix path (%q)", want)
		}
	}

	// The table row prices the same check the same way, and it is outside the
	// section (docSection stops at the next heading). "nothing billable" was
	// scoped to registry storage in the old text; the check does launch real
	// sandbox runs, and the row says so in its own first words — a whole-check
	// "nothing billable" understates what the owner is agreeing to.
	if i := strings.Index(doc, "\n| 38 |"); i >= 0 {
		row := doc[i+1:]
		if j := strings.Index(row, "\n"); j >= 0 {
			row = row[:j]
		}
		if strings.Contains(row, "nothing billable") {
			t.Error("the Check 38 table row still calls the check free")
		}
		if !strings.Contains(row, "$0.21") {
			t.Error("the Check 38 table row does not price the prerequisite fix path")
		}
	} else {
		t.Error("the Check 38 table row is gone")
	}

	// With a release base image at cloneSince the gate warns about nothing,
	// and the section has to say so — and only that: the budget precheck and
	// other launch warnings can print, so "any warning at all is a finding"
	// is wrong and would send the owner after a warning this check does not
	// own.
	for _, want := range []string{"no gate warning", "no `checkout: clone` gate warning"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section does not say %q", want)
		}
	}
	if strings.Contains(section, "Any warning at all") {
		t.Error("the Check 38 section still calls any warning at all a finding")
	}

	// The Restore, which is where an attempt that never finished is caught:
	// it branches on the recorded state (the sandbox's own fugaro.yaml set
	// the key, so write it back; it did not, so drop it), and deletes the
	// record last. A later edit that silently dropped any of these would
	// leave a half-restored sandbox with nothing to notice it.
	for _, want := range []string{
		"$S/checkout", "$S/checkout-source", "says `repo`", "says `default`",
		"`rm -rf \"$S\"`", "last",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's Restore does not say %q", want)
		}
	}

	// Step 4's throwaway commit on the base branch is reverted by the SHA
	// step 4 recorded, on a fresh checkout of origin/$BASE, in a clone whose
	// origin is asserted against the literal sandbox URL first, and the
	// Restore refuses rather than guessing when the record is missing or the
	// commit is not an ancestor. Never a forced push.
	for _, want := range []string{
		"$S/base-commit", "git merge-base --is-ancestor",
		`git revert --no-edit "$(cat "$S/base-commit")"`,
		"origin/${BASE}", "https://bitbucket.org/edgeappinc/fugarosandbox.git", "`--force`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's base-branch revert does not say %q", want)
		}
	}

	// The step 4 chain checks the variables it needs at the TOP, before its
	// first push, and records the base commit before that push: ${BASE:?}
	// used to appear only halfway down, so an unset BASE left the branch
	// commit already pushed — the step half done — and nothing named the
	// commit the Restore has to revert.
	top := strings.Index(section, `: "${BASE:?`)
	record := strings.Index(section, `git rev-parse HEAD > "$S/base-commit"`)
	firstPush := strings.Index(section, `git push origin "${BASE}"`)
	switch {
	case top < 0 || record < 0 || firstPush < 0:
		t.Errorf("the Check 38 section's step 4 chain is gone (vars at %d, record at %d, push at %d)", top, record, firstPush)
	case top > firstPush:
		t.Error("the Check 38 section's step 4 chain checks ${BASE:?} only after its first push")
	case record > firstPush:
		t.Error("the Check 38 section's step 4 chain records $S/base-commit only after its first push")
	}

	// The old step 0, gone: no dev base image, no docker, no registry write,
	// no billable upload, no base-image rewrite, no credential-helper edit,
	// no forced push. And, from the review, no unqualified revert of HEAD
	// and no record named as something it is not.
	for _, row := range []struct{ banned, why string }{
		{"images/build-base.sh", "the removed dev-base build script, by its path"},
		{"build-base.sh", "the same script named without the images/ prefix"},
		{"docker push", "a registry write this check must never make"},
		{"docker images delete", "the old Restore's deletion of the dev base image"},
		{"configure-docker", "the credential-helper registration the old step 0 needed"},
		{"credHelpers", "the same edit in any other wording"},
		{"--base-image", "a rewrite of the installation's base image"},
		{"dev-<commit>", "the dev tag the old step 0 pointed the installation at"},
		{"1.5 GB", "the billable upload the old step 0 made"},
		{"revert --no-edit HEAD", "the revert of whatever HEAD is, not of the check's commit"},
		{"sandbox-head", "the recorded HEAD named as the revert's target"},
		{"git push --force", "a forced push to the sandbox's base branch"},
		{"`bash` sub-shell", "the old advice to run the guard where its variables die"},
	} {
		if strings.Contains(section, row.banned) {
			t.Errorf("the Check 38 section still carries %q (%s)", row.banned, row.why)
		}
	}
}
