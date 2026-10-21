//go:build firebase

package rules

import (
	"strings"
	"testing"
)

// The emulator cases for the orchestrator access record (central-orchestrator
// plan P2, decisions D6 and D12): the device node `config/admin/devices/<uid>`
// *extended* with the email an administrator writes and the role that node
// carries, which is the grant on the orchestrators lease above.
//
// The unit tests in rules_test.go pin the predicate's shape in the generated
// document — every conjunct, and that no other family carries the email. What
// only the emulator can prove is the refusal itself, on the real rules engine:
// a device node the operator wrote and no administrator has administered an
// email and a role into reaches no seat, while every family the phone already
// had is untouched. That is the state `fugaro pair` leaves an installation in
// (pairing is discovery, D6/D165) and the moment before `fugaro orchestrator
// add` — which is why it is the state worth measuring rather than asserting.
//
// The node is always written through the harness's admin path, which is what
// the operator's own credentials are: no client token holds a grant on
// config/admin (the template denies it, and the emulator would refuse a client
// write there for that reason, not for this one).

// TestLeaseAnUnadministeredDeviceIsRefused is the plan's red step, at the
// database: a device node with **no email** is refused the seat, and the same
// write from the same account lands the moment the email and the role are
// there. The body is one the transition accepts in every other respect
// (createBody, which the case above in the lease file uses for exactly this
// reason), so the access record is the only thing left to refuse it.
//
// Hand mutation this fails on: dropping the email conjunct from ORCH — the
// paired device takes the seat with no administrator behind it, which is the
// state a generation-4 document is in.
func TestLeaseAnUnadministeredDeviceIsRefused(t *testing.T) {
	h := newHarness(t)
	h.pairDeviceUnadministered(carolUID)

	// Paired, and the record has no email and no role: refused.
	h.rawDenied(claims{mu: carolUID, fp: "-"}, "PUT", leasePath, createBody(carolUID))
	if got := h.value(leasePath); got != "null" {
		t.Fatalf("after the refused create the lease is %s, want it absent: the refusal wrote something", got)
	}

	// Administering the identity is the only change, and now the write lands —
	// so the refusal above was the access record and nothing else.
	h.administer(carolUID, carolUID+"@example.test", RoleOrchestratorMember)
	carol := h.as(claims{mu: carolUID, fp: "-"})
	if err := take(h, carol, leasePath, leaseRecord(carolUID, 1)); err != nil {
		t.Fatalf("the same account after administration, on the same absent node: %v, want the create accepted", err)
	}
}

// TestLeaseAnAdministeredDeviceNeedsTheRole: the email alone is not the record.
// A node with an administered email and no role is refused, and so is one whose
// role is a value this release does not document. Both are `false` rather than
// an error the operator has to interpret, and the case asserts the node is
// still absent afterwards.
//
// Hand mutation this fails on: dropping the role conjunct (a half-administered
// record admits), or widening it to "role exists" (an undocumented role admits,
// which is a permission nobody granted).
func TestLeaseAnAdministeredDeviceNeedsTheRole(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		why  string
		node map[string]any
	}{
		{"an administered email and no role", map[string]any{"pairedAt": msgAt, "email": carolUID + "@example.test"}},
		{"a role this release does not document", map[string]any{"pairedAt": msgAt, "email": carolUID + "@example.test", "role": "owner"}},
		{"a role that is not a string", map[string]any{"pairedAt": msgAt, "email": carolUID + "@example.test", "role": 1}},
		{"an email that is not a string", map[string]any{"pairedAt": msgAt, "email": 1, "role": RoleOrchestratorMember}},
		{"an empty email", map[string]any{"pairedAt": msgAt, "email": "", "role": RoleOrchestratorMember}},
		{"an empty role", map[string]any{"pairedAt": msgAt, "email": carolUID + "@example.test", "role": ""}},
	} {
		h.wipe()
		h.set(PathDevices+"/"+carolUID, c.node)
		h.rawDenied(claims{mu: carolUID, fp: "-"}, "PUT", leasePath, createBody(carolUID))
		if got := h.value(leasePath); got != "null" {
			t.Fatalf("%s: after the refused create the lease is %s, want it absent", c.why, got)
		}
	}
	// And the two documented roles both admit, so the list above is a
	// vocabulary and not one value with a decoy.
	for _, role := range []string{RoleOrchestratorMember, RoleOrchestratorAdmin} {
		h.wipe()
		h.administer(carolUID, carolUID+"@example.test", role)
		carol := h.as(claims{mu: carolUID, fp: "-"})
		if err := take(h, carol, leasePath, leaseRecord(carolUID, 1)); err != nil {
			t.Fatalf("role %q: %v, want the create accepted", role, err)
		}
		h.adminDo("DELETE", leasePath, nil)
	}
}

// TestLeaseTheAdministeredEmailIsBounded: the rules bound the field they cannot
// validate. A 254-character address is the longest one that can exist (RFC
// 5321) and is admitted; 255 is refused. Neither is checked for being an email
// at all — D6 puts the domain rule and the verified-email rule at
// *administration*, where the command can read the operator's answer, and the
// rules carry only their outcome.
//
// Hand mutation this fails on: dropping the length bound (an unbounded field
// somebody can grow without limit), or lifting it above 254.
func TestLeaseTheAdministeredEmailIsBounded(t *testing.T) {
	h := newHarness(t)
	// The longest address that can exist: 64 + 1 + 185 + 1 + 3 = 254.
	longest := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 185) + ".co"
	if len(longest) != MaxEmailLength {
		t.Fatalf("the fixture is %d characters, want MaxEmailLength (%d)", len(longest), MaxEmailLength)
	}
	h.administer(aliceUID, longest, RoleOrchestratorMember)
	alice := h.as(claims{mu: aliceUID, fp: "-"})
	if err := take(h, alice, leasePath, leaseRecord(aliceUID, 1)); err != nil {
		t.Fatalf("a %d-character address: %v, want the create accepted", len(longest), err)
	}
	h.adminDo("DELETE", leasePath, nil)

	h.administer(aliceUID, longest+"x", RoleOrchestratorMember)
	h.rawDenied(claims{mu: aliceUID, fp: "-"}, "PUT", leasePath, createBody(aliceUID))
	if got := h.value(leasePath); got != "null" {
		t.Fatalf("after the refused create the lease is %s, want it absent", got)
	}
}

// TestAdministeringAnIdentityDoesNotTouchThePhone: the record's blast radius.
// The same paired-but-unadministered account that cannot take a seat (the red
// step above) still reads the families D166 admits it to — its caps, the kill
// switches — so administration is a second, narrower revocation and never a
// lockout of the app. An operator who administers an orchestrator identity, or
// takes it away, moves nothing but the lease.
//
// Hand mutation this fails on: putting ORCH on a family outside the
// orchestrators subtree (the caps stop being readable by a paired phone, which
// is the D166 lockout generation 3 was careful to describe), or widening
// userClaim itself to require the email.
func TestAdministeringAnIdentityDoesNotTouchThePhone(t *testing.T) {
	h := newHarness(t)
	h.pairDeviceUnadministered(carolUID)
	carol := h.as(claims{mu: carolUID, fp: "-"})

	for _, p := range []string{"config/caps/global", "config/caps/defaults", "config/kill/global"} {
		h.mustRead(carol, p)
	}
	// And the seat is the one thing it does not reach, which is what makes the
	// reads above a statement about the blast radius rather than about the
	// grant being absent altogether.
	h.mustReadDenied(carol, leasePath)
	h.rawDenied(claims{mu: carolUID, fp: "-"}, "PUT", leasePath, createBody(carolUID))
}
