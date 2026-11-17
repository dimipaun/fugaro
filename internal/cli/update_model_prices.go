package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// The old cache-price key names, rewritten in place by `fugaro update`
// (docs/spend-accuracy/implementation-plan.md SA4.2, D2646; R4).
//
// The three old keys keep their meaning — SA4.1's loader folds each of them into
// the multiplier every reader below the config package reads — so nothing here
// changes a price and nothing is refused. What changes is the file: the names
// this release documents are cache_read_mult, cache_write_5m_mult and
// cache_write_1h_mult, and a file still written the old way says a price in a
// vocabulary a reader of a newer release has to be told about.
//
// It is a stage of the door rather than another write of the local config,
// because the config's own writer marshals the struct and MarshalYAML keeps the
// spelling the owner chose — that is what makes an unrelated `fugaro update`
// harmless to a file a person edits by hand — so a rename has to happen in the
// bytes, through internal/yamledit, the way the policy generation and the
// checkout's pin are recorded. The plan places it before the policy stage
// (SA4.2), and what that order buys is the config and not the node: the rename
// is applied to the config the run holds, so every write a later stage makes
// from that config — the local-config write and the policy node among them —
// is made from the renamed copy rather than from the bytes read at boot. The
// node itself is the same either way: its prices are the folded multipliers,
// with no key name on them (policyPrices), so it never carried the old spelling
// for this stage to fix.
//
// Nothing is asked (CLAUDE.md: fugaro asks for nothing). No price moves — the
// value each key held is the value the new name holds — so it is a
// SelfConfirming stage whose confirmation is the run's own, and a LocalUnderAgent
// one, because every write of it is on this machine. A comment written on one of
// the renamed keys goes with the old key (yamledit's SetScalar and RemoveKey are
// the two edits that rename one, and neither carries a comment across); every
// other byte of the file, the comments the file wrote around the entry among
// them, stays where it was, and the line the run prints says which of the two it
// is.

// modelPriceRename is one key this run rewrites: the model whose entry writes
// it, and the two names and the value localcfg's own spelling table gives.
type modelPriceRename struct {
	model string
	localcfg.CacheKeyRename
}

// modelPriceRenames is every rename the config in hand holds, models sorted and
// the three dimensions in the order localcfg lists them, so the plan, the write
// and the line it prints are one order and a test can read them.
func modelPriceRenames(c *localcfg.Config) []modelPriceRename {
	if c == nil {
		return nil
	}
	var out []modelPriceRename
	for _, id := range slices.Sorted(maps.Keys(c.ModelPrices)) {
		mp := c.ModelPrices[id]
		for _, r := range mp.LegacyCacheKeys() {
			out = append(out, modelPriceRename{model: id, CacheKeyRename: r})
		}
	}
	return out
}

// modelPriceRenamesText is the renames of one run as the file's paths, for the
// plan's line and the warning about a file this package cannot splice.
func modelPriceRenamesText(renames []modelPriceRename) string {
	names := make([]string, 0, len(renames))
	for _, r := range renames {
		names = append(names, fmt.Sprintf("model_prices.%s.%s -> %s", r.model, r.Old, r.Mult))
	}
	return strings.Join(names, ", ")
}

// modelPricesPlan is one read of the owner's config and the renames it holds:
// what the check reports and what the plan shows. The read is taken once for
// the loop's three phases (plan caches it), and the apply reads the file AGAIN
// rather than working from this one: the images stage writes this same file
// earlier in the run, so the bytes the plan was made from are not the bytes the
// write goes into.
type modelPricesPlan struct {
	path    string
	renames []modelPriceRename
	// skip is a sentence for a state with nothing to do that is worth saying,
	// and it is the stage's whole answer then.
	skip string
}

// modelPricesStage is the stage itself.
type modelPricesStage struct {
	e       *initEngine
	planned bool
	p       *modelPricesPlan
	err     error
	// wrote is what the apply actually renamed, for the verify: an apply that
	// renamed nothing claims nothing, whether the file was already converged or
	// holds a shape this package refuses to splice.
	wrote []modelPriceRename
}

func newModelPricesStage(e *initEngine) *modelPricesStage { return &modelPricesStage{e: e} }

func (modelPricesStage) Name() string     { return initflow.ModelPrices }
func (modelPricesStage) SelfConfirming()  {}
func (modelPricesStage) LocalUnderAgent() {}

func (modelPricesStage) Left() initflow.Left {
	return promptLeft(initflow.ModelPrices, "run the command below: it asks you nothing and renames the cache prices in your own config file", "update")
}

// plan reads the owner's config once per run.
func (s *modelPricesStage) plan() (*modelPricesPlan, error) {
	if !s.planned {
		s.planned = true
		s.p, s.err = modelPricesPlanFor(s.e.path)
	}
	return s.p, s.err
}

func modelPricesPlanFor(path string) (*modelPricesPlan, error) {
	p := &modelPricesPlan{path: path}
	if path == "" {
		p.skip = "this run has no local config file of its own (a shared config has none), so there is no cache price to rename"
		return p, nil
	}
	data, err := os.ReadFile(path)
	// A file that is not there yet is the empty case and not a failure: the path
	// this run holds is the one the installation stage writes the config to, so a
	// first run — and a wizard run, and a checkout whose project has no local
	// config yet — reaches this stage before the file exists. Nothing can be
	// written the old way in a file that holds nothing, and the stage says so
	// rather than stopping the run it is one step of.
	if errors.Is(err, fs.ErrNotExist) {
		p.skip = fmt.Sprintf("%s is not there yet, so this run has no cache price to rename", path)
		return p, nil
	}
	if err != nil {
		return nil, userErr("reading %s: %v", path, err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		return nil, userErr("%s: %v", path, err)
	}
	p.renames = modelPriceRenames(lc)
	return p, nil
}

// converged is the sentence for a file that writes every cache price under the
// name this release documents, which is the state a converged installation is
// in on every run after the first.
func (p *modelPricesPlan) converged() string {
	return fmt.Sprintf("%s writes every cache price under the name this release documents", p.path)
}

func (s *modelPricesStage) Check(context.Context) (initflow.Status, error) {
	p, err := s.plan()
	if err != nil {
		return initflow.Status{}, err
	}
	switch {
	case p.skip != "":
		return initflow.Status{State: initflow.Skipped, Detail: p.skip}, nil
	case len(p.renames) == 0:
		return initflow.Status{State: initflow.Done, Detail: p.converged()}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("%s writes %d cache price(s) under an old key name: this run renames %s in place, keeping the prices as they are",
		p.path, len(p.renames), modelPriceRenamesText(p.renames))}, nil
}

func (s *modelPricesStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	p, err := s.plan()
	if err != nil {
		return initflow.Plan{}, err
	}
	if p.skip != "" {
		return initflow.Plan{NothingToDo: true, Detail: p.skip}, nil
	}
	if len(p.renames) == 0 {
		return initflow.Plan{NothingToDo: true, Detail: p.converged()}, nil
	}
	return initflow.Plan{Detail: fmt.Sprintf("rename %s in %s, keeping the prices as they are and backing the file up first",
		modelPriceRenamesText(p.renames), p.path)}, nil
}

// Apply is the write, and it is the whole of this stage: no confirmation, no
// cloud, and nothing that can fail the run over a file shape it cannot splice.
func (s *modelPricesStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	p, err := s.plan()
	if err != nil {
		return initflow.Outcome{}, err
	}
	if p.skip != "" {
		return initflow.Outcome{Detail: p.skip}, nil
	}
	wrote, refused, err := renameModelPriceKeys(s.e.r, s.e.lc, p.path)
	switch {
	case err != nil:
		return initflow.Outcome{}, err
	case refused:
		return initflow.Outcome{Detail: fmt.Sprintf("left the cache prices in %s as they are: this run could not rewrite that file in place", p.path)}, nil
	case len(wrote) == 0:
		return initflow.Outcome{Detail: p.converged()}, nil
	}
	s.wrote = wrote
	return initflow.Outcome{Changed: true, Detail: fmt.Sprintf("renamed %s in %s", modelPriceRenamesText(wrote), p.path)}, nil
}

// Verify reads the file back: a rename this run claimed has to be in it, and a
// stage that reported one it never made would leave the key the run says it
// renamed on the screen while the file keeps writing it — the same state
// reported converged on every run that follows.
func (s *modelPricesStage) Verify(context.Context) error {
	if len(s.wrote) == 0 {
		return nil
	}
	left, err := legacyModelPriceKeys(s.e.path)
	if err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("%s still writes %s, which this run reported renaming", s.e.path, modelPriceRenamesText(left))
	}
	return nil
}

// legacyModelPriceKeys is what the file at path writes under an old key name
// now, read from the file itself.
func legacyModelPriceKeys(path string) ([]modelPriceRename, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s back: %w", path, err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s no longer parses after the rename: %w", path, err)
	}
	return modelPriceRenames(lc), nil
}

// renameModelPriceKeys rewrites path's old cache prices into the names this
// release documents, in place and in one write: every splice is composed in
// memory, the file the run replaces is backed up first, and the write is
// atomic — the same triple the local config's own writer, the policy record and
// the layer generation take.
//
// A shape internal/yamledit refuses to splice (a cache price written inside a
// flow mapping, say) is a WARNING and not this stage's failure — refused comes
// back true and the file is left as it is: the old names keep their meaning
// (SA4.1 folds them), so it is a file an owner can leave, and `fugaro update`
// is the whole upgrade — a run that stopped here would be asking for a second
// command over a rename nothing depends on. What the owner reads names the keys
// and the fix.
//
// The rename is also applied to the config the run holds (lc), so every later
// write of this file — the run's own local-config write, and the policy node
// this same run publishes from that config — keeps the name just written
// instead of putting the old one back out of the copy read at boot.
func renameModelPriceKeys(r *initRun, lc *localcfg.Config, path string) (wrote []modelPriceRename, refused bool, err error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, false, userErr("reading %s: %v", path, err)
	}
	parsed, err := localcfg.Parse(old)
	if err != nil {
		return nil, false, userErr("%s: %v", path, err)
	}
	renames := modelPriceRenames(parsed)
	if len(renames) == 0 {
		return nil, false, nil
	}
	out := old
	for _, want := range renames {
		value := strconv.FormatFloat(want.Value, 'f', -1, 64)
		if out, err = yamledit.SetScalar(out, []string{"model_prices", want.model, want.Mult}, value); err == nil {
			out, err = yamledit.RemoveKey(out, []string{"model_prices", want.model, want.Old})
		}
		if err != nil {
			r.warn(fmt.Sprintf("%s writes its cache prices under the old key names (%s), and this run could not rewrite the file in place: %s. The old names keep their meaning, so the prices are as the owner set them; rename them by hand to the names this release documents (%s)",
				path, modelPriceRenamesText(renames), oneLineCLI(err.Error()), "cache_read_mult, cache_write_5m_mult, cache_write_1h_mult"))
			return nil, true, nil
		}
	}
	if !yamledit.Changed(old, out) {
		return nil, false, nil
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return nil, false, userErr("backing up %s before renaming its cache prices: %v", path, err)
	}
	if err := writeFileAtomic(path, out); err != nil {
		return nil, false, userErr("renaming the cache prices in %s: %v", path, err)
	}
	for _, id := range modelsRenamed(renames) {
		mp, ok := lc.ModelPrices[id]
		if !ok {
			continue
		}
		mp.RenameLegacyCacheKeys()
		lc.ModelPrices[id] = mp
	}
	fmt.Fprintf(r.stageDetail(), "renamed %s in %s: every price is the one the owner set, and a comment written on one of the renamed keys went with it (the file this run replaced is at %s)\n",
		modelPriceRenamesText(renames), path, backup)
	return renames, false, nil
}

// modelsRenamed is the models a run's renames belong to, each once and in the
// renames' own order.
func modelsRenamed(renames []modelPriceRename) []string {
	var out []string
	for _, r := range renames {
		if !slices.Contains(out, r.model) {
			out = append(out, r.model)
		}
	}
	return out
}
