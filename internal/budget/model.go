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

// The by-tier split's keys: the tiers that are paid for, as the rules'
// `$tier` spells them (C1.3, `byTier/$tier/spent` with `$tier == 'b' ||
// $tier == 'c'`). The credentials service's own answer spells them "B" and
// "C" (the gateway's TierB, TierC, C1.15) and the gateway's stage report
// keeps that spelling; tierKey is the one place that maps one to the other.
//
// Tier A has no leaf on purpose: a subscription call is accounted as
// notional on the nodes that already exist (C1.16, D140), so only money --
// tiers B and C -- is split by the tier that paid it.
const (
	TierB = "b"
	TierC = "c"
)

// tierSeg escapes one tier for a path and panics on a tier no byTier leaf
// exists for. The rules' `$tier` validation is what refuses such a key, and a
// write under it would read as a deployed-rules mismatch rather than as the
// programming error it is; a panic here is caught by the session's safely and
// becomes a fail-closed halt, never a crash (session.go).
func tierSeg(what, tier string) string {
	if tier != TierB && tier != TierC {
		panic("budget: " + what + ": " + tier + " has no byTier leaf (only b and c are paid tiers)")
	}
	return seg("tier", tier)
}

// PathRunByTier is one tier's real spend under the run's lifetime ledger
// (D140); the leaf under it is `spent`. Only a cascade run writes it: no
// other run's calls carry a tier.
func PathRunByTier(slug, run, tier string) string {
	return PathRun(slug, run) + "/byTier/" + tierSeg("run", tier)
}

// PathSpendRunByTier is the same split on the run's share of one day.
func PathSpendRunByTier(day int64, slug, run, tier string) string {
	return PathSpendRun(day, slug, run) + "/byTier/" + tierSeg("run share", tier)
}

// PathSpendRepoByTier is the repository's day counters' split, and
// PathSpendGlobalByTier the project's. Any run of the project can write
// those two (they carry the same latitude `spent` does) and nothing may
// decide anything on them: the split is a display quantity (C1.3's
// constraint on this split, stated in the design and in the plan).
func PathSpendRepoByTier(day int64, slug, tier string) string {
	return PathSpendRepo(day, slug) + "/byTier/" + tierSeg("repository day", tier)
}

func PathSpendGlobalByTier(day int64, tier string) string {
	return PathSpendGlobal(day) + "/byTier/" + tierSeg("project day", tier)
}

// TierSpent is one tier's real spend: the only leaf the rules' byTier node
// carries. It is the same quantity `spent` beside it is, split by the tier
// that paid for it (D140).
type TierSpent struct {
	Spent Micros `json:"spent,omitempty"`
}

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

// RepoPerRun is the repository's own per-run cap, else the default for
// repositories without one. ok is false when neither is set.
func (c Caps) RepoPerRun() (Micros, bool) {
	if c.Repo != nil && c.Repo.PerRunMicros != nil {
		return *c.Repo.PerRunMicros, true
	}
	if c.Defaults != nil && c.Defaults.RepoPerRunMicros != nil {
		return *c.Defaults.RepoPerRunMicros, true
	}
	return 0, false
}

// GlobalPerRun is the project's per-run cap, the fallback for every
// repository that has none of its own.
func (c Caps) GlobalPerRun() (Micros, bool) {
	if c.Global != nil && c.Global.PerRunMicros != nil {
		return *c.Global.PerRunMicros, true
	}
	return 0, false
}

// PerRun is the lifetime cap of a run: min(the repository's own per-run cap
// or the default, the global per-run cap). A side that is not set does not
// bound the run, so the global cap is the fallback of a repository that has
// no cap of its own and the default is the fallback of the global one; ok is
// false only when neither side is set anywhere, which the rules deny (a
// missing node reads null and the comparison is false).
func (c Caps) PerRun() (Micros, bool) {
	repo, repoOK := c.RepoPerRun()
	global, globalOK := c.GlobalPerRun()
	switch {
	case repoOK && globalOK:
		return min(repo, global), true
	case repoOK:
		return repo, true
	case globalOK:
		return global, true
	}
	return 0, false
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
	// ByTier is the split of Spent by the tier that paid for it, keyed b and
	// c (D140, C1.16). It is read here so a write of one tier's leaf is the
	// increase the rules validate (increase-only, absolute on the wire),
	// never a write of the whole struct: the same rule as every other field.
	// Only a cascade run has it, and it is never tied to Spent beside it.
	ByTier map[string]TierSpent `json:"byTier,omitempty"`
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
	// ByTier is the split of Spent by the tier that paid for it (D140,
	// C1.16), keyed b and c, read and written one leaf at a time like every
	// other field here. Nothing decides anything on it.
	ByTier map[string]TierSpent `json:"byTier,omitempty"`
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
	// Action is a safe summary of the agent's last tool call, never the raw
	// command (design generic-tool §10.2, the owner's 2026-10 tightening of
	// G24; see agent.Relay.actionSummary). Rules deployed before 0.7.0
	// refuse it and Tokens; the session then drops both.
	Action         string `json:"action,omitempty"`
	Tokens         int64  `json:"tokens,omitempty"`
	StartedAt      int64  `json:"startedAt,omitempty"`
	StageStartedAt int64  `json:"stageStartedAt,omitempty"`
	StageDeadline  int64  `json:"stageDeadline,omitempty"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
	Spent          Micros `json:"spent,omitempty"`
	Halted         string `json:"halted,omitempty"`
	// Credential is the name — never the value — of the credential the run's
	// cascade is currently using (credentials-service plan, task C1.3's rules
	// child of the same name; C1.16 and C1.19 write it). It is what `fugaro
	// watch` and `fugaro runs` show when someone asks which account a run is
	// spending on, and what the plan's D140 asks the record to carry beside
	// Record.OAuthAccount.
	//
	// It is spelled in the plan's snake_case, like the mailbox's reply_to: the
	// node's name is part of the rules (the template validates `credential`),
	// so the Go field follows it rather than the entry's camelCase.
	//
	// Rules deployed before the credentials children refuse it; a `cascade`
	// run then halts with HaltCredsRulesDenied rather than dropping the write
	// (C1.17), because a run whose credential is unrecorded is one nobody can
	// account for.
	//
	// clipEntry clips it and strips it like every other string of the entry (a
	// name past the rules' MaxString, or carrying a control or bidi character,
	// would otherwise have the whole PATCH denied and freeze the run's
	// registry). A reader that displays it still sanitises with
	// internal/safetext: an entry written by another release is not this one's
	// to trust.
	Credential string `json:"credential,omitempty"`
	// WaitingUntil is the instant, in epoch milliseconds, the run expects to
	// resume at while it is waiting for a credential to become usable again
	// (D152's `oauth-only` "waiting, expected resume time"; C1.17 writes it).
	// Zero means nothing is pending and the node is absent.
	WaitingUntil int64 `json:"waiting_until,omitempty"`
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
