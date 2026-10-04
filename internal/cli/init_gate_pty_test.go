//go:build darwin || linux

package cli

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// ttyEngine is a stage engine whose stdin is a real terminal (no faking of
// stdinIsTerminal), and the way to type at it once a prompt shows.
func ttyEngine(t *testing.T, o *initOptions) (*initEngine, *syncBuf, func(marker string, n int, input string)) {
	t.Helper()
	f := newTTY(t)
	e, out := stageEngine(t, "", o)
	e.r.cmd.SetIn(f.tty)
	e.r.in = bufio.NewReader(f.tty)
	typeAt := func(marker string, n int, input string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); strings.Count(out.String(), marker) < n; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("no prompt %q (%d): %q", marker, n, out.String())
			}
		}
		if _, err := f.master.WriteString(input); err != nil {
			t.Fatal(err)
		}
	}
	return e, out, typeAt
}

// The first-run prompts at a real terminal: suggestions taken with Enter,
// one answer typed.
func TestPtyFirstRunPrompts(t *testing.T) {
	firstRunEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj-1")
	root := repoCheckout(t, githubOrigin, "")
	t.Chdir(root)
	e, out, typeAt := ttyEngine(t, &initOptions{})
	done := make(chan error, 1)
	go func() { done <- e.r.gatherInputs(t.Context()) }()
	typeAt("Fugaro project name", 1, "\n")
	typeAt("GCP project ID", 1, "my-proj-42\n")
	typeAt("Region", 1, "\n")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gatherInputs did not return")
	}
	if o := e.r.o; o.name != "acme" || o.cloud.gcpProject != "my-proj-42" || o.cloud.region != "us-east5" || !strings.Contains(out.String(), "suggested from the origin's owner") {
		t.Errorf("name %q project %q region %q\n%s", o.name, o.cloud.gcpProject, o.cloud.region, out.String())
	}
}

// The plugin's [y/N] and the typed repository confirmation at a real
// terminal: the repository's name is typed once, then the write is confirmed.
func TestPtyPluginConfirmAndTypedRepo(t *testing.T) {
	releaseBuild(t, "0.2.0")
	settings := wiringCheckout(t, settingsWithOthers)
	e, out, typeAt := ttyEngine(t, &initOptions{})
	unknownRepo(e)
	type result struct {
		res *initflow.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e)}, e.options())
		done <- result{res, err}
	}()
	typeAt("Type acme/app to onboard it", 1, "acme/app\n")
	typeAt("[y/N]", 1, "y\n")
	select {
	case r := <-done:
		if r.err != nil || stateOf(r.res, "plugin") != initflow.Changed {
			t.Fatalf("%v %+v\n%s", r.err, r.res.Stages, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not return")
	}
	if data, _ := os.ReadFile(settings); !strings.Contains(string(data), `"fugaro@fugaro": true`) {
		t.Errorf("not wired:\n%s", data)
	}
}
