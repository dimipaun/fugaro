package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
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
	err := runImageRefresh(cmd, refreshOptions{dir: r.dir, yes: true, rerun: "fugaro upgrade --yes " + r.root, onPlan: func(p *refreshPlan) { seen = p }})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped at step 2 (base)") ||
		!strings.HasSuffix(err.Error(), "rerun fugaro upgrade --yes "+r.root+": finished steps say No changes") {
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
