package runner

// The pause: what a run does when the credentials service answers that every
// source its chain can reach is exhausted and that it should be suspended
// rather than wait (R12, R18, R19, D3185). The run saves what a resume needs,
// tells the service it has saved, ends `paused` and lets its container stop —
// the lease goes with it, and the service resumes the chain later as a new run
// (docs/model-credentials-routing/contract-pause.md).
//
// What a resume reads is two objects of this run's own prefix and nothing else
// (D3185, F34): session/session.json + session/<id>.jsonl, the work session
// restored into the new job's HOME, and pause.json, which names that session
// and the commit the run pushed. The two have to agree with the checkout the
// resumed run is cut at, or that run refuses the record rather than starting
// the work afresh (prepareResume, R18) — so every check below is about one of
// the three, and a pause that cannot leave all three consistent is not taken.
//
// THE ORDER IS THE CONTRACT'S, WITH ONE DEVIATION. The contract says save the
// session, push the checkpoint, write pause.json, ask (contract-pause.md
// §"/pause"). The push comes FIRST here, because session/session.json records
// whether its head was pushed (SessionMeta.Pushed, from rec.PushedHead) and a
// resume refuses a session whose head was not: a session saved before the push
// is a session no resume would restore. So the load-bearing half of the rule
// still holds — nothing is asked until the session is saved, the checkpoint is
// pushed and pause.json is written — and only the first two swap.
//
// Nothing here ends `paused` on an ask that did not succeed: a refused /pause
// writes no node, and a pause nothing would resume is the one failure this
// part of the contract exists to prevent. Every way the pause is not taken
// ends the run the way a run whose credential ran out ends (HaltNoCredential),
// naming what stopped it — which is where a record left half-written costs a
// resumed run nothing: it is read by a run that is launched, and one that is
// never launched reads nothing.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// errSuspended ends the stage that was running when the run suspended itself.
// It is not a failure and not a halt: the run has saved what a resume needs and
// the service has been told so, and what follows is the run's own end — the
// record already says `paused` and no pull request is touched (r.skipPR).
var errSuspended = errors.New("the run suspended itself")

// pauseAskBudget bounds the one call the pause makes, the way
// workflowSecretsBudget bounds the fetch: the run is already saved and its
// work is pushed, so a service that has not answered in this long leaves the
// run ending as a credential-starved one rather than holding the container
// open. It is small because the call carries nothing and the service has
// nothing to compute: it reads its own record of this run and writes a node.
const pauseAskBudget = 30 * time.Second

// pauseLeftover is the commit a pause makes of what the agent left in the tree,
// beside finalize's own ("fugaro: uncommitted work at finalize"): what a resume
// checks out is a commit and the session, and work neither of them carries is
// work the pause would silently lose.
const pauseLeftover = "fugaro: uncommitted work at pause"

// init answers the seam C3 left for this file (suspend_stub.go). The
// assignment is here and not left to the stub's guarded one, which runs after
// this file and only while the seam is still nil — so the stub is the answer
// until this file exists, and this file is the answer once it does. I2 deletes
// the stub, and the export with it.
func init() { Suspend = suspendRun }

// suspendRun is Suspend's implementation: the pause itself (R12, R18).
//
// stage is the stage that was running, session the session it ran in, and ex
// the answer the service sent — whose ResumeAt is the instant the run will be
// resumed at, the service's own figure, which this run reads into its log line
// and its record's reason and never chooses (R8).
func suspendRun(ctx context.Context, r *run, stage, session string, ex exhaustedError) error {
	work, why := r.pauseSession(stage, session)
	if why != "" {
		return r.pauseNotTaken(ex, why)
	}
	sha, why := r.pauseCheckpoint(ctx)
	if why != "" {
		return r.pauseNotTaken(ex, why)
	}
	if why := r.pauseSave(ctx, work, sha); why != "" {
		return r.pauseNotTaken(ex, why)
	}
	if err := r.d.Store.WritePause(ctx, runstore.PauseRecord{
		Version: runstore.PauseVersion, Stage: stage, Session: work, Checkpoint: sha,
	}); err != nil {
		return r.pauseNotTaken(ex, fmt.Sprintf("the run could not write %s: %s", runstore.PauseFile, r.redact(err.Error())))
	}
	if why := r.pauseAsk(ctx); why != "" {
		return r.pauseNotTaken(ex, why)
	}
	r.recordEnd(runstore.StatusPaused, runstore.OutcomeNone, "", fmt.Sprintf(
		"the credential service answered that this run should be suspended until %s; the run saved its session and pushed its work at %s, and the service resumes it then",
		ex.ResumeAt.UTC().Format(time.RFC3339), shortSHA(sha)))
	r.skipPR = true
	r.save(ctx)
	r.d.Log.Info("the run suspended itself", "stage", stage, "session", work, "checkpoint", shortSHA(sha),
		"resume_at", ex.ResumeAt.UTC().Format(time.RFC3339))
	// The ending's own writes, as every ending that does not reach finalize
	// makes them (gate.go's endWithoutPR). finalize still runs after this and
	// does nothing more: r.skipPR is what it reads.
	r.storeEnded(ctx, nil)
	return errSuspended
}

// pauseSession is the session a pause saves and names in pause.json, or the
// reason there is none.
//
// It is the run's WORK session and never the stage's own: a review runs in a
// session of its own (agentLoop hands each one a fresh ID), and what a resume
// restores from session/session.json is the conversation that built the branch
// — the reviewer's is saved beside it and is not what a follow-up or a resume
// continues (D138). A resumed run that enters a review stage starts that
// review's session afresh, so its own saved session is only checked and never
// run (prepareResume).
func (r *run) pauseSession(stage, session string) (string, string) {
	if _, err := resumeStage(stage); err != nil {
		// The pause records the stage a resumed run must enter, so a stage this
		// image cannot enter is one whose pause no run could pick up (R18's
		// reading of the record, asked by the writer): the run ends as a
		// credential-starved one rather than leave a record nothing acts on.
		return "", fmt.Sprintf("stage %s is not one a resumed run can enter, so a pause in it would leave a record nothing could pick up", stage)
	}
	if r.pauseNoBranch() {
		return "", "this run has no branch of its own to push, so a pause would leave no work in progress for a resume"
	}
	work := session
	if config.IsReviewStage(stage) {
		r.mu.Lock()
		work = r.sessionID
		r.mu.Unlock()
	}
	if !agent.ValidSessionID(work) {
		return "", "the run has no work session to save, and a resumed run restores the work session alone"
	}
	return work, ""
}

// pauseNoBranch is the runs a pause cannot push for: the modes whose branch is
// not the run's to advance, and a run with no checkout at all. It is
// checkpointsOn's own list, less its two conditions that are not this fact (a
// follow-up and a fugaro.yaml switch do not decide whether the branch may be
// pushed): those modes push no branch of their own — a report run's branch must
// hold the commit its own first step made and nothing else, a challenger arm
// pushes nothing at all — so a checkpoint of one would publish a state of the
// branch no run of the recipe meant to (checkpoint.go, digest.go T3.3,
// reports_commit.go R9, shadow.go A2.1).
func (r *run) pauseNoBranch() bool {
	return r.repo == nil || r.artifact || r.digest || r.commitReports || r.shadow ||
		r.review || r.evaluate || r.rebase || r.replay != ""
}

// pauseCheckpoint pushes the work in progress and reports the commit a resume
// checks out, or the reason there is none.
//
// What the agent left uncommitted is committed first, holding back the build
// artifacts finalize refuses (stray.go): the range a checkpoint's own secret
// scan and workflow guard read starts at the last pushed commit, and the
// push carries exactly what the branch will have. The scan is the poll's own
// (checkpointClean), for the poll's reason — a checkpoint is a push to the
// remote like any other — and it refuses on a hit AND on a scan that failed,
// so its sentence says only that the scan did not clear the commits.
func (r *run) pauseCheckpoint(ctx context.Context) (string, string) {
	strays, _, err := r.strayArtifacts(ctx)
	if err != nil {
		return "", fmt.Sprintf("the run could not list what it left untracked: %s", r.redact(err.Error()))
	}
	if _, err := r.repo.CommitAllExcept(ctx, pauseLeftover, strayPaths(strays)); err != nil {
		return "", fmt.Sprintf("the run could not commit the work it had left uncommitted: %s", r.redact(err.Error()))
	}
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		return "", fmt.Sprintf("the run could not read the commit to push: %s", r.redact(err.Error()))
	}
	if !r.checkpointClean(ctx, sha) {
		return "", "the commits this pause would push did not clear the run's own scan for the values it redacts"
	}
	if err := r.pushCheckpoint(ctx, sha); err != nil {
		return "", fmt.Sprintf("the run could not push its work in progress: %s", r.redact(err.Error()))
	}
	return sha, ""
}

// pauseSave saves the work session and reads back what a resume would restore,
// which is the reader's own reading of the two objects (runstore.ReadSession)
// and not a second one: saving is best effort everywhere else in the run — a
// session that cannot be saved costs a follow-up its continuation — but a pause
// whose session is not there is a run that comes back to nothing, so here it
// refuses the pause.
func (r *run) pauseSave(ctx context.Context, work, sha string) string {
	r.saveOneSession(ctx, work, false)
	m, data, err := r.d.Store.ReadSession(ctx)
	switch {
	case err != nil:
		return fmt.Sprintf("the run could not read back the session it saved: %s", r.redact(err.Error()))
	case m.ID != work || !m.Pushed || !gitprov.SameCommit(m.HeadSHA, sha) || int64(len(data)) != m.Bytes:
		return "the session the run saved is not the one a resume would restore from the commit it pushed"
	}
	return ""
}

// pauseAsk tells the credentials service the run has saved (runstore.PausePath,
// R12). It is the call the service answers by writing the paused node — the
// launcher, the pool and the resume instant are its own record of this run's
// grant and nothing here chooses them (R8, R19) — and its answer is what
// decides whether the run really ends `paused` or ends as a credential-starved
// one (nil, else the reason).
//
// The call is signed exactly as the model path and the workflow-secret fetch
// are (r.serviceIdentities): the job's own ID token as the bearer, the run's
// own identity and the job's name, and no credential of the job's own.
func (r *run) pauseAsk(ctx context.Context) string {
	url := envLookup(r.d.Env, credentialsServiceURLEnv)
	if r.d.Local || url == "" {
		// A local run holds no installation identity, reaches no private
		// service and is never resumed by one (D149): the same gate the
		// workflow-secret fetch reads, for the same reason.
		return "this run's job carries no credentials service URL, so there is nothing to tell that it has saved — and nothing that would resume it"
	}
	job, err := r.jobName()
	if err != nil {
		return fmt.Sprintf("the run could not report the job it is in: %s", r.redact(err.Error()))
	}
	if job == "" {
		return "this run has no Cloud Run job name to report, and a call that cannot report the job it is in is not one the credentials service reads"
	}
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return "this run has no run identity, which is what the credentials service verifies a call by"
	}
	svc := r.serviceIdentities(url, job, sess.IDToken())
	cctx, cancel := context.WithTimeout(ctx, pauseAskBudget)
	defer cancel()
	status, answer, err := svc.Post(cctx, url, runstore.PausePath, nil)
	if err != nil {
		return fmt.Sprintf("the run could not tell the credentials service it had saved: %s", r.redact(err.Error()))
	}
	if status != http.StatusOK {
		// The refusal means the service wrote no node, so nothing will resume
		// this run: ending `paused` here would leave a pause that no run
		// continues (contract-pause.md §"/pause"). Only the code is read and
		// the message beside it dropped — it is the service's text, written for
		// an operator's log (refusedWorkflowSecret's own reading).
		s := fmt.Sprintf("the credentials service answered %d to the run's own report that it had saved, so nothing would resume it", status)
		if code := pauseRefusal(status, answer); code != "" {
			s += ", refused: " + code
		}
		return s
	}
	return ""
}

// pauseRefusal is the contract's own code for a refused /pause, or "" when the
// body carries none this contract could have written.
func pauseRefusal(status int, answer []byte) string {
	e, err := credsvc.ReadWireError(status, answer)
	if err != nil || !reviewServiceErrorCode.MatchString(e.Error) {
		return ""
	}
	return e.Error
}

// pauseNotTaken ends the run the way a run whose credential ran out ends,
// naming why the pause was not taken: the action the service decided is one
// this run could not carry out honestly, and a run that ended `paused` without
// a record a resume can act on — or without the service knowing — would be a
// chain nobody continues.
//
// The words are the halt's and not a failure's: no stage failed, and the reset
// the service named is still what would have to pass for the run to go on
// (HaltNoCredential, halt.go).
func (r *run) pauseNotTaken(ex exhaustedError, why string) error {
	return r.haltNoCredential(fmt.Sprintf(
		"the credential service answered that this run should be suspended until %s, and it was not suspended: %s (the earliest reset is %s)",
		ex.ResumeAt.UTC().Format(time.RFC3339), why, ex.Until.UTC().Format(time.RFC3339)))
}
