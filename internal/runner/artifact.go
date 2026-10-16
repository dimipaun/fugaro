package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// artifactStageDir is the staging directory under the state directory:
// <StateDir>/artifacts/, beside verify/ (P5). On Cloud Run that filesystem
// is memory, which is what the caps and the memory rule in gate.go are
// about.
const artifactStageDir = "artifacts"

// maxArtifactNameBytes caps an artifact's name: the leaf of an object key,
// and the name a reader passes back to the retriever.
const maxArtifactNameBytes = 200

// contentSniffLen is how many of an artifact's first bytes the recorded
// content type is sniffed from: what http.DetectContentType reads, taken from
// the copy as it is written rather than from the staged file afterwards.
const contentSniffLen = 512

// artifactStagedSeam runs between the staging pass (which hashes every
// staged file) and the upload, when a test sets it (export_test.go's
// SetArtifactStagedSeam). It exists because the window it stands in is a real
// one — the plan stage's agent is the same user in the same container and can
// write the staging directory — and because nothing else can reach it.
var artifactStagedSeam func()

// artifactCheckedSeam runs between the pass that examines and sizes every
// match and the pass that copies them, when a test sets it (export_test.go's
// SetArtifactCheckedSeam). It is the same window as artifactStagedSeam's, one
// step earlier: what has been checked is a name and a size, and what is copied
// is read from the filesystem again.
var artifactCheckedSeam func()

// artifactCopiedSeam runs after one match has been copied into the staging
// directory and before the recorded content type is taken, when a test sets it
// (export_test.go's SetArtifactCopiedSeam). It is the window between the
// staged file existing and the record describing it, which is the one a swap
// at the staged path lands in: the type is sniffed from the bytes the copy
// read, so nothing there is opened a second time — a FIFO planted in this
// window must not be able to block the staging step.
var artifactCopiedSeam func()

// artifactSet is what the runner staged, in the order it will be uploaded:
// the staging root and one entry per file.
type artifactSet struct {
	dir   string
	files []staged
}

// staged is one artifact as the staging directory holds it: its sanitised
// name (relative to the artifacts prefix), where it was copied to, and what
// was hashed and read there. It carries no size of its own: the record's is
// the size the upload measured on the bytes it wrote, which is the number the
// record can be held to.
type staged struct {
	name        string
	path        string
	sha256      string
	contentType string
}

// renderTree is the tree a render runs in: the working checkout, uncommitted
// work included, which the plan stage shared. It is recorded in
// runstore.RenderRecord.Tree, whose only legal value it is today — the
// design's D8 is what would add a second one (plan P2).
const renderTree = "working"

// renderPlanPath is where a mode: artifact run's plan stage is asked to leave
// its plan: a file in the checkout, read by nobody but a person (the render
// the runner runs is commands.render, never a plan the model wrote).
const renderPlanPath = ".fugaro/render-plan.json"

// runArtifactMode runs artifactMode, recovering a panic so the run always
// ends with a record, the same guarantee runAgentLoop and runReviewMode
// give their own modes.
func (r *run) runArtifactMode(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			// The same priority every other end branch gives a halt: it
			// decides the run whatever else is true, a panic mid-stage
			// included.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			r.failArtifactInfra(ctx, fmt.Errorf("stage %s panicked: %v", r.rec.Stage, p))
		}
	}()
	r.artifactMode(ctx)
}

// artifactMode runs a mode: artifact run (design artifact-runs.md §3): the
// optional plan stage, then the one render step. The run ends with outcome
// artifacts on success, or failed/none otherwise; it never pushes, never
// opens a pull request and sets r.skipPR, so finalize, which still runs
// after, does nothing more.
//
// An artifact run's agent gets no git credentials (the agent env block and
// refreshGitAuth both skip r.artifact, exactly as they skip r.review): the
// deliverable goes to the runs bucket, never to a branch, so a push
// credential in its environment would only be reachable by the plan stage's
// own prompt injection for nothing.
func (r *run) artifactMode(ctx context.Context) {
	if r.artifactPlan {
		r.rec.Stage = "plan"
		r.save(ctx)
		req := agent.Request{Prompt: r.artifactPlanPrompt(), SessionID: agent.NewSessionID()}
		res, ok, _ := r.stage(ctx, "plan", req, stageOpts{})
		r.countTokens(res)
		if !ok {
			// The same priority gate.go's own error branch follows: a halt
			// decides the run whatever else is true, before cancel is even
			// asked about.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			if r.isCancelled() {
				r.endArtifactCancelled(ctx, "cancelled during the plan stage")
				return
			}
			r.endArtifactFailed(ctx)
			return
		}
		// r.stage checked haltValue() right before returning ok=true, but a
		// halt can land in the window between that check and here.
		if h := r.haltValue(); h != nil {
			r.endArtifactHalted(ctx, h)
			return
		}
	}
	rec, err := r.runRender(ctx)
	r.renderRec = renderRecordFor(r.wf.Commands.Render, rec)
	r.rec.Render = r.renderRec
	r.updateCost()
	switch {
	case err == nil:
		if h := r.haltValue(); h != nil {
			r.endArtifactHalted(ctx, h)
			return
		}
		if aerr := r.collectArtifacts(ctx); aerr != nil {
			// The same priority the render's own failure path follows: a
			// halt or a cancel that landed while the set was staged and
			// uploaded decides the run, and anything else is the run's own
			// failure — a name the sanitiser refused, a cap the set broke,
			// an upload the bucket refused. Failed/none, never
			// infra_error: these are the recipe's and the render's own
			// outcomes, judged by the runner, and there is no pull request
			// to fall back to.
			if h := r.haltValue(); h != nil {
				r.endArtifactHalted(ctx, h)
				return
			}
			if r.isCancelled() {
				r.endArtifactCancelled(ctx, "cancelled while uploading the artifacts")
				return
			}
			r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(aerr.Error())
			r.d.Log.Warn("the artifact step failed", "err", r.rec.Reason)
			break
		}
		r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeArtifacts
	case r.haltValue() != nil:
		// A halt that landed while the render ran: recorded by the stage's
		// own machinery (the budget backend polls independently), and a halt
		// decides the run whatever the render did. Both of these end the run
		// themselves — status, reason, skipPR and the report — so they return:
		// falling through would store the report a second time, as
		// reviewmode.go's equivalent branches do not.
		r.endArtifactHalted(ctx, r.haltValue())
		return
	case r.isCancelled():
		r.endArtifactCancelled(ctx, "cancelled during render")
		return
	default:
		r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(err.Error())
		r.d.Log.Warn("the render step failed", "err", r.rec.Reason)
	}
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// renderRecordFor is the Record.Render to store for what runRender returned,
// or nil when no render ran. runRender's blank-command arm returns before it
// executed anything, and its record describes no render: a blank command,
// the sha256 of the empty string, exit code -1. runstore.Record.Render
// documents a blank command as none, and schemas/result.schema.json's render
// description says the same ("absent ... for an artifact run refused before
// its render step"), so that record is dropped here rather than stored as an
// artifact run's render. bootstrap's checkGateCommands refuses such a recipe
// first, so this is the fail-closed arm behind it, not a reachable launch.
func renderRecordFor(command string, rec runstore.RenderRecord) *runstore.RenderRecord {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return &rec
}

// runRender runs commands.render once, in the checkout, under
// timeouts.render and the run's own finalize reserve, with the agent's own
// environment (the workflow's secrets included, design §8.6/D10). Its exit
// code decides the run.
//
// The command string and the timeout come from the runner's IN-MEMORY
// workflow config (r.wf.Commands.Render, r.wf.Timeouts.Render), which
// bootstrap set once at runner.go and nothing re-reads: this function is
// NOT a verify.LoadSettings caller. That is deliberate, not an omission —
// <StateDir>/workflow.json is 0644 inside a directory the plan-stage agent
// can write (it is the same user in the same container), and
// verify.LoadSettings re-reads it per invocation. A render that loaded its
// command from there could be made to run a string the reviewer never read,
// while render.command_sha256 attested to it (design §3, Review Focus 1).
//
// The record's Command is the redacted string and CommandSHA256 the hash of
// the string the runner RAN (the unredacted one), so the two are one hash
// pair exactly when the redactor did not fire (design D8).
func (r *run) runRender(ctx context.Context) (runstore.RenderRecord, error) {
	command := r.wf.Commands.Render
	started := r.d.Now()
	rec := runstore.RenderRecord{
		Command:       r.redact(command),
		CommandSHA256: sha256Hex(command),
		Tree:          renderTree,
		ExitCode:      -1,
		StartedAt:     started.UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(command) == "" {
		// Fail closed: bootstrap's checkGateCommands already refuses a
		// recipe whose render step has no commands.render, but a caller is
		// never trusted to have checked first, and an empty command would
		// otherwise run as a successful no-op.
		return rec, errors.New("commands.render is empty")
	}
	r.rec.Stage = "render"
	r.save(ctx)
	deadline, cutByReserve := r.budget.StepDeadline(r.wf.Timeouts.Render.Duration)
	stageCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// The render's output is a repository's own command's, so it goes
	// through the same redactor the agent's streams do, whole lines at a
	// time, and only its tail is kept.
	tail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	out := agent.NewRedactor(tail, r.secretList())
	code, err := procgroup.Run(stageCtx, procgroup.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: r.d.WorkDir, Env: r.env, Stdout: out, Stderr: out,
	})
	// Flushed before the tail is read: the redactor holds a line until a
	// newline arrives, so a render whose last line has none — a failure
	// message, typically — would be missing from the tail exactly when it
	// explains the failure.
	out.Flush()
	rec.DurationS = r.d.Now().Sub(started).Seconds()
	rec.ExitCode = code
	rec.TimedOut = errors.Is(stageCtx.Err(), context.DeadlineExceeded)
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{Name: "render", StartedAt: started, DurationS: rec.DurationS})
	log := r.d.Log.With("stage", "render")
	switch {
	case rec.TimedOut && cutByReserve:
		// The run's own reserve ended the render, not timeouts.render: the
		// deadline above is whichever of the two comes first, and the
		// reason says which one it was, so the record never blames a
		// timeout that had time left to run (design §4.5).
		log.Warn("commands.render cut at the finalize reserve", "reserve", r.budget.Reserve, "total", r.budget.Total, "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render was cut at the run's finalize reserve, not at its timeouts.render (%s): the %s run budget keeps %s for finalize",
			r.wf.Timeouts.Render.Duration, r.budget.Total, r.budget.Reserve)
	case rec.TimedOut:
		// procgroup.Run reports a command killed by ctx as ctx.Err().
		log.Warn("commands.render timed out", "timeout", r.wf.Timeouts.Render.Duration, "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render timed out after %s", r.wf.Timeouts.Render.Duration)
	case err != nil:
		return rec, fmt.Errorf("running commands.render: %w", err)
	case code != 0:
		log.Warn("commands.render failed", "exit_code", code)
		r.noteRenderTail(tail)
		return rec, fmt.Errorf("commands.render exited %d", code)
	}
	log.Info("commands.render finished", "exit_code", code, "duration_s", rec.DurationS)
	for _, l := range tail.Lines() {
		log.Info("render output", "line", l)
	}
	return rec, nil
}

// collectArtifacts is the step after a successful render: stage the set,
// hash it in the staging directory, upload it, and record it (design §3, P5).
// Nothing is staged until every refusal has been made, so a refused set
// leaves the staging directory empty and the bucket untouched.
func (r *run) collectArtifacts(ctx context.Context) error {
	set, err := r.stageArtifacts(r.out)
	if err != nil {
		return err
	}
	if artifactStagedSeam != nil {
		artifactStagedSeam()
	}
	files, err := r.uploadArtifacts(ctx, set)
	if err != nil {
		return err
	}
	// Left nil rather than set to an empty slice: the record writes no
	// artifacts key at all when a run delivered none (runstore.Record), so
	// "this run uploaded nothing" and "this record predates the field" are
	// the same bytes on disk.
	if len(files) > 0 {
		r.rec.Artifacts = files
	}
	return nil
}

// stageArtifacts matches the recipe's globs against the checkout, refuses
// what must not be uploaded, copies each match into <StateDir>/artifacts/ and
// hashes it there.
//
// The order of the refusals is the order of their cost: the count against
// max_files first (cheap, and it bounds everything after it), then the names
// and their collisions, then each file's own size against max_bytes, then the
// set's total against max_total_bytes. One cap means one thing: max_files
// counts entries, max_bytes is ONE artifact, max_total_bytes is the whole set
// and is not derived from the other two.
//
// Two kinds of refusal are deliberately not the same:
//
//   - a name that would escape the outputs root, be absolute, hold a NUL or
//     a newline, be over-long, begin with a dot, or collide with another
//     match's name after cleaning fails the RUN. Every one of them means the
//     record would either name something outside the run's own prefix or
//     claim an artifact the retriever cannot hand back, and a set that
//     silently drops one is a record that lies about what the render
//     produced.
//   - a match whose bytes would come from outside the outputs root — a link to
//     somewhere else, or a file reached through a symlinked directory — and a
//     match that is not a regular file are skipped with a warning and the run
//     goes on (the plan's own rule): the bytes would be a file the render never
//     wrote, so they are not copied, but a repository whose outputs hold such a
//     link has not thereby escaped anything — the file is simply not a
//     deliverable. A glob that matched nothing is reported the same way, one
//     line per glob (design §8.4), and is not a refusal at all.
func (r *run) stageArtifacts(out *recipe.Outputs) (artifactSet, error) {
	set := artifactSet{dir: filepath.Join(r.d.StateDir, artifactStageDir)}
	// Empty first, whatever the outcome. The staging directory is the runner's
	// own, and the only writer besides this function is the plan stage's agent,
	// which is the same user in the same container: a file left there by that
	// agent — or by the run this state was resumed from — is not this run's set,
	// and a run that stages nothing must leave nothing behind either. So the
	// directory holds this run's set, exactly, or is not there at all.
	if err := os.RemoveAll(set.dir); err != nil {
		return set, fmt.Errorf("emptying the staging directory %s: %w", set.dir, err)
	}
	if out == nil || len(out.Globs) == 0 {
		return set, errors.New("the recipe names no outputs.globs: an artifact run with nothing to select has nothing to upload")
	}
	root := outputsRoot(out.Globs)
	matches, empty, err := matchOutputs(r.d.WorkDir, out.Globs)
	if err != nil {
		return set, err
	}
	if len(matches) == 0 {
		// Not an error by itself: a render may legitimately produce nothing
		// (design §8.4), and the probe step is what turns "nothing" into a
		// failure when the recipe's owner wants one. It is reported rather
		// than silent — this line, and the report's own "no files" line,
		// which the record's empty artifacts carries — and nothing is
		// staged, so the run leaves the bucket exactly as it found it. Every
		// glob is in empty here, so the one line naming the list is exact.
		r.d.Log.Warn("commands.render matched none of the recipe's outputs.globs", "globs", strings.Join(out.Globs, ", "))
		return set, nil
	}
	// One line per glob that matched nothing, naming that glob and no other
	// (design §8.4, "it is reported"): a set that came out of the other
	// patterns is no reason for a typo in this one to go unsaid.
	for _, g := range empty {
		r.d.Log.Warn("commands.render matched nothing for one of the recipe's outputs.globs", "glob", g)
	}
	kept, err := r.usableOutputs(matches, root)
	if err != nil {
		return set, err
	}
	if len(kept) == 0 {
		// Every match was refused above, each with its own warning saying why,
		// so this line only marks the step as over: nothing is staged and the
		// record carries no artifacts.
		r.d.Log.Warn("commands.render left no usable file for the recipe's outputs.globs", "globs", strings.Join(out.Globs, ", "))
		return set, nil
	}
	if len(kept) > out.FileCount() {
		return set, fmt.Errorf("commands.render produced %d files matching %s, over outputs.max_files %d: narrow the globs or raise the cap",
			len(kept), strings.Join(out.Globs, ", "), out.FileCount())
	}
	names := make([]string, len(kept))
	// Keyed on ArtifactCollisionKey, the retriever's own key for "these two
	// names are one file": two names that differ only in case, or in a
	// normalization macOS applies, are one name where `fugaro artifacts get`
	// writes them back, so the second artifact would be written over the
	// first — and a set keyed more weakly than the retriever's is a set the
	// run stages and no download of it can then hand back.
	taken := make(map[string]string, len(kept))
	for i, o := range kept {
		rel, err := relativeName(r.d.WorkDir, root, o.match)
		if err != nil {
			return set, err
		}
		name, err := sanitiseName(rel)
		if err != nil {
			return set, fmt.Errorf("the render's %s: %w", o.match, err)
		}
		if prev, dup := taken[ArtifactCollisionKey(name)]; dup {
			return set, fmt.Errorf("two of the render's files clean to the same name %q: %s and %s (a name that differs only in case, or in a Unicode normalization a filesystem may apply, is the same name where the retriever writes it back)",
				name, prev, o.match)
		}
		taken[ArtifactCollisionKey(name)] = o.match
		names[i] = name
	}
	sizes := make([]int64, len(kept))
	var total int64
	for i, o := range kept {
		// The size the caps are checked against, measured on the resolved
		// target. It is a moment old by the time it is used, which is why the
		// copy re-stats the file it opens and refuses a size that moved.
		st, err := os.Stat(o.target)
		if err != nil {
			return set, fmt.Errorf("sizing the render's %s: %w", o.target, err)
		}
		if st.Size() > out.MaxFileBytes() {
			return set, fmt.Errorf("the render's %s is %s, over outputs.max_bytes %s (the per-artifact cap): lower the file or raise the cap",
				o.match, recipe.SizeText(st.Size()), recipe.SizeText(out.MaxFileBytes()))
		}
		sizes[i] = st.Size()
		total += st.Size()
	}
	if total > out.MaxSetBytes() {
		return set, fmt.Errorf("the render's %d files total %s, over outputs.max_total_bytes %s (every file is under outputs.max_bytes %s): raise the cap or narrow the globs",
			len(kept), recipe.SizeText(total), recipe.SizeText(out.MaxSetBytes()), recipe.SizeText(out.MaxFileBytes()))
	}
	if artifactCheckedSeam != nil {
		artifactCheckedSeam()
	}
	if err := os.MkdirAll(set.dir, 0o755); err != nil {
		return set, fmt.Errorf("creating the staging directory: %w", err)
	}
	for i, o := range kept {
		dst := filepath.Join(set.dir, filepath.FromSlash(names[i]))
		// The resolved target, and copyFileHashed opens it without following a
		// link and stats the file it opened: what is staged is the file the
		// caps were checked against. It hands back the first bytes it read, so
		// the type below is sniffed from the staged bytes with no second open
		// of the staged path.
		_, sum, head, err := copyFileHashed(o.target, dst, sizes[i])
		if err != nil {
			r.clearStaging(set, "after a copy failed")
			return set, err
		}
		if artifactCopiedSeam != nil {
			artifactCopiedSeam()
		}
		ct := artifactContentType(names[i], head)
		set.files = append(set.files, staged{name: names[i], path: dst, sha256: sum, contentType: ct})
	}
	sort.Slice(set.files, func(i, j int) bool { return set.files[i].name < set.files[j].name })
	return set, nil
}

// clearStaging takes the staging directory back to nothing after a failure
// partway through a set. A half-staged set is worse than no set: the plan
// stage's agent can read the state directory, and a retry in the same state
// would find files that no record describes. Failure here is only logged —
// the run is already failing for the reason that got it to this call.
func (r *run) clearStaging(set artifactSet, why string) {
	if set.dir == "" {
		return
	}
	if err := os.RemoveAll(set.dir); err != nil {
		r.d.Log.Warn("the staging directory could not be emptied", "dir", set.dir, "why", why, "err", err)
	}
}

// uploadArtifacts writes the set to runs/<slug>/<run>/artifacts/, in name
// order, and returns the record's own account of it. The hash each entry
// carries is the one the upload measured on the bytes it wrote, checked
// against the staged copy's: the record never attests to bytes that are not
// in the bucket.
//
// An upload that fails partway leaves nothing of its own in the bucket. The
// record for a run whose upload failed says it produced no artifacts, and a
// bucket that holds the first few objects anyway is a set no record describes
// — read back by `artifacts get` and never by result.json. The writes are
// create-only, so a re-executed run in the same run directory would also stop
// on the first key that was already there: the removal is what makes the
// retry a retry.
func (r *run) uploadArtifacts(ctx context.Context, set artifactSet) ([]runstore.ArtifactFile, error) {
	if len(set.files) == 0 {
		return nil, nil
	}
	files := make([]runstore.ArtifactFile, 0, len(set.files))
	for _, f := range set.files {
		size, sum, err := r.d.Store.PutFileFrom(ctx, runstore.ArtifactPrefix+f.name, f.path, f.contentType, f.sha256)
		if err != nil {
			r.removeUploaded(ctx, files)
			return nil, err
		}
		files = append(files, runstore.ArtifactFile{Name: f.name, Bytes: size, SHA256: sum, ContentType: f.contentType})
	}
	return files, nil
}

// removeUploaded deletes the objects written before an upload failed, best
// effort, and logs any it could not. A bucket that cannot be written to is
// often one that cannot be deleted from either; the run's own failure is
// reported either way, and this line says what was left behind. The delete
// itself is detached from ctx (runstore.DeleteCleanupTimeout): the caller's
// context is usually already cancelled or nearly out of time, which is the very
// failure this is cleaning up after.
func (r *run) removeUploaded(ctx context.Context, files []runstore.ArtifactFile) {
	if len(files) == 0 {
		return
	}
	for _, f := range files {
		if err := r.d.Store.DeleteFile(ctx, runstore.ArtifactPrefix+f.Name); err != nil {
			r.d.Log.Warn("an artifact from a failed upload could not be removed", "name", f.Name, "err", err)
		}
	}
}

// matchOutputs expands the recipe's globs against the checkout, in glob order,
// and names the globs that matched nothing. A pattern that cannot be parsed is
// refused rather than skipped: a glob nobody can match is a recipe bug, and
// silently uploading less than the recipe asked for is the failure this whole
// step exists to prevent.
//
// empty is per glob rather than per run because that is what design §8.4's "it
// is reported" has to mean: a recipe whose second pattern is a typo is exactly
// the case a reader needs to hear about, and it is invisible to a test that
// only asks whether the whole set came out empty.
func matchOutputs(workDir string, globs []string) (matches, empty []string, err error) {
	for _, g := range globs {
		if filepath.IsAbs(g) {
			return nil, nil, fmt.Errorf("the outputs.globs entry %q is absolute: a glob is relative to the checkout", g)
		}
		m, err := filepath.Glob(filepath.Join(workDir, filepath.FromSlash(g)))
		if err != nil {
			return nil, nil, fmt.Errorf("the outputs.globs entry %q: %w", g, err)
		}
		if len(m) == 0 {
			empty = append(empty, g)
		}
		matches = append(matches, m...)
	}
	return matches, empty, nil
}

// output is one of the render's files as the run will copy it: match is the
// path the recipe's own glob produced, which is what the name, the caps and
// the warnings are about, and target is the file its bytes come from with
// every symlink resolved. The two are the same string unless something on the
// way to the match is a link.
type output struct {
	match, target string
}

// usableOutputs drops the matches that must not be copied, with a warning
// naming each one: anything whose bytes would come from outside the outputs
// root (the bytes would be a file the render never wrote), and anything that
// is not a regular file. A match that cannot be examined at all is an error:
// that is the filesystem failing, not a refusal. The same file matched by two
// globs is kept once.
func (r *run) usableOutputs(matches []string, root string) ([]output, error) {
	rootAbs, err := r.outputsRootAbs(root)
	if err != nil {
		return nil, err
	}
	kept := make([]output, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		target, why, err := outputProblem(m, rootAbs)
		if err != nil {
			return nil, err
		}
		if why != "" {
			r.d.Log.Warn("refusing one of the render's outputs", "path", m, "reason", why)
			continue
		}
		if seen[target] {
			// Two globs that match the same FILE are one artifact, not two:
			// out/* and out/*.png side by side are how a recipe is naturally
			// written, and a second copy of one file is not a name collision.
			continue
		}
		seen[target] = true
		kept = append(kept, output{match: m, target: target})
	}
	return kept, nil
}

// outputsRootAbs is the outputs root with every symlink resolved, refused when
// it leaves the checkout. The root is what "inside the outputs root" is
// measured against, so a root that is itself a link would move the question
// with it: a render that leaves ../elsewhere and points out/ at it would have
// every match resolve to a file the repository does not hold, and the run
// would record bytes from anywhere the render can write as its own outputs.
func (r *run) outputsRootAbs(root string) (string, error) {
	workAbs, err := filepath.EvalSymlinks(r.d.WorkDir)
	if err != nil {
		return "", fmt.Errorf("resolving the checkout %s: %w", r.d.WorkDir, err)
	}
	rootAbs := filepath.Join(r.d.WorkDir, filepath.FromSlash(root))
	resolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		// A root that does not exist has matched nothing; the path under the
		// checkout is what the per-match checks compare against.
		return rootAbs, nil
	}
	if !withinDir(workAbs, resolved) {
		return "", fmt.Errorf("the outputs root %s resolves to %s, outside the checkout %s: an artifact run's outputs are the repository's own", root, resolved, workAbs)
	}
	return resolved, nil
}

// outputProblem is why path must not be copied, or "" when it may be. root is
// the outputs root with every symlink resolved.
//
// The question is about the file the bytes would come from, never about the
// name that reaches it, so every element of the path is resolved:
// filepath.EvalSymlinks turns out/evil/secret.png into the file behind a
// symlinked DIRECTORY, and a match like that is refused exactly as a match that
// is itself a link is. Lstat'ing the final component alone would see a plain
// regular file there and copy bytes from outside the root — the one thing this
// check exists to prevent — because the link is in the middle of the path, not
// at its end.
//
// What the path resolves TO matters as well, not just where it is: a link to a
// directory would fail the copy with EISDIR where the plain directory is
// skipped with a warning, and a link to a FIFO would block the copy forever
// (the staging copy takes no context, so nothing could interrupt it). Both get
// the answer their unlinked selves get, from metadata that cannot block.
func outputProblem(path, root string) (string, string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", "", fmt.Errorf("examining the render's %s: %w", path, err)
	}
	link := st.Mode()&os.ModeSymlink != 0
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		if link {
			return "", fmt.Sprintf("it is a symlink whose target cannot be resolved (%v)", err), nil
		}
		return "", "", fmt.Errorf("examining the render's %s: %w", path, err)
	}
	if !withinDir(root, target) {
		if link {
			return "", fmt.Sprintf("it is a symlink to %s, outside the outputs root %s", target, root), nil
		}
		return "", fmt.Sprintf("it reaches %s through a symlinked directory, outside the outputs root %s", target, root), nil
	}
	// target holds no symlink of its own, so Lstat answers about the file the
	// copy would open — and answers it without the open, which is what keeps a
	// FIFO from blocking here.
	tst, err := os.Lstat(target)
	if err != nil {
		return "", "", fmt.Errorf("examining the render's %s: %w", path, err)
	}
	if !tst.Mode().IsRegular() {
		return "", "it is not a regular file", nil
	}
	return target, "", nil
}

// withinDir reports whether path is dir or below it. Both are absolute and
// already resolved (filepath.EvalSymlinks), which is what makes the answer
// about the file the bytes would come from rather than about a name.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// relativeName is a match's path relative to the outputs root, slash
// separated: "out/clips/a.png" under the root "out" is "clips/a.png". The
// root is the deepest common root of the recipe's globs, so the names of a
// one-glob recipe are the glob's own tail.
func relativeName(workDir, root, match string) (string, error) {
	rel, err := filepath.Rel(workDir, match)
	if err != nil {
		return "", fmt.Errorf("the render's %s: %w", match, err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("the render's %s is outside the checkout", match)
	}
	if root == "" || root == "." {
		return rel, nil
	}
	if !strings.HasPrefix(rel, root+"/") {
		return "", fmt.Errorf("the render's %s is not under the outputs root %s", match, root)
	}
	return strings.TrimPrefix(rel, root+"/"), nil
}

// outputsRoot is the deepest directory every one of the recipe's globs lives
// under: the root the uploaded names are relative to. ["out/*.mp4"] is "out",
// ["out/*.mp4", "out/clips/*.png"] is "out", ["*.mp4"] is "." (the checkout
// itself, so a top-level match keeps its own name).
func outputsRoot(globs []string) string {
	var root []string
	for i, g := range globs {
		elems := rootElems(globRoot(g))
		if i == 0 {
			root = elems
			continue
		}
		root = commonElems(root, elems)
	}
	return strings.Join(root, "/")
}

// globRoot is the directory part of one glob: everything before its first
// meta character, taken as a directory.
func globRoot(g string) string {
	prefix := g
	if i := strings.IndexAny(g, "*?["); i >= 0 {
		prefix = g[:i]
	}
	return path.Dir(prefix)
}

// rootElems splits a root into path elements, dropping "." and "".
func rootElems(root string) []string {
	var out []string
	for _, e := range strings.Split(root, "/") {
		if e != "" && e != "." {
			out = append(out, e)
		}
	}
	return out
}

// commonElems is the longest common prefix of two element lists.
func commonElems(a, b []string) []string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// sanitiseName is the one refusal list every artifact name goes through
// (design §8.2): the name is the match's path relative to the outputs root,
// cleaned with path.Clean, and it must be non-empty, at most 200 bytes, not
// absolute, not an escape out of the root, free of a NUL or a newline, and
// not dot-named. It returns the cleaned name, so a caller that accepted
// "clips/./a.png" uploads "clips/a.png".
//
// The dot rule is a matched pair with the retriever's (design §9): `fugaro
// artifacts get` refuses a dot-named entry, and Go's filepath.Glob — unlike a
// shell — matches a leading dot, so without this rule out/*.mp4 would upload
// and record a .hidden.mp4 that can never be handed back. It is the NAME's
// first character and nothing else, which is exactly how the retrieval path
// words its own rule (design §9: "the refusal is only on a name whose first
// character is `.`"), so the two sides cannot drift: a dot inside a name
// (sizzle.v2.mp4) and a name inside a dot-named directory below the outputs
// root (clips/.hidden/a.png) are both names this accepts, and both are names
// `get` hands back.
//
// This function is about the NAME and nothing else. Whether a match is a file
// whose bytes may be copied is stageArtifacts' question (outputProblem): a
// symlink out of the root has a perfectly good name and must still not be
// followed.
func sanitiseName(name string) (string, error) {
	clean, problem := ArtifactName(name)
	if problem != "" {
		return "", errors.New(problem)
	}
	return clean, nil
}

// ArtifactName is the rule above, exported because it is **one rule with two
// callers**: sanitiseName is the uploader's, and `fugaro artifacts get`
// (internal/cli) is the retriever's. It returns the name as an artifact's name
// — path.Clean of it — and the reason it may not be one, or "" when it may.
//
// The two sides must not word the rule twice (design §9: the upload path
// cannot produce what the retrieval path refuses). A retriever that refused
// one character class more than this list — the zero-width characters and
// bidi marks a terminal escapes rather than drops, say — would refuse an
// artifact the run legitimately delivered, and, because `fugaro artifacts get
// RUN` with no NAME refuses the whole set on one bad entry, would refuse every
// artifact of that run. So the class is decided here, once, by firstControl's
// table, and neither caller adds one.
func ArtifactName(name string) (clean, problem string) {
	if name == "" {
		return "", "the name is empty"
	}
	if i, what := firstControl(name); i >= 0 {
		return "", fmt.Sprintf("the name %q holds %s at byte %d: a name is printed in the report and typed at a shell, and a control character is how a name reads as something other than what it is", name, what, i)
	}
	// A backslash is not a control character, and it is refused anyway: it is
	// a path separator on Windows and an ordinary byte everywhere else, so a
	// name carrying one is two different paths depending on the machine that
	// reads it. path.Clean — which is what this rule cleans with, and what the
	// uploader records — reads "frames\a.png" as ONE element, while
	// filepath.Join (what `fugaro artifacts get` builds its destination with)
	// reads it as two on Windows, where "frames\..\..\x.png" then leaves the
	// destination root that the rest of this list exists to keep a name
	// inside. Refusing it on every platform keeps the rule the same on all of
	// them, which is the only way the uploader and the retriever can share it.
	if i := strings.IndexByte(name, '\\'); i >= 0 {
		return "", fmt.Sprintf("the name %q holds a backslash at byte %d: a backslash separates path elements on Windows and is a character elsewhere, so one name is two different paths", name, i)
	}
	if len(name) > maxArtifactNameBytes {
		return "", fmt.Sprintf("the name is %d bytes, over the %d-byte cap", len(name), maxArtifactNameBytes)
	}
	if path.IsAbs(name) {
		return "", fmt.Sprintf("the name %q is absolute: an artifact's name is relative to the outputs root", name)
	}
	clean = path.Clean(name)
	switch {
	case clean == ".":
		return "", fmt.Sprintf("the name %q names no file", name)
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "", fmt.Sprintf("the name %q escapes the outputs root", name)
	case strings.HasPrefix(clean, "."):
		return "", fmt.Sprintf("the name %q begins with a dot, which the retriever refuses: a file nobody can fetch back is not uploaded", clean)
	}
	return clean, ""
}

// ArtifactCollisionKey is the identity two artifact names share when one
// filesystem would store them as one file: NFC-normalised, and then every rune
// replaced by the smallest rune in its simple case-folding orbit.
//
// Both halves are load-bearing, and neither is strings.ToLower:
//
//   - a lower-cased key is not EqualFold's equivalence. unicode.ToLower maps
//     U+0130 (İ) to 'i', which strings.EqualFold does NOT equate with it, so a
//     key like that collides two names that are two files — and, with one name
//     per key, an entry like that evicts the entry it collided with and hides
//     the real pair behind it (a set listing "I.png", "İ.png", "i.png" has a
//     genuine collision between the first and the last). It misses real
//     collisions on the other side as well: ToLower(U+017F) is U+017F, and the
//     long s folds with "s" and "S" (U+212A, the Kelvin sign, is the same
//     shape for k and K). The orbit minimum gets both cases right, in the
//     direction EqualFold decides ("K" for k, K and U+212A; "S" for s, S and
//     U+017F; "İ" stays "İ").
//   - normalization is not case folding at all: EqualFold("café.png",
//     "café.png") is false for the NFC and NFD spellings, and macOS — where
//     the retriever runs — treats them as one file.
//
// The uploader and the retriever ask the "are these two names one file"
// question with this one function (sanitiseName's duplicate table and the
// retriever's artifactSetConflict), the same "one rule, two callers" shape
// ArtifactName has (design §9: "the upload path cannot produce what the
// retrieval path refuses"). Keyed with a weaker rule — strings.ToLower was
// one — the two drift, and a run that renders both K and U+212A, or a name in
// both a mac's normalizations, stages cleanly while every download of the run
// is then refused wholesale.
//
// The key is only ever a map key: nothing prints it, and nothing builds a
// path from it.
func ArtifactCollisionKey(name string) string {
	folded := norm.NFC.String(name)
	var b strings.Builder
	b.Grow(len(folded))
	for _, r := range folded {
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldRune is the smallest rune in r's simple case-folding orbit — the orbit
// unicode.SimpleFold cycles through, over which strings.EqualFold considers
// its members equal. The minimum is what makes the orbit a single key:
// whichever member a name is written with, the key is the same rune.
func foldRune(r rune) rune {
	min := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < min {
			min = f
		}
	}
	return min
}

// firstControl returns the byte offset of the first character in name that
// must not be an artifact's name, and what to call it, or -1 and "". It is
// every C0 control, DEL, the C1 range, and the bidi overrides: the name is
// printed in report.md and typed at a shell, and an escape sequence or a
// U+202E is a name that reads as a different name — a reader cannot tell
// sizzle.mp4 from sizzle‮gnp.exe. Punctuation is the render's to choose
// (design §9 binds the name's first character and nothing else), so a pipe or
// a backtick is allowed here and escaped where it is rendered
// (report.go's tableText and tableCode) — with one punctuation exception that
// is a path question and not a terminal one: a backslash is refused, in
// ArtifactName above, on every platform, because it separates path elements
// on Windows. NUL and the line breaks are the same
// rule as the rest of this table rather than a case of their own.
//
// What is NOT in this table is as deliberate as what is. The zero-width
// characters and bidi marks safetext's OneLine escapes (U+200B-U+200D,
// U+2060, U+FEFF, the soft hyphen, the CGJ, the variation selectors, the tag
// characters, LRM/RLM/ALM, U+2028/29) are a legal filename a render can
// deliver — an emoji's variation selector is one — and ArtifactName is the
// retriever's rule as well as the uploader's, so refusing them here would
// refuse a name a run legitimately recorded. They are escaped where the name
// is *printed* (internal/safetext), which is where reading one wrong matters;
// a path component is not.
func firstControl(name string) (int, string) {
	for i, r := range name {
		switch {
		case r == 0x00:
			return i, "a NUL"
		case r == '\n':
			return i, "a newline"
		case r == '\r':
			return i, "a carriage return"
		case r == '\t':
			return i, "a tab"
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return i, fmt.Sprintf("the control character U+%04X", r)
		case (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			return i, fmt.Sprintf("the control character U+%04X (a bidi override)", r)
		}
	}
	return -1, ""
}

// copyFileHashed copies src to dst, creating dst's parents, and returns the
// bytes written, their lower-case hex sha256 — the staged copy's own hash,
// taken in the same pass that wrote it — and the first contentSniffLen bytes,
// which is what the recorded content type is sniffed from. want is the size
// the caller measured before the copy: a file that changed under the copy is
// refused, because what would be uploaded is then neither the file that was
// measured against the caps nor the one the record describes.
//
// dst is 0600: an artifact is data, never something a later step executes,
// and the staging directory sits in a state directory the plan stage's agent
// can write.
func copyFileHashed(src, dst string, want int64) (int64, string, []byte, error) {
	// The size that was checked against the caps is the size of a file that
	// was examined a moment ago, and this is where it is used. So the open
	// does not follow a symlink and does not block on a FIFO, the fstat is on
	// the very file the bytes will come from, and the copy is bounded by the
	// size that was checked plus one byte: whichever of the three the swap
	// defeats, the record still describes bytes the caps approved or the run
	// fails. (Before this, the copy read whatever was at the path to its end
	// and only then compared the total with the size it had approved.)
	in, err := openNoFollow(src)
	if err != nil {
		return 0, "", nil, fmt.Errorf("staging %s: %w", src, err)
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return 0, "", nil, fmt.Errorf("staging %s: %w", src, err)
	}
	if !st.Mode().IsRegular() {
		return 0, "", nil, fmt.Errorf("staging %s: it is not a regular file (%s): what was checked was a regular file", src, st.Mode())
	}
	if st.Size() != want {
		return 0, "", nil, fmt.Errorf("staging %s: it changed while it was copied (it is %d bytes, it was %d)", src, st.Size(), want)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, "", nil, fmt.Errorf("staging %s: %w", src, err)
	}
	// O_EXCL|O_NOFOLLOW: the staging directory sits in a state directory the
	// plan stage's agent can write, so a symlink planted at dst must fail the
	// create rather than choose what this run writes through. O_TRUNC is gone
	// with it: there is nothing here to truncate, because the directory was
	// emptied before the set was staged into it.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o600)
	if err != nil {
		return 0, "", nil, fmt.Errorf("staging %s: %w", src, err)
	}
	// The head is taken through the descriptor the copy reads from, as the
	// bytes go past: the type the record carries is sniffed from the bytes
	// that were staged, and there is no second open of the staged path left
	// for a link planted there to be followed or a FIFO to block on.
	head := &headBytes{}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(io.TeeReader(in, head), want+1))
	if cerr := out.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return 0, "", nil, fmt.Errorf("staging %s: %w", src, err)
	}
	if n != want {
		return 0, "", nil, fmt.Errorf("staging %s: it changed while it was copied (read %d bytes, it said %d)", src, n, want)
	}
	return n, hex.EncodeToString(h.Sum(nil)), head.buf, nil
}

// headBytes keeps the first contentSniffLen bytes written to it and counts the
// rest: it is the tee a copy sniffs through, so nothing but the write is asked
// of it (a short write here would fail a copy that is otherwise fine).
type headBytes struct{ buf []byte }

func (h *headBytes) Write(p []byte) (int, error) {
	if n := contentSniffLen - len(h.buf); n > 0 {
		h.buf = append(h.buf, p[:min(n, len(p))]...)
	}
	return len(p), nil
}

// artifactContentType is the type recorded for a staged artifact: the name's
// extension first, then a sniff of the first bytes the copy read. It is
// recorded and never trusted (design §8.1) — a reader decides for itself what
// to do with the bytes — and it is never a guess from the render's own
// say-so. head is those first bytes, handed over by copyFileHashed rather than
// read back from the staged path: the staged file is one the plan stage's
// agent can reach, and the recorded type must describe the bytes this run
// wrote, not whatever is at that path when the sniff happens.
func artifactContentType(name string, head []byte) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return http.DetectContentType(head)
}

// noteRenderTail keeps the render's last lines as the report's log tail: a
// failed render's output is the one thing that explains it.
func (r *run) noteRenderTail(tail *logtail.Writer) {
	lines := tail.Lines()
	if len(lines) == 0 {
		return
	}
	r.tail = &LogTail{Source: "the render step", Lines: r.redactAll(lines)}
}

// artifactPlanPrompt is the plan stage's prompt (design §3): the task text,
// the recipe's own use_when, and what the run will render afterwards. It is
// the only place the run describes the deliverable to the agent, so it says
// plainly that the render is the runner's, not the agent's.
//
// It names no FUGARO_INPUTS_DIR and no attachment: that variable arrives
// with the task that adds attachments, beside FUGARO_STATE_DIR in the env
// this run builds, and until then a plan stage cannot see an input because
// nothing can attach one.
func (r *run) artifactPlanPrompt() string {
	var b strings.Builder
	b.WriteString("This is a Fugaro artifact run: the deliverable is a set of files uploaded to this run's bucket, not a pull request. There is no pull request to open, no branch is pushed, and the plan you write is not the deliverable.\n\n")
	name := r.rec.Recipe.Name
	fmt.Fprintf(&b, "The runner renders the deliverable itself, once, after this stage: it runs the workflow's own render command (commands.render in fugaro.yaml at %s) in this checkout and uploads the files the recipe's outputs.globs name. Do not run that render command, and do not try to produce the artifacts yourself.\n\n", r.spec.Ref)
	if u := r.useWhen; u != "" {
		fmt.Fprintf(&b, "The recipe `%s` is for: %s\n\n", name, u)
	}
	if r.out != nil && len(r.out.Globs) > 0 {
		fmt.Fprintf(&b, "The artifacts it expects are the files matching: %s (at most %d files, %s in total).\n\n",
			strings.Join(r.out.Globs, ", "), r.out.FileCount(), fmtBytes(r.out.MaxSetBytes()))
	}
	fmt.Fprintf(&b, "Write your plan for the render to `%s` in this checkout: what the render will have to produce, exactly which files it should leave behind and where, what inputs in the repository it needs, and what is likely to go wrong. Keep it short and concrete.\n\n", renderPlanPath)
	if t := strings.TrimSpace(r.spec.Task); t != "" {
		b.WriteString("**Task:**\n\n" + t + "\n")
	}
	return b.String()
}

// fmtBytes is an artifact cap for a prompt: a whole number of bytes with a
// human unit, never a bare digit string a reader has to decode.
func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// storeArtifactEnded stores an artifact run's report.md — the ordinary run
// report, whose outcome arm and Artifacts section are this task's (report.go)
// — under every terminal state below.
//
// The write is detached from ctx's cancellation and bounded, as save's is:
// this is the only write of report.md for this mode (r.skipPR means finalize
// stores none), and one of the states below IS the cancelled run, whose ctx
// is already cancelled by the time it is reached — so a write on the live ctx
// would refuse exactly the report that explains the cancel.
func (r *run) storeArtifactEnded(ctx context.Context) {
	report := agent.Redact(followUpReport(r.rec, r.d.Store.Prefix(), r.tail, nil, nil), r.secretList())
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
	defer cancel()
	r.storeReport(wctx, report, nil)
}

// endArtifactCancelled ends an artifact run that was cancelled: status
// cancelled, outcome none, never infra_error. finalize, which still runs
// after, does nothing more (r.skipPR).
func (r *run) endArtifactCancelled(ctx context.Context, reason string) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusCancelled, runstore.OutcomeNone, reason
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// endArtifactHalted ends an artifact run that a halt (a budget cap, a kill
// switch, the token cap) stopped. Its outcome is none — a halted artifact
// run has no artifacts, exactly as a halted review run posts no comment —
// which is why the halt advice in halt.go needs no arm of its own for this
// mode.
func (r *run) endArtifactHalted(ctx context.Context, h *runstore.Halt) {
	r.rec.Status, r.rec.Outcome, r.rec.Halt = runstore.StatusHalted, runstore.OutcomeNone, h
	r.rec.Reason = (&HaltError{*h}).Error()
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// failArtifactInfra ends an artifact run whose own operation (the checkout,
// the state directory, the render's process) failed outright: status
// infra_error, outcome none.
func (r *run) failArtifactInfra(ctx context.Context, err error) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	r.skipPR = true
	r.d.Log.Warn("the artifact run failed", "err", r.rec.Reason)
	r.storeArtifactEnded(ctx)
}

// endArtifactFailed ends an artifact run whose plan stage failed (the model
// timed out, crashed, or reported an error) or whose render ended non-zero:
// status failed, outcome none — no pull request to fall back to, and no
// artifacts to claim.
func (r *run) endArtifactFailed(ctx context.Context) {
	reason := r.failure()
	if reason == "" {
		reason = "the plan stage ended without a result"
	}
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, r.redact(reason)
	r.skipPR = true
	r.storeArtifactEnded(ctx)
}

// artifactHasPlan reports whether rcp's steps lead with a plan stage: the
// optional agent stage of a mode: artifact recipe (a recipe's artifact
// shape is exactly [plan], render or [render]).
func artifactHasPlan(rcp *recipe.Recipe) bool {
	for _, s := range rcp.Steps {
		if s.Kind == recipe.StepPlan {
			return true
		}
	}
	return false
}

// sha256Hex is the render record's CommandSHA256: lowercase hex of the
// sha256 of the string the runner ran, which is the text a reviewer can
// hash from the repository at the recorded ref.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
