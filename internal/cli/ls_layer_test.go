package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

func TestLsShowsTheLayerColumnOnlyWhenARunHasOne(t *testing.T) {
	rows := []runview.Row{{Run: "s/20261008-100000-abcd", RunID: "20261008-100000-abcd", Status: "succeeded"}}
	var b bytes.Buffer
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "LAYER") {
		t.Fatalf("no layer: %q %v", b.String(), err)
	}
	rows[0].ProjectLayer = &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}
	rows = append(rows, runview.Row{Run: "s/20261008-110000-abcd", RunID: "20261008-110000-abcd", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3}})
	b.Reset()
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || !strings.Contains(b.String(), "LAYER") ||
		!strings.Contains(b.String(), "gen 3") || !strings.Contains(b.String(), "gen 3 (not applied)") {
		t.Fatalf("with a layer: %q %v", b.String(), err)
	}
}

func TestDiagnosePrintsTheConfigLine(t *testing.T) {
	d := &Diagnosis{Row: runview.Row{Run: "s/20261008-100000-abcd", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}, ConfigSHA256: strings.Repeat("b", 64)}}
	var b bytes.Buffer
	if err := printDiagnosis(&b, d, false); err != nil || !strings.Contains(b.String(), "Config:   project layer generation 3 (sha256 aaaaaaaaaaaa); resolved sha256 bbbbbbbbbbbb") {
		t.Fatalf("%q %v", b.String(), err)
	}
}
