package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestRegistryCarriesVerifyAndPR (G24): once the coder's verify record
// exists the registry entry's verify is its one-line summary; once the
// early draft PR exists, prUrl is its URL. Both reach the database through
// the heartbeat, which the fake RTDB records.
func TestRegistryCarriesVerifyAndPR(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	var sawVerify, sawPR string
	atReview := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		waitUntil(t, "the registry to carry verify and prUrl", func() bool {
			e := b.entry()
			if e == nil {
				return false
			}
			sawVerify, _ = e["verify"].(string)
			sawPR, _ = e["prUrl"].(string)
			return sawVerify != "" && sawPR != ""
		})
		return review("ship", 0)(t, ctx, req)
	}
	rec, err := b.run(t, implement("feature"), atReview)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.HasPrefix(sawVerify, "fugaro verify test #1: passed") {
		t.Fatalf("verify = %q", sawVerify)
	}
	if want := onlyPR(t, b.provider).URL; sawPR != want {
		t.Fatalf("prUrl = %q, want %q", sawPR, want)
	}
}

// TestRegistryVerifySummaryRedacted: a secret that reaches a failing test's
// name (as a credential might, in whatever the agent names a report) is
// redacted before the summary reaches the registry.
func TestRegistryVerifySummaryRedacted(t *testing.T) {
	const secret = "Suite.beta" // a substring of the planted API key below
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.deps.Env = append(filterEnv(b.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+secret)
	b.fails(t, "beta")
	var sawVerify string
	atReview := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		waitUntil(t, "the registry to carry the failed verify summary", func() bool {
			e := b.entry()
			if e == nil {
				return false
			}
			sawVerify, _ = e["verify"].(string)
			return sawVerify != ""
		})
		return review("ship", 0)(t, ctx, req)
	}
	if _, err := b.run(t, implement("feature"), atReview); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sawVerify, "fugaro verify test #1: FAILED") {
		t.Fatalf("verify = %q", sawVerify)
	}
	if strings.Contains(sawVerify, secret) {
		t.Fatalf("a secret reached the registry's verify field: %q", sawVerify)
	}
}
