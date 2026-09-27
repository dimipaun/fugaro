package runner

import (
	"fmt"
	"strings"
)

// PromptData fills the run contract in the system prompt.
type PromptData struct {
	Branch   string
	Base     string
	StateDir string
}

// SystemPrompt is appended to Claude Code's system prompt for implement and fix stages.
func SystemPrompt(d PromptData, instructions string) string {
	lines := []string{
		"You are running unattended inside Fugaro, an ephemeral cloud worker. Nobody will answer questions: make reasonable decisions and explain them in the pull request description.",
		"",
		"Rules for this run:",
		fmt.Sprintf("- You are on branch %s; the pull request will target %s. Commit your work to this branch with clear messages and do not switch branches. You do not need to push or open the pull request: Fugaro does both when you finish.", d.Branch, d.Base),
		"- Build and test only through `fugaro verify build` and `fugaro verify test`. They run this repository's configured commands and record the results. If a test failure looks flaky, run `fugaro verify test --rerun-failed`: tests that pass on the rerun are recorded as flaky.",
		"- The pull request is marked ready for review only if your final commit has a passing `fugaro verify test` run with a clean working tree. Commit first, then verify.",
		fmt.Sprintf("- Write the pull request title on the first line of %s/pr.md and the description below it.", d.StateDir),
	}
	s := strings.Join(lines, "\n") + "\n"
	if strings.TrimSpace(instructions) != "" {
		s += "\nRepository instructions:\n\n" + instructions
	}
	return s
}

const defaultReview = "You are reviewing a pull request written by another engineer. Look for correctness bugs, missing or weak tests, security problems, and deviations from this repository's conventions (see CLAUDE.md if present). Ignore pure style preferences."

// ReviewPrompt builds the review stage prompt. review is the configured
// agent.review: a skill (starting with "/") or empty; promptFile is the
// content of a configured review prompt file.
func ReviewPrompt(review, promptFile, base string) string {
	scope := fmt.Sprintf("Review the changes on this branch against origin/%s (see `git diff origin/%s...HEAD`). Do not modify any files.", base, base)
	tail := "\n\nWhen you are done, give your verdict: \"ship\" if nothing must change before this is merged, otherwise \"changes\" with one finding per issue that must be fixed."
	switch {
	case strings.HasPrefix(review, "/"):
		return review + " " + scope + tail
	case promptFile != "":
		return promptFile + "\n\n" + scope + tail
	default:
		return defaultReview + "\n\n" + scope + tail
	}
}

// FixPrompt asks the implementing session to address review findings.
func FixPrompt(v Verdict) string {
	var b strings.Builder
	b.WriteString("A code review of your changes asked for these fixes:\n\n")
	for _, f := range v.Findings {
		b.WriteString("- ")
		if f.Severity != "" {
			fmt.Fprintf(&b, "[%s] ", f.Severity)
		}
		if f.File != "" {
			fmt.Fprintf(&b, "%s: ", f.File)
		}
		b.WriteString(f.Summary)
		b.WriteString("\n")
	}
	b.WriteString("\nAddress each one, commit, and run `fugaro verify test` again.")
	return b.String()
}
