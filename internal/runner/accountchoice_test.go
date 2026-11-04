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

// TestAutoNeverChoosesADisallowedAccount is the floor under the walk: team-b's
// own allow_repos does not name this repository, so the walk passes over it
// even with the switch on — the second attempt lands on the only admitted
// account, and no account this repository may not use is ever named.
func TestAutoNeverChoosesADisallowedAccount(t *testing.T) {
	node := autoNodeAdmitting(t, "true", map[string][]string{
		accountA: {"github/acme/app"},
		accountB: {"github/other/repo"},
	})
	h := autoRun(t, node)
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t, exhaustedOnce(t, handle), implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "stage=implement from=default to="+accountA) {
		t.Errorf("the second attempt did not land on the only account this repository is admitted to:\n%s", logs)
	}
	if strings.Contains(logs, accountB) {
		t.Errorf("the walk named an account whose own list does not admit this repository:\n%s", logs)
	}
	if got := rec.StageAccounts["implement"]; got != accountA {
		t.Errorf("stage_accounts[implement] = %q, want %q", got, accountA)
	}
}

// TestAutoStopsWhenEveryAccountHasRefusedTheStage is the end of the walk: with
// one account admitted for the repository the list holds two entries, both of
// them already refused, and the stage stops named rather than spending a third
// attempt on an account the service has just said no to.
func TestAutoStopsWhenEveryAccountHasRefusedTheStage(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t, exhaustedOnce(t, handle), exhaustedOnce(t, handle))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "every account this stage may be served by has already refused it") {
		t.Fatalf("rec = %+v, calls = %d; want the run stopped when the walk ran out of accounts:\n%s", rec, len(h.agent.calls), h.logs.String())
	}
	if n := len(h.agent.calls); n != 2 {
		t.Errorf("%d agent calls; want the two the exhaustion ended and no third", n)
	}
	for _, want := range []string{accountA, config.DefaultClaudeAccount, "fugaro accounts ls"} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("the refusal does not name %s: %s", want, rec.Reason)
		}
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
// stage and both accounts and carries no credential. Two stages that fail over
// leave two lines: a run is read afterwards from its log, and a stage that was
// moved without a line would leave the account only in a record no one reads
// as it happens.
func TestAutoLogsEveryFailover(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA, accountB))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t,
		exhaustedOnce(t, handle), implement("feature"),
		exhaustedOnce(t, handle), review("ship", 0),
	)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	logs := h.logs.String()
	lines := 0
	for _, l := range strings.Split(logs, "\n") {
		if !strings.Contains(l, "account failover") {
			continue
		}
		lines++
		if !strings.Contains(l, "from=default to="+accountA) {
			t.Errorf("the failover line does not name the accounts it moved between: %s", l)
		}
	}
	if lines != 2 {
		t.Errorf("%d failover lines; want one for each stage that was moved:\n%s", lines, logs)
	}
	for _, want := range []string{"stage=implement", "stage=review"} {
		if !strings.Contains(logs, want+" from=default to="+accountA) {
			t.Errorf("no failover line names %s:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "oauth-token-abcdef") {
		t.Error("the run's log carries the credential")
	}
}

// TestAutoFailoverRecordsTheNewStagesAccount is D876 (§11.5): every stage's
// account is written into the record's stage_accounts, keyed by the stage, so
// what the log says as it happens is readable afterwards per stage — the
// answer the record's own oauth_account cannot give, which keeps the meaning
// it has (the account the run used last) and is not written here.
func TestAutoFailoverRecordsTheNewStagesAccount(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA, accountB))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t, exhaustedOnce(t, handle), implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	want := map[string]string{"implement": accountA, "review": config.DefaultClaudeAccount}
	if len(rec.StageAccounts) != len(want) {
		t.Fatalf("stage_accounts = %+v, want %+v", rec.StageAccounts, want)
	}
	for stage, account := range want {
		if got := rec.StageAccounts[stage]; got != account {
			t.Errorf("stage_accounts[%s] = %q, want %q", stage, got, account)
		}
	}
	if rec.OAuthAccount != "" {
		t.Errorf("oauth_account = %q; want it untouched: the walk writes the per-stage map, not the run's own account", rec.OAuthAccount)
	}
}

// TestAutoWalksTheAccountsInTheListOrder is A7's red step (plan §A7, spec
// §11.3, D874): with claude_account_auto on, a stage whose first account is
// rate-limited makes its next attempt on the second account of the list
// `fugaro accounts ls` prints, and the failover line names the stage and both
// accounts. It is written before the walk exists, so it is red in the shape
// D875 makes the default: the account is the launch's single account, the
// second is never reached, and nothing writes the stage's account anywhere.
func TestAutoWalksTheAccountsInTheListOrder(t *testing.T) {
	h := autoRun(t, autoNode(t, "true", accountA, accountB))
	handle := watchRun(t)
	runner.SetCredsWait(t, func(context.Context, time.Duration) error { return nil })
	rec, err := h.run(t,
		withSession(exhaustedStage(t, handle, time.Now().Add(3*time.Minute)), "", "interrupted\n"),
		implement("feature"),
		review("ship", 0),
	)
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
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
	if got := rec.StageAccounts["implement"]; got != accountA {
		t.Errorf("stage_accounts[implement] = %q, want the account that took over, %q", got, accountA)
	}
	if strings.Contains(logs, "oauth-token") {
		t.Error("the run's log carries the credential")
	}
}
