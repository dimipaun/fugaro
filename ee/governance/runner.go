// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance

import (
	"time"

	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// Timeout is the stage's own wall-clock ceiling, read off the governance the
// run resolved for it: the effective stage_timeout of the one merge every
// governance key goes through, so the project layer's block is the ceiling and
// the default branch's file and the repository's own may only tighten it. Zero
// means the run resolved none, and the stage is bounded by the run's own
// timeouts.stage alone.
//
// It is the pair the runner's stage loop already performed before the move,
// and it is one function with runner.StageTimeoutOf rather than a second copy
// of the mapping: the bound a stage is given and the bound the core's
// ignored-block line reads out of the layers it merged have to agree (D522).
func Timeout(v runner.StageView) time.Duration {
	return runner.StageTimeoutOf(v.Governance, v.Stage)
}

// TimedOut is the decision when that ceiling fires: the stage_budget halt by
// time, which the core records, reports and words (governanceHalt, the
// checkpoint report). The halt itself stays core, so a hook cannot invent a
// stop the core does not know how to record (D521, D529).
//
// Timeout and TimedOut are one tripwire and are registered as one pair: a
// ceiling nobody stops at would be no ceiling.
func TimedOut(runner.StageView) runner.StageDecision {
	return runner.StageDecision{Halt: true, Reason: runstore.HaltStageBudget, Detail: "time"}
}
