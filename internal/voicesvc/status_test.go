package voicesvc

// The one database check's tests. The one that matters is revocation: the
// device node's deletion is the whole of it (D166), and the very next call must
// be refused.

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestDeviceExistsReadsTheNodeD166Pins reads the node at the path the rest of
// this tree writes a device to — internal/budget's DeviceRoot, which a test of
// that package holds to the rules template's own spelling.
func TestDeviceExistsReadsTheNodeD166Pins(t *testing.T) {
	db := newTestDB(t)
	d := RTDBDevices{DB: db.Client}

	if ok, err := d.DeviceExists(context.Background(), testUID); ok || ReasonOf(err) != ReasonDeviceUnknown {
		t.Fatalf("a device with no node reads (%v, %v), want (false, %q)", ok, err, ReasonDeviceUnknown)
	}

	db.Set(budget.DeviceRoot+"/"+testUID, map[string]any{"role": "owner"})
	ok, err := d.DeviceExists(context.Background(), testUID)
	if err != nil {
		t.Fatalf("DeviceExists: %v", err)
	}
	if !ok {
		t.Fatal("a paired device reads as absent")
	}
}

// TestRevocationRefusesTheVeryNextCall is D166's property, end to end: a paired
// device is answered, its node is deleted — which is what revocation is — and
// the NEXT call is refused with device_unknown. Nothing is cached and no list
// has to be re-published, so a revoked phone stops on its next request rather
// than when a token expires.
func TestRevocationRefusesTheVeryNextCall(t *testing.T) {
	h := newTestHop(t)
	h.pair(t)
	h.db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{
		"providers": []any{"deepgram"},
		"direction": map[string]any{"stt": true},
	})
	tok := h.deviceToken(t)
	body := `{"project":"` + testSlug + `"}`

	rec := h.status(t, tok, body)
	if rec.Code != 200 {
		t.Fatalf("a paired device was refused: %d %s", rec.Code, rec.Body.String())
	}

	h.revoke()

	rec = h.status(t, tok, body)
	if rec.Code != 403 {
		t.Fatalf("a revoked device answered %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), ReasonDeviceUnknown) {
		t.Fatalf("the refusal reads %s, want %q", rec.Body.String(), ReasonDeviceUnknown)
	}
}

// TestDeviceExistsRefusesAUIDThatIsNotADevices: this is the one place a
// caller-supplied string becomes a database path. A uid that is not a device's
// would be a read of a node no device owns, so it is refused by name.
func TestDeviceExistsRefusesAUIDThatIsNotADevices(t *testing.T) {
	db := newTestDB(t)
	db.Set(budget.DeviceRoot+"/"+testRunUID, map[string]any{"role": "owner"})
	d := RTDBDevices{DB: db.Client}

	for _, uid := range []string{testRunUID, "", "config/admin", "../admin", "m~short"} {
		if ok, err := d.DeviceExists(context.Background(), uid); ok || ReasonOf(err) != ReasonNotADevice {
			t.Errorf("DeviceExists(%q) = (%v, %v), want (false, %q)", uid, ok, err, ReasonNotADevice)
		}
	}
}

// TestADeviceReadFailureIsStateAndNotARevocation is the difference the app's
// sentence depends on: a phone told "your device was revoked" stops and asks
// its owner, while a phone told "this service cannot tell" serves the speech
// locally and carries on. Only a node that is really absent is the first.
func TestADeviceReadFailureIsStateAndNotARevocation(t *testing.T) {
	db := newTestDB(t)
	db.DenyRead(budget.DeviceRoot)
	d := RTDBDevices{DB: db.Client}

	ok, err := d.DeviceExists(context.Background(), testUID)
	if ok {
		t.Fatal("a device read as live when every read fails")
	}
	if got := ReasonOf(err); got != ReasonStateUnavailable {
		t.Fatalf("ReasonOf = %q, want %q (%v)", got, ReasonStateUnavailable, err)
	}
}

// TestDeviceExistsWithoutADatabaseIsState: a wiring bug is a refusal this
// service can answer, not a panic a call would take down.
func TestDeviceExistsWithoutADatabaseIsState(t *testing.T) {
	if _, err := (RTDBDevices{}).DeviceExists(context.Background(), testUID); ReasonOf(err) != ReasonStateUnavailable {
		t.Fatalf("DeviceExists = %v (%q), want %q", err, ReasonOf(err), ReasonStateUnavailable)
	}
}

// TestDeviceNodeIsNotTheOnlyNodeTheAccountCanReach is the disclosure's own
// claim, checked where it is cheapest: the account's grant is a read of the
// whole instance (voice.tf), so this reader is correct only because it asks for
// this one node — and the node it asks for is the device's own path and never a
// parent, because a read of a parent would be a read of every device.
func TestDeviceNodeIsNotTheOnlyNodeTheAccountCanReach(t *testing.T) {
	db := newTestDB(t)
	other := "m~BBBBBBBBBBBBBBBBBBBBBB"
	db.Set(budget.DeviceRoot+"/"+testUID, map[string]any{"role": "owner"})
	db.Set(budget.DeviceRoot+"/"+other, map[string]any{"role": "owner"})
	d := RTDBDevices{DB: db.Client}

	if ok, err := d.DeviceExists(context.Background(), testUID); !ok || err != nil {
		t.Fatalf("DeviceExists = (%v, %v)", ok, err)
	}
	if ok, err := d.DeviceExists(context.Background(), other); !ok || err != nil {
		t.Fatalf("DeviceExists(other) = (%v, %v)", ok, err)
	}
	// And a device that has no node is refused even when others do: the reader
	// asks for one node, not for the allowlist.
	if _, err := d.DeviceExists(context.Background(), "m~CCCCCCCCCCCCCCCCCCCCCC"); ReasonOf(err) != ReasonDeviceUnknown {
		t.Fatalf("an unpaired device among paired ones reads %v, want %q", err, ReasonDeviceUnknown)
	}
}
