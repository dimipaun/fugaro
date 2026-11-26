package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// This file is C1.17's share of the credentials cascade (docs/credentials-
// service, D138): the runner's half of a wait. The service answers a typed
// `exhausted_until` while a stage runs (C1.10, C1.11); stage ends that stage
// — it did not fail — waits for the reset the answer names inside the job's
// own timeout, and runs the stage again on the session it had started, with
// --resume. The wait-or-switch decision itself, the threshold and the
// `oauth-only` policy that ignores it (D152) belong to the service, which is
// where the credentials' state is: what reaches this file is already that
// decision, and the runner never writes a mark of its own (D136).

// credsWait is how the wait sleeps: the run's context, and the delay the
// service named. It is a var so a test can replace it — a real wait is
// minutes or hours long, and no test may take that long (export_test.go's
// SetCredsWait).
var credsWait = func(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// exhaustedError ends the stage that was running when the credential service
// answered its typed `exhausted_until` (C1.10). It is not a failure: stage
// carries out the action the answer names, which is the wait for Until and
// the resume of the stage's session (D3184).
type exhaustedError struct {
	// Until is the earliest reset the service found, in UTC.
	Until time.Time
	// Action is the action the service decided for this run (C3, R19): one of
	// runstore's three, and empty from a service of a release behind, which
	// the run waits out as it always has.
	Action runstore.PauseAction
	// ResumeAt is the instant the service will resume the run at, carried
	// with a pause and zero for the other two actions. The job reads it and
	// never chooses it (R8).
	ResumeAt time.Time
}

func (e *exhaustedError) Error() string {
	return "no credential is usable before " + e.Until.UTC().Format(time.RFC3339)
}

// noteExhausted is where the credential service's typed answer arrives, and
// the only way a stage learns of it: it ends the stage in flight, which the
// caller's own loop then answers with the action beside it (haltOnExhaustedAnswer).
// C1.15's upstream calls it from the gateway's request path, where the
// service's answer to a forwarded call is read (ReadExhaustedUntil); a test
// drives it through RunHandle.NoteExhausted. Nothing in this release calls it
// outside tests: C1.15 wires the gateway's answer to it and C1.19 adds the
// end-to-end test, so until those land a wait can only be reached from a
// test's own call.
//
// It records nothing and writes nothing: it ends the stage in flight, which
// is the whole of the runner's reaction, and the answer only ever arrives
// while a call — and so a stage — is running, so there is no answer to keep
// for later. A halt recorded first wins, exactly as it does everywhere else:
// that path cancels the stage itself, and stage's own checks decide.
func (r *run) noteExhausted(e credsvc.ExhaustedUntil, action runstore.PauseAction, resumeAt time.Time) {
	if e.ResetAt.IsZero() {
		// ReadExhaustedUntil refuses an answer with no reset instant, so a
		// zero one is a caller's bug: it is not a reason to end a stage.
		r.d.Log.Warn("the credential service answered exhausted-until with no reset instant; ignoring it")
		return
	}
	r.mu.Lock()
	stop := r.haltStage
	r.mu.Unlock()
	if stop == nil {
		// No stage is in flight, so there is nothing to end: the answer is
		// dropped rather than kept, and the stage that meets the same
		// exhaustion next gets its own answer.
		r.d.Log.Warn("the credential service answered exhausted-until between stages; ignoring it")
		return
	}
	stop(&exhaustedError{Until: e.ResetAt.UTC(), Action: action, ResumeAt: resumeAt.UTC()})
}

// haltOnExhaustedAnswer carries out the action the exhausted answer named,
// and reports the halt that ends the run when the action is one: nil is the
// wait, which the caller takes next (D3184).
//
// The action is the credentials service's decision and never the runner's
// (R19): the service weighs the reset against pause.wait_in_job, its own
// resident orchestrator and the chain's spent pause time, and this is where
// the answer is acted on. The one rule the runner owns is R10, and it is about
// the two answers this run may not take: a hotfix never waits and never
// pauses, because time is the whole of what it has and neither a job spent
// idling nor a run of tomorrow is open to it. A halt it takes like any other
// run, which is the answer it would have had anyway.
//
// A pause is recognized and ends the run the way a halt does: nothing here
// suspends a run yet, and a run that waited for the reset the service meant to
// resume it at would spend the container the pause exists to release (D3184).
// The instant the pause carries is the service's own figure and is named in
// the halt's detail so its operator can see what was asked (R8).
//
// An empty action is a service of a release behind, which decides no action
// and is waited out as it always was, and an action outside the three cannot
// arrive: the gateway refuses one before it hands an answer over.
func (r *run) haltOnExhaustedAnswer(ex exhaustedError) error {
	until := ex.Until.UTC().Format(time.RFC3339)
	if ex.Action != runstore.ActionHalt && r.reachedRigor() == rigor.Hotfix {
		return r.haltNoCredential(fmt.Sprintf(
			"a hotfix run never waits for a credential and never pauses: the credential service answered that no credential is usable before %s", until))
	}
	switch ex.Action {
	case runstore.ActionHalt:
		return r.haltNoCredential(fmt.Sprintf(
			"the credential service answered that no credential is usable before %s and that this run should end rather than wait for it", until))
	case runstore.ActionPause:
		return r.haltNoCredential(fmt.Sprintf(
			"the credential service answered that this run should be suspended until %s; this release cannot suspend a run, so it ended instead (the earliest reset is %s)",
			ex.ResumeAt.UTC().Format(time.RFC3339), until))
	}
	return nil
}

// unreachableError ends the stage that was running when a call could not reach
// the credentials service at all (D2983). Like an exhausted answer it is not a
// failure: stage waits for Until and resumes the stage's session.
//
// What bounds it is not this file. An exhausted answer's wait is the runner's
// to bound (maxCredentialResumes) because the service's answer can name an
// instant that has already passed; an unreachable one cannot loop the same way,
// because the gateway answers a call that meets the spent bound by halting the
// run, naming the service: the wait below ends at Until, the resumed stage
// makes its call, and that call either reaches the service or ends the run
// (internal/gateway's serviceUnreachable, serviceUnreachableBound).
type unreachableError struct {
	// Until is when the wait ends: the bound, measured by the gateway from
	// the first call that could not reach the service, in UTC.
	Until time.Time
	// Service is which credentials service could not be reached, as the
	// gateway names it ("project's" or "workspace's"): the run's log line and
	// the halt's detail name the same one.
	Service string
}

func (e *unreachableError) Error() string {
	return "the " + e.Service + " could not be reached before " + e.Until.UTC().Format(time.RFC3339)
}

// noteUnreachable is where a call that could not reach the credentials service
// arrives (D2983), the way noteExhausted is where the service's typed answer
// arrives: C1.15's upstream hands the gateway's Unreachable answer to it, and a
// test drives it through RunHandle.NoteUnreachable. It ends the stage in
// flight, which is the whole of the runner's reaction; stage waits for the
// instant the answer names and resumes the stage's session, which is the retry.
//
// The two guards are noteExhausted's, for its reasons: a zero instant is a
// caller's bug rather than a reason to end a stage, and an answer that arrives
// with no stage in flight is dropped rather than kept — the stage that meets
// the same outage next gets its own answer, and the gateway's bound is measured
// from the outage and not from the answer.
func (r *run) noteUnreachable(u gateway.Unreachable) {
	if u.RetryAt.IsZero() {
		r.d.Log.Warn("a call could not reach the credentials service with no instant to wait for; ignoring it")
		return
	}
	r.mu.Lock()
	stop := r.haltStage
	r.mu.Unlock()
	if stop == nil {
		r.d.Log.Warn("a call could not reach the credentials service between stages; ignoring it")
		return
	}
	stop(&unreachableError{Until: u.RetryAt.UTC(), Service: u.Service})
}

// waitingUntil records on the run's registry entry the instant it expects to
// resume at, or clears it (a zero instant), for the dashboard's "waiting for
// a credential, expected resume T" view (C1.3, D152). Nothing here fails the
// run: the entry is a display, and a write the rules refuse is the session's
// own business (checkCascadeKeys, internal/budget).
//
// Only a run that is waiting ever calls this, and only a cascade run can
// wait: the typed answer that starts a wait comes from the credentials
// service (C1.10, C1.15), which is in the path of a cascade run alone. So
// the child is written for exactly the runs whose deployed rules carry it
// (C1.3), without a second test of agent.auth that could disagree with that.
// A run with no budget session — a local run — has no entry to write.
func (r *run) setWaitingUntil(at time.Time) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return
	}
	var ms int64
	if !at.IsZero() {
		ms = at.UnixMilli()
	}
	sess.Update(func(e *budget.AgentEntry) { e.WaitingUntil = ms })
}

// waitForCredential waits for the reset the credential service named and
// reports the error to end the run with: nil once the wait is over and the
// stage may be resumed, the run's own context error when the execution was
// cancelled or stopped, a HaltError when something halted the run while it
// waited, and a HaltError of its own when the wait does not fit in the job.
//
// No threshold is applied here. Which wait is too long to wait out, and
// whether a paid tier may be switched to instead — the wait-or-switch rule,
// and `fallback: oauth-only`, which ignores the threshold and waits for the
// earliest reset (spec §"Paid fallback policy", D152) — is the service's
// decision, taken where the credentials' state is. What the runner owns is
// the job's own clock: a wait that would end after the job must already be
// over resumes nothing, so it halts the run named (HaltNoCredential) instead
// of idling into a timeout whose message would blame the wrong limit.
func (r *run) waitForCredential(ctx context.Context, name string, until time.Time) error {
	if final := r.budget.finalizeAt(); !until.Before(final) {
		return r.haltNoCredential(fmt.Sprintf("no credential was usable before this job's own timeout: the credential service answered that the earliest reset is %s, and this job must be finished by %s (see 'fugaro accounts ls' for the accounts' states; the project policy's credentials.fallback decides whether another tier may be used)",
			until.UTC().Format(time.RFC3339), final.UTC().Format(time.RFC3339)))
	}
	// A halt recorded between the stage's own check and here (a kill switch,
	// a cap, the backend's grace, all of them other goroutines) ends the wait
	// before it begins: the sleep below is hours long, and running the stage
	// once more only to have its own check halt it is a stage the run did not
	// need. A halt recorded after this is caught by haltCh below.
	if h := r.haltValue(); h != nil {
		return &HaltError{*h}
	}
	d := until.Sub(r.d.Now())
	r.d.Log.Info("waiting for a credential", "stage", name, "resume_at", until.UTC().Format(time.RFC3339), "wait", d.String())
	r.setWaitingUntil(until)
	defer r.setWaitingUntil(time.Time{})
	// The sleep is the wait; the runner watches beside it, because neither
	// the sleep nor the run's context hears a halt: a kill switch, a cap or
	// the backend's grace recorded while the run idles must end the wait at
	// once, not when the reset arrives.
	done, stopSleep := credsSleep(ctx, d)
	defer stopSleep()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.haltCh():
		return &HaltError{*r.haltValue()}
	}
}

// waitForService waits out the instant an unreachable answer named and reports
// the error to end the run with, exactly as waitForCredential does for a reset:
// nil once the wait is over and the stage may be resumed, the run's own context
// error when the execution was cancelled or stopped, and a HaltError when
// something halted the run while it waited.
//
// Nothing is written to the run's registry entry for it (setWaitingUntil):
// nothing here is waiting for a credential, and the dashboard's "waiting for a
// credential, expected resume T" would say the run was short of one when the
// service it waits for is a service that is down.
//
// The one halt of its own is the job's clock, the same one waitForCredential
// reads: a wait that would end after this job must already be over resumes
// nothing, and idling into the job's own end would spend the timeout that the
// bound exists to be shorter than. It is the halt a service that could not be
// reached gets, the same reason and the same words the gateway's own halt uses
// (ReasonBudgetUnavailable, stateDetailUnreachable): the run names the service
// either way.
func (r *run) waitForService(ctx context.Context, name, service string, until time.Time) error {
	if final := r.budget.finalizeAt(); !until.Before(final) {
		h := runstore.Halt{Reason: runstore.HaltBudgetUnavailable, Scope: "run", At: r.d.Now().UTC(),
			Detail: fmt.Sprintf("the %s could not be reached: this run waits for it until %s, which is after this job's own timeout %s, so nothing would be resumed inside the job (check the service in that project: its revision, its invoker grants and its own log)",
				service, until.UTC().Format(time.RFC3339), final.UTC().Format(time.RFC3339))}
		r.haltNow(h)
		return &HaltError{h}
	}
	if h := r.haltValue(); h != nil {
		return &HaltError{*h}
	}
	d := until.Sub(r.d.Now())
	r.d.Log.Info("waiting for the credentials service", "stage", name, "service", service,
		"resume_at", until.UTC().Format(time.RFC3339), "wait", d.String())
	done, stopSleep := credsSleep(ctx, d)
	defer stopSleep()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.haltCh():
		return &HaltError{*r.haltValue()}
	}
}

// credsSleep starts the wait's sleep and returns its result and the function
// that ends it. The sleep runs on a context of its own, derived from ctx, so
// that it ends with the wait whichever way waitForCredential's select
// returns: what it waits for is the reset the service named, minutes or hours
// away, and a sleep left running behind a wait that ended on a halt would be
// a goroutine and a timer left behind for the rest of the job.
func credsSleep(ctx context.Context, d time.Duration) (<-chan error, func()) {
	sleepCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- credsWait(sleepCtx, d) }()
	return done, stop
}
