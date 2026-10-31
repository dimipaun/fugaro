// Package policy merges budget, model and governance policy layers so that
// only tightening wins. It is pure: no I/O, no clock, no globals.
//
// Merge is total: it never fails and never panics. Values that carry no
// meaning are treated as unset: a cap that is not a finite positive number
// (zero, negative, NaN, +Inf) and a mode that is not one of the three known
// strings. Callers that must reject such input (validation) use ValidMode and
// their own range checks before merging.
//
// Governance (docs/task-governance) merges by the same rule, because every
// one of its keys is a positive bound whose smaller value is the stricter
// one: a stage budget that is a smaller fraction of the per-run cap, a token
// ceiling, a timeout, a review ratio, a fan-out cap. What differs is where
// the defaults enter. Fugaro ships them (ee/governance's Defaults, through the
// hook; a build that links no ee/ features ships none) and they are the
// ceiling a run starts from, so the layer that may loosen them is the
// project's — Governance.WithDefaults turns that layer's block into the
// ceiling — and every layer above it, the repository's own file included,
// only ever tightens. Merge itself invents nothing: a layer that carries no
// governance leaves the ceiling it was given as it is.
//
// Allow-lists: a nil AllowedModels means no list was set anywhere, so every
// model is allowed. A non-nil list is a restriction, and a disjoint
// intersection is an EMPTY NON-NIL list, which means deny everything (it is
// not "unset"). Consumers must use Effective.HasAllowList and
// Effective.Allows and never test len(AllowedModels) == 0.
package policy

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/tier"
)

// Policy keys, as used in Effective.Sources and Ignored.Key.
const (
	KeyPerRunUSD     = "per_run_usd"
	KeyPerDayUSD     = "per_day_usd"
	KeyMode          = "mode"
	KeyMaxRunTokens  = "max_run_tokens"
	KeyAllowedModels = "allowed_models"
	// The per-call output limits of the coder and reviewer roles
	// (agent.max_output_tokens.*).
	KeyMaxOutputCoder    = "max_output_tokens.coder"
	KeyMaxOutputReviewer = "max_output_tokens.reviewer"
	// KeyABRate is the A/B sampling rate (docs/model-ab-testing, D557). It is
	// the one key of the ab: block the merge speaks: every other key of it is
	// the project layer's own and is read where it is set, never merged,
	// because a repository may only turn sampling off (D556).
	KeyABRate = "ab.rate"
	// The governance keys, spelled as fugaro.yaml spells them under
	// governance: (docs/task-governance/spec.md §2, §5). The three stages are
	// the ones a run's governance bounds: an artifact run's plan and render
	// steps have none.
	KeyReviewRatio           = "review_ratio"
	KeyReviewFloorCalls      = "review_floor_calls"
	KeyMaxSubagents          = "max_subagents"
	KeyStageBudgetImplement  = "stage_budget.implement"
	KeyStageBudgetReview     = "stage_budget.review"
	KeyStageBudgetFix        = "stage_budget.fix"
	KeyStageTokensImplement  = "stage_tokens.implement"
	KeyStageTokensReview     = "stage_tokens.review"
	KeyStageTokensFix        = "stage_tokens.fix"
	KeyStageTimeoutImplement = "stage_timeout.implement"
	KeyStageTimeoutReview    = "stage_timeout.review"
	KeyStageTimeoutFix       = "stage_timeout.fix"
	// The three keys every tier of governance.tiers carries, spelled as
	// fugaro.yaml spells them (T8.1). A tier's own key is one of these three
	// behind TierKey, so the twelve names are built rather than declared.
	KeyExpectedUSD     = "expected_usd"
	KeyExpectedMinutes = "expected_minutes"
)

// TierKey is the policy key of one tier's own key, as Effective.Sources and
// Ignored.Key spell it ("tiers.trivial.expected_usd").
func TierKey(t tier.Tier, key string) string { return "tiers." + string(t) + "." + key }

// Layer names, as used in Effective.Sources and Ignored.Source.
const (
	SourceCeiling       = "ceiling"
	SourceDefaultBranch = "default-branch"
	SourceBranch        = "branch"
	// SourcePolicy is the ceiling of a run whose project has published a
	// policy: the first layer is then the RTDB node (config/policy/shared),
	// not the copy of the owner's config the job was deployed with, and a
	// refusal that tells the owner where to raise a limit must not name the
	// other one (plan task 1.3 step 6).
	SourcePolicy = "policy"
)

// Budget modes, loosest to strictest.
const (
	ModeOff     = "off"
	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

// The governance defaults Fugaro ships (plan T7.1, the owner's answers D331
// and D332 of 2026-10-26). They are guardrails, not a floor: the project
// layer may replace any of them for every repository of the project, up to
// the per-run cap, and every layer above it may only tighten what it left.
//
// The stage budgets are fractions of the run's per-run cap — fix's of the
// implement stage's own budget, G§2's "a fraction of implementation budget" —
// and not one of them reaches 1, so no stage can spend the cap alone: the cap
// stays the ceiling whatever a stage is given (G6). The fan-out default is
// the spec's own incident made a stop: a review that fanned out to about ten
// sub-agents is refused at six.
const (
	DefaultReviewRatio      = 5
	DefaultReviewFloorCalls = 20
	DefaultMaxSubagents     = 5

	DefaultStageBudgetImplement = 0.7
	DefaultStageBudgetReview    = 0.5
	DefaultStageBudgetFix       = 0.5
)

// TierExpectation is one tier's expectation: what a run of that tier is
// expected to cost and take, and how far one of its stages may fan out. Every
// field is an expectation and never a cap (G6).
type TierExpectation struct {
	// ExpectedUSD is what a run of the tier is expected to cost, in dollars.
	ExpectedUSD float64
	// ExpectedMinutes is what a run of the tier is expected to take.
	ExpectedMinutes int64
	// MaxSubagents is the most distinct sub-agents one stage of such a run may
	// start, concurrently or in total (spec §5).
	MaxSubagents int64
}

// TierExpectations is the expectation of every tier of tier.Tiers. The tiers
// are named one by one rather than held in a map, so an unknown tier name is an
// unknown key the strict decoder refuses (config.GovernanceTiers) rather than a
// map entry nothing checks. A tier added to the vocabulary is not a compile
// error here, though: TestTierExpectationsAreTheVocabulary walks this struct by
// reflection and fails until the new tier has a field, which is also what makes
// WithDefaults and the moved tighten rule (ee/governance's Tighten) name it.
type TierExpectations struct {
	Trivial TierExpectation
	Small   TierExpectation
	Medium  TierExpectation
	Large   TierExpectation
}

// For is ts's expectation for t, and the zero TierExpectation for a value that
// is no tier of the vocabulary. It is the one lookup from the vocabulary to the
// field that holds a tier's expectation, so a reader — the run that records
// what a tier expects (plan T8.1) — asks for a tier by name rather than
// spelling the four fields out again.
//
// A tier added to tier.Tiers does not fail to compile here: the switch's
// missing case returns the zero expectation, which TestTierDefaultsFromProjectLayer
// catches by walking the vocabulary (tier.Tiers) against the shipped figures
// through this lookup.
func (ts TierExpectations) For(t tier.Tier) TierExpectation {
	switch t {
	case tier.Trivial:
		return ts.Trivial
	case tier.Small:
		return ts.Small
	case tier.Medium:
		return ts.Medium
	case tier.Large:
		return ts.Large
	}
	return TierExpectation{}
}

// withDefaults is t with every key it leaves at zero replaced by d's. A key it
// does set is kept exactly as written, looser than the default included: the
// project layer is the one layer that may loosen a guardrail.
func (t TierExpectation) withDefaults(d TierExpectation) TierExpectation {
	if t.ExpectedUSD == 0 {
		t.ExpectedUSD = d.ExpectedUSD
	}
	if t.ExpectedMinutes == 0 {
		t.ExpectedMinutes = d.ExpectedMinutes
	}
	if t.MaxSubagents == 0 {
		t.MaxSubagents = d.MaxSubagents
	}
	return t
}

// Governance is one layer's governance limits. Every field's zero value means
// "unset", so a layer that writes no governance: block is the zero Governance
// and a key a layer leaves out passes the earlier layers through.
type Governance struct {
	// ReviewRatio is N: review calls above N x the implement stage's calls
	// stop the stage (spec §2's ratio guardrail). ReviewFloorCalls is the
	// count below which the ratio is not applied at all, so a short
	// implementation is still reviewed: the bound is
	// max(floor, N x implement calls).
	ReviewRatio      int64
	ReviewFloorCalls int64
	// MaxSubagents is the most distinct sub-agents one stage may start,
	// concurrently or in total (spec §5).
	MaxSubagents int64
	// StageBudget, StageTokens and StageTimeout bound one stage each.
	StageBudget  StageBudget
	StageTokens  StageTokens
	StageTimeout StageTimeout
	// Tiers are the per-tier expectations (G§3, D330): what a run of a tier is
	// expected to cost and take, and how far one of its stages may fan out.
	Tiers TierExpectations
}

// StageBudget is a stage's share of the run's budget: a fraction of the
// per-run cap, except Fix, which is a fraction of the implement stage's own
// budget (G§2). The two bases are the gateway's arithmetic (T7.2); the merge
// compares each key with itself, so a layer's smaller fraction always wins.
type StageBudget struct{ Implement, Review, Fix float64 }

// StageTokens is a stage's token ceiling, in tokens.
type StageTokens struct{ Implement, Review, Fix int64 }

// StageTimeout is a stage's own wall-clock ceiling; the run's
// timeouts.stage still bounds it (T7.6).
type StageTimeout struct{ Implement, Review, Fix time.Duration }

// WithDefaults is g with every key it leaves unset replaced by Fugaro's
// default. It is how the project layer's governance block becomes the ceiling
// a run starts under: the defaults stop runs out of the box, and the project
// layer is the one layer that may loosen them (plan §7), because what is
// above it — the default branch's fugaro.yaml and the branch the run checks
// out, which the agent can edit mid-run — is the repository's own and may
// only tighten. A key the layer does set is kept exactly as written, looser
// than the default included.
//
// The result is a ceiling layer, so the keys it fills are sourced as the
// ceiling and not as the file that wrote the block — the defaults are
// Fugaro's, not the operator's, so a consumer that has to name the owner of a
// governance value asks DefaultGovernanceKeys before it says "ceiling", the
// way policySources renames the ceiling for the keys the published node set
// (H1).
//
// The shipped figures arrive through the policy hook (hook.go), whose default
// is a zero Governance: the figures themselves are ee/governance's
// (governance.Defaults, §5.2), and this is the one place the core asks for
// them. An untagged build, whose hook is that default, therefore fills in
// nothing here (D522).
func (g Governance) WithDefaults() Governance {
	d := governanceHook().Defaults()
	if g.ReviewRatio == 0 {
		g.ReviewRatio = d.ReviewRatio
	}
	if g.ReviewFloorCalls == 0 {
		g.ReviewFloorCalls = d.ReviewFloorCalls
	}
	if g.MaxSubagents == 0 {
		g.MaxSubagents = d.MaxSubagents
	}
	if g.StageBudget.Implement == 0 {
		g.StageBudget.Implement = d.StageBudget.Implement
	}
	if g.StageBudget.Review == 0 {
		g.StageBudget.Review = d.StageBudget.Review
	}
	if g.StageBudget.Fix == 0 {
		g.StageBudget.Fix = d.StageBudget.Fix
	}
	// A tier defaults per key, so a layer that sets only one key of a tier
	// keeps Fugaro's default for the others of that same tier.
	g.Tiers = TierExpectations{
		Trivial: g.Tiers.Trivial.withDefaults(d.Tiers.Trivial),
		Small:   g.Tiers.Small.withDefaults(d.Tiers.Small),
		Medium:  g.Tiers.Medium.withDefaults(d.Tiers.Medium),
		Large:   g.Tiers.Large.withDefaults(d.Tiers.Large),
	}
	// stage_tokens and stage_timeout have no default: a stage with no token
	// ceiling and no timeout of its own is bounded by the run's own.
	return g
}

// DefaultGovernanceKeys are the keys of g whose value is the one Fugaro ships
// for them (ee/governance's Defaults, through the hook as WithDefaults reads
// it; an untagged build ships none, so none is named there, D522), keyed as
// Effective.Sources and Ignored.Key
// spell them. It is how a consumer tells a guardrail Fugaro itself set from
// one a layer chose: the ceiling every run starts under is built with the
// defaults (WithDefaults), so a key whose effective value is still the shipped
// figure is the ceiling's only because nothing wrote it — no layer made that
// decision, and a record that sourced it as the ceiling would name the owner
// of a value he never set (H1).
//
// A layer that writes the shipped figure itself is named here too: the value
// is the default either way, and the two are the same number to every reader.
// stage_tokens and stage_timeout are never named — they have no shipped
// default, so a key of theirs is either unset (and has no source at all) or a
// layer's own.
func (g Governance) DefaultGovernanceKeys() map[string]bool {
	d := governanceHook().Defaults()
	out := map[string]bool{}
	mark := func(key string, isDefault bool) {
		if isDefault {
			out[key] = true
		}
	}
	mark(KeyReviewRatio, g.ReviewRatio == d.ReviewRatio)
	mark(KeyReviewFloorCalls, g.ReviewFloorCalls == d.ReviewFloorCalls)
	mark(KeyMaxSubagents, g.MaxSubagents == d.MaxSubagents)
	mark(KeyStageBudgetImplement, g.StageBudget.Implement == d.StageBudget.Implement)
	mark(KeyStageBudgetReview, g.StageBudget.Review == d.StageBudget.Review)
	mark(KeyStageBudgetFix, g.StageBudget.Fix == d.StageBudget.Fix)
	for _, t := range tier.Tiers {
		cur, def := g.Tiers.For(t), d.Tiers.For(t)
		mark(TierKey(t, KeyExpectedUSD), cur.ExpectedUSD == def.ExpectedUSD)
		mark(TierKey(t, KeyExpectedMinutes), cur.ExpectedMinutes == def.ExpectedMinutes)
		mark(TierKey(t, KeyMaxSubagents), cur.MaxSubagents == def.MaxSubagents)
	}
	return out
}

// Level is one review level's name as the owner's floor spells it (D722):
// l1, l2 or l3, the levels of docs/review-levels-ab-jobmax/spec.md.
type Level string

// The three levels, shallowest first. The names are the ones a configuration
// and the policy node write; nothing else may name a level.
const (
	LevelL1 Level = "l1"
	LevelL2 Level = "l2"
	LevelL3 Level = "l3"
)

// Levels is the whole vocabulary. It is closed: a name outside it is refused
// where it is written (the owner's project config, the policy node), never
// matched as an unknown level at resolution time.
var Levels = []Level{LevelL1, LevelL2, LevelL3}

// KnownLevel reports whether l is one of Levels. The zero Level is not.
func KnownLevel(l Level) bool { return slices.Contains(Levels, l) }

// ParseLevel is the level a written value names; ok is false for a name
// outside the vocabulary. It is the only way a written value becomes a Level,
// so a near miss ("L2", "l3 ") is a load error rather than a level that
// silently never binds.
func ParseLevel(s string) (Level, bool) {
	l := Level(s)
	return l, KnownLevel(l)
}

// ParseFloor is a written floor — mode name to level names, the way the owner's
// project config and the policy node both spell it — in this package's
// vocabulary, or the errors that say why it is not one.
//
// It is the one rule for the key, and it lives beside the vocabulary it
// enforces: internal/localcfg holds the config file to it and
// internal/projectpolicy holds the node to it, so the file the owner writes
// and the node the runner reads cannot disagree about what a written floor
// means. An unknown mode, an unknown level, a level listed twice and a mode
// with no level in it are all errors rather than values dropped: the floor is
// a minimum, so a level lost on the way in is a run reviewing less than the
// owner asked for — the one direction this key must never move.
//
// A floor that names no mode at all is no floor (nil), whether the file leaves
// the key out or writes an empty object: the empty object is how an operator
// says the node's floor goes away on purpose, and it is what leaves the
// published node without the member.
func ParseFloor(written map[string][]string) (map[rigor.Mode][]Level, error) {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("review_floor."+f, a...)) }
	out := make(map[rigor.Mode][]Level, len(written))
	for _, mode := range slices.Sorted(maps.Keys(written)) {
		m, ok := rigor.Parse(mode)
		if !ok {
			bad("%s: %q is not a rigor mode (%s)", mode, mode, vocabulary(rigor.Modes))
			continue
		}
		names := written[mode]
		if len(names) == 0 {
			bad("%s: a floor with no level in it says nothing; remove the mode to leave it unfloored, or name the levels that must run", mode)
			continue
		}
		levels, seen := make([]Level, 0, len(names)), map[Level]bool{}
		for _, name := range names {
			l, ok := ParseLevel(name)
			switch {
			case !ok:
				bad("%s: %q is not a review level (%s)", mode, name, vocabulary(Levels))
				continue
			case seen[l]:
				bad("%s: %q is listed twice", mode, name)
				continue
			}
			seen[l] = true
			levels = append(levels, l)
		}
		if len(levels) > 0 {
			out[m] = levels
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// vocabulary is a closed vocabulary as an error prints it, read off the list
// itself rather than written out again: the floor's refusals name the whole
// set (rigor.Modes, Levels), so a name this package refuses is a name the
// operator can see is not in it, and a vocabulary that grows cannot leave the
// sentence behind.
func vocabulary[T ~string](names []T) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, string(n))
	}
	return strings.Join(out, ", ")
}

// Layer is one source of policy. The zero value of every field means "unset".
type Layer struct {
	Mode      string  // "" = unset
	PerRunUSD float64 // 0 = unset
	// PerDayUSD is the day cap of the repository, enforced by the runner
	// (the database enforces the owner's own cap); 0 = unset.
	PerDayUSD    float64
	MaxRunTokens int64 // 0 = unset
	// Per-call output limits by role; 0 = unset, smaller wins.
	MaxOutputCoder    int64
	MaxOutputReviewer int64
	AllowedModels     []string // nil = unset
	// ABRate is this layer's ab.rate, nil = the layer writes none. It is a
	// pointer because 0 is a value here and not "unset": turning sampling off
	// is the whole of what a repository's own ab: block may say (D556), and
	// the merge has to be able to tell that layer from one that says nothing
	// (see Merge).
	//
	// Nothing builds a layer with it yet — the builders are the runner's and
	// the CLI's, and the launcher that would ask (plan A3.3) is what reads the
	// resolved layers, so until it ships Effective.ABRate is nil for every run
	// and a sampler wired to it would read "no rate here" as "no rate
	// anywhere".
	ABRate *float64
	// Governance is this layer's governance limits; the zero value sets
	// none, which is what leaves the ceiling's own alone (see Governance).
	Governance Governance
	// ReviewFloor is this layer's floor: per rigor mode, the review levels a
	// run of that mode must run whatever a lower layer asks for (D722, G1).
	// nil, or a mode missing from it, is "this layer sets no floor".
	//
	// The owner's floor is the only one there is, and it reaches a run the way
	// the rest of his policy does: read from the owner-only policy node and
	// put on the ceiling layer (internal/runner's policyCeiling). It is not a
	// key of fugaro.yaml, of the project layer or of the task, because all
	// three are written by the launcher (docs/design/bucket-iam.md §10 L1, L2)
	// — a floor there would be the launcher deciding how much review it gets.
	// It is merged as a union (see Merge), so a lower layer that writes one
	// can add a level and never remove one; the merge is what makes that
	// direction a property of this package rather than of its callers.
	ReviewFloor map[rigor.Mode][]Level
}

// ReviewFloor is the levels the owner requires a run of mode to run, in the
// vocabulary's order (Levels), and nil when no layer set a floor for it. It
// is how a consumer asks — L3.1's resolver is the first — so that the merged
// floor is read through one lookup rather than by ranging the map.
//
// The method shadows Layer.ReviewFloor, the field the merge fills: the whole
// map is e.Layer.ReviewFloor, and a mode's levels are asked for here.
func (e Effective) ReviewFloor(mode rigor.Mode) []Level { return e.Layer.ReviewFloor[mode] }

// Effective is the merged policy plus where each key came from.
type Effective struct {
	Layer
	Sources map[string]string // key -> "ceiling" | "policy" | "default-branch" | "branch"; only keys that are set
	Ignored []Ignored         // looser values dropped
}

// Ignored records a value a later layer set that did not tighten the running
// effective value. Value and Effective are rendered as text; Source is the
// layer the effective value came from.
type Ignored struct{ Key, Value, Effective, Source string }

// ValidMode reports whether m is one of "off", "observe", "enforce". Merge
// treats any other string (including "") as unset.
func ValidMode(m string) bool { return modeRank(m) > 0 }

// HasAllowList reports whether any layer set an allow-list. When it is true
// and AllowedModels is empty, every model is denied.
func (e Effective) HasAllowList() bool { return e.AllowedModels != nil }

// Allows reports whether model may be used under the effective allow-list.
func (e Effective) Allows(model string) bool {
	if !e.HasAllowList() {
		return true
	}
	for _, m := range e.AllowedModels {
		if m == model {
			return true
		}
	}
	return false
}
