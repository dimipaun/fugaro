// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// These are D3's tests: what the settle step does with a set whose members have
// shipped, and what it refuses to do with one that has not. They drive no
// network and no bucket — the member records are held in memory and each
// member's pull request is the fake host of internal/gitprov/fake — so what is
// under test is the decision: which member holds a set back, what is written
// when none does, and what a settle that failed part way leaves behind.

const (
	settleRunA = "20261116-140000-a1b2"
	settleRunB = "20261116-140001-c3d4"

	settleHeadA = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	settleHeadB = "9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d"
	// settleHeadMoved is a commit no member's tests ever ran on: what a
	// push to the branch after the run ended leaves the pull request at.
	settleHeadMoved = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
)

// settleSet is the set every fixture settles: two members, one run each, the
// first member's run naming the set (task.ChangeSet.ID).
func settleSet() *task.ChangeSet {
	return &task.ChangeSet{ID: settleRunA, Members: []task.ChangeSetMember{
		{Repo: "acme/app", RunID: settleRunA},
		{Repo: "acme/other", RunID: settleRunB},
	}}
}

// settleMember is one member's records as its run left them: the task the
// launcher wrote (which carries the whole set, D642), the changeset.json it
// recorded (D2) and the result.json with the commit its tests ran on. head is
// the commit, verdict the word the run ended on, and both are the fixture's to
// vary — every test below is one of them varied.
func settleMember(repo, runID, head, verdict string) MemberReads {
	cs := settleSet()
	url := "https://github.com/" + repo + "/pull/1"
	return MemberReads{
		Task: &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: "main", Workflow: "app",
			Task: "a change", ChangeSet: cs},
		Member: &runstore.ChangeSetMember{Version: 1, SetID: cs.ID, Repo: repo, RunID: runID,
			PRURL: url, Verdict: verdict, At: time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)},
		Run: &runstore.Record{Version: 1, RunID: runID, Repo: repo, HeadSHA: head,
			PR:     &runstore.PRRef{Number: 1, URL: url},
			Verify: []verify.Record{{N: 1, Kind: verify.KindTest, HeadSHA: head, CleanTree: true, Passed: true}}},
	}
}

// settleFixture is the step under test: the member records of one set, held in
// memory, and one fake host per member repository holding the draft pull
// request that member's run opened.
type settleFixture struct {
	records map[string]MemberReads
	hosts   map[string]*fake.Provider
	// opened is every repository the step opened a provider for, in order.
	opened []string
}

// newSettleFixture is a fixture whose members have both shipped, at the commits
// their pull requests are on. A test varies one member's records or one host's
// state from there.
func newSettleFixture() *settleFixture {
	f := &settleFixture{records: map[string]MemberReads{}, hosts: map[string]*fake.Provider{}}
	f.records[settleRunA] = settleMember("acme/app", settleRunA, settleHeadA, runstore.VerdictShip)
	f.records[settleRunB] = settleMember("acme/other", settleRunB, settleHeadB, runstore.VerdictShip)
	f.host("acme/app", settleHeadA)
	f.host("acme/other", settleHeadB)
	return f
}

// host gives repo a fake host holding the draft pull request its member run
// opened, at head.
func (f *settleFixture) host(repo, head string) *fake.Provider {
	h := &fake.Provider{Repo: repo, SelfID: "1", State: fake.State{PRs: []fake.PRState{{
		PR:    gitprov.PR{Number: 1, URL: "https://github.com/" + repo + "/pull/1", Draft: true},
		Spec:  gitprov.PRSpec{Branch: "topic"},
		Title: "a change",
		Head:  head,
	}}}}
	f.hosts[repo] = h
	return h
}

func (f *settleFixture) settler() *Settler {
	return &Settler{
		Member: func(_ context.Context, _, runID string) (MemberReads, error) {
			reads, ok := f.records[runID]
			if !ok {
				return MemberReads{}, errors.New("the runs bucket holds no such run")
			}
			return reads, nil
		},
		Open: func(_ context.Context, repo string) (SettleProvider, error) {
			f.opened = append(f.opened, repo)
			h, ok := f.hosts[repo]
			if !ok {
				return nil, errors.New("this fixture has no host for that repository")
			}
			return h, nil
		},
	}
}

// pr is one member's pull request as the host has it now.
func (f *settleFixture) pr(t *testing.T, repo string) fake.PRState {
	t.Helper()
	prs := f.hosts[repo].Snapshot().PRs
	if len(prs) != 1 {
		t.Fatalf("%s holds %d pull requests, want 1", repo, len(prs))
	}
	return prs[0]
}

// calls is one member host's write log: the provider calls the step made on it,
// in order.
func (f *settleFixture) calls(repo string) []fake.Call { return f.hosts[repo].Snapshot().Calls }

// row is the report row of one member.
func row(t *testing.T, rep SettleReport, repo string) SettleMemberResult {
	t.Helper()
	for _, r := range rep.Members {
		if r.Repo == repo {
			return r
		}
	}
	t.Fatalf("the report has no row for %s: %+v", repo, rep.Members)
	return SettleMemberResult{}
}

// TestSettleFlipsAllWhenEveryMemberShips is D3's red step: a set whose every
// member recorded `ship` with passing tests on the commit its pull request is
// at goes ready — every pull request, in one settle, through the two calls the
// runner's own ready path makes (UpdatePR with the draft flag false, then
// ApplyReady) — and the report says which members it flipped.
func TestSettleFlipsAllWhenEveryMemberShips(t *testing.T) {
	f := newSettleFixture()
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !rep.Settled || rep.Flipped != 2 || rep.SetID != settleRunA {
		t.Fatalf("report = %+v, want a settled set of two flipped members", rep)
	}
	for _, repo := range []string{"acme/app", "acme/other"} {
		if r := row(t, rep, repo); r.Why != "" || !r.Flipped {
			t.Errorf("%s's row = %+v, want it flipped and nothing holding it", repo, r)
		}
		if pr := f.pr(t, repo); pr.Draft {
			t.Errorf("%s's pull request is still a draft", repo)
		}
		// The two calls, in this order: the flip, then what a ready pull
		// request carries.
		var ops []string
		for _, c := range f.calls(repo) {
			ops = append(ops, c.Op+":"+c.Detail)
		}
		want := []string{"UpdatePR:draft=false", "ApplyReady:reviewers= labels="}
		if !slices.Equal(ops, want) {
			t.Errorf("%s's calls = %v, want %v", repo, ops, want)
		}
	}
}

// TestSettleRefusesAMemberNotListedByAll: the set a settle acts on is the one
// every member agrees about (D642). A member whose task carries another set —
// another id, or the same id with a different membership — holds the whole set
// back: nothing is flipped, no repository is even opened, and the row names the
// member holding it.
func TestSettleRefusesAMemberNotListedByAll(t *testing.T) {
	f := newSettleFixture()
	other := f.records[settleRunB]
	other.Task.ChangeSet = &task.ChangeSet{ID: settleRunA, Members: []task.ChangeSetMember{
		{Repo: "acme/app", RunID: settleRunA},
		{Repo: "acme/elsewhere", RunID: "20261116-140002-e5f6"},
	}}
	f.records[settleRunB] = other
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if rep.Settled || rep.Flipped != 0 {
		t.Fatalf("report = %+v, want the set held back with nothing flipped", rep)
	}
	if r := row(t, rep, "acme/other"); !strings.Contains(r.Why, "lists another change set") {
		t.Errorf("the row of the member whose task lists another set = %+v", r)
	}
	if r := row(t, rep, "acme/app"); r.Why != "" {
		t.Errorf("the member that agrees about the set is reported as held: %+v", r)
	}
	if len(f.opened) != 0 {
		t.Errorf("a set held back opened %v: a settle writes nothing and reads no host until every member ships", f.opened)
	}
	for _, repo := range []string{"acme/app", "acme/other"} {
		if !f.pr(t, repo).Draft {
			t.Errorf("%s's pull request was made ready by a settle that refused the set", repo)
		}
	}
}

// TestSettleRereadsPRStateNotTheRecord: a member's record is its own word about
// itself, and the pull request it names may have moved on since it was written
// (F15). A member that recorded `ship` whose pull request the host says is
// closed is reported and not acted on — the record is not what decides.
func TestSettleRereadsPRStateNotTheRecord(t *testing.T) {
	f := newSettleFixture()
	f.hosts["acme/other"].State.PRs[0].State = gitprov.PRClosed
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if rep.Settled || rep.Flipped != 0 {
		t.Fatalf("report = %+v, want the set held back", rep)
	}
	if r := row(t, rep, "acme/other"); !strings.Contains(r.Why, "its pull request is closed") {
		t.Errorf("the row of the member whose pull request is closed = %+v", r)
	}
	if pr := f.pr(t, "acme/other"); !pr.Draft {
		t.Error("a closed pull request was flipped ready")
	}
	if calls := f.calls("acme/app"); len(calls) != 0 {
		t.Errorf("the set's other member was written to while one member held the set back: %v", calls)
	}
}

// TestSettleRefusesAHeadThatMoved: what makes a member shippable is passing
// tests on the commit its pull request is AT, not on the commit its run ended
// at. A pull request the host says is at another commit is reported, so a
// settle never flips a head nothing tested (F15).
func TestSettleRefusesAHeadThatMoved(t *testing.T) {
	f := newSettleFixture()
	f.hosts["acme/app"].State.PRs[0].Head = settleHeadMoved
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if rep.Settled || rep.Flipped != 0 {
		t.Fatalf("report = %+v, want the set held back", rep)
	}
	if r := row(t, rep, "acme/app"); !strings.Contains(r.Why, "moved on from the commit its tests ran on") {
		t.Errorf("the row of the member whose head moved = %+v", r)
	}
	if calls := f.calls("acme/other"); len(calls) != 0 {
		t.Errorf("the set's other member was written to while one member held the set back: %v", calls)
	}
}

// TestSettleRetryFinishesAPartialFlip: a settle that flipped some of a set's
// pull requests and then met a failure leaves them ready — that is where the set
// is going — and the next settle finishes the rest rather than undoing it. A
// member whose pull request is already ready is ready: it is not flipped again,
// and it does not hold the set back.
func TestSettleRetryFinishesAPartialFlip(t *testing.T) {
	f := newSettleFixture()
	f.hosts["acme/other"].FailUpdatePR = 1
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if rep.Settled || rep.Flipped != 1 {
		t.Fatalf("report = %+v, want one member flipped and the set unsettled", rep)
	}
	if r := row(t, rep, "acme/app"); !r.Flipped || r.Why != "" {
		t.Errorf("the member that flipped = %+v", r)
	}
	if r := row(t, rep, "acme/other"); r.Flipped || !strings.Contains(r.Why, "could not be made ready") {
		t.Errorf("the member whose flip failed = %+v", r)
	}
	if !f.pr(t, "acme/other").Draft {
		t.Error("the member whose flip failed is not a draft")
	}

	rep, err = f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle again: %v", err)
	}
	if !rep.Settled || rep.Flipped != 1 {
		t.Fatalf("the second settle = %+v, want the set finished with the one member that was still a draft flipped", rep)
	}
	if r := row(t, rep, "acme/app"); r.Flipped || r.Why != "" {
		t.Errorf("the member flipped by the first settle = %+v, want it ready and not flipped again", r)
	}
	for _, repo := range []string{"acme/app", "acme/other"} {
		if pr := f.pr(t, repo); pr.Draft {
			t.Errorf("%s's pull request is still a draft after the second settle", repo)
		}
	}
}

// TestSettleRefusesAMemberThatHasNotShipped: the hold is lifted by a member's
// own word, and a member that ended without shipping — whose verdict a later
// attempt at the same pull request cleared (D2) — holds the set back whatever
// its pull request looks like.
func TestSettleRefusesAMemberThatHasNotShipped(t *testing.T) {
	f := newSettleFixture()
	f.records[settleRunB] = settleMember("acme/other", settleRunB, settleHeadB, "")
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if rep.Settled || rep.Flipped != 0 {
		t.Fatalf("report = %+v, want the set held back", rep)
	}
	if r := row(t, rep, "acme/other"); !strings.Contains(r.Why, "has not shipped") {
		t.Errorf("the row of the member that has not shipped = %+v", r)
	}
}

// TestSettleRefusesTestsThatDidNotPass: a member whose commit has no passing
// clean-tree test on it is not shippable, whatever its verdict says: the run's
// word is that nothing must change before its pull request is merged, and a
// commit nothing verified is not a pull request a settle may publish.
func TestSettleRefusesTestsThatDidNotPass(t *testing.T) {
	f := newSettleFixture()
	other := f.records[settleRunB]
	other.Run.Verify = []verify.Record{{N: 1, Kind: verify.KindTest, HeadSHA: settleHeadB, CleanTree: true, Passed: false}}
	f.records[settleRunB] = other
	rep, err := f.settler().Settle(context.Background(), settleSet())
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if rep.Settled || rep.Flipped != 0 {
		t.Fatalf("report = %+v, want the set held back", rep)
	}
	if r := row(t, rep, "acme/other"); !strings.Contains(r.Why, "tests did not pass") {
		t.Errorf("the row of the member whose tests failed = %+v", r)
	}
}

// TestSettleNeedsAChangeSet: the step's own preconditions are errors and never
// rows, and a set with no member is one of them: a set is two to five members
// (task.Validate), so a caller that reached here with none is asking the step to
// settle nothing, which is not a report.
func TestSettleNeedsAChangeSet(t *testing.T) {
	f := newSettleFixture()
	if _, err := f.settler().Settle(context.Background(), nil); err == nil {
		t.Error("Settle with no set did not fail")
	}
	if _, err := f.settler().Settle(context.Background(), &task.ChangeSet{ID: settleRunA}); err == nil {
		t.Error("Settle of a set with no member did not fail")
	}
}
