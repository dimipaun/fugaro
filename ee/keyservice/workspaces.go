// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Workspaces and binding: the act that attaches a licence to a Firebase project
// and mints the key for it (portal plan P3.1; D2196, D2197, D2198, D2204, D2205).
//
// A workspace is the portal's object and the project is the installation's, and
// the two meet exactly once — here. Before the binding there is a licence and no
// key: the customer paid (a purchase) or signed up (the free seat), and nothing
// has been minted, because a key is for a workspace and the workspace is the
// Firebase project it names in its `workspace_id` claim (TN2). Binding is
// therefore three things in one act: the project is checked, the workspace is
// written, and the key is minted through the minter seam the paid path already
// uses (merchant.go's `paid`).
//
// Nothing here trusts the project id past its shape. It is a name a person types
// and it goes into a host name (D2205), so it is checked against Firebase's own
// grammar before anything else happens to it, and what the check admits is what
// a real project can be called and nothing wider.
//
// The three ways a binding ends are D2198's: it is immediate the first time, it
// mints a fresh secret every time, and it revokes the key it replaces — with the
// one exception D2206 names, an air-gapped licence, which is issued once as a
// file and never rebound at all.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// workspacesCollection holds one document per workspace, keyed by the
	// workspace's own portal id (wire_workspaces.go).
	workspacesCollection = "workspaces"
	// licenceFilesCollection holds the signed text of an air-gapped licence, one
	// document per entitlement, keyed by the entitlement's own id. It is a
	// collection of its own and not a field of the entitlement because the file
	// is the licence: it is what the customer holds, it is issued once (D2206),
	// and the read that serves it again — P3.2's `/keys/licence` — has to find
	// the same bytes the first download served.
	licenceFilesCollection = "licences"

	// rebindWindow is D2198's thirty days, counted from the rebind that set it:
	// the first rebind after a binding is not held by it, so an owner can undo a
	// binding somebody else made at once, and what this window then holds back is
	// the next one.
	rebindWindow = 30 * 24 * time.Hour

	// ReasonRebind is the store's reason for the key a rebind replaced. It sits
	// beside the merchant's three (merchant.go) because the store's list is one
	// list: what a licensor reads from it is which keys are out and why, and a
	// reason that said "unrecorded" for a rebind would be a support ticket
	// answered wrongly.
	ReasonRebind = "rebind"

	// bindingPrefix names the issuances a binding writes. The store's source is
	// an idempotency key, and a binding is not idempotent: every binding and
	// every rebind mints a key of its own, so the name carries the entitlement
	// and a nonce, and the prefix is what a rebind reads the keys it replaced
	// back with.
	bindingPrefix = "binding:"

	// productionDatabase is the address D2205's read goes to, with one %s for
	// the project id. Every Fugaro installation creates a Realtime Database
	// (`deploy/terraform/gcp/modules/firebase`), so the database is what says
	// whether an installation is there, and its own answer to a stranger is what
	// this service reads.
	productionDatabase = "https://%s-default-rtdb.firebaseio.com"

	// probeTimeout bounds the read above, and probeBodyBytes bounds what of its
	// answer is read: the answer is a line of JSON and a failure page can be
	// anything, and a probe a project can make this service buffer is a probe
	// that stops being a check.
	probeTimeout   = 5 * time.Second
	probeBodyBytes = 4 << 10
)

// The refusals binding owns. Every one of them is a sentinel rather than a
// message built where it failed, so that the route can answer each with the
// status and the sentence its own reader branches on.
var (
	// errBindingNotPermitted is a caller who is a member of the organisation and
	// not an owner or an admin of it. Binding mints the key for an installation
	// and moves the licence's workspace, and neither is a member's act.
	errBindingNotPermitted = errors.New("keyservice: binding a licence is an owner's or an admin's act")
	// errByAnotherEntitlement is a workspace already licensed by a grant that is
	// still active. It is D2204 read at this door: the new grant has to supersede
	// the old one before it can license the workspace, and a binding is not the
	// act that decides which licence an organisation is on.
	errByAnotherEntitlement = errors.New("keyservice: that workspace is licensed by another entitlement")
	// errEntitlementBound is the licence that was bound between this request's
	// read and its write: the second of two bindings that raced, answered as the
	// reload the person has to make rather than as a binding that half happened.
	errEntitlementBound = errors.New("keyservice: that licence was bound a moment ago")
	// errEntitlementNotActive is a licence that is superseded, revoked or
	// expired: there is no key to mint for a term that is not the one in force.
	errEntitlementNotActive = errors.New("keyservice: that licence is not active")
	// errEntitlementEnded is a licence whose term has run out. It is separate
	// from the status above because the person's fix is different: a superseded
	// licence is replaced by the one that superseded it, and an ended one by
	// buying again.
	errEntitlementEnded = errors.New("keyservice: that licence's term has ended")
	// errRebindTooSoon is D2198's window.
	errRebindTooSoon = errors.New("keyservice: a workspace may be rebound once every thirty days")
	// errAirGappedRebind is D2206: an air-gapped licence is issued once as a file
	// and never rebound, because a second issue of it is a second live copy of a
	// key nothing can revoke.
	errAirGappedRebind = errors.New("keyservice: an air-gapped licence is issued once and never rebound")
	// errTrialForAnotherProject is a trial asked to be attached to a project its
	// trial was not for: §14's trial is the trial of the project it was asked for
	// (D2201's keying, D2185) and a licence that could be moved to another one
	// would mint a trial-row key for an installation the rule refused.
	errTrialForAnotherProject = errors.New("keyservice: a trial is bound to the project it was asked for")
)

// The refusals D2205's check owns, kept beside the read that makes them.
var (
	errNoSuchEntitlement = errors.New("keyservice: that licence is not one this caller may bind")
	errProjectIDBound    = errors.New("keyservice: that Firebase project is bound to another workspace")
	errNoInstallation    = errors.New("keyservice: no Fugaro installation answers in that project")
	errProbeUnreachable  = errors.New("keyservice: the project's database could not be reached")
	errProjectIDShape    = errors.New("keyservice: that is not a Firebase project id")
)

// projectIDPattern is Firebase's own grammar for a project id, and it is the
// schema's pattern for the same field (`portal/schemas/workspaces.schema.json`):
// the two are one rule read by two readers, so a change here is a change there.
var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// validProjectID answers whether one id may be put into a host name. It is
// checked before anything else is done with the id (D2205), because the id
// arrives from a person and a name that reaches a host builder is a name that
// can name another host.
func validProjectID(projectID string) bool {
	return projectIDPattern.MatchString(projectID)
}

// projectInstallation is D2205's read: the project's own Realtime Database,
// unauthenticated, at its root. A database whose rules are locked — which is
// what every installation's are — answers `Permission denied`, and that answer
// is the one that says an installation is there; a project with no database
// answers that there is none.
//
// The client is a field rather than a package-level http.DefaultClient because a
// test replaces this whole value with a server of its own: the answers a real
// database gives are the one half of D2205 this repository cannot check, and a
// seam is what keeps that boundary where it can be seen.
type projectInstallation struct {
	database string
	client   *http.Client
}

// probeInstallation is the one probe a deployment reads a project with.
var probeInstallation = projectInstallation{
	database: productionDatabase,
	client: &http.Client{
		Timeout: probeTimeout,
		// A redirect is not an installation's answer: what says one is there is
		// the database's own refusal, and a client that followed a redirect
		// would read whatever answered at the other end of it as that refusal.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	},
}

// url is the address one project id is read at.
func (p projectInstallation) url(projectID string) string {
	return fmt.Sprintf(p.database, projectID) + "/.json"
}

// ask answers whether a Fugaro installation answers in this project (D2205).
//
// The shape is checked first and the id never reaches the address builder
// without it. Then the read: a refusal of permission is an installation with
// locked rules and binding proceeds; "no such database" is a project with no
// installation, and the person is told to set one up; anything else — no answer,
// a timeout, a failure of the database's own — is a project this service did not
// check, and an unchecked project is never bound.
func (p projectInstallation) ask(ctx context.Context, projectID string) error {
	if !validProjectID(projectID) {
		return fmt.Errorf("%w: %q is not one Firebase admits", errProjectIDShape, projectID)
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url(projectID), nil)
	if err != nil {
		return fmt.Errorf("%w: %v", errProbeUnreachable, err)
	}
	answer, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", errProbeUnreachable, err)
	}
	defer func() { _ = answer.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(answer.Body, probeBodyBytes))
	if err != nil {
		return fmt.Errorf("%w: %v", errProbeUnreachable, err)
	}
	switch {
	case answer.StatusCode == http.StatusUnauthorized || answer.StatusCode == http.StatusForbidden:
		return nil
	case answer.StatusCode == http.StatusNotFound || missingDatabase(string(body)):
		return errNoInstallation
	default:
		return fmt.Errorf("%w: the project's database answered %s", errProbeUnreachable, answer.Status)
	}
}

// missingDatabase answers whether the database's own answer says the project has
// no database at all.
//
// The wording is the database's and not this service's, so it is matched as the
// two phrases the reconciliation records rather than parsed (F41): a project
// with no installation answers that there is no such database, and a project
// with a locked one answers that permission is denied — two answers a reader has
// to tell apart, and the second is the ordinary case this service reads as yes.
// The phrases are the one thing here that a live project has to confirm, since
// the answers a real database gives are UNVERIFIED (the reconciliation's open
// list); the status code covers the same refusal where the database states it,
// which is why the phrase list is a fallback and not the check.
func missingDatabase(answer string) bool {
	said := strings.ToLower(answer)
	return strings.Contains(said, "no such database") || strings.Contains(said, "database not found")
}

// bindEntitlement binds one licence to one Firebase project: it checks the
// project, mints the key for it, writes the workspace and the licence, and — on
// a rebind — withdraws the key it replaced.
//
// The order is the point. Everything that can refuse is refused before anything
// is minted, and the mint comes before the writes because the mint is the one
// step that cannot be undone by failing: a key the minter has signed exists, and
// a write that fails with it in hand withdraws it (merchant.go's `paid` does the
// same for a purchase that cannot be recorded).
func (s *Service) bindEntitlement(ctx context.Context, actor Caller, entitlementID, projectID string) (WireWorkspace, error) {
	entitlement, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, entitlementID)
	if err != nil {
		return WireWorkspace{}, err
	}
	if !present {
		return WireWorkspace{}, errNoSuchEntitlement
	}
	// The licence's own organisation is the one whose membership decides: a
	// caller who does not belong to it is told the licence is not there, which is
	// what api_orgs.go's reads answer about an organisation that is not theirs.
	_, membership, member, err := s.ownMembership(ctx, actor, entitlement.OrganisationID)
	if err != nil {
		return WireWorkspace{}, err
	}
	if !member {
		return WireWorkspace{}, errNoSuchEntitlement
	}
	if membership.Role != roleOwner && membership.Role != roleAdmin {
		return WireWorkspace{}, errBindingNotPermitted
	}
	if entitlement.Status != entitlementActive {
		return WireWorkspace{}, errEntitlementNotActive
	}
	now := s.now().UTC()
	if !entitlement.EndsAt.After(now) {
		return WireWorkspace{}, errEntitlementEnded
	}
	rebinding := entitlement.BoundAt != nil
	if rebinding {
		if entitlement.AirGapped {
			return WireWorkspace{}, errAirGappedRebind
		}
		if entitlement.RebindableAt != nil && now.Before(*entitlement.RebindableAt) {
			return WireWorkspace{}, errRebindTooSoon
		}
	}
	// The keys a rebind replaces are read here, at the instant it decides to
	// rebind, and not when the mint returns: two rebinds of one licence at once
	// are two requests that read the same live key, and the one that commits
	// first mints the key the other would withdraw if the list were read again
	// below. The probe (D2205) is the slow step both of them wait on, and the
	// mint is slower still. What is read here is not revoked here: the
	// transaction below revokes it, and only if the state this request read is
	// still the state the store holds.
	var replacing []string
	if rebinding {
		replacing, err = s.bindingKeys(ctx, entitlement.ID)
		if err != nil {
			return WireWorkspace{}, err
		}
	}
	workspace, err := s.bindingWorkspace(ctx, entitlement, projectID, now)
	if err != nil {
		return WireWorkspace{}, err
	}
	// §14's trial is the trial of one project and of no other (D2201's keying,
	// D2185), so a trial-sourced licence may be attached to the project its trial
	// was asked for and never to a second one. That project is what the trial
	// wrote into this licence's own workspace: `trialLicence` names the workspace
	// for the project the ask named and writes no project id onto it — a trial
	// that wrote one would bar the project's real owner from binding it for as
	// long as operations had not transferred it (D2201, F37, TN-P1) — so the name
	// is the whole of what the trial claimed, and it is the licence's own record
	// of it rather than a second rule read against the trials store.
	//
	// Without this the rule is a formality. An organisation refused a trial for a
	// project asks for a decoy id, which is a fresh organisation-and-project pair
	// and is served, and then binds that licence — written bound (BoundAt) and so
	// rebindable at once (D2198) — to the project it was refused: what is minted
	// is a trial-row key for an installation §14's rule refused, and the
	// organisation repeats it for every owner or admin it adds.
	//
	// The workspace this reads is the one the licence names and never one this
	// request made: a trial licence without the workspace its trial wrote is not a
	// shape this service writes, and a workspace named by this request would be
	// no check at all. That no route renames a workspace is what makes its name
	// the record here — a route that came to rename one is a route that has to
	// keep this clause in view.
	if entitlement.Source == entitlementSourceTrial && (entitlement.WorkspaceID == nil || workspace.Name != projectID) {
		return WireWorkspace{}, errTrialForAnotherProject
	}
	// The cheap half of D2204's check: it answers a project somebody already
	// bound without a probe and a mint, and the transaction's own query is the
	// half that holds.
	if err := s.refuseBoundProject(ctx, s.docs, projectID, workspace.ID); err != nil {
		return WireWorkspace{}, err
	}
	if err := probeInstallation.ask(ctx, projectID); err != nil {
		return WireWorkspace{}, err
	}
	source, err := bindingSource(entitlement.ID)
	if err != nil {
		return WireWorkspace{}, err
	}
	request := RequestV1{
		Environment: s.cfg.Environment,
		Policy:      entitlement.Row,
		// The project id is the key's workspace (TN2), which is the whole reason
		// this act exists: the installation compares that claim with its own
		// project, and a key carrying the portal's own workspace id would verify
		// nowhere.
		WorkspaceID:  projectID,
		Organisation: entitlement.OrganisationID,
		Seats:        entitlement.Seats,
		// The key ends where the customer's term ends and never past it (F30):
		// the term runs from the payment and not from this instant (D2197).
		RequestedEnd: entitlement.EndsAt,
	}
	if entitlement.AirGapped {
		// D2206 and F42: an air-gapped key has no secret, because nothing can ever
		// prove one — the key never contacts the server. The minter would mint one
		// and answer it, and a secret that exists is one a door somebody adds later
		// can hand out, so it is handed the hash of a secret made here and dropped:
		// the claim names a hash nothing holds the preimage of. A hash derived from
		// the entitlement instead would be a secret anybody who can read its id
		// could prove to /renew.
		dropped, err := newSecret()
		if err != nil {
			return WireWorkspace{}, err
		}
		request.RenewalSecretHash = digest(dropped)
	}
	answer, err := s.paid(ctx, request, "", source, "")
	if err != nil {
		return WireWorkspace{}, err
	}
	bound := now
	var answered WireWorkspace
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		held, present, err := getRecord[WireEntitlement](ctx, tx, entitlementsCollection, entitlement.ID)
		if err != nil {
			return err
		}
		if !present {
			return errNoSuchEntitlement
		}
		if held.Status != entitlementActive {
			return errEntitlementNotActive
		}
		// The state this binding was decided on, re-read: another binding of the
		// same licence between the read above and this write is the one thing
		// that would otherwise mint a second key for it and write two bindings.
		if (held.BoundAt != nil) != rebinding {
			return errEntitlementBound
		}
		if rebinding && held.RebindableAt != nil && now.Before(*held.RebindableAt) {
			return errRebindTooSoon
		}
		current, present, err := getRecord[WireWorkspace](ctx, tx, workspacesCollection, workspace.ID)
		if err != nil {
			return err
		}
		if !present {
			current = workspace
		}
		if current.OrganisationID != entitlement.OrganisationID {
			return errNoSuchEntitlement
		}
		// D2204's one project, one installation: asked here, inside the transaction
		// that writes the binding, and not only above. A query outside a transaction
		// is a check two binds of one project id pass together — the store commits
		// the second without knowing the first happened — and this query is part of
		// what the transaction read, so a competing bind is either seen here or a
		// conflict that runs this again on the state that includes it.
		if err := s.refuseBoundProject(ctx, tx, projectID, current.ID); err != nil {
			return err
		}
		// D2198's revocation is made here and not on the way in: what a recorded
		// rebind must not leave behind is a live key at the installation it moved
		// away from, and what a rebind that loses its race must not leave behind is a
		// key withdrawn from the binding somebody else's request just made. The keys
		// were read before the probe, and every guard above has re-read the state
		// that read decided on, so a rebind that has lost the race returns there and
		// revokes nothing at all — which is the whole of what keeps a winner's key
		// live. Each withdrawal is made through this transaction, so it lands with
		// the binding or not at all.
		if rebinding {
			if err := s.revokeReplacedBindingKeys(ctx, tx, replacing, now); err != nil {
				return err
			}
		}
		project := projectID
		current.ProjectID = &project
		current.BoundAt = &bound
		current.EntitlementID = &entitlement.ID
		if err := putRecord(ctx, tx, workspacesCollection, current.ID, current); err != nil {
			return err
		}
		held.BoundAt = &bound
		if held.WorkspaceID == nil {
			held.WorkspaceID = &current.ID
		}
		if rebinding {
			// The clock starts with the rebind itself (D2198): what it holds
			// back is the next one, and this one has already happened.
			next := now.Add(rebindWindow)
			held.RebindableAt = &next
		}
		if err := putRecord(ctx, tx, entitlementsCollection, held.ID, held); err != nil {
			return err
		}
		if entitlement.AirGapped {
			// D2206's file is written with the binding and not after it: the file
			// is the licence, so a binding committed without one is an air-gapped
			// customer whose page offers a download that answers nothing, and no
			// other act of this service ever writes it.
			if err := keepLicenceFile(ctx, tx, entitlement.ID, answer); err != nil {
				return err
			}
		}
		answered = current
		return nil
	})
	if err != nil {
		return WireWorkspace{}, errors.Join(err, s.withdrawUnrecorded(ctx, answer.Record.KeyID))
	}
	return answered, nil
}

// bindingWorkspace answers the workspace one binding writes to: the one the
// licence already names, or a new one made for it.
//
// The second is what a free seat needs (F6, D2184): the seat is granted to an
// organisation that has no workspace at all, so the binding that mints its key
// is also what creates the workspace the key belongs to. Its name is the project
// id — the person named no workspace, and asking them for one after they have
// paid is one more thing between them and the key.
func (s *Service) bindingWorkspace(ctx context.Context, entitlement WireEntitlement, projectID string, now time.Time) (WireWorkspace, error) {
	if entitlement.WorkspaceID != nil {
		held, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, *entitlement.WorkspaceID)
		if err != nil {
			return WireWorkspace{}, err
		}
		if !present || held.OrganisationID != entitlement.OrganisationID {
			return WireWorkspace{}, errNoSuchEntitlement
		}
		if held.EntitlementID != nil && *held.EntitlementID != entitlement.ID {
			return WireWorkspace{}, errByAnotherEntitlement
		}
		return held, nil
	}
	id, err := newID()
	if err != nil {
		return WireWorkspace{}, fmt.Errorf("keyservice: the workspace's id: %w", err)
	}
	return WireWorkspace{
		ID:             id,
		OrganisationID: entitlement.OrganisationID,
		Name:           projectID,
		CreatedAt:      now,
	}, nil
}

// refuseBoundProject answers whether one Firebase project is already bound,
// across every organisation (D2204): a project is one installation and an
// installation runs one key, so a second workspace naming it would be a
// workspace whose key never works — and the answer would take a support ticket
// to find.
//
// The workspace being bound is not another workspace, which is what makes a
// rebind to the project the workspace already holds an ordinary rebind rather
// than a collision (D2198).
//
// It is asked twice: once against the store, which answers the ordinary case of a
// project somebody already bound without a probe and a mint, and once inside the
// transaction that writes the binding, which is the one that holds — a query is
// part of what a transaction read (api.go's Tx), so the second of two bindings of
// one project id is either seen there or runs the transaction again on the state
// that holds it.
func (s *Service) refuseBoundProject(ctx context.Context, from recordReader, projectID, workspaceID string) error {
	held, err := recordsWhere[WireWorkspace](ctx, from, workspacesCollection, Where{Field: "project_id", Value: projectID})
	if err != nil {
		return err
	}
	for _, other := range held {
		if other.ID != workspaceID {
			return errProjectIDBound
		}
	}
	return nil
}

// bindingSource names the issuance one binding writes. The store's source is an
// idempotency key and every binding mints a key of its own, so the name carries
// a nonce: two bindings under one source would be the second read as a
// redelivery of the first and refused, which is a customer whose rebind did
// nothing.
func bindingSource(entitlementID string) (string, error) {
	nonce, err := newSecret()
	if err != nil {
		return "", fmt.Errorf("keyservice: the binding's source: %w", err)
	}
	return bindingPrefix + entitlementID + ":" + nonce, nil
}

// bindingKeys answers the keys this licence has bound, as the store's issuance
// list stands at the instant it is read: the key a rebind replaces, and the ones
// earlier rebinds already withdrew. The store's source is an idempotency key and
// this area's own names every binding by the licence it belongs to
// (bindingPrefix), which is the one way the list is read back.
//
// What is read here is what a rebind replaces, so it is read once, before the
// probe: a key minted after this read belongs to a binding this request did not
// see. What keeps such a key from being withdrawn is not this read but the
// transaction that revokes: the list held here is one the store may have added
// to since, and it is the guards there that decide whether anything on it is
// revoked at all.
func (s *Service) bindingKeys(ctx context.Context, entitlementID string) ([]string, error) {
	issued, err := s.store.Issuances(ctx)
	if err != nil {
		return nil, err
	}
	prefix := bindingPrefix + entitlementID + ":"
	keys := make([]string, 0, 1)
	for _, record := range issued {
		if strings.HasPrefix(record.Source, prefix) {
			keys = append(keys, record.KeyID)
		}
	}
	return keys, nil
}

// revokeReplacedBindingKeys withdraws the keys one rebind read, through the
// transaction that makes the rebind (D2198). The key a rebind replaces is the key
// of a workspace the licence has moved away from, and the installation holding it
// is one nobody is looking at any more: it is revoked rather than left to run,
// which is what makes a leaked installation's key stop at the rebind.
//
// The withdrawal goes through the transaction and not through the store's own
// Revoke (store.go). The two seams are one document store — the deploy hands
// NewDocStore (firestore_store.go) the database this seam is reached over — so the
// store's Revoke would run a transaction inside this one, which the backend
// refuses (docstore's errNestedTransaction); and on a deployment that reached the
// two seams over two stores it would commit the withdrawal even when the binding
// it belongs to never landed.
//
// It is called after every guard in the transaction has re-read the state this
// list was read against, which is the one thing that keeps it from withdrawing a
// key that belongs to a binding another request made in between: the list holds
// the keys this rebind read, and the store may have added to them since. A key
// that was already withdrawn keeps the entry it has, which is what makes a retry
// of a failed rebind finish the same list, and why repeating this call — a store
// that runs the transaction again, a person who tries again — changes nothing.
//
// The whole list goes in one call, before the transaction writes the binding it
// belongs to: a transaction reads before it writes (docstore's ErrReadAfterWrite),
// and what the call reads is which of these keys are withdrawn already.
//
// What the list is read by is §8's renewal and status, so a revoked key's
// renewal secret fetches nothing at all — no successor and no answer about where
// the licence went.
func (s *Service) revokeReplacedBindingKeys(ctx context.Context, tx Tx, keyIDs []string, at time.Time) error {
	if len(keyIDs) == 0 {
		return nil
	}
	revocations := make([]Revocation, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		revocations = append(revocations, Revocation{KeyID: keyID, Reason: ReasonRebind, At: at})
	}
	return tx.Revoke(ctx, revocations)
}

// keepLicenceFile writes the file an air-gapped licence is issued as (D2206),
// inside the transaction that commits the binding it belongs to. The file is the
// signed key text and nothing else: it holds no secret, which is what makes it a
// file a customer can keep, and what is stored is what the first download served,
// so reading it again is the same bytes rather than a second issue.
func keepLicenceFile(ctx context.Context, tx Tx, entitlementID string, answer MintedV1) error {
	doc, err := storedDoc(WireLicenceFile{
		KeyID:       answer.Record.KeyID,
		Licence:     answer.Key,
		PaidThrough: answer.Record.Expires,
	})
	if err != nil {
		return err
	}
	return tx.Put(ctx, licenceFilesCollection, entitlementID, doc)
}

// currentOrganisation is the organisation a route without one in its path reads:
// the one the caller last worked in, which is the head of the switcher's own
// list and where a person lands who has not chosen one in this session
// (api_orgs.go's apiListOrganisations).
//
// The rule that organisation's type demands is applied here and not in the list
// (D2207, F10): the list is what the caller's own memberships are in, and a
// request that reads one organisation's workspaces is a request that uses one
// membership, which is where the spec puts the refusal.
func (s *Service) currentOrganisation(ctx context.Context, actor Caller) (WireOrganisation, bool, error) {
	orgs, err := s.organisationsOf(ctx, actor)
	if err != nil {
		return WireOrganisation{}, false, err
	}
	if len(orgs) == 0 {
		return WireOrganisation{}, false, nil
	}
	if err := s.requireSSO(actor, orgs[0]); err != nil {
		return WireOrganisation{}, false, err
	}
	return orgs[0], true, nil
}

// oldestFirst orders records the way a list is read: the oldest first, and by id
// where two were written at the same instant. It is one rule for the two lists of
// this area because it is one answer a reader gets: the store answers a query in
// no particular order (api.go's Docs), and a list that came back in a different
// order on every read would be a screen whose cards moved.
func oldestFirst[T any](held []T, created func(T) time.Time, id func(T) string) {
	sort.Slice(held, func(i, j int) bool {
		left, right := created(held[i]), created(held[j])
		if left.Equal(right) {
			return id(held[i]) < id(held[j])
		}
		return left.Before(right)
	})
}
