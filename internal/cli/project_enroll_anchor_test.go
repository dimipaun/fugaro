package cli

// U9a's tests: the trust anchor — the checkout's own gcp_project line — is this
// command's own step from here (docs/update-from-anywhere, R8/F38).
//
// What the line is for: every teammate's CLI and every CI job that runs fugaro
// against the repository reads the installation's name out of it, so the file
// has to hold it before the enrolment is of any use to anybody but the operator.
// It is a working-tree edit the operator reviews and commits, and these three
// tests are the three ways it can go wrong: the line said nowhere (the operator
// is told to add nothing), the write made outside the gate (a second question,
// or a file written without an answer behind it), and the write made in a
// session that must not make it.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// TestReposAddPrintsTheAnchorLine is the line and the checks around it: the
// plan's first step says which line goes into the checkout, the checks on the
// job images `fugaro update --anchor` runs are printed before the one question,
// the fixes they name are this command's own, and the line then lands in the
// checkout — the run is the owner's own, so the gate writes it.
func TestReposAddPrintsTheAnchorLine(t *testing.T) {
	r := enrolRig(t)
	file := enrolCheckoutFile(t)
	if strings.Contains(readFileString(t, file), "gcp_project") {
		t.Fatalf("the fixture's checkout already carries the anchor:\n%s", readFileString(t, file))
	}
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	conf := strings.Index(out, "⚠ CONFIRM")
	if conf < 0 {
		t.Fatalf("the one confirmation was not asked:\n%s", out)
	}
	before := out[:conf]
	if line := planStep(t, before, 1); !strings.Contains(line, "gcp_project: proj-1234") {
		t.Errorf("the plan's first step names no line to add: %q", line)
	}
	// The checks are the ones `--anchor` runs, and they say what merging the
	// line before the images are ready would cost. They are read before the one
	// question, because that question is the operator answering for the file.
	if !strings.Contains(before, "Warning: the job image of "+enrolRepo) {
		t.Errorf("the checks on the job images are not printed before the question:\n%s", before)
	}
	// And their last fix is this command, in this checkout: an operator who read
	// `fugaro update --anchor` here would be sent to the door this line replaces.
	if !strings.Contains(before, "fugaro project repos add "+enrolRepo) {
		t.Errorf("the checks do not name this command as the door that writes the line:\n%s", before)
	}
	if strings.Contains(before, "update --anchor") {
		t.Errorf("the checks still name the flag this command replaces:\n%s", before)
	}
	if got := readFileString(t, file); !strings.Contains(got, "gcp_project: proj-1234") {
		t.Errorf("the checkout does not carry the line:\n%s", got)
	}
	if !strings.Contains(out, "Updated "+file+" with gcp_project") {
		t.Errorf("the run does not say it wrote the file:\n%s", out)
	}
}

// TestReposAddOffersTheAnchorThroughTheGate: the write goes through the one gate
// every door offering the line shares, with THIS door's facts — and the fact
// that matters is the one the gate does not have an arm for: a person at a
// terminal who has already answered the whole enrolment. So exactly one question
// is asked, the update door's arms leave no mark on this run (no "confirmed by
// this run's own answer", no "(plan only: fugaro.yaml is not written)"), and the
// file is written.
func TestReposAddOffersTheAnchorThroughTheGate(t *testing.T) {
	r := enrolRig(t)
	file := enrolCheckoutFile(t)
	ownerAtTheTerminal(t)
	out, errOut, err := r.enrolOwnerAt(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if n := strings.Count(out, "⚠ CONFIRM"); n != 1 {
		t.Errorf("the run asked %d confirmations, want the enrolment's own one:\n%s", n, out)
	}
	for _, arm := range []string{"confirmed by this run's own answer", "(plan only: fugaro.yaml is not written)", initflow.OwnerProceeded()} {
		if strings.Contains(out, arm) {
			t.Errorf("the write took the update door's arm %q:\n%s", arm, out)
		}
	}
	if got := readFileString(t, file); !strings.Contains(got, "gcp_project: proj-1234") {
		t.Errorf("the gate did not write the checkout's line:\n%s", got)
	}
}

// TestReposAddAnchorRefusedForAnAgent is SD8 for this write: `repos add` is a
// person's step — it writes the published node, this machine's config, the job
// set and an IAM grant — and the anchor is a write into a checkout on top of
// that. An agent's session is refused before anything is read, so the checkout
// the agent ran in is untouched and no line was printed for it to paste.
func TestReposAddAnchorRefusedForAnAgent(t *testing.T) {
	r := enrolRig(t)
	file := enrolCheckoutFile(t)
	before := readFileString(t, file)
	ownerAtTheTerminal(t)
	t.Setenv(agentMarkers[0], "1")
	out, _, err := r.enrolOwnerAt(t, enrolRepo)
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("in an agent session: err = %v, want the SD8 refusal\nstdout:\n%s", err, out)
	}
	if got := readFileString(t, file); got != before {
		t.Errorf("the refused run wrote the checkout:\n%s", got)
	}
	for _, forbidden := range []string{"⚠ CONFIRM", "gcp_project: proj-1234"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the refused run printed %q:\n%s", forbidden, out)
		}
	}
}
