package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
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
//     resolves to now;
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
		out = append(out, imageConfigChecks(ctx, rb, slug, rf.Cfg, rf.Res)...)
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
	if data, err := os.ReadFile(path); err == nil {
		st.Exists, st.SHA256 = true, config.LayerSum(data)
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
// means the owner's own edit, an older one means the published object has
// moved past this file (someone else's publish, which publishing would
// revert), and none at all means no publish this machine can compare with.
func layerLocalCause(st layerLocalState) string {
	recorded, err := strconv.ParseInt(st.Recorded, 10, 64)
	switch {
	case st.Recorded == "" || err != nil:
		return layerLocalUnrecorded
	case recorded < st.Generation:
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
		return fmt.Sprintf("unpublished: the local layer file %s (sha256 %s) differs from %s (sha256 %s): the published layer has moved past generation %s this file was written from, so this is %s — run fugaro update to re-seed it from the published one",
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

// imageConfigChecks names each workflow (with a generated image) whose build
// record's image-config hash is not the hash of what it resolves to now:
// typically a profile's base or image settings changed.
func imageConfigChecks(ctx context.Context, rb *blobx.Bucket, slug string, cfg *config.Config, res *config.Resolution) []doctorCheck {
	var out []doctorCheck
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		if cfg.Workflows[name].Dockerfile != "" {
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
