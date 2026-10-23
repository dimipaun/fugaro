package mailbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The MAC (§8): the tag every inbox message is verified with, and — T2a's half
// of the same function — the tag every outbound write is signed with.
//
// This is task T2 of docs/plans/2026-10-18-mobile-mailbox-consumer.md, and it
// lands §8.1's inbound gate: the check that stands between a message that
// arrived and anything acting on it. §8.4 splits the ownership with P6.7 —
// P4.1 wires the gate, P6.7 owns it as enforcement — and P4.1 must close it
// regardless, because "the consumer **never** runs a destructive command from a
// voice-originated message" is unenforceable without refusing what it cannot
// attribute (ruling M4a).
//
// **The tag is HMAC-SHA-256 over CanonicalBytes, in the one shape the module
// computes** (D171, §3.5): `shared/mailbox-schema/mac.js`'s `macOf`, over
// `canonicalBytes(record, direction)`, lowercase hex. That is not a second
// implementation to be kept in step: TestTheVectorsReplayThroughTheMACFunction
// replays every case and every direction pair in `vectors.json` through the
// functions below, so the covered field set, the serialisation and the tag are
// the file's, case by case (M9).
//
// **What is covered is `canonicalBytes`'s own set**: every field except `mac`
// and `acked`, plus the direction. `mac` is out because a tag cannot cover
// itself, `acked` is out because the phone writes the receipt after the tag was
// made (so a receipt cannot invalidate the message it acknowledges), and the
// direction is in because a tag made for one node must not verify in another —
// it is an argument, supplied by the reader, never a field of the record.
//
// **The key is never here.** It arrives as an argument — the phone's from
// secure storage, the consumer's from the OS keychain (M5, internal/mailbox/
// keychain) — and leaves as a digest. Nothing in this file reads, stores,
// derives, logs or defaults one, and the gate holds the keychain's Key rather
// than its bytes so that the one value that lives for the life of the process
// is the one that cannot be printed.

// FutureTolerance is §8.1's fourth check: a message whose `at` is further ahead
// of the reader's clock than this is refused, because the server's timestamp is
// the authority (D117) and a writer whose clock is far ahead is a writer whose
// `at` cannot be trusted. Five minutes is `MAX_CLOCK_SKEW`
// (`shared/lease/index.js`), which `shared/mailbox`'s
// `DEFAULT_FUTURE_TOLERANCE_MS` imports rather than restates;
// TestFutureToleranceMatchesTheModules reads both numbers out of the modules, so
// a change on either side is a failing test here rather than a tolerance the two
// ends disagree about (M9).
//
// The bound is not decoration: a future `at` is inside the window by definition
// (its age is negative), so an unbounded future timestamp would be a message no
// window could ever evict. The comparison is the module's own — one millisecond
// beyond the tolerance is refused, the tolerance itself is not
// (`at - now > futureToleranceMs`, shared/mailbox's delivery).
const FutureTolerance = 5 * time.Minute

// CodeFutureTimestamp is the refusal a message's `at` earns when it is too far
// ahead (D117, §8.1.4). It is `shared/mailbox`'s `REFUSAL.FUTURE_TIMESTAMP`,
// read back out of index.js by TestFutureToleranceMatchesTheModules.
//
// It is not in Codes, and that is deliberate: Codes is `shared/mailbox-schema`'s
// REFUSALS — the codes a *value* is refused with, which the two ends compare as
// sets — and this one is the delivery's, refused by a reader that has a clock.
// CodeBadMAC is the other way round: the schema owns the code for a tag that is
// not a tag, and a tag that does not verify is refused with it (§7's loop line
// names `bad-mac` beside `future-timestamp`).
const CodeFutureTimestamp Code = "future-timestamp"

// directionInbox is the node the inbound gate reads. The direction is supplied
// by the reader and never taken from the record (§8.1): a record carrying a
// `direction` of its own is refused by the schema and throws in canonicalBytes.
const directionInbox = "inbox"

// MacOf is the tag a record carries: HMAC-SHA-256 over
// CanonicalBytes(record, direction), lowercase hex (§3.5, D171). It is
// `mac.js`'s `macOf`, and it is both halves of the protocol — §8.2's sign and
// §8.1's verify are one function over one canonical form, differing only in the
// direction argument. (T2a lands the write paths that call it; T2 lands the
// gate below.)
//
// An empty key is refused rather than used: HMAC with no key is a tag anyone
// can compute, and a caller that reached here with no key has a bug worth
// hearing about (the consumer reads the key once, at startup, and refuses to
// start without one — M4, §8.3). A record the canonical serialisation refuses —
// `null` in it, a number that is not a safe integer, a string the two languages
// escape differently — is an error, not a tag: signing bytes a reader will not
// see is the failure mode the canonical form exists to prevent.
func MacOf(record Record, key []byte, direction string) (string, error) {
	if len(key) == 0 {
		return "", errors.New("mailbox: the MAC key is empty, and a tag computed with no key is a tag anyone can compute")
	}
	canonical, err := CanonicalBytes(record, direction)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifyMAC decides whether record carries the tag its own bytes produce under
// key, in the direction the reader read it from.
//
// It answers `false` for everything that cannot carry a tag — a record with no
// `mac`, a `mac` that is not a string, a tag of the wrong length — and an error
// only for a record CanonicalBytes refuses, which is a record no tag could have
// been computed over. `mac.js`'s `verifyMac` answers the same `false` and
// swallows the throw; the two are held to the same vectors.
//
// The comparison is timing-safe, because a caller that verifies tags an
// attacker can choose would otherwise leak the expected tag one byte at a time
// — and the expected tag is a function of the key.
func VerifyMAC(record Record, key []byte, direction string) (bool, error) {
	want, err := MacOf(record, key, direction)
	if err != nil {
		return false, err
	}
	got, ok := record["mac"].(string)
	if !ok {
		// No tag, or a tag that is not a string: nothing to compare. The
		// schema refuses both shapes before a caller reaches here.
		return false, nil
	}
	if len(got) != len(want) {
		return false, nil
	}
	return hmac.Equal([]byte(got), []byte(want)), nil
}

// Gate is the inbound gate: the pairing's key, and §8.1's order, applied to
// every message the consumer reads out of the inbox.
//
// It holds the keychain's Key rather than its bytes, so that the value which
// lives for the life of the process is the value that cannot be printed: a `%v`
// of a Gate — in a log line, in a `--json` document, in a test's t.Logf —
// answers `[REDACTED]` for the key and never a byte of it. The bytes are asked
// for once per verification, by the one function that computes with them.
//
// A Gate is not safe for concurrent use by several goroutines only in the sense
// that nothing here mutates it: it is read-only after NewGate, and Verify may
// be called from anywhere.
type Gate struct {
	key keychain.Key
	now func() time.Time
	tol time.Duration
}

// String is what a log line about the gate says: what it is, and never a byte
// of the key. It is belt and braces over the Key's own redaction — the verbs
// that reach a struct's fields are already redacted by it — and it is the
// answer to the one shape they cannot cover: `%s` of the gate itself.
func (g *Gate) String() string {
	if g == nil {
		return "mailbox.Gate(nil)"
	}
	return fmt.Sprintf("mailbox.Gate{future tolerance: %s, key: %s}", g.tol, keychain.Redacted)
}

// GateOptions tune a Gate; the zero value is the production setting.
type GateOptions struct {
	// Now is the clock the future tolerance is measured against; nil is
	// time.Now. It is injected because the tolerance's boundary is one
	// millisecond and a test that waited for it would be a slow test.
	Now func() time.Time
	// FutureTolerance is FutureTolerance when zero.
	FutureTolerance time.Duration
}

// NewGate returns the gate a pass verifies its inbox with. The key is the
// pairing's (D171), read once at startup (M5) — the caller reads it, this does
// not, and a gate built with no key refuses every message rather than
// accepting one.
func NewGate(key keychain.Key, o GateOptions) *Gate {
	g := &Gate{key: key, now: o.Now, tol: o.FutureTolerance}
	if g.now == nil {
		g.now = func() time.Time { return time.Now().UTC() }
	}
	if g.tol <= 0 {
		g.tol = FutureTolerance
	}
	return g
}

// Verify is §8.1, in §8.1's order: the record is what the schema says a message
// is, its tag is its own tag under the pairing's key and the inbox direction,
// and its `at` is not further ahead of this clock than FutureTolerance. It
// answers the checked message, or the refusal — never an error that stops the
// loop, and never a fallback to acting.
//
// **A refused message is not a message.** Whenever the Refusal is not nil the
// Message is nil, so a caller cannot hand the ledger a value this function
// refused, because there is none to hand: §8.1's "never a retry that never
// ends" is true of the pass, and the message that was refused is still in the
// inbox on the next one (M4a, §7's ordering 2 — the re-read is the cost, and it
// is stated there and in §16).
//
// It refuses more than a forger's message: a record the schema refuses is
// refused with the schema's own code, so a message that is merely malformed is
// told apart from one that is not attributable. It refuses less than the
// module's delivery, deliberately — `zero-timestamp`, `replay`, `expired` and
// the ledger's window are the delivery's (T4), and they are decided by a
// mailbox that has a ledger to consult; this is the attribution half, and a
// message that passes here is still refused by those rules.
func (g *Gate) Verify(record Record) (*Message, *Refusal) {
	// A nil gate has no key, and a gate with no key refuses everything: the
	// answer is the one M4's availability row gives, not a panic in a loop
	// that must not stop (§8.1.5).
	if g == nil {
		return nil, refuse(CodeBadMAC, "mac")
	}
	message, refusal := ValidateMessage(record)
	if refusal != nil {
		return nil, refusal
	}
	// The key is asked for here, per verification, and the copy it answers is
	// the only one that exists: nothing else in this package holds the
	// material, so there is nothing else to leak.
	if ok, err := VerifyMAC(record, g.key.Bytes(), directionInbox); err != nil || !ok {
		return nil, refuse(CodeBadMAC, "mac")
	}
	if at := message.At; at-g.now().UnixMilli() > g.tol.Milliseconds() {
		return nil, refuse(CodeFutureTimestamp, "at")
	}
	return message, nil
}
