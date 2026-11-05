package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// accountA and accountB are the two named Claude accounts a walk test's
// published policy admits for the fixture repository, in the order `fugaro
// accounts ls` prints them: the unsuffixed default first, then the sorted
// names.
const (
	accountA = "team-a"
	accountB = "team-b"
)

// autoNode is the published policy a walk test runs against: A2's accounts
// block (account name → the repositories that account's token may be used by,
// projectpolicy.DeriveAccounts) for the fixture repository, and the switch
// that lets Fugaro choose an account for the owner. An absent switch, or one
// that is not a JSON true, is what every installation publishes today and what
// D875 ships. The node carries no profiles: the local run this harness drives
// reaches no project job, so §11.3's stage scope has nothing to narrow here,
// and the review-class half of it is pinned in its own test.
func autoNode(t *testing.T, auto string, names ...string) string {
	t.Helper()
	block := map[string]any{}
	for _, name := range names {
		block[name] = map[string]any{"allow_repos": []string{"github/acme/app"}}
	}
	doc := map[string]any{"version": projectpolicy.Version, "accounts": block}
	if auto != "" {
		doc["claude_account_auto"] = json.RawMessage(auto)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// autoRun is a local OAuth run against a published policy node: the world the
// walk is observable in end to end, because a local run is in no Cloud Run job
// and no container holds an account's token for it, so the account a stage is
// served by is the run's own choice. A cloud run is the other half and its own
// test: there the account is mounted into the job before the run starts, and a
// choice that is not that account is the refusal A7 writes.
func autoRun(t *testing.T, node string) *bk {
	t.Helper()
	b := oauthBK(t, "off")
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	b.db.Set(projectpolicy.PathShared(), json.RawMessage(node))
	return b
}

// autoNodeAdmitting is autoNode with each account's own allow_repos written
// out: the floor every rung of the chain stands on, asked again by the walk
// because the walk chooses after the launch.
func autoNodeAdmitting(t *testing.T, auto string, repos map[string][]string) string {
	t.Helper()
	block := map[string]any{}
	for name, list := range repos {
		block[name] = map[string]any{"allow_repos": list}
	}
	doc := map[string]any{"version": projectpolicy.Version, "accounts": block}
	if auto != "" {
		doc["claude_account_auto"] = json.RawMessage(auto)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// autoRunExplicit is autoRun with agent.oauth_token named in the repository's
// own fugaro.yaml: §11.2's rung 3, above the walk's rung 5.
func autoRunExplicit(t *testing.T, node, account string) *bk {
	t.Helper()
	cfg := strings.Replace(gwConfig(t, "  oauth_token: "+account+"\n"), "auth: api-key", "auth: oauth", 1)
	g := newGW(t, cfg, "off", "")
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	b := attachBackend(t, g, "off")
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	b.db.Set(projectpolicy.PathShared(), json.RawMessage(node))
	return b
}

// exhaustedOnce is the interrupted stage a walk test drives, with the reset
// the answer names inside the fixture workflow's own timeout (total 5m,
// reserve 30s).
func exhaustedOnce(t *testing.T, handle func() *runner.RunHandle) step {
	t.Helper()
	return withSession(exhaustedStage(t, handle, time.Now().Add(3*time.Minute)), "", "interrupted\n")
}

// doneThenExhausted is exhaustedStage's other ending: the stage's work was
// finished when the credential service's answer arrived, so the answer ends
// nothing — the runner does not resume a stage that already has a result — and
// the account that served it served all of it.
func doneThenExhausted(t *testing.T, handle func() *runner.RunHandle, reset time.Time, work step) step {
	t.Helper()
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := work(t, ctx, req)
		if err != nil {
			return res, err
		}
		handle().NoteExhausted(reset)
		<-ctx.Done()
		return res, nil
	}
}

// TestAutoOffMeansExplicitOnly is D875 (§11.4): the switch is off by default
// and off means the run is served by the account the §11.2 chain named and
// nothing else. The walk changes nothing at all about such a run, and an owner
// who is happy with one account never meets it.
func TestAutoOffMeansExplicitOnly(t *testing.T) {
	cases := []struct {
		name string
		node func(*testing.T) string
	}{
		{"the policy carries no switch", func(t *testing.T) string { return autoNode(t, "", accountA, accountB) }},
		{"the policy switches it off", func(t *testing.T) string { return autoNode(t, "false", accountA, accountB) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := autoRun(t, c.node(t))
			handle := watchRun(t)
			runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
			rec, err := h.run(t, exhaustedOnce(t, handle), implement("feature"), review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if n := len(h.agent.calls); n != 3 {
				t.Errorf("%d agent calls; want the interrupted stage waited for and resumed, and the review", n)
			}
			if logs := h.logs.String(); strings.Contains(logs, "account failover") {
				t.Errorf("a run whose owner switched the walk off failed over anyway:\n%s", logs)
			}
			if len(rec.StageAccounts) != 0 {
				t.Errorf("stage_accounts = %+v; want nothing recorded when no account was chosen", rec.StageAccounts)
			}
		})
	}

	t.Run("an explicit account is chosen above the walk", func(t *testing.T) {
		h := autoRunExplicit(t, autoNode(t, "true", accountA, accountB), accountA)
		handle := watchRun(t)
		runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
		rec, err := h.run(t, exhaustedOnce(t, handle), implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if logs := h.logs.String(); strings.Contains(logs, "account failover") {
			t.Errorf("a run that named its own account was moved off it:\n%s", logs)
		}
		if len(rec.StageAccounts) != 0 {
			t.Errorf("stage_accounts = %+v; want nothing recorded for a run that named its own account", rec.StageAccounts)
		}
	})
}

// TestAutoNeverChoosesADisallowedAccount is the floor under the walk
// (stageAccountCandidates): team-a's own allow_repos does not name this
// repository, so the walk passes over it even with the switch on, and the
// second attempt lands on the only admitted account. It sorts first, so a
// candidate list that stopped asking the floor would hand the stage to it —
// the account's own list is what bounds which repositories may use its token,
// and a walk that could reach past it would be a way around claude_accounts.
func TestAutoNeverChoosesADisallowedAccount(t *testing.T) {
	node := autoNodeAdmitting(t, "true", map[string][]string{
		accountA: {"github/other/repo"},
		accountB: {"github/acme/app"},
	})
	h := autoRun(t, node)
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t, exhaustedOnce(t, handle))
	if err != nil {
		t.Fatal(err)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "stage=implement from=default to="+accountB) {
		t.Errorf("the second attempt did not land on the only account this repository is admitted to:\n%s", logs)
	}
	if strings.Contains(logs, accountA) {
		t.Errorf("the walk named an account whose own list does not admit this repository:\n%s", logs)
	}
	if len(rec.StageAccounts) != 1 || rec.StageAccounts["implement"] != config.DefaultClaudeAccount {
		t.Errorf("stage_accounts = %+v; want the account that answered the stage, never one this repository may not use", rec.StageAccounts)
	}
}

// TestAutoNeverSwitchesMidStage is §11.3's third rule (D874): a rate limit
// inside a stage ends that attempt — the stage's transcript, its cost and its
// verdict are one account's — and a stage whose work was already done when the
// answer arrived is not resumed at all, so nothing about it moves either. The
// record is where a mid-stage switch would show: one that advanced the walk
// when the answer arrived, rather than when an attempt ended, would name the
// stage's next account for a stage the first account served to the end.
func TestAutoNeverSwitchesMidStage(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA, accountB))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t,
		withSession(doneThenExhausted(t, handle, time.Now().Add(3*time.Minute), implement("feature")), "", "finished\n"),
		review("ship", 0),
	)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := len(h.agent.calls); n != 2 {
		t.Errorf("%d agent calls; want the implement stage once (its work was done) and the review", n)
	}
	if logs := h.logs.String(); strings.Contains(logs, "account failover") {
		t.Errorf("a stage the exhaustion did not resume was moved to another account:\n%s", logs)
	}
	want := map[string]string{"implement": config.DefaultClaudeAccount, "review": config.DefaultClaudeAccount}
	if len(rec.StageAccounts) != len(want) {
		t.Fatalf("stage_accounts = %+v, want %+v: nothing moved, so every stage stood on the run's own account", rec.StageAccounts, want)
	}
	for stage, account := range want {
		if got := rec.StageAccounts[stage]; got != account {
			t.Errorf("stage_accounts[%s] = %q, want %q", stage, got, account)
		}
	}
}

// TestAutoLogsEveryFailover: every failover is logged, and the line names the
// stage and both accounts and carries no credential. A run stops at the first
// failover it cannot serve, so each stage the walk can move is driven in its
// own run — the implement stage's, and the review stage's, which shares the
// run's job and walks the whole list.
func TestAutoLogsEveryFailover(t *testing.T) {
	cases := []struct {
		stage string
		steps func(t *testing.T, handle func() *runner.RunHandle) []step
	}{
		{"implement", func(t *testing.T, handle func() *runner.RunHandle) []step {
			return []step{exhaustedOnce(t, handle)}
		}},
		{"review", func(t *testing.T, handle func() *runner.RunHandle) []step {
			return []step{implement("feature"), exhaustedOnce(t, handle)}
		}},
	}
	for _, c := range cases {
		t.Run("the "+c.stage+" stage's", func(t *testing.T) {
			h := autoRun(t, autoNode(t, "true", accountA, accountB))
			handle := watchRun(t)
			runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
			rec, err := h.run(t, c.steps(t, handle)...)
			if err != nil {
				t.Fatal(err)
			}
			logs := h.logs.String()
			lines := 0
			for _, l := range strings.Split(logs, "\n") {
				if !strings.Contains(l, "account failover") {
					continue
				}
				lines++
				if !strings.Contains(l, "stage="+c.stage+" from=default to="+accountA) {
					t.Errorf("the failover line does not name what moved: %s", l)
				}
			}
			if lines != 1 {
				t.Errorf("%d failover lines; want the one the walk reached for this stage:\n%s", lines, logs)
			}
			if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, accountA) {
				t.Errorf("rec = %+v; want the run stopped at the account the walk reached:\n%s", rec, logs)
			}
			if strings.Contains(logs, "oauth-token-abcdef") {
				t.Error("the run's log carries the credential")
			}
		})
	}
}

// TestAutoFailoverRecordsTheNewStagesAccount is D876 (§11.5): the record's
// stage_accounts carries the account that served each stage of the run, keyed
// by the stage — the answer the record's own oauth_account cannot give, which
// keeps the meaning it has (the account the run used last) and is not written
// by the walk. The account every stage the walk serves stands on is the run's
// own, and TestAutoNeverSwitchesMidStage pins that map; what the field must
// never carry is an account that did not answer, and a failover is where that
// would show: the stage's attempts were the default account's, the walk
// reached the second account and the run stopped there, so the field keeps the
// account that answered and never the one the walk reached. The line is the
// log's half of the same fact.
func TestAutoFailoverRecordsTheNewStagesAccount(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA, accountB))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t, exhaustedOnce(t, handle))
	if err != nil {
		t.Fatal(err)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "account failover: the stage's next attempt is the next account") {
		t.Fatalf("the walk did not reach the second account, so there is no failover to record:\n%s", logs)
	}
	if len(rec.StageAccounts) != 1 || rec.StageAccounts["implement"] != config.DefaultClaudeAccount {
		t.Errorf("stage_accounts = %+v; want only the account that answered the stage", rec.StageAccounts)
	}
	// The walk writes the per-stage map and never the run's own account: that
	// field is the account the run used last, as the answers of its calls
	// report it (recordCredential), and a local run's calls reach no service
	// that reports one — so a walk that wrote it would be its only writer here.
	if rec.OAuthAccount != "" {
		t.Errorf("oauth_account = %q; want it untouched: the walk writes the per-stage map, not the run's own account", rec.OAuthAccount)
	}
}

// TestAutoWalksTheAccountsInTheListOrder is A7's red step (plan §A7, spec
// §11.3, D874): with claude_account_auto on, a stage whose first account is
// rate-limited makes its next attempt on the second account of the list
// `fugaro accounts ls` prints, and the failover line names the stage and both
// accounts. It was written before the walk existed and was red in the shape
// D875 makes the default: the account was the launch's single account, the
// second was never reached, and nothing wrote the stage's account anywhere.
//
// The second account of the list is also as far as a run can go, and that half
// is the refusal: a run holds one account's token — a local OAuth run has the
// one CLAUDE_CODE_OAUTH_TOKEN its stages run with in its environment
// (internal/agent/env.go), a Cloud Run job its mounted account — so the stage
// the walk reached is stopped named rather than run as an account that did not
// answer it, and the record keeps nothing for it.
func TestAutoWalksTheAccountsInTheListOrder(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA, accountB))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t, exhaustedOnce(t, handle))
	if err != nil {
		t.Fatal(err)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "account failover") {
		t.Fatalf("the run's log carries no failover line, so the walk never reached the second account:\n%s", logs)
	}
	for _, want := range []string{"stage=implement", "from=default", "to=" + accountA} {
		if !strings.Contains(logs, want) {
			t.Errorf("the failover line does not name %s:\n%s", want, logs)
		}
	}
	if n := len(h.agent.calls); n != 1 {
		t.Errorf("%d agent calls; want only the attempt the exhaustion ended: the second account is one this run's token cannot serve", n)
	}
	if rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v; want the run stopped at the account it cannot serve:\n%s", rec, logs)
	}
	for _, want := range []string{accountA, config.DefaultClaudeAccount} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("the refusal does not name %s: %s", want, rec.Reason)
		}
	}
	if len(rec.StageAccounts) != 1 || rec.StageAccounts["implement"] != config.DefaultClaudeAccount {
		t.Errorf("stage_accounts = %+v; want the account that answered the stage, never the one this run cannot serve", rec.StageAccounts)
	}
	if strings.Contains(logs, "oauth-token-abcdef") {
		t.Error("the run's log carries the credential")
	}
}
