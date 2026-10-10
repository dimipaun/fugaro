package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
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
//     resolves to now.
//
// Best effort: what cannot be read says nothing.
func doctorLayerChecks(ctx context.Context, lc *localcfg.Config, root string, rf resolvedFile) []doctorCheck {
	l := rf.Layer.Layer
	if l == nil {
		msg := "no project layer applies to this checkout"
		if rf.Layer.Unknown {
			msg = "whether a project layer applies is unknown"
		}
		if rf.Layer.Note != "" {
			msg += ": " + rf.Layer.Note
		}
		return []doctorCheck{{ID: "project-layer", Severity: "info", Problem: msg}}
	}
	out := []doctorCheck{{ID: "project-layer", Severity: "info",
		Problem: fmt.Sprintf("project layer %s generation %d (sha256 %s) applies; fugaro config show prints where each value comes from", l.Project, rf.Layer.Generation, shortSHA(l.SHA256))}}
	// A rewritten origin (the checkout's raw .git/config disagreeing with
	// what git resolves) is not trusted by name (readOrigin's doc comment),
	// the same guard every other consumer of it in this package applies
	// (checkoutOriginAt, repoKnown): otherwise a spoofed .git/config could
	// make doctor compute another repository's slug and show its
	// layer-drift, copy-staleness and image-config state here.
	oi, ok := readOrigin(ctx, root)
	if !ok || oi.Rewritten {
		return out
	}
	slug, err := task.Slug(rf.Cfg.Git.Provider, oi.Repo)
	if err != nil {
		return out
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return out
	}
	env := &cloudEnv{lc: lc, bucket: b}
	defer env.Close() // closes b, and env.records if recordBucket opened one
	out = append(out, layerDrift(ctx, b, slug, l)...)
	out = append(out, layerCopyCheck(ctx, b, slug, l)...)
	if rb, err := env.recordBucket(ctx); err == nil {
		out = append(out, imageConfigChecks(ctx, rb, slug, rf.Cfg, rf.Res)...)
	}
	return out
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
		return []doctorCheck{{ID: "project-layer-drift", Severity: "info",
			Problem: fmt.Sprintf("the last run %s used project layer generation %d (sha256 %s); the published one is sha256 %s", id, pl.Generation, pluginwire.Printable(shortSHA(pl.SHA256)), shortSHA(l.SHA256)),
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
var imageLeafKeys = []string{"base", "image.node", "image.jdk", "image.apt", "image.setup", "image.skip_build_scripts"}

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
