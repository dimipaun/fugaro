// Package policy merges budget and model policy layers so that only
// tightening wins. It is pure: no I/O, no clock, no globals.
package policy

// Policy keys, as used in Effective.Sources and Ignored.Key.
const (
	KeyPerRunUSD     = "per_run_usd"
	KeyMode          = "mode"
	KeyMaxRunTokens  = "max_run_tokens"
	KeyAllowedModels = "allowed_models"
)

// Layer names, as used in Effective.Sources and Ignored.Source.
const (
	SourceCeiling       = "ceiling"
	SourceDefaultBranch = "default-branch"
	SourceBranch        = "branch"
)

// Budget modes, loosest to strictest.
const (
	ModeOff     = "off"
	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

// Layer is one source of policy. The zero value of every field means "unset".
type Layer struct {
	Mode          string   // "" = unset
	PerRunUSD     float64  // 0 = unset
	MaxRunTokens  int64    // 0 = unset
	AllowedModels []string // nil = unset
}

// Effective is the merged policy plus where each key came from.
type Effective struct {
	Layer
	Sources map[string]string // key -> "ceiling" | "default-branch" | "branch"; only keys that are set
	Ignored []Ignored         // looser values dropped
}

// Ignored records a value a later layer set that did not tighten the running
// effective value. Value and Effective are rendered as text; Source is the
// layer the effective value came from.
type Ignored struct{ Key, Value, Effective, Source string }
