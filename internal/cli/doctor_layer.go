package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// doctorLayerChecks are doctor's project layer lines (decision L20), in a
// checkout whose fugaro.yaml resolves:
//   - the layer that applies (info);
//   - whether the repository's last run used it;
//   - whether the repository's copy, which the daily check and Cloud Build
//     read, is current;
//   - whether each workflow's image was built from the image settings it
//     resolves to now.
//
// Best effort: what cannot be read says nothing.
func doctorLayerChecks(ctx context.Context, lc *localcfg.Config, root string) []doctorCheck {
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	if err != nil {
		return nil
	}
	rf, err := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
	if err != nil || rf.Cfg == nil {
		return nil // fugaroYAMLCheck reports it
	}
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
	oi, ok := readOrigin(ctx, root)
	if !ok {
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
	defer b.Close()
	out = append(out, layerDrift(ctx, b, slug, l)...)
	out = append(out, layerCopyCheck(ctx, b, slug, l)...)
	env := &cloudEnv{lc: lc, bucket: b}
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
		from := res.SourceOf("workflows." + name + ".base")
		if s := res.SourceOf("workflows." + name + ".image"); s != config.SourceDefault {
			from = s
		}
		out = append(out, doctorCheck{ID: "image-config-" + name, Severity: "warning",
			Problem: fmt.Sprintf("workflow %s's job image was built from other image settings than it resolves to now (its base and image settings come from %s)", name, from),
			Fix:     "the daily image check rebuilds it; to do it now: fugaro image build --workflow " + name})
	}
	return out
}
