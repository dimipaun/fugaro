package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

// workflowGuard returns the refusal GitHub is certain to give, without the
// network call: with the github provider, a run whose commits change files
// under .github/workflows/ can't be pushed, because Fugaro's App never has
// the `workflows` permission (a run able to edit CI could change the merge
// gates or reach secrets). nil when the push may go ahead, or when git
// can't tell (the push then decides).
func (r *run) workflowGuard(ctx context.Context) *gitops.PushRejected {
	if r.providerKind != gitprov.KindGitHub {
		return nil
	}
	files, err := r.repo.WorkflowFiles(ctx, r.cfg.Git.BaseBranch)
	if err != nil {
		r.d.Log.Warn("checking the commits for workflow files failed; pushing anyway", "err", r.redact(err.Error()))
		return nil
	}
	if len(files) == 0 {
		return nil
	}
	return &gitops.PushRejected{Kind: gitops.RejectWorkflows, Files: files}
}

// refusalText is the one-line reason a refusal gets, without the work's
// whereabouts.
func refusalText(rej *gitops.PushRejected) string {
	const prefix = "GitHub refused the push: "
	switch rej.Kind {
	case gitops.RejectWorkflows:
		what := "this run changed workflow files"
		if len(rej.Files) > 0 {
			what = "this run changed " + strings.Join(rej.Files[:min(len(rej.Files), 3)], ", ")
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
	}
	return prefix + "the host refused it for a reason retrying won't change"
}

// saveWork stores a git bundle of the run's commits (those HEAD has and
// origin/<base> doesn't) under the run's own prefix, so a push that can
// never succeed doesn't lose them with the container. It returns what to
// tell the reader. The bundle holds only commits, never the checkout's
// config, credentials or untracked files, and it is not saved at all when
// the commits hold a value the run redacts everywhere else.
func (r *run) saveWork(ctx context.Context) string {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workSaveTimeout)
	defer cancel()
	base := r.cfg.Git.BaseBranch
	bundle, err := r.repo.Bundle(wctx, base)
	switch {
	case errors.Is(err, gitops.ErrBundleTooLarge):
		return "the work is too large to save and is lost with the container"
	case err != nil:
		r.d.Log.Warn("bundling the work failed", "err", r.redact(err.Error()))
		return "the work could not be saved"
	}
	patch, err := r.repo.Patch(wctx, base)
	if err != nil {
		r.d.Log.Warn("reading the work's diff failed", "err", r.redact(err.Error()))
		return "the work could not be saved"
	}
	if r.redact(patch) != patch {
		return "the work was not saved because its commits hold a secret value; it is lost with the container"
	}
	if err := r.d.Store.PutFile(wctx, workFile, bundle, "application/octet-stream"); err != nil {
		r.d.Log.Warn("storing the work bundle failed", "err", r.redact(err.Error()))
		return "the work could not be saved"
	}
	return fmt.Sprintf("the work is saved at %s%s in the runs bucket; download it and run: git fetch %s HEAD (in a clone)", r.d.Store.Prefix(), workFile, workFile)
}

// endRefused ends a run whose push the host will never accept: the status
// stays infra_error, as for any push that fails, but the reason says why
// and where the work is. prNumber, when not 0, is a pull request that
// exists: it gets a note too, so people watching it are told.
func (r *run) endRefused(ctx context.Context, rej *gitops.PushRejected, prNumber int, records []verify.Record) {
	reason := r.redact(refusalText(rej) + " (" + r.saveWork(ctx) + ")")
	r.endUnchangedAs(ctx, runstore.StatusInfraError, reason, records)
	if prNumber == 0 {
		return
	}
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
