package imagecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
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
	// Packages and Tools are this image's own resolved set, as one package per
	// fact — its name, version and integrity as three quoted string literals
	// (reduce.go's factOf) — and as "tool@version": §2.4's pkg: and tool:
	// lines without their prefix, the integrity being the one the key file
	// stated for that version's artifact (D618). They are what §4 compares:
	// an entry may be reused only when it holds the whole set at the same
	// versions and the same tools, and §5 calls a repository diverged when
	// the image it is on does not hold its set. Without them the lock would
	// hold only hashes, and no reuse could be decided at all.
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
	if err := l.checkOneJobPerEntry(); err != nil {
		return nil, err
	}
	return &l, nil
}

// checkOneJobPerEntry refuses a lock that places one job on two entries. A
// job is one repository and one workflow together, and it runs one image: an
// entry that said a job is on this image would have to be read against every
// other entry saying otherwise, and §9's "is a job pointing at this image?"
// would then answer with whichever entry the reader met first. A repository
// whose two workflows are on two images is two jobs, and is not this.
//
// The job is the pair the entry states — the repository its Jobs map is keyed
// by, and the job name beside it — because the pair is what a job is: two
// repositories may carry the same job name, and reading the name alone would
// refuse a lock that places one repository's job on one image while another
// repository's job of that name is on a second. What the pair cannot catch is
// one job renamed between two entries, since a name is all the lock states of
// it; §3.3 leaves that to the refresh (C8b1b), which writes both entries.
func (l *Lock) checkOneJobPerEntry() error {
	type job struct{ repo, name string }
	seen := map[job]string{}
	for _, key := range slices.Sorted(maps.Keys(l.Images)) {
		for _, repo := range slices.Sorted(maps.Keys(l.Images[key].Jobs)) {
			name := l.Images[key].Jobs[repo]
			if name == "" {
				continue
			}
			j := job{repo: repo, name: name}
			if prev, ok := seen[j]; ok && prev != key {
				return fmt.Errorf("the image lock: job %s of %s is on two images (%s and %s), and a job runs one image: an entry is wrong and the lock has to be repaired before a launch reads it", name, repo, prev, key)
			}
			seen[j] = key
		}
	}
	return nil
}

// LockReader is the part of a bucket the lock's read needs: *blobx.Bucket
// satisfies it (the assertion under LockWriter holds the read's signature
// too), and it is what ReadLock is written against, so the read is decided
// here and can be tested without a bucket.
type LockReader interface {
	ReadStrict(ctx context.Context, key string) ([]byte, int64, error)
}

// ReadLock reads the installation's lock, and returns nil, nil when there is
// none: absence means "this installation has not migrated", which a caller
// permits, so it is not an error.
//
// A read this machine is not allowed to make is an error, which is why the read
// is ReadStrict (LockWriter's comment has the whole of it): Read folds an HTTP
// 403 into ErrNotExist, so through it a credentials fault would arrive as an
// absent lock — the one answer a caller may act on by doing nothing, and a gate
// that would then permit what it cannot see.
func ReadLock(ctx context.Context, r LockReader) (*Lock, error) {
	data, _, err := r.ReadStrict(ctx, LockKey)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, nil
	case errors.Is(err, blobx.ErrForbidden):
		return nil, fmt.Errorf("reading %s: access denied (HTTP 403): this machine's credentials need read access to the project's runs bucket, and a denied read is not an absent lock: %w", LockKey, err)
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", LockKey, err)
	}
	l, err := ParseLock(data)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// LockWriter is the part of a bucket the lock's write needs. *blobx.Bucket
// satisfies it, and the tests fake it: the lock's write is decided here and
// nowhere else, so it can be tested without a bucket.
//
// The read is ReadStrict, not Read, because absence is what decides the
// write: an absent object is created, a present one is replaced against its
// generation. Read folds an HTTP 403 into ErrNotExist, so a read this
// machine is not allowed to make would look like an empty lock and the
// write after it would be a create that loses with ErrExists — a permissions
// fault reported as another writer, and a lock that is never written.
// ReadStrict keeps the two answers apart, and it is the right read for this
// caller: the operator's machine holds unconditional objectAdmin, so a 403
// there means no access.
type LockWriter interface {
	ReadStrict(ctx context.Context, key string) ([]byte, int64, error)
	Create(ctx context.Context, key string, data []byte, contentType string) (int64, error)
	ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error)
}

// The bucket is the writer the operator's machine has, and nothing else calls
// Update yet, so this is where the two are held together: a change to either
// signature stops the build here rather than at C8b's first refresh.
var _ LockWriter = (*blobx.Bucket)(nil)

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
//
// The two answers are told apart, as §3.4 tells them apart. ErrExists means
// the object appeared while this writer was planning, so nothing was lost:
// the next attempt reads what the other writer made and replaces against
// its generation, without spending the one re-plan. ErrConflict means the
// object it read has changed since, so it re-reads, re-plans once, and a
// second conflict is an error naming the object and the generation that
// collided, for the operator to rerun.
func Update(ctx context.Context, w LockWriter, plan func(*Lock) (*Lock, error)) error {
	conflicts, lastErr := 0, error(nil)
	for attempt := 0; attempt < lockWriteAttempts; attempt++ {
		data, gen, err := w.ReadStrict(ctx, LockKey)
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
		out, err := marshalLock(next)
		if err != nil {
			return err
		}
		if absent {
			_, err = w.Create(ctx, LockKey, out, lockContentType)
		} else {
			_, err = w.ReplaceIf(ctx, LockKey, out, gen, data)
		}
		switch {
		case err == nil:
			return nil
		case errors.Is(err, blobx.ErrExists):
			lastErr = err
		case errors.Is(err, blobx.ErrConflict):
			if conflicts++; conflicts > 1 {
				return fmt.Errorf("writing %s: a second writer changed it while this one held generation %d: %w", LockKey, gen, err)
			}
			lastErr = err
		default:
			return fmt.Errorf("writing %s: %w", LockKey, err)
		}
	}
	return fmt.Errorf("writing %s: another writer kept winning for %d attempts: %w", LockKey, lockWriteAttempts, lastErr)
}

// lockWriteAttempts bounds how many times Update re-reads and re-plans.
// §3.4 grants one re-plan after a conflict, and a lost create does not spend
// it — a create that loses found another writer's object, so nothing was
// lost — so the bound is that budget plus the room a losing create needs. It
// exists only so that a bucket answering ErrExists for an object that is not
// there cannot spin forever.
const lockWriteAttempts = 3

// marshalLock is what Update writes: the plan's lock, normalised so that the
// object stored is one this package can read back. A plan may hand back a
// lock it built itself, and a fresh one holds a zero version and a nil
// images map — an object ParseLock and the schema both refuse, so writing it
// would leave the one source of every repository's image digest unreadable
// for the whole installation while Update reported success. The re-parse is
// the check on everything else a plan can do, and the object it rejects is
// never written.
func marshalLock(l *Lock) ([]byte, error) {
	next := *l
	if next.Version == 0 {
		next.Version = LockVersion
	}
	if next.Images == nil {
		next.Images = map[string]LockedImage{}
	}
	out, err := json.Marshal(&next)
	if err != nil {
		return nil, fmt.Errorf("the image lock: %w", err)
	}
	if _, err := ParseLock(out); err != nil {
		return nil, fmt.Errorf("the lock this write would store: %w", err)
	}
	return out, nil
}
