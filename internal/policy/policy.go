// Package policy merges budget and model policy layers so that only
// tightening wins. It is pure: no I/O, no clock, no globals.
//
// Merge is total: it never fails and never panics. Values that carry no
// meaning are treated as unset: a cap that is not a finite positive number
// (zero, negative, NaN, +Inf) and a mode that is not one of the three known
// strings. Callers that must reject such input (validation) use ValidMode and
// their own range checks before merging.
//
// Allow-lists: a nil AllowedModels means no list was set anywhere, so every
// model is allowed. A non-nil list is a restriction, and a disjoint
// intersection is an EMPTY NON-NIL list, which means deny everything (it is
// not "unset"). Consumers must use Effective.HasAllowList and
// Effective.Allows and never test len(AllowedModels) == 0.
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
