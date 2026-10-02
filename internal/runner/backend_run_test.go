package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestBootstrapExchangesAndDeletesToken(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(1)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !b.tokenObjectGone() {
		t.Fatal("the token object is still in the bucket")
	}
	if b.itk.Exchanges() != 1 {
		t.Fatalf("exchanges = %d, want 1", b.itk.Exchanges())
	}
	creds := b.db.Credentials()
	if len(creds) == 0 {
		t.Fatal("the run never talked to the database")
	}
	for _, c := range creds {
		if !strings.HasPrefix(c, "auth:") {
			t.Fatalf("a database request carried %q: a run authenticates with its own ID token only", c)
		}
	}
	if rec.Budget == nil || rec.Budget.Mode != "enforce" || rec.Budget.Backend != "rtdb" || rec.Budget.Day != b.day() {
		t.Fatalf("budget record = %+v", rec.Budget)
	}
}

func TestKillAtBootstrapNoPR(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.db.Set(budget.PathKillGlobal, killSwitch(true))
	rec, err := b.run(t) // no stage may run
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil ||
		rec.Halt.Reason != runstore.HaltKillSwitch || rec.Halt.Scope != "global" {
		t.Fatalf("record = %+v, halt = %+v", rec, rec.Halt)
	}
	if !strings.Contains(rec.Halt.Detail, "admin@example.invalid") || !strings.Contains(rec.Halt.Detail, "runaway spend") {
		t.Fatalf("the halt does not say who and why: %q", rec.Halt.Detail)
	}
	if ok, _ := b.bucket.Exists(context.Background(), lock.Key("acme-app", "fugaro/"+runID)); ok {
		t.Fatal("the branch lock was taken")
	}
	if len(b.provider.State.PRs) != 0 || testutil.Git(t, b.remote, "for-each-ref", "refs/heads/fugaro/") != "" {
		t.Fatal("a halted bootstrap opened a PR or pushed a branch")
	}
	if len(b.agent.calls) != 0 || b.fake.Count() != 0 {
		t.Fatal("the agent or the gateway ran")
	}
	if out := b.outcome(); out == nil || out["status"] != "halted" || out["requestedBy"] != bkRB {
		t.Fatalf("outcome = %v", out)
	}
	if b.entry() != nil {
		t.Fatal("a registry entry was left")
	}
	if !b.tokenObjectGone() {
		t.Fatal("the token object is still in the bucket")
	}
}

func TestNoCapHaltsEnforce(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.db.Set(budget.PathCapsRepo(bkSlug), nil)
	rec, err := b.run(t)
	if err != nil || rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone ||
		rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap || rec.Halt.Scope != "repo" {
		t.Fatalf("rec = %+v, halt = %+v, err = %v", rec, rec.Halt, err)
	}
	if !strings.Contains(rec.Halt.Detail, "fugaro budget set") {
		t.Fatalf("the detail does not say how to set a cap: %q", rec.Halt.Detail)
	}
	if len(b.agent.calls) != 0 {
		t.Fatal("the agent ran")
	}
}

func TestKillMidImplementOpensDraft(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	rec, err := b.run(t, killMidStage(b.db, budget.PathKillRepo(bkSlug)))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Halt == nil ||
		rec.Halt.Reason != runstore.HaltKillSwitch || rec.Halt.Scope != "repo" {
		t.Fatalf("record = %+v, halt = %+v", rec, rec.Halt)
	}
	if len(b.agent.calls) != 1 || !onlyPR(t, b.provider).Draft {
		t.Fatalf("%d stages ran; PR draft = %v", len(b.agent.calls), onlyPR(t, b.provider).Draft)
	}
	report := lastComment(t, b.harness)
	for _, w := range []string{"**Halted:** the repository's kill switch was on", "admin@example.invalid", "runaway spend", "fugaro budget resume", "fugaro run --pr 1"} {
		if !strings.Contains(report, w) {
			t.Errorf("the report lacks %q:\n%s", w, report)
		}
	}
	if out := b.outcome(); out == nil || out["status"] != "halted" {
		t.Fatalf("outcome = %v", out)
	}
}

func TestObserveStillHonoursKill(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "observe", "")
	rec, err := b.run(t, killMidStage(b.db, budget.PathKillGlobal))
	if err != nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltKillSwitch || rec.Halt.Scope != "global" {
		t.Fatalf("rec = %+v, halt = %+v, err = %v", rec, rec.Halt, err)
	}
}

// TestKillDuringFinalizeIgnored: the work is done and no model call remains
// to stop, so a switch that goes on during finalize changes nothing.
func TestKillDuringFinalizeIgnored(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	var reviewed atomic.Bool
	var fired atomic.Bool
	b.provider.Auth = func(time.Duration) gitprov.GitAuth {
		// The first credential refresh after the review is finalize's.
		if reviewed.Load() && fired.CompareAndSwap(false, true) {
			b.db.Set(budget.PathKillGlobal, killSwitch(true))
			waitUntil(t, "the kill to reach the run", func() bool { return strings.Contains(b.logs.String(), "ignoring it") })
		}
		return gitprov.GitAuth{}
	}
	rev := review("ship", 0)
	rec, err := b.run(t, implement("feature"), func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		defer reviewed.Store(true)
		return rev(t, ctx, req)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fired.Load() {
		t.Fatal("the kill was never set during finalize")
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady || rec.Halt != nil {
		t.Fatalf("a kill during finalize changed the run: %+v", rec)
	}
}

func TestKillLosesToEarlierCancel(t *testing.T) {
	box := newHandleBox(t)
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	cancelThenKill := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !box.h.MarkCancelled() {
			t.Error("the cancel was refused")
		}
		b.db.Set(budget.PathKillGlobal, killSwitch(true))
		waitUntil(t, "the kill to reach the run", func() bool { return strings.Contains(b.logs.String(), "ignoring it") })
		return agent.Result{}, nil
	}
	rec, err := b.run(t, cancelThenKill, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusCancelled || rec.Halt != nil {
		t.Fatalf("a kill after a cancel changed the outcome: %+v", rec)
	}
}

func TestLeaseTopUpAndRelease(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(2)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// Two calls of $0.04: that is spent, and all that stays counted.
	if b.runLeaf("spent") != 80_000 {
		t.Fatalf("spent = %d", b.runLeaf("spent"))
	}
	if out := b.runLeaf("reserved") - b.runLeaf("released"); out != 80_000 {
		t.Fatalf("reserved %d - released %d = %d outstanding, want exactly what was spent", b.runLeaf("reserved"), b.runLeaf("released"), out)
	}
	if b.repoLeaf("counted") != 80_000 || b.globalLeaf("counted") != 80_000 {
		t.Fatalf("counted: repo %d, global %d (a release lowers them by exactly its amount)", b.repoLeaf("counted"), b.globalLeaf("counted"))
	}
	if b.repoLeaf("spent") != 80_000 || b.globalLeaf("spent") != 80_000 || b.repoLeaf("calls") != 2 {
		t.Fatalf("spent: repo %d, global %d; calls %d", b.repoLeaf("spent"), b.globalLeaf("spent"), b.repoLeaf("calls"))
	}
	if got := b.num(budget.PathByModel(b.day(), bkSlug, sonnet) + "/micros"); got != 80_000 {
		t.Fatalf("byModel micros = %d", got)
	}
	if rec.Budget == nil || rec.Budget.GrantedMicros-rec.Budget.ReleasedMicros != 80_000 || rec.Budget.ReleasedMicros == 0 {
		t.Fatalf("budget record = %+v", rec.Budget)
	}
	if b.entry() != nil {
		t.Fatal("the registry entry survived the run")
	}
	if out := b.outcome(); out == nil || out["status"] != "succeeded" {
		t.Fatalf("outcome = %v", out)
	}
}

func TestRepoDailyCapHalts(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(2)...)
	micros := budget.Micros(10_000)
	b.db.Set(budget.PathCapsRepo(bkSlug), budget.RepoCaps{DailyMicros: &micros, PerRunMicros: &micros})
	b.db.Set(budget.PathCapsRepo(bkSlug)+"/perRunMicros", 20_000_000)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0] != 403 || b.fake.Count() != 0 {
		t.Fatalf("statuses %v, upstream calls %d: a refused call is never forwarded", st, b.fake.Count())
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRepoDailyCap || rec.Halt.Scope != "repo" {
		t.Fatalf("rec = %+v, halt = %+v", rec, rec.Halt)
	}
	if !onlyPR(t, b.provider).Draft || !strings.Contains(lastComment(t, b.harness), "fugaro budget set") {
		t.Fatalf("the report does not say how to continue:\n%s", lastComment(t, b.harness))
	}
}

func TestGlobalDailyCapHalts(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(1)...)
	micros := budget.Micros(10_000)
	b.db.Set(budget.PathCapsGlobal+"/dailyMicros", int64(micros))
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}))
	if err != nil || len(st) != 1 || st[0] != 403 {
		t.Fatalf("statuses %v, err %v", st, err)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltGlobalDailyCap || rec.Halt.Scope != "global" {
		t.Fatalf("halt = %+v", rec.Halt)
	}
}

// TestRunCapFromRTDBMin: the database's per-run cap binds even when the
// policy's is far looser; the lower of the two rules.
func TestRunCapFromRTDBMin(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(3)...)
	b.db.Set(budget.PathCapsRepo(bkSlug)+"/perRunMicros", 100_000) // $0.10
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 403 {
		t.Fatalf("statuses = %v: the third call is the one refused, as with a static $0.10", st)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap || !strings.Contains(rec.Halt.Detail, "$0.10") {
		t.Fatalf("halt = %+v", rec.Halt)
	}
}

// ... and the policy's per-run cap binds when it is the lower.
func TestPolicyRunCapBindsBelowRTDB(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "0.10", capScript(3)...)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil || len(st) != 3 || st[2] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("statuses = %v, rec = %+v, err = %v", st, rec, err)
	}
}

func TestCommittedDayCapHalts(t *testing.T) {
	b := newBK(t, gwConfig(t, "")+"budget:\n  per_day_usd: 0.10\n", "enforce", "", capScript(3)...)
	b.deps.Project = "aurora"
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 3 || st[0] != 200 || st[2] != 403 {
		t.Fatalf("statuses = %v", st)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltRepoDailyCap || rec.Halt.Scope != "repo" || !strings.Contains(rec.Halt.Detail, "fugaro.yaml") {
		t.Fatalf("halt = %+v: the detail must name fugaro.yaml", rec.Halt)
	}
	if rec.Policy == nil || rec.Policy.Effective.PerDayUSD != 0.10 {
		t.Fatalf("policy = %+v", rec.Policy)
	}
	if !strings.Contains(lastComment(t, b.harness), "budget.per_day_usd") {
		t.Fatalf("the report does not point at per_day_usd:\n%s", lastComment(t, b.harness))
	}
}

func TestObserveNeverHaltsForCaps(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "observe", "0.10", capScript(3)...)
	b.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{})
	b.db.Set(budget.PathCapsRepo(bkSlug), nil)
	var st []int
	rec, err := b.run(t, calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000}, call{sonnet, 4000}), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for _, s := range st {
		if s != 200 {
			t.Fatalf("statuses = %v: observe refuses nothing for caps", st)
		}
	}
	if !strings.Contains(b.logs.String(), "would halt (observe)") {
		t.Fatalf("the would-halt was not logged:\n%s", b.logs.String())
	}
	if b.runLeaf("spent") != 120_000 {
		t.Fatalf("observe still accounts: spent %d", b.runLeaf("spent"))
	}
}

// vertexBK is a Vertex run (budget observe: enforce is refused) with the backend.
func vertexBK(t *testing.T) *bk {
	t.Helper()
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: vertex", 1)
	g := newGW(t, cfg, "observe", "")
	g.deps.Env = append(g.deps.Env, "CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234")
	g.deps.VertexTokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.TEST", TokenType: "Bearer"})
	return attachBackend(t, g, "observe")
}

func oauthBK(t *testing.T, mode string) *bk {
	t.Helper()
	cfg := strings.Replace(gwConfig(t, ""), "auth: api-key", "auth: oauth", 1)
	g := newGW(t, cfg, mode, "")
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	return attachBackend(t, g, mode)
}

// TestGraceHaltsAfterThreeMinutes: the backend gone for the whole grace halts
// the run, whatever the auth mode and whether the budget enforces or observes
// (D14). The window is shortened by the session's configuration; the default
// is pinned by TestGraceDefaultIsThreeMinutes.
func TestGraceHaltsAfterThreeMinutes(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T) *bk
	}{
		{"api-key enforce", func(t *testing.T) *bk { return newBK(t, gwConfig(t, ""), "enforce", "") }},
		{"api-key observe", func(t *testing.T) *bk { return newBK(t, gwConfig(t, ""), "observe", "") }},
		{"vertex observe", vertexBK},
		{"oauth observe", func(t *testing.T) *bk { return oauthBK(t, "observe") }},
		{"oauth enforce", func(t *testing.T) *bk { return oauthBK(t, "enforce") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := c.make(t)
			down := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				shell(t, req, "echo partial > partial.txt && git add -A && git commit -qm partial")
				b.db.Refuse(503, "UNAVAILABLE", "", "down")
				<-ctx.Done()
				return agent.Result{}, ctx.Err()
			}
			start := time.Now()
			rec, err := b.run(t, down)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft || rec.Halt == nil ||
				rec.Halt.Reason != runstore.HaltBudgetUnavailable || rec.Halt.Scope != "run" {
				t.Fatalf("rec = %+v, halt = %+v", rec, rec.Halt)
			}
			if d := time.Since(start); d < 500*time.Millisecond {
				t.Fatalf("halted after %s, before the grace of 600 ms", d)
			}
			if !strings.Contains(lastComment(t, b.harness), "reachable again") {
				t.Fatalf("the report does not say how to continue:\n%s", lastComment(t, b.harness))
			}
		})
	}
}

func TestGraceDefaultIsThreeMinutes(t *testing.T) {
	if budget.DefaultGrace != 3*time.Minute {
		t.Fatalf("the default grace is %s, want 3m (D14)", budget.DefaultGrace)
	}
	be, err := runner.BackendFromEnv(func(k string) (string, bool) { return "", false }, false)
	if err != nil || be.Grace != 0 || be.On() {
		t.Fatalf("no environment = %+v, %v: no backend, the default grace", be, err)
	}
	for v, ok := range map[string]bool{"5s": true, "3m": true, "1m30s": true, "4s": false, "4m": false, "soon": false, "0": false} {
		_, err := runner.BackendFromEnv(func(k string) (string, bool) {
			if k == runner.BudgetGraceEnv {
				return v, true
			}
			return "", false
		}, false)
		if (err == nil) != ok {
			t.Errorf("FUGARO_BUDGET_GRACE=%q: err = %v, want ok=%v (floor 5 s, never above the 3 m of D14)", v, err, ok)
		}
	}
}

func TestBackendFromEnv(t *testing.T) {
	get := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	be, err := runner.BackendFromEnv(get(map[string]string{
		runner.RTDBURLEnv: "https://aurora-fp-default-rtdb.firebaseio.com", runner.FirebaseAPIKeyEnv: "AIzaSyFakeWebApiKeyForTests000000000"}), false)
	if err != nil || !be.On() || be.APIKey == "" {
		t.Fatalf("be = %+v, err = %v", be, err)
	}
	for name, m := range map[string]map[string]string{
		"cleartext url":   {runner.RTDBURLEnv: "http://aurora-fp-default-rtdb.firebaseio.com"},
		"a foreign host":  {runner.RTDBURLEnv: "https://evil.example.invalid"},
		"a path":          {runner.RTDBURLEnv: "https://aurora-fp-default-rtdb.firebaseio.com/x"},
		"loopback in job": {runner.RTDBURLEnv: "http://127.0.0.1:9000"},
		"empty url":       {runner.RTDBURLEnv: ""},
		"empty key":       {runner.FirebaseAPIKeyEnv: ""},
		"key with query":  {runner.FirebaseAPIKeyEnv: "k&x=1"},
	} {
		if _, err := runner.BackendFromEnv(get(m), false); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := runner.BackendFromEnv(get(map[string]string{runner.RTDBURLEnv: "http://127.0.0.1:9000"}), true); err != nil {
		t.Errorf("a local run against a loopback database was refused: %v", err)
	}
}

// TestHeldLeaseSpendableDuringGrace: the lease a run already holds is its
// money; an outage does not take it away before the grace is over.
func TestHeldLeaseSpendableDuringGrace(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(3)...)
	b.deps.Backend.Tune = wrapTune(b.deps.Backend.Tune, func(c *budget.Config) { c.Grace = 10 * time.Second })
	var st []int
	down := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		status, _ := post(t, req, sonnet, 4000) // takes the lease
		st = append(st, status)
		b.db.Refuse(503, "UNAVAILABLE", "", "down")
		for i := 0; i < 2; i++ {
			status, _ := post(t, req, sonnet, 4000) // served from the lease already held
			st = append(st, status)
		}
		b.db.Refuse(0, "", "", "")
		return implement("feature")(t, ctx, req)
	}
	rec, err := b.run(t, down, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(st) != 3 || st[0] != 200 || st[1] != 200 || st[2] != 200 {
		t.Fatalf("statuses = %v: the held lease must keep serving calls through an outage", st)
	}
}

func wrapTune(first, second func(*budget.Config)) func(*budget.Config) {
	return func(c *budget.Config) { first(c); second(c) }
}

func TestOffNeverTouchesBackend(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "off", "")
	b.db.Refuse(503, "UNAVAILABLE", "", "down")
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Budget != nil {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := len(b.db.Credentials()); n != 0 {
		t.Fatalf("a run with its budget off made %d database requests", n)
	}
	if b.itk.Exchanges() != 0 || b.tokenObjectGone() {
		t.Fatal("a run with its budget off took its budget token")
	}
	if len(b.agent.calls) != 2 {
		t.Fatalf("%d stages ran", len(b.agent.calls))
	}
	if raw, _ := json.Marshal(rec); bytes.Contains(raw, []byte(`"budget"`)) {
		t.Fatalf("the record mentions a budget: %s", raw)
	}
}

func TestTokenExpiredHalts(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	if err := token.DeleteObject(context.Background(), b.bucket, bkSlug, runID); err != nil {
		t.Fatal(err)
	}
	b.mint(time.Now().Add(-2 * time.Hour)) // queued for two hours
	rec, err := b.run(t)
	if err != nil {
		t.Fatalf("a bootstrap halt returned %v, want nil so exec exits 0", err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetTokenExpired {
		t.Fatalf("rec = %+v, halt = %+v", rec, rec.Halt)
	}
	if !strings.Contains(rec.Halt.Detail, "launch it again") {
		t.Fatalf("detail = %q", rec.Halt.Detail)
	}
	if b.itk.Exchanges() != 0 || !b.tokenObjectGone() {
		t.Fatal("an expired token must be removed and never exchanged")
	}
	if len(b.agent.calls) != 0 || len(b.provider.State.PRs) != 0 {
		t.Fatal("the run went on")
	}
}

func TestBackendUnreachableAtBootstrapHalts(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.db.Refuse(503, "UNAVAILABLE", "", "down")
	rec, err := b.run(t)
	if err != nil || rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone ||
		rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("rec = %+v, halt = %+v, err = %v", rec, rec.Halt, err)
	}
	if len(b.provider.State.PRs) != 0 || len(b.agent.calls) != 0 {
		t.Fatal("an unreachable backend let the run begin")
	}
}

func TestMissingTokenIsAnInfraError(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	if err := token.DeleteObject(context.Background(), b.bucket, bkSlug, runID); err != nil {
		t.Fatal(err)
	}
	rec, err := b.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "launch the run again") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestOAuthReportsNotional(t *testing.T) {
	b := oauthBK(t, "enforce")
	usage := map[string]agent.Usage{sonnet: {Input: 1000, Output: 500, CacheRead: 200, CacheCreation: 100}}
	rec, err := b.run(t, withUsage(implement("feature"), agent.Usage{Input: 1000}, usage), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// implement() says $1 and review $0.5: notional, never capped, never leased.
	if b.runLeaf("notional") != 1_500_000 || b.repoLeaf("notional") != 1_500_000 || b.globalLeaf("notional") != 1_500_000 {
		t.Fatalf("notional: run %d, repo %d, global %d", b.runLeaf("notional"), b.repoLeaf("notional"), b.globalLeaf("notional"))
	}
	if b.runLeaf("reserved") != 0 || b.repoLeaf("counted") != 0 || b.globalLeaf("counted") != 0 || b.runLeaf("spent") != 0 {
		t.Fatal("an oauth run leased or spent against the caps")
	}
	if got := b.num(budget.PathByModel(b.day(), bkSlug, sonnet) + "/in"); got != 1000 {
		t.Fatalf("byModel in = %d", got)
	}
	if got := b.num(budget.PathByModel(b.day(), bkSlug, sonnet) + "/micros"); got <= 0 {
		t.Fatalf("byModel micros = %d, want the model's usage priced", got)
	}
	if rec.Cost == nil || rec.Cost.ModelBasis != runstore.BasisSubscription {
		t.Fatalf("cost = %+v", rec.Cost)
	}
	if rec.Budget == nil || rec.Budget.GrantedMicros != 0 {
		t.Fatalf("budget = %+v", rec.Budget)
	}
}

func TestOAuthKillHalts(t *testing.T) {
	b := oauthBK(t, "observe")
	rec, err := b.run(t, killMidStage(b.db, budget.PathKillGlobal))
	if err != nil || rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltKillSwitch {
		t.Fatalf("rec = %+v, halt = %+v, err = %v", rec, rec.Halt, err)
	}
	// The stage's own cost, even though it was stopped, is reported.
	if b.runLeaf("notional") != 100_000 {
		t.Fatalf("notional = %d", b.runLeaf("notional"))
	}
}

func TestOAuthNeverBlockedByMissingCaps(t *testing.T) {
	b := oauthBK(t, "enforce")
	b.db.Set(budget.PathCapsRepo(bkSlug), nil)
	b.db.Set(budget.PathCapsGlobal, nil)
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Halt != nil {
		t.Fatalf("an oauth run is uncapped for dollars: rec = %+v, err = %v", rec, err)
	}
}

func TestRegistryEntryWrittenAndDeleted(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(1)...)
	var seen map[string]any
	inStage := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		waitUntil(t, "the registry to show the stage", func() bool {
			seen = b.entry()
			return seen != nil && seen["stage"] == "implement"
		})
		return implement("feature")(t, ctx, req)
	}
	rec, err := b.run(t, inStage, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if seen["repo"] != "acme/app" || seen["requestedBy"] != bkRB || seen["auth"] != "api-key" || seen["coder"] != sonnet || seen["title"] != "Add a feature" {
		t.Fatalf("entry = %v", seen)
	}
	if b.entry() != nil {
		t.Fatal("the entry is still there after the run")
	}
}

func TestOutcomeWrittenOnce(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if out := b.outcome(); out == nil || out["status"] != "succeeded" || out["requestedBy"] != bkRB {
		t.Fatalf("outcome = %v", out)
	}
}

// TestHaltedRecordSchema: the record of a run halted by the backend, as it
// is stored, satisfies the published result schema.
func TestHaltedRecordSchema(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	rec, err := b.run(t, killMidStage(b.db, budget.PathKillGlobal))
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	stored, err := b.store.ReadFile(context.Background(), "result.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../schemas/result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	id := doc.(map[string]any)["$id"].(string)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the stored record violates the schema: %v\n%s", err, stored)
	}
	var m map[string]any
	_ = json.Unmarshal(stored, &m)
	if m["budget"] == nil || m["halt"] == nil {
		t.Fatalf("record = %s", stored)
	}
}

// TestBudgetSecretScan: a whole run, and nothing of the run's Firebase
// identity is anywhere a person or the agent can read: not in any bucket
// object, the record, the report, the PR, the log, the agent's environment
// or its transcript. (The API key is not secret but is redacted like one.)
func TestBudgetSecretScan(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(2)...)
	// Real values from the job's environment, as on Cloud Run.
	b.deps.Env = append(b.deps.Env, runner.RTDBURLEnv+"="+b.db.URL, runner.FirebaseAPIKeyEnv+"="+bkAPIKey)
	custom := func() string {
		objs := allObjects(t, b.harness.bucket)
		return objs[token.ObjectKey(bkSlug, runID)]
	}()
	if custom == "" {
		t.Fatal("no token object to start from")
	}
	var st []int
	leaky := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		// An agent that tries to print everything in its environment and
		// the secrets it can think of.
		for _, kv := range req.Env {
			if strings.Contains(kv, "FIREBASE") || strings.Contains(kv, "RTDB") || strings.Contains(kv, custom) {
				t.Errorf("the agent's environment has %q", kv)
			}
		}
		return calling(&st, implement("feature"), call{sonnet, 4000}, call{sonnet, 4000})(t, ctx, req)
	}
	rec, err := b.run(t, leaky, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	secrets := append(b.registered(), custom, bkAPIKey)
	if len(b.registered()) < 4 {
		t.Fatalf("only %d secrets were registered for redaction", len(b.registered()))
	}
	stored, _ := b.store.ReadFile(context.Background(), "result.json")
	haystacks := map[string]string{"log": b.logs.String(), "record": string(stored), "pr comments": strings.Join(onlyPR(t, b.provider).Comments, "\n"),
		"pr body": onlyPR(t, b.provider).Spec.Body}
	for k, v := range allObjects(t, b.harness.bucket) {
		haystacks["bucket "+k] = v
	}
	for _, c := range b.agent.calls {
		haystacks["agent env"] += strings.Join(c.Env, "\n")
	}
	for _, s := range secrets {
		if len(s) < 8 {
			continue
		}
		for name, h := range haystacks {
			if strings.Contains(h, s) {
				t.Errorf("a Firebase credential (%d bytes, %q...) is in %s", len(s), s[:6], name)
			}
		}
	}
}

// TestImageCheckUnaffectedByBackendLoss: the daily image check and rebuilds do
// not depend on the budget backend (D14 exempts them): the packages that run
// them import neither the budget nor the database client.
func TestImageCheckUnaffectedByBackendLoss(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./internal/imagecheck", "./internal/image")
	cmd.Dir = "../.."
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	out := string(raw)
	for _, banned := range []string{"internal/budget", "internal/rtdb"} {
		if strings.Contains(out, "fugaro/"+banned) {
			t.Fatalf("the image check depends on %s", banned)
		}
	}
}

var _ = anthropicfake.MessageOK
var _ = pricing.Micros(0)

// TestKillStopsGatewayCalls: once a kill switch is on, the gateway refuses
// every later call, even though the run still holds a lease that could pay for
// it.
func TestKillStopsGatewayCalls(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "", capScript(3)...)
	var st []int
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		first, _ := post(t, req, sonnet, 4000) // takes the lease
		st = append(st, first)
		b.db.Set(budget.PathKillGlobal, killSwitch(true))
		waitUntil(t, "the run to halt", func() bool { return strings.Contains(b.logs.String(), "the run is halted") })
		status, body := post(t, req, sonnet, 4000) // the lease would cover it
		st = append(st, status)
		if !strings.Contains(body, "kill switch") {
			t.Errorf("the refusal does not say why: %s", body)
		}
		return agent.Result{}, nil
	}
	rec, err := b.run(t, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0] != 200 || st[1] != 403 || b.fake.Count() != 1 {
		t.Fatalf("statuses %v, upstream calls %d", st, b.fake.Count())
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltKillSwitch {
		t.Fatalf("halt = %+v", rec.Halt)
	}
}

func TestBackendErrIsInfraError(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.deps.BackendErr = errors.New("FUGARO_RTDB_URL: not a database URL")
	rec, err := b.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "FUGARO_RTDB_URL") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if b.itk.Exchanges() != 0 || len(b.db.Credentials()) != 0 {
		t.Fatal("a malformed backend configuration was used anyway")
	}
}

func TestBackendWithoutAPIKeyIsInfraError(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.deps.Backend.APIKey = ""
	rec, err := b.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, runner.FirebaseAPIKeyEnv) {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestMidRunEnforceStopsAnObservingRun: the project switches to enforce while
// a run that began in observe holds a lease; the next top-up is judged by
// the rules' mode and halts it.
func TestMidRunEnforceStopsAnObservingRun(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "observe", "", capScript(2)...)
	micros, small := budget.Micros(1_000), budget.Micros(50_000)
	b.db.Set(budget.PathLimits, budget.Limits{MaxReserveMicros: &small}) // small leases: the second call tops up
	b.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: &micros, PerRunMicros: &micros})
	b.db.Set(budget.PathCapsRepo(bkSlug), budget.RepoCaps{DailyMicros: &micros, PerRunMicros: &micros})
	var st []int
	step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		s1, _ := post(t, req, sonnet, 4000)
		b.db.Set(budget.PathMode, "enforce")
		// A call the held lease cannot cover: the lease must top up, and
		// now the caps are binding.
		s2, _ := post(t, req, sonnet, 4000)
		st = append(st, s1, s2)
		return implement("feature")(t, ctx, req)
	}
	rec, err := b.run(t, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0] != 200 || st[1] != 403 || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("statuses %v, halt %+v", st, rec.Halt)
	}
}

// TestWrongDatabaseIsAnInfraError: a database that refuses the run's token (a
// project mismatch, the wrong URL) is a configuration error, not a halt that
// waits out the grace.
func TestWrongDatabaseIsAnInfraError(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "")
	b.db.Refuse(401, "UNAUTHENTICATED", "", "Permission denied")
	rec, err := b.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || rec.Halt != nil || !strings.Contains(rec.Reason, "refused the run's credential") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(b.agent.calls) != 0 || len(b.provider.State.PRs) != 0 {
		t.Fatal("the run went on")
	}
}
