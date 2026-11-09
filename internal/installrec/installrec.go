// Package installrec reads and writes the installation's version record:
// the release `fugaro update` last applied and the release the installation
// was on before it (docs/issues-resolved-on-update/reconciliation.md F2,
// F17, D1757).
//
// The record is one object in the runs bucket, beside fugaro/config.yaml,
// and it is operator-written: applying the installation is the update door,
// which is the only caller of Advance. It answers one question — which two
// releases the last update spanned — so the update summary and the daily
// report can list the issues fixed in that window. It is not a lock and
// not a schedule: an absent record means "no window", which is a normal
// state, and a write this machine may not make is a skip; a record that
// cannot be read is an error rather than something to repair in place.
package installrec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// Object is the record's object in the runs bucket. It sits beside
// fugaro/config.yaml, under the prefix the update door may write and a
// launcher may only read (docs/design/bucket-iam.md).
const Object = "fugaro/installed.json"

// Format is the record's format number, the one this build reads and
// writes. A field never changes meaning, so a later shape that has to
// change one raises this instead.
const Format = 1

// contentType is what the object is written as.
const contentType = "application/json"

// maxBytes bounds the record read: the object is a few dozen bytes, and
// anyone holding write access to the runs bucket can replace it.
const maxBytes = 4 << 10

// writeAttempts is how many times Advance re-reads and retries after
// another machine's update wrote the object between its read and its
// write. Two admins updating at once, or an update and a report, is the
// whole of it, and each retry starts from what the other left, so one
// retry settles it; the third is room, and then the record stands as it
// is rather than the update failing over it.
const writeAttempts = 3

// Record is the object's content: the release the last update applied and
// the release before it, empty in the first record an installation writes.
type Record struct {
	Format          int    `json:"format"`
	Release         string `json:"release"`
	PreviousRelease string `json:"previous_release"`
}

// ErrNotWriter means this machine may not write the record (an HTTP 403):
// the runs bucket's fugaro/ prefix is the operator's, so a launcher's
// config-only update is refused it. Callers treat it as a skip — the record
// is an answer to a question the update asks itself, and one it may not
// write is not one it must fail over.
var ErrNotWriter = errors.New("the installation record is written by the update door, which this machine is not")

// Reader is the read half of a bucket: what Read needs. *blobx.Bucket
// satisfies it, and a test wraps one.
type Reader interface {
	ReadMax(ctx context.Context, key string, limit int) ([]byte, int64, error)
}

// Writer is Reader plus the two writes Advance makes: create-if-absent,
// then generation-matched replace. The split is not cosmetic: a launcher
// reads the record (the report's window) without being allowed to write it,
// so the read is what that caller gets and the writer check sits where the
// write is.
type Writer interface {
	Reader
	Create(ctx context.Context, key string, data []byte, contentType string) (int64, error)
	ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error)
}

var _ Writer = (*blobx.Bucket)(nil)

// Read returns the record, and the zero Record when there is none: an
// installation that has never updated with a build that writes this object
// has no window, which is a normal state and not an error. A release is
// returned in the canonical form updatecheck reads one in.
//
// A record this build cannot read is an error: an unknown field, a format
// other than Format, data after the object, a release that is not a version,
// an object past maxBytes, or a bucket that could not answer. Nothing
// repairs it — a wrong value that is itself well-formed has no way to be
// told from a right one — so the answer is the operator's: delete the object
// and the next update writes it again.
func Read(ctx context.Context, r Reader) (Record, error) {
	rec, _, _, _, err := read(ctx, r)
	return rec, err
}

// read is Read plus what Advance needs to write back: the bytes it read, the
// generation they were read at, and whether the object was there at all.
func read(ctx context.Context, r Reader) (rec Record, data []byte, gen int64, absent bool, err error) {
	data, gen, err = r.ReadMax(ctx, Object, maxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return Record{}, nil, 0, true, nil
	case err != nil:
		return Record{}, nil, 0, false, fmt.Errorf("reading %s: %w", Object, err)
	}
	rec, err = parse(data)
	if err != nil {
		return Record{}, nil, 0, false, err
	}
	return rec, data, gen, false, nil
}

// parse decodes the record strictly, as every format a second machine reads
// is decoded: a field this build does not know would be dropped by the next
// write, for everyone.
func parse(data []byte) (Record, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec Record
	if err := dec.Decode(&rec); err != nil {
		return Record{}, fmt.Errorf("%s: %w", Object, err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Record{}, fmt.Errorf("%s: data after the object", Object)
	}
	if rec.Format != Format {
		return Record{}, fmt.Errorf("%s: format %d is not supported (this build reads %d)", Object, rec.Format, Format)
	}
	release, ok := updatecheck.CanonicalRelease(rec.Release)
	if !ok {
		return Record{}, fmt.Errorf("%s: %q is not a release version", Object, rec.Release)
	}
	// A release is read the one way updatecheck reads one, so the form this
	// package hands back is the canonical one the object's schema publishes
	// and the form Advance writes into previous_release: an optional v is the
	// same release, and a record that carried one must not put a v into a
	// field the schema has no room for.
	rec.Release = release
	if rec.PreviousRelease != "" {
		previous, ok := updatecheck.CanonicalRelease(rec.PreviousRelease)
		if !ok {
			return Record{}, fmt.Errorf("%s: %q is not a release version", Object, rec.PreviousRelease)
		}
		rec.PreviousRelease = previous
	}
	return rec, nil
}

// Advance records version as the installation's release and returns the
// record that holds afterwards.
//
// It creates the object when there is none (a first update after this
// feature, so previous_release stays empty), and raises an existing one:
// release becomes version and previous_release the release it found, which
// is the window the update summary and the report list. The record only ever
// rises — a release at or below the recorded one (a re-run, or an older CLI's
// update) does nothing and leaves the window as it is, and a version that is
// not a strict release (a dev build) is not written at all.
//
// The write is conditional either way: Create when the object is absent,
// ReplaceIf against the generation the read returned when it is there. Two
// machines updating at once is why: the loser re-reads, sees the other's
// release and keeps whichever is higher, rather than lowering it or failing.
// A write this machine may not make is ErrNotWriter, and a bucket that
// cannot answer is an error; the caller treats both as a skip.
func Advance(ctx context.Context, w Writer, version string) (Record, error) {
	release, ok := updatecheck.CanonicalRelease(version)
	if !ok {
		rec, _, _, _, err := read(ctx, w)
		return rec, err
	}
	for attempt := 0; attempt < writeAttempts; attempt++ {
		rec, data, gen, absent, err := read(ctx, w)
		if err != nil {
			return Record{}, err
		}
		// Both sides are strict releases here, so this is "not above":
		// equal keeps the window (a re-run), and lower is an older CLI.
		if !absent && updatecheck.AtOrBelow(release, rec.Release) {
			return rec, nil
		}
		next := Record{Format: Format, Release: release}
		if !absent {
			next.PreviousRelease = rec.Release
		}
		out, err := json.Marshal(next)
		if err != nil {
			return rec, err
		}
		if absent {
			_, err = w.Create(ctx, Object, out, contentType)
		} else {
			_, err = w.ReplaceIf(ctx, Object, out, gen, data)
		}
		switch {
		case err == nil:
			return next, nil
		case errors.Is(err, blobx.ErrExists), errors.Is(err, blobx.ErrConflict):
			// Another machine wrote in between: read what it left and
			// decide again against that.
			continue
		case errors.Is(err, blobx.ErrForbidden):
			return rec, ErrNotWriter
		default:
			return rec, err
		}
	}
	rec, _, _, _, err := read(ctx, w)
	return rec, err
}
