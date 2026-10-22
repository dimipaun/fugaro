package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// integrationBranch is the long-lived base branch of the reported runs:
// a branch cut from main with its own commit, which is what a run started
// from another ref (--ref) works on and what a pull request is retargeted
// at afterwards.
const integrationBranch = "integration-0.9.6"

// addBaseBranch creates name on h's remote, at main's tip plus one commit
// of its own — so that a diff of a run's branch against it is not the same
// range as one against main, which is the whole question these tests ask.
func addBaseBranch(t *testing.T, h *harness, name string) string {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed-base")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	if err := os.WriteFile(filepath.Join(seed, "integration.txt"), []byte("a commit only the base branch has\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "a commit on the base branch")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/"+name)
	return testutil.Git(t, seed, "rev-parse", "HEAD")
}

// reviewPromptOf is the review stage's prompt among the agent's calls: the
// one whose scope line names the diff under review.
func reviewPromptOf(t *testing.T, calls []agent.Request) string {
	t.Helper()
	for _, c := range calls {
		if strings.Contains(c.Prompt, "Review the changes on this branch against") {
			return c.Prompt
		}
	}
	t.Fatalf("no review prompt in %d agent calls", len(calls))
	return ""
}

// followUpPromptOf is the follow-up's implement prompt: the fresh session's,
// which carries the diff stat of the branch against the base.
func followUpPromptOf(t *testing.T, calls []agent.Request) string {
	t.Helper()
	for _, c := range calls {
		if strings.Contains(c.Prompt, "This is a follow-up run on pull request") {
			return c.Prompt
		}
	}
	t.Fatalf("no follow-up prompt in %d agent calls", len(calls))
	return ""
}

// TestFirstRunReviewDiffsAgainstItsRefNotTheConfiguredBase is case (a) of
// the reported bug: a first run started from another branch
// (`fugaro run --ref integration-0.9.6`) on a repository whose fugaro.yaml
// names the default branch. The run's own pull request does target main —
// Fugaro opens it there, and the owner retargets it afterwards — but the
// work was cut from integration-0.9.6, so a diff against origin/main is the
// whole divergence of that branch: 33,000 changed lines in 186 files in run
// 865e, reviewed 165 times, for a review stage of 878 model calls and 8
// sub-agent fan-outs against implement's 32.
//
// Mutation (run, restore): put r.cfg.Git.BaseBranch back in place of
// r.diffBase() at runner.go's reviewPrompt — this test fails, finding
// origin/main in the prompt and the review input's own header.
func TestFirstRunReviewDiffsAgainstItsRefNotTheConfiguredBase(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: integrationBranch, Workflow: "app", Task: "Add a feature"}
	h := newHarness(t, "", spec)
	addBaseBranch(t, h, integrationBranch)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	prompt := reviewPromptOf(t, h.agent.calls)
	if !strings.Contains(prompt, "origin/"+integrationBranch) {
		t.Fatalf("the review prompt does not name the ref the run was cut from:\n%s", prompt)
	}
	if strings.Contains(prompt, "origin/main") {
		t.Fatalf("the review prompt diffs against the configured base, not the run's own ref:\n%s", prompt)
	}
	// The prompt's own scope line is not the only base: the input the
	// runner built for the reviewer (reviewinput.go) is the diff it was
	// handed, and that range has to be the same one.
	if !strings.Contains(prompt, "git diff origin/"+integrationBranch+"...HEAD:") {
		t.Fatalf("the review input is not the diff against the run's own ref:\n%s", prompt)
	}
	// And the pull request itself still targets the configured base, which
	// is what makes the two different values worth telling apart.
	if got := onlyPR(t, h.provider).Spec.Base; got != "main" {
		t.Fatalf("the pull request targets %q, want the configured base main", got)
	}
}

// TestFirstRunFetchesTheBaseItDiffsAgainst: the base of a first run's diff is
// the ref it was cut from (diffBase), and a clone job's clone carries the
// remote's default branch alone (gitops.CloneBlobless fetches
// --single-branch), so a run started from another branch names an
// `origin/<ref>` the checkout does not have. CheckoutNewBranch brings that
// ref in as FETCH_HEAD, never as a remote-tracking ref. Unfetched, the review
// prompt's range resolves to nothing: the reviewer's own `git diff
// origin/<ref>...HEAD` fails with "unknown revision", the input the runner
// builds for it is empty, and the stage can still return a verdict on no diff
// at all.
//
// Mutation (run, restore): drop r.fetchDiffBases from bootstrap — the ref
// below does not resolve in the checkout and the review prompt carries no
// built input.
func TestFirstRunFetchesTheBaseItDiffsAgainst(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: integrationBranch, Workflow: "app", Task: "Add a feature"}
	h := newHarness(t, cloneYAML, spec)
	bloblessRemote(t, h)
	h.cloneJob(t)
	addBaseBranch(t, h, integrationBranch)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	mustReady(t, rec, err)
	// The reviewer's own git reads this ref: it has to be in the checkout,
	// resolved to the branch the run was cut from.
	got := testutil.Git(t, h.deps.WorkDir, "rev-parse", "--verify", "refs/remotes/origin/"+integrationBranch)
	if want := testutil.Git(t, h.remote, "rev-parse", "refs/heads/"+integrationBranch); got != want {
		t.Fatalf("origin/%s is at %s, want the branch's own tip %s", integrationBranch, got, want)
	}
	prompt := reviewPromptOf(t, h.agent.calls)
	if !strings.Contains(prompt, "git diff origin/"+integrationBranch+"...HEAD:") {
		t.Fatalf("the review stage was handed no diff against the ref the run was cut from:\n%s", prompt)
	}
}

// TestReviewModeFetchesTheReviewedPRsBase: review mode's diff base is the base
// the reviewed pull request targets now, which is neither the run's own --ref
// (main, whose configuration the run is read from) nor the remote's default
// branch — and a clone job's --single-branch clone holds the default branch
// only. Unfetched, every one of review mode's per-head prefetches fails as a
// warning, the input build fails with it, and the reviewer's diff command
// names a ref that does not exist. The non-clone case is covered by
// TestReviewModeDiffsAgainstTheReviewedPRsBase, where a full clone already has
// every branch and a missing fetch never shows.
//
// Mutation (run, restore): drop r.fetchDiffBases from bootstrap — the ref
// below does not resolve and the review prompt carries no built input.
func TestReviewModeFetchesTheReviewedPRsBase(t *testing.T) {
	h := newHarness(t, cloneYAML, reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	bloblessRemote(t, h)
	h.cloneJob(t)
	addBaseBranch(t, h, integrationBranch)
	pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "someone-elses-change", "")
	// The pull request targets the integration branch: the fake's own state
	// is in memory here (no Path), so it is set on the provider directly.
	h.provider.State.PRs[0].Spec.Base = integrationBranch
	rec, err := h.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v", rec)
	}
	got := testutil.Git(t, h.deps.WorkDir, "rev-parse", "--verify", "refs/remotes/origin/"+integrationBranch)
	if want := testutil.Git(t, h.remote, "rev-parse", "refs/heads/"+integrationBranch); got != want {
		t.Fatalf("origin/%s is at %s, want the branch's own tip %s", integrationBranch, got, want)
	}
	prompt := reviewPromptOf(t, h.agent.calls)
	if !strings.Contains(prompt, "git diff origin/"+integrationBranch+"...HEAD:") {
		t.Fatalf("the reviewed pull request's base was not fetched: the review stage was handed no diff against it:\n%s", prompt)
	}
}

// TestFollowUpReviewDiffsAgainstThePRsCurrentBase is case (b): the pull
// request was retargeted (main -> integration-0.9.6) after the first run,
// and the follow-up's own ref is the previous run's base — main. The diff
// the reviewer is handed must be the branch the pull request targets NOW,
// read from the provider at bootstrap.
//
// Mutation (run, restore): make diffBase return r.spec.Ref (or
// r.cfg.Git.BaseBranch) for a follow-up — this test fails on origin/main.
func TestFollowUpReviewDiffsAgainstThePRsCurrentBase(t *testing.T) {
	h := followUpHarness(t, "", func(h *harness) { addBaseBranch(t, h, integrationBranch) })
	h.editState(t, func(st *fake.State) { st.PRs[0].Spec.Base = integrationBranch })
	h.followUp(t, followID, runID, "Please continue.")
	rec, err := h.run(t, implement("more"), review("ship", 0))
	mustReady(t, rec, err)
	prompt := reviewPromptOf(t, h.agent.calls)
	if !strings.Contains(prompt, "origin/"+integrationBranch) {
		t.Fatalf("the follow-up's review prompt does not name the pull request's current base:\n%s", prompt)
	}
	if strings.Contains(prompt, "origin/main") {
		t.Fatalf("the follow-up's review prompt diffs against the stale base:\n%s", prompt)
	}
	if !strings.Contains(prompt, "git diff origin/"+integrationBranch+"...HEAD:") {
		t.Fatalf("the follow-up's review input is not the diff against the pull request's current base:\n%s", prompt)
	}
	// The follow-up's own prompt carries the base too — "which targets X",
	// the diff stat, and the reads it is told to make.
	impl := followUpPromptOf(t, h.agent.calls)
	if !strings.Contains(impl, "which targets "+integrationBranch) {
		t.Fatalf("the follow-up prompt names another base:\n%s", impl)
	}
	if !strings.Contains(impl, "git diff --stat origin/"+integrationBranch+"...HEAD") {
		t.Fatalf("the follow-up's diff stat is not read against the pull request's current base:\n%s", impl)
	}
}

// TestFollowUpOnAMainBasedPRStillDiffsAgainstMain is case (c): a follow-up
// on a pull request nobody retargeted. The provider says main, the run's
// ref is main, and the diff is against main — the retargeting fix must not
// move a pull request that never moved.
//
// Mutation (run, restore): make diffBase read the repository's default
// branch from anywhere but the pull request — this test fails once the two
// differ, which the retargeted case above is.
func TestFollowUpOnAMainBasedPRStillDiffsAgainstMain(t *testing.T) {
	h := followUpHarness(t, "", func(h *harness) { addBaseBranch(t, h, integrationBranch) })
	h.followUp(t, followID, runID, "Please continue.")
	rec, err := h.run(t, implement("more"), review("ship", 0))
	mustReady(t, rec, err)
	prompt := reviewPromptOf(t, h.agent.calls)
	if !strings.Contains(prompt, "origin/main") || strings.Contains(prompt, "origin/"+integrationBranch) {
		t.Fatalf("a pull request still based on main is not reviewed against main:\n%s", prompt)
	}
}

// TestFollowUpPrefetchUsesThePRsCurrentBase: the objects the reviewer's own
// git reads have to be the ones its prompt names. The prefetch is what
// brings them in with the run's credentials (prefetch.go), so for a
// retargeted pull request it must fetch the base branch's side and not main's —
// the ref it would otherwise fetch is one the review no longer reads, and
// origin/<the retargeted base> would be a ref the checkout never had.
//
// Mutation (run, restore): put r.cfg.Git.BaseBranch back into
// prefetchObjects — this test fails, finding origin/main among the bases
// fetched.
func TestFollowUpPrefetchUsesThePRsCurrentBase(t *testing.T) {
	h := followUpHarness(t, cloneYAML, func(h *harness) {
		addBaseBranch(t, h, integrationBranch)
		h.cloneJob(t)
	})
	h.editState(t, func(st *fake.State) { st.PRs[0].Spec.Base = integrationBranch })
	h.followUp(t, followID, runID, "Please continue.")
	var mu sync.Mutex
	bases := map[string]int{}
	runner.SetPrefetchFetch(t, func(_ *gitops.Repo, _ context.Context, _, base, _ string) error {
		mu.Lock()
		bases[base]++
		mu.Unlock()
		return nil
	})
	rec, err := h.run(t, implement("more"), review("ship", 0))
	mustReady(t, rec, err)
	mu.Lock()
	defer mu.Unlock()
	// Three fetches — the diff against the base, the branch's own commits,
	// the base tip — all of them against the pull request's current base.
	if bases["origin/"+integrationBranch] != 3 {
		t.Fatalf("fetches = %v, want 3 against origin/%s", bases, integrationBranch)
	}
	if bases["origin/main"] != 0 {
		t.Fatalf("fetches = %v, want none against the stale base origin/main", bases)
	}
}

// TestReviewModeDiffsAgainstTheReviewedPRsBase: review mode reviews someone
// else's pull request, and reads that pull request's base from the provider
// like a follow-up's. The run's own --ref is what its configuration comes
// from, not necessarily what the reviewed pull request targets.
//
// Mutation (run, restore): return r.refBase() from reviewBase — this test
// fails on origin/main.
func TestReviewModeDiffsAgainstTheReviewedPRsBase(t *testing.T) {
	h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
	addBaseBranch(t, h, integrationBranch)
	pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(h, 7, "someone-elses-change", "")
	// The pull request targets the integration branch: the fake's own state
	// is in memory here (no Path), so it is set on the provider directly.
	h.provider.State.PRs[0].Spec.Base = integrationBranch
	rec, err := h.run(t, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v", rec)
	}
	prompt := reviewPromptOf(t, h.agent.calls)
	if !strings.Contains(prompt, "origin/"+integrationBranch) || strings.Contains(prompt, "origin/main") {
		t.Fatalf("review mode does not diff against the reviewed pull request's base:\n%s", prompt)
	}
}

// hugeChange commits one file of lines lines, so that the diff under review
// is past the runner's own huge-diff threshold (prompts.go).
func hugeChange(lines int) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "seq 1 "+strconv.Itoa(lines)+" > big.txt && git add -A && git commit -qm 'Add a big file'")
		return agent.Result{CostUSD: 1}, nil
	}
}

// TestReviewPromptWarnsAboutAHugeDiff: a diff past the threshold is
// announced at the top of the review prompt, which tells the reviewer to
// stay on the pull request's own files and not to fan sub-agents out across
// the base; a diff under it says nothing (the note is not boilerplate every
// review pays for).
//
// Mutation (run, restore): drop the size check from ReviewPrompt — the huge
// case finds no note. Make Huge() always true — the small case finds one.
func TestReviewPromptWarnsAboutAHugeDiff(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if prompt := reviewPromptOf(t, h.agent.calls); strings.Contains(prompt, "Do NOT fan out sub-agents") {
		t.Fatalf("a small diff is warned about:\n%s", prompt)
	}

	huge := newHarness(t, "", nil)
	if _, err := huge.run(t, hugeChange(hugeDiffLines+1), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	prompt := reviewPromptOf(t, huge.agent.calls)
	if !strings.Contains(prompt, "Do NOT fan out sub-agents") {
		t.Fatalf("a huge diff is not warned about:\n%s", prompt)
	}
	if !strings.Contains(prompt, "git log --name-only origin/main..HEAD") {
		t.Fatalf("the warning does not scope the reviewer to the pull request's own commits:\n%s", prompt)
	}
	if want := strconv.Itoa(hugeDiffLines+1) + " changed line(s)"; !strings.Contains(prompt, want) {
		t.Fatalf("the warning does not say how big the diff is (%s):\n%s", want, prompt)
	}
}

// hugeDiffLines is the review prompt's line threshold, spelled here as the
// test's own fixture; the runner's own copy is pinned by
// diffbase_internal_test.go.
const hugeDiffLines = 8000

// badBases are the values a provider could hand back as a pull request's
// base that must never reach a git command or the reviewer's prompt: option
// look-alikes, shell-ish and revision syntax, whitespace, and nothing at all.
var badBases = map[string]string{
	"upload-pack": "--upload-pack=x",
	"dash":        "-x",
	"backtick":    "a`b",
	"dotdot":      "a..b",
	"newline":     "a\nb",
	"empty":       "",
}

// assertFellBack: the review prompt of a run whose pull request base was
// refused or empty names the run's own fallback (origin/main), never the raw
// value, and says in one line that the base could not be read.
func assertFellBack(t *testing.T, prompt, raw string) {
	t.Helper()
	if raw != "" && strings.Contains(prompt, raw) {
		t.Fatalf("the review prompt carries the refused base %q:\n%s", raw, prompt)
	}
	if !strings.Contains(prompt, "git diff origin/main...HEAD") {
		t.Fatalf("the review prompt does not fall back to the run's own ref:\n%s", prompt)
	}
	if !strings.Contains(prompt, "base branch could not be read") ||
		!strings.Contains(prompt, "git log origin/main..HEAD") {
		t.Fatalf("the review prompt does not say the base could not be read:\n%s", prompt)
	}
}

// TestFollowUpRefusesABadPRBase: a follow-up whose pull request base is not a
// branch name falls back safely and loudly, and a good name (including one
// with a slash) is used and carries no such line.
//
// Mutation (run, restore): make diffBase's `config.ValidBranchName(b)` check
// `if true` — every refused case here finds the raw value in the prompt.
func TestFollowUpRefusesABadPRBase(t *testing.T) {
	for name, raw := range badBases {
		t.Run(name, func(t *testing.T) {
			h := followUpHarness(t, "", nil)
			h.editState(t, func(st *fake.State) { st.PRs[0].Spec.Base = raw })
			h.followUp(t, followID, runID, "Please continue.")
			rec, err := h.run(t, implement("more"), review("ship", 0))
			mustReady(t, rec, err)
			assertFellBack(t, reviewPromptOf(t, h.agent.calls), raw)
			if raw != "" && strings.Contains(followUpPromptOf(t, h.agent.calls), raw) {
				t.Fatalf("the follow-up prompt carries the refused base %q", raw)
			}
		})
	}
	for _, good := range []string{integrationBranch, "feature/x"} {
		t.Run("good "+good, func(t *testing.T) {
			h := followUpHarness(t, "", func(h *harness) { addBaseBranch(t, h, good) })
			h.editState(t, func(st *fake.State) { st.PRs[0].Spec.Base = good })
			h.followUp(t, followID, runID, "Please continue.")
			rec, err := h.run(t, implement("more"), review("ship", 0))
			mustReady(t, rec, err)
			prompt := reviewPromptOf(t, h.agent.calls)
			if !strings.Contains(prompt, "git diff origin/"+good+"...HEAD") || strings.Contains(prompt, "could not be read") {
				t.Fatalf("a valid base %q is not used plainly:\n%s", good, prompt)
			}
		})
	}
}

// TestReviewModeRefusesABadPRBase is the same for review mode's per-head base
// (reviewBase).
//
// Mutation (run, restore): make reviewBase's `config.ValidBranchName(b)`
// check `if true` — every refused case finds the raw value in the prompt.
func TestReviewModeRefusesABadPRBase(t *testing.T) {
	for name, raw := range badBases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
			pushReviewBranch(t, h, "someone-elses-change", func(dir string) {
				testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
			})
			addPR(h, 7, "someone-elses-change", "")
			h.provider.State.PRs[0].Spec.Base = raw
			rec, err := h.run(t, review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
				t.Fatalf("rec = %+v", rec)
			}
			assertFellBack(t, reviewPromptOf(t, h.agent.calls), raw)
		})
	}
}

// twoCommits commits a.txt and b.txt as two commits: the first run of the
// stat test, whose branch head has a parent a second base can sit at.
func twoCommits(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "echo a > a.txt && git add -A && git commit -qm 'Add a' && echo b > b.txt && git add -A && git commit -qm 'Add b'")
	return agent.Result{CostUSD: 1}, nil
}

// TestFollowUpDiffStatIsMeasuredAgainstThePRsBase: the stat itself, not the
// label above it, is taken over the branch the pull request targets. The
// base here sits at the branch's own first commit, so the stat against it
// names b.txt alone, where the stat against main names a.txt and b.txt.
//
// Mutation (run, restore): `base := r.cfg.Git.BaseBranch` in freshContext
// (followup.go) — the stat names a.txt and this test fails.
func TestFollowUpDiffStatIsMeasuredAgainstThePRsBase(t *testing.T) {
	h := followUpHarness(t, "", nil, twoCommits, review("ship", 0))
	seed := filepath.Join(t.TempDir(), "seed-mid")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	testutil.Git(t, seed, "push", "--quiet", "origin", h.first.PushedHead+"~1:refs/heads/"+integrationBranch)
	h.editState(t, func(st *fake.State) { st.PRs[0].Spec.Base = integrationBranch })
	h.followUp(t, followID, runID, "Please continue.")
	rec, err := h.run(t, implement("more"), review("ship", 0))
	mustReady(t, rec, err)
	impl := followUpPromptOf(t, h.agent.calls)
	if !strings.Contains(impl, "b.txt |") {
		t.Fatalf("the stat does not name the file only the branch's own commits changed:\n%s", impl)
	}
	if strings.Contains(impl, "a.txt |") {
		t.Fatalf("the stat is measured against another base (it names a.txt, which the base already has):\n%s", impl)
	}
}
