package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// workSaveTimeout bounds saving the bundle of a run's work. It runs on a
// context of its own: finalize's may be nearly spent, and this is the only
// copy of the work.
const workSaveTimeout = 2 * time.Minute

// workFile is the bundle's name in the run's prefix.
const workFile = "work.bundle"

// workBase is the revision the run's own changes start after: the commit
// the run was cut from, so nothing a person put on the branch earlier is
// the run's. For a follow-up that is the pull request's head when it
// started; for a first run it is the task's ref as bootstrap read it right
// after the checkout — NOT the base branch, which is another branch
// whenever the run was started from one (--ref console with base_branch
// main): measured against main, a run cut from console looks like it
// added every workflow file console already carried, and the push was
// refused for files the run never touched.
//
// A full commit SHA, never a ref name: the agent controls the checkout
// while a stage runs and could repoint origin/<base> at will
// (checkpoint.go's C6a), and the SHA was read before the agent ever ran.
//
// The base branch stays the fallback for the one case that has no start
// commit — a start that could not be resolved at bootstrap (startErr),
// which is also the case checkpoints are off for.
func (r *run) workBase() string {
	if r.follow != nil {
		return r.follow.startSHA
	}
	if r.startSHA != "" {
		return r.startSHA
	}
	return r.baseRef()
}

// baseRef is the base branch as the checkout sees it: the bound a run
// without a start commit falls back to (workBase).
func (r *run) baseRef() string {
	return "origin/" + r.cfg.Git.BaseBranch
}

// runCommits counts the commits the run made itself: those after the commit
// it was cut from and on neither of the bases the run works from, whose own
// commits are theirs even when the agent merged or rebased them in
// (guardBounds). For a first run with no start commit (startErr) the
// configuration's base branch is all there is to measure against, as
// before.
func (r *run) runCommits(ctx context.Context) (int, error) {
	since, except := r.guardBounds(ctx)
	return r.repo.CommitsAhead(ctx, since, except...)
}

// guardBounds are the two revisions the run's own work is read between: the
// commit the run was cut from (workBase), and the bases the run works from,
// whose own commits are theirs even when the agent merged or rebased them
// in.
//
// There are two of those, and each is the only one that leaves out its own
// commits:
//
//   - the base the review stage diffs against (diffbase.go, diffBase) — the
//     ref a first run was cut from, the branch a follow-up's pull request
//     targets now. A run cut from integration that merged integration in
//     while it worked was refused for the workflow file integration's own
//     commit added, a change the review stage (which diffs
//     origin/integration) never sees (diffbase_matrix_test.go, cell (d)).
//   - the configuration's base branch (git.base_branch) — the branch the
//     run's pull request is opened against, and the one a run started from
//     another ref is most often told to keep up with. With only the first
//     subtracted, a run cut from integration that merged main in was refused
//     for the workflow file main's own commit added, the mirror image of the
//     case above (diffbase_matrix_test.go, cell (e)).
//
// Each base is read from the remote, as a commit SHA, at the moment of the
// check — never the checkout's origin/<base>, which the agent's own git can
// repoint (at the run's own tip, say, which would subtract every commit the
// run made and pass any workflow change). They can only ever make the list
// shorter, so a base the remote cannot name is left out here, and a base
// whose commit the checkout lacks is left out by the reader itself
// (gitops.readableBounds, one bound at a time): the bounds that do resolve
// still leave out their own commits, so one unreadable base cannot blame the
// run for a workflow file another base merged in — the false refusal that
// dropping every bound together would give. For a start that has no start
// commit (startErr: workBase falls back to the base branch) there is only
// the one bound.
func (r *run) guardBounds(ctx context.Context) (since string, except []string) {
	since = r.workBase()
	if since == r.baseRef() {
		return since, nil
	}
	seen := map[string]bool{}
	// The two bases, in the order the run's own work is read: what the diff
	// is measured against, then the branch its configuration came from. They
	// are the same branch for most runs, and reading it once is also what
	// keeps one commit from being named twice.
	for _, base := range []string{r.guardDiffBase(ctx), r.cfg.Git.BaseBranch} {
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		tip, err := r.repo.RemoteTip(ctx, base)
		if err != nil || !gitops.IsFullSHA(tip) {
			if err != nil {
				r.d.Log.Warn("reading a base branch on origin failed; that base's own commits are not subtracted, so the run's work is measured against the bases that could be read", "base", base, "err", r.redact(err.Error()))
			}
			continue
		}
		except = append(except, tip)
	}
	return since, except
}

// workflowGuard returns the refusal GitHub is certain to give, without the
// network call: with the github provider, a run whose own commits change
// files under .github/workflows/ can't be pushed, because Fugaro's App
// never has the `workflows` permission (a run able to edit CI could change
// the merge gates or reach secrets). What is the run's own is read between
// both bounds (gitops.WorkflowFilesExcept): the commit it was cut from,
// which is a base branch only when the run started from it, and the bases
// the run works from (the one its diffs are measured against and the one
// its configuration came from), whose own commits a merged or rebased-in
// base brought. nil when the push may go ahead, or when git can't tell (the
// push then decides).
func (r *run) workflowGuard(ctx context.Context) *gitops.PushRejected {
	if r.providerKind != gitprov.KindGitHub {
		return nil
	}
	since, except := r.guardBounds(ctx)
	files, err := r.repo.WorkflowFilesExcept(ctx, since, except...)
	return r.workflowRefusal(files, err)
}

// workflowGuardAt is workflowGuard for the exact commit a checkpoint is
// about to push, over the exact range it is about to add (since..until,
// since being checkpointSince and until the sha just read): the same
// GitHub-only guard over the same two bounds, so a checkpoint is refused
// exactly when the push it makes would be. It is kept apart from
// workflowGuard because gitops.WorkflowFilesInExcept (unlike the plain
// WorkflowFilesExcept finalize and pushBranch still use) requires until to
// be a full commit SHA, never "HEAD": a checkpoint always has one on hand
// (the tip it polled), so it passes it directly instead of letting git
// resolve a moving ref.
func (r *run) workflowGuardAt(ctx context.Context, since, until string) *gitops.PushRejected {
	if r.providerKind != gitprov.KindGitHub {
		return nil
	}
	if _, except := r.guardBounds(ctx); len(except) > 0 {
		files, err := r.repo.WorkflowFilesInExcept(ctx, since, until, except...)
		return r.workflowRefusal(files, err)
	}
	files, err := r.repo.WorkflowFilesIn(ctx, since, until)
	return r.workflowRefusal(files, err)
}

// workflowRefusal is the refusal files is worth, with err logged. A range
// that cannot be read is left to the push, which is what the guard always
// did when git could not tell (the base-branch side of the comparison is
// only ever a fallback anyway: see gitops.workflowFiles).
func (r *run) workflowRefusal(files []string, err error) *gitops.PushRejected {
	if err != nil {
		r.d.Log.Warn("checking the commits for workflow files failed; pushing anyway", "err", r.redact(err.Error()))
		return nil
	}
	if len(files) == 0 {
		return nil
	}
	return &gitops.PushRejected{Kind: gitops.RejectWorkflows, Files: files}
}

// asRefusal returns the permanent refusal err reports, only for the github
// provider: the classification reads GitHub's messages, and another host's
// failure keeps its plain error.
func (r *run) asRefusal(err error) *gitops.PushRejected {
	if r.providerKind != gitprov.KindGitHub {
		return nil
	}
	var rej *gitops.PushRejected
	if errors.As(err, &rej) {
		return rej
	}
	return nil
}

// refusalText is the one-line reason a refusal gets, without the work's
// whereabouts. File names come from the agent's commits, so they are made
// safe for one line of Markdown (no mention, marker or backtick).
func refusalText(rej *gitops.PushRejected) string {
	const prefix = "GitHub refused the push: "
	switch rej.Kind {
	case gitops.RejectWorkflows:
		what := "this run changed workflow files"
		if len(rej.Files) > 0 {
			names := make([]string, 0, 3)
			for _, f := range rej.Files[:min(len(rej.Files), 3)] {
				names = append(names, inlineText(f))
			}
			what = "this run changed " + strings.Join(names, ", ")
			if n := len(rej.Files) - 3; n > 0 {
				what += fmt.Sprintf(" and %d more", n)
			}
		}
		return prefix + what + " and Fugaro never has permission to change workflow files; a person must make that change"
	case gitops.RejectLargeFile:
		return prefix + "the run added a file over GitHub's file size limit"
	case gitops.RejectProtectedBranch:
		return prefix + "the branch is protected"
	case gitops.RejectSecretScanning:
		return prefix + "push protection found what looks like a secret in the run's commits"
	case gitops.RejectRepoRules:
		return prefix + "a repository rule rejected the push (see the repository's rulesets, for example signed commits or linear history)"
	}
	return prefix + "the host refused it for a reason retrying won't change"
}

// saveWork stores a git bundle of the run's commits (those after workBase)
// under the run's own prefix, so a push that can never succeed doesn't
// lose them with the container. It returns what to tell the reader.
//
// A bundle holds every object of those commits, so what is checked is every
// commit's message and patch, not the net diff: nothing is saved when a
// value the run redacts everywhere else is in any of them, when GitHub's
// own secret scanning refused the push (it found something the redactor
// doesn't know), or when a binary change can't be scanned. The bundle never
// includes the checkout's config, credentials or untracked files.
func (r *run) saveWork(ctx context.Context, kind gitops.RejectKind) string {
	if kind == gitops.RejectSecretScanning {
		return "the work was not saved because GitHub found what looks like a secret in it; it is lost with the container"
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workSaveTimeout)
	defer cancel()
	since := r.workBase()
	redact := agent.RedactFunc(r.secretList())
	scan, err := r.repo.ScanWork(wctx, since, func(text string) bool { return redact(text) != text })
	switch {
	case err != nil:
		r.d.Log.Warn("scanning the work failed", "err", r.redact(err.Error()))
		return "the work was not saved because it could not be checked for secrets"
	case scan.Hit:
		return "the work was not saved because its commits hold a secret value; it is lost with the container"
	case scan.TooLarge:
		return "the work was not saved because its commits are too large to check for secrets; it is lost with the container"
	case scan.Unscannable:
		return "the work was not saved because it changes binary files, which can't be checked for secrets; it is lost with the container"
	}
	bundle, err := r.repo.Bundle(wctx, since)
	switch {
	case errors.Is(err, gitops.ErrBundleTooLarge):
		return "the work is too large to save and is lost with the container"
	case err != nil:
		r.d.Log.Warn("bundling the work failed", "err", r.redact(err.Error()))
		return "the work could not be saved"
	}
	if err := r.d.Store.PutFile(wctx, workFile, bundle, "application/octet-stream"); err != nil {
		r.d.Log.Warn("storing the work bundle failed", "err", r.redact(err.Error()))
		return "the work could not be saved"
	}
	return fmt.Sprintf("the work is saved at %s%s in the runs bucket; download it and run: git fetch %s HEAD (in a clone)", r.d.Store.Prefix(), workFile, workFile)
}

// endRefused records a run whose push the host will never accept, and
// returns the error finalize ends with, so the run exits non-zero and the
// job shows failed, as a failed push always did. The status is the one a
// failed push always had, infra_error with outcome none, whatever a halt
// or cancel recorded (the record keeps the halt); only the reason is new,
// and the work is saved. prNumber, when not 0, is a pull request that
// exists: while it is open it gets a note, and a first run's draft its
// "Stopped" section.
func (r *run) endRefused(ctx context.Context, rej *gitops.PushRejected, prNumber int, records []verify.Record) error {
	reason := r.redact(refusalText(rej) + " (" + r.saveWork(ctx, rej.Kind) + ")")
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, reason
	r.d.Log.Warn("the push was refused", "reason", reason)
	r.storeEnded(ctx, records)
	if prNumber != 0 && r.giveUpNoteAllowed(ctx) {
		pr := gitprov.PR{Number: prNumber}
		if r.rec.PR != nil && r.rec.PR.Number == prNumber {
			pr.URL = r.rec.PR.URL
		}
		note := "**Fugaro:** " + reason + ". Nothing more was pushed.\n" + gitprov.ReportMarker(r.rec.RunID) + "\n"
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
		defer cancel()
		if err := r.provider.Comment(cctx, pr, note); err != nil {
			r.d.Log.Warn("posting the note about the refused push failed", "err", r.redact(err.Error()))
		}
		if r.follow == nil {
			// The early draft still says it is running.
			stopped := "**Stopped:** " + inlineText(reason)
			if err := r.settleText(ctx, prNumber, r.sectionLines(stopped, r.updated("stopped"))); err != nil {
				r.d.Log.Warn("updating the pull request status failed", "err", r.redact(err.Error()))
			}
		}
	}
	return errors.New(reason)
}
