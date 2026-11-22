package mailbox

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The consumer's own-state expiry, the sweeper's local delete set and the
// presence verdict (mobile P4.1, Task T7; D118, D168 as amended, D173; M3, M8,
// §4.2, §6, §10).
//
// **This file is the half of `fugaro mailbox sweep` that P4.1 owns, and it
// deletes nothing in the store.** The store-side half — the stored prompts past
// D118's TTL — is P6.13's and lives in `internal/cli/mailbox.go` beside this
// file's caller. What is here is the rule the two halves share (the TTL and its
// exact boundary), the sweeper's **local** delete set (§6's `ledger.json` and
// `pending.json`, and nothing else), the reader that refuses a delete set which
// names anything this command does not own (M8: a Firebase user, a store node),
// and the **presence verdict** the sweeper computes and reports and never
// writes (§4.2: no node and no grant is designated for one).
//
// **The sweeper never answers a conversation.** Expiring a prompt and handing
// the orchestrator its non-destructive answer — or a stop, when it named none —
// is the consumer's, on its own clock (D168 as amended, `ConfirmMemory.
// ExpirePrompts`), which is why this file's local sweep settles nothing: it
// lets go of what a retention window already allows and of nothing else. A
// prompt still to be answered is never dropped, however old, because dropping it
// would lose the safe answer it is owed.

// PromptTTLMs is D118's TTL: 72 hours in milliseconds, the number
// `shared/prompts` exports as PROMPT_TTL_MS (M3, §10). **It is bound, not
// declared** — TestPromptExpiresAt72h holds it to the module's own number and
// writes 259200000 out as the assertion — because the owner's O5 answer is the
// module's and a copy that drifted would expire prompts at a number nobody
// chose.
const PromptTTLMs int64 = 72 * 60 * 60 * 1_000

// PromptRetentionMs is how long this machine's own memory of a spent id, or of
// a prompt it has already settled, lasts after the instant it names: D118's TTL
// **plus one sweep interval**, which is P6.7's bound for the same ledger and
// the caller's number rather than this file's — the sweep interval is the
// owner-run sweeper's cadence, and the sweeper's cadence is the budget
// sweeper's (`budget.SweepEvery`).
func PromptRetentionMs(sweepIntervalMs int64) int64 {
	return PromptTTLMs + sweepIntervalMs
}

// PromptExpired reports whether a prompt whose `at` is `at` has expired at
// `now`: the difference reaching the TTL exactly is an expiry, and one
// millisecond inside it is live. It is the boundary the consumer's own expiry
// draws (`ConfirmMemory.ExpirePrompts` keeps a prompt while `now-at < ttlMs`),
// stated once here so the sweeper cannot draw the line a millisecond away from
// the machine that answers.
func PromptExpired(at, now int64) bool { return now-at >= PromptTTLMs }

// PromptLive is the other side of the same boundary, named so a caller does not
// have to spell it as a negation.
func PromptLive(at, now int64) bool { return now-at < PromptTTLMs }

// ---------------------------------------------------------------------------
// The sweeper's local delete set
// ---------------------------------------------------------------------------

// LocalSweep is what one sweep of this machine's own state let go of: the two
// files it read, the ledger records and prompt entries past the retention
// window, and nothing else.
//
// **It names no store path and no Firebase user**, and that is a property of the
// value rather than a promise: the paths are the two local state files
// `$XDG_STATE_HOME/fugaro/mailbox/<project>/` holds (LedgerPath and
// ConfirmMemoryPath), and the ids are the ids this machine's own files already
// carry. TestSweepDeletesNoUser holds both halves of that, and SweepRefusals is
// what says so about a set that somehow names another.
type LocalSweep struct {
	// Ledger and Prompts are the files this sweep read and, when it had work to
	// do, rewrote. They are the whole of what a sweep of this machine's own
	// state may write.
	Ledger  string
	Prompts string
	// LedgerIDs are the `sender`/`id` pairs the ledger let go of, as the
	// ledger's own entry carries them.
	LedgerIDs []string
	// PromptIDs are the settled prompts and spent ids the memory let go of.
	PromptIDs []string
}

// Empty answers whether this sweep had nothing to let go of, which is the case
// that must write nothing at all: a sweep that rewrote an untouched file would
// be a write the consumer's next pass reads as its own.
func (s LocalSweep) Empty() bool { return len(s.LedgerIDs) == 0 && len(s.PromptIDs) == 0 }

// Paths is every file this sweep is about, which is what the M8 reader below
// reads: `mailbox`'s local state is the whole of a sweeper's remit (§6, M8).
func (s LocalSweep) Paths() []string {
	out := []string{}
	for _, path := range []string{s.Ledger, s.Prompts} {
		if path != "" {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// SweepLocal is the sweeper's whole local remit: it lets go of the ledger
// records and the memory entries a retention window already allows, and of
// nothing else, on the database's own clock (D117).
//
// **It is the same rule the consumer applies to itself, read one more time.**
// The ledger drops a record only when it is **acked** — the delivery is
// finished — and strictly older than its horizon (`Ledger.evict`); the memory
// drops a spent id older than the retention window and a prompt that has been
// **settled** and is older than it, and never a prompt still to be answered
// (`ConfirmMemory.prune`). Both functions are this package's, so this is a
// second caller of them and not a second copy; what the sweep adds is that it
// happens without a delivery to carry it.
//
// The write happens before the caller is told what was dropped, for the reason
// every commit in this package does: a process killed in between reports
// nothing it did not do.
//
// A sweep with nothing to drop writes nothing at all — no file is created, no
// file is rewritten — because the consumer's own state is not this command's to
// touch when there is no work for it.
func SweepLocal(l *Ledger, m *ConfirmMemory, now int64) (LocalSweep, error) {
	return sweepLocal(l, m, now, true)
}

// PlanLocal is the same set **without the write**, which is what a `--dry-run`
// prints: the sweep is asked what the window allows before it is allowed to do
// it, and the answer is the one the sweep itself would act on because it is the
// same call. Nothing is written and no in-memory state is moved, so a caller that
// plans and then sweeps gets the same set both times.
func PlanLocal(l *Ledger, m *ConfirmMemory, now int64) (LocalSweep, error) {
	return sweepLocal(l, m, now, false)
}

func sweepLocal(l *Ledger, m *ConfirmMemory, now int64, write bool) (LocalSweep, error) {
	out := LocalSweep{}
	if l != nil {
		out.Ledger = l.Path()
		seen, through := l.evict(l.state.seen, l.state.evictedThrough, now)
		if len(seen) < len(l.state.seen) {
			for _, entry := range l.state.seen {
				if !containsEntry(seen, entry) {
					out.LedgerIDs = append(out.LedgerIDs, entry.Sender+"/"+entry.ID)
				}
			}
			sort.Strings(out.LedgerIDs)
			state := ledgerState{now: max(now, l.state.now), evictedThrough: through, seen: seen}
			if write {
				if err := l.commit(state); err != nil {
					return LocalSweep{}, err
				}
				l.state = state
			}
		}
	}
	if m != nil {
		out.Prompts = m.Path()
		next := m.prune(m.state, now)
		if len(next.Spent) != len(m.state.Spent) || len(next.Prompts) != len(m.state.Prompts) {
			for _, entry := range m.state.Spent {
				if !containsSpent(next.Spent, entry) {
					out.PromptIDs = append(out.PromptIDs, entry.ID)
				}
			}
			for _, prompt := range m.state.Prompts {
				if !containsPrompt(next.Prompts, prompt) {
					out.PromptIDs = append(out.PromptIDs, prompt.ID)
				}
			}
			sort.Strings(out.PromptIDs)
			next.Now = max(now, m.state.Now)
			if write {
				if err := m.commit(next); err != nil {
					return LocalSweep{}, err
				}
				m.state = next
			}
		}
	}
	return out, nil
}

func containsEntry(entries []LedgerEntry, want LedgerEntry) bool {
	for _, entry := range entries {
		if entry.Sender == want.Sender && entry.ID == want.ID {
			return true
		}
	}
	return false
}

func containsSpent(spent []ConfirmSpent, want ConfirmSpent) bool {
	for _, entry := range spent {
		if entry.Kind == want.Kind && entry.ID == want.ID {
			return true
		}
	}
	return false
}

func containsPrompt(prompts []ConfirmPrompt, want ConfirmPrompt) bool {
	for _, prompt := range prompts {
		if prompt.ID == want.ID && prompt.Mailbox == want.Mailbox {
			return true
		}
	}
	return false
}

// userKeyPrefixes are the Firebase user id prefixes this tree's sweeps are told
// apart by: a paired device's `m~…` (P6.6's rule, in `internal/budget/sweep.go`)
// and a run's `r~…` (`RunUID`), plus a pairing's `p~…`. **None of them is this
// command's**, and the prefix is what says so.
var userKeyPrefixes = []string{"m~", "r~", "p~"}

// SweepRefusals reads a sweep's delete set and answers one finding per key the
// sweeper does not own, in the order given: the keys it may not delete. This is
// M8 as a reader rather than a paragraph.
//
// Three things are refused, and the list is deliberately short:
//
//   - a **Firebase user** (`m~…`, `r~…`, `p~…`), read as a **bare user root**.
//     The `m~` rule is P6.6's and lives in the history sweeper; a mailbox sweep
//     that found itself about to delete one must **refuse and report** it, never
//     perform it (§6's last row, M8). The bare root is the whole of it: this
//     sweep's own ledger ids are spelled `<sender>/<id>` and every sender in a
//     mailbox ledger is the paired device's `m~…` uid, so a prefix test that
//     ignored the separator would put this command's own delete set in front of
//     its own fence and refuse every sweep that had a ledger row to let go of.
//     That is the fail-closed direction, and it is still a sweep that never runs.
//   - a **store node**, recognised by the store's own namespace: a path whose
//     first segment is `mailbox` and that is not one of the two local state
//     files this sweep owns. The owner-run sweeper's local half deletes this
//     machine's own state and nothing under `mailbox/$mb/`; the stored-prompt
//     deletion is P6.13's and is performed by that half of the command, not by
//     this one.
//   - the empty key, which is not a key at all and would build a path out of
//     nothing.
//
// A local state **file** is not a store path even though its directory is named
// `mailbox`: the two are told apart by their shape and not by a substring, so
// `/home/u/.local/state/fugaro/mailbox/aurora/ledger.json` (absolute, under the
// operator's own state directory) is this sweep's and `mailbox/aurora/outbox/x`
// (relative, under the store's namespace) is not.
func SweepRefusals(keys []string) []string {
	out := []string{}
	for _, key := range keys {
		switch {
		case key == "":
			out = append(out, "an empty key is not a delete target")
		case hasUserKeyPrefix(key):
			out = append(out, fmt.Sprintf("%s is a Firebase user, and a mailbox sweep deletes no user: the m~ rule is the history sweeper's (P6.6)", key))
		case isStorePath(key) && !isLocalStateFile(key):
			out = append(out, fmt.Sprintf("%s is a node under the store's mailbox subtree, which this sweep does not own: the stored-prompt deletion is P6.13's half of the command", key))
		}
	}
	return out
}

// hasUserKeyPrefix reports whether a key is a **bare** Firebase user id: one of
// the three prefixes and no separator anywhere in it. The separator is what
// makes the difference, and it is a fact about this tree rather than a
// convenience: a user id is a node's root (`m~…`), while a ledger id this sweep
// builds is `<sender>/<id>` (`m~…/m-0001`) — and the sender of a mailbox ledger
// row is always the paired device's `m~…` uid.
func hasUserKeyPrefix(key string) bool {
	if strings.Contains(key, "/") {
		return false
	}
	for _, prefix := range userKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// isStorePath is the store's own namespace: `mailbox` alone, or `mailbox/…`.
// The local state's directory is named `mailbox` too, which is why the two
// readings are told apart with isLocalStateFile rather than by this test alone.
func isStorePath(key string) bool {
	return key == "mailbox" || strings.HasPrefix(key, "mailbox/")
}

// isLocalStateFile is a path under the operator's own state directory, which is
// absolute by construction (localcfg.UserDir): a relative `mailbox/…` is the
// store's, an absolute one under `fugaro/mailbox/<project>/` is this machine's.
func isLocalStateFile(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	dir, file := splitPath(path)
	if file != "ledger.json" && file != "pending.json" {
		return false
	}
	return strings.HasSuffix(dir, "/fugaro/mailbox/"+dirName(dir))
}

// splitPath cuts a path into its directory and its last element without
// importing path/filepath's own answer, which would accept a Windows separator
// a store key may carry as a character.
func splitPath(path string) (string, string) {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i], path[i+1:]
	}
	return "", path
}

func dirName(dir string) string {
	_, name := splitPath(dir)
	return name
}

// ---------------------------------------------------------------------------
// The presence verdict (shared/presence, ported)
// ---------------------------------------------------------------------------

// **This is `shared/presence` in Go, and it is a port rather than an import for
// the same reason `action.go` is one.** The module's rule is a pure function
// over a heartbeat, and the sweeper reports its verdict. Go cannot import it, so
// the rule is restated here and held to the module's own file by
// TestPresenceConstantsMatchTheSharedModule, which reads `shared/presence/
// index.js` — the numbers, the four states, the five reasons and the refusal
// codes as sets.
//
// **Two differences from the module, both stated rather than hidden.** The
// module reads a plain JavaScript object and refuses one that is not
// (`input-not-an-object`) or that carries a key it does not name
// (`unknown-field`); here the input is a Go struct a caller built, so neither
// refusal can arise and neither is in PresenceCodes — the same thinning
// `action.go`'s header records for the same reason. And the module's heartbeat
// carries `bootId`, where this tree's wire calls the same value `boot`
// (`shared/mailbox-schema`'s HEARTBEAT_FIELDS, M1's `{at, boot, mac}`), so a
// caller maps the two names when it reads the store; PresenceHeartbeat is the
// module's own vocabulary.
//
// One honest limit the module states and this port keeps: a skew in the **past**
// is not detectable from one heartbeat, so the caller must hand `Now` a clock
// anchored to the server (D117).

// PresenceState is the four states the rule answers with. Nothing else is a
// state: every other outcome is a refusal.
type PresenceState string

const (
	PresenceLive         PresenceState = "live"
	PresenceStale        PresenceState = "stale"
	PresenceOffline      PresenceState = "offline"
	PresenceClockDiffers PresenceState = "clock-differs"
)

// The reasons. A reason is not a fifth state.
const (
	PresenceReasonFresh           = "fresh"
	PresenceReasonMissedIntervals = "missed-intervals"
	PresenceReasonLongSilence     = "long-silence"
	PresenceReasonFutureSkew      = "future-skew"
	PresenceReasonNoHeartbeat     = "no-heartbeat"
)

// The module's bounds, restated because Go cannot import it. The interval is
// provisional until check 84 measures the write cost (the module says so), and
// the floor exists because RTDB bills per write.
const (
	PresenceDefaultIntervalMs        int64 = 60_000
	PresenceMinIntervalMs            int64 = 30_000
	PresenceMaxIntervalMs            int64 = 24 * 60 * 60 * 1_000
	PresenceDefaultMissedIntervals         = 3
	PresenceDefaultOfflineIntervals        = 12
	PresenceMaxBand                        = 1_000
	PresenceDefaultFutureToleranceMs int64 = PresenceDefaultIntervalMs
	PresenceMaxBootIDLength                = 64
)

// The presence refusal codes, in the module's own spelling. `unknown-field` is
// this package's CodeUnknownField: the module's word and the schema's are the
// same word, and one constant for one code is what the two tests that compare
// the sets against the modules expect.
const (
	// CodePresenceNotAnObject is the module's refusal for an input that is not
	// a plain object. It is declared and never answered here (see this file's
	// header), and it is in PresenceCodes because the set is compared with the
	// module's table.
	CodePresenceNotAnObject Code = "input-not-an-object"
	// CodePresenceInvalidHeartbeat, CodePresenceHeartbeatMissingField and
	// CodePresenceHeartbeatUnknownField: a heartbeat that is not a plain
	// object, one missing `at` or `bootId`, and one carrying a key the module
	// does not name.
	CodePresenceInvalidHeartbeat       Code = "invalid-heartbeat"
	CodePresenceHeartbeatMissingField  Code = "heartbeat-missing-field"
	CodePresenceHeartbeatUnknownField  Code = "heartbeat-unknown-field"
	CodePresenceInvalidHeartbeatTime   Code = "invalid-heartbeat-timestamp"
	CodePresenceInvalidBootID          Code = "invalid-boot-id"
	CodePresenceInvalidNow             Code = "invalid-now"
	CodePresenceInvalidInterval        Code = "invalid-interval"
	CodePresenceIntervalBelowFloor     Code = "interval-below-floor"
	CodePresenceIntervalAboveCeiling   Code = "interval-above-ceiling"
	CodePresenceInvalidBand            Code = "invalid-band"
	CodePresenceBandsNotIncreasing     Code = "bands-not-increasing"
	CodePresenceInvalidFutureTolerance Code = "invalid-future-tolerance"
)

// PresenceCodes is the module's whole refusal vocabulary, so a test can hold it
// to `shared/presence`'s REFUSAL table by set. `input-not-an-object` and
// `unknown-field` are in it and are never answered by this port, because a Go
// struct is not a bag (this file's header); leaving them out would make the set
// comparison the test is for impossible to read.
var PresenceCodes = []Code{
	CodePresenceNotAnObject, CodeUnknownField,
	CodePresenceInvalidHeartbeat, CodePresenceHeartbeatMissingField,
	CodePresenceHeartbeatUnknownField, CodePresenceInvalidHeartbeatTime,
	CodePresenceInvalidBootID, CodePresenceInvalidNow,
	CodePresenceInvalidInterval, CodePresenceIntervalBelowFloor,
	CodePresenceIntervalAboveCeiling, CodePresenceInvalidBand,
	CodePresenceBandsNotIncreasing, CodePresenceInvalidFutureTolerance,
}

// presenceBootIDPattern is the module's BOOT_ID_PATTERN. The charset is narrow
// because the boot id reaches a screen.
var presenceBootIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// PresenceHeartbeat is D117's node as `shared/presence` reads it: the instant
// the writer stamped and the boot id beside it. The module validates the boot id
// and hands it back **without letting it decide anything** — the state is a
// function of the timestamp and the bands alone.
type PresenceHeartbeat struct {
	At     int64
	BootID string
}

// PresenceOptions are the module's input. A zero field means the module's
// default for each, which is what a caller with no reason to move one wants.
// Heartbeat is nil when the account has no heartbeat at all, which is `offline`
// and is never `live`.
type PresenceOptions struct {
	Heartbeat         *PresenceHeartbeat
	Now               int64
	IntervalMs        int64
	MissedIntervals   int
	OfflineIntervals  int
	FutureToleranceMs int64
}

// PresenceAnswer is one verdict: a state with the readings it was decided from,
// or a refusal with no state at all. A refusal is never coerced into a state.
type PresenceAnswer struct {
	OK     bool
	State  PresenceState
	Reason string
	AgeMs  int64
	// HasAge is false where the module answers `ageMs: null` (a missing
	// heartbeat), because Go's zero is a reading and this is the absence of one.
	HasAge         bool
	BootID         string
	HeartbeatAt    int64
	IntervalMs     int64
	StaleAfterMs   int64
	OfflineAfterMs int64
	Code           Code
	Field          string
}

func presenceRefuse(code Code, field string) PresenceAnswer {
	return PresenceAnswer{Code: code, Field: field}
}

// EvaluatePresence is the module's rule, branch for branch.
//
// The order of the three branches at the end is the rule and not an
// implementation detail: the future check comes first so that a skewed clock can
// never be read as `live` (a negative age) and never as `offline` (an age that
// "makes no sense"), and the bands are inclusive on the stale and offline side,
// so "at N intervals" is stale rather than a boundary nobody can name.
func EvaluatePresence(o PresenceOptions) PresenceAnswer {
	if o.Now < 1 || o.Now > TimeMax {
		return presenceRefuse(CodePresenceInvalidNow, "now")
	}
	interval := o.IntervalMs
	if interval == 0 {
		interval = PresenceDefaultIntervalMs
	}
	if interval < 1 || interval > TimeMax {
		return presenceRefuse(CodePresenceInvalidInterval, "intervalMs")
	}
	if interval < PresenceMinIntervalMs {
		return presenceRefuse(CodePresenceIntervalBelowFloor, "intervalMs")
	}
	if interval > PresenceMaxIntervalMs {
		return presenceRefuse(CodePresenceIntervalAboveCeiling, "intervalMs")
	}
	missed := o.MissedIntervals
	if missed == 0 {
		missed = PresenceDefaultMissedIntervals
	}
	offline := o.OfflineIntervals
	if offline == 0 {
		offline = PresenceDefaultOfflineIntervals
	}
	for _, band := range []struct {
		field string
		value int
	}{{"missedIntervals", missed}, {"offlineIntervals", offline}} {
		if band.value < 1 || band.value > PresenceMaxBand {
			return presenceRefuse(CodePresenceInvalidBand, band.field)
		}
	}
	if offline <= missed {
		// Two bands that do not nest leave a heartbeat with two answers and no
		// stated tie-break, and an ambiguity is a refusal.
		return presenceRefuse(CodePresenceBandsNotIncreasing, "offlineIntervals")
	}
	tolerance := o.FutureToleranceMs
	if tolerance == 0 {
		tolerance = PresenceDefaultFutureToleranceMs
	}
	if tolerance < 0 {
		return presenceRefuse(CodePresenceInvalidFutureTolerance, "futureToleranceMs")
	}
	staleAfter, offlineAfter := interval*int64(missed), interval*int64(offline)
	if o.Heartbeat == nil {
		// No heartbeat is offline, and is NEVER live: an account that has never
		// been written to has no orchestrator to show. It is not a refusal
		// either — a missing node is a fact about the account, where garbage is
		// a bug.
		return PresenceAnswer{
			OK: true, State: PresenceOffline, Reason: PresenceReasonNoHeartbeat,
			IntervalMs: interval, StaleAfterMs: staleAfter, OfflineAfterMs: offlineAfter,
		}
	}
	hb := *o.Heartbeat
	if hb.BootID == "" {
		// The module answers `heartbeat-missing-field` for an object without
		// `bootId`; in Go an absent string and an empty one are the same zero
		// value, which is the one place this port's answer differs (this file's
		// header).
		return presenceRefuse(CodePresenceHeartbeatMissingField, "heartbeat")
	}
	if hb.At < 1 || hb.At > TimeMax {
		return presenceRefuse(CodePresenceInvalidHeartbeatTime, "heartbeat.at")
	}
	if len(hb.BootID) > PresenceMaxBootIDLength || !presenceBootIDPattern.MatchString(hb.BootID) {
		return presenceRefuse(CodePresenceInvalidBootID, "heartbeat.bootId")
	}
	age := o.Now - hb.At
	state, reason := PresenceLive, PresenceReasonFresh
	switch {
	case age < -tolerance:
		state, reason = PresenceClockDiffers, PresenceReasonFutureSkew
	case age >= offlineAfter:
		state, reason = PresenceOffline, PresenceReasonLongSilence
	case age >= staleAfter:
		state, reason = PresenceStale, PresenceReasonMissedIntervals
	}
	return PresenceAnswer{
		OK: true, State: state, Reason: reason, AgeMs: age, HasAge: true,
		BootID: hb.BootID, HeartbeatAt: hb.At, IntervalMs: interval,
		StaleAfterMs: staleAfter, OfflineAfterMs: offlineAfter,
	}
}
