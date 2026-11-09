package issues

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/redact"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// The shared store is the project's side of an issue: one copy every teammate
// reads and one of them files (F13). It lives under its own prefix, `issues/`
// (D1727), so the grant that reaches it does not widen what a run's own account
// may write.
//
// Four kinds of object, all of them read by every launcher and written by one
// protocol between honest clients (F24):
//
//	issues/counter                    the last number handed out
//	issues/fingerprints/<hash>        the issue a fingerprint is
//	issues/records/<NNNN>             one issue: its record and, while a
//	                                  submitter holds it, the claim on it
//	issues/occurrences/<NNNN>/<name>  one more time that issue happened
//
// None of it is a boundary: a launcher may write the whole prefix and can
// rewrite any of it. What the store does do is condition every write on the
// version it read, so two machines racing produce one number, one claim and one
// filing, and a reader can tell a stale write from a fresh one.
const (
	sharedDir       = "issues"
	recordsDir      = "records"
	fingerprintsDir = "fingerprints"
	occurrencesDir  = "occurrences"
	counterObject   = "counter"
	sharedJSON      = "application/json"
)

// sharedFormat is the version of every object this file writes, the counter and
// the fingerprint entries included. These shapes are read by binaries other
// than this one (a teammate's newer CLI, the console), so a field whose meaning
// must change gets a new name and this number goes up rather than the field
// being reused.
const sharedFormat = 1

// claimLease is how long a claim stands before another submitter may take it
// over (F9): a submitter whose machine dies mid-filing must not block the issue
// for the team. The post `gh` makes runs under a timeout shorter than this, so
// a lease cannot lapse while one is in flight (F24).
const claimLease = 15 * time.Minute

// sharedRetries bounds one read-modify-write loop. Every loop here retries only
// on a lost race against another writer, and four writers losing four times in
// a row is not a race but a store somebody is rewriting.
const sharedRetries = 5

// occurrenceExcerptMax caps the one line of the reporter's text an occurrence
// carries: it goes into a store the whole team reads, and one line is what a
// list shows.
const occurrenceExcerptMax = 200

// sharedRecordMax is the largest encoded record the store writes. Every reader
// caps a read at blobx.MaxReadBytes (F24), so a record past it uploads, names
// its fingerprint, and is then missing from every listing — and every later
// report of that bug fails on the read.
const sharedRecordMax = blobx.MaxReadBytes

// sharedClaimHeadroom is what the store adds to a record after it is written:
// the claim a submitter holds it under, which is a holder name, the time it was
// taken and the time it lapses. A record within this much of the cap would
// upload and then be unreadable the moment somebody claimed it, so the cap the
// write side checks keeps this much in hand.
const sharedClaimHeadroom = 512

// issueURLPrefix is the only shape a stored link may have (F28): the post goes
// to one repository, and the store is writable by every launcher, so a record
// must not be able to send a reader anywhere else.
const issueURLPrefix = "https://github.com/" + updatecheck.Repo + "/issues/"

// beforeConditionalWrite is the seam a test forces an interleaving through: it
// runs after an object is read and before the write conditioned on that read,
// which is the window two machines racing do their damage in. Production leaves
// it nil.
var beforeConditionalWrite func(key string)

// numberDoc is the body of the two objects that carry a bare number: the
// counter (the last number handed out) and a fingerprint's entry (the issue
// that fingerprint is, zero while no upload has taken its number yet).
type numberDoc struct {
	Format int `json:"format"`
	Number int `json:"number"`
}

// sharedRecord is one issue as the store holds it: the record this machine
// wrote, and the claim on it while somebody is filing it. The record keeps its
// own codec and its own version rather than being projected into fields here,
// so a teammate reads the record as it was written — with the report's
// redaction applied to it once more on the way in (F5) and nothing else
// changed.
type sharedRecord struct {
	Format int    `json:"format"`
	Record string `json:"record"`
	Claim  *Lease `json:"claim,omitempty"`
	// Issue is Record decoded: a reader holds the record and its claim together
	// without decoding twice.
	Issue Issue `json:"-"`
}

// setRecord re-encodes Issue into the record's text: the two are one value
// here, and a write that changed the issue without changing its text would
// store the old one.
func (rec *sharedRecord) setRecord() error {
	text, err := Marshal(rec.Issue)
	if err != nil {
		return err
	}
	rec.Record = string(text)
	return nil
}

// Lease is who is filing an issue and until when. It lives inside the record it
// is about, so taking the claim and recording the submission are one versioned
// object rather than two that can disagree (F22).
type Lease struct {
	Holder    string    `json:"holder"`
	ClaimedAt time.Time `json:"claimed_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Occurrence is one more time an issue happened, recorded when an upload finds
// its fingerprint already there (D1724): who hit it, when they hit it, a short
// redacted excerpt, and — when the reporter's version already has the fix — the
// regression flag and the issue it repeats (F25).
type Occurrence struct {
	Reporter    string    `json:"reporter,omitempty"`
	At          time.Time `json:"at"`
	Excerpt     string    `json:"excerpt,omitempty"`
	Regression  bool      `json:"regression,omitempty"`
	OriginalURL string    `json:"original_url,omitempty"`
}

// Uploaded is what an upload did: the number the issue has in the store, and,
// when the fingerprint was already there, whether this report was recorded as
// an occurrence of the issue that is there and whether it is that bug back.
type Uploaded struct {
	Number      int
	Attached    bool
	Regression  bool
	OriginalURL string
}

// SharedRow is one issue the store holds, with how many times it has been hit
// besides the report that created it (F21).
type SharedRow struct {
	Issue
	Occurrences int
}

// ClaimedError is a live claim somebody else holds: the caller files nothing
// and tells the user who has it and until when.
type ClaimedError struct {
	Holder  string
	Expires time.Time
}

func (e *ClaimedError) Error() string {
	who := e.Holder
	if who == "" {
		who = "somebody"
	}
	return fmt.Sprintf("issues: %s is filing this issue until %s", who, e.Expires.UTC().Format(time.RFC3339))
}

// Claimed is a claim this machine holds on one issue, with the record as it was
// when the claim was taken. The preview is built from that record and every
// later write is conditioned on that version, so the bytes filed are the bytes
// previewed even if a teammate rewrites the record in between (F19, F22).
type Claimed struct {
	Number int
	Holder string
	// Issue is the record as the claim was taken.
	Issue Issue

	value   sharedRecord
	version version
}

// version is one read of a shared object: the bytes it held and the generation
// they were at. blobx compares the generation on GCS and the bytes elsewhere,
// so keeping both is what lets one conditional write work on either.
type version struct {
	raw []byte
	gen int64
}

// counterKey is the one counter the store hands numbers from (F9).
func counterKey() string { return sharedDir + "/" + counterObject }

// fingerprintKey is where the issue a fingerprint is gets written.
func fingerprintKey(fingerprint string) string {
	return sharedDir + "/" + fingerprintsDir + "/" + fingerprint
}

// recordKey is one issue's object: its number, printed the way a file name
// prints it, so a prefix listing comes back in number order.
func recordKey(number int) string {
	return fmt.Sprintf("%s/%s/%04d", sharedDir, recordsDir, number)
}

// occurrencePrefix is every occurrence of one issue.
func occurrencePrefix(number int) string {
	return fmt.Sprintf("%s/%s/%04d/", sharedDir, occurrencesDir, number)
}

// occurrenceKey is one occurrence: when it happened and who it happened to, so
// two hits on one issue are two objects and a retried upload is the one that is
// already there (D1724, F21).
func occurrenceKey(number int, occ Occurrence) string {
	return occurrencePrefix(number) + occ.At.UTC().Format("20060102T150405Z") + "-" + Digest(occ.Reporter)[:8]
}

// Upload puts a local issue into the store: it takes the issue's number and
// writes the issue at it, or, when the fingerprint is already there, records
// one more occurrence of the issue that fingerprint names (D1724, F21).
//
// i is the record as the local store has it: no number yet, and not uploaded.
// An error means nothing was written under a number this upload took, so a
// caller may retry the whole call.
func Upload(ctx context.Context, b *blobx.Bucket, i Issue) (Uploaded, error) {
	if err := i.Validate(); err != nil {
		return Uploaded{}, err
	}
	if i.Number != 0 || i.Uploaded {
		return Uploaded{}, fmt.Errorf("issues: %s is already in the shared store", i.LocalID)
	}
	// The store builds a key out of the fingerprint, so the shape is decided
	// here (F20). Validate refuses a malformed one but lets the empty value a
	// record saved without a signature has through, and a key is never built
	// from nothing.
	if !fingerprintRE.MatchString(i.Fingerprint) {
		return Uploaded{}, fmt.Errorf("issues: %q is not a fingerprint", i.Fingerprint)
	}
	// A local record is a file a person can edit (the local store reads what is
	// there), so its links are checked here as well as on the way out: an
	// upload that wrote a link a reader refuses would report success, leave the
	// issue in no listing, and strand the fingerprint on a record nobody can
	// read. Nothing is written before this answers, so a later upload of the
	// corrected issue still takes its number.
	if err := checkLinks(i); err != nil {
		return Uploaded{}, fmt.Errorf("issues: %w", err)
	}
	// The other way a record nobody can read would be written: past what a
	// reader's cap takes back, it is in no listing, and the fingerprint that
	// names it is stranded. Nothing is written before this answers either.
	if err := checkRecordSize(i); err != nil {
		return Uploaded{}, err
	}
	key := fingerprintKey(i.Fingerprint)
	entry, err := json.Marshal(numberDoc{Format: sharedFormat})
	if err != nil {
		return Uploaded{}, fmt.Errorf("issues: %w", err)
	}
	// The fingerprint's entry is created before any number is taken (F21): the
	// create is what makes exactly one upload of a fingerprint the one that
	// numbers it, however many machines run at once.
	gen, err := b.Create(ctx, key, entry, sharedJSON)
	ours := err == nil
	if err != nil && !errors.Is(err, blobx.ErrExists) {
		return Uploaded{}, fmt.Errorf("issues: creating the fingerprint index: %w", err)
	}
	v := version{raw: entry, gen: gen}

	for attempt := 0; attempt < sharedRetries; attempt++ {
		if !ours {
			// Somebody has this fingerprint. The entry is the truth about it: a
			// number means the issue is there, no number means an upload that
			// is still taking one or one that died before it did (F28).
			next, err := readVersion(ctx, b, key)
			if errors.Is(err, blobx.ErrNotExist) {
				// The entry went away between the create and this read — a
				// launcher's delete (F24). Take it again rather than guess at
				// what it held.
				v = version{raw: entry}
				if v.gen, err = b.Create(ctx, key, entry, sharedJSON); err == nil {
					ours = true
				} else if !errors.Is(err, blobx.ErrExists) {
					return Uploaded{}, fmt.Errorf("issues: creating the fingerprint index: %w", err)
				}
				continue
			}
			if err != nil {
				return Uploaded{}, fmt.Errorf("issues: reading the fingerprint index: %w", err)
			}
			doc, err := decodeNumber(next.raw, key)
			if err != nil {
				return Uploaded{}, err
			}
			if doc.Number != 0 {
				return attach(ctx, b, doc.Number, i)
			}
			v, ours = next, true
		}

		number, err := takeNumber(ctx, b)
		if err != nil {
			return Uploaded{}, err
		}
		rec, err := marshalShared(i, number)
		if err != nil {
			return Uploaded{}, err
		}
		recGen, err := b.Create(ctx, recordKey(number), rec, sharedJSON)
		if errors.Is(err, blobx.ErrExists) {
			// The counter handed out a number an issue already has: nothing but
			// a rewritten counter does that (F24), and overwriting the issue
			// that is there is the wrong answer to it.
			return Uploaded{}, fmt.Errorf("issues: the shared store already has an issue %d", number)
		}
		if err != nil {
			return Uploaded{}, fmt.Errorf("issues: writing the shared issue: %w", err)
		}
		named, err := json.Marshal(numberDoc{Format: sharedFormat, Number: number})
		if err != nil {
			return Uploaded{}, fmt.Errorf("issues: %w", err)
		}
		if _, err := replaceRaw(ctx, b, key, named, v); err == nil {
			return Uploaded{Number: number}, nil
		} else if !errors.Is(err, blobx.ErrConflict) {
			// The naming failed for a reason of its own, and a write that
			// committed before its answer was lost looks here exactly like one
			// that never landed: the entry is the truth about a fingerprint, so
			// reading it back is what tells the two apart. It names this number
			// — the upload is done, and only its answer went missing. It names
			// nothing or somebody else — the issue this attempt wrote is an
			// orphan, and dropping it is what keeps the retry a caller makes
			// from leaving a second issue of one fingerprint behind. A read that
			// fails leaves the record alone: debris a later upload meets is
			// recoverable, a fingerprint the entry names with no issue under it
			// is not.
			holds, rerr := entryNames(ctx, b, key, number)
			if rerr == nil && holds {
				return Uploaded{Number: number}, nil
			}
			if rerr == nil {
				_ = b.DeleteIf(ctx, recordKey(number), recGen, rec)
			}
			return Uploaded{}, fmt.Errorf("issues: naming the fingerprint: %w", err)
		}
		// Somebody named the fingerprint between this read and this write — or
		// this write landed and the client, retrying a response it never got,
		// met the entry it had just written and answered a conflict: the entry
		// is the truth about a fingerprint, so it decides which. It names this
		// number and the naming landed; it names anything else, and the issue
		// just written at this upload's own number is an orphan to drop before
		// following theirs.
		holds, rerr := entryNames(ctx, b, key, number)
		if rerr != nil {
			return Uploaded{}, fmt.Errorf("issues: reading the fingerprint index: %w", rerr)
		}
		if !holds {
			if err := b.DeleteIf(ctx, recordKey(number), recGen, rec); err != nil && !errors.Is(err, blobx.ErrConflict) {
				return Uploaded{}, fmt.Errorf("issues: dropping the orphaned issue %d: %w", number, err)
			}
		}
		ours = false
	}
	return Uploaded{}, fmt.Errorf("issues: the fingerprint index for %s kept changing; giving up", i.Fingerprint)
}

// attach records this upload as one more occurrence of the issue the fingerprint
// names, or recognises an upload this very issue already had.
func attach(ctx context.Context, b *blobx.Bucket, number int, i Issue) (Uploaded, error) {
	rec, _, err := readShared(ctx, b, number)
	if err != nil {
		return Uploaded{}, err
	}
	if rec.Issue.Run == i.Run {
		// This issue is already there: a caller that repeated an upload whose
		// answer was lost, or a client that retried a create that committed.
		// One issue uploaded once is not an occurrence of itself.
		return Uploaded{Number: number}, nil
	}
	occ := occurrenceOf(rec.Issue, i)
	if err := AttachOccurrence(ctx, b, number, occ); err != nil {
		return Uploaded{}, err
	}
	return Uploaded{Number: number, Attached: true, Regression: occ.Regression, OriginalURL: occ.OriginalURL}, nil
}

// occurrenceOf is the occurrence this upload is, of an issue that is already
// there: who hit it, when — the time the reporter's record was created, which
// is when the bug happened rather than when it was uploaded — one line of their
// text, and, when the version they run already has the fix, the regression flag
// and the link to the issue it repeats (F25, D1736).
func occurrenceOf(rec, i Issue) Occurrence {
	occ := Occurrence{Reporter: i.Reporter, At: i.Created.UTC(), Excerpt: excerptOf(i)}
	// Flagged and linked, or neither: a regression with nothing to link to is a
	// hit nobody can follow up, and only a filed issue has a URL.
	if rec.URL != "" && updatecheck.AtOrBelow(rec.ResolvedIn, i.FugaroVersion) {
		occ.Regression, occ.OriginalURL = true, rec.URL
	}
	return occ
}

// excerptOf is the one line of the reporter's text an occurrence carries: the
// first line of the summary, redacted and then cut to a length a list can show.
// The pass runs before the cut, not after it: a credential the cut leaves
// shorter than the pattern that finds it would survive into the object the
// whole team reads (F5).
func excerptOf(i Issue) string {
	line, _, _ := strings.Cut(i.Summary, "\n")
	line = redact.Report(line)
	if r := []rune(line); len(r) > occurrenceExcerptMax {
		line = string(r[:occurrenceExcerptMax])
	}
	return line
}

// takeNumber takes the next number for the project (F9). The counter is read
// and written back conditioned on the version that read gave, so two machines
// racing take two numbers and both issues land; without that condition both
// read the same value, both write the same number, and the second issue is
// written at a number the first one already has.
func takeNumber(ctx context.Context, b *blobx.Bucket) (int, error) {
	key := counterKey()
	for attempt := 0; attempt < sharedRetries; attempt++ {
		v, err := readVersion(ctx, b, key)
		first := errors.Is(err, blobx.ErrNotExist)
		if err != nil && !first {
			return 0, fmt.Errorf("issues: reading the shared counter: %w", err)
		}
		next := 1
		if !first {
			doc, err := decodeNumber(v.raw, key)
			if err != nil {
				return 0, err
			}
			next = doc.Number + 1
		}
		if err := validateNumber(next); err != nil {
			return 0, fmt.Errorf("issues: %w", err)
		}
		raw, err := json.Marshal(numberDoc{Format: sharedFormat, Number: next})
		if err != nil {
			return 0, fmt.Errorf("issues: %w", err)
		}
		if first {
			// The counter was read as absent, so this attempt is a create; the
			// seam is the same window as a conditional write's. A create the
			// store says is taken is another machine's number, taken first.
			if beforeConditionalWrite != nil {
				beforeConditionalWrite(key)
			}
			if _, err := b.Create(ctx, key, raw, sharedJSON); errors.Is(err, blobx.ErrExists) {
				continue
			} else if err != nil {
				return 0, fmt.Errorf("issues: taking a number: %w", err)
			}
			return next, nil
		}
		if _, err := replaceRaw(ctx, b, key, raw, v); errors.Is(err, blobx.ErrConflict) {
			continue
		} else if err != nil {
			return 0, fmt.Errorf("issues: taking a number: %w", err)
		}
		return next, nil
	}
	return 0, errors.New("issues: the shared counter kept changing; giving up")
}

// readShared reads one issue's object, bounded, strictly decoded, and refused
// when it links anywhere but to an issue of this project (F28).
func readShared(ctx context.Context, b *blobx.Bucket, number int) (sharedRecord, version, error) {
	if err := validateNumber(number); err != nil {
		return sharedRecord{}, version{}, err
	}
	v, err := readVersion(ctx, b, recordKey(number))
	if errors.Is(err, blobx.ErrNotExist) {
		return sharedRecord{}, version{}, fmt.Errorf("issues: the shared store has no issue %d", number)
	}
	if err != nil {
		return sharedRecord{}, version{}, fmt.Errorf("issues: reading shared issue %d: %w", number, err)
	}
	rec, err := decodeShared(v.raw, number)
	if err != nil {
		return sharedRecord{}, version{}, err
	}
	return rec, v, nil
}

// decodeShared decodes one record, refusing anything outside the format.
func decodeShared(raw []byte, number int) (sharedRecord, error) {
	var rec sharedRecord
	if err := decodeStrict(raw, &rec); err != nil {
		return sharedRecord{}, fmt.Errorf("issues: shared issue %d: %w", number, err)
	}
	if rec.Format != sharedFormat {
		return sharedRecord{}, fmt.Errorf("issues: shared issue %d is format %d, and this fugaro reads %d", number, rec.Format, sharedFormat)
	}
	i, err := Unmarshal([]byte(rec.Record))
	if err != nil {
		return sharedRecord{}, fmt.Errorf("issues: shared issue %d: %w", number, err)
	}
	if i.Number != number {
		return sharedRecord{}, fmt.Errorf("issues: shared issue %d holds the record of issue %d", number, i.Number)
	}
	if err := checkLinks(i); err != nil {
		return sharedRecord{}, fmt.Errorf("issues: shared issue %d: %w", number, err)
	}
	rec.Issue = i
	return rec, nil
}

// checkLinks refuses a record whose links are not issues of this project, in
// the words the caller prefixes with the record it is about. The check belongs
// on the way in as much as on the way out (F28): a record a write accepts must
// be one every reader can read, or an upload reports success for a record the
// store will not hand back.
func checkLinks(i Issue) error {
	for _, link := range []string{i.URL, i.OriginalURL} {
		if link != "" && !projectIssueURL(link) {
			return fmt.Errorf("%q is not an issue URL of %s", link, updatecheck.Repo)
		}
	}
	return nil
}

// projectIssueURL reports whether s names an issue of this project: the only
// shape a stored link may have (F28).
func projectIssueURL(s string) bool {
	rest, ok := strings.CutPrefix(s, issueURLPrefix)
	if !ok || !digits(rest) {
		return false
	}
	number, err := strconv.Atoi(rest)
	return err == nil && validateNumber(number) == nil
}

// decodeNumber reads one of the objects that carry a bare number.
func decodeNumber(raw []byte, key string) (numberDoc, error) {
	var doc numberDoc
	if err := decodeStrict(raw, &doc); err != nil {
		return numberDoc{}, fmt.Errorf("issues: %s: %w", key, err)
	}
	if doc.Format != sharedFormat || doc.Number < 0 || (doc.Number > 0 && validateNumber(doc.Number) != nil) {
		return numberDoc{}, fmt.Errorf("issues: %s is not a number this store wrote", key)
	}
	return doc, nil
}

// entryNames reports whether a fingerprint's entry names a number. The entry is
// the truth about a fingerprint (F21), so it decides what a write that failed
// on its way back left behind.
func entryNames(ctx context.Context, b *blobx.Bucket, key string, number int) (bool, error) {
	v, err := readVersion(ctx, b, key)
	if err != nil {
		return false, err
	}
	doc, err := decodeNumber(v.raw, key)
	if err != nil {
		return false, err
	}
	return doc.Number == number, nil
}

// decodeStrict decodes one JSON object, refusing a field this build does not
// know and a second value after it, the way every wire shape here is read: a
// reader outside this binary may have written it.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("the object holds more than one value")
	}
	return nil
}

// readVersion reads one object and the version a later write about it has to
// match. Records are read with ReadMax (F24) at the package's own bound: the
// prefix is writable by every launcher, so a read here is never unbounded, and
// a record is a few KB of a report and well inside it.
func readVersion(ctx context.Context, b *blobx.Bucket, key string) (version, error) {
	raw, gen, err := b.ReadMax(ctx, key, blobx.MaxReadBytes)
	if err != nil {
		return version{}, err
	}
	return version{raw: raw, gen: gen}, nil
}

// replaceRaw writes raw at key conditioned on v, and answers the version the
// write left. A lost race is blobx.ErrConflict, which every caller here retries
// or reports.
func replaceRaw(ctx context.Context, b *blobx.Bucket, key string, raw []byte, v version) (version, error) {
	if beforeConditionalWrite != nil {
		beforeConditionalWrite(key)
	}
	gen, err := b.ReplaceIfType(ctx, key, raw, sharedJSON, v.gen, v.raw)
	if err != nil {
		return version{}, err
	}
	return version{raw: raw, gen: gen}, nil
}

// checkRecordSize refuses a record the store could not hand back (F24). The
// number it will be written under only widens the encoding, so the check runs
// on the widest one it could take, before the fingerprint entry or the counter
// is touched.
func checkRecordSize(i Issue) error {
	raw, err := marshalShared(i, numberMax)
	if err != nil {
		return err
	}
	if len(raw)+sharedClaimHeadroom > sharedRecordMax {
		return fmt.Errorf("issues: %s is %d bytes encoded, more than the shared store reads back (%d)",
			i.LocalID, len(raw), sharedRecordMax-sharedClaimHeadroom)
	}
	return nil
}

// marshalShared is the object the store holds for issue i at number: the record
// with that number in it — the number is the store's, and the copy the team
// reads says which issue it is — written out by the record's own codec, with
// the report's redaction run again over the text a person wrote. The local
// record is a file a person can edit and the store is read by the whole team,
// so a credential that reached the local copy must not be published with it
// (F5); the header's identity — reporter, run, local ID — stays, because the
// store is where the whole issue lives and only the post to GitHub strips it
// (D1722).
func marshalShared(i Issue, number int) ([]byte, error) {
	i.Number, i.Uploaded = number, true
	i.FailedCommand = redact.Report(i.FailedCommand)
	i.Summary, i.Diagnosis, i.Steps = redact.Report(i.Summary), redact.Report(i.Diagnosis), redact.Report(i.Steps)
	i.Logs = slices.Clone(i.Logs)
	for n := range i.Logs {
		i.Logs[n] = redact.Report(i.Logs[n])
	}
	text, err := Marshal(i)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sharedRecord{Format: sharedFormat, Record: string(text)})
}

// ListShared reads every issue the store holds, in number order, each with how
// many times it has been hit besides the report that created it (F21). A record
// that cannot be read is left out rather than failing the whole read: every
// launcher can write the prefix (F24), and one record this build cannot read
// must not take the team's list down with it — the same reason the local
// store's List leaves one alone.
func ListShared(ctx context.Context, b *blobx.Bucket) ([]SharedRow, error) {
	prefix := sharedDir + "/" + recordsDir + "/"
	iter := b.List(&blob.ListOptions{Prefix: prefix})
	var out []SharedRow
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("issues: listing the shared store: %w", err)
		}
		if obj.IsDir {
			continue
		}
		number, ok := numberFromRecordKey(obj.Key)
		if !ok {
			continue
		}
		rec, _, err := readShared(ctx, b, number)
		if err != nil {
			continue
		}
		n, err := countOccurrences(ctx, b, number)
		if err != nil {
			return nil, err
		}
		out = append(out, SharedRow{Issue: rec.Issue, Occurrences: n})
	}
	sort.Slice(out, func(a, c int) bool { return out[a].Number < out[c].Number })
	return out, nil
}

// numberFromRecordKey reads the number out of a record's key, and refuses a
// name this store did not write (F20): the listing is over a prefix any
// launcher can put an object under (F24), and the number it hands back is read
// as an issue.
func numberFromRecordKey(key string) (int, bool) {
	rest, ok := strings.CutPrefix(key, sharedDir+"/"+recordsDir+"/")
	if !ok || !digits(rest) {
		return 0, false
	}
	number, err := strconv.Atoi(rest)
	if err != nil || validateNumber(number) != nil || key != recordKey(number) {
		return 0, false
	}
	return number, true
}

// countOccurrences counts the times an issue was hit after the report that
// created it (F21). A count needs the names only, and no occurrence is read.
func countOccurrences(ctx context.Context, b *blobx.Bucket, number int) (int, error) {
	iter := b.List(&blob.ListOptions{Prefix: occurrencePrefix(number)})
	n := 0
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("issues: listing the occurrences of issue %d: %w", number, err)
		}
		if !obj.IsDir {
			n++
		}
	}
	return n, nil
}

// Claim takes the claim on one issue for holder: the one submitter that may
// file it (F9, F22). A claim another holder has taken and not let lapse is
// refused with ClaimedError; one that has lapsed is taken over the same way,
// conditioned on the version it was read at. The issue itself is refused once
// it has been filed.
func Claim(ctx context.Context, b *blobx.Bucket, number int, holder string, now time.Time) (*Claimed, error) {
	if err := validateNumber(number); err != nil {
		return nil, err
	}
	if holder == "" {
		return nil, errors.New("issues: a claim names who holds it")
	}
	for attempt := 0; attempt < sharedRetries; attempt++ {
		rec, v, err := readShared(ctx, b, number)
		if err != nil {
			return nil, err
		}
		if rec.Issue.Status == StatusSubmitted {
			return nil, fmt.Errorf("issues: shared issue %d was filed and is final", number)
		}
		if c := rec.Claim; c != nil && c.Holder != holder && now.Before(c.ExpiresAt) {
			return nil, &ClaimedError{Holder: c.Holder, Expires: c.ExpiresAt}
		}
		now = now.UTC()
		rec.Claim = &Lease{Holder: holder, ClaimedAt: now, ExpiresAt: now.Add(claimLease)}
		raw, err := json.Marshal(rec)
		if err != nil {
			return nil, fmt.Errorf("issues: %w", err)
		}
		nv, err := replaceRaw(ctx, b, recordKey(number), raw, v)
		if errors.Is(err, blobx.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("issues: claiming shared issue %d: %w", number, err)
		}
		return &Claimed{Number: number, Holder: holder, Issue: rec.Issue, value: rec, version: nv}, nil
	}
	return nil, fmt.Errorf("issues: shared issue %d kept changing; giving up", number)
}

// Release gives the claim back, for a submitter whose post failed: the record
// is written with the claim cleared, conditioned on the version the claim was
// taken at, so a claim that was taken over is not released by the submitter it
// was taken from (F22).
func Release(ctx context.Context, b *blobx.Bucket, c *Claimed) error {
	return c.finish(ctx, b, func(*sharedRecord) error { return nil })
}

// RecordSubmitted files the issue: the URL and `submitted` are written with the
// claim cleared, in the one write conditioned on still holding it (F22). A
// record already filed is final (F9): the issue is on GitHub, and the store is
// not told otherwise.
func RecordSubmitted(ctx context.Context, b *blobx.Bucket, c *Claimed, url string) error {
	if !projectIssueURL(url) {
		return fmt.Errorf("issues: %q is not an issue URL of %s", url, updatecheck.Repo)
	}
	return c.finish(ctx, b, func(rec *sharedRecord) error {
		if rec.Issue.Status == StatusSubmitted {
			return fmt.Errorf("issues: shared issue %d was filed and is final", c.Number)
		}
		rec.Issue.Status, rec.Issue.URL, rec.Issue.Uploaded = StatusSubmitted, url, true
		return rec.setRecord()
	})
}

// finish writes the record the claim was taken on, with the claim cleared, and
// reports a version somebody else changed in between as itself: the submitter
// that previewed a record is the only one whose write lands (F19). A claim that
// lost that race is spent either way — the caller claims and previews again
// rather than reuse a preview of a version that is gone (T-IS12).
func (c *Claimed) finish(ctx context.Context, b *blobx.Bucket, change func(*sharedRecord) error) error {
	if c.value.Claim == nil || c.value.Claim.Holder != c.Holder {
		return fmt.Errorf("issues: shared issue %d is not held by %q", c.Number, c.Holder)
	}
	if err := change(&c.value); err != nil {
		return err
	}
	c.value.Claim = nil
	raw, err := json.Marshal(c.value)
	if err != nil {
		return fmt.Errorf("issues: %w", err)
	}
	if _, err := replaceRaw(ctx, b, recordKey(c.Number), raw, c.version); err != nil {
		return fmt.Errorf("issues: writing shared issue %d: %w", c.Number, err)
	}
	return nil
}

// SetSharedStatus sets an uploaded issue aside where the team reads it. A
// dismissal is the only status the store is told from outside the claim: a
// submission goes through RecordSubmitted, which holds the claim, and the store
// never reopens an issue — one that was set aside or filed stays that way and a
// later hit is an occurrence (F21).
func SetSharedStatus(ctx context.Context, b *blobx.Bucket, number int, status Status) error {
	if status != StatusDismissed {
		return fmt.Errorf("issues: the shared store sets a dismissal, not %q", status)
	}
	for attempt := 0; attempt < sharedRetries; attempt++ {
		rec, v, err := readShared(ctx, b, number)
		if err != nil {
			return err
		}
		if rec.Issue.Status == status {
			return nil
		}
		if rec.Issue.Status == StatusSubmitted {
			return fmt.Errorf("issues: shared issue %d was filed and is final", number)
		}
		rec.Issue.Status = status
		if err := rec.setRecord(); err != nil {
			return err
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("issues: %w", err)
		}
		if _, err := replaceRaw(ctx, b, recordKey(number), raw, v); errors.Is(err, blobx.ErrConflict) {
			continue
		} else if err != nil {
			return fmt.Errorf("issues: writing shared issue %d: %w", number, err)
		}
		return nil
	}
	return fmt.Errorf("issues: shared issue %d kept changing; giving up", number)
}

// AttachOccurrence records one more time an issue happened, under the issue's
// number, and changes nothing else: an issue that was dismissed or filed stays
// that way and the occurrence is simply there, which is what keeps a bug that
// came back visible instead of buried (F21). The occurrence is created rather
// than written, so a retried upload records the same hit once.
func AttachOccurrence(ctx context.Context, b *blobx.Bucket, number int, occ Occurrence) error {
	if err := validateNumber(number); err != nil {
		return err
	}
	if occ.At.IsZero() {
		return errors.New("issues: an occurrence names the time it happened")
	}
	if occ.OriginalURL != "" && !projectIssueURL(occ.OriginalURL) {
		return fmt.Errorf("issues: %q is not an issue URL of %s", occ.OriginalURL, updatecheck.Repo)
	}
	if occ.Regression && occ.OriginalURL == "" {
		return errors.New("issues: a regression names the issue it repeats")
	}
	// The occurrence carries a teammate's text, so what goes into the store has
	// been through the redaction a report gets before it is written (F5).
	occ.Excerpt = redact.Report(occ.Excerpt)
	raw, err := json.Marshal(occ)
	if err != nil {
		return fmt.Errorf("issues: %w", err)
	}
	if _, err := b.Create(ctx, occurrenceKey(number, occ), raw, sharedJSON); err != nil && !errors.Is(err, blobx.ErrExists) {
		return fmt.Errorf("issues: recording an occurrence of issue %d: %w", number, err)
	}
	return nil
}
