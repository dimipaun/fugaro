//go:build darwin || linux

package cli

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// ttyEngine is a stage engine whose stdin is a real terminal (no faking of
// stdinIsTerminal), and the way to type at it once a prompt shows.
func ttyEngine(t *testing.T, o *initOptions) (*initEngine, *syncBuf, func(marker string, n int, input string)) {
	t.Helper()
	f := newTTY(t)
	e, out := stageEngine(t, "", o)
	e.r.cmd.SetIn(f.tty)
	e.r.in = f.tty
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
	typeAt("Type the GCP project's ID", 1, "my-proj-42\n")
	typeAt("Type the region the installation's jobs and the scheduler run in", 1, "\n")
	typeAt("Type the Fugaro project's name", 1, "\n") // its default can come from an installation in that project
	typeAt("Bake your repos", 1, "\n")                // the wizard's last question (IS7), and Enter is its suggestion
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

// The plugin stage at a real terminal: the person's being there is the whole of
// the answer — the unknown repository is onboarded with no name typed and no
// [y/N] — and the stage's write is the install in Claude Code, which no gate
// stands in for. The checkout's settings file — a pin an older fugaro left —
// comes out of it byte for byte: the merge that wrote it is gone (U2, D949).
func TestPtyPluginConfirmationAtATerminal(t *testing.T) {
	releaseBuild(t, "0.2.0")
	settings := wiringCheckout(t, settingsWithOthers)
	e, out, _ := ttyEngine(t, &initOptions{})
	// After the engine: the rig takes claude off PATH itself.
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
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
	select {
	case r := <-done:
		if r.err != nil || stateOf(r.res, "plugin") != initflow.Changed {
			t.Fatalf("%v %+v\n%s", r.err, r.res.Stages, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not return")
	}
	if data, _ := os.ReadFile(settings); string(data) != settingsWithOthers {
		t.Errorf("the stage wrote the checkout's settings, and the install is the whole of its write (U2):\n%s", data)
	}
	if !slices.ContainsFunc(callArgs(f.Calls(t)), func(a []string) bool { return slices.Equal(a, argsInstall) }) {
		t.Errorf("the plugin was not installed in Claude Code: %q", callArgs(f.Calls(t)))
	}
	if strings.Contains(out.String(), "Type acme/app to onboard it") || strings.Contains(out.String(), "[y/N]") {
		t.Errorf("a confirmation was asked at the terminal:\n%s", out.String())
	}
}

// The repository stage's own confirmation at a real terminal: the person is
// there, so the engine runs once with nothing typed.
func TestPtyRepositoryConfirmationAtATerminal(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	e, out, _ := ttyEngine(t, &initOptions{githubAppID: "12345"})
	unknownRepo(e)
	runs := 0
	rs := newRepositoryStage(e)
	rs.engineStage.run = func(context.Context) error { runs++; return nil }
	type result struct {
		res *initflow.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
		done <- result{res, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || runs != 1 || stateOf(r.res, "repository") != initflow.Done || !strings.Contains(out.String(), dir) {
			t.Fatalf("%v runs %d %+v\n%s", r.err, runs, r.res.Stages, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not return")
	}
}
