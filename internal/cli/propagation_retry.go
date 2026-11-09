package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// This file is the retry a step gets when terraform's apply failed only on
// resources Google had not caught up with (tf.PropagationFailure, the closed
// list): the failure of 2026-11-09, where `fugaro update` created the
// credentials service and the two grants on it were refused by an IAM front
// end that had not seen the service yet. The fix a person would apply is
// running the command again, and CLAUDE.md's HARD RULE says that re-run is not
// theirs to make.
//
// What is retried is the WHOLE cycle the step runs -- plan, the managed gate
// (or the guard and the confirmation), apply -- and never the saved plan file
// of the failed attempt: the point of the wait is that the world has changed,
// so what is left to do is planned afresh and judged by the same gate, the
// same guard and the same cover tables the first plan went through. A retry
// therefore skips nothing; it is the step, run again.

// propagationWaits are the waits before each retry: about three and a half
// minutes in all before the last one. A variable so a test can make them
// instant -- the durations are what a person reads, and a test cannot afford
// to wait for them.
var propagationWaits = []time.Duration{20 * time.Second, 60 * time.Second, 120 * time.Second}

// propagationSleep is the wait itself, and a variable for the same reason: the
// interrupted wait is tested against sleepFor, which is what runs in a real
// one.
var propagationSleep = sleepFor

// sleepFor waits for d, and stops as soon as ctx is done: ctrl-c ends a
// retry's wait where it is rather than after the backoff.
func sleepFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// propagationRetry runs cycle, and runs it again while it fails only on
// resources Google had not caught up with: at most len(propagationWaits)
// times, and never past the last wait. The error of a cycle that is not on the
// closed list, or that is still there after the last retry, comes back as it
// is -- the failure a person would have read before any of this existed. A
// context that is done ends the wait, and its error is what comes back.
//
// cycle returns the plan the apply was on, which is what the closed list is
// judged against: without it there is nothing to say the refused resource is
// one this apply created. It is nil when the cycle failed before it had a
// plan, or when the plan it had changed nothing. what is the step's name in
// the one line a retry prints, such as "the installation step".
func propagationRetry(ctx context.Context, w io.Writer, what string, cycle func(context.Context) (*tf.Plan, error)) error {
	for attempt := 0; ; attempt++ {
		plan, err := cycle(ctx)
		if err == nil {
			return nil
		}
		if attempt >= len(propagationWaits) || !tf.PropagationFailure(err, plan) {
			return err
		}
		wait := propagationWaits[attempt]
		fmt.Fprintf(w, "retrying %s in %ds: Google had not yet caught up with a resource it just created (attempt %d of %d)\n",
			what, int(wait.Seconds()), attempt+1, len(propagationWaits))
		if err := propagationSleep(ctx, wait); err != nil {
			return err
		}
	}
}
