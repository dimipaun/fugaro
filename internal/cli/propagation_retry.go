package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
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
// cycle returns the plan the apply was on, and the closed list is judged
// against what THIS STEP has created: the creates of every plan its attempts
// applied, not only the newest one. The two are not the same plan, and the
// difference is the gap this retry exists for: the first apply creates the
// service or the account before the grants on it are refused, so it lands
// those creates in the state and the plan of the next attempt no longer holds
// them. Judged against that plan alone, the same failure a second time would
// name a resource no plan of the attempt creates -- a real 404 as far as the
// classifier could tell -- and the step would stop after its first wait
// instead of using the three. Every plan is judged on its own terms still: a
// failure off the closed list, or one whose resource no plan of any attempt
// created, stops the step at once. cycle returns a nil plan when it failed
// before it had one, or when the plan it had changed nothing. what is the
// step's name in the one line a retry prints, such as "the installation step".
func propagationRetry(ctx context.Context, w io.Writer, what string, cycle func(context.Context) (*tf.Plan, error)) error {
	var planned []*tf.Plan
	for attempt := 0; ; attempt++ {
		plan, err := cycle(ctx)
		planned = append(planned, plan)
		if err == nil {
			return nil
		}
		if attempt >= len(propagationWaits) || !tf.PropagationFailure(err, createdBy(planned)) {
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

// createdBy is every create the step's attempts applied, as the changes of one
// plan -- the form the classifier reads (tf.PropagationFailure). Only creates
// are kept, because they are the whole of what the closed list asks of a plan:
// whether the resource the error names is one this step made. A resource two
// attempts both planned is kept once, and a step that created nothing gives
// nil, which the classifier reads as any plan without a create in it.
func createdBy(plans []*tf.Plan) *tf.Plan {
	var out *tf.Plan
	seen := map[string]bool{}
	for _, p := range plans {
		if p == nil {
			continue
		}
		for _, rc := range p.ResourceChanges {
			if !slices.Contains(rc.Change.Actions, "create") || seen[rc.Address] {
				continue
			}
			seen[rc.Address] = true
			if out == nil {
				out = &tf.Plan{}
			}
			out.ResourceChanges = append(out.ResourceChanges, rc)
		}
	}
	return out
}
