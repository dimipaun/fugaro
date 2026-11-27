package credsvc

// The pause: the action the credentials service decides when every source a
// run's chain can reach is exhausted, the record it writes when the run tells it
// it has saved, and the grant that lets the resident orchestrator resume it
// later (docs/model-credentials-routing/contract-pause.md, plan task PR3).
//
// THREE THINGS LIVE HERE AND THEY ARE ONE DECISION READ THREE TIMES.
//
// The DECISION (pauseAction) is the service's and never the job's (R19): the
// job follows the action it is given and decides nothing about time itself. The
// three settings it is made with are the project's own (PR9's
// projectpolicy.Pause): a reset nearer than wait_in_job is waited out inside the
// job as it always has been (D3184), a reset beyond max_pause — or one whose
// pause would take the chain's total past it, or an installation with no
// resident orchestrator to resume it (D3186, D3187) — is a halt, and everything
// between is a pause whose resume instant is never nearer than min_pause (D3190
// as amended).
//
// The RECORD is written when the run posts /pause, which is the run's own word
// that it has saved its session, pushed its checkpoint and written pause.json:
// the service then writes what it decided — the resume instant it computed, the
// launcher and the pool of the run's own slip (R8) — under creds/pauses/, and
// the node the orchestrator reads under runstore.PauseNodeRoot (R19, D3189).
// The record is what a resume days later is granted from, and it is why the
// resume survives the service scaling to zero: nothing here is held in memory
// across the two calls that matter.
//
// The GRANT (slip.go's resume arm) is the one the resident orchestrator asks
// for, and it is granted from that record and never from the ask: the launcher
// recorded there is the resumed run's launcher (D3191), so a run that continues
// a person's paused run spends that person's tokens and draws on that person's
// pool, whoever the orchestrator is.
//
// WHAT A REFUSAL COSTS. A refused /pause writes no node and no record, and the
// run that posted it must not end `paused`: it ends as a credential-starved run
// does, naming the refusal (PR2's pauseNotTaken). A pause nothing would resume
// is the one failure this part of the contract exists to prevent, so every
// refusal here is answered with the run's own class of code and recorded’s
// exactly one audit line.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// PurposePause is the run's own report that it has saved what a resume needs
// (runstore.PausePath, R12). Its line is the run's, like every other call a job
// of this installation makes, and the act it records issues no credential: the
// answer is the service's own record of a pause, which is why the one line a
// refusal of it writes carries no credential and no tier (checked).
const PurposePause = "pause"

// pauseRecords is the store of the service's own pause records, as this file
// reads it. It is the slip store's own database (creds.RTDBStore is one), asked
// as an interface so that a deployment wired without one refuses a pause rather
// than writing half of it: a pause whose record is nowhere is a pause nothing
// can resume.
type pauseRecords interface {
	WritePause(ctx context.Context, slug, run string, p creds.Pause) error
	Pause(ctx context.Context, slug, run string) (creds.Pause, bool, error)
	Resume(ctx context.Context, slug, run, by string, now time.Time) error
}

// pauseStore is the deployment's own record store, or nil for a service that
// has none. A store that is not one is nil rather than an error for the
// recorder's reason: the routes that read it answer a refusal naming their own
// state.
func (s *Server) pauseStore() pauseRecords {
	p, _ := s.cfg.Slips.(pauseRecords)
	return p
}

// orchestratorInstalled is the verifier's answer to whether this installation
// runs a resident orchestrator at all (D3186): an installation whose roster of
// orchestrator accounts is empty has none, and a project served by such an
// installation never pauses.
//
// It is an optional reading and not a required one because the Verifier
// interface is C1's own file: a verifier that cannot answer is read as one whose
// installation may have an orchestrator, and what decides there is the project's
// own switch (OrchestratorOn), which is the half a person writes. A deployment
// that answers, answers with the roster its own configuration holds.
type orchestratorInstalled interface{ OrchestratorInstalled() bool }

func (s *Server) hasOrchestrator() bool {
	if o, ok := s.cfg.Auth.(orchestratorInstalled); ok {
		return o.OrchestratorInstalled()
	}
	return true
}

// pauseSettings is the decision's whole input besides the reset instant and the
// chain (PR9's block).
type pauseSettings struct {
	waitInJob time.Duration
	minPause  time.Duration
	maxPause  time.Duration
	// resident is whether anything would resume a pause this service records:
	// an installation with no resident orchestrator suspends nothing (D3186).
	resident bool
}

// pauseSettingsOf reads the project's pause settings. An absent policy and an
// absent block are both the plan's defaults (projectpolicy's accessors answer
// them), and only a document that could not be READ is an error: the caller
// then answers the pre-pause behaviour rather than a guess at the operator's
// switch — a run held in its job for the reset it was told about is the
// behaviour of every release before this one.
func (s *Server) pauseSettingsOf(ctx context.Context) (pauseSettings, error) {
	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil {
		return pauseSettings{}, err
	}
	var p *projectpolicy.Pause
	resident := s.hasOrchestrator()
	if found && shared != nil {
		p = shared.Pause
		resident = resident && shared.OrchestratorOn()
	}
	return pauseSettings{
		waitInJob: p.WaitInJobDuration(),
		minPause:  p.MinPauseDuration(),
		maxPause:  p.MaxPauseDuration(),
		resident:  resident,
	}, nil
}

// pauseChain is what one run's pause is measured against: the chain it belongs
// to and how much of that chain's time has already been spent suspended.
//
// The credentials service cannot walk the runs bucket a chain lives in (it holds
// no reader of it), so it takes the chain forward the one way it can: a run
// whose slip names `resumes` — a run the orchestrator launched to continue a
// pause — inherits the chain and the paused time of the pause it continues. A
// run that continues none roots a chain of its own, which is its own run id
// (runstore.Chain's Root).
type pauseChain struct {
	root          string
	pausedSeconds int64
}

// chainOf reads what the chain of the calling run is: the record of the pause
// its own slip names, when it names one.
//
// An unreadable record is an error and not an empty chain: the difference is a
// chain whose paused time restarts with every follow-up, which is how a bound
// on a chain's total suspended time is evaded. The caller answers the pre-pause
// behaviour for it, as it does for an unreadable policy.
func (s *Server) chainOf(ctx context.Context, slug, run, resumes string) (pauseChain, error) {
	if resumes == "" {
		return pauseChain{root: run}, nil
	}
	store := s.pauseStore()
	if store == nil {
		return pauseChain{}, errors.New("this service holds no record of the pauses it decided")
	}
	prev, found, err := store.Pause(ctx, slug, resumes)
	if err != nil {
		return pauseChain{}, err
	}
	if !found {
		// The pause this run continues is not one this service wrote: the run
		// keeps a chain of its own rather than inheriting a stranger's bound.
		// It cannot be resumed for the same reason (no record, no grant), so
		// what this reads is a bound and not a permission.
		return pauseChain{root: run}, nil
	}
	return pauseChain{root: prev.Chain, pausedSeconds: prev.PausedSeconds}, nil
}

// pauseDecision is what the service decided about one exhausted answer: the
// action the run is told to take, and the instant a pause ends at.
type pauseDecision struct {
	action   runstore.PauseAction
	resumeAt time.Time
	chain    pauseChain
	// seconds is how long this pause lasts, which is the chain's paused time
	// this pause adds: the decision's own figure, and not recomputed later.
	seconds int64
}

// pauseAction decides between waiting, pausing and halting (D3184-D3190, R12,
// R19). reset is the instant the exhausted answer names — the earliest a
// credential of the chain's sources becomes usable again — and now is the
// service's clock.
//
// The order is the contract's and each step is a refusal to suspend:
//
//   - the reset is near, so the run waits for it inside its own job as it
//     always has (D3184). A wait needs nothing of this service, which is why it
//     is asked first and why an installation with no orchestrator still answers
//     it;
//   - the chain has no orchestrator to be resumed by, so a pause here would be
//     a chain nobody continues (D3186);
//   - the run's launcher is a service account, whose runs are never resumed: a
//     slip's owner is a launcher's verified numeric account id and a service
//     account has none (noGrantForTheOrchestrator), so a pause recorded for one
//     is a record the resume grant refuses — the stranding this part of the
//     contract exists to prevent, refused here rather than written there;
//   - the reset is further away than a chain may stay paused in total
//     (D3187);
//   - the pause this would record would take the chain's total past that bound
//     (D3190);
//   - otherwise it pauses, and no sooner than min_pause: a resume instant the
//     caller can compute is one a run may be resumed at, and one nearer than
//     the minimum would spin a chain against a state that has not moved (D3190
//     as amended).
func (s *Server) pauseAction(now, reset time.Time, d pauseChain, set pauseSettings, launcher routing.Principal) pauseDecision {
	away := reset.Sub(now)
	if away <= set.waitInJob {
		return pauseDecision{action: runstore.ActionWait, chain: d}
	}
	if launcher.Kind == routing.PrincipalServiceAccount {
		// The launcher the run's own slip records decides this, and not the
		// job's: a service account's run has no owner a resumed run's slip
		// could be written with, so a pause of it would be a chain nothing
		// resumes (grantResume refuses exactly this record).
		return pauseDecision{action: runstore.ActionHalt, chain: d}
	}
	if !set.resident || away > set.maxPause {
		return pauseDecision{action: runstore.ActionHalt, chain: d}
	}
	length := away
	if length < set.minPause {
		length = set.minPause
	}
	if time.Duration(d.pausedSeconds)*time.Second+length > set.maxPause {
		return pauseDecision{action: runstore.ActionHalt, chain: d}
	}
	return pauseDecision{
		action:   runstore.ActionPause,
		resumeAt: now.Add(length),
		chain:    d,
		seconds:  int64(length / time.Second),
	}
}

// pauseAsked is what the service last answered one run: the decision the run
// was told, and whether the run has since told the service it saved.
//
// It lives in memory, and it is the one half of this contract that does. The
// question it answers is a question about the CALL — did I last tell this run to
// pause, and has it already answered me — and a service that has restarted has
// no answer to it, which is the safe direction: a /pause it cannot place is
// refused, and a refused /pause ends the run as a credential-starved one rather
// than leaving a chain nothing records a resume for. What a resume reads is the
// record on disk, which does survive.
type pauseAsked struct {
	action   runstore.PauseAction
	resumeAt time.Time
	chain    pauseChain
	seconds  int64
	// taken is the once-per-answer mark: the run's own /pause has been honoured
	// for this answer, and a second one is refused.
	taken bool
}

// pauseBook is the service's record of the answers it has given, keyed by the
// repository and the run.
type pauseBook struct {
	mu sync.Mutex
	by map[string]pauseAsked
}

func pauseKey(slug, run string) string { return slug + "\x00" + run }

func (b *pauseBook) put(slug, run string, a pauseAsked) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.by == nil {
		b.by = map[string]pauseAsked{}
	}
	b.by[pauseKey(slug, run)] = a
}

// take is the rule "only if it last answered that run pause, and once per
// answer": it answers the decision the run was told when the answer was a pause
// this run has not yet answered, and false for every other state.
func (b *pauseBook) take(slug, run string) (pauseAsked, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.by[pauseKey(slug, run)]
	if !ok || a.action != runstore.ActionPause || a.taken {
		return pauseAsked{}, false
	}
	a.taken = true
	b.by[pauseKey(slug, run)] = a
	return a, true
}

// forget drops a run's answer, which is what the run's outcome does: a run that
// has ended leaves nothing for a /pause to be honoured from.
func (b *pauseBook) forget(slug, run string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.by, pauseKey(slug, run))
}

// answerExhausted decides what an exhausted answer carries and records the
// decision, which is the whole of what the service tells a run to do: the
// action, and — for a pause — the instant the run will be resumed at.
//
// It is asked for a staged call only (the answer's own rule, C3's
// PauseMemberAction): a gateway of a release behind decodes the refusal
// strictly, so a member it does not know would halt a run this release means to
// pause. A caller that sent no stage is answered exactly as it was before this
// contract existed, actionless, and its run waits in its job.
//
// launcher is the run's own launcher, out of the slip the call presents and
// never out of the job (R8): a run a service account launched is halted rather
// than paused, because nothing could resume it.
func (s *Server) answerExhausted(ctx context.Context, slug, run, resumes string, launcher routing.Principal, now, reset time.Time) (runstore.PauseAction, time.Time) {
	set, err := s.pauseSettingsOf(ctx)
	if err != nil {
		// The settings could not be read, which is this service's own state:
		// the run is told to wait, which is what every release before this one
		// told it, and nothing is recorded. A pause decided on a settings read
		// that failed would be a resume instant nobody could stand behind.
		s.log.Error("credsvc: the pause settings could not be read", "repository", slug, "error", err)
		return "", time.Time{}
	}
	d, err := s.chainOf(ctx, slug, run, resumes)
	if err != nil {
		s.log.Error("credsvc: the chain of a paused run could not be read", "repository", slug, "run", run, "error", err)
		return "", time.Time{}
	}
	dec := s.pauseAction(now, reset, d, set, launcher)
	if dec.action != runstore.ActionPause {
		// A wait and a halt are the run's answer and nothing of this service's:
		// nothing is recorded, because nothing would resume them.
		return dec.action, time.Time{}
	}
	s.pauses.put(slug, run, pauseAsked{
		action:   dec.action,
		resumeAt: dec.resumeAt,
		chain:    dec.chain,
		seconds:  dec.seconds,
	})
	return dec.action, dec.resumeAt
}

// pauseRoute is the run's own report that it has saved (runstore.PausePath,
// R12). It is a route of the run and not of a launcher: it goes through the
// shared parse, so the two identities have been verified, the repository has
// been fixed, the stop switches have been read and the run's slip has been
// verified — the slip is the whole of what the service records about the run's
// grant.
//
// The call carries no member: everything about the pause (the launcher, the
// pool, the resume instant) is the service's own record of the run's grant (R8).
func (s *Server) pauseRoute(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) {
	if r.ContentLength > 0 {
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusRequestRefused, ErrorRequestRefused,
			"this route takes no body: a pause is the run's own report that it has saved, and everything the service records about it is its own")
		return
	}
	b, ok := callSlipOf(ctx)
	if !ok || b.slip == nil {
		// A run with no slip has no grant for this service to record a pause
		// from, and the launcher, the pool and the resume instant are exactly
		// what it would have to invent (R8). A pause from one is refused, so
		// the run ends as a credential-starved one rather than leaving a chain
		// nothing could resume.
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusRequestRefused, ErrorRequestRefused,
			"this run holds no grant of this service's, and the launcher and the pool a pause is recorded with are read from the run's own slip")
		return
	}
	if b.slip.Slug != id.Slug || b.slip.Run != id.Run {
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"the grant this call presents is not the calling run's own")
		return
	}
	if !b.slip.Launcher.Valid() {
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusRequestRefused, ErrorRequestRefused,
			"the run's grant names no launcher, and a pause is recorded with the launcher whose pool a resume draws on")
		return
	}
	store := s.pauseStore()
	if store == nil || s.cfg.DB == nil {
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusUnavailable, ErrorStateUnavailable,
			"this service holds no record of the pauses it decided")
		return
	}
	asked, ok := s.pauses.take(id.Slug, id.Run)
	if !ok {
		// The two halves of the rule, and they are one refusal: a run this
		// service has not just answered `pause` is a run nothing decided a
		// resume instant for, and one that has already reported its pause has
		// nothing left to record (contract-pause, "the service honours it only
		// for a run it last answered pause, and once per answer").
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusRequestRefused, ErrorRequestRefused,
			"this service did not just answer that this run should pause, and a pause nobody decided is one nothing would resume")
		return
	}
	rec := creds.Pause{
		Launcher:      b.slip.Launcher,
		Pool:          b.slip.NamedPool,
		ResumeAt:      asked.resumeAt.UnixMilli(),
		Chain:         asked.chain.root,
		PausedSeconds: asked.chain.pausedSeconds + asked.seconds,
	}
	node := runstore.PauseNode{
		Launcher:      rec.Launcher.ID,
		Pool:          rec.Pool,
		ResumeAt:      rec.ResumeAt,
		Chain:         rec.Chain,
		PausedSeconds: runstore.Amount(rec.PausedSeconds),
	}
	raw, err := node.Bytes()
	if err != nil {
		s.log.Error("credsvc: the paused node of a run could not be built", "repository", id.Slug, "run", id.Run, "error", err)
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusUnavailable, ErrorStateUnavailable, "the pause could not be recorded")
		return
	}
	if err := store.WritePause(ctx, id.Slug, id.Run, rec); err != nil {
		// A record already written for this run is the once that matters most:
		// a second one would be a second resume instant for one pause.
		s.log.Error("credsvc: the pause of a run could not be recorded", "repository", id.Slug, "run", id.Run, "error", err)
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusRequestRefused, ErrorRequestRefused,
			"this run has already paused, and a pause is resumed once")
		return
	}
	if err := s.cfg.DB.Patch(ctx, "", map[string]any{runstore.PauseNodePath(id.Slug, id.Run): json.RawMessage(raw)}); err != nil {
		// The node is written LAST and it is the gate: the orchestrator resumes
		// nothing without one, and so does the resume grant (identity_orchestrated
		// reads it). A failure here therefore leaves a record nothing acts on,
		// which is the safe direction of the two.
		s.log.Error("credsvc: the paused node of a run could not be written", "repository", id.Slug, "run", id.Run, "error", err)
		s.refuseCall(ctx, w, id, PurposePause, "", "", StatusUnavailable, ErrorStateUnavailable, "the pause could not be recorded")
		return
	}
	s.log.Info("credsvc: a run reported that it had paused", "repository", id.Slug, "run", id.Run,
		"launcher", rec.Launcher.ID, "chain", rec.Chain, "paused_seconds", rec.PausedSeconds,
		"resume_at", asked.resumeAt.UTC().Format(time.RFC3339))
	w.WriteHeader(http.StatusOK)
}

// resumeRequest is the part of a grant's ask this file reads: a grant that
// names `resumes` continues the pause of the run it names, and one that names
// none is a launcher's ordinary grant for a new run.
func resumeValid(run string) bool {
	return run != "" && task.ValidRunID(run)
}

// grantResume grants the slip of a run that continues a pause (D3185, D3189,
// D3191), and it is the whole of what "only the orchestrator's class may resume"
// means at this route.
//
// EVERYTHING IS READ FROM THE SERVICE'S OWN RECORD (R8). The launcher the
// resumed run spends tokens as, the pool its launch named, the chain it belongs
// to and the instant it may start at are the record of the pause it names —
// never the ask, and never the paused run's own job, whose agent could name
// another person there and spend their tokens (F37). What the ask decides is
// which run this slip is for, which job account it will call as, and what it may
// be served — the launcher builds those from the paused run's task, which is the
// same task the paused run's own launch recorded.
//
// The record is the only authorization input: a pause this service did not write
// has no record, and a resume of one is refused rather than granted on the
// record the paused run's own container left in its bucket, which is a hostile
// agent's (R23, F52).
func (s *Server) grantResume(ctx context.Context, w http.ResponseWriter, caller grantCaller, req SlipGrantRequest) {
	if caller.Sub != "" {
		// A person's or a roster service account's grant is an ordinary
		// launch's and never a resume's: a resume is the resident orchestrator's
		// act (D3186, R13), and a launcher that names `resumes` is asking this
		// service to continue a chain on another person's tokens.
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"only the resident orchestrator's class may take a run's grant with `resumes`, and this caller's class is admitted the grant of a new run alone")
		return
	}
	if !resumeValid(req.Resumes) || req.Resumes == req.Run {
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"the grant names no run id it continues, and a resumed run continues the pause of another run")
		return
	}
	store := s.pauseStore()
	if store == nil {
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable,
			"this service holds no record of the pauses it decided")
		return
	}
	now := s.now()
	rec, found, err := store.Pause(ctx, req.Slug, req.Resumes)
	switch {
	case err != nil:
		s.log.Error("credsvc: the pause of a resumed run could not be read", "repository", req.Slug, "run", req.Resumes, "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the pause this grant continues could not be read")
		return
	case !found:
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"this service recorded no pause of that run, so there is nothing for this grant to continue")
		return
	case rec.Cancelled:
		// The launcher's own cancel (D3197): a pause that was cancelled is
		// never resumed.
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"the pause this grant continues was cancelled by its launcher, so nothing resumes it")
		return
	case rec.ResumedBy != "":
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"the pause this grant continues has already been resumed, and a pause is resumed once")
		return
	case now.UnixMilli() < rec.ResumeAt:
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"the pause this grant continues is not due yet, and the instant it is due at is this service's own")
		return
	case rec.Launcher.Kind == routing.PrincipalServiceAccount:
		// A slip's owner is the launcher's verified numeric account id, and a
		// service account has none (noGrantForTheOrchestrator). A resumed run's
		// slip would be one every call of it fails to audit, which is a run that
		// dies at its first model call: refused here rather than granted into a
		// record nothing can serve.
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
			"the pause this grant continues was taken by a service account's run, and a resumed run's grant records the launcher it serves as a launcher's own account id")
		return
	}
	// The run the resumed run spends tokens as is the paused run's launcher
	// (D3191), which is also the only owner a per-user credential of this grant
	// can belong to: a name of another account's is refused here exactly as it is
	// on an ordinary grant (R7).
	if err := checkGrantedNames(req.Credentials, rec.Launcher.ID); err != nil {
		s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// The pool the resumed run's credentials are decided from is the one the
	// PAUSED run's launcher resolves for this project, out of the pool its own
	// launch named (PL6, D3191): the orchestrator's designation decides nothing.
	pool, _, err := launcherPool().LauncherPool(ctx, rec.Launcher, rec.Pool)
	if err != nil {
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable,
			"the pool a resumed run draws on could not be resolved")
		return
	}
	// The mark of use is taken BEFORE the slip is written, so that two asks in
	// one instant cannot both be granted: the loser is refused by the record it
	// read again under the compare-and-set, and the winner's mark is what
	// carries the chain forward.
	if err := store.Resume(ctx, req.Slug, req.Resumes, req.Run, now); err != nil {
		s.log.Error("credsvc: the pause of a resumed run could not be marked resumed", "repository", req.Slug, "run", req.Resumes, "error", err)
		if pauseRefusalIsAsk(err) {
			s.refuseGrant(ctx, w, caller.Account, StatusRequestRefused, ErrorRequestRefused, requestRefused+
				"the pause this grant continues was taken by another ask in this instant")
			return
		}
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable,
			"the pause this grant continues could not be marked resumed")
		return
	}
	id, err := newSlipID()
	if err != nil {
		s.log.Error("credsvc: a slip id could not be drawn", "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	runtime := clampRuntime(req.MaxRuntimeSeconds)
	sl := creds.Slip{
		ID:              id,
		Owner:           rec.Launcher.ID,
		Launcher:        rec.Launcher,
		Slug:            req.Slug,
		Run:             req.Run,
		ExpectedAccount: req.Account,
		Allowed:         append([]string(nil), req.Credentials...),
		Pool:            pool.Pool,
		Resumes:         req.Resumes,
		SlipChain: creds.SlipChain{
			Sources:   append([]creds.GrantSource(nil), req.Sources...),
			NamedPool: rec.Pool,
		},
		MaxRuntime: runtime,
	}
	if err := s.cfg.Slips.Create(ctx, sl); err != nil {
		s.log.Error("credsvc: a resumed run's slip could not be written", "slip", id, "run", req.Run, "repository", req.Slug, "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "no slip could be written for this call")
		return
	}
	s.log.Info("credsvc: a resumed run was granted", "slip", id, "run", req.Run, "resumes", req.Resumes,
		"repository", req.Slug, "launcher", rec.Launcher.ID, "pool", pool.Pool, "named_pool", rec.Pool)
	body, err := json.Marshal(SlipGrantResponse{Slip: id, MaxRuntimeSeconds: int64(runtime.Seconds())})
	if err != nil {
		s.log.Error("credsvc: the grant's answer could not be built", "error", err)
		s.refuseGrant(ctx, w, caller.Account, StatusUnavailable, ErrorStateUnavailable, "the grant's answer could not be built")
		return
	}
	writeSlipAnswer(w, body)
}

// pauseRefusalIsAsk reports whether a refusal of the resume mark is the ASK's
// rather than this service's state, which is the difference between a launcher
// that asked twice and a store that could not be written. Each sentinel is the
// record's own answer about the ask or about the ask's caller.
func pauseRefusalIsAsk(err error) bool {
	return errors.Is(err, creds.ErrNoPause) || errors.Is(err, creds.ErrPauseResumed) ||
		errors.Is(err, creds.ErrPauseCancelled) || errors.Is(err, creds.ErrPauseEarly)
}
