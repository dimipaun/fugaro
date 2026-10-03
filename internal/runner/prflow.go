package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The early draft pull request (design §4.2a). The draft opens at the first
// verified push, its status section follows the run, and finalize settles
// it by the number saved in the run record. Reviewers and labels are
// applied only after the flip to ready (ApplyReady). Nothing here may fail
// the run: a status update that fails warns, and finalize still guarantees
// the pull request.

const (
	// statusMinInterval is the least time between two status updates:
	// boundaries closer together coalesce. Finalize always writes.
	statusMinInterval = 20 * time.Second
	// statusMaxFailures is how many failed updates in a row stop the
	// updates until finalize.
	statusMaxFailures = 3
	// statusCallTimeout bounds one status update (its read and its write),
	// so a slow provider cannot hold the agent loop.
	statusCallTimeout = 20 * time.Second
	// pushTimeout bounds a mid-run push.
	pushTimeout = 2 * time.Minute
	// statusReserve is the room kept in a description for the status
	// section when the description itself is clipped.
	statusReserve = 6 * 1024
	// maxStatusText bounds the free text (a reason) placed in the section.
	maxStatusText = 200
)

// earlyOpenTimeout bounds the whole early open (its retries included), so a
// provider that stops answering cannot hold the agent loop: finalize opens
// the PR if this does not. A variable so a test can shorten it.
var earlyOpenTimeout = time.Minute

// prFlow is the run's early-PR state. Only the run goroutine touches it.
type prFlow struct {
	lastStatus time.Time // when the last status update was attempted
	fails      int       // consecutive failed updates
	stopped    bool      // too many failures: no more updates until finalize
	gone       bool      // the PR is no longer open (a person closed or merged it)
	noSection  bool      // a follow-up's PR has no status section: leave it be
	truncWarn  bool
	desc       string // the digest notePR records with a new PR (see descDigest)
}

// notePR records a first run's pull request, and saves the record at once.
// It does nothing for a follow-up (whose PR is recorded at bootstrap) or
// for a PR already recorded.
func (r *run) notePR(ctx context.Context, pr gitprov.PR) {
	if r.follow != nil || pr.Number == 0 || (r.rec.PR != nil && r.rec.PR.Number == pr.Number) {
		return
	}
	r.rec.PR = &runstore.PRRef{Number: pr.Number, URL: pr.URL, Desc: r.pr.desc}
	r.save(ctx)
}

// noteStatusWritten records when the status section was last written, in
// the run record, so a stale draft can be told from a live one.
func (r *run) noteStatusWritten(ctx context.Context, at time.Time) {
	if r.rec.PR == nil || at.IsZero() {
		return
	}
	at = at.UTC()
	r.rec.PR.StatusAt = &at
	r.save(ctx)
}

// prNumber is the number of the run's PR, 0 when none is known.
func (r *run) prNumber() int {
	if r.rec.PR == nil {
		return 0
	}
	return r.rec.PR.Number
}

func (r *run) statusOptions() gitprov.StatusOptions {
	limit := gitprov.GitHubBodyLimit
	if r.providerKind == gitprov.KindBitbucket {
		limit = gitprov.BitbucketBodyLimit
	}
	return gitprov.StatusOptions{Form: gitprov.StatusLinkRef, Limit: limit}
}

// verifiedHead reports whether the verify records hold a passing test on a
// clean tree at HEAD (the first condition of the outcome rule), and HEAD.
func (r *run) verifiedHead(ctx context.Context) (string, bool) {
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		r.d.Log.Warn("reading HEAD for the early pull request failed", "err", r.redact(err.Error()))
		return "", false
	}
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		r.d.Log.Warn("reading verify records for the early pull request failed", "err", r.redact(err.Error()))
		return "", false
	}
	if t := latestVerifiedTest(records, sha); t != nil && t.Passed {
		return sha, true
	}
	return sha, false
}

// afterStage runs at a stage boundary of the agent loop, after stage (which
// succeeded) and before the next one. A first run opens its draft here, at
// the end of the first implement or fix stage whose HEAD has a passing test
// on a clean tree, pushes later verified tips, and keeps the status section
// current. Nothing here fails the run.
func (r *run) afterStage(ctx context.Context, stage string) {
	defer func() {
		// Whatever goes wrong here must not fail the run: finalize still
		// guarantees the pull request.
		if p := recover(); p != nil {
			r.d.Log.Error("the pull request flow panicked at a stage boundary; carrying on", "stage", stage, "panic", fmt.Sprint(p))
		}
	}()
	if ctx.Err() != nil || r.pr.gone {
		return
	}
	if r.follow == nil {
		if !r.cfg.Git.PR.EarlyDraftOn() {
			return
		}
		if stage == "implement" || stage == "fix" {
			if sha, ok := r.verifiedHead(ctx); ok {
				if r.rec.PR == nil {
					r.openDraft(ctx, sha, stage)
					return
				}
				r.pushVerified(ctx, sha)
			}
		}
	}
	r.statusUpdate(ctx, stage)
}

// pushBranch pushes HEAD (sha) with the run's current credentials, on a
// bound of its own. A failure is the caller's to warn about; finalize
// pushes again.
func (r *run) pushBranch(ctx context.Context, sha string) error {
	actx, cancelAuth := context.WithTimeout(ctx, authRefreshTimeout)
	err := r.refreshGitAuth(actx, authValidity(max(r.wf.Timeouts.FinalizeReserve.Duration, bootstrapAuthMinValid)))
	cancelAuth()
	if err != nil {
		r.warnAuthRefresh(err)
	}
	pctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	if err := r.repo.Push(pctx, r.rec.Branch); err != nil {
		return err
	}
	// Saved at once, as finalize does: a run killed later still says it pushed.
	r.rec.PushedHead = sha
	r.save(ctx)
	return nil
}

// pushVerified pushes a later verified tip, so the remote branch is always
// a verified state. A failure only warns.
func (r *run) pushVerified(ctx context.Context, sha string) {
	if r.rec.PushedHead == sha {
		return
	}
	if err := r.pushBranch(ctx, sha); err != nil {
		r.d.Log.Warn("pushing the verified tip failed; finalize pushes again", "err", r.redact(err.Error()))
	}
}

// openDraft pushes the verified HEAD and opens the draft pull request, then
// records its number before anything else. A failure only warns: finalize
// opens the PR if none was recorded.
func (r *run) openDraft(ctx context.Context, sha, stage string) {
	if err := r.pushBranch(ctx, sha); err != nil {
		r.d.Log.Warn("pushing for the early pull request failed; finalize opens it", "err", r.redact(err.Error()))
		return
	}
	title, desc := r.earlyPRText()
	r.pr.lastStatus = r.d.Now()
	section := r.redact(r.runningSection(stage))
	body, trunc := gitprov.ReplaceStatusWith(desc, section, r.statusOptions())
	r.warnTruncated(trunc)
	// A draft carries no reviewers and no labels: they come at ready.
	r.pr.desc = descDigest(title, desc)
	// ensurePR records the number (notePR) the moment the PR exists, before
	// anything else, so a crash from here on finds it by number and one
	// before it finds it by branch.
	octx, cancelOpen := context.WithTimeout(ctx, earlyOpenTimeout)
	pr, err := r.ensurePR(octx, gitprov.PRSpec{Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, Title: title, Body: body, Draft: true})
	cancelOpen()
	if err != nil {
		r.d.Log.Warn("opening the early draft pull request failed; finalize opens it", "err", r.redact(err.Error()))
		return
	}
	if !pr.Draft {
		// Not expected: EnsurePR errs only toward more draft. Never leave
		// an early PR looking ready.
		r.d.Log.Error("the early pull request is not a draft; making it one", "pr", pr.Number)
		d := true
		uctx, cancel := context.WithTimeout(ctx, statusCallTimeout)
		_, uerr := r.provider.UpdatePR(uctx, pr.Number, gitprov.PRUpdate{Draft: &d})
		cancel()
		if uerr != nil {
			r.d.Log.Warn("making the early pull request a draft failed", "pr", pr.Number, "err", r.redact(uerr.Error()))
		}
	}
	r.noteStatusWritten(ctx, r.pr.lastStatus)
	if pr.DraftFallback {
		r.rec.DraftFallback = true
		r.pr.lastStatus = time.Time{} // the next boundary says so in the section
		r.save(ctx)
		r.d.Log.Warn("the host has no draft pull requests: the early PR is a normal one marked [DRAFT]", "pr", pr.Number)
	}
	r.d.Log.Info("early draft pull request opened", "pr", pr.Number, "url", pr.URL)
}

func (r *run) warnTruncated(trunc bool) {
	if trunc && !r.pr.truncWarn {
		r.pr.truncWarn = true
		r.d.Log.Warn("the pull request description is too long for a full status section; it was cut")
	}
}

// statusUpdate rewrites the status section at a stage boundary, at most
// once per statusMinInterval; after statusMaxFailures failures in a row it
// stops until finalize. A failure is a warning, never a failed run.
func (r *run) statusUpdate(ctx context.Context, stage string) {
	n := r.prNumber()
	if n == 0 || r.pr.stopped || r.pr.gone || ctx.Err() != nil {
		return
	}
	if r.follow != nil && r.pr.noSection {
		return
	}
	now := r.d.Now()
	if !r.pr.lastStatus.IsZero() && now.Sub(r.pr.lastStatus) < statusMinInterval {
		return
	}
	r.pr.lastStatus = now
	err := r.writeStatus(ctx, n, r.redact(r.runningSection(stage)), statusCallTimeout)
	switch {
	case err == nil:
		r.pr.fails = 0
		if !(r.follow != nil && r.pr.noSection) {
			r.noteStatusWritten(ctx, now)
		}
	case errors.Is(err, gitprov.ErrPRNotOpen):
		r.pr.gone = true
		r.d.Log.Warn("the pull request is no longer open; finalize will not recreate it", "pr", n)
	default:
		r.pr.fails++
		r.d.Log.Warn("updating the pull request status failed", "pr", n, "failures", r.pr.fails, "err", r.redact(err.Error()))
		if r.pr.fails >= statusMaxFailures {
			r.pr.stopped = true
			r.d.Log.Warn("too many failed status updates; no more until finalize", "pr", n)
		}
	}
}

// writeStatus replaces only the status section of pull request n's
// description with section, keeping everything outside the markers. A
// follow-up's PR with no section is left alone (noSection). A PR that is
// not open gives ErrPRNotOpen and nothing is written.
func (r *run) writeStatus(ctx context.Context, n int, section string, timeout time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	info, err := r.provider.PullRequest(cctx, n)
	if err != nil {
		return err
	}
	if info.State != gitprov.PROpen {
		return fmt.Errorf("PR #%d is %s: %w", n, info.State, gitprov.ErrPRNotOpen)
	}
	if r.follow != nil && gitprov.StripStatus(info.Body) == info.Body {
		r.pr.noSection = true // a PR from before M9e, or one whose section a person removed
		return nil
	}
	body, trunc := gitprov.ReplaceStatusWith(info.Body, section, r.statusOptions())
	r.warnTruncated(trunc)
	if body == info.Body {
		return nil
	}
	_, err = r.provider.UpdatePR(cctx, n, gitprov.PRUpdate{Body: &body})
	return err
}

// descDigest identifies the title and description (outside the status
// section) the runner wrote, whitespace and line endings normalised, so a
// host that trims or converts them does not look like a human edit.
func descDigest(title, body string) string {
	norm := func(s string) string {
		return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	}
	sum := sha256.Sum256([]byte(norm(gitprov.DraftTitle(title, false)) + "\x00" + norm(body)))
	return hex.EncodeToString(sum[:16])
}

// scrubDescription removes what the agent's pr.md or the task text could
// use to forge the runner's own text: status sections and any line naming a
// status or report marker.
func scrubDescription(s string) string {
	s = gitprov.StripMarkers(gitprov.StripStatus(s))
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), "fugaro:status") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// earlyPRText is the title and description for a pull request that will
// carry a status section: scrubbed of forged markers and clipped to leave
// the section its room.
func (r *run) earlyPRText() (string, string) {
	title, body := r.rawPRText()
	title = scrubDescription(title)
	if title == "" {
		title = "Fugaro run " + inlineText(r.rec.RunID) // the agent title was only markers
	}
	body = scrubDescription(body)
	if runes := []rune(title); len(runes) > maxTitleRunes {
		title = string(runes[:maxTitleRunes-1]) + "…"
	}
	body = clipBody(body, min(maxBodyBytes, r.statusOptions().Limit-statusReserve))
	return title, body
}

// ---- the section's text ----

// inlineText makes free text safe to put in one line of the section: control
// characters and line breaks become spaces, and the characters Markdown
// reads as structure or HTML are dropped. It is clipped at maxStatusText.
func inlineText(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '�':
		case c == '@':
			b.WriteString("@\u200b") // a mention must not notify anyone
		case c == '`':
			b.WriteRune('\'')
		case c == '*' || c == '_':
			b.WriteRune('\\')
			b.WriteRune(c)
		case strings.ContainsRune("<>[]|\\#!", c):
		case unicode.IsControl(c) || unicode.IsSpace(c):
			b.WriteRune(' ')
		default:
			b.WriteRune(c)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if runes := []rune(out); len(runes) > maxStatusText {
		out = string(runes[:maxStatusText-1]) + "…"
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// verifyPart is the section's account of the latest test record.
func verifyPart(records []verify.Record) string {
	for i := len(records) - 1; i >= 0; i-- {
		v := records[i]
		if v.Kind != verify.KindTest {
			continue
		}
		state := "passed"
		if !v.Passed {
			state = "failed"
		}
		if !v.CleanTree {
			state += " (tree not clean)"
		}
		return fmt.Sprintf("tests %s on `%s`", state, shortSHA(v.HeadSHA))
	}
	return "no test run yet"
}

// reviewsPart is the section's account of the review rounds, or "".
func reviewsPart(rs []runstore.ReviewSummary) string {
	if len(rs) == 0 {
		return ""
	}
	last := rs[len(rs)-1]
	if last.Verdict == "ship" {
		return fmt.Sprintf("review: ship in round %d", last.Round)
	}
	return fmt.Sprintf("review round %d: %s", last.Round, Plural(last.Findings, "finding"))
}

// costPart is the model cost for the section: dollars for api-key and
// Vertex runs (against the run's cap when it has one), notional for oauth.
func (r *run) costPart() string {
	r.updateCost()
	c := r.rec.Cost
	if c == nil {
		return ""
	}
	if c.ModelBasis == runstore.BasisSubscription {
		return fmt.Sprintf("notional $%.2f (not billed)", c.ModelUSD)
	}
	if r.spend.Cap > 0 {
		return fmt.Sprintf("model cost $%.2f of $%s cap", c.ModelUSD, trimUSD(r.spend.Cap.USD()))
	}
	return fmt.Sprintf("model cost $%.2f", c.ModelUSD)
}

func trimUSD(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	return strings.TrimSuffix(strings.TrimSuffix(s, "0"), ".0")
}

// sectionLines joins the section's parts.
func (r *run) sectionLines(head string, parts ...string) string {
	kept := []string{head}
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	line := strings.Join(kept, " · ")
	id := inlineText(r.rec.RunID)
	out := line + "\n"
	if r.rec.DraftFallback {
		out += "This host has no draft pull requests: this is a normal pull request marked [DRAFT], so reviews may already have been requested.\n"
	}
	return out + fmt.Sprintf("Run `%s` (a stale time here means the run died: `fugaro diagnose %s`)", id, id)
}

func (r *run) updated(label string) string {
	return label + " " + r.d.Now().UTC().Format("2006-01-02 15:04Z")
}

// runningSection is the section while the run is in progress; stage is the
// stage that just finished.
func (r *run) runningSection(stage string) string {
	records, _ := verify.Records(r.d.StateDir)
	head := "**Running**"
	if r.follow != nil {
		head = "**Follow-up running**"
	}
	what := fmt.Sprintf("after stage `%s`", inlineText(stage))
	if stage == "review" {
		if p := reviewsPart(r.rec.Reviews); p != "" {
			what = p
		}
	}
	return r.sectionLines(head, what, verifyPart(records), r.costPart(), r.updated("updated"))
}

// finalSection is the section when the run ended: outcome, reason, tests,
// reviews and cost.
func (r *run) finalSection(ready bool, reason string, records []verify.Record) string {
	head, why := "**Draft**", inlineText(reason)
	switch {
	case ready:
		head, why = "**Ready for review**", ""
	case r.rec.Status == runstore.StatusHalted:
		head = "**Halted**"
	case r.rec.Status == runstore.StatusCancelled:
		head = "**Cancelled**"
	}
	return r.sectionLines(head, why, verifyPart(records), reviewsPart(r.rec.Reviews), r.costPart(), r.updated("finished"))
}

// ---- finalize ----

// settleText writes the run's final status section to pull request n and,
// while nobody has edited them, replaces the title and description from
// pr.md. A follow-up only touches an existing section and never rewrites a
// description. Best effort: a failure warns and the run goes on. A PR that
// is no longer open is reported by the error.
func (r *run) settleText(ctx context.Context, n int, section string) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
	defer cancel()
	info, err := r.provider.PullRequest(cctx, n)
	if err != nil {
		return err
	}
	if info.State != gitprov.PROpen {
		return fmt.Errorf("PR #%d is %s: %w", n, info.State, gitprov.ErrPRNotOpen)
	}
	section = r.redact(section)
	body := info.Body
	var u gitprov.PRUpdate
	if r.follow != nil {
		if gitprov.StripStatus(body) == body {
			return nil // no section: a follow-up changes nothing but the draft state
		}
	} else if ref := r.rec.PR; ref != nil && ref.Desc != "" && ref.Desc == descDigest(info.Title, gitprov.StripStatus(body)) {
		// Still what the runner wrote at the start: the agent's final
		// pr.md replaces the placeholder.
		title, desc := r.earlyPRText()
		body = desc
		if gitprov.DraftTitle(info.Title, false) != title {
			u.Title = &title
		}
	}
	newBody, trunc := gitprov.ReplaceStatusWith(body, section, r.statusOptions())
	r.warnTruncated(trunc)
	if newBody != info.Body {
		u.Body = &newBody
	}
	if u.Title == nil && u.Body == nil {
		return nil
	}
	_, err = r.provider.UpdatePR(cctx, n, u)
	return err
}

// applyReady requests the configured reviewers and labels on the PR the
// run has just made ready. It returns a note for the report when that
// failed: the PR stays ready, as the outcome is the run's verification and
// not the notification.
func (r *run) applyReady(ctx context.Context, n int) string {
	reviewers, labels := r.cfg.Git.PR.Reviewers, r.cfg.Git.PR.Labels
	if len(reviewers) == 0 && len(labels) == 0 {
		return ""
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
	defer cancel()
	err := r.provider.ApplyReady(cctx, n, reviewers, labels)
	if err == nil {
		return ""
	}
	r.d.Log.Warn("requesting reviewers and labels failed; the PR stays ready", "pr", n, "err", r.redact(err.Error()))
	return "reviewers and labels could not be applied (" + inlineText(r.redact(err.Error())) + "); the pull request is ready, so request reviewers by hand"
}
