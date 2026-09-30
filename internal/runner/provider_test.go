package runner_test

import (
	"context"
	"errors"
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
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const gitToken = "tok-git-secret-1234"

func staticAuth(token string) func(time.Duration) gitprov.GitAuth {
	return func(time.Duration) gitprov.GitAuth {
		return gitprov.GitAuth{Username: "x-token-auth", Token: token, Env: map[string]string{"GH_TOKEN": token}}
	}
}

// TestGitCredentialsOverHTTP runs against a remote that demands the
// provider's token, and checks that runner and agent both authenticate
// without the token ever being written to the checkout or a transcript.
func TestGitCredentialsOverHTTP(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	h.provider.Auth = staticAuth(gitToken)
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintf(req.Transcript, "{\"token\":%q}\n", envValue(req.Env, "FUGARO_GIT_TOKEN"))
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, leak, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s, want %s", got, rec.HeadSHA)
	}
	env := h.agent.calls[0].Env
	for k, want := range map[string]string{"FUGARO_GIT_TOKEN": gitToken, "FUGARO_GIT_USERNAME": "x-token-auth", "GH_TOKEN": gitToken, "GIT_CONFIG_COUNT": "2"} {
		if got := envValue(env, k); got != want {
			t.Errorf("agent %s = %q, want %q", k, got, want)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(h.deps.WorkDir, ".git", "config")); strings.Contains(string(data), gitToken) {
		t.Fatalf(".git/config holds the token:\n%s", data)
	}
	transcript, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"transcripts/implement-1.jsonl")
	if err != nil || strings.Contains(string(transcript), gitToken) {
		t.Fatalf("transcript leaks the git token (%v): %s", err, transcript)
	}
}

// TestAgentPushAndAmendIsTolerated covers an agent that pushes the run
// branch itself and then amends it: finalize must still push the amended
// commit rather than fail.
func TestAgentPushAndAmendIsTolerated(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	h.provider.Auth = staticAuth(gitToken)
	pushThenAmend := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo v1 > feature.txt && git add -A && git commit -qm 'Add feature' && git push -q origin HEAD")
		shell(t, req, "echo v2 > feature.txt && git commit -qa --amend -m 'Add feature, amended'")
		verifyTest(t, ctx, req)
		return agent.Result{}, nil
	}
	rec, err := h.run(t, pushThenAmend, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if got := testutil.Git(t, h.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID); got != "Add feature, amended" {
		t.Fatalf("remote branch tip = %q, want the amended commit", got)
	}
}

// TestTokenRefreshedBetweenStages checks that each stage gets a token
// asked to outlive it, that the agent sees the new one, and that every
// token handed out is redacted.
func TestTokenRefreshedBetweenStages(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Prefix("x-token-auth", "tok-"))
	var mu sync.Mutex
	var asked []time.Duration
	h.provider.Auth = func(minValid time.Duration) gitprov.GitAuth {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, minValid)
		return gitprov.GitAuth{Username: "x-token-auth", Token: fmt.Sprintf("tok-%04d", len(asked))}
	}
	echoTokens := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintf(req.Transcript, "{\"old\":\"tok-0002\",\"new\":%q}\n", envValue(req.Env, "FUGARO_GIT_TOKEN"))
		return review("ship", 0)(t, ctx, req)
	}
	if _, err := h.run(t, implement("feature"), echoTokens); err != nil {
		t.Fatal(err)
	}
	// bootstrap, implement, review, finalize; the fixture's stage timeout
	// is 2m and its finalize reserve 30s.
	// Finalize asks for no less than bootstrap does: its 30s reserve alone
	// would let a token expire mid-push.
	want := []time.Duration{10 * time.Minute, 7 * time.Minute, 7 * time.Minute, 10 * time.Minute}
	if !slices.Equal(asked, want) {
		t.Fatalf("GitAuth minValid = %v, want %v", asked, want)
	}
	if got := envValue(h.agent.calls[0].Env, "FUGARO_GIT_TOKEN"); got != "tok-0002" {
		t.Fatalf("implement token = %q", got)
	}
	if got := envValue(h.agent.calls[1].Env, "FUGARO_GIT_TOKEN"); got != "tok-0003" {
		t.Fatalf("review token = %q", got)
	}
	transcript, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"transcripts/review-1.jsonl")
	if err != nil || strings.Contains(string(transcript), "tok-000") {
		t.Fatalf("review transcript leaks a token (%v): %s", err, transcript)
	}
}

// TestTokenValidityCappedBelowTokenLife covers a stage timeout longer than
// a GitHub installation token lives (about an hour): asking for more than
// that could never be satisfied, so the refresh would fail and keep the old
// token. The runner caps what it asks for instead, and relies on the
// refresh between stages.
func TestTokenValidityCappedBelowTokenLife(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 5h, stage: 2h, verify: 1m, finalize_reserve: 70m }", 1)
	h := newHarness(t, cfg, nil)
	h.useHTTPRemote(t, testutil.Prefix("x-token-auth", "tok-"))
	var mu sync.Mutex
	var asked []time.Duration
	h.provider.Auth = func(minValid time.Duration) gitprov.GitAuth {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, minValid)
		return gitprov.GitAuth{Username: "x-token-auth", Token: fmt.Sprintf("tok-%04d", len(asked))}
	}
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	// bootstrap, implement, review, finalize.
	want := []time.Duration{10 * time.Minute, 50 * time.Minute, 50 * time.Minute, 50 * time.Minute}
	if !slices.Equal(asked, want) {
		t.Fatalf("GitAuth minValid = %v, want %v", asked, want)
	}
	if got := envValue(h.agent.calls[1].Env, "FUGARO_GIT_TOKEN"); got != "tok-0003" {
		t.Fatalf("review token = %q, want the refreshed tok-0003", got)
	}
}

func TestProviderOpenedFromConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	var kinds, repos []string
	h.deps.OpenProvider = func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		kinds, repos = append(kinds, kind), append(repos, repo)
		return h.provider, nil, nil
	}
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []string{"github"}) || !slices.Equal(repos, []string{"acme/app"}) {
		t.Fatalf("opened %v for %v, want github once for acme/app", kinds, repos)
	}
}

func TestProviderKindMustMatchConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.ProviderKind = "bitbucket"
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError ||
		!strings.Contains(rec.Reason, "git.provider to github") || !strings.Contains(rec.Reason, "--provider flag says bitbucket") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("a PR was opened despite the mismatch")
	}
}

func TestProviderOpenFailureIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.OpenProvider = func(context.Context, string, string) (gitprov.Provider, []string, error) {
		return nil, []string{"s3cr3t-app-key"}, errors.New("rejected key s3cr3t-app-key")
	}
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "rejected key [REDACTED]") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestEnsurePRRetried covers a transient provider failure at finalize: the
// run retries, and ends with exactly one PR.
func TestEnsurePRRetried(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsure = 2
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	onlyPR(t, h.provider)
}

func TestEnsurePRGivesUp(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsure = 3
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "injected EnsurePR failure") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// Every attempt failed before ever reaching the provider, so nothing
	// was created: the record must not carry a phantom PR.
	if rec.PR != nil {
		t.Fatalf("rec.PR = %+v, want nil: nothing was ever created", rec.PR)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatalf("a PR was created despite every attempt failing: %+v", h.provider.State.PRs)
	}
}

// TestEnsurePRGivesUpKeepsPopulatedPR covers a run where an earlier
// EnsurePR attempt creates the PR but reports failure (the request landed
// but its response was lost), and a later attempt fails outright with no
// PR at all: the populated PR from the earlier attempt must survive into
// the final, still-failed record.
func TestEnsurePRGivesUpKeepsPopulatedPR(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsureAfterCreate = 1 // attempt 1: creates the PR, still errors
	h.provider.FailEnsure = 2            // attempts 2 and 3: fail with no PR at all
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.PR == nil || rec.PR.Number == 0 {
		t.Fatalf("the populated PR from the earlier attempt was dropped: %+v", rec.PR)
	}
	// The PR exists but the run could not settle its state: it carries a
	// comment saying so, in place of the run report.
	pr := onlyPR(t, h.provider)
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "not ready") ||
		!strings.Contains(pr.Comments[0], "check it by hand") || !strings.Contains(pr.Comments[0], "injected EnsurePR failure") {
		t.Fatalf("comments = %q, want one saying the PR is not ready and needs a human check", pr.Comments)
	}
}

// leakyEnsure wraps the fake provider so that EnsurePR creates the PR and
// then fails with an error quoting the git token.
type leakyEnsure struct{ *fake.Provider }

func (l leakyEnsure) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	pr, err := l.Provider.EnsurePR(ctx, spec)
	if err != nil {
		return pr, err
	}
	return pr, errors.New("upstream said: bad credential " + gitToken)
}

// stallEnsure wraps the fake provider so that EnsurePR creates the PR and
// then hangs until finalize's deadline, as a provider that stops answering
// would; Comment, like a real HTTP call, fails on a context already done.
type stallEnsure struct{ *fake.Provider }

func (s stallEnsure) EnsurePR(ctx context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	pr, err := s.Provider.EnsurePR(ctx, spec)
	if err != nil {
		return pr, err
	}
	<-ctx.Done()
	return pr, ctx.Err()
}

func (s stallEnsure) Comment(ctx context.Context, pr gitprov.PR, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Provider.Comment(ctx, pr, body)
}

// TestEnsurePRGivesUpAfterDeadlineStillComments covers ensurePR giving up
// because finalize's reserve ran out: the not-ready comment is exactly
// what matters then, so it must be posted on a context of its own rather
// than the expired finalize one.
func TestEnsurePRGivesUpAfterDeadlineStillComments(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "finalize_reserve: 30s", "finalize_reserve: 3s", 1)
	h := newHarness(t, cfg, nil)
	h.deps.RetryDelay = time.Millisecond
	h.deps.OpenProvider = gitprov.Static(stallEnsure{h.provider})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError || rec.PR == nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "not ready") {
		t.Fatalf("comments = %q, want the not-ready comment despite the expired deadline", pr.Comments)
	}
	if id, ok := gitprov.FugaroRun(pr.Comments[0]); !ok || id != rec.RunID {
		t.Fatalf("the not-ready comment names run %q, %v; want %q:\n%s", id, ok, rec.RunID, pr.Comments[0])
	}
}

// TestNotReadyNoteCarriesMarker: the not-ready note ends with the run's
// marker, so a later follow-up can tell which run posted it.
func TestNotReadyNoteCarriesMarker(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.RetryDelay = time.Millisecond
	h.provider.FailEnsureAfterCreate = 3 // every attempt creates or finds the PR, and still errors
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if len(pr.Comments) != 1 || !strings.Contains(pr.Comments[0], "not ready") ||
		!strings.HasSuffix(pr.Comments[0], "\n"+gitprov.ReportMarker(runID)+"\n") {
		t.Fatalf("comments = %q, want the not-ready note ending with the run's marker", pr.Comments)
	}
}

// TestEnsurePRGivesUpCommentIsRedacted checks that the give-up comment,
// which quotes the provider's error, never carries a credential.
func TestEnsurePRGivesUpCommentIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Token("x-token-auth", gitToken))
	h.provider.Auth = staticAuth(gitToken)
	h.deps.RetryDelay = time.Millisecond
	h.deps.OpenProvider = gitprov.Static(leakyEnsure{h.provider})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec.Status != runstore.StatusInfraError || rec.PR == nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	pr := onlyPR(t, h.provider)
	if len(pr.Comments) != 1 || strings.Contains(pr.Comments[0], gitToken) || !strings.Contains(pr.Comments[0], "[REDACTED]") {
		t.Fatalf("comments = %q, want one with the token redacted", pr.Comments)
	}
}

// TestPartialEnsureDowngradesReadyToDraft covers a run whose tests pass and
// whose review shipped, but whose provider could only leave the PR looking
// like a draft. The run must not report readiness it cannot back up.
func TestPartialEnsureDowngradesReadyToDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	h.provider.PartialEnsure = errors.New("drafts are unsupported on this repository")
	h.provider.PartialEnsureDraft = true
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v", rec)
	}
	if !strings.Contains(rec.Reason, "drafts are unsupported on this repository") {
		t.Fatalf("reason does not name the failure: %q", rec.Reason)
	}
	if rec.PR == nil || rec.PR.Number == 0 {
		t.Fatalf("PR was not recorded: %+v", rec.PR)
	}
	if !onlyPR(t, h.provider).Draft {
		t.Fatal("the fake's PR is not a draft")
	}
}

// TestPartialEnsureNonDraftKeepsReady covers the other direction: the
// provider reports a PartialError (say, labels could not be applied) but
// still returns the PR in the state the run actually asked for (not a
// draft). The run must still report readiness.
func TestPartialEnsureNonDraftKeepsReady(t *testing.T) {
	h := newHarness(t, "", nil)
	h.provider.PartialEnsure = errors.New("labels could not be applied")
	h.provider.PartialEnsureDraft = false
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v", rec)
	}
	if rec.PR == nil || rec.PR.Number == 0 {
		t.Fatalf("PR was not recorded: %+v", rec.PR)
	}
	if onlyPR(t, h.provider).Draft {
		t.Fatal("the fake's PR should not be a draft")
	}
}

// TestGitAuthTokenWithNewlineIsInfraError checks that a provider handing
// back a token gitops.CredentialVars refuses (here, one containing a
// newline) fails the run outright at open, as an infra_error, instead of
// silently dropping git credentials.
func TestGitAuthTokenWithNewlineIsInfraError(t *testing.T) {
	h := newHarness(t, "", nil)
	h.useHTTPRemote(t, testutil.Token("x-token-auth", "irrelevant"))
	h.provider.Auth = staticAuth("bad\ntoken")
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "newline") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}
