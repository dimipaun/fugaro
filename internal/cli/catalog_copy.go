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
//
// What it answers with is what it staged: the bytes, the commit and the
// digest the objects were written from. The caller reads those objects back
// to build its request from what the bucket holds rather than from what this
// process resolved a moment earlier, and the values it staged are what tells
// it the copy it read back is this one and not another publisher's
// (refuseMixedCatalogCopy).
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
// The second result is the slug of a checkout the copy this call replaced baked
// and the definition it wrote does not, "" when the write dropped no bake. The
// bake is the one part of a copy that is not the entry's own bytes, so a build
// that composes none and finds one there is replacing another repository's
// stage: every job of the entry clones at start once it is off the image, and
// the plan the caller prints says nothing about it — the write happened at an
// entry that plan is quiet about (imagebake.Plan.Notable). The caller says it
// out loud (noteBakeDropped); a definition that already carries a bake does not
// differ from what a build composes, so nothing is dropped and no line is owed.
func writeCatalogDockerfile(ctx context.Context, b *blobx.Bucket, open gitprov.Opener, kind string, e config.Entry, bake imagebake.Plan) (catalog.Resolved, string, error) {
	// The keys first: a name that is not one is refused before any credential
	// is used to read a definition, and the refusal is the helper's own
	// sentence (it does not repeat the name, which is what reached it).
	key, err := config.CatalogDockerfileKey(e.Name)
	if err != nil {
		return catalog.Resolved{}, "", err
	}
	var commitKey, hashKey string
	if e.Source.Context != "" {
		if commitKey, err = config.CatalogContextCommitKey(e.Name); err != nil {
			return catalog.Resolved{}, "", err
		}
		if hashKey, err = config.CatalogContextHashKey(e.Name); err != nil {
			return catalog.Resolved{}, "", err
		}
	}
	src, err := catalog.Resolve(ctx, open, kind, e)
	if err != nil {
		return catalog.Resolved{}, "", err
	}
	fetchLen := len(src.Dockerfile)
	data, err := imagebake.Append(src.Dockerfile, bake)
	if err != nil {
		return catalog.Resolved{}, "", err
	}
	// The composed bytes are bounded here and not only where the entry's own
	// were resolved: every reader of this object reads it with this same limit
	// (catalog.MaxDockerfileBytes), so an object over it is one the nightly
	// check fails forever and one this call could not rewrite -- an unreadable
	// copy has no generation to condition on. A definition inside the bound can
	// leave it by the generated stage alone, which is the case this refuses.
	if len(data) > catalog.MaxDockerfileBytes {
		return catalog.Resolved{}, "", fmt.Errorf("the catalog entry %s: its Dockerfile is %d bytes and the generated checkout stage takes it to %d, more than the %d a definition may be; shorten the definition",
			pluginwire.Printable(e.Name), fetchLen, len(data), catalog.MaxDockerfileBytes)
	}
	// The result carries the composed bytes: they are what the bucket holds
	// after this call, and what the caller's read-back is compared with.
	src.Dockerfile = data
	// What this write takes off the copy, read before it is written: the copy
	// holds a stage and the composed definition does not, so the repository that
	// composed it has a job on this image that finds no checkout from now on.
	dropped := ""
	if old, _, rerr := b.ReadMax(ctx, key, catalog.MaxDockerfileBytes); rerr == nil && !bytes.Equal(old, data) {
		if before, baked := imagebake.BakedSlug(old); baked {
			if _, still := imagebake.BakedSlug(data); !still {
				dropped = before
			}
		}
	}
	if err := writeCatalogObject(ctx, b, key, data, catalogDockerfileType, catalog.MaxDockerfileBytes, catalogCopyLead); err != nil {
		return catalog.Resolved{}, "", err
	}
	if e.Source.Context == "" {
		return src, dropped, nil
	}
	// The two values are a commit and a digest: short, and read back by a
	// build's own substitution and by the check. They are written after the
	// Dockerfile so a reader that finds them finds the copy they describe,
	// and the digest last: it is the object the check reads, and an entry
	// whose digest is staged is an entry whose context was read whole.
	if err := writeCatalogObject(ctx, b, commitKey, []byte(src.Commit), catalogIdentityType, catalogIdentityBytes, catalogCommitLead); err != nil {
		return catalog.Resolved{}, "", err
	}
	if err := writeCatalogObject(ctx, b, hashKey, []byte(src.ContextHash), catalogIdentityType, catalogIdentityBytes, catalogHashLead); err != nil {
		return catalog.Resolved{}, "", err
	}
	return src, dropped, nil
}

// refuseMixedCatalogCopy refuses a copy that is not the one this command
// staged: the definition read back from the bucket, the commit and the context
// digest have to be the three values one resolution answered (src).
//
// They are three objects, written one after another and read back the same way,
// and two publishers of one entry interleave — the copy's own read-compare-write
// assumes one (writeCatalogObject). A bucket holding one publisher's definition
// beside another's commit and digest is what that leaves, and a request built
// from it would pass the build's pin step, which sees the definition alone (its
// generation and its sha256), and then build one revision's Dockerfile on
// another revision's context, recording the stale commit beside the newer
// definition's digest. Nothing here can order two writers' objects, so the mix
// is refused before anything is submitted: the copy's read-compare-write leaves
// the bucket holding one revision's three objects, and the next run reads that
// whole copy.
//
// part names the value that differed, so the sentence says which of the three
// moved.
func refuseMixedCatalogCopy(e config.Entry, src catalog.Resolved, definition []byte, commit, hash string) error {
	part := ""
	switch {
	case !bytes.Equal(definition, src.Dockerfile):
		part = "Dockerfile"
	case commit != src.Commit:
		part = "commit"
	case hash != src.ContextHash:
		part = "context digest"
	}
	if part == "" {
		return nil
	}
	return userErr("nothing was submitted: the copy of %s in the runs bucket is not the one this command staged — another publisher staged a different revision of it while this ran (the %s read back is not the one this command copied); run the command again",
		pluginwire.Printable(e.Name), part)
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

// catalogCopyRace is a test seam between a catalog copy and the submission's
// read-back of it, mirroring layer_copy.go's layerCopyWriteRace: a test hook
// can stage another publisher's copy of the entry here, which is the
// interleaving the read-back refuses (refuseMixedCatalogCopy) and the one
// nothing else can put in a test's way.
var catalogCopyRace = func(ctx context.Context, b *blobx.Bucket, e config.Entry) {}
