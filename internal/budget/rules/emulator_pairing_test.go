//go:build firebase

package rules

// P6.4: the fields P6.3's schema made the contract, the device node of D166
// and the pairing nodes of D165 (mobile plan task P6.4, design m9 §6.4).
//
// The plan's verification list is what this file walks, one case at a time:
// a revoked device is denied every read and write; a session whose device node
// was never written is denied everything; a pairing uid writes its request node
// once and reads only its own delivery node; an `action` message without a
// confirmation record is refused; an outbox message without `mac`, and an inbox
// message of any kind without `mac`, are refused; a heartbeat's `agents` list
// over its bound is refused; and every one of them has the positive control
// beside it. The whole file is written against the deployed document — the
// rules Generate() renders — so it tests what the owner would deploy, not a
// transcription of it.
//
// What this file does NOT prove, and must not be read as proving: that a
// message's tag is *valid*. The rules check the field's shape; the value is
// P6.7's (`fugaro mailbox`, D169/D171) and needs the per-pairing key, which
// the database never holds.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The pairing identities. They are `p~` uids because the rules now require one:
// PAIRING reads the pu claim *and* tests `auth.uid.beginsWith('p~')`, so a pu
// claim minted onto an account's or a device's uid opens no pairing node. P6.6
// mints the claim and the `p~` uid together — that is the contract, and the
// `beginsWith` half is why the uid's shape is part of the rules rather than a
// naming convention nothing checks.
const (
	pairUid  = "p~alice-pairing"
	pairUid2 = "p~bob-pairing"
)

func request(uid string) string  { return "pairing/" + uid + "/request" }
func delivery(uid string) string { return "pairing/" + uid + "/delivery" }

// aRequest is the one write a pairing session makes: the phone's public key,
// which is what the operator's displayed fingerprint is compared against
// (D165). It carries no secret — the key is public by construction, and the
// token and the MAC key come back encrypted to it.
func aRequest() map[string]any {
	return map[string]any{"key": "zP8Qw3nT7vKd1sLbYx0aRcEm6JhGf4UoNq2ZiXwVt5Sk", "at": msgAt}
}

// TestDeviceNodeGatesEverythingUserReaches is D166: the allowlist node is the
// second half of the user identity, and deleting it revokes at once.
//
// The states of one account are walked in order — never written, written,
// deleted — because the property that matters most is the deletion: revocation
// that waits for an ID token to expire is not revocation (G14), which is why
// the owner chose a node over a claim. The account is then paired once more so
// the client-write refusals at the end have a node to fail against and the
// reads beside them are controls rather than refusals of an empty tree.
func TestDeviceNodeGatesEverythingUserReaches(t *testing.T) {
	h := newHarness(t)
	w := leased(2 * usd)
	h.seed(w)
	day, slug, run := w.day, "aurora", "r1"
	// An outbox message and a heartbeat for the phone to read, written as the
	// consumer, and the config families the user claim reads.
	h.mustAllow(h.consumer(mbAlice), "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustAllow(h.consumer(mbAlice), "an outbox message", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.set(budget.PathCapsRepo(slug)+"/dailyMicros", 1)

	// 1. Never written. Every family USER reaches is refused, and so is every
	// write the phone holds: an account that is not in the allowlist is not a
	// device, whatever its token says.
	unpaired := h.unpaired(mbAlice)
	for _, p := range []string{
		budget.PathCapsGlobal, budget.PathCapsDefaults, budget.PathCapsRepo(slug),
		budget.PathKillGlobal, budget.PathKillRepo(slug),
		budget.PathSpendGlobal(day), budget.PathSpendRepo(day, slug), budget.PathSpendRun(day, slug, run),
		"mailbox/" + mbAlice + "/outbox", outbox(mbAlice, "c1"), beat(mbAlice),
	} {
		h.mustReadDenied(unpaired, p)
	}
	h.mustDeny(unpaired, "an inbox message from an account with no device node",
		put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustDeny(unpaired, "an ack from an account with no device node", put(outbox(mbAlice, "c1")+"/acked", true))

	// 2. The node is written — the operator's `fugaro pair` — and the same
	// token is admitted, with no other change. The positive control is what
	// makes the refusals above refusals of this node rather than of an empty
	// tree.
	h.pairDevice(mbAlice)
	alice := h.unpaired(mbAlice)
	for _, p := range []string{
		budget.PathCapsGlobal, budget.PathCapsRepo(slug), budget.PathKillGlobal,
		budget.PathSpendGlobal(day), "mailbox/" + mbAlice + "/outbox", beat(mbAlice),
	} {
		h.mustRead(alice, p)
	}
	h.mustAllow(alice, "an inbox message from the paired account",
		put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustAllow(alice, "the ack on its own outbox message", put(outbox(mbAlice, "c1")+"/acked", true))

	// Another account is still another account: alice's node does not admit
	// bob's token, and it reads nothing of bob's.
	h.mustReadDenied(h.unpaired(mbBob), "mailbox/"+mbBob+"/outbox")
	h.mustDeny(h.unpaired(mbBob), "an inbox message in bob's unpaired mailbox",
		put(inbox(mbBob, "m1"), aMessage("m1", mbBob)))

	// 3. `fugaro pair revoke`: the child is deleted and the token that passed a
	// moment ago passes nothing — no expiry, no new sign-in, no
	// deny-list of tokens to maintain.
	h.unpairDevice(mbAlice)
	for _, p := range []string{
		budget.PathCapsGlobal, budget.PathKillGlobal, budget.PathSpendGlobal(day),
		"mailbox/" + mbAlice + "/outbox", beat(mbAlice),
	} {
		h.mustReadDenied(alice, p)
	}
	h.mustDeny(alice, "an ack after the device was revoked", put(outbox(mbAlice, "c2")+"/acked", true))
	h.mustDeny(alice, "an inbox message after the device was revoked",
		put(inbox(mbAlice, "m2"), aMessage("m2", mbAlice)))

	// The operator pairs the account again. This is not decoration: after the
	// delete above the node is gone, and the refusals below are only refusals of
	// *this write* if there is a node for the write to fail against — otherwise
	// they would pass on an empty tree and prove nothing about the client's
	// rights. So alice gets a node back here, and this read fails first if the
	// pairing itself did not take — which leaves the read at the end of the
	// block, after the client's write and delete were both refused, as the
	// control that says neither of them changed the node.
	h.pairDevice(mbAlice)
	alice = h.unpaired(mbAlice)
	h.mustRead(alice, beat(mbAlice))

	// 4. No client token may touch the allowlist, in any state: writable by
	// IAM only (deploy/terraform/gcp/modules/firebase/iam.tf grants
	// roles/firebasedatabase.admin to admins and budget admins, G13). A phone
	// that could write its own node could un-revoke itself, and one that could
	// read the parent would learn which accounts are paired.
	h.mustReadDenied(alice, PathDevices+"/"+mbAlice)
	h.mustReadDenied(alice, PathDevices)
	h.mustReadDenied(h.pairing(pairUid), PathDevices+"/"+mbAlice)
	h.mustReadDenied(h.consumer(mbAlice), PathDevices+"/"+mbAlice)
	// The unpaired phone is the one with a motive: it is refused the write that
	// would admit it, and the delete that would revoke another account.
	h.mustDeny(h.unpaired(mbBob), "an account writing its own device node",
		put(PathDevices+"/"+mbBob, map[string]any{"pairedAt": msgAt}))
	h.mustDeny(alice, "an account deleting its own device node", map[string]any{PathDevices + "/" + mbAlice: nil})
	// And the deletion really is the operator's alone: the node is still there,
	// because the denied delete changed nothing. A phone that could delete its
	// own node would hold the two halves of its identity, and `fugaro pair
	// revoke` would be advice rather than a revocation.
	h.mustRead(alice, beat(mbAlice))
}

// TestPairingSessionWritesItsRequestOnceAndReadsOnlyItsDelivery is D165: the
// two nodes a phone reaches before it is a device.
//
// The session is identified by the pu claim and by the shape of its uid: the
// rules require both pu == auth.uid and a uid that begins with `p~`.
// Everything else about a pairing uid — that it is one-shot, short-lived, and
// never reused — is P6.6's to mint and the operator's to check.
func TestPairingSessionWritesItsRequestOnceAndReadsOnlyItsDelivery(t *testing.T) {
	h := newHarness(t)
	uid := pairUid
	session := h.pairing(uid)

	// The positive control: the phone's public key, once.
	h.mustAllow(session, "the pairing request", put(request(uid), aRequest()))
	h.wantValue(request(uid)+"/key", `"zP8Qw3nT7vKd1sLbYx0aRcEm6JhGf4UoNq2ZiXwVt5Sk"`)

	// Once is the point: a second write — the same key or another — is refused,
	// so the fingerprint the operator confirms cannot be swapped under them
	// after the fact. A partial rewrite of the node is refused with it, and a
	// delete is a write of nothing, which `newData.exists()` refuses.
	h.mustDeny(session, "a second, identical request", put(request(uid), aRequest()))
	other := aRequest()
	other["key"] = "aNsFingerprintOfAnotherKeyEntirely00000000000000"
	h.mustDeny(session, "a second request naming another key", put(request(uid), other))
	h.mustDeny(session, "rewriting one field of the request", put(request(uid)+"/key", "swapped"))
	h.mustDeny(session, "deleting the request", map[string]any{request(uid): nil})
	h.wantValue(request(uid)+"/key", `"zP8Qw3nT7vKd1sLbYx0aRcEm6JhGf4UoNq2ZiXwVt5Sk"`)

	// The request's shape: the key and the instant, both required, nothing
	// else. A request with no key is a pairing with nothing to bind.
	for _, missing := range []string{"key", "at"} {
		// A fresh uid per case: the write-once rule would otherwise refuse the
		// write before validation ever ran, and the case would pass for the
		// wrong reason.
		fresh := uid + "-" + missing
		r := aRequest()
		delete(r, missing)
		h.mustDeny(h.pairing(fresh), "a request with no "+missing, put(request(fresh), r))
	}
	fresh := uid + "-extra"
	extra := aRequest()
	extra["token"] = "the pairing token"
	h.mustDeny(h.pairing(fresh), "a request carrying a field the node does not have",
		put(request(fresh), extra))
	fresh = uid + "-long"
	long := aRequest()
	long["key"] = strings.Repeat("k", MaxPublicKey+1)
	h.mustDeny(h.pairing(fresh), "a request key over its bound", put(request(fresh), long))
	fresh = uid + "-empty"
	empty := aRequest()
	empty["key"] = ""
	h.mustDeny(h.pairing(fresh), "an empty request key", put(request(fresh), empty))
	fresh = uid + "-chars"
	spaced := aRequest()
	spaced["key"] = "not a key"
	h.mustDeny(h.pairing(fresh), "a request key outside the base64 alphabet", put(request(fresh), spaced))

	// The request is nobody's to read: the operator checks the fingerprint
	// against the key they were shown (P6.6), not against the database.
	h.mustReadDenied(session, request(uid))
	h.mustReadDenied(h.pairing(uid+"-other"), request(uid))

	// Another session's node is another session's: the location wildcard and
	// the claim have to agree.
	h.mustDeny(h.pairing(pairUid2), "a request written into another session's node", put(request(uid), aRequest()))

	// The delivery: written by the operator over IAM (the token and the MAC
	// key, encrypted to the key above, D171), readable by this session and by
	// nobody else. The rules cannot check the ciphertext — they never see the
	// plaintext or the recipient's private key — so this is a read fence only.
	h.set(delivery(uid), map[string]any{"token": "sealed", "key": "sealed"})
	h.mustRead(session, delivery(uid))
	h.mustReadDenied(h.pairing(pairUid2), delivery(uid))
	h.mustReadDenied(h.phone(mbAlice), delivery(uid))
	h.mustReadDenied(h.consumer(mbAlice), delivery(uid))

	// The session writes nothing else at all: not its delivery, not the
	// request's parent, and none of the mailbox or the budget families.
	h.mustDeny(session, "a write to its own delivery node", put(delivery(uid), map[string]any{"token": "mine"}))
	h.mustDeny(session, "a write beside the two nodes it has", put("pairing/"+uid+"/other", map[string]any{"x": 1}))
	h.mustDeny(session, "a write to the pairing root", put("pairing", map[string]any{"x": 1}))
	h.mustDeny(session, "an inbox message from a pairing session",
		put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustDeny(session, "an ack from a pairing session", put(outbox(mbAlice, "c1")+"/acked", true))
	h.mustDeny(session, "a heartbeat from a pairing session", put(beat(mbAlice), aHeartbeat()))

	// And the pairing claim is worth nothing outside the pairing family: it is
	// not a user claim, and it admits no dashboard family.
	for _, p := range []string{budget.PathCapsGlobal, budget.PathKillGlobal, budget.PathSpendGlobal(today())} {
		h.mustReadDenied(session, p)
	}

	// A plain signed-in user is not a pairing session, and a pairing session is
	// not a user: neither claim substitutes for the other.
	plain := h.as(claims{uid: uid, fp: "-"})
	h.mustReadDenied(plain, delivery(uid))
	h.mustDeny(plain, "a request from a token with no pu claim", put(request(uid+"-plain"), aRequest()))
	// A pu token whose uid is not a `p~` pairing uid is not a pairing session:
	// the rules bind the session to the uid shape P6.6 mints, so a pu claim
	// minted onto an account's own uid cannot open pairing nodes under it.
	notPairUid := "m~alice-device"
	h.mustDeny(h.pairing(notPairUid), "a request from a pu token whose uid is not a p~ uid",
		put(request(notPairUid), aRequest()))
	h.set(delivery(notPairUid), map[string]any{"token": "sealed"})
	h.mustReadDenied(h.pairing(notPairUid), delivery(notPairUid))

	// The pu claim has to name the token's own uid, exactly as mu does. This
	// token's pu is one session's and its uid is another's, and the path is a
	// third — so the location wildcard refuses the write before the claim's
	// second conjunct is ever reached.
	mismatched := h.as(claims{pu: uid, uid: pairUid2, fp: "-"})
	h.mustDeny(mismatched, "a request under a pu claim that is not this token's uid",
		put(request(uid+"-mm"), aRequest()))
	h.mustReadDenied(mismatched, delivery(uid))

	// Which means the case above does not isolate `pu == auth.uid` on its own:
	// drop that conjunct and it still passes, because `$uid == auth.uid` is
	// false too. This is the case that does isolate it — the write is at the
	// token's OWN uid, so the wildcard holds and the claim is the only thing
	// left that can refuse it.
	isolated := uid + "-iso"
	crossed := h.as(claims{pu: pairUid2, uid: isolated, fp: "-"})
	h.mustDeny(crossed, "a request at the token's own uid under another session's pu claim",
		put(request(isolated), aRequest()))
	h.mustReadDenied(crossed, delivery(uid))
	// The control, so the two refusals above are refusals of the claim and not
	// of the path: the same request, at the same uid, under its own claim — and
	// the delivery of that uid, which this token may not read either.
	h.mustAllow(h.pairing(isolated), "the same request under its own pu claim",
		put(request(isolated), aRequest()))
	h.set(delivery(isolated), map[string]any{"token": "sealed", "key": "sealed"})
	h.mustRead(h.pairing(isolated), delivery(isolated))
}

// TestMailboxMessagesRequireTheTag is D169/D171/O7's field, at the database:
// every message of every kind carries a tag, and so does the heartbeat, and the
// tag's shape is the schema's own (64 lowercase hex characters: MAC_MAX and
// HEX_PATTERN, both checked at the database).
//
// The heart of it, for the reviewer: the rules can only require the *field*.
// They have no key and cannot verify a value, so this case proves that a
// message without a tag is refused, not that a message with a wrong tag is
// caught — that is `fugaro mailbox` (P6.7), which holds the key.
func TestMailboxMessagesRequireTheTag(t *testing.T) {
	h := newHarness(t)
	alice, consumer := h.phone(mbAlice), h.consumer(mbAlice)

	// Positive controls first: a tagged message in each direction, and a tagged
	// heartbeat.
	h.mustAllow(alice, "a tagged inbox message", put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustAllow(consumer, "a tagged outbox message", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.mustAllow(consumer, "a tagged heartbeat", put(beat(mbAlice), aHeartbeat()))

	// No tag at all, in either direction. Each case writes a node of its own so
	// the refusal is `mac` and not the write-once guard.
	untagged := aMessage("m2", mbAlice)
	delete(untagged, "mac")
	h.mustDeny(alice, "an inbox message with no mac", put(inbox(mbAlice, "m2"), untagged))
	untaggedOut := aMessage("c2", mbAlice)
	delete(untaggedOut, "mac")
	h.mustDeny(consumer, "an outbox message with no mac", put(outbox(mbAlice, "c2"), untaggedOut))
	beatNoMac := aHeartbeat()
	delete(beatNoMac, "mac")
	h.mustDeny(consumer, "a heartbeat with no mac", put(beat(mbAlice), beatNoMac))

	// A tag of the wrong shape: short, long, not a string at all. The rules
	// check the type, the length and (below) the hex characters; never the
	// value.
	for i, bad := range []any{"0f1e2d", strings.Repeat("0", MaxMacLength+1), 42, true, nil} {
		id := "m3-" + string(rune('a'+i))
		m := aMessage(id, mbAlice)
		m["mac"] = bad
		h.mustDeny(alice, "an inbox message whose mac is not a tag", put(inbox(mbAlice, id), m))
	}
	short := aHeartbeat()
	short["mac"] = "not-a-tag"
	h.mustDeny(consumer, "a heartbeat whose mac is not a tag", put(beat(mbAlice), short))

	// The tag is required on every kind, not only on the plain message: an
	// `action` is the shape an impostor would most want, and it is the one the
	// schema names first (the tag is why the extension exists).
	action := aMessage("m4", mbAlice)
	action["kind"] = "action"
	action["action"] = aActionRecord()
	delete(action, "mac")
	h.mustDeny(alice, "an action message with no mac", put(inbox(mbAlice, "m4"), action))

	// The heartbeat's advertised agent list: optional, bounded in count, each
	// entry an id and a name.
	withAgents := func(n int) map[string]any {
		// A list, as the consumer writes it: the database stores it keyed by
		// index, which is what the rules bound.
		agents := []any{}
		for i := 0; i < n; i++ {
			agents = append(agents, map[string]any{
				"id": "agent-" + string(rune('a'+i%26)) + string(rune('0'+i/26)), "name": "Agent",
			})
		}
		b := aHeartbeat()
		b["agents"] = agents
		return b
	}
	h.mustAllow(consumer, "a heartbeat advertising agents", put(beat(mbAlice), withAgents(3)))
	h.mustAllow(consumer, "a heartbeat advertising the bound", put(beat(mbAlice), withAgents(MaxAgents)))
	h.mustDeny(consumer, "a heartbeat advertising one agent over the bound",
		put(beat(mbAlice), withAgents(MaxAgents+1)))
	// An entry with no id or no name is not an agent this app can talk to.
	for _, missing := range []string{"id", "name"} {
		b := aHeartbeat()
		entry := map[string]any{"id": "agent-a", "name": "Agent"}
		delete(entry, missing)
		b["agents"] = []any{entry}
		h.mustDeny(consumer, "an advertised agent with no "+missing, put(beat(mbAlice), b))
	}
	b := aHeartbeat()
	b["agents"] = []any{map[string]any{"id": "agent-a", "name": "Agent", "load": 3}}
	h.mustDeny(consumer, "an advertised agent carrying a field the schema does not name", put(beat(mbAlice), b))
	b = aHeartbeat()
	b["agents"] = []any{map[string]any{"id": strings.Repeat("i", MaxAgentLength+1), "name": "Agent"}}
	h.mustDeny(consumer, "an advertised agent id over its bound", put(beat(mbAlice), b))

	// The list is a list: a scalar in its place, or an entry under a key that is
	// not an index below the bound, is not one. Without this a single string
	// passed as `agents` was stored (the review of P6.4: the count bound was a
	// method the rules language does not have, and nothing bounded the type).
	for _, bad := range []any{"agent-a", 7, true} {
		b = aHeartbeat()
		b["agents"] = bad
		h.mustDeny(consumer, "an agents field that is not a list", put(beat(mbAlice), b))
	}
	b = aHeartbeat()
	b["agents"] = map[string]any{"a": map[string]any{"id": "agent-a", "name": "Agent"}}
	h.mustDeny(consumer, "an advertised agent under a key that is not an index", put(beat(mbAlice), b))
	b = aHeartbeat()
	b["agents"] = map[string]any{strconv.Itoa(MaxAgents): map[string]any{"id": "agent-a", "name": "Agent"}}
	h.mustDeny(consumer, "an advertised agent at an index past the bound", put(beat(mbAlice), b))

	// The characters the schema states as a pattern are checked here too.
	b = aHeartbeat()
	b["agents"] = []any{map[string]any{"id": "agent a", "name": "Agent"}}
	h.mustDeny(consumer, "an advertised agent id with a space", put(beat(mbAlice), b))
	b = aHeartbeat()
	b["agents"] = []any{map[string]any{"id": "agent-a", "name": ""}}
	h.mustDeny(consumer, "an advertised agent with an empty name", put(beat(mbAlice), b))
	for _, boot := range []string{strings.Repeat("b", MaxBootLength+1), "boot 1", ""} {
		b = aHeartbeat()
		b["boot"] = boot
		h.mustDeny(consumer, "a heartbeat whose boot is not an identity of at most BOOT_MAX", put(beat(mbAlice), b))
	}
	b = aHeartbeat()
	b["boot"] = strings.Repeat("b", MaxBootLength)
	h.mustAllow(consumer, "a heartbeat whose boot is BOOT_MAX long", put(beat(mbAlice), b))
	for i, bad := range []string{strings.Repeat("g", MaxMacLength), strings.Repeat("A", MaxMacLength), strings.Repeat(" ", MaxMacLength)} {
		id := "m5-" + string(rune('a'+i))
		m := aMessage(id, mbAlice)
		m["mac"] = bad
		h.mustDeny(alice, "an inbox message whose mac is not lowercase hex", put(inbox(mbAlice, id), m))
		b = aHeartbeat()
		b["mac"] = bad
		h.mustDeny(consumer, "a heartbeat whose mac is not lowercase hex", put(beat(mbAlice), b))
	}
	m := aMessage("m6", mbAlice)
	m["agent"] = "agent one"
	h.mustDeny(alice, "a message addressed to an agent id with a space", put(inbox(mbAlice, "m6"), m))
}

// aActionRecord is a merge's action record: D169's typed destructive command,
// with the confirmation that makes it more than a sentence (G11). It is the
// shape the rules accept; the app and the consumer decide whether the
// confirmation is one they asked for.
func aActionRecord() map[string]any {
	return map[string]any{
		"id":           "act-1",
		"verb":         "merge",
		"target":       map[string]any{"repo": "dimipaun/fugaro", "pr": 42},
		"head_sha":     strings.Repeat("a", MinShaLength),
		"confirmation": map[string]any{"id": "cfm-1", "at": msgAt},
	}
}

// actionMessage is a message of kind `action` carrying the record above.
func actionMessage(id, mb string, rec map[string]any) map[string]any {
	m := aMessage(id, mb)
	m["kind"] = "action"
	m["action"] = rec
	return m
}

// TestMailboxActionRecordShape is D169 at the database: an action is typed, and
// a confirmation only the app enforces is prose (G11) — so the record that
// carries one is checked field by field, and the verb has to agree with the
// target it resolves to.
func TestMailboxActionRecordShape(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	// The positive controls, one per verb: cancel resolves to a run, merge to a
	// repository and a pull request, confirm to either.
	h.mustAllow(alice, "a merge action",
		put(inbox(mbAlice, "a1"), actionMessage("a1", mbAlice, aActionRecord())))
	cancel := map[string]any{
		"id": "act-2", "verb": "cancel", "target": map[string]any{"run": "r1"},
		"confirmation": map[string]any{"id": "cfm-2", "at": msgAt},
	}
	h.mustAllow(alice, "a cancel action",
		put(inbox(mbAlice, "a2"), actionMessage("a2", mbAlice, cancel)))
	confirm := map[string]any{
		"id": "act-3", "verb": "confirm", "target": map[string]any{"run": "r1"},
		"confirmation": map[string]any{"id": "cfm-3", "at": msgAt},
	}
	h.mustAllow(alice, "a confirm action on a run",
		put(inbox(mbAlice, "a3"), actionMessage("a3", mbAlice, confirm)))
	confirmRepo := map[string]any{
		"id": "act-4", "verb": "confirm", "target": map[string]any{"repo": "dimipaun/fugaro", "pr": 7},
		"confirmation": map[string]any{"id": "cfm-4", "at": msgAt},
	}
	h.mustAllow(alice, "a confirm action on a pull request",
		put(inbox(mbAlice, "a4"), actionMessage("a4", mbAlice, confirmRepo)))

	// A confirm resolves to a run or to a pull request, never to both: a target
	// carrying both shapes is one whose meaning the consumer has to guess at, on
	// the verb that authorises a destructive action (D169, G11). The merge case
	// further down does not cover this — a merge is refused a `run` by its own
	// arm. `{run, repo}` is the shape an either-shape test written as `||` lets
	// through, which is what this arm used to be.
	for i, tgt := range []map[string]any{
		{"run": "r1", "repo": "dimipaun/fugaro", "pr": 7},
		{"run": "r1", "repo": "dimipaun/fugaro"},
	} {
		rec := map[string]any{
			"id": "act-both", "verb": "confirm", "target": tgt,
			"confirmation": map[string]any{"id": "cfm-both", "at": msgAt},
		}
		id := "a5-" + string(rune('a'+i))
		h.mustDeny(alice, "a confirm whose target carries both shapes",
			put(inbox(mbAlice, id), actionMessage(id, mbAlice, rec)))
	}

	// The record's required fields, one missing at a time — `confirmation`
	// first, because that is the field the whole kind exists for.
	for _, missing := range []string{"confirmation", "id", "verb", "target"} {
		rec := aActionRecord()
		delete(rec, missing)
		id := "b1-" + missing
		h.mustDeny(alice, "an action record with no "+missing,
			put(inbox(mbAlice, id), actionMessage(id, mbAlice, rec)))
	}
	// A confirmation is an id and an instant, nothing else, and both.
	for _, missing := range []string{"id", "at"} {
		rec := aActionRecord()
		cfm := rec["confirmation"].(map[string]any)
		delete(cfm, missing)
		id := "b2-" + missing
		h.mustDeny(alice, "a confirmation with no "+missing,
			put(inbox(mbAlice, id), actionMessage(id, mbAlice, rec)))
	}
	rec := aActionRecord()
	rec["confirmation"] = map[string]any{"id": "cfm-1", "at": msgAt, "by": "alice"}
	id := "b2-extra"
	h.mustDeny(alice, "a confirmation with a field it does not have",
		put(inbox(mbAlice, id), actionMessage(id, mbAlice, rec)))
	rec = aActionRecord()
	rec["confirmation"] = "yes"
	h.mustDeny(alice, "a confirmation that is not a record",
		put(inbox(mbAlice, "b2-str"), actionMessage("b2-str", mbAlice, rec)))

	// The verb is one of the three the ruling names (D169), and the target has
	// to be the shape that verb resolves to: a merge of a run is not a thing,
	// and a cancel of a pull request is not one either.
	rec = aActionRecord()
	rec["verb"] = "delete"
	h.mustDeny(alice, "an action with a verb outside the three",
		put(inbox(mbAlice, "b3"), actionMessage("b3", mbAlice, rec)))
	mergeOfRun := aActionRecord()
	mergeOfRun["target"] = map[string]any{"run": "r1"}
	h.mustDeny(alice, "a merge whose target is a run",
		put(inbox(mbAlice, "b4"), actionMessage("b4", mbAlice, mergeOfRun)))
	cancelOfPR := map[string]any{
		"id": "act-5", "verb": "cancel", "target": map[string]any{"repo": "dimipaun/fugaro", "pr": 7},
		"confirmation": map[string]any{"id": "cfm-5", "at": msgAt},
	}
	h.mustDeny(alice, "a cancel whose target is a pull request",
		put(inbox(mbAlice, "b5"), actionMessage("b5", mbAlice, cancelOfPR)))
	both := aActionRecord()
	both["target"] = map[string]any{"run": "r1", "repo": "dimipaun/fugaro", "pr": 7}
	h.mustDeny(alice, "a target that is both shapes at once",
		put(inbox(mbAlice, "b6"), actionMessage("b6", mbAlice, both)))
	neither := aActionRecord()
	neither["target"] = map[string]any{}
	h.mustDeny(alice, "a target that is neither shape",
		put(inbox(mbAlice, "b7"), actionMessage("b7", mbAlice, neither)))

	// The head SHA is the merge's alone: absent from a merge it is not a merge
	// the consumer can check, and present on any other verb it is a field the
	// record has no place for.
	rec = aActionRecord()
	delete(rec, "head_sha")
	h.mustDeny(alice, "a merge with no head SHA",
		put(inbox(mbAlice, "b8"), actionMessage("b8", mbAlice, rec)))
	wrongLen := aActionRecord()
	wrongLen["head_sha"] = strings.Repeat("a", MinShaLength-1)
	h.mustDeny(alice, "a head SHA of a length Git never produces",
		put(inbox(mbAlice, "b9"), actionMessage("b9", mbAlice, wrongLen)))
	nonHex := aActionRecord()
	nonHex["head_sha"] = strings.Repeat("z", MinShaLength)
	h.mustDeny(alice, "a head SHA that is not hex",
		put(inbox(mbAlice, "b9x"), actionMessage("b9x", mbAlice, nonHex)))
	badRepo := aActionRecord()
	badRepo["target"] = map[string]any{"repo": "no-owner", "pr": 7}
	h.mustDeny(alice, "a repo target that is not owner/name",
		put(inbox(mbAlice, "b9y"), actionMessage("b9y", mbAlice, badRepo)))
	badRun := map[string]any{
		"id": "act-9", "verb": "cancel", "target": map[string]any{"run": "-r1"},
		"confirmation": map[string]any{"id": "cfm-9", "at": msgAt},
	}
	h.mustDeny(alice, "a run target that is not a run id",
		put(inbox(mbAlice, "b9z"), actionMessage("b9z", mbAlice, badRun)))
	shaOf64 := aActionRecord()
	shaOf64["head_sha"] = strings.Repeat("a", MaxShaLength)
	h.mustAllow(alice, "a head SHA of a SHA-256 repository",
		put(inbox(mbAlice, "b10"), actionMessage("b10", mbAlice, shaOf64)))
	cancelWithSha := map[string]any{
		"id": "act-6", "verb": "cancel", "target": map[string]any{"run": "r1"},
		"head_sha": strings.Repeat("a", MinShaLength), "confirmation": map[string]any{"id": "cfm-6", "at": msgAt},
	}
	h.mustDeny(alice, "a head SHA on a verb that has no head",
		put(inbox(mbAlice, "b11"), actionMessage("b11", mbAlice, cancelWithSha)))

	// Three bounds of the record that no other case in this file reaches, and
	// that the security review of P6.4 found loosened without a test noticing:
	// a pull request's number is positive (`pr >= 1`), the record's own `id` is
	// within the key bound, and the confirmation's `at` is an instant rather
	// than any number. Each is a bound the shape checks beside it cannot stand
	// in for — a `{repo, pr}` target is a target whatever the number, a
	// non-empty `id` is an `id` however long, and `hasChildren(['id', 'at'])`
	// says the instant is there, never that it is one.
	//
	// They are here rather than in a case of their own so that the reader of
	// the record's shape meets them with it, and they are deliberately the
	// loosest value each rule refuses (0, one character over the bound, a
	// negative instant): the boundary is where a loosening lands first.
	zeroPR := aActionRecord()
	zeroPR["target"] = map[string]any{"repo": "dimipaun/fugaro", "pr": 0}
	h.mustDeny(alice, "a pull request numbered zero",
		put(inbox(mbAlice, "b17"), actionMessage("b17", mbAlice, zeroPR)))
	longID := aActionRecord()
	longID["id"] = strings.Repeat("i", MaxKeyLength+1)
	h.mustDeny(alice, "an action id over the key bound",
		put(inbox(mbAlice, "b18"), actionMessage("b18", mbAlice, longID)))
	badAt := aActionRecord()
	badAt["confirmation"] = map[string]any{"id": "cfm-1", "at": -1}
	h.mustDeny(alice, "a confirmation whose at is not an instant",
		put(inbox(mbAlice, "b19"), actionMessage("b19", mbAlice, badAt)))

	// An action record on any other kind, and any other kind carrying the
	// fields the (kind, field) table gives to another — the table is what makes
	// "is this an action?" a question with one answer.
	plain := aMessage("b12", mbAlice)
	plain["action"] = aActionRecord()
	h.mustDeny(alice, "an action record on a plain message", put(inbox(mbAlice, "b12"), plain))
	promptWithAction := aMessage("b13", mbAlice)
	promptWithAction["kind"] = "prompt"
	promptWithAction["action"] = aActionRecord()
	h.mustDeny(alice, "an action record on a prompt", put(inbox(mbAlice, "b13"), promptWithAction))
	actionNoRecord := aMessage("b14", mbAlice)
	actionNoRecord["kind"] = "action"
	h.mustDeny(alice, "an action message with no action record", put(inbox(mbAlice, "b14"), actionNoRecord))
	messageWithOptions := aMessage("b15", mbAlice)
	messageWithOptions["options"] = []any{"one", "two"}
	h.mustDeny(alice, "options on a plain message", put(inbox(mbAlice, "b15"), messageWithOptions))
	messageWithReply := aMessage("b16", mbAlice)
	messageWithReply["reply_to"] = "m1"
	h.mustDeny(alice, "a reply_to on a plain message", put(inbox(mbAlice, "b16"), messageWithReply))

	// A prompt's options: allowed, bounded in count and in length, each one a
	// choice the app can read out and the answer can name by position.
	prompt := func(id string, opts any) map[string]any {
		m := aMessage(id, mbAlice)
		m["kind"] = "prompt"
		m["options"] = opts
		return m
	}
	h.mustAllow(alice, "a prompt with options",
		put(inbox(mbAlice, "c1"), prompt("c1", []any{"ship it", "hold"})))
	h.mustAllow(alice, "a prompt with the bound's worth of options",
		put(inbox(mbAlice, "c2"), prompt("c2", optionList(MaxOptions))))
	h.mustDeny(alice, "a prompt with one option over the bound",
		put(inbox(mbAlice, "c3"), prompt("c3", optionList(MaxOptions+1))))
	h.mustDeny(alice, "a prompt with an option over OPTION_MAX",
		put(inbox(mbAlice, "c4"), prompt("c4", []any{strings.Repeat("o", MaxOptionLength+1)})))
	h.mustDeny(alice, "a prompt with an empty option",
		put(inbox(mbAlice, "c5"), prompt("c5", []any{""})))
	h.mustDeny(alice, "a prompt with an option that is not a string",
		put(inbox(mbAlice, "c6"), prompt("c6", []any{1})))
	h.mustDeny(alice, "a prompt whose options is a string, not a list",
		put(inbox(mbAlice, "c7"), prompt("c7", "ship it")))
	h.mustDeny(alice, "a prompt whose options are keyed by name, not by index",
		put(inbox(mbAlice, "c8"), prompt("c8", map[string]any{"a": "ship it"})))

	// An answer names the prompt it answers, and an action may name it too (it
	// is how P6.7 applies the prompt's window rather than the command's).
	answer := func(id string, reply any) map[string]any {
		m := aMessage(id, mbAlice)
		m["kind"] = "answer"
		if reply != nil {
			m["reply_to"] = reply
		}
		return m
	}
	h.mustAllow(alice, "an answer naming its prompt", put(inbox(mbAlice, "d1"), answer("d1", "c1")))
	h.mustDeny(alice, "an answer that names no prompt", put(inbox(mbAlice, "d2"), answer("d2", nil)))
	actionReply := actionMessage("d3", mbAlice, aActionRecord())
	actionReply["reply_to"] = "c1"
	h.mustAllow(alice, "an action naming the prompt it settles", put(inbox(mbAlice, "d3"), actionReply))
	h.mustDeny(alice, "a reply_to that is not a message id",
		put(inbox(mbAlice, "d4"), answer("d4", strings.Repeat("m", MaxKeyLength+1))))

	// An unknown kind is an unknown kind, not a message: the field set IS the
	// version, and a kind from a future protocol must be refused rather than
	// read as the ordinary one.
	unknown := aMessage("d5", mbAlice)
	unknown["kind"] = "command"
	h.mustDeny(alice, "a kind outside the four", put(inbox(mbAlice, "d5"), unknown))
	notAString := aMessage("d6", mbAlice)
	notAString["kind"] = 7
	h.mustDeny(alice, "a kind that is not a string", put(inbox(mbAlice, "d6"), notAString))

	// The same shape on the outbox, where the consumer writes: the table is the
	// message's, not the direction's.
	consumer := h.consumer(mbAlice)
	h.mustAllow(consumer, "an action in the outbox",
		put(outbox(mbAlice, "e1"), actionMessage("e1", mbAlice, aActionRecord())))
	badOut := actionMessage("e2", mbAlice, aActionRecord())
	delete(badOut, "mac")
	h.mustDeny(consumer, "an outbox action with no mac", put(outbox(mbAlice, "e2"), badOut))
}

// optionList is n distinct option strings, for the options bound.
func optionList(n int) []any {
	opts := make([]any, 0, n)
	for i := 0; i < n; i++ {
		opts = append(opts, "option-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	return opts
}
