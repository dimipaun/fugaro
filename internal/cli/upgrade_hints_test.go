package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// TestHintsNameUpgrade (decision U15): every hint that used to list
// update-skills or the slash commands names fugaro upgrade --local, and none
// lists the old steps.
func TestHintsNameUpgrade(t *testing.T) {
	texts := map[string]string{
		"pluginFirstRun": pluginFirstRun,
		"pluginRefresh":  pluginRefresh,
		"stale outdated": staleLine(pluginwire.Report{Pin: pluginwire.Outdated, Ref: "v0.1.0", Binary: "0.2.0"}),
		"stale differs":  staleLine(pluginwire.Report{Pin: pluginwire.OK, Install: pluginwire.InstalledDiffers, Ref: "v0.2.0", Binary: "0.2.0", Version: "0.1.0"}),
	}
	for _, s := range []pluginwire.State{pluginwire.Outdated, pluginwire.NotWired, pluginwire.Unpinned, pluginwire.InstalledDiffers, pluginwire.NotInstalled} {
		texts["fix "+string(s)] = s.Fix()
	}
	for name, text := range texts {
		if !strings.Contains(text, "fugaro upgrade --local") {
			t.Errorf("%s does not name fugaro upgrade --local: %q", name, text)
		}
		for _, old := range []string{"update-skills", "/plugin marketplace add", "/plugin install", "/plugin marketplace update"} {
			if strings.Contains(text, old) {
				t.Errorf("%s still says %q: %q", name, old, text)
			}
		}
	}
	if s := staleLine(pluginwire.Report{Pin: pluginwire.Newer, Ref: "v0.3.0", Binary: "0.2.0"}); !strings.Contains(s, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer: %q", s)
	}
}
