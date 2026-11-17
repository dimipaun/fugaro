// What a resident pass carries out of the plan it reads (mobile plan task
// P5.1d; the design is docs/mobile/design/always-on-orchestrator.md, sections
// 4.1 and 4.2).
//
// WHAT THIS FILE IS, AND WHAT IT IS NOT. Section 4.2's second bullet is the
// launcher: "for a launch request, takes the launch claim, mints the budget
// token, and starts the job execution. That is `fugaro run`'s existing path
// with a service account behind it." That path is internal/cli's
// orchestratorLauncher — the ONE launcher this tree has, over `fugaro run`'s
// own steps (resolveAttemptPlan, buildAttemptSpec, createTask, launchRun) and
// its own gate block, shared with the operator's daemon precisely so a second
// launcher cannot appear (F11, D931, T2: a launch that skipped the budget
// pre-check or the gates is a hole the command does not have). So this file
// holds NO launcher and no step of one: what it holds is the SPLIT of one
// node's plan, which is the part the resident pass owns and the part that is
// worth reusing rather than writing again.
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
// action a plan derives (a first launch, a retry, an abandon) is a write this
// process does not have. ResidentRequests is that split, and a caller that
// ignored it would meet Starter.begin's own refusal rather than start a run
// nothing recorded ("a start it cannot record is one it must not make"),
// because a resident pass's starter carries no commit.
//
// THE VIEW IT IS HANDED IS THE RUNS BUCKET'S, AND A DEPLOYMENT MAY GIVE IT
// NONE. recovery (intent.go) reads one fact off the run records — whether the
// run an intent names ever started — and a pass whose RunView is empty reads
// every run id as one that never started. The direction a re-issue takes is the
// safe one: the SAME run id and never a second one, and the launcher's own
// behaviour is what makes that true rather than lucky — `fugaro run` reads the
// run's launch record before it starts anything (internal/cli's
// existingLaunch) and the runstore's launch.json is create-if-absent, so a run
// that already went is answered with the execution that record names. ClaimTTL
// is the second fence around the same question: nothing is re-issued inside the
// ten minutes a launch beside it may still be in flight.
//
// WHAT A CALLER HAS TO READ ITSELF, and the two things this file's own caller
// does: the CAPS, whose kill switch is read rather than assumed — the field's
// zero value reads as off, which is the permissive direction Caps' doc rules
// out ("a switch it could not read is on") — and the launcher, which is the one
// path above and never a launcher built here.

package orchestrator

import "time"

// ResidentRequests is one node's plan split the way a resident pass may act on
// it. requests are the launch requests the node's own record already carries:
// the re-issues of attempts whose run never started, which a starter holding a
// launcher and no commit carries out — the attempt was committed before this
// process woke, and starting it is a start and not a write. others are the
// actions a plan derived that ARE a write's: a first launch, a retry, an
// abandon. They are handed back rather than dropped, so that a caller can say
// how many steps it left and why, and they belong to whoever holds the node's
// credential (`fugaro orchestrator patch`, D1223/D1241).
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
