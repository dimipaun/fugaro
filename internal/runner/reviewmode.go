package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// reviewHead is one pull request review mode resolved at bootstrap
// (checkReviewPRs): its number and the commit it will check out. The fork
// and open checks gitprov.PRHead also carries have already passed by the
// time a reviewHead exists.
type reviewHead struct {
	Number int
	SHA    string
}

// checkReviewPRs resolves and validates every pull request r.spec.ReviewPRs
// names (design generic-tool §3.6, G12): FetchPRHead, then its open state
// and, for a fork, r.cfg.Review.AllowForks (read from the base branch, like
// followup.trusted: a fork PR cannot opt itself in). Any failure ends
// bootstrap with the reason, before anything is checked out; the runner
// never silently skips a pull request the task named.
func (r *run) checkReviewPRs(ctx context.Context) error {
	heads := make([]reviewHead, 0, len(r.spec.ReviewPRs))
	for _, n := range r.spec.ReviewPRs {
		h, err := r.provider.FetchPRHead(ctx, n)
		if err != nil {
			return fmt.Errorf("reading PR #%d: %w", n, err)
		}
		if !h.Open {
			return fmt.Errorf("PR #%d is not open", n)
		}
		if h.Fork && !r.cfg.Review.AllowForks {
			return fmt.Errorf("PR #%d's head is in a fork; set review.allow_forks: true in fugaro.yaml on %s to review it", n, r.spec.Ref)
		}
		if strings.TrimSpace(h.SHA) == "" {
			return fmt.Errorf("PR #%d has no head commit", n)
		}
		heads = append(heads, reviewHead{Number: n, SHA: h.SHA})
	}
	r.reviewHeads = heads
	return nil
}

// reviewDir is where a pick run checks out pull request n (design
// generic-tool §3.6, G12): a sibling of the main checkout (r.d.WorkDir),
// which pick leaves exactly as the image or clone found it, so none of the
// attempts alias it.
func (r *run) reviewDir(n int) string {
	return filepath.Join(filepath.Dir(r.d.WorkDir), "review", strconv.Itoa(n))
}

// checkoutReviewHeads fetches and checks out every resolved head: directly
// into r.d.WorkDir, detached, for a single pull request (review-only); into
// its own worktree under reviewDir, for several (pick).
func (r *run) checkoutReviewHeads(ctx context.Context) error {
	if len(r.reviewHeads) == 1 {
		return r.repo.CheckoutDetached(ctx, r.reviewHeads[0].SHA)
	}
	for _, h := range r.reviewHeads {
		dir := r.reviewDir(h.Number)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if err := r.repo.Worktree(ctx, h.SHA, dir); err != nil {
			return err
		}
	}
	return nil
}

// reviewModePrompt is the review stage's prompt: ReviewPrompt for a single
// pull request (r.d.WorkDir is checked out to it), PickPrompt listing every
// directory for several.
func (r *run) reviewModePrompt() string {
	if len(r.reviewHeads) == 1 {
		return ReviewPrompt(r.cfg.Agent.Review, r.reviewFile, r.cfg.Git.BaseBranch)
	}
	nums := make([]int, len(r.reviewHeads))
	dirs := make([]string, len(r.reviewHeads))
	for i, h := range r.reviewHeads {
		nums[i], dirs[i] = h.Number, r.reviewDir(h.Number)
	}
	return PickPrompt(r.cfg.Agent.Review, r.reviewFile, nums, dirs)
}

// runReviewMode runs reviewMode, recovering a panic so the run always ends
// with a record, the same guarantee runAgentLoop gives the ordinary loop.
func (r *run) runReviewMode(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			r.failReviewInfra(ctx, fmt.Errorf("stage %s panicked: %v", r.rec.Stage, p))
		}
	}()
	r.reviewMode(ctx)
}

// reviewMode runs review mode's one stage (design generic-tool §3.6, G12):
// check out every resolved head, run the senior review with no git
// credentials of the agent's own (agentenv: the agent env Set/authVars
// block in bootstrap, and refreshGitAuth, both skip the agent's env for
// r.review), post one comment per pull request, and end the run reviewed.
// It never pushes and never opens or edits a pull request; finalize does
// nothing more (r.skipPR).
func (r *run) reviewMode(ctx context.Context) {
	r.rec.Stage = "review"
	r.save(ctx)
	if err := r.checkoutReviewHeads(ctx); err != nil {
		if errors.Is(context.Cause(ctx), ErrCancelled) && r.markCancelled() {
			r.endReviewCancelled(ctx, "cancelled checking out the pull request")
			return
		}
		r.failReviewInfra(ctx, err)
		return
	}
	req := agent.Request{Prompt: r.reviewModePrompt(), SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}
	res, ok, _ := r.stage(ctx, "review", req, stageOpts{})
	r.countTokens(res)
	if !ok {
		// The same priority gate.go's own error branch follows: a halt
		// decides the run whatever else is true, before cancel is even
		// asked about.
		if h := r.haltValue(); h != nil {
			r.endReviewHalted(ctx, h)
			return
		}
		if r.isCancelled() {
			r.endReviewCancelled(ctx, "cancelled during review")
			return
		}
		r.endReviewFailed(ctx)
		return
	}
	v := ParseVerdict(res)
	if n := len(r.reviewHeads); n > 1 && !r.keepIsOneOfReviewed(v.Keep) {
		// A pick run's verdict names no pull request to keep among the
		// ones actually compared (zero, because the stage's structured
		// output didn't parse at all, or a number the model hallucinated):
		// posting a comment and a result.json that claim a keep would be
		// misleading rather than merely incomplete, so this ends the run
		// failed instead, the same way an unparseable verdict ends any
		// other stage failed.
		r.endReviewInvalidVerdict(ctx, fmt.Sprintf("pick's verdict named keep #%d, which is not one of the %d pull requests compared", v.Keep, n))
		return
	}
	r.postReviewComments(ctx, v)
	r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReviewed
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// keepIsOneOfReviewed reports whether keep names one of r.reviewHeads'
// pull requests: a pick run's verdict must choose among the ones it was
// actually shown, never zero (no choice at all) or a number outside them.
func (r *run) keepIsOneOfReviewed(keep int) bool {
	for _, h := range r.reviewHeads {
		if h.Number == keep {
			return true
		}
	}
	return false
}

// postReviewComments posts the same comment on every reviewed pull
// request: the verdict and findings, redacted, capped at 60 KiB through
// the existing PR-body clipper, and for a pick run (len > 1) which one to
// keep. A posting failure is logged and recorded in the report (never the
// structured Reviewed entry, whose Verdict is set regardless) and never
// retried in a loop: every other pull request still gets its comment.
func (r *run) postReviewComments(ctx context.Context, v Verdict) {
	nums := make([]int, len(r.reviewHeads))
	for i, h := range r.reviewHeads {
		nums[i] = h.Number
	}
	body := agent.Redact(clipBody(ReviewComment(r.rec.Recipe.Name, v, nums), maxBodyBytes), r.secretList())
	reviewed := make([]runstore.ReviewedPR, 0, len(r.reviewHeads))
	for _, h := range r.reviewHeads {
		reviewed = append(reviewed, runstore.ReviewedPR{Number: h.Number, SHA: h.SHA, Verdict: v.Verdict})
		if err := r.provider.Comment(ctx, gitprov.PR{Number: h.Number}, body); err != nil {
			r.d.Log.Warn("posting the review comment failed", "pr", h.Number, "err", r.redact(err.Error()))
			r.commentsFailed = append(r.commentsFailed, h.Number)
		}
	}
	r.rec.Reviewed = reviewed
}

// ReviewComment is review mode's comment body (design generic-tool §3.6,
// G12): the header names the recipe and the verdict, a pick run (len(prs)
// > 1) also says which pull request to keep, the findings follow as a
// list, and the run ID closes it (gitprov.ReportMarker, the same marker
// every other Fugaro comment carries, so a later run can tell this one
// apart).
func ReviewComment(recipeName string, v Verdict, prs []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fugaro review (%s): %s\n", recipeName, v.Verdict)
	if len(prs) > 1 {
		fmt.Fprintf(&b, "\nkeep #%d\n", v.Keep)
		for _, n := range prs {
			if n != v.Keep {
				fmt.Fprintf(&b, "- #%d\n", n)
			}
		}
	}
	if len(v.Findings) > 0 {
		b.WriteString("\nFindings:\n")
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
	}
	b.WriteString("\n")
	return b.String()
}

// reviewReport renders a review-mode run's report.md: what it reviewed and
// each pull request's verdict, never a pull request of its own (there is
// none), unlike Report/FollowUpReport, which every other run's record
// uses.
func reviewReport(rec *runstore.Record, location string, failedComments []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Fugaro run `%s`\n\n", rec.RunID)
	if rec.Status == runstore.StatusHalted && rec.Halt != nil {
		b.WriteString(haltLines(rec))
	}
	if rec.Outcome == runstore.OutcomeReviewed {
		b.WriteString("**Outcome:** reviewed\n\n")
	} else {
		fmt.Fprintf(&b, "**Outcome:** stopped — %s\n\n", rec.Reason)
	}
	if len(rec.Reviewed) > 0 {
		b.WriteString("| Pull request | Verdict |\n|---|---|\n")
		for _, rv := range rec.Reviewed {
			fmt.Fprintf(&b, "| #%d | %s |\n", rv.Number, rv.Verdict)
		}
		b.WriteString("\n")
	}
	for _, n := range failedComments {
		fmt.Fprintf(&b, "**Note:** posting the review comment on PR #%d failed; see the run's log.\n\n", n)
	}
	if len(rec.Stages) > 0 {
		b.WriteString("| Stage | Duration |\n|---|---|\n")
		for _, s := range rec.Stages {
			d := time.Duration(s.DurationS * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(&b, "| %s | %s |\n", s.Name, d)
		}
		b.WriteString("\n")
	}
	if rec.Cost != nil {
		b.WriteString(CostLine(*rec.Cost) + "\n\n")
	} else {
		fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)
	}
	b.WriteString(policyLine(rec.Policy))
	fmt.Fprintf(&b, "Transcripts: `%s` in the runs bucket.\n", location)
	b.WriteString("\n" + gitprov.ReportMarker(rec.RunID) + "\n")
	return b.String()
}

// storeReviewEnded stores review mode's report.md (reviewReport, never
// FollowUpReport): the generic-tool plan's own report, used by every
// terminal state below.
func (r *run) storeReviewEnded(ctx context.Context) {
	r.updateCost()
	report := agent.Redact(reviewReport(r.rec, r.d.Store.Prefix(), r.commentsFailed), r.secretList())
	r.storeReport(ctx, report, nil)
}

// endReviewCancelled ends a review run that was cancelled: status
// cancelled, outcome none, never infra_error. finalize, which still runs
// after, does nothing more (r.skipPR).
func (r *run) endReviewCancelled(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// endReviewHalted ends a review run that a halt (a budget cap, a kill
// switch, the token cap) stopped. finalize, which still runs after, does
// nothing more (r.skipPR).
func (r *run) endReviewHalted(ctx context.Context, h *runstore.Halt) {
	r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, h
	r.rec.Reason = (&HaltError{*h}).Error()
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// failReviewInfra ends a review run whose checkout or own operation failed
// outright (never a check that ran and disagreed: there is no such check
// in review mode). finalize, which still runs after, does nothing more
// (r.skipPR).
func (r *run) failReviewInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("review mode failed", "err", r.rec.Reason)
	r.storeReviewEnded(ctx)
}

// endReviewFailed ends a review run whose stage itself failed (the model
// timed out, crashed, or reported an error): status failed, outcome none —
// there is no draft pull request to fall back to, unlike an ordinary run's
// own stage failure. finalize, which still runs after, does nothing more
// (r.skipPR).
func (r *run) endReviewFailed(ctx context.Context) {
	reason := r.failure()
	if reason == "" {
		reason = "the review stage ended without a verdict"
	}
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeReviewEnded(ctx)
}

// endReviewInvalidVerdict ends a review run whose stage returned a verdict
// that cannot be trusted as is (a pick run's keep, reviewMode): status
// failed, outcome none, the same family endReviewFailed is, since no
// comment is posted for a verdict this is never called on to trust. The
// comment and result.json for a pull request's own entry go only once the
// verdict passes this; a failure here leaves no Reviewed entry and posts
// no comment at all, rather than one claiming a keep that isn't real.
func (r *run) endReviewInvalidVerdict(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeReviewEnded(ctx)
}
