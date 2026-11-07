package cli

// fugaro doctor's reviews-due line (D963, R2): the checkout's own report
// folder, read the way `fugaro runs digest --due` reads it, reported as one
// check. It warns when a period has closed with no report at all, says an
// info line when the newest report the checkout holds is more than one period
// old, and is silent in the state a repository that has never reviewed is in
// -- so keeping the cadence is nudged and adopting it is not nagged.
//
// The class is half the task (docs/periodic-reviews/reconciliation.md M9):
// doctor's default view prints the actionable, the update, the decision and
// the security classes, counts the measurements and files every unclassified
// info check in `audit`, where the line is written, counted in the pointer and
// never printed. A test that stops at "it returns a check" would pass against
// a line nobody sees, which is why the default view is rendered here and not
// only the check's fields asserted.
//
// Everything is a folder written by hand under a t.TempDir() and a clock the
// test names: no git, no config, no credential and no bucket.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/digest"
)

// reviewsDay is the check's clock: a day, at midnight UTC.
func reviewsDay(t *testing.T, day string) time.Time {
	t.Helper()
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// reviewsWrite puts the report files of these period keys in a fresh checkout
// root, at the path D321's layout names: what the check reads is a file's
// presence, so the body is only what a reader would find there.
func reviewsWrite(t *testing.T, root string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		path := filepath.Join(root, filepath.FromSlash(digest.Dir), filepath.FromSlash(digest.PeriodPath(key)))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# Run digest: "+key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// reviewsMiss reports the check a root and a day give, failing when there is
// none: the two states this file is about are lines, not absences.
func reviewsMiss(t *testing.T, root string, day string) doctorCheck {
	t.Helper()
	c := doctorReviewsCheck(root, reviewsDay(t, day))
	if c == nil {
		t.Fatalf("the reviews-due check says nothing for %s on %s; want the line that state is", root, day)
	}
	return *c
}

// noReviewsBucketReads fails the test if the check opens either bucket: the
// reports this line reads are the checkout's own files (R2), and the stored
// reports another task reads from the runs bucket (E7, E8) are not its
// source.
func noReviewsBucketReads(t *testing.T) {
	t.Helper()
	oldLayer, oldShared := layerBucketOpener, sharedBucketOpener
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("the reviews-due check read the project layer bucket")
		return nil, nil
	}
	sharedBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("the reviews-due check read the runs bucket")
		return nil, nil
	}
	t.Cleanup(func() { layerBucketOpener, sharedBucketOpener = oldLayer, oldShared })
}

// TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport is the headline and
// the red step: a period has closed and the folder holds no report for it, so
// the cadence lapsed and doctor says so as work to do.
//
// The periods that count are the week, the month and the year -- the ones
// that close on a single day. The seven closed days the due computation
// carries are the catch-up's own window (R0): a missed day is what the next
// report run fills in, and the state they describe is the info line below and
// not a warning.
func TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport(t *testing.T) {
	// 2026-11-02 is a Monday, so the week that ended 2026-11-01 (2026-W44) has
	// closed, and the folder holds day reports and no week report.
	week := t.TempDir()
	reviewsWrite(t, week, "2026-10-27", "2026-10-26")
	c := reviewsMiss(t, week, "2026-11-02")
	if c.ID != "reviews-due" || c.Severity != "warning" {
		t.Errorf("the line is %s at severity %q, want reviews-due at warning", c.ID, c.Severity)
	}
	if !strings.Contains(c.Problem, "2026-W44 (week)") || !strings.Contains(c.Problem, "closed") {
		t.Errorf("the line does not name the period that closed: %q", c.Problem)
	}
	// A day is a period too, and one is missing above -- but the catch-up
	// covers the days, so the warning may not be about one of them: a monthly
	// or a weekly repository would be told off for a day it never keeps.
	if strings.Contains(c.Problem, "(day)") {
		t.Errorf("the warning is about a day, which the catch-up covers: %q", c.Problem)
	}

	// The month, on the day it closes: 2026-12-01 is a Tuesday, so only the
	// month that ended 2026-11-30 is due, and the folder's newest report is
	// the October one.
	month := t.TempDir()
	reviewsWrite(t, month, "2026-10-15")
	c = reviewsMiss(t, month, "2026-12-01")
	if !strings.Contains(c.Problem, "2026-11 (month)") {
		t.Errorf("the first of December does not name the month that closed: %q", c.Problem)
	}
}

// TestDoctorReviewsDueIsInfoWhenTheNewestIsOld is the other state: nothing
// coarse has closed since the newest report, and the newest report is more
// than one period of its own dimension old -- which is what a repository that
// keeps a daily cadence sees when it falls days behind.
func TestDoctorReviewsDueIsInfoWhenTheNewestIsOld(t *testing.T) {
	// 2026-10-27 is a Tuesday and not the first of a month, so no week, month
	// or year is due; the newest report is the day of 2026-10-23, four days
	// before it.
	root := t.TempDir()
	reviewsWrite(t, root, "2026-10-23")
	c := reviewsMiss(t, root, "2026-10-27")
	if c.ID != "reviews-due-old" || c.Severity != "info" {
		t.Errorf("the line is %s at severity %q, want reviews-due-old at info", c.ID, c.Severity)
	}
	if !strings.Contains(c.Problem, "2026-10-23 (day), 4 days old") {
		t.Errorf("the line does not name the newest report and its age: %q", c.Problem)
	}
}

// TestDoctorReviewsDueIsSilentWhenNothingIsDue: silence is the common state,
// and it is what keeps this line from being one a reader skips. A checkout
// whose newest report is the period the cadence is in the middle of, a
// checkout that holds no report folder at all (a repository that has never
// reviewed is told once, by the setup path, not on every doctor run), and an
// empty folder are all nothing to say.
func TestDoctorReviewsDueIsSilentWhenNothingIsDue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		keys  []string
		mkdir bool
		day   string
	}{
		// The report of yesterday, on the day after: one day old, which is not
		// more than one period of a day.
		{name: "the newest report is the period just closed", keys: []string{"2026-10-26"}, day: "2026-10-27"},
		// The week that closed has its report, and the days the catch-up
		// carries are not the warning's business.
		{name: "every closed period has a report", keys: []string{"2026-W44", "2026-11-01"}, day: "2026-11-02"},
		{name: "the checkout holds no report folder", day: "2026-10-27"},
		{name: "the folder is empty", mkdir: true, day: "2026-10-27"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			reviewsWrite(t, root, tc.keys...)
			if tc.mkdir {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(digest.Dir)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if c := doctorReviewsCheck(root, reviewsDay(t, tc.day)); c != nil {
				t.Errorf("doctor says %+v, want nothing: this state is not a finding", *c)
			}
		})
	}
}

// TestDoctorReviewsDueIsInTheDefaultView is the half of the task a check that
// "prints a line" fails (M9): the warning is doctorClassActionable and the
// info line is doctorClassDecision, and the default view prints both. The
// control is the failure the class exists for -- the same checks filed as
// audit are written, counted in the pointer and never shown.
func TestDoctorReviewsDueIsInTheDefaultView(t *testing.T) {
	warnRoot := t.TempDir()
	reviewsWrite(t, warnRoot, "2026-10-27", "2026-10-26")
	warning := reviewsMiss(t, warnRoot, "2026-11-02")
	infoRoot := t.TempDir()
	reviewsWrite(t, infoRoot, "2026-10-23")
	info := reviewsMiss(t, infoRoot, "2026-10-27")

	if got := doctorClassOf(warning); got != doctorClassActionable {
		t.Errorf("the closed-period warning is classed %q, want %q", got, doctorClassActionable)
	}
	if got := doctorClassOf(info); got != doctorClassDecision {
		t.Errorf("the old-newest line is classed %q, want %q: the state is valid and the line names the command for each choice", got, doctorClassDecision)
	}
	if out := doctorDefaultOutput(t, []doctorCheck{warning}); !strings.Contains(out, "reviews-due:") {
		t.Errorf("the warning is not an item of the default view:\n%s", out)
	}
	out := doctorDefaultOutput(t, []doctorCheck{info})
	if !strings.Contains(out, "yours to decide:") || !strings.Contains(out, "reviews-due-old:") {
		t.Errorf("the info line is not a decision the default view prints:\n%s", out)
	}

	// The wiring, which the class is worth nothing without: doctor runs the
	// check on the checkout it stands in. A rig installation whose checkout
	// holds the same folder, on the same day, has the line among the checks it
	// ran and prints it.
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	oldNow := budgetNow
	budgetNow = func() time.Time { return reviewsDay(t, "2026-10-27") }
	t.Cleanup(func() { budgetNow = oldNow })
	checkout := gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n")
	t.Chdir(checkout)
	reviewsWrite(t, checkout, "2026-10-23")
	o := doctorJSON(t)
	wired, ok := doctorCheckByID(o.Checks, "reviews-due-old")
	if !ok {
		t.Fatalf("doctor did not run the reviews-due check: %+v", o.Checks)
	}
	if wired.Class != doctorClassDecision {
		t.Errorf("doctor classed its own reviews-due-old line %q, want %q", wired.Class, doctorClassDecision)
	}
	if out := doctorDefaultOutput(t, o.Checks); !strings.Contains(out, "reviews-due-old:") {
		t.Errorf("doctor ran the check and its default view does not print it:\n%s", out)
	}

	// The control: the same two lines with the info one's ID unclassified --
	// which is what a class nobody named leaves them -- are not printed at
	// all, only counted. That is the state the task exists to keep out of.
	unclassified := warning
	unclassified.ID = "reviews-due-unclassified"
	unclassified.OK = false
	unclassified.Severity = "info"
	if got := doctorClassOf(unclassified); got != doctorClassAudit {
		t.Fatalf("an info check with no class is %q, want %q", got, doctorClassAudit)
	}
	if out := doctorDefaultOutput(t, []doctorCheck{unclassified}); strings.Contains(out, "reviews-due-unclassified") {
		t.Errorf("the control is printed; the class check above proves nothing:\n%s", out)
	}
}

// TestDoctorReviewsDueIsOffline: the line reads the checkout's own folder and
// nothing else, so it answers in a checkout that has never been set up -- no
// git, no config, no credential, no bucket -- and writes nothing. Both bucket
// seams fail the test if they are reached at all.
func TestDoctorReviewsDueIsOffline(t *testing.T) {
	noReviewsBucketReads(t)
	root := t.TempDir()
	reviewsWrite(t, root, "2026-10-23")
	before := dueEntries(t, root)

	c := reviewsMiss(t, root, "2026-10-27")
	if c.ID != "reviews-due-old" {
		t.Fatalf("the check answered %+v, want the folder's own reading", c)
	}
	if got := dueEntries(t, root); !equalStrings(got, before) {
		t.Errorf("the check wrote to the checkout: %v, want the %v it read", got, before)
	}
}

// TestDoctorReviewsDueNamesTheCommand: a line whose fix is not a command the
// reader can run is a line that names the problem and nothing else. Both
// states name `fugaro runs digest`, and each names the work of its own state:
// the period that closed, and the reading that lists every period that is due.
func TestDoctorReviewsDueNamesTheCommand(t *testing.T) {
	warnRoot := t.TempDir()
	reviewsWrite(t, warnRoot, "2026-10-27", "2026-10-26")
	warning := reviewsMiss(t, warnRoot, "2026-11-02")
	if !strings.Contains(warning.Fix, "`fugaro runs digest --period week --date 2026-10-26`") {
		t.Errorf("the warning's fix does not name the report of the period that closed: %q", warning.Fix)
	}
	if line := doctorItemLine(t, warning); !strings.Contains(line, "fugaro runs digest --period week --date 2026-10-26") {
		t.Errorf("the default view prints the warning without its command: %q", line)
	}

	infoRoot := t.TempDir()
	reviewsWrite(t, infoRoot, "2026-10-23")
	info := reviewsMiss(t, infoRoot, "2026-10-27")
	for _, want := range []string{"`fugaro runs digest --period day`", "`fugaro runs digest --due`"} {
		if !strings.Contains(info.Fix, want) {
			t.Errorf("the info line's choices do not name %s: %q", want, info.Fix)
		}
	}
	if out := doctorDefaultOutput(t, []doctorCheck{info}); !strings.Contains(out, "fugaro runs digest --due") {
		t.Errorf("the default view prints neither choice of the info line:\n%s", out)
	}
}
