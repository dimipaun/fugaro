package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/runner"
)

// codeSpanRE finds one `code span`.
var codeSpanRE = regexp.MustCompile("`([^`]+)`")

// commandWordRE is one word of a command path a code span spells.
var commandWordRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// shortFormRE matches the words a text uses for the form a person reads — the
// short ID, the shortest ending two runs are told apart by, the last four or
// more characters of a run ID — and fullFormRE the words for the form a
// command is given. Claims are read by these words rather than by one phrasing,
// so a rule reworded into "give `fugaro logs` the short ID" is read the same
// way the plan's own sentence was.
var (
	shortFormRE = regexp.MustCompile(`(?i)\b(short(?:er|est)?(?: run)? ID|shortest ending|last (?:four|4)(?: or more)? characters)`)
	fullFormRE  = regexp.MustCompile(`(?i)\bfull(?: run)? ID\b`)
	negationRE  = regexp.MustCompile(`(?i)\b(never|not|don't|do not)\b`)
	sentenceRE  = regexp.MustCompile(`[.;]\s+`)
)

// endingHelpRE matches a command's own help telling the reader it takes an
// ending, the last four or more characters of a run ID: the RUN argument SR14
// gives `logs`, `diagnose` and `artifacts` when it wires them to resolveRun.
var endingHelpRE = regexp.MustCompile(`(?i)(characters? of a run ID|short(er)? run ID|an ending)`)

// sentences splits a text into the units a claim is made in: a line of a
// markdown file, or one sentence of it. A claim is read inside one of these, so
// a command is never matched to the form words of the sentence before it.
func sentences(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, sentenceRE.Split(line, -1)...)
	}
	return out
}

// commandIn returns the command a code span names — the words after `fugaro`,
// or the span's own — and "" for a span that names none: a placeholder
// (`<repo-slug>/<run-id>`), a flag (`--run-id`), a record's field (`run`) or
// another document (`working`).
func commandIn(span string) string {
	words := strings.Fields(span)
	if len(words) > 0 && words[0] == "fugaro" {
		words = words[1:]
	}
	var path []string
	for _, w := range words {
		if !commandWordRE.MatchString(w) {
			break
		}
		path = append(path, w)
	}
	return strings.Join(path, " ")
}

// commandsAndLeaves returns c, and every command under it when c is a group: a
// rule naming `artifacts` promises the form for all of what it runs.
func commandsAndLeaves(c *cobra.Command) []*cobra.Command {
	subs := c.Commands()
	if len(subs) == 0 {
		return []*cobra.Command{c}
	}
	var out []*cobra.Command
	for _, sub := range subs {
		out = append(out, commandsAndLeaves(sub)...)
	}
	return out
}

// runArgLeaves returns the commands under c that take a run as their argument:
// `logs RUN`, `artifacts get RUN [NAME]`. A text naming any other command —
// `fugaro run --retry`, `fugaro runs ls`, a record's `run` field — claims
// nothing about the form a run is named in, and running the launcher is no
// test's business.
func runArgLeaves(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, leaf := range commandsAndLeaves(c) {
		if words := strings.Fields(leaf.Use); len(words) > 1 && strings.HasPrefix(words[1], "RUN") {
			out = append(out, leaf)
		}
	}
	return out
}

// shortFormClaims returns the commands of this CLI that a text names as taking
// a short run ID, and the sentences that name one beside the full form in one
// breath, which no scan can read: every code span of a sentence about the short
// form alone that resolves to a command taking a run, unless the sentence
// negates it ("never pass the short ID to `fugaro cancel`" claims the
// opposite).
func shortFormClaims(root *cobra.Command, text string) (claims, mixed []string) {
	seen := map[string]bool{}
	for _, sentence := range sentences(text) {
		short := shortFormRE.FindAllStringIndex(sentence, -1)
		if len(short) == 0 {
			continue
		}
		full := fullFormRE.FindAllStringIndex(sentence, -1)
		for _, span := range codeSpanRE.FindAllStringSubmatchIndex(sentence, -1) {
			name := commandIn(sentence[span[2]:span[3]])
			if name == "" || negationRE.MatchString(sentence[:span[0]]) {
				continue
			}
			cmd, _, err := root.Find(strings.Fields(name))
			if err != nil || cmd == nil || cmd == root || len(runArgLeaves(cmd)) == 0 {
				continue
			}
			if len(full) > 0 {
				if trimmed := strings.TrimSpace(sentence); !seen[trimmed] {
					seen[trimmed] = true
					mixed = append(mixed, trimmed)
				}
				continue
			}
			claims = append(claims, name)
		}
	}
	return claims, mixed
}

// shortIDRuleTexts returns every text that carries the rule for an agent: the
// job prompt, which every implement and fix stage gets, and every plugin skill
// file.
func shortIDRuleTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{
		"the job prompt": runner.SystemPrompt(runner.PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}, ""),
	}
	for path, text := range skillTexts(t) {
		out[path] = text
	}
	return out
}

// endingRefusalText runs one command path with an ending in place of the run
// and returns what the CLI said when it refused the ending as a name it cannot
// resolve, "" when the command took it. A refusal is a lookup's message for a
// ref that is neither <repo-slug>/<run-id> nor a run ID: locateRun's own words
// today, and resolveRun's "no run ending ... pass the full ID" (D1709) once
// SR14 wires the read commands to it.
func endingRefusalText(t *testing.T, path, ending string) string {
	t.Helper()
	out, errOut, err := execute(t, append(strings.Fields(path), ending)...)
	if err == nil {
		return ""
	}
	msg := err.Error() + " " + errOut + " " + out
	for _, refusal := range []string{"is neither", "no run ending"} {
		if strings.Contains(msg, refusal) {
			return strings.TrimSpace(msg)
		}
	}
	return ""
}

// TestShortIDRuleNamesOnlyCommandsThatTakeOne: the rule the job prompt and the
// plugin skills carry tells an agent to pass a run's short ID to a command.
// That is a claim about this CLI, so it is checked here, where the commands
// are: a command the rule names that way must take one, or the agent is sent to
// a command that refuses the ID it was told to pass. No command takes one today
// -- SR3's resolveRun has no caller until SR14 wires `logs`, `diagnose` and
// `artifacts` to it -- so no text claims one, and the rule names no command at
// all; this is the test that fails if the naming comes back before the wiring
// does, whatever words the sentence is written in.
func TestShortIDRuleNamesOnlyCommandsThatTakeOne(t *testing.T) {
	f := newCloudFixture(t)
	id := recentRunID(t, time.Minute, "abcd")
	seedRun(t, f, id, "", "someone@example.com", true)
	root := NewRootCmd()

	// The scan is exercised on the wordings a rule may use, because a guard
	// that knows one phrasing passes whatever the texts say once they change.
	for _, c := range []struct {
		text  string
		short []string
		mixed []string
	}{
		{"Pass the short ID to `logs`, `diagnose` and `artifacts` (the commands that accept it)", []string{"logs", "diagnose", "artifacts"}, nil},
		{"Give `fugaro logs` the short ID", []string{"logs"}, nil},
		{"`diagnose` takes the short ID", []string{"diagnose"}, nil},
		{"`fugaro logs` takes the last four characters of a run ID", []string{"logs"}, nil},
		{"`fugaro logs`, `fugaro diagnose` and `fugaro artifacts` take the full ID", nil, nil},
		{"Never pass the short ID to `fugaro cancel`", nil, nil},
		{"The `run` field of a row is the full ID, and a plan task is its name", nil, nil},
		{"`fugaro logs` takes the short ID and `fugaro cancel` the full ID", nil, []string{"`fugaro logs` takes the short ID and `fugaro cancel` the full ID"}},
	} {
		short, mixed := shortFormClaims(root, c.text)
		if !slices.Equal(short, c.short) || !slices.Equal(mixed, c.mixed) {
			t.Errorf("shortFormClaims(%q) = %v, %v, want %v, %v", c.text, short, mixed, c.short, c.mixed)
		}
	}

	// The probe, exercised on the one command the rule names as taking the full
	// ID: cancel never acts on an ending -- locateRun refuses it today, and
	// SR14 has it list the matches and exit as a user error (D1710) -- so a
	// probe that cannot see this refusal would pass every claim below.
	if refusal := endingRefusalText(t, "cancel", "abcd"); refusal == "" {
		t.Fatalf("fugaro cancel abcd -- an ending -- was not refused: %s is the form cancel acts on, and the probe above cannot be trusted until this one is refused", id)
	}

	for path, text := range shortIDRuleTexts(t) {
		claims, mixed := shortFormClaims(root, text)
		for _, sentence := range mixed {
			t.Errorf("%s names a command beside both the short and the full form in one sentence, which no scan can read: %q -- one form per sentence, or word the claim so this guard can check it", path, sentence)
		}
		for _, name := range claims {
			cmd, _, err := root.Find(strings.Fields(name))
			if err != nil || cmd == nil {
				continue
			}
			for _, leaf := range runArgLeaves(cmd) {
				help := leaf.Long + " " + leaf.Short + " " + leaf.Use
				if !endingHelpRE.MatchString(help) {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s's own help offers none: wire the command to resolveRun (SR14) in the same change that names it here, or name the form the help asks for", path, name, leaf.CommandPath())
				}
				if refusal := endingRefusalText(t, strings.TrimPrefix(leaf.CommandPath(), "fugaro "), "abcd"); refusal != "" {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s refused one: %s", path, name, leaf.CommandPath(), refusal)
				}
			}
		}
	}
}
