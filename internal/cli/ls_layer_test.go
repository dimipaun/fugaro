package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// layerLine returns the one line of out whose RUN column is run, or fails.
func layerLine(t *testing.T, out, run string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, run) {
			return line
		}
	}
	t.Fatalf("no line for %q in %q", run, out)
	return ""
}

func TestLsShowsTheLayerColumnOnlyWhenARunHasOne(t *testing.T) {
	rows := []runview.Row{{Run: "s/20261008-100000-abcd", RunID: "20261008-100000-abcd", Status: "succeeded"}}
	var b bytes.Buffer
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "LAYER") {
		t.Fatalf("no layer: %q %v", b.String(), err)
	}

	rows[0].ProjectLayer = &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}
	rows = append(rows,
		runview.Row{Run: "s/20261008-110000-abcd", RunID: "20261008-110000-abcd", Status: "succeeded",
			ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3}},
		runview.Row{Run: "s/20261008-120000-abcd", RunID: "20261008-120000-abcd", Status: "succeeded"},
	)
	b.Reset()
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil {
		t.Fatalf("with a layer: %v", err)
	}
	out := b.String()
	if header := strings.Split(out, "\n")[0]; !strings.Contains(header, "LAYER") {
		t.Fatalf("header: %q", header)
	}
	// Each row's exact LAYER cell, keyed to its own run: a test that only
	// checks the document contains "gen 3" and "gen 3 (not applied)"
	// somewhere would still pass if the two cells were swapped between
	// rows, so each is pinned to its own line's suffix instead.
	for run, want := range map[string]string{
		"s/20261008-100000-abcd": "gen 3",
		"s/20261008-110000-abcd": "gen 3 (not applied)",
		"s/20261008-120000-abcd": "-",
	} {
		if line := layerLine(t, out, run); !strings.HasSuffix(line, want) {
			t.Errorf("%s: line %q, want suffix %q", run, line, want)
		}
	}
}

func TestDiagnosePrintsTheConfigLine(t *testing.T) {
	d := &Diagnosis{Row: runview.Row{Run: "s/20261008-100000-abcd", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}, ConfigSHA256: strings.Repeat("b", 64)}}
	var b bytes.Buffer
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatalf("%v", err)
	}
	want := "Config:   project layer generation 3 (sha256 aaaaaaaaaaaa); resolved sha256 bbbbbbbbbbbb\n"
	if !strings.Contains(b.String(), want) {
		t.Fatalf("%q, want contains %q", b.String(), want)
	}
}

func TestDiagnosePrintsTheNotAppliedSuffix(t *testing.T) {
	d := &Diagnosis{Row: runview.Row{Run: "s/x", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: false}}}
	var b bytes.Buffer
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatalf("%v", err)
	}
	want := "Config:   project layer generation 3 (sha256 aaaaaaaaaaaa), not applied\n"
	if !strings.Contains(b.String(), want) {
		t.Fatalf("%q, want contains %q", b.String(), want)
	}
}

func TestDiagnosePrintsNoProjectLayerWhenThereIsNone(t *testing.T) {
	d := &Diagnosis{Row: runview.Row{Run: "s/x", Status: "succeeded", ConfigSHA256: strings.Repeat("b", 64)}}
	var b bytes.Buffer
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatalf("%v", err)
	}
	want := "Config:   no project layer; resolved sha256 bbbbbbbbbbbb\n"
	if !strings.Contains(b.String(), want) {
		t.Fatalf("%q, want contains %q", b.String(), want)
	}
}

func TestLayerCellTreatsANegativeGenerationAsInvalid(t *testing.T) {
	r := runview.Row{ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: -5, Applied: true}}
	if got := layerCell(r); got != "-" {
		t.Fatalf("layerCell = %q, want -", got)
	}
}

func TestDiagnoseTreatsANegativeGenerationAsNoProjectLayer(t *testing.T) {
	d := &Diagnosis{Row: runview.Row{Run: "s/x", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: -5, Applied: true},
		ConfigSHA256: strings.Repeat("b", 64)}}
	var b bytes.Buffer
	if err := printDiagnosis(&b, d, false); err != nil {
		t.Fatalf("%v", err)
	}
	want := "Config:   no project layer; resolved sha256 bbbbbbbbbbbb\n"
	if !strings.Contains(b.String(), want) {
		t.Fatalf("%q, want contains %q", b.String(), want)
	}
}

func TestShortSHARejectsAnUntrustedProjectLayerSHA(t *testing.T) {
	cases := map[string]string{
		"ESC sequence":  "\x1b[31m" + strings.Repeat("a", 59),
		"bidi override": "‮" + strings.Repeat("a", 61), // one rune, 3 bytes
		"short garbage": "abc123",
		"64 non-hex":    strings.Repeat("z", 64),
		"64 bytes, not hex-ASCII, from multibyte runes": strings.Repeat("é", 32), // 32 runes * 2 bytes = 64 bytes
	}
	for name, sha := range cases {
		if got := shortSHA(sha); got != "(invalid)" {
			t.Errorf("%s: shortSHA(%q) = %q, want (invalid)", name, sha, got)
		}
	}
}

func TestShortSHAAcceptsAValidProjectLayerDigest(t *testing.T) {
	if got := shortSHA(strings.Repeat("a", 64)); got != "aaaaaaaaaaaa" {
		t.Fatalf("got %q", got)
	}
}
