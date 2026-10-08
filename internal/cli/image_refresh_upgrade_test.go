package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/spf13/cobra"
)

// TestRefreshPreflightUsesDir: the checkout is o.dir's, not the working
// directory's, so fugaro upgrade refreshes several checkouts in one process.
func TestRefreshPreflightUsesDir(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	t.Chdir(t.TempDir()) // not a checkout
	p, err := refreshPreflight(t.Context(), refreshOptions{dir: r.dir}, &initOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.repo != "acme/app" || p.root != r.root || strings.Join(p.kinds, ",") != "go,web-node" {
		t.Fatalf("plan %+v", p)
	}
}

// TestRefreshRerunLineAndOnPlan: with rerun set, a stop names that line
// alone (it already names the checkout), and onPlan sees the plan first.
func TestRefreshRerunLineAndOnPlan(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "base", userErr("declined"))
	var seen *refreshPlan
	cmd := newImageRefreshCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(t.Context())
	err := runImageRefresh(cmd, refreshOptions{dir: r.dir, yes: true, rerun: "fugaro upgrade --yes " + quoteWord(r.root), onPlan: func(p *refreshPlan) { seen = p }})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped at step 2 (base)") ||
		!strings.HasSuffix(err.Error(), "rerun fugaro upgrade --yes "+quoteWord(r.root)+": finished steps say No changes") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if seen == nil || seen.repo != "acme/app" || strings.Join(*ran, ",") != "base" {
		t.Fatalf("onPlan saw %+v, steps %q", seen, *ran)
	}
}

func TestRefreshPlanCurrent(t *testing.T) {
	cur := managedRef("go", "0.5.1")
	p := &refreshPlan{kinds: []string{"go"}, want: map[string]string{"go": cur}, lc: &localcfg.Config{BaseImages: map[string]string{"go": cur}}}
	if !p.current() {
		t.Fatal("a recorded base and no build is not current")
	}
	p.builds = []string{"api"}
	if p.current() {
		t.Fatal("a build to run is current")
	}
	p.builds = nil
	p.lc.BaseImages["go"] = managedRef("go", "0.5.0")
	if p.current() {
		t.Fatal("a base to copy is current")
	}
}

// inProcessCmd is a command with a closed, non-terminal stdin and a buffer.
func inProcessCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := newImageRefreshCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(t.Context())
	return cmd, &out
}

// The agent refusal holds on the in-process path too (dir, rerun, onPlan:
// fugaro upgrade's), --yes included: nothing is read, no hook fires, no
// step runs and nothing is written.
// Mutation (run, restore): skip refuseRefreshHere when o.dir or o.rerun is
// set, and every row fails (the hook fires).
func TestRefreshInProcessRefusedInAgentSession(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			r := newAnchorModeRig(t, refreshYAML, true)
			t.Setenv(marker, "1")
			t.Chdir(t.TempDir())
			ran := fakeSteps(t, "", nil)
			noRecordReads(t)
			cmd, out := inProcessCmd(t)
			hooked := false
			err := runImageRefresh(cmd, refreshOptions{dir: r.dir, yes: true, rerun: "fugaro upgrade --yes " + quoteWord(r.root), onPlan: func(*refreshPlan) { hooked = true }})
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "fugaro image refresh applies cloud changes: "+initflow.AgentRefusal(marker)) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if hooked || len(*ran) != 0 || out.Len() != 0 {
				t.Fatalf("hook %v, steps %v, output %q", hooked, *ran, out)
			}
			r.check(t, refreshYAML)
		})
	}
}

// Without yes the in-process path still needs a real terminal.
func TestRefreshInProcessNeedsATerminal(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "", nil)
	cmd, _ := inProcessCmd(t)
	hooked := false
	err := runImageRefresh(cmd, refreshOptions{dir: r.dir, rerun: "fugaro upgrade " + quoteWord(r.root), onPlan: func(*refreshPlan) { hooked = true }})
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), initflow.NoTerminalAdvice) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if hooked || len(*ran) != 0 {
		t.Fatalf("hook %v, steps %v", hooked, *ran)
	}
}

// The custom-base refusal names the caller's rerun line (as its "then rerun" step) when one is
// set, and with again() otherwise.
// Mutation (run, restore): use o.again() instead of o.rerunLine() in
// refreshPreflight's customBaseRefusal call and the override row fails.
func TestRefreshCustomBaseRefusalRerunLine(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	r.appendConfig(t, "base_images: {go: "+refreshHost+"/fugaro-base/fugaro-go:dev-abc}\n")
	rerun := "fugaro upgrade --yes " + quoteWord("/work/my checkout")
	_, err := refreshPreflight(t.Context(), refreshOptions{workflows: []string{"api"}, rerun: rerun}, &initOptions{})
	if err == nil || !strings.Contains(err.Error(), "then rerun "+rerun+". To keep your own base image, rebuild from it with fugaro image build instead") {
		t.Fatalf("override: %v", err)
	}
	if strings.Contains(err.Error(), "fugaro image refresh --workflow") || !strings.Contains(err.Error(), "'/work/my checkout'") {
		t.Fatalf("override not used alone or not shell-safe: %v", err)
	}
	_, err = refreshPreflight(t.Context(), refreshOptions{workflows: []string{"api"}}, &initOptions{})
	if err == nil || !strings.Contains(err.Error(), "then rerun fugaro image refresh --workflow api. To keep your own base image") {
		t.Fatalf("default: %v", err)
	}
}

// onPlan gets a copy: a hook that changes it changes nothing the run does.
// Mutation (run, restore): pass p instead of p.snapshot() and it fails.
func TestRefreshOnPlanGetsACopy(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	t.Chdir(t.TempDir())
	var real *refreshPlan
	old := newRefreshSteps
	newRefreshSteps = func(_ *initRun, _ *initEngine, p *refreshPlan) refreshSteps {
		real = p
		return refreshSteps{
			base:     func(context.Context) error { return userErr("stop") },
			reload:   func(context.Context) (*refreshTarget, error) { return nil, nil },
			checkJob: func(context.Context, *refreshTarget) error { return nil },
			builds:   func(context.Context, *refreshTarget) error { return nil },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	wantKinds, wantBuilds, wantLC := "", "", ""
	cmd, _ := inProcessCmd(t)
	err := runImageRefresh(cmd, refreshOptions{dir: r.dir, yes: true, onPlan: func(p *refreshPlan) {
		wantKinds, wantBuilds, wantLC = strings.Join(p.kinds, ","), strings.Join(p.builds, ","), fmt.Sprint(p.lc.BaseImages)
		p.kinds[0] = "mutated"
		p.builds = append(p.builds, "mutated")
		p.want["go"] = "mutated"
		p.lc.BaseImages = map[string]string{"go": "mutated"}
		p.lc.Name = "mutated"
	}})
	if err == nil || real == nil {
		t.Fatalf("err %v, plan %v", err, real)
	}
	if got := strings.Join(real.kinds, ","); got != wantKinds || got != "go,web-node" {
		t.Errorf("kinds %q, want %q", got, wantKinds)
	}
	if got := strings.Join(real.builds, ","); got != wantBuilds || real.want["go"] == "mutated" ||
		fmt.Sprint(real.lc.BaseImages) != wantLC || real.lc.Name == "mutated" {
		t.Errorf("the run's plan changed: builds %q, want %v, lc %v %q", got, real.want, real.lc.BaseImages, real.lc.Name)
	}
}

// With o.dir set, the texts name the directory, not "this checkout" or
// "this directory"; without it they are the long-standing ones.
func TestRefreshNamesTheDirectory(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	_, err := refreshPreflight(t.Context(), refreshOptions{dir: elsewhere}, &initOptions{})
	if err == nil || !strings.Contains(err.Error(), "and the directory "+elsewhere+" is not in one") || strings.Contains(err.Error(), "this directory") {
		t.Fatalf("no checkout, dir: %v", err)
	}
	_, err = refreshPreflight(t.Context(), refreshOptions{}, &initOptions{})
	if err == nil || !strings.Contains(err.Error(), "and this directory is not in one") {
		t.Fatalf("no checkout: %v", err)
	}
	_, err = refreshPreflight(t.Context(), refreshOptions{dir: r.dir, repo: "acme/other"}, &initOptions{})
	if err == nil || !strings.Contains(err.Error(), "is not the checkout "+r.root+"'s origin") || strings.Contains(err.Error(), "this checkout") {
		t.Fatalf("--repo mismatch, dir: %v", err)
	}

	// The end notes and the stop line, through a whole run.
	ran := fakeSteps(t, "", nil)
	cmd, out := inProcessCmd(t)
	if err := runImageRefresh(cmd, refreshOptions{dir: r.dir, yes: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "next: run fugaro init --anchor in the checkout "+r.root+" to add") || strings.Contains(out.String(), "in this checkout to add") {
		t.Errorf("anchor note, dir:\n%s", out)
	}
	fakeSteps(t, "base", userErr("declined"))
	cmd, _ = inProcessCmd(t)
	err = runImageRefresh(cmd, refreshOptions{dir: r.dir, yes: true})
	if err == nil || !strings.HasSuffix(err.Error(), " in the checkout "+r.dir+": finished steps say No changes") {
		t.Fatalf("stop, dir: %v", err)
	}
	_ = ran
}
