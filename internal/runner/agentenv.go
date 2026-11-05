package runner

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// modelCredentialVars are kept out of the environment of every git call the
// runner makes, so a command an agent-written .git/config makes git run
// (core.fsmonitor, a filter driver) never sees the model credential.
var modelCredentialVars = gitops.ModelCredentialVars

// strip keeps the model credentials out of repo's git calls and returns it.
func strip(repo *gitops.Repo) *gitops.Repo {
	repo.StripEnv = modelCredentialVars
	return repo
}

// registerModelCredentials registers the model credentials the runner's own
// environment holds with the redactor, before anything runs. A run whose own
// credential is a provider key holds the gateway's key, and a mixed run
// (docs/design/mixed-credentials.md §11) holds the stage-scoped account's
// token besides: the job mounts it, or a local run is started with it, and one
// of the run's stages is served from it. Every one of them is a value a print
// of the runner's environment — a transcript, a stage's error reason — must
// not carry.
//
// A value too short to redact safely is skipped with a warning that names only
// the variable (addMountedSecrets, the variables' other registration): the
// stage that would have run on it is refused where it is built instead
// (agent.OAuthStageEnv), and the warning says so without printing it.
func (r *run) registerModelCredentials() {
	for _, k := range modelCredentialVars {
		switch v := envLookup(r.d.Env, k); {
		case v == "":
		case len(v) < minSecretLen:
			r.d.Log.Warn("a model credential is too short to redact", "env", k)
		default:
			r.addSecret(v)
		}
	}
}

// maxSettingsFile bounds a settings file the routing check reads.
const maxSettingsFile = 1 << 20

// managedPath is where the managed settings file goes.
func (r *run) managedPath() string {
	if r.d.ManagedSettingsPath != "" {
		return r.d.ManagedSettingsPath
	}
	return agent.ManagedSettingsPath
}

// parentEnv is the runner's environment as a map.
func (r *run) parentEnv() map[string]string {
	m := map[string]string{}
	for _, kv := range r.d.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// mixedStages is the run's stage accounts: the stages of the one mix the
// owner's B0 ruling admits, and the account each of them runs on
// (docs/design/mixed-credentials.md §11). A run whose own credential is a
// provider key — agent.auth is api-key — may hold exactly one stage-scoped
// Claude OAuth account, declared by that stage's own key in the project's
// published policy, and it serves the senior review and the adversarial review
// and no other stage. Every other run, and every node that names no stage
// account, answers an empty map: a run's own stages are the gateway's.
//
// The stage list is an allow-list and it is this function's, never the node's:
// a node that named a stage outside it — the coder's own, the cheap first
// review, a fix — is not served by the account whatever that node says. Such a
// node is refused before the launch (config.CheckStageAccountMix, applied by
// the launcher and by `fugaro validate`); the run itself never puts the
// account's token anywhere but the two stages the ruling names.
//
// It is read once, at bootstrap, and the answer is what every reader of a
// stage's credential stands on: the stage's own environment (stageAgentEnv),
// its pins (stagePins) and its managed settings (writeManagedSettings).
func (r *run) resolveMixedStages(ctx context.Context) (map[string]string, error) {
	if r.cfg == nil || r.cfg.Agent.Auth != "api-key" {
		// The mix exists to give an api-key run a Claude review credential it
		// otherwise has none of. Under oauth the keys keep the meaning the
		// accounts design gives them, and that chain is not this one: a run
		// under it holds one account, not a stage's.
		return nil, nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if !found || p == nil {
		return nil, nil
	}
	out := map[string]string{}
	for _, stage := range config.MixedAccountStages {
		if name := p.ClaudeAccountStages[stage]; name != "" {
			out[stage] = name
		}
	}
	return out, nil
}

// stageAccount is the Claude OAuth account the stage runs on, "" for every
// stage that runs on the run's own credential. The map it reads holds the two
// stages the ruling admits and nothing else (mixedStages).
func (r *run) stageAccount(stage string) string { return r.mixedStages[stage] }

// stageAgentEnv is the environment a stage's own process runs in: the run's
// own, with the stage's pins, or — for a stage the run's stage account serves
// — the run's own with the gateway's routing dropped and the account's token
// in its place (agent.OAuthStageEnv), plus the level's effort where one
// applies. The account is recorded as the stage is given it
// (recordStageAccount, spec §11.5).
//
// A stage whose token the runner's environment does not hold is refused here,
// before its agent starts: the run was configured to serve the stage with an
// account it does not hold, and the one answer the ruling forbids is running
// that stage on the coder's credential instead.
func (r *run) stageAgentEnv(stage string, pins map[string]string) ([]string, error) {
	account := r.stageAccount(stage)
	if account == "" {
		return gitops.WithVars(r.env, pins), nil
	}
	env, err := agent.OAuthStageEnv(r.env, envLookup(r.d.Env, config.ReservedSecrets[config.ClaudeOAuthToken]))
	if err != nil {
		return nil, err
	}
	r.recordStageAccount(stage, account)
	return gitops.WithVars(env, pins), nil
}

// stagePins are the pins of a stage's role: its model, the background model
// and its output limit per call. Its model is the review level's own when the
// stage is a review stage a layer named one for (stageModel), and the role
// slot's otherwise. They exist for the gateway's pricing, so without the
// gateway there are none and a run sets no variable the budget-off runs never
// set. When the stage's model or the background model is served by a provider
// route, agent.ProviderRouteVars are added: a Claude stage is never touched by
// them.
//
// A stage the run's stage account serves has none either, whatever the
// gateway's state: the pins are its route table's and its price table's, and
// that stage's calls go straight to Anthropic on the account, where neither
// applies. Its model reaches Claude Code as the --model flag (agent.Args), and
// a pin left in place would be a second answer to which model the stage runs —
// the coder's background pin among them, which named a provider model. The
// review level's own effort is not a routing pin and is merged in by its
// caller (applyStageEffort); a level that names one keeps it.
func (r *run) stagePins(stage string) map[string]string {
	if !r.gatewayOn() || r.stageAccount(stage) != "" {
		return nil
	}
	role := stageRole(stage)
	a := r.cfg.Agent
	model, bg := r.stageModel(stage), r.backgroundModel()
	pins := agent.PinVars(model, bg, a.MaxOutputFor(role))
	if r.stageOnProviderRoute(model, bg) {
		maps.Copy(pins, agent.ProviderRouteVars())
	}
	return pins
}

// stageOnProviderRoute reports whether model or background is claimed by a
// configured provider (config.ProviderFor), so the gateway would route a call
// on it there instead of to Anthropic.
func (r *run) stageOnProviderRoute(model, background string) bool {
	for _, m := range []string{model, background} {
		if _, _, ok := config.ProviderFor(r.modelProviders(), m); ok {
			return true
		}
	}
	return false
}

// writeManagedSettings writes the managed settings for stage: the gateway's
// variables, the pins, and the stage's deny list — the web tools for every
// stage, and the binary reads for a review stage, whose reviewer would
// otherwise be free to Read a changed PNG and have the gateway refuse the
// image block that puts in the next request (agent.deniedBinaryReads). It
// does nothing without the gateway: a budget-off run writes no file, and so
// carries no deny rule at all. A failure is an error, since the settings are
// what keep a repository's own settings from rerouting the agent.
//
// A review-mode run is the one exception, and it is not about the gateway at
// all: with auth: oauth and the budget off — the cloud reviewer's own
// configuration, where the gateway is off — this run's one stage is the one
// stage that reads a pull request's own head, and its posture (D894(a)) is
// what that stage must be launched under whatever the budget says. Such a run
// writes the posture alone: the deny list is left out because both of its
// halves exist for refusals the gateway itself makes.
//
// A stage the run's stage account serves writes none of the gateway's
// variables and none of the pins, for the reason its own environment carries
// none (stageAgentEnv, stagePins): its calls go straight to Anthropic on that
// account. The file is still written, and that is the point of writing it —
// the managed settings outrank the agent's environment, so the previous
// stage's copy would otherwise route this one at the gateway whatever its
// environment said (P5, the sidecar's own rule).
func (r *run) writeManagedSettings(stage string, pins map[string]string) error {
	gw := r.gateway()
	account := r.stageAccount(stage)
	if gw == nil && !r.review && account == "" {
		return nil
	}
	vars := map[string]string{}
	if gw != nil && account == "" {
		gwVars, err := agent.GatewayVars(r.cfg.Agent.Auth, *gw, r.parentEnv())
		if err != nil {
			return fmt.Errorf("writing Claude Code's managed settings: %w", err)
		}
		vars = gwVars
		if r.compression != nil {
			// After GatewayVars, not before: the managed settings outrank the
			// agent's environment, so leaving the gateway's URL here would route
			// Claude Code around the sidecar whatever BuildEnv did (P5).
			vars["ANTHROPIC_BASE_URL"] = r.compression.BaseURL
		}
		for k, v := range pins {
			vars[k] = v
		}
	}
	write, deny := agent.WriteManagedSettings, agent.DenyRules(true, config.IsReviewStage(stage))
	if r.review {
		// A review-mode run's stage reads a pull request's own head, so the
		// settings it is launched under are the review posture (D894(a)):
		// managed hooks only, no project MCP server, and setting sources
		// from the user and managed levels alone. A coding run's own stages
		// — its in-loop review rounds included — keep the file they had.
		write = agent.WriteReviewManagedSettings
		if r.reviewJob() {
			// The independent review job is the one review stage that runs
			// the head's own code (the mutation check) and reads nothing of
			// its own repository, so it is the one that loses the tools that
			// would run or write anything (D891(a)). A coding run's own
			// review rounds, and `fugaro run --pr N --recipe review-only`,
			// are the repository's own run and keep them.
			deny = append(deny, agent.ReviewJobDenyRules()...)
		}
		if gw == nil {
			deny = nil
		}
	}
	if err := write(r.managedPath(), vars, deny); err != nil {
		return fmt.Errorf("writing Claude Code's managed settings: %w", err)
	}
	r.mu.Lock()
	r.wroteManaged = true
	r.mu.Unlock()
	return nil
}

// removeManagedSettings deletes the managed settings file this run wrote:
// they hold the gateway's address and token, dead once the run is over,
// and a local run's file would otherwise redirect every later Claude Code
// on the machine.
func (r *run) removeManagedSettings() {
	r.mu.Lock()
	wrote := r.wroteManaged
	r.wroteManaged = false
	r.mu.Unlock()
	if !wrote {
		return
	}
	if err := os.Remove(r.managedPath()); err != nil && !os.IsNotExist(err) {
		r.d.Log.Warn("removing Claude Code's managed settings failed", "err", r.redact(err.Error()))
	}
}

// checkSettingsRouting refuses any settings file that would send Claude Code
// around the gateway (design §5.1): the checkout's .claude settings, the
// user's, $CLAUDE_CONFIG_DIR's, and anything else in the managed settings
// directory. The runner's own managed file is not read. It returns the
// reason, or "" when all is well.
func (r *run) checkSettingsRouting() string {
	if r.routingKnob() {
		return ""
	}
	parent := r.parentEnv()
	files, err := agent.SettingsFiles(r.d.WorkDir, parent["HOME"], parent["CLAUDE_CONFIG_DIR"], r.managedPath())
	if err != nil {
		return err.Error()
	}
	managedDir := filepath.Dir(r.managedPath())
	for _, f := range files {
		name := f
		if rel, err := filepath.Rel(r.d.WorkDir, f); err == nil && !strings.HasPrefix(rel, "..") {
			name = rel
		}
		fi, err := os.Lstat(f)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Sprintf("%s can't be checked (%v), so it might route Claude Code around Fugaro's gateway; remove it", name, err)
		}
		if filepath.Dir(f) == managedDir && fi.Mode().IsRegular() && agent.IsManagedSettingsTmp(filepath.Base(f)) {
			// What a crashed write of our own left behind: nothing reads
			// it, and the next write would not clear it.
			if os.Remove(f) == nil {
				continue
			}
		}
		if filepath.Dir(f) == managedDir {
			return fmt.Sprintf("%s is in Claude Code's managed settings directory, where only Fugaro's own file may be; remove it", name)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Sprintf("%s is not a regular file, so it might route Claude Code around Fugaro's gateway; remove it", name)
		}
		if fi.Size() > maxSettingsFile {
			return fmt.Sprintf("%s is over %d bytes, too large to check for routing around Fugaro's gateway; remove it", name, maxSettingsFile)
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Sprintf("%s can't be read (%v), so it might route Claude Code around Fugaro's gateway; remove it", name, err)
		}
		keys, err := agent.RoutingKeys(data)
		if err != nil {
			return fmt.Sprintf("%s is not valid settings JSON (%v), so it might route Claude Code around Fugaro's gateway; fix or remove it", name, err)
		}
		if len(keys) > 0 {
			return fmt.Sprintf("%s sets %s, which would route Claude Code around Fugaro's gateway; remove it", name, strings.Join(keys, ", "))
		}
	}
	return ""
}
