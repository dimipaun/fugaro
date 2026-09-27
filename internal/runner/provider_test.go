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
	want := []time.Duration{10 * time.Minute, 7 * time.Minute, 7 * time.Minute, 30 * time.Second}
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
}
