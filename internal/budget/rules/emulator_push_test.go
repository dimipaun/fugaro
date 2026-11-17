//go:build firebase

package rules

// P5.2.3: the registration message and the sibling store against a real rules
// engine (mobile plan task P5.2.3, SECURITY-SENSITIVE; the push-notifications
// design §3.4, §3.5 (b) and §3.8). P5.2.2's generated document is what these
// cases drive — a case written against a template that is still moving is a
// test of a draft — and TestRegistrationFieldValidatorsCarryTheKindGate reads
// the same clauses as text. Neither half subsumes the other: that one proves
// the gate is *written*, this one proves the engine reads it the way the
// design claims.
//
// Three properties, each beside the positive control that makes its refusal a
// refusal of the rule and not of an absent node:
//
//   - the paired phone writes its registration, and only that shape: the kind
//     gate refuses the token, the platform and the preference record on every
//     message that is not a registration, and every bound the schema sets is
//     enforced at the value the schema refuses;
//   - no identity these rules admit reads or writes `config/admin/push/$uid` —
//     the phone whose own uid keys it included. The store's one writer is the
//     registration principal over IAM (P5.2.4), which bypasses the rules; a
//     client grant is the only way one could reach it, and the document
//     carries no node under the path for one to be written on;
//   - the device node is what carries all of it: a revoked account and an
//     account whose device node was never written are both refused the
//     registration write, which is what makes "no device node, no send" true
//     of the registration path as well as of the mailbox.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// aPushToken is a registration's token: a value of the shape TOKENOK accepts —
// a non-empty string within its bound — and deliberately not a real one. The
// rules check the shape and never the value: verifying a token needs a
// provider, which no rules document has, so this is the field's whole fence.
const aPushToken = "a-device-token-for-the-registration"

// pushStore is the store a registration is applied to, a sibling of D166's
// device node and never a child of it (design §3.4): a routine push write that
// could reach `devices/**` is a routine push write that could un-revoke a
// device.
func pushStore(uid string) string { return "config/admin/push/" + uid }

// aPrefs is §3.8's preference record: the five switches the app registers —
// all five required once the node is there, which is the schema's
// PREFS_REQUIRED — and the optional quiet-hours window, whose zone is an IANA
// name rather than an offset (a stored offset is a fixed offset, and it drifts
// onto the wrong hour at every DST transition).
func aPrefs() map[string]any {
	return map[string]any{
		"kill_switch": false, "cap_reached": true, "consumer_offline": false,
		"cap_near": true, "prompt": true,
		"quiet_hours": map[string]any{"zone": "Europe/Berlin", "start": 1320, "end": 420},
	}
}

// aRegistration is the message §3.5 (b) has the app write into its own inbox:
// P6.3's required fields, the registration kind, the token and the platform the
// schema requires of that kind, and §3.8's switches.
func aRegistration(mb, id string) map[string]any {
	m := aMessage(id, mb)
	m["kind"] = "registration"
	m["token"] = aPushToken
	m["platform"] = "ios"
	m["prefs"] = aPrefs()
	return m
}

// TestPushRegistrationMessageOnTheEmulator is §3.5 (b)'s write and the fence
// around it. Mutation (for the reviewer): drop `PARENTKIND == 'registration'
// && ` from a field's node in the template and the kind-gate cases below
// accept a token on an ordinary message — the fail-closed layer is exactly
// that conjunct.
func TestPushRegistrationMessageOnTheEmulator(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)

	// The positive control: the whole of what the app registers, on one message
	// of the registration kind, in its own inbox.
	h.mustAllow(alice, "the phone's own registration", put(inbox(mbAlice, "r1"), aRegistration(mbAlice, "r1")))
	h.wantValue(inbox(mbAlice, "r1")+"/token", `"`+aPushToken+`"`)
	h.wantValue(inbox(mbAlice, "r1")+"/platform", `"ios"`)
	h.wantValue(inbox(mbAlice, "r1")+"/prefs/quiet_hours/zone", `"Europe/Berlin"`)

	// The two fields the schema requires of the kind (KIND_REQUIRED) are
	// required on the message: a registration without its token, and one
	// without its platform, is refused by KINDREQ's registration arm rather
	// than stored with a field that is not there.
	for _, missing := range []string{"token", "platform"} {
		m := aRegistration(mbAlice, "r2-"+missing)
		delete(m, missing)
		h.mustDeny(alice, "a registration with no "+missing, put(inbox(mbAlice, "r2-"+missing), m))
	}

	// The preference record is optional: a registration that carries only the
	// two required fields is accepted, which is what makes `prefs` optional
	// rather than required by this generation.
	bare := aRegistration(mbAlice, "r3")
	delete(bare, "prefs")
	h.mustAllow(alice, "a registration with no preference record", put(inbox(mbAlice, "r3"), bare))

	// The kind gate, which is the fail-closed half: the registration's fields
	// on a message that is not a registration are refused, whatever kind it
	// claims and when it claims none at all (an absent kind reads as
	// 'message', the schema's DEFAULT_KIND). Without this conjunct a token or a
	// preference record would ride on a conversation's message, where the
	// consumer reads it as one. The `answer` and `action` arms are left to the
	// mailbox suite: their own KINDREQ records would be the first thing a case
	// built here failed on.
	for _, kind := range []string{"message", "prompt", "none"} {
		m := aMessage("k-"+kind, mbAlice)
		if kind != "none" {
			m["kind"] = kind
		}
		m["token"] = aPushToken
		m["prefs"] = aPrefs()
		h.mustDeny(alice, "the registration fields on a "+kind+" message", put(inbox(mbAlice, "k-"+kind), m))
	}
	// And the reverse direction of the same gate: the other kinds' own fields
	// are still refused on a registration, so the kind is a field set and not a
	// label a message may carry beside any shape it likes.
	prompt := aRegistration(mbAlice, "k-reg-options")
	prompt["options"] = []any{"yes", "no"}
	h.mustDeny(alice, "a registration carrying a prompt's options", put(inbox(mbAlice, "k-reg-options"), prompt))

	// The bounds the schema's own numbers set, each refused at the value the
	// schema refuses (design §6.4's field-by-field table, schema constants
	// TOKEN_MAX, ZONE_MAX and MINUTE_MAX).
	noSwitch := aPrefs()
	delete(noSwitch, "cap_near")
	notBool := aPrefs()
	notBool["prompt"] = "yes"
	sameEnds := aPrefs()
	sameEnds["quiet_hours"] = map[string]any{"zone": "Europe/Berlin", "start": 420, "end": 420}
	notAZone := aPrefs()
	notAZone["quiet_hours"] = map[string]any{"zone": "420", "start": 0, "end": 60}
	for i, c := range []struct {
		why   string
		field string
		value any
	}{
		{"a token past TOKEN_MAX", "token", strings.Repeat("t", MaxTokenLength+1)},
		{"an empty token", "token", ""},
		{"a platform that is neither ios nor android", "platform", "windows"},
		{"a preference record missing one of the five switches", "prefs", noSwitch},
		{"a preference record whose switch is not a boolean", "prefs", notBool},
		{"a quiet-hours window whose two ends are the same minute", "prefs", sameEnds},
		{"a quiet-hours zone that is not an IANA name", "prefs", notAZone},
	} {
		id := fmt.Sprintf("r4-%d", i)
		m := aRegistration(mbAlice, id)
		m[c.field] = c.value
		h.mustDeny(alice, c.why, put(inbox(mbAlice, id), m))
	}

	// A field the inbox does not name: the message's own `$other` refuses it,
	// which is what keeps a registration from carrying anything the schema has
	// no field for — the app's validator is not a fence the database can rely
	// on. And the mailbox's write-once guard: a registration is written once,
	// like every other message.
	unknown := aRegistration(mbAlice, "r5")
	unknown["nickname"] = "alice's phone"
	h.mustDeny(alice, "a registration carrying a field the inbox does not name", put(inbox(mbAlice, "r5"), unknown))
	h.mustDeny(alice, "rewriting its own registration", put(inbox(mbAlice, "r1"), aRegistration(mbAlice, "r1")))

	// The outbox is the same document — the schema holds the two field sets
	// equal, and the generated rules do too — so the shape is accepted there,
	// and the phone still cannot write it: the outbox is the consumer's, and
	// this generation grants the phone nothing beside its own inbox.
	h.mustAllow(h.consumer(mbAlice), "a registration in the outbox", put(outbox(mbAlice, "c1"), aRegistration(mbAlice, "c1")))
	h.mustDeny(alice, "the phone writing a registration into the outbox", put(outbox(mbAlice, "c2"), aRegistration(mbAlice, "c2")))

	// A session whose device node was never written is admitted nowhere: the
	// same write is refused, and so is every read the paired phone beside it
	// may make — the family's client reads are the outbox and the heartbeat,
	// and the inbox the phone writes into carries no read grant for any client
	// — and the control is alice being live on both (D166).
	h.mustRead(alice, mailbox(mbAlice)+"/outbox")
	h.mustRead(alice, beat(mbAlice))
	bob := h.unpaired(mbBob)
	h.mustDeny(bob, "a registration from a session with no device node", put(inbox(mbBob, "b1"), aRegistration(mbBob, "b1")))
	h.mustReadDenied(bob, mailbox(mbBob)+"/outbox")
	h.mustReadDenied(bob, beat(mbBob))

	// Revocation is final, and the device node is what carries it: `fugaro pair
	// revoke`'s delete (plan task P5.2.5's act, the admin path here) takes the
	// registration write away from the next request on, with no ID token
	// expiring first — and the message written before it is still there, so the
	// refusal is the revoke's and not the write-once guard's.
	h.unpairDevice(mbAlice)
	h.mustDeny(alice, "a registration from a revoked device", put(inbox(mbAlice, "r6"), aRegistration(mbAlice, "r6")))
	h.mustReadDenied(alice, mailbox(mbAlice)+"/outbox")
	h.wantValue(inbox(mbAlice, "r1")+"/token", `"`+aPushToken+`"`)
}

// TestPushSiblingStoreIsClientDeniedOnTheEmulator is the design's §3.4 claim
// against the engine: the store a registration is *applied* to is not one any
// client token reaches — the paired phone's own claim included, though its uid
// keys the node — because that node's only writer holds
// `roles/firebasedatabase.admin` and the rules carry no node under the path for
// a client grant to be written on.
//
// It is the emulator's half of TestDeviceNodesAreClientDenied: that test reads
// the generated document and proves no rule under the path exists, this one
// drives a database that really holds the node — the registration principal
// wrote it over IAM, which bypasses these rules — so each refusal is the rule's
// and not an absent node's.
func TestPushSiblingStoreIsClientDeniedOnTheEmulator(t *testing.T) {
	h := newHarness(t)
	alice := h.phone(mbAlice)
	store := pushStore(mbAlice)

	// The node as P5.2.4's principal writes it (§3.4: the token, the platform
	// and the instant it was updated), with §3.8's record and the sender's
	// ledger row beside it.
	h.set(store, map[string]any{"token": aPushToken, "platform": "ios", "updated_at": msgAt})
	h.set(store+"/prefs", aPrefs())
	h.set(store+"/ledger/e1", map[string]any{"sent_at": msgAt, "category": "cap_reached"})

	// Every identity the rules admit is refused every read and every write of
	// it, down to a single child.
	type identity struct {
		who string
		c   *rtdb.Client
	}
	run := h.run("aurora", "r1")
	for _, id := range []identity{
		{"the paired phone whose uid keys the store", alice},
		{"the mailbox consumer", h.consumer(mbAlice)},
		{"a pairing session", h.pairing(pairUid)},
		{"a run token", run},
		{"a session with no device node", h.unpaired(mbBob)},
	} {
		for _, p := range []string{store, store + "/prefs", store + "/ledger", store + "/ledger/e1"} {
			h.mustReadDenied(id.c, p)
		}
		h.mustDeny(id.c, id.who+" writing the store", put(store, map[string]any{"token": aPushToken, "platform": "ios", "updated_at": msgAt}))
		h.mustDeny(id.c, id.who+" writing a preference into the store", put(store+"/prefs", aPrefs()))
		h.mustDeny(id.c, id.who+" writing a ledger row", put(store+"/ledger/e2", map[string]any{"sent_at": msgAt, "category": "prompt"}))
		h.mustDeny(id.c, id.who+" deleting the store", map[string]any{store: nil})
		h.mustDeny(id.c, id.who+" deleting the token alone", map[string]any{store + "/token": nil})
	}

	// The controls, in one place, so that each refusal above is the store's and
	// not a client that is admitted nowhere: the node is really there and read
	// by the admin credential IAM gives — the sender's and the registration
	// principal's identity, never a client's — and each of those clients is
	// allowed where it belongs. The account with no device node has no such
	// place (D166), and the phone's read of the outbox beside it is the control
	// that says so.
	h.wantValue(store+"/token", `"`+aPushToken+`"`)
	h.wantValue(store+"/ledger/e1/category", `"cap_reached"`)
	h.mustAllow(alice, "the phone's own inbox message", put(inbox(mbAlice, "m1"), aMessage("m1", mbAlice)))
	h.mustRead(alice, mailbox(mbAlice)+"/outbox")
	h.mustReadDenied(h.unpaired(mbBob), mailbox(mbBob)+"/outbox")
	h.mustAllow(h.consumer(mbAlice), "the consumer's heartbeat", put(beat(mbAlice), aHeartbeat()))
	h.mustAllow(h.pairing(pairUid), "the pairing session's own request", put(request(pairUid), aRequest()))
	h.set(delivery(pairUid), map[string]any{"token": "sealed", "key": "sealed"})
	h.mustRead(h.pairing(pairUid), delivery(pairUid))
	h.mustRead(run, "config/caps/global")

	// And the inheritance the document relies on, from the client's side: the
	// parent the store inherits its refusal from is closed too, so nothing
	// about this store is reachable by a wildcard above it either.
	for _, p := range []string{"config/admin", "config/admin/devices/" + mbAlice} {
		h.mustReadDenied(alice, p)
	}
	h.mustDeny(alice, "the phone writing the device allowlist", put(PathDevices+"/"+mbBob, map[string]any{"pairedAt": msgAt}))
	h.mustDeny(alice, "the phone widening its own push store by a parent write", map[string]any{"config/admin/push": map[string]any{"other": true}})
}
