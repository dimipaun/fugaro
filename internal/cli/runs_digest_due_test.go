package cli

// fugaro runs digest --due: the cadence's own reading (D962, spec §6). It is
// read-only and offline -- it reads the committed report folder in the
// checkout it runs in, opens no project, no credential and no bucket, and
// writes nothing -- and it names the periods that have closed with no report
// beside the age of the newest report the folder holds. The computation it
// reads is internal/digest's own (R0), the one a report run's step reads, so a
// person's reading and a report run's cannot disagree about what is due.
//
// The folder is untrusted in the same way the other reads of it are: a file
// whose name is not D321's shape is not a report, so it neither satisfies a
// due period nor becomes the newest one. Every fixture here is a folder
// written by hand in a t.TempDir() and a pinned clock; nothing reaches the
// cloud.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/digest"
)

// dueReportDir is a folder with nothing in it yet, and the clock pinned to
// day: 2026-10-27 is a Tuesday, so the seven closed days the catch-up covers
// are 2026-10-26 down to 2026-10-20 and no week, month or year is due.
func dueReportDir(t *testing.T) (string, []string) {
	t.Helper()
	digestClock(t, "2026-10-27")
	return t.TempDir(), []string{
		"2026-10-26", "2026-10-25", "2026-10-24", "2026-10-23", "2026-10-22", "2026-10-21", "2026-10-20",
	}
}

// writeDueReport hands the folder a file at D321's own path for key. Presence
// is what --due reads, so the body is only what a reader would find there.
func writeDueReport(t *testing.T, dir, key string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(digest.PeriodPath(key)))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Run digest: "+key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// duePeriods are the periods a --due run names, in the order it prints them.
func duePeriods(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(out, "\n") {
		line, ok := strings.CutPrefix(line, "  ")
		if !ok {
			continue
		}
		line, ok = strings.CutSuffix(line, ")")
		if !ok {
			continue
		}
		key, _, ok := strings.Cut(line, " (")
		if !ok {
			continue
		}
		got = append(got, key)
	}
	return got
}

// dueEntries is every file under dir, by its path under dir, in path order.
func dueEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRunsDigestDueNamesTheMissingPeriods is the headline: the periods that
// have closed and have no report under --dir are the ones the command names,
// newest first, and a period the folder does hold is not one of them.
func TestRunsDigestDueNamesTheMissingPeriods(t *testing.T) {
	dir, closed := dueReportDir(t)
	writeDueReport(t, dir, closed[0])
	writeDueReport(t, dir, closed[2])
	before := dueEntries(t, dir)

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir)
	if err != nil {
		t.Fatalf("runs digest --due: %v (%s)", err, errOut)
	}
	want := []string{"2026-10-25", "2026-10-23", "2026-10-22", "2026-10-21", "2026-10-20"}
	if got := duePeriods(t, out); !equalStrings(got, want) {
		t.Errorf("--due names %v, want the five closed days with no report, newest first: %v\n%s", got, want, out)
	}
	if !strings.Contains(out, "5 periods are due under "+dir) {
		t.Errorf("--due does not say how many periods are due and where:\n%s", out)
	}
	// The reading writes nothing: the folder is exactly what the fixture put
	// there, and no index appeared beside it.
	if got := dueEntries(t, dir); !equalStrings(got, before) {
		t.Errorf("--due wrote to the folder: %v, want the %v it read", got, before)
	}
}

// TestRunsDigestDueOnAnEmptyFolder: a checkout that has never reviewed gets
// the whole catch-up window back, newest first, and the command says the
// folder holds nothing rather than inventing a newest report.
func TestRunsDigestDueOnAnEmptyFolder(t *testing.T) {
	dir, closed := dueReportDir(t)

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir)
	if err != nil {
		t.Fatalf("runs digest --due: %v (%s)", err, errOut)
	}
	if got := duePeriods(t, out); !equalStrings(got, closed) {
		t.Errorf("--due names %v, want the seven closed days the catch-up covers: %v\n%s", got, closed, out)
	}
	if !strings.Contains(out, "7 periods are due") {
		t.Errorf("--due does not count the seven days:\n%s", out)
	}
	if !strings.Contains(out, "the folder holds no report") {
		t.Errorf("--due does not say the folder holds no report:\n%s", out)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("--due wrote %v to the folder (err %v), want nothing", entries, err)
	}
}

// TestRunsDigestDueSaysTheNewestReportAge: the second half of the signal is
// the age of the newest report the folder holds -- the newest period it
// covers, not the file that happens to sort last.
func TestRunsDigestDueSaysTheNewestReportAge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		newest string
		want   string
	}{
		{"yesterday", "2026-10-26", "newest report: 2026-10-26 (day), 1 day old"},
		{"two days", "2026-10-25", "newest report: 2026-10-25 (day), 2 days old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, closed := dueReportDir(t)
			writeDueReport(t, dir, closed[6])
			writeDueReport(t, dir, tc.newest)

			out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir)
			if err != nil {
				t.Fatalf("runs digest --due: %v (%s)", err, errOut)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("--due does not report %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestRunsDigestDueWithNothingDue: the folder holds a report for every closed
// period, so the reading is one line saying so -- and it still says how old
// the newest report is.
func TestRunsDigestDueWithNothingDue(t *testing.T) {
	dir, closed := dueReportDir(t)
	for _, key := range closed {
		writeDueReport(t, dir, key)
	}

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir)
	if err != nil {
		t.Fatalf("runs digest --due: %v (%s)", err, errOut)
	}
	if got := duePeriods(t, out); len(got) != 0 {
		t.Errorf("--due names %v, and every closed period has a report:\n%s", got, out)
	}
	if lines := strings.Count(strings.TrimSuffix(out, "\n"), "\n"); lines != 0 {
		t.Errorf("--due printed %d lines, want the one line that says nothing is due:\n%s", lines+1, out)
	}
	if !strings.Contains(out, "nothing is due under "+dir) || !strings.Contains(out, "newest report is 2026-10-26 (day), 1 day old") {
		t.Errorf("--due does not say nothing is due and how old the newest report is:\n%s", out)
	}
}

// TestRunsDigestDueIgnoresAFileThatIsNotAReport: the folder is untrusted, and
// a hand-written file is not a report: a name that is not D321's shape neither
// satisfies a due period nor becomes the newest report.
func TestRunsDigestDueIgnoresAFileThatIsNotAReport(t *testing.T) {
	dir, closed := dueReportDir(t)
	writeDueReport(t, dir, closed[0])
	for name, body := range map[string]string{
		"notes.md":                "notes, not a report\n",
		"2026-10-25.md":           "a report beside the year directory, which no writer of this layout makes\n",
		"2026/2026-10-25.txt":     "a report of another extension\n",
		"2026/2026-10-24.md.bak":  "a report renamed out of the layout\n",
		"index.md":                "an index of another name\n",
		"2026-10-25/README.md":    "a directory that is not a year of this layout's\n",
		"2026/2026-10-25/note.md": "a report under a directory a report has no business being in\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir)
	if err != nil {
		t.Fatalf("runs digest --due: %v (%s)", err, errOut)
	}
	if got := duePeriods(t, out); !equalStrings(got, closed[1:]) {
		t.Errorf("--due names %v, want the six days with no report: %v\n%s", got, closed[1:], out)
	}
	if !strings.Contains(out, "newest report: 2026-10-26 (day), 1 day old") {
		t.Errorf("--due does not read the newest report as the one real report:\n%s", out)
	}
}

// TestRunsDigestDueCountsAFutureReportAsNoDaysOld: a file named for a period
// that has not ended is a report by the layout, so it is a report here too --
// and its age is 0 rather than a negative count, which is the one number the
// schema admits. Nothing about which periods are due changes.
func TestRunsDigestDueCountsAFutureReportAsNoDaysOld(t *testing.T) {
	dir, closed := dueReportDir(t)
	writeDueReport(t, dir, "2027-01-01")

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir)
	if err != nil {
		t.Fatalf("runs digest --due: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "newest report: 2027-01-01 (day), 0 days old") {
		t.Errorf("--due does not read the future report as no days old:\n%s", out)
	}
	if got := duePeriods(t, out); !equalStrings(got, closed) {
		t.Errorf("--due names %v, want every closed day: %v\n%s", got, closed, out)
	}
}

// TestRunsDigestDueIsOffline: the reading reaches nothing. The proof is not a
// count of calls but the setup itself: a real checkout, an isolated config home
// with no project config at all, and an empty PATH, so the cloud path has
// nothing to select and no client to run. The control below shows the same
// checkout cannot report a period at all, which is what makes this run's
// success mean it opened nothing.
//
// It is also the reading of the default folder: this run names no --dir, so
// the reports it reads are the checkout's own docs/fugaro-reviews.
func TestRunsDigestDueIsOffline(t *testing.T) {
	digestCheckout(t)
	digestClock(t, "2026-10-27")
	isolateProjects(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if err := os.MkdirAll(filepath.Join(digest.Dir, "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(digest.Dir, "2026", "2026-10-26.md"), []byte("# Run digest: 2026-10-26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := execute(t, "runs", "digest", "--due")
	if err != nil {
		t.Fatalf("runs digest --due needs something it should not: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "6 periods are due under "+digest.Dir) || !strings.Contains(out, "2026-10-25 (day)") {
		t.Errorf("--due did not read the checkout's own folder:\n%s", out)
	}

	// The control: the same checkout with no project config cannot report a
	// period, because reporting one reads the repository's runs bucket. So
	// the success above is not a fixture that quietly answered from somewhere
	// else.
	_, _, err = execute(t, "runs", "digest")
	if err == nil || !strings.Contains(err.Error(), "no project config") {
		t.Errorf("runs digest without a project config: err = %v, want the refusal to select one; the offline claim above proves nothing unless reporting a period needs the cloud", err)
	}
}

// TestRunsDigestDueRefusesTheReportFlags: --due writes nothing and prints no
// report, so a flag that selects a report's period, its date, its repository,
// its destination or its draft has nothing to act on and is refused by name
// rather than silently ignored. --period is refused even carrying its own
// default: the caller typed it, so it is named.
func TestRunsDigestDueRefusesTheReportFlags(t *testing.T) {
	dir, _ := dueReportDir(t)

	for _, arg := range [][]string{
		{"--period", "week"}, {"--period", "day"}, {"--date", "2026-10-25"},
		{"--repo", "acme/app"}, {"--stdout"}, {"--submit-draft"},
	} {
		_, _, err := execute(t, append([]string{"runs", "digest", "--due", "--dir", dir}, arg...)...)
		if err == nil {
			t.Errorf("--due accepted %s", strings.Join(arg, " "))
			continue
		}
		if !strings.Contains(err.Error(), arg[0]) {
			t.Errorf("--due %s refused with %q, want the flag named", strings.Join(arg, " "), err)
		}
	}
}

// TestRunsDigestDueJSONMatchesSchema validates the document the command writes
// against schemas/runs-digest-due.schema.json, the contract a consumer reads
// it by, and pins the members a consumer branches on.
func TestRunsDigestDueJSONMatchesSchema(t *testing.T) {
	dir, closed := dueReportDir(t)
	writeDueReport(t, dir, closed[1])

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir, "--json")
	if err != nil {
		t.Fatalf("runs digest --due --json: %v (%s)", err, errOut)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "runs-digest-due.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/runs-digest-due.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/runs-digest-due.schema.json: %v\n%s", err, out)
	}

	var decoded struct {
		Version int    `json:"version"`
		Dir     string `json:"dir"`
		Due     []struct {
			Dim string `json:"dim"`
			Key string `json:"key"`
		} `json:"due"`
		Newest *struct {
			Key     string `json:"key"`
			Dim     string `json:"dim"`
			DaysAgo int    `json:"days_ago"`
		} `json:"newest"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 {
		t.Errorf("version = %d, and the document's first version is 1", decoded.Version)
	}
	if decoded.Dir != dir {
		t.Errorf("dir = %q, want the folder the reading read (%q)", decoded.Dir, dir)
	}
	if len(decoded.Due) != 6 {
		t.Errorf("due holds %d periods, and the folder covers one of the seven closed days: %v", len(decoded.Due), decoded.Due)
	}
	for _, d := range decoded.Due {
		if d.Dim != string(digest.ByDay) {
			t.Errorf("due row %q: dim = %q, want %q", d.Key, d.Dim, digest.ByDay)
		}
	}
	if decoded.Newest == nil {
		t.Fatalf("newest is null, and the folder holds a report:\n%s", out)
	}
	if decoded.Newest.Key != "2026-10-25" || decoded.Newest.Dim != string(digest.ByDay) || decoded.Newest.DaysAgo != 2 {
		t.Errorf("newest = %+v, want the 2026-10-25 day report two days old", *decoded.Newest)
	}
}

// TestRunsDigestDueJSONOnAnEmptyFolder: newest is null rather than absent, and
// due is the whole catch-up window -- the two shapes a consumer reads without
// checking for a missing member.
func TestRunsDigestDueJSONOnAnEmptyFolder(t *testing.T) {
	dir, closed := dueReportDir(t)

	out, errOut, err := execute(t, "runs", "digest", "--due", "--dir", dir, "--json")
	if err != nil {
		t.Fatalf("runs digest --due --json: %v (%s)", err, errOut)
	}
	var decoded struct {
		Due    []json.RawMessage `json:"due"`
		Newest json.RawMessage   `json:"newest"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Due) != len(closed) {
		t.Errorf("due holds %d periods, want the seven closed days: %s", len(decoded.Due), out)
	}
	if string(decoded.Newest) != "null" {
		t.Errorf("newest = %s, want null on an empty folder", decoded.Newest)
	}
}
