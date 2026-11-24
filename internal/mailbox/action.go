package mailbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The action record, its rules, and the consumer's memory of what it has spent
// (mobile plan Task P6.7; D128 as amended 2026-10-17, D162, D169, D171, D173).
//
// **This file is `shared/confirm` in Go, and it is held to the module by its own
// data.** The phone runs `shared/confirm`'s state machine -- a read-back, then a
// separate spoken "Fugaro, confirm" -- and the consumer runs here. The two ends
// must agree about what a confirmation authorises, so the rules are implemented
// a second time, in Go, and `TestConfirmParityWithShared` replays every case in
// `shared/confirm/vectors.json` through both vocabularies: the verb set, the
// resolved targets, the confirmation record, the replay ledger, the phases and
// the read-back text. A change on either side is a failing test rather than two
// ends that disagree about what the owner's own voice authorised.
//
// **What the consumer adds to the module, and what it deliberately does not.**
// The confirmation **timeout** (a minute) is the phone's, because the pending
// state it is measured against is phone-local (functional-spec-reconciliation
// §3.5): the consumer never sees a pending read-back and never judges one. What
// it judges is the **action record** the phone built -- `{id, verb, target,
// head_sha?, confirmation}` -- and it judges it with the same readers the
// machine uses (ConfirmValidateAction): the verb is in the closed set, the
// target resolves to the verb's own kind, the confirmation is present, the
// record is inside the six-hour command window measured from the confirmation's
// own time, and neither that time nor the message's `at` is more than five
// minutes ahead of the consumer's clock. Everything the module refuses *before*
// a record exists -- "conform" is not the phrase, the utterance that issued a
// command cannot confirm it, two read-backs are ambiguous -- is refused on the
// phone, where the utterance was heard.
//
// **A destructive command that reaches here without a valid record is refused,
// and nothing runs.** The record is the only thing that authorises one (D128),
// so ConfirmValidateAction is the consumer's whole allow-list, and the CLI
// refuses at it before any command line is built (M11: the decision is a value
// in code, evaluated before anything is spawned).
//
// **Two places this port is deliberately thinner than the module, and why.**
// The module reads an event's and a target's **field set** (an unknown key is
// `unknown-field`, an absent one is `missing-field`) because in JavaScript the
// caller's own object is the input. Here the input is a Go value a strict
// decoder built: `mailbox.Decode` and the JSON tags refuse an unknown field
// before this file sees it, and an absent string and an empty one are the same
// zero value. So an empty required string answers `missing-field` rather than
// the module's `empty-string` -- both refuse, and which of two refusals a caller
// hears is the only difference. The parity test decodes with the same strictness
// the consumer's own reader uses, so the vectors exercise the reader the
// consumer really has.
//
// **The memory.** `shared/confirm`'s ledger bounds itself by a count and refuses
// when it is full; the consumer's must survive a restart for D118's 72-hour TTL
// plus one sweep interval, so it is a file: the ids it has spent (action,
// confirmation, utterance) and the prompts it is holding. It is **not** the
// dedupe ledger (ledger.go): that one is `shared/mailbox`'s state shape and
// carries deliveries by `(sender, id)`, while this one is `shared/confirm`'s and
// `shared/prompts`' vocabulary. Two modules, two files, one directory.

// The three phases, in the module's own words. A phase is a summary of the
// pending read-backs rather than a counter, and `crowded` exists so that a
// confirm with two read-backs outstanding is refused as ambiguous rather than
// guessed at.
type ConfirmPhase string

const (
	ConfirmPhaseIdle    ConfirmPhase = "idle"
	ConfirmPhasePending ConfirmPhase = "pending"
	ConfirmPhaseCrowded ConfirmPhase = "crowded"
)

// ConfirmOutcome is what a step answers with when it does not refuse.
type ConfirmOutcome string

const (
	ConfirmOutcomeReadBack       ConfirmOutcome = "read-back"
	ConfirmOutcomePassThrough    ConfirmOutcome = "pass-through"
	ConfirmOutcomeConfirmed      ConfirmOutcome = "confirmed"
	ConfirmOutcomeDiscarded      ConfirmOutcome = "discarded"
	ConfirmOutcomeNothingPending ConfirmOutcome = "nothing-pending"
)

// ConfirmEventType is the closed event set. An event of any other type is
// refused with `unknown-event`.
type ConfirmEventType string

const (
	ConfirmEventCommand ConfirmEventType = "command"
	ConfirmEventConfirm ConfirmEventType = "confirm"
	ConfirmEventAbort   ConfirmEventType = "abort"
)

// The verbs. `launch` is here because D162's answer is about this machine:
// "kick off ..." passes through rather than being refused as an unknown verb or
// silently treated as destructive -- and D2786 reads a start in a costly mode
// back, which is why it is also in ConfirmPendingVerbs below.
const (
	ConfirmVerbCancel  = "cancel"
	ConfirmVerbConfirm = "confirm"
	ConfirmVerbMerge   = "merge"
	ConfirmVerbLaunch  = "launch"
)

// ConfirmDestructiveVerbs are the verbs D128 as amended needs a confirmation
// for: cancel a job, approve/merge, and confirming an action.
var ConfirmDestructiveVerbs = []string{ConfirmVerbCancel, ConfirmVerbConfirm, ConfirmVerbMerge}

// ConfirmPassThroughVerbs go straight through: D162's launch, and nothing else.
// D2786 amends that answer for a start in a costly mode, which is read back like
// a cancel, so a launch is the one verb whose read-back depends on the session's
// policy rather than on the verb alone.
var ConfirmPassThroughVerbs = []string{ConfirmVerbLaunch}

// ConfirmPendingVerbs are the verbs a pending read-back may hold: the
// destructive ones, and D2786's costly start, which is read back without being
// destructive.
var ConfirmPendingVerbs = []string{ConfirmVerbCancel, ConfirmVerbConfirm, ConfirmVerbMerge, ConfirmVerbLaunch}

// ConfirmRefusedVerbs are every other destructive verb the ruling refuses
// outright: the kill switch, the budget writes, the deletes. The set is closed
// on purpose -- a verb that is neither destructive nor a launch is
// `unknown-verb` -- so a new destructive verb is never silently unprotected.
var ConfirmRefusedVerbs = []string{
	"budget", "budget-kill", "budget-resume", "budget-set", "delete",
	"delete-account", "destroy", "kill", "kill-switch", "purge", "reset", "revoke",
}

// The kinds of resolved target, and the kinds of id the machine's ledger
// remembers.
const (
	ConfirmTargetRun   = "run"
	ConfirmTargetPR    = "pr"
	ConfirmTargetMerge = "merge"
	// ConfirmTargetMode is what a confirmed start binds (D2786): a launch has no
	// run id yet, so the one thing there is to read back and to bind is the mode.
	ConfirmTargetMode = "mode"

	ConfirmLedgerAction       = "action"
	ConfirmLedgerConfirmation = "confirmation"
	ConfirmLedgerUtterance    = "utterance"
)

// The bounds the module owns, declared here because Go cannot import it. They
// are read back out of `shared/confirm/index.js` by
// TestConfirmConstantsMatchTheSharedModule, so the two ends cannot drift.
const (
	// ConfirmCue is the sentence every read-back ends with. It names the wake
	// word because "confirm" alone is agent text (D164) and confirms nothing.
	ConfirmCue = `Say "Fugaro, confirm" to confirm.`
	// ConfirmDefaultTimeoutMs is the phone's confirmation timeout: a state field
	// the caller may move inside ConfirmMinTimeoutMs..ConfirmMaxTimeoutMs.
	ConfirmDefaultTimeoutMs = 60 * 1_000
	ConfirmMinTimeoutMs     = 1 * 1_000
	ConfirmMaxTimeoutMs     = 5 * 60 * 1_000
	// ConfirmDefaultMaxPending is how many read-backs may be outstanding at
	// once: enough for a correction, not a queue.
	ConfirmDefaultMaxPending = 4
	ConfirmMinMaxPending     = 1
	ConfirmMaxMaxPending     = 16
	// ConfirmDefaultMaxLedger is how many spent ids the machine's ledger
	// remembers. A full ledger refuses (`ledger-full`) rather than forgetting,
	// because forgetting re-opens a replay.
	ConfirmDefaultMaxLedger = 1_024
	ConfirmMinMaxLedger     = 1
	ConfirmMaxMaxLedger     = 100_000
	// ConfirmIDMax and ConfirmTextMax are the permitted lengths of an id and of
	// an utterance, in UTF-16 code units (P6.3's ID_MAX and TEXT_MAX).
	ConfirmIDMax   = IDMax
	ConfirmTextMax = TextMax
	// ConfirmRepoMax and ConfirmRepoSegmentMax bound a repository and each of
	// its two segments; ConfirmPRMax bounds a pull-request number.
	ConfirmRepoMax        = TargetMax
	ConfirmRepoSegmentMax = 100
	ConfirmPRMax          = 10_000_000
	// ConfirmForwardJumpMs is how far one event's `now` may lead the state's.
	ConfirmForwardJumpMs = 366 * 24 * 60 * 60 * 1_000
	// ConfirmWindowMs is the command window: a destructive record is honoured
	// only within six hours of the confirmation's own time (D173).
	ConfirmWindowMs = DefaultWindowMs
	// ConfirmMaxPrompts is the pending-prompt cap (P6.5's DEFAULT_MAX_PENDING),
	// ConfirmQueueFullReply is what P6.5 answers beyond it, and
	// ConfirmExpiredReply is what a prompt with no non-destructive answer hands
	// the orchestrator when it expires (P6.5's EXPIRED_REPLY).
	ConfirmMaxPrompts     = 16
	ConfirmQueueFullReply = "queue full"
	ConfirmExpiredReply   = "no answer, expired"
	// ConfirmMaxUtteranceLen is P6.1's MAX_UTTERANCE_LENGTH.
	ConfirmMaxUtteranceLen = 4_096
	// ConfirmMaxCostlyModes and ConfirmMaxModeLen are C2's own bounds for the
	// session's costly-mode list and for one mode name.
	ConfirmMaxCostlyModes = 32
	ConfirmMaxModeLen     = 64
	// ConfirmDefaultBiometricOnCostly is P6's `mobile.biometric_on_costly`
	// default: off, which is also the zero value a caller leaves alone.
	ConfirmDefaultBiometricOnCostly = false
)

// ConfirmDefaultCostlyModes is P6's `mobile.costly_modes` default, which is the
// session policy D2786 reads when the caller names none. It is held to
// shared/confirm/policy.js by TestConfirmConstantsMatchTheSharedModule, so the
// two ends cannot drift about which modes a project calls costly.
var ConfirmDefaultCostlyModes = []string{"pro", "critical"}

// The confirm module's own refusal codes. They are not in Codes: that list is
// `shared/mailbox-schema`'s and the two ends compare it as a set. The codes that
// are also the schema's (bad-key, bad-character, too-long, unknown-verb,
// unknown-field, missing-field, wrong-type, empty-string) are reused rather than
// respelled, which is what the module does.
const (
	// CodeConfirmNotAConfirmation: a confirming utterance that is not the fixed
	// phrase -- "conform", "confirmed it", "yes".
	CodeConfirmNotAConfirmation Code = "not-a-confirmation"
	// CodeConfirmCommandAndConfirm: one utterance carrying a command and a
	// confirm token, so the same misrecognition cannot satisfy both halves of
	// D47's Rule.
	CodeConfirmCommandAndConfirm Code = "command-and-confirm"
	// CodeConfirmRefusedVerb: a verb the ruling refuses outright.
	CodeConfirmRefusedVerb Code = "refused-verb"
	// CodeConfirmUnresolvedTarget: a destructive verb with no resolved target,
	// so there is nothing to read back and nothing to confirm.
	CodeConfirmUnresolvedTarget Code = "unresolved-target"
	// CodeConfirmVerbTargetMismatch: a verb handed an argument of a kind it does
	// not take.
	CodeConfirmVerbTargetMismatch Code = "verb-target-mismatch"
	// CodeConfirmBadRepo, CodeConfirmBadPR and CodeConfirmBadHeadSHA: a
	// repository that is not `owner/name`, a PR number outside its bound, and a
	// head SHA that is not 40 or 64 lowercase hex characters.
	CodeConfirmBadRepo    Code = "bad-repo"
	CodeConfirmBadPR      Code = "bad-pr"
	CodeConfirmBadHeadSHA Code = "bad-head-sha"
	// CodeConfirmNoPendingAction: a confirm whose action id names no outstanding
	// read-back.
	CodeConfirmNoPendingAction Code = "no-pending-action"
	// CodeConfirmAmbiguousPending: a confirm with more than one read-back
	// outstanding, so it names no one of them.
	CodeConfirmAmbiguousPending Code = "ambiguous-pending"
	// CodeConfirmDifferentTarget: a confirm asserting a different target than
	// the one read back -- the pending action changed, or a merge's PR head
	// moved.
	CodeConfirmDifferentTarget Code = "different-target"
	// CodeConfirmExpired: a confirm past the read-back's timeout.
	CodeConfirmExpired Code = "confirmation-expired"
	// CodeConfirmSameUtterance: the utterance that issued the command.
	CodeConfirmSameUtterance Code = "same-utterance"
	// CodeConfirmReplayedUtterance, CodeConfirmReplayedAction and
	// CodeConfirmReplayedConfirmation: an id already spent.
	CodeConfirmReplayedUtterance    Code = "replayed-utterance"
	CodeConfirmReplayedAction       Code = "replayed-action"
	CodeConfirmReplayedConfirmation Code = "replayed-confirmation"
	// CodeConfirmPendingFull: a command with no room for another read-back.
	CodeConfirmPendingFull Code = "pending-full"
	// CodeConfirmLedgerFull: no room in the id ledger, and nothing inside it is
	// ever forgotten to make room.
	CodeConfirmLedgerFull Code = "ledger-full"
	// CodeConfirmUnknownEvent: an event whose `type` is not one of the three.
	CodeConfirmUnknownEvent Code = "unknown-event"
	// CodeConfirmInvalidNow: a `now` or an `at` that is not an epoch-millisecond
	// integer in range.
	CodeConfirmInvalidNow Code = "invalid-now"
	// CodeConfirmNowWentBackwards and CodeConfirmNowJumped: the two `now` guards.
	CodeConfirmNowWentBackwards Code = "now-went-backwards"
	CodeConfirmNowJumped        Code = "now-jumped"
	// CodeConfirmUnknownState and CodeConfirmInvalidState: a phase that is not
	// one of the three, and a declared phase that disagrees with the pendings.
	CodeConfirmUnknownState Code = "unknown-state"
	CodeConfirmInvalidState Code = "invalid-state"
	// CodeConfirmInvalidTimeout, CodeConfirmInvalidMaxPending,
	// CodeConfirmInvalidMaxLedger, CodeConfirmInvalidPending,
	// CodeConfirmInvalidPendingEntry, CodeConfirmDuplicatePendingEntry,
	// CodeConfirmUnsortedPending, CodeConfirmInvalidLedger,
	// CodeConfirmInvalidLedgerEntry, CodeConfirmDuplicateLedgerEntry and
	// CodeConfirmUnsortedLedger: a state that is not one the machine would read.
	CodeConfirmInvalidTimeout        Code = "invalid-timeout"
	CodeConfirmInvalidMaxPending     Code = "invalid-max-pending"
	CodeConfirmInvalidMaxLedger      Code = "invalid-max-ledger"
	CodeConfirmInvalidPending        Code = "invalid-pending"
	CodeConfirmInvalidPendingEntry   Code = "invalid-pending-entry"
	CodeConfirmDuplicatePendingEntry Code = "duplicate-pending-entry"
	CodeConfirmUnsortedPending       Code = "unsorted-pending"
	CodeConfirmInvalidLedger         Code = "invalid-ledger"
	CodeConfirmInvalidLedgerEntry    Code = "invalid-ledger-entry"
	CodeConfirmDuplicateLedgerEntry  Code = "duplicate-ledger-entry"
	CodeConfirmUnsortedLedger        Code = "unsorted-ledger"
	// CodeConfirmQueueFull is P6.5's answer when the pending-prompt queue is
	// full, CodeConfirmUnknownSpentKind is a spent id of a kind this file does
	// not spend, and CodeConfirmInvalidMemory is this file's own: a memory that
	// is present but not one this consumer will trust.
	CodeConfirmQueueFull        Code = "queue-full"
	CodeConfirmUnknownSpentKind Code = "unknown-spent-kind"
	CodeConfirmInvalidMemory    Code = "invalid-memory"
)

// ConfirmCodes is every code this file speaks that the module also speaks, so a
// test can hold the vocabulary to the module's REFUSAL table by set.
// CodeConfirmQueueFull, CodeConfirmUnknownSpentKind and CodeConfirmInvalidMemory
// are this file's own and are deliberately not here.
var ConfirmCodes = []Code{
	CodeConfirmNotAConfirmation, CodeConfirmCommandAndConfirm, CodeConfirmRefusedVerb,
	CodeConfirmUnresolvedTarget, CodeConfirmVerbTargetMismatch,
	CodeConfirmBadRepo, CodeConfirmBadPR, CodeConfirmBadHeadSHA,
	CodeConfirmNoPendingAction, CodeConfirmAmbiguousPending, CodeConfirmDifferentTarget,
	CodeConfirmExpired, CodeConfirmSameUtterance,
	CodeConfirmReplayedUtterance, CodeConfirmReplayedAction, CodeConfirmReplayedConfirmation,
	CodeConfirmPendingFull, CodeConfirmLedgerFull, CodeConfirmUnknownEvent,
	CodeConfirmInvalidNow, CodeConfirmNowWentBackwards, CodeConfirmNowJumped,
	CodeConfirmUnknownState, CodeConfirmInvalidState,
	CodeConfirmInvalidTimeout, CodeConfirmInvalidMaxPending, CodeConfirmInvalidMaxLedger,
	CodeConfirmInvalidPending, CodeConfirmInvalidPendingEntry,
	CodeConfirmDuplicatePendingEntry, CodeConfirmUnsortedPending,
	CodeConfirmInvalidLedger, CodeConfirmInvalidLedgerEntry,
	CodeConfirmDuplicateLedgerEntry, CodeConfirmUnsortedLedger,
}

// ConfirmTarget is a resolved target: the run a cancel cancels, or the
// repository, PR and head a merge merges, or the repository and PR a confirm
// confirms. The zero value of a field means the target did not carry it, which
// is what the field-set rule reads as absent (see this file's header).
type ConfirmTarget struct {
	Kind    string
	RunID   string
	Repo    string
	PR      int64
	HeadSHA string
	// Mode is what a costly start binds (D2786): the mode the read-back named,
	// and the only thing a launch's target carries.
	Mode string
}

// ConfirmPending is one outstanding read-back: what was read back, when, and the
// utterance that asked for it.
type ConfirmPending struct {
	ActionID string
	Verb     string
	Target   ConfirmTarget
	At       int64
	Deadline int64
	By       string
}

// ConfirmSpent is one id the machine's ledger remembers.
type ConfirmSpent struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	At   int64  `json:"at"`
}

// ConfirmState is the machine: the phase and the four knobs, the read-backs
// outstanding and the ids spent. A state is refused rather than repaired when it
// disagrees with itself, because a state that can be read two ways has two
// answers to every question.
type ConfirmState struct {
	Phase      ConfirmPhase
	Now        int64
	TimeoutMs  int64
	MaxPending int
	MaxLedger  int
	// CostlyModes and BiometricOnCostly are the session's own policy values
	// (C2's `costly_modes` and `biometric_on_costly`): they ride the state
	// because D2786's answer is a function of them, as the timeout is.
	CostlyModes       []string
	BiometricOnCostly bool
	Pending           []ConfirmPending
	Spent             []ConfirmSpent
}

// ConfirmEvent is one utterance, as the machine reads it. HasTarget records
// whether the event carried a target at all: a launch carrying one, even a null
// one, is a caller that thinks a launch is read back, and the module refuses it.
type ConfirmEvent struct {
	Type        ConfirmEventType
	Now         int64
	UtteranceID string
	Text        string
	Verb        string
	Target      *ConfirmTarget
	HasTarget   bool
	// Mode is the mode a *launch* would start in, with HasMode recording whether
	// the event carried one at all: a start that names none is a start the
	// router did not resolve a costly mode for (D2786).
	Mode        string
	HasMode     bool
	ActionID    string
	HasActionID bool
	// ConfirmationID is the id of the confirmation the event carries.
	ConfirmationID string
}

// ConfirmOptions are the knobs `shared/confirm`'s initialState takes. Zero means
// the module's default for each, which is what a caller with no reason to move
// one wants.
type ConfirmOptions struct {
	Now        int64
	TimeoutMs  int64
	MaxPending int
	MaxLedger  int
	// CostlyModes and BiometricOnCostly are the session policy D2786 reads. A
	// nil list means P6's default (ConfirmDefaultCostlyModes); a caller that
	// means "no mode is costly" passes an empty, non-nil list.
	CostlyModes       []string
	BiometricOnCostly bool
}

// ConfirmAnswer is one step's answer: an outcome with the state it produced, or
// a refusal with no state at all. A refusal carries no state because a refused
// utterance spends nothing -- the caller keeps what it had.
type ConfirmAnswer struct {
	OK             bool
	Outcome        ConfirmOutcome
	Phase          ConfirmPhase
	Verb           string
	ActionID       string
	ConfirmationID string
	Target         ConfirmTarget
	At             int64
	Deadline       int64
	ConfirmedAt    int64
	ReadBack       string
	// Biometric is D2786's second demand on a costly start: the session's switch
	// asks for the biometric re-auth (P5.1g's path) beside the phrase.
	Biometric bool
	Record    *ConfirmRecord
	ActionIDs []string
	State     ConfirmState
	Code      Code
	Field     string
}

// ConfirmRecord is D169's action record, field for field: the id of the action,
// the verb, the target read back, the head SHA the read-back named (a merge's
// only) and the confirmation. It is the record the phone writes into a message
// and the consumer re-validates.
type ConfirmRecord struct {
	ID           string
	Verb         string
	Target       ConfirmTarget
	Confirmation ConfirmConfirmation
}

// ConfirmConfirmation is the id of a confirmation and the time it was given.
type ConfirmConfirmation struct {
	ID string
	At int64
}

// Value is the record as P6.3's schema spells it: `{id, verb, target,
// head_sha?, confirmation}`, with `head_sha` only on a merge -- which is what
// the schema refuses on every other verb.
func (r ConfirmRecord) Value() Record {
	target := Record{}
	if r.Target.Kind == ConfirmTargetRun {
		target["run"] = r.Target.RunID
	} else if r.Target.Kind == ConfirmTargetMode {
		// D2786's start: the record binds the mode the read-back named, and a
		// start has no run, no repository and no head to bind.
		target["mode"] = r.Target.Mode
	} else {
		target["repo"] = r.Target.Repo
		target["pr"] = r.Target.PR
	}
	value := Record{
		"id":           r.ID,
		"verb":         r.Verb,
		"target":       target,
		"confirmation": Record{"id": r.Confirmation.ID, "at": r.Confirmation.At},
	}
	if r.Target.Kind == ConfirmTargetMerge {
		value["head_sha"] = r.Target.HeadSHA
	}
	return value
}

// Action is the record in the message binding's own vocabulary, which is what
// the consumer's runner reads: `fugaro cancel RUN` takes the run id, and
// `gh pr merge` takes the repository, the number and the head.
func (r ConfirmRecord) Action() *Action {
	// A start's record has no Action: this vocabulary is the two commands the
	// CLI runs, and a launch is the orchestrator's to start (P5.1), so a record
	// built from a costly start read-back authorises nothing here. nil rather
	// than a half-filled Action, which is what the consumer would execute by
	// mistake -- and there is nothing to fill it with: a launch's Action names
	// the repository to start in, and a mode target has no repository.
	if r.Target.Kind == ConfirmTargetMode {
		return nil
	}
	a := &Action{ID: r.ID, Verb: r.Verb, Confirmation: Confirmation{ID: r.Confirmation.ID, At: r.Confirmation.At}}
	if r.Target.Kind == ConfirmTargetRun {
		a.Target = Target{Run: r.Target.RunID}
		return a
	}
	a.Target = Target{Repo: r.Target.Repo, PR: r.Target.PR, IsRepo: true}
	if r.Target.Kind == ConfirmTargetMerge {
		a.HeadSHA, a.HasHeadSHA = r.Target.HeadSHA, true
	}
	return a
}

// ConfirmInitialState builds the empty machine. `now` is required and is the
// caller's: the module reads no clock of its own.
func ConfirmInitialState(o ConfirmOptions) (ConfirmState, *Refusal) {
	if o.Now < 1 || o.Now > TimeMax {
		return ConfirmState{}, refuse(CodeConfirmInvalidNow, "now")
	}
	timeout := o.TimeoutMs
	if timeout == 0 {
		timeout = ConfirmDefaultTimeoutMs
	}
	if timeout < ConfirmMinTimeoutMs || timeout > ConfirmMaxTimeoutMs {
		return ConfirmState{}, refuse(CodeConfirmInvalidTimeout, "timeoutMs")
	}
	maxPending := o.MaxPending
	if maxPending == 0 {
		maxPending = ConfirmDefaultMaxPending
	}
	if maxPending < ConfirmMinMaxPending || maxPending > ConfirmMaxMaxPending {
		return ConfirmState{}, refuse(CodeConfirmInvalidMaxPending, "maxPending")
	}
	maxLedger := o.MaxLedger
	if maxLedger == 0 {
		maxLedger = ConfirmDefaultMaxLedger
	}
	if maxLedger < ConfirmMinMaxLedger || maxLedger > ConfirmMaxMaxLedger {
		return ConfirmState{}, refuse(CodeConfirmInvalidMaxLedger, "maxLedger")
	}
	costlyModes := o.CostlyModes
	if costlyModes == nil {
		costlyModes = ConfirmDefaultCostlyModes
	}
	if refusal := confirmReadCostlyModes(costlyModes); refusal != nil {
		return ConfirmState{}, refusal
	}
	return ConfirmState{
		Phase: ConfirmPhaseIdle, Now: o.Now, TimeoutMs: timeout,
		MaxPending: maxPending, MaxLedger: maxLedger,
		CostlyModes: append([]string{}, costlyModes...), BiometricOnCostly: o.BiometricOnCostly,
		Pending: []ConfirmPending{}, Spent: []ConfirmSpent{},
	}, nil
}

// confirmIsCostlyStart answers whether a start in `mode` is the costly start
// D2786 names: one the session's own list calls costly.
func confirmIsCostlyStart(mode string, costlyModes []string) bool {
	return confirmContains(costlyModes, mode)
}

// confirmReadCostlyModes reads the modes a session calls costly: a bounded list
// of bounded single-line names, read with the same reader an id or a text gets.
//
// A list that names one mode twice is not refused: membership is the whole of
// what the list means, so the second entry answers what the first did. The port
// that builds the list from the project policy is the one that refuses a
// duplicate (C2's checkPolicy), and this file is not the reader of a session.
//
// A nil list is refused rather than read as "no mode is costly": the two ends
// have to answer a state that does not say what the session calls costly the
// same way, and the module refuses it (`missing-field/costlyModes`) -- a state
// that has lost the field is a defect, and the lenient reading of a defect is
// exactly the reading that lets a costly start through with no read-back. A
// state that *names* no costly mode is the empty list, which is read here as
// what it says.
func confirmReadCostlyModes(modes []string) *Refusal {
	if modes == nil {
		return refuse(CodeMissingField, "costlyModes")
	}
	if len(modes) > ConfirmMaxCostlyModes {
		return refuse(CodeTooLong, "costlyModes")
	}
	for _, mode := range modes {
		if _, refusal := confirmString(mode, "costlyModes", ConfirmMaxModeLen); refusal != nil {
			return refusal
		}
	}
	return nil
}

// ConfirmStep applies one event to one state, in the order the rules state it:
// the state, the event's `now`, the two `now` guards, then what the event says.
// Everything that can be said about an utterance is said before anything is
// spent or refused for lack of room, so a caller hears the real reason.
func ConfirmStep(state ConfirmState, ev ConfirmEvent) ConfirmAnswer {
	if refusal := confirmReadState(&state); refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	if ev.Now < 1 || ev.Now > TimeMax {
		return ConfirmAnswer{Code: CodeConfirmInvalidNow, Field: "now"}
	}
	if ev.Now < state.Now {
		// The timeout is a function of the pending time and this `now`, and the
		// ledger is what makes a replay permanent. A `now` that goes backwards
		// would make an expired read-back live again, so it is refused rather
		// than absorbed.
		return ConfirmAnswer{Code: CodeConfirmNowWentBackwards, Field: "now"}
	}
	if ev.Now-state.Now > ConfirmForwardJumpMs {
		// `now` only moves forward, so one far-future `now` would otherwise
		// refuse every later honest one as `now-went-backwards` for ever.
		return ConfirmAnswer{Code: CodeConfirmNowJumped, Field: "now"}
	}
	switch ev.Type {
	case ConfirmEventCommand:
		return confirmCommand(state, ev)
	case ConfirmEventConfirm:
		return confirmConfirm(state, ev)
	case ConfirmEventAbort:
		return confirmAbort(state, ev)
	default:
		// An unknown `type` is refused after `now`, so that the refusal is about
		// the type rather than about a clock the event also got wrong.
		return ConfirmAnswer{Code: CodeConfirmUnknownEvent, Field: "type"}
	}
}

func confirmCommand(state ConfirmState, ev ConfirmEvent) ConfirmAnswer {
	utteranceID, refusal := confirmKeyID(ev.UtteranceID, "utteranceId")
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	text, refusal := confirmString(ev.Text, "text", ConfirmTextMax)
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	verb, refusal := confirmString(ev.Verb, "verb", ConfirmIDMax)
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	actionID := ""
	if ev.HasActionID {
		actionID, refusal = confirmKeyID(ev.ActionID, "actionId")
		if refusal != nil {
			return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
		}
	}

	if confirmCarriesToken(text) {
		// D47's Rule: the same misrecognition must not satisfy both halves.
		return ConfirmAnswer{Code: CodeConfirmCommandAndConfirm, Field: "text"}
	}
	if !confirmContains(confirmVerbValues(), verb) {
		return ConfirmAnswer{Code: CodeUnknownVerb, Field: "verb"}
	}
	if confirmContains(ConfirmRefusedVerbs, verb) {
		// The kill switch, the budget writes, the deletes: refused outright by
		// the 2026-10-17 ruling, whatever the target says. Refused before the
		// target is read, so nothing about it is echoed and nothing about it is
		// trusted.
		return ConfirmAnswer{Code: CodeConfirmRefusedVerb, Field: "verb"}
	}

	kinds := confirmVerbTargetKinds[verb]
	// D162's launch is the one verb whose answer is not the verb's alone: D2786
	// reads it back when the mode the router resolved is a costly one, and lets
	// it through otherwise. `mode` is that verb's own field, and a target is not
	// a field it has at all -- a start has no run id yet, so what there is to
	// read back and to bind is the mode.
	isStart := verb == ConfirmVerbLaunch
	if isStart {
		if ev.HasTarget {
			return ConfirmAnswer{Code: CodeConfirmVerbTargetMismatch, Field: "target"}
		}
	} else if ev.HasMode {
		// A mode is the launch's field alone: a cancel that carries one is a
		// caller that thinks a mode changes what cancelling means.
		return ConfirmAnswer{Code: CodeConfirmVerbTargetMismatch, Field: "mode"}
	}

	// The costly start, resolved from the mode and the session's policy before
	// anything else is spent. A mode this file cannot read is refused as the
	// value it is, in the same place and with the same codes as any other field.
	var resolved ConfirmTarget
	// A destructive verb is always read back; a start is read back only when
	// D2786 makes it costly.
	needsReadBack := !isStart
	biometric := false
	if isStart {
		if ev.HasMode {
			mode, refusal := confirmString(ev.Mode, "mode", ConfirmMaxModeLen)
			if refusal != nil {
				return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
			}
			if confirmIsCostlyStart(mode, state.CostlyModes) {
				resolved = ConfirmTarget{Kind: ConfirmTargetMode, Mode: mode}
				needsReadBack, biometric = true, state.BiometricOnCostly
			}
		}
	} else if !ev.HasActionID {
		// A destructive verb must carry the action id the confirmation will
		// name: without it there is no id for "one confirmation authorises
		// exactly one action" to be about.
		return ConfirmAnswer{Code: CodeMissingField, Field: "actionId"}
	}

	if isStart {
		// A costly start is read back, so it carries the action id the
		// confirmation will name; a start that names no mode, or one the session
		// does not call costly, is not read back and carries no action id either.
		if !needsReadBack {
			if ev.HasActionID {
				return ConfirmAnswer{Code: CodeConfirmVerbTargetMismatch, Field: "actionId"}
			}
		} else if !ev.HasActionID {
			return ConfirmAnswer{Code: CodeMissingField, Field: "actionId"}
		}
	}

	if !isStart {
		resolved, refusal = confirmReadTarget(ev.Target, kinds)
		if refusal != nil {
			return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
		}
	}

	if confirmHasSpent(state, ConfirmLedgerUtterance, utteranceID) {
		return ConfirmAnswer{Code: CodeConfirmReplayedUtterance, Field: "utteranceId"}
	}
	if needsReadBack && confirmHasSpent(state, ConfirmLedgerAction, actionID) {
		// Two commands under one action id are an ambiguity about which action a
		// confirmation authorised, so the second is refused rather than allowed
		// to overwrite the first. It is the resolved target that makes an action
		// id one to spend -- a costly start's mode as much as a run's id -- so a
		// start that is read back is held to it exactly as a cancel is, and a
		// plain start, which carries no action id at all, never reaches here.
		return ConfirmAnswer{Code: CodeConfirmReplayedAction, Field: "actionId"}
	}

	if !needsReadBack {
		// D162: a launch goes straight through. Its utterance is still spent, so
		// "one utterance, one event" holds for it too -- a pass-through
		// authorises nothing, but its id is not a second utterance later.
		if len(state.Spent)+1 > state.MaxLedger {
			return ConfirmAnswer{Code: CodeConfirmLedgerFull, Field: "ledger"}
		}
		spent := confirmSpend(state.Spent, ConfirmSpent{Kind: ConfirmLedgerUtterance, ID: utteranceID, At: ev.Now})
		return ConfirmAnswer{
			OK: true, Outcome: ConfirmOutcomePassThrough, Phase: state.Phase, Verb: verb,
			State: confirmBuildState(state.Phase, ev.Now, state, state.Pending, spent),
		}
	}

	if len(state.Spent)+2 > state.MaxLedger {
		return ConfirmAnswer{Code: CodeConfirmLedgerFull, Field: "ledger"}
	}
	// A read-back that timed out is dropped here rather than kept: the pending
	// set must stay bounded, and a command is the one event that may always add
	// one. A confirm never sweeps, because a sweep there would answer
	// `no-pending-action` where the plan requires the timeout to be named.
	live := confirmSweep(state.Pending, ev.Now)
	if len(live)+1 > state.MaxPending {
		return ConfirmAnswer{Code: CodeConfirmPendingFull, Field: "pending"}
	}
	entry := ConfirmPending{
		ActionID: actionID, Verb: verb, Target: resolved,
		At: ev.Now, Deadline: ev.Now + state.TimeoutMs, By: utteranceID,
	}
	pending := confirmOrderPending(append(append([]ConfirmPending{}, live...), entry))
	phase := confirmPhaseOf(pending)
	spent := confirmSpend(state.Spent,
		ConfirmSpent{Kind: ConfirmLedgerUtterance, ID: utteranceID, At: ev.Now},
		ConfirmSpent{Kind: ConfirmLedgerAction, ID: actionID, At: ev.Now},
	)
	return ConfirmAnswer{
		OK: true, Outcome: ConfirmOutcomeReadBack, Phase: phase, Verb: verb,
		ActionID: actionID, Target: resolved, At: ev.Now, Deadline: entry.Deadline,
		ReadBack:  ConfirmReadBack(verb, resolved),
		Biometric: biometric,
		State:     confirmBuildState(phase, ev.Now, state, pending, spent),
	}
}

func confirmConfirm(state ConfirmState, ev ConfirmEvent) ConfirmAnswer {
	utteranceID, refusal := confirmKeyID(ev.UtteranceID, "utteranceId")
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	text, refusal := confirmString(ev.Text, "text", ConfirmTextMax)
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	actionID, refusal := confirmKeyID(ev.ActionID, "actionId")
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	confirmationID, refusal := confirmKeyID(ev.ConfirmationID, "confirmationId")
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}

	if !ConfirmIsPhrase(text) {
		// "conform", "confirmed it", "yes", "sure": each is refused -- the
		// phrase is fixed, short and low-confusability, and nothing here repairs
		// a misrecognition into a confirmation.
		return ConfirmAnswer{Code: CodeConfirmNotAConfirmation, Field: "text"}
	}
	if len(state.Pending) > 1 {
		// A speaker with two read-backs outstanding has not said which one, so
		// nothing is authorised.
		return ConfirmAnswer{Code: CodeConfirmAmbiguousPending, Field: "pending"}
	}
	if len(state.Pending) == 0 {
		return ConfirmAnswer{Code: CodeConfirmNoPendingAction, Field: "actionId"}
	}
	entry := state.Pending[0]
	if entry.ActionID != actionID {
		// "one confirmation authorises exactly one action": an id naming no
		// outstanding read-back confirms nothing, and an identical confirmation
		// replayed after its action was consumed arrives here, because that
		// read-back is gone.
		return ConfirmAnswer{Code: CodeConfirmNoPendingAction, Field: "actionId"}
	}
	if entry.By == utteranceID {
		return ConfirmAnswer{Code: CodeConfirmSameUtterance, Field: "utteranceId"}
	}
	if confirmHasSpent(state, ConfirmLedgerUtterance, utteranceID) {
		return ConfirmAnswer{Code: CodeConfirmReplayedUtterance, Field: "utteranceId"}
	}
	if confirmHasSpent(state, ConfirmLedgerConfirmation, confirmationID) {
		// A spent confirmation is spent against every later action, not only the
		// one it confirmed.
		return ConfirmAnswer{Code: CodeConfirmReplayedConfirmation, Field: "confirmationId"}
	}

	kinds, _ := confirmVerbTargetKinds[entry.Verb]
	target, refusal := confirmReadTarget(ev.Target, kinds)
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	if !confirmSameTarget(entry.Target, target) {
		// The pending action changed, or -- for a merge -- the PR's head moved
		// since the read-back. Either way the read-back the speaker heard is not
		// what would happen now, so the confirmation is refused (D169).
		return ConfirmAnswer{Code: CodeConfirmDifferentTarget, Field: "target"}
	}
	if ev.Now > entry.Deadline {
		return ConfirmAnswer{Code: CodeConfirmExpired, Field: "now"}
	}
	if len(state.Spent)+2 > state.MaxLedger {
		return ConfirmAnswer{Code: CodeConfirmLedgerFull, Field: "ledger"}
	}

	pending := make([]ConfirmPending, 0, len(state.Pending))
	for _, one := range state.Pending {
		if one.ActionID != entry.ActionID {
			pending = append(pending, one)
		}
	}
	// Everything authorised is the target that was READ BACK, never the one the
	// confirmation asserted: the assertion is only ever compared with it, so a
	// confirmation cannot substitute a target the speaker did not hear, and the
	// record binds the head the read-back named.
	record := ConfirmRecord{
		ID: entry.ActionID, Verb: entry.Verb, Target: entry.Target,
		Confirmation: ConfirmConfirmation{ID: confirmationID, At: ev.Now},
	}
	phase := confirmPhaseOf(pending)
	spent := confirmSpend(state.Spent,
		ConfirmSpent{Kind: ConfirmLedgerUtterance, ID: utteranceID, At: ev.Now},
		ConfirmSpent{Kind: ConfirmLedgerConfirmation, ID: confirmationID, At: ev.Now},
	)
	return ConfirmAnswer{
		OK: true, Outcome: ConfirmOutcomeConfirmed, Phase: phase, Verb: entry.Verb,
		ActionID: entry.ActionID, ConfirmationID: confirmationID, Target: entry.Target,
		ConfirmedAt: ev.Now, ReadBack: ConfirmReadBack(entry.Verb, entry.Target),
		Record: &record,
		State:  confirmBuildState(phase, ev.Now, state, pending, spent),
	}
}

func confirmAbort(state ConfirmState, ev ConfirmEvent) ConfirmAnswer {
	utteranceID, refusal := confirmKeyID(ev.UtteranceID, "utteranceId")
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	text, refusal := confirmString(ev.Text, "text", ConfirmTextMax)
	if refusal != nil {
		return ConfirmAnswer{Code: refusal.Code, Field: refusal.Field}
	}
	if confirmCarriesToken(text) {
		return ConfirmAnswer{Code: CodeConfirmCommandAndConfirm, Field: "text"}
	}
	if confirmHasSpent(state, ConfirmLedgerUtterance, utteranceID) {
		return ConfirmAnswer{Code: CodeConfirmReplayedUtterance, Field: "utteranceId"}
	}
	if len(state.Spent)+1 > state.MaxLedger {
		return ConfirmAnswer{Code: CodeConfirmLedgerFull, Field: "ledger"}
	}
	live := confirmSweep(state.Pending, ev.Now)
	outcome := ConfirmOutcomeNothingPending
	if len(live) > 0 {
		outcome = ConfirmOutcomeDiscarded
	}
	ids := make([]string, 0, len(live))
	for _, one := range live {
		ids = append(ids, one.ActionID)
	}
	spent := confirmSpend(state.Spent, ConfirmSpent{Kind: ConfirmLedgerUtterance, ID: utteranceID, At: ev.Now})
	return ConfirmAnswer{
		OK: true, Outcome: outcome, Phase: ConfirmPhaseIdle, ActionIDs: ids,
		State: confirmBuildState(ConfirmPhaseIdle, ev.Now, state, []ConfirmPending{}, spent),
	}
}

// ConfirmReadBack builds the spoken read-back for a resolved target. It takes
// the verb and the target and nothing else -- no transcript, no name the speaker
// used -- because D47's Rule requires the read-back to name the exact run id (or
// the repository and PR) rather than a paraphrase. It answers "" when the verb
// and the target do not agree, so a caller cannot compose a read-back for a pair
// the machine would refuse.
func ConfirmReadBack(verb string, target ConfirmTarget) string {
	kinds, ok := confirmVerbTargetKinds[verb]
	if !ok {
		return ""
	}
	resolved, refusal := confirmReadTarget(&target, kinds)
	if refusal != nil {
		return ""
	}
	var what string
	switch {
	case resolved.Kind == ConfirmTargetMode:
		// D47's Rule, for a start: there is no run id yet, so what the speaker
		// must hear again is the mode the start would run in -- the one value
		// D2786 makes the difference about.
		what = fmt.Sprintf("Start a run in %s mode.", resolved.Mode)
	case resolved.Kind == ConfirmTargetMerge:
		what = fmt.Sprintf("Merge %s#%d at head %s.", resolved.Repo, resolved.PR, resolved.HeadSHA)
	case verb == ConfirmVerbCancel:
		what = fmt.Sprintf("Cancel run %s.", resolved.RunID)
	case resolved.Kind == ConfirmTargetRun:
		what = fmt.Sprintf("Confirm run %s.", resolved.RunID)
	default:
		what = fmt.Sprintf("Confirm %s#%d.", resolved.Repo, resolved.PR)
	}
	return what + " " + ConfirmCue
}

// ConfirmPhrase is the fixed phrase (D128 as amended; Voice D47's "fixed, short,
// low-confusability phrase"), and it is `shared/command`'s CONFIRMATION_PHRASES'
// only member.
const ConfirmPhrase = "confirm"

// confirmWakeAliases is `shared/command`'s WAKE_ALIASES: the spellings a speaker
// may use for the wake word, matched against the first token only.
var confirmWakeAliases = []string{"fugaro", "figaro"}

// ConfirmIsPhrase decides whether an utterance is the fixed phrase.
//
// It is **P6.1's answer, restated in Go**: the utterance is routed by
// `shared/command`'s router with its default wake-word aliases, and it is the
// phrase only when the route is the `confirm` app command. "Fugaro, confirm" and
// "Figaro, confirm." are the phrase and a bare "confirm" is not -- the router
// forwards that to the agent as text (D164), and a machine that also read it as
// a confirmation would act on words the agent received too. Every near miss
// ("conform", "confirmed it", "yes", "Fugaro, confirm it") and any utterance
// carrying an invisible format character is refused.
//
// The router's own table makes the rest decidable here: the `confirm` row is the
// pattern `['confirm']` and nothing else, so the phrase is exactly two folded
// words -- the wake alias and `confirm`. No other row matches a single token, so
// no other route is reachable from a two-word utterance.
func ConfirmIsPhrase(text string) bool {
	if text == "" || utf16Len(text) > ConfirmMaxUtteranceLen {
		return false
	}
	if bad, _ := confirmBadCharacter(text, true); bad != "" {
		return false
	}
	words := confirmWords(text)
	if len(words) != 2 {
		return false
	}
	if !confirmContains(confirmWakeAliases, words[0]) {
		return false
	}
	return words[1] == ConfirmPhrase
}

// confirmVerbTargetKinds is P6.3's verb/target rule: a cancel names a run, a
// merge a repository, PR and head, and a confirm either a run or a repository
// and PR -- never a head, which the schema refuses on it. A verb absent from
// this table is a pass-through verb, which takes no target at all.
var confirmVerbTargetKinds = map[string][]string{
	ConfirmVerbCancel:  {ConfirmTargetRun},
	ConfirmVerbConfirm: {ConfirmTargetRun, ConfirmTargetPR},
	ConfirmVerbMerge:   {ConfirmTargetMerge},
	// A costly start's target is its mode (D2786). The table is what a pending
	// read-back and a confirmation are read against, so a launch read back is
	// confirmed by asserting the same mode, and nothing else.
	ConfirmVerbLaunch: {ConfirmTargetMode},
}

func confirmVerbValues() []string {
	values := make([]string, 0, len(ConfirmDestructiveVerbs)+len(ConfirmPassThroughVerbs)+len(ConfirmRefusedVerbs))
	values = append(values, ConfirmDestructiveVerbs...)
	values = append(values, ConfirmPassThroughVerbs...)
	values = append(values, ConfirmRefusedVerbs...)
	return values
}

// confirmReadTarget reads a resolved target of the kind the verb requires. The
// target is the one value a confirmation is about, so it is read strictly: the
// kind must be the verb's kind, the field set is the version, every value is
// bounded, and a repository must be `owner/name` with both segments present.
// Nothing is normalised -- a repository is not lower-cased and a SHA is not
// shortened -- because a target this file repaired would be a target the
// consumer never saw.
func confirmReadTarget(value *ConfirmTarget, kinds []string) (ConfirmTarget, *Refusal) {
	if value == nil {
		// An absent target and a null one are the same failure here: neither is
		// a target the machine could read back.
		return ConfirmTarget{}, refuse(CodeConfirmUnresolvedTarget, "target")
	}
	if value.Kind == "" {
		return ConfirmTarget{}, refuse(CodeMissingField, "target.kind")
	}
	// `kind` is read on its own, before the field set: a target of another kind
	// is a mismatch, not an unknown field, and the refusal must say which.
	if !confirmContains(kinds, value.Kind) {
		return ConfirmTarget{}, refuse(CodeConfirmVerbTargetMismatch, "target.kind")
	}
	switch value.Kind {
	case ConfirmTargetMode:
		if value.RunID != "" || value.Repo != "" || value.PR != 0 || value.HeadSHA != "" {
			return ConfirmTarget{}, refuse(CodeUnknownField, "target")
		}
		mode, refusal := confirmString(value.Mode, "target.mode", ConfirmMaxModeLen)
		if refusal != nil {
			return ConfirmTarget{}, refusal
		}
		return ConfirmTarget{Kind: ConfirmTargetMode, Mode: mode}, nil
	case ConfirmTargetRun:
		if value.Repo != "" || value.PR != 0 || value.HeadSHA != "" || value.Mode != "" {
			return ConfirmTarget{}, refuse(CodeUnknownField, "target")
		}
		runID, refusal := confirmKeyID(value.RunID, "target.runId")
		if refusal != nil {
			return ConfirmTarget{}, refusal
		}
		// P6.3's run alphabet and shape: nothing the consumer's command line
		// could read as a flag or a path.
		if !runChars.MatchString(runID) || !runPattern.MatchString(runID) {
			return ConfirmTarget{}, refuse(CodeBadKey, "target.runId")
		}
		return ConfirmTarget{Kind: ConfirmTargetRun, RunID: runID}, nil
	case ConfirmTargetPR, ConfirmTargetMerge:
		if value.RunID != "" || value.Mode != "" {
			return ConfirmTarget{}, refuse(CodeUnknownField, "target")
		}
		if value.Kind == ConfirmTargetPR && value.HeadSHA != "" {
			return ConfirmTarget{}, refuse(CodeUnknownField, "target")
		}
		repo, refusal := confirmRepo(value.Repo)
		if refusal != nil {
			return ConfirmTarget{}, refusal
		}
		if value.PR < 1 || value.PR > ConfirmPRMax {
			return ConfirmTarget{}, refuse(CodeConfirmBadPR, "target.pr")
		}
		if value.Kind == ConfirmTargetPR {
			return ConfirmTarget{Kind: ConfirmTargetPR, Repo: repo, PR: value.PR}, nil
		}
		sha, refusal := confirmHeadSHA(value.HeadSHA)
		if refusal != nil {
			return ConfirmTarget{}, refusal
		}
		return ConfirmTarget{Kind: ConfirmTargetMerge, Repo: repo, PR: value.PR, HeadSHA: sha}, nil
	}
	return ConfirmTarget{}, refuse(CodeConfirmVerbTargetMismatch, "target.kind")
}

// confirmRepo validates `owner/name`.
func confirmRepo(value string) (string, *Refusal) {
	if value == "" {
		return "", refuse(CodeMissingField, "target.repo")
	}
	if utf16Len(value) > ConfirmRepoMax {
		return "", refuse(CodeTooLong, "target.repo")
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !confirmRepoSegment(parts[0]) || !confirmRepoSegment(parts[1]) {
		return "", refuse(CodeConfirmBadRepo, "target.repo")
	}
	return value, nil
}

// confirmRepoSegment is a name GitHub would have, in P6.3's shape: bounded, no
// `-` or `.` first, and no character that could be read as a path, a flag or a
// second repository.
func confirmRepoSegment(part string) bool {
	return part != "" && utf16Len(part) <= ConfirmRepoSegmentMax && confirmRepoSegmentPattern.MatchString(part)
}

// confirmHeadSHA validates the PR head read back at confirmation: 40 or 64
// lowercase hex characters, the two shapes git produces and GitHub returns.
func confirmHeadSHA(value string) (string, *Refusal) {
	if value == "" {
		return "", refuse(CodeMissingField, "target.headSha")
	}
	if (len(value) != 40 && len(value) != 64) || !hexPattern.MatchString(value) {
		return "", refuse(CodeConfirmBadHeadSHA, "target.headSha")
	}
	return value, nil
}

// confirmKeyID validates an id that can be a database key: non-empty, bounded,
// no key character, no lone surrogate. An empty string is an id the event did
// not carry (see this file's header: in Go the zero value is both).
func confirmKeyID(value, field string) (string, *Refusal) {
	if value == "" {
		return "", refuse(CodeMissingField, field)
	}
	if utf16Len(value) > ConfirmIDMax {
		return "", refuse(CodeTooLong, field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || forbiddenKeyChars[r] {
			return "", refuse(CodeBadKey, field)
		}
	}
	if confirmHasLoneSurrogate(value) {
		return "", refuse(CodeBadKey, field)
	}
	return value, nil
}

// confirmString validates a required, bounded, single-line string.
func confirmString(value, field string, max int) (string, *Refusal) {
	if value == "" {
		return "", refuse(CodeMissingField, field)
	}
	if utf16Len(value) > max {
		return "", refuse(CodeTooLong, field)
	}
	if bad, _ := confirmBadCharacter(value, false); bad != "" {
		return "", refuse(CodeBadCharacter, field)
	}
	return value, nil
}

// confirmBadCharacter answers the kind of character a string may not carry: a
// control character (line breaks included only where the caller allows them),
// DEL, a lone surrogate, or an invisible format character -- a zero-width space,
// a joiner, a bidi control, a BOM. A format character is refused rather than
// stripped, because what is shown must be what is judged: "con​firm" reads
// as "confirm" on a screen and as something else to this machine.
func confirmBadCharacter(value string, lineBreaks bool) (string, rune) {
	for i := 0; i < len(value); {
		if isWTF8Surrogate(value[i:]) {
			return "surrogate", 0
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size <= 1 {
			return "surrogate", 0
		}
		switch {
		case r == 0x7f:
			return "control", r
		case r < 0x20 && !(lineBreaks && (r == '\t' || r == '\n' || r == '\r')):
			return "control", r
		case unicode.Is(unicode.Cf, r):
			return "format", r
		}
		i += size
	}
	return "", 0
}

func confirmHasLoneSurrogate(value string) bool {
	for i := 0; i < len(value); {
		if isWTF8Surrogate(value[i:]) {
			return true
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size <= 1 {
			return true
		}
		i += size
	}
	return false
}

// confirmSeparator is `shared/command`'s WORD_SEPARATORS: whitespace and the
// punctuation a speaker pauses at.
func confirmSeparator(r rune) bool {
	switch r {
	case ',', ';', ':', '…', '–', '—':
		return true
	}
	return unicode.IsSpace(r)
}

// confirmWords is `shared/command`'s wordsOf: lower-case the utterance, split it
// on the separators, strip punctuation (category P) from both ends of each word
// and drop the empties.
func confirmWords(text string) []string {
	words := []string{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), confirmSeparator) {
		word = strings.TrimFunc(word, unicode.IsPunct)
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

// confirmCarriesToken reports whether a transcript carries the confirming phrase
// as one of its words. Words are split on whitespace **and punctuation and
// symbols**, because P6.1 strips punctuation from a word before matching it: a
// command ending "confirm." or "x,confirm" carries the phrase exactly as much as
// one ending "confirm". Splitting wider than the router is the safe direction
// here -- this check only ever refuses.
func confirmCarriesToken(text string) bool {
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}) {
		if word == ConfirmPhrase {
			return true
		}
	}
	return false
}

// confirmReadState reads a whole machine, refusing one that disagrees with
// itself: a phase that is not the summary of its pendings, a deadline outside
// the timeout bounds, a ledger out of canonical order.
func confirmReadState(state *ConfirmState) *Refusal {
	if state.Pending == nil || state.Spent == nil {
		return refuse(CodeConfirmInvalidPending, "pending")
	}
	switch state.Phase {
	case ConfirmPhaseIdle, ConfirmPhasePending, ConfirmPhaseCrowded:
	default:
		return refuse(CodeConfirmUnknownState, "phase")
	}
	if state.Now < 1 || state.Now > TimeMax {
		return refuse(CodeConfirmInvalidNow, "now")
	}
	if state.TimeoutMs < ConfirmMinTimeoutMs || state.TimeoutMs > ConfirmMaxTimeoutMs {
		return refuse(CodeConfirmInvalidTimeout, "timeoutMs")
	}
	if state.MaxPending < ConfirmMinMaxPending || state.MaxPending > ConfirmMaxMaxPending {
		return refuse(CodeConfirmInvalidMaxPending, "maxPending")
	}
	if state.MaxLedger < ConfirmMinMaxLedger || state.MaxLedger > ConfirmMaxMaxLedger {
		return refuse(CodeConfirmInvalidMaxLedger, "maxLedger")
	}
	// The session's policy is read here rather than at the point it is used, so
	// a state carrying a value this file cannot read is refused as a state and
	// not answered around.
	if refusal := confirmReadCostlyModes(state.CostlyModes); refusal != nil {
		return refusal
	}
	if len(state.Pending) > state.MaxPending {
		return refuse(CodeConfirmInvalidPending, "pending")
	}
	pending := make([]ConfirmPending, 0, len(state.Pending))
	for _, entry := range state.Pending {
		if !confirmContains(ConfirmPendingVerbs, entry.Verb) {
			return refuse(CodeConfirmInvalidPendingEntry, "pending")
		}
		if _, refusal := confirmKeyID(entry.ActionID, "pending"); refusal != nil {
			return refuse(CodeConfirmInvalidPendingEntry, "pending")
		}
		if _, refusal := confirmKeyID(entry.By, "pending"); refusal != nil {
			return refuse(CodeConfirmInvalidPendingEntry, "pending")
		}
		if entry.At < 1 || entry.At > TimeMax {
			return refuse(CodeConfirmInvalidPendingEntry, "pending")
		}
		if span := entry.Deadline - entry.At; span < ConfirmMinTimeoutMs || span > ConfirmMaxTimeoutMs {
			return refuse(CodeConfirmInvalidPendingEntry, "pending")
		}
		kinds, _ := confirmVerbTargetKinds[entry.Verb]
		target, refusal := confirmReadTarget(&entry.Target, kinds)
		if refusal != nil {
			return refuse(CodeConfirmInvalidPendingEntry, "pending")
		}
		entry.Target = target
		pending = append(pending, entry)
	}
	seen := map[string]bool{}
	for _, entry := range pending {
		if seen[entry.ActionID] {
			return refuse(CodeConfirmDuplicatePendingEntry, "pending")
		}
		seen[entry.ActionID] = true
	}
	if !sort.SliceIsSorted(pending, func(i, j int) bool { return confirmPendingLess(pending[i], pending[j]) }) {
		return refuse(CodeConfirmUnsortedPending, "pending")
	}
	if len(state.Spent) > state.MaxLedger {
		return refuse(CodeConfirmInvalidLedger, "ledger")
	}
	spent := make([]ConfirmSpent, 0, len(state.Spent))
	for _, entry := range state.Spent {
		if !confirmContains([]string{ConfirmLedgerAction, ConfirmLedgerConfirmation, ConfirmLedgerUtterance}, entry.Kind) {
			return refuse(CodeConfirmInvalidLedgerEntry, "ledger")
		}
		if _, refusal := confirmKeyID(entry.ID, "ledger"); refusal != nil {
			return refuse(CodeConfirmInvalidLedgerEntry, "ledger")
		}
		if entry.At < 1 || entry.At > TimeMax {
			return refuse(CodeConfirmInvalidLedgerEntry, "ledger")
		}
		spent = append(spent, entry)
	}
	seen = map[string]bool{}
	for _, entry := range spent {
		key := entry.Kind + "\x00" + entry.ID
		if seen[key] {
			return refuse(CodeConfirmDuplicateLedgerEntry, "ledger")
		}
		seen[key] = true
	}
	if !sort.SliceIsSorted(spent, func(i, j int) bool { return confirmSpentLess(spent[i], spent[j]) }) {
		return refuse(CodeConfirmUnsortedLedger, "ledger")
	}
	if state.Phase != confirmPhaseOf(pending) {
		return refuse(CodeConfirmInvalidState, "phase")
	}
	state.Pending, state.Spent = pending, spent
	return nil
}

// confirmBuildState freezes the seven fields a state has, in the one order they
// are read in.
func confirmBuildState(phase ConfirmPhase, now int64, from ConfirmState, pending []ConfirmPending, spent []ConfirmSpent) ConfirmState {
	if pending == nil {
		pending = []ConfirmPending{}
	}
	if spent == nil {
		spent = []ConfirmSpent{}
	}
	return ConfirmState{
		Phase: phase, Now: now, TimeoutMs: from.TimeoutMs,
		MaxPending: from.MaxPending, MaxLedger: from.MaxLedger,
		CostlyModes: from.CostlyModes, BiometricOnCostly: from.BiometricOnCostly,
		Pending: append([]ConfirmPending{}, pending...), Spent: append([]ConfirmSpent{}, spent...),
	}
}

// confirmSweep is the read-backs still inside their timeout. At the deadline
// exactly, one is still inside.
func confirmSweep(pending []ConfirmPending, now int64) []ConfirmPending {
	live := make([]ConfirmPending, 0, len(pending))
	for _, one := range pending {
		if now <= one.Deadline {
			live = append(live, one)
		}
	}
	return live
}

// confirmPhaseOf is the phase a set of read-backs implies.
func confirmPhaseOf(pending []ConfirmPending) ConfirmPhase {
	switch {
	case len(pending) == 0:
		return ConfirmPhaseIdle
	case len(pending) == 1:
		return ConfirmPhasePending
	default:
		return ConfirmPhaseCrowded
	}
}

// confirmOrderPending puts the read-backs in their canonical order: `at`, then
// `actionId`. The strings are compared by UTF-16 code unit, which is what
// JavaScript's `<` does and what makes two ends agree about the order.
func confirmOrderPending(entries []ConfirmPending) []ConfirmPending {
	out := append([]ConfirmPending{}, entries...)
	sort.SliceStable(out, func(i, j int) bool { return confirmPendingLess(out[i], out[j]) })
	return out
}

func confirmPendingLess(left, right ConfirmPending) bool {
	if left.At != right.At {
		return left.At < right.At
	}
	return utf16Less(left.ActionID, right.ActionID)
}

func confirmSpentLess(left, right ConfirmSpent) bool {
	if left.At != right.At {
		return left.At < right.At
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	return utf16Less(left.ID, right.ID)
}

// confirmSpend adds ledger records and answers the ledger in canonical order.
// The ledger is never truncated and never deduplicated: an id is in it or it is
// not, and making room by forgetting is exactly the replay this file refuses.
func confirmSpend(ledger []ConfirmSpent, entries ...ConfirmSpent) []ConfirmSpent {
	out := append(append([]ConfirmSpent{}, ledger...), entries...)
	sort.SliceStable(out, func(i, j int) bool { return confirmSpentLess(out[i], out[j]) })
	return out
}

// confirmHasSpent reports whether the ledger already holds an id of this kind.
func confirmHasSpent(state ConfirmState, kind, id string) bool {
	for _, entry := range state.Spent {
		if entry.Kind == kind && entry.ID == id {
			return true
		}
	}
	return false
}

// confirmSameTarget reports whether two resolved targets are the same target,
// field by field. A merge compares the head as well: a PR whose head moved since
// the read-back is not the target the speaker heard.
func confirmSameTarget(left, right ConfirmTarget) bool {
	if left.Kind != right.Kind {
		return false
	}
	switch left.Kind {
	case ConfirmTargetRun:
		return left.RunID == right.RunID
	case ConfirmTargetMode:
		// A start's target is its mode: a confirmation asserting another mode
		// heard a different read-back, so it authorises nothing (D2786, with
		// D47's Rule).
		return left.Mode == right.Mode
	case ConfirmTargetPR:
		return left.Repo == right.Repo && left.PR == right.PR
	default:
		return left.Repo == right.Repo && left.PR == right.PR && left.HeadSHA == right.HeadSHA
	}
}

func confirmContains(list []string, value string) bool {
	for _, one := range list {
		if one == value {
			return true
		}
	}
	return false
}

// confirmRepoSegmentPattern is `shared/confirm`'s REPO_SEGMENT_PATTERN, which
// P6.3 does not export and which `message.go`'s `repoPattern` states only for a
// whole `owner/name`.
var confirmRepoSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// ---------------------------------------------------------------------------
// The consumer's re-validation of an action record
// ---------------------------------------------------------------------------

// ConfirmActionOptions are what the consumer judges a record against: one
// authoritative clock's reading, the command window the record must be inside,
// and how far ahead of that clock a time may be.
type ConfirmActionOptions struct {
	Now               int64
	WindowMs          int64
	FutureToleranceMs int64
}

// ConfirmValidateAction re-reads one action record against `shared/confirm`'s
// rules, and answers the record the consumer may run, or the refusal that stops
// it. It is the consumer's whole allow-list (D128): a destructive command that
// does not come back from here with a record does not run.
//
// What it checks, and what it deliberately does not:
//
//   - the verb is one of the destructive three, and the target resolves to the
//     verb's own kind, read by the same reader the machine uses;
//   - the confirmation is present, its id is an id and its time is a time;
//   - the record is inside the **command window** measured from the
//     confirmation's own time, and that time is not further ahead of the
//     consumer's clock than the future tolerance (§8.1's five minutes);
//   - the confirmation **timeout** is not checked here: it is the phone's, and
//     the pending state it is measured against is phone-local (§3.5);
//   - whether the ids have been spent is the memory's answer, not this
//     function's: the caller asks ConfirmMemory.Spent, and it must ask
//     **before** it runs anything.
func ConfirmValidateAction(a *Action, o ConfirmActionOptions) (*ConfirmRecord, *Refusal) {
	if a == nil {
		return nil, refuse(CodeMissingField, "action")
	}
	if o.Now < 1 || o.Now > TimeMax {
		return nil, refuse(CodeConfirmInvalidNow, "now")
	}
	window := o.WindowMs
	if window == 0 {
		window = ConfirmWindowMs
	}
	tolerance := o.FutureToleranceMs
	if tolerance == 0 {
		tolerance = FutureTolerance.Milliseconds()
	}
	if !confirmContains(confirmVerbValues(), a.Verb) {
		return nil, refuse(CodeUnknownVerb, "action.verb")
	}
	if !confirmContains(ConfirmDestructiveVerbs, a.Verb) {
		// A launch is a mailbox action now (J5a, O10), but it is not this
		// function's: this is the destructive allow-list (D128), and O10's gate
		// -- ConfirmValidateLaunch -- is what reads a start against the
		// session's policy. A verb the ruling refuses is refused here, and a
		// record carrying one authorises nothing.
		return nil, refuse(CodeConfirmRefusedVerb, "action.verb")
	}
	var target ConfirmTarget
	if a.Target.IsRepo {
		target = ConfirmTarget{Kind: ConfirmTargetPR, Repo: a.Target.Repo, PR: a.Target.PR}
		if a.HasHeadSHA {
			target.Kind = ConfirmTargetMerge
			target.HeadSHA = a.HeadSHA
		}
	} else {
		target = ConfirmTarget{Kind: ConfirmTargetRun, RunID: a.Target.Run}
	}
	kinds, _ := confirmVerbTargetKinds[a.Verb]
	resolved, refusal := confirmReadTarget(&target, kinds)
	if refusal != nil {
		return nil, refusal
	}
	// The schema refuses a head SHA on anything but a merge; a record that
	// reached here carrying one on a cancel is refused rather than ignored.
	if a.HasHeadSHA && resolved.Kind != ConfirmTargetMerge {
		return nil, refuse(CodeFieldNotAllowed, "action.head_sha")
	}
	id, refusal := confirmKeyID(a.ID, "action.id")
	if refusal != nil {
		return nil, refusal
	}
	confirmationID, refusal := confirmKeyID(a.Confirmation.ID, "action.confirmation.id")
	if refusal != nil {
		return nil, refusal
	}
	at := a.Confirmation.At
	if at < 1 || at > TimeMax {
		return nil, refuse(CodeBadTimestamp, "action.confirmation.at")
	}
	if at-o.Now > tolerance {
		return nil, refuse(CodeFutureTimestamp, "action.confirmation.at")
	}
	if o.Now-at > window {
		// The record is older than the command window. A confirmation the owner
		// gave six hours ago authorises nothing now (D173): the window is
		// measured from the confirmation's own time, not from the message's.
		return nil, refuse(CodeReplay, "action.confirmation.at")
	}
	return &ConfirmRecord{
		ID: id, Verb: a.Verb, Target: resolved,
		Confirmation: ConfirmConfirmation{ID: confirmationID, At: at},
	}, nil
}

// ---------------------------------------------------------------------------
// O10's gate: the typed launch a consumer refuses when it skipped the read-back
// ---------------------------------------------------------------------------

// ConfirmLaunchOptions are what the gate judges a typed launch against: the
// modes the session calls costly, which is D2786's policy and the same list the
// confirm machine reads (C2's `costly_modes`). A nil list means P6's default
// (ConfirmDefaultCostlyModes), exactly as ConfirmOptions.CostlyModes does; a
// caller that means "no mode is costly" passes an empty, non-nil list.
type ConfirmLaunchOptions struct {
	CostlyModes []string
}

// ConfirmValidateLaunch is O10's gate, and the whole of what the typed launch
// adds to the consumer's checks: a start in a mode the session calls costly is
// refused when the record carries no confirmation. A start in any other mode
// passes through with no confirmation, which is D162's answer and stays it for
// every mode D2786 does not name.
//
// **Why it is a library check, and not a case in ConfirmValidateAction.** That
// function is the destructive allow-list (D128) and a launch runs nothing
// there. Wiring this gate into the running consumer -- reading the session's
// policy and starting the run the record asks for -- is the P5.1
// orchestrator's, so the check is exported and tested here, and nothing in this
// package calls it yet. The two functions answer each other's verbs the same
// way, and deliberately: each refuses a verb it does not authorise as
// `refused-verb`, so a caller that hands a record to the wrong one is refused
// rather than answered with a record that authorises nothing.
//
// **What it answers.** The record the consumer may start, or the refusal that
// stops it. A launch's record binds the mode the record named -- the same
// target kind J3's machine binds for a costly start (ConfirmTargetMode), so a
// caller has one shape to read whatever produced it. A costly launch's record
// carries the confirmation the phone wrote, for the caller to spend against its
// own memory before anything runs; a plain launch's carries none.
//
// **What it re-reads, and what it deliberately does not.** It re-reads the
// shape of what it hands back -- the action id, and the confirmation's id and
// time when the record carries one -- for the reason ConfirmValidateAction
// re-reads its own: a record built by hand inside this binary must not leave
// here looking spendable. What it does NOT read is the **command window**: the
// window is measured against the consumer's clock (D173's six hours from the
// confirmation's own time), this gate has no clock in its options, and the
// caller that owns the clock is the one that owns the window -- the same
// division ConfirmValidateAction draws around the phone's confirmation timeout.
// A caller that starts a costly launch must therefore spend the confirmation
// against its own memory AND its own window before anything runs.
//
// **The refusal a costly start without a confirmation gets** is `missing-field`
// at `action.confirmation`: it is the code the schema already answers a
// confirmation-less destructive record with, and D2786 makes a costly start's
// confirmation a requirement of exactly that kind -- one the session policy
// states and the schema cannot, because a schema has no session to read.
func ConfirmValidateLaunch(a *Action, o ConfirmLaunchOptions) (*ConfirmRecord, *Refusal) {
	if a == nil {
		return nil, refuse(CodeMissingField, "action")
	}
	if a.Verb != ConfirmVerbLaunch {
		// The gate answers one verb. A destructive record is
		// ConfirmValidateAction's, and a caller that hands one here has asked
		// the wrong question: refusing it is the fail-closed answer.
		return nil, refuse(CodeConfirmRefusedVerb, "action.verb")
	}
	costlyModes := o.CostlyModes
	if costlyModes == nil {
		costlyModes = ConfirmDefaultCostlyModes
	}
	if refusal := confirmReadCostlyModes(costlyModes); refusal != nil {
		// A policy this file cannot read is refused rather than read as "no mode
		// is costly": the lenient reading of a defect is exactly the reading
		// that lets a costly start through with no read-back.
		return nil, refusal
	}
	if confirmIsCostlyStart(a.Mode, costlyModes) && !a.HasConfirmation {
		// D2786: a costly start is read back like a cancel, so a record that
		// carries no confirmation is one the phone never read back -- a
		// modified client, or one that skipped the read-back. Refused rather
		// than started.
		return nil, refuse(CodeMissingField, "action.confirmation")
	}
	id, refusal := confirmKeyID(a.ID, "action.id")
	if refusal != nil {
		return nil, refusal
	}
	record := &ConfirmRecord{
		ID: id, Verb: ConfirmVerbLaunch,
		Target: ConfirmTarget{Kind: ConfirmTargetMode, Mode: a.Mode},
	}
	if a.HasConfirmation {
		confirmationID, refusal := confirmKeyID(a.Confirmation.ID, "action.confirmation.id")
		if refusal != nil {
			return nil, refusal
		}
		at := a.Confirmation.At
		if at < 1 || at > TimeMax {
			return nil, refuse(CodeBadTimestamp, "action.confirmation.at")
		}
		record.Confirmation = ConfirmConfirmation{ID: confirmationID, At: at}
	}
	return record, nil
}

// ---------------------------------------------------------------------------
// The memory: what the consumer has spent, and the prompts it is holding
// ---------------------------------------------------------------------------

// ConfirmPrompt is one prompt the consumer is holding: its id, when it was
// produced, the pairing it belongs to, whether it is destructive, and the
// non-destructive answer to hand back when it expires. It holds what
// `shared/prompts`' own entry holds (`{prompt, destructive, safe, announcedAt}`)
// — the same values, under **this file's own key names**, because the phone's
// module keeps the whole prompt message in `prompt` and the instant the phone
// spoke it in `announcedAt`, and a local record re-using those two names for its
// id and its production time would be a different shape under the same spelling.
// `mailbox` and `done` are the consumer's on top of that, because a pairing can
// be revoked and an expiry happens exactly once.
type ConfirmPrompt struct {
	ID          string `json:"id"`
	At          int64  `json:"at"`
	Mailbox     string `json:"mailbox"`
	Destructive bool   `json:"destructive,omitempty"`
	Safe        string `json:"safe,omitempty"`
	Done        bool   `json:"done,omitempty"`
}

// ConfirmExpiry is one prompt's expiry: what to hand back, and whether there is
// nothing to hand back at all.
type ConfirmExpiry struct {
	Prompt ConfirmPrompt
	// Stop is true when the prompt carried no non-destructive answer, so what
	// the orchestrator is told is to stop rather than to carry on with an answer
	// nobody gave.
	Stop  bool
	Reply string
}

// ConfirmMemoryOptions configure the memory. Path defaults to
// ConfirmMemoryPath(Project), and RetentionMs is required: the caller binds
// D118's TTL plus one sweep interval rather than this file declaring one.
//
// **One file holds both halves**, the queue and the ids spent, and it is named
// for what the plan names the consumer's own state beside the dedupe ledger
// (`pending.json`, §6): the ids this memory spends are ids *inside* a message —
// an action's, a confirmation's — so they are not entries of the delivery
// ledger, which is keyed by message id and whose format is P4.1's; the two are
// written by one atomic rename here rather than torn between two files.
type ConfirmMemoryOptions struct {
	Path        string
	Project     string
	RetentionMs int64
	MaxPrompts  int
}

type confirmMemoryFile struct {
	Now     int64           `json:"now"`
	Spent   []ConfirmSpent  `json:"spent"`
	Prompts []ConfirmPrompt `json:"prompts"`
}

// ConfirmMemory is the consumer's restart-surviving memory of the ids it has
// spent and the prompts it holds. It is not safe for concurrent use: the
// consumer is one process with one pass.
type ConfirmMemory struct {
	path  string
	opts  ConfirmMemoryOptions
	state confirmMemoryFile
}

// maxConfirmMemoryBytes bounds how much of the memory is read. It is derived
// from what this file can write rather than chosen: the spent ledger is bounded
// by a day of messages and the prompt list by the queue's cap, and both are far
// inside this.
const maxConfirmMemoryBytes = 4 << 20

// confirmTmpPattern is the memory's temporary file, inside the destination
// directory: a rename is atomic only within one filesystem.
const confirmTmpPattern = ".pending-*.tmp"

// ConfirmMemoryPath is the memory's default location, beside the dedupe ledger:
// `$XDG_STATE_HOME/fugaro/mailbox/<project>/pending.json`.
func ConfirmMemoryPath(project string) (string, error) {
	return confirmMemoryPath(os.Getenv, runtime.GOOS, project)
}

func confirmMemoryPath(getenv func(string) string, goos, project string) (string, error) {
	if project == "" {
		return "", errors.New("mailbox: a memory path needs a project name, and it is empty")
	}
	if !config.ProjectNameRE.MatchString(project) {
		return "", fmt.Errorf("mailbox: %q is not a project name, and the memory's directory is named after one", project)
	}
	base, err := localcfg.UserDir(getenv, goos, localcfg.DirState)
	if err != nil {
		return "", fmt.Errorf("mailbox: no state directory to keep the memory in: %w", err)
	}
	return filepath.Join(base, "fugaro", "mailbox", project, "pending.json"), nil
}

// OpenConfirmMemory opens the memory at o.Path (or ConfirmMemoryPath(o.Project))
// and reads it, or starts an empty one where no file exists. A file that is
// present but not a memory this consumer will trust is refused, never replaced
// by an empty one: forgetting what has been spent re-opens a replay.
func OpenConfirmMemory(o ConfirmMemoryOptions) (*ConfirmMemory, error) {
	if o.Path == "" {
		path, err := confirmMemoryPath(os.Getenv, runtime.GOOS, o.Project)
		if err != nil {
			return nil, err
		}
		o.Path = path
	}
	if o.RetentionMs <= 0 {
		return nil, errors.New("mailbox: a memory needs a retention window (D118's TTL plus one sweep interval); this command binds the number rather than declaring one")
	}
	if o.MaxPrompts == 0 {
		o.MaxPrompts = ConfirmMaxPrompts
	}
	if o.MaxPrompts < ConfirmMinMaxPending || o.MaxPrompts > ConfirmMaxMaxPending {
		return nil, fmt.Errorf("mailbox: the prompt cap is %d, outside %d..%d", o.MaxPrompts, ConfirmMinMaxPending, ConfirmMaxMaxPending)
	}
	m := &ConfirmMemory{path: o.Path, opts: o, state: confirmMemoryFile{Spent: []ConfirmSpent{}, Prompts: []ConfirmPrompt{}}}
	info, err := os.Stat(o.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return m, nil
	case err != nil:
		return nil, fmt.Errorf("mailbox: reading the consumer's memory at %s: %w", o.Path, cacheDir(err))
	case info.Size() > int64(maxConfirmMemoryBytes):
		return nil, refuse(CodeConfirmInvalidMemory, "file")
	}
	data, err := os.ReadFile(o.Path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: reading the consumer's memory at %s: %w", o.Path, cacheDir(err))
	}
	if refusal := m.parse(data); refusal != nil {
		return nil, refusal
	}
	return m, nil
}

func (m *ConfirmMemory) parse(data []byte) *Refusal {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file confirmMemoryFile
	if err := dec.Decode(&file); err != nil {
		return refuse(CodeConfirmInvalidMemory, "file")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return refuse(CodeConfirmInvalidMemory, "file")
	}
	if file.Now < 1 || file.Now > TimeMax {
		return refuse(CodeConfirmInvalidNow, "now")
	}
	if file.Spent == nil || file.Prompts == nil {
		return refuse(CodeConfirmInvalidMemory, "file")
	}
	seen := map[string]bool{}
	for _, entry := range file.Spent {
		if !confirmContains([]string{ConfirmLedgerAction, ConfirmLedgerConfirmation, ConfirmLedgerUtterance}, entry.Kind) {
			return refuse(CodeConfirmUnknownSpentKind, "spent")
		}
		if _, refusal := confirmKeyID(entry.ID, "spent"); refusal != nil {
			return refuse(CodeConfirmInvalidLedgerEntry, "spent")
		}
		if entry.At < 1 || entry.At > TimeMax {
			return refuse(CodeConfirmInvalidLedgerEntry, "spent")
		}
		key := entry.Kind + "\x00" + entry.ID
		if seen[key] {
			return refuse(CodeConfirmDuplicateLedgerEntry, "spent")
		}
		seen[key] = true
	}
	// The cap bounds the prompts **still to be answered**, which is what the
	// writer counts and what "queue full" means; a settled prompt is kept until
	// the retention window ends, because it is how an answer already handed back
	// is known to be handed back, so a memory that has settled its cap's worth is
	// the writer's own output and not a corruption (counting every entry here
	// would refuse the file the writer just wrote, and nothing else would repair
	// it).
	if livePrompts(file.Prompts) > m.opts.MaxPrompts {
		return refuse(CodeConfirmQueueFull, "prompts")
	}
	seen = map[string]bool{}
	for _, prompt := range file.Prompts {
		if _, refusal := confirmKeyID(prompt.ID, "prompts"); refusal != nil {
			return refuse(CodeConfirmInvalidMemory, "prompts")
		}
		if _, refusal := confirmKeyID(prompt.Mailbox, "prompts"); refusal != nil {
			return refuse(CodeConfirmInvalidMemory, "prompts")
		}
		if prompt.At < 1 || prompt.At > TimeMax {
			return refuse(CodeConfirmInvalidMemory, "prompts")
		}
		if seen[prompt.ID] {
			return refuse(CodeConfirmInvalidMemory, "prompts")
		}
		seen[prompt.ID] = true
	}
	m.state = file
	return nil
}

// Spent reports whether an id of this kind has already been spent. It is the
// answer a caller must have before it runs anything.
func (m *ConfirmMemory) Spent(kind, id string) bool {
	for _, entry := range m.state.Spent {
		if entry.Kind == kind && entry.ID == id {
			return true
		}
	}
	return false
}

// Spend records an id as spent, and answers false when it was already there --
// which is a replay, and nothing is written for it. The write happens **before**
// the caller runs anything, which is what makes a crash between the two
// re-perform nothing.
func (m *ConfirmMemory) Spend(kind, id string, now int64) (bool, error) {
	if !confirmContains([]string{ConfirmLedgerAction, ConfirmLedgerConfirmation, ConfirmLedgerUtterance}, kind) {
		return false, fmt.Errorf("mailbox: %q is not a kind of id this memory spends", kind)
	}
	if m.Spent(kind, id) {
		return false, nil
	}
	next := m.state
	next.Now = now
	next.Spent = append(append([]ConfirmSpent{}, next.Spent...), ConfirmSpent{Kind: kind, ID: id, At: now})
	next = m.prune(next, now)
	if err := m.commit(next); err != nil {
		return false, err
	}
	m.state = next
	return true, nil
}

// NotePrompt takes a prompt into the queue, and answers the refusal that says
// the queue is full (P6.5's own word for it: "queue full"). A prompt whose id is
// already known is not a second prompt and is not written again.
func (m *ConfirmMemory) NotePrompt(prompt ConfirmPrompt, now int64) (bool, *Refusal, error) {
	for _, known := range m.state.Prompts {
		if known.ID == prompt.ID {
			return false, nil, nil
		}
	}
	if livePrompts(m.state.Prompts)+1 > m.opts.MaxPrompts {
		return false, refuse(CodeConfirmQueueFull, "prompts"), nil
	}
	next := m.state
	next.Now = now
	next.Prompts = append(append([]ConfirmPrompt{}, next.Prompts...), prompt)
	next = m.prune(next, now)
	if err := m.commit(next); err != nil {
		return false, nil, err
	}
	m.state = next
	return true, nil, nil
}

// livePrompts is how many of these prompts are still to be answered: the cap's
// own count, in the reader and the writer alike, so the two cannot disagree
// about a file one of them wrote.
func livePrompts(prompts []ConfirmPrompt) int {
	live := 0
	for _, prompt := range prompts {
		if !prompt.Done {
			live++
		}
	}
	return live
}

// Prompts is the queue, oldest first: the prompts still to be answered, and the
// ones whose answer has already been handed back, so a caller can see both.
func (m *ConfirmMemory) Prompts() []ConfirmPrompt {
	out := append([]ConfirmPrompt{}, m.state.Prompts...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return utf16Less(out[i].ID, out[j].ID)
	})
	return out
}

// ExpirePrompts answers every prompt whose TTL has passed, exactly once each: at
// `at + ttlMs` the prompt's non-destructive answer is handed back, or -- when
// there is none -- a stop is. The consumer expires them on its own clock, so
// this does not depend on when the owner-run sweeper runs, and the memory is
// committed **before** the caller hands anything back, so a process killed in
// between does not deliver the same answer twice.
//
// `mailbox` narrows it to one pairing's prompts, and it is what a pass takes:
// the answers it hands back are reported per pairing, and a prompt of a pairing
// this pass is not serving is left for the pass that serves it. An empty
// `mailbox` is every prompt, which is what a sweeper or a diagnosis wants.
func (m *ConfirmMemory) ExpirePrompts(mailbox string, now, ttlMs int64) ([]ConfirmExpiry, error) {
	expired := []ConfirmExpiry{}
	for _, prompt := range m.state.Prompts {
		if prompt.Done || now-prompt.At < ttlMs {
			continue
		}
		if mailbox != "" && prompt.Mailbox != mailbox {
			continue
		}
		expired = append(expired, confirmExpiryOf(prompt))
	}
	if len(expired) == 0 {
		return nil, nil
	}
	next := m.state
	next.Now = now
	for i := range next.Prompts {
		for _, one := range expired {
			if next.Prompts[i].ID == one.Prompt.ID {
				next.Prompts[i].Done = true
			}
		}
	}
	next = m.prune(next, now)
	if err := m.commit(next); err != nil {
		return nil, err
	}
	m.state = next
	return expired, nil
}

// DropPrompts drops one pairing's prompts and answers the safe answer each one
// is owed, once: it is what a `fugaro pair revoke` leaves the consumer to do,
// because the pairing's key is gone and no answer to those prompts can be
// delivered or verified any more. The removal is committed before the caller
// hands anything back, so the answers are owed exactly once.
func (m *ConfirmMemory) DropPrompts(mailbox string, now int64) ([]ConfirmExpiry, error) {
	dropped := []ConfirmExpiry{}
	kept := make([]ConfirmPrompt, 0, len(m.state.Prompts))
	for _, prompt := range m.state.Prompts {
		if prompt.Mailbox == mailbox && !prompt.Done {
			dropped = append(dropped, confirmExpiryOf(prompt))
			continue
		}
		kept = append(kept, prompt)
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	next := m.state
	next.Now = now
	next.Prompts = kept
	next = m.prune(next, now)
	if err := m.commit(next); err != nil {
		return nil, err
	}
	m.state = next
	return dropped, nil
}

// Prompt answers the prompt one pairing is holding under an id, and whether it
// is still waiting. A prompt whose answer has been handed back is answered too,
// with `waiting` false: a second answer to it is a replay, and the caller has to
// tell that apart from an id it never held.
func (m *ConfirmMemory) Prompt(mailbox, id string) (ConfirmPrompt, bool, bool) {
	for _, prompt := range m.state.Prompts {
		if prompt.ID == id && prompt.Mailbox == mailbox {
			return prompt, !prompt.Done, true
		}
	}
	return ConfirmPrompt{}, false, false
}

// AnswerPrompt settles one prompt with the owner's own answer, and answers what
// the orchestrator is owed: the answer, once. The prompt is marked done and the
// memory is committed **before** the caller hands the answer back -- the same
// fence an expiry takes -- so a process killed in between does not answer the
// same question twice, and a second answer to a settled prompt answers nothing.
//
// A settled prompt is never answered twice, whether it was settled by the
// owner, by its TTL or by a revoke: `Done` is the one flag all three set, which
// is what makes "exactly once" true of the conversation rather than of each way
// it can end.
func (m *ConfirmMemory) AnswerPrompt(mailbox, id, answer string, now int64) (ConfirmExpiry, bool, error) {
	prompt, waiting, held := m.Prompt(mailbox, id)
	if !held || !waiting {
		return ConfirmExpiry{}, false, nil
	}
	next := m.state
	next.Now = now
	for i := range next.Prompts {
		if next.Prompts[i].ID == id && next.Prompts[i].Mailbox == mailbox {
			next.Prompts[i].Done = true
		}
	}
	next = m.prune(next, now)
	if err := m.commit(next); err != nil {
		return ConfirmExpiry{}, false, err
	}
	m.state = next
	return ConfirmExpiry{Prompt: prompt, Reply: answer}, true, nil
}

// confirmExpiryOf is what one prompt's expiry hands back: its non-destructive
// answer, or a stop when it has none. **A destructive prompt is not special**:
// what it carries as `Safe` is the non-destructive answer the orchestrator named
// when it asked, and handing that back is the whole of D168's default. The stop
// is for the prompt that named no answer at all (P6.5's `expiryOf`: `stop` is
// `safe === null`), never for one whose question was destructive.
func confirmExpiryOf(prompt ConfirmPrompt) ConfirmExpiry {
	if prompt.Safe == "" {
		return ConfirmExpiry{Prompt: prompt, Stop: true, Reply: ConfirmExpiredReply}
	}
	return ConfirmExpiry{Prompt: prompt, Reply: prompt.Safe}
}

// prune drops what the retention window allows and nothing else: a spent id
// older than the window (D118's TTL plus one sweep interval, so a replay cannot
// outlive the memory of it), and a prompt that has been answered and is older
// than the window. A prompt still to be answered is never dropped, however old:
// dropping it would lose the safe answer it is owed.
func (m *ConfirmMemory) prune(state confirmMemoryFile, now int64) confirmMemoryFile {
	cutoff := now - m.opts.RetentionMs
	spent := make([]ConfirmSpent, 0, len(state.Spent))
	for _, entry := range state.Spent {
		if entry.At >= cutoff {
			spent = append(spent, entry)
		}
	}
	prompts := make([]ConfirmPrompt, 0, len(state.Prompts))
	for _, prompt := range state.Prompts {
		if !prompt.Done || prompt.At >= cutoff {
			prompts = append(prompts, prompt)
		}
	}
	state.Spent, state.Prompts = spent, prompts
	return state
}

// Path is where the memory is: the operator's own state directory, and not a
// secret.
func (m *ConfirmMemory) Path() string { return m.path }

// commit writes the memory atomically: a temporary file in the destination
// directory, written, closed and chmodded `0o600`, then renamed over the memory,
// for the same two reasons the dedupe ledger's commit gives (a reader sees one
// whole memory or the other, and another account on the machine cannot read the
// ids of the operator's own messages).
func (m *ConfirmMemory) commit(state confirmMemoryFile) error {
	spent := state.Spent
	if spent == nil {
		spent = []ConfirmSpent{}
	}
	prompts := state.Prompts
	if prompts == nil {
		prompts = []ConfirmPrompt{}
	}
	body, err := json.Marshal(confirmMemoryFile{Now: state.Now, Spent: spent, Prompts: prompts})
	if err != nil {
		return fmt.Errorf("mailbox: writing the consumer's memory: %w", err)
	}
	// A memory past the read bound is one this command would refuse to open at its
	// next pass — leaving the operator a file to delete and the spent ids in it
	// gone — so the write is refused instead, and what is on disk stays readable.
	if len(body) > maxConfirmMemoryBytes {
		return fmt.Errorf("mailbox: the consumer's memory would be %d bytes, past the %d this command reads; nothing was written, so the memory on disk stands", len(body), maxConfirmMemoryBytes)
	}
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mailbox: making the memory's directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, confirmTmpPattern)
	if err != nil {
		return fmt.Errorf("mailbox: creating a temporary memory in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: writing the temporary memory: %w", cacheDir(err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: closing the temporary memory: %w", cacheDir(err))
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: setting the memory's mode: %w", cacheDir(err))
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: renaming the temporary memory over %s: %w", m.path, err)
	}
	return nil
}
