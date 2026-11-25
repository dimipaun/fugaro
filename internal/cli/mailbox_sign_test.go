package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The device signature at the consumer (J5b, O6): the gate a device node's
// public key builds, the startup that no longer needs a keychain item, and the
// node's own absence.
//
// **The red step this file was written for** is the downgrade: a MAC-tagged
// message at a node that holds a public key is refused rather than checked. The
// mailbox package's own tests hold the gate itself to that (verify_test.go);
// what is tested here is this command's half — which key the gate is built from,
// what a consumer that holds no key for a pairing does, and what deleting the
// node does to a pass.

// mailboxDeviceUID is the uid a phone paired the new way carries — the second
// pairing these tests use, in the same shape as mailboxUID. Nothing pairs it
// through `fugaro pair`: its keychain item is absent and its device node is what
// names it.
const mailboxDeviceUID = "m~BBBBBBBBBBBBBBBBBBBBBB"

// mailboxDeviceSeed is a fixed, obviously fake seed: the device key below is
// derived from it so that a test is the same test on every run, and no committed
// value is or could be a real key.
func mailboxDeviceSeed(byteValue byte) []byte {
	return []byte(strings.Repeat(string([]byte{byteValue}), ed25519.SeedSize))
}

// mailboxSignatureDevice is the device keypair these tests sign with.
func mailboxSignatureDevice(byteValue byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(mailboxDeviceSeed(byteValue))
}

// mailboxSeedSignatureDeviceNode writes the device node as the pairing endpoint
// writes it (P3's MobileDeviceNode): the address, the roster's role, the
// device's own public key and the moment — and no `name`, which is the one way
// it differs from the node `fugaro pair` writes.
func mailboxSeedSignatureDeviceNode(f *mailboxFixture, uid string, key ed25519.PublicKey) {
	f.db.Set(rules.PathDevices+"/"+uid, map[string]any{
		"email": "owner@example.com", "role": "owner",
		"public_key": base64.RawURLEncoding.EncodeToString(key),
		"at":         "2026-11-17T00:00:00Z",
	})
}

// mailboxDeviceRecord is one record as a phone paired the new way writes it: the
// device's own signature over the covered bytes in place of a `mac`, which is
// C2's `signMessage` over the schema's `canonicalBytes` written in Go for the
// test. It is the mailbox package's own `deviceSigned` (verify_test.go) again,
// because a helper in one package's _test.go file is not reachable from
// another's.
func mailboxDeviceRecord(t *testing.T, id string, at int64, key ed25519.PrivateKey) map[string]any {
	t.Helper()
	record := map[string]any{"id": id, "sender": mailboxDeviceUID, "text": "hello from the new phone", "at": at}
	canonical, err := mailbox.CanonicalBytes(record, "inbox")
	if err != nil {
		t.Fatalf("CanonicalBytes(inbox): %v", err)
	}
	record["sig"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, canonical))
	return record
}

// mailboxPairingFor finds the pairing a uid is served by in the keyring a
// consumer was built with.
func mailboxPairingFor(t *testing.T, c *mailboxConsumer, uid string) mailboxPairing {
	t.Helper()
	for _, pairing := range c.pairings {
		if pairing.uid == uid {
			return pairing
		}
	}
	t.Fatalf("no pairing is served for %s: %v", uid, c.pairings)
	return mailboxPairing{}
}

// TestMailboxKeyringBuildsTheGateFromTheDeviceNode is the CLI half of O6: the
// node's `public_key` is what the pairing's gate verifies against, and the node
// chooses the scheme — a MAC-tagged record, even one signed with the pairing's
// own key, is refused rather than checked, and another device's signature is
// refused with it. **The project config names no item at all here**, which is
// the state a phone paired from the app leaves: the node is the only thing on
// this machine that names the pairing.
func TestMailboxKeyringBuildsTheGateFromTheDeviceNode(t *testing.T) {
	f := newMailboxFixture(t, "# no mailbox block at all\n")
	device := mailboxSignatureDevice('a')
	mailboxSeedSignatureDeviceNode(f, mailboxDeviceUID, device.Public().(ed25519.PublicKey))

	c := f.consumer(t, "boot-sign-test")
	pairing := mailboxPairingFor(t, c, mailboxDeviceUID)
	if pairing.revoked || pairing.gate == nil {
		t.Fatalf("the pairing the device node names is served as %+v, want a gate and no revocation", pairing)
	}

	now := mailboxNowMs()
	// The device's own signature over its own bytes is what the gate hands on.
	if _, refusal := pairing.gate.Verify(mailboxDeviceRecord(t, "msg-1", now, device)); refusal != nil {
		t.Fatalf("the gate refused the device's own signature (%s at %q)", refusal.Code, refusal.Field)
	}
	// The downgrade O6 forbids: the pairing's MAC key is real and this machine
	// holds it, and the record is refused anyway, because a node that holds a
	// public key takes signatures only.
	if _, refusal := pairing.gate.Verify(f.signed(t, "msg-1", nil, now, mailboxKeyMaterial)); refusal == nil {
		t.Fatal("the gate accepted a MAC-tagged message at a node that holds a public key: that is the downgrade O6 forbids")
	} else if refusal.Code != mailbox.CodeBadMAC || refusal.Field != "mac" {
		t.Fatalf("refused %s, want %s at %q", refusal, &mailbox.Refusal{Code: mailbox.CodeBadMAC, Field: "mac"}, "mac")
	}
	// And another device's key is the other forger: the bytes are a device's
	// own, and only the key can refuse them.
	if _, refusal := pairing.gate.Verify(mailboxDeviceRecord(t, "msg-2", now, mailboxSignatureDevice('b'))); refusal == nil {
		t.Fatal("the gate accepted a message signed by another device's key")
	} else if refusal.Code != mailbox.CodeBadSig || refusal.Field != "sig" {
		t.Fatalf("refused %s, want %s at %q", refusal, &mailbox.Refusal{Code: mailbox.CodeBadSig, Field: "sig"}, "sig")
	}
}

// TestMailboxServesADeviceNodeWithNoKeychainItem is the acceptance's startup
// row: a project whose config names no keychain item is no longer refused when a
// device node holds a public key, because there is a message this consumer can
// verify. What it cannot do is sign — the consumer's own key for a phone paired
// the new way is J5c's KMS signer — so the pass takes no lease and acts on
// nothing, which is M4b's row and not a failure: exit 0, nothing written.
func TestMailboxServesADeviceNodeWithNoKeychainItem(t *testing.T) {
	f := newMailboxFixture(t, "# no mailbox block at all\n")
	device := mailboxSignatureDevice('a')
	mailboxSeedSignatureDeviceNode(f, mailboxDeviceUID, device.Public().(ed25519.PublicKey))
	f.db.Set("mailbox/"+mailboxDeviceUID+"/inbox/msg-1", mailboxDeviceRecord(t, "msg-1", mailboxNowMs(), device))

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a device node that holds a public key is a pairing this command can verify", err)
	}
	doc := mailboxJSON(t, out)
	if got, _ := doc["mailbox"].(string); got != mailboxDeviceUID {
		t.Fatalf("mailbox = %q, want %q: the pass is the node's own pairing", got, mailboxDeviceUID)
	}
	if lease, _ := doc["lease"].(map[string]any); lease["state"] != string(mailbox.LeaseLost) {
		t.Fatalf("lease = %v, want %s: no key on this machine signs this pairing's heartbeat", lease, mailbox.LeaseLost)
	}
	if got := mailboxCount(t, doc, "read"); got != 0 {
		t.Fatalf("read = %d, want 0: no lease, so nothing is read and nothing is acted on (§7)", got)
	}
	if warnings, _ := doc["warnings"].([]any); len(warnings) != 1 || !strings.Contains(warnings[0].(string), "could not be signed") {
		t.Fatalf("warnings = %v, want one saying the heartbeat could not be signed", doc["warnings"])
	}
	if f.db.Value("mailbox/"+mailboxDeviceUID+"/heartbeat") != nil {
		t.Fatal("a heartbeat was written for a pairing this machine holds no key for")
	}
	if f.db.Value("mailbox/"+mailboxDeviceUID+"/outbox") != nil {
		t.Fatal("a reply was written for a pairing this machine holds no key for")
	}
}

// TestMailboxDeletedDeviceNodeEndsAcceptance is O6's "deleting the device node
// ends acceptance at once", read at the one place it can be read: the node is
// checked again before a pass acts, so a node deleted while this consumer runs
// ends that pairing at its next pass rather than at its next start. The pairing
// here carries the signers J5c's KMS signer will give it — without one the pass
// takes no lease at all (the test above) and the check would be unobservable —
// and what the deletion changes is that the message the node's key signed is
// read, delivered and replied to before it and read by nobody after.
func TestMailboxDeletedDeviceNodeEndsAcceptance(t *testing.T) {
	f := newMailboxFixture(t, "")
	device := mailboxSignatureDevice('a')
	mailboxSeedSignatureDeviceNode(f, mailboxDeviceUID, device.Public().(ed25519.PublicKey))
	f.db.Set("mailbox/"+mailboxDeviceUID+"/inbox/msg-1", mailboxDeviceRecord(t, "msg-1", mailboxNowMs(), device))

	c := f.consumer(t, "boot-gone-test")
	pairing := mailboxPairingFor(t, c, mailboxDeviceUID)
	key := keychain.NewKey([]byte(mailboxKeyMaterial))
	pairing.signer = mailboxOutboxSigner(key)
	pairing.heartbeat = mailboxHeartbeatSigner(key)

	first, err := c.pass(t.Context(), pairing)
	if err != nil {
		t.Fatalf("the pass over a paired device: %v", err)
	}
	if first.verdict.delivered != 1 || first.lease.State != string(mailbox.LeaseHeld) {
		t.Fatalf("the first pass delivered %d with lease %s, want 1 delivered under a held lease", first.verdict.delivered, first.lease.State)
	}

	// The pairing endpoint's retirement, or an operator's `pair revoke`: the node
	// is gone and the phone's key is the only thing that ever named it.
	f.db.Set(rules.PathDevices+"/"+mailboxDeviceUID, nil)
	second, err := c.pass(t.Context(), pairing)
	if err != nil {
		t.Fatalf("the pass over a deleted device: %v", err)
	}
	if second.verdict.read != 0 {
		t.Fatalf("read = %d, want 0: a node that no longer holds the key its gate was built from is not served", second.verdict.read)
	}
	if len(second.notes) != 1 || !strings.Contains(second.notes[0], "no longer holds the public key") {
		t.Fatalf("notes = %v, want one naming the device node", second.notes)
	}
}
