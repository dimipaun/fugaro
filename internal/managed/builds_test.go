package managed

import (
	"errors"
	"strings"
	"testing"

	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
)

// The image build bound's decision (M5, D1013).
//
// What is asserted here is the decision and not the read: whether an object
// records a bound is internal/infra/managed's question, answered by its own
// fixtures (read_test.go), and the cases below are its answers. The read's own
// end-to-end cases — the version-3 object, the two older versions, and every
// count outside the range, through the real ReadImageBuilds, the real marker
// object and the fakes — are in internal/infra/managed's read_test.go and
// internal/cli's managed_flag_test.go, so a change to the read that stopped
// producing one of these answers is caught there rather than assumed here.

// refusedBound is the bound a read that answered with no usable count leaves
// behind: the zero value, which allows nothing, with ErrNoImageBuildBound for
// a refusal. Every case below starts here, so "nothing may be run under it" is
// asserted the same way for all of them.
func refusedBound(t *testing.T, r ImageBuildsRead) ImageBuilds {
	t.Helper()
	bound, err := ImageBuildsBound(r)
	if err == nil {
		t.Fatalf("a read that answered with no bound returned one: %+v", r)
	}
	if bound.Count() != 0 || bound.Valid() {
		t.Fatalf("the bound after a refusal is %+v, which a caller could count against", bound)
	}
	if !errors.Is(err, ErrNoImageBuildBound) {
		t.Errorf("refusal %v is not ErrNoImageBuildBound", err)
	}
	return bound
}

// Every way the bound's read can fail to answer with a count, each leaving
// nothing to run under and its own sentence: a read that did not answer, a
// recorded figure the marker's own rule refuses, a project that is not
// managed, and an answer that is neither a bound nor the default. The one case
// that is NOT here is the older version, because that one is a value (the
// default of one) and its test is below — the difference between a refusal and
// the default is the whole of what makes this decision unlike the budget's.
func TestImageBuildsBoundRefusesEveryReadThatAnsweredNoBound(t *testing.T) {
	const reason = "gs://fugaro-runs-proj-1234/fugaro/managed.json is not there (or cannot be read), so aurora was not created by a managed run"
	for _, tc := range []struct {
		what string
		read ImageBuildsRead
		want []string
	}{
		{
			what: "the read could not be made at all",
			read: ImageBuildsRead{Project: project, Err: errors.New("reading project proj-1234 for aurora: 502")},
			want: []string{"could not be made", "502"},
		},
		{
			what: "the recorded bound is zero",
			read: ImageBuildsRead{Project: project, OK: true, Builds: 0, Managed: true},
			want: []string{"the image build bound recorded in the marker of aurora is invalid", "outside 1 to 1000"},
		},
		{
			what: "the recorded bound is negative",
			read: ImageBuildsRead{Project: project, OK: true, Builds: -5, Managed: true},
			want: []string{"is invalid", "outside 1 to 1000"},
		},
		{
			what: "the recorded bound is above the ceiling",
			read: ImageBuildsRead{Project: project, OK: true, Builds: 1001, Managed: true},
			want: []string{"is invalid", "outside 1 to 1000"},
		},
		{
			what: "the recorded bound is 9e18",
			read: ImageBuildsRead{Project: project, OK: true, Builds: 9_000_000_000_000_000_000, Managed: true},
			want: []string{"is invalid", "is outside 1 to 1000"},
		},
		{
			what: "a version-3 marker whose recorded bound the read refused",
			read: ImageBuildsRead{Project: project, Invalid: true, Builds: 1001},
			want: []string{"the image build bound recorded in the marker of aurora is invalid", "outside 1 to 1000"},
		},
		{
			what: "the project is not managed",
			read: ImageBuildsRead{Project: project, ManagedReason: reason},
			want: []string{"is not managed", "not there (or cannot be read)"},
		},
		{
			what: "a read that answered nothing at all",
			read: ImageBuildsRead{Project: project},
			want: []string{"is not managed", "its creation fact was not believed"},
		},
		{
			// Managed, and neither a bound nor the default: a hand-built answer,
			// or a version-3 fact whose figure the read refused. It is refused
			// rather than defaulted, because the default is for a creation that
			// was never asked for a bound.
			what: "managed, and the read answered neither a bound nor the default",
			read: ImageBuildsRead{Project: project, Managed: true},
			want: []string{"answered no image build bound and no default", "a bound that was not read is not the default one"},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			bound := refusedBound(t, tc.read)
			_, err := ImageBuildsBound(tc.read)
			if err == nil {
				t.Fatal("ImageBuildsBound refused nothing")
			}
			if !strings.Contains(err.Error(), project) {
				t.Errorf("the refusal does not name the project: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %v does not carry %q", err, want)
				}
			}
			if bound.Count() != 0 {
				t.Errorf("the refused bound allows %d builds, want none", bound.Count())
			}
		})
	}
}

// The plan's table row, as the decision answers it: a version-1 or version-2
// creation fact records no bound, and that is the default of one build per
// invocation — a value, not a refusal, and never "no bound". It is a row and
// not a note because the two directions it refuses are both mutations worth
// catching: a decision that refused here would stop every refresh in a project
// marked before this task, and one that answered with a count larger than one
// would hand an unattended session the unbounded bill D1013 exists to prevent.
func TestImageBuildsBoundOlderVersionIsTheDefaultOfOne(t *testing.T) {
	// The read's own answer for those versions (internal/infra/managed's
	// read_test.go, TestManagedMarkerOlderVersionMeansTheDefaultBound): the
	// fact was read, it is this project's, and it records no bound.
	for _, r := range []ImageBuildsRead{
		{Project: project, Default: true, Managed: true},
		// The same answer for a project whose second read answered with the
		// reason: the reason is not consulted, the default is the answer.
		{Project: project, Default: true, Managed: true, ManagedReason: "a version-1 creation fact"},
	} {
		bound, err := ImageBuildsBound(r)
		if err != nil {
			t.Fatalf("%+v: the default was refused: %v", r, err)
		}
		if bound.Count() != inframanaged.DefaultImageBuilds {
			t.Errorf("%+v: the bound is %d, want the default %d", r, bound.Count(), inframanaged.DefaultImageBuilds)
		}
		if !bound.Valid() {
			t.Errorf("%+v: the default bound is not one a refresh may run under: %v", r, bound.Check())
		}
	}
	// And the default is one and not "unlimited": the count a command counts
	// against is the smallest one the marker could record.
	if inframanaged.DefaultImageBuilds != inframanaged.MinImageBuilds {
		t.Errorf("the default bound is %d, want the smallest count a managed creation records (%d)", inframanaged.DefaultImageBuilds, inframanaged.MinImageBuilds)
	}
}

// The only answer that returns a bound is the read's own count, carried whole:
// what a command counts against is the figure the owner's path recorded and
// not a default, a clamp or a maximum.
func TestImageBuildsBoundCarriesTheRecordedCount(t *testing.T) {
	for _, n := range []int64{1, 2, 500, 1000} {
		bound, err := ImageBuildsBound(ImageBuildsRead{Project: project, Builds: n, OK: true, Managed: true})
		if err != nil {
			t.Fatalf("a read that answered with %d was refused: %v", n, err)
		}
		if bound.Count() != n {
			t.Errorf("the bound is %d, want the count the read answered with (%d)", bound.Count(), n)
		}
		if !bound.Valid() {
			t.Errorf("the bound %d is not one a refresh may run under: %v", n, bound.Check())
		}
	}
}

// The zero value allows nothing, which is the fail-closed direction for a
// caller that never read: a command that counted against it would start no
// build at all rather than an unbounded number of them.
func TestImageBuildsZeroValueAllowsNothing(t *testing.T) {
	var bound ImageBuilds
	if bound.Count() != 0 {
		t.Errorf("the zero bound allows %d builds, want none", bound.Count())
	}
	if bound.Valid() {
		t.Error("the zero bound is valid")
	}
	if err := bound.Check(); err == nil {
		t.Error("the zero bound passes the marker's own check")
	}
}

// Check is the marker's own rule and not a copy of it: the range the decision
// accepts is exactly the range internal/infra/managed writes and reads, so the
// two can never drift into a bound the marker would refuse.
func TestImageBuildsValidIsTheMarkersOwnRange(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want bool
	}{
		{0, false},
		{-1, false},
		{inframanaged.MinImageBuilds, true},
		{inframanaged.MaxImageBuilds, true},
		{inframanaged.MaxImageBuilds + 1, false},
		{9_000_000_000_000_000_000, false},
	} {
		got := ImageBuilds{builds: tc.n}.Valid()
		if got != tc.want {
			t.Errorf("ImageBuilds{%d}.Valid() = %v, want %v", tc.n, got, tc.want)
		}
	}
}

// The refusal a reader acts on is one line, whatever the read put in it: an
// error's text can come from an API, and a newline in it must not become a
// line of this program's output.
func TestImageBuildsBoundRefusalIsOneLine(t *testing.T) {
	_, err := ImageBuildsBound(ImageBuildsRead{Project: project, Err: errors.New("marker read: csi\u009b31m bidi‮evil line\nforged\x00")})
	if err == nil {
		t.Fatal("ImageBuildsBound refused nothing")
	}
	if strings.ContainsAny(err.Error(), "\r\x00") || strings.Count(err.Error(), "\n") != 0 {
		t.Errorf("the refusal carries the error's own control characters: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "marker read:") {
		t.Errorf("the read's own error was dropped: %q", err.Error())
	}
}

// The read's error is carried through safetext.Field: a 5xx body can hold a
// C1 CSI, a bidi override and ten thousand characters, and all of them come
// out neutralised and cut rather than printed.
func TestImageBuildsBoundSanitisesTheReadError(t *testing.T) {
	for _, payload := range []string{"csi\u009b31m", "bidi‮evil", "line\nforged", strings.Repeat("z", 10000)} {
		_, err := ImageBuildsBound(ImageBuildsRead{Project: project, Err: errors.New("marker read: " + payload)})
		if err == nil {
			t.Fatal("a failed read returned a bound")
		}
		got := err.Error()
		if strings.ContainsAny(got, "\u009b‮\n") {
			t.Errorf("the refusal carries a raw control, bidi or newline: %q", got)
		}
		if len([]rune(got)) > 1000 {
			t.Errorf("the refusal is %d runes: the error was not cut", len([]rune(got)))
		}
		if !strings.Contains(got, "marker read:") {
			t.Errorf("the read's own error was dropped: %q", got)
		}
	}
}
