package cli

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// logTime is a log timestamp d after now, in the form the runner writes.
// Entries must not predate the launch: logs reads from a minute before it.
func logTime(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

func TestLogs(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "someone@example.com", true)
	f.logging.AddJSONLines(exec, []byte(`{"time":"`+logTime(time.Second)+`","severity":"INFO","message":"stage started","stage":"implement"}
{"time":"`+logTime(2*time.Second)+`","severity":"INFO","message":"tool Bash: fugaro verify test","stage":"implement","stream":"agent","event":"tool"}
`))
	out, _, err := execute(t, "logs", "20260927-100000-abcd")
	if err != nil || !strings.Contains(out, "[implement] stage started") || !strings.Contains(out, "[implement/agent] tool Bash") {
		t.Fatalf("logs = %s, %v", out, err)
	}
	js, _, err := execute(t, "logs", "--json", appSlug+"/20260927-100000-abcd")
	if err != nil || strings.Count(js, "\n") != 2 || !strings.Contains(js, `"event":"tool"`) {
		t.Fatalf("logs --json = %s, %v", js, err)
	}
	seedRun(t, f, "20260927-110000-bbbb", "", "someone@example.com", false)
	if _, _, err := execute(t, "logs", "20260927-110000-bbbb"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never launched") {
		t.Fatalf("unlaunched logs: %v", err)
	}
}

// Cloud Logging entries were redacted by the relay; logs redacts again, with
// the credentials the CLI itself holds, as defence in depth.
func TestLogsRedacts(t *testing.T) {
	const secret = "fake-token-test-0123456789"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	// The shared redactor's encoded forms apply here too: a base64 dump.
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	f.logging.AddJSONLines(exec, []byte(`{"time":"`+logTime(time.Second)+`","severity":"WARNING","message":"key is `+secret+` or `+b64+`","stage":"implement","stream":"agent"}`+"\n"))
	for _, args := range [][]string{{"logs"}, {"logs", "--json"}} {
		out, _, err := execute(t, append(args, "20260927-100000-abcd")...)
		if err != nil || strings.Contains(out, secret) || strings.Contains(out, b64) || !strings.Contains(out, "[REDACTED]") {
			t.Fatalf("%v = %s, %v", args, out, err)
		}
	}
}

func TestLogsUnknownRun(t *testing.T) {
	newCloudFixture(t)
	if _, _, err := execute(t, "logs", "20260927-100000-ffff"); ExitCode(err) != ExitUserError {
		t.Fatalf("unknown run: %v", err)
	}
}

// With log isolation on, logs and diagnose read only the Fugaro log view,
// which holds nothing of a run from before isolation: an empty read says
// so, once, on stderr. Without a view nothing is added.
func TestLogsHintWhenTheViewIsEmpty(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-100000-abcd", "", "someone@example.com", true)
	for _, cmd := range []string{"logs", "diagnose"} {
		if _, stderr, err := execute(t, cmd, "20260927-100000-abcd"); err != nil || strings.Contains(stderr, "log isolation") {
			t.Fatalf("%s without a view: stderr %q, %v", cmd, stderr, err)
		}
	}
	const view = "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"
	f.appendConfig(t, "log_view: "+view+"\n")
	f.logging.Resource = view
	for _, cmd := range []string{"logs", "diagnose"} {
		_, stderr, err := execute(t, cmd, "20260927-100000-abcd")
		if err != nil || strings.Count(stderr, "log isolation") != 1 || !strings.Contains(stderr, "_Default") {
			t.Fatalf("%s with a view: stderr %q, %v", cmd, stderr, err)
		}
	}
}

// TestLogsURL (G19): --url prints the execution's console link and reads no
// log; the empty-view hint names the same link.
//
// seedRun (ls_test.go) writes a launch with no LogURL, since it bypasses the
// real launch flow that fills it in (run.go's launchRun); the field --url
// prints is otherwise exactly what "run" and "diagnose" already print
// (locateLaunched's *runstore.Launch.LogURL). So this test writes the launch
// itself, with an explicit LogURL, the same way safetext_test.go does for
// the same reason.
func TestLogsURL(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	const consoleURL = "https://console.cloud.google.com/run/jobs/executions/details/us-east5/web-00001-abcd?project=proj-1234"
	seedRun(t, f, id, "", "someone@example.com", false)
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	err = runstore.Open(b, appSlug, id).WriteLaunch(ctx, &runstore.Launch{
		Version: 1, RunID: id, Execution: exec, LogURL: consoleURL, LaunchedAt: time.Now(),
	})
	b.Close()
	if err != nil {
		t.Fatal(err)
	}

	f.logging.FailReads = true // any read fails the test below
	out, _, err := execute(t, "logs", "--url", id)
	if err != nil {
		t.Fatalf("logs --url: %v", err)
	}
	if strings.TrimSpace(out) != consoleURL || strings.Count(out, "\n") != 1 {
		t.Fatalf("logs --url = %q, want one line with %q", out, consoleURL)
	}
	if _, _, err := execute(t, "logs", "--url", "--follow", id); ExitCode(err) != ExitUserError {
		t.Fatalf("--url with --follow: %v", err)
	}

	f.logging.FailReads = false
	const view = "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"
	f.appendConfig(t, "log_view: "+view+"\n")
	f.logging.Resource = view
	_, stderr, err := execute(t, "logs", id)
	if err != nil || !strings.Contains(stderr, consoleURL) {
		t.Fatalf("empty hint without the link: %q, %v", stderr, err)
	}
}

// TestLogsURLConflictIsCheckedFirst: --url with --follow or --json is a pure
// usage error that needs no cloud connection or bucket read to detect, so it
// is checked before openCloud. A run ID that was never seeded proves it:
// were the check to run after locateLaunched (which would fail first on an
// unknown run), the error here would name the unknown run instead.
func TestLogsURLConflictIsCheckedFirst(t *testing.T) {
	newCloudFixture(t)
	for _, args := range [][]string{
		{"logs", "--url", "--follow", "20260927-100000-ffff"},
		{"logs", "--url", "--json", "20260927-100000-ffff"},
	} {
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--url prints only the link") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

// TestLogsURLWithNoRecordedLink: an ordinary launch (seedRun, unlike
// TestLogsURL, writes no LogURL) refuses --url with a clear message instead
// of printing an empty line.
func TestLogsURLWithNoRecordedLink(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "someone@example.com", true)
	out, _, err := execute(t, "logs", "--url", id)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no console link was recorded") {
		t.Fatalf("logs --url with no recorded link: out=%q err=%v", out, err)
	}
}
