package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// TestHintsNameUpgrade (decision U15): every hint for a state the pin step
// fixes names fugaro upgrade --local and none lists the old update-skills
// step. The plugin step (pluginStep, Tasks 6-7) is not in this build yet
// (upgrade.go wires "plugin" to notImplementedStep), so hints for states only
// the plugin step can fix still give the manual /plugin commands that
// actually work today; they must not claim fugaro upgrade --local does
// something it cannot do yet.
func TestHintsNameUpgrade(t *testing.T) {
	pinFixed := map[string]string{
		"stale outdated": staleLine(pluginwire.Report{Pin: pluginwire.Outdated, Ref: "v0.1.0", Binary: "0.2.0"}),
	}
	for _, s := range []pluginwire.State{pluginwire.Outdated, pluginwire.NotWired, pluginwire.Unpinned} {
		pinFixed["fix "+string(s)] = s.Fix()
	}
	for name, text := range pinFixed {
		if !strings.Contains(text, "fugaro upgrade --local") {
			t.Errorf("%s does not name fugaro upgrade --local: %q", name, text)
		}
		if strings.Contains(text, "update-skills") {
			t.Errorf("%s still says update-skills: %q", name, text)
		}
	}
	if s := staleLine(pluginwire.Report{Pin: pluginwire.Newer, Ref: "v0.3.0", Binary: "0.2.0"}); !strings.Contains(s, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer: %q", s)
	}
	if f := pluginwire.Newer.Fix(); !strings.Contains(f, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer Fix: %q", f)
	}

	pluginOnly := map[string]string{
		"pluginFirstRun":        pluginFirstRun,
		"pluginRefresh":         pluginRefresh,
		"stale differs":         staleLine(pluginwire.Report{Pin: pluginwire.OK, Install: pluginwire.InstalledDiffers, Ref: "v0.2.0", Binary: "0.2.0", Version: "0.1.0"}),
		"fix installed differs": pluginwire.InstalledDiffers.Fix(),
		"fix not installed":     pluginwire.NotInstalled.Fix(),
	}
	for name, text := range pluginOnly {
		if strings.Contains(text, "fugaro upgrade --local") {
			t.Errorf("%s names fugaro upgrade --local, but this build's plugin step is not implemented: %q", name, text)
		}
		if !strings.Contains(text, "/plugin") {
			t.Errorf("%s lost its working /plugin command: %q", name, text)
		}
	}
}
