package imagecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// This file is the lock (design
// docs/forge-authorship-images/shared-images.md §3): the installation's one
// record of which images exist, what each was built from and which
// repositories are on it. It is the one source of every repository's image
// digest — a job's image comes from here, not from the repository's own
// build record.
//
// It lives at LockKey in the runs bucket, is written only by the operator's
// machine (§3.3: a build writes its own record and nothing else, and a
// launch or a listing reads and writes nothing) and is decoded strictly
// (§3.2): every operator's machine writes this one object and every fugaro
// version in use reads it, so a reader that ignored a field it did not know
// would drop it for everyone on its next write. ParseRecord is tolerant for
// the opposite reason: a build record is read by the machine that wrote it.

// LockVersion is the lock schema version this package writes.
const LockVersion = 1

// LockKey is the lock's object in the runs bucket. It cannot collide with a
// build record, whose key is builds/<slug>/<workflow>/image.json.
const LockKey = "builds/image-lock.json"

// lockContentType is what the lock's object is written as.
const lockContentType = "application/json"

// Lock is the installation's shared images, keyed by their normalised
// dependency set: the map key is the image's own key, so the same name is
// the same dependency set (§2.6).
type Lock struct {
	Version int                    `json:"version"`
	Images  map[string]LockedImage `json:"images"`
}

// LockedImage is one shared image in the lock.
type LockedImage struct {
	// Key is the image's normalised dependency set, the map key it is
	// stored under.
	Key string `json:"key"`
	// Image is the image, without a tag, and Digest is the sha256:… the
	// build promoted. An entry's digest is never overwritten in place
	// (§3.3): a changed set is a changed key and so a new entry.
	Image  string `json:"image"`
	Digest string `json:"digest"`
	// BaseRef and BaseDigest are the base the build ran FROM.
	BaseRef    string `json:"base_ref"`
	BaseDigest string `json:"base_digest"`
	// Packages and Tools are this image's own resolved set, as "name@version"
	// — §2.4's pkg: and tool: lines without their prefix. They are what §4
	// compares: an entry may be reused only when it holds the whole set at
	// the same versions and the same tools, and §5 calls a repository
	// diverged when the image it is on does not hold its set. Without them
	// the lock would hold only hashes, and no reuse could be decided at all.
	Packages []string `json:"packages"`
	Tools    []string `json:"tools"`
	// BuiltAt and BuildID identify the build that made the image.
	BuiltAt time.Time `json:"built_at"`
	BuildID string    `json:"build_id"`
	// Repositories and Jobs are the same set seen twice: which repositories
	// are on this image, and each one's job name as it was at update time.
	// They answer "is a job pointing at this image?" (§9).
	Repositories []string          `json:"repositories"`
	Jobs         map[string]string `json:"jobs"`
	// LastUsedAt is the last time a refresh placed a repository on this
	// image; §9's age is measured from it.
	LastUsedAt time.Time `json:"last_used_at"`
}

// ParseLock decodes the lock strictly (§3.2): an unknown field anywhere, an
// unknown version, data after the object and a document with no images
// object are all errors, and each names what it found. The version check is
// the one that matters most — an older fugaro meeting a newer lock stops and
// says so, rather than rewriting the object and dropping what it did not
// understand.
func ParseLock(data []byte) (*Lock, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var l Lock
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("the image lock: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, errors.New("the image lock: data after the object")
	}
	if l.Version != LockVersion {
		return nil, fmt.Errorf("the image lock: version %d is not supported (this build reads %d)", l.Version, LockVersion)
	}
	if l.Images == nil {
		return nil, errors.New("the image lock: no images object")
	}
	return &l, nil
}

// LockWriter is the part of a bucket the lock's write needs. *blobx.Bucket
// satisfies it, and the tests fake it: the lock's write is decided here and
// nowhere else, so it can be tested without a bucket.
type LockWriter interface {
	Read(ctx context.Context, key string) ([]byte, int64, error)
	Create(ctx context.Context, key string, data []byte, contentType string) (int64, error)
	ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error)
}

// Update reads the lock, applies plan to it and writes what plan returns by
// §3.4's create-or-replace rule. A plan that returns a nil lock writes
// nothing, which is how a refresh with nothing to record leaves the object
// — and its generation — alone.
//
// Two operators' machines can refresh different keys at the same time, and
// both write this one object, so the write is conditional either way: the
// first write of a missing object is a create, which is create-if-absent,
// and every later one is generation-matched against the generation the read
// returned. Both losing answers mean another writer was there first, and
// both are safe for the same reason: a plan only ever adds or updates its
// own key's entry, so the other writer's entries are not in its way.
// ErrExists means the object appeared while this writer was planning, so
// nothing was lost; ErrConflict means the object it read has changed since.
// Either way the plan runs once more against what the other writer left,
// and a second loss is an error naming the object and the generation that
// collided, for the operator to rerun.
func Update(ctx context.Context, w LockWriter, plan func(*Lock) (*Lock, error)) error {
	for attempt := 0; ; attempt++ {
		data, gen, err := w.Read(ctx, LockKey)
		absent := errors.Is(err, blobx.ErrNotExist)
		if err != nil && !absent {
			return fmt.Errorf("reading %s: %w", LockKey, err)
		}
		lock := &Lock{Version: LockVersion, Images: map[string]LockedImage{}}
		if !absent {
			if lock, err = ParseLock(data); err != nil {
				return err
			}
		}
		next, err := plan(lock)
		if err != nil || next == nil {
			return err
		}
		out, err := json.Marshal(next)
		if err != nil {
			return fmt.Errorf("the image lock: %w", err)
		}
		if absent {
			_, err = w.Create(ctx, LockKey, out, lockContentType)
		} else {
			_, err = w.ReplaceIf(ctx, LockKey, out, gen, data)
		}
		switch {
		case err == nil:
			return nil
		case errors.Is(err, blobx.ErrExists), errors.Is(err, blobx.ErrConflict):
			// Another writer was there. §3.4 re-reads and re-plans once; the
			// second loss is the error below, which the operator acts on.
			if attempt > 0 {
				return fmt.Errorf("writing %s: a second writer changed it while this one held generation %d: %w", LockKey, gen, err)
			}
		default:
			return fmt.Errorf("writing %s: %w", LockKey, err)
		}
	}
}
