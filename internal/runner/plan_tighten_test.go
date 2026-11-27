package runner

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// tightenPlanCfg is a repository file and a project layer that says nothing
// about any round knob, resolved through config.Resolve as a run resolves
// them: the merge keeps the file's own numbers and marks them unchecked (RT1,
// R3), which is the state RT2 reads.
func tightenPlanCfg(t *testing.T, agent string) (*config.Config, *config.Resolution) {
	t.Helper()
	const layer = "version: 1\nproject: aurora\ngcp_project: aurora-fugaro\n" +
		"defaults: { git: { provider: github } }\n" +
		"profiles:\n  p:\n    commands: { build: make, test: make test }\ndefault_profile: p\n"
	l, ps := config.ParseProjectLayer([]byte(layer), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	file := "version: 1\nproject: aurora\ngcp_project: aurora-fugaro\ngit: { provider: github, base_branch: main }\n" +
		agent + "workflows:\n  server: { commands: { build: make, test: make test } }\n"
	c, res, ps := config.Resolve([]byte(file), l)
	if len(ps) > 0 {
		t.Fatalf("config problems: %v", ps)
	}
	return c, res
}

// TestUncheckedRoundsTakeTheMode is RT2 (R3, R20, spec §5.2): a repository's
// round knob the project layer says nothing about is kept unjudged by the
// merge, so the plan is where it is judged — against the mode's own row
// (rigor.DefaultRounds, rigor.AdversarialDefault for level 3), which is the
// baseline the run would have taken had the file written nothing. The tighter
// of the two stands: a knob below the row stands down and the step takes the
// row with Precedence 0 — exactly as for a layer that wrote nothing, which is
// what lets a mode a run raises between rounds supply the ones remaining
// (reviewRounds, D22) — and a knob above it is the run's own number.
func TestUncheckedRoundsTakeTheMode(t *testing.T) {
	knobs := parseRecipe(t, "version: 1\nname: knobs\nsteps:\n  - first_line: {}\n  - review: {}\n  - adversarial: {}\n")
	for _, tc := range []struct {
		name   string
		agent  string
		mode   rigor.Rigor
		l3     bool
		want   []planStep
		marked []string
	}{
		{
			// The knob is a repository's and the mode's row is Pro's three:
			// the file's two stands down, and the step records the mode as
			// the one that decided.
			name:   "a knob under the row takes the row",
			agent:  "agent: { review_rounds: 2, first_line_rounds: 1 }\n",
			mode:   rigor.Pro,
			want:   []planStep{{Kind: recipe.StepFirstLine, Rounds: 3}, {Kind: recipe.StepReview, Rounds: 3, FirstRounds: 3}},
			marked: []string{"agent.first_line_rounds", "agent.review_rounds"},
		},
		{
			// The review knob is above the row: it is the tighter of the two,
			// so it stands and the step records it as what decided. The
			// first-line knob is left unwritten, and at Pro it could not be
			// above the row either way: the recipe bounds it at the row's own
			// three (recipe.MaxFirstLineRounds).
			name:   "a knob over the row stands",
			agent:  "agent: { review_rounds: 8 }\n",
			mode:   rigor.Pro,
			want:   []planStep{{Kind: recipe.StepFirstLine, Rounds: 3}, {Kind: recipe.StepReview, Rounds: 8, Precedence: 8, FirstRounds: 3}},
			marked: []string{"agent.review_rounds"},
		},
		{
			// Level 3's own knob is judged the same way against
			// rigor.AdversarialDefault, which at Critical is the senior row's
			// five and not the two the file wrote.
			name:   "an adversarial knob under the row takes the row",
			agent:  "agent: { adversarial_rounds: 2 }\n",
			mode:   rigor.Critical,
			l3:     true,
			want:   []planStep{{Kind: recipe.StepFirstLine, Rounds: 3}, {Kind: recipe.StepReview, Rounds: 5, FirstRounds: 3}, {Kind: recipe.StepAdversarial, Rounds: 5, FirstRounds: 3}},
			marked: []string{"agent.adversarial_rounds"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, res := tightenPlanCfg(t, tc.agent)
			for _, key := range tc.marked {
				if !slices.Contains(res.Unchecked, key) {
					t.Fatalf("%s is not marked unchecked: the case below is about a knob the project layer says nothing about, and this one is not (%v)", key, res.Unchecked)
				}
			}
			got := planOf(knobs, planLevels(true, true, tc.l3), planAgent(cfg, tc.mode), nil, tc.mode)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("plan at %s = %+v, want %+v (RT2: the tighter of the file's knob and the mode's row)", tc.mode, got, tc.want)
			}
		})
	}
}

// stageBudgetLayer is a project layer that sets the two keys no mode row holds
// (R20): the case where the reader must not judge the file's figure, because
// the merge already did (RT1).
func stageBudgetLayer(t *testing.T, usd, stop float64) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte(fmt.Sprintf(
		"version: 1\nproject: aurora\ngcp_project: aurora-fugaro\ndefaults: { git: { provider: github }, agent: { max_budget_usd: %v, review_stop_fraction: %v } }\nprofiles:\n  p:\n    commands: { build: make, test: make test }\ndefault_profile: p\n", usd, stop)),
		config.LayerAnchor{Project: "aurora", GCPProject: "aurora-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// TestStageBudgetTakesTheBaseline is RT2's leftover (R3, R20, spec §5.2): the
// runner's own readers of the two keys no mode row holds — the per-stage model
// cap agent.max_budget_usd (yield.go's stageBudget and stageRemaining, and the
// checkpoint call) and the stop rule agent.review_stop_fraction — take the
// tighter of the repository's value and Fugaro's own default where the project
// layer says nothing. So a silent project's runs stop at the baseline and not
// where the file asks, exactly as the launcher's preview already quotes them.
func TestStageBudgetTakesTheBaseline(t *testing.T) {
	start := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		cfg     *config.Config
		wantCap float64
		// wantStop is the instant the loop must stop starting rounds: the
		// baseline's share of a ten-minute budget for a looser value under a
		// silent project, and the file's own share where it stands.
		wantStop time.Time
	}{
		{
			name:     "a silent project and figures over the baseline",
			cfg:      &config.Config{Agent: config.Agent{MaxBudgetUSD: 100, ReviewStopFraction: 0.2}},
			wantCap:  config.DefaultMaxBudgetUSD,
			wantStop: start.Add(8 * time.Minute),
		},
		{
			name:     "a silent project and figures under the baseline",
			cfg:      &config.Config{Agent: config.Agent{MaxBudgetUSD: 10, ReviewStopFraction: 0.9}},
			wantCap:  10,
			wantStop: start.Add(9 * time.Minute),
		},
		{
			name:     "a project that sets the keys",
			cfg:      &config.Config{Layer: stageBudgetLayer(t, 100, 0.2), Agent: config.Agent{MaxBudgetUSD: 100, ReviewStopFraction: 0.2}},
			wantCap:  100,
			wantStop: start.Add(2 * time.Minute),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &run{cfg: tc.cfg, rec: &runstore.Record{}}
			if got := r.stageBudget(stageOpts{}); got != tc.wantCap {
				t.Errorf("stageBudget = %v, want %v (R20: the stage cap takes the tighter of the file's figure and the baseline)", got, tc.wantCap)
			}
			if got := r.stageRemaining(&yieldStage{}); got != tc.wantCap {
				t.Errorf("stageRemaining = %v, want %v", got, tc.wantCap)
			}
			// The stop rule's fraction as setup reads it. A looser value under
			// a silent project reads as unset, which is what Budget.stopAt
			// supplies the baseline for.
			b := Budget{StopFraction: config.TightenStopFraction(tc.cfg, tc.cfg.Agent.ReviewStopFraction),
				Start: start, Total: 10 * time.Minute, Reserve: time.Minute}
			at, ok := b.stopAt()
			if !ok || !at.Equal(tc.wantStop) {
				t.Errorf("the loop stops starting rounds at %s (ok=%v), want %s (R20: the stop rule takes the tighter of the file's share and the baseline)", at, ok, tc.wantStop)
			}
		})
	}
}
