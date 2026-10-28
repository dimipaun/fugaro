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
// the defaults enter. Fugaro ships them (DefaultGovernance) and they are the
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

import "time"

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
)

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

// DefaultGovernance is the governance every run starts under.
func DefaultGovernance() Governance {
	return Governance{
		ReviewRatio:      DefaultReviewRatio,
		ReviewFloorCalls: DefaultReviewFloorCalls,
		MaxSubagents:     DefaultMaxSubagents,
		StageBudget: StageBudget{
			Implement: DefaultStageBudgetImplement,
			Review:    DefaultStageBudgetReview,
			Fix:       DefaultStageBudgetFix,
		},
	}
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
// Fugaro's, not the operator's, and a later task that has to name the owner
// of a governance value in a refusal or a record says so the way
// policySources does for the published node (H1).
func (g Governance) WithDefaults() Governance {
	d := DefaultGovernance()
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
	// stage_tokens and stage_timeout have no default: a stage with no token
	// ceiling and no timeout of its own is bounded by the run's own.
	return g
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
	// Governance is this layer's governance limits; the zero value sets
	// none, which is what leaves the ceiling's own alone (see Governance).
	Governance Governance
}

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
