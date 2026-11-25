package credsvc

// The designation route (R26, F61; plan task PL4).
//
// One principal's designation is written HERE and nowhere else that matters:
// the people who administer the project reach the database by IAM, which
// bypasses the rules, so no rule can hold who may write one (F61), and this
// route is the only writer that decides anything — it verifies the caller, keys
// the record on the subject it verified, and refuses a pool the caller does not
// own.
//
// It is the LAUNCHER class's route, like the slip grant: a launcher holds no
// run identity — the run the designation is for does not exist yet, and the
// designation outlives every run of it — so it is answered ahead of the shared
// parse and reads the token itself (launcherCaller). The record it writes is
// the one the lookup reads back (internal/creds/designation.go), and what it
// carries is display data and never a value.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/routing"
)

// PoolDesignatePath is the route a launcher records its own designation
// through: one POST whose body names the pool, answered with the record.
const PoolDesignatePath = "/v1/pool/designate"

// PoolDesignateRequest is the route's body: the pool's NAME and nothing else.
//
// There is deliberately no principal member — not even one this route would
// hold to the caller: a designation is a principal's statement about ITSELF
// (R26), so the principal is the subject the service verified and a body that
// names one is a request of a shape this route does not have, which the strict
// decode below refuses. That is the whole of "a designation in another
// principal's name is refused": there is no field to write it in.
type PoolDesignateRequest struct {
	Pool string `json:"pool"`
}

// readDesignateRequest reads the route's body out of the call the handler
// already read (readCall), which is where the launcher's own token was
// registered with the service's redaction for the length of this call.
//
// The decode is strict, as every body of this contract is, and the name is held
// to the shape a pool's name has (config.ValidCredentialRef, the same rule the
// publish applies): what reaches the refusal below is a name this contract
// already admitted, never an arbitrary string a caller sent.
func readDesignateRequest(body []byte) (PoolDesignateRequest, error) {
	if len(body) > maxMintRequestBytes {
		return PoolDesignateRequest{}, errors.New("the body is larger than the bytes this service accepts")
	}
	var req PoolDesignateRequest
	if err := decodeStrict(body, &req); err != nil {
		return PoolDesignateRequest{}, errors.New("the body is not one JSON object of this route's fields: a designation names the pool, and the principal is the one the service verified")
	}
	if msg := config.ValidCredentialRef(req.Pool); msg != "" {
		return PoolDesignateRequest{}, errors.New("the call names no pool: " + msg)
	}
	return req, nil
}

// designationRefusals, one sentence each, and none of them carries a value: the
// pool is named by the answer's own field and never repeated into a refusal,
// which is what keeps a caller's own text out of an operator's log (the
// package's no-echo rule).
const (
	designationNoRunIdentity  = "this route takes no run identity: a designation is the launcher's own act, and a caller presenting one is a call of a shape it does not have"
	designationNotMigrated    = "this project publishes no pools yet: `fugaro update` migrates them, and a designation written before it would resolve nothing"
	designationNoSuchPool     = "the project publishes no pool of that name"
	designationNotTheCallers  = "the pool belongs to another principal: a designation names a pool the caller owns, or one the project itself owns"
	designationStoreUnwritten = "the designation could not be written"
)

// poolDesignate is the route's handler: verify the launcher, read the body,
// check the entitlement, and write the caller's own designation.
func (s *Server) poolDesignate(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.launcherCaller(ctx, tok)
	if err != nil {
		// The class's own refusal, answered as the grant route answers one: a
		// typed refusal and no line, because nothing has been verified.
		s.refuseLauncher(w, err)
		return
	}
	if r.Header.Get(HeaderRunIdentity) != "" || r.Header.Get(HeaderRunJob) != "" {
		s.refuseDesignate(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+designationNoRunIdentity)
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseDesignate(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readDesignateRequest(cl.body)
	if err != nil {
		s.refuseDesignate(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// The stop switches, read HERE because this route is answered ahead of the
	// shared parse that reads them for every other route. A stopped service
	// writes no new record, the grant route's own rule: a designation is a
	// lasting statement and an operator who stopped the service stopped what it
	// decides, not only what it serves.
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		s.refuseDesignate(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseDesignate(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	shared, found, err := sharedPolicy(ctx, s)
	if err != nil {
		s.refuseDesignate(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the project's policy could not be read")
		return
	}
	if !found || !migrated(shared) {
		s.refuseDesignate(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+designationNotMigrated)
		return
	}
	project, ok := installationProject(s)
	if !ok {
		s.refuseDesignate(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the installation's own project could not be read")
		return
	}
	pool, ok := namedPool(req.Pool, shared)
	if !ok {
		s.refuseDesignate(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+designationNoSuchPool)
		return
	}
	if !poolServesPrincipal(pool, caller.Principal) {
		s.refuseDesignate(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+designationNotTheCallers)
		return
	}
	db, ok := s.cfg.DB.(creds.DB)
	if !ok {
		s.refuseDesignate(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, designationStoreUnwritten)
		return
	}
	d := routing.Designation{Principal: caller.Principal, Project: project, Pool: pool.Name}
	if err := creds.WriteDesignation(ctx, db, d); err != nil {
		// The refusal names the field and never the record: the write's own
		// error carries a node path and the decode's own words, and neither
		// belongs in an answer to a caller.
		s.log.Error("credsvc: a designation could not be written", "error", err)
		s.refuseDesignate(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, designationStoreUnwritten)
		return
	}
	s.auditDesignation(ctx, caller.Account, StatusOK)
	writeDesignationAnswer(w, d)
}

// auditDesignation writes the route's one line: the caller the service verified
// and the purpose, and no run, no job and no credential — the act is a
// launcher's own statement and touches no bundle entry (credentiallessPurpose).
func (s *Server) auditDesignation(ctx context.Context, caller, status string) {
	purpose, credential := routeAct(PoolDesignatePath)
	if err := s.audit.Write(ctx, AuditEvent{Caller: caller, Credential: credential, Purpose: purpose, Status: status}); err != nil {
		// The service's own log line is where an operator reads a line it could
		// not record, for the reason refuseGrant gives: the answer to the
		// caller is settled either way.
		s.log.Error("credsvc: a designation route's line could not be audited", "error", err)
	}
}

// refuseDesignate writes one refusal of this route, with the one audit line
// every refusal after verification owes (the package's rule). No sentence it is
// handed carries a credential or a caller's own text.
func (s *Server) refuseDesignate(ctx context.Context, w http.ResponseWriter, caller string, status int, typ, message string) {
	s.auditDesignation(ctx, caller, typ)
	s.refuse(w, status, typ, message)
}

// writeDesignationAnswer writes the record the call wrote, which is the whole
// of what the launcher asked for: the principal it was keyed on, the project
// and the pool. It is no-store — the answer is the caller's own state, and a
// cached one would tell the next caller what this one designated.
//
// A record that cannot be encoded is answered as the typed refusal instead of
// an empty body: three fields and an object, so the branch is unreachable, and
// the alternative to answering is a panic in a route that has already written.
func writeDesignationAnswer(w http.ResponseWriter, d routing.Designation) {
	body, err := json.Marshal(d)
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		WriteWireError(w, StatusUnavailable, WireError{Error: ErrorStateUnavailable, Message: designationStoreUnwritten})
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
