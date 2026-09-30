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
