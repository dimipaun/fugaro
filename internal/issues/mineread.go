package issues

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// mineIndexCap bounds the user's own index listing. A launcher that floods one
// reporter's prefix degrades only that reporter's view (F7), and the cap keeps
// even that view a bounded read; the rows past it are ones the read says were
// left out.
const mineIndexCap = 500

// mineOccurrenceCap bounds one issue's occurrence listing: an issue a whole
// team hit a thousand times is one row either way, and the count is the only
// thing a longer listing would change.
const mineOccurrenceCap = 1000

// mineStore is the slice of a bucket this reader uses: names-only listings and
// bounded reads. It is an interface so a test can wrap a bucket and count the
// calls, because what this reader must never do is read an object whose name
// was all it needed (F7).
type mineStore interface {
	List(opts *blob.ListOptions) *blob.ListIterator
	ReadMax(ctx context.Context, key string, limit int) ([]byte, int64, error)
}

// ListMine reads the issues this user is a reporter on (F7): the user's own
// reporter index is listed names only, each issue it names is read bounded, and
// the number of other people who hit it is counted by digest and never by name
// (F15).
//
// id is a display identity, so a cached one is enough to read with — nothing
// here writes. A user with no digest, which is nobody at all, is on no issue
// the store can name, and the answer is an empty read rather than an error.
func ListMine(ctx context.Context, b mineStore, id Identity, now time.Time) (MineShared, error) {
	out := MineShared{AsOf: now.UTC()}
	if id.Digest == "" {
		return out, nil
	}
	numbers, truncated, err := mineIndex(ctx, b, id.Digest)
	if err != nil {
		return MineShared{}, err
	}
	out.Truncated = truncated
	for _, number := range numbers {
		rec, ok := readMineRecord(ctx, b, number)
		if !ok {
			continue
		}
		issue, err := mineIssue(ctx, b, id.Digest, rec)
		if err != nil {
			return MineShared{}, err
		}
		out.Issues = append(out.Issues, issue)
	}
	return out, nil
}

// mineIndex lists one reporter's own index: the issue numbers under their
// prefix, names only. A name that is not one this store writes is skipped, and
// the listing is capped at mineIndexCap issues, with the ones past it left out
// and said so.
func mineIndex(ctx context.Context, b mineStore, digest string) (numbers []int, truncated bool, err error) {
	iter := b.List(&blob.ListOptions{Prefix: reporterPrefix(digest)})
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("issues: listing a reporter's issues: %w", err)
		}
		if obj.IsDir {
			continue
		}
		number, ok := numberFromIndexKey(obj.Key, digest)
		if !ok {
			continue
		}
		if len(numbers) >= mineIndexCap {
			return numbers, true, nil
		}
		numbers = append(numbers, number)
	}
	return numbers, false, nil
}

// numberFromIndexKey reads the number out of one index entry's name, and
// refuses a name this store did not write (F20): the prefix is writable by
// every launcher, and the number it hands back is read as an issue. A name with
// a separator, a dot, a leading zero or a length this store does not write is
// skipped rather than joined into a key.
func numberFromIndexKey(key, digest string) (int, bool) {
	rest, ok := strings.CutPrefix(key, reporterPrefix(digest))
	if !ok || !digits(rest) {
		return 0, false
	}
	number, err := strconv.Atoi(rest)
	if err != nil || validateNumber(number) != nil || key != reporterIndexKey(number, digest) {
		return 0, false
	}
	return number, true
}

// readMineRecord reads one issue's record through the reader's own bounded
// read. A record the store cannot hand back — gone, too big, not this format —
// is not an issue the store holds, and the listing leaves it out rather than
// failing: the prefix is writable by every launcher, and one entry this build
// cannot read must not take the user's whole list down (F11).
func readMineRecord(ctx context.Context, b mineStore, number int) (Issue, bool) {
	if err := validateNumber(number); err != nil {
		return Issue{}, false
	}
	raw, _, err := b.ReadMax(ctx, recordKey(number), blobx.MaxReadBytes)
	if err != nil {
		return Issue{}, false
	}
	rec, err := decodeShared(raw, number)
	if err != nil {
		return Issue{}, false
	}
	return rec.Issue, true
}

// mineIssue is one issue of the store as this user reads it: the record's
// display data, the user's own hit time, the count of other people, and — on a
// resolved issue — whether the user's own hit was flagged a regression (F7,
// F14).
func mineIssue(ctx context.Context, b mineStore, digest string, rec Issue) (MineSharedIssue, error) {
	occ, err := mineOccurrences(ctx, b, rec.Number, digest, rec.Status == StatusResolved)
	if err != nil {
		return MineSharedIssue{}, err
	}
	// Who hit the issue: everyone an occurrence names, and the reporter the
	// record was filed by — the creator hit it too. This user is never one of
	// the others, and no name or digest leaves this function (F15).
	people := make(map[string]bool, len(occ.digests)+1)
	for d := range occ.digests {
		people[d] = true
	}
	if rec.Reporter != "" {
		people[Digest(rec.Reporter)] = true
	}
	others := len(people)
	if people[digest] {
		others--
	}
	out := MineSharedIssue{
		Number:      rec.Number,
		Summary:     rec.Summary,
		Status:      MineStatus(rec.Status),
		SubmittedAt: rec.SubmittedAt,
		ResolvedAt:  rec.ResolvedAt,
		DismissedAt: rec.DismissedAt,
		URL:         rec.URL,
		ResolvedIn:  rec.ResolvedIn,
		Regression:  occ.regression,
		OthersHit:   others,
	}
	// The user's hit time: the creator's own record carries it, and a duplicate
	// merged into somebody else's issue has only the user's own occurrences —
	// the earliest of them, because that is when the bug first bit (F7).
	if rec.Reporter != "" && Digest(rec.Reporter) == digest {
		hit := rec.Created.UTC()
		out.HitAt = &hit
	} else {
		out.HitAt = occ.hitAt
	}
	return out, nil
}

// mineOcc is what one issue's occurrence names say about this user: whose hits
// they are, when the user's earliest one was, the user's own objects to read
// for the regression flag, and whether that flag is set.
type mineOcc struct {
	digests    map[string]bool
	hitAt      *time.Time
	ownKeys    []string
	regression bool
}

// mineOccurrences lists one issue's occurrences, names only, and derives from
// the names whose hits they are and when this user's earliest one was (F7).
//
// Only a resolved issue's own objects are read, and only this user's: the
// regression flag belongs to the hit that set it, and another reporter's
// regression must not turn this user's row into one (F14). A name that is not
// one this store writes is skipped, so a hostile entry changes nothing here.
func mineOccurrences(ctx context.Context, b mineStore, number int, digest string, readOwn bool) (mineOcc, error) {
	out := mineOcc{digests: map[string]bool{}}
	iter := b.List(&blob.ListOptions{Prefix: occurrencePrefix(number)})
	seen := 0
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return mineOcc{}, fmt.Errorf("issues: listing the occurrences of issue %d: %w", number, err)
		}
		if obj.IsDir {
			continue
		}
		who, at, ok := occurrenceFromKey(obj.Key, number)
		if !ok {
			continue
		}
		if seen >= mineOccurrenceCap {
			break
		}
		seen++
		out.digests[who] = true
		if who != digest {
			continue
		}
		if out.hitAt == nil || at.Before(*out.hitAt) {
			hit := at
			out.hitAt = &hit
		}
		if readOwn {
			out.ownKeys = append(out.ownKeys, obj.Key)
		}
	}
	if !readOwn {
		return out, nil
	}
	for _, key := range out.ownKeys {
		raw, _, err := b.ReadMax(ctx, key, blobx.MaxReadBytes)
		if err != nil {
			continue
		}
		var occ Occurrence
		if err := decodeStrict(raw, &occ); err != nil {
			continue
		}
		if occ.Regression {
			out.regression = true
		}
	}
	return out, nil
}

// occurrenceFromKey reads the reporter and the hit time out of an occurrence's
// name, and refuses a name this store did not write (F20): the prefix is
// writable by every launcher, and both the digest and the time are read as
// facts. The time is parsed strictly, so a name that only looks like one is
// skipped rather than guessed at, and a name with more segments than this
// store writes is refused by the same parse.
func occurrenceFromKey(key string, number int) (digest string, at time.Time, ok bool) {
	rest, ok := strings.CutPrefix(key, occurrencePrefix(number))
	if !ok {
		return "", time.Time{}, false
	}
	digest, stamp, found := strings.Cut(rest, "/")
	if !found || !reporterDigestRE.MatchString(digest) {
		return "", time.Time{}, false
	}
	at, err := time.Parse(occurrenceTimeLayout, stamp)
	if err != nil {
		return "", time.Time{}, false
	}
	return digest, at.UTC(), true
}
