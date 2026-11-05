package config

import (
	"strings"
	"testing"
)

// TestRunStagesAreTheModelCallingStages pins the closed vocabulary the
// `stages.<stage>.claude_account` segment is held to
// (docs/accounts-add/spec.md §11.1): the stages a run spends a model call on,
// and nothing else. The names are the runner's own stage literals plus
// recipe.StepAdversarial, and the stages that spend no model call —
// bootstrap, render, finalize, writeback and provision — are deliberately
// absent: no account could apply to them, so naming one is refused rather than
// silently never applying.
func TestRunStagesAreTheModelCallingStages(t *testing.T) {
	want := []string{"plan", "implement", "review_first", "review", "adversarial", "fix"}
	if len(RunStages) != len(want) {
		t.Fatalf("RunStages = %v, want %v", RunStages, want)
	}
	for i, stage := range want {
		if RunStages[i] != stage {
			t.Fatalf("RunStages = %v, want %v", RunStages, want)
		}
		if !IsRunStage(stage) {
			t.Errorf("IsRunStage(%q) = false", stage)
		}
	}
	for _, stage := range []string{"bootstrap", "render", "finalize", "writeback", "provision", "Review", ""} {
		if IsRunStage(stage) {
			t.Errorf("IsRunStage(%q) = true, want false", stage)
		}
		if ps := CheckStageSegment(stage, "stages"); len(ps) != 1 || !strings.Contains(ps[0].Message, "is not a stage of a run") {
			t.Errorf("CheckStageSegment(%q) = %+v, want one refusal", stage, ps)
		}
	}
	if ps := CheckStageSegment("review", "stages"); ps != nil {
		t.Errorf("CheckStageSegment(review) = %+v, want none", ps)
	}
}

// TestCheckStageAccountMixIsTheB0Ruling is the ruling of
// docs/design/mixed-credentials.md §11 read as the one function every layer
// that judges it calls: an api-key run holds one provider key and one Claude
// account, and that account serves the senior review and the adversarial
// review and no other stage.
func TestCheckStageAccountMixIsTheB0Ruling(t *testing.T) {
	cases := []struct {
		name   string
		auth   string
		stages map[string]string
		want   [][]string // one entry per expected problem, each the substrings it must name
	}{
		{name: "api-key, the permitted mix", auth: "api-key",
			stages: map[string]string{"review": "acme-overflow", "adversarial": "acme-overflow"}},
		{name: "api-key, review alone", auth: "api-key", stages: map[string]string{"review": "acme-overflow"}},
		{name: "api-key, no stage account", auth: "api-key", stages: nil},
		{name: "api-key, the coder's stage refused", auth: "api-key", stages: map[string]string{"implement": "acme-overflow"},
			want: [][]string{{"stages.implement.claude_account", "would never be used", "review and adversarial"}}},
		{name: "api-key, review_first refused", auth: "api-key", stages: map[string]string{"review_first": "acme-overflow"},
			want: [][]string{{"stages.review_first.claude_account", "review and adversarial"}}},
		{name: "api-key, fix refused", auth: "api-key", stages: map[string]string{"fix": "acme-overflow"},
			want: [][]string{{"stages.fix.claude_account"}}},
		{name: "api-key, plan refused", auth: "api-key", stages: map[string]string{"plan": "acme-overflow"},
			want: [][]string{{"stages.plan.claude_account"}}},
		{name: "api-key, two accounts refused", auth: "api-key",
			stages: map[string]string{"review": "acme-overflow", "adversarial": "acme-personal"},
			want:   [][]string{{"stages", "one provider key and one Claude account", "acme-overflow, acme-personal"}}},
		{name: "api-key, a stage the tree does not have yet", auth: "api-key", stages: map[string]string{"digest": "acme-overflow"},
			want: [][]string{{"stages.digest.claude_account"}}},
		// Two problems at once: the stage key first (the sorted walk), the count
		// after it, so the sentence a person reads starts with the key to fix.
		{name: "api-key, a bad stage and a second account", auth: "api-key",
			stages: map[string]string{"implement": "acme-overflow", "review": "acme-personal"},
			want:   [][]string{{"stages.implement.claude_account"}, {"stages", "one provider key and one Claude account"}}},
		// The accounts design's own rung 2 is what an oauth run's stage key is
		// for: one account for a stage and another for the rest is admitted, and
		// no stage is out of bounds.
		{name: "oauth keeps the accounts design's meaning", auth: "oauth",
			stages: map[string]string{"implement": "acme-overflow", "review": "acme-personal", "fix": "acme-overflow"}},
		{name: "oauth names one account", auth: "oauth", stages: map[string]string{"review": "acme-overflow"}},
		{name: "oauth names none", auth: "oauth", stages: nil},
		// "Never under another auth": neither vertex nor cascade reaches a Claude
		// OAuth credential, so an account there could never be used.
		{name: "vertex refused", auth: "vertex", stages: map[string]string{"review": "acme-overflow"},
			want: [][]string{{"stages.review.claude_account", "this run's is vertex"}}},
		{name: "cascade refused", auth: "cascade", stages: map[string]string{"review": "acme-overflow"},
			want: [][]string{{"stages.review.claude_account", "this run's is cascade"}}},
		{name: "vertex with no stage account", auth: "vertex", stages: nil},
		// The launch resolves the auth from a checkout it may not have yet, so
		// an auth it cannot place is not judged here: the run judges it with the
		// auth it resolves.
		{name: "an unknown auth is not judged", auth: "", stages: map[string]string{"implement": "acme-overflow"}},
		// Every other value is refused rather than read as oauth: a committed
		// fugaro.yaml's agent.auth is its own bytes, so a value this binary
		// cannot place must not be the one that admits a stage account.
		{name: "an auth this binary cannot place is refused", auth: "api_key",
			stages: map[string]string{"review": "acme-overflow"},
			want:   [][]string{{"stages.review.claude_account", `"api_key"`, "not a value this Fugaro knows"}}},
		{name: "an auth this binary cannot place, with no stage account", auth: "apikey", stages: nil},
		{name: "an auth this binary cannot place, with an empty value", auth: "nonesuch", stages: map[string]string{"review": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := CheckStageAccountMix(tc.auth, tc.stages, "stages")
			if len(ps) != len(tc.want) {
				t.Fatalf("CheckStageAccountMix(%q, %v) = %+v, want %d problem(s)", tc.auth, tc.stages, ps, len(tc.want))
			}
			for i, wants := range tc.want {
				for _, want := range wants {
					if !strings.Contains(ps[i].Path, want) && !strings.Contains(ps[i].Message, want) {
						t.Errorf("problem %d = %+v, want it to name %q", i, ps[i], want)
					}
				}
			}
		})
	}
}

// TestCheckMixBudgetIsTheModeClause is the second half of the same rule: the
// one account an api-key run may hold exists because the gateway holds the
// provider key and routes the coder's calls to it, and only budget.mode:
// enforce routes a call to a provider at all. A configuration that declares a
// mix under any other mode declares a key that could never apply, which is
// what the whole rule refuses rather than ignores.
func TestCheckMixBudgetIsTheModeClause(t *testing.T) {
	// The owner's route table, claiming the coder's model for this repository:
	// one entry that serves it, and one that does not.
	routes := map[string]ModelProvider{
		"openrouter": {Kind: "anthropic-compat", BaseURL: "https://openrouter.ai/api",
			Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/other"}},
		"vendorx": {Kind: "anthropic-compat", BaseURL: "https://vendorx.example",
			Models: []string{"vendorx/*"}, AllowDataTo: []string{"acme/app"}},
	}
	const coder = "deepseek/deepseek-v4-flash"
	mixed := map[string]string{"review": "acme-overflow", "adversarial": "acme-overflow"}
	cases := []struct {
		name    string
		auth    string
		mode    string
		coder   string
		def     string
		stages  map[string]string
		routes  map[string]ModelProvider // nil: the owner's whole table
		want    []string                 // substrings the one problem must name; nil for none
		notWant []string                 // substrings the problem must not name
	}{
		{name: "api-key under enforce", auth: "api-key", mode: BudgetEnforce, coder: coder, def: "acme-overflow", stages: mixed},
		{name: "api-key under observe", auth: "api-key", mode: BudgetObserve, coder: coder, def: "acme-overflow", stages: mixed,
			want: []string{"budget.mode", "enforce", "observe", "default_claude_account", "stages.review.claude_account", "openrouter"}},
		{name: "api-key under off, the project default alone", auth: "api-key", mode: BudgetOff, coder: coder, def: "acme-overflow",
			want: []string{"budget.mode", "default_claude_account"}},
		{name: "api-key under off, a stage key alone", auth: "api-key", mode: BudgetOff, coder: coder, stages: mixed,
			want: []string{"budget.mode", "stages.adversarial.claude_account"}},
		// The sentinel is not an account: a local config may spell the project
		// default "default" (localcfg's choiceRefProblem admits it), which names
		// the unsuffixed default account and no account of its own, and an
		// api-key run's job carries no OAuth token for the default at all. So
		// the key declares no mix, and the clause stays silent — a refusal here
		// would tell an operator to raise budget.mode for a key that names
		// nothing, and on the launch's side it would invent the second
		// credential mixedStageAccount refuses.
		{name: "api-key under observe, the project default is the sentinel", auth: "api-key", mode: BudgetObserve, coder: coder, def: DefaultClaudeAccount},
		{name: "api-key under off, the sentinel beside a real stage key", auth: "api-key", mode: BudgetOff, coder: coder, def: DefaultClaudeAccount,
			stages: map[string]string{"review": "acme-overflow"},
			want:   []string{"budget.mode", "stages.review.claude_account"}, notWant: []string{"default_claude_account"}},
		// The clause is judged only where the run has the shape that could mix:
		// with no provider route serving this repository's coder there is no
		// provider key in the gateway, whatever the mode says. The table is the
		// run's own — routes this repository may use, runner.ProvidersFor's own
		// narrowing, done by the caller — so a route written for another
		// repository is simply not here, which the last case pins.
		{name: "a coder no route serves", auth: "api-key", mode: BudgetObserve, coder: "claude-sonnet-5", def: "acme-overflow"},
		{name: "a route this repository does not carry", auth: "api-key", mode: BudgetObserve, coder: "vendorx/model", def: "acme-overflow",
			routes: map[string]ModelProvider{"openrouter": routes["openrouter"]}},
		{name: "no coder pin", auth: "api-key", mode: BudgetObserve, def: "acme-overflow"},
		// Nothing declared is nothing to refuse: default_claude_account is how
		// an oauth run picks its credential, and a run that names no mix is not
		// this clause's business under any mode.
		{name: "nothing declared", auth: "api-key", mode: BudgetObserve, coder: coder},
		{name: "a stage key outside the mix", auth: "api-key", mode: BudgetObserve, coder: coder, stages: map[string]string{"implement": "acme-overflow"}},
		{name: "an oauth run", auth: "oauth", mode: BudgetObserve, coder: coder, def: "acme-overflow", stages: mixed},
		{name: "an auth the launch cannot place yet", auth: "", mode: BudgetObserve, coder: coder, def: "acme-overflow", stages: mixed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := tc.routes
			if table == nil {
				table = routes
			}
			ps := CheckMixBudget(tc.auth, tc.mode, tc.coder, tc.def, tc.stages, table, "budget.mode")
			if tc.want == nil {
				if ps != nil {
					t.Fatalf("CheckMixBudget(%q, %q, %q, %q, %v) = %+v, want no problem", tc.auth, tc.mode, tc.coder, tc.def, tc.stages, ps)
				}
				return
			}
			if len(ps) != 1 {
				t.Fatalf("CheckMixBudget(%q, %q, %q, %q, %v) = %+v, want one problem", tc.auth, tc.mode, tc.coder, tc.def, tc.stages, ps)
			}
			for _, want := range tc.want {
				if !strings.Contains(ps[0].Path, want) && !strings.Contains(ps[0].Message, want) {
					t.Errorf("problem = %+v, want it to name %q", ps[0], want)
				}
			}
			for _, not := range tc.notWant {
				if strings.Contains(ps[0].Path, not) || strings.Contains(ps[0].Message, not) {
					t.Errorf("problem = %+v, want it not to name %q", ps[0], not)
				}
			}
		})
	}
}
