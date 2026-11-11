package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
// The resolution is also where a definition that carries a credential is
// refused (catalog.Fetch): the refusal happens before this call copies the
// bytes into the bucket, and it is the one sentence the operator meets wherever
// a definition is resolved rather than a rule this copy restates.
//
// builds/catalog/ is outside a launcher's write grant (docs/design/
// bucket-iam.md), so a refused write is reported with the operator text
// operatorWriteErr writes, and its lead says what did not happen.
//
// bake is the stage this build appends to the definition — the checkout of the
// repository the build runs for (imagebake, the image setup skill's bake step,
// IS8b) — and it is composed here rather than added to a build request because
// the copy is the one definition every reader of this entry reads: the build
// that pins it, the record it writes, and the nightly check that rebuilds it
// from the same object. A copy an installation's next build resolves
// identically is not rewritten, so republishing an unchanged definition does
// not churn the object either.
//
// The first result is the slug of a checkout the copy this call replaced baked
// and the definition it wrote does not, "" when the write dropped no bake. The
// bake is the one part of a copy that is not the entry's own bytes, so a build
// that composes none and finds one there is replacing another repository's
// stage: every job of the entry clones at start once it is off the image, and
// the plan the caller prints says nothing about it — the write happened at an
// entry that plan is quiet about (imagebake.Plan.Notable). The caller says it
// out loud (noteBakeDropped); a definition that already carries a bake does not
// differ from what a build composes, so nothing is dropped and no line is owed.
func writeCatalogDockerfile(ctx context.Context, b *blobx.Bucket, open gitprov.Opener, kind string, e config.Entry, bake imagebake.Plan) (dropped string, err error) {
	// The key first: a name that is not one is refused before any credential
	// is used to read a definition, and the refusal is the helper's own
	// sentence (it does not repeat the name, which is what reached it).
	key, err := config.CatalogDockerfileKey(e.Name)
	if err != nil {
		return "", err
	}
	data, err := catalog.Fetch(ctx, open, kind, e)
	if err != nil {
		return "", err
	}
	fetchLen := len(data)
	if data, err = imagebake.Append(data, bake); err != nil {
		return "", err
	}
	// The composed bytes are bounded here and not only where the entry's own
	// were resolved: every reader of this object reads it with this same limit
	// (catalog.MaxDockerfileBytes), so an object over it is one the nightly
	// check fails forever and one this call could not rewrite — an unreadable
	// copy has no generation to condition on. A definition inside the bound can
	// leave it by the generated stage alone, which is the case this refuses.
	if len(data) > catalog.MaxDockerfileBytes {
		return "", fmt.Errorf("the catalog entry %s: its Dockerfile is %d bytes and the generated checkout stage takes it to %d, more than the %d a definition may be; shorten the definition",
			pluginwire.Printable(e.Name), fetchLen, len(data), catalog.MaxDockerfileBytes)
	}
	old, gen, err := b.ReadMax(ctx, key, catalog.MaxDockerfileBytes)
	switch {
	case err == nil && bytes.Equal(old, data):
		return "", nil
	case err == nil, errors.Is(err, blobx.ErrNotExist), errors.Is(err, blobx.ErrTooLarge):
	default:
		return "", err
	}
	// What this write takes off the copy, read before it is written: the copy
	// holds a stage and the composed definition does not, so the repository that
	// composed it has a job on this image that finds no checkout from now on.
	dropped = ""
	if err == nil {
		if before, baked := imagebake.BakedSlug(old); baked {
			if _, still := imagebake.BakedSlug(data); !still {
				dropped = before
			}
		}
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
		// needs to tell the two apart) — and it is its bytes that stand, so
		// this call dropped nothing either.
		return "", nil
	}
	if err := operatorWriteErr(url, key, werr, catalogCopyLead); err != nil {
		return "", err
	}
	return dropped, nil
}

// catalogCopyLead opens the refusal of a catalog copy: the entry's Dockerfile
// is read before anything is written, so a refused write lost nothing but the
// copy itself — a build left without it is the next thing that notices.
const catalogCopyLead = "the Dockerfile was not copied into the runs bucket"
