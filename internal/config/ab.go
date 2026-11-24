package config

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// AB is the project layer's ab: block (docs/model-ab-testing, D550-D573):
// whether a project samples its first code runs, which other models may run
// as a challenger, which model judges a pair, and how much extra cost per
// pair a mode accepts before keeping the champion.
//
// It is a top-level key of the layer file, like governance: and
// claude_models: and for the same reason: the defaults: overlay resolves a
// repository's own key over the layer's (resolve.go), so a block under
// defaults: would let a repository replace the owner's choice instead of
// only tightening it. Read here, the two meet only in the merge
// (internal/policy), which takes the lower rate and nothing else from the
// repository. A repository's own ab: block carries ab.rate: 0 alone, its
// opt-out (D556, D557).
//
// Nothing reads these keys to launch a pair yet: the sampler, the challenger
// admission and the pair launch arrive with ee/ab (A3.x, 0.15.0), so this
// block is inert until then.
type AB struct {
	// Rate is the share of eligible runs sampled, in [0, ABMaxRate]; 0 (the
	// default) is off, and a run is sampled by a hash of its run ID below
	// the rate (D557).
	Rate float64 `yaml:"rate"`
	// Repos is the project's per-repository rate, keyed owner/name as
	// rigor.repos is, each value in [0, Rate]: the repository's own rate
	// replaces the block's, so 0 opts that one repository out without
	// turning the project's sampling off (D3297). A repository present here
	// is sampled at its own rate only; one left out takes Rate.
	//
	// It is the project layer's alone, like the challenger list: a
	// repository's file carries ab.rate: 0 and nothing else, and an
	// ab.repos there is refused (validateRepoAB). It is what ee/ab reads a
	// repository's rate from once the planner has moved every per-repository
	// opt-out here (A3.x).
	Repos map[string]float64 `yaml:"repos"`
	// MaxPairsPerDay is the most pairs one repository is sampled for per
	// UTC day, counted by the launcher from today's challenger records
	// (D557, D566). 0 takes the shipped ABDefaultMaxPairsPerDay.
	MaxPairsPerDay int `yaml:"max_pairs_per_day"`
	// Challengers are the models the owner allows as a challenger, in
	// priority order. Never a repository's to name (D556): a repository
	// author could otherwise send that repository's code to a provider the
	// owner opened only to others.
	Challengers []string `yaml:"challengers"`
	// JudgeModel is the model a pair's blind judge run uses (D553). Like
	// Challengers it is the owner's alone.
	JudgeModel string `yaml:"judge_model"`
	// ExchangeRate is the extra median cost a pair may carry, as a fraction
	// of the champion's median cost, before the mode keeps the champion
	// (D565). A mode left out takes its shipped figure
	// (ABExchangeRateDefault); POC's 0 means "never worth extra cost".
	ExchangeRate map[rigor.Rigor]float64 `yaml:"exchange_rate"`
}

const (
	// ABMaxRate is the highest sampling rate a project layer may set
	// (D557): the ceiling on what the owner's opt-in may cost.
	ABMaxRate = 0.10
	// ABDefaultMaxPairsPerDay is the most pairs a repository is sampled for
	// per day when the layer writes no max_pairs_per_day (D557).
	ABDefaultMaxPairsPerDay = 5
)

// ABExchangeRateDefault is the extra-cost figure Fugaro ships for mode
// (D565): what a pair's challenger may cost more than the champion, as a
// fraction of the champion's median cost, before the mode keeps the
// champion. POC accepts none, Startup a quarter, Pro a half and Critical
// double. Hotfix is not eligible for sampling at all (D558), so its figure
// is 0: a pair is never sampled for one.
func ABExchangeRateDefault(m rigor.Rigor) float64 {
	switch m {
	case rigor.Startup:
		return 0.25
	case rigor.Pro:
		return 0.5
	case rigor.Critical:
		return 2.0
	}
	return 0
}

// ExchangeRates is the layer's exchange-rate table with every mode it leaves
// out filled in from ABExchangeRateDefault, so a reader never has to tell a
// missing mode from a zero one. The map is always a fresh one, the layer's
// own map copied into it, so a caller cannot write into the block it read.
func (a *AB) ExchangeRates() map[rigor.Rigor]float64 {
	out := make(map[rigor.Rigor]float64, len(rigor.Rigors))
	for _, m := range rigor.Rigors {
		out[m] = ABExchangeRateDefault(m)
	}
	if a == nil {
		return out
	}
	for m, v := range a.ExchangeRate {
		out[m] = v
	}
	return out
}

// MaxPairs is MaxPairsPerDay with the shipped default applied: 0 is "not
// set", so a layer that writes none gets ABDefaultMaxPairsPerDay and a
// layer that wants fewer names a lower count (rate 0 is how a project stops
// sampling altogether).
func (a *AB) MaxPairs() int {
	if a == nil || a.MaxPairsPerDay == 0 {
		return ABDefaultMaxPairsPerDay
	}
	return a.MaxPairsPerDay
}

// Off reports whether the layer samples nothing: no block at all, or a rate
// of 0. It is the whole of what a repository can say with its own ab: block.
func (a *AB) Off() bool { return a == nil || a.Rate == 0 }

// validateAB checks the project layer's ab: block (D557, D565, D3297). What
// it refuses is a figure no sampling run could use — a rate outside [0,
// ABMaxRate], a negative daily count, a per-repository rate outside [0,
// ab.rate], a repository name that is no repository name, a model ID that is
// no model ID, an unknown rigor mode, a negative exchange rate — and never a
// rate the owner chose: the block is his to set, and where code may go is
// decided by the challenger list he writes, not by this check.
func validateAB(p string, a *AB) []Problem {
	if a == nil {
		return nil
	}
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if math.IsNaN(a.Rate) || a.Rate < 0 || a.Rate > ABMaxRate {
		add(p+".rate", "must be between 0 and %v: 0 (the default) turns sampling off, and a project never samples more than a tenth of its eligible runs (D557)", ABMaxRate)
	}
	if a.MaxPairsPerDay < 0 {
		add(p+".max_pairs_per_day", "must not be negative")
	}
	repos := slices.Sorted(maps.Keys(a.Repos))
	if len(repos) > rigor.MaxRepoLevels {
		add(p+".repos", "must hold at most %d repositories", rigor.MaxRepoLevels)
		repos = nil
	}
	for _, name := range repos {
		v := a.Repos[name]
		switch {
		case rigor.Member{Repo: name}.Validate() != "":
			add(p+".repos."+name, "must be a repository name, owner/name: letters, digits, dot, dash and underscore in each of two segments, and no . or .. segment")
		case math.IsNaN(v) || v < 0 || v > a.Rate:
			add(p+".repos."+name, "is %v: a repository's rate sits inside the block's own ab.rate %v (D3297), where 0 opts that repository out rather than the project (D556)", v, a.Rate)
		}
	}
	for i, m := range a.Challengers {
		if msg := CheckModelPattern(m); msg != "" {
			add(fmt.Sprintf("%s.challengers[%d]", p, i), "%s", msg)
		}
	}
	if a.JudgeModel != "" {
		if msg := CheckModelPattern(a.JudgeModel); msg != "" {
			add(p+".judge_model", "%s", msg)
		}
	}
	for _, m := range sortedModes(a.ExchangeRate) {
		v := a.ExchangeRate[m]
		if !slices.Contains(rigor.Rigors, m) {
			add(fmt.Sprintf("%s.exchange_rate.%s", p, showKey(string(m))), "is not a rigor mode (%s)", modesList())
			continue
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			add(fmt.Sprintf("%s.exchange_rate.%s", p, showKey(string(m))), "must be a finite number, not negative: it is what the mode accepts as the challenger's extra median cost per pair (D565)")
		}
	}
	return ps
}

// validateRepoAB checks a repository's ab: block: only the rate, and only 0
// (D556, D557). A repository may opt out of sampling its own runs; every
// other key is the owner's, because a repository author could otherwise
// name a challenger model and send that repository's code to a provider the
// owner opened only to others. Each message names the project layer, which
// is where the key belongs.
func validateRepoAB(a *AB) []Problem {
	if a == nil {
		return nil
	}
	var ps []Problem
	const owner = "only the project layer (" + LayerKey + ") may set it"
	add := func(path, msg string) { ps = append(ps, Problem{Path: path, Message: msg}) }
	if len(a.Challengers) > 0 {
		add("ab.challengers", "names challenger model "+showKey(a.Challengers[0])+": "+owner+" (D556, F18)")
	}
	if a.JudgeModel != "" {
		add("ab.judge_model", "names the judge model "+showKey(a.JudgeModel)+": "+owner+" (D553)")
	}
	if a.MaxPairsPerDay != 0 {
		add("ab.max_pairs_per_day", owner+": the daily pair count is the project's, counted per repository (D557)")
	}
	if names := slices.Sorted(maps.Keys(a.Repos)); len(names) > 0 {
		add("ab.repos", "names repository "+showKey(names[0])+": ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml; opt this repository out with ab.repos.<owner/name>: 0 in the project layer")
	}
	if modes := sortedModes(a.ExchangeRate); len(modes) > 0 {
		add("ab.exchange_rate", "carries mode "+showKey(string(modes[0]))+": "+owner+" (D565)")
	}
	if a.Rate != 0 {
		add("ab.rate", fmt.Sprintf("is %v: only the project layer (%s) may raise sampling above 0, and a repository may only set 0, which never samples it (D557)", a.Rate, LayerKey))
	}
	return ps
}

// WorkflowAB is a workflow's own ab: block: whether this workflow asks for an
// A/B reviewer arm, and at which level (the owner's ruling of 2026-11-16,
// docs/review-levels-ab-jobmax, O12). It is not the top-level AB: the sampling
// rate, the challenger list and the judge are the project's, sample a
// repository's first code runs and are read by the pair launch (ee/ab's Plan);
// this block asks for a second run at a review slot, which is a review of this
// workflow's own, so the ask sits where every other per-workflow selection
// does — under workflows.<name> — and the same key is what the launch's image
// gate reads (internal/cli's checkWorkflowABImage, the gate workflowABSince
// declares).
//
// The ask is a review level and never the coder: a coder pair is launched at
// the champion's own moment (D571, A3.3), while a reviewer arm waits for the
// champion's patch and replays that level over it (B1.2, B1.4), so `coder`
// here would name the pair that needs no key at all.
type WorkflowAB struct {
	// ReviewerArm is the review level of the arm this workflow asks for: l1,
	// l2 or l3. The absent block asks for no arm, and there is no spelling
	// for "the arm at the workflow's own level": a level is what the pair
	// varies, and a pair whose slot the launcher had to guess is one whose
	// two arms B1.3 could not group.
	ReviewerArm string `yaml:"reviewer_arm,omitempty" json:"reviewer_arm,omitempty"`
}

// ReviewerArm is the level this workflow asks its A/B reviewer arm to run at,
// or "" when the workflow asks for none. It is the one reader of the key: the
// launch (ee/ab's ReviewerArm) and the image gate that judges whether the job's
// runner knows the block at all (internal/cli's checkWorkflowABImage) read the
// same answer, so a launch can never ask the gate about one level and write
// another.
func (w Workflow) ReviewerArm() string {
	if w.AB == nil {
		return ""
	}
	return w.AB.ReviewerArm
}

// ReviewerArmLevel is ReviewerArm as the closed vocabulary of review levels:
// the workflow's ask when it makes one, or "" when it asks for no arm. A file
// whose ask is not one of the three is refused by Validate before any reader
// sees it, so the boolean is a reader's own guard (a Config built in memory by
// a caller) rather than a second refusal.
func (w Workflow) ReviewerArmLevel() (policy.Level, bool) {
	switch lv := policy.Level(w.ReviewerArm()); lv {
	case policy.LevelL1, policy.LevelL2, policy.LevelL3:
		return lv, true
	default:
		return "", false
	}
}

// validateWorkflowAB checks a workflow's ab: block: the ask is one of the three
// review levels and nothing else. `coder` is refused by name, because it is the
// one value a reader would reasonably try and it names the pair every release
// already runs; an unknown value is refused as what the vocabulary is, so a
// typo is a refusal here rather than a launch that samples nothing.
func validateWorkflowAB(p string, a *WorkflowAB) []Problem {
	if a == nil {
		return nil
	}
	switch a.ReviewerArm {
	case string(policy.LevelL1), string(policy.LevelL2), string(policy.LevelL3):
		return nil
	case "":
		return []Problem{{Path: p + ".reviewer_arm", Message: "must name the review level the A/B reviewer arm runs at (" + reviewerArmLevels + "): a block that names none asks for no arm, and this key is how a workflow asks for one"}}
	case string(RoleCoder):
		return []Problem{{Path: p + ".reviewer_arm", Message: "names the coder: a coder pair is launched at the champion's own moment (D571, A3.3) and asks nothing of this key, which is for the arm that replays a review level over the champion's patch (" + reviewerArmLevels + ")"}}
	}
	return []Problem{{Path: p + ".reviewer_arm", Message: "is " + showKey(a.ReviewerArm) + ": must be one of " + reviewerArmLevels + ", the levels a reviewer arm can replay"}}
}

// reviewerArmLevels is the vocabulary as a refusal prints it.
const reviewerArmLevels = "l1, l2 or l3"

// sortedModes is a map's modes in the vocabulary's order, an unknown mode
// after them and sorted, so one bad block reports its problems in the same
// order every time.
func sortedModes(m map[rigor.Rigor]float64) []rigor.Rigor {
	var unknown []rigor.Rigor
	for mode := range m {
		if !slices.Contains(rigor.Rigors, mode) {
			unknown = append(unknown, mode)
		}
	}
	out := make([]rigor.Rigor, 0, len(m))
	for _, mode := range rigor.Rigors {
		if _, ok := m[mode]; ok {
			out = append(out, mode)
		}
	}
	slices.Sort(unknown)
	return append(out, unknown...)
}

// modesList is the vocabulary as an error prints it.
func modesList() string {
	out := make([]string, 0, len(rigor.Rigors))
	for _, m := range rigor.Rigors {
		out = append(out, string(m))
	}
	return strings.Join(out, ", ")
}
