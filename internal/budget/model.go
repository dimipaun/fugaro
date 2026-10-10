// Package budget is the data model of the shared budget database (a Firebase
// Realtime Database, design m9-budget-and-dashboard §6.2) and Evaluate, the
// pure predicate that says whether a lease or a release would pass the
// database rules.
//
// Money is integer micro-dollars (pricing.Micros) everywhere; a cap that is
// not set is nil, never zero, because the rules read a missing node as null
// and null compares false (they fail closed).
//
// The rules generator (internal/budget/rules) and the runner's lease client
// share Evaluate as their single semantic model; this file is the single
// source for node paths.
package budget

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// Micros is the unit of every amount in the database.
type Micros = pricing.Micros

// Node paths, relative to the database root, without a leading slash and
// already key-escaped. Slugs and run ids are escaped here; callers pass the
// raw values.
const (
	PathCapsGlobal   = "config/caps/global"
	PathCapsDefaults = "config/caps/defaults"
	PathLimits       = "config/limits"
	PathMode         = "config/mode"
	PathKillGlobal   = "config/kill/global"
	PathMark         = "fugaro/mark"
	PathProject      = "fugaro/project"
)

// seg escapes one slug, run id or model name for a path and panics on an
// empty one: an empty segment would collapse the path onto a different node
// (runs//r1 is runs/r1 after URL cleaning). Slugs and run ids are validated
// long before a path is built, so an empty one is a programming error.
func seg(what, v string) string {
	if v == "" {
		panic("budget: empty " + what + " in a database path")
	}
	return Key(v)
}

func PathCapsRepo(slug string) string { return "config/caps/repos/" + seg("slug", slug) }
func PathKillRepo(slug string) string { return "config/kill/repos/" + seg("slug", slug) }

// PathRun is a run's lifetime ledger.
func PathRun(slug, run string) string { return "runs/" + seg("slug", slug) + "/" + seg("run id", run) }

func PathSpendGlobal(day int64) string { return "spend/" + DayKey(day) + "/global" }
func PathSpendRepo(day int64, slug string) string {
	return "spend/" + DayKey(day) + "/repos/" + seg("slug", slug)
}

// PathByModel is one model's usage under a repository's day counter. Model
// names are keys too (claude-3.5, gemini-2.5-pro contain dots): see ModelKey.
func PathByModel(day int64, slug, model string) string {
	return PathSpendRepo(day, slug) + "/byModel/" + seg("model", model)
}

// PathSpendRun is the run's share of one day.
func PathSpendRun(day int64, slug, run string) string {
	return "spend/" + DayKey(day) + "/runs/" + seg("slug", slug) + "/" + seg("run id", run)
}
func PathSpendMeta(day int64) string { return "spend/" + DayKey(day) + "/meta" }

// PathAgent is a run's registry entry.
func PathAgent(slug, run string) string {
	return "agents/" + seg("slug", slug) + "/" + seg("run id", run)
}

// PathOutcome is the run's outcome, written once at its end.
func PathOutcome(day int64, slug, run string) string {
	return "outcomes/" + DayKey(day) + "/" + seg("slug", slug) + "/" + seg("run id", run)
}

// ModelKey is the key a model name has under Counters.ByModel on the wire.
// Write byModel entries only under ModelKey(name) (or PathByModel).
func ModelKey(model string) string { return seg("model", model) }

// Mode values of PathMode.
const (
	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

// Key escapes one path segment for RTDB. RTDB forbids . $ # [ ] / and ASCII
// control characters in keys; each becomes %XX (uppercase hex), and % itself
// is escaped so that Unkey inverts Key and no two inputs collide.
func Key(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c == '.' || c == '$' || c == '#' || c == '[' || c == ']' || c == '/' || c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Unkey inverts Key.
func Unkey(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 > len(s) {
			return "", fmt.Errorf("budget: key %q ends in a truncated escape", s)
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return "", fmt.Errorf("budget: key %q has a bad escape %q", s, s[i:i+3])
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), nil
}

// Day is the UTC epoch day number of now: the number of whole days since
// 1970-01-01 UTC, the same value as the rules' TODAY. The day is UTC
// (decision D5).
func Day(now time.Time) int64 {
	ms := now.UnixMilli()
	d := ms / 86_400_000
	if ms%86_400_000 < 0 {
		d--
	}
	return d
}

// DayKey is the day's node key under /spend and /outcomes.
func DayKey(day int64) string { return strconv.FormatInt(day, 10) }

// DayDate is the day as "YYYY-MM-DD" (the /spend/<day>/meta date).
func DayDate(day int64) string { return time.UnixMilli(day * 86_400_000).UTC().Format("2006-01-02") }

// GlobalCaps is /config/caps/global. A nil field is an unset cap.
type GlobalCaps struct {
	DailyMicros  *Micros `json:"dailyMicros,omitempty"`
	PerRunMicros *Micros `json:"perRunMicros,omitempty"`
}

// DefaultCaps is /config/caps/defaults, the caps of a repository with no node
// of its own.
type DefaultCaps struct {
	RepoDailyMicros  *Micros `json:"repoDailyMicros,omitempty"`
	RepoPerRunMicros *Micros `json:"repoPerRunMicros,omitempty"`
}

// RepoCaps is /config/caps/repos/<slug>.
type RepoCaps struct {
	DailyMicros  *Micros `json:"dailyMicros,omitempty"`
	PerRunMicros *Micros `json:"perRunMicros,omitempty"`
	Repo         string  `json:"repo,omitempty"`
}

// Limits is /config/limits.
type Limits struct {
	MaxReserveMicros *Micros `json:"maxReserveMicros,omitempty"`
}

// Caps is everything under /config a run reads for its repository: a nil
// pointer is an absent node. Mode is /config/mode ("observe" or "enforce";
// anything else, absent included, behaves as observe for cap comparisons
// only, never for kill switches or maxReserve).
type Caps struct {
	Global   *GlobalCaps
	Defaults *DefaultCaps
	Repo     *RepoCaps
	Limits   *Limits
	Mode     string
}

// Enforcing reports whether the cap comparisons apply.
func (c Caps) Enforcing() bool { return c.Mode == ModeEnforce }

// RepoDaily is the repository's day cap: its own node, else the default.
// ok is false when neither is set.
func (c Caps) RepoDaily() (Micros, bool) {
	if c.Repo != nil && c.Repo.DailyMicros != nil {
		return *c.Repo.DailyMicros, true
	}
	if c.Defaults != nil && c.Defaults.RepoDailyMicros != nil {
		return *c.Defaults.RepoDailyMicros, true
	}
	return 0, false
}

// GlobalDaily is the project's day cap.
func (c Caps) GlobalDaily() (Micros, bool) {
	if c.Global != nil && c.Global.DailyMicros != nil {
		return *c.Global.DailyMicros, true
	}
	return 0, false
}

// PerRun is the lifetime cap of a run: min(the repository's own per-run cap
// or the default, the global per-run cap). ok is false when either side is
// unset, because the rules deny when either comparison reads null.
func (c Caps) PerRun() (Micros, bool) {
	var repo Micros
	switch {
	case c.Repo != nil && c.Repo.PerRunMicros != nil:
		repo = *c.Repo.PerRunMicros
	case c.Defaults != nil && c.Defaults.RepoPerRunMicros != nil:
		repo = *c.Defaults.RepoPerRunMicros
	default:
		return 0, false
	}
	if c.Global == nil || c.Global.PerRunMicros == nil {
		return 0, false
	}
	return min(repo, *c.Global.PerRunMicros), true
}

// MaxReserve is the largest single lease.
func (c Caps) MaxReserve() (Micros, bool) {
	if c.Limits != nil && c.Limits.MaxReserveMicros != nil {
		return *c.Limits.MaxReserveMicros, true
	}
	return 0, false
}

// Kill is /config/kill/global or /config/kill/repos/<slug>. At is epoch
// milliseconds.
type Kill struct {
	On     bool   `json:"on"`
	By     string `json:"by,omitempty"`
	At     int64  `json:"at,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Kills are the two switches a run honours; nil is an absent node, which is
// off.
type Kills struct{ Global, Repo *Kill }

// RunLedger (see the note on Counters: never write it whole) is /runs/<slug>/<run> (the lifetime ledger) and, with only
// Reserved, Released and Spent used, /spend/<day>/runs/<slug>/<run> (the
// run's share of one day).
type RunLedger struct {
	Reserved Micros `json:"reserved,omitempty"`
	Released Micros `json:"released,omitempty"`
	Spent    Micros `json:"spent,omitempty"`
	Notional Micros `json:"notional,omitempty"`
	Overrun  Micros `json:"overrun,omitempty"`
	Tokens   int64  `json:"tokens,omitempty"`
	Exp      int64  `json:"exp,omitempty"`
	Crashed  bool   `json:"crashed,omitempty"`
}

// Outstanding is what the run still counts against the shared counters:
// reserved less released.
func (l RunLedger) Outstanding() Micros { return l.Reserved - l.Released }

// Unsettled is what the run holds and has not spent: reserved less released
// less spent; a release may take at most this.
func (l RunLedger) Unsettled() Micros { return l.Reserved - l.Released - l.Spent }

// ModelUse is one model's usage under a repository's day counter.
type ModelUse struct {
	Micros Micros `json:"micros"`
	In     int64  `json:"in"`
	Out    int64  `json:"out"`
	CR     int64  `json:"cr"`
	CW     int64  `json:"cw"`
}

// Counters is /spend/<day>/global or /spend/<day>/repos/<slug>. Counted is
// what is reserved by runs and not released (a ceiling on spend); Spent is
// what was reported. ByModel's keys are ModelKey-escaped on the wire; read
// them through Models. Like RunLedger, never write a Counters whole (zero
// fields are omitted, so a whole-struct write would leave old values in
// place): write single fields by path in a Patch.
type Counters struct {
	Counted  Micros              `json:"counted,omitempty"`
	Spent    Micros              `json:"spent,omitempty"`
	Notional Micros              `json:"notional,omitempty"`
	Calls    int64               `json:"calls,omitempty"`
	ByModel  map[string]ModelUse `json:"byModel,omitempty"`
}

// Models returns ByModel with the model names unescaped.
func (c Counters) Models() map[string]ModelUse {
	out := make(map[string]ModelUse, len(c.ByModel))
	for k, v := range c.ByModel {
		if name, err := Unkey(k); err == nil && name != "" {
			out[name] = v
		}
	}
	return out
}

// AgentEntry is /agents/<slug>/<run>, the live registry; every string is
// untrusted and clipped to 200 characters by the rules.
type AgentEntry struct {
	Repo     string `json:"repo"`
	Workflow string `json:"workflow,omitempty"`
	Title    string `json:"title,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Round    int    `json:"round,omitempty"`
	Verify   string `json:"verify,omitempty"`
	Coder    string `json:"coder,omitempty"`
	Reviewer string `json:"reviewer,omitempty"`
	// Recipe is the run's recipe when it is not default (docs/design/
	// recipes.md §8). Rules deployed before 0.5.0 refuse it; Start then
	// drops it.
	Recipe string `json:"recipe,omitempty"`
	Auth   string `json:"auth,omitempty"`
	PRURL  string `json:"prUrl,omitempty"`
	// Action is the agent's last tool call, redacted (design generic-tool
	// §10.2). Rules deployed before 0.7.0 refuse it and Tokens; the session
	// then drops both.
	Action         string `json:"action,omitempty"`
	Tokens         int64  `json:"tokens,omitempty"`
	StartedAt      int64  `json:"startedAt,omitempty"`
	StageStartedAt int64  `json:"stageStartedAt,omitempty"`
	StageDeadline  int64  `json:"stageDeadline,omitempty"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
	Spent          Micros `json:"spent,omitempty"`
	Halted         string `json:"halted,omitempty"`
	// RequestedBy must equal the token's rb claim (the launcher's identity),
	// or the rules refuse the write.
	RequestedBy string `json:"requestedBy"`
}

// Outcome is /outcomes/<day>/<slug>/<run>, written once.
type Outcome struct {
	Status string `json:"status"`
	// RequestedBy must equal the token's rb claim, as for AgentEntry.
	RequestedBy string `json:"requestedBy"`
}
