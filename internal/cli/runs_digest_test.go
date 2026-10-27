package cli

// fugaro runs digest: one closed period's report of one repository, written
// into the repository's own docs tree. The command is the terminal half of
// D320 (the report run is the other), and these tests hold the four things
// that make it safe to commit what it writes:
//
//   - one repository: a record whose own repository is another is not read
//     into the report, and there is no --all (G2, D322);
//   - closed periods: a period holding today is refused (G4);
//   - determinism: the same files give byte-identical bytes, the index
//     included (G3);
//   - the untrusted folder: a report a person edited by hand is named and
//     skipped, never fatal.
//
// Every fixture here is a placeholder repository of the synthetic
// installation the cloud fixture builds (reconciliation Global Constraint 6).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// digestCheckout makes the working directory a checkout of the fixture's own
// repository, so that "this repository" resolves the way fugaro run resolves
// it: from the checkout's origin, with no --repo.
func digestCheckout(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	t.Chdir(dir)
}

// digestClock pins runs digest's clock to a day, so which period is closed
// does not depend on the day the test happens to run.
func digestClock(t *testing.T, day string) {
	t.Helper()
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	digestNow = func() time.Time { return at }
	t.Cleanup(func() { digestNow = time.Now })
}

// seedDigestRun writes one run's result.json under the fixture repository's
// own slug, finished at fin, with repo as the repository the record itself
// names.
func seedDigestRun(t *testing.T, f *cloudFixture, id string, fin time.Time, repo string) {
	t.Helper()
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: repo, Workflow: "web",
		Status:    runstore.StatusSucceeded,
		StartedAt: fin.Add(-time.Hour), FinishedAt: &fin,
	}
	if err := runstore.Open(statsBucket(t, f), appSlug, id).WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// digestReport reads one report file the command wrote.
func digestReport(t *testing.T, dir, key string) string {
	t.Helper()
	path := filepath.Join(dir, key[:4], key+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the command wrote no report: %v", err)
	}
	return string(data)
}

// TestRunsDigestWritesPeriodFile is the headline: a closed day's report is
// written where D321 puts it, carrying the period's runs and the two sections
// a later report reads.
func TestRunsDigestWritesPeriodFile(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")

	out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir)
	if err != nil {
		t.Fatalf("runs digest: %v (%s)", err, errOut)
	}
	body := digestReport(t, dir, "2026-10-25")
	for _, want := range []string{
		"# Run digest: 2026-10-25",
		"20261025-120000-abcd",
		"## Summary",
		`"period":"2026-10-25"`,
		`"end":"2026-10-25"`,
		"## Patterns",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the report does not carry %q:\n%s", want, body)
		}
	}
	if !strings.Contains(out, filepath.Join(dir, "2026", "2026-10-25.md")) {
		t.Errorf("the command does not say which report it wrote:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(dir, "README.md")) {
		t.Errorf("the command does not say it regenerated the index:\n%s", out)
	}
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("the command regenerated no index: %v", err)
	}
	if !strings.Contains(string(index), "2026/2026-10-25.md") {
		t.Errorf("the index does not link the report:\n%s", index)
	}

	// The report carries no person: no address and no task text reaches a file
	// that is committed (G1, D323).
	for _, absent := range []string{"@", "person-1", "someone"} {
		if strings.Contains(body, absent) {
			t.Errorf("the report carries %q:\n%s", absent, body)
		}
	}

	// --json prints the summary block and the patterns, and writes nothing.
	before, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Summary  digest.Summary   `json:"summary"`
		Patterns []digest.Pattern `json:"patterns"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("%v: %s", err, doc)
	}
	if got.Summary.Period != "2026-10-25" || got.Summary.Runs != 1 || got.Summary.End != "2026-10-25" {
		t.Errorf("--json printed %+v", got.Summary)
	}
	after, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("--json regenerated the index")
	}
}

// TestRunsDigestOneRepositoryOnly is G2: the report is of one repository, and
// a record under its prefix whose own repository is another is not read into
// it. On a project job the job's account reaches every repository's runs/, so
// the record is untrusted input and this check is the guard.
func TestRunsDigestOneRepositoryOnly(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	fin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	seedDigestRun(t, f, "20261025-120000-abcd", fin, "acme/app")
	seedDigestRun(t, f, "20261025-120001-abcd", fin, "owner/repo-b")

	out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--stdout")
	if err != nil {
		t.Fatalf("runs digest: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "20261025-120000-abcd") {
		t.Errorf("the report leaves out this repository's own run:\n%s", out)
	}
	if strings.Contains(out, "20261025-120001-abcd") {
		t.Errorf("the report reads a record of another repository:\n%s", out)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("--stdout wrote %v (err %v), want nothing", entries, err)
	}

	// There is no --all: a report is committed into one repository and never
	// names another (D322).
	if _, _, err := execute(t, "runs", "digest", "--period", "day", "--all"); err == nil {
		t.Error("runs digest accepted --all")
	}
}

// TestRunsDigestRefusesOpenPeriod is G4: a period holding today is still open,
// and today's runs are still arriving.
func TestRunsDigestRefusesOpenPeriod(t *testing.T) {
	digestCheckout(t)
	newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")

	for _, tc := range []struct{ period, date, key string }{
		{"day", "2026-10-27", "2026-10-27"},
		{"week", "2026-10-27", "2026-W44"},
		{"month", "2026-10-27", "2026-10"},
		{"year", "2026-10-27", "2026"},
	} {
		_, _, err := execute(t, "runs", "digest", "--period", tc.period, "--date", tc.date, "--dir", dir)
		if err == nil {
			t.Errorf("--period %s --date %s reported an open period", tc.period, tc.date)
			continue
		}
		if !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), "open") {
			t.Errorf("--period %s --date %s refused with %q, want the open period named", tc.period, tc.date, err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("a refused report wrote %v (err %v), want nothing", entries, err)
	}

	// With no --date the most recent closed period is reported: the day
	// before today, not today.
	seedDigestRun(t, newCloudFixture(t), "20261026-120000-abcd",
		time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), "acme/app")
	if _, _, err := execute(t, "runs", "digest", "--period", "day", "--dir", dir); err != nil {
		t.Fatalf("the default period is not the most recent closed one: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026", "2026-10-26.md")); err != nil {
		t.Errorf("the default period is not yesterday: %v", err)
	}
}

// TestRunsDigestIndexRegenerated pins the index: it lists the files present,
// and the same files give byte-identical bytes (G3), so a regeneration in
// review diffs only where a report changed.
func TestRunsDigestIndexRegenerated(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261024-120000-abcd", time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), "acme/app")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")
	for _, day := range []string{"2026-10-24", "2026-10-25"} {
		if _, _, err := execute(t, "runs", "digest", "--period", "day", "--date", day, "--dir", dir); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(dir, "README.md")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report := digestReport(t, dir, "2026-10-25")
	for _, want := range []string{"2026-10-24", "2026-10-25", "2026/2026-10-24.md", "2026/2026-10-25.md"} {
		if !strings.Contains(string(first), want) {
			t.Errorf("the index does not carry %q:\n%s", want, first)
		}
	}
	if strings.Contains(string(first), "@") {
		t.Errorf("the index carries an address:\n%s", first)
	}

	// Regenerating over the same files rewrites the same bytes, and so does
	// the report itself: nothing here reads a clock (G3).
	if _, _, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) {
		t.Errorf("the index is not byte-identical when regenerated:\n%s\nwant:\n%s", again, first)
	}
	if digestReport(t, dir, "2026-10-25") != report {
		t.Error("the report is not byte-identical when regenerated")
	}
}

// TestRunsDigestSkipsMalformedReport: a report is a file a person may edit by
// hand. One the command cannot read is named -- by its own key, never by
// anything it holds -- and the report being written goes on.
func TestRunsDigestSkipsMalformedReport(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261024-120000-abcd", time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), "acme/app")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")
	for _, day := range []string{"2026-10-24", "2026-10-25"} {
		if _, _, err := execute(t, "runs", "digest", "--period", "day", "--date", day, "--dir", dir); err != nil {
			t.Fatal(err)
		}
	}

	// A hand edit to the committed block: the figure is no longer one this
	// report writes, so the file is no summary at all.
	path := filepath.Join(dir, "2026", "2026-10-24.md")
	body := digestReport(t, dir, "2026-10-24")
	edited := strings.Replace(body, `"runs":1`, `"runs":"many"`, 1)
	if edited == body {
		t.Fatal("the fixture's block carries no runs figure to edit")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir)
	if err != nil {
		t.Fatalf("a hand-edited report failed the command: %v", err)
	}
	if !strings.Contains(errOut, "2026-10-24") {
		t.Errorf("the command does not name the report it skipped:\n%s", errOut)
	}
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "2026/2026-10-24.md") {
		t.Errorf("the index does not name the hand-edited report:\n%s", index)
	}
	if !strings.Contains(string(index), "2026-10-25") {
		t.Errorf("the index lost the report beside it:\n%s", index)
	}
	// The report being written is written, whatever the folder held.
	if !strings.Contains(digestReport(t, dir, "2026-10-25"), "20261025-120000-abcd") {
		t.Error("the report being written is missing its own run")
	}
}

// TestRunsDigestYearRollsUpMonths is F24: the records are kept 90 days, so a
// yearly report has no runs of its own to read -- it is the twelve monthly
// reports of that year added up, and a month of another year is not one of
// them.
func TestRunsDigestYearRollsUpMonths(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20250815-120000-abcd", time.Date(2025, 8, 15, 12, 0, 0, 0, time.UTC), "acme/app")
	seedDigestRun(t, f, "20250915-120000-abcd", time.Date(2025, 9, 15, 12, 0, 0, 0, time.UTC), "acme/app")
	// A month of another year, so the roll-up has one to leave out.
	seedDigestRun(t, f, "20240815-120000-abcd", time.Date(2024, 8, 15, 12, 0, 0, 0, time.UTC), "acme/app")
	for _, month := range []string{"2024-08-15", "2025-08-15", "2025-09-15"} {
		if _, _, err := execute(t, "runs", "digest", "--period", "month", "--date", month, "--dir", dir); err != nil {
			t.Fatal(err)
		}
	}

	if _, errOut, err := execute(t, "runs", "digest", "--period", "year", "--date", "2025-01-01", "--dir", dir); err != nil {
		t.Fatalf("runs digest --period year: %v (%s)", err, errOut)
	}
	body := digestReport(t, dir, "2025")
	for _, want := range []string{
		"# Run digest: 2025",
		`"period":"2025"`,
		`"start":"2025-01-01"`,
		`"end":"2025-12-31"`,
		`"runs":2`,
		"2025-08",
		"2025-09",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the year's report does not carry %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "2024-08") {
		t.Errorf("the year's report rolls up a month of another year:\n%s", body)
	}

	// The index lists the year beside the months it rolled up.
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2025/2025.md", "2025/2025-08.md", "2025/2025-09.md"} {
		if !strings.Contains(string(index), want) {
			t.Errorf("the index does not carry %q:\n%s", want, index)
		}
	}

	// A year no committed month names has nothing to roll up, and says so
	// rather than writing a report of zeros (G5).
	if _, _, err := execute(t, "runs", "digest", "--period", "year", "--date", "2023-01-01", "--dir", dir); err == nil {
		t.Error("a year with no committed monthly report was reported")
	}
}
