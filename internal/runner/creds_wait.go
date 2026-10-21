package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/credsvc"
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
// waits for Until and resumes the stage's session.
type exhaustedError struct {
	// Until is the earliest reset the service found, in UTC.
	Until time.Time
}

func (e *exhaustedError) Error() string {
	return "no credential is usable before " + e.Until.UTC().Format(time.RFC3339)
}

// noteExhausted is where the credential service's typed answer arrives, and
// the only way a stage learns of it. C1.15's upstream calls it from the
// gateway's request path, where the service's answer to a forwarded call is
// read (ReadExhaustedUntil); a test drives it through
// RunHandle.NoteExhausted.
//
// It records nothing and writes nothing: it ends the stage in flight, which
// is the whole of the runner's reaction, and the answer only ever arrives
// while a call — and so a stage — is running, so there is no answer to keep
// for later. A halt recorded first wins, exactly as it does everywhere else:
// that path cancels the stage itself, and stage's own checks decide.
func (r *run) noteExhausted(e credsvc.ExhaustedUntil) {
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
	stop(&exhaustedError{Until: e.ResetAt.UTC()})
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
		h := runstore.Halt{Reason: runstore.HaltNoCredential, Scope: "run", At: r.d.Now().UTC(),
			Detail: fmt.Sprintf("no credential was usable before this job's own timeout: the credential service answered that the earliest reset is %s, and this job must be finished by %s (see 'fugaro accounts ls' for the accounts' states; the project policy's credentials.fallback decides whether another tier may be used)",
				until.UTC().Format(time.RFC3339), final.UTC().Format(time.RFC3339))}
		r.haltNow(h)
		return &HaltError{h}
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
	done := make(chan error, 1)
	go func() { done <- credsWait(ctx, d) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.haltCh():
		return &HaltError{*r.haltValue()}
	}
}
