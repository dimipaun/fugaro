package cli

import (
	"bytes"
	"context"
	"errors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
)

// writeLayerCopy makes builds/<slug>/project-layer.yaml, the copy the daily
// image check and Cloud Build read (decision L6), hold data, writing only
// when it differs; nil data removes a copy.
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
			return operatorWriteErr(url, key, b.DeleteIf(ctx, key, gen, old))
		}
		// ErrTooLarge: the oversized object's generation is not known.
		return operatorWriteErr(url, key, b.Delete(ctx, key))
	}
	// Put, not WriteAll: only blobx's own writers classify a 403 as
	// blobx.ErrForbidden (docs/design/bucket-iam.md §2.3).
	return operatorWriteErr(url, key, b.Put(ctx, key, data, "application/yaml"))
}

// operatorWriteErr is a stand-in for bucket-iam plan Task 6's
// internal/cli/operator_write.go (docs/plans/2026-10-08-bucket-iam.md),
// not yet on main: since the runs-bucket IAM hardening (bucket iam task 5,
// merged) makes a launcher's write to fugaro/ or builds/ come back as
// blobx.ErrForbidden, a publish must say so plainly rather than failing
// with a generic remote error. isAccessDenied is also checked: it is the
// embedded gocloud Bucket's own unclassified 403 (the oversized-object
// delete above goes through it, not a blobx writer, since its generation
// is not known), which gocloud maps to a NotFound-shaped error that only
// isAccessDenied's googleapi.Error check still finds. Delete this once
// operator_write.go lands with the identical function; its other call
// sites (recipes.go, sharedcfg.go, imagecheck.go) still need wiring then.
func operatorWriteErr(url, key string, err error) error {
	if err == nil || !(errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)) {
		return err
	}
	return userErr("nothing was published: writing %s in %s needs the operator role (launchers read the runs bucket but write only runs/); ask an operator to publish it, or to add you with fugaro init --operator", key, url)
}
