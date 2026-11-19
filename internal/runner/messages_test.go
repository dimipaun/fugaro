package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runmsg"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// messagesOn gives the harness the runs bucket the message channel lives in:
// Deps.Bucket is nil in the harness, which disables the branch lock and the
// caches as well as this (Deps.Bucket's own comment).
func (h *harness) messagesOn() { h.deps.Bucket = blobx.Wrap(h.bucket) }

// tell writes one message into run's prefix, which is what `fugaro tell` does
// from the launcher's side (RM1): the numbering and the store are the real
// ones, and only the access check around them is the CLI's.
func (h *harness) tell(t *testing.T, run, text string) int {
	t.Helper()
	seq, err := runmsg.Put(context.Background(), blobx.Wrap(h.bucket), repoSlug, run,
		runmsg.Message{Version: runmsg.Format, Text: text, SentBy: "dimi"})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// during is a step that writes one message as the stage runs, which is a
// message arriving while the run goes on.
func (h *harness) during(run, text string, wrap step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		h.tell(t, run, text)
		return wrap(t, ctx, req)
	}
}

// promptOf is the prompt of the one stage that holds want.
func promptOf(t *testing.T, h *harness, want string) string {
	t.Helper()
	for _, c := range h.agent.calls {
		if strings.Contains(c.Prompt, want) {
			return c.Prompt
		}
	}
	t.Fatalf("no stage's prompt holds %q", want)
	return ""
}

// delivery is the record's own entry for one message.
func delivery(t *testing.T, rec *runstore.Record, seq int) runmsg.Delivery {
	t.Helper()
	if rec.Messages == nil {
		t.Fatal("the record carries no messages block at all")
	}
	for _, d := range rec.Messages.Messages {
		if d.Seq == seq {
			return d
		}
	}
	t.Fatalf("the record says nothing about message %d: %+v", seq, rec.Messages)
	return runmsg.Delivery{}
}

// TestMessageReachesNextFix is RM2's red step: a message sent while the
// implement stage runs reaches the next coder stage's prompt — the fix — and
// the record says it was delivered there (rule 1, D2943).
func TestMessageReachesNextFix(t *testing.T) {
	h := newHarness(t, "", nil)
	h.messagesOn()
	rec, err := h.run(t,
		h.during(runID, "use the helper in internal/foo", implement("feature")),
		review("changes", 1),
		fixStep,
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	fix := promptOf(t, h, "asked for these fixes")
	if !strings.Contains(fix, "use the helper in internal/foo") {
		t.Errorf("the fix prompt carries no message:\n%s", fix)
	}
	d := delivery(t, rec, 1)
	if d.State != runmsg.StateDelivered || d.Stage != "fix" || d.DeliveredAt == nil {
		t.Fatalf("the record's delivery of message 1 = %+v, want it delivered into the fix", d)
	}
}
