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
	"fmt"
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

// signed is a record whose tag is the second scheme rather than the first
// (J5r, O6): the same record with `mac` taken out and `sig` put in. Both are
// well-formed strings that no key produced, which is the whole of what the
// rules see.
func signed(v map[string]any) map[string]any {
	delete(v, "mac")
	v["sig"] = aSig
	return v
}

// TestEmulatorMailboxTagIsOneOfTwo: J5r's tag against the database (O6, which amends
// D171) — a message and the heartbeat carry exactly one of `mac` and `sig`, and
// the two are interchangeable everywhere a tag is required: the message's own
// validator, the registration's three fields and the heartbeat.
//
// "Exactly one" is the half no schema can state and the half a client gets
// wrong: a record carrying both is a record two verifiers would each accept, so
// it is refused here, and so is one carrying neither. Which scheme a device
// must use, and whether the value verifies, is its device node's and the gate's
// (J5b) — nothing here signs anything, and the rules check length and
// characters alone.
//
// Red step (for the reviewer): under generation 10's document every row below
// that writes a `sig` is refused, because a `mac` is required of every message
// and of the heartbeat.
func TestEmulatorMailboxTagIsOneOfTwo(t *testing.T) {
	h := newHarness(t)
	alice, consumer := h.phone(mbAlice), h.consumer(mbAlice)

	// The positive control: the signature where the MAC goes, on all three
	// holders of a tag — the phone's inbox message, the consumer's outbox
	// message and the consumer's heartbeat.
	h.mustAllow(alice, "an inbox message signed rather than tagged",
		put(inbox(mbAlice, "s1"), signed(aMessage("s1", mbAlice))))
	h.mustAllow(consumer, "an outbox message signed rather than tagged",
		put(outbox(mbAlice, "s2"), signed(aMessage("s2", mbAlice))))
	h.mustAllow(consumer, "a heartbeat signed rather than tagged", put(beat(mbAlice), signed(aHeartbeat())))
	h.wantValue(inbox(mbAlice, "s1")+"/sig", `"`+aSig+`"`)

	// The ack on a signed outbox message is unaffected: the ack is a field of
	// the message and not of the tag beside it.
	h.mustAllow(alice, "the ack on a signed outbox message", put(outbox(mbAlice, "s2")+"/acked", true))

	// The registration's three fields accept either scheme (J5b reads the
	// device node to decide which one a device must present), so a signing
	// device registers with the same record a MACing one does.
	reg := signed(aMessage("s5", mbAlice))
	reg["kind"] = "registration"
	reg["token"] = "a-push-token"
	reg["platform"] = "ios"
	h.mustAllow(alice, "a signed registration", put(inbox(mbAlice, "s5"), reg))

	// Both, and neither: the rule is "exactly one", not "either". Note the two
	// refusals have different causes under the older document (both are refused
	// for carrying a `mac` they may not duplicate; neither is refused for
	// carrying none), which is why the case names them apart.
	both := aMessage("s3", mbAlice)
	both["sig"] = aSig
	h.mustDeny(alice, "a message carrying both a mac and a sig", put(inbox(mbAlice, "s3"), both))
	neither := aMessage("s4", mbAlice)
	delete(neither, "mac")
	h.mustDeny(alice, "a message carrying neither a mac nor a sig", put(inbox(mbAlice, "s4"), neither))
	bothBeat := aHeartbeat()
	bothBeat["sig"] = aSig
	h.mustDeny(consumer, "a heartbeat carrying both a mac and a sig", put(beat(mbAlice), bothBeat))
	neitherBeat := aHeartbeat()
	delete(neitherBeat, "mac")
	h.mustDeny(consumer, "a heartbeat carrying neither a mac nor a sig", put(beat(mbAlice), neitherBeat))
	bothReg := signed(aMessage("s6", mbAlice))
	bothReg["kind"], bothReg["token"], bothReg["platform"] = "registration", "a-push-token", "ios"
	bothReg["mac"] = aMac
	h.mustDeny(alice, "a registration carrying both", put(inbox(mbAlice, "s6"), bothReg))

	// The signature's own shape: 86 characters of the base64url alphabet and no
	// padding, checked for length and characters exactly as the MAC is.
	for _, bad := range []struct {
		what string
		v    any
	}{
		{"one character short", aSig[:len(aSig)-1]},
		{"one character long", aSig + "A"},
		{"carrying base64's padding", aSig[:len(aSig)-2] + "=="},
		{"carrying a character outside the alphabet", aSig[:len(aSig)-1] + "+"},
		{"that is a number", 42},
		{"that is empty", ""},
	} {
		id := "s7-" + strings.ReplaceAll(bad.what, " ", "-")
		m := aMessage(id, mbAlice)
		delete(m, "mac")
		m["sig"] = bad.v
		h.mustDeny(alice, "a sig "+bad.what, put(inbox(mbAlice, id), m))
	}
	full := aMessage("s8", mbAlice)
	delete(full, "mac")
	full["sig"] = strings.Repeat("A", MaxSigLength)
	h.mustAllow(alice, "a sig exactly at the bound, unpadded", put(inbox(mbAlice, "s8"), full))
	h.mustDeny(consumer, "a heartbeat with a sig over the bound",
		put(beat(mbAlice), map[string]any{"at": msgAt, "boot": "boot-1", "sig": aSig + "A"}))

	// Nothing else about the message moved: the tag is a field of the message
	// and never a field of a field, so a child of it is refused.
	h.mustDeny(alice, "a child of the sig", put(inbox(mbAlice, "s9")+"/sig/x", "y"))
}

// TestEmulatorMailboxTypedLaunch: J5r's launch action against the database (O10) — an
// inbox action whose `verb` is `launch` carries a `mode` and needs no
// `confirmation`, where every other verb still carries one.
//
// The rules check the *shape* of the mode and nothing about its value: the mode
// is a rigor mode, whose vocabulary is internal/rigor's, and which modes need a
// read-back before they run is the launch gate's (J5a, D2786). So the rules
// admit a mode that names no rigor mode at all, and that is deliberate — a
// bound is not a vocabulary, and a rules generation must not be what carries
// the sixth mode when one is added.
//
// Red step (for the reviewer): under generation 10's document every row below
// that writes `launch` is refused at the verb enumeration, and the positive
// control is the case that says so.
func TestEmulatorMailboxTypedLaunch(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)
	repo := map[string]any{"repo": "aurora/web"}
	launch := func(id, mode string, target map[string]any) map[string]any {
		a := map[string]any{"id": id, "verb": "launch", "target": target}
		if mode != "" {
			a["mode"] = mode
		}
		return a
	}
	msg := func(id string, action map[string]any) map[string]any {
		m := aMessage(id, mbAlice)
		m["kind"] = "action"
		m["action"] = action
		return m
	}

	// The positive control: a start naming a repository and a mode, with no
	// confirmation at all. A plain start needs none — the mode is what the gate
	// reads, and a costly one is the gate's read-back to ask for.
	h.mustAllow(alice, "a typed launch", put(inbox(mbAlice, "L1"), msg("L1", launch("L1", "startup", repo))))

	// A launch may carry a confirmation: the gate's read-back is written the
	// same way on a launch as on the verbs that always need it.
	withConfirm := launch("L2", "critical", repo)
	withConfirm["confirmation"] = map[string]any{"id": "L2", "at": msgAt}
	h.mustAllow(alice, "a typed launch carrying a confirmation", put(inbox(mbAlice, "L2"), msg("L2", withConfirm)))

	// The mode is the launch's own: required on it, and refused on any other
	// verb, so `mode` on a cancel is a shape the rules do not have.
	h.mustDeny(alice, "a launch with no mode", put(inbox(mbAlice, "L3"), msg("L3", launch("L3", "", repo))))
	cancel := map[string]any{"id": "L4", "verb": "cancel", "target": map[string]any{"run": "r1"},
		"confirmation": map[string]any{"id": "L4", "at": msgAt}, "mode": "startup"}
	h.mustDeny(alice, "a cancel carrying a mode", put(inbox(mbAlice, "L4"), msg("L4", cancel)))

	// The mode's shape: a short identity string. Nothing here is a vocabulary
	// the rules know, which is why an unlisted word of the right shape is
	// admitted (the row above) and only the bound and the characters are what
	// this document holds.
	for i, bad := range []struct {
		what string
		v    any
	}{
		{"over the bound", strings.Repeat("m", MaxModeLength+1)},
		{"carrying a space", "the quick brown fox"},
		{"carrying a slash", "pro/hotfix"},
		{"that is empty", ""},
		{"that is a number", 7},
	} {
		id := fmt.Sprintf("L5-%d", i)
		h.mustDeny(alice, "a launch whose mode is "+bad.what,
			put(inbox(mbAlice, id), msg(id, map[string]any{"id": id, "verb": "launch", "target": repo, "mode": bad.v})))
	}
	at := launch("L6", strings.Repeat("m", MaxModeLength), repo)
	h.mustAllow(alice, "a mode exactly at the bound", put(inbox(mbAlice, "L6"), msg("L6", at)))

	// The target is a repository alone: a start names no run and no pull
	// request, and the arm refuses a target that carries either of them beside
	// it — the shape TestConfirmTargetsCarryOneShape pins statically.
	h.mustDeny(alice, "a launch whose target carries a run",
		put(inbox(mbAlice, "L7"), msg("L7", launch("L7", "startup", map[string]any{"repo": "aurora/web", "run": "r1"}))))
	h.mustDeny(alice, "a launch whose target carries a pull request",
		put(inbox(mbAlice, "L8"), msg("L8", launch("L8", "startup", map[string]any{"repo": "aurora/web", "pr": 12}))))
	h.mustDeny(alice, "a launch whose target carries no repository",
		put(inbox(mbAlice, "L9"), msg("L9", launch("L9", "startup", map[string]any{"run": "r1"}))))

	// Every other verb is exactly what it was: the enumeration gained one word
	// and lost none, so an unknown one is still refused, and a cancel still
	// needs its confirmation.
	h.mustDeny(alice, "an action whose verb is no verb the schema has",
		put(inbox(mbAlice, "L10"), msg("L10", map[string]any{"id": "L10", "verb": "start", "target": repo,
			"confirmation": map[string]any{"id": "L10", "at": msgAt}})))
	h.mustDeny(alice, "a cancel with no confirmation",
		put(inbox(mbAlice, "L11"), msg("L11", map[string]any{"id": "L11", "verb": "cancel", "target": map[string]any{"run": "r1"}})))
	h.mustAllow(alice, "a cancel with its confirmation",
		put(inbox(mbAlice, "L12"), msg("L12", map[string]any{"id": "L12", "verb": "cancel", "target": map[string]any{"run": "r1"},
			"confirmation": map[string]any{"id": "L12", "at": msgAt}})))
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
// grant is tension (b1)'s set — config/caps/**, spend/$day/**, config/kill/**
// and the phone's own outbox and heartbeat — plus, since J1 (mobile
// identity-pairing, F30, D2797, O3), the one outcome node the running-jobs view
// names a run by. A rules test that proved only the writes are fenced would
// leave the reads open, so every family the grant does not name has its own
// denial here, and the families it does name have the read beside it.
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
	// The granted set: the eight expressions that carried a run's read now carry
	// `|| USER`, the mailbox's two carry MAILBOX, and — since J1 (mobile
	// identity-pairing, F30, D2797, O3) — the outcome node carries USER alone,
	// one location rather than a family.
	for _, p := range []string{
		budget.PathCapsGlobal, budget.PathCapsDefaults, budget.PathCapsRepo(slug),
		budget.PathKillGlobal, budget.PathKillRepo(slug),
		budget.PathSpendGlobal(day), budget.PathSpendRepo(day, slug), budget.PathSpendRun(day, slug, run),
		budget.PathOutcome(day, slug, run),
		"mailbox/" + mbAlice + "/outbox", outbox(mbAlice, "c1"), outbox(mbAlice, "c1") + "/acked",
		beat(mbAlice),
	} {
		h.mustRead(alice, p)
	}

	// Every family the grant does not name, one explicit case each. The run's
	// slices (`agents/$slug/$run`, `runs/**`) are what tension (b1) struck, and
	// the two parents of the outcome node J1 opened are the same row one
	// generation later — the grant is on the location it sits at and not on the
	// days above it, so the app reads the run it names and no list of runs; the
	// rest of config is beside the granted part; `fugaro/**` is the project's
	// own mark; the parents of the granted nodes are not granted (an RTDB read
	// grant is on the location it sits at, and only cascades down); and the
	// inbox is the phone's write, not its read — the consumer, not the phone,
	// collects what the phone wrote (§6.4).
	for _, p := range []string{
		budget.PathAgent(slug, run), "agents", "agents/" + slug,
		"outcomes", "outcomes/" + budget.DayKey(day),
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

	// And the outcome node J1 opens is one location rather than one project's:
	// its grant is the claim's, as the families' are, so the day and the
	// repository a run is in are the reader's choice. What the phone reads is
	// still a run it could already see the spend of.
	outcome := budget.PathOutcome(day-1, "another-repo", "r9")
	h.set(outcome, map[string]any{"status": "succeeded", "requestedBy": "alice@example.invalid"})
	h.mustRead(alice, outcome)
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
