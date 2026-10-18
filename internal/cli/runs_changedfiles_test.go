package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// TestRunsLSChangedFilesReachTheDocument: the run's own record of the paths it
// changed is in `runs ls --json`'s rows (design D27), bounded and path-only,
// and absent — not empty — for a run that recorded none.
func TestRunsLSChangedFilesReachTheDocument(t *testing.T) {
	rows := []runview.Row{
		{Run: "acme-app/20261007-100000-abcd", Status: "succeeded",
			ChangedFiles: runstore.NewChangedFiles([]string{"internal/cli/runs.go", "docs/design/desktop-console-v2.md"})},
		{Run: "acme-app/20261007-110000-abcd", Status: "running"},
	}
	var b strings.Builder
	if err := printRows(&b, "aurora", rows, nil, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	cf, ok := doc.Runs[0]["changed_files"].(map[string]any)
	if !ok {
		t.Fatalf("the first run's changed_files = %v", doc.Runs[0]["changed_files"])
	}
	paths, _ := cf["paths"].([]any)
	if len(paths) != 2 || paths[0] != "internal/cli/runs.go" {
		t.Errorf("changed_files.paths = %v", cf["paths"])
	}
	if cf["total"] != float64(2) || cf["truncated"] != nil {
		t.Errorf("changed_files = %v, want total 2 and no truncation", cf)
	}
	if _, present := doc.Runs[1]["changed_files"]; present {
		t.Errorf("a run that recorded none names the field: %v", doc.Runs[1]["changed_files"])
	}
	// The document says how many paths there were even when it cannot show
	// them all: a truncated set is never read as a whole one.
	rows[0].ChangedFiles = &runstore.ChangedFiles{Paths: []string{"a.go"}, Total: 400, Truncated: true}
	b.Reset()
	if err := printRows(&b, "aurora", rows, nil, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"total": 400`) || !strings.Contains(b.String(), `"truncated": true`) {
		t.Errorf("a truncated record does not say so:\n%s", b.String())
	}
}

// TestRunsLSChangedFilesColumn: the human listing shows the count, "N/M" when
// the record was truncated, "-" for a run that recorded none — and no column
// at all when no listed run has one, so a listing of runs from before the
// field looks exactly as it did.
func TestRunsLSChangedFilesColumn(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded"}}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "FILES") {
		t.Fatalf("no recorded set:\n%s", b.String())
	}

	b.Reset()
	rows = append(rows,
		runview.Row{Run: "acme-app/20261007-110000-abcd", Status: "running",
			ChangedFiles: runstore.NewChangedFiles([]string{"a.go", "b.go"})},
		runview.Row{Run: "acme-app/20261007-120000-abcd", Status: "running",
			ChangedFiles: &runstore.ChangedFiles{Paths: []string{"a.go"}, Total: 40, Truncated: true}},
	)
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "FILES") {
		t.Fatalf("no FILES column:\n%s", out)
	}
	line := func(run string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, run) {
				return strings.TrimRight(l, " ")
			}
		}
		return ""
	}
	if got := line("110000"); !strings.HasSuffix(got, " 2") {
		t.Errorf("a two-path run's cell: %q", got)
	}
	if got := line("120000"); !strings.HasSuffix(got, " 1/40") {
		t.Errorf("a truncated run's cell: %q, want it to say 1 of 40", got)
	}
	if got := line("100000"); !strings.HasSuffix(got, " -") {
		t.Errorf("a run that recorded none: %q, want -", got)
	}
}
