package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

const settingsWithOthers = `{
  "permissions": { "allow": ["Bash(go test:*)"] },
  "extraKnownMarketplaces": { "acme": { "source": { "source": "github", "repo": "acme/tools" } } },
  "enabledPlugins": { "acme@acme": true },
  "model": "sonnet"
}
`

func releaseBuild(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

// wiringCheckout is a checkout (cwd) with settings as .claude/settings.json
// ("" for none), and the path of that file.
func wiringCheckout(t *testing.T, settings string) string {
	t.Helper()
	return wiringCheckoutAt(t, githubOrigin, settings)
}

func wiringCheckoutAt(t *testing.T, origin, settings string) string {
	t.Helper()
	dir := repoCheckout(t, origin, "")
	t.Chdir(dir)
	p := filepath.Join(dir, ".claude", "settings.json")
	if settings != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func runPlugin(t *testing.T, e *initEngine) *initflow.Result {
	t.Helper()
	res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e)}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The wiring is confirmed first (declined: nothing is written, the one-line
// command that does it is left for the user), shows the diff, and merges only
// the plugin's two entries into the file, pinned to the binary's tag.
func TestWiringStageConfirmsAndMergesOnly(t *testing.T) {
	releaseBuild(t, "0.2.0")
	settings := wiringCheckout(t, settingsWithOthers)
	fakeTerminal(t)

	// Declined at the prompt (or anything but yes).
	e, out := stageEngine(t, "n\n", nil)
	res := runPlugin(t, e)
	got, _ := os.ReadFile(settings)
	if string(got) != settingsWithOthers || stateOf(res, "plugin") != initflow.NeedsYou || len(res.Left) != 1 || !strings.HasSuffix(res.Left[0].Text, "update-skills") {
		t.Fatalf("declined: stages %+v left %v\n%s", res.Stages, res.Left, got)
	}
	if !strings.Contains(out.String(), "Write this to") || !strings.Contains(out.String(), `+    "fugaro": {`) && !strings.Contains(out.String(), `"fugaro"`) {
		t.Errorf("the diff was not shown before the question:\n%s", out.String())
	}

	// Confirmed.
	e, out = stageEngine(t, "y\n", nil)
	res = runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed || len(res.Left) != 0 {
		t.Fatalf("stages %+v left %v", res.Stages, res.Left)
	}
	var m map[string]json.RawMessage
	data, _ := os.ReadFile(settings)
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	for _, k := range []string{"permissions", "model"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%s lost:\n%s", k, data)
		}
	}
	for _, want := range []string{`"acme@acme": true`, `"acme/tools"`, `"fugaro@fugaro": true`, `"repo": "dimipaun/fugaro"`, `"ref": "v0.2.0"`, `Bash(go test:*)`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the merged file lacks %s:\n%s", want, data)
		}
	}
	for _, want := range []string{"Updated", "git diff", "installs by itself", "/plugin marketplace update fugaro"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output lacks %q:\n%s", want, out.String())
		}
	}
	// The second run has nothing to do, and says so.
	e, _ = stageEngine(t, "", nil)
	res = runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Done || !strings.Contains(st.Detail, "No changes") {
		t.Errorf("rerun: %+v", st)
	}
	// --yes confirms without a question.
	if err := os.WriteFile(settings, []byte(settingsWithOthers), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out = stageEngine(t, "", &initOptions{yes: true})
	if res = runPlugin(t, e); stateOf(res, "plugin") != initflow.Changed || strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "confirmed by --yes") {
		t.Errorf("--yes: %+v\n%s", res.Stages, out.String())
	}
}

// Without a terminal and without --yes nothing is asked and nothing is
// written: the loop refuses to go on.
func TestWiringStageNeedsConfirmation(t *testing.T) {
	releaseBuild(t, "0.2.0")
	settings := wiringCheckout(t, settingsWithOthers)
	e, _ := stageEngine(t, "y\n", nil)
	_, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e)}, e.options())
	var nt *initflow.NoTerminalError
	if !errors.As(err, &nt) {
		t.Fatalf("err %v", err)
	}
	if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers {
		t.Errorf("written without a confirmation:\n%s", got)
	}
	// The plan-only run shows the diff and writes nothing.
	e, out := stageEngine(t, "", &initOptions{planOnly: true})
	res := runPlugin(t, e)
	if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers || stateOf(res, "plugin") != initflow.Todo || !strings.Contains(out.String(), "fugaro@fugaro") {
		t.Errorf("plan-only: %+v\n%s", res.Stages, out.String())
	}
}

// Outside a checkout there is nothing to write: the settings to add are
// printed, once, and the stage is skipped.
func TestWiringStageOutsideCheckoutPrintsSnippet(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Chdir(t.TempDir())
	e, out := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "not in a checkout") {
		t.Fatalf("%+v", st)
	}
	if strings.Count(out.String(), `"fugaro@fugaro": true`) != 1 || !strings.Contains(out.String(), `"ref": "v0.2.0"`) || !strings.Contains(out.String(), ".claude/settings.json") {
		t.Errorf("the snippet:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(".claude", "settings.json")); err == nil {
		t.Error("a settings file was written outside a checkout")
	}
}

// A dev build has no tag to pin to: skipped with the reason, nothing written.
func TestWiringStageDevBuildSkipped(t *testing.T) {
	settings := wiringCheckout(t, "")
	e, _ := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "no release tag") {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Error("a dev build wrote the settings")
	}
}

// A marketplace that is not Fugaro's is never moved without --allow-fork; a
// file that is not JSON is never rewritten; both are the user's.
func TestWiringStageLeavesWhatIsNotItsOwn(t *testing.T) {
	releaseBuild(t, "0.2.0")
	fork := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"acme/fugaro-fork","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}` + "\n"
	settings := wiringCheckout(t, fork)
	e, _ := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if got, _ := os.ReadFile(settings); string(got) != fork || stateOf(res, "plugin") != initflow.NeedsYou || len(res.Left) != 1 ||
		!strings.Contains(res.Left[0].Text, "--allow-fork") || !strings.Contains(res.Stages[0].Detail, "acme/fugaro-fork") {
		t.Fatalf("fork: %+v left %v\n%s", res.Stages, res.Left, got)
	}
	// With --allow-fork only its ref moves.
	e, _ = stageEngine(t, "", &initOptions{yes: true, allowFork: true})
	res = runPlugin(t, e)
	got, _ := os.ReadFile(settings)
	if stateOf(res, "plugin") != initflow.Changed || !strings.Contains(string(got), "acme/fugaro-fork") || !strings.Contains(string(got), `"v0.2.0"`) || strings.Contains(string(got), "dimipaun") {
		t.Fatalf("allow-fork: %+v\n%s", res.Stages, got)
	}
	// Invalid JSON: named, untouched.
	if err := os.WriteFile(settings, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ = stageEngine(t, "", &initOptions{yes: true})
	res = runPlugin(t, e)
	got, _ = os.ReadFile(settings)
	if string(got) != "{not json" || stateOf(res, "plugin") != initflow.NeedsYou || !strings.Contains(res.Stages[0].Detail, "settings.json") || len(res.Left) != 1 || strings.Contains(res.Left[0].Text, "\n") {
		t.Fatalf("invalid: %+v left %v\n%s", res.Stages, res.Left, got)
	}
}

// A fugaro.yaml that cannot be read (a symlink, a FIFO, an oversize file) is
// unknown, not a pass: the stage cannot tell the checkout is this project's,
// so nothing is wired and the user is told why. A missing one is the
// first-run flow and is wired.
func TestWiringStageUnreadableConfigIsUnknown(t *testing.T) {
	releaseBuild(t, "0.2.0")
	for name, make := range map[string]func(t *testing.T, p string){
		"a symlink": func(t *testing.T, p string) {
			target := filepath.Join(t.TempDir(), "other.yaml")
			if err := os.WriteFile(target, []byte(checkoutYAML("github", "oauth", "borealis", "")), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
		},
		"oversize": func(t *testing.T, p string) {
			if err := os.WriteFile(p, make1MiB(), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			settings := wiringCheckout(t, settingsWithOthers)
			make(t, filepath.Join(filepath.Dir(filepath.Dir(settings)), "fugaro.yaml"))
			e, _ := stageEngine(t, "", &initOptions{yes: true})
			res := runPlugin(t, e)
			if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers {
				t.Errorf("wired in a checkout whose fugaro.yaml could not be read:\n%s", got)
			}
			if stateOf(res, "plugin") != initflow.NeedsYou || len(res.Left) != 1 || !strings.Contains(res.Stages[0].Detail, "fugaro.yaml") {
				t.Fatalf("%+v left %+v", res.Stages, res.Left)
			}
		})
	}
}

func make1MiB() []byte { return []byte(strings.Repeat("#", maxFugaroYAML+2)) }
