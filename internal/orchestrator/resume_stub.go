// The resumer's stub: C3 ships the pause contract and no resumer. It is the
// seam the tick resumes a project's paused runs through, and the answer that
// stands until PR4 fills it: nothing is due, so nothing is reissued. PR4 owns
// the real one (internal/orchestrator/resume.go) and points the tick at it, and
// I2 deletes this file.

package orchestrator

import (
	"context"
	"time"
)

// ResumePauses resumes every pause the project is due to resume at now: it
// reads the nodes under runstore.PauseNodeRoot whose resume time has passed, is
// not cancelled and whose run really ended paused, and reissues each one once
// as a run that continues the paused chain (D3185, D3189). It returns how many
// it resumed, for the tick's own report.
//
// PR4's body. Until it exists this answers zero and reads nothing: no node, no
// record, no clock — two ticks with two different instants get the same answer,
// which is what a stub that decides nothing means. The project and the database
// the real one reads are the daemon's, which is where PR4 wires it.
func ResumePauses(ctx context.Context, now time.Time) (int, error) {
	return 0, nil
}
