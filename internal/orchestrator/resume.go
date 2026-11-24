package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is PR4 of docs/model-credentials-routing: the resident orchestrator
// resumes what is due. A run that suspends itself ends with the status `paused`
// and leaves the credentials service's own record of the pause — the node under
// runstore.PauseNodeRoot — and this is the side that continues it: on each tick,
// the pauses whose resume time has passed, that nothing has resumed and whose
// run really ended `paused`, are started once each as a run that continues the
// paused one (D3185).
//
// What is decided here and what is not is the contract's split (R8, R19): the
// resume TIME, the launcher and the pool are the service's own record and this
// side reads them and decides nothing about them — a run's agent could name
// another person's pool there and spend their tokens (F37) — while the three
// questions this file does answer are the ones a record cannot: is the run
// really paused, has something resumed it already, and is it due yet. The
// acceptance's fourth — that the issuer serves an identity for a resume only
// where the node is one the service wrote — is not here at all: it is the
// credentials service's own check (internal/credsvc/identity_orchestrated.go,
// D3189), because a resume the service did not recognise is a resume no run
// could be launched with.
//
// The node carries no "resumed" mark (contract-pause, "Resuming"): the run that
// resumed a pause is the run of the chain whose task names `resumes` with the
// paused run's id, and that is where PausedRun.ResumedBy comes from. A reader
// that answered it from anything else — a mark this side wrote back onto the
// node, say — would be a second writer of the service's record, which R19
// forbids.

// PausedRun is one pause as the resumer reads it: the pause the credentials
// service recorded for one run, beside the two things only the paused run's own
// project can answer.
type PausedRun struct {
	// Slug is the repository the paused run belongs to, as the node's path
	// spells it.
	Slug string
	// Run is the paused run's id: the run the node was written for, and the run
	// a resume's task names (runstore.PauseMemberResumes).
	Run string
	// Node is the node's own bytes, exactly as the service wrote them. It is
	// parsed and validated here, by runstore.ParsePauseNode — the reader C3
	// gives every side that is not the service, so that the orchestrator, the
	// cancel route and the views hold a node to the same rules — and a node this
	// release cannot read is refused rather than acted on (a pause spans a
	// fugaro update, F36: a member a newer release wrote is a member this one
	// must not guess at).
	Node []byte
	// Status is how the paused run's own record ended. The node is the
	// service's word about the pause; this is the run's, and the resume is
	// granted only where the two agree — a `/pause` posted by an agent whose
	// runner does not run on resumes nothing (the acceptance's own words, and
	// contract-pause's "Never a run that did not end paused").
	Status runstore.Status
	// ResumedBy is the id of the run that has already resumed this pause, empty
	// while nothing has. It is read from the chain — the run whose task names
	// `resumes` with the paused run's id — and never from the node, which is
	// the service's record of the pause and holds nothing about a resume that
	// had not happened when it was written (runstore.PauseMemberResumedBy's own
	// note).
	ResumedBy string
}

// PauseReader reads the pauses the credentials service recorded for this
// project: the nodes under runstore.PauseNodeRoot, each with the paused run's
// status and the run that has resumed it.
//
// It is an interface and not a store for the reason every other seam here is
// one: the database and the runs bucket are the CLI's, and what this package
// holds is the decision. What it may NOT do is read the pause out of the paused
// run's own bucket — the record the job wrote (pause.json) is a hostile agent's
// word about its own pause (R23), and a resume derived from it would widen the
// issuer by exactly the case D3189 does not (F52).
type PauseReader interface {
	Pauses(ctx context.Context) ([]PausedRun, error)
}

// ResumeRequest is one pause to continue: what the launcher is told about it,
// and the whole of what it is told. The node travels whole because it is the
// service's own record of the run's grant (its launcher, its pool, its chain),
// and a launcher that had to ask for the node again would be a second reader of
// it.
type ResumeRequest struct {
	// Slug is the repository the paused run belongs to, and the repository the
	// resumed run is launched in: a resume is never across repositories, and the
	// issuer refuses one that tried (the node's path is the repository's).
	Slug string
	// Run is the paused run's id: what the resumed run's task names as the run
	// it continues, and what the chain is continued from.
	Run string
	// Node is the pause the credentials service recorded.
	Node runstore.PauseNode
}

// ResumeLauncher starts the run that continues one paused run. It answers the
// id of the run it started, which nothing on the orchestrator's record names:
// the resume is a new run in the paused one's chain and not an attempt of any
// intent this side holds, so the id is the launcher's to mint and this side's
// only to check (D3189).
//
// It is the launcher's own path and not a plan action: a resume is not an item
// of the plan — the paused run's item is in flight and stays so (the chain is
// one piece of work) — and no allowance of a cap's is its business. A chain's
// remaining budget is the resumed run's, which is the same reason planNode does
// not cap a re-issue (D1045-D1047). The one thing in Caps that is every launch
// path's is the kill switch, and that one a resume asks like the rest
// (ResumePauses).
type ResumeLauncher interface {
	Resume(ctx context.Context, r ResumeRequest) (string, error)
}

// ResumeOutcome is one pass over the project's pauses.
type ResumeOutcome struct {
	// Due is how many pauses the pass found to resume: the nodes whose resume
	// time had passed, that nothing had resumed, whose run really ended
	// `paused`, and that this release can read.
	Due int
	// Resumed is how many of them a launcher started. It is what the report
	// counts as progress.
	Resumed int
	// Refused is what the pass would not resume and why: a node this release
	// cannot read, and a pause a launcher would not start. None of them fails
	// the pass — one pause that cannot be resumed must not hold the daemon's
	// other work back (the resident pass's own rule) — and every one of them is
	// still due at the next tick.
	Refused []error
}

// ResumePauses is the seam the tick resumes through, and the whole of what PR4
// adds to it. Its body is assigned by this file and stands only while nothing
// else has made one (the guard in resume_stub.go), so the stub that stands
// until the body exists is replaced by it without either file naming the other.
//
// caps and paused are the guard every path that starts a run asks before it
// starts anything (Caps.KillSwitch's own note): the kill switch — both switches
// a caller folded into it, ON where the reading failed — and the daemon's
// pause, the breaker's question standing on a node (BreakerOpen). A resume is a
// launch like the plan's, the ladder's and the decision agent's, so it asks them
// here rather than being written to afterwards: a daemon that could not read its
// budget, a project whose orchestrator switch is off, and a daemon a breaker has
// paused all resume nothing.
var ResumePauses func(ctx context.Context, pauses PauseReader, launcher ResumeLauncher, caps Caps, paused bool, now time.Time) (ResumeOutcome, error)

// resumeDue is one pass over the project's pauses: read them, decide which are
// due, and start each one once.
//
// The guard comes first, and before the read: a pass under the kill switch or
// under the daemon's pause starts nothing and reads nothing at all, exactly as
// the ladder and the decision agent do — a stopped daemon has no business on the
// project's database either. It is not a cap of the allowances: those bound what
// a cycle may spend, and a resume spends none of them (a chain's budget is the
// resumed run's, D1045-D1047), which is why planNode's re-issue is uncapped for
// the same reason.
//
// The read is the pass's own failure and nothing else is: a project whose
// pauses could not be read is a project whose daemon is working blind, and the
// cycle that called this fails rather than carrying on as though nothing were
// paused. A launcher that refuses one pause is not that — it is one entry in
// the outcome's refusals — because the next tick reads the same node again and
// the pause is still due.
func resumeDue(ctx context.Context, pauses PauseReader, launcher ResumeLauncher, caps Caps, paused bool, now time.Time) (ResumeOutcome, error) {
	if caps.KillSwitch || paused {
		return ResumeOutcome{}, nil
	}
	if pauses == nil || launcher == nil {
		// A daemon given no reader or no launcher resumes nothing, which is the
		// direction every unset seam here fails in: a resume it cannot make is
		// a resume it must not pretend to have made.
		return ResumeOutcome{}, nil
	}
	read, err := pauses.Pauses(ctx)
	if err != nil {
		return ResumeOutcome{}, err
	}
	due, refused := duePauses(read, now)
	out := ResumeOutcome{Due: len(due), Refused: refused}
	for _, p := range due {
		run, err := launcher.Resume(ctx, ResumeRequest{Slug: p.slug, Run: p.run, Node: p.node})
		if err != nil {
			out.Refused = append(out.Refused, err)
			continue
		}
		if !task.ValidRunID(run) {
			// A launcher that answered something that is no run id of this
			// installation's shape has started a run nothing can be read back
			// under: it is refused here and counted as nothing, so the next
			// tick resumes the pause again rather than trusting the answer.
			out.Refused = append(out.Refused, fmt.Errorf("the launch of the pause of %s answered %q as the resumed run's id, and that is no run id of this installation's shape", safetext.OneLine(p.run), safetext.OneLine(run)))
			continue
		}
		out.Resumed++
	}
	return out, nil
}

// duePause is one pause the pass decided to resume: the node it read, with the
// two names the launcher is given.
type duePause struct {
	slug string
	run  string
	node runstore.PauseNode
}

// duePauses is the decision, and it is the whole of what this side decides
// about a pause: which of the nodes the service recorded are the resumer's to
// resume now.
//
// A pause is due when its run's record ended `paused` — never a run that did
// not, because a `/pause` a run posted while its runner went on waiting is a
// run that is still working (the acceptance's own rule) — when the resume time
// the SERVICE computed has passed, when the node carries no cancellation
// (D3197: a cancelled pause is still readable as the pause it was, and it is
// never resumed), and when nothing has resumed it yet.
//
// The order of the checks is the order of what they cost: the two the reader
// answered beside the node first, then the node's own parse, then the two
// members the parse made readable.
func duePauses(paused []PausedRun, now time.Time) ([]duePause, []error) {
	var due []duePause
	var refused []error
	for _, p := range paused {
		if p.Slug == "" || p.Run == "" {
			refused = append(refused, fmt.Errorf("a pause was read with no repository or no run, and a pause nothing names is a pause nothing can be resumed from"))
			continue
		}
		if p.ResumedBy != "" {
			continue
		}
		if p.Status != runstore.StatusPaused {
			continue
		}
		node, err := runstore.ParsePauseNode(p.Node)
		if err != nil {
			refused = append(refused, fmt.Errorf("the pause of %s: %w", safetext.OneLine(p.Run), err))
			continue
		}
		if node.Cancelled {
			continue
		}
		if node.ResumeAt > now.UnixMilli() {
			// The node keeps its instant in epoch milliseconds UTC, which is
			// the unit the database keeps instants in (runstore.PauseNode), so
			// the comparison is the one figure against the one figure and never
			// a duration this side computed.
			continue
		}
		due = append(due, duePause{slug: p.Slug, run: p.Run, node: *node})
	}
	return due, refused
}

// The assignment stands only while nothing else has made one, exactly as the
// suspend's does: resume_stub.go is presented after this file (resume.go sorts
// before resume_stub.go), so an unguarded init there would run last and put the
// body that resumes nothing back over this one. I2 deletes the stub once this
// is the only answer there is.
func init() {
	if ResumePauses == nil {
		ResumePauses = resumeDue
	}
}
