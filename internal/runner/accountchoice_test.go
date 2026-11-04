package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
