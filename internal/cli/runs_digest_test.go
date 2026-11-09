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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/issues"
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
		// The run table names the period's runs by their short IDs (D1706).
		"| `abcd` | succeeded |",
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
	// The two seeded IDs end alike, so the run the table read shows its four
	// characters; a report that wrongly read the other repository's record
	// would extend both endings through the dash to tell the rows apart.
	if !strings.Contains(out, "| `abcd` | succeeded |") {
		t.Errorf("the report leaves out this repository's own run:\n%s", out)
	}
	if strings.Contains(out, "-abcd`") {
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
	if !strings.Contains(digestReport(t, dir, "2026-10-25"), "| `abcd` | succeeded |") {
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
	// The three seeded IDs end alike, so the endings the table shows are the
	// ones its own rows need to tell each other apart: the newest two need ten
	// characters, and the run off the bound would show the oldest one's.
	if strings.Contains(out, "`00000-abcd`") {
		t.Errorf("the report reads a run past the bound, so the cap does not keep the newest:\n%s", out)
	}
	if !strings.Contains(out, "| `20000-abcd` | succeeded |") {
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

	// The index says how much of the year its row covers too: a year of two
	// months read as a whole one is the same mistake the block's Months field
	// exists to prevent (G5).
	if !strings.Contains(string(index), "2 of 12 months") {
		t.Errorf("the index does not say how much of the year it covers:\n%s", index)
	}

	// A year no committed month names has nothing to roll up, and says so
	// rather than writing a report of zeros (G5).
	if _, _, err := execute(t, "runs", "digest", "--period", "year", "--date", "2023-01-01", "--dir", dir); err == nil {
		t.Error("a year with no committed monthly report was reported")
	}
}

// digestSeedWeek writes one committed weekly report carrying one flag, the way
// an earlier call of this command wrote it, and returns its period key: the
// pattern rules' history, without a run record per week.
func digestSeedWeek(t *testing.T, dir, date, flag string) string {
	t.Helper()
	p, err := digest.NewPeriod(digest.ByWeek, date)
	if err != nil {
		t.Fatal(err)
	}
	block, err := json.Marshal(digest.Summary{
		Period: p.Key, Start: p.Start, End: p.End,
		Runs: 5, Shown: 5, Flagged: 1,
		Billed: "1.000000", Notional: "0.000000", Review: "0.000000",
		Flags: map[string]int{flag: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := "# Run digest: " + p.Key + "\n\n```json\n" + string(block) + "\n```\n"
	path := filepath.Join(dir, p.Key[:4], p.Key+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p.Key
}

// TestRunsDigestBackfillReadsNoLaterPeriod: a period's history is the reports
// committed before it in time, not the ones the folder happens to hold. The
// command is run from a terminal and a period is reported over its own runs,
// so a backfill of an older week runs with the later weeks' reports already
// committed, and a rule reading them would report a pattern over weeks this
// report does not cover (G5).
func TestRunsDigestBackfillReadsNoLaterPeriod(t *testing.T) {
	digestCheckout(t)
	newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")

	// Four weeks running, each carrying the same flag: a history the first
	// rule fires on. All of them are after the week being backfilled.
	later := map[string]bool{}
	for _, day := range []string{"2026-05-11", "2026-05-18", "2026-05-25", "2026-06-01"} {
		later[digestSeedWeek(t, dir, day, digest.FlagHalt)] = true
	}

	// 2026-03-02 is in 2026-W10, ten weeks before the earliest of them.
	doc, errOut, err := execute(t, "runs", "digest", "--period", "week", "--date", "2026-03-02", "--dir", dir, "--json")
	if err != nil {
		t.Fatalf("backfilling a week failed: %v (%s)", err, errOut)
	}
	var got struct {
		Period   string           `json:"period"`
		Patterns []digest.Pattern `json:"patterns"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("%v: %s", err, doc)
	}
	if got.Period != "2026-W10" {
		t.Fatalf("the report is of %s, want 2026-W10", got.Period)
	}
	for _, p := range got.Patterns {
		for _, period := range p.Periods {
			if later[period] {
				t.Errorf("the report of %s read the later period %s into %s", got.Period, period, p.Rule)
			}
		}
	}
	if len(got.Patterns) != 0 {
		t.Errorf("a week with no earlier report raised %+v", got.Patterns)
	}
}

// TestRunsDigestRefusesFuturePeriod: a period that has not begun has received
// no finished run at all, so it is refused the way a period holding today is,
// and nothing is written for a period that is not yet a period (G4).
func TestRunsDigestRefusesFuturePeriod(t *testing.T) {
	digestCheckout(t)
	newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")

	for _, tc := range []struct{ period, date, key string }{
		{"day", "2099-01-01", "2099-01-01"},
		{"week", "2099-01-01", "2099-W01"},
		{"month", "2099-01-01", "2099-01"},
		{"year", "2099-01-01", "2099"},
	} {
		_, _, err := execute(t, "runs", "digest", "--period", tc.period, "--date", tc.date, "--dir", dir)
		if err == nil {
			t.Errorf("--period %s --date %s reported a period that has not begun", tc.period, tc.date)
			continue
		}
		if !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), "2026-10-27") {
			t.Errorf("--period %s --date %s refused with %q, want the period and today named", tc.period, tc.date, err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("a refused report wrote %v (err %v), want nothing", entries, err)
	}
}

// TestRunsDigestSkippedNameIsOneLine: the files under --dir are a person's to
// name, so a skipped one can carry a control character. The command names it
// on stderr and in --json's skipped list, and neither a terminal nor a
// consumer may take those bytes for anything but the text of one line (G1).
func TestRunsDigestSkippedNameIsOneLine(t *testing.T) {
	digestCheckout(t)
	newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")

	// A name whose escape sequence rewrites the screen, and one that carries a
	// line break of its own. Neither is D321's shape, so both are skipped.
	names := []string{"2026/report\x1b[31m.md", "2026/two\nlines.md"}
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# Run digest\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	doc, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--json")
	if err != nil {
		t.Fatalf("a name with a control character failed the command: %v (%s)", err, errOut)
	}
	var got struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("%v: %s", err, doc)
	}
	if len(got.Skipped) != len(names) {
		t.Fatalf("--json skipped %v, want the fixture's two files", got.Skipped)
	}
	// Every printed name is one line of printable text: the warning lines this
	// command writes may end in a newline of their own, and nothing else.
	for _, line := range append(append([]string{}, got.Skipped...), strings.Split(errOut, "\n")...) {
		if i := strings.IndexFunc(line, digestControl); i >= 0 {
			t.Errorf("%q carries the control character %q", line, line[i])
		}
	}
	for _, want := range []string{"2026/report.md", "2026/two?lines.md"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr does not name %q:\n%s", want, errOut)
		}
		named := false
		for _, s := range got.Skipped {
			named = named || s == want
		}
		if !named {
			t.Errorf("--json skipped %v, want %q", got.Skipped, want)
		}
	}
}

// digestControl is a character a skip listing must not carry: everything
// safetext.OneLine rewrites or drops, so a test written without it proves the
// listing is one line of text.
func digestControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// seedDigestIssue uploads one issue into the fixture project's shared store,
// under the run stem and summary given, and answers the upload. The store is
// the one runs digest reads -- the bucket the fixture's config names -- so the
// row the section prints is one the store really holds, not a hand-built row.
func seedDigestIssue(t *testing.T, f *cloudFixture, run, summary string, created time.Time) issues.Uploaded {
	t.Helper()
	ctx := context.Background()
	b, err := blobx.Open(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	up, err := issues.Upload(ctx, b, issues.Issue{
		Run: run, LocalID: "local-" + run,
		Reporter: "someone@example.test", Created: created,
		Class: issues.ClassFugaro, Status: issues.StatusPending,
		OS: "linux", FugaroVersion: "0.21.0",
		// One byte pair repeated, so no literal here is the hash of anything.
		Fingerprint: strings.Repeat("ab", 32),
		Summary:     summary, Diagnosis: "the review stage starts twice", Steps: "run it again",
	})
	if err != nil {
		t.Fatalf("seeding issue %s: %v", run, err)
	}
	return up
}

// TestRunsDigestPrintsIssuesSectionOnTerminal is D1729's terminal half: after
// the output of its own, a run on a terminal prints the project's Issues
// section, and a coding agent's session prints none of it.
func TestRunsDigestPrintsIssuesSectionOnTerminal(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")
	up := seedDigestIssue(t, f, "run-20261025120000-48213", "the review stage starts twice",
		time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC))
	bothTerminals(t)
	args := []string{"runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir}

	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("runs digest: %v (%s)", err, errOut)
	}
	// The row names the issue, its summary and the command that files it, and
	// the wording firms up: the default threshold is a week and this issue has
	// waited seven days (spec §6, T-IS15).
	for _, want := range []string{
		"## Issues",
		fmt.Sprintf("#%04d", up.Number),
		"the review stage starts twice",
		fmt.Sprintf("[fugaro issue submit %d]", up.Number),
		"please file it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal section does not carry %q:\n%s", want, out)
		}
	}
	// After the digest's own output, which is what "after runs digest" means.
	if wrote := strings.Index(out, "wrote "); wrote < 0 || wrote > strings.Index(out, "## Issues") {
		t.Errorf("the section is not printed after the report the command wrote:\n%s", out)
	}
	// And the report itself is untouched: the section is the terminal's own
	// (D1729).
	if body := digestReport(t, dir, "2026-10-25"); strings.Contains(body, "## Issues") {
		t.Errorf("the committed report carries the Issues section:\n%s", body)
	}

	// The wording is this machine's own setting (T-IS15): a threshold of 30
	// days reads the same issue as merely waiting.
	f.appendConfig(t, "issues: { nudge_after_days: 30 }\n")
	out, errOut, err = execute(t, args...)
	if err != nil {
		t.Fatalf("runs digest with a nudge threshold of its own: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "waiting 7 days") || strings.Contains(out, "please file it") {
		t.Errorf("a threshold of 30 days did not change the wording:\n%s", out)
	}

	t.Setenv("CLAUDECODE", "1")
	out, errOut, err = execute(t, args...)
	if err != nil {
		t.Fatalf("runs digest in an agent session: %v (%s)", err, errOut)
	}
	if strings.Contains(out, "## Issues") {
		t.Errorf("a coding agent's session printed the Issues section:\n%s", out)
	}
}

// TestRunsDigestFileUnchangedByIssues is D1729's other half: the section is
// printed to the terminal and written nowhere, so a report and an index
// regenerated with issues in the project are byte for byte the ones the same
// run wrote without them.
func TestRunsDigestFileUnchangedByIssues(t *testing.T) {
	digestCheckout(t)
	f := newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedDigestRun(t, f, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "acme/app")
	bothTerminals(t)
	args := []string{"runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir}

	if _, errOut, err := execute(t, args...); err != nil {
		t.Fatalf("runs digest: %v (%s)", err, errOut)
	}
	report, index := digestReport(t, dir, "2026-10-25"), digestIndex(t, dir)
	// Two runs of the same period write the same bytes (G3), which is what
	// makes the comparisons below a statement about the section and nothing
	// else.
	if _, errOut, err := execute(t, args...); err != nil {
		t.Fatalf("runs digest: %v (%s)", err, errOut)
	} else if got := digestReport(t, dir, "2026-10-25"); got != report {
		t.Fatal("a second run of the same report wrote different bytes")
	}
	seedDigestIssue(t, f, "run-20261025120000-48213", "the review stage starts twice",
		time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC))
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("runs digest with an issue in the project: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "## Issues") {
		t.Errorf("the run with an issue printed no Issues section, so this test proves nothing:\n%s", out)
	}
	if got := digestReport(t, dir, "2026-10-25"); got != report {
		t.Errorf("an issue in the project changed the report:\n%s", got)
	}
	if got := digestIndex(t, dir); !bytes.Equal(got, index) {
		t.Errorf("an issue in the project changed the index:\n%s", got)
	}
}

// TestRunsDigestOmitsSectionOnReadFailure: the section is a read of the
// project's shared store, and a read that fails costs one line on stderr. The
// report is already written and stays a success: a store nobody can reach must
// not take the daily report down with it.
func TestRunsDigestOmitsSectionOnReadFailure(t *testing.T) {
	digestCheckout(t)
	newCloudFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	bothTerminals(t)
	old := digestIssuesRows
	digestIssuesRows = func(context.Context, *blobx.Bucket) ([]issues.SharedRow, error) {
		return nil, errors.New("the store could not be listed")
	}
	t.Cleanup(func() { digestIssuesRows = old })

	out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir)
	if err != nil {
		t.Fatalf("a shared store that cannot be read failed the report: %v", err)
	}
	if strings.Contains(out, "## Issues") {
		t.Errorf("the section was printed although the store could not be read:\n%s", out)
	}
	// One line about the section, and it names the cause: the other line
	// stderr carries is the project header every command prints.
	var reported []string
	for _, line := range strings.Split(strings.TrimSuffix(errOut, "\n"), "\n") {
		if strings.Contains(line, "Issues section") {
			reported = append(reported, line)
		}
	}
	if len(reported) != 1 || !strings.Contains(reported[0], "the store could not be listed") {
		t.Errorf("the read failure printed %v, want one line naming the failure:\n%s", reported, errOut)
	}
	if !strings.Contains(digestReport(t, dir, "2026-10-25"), "# Run digest") {
		t.Error("the report was not written")
	}
}

// digestIndex reads the index the command regenerated.
func digestIndex(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("the command regenerated no index: %v", err)
	}
	return data
}
