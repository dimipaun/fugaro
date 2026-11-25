package cli

import (
	"cmp"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// followUpLookback is how far back fugaro run --pr looks for a pull
// request's runs: the runs/ lifecycle age (design §3.3), so every run the
// bucket still holds is seen.
const followUpLookback = 90 * 24 * time.Hour

// prChain is what the runs bucket says about a Fugaro pull request.
type prChain struct {
	Branch   string
	Previous runview.Row   // the newest run on the PR (by started_at) that pushed
	Runs     []runview.Row // every run on the PR, newest first
	// Ref and Workflow are the previous run's: a follow-up keeps the base
	// branch and workflow of the runs before it.
	Ref, Workflow string
	// OAuthToken is the previous run's resolved named Claude account
	// (task.Spec.OAuthToken; "" is the repository's default): a follow-up
	// inherits it by default, never re-deriving it from fugaro.yaml or
	// anything else a first run would read — but an explicit --token on the
	// follow-up command overrides it (newFollowUpSpec, owner ruling
	// 2026-10-12, task 9b review; design docs/named-oauth-tokens/spec.md
	// §3 T6 and §6b).
	OAuthToken string
	// CompressionOff is the previous run's own override (task.Spec.
	// CompressionOff, fugaro run --no-compression): a follow-up keeps it, so
	// a chain that turned compression off for one suspicious run stays off
	// without the flag being repeated on every follow-up. The override is
	// one-directional, so a follow-up can turn compression off but never
	// back on: --no-compression sets it, and nothing clears it.
	CompressionOff bool
	Recipe         *task.Recipe // the previous run's: a follow-up keeps it unless --recipe names one
	// ChangeSet is the previous run's change set (task.Spec.ChangeSet), when
	// it was launched in one: a follow-up carries it, so the hold the set puts
	// on every member's pull request binds the follow-up too (D614, F15) — a
	// follow-up that dropped the block would flip ready the very pull request
	// its set keeps a draft. It is the block as the previous run's task holds
	// it, members and all: membership is fixed at the set's launch (D642), so
	// a follow-up joins the set it finds rather than naming one of its own.
	ChangeSet *task.ChangeSet
	// PrevReport is whether the previous run's task carried a manifest
	// (task.Spec.DigestRuns), and so whether the recipe this follow-up
	// inherits is a report recipe: a manifest is accepted under no other
	// (task.Validate). Only that task holds the fact: the copy a name
	// resolves to now may have been published since, so it need not be the
	// recipe the previous run ran.
	PrevReport bool
	// Title is the previous run's own job title (task.Spec.Title, "") when
	// it has none: a follow-up keeps its run's title, so a chain of
	// follow-ups stays one job in fugaro watch instead of being listed
	// under each follow-up's own instructions. --title overrides it.
	Title string
	// Chosen is the recipe the runner chose for the previous run (its record's),
	// when that run's task named none; nil when unknown, absent or default.
	Chosen *runstore.RecipeRecord
	// Rigor is the mode the previous run recorded (runstore.RigorRecord.Mode),
	// when it recorded a readable one: a follow-up keeps it and may only raise
	// it (T5.2, D6, F14). Empty — no record, a block no reader may trust, or a
	// readable block whose mode is hotfix, which is off the axis and so not a
	// mode a follow-up inherits (resolvePR) — imposes nothing, which is what
	// the record of an older run says.
	Rigor rigor.Rigor
	// Tier is the tier the previous run recorded (runstore.TierRecord.Tier),
	// when it recorded a readable one: a follow-up keeps it, as it keeps the
	// mode above, so a chain of follow-ups stays the one envelope its pull
	// request was dispatched under. Empty — no record, or a block no reader
	// may trust — imposes nothing, which is what the record of an older run
	// says. Unlike the mode there is no axis to raise along: a tier is an
	// expectation and not a guardrail (G6 keeps the cap out of it), so --tier
	// replaces the kept one rather than being held to it.
	Tier tier.Tier
}

// readRunRecord reads a run's result.json; tests replace it.
var readRunRecord = func(ctx context.Context, s *runstore.Store) (*runstore.Record, error) { return s.ReadRecord(ctx) }

// resolvePR finds pull request pr of slug through the runs bucket, and
// refuses a follow-up whose context can't be trusted. exclude is a run ID
// to leave out (a retry, or a repeated --run-id). ruled says this launch
// already carries the owner's ruling (--owner-ruled, or a stored task that
// carries one): without it, a follow-up whose previous run stopped waiting on
// owner decisions is refused rather than launched (OD1). Every refusal is a
// userErr naming the run and what to do; a failed read is a remote error.
// The runner checks the PR itself with the provider when it starts.
func resolvePR(ctx context.Context, env *cloudEnv, repo, slug string, pr int, exclude string, ruled bool, warn io.Writer) (*prChain, error) {
	if warn == nil {
		warn = io.Discard
	}
	now := time.Now()
	// noCache: whether this pull request allows a follow-up is a launch, and
	// a cached object is what a listing saw rather than what the bucket
	// holds. The window is one pull request's runs, so reading them from the
	// bucket costs little and decides on the bucket.
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{slug}, since: now.Add(-followUpLookback), pr: pr, warn: warn, noCache: true}, now)
	if err != nil {
		return nil, err
	}
	// A run is on the PR when its task continues it, or when its record
	// names it and it is on a Fugaro branch.
	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return r.RunID == exclude || (r.TaskPR != pr && !task.BranchRE.MatchString(r.Branch))
	})
	if len(rows) == 0 {
		return nil, userErr("PR #%d is not a Fugaro PR in this repository, or its runs are older than %d days; start a new run instead, or — for a run that opened nothing, so this PR is not its link — continue it with fugaro run --continues RUN-ID",
			pr, int(followUpLookback/(24*time.Hour)))
	}
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := startOf(b).Compare(startOf(a)); c != 0 {
			return c
		}
		return strings.Compare(b.RunID, a.RunID)
	})

	c := &prChain{Runs: rows}
	// The pause node of a run that ended paused, for the one word the rows
	// cannot carry: a cancellation is written on the credentials service's node
	// (D3197) and nowhere the runs bucket holds, so a chain whose pause was
	// cancelled is one this follow-up may continue, and a chain whose pause
	// stands is not (F64). It is read only for a row that says paused, so a
	// pull request whose runs never suspended opens no database at all.
	pauses := pauseFactsFor(ctx, env, func(format string, a ...any) { fmt.Fprintf(warn, format, a...) })
	var runs []runview.Row // the launched runs
	for _, r := range rows {
		switch r.Status {
		case runview.StatusError:
			return nil, userErr("run %s on PR #%d can't be read (%s), so the PR's state is unknown; see fugaro diagnose %s", task.ShortRun(oneLine(r.Run)), pr, oneLine(r.Reason), task.ShortRun(oneLine(r.Run)))
		case string(runstore.StatusPaused):
			f, paused := pauseInFlight(r, pauses)
			if !paused {
				// A cancelled pause is a chain that is over: the run is the
				// finished run its record says it is, and the reading below
				// treats it as one.
				break
			}
			// A suspended chain is in flight exactly as a running one is: the
			// orchestrator resumes it at its own instant, and a follow-up
			// launched now would be the second run of the chain between the
			// pause and its resume (F64). So the refusal is the running one's
			// shape, and the resume instant is the whole of what it adds --
			// pause.max_pause bounds it (six hours by default, R15), so the
			// wait it asks for is a bounded one. It names no cancel: a pause is
			// cancelled by the launcher through the service's own route (PR10,
			// D3197), not by anything this CLI takes today.
			until := ""
			if !f.resumeAt.IsZero() {
				until = " until " + f.resumeAt.Format(time.RFC3339)
			}
			return nil, userErr("run %s on PR #%d is paused%s; wait for it to be resumed (fugaro runs ls --pr %d --repo %s)", task.ShortRun(oneLine(r.Run)), pr, until, pr, repo)
		case runview.StatusLaunching, runview.StatusPending, string(runstore.StatusRunning):
			return nil, userErr("run %s on PR #%d is still %s; wait for it to finish (fugaro runs ls --pr %d --repo %s) or cancel it", task.ShortRun(oneLine(r.Run)), pr, r.Status, pr, repo)
		case runview.StatusUnlaunched:
			fmt.Fprintf(warn, "warning: run %s on PR #%d never launched; leaving it out\n", task.ShortRun(oneLine(r.Run)), pr)
			continue
		}
		runs = append(runs, r)
	}
	// Only launched runs vote on the branch: an unlaunched one is left out,
	// as its warning says.
	for _, r := range runs {
		switch {
		case r.Branch == "":
		case c.Branch == "":
			c.Branch = r.Branch
		case r.Branch != c.Branch:
			return nil, userErr("the runs on PR #%d disagree on its branch (%s, %s); follow it up by hand", pr, oneLine(c.Branch), oneLine(r.Branch))
		}
	}
	root, ok := task.BranchRunID(c.Branch)
	if !ok {
		return nil, userErr("PR #%d's branch %q is not a Fugaro branch; start a new run instead", pr, oneLine(c.Branch))
	}
	if err := checkRoot(ctx, env, slug, root, c.Branch, pr); err != nil {
		return nil, err
	}
	i := slices.IndexFunc(runs, func(r runview.Row) bool { return r.Pushed })
	if i < 0 {
		return nil, userErr("no run on PR #%d has pushed to %s, so there is nothing to follow up; start a new run instead", pr, c.Branch)
	}
	c.Previous = runs[i]
	if err := checkBranchLock(ctx, env, slug, c.Branch, now, warn); err != nil {
		return nil, err
	}
	prev, err := readTask(ctx, env, slug, c.Previous.RunID)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, userErr("run %s, the last to update PR #%d, has no task.json, so its base branch is unknown; start a new run instead", task.ShortRun(oneLine(c.Previous.Run)), pr)
	case err != nil:
		return nil, err
	}
	// The base the PR targets, which the previous run recorded; a record
	// from before it was kept falls back to that run's ref.
	c.Ref, c.Workflow = c.Previous.BaseBranch, prev.Workflow
	c.OAuthToken = prev.OAuthToken
	c.CompressionOff = prev.CompressionOff
	c.Recipe = prev.Recipe
	c.ChangeSet = prev.ChangeSet
	c.Title = chainTitle(prev)
	c.PrevReport = len(prev.DigestRuns) > 0
	// The previous run's record, read once for the two things only it knows:
	// the mode that run resolved, which a follow-up keeps and may only raise
	// (T5.2), and the recipe the runner chose for it, which a follow-up keeps
	// when its task names none. A record that cannot be read is a refusal
	// either way, and the mode is the half that makes it fail closed: a
	// follow-up that read no mode would let --mode lower one this launch could
	// not see. The block goes through RigorRecord.Bounded, the reader's own
	// bound: the record is an object the job's service account can write, so a
	// block no reader may trust is the same answer as no block at all.
	rec, err := readRunRecord(ctx, runstore.Open(env.bucket.Bucket, slug, c.Previous.RunID))
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		// A ruling answers the decisions a run left the owner, and this run's
		// record is gone, so there is nothing to hold it to: the launch would
		// reach the review stages as an answer to a question no reader can
		// name (below). Refused rather than trusted, the way an unreadable
		// record is.
		if ruled {
			return nil, userErr("run %s, the last to update PR #%d, has no record left, so whether it left the repository's owner any decisions cannot be known; --owner-ruled answers the decisions a run left waiting, so a launch carrying one here would reach the reviewers as an answer to nothing: start a new run instead", task.ShortRun(oneLine(c.Previous.Run)), pr)
		}
	case corruptObject(err):
		return nil, userErr("run %s's result.json is unreadable (%v), so the mode it ran under and the recipe the runner chose for it are unknown; nothing was launched", task.ShortRun(oneLine(c.Previous.Run)), err)
	case err != nil:
		return nil, remote(fmt.Errorf("reading the record of run %s to keep its mode and its recipe: %w; nothing was launched", task.ShortRun(oneLine(c.Previous.Run)), err))
	default:
		od := rec.OwnerDecisions.Bounded()
		// A ruling is accepted only where there is something to answer (OD1).
		// Without that, --owner-ruled is free text the launcher chooses, and
		// the runner hands it to every review level as the answer that settles
		// the findings it names (followup.RulingAddendum) — so a launcher, or
		// whatever drives it, could talk the deciding reviewer out of real
		// findings and turn the pull request ready. Held to the block, the flag
		// answers what a run left waiting, which is the one thing it is for.
		if od == nil && ruled {
			return nil, userErr("run %s, the last to update PR #%d, left the repository's owner no decisions to answer, so there is nothing here for --owner-ruled to answer: the flag is the owner's ruling on the findings a run left waiting for them, and the reviewers read it as the answer to those; launch again without it, or start a new follow-up",
				task.ShortRun(oneLine(c.Previous.Run)), pr)
		}
		// The previous run stopped on findings only the owner can settle
		// (OD1): a follow-up launched without their answer would read the same
		// diff, find the same finding and stop in the same place — which is
		// how a blind loop burns run after run on a question no code change
		// answers. Refused here, before anything is stored or launched, and
		// the refusal names each decision so the person who can answer them
		// has the question in front of them.
		if od != nil && !ruled {
			return nil, userErr("%s; answer them and launch again with --owner-ruled <text-or-file>, which is appended to this follow-up's brief verbatim",
				ownerWaitingRefusal(task.ShortRun(oneLine(c.Previous.Run)), pr, od))
		}
		// The runner chose this run's recipe (agent.recipe at the ref); a
		// follow-up keeps it, as the runner ignores agent.recipe on follow-ups.
		// The name is one the run really ran, so it is a recipe name or the
		// project's own copy of one (a copy name is what a run that ran the
		// repository's recipe records, R19): a name neither grammar accepts is
		// not a recipe this run could have run, and carrying it would have the
		// launch resolve a name no store can hold.
		if c.Recipe == nil && rec.Recipe != nil && rec.Recipe.Name != recipe.DefaultName &&
			(recipe.NameRE.MatchString(rec.Recipe.Name) || recipe.IsCopyName(rec.Recipe.Name)) {
			c.Chosen = rec.Recipe
		}
		// A bounded block that recorded hotfix imposes nothing either: hotfix
		// is off the axis (it ranks below every on-axis mode), so a follow-up
		// never inherits it. Hotfix is a mode an operator asks for on the run
		// that needs it, and a follow-up that inherited it would reach it
		// through a launch that asked for nothing — which is not the explicit
		// per-run choice the mode is (rigor.Rigor.PerRun). A follow-up that
		// wants it says so: --mode hotfix is accepted on one. The record still
		// says hotfix; that is what the run it describes resolved.
		if bounded := rec.Rigor.Bounded(); bounded != nil && bounded.LevelOf() != rigor.Hotfix {
			c.Rigor = bounded.LevelOf()
		}
		// The tier this run recorded, bounded the same way: the record is an
		// object the job's service account can write, so a block naming a tier
		// no runner resolved is the same answer as no block at all.
		if bounded := rec.Tier.Bounded(); bounded != nil {
			c.Tier = bounded.Tier
		}
	}
	recorded := c.Ref != ""
	if !recorded {
		c.Ref = prev.Ref
	}
	// A recorded base is a real git.base_branch, whatever it looks like;
	// only a fallback ref might be a commit ID.
	ref, ok := branchName(c.Ref, !recorded)
	if !ok {
		return nil, userErr("run %s, the last to update PR #%d, ran on ref %q, which is not a branch name (a tag or a commit?); a follow-up reads its configuration from its base branch, so start a new run instead",
			task.ShortRun(oneLine(c.Previous.Run)), pr, oneLine(c.Ref))
	}
	c.Ref = ref
	if c.Workflow == "" {
		c.Workflow = c.Previous.Workflow
	}
	return c, nil
}

// ownerWaitingRefusal is the sentence resolvePR refuses such a follow-up
// with: the run it read, the pull request, and one clause per pending decision
// — the file the reviewer named and its own sentence — so the person who can
// answer them reads the questions in the refusal itself, not in a transcript
// they have to open. Both fields are the reviewer's text about an untrusted
// diff, so both are flattened to one line before they reach a terminal.
func ownerWaitingRefusal(run string, pr int, od *runstore.OwnerDecisions) string {
	parts := make([]string, 0, len(od.Decisions))
	for _, d := range od.Decisions {
		s := oneLine(d.Summary)
		if d.File != "" {
			s = "`" + oneLine(d.File) + "`: " + s
		}
		parts = append(parts, s)
	}
	msg := fmt.Sprintf("run %s, the last to update PR #%d, stopped waiting on %s only you can settle: %s",
		oneLine(run), pr, runner.Plural(od.Total, "owner decision"), strings.Join(parts, "; "))
	if n := od.Total - len(od.Decisions); n > 0 {
		msg += fmt.Sprintf(" (and %s more: fugaro diagnose %s lists them)", runner.Plural(n, "decision"), oneLine(run))
	}
	return msg
}

// commitLikeRE is a ref that looks like a commit ID, full or abbreviated.
var commitLikeRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// branchName is ref as a branch name ("refs/heads/x" is x), and false when
// it is plainly something else: a tag or other ref, HEAD, a revision
// expression, a name git would refuse, or, when commitLike is set, a name
// shaped like a commit ID (a branch may be named so, so only a ref that
// isn't known to be a branch is refused for it). A tag without refs/tags/
// can't be told apart without the remote; the runner then refuses it.
func branchName(ref string, commitLike bool) (string, bool) {
	name := strings.TrimPrefix(ref, "refs/heads/")
	switch {
	case name == "", name == "HEAD", strings.HasPrefix(name, "refs/"), strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"),
		commitLike && commitLikeRE.MatchString(name), strings.ContainsAny(name, " ~^:?*[\\\t\n"), strings.Contains(name, ".."), strings.Contains(name, "@{"),
		strings.Contains(name, "//"), strings.HasSuffix(name, "/"), strings.HasSuffix(name, "."), strings.HasSuffix(name, ".lock"):
		return "", false
	}
	return name, true
}

// readTask reads run id's task.json: runstore.ErrNotFound when there is
// none, a userErr when it is unreadable, a remote error when the read fails.
func readTask(ctx context.Context, env *cloudEnv, slug, id string) (*task.Spec, error) {
	raw, err := runstore.Open(env.bucket.Bucket, slug, id).ReadFile(ctx, "task.json")
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, err
	case corruptObject(err):
		return nil, userErr("run %s/%s's task.json is unreadable: %v", slug, task.ShortRun(id), err)
	case err != nil:
		return nil, remote(err)
	}
	spec, err := task.Parse(raw)
	if err != nil {
		return nil, userErr("run %s/%s's task.json is unreadable: %v", slug, task.ShortRun(id), err)
	}
	return spec, nil
}

// startOf is when a row's run started: its record's started_at, else its
// run ID's time. A run ID can be chosen with --run-id, so its time is used
// only when nothing better is known.
func startOf(r runview.Row) time.Time {
	if r.StartedAt != nil && !r.StartedAt.IsZero() {
		return *r.StartedAt
	}
	return r.Created
}

// checkRoot refuses a branch whose root run (the run it names) still has a
// record that isn't on that branch: the branch would then not be the one
// the root run opened.
func checkRoot(ctx context.Context, env *cloudEnv, slug, root, branch string, pr int) error {
	rec, err := runstore.Open(env.bucket.Bucket, slug, root).ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil // expired: the runs that remain agree on the branch
	case corruptObject(err):
		// The run is named the way resolvePR names one (its ending, and the
		// ending alone) and the hint is the command that takes that name: a
		// `<slug>/<ending>` is a listing's cell, not a reference any command
		// resolves (locateRun's ParseRef wants the whole pair).
		return userErr("run %s, which PR #%d's branch names, has an unreadable result.json (%v); see fugaro diagnose %s",
			task.ShortRun(root), pr, err, task.ShortRun(root))
	case err != nil:
		return remote(err)
	}
	if rec.Branch != branch {
		return userErr("run %s, which PR #%d's branch %s names, records branch %q; the PR is not the one it opened, so follow it up by hand",
			task.ShortRun(root), pr, branch, oneLine(rec.Branch))
	}
	return nil
}

// checkBranchLock refuses a branch whose lock is live and whose holder is
// not provably over. An absent, expired or unparsable lock, or one naming
// no run, is fine: the runner takes it over (lock.Acquire). One too large
// to read is refused: the runner can't take it over either.
//
// A lock that still looks live by its expiry is taken over anyway, by
// deleting it outright (lock.Takeover) rather than merely launching past
// it, when lock.Stale says its holder is provably over: the backend
// independently confirms (executionTerminal) that the holder's own
// execution has ended, never anything read from runs/, which a launcher
// could forge (lock.go's own doc explains why). Deleting the lock, not
// just deciding to launch, is what lets the runner's own lock.Acquire
// succeed right after: Acquire trusts nothing but the lock object's own
// ExpiresAt, so with the lock gone it has nothing left to conflict with.
// A launcher's delete refused for lack of access (the 0.7.0 bucket
// hardening, §10) is reported with the command an operator or the
// sweeper runs instead. warn gets a note when the takeover happens.
func checkBranchLock(ctx context.Context, env *cloudEnv, slug, branch string, now time.Time, warn io.Writer) error {
	key := lock.Key(slug, branch)
	data, _, err := env.bucket.Read(ctx, key)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil
	case errors.Is(err, blobx.ErrTooLarge):
		// The runner can't read it either, and would fail at the lock
		// after a container start.
		return userErr("the lock object %s is larger than fugaro reads, so no run can take the branch; delete it, then launch again", key)
	case err != nil:
		return remote(err)
	}
	var h lock.Holder
	if json.Unmarshal(data, &h) != nil || h.RunID == "" || !now.Before(h.ExpiresAt) {
		return nil
	}
	if !lock.Stale(h, executionTerminal(ctx, env, h.Execution)) {
		return userErr("branch busy: run %s holds it until %s; wait for it, or cancel it", task.ShortRun(oneLine(h.RunID)), h.ExpiresAt.UTC().Format(time.RFC3339))
	}
	gen, err := lock.Takeover(ctx, env.bucket, key, h)
	switch {
	case err == nil:
		fmt.Fprintf(warn, "note: run %s's execution has ended (confirmed by the backend); cleared its branch lock\n", task.ShortRun(oneLine(h.RunID)))
		return nil
	case errors.Is(err, lock.ErrHolderChanged):
		return userErr("branch busy: run %s holds it until %s; wait for it, or cancel it", task.ShortRun(oneLine(h.RunID)), h.ExpiresAt.UTC().Format(time.RFC3339))
	case isAccessDenied(err):
		return userErr("%s", lockClearMessage(env, key, gen, h.RunID))
	default:
		return remote(err)
	}
}

// executionTerminal reports whether the backend confirms execution has
// ended: the same backend.Execution.State.Terminal() signal runview.Join
// uses to call a run infra_error after a kill that never finalized it
// (ls, diagnose). It double-checks the execution the backend answered
// about is, by backend.SameExecution, the one asked for: defense in depth
// before lock.Takeover deletes anything on this word. Anything else —
// empty, not found, a read error, not yet terminal — is not treated as
// proof.
func executionTerminal(ctx context.Context, env *cloudEnv, execution string) bool {
	if execution == "" {
		return false
	}
	e, err := env.be.Execution(ctx, execution)
	return err == nil && backend.SameExecution(e.Name, execution) && e.State.Terminal()
}

// lockClearMessage is what checkBranchLock and cancel print when a lock's
// holder is confirmed over but deleting its lock was refused for lack of
// access: under the 0.7.0 bucket hardening (design bucket-iam.md §3, §10)
// locks/ is no longer launcher-writable, so only an operator, or the
// sweeper, can clear it. gen must be the generation lock.Takeover itself
// verified matched the confirmed-stale holder (its return value, even on
// a failure): it is never read fresh here, because a second read, after
// Takeover already failed, could see a different run's lock by then — a
// launcher who ran the printed command against its current generation
// could delete a live run's lock.
//
// gen zero means Takeover never verified a generation at all (its own
// read failed before the delete was even attempted — reads are not what
// the 0.7.0 hardening denies, so this is a different, rarer failure).
// Printing an unpinned gcloud storage rm then would be just as unsafe as
// a fresh re-read: without a generation to match, the command deletes
// whatever is at that key when an operator finally runs it, which could
// by then be a different, live run's lock. So no command is printed;
// the message says the lock could not be read and asks for a look by
// hand instead.
func lockClearMessage(env *cloudEnv, key string, gen int64, runID string) string {
	if gen == 0 {
		object := key
		if name := env.lc.RunsBucketName(); name != "" {
			object = "gs://" + name + "/" + key
		}
		return fmt.Sprintf("the lock of run %s is held by an execution that ended, but it could not be read to name its generation safely; an operator must inspect %s by hand before clearing it",
			task.ShortRun(oneLine(runID)), object)
	}
	object := key
	if name := env.lc.RunsBucketName(); name != "" {
		object = fmt.Sprintf("gs://%s/%s#%d", name, key, gen)
	}
	return fmt.Sprintf("the lock of run %s is held by an execution that ended; an operator must clear it (or the sweeper does): gcloud storage rm %s",
		task.ShortRun(oneLine(runID)), object)
}

// prSpec builds the spec of fugaro run --pr: a stored follow-up that a
// repeated --run-id names (reused true), else a new one after resolvePR.
func prSpec(ctx context.Context, env *cloudEnv, o *runOptions, text string, total time.Duration, warn io.Writer) (slug string, spec *task.Spec, reused bool, err error) {
	// Checked first: the ID names objects below.
	if o.runID != "" && !task.ValidRunID(o.runID) {
		return "", nil, false, userErr("--run-id %q must look like 20260926-221530-a1b2", o.runID)
	}
	repo := o.repo
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return "", nil, false, err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", nil, false, userErr("--repo: %v", err)
	}
	if err = refuseSharedCheckout(ctx, repo); err != nil {
		return "", nil, false, err
	}
	if slug, err = env.repoSlug(repo, checkoutOf(ctx, repo)); err != nil {
		return "", nil, false, err
	}
	if o.runID != "" {
		if spec, err = storedFollowUp(ctx, env, slug, o, text, total); err != nil || spec != nil {
			return slug, spec, spec != nil, err
		}
	}
	c, err := resolvePR(ctx, env, repo, slug, o.pr[0], o.runID, o.ownerRuled != "", warn)
	if err != nil {
		return "", nil, false, err
	}
	spec, err = newFollowUpSpec(ctx, env, o, repo, c, text, total, warn)
	return slug, spec, false, err
}

// writePRIndex records a `--pr` launch's run in the pull-request index, so
// `runs ls --pr N` finds the runs of a pull request without scanning the
// repository (design docs/runs-ls-speed, D2962).
//
// The launcher writes this beside the runner's own write of the same run
// because the job's image may predate the index: a follow-up started on one
// writes nothing itself, and the run would stay invisible to `--pr N` until
// the backfill. Both writes are create-if-absent and per run, so whichever
// lands first stands and neither can replace the other.
//
// It is best effort and never a refusal, and it never waits: the index is a
// hint, and a reader checks every run it names against that run's own
// records before it shows a row (D2963), so a write that fails is a warning
// on the launch's stderr and the launch goes on.
func writePRIndex(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, now time.Time, warn io.Writer) {
	if spec.PR < 1 {
		return
	}
	e := runstore.PRIndexEntry{RunID: spec.RunID, Slug: slug, PR: spec.PR, Kind: runstore.PRIndexFollowed, At: now.UTC()}
	if err := runstore.PutPRIndex(ctx, env.bucket.Bucket, e); err != nil {
		// The run by its ending, as a person reads one (spec.md §1): the launch
		// has just named the repository, and the ending is what tells this run
		// from the next one.
		fmt.Fprintf(warn, "warning: no pull-request index entry was written for run %s in %s, so runs ls --pr %d may have to scan for it: %s\n",
			task.ShortRun(spec.RunID), oneLine(slug), spec.PR, oneLine(err.Error()))
	}
}

// repeatOverrides is the overrides a repeat of this launch must find stored,
// built the way the launch writes them rather than as an echo of the flags:
// createTask compares the whole task, so every field the launcher derives has
// to be derived here too.
//
// The four chain limits are the caller's own figures. A limit may only tighten
// the policy a launch resolves, so the merged value is the caller's wherever
// the launch was not refused (chainLimitGate), and the two no policy layer
// under them sets — the wall time and the review rounds — are the caller's by
// construction.
//
// The wall time is the one field two flags write: --max-time and
// --total-timeout compose as the smaller of the two (chainLimitGate), and the
// clock they composed with is the stored task's own — the smaller of the two
// is what that gate wrote as total_timeout, the field the backend's override
// reads. The repeat reads it back rather than resolving a clock of its own,
// because this call's environment is not the launch's: the checkout in hand
// now need not be the one that launch read, and there need not be one at all.
// So repeating `--total-timeout 6h --max-time 90m` finds the composed 1h30m0s
// and not the 6h it typed, and a launch that could resolve no clock — the
// chain's figure written alone, the composition left to the runner (L5) — is
// repeated as it stands rather than gaining a clock from wherever the repeat
// is run.
//
// A dimension this call names no flag for is taken from the stored task, which
// is the rule the title, the plan task and the mode already follow: a flag that
// is given must be the stored one, and a launch that gives none makes no claim.
// The chain half of a launch (docs/run-limit-carry-over L4) is what makes it
// necessary rather than merely tidy: a `--pr` follow-up writes the chain's
// remaining into each dimension its chain limits, whatever this call typed, so
// a repeat that echoed the flags alone would be refused as a different task
// against the very task its own launch wrote.
func repeatOverrides(have *task.Spec, o *runOptions, total time.Duration) task.Overrides {
	want := have.Overrides
	if o.limits.usd != nil {
		want.MaxUSD = o.limits.usd
	}
	if o.limits.tokens != nil {
		want.MaxTokens = o.limits.tokens
	}
	if o.limits.rounds != nil {
		want.MaxReviewRounds = o.limits.rounds
	}
	if o.limits.time > 0 {
		want.MaxTime = o.limits.time.String()
		// The clock the launch composed with, as it wrote it: min is
		// idempotent, so composing it once more with this call's figure
		// answers the same figure the launch stored.
		if clock := launchTimeoutOf(have); clock > 0 {
			want.TotalTimeout = min(clock, o.limits.time).String()
		}
		return want
	}
	if total > 0 {
		want.TotalTimeout = total.String()
	}
	return want
}

// storedFollowUp is the task a repeated --run-id already stored, when it is
// this call's follow-up (the same PR, text, batch and overrides): it is
// reused as it is, its previous_run and requested_by included. Any other
// stored task is refused; nil means there is none.
func storedFollowUp(ctx context.Context, env *cloudEnv, slug string, o *runOptions, text string, total time.Duration) (*task.Spec, error) {
	return storedRepeat(ctx, env, slug, o, o.pr[0], text, total)
}

// storedRepeat is the same rule for every launch shape that repeats against a
// stored task, with the pull request passed rather than read off this call: a
// --continues launch names no --pr and the task it repeats carries none either
// (pr 0). The figures such a task holds include what the chain resolved when
// it was written (chainInherit), so a repeat that re-read the chain instead of
// the stored task would derive another remaining every time a run behind it
// spends or a run in flight ages, and refuse the repeated command as a
// different task. The task is what the run reads; a repeat answers with it.
func storedRepeat(ctx context.Context, env *cloudEnv, slug string, o *runOptions, pr int, text string, total time.Duration) (*task.Spec, error) {
	have, err := readTask(ctx, env, slug, o.runID)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A --used-* figure is the one flag a repeat cannot be held to the stored
	// task by (D1048): the stored task holds the chain's remaining as the launch
	// that wrote it resolved it, and the charge that launch was given is inside
	// that figure rather than beside it (chainRemaining), so the same figure and
	// a different one are the same task here. Left there it would be dropped in
	// silence — the failure a fresh launch refuses for a dimension the chain does
	// not limit, where the figure is "refused rather than accepted and forgotten"
	// — and it is the worse half: the caller is told the run is already launched
	// while the figure they stated was never applied. Refused, and the message
	// names the two ways to state it: without the flags, or on a new run ID.
	if o.used.set() {
		return nil, userErr("run ID %s already holds a task, and a repeat cannot state what the chain has used: the figures it holds are the chain's remaining as its own launch resolved them, which a --used-* figure cannot be checked against. Repeat without --used-*, or state it on a run ID that has not launched", o.runID)
	}
	want := repeatOverrides(have, o, total)
	a, err1 := json.Marshal(have.Overrides)
	b, err2 := json.Marshal(want)
	if err := errors.Join(err1, err2); err != nil {
		return nil, err
	}
	haveRecipe := recipe.DefaultName
	if have.Recipe != nil {
		haveRecipe = have.Recipe.Name
	}
	// An explicit --title must be the stored one: otherwise a repeated
	// --run-id would report a launch whose title is not the one asked for.
	// No --title is no claim about it: the stored title (the follow-up's
	// own, or the one it inherited from its run) is kept as it is, which is
	// what makes the repeat idempotent. The plan task follows the same rule:
	// an explicit --plan-task must be the stored one, and none repeats the
	// stored task whatever it claims.
	//
	// The run's own rigor request (--mode, --path) is compared the same way,
	// and the case for it is stronger than the title's: the stored task is
	// what the job reads, so a raise this launch asked for that the list did
	// not check would be dropped from a spec reused as it is, while the
	// launch reported the run as already launched — a mode nobody resolved,
	// reported as success. The same shape as the title: a flag that is given
	// must be the stored one, and a launch that gives none makes no claim
	// (the stored request stands, exactly as the stored title does).
	//
	// The owner's ruling (--owner-ruled, OD1) is that shape too, and it is the
	// one a repeat most often omits: a launch that answers the review's
	// pending decisions again must carry the same ruling, and one that gives
	// none repeats the stored task — the ruling it holds stands — rather than
	// being refused as a different one.
	//
	// --token is a name a person writes, so it may name an alias (D2091), and
	// what the flag is held to is the account this launch would go out on: the
	// name resolved through the file's aliases and no other step (nameOrigin's
	// nameWritten; the allow-list is asked below, over the stored account). A
	// repeat of `--token ALIAS` whose alias stands for the account the stored
	// task mounts is the launch the id already holds — refused as a different
	// task it would answer a caller who asked for exactly what is there, and
	// the plugin's repeat is documented as idempotent.
	token, _ := env.lc.ResolveAccount(o.token)
	if have.PR != pr || have.Task != text || have.Batch != o.batch || (o.title != "" && have.Title != o.title) || string(a) != string(b) || (o.recipe != "" && o.recipe != haveRecipe) ||
		(o.token != "" && token != cmp.Or(have.OAuthToken, config.DefaultClaudeAccount)) ||
		(o.planTask != "" && have.PlanTask != o.planTask) ||
		(o.rigor != "" && string(have.Rigor) != o.rigor) ||
		(o.ownerRuled != "" && have.OwnerRuled != o.ownerRuled) ||
		(o.tier != "" && string(have.Tier) != o.tier) ||
		(len(o.paths) > 0 && !slices.Equal(have.Paths, o.paths)) {
		return nil, userErr("run ID %s already holds a different task", o.runID)
	}
	// have is stored bucket data nobody vouches for (review finding A): its
	// OAuthToken's shape was checked when it was first written (task.Parse),
	// but whether this repository may still use that account was never
	// re-checked here, the one place a repeated --run-id reuses it as is,
	// before any build-record read or the launch itself. It goes through the
	// same resolver a fresh launch's account does (claudeAccountFor) rather
	// than through the allow-list check alone, so this path is held to the
	// whole rule every other launch is: the allow-list, the auth clause, the
	// mix a config may not declare (config.CheckStageAccountMix) and the
	// budget mode that mix needs (config.CheckMixBudget).
	if _, err := claudeAccountFor(ctx, env, have.Repo, cmp.Or(have.OAuthToken, config.DefaultClaudeAccount), "", have.Ref, checkoutOf(ctx, have.Repo)(), nameRecorded); err != nil {
		return nil, err
	}
	return have, nil
}

// reuseStored answers a launch that names an --run-id with the task that ID
// already holds, when that task is this call's own launch (storedRepeat):
// reused true with the stored task, and the chain is not read again, because
// what the stored task holds is what the chain left the run when it was written
// (chainInherit) and a chain read now answers another figure as soon as a run
// behind it spends or a run in flight ages. A run ID that holds nothing is a
// launch, and pr is the pull request the stored task must carry — 0 for the
// launch shapes that name none (a review job, --continues).
func reuseStored(ctx context.Context, env *cloudEnv, slug string, o *runOptions, pr int, text string, total time.Duration, spec *task.Spec) (*task.Spec, bool, error) {
	if o.runID == "" {
		return spec, false, nil
	}
	have, err := storedRepeat(ctx, env, slug, o, pr, text, total)
	if err != nil {
		return nil, false, err
	}
	if have == nil {
		return spec, false, nil
	}
	// The pull requests a review job reviews are the one identity storedRepeat
	// cannot compare: a review task keeps them in review_prs and carries no pr
	// of its own, so the figure the pr argument is held to is 0 for every one of
	// them, and a repeat of `--pr 8 --recipe review-only` over a run ID holding
	// PR 7's review would be answered with that stored task — the review of a
	// pull request this call never named, reported as the launch it asked for.
	// The task that was written is the task a repeat answers with, so the list
	// has to be this call's own.
	if !slices.Equal(have.ReviewPRs, spec.ReviewPRs) {
		return nil, false, userErr("run ID %s already holds a different task", o.runID)
	}
	return have, true, nil
}

// chainTitle is the title a follow-up inherits from the run it continues: the
// run's own title, else the first line of its brief, which is what watch, ls and
// the console list that run by. It is made fit the way a stored title is
// (safetext.Field, task.MaxTitleRunes, task.TitleProblem); "" only when the run
// really carries nothing to name it by.
func chainTitle(prev *task.Spec) string {
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prev.TitleLine()), "# Task:"))
	title := safetext.Field(line)
	if r := []rune(title); len(r) > task.MaxTitleRunes {
		title = strings.TrimSpace(string(r[:task.MaxTitleRunes]))
	}
	if task.TitleProblem(title) != "" {
		return ""
	}
	return title
}

// newFollowUpSpec is the task of a new follow-up of chain c: its branch and
// PR, the previous run it continues, and that run's base branch and
// workflow; nothing else is inherited.
func newFollowUpSpec(ctx context.Context, env *cloudEnv, o *runOptions, repo string, c *prChain, text string, total time.Duration, warn io.Writer) (*task.Spec, error) {
	workflow := c.Workflow
	// The launch's own document before the reader below, as resolveAttemptPlan
	// and reviewSpec: the workflow this follow-up has to resolve when the run
	// it continues names none is the one the ref's file names, and a follow-up
	// whose working tree has no fugaro.yaml would otherwise be refused for a
	// question the document answers. The chain's ref is what the run is cut
	// from, so the read is made at it and not at the working tree.
	ctx = withLaunchDocument(ctx, repo, c.Ref)
	checkout := checkoutOf(ctx, repo)
	if workflow == "" {
		var err error
		if workflow, err = resolveWorkflow(env, repo, "", checkout); err != nil {
			return nil, err
		}
	}
	// Best effort: the local checkout's finalize_reserve, which may not be
	// the base branch's; the runner validates the override against the
	// config it reads from origin/<ref>.
	if total > 0 {
		if cfg := checkout(); cfg != nil {
			if w, ok := cfg.Workflows[workflow]; ok && total <= w.Timeouts.FinalizeReserve.Duration {
				return nil, userErr("--total-timeout %s must be longer than workflow %s's timeouts.finalize_reserve %s", total, workflow, w.Timeouts.FinalizeReserve.Duration)
			}
		}
	}
	me, err := env.lc.Me(ctx)
	if err != nil {
		return nil, userErr("%v", err)
	}
	runID := o.runID
	if runID == "" {
		if runID, err = task.NewRunID(time.Now(), crand.Reader); err != nil {
			return nil, err
		}
	}
	// Resolved before the recipe (review finding E, the same ordering bug
	// newSpec had): resolveRecipe/carryChosenRecipe below can read a project
	// or catalog recipe from the bucket, a network call a disallowed account
	// (inherited or overridden) should never trigger.
	//
	// A follow-up inherits its previous run's account by default, but an
	// EXPLICIT --token now overrides it (owner ruling, 2026-10-12, named
	// oauth tokens task 9b review): switching accounts after a usage limit
	// needs this, and it widens nothing a launcher could not already do with
	// a fresh run — the override is resolved exactly like claudeAccountFor
	// resolves a first run's (§6b), and goes through the identical resolution
	// immediately below (claudeAccountFor, holding the flag as its top rung):
	// the allow-list, the agent.auth rule and the mix rule, and (via
	// spec.OAuthToken below, the same field a first run sets) the
	// namedTokensSince version gate and the
	// job-variant-must-exist rule at launch. --token default is not
	// special-cased: like a first run's empty default, it simply makes
	// account the reserved name, which claudeAccountAllowed always allows.
	account := c.OAuthToken
	origin := nameRecorded
	if account == "" {
		account = config.DefaultClaudeAccount
	}
	if o.token != "" {
		account, origin = o.token, nameWritten
	}
	// c.OAuthToken (the inherited case) came from the previous run's
	// task.json, bucket data a launcher can write directly: its shape was
	// already checked when readTask parsed it (task.Parse validates), but
	// whether this repository may still use it
	// (claude_accounts.<name>.allow_data_to) is not — a name that was
	// allowed when the first run launched, or a forged record naming one
	// that never was, must not let a follow-up launch under it. An explicit
	// override (o.token, a launcher's own flag) gets the identical check,
	// never a lesser one. The check is the whole resolver's
	// (claudeAccountFor), so a follow-up launch is refused a mix its config
	// may not declare exactly as a fresh one is.
	//
	// The two names are not read the same way (nameOrigin): the inherited
	// account is the one its previous run resolved and wrote down, so it stands
	// as written and the alias block may not answer it (D2103) — a name the
	// block answers to is refused rather than followed, because a follow-up
	// keeps the account its previous run resolved (D2098) and the task written
	// below carries what this walk answers. Only --token, a name written now,
	// may name an alias, and it is the one way to move a follow-up on purpose.
	//
	// What the walk resolves is what the follow-up launches as, the inherited
	// name included: a mixed run starts on the job that mounts its account's
	// token (claudeAccountFor's mixedRunAccount), and a follow-up of one has
	// to land there too — its previous run's task may name no account at all
	// (one stored before the mix reached the launch, or launched when this
	// repository mixed nothing), and the name the run's review stages will be
	// served by is still the config's.
	resolved, alias, err := claudeAccountChoice(ctx, env, repo, account, "", c.Ref, checkout(), origin)
	if err != nil {
		return nil, err
	}
	// A follow-up names the pair only where this launch read an alias:
	// `--pr N --token ALIAS` takes the alias' current target (D2098), while the
	// account a follow-up inherits is one its previous run resolved, and the
	// record carries no alias to name (F4).
	o.account = launchAccount{Name: resolved, Alias: alias}
	// The note is about the account the follow-up goes out on, not about the
	// name the flag was written with: --token may name an alias (D2091), so a
	// flag whose alias stands for the account the previous run used switches
	// nothing (a note there would report a move that did not happen), and what
	// a real switch lands on is the account the alias stands for — no job
	// mounts an alias by its own name (F4).
	if o.token != "" && resolved != cmp.Or(c.OAuthToken, config.DefaultClaudeAccount) {
		fmt.Fprintf(warn, "note: --token %s switches this follow-up from %s's Claude account %s to %s\n",
			pluginwire.Printable(o.token), pluginwire.Printable(task.ShortRun(oneLineCLI(c.Previous.Run))),
			pluginwire.Printable(cmp.Or(c.OAuthToken, config.DefaultClaudeAccount)), pluginwire.Printable(resolved))
	}
	// Every arm but the inherited-YAML one resolves its name the launch's own
	// way (resolveLaunchRecipe): the repository's own copy in the project store
	// first, then the name itself, then the catalog. A follow-up and a first run
	// read the same store now, and neither reads the checkout's
	// .fugaro/recipes/ (D3237, R7): an inherited repository recipe is embedded
	// as the project's copy of it, which is what the release-A runner runs.
	provider := env.repoProvider(repo, checkout())
	rcp := c.Recipe
	if o.recipe != "" {
		rr, err := resolveLaunchRecipe(ctx, env, provider, repo, o.recipe, time.Now())
		if err != nil {
			return nil, err
		}
		// Refused in the name the person wrote, not in the copy it resolved to:
		// --recipe NAME is what they typed and what launches the run again.
		if err := refuseFollowUpReviewMode(rr.Recipe, o.recipe); err != nil {
			return nil, err
		}
		if err := refuseFollowUpArtifactMode(rr.Recipe, o.recipe); err != nil {
			return nil, err
		}
		if err := refuseFollowUpReportMode(rr.Recipe, o.recipe); err != nil {
			return nil, err
		}
		printRecipeNote(warn, rr, true)
		rcp = rr.taskRecipe()
	} else if rcp == nil && c.Chosen != nil {
		var err error
		if rcp, err = carryChosenRecipe(ctx, env, provider, repo, c, warn); err != nil {
			return nil, err
		}
	} else if rcp == nil {
		// The default fallback: a follow-up that inherits no recipe at all runs
		// the default (the runner never applies agent.recipe to one), and the
		// repository's own copy of it in the project store is that default when
		// it has one — the catalog's is left out of the task, as before.
		var err error
		if rcp, err = inheritDefaultRecipe(ctx, env, provider, repo); err != nil {
			return nil, err
		}
	} else if rcp != nil && rcp.YAML != "" {
		// An inherited recipe is parsed again: a bad one fails here, not in the cloud.
		inherited, err := parseRecipeAt([]byte(rcp.YAML), rcp.Name, "the recipe of the previous run ("+rcp.Name+")")
		if err != nil {
			return nil, err
		}
		if err := refuseFollowUpReviewMode(inherited, rcp.Name); err != nil {
			return nil, err
		}
		if err := refuseFollowUpArtifactMode(inherited, rcp.Name); err != nil {
			return nil, err
		}
		if err := refuseFollowUpReportMode(inherited, rcp.Name); err != nil {
			return nil, err
		}
	} else if rcp != nil {
		// An inherited repository recipe by name: task.Recipe carries no text
		// for one (taskRecipe, recipes_resolve.go), so there is nothing to
		// parse here — the name is resolved the launch's way instead and its
		// copy embedded, so that the task this follow-up writes names no
		// repository recipe either (R7).
		//
		// The previous run's own task still tells that its recipe was a report
		// recipe, when it was: only a report recipe's task carries a manifest
		// (task.Validate), and the copy the store holds now is not the recipe
		// that task ran.
		if c.PrevReport {
			return nil, refuseReportFollowUp(rcp.Name)
		}
		rr, err := followUpRecipeOfName(ctx, env, provider, repo, rcp.Name)
		if err != nil {
			return nil, err
		}
		printRecipeNote(warn, rr, true)
		rcp = rr.taskRecipe()
	}
	// The follow-up's title: the flag's when it is given, else the previous
	// run's own (c.Title), so a chain of follow-ups keeps naming itself by the
	// job it is, else the first line of the brief it carries (TitleLine). A
	// follow-up with none of the three is refused: every run has a title, a run
	// is never listed by "-", and a name the launcher made up or read off a code
	// host (a read that fails without a token, silently) is how sixteen untitled
	// runs went out in one batch.
	title := cmp.Or(o.title, c.Title)
	if title == "" && text == "" {
		return nil, userErr("nothing names this follow-up: the chain of PR #%d carries no title and this launch has no brief of its own, and every run has a title; pass --title \"<what the run does>\" (at most 100 characters)", o.pr[0])
	}
	// The follow-up's plan task is the flag's alone, never the run's it
	// continues: a follow-up is joined to the graph through its pull request
	// number (the run it follows holds the node), and a chain that kept
	// re-claiming a task would say the task is running again with every
	// follow-up. An explicit --plan-task claims one for this run itself.
	//
	// The mode this follow-up runs at (T5.2, D6, F14) is resolved here,
	// before the spec is built, so a request it refuses writes nothing.
	mode, err := followUpRigor(o, c)
	if err != nil {
		return nil, err
	}
	// The promote block, for the follow-up that raises an area: the shape
	// 12a's reading most often takes, since the pull request that pays an
	// item down is the one the run is on and it is what the item's `## Closed
	// by` names (D31). Resolved against this repository's own file, exactly as
	// a first run's is (resolvePromote), and on the mode this follow-up
	// resolved rather than the flag's: the target is the level the run really
	// works at, and the raise-only rule below has already raised it.
	var promote *task.Promote
	if o.promote != "" {
		if promote, err = resolvePromote(ctx, repo, o.promote, string(mode)); err != nil {
			return nil, err
		}
	}
	// The follow-up's tier is the flag's when it is given, else the tier the
	// run it continues recorded: the envelope its pull request was dispatched
	// under, which a follow-up of it keeps (resolvePR). No flag and no
	// recorded tier is no expectation at all.
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: c.Ref, BaseBranch: specBaseBranch(c.Ref), Workflow: workflow, Task: text,
		Title:          title,
		PlanTask:       o.planTask,
		CompressionOff: c.CompressionOff,
		Branch:         c.Branch, PR: o.pr[0], PreviousRun: c.Previous.RunID, RequestedBy: me, Batch: o.batch, Recipe: rcp,
		OwnerRuled: o.ownerRuled, ChangeSet: c.ChangeSet,
		Rigor: mode, Paths: promotePaths(promote, o.paths), Promote: promote, Tier: cmp.Or(tier.Tier(o.tier), c.Tier)}
	if total > 0 {
		spec.Overrides.TotalTimeout = total.String()
	}
	if resolved != config.DefaultClaudeAccount {
		spec.OAuthToken = resolved
	}
	if err := spec.Validate(); err != nil {
		return nil, userErr("%v", err)
	}
	return spec, nil
}

// followUpRigor is the mode a follow-up asks for (T5.2, D6, F14): the
// launcher's --mode raised to the mode the PR's previous run recorded, or that
// recorded mode alone when no flag is given, so a chain of follow-ups cannot
// walk a pull request's rigor back. A request the rule refuses is refused
// before the spec is built, so nothing is written (refuseFollowUpRigor).
//
// The mode written into the spec is one the run carried and not one it asked
// for (task.Spec.RigorCarried), which is the other half of "never lower":
// raise-only against the recorded mode is a rule about the pull request, and
// raise-only is also what the resolver holds the carried mode to (LF5 §3a), so
// a repository whose settings have risen since that run — or a diff of the
// follow-up's own that matches a stricter area — decides the level. The
// recorded mode is where the work starts, never a ceiling on what the run must
// be held to today.
//
// A record that carries no readable mode — an older run, or one that died
// before bootstrap — imposes nothing: Strictest skips the empty value, so the
// flag stands exactly as it does on a first run. A recorded hotfix is dropped
// before it gets here, for the reason resolvePR gives, so it imposes nothing
// the same way.
func followUpRigor(o *runOptions, c *prChain) (rigor.Rigor, error) {
	req := rigor.Rigor(o.rigor)
	if err := refuseFollowUpRigor(req, c.Rigor, task.ShortRun(oneLine(c.Previous.Run)), o.pr[0], "--mode", ""); err != nil {
		return "", err
	}
	if req.PerRun() {
		// The operator's own choice, kept whole: Strictest would drop it, as
		// it drops any mode ranking below the one it is compared against, and
		// the recorded mode of the PR is almost always an on-axis one.
		return req, nil
	}
	return rigor.Strictest(req, c.Rigor), nil
}

// refuseFollowUpRigor is the raise-only rule of a follow-up's mode (T5.2, D6,
// F14), as a refusal: req is the mode the launch asks for — the flag's on a
// fresh follow-up, the request a stored task already carries on a reused one —
// and recorded is the mode one run on the pull request recorded: the last to
// update it (c.Previous), which is the one record resolvePR reads and so the
// only one the rule judges. recordedBy names that run, because the refusal says
// which record it read rather than "the runs on PR #N", a claim over runs
// nobody looked at — a run on the PR that pushed nothing is skipped by
// resolvePR, and the mode it recorded is not part of this rule.
//
// One request is refused instead of resolved: one below the recorded mode,
// naming what the PR carries, since a follow-up is the comment-sourced surface
// (F13) and its own flag would otherwise be the way down.
//
// Hotfix is not refused and not resolved: it is the mode a request takes whole
// (rigor.Rigor.PerRun), so it is neither below nor above the recorded mode and
// the operator who asks for it on a follow-up means it exactly as they mean it
// on a first run — the owner's ruling of 2026-11-13 dropped the base branch's
// opt-in rather than building it (docs/rigor-command/implementation-plan.md
// R7). followUpRigor is what returns it, since Strictest would drop it.
//
// A zero req asks for nothing, and is never below a mode the PR recorded:
// resolving one is the caller's (Strictest, in followUpRigor), and only a
// stored task, reused as it is, has no resolution left to make — which is why
// recheckFollowUp asks this with the task's own request. subject names the
// request in the refusal's sentence, and cure what the launcher does instead.
func refuseFollowUpRigor(req, recorded rigor.Rigor, recordedBy string, pr int, subject, cure string) error {
	switch {
	case req.PerRun():
		return nil
	case req != "" && req.Rank() < recorded.Rank():
		return userErr("%s asks for %s, lower than %s, the mode run %s, the last to update PR #%d, recorded; a follow-up may raise a pull request's mode and never lower it%s",
			subject, req, recorded, recordedBy, pr, cure)
	}
	return nil
}

// recheckFollowUp runs the PR's checks again for a stored follow-up about
// to launch (--retry, or a repeated --run-id of one that never launched),
// with the run itself left out, and refuses it when another run has
// updated the PR since it was made: it would act on stale context.
func recheckFollowUp(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, warn io.Writer) error {
	c, err := resolvePR(ctx, env, spec.Repo, slug, spec.PR, spec.RunID, spec.OwnerRuled != "", warn)
	if err != nil {
		return err
	}
	if c.Branch != spec.Branch {
		return userErr("run %s continues %s, but PR #%d is on %s; start a new follow-up: fugaro run --pr %d", task.ShortRun(slug+"/"+spec.RunID), spec.Branch, spec.PR, c.Branch, spec.PR)
	}
	if c.Previous.RunID != spec.PreviousRun {
		return userErr("run %s follows %s, but run %s has updated PR #%d since; start a new follow-up: fugaro run --pr %d",
			task.ShortRun(slug+"/"+spec.RunID), task.ShortRun(oneLine(spec.PreviousRun)), task.ShortRun(oneLine(c.Previous.Run)), spec.PR, spec.PR)
	}
	// The raise-only rule again, on the request the stored task itself carries
	// (T5.2, D6, F14), after the two checks above because both read the chain
	// this one judges against. A stored task is what the job reads, and this is
	// the one launch shape whose mode nobody resolves a second time: a task
	// stored before the rule existed, or one whose previous run recorded a
	// higher mode since it was stored, would otherwise walk the pull request's
	// rigor back here. A task that asks for nothing is the same hole from the
	// other side — with no request the runner resolves from the configuration
	// alone, which is what the recorded mode is not — and it has no resolution
	// left to make, since the task is reused as it is. Both are refused, and
	// the cure is a new follow-up, which resolves the mode again.
	subject := fmt.Sprintf("the stored task of run %s", task.ShortRun(slug+"/"+spec.RunID))
	cure := fmt.Sprintf("; start a new follow-up: fugaro run --pr %d", spec.PR)
	if err := refuseFollowUpRigor(rigor.Rigor(spec.Rigor), c.Rigor, task.ShortRun(oneLine(c.Previous.Run)), spec.PR, subject, cure); err != nil {
		return err
	}
	if spec.Rigor == "" && c.Rigor != "" {
		return userErr("%s asks for no rigor mode, but run %s, the last to update PR #%d, recorded %s, which a follow-up keeps%s",
			subject, task.ShortRun(oneLine(c.Previous.Run)), spec.PR, c.Rigor, cure)
	}
	return nil
}

// followUpRecipeOfName resolves a name a follow-up inherits, the way every
// launch resolves one — the repository's own copy of it in the project store
// first, then the name, then the catalog — and runs the three refusals a
// follow-up cannot carry on the text that resolution found. It is one call so
// that the arms that inherit a repository recipe by name (a previous task's
// task.Recipe{Source: repo}, and the recipe the runner chose for the previous
// run) cannot drift: neither has text of its own to judge.
func followUpRecipeOfName(ctx context.Context, env *cloudEnv, provider, repo, name string) (*resolvedRecipe, error) {
	rr, err := resolveLaunchRecipe(ctx, env, provider, repo, name, time.Now())
	if err != nil {
		return nil, err
	}
	if err := refuseFollowUpReviewMode(rr.Recipe, name); err != nil {
		return nil, err
	}
	if err := refuseFollowUpArtifactMode(rr.Recipe, name); err != nil {
		return nil, err
	}
	if err := refuseFollowUpReportMode(rr.Recipe, name); err != nil {
		return nil, err
	}
	return rr, nil
}

// carryChosenRecipe is the recipe a follow-up keeps when the runner chose the
// previous run's: resolved now, the way every launch resolves a name (the
// repository's own copy of it in the project store first, then the name, then
// the catalog), and embedded. A repository recipe the previous run ran is the
// copy of it the project holds (D3237, R7) — the folder the runner read it from
// is not read any more, by this launcher or by the run — and a name nothing
// holds is refused rather than carried: a task that leaves its recipe to the
// folder or to a name no store has is refused at bootstrap (D3304), after the
// run was paid for. The refusals a follow-up cannot carry run on the resolved
// recipe's own text, and the notes say what is kept and whether it changed.
func carryChosenRecipe(ctx context.Context, env *cloudEnv, provider, repo string, c *prChain, warn io.Writer) (*task.Recipe, error) {
	ch := c.Chosen
	msg := func(cause error) error {
		return fmt.Errorf("the recipe run %s ran (%s) cannot be used: %w; pass --recipe NAME to choose another", pluginwire.Printable(task.ShortRun(oneLineCLI(c.Previous.Run))), ch.Name, cause)
	}
	wrap := func(err error) error {
		var ee *ExitError
		if errors.As(err, &ee) {
			return &ExitError{Code: ee.Code, Err: msg(ee.Err)}
		}
		return msg(err)
	}
	rr, err := followUpRecipeOfName(ctx, env, provider, repo, ch.Name)
	if err != nil {
		return nil, wrap(err)
	}
	printRecipeNote(warn, rr, true)
	rcp := rr.taskRecipe()
	fmt.Fprintf(warn, "note: keeping recipe %s, which the runner chose for run %s\n", pluginwire.Printable(ch.Name), pluginwire.Printable(task.ShortRun(oneLineCLI(c.Previous.Run))))
	if ch.SHA256 != "" && !sameRecipeAsRecorded(rr.Text, ch) {
		fmt.Fprintf(warn, "note: recipe %s has changed since run %s\n", pluginwire.Printable(ch.Name), pluginwire.Printable(task.ShortRun(oneLineCLI(c.Previous.Run))))
	}
	return rcp, nil
}

// sameRecipeAsRecorded is whether text is the recipe the record sums, the
// record's own name included.
//
// A record written before release A that named a repository recipe sums the
// repository's file, and the copy that file reaches a launch as is the file
// with its name: line rewritten (R14) — the record's name is the file's, the
// copy's is the name CopyName derives. Summing the two as they stand would
// report a change the migration alone made, so the copy is put back to the
// record's name and asked again.
func sameRecipeAsRecorded(text []byte, ch *runstore.RecipeRecord) bool {
	if recipe.Sum(text) == ch.SHA256 {
		return true
	}
	if recipe.IsCopyName(ch.Name) {
		return false
	}
	file, err := recipe.Rename(text, ch.Name)
	return err == nil && recipe.Sum(file) == ch.SHA256
}

// inheritDefaultRecipe is the default fallback of a follow-up that inherits no
// recipe at all: this repository's own copy of `default` in the project store
// when the project holds one, which is the run's default for this repository
// (R22), and nothing when it does not — the catalog default is the runner's to
// resolve and stays out of the task, exactly as a first run's does.
//
// The refusals a follow-up cannot carry judge what the store holds: a
// repository whose own default is mode: artifact or a report recipe would
// otherwise be run as one by a launch that asked for nothing.
func inheritDefaultRecipe(ctx context.Context, env *cloudEnv, provider, repo string) (*task.Recipe, error) {
	rr, err := followUpRecipeOfName(ctx, env, provider, repo, recipe.DefaultName)
	if err != nil {
		return nil, err
	}
	return rr.taskRecipe(), nil
}
