// The hop's one database check, and the answer the app asks for.
//
// The check is the device node at config/admin/devices/$uid, and it is the
// second half of "the caller is a device": the token says which device is
// calling, and the node says whether that device is still one. It is the
// revocation bound of D166 — `fugaro pair revoke` (and the sweep) revokes a
// device by DELETING that node, so a call from a revoked phone is refused on
// its very next request with no cache to expire and no list to re-publish. A
// test pins exactly that sequence: admitted, then the node deleted, then
// refused.
//
// Why the node and not a claim: a claim is minted once and lives for an hour,
// so a revocation carried in the token would take an hour to bite, and the
// rules already treat the node's existence as the whole of a device's
// standing (rules.go's userClaim). The hop reads the same node for the same
// reason — one authority for one fact.
//
// It is also the reason this service's account has an instance-wide read at
// all (voice.tf): Realtime Database IAM has no path-level scope, so
// firebasedatabase.instances.get is a read of ANY node, and the two paths this
// package builds are a property of this code and not of the role. SECURITY.md
// states it for that reason.
package voicesvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/budget"
)

// DeviceReader answers whether a device's node exists. It is an interface so
// the server's tests can decide a device's standing without a database, and so
// P5.3.5's forwarding half can ask the same question without a second reader.
type DeviceReader interface {
	// DeviceExists reports whether uid is a live device — paired and not
	// revoked. A uid that is not a device's is refused rather than answered
	// false: the path below is built from this string, and a caller that
	// reached here with one has a bug this refusal names.
	DeviceExists(ctx context.Context, uid string) (bool, error)
}

// RTDBDevices reads the device allowlist over IAM.
type RTDBDevices struct {
	DB Getter
}

// DeviceExists reads the one node.
//
// A read that FAILS is not "the device is gone": it is state this service could
// not reach, and the app is told to fall back to local rather than that its
// device was revoked. Only a node that is really absent — which is what a
// deleted node reads as, and what a node that was never written reads as — is
// the revocation refusal.
func (d RTDBDevices) DeviceExists(ctx context.Context, uid string) (bool, error) {
	if !IsDeviceUID(uid) {
		// Unreachable through the server, which verifies the token first, and
		// kept for that reason: this is the one place a caller-supplied string
		// becomes a database path, and a path built from a string that is not a
		// device's is a read of a node no device owns.
		return false, refuse(ReasonNotADevice, errors.New("voicesvc: the device node path is built from a uid that is not a device's"))
	}
	if d.DB == nil {
		return false, refuse(ReasonStateUnavailable, errors.New("voicesvc: the device reader holds no database"))
	}
	found, err := d.DB.Get(ctx, budget.DeviceRoot+"/"+uid, nil)
	if err != nil {
		return false, refuse(ReasonStateUnavailable, fmt.Errorf("voicesvc: reading the device node: %w", err))
	}
	if !found {
		return false, refuse(ReasonDeviceUnknown, errors.New("voicesvc: no device node exists for this uid: the device was never paired, or it has been revoked, and revocation deletes this node (D166)"))
	}
	return true, nil
}
