// What a tick tells a person (O7).
//
// A tick has two things to say: a run it folded has ended, and it has raised a
// question somebody has to answer. Both are things the cycle itself wrote — the
// other half of the notification rule, level-triggered and without a cursor
// (D1229): the cycle that records an outcome or commits a question is the one
// that says so, and no cycle afterwards repeats it, because the node no longer
// has anything new to say. There is nothing on a decision or an intent that
// remembers "a person was told", and there is nothing to add to the wire
// formats either: what was told is derived from what the cycle wrote.
//
// A sink is a channel, not a record: a notification that could not be delivered
// is reported to the daemon and never fails the cycle (what a cycle must not
// lose is the write behind the notification). The sinks O9 adds — the macOS OS
// notification, the owner's mobile push, the desktop notifier on Linux (Q3) —
// implement this interface; the log sink here is the one the daemon has in
// slice 1.

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// maxNotifyLineRunes caps one notification line. A notification is a glance at
// something that happened, not a document: what a run did in full is in the
// run's own record, and this is the sentence that sends a person there.
const maxNotifyLineRunes = 200

// Kind is what a notification is about.
type Kind string

const (
	// NotifyRunFinished is a run behind one of the node's intents that has
	// ended, folded into its item in the same cycle.
	NotifyRunFinished Kind = "run-finished"
	// NotifyDecisionNeeded is a question the cycle itself committed and
	// nobody has answered — the breaker's, in this slice — so the daemon has
	// stopped starting what the question holds back.
	NotifyDecisionNeeded Kind = "decision-needed"
)

// Kinds is the whole vocabulary.
var Kinds = []Kind{NotifyRunFinished, NotifyDecisionNeeded}

// Notification is one thing a tick says. Subject is the id the event is
// about — the run's id, or the decision's — and Text is the line a person
// reads: free text, cleaned and cut on the way out (G3), so no sink has to
// defend itself against what it is handed.
type Notification struct {
	Kind    Kind
	Subject string
	Text    string
}

// Line is the one line a notification renders to.
func (n Notification) Line() string {
	return cleanLine(fmt.Sprintf("%s %s: %s", n.Kind, n.Subject, n.Text), maxNotifyLineRunes)
}

// Notifier is where a tick's notifications go. It is called after the write
// that made the notification true has landed, and once per event: an error it
// returns is the daemon's to log or count, and never a reason for the cycle to
// undo or repeat anything.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// LogSink is the notifier of a foreground daemon: one cleaned line per
// notification, on the writer it was built with.
type LogSink struct {
	W io.Writer
}

// Notify writes the notification as one line.
func (s LogSink) Notify(_ context.Context, n Notification) error {
	if s.W == nil {
		return errors.New("orchestrator: the log sink has nowhere to write, and a notification nobody sees is not one")
	}
	_, err := fmt.Fprintln(s.W, n.Line())
	return err
}
