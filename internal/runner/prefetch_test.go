package runner_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// agentReads are the commands a reviewer runs in a clone job's checkout, and
// the ones a follow-up's or a review run's agent — which has no git
// credentials of its own — can only run if the runner fetched their objects
// first: the prompted three-dot diff (review N1), and reading the pull
// request commit by commit plus the two-dot diff against the base tip
// (review M2). readThenReview fails the test if any of them fails.
func agentReads() []string {
	return []string{
		"git diff origin/main...HEAD",
		"git log -p origin/main..HEAD",
		"git show HEAD",
		"git show HEAD~1",
		"git diff origin/main",
	}
}

// readThenReview is the review stage running commands — the prompt's own
// diff above all — in the agent's environment, which is the allowlist with
// no git credentials of its own, before giving its verdict. shell fails the
// test if a command fails.
func readThenReview(verdict string, commands ...string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for _, command := range commands {
			shell(t, req, command)
		}
		return review(verdict, 0)(t, ctx, req)
	}
}

// advanceMain commits change on top of main in a fresh clone of h's remote
// and pushes it: the base moves past the pull request's merge base, which
// is what makes the base tip's side of a file the branch also changed one
// the checkout of the branch never brings in.
func advanceMain(t *testing.T, h *harness, change func(dir string)) string {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed-main")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	change(seed)
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "someone else's commit on main")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return testutil.Git(t, seed, "rev-parse", "HEAD")
}

// editTwice appends two lines to name in two commits, so the branch carries
// a revision of it that its tip does not: the checkout brings in the tip's
// contents alone, and reading the branch's own commits (`git show HEAD~1`,
// `git log -p`) needs the one in between (review M2).
func editTwice(name string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo one >> "+name+" && git add -A && git commit -qm 'edit "+name+"' && "+
			"echo two >> "+name+" && git add -A && git commit -qm 'edit "+name+" again'")
		return agent.Result{CostUSD: 1}, nil
	}
}

// bloblessRemote makes h's remote a token-gated HTTP one that serves the
// lazy fetches a prefetch asks for: useHTTPRemote's credentials are what the
// agent's environment lacks, so its own fetch is refused exactly as it is on
// a private repository, and the server has to honour the filter (or the
// clone would not be blobless at all) and serve the single objects a lazy
// fetch names.
func bloblessRemote(t *testing.T, h *harness) *testutil.HTTPRemote {
	t.Helper()
	remote := h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	testutil.Git(t, remote.Bare, "config", "uploadpack.allowFilter", "true")
	testutil.Git(t, remote.Bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	return remote
}

// TestCloneReviewPrefetchesTheDiff (review N1, M2): a clone job's checkout
// is blobless, so the file contents it holds are the checked-out revision's
// alone; the branch's diff against the base also needs the base's side of
// every file the branch changed, and reading the branch commit by commit
// (`git log -p`, `git show`, `git diff HEAD~1`) and a two-dot diff against
// the base tip need the revisions and the base tip in between. The agent's
// git has no credentials to fetch any of them with. The runner fetches them
// itself, at bootstrap, while its own git still carries the run's
// credentials, and the objects it brings in are the ones the agent's reads
// then use.
//
// The remote demands the run's token (useHTTPRemote), which is what the
// agent's environment lacks: its lazy fetch is refused, exactly as it is on
// a private repository. The pull request's branch carries two commits, and
// main is moved past the merge base first, so the objects the reads want —
// the branch's own earlier revision, and the base tip's — are genuinely
// ones no checkout brought in.
//
// Mutation (run, restore): make r.prefetchObjects return without fetching —
// the review stage's first read fails with "could not fetch <oid> from
// promisor remote", which is the reported bug. Dropping either
// PrefetchCommits or PrefetchBaseDiff from it leaves the reads they serve
// failing here too.
func TestCloneReviewPrefetchesTheDiff(t *testing.T) {
	readme := testutil.FixtureFiles(t)["README.md"]
	h := newHarness(t, cloneYAML, reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	bloblessRemote(t, h)
	h.cloneJob(t)
	sha := pushReviewBranch(t, h, "someone-elses-change",
		func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"README.md": readme + "\ntheir first change\n"})
		},
		func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"README.md": readme + "\ntheir first change\n\ntheir second change\n"})
		})
	advanceMain(t, h, func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"README.md": readme + "\nthe base moved\n"})
	})
	addPR(h, 7, "someone-elses-change", "")

	rec, err := h.run(t, readThenReview("ship", agentReads()...))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v", rec)
	}
	if len(rec.Reviewed) != 1 || rec.Reviewed[0].SHA != sha {
		t.Fatalf("reviewed = %+v, want the pull request's head %s", rec.Reviewed, sha)
	}
}

// TestCloneFollowUpPrefetchesTheDiff (review M1): the follow-up path is the
// one the round-2 finding named first ("every follow-up's reviewer and
// fixer"), and it is the one a fresh session's diff stat can mask — that
// stat runs with the runner's own credentials, so the three-dot diff's
// objects are fetched for it before any prefetch happens. What it does not
// fetch is the branch's own earlier revisions or the base tip, so those are
// what this test reads, in the agent's environment, on a follow-up whose
// pull request has two commits and whose base has moved on.
//
// Mutation (run, restore): make prefetchObjects skip follow-ups
// (`r.follow != nil`) — git show HEAD~1 fails with "could not fetch <oid>
// from promisor remote", which is the bug on the path round 2 named.
func TestCloneFollowUpPrefetchesTheDiff(t *testing.T) {
	h := newHarness(t, cloneYAML, nil)
	bloblessRemote(t, h)
	h.provider.SelfID = fugaroSelf
	statePath := filepath.Join(t.TempDir(), "fake.json")
	h.provider.Path = statePath
	h.cloneJob(t)
	first, err := h.run(t, editTwice("feature.txt"), review("ship", 0))
	if err != nil || first.PushedHead == "" || first.PR == nil || first.PR.Number != 1 {
		t.Fatalf("first run: rec = %+v, err = %v", first, err)
	}
	advanceMain(t, h, func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"base.txt": "the base moved\n"})
	})
	fu := &fuHarness{harness: h, statePath: statePath, first: first}
	fu.followUp(t, followID, runID, "Please continue.")
	rec, err := fu.run(t, implement("more"), readThenReview("ship", agentReads()...))
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
}

// TestCloneReviewPrefetchIsBounded: a prefetch fetch that never answers is
// cut off at prefetchBound and reported as a warning, never as a bootstrap
// failure: the run goes on, reviews, and succeeds. Without a bound, a
// promisor remote that accepts the connection and then stalls would hold
// the run at bootstrap until its own total timeout, with no stage started
// and nothing written.
//
// Mutation (run, restore): make fetchWithin call fetch(ctx) without the
// WithTimeout — this test then blocks in the stub until the run's own
// context ends, and times out instead of finding its three warnings.
func TestCloneReviewPrefetchIsBounded(t *testing.T) {
	h := newHarness(t, cloneYAML, reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	h.cloneJob(t)
	pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"README.md": "their change\n"})
	})
	addPR(h, 7, "someone-elses-change", "")
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	runner.SetPrefetchBound(t, 50*time.Millisecond)
	runner.SetPrefetchFetch(t, func(_ *gitops.Repo, ctx context.Context, _, _, _ string) error {
		<-ctx.Done() // a blackholed remote: the request is never answered
		return ctx.Err()
	})
	rec, err := h.run(t, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v, err = %v: a stuck prefetch must not fail the run", rec, err)
	}
	if n := strings.Count(logs.String(), "prefetching what the agent reads failed"); n != 3 {
		t.Fatalf("%d prefetch warnings, want 3 (one per fetch, each cut off):\n%s", n, logs.String())
	}
	if s := logs.String(); !strings.Contains(s, "context deadline exceeded") {
		t.Fatalf("the warnings do not say the fetch ran out of time:\n%s", s)
	}
}

// TestCloneReviewPrefetchTriesEveryPR (review L2): a pick run reviews
// several pull requests, and prefetchObjects fetches what each head's reads
// need. A head whose fetch fails must not cost the later heads theirs: the
// loop warns and carries on, so every head is attempted and each pull
// request gets the same chance.
//
// Mutation (run, restore): break out of the per-head loop when a fetch
// fails (or return from prefetchObjects) — the second head is never
// attempted, and the count per head fails.
func TestCloneReviewPrefetchTriesEveryPR(t *testing.T) {
	h := newHarness(t, cloneYAML, reviewTaskSpec("pick", pickYAML, 7, 9))
	h.cloneJob(t)
	first := pushReviewBranch(t, h, "a", func(dir string) { testutil.WriteFiles(t, dir, map[string]string{"a.txt": "x\n"}) })
	second := pushReviewBranch(t, h, "b", func(dir string) { testutil.WriteFiles(t, dir, map[string]string{"b.txt": "x\n"}) })
	addPR(h, 7, "a", "")
	addPR(h, 9, "b", "")
	logs := &lockedBuf{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	var mu sync.Mutex
	heads := map[string]int{}
	runner.SetPrefetchFetch(t, func(_ *gitops.Repo, _ context.Context, _, _, head string) error {
		mu.Lock()
		heads[head]++
		mu.Unlock()
		if head == first {
			return errors.New("could not fetch from promisor remote")
		}
		return nil
	})
	rec, err := h.run(t, pickVerdict(9))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	mu.Lock()
	defer mu.Unlock()
	// Three fetches per head, the first head's all failing and the second
	// head's running anyway.
	if heads[first] != 3 || heads[second] != 3 {
		t.Fatalf("fetches per head = %v, want 3 each (%s, %s)", heads, first, second)
	}
	if n := strings.Count(logs.String(), "prefetching what the agent reads failed"); n != 3 {
		t.Fatalf("%d warnings, want the first head's three:\n%s", n, logs.String())
	}
}
