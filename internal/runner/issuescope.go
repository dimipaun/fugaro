package runner

import (
	"fmt"
	"slices"
	"strings"
)

// An issue's scope says whether it is about the task the pull request was
// written for, which is a different axis from its class and its score (D2930).
// The three words are:
//
//   - unmet — the task asks for something the diff does not do;
//   - beyond — the diff does something the task did not ask for;
//   - differs — the task asked for Y and the diff does X instead.
//
// It is NOT the review's own scope: ReviewScope is the range of files a review
// was handed (runstore), a budget's Scope is what the budget bounds, and a
// halt's review_scope is that same range in result.json. This one is per issue
// and never meets any of them.
//
// The field is additive and absent on an issue that is about the code alone,
// which is every issue written before it existed: ReviewArtifactVersion and
// the verdict's own shape do not move, and a reader that does not know the
// field reads the issue exactly as it did.
const (
	scopeUnmet   = "unmet"
	scopeBeyond  = "beyond"
	scopeDiffers = "differs"
)

// issueScopes is the whole vocabulary, in the order a message names it.
var issueScopes = []string{scopeUnmet, scopeBeyond, scopeDiffers}

// checkIssueScope holds one issue's scope to the rule R6 fixes: the word is
// one of the three, and a requirement the diff does not meet is never a small
// matter -- an unmet or differing issue below major is refused rather than
// scored, because a mode's threshold table could otherwise wave through a
// change that does not do what it was asked to do. A `beyond` issue is scored
// by its own risk and may be minor.
//
// The severity is checked by checkReviewIssue before this runs, so a severity
// that is absent or a fourth word is refused there and never read as minor
// here.
func checkIssueScope(is ReviewIssue) error {
	if is.Scope == "" {
		return nil
	}
	if !slices.Contains(issueScopes, is.Scope) {
		return fmt.Errorf("scope is %q, and the three words are %s", is.Scope, strings.Join(issueScopes, ", "))
	}
	if (is.Scope == scopeUnmet || is.Scope == scopeDiffers) && is.Severity == "minor" {
		return fmt.Errorf("scope is %s and severity is minor, and a change that does not do what its task asks is never below major (R6)", is.Scope)
	}
	return nil
}

// checkVerdictScopes refuses a review that ships a change carrying an unmet or
// differing issue: `ship` is the word a coordinator reads as "nothing must
// change before this merges", and an issue that says the task is not done is
// the one thing that word cannot sit beside (R6). It is checked here and not
// only in the prompt's wording, so the rule holds on the reader's side too.
//
// A `beyond` issue is not refused: the diff did more than it was asked to,
// which is a thing to report and not a reason to withhold a ship.
func checkVerdictScopes(verdict string, issues []ReviewIssue) error {
	if verdict != "ship" {
		return nil
	}
	for i, is := range issues {
		if is.Scope == scopeUnmet || is.Scope == scopeDiffers {
			return fmt.Errorf("issues[%d]: the verdict is ship and the issue's scope is %s, and a review that ships a change whose own task it says is not done is refused",
				i, is.Scope)
		}
	}
	return nil
}
