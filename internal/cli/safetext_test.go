package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// hostile payloads a run can put in a string the views print (security
// review I1): each must reach the terminal with no control byte left.
var hostile = map[string]string{
	"osc8":      "\x1b]8;;https://evil.example/\x1b\\https://bitbucket.org/acme/app/pull-requests/7\x1b]8;;\x1b\\",
	"osc52":     "ok\x1b]52;c;ZXZpbA==\x07",
	"csi-clear": "\x1b[H\x1b[2Jsucceeded",
	"cr":        "failed\rsucceeded",
	"c1-csi":    "x\u009b2Jy",
	"c1-osc":    "x\u009d52;c;ZXZpbA==\u009cy",
	"invalid":   "x\x9b2Jy",
}

// noControls fails when s holds a byte a terminal would interpret.
func noControls(t *testing.T, what, s string) {
	t.Helper()
	for i, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s: control %U at %d in %q", what, r, i, s)
		}
	}
	if !utf8.ValidString(s) {
		t.Fatalf("%s: invalid UTF-8 in %q", what, s)
	}
	if strings.ContainsRune(s, 0x1b) {
		t.Fatalf("%s: ESC in %q", what, s)
	}
}

func TestSafeText(t *testing.T) {
	for name, in := range hostile {
		noControls(t, name, oneLine(in))
		noControls(t, name, multiLine(in))
	}
	for in, want := range map[string]string{
		hostile["osc8"]:      "https://bitbucket.org/acme/app/pull-requests/7",
		hostile["osc52"]:     "ok",
		hostile["csi-clear"]: "succeeded",
		hostile["cr"]:        "failed?succeeded",
		hostile["c1-csi"]:    "xy",
		hostile["c1-osc"]:    "xy",
		hostile["invalid"]:   "x?2Jy",
		"a\tb\nc":            "a\tb?c",
		"plain · ≈ ascii":    "plain · ≈ ascii",
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
	if got := multiLine("a\nb\rc"); got != "a\nb?c" {
		t.Errorf("multiLine = %q", got)
	}
}

// every hostile payload, joined, as one string a run could store.
func hostileAll() string {
	var b strings.Builder
	for _, k := range []string{"osc8", "osc52", "csi-clear", "cr", "c1-csi", "c1-osc", "invalid"} {
		b.WriteString(hostile[k] + " ")
	}
	return b.String()
}

// The views print strings the run controls through the terminal-safe
// helpers: ls, logs, diagnose, cancel and run (security review I1).
func TestViewsStripTerminalControls(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	evil := hostileAll()
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: exec, Status: runstore.StatusFailed,
		Stage: "fix" + evil, Reason: evil, PR: &runstore.PRRef{Number: 7, URL: "https://example.com/pr/7" + evil},
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, Tests: 1, Failures: 1, Failed: []string{evil}, Flaky: []string{evil}, Warning: evil}}})
	f.logging.AddJSONLines(exec, []byte(`{"time":"`+logTime(time.Second)+`","severity":"INFO","message":`+jsonString(evil+"\nnext")+`,"stage":`+jsonString(evil)+`}`+"\n"))
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	fix := `{"type":"result","subtype":"success","result":` + jsonString("done"+evil) + `}` + "\n"
	_ = runstore.Open(b, appSlug, id).PutFile(context.Background(), "transcripts/implement-1.jsonl", []byte(fix), "application/x-ndjson")
	b.Close()
	for _, args := range [][]string{{"ls"}, {"logs", id}, {"diagnose", id}, {"cancel", "--poll", "10ms", id}} {
		out, errOut, err := execute(t, args...)
		if err != nil {
			t.Fatalf("%v: %v (%s)", args, err, errOut)
		}
		noControls(t, strings.Join(args, " "), out)
		noControls(t, strings.Join(args, " ")+" stderr", errOut)
	}
	// logs can't forge a line of its own with an embedded newline.
	out, _, _ := execute(t, "logs", id)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, "next") {
			t.Fatalf("logs printed a forged line: %q", out)
		}
	}
}

func TestRunOutputStripsTerminalControls(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", false)
	_ = exec
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	_ = runstore.Open(b, appSlug, id).WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: id,
		Execution: appExecution("x1"), LogURL: "https://console.example/" + hostileAll(), LaunchedAt: time.Now()})
	b.Close()
	out, errOut, err := execute(t, "run", "--retry", id)
	if err != nil {
		t.Fatalf("run --retry: %v (%s)", err, errOut)
	}
	noControls(t, "run", out)
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// ErrorText is what the command prints for a failed command's error, which
// can quote a stored value: controls go, newlines stay.
func TestErrorTextStripsTerminalControls(t *testing.T) {
	err := fmt.Errorf("reading run: \x1b]8;;https://evil.example\x07click\x1b]8;;\x07\nsecond line\x1b[2J")
	if got, want := ErrorText(err), "reading run: click\nsecond line"; got != want {
		t.Fatalf("ErrorText = %q, want %q", got, want)
	}
}
