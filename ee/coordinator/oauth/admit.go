// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/mobileauth"
)

// This file is the third of the three things a member checks before a person
// reaches anything through a workspace (docs/workspaces/spec.md §3 step 3,
// D1295): the assertion's address is marked verified and its provider is one of
// the pinned set — both of them the exchange's own (workspace.go) — AND the
// person is one this member's own launcher or operator lists name. The exchange
// answers the first two and hands back a verified identity; whether that person
// may do anything here is the lists' answer, read beside that identity and
// never from it.
//
// **It reads the lists and never writes them** (D1295). They are the two lists
// `fugaro user` writes at the member's own terminal, and a workspace grants
// nothing beyond them: the workspace's published record names its members, and
// a member that let the record, the assertion or a signer's word add a person
// to its own lists would be a member whose admission a workspace chooses. The
// identifiers a workspace does publish reach this member nowhere: nothing here
// reads a workspace's record, and there is no constructor that takes one.
//
// **Refusals are one refusal.** An address the lists do not name, an address
// that is off them and one that was never in a list at all are refused with
// ErrUnverifiedAssertion, the exchange's own refusal, and the sentence names no
// address: whoever holds the assertion is the person it is about, and a refusal
// that told them apart would be a way to ask this member whether somebody is a
// member here.
//
// **A member that cannot read its lists admits nobody.** The lists are read
// before the assertion is verified, so a host whose own configuration cannot be
// read refuses with ErrMemberListsUnreadable and spends nothing — no assertion
// id is burnt by the member's own fault. That is the fail-closed half of W5's
// rule: a member with no route to its lists, and a member wired with none at
// all (NewAdmission), refuse rather than falling back on a value read earlier.
//
// **What this file does not do: the front door.** It mounts no route and holds
// no PKCE state, exactly as the exchange does not (its own note). W5's answer
// is a function over a raw assertion, and the member's flow asks it where the
// member's flow already decides who may sign in.

// ErrMemberListsUnreadable is the refusal of a member that could not read its
// own launcher and operator lists. It is NOT ErrUnverifiedAssertion: nothing is
// wrong with the assertion, and the failure is this member's own — a caller
// answers a 5xx or its own refusal, never "this sign-in was bad", because the
// person presenting the assertion may be exactly the person the lists name.
var ErrMemberListsUnreadable = errors.New("oauth: this member's own launcher and operator lists could not be read")

// Lists is the member's own admission lists as the exchange reads them: every
// address the launcher and operator entries `fugaro user` wrote name, in the
// spellings the operator wrote them in (projectpolicy.MemberLists.Addresses
// reads which entries are one person's address; the folding below is the
// comparison's own).
//
// It is a source and not a value the caller hands over once, and it is read on
// every request rather than at startup (the MCP door's own Roster rule): an
// address the operator added this morning is admitted without a restart, and a
// host that cannot read its lists now refuses rather than answering from what
// it read earlier. Nothing that could read a workspace's record may implement
// it, and nothing in this package does: this is the member's own configuration,
// and the member's own wiring is what supplies one.
type Lists interface {
	Addresses(ctx context.Context) ([]string, error)
}

// Admission is the member's whole admission: the exchange that verifies what a
// workspace asserts, and the member's own lists.
type Admission struct {
	exchange *Exchange
	lists    Lists
}

// NewAdmission builds a member's admission. Both halves are required: a nil
// exchange is a member with nothing to verify an assertion against, and a nil
// lists source is a member that can name nobody — a member whose lists are
// missing admits nobody (D1295), and it is refused here, where it is wired,
// rather than at somebody's first sign-in.
func NewAdmission(ex *Exchange, lists Lists) (*Admission, error) {
	if ex == nil {
		return nil, errors.New("oauth: an admission needs the member's own exchange")
	}
	if lists == nil {
		return nil, errors.New("oauth: an admission needs the member's own launcher and operator lists, and a member with none admits nobody")
	}
	return &Admission{exchange: ex, lists: lists}, nil
}

// Admit decides whether the person this assertion is for may sign in here, and
// answers the member's own session when they may.
//
// The order is the decision's own: the member's lists first, so that a member
// which cannot read them refuses without spending the assertion's id; then the
// exchange, which verifies the assertion against the member's own pin and
// spends that id exactly once (its own rules, and the refusal it answers is its
// own); and last the comparison, between the folded address the exchange
// verified and the addresses the member's own lists name, folded with the same
// rule (namesAddress).
//
// An assertion whose person the lists do not name is spent by then — the
// exchange spends the id of every assertion it verifies — so the person is
// refused here and signs in again with a new assertion if an operator adds them
// meanwhile. Nothing else is written: the lists are the member's and this
// refuses to change them.
func (a *Admission) Admit(ctx context.Context, raw string) (Session, error) {
	named, err := a.lists.Addresses(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrMemberListsUnreadable, err)
	}
	sess, err := a.exchange.Admit(ctx, raw)
	if err != nil {
		return Session{}, err
	}
	if !namesAddress(named, sess.Identity.Email) {
		return Session{}, refusef("the assertion's person is not one this member's own launcher or operator lists name")
	}
	return sess, nil
}

// namesAddress reports whether one of the addresses a member's lists name is
// the address the exchange verified.
//
// Both sides are held to this tree's one rule for addresses
// (mobileauth.NormalizeEmail: the domain folded, the local part byte for byte),
// so the comparison is between one spelling and another rather than a second
// rule about addresses, and the verified address is the folded one the exchange
// answered with. An entry the rule cannot read is not a refusal of the person:
// it is an entry that names nobody, and the entries beside it are unaffected —
// a display-name form is refused by the rule rather than parsed into an
// address, which is how a name would become an admission.
func namesAddress(named []string, verified string) bool {
	for _, entry := range named {
		canonical, err := mobileauth.NormalizeEmail(entry)
		if err != nil {
			continue
		}
		if canonical == verified {
			return true
		}
	}
	return false
}
