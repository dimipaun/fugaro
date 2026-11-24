// The resumer's stub: C3 shipped the pause contract and no resumer, and this is
// the answer that stood until PR4 filled the seam. PR4 owns the real body
// (internal/orchestrator/resume.go, assigned to ResumePauses), and what is left
// here is the body that resumes nothing — the one a build with no real one must
// answer with — and the guard that makes it the answer only while the real one
// is missing. I2 deletes this file.
//
// The shape is internal/runner/suspend_stub.go's, and for the same reason: a
// side written against a contract runs its own tests against a signature that
// exists, and the file that replaces it lands without either file naming the
// other.

package orchestrator

import (
	"context"
	"time"
)

// resumeNothing is the answer of a build with no resumer in it: nothing is due,
// so nothing is reissued. It reads no node, no record and no clock — two ticks
// with two different instants get the same answer, which is what a body that
// decides nothing means.
func resumeNothing(context.Context, PauseReader, ResumeLauncher, time.Time) (ResumeOutcome, error) {
	return ResumeOutcome{}, nil
}

// The assignment stands only while nothing else has made one: resume.go is
// presented before this file, so an unguarded init here would run last and put
// the body that resumes nothing back over the real one. Asking whether the seam
// is still nil makes this file the answer that stands until a real one exists,
// whatever the other file is called.
func init() {
	if ResumePauses == nil {
		ResumePauses = resumeNothing
	}
}
