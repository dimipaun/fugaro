package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/storage"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
)

// errNewerLayerCopy is writeLayerCopy's report that, between its own read
// of the copy and its write, another publisher's fan-out already wrote a
// copy here: the object in place is at least as current as data, so
// nothing of this publish is lost. fanOutLayer reports this and stops
// rather than treating it as a failed write of this publish's own.
var errNewerLayerCopy = errors.New("a newer publish already wrote this repository's copy")

// writeLayerCopy makes builds/<slug>/project-layer.yaml, the copy the daily
// image check and Cloud Build read (decision L6), hold data, writing only
// when it differs, except that an existing object over LayerMaxBytes
// cannot be read to compare: it is always overwritten (ReadMax's old is
// nil for it, so the equality check below never fires). nil data removes
// a copy. The write itself is conditioned on the copy this call read
// (ReplaceIfType when it existed, Create when it did not), so a publisher
// slower than a concurrent one can never overwrite that one's newer copy
// with this call's older data (errNewerLayerCopy instead); the oversized
// case stays an unconditional Put, as it always was, since there is
// nothing here to condition it on.
func writeLayerCopy(ctx context.Context, b *blobx.Bucket, slug string, data []byte) error {
	key := config.LayerCopyKey(slug)
	old, gen, err := b.ReadMax(ctx, key, config.LayerMaxBytes)
	exists := err == nil || errors.Is(err, blobx.ErrTooLarge)
	switch {
	case err == nil && data != nil && bytes.Equal(old, data):
		return nil
	case exists, errors.Is(err, blobx.ErrNotExist):
	default:
		return err
	}
	url := "gs://" + b.GCSName
	if data == nil {
		if !exists {
			return nil
		}
		if err == nil {
			// gen is known: delete under a precondition, so a concurrent
			// writer's copy is never dropped out from under it.
			return operatorWriteErr(url, key, b.DeleteIf(ctx, key, gen, old), layerCopyRemoveLead)
		}
		// ErrTooLarge: the content is unreadable, but the object's
		// generation is still available as metadata (Attributes is a
		// HEAD, not a GET of the oversized body): delete under that
		// precondition too, so a concurrent writer's replacement (even
		// one no longer oversized) is never dropped out from under it;
		// ErrConflict there means someone replaced it, and the
		// replacement is left alone, not deleted. GCSName is "" only for
		// a driver with no generation concept (file://, mem://: the
		// single-writer dev and test fakes, never a real concurrent
		// writer), which is the only case with nothing to match against;
		// any other failure to read the generation (a transient GCS
		// error, a permissions problem, a concurrent delete) must not
		// fall back to an unconditional delete, which would defeat the
		// whole precondition: it is reported instead, deleting nothing.
		if b.GCSName == "" {
			return operatorWriteErr(url, key, b.Delete(ctx, key), layerCopyRemoveLead)
		}
		attrs, aerr := b.Attributes(ctx, key)
		if aerr != nil {
			return operatorWriteErr(url, key, aerr, layerCopyRemoveLead)
		}
		var sa storage.ObjectAttrs
		if !attrs.As(&sa) || sa.Generation == 0 {
			return operatorWriteErr(url, key, fmt.Errorf("%s: its generation could not be read, so it was not deleted", key), layerCopyRemoveLead)
		}
		layerCopyRemovalRace(ctx, b, key)
		return operatorWriteErr(url, key, b.DeleteIf(ctx, key, sa.Generation, nil), layerCopyRemoveLead)
	}
	var werr error
	switch {
	case err == nil:
		// The copy exists and was read whole: condition the write on
		// exactly what this call read, so a concurrent writer's newer
		// copy (gen on GCS, content on file:// and mem://, same as
		// ReplaceIfType's own two drivers) is never clobbered.
		layerCopyWriteRace(ctx, b, key)
		_, werr = b.ReplaceIfType(ctx, key, data, "application/yaml", gen, old)
	case errors.Is(err, blobx.ErrTooLarge):
		// Unreadable: there is no content or generation to condition on,
		// so this stays the unconditional overwrite the doc comment above
		// already promises (Put, not WriteAll: only blobx's own writers
		// classify a 403 as blobx.ErrForbidden, docs/design/bucket-iam.md
		// §2.3).
		werr = b.Put(ctx, key, data, "application/yaml")
	default: // errors.Is(err, blobx.ErrNotExist)
		layerCopyWriteRace(ctx, b, key)
		_, werr = b.Create(ctx, key, data, "application/yaml")
	}
	if errors.Is(werr, blobx.ErrConflict) || errors.Is(werr, blobx.ErrExists) {
		return errNewerLayerCopy
	}
	return operatorWriteErr(url, key, werr, layerCopyWriteLead)
}

// layerCopyWriteLead and layerCopyRemoveLead are writeLayerCopy's own
// lead-ins for operatorWriteErr (internal/cli/operator_write.go):
// writeLayerCopy's only caller is fanOutLayer, always after the project
// layer itself has already been published, so operatorWriteErr's default
// ("nothing was published") would be false here.
const (
	layerCopyWriteLead  = "the layer was published, but this repository's copy was not written"
	layerCopyRemoveLead = "the layer was published, but this repository's copy was not removed"
)

// layerCopyRemovalRace is a test seam between writeLayerCopy's
// oversized-removal read of the object's generation and its conditional
// delete, mirroring config_publish.go's layerPublishRace.
var layerCopyRemovalRace = func(ctx context.Context, b *blobx.Bucket, key string) {}

// layerCopyWriteRace is a test seam between writeLayerCopy's read of the
// copy (old, gen) and its conditional write, mirroring layerCopyRemovalRace
// above and config_publish.go's layerPublishRace: a test hook can write a
// different copy here to simulate a faster, concurrent publisher's fan-out
// landing first.
var layerCopyWriteRace = func(ctx context.Context, b *blobx.Bucket, key string) {}
