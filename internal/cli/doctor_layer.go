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
	"strings"
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
		// The same node answers the map, and its read error is reported once
		// (imageCatalogUnreadCheck): a map this machine could not read is the
		// file's own or none, which is the keep rule's answer for it.
		imap, _ := effectiveImageMap(ctx, env)
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
		out = append(out, imageConfigChecks(ctx, env, rb, oi.Repo, slug, rf.Cfg, rf.Res, cat, imap, catUnknown)...)
		out = append(out, catalogImageChecks(ctx, env, rb, oi.Repo, slug, rf.Cfg, cat, imap, catUnknown)...)
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

// imageConfigWorkflows are the workflows an image-config-<name> check is
// emitted for: every workflow of cfg except the two a per-workflow build
// record is never compared for.
//
// A workflow that builds from a dockerfile: is never one of them: the hash the
// check compares covers the Dockerfile's own blob, which needs a tree this run
// does not have.
//
// A workflow the project's image catalog speaks for is not one either
// (catalogImageChecks judges those by their entry's record): the per-workflow
// record compared here is not the image its jobs start on, and the fix the
// check would name, `fugaro image build --workflow`, builds an image those
// jobs never run. Whether the catalog speaks for it is workflowCatalogEntry's
// own answer, over the catalog and map the caller resolved.
//
// One predicate, two callers, so that the checks `doctor --offline` records a
// skip for are the checks that exist: the offline run cannot read the record
// (that is what the skip says), but which workflows have one at all is a fact
// of the file on disk, and this is where both runs read it.
func imageConfigWorkflows(cfg *config.Config, cat *config.Catalog, imap map[string]string, catUnknown bool, repo string, be catalog.Backend) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		w := cfg.Workflows[name]
		if w.Dockerfile != "" {
			continue
		}
		if _, ok := workflowCatalogEntry(cat, imap, catUnknown, repo, w, be); ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

// offlineImageConfigWorkflows are the image-config-<workflow> skips `fugaro
// doctor --offline` records for a resolved checkout: imageConfigWorkflows'
// own list, over the selection this run can read.
//
// That selection is the catalog and the repo-to-image map this machine's own
// project config states — the two that win over the published node's wherever
// they are stated (localcfg's keep rules, D1140 and D1906) and that cost no
// bucket — and the backend the catalog's docker rule is read against. A config
// that states neither is read as no selection and not as an unknown, which is
// the choice the skip list turns on: offline cannot tell a project with no
// catalog from one whose catalog lives in the node, and the unknown reading
// would leave every baked workflow unjudged and unsaid — the silent pass the
// classes exist to prevent — where this one still names a check the ordinary
// run may hold. The node's half of the selection is the residual, and it is
// reported rather than guessed at.
func offlineImageConfigWorkflows(ctx context.Context, root string, cfg *config.Config, lc *localcfg.Config) []string {
	cat, imap, be := (*config.Catalog)(nil), map[string]string(nil), catalog.CloudRun
	if lc != nil {
		cat, imap, be = lc.ImageCatalogEffective(nil), lc.ImageMapEffective(nil), catalog.Backend(lc.Backend())
	}
	// The repository's own name is read only for a map that could point at an
	// entry (the only thing that reads it), and under the guard every other
	// consumer of it applies (doctorLayerChecks): the checkout's raw
	// .git/config is not trusted by name, and a name this run cannot establish
	// leaves the map half unjudged, which keeps the skip.
	repo := ""
	if len(imap) > 0 {
		if oi, ok := readOrigin(ctx, root); ok && !oi.Rewritten {
			repo = oi.Repo
		}
	}
	return imageConfigWorkflows(cfg, cat, imap, false, repo, be)
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
// The workflows it judges are imageConfigWorkflows', which is the same list
// `doctor --offline` records its skips from: the flag says which checks it did
// not run, so it has to name the checks that exist and no others.
//
// cat is the catalog the job spec resolves (doctorLayerChecks), never the
// local config's alone: a workflow the published node's catalog speaks for is
// as much the catalog's as one this file's own catalog names. catUnknown is
// the other half of that argument (workflowCatalogEntry): a project with no
// catalog at all is one whose pinned workflows are compared here, because
// their jobs run the derived image — so the two nils are not one state.
func imageConfigChecks(ctx context.Context, env *cloudEnv, rb *blobx.Bucket, repo, slug string, cfg *config.Config, res *config.Resolution, cat *config.Catalog, imap map[string]string, catUnknown bool) []doctorCheck {
	var out []doctorCheck
	for _, name := range imageConfigWorkflows(cfg, cat, imap, catUnknown, repo, catalog.Backend(env.lc.Backend())) {
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
// nothing to build, and what it is judged on instead is the smoke of the
// reference it names (prebuiltSmokeChecks).
func catalogImageChecks(ctx context.Context, env *cloudEnv, rb *blobx.Bucket, repo, slug string, cfg *config.Config, cat *config.Catalog, imap map[string]string, catUnknown bool) []doctorCheck {
	var out []doctorCheck
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		e, ok := workflowCatalogEntry(cat, imap, catUnknown, repo, cfg.Workflows[name], catalog.Backend(env.lc.Backend()))
		if !ok {
			continue
		}
		// A prebuilt entry is never built, so what says whether its image is
		// one a job can start on is the smoke of it (F10/IS11), judged on the
		// record that run writes.
		if e.Source.Kind == config.SourceImage {
			out = append(out, prebuiltSmokeChecks(ctx, env, rb, slug, name, e)...)
			continue
		}
		// Only an entry a Fugaro build writes: an entry with no source kind is
		// the unknown catalog's (workflowCatalogEntry — the unread line says
		// such a workflow is not judged here).
		if e.Source.Kind != config.SourceDockerfile && e.Source.Kind != config.SourceInline {
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

// prebuiltSmokeChecks judges a workflow whose entry is a prebuilt image: the
// reference the entry names is what its jobs start on, and the only thing that
// says the image meets the runtime contract is the smoke-only run of it
// (F10/IS11). That run is the one writer of the entry's record, and a Cloud
// Build stops at a failed step, so a record that is absent is both states at
// once — the reference was never smoked, or its smoke failed — and there is
// nothing else the difference could be read from. The sentence says so rather
// than claiming which one it was, and the fix is the run itself: the smoke
// fails loudly when the image is not one a job can start on.
func prebuiltSmokeChecks(ctx context.Context, env *cloudEnv, rb *blobx.Bucket, slug, workflow string, e config.Entry) []doctorCheck {
	slugs := catalogEntrySlugs(ctx, env, slug, e)
	if len(slugs) == 0 {
		// The record lives under the slug of the repository the run was
		// submitted as, so an entry this machine cannot place has no place to
		// look (catalogEntryRecord, and the built entry's own arm above).
		return nil
	}
	rec, err := catalogEntryRecord(ctx, rb, slugs, e.Name)
	if err != nil {
		// A record that is there and unreadable is not a reference nobody
		// smoked: what cannot be read says nothing (catalogEntryRecord).
		return nil
	}
	const id = "image-catalog-"
	if rec == nil {
		return []doctorCheck{{ID: id + workflow, Severity: "warning",
			Problem: fmt.Sprintf("workflow %s runs the project image catalog's entry %s, the prebuilt image %s, and no smoke of that reference has been recorded: either it was never smoked or its smoke failed, and its jobs start on an image nothing has checked against the runtime contract", workflow, e.Name, e.Source.Image),
			Fix:     "fugaro image build " + e.Name + " smokes it in Cloud Build, which fails there if the image is not one a job can start on"}}
	}
	if rec.Image == e.Source.Image {
		return nil
	}
	tested := rec.Image
	if tested == "" {
		tested = "no reference at all"
	}
	return []doctorCheck{{ID: id + workflow, Severity: "warning",
		Problem: fmt.Sprintf("workflow %s runs the project image catalog's entry %s, the prebuilt image %s, and the last smoke of the entry tested %s: the reference its jobs start on has not been smoked", workflow, e.Name, e.Source.Image, tested),
		Fix:     "fugaro image build " + e.Name + " smokes the reference the entry names now"}}
}

// workflowCatalogEntry is the catalog entry the workflow w runs, and whether
// the project's image catalog decides its image at all. It asks the one
// resolver the deploy-time job spec (internal/infra) and the launch's image
// rule (run.go) ask (catalog.JobImage), of the same catalog those two resolve
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
// internal/infra's jobImage and run.go's catalogDockerAsk both stop at
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
// The question asked in that state is therefore MayApply's and not the
// resolver's: whether the catalog could speak for this workflow at all,
// without asking it the one thing that is not known. A workflow the resolver
// would give no entry for want of a default is the catalog's here, because the
// node nobody read may mark one,
// and the entry returned is the pin's name by itself (no source) for a pin,
// and nothing at all for one the catalog would have chosen — the caller judges
// what it can of that, which is the entry it was given or nothing.
func workflowCatalogEntry(cat *config.Catalog, imap map[string]string, catUnknown bool, repo string, w config.Workflow, be catalog.Backend) (config.Entry, bool) {
	sel, err := catalog.JobImage(catalog.ImageRequest{
		Catalog:   cat,
		ImageMap:  imap,
		Canonical: repo,
		Workflow:  w,
		Backend:   be,
	})
	if err == nil && sel.Source == catalog.SourceCatalog {
		return sel.Entry, true
	}
	switch {
	case err != nil:
		// A configuration the resolver refuses — a pin or a map entry the
		// catalog does not hold, a docker-tagged entry on a backend with no
		// daemon — is a mistake the job spec's own rule and `fugaro validate`
		// report, and there is no entry here to judge an image of.
		return config.Entry{}, false
	case cat == nil && catUnknown && catalog.MayApply(w):
		// A project whose images nobody read may hold the entry a pin names,
		// one the map points this repository at, or the default a workflow
		// that names nothing runs, so the workflow is left to the selection
		// after all — MayApply is the half of the predicate that reads
		// neither the catalog nor the map. The pin is the one name this state
		// has, and whether the node holds it is exactly what nobody read.
		return config.Entry{Name: w.ImageName}, true
	}
	return config.Entry{}, false
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

// imageLeafKeys are the leaf paths under workflows.<name>. that decide a
// generated image's content (the fields imagecheck.ImageConfigHash hashes):
// base is a scalar on its own; the rest are image:'s own leaves.
// Resolution.SourceOf only ever sees leaves (a bare "workflows.X.image" is
// never itself a key, since the resolve.go merge records a source only for
// a scalar or a list, never for a map it recurses into), so asking it for
// "workflows.X.image" the way an earlier version of this function did
// always answered SourceDefault, silently never attributing an image
// override to anything.
// image.node and image.jdk were removed in 0.7.0 (image.tools replaces
// them, but as a map it has no one leaf path to ask Resolution.SourceOf for:
// its own leaves are image.tools.<name>, one per declared tool, so it is
// left out here rather than named at a path that can never match).
var imageLeafKeys = []string{"base", "image.apt", "image.setup", "image.skip_build_scripts"}

// imageConfigReason names where a workflow's image-affecting settings come
// from, for imageConfigChecks' message: base's own source first, then every
// other distinct non-default source among the rest of imageLeafKeys (at
// most one more in practice: a workflow has one profile and one repo
// override, so sources are limited to SourceRepo and one SourceProfile).
func imageConfigReason(res *config.Resolution, name string) string {
	baseFrom := res.SourceOf("workflows." + name + ".base")
	others := map[string]bool{}
	for _, k := range imageLeafKeys[1:] {
		if s := res.SourceOf("workflows." + name + "." + k); s != config.SourceDefault && s != baseFrom {
			others[s] = true
		}
	}
	if len(others) == 0 {
		return fmt.Sprintf("its base comes from %s", baseFrom)
	}
	return fmt.Sprintf("its base comes from %s and its image settings from %s", baseFrom, strings.Join(slices.Sorted(maps.Keys(others)), ", "))
}
