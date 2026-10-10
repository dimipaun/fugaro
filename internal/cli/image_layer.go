package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// layerSHAOf is the sum of the project layer cfg was resolved over, "" for
// none: what a build of it is submitted with.
func layerSHAOf(cfg *config.Config) string {
	if cfg == nil || cfg.Layer == nil {
		return ""
	}
	return cfg.Layer.SHA256
}

// prepareLayerCopy makes the repository's copy hold the layer a build is
// about to resolve against (decision L6) and returns its sum for the
// build. A layer needs a base whose fugaro renders with it (layeredSince):
// an older release base is refused before anything is written. Callers
// call it only with a layer; a build without one never touches a copy
// (to retire a layer, publish one with no defaults and no profiles).
func prepareLayerCopy(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer, base string) (string, error) {
	if m := baseRefReleaseRE.FindStringSubmatch(base); m != nil && imagePredates(m[2], layeredSince) {
		return "", userErr("the project layer needs a base image whose fugaro reads it (fugaro %s or later), but this build starts from %s, release %s: run fugaro image refresh in the checkout, in your own terminal window",
			layeredSince, pluginwire.Printable(base), m[2])
	}
	if err := writeLayerCopy(ctx, b, slug, l.Raw); err != nil {
		return "", remote(fmt.Errorf("writing the project layer copy %s: %w", config.LayerCopyKey(slug), err))
	}
	return l.SHA256, nil
}

// readLayerCopy reads the repository's copy of the project layer for a
// Cloud Build render, as the build account, and refuses one whose sum is
// not the one the build was submitted with.
func readLayerCopy(ctx context.Context, bucketURL, slug, sha string) ([]byte, error) {
	b, err := blobx.Open(ctx, bucketURL)
	if err != nil {
		return nil, remote(err)
	}
	defer b.Close()
	key := config.LayerCopyKey(slug)
	data, _, err := b.ReadMax(ctx, key, config.LayerMaxBytes)
	if err != nil {
		return nil, remote(fmt.Errorf("reading the project layer copy %s: %w", key, err))
	}
	if got := config.LayerSum(data); got != sha {
		return nil, userErr("the project layer changed while the build was queued (its copy %s has sha256 %s, the build was submitted with %s): run the build again", key, got, pluginwire.Printable(sha))
	}
	return data, nil
}
