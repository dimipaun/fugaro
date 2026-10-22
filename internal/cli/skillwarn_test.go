package cli

import "testing"

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
