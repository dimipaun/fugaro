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

// shortIDClaimRE finds the sentence that hands a command a short run ID: the
// names it hands one to follow the phrase.
var shortIDClaimRE = regexp.MustCompile(`Pass the short ID to ([^.;(]*)`)

// codeSpanRE finds one `code span`.
var codeSpanRE = regexp.MustCompile("`([^`]+)`")

// endingHelpRE matches a command's own help telling the reader it takes an
// ending, the last four or more characters of a run ID: the RUN argument SR14
// gives `logs`, `diagnose` and `artifacts` when it wires them to resolveRun.
var endingHelpRE = regexp.MustCompile(`(?i)(characters? of a run ID|short(er)? run ID|an ending)`)

// shortIDClaimNames returns the commands a text tells an agent to pass a short
// run ID to. A text that makes no such claim names none.
func shortIDClaimNames(text string) []string {
	m := shortIDClaimRE.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var out []string
	for _, span := range codeSpanRE.FindAllStringSubmatch(m[1], -1) {
		words := strings.Fields(span[1])
		if len(words) > 0 && words[0] == "fugaro" {
			words = words[1:]
		}
		if len(words) > 0 {
			out = append(out, strings.Join(words, " "))
		}
	}
	return out
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

// endingRefusalText runs one command path with an ending in place of the run and
// returns what the CLI said when it refused the ending as a name it cannot
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
// `artifacts` to it -- so the rule names none and sends the agent to each
// command's own help instead, which is what this test holds it to.
func TestShortIDRuleNamesOnlyCommandsThatTakeOne(t *testing.T) {
	f := newCloudFixture(t)
	id := recentRunID(t, time.Minute, "abcd")
	seedRun(t, f, id, "", "someone@example.com", true)

	// A text that makes no claim is nothing to check, so the extractor is
	// exercised on one that does: a guard that found no names to check would
	// pass whatever the texts say.
	claim := "Pass the short ID to `logs`, `diagnose` and `artifacts` (the commands that accept it)"
	if got, want := shortIDClaimNames(claim), []string{"logs", "diagnose", "artifacts"}; !slices.Equal(got, want) {
		t.Fatalf("shortIDClaimNames(%q) = %v, want %v", claim, got, want)
	}

	// The probe, exercised on the one command the rule names as taking the full
	// ID: cancel never acts on an ending -- locateRun refuses it today, and
	// SR14 has it list the matches and exit as a user error (D1710) -- so a
	// probe that cannot see this refusal would pass every claim below.
	if refusal := endingRefusalText(t, "cancel", "abcd"); refusal == "" {
		t.Fatalf("fugaro cancel abcd -- an ending -- was not refused: %s is the form cancel acts on, and the probe above cannot be trusted until this one is refused", id)
	}

	root := NewRootCmd()
	for path, text := range shortIDRuleTexts(t) {
		for _, name := range shortIDClaimNames(text) {
			cmd, _, err := root.Find(strings.Fields(name))
			if err != nil || cmd == nil || cmd == root {
				t.Errorf("%s tells an agent to pass a short ID to `%s`, which is no command of this CLI", path, name)
				continue
			}
			for _, leaf := range commandsAndLeaves(cmd) {
				help := leaf.Long + " " + leaf.Short + " " + leaf.Use
				if !endingHelpRE.MatchString(help) {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s's own help offers none: wire the command to resolveRun (SR14) in the same change that names it here, or word the rule as the form the help asks for", path, name, leaf.CommandPath())
				}
				if refusal := endingRefusalText(t, strings.TrimPrefix(leaf.CommandPath(), "fugaro "), "abcd"); refusal != "" {
					t.Errorf("%s tells an agent to pass a short ID to `%s`, and %s refused one: %s", path, name, leaf.CommandPath(), refusal)
				}
			}
		}
	}
}
