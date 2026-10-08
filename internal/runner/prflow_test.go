package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	statusBegin = "[//]: # (fugaro:status begin)"
	statusEnd   = "[//]: # (fugaro:status end)"
)

// auditProvider wraps the fake and checks, after every call, what must hold
// at every moment: a draft carries no reviewers and no labels, and once a PR
// exists its number is in the run's stored record before the runner makes
// another call.
type auditProvider struct {
	*fake.Provider
	t       *testing.T
	store   *runstore.Store
	created bool
}

func (a *auditProvider) before(op string) {
	a.t.Helper()
	if !a.created {
		return
	}
	rec, err := a.store.ReadRecord(context.Background())
	if err != nil || rec.PR == nil || rec.PR.Number == 0 {
		a.t.Errorf("%s was called before the PR number was saved in the run record (rec=%+v, err=%v)", op, rec, err)
	}
}

func (a *auditProvider) after(op string) {
	a.t.Helper()
	for _, pr := range a.Provider.State.PRs {
		if pr.Draft && (len(pr.Reviewers) > 0 || len(pr.Labels) > 0) {
			a.t.Errorf("after %s: draft PR #%d carries reviewers %v / labels %v", op, pr.Number, pr.Reviewers, pr.Labels)
		}
	}
	if len(a.Provider.State.PRs) > 0 {
		a.created = true
	}
}

func (a *auditProvider) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	a.before("EnsurePR")
	if spec.Draft && (len(spec.Reviewers) > 0 || len(spec.Labels) > 0) {
		a.t.Errorf("EnsurePR asked for a draft carrying reviewers %v / labels %v", spec.Reviewers, spec.Labels)
		return gitprov.PR{}, fmt.Errorf("audit: draft spec with reviewers or labels")
	}
	pr, err := a.Provider.EnsurePR(ctx, spec)
	a.after("EnsurePR")
	return pr, err
}

func (a *auditProvider) UpdatePR(ctx context.Context, n int, u gitprov.PRUpdate) (gitprov.PR, error) {
	a.before("UpdatePR")
	pr, err := a.Provider.UpdatePR(ctx, n, u)
	a.after("UpdatePR")
	return pr, err
}

func (a *auditProvider) ApplyReady(ctx context.Context, n int, reviewers, labels []string) error {
	a.before("ApplyReady")
	err := a.Provider.ApplyReady(ctx, n, reviewers, labels)
	a.after("ApplyReady")
	return err
}

func (a *auditProvider) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	a.before("Comment")
	err := a.Provider.Comment(ctx, pr, body)
	a.after("Comment")
	return err
}

func (a *auditProvider) PullRequest(ctx context.Context, n int) (gitprov.PRInfo, error) {
	a.before("PullRequest")
	return a.Provider.PullRequest(ctx, n)
}

// testClock is a clock only the test moves.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

// newClock starts at the real time: the run's deadline is taken from this clock
// while the stage contexts count against the wall clock, so a fixed date makes
// every run time out once the wall clock passes it.
func newClock() *testClock { return &testClock{t: time.Now().UTC().Truncate(time.Second)} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// afterStep runs s, then advances the clock past the status interval.
func afterStep(c *testClock, s step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := s(t, ctx, req)
		c.advance(25 * time.Second)
		return res, err
	}
}

// prCfg is the fixture config with reviewers and labels, review rounds, and
// extra under git.pr.
func prCfg(t *testing.T, rounds int, prExtra string) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	cfg = strings.Replace(cfg, "  base_branch: main\n", "  base_branch: main\n  pr: { labels: [fugaro], reviewers: [octocat]"+prExtra+" }\n", 1)
	return strings.Replace(cfg, "review_rounds: 2", fmt.Sprintf("review_rounds: %d", rounds), 1)
}

// prHarness is a harness whose provider is audited and which retries fast.
func prHarness(t *testing.T, cfg string) *harness {
	t.Helper()
	h := newHarness(t, cfg, nil)
	h.deps.OpenProvider = gitprov.Static(&auditProvider{Provider: h.provider, t: t, store: h.store})
	h.deps.RetryDelay = time.Millisecond
	return h
}

// ops is the provider's write-call log as "Op#pr detail".
func ops(h *harness) []string {
	var out []string
	for _, c := range h.provider.State.Calls {
		out = append(out, fmt.Sprintf("%s#%d %s", c.Op, c.PR, c.Detail))
	}
	return out
}

// indexOf is the position of the first call of op whose text contains
// detail at or after from, or -1.
func indexOf(calls []string, op, detail string, from int) int {
	for i := from; i < len(calls); i++ {
		if strings.HasPrefix(calls[i], op+"#") && strings.Contains(calls[i], detail) {
			return i
		}
	}
	return -1
}

func count(calls []string, op, detail string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, op+"#") && strings.Contains(c, detail) {
			n++
		}
	}
	return n
}

// fixVerified is a fix stage that commits and verifies.
func fixVerified(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "echo more >> feature.txt && git commit -qam 'more'")
	verifyTest(t, ctx, req)
	return agent.Result{CostUSD: 0.5}, nil
}

// probe runs check before s.
func probe(check func(t *testing.T), s step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		check(t)
		return s(t, ctx, req)
	}
}

func remoteHasBranch(t *testing.T, h *harness) bool {
	t.Helper()
	return testutil.Git(t, h.remote, "for-each-ref", "refs/heads/fugaro/") != ""
}

func storedPR(t *testing.T, h *harness) *runstore.PRRef {
	t.Helper()
	rec, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rec.PR
}

// TestOpensDraftAfterFirstVerifiedStage: the draft appears after the first
// verified implement stage, not at the start, with no reviewers; the number
// is already in the record; reviewers come only after the flip to ready.
func TestOpensDraftAfterFirstVerifiedStage(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	var seen int
	var draft bool
	var reviewers []string
	var saved *runstore.PRRef
	var body string
	rec, err := h.run(t, implement("feature"), probe(func(t *testing.T) {
		seen = len(h.provider.State.PRs)
		if seen == 1 {
			pr := h.provider.State.PRs[0]
			draft, reviewers, body = pr.Draft, pr.Reviewers, pr.Body
		}
		saved = storedPR(t, h)
	}, review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if seen != 1 || !draft || len(reviewers) != 0 || saved == nil || saved.Number != 1 || saved.Desc == "" || saved.StatusAt == nil {
		t.Fatalf("at the review stage: PRs=%d draft=%v reviewers=%v saved=%+v", seen, draft, reviewers, saved)
	}
	if !strings.Contains(body, statusBegin) || !strings.Contains(body, "**Running**") || !strings.HasPrefix(body, "Adds feature.txt.") {
		t.Fatalf("early body:\n%s", body)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.PR.Number != 1 {
		t.Fatalf("rec = %+v", rec)
	}
	calls := ops(h)
	if calls[0] != "EnsurePR#0 draft=true" {
		t.Fatalf("the first provider write must be the draft creation: %q", calls)
	}
	flip := indexOf(calls, "EnsurePR", "draft=false", 0)
	apply := indexOf(calls, "ApplyReady", "reviewers=octocat labels=fugaro", 0)
	comment := indexOf(calls, "Comment", "", 0)
	if count(calls, "EnsurePR", "") != 2 || flip < 1 || apply < flip || comment < apply {
		t.Fatalf("want create, flip, ApplyReady, Comment in that order: %q", calls)
	}
	if calls[flip] != "EnsurePR#1 draft=false" {
		t.Fatalf("the flip must go by number: %q", calls)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) || !slices.Equal(pr.Labels, []string{"fugaro"}) {
		t.Fatalf("final PR = %+v", pr)
	}
	if !strings.Contains(pr.Body, "**Ready for review**") || strings.Count(pr.Body, statusBegin) != 1 || !strings.HasPrefix(pr.Body, "Adds feature.txt.") {
		t.Fatalf("final body:\n%s", pr.Body)
	}
	if pr.Title != "Add feature" {
		t.Fatalf("title = %q", pr.Title)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote tip %s != head %s", got, rec.HeadSHA)
	}
}

// TestNoPushWithoutVerifiedTest: an implement stage without a passing test
// on a clean tree at HEAD pushes nothing and opens nothing; finalize opens
// the draft as before. With checkpoints off (an unverified boundary pushes
// since 0.5.1).
func TestNoPushWithoutVerifiedTest(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ", checkpoints: false"))
	rec, err := h.run(t, commitOnly("feature"), probe(func(t *testing.T) {
		if len(h.provider.State.PRs) != 0 || remoteHasBranch(t, h) {
			t.Errorf("an unverified stage pushed or opened a PR: %+v", h.provider.State.PRs)
		}
	}, review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || !onlyPR(t, h.provider).Draft {
		t.Fatalf("rec = %+v", rec)
	}
	if h.provider.State.Calls[0].Op != "EnsurePR" || len(onlyPR(t, h.provider).Reviewers) != 0 {
		t.Fatalf("calls = %q", ops(h))
	}
}

// TestFirstRoundFailsThenFixOpensDraft: a first round that fails verification
// opens nothing; the fix stage that verifies opens the draft. With
// checkpoints off (an unverified boundary pushes since 0.5.1).
func TestFirstRoundFailsThenFixOpensDraft(t *testing.T) {
	h := prHarness(t, prCfg(t, 3, ", checkpoints: false"))
	h.fails(t, "beta")
	clear := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := os.WriteFile(h.failsFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return fixVerified(t, ctx, req)
	}
	var afterFirst, afterFix int
	rec, err := h.run(t, implement("feature"),
		probe(func(t *testing.T) {
			afterFirst = len(h.provider.State.PRs)
			if remoteHasBranch(t, h) {
				t.Error("a branch whose tests fail was pushed")
			}
		}, review("changes", 1)),
		clear,
		probe(func(t *testing.T) { afterFix = len(h.provider.State.PRs) }, review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst != 0 || afterFix != 1 || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("PRs after the first round %d, after the fix %d; rec = %+v", afterFirst, afterFix, rec)
	}
}

// TestFailedRunLeavesDraftWithoutReviewers: a run whose tests never pass ends
// with a draft opened at finalize, no reviewers, no labels, no ApplyReady.
func TestFailedRunLeavesDraftWithoutReviewers(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	h.fails(t, "beta")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || len(pr.Reviewers) != 0 || len(pr.Labels) != 0 || count(ops(h), "ApplyReady", "") != 0 {
		t.Fatalf("PR = %+v, calls = %q", pr, ops(h))
	}
	if !strings.Contains(pr.Body, "**Draft** · tests failing on the final commit") {
		t.Fatalf("body:\n%s", pr.Body)
	}
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "tests failing") {
		t.Fatalf("comments = %q", pr.Comments)
	}
}

// TestDraftOutcomeGetsNoReviewersOrLabels: an early draft that ends a draft
// (the last review still has findings) never gets reviewers.
func TestDraftOutcomeGetsNoReviewersOrLabels(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixVerified, review("changes", 2))
	if err != nil || rec.Outcome != runstore.OutcomeDraft || rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || len(pr.Reviewers) != 0 || len(pr.Labels) != 0 || count(ops(h), "ApplyReady", "") != 0 {
		t.Fatalf("PR = %+v, calls = %q", pr, ops(h))
	}
	if !strings.Contains(pr.Body, "**Draft** · review round 2 still has 2 findings") {
		t.Fatalf("body:\n%s", pr.Body)
	}
}

// TestReviewerFailureKeepsReady: ApplyReady failing leaves the PR ready and
// says so in the report.
func TestReviewerFailureKeepsReady(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	h.provider.FailApplyReady = 1
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || len(pr.Reviewers) != 0 {
		t.Fatalf("PR = %+v", pr)
	}
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "**Note:** reviewers and labels could not be applied") {
		t.Fatalf("comments = %q", pr.Comments)
	}
}

// TestRejectedReviewerNotedInReport: a host that refuses one reviewer still
// gets the rest, and the report names the failure.
func TestRejectedReviewerNotedInReport(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	h.provider.RejectReviewers = []string{"octocat"}
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || !slices.Equal(pr.Labels, []string{"fugaro"}) || !strings.Contains(pr.Comments[0], "were rejected") {
		t.Fatalf("PR = %+v", pr)
	}
}

// TestHaltAfterPushLeavesDraftWithComment: a halt after the first push
// leaves the draft, with the halt in its section and in the comment.
func TestHaltAfterPushLeavesDraftWithComment(t *testing.T) {
	b := newHandleBox(t)
	h := prHarness(t, prCfg(t, 2, ""))
	rec, err := h.run(t, implement("feature"), haltAfter(b, review("changes", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || len(pr.Reviewers) != 0 || !strings.Contains(pr.Body, `**Halted** · halted: run\_cap`) {
		t.Fatalf("PR = %+v", pr)
	}
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "**Halted:**") {
		t.Fatalf("comments = %q", pr.Comments)
	}
}

// TestCancelFinalizesDraftWithNote: a cancel after the first push leaves the
// draft with a cancelled section and the report.
func TestCancelFinalizesDraftWithNote(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	cancelThenBlock := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, implement("feature"), cancelThenBlock)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || len(pr.Reviewers) != 0 || !strings.Contains(pr.Body, "**Cancelled** · cancelled during review") || len(pr.Comments) != 1 {
		t.Fatalf("PR = %+v", pr)
	}
}

// closePR closes PR 1 as a person would.
func closePR(h *harness) {
	h.provider.State.PRs[0].State = gitprov.PRClosed
}

// TestClosedMidRunEndsNone: a person closes the draft mid-run; no second PR
// is opened, the loop stops, the branch stays pushed, nothing is posted.
func TestClosedMidRunEndsNone(t *testing.T) {
	clk := newClock()
	h := prHarness(t, prCfg(t, 3, ""))
	h.deps.Now = clk.Now
	closeIt := afterStep(clk, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		closePR(h)
		return review("changes", 1)(t, ctx, req)
	})
	rec, err := h.run(t, implement("feature"), closeIt)
	if err != nil {
		t.Fatal(err)
	}
	wantReason := "PR #1 was closed during the run; the branch was pushed"
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone || rec.Reason != wantReason {
		t.Fatalf("rec = %+v", rec)
	}
	if len(h.provider.State.PRs) != 1 || len(h.provider.State.PRs[0].Comments) != 0 {
		t.Fatalf("PRs = %+v", h.provider.State.PRs)
	}
	if len(h.agent.calls) != 2 {
		t.Fatalf("%d stages ran; the loop should stop once the PR is closed", len(h.agent.calls))
	}
	if rec.PushedHead == "" || !remoteHasBranch(t, h) {
		t.Fatalf("the branch was not pushed: %+v", rec)
	}
	if n := count(ops(h), "EnsurePR", "#0 "); n != 1 {
		t.Fatalf("EnsurePR looked up by branch %d times, want only the creation: %q", n, ops(h))
	}
}

// TestFinalizeNeverCreatesSecondPR: closed during the last stage, so only
// finalize notices; it still opens no second PR.
func TestFinalizeNeverCreatesSecondPR(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	closeIt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		closePR(h)
		return review("ship", 0)(t, ctx, req)
	}
	rec, err := h.run(t, implement("feature"), closeIt)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone || rec.Reason != "PR #1 was closed during the run; the branch was pushed" {
		t.Fatalf("rec = %+v", rec)
	}
	if len(h.provider.State.PRs) != 1 || len(h.provider.State.PRs[0].Comments) != 0 || count(ops(h), "ApplyReady", "") != 0 {
		t.Fatalf("PRs = %+v, calls = %q", h.provider.State.PRs, ops(h))
	}
	if _, err := h.store.ReadRecord(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestCrashAfterCreateRecoversByBranch: the draft is created and the call
// reports failure (a lost response). The retry finds it by branch: one PR,
// its number recorded, and finalize settles it by number.
func TestCrashAfterCreateRecoversByBranch(t *testing.T) {
	for _, failures := range []int{1, 3} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			h := prHarness(t, prCfg(t, 2, ""))
			h.provider.FailEnsureAfterCreate = failures
			var saved *runstore.PRRef
			rec, err := h.run(t, implement("feature"), probe(func(t *testing.T) { saved = storedPR(t, h) }, review("ship", 0)))
			if err != nil || rec.Outcome != runstore.OutcomeReady {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if saved == nil || saved.Number != 1 {
				t.Fatalf("the number was not recorded when the draft was opened: %+v", saved)
			}
			pr := onlyPR(t, h.provider)
			if pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) {
				t.Fatalf("PR = %+v", pr)
			}
			if i := indexOf(ops(h), "EnsurePR", "draft=false", 0); i < 0 || !strings.HasPrefix(ops(h)[i], "EnsurePR#1 ") {
				t.Fatalf("finalize must settle by number: %q", ops(h))
			}
		})
	}
}

// TestFinalizeStillCreatesAfterEarlyFailures: the early open fails every
// attempt; the run goes on and finalize creates the PR.
func TestFinalizeStillCreatesAfterEarlyFailures(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	h.provider.FailEnsure = 3
	var atReview int
	rec, err := h.run(t, implement("feature"), probe(func(t *testing.T) { atReview = len(h.provider.State.PRs) }, review("ship", 0)))
	if err != nil || rec.Outcome != runstore.OutcomeReady || atReview != 0 {
		t.Fatalf("rec = %+v, err = %v, PRs at review = %d", rec, err, atReview)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) {
		t.Fatalf("PR = %+v", pr)
	}
}

// TestStatusUpdateFailureOnlyWarns: every status update fails; the run still
// ends ready, with reviewers applied.
func TestStatusUpdateFailureOnlyWarns(t *testing.T) {
	clk := newClock()
	h := prHarness(t, prCfg(t, 2, ""))
	h.deps.Now = clk.Now
	h.provider.FailUpdatePR = 100
	rec, err := h.run(t, afterStep(clk, implement("feature")), afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) || !strings.Contains(pr.Comments[0], "status section may be out of date") {
		t.Fatalf("PR = %+v", pr)
	}
}

// TestThreeFailuresStopUpdates: after three failed updates in a row there are
// no more until finalize, which still writes.
func TestThreeFailuresStopUpdates(t *testing.T) {
	clk := newClock()
	h := prHarness(t, prCfg(t, 4, ""))
	h.deps.Now = clk.Now
	h.provider.FailUpdatePR = 3
	rec, err := h.run(t, afterStep(clk, implement("feature")),
		afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified),
		afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified),
		afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified),
		review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// Three boundary updates failed; the other three boundaries were skipped;
	// finalize wrote once more, and that one succeeded.
	if n := count(ops(h), "UpdatePR", ""); n != 4 {
		t.Fatalf("%d UpdatePR calls, want 3 failing boundary updates and finalize's: %q", n, ops(h))
	}
	if !strings.Contains(onlyPR(t, h.provider).Body, "**Ready for review**") {
		t.Fatalf("finalize did not write the final section:\n%s", onlyPR(t, h.provider).Body)
	}
}

// TestSuccessResetsFailureCount: failures that are not three in a row never
// stop the updates.
func TestSuccessResetsFailureCount(t *testing.T) {
	clk := newClock()
	h := prHarness(t, prCfg(t, 4, ""))
	h.deps.Now = clk.Now
	h.provider.FailUpdatePR = 2
	steps := []step{afterStep(clk, implement("feature")),
		afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified),
		afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified),
		afterStep(clk, review("changes", 1)), afterStep(clk, fixVerified),
		review("ship", 0)}
	if rec, err := h.run(t, steps...); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// Six boundaries (two failed, four written) and finalize's.
	if n := count(ops(h), "UpdatePR", ""); n != 7 {
		t.Fatalf("%d UpdatePR calls, want 7: %q", n, ops(h))
	}
}

// TestCoalescesWithin20s: boundaries closer together than 20 seconds do not
// each write; finalize does.
func TestCoalescesWithin20s(t *testing.T) {
	clk := newClock() // frozen: no boundary is 20 seconds after the opening
	h := prHarness(t, prCfg(t, 4, ""))
	h.deps.Now = clk.Now
	steps := []step{implement("feature"), review("changes", 1), fixVerified, review("changes", 1), fixVerified, review("changes", 1), fixVerified, review("ship", 0)}
	if rec, err := h.run(t, steps...); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := count(ops(h), "UpdatePR", ""); n != 1 {
		t.Fatalf("%d UpdatePR calls, want only finalize's: %q", n, ops(h))
	}
	// One boundary after 25 seconds writes.
	clk2 := newClock()
	h2 := prHarness(t, prCfg(t, 4, ""))
	h2.deps.Now = clk2.Now
	steps = []step{implement("feature"), afterStep(clk2, review("changes", 1)), fixVerified, review("changes", 1), fixVerified, review("changes", 1), fixVerified, review("ship", 0)}
	if rec, err := h2.run(t, steps...); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := count(ops(h2), "UpdatePR", ""); n != 2 {
		t.Fatalf("%d UpdatePR calls, want the boundary after 25s (the later ones coalesced) and finalize's: %q", n, ops(h2))
	}
}

// TestHumanEditOutsideMarkersKept: a person's edit of the description survives
// the status updates and finalize; only the section changes.
func TestHumanEditOutsideMarkersKept(t *testing.T) {
	clk := newClock()
	h := prHarness(t, prCfg(t, 2, ""))
	h.deps.Now = clk.Now
	edit := afterStep(clk, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		pr := &h.provider.State.PRs[0]
		pr.Body = "Reviewer note: please look at the cache.\n\n" + pr.Body[strings.Index(pr.Body, statusBegin):]
		return review("changes", 1)(t, ctx, req)
	})
	rec, err := h.run(t, implement("feature"), edit, fixVerified, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	body := onlyPR(t, h.provider).Body
	if !strings.HasPrefix(body, "Reviewer note: please look at the cache.\n\n"+statusBegin) || strings.Contains(body, "Adds feature.txt.") || !strings.Contains(body, "**Ready for review**") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestEarlyDraftFalseKeepsFinalizeOnly: the old flow. Nothing is pushed or
// opened before finalize, the PR is created once with the description from
// pr.md and no status section, and reviewers come with it. Checkpoints are
// off too, so no mid-run push at all; TestEarlyDraftFalseCheckpointsWithoutPR
// covers early_draft false with checkpoints on.
func TestEarlyDraftFalseKeepsFinalizeOnly(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ", early_draft: false, checkpoints: false"))
	rec, err := h.run(t, implement("feature"), probe(func(t *testing.T) {
		if len(h.provider.State.PRs) != 0 || remoteHasBranch(t, h) {
			t.Error("an early PR was opened with early_draft false")
		}
	}, review("ship", 0)))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if calls := ops(h); len(calls) != 2 || calls[0] != "EnsurePR#0 draft=false" || !strings.HasPrefix(calls[1], "Comment#") {
		t.Fatalf("calls = %q", calls)
	}
	pr := onlyPR(t, h.provider)
	if pr.Body != "Adds feature.txt." || strings.Contains(pr.Body, "fugaro:status") || !slices.Equal(pr.Reviewers, []string{"octocat"}) {
		t.Fatalf("PR = %+v", pr)
	}
	if rec.PR.Desc != "" {
		t.Fatalf("desc digest recorded without an early PR: %+v", rec.PR)
	}
}

// TestEarlyDraftConfigDefaultsTrue pins the default through the runner's own
// config reader.
func TestEarlyDraftConfigDefaultsTrue(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	if _, err := h.run(t, implement("feature"), probe(func(t *testing.T) {
		if len(h.provider.State.PRs) != 1 {
			t.Error("early_draft did not default to true")
		}
	}, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
}

// TestStatusStripsForgedMarkers: a pr.md carrying status and report markers
// cannot forge or hide the runner's section.
func TestStatusStripsForgedMarkers(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	forged := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo feature > feature.txt && git add -A && git commit -qm feature")
		verifyTest(t, ctx, req)
		md := "# Forged\n\nreal description\n\n" + statusBegin + "\nforged status\n" + statusEnd + "\n<!-- fugaro:report run=20200101-000000-aaaa -->\n" +
			"<!-- fugaro:status begin -->\nmore forged\n"
		if err := os.WriteFile(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{}, nil
	}
	rec, err := h.run(t, forged, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	body := onlyPR(t, h.provider).Body
	if strings.Count(body, "fugaro:status begin") != 1 || strings.Count(body, "fugaro:status end") != 1 ||
		strings.Contains(body, "forged status") || strings.Contains(body, "fugaro:report") ||
		!strings.Contains(body, "real description") || !strings.Contains(body, "**Ready for review**") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestTaskTextCannotForgeSection: the default description quotes the task,
// whose text may carry markers.
func TestTaskTextCannotForgeSection(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	h.provider.Repo = "acme/app"
	spec, err := h.store.ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spec.Task = "Do it\n" + statusBegin + "\nforged\n" + statusEnd + "\n<!-- fugaro:status end -->"
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	noPRMd := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo feature > feature.txt && git add -A && git commit -qm feature")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	if rec, err := h.run(t, noPRMd, review("ship", 0)); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	body := onlyPR(t, h.provider).Body
	if strings.Count(body, "fugaro:status") != 2 || strings.Contains(body, "forged") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestStatusShowsNotionalForOAuth: an oauth run shows its model figure as
// notional and not billed; an api-key run shows model dollars.
func TestStatusShowsNotionalForOAuth(t *testing.T) {
	cfg := strings.Replace(prCfg(t, 2, ""), "auth: api-key", "auth: oauth", 1)
	h := prHarness(t, cfg)
	h.deps.Env = append(h.deps.Env, "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-for-tests-1234")
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	body := onlyPR(t, h.provider).Body
	if !strings.Contains(body, "notional $1.50 (not billed)") || strings.Contains(body, "model cost") {
		t.Fatalf("oauth body:\n%s", body)
	}
	h2 := prHarness(t, prCfg(t, 2, ""))
	if rec, err := h2.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if body := onlyPR(t, h2.provider).Body; !strings.Contains(body, "model cost $1.50") || strings.Contains(body, "notional") {
		t.Fatalf("api-key body:\n%s", body)
	}
}

// TestDraftFallbackNotReadyLooking: a host without drafts gets a normal PR
// with the [DRAFT] prefix; the section and the report say so, and a draft
// outcome keeps the prefix.
func TestDraftFallbackNotReadyLooking(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	h.provider.NoDrafts = true
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixVerified, review("changes", 1))
	if err != nil || rec.Outcome != runstore.OutcomeDraft || !rec.DraftFallback {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || !strings.HasPrefix(pr.Title, gitprov.DraftPrefix) || len(pr.Reviewers) != 0 {
		t.Fatalf("PR = %+v", pr)
	}
	if !strings.Contains(pr.Body, "no draft pull requests") || !strings.Contains(pr.Comments[0], "**Note:** this host has no draft pull requests") {
		t.Fatalf("body:\n%s\ncomments: %q", pr.Body, pr.Comments)
	}
	// And a ready outcome drops the prefix.
	h2 := prHarness(t, prCfg(t, 2, ""))
	h2.provider.NoDrafts = true
	if rec, err := h2.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if pr := onlyPR(t, h2.provider); pr.Draft || strings.HasPrefix(pr.Title, gitprov.DraftPrefix) || !slices.Equal(pr.Reviewers, []string{"octocat"}) {
		t.Fatalf("PR = %+v", pr)
	}
}

// ---- follow-ups ----

// earlyFirst is a first run that opens its draft early and ends a draft.
func earlyFirst() []step {
	return []step{implement("feature"), review("changes", 1), fixVerified, review("changes", 1)}
}

func prFollowYAML(t *testing.T, prExtra string) string {
	t.Helper()
	cfg := followUpYAML(t, "")
	return strings.Replace(cfg, "  base_branch: main\n", "  base_branch: main\n  pr: { labels: [fugaro], reviewers: [octocat]"+prExtra+" }\n", 1)
}

// TestFollowUpFlipAppliesReviewers: a follow-up that flips the draft to ready
// requests the reviewers, after the flip, updates only the section, and
// never rewrites the description.
func TestFollowUpFlipAppliesReviewers(t *testing.T) {
	h := followUpHarness(t, prFollowYAML(t, ""), nil, earlyFirst()...)
	st := h.state(t)
	if !st.PRs[0].Draft || len(st.PRs[0].Reviewers) != 0 {
		t.Fatalf("first run's PR = %+v", st.PRs[0])
	}
	before := st.PRs[0].Body
	firstCalls := len(st.Calls)
	h.followUp(t, followID, runID, "")
	rec, err := h.run(t, implement("again"), review("ship", 0))
	mustReady(t, rec, err)
	st = h.state(t)
	pr := st.PRs[0]
	if pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) || len(st.PRs) != 1 {
		t.Fatalf("PR = %+v", pr)
	}
	var calls []string
	for _, c := range st.Calls[firstCalls:] {
		calls = append(calls, fmt.Sprintf("%s#%d %s", c.Op, c.PR, c.Detail))
	}
	flip, apply := indexOf(calls, "EnsurePR", "draft=false", 0), indexOf(calls, "ApplyReady", "reviewers=octocat", 0)
	if count(calls, "EnsurePR", "") != 1 || calls[flip] != "EnsurePR#1 draft=false" || apply < flip || count(calls, "UpdatePR", "title") != 0 {
		t.Fatalf("follow-up calls = %q", calls)
	}
	outside := func(b string) string { return gitprov.StripStatus(b) }
	if outside(pr.Body) != outside(before) || !strings.Contains(pr.Body, "**Ready for review**") {
		t.Fatalf("description changed outside the section:\n--- before ---\n%s\n--- after ---\n%s", before, pr.Body)
	}
}

// TestFollowUpOnReadyPRDoesNotReRequest: a follow-up that finds a ready PR
// does not request reviewers again.
func TestFollowUpOnReadyPRDoesNotReRequest(t *testing.T) {
	h := followUpHarness(t, prFollowYAML(t, ""), nil, implement("feature"), review("ship", 0))
	first := len(h.state(t).Calls)
	h.followUp(t, followID, runID, "")
	rec, err := h.run(t, implement("again"), review("ship", 0))
	mustReady(t, rec, err)
	st := h.state(t)
	for _, c := range st.Calls[first:] {
		if c.Op == "ApplyReady" {
			t.Fatalf("a follow-up re-requested reviewers on a ready PR: %+v", st.Calls[first:])
		}
	}
	if !strings.Contains(st.PRs[0].Body, "**Ready for review**") {
		t.Fatalf("body:\n%s", st.PRs[0].Body)
	}
}

// TestFollowUpWithoutSectionChangesOnlyDraftState: a PR with no status
// section (opened by the finalize-only flow) is left alone but for its draft
// state; the title and description are never written.
func TestFollowUpWithoutSectionChangesOnlyDraftState(t *testing.T) {
	cfg := prFollowYAML(t, ", early_draft: false")
	h := followUpHarness(t, cfg, nil, commitOnly("feature"), review("ship", 0))
	st := h.state(t)
	if !st.PRs[0].Draft || strings.Contains(st.PRs[0].Body, "fugaro:status") {
		t.Fatalf("first run's PR = %+v", st.PRs[0])
	}
	before, first := st.PRs[0].Body, len(st.Calls)
	h.followUp(t, followID, runID, "")
	rec, err := h.run(t, implement("again"), review("ship", 0))
	mustReady(t, rec, err)
	st = h.state(t)
	var calls []string
	for _, c := range st.Calls[first:] {
		calls = append(calls, fmt.Sprintf("%s#%d %s", c.Op, c.PR, c.Detail))
	}
	if count(calls, "UpdatePR", "") != 0 || st.PRs[0].Body != before || st.PRs[0].Draft {
		t.Fatalf("calls = %q, body = %q", calls, st.PRs[0].Body)
	}
	if !slices.Equal(st.PRs[0].Reviewers, []string{"octocat"}) {
		t.Fatalf("the flip to ready must request reviewers: %+v", st.PRs[0])
	}
}

// TestHumanEditKeptByFollowUp: a follow-up updates the section of a PR whose
// description a person edited, and keeps the edit.
func TestHumanEditKeptByFollowUp(t *testing.T) {
	h := followUpHarness(t, prFollowYAML(t, ""), nil, earlyFirst()...)
	h.editState(t, func(st *fake.State) {
		b := st.PRs[0].Body
		st.PRs[0].Body = "Edited by a person.\n\n" + b[strings.Index(b, statusBegin):]
	})
	h.followUp(t, followID, runID, "")
	rec, err := h.run(t, implement("again"), review("ship", 0))
	mustReady(t, rec, err)
	if body := h.state(t).PRs[0].Body; !strings.HasPrefix(body, "Edited by a person.\n\n"+statusBegin) || !strings.Contains(body, "**Ready for review**") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestEarlyOpenThatStallsDoesNotHoldTheRun: a provider that stops answering
// during the early open costs only the bound; the number it did create is
// recorded and finalize settles it.
func TestEarlyOpenThatStallsDoesNotHoldTheRun(t *testing.T) {
	runner.SetEarlyOpenTimeout(t, 100*time.Millisecond)
	h := newHarness(t, prCfg(t, 2, ""), nil)
	h.deps.RetryDelay = time.Millisecond
	h.deps.OpenProvider = gitprov.Static(&stallFirstEnsure{Provider: h.provider})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(h.agent.calls) != 2 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) {
		t.Fatalf("PR = %+v", pr)
	}
}

// stallFirstEnsure creates the PR and then hangs until the context ends, on
// the first EnsurePR only.
type stallFirstEnsure struct {
	*fake.Provider
	stalled bool
}

func (s *stallFirstEnsure) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	pr, err := s.Provider.EnsurePR(ctx, spec)
	if err != nil || s.stalled {
		return pr, err
	}
	s.stalled = true
	<-ctx.Done()
	return pr, ctx.Err()
}

// failFlip fails every EnsurePR that goes by number: finalize's flip.
type failFlip struct{ gitprov.Provider }

func (f failFlip) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	if spec.Number != 0 {
		return gitprov.PR{}, fmt.Errorf("flip refused")
	}
	return f.Provider.EnsurePR(ctx, spec)
}

// TestFinalizeFlipFailureLeavesDraftWithNote: when finalize cannot settle the
// early draft, it stays a draft (never looks more ready than it is), says
// so in its section and in a comment, and the run is an infra error as
// before.
func TestFinalizeFlipFailureLeavesDraftWithNote(t *testing.T) {
	h := newHarness(t, prCfg(t, 2, ""), nil)
	h.deps.RetryDelay = time.Millisecond
	h.deps.OpenProvider = gitprov.Static(failFlip{h.provider})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError || rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || len(pr.Reviewers) != 0 || len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "not ready") ||
		!strings.Contains(pr.Body, "**Stopped:**") {
		t.Fatalf("PR = %+v", pr)
	}
}

// TestAgentOpenedReadyPRIsAdoptedAsDraft: an agent that disobeys and opens a
// ready PR with reviewers is adopted by branch, turned into a draft, its
// number saved, no second PR opened, and the reviewers already on it are not
// duplicated. (The notification itself cannot be undone.)
func TestAgentOpenedReadyPRIsAdoptedAsDraft(t *testing.T) {
	h := newHarness(t, prCfg(t, 2, ""), nil)
	h.deps.RetryDelay = time.Millisecond
	var atReview *fake.PRState
	openIt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		_, perr := h.provider.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/" + runID, Base: "main", Title: "Agent's own", Body: "x", Reviewers: []string{"octocat"}})
		if perr != nil {
			t.Fatal(perr)
		}
		return res, err
	}
	snap := probe(func(t *testing.T) { c := h.provider.State.PRs[0]; atReview = &c }, review("changes", 1))
	rec, err := h.run(t, openIt, snap, fixVerified, review("changes", 1))
	if err != nil || rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if atReview == nil || !atReview.Draft {
		t.Fatalf("the agent's PR was not converted to a draft: %+v", atReview)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft || !slices.Equal(pr.Reviewers, []string{"octocat"}) || count(ops(h), "ApplyReady", "") != 0 {
		t.Fatalf("PR = %+v, calls %q", pr, ops(h))
	}
}

// slowProvider delays the reads and requests finalize makes around the report.
type slowProvider struct {
	gitprov.Provider
	d time.Duration
}

func (s slowProvider) PullRequest(ctx context.Context, n int) (gitprov.PRInfo, error) {
	time.Sleep(s.d)
	return s.Provider.PullRequest(ctx, n)
}

func (s slowProvider) ApplyReady(ctx context.Context, n int, r, l []string) error {
	time.Sleep(s.d)
	return s.Provider.ApplyReady(ctx, n, r, l)
}

func (s slowProvider) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Provider.Comment(ctx, pr, body)
}

// TestSlowSettleDoesNotStarveTheReport: slow reads and a slow reviewer
// request eat finalize's reserve; the report is still posted.
func TestSlowSettleDoesNotStarveTheReport(t *testing.T) {
	cfg := strings.Replace(prCfg(t, 2, ""), "finalize_reserve: 30s", "finalize_reserve: 2s", 1)
	h := newHarness(t, cfg, nil)
	h.deps.OpenProvider = gitprov.Static(slowProvider{h.provider, 1200 * time.Millisecond})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if pr := onlyPR(t, h.provider); len(pr.Comments) != 1 {
		t.Fatalf("report not posted: %+v", pr.Comments)
	}
}

// failFirstSettle fails the first body write after the flip to ready.
type failFirstSettle struct {
	gitprov.Provider
	ready, failed bool
}

func (f *failFirstSettle) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	pr, err := f.Provider.EnsurePR(ctx, spec)
	if spec.Number != 0 && !spec.Draft {
		f.ready = true
	}
	return pr, err
}

func (f *failFirstSettle) UpdatePR(ctx context.Context, n int, u gitprov.PRUpdate) (gitprov.PR, error) {
	if f.ready && !f.failed {
		f.failed = true
		return gitprov.PR{}, fmt.Errorf("transient")
	}
	return f.Provider.UpdatePR(ctx, n, u)
}

// TestFinalSectionRetriedOnce: a ready PR does not keep saying Running after
// one failed write.
func TestFinalSectionRetriedOnce(t *testing.T) {
	h := newHarness(t, prCfg(t, 2, ""), nil)
	h.deps.OpenProvider = gitprov.Static(&failFirstSettle{Provider: h.provider})
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if body := onlyPR(t, h.provider).Body; !strings.Contains(body, "**Ready for review**") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestMarkerOnlyTitleFallsBack: a title that is only a forged marker does not
// leave the PR without one.
func TestMarkerOnlyTitleFallsBack(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	mk := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo feature > feature.txt && git add -A && git commit -qm feature")
		verifyTest(t, ctx, req)
		md := statusBegin + "\n\nbody\n"
		if err := os.WriteFile(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{}, nil
	}
	if rec, err := h.run(t, mk, review("ship", 0)); err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if title := onlyPR(t, h.provider).Title; title != "Fugaro run "+runID {
		t.Fatalf("title = %q", title)
	}
}

// TestMergedMidRunSaysMerged: the failure reason tells merged from closed.
func TestMergedMidRunSaysMerged(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	mergeIt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		h.provider.State.PRs[0].State = gitprov.PRMerged
		return review("ship", 0)(t, ctx, req)
	}
	rec, err := h.run(t, implement("feature"), mergeIt)
	if err != nil || rec.Status != runstore.StatusFailed || rec.Reason != "PR #1 was merged during the run; the branch was pushed" {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestCancelledRunWithClosedPRStaysCancelled: E10, a cancelled run keeps its
// status; the closed PR is its reason, and nothing is posted.
func TestCancelledRunWithClosedPRStaysCancelled(t *testing.T) {
	h := prHarness(t, prCfg(t, 2, ""))
	cancelThenBlock := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		closePR(h)
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, implement("feature"), cancelThenBlock)
	if err != nil || rec.Status != runstore.StatusCancelled || rec.Outcome != runstore.OutcomeNone || !strings.Contains(rec.Reason, "was closed during the run") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(onlyPR(t, h.provider).Comments) != 0 {
		t.Fatal("something was posted on a closed PR")
	}
}
