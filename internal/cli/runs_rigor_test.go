package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// rigorRow is a row with a rigor block, as runview.Join hands it over.
func rigorRow(run string) runview.Row {
	return runview.Row{Run: run, Status: "succeeded", Rigor: &runstore.RigorRecord{
		Mode: rigor.Critical, From: rigor.FromArea,
		Areas: []runstore.RigorArea{
			{Name: "payments", Mode: rigor.Critical, At: runstore.RigorAtBootstrap},
			{Name: "docs", Mode: rigor.POC, At: runstore.RigorAtRound},
		},
		At:       runstore.RigorAtFinalize,
		Revision: "origin/main",
	}}
}

// TestRunsLSRigorReachesTheDocument: the mode the run resolved, the layer that
// decided it and the areas that matched are in `runs ls --json`'s rows
// (docs/rigor-modes D24), and absent — not empty — for a run that resolved
// none, so the console reads one field rather than recomputing the mode.
func TestRunsLSRigorReachesTheDocument(t *testing.T) {
	rows := []runview.Row{rigorRow("acme-app/20261007-100000-abcd"), {Run: "acme-app/20261007-110000-abcd", Status: "running"}}
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
	rg, ok := doc.Runs[0]["rigor"].(map[string]any)
	if !ok {
		t.Fatalf("the first run's rigor = %v", doc.Runs[0]["rigor"])
	}
	if rg["mode"] != "critical" || rg["from"] != "area" || rg["at"] != "finalize" || rg["revision"] != "origin/main" {
		t.Errorf("rigor = %v", rg)
	}
	areas, _ := rg["areas"].([]any)
	if len(areas) != 2 {
		t.Fatalf("rigor.areas = %v", rg["areas"])
	}
	if first, _ := areas[0].(map[string]any); first["name"] != "payments" || first["mode"] != "critical" || first["at"] != "bootstrap" {
		t.Errorf("the strictest area is not first with its own mode and moment: %v", areas[0])
	}
	if _, present := doc.Runs[1]["rigor"]; present {
		t.Errorf("a run that resolved none names the field: %v", doc.Runs[1]["rigor"])
	}
}

// TestRunsLSRigorColumn: the human listing shows the mode and the layer that
// decided it, plus the strictest area when one matched — "-" for a run that
// resolved none — and no column at all when no listed run has a block, so a
// listing of runs from before the field looks exactly as it did.
func TestRunsLSRigorColumn(t *testing.T) {
	var b strings.Builder
	rows := []runview.Row{{Run: "acme-app/20261007-100000-abcd", Status: "succeeded"}}
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "MODE") {
		t.Fatalf("no resolved mode:\n%s", b.String())
	}

	b.Reset()
	rows = append(rows,
		rigorRow("acme-app/20261007-110000-abcd"),
		runview.Row{Run: "acme-app/20261007-120000-abcd", Status: "running",
			Rigor: &runstore.RigorRecord{Mode: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtBootstrap}},
		// This one resolved none, and its launcher is named: the "-" the last
		// column shows is the mode's, not the BY column's.
		runview.Row{Run: "acme-app/20261007-130000-abcd", Status: "running", LaunchedBy: "me@example.com"},
	)
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "MODE") {
		t.Fatalf("no MODE column:\n%s", out)
	}
	line := func(run string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, run) {
				return strings.TrimRight(l, " ")
			}
		}
		return ""
	}
	if got := line("110000-abcd"); !strings.HasSuffix(got, "critical (area payments +1)") {
		t.Errorf("a run with two areas: %q", got)
	}
	if got := line("120000-abcd"); !strings.HasSuffix(got, "pro (repo)") {
		t.Errorf("a run with no area: %q", got)
	}
	if got := line("130000-abcd"); !strings.HasSuffix(got, "-") {
		t.Errorf("a run that resolved none: %q", got)
	}
}

// TestRunsLSRigorCellIsBoundedAndInert: the MODE cell is built from the
// record's own block, so it is exactly as hostile as an area name the job
// wrote — a newline, an escape or 10 KB of it must not forge a row or rewrite
// the terminal. The block is bounded on read (runstore.RigorRecord.Bounded)
// and the cell is clipped like every other name cell.
func TestRunsLSRigorCellIsBoundedAndInert(t *testing.T) {
	row := rigorRow("acme-app/20261007-100000-abcd")
	row.Rigor.Areas[0].Name = "a\nb\tc\x1b[2J" + strings.Repeat("x", 10_000)
	var b strings.Builder
	if err := printRows(&b, "aurora", []runview.Row{row}, nil, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	// The listing is a header, one line per run and a summary: the hostile
	// name must not add a line of its own.
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("one run rendered as %d lines:\n%s", len(lines), b.String())
	}
	rendered := lines[1]
	for _, bad := range []string{"\x1b", "\n", "\r", string(rune(0x202e)), string(rune(0x2028))} {
		if strings.Contains(rendered, bad) {
			t.Errorf("the cell carries %q: %q", bad, rendered)
		}
	}
	if n := len([]rune(rendered)); n > 200 {
		t.Errorf("the row is %d runes, not bounded: %q", n, rendered)
	}
}

// TestDiagnosePrintsTheRigorLine: the diagnosis names the mode, the layer that
// decided it, every area that matched with the moment it was applied at, the
// moment of the block and the revision the configuration was read from (D22,
// D24, D25) — and no line at all for a run that resolved none.
func TestDiagnosePrintsTheRigorLine(t *testing.T) {
	var b strings.Builder
	if err := printDiagnosis(&b, &Diagnosis{Row: runview.Row{Run: "a/b", Status: "succeeded"}}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Rigor:") {
		t.Fatalf("a run that resolved none printed a rigor line:\n%s", b.String())
	}

	b.Reset()
	if err := printDiagnosis(&b, &Diagnosis{Row: rigorRow("a/b")}, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"Rigor:    critical, from area",
		"payments at bootstrap",
		"docs at round",
		"at finalize",
		"read from origin/main",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rigor line lacks %q:\n%s", want, out)
		}
	}
	if l := rigorLine(&runstore.RigorRecord{Mode: rigor.POC, From: rigor.FromProject, At: runstore.RigorAtBootstrap}); !strings.Contains(l, "poc") || strings.Contains(l, "read from") {
		t.Errorf("a block with no areas and no revision: %q", l)
	}
}
