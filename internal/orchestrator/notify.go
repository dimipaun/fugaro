// What a tick tells a person (O7).
//
// A tick has six things to say: a run it folded has ended, it has raised a
// question somebody has to answer, it has merged a pull request under a
// ceiling that merges and tells somebody (D2030), it has blocked an item on a
// dependency cycle that crosses orchestrators (D3220), a run has suspended
// itself on an exhausted chain until a time, and — that same exhaustion read
// the other way — a run has halted for want of a model (C3). All six are things
// the cycle itself wrote — the other half of the notification rule, level-triggered
// and without a cursor (D1229):
// the cycle that records an outcome, commits a question or lands a merge is the
// one that says so, and no cycle afterwards repeats it, because the node no
// longer has anything new to say. There is nothing on a decision or an intent
// that remembers "a person was told", and there is nothing to add to the wire
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
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
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
	// NotifyMergedNeedsReview is a merge the escalation ladder made under its
	// gate for an item whose ceiling is Pro's or Hotfix's, which is the one
	// ceiling that merges and tells somebody (D2030): the work landed without
	// a person reading it, and the line says how much it left behind.
	NotifyMergedNeedsReview Kind = "merged-needs-review"
	// NotifyCycleBlocked is the escalation of an item the cycle set blocked
	// because it sits on a dependency cycle that crosses orchestrators (D3220):
	// the cycle is on the item's note, and the line says the daemon stopped the
	// item rather than waiting on it for ever. Nothing in the loop can break a
	// cycle — only a person edits a dependency — so this is the one reading of
	// a deadlock a person gets.
	NotifyCycleBlocked Kind = "cycle-blocked"
	// NotifyRunPaused is a run behind one of the node's intents that
	// suspended itself because the chain it draws on is exhausted until a
	// reset further away than the in-job wait (C3, D3184): the pause holds
	// until the instant the credentials service computed, and the resident
	// orchestrator resumes the chain then (D3185, D3186). It is the one kind
	// the mobile push plan delivers when it lands (R13) — this plan builds no
	// channel (D141 stands) — because nothing is held open while the run waits
	// (R12): a person is told their run comes back rather than finding it
	// stopped.
	NotifyRunPaused Kind = "run-paused"
	// NotifyPauseHalted is the same exhaustion read the other way: the chain
	// ran out and the run halted at once instead of suspending itself — the
	// service answered `halt` (D3186, D3187, D3190) or refused a `/pause` it
	// had not answered — so no pause was recorded, nothing will resume the run
	// and a person has to act. It is a kind of its own and not the
	// run-finished line, because the two say opposite things to whoever reads
	// them: one run comes back on its own and the other does not, and a sink
	// is written against the kind and not against a sentence.
	NotifyPauseHalted Kind = "pause-halted"
)

// Kinds is the whole vocabulary.
var Kinds = []Kind{NotifyRunFinished, NotifyDecisionNeeded, NotifyMergedNeedsReview, NotifyCycleBlocked, NotifyRunPaused, NotifyPauseHalted}

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

// pausedNotice is what a tick says about a run that suspended itself: the kind
// the mobile push plan delivers when it lands, the run it is about, and the
// instant it comes back. The instant is the service's and not the run's (R8,
// D3189): no record carries one, so it is read off the node the credentials
// service wrote — epoch milliseconds there, RFC 3339 UTC here, the way a run's
// row spells an instant a person reads.
//
// A pause its launcher cancelled is the one reading of it that changes: a
// cancelled pause is never resumed (D3197), and a line promising this run's
// return would be false.
//
// Which of the two readings it is comes before the title and not after it. The
// line is capped from its end and a title runs to MaxTitleRunes, so a title
// placed ahead of the clause pushes it off the line for every title longer than
// what the rest of the line leaves — and the two readings are then one line,
// with a cancelled pause printed exactly like a run that comes back, which is
// the false promise this notice may not make. The title is what the cap trims
// instead: which work a run was is a glance at a row, and whether anything
// resumes it is the whole of what a person does next.
func pausedNotice(runID, title string, n runstore.PauseNode) Notification {
	at := time.UnixMilli(n.ResumeAt).UTC().Format(time.RFC3339)
	text := fmt.Sprintf("run %s paused until %s, and the orchestrator resumes it then (for %q)", runID, at, title)
	if n.Cancelled {
		text = fmt.Sprintf("run %s paused until %s, and its launcher cancelled the pause, so nothing resumes it (for %q)", runID, at, title)
	}
	return Notification{Kind: NotifyRunPaused, Subject: runID, Text: text}
}

// pauseHaltedNotice is the other reading of the same exhaustion: a run that
// halted with no credential (C3's `halt`) ended where it stood instead of
// suspending itself, so no pause holds it and no resume is coming. It is the
// reason that makes it this notice and not the run-finished line: every other
// halt — a cap, the kill switch, a governance tripwire — is a run the ladder
// already knows about, and this one is the pause mechanism's own failure. What
// the halt carries is the sentence its operator read — for this reason, the
// reset the service named — and that is the one thing that tells a person
// whether waiting is worth another launch, so it travels with the notice rather
// than being summarised away.
//
// The sentence is clipped here, to the room the fixed words leave, and not left
// to the cap: the cap cuts from the end of the line, and the instant a person
// acts on sits about a hundred runes into the sentences a run really records
// ("the credential service answered that no credential was usable … the last
// until <instant>", internal/runner's own words), so a sentence appended whole
// arrives as its opening and never reaches the fact it was included for. The
// item's title is what gives way to that room: the subject already names the
// run, and which work it stopped is on the run's own row, where the instant it
// waits for is not.
func pauseHaltedNotice(runID string, h Halt) Notification {
	const head = "halted, nothing resumes it"
	room := maxNotifyLineRunes - len([]rune(string(NotifyPauseHalted)+" "+runID+": "+head+": "))
	text := head
	if d := cleanLine(strings.TrimSpace(h.Detail), room); d != "" {
		text += ": " + d
	}
	return Notification{Kind: NotifyPauseHalted, Subject: runID, Text: text}
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
