package mailbox

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The device signature's tests (J5b, O6): the second scheme the gate speaks, and
// the node's answer to which one a record must carry.
//
// **The red step this file was written for** is the downgrade case below: a
// MAC-tagged message at a node that holds a public key is *accepted* by a gate
// that knows only the MAC, and O6's whole point is that it must be refused. The
// other rows are the same property from the other sides — a signature made with
// another device's key, a signature over a body that changed, a signature read
// from the wrong node, and the mirror image at a node that holds no key at all.

// deviceSeed is a fixed, obviously fake seed: the device keys below are derived
// from it so that a test is the same test on every run, and no committed value
// is or could be a real key.
func deviceSeed(byteValue byte) []byte {
	return []byte(strings.Repeat(string([]byte{byteValue}), ed25519.SeedSize))
}

// deviceSigned is one record as a phone paired the new way writes it: the `mac`
// replaced by the device's own signature over the covered bytes in `direction`.
// It is the phone's half of O6 written in Go for the test — C2's `signMessage`
// is the real one, and the vectors are what holds the two to each other.
func deviceSigned(t *testing.T, record Record, key ed25519.PrivateKey, direction string) Record {
	t.Helper()
	canonical, err := CanonicalBytes(record, direction)
	if err != nil {
		t.Fatalf("CanonicalBytes(%s): %v", direction, err)
	}
	delete(record, "mac")
	record["sig"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, canonical))
	return record
}

// deviceGate is the gate a device node holding this key's public half is served
// by, at a clock level with the fixtures' timestamps.
func deviceGate(t *testing.T, key ed25519.PublicKey, now time.Time) *Gate {
	t.Helper()
	gate, err := NewSignatureGate(base64.RawURLEncoding.EncodeToString(key), GateOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("building the signature gate: %v", err)
	}
	return gate
}

// TestGateSignatureAcceptsTheDevicesOwnSignature is the honest path: a message
// the device signed over its own canonical bytes is a message the node's gate
// hands on, and the message it answers carries the signature and not a MAC.
func TestGateSignatureAcceptsTheDevicesOwnSignature(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	gate := deviceGate(t, device.Public().(ed25519.PublicKey), now)

	record := deviceSigned(t, withAt(plainMessage(t), now.UnixMilli()), device, directionInbox)
	message, refusal := gate.Verify(record)
	if refusal != nil {
		t.Fatalf("the gate refused the device's own signature (%s at %q)", refusal.Code, refusal.Field)
	}
	if message == nil {
		t.Fatal("the gate accepted a message and answered no message")
	}
	if message.Sig != text(t, record["sig"], "sig") {
		t.Errorf("the message the gate answered carries another signature: %q", message.Sig)
	}
	if message.MAC != "" {
		t.Errorf("the message the gate answered carries a MAC (%q) as well as a signature", message.MAC)
	}
}

// TestGateSignatureRefusesAMACTaggedMessage is the plan's red step, and the
// fence O6 exists for. A node that holds a public key takes signatures only, so
// a record carrying a MAC — a real one, made with the pairing's old key, or any
// other — is refused rather than checked: checking it would be accepting a
// scheme the device has retired, and the key that made it has three homes on
// the owner's machine.
func TestGateSignatureRefusesAMACTaggedMessage(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	gate := deviceGate(t, device.Public().(ed25519.PublicKey), now)

	// The pairing's own MAC key, which is exactly what makes this the downgrade
	// case: the tag is the right one for a node that holds no public key.
	mac := signed(t, withAt(plainMessage(t), now.UnixMilli()), vectorKey(t), directionInbox)
	message, refusal := gate.Verify(mac)
	if refusal == nil {
		t.Fatal("the gate accepted a MAC-tagged message at a node that holds a public key: that is the downgrade O6 forbids")
	}
	if refusal.Code != CodeBadMAC || refusal.Field != "mac" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeBadMAC, Field: "mac"}, "mac")
	}
	if message != nil {
		t.Errorf("the gate answered a message AND a refusal: %v", message)
	}
}

// TestGateSignatureRefusesAnotherDevicesKey is the other forger: a signature
// that is real, made over the right bytes in the right direction, by a device
// that is not this node's. Nothing about the record says so — the bytes are the
// device's own — which is why the key is the only thing that can refuse it.
func TestGateSignatureRefusesAnotherDevicesKey(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	other := ed25519.NewKeyFromSeed(deviceSeed('b'))
	gate := deviceGate(t, device.Public().(ed25519.PublicKey), now)

	record := deviceSigned(t, withAt(plainMessage(t), now.UnixMilli()), other, directionInbox)
	message, refusal := gate.Verify(record)
	if refusal == nil {
		t.Fatal("the gate accepted a message signed by another device's key")
	}
	if refusal.Code != CodeBadSig || refusal.Field != "sig" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeBadSig, Field: "sig"}, "sig")
	}
	if message != nil {
		t.Errorf("the gate answered a message AND a refusal: %v", message)
	}
}

// TestGateSignatureRefusesASignatureAtAMACNode is the scheme choice read from
// the other end: a node that holds no public key is a device paired the old
// way, and it accepts a MAC only. A signature there is refused rather than
// ignored — a record accepted on no evidence at all is worse than one refused
// for carrying the wrong tag.
func TestGateSignatureRefusesASignatureAtAMACNode(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	gate := NewGate(keychain.NewKey([]byte(vectorKey(t))), GateOptions{Now: func() time.Time { return now }})

	record := deviceSigned(t, withAt(plainMessage(t), now.UnixMilli()), device, directionInbox)
	message, refusal := gate.Verify(record)
	if refusal == nil {
		t.Fatal("a MAC node accepted a signature-tagged message")
	}
	if refusal.Code != CodeBadSig || refusal.Field != "sig" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeBadSig, Field: "sig"}, "sig")
	}
	if message != nil {
		t.Errorf("the gate answered a message AND a refusal: %v", message)
	}
	// And the same node still accepts the MAC-tagged record it is for, so the
	// refusal above is the scheme and not the node refusing everything.
	if _, refusal := gate.Verify(signed(t, withAt(plainMessage(t), now.UnixMilli()), vectorKey(t), directionInbox)); refusal != nil {
		t.Errorf("the MAC node refused a MAC-tagged message (%s at %q)", refusal.Code, refusal.Field)
	}
}

// TestGateSignatureRefusesATamperedMessage is the signature's own version of
// the file's central forgery: a body changed under a signature that was made
// for another one. The covered bytes are `canonicalBytes`'s, so every field of
// the schema is in them and a swap of any of them is a refusal.
func TestGateSignatureRefusesATamperedMessage(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	gate := deviceGate(t, device.Public().(ed25519.PublicKey), now)

	record := deviceSigned(t, withAt(plainMessage(t), now.UnixMilli()), device, directionInbox)
	tampered := cloneRecord(t, record)
	tampered["text"] = "cancel everything"
	if _, refusal := gate.Verify(tampered); refusal == nil {
		t.Fatal("the gate accepted a body under a signature made for another body")
	} else if refusal.Code != CodeBadSig || refusal.Field != "sig" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeBadSig, Field: "sig"}, "sig")
	}
	// A field the schema does not cover cannot be smuggled in beside a valid
	// signature: an unknown field is the schema's refusal, before the tag.
	unknown := cloneRecord(t, record)
	unknown["extra"] = "x"
	if _, refusal := gate.Verify(unknown); refusal == nil {
		t.Fatal("the gate accepted a record with a field the schema does not name")
	} else if refusal.Code != CodeUnknownField || refusal.Field != "extra" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeUnknownField, Field: "extra"}, "extra")
	}
}

// TestGateSignatureRefusesAWrongDirection is the direction's half: the bytes a
// signature covers carry the node the record was read from, so a signature the
// device made for its own outbox does not verify as an inbox message. Without
// it, a message the phone wrote for one node would be a message in the other.
func TestGateSignatureRefusesAWrongDirection(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	gate := deviceGate(t, device.Public().(ed25519.PublicKey), now)

	record := deviceSigned(t, withAt(plainMessage(t), now.UnixMilli()), device, directionOutbox)
	if _, refusal := gate.Verify(record); refusal == nil {
		t.Fatal("the gate accepted a signature made for the outbox as an inbox message")
	} else if refusal.Code != CodeBadSig || refusal.Field != "sig" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeBadSig, Field: "sig"}, "sig")
	}
}

// TestGateSignatureKeepsTheSchemaAndTheClock is the rest of §8.1's order, which
// the scheme choice sits inside and does not replace: a record the schema
// refuses is refused with the schema's own code, and a correctly signed message
// whose `at` is beyond the tolerance is refused for its time. A gate that
// checked the signature first would still be wrong about the clock, and one
// that checked nothing else would be wrong about the record.
func TestGateSignatureKeepsTheSchemaAndTheClock(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	gate := deviceGate(t, device.Public().(ed25519.PublicKey), now)

	if _, refusal := gate.Verify(vectorRefusal(t, "unsigned message")); refusal == nil {
		t.Fatal("the gate accepted the file's own unsigned message")
	} else if refusal.Code != CodeMissingField || refusal.Field != "mac" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeMissingField, Field: "mac"}, "mac")
	}
	if _, refusal := gate.Verify(nil); refusal == nil {
		t.Fatal("the gate accepted a record that is not an object")
	} else if refusal.Code != CodeNotAnObject {
		t.Errorf("refused %s, want %s", refusal, &Refusal{Code: CodeNotAnObject})
	}

	future := deviceSigned(t, withAt(plainMessage(t), now.UnixMilli()+FutureTolerance.Milliseconds()+1), device, directionInbox)
	if _, refusal := gate.Verify(future); refusal == nil {
		t.Fatal("the gate accepted a correctly signed message stamped past the tolerance")
	} else if refusal.Code != CodeFutureTimestamp || refusal.Field != "at" {
		t.Errorf("refused %s, want %s at %q", refusal, &Refusal{Code: CodeFutureTimestamp, Field: "at"}, "at")
	}
}

// TestNewSignatureGateRefusesAKeyThatIsNotOne pins the constructor's own
// refusal: a node whose `public_key` cannot be read is a node this gate cannot
// be built for, and the caller's answer is to serve no gate rather than to fall
// back to the MAC — a fallback would accept exactly the tag O6 retires.
func TestNewSignatureGateRefusesAKeyThatIsNotOne(t *testing.T) {
	good := base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(deviceSeed('a')).Public().(ed25519.PublicKey))
	for _, c := range []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"a key that is not base64url", strings.Repeat("*", len(good))},
		{"a key one character short", good[:len(good)-1]},
		{"a key one character long", good + "A"},
		{"a padded key", good + "="},
		{"the MAC key's own spelling", vectorKey(t)},
		// The last character of a 32-byte key carries two bits the encoding
		// discards, so a key whose discarded bits are not zero is a second
		// spelling of one key — and a gate that accepted it would verify
		// against bytes no phone ever published.
		{"a non-canonical last character", good[:len(good)-1] + "B"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewSignatureGate(c.key, GateOptions{}); err == nil {
				t.Fatalf("NewSignatureGate accepted %q as a device public key", c.key)
			}
		})
	}
	if _, err := NewSignatureGate(good, GateOptions{}); err != nil {
		t.Errorf("NewSignatureGate refused the device's own key: %v", err)
	}
}

// TestGateSignatureIsWhatALogLineSays pins the one thing a gate prints about
// itself: which scheme it takes, and never a byte of a key. It is the reason
// String exists at all — a log line that said only "mailbox.Gate" would not
// tell a reader which of the two schemes refused a message.
func TestGateSignatureIsWhatALogLineSays(t *testing.T) {
	device := ed25519.NewKeyFromSeed(deviceSeed('a'))
	signature := deviceGate(t, device.Public().(ed25519.PublicKey), time.UnixMilli(1700000000000))
	if got := signature.String(); !strings.Contains(got, "device signature") {
		t.Errorf("a signature gate's log line does not say which scheme it takes: %s", got)
	}
	mac := NewGate(keychain.NewKey([]byte(vectorKey(t))), GateOptions{})
	if got := mac.String(); !strings.Contains(got, "MAC") {
		t.Errorf("a MAC gate's log line does not say which scheme it takes: %s", got)
	}
	for _, gate := range []*Gate{signature, mac} {
		if got := gate.String(); strings.Contains(got, vectorKey(t)) {
			t.Errorf("a gate's log line carries key material: %s", got)
		}
	}
}
