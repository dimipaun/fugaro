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
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
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
	// 2026-10-12, task 9b review; design docs/design/named-oauth-tokens.md
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
	// PrevReport is whether the previous run's task carried a manifest
	// (task.Spec.DigestRuns), and so whether the recipe this follow-up
	// inherits is a report recipe: a manifest is accepted under no other
	// (task.Validate). It is the one fact about that recipe the launcher
	// holds when it cannot read the recipe's own file — a repository recipe
	// by name, read at the run's base, is not in a launcher with no
	// checkout of that repository (refuseFollowUpRepoRecipe).
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
	// mode a follow-up keeps (D12, resolvePR) — imposes nothing, which is what
	// the record of an older run says.
	Rigor rigor.Mode
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
// to leave out (a retry, or a repeated --run-id). Every refusal is a
// userErr naming the run and what to do; a failed read is a remote error.
// The runner checks the PR itself with the provider when it starts.
func resolvePR(ctx context.Context, env *cloudEnv, repo, slug string, pr int, exclude string, warn io.Writer) (*prChain, error) {
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
		return nil, userErr("PR #%d is not a Fugaro PR in this repository, or its runs are older than %d days; start a new run instead",
			pr, int(followUpLookback/(24*time.Hour)))
	}
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := startOf(b).Compare(startOf(a)); c != 0 {
			return c
		}
		return strings.Compare(b.RunID, a.RunID)
	})

	c := &prChain{Runs: rows}
	var runs []runview.Row // the launched runs
	for _, r := range rows {
		switch r.Status {
		case runview.StatusError:
			return nil, userErr("run %s on PR #%d can't be read (%s), so the PR's state is unknown; see fugaro diagnose %s", r.Run, pr, oneLine(r.Reason), r.Run)
		case runview.StatusLaunching, runview.StatusPending, string(runstore.StatusRunning):
			return nil, userErr("run %s on PR #%d is still %s; wait for it to finish (fugaro runs ls --pr %d --repo %s) or cancel it", r.Run, pr, r.Status, pr, repo)
		case runview.StatusUnlaunched:
			fmt.Fprintf(warn, "warning: run %s on PR #%d never launched; leaving it out\n", oneLine(r.Run), pr)
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
		return nil, userErr("run %s, the last to update PR #%d, has no task.json, so its base branch is unknown; start a new run instead", c.Previous.Run, pr)
	case err != nil:
		return nil, err
	}
	// The base the PR targets, which the previous run recorded; a record
	// from before it was kept falls back to that run's ref.
	c.Ref, c.Workflow = c.Previous.BaseBranch, prev.Workflow
	c.OAuthToken = prev.OAuthToken
	c.CompressionOff = prev.CompressionOff
	c.Recipe = prev.Recipe
	c.Title = prev.Title
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
	case corruptObject(err):
		return nil, userErr("run %s's result.json is unreadable (%v), so the mode it ran under and the recipe the runner chose for it are unknown; nothing was launched", c.Previous.Run, err)
	case err != nil:
		return nil, remote(fmt.Errorf("reading the record of run %s to keep its mode and its recipe: %w; nothing was launched", c.Previous.Run, err))
	default:
		// The runner chose this run's recipe (agent.recipe at the ref); a
		// follow-up keeps it, as the runner ignores agent.recipe on follow-ups.
		if c.Recipe == nil && rec.Recipe != nil && rec.Recipe.Name != recipe.DefaultName && recipe.NameRE.MatchString(rec.Recipe.Name) {
			c.Chosen = rec.Recipe
		}
		// A bounded block that recorded hotfix imposes nothing either: hotfix
		// is off the axis (it ranks below every on-axis mode), so a follow-up
		// never keeps it. It is D12's own mode — reached by an operator's run
		// under the base branch's opt-in, and refused outright as a request —
		// and a follow-up that inherited it would be a second route to it, one
		// no configuration asked for and one a stored task could then be
		// refused for on --retry. The record still says hotfix; that is what
		// the run it describes resolved.
		if bounded := rec.Rigor.Bounded(); bounded != nil && bounded.Mode != rigor.Hotfix {
			c.Rigor = bounded.Mode
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
			c.Previous.Run, pr, oneLine(c.Ref))
	}
	c.Ref = ref
	if c.Workflow == "" {
		c.Workflow = c.Previous.Workflow
	}
	return c, nil
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
		return nil, userErr("run %s/%s's task.json is unreadable: %v", slug, id, err)
	case err != nil:
		return nil, remote(err)
	}
	spec, err := task.Parse(raw)
	if err != nil {
		return nil, userErr("run %s/%s's task.json is unreadable: %v", slug, id, err)
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
		return userErr("run %s/%s, which PR #%d's branch names, has an unreadable result.json (%v); see fugaro diagnose %s/%s", slug, root, pr, err, slug, root)
	case err != nil:
		return remote(err)
	}
	if rec.Branch != branch {
		return userErr("run %s/%s, which PR #%d's branch %s names, records branch %q; the PR is not the one it opened, so follow it up by hand",
			slug, root, pr, branch, oneLine(rec.Branch))
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
		return userErr("branch busy: run %s holds it until %s; wait for it, or cancel it", oneLine(h.RunID), h.ExpiresAt.UTC().Format(time.RFC3339))
	}
	gen, err := lock.Takeover(ctx, env.bucket, key, h)
	switch {
	case err == nil:
		fmt.Fprintf(warn, "note: run %s's execution has ended (confirmed by the backend); cleared its branch lock\n", oneLine(h.RunID))
		return nil
	case errors.Is(err, lock.ErrHolderChanged):
		return userErr("branch busy: run %s holds it until %s; wait for it, or cancel it", oneLine(h.RunID), h.ExpiresAt.UTC().Format(time.RFC3339))
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
			oneLine(runID), object)
	}
	object := key
	if name := env.lc.RunsBucketName(); name != "" {
		object = fmt.Sprintf("gs://%s/%s#%d", name, key, gen)
	}
	return fmt.Sprintf("the lock of run %s is held by an execution that ended; an operator must clear it (or the sweeper does): gcloud storage rm %s",
		oneLine(runID), object)
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
	c, err := resolvePR(ctx, env, repo, slug, o.pr[0], o.runID, warn)
	if err != nil {
		return "", nil, false, err
	}
	spec, err = newFollowUpSpec(ctx, env, o, repo, c, text, total, warn)
	return slug, spec, false, err
}

// storedFollowUp is the task a repeated --run-id already stored, when it is
// this call's follow-up (the same PR, text, batch and overrides): it is
// reused as it is, its previous_run and requested_by included. Any other
// stored task is refused; nil means there is none.
func storedFollowUp(ctx context.Context, env *cloudEnv, slug string, o *runOptions, text string, total time.Duration) (*task.Spec, error) {
	have, err := readTask(ctx, env, slug, o.runID)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var want task.Overrides
	if total > 0 {
		want.TotalTimeout = total.String()
	}
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
	if have.PR != o.pr[0] || have.Task != text || have.Batch != o.batch || (o.title != "" && have.Title != o.title) || string(a) != string(b) || (o.recipe != "" && o.recipe != haveRecipe) ||
		(o.token != "" && o.token != cmp.Or(have.OAuthToken, config.DefaultClaudeAccount)) ||
		(o.planTask != "" && have.PlanTask != o.planTask) ||
		(o.rigor != "" && string(have.Rigor) != o.rigor) ||
		(o.tier != "" && string(have.Tier) != o.tier) ||
		(len(o.paths) > 0 && !slices.Equal(have.Paths, o.paths)) {
		return nil, userErr("run ID %s already holds a different task", o.runID)
	}
	// have is stored bucket data nobody vouches for (review finding A): its
	// OAuthToken's shape was checked when it was first written (task.Parse),
	// but whether this repository may still use that account was never
	// re-checked here, the one place a repeated --run-id reuses it as is,
	// before any build-record read or the launch itself.
	if err := claudeAccountAllowed(env, have.Repo, cmp.Or(have.OAuthToken, config.DefaultClaudeAccount), checkoutAgentAuth(checkoutOf(ctx, have.Repo)())); err != nil {
		return nil, err
	}
	return have, nil
}

// newFollowUpSpec is the task of a new follow-up of chain c: its branch and
// PR, the previous run it continues, and that run's base branch and
// workflow; nothing else is inherited.
func newFollowUpSpec(ctx context.Context, env *cloudEnv, o *runOptions, repo string, c *prChain, text string, total time.Duration, warn io.Writer) (*task.Spec, error) {
	workflow := c.Workflow
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
	// resolves a first run's (§6b), and goes through the identical
	// claudeAccountAllowed gate immediately below: the allow-list, the
	// agent.auth oauth rule, and (via spec.OAuthToken below, the same field
	// a first run sets) the namedTokensSince version gate and the
	// job-variant-must-exist rule at launch. --token default is not
	// special-cased: like a first run's empty default, it simply makes
	// account the reserved name, which claudeAccountAllowed always allows.
	account := c.OAuthToken
	if account == "" {
		account = config.DefaultClaudeAccount
	}
	if o.token != "" {
		account = o.token
	}
	// c.OAuthToken (the inherited case) came from the previous run's
	// task.json, bucket data a launcher can write directly: its shape was
	// already checked when readTask parsed it (task.Parse validates), but
	// whether this repository may still use it
	// (claude_accounts.<name>.allow_data_to) is not — a name that was
	// allowed when the first run launched, or a forged record naming one
	// that never was, must not let a follow-up launch under it. An explicit
	// override (o.token, a launcher's own flag) gets the identical check,
	// never a lesser one.
	if err := claudeAccountAllowed(env, repo, account, checkoutAgentAuth(checkout())); err != nil {
		return nil, err
	}
	if o.token != "" && account != cmp.Or(c.OAuthToken, config.DefaultClaudeAccount) {
		fmt.Fprintf(warn, "note: --token %s switches this follow-up from %s's Claude account %s to %s\n",
			pluginwire.Printable(o.token), pluginwire.Printable(oneLineCLI(c.Previous.Run)),
			pluginwire.Printable(cmp.Or(c.OAuthToken, config.DefaultClaudeAccount)), pluginwire.Printable(account))
	}
	rcp := c.Recipe
	if o.recipe != "" {
		rr, err := resolveRecipe(ctx, env, checkoutRoot(ctx, repo), o.recipe, time.Now(), false)
		if err != nil {
			return nil, err
		}
		if err := refuseFollowUpReviewMode(rr.Recipe, rr.Name); err != nil {
			return nil, err
		}
		if err := refuseFollowUpArtifactMode(rr.Recipe, rr.Name); err != nil {
			return nil, err
		}
		if err := refuseFollowUpReportMode(rr.Recipe, rr.Name); err != nil {
			return nil, err
		}
		printRecipeNote(warn, rr, true)
		rcp = rr.taskRecipe()
	} else if rcp == nil && c.Chosen != nil {
		var err error
		if rcp, err = carryChosenRecipe(ctx, env, repo, c, warn); err != nil {
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
		// An inherited repository recipe by name: task.Recipe carries no
		// text for one (taskRecipe, recipes_resolve.go), so there is
		// nothing to parse here. This checkout's copy is read when there
		// is one; otherwise the runner refuses the file it reads at this
		// task's base.
		//
		// A launcher with no copy of that file can still tell that the
		// previous run's recipe was a report recipe, from the previous run's
		// own task: the recipe this follow-up inherits is the one that task
		// ran, and only a report recipe's task carries a manifest
		// (task.Validate). Refused from the manifest, here, because the file
		// this checkout does not have is the one the agent would have run —
		// the runner's own backstop refuses the same task after it is paid
		// for, at its base.
		if c.PrevReport {
			return nil, refuseReportFollowUp(rcp.Name)
		}
		if err := refuseFollowUpRepoRecipe(ctx, repo, rcp.Name); err != nil {
			return nil, err
		}
	}
	// The follow-up's title: the flag's when it is given, else the previous
	// run's own (c.Title), so a chain of follow-ups keeps naming itself by
	// the job it is. A chain whose first run carried no title has none to
	// inherit, and writes no title at all — the follow-up is then listed by
	// its own instructions' first line, exactly as before the flag existed.
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
	// The follow-up's tier is the flag's when it is given, else the tier the
	// run it continues recorded: the envelope its pull request was dispatched
	// under, which a follow-up of it keeps (resolvePR). No flag and no
	// recorded tier is no expectation at all.
	spec := &task.Spec{Version: 1, RunID: runID, Repo: repo, Ref: c.Ref, BaseBranch: specBaseBranch(c.Ref), Workflow: workflow, Task: text,
		Title:          cmp.Or(o.title, c.Title),
		PlanTask:       o.planTask,
		CompressionOff: c.CompressionOff,
		Branch:         c.Branch, PR: o.pr[0], PreviousRun: c.Previous.RunID, RequestedBy: me, Batch: o.batch, Recipe: rcp,
		Rigor: mode, Paths: o.paths, Tier: cmp.Or(tier.Tier(o.tier), c.Tier)}
	if total > 0 {
		spec.Overrides.TotalTimeout = total.String()
	}
	if account != config.DefaultClaudeAccount {
		spec.OAuthToken = account
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
// A record that carries no readable mode — an older run, or one that died
// before bootstrap — imposes nothing: Strictest skips the empty value, so the
// flag stands exactly as it does on a first run. A recorded hotfix is dropped
// before it gets here, for the reason resolvePR gives, so it imposes nothing
// the same way.
func followUpRigor(o *runOptions, c *prChain) (rigor.Mode, error) {
	req := rigor.Mode(o.rigor)
	if err := refuseFollowUpRigor(req, c.Rigor, c.Previous.Run, o.pr[0], "--mode", ""); err != nil {
		return "", err
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
// Two requests are refused instead of resolved. One below the recorded mode,
// naming what the PR carries: a follow-up is the comment-sourced surface (F13),
// so its own flag would otherwise be the way down. And hotfix, which is off the
// axis and ranks below every on-axis mode, and which an operator reaches with
// their own run under the base branch's opt-in, never through --pr (D12).
//
// A zero req asks for nothing, and is never below a mode the PR recorded:
// resolving one is the caller's (Strictest, in followUpRigor), and only a
// stored task, reused as it is, has no resolution left to make — which is why
// recheckFollowUp asks this with the task's own request. subject names the
// request in the refusal's sentence, and cure what the launcher does instead.
func refuseFollowUpRigor(req, recorded rigor.Mode, recordedBy string, pr int, subject, cure string) error {
	switch {
	case req == rigor.Hotfix:
		return userErr("%s asks for hotfix, which is an operator's own run and not a follow-up's: it is reached by fugaro run --mode hotfix under the base branch's opt-in (D12), never through --pr %d%s",
			subject, pr, cure)
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
	c, err := resolvePR(ctx, env, spec.Repo, slug, spec.PR, spec.RunID, warn)
	if err != nil {
		return err
	}
	if c.Branch != spec.Branch {
		return userErr("run %s/%s continues %s, but PR #%d is on %s; start a new follow-up: fugaro run --pr %d", slug, spec.RunID, spec.Branch, spec.PR, c.Branch, spec.PR)
	}
	if c.Previous.RunID != spec.PreviousRun {
		return userErr("run %s/%s follows %s, but run %s has updated PR #%d since; start a new follow-up: fugaro run --pr %d",
			slug, spec.RunID, spec.PreviousRun, c.Previous.Run, spec.PR, spec.PR)
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
	subject := fmt.Sprintf("the stored task of run %s/%s", slug, spec.RunID)
	cure := fmt.Sprintf("; start a new follow-up: fugaro run --pr %d", spec.PR)
	if err := refuseFollowUpRigor(rigor.Mode(spec.Rigor), c.Rigor, c.Previous.Run, spec.PR, subject, cure); err != nil {
		return err
	}
	if spec.Rigor == "" && c.Rigor != "" {
		return userErr("%s asks for no rigor mode, but run %s, the last to update PR #%d, recorded %s, which a follow-up keeps%s",
			subject, c.Previous.Run, spec.PR, c.Rigor, cure)
	}
	return nil
}

// carryChosenRecipe is the recipe a follow-up keeps when the runner chose the
// previous run's: a repository recipe by name (the runner reads it at the
// base), any other resolved now and embedded. A repository recipe is refused
// for the mismatches a follow-up cannot carry (refuseFollowUpRepoRecipe),
// from this checkout's copy of it; the runner refuses the file it reads at
// the base when there is none here. A note says what is kept, and that the
// text changed since when it did.
func carryChosenRecipe(ctx context.Context, env *cloudEnv, repo string, c *prChain, warn io.Writer) (*task.Recipe, error) {
	ch := c.Chosen
	var rcp *task.Recipe
	changed := false
	if ch.Source == string(recipe.SourceRepo) {
		if err := refuseFollowUpRepoRecipe(ctx, repo, ch.Name); err != nil {
			return nil, err
		}
		rcp = &task.Recipe{Name: ch.Name, Source: ch.Source}
	} else {
		msg := func(cause error) error {
			return fmt.Errorf("the recipe run %s ran (%s) cannot be used: %w; pass --recipe NAME to choose another", pluginwire.Printable(oneLineCLI(c.Previous.Run)), ch.Name, cause)
		}
		wrap := func(err error) error {
			var ee *ExitError
			if errors.As(err, &ee) {
				return &ExitError{Code: ee.Code, Err: msg(ee.Err)}
			}
			return msg(err)
		}
		rr, err := resolveRecipe(ctx, env, checkoutRoot(ctx, repo), ch.Name, time.Now(), false)
		if err != nil {
			return nil, wrap(err)
		}
		if err := refuseFollowUpReviewMode(rr.Recipe, rr.Name); err != nil {
			return nil, wrap(err)
		}
		if err := refuseFollowUpArtifactMode(rr.Recipe, rr.Name); err != nil {
			return nil, wrap(err)
		}
		if err := refuseFollowUpReportMode(rr.Recipe, rr.Name); err != nil {
			return nil, wrap(err)
		}
		printRecipeNote(warn, rr, true)
		rcp = rr.taskRecipe()
		changed = ch.SHA256 != "" && recipe.Sum(rr.Text) != ch.SHA256
	}
	fmt.Fprintf(warn, "note: keeping recipe %s, which the runner chose for run %s\n", pluginwire.Printable(ch.Name), pluginwire.Printable(oneLineCLI(c.Previous.Run)))
	if changed {
		fmt.Fprintf(warn, "note: recipe %s has changed since run %s\n", pluginwire.Printable(ch.Name), pluginwire.Printable(oneLineCLI(c.Previous.Run)))
	}
	return rcp, nil
}
