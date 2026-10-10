package runner_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// followUpGWConfig is followUpYAML with the model fields a budget needs to
// price the run (gwConfig's replacement, on top of followUpYAML's).
func followUpGWConfig(t *testing.T) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	cfg = strings.Replace(cfg, "      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }\n",
		"      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }\n      - { name: planted, env: PLANTED_SECRET }\n", 1)
	cfg = strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: 2\n  model: "+sonnet+"\n  models: { coder: "+sonnet+", reviewer: "+sonnet+", background: "+haiku+" }\n  max_output_tokens: { coder: 4096, reviewer: 4096 }\n", 1)
	return cfg + "followup:\n  trusted: [\"" + aliceID + "\", \"" + bobID + "\"]\n"
}

// followUpBK attaches the budget backend to a fuHarness whose first run
// already opened PR 1, so a follow-up on it goes through the backend too.
func followUpBK(t *testing.T, mode string) (*fuHarness, *bk) {
	t.Helper()
	fh := followUpHarness(t, followUpGWConfig(t), nil)
	fh.deps.Env = append(filterEnv(fh.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+plantedKey)
	fake, up := anthropicfake.New(t)
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) {
		if k == runner.BudgetModeEnv {
			return mode, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	fh.deps.Spend, fh.deps.GatewayUpstream = spend, up.URL
	fh.deps.Log = slog.New(slog.NewTextHandler(&lockedBuf{}, nil))
	g := &gw{harness: fh.harness, fake: fake}
	return fh, attachBackend(t, g, mode)
}

// mintFor leaves run's budget token in the bucket, as fugaro run does at
// launch, for a run id other than the package's shared runID constant.
func mintFor(t *testing.T, b *bk, run string, at time.Time) {
	t.Helper()
	signer, err := token.NewIAMSigner(bkSignerEmail, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(b.iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: bkSlug, Run: run, FX: token.ExpiryFX(at, 3*time.Hour), FP: "aurora", RB: bkRB}, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := token.PutObject(context.Background(), b.bucket, bkSlug, run, tok); err != nil {
		t.Fatal(err)
	}
}

// entryFor is the registry entry of a run other than the package's shared
// runID constant, which (b *bk) entry() is hard-coded to.
func entryFor(b *bk, run string) map[string]any {
	m, _ := b.db.Value(budget.PathAgent(bkSlug, run)).(map[string]any)
	return m
}

// TestFollowUpRegistryHasPRURLFromBootstrap: a follow-up's PR is known from
// checkPullRequest at bootstrap, long before any stage boundary could call
// notePR (which a follow-up never does: the PR already exists). The
// registry must carry prUrl from the first stage on, not stay empty for the
// run's whole duration.
func TestFollowUpRegistryHasPRURLFromBootstrap(t *testing.T) {
	fh, b := followUpBK(t, "enforce")
	fh.followUp(t, followID, runID, "please continue")
	mintFor(t, b, followID, time.Now())
	var sawPR string
	first := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		waitUntil(t, "the registry to carry the follow-up's prUrl", func() bool {
			e := entryFor(b, followID)
			if e == nil {
				return false
			}
			sawPR, _ = e["prUrl"].(string)
			return sawPR != ""
		})
		return withAnswer(implement("more"), "did more")(t, ctx, req)
	}
	rec, err := fh.run(t, first, review("ship", 0))
	mustReady(t, rec, err)
	if want := onlyPR(t, fh.provider).URL; sawPR != want {
		t.Fatalf("registry prUrl = %q, want %q", sawPR, want)
	}
}
