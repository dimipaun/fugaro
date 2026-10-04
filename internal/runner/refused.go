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

// workBase is the revision the run's own commits start after: the base
// branch for a first run, and for a follow-up the commit it started from,
// so commits people put on the branch earlier are not the run's.
func (r *run) workBase() string {
	if r.follow != nil {
		return r.follow.startSHA
	}
	return "origin/" + r.cfg.Git.BaseBranch
}

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
	files, err := r.repo.WorkflowFiles(ctx, r.workBase())
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
