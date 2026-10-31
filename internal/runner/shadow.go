package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The challenger arm (docs/model-ab-testing, plan A2.1): a task the launcher
// sampled carries task.AB, and the arm it names is the run's whole difference
// from an unsampled one. A challenger keeps the task as it was given, runs the
// same stages, caps and governance, and differs in exactly two ways (D554,
// D555): its coder is another model — the owner's choice, never the
// repository's (D556) — and it publishes nothing at all. Not a push, not a
// checkpoint, not a pull request, not a comment; its agent holds no git
// credential; the one comment the runner would post on a refusal is not
// posted. What it does instead is record the same facts an unsampled run
// records, so a pair can be scored from records alone (D559), and upload its
// patch beside the champion's.
//
// Every guard below sits where the side effect would happen rather than at
// one entry point, because the ways a run reaches a remote are many and the
// promise is absolute: nothing of a challenger's reaches the developer.

// readOnlyAuth is what a provider must offer to carry a challenger arm: a
// credential that reads the repository and cannot write to it. The interface
// is declared here, on the caller's side, so a provider gains the ability by
// having the method — github.Provider's GitAuthReadOnly — and one that has no
// read-only mint (bitbucket's, or GitHub's where the App's key is the
// credentials service's) refuses the arm instead of handing it the write
// capability it must never hold (A2.1).
type readOnlyAuth interface {
	GitAuthReadOnly(ctx context.Context, minValid time.Duration) (gitprov.GitAuth, error)
}

// readArm reads the task's pair block: which arm this run is, and, for a
// challenger, the coder model it runs. It is called as soon as the task is
// read, because the arm changes what the rest of the run may do — the provider
// is not even opened yet — and because the block is echoed onto the record
// here, so a run that stops for anything later still says it was an arm
// (D559).
//
// The challenger's model ID is checked here, before it reaches the record or
// a log line: task.json is decoded strictly but its ab block carries a free
// string, and a hand-edited one could hold a control character or a name no
// reader should have to render. The check is the same rule a route's models
// are held to (config.CheckModelPattern), so a challenger this refuses is one
// the run could not have used anyway.
func (r *run) readArm() error {
	ab := r.spec.AB
	if ab == nil {
		return nil
	}
	if ab.Arm == task.ABArmChallenger {
		if msg := config.CheckModelPattern(ab.Challenger); msg != "" {
			return r.refuseArm(fmt.Sprintf("the task's ab.challenger %s", msg))
		}
		if len(r.spec.ReviewPRs) > 0 {
			// A review run's output is the comment it posts on the pull
			// requests it reviews, which no arm may write.
			return r.refuseArm("a review run is never a challenger arm: its verdict is a comment on the pull requests it reviews, and a challenger publishes nothing (D554)")
		}
		r.shadow = true
	}
	block := *ab
	r.rec.AB = &block
	return nil
}

// ignoredABLine is what a build with no A/B feature says once, in the run log,
// about the ab: block it was handed and does not act on: a statement about
// what this build does with the block, and never a notice about a licence
// (D522, D523).
const ignoredABLine = "ab: is set but this build does not include it, so it is ignored"

// noteIgnoredAB writes that line when the project layer this run read carries
// an ab: block, once per run. Silence about a switch somebody set is what D522
// exists to prevent, and the run log is where the person who wrote ab: will
// look.
//
// What this build does not hold is the sampling half of the feature: the
// sampler that decides a run is sampled, the launch of the pair and the report
// over it (D550 puts all three in ee/ab, which no build of this release
// carries), so a project that writes ab: is sampled for nothing here whatever
// it says. The shadow half is this build's and does act: a task carrying an
// arm shadows (readArm above). That is why the line is about the layer's block
// and never about a task's own — the block is what this build reads and cannot
// act on, and the task's is read and honoured.
func (r *run) noteIgnoredAB() {
	if r.ab == nil || r.d.Log == nil {
		return
	}
	r.mu.Lock()
	said := r.abSaid
	r.abSaid = true
	r.mu.Unlock()
	if !said {
		r.d.Log.Warn(ignoredABLine)
	}
}

// applyChallenger puts a challenger arm's coder model in place: the model the
// task names, on the coder role alone, after the recipe's own role mapping has
// run (ApplyRoles) so the reviewer keeps the model the champion's run has
// (D555) — with a recipe that maps one role onto the other the mapping stands
// and only the coder moves.
//
// It is also the arm's re-check at bootstrap, which the launcher's admission
// cannot be: a task replayed by hand, or edited after it was stored, names a
// challenger the owner never opened for this repository or one this run may
// not reach. Membership is the owner's list in the project layer the task
// carries (D556) — the repository's own file may not name challengers at all —
// and a task whose layer names none admits none: fail-closed, since a run that
// cannot read the list cannot know the model is the owner's. The run's own
// allow-list, price table and provider policy then judge the model the
// ordinary way, as the coder the run is pinned to, because this runs before
// checkBudget — and a problem one of them reports about the coder pin is what
// bootstrap reads as the arm's own refusal (modelRefusal, gateway.go).
func (r *run) applyChallenger(a *config.Agent) error {
	if !r.shadow {
		return nil
	}
	ch := r.spec.AB.Challenger
	switch {
	case r.ab == nil || len(r.ab.Challengers) == 0:
		return r.refuseArm("this task is a challenger arm, but the project layer it carries names no ab.challengers: the models a challenger may run are the owner's to name, and a run that cannot read the list cannot tell such a model from one nobody opened for this repository (D556)")
	case !slices.Contains(r.ab.Challengers, ch):
		return r.refuseArm(fmt.Sprintf("the task's ab.challenger %q is not one of the project layer's ab.challengers: a challenger is the owner's to name, and never the repository's (D556)", ch))
	}
	r.championCoder = a.ModelFor(config.RoleCoder)
	a.Models.Coder = ch
	return nil
}

// errNoReadOnlyToken marks mintGitAuth's refusal of an arm whose provider
// has no read-only credential: the caller at bootstrap tells it from a
// provider that failed, because the first is the arm's own boundary and the
// second is infrastructure (openProvider).
var errNoReadOnlyToken = errors.New("no read-only installation token")

// refuseArm ends a challenger arm the run may not carry. The record is
// StatusFailed/OutcomeNone with reason exactly here — not infra_error:
// nothing about the runner or its infrastructure failed, the boundary was
// held, and the decision is the whole point (the shape refuseReview records
// a refused review with). It is the run's own trace and nothing else: at
// every point this is called from, nothing has been cloned, pushed, opened
// or commented on.
func (r *run) refuseArm(reason string) error {
	reason = r.redact(reason)
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	r.d.Log.Warn("the challenger arm was refused", "reason", reason)
	return errors.New(reason)
}

// mintGitAuth is the run's one git credential, minted for the life it must
// have: a challenger arm's is read-only, so no token this run holds could
// push however the agent spent it, and every other run's is the provider's
// ordinary installation token.
//
// The refusal is the type assertion, and it is fail-closed by construction:
// a provider that has no read-only mint — Bitbucket's, or GitHub's where the
// App's key is the credentials service's — never reaches its write path at
// all, because this asks for the read-only method and not for a permission
// the caller could get wrong. That is also why a Bitbucket repository is
// never a challenger in v1 (A2.1): no such token exists to mint.
func (r *run) mintGitAuth(ctx context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	if !r.shadow {
		return r.provider.GitAuth(ctx, minValid)
	}
	ro, ok := r.provider.(readOnlyAuth)
	if !ok {
		return gitprov.GitAuth{}, fmt.Errorf("the %s provider mints no read-only installation token, and a challenger arm publishes nothing, so it may hold no credential that could push: a repository on it is never a challenger (A2.1): %w", r.providerKind, errNoReadOnlyToken)
	}
	return ro.GitAuthReadOnly(ctx, minValid)
}

// MaxPatchBytes caps the stored patch object. A patch is read by a person and
// by the judge run (A5.1), so a pair's two diffs must stay fetchable; the cap
// is a fact on the record (PatchTruncated) rather than a silent cut, and the
// run it belongs to is not failed over it.
const MaxPatchBytes = 2 << 20

// uploadPatch stores the run's own tree diff as runs/<slug>/<run-id>/
// ab/patch.diff, from the commit the run started at to its final head — both
// arms upload one, which is the pair's material (D559, A2.1), and a run that
// was not sampled uploads none. A failure warns: the pair loses the diff, and
// the run it belongs to loses nothing it published. The two record fields are
// set together, so a truncated patch is never one a reader has to guess the
// key of.
//
// The diff is redacted before it is stored (patchDiff), the way every other
// agent-derived object in the runs bucket is: an agent may commit a value the
// run redacts — SECURITY.md says a run cannot be stopped from doing that —
// and a challenger's diff reaches no remote where anything else would scan
// it, while the judge run of a pair (A5.1) fetches exactly this object. The
// same pass runs for both arms, so the two diffs of a pair stay comparable.
func (r *run) uploadPatch(ctx context.Context, head string) {
	if r.rec.AB == nil {
		return
	}
	diff, truncated, err := r.patchDiff(ctx, head)
	if err != nil {
		r.d.Log.Warn("reading the run's patch failed; no patch object was stored", "err", r.redact(err.Error()))
		return
	}
	if err := r.d.Store.PutFile(ctx, runstore.PatchName, diff, "text/x-diff"); err != nil {
		r.d.Log.Warn("storing the patch object failed", "err", r.redact(err.Error()))
		return
	}
	r.rec.PatchObject, r.rec.PatchTruncated = runstore.PatchKey(r.d.Store.Prefix()), truncated
}

// patchGuard is how much of a diff past the cap is read before the cut: a
// value the run redacts may straddle the cut, and redacting the whole of it
// needs the bytes just past it. The guard is read, redacted and dropped, so
// it never widens what the bucket holds.
const patchGuard = 64 << 10

// patchDiff reads the tree diff between the run's start commit and head.
// Nothing external runs for it (not diff.external, not a textconv, which the
// agent could set in .git/config or .gitattributes), rename detection is off
// so the same trees always give the same bytes, and no commit metadata is in
// it: a pair's two diffs are compared, and the messages an agent writes are
// not part of what is compared.
//
// What it holds is bounded as git writes it, never after: a vendored tree or
// a formatter sweep can be hundreds of MiB of diff, and a buffer holding all
// of it before the cap applies would take the container to its memory limit
// and lose the patch with an infra_error. The bytes past the guard are read
// and dropped.
func (r *run) patchDiff(ctx context.Context, head string) ([]byte, bool, error) {
	if r.repo == nil || r.startSHA == "" || head == "" {
		return nil, false, errors.New("the run has no start commit to diff from")
	}
	w := &cappedWriter{limit: MaxPatchBytes + patchGuard}
	args := []string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--end-of-options", r.startSHA, head}
	if err := gitStream(ctx, r.repo, w, args...); err != nil {
		return nil, false, err
	}
	text := agent.Redact(w.buf.String(), r.secretList())
	truncated := w.over
	if len(text) > MaxPatchBytes {
		// Cut at the last line boundary the cap allows, so the stored patch is
		// whole lines wherever there is one; a diff whose bytes are one line
		// longer than the cap (a minified file, a fixture) is cut at the cap
		// itself, which is still the diff's own first bytes.
		cut := strings.LastIndexByte(text[:MaxPatchBytes], '\n') + 1
		if cut == 0 {
			cut = MaxPatchBytes
		}
		text, truncated = text[:cut], true
	}
	return []byte(text), truncated, nil
}

// cappedWriter keeps the first limit bytes written to it and counts the rest
// as dropped, so a reader can bound what a command may cost it.
type cappedWriter struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := w.limit - w.buf.Len(); room > 0 {
		if room < n {
			w.over = true
			p = p[:room]
		}
		w.buf.Write(p)
	} else if n > 0 {
		w.over = true
	}
	return n, nil
}

// gitStream runs a read-only git command in repo with its stdout going to w,
// as sessionGit runs one into a buffer of its own: the same error shape,
// the same environment, the same wait for a child holding the pipe.
func gitStream(ctx context.Context, repo *gitops.Repo, w io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo.Dir
	cmd.Env = repo.Environ()
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// shadowFinalize is finalize for a challenger arm: the same record, without
// anything that reaches the developer. The leftover commit, the run's own
// commits, the changed paths, the rigour mode, the verify records, the patch
// object and the verdict are all the run's own; the push, the pull request,
// the status section, the reviewers and the report comment are not, and
// nothing here calls the provider at all (D554).
//
// The status says how the run ended — halted, cancelled, or the verdict Decide
// reached — and the outcome is none in every case, which is what tells a
// reader and every figure downstream that this arm published nothing (D568).
func (r *run) shadowFinalize(ctx context.Context) error {
	halt := r.haltValue()
	leftover := "fugaro: uncommitted work at finalize"
	if halt != nil {
		leftover = leftoverHaltMessage
	}
	// The champion's leftover commit holds back the artifacts finalize refuses
	// (stray.go), and this arm holds back the same ones, so the two arms'
	// changed paths and patch objects describe the same checkout of the same
	// work — which is what a pair is scored on (D559). Nothing is recorded as
	// refused here: this arm has no branch for a refusal to be about.
	strays, err := r.strayArtifacts(ctx)
	if err != nil {
		return fmt.Errorf("listing what the run left untracked: %w", err)
	}
	committed, err := r.repo.CommitAllExcept(ctx, leftover, strayPaths(strays))
	if err != nil {
		return fmt.Errorf("committing leftover work: %w", err)
	}
	if r.follow == nil {
		ahead, err := r.runCommits(ctx)
		if err != nil {
			return fmt.Errorf("counting the run's own commits: %w", err)
		}
		if ahead == 0 {
			r.fail("the agent made no commits")
			if err := r.repo.CommitEmpty(ctx, "fugaro: "+r.failure()); err != nil {
				return fmt.Errorf("recording an empty commit: %w", err)
			}
		}
	}
	sha, err := r.repo.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("reading head sha: %w", err)
	}
	r.recordChangedFiles(ctx)
	r.finalizeRigor()
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		return fmt.Errorf("reading verify records: %w", err)
	}
	records = r.redactRecords(records)
	r.rec.HeadSHA, r.rec.Verify = sha, records
	r.uploadVerifyRecords(ctx, records)
	r.uploadPatch(ctx, sha)

	pol := r.findingsPolicy()
	ready, reason := Decide(records, sha, seniorReview(r.rec.Reviews), pol)
	r.rec.Tolerated = r.toleratedRecord(pol)
	if committed && reason == ReasonNoVerifiedTest {
		reason = "uncommitted changes were committed at finalize, after the last verified test run"
	}
	if fr := r.failure(); fr != "" {
		ready, reason = false, fr
	}
	if halt != nil {
		ready, reason = false, (&HaltError{*halt}).Error()
	}
	r.rec.Reason = reason
	switch {
	case halt != nil:
		r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, halt
	case r.isCancelled():
		r.rec.Status, r.rec.Outcome = runstore.StatusCancelled, runstore.OutcomeNone
	case ready:
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeNone
	default:
		r.rec.Status, r.rec.Outcome = runstore.StatusFailed, runstore.OutcomeNone
	}
	r.updateCost()
	r.narrowText = r.narrowSummary(ctx)
	r.storeEnded(ctx, records)
	return nil
}
