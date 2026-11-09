package issues

import (
	"strings"
	"testing"
	"time"
)

// sectionNow is the clock the section's tests read: one instant, so an age is
// a whole number of days and the 24-hour window is unambiguous.
var sectionNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// sectionDay is one day, for the ages the rows below are built from.
const sectionDay = 24 * time.Hour

// sectionRow is one row of a section test.
func sectionRow(number int, status Status, class Class, age time.Duration, reports int, summary string) SectionRow {
	return SectionRow{
		Number:  number,
		LocalID: "local-deploy-20261101000000-1234",
		Class:   class,
		Status:  status,
		Summary: summary,
		Created: sectionNow.Add(-age),
		Reports: reports,
	}
}

// sectionBlocks cuts the section at its list headings and returns each block's
// own lines, in the order the blocks print, so a test asserts an order rather
// than a whole paragraph.
func sectionBlocks(t *testing.T, out string) ([]string, map[string][]string) {
	t.Helper()
	var order []string
	blocks := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "## "):
		case strings.HasPrefix(line, "- "):
			if len(order) == 0 {
				t.Fatalf("a line stands outside a block: %q", line)
			}
			last := order[len(order)-1]
			blocks[last] = append(blocks[last], line)
		case strings.HasSuffix(line, ":"):
			order = append(order, line)
		default:
			t.Fatalf("unexpected line %q", line)
		}
	}
	return order, blocks
}

// lineWith is the one line of a block that names ref.
func lineWith(t *testing.T, lines []string, ref string) string {
	t.Helper()
	var found []string
	for _, line := range lines {
		if strings.Contains(line, ref) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d lines of the block name %s, want 1: %q", len(found), ref, lines)
	}
	return found[0]
}

func TestReportSectionOrdersAndCounts(t *testing.T) {
	rows := []SectionRow{
		sectionRow(3, StatusPending, ClassFugaro, 10*sectionDay, 1, "the oldest pending"),
		sectionRow(7, StatusPending, ClassUnclear, sectionDay, 4, "the newest pending"),
		sectionRow(5, StatusSubmitted, ClassFugaro, 3*sectionDay, 4, "already filed"),
		sectionRow(9, StatusDismissed, ClassUnclear, 40*sectionDay, 1, "set aside"),
	}
	// A local issue has no number yet: it is named, and submitted, by its
	// local ID (F23, D1723).
	local := sectionRow(0, StatusPending, ClassFugaro, 5*sectionDay, 1, "the middle pending")
	rows = append(rows, local)

	out := Section(rows, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	order, blocks := sectionBlocks(t, out)
	if len(order) != 3 {
		t.Fatalf("the section has %d blocks, want 3: %q", len(order), order)
	}

	// New is the last 24 hours and nothing else: #0005 is three days old, and
	// #0009 is neither new, nor pending, nor reported twice, so it prints
	// nowhere at all.
	fresh := blocks[order[0]]
	if len(fresh) != 1 {
		t.Errorf("the new block has %d lines, want 1: %q", len(fresh), fresh)
	}
	if line := lineWith(t, fresh, "#0007"); !strings.Contains(line, "(unclear)") || !strings.Contains(line, "the newest pending") {
		t.Errorf("the new line = %q, want the classification and the summary", line)
	}
	if strings.Contains(out, "#0009") {
		t.Errorf("a dismissed, once-reported, old issue is printed:\n%s", out)
	}

	// Pending is oldest first, and each line carries the command that files it.
	pending := blocks[order[1]]
	wantOrder := []string{"#0003", local.LocalID, "#0007"}
	if len(pending) != len(wantOrder) {
		t.Fatalf("the pending block has %d lines, want %d: %q", len(pending), len(wantOrder), pending)
	}
	for idx, want := range wantOrder {
		if !strings.Contains(pending[idx], want) {
			t.Errorf("pending line %d = %q, want it to name %s", idx+1, pending[idx], want)
		}
	}
	for _, tc := range []struct{ ref, command string }{
		{"#0003", "fugaro issue submit 3"},
		{local.LocalID, "fugaro issue submit " + local.LocalID},
		{"#0007", "fugaro issue submit 7"},
	} {
		if line := lineWith(t, pending, tc.ref); !strings.Contains(line, tc.command) {
			t.Errorf("the line of %s = %q, want it to carry %q", tc.ref, line, tc.command)
		}
	}

	// Recurring is by how many times the issue was reported, most first, and
	// a tie is broken by the name so the section is stable.
	recurring := blocks[order[2]]
	if len(recurring) != 2 {
		t.Fatalf("the recurring block has %d lines, want 2: %q", len(recurring), recurring)
	}
	for idx, want := range []string{"#0005", "#0007"} {
		if !strings.Contains(recurring[idx], want) {
			t.Errorf("recurring line %d = %q, want it to name %s", idx+1, recurring[idx], want)
		}
	}
	if line := lineWith(t, recurring, "#0007"); !strings.Contains(line, "reported 4 times") {
		t.Errorf("the recurring line of #0007 = %q, want the count of reports", line)
	}
}

func TestReportSectionNudgeFirmer(t *testing.T) {
	rows := []SectionRow{
		sectionRow(1, StatusPending, ClassFugaro, 3*sectionDay, 1, "young"),
		sectionRow(2, StatusPending, ClassFugaro, 9*sectionDay, 1, "old"),
		sectionRow(3, StatusSubmitted, ClassFugaro, 3*sectionDay, 2, "twice"),
		sectionRow(4, StatusSubmitted, ClassFugaro, 3*sectionDay, 5, "many"),
	}
	// The thresholds are the setting's, so the same rows are read twice: once
	// at the defaults and once at a threshold nothing reaches.
	_, firm := sectionBlocks(t, Section(rows, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3}))
	_, calm := sectionBlocks(t, Section(rows, sectionNow, Thresholds{AfterDays: 30, Recurrence: 30}))
	pending, recurring := "Pending, oldest first:", "Recurring, most reported first:"

	youngCalm, oldCalm := lineWith(t, calm[pending], "#0001"), lineWith(t, calm[pending], "#0002")
	if strings.Contains(youngCalm, "please file it") {
		t.Errorf("a three-day-old pending issue is already firm: %q", youngCalm)
	}
	if oldFirm := lineWith(t, firm[pending], "#0002"); !strings.Contains(oldFirm, "please file it") {
		t.Errorf("a nine-day-old pending issue is not firm: %q", oldFirm)
	}
	// The firmer line is the calm one with the clause in it and nothing else
	// changed, and the line under the threshold is the same under both.
	if got, want := lineWith(t, firm[pending], "#0002"), strings.Replace(oldCalm, ":", ", please file it:", 1); got != want {
		t.Errorf("the firm line = %q, want %q", got, want)
	}
	if got, want := lineWith(t, firm[pending], "#0001"), youngCalm; got != want {
		t.Errorf("the calm line = %q, want %q", got, want)
	}

	twiceCalm, manyCalm := lineWith(t, calm[recurring], "#0003"), lineWith(t, calm[recurring], "#0004")
	if strings.Contains(twiceCalm, "it keeps happening") {
		t.Errorf("an issue reported twice is already firm: %q", twiceCalm)
	}
	if manyFirm := lineWith(t, firm[recurring], "#0004"); !strings.Contains(manyFirm, "it keeps happening") {
		t.Errorf("an issue reported five times is not firm: %q", manyFirm)
	}
	if got, want := lineWith(t, firm[recurring], "#0004"), strings.Replace(manyCalm, ":", ", it keeps happening:", 1); got != want {
		t.Errorf("the firm line = %q, want %q", got, want)
	}
	if got, want := lineWith(t, firm[recurring], "#0003"), twiceCalm; got != want {
		t.Errorf("the calm line = %q, want %q", got, want)
	}

	// A threshold under every age and count firms both kinds of line.
	_, low := sectionBlocks(t, Section(rows, sectionNow, Thresholds{AfterDays: 1, Recurrence: 1}))
	if line := lineWith(t, low[pending], "#0001"); !strings.Contains(line, "please file it") {
		t.Errorf("a lowered threshold leaves a pending line calm: %q", line)
	}
	if line := lineWith(t, low[recurring], "#0003"); !strings.Contains(line, "it keeps happening") {
		t.Errorf("a lowered threshold leaves a recurring line calm: %q", line)
	}
}

func TestReportSectionEmptyIsOneLine(t *testing.T) {
	const want = "No pending issues.\n"
	for _, tc := range []struct {
		name string
		rows []SectionRow
	}{
		{"no issue at all", nil},
		{"no pending issue", []SectionRow{
			sectionRow(1, StatusSubmitted, ClassFugaro, time.Hour, 1, "filed"),
			sectionRow(2, StatusDismissed, ClassUnclear, 2*time.Hour, 3, "set aside"),
		}},
	} {
		got := Section(tc.rows, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
		if got != want {
			t.Errorf("%s: the section = %q, want %q", tc.name, got, want)
		}
	}
}

func TestReportSectionPrintsSummaryAsData(t *testing.T) {
	// A summary is the reporter's own text, and it reaches a terminal: an
	// escape sequence, a bidi override and a line break in it are data and
	// nothing more (F6).
	hostile := "run this: \x1b[31mred\x1b[0m \u202eversi\u202c\n- #9999 (fugaro) forged"
	rows := []SectionRow{
		sectionRow(1, StatusPending, ClassFugaro, time.Hour, 1, hostile),
		{
			LocalID: "local-deploy-20261101000000-1234\x1b[2J",
			Class:   ClassUnclear,
			Status:  StatusPending,
			Summary: "a hostile local ID",
			Created: sectionNow.Add(-2 * time.Hour),
			Reports: 1,
		},
	}
	out := Section(rows, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	for _, bad := range []string{"\x1b", "\u202e", "\u202c"} {
		if strings.Contains(out, bad) {
			t.Errorf("the section carries %q:\n%q", bad, out)
		}
	}
	if !strings.Contains(out, "run this:") {
		t.Errorf("the summary is not printed at all:\n%s", out)
	}
	// The forged bullet is inside the summary's own line and does not become
	// one: the section prints exactly the blocks and the lines it has.
	_, blocks := sectionBlocks(t, out)
	if got := len(blocks["Pending, oldest first:"]); got != 2 {
		t.Errorf("the pending block has %d lines, want 2:\n%s", got, out)
	}
}
