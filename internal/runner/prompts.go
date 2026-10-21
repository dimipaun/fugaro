package runner

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/verify"
)

// PromptData fills the run contract in the system prompt.
type PromptData struct {
	Branch   string
	Base     string
	StateDir string
	// FollowUp, for a follow-up run, replaces the pr.md line: the pull
	// request exists, and the agent writes its account elsewhere
	// (followup.SystemPromptLines).
	FollowUp []string
	// NoWorkflows adds the rule that the run can't change GitHub workflow
	// files (provider github): Fugaro's push of them is always refused.
	NoWorkflows bool
	// Checkpoints adds the rule to commit early: the runner pushes new
	// commits while the stage runs (first runs with git.pr.checkpoints on).
	Checkpoints bool
	// LintConfigured adds `fugaro verify lint` to the build/test rule:
	// commands.lint, unlike build and test, is optional, and verify.Run
	// refuses outright when it isn't set, so the rule only names the
	// command when the run's own fugaro.yaml configures one.
	LintConfigured bool
}

// SystemPrompt is appended to Claude Code's system prompt for implement and fix stages.
func SystemPrompt(d PromptData, instructions string) string {
	explain := "the pull request description"
	if d.FollowUp != nil {
		// A follow-up leaves the title and description as they are.
		explain = d.StateDir + "/followup.md"
	}
	buildRule := "- Build and test only through `fugaro verify build` and `fugaro verify test`. They run this repository's configured commands and record the results. If a test failure looks flaky, run `fugaro verify test --rerun-failed`: tests that pass on the rerun are recorded as flaky."
	if d.LintConfigured {
		buildRule = "- Build, test and lint only through `fugaro verify build`, `fugaro verify test` and `fugaro verify lint`. They run this repository's configured commands and record the results. If a test failure looks flaky, run `fugaro verify test --rerun-failed`: tests that pass on the rerun are recorded as flaky."
	}
	lines := []string{
		"You are running unattended inside Fugaro, an ephemeral cloud worker. Nobody will answer questions: make reasonable decisions and explain them in " + explain + ".",
		"",
		"Rules for this run:",
		fmt.Sprintf("- You are on branch %s; the pull request will target %s. Commit your work to this branch with clear messages and do not switch branches. You do not need to push or open the pull request: Fugaro does both. Never create, edit, convert or comment on pull requests (with `gh`, an API or any other tool) and never request reviewers: Fugaro owns the pull request, and a pull request you open can notify people before the work is verified.", d.Branch, d.Base),
		buildRule,
		"- The pull request is marked ready for review only if your final commit has a passing `fugaro verify test` run with a clean working tree. Commit first, then verify.",
		"- Never end your turn to wait for anything: this run is headless and ends the moment you stop, so pending background commands, notifications and test runs are lost and nothing wakes you later. Run builds and tests in the foreground (`fugaro verify test` blocks until done) and keep working until the result is in before you finish.",
	}
	if d.NoWorkflows {
		lines = append(lines, "- Never create, edit or delete files under .github/workflows/: GitHub refuses Fugaro's push of workflow changes, so the whole run could not be delivered. If the task needs one, describe the change in "+explain+" instead.")
	}
	if d.Checkpoints {
		lines = append(lines, fmt.Sprintf("- Commit early and often, after every step that works: Fugaro pushes each new commit to %s within about a minute, and if this container dies only pushed commits survive; uncommitted changes are lost. Add new commits rather than amending or rebasing commits you already made: rewritten commits are not pushed until the run ends.", d.Branch))
	}
	if d.FollowUp != nil {
		lines = append(lines, d.FollowUp...)
	} else {
		lines = append(lines, fmt.Sprintf("- Write the pull request title on the first line of %s/pr.md and the description below it.", d.StateDir))
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

// PickPrompt is the review stage prompt for a pick run (design generic-tool
// §3.6, G12): nums[i]'s checkout is dirs[i], one directory per attempt, so
// the reviewer reads every one and names which to keep; see ReviewPrompt
// for review, promptFile and the scope line review-only's single-PR case
// uses instead of this.
func PickPrompt(review, promptFile string, nums []int, dirs []string) string {
	var b strings.Builder
	switch {
	case strings.HasPrefix(review, "/"):
		b.WriteString(review)
	case promptFile != "":
		b.WriteString(promptFile)
	default:
		b.WriteString(defaultReview)
	}
	b.WriteString("\n\nSeveral attempts at the same task are checked out, each in its own directory, as separate pull requests. Do not modify any files. " + binaryRule + "\n")
	for i, n := range nums {
		fmt.Fprintf(&b, "- PR #%d: %s\n", n, dirs[i])
	}
	b.WriteString("\nCompare them and decide which to keep.\n\nWhen you are done, give your verdict: \"ship\" if the one you keep needs no more changes, otherwise \"changes\" with one finding per issue it still has; either way, name the pull request you keep by its number.")
	return b.String()
}

// GatePrompt is the implement prompt with the check gate's failures (gate.go)
// added as untrusted output: one block per failure, each wrapped against
// prompt injection (gateBlock). task is the prompt the gate ran ahead of
// (r.spec.Task for a first run, the follow-up's implement prompt otherwise).
func GatePrompt(task string, g gateResult) string {
	var b strings.Builder
	b.WriteString(task)
	if g.FixProblem != "" {
		fmt.Fprintf(&b, "\n\nThe check gate's autofix did not finish: %s. Fix it yourself, then commit.", g.FixProblem)
	}
	for i, f := range g.Failures {
		var tail string
		if i < len(g.Tails) {
			tail = g.Tails[i]
		}
		fmt.Fprintf(&b, "\n\nThe check gate ran before you started and found a failure: %s\n\nuntrusted output of commands.%s:\n\n%s", f.Summary(), f.Kind, gateBlock(tail))
		if f.Kind == verify.KindLint {
			b.WriteString("\nOnce you've fixed it, you can check with `fugaro verify lint` before committing.")
		}
	}
	return b.String()
}

// gateDelimiterRemoved replaces anything in an untrusted check command's
// output that looks like gateBlock's own delimiters, the same hygiene
// internal/followup.Block uses for a pull request's comments: a nonce picked
// fresh for each block, so the output cannot forge the block's end and
// smuggle instructions of its own into the prompt. The delimiter is
// hygiene, not a security boundary: what runs is still only the recipe's
// own check steps.
const gateDelimiterRemoved = "[fugaro-delimiter removed]"

// gateBlock wraps text (an untrusted command's log tail) in a nonce-delimited
// block.
func gateBlock(text string) string {
	nonce := followup.NewNonce()
	neutralize := regexp.MustCompile(`(?i)(end-)?fugaro-gate-|` + nonce)
	clean := neutralize.ReplaceAllString(text, gateDelimiterRemoved)
	return "<<<fugaro-gate-" + nonce + ">>>\n" + clean + "\n<<<end-fugaro-gate-" + nonce + ">>>\n"
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
