package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fencedBlocks returns the body of every fenced code block in a document
// section, indented ones included (step 0's build block sits inside a list
// item). The info string (```bash) is dropped: what comes back is what a shell
// would be given.
func fencedBlocks(section string) []string {
	var out []string
	var cur []string
	in := false
	for _, l := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if in {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, l)
		}
	}
	return out
}

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
	for _, want := range []string{"USER-RUN, NOT RUN", "run by you", "Never `belong`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section does not say %q", want)
		}
	}
	// ... and "sandbox" alone was toothless: the word appears in dozens of
	// sentences, so asserting it said nothing. The property it stood for is
	// that every step runs against the sandbox repository, and that the guard
	// is what enforces it — its third hard stop is the checkout's origin.
	for _, want := range []string{"this checkout's origin is not the sandbox", "edgeappinc/fugarosandbox"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's guard does not prove the sandbox checkout (%q)", want)
		}
	}

	// An apostrophe inside a ${VAR:?...} message is not a message with an
	// apostrophe: bash parses the word expecting a closing quote, so the block
	// fails with "unexpected EOF while looking for matching `''" and none of it
	// runs. Step 4's chain carried two ("step 1's run ID", "step 1's pull
	// request number"), which broke the whole chain — the files, the commits
	// and both pushes — and Check 43 carried a third and a fourth. The bash
	// parse below (over the section's fenced blocks) is the same defect's other
	// face; this one names it, and covers the whole file, because the message
	// is the only place a stray apostrophe is invisible.
	apostropheInVarMsg := regexp.MustCompile(`\$\{[^}\n]*:\?[^}\n]*'`)
	if m := apostropheInVarMsg.FindString(doc); m != "" {
		t.Errorf("a ${VAR:?...} message carries an apostrophe, which bash reads as an unclosed quote: %s", m)
	}

	// Every fenced block in this section is meant to be pasted into a shell
	// and run, so every one of them has to parse. bash -n is the check that
	// does not need anything installed or any cloud: it is what caught the
	// apostrophes above.
	blocks := fencedBlocks(section)
	if len(blocks) < 8 {
		t.Errorf("the Check 38 section has %d fenced blocks; the extractor or the section lost some", len(blocks))
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		for i, b := range blocks {
			f := filepath.Join(t.TempDir(), fmt.Sprintf("check38-%d.sh", i))
			if err := os.WriteFile(f, []byte(b), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
				t.Errorf("Check 38's fenced block %d does not parse as bash (%v): %s", i, err, out)
			}
		}
	} else {
		t.Log("bash is not on PATH: the syntax half of this test did not run")
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
	// ... and the re-record is atomic like the recorded state's own block: it
	// writes the new ref to a temp file and moves it over the record only once
	// the read came back, because `> "$S/base-image"` truncates the only
	// record of the pre-move ref before doctor's output is known.
	for _, want := range []string{"check38-base-image.XXXXXX", `mv "$T" "$S/base-image"`} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's re-record does not say %q", want)
		}
	}
	if strings.Contains(section, `> "$S/base-image"`) {
		t.Error("the Check 38 section's re-record still writes the base-image record in place, truncating the pre-move ref")
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

	// Step 0 must not send the owner to the recorded-state block: step 0 is
	// printed after the Setup paragraph, whose (b) has already set
	// `checkout: clone` and merged it, so a record taken there would record
	// `clone` — and the Restore's step 1 would write `clone` back. The block
	// cannot catch it either: `[ ! -e "$S" ]` refuses a leftover record, not a
	// record that is simply late. It points at (a) instead.
	for _, want := range []string{"The recorded state is **not** taken here", "there is no record to be left over"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's step 0 does not say %q", want)
		}
	}
	if strings.Contains(section, "run the recorded-state block too, in the same shell") {
		t.Error("the Check 38 section's step 0 still tells the owner to take the record after the first change")
	}

	// ... and the one comment this PR added to another check names the right
	// item: the two apostrophe-bearing ${VAR:?...} messages were in Check 43's
	// 43e block (the digest pull and the digest assignment), not in 43c's, and
	// a comment that sends a reader to a block which never had the defect is
	// the same wrong reference the step-5 assertions above ban.
	if strings.Contains(doc, "The 43c block below carried one") {
		t.Error("the Check 43 guard comment points at 43c for a defect that was in 43e")
	}
	if !strings.Contains(doc, "The 43e block below carried one") {
		t.Error("the Check 43 guard comment does not name 43e, where the apostrophes were")
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
	// it branches on the recorded state, and deletes the record last. A later
	// edit that silently dropped any of these would leave a half-restored
	// sandbox with nothing to notice it.
	for _, want := range []string{"$S/checkout", "$S/checkout-source", "`rm -rf \"$S\"`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's Restore does not say %q", want)
		}
	}

	// ... every source the recording block can record, not only the two the
	// Restore used to branch on. SourceOf returns "repo", "default", "project"
	// and "profile <name>" (internal/config/resolve.go's SourceOf/SourceProfile),
	// and a `checkout:` the project layer or a profile sets is not in the
	// sandbox's own file at all — the case the old two-branch Restore read as
	// "default" and then dropped a key that was never there.
	for _, want := range []string{
		"says `repo`", "anything else — `default`, `project` or `profile <name>`",
		"not in the sandbox's fugaro.yaml", "Never edit the project layer",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's Restore does not say %q", want)
		}
	}
	// The recording block refuses a source it does not handle, so an unhandled
	// one stops the check at the record rather than at the Restore, when the
	// sandbox is already half-changed.
	for _, want := range []string{`case "$(cat "$T/checkout-source")" in`, "a source this check does not know"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's recorded state does not refuse an unhandled checkout source (%q)", want)
		}
	}

	// The record goes last, anchored: "last" on its own was satisfied by "the
	// last is not a spare" in step 4. The deletion is the Restore's own fifth
	// item, and it comes after the clones the check made are removed.
	if i := strings.Index(section, "**Restore.**"); i < 0 {
		t.Error("the Check 38 section has no Restore paragraph")
	} else {
		restore := section[i:]
		if !strings.Contains(restore, "5. **The record, last.** `rm -rf \"$S\"`") {
			t.Error("the Check 38 Restore does not delete the record last, as its own fifth item")
		}
		if del, clones := strings.Index(restore, "`rm -rf \"$S\"`"), strings.Index(restore, "rm -rf /tmp/check38-bin"); del < 0 || clones < 0 || del < clones {
			t.Error("the Check 38 Restore deletes the record before it has removed the clones it made")
		}
	}

	// Step 4's throwaway commit on the base branch is reverted by the SHA
	// step 4 recorded, on a fresh checkout of origin/$BASE, in a clone whose
	// origin is asserted against the literal sandbox URL first, and the
	// Restore refuses rather than guessing when the record is missing or the
	// commit is not an ancestor. Never a forced push.
	for _, want := range []string{
		"step 1's agent commit",
		"$S/base-commit", "git merge-base --is-ancestor",
		`git revert --no-edit "$(cat "$S/base-commit")"`,
		"origin/${BASE}", "https://bitbucket.org/edgeappinc/fugarosandbox.git", "`--force`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's base-branch revert does not say %q", want)
		}
	}

	// The missing-record refusal is a hard stop and has to read like one: the
	// old text ended "record that and continue" and then exited 1, and the
	// section's own guard paragraph says that exit ends the shell. "Continue"
	// there means carry on with the Restore in a new shell, and says so.
	for _, want := range []string{"this stop ends the shell", "NEW shell"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's missing-record refusal does not say %q", want)
		}
	}
	if strings.Contains(section, "record that and continue") {
		t.Error("the Check 38 section's missing-record refusal still says to continue in the shell it just ended")
	}

	// Step 5's citations name the code that exists: the step is pre-existing,
	// but it sits in the reviewed section and a wrong line number sends the
	// owner to a line that says something else.
	for _, want := range []string{"`CloneBlobless`, `internal/gitops/gitops.go:169`", "`gitTo`, `internal/gitops/gitops.go:231`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's step 5 does not cite %q", want)
		}
	}
	for _, row := range []struct{ banned, why string }{
		{"gitops.go:152", "the old, wrong CloneBlobless line"},
		{"gitops.go:214", "the old, wrong gitTo span"},
	} {
		if strings.Contains(section, row.banned) {
			t.Errorf("the Check 38 section still cites %q (%s)", row.banned, row.why)
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
		{"the earlier change to the same path", "the wrong account of what HEAD~1 is (step 1's agent commit, not a change to check38-note.txt)"},
	} {
		if strings.Contains(section, row.banned) {
			t.Errorf("the Check 38 section still carries %q (%s)", row.banned, row.why)
		}
	}
}
