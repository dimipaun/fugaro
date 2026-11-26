package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner"
)

// claude_gate.go is KS20 of docs/keys-through-service, in the state the owner
// ruled on 2026-11-26: the release ships WITHOUT Check 50's result. KS3's
// refusal for a Claude model through a provider key stays exactly as it is
// (internal/creds' checkCandidates), Check 50's row in the checklist still
// reads not run, and the check's result flips the gate in a change of its own.
//
// What that leaves to build is the not-run branch of the task's Acceptance:
// the release stops a call that works on the mounted key today (F40), so the
// two surfaces that can see the whole project name every repository whose runs
// it stops, with the fix, before it does — `fugaro doctor` as a check, and
// `fugaro update` as a notice. One finding, one text, printed by both, for the
// reason repo_notices.go gives: a person who reads the line here and then runs
// the update meets the same sentence twice.
//
// Both read C1's enrolled list (enrolledRepos), so the repositories they name
// are the ones every stage of the update reads, and neither reads the cloud:
// whether the gate stops a repository is a fact about the project's provider
// table and the models a run of that repository carries.
//
// The models are the project layer's, not the repository's file's: since S1
// (D3237) config.Resolve drops every lifted key a file still writes, so a run
// reads its coder and its levels off the layer the file resolves over, and so
// does this (claudeGateCfg). A file that writes a lifted pin anyway is the
// note S2 and S5 print and not a gate stop (R1).

// claudeGateCheckID is doctor's row, and claudeGateStep the name the update
// reports the same finding under. They are the same string because they are
// the same finding.
const (
	claudeGateCheckID = "claude-through-a-provider"
	claudeGateStep    = "claude-through-a-provider"
)

// claudeGateLead and claudeGateFix are the finding and its fix, the same text
// in doctor and in the update. The lead is what the count continues, as
// doctorRepoNoticeChecks' two are.
const (
	claudeGateLead = "a Claude model through a provider key, which this release refuses, is still pinned by"
	claudeGateFix  = "take each Claude ID out of the route's models in the project config"
)

// claudeGatePaths are the config paths of an Agent's four pins, in
// config.PinnedModels' own order. The paths are spelled out here as `validate`
// spells them beside its two other loops over the same four.
var claudeGatePaths = [...]string{"agent.models.coder", "agent.models.reviewer", "agent.models.background", "agent.models.vision"}

// claudeGatePin is one pin of a repository that the gate stops: the key it is
// written at, the model, and the provider whose route serves it — the three
// facts the edit claudeGateFix names is made from.
type claudeGatePin struct {
	Path     string
	Model    string
	Provider string
}

// claudeGatePins are the pins of one repository that reach the credentials
// service through a provider key and are Claude's: exactly the calls KS3's
// refusal answers once the service serves the repository's keys (D3000, R3).
//
// A pin reaches a provider when all four of these hold, and each is read from
// the document that decides it rather than restated here:
//
//   - the repository runs under a gateway and an enforcing budget. Without the
//     first a run routes nothing, and without the second CheckProviderGateway
//     refuses the file outright — a repository in either state is stopped by a
//     rule of its own, and the gate is not what stops it (the reading
//     config.NeedsAnthropicKey makes of the same two);
//   - the model is Claude's by config.IsClaudeModel, the vocabulary every
//     other reader of a pin already shares (R3), never a list of this file's;
//   - a provider of the owner's table claims it. Only an explicit
//     `anthropic/<id>` may claim a Claude model (config.CheckClaudeClaim), so a
//     pin that is claimed is one the owner wrote as exactly that ID;
//   - that provider's allow_data_to names this repository, which is the half
//     CheckProviderPolicy asks beside the claim. Both are read off
//     layerRoutes' second answer, the owner's table with the project layer's
//     claims written in.
//
// A review level's own model is a pin of its own and is read as one
// (agent.levels.lN.model): it runs in place of the role slot the level sits
// in. A level left on the model its role resolves to is that role's pin
// already, and earns one line and not two.
func claudeGatePins(lc *localcfg.Config, cfg *config.Config, repo string) []claudeGatePin {
	if lc == nil || cfg == nil {
		return nil
	}
	auth := cfg.Agent.Auth
	if auth == "" {
		auth = config.DefaultAgentAuth
	}
	if !runner.GatewayOn(auth) || policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode != policy.ModeEnforce {
		return nil
	}
	_, claimed, _ := layerRoutes(lc, cfg.Layer, repo)
	var out []claudeGatePin
	add := func(path, model string) {
		if model == "" || !config.IsClaudeModel(model) {
			return
		}
		name, p, ok := config.ProviderFor(claimed, model)
		if !ok || !p.AllowsData(repo) {
			return
		}
		for _, seen := range out {
			if seen.Model == model {
				return
			}
		}
		out = append(out, claudeGatePin{Path: path, Model: model, Provider: name})
	}
	for i, m := range cfg.Agent.PinnedModels() {
		add(claudeGatePaths[i], m)
	}
	for _, l := range []struct {
		name string
		set  *config.LevelSetting
	}{{"l1", cfg.Agent.Levels.L1}, {"l2", cfg.Agent.Levels.L2}, {"l3", cfg.Agent.Levels.L3}} {
		if l.set != nil {
			add("agent.levels."+l.name+".model", l.set.Model)
		}
	}
	return out
}

// claudeGateLine is one repository the gate stops, with the pins that stop it,
// as both surfaces report it.
type claudeGateLine struct {
	Repo   string
	Detail string
}

// claudeGateLines is every enrolled repository the gate stops, in the order the
// reader listed them, each with one detail naming its pins and the edit that
// takes them out of the gate's way. A repository the reader could not read
// earns no line — the reader's own unread line is where that is said — and so
// does one whose pins reach no provider.
//
// The pins are read from the config a run of the repository carries rather than
// from the view's own resolution, because the two are not always the same
// document: a caller that hands the reader no layer (doctor's, and the
// update's own C1 read) gets views resolved over nothing, in which every model
// a file writes has already been dropped (S1, D3237). The layer here is the one
// this machine last read, which is the same one both surfaces hold.
func claudeGateLines(lc *localcfg.Config, views []repoView) []claudeGateLine {
	layer := claudeGateProjectLayer(lc)
	var out []claudeGateLine
	for _, v := range views {
		if !v.Read() {
			continue
		}
		pins := claudeGatePins(lc, claudeGateCfg(v, layer), v.Repo)
		if len(pins) == 0 {
			continue
		}
		parts := make([]string, 0, len(pins))
		for _, p := range pins {
			parts = append(parts, fmt.Sprintf("%s: %s through %s", p.Path, p.Model, p.Provider))
		}
		out = append(out, claudeGateLine{
			Repo:   pluginwire.Printable(oneLine(v.Repo)),
			Detail: pluginwire.Printable(oneLine(strings.Join(parts, ", ") + ": " + claudeGateFix)),
		})
	}
	return out
}

// claudeGateProjectLayer is the project layer every repository's file is read
// over: the one this machine last read (modelsLayer), which is where a
// project's models live now that a repository's file may name none (S1,
// D3237). A machine that has read no layer resolves the file alone, in which
// no repository names a model at all — a launch of one is refused before the
// gate could stop it.
func claudeGateProjectLayer(lc *localcfg.Config) *config.ProjectLayer {
	if lc == nil || lc.Name == "" {
		return nil
	}
	return modelsLayer(lc.Name)
}

// claudeGateCfg is the config a run of one repository carries: the repository's
// own bytes resolved over the project layer, as a launch resolves them. A file
// the resolution refuses answers no config, and a repository with no config
// earns no pins — the gate has nothing to judge, and the refusal is its own
// line elsewhere.
func claudeGateCfg(v repoView, layer *config.ProjectLayer) *config.Config {
	cfg, _, _ := config.Resolve(v.YAML, layer)
	return cfg
}

// claudeGateProblem is the finding as doctor prints it: the lead, the count,
// and one line per repository, as doctorRepoNoticeChecks prints its own.
func claudeGateProblem(lines []claudeGateLine) string {
	rows := make([]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, l.Repo+": "+l.Detail)
	}
	return fmt.Sprintf("%s %d %s:\n%s", claudeGateLead, len(lines), plural(len(lines), "repository", "repositories"), strings.Join(rows, "\n"))
}

// noticeClaudeGate reports one line per enrolled repository the gate will stop,
// naming the pins and the edit that takes them out of its way, off the views
// the update's own read of the enrolled list took (C1).
//
// It is the update's step after the stage loop, and it rides noticeRepoValues'
// turn because that step is the same thing — one pass over the enrolled list,
// one line per repository, needs-you — and because the step list in
// init_stages.go is KS12's file in this plan's own order, which this task does
// not write.
//
// It is a notice and not a write, for the reason noticeRepoValues is one: the
// release stops these runs, so a person is owed the list before it does
// (CLAUDE.md's HARD RULE — the update is the whole upgrade) — while the
// project's provider table and the repository's own pin are the owner's files,
// and neither is this machine's to edit. It never fails the update: a
// repository the reader could not read earns no line, because the reader's own
// unread line is where that is said.
func (e *initEngine) noticeClaudeGate(views []repoView) {
	r := e.r
	var lines []initflow.RepoLine
	for _, l := range claudeGateLines(e.lc, views) {
		lines = append(lines, newRepoLine(l.Repo, initflow.RepoNeedsYou, l.Detail))
	}
	if len(lines) == 0 {
		return
	}
	e.reportRepos(claudeGateStep, lines)
	for _, l := range lines {
		fmt.Fprintf(r.w, "%s: %s: %s\n", l.Repo, l.State, l.Detail)
	}
}

// doctorClaudeGateChecks is the same finding as doctor's row: one warning
// naming every enrolled repository the gate stops, with the pins and the fix
// (D3000, R15). It reads the repositories through C1's list, as doctor's two
// per-repository notice checks do, because doctor has no engine of its own.
func doctorClaudeGateChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	views, _ := enrolledRepos(ctx, enrolledIn{LC: lc})
	lines := claudeGateLines(lc, views)
	if len(lines) == 0 {
		return nil
	}
	return []doctorCheck{{
		ID:       claudeGateCheckID,
		Severity: "warning",
		Problem:  claudeGateProblem(lines),
		Fix:      claudeGateFix,
	}}
}
