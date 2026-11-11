package gitprov

import (
	"strings"
	"testing"
)

func TestReportMarkerRoundTrip(t *testing.T) {
	const id = "20260930-101500-0a1b"
	m := ReportMarker(id)
	if m != "<!-- fugaro:report run=20260930-101500-0a1b -->" {
		t.Fatalf("ReportMarker = %q", m)
	}
	if got, ok := FugaroRun("Some report.\n" + m + "\n"); !ok || got != id {
		t.Fatalf("FugaroRun(report with marker) = %q, %v", got, ok)
	}
}

func TestFugaroRunRecognizesMarkerAndLegacyHeading(t *testing.T) {
	const id = "20260925-000000-0000"
	for _, c := range []struct {
		name, body string
		want       string
		ok         bool
	}{
		{"marker at the end", "### Report\n\nbody\n" + ReportMarker(id) + "\n", id, true},
		{"marker in the middle", "before " + ReportMarker(id) + " after", id, true},
		{"legacy heading at the start", "### Fugaro run `" + id + "`\n\n**Outcome:** ready for review\n", id, true},
		{"legacy heading after leading blank lines", "\n\n### Fugaro run `" + id + "`\n", id, true},
		{"legacy heading not at the start", "Looks good.\n\n### Fugaro run `" + id + "`\n", "", false},
		{"legacy not-ready note", "**Fugaro:** this pull request is not ready. The run could not finish setting it up (boom).", "", true},
		{"not-ready wording quoted in prose", "Why did it say **Fugaro:** this pull request is not ready?", "", false},
		{"prose quoting a run ID", "Run " + id + " broke the build; see fugaro:report run=" + id, "", false},
		{"a marker with a malformed run ID", "<!-- fugaro:report run=../../x -->", "", false},
		{"nothing", "LGTM", "", false},
	} {
		got, ok := FugaroRun(c.body)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: FugaroRun = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestMarkerRoundTripEscalation pins the ladder's own marker kind (E8, E10a):
// a transition comment is Fugaro's, it names the run and the rung, and a body
// that quotes one cannot pass it off as another run's.
func TestMarkerRoundTripEscalation(t *testing.T) {
	const id = "20261111-172557-dabe"
	m := TransitionMarker(id, "R2")
	if m != "<!-- fugaro:escalation run=20261111-172557-dabe rung=R2 -->" {
		t.Fatalf("TransitionMarker = %q", m)
	}
	body := "R1 → R2: churn_same at deadbeef.\n\n" + m + "\n"
	if run, rung, ok := TransitionOf(body); !ok || run != id || rung != "R2" {
		t.Fatalf("TransitionOf = %q, %q, %v", run, rung, ok)
	}
	if got, ok := FugaroRun(body); !ok || got != id {
		t.Fatalf("FugaroRun(transition) = %q, %v; a transition comment is Fugaro's own", got, ok)
	}
	if got := StripMarkers(body); strings.Contains(got, "fugaro:") || strings.Contains(got, id) {
		t.Fatalf("StripMarkers left %q", got)
	}
	// A rung the ladder has no name for is not a transition, whatever else the
	// comment looks like.
	for _, bad := range []string{
		"<!-- fugaro:escalation run=" + id + " rung=R9 -->",
		"<!-- fugaro:escalation run=" + id + " -->",
		"<!-- fugaro:escalation run=../../x rung=R2 -->",
		"<!-- fugaro:escalation run=" + id + " rung =R2 -->",
	} {
		if _, _, ok := TransitionOf(bad); ok {
			t.Errorf("TransitionOf(%q) read a marker it should not", bad)
		}
	}
}

// TestFugaroRunLastMarkerWinsAcrossKinds covers the rule that the last marker
// of either kind decides which run a body names: a report quoting a transition
// marker earlier must not be read as that transition's.
func TestFugaroRunLastMarkerWinsAcrossKinds(t *testing.T) {
	const mine = "20261111-172557-dabe"
	body := "quoted: " + TransitionMarker("20260101-000000-dead", "R3") + "\n\n" + ReportMarker(mine) + "\n"
	if got, ok := FugaroRun(body); !ok || got != mine {
		t.Fatalf("FugaroRun = %q, %v; want the last marker's run", got, ok)
	}
	// And the other way round: the transition written last is the one that
	// names the body.
	body = "quoted: " + ReportMarker("20260101-000000-dead") + "\n\n" + TransitionMarker(mine, "R4") + "\n"
	if got, ok := FugaroRun(body); !ok || got != mine {
		t.Fatalf("FugaroRun = %q, %v; want the last marker's run", got, ok)
	}
	// A transition marker's rung is what TransitionOf reads, and it is the
	// LAST one: an item that moved twice names where it ended.
	body = TransitionMarker(mine, "R1") + "\n" + TransitionMarker(mine, "R3")
	if _, rung, ok := TransitionOf(body); !ok || rung != "R3" {
		t.Fatalf("TransitionOf = %q, %v; want R3", rung, ok)
	}
}

func TestFugaroRunLastMarkerWins(t *testing.T) {
	// An agent's text quoted earlier in a report can't name another run:
	// Fugaro's own marker always comes last.
	body := "### Fugaro run `20260925-000000-0000`\n\nquoted: " + ReportMarker("20260101-000000-dead") + "\n\n" + ReportMarker("20260930-101500-0a1b") + "\n"
	if got, ok := FugaroRun(body); !ok || got != "20260930-101500-0a1b" {
		t.Fatalf("FugaroRun = %q, %v", got, ok)
	}
	// A marker also beats the legacy not-ready opening.
	note := "**Fugaro:** this pull request is not ready.\n" + ReportMarker("20260930-101500-0a1b") + "\n"
	if got, ok := FugaroRun(note); !ok || got != "20260930-101500-0a1b" {
		t.Fatalf("FugaroRun(note) = %q, %v", got, ok)
	}
}

func TestStripMarkers(t *testing.T) {
	in := strings.Join([]string{
		"I fixed the null check.",
		"### Fugaro run `20260101-000000-dead`",
		"   ### Fugaro run `20260101-000000-beef`",
		"Declined the rename: " + ReportMarker("20260101-000000-dead") + " done.",
		"<!--   FUGARO:report run=anything -->",
		"<!-- fugaro:report\nrun=20260101-000000-dead -->",
		"### Fugaro runs, a heading variant, goes too",
		"#### Fugaro run notes stay",
		"Reassembled: <!-<!-- fugaro:report -->- fugaro:report run=20260101-000000-dead --> end",
	}, "\n")
	got := StripMarkers(in)
	if strings.Contains(got, "<!--") {
		t.Errorf("stripped text still holds a comment opener:\n%s", got)
	}
	if _, ok := FugaroRun(got); ok {
		t.Fatalf("stripped text is still recognized:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "### Fugaro run") {
			t.Errorf("stripped text still holds the heading line %q", l)
		}
	}
	for _, gone := range []string{"fugaro:report", "FUGARO:report", "dead", "beef"} {
		if strings.Contains(got, gone) {
			t.Errorf("stripped text still holds %q:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"I fixed the null check.", "Declined the rename:  done.", "#### Fugaro run notes stay"} {
		if !strings.Contains(got, kept) {
			t.Errorf("stripped text lost %q:\n%s", kept, got)
		}
	}
}

func TestSameCommit(t *testing.T) {
	const full = "0123456789abcdef0123456789abcdef01234567"
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{full, full, true},
		{full, full[:12], true},
		{full[:12], full, true},
		{full, full[:7], true},
		{strings.ToUpper(full[:12]), full, true},
		{full[:12], "0123456789ab", true},
		{full[:12], "0123456789ac", false},
		{full, "fedcba9876543210fedcba9876543210fedcba98", false},
		{full, full[:6], false},
		{full[:6], full[:6], false},
		{"", "", false},
		{full, "", false},
		{"0123456789abcdeg", full, false},
	} {
		if got := SameCommit(c.a, c.b); got != c.want {
			t.Errorf("SameCommit(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestReportRunIsNarrowerThanFugaroRun is the split the runner needs: a report
// and a ladder transition are both Fugaro's own and both name a run, but only
// one of them answers "has this run posted its report". A transition comment
// can name a run that has posted nothing (E10a writes it before the run
// finishes, if it finishes at all), and reading it as that run's report would
// silently suppress the report the pull request is waiting for.
func TestReportRunIsNarrowerThanFugaroRun(t *testing.T) {
	const mine = "20261111-172557-dabe"
	for _, c := range []struct {
		name, body     string
		wantRun        string
		wantReport     bool
		wantFugaroOwns bool
	}{
		{"the run's own report", "### Report\n\nbody\n" + ReportMarker(mine) + "\n", mine, true, true},
		{"a report quoting a transition marker", "quoted: " + TransitionMarker(mine, "R2") + "\n\n" + ReportMarker(mine) + "\n", mine, true, true},
		{"a transition comment naming the run", "R1 → R2: churn_same.\n\n" + TransitionMarker(mine, "R2") + "\n", "", false, true},
		{"the legacy report heading", "### Fugaro run `" + mine + "`\n\n**Outcome:** ready\n", mine, true, true},
		{"the legacy not-ready note", "**Fugaro:** this pull request is not ready.\n", "", true, true},
		{"somebody else's comment", "LGTM\n", "", false, false},
		{"a transition marker alone, with no report", TransitionMarker(mine, "R4"), "", false, true},
	} {
		run, ok := ReportRun(c.body)
		if run != c.wantRun || ok != c.wantReport {
			t.Errorf("%s: ReportRun = %q, %v; want %q, %v", c.name, run, ok, c.wantRun, c.wantReport)
		}
		if _, owns := FugaroRun(c.body); owns != c.wantFugaroOwns {
			t.Errorf("%s: FugaroRun ok = %v, want %v", c.name, owns, c.wantFugaroOwns)
		}
	}
}
