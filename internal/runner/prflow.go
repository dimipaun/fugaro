package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The early draft pull request (design §4.2a). The draft opens at the first
// checkpoint push (not verified until a verify record passes; with
// git.pr.checkpoints off, at the first verified stage end), its status section follows the run, and finalize settles
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
	// maxStatusBinaries is how many of the run's admitted binaries the section
	// names before it counts the rest (D993). The body is a surface a run can
	// grow — a task that adds a hundred assets is one task — and the paths are
	// there to say an unreadable file is in the diff, not to list it: a reader
	// who needs every path reads the diff, and the count says how many there
	// are.
	maxStatusBinaries = 3
)

// earlyOpenTimeout bounds the whole early open (its retries included), so a
// provider that stops answering cannot hold the agent loop: finalize opens
// the PR if this does not. A variable so a test can shorten it.
var earlyOpenTimeout = time.Minute

// prFlow is the run's early-PR state. While a stage runs, its checkpoint poll
// goroutine is the only one that touches it (prflow.go's openDraftPR and
// statusUpdate are reached from the poll, checkpoint.go); between stages, and
// after that poll's stop has returned, the run goroutine's own boundary and
// finalize paths do. A governance halt in the middle of a stage stops the poll
// and waits for it before it touches any of this (governanceHalt, halt.go), so
// the two never write it at once.
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
	r.noteRegistryPRURL(r.rec.PR.URL)
	r.noteChangeSetMember(ctx, pr)
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
// current. A report run pushes nothing here: a fix stage's commit is a
// stage's, and the bytes a report run's pull request carries must be the
// step's own — the digest generator's (digest.go, T3.3), the store's for a
// commit run (reports_commit.go, R9) — so its branch reaches the remote at
// finalize and nowhere else. Nothing here fails the run.
func (r *run) afterStage(ctx context.Context, stage string) {
	defer func() {
		// Whatever goes wrong here must not fail the run: finalize still
		// guarantees the pull request.
		if p := recover(); p != nil {
			r.d.Log.Error("the pull request flow panicked at a stage boundary; carrying on", "stage", stage, "panic", fmt.Sprint(p))
		}
	}()
	r.noteVerify()
	if ctx.Err() != nil || r.pr.gone {
		return
	}
	if r.follow == nil {
		// A report run's only push is finalize's: checkpointsOn() is false
		// for it, so the boundary checkpoint below pushes nothing, and no
		// draft is opened on a stage's commit either -- the pull request
		// carries the step's bytes and nothing else.
		if r.cfg.Git.PR.EarlyDraftOn() && !r.digest && !r.commitReports && (stage == "implement" || stage == "fix") {
			if sha, ok := r.verifiedHead(ctx); ok {
				if r.rec.PR == nil {
					r.openDraft(ctx, sha, stage)
					return
				}
				r.pushVerified(ctx, sha)
			}
		}
		// What the stage left committed and still unpushed goes now,
		// fast-forward only: an unverified tip (a verified one was pushed
		// just above). It may open the draft.
		r.boundaryCheckpoint(ctx, stage)
		if !r.cfg.Git.PR.EarlyDraftOn() {
			return
		}
	}
	r.statusUpdate(ctx, stage, false)
}

// pushBranch pushes HEAD (sha) with the run's current credentials, on a
// bound of its own. A failure is the caller's to warn about; finalize
// pushes again.
func (r *run) pushBranch(ctx context.Context, sha string) error {
	if r.shadow {
		// A challenger arm never pushes, on any path (A2.1): an early draft,
		// a verified tip and a checkpoint all come through here, and the
		// guard is here rather than at each caller because the promise is
		// absolute and the callers are many.
		return nil
	}
	r.refreshForPush(ctx)
	if rej := r.workflowGuard(ctx); rej != nil {
		return rej // no network call for a push GitHub is sure to refuse
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

// refreshForPush refreshes the git credentials for a mid-run push, on a
// bound of its own; a failure warns once and keeps the current ones.
func (r *run) refreshForPush(ctx context.Context) {
	actx, cancel := context.WithTimeout(ctx, authRefreshTimeout)
	err := r.refreshGitAuth(actx, authValidity(max(r.wf.Timeouts.FinalizeReserve.Duration, bootstrapAuthMinValid)))
	cancel()
	if err != nil {
		r.warnAuthRefresh(err)
	}
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

// openDraft pushes the verified HEAD and opens the draft pull request. A
// failure only warns: finalize opens the PR if none was recorded.
func (r *run) openDraft(ctx context.Context, sha, stage string) {
	if r.shadow {
		// A challenger arm opens no pull request, the early draft included
		// (A2.1). Nothing would have been pushed for it either
		// (pushBranch), so this is the boundary and not a shortcut.
		return
	}
	if err := r.pushBranch(ctx, sha); err != nil {
		r.d.Log.Warn("pushing for the early pull request failed; finalize opens it", "err", r.redact(err.Error()))
		return
	}
	r.openDraftPR(ctx, stage, false)
}

// openDraftPR opens the draft pull request for the branch already pushed and
// records its number before anything else. during says it is a checkpoint's,
// mid-stage, rather than the verified boundary's. A failure only warns:
// finalize opens the PR if none was recorded.
func (r *run) openDraftPR(ctx context.Context, stage string, during bool) {
	if r.shadow {
		// No pull request of a challenger arm's is ever opened, so none is
		// opened here (A2.1): the plan says the side effect and not just the
		// path that usually reaches it.
		return
	}
	title, desc := r.earlyPRText()
	r.pr.lastStatus = r.d.Now()
	section := r.redact(r.runningSection(stage, during))
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
	r.d.Log.Info("early draft pull request opened", "pr", pr.Number, "url", pr.URL, "checkpoint", during)
}

func (r *run) warnTruncated(trunc bool) {
	if trunc && !r.pr.truncWarn {
		r.pr.truncWarn = true
		r.d.Log.Warn("the pull request description is too long for a full status section; it was cut")
	}
}

// statusUpdate rewrites the status section at a stage boundary or a
// checkpoint (during), at most once per statusMinInterval; after
// statusMaxFailures failures in a row it stops until finalize. A failure is
// a warning, never a failed run.
func (r *run) statusUpdate(ctx context.Context, stage string, during bool) {
	if r.shadow {
		// A challenger arm has no pull request to carry a status section
		// (A2.1), and none of the calls below could do anything but fail.
		return
	}
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
	err := r.writeStatus(ctx, n, r.redact(r.runningSection(stage, during)), statusCallTimeout)
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
	if r.shadow {
		// A challenger arm writes no status section, on any path (A2.1), and
		// this is the one method that reads a pull request and updates it. It
		// is guarded here rather than at its callers because a governance
		// halt reaches it through checkpointPR, which needs no pull request of
		// its own to find a number: a follow-up's bootstrap records one, and
		// the arm of a follow-up publishes nothing like any other arm.
		return nil
	}
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
		title = "Fugaro run " + inlineText(task.ShortRun(r.rec.RunID)) // the agent title was only markers
	}
	body = scrubDescription(body)
	if runes := []rune(title); len(runes) > maxTitleRunes {
		title = string(runes[:maxTitleRunes-1]) + "…"
	}
	body = clipBody(body, min(maxBodyBytes, r.statusOptions().Limit-statusReserve))
	return title, r.withFixes(body)
}

// ticketRE is a work item's external id as this file reads it back:
// `github:<owner>/<repo>#<n>`, the ticket outside Fugaro the run's item came
// from. The shape is the orchestrator's (its externalRE, applied where the id
// is written and again where the node is read), and the field on the task is
// a copy of an id already admitted there — but a task is a document this
// runner reads from its own bucket, so a hand-written one can carry anything,
// and what is put in a pull request's body is published: this is the read of
// the copy, not a second rule about what a ticket is. The owner, the name and
// the number are the classes the daemon writes, so an id outside them names
// no issue this run can honestly claim and no text it could use to mention
// anyone.
var ticketRE = regexp.MustCompile(`^github:([a-z0-9][a-z0-9._-]*)/([a-z0-9][a-z0-9._-]*)#([1-9][0-9]{0,18})$`)

// fixesLine is the line the pull request of a run for a GitHub item carries
// so that merging it closes the issue the run was launched for (D2727, B5):
// GitHub's own closing keyword in the pull request's body, and no daemon
// write. `Fixes #<n>` names an issue in the run's own repository, which is
// what GitHub closes on a merge into the default branch; another
// repository's issue is spelled out whole, `Fixes <owner>/<repo>#<n>`, so a
// reader lands on the ticket even where the two repositories do not share
// one.
//
// "" for a task that carries no external id — every run a person starts, and
// a daemon launch whose job image cannot read the field (internal/cli's
// externalSkew drops it there) — and for one whose id is not a ticket
// (ticketRE): the run opens its pull request without a closing line rather
// than failing over a link, exactly as the launch does.
func (r *run) fixesLine() string {
	if r.spec.External == "" {
		return ""
	}
	m := ticketRE.FindStringSubmatch(r.spec.External)
	own, err := task.CanonicalRepo(r.spec.Repo)
	if m == nil || err != nil {
		r.d.Log.Warn("the task's external id is not a ticket this run can name, so its pull request carries no closing line", "external", safetext.OneLine(r.spec.External))
		return ""
	}
	if m[1]+"/"+m[2] == own {
		return "Fixes #" + m[3]
	}
	return "Fixes " + m[1] + "/" + m[2] + "#" + m[3]
}

// withFixes appends the closing line to a finished body: the line goes after
// the clip rather than before it, so a description that fills the bound
// cannot cut the one line the issue's closing depends on.
func (r *run) withFixes(body string) string {
	line := r.fixesLine()
	if line == "" {
		return body
	}
	return body + "\n\n" + line
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

// shortSHA shortens a digest the harness's own records state -- a verify
// record's HeadSHA -- to seven characters. It checks nothing about the text: a
// verify record may carry a placeholder rather than a commit ("abc" is what this
// package's own fixtures write), so this is a cut and not an abbreviation of an
// object id. A head a run records goes through shortGitHead instead.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// validGitHead reports whether s is a git object id: 40 lowercase hex
// characters, the SHA-1 a `git rev-parse` writes, or 64, the SHA-256 of a
// repository created with --object-format=sha256.
func validGitHead(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// shortGitHead shortens a head a record states for the section: 7 characters of
// a SHA-1 object id and 12 of a SHA-256 one, git's own abbreviated length for
// each, and "(invalid)" for anything that is not an object id.
//
// It rejects rather than cuts because the section is Markdown in a pull request
// body and the record is launcher-writable: a hand can put a backtick, a line
// break or a whole forged status marker in Head or PushedHead, and seven bytes
// of that would land in the body. A head that is not an object id is one no run
// can have written, so the section says so instead of printing it — the answer
// diagnose gives a record's head too (internal/cli/diagnose.go's shortGitHead,
// which this cannot share: the CLI's diagnosis and the runner's section read the
// same record field from two packages, neither of which imports the other).
func shortGitHead(s string) string {
	if !validGitHead(s) {
		return "(invalid)"
	}
	if len(s) == 40 {
		return s[:7]
	}
	return s[:12]
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

// reviewsPart is the section's account of the review rounds, or "". With a
// first line, it says which tier the latest review is: the first line only
// advises, the senior review decides and level 3 decides over both (D723). pol
// is the mode's findings policy for the round the line reports (finalSection);
// a round the mode let through names each finding it tolerated beside the count
// (T3.3). The line ends with what reviewed the round — its model, its effort
// and the commit it read (reviewFactsPart, CLI 0.16.0) — so a reader of the
// pull request can tell which model shipped which head.
//
// The round the line reports is the policy's where the policy names one, and
// the last review the record holds only where it names none (the running
// section, which asks with the zero policy). The two are not the same round:
// after a bounce the cheap loop's rounds are the last ones written while the
// round readiness read is the outer one behind them, and the policy is that
// round's split — so a line built from the record's last entry asks
// toleratedPart about a round the split was never made from, and the count it
// compares is the wrong round's. Where the two are equal in number, which the
// levels' own numbering makes possible, the line would file the outer round's
// acceptance as the cheap round's.
func reviewsPart(rs []runstore.ReviewSummary, pol FindingsPolicy) string {
	if len(rs) == 0 {
		return ""
	}
	last := rs[len(rs)-1]
	if pol.Round > 0 {
		if d := decidingRound(rs, pol.Round); d != nil {
			last = *d
		}
	}
	who := ""
	switch last.Tier {
	case runstore.TierFirst:
		who = "first-line "
	case runstore.TierSenior:
		who = "senior "
	case runstore.TierAdversarial:
		who = "adversarial "
	}
	switch last.Verdict {
	case "ship":
		return fmt.Sprintf("%sreview: ship in round %d%s", who, last.Round, reviewFactsPart(last))
	case "none":
		if last.Tier == runstore.TierFirst {
			return fmt.Sprintf("first-line review round %d: skipped, no verdict%s", last.Round, reviewFactsPart(last))
		}
		// An outer round is never skipped: this is one whose answer carried no
		// parseable verdict, retried once and then left the run with no verdict
		// at all (seniorLoop, adversarialLoop), and the line is the reason Decide
		// gave for keeping the pull request a draft.
		return fmt.Sprintf("%sreview round %d returned no parseable verdict after a retry%s", who, last.Round, reviewFactsPart(last))
	}
	return fmt.Sprintf("%sreview round %d: %s%s%s", who, last.Round, Plural(last.Findings, "finding"), toleratedPart(pol, last), reviewFactsPart(last))
}

// reviewFactsPart is what reviewed the round the section's line reports — the
// model it ran on, the reasoning effort it ran at and the commit it read — as
// the parenthetical the line carries (CLI 0.16.0): "senior review: ship in
// round 1 (model X, effort Y, at abc1234)". Each part is written only when the
// round recorded it, so a record written before the fields existed, a level
// that named no effort and a run with no head to read all leave the
// parenthetical out rather than printing an empty claim, and "" is what every
// line of a record that carries none ends with.
//
// It is appended last on every line, tolerated part included, so the verdict,
// the count and the policy that let a round through read as one clause and the
// reviewer's own facts stand apart from them.
//
// The model and the effort are the runner's own vocabulary, but they are the
// record's bytes: a hand can edit result.json, so both go through inlineText
// like every other string the section prints. The head is the record's bytes
// too, and shortGitHead is what keeps it to an object id.
func reviewFactsPart(last runstore.ReviewSummary) string {
	parts := make([]string, 0, 3)
	if m := inlineText(last.Model); m != "" {
		parts = append(parts, "model "+m)
	}
	if e := inlineText(last.Effort); e != "" {
		parts = append(parts, "effort "+e)
	}
	if last.Head != "" {
		parts = append(parts, "at "+shortGitHead(last.Head))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (recorded: " + strings.Join(parts, ", ") + ")"
}

// levelNames are the levels as the section's own line names them, in the order
// a run holds them. The index is the level's own number, so the three read
// together as the ladder they are.
var levelNames = map[policy.Level]string{policy.LevelL1: "level 1", policy.LevelL2: "level 2", policy.LevelL3: "level 3"}

// levelsPart is the section's line per review level the run held: the level,
// its last round's verdict, the head that round approved and the focus the
// level was asked to look at (D727, L4.2). It exists beside reviewsPart, which
// reports the last round of all, because the levels can approve different
// commits: after an adversarial rejection the cheap loop runs and level 2 is
// never reviewed again, so a run that ships carries a level 2 verdict about an
// earlier head than the one it ships. A reader of the pull request has to be
// able to see that.
//
// The focus is shown beside every level whose reviewer was given one, so the
// line says which level was asked to look at what: each of the three levels'
// reviewer reads its own focus (reviewPromptWithFocus, AdversarialPrompt,
// levelFocus), and a level no layer gave one to shows none.
//
// The focus is the same sanitiser's business as the verdict beside it, and the
// two agree on every value that can reach here: the resolver refuses a focus
// carrying a control, a bidi or a zero-width character at every layer that may
// write one (config.LevelSetting.validate), so there is nothing left for
// safetext to escape that inlineText does not already handle.
//
// "" for a run that resolved no levels (levelsResolved) and for one whose
// levels left no round at all: reviewsPart's own line is then the whole
// account, and an empty line would only repeat it.
func (r *run) levelsPart() string {
	if !r.levelsResolved() {
		return ""
	}
	var lines []string
	for _, l := range []policy.Level{policy.LevelL1, policy.LevelL2, policy.LevelL3} {
		if !levelOn(r.levels, l) {
			continue
		}
		rs := lastRoundOf(r.rec.Reviews, l)
		if rs == nil {
			continue
		}
		line := fmt.Sprintf("%s: %s in round %d", levelNames[l], inlineText(rs.Verdict), rs.Round)
		if rs.Head != "" {
			line += fmt.Sprintf(" on `%s`", shortGitHead(rs.Head))
		}
		if f := inlineText(r.levelFocus(l)); f != "" {
			line += fmt.Sprintf(" (focus: %s)", f)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "review levels: " + strings.Join(lines, "; ")
}

// toleratedPart is the rest of a review line whose round the run's mode let
// through: one label per tolerated finding, in the reviewer's own order
// (rigor.Split), and the mode that let them through — the words Decide's own
// reason uses, so the body and the record can be diffed (D18, T3.3).
//
// A round the stop rule ended the run's review on reads "accepted at <mode>
// (<half>)": the same list, and the two facts Decide's own reason states for it
// (FindingsPolicy.AtStop, the owner's ruling of 2026-11-08) — which half of the
// stop left the findings, so a reader of the body can tell a run that merged at
// its time threshold from one whose review rounds ran out. The words are still
// the policy's, so the body and the record cannot disagree.
//
// "" for every other round, including a round the mode only partly tolerated:
// what was let through is the whole round or nothing, and a line naming the
// findings a mode would have tolerated in a round it blocked would be
// claiming an answer Decide never gave (D7, F4). A first-line review never
// decides (seniorReview), so it tolerates nothing whatever its counts say —
// with the stop's own exception, which is the one moment readiness reads a
// cheap round: where the loop stopped before an outer level could start, that
// round is the deciding one (firstLineStop, item 6b), the policy is its split,
// and the ruling files its findings on the body exactly as it files a senior
// round's. The tier alone cannot tell the two apart, which is why the
// exception is asked for by the stop (acceptsAtStop) and not by the tier. A
// run that never reaches Decide tolerates nothing either, which is why the
// running section asks with the zero policy.
func toleratedPart(pol FindingsPolicy, last runstore.ReviewSummary) string {
	if !pol.tolerates(&last) || (last.Tier == runstore.TierFirst && !pol.acceptsAtStop(&last)) {
		return ""
	}
	labels := make([]string, 0, len(pol.Tolerated))
	for _, s := range pol.Tolerated {
		labels = append(labels, string(s.Label()))
	}
	part := ", tolerated at " + inlineText(string(pol.Mode))
	if pol.AtStop != "" {
		// The stop's own words only where the stop is what decided the run: a
		// round it would have taken and did not — one whose commit is not the
		// one the run publishes (UnreadFix), or one a level under the decider
		// left a finding the stop does not take (StopBlocking) — is a draft, and
		// a body that filed the acceptance anyway would claim a merge the record
		// has no block for (acceptsAtStop).
		if !pol.acceptsAtStop(&last) {
			return ""
		}
		part = ", accepted at " + inlineText(string(pol.Mode)) + " (" + inlineText(pol.AtStop) + ")"
	}
	return part + " (" + inlineText(strings.Join(labels, ", ")) + ")"
}

// checkpointReportPart is the section's checkpoint report, written when a
// governance stop halted the run (T6.1, D329, G7): the tripwire's trigger, the
// runner's words for why the run is over, and whichever of the stopping
// agent's three answers the checkpoint stage filled (T7.3). Only fields that
// are set are rendered — a checkpoint written before that stage exists carries
// the trigger and the runner's own two facts alone — and every one of them
// goes through inlineText, so the fields the agent fills cannot forge section
// markup.
//
// The trigger is in a code span so it reads as the vocabulary value it is; the
// estimate says whose figure it is, because it is the record's model spend and
// not a forecast of what finishing would cost. "" for every run that did not
// take a governance halt.
//
// The report is read under r.mu (checkpointValue), the lock the halt writes it
// under (governanceHalt). Today the halt has stopped the stage's checkpoint
// poll before either happens, so no poll renders this section beside that
// write; the copy is what keeps a reader whole if the stopping agent's answers
// are later filled in through the same report (T7.3).
func (r *run) checkpointReportPart() string {
	cp := r.checkpointValue()
	if cp == nil {
		return ""
	}
	parts := []string{"checkpoint `" + inlineText(cp.Trigger) + "`"}
	if cp.WhyOver != "" {
		parts = append(parts, inlineText(cp.WhyOver))
	}
	if cp.Done != "" {
		parts = append(parts, "done: "+inlineText(cp.Done))
	}
	if cp.Next != "" {
		parts = append(parts, "next: "+inlineText(cp.Next))
	}
	if cp.EstimateUSD > 0 {
		parts = append(parts, fmt.Sprintf("estimate $%.2f (the model spend so far)", cp.EstimateUSD))
	}
	if cp.HardInstructions != "" {
		parts = append(parts, "hard to satisfy: "+inlineText(cp.HardInstructions))
	}
	return strings.Join(parts, "; ")
}

// checkpointValue is a copy of the record's checkpoint report, or nil: it is
// read under r.mu, where the halt writes it (governanceHalt), and copied
// rather than taken by pointer because the fields a later stage fills in are
// written through it (T7.3).
func (r *run) checkpointValue() *runstore.Checkpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec.Checkpoint == nil {
		return nil
	}
	cp := *r.rec.Checkpoint
	return &cp
}

// rigorPart is the section's line for the mode the run resolved (D18, D24):
// the mode, the layer that decided it, and the strictest area rule that
// matched, with a "+N" for the rest D23 also recorded — the shape D24 gives,
// and the same words the record carries, so the two can be diffed (D18's
// one-value rule: the mode is not re-derived here, it is read from the block
// the runner wrote).
//
// The mode is lower-case because the record's is (the vocabulary's own
// spelling), and the layer is the record's own word for it. "" for a run whose
// record carries no block: an absent answer is not the built-in default, and a
// line claiming one would be a mode nobody resolved.
func (r *run) rigorPart() string {
	rg := r.rec.Rigor
	if rg == nil {
		return ""
	}
	out := "rigor: " + string(rg.LevelOf()) + " (" + string(rg.From)
	if len(rg.Areas) > 0 {
		out += " " + inlineText(rg.Areas[0].Name)
		if n := len(rg.Areas) - 1; n > 0 {
			out += fmt.Sprintf(" +%d", n)
		}
	}
	return out + ")"
}

// costPart is the model cost for the section: dollars for api-key and
// Vertex runs (against the run's cap when it has one), notional for oauth.
func (r *run) costPart() string {
	r.updateCost()
	c := r.rec.Cost
	if c == nil {
		return ""
	}
	if c.AllNotional() {
		return fmt.Sprintf("notional $%.2f (not billed)", c.ModelSubscriptionUSD())
	}
	// The cap is enforced on money, so the figure beside it is the money part
	// of the model spend: a cascade run's tier-A dollars are the
	// subscription's and are in no cap (C1.16, P12, D140).
	money := c.ModelMoneyUSD()
	if r.spend.Cap > 0 {
		return fmt.Sprintf("model cost $%.2f of $%s cap", money, trimUSD(r.spend.Cap.USD()))
	}
	return fmt.Sprintf("model cost $%.2f", money)
}

func trimUSD(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	return strings.TrimSuffix(strings.TrimSuffix(s, "0"), ".0")
}

// sectionLines joins the section's parts: a one-line part goes on the first
// line, and a part carrying a block of its own is appended under it, in the
// order the blocks already are (the hand-back, LV4.3).
func (r *run) sectionLines(head string, parts ...string) string {
	kept := []string{head}
	var blocks []string
	for _, p := range parts {
		switch {
		case p == "":
		case strings.Contains(p, "\n"):
			blocks = append(blocks, p)
		default:
			kept = append(kept, p)
		}
	}
	line := strings.Join(kept, " · ")
	// The section names the run by its short ID, the form spec.md §1 settles on
	// (D1701). The command it tells a reader to paste names the run exactly:
	// the CLI resolves an ending only once a lookup accepts one (resolveRun,
	// wired at SR14), and a hint that refuses its own argument is worse than a
	// longer line -- a full run ID is what every version of the CLI takes.
	id := inlineText(task.ShortRun(r.rec.RunID))
	out := line + "\n"
	// The questions the agent asked open the section, above its change set, its
	// decisions and its deferrals (LV5.2, LD8, LF11): the run stopped on a call
	// it may not make, and a reader who reads no further than the first lines
	// has to meet what a follow-up has to answer.
	out += r.openQuestionsPart()
	out += r.changeSetPart()
	out += r.ownerPart()
	out += r.deferredPart()
	for _, b := range blocks {
		out += b
	}
	if r.rec.DraftFallback {
		out += "This host has no draft pull requests: this is a normal pull request marked [DRAFT], so reviews may already have been requested.\n"
	}
	return out + fmt.Sprintf("Run `%s` (a stale time here means the run died: `fugaro diagnose %s`)",
		id, inlineText(r.rec.RunID))
}

// changeSetPart is the section's change-set block (D1): the run is one member
// of a set launched together, and the block names the runs beside it so a
// reader of one member's pull request can find them. It sits under the first
// line, above the decisions and the deferrals, because it says what the run is
// rather than what it left.
//
// "" for a run in no set, which is every other run: a launch that is in no
// change set carries none (task.Spec.ChangeSet), and the section it writes is
// the one it wrote before the field existed.
//
// The members are the run's own task's (D642) and are already canonical
// (task.Validate), and no sibling's pull request is knowable here: a run
// reaches only its own repository's prefixes in the runs bucket (SECURITY.md),
// so the block a member writes leaves them unnamed and the settle step, which
// reads every member's record, is what fills them in (D3).
func (r *run) changeSetPart() string {
	// A run under test can have no task at all (the section is rendered from
	// the record alone in most of these paths), and a run with no task is in no
	// set.
	if r.spec == nil || r.spec.ChangeSet == nil {
		return ""
	}
	cs := r.spec.ChangeSet
	members := make([]gitprov.ChangeSetMember, 0, len(cs.Members))
	for _, m := range cs.Members {
		members = append(members, gitprov.ChangeSetMember{Repo: inlineText(m.Repo), RunID: inlineText(task.ShortRun(m.RunID))})
	}
	return gitprov.ChangeSetLine(inlineText(task.ShortRun(cs.ID)), inlineText(task.ShortRun(r.rec.RunID)), members)
}

// changeSetHoldReason is the sentence a member run's record and pull request
// carry instead of the ready one: why a run that answered ready is left a draft
// (D614). It names the set the way the pull request's own change-set block does
// (the short form, which is what a reader of one member can see) and says what
// lifts the hold, because neither the draft state nor the record says it: what
// turns the pull request ready is the set's own step, once every member has
// shipped (D3).
func changeSetHoldReason(cs *task.ChangeSet) string {
	return fmt.Sprintf("held: the pull requests of change set %s go ready together, when every member has shipped",
		inlineText(task.ShortRun(cs.ID)))
}

// noteChangeSetMember records this run as a member of its change set, with the
// pull request it just opened. The record lives in the member run's prefix, the
// only one a run of its repository reaches, and it is what a step that reads a
// whole set reads from this member (D3); the run's own verdict joins it when
// the run ends (recordChangeSetVerdict, D2). A run in no change set records
// nothing.
func (r *run) noteChangeSetMember(ctx context.Context, pr gitprov.PR) {
	r.writeChangeSetMember(ctx, pr, "", "", false)
}

// recordChangeSetVerdict writes the member's verdict once the run has ended
// (D2), so the step that settles a whole set reads each member's own word about
// whether anything must change before its pull request is merged (D3). It
// writes the empty verdict for a run that ended without shipping, which is what
// clears one an earlier attempt at the same pull request left behind: a member
// whose follow-up failed must not be settled from a `ship` nobody stands by.
//
// The commit the verdict is about goes with it: the head this run pushed and
// whether a clean-tree test passed there. It has to travel with the verdict
// rather than being read later from this run's result.json, because the run
// writing this record is not always the run the set was launched from — a
// follow-up pushes to the member's branch and answers on the MEMBER's record —
// and a settle step that compared the member's pull request against the launch
// run's commit would refuse a followed-up member forever (D3).
func (r *run) recordChangeSetVerdict(ctx context.Context, pr gitprov.PR, verdict, head string, verified bool) {
	r.writeChangeSetMember(ctx, pr, verdict, head, verified)
}

// writeChangeSetMember stores the member record of this run, at pr, with
// verdict as the run's own word about merging it ("" while the run has none),
// head as the commit that word is about and verified as whether a clean-tree
// test passed there. The commit is empty when the run has no verdict yet: the
// record's word about a commit is the verdict's, and nothing else writes it. A
// run in no change set writes nothing.
//
// The record names and sits under the set's own member run — the run the set
// was launched from (task.ChangeSetMember.RunID) — never the run that happens
// to be writing: membership is fixed at the set's launch (D642), and a
// follow-up to a member's pull request answers on that member's record rather
// than making itself a member (D2). Runs of one repository share their
// prefixes, which is what lets a follow-up write there (SECURITY.md, T5).
//
// The repository is written canonically (task.CanonicalRepo) while the task
// keeps the spelling it was launched with: the record is matched against the
// member list every member's task carries, which is canonical, and a record
// that spelled its own repository differently would not match the list it
// repeats.
func (r *run) writeChangeSetMember(ctx context.Context, pr gitprov.PR, verdict, head string, verified bool) {
	cs := r.spec.ChangeSet
	if cs == nil {
		return
	}
	repo, err := task.CanonicalRepo(r.spec.Repo)
	if err != nil {
		r.d.Log.Warn("the run's repository is not a name a change set can record", "err", r.redact(err.Error()))
		return
	}
	// task.Validate requires the set to name the run's own repository, so a
	// set that does not is a task written by something other than a launcher
	// this build ships; nothing is recorded for it rather than a member the
	// set never named.
	member, ok := cs.Member(repo)
	if !ok {
		r.d.Log.Warn("the change set does not name this run's repository", "repo", repo)
		return
	}
	m := &runstore.ChangeSetMember{
		Version:  1,
		SetID:    cs.ID,
		Repo:     repo,
		RunID:    member.RunID,
		PRURL:    pr.URL,
		Verdict:  verdict,
		HeadSHA:  head,
		Verified: verified,
		At:       r.d.Now().UTC(),
	}
	if err := r.d.Store.Sibling(member.RunID).WriteChangeSetMember(ctx, m); err != nil {
		// The record is a coordination aid, not the run's own record: a set
		// whose member cannot record its pull request is one the settle step
		// reports rather than one this run fails over (T5).
		r.d.Log.Warn("storing changeset.json failed", "err", r.redact(err.Error()))
	}
}

// deferredPart is the section's list of the findings the run did not fix and
// left to a follow-up (T-TR9, D1843): one line per deferral, the key it is
// known by, the reviewer's own summary of it and the one-line why the deferral
// took effect, and the command that files them once a person has merged.
//
// What is listed is Record.Deferred, the findings whose deferral took effect:
// T-TR7b's and T-TR8's own field, which the shadow stage of T-TR7a never
// writes, so a run that only logged verdicts lists nothing here. "" for a run
// that deferred nothing at all, rather than a heading over an empty list.
//
// The summary and the why are the reviewer's and the judge's words about an
// untrusted diff, and the record they are read from is launcher-writable, so
// both go through inlineText: a sentence carrying a newline would end its own
// bullet and the line after it would be read as Markdown of the section's. The
// section as a whole is redacted where it is written (settleText), which is
// what keeps a secret a record carries out of the pull request's body.
func (r *run) deferredPart() string {
	df := r.rec.Deferred
	if df == nil || len(df.Findings) == 0 {
		return ""
	}
	return "**Deferred (follow-ups)**\n" + deferralLines(df) + followupsLine(r.rec.RunID)
}

// ownerWaiting reports whether the run ended waiting on the owner's decisions
// (OD1), whether its loop stopped on them or its review left them and the run
// ended a draft. It is what gives the section its own head instead of the
// halted or draft one — the run is over because a person has to answer, not
// because a limit stopped it.
func (r *run) ownerWaiting() bool {
	// The decisions on the record are the fact the section reads: the run
	// halted on them (ownerStop) or its review left them and the run ended a
	// draft that carries them (recordOwnerDecisions) — either way what comes
	// next is a person's answer, and the head above the list says so. The halt
	// is still read, so a record whose block could not be stored (a decision
	// with no sentence is not one) still says the run is waiting.
	if od := r.rec.OwnerDecisions; od != nil && len(od.Decisions) > 0 {
		return true
	}
	return r.rec.Halt != nil && r.rec.Halt.Reason == runstore.HaltOwnerDecision
}

// openQuestionsPart is the status section's OPEN QUESTIONS block (LV5.2, LD8,
// LF11): the questions the agent asked in its own hand-back file, one bullet
// each, and what the section it opens is for — the pull request is a draft
// until a follow-up answers them, and a reader has to see the questions to
// answer them. It opens every status section the run writes while its file
// carries them, and the final one most of all.
//
// The entries are the agent's own text about its own work, so they go through
// handBackEntry like every other hand-back entry: a question carrying a
// heading, a marker or a line break stays the text it is rather than the
// section's structure.
//
// "" for a run whose own file asked nothing: the runner's fallback body is
// built from the task text and asks nothing on the agent's behalf (handBack).
// A section whose bound left entries out says so, the way the hand-back block
// does, so a partial list never reads as the whole one.
func (r *run) openQuestionsPart() string {
	hb, fromAgent := r.handBack()
	if !askedGuidance(hb, fromAgent) {
		return ""
	}
	var b strings.Builder
	b.WriteString("**OPEN QUESTIONS**\n")
	for _, q := range hb.OpenQuestions.Entries {
		b.WriteString(handBackEntry(q) + "\n")
	}
	if hb.OpenQuestions.Dropped > 0 {
		b.WriteString(hiddenHandBackLine(hb.OpenQuestions.Dropped) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// ownerPart is the status section's list of the decisions the run is waiting
// for (OD1): one line per decision, the file the reviewer named and its own
// sentence, under the section's own head. "" for a run whose review left none.
//
// Both fields are the reviewer's text about an untrusted diff, so both go
// through inlineText: a sentence carrying a newline would end its own bullet
// and the line after it would be read as Markdown of the section's, and a
// mention or a marker in one must not reach the pull request as either.
func (r *run) ownerPart() string {
	od := r.rec.OwnerDecisions
	if od == nil || len(od.Decisions) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range od.Decisions {
		if d.File != "" {
			fmt.Fprintf(&b, "- `%s`: %s\n", inlineText(d.File), inlineText(d.Summary))
			continue
		}
		fmt.Fprintf(&b, "- %s\n", inlineText(d.Summary))
	}
	if od.Total > len(od.Decisions) {
		// The list is capped (runstore.MaxOwnerDecisions), and a section that
		// stopped silently would read as the whole answer.
		fmt.Fprintf(&b, "- and %s more; the run's record lists %d of them\n", Plural(od.Total-len(od.Decisions), "decision"), len(od.Decisions))
	}
	return b.String()
}

// binariesPart is the section's line for the binaries this run added at a path
// the project's policy admits (D993, spec.md §5): the run used the rule, and a
// reader of the pull request is told an unreadable file is in it without having
// to open the diff and find one. "" for a run that added none, which is every
// run whose task was not an asset — the section says nothing about a rule the
// run did not use.
//
// The list is bounded (maxStatusBinaries) and the count is always the whole
// one: a line that showed three paths of seven and stopped would read as a run
// that added three. The paths are the run's own diff and go through inlineText,
// so a path an agent named with a newline or a mention cannot break the line or
// notify anyone.
func (r *run) binariesPart() string {
	if len(r.binariesAdmitted) == 0 {
		return ""
	}
	shown, more := r.binariesAdmitted, 0
	if len(shown) > maxStatusBinaries {
		more, shown = len(shown)-maxStatusBinaries, shown[:maxStatusBinaries]
	}
	paths := make([]string, len(shown))
	for i, p := range shown {
		paths[i] = inlineText(p)
	}
	out := fmt.Sprintf("binaries: %d admitted (%s", len(r.binariesAdmitted), strings.Join(paths, ", "))
	if more > 0 {
		out += fmt.Sprintf(", and %d more", more)
	}
	return out + ")"
}

func (r *run) updated(label string) string {
	return label + " " + r.d.Now().UTC().Format("2006-01-02 15:04Z")
}

// runningSection is the section while the run is in progress: after stage
// ended, or (during) at a checkpoint in the middle of it. A first run's
// pushed commit without a passing clean test is marked not verified, and a
// halt recorded while the stage it stopped is still unwinding says so rather
// than "Running" (governanceHalt writes the section before the stage ends).
func (r *run) runningSection(stage string, during bool) string {
	records, _ := verify.Records(r.d.StateDir)
	head := "**Running**"
	switch {
	case r.haltValue() != nil:
		head = "**Halted**"
	case r.follow != nil:
		head = "**Follow-up running**"
	case r.rec.PushedHead != "" && !pushedVerified(records, r.rec.PushedHead):
		head = "**Running: work in progress, not verified**"
	}
	what := fmt.Sprintf("after stage `%s`", inlineText(stage))
	if during {
		what = fmt.Sprintf("checkpoint during stage `%s`", inlineText(stage))
	} else if _, ok := reviewStageOf(stage); ok {
		// The zero policy: mid-run nothing has been tolerated yet, and the
		// section names what the round left rather than what a mode would do
		// with it once the run reaches Decide (T3.3).
		if p := reviewsPart(r.rec.Reviews, FindingsPolicy{}); p != "" {
			what = p
		}
	}
	return r.sectionLines(head, what, r.checkpointReportPart(), verifyPart(records), r.pushedPart(records), r.costPart(), r.updated("updated"))
}

// pushedVerified reports whether sha has a passing test on a clean tree.
func pushedVerified(records []verify.Record, sha string) bool {
	t := latestVerifiedTest(records, sha)
	return t != nil && t.Passed
}

// pushedPart says which commit the run branch holds and whether it is
// verified; "" before a first run's first push and for a follow-up.
func (r *run) pushedPart(records []verify.Record) string {
	sha := r.rec.PushedHead
	if sha == "" || r.follow != nil {
		return ""
	}
	if pushedVerified(records, sha) {
		return fmt.Sprintf("branch at `%s`: verified", shortGitHead(sha))
	}
	return fmt.Sprintf("branch at `%s`: not verified", shortGitHead(sha))
}

// finalSection is the section when the run ended: outcome, reason, tests,
// reviews and cost, and the hand-back the run's level owes the reader (LV4.3).
// A review round the run's mode let through names each
// finding it tolerated and the mode that tolerated it (T3.3, D7, D18), and a
// run that held more than one review level lists each level's verdict and the
// head it approved (levelsPart, D727).
func (r *run) finalSection(ready bool, reason string, records []verify.Record) string {
	head, why := "**Draft**", inlineText(reason)
	switch {
	case ready:
		head, why = "**Ready for review**", ""
	case r.ownerWaiting():
		// OD1's own head, above the halted one: the run is over because a
		// person has to answer, not because a limit stopped it, and the
		// decisions it is waiting for are listed under this line (ownerPart).
		head = "**Waiting on you**"
	case r.rec.Status == runstore.StatusHalted:
		head = "**Halted**"
	case r.rec.Status == runstore.StatusCancelled:
		head = "**Cancelled**"
	}
	return r.sectionLines(head, why, r.checkpointReportPart(), r.rigorPart(), verifyPart(records), reviewsPart(r.rec.Reviews, r.findingsPolicy()), r.levelsPart(), r.narrowText, r.binariesPart(), r.promotePart(), r.costPart(), r.handBackPart(), r.updated("finished"))
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

// providerRefusal is err as an ending whose cause the run can name: the forge
// refused or failed a call the run needed — the pull request it could not
// create or settle, the ready flip that came back an error (design §3's
// git_provider_error). finalize is where that call's outcome becomes the run's,
// but the ending itself is written by Run's deferred fallback, so the class
// travels on the error (endClass, runner.go) rather than being re-derived where
// the error is read back.
//
// It does not change what a caller sees: the error still says what the forge
// said, errors.Is still answers for the sentinels finalize switches on
// (gitprov.ErrPRNotOpen), and only the class the ending is written with is
// added. applyReady's failure is not one of these: it is a note on a ready pull
// request and never ends the run.
func providerRefusal(err error) error {
	if err == nil {
		return nil
	}
	return endClass{failure.GitProviderError, err}
}
