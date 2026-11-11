package runner

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// inflightRun is the one window a repoint leaves open (docs/account-aliases/
// reconciliation.md F3): a mixed api-key run that was launched when the
// published node named launch for its review stages — so launch is the account
// its task carries (spec.OAuthToken) and the account whose token its job
// mounts, the value the runner's own environment holds here — and that boots
// against a node now naming node for those stages, the publish a repoint
// carried in between.
//
// It builds the run and resolves nothing: each test calls resolveMixedStages
// the way bootstrap does, so a fixture meant to refuse reads the refusal
// itself. The log is a buffer the caller reads for the difference the runner
// is expected to report. node is the published claude_account_stages map, and
// accounts is the node's accounts block, which is what holds the launch's
// account to its own allow_repos.
func inflightRun(t *testing.T, launch, node string, accounts map[string]projectpolicy.Account, log io.Writer) *run {
	t.Helper()
	parent := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=" + mixedToken}
	env, _, err := agent.BuildEnv(parent, agent.EnvSpec{Auth: "api-key",
		Gateway: &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: gatewayToken}})
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string]string{}
	for _, stage := range config.MixedAccountStages {
		stages[stage] = node
	}
	return &run{
		cfg: &config.Config{Agent: config.Agent{Auth: "api-key",
			Models: config.ModelRoles{Coder: "vendor/model-a", Reviewer: "claude-opus-5"}}},
		spend: Spend{Mode: "enforce"},
		spec:  &task.Spec{Version: 1, RunID: "20261111-090000-abcd", Repo: "acme/app", Ref: "main", OAuthToken: launch},
		// The job the launch landed on names its provider, which is how a
		// project job tells this run which repository its allow-lists are
		// written about (projectpolicy.Canonical).
		job: jobTriple{Profile: "standard", Provider: "github"},
		d: Deps{Env: parent, Log: slog.New(slog.NewTextHandler(log, nil)),
			ManagedSettingsPath: filepath.Join(t.TempDir(), "managed-settings.json")},
		rec: &runstore.Record{Version: 1, RunID: "20261111-090000-abcd"},
		env: env,
		projPolicy: policyRead{done: true, found: true, policy: &projectpolicy.Policy{
			Version: projectpolicy.Version, Accounts: accounts, ClaudeAccountStages: stages}},
	}
}

// TestMixedRunKeepsItsLaunchAccountAfterARepoint is the ruling's own case
// (docs/account-aliases/spec.md §5, D2094): a repoint moves every NEW launch
// and no run in flight. This run's task carries team-a — the account its job
// mounts the token of — and the node it boots against, published after the
// repoint, now names team-b for both review stages. The stages are served with
// the run's own account anyway: the node's accounts.team-a still admits this
// repository, so the account the launch resolved may keep serving it, and the
// difference is logged. Running them as team-b would put a token in the stage
// that this job does not hold and write an account into the record that did
// not answer it.
func TestMixedRunKeepsItsLaunchAccountAfterARepoint(t *testing.T) {
	var log bytes.Buffer
	admit := []string{"github/acme/*"}
	r := inflightRun(t, "team-a", "team-b", map[string]projectpolicy.Account{
		"team-a": {AllowRepos: admit},
		"team-b": {AllowRepos: admit},
	}, &log)
	mixed, err := r.resolveMixedStages(context.Background())
	if err != nil {
		t.Fatalf("a run in flight was stopped by a repoint: %v", err)
	}
	r.mixedStages = mixed
	for _, stage := range []string{"review", "adversarial"} {
		if got := r.stageAccount(stage); got != "team-a" {
			t.Errorf("stage %s is served by %q, want the account this run was launched with", stage, got)
		}
		keys := envKeysOf(stageEnvOf(t, r, stage))
		if keys["CLAUDE_CODE_OAUTH_TOKEN"] != mixedToken {
			t.Errorf("stage %s does not present the token this job mounts: %v", stage, keys)
		}
		if got := r.rec.StageAccounts[stage]; got != "team-a" {
			t.Errorf("stage_accounts[%s] = %q, want the account that answered it", stage, got)
		}
	}
	if got := log.String(); !strings.Contains(got, "team-a") || !strings.Contains(got, "team-b") {
		t.Errorf("the run does not log the difference the repoint made: %s", got)
	}
}

// TestMixedRunRefusesALaunchAccountTheNodeDoesNotAdmit: the fallback is held
// to the node's own admission (D2094). Here the node no longer lets team-a's
// token serve this repository, so the repointed stage may not be served by the
// run's own account either — and it may not be served by team-b, whose token
// this job does not hold. It is refused where the stages are resolved, before
// any stage of the run starts, rather than run as an account nothing bounds.
func TestMixedRunRefusesALaunchAccountTheNodeDoesNotAdmit(t *testing.T) {
	const node = "team-b"
	for _, tc := range []struct {
		name     string
		accounts map[string]projectpolicy.Account
		want     []string
	}{
		{
			"the node's list for the launch account excludes this repository",
			map[string]projectpolicy.Account{
				"team-a": {AllowRepos: []string{"github/other/*"}},
				"team-b": {AllowRepos: []string{"github/acme/*"}},
			},
			[]string{"team-a", "allow_repos", "github/acme/app"},
		},
		{
			"the node no longer defines the launch account",
			map[string]projectpolicy.Account{
				"team-b": {AllowRepos: []string{"github/acme/*"}},
			},
			[]string{"team-a", "accounts", "team-b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			r := inflightRun(t, "team-a", node, tc.accounts, &log)
			mixed, err := r.resolveMixedStages(context.Background())
			if err == nil {
				t.Fatalf("a stage served by an account the node does not admit was resolved anyway: %v", mixed)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("the refusal is not one line: %q", err)
			}
			if len(r.rec.StageAccounts) != 0 {
				t.Errorf("a refused run recorded a stage account: %v", r.rec.StageAccounts)
			}
		})
	}
}

// TestMixedRunWithoutARepointIsUnchanged is the guard's other half: the node
// naming the account the run was launched with resolves exactly as it did
// before the guard existed, with no difference to report, and a run that holds
// no named account at all — spec.OAuthToken is the unsuffixed default, which a
// mixed run cannot launch on (mixedRunAccount, internal/cli) but the guard must
// not read as one — takes the node's name for the stage.
func TestMixedRunWithoutARepointIsUnchanged(t *testing.T) {
	admit := []string{"github/acme/*"}
	t.Run("the node names the launch's own account", func(t *testing.T) {
		var log bytes.Buffer
		r := inflightRun(t, "team-a", "team-a", map[string]projectpolicy.Account{
			"team-a": {AllowRepos: admit},
		}, &log)
		mixed, err := r.resolveMixedStages(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		r.mixedStages = mixed
		if got := r.stageAccount("review"); got != "team-a" {
			t.Errorf("stage review is served by %q, want the node's own name", got)
		}
		if got := log.String(); got != "" {
			t.Errorf("a run with nothing to reconcile logged a difference: %s", got)
		}
	})
	t.Run("the run holds no named account", func(t *testing.T) {
		var log bytes.Buffer
		r := inflightRun(t, config.DefaultClaudeAccount, "team-a", map[string]projectpolicy.Account{
			"team-a": {AllowRepos: admit},
		}, &log)
		mixed, err := r.resolveMixedStages(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		r.mixedStages = mixed
		if got := r.stageAccount("review"); got != "team-a" {
			t.Errorf("stage review is served by %q, want the node's own name", got)
		}
	})
}
