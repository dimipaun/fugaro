package runstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// PRIndexVersion is the format version of one pull-request index entry. It is
// written on every entry and it is the only version this reader reads: an
// entry of another version is skipped rather than read as this one, because a
// field never changes meaning and a version this reader does not know is a
// shape it cannot vouch for (F8).
const PRIndexVersion = 1

// PRIndexDir is the object-name segment the pull-request index lives under,
// a sibling of a repository's run prefixes and never a run of its own:
// runs/<slug>/pr-index/<N>/<run-id>.json. It sits under the job account's
// existing runs/<slug>/ grant, which is the whole point of the layout — the
// outline's single index/<slug>/pr-<N>.json would have needed an IAM change
// and would be one file any run of the slug could overwrite whole (F3,
// D2962).
const PRIndexDir = "pr-index"

// PRIndexKind says how a run came to be on a pull request: it opened one, it
// followed one that already existed, or it reviewed one. The three are the
// whole vocabulary, and a value outside it names no entry this reader can
// place.
type PRIndexKind string

const (
	// PRIndexOpened is a run that opened the pull request itself.
	PRIndexOpened PRIndexKind = "opened"
	// PRIndexFollowed is a follow-up run that started on a pull request that
	// already existed.
	PRIndexFollowed PRIndexKind = "followed"
	// PRIndexReviewed is a review run: it opened no pull request and followed
	// none, and it is on one the moment its artifact stage names the number it
	// reviewed, which is also what ends it and what makes its row one
	// `runs ls --pr N` prints. Without an entry an old finished review is a
	// row the scan lists and the index cannot name, so it is written the same
	// way the other two are (D2964).
	PRIndexReviewed PRIndexKind = "reviewed"
)

// PRIndexEntry is one run's entry in the pull-request index: a small object
// naming the pull request the run opened, followed or reviewed, so `runs ls --pr N`
// finds the runs of a pull request without scanning the repository (D2961,
// D2962).
//
// It is a hint and never a source of rows. The job account that writes it is
// not trusted — a run's own agent can write or delete any object under its
// slug (SECURITY.md, bucket-iam §2.1) — so a reader checks every run the
// index names against that run's own records and shows it only if they agree
// (D2963). A forged entry buys a read, never a row; an erased one hides a run,
// which the reader bounds with a scan of the recent window (D2964).
type PRIndexEntry struct {
	// Version is PRIndexVersion on every entry this tree writes. A reader
	// refuses any other value rather than read it as this shape.
	Version int `json:"version"`
	// RunID is the run's own ID, which is also the object's name: the write
	// is one object per run, so no run can overwrite another's entry as a
	// unit (D2962). A reader skips an object whose run_id is not its name.
	RunID string `json:"run_id"`
	// Slug is the repository slug, which is the directory the entry sits
	// under. A reader skips an entry that names another slug.
	Slug string `json:"slug"`
	// PR is the pull-request number, which is the directory the entry sits
	// under. A reader skips an entry that names another number.
	PR int `json:"pr"`
	// Kind is how the run came to be on the pull request.
	Kind PRIndexKind `json:"kind"`
	// At is when the entry was written, in UTC. It is the writer's own clock
	// and so is not trusted for anything but ordering a listing.
	At time.Time `json:"at"`
}

// PRIndexPath is the object name of one run's index entry:
// runs/<slug>/pr-index/<N>/<run-id>.json. It is per run and never per pull
// request, so two runs on one pull request are two objects and neither write
// can replace the other (D2962).
//
// Every segment is validated, and a value that is not one this tree writes is
// refused rather than joined into a path: a slug becomes a directory, a
// number becomes one, and a run ID becomes the leaf, so a value carrying a
// separator or a ".." would name an object outside the index.
func PRIndexPath(slug string, pr int, runID string) (string, error) {
	if !slugRE.MatchString(slug) {
		return "", fmt.Errorf("%q is not a repo slug", slug)
	}
	if pr < 1 {
		return "", fmt.Errorf("%d is not a pull-request number", pr)
	}
	if !runIDRE.MatchString(runID) {
		return "", fmt.Errorf("%q is not a run ID", runID)
	}
	return prIndexPrefix(slug, pr) + runID + ".json", nil
}

// prIndexPrefix is the object-name prefix every entry of one pull request of
// one repository shares, ending in "/". It is built only from a slug and a
// number PRIndexPath has already validated.
func prIndexPrefix(slug string, pr int) string {
	return "runs/" + slug + "/" + PRIndexDir + "/" + strconv.Itoa(pr) + "/"
}

// PutPRIndex writes one run's index entry, and only if that run has none
// yet: a second write of the same run is a no-op and never an overwrite. The
// object is the run's own, so a follow-up that finds the entry already there
// — a retry, a re-execution, the launcher and the runner both writing — must
// leave what the first write said rather than replace it (D2962).
//
// Version is set here and never taken from the caller, so an entry this
// function wrote always carries PRIndexVersion. A slug, a run ID, a number, a
// kind or a time this tree does not write is refused before the bucket is
// touched: the object is written once and never corrected, so an entry that
// could not be read back must not be written at all.
func PutPRIndex(ctx context.Context, b *blob.Bucket, e PRIndexEntry) error {
	e.Version = PRIndexVersion
	key, err := PRIndexPath(e.Slug, e.PR, e.RunID)
	if err != nil {
		return err
	}
	if !knownPRIndexKind(e.Kind) {
		return fmt.Errorf("%q is not a pull-request index kind", e.Kind)
	}
	if e.At.IsZero() {
		return fmt.Errorf("the pull-request index entry for %s names no time", e.RunID)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the pull-request index entry for %s: %w", e.RunID, err)
	}
	if _, err := blobx.Wrap(b).Create(ctx, key, data, "application/json"); err != nil {
		// An entry already there is the create-if-absent rule working, not a
		// failure: the first write of this run stands.
		if errors.Is(err, blobx.ErrExists) {
			return nil
		}
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// ListPRIndex answers the entries of one pull request of one repository, from
// one listing of runs/<slug>/pr-index/<N>/, newest run first.
//
// It is a reader of an object the job account can write, so it trusts nothing
// in it: every object is decoded strictly (an unknown member, a second JSON
// value or a value of the wrong type is refused) and then checked against the
// name and the prefix it was found under — the version, the run ID against
// the object's own name, the slug and the number against the directory, the
// kind against the three words, and the time against zero. An object that fails
// any of those is skipped with a warning and never fails the query: the index
// is a hint, and a caller that gets fewer entries than objects scans the
// recent window beside it (D2964). A listing or a read of the bucket itself
// that fails is returned as an error, which is the caller's to fall back
// from.
//
// warnf is where a skip is reported, or nil for silence. It is the caller's
// because the index never prints anything itself: `runs ls` keeps its stderr
// note and its --json document separate, and a package has no writer to
// choose between them (F9).
func ListPRIndex(ctx context.Context, b *blob.Bucket, slug string, pr int, warnf func(string, ...any)) ([]PRIndexEntry, error) {
	if !slugRE.MatchString(slug) {
		return nil, fmt.Errorf("%q is not a repo slug", slug)
	}
	if pr < 1 {
		return nil, fmt.Errorf("%d is not a pull-request number", pr)
	}
	prefix := prIndexPrefix(slug, pr)
	wb := blobx.Wrap(b)
	var out []PRIndexEntry
	it := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if obj.IsDir {
			continue
		}
		data, _, err := wb.Read(ctx, obj.Key)
		if err != nil {
			// An object that was erased between the listing and the read is
			// not an entry, and an erased entry is the residual D2964 accepts:
			// it is skipped silently. Anything else is a bucket that refused
			// this one object, which is a skip with a warning like a bad entry.
			if !errors.Is(err, blobx.ErrNotExist) {
				warnPRIndex(warnf, "skipping %s: %v", obj.Key, err)
			}
			continue
		}
		e, err := decodePRIndexEntry(prefix, slug, pr, obj.Key, data)
		if err != nil {
			warnPRIndex(warnf, "skipping %s: %v", obj.Key, err)
			continue
		}
		out = append(out, *e)
	}
	slices.SortFunc(out, func(a, b PRIndexEntry) int { return strings.Compare(b.RunID, a.RunID) })
	return out, nil
}

// decodePRIndexEntry reads one listed object as an index entry, refusing
// everything about it that does not agree with where it was found: the leaf
// must be a run ID and the entry must carry that run ID, the slug and the
// number of the directory, a version this reader knows, one of the three kinds
// and a time.
func decodePRIndexEntry(prefix, slug string, pr int, key string, data []byte) (*PRIndexEntry, error) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return nil, fmt.Errorf("the object is not under %s", prefix)
	}
	name, ok := strings.CutSuffix(rest, ".json")
	if !ok || !runIDRE.MatchString(name) {
		return nil, fmt.Errorf("%q is not a run's entry", rest)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var e PRIndexEntry
	if err := dec.Decode(&e); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the entry carries more than one JSON value")
	}
	switch {
	case e.Version != PRIndexVersion:
		return nil, fmt.Errorf("the entry's version is %d, not %d", e.Version, PRIndexVersion)
	case e.RunID != name:
		return nil, fmt.Errorf("the entry names run %q and is the object of %q", e.RunID, name)
	case e.Slug != slug || e.PR != pr:
		return nil, fmt.Errorf("the entry is for %s/%d and sits under %s/%d", e.Slug, e.PR, slug, pr)
	case !knownPRIndexKind(e.Kind):
		return nil, fmt.Errorf("%q is not a pull-request index kind", e.Kind)
	case e.At.IsZero():
		return nil, errors.New("the entry names no time")
	}
	return &e, nil
}

// knownPRIndexKind reports whether k is one of the three kinds, the way
// knownVerdict is for triage's vocabulary: a value outside them is an entry
// no reader can place, so both the writer and the reader refuse it.
func knownPRIndexKind(k PRIndexKind) bool {
	return k == PRIndexOpened || k == PRIndexFollowed || k == PRIndexReviewed
}

// warnPRIndex reports a skipped object through the caller's warnf, or does
// nothing when the caller passed none.
func warnPRIndex(warnf func(string, ...any), format string, args ...any) {
	if warnf != nil {
		warnf(format, args...)
	}
}
