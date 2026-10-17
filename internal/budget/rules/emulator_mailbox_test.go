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
func aHeartbeat() map[string]any  { return map[string]any{"at": msgAt, "boot": "boot-1"} }
func aMessage(id, from string) map[string]any {
	return map[string]any{"id": id, "sender": from, "text": "the build is green", "at": msgAt}
}

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

	// The required fields, one missing at a time.
	for _, missing := range []string{"id", "sender", "text", "at"} {
		m := aMessage("m1", mbAlice)
		delete(m, missing)
		h.mustDeny(alice, "a message with no "+missing, put(inbox(mbAlice, "m2"), m))
	}
	// An unknown field, and the ack on an inbox message: the inbox's `$other`
	// refuses `acked` like any other field it does not name (D113's second
	// phone write is the ack of a message the phone has *spoken*, which is an
	// outbox message — design §6.2).
	for _, extra := range []map[string]any{
		{"acked": true},
		{"kind": "text"},
	} {
		m := aMessage("m1", mbAlice)
		for k, v := range extra {
			m[k] = v
		}
		h.mustDeny(alice, "a message carrying an unknown field", put(inbox(mbAlice, "m3"), m))
	}

	// id is the node key, within the tree's key bound.
	m := aMessage("other-id", mbAlice)
	h.mustDeny(alice, "an id that is not the node it is stored under", put(inbox(mbAlice, "m4"), m))
	long := strings.Repeat("k", 100)
	h.mustAllow(alice, "a 100-character id", put(inbox(mbAlice, long), aMessage(long, mbAlice)))
	h.mustDeny(alice, "a 101-character id", put(inbox(mbAlice, long+"k"), aMessage(long+"k", mbAlice)))

	// sender is the writer's identity: the rules bind it to the mailbox.
	m = aMessage("m5", mbBob)
	h.mustDeny(alice, "the sender of another account", put(inbox(mbAlice, "m5"), m))
	m = aMessage("m5", strings.Repeat("s", 201))
	h.mustDeny(alice, "a sender over 200 characters", put(inbox(mbAlice, "m5"), m))

	// text: non-empty, at most the schema's TEXT_MAX.
	m = aMessage("m6", mbAlice)
	m["text"] = ""
	h.mustDeny(alice, "an empty text", put(inbox(mbAlice, "m6"), m))
	m["text"] = strings.Repeat("t", MaxText)
	h.mustAllow(alice, "a text exactly at TEXT_MAX", put(inbox(mbAlice, "m6"), m))
	m["text"] = strings.Repeat("t", MaxText+1)
	h.mustDeny(alice, "a text one over TEXT_MAX", put(inbox(mbAlice, "m6"), m))
	m = aMessage("m6", mbAlice)
	m["text"] = 42
	h.mustDeny(alice, "a text that is a number", put(inbox(mbAlice, "m6"), m))

	// at: a non-negative integer instant, at most the schema's AT_MAX — which
	// is what the rules apply (TIMESTAMP, MaxTimestamp) and no longer the
	// amount bound. An instant the phone's validator accepts must be one the
	// database stores: this row is the review finding that made the two agree.
	m = aMessage("m7", mbAlice)
	for _, bad := range []any{"1700000000000", -1, 1.5} {
		m["at"] = bad
		h.mustDeny(alice, "at of the wrong shape", put(inbox(mbAlice, "m7"), m))
	}
	m["at"] = MaxTimestamp
	h.mustAllow(alice, "at exactly at AT_MAX", put(inbox(mbAlice, "m7"), m))
	m["at"] = MaxTimestamp + 1
	h.mustDeny(alice, "at one over AT_MAX", put(inbox(mbAlice, "m7"), m))

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

	// The heartbeat's shape: both fields, at an instant, unknown refused.
	h.mustDeny(consumer, "a heartbeat with no boot", put(beat(mbAlice), map[string]any{"at": msgAt}))
	h.mustDeny(consumer, "a heartbeat with an unknown field",
		put(beat(mbAlice), map[string]any{"at": msgAt, "boot": "b", "pid": 7}))

	// The ack is the one field inside the outbox the consumer can reach, and
	// the reason is the tree's shape rather than a grant of its own: `.write`
	// cascades downward, so the consumer's grant on the message node covers
	// that message's `acked` child too. What it can set is a boolean on a
	// message it wrote itself, in a mailbox it already writes, and the value
	// says "the phone has spoken this" — a claim the consumer is the only
	// reader of. Pinned here because §6.4 used to say the consumer could not
	// write an ack at all, which the cascade makes untrue (the phone's ack
	// itself is unaffected: it is the phone that must be able to reach it).
	h.mustAllow(consumer, "the acked child of its own message (its message-node grant cascades)",
		put(outbox(mbAlice, "c1")+"/acked", true))
	h.mustDeny(consumer, "an ack on another account's outbox message", put(outbox(mbBob, "c1")+"/acked", true))
	h.mustDeny(consumer, "an ack of the wrong type", put(outbox(mbAlice, "c1")+"/acked", "yes"))
}

// TestMailboxPhoneAcksWhatItSpoke: D113's second phone write — one field on a
// message the consumer left it — and the fact that it carries no more than
// that.
func TestMailboxPhoneAcksWhatItSpoke(t *testing.T) {
	h := newHarness(t)
	alice, consumer := h.phone(mbAlice), h.consumer(mbAlice)
	h.mustAllow(consumer, "an outbox message to ack", put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))

	h.mustAllow(alice, "the ack on a message in its own outbox", put(outbox(mbAlice, "c1")+"/acked", true))
	h.wantValue(outbox(mbAlice, "c1")+"/acked", "true")
	h.mustAllow(alice, "taking the ack back", put(outbox(mbAlice, "c1")+"/acked", false))

	// The ack is one field and nothing beside it: the phone cannot write the
	// message it is acking (that is the consumer's), and it cannot write a
	// field the message does not name.
	h.mustDeny(alice, "rewriting the message it acked",
		put(outbox(mbAlice, "c1"), aMessage("c1", mbAlice)))
	h.mustDeny(alice, "an unknown field beside the ack", put(outbox(mbAlice, "c1")+"/spokeAt", msgAt))
	h.mustDeny(alice, "an ack of the wrong type", put(outbox(mbAlice, "c1")+"/acked", 1))
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
