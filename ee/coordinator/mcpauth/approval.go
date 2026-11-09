// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// This file is M5b: **the paired device's approval**, which is M-a's first
// requirement, and **the ID token spent**, which is its second.
//
// **The act is a request of its own.** M5 built the page half of number
// matching — the flow's number is rendered on the page a person reads and the
// same page presents it back — and that is a binding to the *page*: the page
// knows its own number, so a page that rendered a flow of its own displays its
// own number and whoever fills that form hands over the number and the ID token
// together (authorize.go's note). What M-a asks for is an approval made on the
// device paired to the installation, and this file is that act: a POST whose
// body carries the flow's own id, the flow's number as the person read it off
// the screen, and the Firebase ID token the device's session produced. The
// page's answer that follows carries neither the number nor the token — see
// servePageAnswer — and the code is minted only for a flow this act approved.
//
// **Why the act is a shape of the authorize endpoint and not a path of its
// own.** The endpoint's name is this task's to fix (implementation-plan.md,
// M5b's interface), and the one thing that cannot fix it here is a new route:
// the routes of this front door are mounted in server.go's `Mount`, and
// server.go is M5/M7/M6's file (§4's shared-files table), so a path added from
// this task would be a handler nothing mounts and no test could reach. What
// this task fixes instead is the *shape*: the authorize endpoint's POST takes
// two bodies and never one that is both — a body carrying an ID token is the
// device's act, a body carrying a decision is the page's answer, and a body
// carrying both is refused (serveAuthorizeConsent). The wiring task, which
// waits on this one, is where a path of its own can be mounted if the owner
// wants the device's act on one.
//
// **How the flow reaches the paired device is not built here.** The page
// renders the number and carries the flow's id, but nothing yet hands the two
// to the device — no QR of the flow's binding, no launcher reading the page —
// and that is the wiring task's half: this file takes an act that already
// carries the flow's id and the number, and it is the one place that decides
// what such an act means.
//
// **The spend is a create and not a flag.** The ID token is recorded in M2's
// store as the sha256 of its value (D922) through the store's own PutToken,
// which refuses a hash that is already written: the first act writes the
// record, and every later act with the same token is refused as the replay it
// is. That is one conditional write with no read in front of it, so two acts
// with one token landing at once are one approval and one refusal rather than
// two approvals, and nothing here ever holds the token itself.
//
// **And the spend is the last thing before the approval.** Every refusal above
// it — a flow that is not pending, one past its lifetime, a number that is not
// the flow's, a sign-in that does not verify, a flow another device already
// approved — leaves the token unspent, so a device that mistyped the number or
// came back to a flow that was finished can try again with the same session.
// What the order does not do is leave a spent token behind an unrecorded
// approval: the write that spends is the write that precedes the approval's
// own, so a crash between them costs a sign-in and never an approval of a flow
// nobody proved.

// isDeviceAct answers whether an authorize POST is the paired device's act, and
// the ID token's field is what names it.
//
// It is answered by the field's presence rather than by its value: an act whose
// token field is empty is still the device's act, and it is refused as an
// unverified sign-in, which is the answer a device that lost its session needs.
// A body *without* that field is the page's answer, whatever else it carries —
// the page posts a decision and nothing else — so the shapes the caller tells
// apart are the token's presence, and the one body that is neither is the token
// beside a decision (serveAuthorizeConsent).
func isDeviceAct(form url.Values) bool { return form.Has("id_token") }

// serveDeviceApproval is the paired device's act: it checks the flow, checks
// the number the person read off the screen, spends the ID token, and records
// the approval on the flow.
//
// Every refusal here is a page rather than a redirect, and the difference from
// the page's own answer is who is reading: the device is not the browser the
// client is waiting on, so an error sent to the client's redirect URI from here
// would be an answer the device's own program tries to follow. The client is
// told what happened by the page's answer — the browser that asked is still
// there and its Continue reaches the same flow.
func (s *Server) serveDeviceApproval(w http.ResponseWriter, r *http.Request, form url.Values) {
	pending, err := s.store.GetRequest(r.Context(), form.Get("request_id"))
	switch {
	case errors.Is(err, ErrRequestNotFound), errors.Is(err, ErrRequestUsed):
		refusePage(w, http.StatusBadRequest, "This approval does not name a flow this installation is waiting for. A device approves a flow that is still open, and it presents that flow's own id.")
		return
	case err != nil:
		refusePage(w, http.StatusInternalServerError, "This approval could not be read.")
		return
	}
	if s.now().After(pending.ExpiresAt) {
		// Not sent to the client's redirect URI: what is refused here is the
		// device's act, and the browser that asked is the one that answers the
		// client. It is refused rather than recorded, because a flow that is
		// over is not one a device may approve.
		refusePage(w, http.StatusBadRequest, "This flow expired before the device approved it. Approve from the page that is still open, and from the device it is paired with.")
		return
	}
	// The number before the token, over the whole value and in constant time:
	// an act that did not carry this flow's number has not approved this flow,
	// whatever it carries beside it, and a device act that burned a token on a
	// mistyped number would cost a person a sign-in for a typo.
	if subtle.ConstantTimeCompare([]byte(form.Get("number")), []byte(pending.Number)) != 1 {
		refusePage(w, http.StatusBadRequest, "This approval does not carry the number this flow is showing. The number on the screen that asked and the number on the paired device have to be the same one.")
		return
	}
	// The token is canonicalised once, and the value that is verified is the
	// value that is spent. verifySignIn drops the whitespace a form may carry
	// around a value, so spending the raw field would spend a *different*
	// string from the one whose signature was checked — and since the spend is
	// a create on the hash, the same token padded by one byte would approve a
	// second flow, and a third, for as long as the padding varies. One token,
	// one spend, whatever the field carried around it.
	token := strings.TrimSpace(form.Get("id_token"))
	identity, err := s.verifySignIn(r.Context(), token)
	if err != nil {
		// The sign-in step, and it is not the client's failure: a device whose
		// session did not verify is told what to do, and the client is told
		// nothing that would let it retry in a loop.
		refusePage(w, http.StatusUnauthorized, "The sign-in was not verified. Approve this flow from a device paired to the installation, and present the Firebase ID token that device's own session produced.")
		return
	}
	// A flow that already carries an approval is refused before the token is
	// spent, and the identity check that makes it a refusal and not a leak is
	// the one just above: only a device holding a verified sign-in reaches this
	// line at all. The refusal below — ApproveRequest's own ErrRequestApproved —
	// says the same thing one write too late, and a device whose stale page
	// submits an act for a flow that is already approved would have burned the
	// token of a real sign-in on it.
	if pending.Approval != nil {
		refusePage(w, http.StatusBadRequest, "This flow has already been approved on a device. One flow is approved once, and the identity it was approved with is the one the code acts for.")
		return
	}
	spent := TokenHash(token)
	if err := s.spendSignIn(r.Context(), spent, identity); err != nil {
		if errors.Is(err, ErrIDTokenSpent) {
			refusePage(w, http.StatusBadRequest, "This sign-in token has already approved a flow. One sign-in approves one flow: present the token a fresh sign-in on the device produced.")
			return
		}
		refusePage(w, http.StatusInternalServerError, "This approval could not be recorded.")
		return
	}
	approval := Approval{
		IDTokenHash: spent,
		Identity:    identity,
		ApprovedAt:  s.now(),
	}
	if _, err := s.store.ApproveRequest(r.Context(), pending.ID, approval); err != nil {
		switch {
		case errors.Is(err, ErrRequestApproved):
			refusePage(w, http.StatusBadRequest, "This flow has already been approved on a device. One flow is approved once, and the identity it was approved with is the one the code acts for.")
		case errors.Is(err, ErrRequestUsed), errors.Is(err, ErrRequestNotFound):
			refusePage(w, http.StatusBadRequest, "This flow was answered while the device was approving it. A flow is answered once, and it is answered on the page that asked.")
		case errors.Is(err, ErrConflict):
			// The compare-and-swap lost a race with something else writing
			// this record, which is the same event as the case above seen from
			// the other side. It is refused and not retried: the flow moved on
			// under this act, so what the retry would approve is a record this
			// act did not read.
			refusePage(w, http.StatusBadRequest, "This flow changed while the device was approving it. Approve it again from the page it is open on.")
		default:
			refusePage(w, http.StatusInternalServerError, "This approval could not be recorded.")
		}
		return
	}
	writeApprovedPage(w)
}

// spendSignIn records the ID token as used, and it is the store's own write
// that makes it single-use: PutToken creates a record and refuses a hash that
// is already there, so the second use of one token is a refusal and not a
// second approval (M5b's second requirement, spec.md §8).
//
// The record carries the identity the token proved and no expiry, and the empty
// expiry is stated rather than left to be read off the shape: the verifier
// answers the identity and not the token's own claims (firebase.go), so this
// record is the marker of a spend and not a credential any path of this front
// door resolves — LookupByHash refuses a record that names no family, which is
// every record written here.
//
// It takes the hash and not the token, because the caller is the one that has
// to spend the same string it verified: a value this function hashed itself
// would be a second chance for the two to disagree.
func (s *Server) spendSignIn(ctx context.Context, hash, identity string) error {
	rec := TokenRecord{
		Hash:  hash,
		Kind:  KindIDToken,
		Token: Token{Identity: identity},
	}
	if err := s.store.PutToken(ctx, rec); err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("mcpauth: hash %s: %w", rec.Hash, ErrIDTokenSpent)
		}
		return err
	}
	return nil
}

// approvedPage is what the paired device reads when its act was recorded. It
// carries no value at all — not the flow, not the number, not the identity —
// so there is nothing in it to escape and nothing of the person's in a page the
// device's browser may keep.
const approvedPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>This flow is approved</title></head>
<body><h1>This flow is approved</h1>
<p>Return to the page that asked to be authorized and continue there. The
authorization code is sent to the client from that page, in the browser the
request was made in, and this device has nothing left to do.</p></body>
</html>
`

// writeApprovedPage answers the device's act. It is not framable for the same
// reason the consent page is not: it is a page this endpoint rendered, and the
// rule is about every page it renders rather than about the one a person acts
// on.
func writeApprovedPage(w http.ResponseWriter) {
	noFraming(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_, _ = w.Write([]byte(approvedPage))
}

// deviceApprovalRefusal is the message a body carrying both shapes is refused
// with. It is here rather than in the dispatcher because it is this file's
// rule: the page's answer and the device's act are two acts, and a submission
// that is both is the shape M5 accepted — the page's own number beside the
// token — which is the one shape M5b exists to refuse.
const deviceApprovalRefusal = "This request carries the ID token of a sign-in beside the page's own answer, and those are two different acts: the approval is made on the device paired to the installation, and the page's answer carries this flow's binding and its decision. Remove the token from this submission and approve the flow on the device."
