package runner

// RA3 of docs/review-against-task: the review job reads the spec it was handed
// (reviewmode.go's prompt and comment, and the `against` reviewartifact.go
// fills from RA1's record).
//
// The spec a launched review is checked against is the launcher's own block
// (R2, D2934): this run reads no other run for it, so every test here builds a
// run whose task carries a review_spec, reads it the way bootstrap reads it
// (readReviewSpec), and then asks the two things RA3 adds — the prompt the
// reviewer is given, and the record the review leaves behind. The plan section
// is read from a real checkout, because what D2932 is about is which revision
// the section comes from: the commit the brief pins, or the launch ref, and
// never the base the pull request targets now.

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"log/slog"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// reviewPromptRun is a review run as its prompt reader meets it: one pull
// request already resolved (checkReviewPRs' own answer), the configuration its
// launch base resolved, and the launcher's block read the way bootstrap reads
// it (readReviewSpec, R16). job marks it the independent review job. files
// gives the checkout a remote when a test needs one.
func reviewPromptRun(t *testing.T, files map[string]string, job bool, edit func(*run)) *run {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: reviewNextRun, Repo: "acme/app", Ref: "main", ReviewPRs: []int{7}}
	r, _ := reviewSpecRun(t, spec, files)
	r.review = true
	if job {
		r.d.Env = []string{EnvReviewJob + "=1"}
	}
	r.reviewHeads = []reviewHead{{Number: 7, SHA: reviewJobHead, Base: "main"}}
	r.cfg = &config.Config{Git: config.Git{BaseBranch: "main"}}
	if edit != nil {
		edit(r)
	}
	r.readReviewSpec(context.Background())
	return r
}

// reviewRunCloned is reviewSpecRun for a remote the test itself also pushed a
// branch to: a run with a store of its own and a checkout cloned from remote.
func reviewRunCloned(t *testing.T, spec *task.Spec, remote string) *run {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	work := t.TempDir()
	r := &run{
		d: Deps{
			Store:   runstore.Open(bucket, "acme-app", spec.RunID),
			WorkDir: work,
			Log:     slog.New(slog.DiscardHandler),
		},
		spec: spec,
	}
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(work, "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	r.repo = repo
	return r
}

// taskBlockNonceRE is the one shape taskSpecBlock's fence can take: the nonce
// is drawn per render, so a test reads it back out of the prompt rather than
// predicting it, and the first match is the block's real opener — a text that
// spells a fence of its own is neutralized inside the fence, not before it.
var taskBlockNonceRE = regexp.MustCompile(`<<<fugaro-task-([0-9a-f]{16})>>>`)

// promptTaskNonce is the nonce the prompt's own task block is fenced with.
func promptTaskNonce(t *testing.T, prompt string) string {
	t.Helper()
	m := taskBlockNonceRE.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("the prompt carries no task block:\n%s", prompt)
	}
	return m[1]
}

// TestReviewJobSpecInPrompt: the job's prompt carries the brief the launcher
// handed in, quoted as data — indented inside a nonce fence, with the R3
// sentence that says what it is — and a repository whose base file turns the
// check off gets no block at all (R7).
//
// Red step: this fails while the job prompt lacks the spec.
func TestReviewJobSpecInPrompt(t *testing.T) {
	ctx := context.Background()
	const brief = "add a retry budget to the fetch path and cover the refusal arm"
	r := reviewPromptRun(t, nil, true, func(r *run) {
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: brief}
	})
	prompt := r.reviewModePrompt(ctx)
	if !strings.Contains(prompt, brief) {
		t.Fatalf("the review job's prompt does not carry the brief the launcher handed in:\n%s", prompt)
	}
	if !strings.Contains(prompt, "This pull request was written for the task below.") {
		t.Errorf("the prompt does not say what the text below it is (R3):\n%s", prompt)
	}
	nonce := promptTaskNonce(t, prompt)
	if prose := outsideFences(prompt, nonce); strings.Contains(prose, brief) {
		t.Errorf("the brief reaches the prompt's own prose, outside its fence:\n%s", prose)
	}
	if !strings.Contains(prompt, "\n"+taskSpecIndent+brief+"\n") {
		t.Errorf("the brief is not quoted as indented data:\n%s", prompt)
	}

	off := false
	r = reviewPromptRun(t, nil, true, func(r *run) {
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: brief}
		r.baseCfg = &config.Config{Agent: config.Agent{ReviewAgainstTask: &off}}
	})
	if p := r.reviewModePrompt(ctx); strings.Contains(p, brief) || strings.Contains(p, "written for the task below") {
		t.Errorf("a repository whose base file turns the check off still had the brief quoted:\n%s", p)
	}
}

// TestReviewJobSpecPlanSectionNotFromPRBase: the section of the plan the brief
// names is read at the launch ref, never at the base the pull request targets
// now — which whoever can edit the pull request can retarget to a branch they
// pushed, and so write the brief the reviewer judges against (D2932, F31).
//
// Red step: this fails while a retargeted base's plan is quoted instead.
func TestReviewJobSpecPlanSectionNotFromPRBase(t *testing.T) {
	ctx := context.Background()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, planFixtureFiles())
	// The branch the pull request was retargeted at carries a section of its own,
	// pushed to the same remote the run clones from.
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{
		planFixtureDocPath: strings.Replace(planFixtureDoc, "Read the thing and write it down.", "What the retargeted base says the task is.", 1),
	})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "the branch the pull request was retargeted at")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/integration")

	spec := &task.Spec{Version: 1, RunID: reviewNextRun, Repo: "acme/app", Ref: "main", ReviewPRs: []int{7}}
	r := reviewRunCloned(t, spec, remote)
	r.review = true
	r.d.Env = []string{EnvReviewJob + "=1"}
	r.reviewHeads = []reviewHead{{Number: 7, SHA: reviewJobHead, Base: "integration"}}
	r.cfg = &config.Config{Git: config.Git{BaseBranch: "main"}}
	// fetchDiffBases brings the base the pull request targets now into the
	// checkout, so the retargeted branch is one this run really can read.
	if err := r.repo.FetchBase(ctx, "integration"); err != nil {
		t.Fatal(err)
	}
	r.spec.ReviewSpec = &task.ReviewSpec{
		Source: task.ReviewSpecRun, Run: reviewRootRun,
		Text:     "a brief a person wrote",
		PlanTask: planFixtureID + "/G1",
	}
	r.readReviewSpec(ctx)

	prompt := r.reviewModePrompt(ctx)
	if !strings.Contains(prompt, "Read the thing and write it down.") {
		t.Fatalf("the plan section was not read at the launch ref:\n%s", prompt)
	}
	if strings.Contains(prompt, "What the retargeted base says the task is.") {
		t.Fatalf("the plan was read from the base the pull request targets now:\n%s", prompt)
	}
}

// TestReviewJobSpecAndLauncherTextSeparate: the launcher's TEXT and the spec
// are two different claims — what the change is judged against, and an
// instruction to this review — so each keeps its own block, and the TEXT is
// never quoted as part of the brief (D2934).
func TestReviewJobSpecAndLauncherTextSeparate(t *testing.T) {
	const brief = "add the retry budget to the fetch path"
	const text = "focus on the auth changes"
	r := reviewPromptRun(t, nil, true, func(r *run) {
		r.spec.Task = text
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: brief}
	})
	prompt := r.reviewModePrompt(context.Background())
	nonce := promptTaskNonce(t, prompt)
	if !strings.Contains(prompt, brief) {
		t.Fatalf("the job's prompt does not carry the brief:\n%s", prompt)
	}
	if strings.Contains(prompt, "Additional instructions from the launcher:\n\n"+brief) {
		t.Errorf("the brief was spliced in as the launcher's instructions:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Additional instructions from the launcher:\n\n"+text) {
		t.Errorf("the launcher's own TEXT is not its own block:\n%s", prompt)
	}
	closeTag := "<<<end-fugaro-task-" + nonce + ">>>"
	if at, end := strings.Index(prompt, text), strings.Index(prompt, closeTag); end < 0 || at < end {
		t.Errorf("the launcher's TEXT is quoted inside the brief's fence:\n%s", prompt)
	}
}

// TestReviewJobSpecPickSharedOnce: a pick run reviews several attempts at one
// task, so the one shared brief is quoted once beside the directories rather
// than once per directory (R9).
func TestReviewJobSpecPickSharedOnce(t *testing.T) {
	const brief = "add the retry budget to the fetch path"
	r := reviewPromptRun(t, nil, false, func(r *run) {
		r.reviewHeads = []reviewHead{
			{Number: 7, SHA: reviewJobHead, Base: "main"},
			{Number: 8, SHA: reviewJobHead, Base: "main"},
		}
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: brief}
	})
	prompt := r.reviewModePrompt(context.Background())
	if got := strings.Count(prompt, brief); got != 1 {
		t.Fatalf("the shared brief is quoted %d times, want once:\n%s", got, prompt)
	}
	if !strings.Contains(prompt, "Compare them and decide which to keep.") {
		t.Fatalf("the prompt is not the pick run's:\n%s", prompt)
	}
}

// TestReviewJobSpecArtifactRecordsAgainst: the artifact records what the review
// was checked against (R8) — the source, the root run and the digest of the
// brief the reviewer was shown — so a reader of the artifact can tell it judged
// the change against the task it was written for.
func TestReviewJobSpecArtifactRecordsAgainst(t *testing.T) {
	ctx := context.Background()
	const brief = "add the retry budget to the fetch path"
	r, _ := reviewJobRun(t, func(r *run) {
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: brief}
		r.readReviewSpec(ctx)
	})
	if err := r.writeReviewArtifact(ctx, reviewResult(t, reviewAnswerOf("ship", "the change does what the brief asks"))); err != nil {
		t.Fatalf("the review job could not write its artifact: %v", err)
	}
	art, err := DecodeReviewArtifact(reviewObject(t, r, ReviewArtifactName))
	if err != nil {
		t.Fatalf("the artifact this run wrote does not read back: %v", err)
	}
	if art.Against == nil {
		t.Fatal("the artifact records nothing of what the review was checked against")
	}
	if art.Against.Source != runstore.AgainstSourceRun || art.Against.Run != reviewRootRun {
		t.Errorf("against = %+v, want the root run %s", art.Against, reviewRootRun)
	}
	if art.Against.SHA256 != digestOf(brief) {
		t.Errorf("against.sha256 = %q, want the digest of the brief the reviewer was shown (%q)", art.Against.SHA256, digestOf(brief))
	}
	if err := art.Against.Validate(); err != nil {
		t.Errorf("the record the artifact carries does not hold its own shape: %v", err)
	}
}

// TestReviewJobSpecNoneRecordsReason: a review the launcher resolved to none —
// --code-only here — records that with its reason, so a reader can tell a
// review that judged the code alone from one whose task was lost (R8, F35), and
// its prompt carries no task block at all.
func TestReviewJobSpecNoneRecordsReason(t *testing.T) {
	ctx := context.Background()
	r, _ := reviewJobRun(t, func(r *run) {
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecNone, Reason: "--code-only"}
		r.readReviewSpec(ctx)
	})
	if err := r.writeReviewArtifact(ctx, reviewResult(t, reviewAnswerOf("ship", "the change is sound on its own"))); err != nil {
		t.Fatalf("the review job could not write its artifact: %v", err)
	}
	art, err := DecodeReviewArtifact(reviewObject(t, r, ReviewArtifactName))
	if err != nil {
		t.Fatalf("the artifact this run wrote does not read back: %v", err)
	}
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
	if prompt := r.reviewModePrompt(ctx); strings.Contains(prompt, "written for the task below") {
		t.Errorf("a code-only review's prompt carries a task block:\n%s", prompt)
	}
}

// TestReviewJobSpecNonJobPathComment: fugaro run --recipe review-only writes no
// artifact, so its prompt carries the same block and the line that says what
// the review was checked against goes into its comment instead (F36).
func TestReviewJobSpecNonJobPathComment(t *testing.T) {
	ctx := context.Background()
	const brief = "add the retry budget to the fetch path"
	r := reviewPromptRun(t, nil, false, func(r *run) {
		r.spec.ReviewSpec = &task.ReviewSpec{Source: task.ReviewSpecRun, Run: reviewRootRun, Text: brief}
	})
	if prompt := r.reviewModePrompt(ctx); !strings.Contains(prompt, brief) {
		t.Fatalf("the review-only path's prompt does not carry the brief:\n%s", prompt)
	}

	p := &fake.Provider{Repo: r.spec.Repo}
	p.State.PRs = []fake.PRState{{PR: gitprov.PR{Number: 7}}}
	r.provider = p
	r.rec = &runstore.Record{
		Version: 1, RunID: r.spec.RunID, Repo: r.spec.Repo,
		Recipe: &runstore.RecipeRecord{Name: "reviewonly", Source: "catalog"},
	}
	r.postReviewComments(ctx, Verdict{Verdict: "ship"})

	comments := p.State.PRs[0].Comments
	if len(comments) != 1 {
		t.Fatalf("the review posted %d comments, want one: %q", len(comments), comments)
	}
	if !strings.Contains(comments[0], "Checked against: the task of run "+reviewRootRun) {
		t.Errorf("the comment does not say what the review was checked against:\n%s", comments[0])
	}
	if !strings.Contains(comments[0], "Fugaro review (reviewonly): ship") {
		t.Errorf("the comment no longer opens with the recipe and the verdict:\n%s", comments[0])
	}
}

// TestReviewJobSpecNoneReasonNeutralized: the reason is the one field of the
// "Checked against" line a launcher wrote — the source, the run and the digest
// are Fugaro's own words and its own spelling of a run id and a hex digest — so
// it is neutralized like every other field of the comment that reaches a
// repository: an @mention in it cannot pull an account into a review's comment,
// and an issue autolink or a Markdown marker cannot forge structure in the
// rendered text (R8, F36).
//
// Mutation (drop the neutralizeFinding call in againstLine, leaving
// safetext.OneLine alone): this test fails, finding the mention and the
// autolink intact in the posted comment.
func TestReviewJobSpecNoneReasonNeutralized(t *testing.T) {
	ctx := context.Background()
	r := reviewPromptRun(t, nil, false, func(r *run) {
		r.spec.ReviewSpec = &task.ReviewSpec{
			Source: task.ReviewSpecNone,
			Reason: "launched with **@dimi** by hand on #12",
		}
	})

	p := &fake.Provider{Repo: r.spec.Repo}
	p.State.PRs = []fake.PRState{{PR: gitprov.PR{Number: 7}}}
	r.provider = p
	r.rec = &runstore.Record{
		Version: 1, RunID: r.spec.RunID, Repo: r.spec.Repo,
		Recipe: &runstore.RecipeRecord{Name: "reviewonly", Source: "catalog"},
	}
	r.postReviewComments(ctx, Verdict{Verdict: "ship"})

	comments := p.State.PRs[0].Comments
	if len(comments) != 1 {
		t.Fatalf("the review posted %d comments, want one: %q", len(comments), comments)
	}
	body := comments[0]
	for _, raw := range []string{"@dimi", "#12"} {
		if strings.Contains(body, raw) {
			t.Errorf("the launcher's own reason reaches the comment with %q intact, so the rendered comment pings an account or auto-links an issue: %q", raw, body)
		}
	}
	// The zero-width space's exact position, not merely "the raw substring is
	// gone": elsewhere in neutralizeFinding ordinary punctuation gains a
	// backslash, which a plain substring check for the raw form would pass on
	// without breaking GitHub's own text scanning at render time.
	for _, want := range []string{"@​dimi", "#​12"} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment does not carry the neutralized form %q: %q", want, body)
		}
	}
}
