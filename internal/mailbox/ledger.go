package mailbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The ledger: the dedupe rule of `shared/mailbox` (P1.1), persisted (ruling
// M7).
//
// This is task T4 of docs/plans/2026-10-18-mobile-mailbox-consumer.md, and it
// is the half of the consumer that makes **at-most-once acting** true across a
// restart: the same `(sender, id)` is never a second delivery, and the record
// of a delivery is on disk **before** the action it asked for runs, so a
// process killed in between re-performs nothing when it comes back (§7's first
// ordering, M2).
//
// **The module is the rule, and this file is its Go binding.** `shared/mailbox`
// carries the state machine — the ledger's record shape, its canonical order,
// its cap and per-sender quota, the eviction horizon, the watermark — and Go
// cannot import it, so the rules are implemented here and held to the module by
// its own data (M9): TestLedgerConstantsMatchTheSharedModule reads the
// constants and the refusal vocabulary out of `shared/mailbox/index.js`, and
// T8's parity corpus replays the module's outcomes and codes through this
// package. Two rules this file does **not** restate: the command window is
// `shared/mailbox`'s `DEFAULT_WINDOW_MS` and the prompt's TTL is
// `shared/prompts`' `PROMPT_TTL_MS` (M3), both supplied by the caller
// (`LedgerOptions`) rather than declared as this command's own numbers.
//
// **What the file holds, and what it deliberately does not.** The ledger file
// carries the ledger (`seen`) and the window state (`now` and the eviction
// watermark `evictedThrough`). It does **not** carry the windows, the cap or
// the quota: those are the consumer's configuration, read from the modules, and
// a file that could declare its own window could widen what the ledger admits —
// a message the module's six-hour window calls a `replay` would be a fresh
// delivery under a file that says seven days. So the values the file must not
// own are not in the file's format at all (see `ledgerFile`), and a file whose
// watermark is not consistent with the caller's windows is refused rather than
// trusted (`invalid-evicted-through`, the module's own rule).
//
// **A ledger that cannot be read is refused, never replaced by an empty one.**
// That is deliberately not `internal/watch/acks.go`'s answer, whose corrupt
// file is the empty set of dismissals: an empty set of *dismissals* shows a
// viewer a run again, and an empty *ledger* would re-accept every id it holds
// and re-perform the action the operator's phone asked for once. Losing the
// dedupe memory is the whole failure this file exists to prevent, so a ledger
// that is present but unreadable, truncated or not a ledger is a refusal that
// stops the command rather than one it starts from.
//
// The write discipline **is** `acks.go`'s, and M7 takes exactly two things from
// it: the `$XDG_STATE_HOME/fugaro` prefix (with the platform rule under it,
// `localcfg.UserDir`) and tmp+rename at `0o600` in the destination directory.
// The `<project>` level, the document's shape and its contents are new here.

// The bounds `shared/mailbox` owns. They are declared here because Go cannot
// import the module, and TestLedgerConstantsMatchTheSharedModule reads them back
// out of `shared/mailbox/index.js`, so a change to either side is a failing test
// rather than a ledger the two ends disagree about (M9).
const (
	// DefaultWindowMs is the command window: how old a command may be and still
	// be a delivery rather than a replay (the module's DEFAULT_WINDOW_MS, six
	// hours, D173).
	DefaultWindowMs = 6 * 60 * 60 * 1_000
	// MinWindowMs and MaxWindowMs bound a caller's window. They are refused
	// rather than clamped: a window short enough to refuse ordinary latency, or
	// one so long the cap could never be reached without its help, is a
	// misconfiguration, not a number to quietly move.
	MinWindowMs = 1_000
	MaxWindowMs = 7 * 24 * 60 * 60 * 1_000
	// MaxPromptTtlMs is the ceiling on a prompt's TTL: a bound on the ledger's
	// memory, not a retention policy. The TTL itself is `shared/prompts`'
	// exported PROMPT_TTL_MS (M3), which is the caller's number.
	MaxPromptTtlMs = 30 * 24 * 60 * 60 * 1_000
	// DefaultMaxSeen is the dedupe ledger's cap: how many `(sender, id)`
	// records a mailbox holds. A full ledger is refused (`dedupe-full`), never
	// grown.
	DefaultMaxSeen = 1_024
	// MinMaxSeen and MaxMaxSeen bound a caller's cap.
	MinMaxSeen = 1
	MaxMaxSeen = 100_000
	// SenderShare is the divisor the per-sender quota is taken from:
	// DefaultMaxPerSender(maxSeen) is a quarter of the cap, at least one. It is
	// what keeps one sender from filling the ledger and locking every other
	// sender out with `dedupe-full` until a window passes.
	SenderShare = 4
	// MaxForwardJumpMs is how far one event's `now` may be ahead of the
	// ledger's own. `now` never goes backwards, so without this bound one
	// far-future `now` would freeze the mailbox until that time.
	MaxForwardJumpMs = 366 * 24 * 60 * 60 * 1_000
)

// TimeMax is the latest time the module reads on a `now` or an `at`: the
// lease's own ceiling (`shared/lease`'s MAX_TIMESTAMP), imported rather than
// restated so the two modules cannot drift. MaxTimestamp is that number, from
// lease.go.
const TimeMax = MaxTimestamp

// The two kinds of message the ledger judges by their own window. They are
// `shared/mailbox`'s KIND, which is *not* the schema's kind vocabulary: the
// schema says what a message is (`message`, `prompt`, `answer`, `action`), and
// the module says how long it is deliverable for (a `prompt`, or a `command` —
// everything else, which is the stricter of the two windows).
const (
	// KindCommand is the kind a delivery is judged under unless it is a prompt.
	KindCommand = "command"
	// KindPrompt is a queued prompt, judged by its own TTL.
	KindPrompt = "prompt"
)

// The statuses a ledger record may carry. A record is `accepted` when the
// delivery was taken, `acted` when the consumer has decided what to do about it
// — committed with the ledger before the action runs, which is what makes
// at-most-once true — and `acked` when the phone has spoken the reply, which is
// the only status that may ever be evicted.
const (
	StatusAccepted = "accepted"
	StatusActed    = "acted"
	StatusAcked    = "acked"
)

// The outcomes a step of the ledger answers with, spelled as
// `shared/mailbox`'s OUTCOME spells them. They are outcomes, never errors: a
// refused delivery is a recorded fact about a pass, not a failure to run.
const (
	// OutcomeAccepted: the delivery was taken and recorded.
	OutcomeAccepted = "accepted"
	// OutcomeDuplicate: the same `(sender, id)` — and the same `at` and kind —
	// is already in the ledger. It is answered, not acted on, and the ledger is
	// left exactly as it was.
	OutcomeDuplicate = "duplicate"
	// OutcomeActed: the oldest accepted record was acted on, and it is inside
	// its window.
	OutcomeActed = "acted"
	// OutcomeStalePending: the oldest accepted record was acted on, and its
	// window has already passed — the work is surfaced as late rather than
	// dropped. The consumer performs nothing for it (§7) and still answers it.
	OutcomeStalePending = "stale-pending"
	// OutcomeAcked: the oldest acted record is now acked.
	OutcomeAcked = "acked"
	// OutcomeNothingToAct, OutcomeNothingToAck: the question was asked and
	// there was nothing to answer. Not refusals: a consumer that asks again on
	// a redelivery is asking a question whose answer is this.
	OutcomeNothingToAct = "nothing-to-act"
	OutcomeNothingToAck = "nothing-to-ack"
)

// The refusal codes the ledger speaks beyond the schema's own (which are in
// Codes). They are `shared/mailbox`'s REFUSALS, spelled as the module spells
// them, plus one of this file's own — `invalid-ledger` — for the failures the
// module has no code for because it has no file: a ledger that is not a ledger
// at all. TestLedgerConstantsMatchTheSharedModule requires every code below
// that is the module's to be in the module's REFUSALS, so a reworded code on
// either side is a failing test rather than a protocol the two ends disagree
// about (M9).
//
// They are not in Codes, and that is deliberate: Codes is
// `shared/mailbox-schema`'s REFUSALS — the codes a *value* is refused with,
// which the two ends compare as sets — and these are the ledger's.
const (
	// CodeZeroTimestamp: an `at` of zero or less. The schema allows zero; the
	// ledger does not carry it, because a zero timestamp is the shape of an
	// unset counter rather than a time (D117).
	CodeZeroTimestamp Code = "zero-timestamp"
	// CodeReplay: a command older than the command window.
	CodeReplay Code = "replay"
	// CodeExpired: a prompt older than its TTL.
	CodeExpired Code = "expired"
	// CodeIDConflict: the pair is known, and this message's `at` or kind is not
	// the one the ledger holds. The id is the message's identity, so a second
	// message under it is an ambiguity, never a new delivery.
	CodeIDConflict Code = "id-conflict"
	// CodeDedupeFull: the ledger is at its cap. Nothing is evicted to make
	// room, because a record inside the horizon is never forgotten.
	CodeDedupeFull Code = "dedupe-full"
	// CodeSenderFull: the sender already holds its quota of records.
	CodeSenderFull Code = "sender-full"
	// CodeNowWentBackwards: this event's `now` is earlier than the ledger's own.
	CodeNowWentBackwards Code = "now-went-backwards"
	// CodeNowJumped: this event's `now` is further ahead of the ledger's own
	// than MaxForwardJumpMs.
	CodeNowJumped Code = "now-jumped"
	// CodeInvalidLedger: the file is not a ledger this consumer will trust —
	// it is not JSON, not an object, carries a field the format does not name,
	// or carries more than one document. This code is the file's own and is not
	// the module's.
	CodeInvalidLedger Code = "invalid-ledger"
	// CodeInvalidNow, CodeInvalidSeen, CodeInvalidSeenEntry,
	// CodeDuplicateSeenEntry, CodeUnsortedSeen, CodeInvalidEvictedThrough: the
	// stored state is not one the module would read — a `now` outside the
	// module's range, a ledger that is not an array or holds a record the
	// module refuses, two records for one pair, records out of canonical order,
	// or a watermark past what the windows allow.
	CodeInvalidNow            Code = "invalid-now"
	CodeInvalidSeen           Code = "invalid-seen"
	CodeInvalidSeenEntry      Code = "invalid-seen-entry"
	CodeDuplicateSeenEntry    Code = "duplicate-seen-entry"
	CodeUnsortedSeen          Code = "unsorted-seen"
	CodeInvalidEvictedThrough Code = "invalid-evicted-through"
)

// LedgerCodes is every code this file speaks that is the *module's*, so that
// TestLedgerConstantsMatchTheSharedModule can hold the two vocabularies to each
// other by set. CodeInvalidLedger is this file's own and is deliberately not
// here; CodeFutureTimestamp is the delivery's and is declared in mac.go beside
// the gate that raises it first.
var LedgerCodes = []Code{
	CodeZeroTimestamp, CodeReplay, CodeExpired, CodeIDConflict,
	CodeDedupeFull, CodeSenderFull, CodeNowWentBackwards, CodeNowJumped,
	CodeInvalidNow, CodeInvalidSeen, CodeInvalidSeenEntry,
	CodeDuplicateSeenEntry, CodeUnsortedSeen, CodeInvalidEvictedThrough,
}

// DefaultMaxPerSender is the module's `defaultMaxPerSender`: a quarter of the
// cap, at least one.
func DefaultMaxPerSender(maxSeen int) int {
	share := maxSeen / SenderShare
	if share < 1 {
		return 1
	}
	return share
}

// LedgerEntry is one delivery the ledger remembers: the module's frozen record
// `{ sender, id, at, kind, status }`, and never the message's text. A ledger
// that carried text would be a second copy of the mailbox, and the pairing's
// MAC (D113b) is what protects a message's contents, not this table.
type LedgerEntry struct {
	// Sender is the mailbox the message came from.
	Sender string `json:"sender"`
	// ID is the message's id. Together with Sender it is the ledger's key: the
	// same id from another sender is another message.
	ID string `json:"id"`
	// At is the message's own timestamp, in milliseconds.
	At int64 `json:"at"`
	// Kind is the kind the delivery was judged under, KindCommand or
	// KindPrompt.
	Kind string `json:"kind"`
	// Status is one of the three statuses above.
	Status string `json:"status"`
}

// ledgerState is the state the file carries: the ledger, the clock's floor and
// the eviction watermark. The rules that keep the watermark honest are
// `shared/mailbox`'s, and so is the name.
type ledgerState struct {
	now            int64
	evictedThrough int64
	seen           []LedgerEntry
}

// ledgerFile is the document on disk, and it is the whole format. The field set
// is the version: a file carrying anything else is refused
// (`invalid-ledger`), because a state from a later protocol must be refused
// rather than silently read with its new field dropped. The windows, the cap
// and the quota are deliberately **not** here (see this file's header).
type ledgerFile struct {
	Now            int64         `json:"now"`
	EvictedThrough int64         `json:"evictedThrough"`
	Seen           []LedgerEntry `json:"seen"`
}

// LedgerOptions configure a ledger. The zero value of everything but
// PromptTtlMs is the production setting.
type LedgerOptions struct {
	// Path is the ledger file. Empty means LedgerPath(Project).
	Path string
	// Project names the directory under the state directory when Path is empty.
	// It must be a Fugaro project name (config.ProjectNameRE), because it is a
	// path component here: a name carrying a separator would place the ledger
	// outside the state directory the operator expects.
	Project string
	// WindowMs is the command window. Zero means DefaultWindowMs.
	WindowMs int64
	// PromptTtlMs is the prompt's TTL, and it is **required**: it is
	// `shared/prompts`' PROMPT_TTL_MS (M3), which this command binds rather
	// than declares. A ledger built with no TTL is refused, because a default
	// here would be this file inventing D118's number.
	PromptTtlMs int64
	// MaxSeen is the ledger's cap. Zero means DefaultMaxSeen. It bounds
	// `seen` and nothing else; the parser reads at most maxLedgerBytes.
	MaxSeen int
	// MaxPerSender is one sender's quota. Zero means
	// DefaultMaxPerSender(MaxSeen).
	MaxPerSender int
	// FutureToleranceMs is how far ahead of `now` an `at` may be (§8.1's
	// fifth-minute bound). Zero means FutureTolerance.
	FutureToleranceMs int64
	// BeforeRename is called with the temporary file's path after it is
	// written, closed and chmodded, and before it is renamed over the ledger.
	// It exists for the tests: TestLedgerWriteIsAtomic inspects the directory
	// from inside a commit, which is the only place a reader could ever see a
	// half-written file. It is nil in production, and an error from it aborts
	// the commit with the destination untouched.
	BeforeRename func(tmpPath string) error
}

// maxLedgerBytes bounds how much of a ledger file OpenLedger reads. A full
// ledger is at most MaxMaxSeen records of three short fields — well under a
// megabyte — so anything past this is not a ledger, and reading it all into
// memory first would be the hostile input's choice rather than this package's.
const maxLedgerBytes = 4 << 20 // 4 MiB

// tmpPattern is the temporary file's name inside the destination directory: the
// rename that makes the write atomic is only atomic within one filesystem, so
// the temp file is created beside its destination and never in /tmp.
const tmpPattern = ".ledger-*.tmp"

// Ledger is the consumer's dedupe memory: `shared/mailbox`'s ledger, persisted
// at `$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json` (M7).
//
// It is not safe for concurrent use by several goroutines. The consumer is one
// process with one pass, which is what makes the store's ETag compare-and-swap
// the fence between consumers (M1) rather than a lock in this file.
type Ledger struct {
	path  string
	opts  LedgerOptions
	state ledgerState
}

// LedgerPath is the ledger's default location:
// `$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json`, under the state
// directory `localcfg.UserDir` names ($XDG_STATE_HOME where it is set and
// absolute, else %LOCALAPPDATA% on Windows, else ~/.local/state).
//
// Unlike `internal/watch/acks.go` — whose acks are best effort and which falls
// back to "." when no state directory is configured — a failure here is an
// error: a ledger in the process's working directory is a dedupe fence in a
// place nobody looks, and M7's `0o600` is a claim about a file the operator's
// own account owns.
func LedgerPath(project string) (string, error) {
	return ledgerPath(os.Getenv, runtime.GOOS, project)
}

// ledgerPath is LedgerPath with the platform as a parameter, so the Windows
// rule is testable from a Linux checkout — the same reason localcfg.UserDir and
// watch.ackPath take goos rather than reading runtime.GOOS.
func ledgerPath(getenv func(string) string, goos, project string) (string, error) {
	if project == "" {
		return "", errors.New("mailbox: a ledger path needs a project name, and it is empty")
	}
	if !config.ProjectNameRE.MatchString(project) {
		// The name is a path component here as well as a project name. One
		// rule, one place: the project name's own rule is config's, and
		// reusing it is what makes "the ledger is under fugaro/mailbox" true
		// for every name this command can be configured with.
		return "", fmt.Errorf("mailbox: %q is not a project name, and a ledger's directory is named after one", project)
	}
	base, err := localcfg.UserDir(getenv, goos, localcfg.DirState)
	if err != nil {
		return "", fmt.Errorf("mailbox: no state directory to keep the ledger in: %w", err)
	}
	return filepath.Join(base, "fugaro", "mailbox", project, "ledger.json"), nil
}

// OpenLedger opens the ledger at o.Path (or LedgerPath(o.Project)) and reads it,
// or starts an empty one where no file exists.
//
// A file that is present but not a ledger this consumer will trust is refused
// with a `*Refusal` — the code is in it, and errors.As reaches it — and the
// caller must stop rather than run a pass over an empty ledger (see this file's
// header: losing the dedupe memory re-performs work). A file that cannot be read
// at all (a permission, a directory in its place) is an ordinary error.
//
// The refusal is a value, not a panic, because it is a *state* this command can
// be in — a hand-edited file, a file from a later protocol, a truncated one
// left by something that was not this writer — and the command's answer to it
// is a message to the operator (exit 1), not a crash.
func OpenLedger(o LedgerOptions) (*Ledger, error) {
	if o.Path == "" {
		path, err := LedgerPath(o.Project)
		if err != nil {
			return nil, err
		}
		o.Path = path
	}
	if o.PromptTtlMs == 0 {
		return nil, errors.New("mailbox: a ledger needs the prompt TTL (shared/prompts' PROMPT_TTL_MS); this command binds the module's number rather than declaring one")
	}
	if o.WindowMs == 0 {
		o.WindowMs = DefaultWindowMs
	}
	if o.MaxSeen == 0 {
		o.MaxSeen = DefaultMaxSeen
	}
	if o.MaxPerSender == 0 {
		o.MaxPerSender = DefaultMaxPerSender(o.MaxSeen)
	}
	if o.FutureToleranceMs == 0 {
		o.FutureToleranceMs = FutureTolerance.Milliseconds()
	}
	if err := o.check(); err != nil {
		return nil, err
	}

	l := &Ledger{path: o.Path, opts: o}
	// The size is read before the file is: a ledger is at most MaxMaxSeen
	// records of three short fields, so a file past the bound is not a ledger,
	// and reading one all the way in first would be the hostile input's choice
	// rather than this package's (`acks.go` bounds its own the same way).
	info, err := os.Stat(o.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		l.state = ledgerState{seen: []LedgerEntry{}}
		return l, nil
	case err != nil:
		return nil, fmt.Errorf("mailbox: reading the ledger at %s: %w", o.Path, cacheDir(err))
	case info.Size() > maxLedgerBytes:
		return nil, refuse(CodeInvalidLedger, "file")
	}
	data, err := os.ReadFile(o.Path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: reading the ledger at %s: %w", o.Path, cacheDir(err))
	}
	if refusal := l.parse(data); refusal != nil {
		return nil, refusal
	}
	return l, nil
}

// check refuses options that are not a ledger's. They are programming and
// configuration errors — the shape of the command's own settings, not of a
// message — so they are ordinary errors rather than refusals with a code.
func (o LedgerOptions) check() error {
	switch {
	case o.WindowMs < MinWindowMs || o.WindowMs > MaxWindowMs:
		return fmt.Errorf("mailbox: the command window is %d ms, outside the module's %d..%d", o.WindowMs, MinWindowMs, MaxWindowMs)
	case o.PromptTtlMs < MinWindowMs || o.PromptTtlMs > MaxPromptTtlMs:
		return fmt.Errorf("mailbox: the prompt TTL is %d ms, outside the module's %d..%d", o.PromptTtlMs, MinWindowMs, MaxPromptTtlMs)
	case o.MaxSeen < MinMaxSeen || o.MaxSeen > MaxMaxSeen:
		return fmt.Errorf("mailbox: the ledger cap is %d, outside the module's %d..%d", o.MaxSeen, MinMaxSeen, MaxMaxSeen)
	case o.MaxPerSender < 1 || o.MaxPerSender > o.MaxSeen:
		return fmt.Errorf("mailbox: the per-sender quota is %d, outside 1..%d", o.MaxPerSender, o.MaxSeen)
	case o.FutureToleranceMs < 0 || o.FutureToleranceMs > 24*60*60*1_000:
		return fmt.Errorf("mailbox: the future tolerance is %d ms, outside the module's 0..86400000", o.FutureToleranceMs)
	}
	return nil
}

// parse reads the file into the ledger, or answers the refusal that stops the
// command. Every check here is `shared/mailbox`'s readState, field for field and
// in its order, minus the fields the file does not carry (the windows, the cap
// and the quota are the caller's, and the phase is derived rather than stored).
func (l *Ledger) parse(data []byte) *Refusal {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file ledgerFile
	if err := dec.Decode(&file); err != nil {
		// Not JSON, not an object, an unknown field, or a value of the wrong
		// type: the format is the version, and a file this reader cannot
		// account for is refused whole. Nothing here echoes what the file said
		// — a refusal can be logged.
		return refuse(CodeInvalidLedger, "file")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		// Two documents in one file: the second is not something this writer
		// ever produces, and reading the first of two is a guess.
		return refuse(CodeInvalidLedger, "file")
	}
	if file.Now < 1 || file.Now > TimeMax {
		return refuse(CodeInvalidNow, "now")
	}
	// The watermark moves only by horizon eviction, so it is never later than
	// `now - horizon`. A state that claims otherwise — because it was hand
	// edited, or because the windows were narrowed under it — is refused rather
	// than trusted: the watermark is the floor below which nothing is admitted
	// again, and a file that could raise it would be a file that could refuse
	// genuine deliveries.
	horizon := l.horizon()
	if file.EvictedThrough < 0 || file.EvictedThrough > max(0, file.Now-horizon) {
		return refuse(CodeInvalidEvictedThrough, "evictedThrough")
	}
	if file.Seen == nil {
		// A ledger with no ledger in it: `seen` is required, and an object
		// without it is not a state this writer produces. Reading it as empty
		// would be the dedupe hole this file's header refuses.
		return refuse(CodeInvalidSeen, "seen")
	}
	if len(file.Seen) > l.opts.MaxSeen {
		return refuse(CodeInvalidSeen, "seen")
	}
	pairs := make(map[string]bool, len(file.Seen))
	for i, entry := range file.Seen {
		if !validEntry(entry) {
			return refuse(CodeInvalidSeenEntry, "seen")
		}
		key := pairKey(entry.Sender, entry.ID)
		if pairs[key] {
			// Two records for one pair: the ledger cannot say whether a
			// redelivery is a duplicate.
			return refuse(CodeDuplicateSeenEntry, "seen")
		}
		pairs[key] = true
		if i > 0 && !entryLess(file.Seen[i-1], entry) {
			// The module's order is total and its entries are distinct, so
			// "not strictly after" is out of order — and a ledger that is not
			// in order cannot be searched.
			return refuse(CodeUnsortedSeen, "seen")
		}
	}
	l.state = ledgerState{now: file.Now, evictedThrough: file.EvictedThrough, seen: file.Seen}
	return nil
}

// validEntry is the module's readSeen entry rule: the five fields, each within
// the bound the schema already puts on the value it came from.
func validEntry(e LedgerEntry) bool {
	if e.Sender == "" || utf16Len(e.Sender) > SenderMax {
		return false
	}
	if e.ID == "" || utf16Len(e.ID) > IDMax {
		return false
	}
	if e.At < 1 || e.At > TimeMax {
		return false
	}
	if e.Kind != KindCommand && e.Kind != KindPrompt {
		return false
	}
	return e.Status == StatusAccepted || e.Status == StatusActed || e.Status == StatusAcked
}

// Entries is the ledger, in the module's canonical order: a copy, so a caller
// cannot reach past this package's fence by editing what it is handed.
func (l *Ledger) Entries() []LedgerEntry {
	out := make([]LedgerEntry, len(l.state.seen))
	copy(out, l.state.seen)
	return out
}

// Path is where this ledger is, which is not a secret: it is the operator's own
// state directory.
func (l *Ledger) Path() string { return l.path }

// horizon is the eviction horizon: the longest window any kind is accepted for,
// `max(windowMs, promptTtlMs)`. One horizon whatever the record's kind, so a
// kind the event claims cannot reopen a record that was evicted.
func (l *Ledger) horizon() int64 { return max(l.opts.WindowMs, l.opts.PromptTtlMs) }

// windowFor is the acceptance window of a kind.
func (l *Ledger) windowFor(kind string) int64 {
	if kind == KindPrompt {
		return l.opts.PromptTtlMs
	}
	return l.opts.WindowMs
}

// KindOf is the kind the ledger judges a message under: a prompt by its own
// TTL, and everything else as a command, which is the stricter of the two
// windows. It is the module's own default (`KIND.COMMAND`), and it exists so
// that the mapping from the schema's kind vocabulary to the module's is written
// once rather than per caller.
func KindOf(msg *Message) string {
	if msg != nil && msg.Kind == "prompt" {
		return KindPrompt
	}
	return KindCommand
}

// PerformResult is what a pass learned from handing one message to the ledger.
type PerformResult struct {
	// Outcome is OutcomeActed, OutcomeStalePending or OutcomeDuplicate, and it
	// is the outcome the pass reports. There is no "accepted": a delivery that
	// is taken is acted on in the same call, which is the fence below.
	Outcome string
	// Entry is the record the outcome is about: the one just written, or the
	// one the ledger already held.
	Entry LedgerEntry
	// Refusal is set when the message was refused. A refusal is an outcome of a
	// running pass, not a failure to run: the pass continues and the code is
	// recorded.
	Refusal *Refusal
}

// Perform is §7's ordering as one call, and that is the whole reason it exists:
// the delivery is decided, the record is written, the ledger is **committed**
// (tmp+rename, `0o600`), and only then — and only for OutcomeActed — is perform
// called. A process killed between the commit and the action re-performs
// nothing when it comes back, because the record on disk says the delivery was
// already acted on; a process killed between the decision and the commit has
// performed nothing yet, which is what makes the acting at-most-once rather
// than at-least-once (M1, M2).
//
// The commit is inside this call rather than the caller's because a fence a
// caller can forget to close is not a fence. The mutation this pins is the one
// T4 names: move the commit after perform and TestRestartDoesNotRePerform goes
// red with the action performed twice across the restart.
//
// Nothing is written for a duplicate or a refusal. That is the module's rule
// and it is load-bearing: a redelivery must not re-arm a delivery that was
// already acted on, and must not resurrect an acked one.
//
// The error is the ledger's own — an unreadable file, a directory that cannot
// be written — and it means **nothing was performed**: the in-memory ledger is
// rolled back to the state on disk, so a failed write cannot leave this process
// believing in a record that no restart would find.
func (l *Ledger) Perform(msg *Message, kind string, now int64, perform func(LedgerEntry)) (PerformResult, error) {
	state, result := l.deliver(l.state, msg, kind, now)
	if result.Refusal != nil {
		return PerformResult{Refusal: result.Refusal}, nil
	}
	if result.Outcome == OutcomeDuplicate {
		// A duplicate changes no record, and nothing is written — a redelivery
		// must not re-arm a delivery that was already acted on, and a loop of
		// them must not rewrite the file. The **floor** does move, and that is
		// the module's own answer: `answered` carries the event's `now` for a
		// duplicate as for any other outcome, so the events after it are judged
		// against this `now` — which is what makes this ledger's answers a
		// schedule's the same as the module's (M9), and is the stricter
		// direction for `now-went-backwards`.
		l.state.now = now
		return PerformResult{Outcome: OutcomeDuplicate, Entry: result.Entry}, nil
	}
	acted, actResult := l.act(state, now)
	if err := l.commit(acted); err != nil {
		return PerformResult{Outcome: actResult.Outcome, Entry: actResult.Entry}, err
	}
	l.state = acted
	if actResult.Outcome == OutcomeActed && perform != nil {
		perform(actResult.Entry)
	}
	return PerformResult{Outcome: actResult.Outcome, Entry: actResult.Entry}, nil
}

// Ack is the module's `ack` step: the phone has spoken the answer to which the
// oldest acted delivery led (M2), so its record becomes `acked` — the only
// status that may ever be evicted — and the ledger is committed.
//
// It is the caller's to decide when that is true (the phone's `acked` on its
// outbox message is the receipt, and reading it is T5's), and it is here
// because without it no record would ever be evictable and a full ledger could
// never recover.
func (l *Ledger) Ack(now int64) (PerformResult, error) {
	if refusal := l.readNow(l.state, now); refusal != nil {
		// A `now` that went backwards would write a state whose watermark is
		// later than its own clock allows — a ledger this package would then
		// refuse to read. The guard is here as well as in deliver because a
		// caller reaches this function without a delivery.
		return PerformResult{Refusal: refusal}, nil
	}
	state, result := l.ack(l.state, now)
	if err := l.commit(state); err != nil {
		return PerformResult{Outcome: result.Outcome}, err
	}
	l.state = state
	return result, nil
}

// deliver is the module's deliver step: what a delivery is, in memory. It
// answers the new state and what happened — a refusal, a duplicate, or an
// accepted record — and it writes nothing.
//
// The order of the checks is the rule, and it is `shared/mailbox`'s own:
// the message's own time first (the ledger's floor, then the ceiling, then the
// future bound, then the kind's window), then the ledger, then the watermark,
// then the cap, then the sender's quota.
func (l *Ledger) deliver(state ledgerState, msg *Message, kind string, now int64) (ledgerState, PerformResult) {
	if refusal := l.readNow(state, now); refusal != nil {
		return state, PerformResult{Refusal: refusal}
	}
	if kind != KindCommand && kind != KindPrompt {
		return state, PerformResult{Refusal: refuse(CodeUnknownKind, "kind")}
	}
	if msg == nil {
		// The gate hands this function a checked message and never a nil one
		// (§8.1: a refused message is not a message); a nil here is a caller
		// that skipped the gate, which is exactly what must not be acted on.
		return state, PerformResult{Refusal: refuse(CodeNotAnObject, "message")}
	}
	at := msg.At
	if at < 1 {
		// The schema allows `at: 0`; the ledger does not carry it, because a
		// zero timestamp is the shape of an unset counter rather than a time
		// (D117). Admitting it would be worse than refusing it: the delivery
		// would be recorded and then refused by this file's own reader.
		return state, PerformResult{Refusal: refuse(CodeZeroTimestamp, "message.at")}
	}
	if at > TimeMax || at-now > l.opts.FutureToleranceMs {
		// The ceiling is checked on its own rather than left to the tolerance:
		// with `now` at the ceiling an `at` inside the tolerance could pass it
		// and then be refused by this file's own reader.
		return state, PerformResult{Refusal: refuse(CodeFutureTimestamp, "message.at")}
	}
	if now-at > l.windowFor(kind) {
		// Two codes, because "replayed" and "waited too long" are different
		// facts for the consumer to report.
		return state, PerformResult{Refusal: refuse(refusalFor(kind), "message.at")}
	}

	// The ledger, consulted whatever the record's age. A pair it still holds is
	// never a new delivery: un-acked records are never evicted, so a stale
	// record for this pair is either work in flight — acting on the "new" one
	// would be the same id acted on twice — or an acked one not yet evicted.
	// The kind is part of the identity: the same pair and `at` under another
	// kind is a second message, not a duplicate.
	if index, known := findEntry(state.seen, msg.Sender, msg.ID); known {
		entry := state.seen[index]
		if entry.At != at || entry.Kind != kind {
			return state, PerformResult{Refusal: refuse(CodeIDConflict, "message.at")}
		}
		return state, PerformResult{Outcome: OutcomeDuplicate, Entry: entry}
	}

	// The watermark: nothing at or before the `at` of a record this ledger has
	// ever forgotten is admitted again, whatever kind the event claims. It is
	// checked after the lookup, so a held record is still answered as a
	// duplicate or a conflict.
	if at <= state.evictedThrough {
		return state, PerformResult{Refusal: refuse(refusalFor(kind), "message.at")}
	}

	kept, through := l.evict(state.seen, state.evictedThrough, now)
	if len(kept) >= l.opts.MaxSeen {
		// No room, and nothing is dropped to make some: a record inside the
		// horizon is never forgotten, so the same `(sender, id)` is never acted
		// on twice inside it. Refusing is the only answer that keeps that
		// promise.
		return state, PerformResult{Refusal: refuse(CodeDedupeFull, "seen")}
	}
	if senderCount(kept, msg.Sender) >= l.opts.MaxPerSender {
		// The quota counts every record the sender holds — accepted, acted or
		// acked — so one sender can fill only its own share. Refused, never
		// evicted: the quota is a fence, not a reason to forget.
		return state, PerformResult{Refusal: refuse(CodeSenderFull, "message.sender")}
	}
	entry := LedgerEntry{Sender: msg.Sender, ID: msg.ID, At: at, Kind: kind, Status: StatusAccepted}
	next := ledgerState{now: now, evictedThrough: through, seen: insertEntry(kept, entry)}
	return next, PerformResult{Outcome: OutcomeAccepted, Entry: entry}
}

// act is the module's `act` step: the oldest accepted record becomes acted, and
// the outcome says whether its window had already passed. Nothing is dropped
// for being late — the work is surfaced rather than lost — and the consumer
// performs nothing for a `stale-pending` one (§7), though it still answers it.
func (l *Ledger) act(state ledgerState, now int64) (ledgerState, PerformResult) {
	index := statusIndex(state.seen, StatusAccepted)
	if index < 0 {
		// Nothing to act on. The module's answer carries the event's `now` all
		// the same (`answered`), so the floor moves even when no record does —
		// this is a state this caller persists either way.
		return ledgerState{now: now, evictedThrough: state.evictedThrough, seen: state.seen}, PerformResult{Outcome: OutcomeNothingToAct}
	}
	target := state.seen[index]
	outcome := OutcomeActed
	if now-target.At > l.windowFor(target.Kind) {
		outcome = OutcomeStalePending
	}
	return move(state, index, StatusActed, now), PerformResult{Outcome: outcome, Entry: target}
}

// ack is the module's `ack` step, over the oldest acted record.
func (l *Ledger) ack(state ledgerState, now int64) (ledgerState, PerformResult) {
	index := statusIndex(state.seen, StatusActed)
	if index < 0 {
		// Nothing to ack, and the floor moves as it does for act: the module's
		// `answered` carries the event's `now` whatever the outcome.
		return ledgerState{now: now, evictedThrough: state.evictedThrough, seen: state.seen}, PerformResult{Outcome: OutcomeNothingToAck}
	}
	target := state.seen[index]
	return move(state, index, StatusAcked, now), PerformResult{Outcome: OutcomeAcked, Entry: target}
}

// readNow is the two `now` guards the module applies before any step: a `now`
// earlier than the ledger's own would make the window run in reverse — and with
// it "outside the window" would mean nothing, which is what the eviction
// argument rests on — and one further ahead than MaxForwardJumpMs would refuse
// every later honest `now` for as long as it claimed.
func (l *Ledger) readNow(state ledgerState, now int64) *Refusal {
	if now < 1 || now > TimeMax {
		return refuse(CodeInvalidNow, "now")
	}
	if state.now == 0 {
		// A ledger that has never been written has no clock of its own yet.
		return nil
	}
	if now < state.now {
		return refuse(CodeNowWentBackwards, "now")
	}
	if now-state.now > MaxForwardJumpMs {
		return refuse(CodeNowJumped, "now")
	}
	return nil
}

// evict drops the records that may be dropped and answers the survivors with
// the new watermark. A record is dropped only when it is **acked** — the
// delivery is finished, so forgetting it cannot lose work — and **strictly**
// older than the horizon. Every eviction raises the watermark to the evicted
// record's `at`, which is the only thing that moves it, so it is never later
// than `now - horizon`.
//
// Nothing that is not acked is ever dropped, at any age: an accepted record is
// work not yet done, and an acted one is work this consumer still owes an
// answer for. Dropping either would lose the work or make room for a second
// action.
func (l *Ledger) evict(seen []LedgerEntry, through, now int64) ([]LedgerEntry, int64) {
	horizon := l.horizon()
	kept := make([]LedgerEntry, 0, len(seen))
	for _, entry := range seen {
		if entry.Status != StatusAcked || now-entry.At <= horizon {
			kept = append(kept, entry)
			continue
		}
		through = max(through, entry.At)
	}
	return kept, through
}

// refusalFor is the code a message too old for its window earns: `expired` for
// a prompt, `replay` for a command.
func refusalFor(kind string) Code {
	if kind == KindPrompt {
		return CodeExpired
	}
	return CodeReplay
}

// pairKey is the ledger's key for a delivery: `(sender, id)`, and not the id
// alone. The length prefix is the module's own spelling, and it is what keeps
// one pair from colliding with another ("a" + "bc" against "ab" + "c").
func pairKey(sender, id string) string {
	return fmt.Sprintf("%d:%s%s", len(sender), sender, id)
}

// entryLess is the module's canonical order: `at`, then `sender`, then `id`,
// compared by code point (which is byte order, the order `sortedKeys` uses in
// message.go). `at` first because it is the order the mailbox would have
// learned the messages in had the network been kind; the other two only to make
// the order total, so two messages with one `at` cannot leave two different
// states depending on which arrived first.
func entryLess(left, right LedgerEntry) bool {
	if left.At != right.At {
		return left.At < right.At
	}
	if left.Sender != right.Sender {
		return left.Sender < right.Sender
	}
	return left.ID < right.ID
}

// insertEntry keeps the ledger in its canonical order, without a sort: a
// delivery is one record into a list that is already ordered, and insertion is
// what keeps the invariant from depending on a comparison being total (it is).
func insertEntry(entries []LedgerEntry, entry LedgerEntry) []LedgerEntry {
	index := 0
	for index < len(entries) && entryLess(entries[index], entry) {
		index++
	}
	out := make([]LedgerEntry, 0, len(entries)+1)
	out = append(out, entries[:index]...)
	out = append(out, entry)
	out = append(out, entries[index:]...)
	return out
}

// findEntry is the ledger lookup, by the pair alone.
func findEntry(entries []LedgerEntry, sender, id string) (int, bool) {
	for i, entry := range entries {
		if entry.Sender == sender && entry.ID == id {
			return i, true
		}
	}
	return -1, false
}

// statusIndex is the oldest record with the given status, or -1. "Oldest" is
// the canonical order, so two deliveries in either order are acted on in the
// same sequence.
func statusIndex(entries []LedgerEntry, status string) int {
	for i, entry := range entries {
		if entry.Status == status {
			return i
		}
	}
	return -1
}

// senderCount is how many records a sender holds, whatever their status: the
// quota counts accepted, acted and acked alike, so a sender cannot hold one
// share of un-acked work and another of finished work.
func senderCount(entries []LedgerEntry, sender string) int {
	count := 0
	for _, entry := range entries {
		if entry.Sender == sender {
			count++
		}
	}
	return count
}

// move is a status change on one record, with the state's `now` advanced. The
// order is preserved: no record moves.
func move(state ledgerState, index int, status string, now int64) ledgerState {
	seen := make([]LedgerEntry, len(state.seen))
	copy(seen, state.seen)
	seen[index].Status = status
	return ledgerState{now: now, evictedThrough: state.evictedThrough, seen: seen}
}

// commit writes the state, atomically: a temporary file in the destination
// directory, written, closed and chmodded `0o600`, then renamed over the
// ledger. A reader therefore sees either the previous ledger or this one, and
// never a half-written file — which for this file means never an empty one.
//
// The mode matters as much as the rename: the ledger lists the ids of the
// operator's phone messages, and `0o600` is what keeps another account on the
// machine from reading it (M7).
func (l *Ledger) commit(state ledgerState) error {
	seen := state.seen
	if seen == nil {
		seen = []LedgerEntry{}
	}
	body, err := json.Marshal(ledgerFile{Now: state.now, EvictedThrough: state.evictedThrough, Seen: seen})
	if err != nil {
		return fmt.Errorf("mailbox: writing the ledger: %w", err)
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mailbox: making the ledger's directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("mailbox: creating a temporary ledger in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: writing the temporary ledger: %w", cacheDir(err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: closing the temporary ledger: %w", cacheDir(err))
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: setting the ledger's mode: %w", cacheDir(err))
	}
	if l.opts.BeforeRename != nil {
		if err := l.opts.BeforeRename(tmpPath); err != nil {
			os.Remove(tmpPath)
			return err
		}
	}
	if err := os.Rename(tmpPath, l.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mailbox: renaming the temporary ledger over %s: %w", l.path, err)
	}
	return nil
}

// cacheDir drops a *PathError's path from an error this package returns. The
// message is the same either way and the path is the operator's own state
// directory, but a wrapped PathError prints the operation and the temporary
// file's name as well, and the caller adds the ledger's path once, where it is
// known.
func cacheDir(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
