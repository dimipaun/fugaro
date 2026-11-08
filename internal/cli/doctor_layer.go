package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// doctorLayerChecks are doctor's project layer lines (decision L20), given
// rf, the checkout's fugaro.yaml already resolved over its project layer by
// fugaroYAMLCheck (never re-resolved here, which would be a second bucket
// round trip per doctor invocation and could disagree with fy if the
// published layer changed in between):
//   - the layer that applies (info);
//   - whether the repository's last run used it;
//   - whether the repository's copy, which the daily check and Cloud Build
//     read, is current;
//   - whether each workflow's image was built from the image settings it
//     resolves to now, and whether a workflow the project's image catalog
//     gives its image to runs an entry that has been built — judged against
//     the catalog the job spec resolves, with an unread line of its own when
//     that catalog could not be read at all;
//   - the one local layer file (localLayerFileCheck), which reads no bucket
//     and so is reported even when none of the above can be.
//
// Best effort: what cannot be read says nothing.
func doctorLayerChecks(ctx context.Context, lc *localcfg.Config, root string, rf resolvedFile) []doctorCheck {
	l := rf.Layer.Layer
	if l == nil {
		// Whether a layer applies is a fact when it was resolved and an
		// unknown when it was not: the second is a counted finding, not a
		// measurement a reader skips over.
		msg := "no project layer applies to this checkout"
		unknown := rf.Layer.Unknown
		if unknown {
			msg = "whether a project layer applies is unknown"
		}
		if rf.Layer.Note != "" {
			msg += ": " + rf.Layer.Note
		}
		return append([]doctorCheck{{ID: "project-layer", Severity: "info", unread: unknown, Problem: msg}}, localLayerFileCheck(lc, rf)...)
	}
	problem := fmt.Sprintf("project layer %s generation %d (sha256 %s) applies; fugaro config show prints where each value comes from", l.Project, rf.Layer.Generation, shortSHA(l.SHA256))
	if rf.Layer.Note != "" {
		// The layer applies, but came from the offline cache (the bucket was
		// unreachable): degrade with the note, never drop it (design: "never
		// fail for lack of network").
		problem += "; " + rf.Layer.Note
	}
	out := []doctorCheck{{ID: "project-layer", Severity: "info", Problem: problem}}
	// A rewritten origin (the checkout's raw .git/config disagreeing with
	// what git resolves) is not trusted by name (readOrigin's doc comment),
	// the same guard every other consumer of it in this package applies
	// (checkoutOriginAt, repoKnown): otherwise a spoofed .git/config could
	// make doctor compute another repository's slug and show its
	// layer-drift, copy-staleness and image-config state here.
	oi, ok := readOrigin(ctx, root)
	if !ok || oi.Rewritten {
		return append(out, localLayerFileCheck(lc, rf)...)
	}
	slug, err := task.Slug(rf.Cfg.Git.Provider, oi.Repo)
	if err != nil {
		return append(out, localLayerFileCheck(lc, rf)...)
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return append(out, localLayerFileCheck(lc, rf)...)
	}
	env := &cloudEnv{lc: lc, bucket: b}
	defer env.Close() // closes b, and env.records if recordBucket opened one
	out = append(out, localLayerFileCheck(lc, rf)...)
	out = append(out, layerDrift(ctx, b, slug, l)...)
	out = append(out, layerCopyCheck(ctx, b, slug, l)...)
	if rb, err := env.recordBucket(ctx); err == nil {
		// The catalog the deploy-time job spec is computed against and the
		// launcher judges the deployed job's image by: this checkout's config
		// when it states one, the published node's otherwise (effectiveCatalog,
		// the resolution internal/infra's spec and run.go both make). Reading
		// the local config alone left a machine whose file is silent about the
		// catalog reading "no catalog" for a project that has one, and its
		// pinned workflow was then judged by no line at all while its job runs
		// an entry only the published node holds.
		cat, catErr := effectiveCatalog(ctx, env)
		out = append(out, imageCatalogUnreadCheck(lc, rf.Cfg, catErr)...)
		// A nil catalog is two states, and the checks below judge a workflow
		// differently in each: a catalog nobody read leaves the workflows it
		// might decide to the catalog, where a project whose catalog is known
		// to be empty runs the derived image for every one of them
		// (workflowCatalogEntry).
		//
		// The second state is not "cat is nil and the read failed": a catalog
		// written empty in this machine's own config is an absence and not an
		// unknown, however the node answered — the file's catalog wins
		// (localcfg's keep rule, D1140), and ImageCatalogEffective answers nil
		// for it on purpose, "this file says nothing is in it". This is the
		// same condition imageCatalogUnreadCheck states, and the two are read
		// one after the other so they cannot drift.
		catUnknown := catErr != nil && lc.ImageCatalog == nil
		out = append(out, imageConfigChecks(ctx, env, rb, slug, rf.Cfg, rf.Res, cat, catUnknown)...)
		out = append(out, catalogImageChecks(ctx, env, rb, slug, rf.Cfg, cat, catUnknown)...)
	}
	return out
}

// The three causes of the shared word `unpublished`. Differing bytes are never
// the whole answer: a local edit is the owner's change, to publish, and a stale
// copy is another machine's publish this file would revert, to re-seed — and
// the record (D854) is the only thing that tells them apart.
const (
	layerLocalEdit       = "a local edit"
	layerLocalStale      = "a stale copy from another machine"
	layerLocalUnrecorded = "not recorded as written from the published layer"
)

// layerLocalState is the one local layer file (D830) against the published
// object: the same sha256 comparison `fugaro config layer`, doctor and
// `fugaro rigor show` all decide the shared word `unpublished` by, plus the
// record that says which of its causes this is.
//
// The published side is handed in already resolved (pub, gen) and is never
// read from the bucket here: the callers hold it, and a state that vanished
// when the bucket was unreachable would be missing exactly when it is needed.
type layerLocalState struct {
	Path   string
	Exists bool
	SHA256 string // the local file's, when it exists
	// Published is whether an object applies, with its own sha256 and the
	// generation this machine resolved it at.
	Published    bool
	PublishedSHA string
	Generation   int64
	// Recorded is the local project config's layer_generation, "" when
	// nothing is recorded.
	Recorded string
	// Unreadable is why a file that is there was not read (a symlink, a
	// non-regular file, one over the layer size limit) — the states above are
	// about bytes, and this is the one where there are none to compare because
	// the file is not one this Fugaro reads. It is read the way the publish
	// doors read it (readLayerFile, config.ReadRegular), so a file a publish
	// refuses is not counted here as a layer the project has.
	Unreadable string
	// Unpublished is true only when both sides are present and their
	// sha256s differ: with either absent there is nothing to compare.
	Unpublished bool
	Cause       string
}

// localLayerState reads the local layer file of project project ("" when this
// machine cannot name it) and compares it with pub, the published object as
// this machine resolved it (nil: none applies). lc is the local project config
// the record lives in.
//
// It reads the environment through getenvOS, the same name localLayerFileCheck
// and every findLayer call site use: the file's directory is the config home
// the whole process is resolving against, and a second way of asking for it
// would be a second answer.
func localLayerState(lc *localcfg.Config, project string, pub *config.ProjectLayer, gen int64) layerLocalState {
	var st layerLocalState
	if project == "" {
		return st
	}
	path, err := localcfg.LayerFile(getenvOS, project)
	if err != nil {
		return st
	}
	st.Path = path
	data, err := readLayerFile(path)
	switch {
	case err == nil:
		st.Exists, st.SHA256 = true, config.LayerSum(data)
	case errors.Is(err, os.ErrNotExist):
	default:
		st.Unreadable = oneLineCLI(err.Error())
	}
	if lc != nil {
		st.Recorded = lc.LayerGeneration
	}
	if pub == nil {
		return st
	}
	st.Published, st.PublishedSHA, st.Generation = true, pub.SHA256, gen
	if st.Exists && st.SHA256 != pub.SHA256 {
		st.Unpublished = true
		st.Cause = layerLocalCause(st)
	}
	return st
}

// layerLocalCause names which reading of `unpublished` this is. The record is
// the generation this file's bytes were written from: still the published one
// means the owner's own edit, any other one means the object published now is
// not the one this file was written from (an older record, after someone else's
// publish; a newer one, after the object was deleted and re-created or the
// bucket was restored — publishing would replace a layer this machine cannot
// account for), and none at all means no publish this machine can compare with.
func layerLocalCause(st layerLocalState) string {
	recorded, err := strconv.ParseInt(st.Recorded, 10, 64)
	switch {
	case st.Recorded == "" || err != nil:
		return layerLocalUnrecorded
	case recorded != st.Generation:
		return layerLocalStale
	default:
		return layerLocalEdit
	}
}

// unpublishedText is the one sentence the doors that call the state
// `unpublished` print: the word, both sha256s, which of the two causes it is,
// and the remedy that fits it. It is "" for a state that is not unpublished.
func (s layerLocalState) unpublishedText() string {
	if !s.Unpublished {
		return ""
	}
	const pub = "the published project layer"
	switch s.Cause {
	case layerLocalStale:
		return fmt.Sprintf("unpublished: the local layer file %s (sha256 %s) differs from %s (sha256 %s), and this file was written from generation %s, which is not the published one, so it is %s — run fugaro update to re-seed it from the published one",
			s.Path, shortSHA(s.SHA256), pub, shortSHA(s.PublishedSHA), generationText(s.Recorded), layerLocalStale)
	case layerLocalUnrecorded:
		return fmt.Sprintf("unpublished: the local layer file %s (sha256 %s) differs from %s (sha256 %s), and no generation is recorded for this file, so it is %s — run fugaro update to publish it",
			s.Path, shortSHA(s.SHA256), pub, shortSHA(s.PublishedSHA), layerLocalUnrecorded)
	}
	return fmt.Sprintf("unpublished: the local layer file %s (sha256 %s) differs from %s (sha256 %s): this is %s — run fugaro update to publish it",
		s.Path, shortSHA(s.SHA256), pub, shortSHA(s.PublishedSHA), layerLocalEdit)
}

// localLayerFileCheck is doctor's line for the one local layer file (D830).
// It is appended to doctorLayerChecks's output on every path that function
// returns from, because it reads no bucket: the published side is the already
// resolved rf, and the one line the owner needs in the state that breaks
// `fugaro rigor set` must not vanish with the network.
func localLayerFileCheck(lc *localcfg.Config, rf resolvedFile) []doctorCheck {
	project := ""
	var pub *config.ProjectLayer
	if l := rf.Layer.Layer; l != nil {
		project, pub = l.Project, l
	} else if lc != nil {
		project = lc.Name
	}
	st := localLayerState(lc, project, pub, rf.Layer.Generation)
	if st.Path == "" {
		return nil
	}
	fix := selfCommand() + " update"
	switch {
	case st.Unpublished:
		return []doctorCheck{{ID: "local-layer-file", Severity: "warning", Problem: st.unpublishedText(), Fix: fix}}
	case st.Unreadable != "":
		// No fix command: the file is the owner's, and the command that would
		// write one (update) refuses this file as the publish doors do.
		return []doctorCheck{{ID: "local-layer-file", Severity: "warning",
			Problem: fmt.Sprintf("the local layer file %s is not read: %s", st.Path, st.Unreadable)}}
	case !st.Exists && !st.Published:
		// Neither side exists, which is an installation that publishes no
		// layer and has never been seeded: there is nothing to edit, nothing
		// to publish and nothing to seed from, so the check says nothing. A
		// row here would be a finding on a clean installation — doctor's own
		// clean screen is one line (TestDoctorSecretScopeIsNotAChoice), and a
		// pointer to `--all` for a state with no action in it is the one thing
		// that screen exists not to do.
		return nil
	case !st.Exists:
		return []doctorCheck{{ID: "local-layer-file", Severity: "warning",
			Problem: fmt.Sprintf("the local layer file %s does not exist while a project layer is published: %s seeds it from the published layer", st.Path, fix), Fix: fix}}
	case !st.Published:
		return []doctorCheck{{ID: "local-layer-file", Severity: "warning",
			Problem: fmt.Sprintf("the local layer file %s (sha256 %s) is not published: this project has published no layer, and %s publishes this one", st.Path, shortSHA(st.SHA256), fix), Fix: fix}}
	}
	return []doctorCheck{{ID: "local-layer-file", Severity: "info",
		Problem: fmt.Sprintf("the local layer file %s (sha256 %s) is the published project layer (generation %d)", st.Path, shortSHA(st.SHA256), st.Generation)}}
}

// layerDrift compares the repository's last run with the published layer.
func layerDrift(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer) []doctorCheck {
	ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, time.Time{})
	if err != nil || len(ids) == 0 {
		return nil
	}
	id := ids[0] // newest first
	rec, err := runstore.Open(b.Bucket, slug, id).ReadRecord(ctx)
	if err != nil {
		return nil
	}
	fix := "upgrade every teammate's fugaro CLI and CI pin to " + layeredSince + " or later"
	switch pl := rec.ProjectLayer; {
	case pl == nil:
		return []doctorCheck{{ID: "project-layer-drift", Severity: "warning",
			Problem: fmt.Sprintf("the last run %s ran without the project layer: it was launched by a fugaro CLI older than %s, or before the layer was published", id, layeredSince), Fix: fix}}
	case !pl.Applied:
		return []doctorCheck{{ID: "project-layer-drift", Severity: "warning",
			Problem: fmt.Sprintf("the last run %s did not apply the project layer: fugaro.yaml at its ref did not name the layer's project and gcp_project", id),
			Fix:     "merge the gcp_project: line, or launch from the repository's checkout"}}
	case pl.SHA256 != l.SHA256:
		gen := "-" // the record is untrusted input: a negative generation never came from a publisher (validGeneration, ls.go)
		if validGeneration(pl.Generation) {
			gen = strconv.FormatInt(pl.Generation, 10)
		}
		return []doctorCheck{{ID: "project-layer-drift", Severity: "info",
			Problem: fmt.Sprintf("the last run %s used project layer generation %s (sha256 %s); the published one is sha256 %s", id, gen, pluginwire.Printable(shortSHA(pl.SHA256)), shortSHA(l.SHA256)),
			Fix:     "nothing, if the change was meant: the next run uses the published layer"}}
	}
	return nil
}

// layerCopyCheck compares the repository's copy with the published layer.
func layerCopyCheck(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer) []doctorCheck {
	const fix = "run fugaro config publish again, or fugaro image build in this checkout"
	data, _, err := b.ReadMax(ctx, config.LayerCopyKey(slug), config.LayerMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return []doctorCheck{{ID: "project-layer-copy", Severity: "warning",
			Problem: "the repository has no copy of the project layer (" + config.LayerCopyKey(slug) + "), so its daily image check and its image builds resolve without it", Fix: fix}}
	case err != nil:
		return nil
	case config.LayerSum(data) != l.SHA256:
		return []doctorCheck{{ID: "project-layer-copy", Severity: "warning",
			Problem: fmt.Sprintf("the repository's copy of the project layer (sha256 %s) is not the published one (%s): its daily image check resolves against the old one", shortSHA(config.LayerSum(data)), shortSHA(l.SHA256)), Fix: fix}}
	}
	return nil
}

// imageCatalogUnreadCheck is doctor's line for the one state in which the
// catalog the two checks below judge workflows against was not read: this
// machine's config states none of its own, and the published node it would
// take one from did not answer (effectiveCatalog's error). The two checks
// below judge no workflow then — a pinned entry is neither compared nor
// called unbuilt, one whose needs: the node's catalog might cover is left
// alone, and so is one that names nothing, since the node that did not answer
// is exactly the one that may mark the default such a workflow runs. Judging
// any of them would name a build of an image its jobs may never run. The
// unknown is a line of its own, never silence. It is doctor's unread state
// (unread, so doctorClassOf counts it as a finding: an unknown is not a
// measurement).
//
// A config that states a catalog of its own is not this state, however the
// node answered: the local one wins (localcfg's keep rule, D1140), it is what
// the job spec resolves, and the node's copy cannot change a line below.
func imageCatalogUnreadCheck(lc *localcfg.Config, cfg *config.Config, catErr error) []doctorCheck {
	if catErr == nil || lc.ImageCatalog != nil || len(cfg.Workflows) == 0 {
		return nil
	}
	return []doctorCheck{{ID: "image-catalog", Severity: "info", unread: true,
		Problem: "whether the project's image catalog decides a workflow's image could not be checked: this machine's config states no catalog of its own, and the published policy node it would take one from could not be read (" + oneLineCLI(catErr.Error()) + "), so a workflow that pins an entry, needs a tag one covers, or runs the project's default is not judged here",
		Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
}

// imageConfigChecks names each workflow (with a generated image) whose build
// record's image-config hash is not the hash of what it resolves to now:
// typically a profile's base or image settings changed.
//
// A workflow the project's image catalog speaks for is not one of them
// (catalogImageChecks judges those): its jobs run the catalog entry's image,
// which the catalog build writes and records per entry, so the per-workflow
// record compared here is not the image they start on — and the fix this
// check would name, `fugaro image build --workflow`, builds an image those
// jobs never run. The owner's rule is what that serves: doctor is clean after
// `fugaro update`, and sending the owner back to it for an image it does not
// build is a loop they have already run.
//
// cat is the catalog the job spec resolves (doctorLayerChecks), never the
// local config's alone: a workflow the published node's catalog speaks for is
// as much the catalog's as one this file's own catalog names. catUnknown is
// the other half of that argument (workflowCatalogEntry): a project with no
// catalog at all is one whose pinned workflows are compared here, because
// their jobs run the derived image — so the two nils are not one state.
func imageConfigChecks(ctx context.Context, env *cloudEnv, rb *blobx.Bucket, slug string, cfg *config.Config, res *config.Resolution, cat *config.Catalog, catUnknown bool) []doctorCheck {
	var out []doctorCheck
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		w := cfg.Workflows[name]
		if _, ok := workflowCatalogEntry(cat, catUnknown, slug, w, catalog.Backend(env.lc.Backend())); ok {
			continue
		}
		if w.Dockerfile != "" {
			continue // its hash covers the Dockerfile's blob, which needs a tree
		}
		data, _, err := rb.Read(ctx, imagecheck.RecordKey(slug, name))
		if err != nil {
			continue
		}
		rec, err := imagecheck.ParseRecord(data)
		if err != nil {
			continue
		}
		want, err := imagecheck.ImageConfigHash(cfg, name, nil)
		if err != nil || rec.ImageConfigHash == want {
			continue
		}
		reason := imageConfigReason(res, name)
		out = append(out, doctorCheck{ID: "image-config-" + name, Severity: "warning",
			Problem: fmt.Sprintf("workflow %s's job image was built from other image settings than it resolves to now (%s)", name, reason),
			Fix:     "the daily image check rebuilds it; to do it now: fugaro image build --workflow " + name})
	}
	return out
}

// catalogImageChecks names each workflow whose image the project's image
// catalog gives it and whose entry is not the image a build of the entry
// would produce now: no build has built it at all, or the build that did read
// another definition than the runs bucket's copy holds (the daily catalog
// check's own catalog-definition trigger, D1148).
//
// The fix is the entry's own build, never `fugaro update`: update publishes
// the catalog and builds no entry of it, so its line would be the loop
// imageConfigChecks' comment is about. The check's ID is deliberately outside
// doctorUpdatePrefixes for the same reason — the default view prints the
// command that does the work.
//
// Best effort, as the layer checks are: what cannot be read says nothing, and
// a catalog that was not read — cat nil with catUnknown, the state
// imageCatalogUnreadCheck has a line of its own for — leaves every workflow's
// entry unjudged here. So is an entry's build record that is there and could
// not be read or parsed: only an absent one is the "no build has built it"
// warning below (catalogEntryRecord). An entry that is a prebuilt image has
// nothing to build.
func catalogImageChecks(ctx context.Context, env *cloudEnv, rb *blobx.Bucket, slug string, cfg *config.Config, cat *config.Catalog, catUnknown bool) []doctorCheck {
	var out []doctorCheck
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		e, ok := workflowCatalogEntry(cat, catUnknown, slug, cfg.Workflows[name], catalog.Backend(env.lc.Backend()))
		// Only an entry this machine read the catalog for, and only one a
		// Fugaro build writes: an entry with no source kind is the unknown
		// catalog's (workflowCatalogEntry — the unread line says such a
		// workflow is not judged here), and a prebuilt image has nothing to
		// build.
		if !ok || (e.Source.Kind != config.SourceDockerfile && e.Source.Kind != config.SourceInline) {
			continue
		}
		slugs := catalogEntrySlugs(ctx, env, slug, e)
		rec, rerr := catalogEntryRecord(ctx, rb, slugs, e.Name)
		if rerr != nil {
			// A record that is there and unreadable is not an entry no build
			// has built: what cannot be read says nothing (catalogEntryRecord).
			continue
		}
		if rec == nil {
			// Only an entry this machine can place is called unbuilt: the
			// record lives under the slug of the repository the build ran as,
			// so an entry whose source names a repository this machine cannot
			// resolve has no place to look.
			if len(slugs) == 0 {
				continue
			}
			// The one command that can build an entry no build has built is a
			// person's build from the checkout the entry's build has to be
			// submitted from: the daily image check cannot submit one (its own
			// line says why — a rebuild is pinned to the commit the last build
			// recorded, and there is none), so a Fix naming the daily check
			// would name a run that does nothing.
			from := "this checkout"
			if e.Source.Repo != "" {
				from = e.Source.Repo + "'s checkout"
			}
			out = append(out, doctorCheck{ID: "image-catalog-" + name, Severity: "warning",
				Problem: fmt.Sprintf("workflow %s runs the project image catalog's entry %s, which no build has built: its jobs have no image to start", name, e.Name),
				Fix:     "fugaro image build " + e.Name + " from " + from + " builds it once, and the daily image check rebuilds it from then on"})
			continue
		}
		key, err := config.CatalogDockerfileKey(e.Name)
		if err != nil {
			continue
		}
		data, _, err := rb.Read(ctx, key)
		if err != nil {
			continue // no copy to compare the record's definition with
		}
		sum := sha256.Sum256(data)
		built := hex.EncodeToString(sum[:])
		if built == rec.DefinitionSHA256 {
			continue
		}
		out = append(out, doctorCheck{ID: "image-catalog-" + name, Severity: "warning",
			Problem: fmt.Sprintf("workflow %s runs the project image catalog's entry %s, whose image was built from another definition than the runs bucket holds now (sha256 %s, built from %s)", name, e.Name, shortSHA(built), shortSHA(rec.DefinitionSHA256)),
			Fix:     "the daily image check rebuilds it; to do it now: fugaro image build " + e.Name})
	}
	return out
}

// workflowCatalogEntry is the catalog entry the workflow w runs, and whether
// the project's image catalog decides its image at all. It is catalog.Applies
// — the predicate the deploy-time job spec (internal/infra) and the launch's
// image rule (run.go) ask — asked of the same catalog those two resolve
// (effectiveCatalog, handed in as cat), so doctor judges the workflows the
// deployed job's own image came from.
//
// cat is that catalog, nil when the project has none — or when it is a config
// that states none of its own beside a published node that did not answer,
// which imageCatalogUnreadCheck reports as its own line. catUnknown says which
// of those two nils it is: the second, the node that did not answer.
//
// The two nils are not one state, and the difference is what the job spec
// resolves. A project with no catalog at all is the plain one:
// internal/infra's catalogJobImage and run.go's catalogDockerAsk both stop at
// cat == nil, so every workflow's job runs the derived image, which is the
// image the per-workflow comparison is about, and a pin names an entry no
// catalog holds (a configuration mistake the job spec's own rule and
// `fugaro validate` report). A catalog that was not read is the other way
// round: the node nobody read may hold the entry a pin names, or one whose
// tags cover a workflow's needs:, or the default a workflow that names nothing
// runs, so the catalog may be what decides the workflow's image after all, and
// the comparison would then name a build of an image its jobs never run — the
// loop imageConfigChecks' own comment is about.
//
// The question asked in that state is therefore MayApply's and not Applies':
// whether the catalog could speak for this workflow at all, without asking it
// the one thing that is not known. A workflow Applies refuses only for want of
// a default is the catalog's here, because the node nobody read may mark one,
// and the entry returned is the pin's name by itself (no source) for a pin,
// and nothing at all for one the catalog would have chosen — the caller judges
// what it can of that, which is the entry it was given or nothing.
func workflowCatalogEntry(cat *config.Catalog, catUnknown bool, slug string, w config.Workflow, be catalog.Backend) (config.Entry, bool) {
	var c config.Catalog
	if cat != nil {
		c = *cat
	}
	if !catalog.Applies(c, w) {
		// The one clause of the predicate that reads the catalog is the
		// default entry, and that is exactly what a catalog nobody read does
		// not answer: a workflow that names nothing runs the default's image
		// when the node marks one, so it is left to the catalog too
		// (MayApply is the half of the predicate that reads no catalog).
		if catUnknown && catalog.MayApply(w) {
			return config.Entry{}, true
		}
		return config.Entry{}, false
	}
	if w.ImageName != "" {
		e, ok := catalogEntryNamed(&c, w.ImageName)
		switch {
		case ok:
			return e, true
		case cat == nil && catUnknown:
			return config.Entry{Name: w.ImageName}, true
		}
		// A pin the catalog does not hold is a configuration mistake the job
		// spec's own rule and `fugaro validate` report, and there is no entry
		// here to judge an image of.
		return config.Entry{}, false
	}
	if cat == nil {
		return config.Entry{}, catUnknown
	}
	choice, err := catalog.Choose(catalog.Job{Catalog: c, Repos: []catalog.Repo{{Slug: slug, Needs: w.Needs}}, Backend: be})
	if err != nil {
		return config.Entry{}, false
	}
	return choice.Entry, true
}

// catalogEntrySlugs are the repository slugs entry e's build record may be
// under. A catalog build runs as the build account of the checkout it was
// submitted from, so the record is under that checkout's repository's slug
// (gcp.CatalogRecordKey). An entry whose Dockerfile names a repository is
// built by that repository or not at all — a catalog build refuses another
// repository's entry (runImageBuildCatalog) — so its one slug is that
// repository's, and a record that is not there is not anywhere. An inline
// entry names no repository: its record is under whichever checkout built it,
// which is this one first and then every repository this machine's config
// knows, the set `fugaro image ls` reads an entry's digest from.
func catalogEntrySlugs(ctx context.Context, env *cloudEnv, slug string, e config.Entry) []string {
	if e.Source.Kind == config.SourceDockerfile && e.Source.Repo != "" {
		s, err := env.repoSlug(e.Source.Repo, func() *config.Config { return checkoutConfig(ctx, e.Source.Repo) })
		if err != nil {
			return nil
		}
		return []string{s}
	}
	out := []string{slug}
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		s, err := env.repoSlug(repo, func() *config.Config { return checkoutConfig(ctx, repo) })
		if err != nil || s == slug {
			continue
		}
		out = append(out, s)
	}
	return out
}

// catalogEntryRecord reads entry's build record from the first of slugs that
// has one: the record, nil when none does, and an error when one is there and
// could not be read.
//
// Only blobx.ErrNotExist is an absent record. Any other read error, and a
// record that does not parse, stop the search and are returned as themselves:
// the caller's "no build has built it" is a claim about a record that was not
// there to read, and a record that is there and unreadable is the state the
// daily check stops on ("nothing is submitted for it until it is fixed or
// deleted", image_check_catalog.go) — told to rebuild instead, it would name a
// build of an entry that has one.
// (A 403 is ErrNotExist before it arrives here: the non-strict read folds it,
// which is what the job and build accounts' conditional grant needs.)
// `fugaro image ls` reads the same records with the other policy
// (catalogEntryDigest reads one that does not parse as no record): a listing
// answers which digest is known, where the caller here makes a claim about
// what a build did, and that claim may not rest on a record nobody read.
func catalogEntryRecord(ctx context.Context, rb *blobx.Bucket, slugs []string, entry string) (*imagecheck.CatalogRecordState, error) {
	for _, s := range slugs {
		key, err := gcp.CatalogRecordKey(s, entry)
		if err != nil {
			continue
		}
		data, _, err := rb.Read(ctx, key)
		if errors.Is(err, blobx.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", key, err)
		}
		rec, err := imagecheck.ParseCatalogRecordState(data)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", key, err)
		}
		return rec, nil
	}
	return nil, nil
}

// imageConfigReason names where a workflow's image-affecting settings come
// from, for imageConfigChecks' message.
//
// It walked imageLeafKeys — base, image.apt, image.setup and
// image.skip_build_scripts — and named every distinct source among them. The
// repository-image removal refuses the last three by name (D1195) and base has
// been the one Fugaro base since 0.7.0, so there is no second source left for
// them to differ over and the base's own source is the whole answer.
// Resolution.SourceOf only ever sees leaves (a bare "workflows.X.image" is
// never itself a key, since the resolve.go merge records a source only for a
// scalar or a list, never for a map it recurses into), so the leaf path is
// what it is asked for.
func imageConfigReason(res *config.Resolution, name string) string {
	return fmt.Sprintf("its base comes from %s", res.SourceOf("workflows."+name+".base"))
}
