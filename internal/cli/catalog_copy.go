package cli

import (
	"bytes"
	"context"
	"errors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// catalogDockerfileType is the copied definition's content type: a
// Dockerfile is text a person reads, and the copy is what a build hands to
// Docker, so it is stored as the text it is.
const catalogDockerfileType = "text/plain; charset=utf-8"

// writeCatalogDockerfile puts e's Dockerfile in the project's runs bucket at
// config.CatalogDockerfileKey(e.Name), so a build reads one object instead
// of a checkout (D1140, spec.md §Where Dockerfiles live). The definition is
// resolved through catalog.Fetch — a repository read with the project's
// existing credentials, or the entry's own inline bytes — and written only
// when the object differs, the read-compare-write rule layer_copy.go's
// writeLayerCopy uses, so republishing an unchanged definition does not
// churn the object.
//
// builds/catalog/ is outside a launcher's write grant (docs/design/
// bucket-iam.md), so a refused write is reported with the operator text
// operatorWriteErr writes, and its lead says what did not happen.
func writeCatalogDockerfile(ctx context.Context, b *blobx.Bucket, open gitprov.Opener, kind string, e config.Entry) error {
	data, err := catalog.Fetch(ctx, open, kind, e)
	if err != nil {
		return userErr("%v", err)
	}
	key, err := config.CatalogDockerfileKey(e.Name)
	if err != nil {
		return userErr("%v", err)
	}
	old, gen, err := b.ReadMax(ctx, key, catalog.MaxDockerfileBytes)
	switch {
	case err == nil && bytes.Equal(old, data):
		return nil
	case err == nil, errors.Is(err, blobx.ErrNotExist), errors.Is(err, blobx.ErrTooLarge):
	default:
		return err
	}
	url := "gs://" + b.GCSName
	var werr error
	switch {
	case err == nil:
		// The copy exists and was read whole: condition the write on exactly
		// what this call read, so a concurrent publisher's newer copy is
		// never clobbered with this one's older bytes.
		_, werr = b.ReplaceIfType(ctx, key, data, catalogDockerfileType, gen, old)
	case errors.Is(err, blobx.ErrTooLarge):
		// Unreadable, so there is no content or generation to condition on;
		// data is bounded by catalog.MaxDockerfileBytes, so an object over
		// that limit is never the definition this call resolved.
		werr = b.Put(ctx, key, data, catalogDockerfileType)
	default: // blobx.ErrNotExist
		_, werr = b.Create(ctx, key, data, catalogDockerfileType)
	}
	if errors.Is(werr, blobx.ErrConflict) || errors.Is(werr, blobx.ErrExists) {
		// Another publisher wrote this copy between the read and the write:
		// it resolved the same definition this call did, so nothing here is
		// lost (writeLayerCopy's errNewerLayerCopy, without a caller that
		// needs to tell the two apart).
		return nil
	}
	return operatorWriteErr(url, key, werr, catalogCopyLead)
}

// catalogCopyLead opens the refusal of a catalog copy: the entry's Dockerfile
// is read before anything is written, so a refused write lost nothing but the
// copy itself — a build left without it is the next thing that notices.
const catalogCopyLead = "the Dockerfile was not copied into the runs bucket"
