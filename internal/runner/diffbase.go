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
// `origin/<diffBase>...HEAD` (reviewRev, unless a first run's range was
// narrowed to its own commits) is the range the review prompt names, the range
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

// baseUnreadable reports that the pull request's own base could not be used
// for this run's review diff: the provider returned none, or a value that is
// not a branch name, so the diff fell back to the run's ref (refBase) — for a
// follow-up usually the configuration's base, the old unbounded range. The
// review prompt says so (ReviewPrompt), so the reviewer restricts itself to
// the branch's own commits instead of trusting the range.
func (r *run) baseUnreadable() bool {
	switch {
	case r.review && len(r.reviewHeads) == 1:
		return !config.ValidBranchName(r.reviewHeads[0].Base)
	case r.follow != nil:
		return !config.ValidBranchName(r.follow.pr.Base)
	}
	return false
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

// narrowed is the commit a first run's range was narrowed to
// (narrowReviewRange), "" for every other run: the one place both readers —
// reviewRev and guardBounds — take the follow-up and review-run conditions
// from, so the review range and the guard can never disagree about them.
func (r *run) narrowed() string {
	if r.follow != nil || r.review {
		return ""
	}
	return r.narrowBase
}

// reviewRev is the revision the review stage's range starts from,
// `<reviewRev>...HEAD`: the review prompt, the input built for it, the size
// it is warned about and the follow-up's diff stat all read this one value,
// and guardBounds subtracts the same narrowed commit. It is
// origin/<reviewPromptBase>, except for a first run whose range was narrowed
// to its own commits (narrowReviewRange), where it is the commit before them.
//
// The narrowed commit is used only while it is still an ancestor of HEAD. A
// fix round that rebases the run's commits elsewhere (onto origin/main, say)
// leaves it behind, and `<it>...HEAD` would then start at its merge base
// with HEAD — the integration branch's fork point, which can be a larger
// range than the recorded one. That round reads the recorded range instead,
// which is what it would have read before narrowing existed; the prompt's
// scope line, built once before the first round, still names the commit,
// so that round's prompt says the range moved (narrowLeftNote), and the
// input's own header names the range it was built over.
func (r *run) reviewRev(ctx context.Context) string {
	if nb := r.narrowed(); nb != "" && r.repo != nil {
		head, err := r.repo.HeadSHA(ctx)
		if err == nil {
			var ok bool
			if ok, err = r.repo.IsAncestor(ctx, nb, head); err == nil && ok {
				return nb
			}
		}
		r.d.Log.Warn("the run's own range no longer leads to HEAD; this diff uses the recorded range",
			"narrowed", nb, "err", r.redactErr(err))
	}
	return "origin/" + r.reviewPromptBase()
}

// narrowReviewRange narrows a first run's review range to the commits the
// run made itself, when its recorded base says otherwise. It is called once,
// after implement and before the review stage's prompt is built, so every
// review round and the writeback read the same range.
//
// A run launched without --ref has main as its base (refBase) whatever its
// agent then does, and briefs that say `git fetch origin <integration> &&
// git checkout -b <b> origin/<integration>` cut the work from a branch
// nothing recorded: `origin/main...HEAD` is then the whole integration
// branch, and the reviewer was handed it every round — the cost this file's
// diffBase comment measures, back by another door. The run's own commits
// need no branch name to be found (gitops.OwnCommits): they are the ones no
// branch on origin holds but the run's own. The range then starts at the
// first one's parent.
//
// It only ever narrows: the range is kept when the run has no commits of its
// own yet, when the first one has no parent, when anything cannot be read,
// and whenever the narrowed range holds no fewer commits than the recorded
// one (a run on main, a run that merged another branch in: the two are the
// same range, or the merge's content is part of the change). A follow-up and
// a review run keep theirs: their base is the pull request's, read from the
// provider, and their prefetch has already fetched for it at bootstrap.
//
// A git older than 2.44 keeps the range too: it ignores GIT_NO_LAZY_FETCH,
// so reading which branches' commits the checkout holds would fetch every
// missing one in a blobless clone (gitops.NoLazyFetch). The base image's git
// is newer (images/base_docker_test.go); a repository's own image may not be.
func (r *run) narrowReviewRange(ctx context.Context) {
	if r.follow != nil || r.review || r.repo == nil || r.rec == nil {
		return
	}
	def := "origin/" + r.diffBase()
	if v, err := r.repo.GitVersion(ctx); err != nil || !gitops.NoLazyFetch(v) {
		r.d.Log.Warn("this git cannot read origin's branches without fetching them (GIT_NO_LAZY_FETCH needs git 2.44); the review range is the run's recorded base",
			"git", v, "range", r.redact(def)+"...HEAD", "err", r.redactErr(err))
		return
	}
	base, why := ownCommitsBase(ctx, r.repo, r.rec.Branch, def)
	if base == "" {
		r.d.Log.Info("the review range is the run's recorded base", "range", r.redact(def)+"...HEAD", "why", r.redact(why))
		return
	}
	r.narrowBase = base
	r.d.Log.Info("the review range is narrowed to the run's own commits", "range", base+"...HEAD",
		"instead_of", r.redact(def)+"...HEAD", "why", r.redact(why))
}

// ownCommitsBase is narrowReviewRange's decision: the parent of the run's
// first own commit when the range from it holds fewer commits than def's,
// else "". why says which, for the log.
func ownCommitsBase(ctx context.Context, repo *gitops.Repo, runBranch, def string) (base, why string) {
	own, err := repo.OwnCommits(ctx, runBranch)
	if err != nil {
		return "", "the run's own commits could not be read: " + err.Error()
	}
	if len(own) == 0 {
		return "", "the run has no commits of its own"
	}
	parent, err := repo.FirstParent(ctx, own[0])
	if err != nil {
		return "", "the parent of the run's first own commit could not be read: " + err.Error()
	}
	if parent == "" {
		return "", "the run's first own commit has no parent"
	}
	narrow, err := repo.CommitsAhead(ctx, parent)
	if err != nil {
		return "", "the narrowed range could not be counted: " + err.Error()
	}
	recorded, err := repo.CommitsAhead(ctx, def)
	if err != nil {
		return "", "the recorded range could not be counted: " + err.Error()
	}
	if narrow >= recorded {
		return "", fmt.Sprintf("the recorded range holds %d commits, the run's own range %d: nothing to narrow", recorded, narrow)
	}
	return parent, fmt.Sprintf("%d own commits; the recorded range holds %d commits, the run's own range %d", len(own), recorded, narrow)
}

// narrowSummary is the line the run report and the pull request's status
// section carry when the review range was narrowed (narrowReviewRange): a
// person reading the pull request sees the whole of its diff, the reviewer
// saw only the run's own commits, and without this line nothing says so. It
// is read at finalize, over the final HEAD, so the counts include every fix
// round's commits; "" when the range was not narrowed or cannot be counted.
// Its parts are a commit SHA, counts and a validated branch name, so it is
// bounded and needs no escaping.
func (r *run) narrowSummary(ctx context.Context) string {
	nb := r.narrowed()
	if nb == "" || r.repo == nil {
		return ""
	}
	short := "`" + shortSHA(nb) + "`"
	if rev := r.reviewRev(ctx); rev != nb {
		return fmt.Sprintf("review covered commits after %s until a fix round moved the branch off it; later rounds covered %s...HEAD", short, rev)
	}
	own, err := r.repo.CommitsAhead(ctx, nb)
	if err != nil {
		r.d.Log.Warn("counting the run's own commits for the report failed", "err", r.redact(err.Error()))
		return ""
	}
	line := fmt.Sprintf("review covered commits after %s (%s)", short, Plural(own, "commit"))
	all, err := r.repo.CommitsAhead(ctx, "origin/"+r.cfg.Git.BaseBranch)
	if err != nil {
		r.d.Log.Warn("counting the pull request's commits for the report failed", "err", r.redact(err.Error()))
		return line
	}
	if other := all - own; other > 0 {
		line += fmt.Sprintf("; this PR also carries %s from other branches", Plural(other, "commit"))
	}
	return line
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

// fetchDiffBases brings in origin/<base> for every base this run's diffs are
// measured against (diffRanges), with the run's own credentials, before the
// prefetch reads it.
//
// It is not a nicety: `origin/<base>` has to resolve in the checkout for the
// review prompt's range, the review input built over it and the reviewer's own
// `git diff origin/<base>...HEAD` to work at all. A clone job's clone is
// `--single-branch` of the remote's default branch (gitops.CloneBlobless), so
// a base that is not that branch is a ref the checkout simply does not have:
// a first run whose --ref is an integration branch would otherwise name
// origin/<that branch> in its review prompt while nothing here ever fetched
// it (CheckoutNewBranch brings spec.Ref in as FETCH_HEAD, not as a
// remote-tracking ref — this is the first-run half of the same hole the
// follow-up path had), and a review run's per-head base is neither spec.Ref
// nor the default branch at all. A missing ref there is not a quiet
// degradation: the input build fails, the reviewer is handed no diff and can
// still return a verdict.
//
// The base bootstrap already fetched is skipped: a first run's configuration
// base (runner.go fetches cfg.Git.BaseBranch before the lock, and the
// checkpoint scan needs origin/<base> from that same fetch) and the ref a
// follow-up or a review run read its configuration from (readBaseConfig's own
// fetch of origin/<ref>). Everything else is fetched once per run even when
// the checkout already holds the ref, the same as that first-run fetch does:
// a baked job's clone may hold a base that has since moved on.
//
// A failure is a warning, never a bootstrap error, the same shape as the
// prefetch below it: the ref may already be there (the fetch only refreshes
// it), the failure is not something a later stage can do anything about, and
// the stage that cannot read the range says so itself.
func (r *run) fetchDiffBases(ctx context.Context) {
	if r.repo == nil || r.spec == nil {
		return
	}
	seen := map[string]bool{r.alreadyFetchedBase(): true}
	for _, rng := range r.diffRanges() {
		if seen[rng.base] {
			continue
		}
		seen[rng.base] = true
		if err := r.repo.FetchBase(ctx, rng.base); err != nil {
			r.d.Log.Warn("fetching the branch the diff is measured against failed; the review stage's diff may not be readable",
				"base", rng.base, "err", r.redact(err.Error()))
		}
	}
}

// alreadyFetchedBase is the one base bootstrap has fetched by the time
// fetchDiffBases runs, so it is not asked for a second time: the
// configuration's base for a first run, and for a follow-up or a review run
// the ref whose configuration was read (readBaseConfig).
func (r *run) alreadyFetchedBase() string {
	if r.follow != nil || r.review {
		return r.spec.Ref
	}
	return r.cfg.Git.BaseBranch
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
	size, err := diffSize(ctx, r.repo, r.reviewRev(ctx)+"...HEAD")
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
