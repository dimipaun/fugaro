package runner

import (
	"context"
	"time"
)

// This file is C3's half of the pause contract for the runner: the seam a run
// suspends itself through, and the body that stands until PR2 fills it. C3
// ships the contract and no behaviour, so what is here decides nothing about the
// pause — it answers the way the runner has always answered an exhausted
// credential, and PR2 replaces the body with the suspend itself (save the
// session, push the checkpoint, write pause.json, tell the service, end paused
// and release the lease).
//
// It exists at all because the seam has to answer before its implementation
// arrives: PR2's runner reads the action the service named and calls this for
// pause, and the call has to be a real signature and a real body from the
// start. The same shape as internal/credsvc/provider_wire_stub.go, and for the
// same reason: a side written against a contract runs its own tests against a
// signature that exists, rather than against one invented beside it.

// Suspend is what a run does when the credentials service answers that every
// source of its chain is exhausted and the action is pause (R19, D3185):
// suspend the run so the service can resume it later as a new run in the same
// chain.
//
// stage is the stage that was running, and until is the instant the service
// said it would resume the run at. The instant is the service's own figure and
// is passed for the run's log line alone: pause.json carries no time, the node
// the service writes is the only record of it, and the job never chooses one
// (R8, D3189).
var Suspend func(ctx context.Context, r *run, stage string, until time.Time) error

// suspendInJob is today's answer to an exhausted credential: the run waits for
// the reset inside the job, on the session it had started (D138, F28). That is
// what the pause replaces, so it is exactly the body a run must not keep — and
// the reason PR2's TestSuspendFollowsTheAnswer/pause fails while this body
// stands: a pause answered by a wait is a container billing for the whole
// reset.
func suspendInJob(ctx context.Context, r *run, stage string, until time.Time) error {
	return r.waitForCredential(ctx, stage, until)
}

// The assignment stands only while nothing else has made one.
//
// The guard is what lets the real side land in a file of its own: PR2 assigns
// Suspend from suspend.go, which the toolchain presents before this file
// (suspend.go sorts before suspend_stub.go), so an unguarded init here would
// run last and put the waiting body back over the suspend. Asking whether the
// seam is still nil makes this file the answer that stands until a real one
// exists, whatever the other file is called. I2 deletes this file once the
// suspend is the only answer there is.
func init() {
	if Suspend == nil {
		Suspend = suspendInJob
	}
}
