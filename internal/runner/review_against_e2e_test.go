package runner

// RA10 of docs/review-against-task: the check proved end to end. Every other
// test of this plan pins one piece — the launcher's block (RA3), the record
// (RA5b), the issue's scope (RA6a), the fix brief (RA6b), the policy's two
// homes (RA8a) — and each of them builds the run it needs by hand. This file
// builds the run the way a review job really meets one: a clone the fake forge
// answers for, one pull request resolved out of it, the review stage driven
// with that run's own environment, and whatever the run then leaves behind read
// back out of its store.
//
// So the parts meet here and nowhere else: a reviewer's answer that says the
// task is not done has to survive the artifact writer, and the fix brief the
// launcher builds from that answer has to carry the finding back to the coder
// as an unmet one. A piece that lands with a shape its neighbour does not read
// — the record RA5b fills and RA7's reader reads, the scope RA6a validates and
// RA6b's brief names — fails here rather than in front of a person.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The brief a review of a run of this test's own is checked against, and the
// pull request it was written for.
const (
	reviewAgainstPR     = 7
	reviewAgainstBranch = "fugaro/" + reviewRootRun
	reviewAgainstBrief  = "add a retry budget to the fetch path and cover the refusal arm"
)

// reviewAgainstStage is the reviewer: it keeps the prompt it was handed and
// answers with the one structured answer a test planted, which is what the
// review job's stage returns.
type reviewAgainstStage struct {
	answer agent.Result
	prompt string
}

func (a *reviewAgainstStage) Run(_ context.Context, req agent.Request) (agent.Result, error) {
	a.prompt = req.Prompt
	return a.answer, nil
}

// reviewAgainstTask is the block a launcher writes for a review of the run
// whose pull request this is: the brief, and the run id its own record names.
func reviewAgainstTask() *task.ReviewSpec {
	return &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: reviewAgainstBrief}
}

// reviewAgainstHandoff is the pull request's own commit: a change on a branch
// of its own, pushed to the remote the run clones from, so the provider
// resolves its tip the way a forge does rather than being told a hash.
func reviewAgainstHandoff(t *testing.T, remote string) string {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{
		"src/fetch.go": "package src\n\nfunc Fetch() error {\n\treturn tryFetch()\n}\n",
	})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "fetch it with a retry")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/"+reviewAgainstBranch)
	return testutil.Git(t, seed, "rev-parse", "HEAD")
}

// reviewAgainstRun is the review job as reviewMode meets it, end to end: the
// job's own environment marker, one pull request the fake forge answers for on
// a real branch, a clone of that repository, a store to write the artifact
// into, the run's own clock and budget, and a reviewer that answers with what
// the test planted. It is not the whole run — bootstrap is what resolves the
// policy, fetches the base and reads the task — so the fields reviewMode's own
// path reaches are the ones a test fills, and edit is where a test plants the
// rest (the base configuration, the published node).
func reviewAgainstRun(t *testing.T, spec *task.ReviewSpec, verdict string, issues []ReviewIssue, edit func(*run)) (*run, *reviewAgainstStage, string) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{
		"src/fetch.go": "package src\n\nfunc Fetch() error {\n\treturn nil\n}\n",
		"README.md":    "the sample repository\n",
	})
	work := filepath.Join(t.TempDir(), "work")
	repo, err := gitops.OpenOrClone(context.Background(), work, remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	head := reviewAgainstHandoff(t, remote)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	svc := newReviewService(t)
	setReviewSigner(t, svc.signer)
	stage := &reviewAgainstStage{answer: reviewResult(t, reviewAnswerOf(verdict, "the change is the one the brief asks for", issues...))}
	provider := &fake.Provider{Repo: "acme/app", Remote: remote}
	provider.State.PRs = []fake.PRState{{
		PR:   gitprov.PR{Number: reviewAgainstPR},
		Spec: gitprov.PRSpec{Branch: reviewAgainstBranch, Base: "main"},
	}}
	state, managed, tmp, cache := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Now = func() time.Time { return now }
		r.d.Store = runstore.Open(bucket, svcSlug, svcRunID)
		r.d.StateDir, r.d.WorkDir = state, work
		r.d.ManagedSettingsPath = filepath.Join(managed, "managed-settings.json")
		r.d.Agent = stage
		r.d.Env = []string{
			"HOME=" + state,
			"TMPDIR=" + tmp,
			"GOCACHE=" + cache,
			EnvReviewJob + "=1",
		}
		r.env = r.d.Env
		r.stageN = map[string]int{}
		r.haltDone = make(chan struct{})
		r.budget = Budget{Start: now, Total: time.Hour, Reserve: time.Minute, Stage: time.Minute, Now: func() time.Time { return now }}
		r.setLevelModel(policy.LevelL2, "claude-opus-5-5")
		r.levels = config.Levels{L2: config.ResolvedLevel{On: true, Model: "claude-opus-5-5", Effort: "high"}}
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Stage: "review"}
		r.d.OpenProvider = gitprov.Static(provider)
		r.provider, r.repo, r.review = provider, repo, true
		r.providerKind = gitprov.KindGitHub
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app",
			Task: "review the pull request", OAuthToken: "team-a", ReviewPRs: []int{reviewAgainstPR}}
		if spec != nil {
			r.spec.ReviewSpec = spec
		}
		if edit != nil {
			edit(r)
		}
		r.readReviewSpec(context.Background())
	})
	return r, stage, head
}

// reviewAgainstDrives runs the two steps a launched review job's own bootstrap
// takes before its stage: resolve the pull request out of the provider, bring
// the base it targets into the checkout, and then review it.
func reviewAgainstDrives(t *testing.T, r *run) {
	t.Helper()
	ctx := context.Background()
	if err := r.checkReviewPRs(ctx); err != nil {
		t.Fatalf("resolving the pull request the run reviews: %v", err)
	}
	if err := r.repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	r.reviewMode(ctx)
}

// reviewAgainstArtifact reads the artifact the run wrote back out of its own
// store, the way a coordinator reads it.
func reviewAgainstArtifact(t *testing.T, r *run) ReviewArtifact {
	t.Helper()
	art, err := DecodeReviewArtifact(reviewObject(t, r, ReviewArtifactName))
	if err != nil {
		t.Fatalf("the artifact this run wrote does not read back: %v", err)
	}
	return art
}

// reviewAgainstIssue is the finding the reviewer answers with: the retry budget
// the brief asks for is not in the change.
func reviewAgainstIssue() ReviewIssue {
	return ReviewIssue{
		Severity: "major", Class: "correctness", Scope: scopeUnmet,
		File: "src/fetch.go", Line: 4,
		Summary:  "the fetch path still makes one attempt, so the retry budget the task asks for is not there",
		Fix:      "wrap the call in the retry budget the task names and cover the arm that gives up",
		Evidence: "src/fetch.go:4 reads `return tryFetch()` with no budget around it",
	}
}

// TestReviewAgainstTaskEndToEnd is the plan's red step once every piece is in:
// a review job launched on a pull request a run opened is checked against that
// run's task from the prompt to the artifact and on to the fix brief.
//
// The prompt carries the brief as quoted data, the artifact records the run it
// came from and the digest of the text the reviewer was shown, the reviewer's
// own `fix first` stands beside the unmet finding, and the fix brief the
// launcher builds from that artifact lists the finding as unmet — which is the
// one sentence a coder needs to do what the task asked rather than what the
// diff does.
func TestReviewAgainstTaskEndToEnd(t *testing.T) {
	r, stage, head := reviewAgainstRun(t, reviewAgainstTask(), "fix first", []ReviewIssue{reviewAgainstIssue()}, nil)
	reviewAgainstDrives(t, r)

	if !strings.Contains(stage.prompt, reviewAgainstBrief) {
		t.Fatalf("the review job's prompt does not carry the task the pull request was written for:\n%s", stage.prompt)
	}
	if !strings.Contains(stage.prompt, "This pull request was written for the task below.") {
		t.Errorf("the prompt does not say what the text below it is (R3):\n%s", stage.prompt)
	}
	nonce := promptTaskNonce(t, stage.prompt)
	if prose := outsideFences(stage.prompt, nonce); strings.Contains(prose, reviewAgainstBrief) {
		t.Errorf("the task reaches the prompt's own prose, outside its fence:\n%s", prose)
	}
	if !strings.Contains(stage.prompt, "\n"+taskSpecIndent+reviewAgainstBrief+"\n") {
		t.Errorf("the task is not quoted as indented data:\n%s", stage.prompt)
	}

	art := reviewAgainstArtifact(t, r)
	if art.Against == nil {
		t.Fatal("the artifact records nothing of what the review was checked against")
	}
	if art.Against.Source != runstore.AgainstSourceRun || art.Against.Run != reviewRootRun {
		t.Errorf("against = %+v, want the run %s", art.Against, reviewRootRun)
	}
	if art.Against.SHA256 != digestOf(reviewAgainstBrief) {
		t.Errorf("against.sha256 = %q, want the digest of the brief the reviewer was shown (%q)", art.Against.SHA256, digestOf(reviewAgainstBrief))
	}
	if err := art.Against.Validate(); err != nil {
		t.Errorf("the record the artifact carries does not hold its own shape: %v", err)
	}
	if art.Verdict != "fix first" {
		t.Errorf("the artifact's verdict is %q, want the reviewer's own fix first", art.Verdict)
	}
	if art.HeadSHA != head {
		t.Errorf("the artifact's head is %q, want the commit the review read (%q)", art.HeadSHA, head)
	}
	if r.rec.Status != runstore.StatusSucceeded || r.rec.Outcome != runstore.OutcomeReviewed {
		t.Errorf("the run ended %s/%s (reason %q), want a review that was written", r.rec.Status, r.rec.Outcome, r.rec.Reason)
	}

	brief := FixFindings(art.Against.Run, art.Issues)
	if !strings.Contains(brief, "- `unmet` — the task asks for something this change does not do") {
		t.Errorf("the fix brief does not say what the word beside the finding is:\n%s", brief)
	}
	if !strings.Contains(brief, "[major] [unmet] correctness") {
		t.Errorf("the fix brief does not list the finding as an unmet one:\n%s", brief)
	}
	if !strings.Contains(brief, art.Issues[0].Summary) {
		t.Errorf("the fix brief does not carry the finding's own words:\n%s", brief)
	}
}

// TestReviewAgainstTaskCodeOnlyEndToEnd: --code-only is a review of the code
// alone, and the whole path says so — no task block in the prompt, a record of
// no spec with the flag as its reason, and neither a run nor a digest on it —
// so a reader can tell a review that judged the code alone from one whose task
// was lost (R8, F35).
func TestReviewAgainstTaskCodeOnlyEndToEnd(t *testing.T) {
	spec := &task.ReviewSpec{Source: task.ReviewSpecNone, Reason: "--code-only"}
	r, stage, _ := reviewAgainstRun(t, spec, "ship", nil, nil)
	reviewAgainstDrives(t, r)

	if strings.Contains(stage.prompt, "written for the task below") || strings.Contains(stage.prompt, "<<<fugaro-task-") {
		t.Errorf("a code-only review's prompt carries a task block:\n%s", stage.prompt)
	}

	art := reviewAgainstArtifact(t, r)
	if art.Against == nil {
		t.Fatal("a code-only review records nothing about what it was checked against")
	}
	if art.Against.Source != runstore.AgainstSourceNone || art.Against.Reason != "--code-only" {
		t.Errorf("against = %+v, want source none with the launcher's own reason", art.Against)
	}
	if art.Against.Run != "" || art.Against.SHA256 != "" {
		t.Errorf("a review that quoted nothing records a run or a digest: %+v", art.Against)
	}
	if err := art.Against.Validate(); err != nil {
		t.Errorf("the record the artifact carries does not hold its own shape: %v", err)
	}
}

// TestReviewAgainstTaskShipWithUnmetEndToEnd is the rule the parts have to
// agree on: a reviewer that answers `ship` beside a finding whose own scope
// says the task is not done is refused, on the reader's side and not only in
// the prompt's wording (R6). The refusal ends the run failed with no artifact
// written, so no coordinator ever reads a ship over a change its own review
// says does not do what it was asked to.
func TestReviewAgainstTaskShipWithUnmetEndToEnd(t *testing.T) {
	r, _, _ := reviewAgainstRun(t, reviewAgainstTask(), "ship", []ReviewIssue{reviewAgainstIssue()}, nil)
	reviewAgainstDrives(t, r)

	if r.rec.Status != runstore.StatusFailed || r.rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("the run ended %s/%s, want the refusal to end it failed with no outcome", r.rec.Status, r.rec.Outcome)
	}
	for _, want := range []string{"the verdict is ship", scopeUnmet} {
		if !strings.Contains(r.rec.Reason, want) {
			t.Errorf("the refusal reason %q does not say %q", r.rec.Reason, want)
		}
	}
	if _, err := r.d.Store.ReadFile(context.Background(), runstore.ArtifactPrefix+ReviewArtifactName); err == nil {
		t.Error("a refused review wrote an artifact anyway")
	}
}

// TestReviewAgainstTaskPolicyForcesEndToEnd: the owner's published `true` is a
// gate over the repository's own answer (R7, D2935), and the whole path follows
// it — a repository whose base file turns the check off still gets the task in
// its prompt and the run in its artifact, resolved from the policy the run
// resolved at bootstrap rather than from the file alone.
//
// The node is the one a cascade installation publishes: the switch and the
// allow-list the credentials service holds every call to, beside the gate. A
// node read without them stops at the cascade's own refusal (O2, C1.11) before
// the key under test is ever reached, which is a fact about this fixture's
// `agent.auth: cascade` rather than about the gate.
func TestReviewAgainstTaskPolicyForcesEndToEnd(t *testing.T) {
	on, off := true, false
	node := &projectpolicy.Policy{
		ReviewAgainstTask: &on,
		Budget:            projectpolicy.Budget{AllowedModels: []string{"claude-opus-5-5"}},
		Routing:           &projectpolicy.Routing{Claude: &projectpolicy.ClaudeRoute{Via: config.RouteCascade}},
	}
	r, stage, _ := reviewAgainstRun(t, reviewAgainstTask(), "ship", nil, func(r *run) {
		r.baseCfg = &config.Config{Agent: config.Agent{ReviewAgainstTask: &off}}
		r.projPolicy = policyRead{done: true, policy: node, found: true}
		if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
			t.Fatal(err)
		}
		// The artifact's reviewer block records the level's own model, which the
		// resolution above has just written: the stage's answer is set after it.
		r.setLevelModel(policy.LevelL2, "claude-opus-5-5")
		r.levels = config.Levels{L2: config.ResolvedLevel{On: true, Model: "claude-opus-5-5", Effort: "high"}}
	})
	if !r.policy.ReviewAgainstTaskForced() {
		t.Fatal("the node's true did not reach the effective policy as the forcing value")
	}
	reviewAgainstDrives(t, r)

	if !strings.Contains(stage.prompt, reviewAgainstBrief) {
		t.Fatalf("a published true over a base false left the task out of the prompt:\n%s", stage.prompt)
	}
	art := reviewAgainstArtifact(t, r)
	if art.Against == nil || art.Against.Source != runstore.AgainstSourceRun || art.Against.Run != reviewRootRun {
		t.Fatalf("against = %+v, want the run %s: the check a published true forced must reach the artifact", art.Against, reviewRootRun)
	}
}
