package runner_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestARunThatAddsAnAdmittedImageIsReady: the rule's whole property, end to
// end, on the run it exists for (P5, spec.md §4). The task is an asset, the
// coder writes the bytes where the project's own node admits them and commits
// them with the rest of its work, the suite passes on that commit, and the
// review ships it — so the run ends the ordinary ending a run whose diff holds
// no binary at all gets: succeeded / ready, the branch carrying the image
// beside the run's own work.
//
// That is the half of the rule nothing may move. The rule decides what a run
// may *commit*, never what it may *ship*: an admitted image is the run's work
// like any other file, and a run is refused for the bytes a project did not
// admit and for nothing else. A later change that read the rule as a readiness
// gate — a run holding any binary ending a draft whatever the list says — fails
// here.
//
// The status section names the image (D993): the pull request is where a reader
// learns an unreadable file is in the diff without opening it, and that line is
// the rule's own answer reaching a person.
//
// Its mirror is TestARunThatLeavesABuildProductIsADraft in runner_test.go: the
// same steps, the same tests, the same review, and the one difference between
// the two runs is whether the project admits the bytes.
func TestARunThatAddsAnAdmittedImageIsReady(t *testing.T) {
	const path = "website/public/images/logo.webp"
	b := policyRun(t, gwConfig(t, ""), "off", "", binariesNode(`"website/public/**"`, "1048576"))
	rec, err := b.run(t, implementCommitting(path, selfCommitImage(512)), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
		t.Fatalf("record = status %s, outcome %s, reason %q; want the ordinary ending: the image is at a path the project admits and within its cap", rec.Status, rec.Outcome, rec.Reason)
	}
	files := branchFiles(t, b.harness, rec.Branch)
	if !slices.Contains(files, path) {
		t.Errorf("the branch does not carry the admitted image the run committed: %v", files)
	}
	if !slices.Contains(files, "feature.txt") {
		t.Errorf("the branch does not carry the run's own work: %v", files)
	}
	pr := onlyPR(t, b.harness.provider)
	if pr.Draft {
		t.Errorf("the pull request is a draft although the run ended ready: %+v", pr)
	}
	if body := pr.Body; !strings.Contains(body, "binaries: 1 admitted ("+path+")") {
		t.Errorf("the status section does not name the admitted image:\n%s", body)
	}
}
