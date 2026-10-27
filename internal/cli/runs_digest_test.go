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
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// names. The two are deliberately separable: a project job's account reaches
// every repository's runs/, so a record under one repository's prefix that
// names another is exactly the untrusted input G2's check is for.
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
	// The document's keys are the ones the help names and no others, in the
	// document's own case: a consumer reads them, and a key nothing documents
	// is a shape it cannot rely on.
	var doc2 map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &doc2); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(doc2))
	for k := range doc2 {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := "period,repo,summary"; strings.Join(keys, ",") != want {
		t.Errorf("--json printed the keys %v, want %s", keys, want)
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

// TestRunsDigestNamesRenamedReport: the folder is a person's to edit, so a
// report file whose name is not D321's layout -- renamed within its year, or
// filed under a year its key does not name -- is named by its own path and
// left out of the patterns and of the index's figures, rather than vanishing
// from the folder without a word.
func TestRunsDigestNamesRenamedReport(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")
	if _, _, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir); err != nil {
		t.Fatal(err)
	}

	// The same report under two names this command did not write: one renamed
	// inside its year, one filed under a year its key does not name.
	body, err := os.ReadFile(filepath.Join(dir, "2026", "2026-10-25.md"))
	if err != nil {
		t.Fatal(err)
	}
	renamed := []string{"2026/2026-10-25-digest.md", "2025/2026-10-25.md"}
	for _, rel := range renamed {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// --json writes nothing, so both files are still there to be named, and
	// both are in the document's skipped list.
	doc, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--json")
	if err != nil {
		t.Fatalf("a renamed report failed the command: %v (%s)", err, errOut)
	}
	for _, rel := range renamed {
		if !strings.Contains(errOut, rel) {
			t.Errorf("the command does not name %s on stderr:\n%s", rel, errOut)
		}
	}
	var got struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("%v: %s", err, doc)
	}
	// The skipped list is in path order, which is what makes it the same list
	// twice (G3).
	want := append([]string(nil), renamed...)
	sort.Strings(want)
	if strings.Join(got.Skipped, ",") != strings.Join(want, ",") {
		t.Errorf("--json skipped %v, want %s", got.Skipped, want)
	}

	// The report being written goes on, and the index lists the report the
	// layout holds and not the names it does not.
	if _, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir); err != nil {
		t.Fatalf("a renamed report failed the write: %v (%s)", err, errOut)
	}
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "2026/2026-10-25.md") {
		t.Errorf("the index lost the report of the layout:\n%s", index)
	}
	for _, rel := range renamed {
		if strings.Contains(string(index), rel) {
			t.Errorf("the index lists %s:\n%s", rel, index)
		}
	}
}

// TestRunsDigestRegeneratesHandEditedReport: regenerating a report a person
// edited by hand is the repair this command is for, and the file it replaces
// is not named as skipped -- the warning is computed before the write, and a
// caller reading it would think the folder still holds something it does not.
func TestRunsDigestRegeneratesHandEditedReport(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")
	if _, _, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "2026", "2026-10-25.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(body), `"runs":1`, `"runs":"many"`, 1)
	if edited == string(body) {
		t.Fatal("the fixture's block carries no runs figure to edit")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// A call that writes nothing is not fixing the file, so it names it.
	_, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "2026/2026-10-25.md") {
		t.Errorf("a call that writes nothing does not name the hand-edited report:\n%s", errOut)
	}

	// The call that replaces it does not: the file it names is the one it is
	// about to write over.
	_, errOut, err = execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "2026/2026-10-25.md") {
		t.Errorf("the command names the report it is replacing:\n%s", errOut)
	}
	if !strings.Contains(digestReport(t, dir, "2026-10-25"), `"runs":1`) {
		t.Error("the report being written is not a good one")
	}
	index, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), "| - | - | - | - |") {
		t.Errorf("the index lists the replaced report without figures:\n%s", index)
	}
}

// TestRunsDigestWarnsTruncatedListing pins the bound on the read: a listing
// past it is reported over its newest runs, and the command says so on stderr
// rather than quietly publishing a period's figures over part of it.
// digestMaxRuns is a variable for this test's sake: five thousand real runs
// are not a fixture.
func TestRunsDigestWarnsTruncatedListing(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	bound := digestMaxRuns
	digestMaxRuns = 2
	t.Cleanup(func() { digestMaxRuns = bound })

	for i, at := range []time.Time{
		time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
	} {
		seedDigestRun(t, f, fmt.Sprintf("20261025-1%d0000-abcd", i), at, "acme/app")
	}

	out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--stdout")
	if err != nil {
		t.Fatalf("runs digest: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "more than 2 runs") {
		t.Errorf("the command does not say the listing was truncated:\n%s", errOut)
	}
	if !strings.Contains(out, `"runs":2`) {
		t.Errorf("the report is not over the newest runs of the bound:\n%s", out)
	}
	if strings.Contains(out, "20261025-100000-abcd") {
		t.Errorf("the report reads a run past the bound, so the cap does not keep the newest:\n%s", out)
	}
	if !strings.Contains(out, "20261025-120000-abcd") {
		t.Errorf("the report leaves out the newest run:\n%s", out)
	}

	// A listing within the bound says nothing: the warning is about a report
	// read over part of its period, not about every call.
	digestMaxRuns = bound
	if _, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--stdout"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(errOut, "truncated") || strings.Contains(errOut, "more than") {
		t.Errorf("a listing within the bound warned:\n%s", errOut)
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
		// The report says how much of the year it added up: two of the
		// twelve months are committed, and the block carries the same count,
		// so neither a reader nor a rule takes it for a full year (G5).
		"2 of the year's twelve",
		`"months":2`,
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
