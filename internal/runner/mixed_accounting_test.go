package runner_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The accounting of the one run the owner's B0 ruling admits
// (docs/design/mixed-credentials.md §5, §11; task B4). A stage the run's
// stage-scoped Claude OAuth account serves sends its calls straight to
// Anthropic, so the gateway's ledger never sees them and the only figure the
// run has for that stage is Claude Code's own: notional dollars, recorded
// beside the gateway's billed ones, in no cap and in no total.

// accountToken is the account's token where the job mounts it: in the
// runner's own environment, long enough that the redactor registers it
// (registerModelCredentials).
const accountToken = "oauth-token-abcdef"

// accountNode is the published policy node that names the stage account: the
// rung the ruling admits, and the only rung that may opt an api-key run into
// the mix (docs/design/mixed-credentials.md §11).
func accountNode(account string) string {
	return fmt.Sprintf(`{"version":%d,"claude_account_stages":{"review":%q}}`, projectpolicy.Version, account)
}

// accountReview is a review stage the account served: it ends the loop with
// its verdict and reports the figure Claude Code has for the stage it paid
// for itself -- a figure the gateway never settled, so it is the only one
// this stage's run ever has.
func accountReview(verdict string, usd float64) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := review(verdict, 0)(t, ctx, req)
		res.CostUSD = usd
		return res, err
	}
}

// boundedStage asserts what bounds a stage the gateway never sees: the
// workflow's own timeouts.stage, and agent.max_budget_usd handed to Claude
// Code as --max-budget-usd. For such a stage those two are the whole of its
// bounds -- no cap of the run's applies, and the run's budget.per_run_usd does
// not bind it (P15) -- so they are pinned on the stage itself rather than on
// the run.
func boundedStage(inner step, within time.Duration, maxUSD float64) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		t.Helper()
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("the stage has no deadline: the workflow's timeouts.stage does not bind it")
		}
		if left := time.Until(dl); left <= 0 || left > within {
			t.Fatalf("the stage's deadline is %v away, want no more than the workflow's timeouts.stage (%v)", left, within)
		}
		if req.MaxBudgetUSD != maxUSD {
			t.Fatalf("the stage's --max-budget-usd is %v, want the workflow's agent.max_budget_usd (%v)", req.MaxBudgetUSD, maxUSD)
		}
		return inner(t, ctx, req)
	}
}

// mixedAccountConfig is the fixture's configuration as the ruling's run has
// it: the coder on the gateway's Claude model and the reviewer on the account,
// with the account stage's own bound and its own budget spelled out.
func mixedAccountConfig(t *testing.T, maxBudgetUSD string, stageWithin string) string {
	t.Helper()
	cfg := gwRolesConfig(t)
	withBudget := strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: 2\n  max_budget_usd: "+maxBudgetUSD+"\n", 1)
	if withBudget == cfg {
		t.Fatal("the fixture has no review_rounds line")
	}
	out := strings.Replace(withBudget, "stage: 2m", "stage: "+stageWithin, 1)
	if out == withBudget {
		t.Fatal("the fixture has no timeouts.stage line")
	}
	return out
}

// mixedAccountRun is the whole run the ruling admits, end to end: the
// fixture's repository with an account's token in the job's environment, the
// node that names the account for review, and the backend the policy read and
// the notional counters need.
func mixedAccountRun(t *testing.T, account string) *bk {
	t.Helper()
	// One reply, for the coder's one call: the account stage's own call never
	// reaches the gateway, so a run that made it there would leave this
	// fixture answering a call no stage of it makes.
	g := newGW(t, mixedAccountConfig(t, "3", "30s"), "enforce", "50", out4k())
	g.deps.Env = append(filterEnv(g.deps.Env, "CLAUDE_CODE_OAUTH_TOKEN"), "CLAUDE_CODE_OAUTH_TOKEN="+accountToken)
	return publishPolicy(t, g, accountNode(account))
}

// TestMixedRunAccountsTheAccountStageAsNotional is Review Focus 7 and 8 of the
// OAuth-reviewer plan, on the run itself: one run, two stages, two
// credentials.
//
//   - The account stage's figure is Claude Code's own, all of it notional, with
//     no call count -- the gateway counted none of its calls -- and the
//     credential that says so. The mutation it fails on is stageTiming keying
//     off the run's gateway rather than off the stage: the stage would then
//     carry the PREVIOUS stage's gateway figure ($0.04, the coder's) and a call
//     count, which is what makes this the test focus 8 asks for.
//   - cost.model_usd is both stages' figures -- notional dollars included --
//     which is what keeps the documented invariant
//     sum(stages[].model_usd) + model_by["unattributed"] == model_usd true
//     exactly, and cost.model_notional_usd says how much of it was notional.
//     Billed dollars are total_usd, and the account's stage is not in them:
//     "the ungated review stage is notional" is this split, and its own
//     --max-budget-usd is what bounds it.
//   - Neither stage warns on the cost cross-check, and the gateway answered
//     exactly one call: the account stage's call went straight to Anthropic.
//     The mutation here is the cross-check comparing the account stage's own
//     figure against the gateway's last stage report -- $0.42 against $0.04 is
//     the 90% disagreement it exists to see, and the warning would name a
//     stage the gateway never served.
func TestMixedRunAccountsTheAccountStageAsNotional(t *testing.T) {
	const (
		coderUSD   = 0.04 // out4k(): 4,000 output tokens on sonnet, settled by the gateway
		accountUSD = 0.42 // what Claude Code reports for the review stage it paid for itself
	)
	b := mixedAccountRun(t, "acme-overflow")
	var st []int
	rec, err := b.run(t,
		calling(&st, implement("feature"), call{sonnet, 4000}),
		boundedStage(accountReview("ship", accountUSD), 30*time.Second, 3))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !eq(stageNames(rec), "implement", "review") {
		t.Fatalf("the run's stages are %v", stageNames(rec))
	}
	coder, account := rec.Stages[0], rec.Stages[1]

	if coder.Credential != "" || coder.Calls == nil || *coder.Calls != 1 ||
		micros(t, coder.ModelUSD) != micros(t, coderUSD) || coder.ModelNotionalUSD != 0 {
		t.Fatalf("the coder stage = %+v, want the gateway's own figure, its call count and no credential", coder)
	}
	if account.Name != "review" || account.Model != opus {
		t.Fatalf("the account stage = %+v, want the reviewer's own model", account)
	}
	if account.Credential != runstore.CredentialOAuth {
		t.Fatalf("the account stage's credential = %q, want %q: the gateway saw none of its calls", account.Credential, runstore.CredentialOAuth)
	}
	if account.Calls != nil {
		t.Fatalf("the account stage carries a call count (%d): the gateway counted no call of it", *account.Calls)
	}
	if micros(t, account.ModelUSD) != micros(t, accountUSD) {
		t.Fatalf("the account stage's model_usd = %v, want Claude Code's own figure %v (not the previous stage's %v)",
			account.ModelUSD, accountUSD, coder.ModelUSD)
	}
	if micros(t, account.ModelNotionalUSD) != micros(t, accountUSD) {
		t.Fatalf("the account stage's model_notional_usd = %v, want all of it notional (%v)", account.ModelNotionalUSD, accountUSD)
	}

	c := rec.Cost
	if c == nil {
		t.Fatal("the record carries no cost breakdown")
	}
	// The whole model figure, both stages': the invariant a reader leans on.
	if micros(t, c.ModelUSD) != micros(t, coderUSD+accountUSD) {
		t.Fatalf("cost.model_usd = %v, want the two stages' figures (%v)", c.ModelUSD, coderUSD+accountUSD)
	}
	if micros(t, c.ModelNotionalUSD) != micros(t, accountUSD) {
		t.Fatalf("cost.model_notional_usd = %v, want the account stage's figure (%v)", c.ModelNotionalUSD, accountUSD)
	}
	if got := stageMicros(t, rec.Stages) + micros(t, c.ModelBy["unattributed"]); got != micros(t, c.ModelUSD) {
		t.Fatalf("sum(stages) + unattributed = %v, cost.model_usd = %v", got, c.ModelUSD)
	}
	if micros(t, c.ModelBy[opus]) != micros(t, accountUSD) || micros(t, c.ModelBy[sonnet]) != micros(t, coderUSD) {
		t.Fatalf("cost.model_by = %v, want the account stage's figure under %s", c.ModelBy, opus)
	}
	// Billed dollars: the account's are in no bill and in no cap.
	if micros(t, c.TotalUSD) != micros(t, coderUSD) {
		t.Fatalf("cost.total_usd = %v, want the gateway's money alone (%v)", c.TotalUSD, coderUSD)
	}
	// A mixed run is an api-key run: its basis and its source do not change
	// because one stage ran elsewhere, which is why a reader splits its dollars
	// on model_notional_usd and never on the basis (P12).
	if c.ModelBasis != runstore.BasisAPIList || c.ModelSource != "gateway" {
		t.Fatalf("cost = %+v, want the gateway's source on the run's own basis", c)
	}
	if micros(t, rec.CostUSD) != micros(t, c.ModelUSD) {
		t.Fatalf("cost_usd = %v, cost.model_usd = %v", rec.CostUSD, c.ModelUSD)
	}
	// The record names the account that served the stage, and no more than that
	// (runstore.Record.StageAccounts, spec §11.5).
	if rec.StageAccounts["review"] != "acme-overflow" || len(rec.StageAccounts) != 1 {
		t.Fatalf("stage_accounts = %v, want the review stage's account alone", rec.StageAccounts)
	}
	// The gateway answered the coder's call and nothing else: the account
	// stage's own call never reached it.
	if n := b.fake.Count(); n != 1 {
		t.Fatalf("the gateway's upstream answered %d calls, want the coder's alone", n)
	}
	// And the account stage's figure is never cross-checked against the
	// gateway's: it has none, and the previous stage's report is $0.04 against
	// this stage's $0.42 -- the 90% disagreement the check exists to see, about
	// a stage the gateway never served. (The coder stage's own warning is not
	// this: the fixture's implement step reports a figure of its own choosing.)
	if logs := b.logs.String(); crossCheckedStage(logs, "review") {
		t.Fatalf("the account stage was cross-checked against the gateway's figure: %s", logs)
	}
	// The account stage's dollars reached the backend's notional counters -- the
	// run's own leaf and the repository's and project's -- while the run's own
	// counted spend stayed the gateway's (P13).
	if got := b.runLeaf("notional"); got != int64(micros(t, accountUSD)) {
		t.Fatalf("the run's notional leaf = %d micros, want %v", got, micros(t, accountUSD))
	}
	if got := b.repoLeaf("notional"); got != int64(micros(t, accountUSD)) {
		t.Fatalf("the repository's notional counter = %d micros, want %v", got, micros(t, accountUSD))
	}
	if got := b.globalLeaf("notional"); got != int64(micros(t, accountUSD)) {
		t.Fatalf("the project's notional counter = %d micros, want %v", got, micros(t, accountUSD))
	}
}

// crossCheckedStage reports whether the run's log carries a cost cross-check
// warning about the named stage.
func crossCheckedStage(logs, stage string) bool {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "cost cross-check") && strings.Contains(line, "stage="+stage) {
			return true
		}
	}
	return false
}

// TestModelCredentialsAreRegisteredBeforeBootstrap: the runner registers the
// model credentials its own environment holds (registerModelCredentials) at
// the top of Run, before bootstrap starts the gateway or the first stage. The
// registration does not ride on the stage that happens to use the token
// (docs/design/mixed-credentials.md §11, "Who holds the token, and when"): a
// mixed run holds the account's token in the runner's environment for the
// whole run, so there must be no stage — the coder's, a review's, or one a
// later recipe adds — whose transcript or log line could carry it because the
// redactor did not know it yet.
//
// The mutation this fails on is moving the call below bootstrap: the token is
// still registered before any stage runs in most orderings, and the run reads
// the same either way — which is exactly why the observation is the run's own
// secret list at the moment Run hands it over.
func TestModelCredentialsAreRegisteredBeforeBootstrap(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Env = append(filterEnv(h.deps.Env, "CLAUDE_CODE_OAUTH_TOKEN"), "CLAUDE_CODE_OAUTH_TOKEN="+accountToken)
	var atStart []string
	runner.OnRun(t, func(r *runner.RunHandle) { atStart = r.Secrets() })
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(atStart, accountToken) {
		t.Fatalf("the run started with a model credential in its environment that the redactor did not know: %v", atStart)
	}
}
