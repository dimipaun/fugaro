// What a resident pass carries out of the plan it reads (mobile plan task
// P5.1d; the design is docs/mobile/design/always-on-orchestrator.md, sections
// 4.1 and 4.2).
//
// WHAT THIS FILE IS, AND WHAT IT IS NOT. Section 4.2's second bullet is the
// launcher: "for a launch request, takes the launch claim, mints the budget
// token, and starts the job execution. That is `fugaro run`'s existing path
// with a service account behind it." That path is the command's own launch —
// launchRun's claim, mint and job execution — and internal/cli binds it to this
// package's Launcher interface TWICE OVER ONE PATH and not twice over two:
// orchestratorLauncher, the daemon's, which builds the spec and the task an
// item names and passes the command's whole gate block (freshLaunchGates), and
// P5.1d's residentLauncher, which starts the task an earlier launcher already
// stored and deliberately runs no gate block — its own header names the two
// halves a container has none of and what the skip can cost, and the steps the
// design names (the claim, the mint, the job start) are the command's in both.
// F11, D931 and T2's one path is what that sharing keeps; what it does not make
// true is that every launcher passes every gate, and a reader who needs the
// gate block has to ask which launcher they are reading. So this file
// holds NO launcher and no step of one: what it holds is the SPLIT of one
// node's plan (ResidentRequests) and the CARRYING of the half a resident pass
// may act on (ResidentCarry), which is the part the resident pass owns and the
// part that is worth reusing rather than writing again. The launcher itself is
// handed in — Starter.Launcher, the interface this package already carries
// actions through — and internal/cli is what fills it, with the command's own
// launch of the stored task the recorded attempt names.
//
// WHY A RESIDENT PASS CARRIES OUT RE-ISSUES AND NOTHING ELSE. A node's writes
// stay with `fugaro orchestrator patch` and the person's own credential (D1223,
// D1241): the service account reaches the database over IAM, whose one read
// permission is `firebasedatabase.instances.get` and whose write —
// `firebasedatabase.instances.update` — is deliberately not bound. So the
// intent is on the node before a pass is asked to start anything: a person's
// patch commits the attempt under the approval they gave it, and what is left
// for the resident service is the attempt that record already carries. That is
// ActionReissue exactly — a launch whose run id is recorded and whose run never
// started — which Starter.Start carries out without a commit, and every other
// action a plan derives (a first launch, a retry, an abandon, a ladder rung) is
// a write this process does not have. ResidentRequests is that split, and a
// caller that ignored it would meet Starter.begin's own refusal rather than
// start a run nothing recorded ("a start it cannot record is one it must not
// make"), because a resident pass's starter carries no commit and no mint.
//
// WHY THE CARRYING DOES NOT STOP AT THE FIRST REFUSAL. The daemon's cycle stops
// at one (cycle.carry: what a failure left is what the starter committed, and
// the next tick re-derives the rest), and it can, because a daemon writes: an
// attempt no run of it can start is eventually abandoned or the breaker pauses
// the node, and either way the node moves on. A resident pass has neither — no
// abandon, no breaker, no pause, all of them writes — so one attempt a launcher
// refuses for good (a record whose task the runs bucket does not hold, a
// repository this deployment cannot start) would hold every other attempt of
// the node back for as long as it stands, every tick, and the node would never
// make progress again. Every request is therefore carried on its own, a refusal
// is collected rather than fatal, and the caller reports it: the count of what
// started and one line per failure, which is a thing a person can act on. The
// fence is unchanged — each request is one AttemptStart, at most once, under
// the run id the node recorded — so a refusal neither starts a second run nor
// hides one.
//
// THE VIEW IT IS HANDED IS THE RUNS BUCKET'S, AND A DEPLOYMENT MAY GIVE IT
// NONE — WHICH IS A READING AND NOT A FACT. recovery (intent.go) reads one fact
// off the run records — whether the run an intent names ever started — and a
// pass whose RunView is empty reads every run id as one that never started, so
// every open attempt past ClaimTTL comes back as ActionReissue. That includes
// the run that is simply still going: a run longer than ten minutes is not
// distinguishable here from one that never went, which is why a caller must
// report these as attempts it cannot see started and never as launches owed
// (internal/cli's pass counts them under `unreported`, and a name is not a
// changeable thing once it is a wire field). Acting on one is safe for a
// different reason: the SAME run id and never a second one, and the launcher's
// own behaviour is what makes that true rather than lucky — the command reads
// the run's launch record before it starts anything and the runstore's
// launch.json is create-if-absent, so a run that already went is answered with
// the execution that record names (existingLaunch) rather than started again.
// ClaimTTL is the second fence around the same question: nothing is re-issued
// inside the ten minutes a launch beside it may still be in flight.
//
// WHAT A CALLER HAS TO READ ITSELF, and the two things this file's own caller
// does: the CAPS, whose kill switch is read rather than assumed — the field's
// zero value reads as off, which is the permissive direction Caps' doc rules
// out ("a switch it could not read is on") — and the launcher, which is the one
// path above and never a launcher built here.

package orchestrator

import (
	"context"
	"time"
)

// ResidentOutcome is what one resident pass came to: what it read of the node's
// record, what it carried out of that, and what it left.
//
// Read and Carried are both counted over the same list and neither is the
// other: Read is the requests the plan derived (every attempt past its claim
// window this pass cannot see a start for, which with an empty RunView includes
// a run that is still going — the header says why), and Carried is how many of
// them a launcher answered a start for. The difference is what a launcher
// refused, and Refused is the refusals themselves, in the plan's order and one
// per request that failed: a caller reports them (the count is not the whole of
// what a person needs, and a pass that swallowed them would look like a pass
// with nothing to do).
//
// Left is the actions the plan derived that ARE a write's: a first launch, a
// retry, an abandon, a ladder rung. They are not this pass's to carry out at
// all (the header), and they are counted so that a caller can say how many
// steps it left and why.
type ResidentOutcome struct {
	Read    int
	Carried int
	Left    int
	Refused []error
}

// ResidentRequests is one node's plan split the way a resident pass may act on
// it. requests are the plan's re-issues: attempts whose run the caller cannot
// see started, which a starter holding a launcher and no commit carries out —
// the attempt was committed before this process woke, and starting it is a start
// and not a write. With an empty RunView that includes a run that is still
// going (the header says why), so a caller that cannot read the runs bucket must
// report these under a name that says what it read — attempts not seen to start
// — and never as launches owed. others are the actions a plan derived that ARE a
// write's: a first launch, a retry, an abandon. They are handed back rather than
// dropped, so that a caller can say how many steps it left and why, and they
// belong to whoever holds the node's credential (`fugaro orchestrator patch`,
// D1223/D1241).
//
// It is Plan's own reading of the node and nothing beside it: the same items in
// the same order (an important follow-up ahead of new work), the same caps, the
// same breaker, and the same ClaimTTL window. A caller that hands the ZERO Caps
// — a pass that cannot read the installation's guardrails reads no cap at all,
// and the Tick's own rule is that the zero Caps plans nothing — gets no first
// launch and no retry, and its requests all the same: a re-issue starts the
// attempt that was already committed and counted, so it spends nothing a cap is
// there to bound (planNode's own reason). The kill switch is the exception and
// is NOT one of those zeros: planNode refuses every action under it, and the
// value a caller hands in is one it read (or `true`, for a switch it could not
// read).
//
// What no part of this reads is Queue: the items waiting for a person are not a
// launcher's work — nothing may start them (G6, F21) — and the list a message
// to a person is built from is the consumer half's (P5.1f). The caps and the
// breaker reach a plan through Plan, which is what makes this a split of the
// daemon's own reading rather than a second planning of the same node.
func ResidentRequests(s State, view RunView, caps Caps, now time.Time) (requests, others []Action) {
	for _, a := range Plan(s, view, caps, now) {
		if a.Kind == ActionReissue {
			requests = append(requests, a)
			continue
		}
		others = append(others, a)
	}
	return requests, others
}

// ResidentCarry carries out the requests one resident pass read, through the
// starter it is handed, and it is the whole of what a resident pass does besides
// reading.
//
// It is ResidentRequests plus Starter.Start and nothing else: the plan is read
// once, each re-issue of it goes to the starter in the order the plan decided,
// and what a starter answers is what this returns. Nothing here decides what to
// start — the ids, the specs and the fence between them are the plan's and the
// state's — and nothing here is a step of a launch: the claim, the mint and the
// job execution are the launcher's, the command's own path in internal/cli,
// handed in as Starter.Launcher.
//
// The starter a caller hands in is one with a Launcher and WITHOUT a commit or a
// mint, and that is what makes the split hold rather than what a comment asks
// for: ActionReissue reaches only Launcher (Starter.start), while every action a
// write's reaches Commit and is answered with the starter's own refusal — which
// is why only the requests are handed to it here. A starter with a commit would
// be a process that writes the node, which is not this one (D1223, D1241).
//
// now is the caller's reading of the STORE's clock, never a container's
// (D1230): it is what Plan judges the ten-minute claim window with, so a pass
// that passed its own clock would re-issue an attempt whose launch is still in
// flight.
//
// A refusal is collected and the carrying goes on (the header's paragraph): the
// one thing that must not happen is a pass that stops trying because one
// attempt of the node cannot be started.
func ResidentCarry(ctx context.Context, st Starter, s State, view RunView, caps Caps, now time.Time) ResidentOutcome {
	requests, others := ResidentRequests(s, view, caps, now)
	out := ResidentOutcome{Read: len(requests), Left: len(others)}
	for _, a := range requests {
		if _, err := st.Start(ctx, s, a, now); err != nil {
			out.Refused = append(out.Refused, err)
			continue
		}
		out.Carried++
	}
	return out
}

// ResidentResumes is the resident pass's other half, and the one action besides
// a re-issue it may carry out (PR4, D3185): the pauses the credentials service
// recorded that are due now, each started once as the run that continues it.
//
// It belongs to this file because it is the same question ResidentCarry answers
// — what of the project's owed work may a process with no write of the node act
// on — and the answer is the same shape of yes. A resume is not a write of the
// node: the record of a resume is the chain's own run, whose task names
// `resumes`, and the paused run's item is in flight and stays so; what the
// service holds (the node under runstore.PauseNodeRoot) was written by the
// service and never by this pass (R19). So the reason a resident pass carries
// re-issues and nothing else does not exclude a resume — a resume reaches no
// commit — and the pass that left it to `fugaro orchestrator patch` would leave
// a suspended chain suspended for as long as no daemon ran, which is exactly the
// always-on case this pass exists for.
//
// It reads nothing of the node, and there is nothing of the node for it to read:
// whether a pause is due is the service's own record and the paused run's own
// status (ResumePauses's decision), and a resident pass has the same two
// readings a daemon has.
//
// It is ResumePauses under the name this pass reads, and the seam is called here
// rather than by the caller for that reason: the body behind it is a build's own
// (resume.go, or the stub until I2 deletes it), and a caller outside this package
// that called the seam directly would be reading a variable instead of an
// interface. A refusal is collected and the pass goes on, exactly as
// ResidentCarry's is: the node has no breaker and no pause, so a pause a
// launcher refuses for good must not hold the others back.
func ResidentResumes(ctx context.Context, pauses PauseReader, launcher ResumeLauncher, now time.Time) (ResumeOutcome, error) {
	return ResumePauses(ctx, pauses, launcher, now)
}
