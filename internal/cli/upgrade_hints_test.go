package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// TestHintsNameUpgrade (decision U15): every hint for a state fugaro upgrade
// can fix names fugaro upgrade --local and none lists the old update-skills
// step. The plugin step (pluginStep, Task 6) now backs InstalledDiffers,
// NotInstalled, pluginFirstRun and pluginRefresh too, so those hints also
// name it (alongside the manual /plugin commands, kept as the fallback for a
// machine with no claude on PATH).
func TestHintsNameUpgrade(t *testing.T) {
	named := map[string]string{
		"stale outdated":        staleLine(pluginwire.Report{Pin: pluginwire.Outdated, Ref: "v0.1.0", Binary: "0.2.0"}),
		"pluginFirstRun":        pluginFirstRun,
		"pluginRefresh":         pluginRefresh,
		"stale differs":         staleLine(pluginwire.Report{Pin: pluginwire.OK, Install: pluginwire.InstalledDiffers, Ref: "v0.2.0", Binary: "0.2.0", Version: "0.1.0"}),
		"fix installed differs": pluginwire.InstalledDiffers.Fix(),
		"fix not installed":     pluginwire.NotInstalled.Fix(),
	}
	for _, s := range []pluginwire.State{pluginwire.Outdated, pluginwire.NotWired, pluginwire.Unpinned} {
		named["fix "+string(s)] = s.Fix()
	}
	for name, text := range named {
		if !strings.Contains(text, "fugaro upgrade --local") {
			t.Errorf("%s does not name fugaro upgrade --local: %q", name, text)
		}
		if strings.Contains(text, "update-skills") {
			t.Errorf("%s still says update-skills: %q", name, text)
		}
	}
	// The plugin-only hints keep their manual /plugin commands too: a
	// fallback for a machine with no claude on PATH (noClaudeHint says the
	// same).
	for name, text := range map[string]string{
		"pluginFirstRun":        pluginFirstRun,
		"pluginRefresh":         pluginRefresh,
		"stale differs":         named["stale differs"],
		"fix installed differs": named["fix installed differs"],
		"fix not installed":     named["fix not installed"],
	} {
		if !strings.Contains(text, "/plugin") {
			t.Errorf("%s lost its working /plugin command: %q", name, text)
		}
	}
	if s := staleLine(pluginwire.Report{Pin: pluginwire.Newer, Ref: "v0.3.0", Binary: "0.2.0"}); !strings.Contains(s, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer: %q", s)
	}
	if f := pluginwire.Newer.Fix(); !strings.Contains(f, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer Fix: %q", f)
	}
}
