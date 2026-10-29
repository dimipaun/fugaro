// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

// The sweep's bounds and its tolerance of what other writers put in front of
// it: the security review of P5's pull request found a tick that read every
// run a repository ever had and every pull request those runs named, always
// from the same first repository, with one bad number able to keep a whole
// repository stale and one bad record able to stop the tick. Each test below
// names the change it fails without.

// TestOnePoisonedPullRequestDoesNotStaleTheRepository: the numbers a view
// reads are written by the runs, from inside their containers, so a run can
// name a pull request the host refuses. That number is recorded on its own
// and the others are read, rather than the whole repository being left stale
// for as long as the run's record stands.
//
// Fails without the per-number handling in recompute (a *PartialError read as
// a whole-repository failure).
func TestOnePoisonedPullRequestDoesNotStaleTheRepository(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	slug := testRepo().Slug
	src.runs = []Run{
		{ID: "20261024-100000-aaaa", PR: 7, Files: []string{"a.go"}},
		{ID: "20261024-100500-bbbb", PR: 8, Files: []string{"b.go"}},
		{ID: "20261024-101000-cccc", PR: 999999, Files: []string{"c.go"}},
	}
	src.prs = []PullRequest{{Number: 7, State: StateOpen}, {Number: 8, State: StateOpen}}
	src.fail = map[int]bool{999999: true}

	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	row := res.Repos[0]
	if row.Stale {
		t.Fatalf("one refused number left the repository stale: %+v", row)
	}
	if _, found, _ := s.Store.Read(ctx, PathStale(slug), nil); found {
		t.Errorf("one refused number wrote the repository's stale marker")
	}
	var v View
	if _, found, err := s.Store.Read(ctx, PathView(slug), &v); err != nil || !found {
		t.Fatalf("no view written: found=%v err=%v", found, err)
	}
	if want := []Unread{{Number: 999999, Reason: UnreadFailed, Failures: 1}}; !slices.Equal(v.Unread, want) {
		t.Errorf("unread = %+v, want %+v", v.Unread, want)
	}
	states := map[int]string{}
	for _, r := range v.Records {
		if r.PR != nil {
			states[r.PR.Number] = r.PR.State
		}
	}
	if states[7] != StateOpen || states[8] != StateOpen {
		t.Errorf("the pull requests that were read are not in the view: %v", states)
	}

	// A pull request the host answered for once and refuses now keeps the
	// state that sweep read, and the view says it is carried.
	src.fail = map[int]bool{8: true, 999999: true}
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	v = View{}
	if _, _, err := s.Store.Read(ctx, PathView(slug), &v); err != nil {
		t.Fatal(err)
	}
	want := []Unread{{Number: 8, Reason: UnreadFailed, Carried: true, Failures: 1}, {Number: 999999, Reason: UnreadFailed, Failures: 2}}
	if !slices.Equal(v.Unread, want) {
		t.Errorf("unread = %+v, want %+v", v.Unread, want)
	}
	for _, r := range v.Records {
		if r.PR != nil && r.PR.Number == 8 && r.PR.State != StateOpen {
			t.Errorf("pull request 8 lost the state the last sweep read: %+v", r.PR)
		}
	}
}

// TestAPoisonedNumberBacksOff: a number the host keeps refusing is a number a
// run wrote and no pull request has, and it is never "seen", so without a
// memory of its failures it would be asked for on every tick — ahead of the
// rest, because a number no sweep has read sorts first — and spend the
// repository's share of the read budget forever. After MaxReadFailures
// consecutive failures it is asked for once per ReadRetryAfter instead, and a
// read that succeeds clears it.
//
// Fails without the failure count and the retry-after in recompute.
func TestAPoisonedNumberBacksOff(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	slug := testRepo().Slug
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	src.runs = []Run{
		{ID: "20261024-100000-aaaa", PR: 7, Files: []string{"a.go"}},
		{ID: "20261024-101000-cccc", PR: 999999, Files: []string{"c.go"}},
	}
	src.prs = []PullRequest{{Number: 7, State: StateOpen}}
	src.fail = map[int]bool{999999: true}

	tick := func() (RepoResult, Unread) {
		t.Helper()
		src.asked = nil
		res, err := s.Sweep(ctx, []Repo{testRepo()})
		if err != nil {
			t.Fatal(err)
		}
		var v View
		if _, _, err := s.Store.Read(ctx, PathView(slug), &v); err != nil {
			t.Fatal(err)
		}
		for _, u := range v.Unread {
			if u.Number == 999999 {
				return res.Repos[0], u
			}
		}
		return res.Repos[0], Unread{}
	}

	for i := 1; i <= MaxReadFailures; i++ {
		now = now.Add(15 * time.Minute)
		row, u := tick()
		if !slices.Contains(src.asked, 999999) || row.Read != 2 {
			t.Fatalf("tick %d asked for %v (read %d), want the poisoned number asked while it has failed fewer than %d times", i, src.asked, row.Read, MaxReadFailures)
		}
		if u.Reason != UnreadFailed || u.Failures != i {
			t.Errorf("tick %d: unread = %+v, want %q with %d failures", i, u, UnreadFailed, i)
		}
		wantRetry := time.Time{}
		if i == MaxReadFailures {
			wantRetry = now.Add(ReadRetryAfter)
		}
		if !u.RetryAfter.Equal(wantRetry) {
			t.Errorf("tick %d: retry after %s, want %s", i, u.RetryAfter, wantRetry)
		}
	}
	retry := now.Add(ReadRetryAfter)

	for i := range 4 {
		now = now.Add(15 * time.Minute)
		row, u := tick()
		if slices.Contains(src.asked, 999999) || row.Read != 1 {
			t.Errorf("backed-off tick %d asked for %v (read %d), want only [7]: a poisoned number spends no budget until its retry", i, src.asked, row.Read)
		}
		if u.Reason != UnreadFailed || u.Failures != MaxReadFailures || !u.RetryAfter.Equal(retry) {
			t.Errorf("backed-off tick %d: unread = %+v, want it still recorded with its retry at %s", i, u, retry)
		}
	}

	now = retry
	if row, u := tick(); !slices.Contains(src.asked, 999999) || row.Read != 2 {
		t.Errorf("at the retry the tick asked for %v, want the poisoned number asked once more", src.asked)
	} else if u.Failures != MaxReadFailures || !u.RetryAfter.Equal(now.Add(ReadRetryAfter)) {
		t.Errorf("a retry that failed again = %+v, want %d failures and the next retry a day on", u, MaxReadFailures)
	}

	now = now.Add(ReadRetryAfter)
	src.fail = nil
	src.prs = append(src.prs, PullRequest{Number: 999999, State: StateOpen})
	if row, u := tick(); !slices.Contains(src.asked, 999999) || row.Unread != 0 || u != (Unread{}) {
		t.Errorf("a retry the host answered: asked %v, unread %d (%+v), want it read and no longer unread", src.asked, row.Unread, u)
	}
	now = now.Add(15 * time.Minute)
	if _, u := tick(); !slices.Contains(src.asked, 999999) || u != (Unread{}) {
		t.Errorf("after recovering the number was asked %v and unread %+v, want it read like any other", src.asked, u)
	}
}

// TestAFarRetryAfterIsDue: the view is a document in a database, so a retry
// stamped further off than one back-off is not believed — it would be a number
// the sweep never asks for again — and the number is asked for now.
//
// Fails without the clamp in previous.
func TestAFarRetryAfterIsDue(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs = []Run{{ID: "20261024-101000-cccc", PR: 999999}}
	src.fail = map[int]bool{999999: true}
	far := s.Now().Add(365 * ReadRetryAfter)
	if err := s.Store.Write(ctx, PathView(testRepo().Slug), View{
		Repo:    testRepo().Name,
		Records: []Record{{PR: &PullRequest{Number: 999999}, Runs: []Run{}, Files: []string{}, Services: []string{}}},
		Unread:  []Unread{{Number: 999999, Reason: UnreadFailed, Failures: 99, RetryAfter: far}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(src.asked, []int{999999}) {
		t.Errorf("asked for %v, want the number with a retry a year off asked now", src.asked)
	}
	var v View
	if _, _, err := s.Store.Read(ctx, PathView(testRepo().Slug), &v); err != nil {
		t.Fatal(err)
	}
	want := []Unread{{Number: 999999, Reason: UnreadFailed, Failures: MaxReadFailures, RetryAfter: s.Now().Add(ReadRetryAfter)}}
	if len(v.Unread) != 1 || v.Unread[0].Failures != want[0].Failures || !v.Unread[0].RetryAfter.Equal(want[0].RetryAfter) {
		t.Errorf("unread = %+v, want %+v", v.Unread, want)
	}
}

// TestTheSweepReadsTheRunsOfTheWindowOnly: a tick reads the runs of the last
// RunsWindow and not everything a repository ever ran.
//
// Fails with the window gone (a zero since).
func TestTheSweepReadsTheRunsOfTheWindowOnly(t *testing.T) {
	s, _, src := testSweeper(t)
	src.runs, src.prs = testWork()
	if _, err := s.Sweep(testCtx(t), []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	if want := s.Now().Add(-RunsWindow); !src.since.Equal(want) {
		t.Errorf("the runs were read since %s, want %s (RunsWindow before the sweep's clock)", src.since, want)
	}
	if RunsWindow > 31*24*time.Hour {
		t.Errorf("RunsWindow = %s, want about 30 days", RunsWindow)
	}
}

// TestTheTickReadsAtMostItsBudget: the pull requests one tick reads are
// bounded, the newest first, and the ones left are recorded as deferred with
// the state the last sweep read. A repository the budget did not reach is not
// swept at all, and the next tick starts with it.
//
// Fails without the budget cut in recompute or the stop in Sweep.
func TestTheTickReadsAtMostItsBudget(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	s.Budget = 3
	for n := 1; n <= 5; n++ {
		src.runs = append(src.runs, Run{ID: fmt.Sprintf("20261024-10000%d-aaaa", n), PR: n})
		src.prs = append(src.prs, PullRequest{Number: n, State: StateOpen})
	}
	other := Repo{Provider: "github", Name: "acme/other", Slug: "acme-other"}

	res, err := s.Sweep(ctx, []Repo{testRepo(), other})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(src.asked, []int{3, 4, 5}) {
		t.Errorf("the tick asked for %v, want the newest three [3 4 5]", src.asked)
	}
	if len(res.Repos) != 1 || res.Stopped != StoppedBudget || res.Next != other.Slug {
		t.Errorf("result = %+v, want one repository swept, stopped on the budget, next %s", res, other.Slug)
	}
	var v View
	if _, _, err := s.Store.Read(ctx, PathView(testRepo().Slug), &v); err != nil {
		t.Fatal(err)
	}
	if want := []Unread{{Number: 1, Reason: UnreadDeferred}, {Number: 2, Reason: UnreadDeferred}}; !slices.Equal(v.Unread, want) {
		t.Errorf("unread = %+v, want %+v", v.Unread, want)
	}

	src.order = nil
	if _, err := s.Sweep(ctx, []Repo{testRepo(), other}); err != nil {
		t.Fatal(err)
	}
	if len(src.order) == 0 || src.order[0] != other.Slug {
		t.Errorf("the next tick swept %v, want it to start with %s", src.order, other.Slug)
	}
}

// TestAMergedPullRequestIsNotReadAgain: merged is final on the host, so the
// state the last sweep read is carried and the rate limit is not spent on it.
func TestAMergedPullRequestIsNotReadAgain(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs = []Run{{ID: "20261024-100000-aaaa", PR: 7, Terminal: true}, {ID: "20261024-100500-bbbb", PR: 8}}
	src.prs = []PullRequest{{Number: 7, State: StateMerged}, {Number: 8, State: StateOpen}}
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	_, etag1, _ := viewOf(t, s, ctx, testRepo().Slug)
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(src.asked, []int{8}) {
		t.Errorf("the second tick asked for %v, want [8]: 7 is merged", src.asked)
	}
	if _, etag2, _ := viewOf(t, s, ctx, testRepo().Slug); etag1 != etag2 {
		t.Errorf("carrying the merged state moved the view's generation")
	}
}

// TestTheCursorRotatesTheStartingRepository: every tick that reaches every
// repository moves the start one on, so the same repository is not always
// the first one a short tick reaches.
//
// Fails without the cursor (every tick starts with the first repository).
func TestTheCursorRotatesTheStartingRepository(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	repos := []Repo{
		{Provider: "github", Name: "acme/a", Slug: "acme-a"},
		{Provider: "github", Name: "acme/b", Slug: "acme-b"},
		{Provider: "github", Name: "acme/c", Slug: "acme-c"},
	}
	for _, want := range [][]string{{"acme-a", "acme-b", "acme-c"}, {"acme-b", "acme-c", "acme-a"}, {"acme-c", "acme-a", "acme-b"}, {"acme-a", "acme-b", "acme-c"}} {
		src.order = nil
		res, err := s.Sweep(ctx, repos)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(src.order, want) {
			t.Fatalf("the tick swept %v, want %v", src.order, want)
		}
		if res.Next != want[1] {
			t.Errorf("next = %q, want %q", res.Next, want[1])
		}
	}
	// A cursor that names no repository of the list, or does not decode,
	// starts from the first and does not stop the tick.
	for _, bad := range []any{map[string]any{"next": "acme-gone"}, "not a cursor"} {
		f.Set(PathSweepCursor, bad)
		src.order = nil
		if _, err := s.Sweep(ctx, repos); err != nil {
			t.Fatalf("cursor %v: %v", bad, err)
		}
		if src.order[0] != "acme-a" {
			t.Errorf("cursor %v: the tick started with %s, want acme-a", bad, src.order[0])
		}
	}
}

// TestADeadlineStillWritesTheStaleMarker: the tick that runs out of time is
// the one that must say so, and its context is already done when it does.
// The repository it ran out on is marked, the ones after it are not swept,
// and the next tick starts after it.
//
// Fails when the marker and the cursor are written under the tick's own
// context.
func TestADeadlineStillWritesTheStaleMarker(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx, cancel := context.WithCancel(testCtx(t))
	defer cancel()
	src.runs, src.prs = testWork()
	src.onRuns = cancel
	other := Repo{Provider: "github", Name: "acme/other", Slug: "acme-other"}
	third := Repo{Provider: "github", Name: "acme/third", Slug: "acme-third"}

	res, err := s.Sweep(ctx, []Repo{testRepo(), other, third})
	if err != nil {
		t.Fatalf("a tick that ran out of time failed: %v", err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Reason != StaleDeadline || res.Stopped != StoppedDeadline || res.Next != other.Slug {
		t.Fatalf("result = %+v, want the first repository marked %q and the next tick starting with %s", res, StaleDeadline, other.Slug)
	}
	bg := testCtx(t)
	var mark Stale
	if _, found, err := s.Store.Read(bg, PathStale(testRepo().Slug), &mark); err != nil || !found || mark.Reason != StaleDeadline {
		t.Errorf("the marker = %+v found=%v err=%v, want %q", mark, found, err, StaleDeadline)
	}
	var cur SweepCursor
	if _, _, err := s.Store.Read(bg, PathSweepCursor, &cur); err != nil || cur.Next != other.Slug {
		t.Errorf("the cursor = %+v err=%v, want %s", cur, err, other.Slug)
	}
}

// TestTheViewIsBounded: the records in flight are kept before the finished
// ones and the newest before the oldest, and the document says how many it
// left out.
func TestTheViewIsBounded(t *testing.T) {
	var runs []Run
	for n := 1; n <= 6; n++ {
		runs = append(runs, Run{ID: fmt.Sprintf("20261024-10000%d-aaaa", n), PR: n, Terminal: n != 1, Files: []string{"same.go"}})
	}
	v := bound(Join("acme/webapp", nil, runs), 3, 0)
	if v.Omitted != 3 || len(v.Records) != 3 {
		t.Fatalf("records = %d, omitted = %d, want 3 and 3", len(v.Records), v.Omitted)
	}
	var kept []int
	for _, r := range v.Records {
		kept = append(kept, r.PR.Number)
	}
	if !slices.Equal(kept, []int{1, 5, 6}) {
		t.Errorf("kept %v, want the one in flight (1) and the two newest finished (5, 6)", kept)
	}

	var busy []Run
	for n := 1; n <= 4; n++ {
		busy = append(busy, Run{ID: fmt.Sprintf("20261024-10000%d-aaaa", n), PR: n, Files: []string{"same.go"}})
	}
	c := bound(Join("acme/webapp", nil, busy), 10, 2)
	if len(c.Collisions) != 2 || c.OmittedCollisions != 4 {
		t.Errorf("collisions = %d, omitted = %d, want 2 and 4 (six pairs)", len(c.Collisions), c.OmittedCollisions)
	}
	if MaxViewRecords <= 0 || MaxViewCollisions <= 0 {
		t.Errorf("the view's bounds are not set")
	}
}

// TestIntentsArePrunedByAge: a record older than MaxAge goes whatever the
// count.
//
// Fails with the age bound gone (only the count prunes).
func TestIntentsArePrunedByAge(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug
	old := Intent{DeclaredBy: "old@example.com", At: s.Now().Add(-DefaultIntentMaxAge - time.Hour), Branch: "main"}
	fresh := Intent{DeclaredBy: "fresh@example.com", At: s.Now().Add(-time.Hour), Branch: "main"}
	for _, in := range []Intent{old, fresh} {
		if err := s.Store.Write(ctx, PathIntent(slug, IntentID(in)), in); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Repos[0].Pruned != 1 {
		t.Errorf("pruned %d, want the one past the age bound", res.Repos[0].Pruned)
	}
	if _, found, _ := s.Store.Read(ctx, PathIntent(slug, IntentID(old)), nil); found {
		t.Errorf("the record past the age bound was kept")
	}
	if _, found, _ := s.Store.Read(ctx, PathIntent(slug, IntentID(fresh)), nil); !found {
		t.Errorf("the fresh record was pruned")
	}
}

// TestAFutureDatedIntentIsNotTheNewest: At is the reporter's clock, and a
// record dated ahead of the sweep's is ordered as the oldest, so it cannot
// outlive the count bound by lying about its age.
//
// Fails when a future At is trusted for the ordering.
func TestAFutureDatedIntentIsNotTheNewest(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug
	s.Keep = 2
	future := Intent{DeclaredBy: "ahead@example.com", At: s.Now().Add(365 * 24 * time.Hour), Branch: "main"}
	a := Intent{DeclaredBy: "a@example.com", At: s.Now().Add(-2 * time.Hour), Branch: "main"}
	b := Intent{DeclaredBy: "b@example.com", At: s.Now().Add(-time.Hour), Branch: "main"}
	for _, in := range []Intent{future, a, b} {
		if err := s.Store.Write(ctx, PathIntent(slug, IntentID(in)), in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Store.Read(ctx, PathIntent(slug, IntentID(future)), nil); found {
		t.Errorf("the record dated a year ahead was kept as the newest")
	}
	for _, in := range []Intent{a, b} {
		if _, found, _ := s.Store.Read(ctx, PathIntent(slug, IntentID(in)), nil); !found {
			t.Errorf("%s's record was pruned in place of the future-dated one", in.DeclaredBy)
		}
	}
}

// TestAnUndecodableRecordDoesNotStopTheTick: the intents node is written by
// every reporter and the stale marker by an earlier sweep, so one document
// that does not decode is counted (and an intent record removed), and the
// tick goes on.
//
// Fails when prune or markStale decode the node in one piece.
func TestAnUndecodableRecordDoesNotStopTheTick(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug
	s.Keep = 1
	f.Set(PathIntent(slug, "garbage"), map[string]any{"at": 7})
	for i := range 2 {
		in := Intent{DeclaredBy: fmt.Sprintf("someone-%d@example.com", i), At: s.Now().Add(time.Duration(i-3) * time.Hour), Branch: "main"}
		if err := s.Store.Write(ctx, PathIntent(slug, IntentID(in)), in); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatalf("an undecodable intent record stopped the tick: %v", err)
	}
	if res.Repos[0].Undecodable != 1 || res.Repos[0].Pruned != 2 {
		t.Errorf("result = %+v, want one undecodable record counted and pruned beside the one past the count", res.Repos[0])
	}

	f.Set(PathStale(slug), "not a marker")
	src.runErr = fmt.Errorf("the runs bucket refused")
	res, err = s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatalf("an undecodable stale marker stopped the tick: %v", err)
	}
	var mark Stale
	if _, found, err := s.Store.Read(ctx, PathStale(slug), &mark); err != nil || !found || mark.Reason != StaleRuns {
		t.Errorf("the marker = %+v found=%v err=%v, want it replaced with %q", mark, found, err, StaleRuns)
	}
	if res.Repos[0].Undecodable != 1 {
		t.Errorf("undecodable = %d, want the marker counted, the intent record being gone since the first sweep", res.Repos[0].Undecodable)
	}
}

// TestAnUndecodableIntentIsPruned: a record that does not decode has no age
// and no place in the order, so neither bound would ever reach it. The sweep
// removes it on its own ETag, at most MaxUndecodablePrunes a repository a
// tick, so a node full of them costs the tick a bounded number of writes and
// the next tick takes the rest.
//
// Fails when an undecodable record is only counted (the node grows for ever).
func TestAnUndecodableIntentIsPruned(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug
	const extra = 3
	for i := range MaxUndecodablePrunes + extra {
		f.Set(PathIntent(slug, fmt.Sprintf("garbage-%03d", i)), map[string]any{"at": i})
	}
	good := Intent{DeclaredBy: "someone@example.com", At: s.Now().Add(-time.Hour), Branch: "main"}
	if err := s.Store.Write(ctx, PathIntent(slug, IntentID(good)), good); err != nil {
		t.Fatal(err)
	}
	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Repos[0]; got.Undecodable != MaxUndecodablePrunes+extra || got.Pruned != MaxUndecodablePrunes {
		t.Errorf("result = %+v, want %d undecodable and %d pruned", got, MaxUndecodablePrunes+extra, MaxUndecodablePrunes)
	}
	res, err = s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Repos[0]; got.Undecodable != extra || got.Pruned != extra {
		t.Errorf("second sweep = %+v, want the remaining %d taken", got, extra)
	}
	var raw map[string]json.RawMessage
	if _, _, err := s.Store.Read(ctx, PathIntents(slug), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Errorf("the node holds %d records, want only the decodable one", len(raw))
	}
	if _, found, _ := s.Store.Read(ctx, PathIntent(slug, IntentID(good)), nil); !found {
		t.Errorf("the decodable record was pruned")
	}
}
