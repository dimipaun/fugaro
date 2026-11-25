package runner

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// autoFakeRun is the walk's stage-start decision without a database: the run's
// own read of the project's policy is already answered (the memo a real run
// fills from the node), so the rules that need a whole node document — the
// stage scope against the profiles block, the refusal for an account the run's
// own credential is not, and the end of the list — are pinned here, where the
// node can be built as the walk reads it. failovers is how many attempts of
// the stage the credential service has already ended; local is a run of
// `fugaro exec --task-file` rather than a Cloud Run job (Deps.Local), and the
// refusal is asked the same way in both; holds is the account the run's own
// token belongs to, the empty string being the default.
type autoFakeRun struct {
	r    *run
	logs *bytes.Buffer
}

func newAutoFakeRun(t *testing.T, local bool, reviewJob bool, p *projectpolicy.Policy, holds string, failovers map[string]int) autoFakeRun {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	r := &run{
		d: Deps{
			Local: local, Log: slog.New(slog.NewTextHandler(logs, nil)),
			Store: runstore.Open(bucket, slug, "20261104-033534-1422"),
		},
		cfg:  &config.Config{Agent: config.Agent{Auth: "oauth"}},
		spec: &task.Spec{Version: 1, RunID: "20261104-033534-1422", Repo: "acme/app", Ref: "main", Workflow: "app", OAuthToken: holds},
		rec:  &runstore.Record{Version: 1, RunID: "20261104-033534-1422"},
	}
	if reviewJob {
		r.d.Env = []string{EnvReviewJob + "=1"}
	}
	r.projPolicy = policyRead{done: true, policy: p, found: true}
	r.accounts = accountChoice{read: true, auto: true, failovers: failovers}
	return autoFakeRun{r: r, logs: logs}
}

// autoPolicyNode is the published policy these tests decide on: two accounts
// admitted for the fixture repository, and one profile whose job variants are
// built for team-a alone — the review class of spec §11.3.
func autoPolicyNode() *projectpolicy.Policy {
	return &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		AllowRepos: []string{"github/acme/app"},
		Accounts: map[string]projectpolicy.Account{
			"team-a": {AllowRepos: []string{"github/acme/app"}},
			"team-b": {AllowRepos: []string{"github/acme/app"}},
		},
		Profiles: map[string]projectpolicy.Profile{
			projectpolicy.DefaultProfileName: {JobProfile: config.JobProfile{
				Provider:       "github",
				AllowRepos:     []string{"github/acme/app"},
				ClaudeAccounts: []string{config.DefaultClaudeAccount, "team-a"},
			}},
		},
	}
}

// TestAutoReviewStageWalksOnlyReviewClassAccounts is spec §11.3's second rule,
// and the negative test that keeps stage scope from collapsing back into the
// repository's allow-list: an account the repository is admitted to but that
// no profile's job is built for (team-b here) is never chosen for the
// independent review job's stage, whatever the accounts block says — the walk
// that stage stands on is the review class. The run holds the account the walk
// reached, which is the world a job that mounts the accounts the walk walks
// would be: the design's per-account review job, which this release does not
// deploy (the refusal below is what a run that holds one account does).
func TestAutoReviewStageWalksOnlyReviewClassAccounts(t *testing.T) {
	h := newAutoFakeRun(t, true, true, autoPolicyNode(), "team-a", map[string]int{"review": 1})
	if reason := h.r.checkStageAccount(context.Background(), "review"); reason != "" {
		t.Fatalf("the review job's stage was refused: %s", reason)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "from=default to=team-a") || !strings.Contains(logs, "stage=review") {
		t.Errorf("the review job's stage did not fail over to its own class:\n%s", logs)
	}
	if strings.Contains(logs, "team-b") {
		t.Errorf("the walk reached an account no review job is built for:\n%s", logs)
	}
	if got := h.r.rec.StageAccounts["review"]; got != "team-a" {
		t.Errorf("stage_accounts[review] = %q, want the review-class account team-a", got)
	}
}

// TestAutoReviewStageNeverLeavesTheRepositorysAdmission is the other half of
// the same rule, on a node whose two rules disagree: the repository's own
// admission is the floor, so an account whose allow_repos does not name this
// repository is no candidate however a profile lists it. It is the rule
// claudeAccountAllowed asks before a launch, asked again here because the walk
// chooses after the launch, and team-a sorts first so that dropping the floor
// would hand the stage to it.
func TestAutoReviewStageNeverLeavesTheRepositorysAdmission(t *testing.T) {
	p := &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		AllowRepos: []string{"github/acme/app"},
		Accounts: map[string]projectpolicy.Account{
			"team-a": {AllowRepos: []string{"github/other/repo"}},
			"team-b": {AllowRepos: []string{"github/acme/app"}},
		},
		Profiles: map[string]projectpolicy.Profile{
			projectpolicy.DefaultProfileName: {JobProfile: config.JobProfile{
				Provider:       "github",
				AllowRepos:     []string{"github/acme/app"},
				ClaudeAccounts: []string{config.DefaultClaudeAccount, "team-a", "team-b"},
			}},
		},
	}
	h := newAutoFakeRun(t, true, true, p, "team-b", map[string]int{"review": 1})
	if reason := h.r.checkStageAccount(context.Background(), "review"); reason != "" {
		t.Fatalf("the review job's stage was refused: %s", reason)
	}
	if got := h.r.rec.StageAccounts["review"]; got != "team-b" {
		t.Errorf("stage_accounts[review] = %q, want team-b: team-a's own list does not admit this repository", got)
	}
	if strings.Contains(h.logs.String(), "team-a") {
		t.Errorf("the walk chose an account this repository may not use:\n%s", h.logs.String())
	}
}

// TestAutoRefusesAnAccountTheJobDoesNotHold is the refusal A7 writes and the
// limit it ships with, asked the same way wherever the run stands: a run holds
// one account's token — a Cloud Run job mounts it before the run starts, and a
// local OAuth run's process environment holds the one CLAUDE_CODE_OAUTH_TOKEN
// its stages run with — so a stage whose walk reached another account is
// refused rather than run, never a silent fallback, and never a record that
// names an account that did not answer. The refusal the project job already
// has (checkProjectJobAccount) is untouched by this.
func TestAutoRefusesAnAccountTheJobDoesNotHold(t *testing.T) {
	for _, c := range []struct {
		name  string
		local bool
	}{
		{"a local run", true},
		{"a Cloud Run job", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newAutoFakeRun(t, c.local, true, autoPolicyNode(), "", map[string]int{"review": 1})
			reason := h.r.checkStageAccount(context.Background(), "review")
			if reason == "" {
				t.Fatal("a stage whose account the run's own credential is not was allowed to run")
			}
			for _, want := range []string{"team-a", config.DefaultClaudeAccount} {
				if !strings.Contains(reason, want) {
					t.Errorf("the refusal does not name %s: %s", want, reason)
				}
			}
			if !strings.Contains(reason, "mounted into its job before the run starts") {
				t.Errorf("the refusal does not say why this run cannot serve the stage from that account: %s", reason)
			}
			if len(h.r.rec.StageAccounts) != 0 {
				t.Errorf("a refused stage wrote stage_accounts: %+v", h.r.rec.StageAccounts)
			}
		})
	}
}

// TestAutoStopsWhenEveryAccountHasRefusedTheStage is the end of the walk: with
// one account admitted for the repository the list holds two entries, both of
// them already refused, and the stage stops named rather than spending a third
// attempt on an account the service has just said no to.
func TestAutoStopsWhenEveryAccountHasRefusedTheStage(t *testing.T) {
	h := newAutoFakeRun(t, true, true, autoPolicyNode(), "team-a", map[string]int{"review": 2})
	reason := h.r.checkStageAccount(context.Background(), "review")
	if !strings.Contains(reason, "every account this stage may be served by has already refused it") {
		t.Fatalf("the stage was allowed past the end of the list: %q", reason)
	}
	for _, want := range []string{"team-a", config.DefaultClaudeAccount, "fugaro accounts ls"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the refusal does not name %s: %s", want, reason)
		}
	}
	if len(h.r.rec.StageAccounts) != 0 {
		t.Errorf("a stage the walk ran out of accounts for wrote stage_accounts: %+v", h.r.rec.StageAccounts)
	}
}

// TestAccountFailoverIsClosedToTheAccountsOwnAllowance is the walk's
// classifier, and A7b's whole gate on it (spec §11.3): only a stage whose own
// result event reported an error *and* whose text is one of B5's allowance
// phrases *and* that ran on the account's own OAuth token advances the walk.
// Everything else is an ordinary stage failure, which is the direction to fail
// in — a walk that moved on a text that only mentions a limit would move the
// owner's run off an account that answered it, and the run's record would name
// an account that produced none of it.
//
// The phrase set itself is B5's and is pinned by his own table
// (TestSubscriptionUsageLimitIsTheAccountsOwnSignal); what this test pins is
// the conjunction the walk reads it through, so the rows below are one shape
// per half of it rather than a second copy of that table.
//
// Mutation (drop res.IsError): the "reported no error" rows classify, so a
// stage that succeeded — a task or a repository that is about usage limits puts
// the words in its own result text — moves the walk.
// Mutation (ask subscriptionUsageLimit without the auth, or answer "" for it):
// the api-key and cascade rows classify, so a run that holds no subscription
// allowance of its own is moved off its credential.
// Mutation (widen a phrase, in subscriptionUsageLimit): the lookalike row
// classifies; B5's table holds that half.
func TestAccountFailoverIsClosedToTheAccountsOwnAllowance(t *testing.T) {
	const wall = "Claude AI usage limit reached|1760000000"
	cases := []struct {
		name string
		auth string
		res  agent.Result
		want bool
	}{
		{"the CLI's own allowance sentence", "oauth", agent.Result{IsError: true, Text: wall}, true},
		{"the CLI's newer wording", "oauth", agent.Result{IsError: true, Text: "You've hit your limit - resets at 4pm (UTC)"}, true},
		{"the API refusal of a spent token", "oauth", agent.Result{IsError: true, Text: `API Error: 429 {"type":"error","error":{"type":"rate_limit_error"}}`}, true},
		{"a stage that reported no error", "oauth", agent.Result{Text: wall}, false},
		{"the same words on an api-key run", "api-key", agent.Result{IsError: true, Text: wall}, false},
		{"the same words on a cascade run", config.AuthCascade, agent.Result{IsError: true, Text: wall}, false},
		{"a resource limit, not the allowance", "oauth", agent.Result{IsError: true, Text: "the build failed: disk usage limit reached"}, false},
		{"an ordinary API refusal", "oauth", agent.Result{IsError: true, Text: `API Error: 500 {"type":"error","error":{"type":"api_error"}}`}, false},
		{"an error with no text at all", "oauth", agent.Result{IsError: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newAutoFakeRun(t, true, true, autoPolicyNode(), "", nil)
			h.r.cfg.Agent.Auth = c.auth
			if got := h.r.accountEndedTheAttempt(c.res); got != c.want {
				t.Fatalf("accountEndedTheAttempt(%+v) on a %s run = %v, want %v", c.res, c.auth, got, c.want)
			}
		})
	}
}

// TestNoAccountWalk is CS7: where the credentials service serves the run's
// calls, neither account rule of the runner's is read. The walk
// (accountWalkApplies) and the mix (resolveMixedStages) are the release-1
// arrangement the pool's own rule replaces (D3171), and a run the service serves
// keeps the other one: an unmigrated repository, on the image this release
// ships, is walked and mixed exactly as before.
//
// The pool's half is the same fact from the other side: a member's admission is
// the account's own allow_repos (F10, R20) and never the walk's list, which is
// this run's own and gone where the service serves.
//
// Mutation (restore a walk read, in poolMemberAdmits): judge a Claude OAuth
// member by stageAccountCandidates again, and the member this repository's
// accounts admit but this stage's own list holds no place for is skipped.
func TestNoAccountWalk(t *testing.T) {
	ctx := context.Background()

	// The walk, on a run the credentials service serves: the switch is not read
	// at all, and no stage's start is refused for an account.
	served := newAutoFakeRun(t, false, true, autoPolicyNode(), "", nil)
	served.r.d.Env = append(served.r.d.Env, credentialsServiceURLEnv+"=https://creds.example.run.app")
	if !served.r.chainServed() {
		t.Fatal("a run whose model path is the credentials service's is not read as served by it")
	}
	served.r.accounts = accountChoice{}
	if served.r.accountWalkApplies() {
		t.Fatal("the walk still applies to a run the credentials service serves")
	}
	if served.r.accountAuto(ctx) {
		t.Fatal("Fugaro still chooses an account for a run the credentials service serves")
	}
	for _, stage := range config.RunStages {
		if reason := served.r.checkStageAccount(ctx, stage); reason != "" {
			t.Fatalf("stage %s was refused by the run-level account choice: %s", stage, reason)
		}
	}
	// The same fixture with no credentials service is the release-1 run, and the
	// walk is its own: this is what D3171 removes, and it is not what a served
	// run does.
	if unserved := newAutoFakeRun(t, false, true, autoPolicyNode(), "", nil); !unserved.r.accountWalkApplies() {
		t.Fatal("the walk does not apply to the unmigrated run it is the release-1 arrangement of")
	}

	// The pool's own rule: the account's own admission, asked even where the
	// walk's list is not this run's. team-b is admitted for this repository by
	// its own allow_repos and is no review-class account, so the list this
	// review job's walk stands on (autoPolicyNode's profile is built for the
	// default account and team-a) holds no place for it.
	member := config.PoolMember{Name: "team-b", Type: config.CredentialTypeClaudeOAuth}
	admits, why, err := served.r.poolMemberAdmits(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if !admits {
		t.Fatalf("a pool member whose own allow_repos admits this repository was skipped: %s", why)
	}
	candidates, err := served.r.stageAccountCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(candidates, "team-b") {
		t.Fatalf("the fixture does not tell the two rules apart: the walk's list holds team-b (%v)", candidates)
	}
	// And a member the account's own list leaves this repository out of is
	// skipped, whether or not the walk would have held it.
	foreign := newAutoFakeRun(t, false, false, &projectpolicy.Policy{Version: projectpolicy.Version,
		Accounts: map[string]projectpolicy.Account{"team-c": {AllowRepos: []string{"github/other/repo"}}}}, "", nil)
	if admits, why, err := foreign.r.poolMemberAdmits(ctx, config.PoolMember{Name: "team-c",
		Type: config.CredentialTypeClaudeOAuth}); err != nil || admits || why != poolSkipNotAdmitted {
		t.Fatalf("a member whose account does not admit this repository: admits=%v why=%q err=%v, want the skip", admits, why, err)
	}

	// The mix, on an api-key run: the release-1 environment first, which is the
	// account's token in the stage the node names for it.
	mixed := mixedStageRun(t, map[string]string{"review": "team-a"})
	if mixed.mixedStages["review"] != "team-a" {
		t.Fatalf("the fixture's mix is not the one the node names: %v", mixed.mixedStages)
	}
	if keys := envKeysOf(stageEnvOf(t, mixed, "review")); keys["CLAUDE_CODE_OAUTH_TOKEN"] != mixedToken {
		t.Fatalf("the unmigrated run's stage does not present the account's token: %v", keys)
	}
	// Where the credentials service serves the run, no stage of it holds the
	// account: the service attaches the credential per call, and the stage's
	// calls are the run's own, through the gateway.
	mixed.d.Env = append(mixed.d.Env, credentialsServiceURLEnv+"=https://creds.example.run.app")
	stages, err := mixed.resolveMixedStages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 0 {
		t.Fatalf("the mix still serves stages of a run the credentials service serves: %v", stages)
	}
	mixed.mixedStages = stages
	if keys := envKeysOf(stageEnvOf(t, mixed, "review")); keys["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
		t.Fatalf("a served run's stage carries the account's token: %v", keys)
	}
}
