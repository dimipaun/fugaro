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
// resolved through catalog.Resolve — a repository read with the project's
// existing credentials, at the commit the entry's branch resolves to, or the
// entry's own inline bytes — and written only when the object differs, the
// read-compare-write rule layer_copy.go's writeLayerCopy uses, so republishing
// an unchanged definition does not churn the object.
//
// An entry that names a context stages two objects beside the Dockerfile
// (CB1's keys): the commit the Dockerfile and the context were both read at,
// and the digest of the context tree. They are what the build pins and what
// the nightly check compares, and they are written here, before the build, so
// the entry's own identity is complete before anything is paid for. An entry
// with no context writes neither, and its Dockerfile is the only object it
// stages, as it was before the key existed (F23).
//
// The resolution is also where a definition that carries a credential is
// refused (catalog.Resolve) and where a context over its bound is refused
// (D1398): both happen before this call copies anything into the bucket, and
// both name the entry.
//
// builds/catalog/ is outside a launcher's write grant (docs/design/
// bucket-iam.md), so a refused write is reported with the operator text
// operatorWriteErr writes, and its lead says what did not happen.
func writeCatalogDockerfile(ctx context.Context, b *blobx.Bucket, open gitprov.Opener, kind string, e config.Entry) error {
	// The keys first: a name that is not one is refused before any credential
	// is used to read a definition, and the refusal is the helper's own
	// sentence (it does not repeat the name, which is what reached it).
	key, err := config.CatalogDockerfileKey(e.Name)
	if err != nil {
		return err
	}
	var commitKey, hashKey string
	if e.Source.Context != "" {
		if commitKey, err = config.CatalogContextCommitKey(e.Name); err != nil {
			return err
		}
		if hashKey, err = config.CatalogContextHashKey(e.Name); err != nil {
			return err
		}
	}
	src, err := catalog.Resolve(ctx, open, kind, e)
	if err != nil {
		return err
	}
	if err := writeCatalogObject(ctx, b, key, src.Dockerfile, catalogDockerfileType, catalog.MaxDockerfileBytes, catalogCopyLead); err != nil {
		return err
	}
	if e.Source.Context == "" {
		return nil
	}
	// The two values are a commit and a digest: short, and read back by a
	// build's own substitution and by the check. They are written after the
	// Dockerfile so a reader that finds them finds the copy they describe,
	// and the digest last: it is the object the check reads, and an entry
	// whose digest is staged is an entry whose context was read whole.
	if err := writeCatalogObject(ctx, b, commitKey, []byte(src.Commit), catalogIdentityType, catalogIdentityBytes, catalogCommitLead); err != nil {
		return err
	}
	return writeCatalogObject(ctx, b, hashKey, []byte(src.ContextHash), catalogIdentityType, catalogIdentityBytes, catalogHashLead)
}

// writeCatalogObject puts data at key unless the object already holds it:
// layer_copy.go's read-compare-write rule, for every object a catalog copy
// stages. max is the ceiling a caller reads the object back with, so an object
// over it is treated as one this call did not write — the arm that replaces it
// without a generation to condition on, because there is nothing to read.
func writeCatalogObject(ctx context.Context, b *blobx.Bucket, key string, data []byte, contentType string, max int, lead string) error {
	old, gen, err := b.ReadMax(ctx, key, max)
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
		_, werr = b.ReplaceIfType(ctx, key, data, contentType, gen, old)
	case errors.Is(err, blobx.ErrTooLarge):
		// Unreadable, so there is no content or generation to condition on;
		// data is bounded by max, so an object over that limit is never the
		// value this call resolved.
		werr = b.Put(ctx, key, data, contentType)
	default: // blobx.ErrNotExist
		_, werr = b.Create(ctx, key, data, contentType)
	}
	if errors.Is(werr, blobx.ErrConflict) || errors.Is(werr, blobx.ErrExists) {
		// Another publisher wrote this copy between the read and the write:
		// it resolved the same definition this call did, so nothing here is
		// lost (writeLayerCopy's errNewerLayerCopy, without a caller that
		// needs to tell the two apart).
		return nil
	}
	return operatorWriteErr(url, key, werr, lead)
}

// catalogIdentityType is the content type of the two objects an entry's
// context stages: a commit and a digest are text a person reads back, stored
// as the text they are, beside a definition stored the same way.
const catalogIdentityType = "text/plain; charset=utf-8"

// catalogIdentityBytes bounds a staged commit or digest on the way back in: a
// full commit is at most 64 hex characters and a sha256 is 64, so an object
// larger than this one is not a value this command wrote, and it is replaced
// rather than read.
const catalogIdentityBytes = 128

// catalogCopyLead opens the refusal of a catalog copy: the entry's Dockerfile
// is read before anything is written, so a refused write lost nothing but the
// copy itself — a build left without it is the next thing that notices.
const catalogCopyLead = "the Dockerfile was not copied into the runs bucket"

// catalogCommitLead and catalogHashLead are the same sentence for the two
// objects an entry's context stages: what did not happen is the entry's own
// identity, and a build that reads the copy without it builds a definition
// nobody pinned.
const (
	catalogCommitLead = "the commit the entry's Dockerfile and context were read at was not staged in the runs bucket"
	catalogHashLead   = "the digest of the entry's context was not staged in the runs bucket"
)
