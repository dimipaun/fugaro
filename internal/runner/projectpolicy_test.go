package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// policyRun is newBK with the project's policy published on its database: the
// rules marker, which the owner's rules deploy writes, and
// config/policy/shared. mode and capUSD are the job environment's own budget,
// which a published policy replaces (internal/runner's resolvePolicy).
func policyRun(t *testing.T, cfg, mode, capUSD, policy string, script ...anthropicfake.Reply) *bk {
	t.Helper()
	b := newBK(t, cfg, mode, capUSD, script...)
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	if policy != "" {
		b.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
	}
	return b
}

// sourceOf is where the run's result.json says one effective value came from.
func sourceOf(t *testing.T, rec *runstore.Record, key string) string {
	t.Helper()
	if rec.Policy == nil {
		t.Fatalf("the run recorded no policy: %+v", rec)
	}
	return rec.Policy.Sources[key]
}

// A published policy is the run's ceiling, not the copy the job was deployed
// with: it decides the mode and the cap (D11).
func TestRunnerReadsPolicyWhenTheNodeExists(t *testing.T) {
	// The job's environment says off with no cap; the policy says enforce with
	// $0.09. Each call on sonnet costs $0.04, and the third is refused before
	// it is made: what is left of the cap does not cover it.
	b := policyRun(t, gwConfig(t, ""), "off", "",
		`{"version":1,"budget":{"mode":"enforce","per_run_usd":0.09}}`, capScript(3)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 403 {
		t.Fatalf("statuses = %v: the policy's $0.09 cap must refuse the third call", st)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "0.09") {
		t.Fatalf("halt = %+v, want the policy's cap", rec.Halt)
	}
	if s := sourceOf(t, rec, "mode"); s != "policy" {
		t.Errorf("mode source = %q, want policy", s)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "policy" {
		t.Errorf("per_run_usd source = %q, want policy", s)
	}
	// The mode is the policy's too: the session exists although the job's
	// environment says off.
	if rec.Budget == nil || rec.Budget.Mode != "enforce" || rec.Budget.Backend != "rtdb" {
		t.Fatalf("budget record = %+v, want an enforce session", rec.Budget)
	}
}

// With the rules deployed and no node, the run is exactly today's: the job
// environment's ceiling, and the record says so (P3).
func TestRunnerUsesEnvCeilingWhenTheNodeIsAbsent(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "ceiling" {
		t.Errorf("per_run_usd source = %q, want ceiling", s)
	}
	if s := sourceOf(t, rec, "mode"); s != "ceiling" {
		t.Errorf("mode source = %q, want ceiling", s)
	}
	if rec.Budget == nil || rec.Budget.Mode != "enforce" {
		t.Fatalf("budget record = %+v", rec.Budget)
	}
}

// The finding-1 regression test: before the policy rules are deployed no policy
// can have been published here, so a denied read of the node is not an error
// and the installation runs as it always has (a node is published only with the
// marker, and the deploy that publishes it grants the read).
func TestRunnerWithoutTheMarkerTreatsADeniedReadAsLegacy(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":1,"budget":{"mode":"enforce","per_run_usd":0.05}}`, capScript(3)...)
	// The pre-policy rules deny the node as they deny everything under
	// config/policy: the read is the same read, and only the marker tells the
	// two situations apart.
	b.db.Set(projectpolicy.PathRulesMarker(), nil)
	b.db.DenyRead(projectpolicy.PathShared())
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a denied read without the marker must take the legacy path: rec = %+v, err = %v", rec, err)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "ceiling" {
		t.Errorf("per_run_usd source = %q, want ceiling", s)
	}
	if len(st) != 3 || st[2] != 200 {
		t.Fatalf("statuses = %v: the environment's $50 cap must apply", st)
	}
}

// Carried review item (a): the same denial with the marker deployed is genuine,
// and the run does not start. The marker is written by the deploy that grants
// the read, so a denial means the rules were changed out from under a published
// policy (an older CLI's init --firebase redeploys rules that predate the
// marker). Falling back to the environment ceiling here would run outside the
// policy the owner published.
func TestRunnerHaltsOnADeniedReadWhenTheMarkerExists(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":1,"budget":{"mode":"enforce","per_run_usd":0.05}}`, capScript(3)...)
	b.db.DenyRead(projectpolicy.PathShared())
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a denied read of a published policy must stop the run: rec = %+v", rec)
	}
	for _, want := range []string{projectpolicy.PathShared(), projectpolicy.PathRulesMarker(), "fugaro init --firebase"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(b.agent.calls) != 0 || rec.Outcome != runstore.OutcomeNone || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("a stage ran or an outcome was written: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// The mode-off path (B1): the job's own mode is off and the run has no
// credential to read a policy with, so nothing is read, no session opens and
// the environment ceiling applies. This is safe for a launcher-minted token —
// one is minted whenever the project has published a policy, mode off included
// (internal/cli's mint decision), so a mode-off run with no token is a project
// with nothing published.
func TestRunnerWithNoCredentialTakesTheModeOffPath(t *testing.T) {
	t.Run("the job carries no backend", func(t *testing.T) {
		g := newGW(t, gwConfig(t, ""), "off", "")
		h := withBucket(g.harness)
		rec, err := g.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if rec.Budget != nil {
			t.Fatalf("a budget-off run with no backend joined the shared budget: %+v", rec.Budget)
		}
		ok, err := h.Exists(context.Background(), token.ObjectKey(bkSlug, runID))
		if err != nil || ok {
			t.Fatalf("a token object was left in the bucket (exists = %v, err = %v)", ok, err)
		}
	})
	t.Run("the bucket holds no token", func(t *testing.T) {
		b := newBK(t, gwConfig(t, ""), "off", "")
		if err := token.DeleteObject(context.Background(), b.bucket, bkSlug, runID); err != nil {
			t.Fatal(err)
		}
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Budget != nil {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if n := len(b.db.Credentials()); n != 0 {
			t.Fatalf("a run with its budget off made %d database requests", n)
		}
		if b.itk.Exchanges() != 0 {
			t.Fatal("a run with its budget off and no token exchanged one")
		}
	})
}

// B1's regression test: the job's environment says the budget is on but carries
// no FUGARO_FIREBASE_API_KEY. That is the shipped refusal — never the mode-off
// path, and never "no policy here": the run must not proceed on the environment
// ceiling with the project's policy unenforced.
func TestRunnerWithTheModeOnAndNoCredentialHalts(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	b.deps.Backend.APIKey = ""
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), runner.FirebaseAPIKeyEnv+" is not set") {
		t.Fatalf("err = %v, want the missing-key refusal", err)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run proceeded without its Firebase API key: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// B1's sibling: the credential is there, the token object is not. A budget-on
// run never reads that as "no policy here" — a first take would have consumed
// the token, so an empty bucket cannot be told from a launch that never minted
// one, and the shipped refusal is the only safe answer.
func TestRunnerWithTheModeOnAndNoTokenHalts(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	if err := token.DeleteObject(context.Background(), b.bucket, bkSlug, runID); err != nil {
		t.Fatal(err)
	}
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), "not in the runs bucket") {
		t.Fatalf("err = %v, want the missing-token refusal", err)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run proceeded without its budget token: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
}

// R2-3's regression test: with the URL, the key and a token object all present,
// the exchange fails. The run halts the way the session's own bootstrap halts —
// it does not fall back to the environment ceiling, which on an installation
// that has published a policy would be a run outside it.
func TestRunnerHaltsWhenTheExchangeFails(t *testing.T) {
	t.Run("an outage outlasts the grace", func(t *testing.T) {
		b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
		b.itk.Refuse(500, "INTERNAL", "", "boom")
		rec, err := b.run(t, implement("feature"))
		if err != nil {
			t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
			t.Fatalf("rec = %+v, halt = %+v: the run fell back to the env ceiling after a failed exchange", rec, rec.Halt)
		}
		if len(b.agent.calls) != 0 {
			t.Fatalf("%d stages ran after a failed exchange", len(b.agent.calls))
		}
	})
	t.Run("a token past its hour", func(t *testing.T) {
		b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
		// The launcher's own token, replaced by one minted two hours ago: a
		// run that queued that long.
		if err := token.DeleteObject(context.Background(), b.bucket, bkSlug, runID); err != nil {
			t.Fatal(err)
		}
		b.mint(time.Now().Add(-2 * time.Hour))
		rec, err := b.run(t, implement("feature"))
		if err != nil {
			t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
		}
		if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetTokenExpired {
			t.Fatalf("rec = %+v, halt = %+v", rec, rec.Halt)
		}
		if len(b.agent.calls) != 0 {
			t.Fatalf("%d stages ran after an expired exchange", len(b.agent.calls))
		}
	})
}

// The marker's own read can fail, and its answer is what decides whether a
// policy may exist at all: an unreachable node is an outage, retried like the
// session's own first calls until the grace runs out, and then a halt — never a
// silent legacy path.
func TestRunnerHaltsWhenTheMarkerReadFails(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":1}`, capScript(3)...)
	b.db.RefusePath("policyRules", 503, "UNAVAILABLE", "", "down")
	rec, err := b.run(t, implement("feature"))
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("rec = %+v, halt = %+v: an unreadable marker must halt, not read as absent", rec, rec.Halt)
	}
	if len(b.agent.calls) != 0 {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// A node that exists but is not a policy is a node the run may not use: it is
// never read as "no policy here" (the ceiling would then be the environment's,
// which is wider than what the owner published, and would be entered by
// tampering with the node).
func TestPolicyReadErrorHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", "", capScript(3)...)
	b.db.Set(projectpolicy.PathShared(), "not a policy")
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		t.Fatalf("an unreadable policy must stop the run naming the node: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// A policy whose budget block is refused by the decoder is the same class: the
// decode is strict about documented keys precisely because a dropped key is
// enforcement that never happens. The unknown top-level keys are the ones the
// decoder tolerates, and the run logs them (projectpolicy.Decode's
// UnknownKeys; the CLI refuses them, task 1.6).
func TestPolicyParseErrorHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":1,"budget":{"mode":"enforce","per_run_usdc":5}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		t.Fatalf("a misspelt budget key must stop the run: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// Carried review item (b): a top-level key this Fugaro does not know is logged
// and the run goes on — a newer CLI's key must not break an older image, and
// the last word on a misspelling is the CLI's (task 1.6).
func TestPolicyUnknownKeyIsLoggedAndRuns(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":1,"budget":{"mode":"enforce","per_run_usd":5},"allow_repo":["acme/*"]}`, capScript(1)...)
	rec, err := b.run(t, calling(new([]int), implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("an unknown top-level key stopped the run: rec = %+v, err = %v", rec, err)
	}
	if logs := b.logs.String(); !strings.Contains(logs, "allow_repo") {
		t.Errorf("the unknown key was not logged:\n%s", logs)
	}
}

// P8: a policy written by a newer Fugaro is not this image's to apply, and the
// image cannot be upgraded mid-run. It stops with both versions in the message.
func TestPolicyVersionMismatchHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":99,"budget":{"mode":"enforce"}}`, capScript(3)...)
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a policy of a newer version must stop the run: rec = %+v", rec)
	}
	for _, want := range []string{"version 99", "understands version 1", "fugaro upgrade", projectpolicy.PathShared()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// Carried review item (d): a marker that is not an integer generation is a
// user's node, named as itself — never read as "no marker", which would make a
// project-centric installation a legacy one.
func TestPolicyNonIntegerMarkerHaltsTheRun(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":1,"budget":{"mode":"enforce"}}`, capScript(3)...)
	b.db.Set(projectpolicy.PathRulesMarker(), "one")
	rec, err := b.run(t, implement("feature"))
	if err == nil || !strings.Contains(err.Error(), projectpolicy.PathRulesMarker()) || !strings.Contains(err.Error(), "one") {
		t.Fatalf("a hand-edited marker must stop the run naming the node: err = %v, rec = %+v", err, rec)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
}

// The single token take (finding 2): the policy read and the session share one
// TakeObject, because the call deletes the object as it reads it. A second take
// inside the run would find nothing and stop it, so a run that finishes — with
// a policy published, so that both readers use the identity — is the assertion.
// The exchange itself happens twice (once for the client the policy is read
// with, once in budget.Open), with the same custom token and the same claims.
func TestTokenIsTakenOnce(t *testing.T) {
	b := policyRun(t, gwConfig(t, ""), "enforce", "50", `{"version":1,"budget":{"mode":"enforce","per_run_usd":5}}`, capScript(1)...)
	rec, err := b.run(t, calling(new([]int), implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the run could not take the token twice", rec, err)
	}
	if s := sourceOf(t, rec, "mode"); s != "policy" {
		t.Fatalf("mode source = %q: the policy was not read", s)
	}
	if !b.tokenObjectGone() {
		t.Fatal("the token object is still in the bucket")
	}
	if n := b.itk.Exchanges(); n != 2 {
		t.Fatalf("%d exchanges, want 2 (the policy read's and the session's)", n)
	}
}

// The policy replaces the ceiling, and a looser file below it still only
// tightens: the default branch's $1000 does not lift the policy's $0.05, and
// the record says the policy set the value and that the other was ignored.
func TestPolicyCannotLoosenTheBranchLayer(t *testing.T) {
	b := policyRun(t, gwConfig(t, "")+"budget:\n  per_run_usd: 1000\n", "enforce", "50",
		`{"version":1,"budget":{"mode":"enforce","per_run_usd":0.05}}`, capScript(3)...)
	b.deps.Project = "aurora"
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "0.05") {
		t.Fatalf("rec = %+v, err = %v: the branch's $1000 must not lift the policy's cap", rec, err)
	}
	if s := sourceOf(t, rec, "per_run_usd"); s != "policy" {
		t.Errorf("per_run_usd source = %q, want policy", s)
	}
	if len(rec.Policy.Ignored) == 0 {
		t.Error("the ignored branch value is not recorded")
	}
}

// The policy carries the owner's price overrides, and they replace the job
// environment's copy of them whole: the run is metered by the node.
func TestPolicyPricesReplaceTheEnvOverrides(t *testing.T) {
	// The environment prices sonnet's output at $100/M; the policy at $10/M.
	// One 4,000-token reply is $0.40 or $0.04.
	b := policyRun(t, gwConfig(t, ""), "enforce", "50",
		`{"version":1,"budget":{"mode":"enforce","per_run_usd":5},"model_prices":{"`+sonnet+`":{"input_per_m":20,"output_per_m":10}}}`,
		capScript(1)...)
	b.deps.Spend = ceilingOf(t, runner.ModelPricesEnv, `{"`+sonnet+`":{"input_per_m":20,"output_per_m":100}}`)
	rec, err := b.run(t, calling(new([]int), implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !near(rec.CostUSD, 0.04) {
		t.Fatalf("cost = $%v, want $0.04: the environment's $100/M still applies", rec.CostUSD)
	}
}
