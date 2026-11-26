package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/spf13/cobra"
)

// waitForLaterStep blocks a fake rebuild until a step the update runs after the
// images stage has written its marker, and says whether it did. A run that
// rebuilds in line never lets it: the rebuild would be waiting on a step that
// cannot start until it returns.
func waitForLaterStep(r *convergeRig) bool {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if r.db.Value(rules.PathOrchestratorRulesMarker()) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestUpdateRunsItsOtherStepsWhileACatalogImageBuilds: the images stage starts
// the stale entry's rebuild and returns, the rules step that comes after it
// runs while the build is in flight, and the run waits for the build before it
// ends, saying the same thing it said when the stage rebuilt in line.
func TestUpdateRunsItsOtherStepsWhileACatalogImageBuilds(t *testing.T) {
	r, built, _ := catalogStaleRig(t, pinnedRef(config.BaseKind, convergePrev), pinnedRef(config.BaseKind, convergeRelease))
	sawLater := false
	prev := catalogRebuild
	catalogRebuild = func(cmd *cobra.Command, entry string, o imageBuildOptions) error {
		sawLater = waitForLaterStep(r)
		return prev(cmd, entry, o)
	}
	t.Cleanup(func() { catalogRebuild = prev })

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the update did not converge (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if !sawLater {
		t.Errorf("the rebuild never saw a later step run: the images stage waited for it instead of starting it:\n%s", out)
	}
	if got := strings.Join(*built, ","); got != "server" {
		t.Errorf("the run rebuilt %q, want the stale entry of this checkout alone", got)
	}
	if want := "rebuilt 1 catalog image(s) FROM this release's base image: server"; !strings.Contains(out, want) {
		t.Errorf("the run does not say what the rebuild came to:\n%s\nwant %q", out, want)
	}
	if strings.Contains(out, "not covered") {
		t.Errorf("a run whose only rebuild finished left a step behind:\n%s", out)
	}
}

// TestUpdateKeepsItsOtherStepsWhenACatalogBuildFails: a rebuild that ends in
// failure while the later steps ran fails nothing but its own entry: the rules
// the run deployed in the meantime stay, the failure is named with its reason
// and the command, and the run ends not finished.
func TestUpdateKeepsItsOtherStepsWhenACatalogBuildFails(t *testing.T) {
	r, _, _ := catalogStaleRig(t, pinnedRef(config.BaseKind, convergePrev), pinnedRef(config.BaseKind, convergeRelease))
	catalogRebuild = func(_ *cobra.Command, entry string, _ imageBuildOptions) error {
		waitForLaterStep(r)
		return errors.New("Cloud Build ended FAILURE for " + entry)
	}

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("a run whose rebuild failed did not end not finished (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if r.db.Value(rules.PathOrchestratorRulesMarker()) == nil {
		t.Errorf("the failed build took the rules step down with it:\n%s", out)
	}
	all := out + errOut
	for _, want := range []string{"server: NOT rebuilt: its rebuild did not finish (Cloud Build ended FAILURE for server)", "fugaro image build server"} {
		if !strings.Contains(all, want) {
			t.Errorf("the run does not say %q:\n%s", want, all)
		}
	}
}
