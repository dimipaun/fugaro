// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Revealing a key, and the file an air-gapped licence is downloaded as (portal
// plan P3.2; D2182 as amended, D2206, D2187, F3, F4, F5, F30, F32, F35, F42).
//
// The cases here drive the two doors the SPA calls, through the portal's own
// handler and behind a signed-in caller (apiAsCaller), so what they read is what
// a person meets: a refusal's code is what the screen branches on and its
// sentence is what it shows.
//
// What every one of them starts from is one licence bound to one Firebase
// project, which is the only state a reveal has anything to rotate — a licence
// nobody bound has no key, and a workspace nobody licensed has no licence
// (reveal.go's `revealTarget`).

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// revealFixture is one organisation with one licence bound to one Firebase
// project: the state every case about a reveal starts from. The binding's own
// bundle is kept, because it is the predecessor a reveal rotates — the key an
// installation is running on and the secret the store has hashed.
type revealFixture struct {
	*bindingFixture
	org         WireOrganisation
	workspace   WireWorkspace
	entitlement WireEntitlement
	predecessor MintedV1
}

// newRevealFixture builds it on the package's binding fixture, which already
// holds the three things a case about a reveal needs: a probe that answers the
// way a locked database does, a store that reads before it writes inside a
// transaction (the rule the deploy's own store keeps), and a minter wrapper that
// keeps the whole bundle rather than the key alone.
func newRevealFixture(t *testing.T) *revealFixture {
	t.Helper()
	return newRevealFixtureOn(t, PolicyTeamAnnual, exampleProject)
}

// newRevealFixtureOn is the same fixture with the row a case names, which is how
// the air-gapped cases are built: the row is what makes a key air-gapped (D2206)
// and no request may name that.
func newRevealFixtureOn(t *testing.T, row, projectID string) *revealFixture {
	t.Helper()
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's portal")
	held := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            row,
		Seats:          3,
		StartsAt:       f.now,
	})
	if rec := f.bind(t, held.ID, projectID); rec.Code != http.StatusOK {
		t.Fatalf("binding the licence answered %d: %s", rec.Code, rec.Body)
	}
	if len(f.mints.answers) != 1 {
		t.Fatalf("the binding minted %d key(s), want the one a reveal rotates", len(f.mints.answers))
	}
	bound, found := f.workspace(workspace.ID)
	if !found {
		t.Fatalf("the store holds no workspace %s after binding it", workspace.ID)
	}
	licensed, found := f.entitlement(held.ID)
	if !found {
		t.Fatalf("the store holds no licence %s after binding it", held.ID)
	}
	return &revealFixture{
		bindingFixture: f,
		org:            org,
		workspace:      bound,
		entitlement:    licensed,
		predecessor:    f.mints.answers[0],
	}
}

// reveal posts one reveal the way the SPA does: the workspace's own portal id
// and nothing else.
func (f *revealFixture) reveal(t *testing.T, workspaceID string) *httptest.ResponseRecorder {
	t.Helper()
	return f.apiAsCaller(http.MethodPost, apiKeysRevealPath,
		`{"workspace_id":`+jsonString(t, workspaceID)+`}`)
}

// download asks for one workspace's licence file the way the SPA does.
func (f *revealFixture) download(t *testing.T, workspaceID string) *httptest.ResponseRecorder {
	t.Helper()
	return f.apiAsCaller(http.MethodGet,
		apiKeysLicencePath+"?workspace_id="+url.QueryEscape(workspaceID), "")
}

// signedInAgain is the same person signing in afresh, which is what D2187 asks
// of a reveal: the fixture stamps a sign-in when it is made (actsAs), so a case
// that moves the clock moves the sign-in out of its five minutes with it.
func (f *revealFixture) signedInAgain() {
	f.actsAs(danaUID, danaEmail, signInGoogle)
}

// bundleOf is one reveal's answer as the shape the SPA reads, with the case
// stopped if the reveal was refused.
func (f *revealFixture) bundleOf(t *testing.T, rec *httptest.ResponseRecorder) WireRevealBundle {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the reveal answered %d: %s", rec.Code, rec.Body)
	}
	return readAnswer[WireRevealBundle](f.fixture, rec)
}

// withdrawn answers whether §8's list holds an entry for one key, which is how a
// case reads that a key was left live rather than revoked.
func (f *revealFixture) withdrawn(keyID string) bool {
	f.t.Helper()
	for _, entry := range f.revocations() {
		if entry.KeyID == keyID {
			return true
		}
	}
	return false
}

// TestRevealNeverExtendsTheTerm is the task's first case: what a reveal hands
// back is a key that ends where the key it replaces ended, and never a fresh
// period (F4, F30). The key it replaces is a thirty-day one (D2202) with ten of
// its days gone, so a reveal that minted a term of its own would hand the
// workspace a key ending ten days after the one its installation is running on —
// which is the shape a customer would ask for and the one the ruling refuses.
func TestRevealNeverExtendsTheTerm(t *testing.T) {
	f := newRevealFixture(t)
	predecessor := f.predecessor.Record
	if !predecessor.Expires.After(f.now) {
		t.Fatalf("the bound key ends at %s, which is not after the fixture's own instant %s", predecessor.Expires, f.now)
	}

	f.advance(10 * 24 * time.Hour)
	f.signedInAgain()
	bundle := f.bundleOf(t, f.reveal(t, f.workspace.ID))

	if !bundle.PaidThrough.Equal(predecessor.Expires) {
		t.Errorf("the revealed key ends at %s, want the predecessor's own end %s: a reveal recovers a key and never extends a term (F4, F30)",
			bundle.PaidThrough, predecessor.Expires)
	}
	if bundle.PaidThrough.After(f.entitlement.EndsAt) {
		t.Errorf("the revealed key ends at %s, past the end of the licence it is minted under (%s)",
			bundle.PaidThrough, f.entitlement.EndsAt)
	}
	claims := f.claimsOf(t, 1, exampleProject)
	if !claims.PaidThrough.Equal(predecessor.Expires) {
		t.Errorf("the revealed key's own claim ends at %s, want %s: the end is the minter's to write and the record's to state",
			claims.PaidThrough, predecessor.Expires)
	}
}

// TestRevealRotatesTheSecret: a reveal is a rotation and not a reprint. The
// portal holds no key to show (D2182: the store keeps the secret's hash and
// never the secret), so the answer is the licence's **next** key — a new key id
// and a new renewal secret — and the store records the rotation, which is what
// makes the secret the old installation holds spent.
func TestRevealRotatesTheSecret(t *testing.T) {
	f := newRevealFixture(t)
	old := f.predecessor
	f.advance(time.Hour)
	f.signedInAgain()
	bundle := f.bundleOf(t, f.reveal(t, f.workspace.ID))

	if bundle.KeyID == old.Record.KeyID {
		t.Errorf("the reveal answered the key %s, which is the one it was asked to replace", bundle.KeyID)
	}
	if bundle.Key == old.Key {
		t.Error("the reveal answered the predecessor's own key text, so nothing was rotated")
	}
	if bundle.RenewalSecret == "" || bundle.RenewalSecret == old.RenewalSecret {
		t.Errorf("the reveal answered the renewal secret %q, want a fresh one: a rotation is what makes the old secret spent",
			bundle.RenewalSecret)
	}
	if got := f.secretOf(t, bundle.KeyID); got != bundle.RenewalSecret {
		t.Errorf("the mint answered the secret %q and the bundle carries %q, want the one secret the holder is shown",
			got, bundle.RenewalSecret)
	}
	// The successor is filed under the secret its holder is handed, which is the
	// one its own renewals prove with.
	state, known, err := f.store.KeyState(context.Background(), bundle.KeyID)
	if err != nil || !known {
		t.Fatalf("the successor's key state: known=%v err=%v", known, err)
	}
	if state.RenewalSecretHash != digest(bundle.RenewalSecret) {
		t.Errorf("the successor's key state is hashed from %q, want the digest of the secret the bundle carries", state.RenewalSecretHash)
	}
	if state.WorkspaceID != exampleProject {
		t.Errorf("the successor's key state names the workspace %q, want the project id %q: a renewal mints into it",
			state.WorkspaceID, exampleProject)
	}

	// The rotation itself is one record naming the key it replaced, which is
	// what renew.go reads to answer the old secret its successor for a day and
	// what §8's walk from a refunded key follows (F29).
	rotations := 0
	for _, record := range f.records() {
		if record.Source != rotationSource(old.Record.KeyID) {
			continue
		}
		rotations++
		switch {
		case record.KeyID != bundle.KeyID:
			t.Errorf("the rotation names the key %s, want the successor %s", record.KeyID, bundle.KeyID)
		case record.EntitlementID != f.entitlement.ID:
			t.Errorf("the rotation names the licence %s, want %s", record.EntitlementID, f.entitlement.ID)
		case !record.Expires.Equal(bundle.PaidThrough):
			t.Errorf("the rotation ends at %s, want the bundle's own end %s", record.Expires, bundle.PaidThrough)
		}
	}
	if rotations != 1 {
		t.Errorf("the store holds %d rotation(s) of %s, want one", rotations, old.Record.KeyID)
	}

	// The predecessor is left live and in its handover rather than withdrawn: an
	// installation whose answer was lost asks again with the secret it holds, and
	// a revoked key is answered its state before that branch is ever reached
	// (renew.go's `renew`).
	if f.withdrawn(old.Record.KeyID) {
		t.Error("the reveal withdrew the key it replaced, which leaves the installation running on it nothing to ask with")
	}
	handover, live, err := f.store.Handover(context.Background(), old.Record.KeyID, f.now)
	if err != nil || !live {
		t.Fatalf("the handover left for %s: live=%v err=%v", old.Record.KeyID, live, err)
	}
	if handover.SecretHash != digest(old.RenewalSecret) {
		t.Errorf("the handover is held against %q, want the digest of the secret that earned it", handover.SecretHash)
	}
	if bytes.Contains(handover.Sealed, []byte(bundle.Key)) || bytes.Contains(handover.Sealed, []byte(bundle.RenewalSecret)) {
		t.Error("the handover the reveal wrote holds the successor's bundle in the clear (D2182 as amended, F3)")
	}
	opened, err := f.sealer.Open(context.Background(), handover.Sealed)
	if err != nil {
		t.Fatalf("opening the handover: %v", err)
	}
	key, secret := splitHandover(opened)
	if key != bundle.Key || secret != bundle.RenewalSecret {
		t.Errorf("the handover opens as (%q, %q), want the bundle the reveal answered (%q, %q)",
			key, secret, bundle.Key, bundle.RenewalSecret)
	}
}

// TestRevealIsOncePerDay: F32's one reveal per workspace per twenty-four hours,
// which is what keeps two rotations inside a day from stranding the installation
// running on the key between them — the person who asks for the second one holds
// the first key, and the first key's secret is already spent. A second workspace
// has a day of its own, because the harm the rule bounds is one installation's.
func TestRevealIsOncePerDay(t *testing.T) {
	f := newRevealFixture(t)
	first := f.bundleOf(t, f.reveal(t, f.workspace.ID))

	f.advance(time.Minute)
	f.signedInAgain()
	again := f.reveal(t, f.workspace.ID)
	if again.Code != http.StatusTooManyRequests {
		t.Fatalf("a second reveal answered %d, want %d: %s", again.Code, http.StatusTooManyRequests, again.Body)
	}
	if code := f.refused(again); code != "reveal_too_soon" {
		t.Errorf("the second reveal was refused as %q, want reveal_too_soon", code)
	}
	if len(f.mints.answers) != 2 {
		t.Errorf("the deployment minted %d key(s), want the binding's and the first reveal's: a refused reveal mints nothing",
			len(f.mints.answers))
	}

	// A day later the window is open, and what it rotates is the key the first
	// reveal issued.
	f.advance(revealWindow)
	f.signedInAgain()
	third := f.bundleOf(t, f.reveal(t, f.workspace.ID))
	if third.KeyID == first.KeyID {
		t.Errorf("the next day's reveal answered the same key %s, want the successor of it", first.KeyID)
	}

	// Another workspace of the same organisation has its own day: the window is
	// the workspace's (F32, F35), and it is keyed on the workspace rather than on
	// the licence or the organisation.
	other := f.seedWorkspace("ws-2", f.org.ID, "Dana's other portal")
	licence := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: f.org.ID,
		WorkspaceID:    &other.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          1,
		StartsAt:       f.now,
	})
	if rec := f.bind(t, licence.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("binding the second licence answered %d: %s", rec.Code, rec.Body)
	}
	if rec := f.reveal(t, other.ID); rec.Code != http.StatusOK {
		t.Errorf("revealing another workspace of the same organisation answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
}

// TestOnlyOwnersAndAdminsReveal: F5 — a reveal rotates a live key, so it is the
// customer organisation's owners' and admins' act. A member of the organisation
// reads the workspace and is refused the rotation, and the refusal names the
// role rather than the workspace: the person may see it and may not re-key it.
func TestOnlyOwnersAndAdminsReveal(t *testing.T) {
	f := newRevealFixture(t)
	f.seed(membershipsCollection, membershipID(f.org.ID, samUID), WireMembership{
		OrganisationID: f.org.ID, UID: samUID, Email: samEmail, Role: roleMember, CreatedAt: f.now,
	})
	f.seed(membershipsCollection, membershipID(f.org.ID, sellerUID), WireMembership{
		OrganisationID: f.org.ID, UID: sellerUID, Email: sellerMail, Role: roleAdmin, CreatedAt: f.now,
	})

	f.actsAs(samUID, samEmail, signInGoogle)
	member := f.reveal(t, f.workspace.ID)
	if member.Code != http.StatusForbidden {
		t.Fatalf("a member's reveal answered %d, want %d: %s", member.Code, http.StatusForbidden, member.Body)
	}
	if code := f.refused(member); code != "not_permitted" {
		t.Errorf("a member's reveal was refused as %q, want not_permitted", code)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("a refused reveal minted a key: the deployment holds %d answer(s)", len(f.mints.answers))
	}

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	if rec := f.reveal(t, f.workspace.ID); rec.Code != http.StatusOK {
		t.Errorf("an admin's reveal answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
}

// TestRevealNeedsAFreshSignIn: D2187's step-up, which is what a rotated
// credential is worth — a session somebody left open is not a person asking.
// The age is the **sign-in's** (`auth_time`) and not the token's, and a refusal
// here is about the sign-in rather than about the person or the day: the same
// request goes through once they have signed in again.
func TestRevealNeedsAFreshSignIn(t *testing.T) {
	f := newRevealFixture(t)

	f.signIn.caller.AuthTime = f.now.Add(-revealStepUp - time.Second)
	stale := f.reveal(t, f.workspace.ID)
	if stale.Code != http.StatusForbidden {
		t.Fatalf("a reveal behind a sign-in older than the step-up answered %d, want %d: %s",
			stale.Code, http.StatusForbidden, stale.Body)
	}
	if code := f.refused(stale); code != "stale_sign_in" {
		t.Errorf("a reveal behind a stale sign-in was refused as %q, want stale_sign_in", code)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("a reveal refused for a stale sign-in minted a key: the deployment holds %d answer(s)", len(f.mints.answers))
	}

	// The refusal spends no day: the same person, signed in again, is served.
	f.signedInAgain()
	if rec := f.reveal(t, f.workspace.ID); rec.Code != http.StatusOK {
		t.Errorf("the reveal after signing in again answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
}

// TestRevealNotifiesOtherOwners: F32 — the organisation's other owners are told
// at each reveal, because a rotation is the kind of act somebody else has to
// know about. What they are told is a link and never a key (F3, D2182 as
// amended): the mail carries no credential, and the actor is not mailed their
// own act.
func TestRevealNotifiesOtherOwners(t *testing.T) {
	f := newRevealFixture(t)
	f.seed(membershipsCollection, membershipID(f.org.ID, erinUID), WireMembership{
		OrganisationID: f.org.ID, UID: erinUID, Email: erinEmail, Role: roleOwner, CreatedAt: f.now,
	})
	f.seed(membershipsCollection, membershipID(f.org.ID, sellerUID), WireMembership{
		OrganisationID: f.org.ID, UID: sellerUID, Email: sellerMail, Role: roleAdmin, CreatedAt: f.now,
	})

	bundle := f.bundleOf(t, f.reveal(t, f.workspace.ID))

	if len(f.mail.sent) != 1 {
		t.Fatalf("the reveal mailed %d message(s), want the one to the other owner: %v", len(f.mail.sent), f.mail.sent)
	}
	sent := f.mail.sent[0]
	if sent.To != erinEmail {
		t.Errorf("the notice went to %q, want the other owner %q", sent.To, erinEmail)
	}
	if sent.Subject != revealSubject {
		t.Errorf("the notice's subject is %q, want the reveal one %q", sent.Subject, revealSubject)
	}
	if !strings.Contains(sent.Body, routeWorkspaces) {
		t.Errorf("the notice carries no link to the portal:\n%s", sent.Body)
	}
	for _, credential := range []string{bundle.Key, bundle.RenewalSecret, f.predecessor.Key, f.predecessor.RenewalSecret} {
		if strings.Contains(sent.Body, credential) {
			t.Errorf("the notice carries a key or a secret, which no notice may (F3):\n%s", sent.Body)
		}
	}
	for _, msg := range f.mail.sent {
		if msg.To == danaEmail || msg.To == sellerMail {
			t.Errorf("the reveal mailed %q, want the organisation's other owners alone: the actor is not told their own act and an admin is not an owner", msg.To)
		}
	}
}

// TestAirGappedRevealIsAFile: D2206, F42 — an air-gapped key has no secret and
// nothing can ever prove one, so what a person gets for it is the file the
// binding issued: the same signed text on every download, because there is
// nothing to revoke and nothing to keep alive. A licence that is not air-gapped
// has no file at all, and asking for one is refused rather than served.
func TestAirGappedRevealIsAFile(t *testing.T) {
	f := newRevealFixtureOn(t, PolicyEnterpriseAirGapped, exampleProject)
	if !f.entitlement.AirGapped {
		t.Fatalf("the %s row did not grant an air-gapped licence, so this case reads nothing", f.entitlement.Row)
	}

	first := f.download(t, f.workspace.ID)
	if first.Code != http.StatusOK {
		t.Fatalf("the licence download answered %d: %s", first.Code, first.Body)
	}
	file := readAnswer[WireLicenceFile](f.fixture, first)
	switch {
	case file.KeyID != f.predecessor.Record.KeyID:
		t.Errorf("the file names the key %s, want the one the binding issued %s", file.KeyID, f.predecessor.Record.KeyID)
	case file.Licence != f.predecessor.Key:
		t.Error("the file is not the signed text the binding issued, so the download is not the licence itself")
	case !file.PaidThrough.Equal(f.predecessor.Record.Expires):
		t.Errorf("the file's term ends at %s, want the key's own end %s", file.PaidThrough, f.predecessor.Record.Expires)
	}
	if again := readAnswer[WireLicenceFile](f.fixture, f.download(t, f.workspace.ID)); again != file {
		t.Errorf("the second download answered %+v, want the same file %+v: the licence is issued once", again, file)
	}

	// The other half: a licence whose key is revealed has no file to download.
	plain := newRevealFixture(t)
	refused := plain.download(t, plain.workspace.ID)
	if refused.Code != http.StatusConflict {
		t.Fatalf("downloading a connected licence's file answered %d, want %d: %s", refused.Code, http.StatusConflict, refused.Body)
	}
	if code := plain.refused(refused); code != "not_air_gapped" {
		t.Errorf("a connected licence's download was refused as %q, want not_air_gapped", code)
	}
}

// TestAirGappedIsNeverRevealed: D2206 — an air-gapped key is issued once, at
// binding, and is never revealed. A reveal would be a second signature of a key
// nothing can revoke, which is a permanent extra copy of a customer's licence,
// so the door refuses and the file above is the whole of what there is to get.
func TestAirGappedIsNeverRevealed(t *testing.T) {
	f := newRevealFixtureOn(t, PolicyEnterpriseAirGapped, exampleProject)

	rec := f.reveal(t, f.workspace.ID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("revealing an air-gapped licence answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body)
	}
	if code := f.refused(rec); code != "air_gapped" {
		t.Errorf("revealing an air-gapped licence was refused as %q, want air_gapped", code)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("the deployment minted %d key(s), want the binding's alone: nothing re-signs an air-gapped licence", len(f.mints.answers))
	}
	if _, found := heldRecord[WireLicenceFile](f.fixture, licenceFilesCollection, f.entitlement.ID); !found {
		t.Errorf("the store holds no file for the air-gapped licence %s, which is the only thing its holder can be given", f.entitlement.ID)
	}
}

// TestResellerAndOperationsSeeMetadataOnly: F5 — a reseller and an operations
// member see key **metadata** (the key id, the seats, the tier, the renewal date
// and the last refresh) and never a key. The metadata reads are P5.1's and
// P6.1's; what this task owns is that the two doors that hand a credential
// refuse them, and refuse them the way a workspace that is not there is refused
// — the workspace is read through the caller's own membership, and neither of
// these two belongs to the customer's organisation.
func TestResellerAndOperationsSeeMetadataOnly(t *testing.T) {
	f := newRevealFixture(t)
	f.seededOrganisation("org-ops", "Fugaro Operations", orgTypeOperations, samUID, samEmail, roleOwner)
	f.seededOrganisation("org-partner", "Partner Ltd", orgTypeReseller, erinUID, erinEmail, roleOwner)

	for _, tc := range []struct {
		name  string
		uid   string
		email string
	}{
		{name: "operations", uid: samUID, email: samEmail},
		{name: "a reseller", uid: erinUID, email: erinEmail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.actsAs(tc.uid, tc.email, signInGoogle)

			revealed := f.reveal(t, f.workspace.ID)
			if revealed.Code != http.StatusNotFound {
				t.Fatalf("the reveal answered %d, want %d: %s", revealed.Code, http.StatusNotFound, revealed.Body)
			}
			if code := f.refused(revealed); code != "not_found" {
				t.Errorf("the reveal was refused as %q, want not_found", code)
			}
			if downloaded := f.download(t, f.workspace.ID); downloaded.Code != http.StatusNotFound {
				t.Errorf("the licence download answered %d, want %d: %s", downloaded.Code, http.StatusNotFound, downloaded.Body)
			}
			if len(f.mints.answers) != 1 {
				t.Errorf("a refused reveal minted a key: the deployment holds %d answer(s)", len(f.mints.answers))
			}
		})
	}
}

// TestAnotherOrgsKeyIsRefused: the workspace is read through the caller's own
// membership of the organisation that holds it, so another organisation's
// workspace is not there to them — the same 404 as a workspace that does not
// exist, which is api_orgs.go's rule read at this door. The same person reading
// their own workspace is served, so what refused them is the membership.
func TestAnotherOrgsKeyIsRefused(t *testing.T) {
	f := newRevealFixture(t)
	f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)

	f.actsAs(erinUID, erinEmail, signInGoogle)
	rec := f.reveal(t, f.workspace.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revealing another organisation's workspace answered %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body)
	}
	if code := f.refused(rec); code != "not_found" {
		t.Errorf("another organisation's workspace was refused as %q, want not_found", code)
	}
	if downloaded := f.download(t, f.workspace.ID); downloaded.Code != http.StatusNotFound {
		t.Errorf("downloading another organisation's licence file answered %d, want %d: %s",
			downloaded.Code, http.StatusNotFound, downloaded.Body)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("a refused reveal minted a key: the deployment holds %d answer(s)", len(f.mints.answers))
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("a refused reveal mailed %d notice(s), want none: nothing happened to tell anybody about", len(f.mail.sent))
	}

	f.signedInAgain()
	if rec := f.reveal(t, f.workspace.ID); rec.Code != http.StatusOK {
		t.Errorf("the workspace's own owner was refused a reveal (%d): %s", rec.Code, rec.Body)
	}
}
