package issues

import (
	"reflect"
	"testing"
	"time"
)

// mineNow is the clock every case is built against: the 30-day hiding and the
// within-group orders are all relative to it.
var mineNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// mineRead is the time the shared reads below happened at, so a row's as-of is
// a value the test knows.
var mineRead = time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

// mineDaysAgo is n whole days before the clock, for the cases about the
// 30-day rule: an AddDate over a UTC instant keeps the difference exactly n
// days, which a test that pins the boundary needs.
func mineDaysAgo(n int) *time.Time {
	at := mineNow.AddDate(0, 0, -n)
	return &at
}

func mineAt(t *testing.T, s string) *time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad test time %q: %v", s, err)
	}
	return &at
}

// mineWant is the part of a row the display table compares: which source it
// came from, the words it carries and the status it derives. Coverage, which
// has its own tables, is left to them.
type mineWant struct {
	Number  int
	LocalID string
	Summary string
	Display Display
	AsOf    time.Time
}

// mineCheckWants compares the rows with what the case expects, so that one
// case's input can name more issues than it produces rows for: the row count
// is what pins the merge of a local file into a shared row.
func mineCheckWants(t *testing.T, rows []MineRow, want []mineWant) {
	t.Helper()
	got := make([]mineWant, 0, len(rows))
	for _, r := range rows {
		got = append(got, mineWant{Number: r.Number, LocalID: r.LocalID, Summary: r.Summary, Display: r.Display, AsOf: r.AsOf})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildRows rows:\n got %+v\nwant %+v", got, want)
	}
}

// mineLocalOf is an input whose only source is one issue file of this machine.
func mineLocalOf(l MineLocal) MineInput {
	return MineInput{Local: []MineLocal{l}, Now: mineNow}
}

// mineSharedOf is an input whose only source is one shared read.
func mineSharedOf(s ...MineSharedIssue) MineInput {
	return MineInput{Shared: MineShared{AsOf: mineRead, Issues: s}, Now: mineNow}
}

// mineResolved is a resolved row's shared view with the fixing release the
// coverage cases compare against.
func mineResolved(resolvedIn string) MineSharedIssue {
	return MineSharedIssue{
		Number:     7,
		Summary:    "bootstrap failed",
		Status:     MineStatusResolved,
		HitAt:      mineDaysAgo(3),
		ResolvedAt: mineDaysAgo(1),
		URL:        "https://github.com/dimipaun/fugaro/issues/7",
		ResolvedIn: resolvedIn,
	}
}

// mineCoverageInput is the one-resolved-row input the coverage cases compare
// the running CLI and the installation's release against.
func mineCoverageInput(version, installed, resolvedIn string) MineInput {
	in := mineSharedOf(mineResolved(resolvedIn))
	in.Version, in.Installed = version, installed
	return in
}

func mineCoverageOf(t *testing.T, in MineInput) Coverage {
	t.Helper()
	rows, _ := BuildRows(in)
	if len(rows) != 1 {
		t.Fatalf("BuildRows gave %d rows, want 1", len(rows))
	}
	return rows[0].Coverage
}

func TestMineDisplayStatusDerivation(t *testing.T) {
	cases := []struct {
		name string
		in   MineInput
		want []mineWant
	}{
		{
			name: "an issue this machine has not uploaded is local",
			in: mineLocalOf(MineLocal{
				LocalID: "local-run-20261109120000-4242",
				Summary: "bootstrap failed",
				Status:  StatusPending,
				HitAt:   mineDaysAgo(2),
			}),
			want: []mineWant{{
				LocalID: "local-run-20261109120000-4242",
				Summary: "bootstrap failed",
				Display: DisplayLocal,
				AsOf:    mineNow,
			}},
		},
		{
			name: "an uploaded file with no shared view shows its own status",
			in: mineLocalOf(MineLocal{
				Number:  7,
				LocalID: "local-run-20261109120000-4242",
				Summary: "bootstrap failed",
				Status:  StatusSubmitted,
				HitAt:   mineDaysAgo(2),
			}),
			want: []mineWant{{
				Number:  7,
				Summary: "bootstrap failed",
				Display: DisplaySubmitted,
				AsOf:    mineNow,
			}},
		},
		{
			name: "an issue dismissed on this machine is dismissed",
			in: mineLocalOf(MineLocal{
				LocalID:     "local-run-20261109120000-4242",
				Summary:     "terraform apply was refused",
				Status:      StatusDismissed,
				HitAt:       mineDaysAgo(4),
				DismissedAt: mineDaysAgo(1),
			}),
			want: []mineWant{{
				LocalID: "local-run-20261109120000-4242",
				Summary: "terraform apply was refused",
				Display: DisplayDismissed,
				AsOf:    mineNow,
			}},
		},
		{
			name: "a numbered file the store did not answer for is its own status",
			in: mineLocalOf(MineLocal{
				Number:  9,
				LocalID: "local-run-20261109120000-4242",
				Summary: "run refused to start",
				Status:  StatusPending,
				HitAt:   mineDaysAgo(2),
			}),
			want: []mineWant{{
				Number:  9,
				Summary: "run refused to start",
				Display: DisplayPending,
				AsOf:    mineNow,
			}},
		},
		{
			name: "a file marked submitted without a number the store gave it is local",
			in: mineLocalOf(MineLocal{
				LocalID: "local-run-20261109120000-4242",
				Summary: "run refused to start",
				Status:  StatusSubmitted,
				HitAt:   mineDaysAgo(2),
			}),
			want: []mineWant{{
				LocalID: "local-run-20261109120000-4242",
				Summary: "run refused to start",
				Display: DisplayLocal,
				AsOf:    mineNow,
			}},
		},
		{
			name: "the record's pending status is the row's",
			in: mineSharedOf(MineSharedIssue{
				Number:  11,
				Summary: "cache lock was held",
				Status:  MineStatusPending,
				HitAt:   mineDaysAgo(5),
			}),
			want: []mineWant{{
				Number:  11,
				Summary: "cache lock was held",
				Display: DisplayPending,
				AsOf:    mineRead,
			}},
		},
		{
			name: "the record's submitted status is the row's",
			in: mineSharedOf(MineSharedIssue{
				Number:  12,
				Summary: "policy publish refused",
				Status:  MineStatusSubmitted,
				HitAt:   mineDaysAgo(6),
			}),
			want: []mineWant{{
				Number:  12,
				Summary: "policy publish refused",
				Display: DisplaySubmitted,
				AsOf:    mineRead,
			}},
		},
		{
			name: "the record's dismissed status is the row's",
			in: mineSharedOf(MineSharedIssue{
				Number:      13,
				Summary:     "not our bug",
				Status:      MineStatusDismissed,
				HitAt:       mineDaysAgo(7),
				DismissedAt: mineDaysAgo(2),
			}),
			want: []mineWant{{
				Number:  13,
				Summary: "not our bug",
				Display: DisplayDismissed,
				AsOf:    mineRead,
			}},
		},
		{
			name: "a resolved record this user did not hit again is resolved",
			in:   mineSharedOf(mineResolved("0.22.0")),
			want: []mineWant{{
				Number:  7,
				Summary: "bootstrap failed",
				Display: DisplayResolved,
				AsOf:    mineRead,
			}},
		},
		{
			name: "this user's own flagged occurrence makes the row a regression",
			in: mineSharedOf(func() MineSharedIssue {
				s := mineResolved("0.22.0")
				s.Regression = true
				return s
			}()),
			want: []mineWant{{
				Number:  7,
				Summary: "bootstrap failed",
				Display: DisplayRegression,
				AsOf:    mineRead,
			}},
		},
		{
			name: "a flag on a record that is not resolved leaves the status alone",
			in: mineSharedOf(MineSharedIssue{
				Number:     14,
				Summary:    "still open",
				Status:     MineStatusSubmitted,
				HitAt:      mineDaysAgo(8),
				Regression: true,
			}),
			want: []mineWant{{
				Number:  14,
				Summary: "still open",
				Display: DisplaySubmitted,
				AsOf:    mineRead,
			}},
		},
		{
			name: "this machine's own flagged file is a regression",
			in: mineLocalOf(MineLocal{
				Number:     15,
				LocalID:    "local-run-20261109120000-4242",
				Summary:    "the same bug came back",
				Status:     StatusSubmitted,
				HitAt:      mineDaysAgo(1),
				ResolvedIn: "0.22.0",
				Regression: true,
			}),
			want: []mineWant{{
				Number:  15,
				Summary: "the same bug came back",
				Display: DisplayRegression,
				AsOf:    mineNow,
			}},
		},
		{
			name: "a local file and the shared row of its number are one row",
			in: MineInput{
				Local: []MineLocal{{
					Number:  7,
					LocalID: "local-run-20261109120000-4242",
					Summary: "what this machine wrote",
					Status:  StatusSubmitted,
					HitAt:   mineDaysAgo(2),
					URL:     "https://github.com/dimipaun/fugaro/issues/7",
				}},
				Shared: mineSharedOf(mineResolved("0.22.0")).Shared,
				Now:    mineNow,
			},
			want: []mineWant{{
				Number:  7,
				Summary: "bootstrap failed",
				Display: DisplayResolved,
				AsOf:    mineRead,
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := BuildRows(tc.in)
			mineCheckWants(t, rows, tc.want)
		})
	}
}

func TestMineCoverageCLIAndInstallation(t *testing.T) {
	cases := []struct {
		name                           string
		version, installed, resolvedIn string
		want                           Coverage
	}{
		{
			name:       "both at the fixing release is covered",
			version:    "0.22.1",
			installed:  "0.22.1",
			resolvedIn: "0.22.0",
			want:       CoverageCovered,
		},
		{
			name:       "the fixing release itself counts as covered",
			version:    "0.22.1",
			installed:  "0.22.1",
			resolvedIn: "0.22.1",
			want:       CoverageCovered,
		},
		{
			name:       "a release above the fixing one is covered",
			version:    "0.23.0",
			installed:  "0.22.5",
			resolvedIn: "0.22.0",
			want:       CoverageCovered,
		},
		{
			name:       "a fixing release written with a v prefix is compared",
			version:    "0.22.1",
			installed:  "0.22.1",
			resolvedIn: "v0.22.0",
			want:       CoverageCovered,
		},
		{
			name:       "a CLI below the fixing release is behind",
			version:    "0.22.0",
			installed:  "0.22.1",
			resolvedIn: "0.22.1",
			want:       CoverageBehind,
		},
		{
			name:       "a CLI that is fine does not cover an installation that is not",
			version:    "0.22.2",
			installed:  "0.22.0",
			resolvedIn: "0.22.1",
			want:       CoverageBehind,
		},
		{
			name:       "both below the fixing release is behind",
			version:    "0.22.0",
			installed:  "0.22.0",
			resolvedIn: "0.22.1",
			want:       CoverageBehind,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := mineCoverageInput(tc.version, tc.installed, tc.resolvedIn)
			if got := mineCoverageOf(t, in); got != tc.want {
				t.Errorf("Coverage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMineCoverageInstallationUnknownIsCLIOnly(t *testing.T) {
	in := mineCoverageInput("0.22.1", "", "0.22.0")
	if got := mineCoverageOf(t, in); got != CoverageCLIOnly {
		t.Errorf("coverage with no installation record = %q, want %q", got, CoverageCLIOnly)
	}
	in = mineCoverageInput("0.22.1", "", "0.22.1")
	if got := mineCoverageOf(t, in); got != CoverageCLIOnly {
		t.Errorf("coverage at the fixing release with no installation record = %q, want %q", got, CoverageCLIOnly)
	}
	// The CLI alone decides, so a CLI below the fixing release is behind and
	// not covered-by-the-CLI-alone (F5, O4).
	in = mineCoverageInput("0.22.0", "", "0.22.1")
	if got := mineCoverageOf(t, in); got != CoverageBehind {
		t.Errorf("coverage with no installation record and an older CLI = %q, want %q", got, CoverageBehind)
	}
}

func TestMineCoverageDevBuildIsUnknown(t *testing.T) {
	for _, version := range []string{"dev", "", "0.22.1-rc1", "main"} {
		in := mineCoverageInput(version, "0.22.1", "0.22.0")
		if got := mineCoverageOf(t, in); got != CoverageUnknown {
			t.Errorf("coverage of CLI %q = %q, want %q", version, got, CoverageUnknown)
		}
	}
	// A fixing release that is not a release, or none at all, cannot be
	// compared to anything: a resolved row that names one is unknown rather
	// than fixed (F11).
	for _, resolvedIn := range []string{"", "0.22.x", "0.21", "release-0.22.0"} {
		in := mineCoverageInput("0.22.1", "0.22.1", resolvedIn)
		if got := mineCoverageOf(t, in); got != CoverageUnknown {
			t.Errorf("coverage of a row whose fixing release is %q = %q, want %q", resolvedIn, got, CoverageUnknown)
		}
	}
	// An installation record that names something that is not a release is
	// not known to be at or above the fix, so the row is behind and the
	// nudge is the one command that would repair the record too (F17).
	in := mineCoverageInput("0.22.1", "main", "0.22.0")
	if got := mineCoverageOf(t, in); got != CoverageBehind {
		t.Errorf("coverage with an installation release that is not a release = %q, want %q", got, CoverageBehind)
	}
}

func TestMineOrderGroups(t *testing.T) {
	resolved := func(number int, resolvedIn string, resolvedAt *time.Time) MineSharedIssue {
		s := mineResolved(resolvedIn)
		s.Number, s.ResolvedAt = number, resolvedAt
		return s
	}
	regression := func(number int, hitAt *time.Time) MineSharedIssue {
		s := mineResolved("0.22.0")
		// The fix came first and the user hit the bug again after it: the
		// row's last change is the hit, not the old resolution.
		s.Number, s.HitAt, s.Regression = number, hitAt, true
		s.ResolvedAt = mineAt(t, "2026-10-01T12:00:00Z")
		return s
	}
	hit := func(number int, status MineStatus, hitAt *time.Time) MineSharedIssue {
		return MineSharedIssue{Number: number, Summary: "hit", Status: status, HitAt: hitAt}
	}
	six, eight := mineAt(t, "2026-11-02T12:00:00Z"), mineAt(t, "2026-11-04T12:00:00Z")
	in := MineInput{
		Version:   "0.22.0",
		Installed: "0.22.0",
		Now:       mineNow,
		Shared: MineShared{
			AsOf: mineRead,
			Issues: []MineSharedIssue{
				regression(1, mineAt(t, "2026-11-01T12:00:00Z")),
				regression(2, mineAt(t, "2026-11-07T12:00:00Z")),
				resolved(3, "0.22.1", mineAt(t, "2026-11-06T12:00:00Z")),
				resolved(4, "0.22.1", mineAt(t, "2026-11-08T12:00:00Z")),
				hit(6, MineStatusPending, mineAt(t, "2026-11-03T12:00:00Z")),
				hit(7, MineStatusSubmitted, eight),
				hit(8, MineStatusSubmitted, six),
				resolved(9, "0.22.0", mineAt(t, "2026-11-07T12:00:00Z")),
				resolved(11, "0.22.0", mineAt(t, "2026-11-07T12:00:00Z")),
				{
					Number:      10,
					Summary:     "dismissed",
					Status:      MineStatusDismissed,
					HitAt:       mineAt(t, "2026-11-05T12:00:00Z"),
					DismissedAt: mineAt(t, "2026-11-09T12:00:00Z"),
				},
			},
		},
		Local: []MineLocal{{
			LocalID: "local-run-20261109120000-4242",
			Summary: "still only here",
			Status:  StatusPending,
			HitAt:   mineAt(t, "2026-11-05T12:00:00Z"),
		}},
	}
	rows, hidden := BuildRows(in)
	if hidden != 0 {
		t.Fatalf("hidden = %d, want 0: nothing here is old enough to hide", hidden)
	}
	// Regression first (newest hit first), then fixed-not-updated (newest
	// resolution first), then the waiting rows and the filed ones (oldest hit
	// first), and last the finished ones (newest change first), with equal
	// times broken by number.
	want := []int{2, 1, 4, 3, 6, 0, 8, 7, 10, 9, 11}
	got := make([]int, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Number)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("row numbers in order = %v, want %v (0 is the local row)", got, want)
	}
}

func TestMineHidesOldResolvedAndDismissedOnly(t *testing.T) {
	resolved := func(number int, resolvedIn string, resolvedAt *time.Time) MineSharedIssue {
		s := mineResolved(resolvedIn)
		s.Number, s.ResolvedAt = number, resolvedAt
		return s
	}
	in := MineInput{
		Version:   "0.22.0",
		Installed: "0.22.0",
		Now:       mineNow,
		Shared: MineShared{AsOf: mineRead, Issues: []MineSharedIssue{
			resolved(1, "0.22.0", mineDaysAgo(31)),
			resolved(2, "0.22.0", mineDaysAgo(30)),
			resolved(3, "0.22.1", mineDaysAgo(31)),
			{
				Number:      4,
				Summary:     "dismissed long ago",
				Status:      MineStatusDismissed,
				HitAt:       mineDaysAgo(90),
				DismissedAt: mineDaysAgo(40),
			},
			{
				Number:  5,
				Summary: "filed",
				Status:  MineStatusSubmitted,
				HitAt:   mineDaysAgo(90),
			},
			{
				Number:  6,
				Summary: "pending",
				Status:  MineStatusPending,
				HitAt:   mineDaysAgo(90),
			},
		}},
		Local: []MineLocal{{
			LocalID: "local-run-20261109120000-4242",
			Summary: "still only here",
			Status:  StatusPending,
			HitAt:   mineDaysAgo(90),
		}},
	}
	rows, hidden := BuildRows(in)
	if hidden != 2 {
		t.Fatalf("hidden = %d, want 2", hidden)
	}
	got := make(map[int]bool, len(rows))
	var local bool
	for _, r := range rows {
		if r.Number == 0 {
			local = true
			continue
		}
		got[r.Number] = true
	}
	for _, number := range []int{2, 3, 5, 6} {
		if !got[number] {
			t.Errorf("row %d is missing from the list", number)
		}
	}
	for _, number := range []int{1, 4} {
		if got[number] {
			t.Errorf("row %d is more than 30 days finished and should be hidden", number)
		}
	}
	if !local {
		t.Error("the local row is missing from the list")
	}
}

func TestMineNeverHidesBehindOrRegressionOrUndated(t *testing.T) {
	behind := mineResolved("0.22.1")
	behind.Number, behind.ResolvedAt = 1, mineDaysAgo(90)
	flagged := mineResolved("0.22.0")
	flagged.Number, flagged.HitAt, flagged.Regression = 2, mineDaysAgo(90), true
	undated := mineResolved("0.22.0")
	undated.Number, undated.ResolvedAt = 3, nil
	in := MineInput{
		Version:   "0.22.0",
		Installed: "0.22.0",
		Now:       mineNow,
		Shared:    MineShared{AsOf: mineRead, Issues: []MineSharedIssue{behind, flagged, undated}},
		Local: []MineLocal{{
			Number:  4,
			LocalID: "local-run-20261109120000-4242",
			Summary: "dismissed with no time",
			Status:  StatusDismissed,
			HitAt:   mineDaysAgo(90),
		}},
	}
	rows, hidden := BuildRows(in)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0: only a finished row with such a time is hidden", hidden)
	}
	if len(rows) != 4 {
		t.Errorf("BuildRows gave %d rows, want 4", len(rows))
	}
}

func TestMineAllShowsEverything(t *testing.T) {
	resolved := mineResolved("0.22.0")
	resolved.Number, resolved.HitAt, resolved.ResolvedAt = 1, mineDaysAgo(90), mineDaysAgo(90)
	dismissed := MineSharedIssue{
		Number:      2,
		Summary:     "dismissed long ago",
		Status:      MineStatusDismissed,
		HitAt:       mineDaysAgo(90),
		DismissedAt: mineDaysAgo(60),
	}
	in := MineInput{
		Version:   "0.22.1",
		Installed: "0.22.1",
		Now:       mineNow,
		All:       true,
		Shared:    MineShared{AsOf: mineRead, Issues: []MineSharedIssue{resolved, dismissed}},
	}
	rows, hidden := BuildRows(in)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0: --all hides nothing", hidden)
	}
	if len(rows) != 2 {
		t.Fatalf("BuildRows gave %d rows, want 2", len(rows))
	}
	if rows[0].Display != DisplayDismissed || rows[1].Display != DisplayResolved {
		t.Errorf("--all rows = %q, %q: want the finished ones still ordered newest change first", rows[0].Display, rows[1].Display)
	}
}

func TestMineHiddenCount(t *testing.T) {
	resolved := mineResolved("0.22.0")
	resolved.Number, resolved.ResolvedAt = 1, mineDaysAgo(31)
	dismissed := MineSharedIssue{
		Number:      2,
		Summary:     "dismissed long ago",
		Status:      MineStatusDismissed,
		HitAt:       mineDaysAgo(90),
		DismissedAt: mineDaysAgo(31),
	}
	behind := mineResolved("0.22.1")
	behind.Number, behind.ResolvedAt = 3, mineDaysAgo(31)
	in := MineInput{
		Version:   "0.22.0",
		Installed: "0.22.0",
		Now:       mineNow,
		Shared: MineShared{
			AsOf:   mineRead,
			Issues: []MineSharedIssue{resolved, dismissed, behind},
		},
	}
	rows, hidden := BuildRows(in)
	if hidden != 2 {
		t.Errorf("hidden = %d, want 2: a behind row is never hidden, so it is not counted", hidden)
	}
	if len(rows) != 1 || rows[0].Number != 3 {
		t.Errorf("BuildRows gave %d rows, want only the behind one", len(rows))
	}
}
