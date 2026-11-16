// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The 14-day Team trial (§14, D2136, D2201; K2.7): the door a signed-in account
// asks at and the rule that one installation and one account get one. Every
// test here drives the route through the handler, because what it owes is an
// answer to a caller — the trial it minted, the refusal a second ask meets and
// the sign-in a request without one is met with — and not a function's return
// value.
//
// The project id a test names is the fixture's own `workspaceID` and the account
// is the fixture's own address: neither is real, and the key every test reads
// back was minted through the fixture's throwaway keyring and verified by
// ee/licence's verifier rather than by this package's own ideas about a claim
// set.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
	"github.com/dimipaun/fugaro/ee/licence"
)

// trialAccount is the account a test signs in as. It is an address at the
// fixture's own domain and obviously not a real one.
const trialAccount = "founder@acme.example"

// trialOrganisation is the portal organisation that account is a member of: the
// other half of D2201's pair, which is an id the store minted (P2.1) and not the
// address's domain (P3.3, D2185). Every case that reads a claim, a store record
// or an installation name reads this value.
const trialOrganisation = "org-acme"

// trialTicket is the ID token a test's sign-in carries: the verifier behind the
// fixture's seam answers the caller it was built with whatever it is handed, so
// what this string says never matters — that it arrives as the request's bearer
// token is the whole of what the route is asked about it.
const trialTicket = "fake-id-token-0123456789"

// trialAs is the fixture with an account signed in and an organisation it is the
// owner of: the seam the route reads to learn who asks proves trialAccount, and
// the store holds the organisation the membership half of §14's rule is read
// from (P2.1, P3.3).
func trialAs(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, Production, func(f *fixture) {
		f.signIn.caller = Caller{UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now}
		f.seededOrganisation(trialOrganisation, "Acme Ltd", orgTypeCustomer, "uid-trial", trialAccount, roleOwner)
	})
}

// ask is one trial request, from the applicant's own address: the route's own
// body, carrying the token the sign-in rides in.
func (f *fixture) ask(body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.askFrom(applicantIP, body)
}

// askFrom is the same ask from an address the test names, which is what tells a
// per-domain bucket from a per-address one.
func (f *fixture) askFrom(ip, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteTrial, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+trialTicket)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// signsInAs turns the fixture's sign-in seam to another account, and it is the
// spy the running service already holds: a test that asks twice as two accounts
// is asking the same intake — the same limiter, the same store, the same key ring
// — rather than a service rebuilt for the second ask, which is what a per-domain
// bucket has to be read on. The account it turns to is a member of no
// organisation, which is what a case that means to read the organisation refusal
// wants; the two below are what a case that means to ask as somebody else wants.
func (f *fixture) signsInAs(uid, email string) {
	f.t.Helper()
	f.signIn.caller = Caller{UID: uid, Email: email, Provider: "google.com", AuthTime: f.now}
}

// foundsAs turns the fixture's sign-in to another account and gives it an
// organisation of its own, which is what a second organisation asking for a trial
// is (P3.3): the organisation half of §14's rule is a portal organisation (P2.1),
// so an account that is a member of none is refused before the rule these cases
// are about is reached at all.
func (f *fixture) foundsAs(uid, email, name string) WireOrganisation {
	f.t.Helper()
	f.signsInAs(uid, email)
	return f.seededOrganisation("org-"+uid, name, orgTypeCustomer, uid, email, roleOwner)
}

// joinsAs turns the fixture's sign-in to another account and makes it a member of
// an organisation that already exists — a colleague of the account that asked
// first, which is what tells the organisation half of §14's rule from the account
// half: the same organisation asking twice meets the installation's claim and the
// same account asking twice meets the account's.
func (f *fixture) joinsAs(org WireOrganisation, uid, email, role string) {
	f.t.Helper()
	f.signsInAs(uid, email)
	f.seed(membershipsCollection, membershipID(org.ID, uid), WireMembership{
		OrganisationID: org.ID, UID: uid, Email: email, Role: role, CreatedAt: f.now,
	})
}

// trialLicence reads the licence a served trial is held as: the grant the route
// wrote and the workspace it wrote for it, read out of the store rather than out
// of a value the case remembered, so what is checked is what the route really
// wrote. The grant is found by its source, because a trial is the only grant of
// this fixture that carries one (entitlements.go's vocabulary).
func (f *fixture) trialLicence() (WireWorkspace, WireEntitlement) {
	f.t.Helper()
	held, err := recordsWhere[WireEntitlement](context.Background(), f.docs, entitlementsCollection,
		Where{Field: "source", Value: entitlementSourceTrial})
	if err != nil {
		f.t.Fatalf("reading the trial's entitlement: %v", err)
	}
	if len(held) != 1 {
		f.t.Fatalf("the store holds %d trial entitlement(s), want one: %+v", len(held), held)
	}
	if held[0].WorkspaceID == nil {
		f.t.Fatalf("the trial's entitlement %+v names no workspace, so no card holds the licence (P3.1)", held[0])
	}
	workspace, present := heldRecord[WireWorkspace](f, workspacesCollection, *held[0].WorkspaceID)
	if !present {
		f.t.Fatalf("the trial's entitlement names the workspace %s, which the store does not hold", *held[0].WorkspaceID)
	}
	return workspace, held[0]
}

// trialEntitlements is every trial this fixture's store holds, for the cases that
// serve more than one.
func (f *fixture) trialEntitlements() []WireEntitlement {
	f.t.Helper()
	held, err := recordsWhere[WireEntitlement](context.Background(), f.docs, entitlementsCollection,
		Where{Field: "source", Value: entitlementSourceTrial})
	if err != nil {
		f.t.Fatalf("reading the trials' entitlements: %v", err)
	}
	return held
}

// organisationWorkspaces is every workspace the store holds for one
// organisation, which is what the trials a case served wrote.
func (f *fixture) organisationWorkspaces(organisationID string) []WireWorkspace {
	f.t.Helper()
	held, err := recordsWhere[WireWorkspace](context.Background(), f.docs, workspacesCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		f.t.Fatalf("reading the workspaces: %v", err)
	}
	return held
}

// claimRefuser is the store with a claim that cannot be written: every other
// method is the fixture's own, so what a test reads after such an ask is what
// the route really wrote and not what a stub remembers.
type claimRefuser struct {
	Store
	err error
}

func (c claimRefuser) ClaimTrial(context.Context, Trial) error { return c.err }

// unansweredClaim is the store whose answer to a claim was lost: the claim
// lands — the fixture's store holds the trial — and the error that reaches the
// route is the one a re-run of the deployed store's transaction answers when it
// reads the documents its own first attempt committed (firestore_store.go's
// `ClaimTrial`, which is re-run when its result is lost).
type unansweredClaim struct {
	Store
}

func (c unansweredClaim) ClaimTrial(ctx context.Context, t Trial) error {
	if err := c.Store.ClaimTrial(ctx, t); err != nil {
		return err
	}
	return ErrTrialAlreadyClaimed
}

// blindAfterAClaim is the store that cannot be read once a claim has failed, so
// what it answers leaves the claim's landing unknowable: the one case in which
// the route has nothing but a guess to go on.
type blindAfterAClaim struct {
	Store
	blind bool
}

func (b *blindAfterAClaim) ClaimTrial(context.Context, Trial) error {
	b.blind = true
	return errors.New("the store could not be written")
}

func (b *blindAfterAClaim) Trials(ctx context.Context) ([]Trial, error) {
	if b.blind {
		return nil, errors.New("the store could not be read")
	}
	return b.Store.Trials(ctx)
}

// unansweredIssuance is the store whose answer to an issuance write was lost:
// the record lands — the fixture's store holds it — and the error that reaches
// the route is the one a re-run of the deployed store's transaction answers when
// it reads the document its own first attempt committed
// (firestore_store.go's `SaveIssuance`, re-run when its result is lost).
type unansweredIssuance struct {
	Store
}

func (u unansweredIssuance) SaveIssuance(ctx context.Context, r Record) error {
	if err := u.Store.SaveIssuance(ctx, r); err != nil {
		return err
	}
	return ErrAlreadyRecorded
}

// anotherKeysIssuance is the store whose issuance write landed under a key this
// ask did not mint: the source the route wrote is held, and what it names is
// somebody else's key. The case is driven by a store and not by a seeded record
// because the source is no longer a name a test can write in advance — a trial's
// issuance is named by its licence (bindingSource), and the licence's id is
// minted inside the ask — so a record a test seeded itself could never be the one
// this route re-reads.
type anotherKeysIssuance struct {
	Store
}

func (a anotherKeysIssuance) SaveIssuance(ctx context.Context, r Record) error {
	other := r
	other.KeyID = "lk_someone_elses_key"
	if err := a.Store.SaveIssuance(ctx, other); err != nil {
		return err
	}
	return ErrAlreadyRecorded
}

// trialBody is the request one test sends: the workspace's project id, and
// nothing else (§10.3's self-serve trial is single-seat, so this door reads no
// seat count — where a test wants to send one it writes the body out itself,
// which is what pins the refusal).
func trialBody(workspace string) string {
	body, err := json.Marshal(TrialCall{WorkspaceID: workspace})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// TestTrialIsTeamTierFourteenDays: §14's trial is a normal Team key issued
// without payment — 14 days, no grace, a daily check — and it is keyed on the
// organisation the sign-in proved and the installation's Firebase project id
// (D2136, D2201). The key is read back through the released verifier, so what
// this holds is the claim set an installation receives and not this package's own
// record of one.
func TestTrialIsTeamTierFourteenDays(t *testing.T) {
	f := trialAs(t)
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a trial ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}

	keys := f.mintedKeys()
	if len(keys) != 1 {
		t.Fatalf("the ask minted %d key(s), want one", len(keys))
	}
	claims, state, err := licence.Verify([]byte(keys[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the trial's key does not verify: %v", err)
	}
	if state != licence.Active {
		t.Errorf("the trial's key reads %s the instant it was minted, want active", state)
	}
	if claims.Tier != licence.TierTeam || claims.Kind != licence.KindTrial {
		t.Errorf("the trial's key is %s/%s, want %s/%s (D2136)", claims.Tier, claims.Kind, licence.TierTeam, licence.KindTrial)
	}
	if claims.GraceDays != 0 {
		t.Errorf("the trial's key carries %d day(s) of grace, and D2138 rules that a trial has none", claims.GraceDays)
	}
	if want := f.now.Add(14 * 24 * time.Hour); !claims.PaidThrough.Equal(want) {
		t.Errorf("the trial runs to %s, want the fourteen days of §14 (%s)", claims.PaidThrough, want)
	}
	if claims.CheckInterval == nil || *claims.CheckInterval != dailyCheck {
		t.Errorf("the trial's check interval is %v, want %s: a trial is revoked by contact like any other connected key (§14)", claims.CheckInterval, dailyCheck)
	}
	if claims.WorkspaceID != workspaceID {
		t.Errorf("the trial's key binds to workspace %q, want the installation's own project id %q (TN2)", claims.WorkspaceID, workspaceID)
	}
	if claims.Organisation != trialOrganisation {
		t.Errorf("the trial's key is issued to %q, want the portal organisation the caller is a member of (%q): the organisation half of D2201's pair is the portal's own (P3.3)", claims.Organisation, trialOrganisation)
	}
	if claims.Seats != 1 {
		t.Errorf("the trial's key is minted for %d seat(s), want the row's one: the self-serve trial is single-seat (§10.3, B6) and the ask cannot name another", claims.Seats)
	}

	// The store's trial names the two halves §14 counts — the organisation the
	// caller is a member of with the project id the ask named — and the record
	// names the installation the key was minted into.
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 {
		t.Fatalf("the store holds %d trial(s), want one: %+v", len(trials), trials)
	}
	if want := trialInstallation(trialOrganisation, workspaceID); trials[0].Installation != want {
		t.Errorf("the trial counts the installation %q, want %q: §14's installation half is the organisation and the project id together (D2201)",
			trials[0].Installation, want)
	}
	if trials[0].Account != trialAccount {
		t.Errorf("the trial is held for account %q, want the address the sign-in proved (%q)", trials[0].Account, trialAccount)
	}
	if trials[0].KeyID == "" {
		t.Error("the trial names no key, so an operator reading the list cannot find what it minted")
	}
	records := f.records()
	if len(records) != 1 || records[0].Organisation != trialOrganisation {
		t.Errorf("the minted key's record is %+v, want one record for the account's own organisation", records)
	}

	// The trial is a licence the portal holds (P3.3): a workspace of the
	// organisation's own, named for the project the ask named, and the grant that
	// licenses it — which is what P3.1's screen reads a card off and what a
	// purchase of the same workspace would supersede (D2204). What the workspace
	// does **not** carry is the project id, and that is the point of the assertions
	// below: a project id on a workspace is the cross-organisation claim only a
	// binding may make, after the installation there is probed (D2205, TN-P1).
	workspace, entitlement := f.trialLicence()
	if entitlement.Source != "trial" || entitlement.Row != PolicyTrial || entitlement.Status != "active" {
		t.Errorf("the trial's entitlement is %+v, want an active grant of the %s row sourced as a trial", entitlement, PolicyTrial)
	}
	if entitlement.OrganisationID != trialOrganisation || entitlement.WorkspaceID == nil || *entitlement.WorkspaceID != workspace.ID {
		t.Errorf("the trial's entitlement is %+v, want one of %s for the workspace %s", entitlement, trialOrganisation, workspace.ID)
	}
	if entitlement.EndsAt.Sub(entitlement.StartsAt) != 14*24*time.Hour {
		t.Errorf("the trial's entitlement runs %s, want the row's fourteen days (§14, D2136)", entitlement.EndsAt.Sub(entitlement.StartsAt))
	}
	if entitlement.BoundAt == nil {
		t.Error("the trial's entitlement reads unbound, and its key has just been minted: a card would offer to bind a workspace that already has one (P3.1)")
	}
	if workspace.Name != workspaceID {
		t.Errorf("the trial's workspace is named %q, want the project the ask named (%q): the card P3.4 draws shows it by name", workspace.Name, workspaceID)
	}
	if workspace.ProjectID != nil {
		t.Errorf("the trial's workspace carries the project id %q: nothing proved the asker controls that project (D2201, F37), and a project id on a workspace is the claim that bars every other organisation's binding of it until operations transfer it (D2204, D2205, TN-P1). The id is attached by the bind path, which probes the installation first", *workspace.ProjectID)
	}
	if workspace.BoundAt == nil {
		t.Errorf("the trial's workspace is %+v, want one reading bound: its key is minted by the same request, and a workspace with a key behind it that read unbound is one the screen offers to bind again", workspace)
	}
	if workspace.EntitlementID == nil || *workspace.EntitlementID != entitlement.ID {
		t.Errorf("the trial's workspace is %+v, want the one pointing at its licence %s (D2204)", workspace, entitlement.ID)
	}
	// The key's issuance is named by its licence, which is what a rebind of that
	// licence reads the key it replaces back with (D2198, `bindingKeys`).
	if want := bindingPrefix + entitlement.ID + ":"; !strings.HasPrefix(records[0].Source, want) {
		t.Errorf("the key's issuance is named %q, want one under %q: a trial's key is its licence's, and a rebind has to be able to find it", records[0].Source, want)
	}

	// The bundle leaves with the key: the second line is the renewal secret, and
	// the store holds its hash alone (§7.3, D2135).
	if len(f.mail.sent) != 1 || !strings.Contains(f.mail.sent[0].Body, keys[0]) {
		t.Fatalf("the ask mailed %d message(s) and the key is not in them: %+v", len(f.mail.sent), f.mail.sent)
	}
	if f.mail.sent[0].To != trialAccount {
		t.Errorf("the bundle went to %q, want the address the sign-in proved (%q)", f.mail.sent[0].To, trialAccount)
	}
	held, known, err := f.store.KeyState(t.Context(), records[0].KeyID)
	if err != nil || !known {
		t.Fatalf("the minted key has no state (known=%v err=%v), so nothing can check it in (§8)", known, err)
	}
	if held.WorkspaceID != workspaceID {
		t.Errorf("the key's state names workspace %q, want %q", held.WorkspaceID, workspaceID)
	}
}

// TestTrialIsOnePerWorkspaceAndAccount: §14's rule is one trial per installation
// or account (D2136, D2201), which in the portal is one per organisation **and**
// project id, and one per account. Every refused ask here mints nothing at the
// key ring — a refused ask that had already signed a key would spend the
// licensor's authority on a trial nobody gets — and writes nothing: the store
// still holds the one trial, the one workspace and the one licence the first ask
// wrote. The last ask is the one that shows what the rule is not: an account at
// the same organisation with a project of its own has had no trial, and it is
// served on its own pair.
func TestTrialIsOnePerWorkspaceAndAccount(t *testing.T) {
	f := trialAs(t)
	if rec := f.ask(trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Fatalf("the first ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}

	// The same account, another project: the account has had its trial.
	sameAccount := f.ask(trialBody("acme-other"))
	if sameAccount.Code != http.StatusConflict {
		t.Errorf("a second ask from the same account answered %d (%s), want 409", sameAccount.Code, sameAccount.Body.String())
	}
	if code := trialFaultCode(t, sameAccount); code != "trial_already_claimed" {
		t.Errorf("the refusal carries the code %q, want %q: what refuses it is the account half of §14's rule (D2201)", code, "trial_already_claimed")
	}
	// A colleague in the same organisation, the project the first trial was
	// minted for: the installation has had its trial.
	f.joinsAs(WireOrganisation{ID: trialOrganisation}, "uid-other", "other@acme.example", roleMember)
	sameInstallation := f.ask(trialBody(workspaceID))
	if sameInstallation.Code != http.StatusConflict {
		t.Errorf("a second ask for the same installation answered %d (%s), want 409", sameInstallation.Code, sameInstallation.Body.String())
	}
	if code := trialFaultCode(t, sameInstallation); code != "trial_already_claimed" {
		t.Errorf("the refusal carries the code %q, want %q: what refuses it is the installation half of §14's rule (D2201)", code, "trial_already_claimed")
	}

	if minted := f.mintedKeys(); len(minted) != 1 {
		t.Errorf("the refused asks minted %d key(s) past the first, want none", len(minted)-1)
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 {
		t.Errorf("the store holds %d trial(s) after two refusals, want the first ask's one: %+v", len(trials), trials)
	}
	if licences := f.trialEntitlements(); len(licences) != 1 {
		t.Errorf("the store holds %d trial entitlement(s) after two refusals, want the first ask's one: %+v", len(licences), licences)
	}
	if workspaces := f.organisationWorkspaces(trialOrganisation); len(workspaces) != 1 {
		t.Errorf("the organisation holds %d workspace(s) after two refusals, want the one the served ask wrote: %+v", len(workspaces), workspaces)
	}
	if revocations := f.revocations(); len(revocations) != 0 {
		t.Errorf("the refusals withdrew %d key(s), want none: a refused ask never reached the key ring (%+v)", len(revocations), revocations)
	}

	// The account half belongs to the account and not to the organisation: a
	// colleague asking for a project of their own is served, and what they are
	// served is a licence of their own — the organisation's second.
	f.joinsAs(WireOrganisation{ID: trialOrganisation}, "uid-other", "other@acme.example", roleMember)
	if rec := f.ask(trialBody("acme-colleague")); rec.Code != http.StatusNoContent {
		t.Fatalf("a colleague's ask for a project of their own answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if licences := f.trialEntitlements(); len(licences) != 2 {
		t.Errorf("the store holds %d trial entitlement(s), want the two served asks': %+v", len(licences), licences)
	}
}

// TestAnotherOrgCannotBurnYourTrial: §14's installation half is the portal
// organisation **and** the project id (D2201), and what that keeps is the harm
// this case drives. A project id is a caller's own string and nothing proves the
// caller controls it (F37, TN3), so a rule counting the id alone would let any
// signed-in account found an organisation, name a stranger's project id, spend
// "its" trial on it and bar the organisation that really runs the project for
// good. Here the stranger asks **first** and is served, and the organisation the
// project belongs to asks afterwards and is served too: nothing the stranger did
// reached the pair that organisation's trial is counted as. What is still
// counted is the pair — each organisation that asks again for the same project
// is refused — and the two served trials are two workspaces, one per
// organisation, both naming the same project id: the state D2201 accepts,
// because a project id alone is worth nothing until the binding proves who runs
// the project (D2205, TN-P1).
func TestAnotherOrgCannotBurnYourTrial(t *testing.T) {
	f := trialAs(t)
	stranger := f.foundsAs("uid-squatter", "founder@widgets.example", "Widgets Ltd")
	if rec := f.askFrom("198.51.100.30:4040", trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Fatalf("a stranger naming a project id of its own answered %d (%s), want 204: the id is a caller's own string and the trial it names is the asking organisation's (D2201)",
			rec.Code, rec.Body.String())
	}

	// The organisation the project belongs to asks for that same project id, and
	// what the stranger did might not have barred it: its own pair has never been
	// asked for.
	f.signsInAs("uid-trial", trialAccount)
	owning := f.askFrom("198.51.100.31:4040", trialBody(workspaceID))
	if owning.Code != http.StatusNoContent {
		t.Fatalf("the organisation the project belongs to answered %d (%s), want 204: another organisation's trial naming the same project id is not this one's and cannot bar it (D2201, TN-P1, F37)",
			owning.Code, owning.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 2 {
		t.Errorf("the asks minted %d key(s), want the two that were served", len(minted))
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 2 {
		t.Fatalf("the store holds %d trial(s), want one per organisation: %+v", len(trials), trials)
	}
	want := map[string]bool{
		trialInstallation(stranger.ID, workspaceID):       true,
		trialInstallation(trialOrganisation, workspaceID): true,
	}
	for _, held := range trials {
		if !want[held.Installation] {
			t.Errorf("the trial %+v counts an installation neither organisation asked for, and both asks named the project id %s (D2201)", held, workspaceID)
		}
	}
	// Each organisation holds its own licence for the project, and neither names
	// the other's: the two workspaces are two, both drawn by the project's name,
	// and **neither carries its id** — the stranger's ask proves nothing about the
	// project (D2201, F37), so the claim that the project is spoken for is left to
	// a binding, which probes the installation before it makes it (D2205, TN-P1).
	if held := f.organisationWorkspaces(stranger.ID); len(held) != 1 || held[0].Name != workspaceID || held[0].ProjectID != nil {
		t.Errorf("the stranger's organisation holds %+v, want the one workspace its trial named for %s and no project id", held, workspaceID)
	}
	if held := f.organisationWorkspaces(trialOrganisation); len(held) != 1 || held[0].Name != workspaceID || held[0].ProjectID != nil {
		t.Errorf("the owning organisation holds %+v, want the one workspace its trial named for %s and no project id", held, workspaceID)
	}
	if licences := f.trialEntitlements(); len(licences) != 2 {
		t.Errorf("the store holds %d trial entitlement(s), want one per organisation: %+v", len(licences), licences)
	}

	// Within the pair the rule is what it was: the organisation that already
	// asked for this project is refused when it asks again.
	again := f.askFrom("198.51.100.32:4040", trialBody(workspaceID))
	if again.Code != http.StatusConflict {
		t.Errorf("the organisation asking again for its own installation answered %d (%s), want 409", again.Code, again.Body.String())
	}
}

// TestAnotherOrgsTrialDoesNotBarYourBinding is the harm the trial's own write is
// kept away from, driven through the two acts that would meet on one project id.
//
// A project id on a workspace is the claim that the project is spoken for in
// **every** organisation (D2204, `refuseBoundProject`), and the binding asks that
// check before it probes the installation (D2205). A trial ask, though, is a
// signed-in stranger's: the id it names is a string the asker typed and nothing
// in the ask proves they run that project (D2201, F37). So a trial that wrote the
// id onto its workspace would let any account bar the organisation that really
// runs the project from binding it — 409 `project_id_bound` for as long as
// operations has not transferred it (TN-P1) — for a licence covering nothing. The
// trial therefore writes the project's **name** and no id (trialLicence), and the
// claim is the binding's: the second act below is answered as if the stranger's
// trial had not happened, and the installation is probed by the binding alone.
func TestAnotherOrgsTrialDoesNotBarYourBinding(t *testing.T) {
	f := newBindingFixture(t)
	// An organisation with a paid licence nobody has bound yet: the second act is
	// this licence's binding to the project the case is about.
	_, dana, paid := f.licensingWorkspace(t)

	// A stranger founds an organisation of its own and takes a trial naming a
	// project id it does not run — which is the only thing a project id is at this
	// door, a caller's own string.
	stranger := f.foundsAs("uid-squatter", "founder@widgets.example", "Widgets Ltd")
	if rec := f.ask(trialBody(exampleProject)); rec.Code != http.StatusNoContent {
		t.Fatalf("the stranger's trial ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if asked := f.probe.saw(); len(asked) != 0 {
		t.Errorf("the trial asked a project's database about %v: D2205's probe is the binding's, and an ask that probed would be a door that reads a project on a stranger's word", asked)
	}
	held := f.organisationWorkspaces(stranger.ID)
	if len(held) != 1 || held[0].Name != exampleProject || held[0].ProjectID != nil {
		t.Fatalf("the stranger's trial wrote %+v, want one workspace naming %s and claiming no project id", held, exampleProject)
	}
	strangers := held[0].ID

	// The organisation that really runs the project binds it. What answers is the
	// binding's own state: the stranger's trial is not another organisation's
	// binding of this project, and it is not an answer to this one.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	rec := f.bind(t, paid.ID, exampleProject)
	if rec.Code != http.StatusOK {
		t.Fatalf("binding the project answered %d (%s), want 200: another organisation's trial naming this project id is a caller's own string and claims nothing about it (D2201, D2204, F37, TN-P1)",
			rec.Code, rec.Body.String())
	}
	if asked := f.probe.saw(); len(asked) != 1 || asked[0] != exampleProject {
		t.Errorf("the binding asked about %v, want the one probe of %s the binding itself makes (D2205)", asked, exampleProject)
	}
	bound, _ := f.workspace(dana.ID)
	if bound.ProjectID == nil || *bound.ProjectID != exampleProject {
		t.Errorf("the organisation's own workspace is %+v, want it bound to the project it named", bound)
	}
	if after, _ := f.workspace(strangers); after.ProjectID != nil {
		t.Errorf("the stranger's workspace is %+v after the binding, want it still claiming no project id: the trial never held one to lose", after)
	}
}

// TestTrialIsRefusedForAProjectTheOrganisationHasBound is the first of the two
// readings of D2204's rule the trial route makes: the one it takes before
// anything is minted (trialProjectFree). An organisation that already holds a
// workspace of its own on the project the ask names holds a licence on that
// installation already — the binding behind that workspace probed the project
// before it made the claim (D2205) — so a trial for the same project would be a
// second licence on one installation: two cards drawn for one workspace and a
// key that fights the one already there.
//
// The refusal is the binding's own for that state — 409 `project_id_bound`, the
// code `refuseBinding` answers `errProjectIDBound` with — and it is read before
// the mint and before the account's domain spends its allowance, so a refused ask
// costs neither a key nor a domain's trial.
//
// What the guard is **not** is every organisation's project: a project id another
// organisation bound is a string this asker cannot be refused for naming (D2201,
// F37, `TestAnotherOrgsTrialDoesNotBarYourBinding`), so it reads the asking
// organisation's own records — and this case is the refuser, which is what makes
// it one: without the guard the ask below is served and writes a licence beside
// the one the binding already made.
func TestTrialIsRefusedForAProjectTheOrganisationHasBound(t *testing.T) {
	f := newBindingFixture(t)
	organisation, workspace, paid := f.licensingWorkspace(t)
	// The licence the organisation already holds on this installation, bound the
	// way every licence is: through the binding, which probes the project. The
	// key that binding mints and the mail it may send are the counts the refusal
	// below is read against.
	if rec := f.bind(t, paid.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding the project answered %d (%s), want 200: this case reads the trial that comes after it", rec.Code, rec.Body.String())
	}
	bound, _ := f.workspace(workspace.ID)
	if bound.ProjectID == nil || *bound.ProjectID != exampleProject {
		t.Fatalf("the organisation's workspace is %+v after the binding, want it bound to %s", bound, exampleProject)
	}
	minted, mailed := len(f.mintedKeys()), len(f.mail.sent)

	rec := f.ask(trialBody(exampleProject))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the ask answered %d (%s), want 409: the organisation holds a licence on that installation already (D2204)", rec.Code, rec.Body.String())
	}
	if code := trialFaultCode(t, rec); code != "project_id_bound" {
		t.Errorf("the refusal carries the code %q, want %q: it is the state the binding refuses under that code (api_workspaces.go)", code, "project_id_bound")
	}
	if now := len(f.mintedKeys()); now != minted {
		t.Errorf("the refused ask minted %d key(s) where the binding had minted %d, want no key of its own: the rule is read before the mint", now, minted)
	}
	if now := len(f.mail.sent); now != mailed {
		t.Errorf("the refused ask mailed %d message(s) where the binding had mailed %d, want nothing", now, mailed)
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 0 {
		t.Errorf("the refused ask claimed %+v, want no trial: a refusal writes nothing, so the account can still ask for a project of its own", trials)
	}
	if licences := f.trialEntitlements(); len(licences) != 0 {
		t.Errorf("the refused ask wrote %+v, want no licence", licences)
	}
	held := f.organisationWorkspaces(organisation.ID)
	if len(held) != 1 || held[0].ID != workspace.ID {
		t.Errorf("the organisation holds %+v after the refusal, want the one workspace it already had (%s)", held, workspace.ID)
	}
}

// TestTrialIsRefusedForAProjectBoundBetweenTheReadAndTheWrite is the trial
// route's second reading of D2204's rule: the one inside the transaction that
// writes the licence (trialLicence). The route reads the rule above, but a
// binding another request makes between that read and this write is state the
// licence would be written on and the route never saw — so the rule is asked
// again inside the write that would break it, which is what a store's
// transaction is for (api.go's Tx, docstore's ErrReadAfterWrite). This case puts
// a binding of the project for the asking organisation in exactly that window,
// which is the state a competing `POST /workspaces/bind` leaves, and reads what
// the transaction does with it.
//
// The answer is this route's failure shape and not a refusal: the key is minted
// and the trial claimed before this transaction runs, so a refusal a caller could
// act on would be a lie — no second project gets this account a trial — and what
// the route leaves behind is the state its own order documents: a claimed trial,
// a key nobody was mailed and no licence, which an operator reads off the store's
// list (§8, K2.2). Without the guard inside the write the ask below is served and
// a second licence is written for the project the organisation has just bound.
func TestTrialIsRefusedForAProjectBoundBetweenTheReadAndTheWrite(t *testing.T) {
	// The asking organisation is the trial fixture's own pair: the portal
	// organisation the membership half of §14's rule counts and the account the
	// trial is held for (D2201).
	f := newBindingFixtureWith(t, func(f *fixture) {
		f.seededOrganisation(trialOrganisation, "Acme Ltd", orgTypeCustomer, "uid-trial", trialAccount, roleOwner)
		f.deps.Docs = &racingDocs{Docs: f.deps.Docs, spy: f.docs}
	})
	f.actsAs("uid-trial", trialAccount, signInGoogle)
	racer := f.racer(t)
	racer.before = func(*docsSpy) {
		// The binding that got there first, written straight into the store the
		// way a committed request's own write lands, in the instant before the
		// trial's transaction runs.
		project, at := exampleProject, f.now
		f.seed(workspacesCollection, "ws-raced", WireWorkspace{
			ID:             "ws-raced",
			OrganisationID: trialOrganisation,
			Name:           exampleProject,
			ProjectID:      &project,
			CreatedAt:      at,
			BoundAt:        &at,
		})
	}

	rec := f.ask(trialBody(exampleProject))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the ask answered %d (%s), want the route's failure shape: the trial is claimed and its key minted by the time the rule is read inside the write, so there is no refusal left to answer (trialLicence, trialFailed)", rec.Code, rec.Body.String())
	}
	if code := trialFaultCode(t, rec); code != apiFailedCode {
		t.Errorf("the failure carries the code %q, want %q", code, apiFailedCode)
	}
	if licences := f.trialEntitlements(); len(licences) != 0 {
		t.Errorf("the store holds %+v, want no licence: the transaction read the rule and refused the write (D2204)", licences)
	}
	if sent := f.mail.sent; len(sent) != 0 {
		t.Errorf("the ask mailed %+v, want nothing: no licence was written for a key to be sent about", sent)
	}
	held := f.organisationWorkspaces(trialOrganisation)
	if len(held) != 1 || held[0].ID != "ws-raced" {
		t.Errorf("the organisation holds %+v, want the one workspace the binding wrote and none of the trial's", held)
	}
	if minted := f.mintedKeys(); len(minted) != 1 {
		t.Errorf("the ask minted %d key(s), want the one the mint above the claim answered (K2.2)", len(minted))
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 || trials[0].Installation != trialInstallation(trialOrganisation, exampleProject) {
		t.Errorf("the store holds %+v, want the one trial this ask claimed for %s (D2201)", trials, trialInstallation(trialOrganisation, exampleProject))
	}
}

// trialForProject is the trial licence whose workspace is named for one project,
// which is how a case holding two of them — the trial an organisation really took
// and the decoy it asked for after it — tells them apart (trialLicence writes one
// workspace per trial, named for the project the ask named).
func (f *fixture) trialForProject(t *testing.T, projectID string) WireEntitlement {
	t.Helper()
	for _, held := range f.trialEntitlements() {
		if held.WorkspaceID == nil {
			continue
		}
		if workspace, present := heldRecord[WireWorkspace](f, workspacesCollection, *held.WorkspaceID); present && workspace.Name == projectID {
			return held
		}
	}
	t.Fatalf("the store holds no trial licence naming %s: %+v", projectID, f.trialEntitlements())
	return WireEntitlement{}
}

// TestATrialIsBoundOnlyToTheProjectItWasAskedFor is §14's rule against the act
// that could walk around it.
//
// A trial writes its workspace bound and with no project id on it (trialLicence:
// the id is the claim a binding makes, D2201, F37), which is what leaves the
// card offering that licence its binding — and a trial licence bindable to *any*
// project would let an organisation refused a trial for a project ask for a decoy
// one, which is a fresh organisation-and-project pair and is served, and then
// attach that licence to the project it was refused. What that mints is a
// trial-row key for the refused installation, and the organisation repeats it for
// every owner or admin it adds — §14's "one per installation and account" as a
// formality. The project the trial was asked for is what the trial wrote into
// this licence's own workspace (its name), so that is what the binding reads
// (`bindEntitlement`).
func TestATrialIsBoundOnlyToTheProjectItWasAskedFor(t *testing.T) {
	f := newBindingFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Dana Ltd")
	// The trial the organisation really took, for the project it runs.
	if rec := f.ask(trialBody(exampleProject)); rec.Code != http.StatusNoContent {
		t.Fatalf("the organisation's trial ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	// A second admin asks for a decoy project and is served: the account half of
	// §14's rule is why the same account cannot ask twice, and another account of
	// the organisation asking about another project is a pair the rule has not
	// counted.
	f.joinsAs(org, "uid-second", "second@dana.example", roleAdmin)
	if rec := f.ask(trialBody(secondProject)); rec.Code != http.StatusNoContent {
		t.Fatalf("the decoy ask answered %d (%s), want 204: another project is another pair and §14 counts the account beside it (D2201)", rec.Code, rec.Body.String())
	}
	decoy := f.trialForProject(t, secondProject)
	minted, asked := len(f.mintedKeys()), len(f.probe.saw())

	// The decoy licence is bound to the project the organisation was refused a
	// trial for, and what answers is the rule: a trial may be bound to the project
	// it was asked for and to nothing else.
	rec := f.bind(t, decoy.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding the decoy licence to %s answered %d (%s), want 409: a trial is the trial of the project it was asked for (§14, D2201)", exampleProject, rec.Code, rec.Body.String())
	}
	if code := f.refused(rec); code != "trial_for_another_project" {
		t.Errorf("the refusal carries the code %q, want %q", code, "trial_for_another_project")
	}
	if now := len(f.mintedKeys()); now != minted {
		t.Errorf("the refused binding minted %d key(s) where the trials had minted %d, want none: a trial-row key for an installation the rule refused is the whole of what this act would buy", now, minted)
	}
	if now := f.probe.saw(); len(now) != asked {
		t.Errorf("the refused binding asked a database about %v, want no call: the rule is read before the probe (D2205)", now[asked:])
	}
	if held, _ := f.workspace(*decoy.WorkspaceID); held.ProjectID != nil {
		t.Errorf("the decoy's workspace is %+v after the refusal, want it still claiming no project: the binding wrote nothing", held)
	}

	// The project that trial *is* for is bound as any licence's is, so what the
	// refusal above is about is the project and not a trial licence nobody can
	// bind, and the key it mints names that project and no other (TN2).
	if rec := f.bind(t, decoy.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("binding the decoy licence to its own project answered %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if now := f.probe.saw(); len(now) != asked+1 || now[asked] != secondProject {
		t.Errorf("the bindings asked about %v, want the one probe of %s that the binding which was served makes (D2205)", now, secondProject)
	}
	claims := f.claimsOf(t, minted, secondProject)
	if claims.WorkspaceID != secondProject || claims.Kind != licence.KindTrial {
		t.Errorf("the key the binding minted is %s for %q, want a %s key for %s: the decoy's trial is that project's licence and a key it mints names it (§14, TN2)",
			claims.Kind, claims.WorkspaceID, licence.KindTrial, secondProject)
	}
	if held, _ := f.workspace(*decoy.WorkspaceID); held.ProjectID == nil || *held.ProjectID != secondProject {
		t.Errorf("the decoy's workspace is %+v after its own binding, want it bound to %s", held, secondProject)
	}
	// The trial the organisation took for the other project is untouched: what the
	// refusal above kept away from is a second key for an installation whose trial
	// was already taken.
	took := f.trialForProject(t, exampleProject)
	if held, _ := f.workspace(*took.WorkspaceID); held.ProjectID != nil {
		t.Errorf("the organisation's own trial workspace is %+v, want it claiming no project: a trial is the licence of that project's card, and the claim on the project is a binding's (D2201, F37)", held)
	}
}

// TestTrialIsRefusedForAnAccountWithNoOrganisation: the half of D2201's pair
// that is not the project id is a portal organisation (P3.3, D2185), and an
// account that is a member of none has none for the trial to be counted against
// or written for. The refusal is one a person can act on — founding an
// organisation is a thing a signed-in person does (P2.1) — rather than a failure
// of the service's own making, and it comes before the mint: a key minted for an
// ask with no organisation is a key the minter refuses (minter.go), which would
// answer the caller a failure it can do nothing about. What the last ask shows is
// that the refusal is the asking account's and not the store's: the member's own
// ask is served.
func TestTrialIsRefusedForAnAccountWithNoOrganisation(t *testing.T) {
	f := trialAs(t)
	f.signsInAs("uid-orphan", "orphan@acme.example")
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an ask from an account in no organisation answered %d (%s), want 403", rec.Code, rec.Body.String())
	}
	if code := trialFaultCode(t, rec); code != "no_organisation" {
		t.Errorf("the refusal carries the code %q, want %q", code, "no_organisation")
	}
	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("the ask minted %d key(s), want none: there is no organisation for the key to name", len(minted))
	}
	if workspaces := f.organisationWorkspaces(trialOrganisation); len(workspaces) != 0 {
		t.Errorf("the refused ask wrote %+v, want no workspace", workspaces)
	}
	f.signsInAs("uid-trial", trialAccount)
	if rec := f.ask(trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Errorf("the member's ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
}

// TestTrialIsRefusedWithoutAVerifiedSignIn: the account is what the rule counts
// and the sign-in is the only thing that names it (K2.4's verifier, P1.3), so a
// request without one, one whose token the verifier refuses, and one whose
// sign-in proved no address are all answered the same way and mint nothing. The
// third is the case the route's own guard is for: an accepted sign-in with no
// address is an account §14 cannot count, and without the guard the ask falls
// through to the mint, which refuses a key with no organisation (minter.go) and
// is answered a failure of the service's own making instead of the refusal the
// caller can act on.
func TestTrialIsRefusedWithoutAVerifiedSignIn(t *testing.T) {
	f := trialAs(t)
	for _, tc := range []struct {
		name  string
		token string
		// caller is what the verifier answers, for the case of a sign-in it
		// accepts and the account half of §14 that answer still does not have.
		caller *Caller
	}{
		{name: "no token at all"},
		{name: "a token the verifier refuses", token: "another-token-0123456789"},
		{name: "a sign-in that proved no address", token: trialTicket,
			caller: &Caller{UID: "uid-trial", Provider: "google.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &signInSpy{err: errors.New("this token was minted for another project")}
			if tc.caller != nil {
				spy = &signInSpy{caller: Caller{
					UID: tc.caller.UID, Email: tc.caller.Email, Provider: tc.caller.Provider, AuthTime: f.now,
				}}
			}
			f.deps.SignIn = spy
			f.start()
			req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteTrial, strings.NewReader(trialBody(workspaceID)))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			req.RemoteAddr = applicantIP
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("the ask answered %d (%s), want 401", rec.Code, rec.Body.String())
			}
			if minted := f.mintedKeys(); len(minted) != 0 {
				t.Errorf("an ask with no verified sign-in minted %d key(s), want none", len(minted))
			}
		})
	}
}

// TestTrialIsRefusedForAFreeMailAddress: D453's list is read against the
// **address** the sign-in proved, and a domain on it is not an organisation:
// anybody can hold an address at one, so an address there is not one an
// organisation's member asks with — which is why the free seat sends such a
// domain to the owner's queue (service.go's `freeSeatForm`) and this door refuses
// it outright. The comparison is D453's own — exact, on a domain lowercased as
// every other address this service reads (§4.1's `address`), so a sign-in that
// spells its domain in another case is refused too rather than counted as a
// domain of its own — and what it reads is the address and not the organisation
// the trial is counted with (P3.3): an account at a free mail address is refused
// even where it is a member of an organisation of its own, and an account at a
// domain of its own is served, which is what the last ask shows.
func TestTrialIsRefusedForAFreeMailAddress(t *testing.T) {
	f := trialAs(t)
	for _, tc := range []struct{ uid, account string }{
		{"uid-free-one", "founder@gmail.example"},
		{"uid-free-two", "founder@Outlook.Example"},
	} {
		f.foundsAs(tc.uid, tc.account, "Free Mail Ltd")
		rec := f.askFrom("198.51.100.40:4040", trialBody(workspaceID))
		if rec.Code != http.StatusForbidden {
			t.Errorf("an ask from %s answered %d (%s), want 403: a free mail domain is not an organisation and D453's list is read against the account's own address",
				tc.account, rec.Code, rec.Body.String())
		}
		if code := trialFaultCode(t, rec); code != "free_mail_address" {
			t.Errorf("the refusal of %s carries the code %q, want %q", tc.account, code, "free_mail_address")
		}
	}
	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("an ask from a free mail domain minted %d key(s), want none", len(minted))
	}
	if workspaces := f.organisationWorkspaces(trialOrganisation); len(workspaces) != 0 {
		t.Errorf("the refused asks wrote %+v, want no workspace: a refusal writes nothing", workspaces)
	}
	f.signsInAs("uid-trial", trialAccount)
	if rec := f.ask(trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Errorf("an address at a domain of its own answered %d (%s), want 204: the refusal is the account's domain's, not the route's", rec.Code, rec.Body.String())
	}
}

// trialFaultCode reads the code out of a refusal, which is what a client
// branches on (api.go's refusal shape). An answer that is not a refusal at all
// is reported and answered with no code, rather than stopping the test: what the
// caller did wrong is the status the test above already holds.
func trialFaultCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var held apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &held); err != nil {
		t.Errorf("the answer is not the API's error object: %v: %s", err, rec.Body.String())
		return ""
	}
	return held.Error.Code
}

// TestTrialIsRefusedForABodyItCannotRead: the body is the API's own JSON and it
// is read strictly, so a body with a field this route does not know — a seat
// count above all — and a body naming no workspace are refused before anything
// is minted. The id itself is not checked for shape here: a value that is not a
// project id mints a key no installation accepts, which is the asker's own loss.
func TestTrialIsRefusedForABodyItCannotRead(t *testing.T) {
	f := trialAs(t)
	bodies := map[string]string{
		"a field the route does not know": `{"workspace_id":"acme-widgets","note":"please"}`,
		"a seat count":                    `{"workspace_id":"acme-widgets","seats":3}`,
		"a seat count of none":            `{"workspace_id":"acme-widgets","seats":0}`,
		"no workspace":                    `{"note":"please"}`,
		"not an object at all":            `"acme-widgets"`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rec := f.ask(body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("the ask answered %d (%s), want 400", rec.Code, rec.Body.String())
			}
		})
	}
	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("a body the route cannot read minted %d key(s), want none", len(minted))
	}
}

// TestTrialIsSingleSeatWhateverTheAskNames: the self-serve trial is single-seat
// — §10.3 of the Subscription Terms makes the free seat's renewable key "the
// self-serve, single-seat trial" and B6 rules the same, while §17 keeps a
// multi-seat trial to the manual request form, which a person reads — so this
// door reads no seat count at all. A body naming one is refused, strictly, as
// every field the route does not read is, and the ask that is served mints the
// row's one seat: the row names it, the ask cannot, and the minter refuses a
// request that names another. The case exists because the opposite was true
// once: the row named no seats, so whatever the ask said was minted — a million
// seats, for a signed-in stranger.
func TestTrialIsSingleSeatWhateverTheAskNames(t *testing.T) {
	f := trialAs(t)
	greedy := f.ask(`{"workspace_id":"acme-widgets","seats":1000000}`)
	if greedy.Code != http.StatusBadRequest {
		t.Errorf("an ask naming a million seats answered %d (%s), want 400: a self-serve trial is single-seat and this door reads no seat count (§10.3, B6)", greedy.Code, greedy.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("an ask naming a seat count minted %d key(s), want none", len(minted))
	}

	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the ask this route reads answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	keys := f.mintedKeys()
	if len(keys) != 1 {
		t.Fatalf("the served ask minted %d key(s), want one", len(keys))
	}
	claims, _, err := licence.Verify([]byte(keys[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the trial's key does not verify: %v", err)
	}
	if claims.Seats != 1 {
		t.Errorf("the served trial carries %d seat(s), want the row's one", claims.Seats)
	}
}

// TestTrialIsMountedOnlyWithASignInVerifier: the route reads the sign-in
// verifier to learn the account half of §14's rule and the document store to
// learn the organisation half and to write the licence (P3.3), and it declares
// both seams where it is registered. It is the declaration and not the handler
// that keeps the service honest: a deployment that mounts this door without
// either is refused when it starts (api.go's `reaching`, `missing`), rather than
// answering every ask with a failure of its own or, with the declaration gone,
// serving the ask with a nil seam.
func TestTrialIsMountedOnlyWithASignInVerifier(t *testing.T) {
	route, known := apiRoutes.lookup(http.MethodPost, RouteTrial)
	if !known {
		t.Fatalf("no route is mounted at %s %s, so nothing serves a trial ask", http.MethodPost, RouteTrial)
	}
	for _, seam := range []struct {
		need apiNeed
		why  string
	}{
		{needSignIn, "the account half of §14's rule is the one the sign-in proved (K2.4, P1.3)"},
		{needDocs, "the organisation the trial is counted with and the licence it writes are both the store's (P2.1, P3.3)"},
	} {
		if !slices.Contains(route.needs, seam.need) {
			t.Errorf("the route %s %s does not declare %s it reads, and %s: a deployment holding none mounts it and answers every ask with a failure of its own",
				route.method, route.path, seam.need, seam.why)
		}
	}
	only := &apiRegistry{routes: []apiRoute{route}, byPath: map[string][]int{}}
	if err := only.missing(Deps{}); err == nil {
		t.Error("a deployment holding neither seam the trial reads is not refused the route, want the start refused (P1.0)")
	}
}

// TestTrialIsThrottledPerDomain: §14's trial is a speed bump and the throttle it
// is documented with is the **domain's** and not the address's — "the trial path
// is throttled per domain" (docs/licensing/licence-key.md, Limits, accepted) —
// because the account a caller asks from is one they may open another of while
// the organisation behind it is not. The fixture's rate is one ask per window, so
// the second account at the first account's domain meets the bucket from a
// different address (which a per-address rule would have let through), and an
// account at another domain still asks.
func TestTrialIsThrottledPerDomain(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 1, Window: time.Hour}
		f.signIn.caller = Caller{UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now}
		f.seededOrganisation(trialOrganisation, "Acme Ltd", orgTypeCustomer, "uid-trial", trialAccount, roleOwner)
	})
	if rec := f.askFrom(applicantIP, trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Fatalf("the first ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	// The same domain, another account, another address: this is the ask a
	// per-address bucket would have served.
	f.joinsAs(WireOrganisation{ID: trialOrganisation}, "uid-two", "colleague@acme.example", roleMember)
	second := f.askFrom("198.51.100.44:4040", trialBody("acme-other"))
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("a second account at the same domain answered %d (%s), want 429: §14's trial is throttled per domain and not per address", second.Code, second.Body.String())
	}
	// Another organisation asks and is served: what the bucket counts is the
	// domain and not the trial route as a whole.
	f.foundsAs("uid-three", "founder@widgets.example", "Widgets Ltd")
	third := f.askFrom("198.51.100.45:4040", trialBody("widgets-workspace"))
	if third.Code != http.StatusNoContent {
		t.Errorf("an ask from another domain answered %d (%s), want 204: the bucket is the domain's and not the route's", third.Code, third.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 2 {
		t.Errorf("the asks minted %d key(s), want the two that were served", len(minted))
	}
}

// trialOnTheDocstore is the fixture answering out of the deploy's own store
// (firestore_store.go over docstore's memory store) rather than the memory store
// a handler test usually drives. The rule the cases below hold is the store's:
// it is the durable store that refuses a document id its backend cannot file,
// and store.go's Memory files whatever it is handed — the pair the conformance
// suite exists for, and the reason the rule has to be refused here rather than
// read off the memory store's answer.
func trialOnTheDocstore(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, Production, func(f *fixture) {
		f.deps.Store = NewDocStore(docstore.NewMemory())
		f.signIn.caller = Caller{UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now}
		f.seededOrganisation(trialOrganisation, "Acme Ltd", orgTypeCustomer, "uid-trial", trialAccount, roleOwner)
	})
}

// TestTrialIsRefusedForANameTheStoreCannotFile: the two names a trial is filed
// under are made from strings the ask brought — the account the sign-in proved,
// and the installation name the workspace id goes into — and the store refuses a
// name its backend cannot hold at the moment it writes one, which is after the
// mint. An ask that reached the mint and then could not be claimed would leave a
// key signed for nobody, withdrawn into §8's list, which only grows, once per
// request a signed-in caller makes. So both names are refused before anything is
// minted: an account carrying a slash in its local part (§4.1's atext admits
// one, so the sign-in proves an address the account collection cannot file) and
// a workspace id longer than a document id may be. What the last ask of the case
// reads is that the two refusals were the names' and not the store's: the same
// account and the same store are served an ask naming what both halves can hold.
func TestTrialIsRefusedForANameTheStoreCannotFile(t *testing.T) {
	f := trialOnTheDocstore(t)
	for _, tc := range []struct {
		name    string
		account string
		body    string
		code    string
	}{
		{
			name:    "an account the account collection cannot hold",
			account: "founder/other@acme.example",
			body:    trialBody(workspaceID),
			code:    "unfileable_account",
		},
		{
			name:    "a workspace id longer than a document id may be",
			account: trialAccount,
			body:    trialBody(strings.Repeat("w", 2000)),
			code:    "unfileable_workspace",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.joinsAs(WireOrganisation{ID: trialOrganisation}, "uid-refused", tc.account, roleMember)
			rec := f.ask(tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("the ask answered %d (%s), want 400: the store cannot file the name it makes", rec.Code, rec.Body.String())
			}
			if code := trialFaultCode(t, rec); code != tc.code {
				t.Errorf("the refusal carries the code %q, want %q", code, tc.code)
			}
		})
	}

	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("the refused asks minted %d key(s) (%v), want none: a key signed for a name the store cannot file is one nobody holds", len(minted), minted)
	}
	withdrawn, err := f.deps.Store.Revocations(t.Context())
	if err != nil {
		t.Fatalf("reading the revocations: %v", err)
	}
	if len(withdrawn) != 0 {
		t.Errorf("the refused asks wrote %d key(s) into §8's revocation list, want none: the store's refusal used to arrive after the mint, so every such ask minted and revoked a key (%+v)", len(withdrawn), withdrawn)
	}
	trials, err := f.deps.Store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 0 {
		t.Errorf("the refused asks claimed %+v, want no trial: neither was refused by §14's rule", trials)
	}

	f.signsInAs("uid-trial", trialAccount)
	served := f.ask(trialBody(workspaceID))
	if served.Code != http.StatusNoContent {
		t.Fatalf("the ask naming what the store holds answered %d (%s), want 204: the refusals are the names' and not the store's", served.Code, served.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 1 {
		t.Errorf("the served ask minted %d key(s), want the one it was served", len(minted))
	}
	if trials, err := f.deps.Store.Trials(t.Context()); err != nil || len(trials) != 1 {
		t.Errorf("the store holds %+v (err=%v), want the one trial the served ask claimed", trials, err)
	}
}

// TestTrialInstallationIsANameTheStoreCanFile: the pair D2201 counts goes into
// the one installation field the store's Trial carries (K2.2, store.go), and the
// id a document is filed under is one the deploy's backend can hold. The project
// half is the ask's own string and nothing here bounds it, and since P3.3 the
// organisation half is the portal's own id — which the store mints and no
// request can spell (newID) — so the pairs below that carry a slash or a pipe in
// that half are shapes no caller can send and the join still has to survive: a
// name written by a later task, or a record seeded by an operator, must not reach
// into the other half. A join that let either half through unchanged would file a
// trial as a path into another collection — a write the store's own query of that
// collection never returns, and a read that answers about another document
// (docstore's `ErrUnstorableName`) — and a join that let the separator through
// would let one pair's name be another pair's, so one organisation's ask would
// spend the trial of the organisation whose name it spelled.
func TestTrialInstallationIsANameTheStoreCanFile(t *testing.T) {
	pairs := [][2]string{
		{"acme.example", workspaceID},
		{"acme.example", "a/b"},
		{"acme.example", "../../other"},
		{"acme.example", ".."},
		{"acme.example", "a|b"},
		{"a|b.example", "acme-widgets"},
		{"a/b.example", "widgets-workspace"},
	}
	seen := map[string][2]string{}
	for _, pair := range pairs {
		name := trialInstallation(pair[0], pair[1])
		if name == "." || name == ".." || len(name) > 1500 ||
			strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '/' }) {
			t.Errorf("the trial for %q and %q is counted as %q, which is not a name the deploy's store can file a document under",
				pair[0], pair[1], name)
		}
		if other, collided := seen[name]; collided {
			t.Errorf("%q/%q and %q/%q are one installation name (%q), so one organisation's ask would spend the other's trial (D2201)",
				other[0], other[1], pair[0], pair[1], name)
		}
		seen[name] = pair
	}
}

// TestTrialIsServedWhenItsIssuanceWentUnanswered: an error from the issuance
// write is not the same as a record that did not land. The store the deploy
// names writes one in a transaction that is re-run when its result is lost, and
// the re-run answers `ErrAlreadyRecorded` about its own commit — the case
// merchant.go's paid path re-reads for, and the claim above it here. The key has
// been minted, claimed and mailed by then, so answering a failure would hand the
// account a key the store holds no state for: the daily status check proves a
// key by its state (renew.go's `proved`), so that key is refused as one nobody
// issued, and asking again is answered 409 because the trial is spent — no
// method of the Store interface repairs a trial. The ask is served instead, and
// what the account was mailed is a key it can really check in.
func TestTrialIsServedWhenItsIssuanceWentUnanswered(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = unansweredIssuance{Store: f.store}
	f.start()
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204: its issuance landed and only its answer was lost", rec.Code, rec.Body.String())
	}
	minted := f.mintedKeys()
	if len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want one", len(minted))
	}
	// The pair is finished despite the store's error: the state is what the key
	// mailed to the account is checked in by.
	claims, _, err := licence.Verify([]byte(minted[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the served key does not verify: %v", err)
	}
	held, known, err := f.store.KeyState(t.Context(), claims.KeyID)
	if err != nil || !known {
		t.Fatalf("the mailed key has no state (known=%v err=%v), so the account cannot check it in (§8, renew.go's `proved`)", known, err)
	}
	if held.WorkspaceID != workspaceID {
		t.Errorf("the key's state names workspace %q, want %q", held.WorkspaceID, workspaceID)
	}
	if records := f.records(); len(records) != 1 || records[0].KeyID != claims.KeyID {
		t.Errorf("the store recorded %+v, want the one issuance of the key this ask served", records)
	}
	if len(f.mail.sent) != 1 || !strings.Contains(f.mail.sent[0].Body, minted[0]) {
		t.Errorf("the ask mailed %d message(s) and the key is not in them: %+v", len(f.mail.sent), f.mail.sent)
	}
}

// TestTrialIsNotServedWhenTheIssuanceIsAnotherKeys: the re-read is what tells a
// lost answer from a record that really did not land, and it has to answer about
// **this** key: a source the store already holds under another key's name is a
// write that did not land, and serving the ask would mail a key the store
// records nothing for and check in nothing for. What the account is handed is a
// failure, so the ask is one an operator reads (the trial's claim names the key
// it was minted for), and nothing is written for the key the mint answered.
func TestTrialIsNotServedWhenTheIssuanceIsAnotherKeys(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = anotherKeysIssuance{Store: f.store}
	f.start()
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the ask answered %d (%s), want 500: the source is held under a key this ask did not mint", rec.Code, rec.Body.String())
	}
	minted := f.mintedKeys()
	if len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want the one it could not record", len(minted))
	}
	claims, _, err := licence.Verify([]byte(minted[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the minted key does not verify: %v", err)
	}
	if records := f.records(); len(records) != 1 || records[0].KeyID == claims.KeyID {
		t.Errorf("the store recorded %+v, want the one record written under another key: the re-read has to answer about this ask's key and not about the source alone", records)
	}
	if _, known, err := f.store.KeyState(t.Context(), claims.KeyID); err != nil || known {
		t.Errorf("the store holds a state for the key this ask did not record (known=%v err=%v), want none: the record it re-read is another key's", known, err)
	}
}

// TestTrialIsServedWhenItsOwnClaimWentUnanswered: an error is not a claim that
// did not land, and the store's answer alone cannot tell the two apart — the
// deployed store re-runs its transaction when its result is lost and then reads
// its own committed documents, so it answers "already claimed" about the ask
// that claimed it. Withdrawing there destroys the ask's own trial: the store
// holds a trial naming a revoked key, the account is mailed nothing, and the
// account and the installation are barred for good, because no method of the
// Store interface repairs one. The key is what tells them apart, and the ask is
// served: the key is mailed, and the trial, the issuance and the state are the
// ones the served key reads back as.
func TestTrialIsServedWhenItsOwnClaimWentUnanswered(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = unansweredClaim{Store: f.store}
	f.start()
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204: its own claim landed and only its answer was lost", rec.Code, rec.Body.String())
	}
	minted := f.mintedKeys()
	if len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want one", len(minted))
	}
	claims, _, err := licence.Verify([]byte(minted[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the served key does not verify: %v", err)
	}
	if len(f.mail.sent) != 1 || !strings.Contains(f.mail.sent[0].Body, minted[0]) {
		t.Fatalf("the ask mailed %d message(s) and the key is not in them, so the account that claimed the trial was handed nothing: %+v", len(f.mail.sent), f.mail.sent)
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 || trials[0].KeyID != claims.KeyID {
		t.Fatalf("the store holds %+v, want the one trial naming the key this ask minted (%s)", trials, claims.KeyID)
	}
	if withdrawn := f.revocations(); len(withdrawn) != 0 {
		t.Errorf("the ask withdrew %d key(s), want none: the trial it landed names one of them, so the revocation would bar the account it was minted for (%+v)", len(withdrawn), withdrawn)
	}
	if records := f.records(); len(records) != 1 || records[0].KeyID != claims.KeyID {
		t.Errorf("the store recorded %+v, want the issuance of the key it served", records)
	}
	if held, known, err := f.store.KeyState(t.Context(), claims.KeyID); err != nil || !known {
		t.Errorf("the served key has no state (known=%v err=%v), so nothing can check it in (§8)", known, err)
	} else if held.WorkspaceID != workspaceID {
		t.Errorf("the key's state names workspace %q, want %q", held.WorkspaceID, workspaceID)
	}
}

// TestTrialIsNotWithdrawnWhenTheStoreCannotSay: when the claim fails and the
// store cannot be read either, nothing says whether the claim landed — and the
// two ways of being wrong are not equal. A key left where the mint put it is
// held by nobody (the mail never went out) and is what an operator reads off the
// trial if one landed; a trial withdrawn on a guess is destroyed, and the
// account and the installation are barred with no way back. Nothing is
// withdrawn, and the ask is answered a failure.
func TestTrialIsNotWithdrawnWhenTheStoreCannotSay(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = &blindAfterAClaim{Store: f.store}
	f.start()
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the ask answered %d (%s), want 500: the claim failed and the trials could not be read", rec.Code, rec.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want the one whose fate the store could not say", len(minted))
	}
	if withdrawn := f.revocations(); len(withdrawn) != 0 {
		t.Errorf("the ask withdrew %d key(s) on a claim it could not read, want none: withdrawing a trial that landed bars the account and the installation for good (%+v)", len(withdrawn), withdrawn)
	}
	if trials, err := f.store.Trials(t.Context()); err != nil || len(trials) != 0 {
		t.Errorf("the store holds %+v (err=%v), want no trial: this claim did not land", trials, err)
	}
}

// TestTrialIsNotSpentByAnAccountThatAlreadyHadOne: the domain's allowance is
// spent by the asks that mint and not by the ones §14 refuses. A domain is
// shared — a free mail domain is the case D453's own list exists for — so an
// account that already had its trial and keeps asking would otherwise hold the
// allowance at nothing and lock every other account at that domain out of a
// trial it never took. Here the fixture's rate is two asks, the account's own
// trial spends one, three refused repeats spend none, and the colleague who has
// had no trial is served with the one that is left.
func TestTrialIsNotSpentByAnAccountThatAlreadyHadOne(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 2, Window: time.Hour}
		f.signIn.caller = Caller{UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now}
		f.seededOrganisation(trialOrganisation, "Acme Ltd", orgTypeCustomer, "uid-trial", trialAccount, roleOwner)
	})
	if rec := f.askFrom(applicantIP, trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Fatalf("the account's own ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	// The same account again, three times, each from its own address: every one
	// is refused by §14's rule, and none of them is an ask the domain pays for.
	for _, repeat := range []struct{ ip, installation string }{
		{"198.51.100.20:4040", "acme-repeat-one"},
		{"198.51.100.21:4040", "acme-repeat-two"},
		{"198.51.100.22:4040", "acme-repeat-three"},
	} {
		rec := f.askFrom(repeat.ip, trialBody(repeat.installation))
		if rec.Code != http.StatusConflict {
			t.Errorf("a repeat ask for %s answered %d (%s), want 409: the account has had its trial, and a refusal mints nothing and must not spend the domain's allowance",
				repeat.installation, rec.Code, rec.Body.String())
		}
	}
	f.joinsAs(WireOrganisation{ID: trialOrganisation}, "uid-two", "colleague@acme.example", roleMember)
	colleague := f.askFrom("198.51.100.23:4040", trialBody("acme-colleague"))
	if colleague.Code != http.StatusNoContent {
		t.Errorf("a colleague at the same domain answered %d (%s), want 204: the allowance the repeats would have spent is still there",
			colleague.Code, colleague.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 2 {
		t.Errorf("the asks minted %d key(s), want the two that were served", len(minted))
	}
}

// TestTrialIsWithdrawnWhenTheClaimCannotBeWritten: §14 counts a trial in the
// store's check-and-write, and a mint stands before it. A claim the store
// refuses — the lost race, or a store that could not be written — leaves the key
// the mint answered recorded nowhere, and the store says so here: the trials
// read back name no such key, so the claim did not land, and the key is
// withdrawn rather than left live, which is what puts a key nobody holds into
// §8's list where the licensor can see it (K2.2).
func TestTrialIsWithdrawnWhenTheClaimCannotBeWritten(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = claimRefuser{Store: f.store, err: errors.New("the store could not be written")}
	f.start()
	rec := f.ask(trialBody(workspaceID))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the ask answered %d (%s), want 500: the claim could not be written", rec.Code, rec.Body.String())
	}
	minted := f.mintedKeys()
	if len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want the one it then had to withdraw", len(minted))
	}
	claims, _, err := licence.Verify([]byte(minted[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the minted key does not verify: %v", err)
	}
	withdrawn := f.revocations()
	if len(withdrawn) != 1 {
		t.Fatalf("the store holds %d revocation(s), want the one key this ask minted and could not record: %+v", len(withdrawn), withdrawn)
	}
	if withdrawn[0].KeyID != claims.KeyID {
		t.Errorf("the withdrawn key is %s, want the key this ask minted (%s)", withdrawn[0].KeyID, claims.KeyID)
	}
	if withdrawn[0].Reason != ReasonUnrecorded {
		t.Errorf("the key was withdrawn as %q, want %q: nothing holds it and nothing recorded it", withdrawn[0].Reason, ReasonUnrecorded)
	}
}
