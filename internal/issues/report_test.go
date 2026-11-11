package issues

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/updatecheck"
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

// sectionResolved is one fixed row of a section test: the release that shipped
// the fix (its milestone, F4) and who ran into it, as the caller read them from
// the shared store.
func sectionResolved(number int, release string, age time.Duration, reports int, hitBy, summary string) SectionRow {
	row := sectionRow(number, StatusResolved, ClassFugaro, age, reports, summary)
	row.ResolvedIn, row.HitBy = release, hitBy
	return row
}

// sectionVersions is the window every test in this file reads: an installation
// that updated from 0.21.1 to 0.22.0 and is running 0.22.0, which is the
// release it just installed.
func sectionVersions() Versions {
	return Versions{Previous: "0.21.1", Release: "0.22.0", Running: "0.22.0"}
}

// sectionBlocks cuts the section at its list headings and returns each block's
// own lines, in the order the blocks print, so a test asserts an order rather
// than a whole paragraph. notes are the section's own lines that stand outside
// a block -- the upgrade nudge -- which are what a test about them reads.
func sectionBlocks(t *testing.T, out string) (order []string, blocks map[string][]string, notes []string) {
	t.Helper()
	blocks = map[string][]string{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "## "):
		case strings.HasPrefix(line, "- "):
			if len(order) == 0 {
				t.Fatalf("a line stands outside a block: %q", line)
			}
			last := order[len(order)-1]
			blocks[last] = append(blocks[last], line)
		case strings.HasPrefix(line, sectionUpgradeLead):
			notes = append(notes, line)
		case strings.HasSuffix(line, ":"):
			order = append(order, line)
		default:
			t.Fatalf("unexpected line %q", line)
		}
	}
	return order, blocks, notes
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

	out := Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	order, blocks, _ := sectionBlocks(t, out)
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
	_, firm, _ := sectionBlocks(t, Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3}))
	_, calm, _ := sectionBlocks(t, Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 30, Recurrence: 30}))
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
	_, low, _ := sectionBlocks(t, Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 1, Recurrence: 1}))
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
		got := Section(tc.rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
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
	out := Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
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
	_, blocks, _ := sectionBlocks(t, out)
	if got := len(blocks["Pending, oldest first:"]); got != 2 {
		t.Errorf("the pending block has %d lines, want 2:\n%s", got, out)
	}
}

// resHead is the heading of the section's Resolved list.
const resHead = "Resolved since the last update:"

// The Resolved list is the issues the installation's window fixed: a release at
// or below the one the last update applied and above the one before it (F17,
// D1754, D1757). The other resolved rows are real and stay out of the list: one
// the update before this one fixed, one fixed earlier still, one fixed by a
// release nobody has installed yet, and one a launcher resolved without naming
// a release at all.
func TestReportSectionListsResolvedInWindow(t *testing.T) {
	rows := []SectionRow{
		sectionResolved(11, "0.22.0", 5*sectionDay, 1, "hit by Ana", "fixed by this update"),
		sectionResolved(12, "0.21.1", 5*sectionDay, 1, "hit by Bo", "fixed by the update before"),
		sectionResolved(13, "0.21.0", 5*sectionDay, 1, "hit by Cy", "fixed earlier still"),
		sectionResolved(14, "0.23.0", 5*sectionDay, 1, "hit by Di", "fixed by a release nobody installed"),
		sectionResolved(15, "", 5*sectionDay, 1, "hit by Ed", "resolved with no release"),
	}

	out := Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	order, blocks, _ := sectionBlocks(t, out)
	if len(order) != 1 || order[0] != resHead {
		t.Fatalf("the section's blocks = %q, want the Resolved one alone", order)
	}
	lines := blocks[resHead]
	if len(lines) != 1 {
		t.Fatalf("the Resolved block has %d lines, want 1: %q", len(lines), lines)
	}
	for _, want := range []string{"#0011", "fixed in 0.22.0", "hit by Ana", "fixed by this update"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the Resolved line = %q, want it to name %q", lines[0], want)
		}
	}
	for _, number := range []string{"#0012", "#0013", "#0014", "#0015"} {
		if strings.Contains(out, number) {
			t.Errorf("a resolved issue outside the window is printed (%s):\n%s", number, out)
		}
	}

	// A machine with no record has no window, and one that cannot be read reads
	// the same: the list is omitted rather than guessed at (F17).
	for _, tc := range []struct {
		name string
		v    Versions
	}{
		{"no record", Versions{Running: "0.22.0"}},
		{"a record past every release", Versions{Previous: "", Release: "", Running: "0.22.0"}},
	} {
		out := Section(rows, tc.v, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
		if out != noPendingLine+"\n" {
			t.Errorf("%s: the section = %q, want the one line %q", tc.name, out, noPendingLine)
		}
	}
}

// The list is the update's window on the team's store, and the section nags a
// machine still running a release older than a fix it lists (F19): the report
// is what reaches a reporter between updates, so it names the one command that
// brings them the fix. A release the machine already has is no nag, and neither
// is a build that is not a release at all.
func TestReportSectionNudgesOlderCLI(t *testing.T) {
	rows := []SectionRow{
		sectionResolved(11, "0.21.2", 5*sectionDay, 1, "hit by Ana", "fixed by a patch"),
		sectionResolved(12, "0.22.0", 5*sectionDay, 1, "hit by Bo", "fixed by the release"),
	}
	nudge := "Upgrade to 0.22.0 for these fixes: " + updatecheck.BrewCommand + ", then fugaro update."
	for _, tc := range []struct {
		name    string
		running string
		want    string
	}{
		{"an older patch", "0.21.5", nudge},
		{"an older minor", "0.20.9", nudge},
		{"the release the list names", "0.22.0", ""},
		{"a newer release", "0.22.1", ""},
		{"a dev build", "dev", ""},
	} {
		v := sectionVersions()
		v.Running = tc.running
		_, _, notes := sectionBlocks(t, Section(rows, v, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3}))
		if len(notes) == 0 && tc.want == "" {
			continue
		}
		if len(notes) != 1 || notes[0] != tc.want {
			t.Errorf("%s: the section's upgrade lines = %q, want %q", tc.name, notes, tc.want)
		}
	}

	// The nag names the highest release the list holds, which is what the
	// machine is missing and not the newest one of the window it already has.
	rows = append(rows, sectionResolved(13, "0.22.0", 5*sectionDay, 1, "", "fixed by the same release"))
	v := sectionVersions()
	v.Running = "0.21.5"
	_, _, notes := sectionBlocks(t, Section(rows, v, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3}))
	if len(notes) != 1 || !strings.Contains(notes[0], "Upgrade to 0.22.0 ") {
		t.Errorf("the upgrade line = %q, want it to name 0.22.0", notes)
	}
}

// A fixed issue is not a bug that keeps happening: the Recurring list is about
// the ones still out there, so a resolved row leaves it even when it was
// reported many times (F21). It is listed where a person reads what the
// installation fixed.
func TestReportRecurringExcludesResolved(t *testing.T) {
	rows := []SectionRow{
		sectionRow(21, StatusSubmitted, ClassFugaro, 3*sectionDay, 4, "still out there"),
		sectionResolved(22, "0.22.0", 3*sectionDay, 4, "hit by Ana", "fixed and reported four times"),
	}

	out := Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	_, blocks, _ := sectionBlocks(t, out)
	recurring := blocks["Recurring, most reported first:"]
	if len(recurring) != 1 || !strings.Contains(recurring[0], "#0021") {
		t.Errorf("the Recurring block = %q, want only the issue still out there", recurring)
	}
	fixed := blocks[resHead]
	if len(fixed) != 1 || !strings.Contains(fixed[0], "#0022") {
		t.Errorf("the Resolved block = %q, want the fixed issue", fixed)
	}
}

// An installation with no record to read — one that has never updated with a
// build that writes one, or one whose record nobody can read — lists nothing:
// without a window there is no answer to which fixes are new (F17). The other
// blocks are what they were, and the nag lines stay away with the list.
func TestReportSectionNoRecordOmitsResolved(t *testing.T) {
	rows := []SectionRow{
		sectionRow(31, StatusPending, ClassFugaro, 3*sectionDay, 1, "not filed yet"),
		sectionResolved(32, "0.22.0", 3*sectionDay, 1, "hit by Ana", "fixed by a release"),
	}

	out := Section(rows, Versions{Running: "0.21.0"}, sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	if strings.Contains(out, "Resolved") || strings.Contains(out, sectionUpgradeLead) {
		t.Errorf("a section with no record lists fixed issues or nags:\n%s", out)
	}
	for _, want := range []string{"#0031", "not filed yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("the section without a record does not print %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#0032") {
		t.Errorf("a fixed issue is printed with no window to read it in:\n%s", out)
	}
}

// A fixed issue's line carries text from the store like every other line: the
// reporter's summary and the names of the people who hit it are data, printed
// through safetext and never as lines of their own (F6, F16a).
func TestReportResolvedPrintsSummaryAsData(t *testing.T) {
	hostile := "run this: \x1b[31mred\x1b[0m \u202eversi\u202c\n- #9999 (fugaro) forged"
	rows := []SectionRow{
		sectionResolved(41, "0.22.0", 3*sectionDay, 1, "hit by \x1b[31mAna\x1b[0m\n- #9999 (fugaro) forged", hostile),
	}

	out := Section(rows, sectionVersions(), sectionNow, Thresholds{AfterDays: 7, Recurrence: 3})
	for _, bad := range []string{"\x1b", "\u202e", "\u202c"} {
		if strings.Contains(out, bad) {
			t.Errorf("the section carries %q:\n%q", bad, out)
		}
	}
	if !strings.Contains(out, "run this:") || !strings.Contains(out, "hit by Ana") {
		t.Errorf("the fixed issue is not printed at all:\n%s", out)
	}
	// The forged bullet is inside the Resolved line and does not become one.
	_, blocks, _ := sectionBlocks(t, out)
	if got := len(blocks[resHead]); got != 1 {
		t.Errorf("the Resolved block has %d lines, want 1:\n%s", got, out)
	}
}
