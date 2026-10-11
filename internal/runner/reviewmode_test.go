package runner_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// reviewOnlyYAML and pickYAML are the catalog's review-only and pick
// recipes, inlined so a test can tell the YAML it embeds apart from
// whatever the catalog happens to say (recipe.CatalogText("review-only")
// covers that the catalog's own text parses and is launchable,
// cli/recipes_test.go).
const (
	reviewOnlyYAML = "version: 1\nname: reviewonly\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"
	pickYAML       = "version: 1\nname: pick\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"
)

// reviewTaskSpec is a review-mode first run: no branch, no previous run,
// prs pull requests to review.
func reviewTaskSpec(name, yaml string, prs ...int) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Recipe: embeddedTask(name, yaml), ReviewPRs: prs}
}

// allowForksCfg is the fixture's fugaro.yaml with review.allow_forks: true.
func allowForksCfg(t *testing.T) string {
	t.Helper()
	return testutil.FixtureFiles(t)["fugaro.yaml"] + "review:\n  allow_forks: true\n"
}

// pushBranch commits change in a fresh clone of h's remote and pushes it to
// branch: review mode's pull requests are never the run's own branch, so
// this never touches main or a fugaro/<run-id> branch.
func pushBranch(t *testing.T, h *harness, branch string, change func(dir string)) string {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed-"+branch)
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	change(seed)
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "someone else's change")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return testutil.Git(t, seed, "rev-parse", "HEAD")
}

// addPR registers number as an open pull request on branch in h's fake
// provider, without ever calling EnsurePR: review mode reviews a pull
// request it never opened.
func addPR(h *harness, number int, branch, source string) {
	h.provider.State.PRs = append(h.provider.State.PRs, fake.PRState{
		PR:     gitprov.PR{Number: number, URL: fmt.Sprintf("https://example.invalid/pr/%d", number)},
		Spec:   gitprov.PRSpec{Branch: branch},
		Source: source,
	})
}

// TestReviewModeNeverPushes: a review-only run on someone else's pull
// request posts one comment, never pushes a branch of its own, and ends
// reviewed.
//
// Mutation (run, restore): make reviewMode call r.ensurePR or
// r.repo.Push, and this test fails: a branch is pushed or a PR is
// recorded.
func TestReviewModeNeverPushes(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	pushBranch(t, h, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "someone-elses-change", "")
	rec, err := h.run(t, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v", rec)
	}
	if hasBranch(t, h.remote, "fugaro/"+runID) {
		t.Fatal("review mode pushed a branch of its own")
	}
	for _, c := range h.provider.State.Calls {
		if c.Op == "EnsurePR" || c.Op == "UpdatePR" {
			t.Fatalf("review mode wrote to the pull request: %+v", c)
		}
	}
	c := h.provider.State.PRs[0].Comments
	if len(c) != 1 || !strings.Contains(c[0], "Fugaro review (reviewonly): changes") {
		t.Fatalf("comments = %q", c)
	}
	if len(rec.Reviewed) != 1 || rec.Reviewed[0] != (runstore.ReviewedPR{Number: 7, SHA: h.provider.State.PRs[0].Head, Verdict: "changes"}) {
		t.Fatalf("reviewed = %+v", rec.Reviewed)
	}
}

// TestReviewModeAgentHasNoGitToken: the review stage's agent gets no git
// credentials and no GH_TOKEN (design generic-tool §3.6, G12), exactly as
// a follow-up's agent has none (runner.go's authVars and refreshGitAuth
// blocks).
//
// Mutation (drop the `&& !r.review` guard runner.go's agentAuthVars block
// has): this test fails, finding GH_TOKEN, FUGARO_GIT_TOKEN or
// GIT_CONFIG_COUNT in the agent's environment.
func TestReviewModeAgentHasNoGitToken(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	h.provider.Auth = func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: "x-access-token", Token: "ghs-fixture", Env: map[string]string{"GH_TOKEN": "ghs-fixture"}}
	}
	pushBranch(t, h, "feature", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "feature", "")
	var envAtReview []string
	capture := func(t *testing.T, ctx testingContext, req agent.Request) (agent.Result, error) {
		envAtReview = req.Env
		return review("ship", 0)(t, ctx, req)
	}
	if _, err := h.run(t, capture); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"GH_TOKEN", "FUGARO_GIT_TOKEN", "GIT_CONFIG_COUNT"} {
		if envValue(envAtReview, k) != "" {
			t.Errorf("review-mode agent has %s", k)
		}
	}
}

// TestReviewModeRefusesForkWithoutOptIn: a pull request whose head is in a
// fork is refused unless the base branch's fugaro.yaml sets
// review.allow_forks: true (design generic-tool §3.6, G12); with it set,
// the same fork is reviewed as any other pull request.
//
// Mutation (drop the `h.Fork && !r.cfg.Review.AllowForks` check in
// checkReviewPRs): the first half fails, finding the run succeeded instead
// of refused.
func TestReviewModeRefusesForkWithoutOptIn(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	pushBranch(t, h, "feature", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "feature", "outsider/repo")
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "review.allow_forks") || len(stageNames(rec)) != 0 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}

	h2 := newHarness(t, allowForksCfg(t), reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	pushBranch(t, h2, "feature", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h2, 7, "feature", "outsider/repo")
	rec2, err := h2.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("allow_forks did not allow it: rec = %+v", rec2)
	}
}

// TestReviewModeLeavesDraftState: review mode never changes a pull
// request's draft state (design generic-tool §3.6, G12: "mode: review
// never changes a PR's draft state").
//
// Mutation (call r.provider.UpdatePR, or EnsurePR with spec.Number, from
// reviewMode): this test fails, finding an UpdatePR/EnsurePR call.
func TestReviewModeLeavesDraftState(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	pushBranch(t, h, "feature", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "feature", "")
	h.provider.State.PRs[0].Draft = true
	if _, err := h.run(t, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !h.provider.State.PRs[0].Draft {
		t.Fatal("review mode changed the pull request's draft state")
	}
	for _, c := range h.provider.State.Calls {
		if c.Op == "UpdatePR" || c.Op == "EnsurePR" {
			t.Fatalf("review mode wrote to the pull request: %+v", c)
		}
	}
}

// pickVerdict is a review step whose structured output names keep, for a
// pick run's "keep #N" comment.
func pickVerdict(keep int) step {
	return func(t *testing.T, ctx testingContext, req agent.Request) (agent.Result, error) {
		b, _ := json.Marshal(map[string]any{"verdict": "ship", "findings": []map[string]string{}, "keep": keep})
		return agent.Result{Structured: b, CostUSD: 0.5}, nil
	}
}

// TestPickCommentsOnEveryPR: a pick run comments on every pull request it
// compares, each naming the same one to keep.
//
// Mutation (post a different body per PR instead of the same one): the
// second PR's comment check fails.
func TestPickCommentsOnEveryPR(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("pick", pickYAML, 7, 9))
	pushBranch(t, h, "a", func(dir string) { testutil.WriteFiles(t, dir, map[string]string{"a.txt": "x\n"}) })
	pushBranch(t, h, "b", func(dir string) { testutil.WriteFiles(t, dir, map[string]string{"b.txt": "x\n"}) })
	addPR(h, 7, "a", "")
	addPR(h, 9, "b", "")
	rec, err := h.run(t, pickVerdict(9))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v", rec)
	}
	for _, n := range []int{7, 9} {
		var c []string
		for i := range h.provider.State.PRs {
			if h.provider.State.PRs[i].Number == n {
				c = h.provider.State.PRs[i].Comments
			}
		}
		if len(c) != 1 || !strings.Contains(c[0], "keep #9") {
			t.Fatalf("PR %d comments = %q", n, c)
		}
	}
}
