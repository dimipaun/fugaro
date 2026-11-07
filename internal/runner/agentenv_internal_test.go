package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// stagePinsRun builds the minimal run stagePins needs: the gateway on
// (api-key auth, budget enforcing) with coder, reviewer and background
// models, and providers claiming whichever of them start with "vendor/".
func stagePinsRun(t *testing.T, coder, reviewer, background string) *run {
	t.Helper()
	return &run{
		cfg: &config.Config{Agent: config.Agent{
			Auth:   "api-key",
			Models: config.ModelRoles{Coder: coder, Reviewer: reviewer, Background: background},
		}},
		spend: Spend{Mode: "enforce"},
		d: Deps{Providers: map[string]config.ModelProvider{
			"acme": {Kind: config.ModelProviderKind, Models: []string{"vendor/*"}},
		}},
	}
}

// TestStagePinsProviderRouteVars: a stage whose role model is served by a
// provider gets CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS and
// MAX_THINKING_TOKENS=0 added to its pins (the gateway refuses both fields on
// a provider route, forward.go's routedKeys); an all-Claude stage gets
// neither, so its request shape is untouched.
func TestStagePinsProviderRouteVars(t *testing.T) {
	const providerVar1, providerVar2 = "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "MAX_THINKING_TOKENS"

	// All-Claude: coder and reviewer are Claude models, no background set.
	r := stagePinsRun(t, "claude-sonnet-5-5", "claude-opus-5", "")
	for _, stage := range []string{"implement", "review"} {
		pins := r.stagePins(stage)
		if _, ok := pins[providerVar1]; ok {
			t.Errorf("stage %s (all-Claude): %s is set, want unset", stage, providerVar1)
		}
		if _, ok := pins[providerVar2]; ok {
			t.Errorf("stage %s (all-Claude): %s is set, want unset", stage, providerVar2)
		}
	}

	// Mixed, OWNER RULING mirror case (docs/multi-model.md §7): the coder is
	// a provider model, the reviewer stays Claude, and an explicit Claude
	// background keeps config.EffectiveBackground from defaulting the
	// reviewer's background to the provider coder's model too. With the
	// reviewer's own role model and its background both Claude, its stage
	// gets neither variable.
	r = stagePinsRun(t, "vendor/model-a", "claude-opus-5", "claude-haiku-4-5")
	pins := r.stagePins("implement")
	if pins[providerVar1] != "1" || pins[providerVar2] != "0" {
		t.Errorf("coder (provider) stage pins = %+v, want both set", pins)
	}
	pins = r.stagePins("review")
	if _, ok := pins[providerVar1]; ok {
		t.Errorf("reviewer (Claude, explicit Claude background) stage: %s is set, want unset", providerVar1)
	}
	if _, ok := pins[providerVar2]; ok {
		t.Errorf("reviewer (Claude, explicit Claude background) stage: %s is set, want unset", providerVar2)
	}

	// Mixed, OWNER RULING (docs/multi-model.md §7): the same coder/reviewer
	// split, but agent.models.background is left empty. config.
	// EffectiveBackground then defaults the background to the provider
	// coder's model (TestBackgroundDefaultsToCoder), so the Claude reviewer
	// stage's own Claude Code process still makes background calls on the
	// provider route and would be refused the same way without the
	// variables. The decision is "any model this stage's Claude Code process
	// uses", not "the stage's own role model": kept as is, on purpose, not a
	// bug to fix.
	r = stagePinsRun(t, "vendor/model-a", "claude-opus-5", "")
	pins = r.stagePins("review")
	if pins[providerVar1] != "1" || pins[providerVar2] != "0" {
		t.Errorf("reviewer stage with no explicit background (defaults to the provider coder) pins = %+v, want both set", pins)
	}

	// The background model alone being a provider's is enough: the review
	// stage's role (reviewer) is Claude, but its background call is not.
	r = stagePinsRun(t, "claude-sonnet-5-5", "claude-opus-5", "vendor/model-b")
	pins = r.stagePins("review")
	if pins[providerVar1] != "1" || pins[providerVar2] != "0" {
		t.Errorf("reviewer stage with a provider background pins = %+v, want both set", pins)
	}

	// Without the gateway on, no pins at all, provider or not.
	r = stagePinsRun(t, "vendor/model-a", "claude-opus-5", "")
	r.spend = Spend{Mode: "off"}
	if pins := r.stagePins("implement"); pins != nil {
		t.Errorf("gateway off: pins = %+v, want nil", pins)
	}
}

// mixedToken is the token the fixture's runner environment holds: the
// account's own, mounted into the job before the run starts, and a value of
// the fixture's own — long enough that the redactor registers it.
const mixedToken = "oauth-token-abcdef"

// gatewayToken is the per-run token the gateway hands the agent, which stands
// in for the runner's real provider key in the fixture's environment.
const gatewayToken = "gateway-token-abcdef"

// mixedStageRun is the one run the owner's B0 ruling admits
// (docs/design/mixed-credentials.md §11): an api-key run whose coder stays on
// the provider key the gateway holds, with the stage-scoped Claude OAuth
// account the published node names for review and adversarial alone. Its own
// environment (r.env) is what agent.BuildEnv built for the run's stages — the
// gateway's routing and no token — and the runner's environment (r.d.Env)
// holds the account's token the way the job's mount puts it there.
func mixedStageRun(t *testing.T, stages map[string]string) *run {
	t.Helper()
	parent := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=" + mixedToken}
	env, _, err := agent.BuildEnv(parent, agent.EnvSpec{Auth: "api-key",
		Gateway: &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: gatewayToken}})
	if err != nil {
		t.Fatal(err)
	}
	r := &run{
		cfg: &config.Config{Agent: config.Agent{Auth: "api-key",
			Models: config.ModelRoles{Coder: "vendor/model-a", Reviewer: "claude-opus-5"}}},
		spend: Spend{Mode: "enforce"},
		d: Deps{Env: parent, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			ManagedSettingsPath: filepath.Join(t.TempDir(), "managed-settings.json")},
		rec: &runstore.Record{Version: 1, RunID: "20261105-065113-dbfc"},
		env: env,
		// The gateway a real run's bootstrap has started by the time a stage
		// runs: its address and per-run token are what the managed settings
		// file and the agent's own environment carry.
		gwAgent: &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: gatewayToken},
	}
	r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
		Version: projectpolicy.Version, ClaudeAccountStages: stages}}
	// The stages the node names, resolved the way bootstrap resolves them: a
	// node naming a stage outside the ruling's allow-list must not serve it.
	mixed, err := r.resolveMixedStages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.mixedStages = mixed
	return r
}

// stageEnvOf is the environment of one of the fixture's stages, as the stage
// itself gets it.
func stageEnvOf(t *testing.T, r *run, stage string) []string {
	t.Helper()
	env, err := r.stageEnv(stage, r.stagePins(stage))
	if err != nil {
		t.Fatalf("stage %s: %v", stage, err)
	}
	return env
}

// envKeysOf is env as an agent's own environment reads it: the last entry for
// a name wins, the way a process reads its environment.
func envKeysOf(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestModelCredentialsAreRegisteredForRedaction: the runner registers the
// values of gitops.ModelCredentialVars it holds before anything runs
// (registerModelCredentials, called at the top of Run beside
// addMountedSecrets). A mixed run holds the stage-scoped account's token in
// its own environment — the job mounts it, a local run is started with it —
// and everything the run publishes about itself is redacted of what it holds:
// the transcript, the relay, an error reason. A value too short to redact
// safely is skipped, the way addMountedSecrets skips one, and the stage that
// would have run on it is refused where its environment is built instead
// (agent.OAuthStageEnv).
func TestModelCredentialsAreRegisteredForRedaction(t *testing.T) {
	r := &run{d: Deps{Env: []string{
		"ANTHROPIC_API_KEY=key-123",
		"CLAUDE_CODE_OAUTH_TOKEN=" + mixedToken,
		"ANTHROPIC_AUTH_TOKEN=abc",
	}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	r.registerModelCredentials()
	for _, v := range []string{"key-123", mixedToken} {
		if got := r.redact("the job's environment holds " + v); strings.Contains(got, v) {
			t.Errorf("the redactor leaves a model credential readable: %q", got)
		}
	}
	if slices.Contains(r.secretList(), "abc") {
		t.Error("a value too short to redact safely was registered anyway")
	}
}

// TestMixedRunStageEnvKeys is the B0 ruling's allow-list, read off the
// environment of every stage a run spends a model call on: the two stages the
// account serves carry its token and none of the gateway's routing, and every
// other stage carries the gateway and no token at all — the coder's own
// environment is an allow-list and holds no model credential.
//
// Mutation (run, restored): put the account's token in the run's own
// environment — merge OAuthStageEnv's answer into r.env at bootstrap, or add
// CLAUDE_CODE_OAUTH_TOKEN to the coder's stage environment in stageAgentEnv.
// The first loop fails on every stage it walks: a coder stage would carry the
// credential the ruling keeps out of its own environment.
func TestMixedRunStageEnvKeys(t *testing.T) {
	r := mixedStageRun(t, map[string]string{"review": "team-a", "adversarial": "team-a"})
	for _, stage := range []string{"plan", "implement", "review_first", "fix"} {
		keys := envKeysOf(stageEnvOf(t, r, stage))
		if keys["ANTHROPIC_BASE_URL"] == "" || keys["ANTHROPIC_API_KEY"] != gatewayToken {
			t.Errorf("stage %s: env = %v, want the gateway's routing", stage, keys)
		}
		if _, ok := keys["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
			t.Errorf("stage %s carries the account's token, and the coder's own environment holds no model credential", stage)
		}
	}
	for _, stage := range []string{"review", "adversarial"} {
		keys := envKeysOf(stageEnvOf(t, r, stage))
		if keys["CLAUDE_CODE_OAUTH_TOKEN"] != mixedToken {
			t.Errorf("stage %s: env = %v, want the account's token", stage, keys)
		}
		for _, gw := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"} {
			if _, ok := keys[gw]; ok {
				t.Errorf("stage %s carries %s: a stage the account serves goes direct to Anthropic", stage, gw)
			}
		}
	}
}

// TestMixedRunStagesNotNamedByTheKeyKeepTheGateway: the mix is the stage key's
// and no other's. A run whose node names no stage account keeps its reviews on
// the gateway whatever the job's environment holds, and a node that names a
// stage the ruling does not admit — the coder's own, the cheap first review,
// a fix — is never served by the account: the allow-list is the program's,
// not the node's, and a node that named one would have been refused before the
// launch (config.CheckStageAccountMix).
func TestMixedRunStagesNotNamedByTheKeyKeepTheGateway(t *testing.T) {
	named := mixedStageRun(t, nil)
	for _, stage := range []string{"implement", "review_first", "review", "adversarial", "fix"} {
		if keys := envKeysOf(stageEnvOf(t, named, stage)); keys["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			t.Errorf("stage %s is served by the account with no stage key naming it: %v", stage, keys)
		}
	}
	refused := mixedStageRun(t, map[string]string{"implement": "team-a", "review_first": "team-a", "fix": "team-a"})
	for _, stage := range []string{"implement", "review_first", "fix"} {
		keys := envKeysOf(stageEnvOf(t, refused, stage))
		if _, ok := keys["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
			t.Errorf("stage %s is served by the account, and the ruling names %s and no other stage: %v",
				stage, strings.Join(config.MixedAccountStages, " and "), keys)
		}
		if keys["ANTHROPIC_API_KEY"] != gatewayToken {
			t.Errorf("stage %s lost the gateway: %v", stage, keys)
		}
	}
}

// TestMixedRunRecordsTheStagesAccount: the record names the account that
// served each stage and no more than that (runstore.Record.StageAccounts,
// spec §11.5) — a stage the account served, and no entry for a stage it did
// not.
func TestMixedRunRecordsTheStagesAccount(t *testing.T) {
	r := mixedStageRun(t, map[string]string{"review": "team-a"})
	stageEnvOf(t, r, "implement")
	stageEnvOf(t, r, "review")
	if got := r.rec.StageAccounts["review"]; got != "team-a" {
		t.Errorf("stage_accounts[review] = %q, want the account that served it", got)
	}
	if _, ok := r.rec.StageAccounts["implement"]; ok {
		t.Errorf("stage_accounts names a coder stage: %v", r.rec.StageAccounts)
	}
	if len(r.rec.StageAccounts) != 1 {
		t.Errorf("stage_accounts = %v, want the review stage alone", r.rec.StageAccounts)
	}
}

// TestMixedRunStageWithoutItsTokenIsRefused: a stage the account serves whose
// token the runner's environment does not hold is refused rather than run on
// the coder's credential, and the refusal names the variable that is missing.
func TestMixedRunStageWithoutItsTokenIsRefused(t *testing.T) {
	r := mixedStageRun(t, map[string]string{"review": "team-a"})
	r.d.Env = []string{"PATH=/bin"}
	env, err := r.stageEnv("review", r.stagePins("review"))
	if err == nil {
		t.Fatalf("a stage the account serves, with no token in the runner's environment, was given an environment anyway: %v", env)
	}
	if !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("refusal = %q, want it to name the variable the run holds none of", err)
	}
	if env != nil {
		t.Errorf("the refused stage came back with an environment: %v", env)
	}
	if keys := envKeysOf(stageEnvOf(t, r, "implement")); keys["ANTHROPIC_API_KEY"] != gatewayToken {
		t.Errorf("the run's own stages stopped working: %v", keys)
	}
}

// TestMixedRunManagedSettingsFollowTheStage: the managed settings outrank the
// agent's environment, so the file every stage is launched under has to say
// what that stage's own environment says — the gateway's routing for a stage
// the run's own credential serves, and neither the gateway's URL nor its
// token for a stage the account serves.
func TestMixedRunManagedSettingsFollowTheStage(t *testing.T) {
	r := mixedStageRun(t, map[string]string{"review": "team-a", "adversarial": "team-a"})
	raw := func() string {
		t.Helper()
		b, err := os.ReadFile(r.managedPath())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if err := r.writeManagedSettings("implement", r.stagePins("implement")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw(), "ANTHROPIC_BASE_URL") || !strings.Contains(raw(), gatewayToken) {
		t.Errorf("the coder's managed settings do not name the gateway: %s", raw())
	}
	if err := r.writeManagedSettings("review", r.stagePins("review")); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", gatewayToken} {
		if strings.Contains(raw(), gone) {
			t.Errorf("the review stage's managed settings still carry %s: %s", gone, raw())
		}
	}
}

// imageEnvRun is the minimal run imageEnv reads: the job's own environment.
// Nothing else about the run is consulted — whether the job carries the stamp
// and which mounted secret names the run declares are what decide the answer.
func imageEnvRun(env []string) *run { return &run{d: Deps{Env: env}} }

// TestImageEnvIsNilWithoutTheStamp is the load-bearing test of the
// passthrough: a job that carries no FUGARO_IMAGE_ENTRY passes nothing,
// whatever its environment holds (D1371's fail-closed). That is every job
// whose image the operator's catalog did not choose — a repository-built
// image, checkout: clone's base image — and every job deployed before the
// stamp existed; an EMPTY stamp is the same fact as an absent one, because the
// deploy writes the key on every job so a repository's own Dockerfile cannot
// supply one.
func TestImageEnvIsNilWithoutTheStamp(t *testing.T) {
	for _, env := range [][]string{
		{
			"NEXUS_BASEURL=https://example.invalid/repository/maven-public/",
			"MAVEN_OPTS=-Dmaven.repo.local=/opt/m2",
		},
		{
			ImageEntryEnv + "=",
			"NEXUS_BASEURL=https://example.invalid/repository/maven-public/",
		},
	} {
		if got := imageEnvRun(env).imageEnv(nil); got != nil {
			t.Errorf("imageEnv = %v, want nil: a job with no stamp passes nothing, whatever its environment holds", got)
		}
	}
}

// TestImageEnvDropsTheJobsUndeclaredSecrets: a job mounts every secret its
// workflow declares (F8), so a name in FUGARO_SECRET_ENVS that this run did not
// declare never arrives through the image arm — the run reaches it through the
// declared-secret path or not at all — while a declared one does, and the
// image's own names pass beside it. Nothing of the dropped value is registered
// with the redactor by this arm (D1376, the owner's O3 answer): a passed value
// is not a secret.
func TestImageEnvDropsTheJobsUndeclaredSecrets(t *testing.T) {
	r := imageEnvRun([]string{
		ImageEntryEnv + "=base",
		SecretEnvsVar + "=NPM_TOKEN,DEPLOY_KEY",
		"NPM_TOKEN=npm-token-placeholder",
		"DEPLOY_KEY=deploy-key-placeholder",
		"NEXUS_BASEURL=https://example.invalid/repository/maven-public/",
	})
	got := r.imageEnv([]string{"NPM_TOKEN"})
	if got["NPM_TOKEN"] != "npm-token-placeholder" {
		t.Errorf("a declared secret name is absent from the passthrough: %v", got)
	}
	if _, ok := got["DEPLOY_KEY"]; ok {
		t.Errorf("a secret name the run did not declare reached the passthrough: %v", got)
	}
	if got["NEXUS_BASEURL"] != "https://example.invalid/repository/maven-public/" {
		t.Errorf("the image's own name is absent: %v", got)
	}
	if secrets := r.secretList(); len(secrets) != 0 {
		t.Errorf("the passthrough registered %v with the redactor, want nothing: a passed value is not a secret", secrets)
	}
	// A review run declares none of the workflow's secrets (bootstrap's
	// secretEnvs is empty for it), so every mounted name is dropped there.
	if got := r.imageEnv(nil); got["NPM_TOKEN"] != "" || got["DEPLOY_KEY"] != "" {
		t.Errorf("a run that declares nothing still passes a mounted secret name: %v", got)
	}
}

// TestImageEnvDropsThePlatformNames: the installation's own five and the
// launcher's names are the deployment's, and the agent sees none of them
// through the image arm — the value the job set is the one that stands.
func TestImageEnvDropsThePlatformNames(t *testing.T) {
	env := []string{
		ImageEntryEnv + "=base",
		"NEXUS_BASEURL=https://example.invalid/repository/maven-public/",
		"FUGARO_RUN=acme-app/20260927-100000-abcd",
		"FUGARO_BUDGET_TOKEN=sealed-placeholder",
		"FUGARO_CREDENTIALS_SERVICE_URL=https://creds.invalid",
		"FUGARO_REVIEW_JOB=1",
		"CLOUD_RUN_JOB=fugaro-acme-app-web",
		"CLOUD_RUN_EXECUTION=fugaro-acme-app-web-x7k2p",
	}
	for _, name := range backend.PlatformEnvNames {
		env = append(env, name+"=us-east5")
	}
	got := imageEnvRun(env).imageEnv(nil)
	for _, name := range env {
		k, _, _ := strings.Cut(name, "=")
		if k == "NEXUS_BASEURL" {
			continue
		}
		if _, ok := got[k]; ok {
			t.Errorf("%s reached the passthrough from the image arm: %v", k, got)
		}
	}
}
