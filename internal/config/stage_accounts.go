package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The stage vocabulary of the account-choice keys (docs/accounts-add/spec.md
// §11.1) and the one mix the owner's B0 ruling admits
// (docs/design/mixed-credentials.md §11). Both rules live here because four
// readers judge them — the local config at load, the published policy node,
// `fugaro validate` and the launch — and a second copy of either would be a
// second answer to drift from.
//
// RunStages are the run's own stage names for the stages a run spends a model
// call on, which is what a `stages.<stage>.claude_account` segment may be. It
// is deliberately not every stage name the tree writes: bootstrap, render,
// finalize, writeback and provision spend no model call, so no account could
// apply to them, and naming one is refused rather than silently never
// applying. The names are the runner's own stage literals
// (internal/runner/runner.go, reviewmode.go, artifact.go) and
// recipe.StepAdversarial, which the record's own stage enum does not yet
// carry.
var RunStages = []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}

// MixedAccountStages are the stages of a run that mixes credentials: the
// senior review and the adversarial review, and no other stage
// (docs/design/mixed-credentials.md §11, "An allow-list, not a denylist"). An
// api-key run may name its one Claude account for these two stages only.
var MixedAccountStages = []string{"review", "adversarial"}

// IsRunStage reports whether stage is one of RunStages.
func IsRunStage(stage string) bool {
	return slices.Contains(RunStages, stage)
}

// CheckStageSegment refuses a stages.<stage> key whose segment is not a stage
// a run spends a model call on. path is the key's own path ("stages", or the
// node's spelling of it), so the problem names where it was written.
//
// A segment outside the vocabulary is refused and not ignored: the key would
// otherwise name an account for a stage that never reads it, and the operator
// would believe the account was in use. The stage the tree does not have yet
// is refused for the same reason — the vocabulary is closed, so a recipe that
// adds a stage adds it here in the same change.
func CheckStageSegment(stage, path string) []Problem {
	if IsRunStage(stage) {
		return nil
	}
	return []Problem{{Path: path + "." + stage,
		Message: fmt.Sprintf("%q is not a stage of a run: a stage account may be named for %s, the stages a run spends a model call on (bootstrap, render, finalize, writeback and provision spend none, so no account could apply to them)",
			stage, strings.Join(RunStages, ", "))}}
}

// CheckStageAccountMix judges the stage accounts a run's own credential
// admits, and it is the B0 ruling written once
// (docs/design/mixed-credentials.md §11). auth is the agent.auth the run
// resolves; stages is the stage account each stage would use, already
// resolved from the config (empty values dropped by the caller or ignored
// here).
//
// Under api-key the ruling admits exactly one Claude account, alongside the
// provider key the gateway holds, and only for the senior review and the
// adversarial review: the coder's stages keep the provider key, so an account
// named for one of them would never be used, and a second account is a second
// credential the ruling does not grant.
//
// Under oauth the keys keep the meaning the accounts design gives them — one
// account for a stage and another for the rest is what the design's rung 2 is
// for — so nothing is refused here. Under vertex and cascade a stage account
// cannot apply at all (neither reaches a Claude OAuth credential), and it is
// refused rather than ignored, the way a segment outside RunStages is. An
// unknown auth ("") is not judged: the launch resolves it from a checkout it
// may not have yet, and the run itself judges it with the auth it resolves.
// Every *other* value is refused rather than read as oauth: the auth here can
// be a committed fugaro.yaml's own bytes (config.AgentOf validates nothing),
// so a value nobody can place must not be the one that admits a stage account.
func CheckStageAccountMix(auth string, stages map[string]string, path string) []Problem {
	switch auth {
	case "api-key":
		return mixedAccountProblems(stages, path)
	case "oauth", "":
		return nil
	case "vertex", AuthCascade:
		return stageAccountUnderOtherAuth(auth, stages, path)
	default:
		return stageAccountUnderUnknownAuth(auth, stages, path)
	}
}

// stageAccountUnderUnknownAuth refuses a stage account under an agent.auth
// this binary cannot place. The launch refuses the same name with the same
// sentence where it judges the run's own account (claudeAccountAllowed,
// internal/cli/run.go), and it is here too because the stage keys are read
// before that: an auth nothing knows admits no account, and a key that
// silently never applies is what this whole rule refuses.
func stageAccountUnderUnknownAuth(auth string, stages map[string]string, path string) []Problem {
	var ps []Problem
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		if stages[stage] == "" {
			continue
		}
		ps = append(ps, Problem{Path: path + "." + stage + ".claude_account",
			Message: fmt.Sprintf("a stage account cannot be judged against an agent.auth of %q, which is not a value this Fugaro knows (agent.auth is one of %s): a stage account applies to a run whose agent.auth is oauth or api-key, so under a value nobody can place the account would never be used — fix agent.auth, or remove the key",
				auth, strings.Join(AgentAuths, ", "))})
	}
	return ps
}

func mixedAccountProblems(stages map[string]string, path string) []Problem {
	var ps []Problem
	named := map[string]bool{}
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		account := stages[stage]
		if account == "" {
			continue
		}
		if !slices.Contains(MixedAccountStages, stage) {
			ps = append(ps, Problem{Path: path + "." + stage + ".claude_account",
				Message: fmt.Sprintf("an api-key run's %s stage runs on the provider key the gateway holds, so a Claude account named for it would never be used: the one account such a run may hold serves %s and no other stage (docs/design/mixed-credentials.md §11) — name the account for %s, or remove this key",
					stage, strings.Join(MixedAccountStages, " and "), strings.Join(MixedAccountStages, " or "))})
		}
		named[account] = true
	}
	if len(named) > 1 {
		ps = append(ps, Problem{Path: path,
			Message: fmt.Sprintf("an api-key run holds one provider key and one Claude account, and these keys name %d (%s): name one account for %s, or none",
				len(named), strings.Join(slices.Sorted(maps.Keys(named)), ", "), strings.Join(MixedAccountStages, " and "))})
	}
	return ps
}

// CheckMixBudget is the mix's budget-mode clause, and the second half of the
// same rule (docs/design/mixed-credentials.md §11): an api-key run's one
// Claude account exists because the gateway holds the provider key and routes
// the coder's calls to it, and only budget.mode: enforce routes a call to a
// provider at all (CheckProviderGateway). So the mix is admitted only when the
// mode the run resolves is enforce — and the clause is judged only where the
// run has the shape that could mix: a coder pin a route this repository may
// use serves. With no such coder there is no provider key in the gateway, the
// account would never be used, and the config is none of this rule's business.
//
// auth, mode, coder and routes are the run's as the caller resolves them (the
// launch and `fugaro validate` each build them the way the run does); def is
// the project default_claude_account, "" when none is set; stages is the
// declared stage accounts, as CheckStageAccountMix reads them. path is where
// the caller spells the budget mode ("budget.mode" today), and it is the key
// the problem points at: the fix is to raise the mode, or to drop the keys
// that declare a mix this run cannot carry.
func CheckMixBudget(auth, mode, coder, def string, stages map[string]string, routes map[string]ModelProvider, repo, path string) []Problem {
	if auth != "api-key" || mode == BudgetEnforce {
		return nil
	}
	var keys []string
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		if stages[stage] != "" && slices.Contains(MixedAccountStages, stage) {
			keys = append(keys, "stages."+stage+".claude_account")
		}
	}
	if def != "" {
		keys = append(keys, "default_claude_account")
	}
	if len(keys) == 0 {
		return nil
	}
	name, _, served := ProviderFor(routes, coder)
	if !served {
		return nil
	}
	return []Problem{{Path: path, Message: fmt.Sprintf(
		"an api-key run may hold one Claude account for %s only with budget.mode: enforce, and this run resolves %s: the provider key lives in the gateway, which is what routes a call to %s (a provider model is refused in any other mode), so under %s the account named by %s would never be used — set budget.mode: enforce, or remove those keys",
		strings.Join(MixedAccountStages, " and "), modeName(mode), name, modeName(mode), strings.Join(keys, ", "))}}
}

// modeName is a resolved budget mode as a person reads it: the merged layers
// spell "no gateway" as "" (policy's own zero value) and the config's own
// vocabulary spells it "off" (config.BudgetOff), and a sentence that printed
// the empty string would name nothing.
func modeName(mode string) string {
	if mode == "" {
		return BudgetOff
	}
	return mode
}

func stageAccountUnderOtherAuth(auth string, stages map[string]string, path string) []Problem {
	var ps []Problem
	for _, stage := range slices.Sorted(maps.Keys(stages)) {
		if stages[stage] == "" {
			continue
		}
		ps = append(ps, Problem{Path: path + "." + stage + ".claude_account",
			Message: fmt.Sprintf("a stage account only applies to a run whose agent.auth is oauth or api-key, and this run's is %s: under %s no Claude OAuth credential reaches the run, so the account would never be used — remove the key, or set agent.auth to oauth (the run's one account) or api-key (one account for %s and no other stage)",
				auth, auth, strings.Join(MixedAccountStages, " and "))})
	}
	return ps
}
