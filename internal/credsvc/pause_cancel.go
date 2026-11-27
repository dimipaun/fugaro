package credsvc

// The cancel route (docs/model-credentials-routing, contract-pause
// "Cancelling"; plan task PR10).
//
// A pause that has not been cancelled is resumed by the resident orchestrator,
// and only the run's own launcher may end that (D3197): the service has no admin
// surface (D143), so it cannot tell a project admin from anyone else, and an
// admin does not cancel another person's pause in this plan. The route therefore
// verifies the caller's principal — the person or roster service account the
// launcher class already admits — against the launcher the SERVICE ITSELF
// recorded for that run's pause (R8), and marks the pause cancelled. A caller
// whose principal is another's is refused, and so is one that names a run this
// service recorded no pause of.
//
// THE MARK IS WRITTEN TWICE, in the order the two halves matter. The RECORD
// first (creds.Cancel), which is what a resume grant refuses on: the resume that
// races this cancel reads it and is refused rather than granted into a run the
// launcher has just ended. The NODE last, which is the gate the orchestrator's
// tick reads, and it is rebuilt from the record rather than read and patched —
// the node's members are the record's own (pause.go), so the two cannot disagree
// about the pause the mark is set on. A cancel is idempotent: a mark set twice
// is the mark, and a caller that lost the answer to its own call may ask again.
//
// The route is the LAUNCHER class's, like the slip grant and the designation: a
// launcher holds no run identity — the run it cancels is one it launched, and it
// presents its own Google ID token rather than a run's — so it is answered ahead
// of the shared parse and reads the token itself (launcherCaller).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// PurposePauseCancel is the launcher's own cancellation of its run's pause
// (D3197), over the route of this file. It is a purpose of its own and not the
// designation's or the grant's, for the reason the purpose set exists: an
// operator greps it, and a cancel is not a pool a launcher recorded nor a grant
// it asked for.
//
// Its line names the verified caller, the run and the repository the cancel is
// about, and NO job and NO credential: the act is a launcher's own statement
// about a run it launched, made with no run identity at all, and it touches no
// bundle entry — it sets a mark — so a credential's name on its line would be an
// entry this service never read. That is the shape checked's
// caller-without-job rule admits (callerWithoutJobPurpose) and the reason a
// successful cancel writes no audit line at all: the OK branch of that rule
// wants a credential, and this act has none, so what records it is the service's
// own log line (the /pause route's shape).
const PurposePauseCancel = "pause_cancel"

// PauseCancelPath is the route a paused run's launcher cancels its pause
// through: one POST whose body names the repository and the run, answered with
// no member.
const PauseCancelPath = "/v1/pause/cancel"

// PauseCancelRequest is the route's body: which pause is cancelled.
//
// There is deliberately no principal member — not even one the route would hold
// to the caller: the cancel is the launcher's own act about a run it launched,
// so the principal is the one the service verified and a body that names one is
// a request of a shape this route does not have, which the strict decode below
// refuses. That is the whole of "an admin does not cancel another person's
// pause": there is no field to write one in.
type PauseCancelRequest struct {
	Slug string `json:"slug"`
	Run  string `json:"run"`
}

// pauseCancels is the face of the record store this route writes through: the
// mark the launcher sets. It is asked as its own interface beside pauseRecords
// rather than added to it, so that a deployment whose store cannot write the
// mark refuses a cancel rather than writing the node alone — a node marked
// cancelled with no record behind it would leave a resume admissible.
type pauseCancels interface {
	Cancel(ctx context.Context, slug, run string) error
}

// pauseCancelStore is the deployment's own record store, as this route writes
// it, or nil for a service that has none.
func (s *Server) pauseCancelStore() pauseCancels {
	c, _ := s.cfg.Slips.(pauseCancels)
	return c
}

// pauseCancelRefusals, one sentence each, and none of them carries a value: the
// run and the repository are named by the answer's own fields and never repeated
// into a refusal, and no launcher's identity is ever named — a caller that is
// not the pause's launcher is told whose act this is and not who the launcher
// is, which is what keeps one person's address out of another's terminal (the
// package's no-echo rule).
const (
	pauseCancelNoRunIdentity = "this route takes no run identity: a cancel is the launcher's own act, and a caller presenting one is a call of a shape it does not have"
	pauseCancelNoPause       = "this service recorded no pause of that run, so there is nothing to cancel"
	pauseCancelNotTheirs     = "the pause of that run was recorded for another launcher, and only the launcher whose run paused may cancel its pause"
	pauseCancelResumed       = "the pause of that run has already been resumed, and cancelling it would stop nothing"
	pauseCancelNoStore       = "this service holds no record of the pauses it decided"
	pauseCancelUnwritten     = "the pause could not be marked cancelled"
)

// readPauseCancelRequest reads the route's body out of the call the handler
// already read (readCall), which is where the launcher's own token was
// registered with the service's redaction for the length of this call.
//
// The decode is strict, as every body of this contract is, and the two names are
// held to the shapes the paths built from them are safe against: a slug is one
// database segment and a run id is the grammar every surface spells a run in
// (task.ValidRunID), so what reaches the record lookup below is never a value
// that could name another node.
func readPauseCancelRequest(body []byte) (PauseCancelRequest, error) {
	if len(body) > maxMintRequestBytes {
		return PauseCancelRequest{}, errors.New("the body is larger than the bytes this service accepts")
	}
	var req PauseCancelRequest
	if err := decodeStrict(body, &req); err != nil {
		return PauseCancelRequest{}, errors.New("the body is not one JSON object of this route's fields: a cancel names the repository and the run whose pause it ends")
	}
	if req.Slug == "" || !token.ValidSegment(req.Slug) {
		return PauseCancelRequest{}, errors.New("the call names no repository: a pause is one run's, and the run is looked up in its repository")
	}
	if !task.ValidRunID(req.Run) {
		return PauseCancelRequest{}, errors.New("the call names no run id")
	}
	return req, nil
}

// pauseCancel is the route's handler: verify the launcher, read the body, check
// the pause is the caller's own, and mark it cancelled.
func (s *Server) pauseCancel(ctx context.Context, w http.ResponseWriter, r *http.Request) {
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
		s.refusePauseCancel(ctx, w, caller.Account, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+pauseCancelNoRunIdentity)
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refusePauseCancel(ctx, w, caller.Account, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readPauseCancelRequest(cl.body)
	if err != nil {
		s.refusePauseCancel(ctx, w, caller.Account, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// From here the repository and the run are known, so every refusal below
	// names them: a line an operator reads says which pause was refused.
	refuse := func(status int, typ, message string) {
		s.refusePauseCancel(ctx, w, caller.Account, req.Run, req.Slug, status, typ, message)
	}
	// The stop switches, read HERE because this route is answered ahead of the
	// shared parse that reads them for every other route. A stopped service
	// cancels nothing, the designation route's own rule: an operator who stopped
	// the service stopped what it decides, and ending a pause is a decision.
	on, which, err := s.cfg.Kills.Stop(ctx, req.Slug)
	if err != nil {
		refuse(StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		refuse(StatusUnavailable, ErrorStateUnavailable, "the credentials service is stopped by the "+which+" kill switch")
		return
	}
	records, cancels := s.pauseStore(), s.pauseCancelStore()
	if records == nil || cancels == nil || s.cfg.DB == nil {
		refuse(StatusUnavailable, ErrorStateUnavailable, pauseCancelNoStore)
		return
	}
	// The record is the whole of what the caller is verified against (R8): the
	// launcher the pause was recorded with is the one the service read out of the
	// run's own slip when the run paused, and never the run's word about itself.
	rec, found, err := records.Pause(ctx, req.Slug, req.Run)
	switch {
	case err != nil:
		s.log.Error("credsvc: the pause of a cancelled run could not be read", "repository", req.Slug, "run", req.Run, "error", err)
		refuse(StatusUnavailable, ErrorStateUnavailable, "the pause of that run could not be read")
		return
	case !found:
		refuse(StatusRequestRefused, ErrorRequestRefused, requestRefused+pauseCancelNoPause)
		return
	case rec.Launcher != caller.Principal:
		// The launcher's own act and nobody else's (D3197). An admin is refused
		// here with everyone else, and no name of the launcher's is written into
		// the answer: what the caller is told is whose act this is.
		refuse(StatusRequestRefused, ErrorRequestRefused, requestRefused+pauseCancelNotTheirs)
		return
	case rec.ResumedBy != "":
		// The chain is in flight again as the run that continued this pause, and
		// that run is a new one this pause's cancel cannot reach.
		refuse(StatusRequestRefused, ErrorRequestRefused, requestRefused+pauseCancelResumed)
		return
	}
	if err := cancels.Cancel(ctx, req.Slug, req.Run); err != nil {
		s.log.Error("credsvc: the pause of a cancelled run could not be marked", "repository", req.Slug, "run", req.Run, "error", err)
		// The three the record itself answers are the ask's and are named as
		// such; anything else is this service's state.
		switch {
		case errors.Is(err, creds.ErrNoPause):
			refuse(StatusRequestRefused, ErrorRequestRefused, requestRefused+pauseCancelNoPause)
		case errors.Is(err, creds.ErrPauseResumed):
			refuse(StatusRequestRefused, ErrorRequestRefused, requestRefused+pauseCancelResumed)
		default:
			refuse(StatusUnavailable, ErrorStateUnavailable, pauseCancelUnwritten)
		}
		return
	}
	// The node last, and it is the gate the orchestrator's tick reads: rebuilt
	// from the record the mark was just set on, so the node and the record cannot
	// disagree about the pause this mark ends. A failure here leaves a record no
	// resume is granted from and a node the orchestrator may still try — whose
	// ask the grant then refuses — which is the safe direction of the two.
	node := runstore.PauseNode{
		Launcher:      rec.Launcher.ID,
		Pool:          rec.Pool,
		ResumeAt:      rec.ResumeAt,
		Chain:         rec.Chain,
		PausedSeconds: runstore.Amount(rec.PausedSeconds),
		Cancelled:     true,
	}
	raw, err := node.Bytes()
	if err != nil {
		s.log.Error("credsvc: the paused node of a cancelled run could not be built", "repository", req.Slug, "run", req.Run, "error", err)
		refuse(StatusUnavailable, ErrorStateUnavailable, pauseCancelUnwritten)
		return
	}
	if err := s.cfg.DB.Patch(ctx, "", map[string]any{runstore.PauseNodePath(req.Slug, req.Run): json.RawMessage(raw)}); err != nil {
		s.log.Error("credsvc: the paused node of a cancelled run could not be written", "repository", req.Slug, "run", req.Run, "error", err)
		refuse(StatusUnavailable, ErrorStateUnavailable, pauseCancelUnwritten)
		return
	}
	s.log.Info("credsvc: a launcher cancelled the pause of its run", "repository", req.Slug, "run", req.Run, "launcher", rec.Launcher.ID)
	w.WriteHeader(http.StatusOK)
}

// refusePauseCancel writes the route's one line for a refusal: the verified
// caller, the pause's own run and repository when they are known, the purpose
// and the status — and no credential and no job, which is the shape this
// purpose's line has (PurposePauseCancel).
func (s *Server) refusePauseCancel(ctx context.Context, w http.ResponseWriter, caller, run, repository string, status int, typ, message string) {
	if err := s.audit.Write(ctx, AuditEvent{
		Caller: caller, Run: run, Repository: repository, Purpose: PurposePauseCancel, Status: typ,
	}); err != nil {
		// The service's own log line is where an operator reads a line it could
		// not record, for the reason refuseGrant gives: the answer to the caller
		// is settled either way.
		s.log.Error("credsvc: a refused pause cancel could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}
