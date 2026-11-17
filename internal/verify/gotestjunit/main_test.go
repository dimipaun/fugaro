package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/verify"
)

// stream is a `go test -json` stream as the go tool writes one: a package that
// passes, skips and fails, a package that failed to build, a package with no
// test files, and a package whose test binary died partway -- its `-timeout`
// panicked, so TestHang has a `run` event and no outcome at all and the package
// carries the failure. It is the shape internal/verify's reader has to end up
// seeing.
const stream = `{"Time":"2026-11-17T05:22:09.454Z","Action":"start","Package":"probe/a"}
{"Time":"2026-11-17T05:22:09.455Z","Action":"start","Package":"probe/b"}
{"ImportPath":"probe/b","Action":"build-output","Output":"# probe/b\n"}
{"ImportPath":"probe/b","Action":"build-output","Output":"b/b.go:3:23: undefined: undefinedThing\n"}
{"ImportPath":"probe/b","Action":"build-fail"}
{"Time":"2026-11-17T05:22:09.455Z","Action":"output","Package":"probe/b","Output":"FAIL\tprobe/b [build failed]\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.455Z","Action":"fail","Package":"probe/b","Elapsed":0,"FailedBuild":"probe/b"}
{"Time":"2026-11-17T05:22:09.455Z","Action":"start","Package":"probe/c"}
{"Time":"2026-11-17T05:22:09.455Z","Action":"output","Package":"probe/c","Output":"?   \tprobe/c\t[no test files]\n"}
{"Time":"2026-11-17T05:22:09.455Z","Action":"skip","Package":"probe/c","Elapsed":0}
{"Time":"2026-11-17T05:22:09.456Z","Action":"start","Package":"probe/d"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"run","Package":"probe/d","Test":"TestHang"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/d","Test":"TestHang","Output":"=== RUN   TestHang\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/d","Test":"TestHang","Output":"    d_test.go:5: waiting\n","OutputType":"error"}
{"Time":"2026-11-17T05:22:12.460Z","Action":"output","Package":"probe/d","Output":"panic: test timed out after 3s\n\trunning tests:\n\t\tTestHang (3s)\n","OutputType":"error"}
{"Time":"2026-11-17T05:22:12.460Z","Action":"output","Package":"probe/d","Output":"FAIL\tprobe/d\t3.005s\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:12.460Z","Action":"fail","Package":"probe/d","Elapsed":3.005}
{"Time":"2026-11-17T05:22:09.457Z","Action":"run","Package":"probe/a","Test":"TestPass"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestPass","Output":"=== RUN   TestPass\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestPass","Output":"--- PASS: TestPass (0.00s)\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"pass","Package":"probe/a","Test":"TestPass","Elapsed":0}
{"Time":"2026-11-17T05:22:09.457Z","Action":"run","Package":"probe/a","Test":"TestSkip"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestSkip","Output":"=== RUN   TestSkip\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestSkip","Output":"    a_test.go:6: nope\n"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestSkip","Output":"--- SKIP: TestSkip (0.00s)\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"skip","Package":"probe/a","Test":"TestSkip","Elapsed":0}
{"Time":"2026-11-17T05:22:09.457Z","Action":"run","Package":"probe/a","Test":"TestFail"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestFail","Output":"=== RUN   TestFail\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestFail","Output":"    a_test.go:7: boom\n","OutputType":"error"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Test":"TestFail","Output":"--- FAIL: TestFail (0.00s)\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"fail","Package":"probe/a","Test":"TestFail","Elapsed":0}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Output":"FAIL\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"output","Package":"probe/a","Output":"FAIL\tprobe/a\t0.002s\n","OutputType":"frame"}
{"Time":"2026-11-17T05:22:09.457Z","Action":"fail","Package":"probe/a","Elapsed":0.003}
`

// TestReportReadsTheStream: the report internal/verify reads back holds the
// tests the stream described, with their outcomes and their package, whichever
// order the packages arrived in -- and, where a package's own failure left a
// test without an outcome, the failure that failure implies: TestHang's binary
// died partway, so nothing else in the stream says it failed, and probe/b never
// built, so it has no test to name at all.
func TestReportReadsTheStream(t *testing.T) {
	var echoed strings.Builder
	rep, err := read(strings.NewReader(stream), &echoed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reports", "tree.xml")
	if err := rep.write(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, err := verify.ParseJUnit(f)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cases {
		state := "passed"
		switch {
		case c.Failed:
			state = "failed"
		case c.Skipped:
			state = "skipped"
		}
		got[c.ID] = state
	}
	want := map[string]string{
		"probe/a.TestPass":       "passed",
		"probe/a.TestSkip":       "skipped",
		"probe/a.TestFail":       "failed",
		"probe/b.[build failed]": "failed",
		"probe/d.TestHang":       "failed",
	}
	if len(got) != len(want) {
		t.Fatalf("report holds %v, want %v", got, want)
	}
	for id, state := range want {
		if got[id] != state {
			t.Errorf("%s is %q in the report, want %q (report: %v)", id, got[id], state, got)
		}
	}
}

// TestEchoKeepsWhatPlainGoTestPrints: the log tail is 40 lines read to explain a
// failed run, so the frames `-json` carries for every test are dropped and the
// lines a plain `go test` prints are kept.
func TestEchoKeepsWhatPlainGoTestPrints(t *testing.T) {
	var echoed strings.Builder
	if _, err := read(strings.NewReader(stream), &echoed); err != nil {
		t.Fatal(err)
	}
	out := echoed.String()
	for _, keep := range []string{
		"FAIL\tprobe/b [build failed]\n",
		"b/b.go:3:23: undefined: undefinedThing\n",
		"    a_test.go:7: boom\n",
		"--- FAIL: TestFail (0.00s)\n",
		"panic: test timed out after 3s\n",
		"FAIL\tprobe/d\t3.005s\n",
		"FAIL\tprobe/a\t0.002s\n",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("the log lacks %q; it is:\n%s", keep, out)
		}
	}
	for _, drop := range []string{"=== RUN", "--- PASS:", "--- SKIP:"} {
		if strings.Contains(out, drop) {
			t.Errorf("the log carries %q, which a plain `go test` does not print:\n%s", drop, out)
		}
	}
}

// TestReadRefusesAStreamOfNothing: a command that printed no events is a
// missing `-json`, not a run of zero tests -- recording it as one would leave a
// green verify whose record carries nothing, which is the fault the workflow
// wraps these calls to fix.
func TestReadRefusesAStreamOfNothing(t *testing.T) {
	var echoed strings.Builder
	_, err := read(strings.NewReader("ok  	probe/a	0.002s\n"), &echoed)
	if err == nil || !strings.Contains(err.Error(), "-json") {
		t.Fatalf("err = %v, want a refusal naming -json", err)
	}
	if !strings.Contains(echoed.String(), "ok  \tprobe/a") {
		t.Errorf("the line the command printed was not echoed: %q", echoed.String())
	}
}

// TestExecuteAnswersTheCommandsExitCode: `commands.test`'s exit code is the test
// command's (fugaro.yaml), so the wrapper has to give the child's back -- for a
// failing run and for a passing one -- and still write the report either way.
func TestExecuteAnswersTheCommandsExitCode(t *testing.T) {
	fixture := writeFixture(t)
	for _, tc := range []struct {
		name string
		code int
	}{
		{"a failing command", 3},
		{"a passing command", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reports", "tree.xml")
			var stdout, stderr strings.Builder
			code, err := execute(path, []string{"sh", "-c", "cat " + fixture + "; exit " + strconv.Itoa(tc.code)}, &stdout, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.code {
				t.Errorf("code = %d, want %d (stderr %q)", code, tc.code, stderr.String())
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("no report at %s: %v", path, err)
			}
		})
	}
}

// TestExecuteFailsWhenItCannotWriteTheReport: a report that could not be written
// would leave the record with no counts at all, so the wrapper says so and exits
// non-zero even though the tests passed.
func TestExecuteFailsWhenItCannotWriteTheReport(t *testing.T) {
	fixture := writeFixture(t)
	blocked := filepath.Join(t.TempDir(), "reports")
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code, err := execute(filepath.Join(blocked, "tree.xml"),
		[]string{"sh", "-c", "cat " + fixture + "; exit 0"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want an error for a report that could not be written")
	}
	if code == 0 {
		t.Errorf("code = %d, want non-zero", code)
	}
}

// writeFixture stores the stream where a wrapped command can print it back, and
// answers its path.
func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream.json")
	if err := os.WriteFile(path, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
