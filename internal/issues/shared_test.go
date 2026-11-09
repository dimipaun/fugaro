package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// sharedAt is the clock the shared store's tests run on.
var sharedAt = time.Date(2026, 11, 8, 17, 40, 7, 0, time.UTC)

// fingerprint is a value the record's fingerprint field takes: 64 lowercase hex
// characters, the shape of the SHA-256 verdict.go's writer makes. The fixture
// is one byte pair repeated, so no two tests share an entry and no literal here
// is the hash of anything.
func fingerprint(hexPair string) string { return strings.Repeat(hexPair, 32) }

// sharedBucket is the bucket those tests run against: gcpfake models the object
// generations the store's conditional writes are matched on, and a file://
// bucket compares contents instead, which is not what any real launcher gives
// it.
func sharedBucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	return gcpfake.NewGCS(t).Bucket(t, "fugaro-issues")
}

// sharedIssue is a local issue ready to upload: the sample's text, under a run
// stem and a fingerprint of its own, and with nothing filled in that only the
// store or a later writer fills in.
func sharedIssue(run, fingerprint string) Issue {
	i := issueLocal()
	i.Run, i.LocalID = run, localPrefix+run
	i.Fingerprint = fingerprint
	i.ResolvedIn, i.Regression, i.OriginalURL = "", false, ""
	return i
}

// hit is another machine's report of the same bug: one fingerprint, its own run
// stem, its own reporter and hit time, and the version that machine runs.
func hit(run, fingerprint, reporter, version string, at time.Time) Issue {
	i := sharedIssue(run, fingerprint)
	i.Reporter, i.FugaroVersion, i.Created = reporter, version, at
	return i
}

// upload is Upload with the error taken out, for the tests whose subject is
// what happened after it.
func upload(t *testing.T, ctx context.Context, b *blobx.Bucket, i Issue) Uploaded {
	t.Helper()
	up, err := Upload(ctx, b, i)
	if err != nil {
		t.Fatalf("uploading %s: %v", i.Run, err)
	}
	return up
}

// filedIssue uploads an issue, files it as this machine, and answers the upload
// and the URL it was filed at: a resolved issue is one that was filed, and the
// pass that learns its fix writes resolved_in on the record afterwards (F27).
func filedIssue(t *testing.T, ctx context.Context, b *blobx.Bucket, fingerprint string) (Uploaded, string) {
	t.Helper()
	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint))
	claim, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt)
	if err != nil {
		t.Fatalf("claiming issue %d: %v", up.Number, err)
	}
	url := fmt.Sprintf("https://github.com/dimipaun/fugaro/issues/%d", up.Number)
	if err := RecordSubmitted(ctx, b, claim, url); err != nil {
		t.Fatalf("filing issue %d: %v", up.Number, err)
	}
	return up, url
}

// setResolvedIn writes the release that fixed an issue, the way the `update`
// command's own spec will (F27, D1731): this plan stores the field and compares
// it, and builds no writer of it.
func setResolvedIn(t *testing.T, ctx context.Context, b *blobx.Bucket, number int, release string) {
	t.Helper()
	rec, v, err := readShared(ctx, b, number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", number, err)
	}
	rec.Issue.ResolvedIn = release
	if err := rec.setRecord(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replaceRaw(ctx, b, recordKey(number), raw, v); err != nil {
		t.Fatalf("writing the release that fixed issue %d: %v", number, err)
	}
}

// storedOccurrences is what the store holds for an issue's occurrences,
// decoded from the objects themselves: a test asserts on what a teammate reads
// rather than on what a call returned.
func storedOccurrences(t *testing.T, ctx context.Context, b *blobx.Bucket, number int) []Occurrence {
	t.Helper()
	iter := b.List(&blob.ListOptions{Prefix: occurrencePrefix(number)})
	var out []Occurrence
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("listing the occurrences of issue %d: %v", number, err)
		}
		if obj.IsDir {
			continue
		}
		raw, _, err := b.ReadMax(ctx, obj.Key, 4<<10)
		if err != nil {
			t.Fatalf("reading %s: %v", obj.Key, err)
		}
		var occ Occurrence
		if err := json.Unmarshal(raw, &occ); err != nil {
			t.Fatalf("decoding %s: %v", obj.Key, err)
		}
		out = append(out, occ)
	}
	return out
}

// sharedSize is what the store would encode an issue as at the widest number it
// can take: the size Upload checks a record against (F24).
func sharedSize(t *testing.T, i Issue) int {
	t.Helper()
	raw, err := marshalShared(i, numberMax)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}

// sized is a record whose encoded form is want bytes: a log line carries the
// size, which is where a report's bulk is.
func sized(t *testing.T, run, fingerprint string, want int) Issue {
	t.Helper()
	i := sharedIssue(run, fingerprint)
	i.Logs = []string{""}
	pad := want - sharedSize(t, i)
	if pad < 0 {
		t.Fatalf("a record cannot encode to %d bytes: the fixture is already %d", want, sharedSize(t, i))
	}
	i.Logs = []string{strings.Repeat("x", pad)}
	if got := sharedSize(t, i); got != want {
		t.Fatalf("the fixture encodes to %d bytes, want %d", got, want)
	}
	return i
}

// race opens the window between the store's read of an object and the write it
// conditions on that version, and runs fn to completion inside it: the second
// machine gets all the way through while the first one is still sitting there.
// Forcing it is what makes a test of a race the same on every run.
func race(t *testing.T, key string, fn func()) {
	t.Helper()
	var (
		mu    sync.Mutex
		fired bool
	)
	beforeConditionalWrite = func(written string) {
		if written != key {
			return
		}
		mu.Lock()
		first := !fired
		fired = true
		mu.Unlock()
		if first {
			fn()
		}
	}
	t.Cleanup(func() { beforeConditionalWrite = nil })
}

// Two machines uploading at the same instant take two numbers: the counter is
// read and written back conditioned on the version that read gave, so the
// second one to write finds the first one's number there and takes the one
// after it.
func TestConcurrentUploadsGetDistinctNumbers(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	// A project that already has an issue, so the counter exists: creating it
	// is atomic on its own, and it is the read and the write back on an
	// existing counter that race.
	upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("11")))

	var (
		other    Uploaded
		otherErr error
	)
	race(t, counterKey(), func() {
		ch := make(chan struct{})
		go func() {
			defer close(ch)
			other, otherErr = Upload(ctx, b, sharedIssue("update-20261107183015-48216", fingerprint("33")))
		}()
		<-ch
	})
	ours, err := Upload(ctx, b, sharedIssue("update-20261107183014-48215", fingerprint("22")))
	if err != nil {
		t.Fatalf("this machine's upload: %v", err)
	}
	if otherErr != nil {
		t.Fatalf("the other machine's upload: %v", otherErr)
	}
	if ours.Number == other.Number {
		t.Fatalf("two machines took the same number %d", ours.Number)
	}
	if got, want := []int{other.Number, ours.Number}, []int{2, 3}; !slices.Equal(got, want) {
		t.Errorf("the two uploads took %v, want %v", got, want)
	}
	// The counter is the last number handed out. A store that read it and
	// wrote it back without matching the version leaves 2 here, and hands 2 out
	// again to the next machine that uploads.
	v, err := readVersion(ctx, b, counterKey())
	if err != nil {
		t.Fatalf("reading the counter: %v", err)
	}
	if doc, err := decodeNumber(v.raw, counterKey()); err != nil || doc.Number != 3 {
		t.Errorf("the counter = %+v, %v; want 3", doc, err)
	}
	// Both uploads are issues a reader can find.
	rows, err := ListShared(ctx, b)
	if err != nil || len(rows) != 3 {
		t.Fatalf("the shared store = %+v, %v; want three issues", rows, err)
	}
}

// One fingerprint is one issue: the machine that reports a bug somebody already
// reported gets that issue's number and its report is an occurrence of it.
func TestSameFingerprintOneNumber(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	fp := fingerprint("44")
	first := sharedIssue("update-20261107183012-48213", fp)

	up, err := Upload(ctx, b, first)
	if err != nil || up.Number != 1 || up.Attached {
		t.Fatalf("the first upload = %+v, %v", up, err)
	}
	up2, err := Upload(ctx, b, hit("update-20261107183013-48214", fp, "bo@example.test", "0.9.0", issueAt.Add(time.Minute)))
	if err != nil || up2.Number != 1 || !up2.Attached {
		t.Fatalf("the second machine's upload = %+v, %v", up2, err)
	}
	// An upload repeated because its answer was lost is the issue that is
	// there, not an occurrence of itself.
	again, err := Upload(ctx, b, first)
	if err != nil || again.Number != 1 || again.Attached {
		t.Fatalf("the retried upload = %+v, %v", again, err)
	}
	rows, err := ListShared(ctx, b)
	if err != nil || len(rows) != 1 || rows[0].Number != 1 || rows[0].Occurrences != 1 {
		t.Fatalf("the shared store = %+v, %v; want one issue with one occurrence", rows, err)
	}

	// Two machines that both find the fingerprint new take numbers for it, and
	// the entry is what decides which of the two numbers the fingerprint gets:
	// the other one is a number nobody holds and no issue.
	t.Run("both machines numbering it at once", func(t *testing.T) {
		ctx := context.Background()
		b := sharedBucket(t)
		fp := fingerprint("55")
		var (
			other    Uploaded
			otherErr error
		)
		race(t, fingerprintKey(fp), func() {
			ch := make(chan struct{})
			go func() {
				defer close(ch)
				other, otherErr = Upload(ctx, b, hit("update-20261107183015-48216", fp, "bo@example.test", "0.9.0", issueAt.Add(2*time.Minute)))
			}()
			<-ch
		})
		ours, err := Upload(ctx, b, hit("update-20261107183014-48215", fp, "ana@example.test", "0.9.0", issueAt.Add(time.Minute)))
		if err != nil {
			t.Fatalf("this machine's upload: %v", err)
		}
		if otherErr != nil {
			t.Fatalf("the other machine's upload: %v", otherErr)
		}
		if ours.Number != other.Number {
			t.Errorf("the two uploads took %d and %d, want the one number the fingerprint has", ours.Number, other.Number)
		}
		if !ours.Attached || other.Attached {
			t.Errorf("the uploads attached %v and %v, want the second one to attach", ours.Attached, other.Attached)
		}
		rows, err := ListShared(ctx, b)
		if err != nil || len(rows) != 1 || rows[0].Number != other.Number || rows[0].Occurrences != 1 {
			t.Fatalf("the shared store = %+v, %v; want one issue with one occurrence", rows, err)
		}
		// The issue this machine wrote at the number it took and then gave back
		// is gone, and the counter is where the issue that stands is: a number
		// a fingerprint did not keep is not an issue.
		if _, err := readVersion(ctx, b, recordKey(1)); !errors.Is(err, blobx.ErrNotExist) {
			t.Errorf("the number nobody kept still holds an issue: %v", err)
		}
		v, err := readVersion(ctx, b, fingerprintKey(fp))
		if err != nil {
			t.Fatalf("reading the fingerprint's entry: %v", err)
		}
		if doc, err := decodeNumber(v.raw, fingerprintKey(fp)); err != nil || doc.Number != other.Number {
			t.Errorf("the fingerprint's entry = %+v, %v; want %d", doc, err, other.Number)
		}
	})
}

// A hit on an issue whose fix the reporter's version already has is the same
// bug back: the occurrence is flagged and linked to the issue it repeats (F25).
func TestMatchAtOrAboveResolvedInIsRegression(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	fp := fingerprint("66")
	up, url := filedIssue(t, ctx, b, fp)
	setResolvedIn(t, ctx, b, up.Number, "0.21.1")

	at := issueAt.Add(time.Minute)
	for _, version := range []string{"0.21.1", "0.22.0"} {
		t.Run(version, func(t *testing.T) {
			got, err := Upload(ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", version, at))
			if err != nil {
				t.Fatalf("uploading the hit: %v", err)
			}
			at = at.Add(time.Minute)
			if got.Number != up.Number || !got.Attached {
				t.Fatalf("the hit = %+v, want an occurrence of issue %d", got, up.Number)
			}
			if !got.Regression || got.OriginalURL != url {
				t.Errorf("the hit = %+v, want a regression linking to %s", got, url)
			}
			occs := storedOccurrences(t, ctx, b, up.Number)
			if len(occs) == 0 {
				t.Fatal("the hit was not stored as an occurrence")
			}
			last := occs[len(occs)-1]
			if !last.Regression || last.OriginalURL != url {
				t.Errorf("the stored occurrence = %+v, want a regression linking to %s", last, url)
			}
		})
	}
}

// A hit from a version below the release that fixed the issue is an ordinary
// occurrence: that reporter has the bug for the first time, not again (F25).
// An issue nothing resolved takes hits the same way.
func TestMatchBelowResolvedInAttaches(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	fp := fingerprint("77")
	up, _ := filedIssue(t, ctx, b, fp)
	setResolvedIn(t, ctx, b, up.Number, "0.21.1")

	got, err := Upload(ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", "0.20.3", issueAt.Add(time.Minute)))
	if err != nil {
		t.Fatalf("uploading the hit: %v", err)
	}
	if got.Number != up.Number || !got.Attached {
		t.Fatalf("the hit = %+v, want an occurrence of issue %d", got, up.Number)
	}
	if got.Regression || got.OriginalURL != "" {
		t.Errorf("the hit = %+v, want a plain occurrence", got)
	}
	occs := storedOccurrences(t, ctx, b, up.Number)
	if len(occs) != 1 {
		t.Fatalf("the stored occurrences = %+v, want one", occs)
	}
	if occs[0].Regression || occs[0].Reporter != "bo@example.test" || occs[0].At.IsZero() {
		t.Errorf("the stored occurrence = %+v, want a plain one by bo", occs[0])
	}
	// The issue this hit attaches to was never resolved, so a hit from any
	// version is a plain occurrence too.
	unresolved := upload(t, ctx, b, sharedIssue("update-20261107183016-48217", fingerprint("78")))
	other, err := Upload(ctx, b, hit("update-20261107183017-48218", fingerprint("78"), "cy@example.test", "0.99.0", issueAt.Add(2*time.Minute)))
	if err != nil || other.Number != unresolved.Number || other.Regression {
		t.Fatalf("the hit on an unresolved issue = %+v, %v", other, err)
	}
}

// A version that is not a release resolves nothing: a dev build, a
// pre-release, an empty version, or a resolved_in that is none of the three
// takes a hit as a plain occurrence rather than calling it a regression
// (D1736).
func TestDevOrUnparseableVersionNeverRegresses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ name, resolvedIn, reporter string }{
		{"a dev build", "0.21.1", "dev"},
		{"a version that is not a release", "0.21.1", "v0.22"},
		{"a pre-release", "0.21.1", "0.21.1-rc1"},
		{"no version at all", "0.21.1", ""},
		{"a resolved_in that is not a release", "dev", "0.22.0"},
		{"a resolved_in that is empty", "", "0.22.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sharedBucket(t)
			fp := fingerprint("88")
			up, _ := filedIssue(t, ctx, b, fp)
			if tc.resolvedIn != "" {
				setResolvedIn(t, ctx, b, up.Number, tc.resolvedIn)
			}
			got, err := Upload(ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", tc.reporter, issueAt.Add(time.Minute)))
			if err != nil {
				t.Fatalf("uploading the hit: %v", err)
			}
			if got.Regression || got.OriginalURL != "" {
				t.Errorf("the hit = %+v, want a plain occurrence", got)
			}
			if occs := storedOccurrences(t, ctx, b, up.Number); len(occs) != 1 || occs[0].Regression {
				t.Errorf("the stored occurrences = %+v, want one plain occurrence", occs)
			}
		})
	}
}

// An uploader that died between creating the fingerprint's entry and naming it
// leaves the entry holding no number; the next upload of that fingerprint takes
// the number and completes it rather than stranding the fingerprint (F28).
func TestFingerprintWithoutNumberIsCompleted(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	fp := fingerprint("99")
	entry, err := json.Marshal(numberDoc{Format: sharedFormat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(ctx, fingerprintKey(fp), entry, sharedJSON); err != nil {
		t.Fatalf("seeding the fingerprint's entry: %v", err)
	}

	up, err := Upload(ctx, b, sharedIssue("update-20261107183012-48213", fp))
	if err != nil || up.Number != 1 || up.Attached {
		t.Fatalf("the upload = %+v, %v", up, err)
	}
	v, err := readVersion(ctx, b, fingerprintKey(fp))
	if err != nil {
		t.Fatalf("reading the fingerprint's entry: %v", err)
	}
	if doc, err := decodeNumber(v.raw, fingerprintKey(fp)); err != nil || doc.Number != 1 {
		t.Errorf("the fingerprint's entry = %+v, %v; want 1", doc, err)
	}
	// The number is an issue: the next machine to report the bug attaches to
	// it rather than completing the entry again.
	got, err := Upload(ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", "0.9.0", issueAt.Add(time.Minute)))
	if err != nil || got.Number != 1 || !got.Attached {
		t.Fatalf("the next machine's upload = %+v, %v", got, err)
	}
}

// A naming write the store refuses — a flaky backend, not a lost race — must
// leave no issue behind: without that, the retry a caller makes takes a second
// number and the first record sits in the listing as an issue no fingerprint
// names. The refusal is one attempt only, so the retry that follows it succeeds
// (F28).
func TestRefusedNamingLeavesNoIssueBehind(t *testing.T) {
	// The entry is there and names nothing, so it is what the retry completes.
	t.Run("an entry that names nothing", func(t *testing.T) {
		ctx := context.Background()
		g := gcpfake.NewGCS(t)
		b := g.Bucket(t, "fugaro-issues")
		fp := fingerprint("31")
		race(t, fingerprintKey(fp), func() { g.FailObjectWrites("fugaro-issues", fingerprintKey(fp), 1) })

		issue := sharedIssue("update-20261107183012-48213", fp)
		_, err := Upload(ctx, b, issue)
		if err == nil {
			t.Fatal("an upload reported success for a naming write the store refused")
		}
		if errors.Is(err, blobx.ErrConflict) {
			t.Fatalf("the refused write read as a lost race: %v", err)
		}
		if rows, err := ListShared(ctx, b); err != nil || len(rows) != 0 {
			t.Errorf("the listing after a refused naming = %+v, %v; want nothing", rows, err)
		}
		// The entry names nothing, so the fingerprint is not stranded either: it
		// is what the retry completes.
		v, err := readVersion(ctx, b, fingerprintKey(fp))
		if err != nil {
			t.Fatalf("reading the fingerprint's entry: %v", err)
		}
		if doc, err := decodeNumber(v.raw, fingerprintKey(fp)); err != nil || doc.Number != 0 {
			t.Errorf("the fingerprint's entry = %+v, %v; want no number", doc, err)
		}
		// The retry takes the next number — the counter is not rolled back — and
		// the store holds the bug once.
		up, err := Upload(ctx, b, issue)
		if err != nil || up.Number != 2 || up.Attached {
			t.Fatalf("the retry = %+v, %v; want issue 2", up, err)
		}
		rows, err := ListShared(ctx, b)
		if err != nil || len(rows) != 1 || rows[0].Number != up.Number {
			t.Errorf("the listing after the retry = %+v, %v; want issue %d once", rows, err, up.Number)
		}
	})

	// The entry itself goes away inside the naming window: a launcher can
	// delete any object under the prefix (F24). An entry that is not there
	// names no number either, and must be read as that rather than as
	// "unknown": a record left for it is an issue no fingerprint names, listed
	// as an ordinary pending issue that anybody can claim and file as a
	// duplicate of the retry's.
	t.Run("an entry that went away", func(t *testing.T) {
		ctx := context.Background()
		g := gcpfake.NewGCS(t)
		b := g.Bucket(t, "fugaro-issues")
		fp := fingerprint("33")
		race(t, fingerprintKey(fp), func() {
			v, err := readVersion(ctx, b, fingerprintKey(fp))
			if err != nil {
				t.Fatal(err)
			}
			if err := b.DeleteIf(ctx, fingerprintKey(fp), v.gen, v.raw); err != nil {
				t.Fatal(err)
			}
			g.FailObjectWrites("fugaro-issues", fingerprintKey(fp), 1)
		})

		issue := sharedIssue("update-20261107183012-48213", fp)
		if _, err := Upload(ctx, b, issue); err == nil {
			t.Fatal("an upload reported success for a naming write the store refused")
		}
		if rows, err := ListShared(ctx, b); err != nil || len(rows) != 0 {
			t.Errorf("the listing after the naming = %+v, %v; want nothing", rows, err)
		}
		if _, err := readVersion(ctx, b, fingerprintKey(fp)); !errors.Is(err, blobx.ErrNotExist) {
			t.Errorf("the fingerprint's entry after the naming = %v; want it still gone", err)
		}
		// The retry takes the next number and the store holds the bug once.
		up, err := Upload(ctx, b, issue)
		if err != nil || up.Number != 2 || up.Attached {
			t.Fatalf("the retry = %+v, %v; want issue 2", up, err)
		}
		rows, err := ListShared(ctx, b)
		if err != nil || len(rows) != 1 || rows[0].Number != up.Number {
			t.Errorf("the listing after the retry = %+v, %v; want issue %d once", rows, err, up.Number)
		}
	})
}

// A caller that gives up while the naming write is on its way — Ctrl-C, a
// deadline — is the case the follow-up exists for: the read that decides what
// the failed write left behind, and the delete that follows it, run on a
// context of their own, so the record this attempt wrote is not left in the
// store for the retry to number a second issue beside (F21, F28).
func TestCancelledNamingLeavesNoIssueBehind(t *testing.T) {
	ctx := context.Background()
	g := gcpfake.NewGCS(t)
	b := g.Bucket(t, "fugaro-issues")
	fp := fingerprint("34")
	cctx, cancel := context.WithCancel(ctx)
	race(t, fingerprintKey(fp), cancel)

	issue := sharedIssue("update-20261107183012-48213", fp)
	if _, err := Upload(cctx, b, issue); !errors.Is(err, context.Canceled) {
		t.Fatalf("the upload = %v, want the caller's own cancellation", err)
	}
	// Nothing of the cancelled attempt is left: not a record the retry numbers
	// beside, not a row the listing hides.
	if _, err := readVersion(ctx, b, recordKey(1)); !errors.Is(err, blobx.ErrNotExist) {
		t.Errorf("the cancelled attempt left a record at the number it took: %v", err)
	}
	// The retry the contract invites takes the next number — the counter is not
	// rolled back — and the store holds the bug once.
	up, err := Upload(ctx, b, issue)
	if err != nil || up.Number != 2 || up.Attached {
		t.Fatalf("the retry = %+v, %v; want issue 2", up, err)
	}
	rows, err := ListShared(ctx, b)
	if err != nil || len(rows) != 1 || rows[0].Number != up.Number {
		t.Errorf("the listing after the retry = %+v, %v; want issue %d once", rows, err, up.Number)
	}
}

// The listing leaves out a record its fingerprint's entry does not name: the
// entry is the truth about a fingerprint (F21), so a record it does not name is
// not an issue the store holds but the debris of a naming write that never came
// back — an uploader that died there, or a store that refused the write and
// could not be asked what became of it (F28). A row there is a second issue of
// a fingerprint that already has one, and anything reads a row as an issue to
// take the claim on and file (F22).
func TestListingLeavesOutARecordNoFingerprintNames(t *testing.T) {
	ctx := context.Background()
	named := func(number int) []byte {
		raw, err := json.Marshal(numberDoc{Format: sharedFormat, Number: number})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, tc := range []struct {
		name        string
		fingerprint string
		entry       []byte
	}{
		{"an entry that names nothing", fingerprint("52"), named(0)},
		{"an entry that names another number", fingerprint("53"), named(1)},
		{"no entry at all", fingerprint("54"), nil},
		{"a record whose fingerprint is not a fingerprint", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sharedBucket(t)
			up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("51")))
			// What an uploader that died between its record and its naming left:
			// a record the store would write, at a number the counter handed out.
			orphan := sharedIssue("update-20261107183013-48214", tc.fingerprint)
			raw, err := marshalShared(orphan, up.Number+1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Create(ctx, recordKey(up.Number+1), raw, sharedJSON); err != nil {
				t.Fatalf("writing the record no entry names: %v", err)
			}
			if tc.entry != nil {
				if _, err := b.Create(ctx, fingerprintKey(orphan.Fingerprint), tc.entry, sharedJSON); err != nil {
					t.Fatalf("writing the entry: %v", err)
				}
			}
			rows, err := ListShared(ctx, b)
			if err != nil || len(rows) != 1 || rows[0].Number != up.Number {
				t.Errorf("the listing = %+v, %v; want issue %d once", rows, err, up.Number)
			}
		})
	}
}

// A naming write that landed before its answer was lost is the case the entry
// has to settle: the write is there, so the issue is in the store and must not
// be dropped as an orphan — dropping it strands the fingerprint, and every
// later report of that bug fails with no way back (F21, F28).
func TestNamingThatLandedBeforeItsErrorKeepsTheIssue(t *testing.T) {
	ctx := context.Background()
	g := gcpfake.NewGCS(t)
	b := g.Bucket(t, "fugaro-issues")
	fp := fingerprint("32")
	// The fake stores this write and then answers an error: the naming landed,
	// and the caller cannot tell that from a write that never happened.
	race(t, fingerprintKey(fp), func() { g.FailObjectStoresAfterWrite(1) })

	up, err := Upload(ctx, b, sharedIssue("update-20261107183012-48213", fp))
	if err != nil || up.Number != 1 {
		t.Fatalf("the upload = %+v, %v; want issue 1", up, err)
	}
	rows, err := ListShared(ctx, b)
	if err != nil || len(rows) != 1 || rows[0].Number != 1 {
		t.Fatalf("the listing = %+v, %v; want issue 1 once", rows, err)
	}
	// The entry and the issue agree, so the next machine to hit the bug attaches
	// to it instead of meeting a fingerprint that names nothing.
	got := upload(t, ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", "0.9.0", issueAt.Add(time.Minute)))
	if got.Number != 1 || !got.Attached {
		t.Errorf("the next machine's upload = %+v, want an attached occurrence of issue 1", got)
	}
}

// A conditional write can also meet the entry it just wrote — the storage
// client retries a write whose response it never got, and the retry meets the
// version the first attempt left — and the store answers a conflict. The entry
// decides that case too: it names this upload's number, so the record under it
// is the issue, and deleting it (what the conflict path did on its own) leaves
// a fingerprint naming an issue the store does not have, which every later
// report of that bug fails on with no way back (F21, F28).
func TestNamingConflictReadsTheEntryBack(t *testing.T) {
	ctx := context.Background()
	g := gcpfake.NewGCS(t)
	b := g.Bucket(t, "fugaro-issues")
	fp := fingerprint("43")
	// The entry holds this upload's own number before the naming write runs, so
	// the write is conditioned on a version that is already gone.
	named, err := json.Marshal(numberDoc{Format: sharedFormat, Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	race(t, fingerprintKey(fp), func() { g.Put("fugaro-issues", fingerprintKey(fp), named) })

	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fp))
	if up.Number != 1 || up.Attached {
		t.Fatalf("the upload = %+v, want issue 1", up)
	}
	if rows, err := ListShared(ctx, b); err != nil || len(rows) != 1 || rows[0].Number != 1 {
		t.Fatalf("the listing = %+v, %v; want issue 1 once", rows, err)
	}
	// The entry and the issue agree, so the next machine to hit the bug attaches
	// to it instead of meeting a fingerprint that names nothing.
	got := upload(t, ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", "0.9.0", sharedAt.Add(time.Minute)))
	if got.Number != 1 || !got.Attached {
		t.Errorf("the next machine's upload = %+v, want an attached occurrence of issue 1", got)
	}
}

// A hit on an issue the team set aside or filed is recorded and reopens
// nothing (F21): the status stays where the team put it, and the occurrence is
// what shows the bug is still there.
func TestOccurrenceOnDismissedReopensNothing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		closing func(t *testing.T, b *blobx.Bucket, number int)
		status  Status
	}{
		{
			name: "a dismissed issue",
			closing: func(t *testing.T, b *blobx.Bucket, number int) {
				if err := SetSharedStatus(ctx, b, number, StatusDismissed); err != nil {
					t.Fatalf("dismissing issue %d: %v", number, err)
				}
				// Asking for the status it already has writes nothing and is
				// not an error.
				if err := SetSharedStatus(ctx, b, number, StatusDismissed); err != nil {
					t.Errorf("dismissing issue %d twice: %v", number, err)
				}
			},
			status: StatusDismissed,
		},
		{
			name: "a filed issue",
			closing: func(t *testing.T, b *blobx.Bucket, number int) {
				claim, err := Claim(ctx, b, number, "user:ana@example.test", sharedAt)
				if err != nil {
					t.Fatalf("claiming issue %d: %v", number, err)
				}
				if err := RecordSubmitted(ctx, b, claim, "https://github.com/dimipaun/fugaro/issues/12"); err != nil {
					t.Fatalf("filing issue %d: %v", number, err)
				}
			},
			status: StatusSubmitted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sharedBucket(t)
			fp := fingerprint("12")
			up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fp))
			tc.closing(t, b, up.Number)

			got, err := Upload(ctx, b, hit("update-20261107183014-48215", fp, "bo@example.test", "0.9.0", issueAt.Add(time.Minute)))
			if err != nil || got.Number != up.Number || !got.Attached {
				t.Fatalf("the hit = %+v, %v; want an occurrence of issue %d", got, err, up.Number)
			}
			rec, _, err := readShared(ctx, b, up.Number)
			if err != nil {
				t.Fatalf("reading shared issue %d: %v", up.Number, err)
			}
			if rec.Issue.Status != tc.status {
				t.Errorf("the hit moved the issue to %q, want %q", rec.Issue.Status, tc.status)
			}
			rows, err := ListShared(ctx, b)
			if err != nil || len(rows) != 1 || rows[0].Status != tc.status || rows[0].Occurrences != 1 {
				t.Fatalf("the shared store = %+v, %v; want one %s issue with one occurrence", rows, err, tc.status)
			}
		})
	}
}

// One submitter files an issue: the claim is taken on the record's version and
// the second machine is told who holds it and until when (F9, F22).
func TestClaimIsExclusive(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("13")))

	held, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt)
	if err != nil {
		t.Fatalf("claiming issue %d: %v", up.Number, err)
	}
	if held.Number != up.Number || held.Holder != "user:ana@example.test" || held.Issue.Number != up.Number {
		t.Fatalf("the claim = %+v, want %d held by ana", held, up.Number)
	}
	if held.Issue.Status != StatusPending || held.Issue.Fingerprint != fingerprint("13") {
		t.Errorf("the claimed issue = %+v, want the record as it stands", held.Issue)
	}

	_, err = Claim(ctx, b, up.Number, "user:bo@example.test", sharedAt.Add(time.Minute))
	var other *ClaimedError
	if !errors.As(err, &other) {
		t.Fatalf("the second claim = %v, want the holder reported", err)
	}
	if other.Holder != "user:ana@example.test" || !other.Expires.Equal(sharedAt.Add(claimLease)) {
		t.Errorf("the refusal = %+v, want ana's claim until %s", other, sharedAt.Add(claimLease))
	}
	// The holder asking again is not somebody else's claim.
	if _, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt.Add(time.Minute)); err != nil {
		t.Errorf("the holder claiming again: %v", err)
	}
	rec, _, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if rec.Claim == nil || !rec.Claim.ExpiresAt.Equal(sharedAt.Add(time.Minute+claimLease)) {
		t.Errorf("the stored claim = %+v, want it refreshed", rec.Claim)
	}
}

// A lease lapses on its own so a submitter whose machine died cannot block the
// issue for the team; taking it over is the same conditional write, so the
// submitter it was taken from cannot file it afterwards (F9, F19, F22).
func TestClaimTakeoverIsVersioned(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)

	t.Run("a lapsed claim is taken over", func(t *testing.T) {
		up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("14")))
		first, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt)
		if err != nil {
			t.Fatalf("claiming issue %d: %v", up.Number, err)
		}
		second, err := Claim(ctx, b, up.Number, "user:bo@example.test", sharedAt.Add(claimLease))
		if err != nil {
			t.Fatalf("taking over the lapsed claim: %v", err)
		}
		// Ana previewed the version she claimed, and that version is gone: her
		// filing must not land under bo's claim (F19).
		if err := RecordSubmitted(ctx, b, first, "https://github.com/dimipaun/fugaro/issues/14"); !errors.Is(err, blobx.ErrConflict) {
			t.Errorf("filing from a claim that was taken over = %v, want a version conflict", err)
		}
		rec, _, err := readShared(ctx, b, up.Number)
		if err != nil {
			t.Fatalf("reading shared issue %d: %v", up.Number, err)
		}
		if rec.Issue.Status != StatusPending {
			t.Errorf("the issue is %q after the stale filing, want pending", rec.Issue.Status)
		}
		if err := RecordSubmitted(ctx, b, second, "https://github.com/dimipaun/fugaro/issues/14"); err != nil {
			t.Fatalf("bo filing the issue: %v", err)
		}
	})

	// Two machines taking over the same lapsed claim: the record's generation
	// is what decides, so exactly one of them holds it.
	t.Run("two takeovers at once", func(t *testing.T) {
		up := upload(t, ctx, b, sharedIssue("update-20261107183018-48219", fingerprint("15")))
		if _, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt); err != nil {
			t.Fatalf("claiming issue %d: %v", up.Number, err)
		}
		now := sharedAt.Add(claimLease + time.Minute)
		var (
			other    *Claimed
			otherErr error
		)
		race(t, recordKey(up.Number), func() {
			ch := make(chan struct{})
			go func() {
				defer close(ch)
				other, otherErr = Claim(ctx, b, up.Number, "user:bo@example.test", now)
			}()
			<-ch
		})
		_, err := Claim(ctx, b, up.Number, "user:carol@example.test", now)
		var held *ClaimedError
		if !errors.As(err, &held) {
			t.Fatalf("the losing takeover = %v, want the holder reported", err)
		}
		if otherErr != nil {
			t.Fatalf("the takeover that won: %v", otherErr)
		}
		if held.Holder != other.Holder || other.Holder != "user:bo@example.test" {
			t.Errorf("the losing takeover was told %q, want %q", held.Holder, other.Holder)
		}
	})
}

// A submission is recorded by the submitter that holds the claim, on the
// version it was taken at; there is no other way in (F22).
func TestSubmittedNeedsTheClaim(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("16")))

	// A claim this machine never took, and a claim it took and released.
	if err := RecordSubmitted(ctx, b, &Claimed{Number: up.Number, Holder: "user:bo@example.test"}, "https://github.com/dimipaun/fugaro/issues/16"); err == nil {
		t.Error("a submission was recorded with no claim to condition it on")
	}
	released, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt)
	if err != nil {
		t.Fatalf("claiming issue %d: %v", up.Number, err)
	}
	if err := Release(ctx, b, released); err != nil {
		t.Fatalf("releasing the claim: %v", err)
	}
	if err := RecordSubmitted(ctx, b, released, "https://github.com/dimipaun/fugaro/issues/16"); err == nil {
		t.Error("a submission was recorded by a submitter that gave the claim back")
	}
	rec, _, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if rec.Issue.Status != StatusPending || rec.Issue.URL != "" {
		t.Errorf("the issue = %+v, want it untouched", rec.Issue)
	}

	// The submitter that holds it records the submission and the URL, and the
	// claim is cleared in the same write.
	claim, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("claiming issue %d again: %v", up.Number, err)
	}
	if err := RecordSubmitted(ctx, b, claim, "https://github.com/dimipaun/fugaro/issues/16"); err != nil {
		t.Fatalf("filing issue %d: %v", up.Number, err)
	}
	rec, _, err = readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if rec.Issue.Status != StatusSubmitted || rec.Issue.URL != "https://github.com/dimipaun/fugaro/issues/16" || !rec.Issue.Uploaded {
		t.Errorf("the filed issue = %+v, want it submitted with its URL", rec.Issue)
	}
	if rec.Claim != nil {
		t.Errorf("the claim is still held after the filing: %+v", rec.Claim)
	}
	// A URL that is not an issue of this project is refused: the post is a
	// link a reader follows.
	if err := RecordSubmitted(ctx, b, claim, "https://github.com/attacker/fugaro/issues/16"); err == nil {
		t.Error("a submission was recorded with somebody else's URL")
	}
}

// The store is writable by every launcher (F24) and what comes out of it is
// printed and opened, so a record that links anywhere but to an issue of this
// project is refused, and a reading that meets one leaves it out rather than
// failing (F28).
func TestRejectsForeignIssueURL(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"", false},
		{"https://github.com/dimipaun/fugaro/issues/7", true},
		{"https://github.com/dimipaun/fugaro/issues/00007", true},
		{"http://github.com/dimipaun/fugaro/issues/7", false},
		{"https://github.com/attacker/fugaro/issues/7", false},
		{"https://github.com/dimipaun/fugaro/pull/7", false},
		{"https://github.com/dimipaun/fugaro/issues/", false},
		{"https://github.com/dimipaun/fugaro/issues/7/../../attacker", false},
		{"https://github.com/dimipaun/fugaro/issues/abc", false},
		{"https://github.com/dimipaun/fugaro/issues/0", false},
		{"https://github.com/dimipaun/fugaro/issues/1000000", false},
		{"javascript:alert(1)", false},
		{"https://github.com/dimipaun/fugaro/issues/7\nhttps://attacker.example", false},
	} {
		if got := projectIssueURL(tc.url); got != tc.want {
			t.Errorf("projectIssueURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}

	for _, tc := range []struct{ name, url, original string }{
		{"a foreign issue", "https://github.com/attacker/fugaro/issues/7", ""},
		{"a foreign original", "https://github.com/dimipaun/fugaro/issues/7", "https://github.com/attacker/fugaro/issues/3"},
		{"something that is not a link at all", "not a URL", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sharedBucket(t)
			up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("17")))
			rec, v, err := readShared(ctx, b, up.Number)
			if err != nil {
				t.Fatalf("reading shared issue %d: %v", up.Number, err)
			}
			rec.Issue.URL, rec.Issue.OriginalURL = tc.url, tc.original
			if err := rec.setRecord(); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replaceRaw(ctx, b, recordKey(up.Number), raw, v); err != nil {
				t.Fatalf("writing the record: %v", err)
			}
			if _, _, err := readShared(ctx, b, up.Number); err == nil {
				t.Errorf("reading a record linking to %q was allowed", tc.url)
			}
			rows, err := ListShared(ctx, b)
			if err != nil || len(rows) != 0 {
				t.Errorf("the listing = %+v, %v; want the record left out", rows, err)
			}
		})
	}

	// The write path refuses what the read path refuses. A local record is a
	// file a person can edit, and an upload that reported success for one no
	// reader accepts would retire the local copy (the caller marks it uploaded)
	// while the issue reaches no listing, and would strand the fingerprint on a
	// record nobody can read: every later report of that bug would fail.
	t.Run("an upload that carries one", func(t *testing.T) {
		b := sharedBucket(t)
		fp := fingerprint("17")
		for _, tc := range []struct{ name, url, original string }{
			{"a foreign issue", "https://github.com/attacker/fugaro/issues/7", ""},
			{"a foreign original", "https://github.com/dimipaun/fugaro/issues/7", "https://github.com/attacker/fugaro/issues/3"},
			{"something that is not a link at all", "not a URL", ""},
		} {
			bad := sharedIssue("update-20261107183012-48213", fp)
			bad.URL, bad.OriginalURL = tc.url, tc.original
			if _, err := Upload(ctx, b, bad); err == nil {
				t.Errorf("uploading a record with %s (%q) was allowed", tc.name, tc.url)
			}
		}
		// The refusal happens before anything is written, so the fingerprint is
		// not stranded: the same issue uploaded with its links straightened out
		// takes its number.
		if _, err := readVersion(ctx, b, counterKey()); !errors.Is(err, blobx.ErrNotExist) {
			t.Errorf("a refused upload moved the counter: %v", err)
		}
		if _, err := readVersion(ctx, b, fingerprintKey(fp)); !errors.Is(err, blobx.ErrNotExist) {
			t.Errorf("a refused upload took the fingerprint: %v", err)
		}
		up, err := Upload(ctx, b, sharedIssue("update-20261107183012-48213", fp))
		if err != nil || up.Number != 1 {
			t.Fatalf("the corrected upload = %+v, %v; want issue 1", up, err)
		}
	})

	// An occurrence is an object the team reads and follows the link it carries
	// (F21), so the store's own door for one refuses a foreign original_url
	// exactly as an upload does: a plain hit may carry none at all, a
	// regression must carry one, and neither may carry somebody else's.
	t.Run("an occurrence that carries one", func(t *testing.T) {
		b := sharedBucket(t)
		up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("25")))
		for _, tc := range []struct {
			name string
			occ  Occurrence
		}{
			{"a regression", Occurrence{Reporter: "bo@example.test", At: sharedAt, Regression: true, OriginalURL: "https://github.com/attacker/fugaro/issues/3"}},
			{"a plain hit", Occurrence{Reporter: "bo@example.test", At: sharedAt, OriginalURL: "not a URL"}},
		} {
			if err := AttachOccurrence(ctx, b, up.Number, tc.occ); err == nil {
				t.Errorf("%s carrying %q was recorded", tc.name, tc.occ.OriginalURL)
			}
		}
		if occs := storedOccurrences(t, ctx, b, up.Number); len(occs) != 0 {
			t.Errorf("the store holds %+v, want nothing", occs)
		}
		if err := AttachOccurrence(ctx, b, up.Number, Occurrence{Reporter: "bo@example.test", At: sharedAt, Regression: true,
			OriginalURL: "https://github.com/dimipaun/fugaro/issues/1"}); err != nil {
			t.Errorf("an occurrence linking to issue 1 was refused: %v", err)
		}
	})
}

// A filed issue is final: the store never moves it back, never sets it aside
// again and never files it twice (F9) — except for the one transition the
// resolver makes, `submitted` to `resolved` or `dismissed`, which is the two
// calls of its own below (F5 amended).
func TestSubmittedStaysFinalOtherwise(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	up, url := filedIssue(t, ctx, b, fingerprint("18"))

	for _, status := range []Status{StatusDismissed, StatusPending, StatusSubmitted} {
		if err := SetSharedStatus(ctx, b, up.Number, status); err == nil {
			t.Errorf("a filed issue was set to %q", status)
		}
	}
	if _, err := Claim(ctx, b, up.Number, "user:bo@example.test", sharedAt.Add(time.Hour)); err == nil {
		t.Error("a filed issue was claimed again")
	}
	if err := RecordSubmitted(ctx, b, &Claimed{Number: up.Number, Holder: "user:bo@example.test"}, url); err == nil {
		t.Error("a filed issue was submitted a second time")
	}
	rec, _, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if rec.Issue.Status != StatusSubmitted || rec.Issue.URL != url {
		t.Errorf("the filed issue = %+v, want it unchanged", rec.Issue)
	}

	// The two that may move it, and only from `submitted`: everything else is
	// where the issue stands, and a transition out of it would rewrite a
	// record that a person, or another machine's pass, decided.
	fixed, _ := filedIssue(t, ctx, b, fingerprint("19"))
	if err := SetResolved(ctx, b, fixed.Number, "0.22.0", 0); err != nil {
		t.Errorf("the resolver could not mark a filed issue resolved: %v", err)
	}
	closed, _ := filedIssue(t, ctx, b, fingerprint("1a"))
	if err := SetClosed(ctx, b, closed.Number, ClosedNotPlanned, 0); err != nil {
		t.Errorf("the resolver could not set aside a filed issue: %v", err)
	}
	// The transition a record is already in is a no-op, not an error: a
	// second machine's pass, or this machine a day later, finds the issue
	// where it was left and is content. The other transition, and every other
	// status, is refused: nothing moves an issue that was decided.
	if err := SetResolved(ctx, b, fixed.Number, "0.23.0", 0); err != nil {
		t.Errorf("a second pass over a resolved issue failed: %v", err)
	}
	if err := SetClosed(ctx, b, closed.Number, ClosedDuplicate, 0); err != nil {
		t.Errorf("a second pass over a dismissed issue failed: %v", err)
	}
	if err := SetClosed(ctx, b, fixed.Number, ClosedNotPlanned, 0); err == nil {
		t.Error("the resolver set a resolved issue aside")
	}
	if err := SetResolved(ctx, b, closed.Number, "0.23.0", 0); err == nil {
		t.Error("the resolver resolved a dismissed issue")
	}
}

// resolved is a filed issue the resolver has marked fixed, and the URL it was
// filed at: the state every later reader — the report, `issue list`, another
// pass — reads (the update spec's F27).
func resolved(t *testing.T, ctx context.Context, b *blobx.Bucket, fingerprint, release string) (Uploaded, string) {
	t.Helper()
	up, url := filedIssue(t, ctx, b, fingerprint)
	if err := SetResolved(ctx, b, up.Number, release, 0); err != nil {
		t.Fatalf("resolving issue %d: %v", up.Number, err)
	}
	return up, url
}

// The transition is the resolver's alone, and a write of it lands only on the
// version it read: two machines resolving one issue write one transition, the
// second one re-reads, finds the issue fixed and answers nothing rather than
// writing the copy it read (F18).
func TestSetResolvedIsIdempotentUnderRace(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	up, _ := filedIssue(t, ctx, b, fingerprint("1b"))

	var other error
	race(t, recordKey(up.Number), func() {
		ch := make(chan struct{})
		go func() {
			defer close(ch)
			other = SetResolved(ctx, b, up.Number, "0.23.0", 0)
		}()
		<-ch
	})
	if err := SetResolved(ctx, b, up.Number, "0.22.0", 0); err != nil {
		t.Fatalf("this machine's transition: %v", err)
	}
	if other != nil {
		t.Fatalf("the other machine's transition failed: %v", other)
	}
	rec, _, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	// Both machines saw the issue fixed, so both are content; the store holds
	// the one that wrote first, and the machine that lost its race did not
	// write the release it had read over it.
	if rec.Issue.Status != StatusResolved || rec.Issue.ResolvedIn != "0.23.0" {
		t.Errorf("the issue is %q fixed in %q, want %q in 0.23.0 (the transition that landed first)",
			rec.Issue.Status, rec.Issue.ResolvedIn, StatusResolved)
	}
	// A second pass over an issue that is already fixed is the ordinary case:
	// another machine's update, or this machine a day later, finds it fixed
	// and writes nothing.
	if err := SetResolved(ctx, b, up.Number, "0.22.0", 0); err != nil {
		t.Fatalf("a second pass over a resolved issue failed: %v", err)
	}
	rec, _, err = readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if rec.Issue.ResolvedIn != "0.23.0" {
		t.Errorf("a second pass rewrote resolved_in to %q", rec.Issue.ResolvedIn)
	}
}

// Where the transition may start from, and what it refuses: a release that is
// not a release is no verdict to record, and an issue that is not filed has no
// fix to record (F4, F18).
func TestSetResolvedOnlyFromSubmitted(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	pending := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("1c")))

	for _, release := range []string{"", "dev", "0.22", "0.22.0-rc1", "main", "0.22.0.1"} {
		if err := SetResolved(ctx, b, pending.Number, release, 0); err == nil {
			t.Errorf("the resolver recorded %q as a release", release)
		}
	}
	if err := SetResolved(ctx, b, pending.Number, "0.22.0", 0); err == nil {
		t.Error("the resolver resolved an issue nobody filed")
	}
	if err := SetClosed(ctx, b, pending.Number, ClosedNotPlanned, 0); err == nil {
		t.Error("the resolver dismissed an issue nobody filed")
	}
	if err := SetResolved(ctx, b, pending.Number+100, "0.22.0", 0); err == nil {
		t.Error("the resolver resolved an issue the store does not hold")
	}
	rec, _, err := readShared(ctx, b, pending.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", pending.Number, err)
	}
	if rec.Issue.Status != StatusPending || rec.Issue.ResolvedIn != "" || rec.Issue.ClosedAs != "" {
		t.Errorf("a refused transition left the record as %+v", rec.Issue)
	}
	// The version a caller read is the one the write lands on: a caller that
	// hands in another one is told the record moved rather than writing what
	// it read over it.
	up, _ := filedIssue(t, ctx, b, fingerprint("1d"))
	_, v, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if err := SetResolved(ctx, b, up.Number, "0.22.0", v.gen+1); err == nil {
		t.Error("the resolver wrote a record at a generation it did not read")
	}
	if err := SetResolved(ctx, b, up.Number, "0.22.0", v.gen); err != nil {
		t.Errorf("the resolver refused the generation it read: %v", err)
	}
}

// An issue closed without a fix is set aside with the reason, so the team sees
// why it closed and nothing else on the record moves (F9, D1752).
func TestSetClosedRecordsReason(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	notPlanned, _ := filedIssue(t, ctx, b, fingerprint("1e"))
	for _, reason := range []ClosedAs{"", "completed", "fixed", ClosedAs("dismissed")} {
		if err := SetClosed(ctx, b, notPlanned.Number, reason, 0); err == nil {
			t.Errorf("the resolver recorded %q as a reason an issue closed", reason)
		}
	}
	if err := SetClosed(ctx, b, notPlanned.Number, ClosedNotPlanned, 0); err != nil {
		t.Fatalf("setting the issue aside: %v", err)
	}
	duplicate, _ := filedIssue(t, ctx, b, fingerprint("1f"))
	if err := SetClosed(ctx, b, duplicate.Number, ClosedDuplicate, 0); err != nil {
		t.Fatalf("setting the duplicate aside: %v", err)
	}
	for _, c := range []struct {
		number int
		reason ClosedAs
	}{{notPlanned.Number, ClosedNotPlanned}, {duplicate.Number, ClosedDuplicate}} {
		rec, _, err := readShared(ctx, b, c.number)
		if err != nil {
			t.Fatalf("reading shared issue %d: %v", c.number, err)
		}
		if rec.Issue.Status != StatusDismissed || rec.Issue.ClosedAs != c.reason {
			t.Errorf("shared issue %d = %q with %q, want %q with %q",
				c.number, rec.Issue.Status, rec.Issue.ClosedAs, StatusDismissed, c.reason)
		}
		if rec.Issue.ResolvedIn != "" || !rec.Issue.Uploaded || rec.Issue.URL == "" {
			t.Errorf("setting shared issue %d aside changed the rest of it: %+v", c.number, rec.Issue)
		}
	}
	// The same answer twice is the same record: a second pass over an issue
	// already set aside writes nothing, and the reason it recorded stays.
	if err := SetClosed(ctx, b, notPlanned.Number, ClosedDuplicate, 0); err != nil {
		t.Fatalf("a second pass over a dismissed issue failed: %v", err)
	}
	rec, _, err := readShared(ctx, b, notPlanned.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", notPlanned.Number, err)
	}
	if rec.Issue.ClosedAs != ClosedNotPlanned {
		t.Errorf("a second pass rewrote closed_as to %q", rec.Issue.ClosedAs)
	}
}

// Who hit an issue is the reporter of the record that filed it plus every
// occurrence's reporter, each one once (F10): the same person on two machines
// is one name, and the order is the store's, so two runs print the same line.
func TestReportersMergesOccurrences(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	up, _ := filedIssue(t, ctx, b, fingerprint("21"))
	// The record that filed it is ana@example.test (the fixture's reporter).
	got, err := Reporters(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading the reporters: %v", err)
	}
	if want := []string{"ana@example.test"}; !slices.Equal(got, want) {
		t.Errorf("the reporters of an issue nobody else hit are %v, want %v", got, want)
	}

	for _, occ := range []Occurrence{
		{Reporter: "bo@example.test", At: sharedAt},
		{Reporter: "ana@example.test", At: sharedAt.Add(time.Minute)},
		{Reporter: "", At: sharedAt.Add(2 * time.Minute)},
		{Reporter: "cy@example.test", At: sharedAt.Add(3 * time.Minute)},
	} {
		if err := AttachOccurrence(ctx, b, up.Number, occ); err != nil {
			t.Fatalf("recording an occurrence: %v", err)
		}
	}
	// One object a launcher rewrote under the prefix answers as little as one
	// that is not there: the pass prints the names it could read (F24).
	if _, err := b.Create(ctx, occurrencePrefix(up.Number)+"junk", []byte("not an occurrence"), sharedJSON); err != nil {
		t.Fatalf("seeding an unreadable occurrence: %v", err)
	}
	got, err = Reporters(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading the reporters: %v", err)
	}
	want := []string{"ana@example.test", "bo@example.test", "cy@example.test"}
	if !slices.Equal(got, want) {
		t.Errorf("the reporters are %v, want %v", got, want)
	}
	if _, err := Reporters(ctx, b, up.Number+100); err == nil {
		t.Error("the reporters of an issue the store does not hold were reported")
	}
}

// A regression finds the resolved issue it repeats: the fingerprint is still
// the one the store holds, so a hit from a version that already has the fix is
// flagged and linked to the GitHub issue, and the fix itself is not undone
// (F14, F25).
func TestResolvedIssueIsFoundByFingerprintAsRegression(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	up, url := resolved(t, ctx, b, fingerprint("22"), "0.21.0")

	other := hit("update-20261107183020-48220", fingerprint("22"), "bo@example.test", "0.22.1", sharedAt.Add(time.Minute))
	got := upload(t, ctx, b, other)
	if !got.Attached || !got.Regression || got.OriginalURL != url {
		t.Fatalf("a hit on a resolved issue = %+v, want it attached to %s as a regression", got, url)
	}
	rec, _, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading shared issue %d: %v", up.Number, err)
	}
	if rec.Issue.Status != StatusResolved || rec.Issue.ResolvedIn != "0.21.0" || rec.Issue.URL != url {
		t.Errorf("the hit changed the resolved issue: %+v", rec.Issue)
	}
	// The reporter that is still below the fix is an ordinary hit of an issue
	// that is already filed: they are not told it came back (F25).
	below := hit("update-20261107183021-48221", fingerprint("22"), "cy@example.test", "0.20.0", sharedAt.Add(2*time.Minute))
	if got := upload(t, ctx, b, below); got.Regression || got.OriginalURL != "" {
		t.Errorf("a hit from below the fix = %+v, want a plain occurrence", got)
	}
}

// shape joins the pieces of a credential-shaped fixture: this file carries no
// secret-shaped literal, and a pattern pass is tested on shapes.
func shape(parts ...string) string { return strings.Join(parts, "") }

// An occurrence carries a teammate's own text into an object the whole team
// reads, so the excerpt goes in through the redaction a report gets (F5) —
// whichever path attached it. The fixture is asserted to leak as it stands, so
// a store that wrote it verbatim fails this test rather than passing it.
func TestOccurrenceExcerptIsRedacted(t *testing.T) {
	ctx := context.Background()
	leaky := "the update failed for ana@example.test under " + shape("/home/", "ana/.fugaro") +
		", printing " + shape("ghp_", strings.Repeat("A1b2C3d4", 5))
	for _, piece := range []string{"ana@example.test", shape("/home/", "ana"), shape("ghp_")} {
		if !strings.Contains(leaky, piece) {
			t.Fatalf("the fixture does not carry %q", piece)
		}
	}

	// stored asserts on what the bucket holds: a teammate reads the object, not
	// the call's return value.
	stored := func(t *testing.T, b *blobx.Bucket, number int) Occurrence {
		t.Helper()
		occs := storedOccurrences(t, ctx, b, number)
		if len(occs) != 1 {
			t.Fatalf("the store holds %d occurrences of issue %d, want 1", len(occs), number)
		}
		for _, piece := range []string{"ana@example.test", shape("/home/", "ana"), shape("ghp_")} {
			if strings.Contains(occs[0].Excerpt, piece) {
				t.Errorf("the stored excerpt kept %q: %q", piece, occs[0].Excerpt)
			}
		}
		if !strings.Contains(occs[0].Excerpt, redactedValue) {
			t.Errorf("the stored excerpt carries no redaction marker: %q", occs[0].Excerpt)
		}
		if !strings.Contains(occs[0].Excerpt, "the update failed for") {
			t.Errorf("the stored excerpt lost the text around it: %q", occs[0].Excerpt)
		}
		return occs[0]
	}

	// The path the upload takes: a machine hits an issue that is already there,
	// and the excerpt is cut from its summary.
	t.Run("an upload that hits an issue again", func(t *testing.T) {
		b := sharedBucket(t)
		up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("21")))
		other := hit("update-20261107183020-48220", fingerprint("21"), "bo@example.test", "0.9.0", sharedAt.Add(time.Minute))
		other.Summary = leaky
		if got := upload(t, ctx, b, other); !got.Attached {
			t.Fatalf("the second upload = %+v, want an attached occurrence", got)
		}
		stored(t, b, up.Number)
	})

	// The store's own door, for a caller that already has an occurrence in
	// hand.
	t.Run("recorded on its own", func(t *testing.T) {
		b := sharedBucket(t)
		up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("22")))
		occ := Occurrence{Reporter: "bo@example.test", At: sharedAt.Add(time.Minute), Excerpt: leaky}
		if err := AttachOccurrence(ctx, b, up.Number, occ); err != nil {
			t.Fatalf("recording an occurrence of issue %d: %v", up.Number, err)
		}
		stored(t, b, up.Number)
	})

	// The excerpt is one line a list can show, and a credential the cut lands
	// inside would be left shorter than the pattern that finds it — a head of a
	// token in the object the whole team reads. The pass runs on the whole line,
	// so what the cut keeps of it is a marker.
	t.Run("a credential the cut would shorten", func(t *testing.T) {
		b := sharedBucket(t)
		up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("25")))
		token := shape("ghp_", strings.Repeat("A1b2C3d4", 5))
		other := hit("update-20261107183020-48220", fingerprint("25"), "bo@example.test", "0.9.0", sharedAt.Add(time.Minute))
		other.Summary = strings.Repeat("x", occurrenceExcerptMax-20) + " " + token
		if !strings.Contains(other.Summary, token) {
			t.Fatal("the fixture does not carry the credential")
		}
		if got := upload(t, ctx, b, other); !got.Attached {
			t.Fatalf("the second upload = %+v, want an attached occurrence", got)
		}
		occs := storedOccurrences(t, ctx, b, up.Number)
		if len(occs) != 1 {
			t.Fatalf("the store holds %d occurrences of issue %d, want 1", len(occs), up.Number)
		}
		for _, piece := range []string{shape("ghp_"), "A1b2C3d4"} {
			if strings.Contains(occs[0].Excerpt, piece) {
				t.Errorf("the cut left %q of the credential in the stored excerpt: %q", piece, occs[0].Excerpt)
			}
		}
		if !strings.Contains(occs[0].Excerpt, redactedValue) {
			t.Errorf("the stored excerpt carries no redaction marker: %q", occs[0].Excerpt)
		}
	})
}

// The store is where the whole issue lives, so every field of it a person wrote
// goes in with the report's redaction over it (F5): the local record is a file
// a person can edit, and a credential that reached it must not be published to
// the team. The header's identity stays — the store keeps the reporter and the
// run, and only the post to GitHub strips them (D1722).
func TestSharedRecordIsRedacted(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	leaky := "the update failed for bo@example.test under " + shape("/home/", "bo/.fugaro") +
		", printing " + shape("ghp_", strings.Repeat("A1b2C3d4", 5)) + "\nand then it stopped"
	for _, piece := range []string{"bo@example.test", shape("/home/", "bo/"), shape("ghp_")} {
		if !strings.Contains(leaky, piece) {
			t.Fatalf("the fixture does not carry %q", piece)
		}
	}
	i := sharedIssue("update-20261107183012-48213", fingerprint("44"))
	i.Summary, i.Diagnosis, i.Steps = leaky, leaky, leaky
	i.Logs = []string{leaky}
	i.FailedCommand = "fugaro run --repo fugaro " + shape("ghp_", strings.Repeat("A1b2C3d4", 5))
	up := upload(t, ctx, b, i)

	// What the bucket holds, not what a call returned: the object is what a
	// teammate reads.
	raw, _, err := b.ReadMax(ctx, recordKey(up.Number), blobx.MaxReadBytes)
	if err != nil {
		t.Fatalf("reading shared issue %d back: %v", up.Number, err)
	}
	text := string(raw)
	for _, piece := range []string{"bo@example.test", shape("/home/", "bo/"), shape("ghp_")} {
		if strings.Contains(text, piece) {
			t.Errorf("the stored record kept %q", piece)
		}
	}
	if !strings.Contains(text, redactedValue) {
		t.Errorf("the stored record carries no redaction marker")
	}
	if !strings.Contains(text, "the update failed for") || !strings.Contains(text, "and then it stopped") {
		t.Errorf("the stored record lost the text around it")
	}
	for _, kept := range []string{i.Reporter, i.Run, i.LocalID} {
		if !strings.Contains(text, kept) {
			t.Errorf("the stored record lost %q, which the store keeps (D1722)", kept)
		}
	}
}

// A fingerprint names the object an issue is filed under, and the local record
// it comes from is a file a person can edit, so the store builds no key from a
// value that is not one (F20): blogx takes the key as given, and a fingerprint
// carrying a slash or a dot-dot would reach outside the prefix. Nothing is
// written, so a refused upload costs its caller nothing.
func TestUploadRefusesAValueThatIsNotAFingerprint(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)
	for _, tc := range []struct{ name, fingerprint string }{
		{"empty", ""},
		{"a path", "../../../etc/passwd"},
		{"a nested name", "aaaa/bbbb"},
		{"uppercase", "9F2C4A1B0D3E5F60718293A4B5C6D7E8"},
		{"too short", "9f2c4a1b"},
		{"not hexadecimal", "9f2c4a1b0d3e5f60718293a4b5c6d7gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Upload(ctx, b, sharedIssue("update-20261107183012-48213", tc.fingerprint)); err == nil {
				t.Errorf("uploading %q as a fingerprint was allowed", tc.fingerprint)
			}
		})
	}

	// Nothing of a refused upload is in the store: no number taken, no entry
	// under the fingerprint prefix, and the corrected issue still takes 1.
	if _, err := readVersion(ctx, b, counterKey()); !errors.Is(err, blobx.ErrNotExist) {
		t.Errorf("a refused upload moved the counter: %v", err)
	}
	iter := b.List(&blob.ListOptions{Prefix: sharedDir + "/" + fingerprintsDir + "/"})
	if obj, err := iter.Next(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("a refused upload left %v under the fingerprint prefix: %v", obj, err)
	}
	up, err := Upload(ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("23")))
	if err != nil || up.Number != 1 {
		t.Fatalf("the corrected upload = %+v, %v; want issue 1", up, err)
	}
}

// A record past what a reader takes back would upload and then be in no
// listing, with the fingerprint that names it stranded on it and every later
// report of that bug failing on the read (F24). Upload refuses one before
// anything is written, and keeps in hand the room the claim takes: the claim a
// submitter holds the record under is written into the same object, so a record
// written up to the cap would be unreadable the moment somebody files it.
func TestUploadRefusesARecordTheStoreCouldNotReadBack(t *testing.T) {
	ctx := context.Background()
	b := sharedBucket(t)

	if _, err := Upload(ctx, b, sized(t, "update-20261107183012-48213", fingerprint("41"), sharedRecordMax-sharedClaimHeadroom+1)); err == nil {
		t.Fatal("an upload wrote a record past what a reader takes back")
	} else if !strings.Contains(err.Error(), "more than the shared store reads back") {
		t.Errorf("the refusal = %v, want the size of the record", err)
	}
	// Nothing of it is in the store: the counter has not moved, and there is
	// neither an entry to name it nor a record to read.
	if _, err := readVersion(ctx, b, counterKey()); !errors.Is(err, blobx.ErrNotExist) {
		t.Errorf("a refused upload moved the counter: %v", err)
	}
	if rows, err := ListShared(ctx, b); err != nil || len(rows) != 0 {
		t.Errorf("the listing after a refused upload = %+v, %v; want nothing", rows, err)
	}
	iter := b.List(&blob.ListOptions{Prefix: sharedDir + "/" + fingerprintsDir + "/"})
	if obj, err := iter.Next(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("a refused upload left %v under the fingerprint prefix: %v", obj, err)
	}

	// One within the cap uploads and is still read back once it is claimed, the
	// way a listing reads every issue it holds.
	up := upload(t, ctx, b, sized(t, "update-20261107183014-48215", fingerprint("42"), sharedRecordMax-sharedClaimHeadroom))
	if up.Number != 1 || up.Attached {
		t.Fatalf("the upload at the cap = %+v, want issue 1", up)
	}
	if _, err := Claim(ctx, b, up.Number, "user:ana@example.test", sharedAt); err != nil {
		t.Fatalf("claiming issue %d: %v", up.Number, err)
	}
	rec, _, err := readShared(ctx, b, up.Number)
	if err != nil {
		t.Fatalf("reading a claimed issue written at the cap: %v", err)
	}
	if rec.Issue.Number != up.Number {
		t.Errorf("the claimed record holds issue %d, want %d", rec.Issue.Number, up.Number)
	}
	if rows, err := ListShared(ctx, b); err != nil || len(rows) != 1 || rows[0].Number != up.Number {
		t.Errorf("the listing of a claimed issue at the cap = %+v, %v; want issue %d once", rows, err, up.Number)
	}
}

// The listing walks a prefix any launcher can write (F24) and reads the number
// a record's name spells as an issue, so only the name this store writes is one
// (F20): a stray object beside it is not a second row of the same issue.
func TestRecordKeyMustBeCanonical(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want int
		ok   bool
	}{
		{"issues/records/0001", 1, true},
		{"issues/records/0007", 7, true},
		{"issues/records/00001", 0, false},
		{"issues/records/1", 0, false},
		{"issues/records/0000", 0, false},
		{"issues/records/0001/x", 0, false},
		{"issues/records/", 0, false},
		{"issues/records/abc", 0, false},
		{"issues/records/0001.json", 0, false},
		{"issues/fingerprints/0001", 0, false},
	} {
		got, ok := numberFromRecordKey(tc.key)
		if got != tc.want || ok != tc.ok {
			t.Errorf("numberFromRecordKey(%q) = %d, %v; want %d, %v", tc.key, got, ok, tc.want, tc.ok)
		}
	}

	ctx := context.Background()
	b := sharedBucket(t)
	up := upload(t, ctx, b, sharedIssue("update-20261107183012-48213", fingerprint("24")))
	raw, _, err := b.ReadMax(ctx, recordKey(up.Number), 1<<20)
	if err != nil {
		t.Fatalf("reading shared issue %d back: %v", up.Number, err)
	}
	for _, stray := range []string{"issues/records/00001", "issues/records/1"} {
		if err := b.Put(ctx, stray, raw, sharedJSON); err != nil {
			t.Fatalf("writing %s: %v", stray, err)
		}
	}
	// The name is one this store would write, and the record under it belongs to
	// another issue: issue 2's name holding issue 1's record is not a second row
	// of issue 1.
	if err := b.Put(ctx, recordKey(2), raw, sharedJSON); err != nil {
		t.Fatalf("writing %s: %v", recordKey(2), err)
	}
	rows, err := ListShared(ctx, b)
	if err != nil {
		t.Fatalf("listing the shared store: %v", err)
	}
	if len(rows) != 1 || rows[0].Number != up.Number {
		t.Errorf("the listing = %+v, want issue %d once", rows, up.Number)
	}
}
