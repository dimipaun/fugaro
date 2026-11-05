package runner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

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
