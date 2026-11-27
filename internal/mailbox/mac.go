package mailbox

import (
	"context"
	"crypto/ed25519"
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
// This is task T2 of docs/mobile/mailbox-consumer-plan.md, and it
// lands §8.1's inbound gate: the check that stands between a message that
// arrived and anything acting on it. **T2a extends this file with §8.2's other
// half** — the `Signer` below, and the write paths that call it — so that
// `sign` and `verify` are one function and one covered field set rather than two
// implementations to be kept in step (§13's "one deliberate two-writer file").
// §8.4 splits the ownership with P6.7 —
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

// directionInbox is the node the inbound gate reads, and directionOutbox and
// directionHeartbeat are the two the consumer writes (§6, §8.2). The direction
// is supplied by the reader or the writer and never taken from the record
// (§8.1): a record carrying a `direction` of its own is refused by the schema
// and throws in canonicalBytes.
const (
	directionInbox     = "inbox"
	directionOutbox    = "outbox"
	directionHeartbeat = "heartbeat"
)

// The two record fields a tag lands in (J5r): `mac` is D171's HMAC and `sig` is
// O6's device signature. **Which one a value carries is its signer's and never
// the caller's** — a writer that could choose per value could downgrade itself,
// and the schema accepts exactly one of the two (message.go's tagFields) — so
// these names are read off the signer (`Field`) and never written by a caller.
const (
	tagFieldMAC = "mac"
	tagFieldSig = "sig"
)

// TagFields is every field a tag can land in, in the module's own order. It is
// the closed vocabulary J5r's rules are written against: a record carries
// exactly one of these and nothing else, and the two are `mac` and `sig` on
// every node the mailbox has.
var TagFields = []string{tagFieldMAC, tagFieldSig}

// unsignedRefusal is the refusal a value that carries no tag earns: the schema's
// code and the field of the scheme its signer writes in — `bad-sig` at `sig` for
// O6's signature, `bad-mac` at `mac` for D171's HMAC (J5r). A refusal that named
// the other scheme's field would point an operator at a field their value never
// had. A signer that named no field at all is answered as the MAC, which is the
// field the schema refuses a record carrying neither tag at (message.go's
// tagFields): an absent scheme is not a reason to name the newer one.
func unsignedRefusal(field string) *Refusal {
	if field == tagFieldSig {
		return refuse(CodeBadSig, tagFieldSig)
	}
	return refuse(CodeBadMAC, tagFieldMAC)
}

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

// ErrUnsignable is M4's **third row**: the MAC layer is available, the key is
// loaded, and it is *this value* the signer errors on — so nothing is written
// for it, no reply and no heartbeat. It is T2a's, and it is what keeps that row
// told apart from the first one, which is the only run-level state of the three
// (M4's table, §8.3).
//
// **It is not the availability refusal, and a caller must not treat the two as
// one.** §8.3's startup exit — there is no pairing key at the item M5 names — is
// `keychain.ErrNoKey` (or `keychain.ErrBackend`), decided once *before* any pass,
// and its answer is exit 1 with no pass run and nothing written. This one is a
// **per-write refusal inside a running pass**: nothing is written for that value,
// the code is recorded, the pass continues, and the reply is owed again on the
// next pass (§8.2.4). §4.4 is explicit that it is **exit 0** — an outcome of a
// completed pass, like a message that does not verify, and not a failure to run.
// The two rows collapsing into one is the error M4's table exists to prevent, and
// it is why M5 reads the key once: a *key read* that fails is always the
// availability check, and what is left at run time is a signer that is present
// and errored on one value — a fault path no shipped build reaches by itself
// (which is why T2a reaches it with an injected failing signer).
var ErrUnsignable = errors.New("mailbox: the value could not be signed, so nothing was written")

// Signer is §8.2's half of the one function above: the tag every value the
// consumer writes carries **before it lands**. §8.2.2 is the reason it is not a
// second implementation — `sign` and `verify` are MacOf over CanonicalBytes,
// differing in the direction argument alone, and TestSignVerifyRoundTrip holds
// what this produces to what VerifyMAC accepts, and both to the vectors (M9).
//
// **The direction is fixed by the constructor and not passed per call.** The two
// families are the outbox reply and the heartbeat (§8.2.3), and a value signed
// for one node does not verify in the other — so the node a value is signed for
// is a property of the signer, and a caller holding the wrong one gets a
// `Heartbeat` that refuses rather than a heartbeat nothing verifies (T2a's red
// step (d)).
//
// **The key is never here in printable form**, exactly as it is not in the Gate:
// the signer holds the keychain's Key, whose every fmt verb and JSON answer
// Redacted, and the bytes are asked for once per tag by the one function that
// computes with them. A Signer is read-only after construction and may be used
// from anywhere.
type Signer struct {
	key       keychain.Key
	direction string
}

// NewOutboxSigner is the signer a reply is signed with: direction `outbox`, the
// node §6 puts the consumer's answers in.
func NewOutboxSigner(key keychain.Key) *Signer {
	return &Signer{key: key, direction: directionOutbox}
}

// NewHeartbeatSigner is the signer the heartbeat is signed with: direction
// `heartbeat`, the node the lease lives in (M1).
func NewHeartbeatSigner(key keychain.Key) *Signer {
	return &Signer{key: key, direction: directionHeartbeat}
}

// Direction is the node this signer signs for. It is safe to print: the
// direction is in the tagged bytes and is not a secret.
func (s *Signer) Direction() string {
	if s == nil {
		return ""
	}
	return s.direction
}

// Field is the record field this signer's tag lands in: `mac`, which is the
// scheme D171 gave the consumer and the only one this signer can write. It is
// what a writer reads to place the tag and to name the right field in a refusal
// (J5r) — the KMS signer beside this one answers `sig` — so a caller never
// spells either field itself.
func (s *Signer) Field() string {
	if s == nil {
		return ""
	}
	return tagFieldMAC
}

// String is what a log line about the signer says: what it is, and never a byte
// of the key — the same belt and braces Gate.String is, for the one shape the
// Key's own redaction cannot cover (`%s` of the signer itself).
func (s *Signer) String() string {
	if s == nil {
		return "mailbox.Signer(nil)"
	}
	return fmt.Sprintf("mailbox.Signer{direction: %s, key: %s}", s.direction, keychain.Redacted)
}

// Tag is the tag the value is written with: MacOf over the value's canonical
// bytes in this signer's direction (§8.2.1 — the value is built first, signed
// second, and the tag is added last, so what the tag covers is exactly what
// lands).
//
// A value the canonical serialisation refuses is an error rather than a tag, for
// the reason MacOf gives: signing bytes a reader will not see is the failure mode
// the canonical form exists to prevent. Every error here is ErrUnsignable's —
// the per-write refusal — so a caller never has to guess which of M4's three
// rows it is looking at.
//
// **The context is the interface's and not this signer's.** An HMAC needs no
// clock, no store and no call out, and this one uses the context for nothing;
// `KMSSigner.Tag` beside it is the signer that does, and one signature for both
// is what lets a writer hold either without knowing which (J5c).
func (s *Signer) Tag(ctx context.Context, value Record) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: there is no signer", ErrUnsignable)
	}
	tag, err := MacOf(value, s.key.Bytes(), s.direction)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnsignable, err)
	}
	return tag, nil
}

// heartbeatValue is the record a heartbeat is signed over and the record that is
// written: `{at, boot}` plus the advertised `agents` list when there is one
// (D161). **One function builds it for both halves** — the signer that covers it
// and the lease that writes it — because `canonicalBytes` covers every field but
// the tag, so a tag over a list the stored record did not carry would verify
// nowhere, with no error, and because the write is the lease there would be no
// lease either.
func heartbeatValue(hb Heartbeat) Record {
	value := Record{"at": hb.At, "boot": hb.Boot}
	if len(hb.Agents) > 0 {
		value["agents"] = AgentList(hb.Agents)
	}
	return value
}

// Heartbeat is the heartbeat's signature, and it is `LeaseSigner`'s own shape
// (`Heartbeat(context.Context, Heartbeat) (string, error)`) so that the lease's
// writer takes a signer and never a key: `lease.go` computes no tag and holds no
// keychain (§13 — one implementation of the covered field set, so the two halves
// cannot drift).
//
// It signs `heartbeatValue` — the record `lease.go` writes, field for field,
// built by the one function both halves call — and the list it carries is the
// caller's own agents in the order it advertises them (the array's order is
// covered).
//
// It refuses a signer built for another node: a heartbeat signed in the
// `outbox` direction is a heartbeat nothing verifies, so the node is not
// something a caller can get wrong (T2a's red step (d)).
func (s *Signer) Heartbeat(ctx context.Context, hb Heartbeat) (string, error) {
	if s == nil || s.direction != directionHeartbeat {
		return "", fmt.Errorf("%w: a heartbeat is signed in the %s direction, and this signer signs for another node", ErrUnsignable, directionHeartbeat)
	}
	return s.Tag(ctx, heartbeatValue(hb))
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
	// key is the pairing's MAC key (D171) and public is the device's own
	// (O6). Exactly one of the two is set, and which one IS the scheme this
	// gate accepts: the device node chose it, and a gate cannot be talked out
	// of it by the record it is handed.
	key    keychain.Key
	public ed25519.PublicKey
	now    func() time.Time
	tol    time.Duration
}

// String is what a log line about the gate says: what it is, which scheme it
// takes, and never a byte of the key. It is belt and braces over the Key's own
// redaction — the verbs that reach a struct's fields are already redacted by it
// — and it is the answer to the one shape they cannot cover: `%s` of the gate
// itself. The scheme is named because a log line that said only "mailbox.Gate"
// would not tell a reader which of the two a refusal came from.
func (g *Gate) String() string {
	if g == nil {
		return "mailbox.Gate(nil)"
	}
	if g.public != nil {
		return fmt.Sprintf("mailbox.Gate{future tolerance: %s, scheme: device signature}", g.tol)
	}
	return fmt.Sprintf("mailbox.Gate{future tolerance: %s, scheme: MAC, key: %s}", g.tol, keychain.Redacted)
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

// NewGate returns the gate a pass verifies its inbox with when the device node
// holds no public key (D171). The key is the pairing's, read once at startup
// (M5) — the caller reads it, this does not, and a gate built with no key
// refuses every message rather than accepting one. NewSignatureGate is O6's
// other half, and which of the two a pairing gets is the device node's answer.
func NewGate(key keychain.Key, o GateOptions) *Gate {
	g := &Gate{key: key}
	o.apply(g)
	return g
}

// apply fills a gate's clock and tolerance: the zero value of GateOptions is
// the production setting, and the two constructors share the rule rather than
// each writing it (a gate whose tolerance defaulted differently by scheme would
// be two answers to one question).
func (o GateOptions) apply(g *Gate) {
	g.now, g.tol = o.Now, o.FutureTolerance
	if g.now == nil {
		g.now = func() time.Time { return time.Now().UTC() }
	}
	if g.tol <= 0 {
		g.tol = FutureTolerance
	}
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
//
// **The scheme is the node's and never the record's** (O6). A gate built on a
// device node that holds a public key checks a signature and refuses a
// MAC-tagged record; one built on a pairing's key checks a MAC and refuses a
// signature. That is the whole of the downgrade fence: a record carrying the
// other tag is refused rather than tried, so a phone that has dropped its
// shared key cannot have a message accepted by carrying a tag anyone who ever
// read that key could have made.
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
	if refusal := g.attribute(record); refusal != nil {
		return nil, refusal
	}
	if at := message.At; at-g.now().UnixMilli() > g.tol.Milliseconds() {
		return nil, refuse(CodeFutureTimestamp, "at")
	}
	return message, nil
}

// attribute checks the record's one tag the way this gate's device must have
// written it, and answers the refusal naming the field that was wrong.
//
// The MAC key is asked for here, per verification, and the copy it answers is
// the only one that exists: nothing else in this package holds the material, so
// there is nothing else to leak. The public key is not a secret and is held for
// the life of the gate.
func (g *Gate) attribute(record Record) *Refusal {
	if g.public != nil {
		if record.has("mac") {
			// A MAC-tagged message at a node that holds a public key. The tag
			// is refused whatever it says: this node's phone does not write one,
			// and checking it would be checking a scheme the device retired.
			return refuse(CodeBadMAC, "mac")
		}
		if ok, err := VerifySignature(record, g.public, directionInbox); err != nil || !ok {
			return refuse(CodeBadSig, "sig")
		}
		return nil
	}
	if record.has("sig") {
		// The other way round: a signature at a node that holds no public key.
		// Nothing here could check it, and a node that holds no key is a device
		// paired the old way — so the signature is refused rather than ignored,
		// which would be accepting a record on no evidence at all.
		return refuse(CodeBadSig, "sig")
	}
	if ok, err := VerifyMAC(record, g.key.Bytes(), directionInbox); err != nil || !ok {
		return refuse(CodeBadMAC, "mac")
	}
	return nil
}
