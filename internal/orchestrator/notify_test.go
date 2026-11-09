package orchestrator_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The notification tests (O7).
//
// A notification leaves the daemon and reaches a person, so what is pinned here
// is what a sink is handed: one cleaned line, one per event, and a vocabulary
// of kinds that a sink can be written against without a second one appearing
// quietly.

// TestNotifyLineIsOneCleanedLine is G3 at the last hop: whatever a cycle built a
// notification's text from, the line a sink writes carries no terminal control
// character and is cut to the cap the package publishes. Tabs go through: they
// are what safetext keeps, and they cannot move a cursor.
func TestNotifyLineIsOneCleanedLine(t *testing.T) {
	n := orchestrator.Notification{
		Kind:    orchestrator.NotifyRunFinished,
		Subject: "20261107-090000-abcd",
		Text:    "run ended\n\x1b[31mfailed\x1b[0m\rfor \t\"wi-1\"",
	}
	line := n.Line()
	if strings.ContainsAny(line, "\n\r\x1b") {
		t.Errorf("the line carries a terminal control character: %q", line)
	}
	if !strings.Contains(line, "20261107-090000-abcd") {
		t.Errorf("the line does not name what it is about: %q", line)
	}
	if len([]rune(line)) > orchestrator.MaxNotifyLineRunes {
		t.Errorf("the line is %d runes and the cap is %d", len([]rune(line)), orchestrator.MaxNotifyLineRunes)
	}

	long := orchestrator.Notification{Kind: orchestrator.NotifyDecisionNeeded, Subject: "breaker-tripped-20261107T120000Z", Text: strings.Repeat("a question ", 100)}
	if got := len([]rune(long.Line())); got > orchestrator.MaxNotifyLineRunes {
		t.Errorf("a long question renders to %d runes and the cap is %d", got, orchestrator.MaxNotifyLineRunes)
	}
}

// TestNotifyLogSinkWritesOneLinePerNotification is the sink slice 1 ships: every
// notification is one line on the writer the daemon was handed, in the order
// they were made.
func TestNotifyLogSinkWritesOneLinePerNotification(t *testing.T) {
	var buf bytes.Buffer
	sink := orchestrator.LogSink{W: &buf}
	ctx := context.Background()
	told := []orchestrator.Notification{
		{Kind: orchestrator.NotifyRunFinished, Subject: "20261107-090000-abcd", Text: "run ended succeeded for \"wi-1\""},
		{Kind: orchestrator.NotifyDecisionNeeded, Subject: "breaker-tripped-20261107T120000Z", Text: "what should it do?"},
	}
	for _, n := range told {
		if err := sink.Notify(ctx, n); err != nil {
			t.Fatalf("the log sink answered %v", err)
		}
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != len(told) {
		t.Fatalf("%d notifications rendered to %d line(s):\n%s", len(told), len(lines), buf.String())
	}
	for i, n := range told {
		if lines[i] != n.Line() {
			t.Errorf("line %d is %q and the notification renders to %q", i+1, lines[i], n.Line())
		}
		if !strings.Contains(lines[i], string(n.Kind)) || !strings.Contains(lines[i], n.Subject) {
			t.Errorf("line %d does not say what it is or what it is about: %q", i+1, lines[i])
		}
	}
}

// TestNotifyLogSinkWithoutAWriterRefuses is the fail-closed half: a sink with
// nowhere to write says so rather than dropping what it was handed. The tick
// records that answer on its report and goes on — the node is the record — but
// a daemon told nothing must not look like a daemon that told somebody.
func TestNotifyLogSinkWithoutAWriterRefuses(t *testing.T) {
	var sink orchestrator.LogSink
	if err := sink.Notify(context.Background(), orchestrator.Notification{Kind: orchestrator.NotifyRunFinished}); err == nil {
		t.Fatalf("a sink with no writer accepted a notification")
	}
}

// TestNotifyKindsAreClosed pins the vocabulary a sink is written against: two
// events, both of them something a cycle itself wrote. A third kind is a
// deliberate change to what a person is told, so it fails here first.
func TestNotifyKindsAreClosed(t *testing.T) {
	want := []orchestrator.Kind{orchestrator.NotifyRunFinished, orchestrator.NotifyDecisionNeeded}
	if got := orchestrator.Kinds; !slices.Equal(got, want) {
		t.Fatalf("this release has the kinds %v and the tests are written against %v", got, want)
	}
}
