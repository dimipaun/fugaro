// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The operations API's routes (portal plan P5.1; D2187, D2188, D2190, D2204,
// D2207, D456, F9, F40).
//
// The paths are the SPA's half of one contract and are written once
// (`ee/portal/src/screens/ops/api.ts`) rather than invented on both sides: the
// plan pinned no path for this area, so P5.3's client is what registers them
// here, and a reconciliation between the two moves one file instead of seven.
// `/api/ops/reports` is deliberately not here — the month of reports is P5.2's
// route and its own shape.
//
// Every answer is a shape the portal already carries and never one made up for
// a screen (F20): a read answers the bare array of the shape its area declares
// — an organisation, a membership, an entitlement, key metadata, an event, a
// ledger line, an audit line — and a write answers the record it wrote.
//
// What every route here shares is the gate, and the gate is two rules: the
// caller is a member of the operations organisation with the role the request's
// kind needs (D2187), and a request that changes anything carries a sign-in from
// the last five minutes. Both are asked in one place (`operationsCaller`) so a
// route added later cannot hold one and forget the other, which is the reason
// the table itself exists (api.go).
//
// Every read of this area writes its line **between** the gate and the read
// (`operationsLine`), including the reads that are only lookups (D456). The
// order is the point: a line written after the read is one a failure can lose
// while the caller already holds the customer's metadata it is about. The query
// is validated before the line is written, so a typo answers `invalid_query`
// rather than becoming an entry in the log.

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// The routes this area mounts, spelled as `ee/portal/src/screens/ops/api.ts`
// spells them.
const (
	apiOpsMembershipPath         = "/api/ops/membership"
	apiOpsOrganisationsPath      = "/api/ops/organisations"
	apiOpsWorkspacesPath         = "/api/ops/workspaces"
	apiOpsEntitlementsPath       = "/api/ops/entitlements"
	apiOpsMembersPath            = "/api/ops/members"
	apiOpsKeysPath               = "/api/ops/keys"
	apiOpsEventsPath             = "/api/ops/events"
	apiOpsEventResolutionsPath   = "/api/ops/events/resolutions"
	apiOpsLedgerPath             = "/api/ops/ledger"
	apiOpsAuditPath              = "/api/ops/audit"
	apiOpsListsPath              = "/api/ops/lists"
	apiOpsMintsPath              = "/api/ops/mints"
	apiOpsEntitlementActionsPath = "/api/ops/entitlement-actions"
	apiOpsProjectTransfersPath   = "/api/ops/project-transfers"
	apiOpsHandoverCutPath        = "/api/ops/keys/{id}/handover-cut"
)

func init() {
	registerAPI(http.MethodGet, apiOpsMembershipPath, (*Service).apiOpsMembership, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsOrganisationsPath, (*Service).apiOpsOrganisations, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsWorkspacesPath, (*Service).apiOpsWorkspaces, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsEntitlementsPath, (*Service).apiOpsEntitlements, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsMembersPath, (*Service).apiOpsMembers, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsKeysPath, (*Service).apiOpsKeys, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsEventsPath, (*Service).apiOpsEvents, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsLedgerPath, (*Service).apiOpsLedger, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsAuditPath, (*Service).apiOpsAuditLog, reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOpsListsPath, (*Service).apiOpsReadLists, reaching(needDocs, needSignIn))

	registerAPI(http.MethodPost, apiOpsEventResolutionsPath, (*Service).apiOpsResolveEvent,
		withBody(maxOperationsBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPut, apiOpsListsPath, (*Service).apiOpsSaveLists,
		withBody(maxOperationsBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiOpsMintsPath, (*Service).apiOpsMint,
		withBody(maxOperationsBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiOpsEntitlementActionsPath, (*Service).apiOpsAct,
		withBody(maxOperationsBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiOpsProjectTransfersPath, (*Service).apiOpsTransfer,
		withBody(maxOperationsBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiOpsHandoverCutPath, (*Service).apiOpsCutHandover,
		reaching(needDocs, needSignIn))
}

// operationsCaller is the two gates every route of this area passes, and the
// three lines each route would otherwise write for itself.
//
// The freshness rule is D2187's and it is asked **after** the role: a member
// whose sign-in is an hour old is refused for being a member, which is the
// answer that tells them what would change it.
func (s *Service) operationsCaller(w http.ResponseWriter, r *http.Request, need operationsNeed) (Caller, operationsAccess, bool) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return Caller{}, operationsAccess{}, false
	}
	access, err := s.operationsFor(r.Context(), actor, need)
	if err != nil {
		refuseOperations(w, err)
		return Caller{}, operationsAccess{}, false
	}
	if need != operationsMayRead {
		if err := s.requireFreshOperationsSignIn(actor); err != nil {
			refuseOperations(w, err)
			return Caller{}, operationsAccess{}, false
		}
	}
	return actor, access, true
}

// operationsLine writes the line one read is, and answers whether it was
// written. A line that cannot be written refuses the read: what carries on after
// its entry was lost is the one thing the log exists to prevent (audit.go's
// recordAudit, D456).
func (s *Service) operationsLine(w http.ResponseWriter, r *http.Request, actor Caller, action string, organisationID, targetID, detail *string) bool {
	if err := s.operationsAudit(r.Context(), actor, action, organisationID, targetID, true, detail); err != nil {
		failAPI(w, err)
		return false
	}
	return true
}

// operationsBody reads one write's body into its shape. The strictness is
// decodeWire's (wire.go): a body carrying a field this shape does not have, a
// field twice, a field missing, or a null where the field is not optional is
// refused here rather than half-read.
func operationsBody(w http.ResponseWriter, r *http.Request, shape any) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return false
	}
	if err := decodeWire(body, shape); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return false
	}
	return true
}

// operationsParam reads one id a read names out of the query. An absent one and
// one longer than a name can be are the same answer, and it is a refusal rather
// than a lookup: a read nobody named a subject for is not a read of everything.
//
// The bound is also what keeps an id out of the audit line at a length the line
// was not sized for: the id a read names is written into that line's own fields
// before the read happens.
func operationsParam(r *http.Request, name string) (string, bool) {
	held := strings.TrimSpace(r.URL.Query().Get(name))
	if held == "" || len(held) > maxOperationsLookupTerm {
		return "", false
	}
	return held, true
}

// operationsMonth reads one month in the only form the ledger and the reports
// agree on (`YYYY-MM`, UTC), checked before the read's line is written so that a
// typo is not an entry in the log.
func operationsMonth(month string) (string, bool) {
	held := strings.TrimSpace(month)
	if _, err := time.Parse("2006-01", held); err != nil {
		return "", false
	}
	return held, true
}

// apiOpsMembership answers the caller's own place in the operations
// organisation, which is the one read the whole screen is gated on: a caller
// who is not a member is refused here and the SPA mounts no section at all
// (ops.tsx).
func (s *Service) apiOpsMembership(w http.ResponseWriter, r *http.Request) {
	actor, access, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	if !s.operationsLine(w, r, actor, actionOperationsMembershipRead, &access.Organisation.ID, &access.Membership.UID, nil) {
		return
	}
	answerAPI(w, http.StatusOK, access.Membership)
}

// apiOpsOrganisations is the lookup every investigation starts from: the
// organisations whose name holds the term, and the one whose id is it.
func (s *Service) apiOpsOrganisations(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if term == "" || len(term) > maxOperationsLookupTerm {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionOrganisationSearch, nil, nil, &term) {
		return
	}
	found, err := s.operationsSearchOrganisations(r.Context(), term)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, found)
}

func (s *Service) apiOpsWorkspaces(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	organisationID, ok := operationsParam(r, "organisation_id")
	if !ok {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionWorkspaceList, &organisationID, nil, nil) {
		return
	}
	held, err := s.operationsWorkspaces(r.Context(), organisationID)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

func (s *Service) apiOpsEntitlements(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	organisationID, ok := operationsParam(r, "organisation_id")
	if !ok {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionEntitlementList, &organisationID, nil, nil) {
		return
	}
	held, err := s.operationsEntitlements(r.Context(), organisationID)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// apiOpsMembers is the same read P2.1's `/api/orgs/{id}/members` answers, at the
// door the operations membership widens it through: it is one act under one
// action name (organisation.members.read), because two names for one read would
// be two counts of it in the log.
func (s *Service) apiOpsMembers(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	organisationID, ok := operationsParam(r, "organisation_id")
	if !ok {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionOrganisationMembersRead, &organisationID, nil, nil) {
		return
	}
	held, err := s.operationsMembers(r.Context(), organisationID)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// apiOpsKeys answers one workspace's key metadata (F5). The parameter carries
// the PORTAL workspace id while the answer's own `workspace_id` is the Firebase
// project id the key's claim names (TN2) — two ids under one name, which is the
// answer shape's and not this route's (wire_keys.go).
func (s *Service) apiOpsKeys(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	workspaceID, ok := operationsParam(r, "workspace_id")
	if !ok {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionKeyList, nil, &workspaceID, nil) {
		return
	}
	held, err := s.operationsKeys(r.Context(), workspaceID)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// apiOpsEvents is the queue a person decides: the parked Paddle events (F15,
// F16, F20). It is the only queue of this area whose items have a wire shape,
// and `state` is read rather than assumed so that a second state is a route
// change and not a silent renaming of this one.
func (s *Service) apiOpsEvents(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state != ledgerParked {
		refuseOperations(w, errOperationsBadState)
		return
	}
	if !s.operationsLine(w, r, actor, actionQueueRead, nil, nil, nil) {
		return
	}
	held, err := s.operationsParkedEvents(r.Context(), state)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

func (s *Service) apiOpsResolveEvent(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	var asked WireEventResolution
	if !operationsBody(w, r, &asked) {
		return
	}
	resolved, err := s.resolveOperationsEvent(r.Context(), actor, asked)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, resolved)
}

// apiOpsLedger is one UTC month of the ledger: a month is UTC because the
// reports are (P5.2), and a month read in the browser's own zone would be a
// different month from the report beside it.
func (s *Service) apiOpsLedger(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	month, ok := operationsMonth(r.URL.Query().Get("month"))
	if !ok {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if !s.operationsLine(w, r, actor, actionLedgerRead, nil, nil, &month) {
		return
	}
	held, err := s.operationsLedger(r.Context(), month)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// apiOpsAuditLog answers the newest page of the log. Reading it is audited like
// everything else (D456), which is deliberate: who looked at the log is the one
// fact an incident review starts from.
func (s *Service) apiOpsAuditLog(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	if !s.operationsLine(w, r, actor, actionAuditRead, nil, nil, nil) {
		return
	}
	held, err := s.operationsAuditLog(r.Context())
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// apiOpsReadLists answers the two lists the operations owner set. A member reads
// them and is offered no way to save them (D2187, F40). An empty list is a real
// answer and not a missing one: it refuses every sign-in it would have admitted.
func (s *Service) apiOpsReadLists(w http.ResponseWriter, r *http.Request) {
	actor, access, ok := s.operationsCaller(w, r, operationsMayRead)
	if !ok {
		return
	}
	if !s.operationsLine(w, r, actor, actionOperationsListsRead, &access.Organisation.ID, nil, nil) {
		return
	}
	held, err := s.operationsLists(r.Context(), access.Organisation.ID)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, held)
}

// apiOpsSaveLists sets both lists, and it is the owner's alone (D2187 keeps
// D455's rule for the operations team's own door, D2207, F40).
func (s *Service) apiOpsSaveLists(w http.ResponseWriter, r *http.Request) {
	actor, access, ok := s.operationsCaller(w, r, operationsMaySetLists)
	if !ok {
		return
	}
	var asked WireOpsLists
	if !operationsBody(w, r, &asked) {
		return
	}
	written, err := s.setOperationsLists(r.Context(), actor, access.Organisation.ID, asked)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, written)
}

// apiOpsMint grants a term to any workspace, or queues the request when D454's
// rate for the row's tier is spent. The queued answer is the cap's own refusal
// (429) and its sentence says what happened to the request, which is what
// `admin.go` answers there and what the SPA shows.
func (s *Service) apiOpsMint(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	var asked WireEntitlementMint
	if !operationsBody(w, r, &asked) {
		return
	}
	granted, err := s.operationsMint(r.Context(), actor, asked)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, granted)
}

func (s *Service) apiOpsAct(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	var asked WireEntitlementAction
	if !operationsBody(w, r, &asked) {
		return
	}
	changed, err := s.operationsAct(r.Context(), actor, asked)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, changed)
}

func (s *Service) apiOpsTransfer(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	var asked WireProjectTransfer
	if !operationsBody(w, r, &asked) {
		return
	}
	moved, err := s.operationsTransfer(r.Context(), actor, asked)
	if err != nil {
		refuseOperations(w, err)
		return
	}
	answerAPI(w, http.StatusOK, moved)
}

// apiOpsCutHandover closes the window a key's reissue can still be fetched in.
// It carries no body and answers nothing (204): the request has nothing to say
// beyond which key it is about, and what a caller could read back is a fact
// about a secret (§7).
func (s *Service) apiOpsCutHandover(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	keyID := strings.TrimSpace(r.PathValue("id"))
	if keyID == "" || len(keyID) > maxOperationsLookupTerm {
		refuseOperations(w, errOperationsBadQuery)
		return
	}
	if err := s.operationsCutHandover(r.Context(), actor, keyID); err != nil {
		refuseOperations(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refuseOperations answers one failed operations request. Each refusal is the
// code the SPA branches on and a sentence it can show, and never a word about
// the service's inside (§7.2's rule, read at this area's door).
func refuseOperations(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotOperations):
		refuseAPI(w, http.StatusForbidden, "not_operations", "These screens are the operations team's, and this sign-in is not one of them.")
	case errors.Is(err, errOperationsReadOnly):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only the operations organisation's owners and admins may change anything here. Members read the screens.")
	case errors.Is(err, errOperationsListsAreTheOwners):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "The domain and directory lists are the operations owner's to set.")
	case errors.Is(err, errStaleOperationsSignIn):
		refuseAPI(w, http.StatusForbidden, "stale_sign_in", "This action needs a sign-in from the last five minutes. Sign in again, then send the change again.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "The operations organisation's members sign in with Google or Microsoft. Sign in that way to reach these screens.")
	case errors.Is(err, errNoSuchOrganisation):
		refuseAPI(w, http.StatusNotFound, "not_found", "No organisation in this portal has that id.")
	case errors.Is(err, errOperationsNoWorkspace):
		refuseAPI(w, http.StatusNotFound, "not_found", "No workspace in this portal has that id.")
	case errors.Is(err, errOperationsNoEntitlement):
		refuseAPI(w, http.StatusNotFound, "not_found", "No licence in this portal has that id.")
	case errors.Is(err, errOperationsNoEvent):
		refuseAPI(w, http.StatusNotFound, "not_found", "No stored event has that id.")
	case errors.Is(err, errOperationsNoHandover):
		refuseAPI(w, http.StatusNotFound, "no_handover", "No handover is open for that key: its window has closed, or it never had one.")
	case errors.Is(err, errOperationsProjectUnbound):
		refuseAPI(w, http.StatusConflict, "project_unbound", "That Firebase project is bound to no workspace, so there is nothing to move.")
	case errors.Is(err, errOperationsSameWorkspace):
		refuseAPI(w, http.StatusConflict, "already_bound", "That workspace already holds this project id.")
	case errors.Is(err, errOperationsDestinationBound):
		refuseAPI(w, http.StatusConflict, "workspace_bound", "That workspace is bound to another Firebase project. Move that binding first, or transfer this project id into a workspace that holds none.")
	case errors.Is(err, errOperationsEventAnswered):
		refuseAPI(w, http.StatusConflict, "already_answered", "That event has already been decided.")
	case errors.Is(err, errOperationsOverCap):
		refuseAPI(w, http.StatusTooManyRequests, "over_cap", refuseOverCap)
	case errors.Is(err, errProjectIDShape):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_project_id", "A Firebase project id is 6 to 30 characters of lower-case letters, digits and dashes, and begins with a letter.")
	case errors.Is(err, errOperationsBadRow):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_row", "That row is not one the policy table carries.")
	case errors.Is(err, errOperationsBadSeats):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_seats", "That seat count is not one this row may be granted with: a row that carries its own takes no other, and nobody can use fewer than one.")
	case errors.Is(err, errOperationsBadTerm):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_term", "That end is not inside the row's own term. A grant runs from the instant it is made, and an extension stays inside the term the row carries.")
	case errors.Is(err, errOperationsCheckInterval):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_check_interval", "A check interval is an ISO 8601 duration of 1 to 30 days, and it belongs to the row: the air-gapped row's key never checks the server, and no other row's may say that.")
	case errors.Is(err, errOperationsBadQuery):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_query", "This read names nothing to look up. Name a term, an organisation, a workspace or a month, and ask again.")
	case errors.Is(err, errOperationsBadState):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_state", "The only queue this API serves is the parked events, asked for as `state=parked`.")
	case errors.Is(err, errOperationsBadList):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_list", "That list is not one the schema takes: a domain is lower-case labels joined by dots, and a directory is a non-empty identifier.")
	case errors.Is(err, errOperationsUnknownAction):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_action", "An entitlement action is one of extend, seats, comp and revoke.")
	case errors.Is(err, errOperationsBadDecision):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_decision", "A parked event is either attached to a licence, which names one, or dismissed, which attaches nothing.")
	default:
		failAPI(w, err)
	}
}
