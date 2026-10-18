//go:build firebase

package rules

// P2.2: the mailbox's directed cases (mobile plan task P2.2, design m9 §6.4).
//
// P2.1's golden pins the document the owner deploys; TestRulesParity proves the
// rules and budget.Evaluate agree. Neither proves the fence holds — F21 is a
// claim about what a phone *cannot* do, and this is the only place it is
// tested. So every granted write (D113's two, D113b's two) has its positive
// control here, every family the user claim may *not* read has an explicit
// denial, and the two accounts below are two Firebase Auth uids, which is what
// a mailbox is keyed by.
//
// Nothing here mints a claim. mu and mc are written into the token by the
// harness (unsignedToken), which is exactly the point: the rules read them,
// and setting one on an Auth user is the owner's own step when an account is
// added (D114). A token that carries neither is a plain signed-in user, and it
// is admitted nowhere.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// Two accounts, as their mailbox keys: the mailbox is keyed by the account's
// Firebase Auth uid (§6.2), so "another account" is another uid.
const (
	mbAlice = "alice-uid"
	mbBob   = "bob-uid"
)

// msgAt is a plausible epoch-millisecond instant (2023-11-14), far from the
// bound the schema and the rules share.
const msgAt int64 = 1_700_000_000_000

func inbox(mb, id string) string  { return "mailbox/" + mb + "/inbox/" + id }
func outbox(mb, id string) string { return "mailbox/" + mb + "/outbox/" + id }
func beat(mb string) string       { return "mailbox/" + mb + "/heartbeat" }
func mailbox(mb string) string    { return "mailbox/" + mb }

// aMessage is a well-formed record: P6.3's required fields, which since P6.4
// include the tag. Every case that varies one field starts from this, so a
// refusal is the rule the case names and not a missing field it forgot.
func aMessage(id, from string) map[string]any {
	return map[string]any{"id": id, "sender": from, "text": "the build is green", "at": msgAt, "mac": aMac}
}

// aHeartbeat is the consumer's liveness record, whose required fields are
// P6.3's three: the instant, the boot id and the tag (O7).
func aHeartbeat() map[string]any { return map[string]any{"at": msgAt, "boot": "boot-1", "mac": aMac} }

// put is one write of one whole node: the mailbox's writers write a message or
// a heartbeat whole, so they are not multi-path updates of the ledger's shape.
func put(path string, v any) map[string]any { return map[string]any{path: v} }

// TestMailboxPhoneWritesItsOwnInbox: D113's first phone write, and the fence
// around it. The token carries mu == auth.uid, which is what makes the claim
// usable at all, and `$mb == auth.uid` is what keeps it inside one account.
//
// Mutation (for the reviewer): delete `$mb == auth.uid` from MAILBOX in the
// template and the cross-account rows below fail; delete `mu == auth.uid` from
// USER and TestMailboxClaimMismatchIsAdmittedNowhere fails.
func TestMailboxPhoneWritesItsOwnInbox(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	// The positive control: one well-formed message under its own id.
	h.mustAllow(alice, "the phone's own inbox message", put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.wantValue(inbox(mbAlice, "m1")+"/text", `"the build is green"`)

	// A message is written once (`!data.exists()` on the message node): the
	// phone cannot rewrite what it has sent, with the same words or others, and
	// neither can the consumer, which holds no grant on the inbox at all.
	h.mustDeny(alice, "rewriting its own sent inbox message, unchanged",
		put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	edited := aMessage("m1", mbAlice)
	edited["text"] = "the build is red"
	h.mustDeny(alice, "rewriting its own sent inbox message with other words", put(inbox(mbAlice, "m1"), edited))
	h.mustDeny(alice, "rewriting one field of its own sent inbox message", put(inbox(mbAlice, "m1")+"/text", "edited"))
	h.mustDeny(h.consumer(mbAlice), "the consumer rewriting a sent inbox message", put(inbox(mbAlice, "m1"), edited))
	h.wantValue(inbox(mbAlice, "m1")+"/text", `"the build is green"`)

	// Another account's mailbox: `$mb == auth.uid` fails on the location, so
	// neither its inbox nor its outbox message nor its ack is reachable.
	h.mustDeny(alice, "an inbox message in another account's mailbox",
		put(inbox(mbBob, "m1"), aMessage("m1", mbBob)))
	bob := h.phone(mbBob)
	h.mustAllow(bob, "the other account's own inbox message", put(inbox(mbBob, "m1"), aMessage("m1", mbBob)))
	h.mustAllow(h.consumer(mbBob), "a message in bob's outbox", put(outbox(mbBob, "c1"), aMessage("c1", mbBob)))
	h.mustDeny(alice, "an ack on another account's outbox message", put(outbox(mbBob, "c1")+"/acked", true))

	// The other writer's nodes. The outbox message and the heartbeat are the
	// consumer's (D113b): the phone's grant on the inbox grants nothing beside
	// it, and no ancestor of these nodes grants the phone anything.
	h.mustDeny(alice, "an outbox message from the phone", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.mustAllow(h.consumer(mbAlice), "the consumer's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustDeny(alice, "the heartbeat from the phone", put(beat(mbAlice), aHeartbeat()))
	h.mustDeny(alice, "overwriting an existing heartbeat", put(beat(mbAlice), aHeartbeat()))

	// The nodes the template grants nothing on: the mailbox root, the account
	// node, the inbox parent, and a child of the account that is not one of the
	// three the tree names. A rule that grants nothing is a rule that denies —
	// `mailbox/<mb>` carries no `$other`, and it needs none: nothing below it
	// is reachable by a name no rule mentions.
	for _, p := range []string{"mailbox", mailbox(mbAlice), "mailbox/" + mbAlice + "/inbox",
		"mailbox/" + mbAlice + "/secret/x"} {
		h.mustDeny(alice, "a write to "+p, put(p, map[string]any{"x": 1}))
	}
}

// TestMailboxMessageShape: the schema, field by field, against the database —
// the half of §6.4 that the golden cannot prove. Everything here is the
// phone's own inbox under its own claim, so every refusal is about the value
// and nothing else.
func TestMailboxMessageShape(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)
	node := inbox(mbAlice, "m1")
	h.mustAllow(alice, "a well-formed message", put(node, aMessage("m1", mbAlice)))

	// Every case below writes a node of its own whose id matches its key, so
	// each refusal is the rule the case names and not `id == $msg` or the
	// write-once guard of a node an earlier case wrote (mutation-checked: flip
	// the rule a case names and that case fails).
	//
	// The required fields, one missing at a time.
	for _, missing := range []string{"id", "sender", "text", "at"} {
		m := aMessage("m2-"+missing, mbAlice)
		delete(m, missing)
		h.mustDeny(alice, "a message with no "+missing, put(inbox(mbAlice, "m2-"+missing), m))
	}
	// An unknown field, and the ack on an inbox message: the inbox's `$other`
	// refuses `acked` like any other field it does not name (D113's second
	// phone write is the ack of a message the phone has *spoken*, which is an
	// outbox message — design §6.2). The second name is the one the schema
	// calls out: there is no expiry field, so every name for one is a field the
	// rules do not have either.
	for _, extra := range []string{"acked", "expires_at"} {
		id := "m3-" + extra
		m := aMessage(id, mbAlice)
		m[extra] = true
		h.mustDeny(alice, "a message carrying the unknown field "+extra, put(inbox(mbAlice, id), m))
	}

	// id is the node key, within the tree's key bound.
	m := aMessage("other-id", mbAlice)
	h.mustDeny(alice, "an id that is not the node it is stored under", put(inbox(mbAlice, "m4"), m))
	long := strings.Repeat("k", 100)
	h.mustAllow(alice, "a 100-character id", put(inbox(mbAlice, long), aMessage(long, mbAlice)))
	h.mustDeny(alice, "a 101-character id", put(inbox(mbAlice, long+"k"), aMessage(long+"k", mbAlice)))

	// sender is the writer's identity: the rules bind it to the mailbox.
	m = aMessage("m5a", mbBob)
	h.mustDeny(alice, "the sender of another account", put(inbox(mbAlice, "m5a"), m))
	m = aMessage("m5b", strings.Repeat("s", 201))
	h.mustDeny(alice, "a sender over 200 characters", put(inbox(mbAlice, "m5b"), m))

	// text: non-empty, at most the schema's TEXT_MAX.
	withText := func(id string, text any) map[string]any {
		m := aMessage(id, mbAlice)
		m["text"] = text
		return m
	}
	h.mustDeny(alice, "an empty text", put(inbox(mbAlice, "m6a"), withText("m6a", "")))
	h.mustDeny(alice, "a text one over TEXT_MAX",
		put(inbox(mbAlice, "m6b"), withText("m6b", strings.Repeat("t", MaxText+1))))
	h.mustDeny(alice, "a text that is a number", put(inbox(mbAlice, "m6c"), withText("m6c", 42)))
	h.mustAllow(alice, "a text exactly at TEXT_MAX",
		put(inbox(mbAlice, "m6d"), withText("m6d", strings.Repeat("t", MaxText))))

	// at: a non-negative integer instant, at most the schema's AT_MAX — which
	// is what the rules apply (TIMESTAMP, MaxTimestamp) and no longer the
	// amount bound. An instant the phone's validator accepts must be one the
	// database stores: this row is the review finding that made the two agree.
	withAt := func(id string, at any) map[string]any {
		m := aMessage(id, mbAlice)
		m["at"] = at
		return m
	}
	for i, bad := range []any{"1700000000000", -1, 1.5, MaxTimestamp + 1} {
		id := "m7-" + string(rune('a'+i))
		h.mustDeny(alice, "at of the wrong shape or over AT_MAX", put(inbox(mbAlice, id), withAt(id, bad)))
	}
	h.mustAllow(alice, "at exactly at AT_MAX", put(inbox(mbAlice, "m7-max"), withAt("m7-max", MaxTimestamp)))

	// A delete is not a write: validation does not run on one, which is why
	// every grant carries newData.exists().
	h.mustDeny(alice, "deleting its own inbox message", map[string]any{node: nil})
	h.mustDeny(alice, "deleting a child of its own inbox message", map[string]any{node + "/text": nil})

	// A zero-length node key is not a path.
	h.mustDeny(alice, "a message at the inbox itself", put("mailbox/"+mbAlice+"/inbox", aMessage("m1", mbAlice)))
}

// TestMailboxConsumerWritesOnlyItsMailbox: D113b's writer, and the fence
// around it. Its claim names one mailbox; the inbox, another account and every
// family outside the mailbox are out of its reach.
func TestMailboxConsumerWritesOnlyItsMailbox(t *testing.T) {
	h := newHarness(t)
	consumer := h.consumer(mbAlice)

	// The positive controls: a message in the outbox it serves, and the
	// heartbeat beside it.
	h.mustAllow(consumer, "its own outbox message", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.mustAllow(consumer, "its own heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.wantValue(beat(mbAlice)+"/boot", `"boot-1"`)
	// The heartbeat is the one node overwritten by design: it is the latest
	// liveness, not a record.
	h.mustAllow(consumer, "its heartbeat again", put(beat(mbAlice), map[string]any{"at": msgAt + 1, "boot": "boot-1", "mac": aMac}))

	// An outbox message is written once: the consumer cannot rewrite history,
	// with the same words, other words, or one field at a time.
	h.mustDeny(consumer, "rewriting its own outbox message, unchanged", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	edited := aMessage("c1", mbAlice)
	edited["text"] = "the build is red"
	h.mustDeny(consumer, "rewriting its own outbox message with other words", put(outbox(mbAlice, "c1"), edited))
	h.mustDeny(consumer, "rewriting one field of its own outbox message", put(outbox(mbAlice, "c1")+"/text", "edited"))
	h.wantValue(outbox(mbAlice, "c1")+"/text", `"the build is green"`)

	// An unknown field on an outbox message, under a fresh matching id.
	m3 := aMessage("c3", mbAlice)
	m3["expires_at"] = msgAt
	h.mustDeny(consumer, "an outbox message carrying an unknown field", put(outbox(mbAlice, "c3"), m3))

	// The inbox is the phone's: no ancestor of it grants the consumer
	// anything, and the message's own grant is MAILBOX.
	h.mustDeny(consumer, "an inbox message", put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustDeny(consumer, "a heartbeat beside the mailbox it serves", put(beat(mbBob), aHeartbeat()))
	h.mustDeny(consumer, "an outbox message in another account's mailbox",
		put(outbox(mbBob, "c1"), aMessage("c1", mbBob)))

	// sender is bound to the claim: `mc == $mb` on the message's own sender
	// field is `sender == $mb`, so a consumer cannot put words in a message
	// under another identity.
	m := aMessage("c2", "the-consumer")
	h.mustDeny(consumer, "a sender that is not the mailbox", put(outbox(mbAlice, "c2"), m))
	m["sender"] = mbAlice
	h.mustAllow(consumer, "a second outbox message", put(outbox(mbAlice, "c2"), m))
	h.mustDeny(consumer, "an outbox id that is not the node it is stored under",
		put(outbox(mbAlice, "c5"), aMessage("other-id", mbAlice)))

	// The heartbeat's shape: each required field, at an instant, unknown refused
	// (each case carries a mac, so the refusal is the field it names).
	h.mustDeny(consumer, "a heartbeat with no boot", put(beat(mbAlice), map[string]any{"at": msgAt, "mac": aMac}))
	h.mustDeny(consumer, "a heartbeat with an unknown field",
		put(beat(mbAlice), map[string]any{"at": msgAt, "boot": "b", "mac": aMac, "pid": 7}))

	// The ack is the phone's alone. The consumer's grant on the message node
	// cascades to `acked`, but that grant is write-once (`!data.exists()`), so
	// on a message that is already there it grants nothing; and a message
	// created already acked is refused by `acked`'s own `data.parent().exists()`.
	h.mustDeny(consumer, "the acked child of its own existing message", put(outbox(mbAlice, "c1")+"/acked", true))
	pre := aMessage("c4", mbAlice)
	pre["acked"] = true
	h.mustDeny(consumer, "an outbox message created already acked", put(outbox(mbAlice, "c4"), pre))
	h.mustDeny(consumer, "an ack on another account's outbox message", put(outbox(mbBob, "c1")+"/acked", true))
	h.mustDeny(consumer, "an ack of the wrong type", put(outbox(mbAlice, "c1")+"/acked", "yes"))
}

// TestMailboxHeartbeatDeleteIsDenied: the one mailbox node that is overwritten
// by design — D113b's heartbeat is the consumer's *latest liveness*, not a
// record — is still not removable, by either writer or by anyone.
//
// The heartbeat's `.write` is `CONSUMER && newData.exists()`, and the
// `newData.exists()` half is what refuses a delete: RTDB does not run
// validation on one, so a rule that only validated the shape would accept a
// delete of the node it validated. That matters here more than for a message:
// the staleness rule (P1.4) decides "offline" by how old the heartbeat is, so
// removing the node is removing the evidence rather than updating it, and a
// phone that could delete its own heartbeat could make the consumer's view
// say anything (or nothing) about liveness.
//
// TestMailboxWritesAreExactlyTheFour pins the fragment against the generated
// rules without an emulator; this is the case that proves what it does against
// the database, which is what the fragment is for.
func TestMailboxHeartbeatDeleteIsDenied(t *testing.T) {
	h := newHarness(t)
	alice, consumer := h.phone(mbAlice), h.consumer(mbAlice)
	h.mustAllow(consumer, "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))

	// The consumer may overwrite it — that is what the node is for — but not
	// remove it, whole or field by field.
	h.mustAllow(consumer, "overwriting the heartbeat", put(beat(mbAlice), map[string]any{"at": msgAt + 1, "boot": "boot-2", "mac": aMac}))
	h.mustDeny(consumer, "deleting its own heartbeat", map[string]any{beat(mbAlice): nil})
	h.mustDeny(consumer, "deleting one field of its own heartbeat", map[string]any{beat(mbAlice) + "/at": nil})
	// A denial is atomic: the message this multi-path update writes beside the
	// delete is not written either.
	h.mustDeny(consumer, "deleting the heartbeat in a multi-path update that writes a message too",
		map[string]any{beat(mbAlice): nil, outbox(mbAlice, "c1"): aMessage("c1", mbAlice)})
	h.wantValue(beat(mbAlice)+"/boot", `"boot-2"`)
	h.wantValue(outbox(mbAlice, "c1"), "null")

	// The phone holds no write grant on the heartbeat at all: it reads it
	// (MAILBOX) and may not write it, delete it or replace it.
	h.mustDeny(alice, "deleting the heartbeat from the phone", map[string]any{beat(mbAlice): nil})
	h.wantValue(beat(mbAlice)+"/boot", `"boot-2"`)

	// Nothing above the heartbeat grants a delete either: the mailbox node and
	// the account node carry no `.write` at all, and a rule that grants nothing
	// denies.
	h.mustDeny(consumer, "deleting the mailbox that holds the heartbeat", map[string]any{mailbox(mbAlice): nil})
	h.mustDeny(alice, "deleting its own mailbox", map[string]any{mailbox(mbAlice): nil})
	h.wantValue(beat(mbAlice)+"/boot", `"boot-2"`)
}

// TestMailboxPhoneAcksWhatItSpoke: D113's second phone write — one field on a
// message the consumer left it — and the fact that it carries no more than
// that.
func TestMailboxPhoneAcksWhatItSpoke(t *testing.T) {
	h := newHarness(t)
	alice, consumer := h.phone(mbAlice), h.consumer(mbAlice)
	h.mustAllow(consumer, "an outbox message to ack", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))

	h.mustAllow(consumer, "a second outbox message", put(outbox(mbAlice, "c2"), aMessage("c2", mbAlice)))
	h.mustAllow(consumer, "a third outbox message", put(outbox(mbAlice, "c3"), aMessage("c3", mbAlice)))

	h.mustAllow(alice, "the ack on a message in its own outbox", put(outbox(mbAlice, "c1")+"/acked", true))
	h.wantValue(outbox(mbAlice, "c1")+"/acked", "true")

	// The ack is written once, and only as true: it cannot be taken back or
	// written again (`!data.exists()` on acked), and a first write of false is
	// refused by `newData.val() == true` (on a message with no ack yet, so that
	// is the rule that refuses it).
	h.mustDeny(alice, "taking the ack back", put(outbox(mbAlice, "c1")+"/acked", false))
	h.mustDeny(alice, "acking the same message again", put(outbox(mbAlice, "c1")+"/acked", true))
	h.mustDeny(alice, "a first ack of false", put(outbox(mbAlice, "c2")+"/acked", false))
	h.wantValue(outbox(mbAlice, "c1")+"/acked", "true")

	// The ack is one field and nothing beside it: the phone cannot write the
	// message it is acking (that is the consumer's), and it cannot write a
	// field the message does not name.
	h.mustDeny(alice, "rewriting the message it acked",
		put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.mustDeny(alice, "an unknown field beside the ack", put(outbox(mbAlice, "c1")+"/spokeAt", msgAt))
	h.mustDeny(alice, "an ack of the wrong type", put(outbox(mbAlice, "c3")+"/acked", 1))
	h.mustDeny(alice, "an ack on an outbox message that does not exist", put(outbox(mbAlice, "c9")+"/acked", true))
	h.mustDeny(alice, "deleting an ack", map[string]any{outbox(mbAlice, "c1") + "/acked": nil})
}

// TestMailboxClaimMismatchIsAdmittedNowhere: the claim is read, never trusted
// on its own. A token whose mu names a mailbox that is not its uid — the one
// way a client could try to choose someone else's mailbox — is refused
// everywhere, and so is a token with no claim at all, which keeps F8's
// property: no grant admits a signed-in user by itself.
func TestMailboxClaimMismatchIsAdmittedNowhere(t *testing.T) {
	h := newHarness(t)
	w := stdWorld()
	h.seed(w)
	h.mustAllow(h.phone(mbAlice), "alice's own inbox message", put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustAllow(h.consumer(mbAlice), "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))
	// Both accounts are paired, so that the refusals below are refusals of the
	// claim and not of D166's device node — which has its own case
	// (TestDeviceNodeGatesEverythingUserReaches).
	h.pairDevice(mbAlice)
	h.pairDevice(mbBob)

	// mu names alice's mailbox; the uid Firebase minted is bob's. The token is
	// otherwise an ordinary one for this project (it keeps fp), because that is
	// the token an attacker would have: the claim is what must not help it.
	forged := h.as(claims{mu: mbAlice, uid: mbBob})
	h.mustDeny(forged, "an inbox message under a claim that is not this token's uid",
		put(inbox(mbAlice, "m2"), aMessage("m2", mbAlice)))
	h.mustDeny(forged, "an ack under a claim that is not this token's uid",
		put(outbox(mbAlice, "c1")+"/acked", true))
	h.mustReadDenied(forged, "mailbox/"+mbAlice+"/outbox")
	h.mustReadDenied(forged, beat(mbAlice))
	// The mailbox rows above are also refused by `$mb == auth.uid`; these are
	// refused by USER's own `mu == auth.uid` and nothing else, since the read
	// grants on caps, kill and spend name no mailbox. The token carries no fp
	// here, or TOKEN (a run's read) would admit it and the row would not be
	// about USER.
	forgedUser := h.as(claims{mu: mbAlice, uid: mbBob, fp: "-"})
	for _, p := range []string{budget.PathCapsGlobal, budget.PathKillGlobal, budget.PathSpendGlobal(w.day)} {
		h.mustReadDenied(forgedUser, p)
	}

	// A plain signed-in user: a uid and nothing else, no mu and no mc — which
	// is also why it carries no fp here (a token with fp is a run's, and the
	// question this row asks is about a user's). It reads none of the four
	// granted families and writes nothing: the row that keeps the read grants
	// from being a grant to "any signed-in user".
	plain := h.as(claims{uid: mbAlice, fp: "-"})
	for _, p := range []string{budget.PathCapsGlobal, budget.PathCapsDefaults, budget.PathCapsRepo("aurora"),
		budget.PathKillGlobal, budget.PathKillRepo("aurora"), budget.PathSpendGlobal(w.day),
		budget.PathSpendRepo(w.day, "aurora"), budget.PathSpendRun(w.day, "aurora", "r1"),
		"mailbox/" + mbAlice + "/outbox", beat(mbAlice)} {
		h.mustReadDenied(plain, p)
	}
	h.mustDeny(plain, "an inbox message from a user with no claim",
		put(inbox(mbAlice, "m2"), aMessage("m2", mbAlice)))
	h.mustDeny(plain, "an ack from a user with no claim", put(outbox(mbAlice, "c1")+"/acked", true))
	h.mustDeny(plain, "a heartbeat from a user with no claim", put(beat(mbAlice), aHeartbeat()))

	// A run token: it is not a mailbox identity either, and the mailbox grants
	// are not read by any of its claims.
	run := w.client(h)
	for _, p := range []string{"mailbox/" + mbAlice + "/outbox", beat(mbAlice), inbox(mbAlice, "m1")} {
		h.mustReadDenied(run, p)
	}
	h.mustDeny(run, "an inbox message from a run token", put(inbox(mbAlice, "m2"), aMessage("m2", mbAlice)))
	h.mustDeny(run, "an outbox message from a run token", put(outbox(mbAlice, "c2"), aMessage("c2", mbAlice)))
	h.mustDeny(run, "a heartbeat from a run token", put(beat(mbAlice), aHeartbeat()))

	// And the grants WERE there to refuse: alice reads her own, so the rows
	// above are refusals of the claim and not of an empty tree.
	h.mustRead(h.phone(mbAlice), "mailbox/"+mbAlice+"/outbox")
	h.mustRead(h.phone(mbAlice), beat(mbAlice))
}

// TestMailboxReadsAreTheGrantedSet: D112's answer is direct reads, and the
// grant is exactly tension (b1)'s set — config/caps/**, spend/$day/**,
// config/kill/** and the phone's own outbox and heartbeat. A rules test that
// proved only the writes are fenced would leave the reads open, so every
// family the grant does not name has its own denial here, and the families it
// does name have the read beside it.
func TestMailboxReadsAreTheGrantedSet(t *testing.T) {
	h := newHarness(t)
	w := leased(2 * usd)
	h.seed(w)
	day, slug, run := w.day, "aurora", "r1"
	h.patch(map[string]any{
		budget.PathCapsRepo(slug):          map[string]any{"dailyMicros": 1},
		budget.PathKillRepo(slug) + "/on":  false,
		budget.PathAgent(slug, run):        map[string]any{"repo": "aurora/web", "requestedBy": "alice@example.invalid"},
		budget.PathOutcome(day, slug, run): map[string]any{"status": "succeeded", "requestedBy": "alice@example.invalid"},
		projectpolicy.PathShared():         map[string]any{"version": 1, "budget": map[string]any{"mode": "enforce"}},
		outbox(mbAlice, "c1"):              aMessage("c1", mbAlice),
		inbox(mbAlice, "m1"):               aMessage("m1", mbAlice),
		beat(mbAlice):                      aHeartbeat(),
		outbox(mbBob, "c1"):                aMessage("c1", mbBob),
		beat(mbBob):                        aHeartbeat(),
	})

	alice := h.phone(mbAlice)
	// The four granted families: the eight expressions that carried a run's
	// read now carry `|| USER`, and the mailbox's two carry MAILBOX.
	for _, p := range []string{
		budget.PathCapsGlobal, budget.PathCapsDefaults, budget.PathCapsRepo(slug),
		budget.PathKillGlobal, budget.PathKillRepo(slug),
		budget.PathSpendGlobal(day), budget.PathSpendRepo(day, slug), budget.PathSpendRun(day, slug, run),
		"mailbox/" + mbAlice + "/outbox", outbox(mbAlice, "c1"), outbox(mbAlice, "c1") + "/acked",
		beat(mbAlice),
	} {
		h.mustRead(alice, p)
	}

	// Every family the grant does not name, one explicit case each. The run's
	// slices (`agents/$slug/$run`, `outcomes/$day/$slug/$run`, `runs/**`) are
	// what tension (b1) struck; the rest of config is beside the granted part;
	// `fugaro/**` is the project's own mark; the parents of the granted nodes
	// are not granted (an RTDB read grant is on the location it sits at, and
	// only cascades down); and the inbox is the phone's write, not its read —
	// the consumer, not the phone, collects what the phone wrote (§6.4).
	for _, p := range []string{
		budget.PathAgent(slug, run), "agents", "agents/" + slug,
		budget.PathOutcome(day, slug, run), "outcomes", "outcomes/" + budget.DayKey(day),
		"runs", budget.PathRun(slug, run),
		budget.PathLimits, budget.PathMode, "config", "config/caps", "config/caps/repos",
		"config/kill", "config/kill/repos", projectpolicy.PathShared(), projectpolicy.PathRepo(slug),
		budget.PathSpendMeta(day), "spend", "spend/" + budget.DayKey(day), "spend/" + budget.DayKey(day) + "/repos",
		budget.PathProject, budget.PathMark, "fugaro",
		"mailbox", mailbox(mbAlice), "mailbox/" + mbAlice + "/inbox", inbox(mbAlice, "m1"),
		"mailbox/" + mbBob + "/outbox", beat(mbBob),
	} {
		h.mustReadDenied(alice, p)
	}

	// Another account's claim is another account's mailbox: bob reads his own
	// and not alice's, and the consumer — which holds no read grant anywhere —
	// reads nothing at all, its own mailbox included.
	bob := h.phone(mbBob)
	h.mustRead(bob, "mailbox/"+mbBob+"/outbox")
	h.mustRead(bob, beat(mbBob))
	h.mustReadDenied(bob, "mailbox/"+mbAlice+"/outbox")
	h.mustReadDenied(bob, beat(mbAlice))
	for _, p := range []string{"mailbox/" + mbAlice + "/outbox", beat(mbAlice),
		budget.PathCapsGlobal, budget.PathSpendGlobal(day), budget.PathKillGlobal, "mailbox/" + mbAlice + "/inbox"} {
		h.mustReadDenied(h.consumer(mbAlice), p)
	}

	// The grant is on the claim, not on the day: yesterday's and tomorrow's
	// spend are readable by the app (the reader needs the day it is showing),
	// and so is a repository the phone does not run — `config/caps/repos/$slug`
	// and `spend/$day/repos/$slug` are `|| USER` beside the run's own
	// slug-bound disjunct. Stated here because it is wider than the run's
	// grant and is what tension (b1) settled: caps/** and spend/$day/** are the
	// whole families.
	other := budget.PathSpendRepo(day-1, "another-repo")
	h.set(other+"/counted", 5)
	h.mustRead(alice, other)
	h.mustRead(alice, budget.PathSpendGlobal(day+1))
}

// TestMailboxResidualMintedTokenNeedsTheDeviceNode is the residual P2.2
// documented, in the state P6.4 leaves it.
//
// USER binds mu to auth.uid, and a client cannot choose either. But phones
// sign in with custom tokens only (T5, D165), and whoever mints a custom token
// chooses the uid and every claim: a launcher (signJwt, iam.tf's token_minter)
// or any holder of a key of one of the project's service accounts. So a token
// minted with uid == mu == alice is, to the rules, as much a user as alice's
// phone — but P6.4's device conjunct (D166) is the second key it does not
// hold: config/admin/devices/<uid> is written over IAM, and minting a token
// does not create one. That is the half this test now pins: **no device node,
// no mailbox**.
//
// What the device node does not close, and what this file must not pretend it
// does: the same principal that can sign a token usually holds
// roles/firebasedatabase.admin, so it can write the device node as well as the
// token — the row at the end of this test does exactly that and passes. The
// rules also cannot tell a message the phone spoke from one the minter
// invented; the per-pairing MAC of D169/D171 is what separates them, it is
// required on every message and on the heartbeat since P6.4, and verifying it
// is P6.7's (`fugaro mailbox`) — the rules have no key. And a launcher reads
// every mailbox through roles/firebasedatabase.viewer, which bypasses the
// rules entirely (mobile reconciliation G13).
func TestMailboxResidualMintedTokenNeedsTheDeviceNode(t *testing.T) {
	h := newHarness(t)
	h.mustAllow(h.consumer(mbAlice), "alice's outbox message", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.mustAllow(h.consumer(mbAlice), "alice's heartbeat", put(beat(mbAlice), aHeartbeat()))

	// A token a launcher minted for alice's uid, carrying alice's mu — and
	// alice has no device node, which is the state of every account before it
	// is paired and of every account after `fugaro pair revoke`. It also names
	// the launcher in rb, which the mailbox rules do not read.
	minted := h.as(claims{uid: mbAlice, mu: mbAlice, fp: "-", rb: "mallory@example.invalid"})
	h.mustReadDenied(minted, "mailbox/"+mbAlice+"/outbox")
	h.mustReadDenied(minted, beat(mbAlice))
	h.mustReadDenied(minted, budget.PathCapsGlobal)
	h.mustDeny(minted, "an inbox message in alice's mailbox from a minted token",
		put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustDeny(minted, "an ack of alice's outbox message from a minted token",
		put(outbox(mbAlice, "c1")+"/acked", true))

	// The node goes in and the same token is alice again: this is the whole of
	// what D166 buys, and it is why the residual below is a launcher-level
	// finding and not a claim-level one.
	h.pairDevice(mbAlice)
	h.mustRead(minted, "mailbox/"+mbAlice+"/outbox")
	h.mustAllow(minted, "an inbox message from a minted token whose account is paired",
		put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))

	// Nothing personal is in the device node: it is a liveness allowlist, and
	// the rules read its existence and no field of it. (A reader of the node
	// needs IAM; no client token may read it at all — TestDeviceNodesAreClientDenied.)
	h.mustReadDenied(minted, PathDevices+"/"+mbAlice)

	// A token minted with mc == alice is alice's consumer, and the device node
	// is not what a consumer passes: CONSUMER names the mailbox it serves and
	// nothing else. So the heartbeat's integrity is still not the rules' to
	// keep — the tag P6.4 requires on it is what P6.7 verifies, and the signed
	// heartbeat of O7 is what would bind it to a machine.
	forgedConsumer := h.as(claims{mc: mbAlice, fp: "-", rb: "mallory@example.invalid"})
	h.mustAllow(forgedConsumer, "alice's heartbeat from a minted mc token",
		put(beat(mbAlice), map[string]any{"at": msgAt + 1, "boot": "forged", "mac": aMac}))
	h.wantValue(beat(mbAlice)+"/boot", `"forged"`)
}
