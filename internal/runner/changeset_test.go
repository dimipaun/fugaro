package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// siblingRun is the other member of every fixture's change set: the run of
// another repository, launched with this one (D642).
const siblingRun = "20260926-221531-ef01"

// changeSetFor is the block a member's task carries: the set and both of its
// members, the repository of the run reading it included, with memberRun as
// this member's own run — which is also the set's ID, since the first member
// names the set.
func changeSetFor(memberRun string) *task.ChangeSet {
	return &task.ChangeSet{ID: memberRun, Members: []task.ChangeSetMember{
		{Repo: "acme/app", RunID: memberRun},
		{Repo: "acme/other", RunID: siblingRun},
	}}
}

// memberSpec is the task a launch writes for one member of a change set: this
// run, its repository, and the whole set.
func memberSpec(cs *task.ChangeSet) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app",
		Task: "Add a feature", ChangeSet: cs}
}

// TestChangeSetRunStaysDraft is D2's red step: a member run whose tests pass and
// whose senior review shipped — a run this repository publishes ready on its own
// — leaves its pull request a draft, because the set's pull requests go ready
// together and what flips them is the set's own step (D614, D3). The reviewers
// and labels follow the same fact: they are requested for a pull request that
// goes ready, and this one does not.
func TestChangeSetRunStaysDraft(t *testing.T) {
	h := newHarness(t, prCfg(t, 2, ""), memberSpec(changeSetFor(runID)))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Fatalf("the member's pull request was made ready: %+v", pr)
	}
	if len(pr.Reviewers) != 0 || len(pr.Labels) != 0 {
		t.Fatalf("a held pull request was given reviewers %v and labels %v", pr.Reviewers, pr.Labels)
	}
	if !strings.Contains(pr.Body, "**Draft**") || !strings.Contains(pr.Body, "go ready together") {
		t.Fatalf("the status section does not say why it is a draft:\n%s", pr.Body)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %s/%s, want %s/%s: %+v", rec.Status, rec.Outcome, runstore.StatusSucceeded, runstore.OutcomeDraft, rec)
	}
	if !strings.Contains(rec.Reason, "go ready together") || rec.FailureClass != "" {
		t.Fatalf("record = %+v", rec)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Status != rec.Status || stored.Outcome != rec.Outcome || stored.Reason != rec.Reason {
		t.Fatalf("stored record = %+v, %v", stored, err)
	}
}

// TestChangeSetRunRecordsVerdict: the member records its own verdict beside its
// pull request, under its own prefix, because that is the record the step that
// settles the whole set reads from it (D2, D3) — and it is the run's answer, not
// its pull request's state: the run that shipped is a draft, and it still
// records `ship`. The verdict carries the commit it is about and whether a
// clean-tree test passed there, because the record outlives the run that wrote
// it and the run that settles the set cannot see the writer's verify table: the
// commit is the one the run pushed, and it was verified — which is what the
// shipped ending means. A member that ended without shipping records the empty
// verdict over whatever stood there, so that step never settles a pull request
// on a word no run still stands by.
func TestChangeSetRunRecordsVerdict(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, "", memberSpec(changeSetFor(runID)))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	m, err := h.store.ReadChangeSetMember(ctx)
	if err != nil {
		t.Fatalf("the member's record: %v", err)
	}
	if m.Version != 1 || m.SetID != runID || m.RunID != runID || m.Repo != "acme/app" {
		t.Fatalf("record = %+v", m)
	}
	if m.Verdict != runstore.VerdictShip {
		t.Fatalf("a run whose review shipped recorded the verdict %q, want %q: %+v", m.Verdict, runstore.VerdictShip, m)
	}
	if m.HeadSHA != rec.PushedHead || !m.Verified {
		t.Fatalf("the verdict was recorded with head %q verified %v, want the commit it pushed, cleaned and tested: %+v",
			m.HeadSHA, m.Verified, m)
	}
	if rec.PR == nil || m.PRURL != rec.PR.URL || m.At.IsZero() {
		t.Fatalf("record = %+v, run record = %+v", m, rec.PR)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("the holding run = %+v", rec)
	}

	// A member that did not ship records the empty verdict over whatever stood
	// there: the set must never settle a pull request on a word no run still
	// stands by. Its commit goes with the empty word — the commit it pushed,
	// which nothing verified, so the set's step cannot settle on it either.
	h = newHarness(t, "", memberSpec(changeSetFor(runID)))
	rec, err = h.run(t, commitOnly("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status == runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v, want a run that did not ship", rec)
	}
	m, err = h.store.ReadChangeSetMember(ctx)
	if err != nil {
		t.Fatalf("the member's record: %v", err)
	}
	if m.Verdict != "" || m.PRURL == "" {
		t.Fatalf("a run that did not ship recorded %+v", m)
	}
	if m.HeadSHA != rec.PushedHead || m.Verified {
		t.Fatalf("a run that did not ship recorded head %q verified %v, want the commit it pushed, unverified: %+v",
			m.HeadSHA, m.Verified, m)
	}
}

// TestChangeSetFollowUpKeepsTheHold: a follow-up on a member's pull request
// carries the change set its previous run was launched in, so the hold binds it
// too — otherwise `fugaro run --pr N` would be the way to flip ready the very
// pull request its set keeps a draft (F15). What it writes is the member's
// record, under the member run's prefix: the set's membership was fixed at its
// launch (D642), and this run answers on that member's pull request rather than
// making itself a member of anything. The commit it records is the one the
// FOLLOW-UP pushed, with its own test on it: the record is what the settle step
// compares the member's pull request against, and the member run's own commit
// is not what the pull request is at any more (D3).
func TestChangeSetFollowUpKeepsTheHold(t *testing.T) {
	ctx := context.Background()
	h := followUpHarness(t, "", nil, implement("feature"), review("ship", 0))
	if h.state(t).PRs[0].Draft {
		t.Fatal("the first run's pull request is a draft")
	}
	h.followUp(t, followID, runID, "Tidy up.")
	// What the launcher carries into a follow-up's task (D2): the previous
	// run's own block, members and all.
	spec, err := h.store.ReadTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spec.ChangeSet = changeSetFor(runID)
	if err := h.store.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("tidy"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %s/%s: %+v", rec.Status, rec.Outcome, rec)
	}
	st := h.state(t)
	if !st.PRs[0].Draft {
		t.Fatalf("the follow-up flipped the member's pull request ready: %+v", st.PRs[0])
	}
	report := st.PRs[0].Comments[len(st.PRs[0].Comments)-1]
	if !strings.Contains(report, "was ready; moved back to draft because "+rec.Reason) {
		t.Fatalf("the report does not say why it went back to draft:\n%s", report)
	}
	// The member's record, not the follow-up's: the run the set names.
	m, err := runstore.Open(h.bucket, repoSlug, runID).ReadChangeSetMember(ctx)
	if err != nil {
		t.Fatalf("the member's record: %v", err)
	}
	if m.RunID != runID || m.SetID != runID || m.Verdict != runstore.VerdictShip || m.PRURL == "" {
		t.Fatalf("the follow-up recorded %+v", m)
	}
	if rec.PushedHead == h.first.PushedHead {
		t.Fatal("the follow-up pushed nothing, so this test would not tell the two commits apart")
	}
	if m.HeadSHA != rec.PushedHead || !m.Verified {
		t.Fatalf("the follow-up recorded head %q verified %v, want the commit it pushed, tested: %+v",
			m.HeadSHA, m.Verified, m)
	}
	if _, err := h.store.ReadChangeSetMember(ctx); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("the follow-up recorded a member of its own: %v", err)
	}
}

// TestRunWithoutChangeSetFlipsAsToday: a run in no change set is untouched by
// the hold — it goes ready, with its reviewers and labels, and records no member
// record at all (D1's byte-identity requirement, from the other side).
func TestRunWithoutChangeSetFlipsAsToday(t *testing.T) {
	h := newHarness(t, prCfg(t, 2, ""), nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
		t.Fatalf("record = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || len(pr.Reviewers) != 1 || len(pr.Labels) != 1 {
		t.Fatalf("pull request = %+v", pr)
	}
	if _, err := h.store.ReadChangeSetMember(context.Background()); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("a run in no change set recorded a member record: %v", err)
	}
}
