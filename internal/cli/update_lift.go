package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/lift"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/repofiles"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// update_lift.go is the lift stage (L8) of the converge: `fugaro update`'s
// migration of what a repository's own file declares into the project the run
// is upgrading (docs/repo-override-rulings, D3305 and D3306; the contract is
// internal/lift, C2, and the plan's M2 is this file).
//
// # Say first, then write, once
//
// The stage has two halves and the order between them is the whole of what it
// promises. It SAYS first: it runs every registered planner over the
// repositories it could list and prints every line of every change — one line
// per repository whose runs change, each admit pattern it could not expand and
// each repository it could not read, whose lifted values are dropped and never
// moved (D3306) — and it writes nothing until every line of every change is on
// screen. Then it WRITES: the layer file's edits, which the Layer stage that
// runs next publishes, the store objects, and the database nodes, each behind
// the same confirmation every other stage's write passes (confirmStageWrite).
//
// It asks nothing of its own: the confirmations above are the gate every
// stage's write goes through, and no value is typed anywhere in this file
// (docs/no-prompt-policy; T7).
//
// # Once per repository (D3305)
//
// The migration is once per repository: a repository whose record
// (lift.RecordPath) is already in the runs bucket is not migrated again, and
// the planners are told so through lift.Project.Migrated. The record itself is
// written by the ENGINE and not here (writeLiftRecords), after the loop: a
// record claims that a repository's migration has landed, and what lands the
// layer half of it is the Layer stage's publish, which runs after this stage.
// A publish that was refused, skipped or never reached therefore writes no
// record, and the next update migrates the repository again.
//
// # Why the layer half is a file edit
//
// The lifted values belong to the project (B3, B9, B11), and the project's
// values live in the local layer file, which the Layer stage publishes: this
// stage edits the file and forgets the layer stage's cached plan of it
// (layerStage.forget), so the publish that follows is made from the document
// this run just wrote rather than from the one it read before.
//
// # What it does when it cannot read something
//
// The stage is fail-open about the migration and fail-closed about the record,
// which is D3306's own direction: a bucket or a repository it cannot read is
// named, nothing of that repository is moved, and the run goes on. A planner
// that fails drops its own change, prints the reason, and withholds every
// record of the run — the migration is per repository and not per planner, so
// a repository half migrated must not be recorded as done; the next update
// retries it.

// The three things this stage's writes are, in the words the operator reads
// above the confirmation.
const (
	liftLayerWhat = "this writes what your repositories declare into your local layer file: the Layer stage of this run publishes it, and every repository of the project resolves against the published layer"
	liftStoreWhat = "this writes the recipes your repositories keep into the project's recipe store: a repository of the project that names one resolves the copy in the store from then on"
	liftDBWhat    = "this lowers the repository caps this project sets above its ceiling: a lease of such a repository is compared against the lower figure, and a run meets it at its next lease"
)

// liftKeyRead is the command the credential refusal names when this stage
// cannot read a repository's key (refuseKeyReadAt): the read is the update's
// own, so the fix it names is the command the operator is already running.
const liftKeyRead = "fugaro update"

// liftPlanners and liftRepositories are the two things the stage reads the
// world through, and both are seams for the same reason: a test has neither a
// registered planner that produces a change (a fixture decides what each says) nor an account to read a
// repository with. Production reads the registry and repofiles.Read.
var (
	liftPlanners     = lift.Planners
	liftRepositories = readLiftRepositories
)

// liftStage is the stage itself.
type liftStage struct {
	e       *initEngine
	planned bool
	p       *liftPlan
	err     error

	// said is the once-guard of the lines: Check runs twice in an applying run
	// (the preview's read-only pass and the execution pass), the plan behind
	// them is read once, and the lines are the one thing here that must not be
	// said twice.
	said bool

	// records is the slugs whose migration this run MAY record, and the engine
	// writes them after the loop (writeLiftRecords) once the Layer stage's
	// publish of them has landed. A write that did not land clears it, and so
	// does a planner that failed.
	records []string
}

func newLiftStage(e *initEngine) *liftStage { return &liftStage{e: e} }

func (liftStage) Name() string { return initflow.Lift }

// SelfConfirming says this stage's writes are ordinary ones that a run
// carrying its own answer may make (the loop's apply gate): the file edit is
// this machine's own, and the store and the database are written from the
// project's own local config behind the confirmations above, which are the
// same three-shape gate the Layer stage's writes pass.
func (liftStage) SelfConfirming() {}

func (liftStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Lift, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// liftPlan is one read of everything the migration is planned from — the
// repositories, the project's layer, store, caps and price table, and which
// repositories are already migrated — and the whole of what this run does
// about them. It is read once (plan) and the check, the plan the operator is
// shown and the apply all work from it; the bucket handle is not kept, because
// a handle held across three phases of the loop would have to be closed by
// whichever of them ran last (the Layer stage's own reason).
type liftPlan struct {
	lc  *localcfg.Config
	url string // the runs bucket, as this machine reads it

	layerPath   string // the one local layer file (D830)
	base        []byte // the document the edits are made on
	baseFrom    string // where that document came from, for the write's sentence
	next        []byte // the document the edits produce
	layerExists bool   // the base is this machine's own file
	tree        map[string]any

	migrated map[string]bool   // the slugs whose record is in the bucket (D3305)
	store    map[string]string // the store's recipe names and the sha256 of each

	edits   []lift.LayerEdit
	stored  []lift.StoreWrite
	dbw     []lift.DBWrite
	records []string
	lines   []string

	// skip is a sentence for a state in which this stage has nothing to do,
	// and it is the stage's whole answer then. skipUndone says the skip is not
	// "nothing to do" but "nothing could be decided": the step this stage was
	// run for stays owed (initflow.Status.Undone).
	skip       string
	skipUndone bool
	// empty is a run of the planners that produced nothing to write: every
	// repository of this project is migrated, or none of them declares
	// anything this release lifts.
	empty bool
}

// plan reads the world once per run.
func (s *liftStage) plan(ctx context.Context) (*liftPlan, error) {
	if !s.planned {
		s.planned = true
		s.p, s.err = liftPlanFor(ctx, s.e, s.e.lc)
	}
	return s.p, s.err
}

// liftPlanFor reads the state and runs the planners over it. A refusal here is
// the run's (a local layer file that does not parse, or a planner's edit that
// does not read back, is a document a person has to look at); a bucket nobody
// can read is not, and is reported as the skipped stage it is.
func liftPlanFor(ctx context.Context, e *initEngine, lc *localcfg.Config) (*liftPlan, error) {
	p := &liftPlan{lc: lc}
	if lc == nil {
		p.skip = "no project is selected, so there is nothing to migrate"
		return p, nil
	}
	p.url = recordReadURL(lc)
	if p.url == "" {
		p.skip = "this installation declares no runs bucket, so there is no store to migrate into and no repository to read: nothing is migrated"
		return p, nil
	}
	if fakeEndpointsOnGS(lc, p.url) {
		p.skip = fmt.Sprintf("the runs bucket (%s) is a fake endpoint, so nothing of the migration is read or written", p.url)
		p.skipUndone = true
		return p, nil
	}
	b, err := openRecordBucket(ctx, p.url)
	if err != nil {
		p.skip = fmt.Sprintf("the runs bucket (%s) could not be read, so this run migrates nothing: %s", p.url, oneLineCLI(err.Error()))
		p.skipUndone = true
		return p, nil
	}
	defer b.Close()

	if p.migrated, err = readLiftMigrated(ctx, b); err != nil {
		p.skip = fmt.Sprintf("the migration records in %s could not be listed, so this run cannot tell a migrated repository from one it never read and migrates nothing: %s", p.url, oneLineCLI(err.Error()))
		p.skipUndone = true
		return p, nil
	}
	if p.store, err = readLiftStore(ctx, b); err != nil {
		p.skip = fmt.Sprintf("the project's recipe store could not be read, so a recipe this run would publish cannot be told from one already there and nothing is migrated: %s", oneLineCLI(err.Error()))
		p.skipUndone = true
		return p, nil
	}
	if err := p.readLayer(ctx, lc, b); err != nil {
		return nil, err
	}
	caps, admits := liftProjectRead(ctx, e, lc, p)

	repos, patterns, err := liftRepositories(ctx, e, lc, admits)
	if err != nil {
		p.skip = fmt.Sprintf("the repositories this project can migrate could not be listed, so this run migrates nothing: %s", oneLineCLI(err.Error()))
		p.skipUndone = true
		return p, nil
	}
	p.nameUnlisted(patterns, repos)

	prices, _, perr := priceTable(lc, time.Now())
	if perr != nil {
		p.skip = fmt.Sprintf("the model price table could not be read, so the model choice a repository declares cannot be judged: %s", oneLineCLI(perr.Error()))
		p.skipUndone = true
		return p, nil
	}
	if err := p.runPlanners(lift.Project{
		Layer:    p.tree,
		Recipes:  p.store,
		Caps:     caps,
		Prices:   prices,
		Migrated: p.migrated,
	}, repos); err != nil {
		return nil, err
	}
	if err := p.applyEdits(lc); err != nil {
		return nil, err
	}
	p.empty = len(p.edits) == 0 && len(p.stored) == 0 && len(p.dbw) == 0
	return p, nil
}

// readLayer reads the document the edits are made on: this machine's own layer
// file when it has one, else the published object (the document every
// repository of the project already resolves against), else the shell a first
// publish starts from. A file that is not there is a state and not a failure;
// a file that is there and does not read as a layer is a refusal, because
// nothing can be lifted into a document this CLI would refuse to publish.
func (p *liftPlan) readLayer(ctx context.Context, lc *localcfg.Config, b *blobx.Bucket) error {
	path, err := localcfg.LayerFile(os.Getenv, lc.Name)
	if err != nil {
		return userErr("the project layer: %v", err)
	}
	p.layerPath = path
	p.base, p.baseFrom = layerRigorShell(lc), "a new layer file"
	data, rerr := readLayerFile(path)
	switch {
	case rerr == nil:
		p.base, p.baseFrom, p.layerExists = data, path, true
	case errors.Is(rerr, os.ErrNotExist):
		rctx, cancel := context.WithTimeout(ctx, layerBucketTimeout)
		defer cancel()
		if pub, _, perr := b.ReadMaxStrict(rctx, config.LayerKey, config.LayerMaxBytes); perr == nil {
			p.base, p.baseFrom = pub, config.LayerKey
		}
	default:
		return userErr("reading the local layer file %s: %v", path, oneLineCLI(rerr.Error()))
	}
	if _, ps := projectLayerProblems(p.base, lc); len(ps) > 0 {
		return userErr("%s is invalid, so nothing can be lifted into it: %s", p.baseFrom, pluginwire.Printable(layerProblemsText(ps)))
	}
	if err := yaml.Unmarshal(p.base, &p.tree); err != nil {
		return userErr("%s could not be decoded as a project layer: %v", p.baseFrom, oneLineCLI(err.Error()))
	}
	return nil
}

// nameUnlisted prints D3306's two lines, one per admit pattern this run could
// not expand and one per repository it could not read — before any write, and
// naming what is dropped: the repository's own file values are not moved, and
// only the project's values stand for it.
func (p *liftPlan) nameUnlisted(patterns []string, repos []lift.Repo) {
	for _, pat := range patterns {
		p.lines = append(p.lines, fmt.Sprintf("lift: %s admits repositories by pattern and is not a repository this run can read: every repository it admits keeps only the project's values, because its own are dropped and never moved", pluginwire.Printable(pat)))
	}
	for _, r := range repos {
		if r.Unreadable == "" {
			continue
		}
		p.lines = append(p.lines, fmt.Sprintf("lift: %s could not be read (%s), so it is not migrated: its own values are dropped and never moved, and only the project's stand for it", pluginwire.Printable(r.Repo), oneLineCLI(r.Unreadable)))
	}
}

// runPlanners runs every registered planner, in name order, over the project
// and the repositories, and collects what they say. A planner that fails drops
// its own change, and the failure withholds every record of the run (records):
// the migration is per repository, so a repository another planner has already
// changed must not be recorded as done while one of its items was not planned.
func (p *liftPlan) runPlanners(proj lift.Project, repos []lift.Repo) error {
	failed := false
	for _, np := range liftPlanners() {
		ch, err := np.Plan(proj, repos)
		if err != nil {
			failed = true
			p.lines = append(p.lines, fmt.Sprintf("lift: the %s migration could not be planned (%s), so nothing it would have changed is written and no repository of this run is recorded as migrated", np.Name, oneLineCLI(err.Error())))
			continue
		}
		p.edits = append(p.edits, ch.LayerEdits...)
		p.stored = append(p.stored, ch.StoreWrites...)
		p.dbw = append(p.dbw, ch.DBWrites...)
		p.lines = append(p.lines, ch.Lines...)
		p.records = append(p.records, ch.Records...)
	}
	if failed {
		p.records = nil
		return nil
	}
	slices.Sort(p.records)
	p.records = slices.Compact(p.records)
	return nil
}

// applyEdits builds the document the layer file's edits produce, in the order
// the planners named them, and refuses where the result would not read back as
// a project layer: a file the run left unloadable is a file the Layer stage
// would refuse to publish and the owner's next edit would have to repair.
func (p *liftPlan) applyEdits(lc *localcfg.Config) error {
	if len(p.edits) == 0 {
		p.next = p.base
		return nil
	}
	next := p.base
	for _, ed := range p.edits {
		out, err := applyLiftEdit(next, ed)
		if err != nil {
			return userErr("writing %s into the layer file %s: %v", pluginwire.Printable(pathString(ed.Path)), p.layerPath, oneLineCLI(err.Error()))
		}
		next = out
	}
	if _, ps := projectLayerProblems(next, lc); len(ps) > 0 {
		return userErr("the lifted values would leave %s unreadable as a project layer: %s", p.layerPath, pluginwire.Printable(layerProblemsText(ps)))
	}
	p.next = next
	return nil
}

// applyLiftEdit writes one edit of one change into the layer document, in the
// two shapes C2's LayerEdit has: a scalar at a path, and an item of the
// sequence at a path, which is written whole — its scalars, its lists and its
// own sequences of items (yamledit's three item writers, one per shape). An
// item the sequence already holds under its key field is written where it
// stands and never doubled, so a second update converges.
func applyLiftEdit(src []byte, ed lift.LayerEdit) ([]byte, error) {
	if ed.Item == nil {
		return yamledit.SetScalar(src, ed.Path, ed.Value)
	}
	it := *ed.Item
	out := src
	if len(it.Fields) > 0 {
		next, err := yamledit.UpsertSeqItemByKey(out, ed.Path, it.KeyField, it.Key, it.Fields)
		if err != nil {
			return src, err
		}
		out = next
	}
	for _, f := range sortedListKeys(it.Lists) {
		next, err := yamledit.UpsertSeqItemListByKey(out, ed.Path, it.KeyField, it.Key, f, it.Lists[f])
		if err != nil {
			return src, err
		}
		out = next
	}
	for _, f := range sortedItemKeys(it.Items) {
		next, err := yamledit.UpsertSeqItemItemsByKey(out, ed.Path, it.KeyField, it.Key, f, seqItems(it.Items[f]))
		if err != nil {
			return src, err
		}
		out = next
	}
	return out, nil
}

// seqItems is a layer item's nested items in the editor's own shape.
func seqItems(in []lift.LayerItem) []yamledit.SeqItem {
	out := make([]yamledit.SeqItem, 0, len(in))
	for _, it := range in {
		out = append(out, yamledit.SeqItem{
			KeyField: it.KeyField, Key: it.Key,
			Fields: it.Fields, Lists: it.Lists, Items: seqItemsMap(it.Items),
		})
	}
	return out
}

func seqItemsMap(in map[string][]lift.LayerItem) map[string][]yamledit.SeqItem {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]yamledit.SeqItem, len(in))
	for k, v := range in {
		out[k] = seqItems(v)
	}
	return out
}

// pathString is a key path as one dotted line, for a message: the value splits
// a repository's own name on its dots, which is why the path itself is never
// spelled this way (C2's LayerEdit).
func pathString(p []string) string { return strings.Join(p, ".") }

func sortedListKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sortedItemKeys(m map[string][]lift.LayerItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// readLiftMigrated is the set of slugs whose migration record is in the runs
// bucket: D3305's "already migrated", which a planner acts on.
func readLiftMigrated(ctx context.Context, b *blobx.Bucket) (map[string]bool, error) {
	prefix := path.Dir(lift.RecordPath("slug")) + "/"
	out := map[string]bool{}
	it := b.Bucket.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(obj.Key, prefix), ".json")
		if !obj.IsDir && slug != "" && !strings.Contains(slug, "/") {
			out[slug] = true
		}
	}
}

// readLiftStore is the project's recipe store as a planner reads it: each
// object's name and the hex sha256 of its bytes, which is how a planner tells a
// clash from a copy of the same text (D3301).
func readLiftStore(ctx context.Context, b *blobx.Bucket) (map[string]string, error) {
	out := map[string]string{}
	it := b.Bucket.List(&blob.ListOptions{Prefix: recipe.ObjectPrefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if obj.IsDir {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, layerBucketTimeout)
		data, _, rerr := b.ReadMaxStrict(rctx, obj.Key, recipe.MaxBytes)
		cancel()
		if rerr != nil {
			return nil, fmt.Errorf("reading %s: %w", obj.Key, rerr)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(obj.Key, recipe.ObjectPrefix), ".yaml")
		if name != "" {
			out[name] = recipe.Sum(data)
		}
	}
}

// liftCapsRead is the budget database's cap nodes, the shape lift.Caps has:
// the global and default caps a repository's ceiling is read from, and each
// repository's own node by slug.
type liftCapsRead struct {
	Global   *budget.GlobalCaps         `json:"global"`
	Defaults *budget.DefaultCaps        `json:"defaults"`
	Repos    map[string]budget.RepoCaps `json:"repos"`
}

// readLiftCaps reads them. An installation with no budget backend has none, and
// a read that failed is reported by the caller and leaves every cap unknown.
func readLiftCaps(ctx context.Context, db *rtdb.Client) (lift.Caps, error) {
	var doc liftCapsRead
	if _, err := db.Get(ctx, "config/caps", &doc); err != nil {
		return lift.Caps{}, err
	}
	return lift.Caps{Global: doc.Global, Defaults: doc.Defaults, Repos: doc.Repos}, nil
}

// liftProjectRead reads the two things the project contributes to a plan and
// are not in the layer file: the cap nodes (RC1, RC4) and the published
// policy's admitted repositories (the concrete names among them are further
// repositories to migrate, D3306). Both live in the budget database, so neither
// is read where this installation has none; a database that is declared and
// cannot be read is said out loud, and leaves the caps unknown and the admits
// empty rather than taking either for nothing.
func liftProjectRead(ctx context.Context, e *initEngine, lc *localcfg.Config, p *liftPlan) (lift.Caps, []string) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return lift.Caps{}, nil
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		p.lines = append(p.lines, fmt.Sprintf("lift: the budget database could not be read (%s), so this run lowers no cap and migrates only the repositories this machine's config names", oneLineCLI(err.Error())))
		return lift.Caps{}, nil
	}
	var caps lift.Caps
	if caps, err = readLiftCaps(ctx, db); err != nil {
		p.lines = append(p.lines, fmt.Sprintf("lift: the cap nodes could not be read (%s), so this run lowers no cap", oneLineCLI(err.Error())))
		caps = lift.Caps{}
	}
	var admits []string
	if doc, found, perr := projectpolicy.ReadCarriedForward(ctx, db); perr == nil && found && doc != nil {
		admits = doc.AllowRepos
	}
	return caps, admits
}

// readLiftRepositories is M1's repofiles.Read adapted to C2's picture of a
// repository: the repositories this project can name — the local config's, and
// every concrete owner/name in the published admits — read at their default
// branch, and every admit this reader could not expand answered as a pattern
// (D3306, R12). A read that failed is the repository's own one-line reason and
// never the caller's error, so a project with one unreachable repository still
// migrates the others.
func readLiftRepositories(ctx context.Context, e *initEngine, lc *localcfg.Config, admits []string) ([]lift.Repo, []string, error) {
	in := repofiles.Input{Admits: admits}
	for name, r := range lc.Repos {
		in.Repos = append(in.Repos, repofiles.Listed{Repo: name, Provider: r.Provider})
	}
	slices.SortFunc(in.Repos, func(a, b repofiles.Listed) int { return strings.Compare(a.Repo, b.Repo) })
	read, patterns, err := repofiles.Read(ctx, in, liftOpener(ctx, e, lc))
	if err != nil {
		return nil, nil, err
	}
	out := make([]lift.Repo, 0, len(read))
	for _, r := range read {
		lr := lift.Repo{Repo: r.Repo, Provider: r.Provider, Config: r.YAML, Unreadable: r.Unread}
		for _, f := range r.Recipes {
			lr.Recipes = append(lr.Recipes, lift.RecipeFile{Path: f.Path, Data: f.Data})
		}
		out = append(out, lr)
	}
	return out, patterns, nil
}

// liftOpener is the provider the migration reads repositories through: the
// same credential path every other read of a repository this command makes
// takes (followUpsRepoSpec, repoProviderEnv, the catalog build's own opener),
// resolved per repository because the App ID and the secret are the
// repository's own. A failure here is a failure to open that one repository,
// which repofiles records as the repository's unreadable reason.
func liftOpener(ctx context.Context, e *initEngine, lc *localcfg.Config) gitprov.Opener {
	r := e.r
	return func(ctx context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		rs, err := followUpsRepoSpec(lc, repo, kind, os.Getenv)
		if err != nil {
			return nil, nil, err
		}
		provEnv, _, err := repoProviderEnv(ctx, r.stageDetail(), lc, rs, os.Environ(), liftKeyRead, os.Getenv,
			r.stdinTerminal(), nodePolicy(ctx, &r.policy, lc))
		if err != nil {
			return nil, nil, err
		}
		_, open, err := openCatalogProvider(provEnv, func(s string) {
			fmt.Fprintf(r.stageDetail(), "fugaro: warning: %s\n", s)
		})
		if err != nil {
			return nil, nil, err
		}
		return open(ctx, kind, repo)
	}
}

// confirmWrite is this stage's own confirmation, and it is the Layer stage's
// shape for the same reason: nothing at all for the owner's own terminal on a
// managed project (the owner's ruling of 2026-11-06), and the confirmation of a
// write that loses no data and takes no secret value everywhere else
// (confirmStageWrite) — a person at their own terminal answers it by being
// there, --yes answers it where the flag reaches the stage, --ask puts the
// question back, and a session that can do none of the three is refused rather
// than asked. Nothing is typed.
func (s *liftStage) confirmWrite(env initflow.Env, what string) error {
	r := s.e.r
	if r.gateStandsIn() && initflow.PersonAtTheTerminal(r.conditions()) {
		fmt.Fprintf(r.w, "⚠ CONFIRM (project %s): %s\n", s.p.lc.Name, what)
		fmt.Fprintf(r.w, "  %s\n", initflow.OwnerProceeded())
		return nil
	}
	return confirmStageWrite(r.cmd, s.p.lc, restoreAsk(r.conditions(), r.asksBack()), env.Yes, what)
}

// say prints every line of every change, once, and it is the first thing this
// stage does with anything it has read: every line is on screen before the
// first write of the run, which is what "say first, then write" is.
func (s *liftStage) say(p *liftPlan) {
	if s.said {
		return
	}
	s.said = true
	for _, ln := range p.lines {
		fmt.Fprintln(s.e.r.w, ln)
	}
}

// Check reports what the migration would change, and prints its lines. It
// writes nothing, in this pass or in the execution pass behind it, and a
// bucket or a list it could not read leaves the step this stage was run for
// owed rather than answered (Undone).
func (s *liftStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.plan(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	if p.skip != "" {
		return initflow.Status{State: initflow.Skipped, Detail: p.skip, Undone: p.skipUndone}, nil
	}
	s.say(p)
	if p.empty {
		return initflow.Status{State: initflow.Done, Detail: "every repository this project can list is migrated, and nothing this release lifts is left to move: no change to write"}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: p.planDetail()}, nil
}

// Plan says what the run is about to write. The lines went out in the check
// above and are not said twice; what is here is the one sentence the plan view
// reads.
func (s *liftStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := s.plan(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	if d := s.writeDetail(p); d != "" {
		return initflow.Plan{NothingToDo: true, Detail: d}, nil
	}
	return initflow.Plan{Detail: p.planDetail()}, nil
}

// planDetail is what this stage says it is about to do.
func (p *liftPlan) planDetail() string {
	var parts []string
	if len(p.edits) > 0 {
		parts = append(parts, fmt.Sprintf("write %d %s into %s", len(p.edits), plural(len(p.edits), "lifted value", "lifted values"), p.layerPath))
	}
	if len(p.stored) > 0 {
		parts = append(parts, fmt.Sprintf("publish %d %s into the project's recipe store", len(p.stored), plural(len(p.stored), "recipe", "recipes")))
	}
	if len(p.dbw) > 0 {
		parts = append(parts, fmt.Sprintf("lower %d %s to the project's ceiling", len(p.dbw), plural(len(p.dbw), "repository cap", "repository caps")))
	}
	if len(p.records) > 0 {
		parts = append(parts, fmt.Sprintf("record %d %s as migrated once the Layer stage has published them", len(p.records), plural(len(p.records), "repository", "repositories")))
	}
	return strings.Join(parts, ", ") + ", after your confirmation"
}

// writeDetail is the stage's whole answer when it has nothing to write: the
// skip it found, or the state the planners left.
func (s *liftStage) writeDetail(p *liftPlan) string {
	switch {
	case p.skip != "":
		return p.skip
	case p.empty:
		return "every repository this project can list is migrated, and nothing this release lifts is left to move: no change to write"
	}
	return ""
}

// Apply is the write, and it is the second half of the stage's promise: every
// line was printed in the check above, and what is left is the three families
// of writes in the order they have to be in — the layer file first, because the
// Layer stage that runs next publishes it and a record's landing is that
// publish. A coding agent's session never reaches here: the file edit is this
// machine's own, and the store and the database are cloud writes like any
// other.
func (s *liftStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	p, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if d := s.writeDetail(p); d != "" {
		return initflow.Outcome{Detail: d}, nil
	}
	if err := s.writeLayer(p, env); err != nil {
		return initflow.Outcome{}, err
	}
	if err := s.writeStore(ctx, p, env); err != nil {
		return initflow.Outcome{}, err
	}
	if err := s.writeCaps(ctx, p, env); err != nil {
		return initflow.Outcome{}, err
	}
	s.records = p.records
	return initflow.Outcome{Changed: true, Detail: p.planDetail()}, nil
}

// writeLayer writes the document the edits produce over the layer file, and
// forgets the Layer stage's cached read of it (layerStage.forget): the stage
// that publishes the file runs after this one and would otherwise publish the
// document it read before the edits, or refuse this run's own write as one that
// changed under it.
//
// A file that moved since the plan was read is refused rather than overwritten:
// the plan is one read of the file and the diff the confirmation is about is
// that read's, so a file a person edited in between is a document the operator
// never saw. A file that is not there yet is created from the base the edits
// were made on — the published object or the shell — because the edit is one
// value inside a document and not a document.
func (s *liftStage) writeLayer(p *liftPlan, env initflow.Env) error {
	if len(p.edits) == 0 {
		return nil
	}
	if err := s.e.r.asking(func() error { return s.confirmWrite(env, liftLayerWhat) }); err != nil {
		return err
	}
	w := s.e.r.w
	if p.layerExists {
		cur, err := readLayerFile(p.layerPath)
		if err != nil {
			return userErr("reading %s back before writing the lifted values into it: %v", p.layerPath, oneLineCLI(err.Error()))
		}
		if !bytes.Equal(cur, p.base) {
			return userErr("%s changed while this run was going: the lifted values were planned against the file this run read, so write them again with %s and look at the file first", p.layerPath, selfCommand()+" update")
		}
		backup, err := backupFile(p.layerPath, p.base, time.Now())
		if err != nil {
			return userErr("backing up %s before writing the lifted values into it: %v", p.layerPath, err)
		}
		if err := writeFileAtomic(p.layerPath, p.next); err != nil {
			return userErr("writing the lifted values into %s: %v", p.layerPath, err)
		}
		fmt.Fprintf(w, "wrote the lifted values into %s (backup at %s); the Layer stage of this run publishes it\n", p.layerPath, backup)
	} else {
		if err := os.MkdirAll(filepath.Dir(p.layerPath), 0o700); err != nil {
			return userErr("creating %s: %v", p.layerPath, err)
		}
		if err := writeFileAtomic(p.layerPath, p.next); err != nil {
			return userErr("creating %s with the lifted values: %v", p.layerPath, err)
		}
		fmt.Fprintf(w, "created %s from %s with the lifted values; the Layer stage of this run publishes it\n", p.layerPath, p.baseFrom)
	}
	if s.e.layer != nil {
		s.e.layer.forget()
	}
	return nil
}

// writeStore publishes the recipes the change carries into the project's store.
// An object that is already there with the same bytes is the same recipe and is
// left alone; an object that is there with DIFFERENT bytes is a value someone
// published after the migration was decided, and this run neither overwrites it
// nor records the repository as migrated (D3305: the migration never overwrites
// a project value written after it) — the next update decides again.
func (s *liftStage) writeStore(ctx context.Context, p *liftPlan, env initflow.Env) error {
	if len(p.stored) == 0 {
		return nil
	}
	if err := s.e.r.asking(func() error { return s.confirmWrite(env, liftStoreWhat) }); err != nil {
		return err
	}
	b, err := openRecordBucket(ctx, p.url)
	if err != nil {
		return remote(fmt.Errorf("opening %s to publish the lifted recipes: %w", p.url, err))
	}
	defer b.Close()
	w := s.e.r.w
	for _, sw := range p.stored {
		rctx, cancel := context.WithTimeout(ctx, layerBucketTimeout)
		old, gen, rerr := b.ReadMaxStrict(rctx, sw.Key, recipe.MaxBytes)
		cancel()
		switch {
		case rerr == nil && bytes.Equal(old, sw.Data):
			continue
		case rerr == nil:
			s.records = nil
			return userErr("the project's store already carries %s (sha256 %s) and this run would write different bytes into it (sha256 %s): nothing was written there and no repository is recorded as migrated, so look at it (fugaro recipes show) and run %s again", sw.Key, recipe.Sum(old), recipe.Sum(sw.Data), selfCommand()+" update")
		case errors.Is(rerr, blobx.ErrNotExist):
		case errors.Is(rerr, blobx.ErrTooLarge):
			s.records = nil
			return userErr("the project's store holds an oversized object at %s (over 16 KiB): delete it by hand, then run %s again", sw.Key, selfCommand()+" update")
		case rerr != nil:
			return remote(fmt.Errorf("reading %s before publishing it: %w", sw.Key, rerr))
		}
		if _, err := b.Create(ctx, sw.Key, sw.Data, "application/yaml"); err != nil {
			switch {
			case errors.Is(err, blobx.ErrExists), errors.Is(err, blobx.ErrConflict):
				s.records = nil
				return userErr("another publisher wrote %s while this run was going: nothing was written there and no repository is recorded as migrated, so run %s again", sw.Key, selfCommand()+" update")
			case errors.Is(err, blobx.ErrForbidden):
				return operatorWriteErr("gs://"+b.GCSName, sw.Key, err)
			default:
				return remote(fmt.Errorf("writing %s: %w", sw.Key, err))
			}
		}
		fmt.Fprintf(w, "published the lifted recipe %s into the project's store (sha256 %s)\n", sw.Key, recipe.Sum(sw.Data))
		_ = gen
	}
	return nil
}

// writeCaps lowers each repository cap node the change names, against the read
// that was made when it was planned: a node that moved in between is refused
// (PutIfMatch), and a node that already holds the value is left alone. The
// write is a lowering in place — the node keeps the other half of its caps and
// its repo name — so nothing but the two figures this release lifts moves.
func (s *liftStage) writeCaps(ctx context.Context, p *liftPlan, env initflow.Env) error {
	if len(p.dbw) == 0 {
		return nil
	}
	if err := s.e.r.asking(func() error { return s.confirmWrite(env, liftDBWhat) }); err != nil {
		return err
	}
	_, db, err := budgetDBFor(ctx, p.lc)
	if err != nil {
		return err
	}
	w := s.e.r.w
	for _, dw := range p.dbw {
		want, err := json.Marshal(dw.Value)
		if err != nil {
			return userErr("encoding the cap this run lowers at %s: %v", dw.Path, err)
		}
		var cur json.RawMessage
		etag, found, err := db.GetETag(ctx, dw.Path, &cur)
		if err != nil {
			return remote(fmt.Errorf("reading %s before lowering it: %w", dw.Path, err))
		}
		if found && sameNode(cur, want) {
			continue
		}
		if err := db.PutIfMatch(ctx, dw.Path, etag, dw.Value); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) {
				s.records = nil
				return userErr("%s changed while this run was going: the cap this run lowers was read before the change, so nothing was written there and no repository is recorded as migrated; run %s again", dw.Path, selfCommand()+" update")
			}
			return remote(fmt.Errorf("lowering %s: %w", dw.Path, err))
		}
		fmt.Fprintf(w, "lowered %s to %s, the project's ceiling\n", dw.Path, string(want))
	}
	return nil
}

// Verify reads back what this run wrote: the layer file against the document
// the edits produce, each store object against its bytes, and each cap node
// against the value the change names. A write that claims something it did not
// land is a migration recorded for a project that does not carry it.
func (s *liftStage) Verify(ctx context.Context) error {
	p, err := s.plan(ctx)
	if err != nil || p == nil {
		return err
	}
	if s.writeDetail(p) != "" {
		return nil
	}
	if len(p.edits) > 0 {
		data, err := os.ReadFile(p.layerPath)
		if err != nil {
			return fmt.Errorf("the lifted values were written but %s cannot be read back: %w", p.layerPath, err)
		}
		if !bytes.Equal(data, p.next) {
			return fmt.Errorf("%s is not the document the lifted values were written into", p.layerPath)
		}
	}
	if len(p.stored) > 0 {
		b, err := openRecordBucket(ctx, p.url)
		if err != nil {
			return fmt.Errorf("the lifted recipes were published but %s cannot be read back: %w", p.url, err)
		}
		defer b.Close()
		for _, sw := range p.stored {
			data, _, err := b.ReadMaxStrict(ctx, sw.Key, recipe.MaxBytes)
			if err != nil {
				return fmt.Errorf("the lifted recipe %s was published but cannot be read back: %w", sw.Key, err)
			}
			if !bytes.Equal(data, sw.Data) {
				return fmt.Errorf("the lifted recipe %s is not the object this run published", sw.Key)
			}
		}
	}
	if len(p.dbw) > 0 {
		_, db, err := budgetDBFor(ctx, p.lc)
		if err != nil {
			return err
		}
		for _, dw := range p.dbw {
			want, err := json.Marshal(dw.Value)
			if err != nil {
				return err
			}
			var cur json.RawMessage
			found, err := db.Get(ctx, dw.Path, &cur)
			if err != nil {
				return fmt.Errorf("the cap at %s was lowered but cannot be read back: %w", dw.Path, err)
			}
			if !found || !sameNode(cur, want) {
				return fmt.Errorf("the cap at %s is not the value this run lowered it to", dw.Path)
			}
		}
	}
	return nil
}

// liftRecord is the object update writes in the runs bucket once a repository's
// migration has landed (D3305). The launcher reads the record's presence to
// tell a migrated repository from one update could not list (D3308, PR1); the
// release and the time are what a person reads when they ask when and by what
// a repository was migrated.
type liftRecord struct {
	Release string `json:"release,omitempty"`
	At      string `json:"at"`
}

// writeLiftRecords writes D3305's records, and it runs AFTER the loop rather
// than in the stage: a record claims that a repository's migration has landed,
// and what lands the layer half of it is the Layer stage's publish of the
// document this stage edited, which runs after it. A publish that was refused,
// skipped, blocked or never reached therefore writes no record, and the next
// update migrates the repository again — the fail-closed half of D3306's
// direction.
//
// A record that is already there is left as it is: the first migration is the
// one the launcher's refusal is about, and a second update's rereading of the
// same repository must not move its date.
func (e *initEngine) writeLiftRecords(ctx context.Context, res *initflow.Result) {
	s := e.lift
	if s == nil || e.lc == nil || len(s.records) == 0 || res == nil || res.PlanOnly {
		return
	}
	if !e.layerLanded(res) {
		return
	}
	url := recordReadURL(e.lc)
	if url == "" || fakeEndpointsOnGS(e.lc, url) {
		return
	}
	b, err := openRecordBucket(ctx, url)
	if err != nil {
		e.liftRecordWarn(fmt.Sprintf("the migration was written but its records could not be written, because %s could not be read (%s): run %s again to record them", url, oneLineCLI(err.Error()), selfCommand()+" update"))
		return
	}
	defer b.Close()
	for _, slug := range s.records {
		key := lift.RecordPath(slug)
		if _, _, err := b.ReadMaxStrict(ctx, key, 4096); err == nil {
			continue
		} else if !errors.Is(err, blobx.ErrNotExist) {
			e.liftRecordWarn(fmt.Sprintf("the migration record %s could not be read (%s), so it was not written: run %s again", key, oneLineCLI(err.Error()), selfCommand()+" update"))
			continue
		}
		data, err := json.Marshal(liftRecord{Release: releaseVersion(), At: time.Now().UTC().Format(time.RFC3339)})
		if err != nil {
			e.liftRecordWarn(fmt.Sprintf("the migration record %s could not be encoded (%s)", key, oneLineCLI(err.Error())))
			continue
		}
		if _, err := b.Create(ctx, key, data, "application/json"); err != nil {
			e.liftRecordWarn(fmt.Sprintf("the migration record %s could not be written (%s): the next %s migrates that repository again", key, oneLineCLI(err.Error()), selfCommand()+" update"))
			continue
		}
		fmt.Fprintf(e.r.w, "recorded the migration of %s at %s\n", key, lift.RecordPath(slug))
	}
}

// liftRecordWarn says what a record that did not land means, once, in the shape
// every other warning this door makes takes.
func (e *initEngine) liftRecordWarn(msg string) {
	e.r.warn("lift: " + msg)
}

// layerLanded is whether this run published the project layer, which is what
// the records wait for. It reads the layer stage's own fact rather than its row
// (layerStage.published), because Changed is the stage's state for three writes
// that publish nothing — a re-seed, a generation record and the wizard's rigor
// answer — and a re-seed in particular replaces the file with the object and
// DROPS the lifted values the planners just wrote into it. The row is still
// read, for the failure the fact alone cannot see: a publish whose
// per-repository fan-out failed is an object this project's repositories have
// not all been told about, and this run records nothing (the next update
// migrates the repository again).
func (e *initEngine) layerLanded(res *initflow.Result) bool {
	if e.layer == nil || !e.layer.published {
		return false
	}
	for _, r := range res.Stages {
		if r.Name == initflow.Layer {
			return r.State == initflow.Changed
		}
	}
	return false
}
