package runner

import (
	"fmt"
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
// and its output limit per call.
func (r *run) stagePins(stage string) map[string]string {
	role := config.StageRole(stage)
	a := r.cfg.Agent
	return agent.PinVars(a.ModelFor(role), a.Models.Background, a.MaxOutputFor(role))
}

// writeManagedSettings writes the managed settings for a stage: with the
// gateway on its variables and the web-tool denial, and always the pins. It
// does nothing when there is neither. A failure with the gateway on is an
// error, since the settings are what keep a repository's own settings from
// rerouting the agent; with pins only, it is only logged.
func (r *run) writeManagedSettings(pins map[string]string) error {
	gw := r.gateway()
	if gw == nil && len(pins) == 0 {
		return nil
	}
	vars := map[string]string{}
	if gw != nil {
		gv, err := agent.GatewayVars(r.cfg.Agent.Auth, *gw, r.parentEnv())
		if err != nil {
			return fmt.Errorf("writing Claude Code's managed settings: %w", err)
		}
		vars = gv
	}
	for k, v := range pins {
		vars[k] = v
	}
	err := agent.WriteManagedSettings(r.managedPath(), vars, gw != nil)
	switch {
	case err == nil:
		return nil
	case gw != nil:
		return fmt.Errorf("writing Claude Code's managed settings: %w", err)
	default:
		if r.warnedSettings {
			return nil
		}
		r.warnedSettings = true
		r.d.Log.Warn("writing Claude Code's managed settings failed; the model pins apply through the environment only", "err", r.redact(err.Error()))
		return nil
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
