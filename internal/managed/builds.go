package managed

import (
	"errors"
	"fmt"

	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The image build bound, and the decision the read's answer is put to (M5,
// D1013).
//
// What it is, and why it is here rather than in the command. D1013 is "what
// bounds the cost of billable image builds in an unattended run?", and its
// answer is a count recorded in the project's own creation fact — a
// version-3 `fugaro/managed.json`, written only by the two owner's paths and
// read through the same number-and-label proof as the rest of the object. The
// read itself is internal/infra/managed's (ReadImageBuilds, over the one
// readFact this package's budget.go also names), and what THIS file owns is
// the decision the read's answer is put to: which of the answers is a bound a
// command may run under, which is the default of one, and which is a refusal.
// It is the same split as the budget's — internal/infra/managed reads,
// this package decides — so there is exactly one place that says what a
// bound is, and the command that counts builds calls it rather than judging
// the answer itself.
//
// The one thing that makes this decision unlike the budget's is the default.
// A managed creation that recorded no amount is a refusal for an apply (an
// absent amount is not an unlimited one, Task 4.4); a managed creation that
// recorded no *bound* is the default of one build per invocation, never "no
// bound" (D1013's table, spec §5), because a version-1 or version-2 fact is a
// project whose builds are still held to a count and the count it is held to
// is the default. So the two refusals the budget's gate has — a project that
// is not managed, and a figure the marker's own rule refuses — are refusals
// here too, and the third — no figure recorded — is a value.

// ImageBuilds is the bound a command runs under: the number of billable image
// builds one invocation of `fugaro image refresh` may start, in a managed
// project. Its field is unexported and the zero value holds nothing, for the
// reason enable.go's Enable gives: a caller that forgets to thread a read
// through is held to nothing rather than to everything, and outside this
// package the only way to hold a bound is to be handed one by ImageBuildsBound
// — another package can copy a value or hold the zero one, and cannot build a
// usable bound of its own.
type ImageBuilds struct {
	builds int64
}

// Check refuses a bound an image refresh may not run under, and it is the
// marker's own rule and not a copy of it: internal/infra/managed's
// CheckImageBuilds, which the marker's writer (Marker.Bytes, and the two
// owner's paths before anything is created or replaced) and its reader
// (readFactObject, and ReadImageBuilds' own answer) both apply — a count from
// MinImageBuilds to MaxImageBuilds. One rule, called rather than restated, so
// this decision can never accept a figure the marker would refuse.
func (b ImageBuilds) Check() error { return inframanaged.CheckImageBuilds(b.builds) }

// Valid reports whether the bound is one a refresh may run under: exactly when
// Check refuses nothing. It is a method rather than a check inside
// ImageBuildsBound so that the zero value's answer and the read's answer are
// judged by the same rule.
func (b ImageBuilds) Valid() bool { return b.Check() == nil }

// Count is the number of builds the bound allows. It is what a command counts
// against, and it is meaningful only for a bound ImageBuildsBound returned
// (the zero value's 0 allows nothing, which is the fail-closed direction for a
// caller that never read).
func (b ImageBuilds) Count() int64 { return b.builds }

// ImageBuildsRead is the bound's read as the decision takes it: the answer
// internal/infra/managed.ReadImageBuilds gave, in the shape the caller that
// holds the cloud clients can make it. It is deliberately the caller's own
// struct rather than the read's return values, because the decision needs one
// thing the read cannot answer on its own — whether the project is managed at
// all, which is what tells a creation that recorded no bound (the default of
// one) from a project this bound does not apply to.
type ImageBuildsRead struct {
	// Project is the Fugaro project's name: every refusal below names it.
	Project string
	// Builds, OK, Default and Invalid are internal/infra/managed's
	// ImageBuildsAnswer, field for field. OK is true only when the creation
	// fact was read AND it records a bound the marker's own rule accepts;
	// Default is true when it was read and is a version that records no bound,
	// which is the case this decision answers with the default of one; Invalid
	// is true when the fact records a figure that fails that rule, and Builds
	// then holds the refused figure. The read's own one-line reason is not
	// carried: the case it would be printed for (a project that is not managed)
	// is answered by ManagedReason below, and the case the budget prints the
	// read's reason for (managed, and no figure recorded) is the case this
	// bound answers with the default rather than with a sentence.
	Builds  int64
	OK      bool
	Default bool
	Invalid bool
	// Err is set only when the read could not be made at all, and it is never
	// a reason to proceed.
	Err error
	// Managed and ManagedReason are internal/infra/managed's IsManaged, and
	// the decision below consults them in two of its cases, so a caller fills
	// them in whenever the read answered at all — for a version-1 or version-2
	// fact as much as for one that answered nothing. Default && Managed is the
	// case that answers with the default of one, so a caller that left Managed
	// false there would have a managed project's older fact refused as one this
	// bound does not apply to; and a version-3 fact whose recorded bound the
	// read refused and a project that is not managed at all both arrive with no
	// bound, no default and no error, where the second read is what tells them
	// apart. The zero value is "not managed", which is the fail-closed
	// direction: a bound that applies to a project nobody proved is managed is
	// a bound nothing may be run under.
	Managed       bool
	ManagedReason string
}

// ErrNoImageBuildBound is the refusal of a bound that was not read. It is a
// value as well as a message, so a test can match on it without reading
// English, and it is this package's own sentinel rather than a second one for
// the budget's gate: the two are different questions asked of the same object.
var ErrNoImageBuildBound = errors.New("the image build bound was not read")

// ImageBuildsBound is D1013's whole decision, in one place: given the bound's
// read, it returns the bound a refresh runs under, or the refusal that says
// why none does.
//
// The order of the cases is the order of the answers: an error first (a read
// that did not answer is not a bound), then the recorded bound itself, then
// the recorded figure that fails the marker's own rule, then the default the
// two older versions mean, and last the two ways of having nothing at all.
// Only one branch returns a usable bound, and every refusal returns the zero
// value, so there is no path through this function that lets a command run
// under a count nothing recorded.
func ImageBuildsBound(r ImageBuildsRead) (ImageBuilds, error) {
	b := ImageBuilds{builds: r.Builds}
	switch {
	case r.Err != nil:
		return ImageBuilds{}, fmt.Errorf("%w: the bound read of %s could not be made, and a read that did not answer is a project whose builds nothing bounds: %s",
			ErrNoImageBuildBound, safetext.Field(r.Project), safetext.Field(r.Err.Error()))
	case r.OK && b.Valid():
		return b, nil
	case r.OK || r.Invalid || r.Builds != 0:
		// A recorded figure that fails the marker's own Check: through
		// ReadImageBuilds this is a version-3 marker whose count is outside
		// the shape the write enforces (the read answers OK only for an
		// accepted one, and hands the refused figure back for exactly this
		// sentence); a second caller passing a figure built by hand lands here
		// too. Either way it is refused rather than replaced by the default,
		// which is the whole point of the version: a project whose recorded
		// bound cannot be read is a project whose bound is not known, and the
		// default is for a creation that was never asked for one.
		why := "the recorded figure was refused"
		if err := b.Check(); err != nil {
			why = err.Error()
		}
		return ImageBuilds{}, fmt.Errorf("%w: the image build bound recorded in the marker of %s is invalid: %s",
			ErrNoImageBuildBound, safetext.Field(r.Project), safetext.Field(why))
	case r.Default && r.Managed:
		// The version-1 and version-2 fact: managed, and no bound recorded, so
		// the bound is the default of one — never "no bound" (D1013's table,
		// spec §5). It is a value and not a refusal because the project IS
		// managed and its builds ARE held to a count; the count is the default
		// one.
		return ImageBuilds{builds: inframanaged.DefaultImageBuilds}, nil
	case !r.Managed:
		// Not managed at all: no recorded bound applies to the project, and
		// the command keeps the coding agent's refusal. This is the branch a
		// caller that reaches this decision without having read the marks
		// lands in, which is why the zero value of the answer is here.
		reason := safetext.Field(r.ManagedReason)
		if reason == "" {
			reason = "its creation fact was not believed"
		}
		return ImageBuilds{}, fmt.Errorf("%w: %s is not managed (%s), so no recorded image build bound applies to it and its builds are the project's own path",
			ErrNoImageBuildBound, safetext.Field(r.Project), reason)
	default:
		// Managed, and the read answered neither a bound nor the default: a
		// version-3 fact whose figure was refused, or an answer built by hand.
		// Refused rather than defaulted — the default is for a creation that
		// was never asked for a bound, and this project's recorded bound is
		// one this program could not read.
		return ImageBuilds{}, fmt.Errorf("%w: %s is managed and the read of its creation fact answered no image build bound and no default, and a bound that was not read is not the default one",
			ErrNoImageBuildBound, safetext.Field(r.Project))
	}
}
