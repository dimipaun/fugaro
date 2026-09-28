package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// A run's own service account can write its result.json. A record naming
// another repository's execution must never be followed: not cancelled, not
// streamed, not backfilled into launch.json.
func TestForgedRecordExecutionIsRefused(t *testing.T) {
	f := newCloudFixture(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "web"), "4", "8Gi")
	victim := f.run.Start(gcp.JobName(other, "web"))
	f.run.SetState(victim, backend.StateRunning)
	f.logging.AddJSONLines(victim, []byte(`{"time":"`+logTime(0)+`","severity":"INFO","message":"other repo's secret log"}`+"\n"))

	const launched, unlaunched = "20260927-100000-abcd", "20260927-110000-bbbb"
	seedRun(t, f, launched, "", "someone@example.com", true)
	seedRun(t, f, unlaunched, "", "someone@example.com", false)
	for _, id := range []string{launched, unlaunched} {
		writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: victim, Status: runstore.StatusRunning, Stage: "implement"})
	}

	for _, id := range []string{launched, unlaunched} {
		out, _, err := execute(t, "cancel", "--now", "--json", "--poll", "10ms", id)
		if err == nil {
			t.Fatalf("cancel %s = %s, want a refusal", id, out)
		}
		if f.run.State(victim) != backend.StateRunning {
			t.Fatalf("cancel %s cancelled another repository's execution", id)
		}
		out, _, err = execute(t, "logs", id)
		if err == nil || strings.Contains(out, "other repo") {
			t.Fatalf("logs %s = %q, %v", id, out, err)
		}
		if _, _, err := execute(t, "diagnose", id); err == nil {
			t.Fatalf("diagnose %s followed the forged execution", id)
		}
	}

	// --retry must not backfill launch.json from the forged record.
	if out, _, err := execute(t, "run", "--retry", unlaunched); err == nil {
		t.Fatalf("run --retry = %s, want a refusal", out)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	if l, err := runstore.Open(b, appSlug, unlaunched).ReadLaunch(context.Background()); err == nil {
		t.Fatalf("launch.json backfilled: %+v", l)
	}

	// ls shows each such run as a per-row error, with a warning, and goes on.
	out, errOut, err := execute(t, "ls", "--json", "--since", "0")
	var got lsOut
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Runs) != 2 {
		t.Fatalf("ls = %s, %v (%s)", out, err, errOut)
	}
	for _, r := range got.Runs {
		if r.Status != "error" || !strings.Contains(r.Reason, "execution") || backend.SameExecution(r.Execution, victim) {
			t.Fatalf("row = %+v", r)
		}
	}
	if !strings.Contains(errOut, "warning") {
		t.Fatalf("ls stderr = %q", errOut)
	}
}
