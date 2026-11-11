package runner

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// inflightRun is the one window a repoint leaves open (docs/account-aliases/
// reconciliation.md F3): a mixed api-key run that was launched when the
// published node named launch for its review stages — so launch is the account
// its task carries (spec.OAuthToken) and the account its launch resolved — and
// that boots against a node now naming node for those stages, the publish a
// repoint carried in between. The runner's own environment holds mixedToken,
// the token of the job this run is in, which is where a stage's token is read
// from (agent.OAuthStageEnv).
//
// job is the job that run is in: the zero value is the per-repository job of
// (slug, WorkflowVariant(workflow, token)), whose variant for launch is the one
// that mounts launch's token, and a job carrying a Profile is the shared
// project job the launcher places the run on. The two are not one placement,
// and only the first mounts the launch's account: a mixed api-key run is placed
// on the profile's *no-account* project job, because a named Claude account is
// an account of the axis for an OAuth run and for no other
// (projectJobAccountFor, internal/cli), and that job's container holds whatever
// token config.ProjectJobTokenAccount names for it — the profile's `default`
// entry's when it lists one, none otherwise — never launch's. The guard's
// reason is therefore the account the run's launch resolved and the node's
// admission of it, which hold in both placements, and never a token the job
// holds, which holds in one.
//
// It builds the run and resolves nothing: each test calls resolveMixedStages
// the way bootstrap does, so a fixture meant to refuse reads the refusal
// itself. The log is a buffer the caller reads for the difference the runner
// is expected to report. node is the published claude_account_stages map, and
// accounts is the node's accounts block, which is what holds the launch's
// account to its own allow_repos.
func inflightRun(t *testing.T, launch, node string, accounts map[string]projectpolicy.Account, job jobTriple, log io.Writer) *run {
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
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return &run{
		cfg: &config.Config{Agent: config.Agent{Auth: "api-key",
			Models: config.ModelRoles{Coder: "vendor/model-a", Reviewer: "claude-opus-5"}}},
		spend: Spend{Mode: "enforce"},
		job:   job,
		spec:  &task.Spec{Version: 1, RunID: "20261111-090000-abcd", Repo: "acme/app", Ref: "main", OAuthToken: launch},
		d: Deps{Env: parent, Log: slog.New(slog.NewTextHandler(log, nil)),
			Store:               inflightStore(t, slug),
			ManagedSettingsPath: filepath.Join(t.TempDir(), "managed-settings.json")},
		rec: &runstore.Record{Version: 1, RunID: "20261111-090000-abcd"},
		env: env,
		projPolicy: policyRead{done: true, found: true, policy: &projectpolicy.Policy{
			Version: projectpolicy.Version, Accounts: accounts, ClaudeAccountStages: stages}},
	}
}

// inflightStore is the run's store, opened at slug on a bucket of its own: the
// repository the guard names is read off this run's job, and the slug is the
// half of that read a per-repository run stands on (providerForSlug).
func inflightStore(t *testing.T, slug string) *runstore.Store {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	return runstore.Open(bucket, slug, "20261111-090000-abcd")
}

// TestMixedRunKeepsItsLaunchAccountAfterARepoint is the ruling's own case
// (docs/account-aliases/spec.md §5, D2094): a repoint moves every NEW launch
// and no run in flight. This run is in the per-repository job its launch was
// placed on — the variant of team-a, the account its task carries and the only
// job that mounts team-a's token — and the node it boots against, published
// after the repoint, now names team-b for both review stages. The stages are
// served with the run's own account anyway: the node's accounts.team-a still
// admits this repository, so the account the launch resolved may keep serving
// it, and the difference is logged. Running them as team-b would put a token in
// the stage that this job does not hold and write an account into the record
// that did not answer it.
func TestMixedRunKeepsItsLaunchAccountAfterARepoint(t *testing.T) {
	var log bytes.Buffer
	admit := []string{"github/acme/*"}
	r := inflightRun(t, "team-a", "team-b", map[string]projectpolicy.Account{
		"team-a": {AllowRepos: admit},
		"team-b": {AllowRepos: admit},
	}, jobTriple{}, &log)
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
			r := inflightRun(t, "team-a", node, tc.accounts, jobTriple{}, &log)
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
		}, jobTriple{}, &log)
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
		}, jobTriple{}, &log)
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

// TestMixedRunInAProjectJobKeepsItsLaunchAccount is the guard on the other
// placement a mixed api-key run can have: the shared project job the launcher
// resolves for a repository whose checkout workflow clones through a profile.
// A named Claude account is an account of that job's axis for an OAuth run and
// for no other (projectJobAccountFor, internal/cli), so a mixed run — an
// api-key run by definition — is placed on the profile's *no-account* job,
// whose container holds config.ProjectJobTokenAccount's token for it, the
// profile's `default` entry's when it lists one and none otherwise, and never
// team-a's. The guard is the same run there as on a per-repository job, because
// what it stands on is the account the launch resolved and the node's admission
// of it: the repointed stage keeps team-a and the difference is logged, and a
// node that stops admitting team-a stops the run.
//
// The repository that admission is read about is the one this run's job names,
// and a project job's provider is the one its own name pins — the arm
// checkProviderAdmission reads (project.go), which tolerates a store slug that
// agrees with neither this repository nor any provider kind because a project
// job's provider is never derived from it. The second subtest is that state:
// the guard has to read the job's provider there or it would refuse a stage
// over a repository this run was already admitted to.
func TestMixedRunInAProjectJobKeepsItsLaunchAccount(t *testing.T) {
	job := jobTriple{Profile: "standard", Provider: "github"}
	accounts := map[string]projectpolicy.Account{
		"team-a": {AllowRepos: []string{"github/acme/*"}},
		"team-b": {AllowRepos: []string{"github/acme/*"}},
	}
	// served resolves the fixture's stages and fails the test on a refusal.
	served := func(t *testing.T, r *run, log *bytes.Buffer) {
		t.Helper()
		mixed, err := r.resolveMixedStages(context.Background())
		if err != nil {
			t.Fatalf("a run in a project job was stopped by a repoint: %v", err)
		}
		r.mixedStages = mixed
		for _, stage := range []string{"review", "adversarial"} {
			if got := r.stageAccount(stage); got != "team-a" {
				t.Errorf("stage %s is served by %q, want the account this run was launched with", stage, got)
			}
			// The stage's environment is what reads the account's token out of
			// the run's own, and what the account is recorded as answering it
			// is read at (stageAgentEnv).
			keys := envKeysOf(stageEnvOf(t, r, stage))
			if keys["CLAUDE_CODE_OAUTH_TOKEN"] != mixedToken {
				t.Errorf("stage %s does not present this job's own token: %v", stage, keys)
			}
			if got := r.rec.StageAccounts[stage]; got != "team-a" {
				t.Errorf("stage_accounts[%s] = %q, want the account that answered it", stage, got)
			}
		}
		if got := log.String(); !strings.Contains(got, "team-a") || !strings.Contains(got, "team-b") {
			t.Errorf("the run does not log the difference the repoint made: %s", got)
		}
	}

	t.Run("a repoint keeps the account the launch resolved", func(t *testing.T) {
		var log bytes.Buffer
		served(t, inflightRun(t, "team-a", "team-b", accounts, job, &log), &log)
	})

	t.Run("the job's provider names the repository, not the slug", func(t *testing.T) {
		var log bytes.Buffer
		r := inflightRun(t, "team-a", "team-b", accounts, job, &log)
		// A slug no provider kind derives: the store's slug is the per-
		// repository half of this read, and a project job's job is placed by
		// its name whatever the slug says (checkProviderAdmission, project.go).
		r.d.Store = inflightStore(t, "acme-app")
		served(t, r, &log)
	})

	t.Run("a node that stops admitting the account stops the run", func(t *testing.T) {
		var log bytes.Buffer
		notAdmitted := map[string]projectpolicy.Account{
			"team-a": {AllowRepos: []string{"github/other/*"}},
			"team-b": {AllowRepos: []string{"github/acme/*"}},
		}
		r := inflightRun(t, "team-a", "team-b", notAdmitted, job, &log)
		mixed, err := r.resolveMixedStages(context.Background())
		if err == nil {
			t.Fatalf("a stage served by an account the node does not admit was resolved anyway: %v", mixed)
		}
		for _, want := range []string{"team-a", "allow_repos", "github/acme/app"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q lacks %q", err, want)
			}
		}
		if len(r.rec.StageAccounts) != 0 {
			t.Errorf("a refused run recorded a stage account: %v", r.rec.StageAccounts)
		}
	})
}
