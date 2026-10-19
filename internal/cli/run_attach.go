package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// attachInputsFn is attachInputs' call in attachLaunch; a test swaps it to
// see when the upload happens relative to the task's own write.
var attachInputsFn = attachInputs

// attachStreamFn is the blobx write attachInputs makes, and it streams the
// handle it is given. A test swaps it to replace the PATH in the instant
// between the judgement and the copy — the race the single open exists to
// close — and can then assert that what went up is the file the launcher
// judged and not whatever the path names by then. It is a seam and not the
// bare method because the mutation that matters here is streaming the PATH
// again: the file's name is all a re-open needs.
var attachStreamFn = func(b *blobx.Bucket, ctx context.Context, key string, f *os.File, st fs.FileInfo, ct string, ifNotExist bool) (int64, string, error) {
	return b.PutFileOpened(ctx, key, f, st, ct, ifNotExist)
}

// attachInputs uploads each path to runs/<slug>/<run>/inputs/<name> and
// returns the spec's Inputs (design artifact-runs.md §4 step 3). It runs
// between the run id's mint and CreateTask (plan P7), checks the workflow's
// caps before the first byte, and hashes as it uploads (blobx.PutFileOpened),
// so the sha256 it records is of the bytes that went up and not of a second
// read of the file.
//
// It opens each path once, and everything it checks is checked on that same
// handle: attachOpen's fstat is what the caps are judged by, and the handle it
// returns is what PutFileOpened streams, bounded by exactly that size. A path
// swapped between attachLaunch's own pre-check and this upload — for a FIFO,
// for /dev/zero, or for a file larger than the cap — is therefore refused by
// the check on the handle, or bounded by the size that check allowed, rather
// than streamed into the bucket and left for the runner to refuse at
// bootstrap with the bytes already up (blobx.PutFile's contract: what it
// streams is the regular file it fstat'ed, at most one byte past the size the
// caller judged).
//
// Every upload is create-only (ifNotExist): that is race hardening, not the
// guarantee — the deterministic refusal is attachRefusesStoredRun's, made
// before this runs — and it is what stops a second launcher that won the run
// id in the meantime from overwriting the objects of the run whose task is
// already there. blobx classifies gocloud's FailedPrecondition, on the
// file:// driver as well as on GCS, as ErrExists.
func attachInputs(ctx context.Context, b *blobx.Bucket, prefix string, paths []string, caps config.Inputs) ([]task.InputFile, error) {
	opened, err := attachOpen(paths, caps)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, o := range opened {
			_ = o.f.Close()
		}
	}()
	inputs := make([]task.InputFile, 0, len(opened))
	for _, o := range opened {
		ct := mime.TypeByExtension(filepath.Ext(o.name))
		if ct == "" {
			// A recorded hint, never trusted by any reader (design §8.1):
			// an extension nobody knows is a file whose type is unknown.
			ct = "application/octet-stream"
		}
		size, sum, err := attachStreamFn(b, ctx, runstore.InputKey(prefix, o.name), o.f, o.st, ct, true)
		if err != nil {
			if errors.Is(err, blobx.ErrExists) {
				return nil, userErr("input %s is already in run %s; --attach only ever uploads into a new run", o.name, strings.TrimSuffix(prefix, "/"))
			}
			return nil, remote(fmt.Errorf("uploading input %s: %w", o.name, err))
		}
		inputs = append(inputs, task.InputFile{Name: o.name, Bytes: size, SHA256: sum})
	}
	return inputs, nil
}

// openedInput is one attachment the caps allow, held open: the name it will be
// stored and downloaded under, the fstat of the handle in f, and that handle —
// the one attachInputs uploads from.
type openedInput struct {
	name string
	st   fs.FileInfo
	f    *os.File
}

// attachOpen opens every path --attach named ONCE and judges the caps on the
// handle it opened, which is where the guarantee lives: attachPrepared's own
// check runs earlier (attachLaunch), before the launch's image and budget
// gates, and a path may be a different file by the time this call runs. What
// is opened here is what is uploaded, and nothing is re-opened in between —
// os.Stat asks about a path, this asks about the file, and only the second one
// is the same file the bytes come from.
//
// blobx.OpenSource does the opening, so it is the launcher's own rule: without
// following a final symlink (a link to /dev/zero is a stream that never ends)
// and without blocking on a FIFO (a non-blocking fd is what makes an unbounded
// read visible instead of a hang). A directory, a device, a socket and a FIFO
// then fail attachJudge's regular-file rule on the handle's own mode. On any
// refusal every handle opened so far is closed before the error is returned:
// nothing is left open, and nothing has been read or written.
func attachOpen(paths []string, caps config.Inputs) ([]openedInput, error) {
	out := make([]openedInput, 0, len(paths))
	closeHeld := func() {
		for _, o := range out {
			_ = o.f.Close()
		}
	}
	for _, p := range paths {
		f, err := blobx.OpenSource(p)
		if err != nil {
			closeHeld()
			if errors.Is(err, syscall.ELOOP) {
				return nil, userErr(attachSymlink, p)
			}
			return nil, userErr("--attach %s: %v", p, err)
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			closeHeld()
			return nil, userErr("--attach %s: %v", p, err)
		}
		out = append(out, openedInput{name: filepath.Base(p), st: st, f: f})
	}
	cands := make([]attachCandidate, 0, len(out))
	for i, o := range out {
		cands = append(cands, attachCandidate{path: paths[i], name: o.name, size: o.st.Size(), mode: o.st.Mode()})
	}
	if err := attachJudge(cands, caps); err != nil {
		closeHeld()
		return nil, err
	}
	return out, nil
}

// attachSymlink refuses a symlinked attachment, and it is a format string for
// the path: Lstat says the link, and blobx.OpenSource's O_NOFOLLOW fails the
// open of one, so both producers reach the same sentence.
const attachSymlink = "--attach %s is a symbolic link, and --attach uploads the file itself: the link would be stored under its own name and read from somewhere this launch never named, so point --attach at the file"

// attachCandidate is one path --attach named, with what the filesystem said
// about it: the name it is stored and downloaded under, its size, and its
// mode. Two producers build these — attachPrepared from an Lstat, before the
// launch's gates, and attachOpen from the fstat of the handle the upload will
// read — and attachJudge is the one place the rules are applied to them.
type attachCandidate struct {
	path string
	name string
	size int64
	mode fs.FileMode
}

// attachJudge refuses a whole set --attach named, or returns nil: the name
// rules (task.InputNameProblem, the one predicate the runner applies again),
// the duplicate rules, the regular-file rule, and the three caps. It is the
// only place these live, so the pre-check and the upload cannot disagree, and
// it writes nothing and reads nothing.
//
// The three caps are independent and each one means one thing (design §4
// step 2, P6): max_files counts PATHS, max_bytes bounds ONE FILE and
// max_total_bytes bounds THE SET. A set of ten files each comfortably under
// max_bytes is still over the total; one large file is over the per-file cap
// with the count and the total untouched. Every refusal names the number that
// tripped, the cap it tripped and the count, and happens before the first
// byte is written.
func attachJudge(cands []attachCandidate, caps config.Inputs) error {
	count := caps.MaxFileCount()
	if len(cands) > count {
		return userErr("--attach: %d files, and workflows.inputs.max_files allows %d for one launch", len(cands), count)
	}
	perFile := caps.MaxFileBytes()
	seen := map[string]string{}
	// Keyed on the FOLDED name — runner.ArtifactCollisionKey, the same key the
	// runner's own duplicate check for these names uses and the one Task 4's
	// artifact names are keyed on: two names differing only in case, or in a
	// Unicode normalization the filesystem applies, are one name where the
	// runner writes them back, and a local run downloads into the checkout on
	// the operator's own filesystem, which folds. A second upload would then
	// have replaced the first file, and the task would name two inputs and hold
	// one. One key for both sides, or the launcher accepts a set the runner
	// refuses (or worse, writes over itself).
	folded := map[string]string{}
	var total int64
	for _, c := range cands {
		if problem := task.InputNameProblem(c.name); problem != "" {
			return userErr("--attach %s: its name %s", c.path, problem)
		}
		if first, dup := seen[c.name]; dup {
			// Refused rather than overwritten: two paths with one base name
			// would be one object and one entry, and the second upload would
			// be a create-only write against an object already up. Both
			// PATHS are named, not the base name twice: the collision is
			// between two directories holding one file name, so the two
			// paths are the whole of what the owner has to tell apart.
			return userErr("--attach: %s and %s are both named %s; a run stores one object per name, so rename one of them or attach them from different directories", first, c.path, c.name)
		}
		if first, dup := folded[runner.ArtifactCollisionKey(c.name)]; dup {
			return userErr("--attach: %s is named %s and %s is named %s; the names differ only in case (or in a Unicode normalization), and the runner writes the inputs into one directory, where that is one name — so rename one of them or attach them from different directories",
				first, filepath.Base(first), c.path, c.name)
		}
		seen[c.name] = c.path
		folded[runner.ArtifactCollisionKey(c.name)] = c.path
		switch {
		case c.mode&fs.ModeSymlink != 0:
			// Refused, not followed: os.Stat would follow it and describe its
			// target, so the size and the mode below would be the target's
			// while the name stored is the link's — and the bytes uploaded
			// come from wherever the link points, which is not the file this
			// launch named. See attachSymlink.
			return userErr(attachSymlink, c.path)
		case c.mode.IsDir():
			return userErr("--attach %s is a directory; attach files, one path each", c.path)
		case !c.mode.IsRegular():
			// A device, a FIFO, a socket: a stat describes it, and the size
			// it reports (0 for /dev/zero, as for most of them) is a lie about
			// what uploading it would read. Attaching one would stream an
			// unbounded, non-file stream into the run's bucket and record the
			// size the stat claimed, so it is refused here, before anything is
			// read or written — the same rule the directory above follows, and
			// the same one blobx.PutFileOpened makes for itself on the handle
			// it is given (the two are independent: this one names the mode to
			// the owner, that one guarantees the copy).
			return userErr("--attach %s is %s, not a regular file; --attach uploads files, one path each", c.path, c.mode)
		}
		if c.size > perFile {
			return userErr("--attach %s is %s, and workflows.inputs.max_bytes allows %s per file (of %d attached)", c.path, attachBytes(c.size), attachBytes(perFile), len(cands))
		}
		total += c.size
	}
	if set := caps.MaxSetBytes(); total > set {
		return userErr("--attach: %d files totalling %s, and workflows.inputs.max_total_bytes allows %s for the set", len(cands), attachBytes(total), attachBytes(set))
	}
	return nil
}

// attachPrepared is attachJudge's early caller: it describes each path the
// launch is about to attach with an os.Lstat and refuses the set if the rules
// say so. It is called once by attachLaunch, so a set over the caps or holding
// a directory is refused before the launch's image and budget gates, and it
// writes and opens nothing — the handles, the fstat the upload is judged on
// and the upload itself are attachOpen's and attachInputs' (the different
// producers are why attachJudge takes candidates rather than paths).
//
// Lstat, not Stat: a symlink is refused here rather than described as its
// target (attachJudge's symlink arm), and every other rule reads the same
// either way. This check is advisory — a path can be a different file by the
// time the upload opens it — and the upload's own judgement on the handle it
// holds is what the guarantee rests on.
func attachPrepared(paths []string, caps config.Inputs) error {
	cands := make([]attachCandidate, 0, len(paths))
	for _, p := range paths {
		st, err := os.Lstat(p)
		if err != nil {
			return userErr("--attach %s: %v", p, err)
		}
		cands = append(cands, attachCandidate{path: p, name: filepath.Base(p), size: st.Size(), mode: st.Mode()})
	}
	return attachJudge(cands, caps)
}

// attachBytes spells a size the way the caps are written, so a refusal and
// the config key it names are read in the same units.
func attachBytes(n int64) string {
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

// attachRefusesStoredRun is the run-id freshness check, and it is FIRST: it
// runs before the caps are checked and before the first byte leaves the
// machine (design §4 step 1, plan Review Focus 11). A run id whose task.json
// is already there is refused whether the stored task differs from this one
// or is byte-identical to it.
//
// --attach does not adopt. A stored task already names its inputs and those
// objects are already in the bucket, so the upload this launch would do is a
// create-only write against objects that are there — a failure, not a
// no-op — and "the same bytes" cannot be established without reading the old
// objects back and re-hashing them, which this slice does not do. Without
// this check the upload would run first and only then reach createTask's
// adoption-or-refusal rule, which sits after the task's write and after a
// launch could have happened: a mistyped --run-id would have overwritten a
// finished run's inputs before anyone looked at its task. A plain launch —
// no --attach — keeps createTask's adoption rule exactly as it is.
func attachRefusesStoredRun(ctx context.Context, s *runstore.Store, runID string) error {
	_, err := s.ReadTask(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil
	case err != nil:
		return remote(err)
	}
	return userErr("run ID %s already holds a task; --attach uploads into a run's own inputs/, so it never adopts an existing run — use a new run ID", runID)
}

// attachLaunch is everything an --attach launch refuses BEFORE it uploads:
// the recipe refusal, the caps, the D9 line and the image gate, in the
// design's order (§4 steps 2 to 4). It returns the caps the files were judged
// by, which attachUpload applies again as it writes them.
//
// It deliberately does not upload. The upload is attachUpload's, which
// finishLaunch calls last, after every gate that can still refuse the launch
// (checkMaxParallel, checkArtifactLaunch, budgetPrecheck) and before
// createTask: a refusal the launcher can make from what it already has must
// leave nothing in the bucket (design §4 step 1, plan Review Focus 11), and
// an upload here would leave a run id that never gets a task holding files
// nobody can see, read or clean up.
func attachLaunch(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, prefix string, paths []string, kind string, clone bool, warn io.Writer) (config.Inputs, error) {
	caps := config.Inputs{}
	if len(paths) == 0 {
		return caps, nil
	}
	if err := checkAttachRecipe(ctx, spec); err != nil {
		return caps, err
	}
	caps = attachCaps(ctx, spec)
	if err := attachPrepared(paths, caps); err != nil {
		return caps, err
	}
	// D9's line, printed once per launch and only here, where --attach
	// carried a file. It is not a gate: the policy that already governs the
	// model's own data governs this, and the run proceeds whatever it says.
	ag := config.Agent{}
	if co := checkoutConfig(ctx, spec.Repo); co != nil {
		ag = co.Agent
	}
	attachProviderWarning(warn, attachProviders(env.lc, ag, spec.Repo))
	// The image gate (plan P9), with the rest of the launch's gates but
	// ahead of the mode: artifact one below, so a job image that predates
	// this feature is refused by the sentence that names --attach rather
	// than by the one that names the mode.
	subject, knows := "--attach", "attached files"
	if clone {
		if err := checkCloneImageSince(env.lc, spec, artifactsSince, subject, knows, "", warn); err != nil {
			return caps, err
		}
	} else if err := checkImageSince(ctx, env, slug, spec, kind, artifactsSince, subject, knows, "", warn); err != nil {
		return caps, err
	}
	return caps, nil
}

// attachUpload is the last thing an --attach launch does, and the only write
// it makes before createTask's own: it uploads the files and fills in
// spec.Inputs, so the task that is written next names exactly the bytes that
// are in the bucket. finishLaunch calls it after every gate that can refuse
// the launch, so a refused launch leaves no input object behind.
func attachUpload(ctx context.Context, env *cloudEnv, spec *task.Spec, prefix string, paths []string, caps config.Inputs) error {
	if len(paths) == 0 {
		return nil
	}
	inputs, err := attachInputsFn(ctx, env.bucket, prefix, paths, caps)
	if err != nil {
		return err
	}
	spec.Inputs = inputs
	return nil
}

// checkAttachRecipe refuses an --attach launch that is not an artifact run
// (design §4 step 1's second half), before the caps and before the first
// byte: an attachment is only meaningful to a run that produces files rather
// than a pull request — on an implement or review recipe the inputs would be
// downloaded into a run that commits a diff and opens a PR, and nothing in
// its steps reads inputs/.
//
// Every arm fails closed, and the two ways a launch can be an artifact run
// are ALLOWED for a first run only. A follow-up is never one: it continues a
// pull request, and the runner refuses every follow-up whose recipe is
// mode: artifact (runner.go, `r.follow != nil && rcp.Mode ==
// recipe.ModeArtifact`), whatever resolved that recipe — including the
// repository's own .fugaro/recipes/default.yaml, which a follow-up that
// inherited nothing falls back to. So both allow arms here are gated on
// !spec.IsFollowUp(), and a follow-up that reaches either of them is refused
// with the reason, before the caps and before the first byte.
//
// The rest are fail-closed. A resolved recipe of another mode is refused with
// its name and its mode. A launch that leaves the recipe to the runner
// (runnerChooses: no recipe in the task, not a follow-up, no checkout here)
// is refused too, because this CLI cannot prove the mode the runner will
// pick — it takes agent.recipe from fugaro.yaml at the ref, else the catalog
// default — and being unable to prove it is not a reason to upload the files
// anyway. The message names the fix. And a follow-up the previous run left no
// recipe for is refused with the recipe the runner will really use, since it
// never reads agent.recipe for one (recipeForLaunch's follow-up arm).
func checkAttachRecipe(ctx context.Context, spec *task.Spec) error {
	if spec.Mode == recipe.ModeArtifact && !spec.IsFollowUp() {
		return nil
	}
	if runnerChooses(ctx, spec) {
		return userErr("--attach needs an artifact recipe, but this launch leaves the recipe to the runner (agent.recipe in fugaro.yaml at %s, else default), so the CLI cannot tell whether the run it starts is mode: artifact; pass the artifact recipe explicitly with --recipe", spec.Ref)
	}
	rcp := recipeForLaunch(ctx, checkoutConfig(ctx, spec.Repo), spec)
	switch {
	case spec.IsFollowUp() && rcp != nil && rcp.Mode == recipe.ModeArtifact:
		// The allow arm below must never fire for a follow-up, so it is
		// gated on !spec.IsFollowUp() and this arm says why: a follow-up
		// inherits the recipe it resolves rather than choosing one, and the
		// runner refuses EVERY follow-up whose recipe is mode: artifact
		// (runner.go, `r.follow != nil && rcp.Mode == recipe.ModeArtifact`),
		// because an artifact run opens no pull request for one to continue.
		// The recipe here need not be one anyone asked for: a follow-up that
		// inherited nothing resolves the repository's own
		// .fugaro/recipes/default.yaml at this task's base, and a repository
		// may commit a mode: artifact one there. Uploading then writing the
		// task would put the bytes in the bucket for a run that cannot start.
		return userErr("--attach needs an artifact recipe, and %s is one, but this is a follow-up: a follow-up inherits the recipe it resolves rather than choosing one, and the runner refuses every follow-up whose recipe is mode: artifact, because an artifact run opens no pull request for one to continue — so this launch is refused here rather than uploading files the run cannot use", rcp.Name)
	case spec.IsFollowUp() && spec.Mode == recipe.ModeArtifact:
		// The same rule for a task that CARRIES the mode rather than
		// resolving to it: no follow-up is ever an artifact run, and the
		// runner refuses the pair as well — on the resolved recipe, which
		// agrees with this stamp or is refused for disagreeing with it.
		return userErr("--attach needs an artifact recipe, but a follow-up is never an artifact run: it continues a pull request, so no follow-up carries mode: artifact, and the runner refuses one that does")
	case !spec.IsFollowUp() && rcp != nil && rcp.Mode == recipe.ModeArtifact:
		// An artifact recipe the CLI resolved is exactly what --attach needs,
		// whatever spec.Mode says: the first arm above is keyed on the task's
		// own mode, and design §8.2 lets a task carry none (the runner
		// resolves the recipe from the task, the checkout or the catalog, and
		// its own cross-check refuses a stamp that disagrees with the recipe).
		// Keying this on rcp != nil alone would refuse an artifact recipe here
		// with a message claiming it is not one.
		return nil
	case spec.IsFollowUp() && spec.Recipe == nil && rcp != nil:
		// A follow-up runs the recipe of the run it continues, carried either
		// in its task or in that run's record; when both are silent the runner
		// takes its own default, and it never reads agent.recipe for a
		// follow-up (resolveRecipe's `r.follow == nil` arm). So rcp is the
		// recipe the run will really use — not artifact — even when THIS
		// checkout's fugaro.yaml names an artifact one, which is exactly what
		// this sentence has to say: an owner looking at a fugaro.yaml that
		// already names an artifact recipe would otherwise read the generic
		// refusal below as a bug.
		return userErr("--attach needs an artifact recipe; the run this follow-up continues left no recipe behind, and a follow-up never takes agent.recipe from fugaro.yaml, so the runner runs recipe %s (mode: %s)", rcp.Name, rcp.Mode)
	case rcp != nil:
		return userErr("--attach needs an artifact recipe; %s is mode: %s", rcp.Name, rcp.Mode)
	case spec.Recipe != nil:
		return userErr("--attach needs an artifact recipe; recipe %s could not be read here, so the launch is refused rather than upload files a run may not use", spec.Recipe.Name)
	default:
		return userErr("--attach needs an artifact recipe")
	}
}

// attachCaps is the workflow's inputs caps for this launch, as the launcher
// can read them: the checkout's fugaro.yaml when there is one. With no
// checkout to read — a launch made from outside the repository — the zero
// config.Inputs answers with the DEFAULTS (config.Inputs.MaxFileCount and its
// two siblings).
//
// Those defaults are the caps' ceilings, not a floor, so the zero value is
// not the conservative reading of a workflow this CLI cannot see: a workflow
// may declare `inputs.max_files: 1` (config.validateInputs bounds the caps
// from above only), and a launch from outside the checkout then uploads a
// set the workflow refuses. That is the accepted cost of the check being a
// pre-check — design §4 step 2 has the launcher refuse a set over the caps
// before the first byte and the runner apply the workflow's own values
// again, which is the check that always bites — but it is not conservatism
// and must not be described as though it were.
func attachCaps(ctx context.Context, spec *task.Spec) config.Inputs {
	cfg := checkoutConfig(ctx, spec.Repo)
	if cfg == nil {
		return config.Inputs{}
	}
	if _, wf, err := cfg.SelectWorkflow(spec.Workflow); err == nil {
		return wf.Inputs
	}
	return config.Inputs{}
}

// The names D9's line uses for the route a model no provider claims takes.
// agent.auth decides which one it is: vertex sends the call to Google, the
// other two to Anthropic, on its API or on a subscription's token.
const (
	attachRouteVertex = "Google Vertex AI"
	// attachDirectAnthropic is Anthropic's own API, which agent.auth api-key
	// calls with the run's stored key.
	attachDirectAnthropic = "Anthropic (direct)"
	// attachRouteOAuth is Anthropic reached with a Claude subscription's
	// oauth token. The label names the secret the token is stored under
	// (config.ClaudeOAuthToken) rather than a bare "oauth token": the
	// reserved name is what tells this credential apart from another
	// harness's — codex-oauth-token is reserved for Codex
	// (config.CodexOAuthToken, owner ruling of 2026-10-18), and a harness
	// added later reuses the shape — so a destination line that said only
	// "oauth token" would name a credential the owner cannot find and could
	// not tell from the next one.
	attachRouteOAuth = "Anthropic (" + config.ClaudeOAuthToken + ")"
)

// attachRouteFor names the destination of a model no provider claims, which
// is the run's own auth route and nothing else. agent.auth vertex — and ""
// with it, since config.applyDefaults writes vertex into every config the
// launcher reads and a launch made outside the checkout hands this function a
// zero config.Agent — sends Claude calls to Google Vertex AI; api-key sends
// them to Anthropic's own API; oauth to Anthropic on a subscription's token.
// A single "Anthropic (direct)" for all three would tell the owner of a
// default configuration that their screenshot goes straight to Anthropic when
// the call, and the bytes in it, go to Google. config.Validate admits only
// these three, so there is no fourth answer to give; the default arm answers
// with the one applyDefaults writes.
func attachRouteFor(auth string) string {
	switch auth {
	case "api-key":
		return attachDirectAnthropic
	case "oauth":
		return attachRouteOAuth
	default:
		return attachRouteVertex
	}
}

// attachProviders lists, sorted, every destination the attached bytes may
// reach through the model (design §8 item 8), and it is NOT lc.Providers on
// its own: lc.Providers holds only the non-Anthropic backends
// (internal/localcfg/localcfg.go), so a set read from it alone is EMPTY for
// a default Claude workflow — exactly the workflow the owner runs — and a
// line built from it would tell an owner their screenshot stays local while
// it is on its way to Anthropic. The set is the union of:
//
//   - each provider that claims one of the run's models (config.ProviderFor)
//     AND may receive this repository's data (config.ModelProvider.AllowsData).
//     The AllowsData half is the filter runner.ProvidersEnv applies when it
//     builds the job's own FUGARO_MODEL_PROVIDERS; the "claims one of the
//     run's models" half is this line's own narrowing and is NOT part of that
//     set — the job is handed every allowed provider so it can route, and a
//     provider that claims none of these three models is one this file's
//     bytes cannot reach. The two sets are not the same set, and nothing here
//     claims they are; and
//   - the run's own auth route (attachRouteFor) whenever any of those models
//     is not claimed by one of those providers, which is
//     config.NeedsAnthropicKey's own rule — a model no allowed provider
//     claims goes direct — and is the case a default Claude workflow always
//     takes. The route is named by ag.Auth, because "direct" is Vertex for a
//     default configuration and Anthropic only under api-key or oauth: the
//     models arrive at a destination the auth mode picks, and the line has to
//     name the one the bytes reach (config.CheckProviderAuth's own reading of
//     the three).
//
// The three models are NeedsAnthropicKey's own three, in its own order
// (coder, reviewer, EffectiveBackground) — NOT Agent.PinnedModels, whose
// third entry is the raw Models.Background. Background is empty whenever the
// config leaves it unset, and a run whose coder a provider claims never makes
// an unset-background call to Anthropic at all: config.EffectiveBackground
// substitutes the coder's own model there, so PinnedModels would print the
// direct route for a run that makes no such call. The set never under-reports
// either way — an unclaimed model always names the auth route — and the one
// line D9 exists for must not name a destination the bytes cannot reach.
//
// One case does not get that right, and it is the launcher/checkout
// approximation the rest of this file already makes: with no checkout of the
// task's repository there are no agent models to read, and attachLaunch hands
// this function a ZERO config.Agent (run_attach.go, attachLaunch), so all
// three models are "", the direct route is added — correctly, an unclaimable
// model is a direct-path model in NeedsAnthropicKey's own rule — and that
// route is then named vertex, the auth applyDefaults gives every config this
// CLI reads. So a repository whose fugaro.yaml sets agent.auth api-key, and
// whose every model a gateway serves, is told Vertex AI where the bytes do
// reach Anthropic. It never claims the bytes stay on the machine, but it can
// name a destination the bytes do not reach and leave out the one they do.
// Reading the repository's agent config at the launch's ref is what would fix
// it, which is the same base-file read this CLI cannot make
// (checkArtifactLaunch's and recipeForLaunch's own caveat), and it is no
// worse than the models half of the same approximation, which is silent
// about a claiming provider for the same reason.
//
// The empty set therefore means "no direct path and no claiming
// provider". With these three models it cannot actually arise (an unclaimed
// one is a direct path); the branch is defensive, and
// attachProviderWarning says so with the local wording rather than claiming
// the file stays on the machine.
func attachProviders(lc *localcfg.Config, ag config.Agent, repo string) []string {
	var providers map[string]config.ModelProvider
	if lc != nil {
		providers = lc.Providers
	}
	claimed := map[string]bool{}
	direct := false
	models := []string{ag.ModelFor(config.RoleCoder), ag.ModelFor(config.RoleReviewer), config.EffectiveBackground(ag, providers)}
	for _, model := range models {
		name, p, ok := config.ProviderFor(providers, model)
		if ok && p.AllowsData(repo) {
			claimed[name] = true
			continue
		}
		direct = true
	}
	names := slices.Sorted(maps.Keys(claimed))
	if direct {
		names = append(names, attachRouteFor(ag.Auth))
	}
	return names
}

// attachProviderWarning is D9's line, and it is the whole of D9's mechanism:
// one printed sentence naming every destination an attached file may reach,
// so an owner handing over a customer's screenshot is told where it may go
// before it goes. It is printed once per launch, on the same writer the
// other launch notes use, only when --attach carried at least one file, and
// it is never a gate — the policy that already governs the model's own data
// governs this, and no launch is refused over it.
//
// The local wording is for the empty set alone: nothing claiming the run's
// models and no direct path — not even the run's own auth route, which every
// unclaimed model takes. It says the bytes stay on this machine because in
// that case that is true, and it is the only case where it is.
func attachProviderWarning(warn io.Writer, providers []string) {
	if len(providers) == 0 {
		fmt.Fprintln(warn, "--attach: the attached files reach the model, and the workflow's models route nowhere: no provider claims them and the run's auth route is unset, so the bytes stay on this machine.")
		return
	}
	fmt.Fprintf(warn, "--attach: the attached files reach the model, and so reach %s. Attaching a file is sending it there, under the same policy the workflow's own model calls already run under.\n",
		strings.Join(providers, ", "))
}
