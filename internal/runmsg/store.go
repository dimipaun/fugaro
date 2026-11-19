package runmsg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/token"
)

// messageJSON is the content type of a message object. The bytes are JSON, and
// a reader that fetches one by hand should be told so.
const messageJSON = "application/json"

// seqDigits is how wide the number in a message object's name is. Four digits
// cover the cap with room, and a fixed width is what makes a listing sort the
// way the runner walks it.
const seqDigits = 4

// putAttempts bounds the race a sender can lose: each attempt lists, takes the
// highest number plus one and writes it create-if-absent, so an attempt is
// lost only to another sender taking that number in the window between the two
// (F10). Twenty senders racing at once is not a shape this channel has.
const putAttempts = 5

// beforeCreate is the seam a test forces an interleaving through: it runs after
// the listing and before the write conditioned on it, which is the window two
// senders racing do their damage in. Production leaves it nil.
var beforeCreate func(key string)

// Prefix is where slug/run's messages live. It is built from token.ObjectPrefix
// and never a second literal, so the name a launcher writes cannot drift from
// the grants the Terraform modules put on exactly that prefix (D2941).
func Prefix(slug, run string) string {
	return token.ObjectPrefix + "/" + slug + "/" + run + "/messages/"
}

// Path is the object a message numbered seq is stored at, beside the run's
// budget token under tokens/ and never under runs/ (D2941, F2).
func Path(slug, run string, seq int) string {
	return Prefix(slug, run) + fmt.Sprintf("%0*d%s", seqDigits, seq, Suffix)
}

// Put writes one message and returns the number it took. It numbers the
// message itself: the highest number under the run's prefix plus one, written
// create-if-absent, so two senders never overwrite each other and a listing is
// the only thing a sender needs (F10). A number that went missing below the
// highest is refused rather than papered over, because writing past a hole
// would leave the runner's numbering with a gap it cannot tell from a message
// nobody wrote yet.
//
// SentAt is stamped when the caller leaves it zero. The text is not cleaned or
// redacted here: the runner does that before the agent sees it, so what a
// person typed is what the object holds (F12).
func Put(ctx context.Context, b *blobx.Bucket, slug, run string, m Message) (int, error) {
	if !token.ValidSegment(slug) || !token.ValidSegment(run) {
		return 0, errors.New("run message: invalid slug or run id")
	}
	if m.SentAt.IsZero() {
		m.SentAt = time.Now().UTC()
	}
	if err := check(m); err != nil {
		return 0, err
	}
	for attempt := 0; attempt < putAttempts; attempt++ {
		nums, err := numbers(ctx, b, slug, run)
		if err != nil {
			return 0, err
		}
		highest := 0
		if len(nums) > 0 {
			highest = nums[len(nums)-1]
		}
		if highest >= MaxMessages {
			return 0, fmt.Errorf("%w: it already holds %d", ErrTooMany, highest)
		}
		for i, n := range nums {
			if n != i+1 {
				return 0, fmt.Errorf("%w: message %0*d is missing under %s", ErrGap, seqDigits, i+1, Prefix(slug, run))
			}
		}
		m.Seq = highest + 1
		raw, err := json.Marshal(m)
		if err != nil {
			return 0, fmt.Errorf("run message: encoding message %d failed: %s", m.Seq, err)
		}
		key := Path(slug, run, m.Seq)
		if beforeCreate != nil {
			beforeCreate(key)
		}
		if _, err := b.Create(ctx, key, raw, messageJSON); err != nil {
			if errors.Is(err, blobx.ErrExists) {
				// Another sender took the number between the listing and the
				// write; the message is theirs and this one takes the next.
				continue
			}
			return 0, fmt.Errorf("run message: writing message %d failed: %s", m.Seq, err)
		}
		return m.Seq, nil
	}
	return 0, fmt.Errorf("run message: gave up after %d attempts, every number taken as it was chosen", putAttempts)
}

// Next reads the message numbered seq, or answers none. A job account's grant
// on tokens/ carries no storage.objects.list, so GCS refuses its read of a
// missing object with 403 where a launcher would get 404: both mean "nothing
// at this number yet", and the runner polls 1..MaxMessages on its cancel
// watch's cadence (F10). A number past the cap is never read, whatever is
// stored at it (F12).
//
// An object that is there and is not a message this build reads — unparseable,
// over the cap, a version above the one it knows, a name that disagrees with
// its own number — is ErrRefused, which the runner records and never shows to
// the agent (rule 6).
func Next(ctx context.Context, b *blobx.Bucket, slug, run string, seq int) (*Message, error) {
	if !token.ValidSegment(slug) || !token.ValidSegment(run) {
		return nil, errors.New("run message: invalid slug or run id")
	}
	if seq < 1 || seq > MaxMessages {
		return nil, nil
	}
	key := Path(slug, run, seq)
	raw, _, err := b.ReadMaxStrict(ctx, key, MaxObjectBytes)
	switch {
	case errors.Is(err, blobx.ErrForbidden) || errors.Is(err, blobx.ErrNotExist):
		return nil, nil
	case errors.Is(err, blobx.ErrTooLarge):
		return nil, fmt.Errorf("%w: the object at %s is larger than %d bytes", ErrRefused, key, MaxObjectBytes)
	case err != nil:
		return nil, fmt.Errorf("run message: reading %s failed: %s", key, err)
	}
	m, err := decodeStrict(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrRefused, key, err)
	}
	if m.Seq != seq {
		return nil, fmt.Errorf("%w: the object at %s carries number %d", ErrRefused, key, m.Seq)
	}
	return m, nil
}

// decodeStrict turns a message object's bytes into the Message they carry. It
// refuses an unknown field and a second value after the first as well as the
// shapes check refuses, because the prefix is writable by hand: a document
// this build does not know is refused rather than guessed at (rule 6).
func decodeStrict(raw []byte) (*Message, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Message
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("the object holds more than one value")
	}
	if m.Seq < 1 || m.Seq > MaxMessages {
		return nil, fmt.Errorf("%w: it carries number %d", ErrShape, m.Seq)
	}
	if err := check(m); err != nil {
		return nil, err
	}
	return &m, nil
}

// numbers is the message numbers a listing shows, in ascending order. A name
// that is not one this store writes is skipped rather than counted: the prefix
// is writable by every launcher, and an object put there by hand must not be
// able to stall a sender for ever.
func numbers(ctx context.Context, b *blobx.Bucket, slug, run string) ([]int, error) {
	prefix := Prefix(slug, run)
	it := b.List(&blob.ListOptions{Prefix: prefix})
	var out []int
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("run message: listing %s failed: %s", prefix, err)
		}
		if n, ok := seqOf(obj.Key); ok {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out, nil
}

// seqOf reads the number a listed object's name carries, or reports that the
// name is not one this store writes.
func seqOf(key string) (int, bool) {
	stem, ok := strings.CutSuffix(path.Base(key), Suffix)
	if !ok || len(stem) != seqDigits {
		return 0, false
	}
	n, err := strconv.Atoi(stem)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
