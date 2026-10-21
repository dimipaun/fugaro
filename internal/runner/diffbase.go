package runner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// diffBase is the branch every diff this run takes is measured against:
// `origin/<diffBase>...HEAD` is the range the review prompt names, the range
// the review input is built over, what the prefetch fills the checkout in
// for, and what the follow-up's own diff stat is read against.
//
// It is deliberately NOT r.cfg.Git.BaseBranch, which is the base the run's
// own pull request is opened against and the branch its configuration comes
// from. Those two are not the run's base of work, and for a run started from
// another ref (`fugaro run --ref integration-0.9.6`) on a repository whose
// fugaro.yaml still names the default branch, `origin/main...HEAD` is the
// whole of the integration branch's divergence — measured on run 865e of PR
// #754: 165 review-stage diffs of that range, ~33,000 changed lines in 186
// files, and review stages spending 700-1500 model calls against 30-120 for
// implement, in 4 to 17 sub-agent fan-outs, out of a $10 run cap. The
// reviewer never reached the pull request's own change.
//
// So, for the three ways a run has a base:
//
//   - a first run diffs against the ref it was cut from (spec.Ref): the
//     branch the work started on is the base of the work;
//   - a follow-up diffs against the branch its pull request targets NOW,
//     read from the provider (r.follow.pr.Base — the follow-up state is
//     filled at bootstrap, before the prefetch). The previous run's base is
//     stale the moment someone retargets the pull request (main ->
//     integration-0.9.6), and reading it was why a follow-up on a retargeted
//     pull request reviewed main's own history;
//   - a review run diffs each reviewed pull request against the base that
//     pull request targets now (reviewHead.Base, per head); its own --ref
//     is the fallback, and the branch its configuration comes from.
//
// A value that is not a branch name is refused rather than used: it goes
// into a git revision (`origin/<base>...HEAD`) and into the reviewer's own
// prompt. An empty or refused one falls back — for a follow-up to the ref
// it ran at, and from there to the configuration's own base, the
// repository's default branch.
func (r *run) diffBase() string {
	if r.follow != nil {
		if b := r.follow.pr.Base; b != "" {
			if config.ValidBranchName(b) {
				return b
			}
			r.d.Log.Warn("the pull request's base branch is not a branch name; using the run's own ref",
				"base", r.redact(b))
		}
	}
	return r.refBase()
}

// refBase is diffBase's fallback: the run's own ref when it is a branch
// name, else the configuration's base (the repository's default branch).
// spec is nil in the unit tests that call a single method on a bare run.
func (r *run) refBase() string {
	if r.spec != nil {
		if b := r.spec.Ref; config.ValidBranchName(b) {
			return b
		}
	}
	return r.cfg.Git.BaseBranch
}

// reviewPromptBase is the base the review stage names, in the prompt and in
// the input built for it (reviewinput.go): the one reviewed pull request's
// own base for a single-pull-request review run — whose checkout holds that
// head — and diffBase for every other run, which has one range of its own.
func (r *run) reviewPromptBase() string {
	if r.review && len(r.reviewHeads) == 1 {
		return r.reviewBase(r.reviewHeads[0])
	}
	return r.diffBase()
}

// diffRange is one range the run's diffs are taken over: head, measured
// against base (`origin/<base>...<head>`). A run has one — its own
// checkout's HEAD — except a review run, which has one per reviewed pull
// request, each against the base that pull request targets.
type diffRange struct {
	base, head string
}

// diffRanges is what the prefetch fetches for, in order: the review run's
// own per-head ranges, else the run's single range.
func (r *run) diffRanges() []diffRange {
	if r.review {
		out := make([]diffRange, 0, len(r.reviewHeads))
		for _, h := range r.reviewHeads {
			out = append(out, diffRange{base: r.reviewBase(h), head: h.SHA})
		}
		return out
	}
	return []diffRange{{base: r.diffBase(), head: "HEAD"}}
}

// reviewBase is the base one reviewed pull request is diffed against: the
// branch it targets now, else the run's own ref.
func (r *run) reviewBase(h reviewHead) string {
	if b := h.Base; b != "" {
		if config.ValidBranchName(b) {
			return b
		}
		r.d.Log.Warn("the reviewed pull request's base branch is not a branch name; using the run's own ref",
			"pr", h.Number, "base", r.redact(b))
	}
	return r.refBase()
}

// diffSize measures the range rng (`base...head`, as the prompt spells it)
// through the checkout's own git: how many files it changes and how many
// lines it adds or removes. A binary file (numstat's "-" counts) is a file
// and no lines. Nothing here is trusted to be small: the numbers only
// decide whether the reviewer is warned (prompts.go's hugeDiffNote).
func diffSize(ctx context.Context, repo *gitops.Repo, rng string) (DiffSize, error) {
	out, err := sessionGit(ctx, repo, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv",
		"--end-of-options", rng)
	if err != nil {
		return DiffSize{}, err
	}
	_, size, err := parseNumstat(out)
	return size, err
}

// reviewDiffSize is the size of the diff the review stage is about to be
// handed, for its prompt: the run's own range, or — for a review run, whose
// checkout dir holds the single reviewed head — that pull request's range.
// A failure is a warning and an empty size (no note), never a failed run:
// the reviewer reads the diff itself either way.
func (r *run) reviewDiffSize(ctx context.Context) DiffSize {
	if r.repo == nil || (r.review && len(r.reviewHeads) != 1) {
		return DiffSize{}
	}
	size, err := diffSize(ctx, r.repo, "origin/"+r.reviewPromptBase()+"...HEAD")
	if err != nil {
		r.d.Log.Warn("measuring the diff under review failed; the review prompt carries no size", "err", r.redact(err.Error()))
		return DiffSize{}
	}
	return size
}

// parseNumstat reads `git diff --numstat -z`'s output: one record per
// changed path, "<added>\t<deleted>\t<path>", where a binary file's counts
// are both "-". It returns the changes in git's own order and the totals
// the review prompt's huge-diff note is decided by.
func parseNumstat(out string) ([]reviewChange, DiffSize, error) {
	var changes []reviewChange
	var size DiffSize
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		added, rest, ok := strings.Cut(rec, "\t")
		if !ok {
			return nil, DiffSize{}, fmt.Errorf("reading the changed files: %q carries no counts", rec)
		}
		deleted, path, ok := strings.Cut(rest, "\t")
		if !ok {
			return nil, DiffSize{}, fmt.Errorf("reading the changed files: %q carries no path", rec)
		}
		changes = append(changes, reviewChange{path: path, binary: added == "-"})
		size.Files++
		if added == "-" {
			continue
		}
		a, err := strconv.Atoi(added)
		if err != nil {
			return nil, DiffSize{}, fmt.Errorf("reading the changed files: %q's added count is not a number", rec)
		}
		d, err := strconv.Atoi(deleted)
		if err != nil {
			return nil, DiffSize{}, fmt.Errorf("reading the changed files: %q's deleted count is not a number", rec)
		}
		size.Lines += a + d
	}
	return changes, size, nil
}
