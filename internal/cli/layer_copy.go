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

// writeLayerCopy makes builds/<slug>/project-layer.yaml, the copy the daily
// image check and Cloud Build read (decision L6), hold data, writing only
// when it differs, except that an existing object over LayerMaxBytes
// cannot be read to compare: it is always overwritten (ReadMax's old is
// nil for it, so the equality check below never fires). nil data removes
// a copy.
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
			return operatorWriteErr("the repository's copy was not removed", url, key, b.DeleteIf(ctx, key, gen, old))
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
			return operatorWriteErr("the repository's copy was not removed", url, key, b.Delete(ctx, key))
		}
		attrs, aerr := b.Attributes(ctx, key)
		if aerr != nil {
			return operatorWriteErr("the repository's copy was not removed", url, key, aerr)
		}
		var sa storage.ObjectAttrs
		if !attrs.As(&sa) || sa.Generation == 0 {
			return operatorWriteErr("the repository's copy was not removed", url, key, fmt.Errorf("%s: its generation could not be read, so it was not deleted", key))
		}
		layerCopyRemovalRace(ctx, b, key)
		return operatorWriteErr("the repository's copy was not removed", url, key, b.DeleteIf(ctx, key, sa.Generation, nil))
	}
	// Put, not WriteAll: only blobx's own writers classify a 403 as
	// blobx.ErrForbidden (docs/design/bucket-iam.md §2.3).
	return operatorWriteErr("the repository's copy was not written", url, key, b.Put(ctx, key, data, "application/yaml"))
}

// layerCopyRemovalRace is a test seam between writeLayerCopy's
// oversized-removal read of the object's generation and its conditional
// delete, mirroring config_publish.go's layerPublishRace.
var layerCopyRemovalRace = func(ctx context.Context, b *blobx.Bucket, key string) {}

// operatorWriteErr is a stand-in for bucket-iam plan Task 6's
// internal/cli/operator_write.go (docs/plans/2026-10-08-bucket-iam.md),
// not yet on main: since the runs-bucket IAM hardening (bucket iam task 5,
// merged) makes a launcher's write to fugaro/ or builds/ come back as
// blobx.ErrForbidden, a publish must say so plainly rather than failing
// with a generic remote error. isAccessDenied is also checked: it is the
// embedded gocloud Bucket's own unclassified 403 (Attributes, and the
// oversized-object delete's fallback on a driver with no generation
// concept, both go through it rather than a blobx writer), which gocloud
// maps to a NotFound-shaped error that only isAccessDenied's
// googleapi.Error check still finds. Delete this once
// operator_write.go lands with the identical function; its other call
// sites (recipes.go, sharedcfg.go, imagecheck.go) still need wiring then.
//
// prefix says what the caller's own write was about ("nothing was
// published" for the main object, "the repository's copy was not
// written/removed" for a fan-out copy): during a fan-out the main object
// IS already published, so echoing "nothing was published" there would be
// false.
func operatorWriteErr(prefix, url, key string, err error) error {
	if err == nil || !(errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)) {
		return err
	}
	return userErr("%s: writing %s in %s needs the operator role (launchers read the runs bucket but write only runs/); ask an operator to publish it, or to add you with fugaro init --operator", prefix, key, url)
}
