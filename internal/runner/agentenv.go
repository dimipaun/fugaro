package runner

import (
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

// stagePins are the pins of a stage's role: its model, the background model
// and its output limit per call. Its model is the review level's own when the
// stage is a review stage a layer named one for (stageModel), and the role
// slot's otherwise. They exist for the gateway's pricing, so without the
// gateway there are none and a run sets no variable the budget-off runs never
// set. When the stage's model or the background model is served by a provider
// route, agent.ProviderRouteVars are added: a Claude stage is never touched by
// them.
func (r *run) stagePins(stage string) map[string]string {
	if !r.gatewayOn() {
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
func (r *run) writeManagedSettings(stage string, pins map[string]string) error {
	gw := r.gateway()
	if gw == nil && !r.review {
		return nil
	}
	vars := map[string]string{}
	if gw != nil {
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
