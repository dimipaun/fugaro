package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// attachInputsFn is attachInputs' call in attachLaunch; a test swaps it to
// see when the upload happens relative to the task's own write.
var attachInputsFn = attachInputs

// attachInputs uploads each path to runs/<slug>/<run>/inputs/<name> and
// returns the spec's Inputs (design artifact-runs.md §4 step 3). It runs
// between the run id's mint and CreateTask (plan P7), checks the workflow's
// caps before the first byte, and hashes as it uploads (blobx.PutFile), so
// the sha256 it records is of the bytes that went up and not of a second
// read of the file.
//
// Every upload is create-only (ifNotExist): that is race hardening, not the
// guarantee — the deterministic refusal is attachRefusesStoredRun's, made
// before this runs — and it is what stops a second launcher that won the run
// id in the meantime from overwriting the objects of the run whose task is
// already there. blobx classifies gocloud's FailedPrecondition, on the
// file:// driver as well as on GCS, as ErrExists.
func attachInputs(ctx context.Context, b *blobx.Bucket, prefix string, paths []string, caps config.Inputs) ([]task.InputFile, error) {
	prepared, err := attachPrepared(paths, caps)
	if err != nil {
		return nil, err
	}
	inputs := make([]task.InputFile, 0, len(prepared))
	for _, p := range prepared {
		ct := mime.TypeByExtension(filepath.Ext(p.path))
		if ct == "" {
			// A recorded hint, never trusted by any reader (design §8.1):
			// an extension nobody knows is a file whose type is unknown.
			ct = "application/octet-stream"
		}
		size, sum, err := b.PutFile(ctx, runstore.InputKey(prefix, p.name), p.path, ct, true)
		if err != nil {
			if errors.Is(err, blobx.ErrExists) {
				return nil, userErr("input %s is already in run %s; --attach only ever uploads into a new run", p.name, strings.TrimSuffix(prefix, "/"))
			}
			return nil, remote(fmt.Errorf("uploading input %s: %w", p.name, err))
		}
		inputs = append(inputs, task.InputFile{Name: p.name, Bytes: size, SHA256: sum})
	}
	return inputs, nil
}

// preparedInput is one attachment the caps allow: the path --attach named,
// the name it will be stored and downloaded under, and its size on this
// machine, read before the first byte leaves.
type preparedInput struct {
	path string
	name string
	size int64
}

// attachPrepared turns the paths --attach named into what will be uploaded,
// or refuses the whole set. It is called twice on a launch that attaches
// anything — once by attachLaunch, so the caps are judged before the launch's
// image gate, and once by attachInputs, which is where the guarantee lives —
// and it writes nothing either time.
//
// The three caps are independent and each one means one thing (design §4
// step 2, P6): max_files counts PATHS, max_bytes bounds ONE FILE and
// max_total_bytes bounds THE SET. A set of ten files each comfortably under
// max_bytes is still over the total; one large file is over the per-file cap
// with the count and the total untouched. Every refusal names the number that
// tripped, the cap it tripped and the count, and happens before the first
// byte is written.
func attachPrepared(paths []string, caps config.Inputs) ([]preparedInput, error) {
	count := caps.MaxFileCount()
	if len(paths) > count {
		return nil, userErr("--attach: %d files, and workflows.inputs.max_files allows %d for one launch", len(paths), count)
	}
	perFile := caps.MaxFileBytes()
	out := make([]preparedInput, 0, len(paths))
	seen := map[string]bool{}
	var total int64
	for _, p := range paths {
		name := filepath.Base(p)
		if problem := task.InputNameProblem(name); problem != "" {
			return nil, userErr("--attach %s: its name %s", p, problem)
		}
		if seen[name] {
			// Refused rather than overwritten: two paths with one base name
			// would be one object and one entry, and the second upload would
			// be a create-only write against an object already up.
			return nil, userErr("--attach: %s is named twice (both as %s); rename one of them or attach them from different directories", name, name)
		}
		seen[name] = true
		st, err := os.Stat(p)
		switch {
		case err != nil:
			return nil, userErr("--attach %s: %v", p, err)
		case st.IsDir():
			return nil, userErr("--attach %s is a directory; attach files, one path each", p)
		case !st.Mode().IsRegular():
			// A device, a FIFO, a socket: os.Stat describes it, and the size
			// it reports (0 for /dev/zero, as for most of them) is a lie about
			// what uploading it would read. Attaching one would stream an
			// unbounded, non-file stream into the run's bucket and record the
			// size the stat claimed, so it is refused here, before anything is
			// read or written — the same rule the directory above follows, and
			// the one blobx.PutFile's own stat cannot make for us: it never
			// sees the path's mode until it has opened it.
			return nil, userErr("--attach %s is %s, not a regular file; --attach uploads files, one path each", p, st.Mode())
		}
		if st.Size() > perFile {
			return nil, userErr("--attach %s is %s, and workflows.inputs.max_bytes allows %s per file (of %d attached)", p, attachBytes(st.Size()), attachBytes(perFile), len(paths))
		}
		total += st.Size()
		out = append(out, preparedInput{path: p, name: name, size: st.Size()})
	}
	if set := caps.MaxSetBytes(); total > set {
		return nil, userErr("--attach: %d files totalling %s, and workflows.inputs.max_total_bytes allows %s for the set", len(paths), attachBytes(total), attachBytes(set))
	}
	return out, nil
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
	if _, err := attachPrepared(paths, caps); err != nil {
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
// Three arms, all fail-closed. A resolved recipe of another mode is refused
// with its name and its mode. A launch that leaves the recipe to the runner
// (runnerChooses: no recipe in the task, not a follow-up, no checkout here)
// is refused too, because this CLI cannot prove the mode the runner will
// pick — it takes agent.recipe from fugaro.yaml at the ref, else the catalog
// default — and being unable to prove it is not a reason to upload the files
// anyway. The message names the fix. And a follow-up the previous run left no
// recipe for is refused with the recipe the runner will really use, since it
// never reads agent.recipe for one (recipeForLaunch's follow-up arm).
func checkAttachRecipe(ctx context.Context, spec *task.Spec) error {
	if spec.Mode == recipe.ModeArtifact {
		return nil
	}
	if runnerChooses(ctx, spec) {
		return userErr("--attach needs an artifact recipe, but this launch leaves the recipe to the runner (agent.recipe in fugaro.yaml at %s, else default), so the CLI cannot tell whether the run it starts is mode: artifact; pass the artifact recipe explicitly with --recipe", spec.Ref)
	}
	rcp := recipeForLaunch(ctx, checkoutConfig(ctx, spec.Repo), spec)
	switch {
	case rcp != nil && rcp.Mode == recipe.ModeArtifact:
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

// attachDirectAnthropic is the name D9's line uses for the Anthropic route
// itself: the destination of every model a provider does not claim.
const attachDirectAnthropic = "Anthropic (direct)"

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
//   - attachDirectAnthropic whenever any of those models is not claimed by
//     one of those providers, which is config.NeedsAnthropicKey's own rule
//     and is the case a default Claude workflow always takes.
//
// The three models are NeedsAnthropicKey's own three, in its own order
// (coder, reviewer, EffectiveBackground) — NOT Agent.PinnedModels, whose
// third entry is the raw Models.Background. Background is empty whenever the
// config leaves it unset, and a run whose coder a provider claims never makes
// an unset-background call to Anthropic at all: config.EffectiveBackground
// substitutes the coder's own model there, so PinnedModels would print
// "Anthropic (direct)" for a run that makes no Anthropic call. The set never
// under-reports either way — an unclaimed model always sets direct — but the
// one line D9 exists for must not name a destination the bytes cannot reach.
//
// The empty set therefore means "no Anthropic path and no claiming
// provider". With these three models it cannot actually arise (an unclaimed
// one is an Anthropic path); the branch is defensive, and
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
		names = append(names, attachDirectAnthropic)
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
// models and no Anthropic path. It says the bytes stay on this machine
// because in that case that is true, and it is the only case where it is.
func attachProviderWarning(warn io.Writer, providers []string) {
	if len(providers) == 0 {
		fmt.Fprintln(warn, "--attach: the attached files reach the model, and the workflow's models route nowhere: no provider claims them and no Anthropic route is configured, so the bytes stay on this machine.")
		return
	}
	fmt.Fprintf(warn, "--attach: the attached files reach the model, and so reach %s. Attaching a file is sending it there, under the same policy the workflow's own model calls already run under.\n",
		strings.Join(providers, ", "))
}
