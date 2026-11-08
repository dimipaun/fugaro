//go:build darwin || linux

package cli

// report_prompt_pty_test.go is CL7a's G1 pin for the hidden prompt of the
// commands that read a value: the step's line in flight is taken down before
// the prompt is written and stays down until the answer is in (stepRun.ask),
// so neither the frame nor the redraw behind it lands on the line the prompt
// is on, or on the value typed under it. It needs a real terminal, which is
// what the pty fixture is for: off one, nothing is drawn and the rule is
// vacuously true.

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestCredsSetPromptIsNeverDrawnOver is the wrapped call in credsSet — the one
// place the command reads the value — and the one this test would not notice
// going away: an unwrapped read leaves the suite green, because the run's
// account is identical and only a terminal shows the difference.
func TestCredsSetPromptIsNeverDrawnOver(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	f := credsFixtureFor(t)
	f.seed(t, nil)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "creds", "set", config.ClaudeOAuthToken)
	prompt := "Paste the value for " + config.ClaudeOAuthToken + " (input hidden), then press Enter: "
	waitTerminal(t, tty, prompt)
	// Two ticks of the line in flight go by with the prompt unanswered: with
	// the line still drawn, each one lands on it (the reporter redraws every
	// 100ms).
	time.Sleep(300 * time.Millisecond)
	shown := tty.displayed()
	at := strings.Index(shown, prompt)
	if at < 0 {
		t.Fatalf("the prompt was never asked:\n%q", shown)
	}
	if strings.Contains(shown[at+len(prompt):], " Writing ") {
		t.Errorf("a frame was drawn over the prompt the person is answering:\n%q", shown)
	}
	// And it was taken down for the prompt rather than left behind: the frame
	// of the step in flight, then the clear, then the prompt.
	if before := shown[:at]; !strings.Contains(before, " Writing ") || !strings.HasSuffix(before, "\r\x1b[K") {
		t.Errorf("the line in flight was not taken down before the prompt:\n%q", before)
	}
	if _, err := tty.master.WriteString(credsValue(t, "typed") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("creds set: %v\nterminal:\n%s", err, tty.displayed())
	}
	if got := f.value(t); !strings.Contains(got, credsValue(t, "typed")) {
		t.Errorf("the bundle holds %q, want the value typed at the prompt", got)
	}
}
