package issues

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// The reporter these fixtures name is the address itself, and an upload
// indexes the reporter the identity it was handed names: a read asked with
// another spelling looks under another person's digest and finds nothing.

// countingBucket wraps a bucket and counts the calls a reader makes. What
// `mine` must not do is read an object whose name was all it needed: the index
// listing answers who is on which issue, and reading every record it walks past
// is the read F7 exists to avoid.
type countingBucket struct {
	b        *blobx.Bucket
	readKeys []string
	lists    int
}

func (c *countingBucket) List(opts *blob.ListOptions) *blob.ListIterator {
	c.lists++
	return c.b.List(opts)
}

func (c *countingBucket) ReadMax(ctx context.Context, key string, limit int) ([]byte, int64, error) {
	c.readKeys = append(c.readKeys, key)
	return c.b.ReadMax(ctx, key, limit)
}

// mineNumbers is the numbers of the rows a read answered, sorted, so a test
// compares sets rather than the order a store happens to return.
func mineNumbers(got MineShared) []int {
	out := make([]int, 0, len(got.Issues))
	for _, i := range got.Issues {
		out = append(out, i.Number)
	}
	slices.Sort(out)
	return out
}

// mineRowOf is the one row for an issue, or a failure naming what was there.
func mineRowOf(t *testing.T, got MineShared, number int) MineSharedIssue {
	t.Helper()
	for _, i := range got.Issues {
		if i.Number == number {
			return i
		}
	}
	t.Fatalf("the read has no row for issue %d: %+v", number, got.Issues)
	return MineSharedIssue{}
}

// A user sees the issues they created and the duplicates of theirs merged into
// another reporter's issue, and nothing else (acceptance 1, F7).
func TestMineFindsOwnAndMergedIssues(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana, bo := "ana@example.test", "bo@example.test"
	mine := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("61")))
	theirs := upload(t, ctx, b, hit("update-20261107183013-48214", fingerprint("62"), bo, "0.9.0", issueAt))
	upload(t, ctx, b, hit("update-20261107183014-48215", fingerprint("62"), ana, "0.9.0", issueAt.Add(time.Hour)))
	upload(t, ctx, b, hit("update-20261107183015-48216", fingerprint("63"), "cy@example.test", "0.9.0", issueAt))

	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if want := []int{mine.Number, theirs.Number}; !slices.Equal(mineNumbers(got), want) {
		t.Errorf("the rows are %v, want %v", mineNumbers(got), want)
	}
	if !got.AsOf.Equal(sharedAt) {
		t.Errorf("as of = %s, want %s", got.AsOf, sharedAt)
	}
}

// The count is of other people, by digest, and a name in an occurrence body
// decides nothing: a launcher can write any body under any digest, and the
// digest in the name is what a hit is (F7, F15).
func TestMineCountsOthersByDigestNotNames(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana := "ana@example.test"
	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("64")))
	// ana's own digest, a body naming somebody else: still ana's hit.
	raw, err := json.Marshal(Occurrence{Reporter: "someone-else@example.test", At: sharedAt})
	if err != nil {
		t.Fatal(err)
	}
	key := occurrenceDigestPrefix(up.Number, Digest(ana)) + sharedAt.Format(occurrenceTimeLayout)
	if _, err := b.Create(ctx, key, raw, sharedJSON); err != nil {
		t.Fatalf("writing the occurrence: %v", err)
	}
	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if row := mineRowOf(t, got, up.Number); row.OthersHit != 0 {
		t.Errorf("others = %d, want 0: the hit is ana's own, whatever the body says", row.OthersHit)
	}
}

// The same person on two machines is one digest, so two hits from one address
// are one other person and the earliest of them is the hit time (F7).
func TestMineSamePersonTwoMachinesCountsOnce(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana, bo := "ana@example.test", "bo@example.test"
	up := upload(t, ctx, b, hit("update-20261107183012-48213", fingerprint("65"), bo, "0.9.0", issueAt))
	first := issueAt.Add(time.Hour)
	upload(t, ctx, b, hit("update-20261107183014-48215", fingerprint("65"), ana, "0.9.0", first))
	upload(t, ctx, b, hit("update-20261107183015-48216", fingerprint("65"), ana, "0.9.0", issueAt.Add(2*time.Hour)))

	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	row := mineRowOf(t, got, up.Number)
	if row.OthersHit != 1 {
		t.Errorf("others = %d, want 1: bo, counted once", row.OthersHit)
	}
	if row.HitAt == nil || !row.HitAt.Equal(first) {
		t.Errorf("hit at = %v, want the earliest hit %s", row.HitAt, first)
	}
}

// The user is never one of the others, on their own issue or on somebody
// else's (F15).
func TestMineUserNotCountedAsOther(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana, bo := "ana@example.test", "bo@example.test"
	mine := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("66")))
	theirs := upload(t, ctx, b, hit("update-20261107183013-48214", fingerprint("67"), bo, "0.9.0", issueAt))
	upload(t, ctx, b, hit("update-20261107183014-48215", fingerprint("67"), ana, "0.9.0", issueAt.Add(time.Hour)))

	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if row := mineRowOf(t, got, mine.Number); row.OthersHit != 0 {
		t.Errorf("others on ana's own issue = %d, want 0", row.OthersHit)
	}
	if row := mineRowOf(t, got, theirs.Number); row.OthersHit != 1 {
		t.Errorf("others on bo's issue = %d, want 1", row.OthersHit)
	}
}

// Finding the user's issues is one names-only listing of their own prefix: the
// index objects are never read, and the two objects a read does take back are
// the issue's record and the fingerprint entry that names it (F7, F28).
func TestMineMembershipReadsNoObjects(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana := "ana@example.test"
	fp := fingerprint("68")
	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fp))
	upload(t, ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", "0.9.0", issueAt.Add(time.Hour)))
	c := &countingBucket{b: b}
	if _, err := ListMine(ctx, c, verified(ana), sharedAt); err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if want := []string{recordKey(up.Number), fingerprintKey(fp)}; !slices.Equal(c.readKeys, want) {
		t.Errorf("the read took back %v, want %v: the issue's record and the entry that names it (issue %d)", c.readKeys, want, up.Number)
	}
	for _, key := range c.readKeys {
		if strings.HasPrefix(key, reporterPrefix(Digest(ana))) {
			t.Errorf("the read took back the index object %q: the listing answers who is on which issue, and its names are all a reader needs (F7)", key)
		}
	}
}

// The index listing is capped, and says so; a flood under one digest changes
// only that reporter's view (F7).
func TestMineIndexCapSetsTruncatedAndOtherUsersUnaffected(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana, bo := "ana@example.test", "bo@example.test"
	for n := 1; n <= mineIndexCap+1; n++ {
		if _, err := b.Create(ctx, reporterIndexKey(n, Digest(ana)), nil, sharedJSON); err != nil {
			t.Fatalf("indexing issue %d: %v", n, err)
		}
	}
	theirs := upload(t, ctx, b, hit("update-20261107183012-48213", fingerprint("69"), bo, "0.9.0", issueAt))

	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if !got.Truncated {
		t.Errorf("the read is not marked truncated past %d entries", mineIndexCap)
	}
	other, err := ListMine(ctx, b, verified(bo), sharedAt)
	if err != nil {
		t.Fatalf("reading bo's mine: %v", err)
	}
	if other.Truncated || len(other.Issues) != 1 || other.Issues[0].Number != theirs.Number {
		t.Errorf("bo's read = %+v, want one untruncated row for issue %d", other, theirs.Number)
	}
}

// A name the store did not write is skipped, and it changes nothing a row
// says: the number, the digest and the time are all read as facts (F11, F20).
func TestMineSkipsMalformedNames(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana, bo := "ana@example.test", "bo@example.test"
	up := upload(t, ctx, b, hit("update-20261107183012-48213", fingerprint("70"), bo, "0.9.0", issueAt))
	upload(t, ctx, b, hit("update-20261107183014-48215", fingerprint("70"), ana, "0.9.0", issueAt.Add(time.Hour)))
	// An index entry that is not a number, a digest that is not one, and a time
	// that does not parse: none of them is an issue, a person or a hit.
	for _, key := range []string{
		reporterPrefix(Digest(ana)) + "../0007",
		reporterPrefix(Digest(ana)) + "not-a-number",
		occurrencePrefix(up.Number) + "abc/" + sharedAt.Format(occurrenceTimeLayout),
		occurrencePrefix(up.Number) + Digest("cy@example.test") + "/not-a-time",
	} {
		if _, err := b.Create(ctx, key, []byte("{}"), sharedJSON); err != nil {
			t.Fatalf("writing %s: %v", key, err)
		}
	}
	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if want := []int{up.Number}; !slices.Equal(mineNumbers(got), want) {
		t.Errorf("the rows are %v, want %v", mineNumbers(got), want)
	}
	if row := mineRowOf(t, got, up.Number); row.OthersHit != 1 {
		t.Errorf("others = %d, want 1: the malformed names are not people", row.OthersHit)
	}
}

// The reader reads the records its own index names and no others: an issue of
// somebody else's, however many there are, costs nothing (F7).
func TestMineReadsOnlyOwnRecords(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana := "ana@example.test"
	for _, fp := range []string{fingerprint("71"), fingerprint("72"), fingerprint("73")} {
		upload(t, ctx, b, hit("update-20261107183012-48213", fp, "bo@example.test", "0.9.0", issueAt))
	}
	fp := fingerprint("74")
	mine := upload(t, ctx, b, sharedIssue("update-20261107183019-48220", fp))
	c := &countingBucket{b: b}
	got, err := ListMine(ctx, c, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if want := []int{mine.Number}; !slices.Equal(mineNumbers(got), want) {
		t.Errorf("the rows are %v, want %v", mineNumbers(got), want)
	}
	if want := []string{recordKey(mine.Number), fingerprintKey(fp)}; !slices.Equal(c.readKeys, want) {
		t.Errorf("the read took back %v, want %v: the user's own record and its entry, and none of the three of somebody else's", c.readKeys, want)
	}
}

// A regression is the user's own flagged occurrence on a resolved issue: the
// hit that set the flag is theirs, and the issue is one a release fixed (F14).
func TestMineRegressionIsTheUsersOwnFlaggedOccurrence(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana := "ana@example.test"
	fp := fingerprint("75")
	up, _ := filedIssue(t, ctx, b, fp)
	if err := SetResolved(ctx, b, up.Number, "0.20.0", 0, sharedAt); err != nil {
		t.Fatalf("resolving issue %d: %v", up.Number, err)
	}
	// The reporter's own version is already at the fix, so the hit is the bug
	// back.
	upload(t, ctx, b, hit("update-20261107183014-48215", fp, ana, "0.21.0", sharedAt.Add(time.Hour)))

	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	row := mineRowOf(t, got, up.Number)
	if row.Status != MineStatusResolved || !row.Regression {
		t.Errorf("the row = %+v, want a resolved issue the user hit again", row)
	}
}

// Another reporter's regression is not this user's: the flag is read from the
// user's own occurrences and nowhere else (F14).
func TestMineOtherReportersRegressionDoesNotMakeMineOne(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana, bo := "ana@example.test", "bo@example.test"
	fp := fingerprint("76")
	up := upload(t, ctx, b, hit("update-20261107183012-48213", fp, bo, "0.9.0", issueAt))
	// bo files it and a release fixes it.
	claim, err := Claim(ctx, b, up.Number, bo, sharedAt)
	if err != nil {
		t.Fatalf("claiming issue %d: %v", up.Number, err)
	}
	if err := RecordSubmitted(ctx, b, claim, "https://github.com/dimipaun/fugaro/issues/1", sharedAt); err != nil {
		t.Fatalf("filing issue %d: %v", up.Number, err)
	}
	if err := SetResolved(ctx, b, up.Number, "0.20.0", 0, sharedAt); err != nil {
		t.Fatalf("resolving issue %d: %v", up.Number, err)
	}
	// bo hits it again at the fix, and ana hits it plainly from before it.
	upload(t, ctx, b, hit("update-20261107183015-48216", fp, bo, "0.21.0", sharedAt.Add(time.Hour)))
	upload(t, ctx, b, hit("update-20261107183014-48215", fp, ana, "0.9.0", sharedAt.Add(-time.Hour)))

	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if row := mineRowOf(t, got, up.Number); row.Regression {
		t.Errorf("the row = %+v, want no regression: the flagged hit is bo's", row)
	}
}

// Every record is read through the bounded read, so one a reader could not take
// back is left out rather than failing the whole listing (F11, F24).
func TestMineBoundedRecordRead(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	ana := "ana@example.test"
	big := sized(t, "update-20261107183012-48213", fingerprint("77"), blobx.MaxReadBytes+1024)
	raw, err := marshalShared(big, 1)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}
	if _, err := b.Create(ctx, recordKey(1), raw, sharedJSON); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	if _, err := b.Create(ctx, reporterIndexKey(1, Digest(ana)), nil, sharedJSON); err != nil {
		t.Fatalf("indexing issue 1: %v", err)
	}
	got, err := ListMine(ctx, b, verified(ana), sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if len(got.Issues) != 0 {
		t.Errorf("the read answered %d rows, want none: the record is past what a reader takes back", len(got.Issues))
	}
}

// A record no fingerprint entry names is an orphan a naming that failed left
// behind, not an issue the store holds: the team's own listing skips it (F28),
// and so does this one, or its reporter is shown — and can then claim and file
// — an issue nothing else in the store will ever show (F7).
func TestMineSkipsRecordNoFingerprintEntryNames(t *testing.T) {
	ctx := context.Background()
	ana := "ana@example.test"
	fp := fingerprint("79")
	// The states a failed naming leaves the entry in, and the naming that
	// landed beside them, so what the read answers is the entry's doing rather
	// than the fixture's.
	for _, tc := range []struct {
		what  string
		entry int // -1: the entry was never written
		want  []int
	}{
		{"the entry was never written", -1, nil},
		{"the entry names no issue yet", 0, nil},
		{"the entry names another issue", 2, nil},
		{"the entry names it", 1, []int{1}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			b := sharedBucket(t)
			i := sharedIssue("update-20261107183012-48213", fp)
			i.Reporter = ana
			raw, err := marshalShared(i, 1)
			if err != nil {
				t.Fatalf("encoding the record: %v", err)
			}
			if _, err := b.Create(ctx, recordKey(1), raw, sharedJSON); err != nil {
				t.Fatalf("writing the record: %v", err)
			}
			if _, err := b.Create(ctx, reporterIndexKey(1, Digest(ana)), nil, sharedJSON); err != nil {
				t.Fatalf("indexing issue 1: %v", err)
			}
			if tc.entry >= 0 {
				entry, err := json.Marshal(numberDoc{Format: sharedFormat, Number: tc.entry})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := b.Create(ctx, fingerprintKey(fp), entry, sharedJSON); err != nil {
					t.Fatalf("writing the fingerprint entry: %v", err)
				}
			}
			got, err := ListMine(ctx, b, verified(ana), sharedAt)
			if err != nil {
				t.Fatalf("reading mine: %v", err)
			}
			if !slices.Equal(mineNumbers(got), tc.want) {
				t.Errorf("the rows are %v, want %v", mineNumbers(got), tc.want)
			}
		})
	}
}

// A user nobody could name is on no issue, and the answer is an empty read
// rather than an error.
func TestMineNobodyReadsNothing(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("78")))
	got, err := ListMine(ctx, b, Identity{Source: SourceNone}, sharedAt)
	if err != nil {
		t.Fatalf("reading mine: %v", err)
	}
	if len(got.Issues) != 0 || got.Truncated {
		t.Errorf("the read = %+v, want an empty untruncated one", got)
	}
}
