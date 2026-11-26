package localcfg

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestPauseCarrierValidatesAtLoad: the pause block is carried under this file's
// own key and held at load to the rule the node's own decode applies to its
// member (projectpolicy.ValidPause), so a block the credentials service would
// refuse is refused where the operator wrote it — at the write, which reads the
// file back, and not at the publish that carries it into the node (R11 of
// docs/model-credentials-routing, D3184, D3187, D3190).
//
// The one-member cases are the point of asking the rule of the block as it
// would be read: a member is held to the other two's defaults when the file
// states nothing about them, so `fugaro settings set pause.wait_in_job 45m` is
// the wait the default 30m minimum refuses, and a refusal that only compared
// the members the file carries would accept it.
func TestPauseCarrierValidatesAtLoad(t *testing.T) {
	c, err := Parse([]byte(sample + `pause:
  wait_in_job: 2m
  min_pause: 40m
  max_pause: 8h
`))
	if err != nil {
		t.Fatalf("a block the rule accepts was refused: %v", err)
	}
	if c.Pause == nil {
		t.Fatal("the file states a pause block and the config carries none")
	}
	// Read through the accessors the publisher reads, so this test cannot agree
	// with a decode that put the values somewhere no reader looks.
	for _, tc := range []struct {
		member string
		got    time.Duration
		want   time.Duration
	}{
		{"wait_in_job", c.Pause.WaitInJobDuration(), 2 * time.Minute},
		{"min_pause", c.Pause.MinPauseDuration(), 40 * time.Minute},
		{"max_pause", c.Pause.MaxPauseDuration(), 8 * time.Hour},
	} {
		if tc.got != tc.want {
			t.Errorf("pause.%s = %s, want %s", tc.member, tc.got, tc.want)
		}
	}
	// A file that says nothing about the block carries none: the node's own
	// block is what a publish keeps then, and telling "the file states nothing"
	// from "the file states an empty block" is what the merge is built on.
	if silent, err := Parse([]byte(sample)); err != nil || silent.Pause != nil {
		t.Errorf("a file that says nothing about the pause = %+v, %v; want no block", silent.Pause, err)
	}
	if empty, err := Parse([]byte(sample + "pause: {}\n")); err != nil || empty.Pause == nil {
		t.Errorf("a written empty block = %+v, %v; want a block that states nothing", empty.Pause, err)
	}

	// Each refusal names the key the operator wrote — `pause.wait_in_job`, not
	// the node's `pause.wait_in_job` under another name — and the rule is the
	// node's own, so a file this loader accepts is a block the node can carry.
	for name, block := range map[string]string{
		"an in-job floor above the default minimum": "pause: {wait_in_job: 45m}\n",
		"a minimum above the default maximum":       "pause: {min_pause: 8h}\n",
		"a maximum below the default minimum":       "pause: {max_pause: 20m}\n",
		"a wait and a minimum in the wrong order":   "pause: {wait_in_job: 40m, min_pause: 30m}\n",
		"a value that is not a duration":            "pause: {min_pause: half an hour}\n",
	} {
		_, err := Parse([]byte(sample + block))
		if err == nil {
			t.Errorf("%s: the file loaded, and a pause block the node would refuse must not be readable here", name)
			continue
		}
		if !strings.Contains(err.Error(), "pause.") {
			t.Errorf("%s: the refusal does not name the member the operator wrote: %v", name, err)
		}
	}
	// The boundary the rule allows is equality: the in-job floor and the
	// shortest pause may be the same wait, which is a project that waits a
	// reset out in the job exactly as long as a pause would have been.
	if _, err := Parse([]byte(sample + "pause: {wait_in_job: 30m, min_pause: 30m, max_pause: 30m}\n")); err != nil {
		t.Errorf("a block whose three settings are equal was refused: %v", err)
	}
	// The refusal is the node's own rule and not a second copy of it: the two
	// documents agree on every member but the one the file cannot state.
	if err := projectpolicy.ValidPause(c.Pause, "pause"); err != nil {
		t.Errorf("the loader accepted a block the node's own rule refuses: %v", err)
	}
}

// TestPauseIsNeverPublished: the block is this machine's own and never the
// shared config's — a launcher who could write it into the runs bucket's file
// would be holding every teammate's runs to their own pause (Shared's own
// reason, the cascade's one block over). It travels to the policy node
// instead, which the credentials service reads.
func TestPauseIsNeverPublished(t *testing.T) {
	c, err := Parse([]byte(sample + "pause:\n  wait_in_job: 2m\n  min_pause: 40m\n  max_pause: 8h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if published := c.Shared(); published.Pause != nil {
		t.Errorf("the published shared config carries the block: %+v", published.Pause)
	}
}
