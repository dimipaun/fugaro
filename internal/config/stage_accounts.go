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
func CheckStageAccountMix(auth string, stages map[string]string, path string) []Problem {
	switch auth {
	case "api-key":
		return mixedAccountProblems(stages, path)
	case "vertex", AuthCascade:
		return stageAccountUnderOtherAuth(auth, stages, path)
	default:
		return nil
	}
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
