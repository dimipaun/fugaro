package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The staleness line follows the front door (design 4.5, task W2.1). W1.2
// split the retired `init` into `fugaro setup` and `fugaro update` and left the
// line's registry naming the retired word, so the two commands a reader
// actually runs carried no line at all while the tombstone -- which exits 1
// before the line can mean anything -- was the only name in it.
//
// The test asserts both directions, and the mutation it catches is the map
// going back to a single verb: `setup` and `update` must be in it, `init` must
// not, and the commands that carried the line before the split must still
// carry it.
func TestTheStalenessLineFollowsTheFrontDoor(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"setup", "update"} {
		if !skillWarnCommands[name] {
			t.Errorf("`fugaro %s` carries no staleness line, and it reads the checkout the pin is in", name)
		}
		c, _, err := root.Find([]string{name})
		if err != nil || c.Name() != name {
			t.Errorf("`fugaro %s` is not a command of this build: %v", name, err)
		}
	}
	if skillWarnCommands["init"] {
		t.Error("the retired tombstone carries the staleness line: it exits 1 before the line is read, and the map would send a reader to a command that was retired")
	}
	for _, name := range []string{"run", "ls", "validate", "doctor"} {
		if !skillWarnCommands[name] {
			t.Errorf("`fugaro %s` lost the staleness line the split never touched", name)
		}
	}
}

// TestTheStalenessLineWaitsForTheUpdateRun (#1836b, reverse direction): the
// line's own remedy is `fugaro update`, so that door is the one command that
// does not say it before it runs — read there it sends the reader to the
// command they are running, over the two steps of that very run it is about —
// and says it at the end instead, where the only thing left to report is what
// the run did not move. The map above is not what this test is about: it is
// the pair of entry points the two calls into the line reach (root.go's
// warnStaleSkills, init.go's warnStaleSkillsAfter), on a checkout whose
// installed plugin is a release behind the pin.
func TestTheStalenessLineWaitsForTheUpdateRun(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	t.Chdir(root)
	installed := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte(`{"version":2,"plugins":{"`+pluginwire.PluginID+`":[{"scope":"user","version":"0.1.0"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	up, _, err := NewRootCmd().Find([]string{updateDoor.name})
	if err != nil || up.Name() != updateDoor.name {
		t.Fatalf("`fugaro %s` is not a command of this build: %v", updateDoor.name, err)
	}
	var said bytes.Buffer
	up.SetErr(&said)

	warnStaleSkills(up)
	if said.Len() != 0 {
		t.Errorf("`fugaro %s` printed the staleness line before its own run, pointing the reader at the command they are running: %q", updateDoor.name, said.String())
	}
	warnStaleSkillsAfter(up)
	if got := said.String(); !strings.Contains(got, "the installed Fugaro plugin is 0.1.0") {
		t.Errorf("`fugaro %s` printed no staleness line at its end, where the install it did not make is the only thing left to say: %q", updateDoor.name, got)
	}
}
