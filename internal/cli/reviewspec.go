package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/redact"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// The four reasons a launcher writes beside ReviewSpecNone (R2, F35). They are
// a closed set of sentences rather than a code: the runner prints one as why
// the review judges the code alone, and the reader of a run's record reads the
// same words the launcher wrote.
const (
	launchSpecCodeOnly = "--code-only"
	launchSpecNoRun    = "no run found"
	launchSpecPick     = "pick of different tasks"
	launchSpecOwnStep  = "own-step recipe"
)

// launchSpecAsk is what a launched review asks the launcher's resolver beyond
// the pull requests themselves: --code-only and --spec FILE, the two answers
// that come before the bucket is read at all (R1, R11, D2934). The two are
// mutually exclusive, and the refusal for giving both is made where the flags
// are (reviewSpecFlagsRefusal), so at most one of them is set here.
type launchSpecAsk struct {
	PRs      []int
	CodeOnly bool
	SpecFile string
}

// resolveLaunchSpec is the spec a launched review is checked against (R1,
// D2934): the task the pull request was written for, the latest follow-up's
// own text beside it and the plan task it names, or a none with the reason
// there is none. fugaro review and fugaro run --recipe review-only both reach
// it through reviewSpec, so both review against the task by default and
// neither has a second rule (D2934).
//
// The pull request's root run is found through the runs bucket alone: the rows
// that record this pull request number in this repository, and the branch
// those rows recorded. The forge's head ref name is never read — anyone opening
// a pull request chooses it, so a branch named after another run would make
// the reviewer judge against that run's task (F22) — and resolvePR is never
// called either, whose refusals (a live run, pending owner decisions, a branch
// lock) belong to a follow-up launch and not to a review of someone's pull
// request (R1).
//
// Every text it returns is clipped to MaxReviewSpecBytes with a note and
// redacted with redact.Text, so what Validate accepts is what the launcher
// writes (R4, R13).
func resolveLaunchSpec(ctx context.Context, env *cloudEnv, slug string, ask launchSpecAsk, warn io.Writer) (*task.ReviewSpec, error) {
	if warn == nil {
		warn = io.Discard
	}
	if ask.CodeOnly {
		return launchSpecNone(launchSpecCodeOnly), nil
	}
	// --spec FILE is the spec by hand (spec §4): it overrides the originating
	// run's task, which is what makes it the way an orphan pull request is
	// reviewed against something, so the bucket is not read at all.
	if ask.SpecFile != "" {
		return launchSpecFromFile(ask.SpecFile)
	}
	if len(ask.PRs) == 0 {
		return launchSpecNone(launchSpecNoRun), nil
	}
	specs := make([]*task.ReviewSpec, len(ask.PRs))
	for i, pr := range ask.PRs {
		rs, err := launchSpecForPR(ctx, env, slug, pr, warn)
		if err != nil {
			return nil, err
		}
		specs[i] = rs
	}
	if len(specs) == 1 {
		return specs[0], nil
	}
	// A pick run reviews several pull requests in one job, and a job carries
	// one spec (R9): redundant attempts of one task give that one spec, and
	// anything else gives none with the reason. What the attempts are compared
	// on is specReadBy, because two attempts of one task are two runs and
	// always two run ids — the question is whether the same text reaches the
	// reviewer, and not whether one run wrote both pull requests.
	pick := specReadBy(specs[0])
	for _, rs := range specs[1:] {
		if specReadBy(rs) != pick {
			return launchSpecNone(launchSpecPick), nil
		}
	}
	return specs[0], nil
}

// specReadBy is what two pull requests of one pick run have to share to be
// redundant attempts of one task: the source, the reason a none carries, and
// the three texts a reviewer reads. Run is blanked rather than compared, since
// each attempt has a root run of its own and the id a spec carries is one of
// them (R9).
func specReadBy(rs *task.ReviewSpec) task.ReviewSpec {
	read := *rs
	read.Run = ""
	return read
}

// launchSpecNone is a spec of no spec, with the reason it is none. Reason is
// required beside this source (task.ReviewSpecProblem), and it is the value
// the review's against record carries back to the person.
func launchSpecNone(reason string) *task.ReviewSpec {
	return &task.ReviewSpec{Source: task.ReviewSpecNone, Reason: reason}
}

// launchSpecFromFile is the spec of `--spec FILE`: the file's own text, which
// overrides the task of the run that opened the pull request (spec §4). The
// text is redacted with redact.Text like every other text a launcher adds
// (R13), so no credential a hand-written spec happens to quote reaches the
// reviewer, and it is a file source, which carries no run id at all
// (task.ReviewSpecProblem).
//
// R4's cap is a REFUSAL here rather than a clip, the one place the two differ:
// a stored task cannot be trimmed and a person can trim a file, so a file over
// the cap is refused with its size and the person decides what to drop. An
// empty file is refused too — it is not a spec, and a review asked for against
// nothing is `--code-only`, which records why rather than reading as a brief
// that happens to say nothing.
func launchSpecFromFile(path string) (*task.ReviewSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, userErr("--spec %s: %v", oneLineCLI(path), err)
	}
	if len(data) > task.MaxReviewSpecBytes {
		return nil, userErr("--spec %s is %d bytes, and a review's spec carries at most %d: trim it to the part of it the pull request was written for", oneLineCLI(path), len(data), task.MaxReviewSpecBytes)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, userErr("--spec %s is empty, so there is nothing for the review to judge the diff against: write the task in it, or ask for a review of the code alone with --code-only", oneLineCLI(path))
	}
	return &task.ReviewSpec{Source: task.ReviewSpecFile, Text: redact.Text(text, nil)}, nil
}

// reviewSpecFlagsRefusal refuses the two combinations R11 rules out, and both
// are the flags' own shape rather than the launch's: the launcher answers them
// before it reads anything.
//
// --code-only and --spec ask a review for different things — no task at all,
// and this file's text as the task — so giving both is refused rather than one
// of them being dropped silently (spec §5). Each one also asks a review for
// something, and a recipe that is not mode: review is no review: the flag would
// write no field anywhere and the caller would read a launch that did not do
// what was asked.
func reviewSpecFlagsRefusal(codeOnly bool, specFile string, mode string) error {
	if !codeOnly && specFile == "" {
		return nil
	}
	if codeOnly && specFile != "" {
		return userErr("--code-only and --spec ask a review for different things: --code-only judges the code alone with no task, and --spec FILE is the task the diff is judged against. Give one of the two, or neither — a review against the task of the run that opened the pull request is what both fall back to")
	}
	if mode != recipe.ModeReview {
		flag := "--code-only"
		if specFile != "" {
			flag = "--spec"
		}
		return userErr("%s asks a review what to judge the pull request against, and this launch is not a review: it needs a mode: review recipe and --pr, as in fugaro run --recipe %s --pr N. For a review of a pull request, use fugaro review --pr N", flag, reviewOnlyRecipe)
	}
	return nil
}

// launchSpecForPR resolves one pull request's spec (R1): its rows in this
// repository, the branch they recorded, the root run that branch names, that
// run's stored task, and the latest follow-up's own text.
func launchSpecForPR(ctx context.Context, env *cloudEnv, slug string, pr int, warn io.Writer) (*task.ReviewSpec, error) {
	now := time.Now()
	// The window is the follow-up lookback, so a pull request whose runs are
	// older reviews code-only with the reason (R1), and noCache because
	// whether a pull request has a task is a launch and a cached row is what
	// a listing saw (resolvePR's own reason).
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{slug}, since: now.Add(-followUpLookback), pr: pr, warn: warn, noCache: true}, now)
	if err != nil {
		return nil, err
	}
	// A run is on the pull request when its task continues it, or when the
	// branch it recorded names a run: that is resolvePR's own reading, and the
	// review needs no more of a run than a branch to find the task the pull
	// request was written for.
	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return r.TaskPR != pr && !task.BranchRE.MatchString(r.Branch)
	})
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := startOf(b).Compare(startOf(a)); c != 0 {
			return c
		}
		return strings.Compare(b.RunID, a.RunID)
	})

	// The root, read newest first: the first row that names a run and holds a
	// task the pull request was written for is the answer, and two rows that
	// name different branches are no single pull request at all.
	//
	// A row that names the review run of this very pull request is left out:
	// such a run's branch is its own lock key, never a branch it pushed
	// (the runner sets it to fugaro/<the review's run id>), and its task
	// reviews this pull request rather than having written it. It is read
	// from the task and not from the row, because a row carries no recipe
	// mode: the name of a review recipe is a repository's to choose.
	//
	// Neither a disagreement nor a record that does not read back as this
	// pull request's own refuses the launch: they are the bucket saying it
	// cannot name one root run, which is what a none with `no run found`
	// answers, and one line on stderr says which rows or which record it was.
	// resolvePR refuses both, and rightly — a follow-up that wrote to the
	// wrong branch would spend a run on the wrong pull request — while a
	// review that judges the code alone costs nothing (R1, D2933).
	var (
		branch string
		rootID string
		root   *task.Spec
	)
	for _, r := range rows {
		id, ok := task.BranchRunID(r.Branch)
		if !ok {
			continue
		}
		spec, err := readTask(ctx, env, slug, id)
		switch {
		case ExitCode(err) == ExitRemoteError:
			return nil, err
		case err != nil:
			// A branch's root run with no readable task is no vote: it may be
			// a run that never started, and a review of someone's pull
			// request is not worth refusing over a record the bucket lost.
			continue
		}
		if len(spec.ReviewPRs) > 0 {
			continue
		}
		switch {
		case rootID == "":
			branch, rootID, root = r.Branch, id, spec
		case branch != r.Branch:
			fmt.Fprintf(warn, "warning: the runs on PR #%d name different branches (%s, %s), so the review judges the pull request's code alone\n", pr, oneLine(branch), oneLine(r.Branch))
			return launchSpecNone(launchSpecNoRun), nil
		}
	}
	if rootID == "" {
		return launchSpecNone(launchSpecNoRun), nil
	}
	// The branch's own root is checked with checkRoot's comparison, the one a
	// follow-up launch makes: a pull request whose rows name a run whose record
	// is on another branch is not the pull request that run opened, so no task
	// here is the one it was written for.
	if err := checkRoot(ctx, env, slug, rootID, branch, pr); err != nil {
		if ExitCode(err) == ExitRemoteError {
			return nil, err
		}
		fmt.Fprintf(warn, "warning: %s\n", oneLine(err.Error()))
		return launchSpecNone(launchSpecNoRun), nil
	}
	// A recipe whose first step is the runner's own (digest, commit_reports)
	// has a launcher label where a brief would be ("periodic run review"), so
	// reviewing against its text would raise noise and nothing else (R10,
	// F13): the review is code-only, and says why.
	if ownStepRecipe(root.Recipe) {
		return launchSpecNone(launchSpecOwnStep), nil
	}
	rs := &task.ReviewSpec{
		Source: task.ReviewSpecRun,
		Run:    rootID,
		Text:   clipReviewSpecText(redact.Text(root.Task, nil)),
	}
	// The plan task is a copy of the root task's, held to the rule the field
	// carries everywhere (R2). A root task that fails it is quoted without the
	// pointer rather than having the launch refused over it: the text still
	// says what the pull request was for, and PlanTaskProblem is the
	// launcher's own shape check on a field it did not write.
	if p := root.PlanTask; p != "" && task.PlanTaskProblem(p) == "" {
		rs.PlanTask = p
	}
	// The latest follow-up's text, which is what the pull request was last
	// asked to do (R1). The newest row of the pull request that is a
	// follow-up and not the root is it; a follow-up launched from a review's
	// findings contributes the person's own words alone, since the rest of its
	// text is a reviewer's report (F25, F30).
	//
	// The row is picked by the pull request its record names and not by its
	// task's own PR (Row.TaskPR), so a follow-up whose task.json the bucket
	// has lost is still the newest follow-up: it contributes nothing and the
	// line says so, rather than an older follow-up's text being quoted as what
	// the pull request was last asked to do.
	for _, r := range rows {
		if r.RunID == rootID || r.PR != pr {
			continue
		}
		fu, err := readTask(ctx, env, slug, r.RunID)
		switch {
		case ExitCode(err) == ExitRemoteError:
			return nil, err
		case err != nil:
			fmt.Fprintf(warn, "warning: run %s on PR #%d has no readable task, so the review judges the pull request without its follow-up text\n", task.ShortRun(r.RunID), pr)
			continue
		}
		if len(fu.ReviewPRs) > 0 {
			// A review of this pull request is on it, and its text is a
			// review's, not a brief (the root loop's own exclusion).
			continue
		}
		if t := followUpSpecText(fu); t != "" {
			rs.Followup = clipReviewSpecText(redact.Text(t, nil))
		}
		break
	}
	return rs, nil
}

// followUpSpecText is what a follow-up run contributes to a review's spec: its
// own text, and for one launched from a review's findings the person's own
// words alone (Spec.OwnText) — the rest of its text is the reviewer's report,
// and a reviewer's words are no brief (F25, F30).
func followUpSpecText(fu *task.Spec) string {
	if fu.FromFindings {
		return fu.OwnText
	}
	return fu.Task
}

// ownStepRecipe reports whether rcp is a recipe whose first step is one of the
// runner's own — the digest step or the commit_reports step (recipe.UsesDigest
// and recipe.UsesCommitReports) — which is the recipe a report run carries
// (R10, task.RecipeUsesDigest). A repository recipe is the project's copy of
// it, which the task embeds (D3303), so the text it carries is the whole of
// what is judged: a task naming a repository recipe — one an older release's
// launcher wrote — carries none, and answers no.
func ownStepRecipe(rcp *task.Recipe) bool {
	if rcp == nil {
		return false
	}
	return task.RecipeUsesDigest(rcp.YAML) || task.RecipeUsesCommitReports(rcp.YAML)
}

// clipReviewSpecText clips text to MaxReviewSpecBytes, cutting at a line
// boundary and ending with a note that names the size and the cap (R4). A
// stored task cannot be trimmed, so a long one is clipped rather than refused
// — the alternative is a review that never runs because the brief it was
// written for is verbose — and the note is what tells the reviewer that what
// it is reading is not the whole of it.
func clipReviewSpecText(text string) string {
	if len(text) <= task.MaxReviewSpecBytes {
		return text
	}
	note := fmt.Sprintf("\n\n[the text above is %d bytes; a review's spec carries at most %d, so the end of it was dropped]", len(text), task.MaxReviewSpecBytes)
	keep := text[:task.MaxReviewSpecBytes-len(note)]
	// Half a line is worse than none of it: the cut lands on the last line
	// boundary that fits, and on the cap itself when the text is one line
	// longer than the room there is for it.
	if i := strings.LastIndexByte(keep, '\n'); i >= 0 {
		keep = keep[:i+1]
	}
	return keep + note
}

// reviewSpecSkew drops a launched review's spec when the job's image runs a
// fugaro older than reviewSpecSince, and says so in one line (D2933): such a
// runner refuses the whole task at bootstrap (task.Parse decodes with
// DisallowUnknownFields), so carrying the fields would turn a launch that
// works today into a run that dies after it was paid for. Nothing is refused:
// a review whose image predates the check runs code-only, and the launch
// proceeds — the run is worth more than the check — so the one line names
// `fugaro update`, the whole upgrade that rebuilds the image.
//
// It drops the three fields together, because one release added all three and
// a runner older than that release reads none of them.
//
// The judge is baseBranchSkew's, for the same two shapes: a checkout: clone
// job runs the installation's base image itself, and a derived image runs the
// binary of the base its build record was built FROM. clone is nil when the
// launch read no workflow at all, and both judges are then asked and either
// one predating drops the fields. An image neither judge can date keeps them,
// exactly as baseBranchSkew keeps its field: what the image reads cannot be
// shown, and an installation built from source must not lose its review's
// spec because no record dates its image. This gate's only power is to omit
// fields, so a read that fails drops nothing.
//
// It returns the reason, once, for the two readers of it: the warning line it
// writes on stderr and the launch's own --json document (launchResult.
// ReviewSpecReason), so a caller reading either is told the same thing. The
// returned string is empty when nothing was dropped.
func reviewSpecSkew(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, clone *bool, warn io.Writer) string {
	if spec.ReviewSpec == nil && !spec.FromFindings {
		return ""
	}
	reason := ""
	drop := func() {
		spec.ReviewSpec, spec.FromFindings, spec.OwnText = nil, false, ""
		reason = fmt.Sprintf("the job image predates %s and cannot read a review's spec, so this review judges the code alone", reviewSpecSince)
		fmt.Fprintf(warn, "%s; fugaro update in the checkout moves it onto this release's base image\n", reason)
	}
	predates := func(rel string, known bool) bool { return known && imagePredates(rel, reviewSpecSince) }
	if clone == nil {
		rel, known := releaseOf(env.lc.BaseImage(config.BaseKind))
		if predates(rel, known) {
			drop()
			return reason
		}
		if rel, known = recordBaseRelease(ctx, env, slug, spec); predates(rel, known) {
			drop()
		}
		return reason
	}
	if *clone {
		if rel, known := releaseOf(env.lc.BaseImage(config.BaseKind)); predates(rel, known) {
			drop()
		}
		return reason
	}
	if rel, known := recordBaseRelease(ctx, env, slug, spec); predates(rel, known) {
		drop()
	}
	return reason
}
