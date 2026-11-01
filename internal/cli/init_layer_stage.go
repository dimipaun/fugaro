package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The project layer as a stage of the converge (D830/D831), and the one local
// layer file beside the project config: `fugaro setup` and `fugaro update` seed
// the file from the published object when there is none, publish the file when
// it differs from the published one, and record the generation the file was
// written from (D854).
//
// It is the object every repository of the project resolves against, so the
// publish is a confirmation of its own (the stage is SelfConfirming) and the
// executable-key gate is the one publishLayer already holds: `fugaro update`
// has no --executable-changes, exactly as the door that owns that flag is the
// one that takes it.
//
// The seeding half exists for the same rule the policy stage does: `fugaro
// update` is the whole upgrade. Without it, an installation whose project
// publishes a layer ends the one command with no file at the path D830 names,
// and the owner's next step is a file they have never seen.

// layerSeedWhat, layerPublishWhat and layerReseedWhat are the three things this
// stage asks before it writes, in the words the operator reads.
const (
	layerSeedWhat    = "this writes your local layer file from the published project layer: it is the file you edit, and the file fugaro update publishes"
	layerPublishWhat = "this rewrites the published project layer from your local layer file: every repository of the project resolves against it, and so does every run of them"
	layerReseedWhat  = "this re-seeds your local layer file from the published project layer: the file you have now is kept beside it"
)

// layerStage is the stage itself.
type layerStage struct {
	e       *initEngine
	planned bool
	p       *layerPlan
	err     error
}

func newLayerStage(e *initEngine) *layerStage { return &layerStage{e: e} }

func (layerStage) Name() string    { return initflow.Layer }
func (layerStage) SelfConfirming() {}

func (layerStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Layer, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// layerPlan is one read of the project layer, the local layer file and the
// generation this machine's config records, and what this run does about them.
// The read is taken once and kept for the whole run (plan): the check, the plan
// the operator is shown and the apply all work from it, and the write is
// conditional on the generation THAT read returned, so a publisher that landed
// in between is refused rather than overwritten.
//
// The bucket handle itself is not kept: what a write needs from the read is the
// bytes and the generation (layerAt), and a handle held across three phases of
// the loop would have to be closed by whichever of them ran last.
type layerPlan struct {
	lc         *localcfg.Config
	url        string // the runs bucket, as this machine reads it
	path       string // the one local layer file (D830)
	configPath string // the local project config the generation is recorded in ("" when there is none)

	exists bool
	local  []byte
	l      *config.ProjectLayer // the local file parsed, when it exists

	published []byte
	pubExists bool
	gen       int64

	// recorded is the generation this machine's config records as the one its
	// layer file was written from ("" when it records none).
	recorded string

	// skip is a sentence for a state with nothing to do that is worth saying,
	// and it is the stage's whole answer then.
	skip string
	// seed: there is no local file and the project has published one.
	seed bool
	// publish: the local file is there and differs from the published object.
	publish bool
	// recordGen: the file and the object agree and the record is not the
	// generation the file is written from.
	recordGen bool
	// adopted: nothing was recorded, so this run records the generation it
	// publishes as the one the file is written from (D854's first-run answer).
	adopted bool
	// stale: D854's refusal — the published object has moved past the
	// generation this machine's file was written from, so publishing would
	// replace someone else's newer layer with this older file.
	stale *layerGenerationState
}

// layerGenerationState is D854's third value for the layer: what this machine's
// config records, and what the object is at, told apart by the record.
type layerGenerationState struct {
	recorded  string
	published int64
}

func (s layerGenerationState) text() string {
	return fmt.Sprintf("generation %s recorded for this machine's config, generation %d published at %s", generationText(s.recorded), s.published, config.LayerKey)
}

// plan reads the object, the file and the record once per run.
func (s *layerStage) plan(ctx context.Context) (*layerPlan, error) {
	if !s.planned {
		s.planned = true
		s.p, s.err = layerPlanFor(ctx, s.e.lc)
	}
	return s.p, s.err
}

// layerPlanFor reads the state and decides what this run does about it. A
// refusal here is the run's (a local file that does not parse is an edit the
// operator has to fix before anything can be published from it); a bucket that
// cannot be read is not, and is reported as the skipped stage it is.
func layerPlanFor(ctx context.Context, lc *localcfg.Config) (*layerPlan, error) {
	p := &layerPlan{lc: lc, url: recordReadURL(lc)}
	path, err := localcfg.LayerFile(os.Getenv, lc.Name)
	if err != nil {
		return nil, userErr("the project layer: %v", err)
	}
	p.path, p.configPath, p.recorded = path, policyConfigPath(lc), lc.LayerGeneration
	// The same read the door makes (readLayerFile, config.ReadRegular): a
	// symlink or an oversized file is refused here as it is there, so the file
	// this stage publishes is the file `fugaro config publish` does, and a file
	// only one of the two would read cannot make the two disagree.
	data, rerr := readLayerFile(path)
	switch {
	case rerr == nil:
		p.exists, p.local = true, data
	case errors.Is(rerr, os.ErrNotExist):
	default:
		return nil, userErr("reading the local layer file %s: %v", path, oneLineCLI(rerr.Error()))
	}
	if p.exists {
		l, ps := projectLayerProblems(p.local, lc)
		if len(ps) > 0 {
			return nil, userErr("%s is invalid, so nothing of it can be published: %s", path, pluginwire.Printable(layerProblemsText(ps)))
		}
		p.l = l
	}
	data, gen, exists, rerr := layerPublishedRead(ctx, lc, p.url)
	switch {
	case rerr != nil:
		// The bucket this run would publish into cannot be read, so nothing of
		// this stage can be decided: the published bytes are what a seed writes
		// and what a publish is compared against. It is a skip and not the
		// run's failure for the reason the layer is best effort everywhere else
		// (findLayer's own fallback, doctor's local-layer-file check): the
		// state it leaves is a local file the owner can still edit, and
		// `fugaro config layer` and doctor report the same thing the next time
		// the bucket answers.
		p.skip = fmt.Sprintf("the project layer's bucket (%s) could not be read, so this run leaves the project layer and its local layer file as they are: %s", p.url, oneLineCLI(rerr.Error()))
		return p, nil
	case exists:
		p.published, p.pubExists, p.gen = data, true, gen
	}
	recorded, rerr := strconv.ParseInt(p.recorded, 10, 64)
	hasRecord := p.recorded != "" && rerr == nil
	switch {
	case !p.exists && !p.pubExists:
		p.skip = fmt.Sprintf("there is no local layer file at %s and this project has published no layer: there is nothing to seed the file from and nothing to publish", p.path)
	case !p.exists:
		p.seed = true
	case !p.pubExists:
		p.publish = true
	case bytes.Equal(p.local, p.published):
		p.recordGen = !hasRecord || recorded != gen
	default:
		// Differing bytes are never the whole answer (D854): a record that is
		// older than the published generation means another machine published
		// after this file was written from the object, and publishing would
		// replace it — the owner decides, and the remedy is the re-seed, not
		// this file's bytes.
		if hasRecord && recorded < gen {
			p.stale = &layerGenerationState{recorded: p.recorded, published: gen}
			break
		}
		p.publish = true
		p.adopted = !hasRecord
	}
	return p, nil
}

// layerPublishedRead reads the published object for the stage; a var because a
// file:// or mem:// bucket reports generation 0 for every object, and the state
// D854's refusal exists for — a published generation ahead of the record — is
// one only a test can hand the stage.
var layerPublishedRead = readPublishedLayer

// readPublishedLayer reads the canonical project layer object from url, the
// runs bucket as this machine reads it. exists is false — with no error — when
// the project has published none, which is a state of its own and not a
// failure.
func readPublishedLayer(ctx context.Context, lc *localcfg.Config, url string) (data []byte, gen int64, exists bool, err error) {
	if fakeEndpointsOnGS(lc, url) {
		return nil, 0, false, errors.New("the local config's storage endpoint is a fake, so the real bucket is not read")
	}
	b, err := openRecordBucket(ctx, url)
	if err != nil {
		return nil, 0, false, err
	}
	defer b.Close()
	rctx, cancel := context.WithTimeout(ctx, layerBucketTimeout)
	defer cancel()
	data, gen, err = b.ReadMaxStrict(rctx, config.LayerKey, config.LayerMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, false, nil
	case err != nil:
		return nil, 0, false, err
	}
	return data, gen, true, nil
}

// Check reports where the project layer and its local file stand. A bucket it
// cannot read is a skip with the reason, never a failed run.
func (s *layerStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.plan(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	switch {
	case p.skip != "":
		return initflow.Status{State: initflow.Skipped, Detail: p.skip}, nil
	case p.seed:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("there is no local layer file at %s: this run seeds it from the published project layer (generation %d)", p.path, p.gen)}, nil
	case p.stale != nil:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published project layer has moved past the generation this machine's layer file was written from (%s): this run re-seeds the file, after your confirmation", p.stale.text())}, nil
	case p.publish:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the local layer file %s is unpublished: this run publishes it, after your confirmation", p.path)}, nil
	case p.recordGen:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("this machine's config records %s as the generation its layer file was written from: this run records %d", generationText(p.recorded), p.gen)}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the local layer file %s is the published project layer (generation %d)", p.path, p.gen)}, nil
}

// Plan is what the run says it is about to do, and it prints the three-way
// state of D854's refusal before asking: the confirmation for a re-seed is made
// against both files, and one the operator cannot see is one they cannot judge.
func (s *layerStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := s.plan(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	if p.skip != "" {
		return initflow.Plan{NothingToDo: true, Detail: p.skip}, nil
	}
	if p.stale != nil && (env.Interactive || env.Yes) {
		p.printReseedState(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: p.planDetail()}, nil
}

// planDetail is what the stage says it is about to do, in the wording that fits
// this run's plan.
func (p *layerPlan) planDetail() string {
	switch {
	case p.seed:
		return fmt.Sprintf("seed %s from the published project layer (generation %d), after your confirmation", p.path, p.gen)
	case p.stale != nil:
		return fmt.Sprintf("re-seed %s from the published project layer, after your confirmation", p.path)
	case p.publish:
		return fmt.Sprintf("publish the project layer from %s, after your confirmation", p.path)
	case p.recordGen:
		return fmt.Sprintf("record the published project layer's generation (%d) in %s", p.gen, p.configPath)
	}
	return fmt.Sprintf("the local layer file is the published project layer (generation %d)", p.gen)
}

// Apply is the write. A coding agent's session never reaches it: the stage can
// publish the object every repository of the project resolves against, and that
// is a cloud write like any other.
func (s *layerStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	p, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	switch {
	case p.skip != "":
		return initflow.Outcome{Detail: p.skip}, nil
	case p.seed:
		return s.seed(env, p)
	case p.stale != nil:
		return s.reseed(env, p)
	case p.publish:
		return s.publish(ctx, env, p)
	case p.recordGen:
		if err := layerGenerationRecord(s.e.r.cmd.OutOrStdout(), p.configPath, p.lc, p.gen); err != nil {
			return initflow.Outcome{}, err
		}
		return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("recorded the published project layer's generation (%d) in %s", p.gen, p.configPath)}, nil
	}
	return initflow.Outcome{Detail: fmt.Sprintf("the local layer file is the published project layer (generation %d)", p.gen)}, nil
}

// seed writes the published object's bytes to the local layer file, which is
// what makes the file usable on an installation that has never had one. It
// writes only this machine's file: the object is left exactly as it is.
func (s *layerStage) seed(env initflow.Env, p *layerPlan) (initflow.Outcome, error) {
	cmd := s.e.r.cmd
	if err := confirmRemoval(cmd, p.lc, env.Yes, layerSeedWhat); err != nil {
		return initflow.Outcome{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return initflow.Outcome{}, userErr("seeding %s: %v", p.path, err)
	}
	if err := writeFileAtomic(p.path, p.published); err != nil {
		return initflow.Outcome{}, userErr("seeding %s: %v", p.path, err)
	}
	if err := layerGenerationRecord(cmd.OutOrStdout(), p.configPath, p.lc, p.gen); err != nil {
		return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("seeded %s from the published project layer", p.path)}, err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "seeded %s from the published project layer (generation %d, sha256 %s): edit it and run %s to publish it\n",
		p.path, p.gen, config.LayerSum(p.published), selfCommand()+" update")
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("seeded %s from the published project layer (generation %d)", p.path, p.gen)}, nil
}

// publish writes the local layer file to the canonical object, against the read
// this run took (so a publisher that landed in between is refused, not
// overwritten), and fans the object out to every repository of the installation
// the way the explicit door does.
//
// The confirmation comes before publishLayer prints its diff: the diff is what
// the run REPORTS it replaced, and the one thing a confirmation withholds is a
// write the operator did not agree to — which publishLayer's own executable-key
// gate still withholds afterwards, with the banner that names the keys.
func (s *layerStage) publish(ctx context.Context, env initflow.Env, p *layerPlan) (initflow.Outcome, error) {
	cmd := s.e.r.cmd
	if err := confirmRemoval(cmd, p.lc, env.Yes, layerPublishWhat); err != nil {
		return initflow.Outcome{}, err
	}
	b, err := openRecordBucket(ctx, p.url)
	if err != nil {
		return initflow.Outcome{}, remote(fmt.Errorf("opening %s to publish the project layer: %w", p.url, err))
	}
	defer b.Close()
	at := &layerAt{old: p.published, gen: p.gen, exists: p.pubExists}
	rec := layerRecord{path: p.configPath, lc: p.lc}
	gen, err := publishLayer(ctx, cmd.ErrOrStderr(), b, p.l, false, at, rec)
	if err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "published the project layer of %s (generation %d, sha256 %s) from %s\n", p.lc.Name, gen, p.l.SHA256, p.path)
	if p.adopted {
		// D854's first-run answer: an installation that records no generation
		// has no baseline to compare against, so this run records the one it
		// publishes as the generation the file is written from — and says so,
		// because the record that appears in the owner's config afterwards is
		// what their next edit is read against.
		fmt.Fprintf(cmd.OutOrStdout(), "this machine's config records no generation for its layer file, so this run adopts the one it publishes (%d) as the generation the file is written from\n", gen)
	}
	// The fan-out needs the project's repositories and the bucket this run
	// already holds: opening a backend it never calls would make the layer
	// depend on the compute platform being reachable, which the run's own
	// stages answer for.
	failed := fanOutLayer(ctx, cmd.OutOrStdout(), &cloudEnv{lc: p.lc, bucket: b}, p.l, gen)
	if failed > 0 {
		return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("published the project layer (generation %d)", gen)},
			remote(fmt.Errorf("the project layer is published, but %d repository copy(ies) failed (listed above); run %s again", failed, selfCommand()+" update"))
	}
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("published the project layer from %s (generation %d)", p.path, gen)}, nil
}

// printReseedState is D854's three-way state: the file this machine has, the
// generation it was written from, and the object published now — the owner
// decides which of the two files to keep, and neither is a figure they hold.
func (p *layerPlan) printReseedState(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, "the published project layer has moved past the generation this machine's layer file was written from, so publishing from here would replace it with an older file: %s\n", p.stale.text())
	fmt.Fprintf(w, "  this machine's layer file %s (sha256 %s)\n", p.path, config.LayerSum(p.local))
	fmt.Fprintf(w, "  the published project layer (generation %d, sha256 %s)\n", p.gen, config.LayerSum(p.published))
	fmt.Fprintf(w, "  re-seeding writes the published object's bytes into %s instead (the file is backed up first); publishing stays yours to do afterwards\n", p.path)
}

// reseed is D854's remedy: the operator reads the three-way state and, on
// confirming it, gets this machine's layer file written from the published
// object — backed up first — instead of the object being overwritten with what
// the older file says.
func (s *layerStage) reseed(env initflow.Env, p *layerPlan) (initflow.Outcome, error) {
	cmd := s.e.r.cmd
	out := cmd.OutOrStdout()
	p.printReseedState(out)
	if err := confirmRemoval(cmd, p.lc, env.Yes, layerReseedWhat); err != nil {
		return initflow.Outcome{}, err
	}
	backup, err := backupFile(p.path, p.local, time.Now())
	if err != nil {
		return initflow.Outcome{}, userErr("backing up %s before re-seeding it: %v", p.path, err)
	}
	if err := writeFileAtomic(p.path, p.published); err != nil {
		return initflow.Outcome{}, userErr("re-seeding %s: %v", p.path, err)
	}
	if err := layerGenerationRecord(out, p.configPath, p.lc, p.gen); err != nil {
		return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("re-seeded %s from the published project layer", p.path)}, err
	}
	fmt.Fprintf(out, "re-seeded %s from the published project layer (generation %d, sha256 %s); the file this run replaced is at %s\n",
		p.path, p.gen, config.LayerSum(p.published), backup)
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("re-seeded %s from the published project layer (generation %d)", p.path, p.gen)}, nil
}

// Verify reads back what this run wrote: the object for a publish, the file for
// a seed or a re-seed, and the record for a run whose only write was the record.
// A write that claims bytes it did not land is a layer this Fugaro would keep
// publishing on every run, and a record that did not land is the same file
// reported as a local edit on every door that reads it.
func (s *layerStage) Verify(ctx context.Context) error {
	p, err := s.plan(ctx)
	if err != nil || p == nil {
		return err
	}
	switch {
	case p.skip != "":
		return nil
	case p.recordGen:
		// The plan's own recordGen is what Apply answered: the bytes already
		// agree, so the only write this run claimed is the record, and reading
		// the config back is the only way to see it landed (the layer file and
		// the object would say the same thing either way).
		data, err := os.ReadFile(p.configPath)
		if err != nil {
			return fmt.Errorf("the generation was recorded but %s cannot be read back: %w", p.configPath, err)
		}
		lc, err := localcfg.Parse(data)
		if err != nil {
			return fmt.Errorf("the generation was recorded but %s no longer parses: %w", p.configPath, err)
		}
		if want := strconv.FormatInt(p.gen, 10); lc.LayerGeneration != want {
			return fmt.Errorf("%s records generation %s for its layer file, and this run recorded %s", p.configPath, generationText(lc.LayerGeneration), want)
		}
		return nil
	case p.seed, p.stale != nil:
		data, err := os.ReadFile(p.path)
		if err != nil {
			return fmt.Errorf("the local layer file was written but cannot be read back: %w", err)
		}
		if !bytes.Equal(data, p.published) {
			return errors.New("the local layer file is not the published project layer it was written from")
		}
		return nil
	case p.publish:
		data, gen, exists, err := layerPublishedRead(ctx, p.lc, p.url)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("the published project layer is gone")
		}
		if !bytes.Equal(data, p.local) {
			return fmt.Errorf("the project layer was published but the object at %s is not the local layer file", config.LayerKey)
		}
		_ = gen
	}
	return nil
}
