package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// TestHintsNameUpdate: every hint for a state fugaro update fixes names
// fugaro update, and none lists the old update-skills step or sends the owner
// to fugaro upgrade --local, which a coding agent's session can run and which
// is the work-around of the refusal the one command does not need. The
// checkout's plugin step (pluginStep, Task 6) is fugaro update's own, so
// InstalledDiffers and NotInstalled name it too, and both keep their manual
// /plugin commands as the fallback for a machine with no claude on PATH
// (noClaudeHint says the same).
//
// NotWired and Unpinned have no remedy any more (U2 of
// docs/plugin-user-scope/implementation-plan.md, D949): nothing in the tree
// writes a pin, so a repository without one has nothing to do, and a
// marketplace that carries no ref simply follows the default branch. Outdated
// keeps its line: the entry is there, and taking it out is what D943 gives
// fugaro update.
func TestHintsNameUpdate(t *testing.T) {
	named := map[string]string{
		"stale outdated":        staleLine(pluginwire.Report{Pin: pluginwire.Outdated, Ref: "v0.1.0", Binary: "0.2.0"}),
		"stale differs":         staleLine(pluginwire.Report{Pin: pluginwire.OK, Install: pluginwire.InstalledDiffers, Ref: "v0.2.0", Binary: "0.2.0", Version: "0.1.0"}),
		"fix outdated":          pluginwire.Outdated.Fix(),
		"fix installed differs": pluginwire.InstalledDiffers.Fix(),
		"fix not installed":     pluginwire.NotInstalled.Fix(),
	}
	for name, text := range named {
		if !strings.Contains(text, "fugaro update") {
			t.Errorf("%s does not name fugaro update: %q", name, text)
		}
		if strings.Contains(text, "upgrade --local") {
			t.Errorf("%s still sends the owner to fugaro upgrade --local: %q", name, text)
		}
		if strings.Contains(text, "update-skills") {
			t.Errorf("%s still says update-skills: %q", name, text)
		}
	}
	// The warning can land in a coding agent's session, where fugaro update
	// writes nothing by design: the line says where to run it rather than
	// leaving a session to find a way around that refusal.
	if s := named["stale outdated"]; !strings.Contains(s, "in your own terminal") {
		t.Errorf("the stale-pin line does not say where to run fugaro update: %q", s)
	}
	// The machine-side hints keep their manual /plugin commands too: the
	// fallback for a machine with no claude on PATH.
	for name, text := range map[string]string{
		"stale differs":         named["stale differs"],
		"fix installed differs": named["fix installed differs"],
		"fix not installed":     named["fix not installed"],
	} {
		if !strings.Contains(text, "/plugin") {
			t.Errorf("%s lost its working /plugin command: %q", name, text)
		}
	}
	// A repository with no pin is not a repository with something to fix: the
	// merging half is deleted, and a state whose remedy was "run fugaro update"
	// for the pin it would write has none.
	for _, s := range []pluginwire.State{pluginwire.NotWired, pluginwire.Unpinned} {
		if fix := s.Fix(); fix != "" {
			t.Errorf("%s still offers a fix (%q), and nothing in the tree writes a pin any more", s, fix)
		}
	}
	if s := staleLine(pluginwire.Report{Pin: pluginwire.Newer, Ref: "v0.3.0", Binary: "0.2.0"}); !strings.Contains(s, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer: %q", s)
	}
	if f := pluginwire.Newer.Fix(); !strings.Contains(f, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("newer Fix: %q", f)
	}
}
