package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// detailSink is a run's log as these tests hand it to one: a buffer with no
// file behind it, which is all report.Sink asks for.
type detailSink struct{ *bytes.Buffer }

func (detailSink) Path() string { return "" }
func (detailSink) Close() error { return nil }

// TestUpdateStaleJobConfigNoteIsOneShortLine: the drift note is one line a
// person reads, and the account of the drift — which keys differ, and which of
// them a published policy has taken over — is the log's and --json's. Terminal
// and log are separate writers here, the way a converge run has them; a run
// with no log still gets both, and the old wall of one sentence per key is gone
// from the terminal either way.
func TestUpdateStaleJobConfigNoteIsOneShortLine(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	fakeTerminal(t)

	out, detail := &syncBuf{}, &syncBuf{}
	o := &initOptions{}
	cmd := newUpdateCmdWith(o)
	cmd.SetContext(context.Background())
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("fugaro update: %v", err)
	}
	loc, ok := pluginwire.Locate(".")
	if !ok {
		t.Fatal("not a checkout")
	}
	u := &stepCtx{cmd: cmd, w: out, loc: loc, detail: detail,
		o: stepOptions{yes: o.yes, refresh: refreshOptions{
			imageSource: o.imageSource, expectDigests: o.expectDigests, cloud: o.cloud,
			in: cmd.InOrStdin(), out: out}},
		agent: agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))}
	if got := cloudStep(context.Background(), u); got.state == stepFailed {
		t.Fatalf("the cloud step failed: %s", got.reason)
	}

	terminal := out.String()
	if !strings.Contains(terminal, "note: "+jobConfigCopyStaleLine) {
		t.Fatalf("the terminal does not carry the one stale line:\n%s", terminal)
	}
	for _, line := range strings.Split(terminal, "\n") {
		if !strings.HasPrefix(line, "note: ") {
			continue
		}
		if len(line) > maxMessageChars {
			t.Errorf("a note on the terminal is %d characters, past the %d a message is allowed:\n%s", len(line), maxMessageChars, line)
		}
	}
	// What the wall used to carry is in the log, and not on the terminal.
	for _, want := range []string{"providers: added openrouter", "model_prices: added deepseek/deepseek-v4-flash"} {
		if !strings.Contains(detail.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, detail.String())
		}
		if strings.Contains(terminal, want) {
			t.Errorf("the terminal carries %q, which is the log's:\n%s", want, terminal)
		}
	}
}

// TestUpdateConfigWriteIsOneLineAndNoBackupLine: a run that writes the local
// config says so in one line — one per repository — and prints no "backed up"
// line and no YAML: those are the log's and --json's. A write that changes
// nothing prints no write line at all.
//
// The same write is what `fugaro update` makes once per repository it onboards,
// so three of them are three lines and never the two-per-write pair the owner's
// run showed.
func TestUpdateConfigWriteIsOneLineAndNoBackupLine(t *testing.T) {
	lc := parseLocalConfig(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
`)
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	cmd := newSetupCmd()
	cmd.SetOut(&out)
	r := newInitRun(cmd, &initOptions{yes: true, cloud: cloudOptions{stderr: discard}})
	log := detailSink{&bytes.Buffer{}}
	r.repoDoor, r.repLog = true, log
	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := r.writeLocalConfig(lc, path, nil, true); err != nil {
		t.Fatal(err)
	}
	first := out.String()
	if n := strings.Count(first, "wrote "+path); n != 1 {
		t.Errorf("the write printed %d write lines, want one:\n%s", n, first)
	}
	for _, unwanted := range []string{"backed up", "changes:"} {
		if strings.Contains(first, unwanted) {
			t.Errorf("the terminal carries %q, which is the log's:\n%s", unwanted, first)
		}
	}

	// A second write of a file that changed: one line, still no backup line on
	// the terminal and the account of it in the log.
	out.Reset()
	log.Reset()
	next := *lc
	next.Name = "aurora-two"
	if err := r.writeLocalConfig(&next, path, readFile(t, path), true); err != nil {
		t.Fatal(err)
	}
	second := out.String()
	if n := strings.Count(second, "wrote "+path); n != 1 {
		t.Errorf("the changed write printed %d write lines, want one:\n%s", n, second)
	}
	for _, unwanted := range []string{"backed up", "changes:"} {
		if strings.Contains(second, unwanted) {
			t.Errorf("the terminal carries %q, which is the log's:\n%s", unwanted, second)
		}
	}
	for _, want := range []string{"changes:", "backed up the old local config to"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, log.String())
		}
	}

	// Nothing changed: no write line at all.
	out.Reset()
	if err := r.writeLocalConfig(&next, path, readFile(t, path), true); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "wrote "+path) {
		t.Errorf("an unchanged config printed a write line:\n%s", got)
	}
}
