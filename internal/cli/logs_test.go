package cli

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
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
