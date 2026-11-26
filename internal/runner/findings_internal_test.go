package runner

import (
	"context"
	"log/slog"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// findingsRun is the least a run needs to write its open-findings artifact: a
// store under the run's own prefix, the record it names itself with, and the
// deciding review round the artifact is read from.
func findingsRun(t *testing.T, runID string, findings ...Finding) *run {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	return &run{
		rec:        &runstore.Record{Version: 1, RunID: runID},
		lastSenior: seniorRound{Round: 2, Findings: findings},
		d:          Deps{Store: runstore.Open(bucket, "acme/app", runID), Log: slog.New(slog.DiscardHandler)},
	}
}

// TestWriteFindingsPinsTheArtifact (BF1): the bytes the runner leaves under the
// run's own prefix are the artifact the triage reads — version 1, this run's
// own id, and one severity, path and key per open finding, with the key the
// record's own triage entries carry (findingKey). The severity is the review's
// own word, unmapped: the rule refuses a word it does not know rather than
// reading it as low.
func TestWriteFindingsPinsTheArtifact(t *testing.T) {
	const runID = "20261125-120128-a8f5"
	high := Finding{
		Severity: "high", Class: "data_loss", File: "internal/creds/store.go",
		Summary: "the key is logged", Likelihood: 4, Impact: 5, FixCost: 2,
	}
	nit := Finding{Severity: "nit", File: "docs/design/sweep.md", Summary: "a stale sentence"}
	r := findingsRun(t, runID, high, nit)

	r.writeFindings(context.Background())

	want := `{"version":1,"run":"` + runID + `","findings":[` +
		`{"severity":"high","path":"internal/creds/store.go","key":"` + findingKey(high) + `"},` +
		`{"severity":"nit","path":"docs/design/sweep.md","key":"` + findingKey(nit) + `"}]}` + "\n"
	got, err := r.d.Store.ReadFile(context.Background(), FindingsName)
	if err != nil {
		t.Fatalf("reading the artifact the run wrote: %v", err)
	}
	if string(got) != want {
		t.Fatalf("the artifact is\n%s\nwant\n%s", got, want)
	}
}

// TestWriteFindingsWritesAnEmptyListForARunWithNone: a run that ended with no
// open finding writes the file rather than leaving none. The reader tells the
// two apart — an absent file is a run nobody could read, and an empty list is a
// run that ended with nothing — so writing it here is the whole difference
// between the merge gate's fail-closed answer and its positive evidence.
func TestWriteFindingsWritesAnEmptyListForARunWithNone(t *testing.T) {
	const runID = "20261125-120128-a8f5"
	r := findingsRun(t, runID)

	r.writeFindings(context.Background())

	got, err := r.d.Store.ReadFile(context.Background(), FindingsName)
	if err != nil {
		t.Fatalf("reading the artifact the run wrote: %v", err)
	}
	if want := `{"version":1,"run":"` + runID + `","findings":[]}` + "\n"; string(got) != want {
		t.Fatalf("the artifact is\n%s\nwant\n%s", got, want)
	}
}
