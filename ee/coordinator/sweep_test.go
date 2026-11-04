// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The aggregation job's own logic, as P5 builds it: one repository's view
// recomputed from what GitHub and the runs bucket say, written into P3's
// nodes, and a marker where it could not be recomputed rather than half a
// view. Nothing here contacts Firebase or GitHub: the database is
// internal/gcpfake's and both reads are the test's own.

// testSweeper returns a sweeper over a fake database, the fake itself, and
// the two sources a test drives.
func testSweeper(t *testing.T) (*Sweeper, *gcpfake.RTDB, *fakeSource) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{}
	s := &Sweeper{
		Reader: src,
		Runs:   src,
		Store:  NewStore(c),
		Now:    func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) },
	}
	return s, f, src
}

// fakeSource is the sweep's two inputs as a test drives them: the runs one
// repository's bucket holds, and the pull requests its reader answers with.
type fakeSource struct {
	runs   []Run
	runErr error
	prs    []PullRequest
	prErr  error
	// fail is the numbers the host refuses one by one, which the reader
	// reports as P2's own reader does: a *PartialError carrying the rest.
	fail map[int]bool
	// asked is the numbers the last read asked for, calls how many times the
	// reader was reached, since the window the last runs read asked for, and
	// order the slugs whose runs were read, in order.
	asked []int
	calls int
	since time.Time
	order []string
	// onRuns runs inside each runs read, before it answers.
	onRuns func()
}

func (s *fakeSource) Runs(ctx context.Context, repo Repo, since time.Time) ([]Run, error) {
	s.since = since
	s.order = append(s.order, repo.Slug)
	if s.onRuns != nil {
		s.onRuns()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if s.runErr != nil {
		return nil, s.runErr
	}
	return s.runs, nil
}

func (s *fakeSource) PullRequests(_ context.Context, _, _ string, numbers []int) ([]PullRequest, error) {
	s.calls++
	s.asked = numbers
	if s.prErr != nil {
		return nil, s.prErr
	}
	out := make([]PullRequest, 0, len(numbers))
	var failed []int
	for _, n := range numbers {
		if s.fail[n] {
			failed = append(failed, n)
			continue
		}
		for _, pr := range s.prs {
			if pr.Number == n {
				out = append(out, pr)
			}
		}
	}
	if len(failed) > 0 {
		return nil, &PartialError{Read: out, Failed: failed, err: errors.New("the host answered 404 for a number a run wrote")}
	}
	return out, nil
}

// PullRequest is Reader's single-read half, which the sweep never reaches: a
// tick reads each repository's pull requests in one call (PullRequests). It
// answers from the same pull requests, so the fake keeps one story about the
// host, and refuses a number it does not hold the way the host does rather
// than inventing an empty document.
func (s *fakeSource) PullRequest(_ context.Context, _, _ string, number int) (PRDocument, error) {
	for _, pr := range s.prs {
		if pr.Number == number {
			return PRDocument{Number: pr.Number, URL: pr.URL, Title: pr.Title, State: pr.State, Draft: pr.Draft, Branch: pr.Branch}, nil
		}
	}
	return PRDocument{}, errors.New("the host answered 404 for a pull request the fake does not hold")
}

func testRepo() Repo {
	return Repo{Provider: "github", Name: "acme/webapp", Slug: "acme-webapp"}
}

// testWork is one repository with work in flight: a run on pull request 7,
// touching one file, and the pull request itself.
func testWork() ([]Run, []PullRequest) {
	runs := []Run{{
		ID: "20261024-110000-abcd", PR: 7, Branch: "release/v0.11.0",
		Files: []string{"internal/coordinator/view.go"},
	}}
	prs := []PullRequest{{Number: 7, State: StateOpen, Branch: "release/v0.11.0"}}
	return runs, prs
}

// viewOf reads one repository's view document back, as the bytes the database
// holds it as, with the ETag it carries. The ETag is the database's own hash
// of the node's content, so an unchanged document is the same ETag and a
// moved one is a moved generation, and the document is compared the same way:
// two views of the same inputs are the same JSON.
func viewOf(t *testing.T, s *Sweeper, ctx context.Context, slug string) (string, string, bool) {
	t.Helper()
	var got View
	etag, found, err := s.Store.Read(ctx, PathView(slug), &got)
	if err != nil {
		t.Fatalf("reading /%s: %v", PathView(slug), err)
	}
	if !found {
		return "", etag, false
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), etag, true
}

// viewJSON is the view the inputs produce, as the document the sweep writes
// it: the join (P1), marshalled the way the database holds it.
func viewJSON(t *testing.T, repo Repo, prs []PullRequest, runs []Run) string {
	t.Helper()
	raw, err := json.Marshal(Join(repo.Name, prs, runs))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestASecondSweepOverUnchangedInputsWritesTheSameDocuments is the part's
// first red step: the document the sweep writes is a function of its inputs,
// so a second sweep over the same GitHub and the same runs writes the same
// bytes and the node's generation does not move — the database's ETag is a
// hash of the node's content, so an unchanged sweep leaves it where it was.
//
// Hand mutation this fails on: stamping the document with the clock the
// sweep ran at (Sweeper.Now), which is the mutation the plan names — the
// second sweep then writes different bytes and the ETag moves, so every tick
// of the schedule would be a new generation of a view that did not change.
func TestASecondSweepOverUnchangedInputsWritesTheSameDocuments(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug

	first, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if first.Repos[0].Stale || first.Repos[0].Records != 1 || first.Repos[0].InFlight != 1 {
		t.Fatalf("the first sweep = %+v, want one record in flight and not stale", first.Repos[0])
	}
	view1, etag1, found := viewOf(t, s, ctx, slug)
	if !found {
		t.Fatalf("the sweep wrote no view at /%s", PathView(slug))
	}
	if want := viewJSON(t, testRepo(), src.prs, src.runs); view1 != want {
		t.Errorf("the view at /%s is %s, want the join of the inputs %s", PathView(slug), view1, want)
	}
	if _, found, _ := s.Store.Read(ctx, PathStale(slug), nil); found {
		t.Errorf("a sweep that read everything wrote a stale marker at /%s", PathStale(slug))
	}

	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	view2, etag2, _ := viewOf(t, s, ctx, slug)
	if view1 != view2 {
		t.Errorf("a second sweep over unchanged inputs wrote a different view:\n%s\n%s", view1, view2)
	}
	if etag1 != etag2 {
		t.Errorf("the second sweep moved /%s's generation: %q then %q", PathView(slug), etag1, etag2)
	}
}

// TestAFailedReadLeavesTheViewAndMarksTheRepositoryStale is the part's second
// red step: a read that fails leaves the previous view exactly where it was
// and records that it could not be reconciled, rather than writing a view
// computed from half its inputs. A view that quietly dropped the pull
// requests GitHub would not answer for is the one outcome this must not
// produce: it would read as "nothing is in flight" about a repository that
// has work in it.
//
// The marker carries a classification of this package's own and never the
// host's text (Q14 A): the failure's own message is another writer's, and it
// is not stored.
//
// Hand mutation this fails on: writing the recomputed view on the failure
// path (the view moves, and the record it now holds is the one the runs
// alone produce).
func TestAFailedReadLeavesTheViewAndMarksTheRepositoryStale(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug

	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	before, etagBefore, _ := viewOf(t, s, ctx, slug)

	src.prErr = errors.New("the host answered 403 rate limited for acme/webapp")
	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatalf("a read that failed failed the sweep: %v", err)
	}
	if !res.Repos[0].Stale {
		t.Fatalf("the repository is not reported stale after a failed read: %+v", res.Repos[0])
	}
	after, etagAfter, _ := viewOf(t, s, ctx, slug)
	if etagBefore != etagAfter || before != after {
		t.Errorf("the failed read moved the view:\n%s\n%s", before, after)
	}
	var mark Stale
	if _, found, err := s.Store.Read(ctx, PathStale(slug), &mark); err != nil || !found {
		t.Fatalf("the failed read left no stale marker at /%s: found=%v err=%v", PathStale(slug), found, err)
	}
	if mark.Reason != StalePullRequests {
		t.Errorf("the marker's reason = %q, want %q", mark.Reason, StalePullRequests)
	}
	if !mark.Since.Equal(s.Now()) {
		t.Errorf("the marker's since = %s, want the sweep's clock %s", mark.Since, s.Now())
	}
	if strings.Contains(mark.Reason, "403") || strings.Contains(mark.Reason, "acme/webapp") {
		t.Errorf("the marker carries the host's own text: %q", mark.Reason)
	}
	if raw := fmt.Sprint(f.Value(PathStale(slug))); strings.Contains(raw, "403") {
		t.Errorf("the node at /%s carries the host's own text: %s", PathStale(slug), raw)
	}

	// A second failure does not move the marker's own generation either: the
	// repository has been stale since the first one, and a marker that
	// restamped itself every tick would be a document that changes on every
	// sweep that changed nothing.
	var first Stale
	etagMark, _, _ := s.Store.Read(ctx, PathStale(slug), &first)
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	var second Stale
	etagAgain, _, _ := s.Store.Read(ctx, PathStale(slug), &second)
	if first.Since != second.Since || etagMark != etagAgain {
		t.Errorf("a second failed sweep restamped the marker: %+v then %+v", first, second)
	}

	// And a read that works again clears it, leaving the view the recompute
	// produced: the marker is a fact about the last sweep and not a latch.
	src.prErr = nil
	res, err = s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Repos[0].Stale {
		t.Errorf("the repository is still reported stale after a read that worked: %+v", res.Repos[0])
	}
	if _, found, err := s.Store.Read(ctx, PathStale(slug), nil); err != nil || found {
		t.Errorf("the recovered sweep left a stale marker at /%s: found=%v err=%v", PathStale(slug), found, err)
	}
}

// TestARunsReadThatFailsMarksTheRepositoryStale: the runs are the view's
// other input, and a repository whose runs could not be read is exactly as
// unreconciled as one whose pull requests could not be — the view is a join,
// and neither half alone is the answer.
func TestARunsReadThatFailsMarksTheRepositoryStale(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runErr = errors.New("the runs bucket refused")
	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Repos[0].Stale {
		t.Fatalf("a repository whose runs could not be read is not stale: %+v", res.Repos[0])
	}
	var mark Stale
	if _, found, err := s.Store.Read(ctx, PathStale(testRepo().Slug), &mark); err != nil || !found || mark.Reason != StaleRuns {
		t.Errorf("the marker = %+v found=%v err=%v, want %q", mark, found, err, StaleRuns)
	}
	if src.calls != 0 {
		t.Errorf("the sweep read GitHub %d times for a repository whose runs it could not read", src.calls)
	}
}

// TestAProviderThisReadDoesNotServeIsRecordedAndNotRead: the project-wide
// read refuses a bitbucket repository before it mints or opens anything
// (Q12 A, finding #30), and the sweep records that as what it is — a
// repository this installation cannot reconcile — rather than failing the
// whole tick, which would leave every other repository's view unmoved
// because of one the project cannot read at all.
func TestAProviderThisReadDoesNotServeIsRecordedAndNotRead(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	src.prErr = ErrBitbucket
	repo := Repo{Provider: "bitbucket", Name: "acme/webapp", Slug: "acme-webapp"}

	res, err := s.Sweep(ctx, []Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Repos[0].Stale {
		t.Fatalf("a repository this read does not serve is not stale: %+v", res.Repos[0])
	}
	var mark Stale
	if _, found, err := s.Store.Read(ctx, PathStale(repo.Slug), &mark); err != nil || !found || mark.Reason != StaleUnsupported {
		t.Errorf("the marker = %+v found=%v err=%v, want %q", mark, found, err, StaleUnsupported)
	}
	if _, found, _ := s.Store.Read(ctx, PathView(repo.Slug), nil); found {
		t.Errorf("the sweep wrote a view for a repository it could not read")
	}
}

// TestTheSweepKeepsTheNewestIntentsAndPrunesTheRest is the count half of the
// follow-up P4's review named: the intent records of a repository are bounded,
// and the bound is applied by the sweep because the report path writes and
// takes nothing back (P4) — a writer that pruned would be a writer that reads
// what is in flight.
//
// The newest records are the ones kept: a declaration is about work about to
// start, so the oldest ones are the ones whose work has long finished. Ties
// on the clock are broken by the ID, so which record is dropped does not
// depend on the map's order.
//
// Hand mutation this fails on: pruning the newest instead of the oldest, or
// pruning by a bound of one.
func TestTheSweepKeepsTheNewestIntentsAndPrunesTheRest(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug
	s.Keep = 3

	base := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	var paths []string
	for i := range 5 {
		in := Intent{DeclaredBy: fmt.Sprintf("someone-%d@example.com", i), At: base.Add(time.Duration(i) * time.Hour), Branch: "main"}
		path := PathIntent(slug, IntentID(in))
		if err := s.Store.Write(ctx, path, in); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Repos[0].Pruned != 2 {
		t.Errorf("the sweep pruned %d records, want 2", res.Repos[0].Pruned)
	}
	for i, path := range paths {
		_, found, err := s.Store.Read(ctx, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := i >= 2; found != want {
			t.Errorf("record %d of 5 (%s): kept=%v, want kept=%v: the newest three are the ones kept", i, path, found, want)
		}
	}

	// A sweep that prunes nothing reports nothing, and leaves the records
	// alone: the bound is a ceiling and not a rewrite.
	again, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if again.Repos[0].Pruned != 0 {
		t.Errorf("a second sweep pruned %d records, want 0", again.Repos[0].Pruned)
	}
}

// TestTheSweepKeepsEveryRecordWhenTheBoundIsNegative: the field's own doc says
// a negative Keep keeps every record, and that is the reading this pins —
// otherwise the bound is a slice past the end of the list, which is a panic in
// a tick rather than a decision about a record.
//
// Hand mutation this fails on: dropping the negative case from prune's switch,
// so ids[:len(ids)-keep] is indexed past the end.
func TestTheSweepKeepsEveryRecordWhenTheBoundIsNegative(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	slug := testRepo().Slug
	s.Keep = -1

	base := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	var paths []string
	for i := range 3 {
		in := Intent{DeclaredBy: fmt.Sprintf("someone-%d@example.com", i), At: base.Add(time.Duration(i) * time.Hour), Branch: "main"}
		path := PathIntent(slug, IntentID(in))
		if err := s.Store.Write(ctx, path, in); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	res, err := s.Sweep(ctx, []Repo{testRepo()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Repos[0].Pruned != 0 {
		t.Errorf("a sweep with a negative bound pruned %d records, want 0", res.Repos[0].Pruned)
	}
	for _, path := range paths {
		if _, found, err := s.Store.Read(ctx, path, nil); err != nil {
			t.Fatal(err)
		} else if !found {
			t.Errorf("the record %s was dropped, want every record kept", path)
		}
	}
}

// TestTheSweepPrunesOneRepositorysRecordsOnly: the records sit under the
// repository they are about (D195), so a sweep of one repository touches no
// other's — the same rule the read grant of Q15(A) is built on.
func TestTheSweepPrunesOneRepositorysRecordsOnly(t *testing.T) {
	s, _, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	s.Keep = 1

	other := Repo{Provider: "github", Name: "acme/other", Slug: "acme-other"}
	base := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	var sweptOldest, sweptNewest, otherOldest string
	for _, repo := range []Repo{testRepo(), other} {
		for i := range 3 {
			in := Intent{DeclaredBy: fmt.Sprintf("someone-%d@example.com", i), At: base.Add(time.Duration(i) * time.Hour), Branch: "main"}
			path := PathIntent(repo.Slug, IntentID(in))
			if err := s.Store.Write(ctx, path, in); err != nil {
				t.Fatal(err)
			}
			switch {
			case repo.Slug == testRepo().Slug && i == 0:
				sweptOldest = path
			case repo.Slug == testRepo().Slug && i == 2:
				sweptNewest = path
			case i == 0:
				otherOldest = path
			}
		}
	}

	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Store.Read(ctx, otherOldest, nil); !found {
		t.Errorf("the sweep of %s pruned a record under %s", testRepo().Name, other.Name)
	}
	if _, found, _ := s.Store.Read(ctx, sweptNewest, nil); !found {
		t.Errorf("the sweep of %s pruned its own newest record", testRepo().Name)
	}
	if _, found, _ := s.Store.Read(ctx, sweptOldest, nil); found {
		t.Errorf("the sweep of %s kept a record it should have pruned", testRepo().Name)
	}
}

// TestTheSweepRefusesToRunWithoutItsParts: a sweeper that would write a view
// it did not compute, or read nothing, is refused by name rather than
// silently doing half its job.
func TestTheSweepRefusesToRunWithoutItsParts(t *testing.T) {
	ctx := testCtx(t)
	s, _, _ := testSweeper(t)
	for _, tc := range []struct {
		name string
		s    *Sweeper
	}{
		{"no reader", &Sweeper{Runs: s.Runs, Store: s.Store}},
		{"no runs source", &Sweeper{Reader: s.Reader, Store: s.Store}},
		{"no store", &Sweeper{Reader: s.Reader, Runs: s.Runs}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("a sweeper with %s ran instead of refusing", tc.name)
				}
			}()
			_, _ = tc.s.Sweep(ctx, []Repo{testRepo()})
		})
	}
}

// TestASweepThatCannotWriteFailsRatherThanReportingSuccess: a database that
// refuses the write is not a repository that could not be reconciled — it is
// the sweep's own failure, and it must reach the job's exit code rather than
// be recorded as a stale view (which would read as a GitHub problem).
func TestASweepThatCannotWriteFailsRatherThanReportingSuccess(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	f.Deny(PathView(testRepo().Slug))

	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err == nil {
		t.Fatal("a sweep whose write was refused returned no error")
	}
	if _, found, _ := s.Store.Read(ctx, PathStale(testRepo().Slug), nil); found {
		t.Errorf("a refused write was recorded as a stale view")
	}
}
