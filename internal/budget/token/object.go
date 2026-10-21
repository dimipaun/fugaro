package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dimipaun/fugaro/internal/blobx"
	"gocloud.dev/gcerrors"
	"google.golang.org/api/googleapi"
)

// ObjectPrefix is the runs bucket prefix the token objects live under, out of
// runs/ (docs/design/budget-token-channel.md §7.2, D16): runs/ is where a
// run's task, launch record and logs live, and a job account may write there,
// while tokens/ is read-only for it, so a peer run cannot delete a queued
// run's token. The Terraform modules grant on exactly this prefix — the job's
// read-only condition (internal/backend/gcp's TokenBucketCondition) and the
// launchers' write (LauncherBucketCondition) — and the bucket's lifecycle rule
// reaps under it; it is one constant so the name a launcher writes, the name a
// run reads and the grants cannot disagree.
const ObjectPrefix = "tokens"

// ObjectKey is where a run's sealed custom token waits in the runs bucket:
// tokens/<slug>/<run>/budget-token. The name is also the envelope's additional
// authenticated data (aad), so an object sealed under the old runs/ name does
// not open under this one: the formats are not interchangeable across the
// move, which is the property and not a migration problem (a queued token is
// short-lived, and a relaunch re-seals).
func ObjectKey(slug, run string) string {
	return ObjectPrefix + "/" + slug + "/" + run + "/budget-token"
}

// PutObject leaves tok for the run slug/run, sealed under key,
// create-if-absent: an object already there (an earlier token nobody took) is
// ErrObjectExists and is not overwritten, because the object is how a run
// finds its own identity and a replacement is an identity swap. tok must name
// that run.
//
// key must be a seal key (KeySize bytes, NewKey): the object is sealed (Seal,
// with ObjectKey as the additional authenticated data) and the caller hands
// the same key to the one execution the token is for (D15). There is no
// plaintext form: a key that is not one is ErrSealKey and nothing is written,
// because a plaintext object in the bucket is the exposure the seal closes
// (§7.4), and a reader of tokens/ must never find one written by Fugaro.
func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {
	if !ValidSegment(slug) || !ValidSegment(run) {
		return errors.New("budget token: invalid slug or run id")
	}
	if len(key) != KeySize {
		return fmt.Errorf("%w: the launcher seals every token object, and a key of %d bytes is not a seal key, so nothing was written", ErrSealKey, len(key))
	}
	if err := checkNames(tok, slug, run); err != nil {
		return err
	}
	name := ObjectKey(slug, run)
	sealed, err := Seal(key, name, []byte(tok))
	if err != nil {
		return err
	}
	if _, err := b.Create(ctx, name, sealed, "application/octet-stream"); err != nil {
		if errors.Is(err, blobx.ErrExists) {
			return ErrObjectExists
		}
		return fmt.Errorf("budget token: writing the token object failed: %s", errText(err))
	}
	return nil
}

// DeleteObject removes the run's token object (the launcher's cleanup when
// the launch fails after the token was left, and its replacement of an
// earlier attempt's object on a retry). A missing object is fine. A refused
// delete (HTTP 403) must not read as "already gone": gocloud maps a GCS 403 to
// the same gcerrors.NotFound a missing object gets, so the 403 is detected
// first and reported as blobx.ErrForbidden, never silently treated as
// success. A token a refused cleanup left behind stays replayable by a holder
// of its key (bucket-iam.md §2.3).
func DeleteObject(ctx context.Context, b *blobx.Bucket, slug, run string) error {
	if !ValidSegment(slug) || !ValidSegment(run) {
		return errors.New("budget token: invalid slug or run id")
	}
	err := b.Delete(ctx, ObjectKey(slug, run))
	switch {
	case err == nil:
		return nil
	case isForbidden(err):
		return fmt.Errorf("budget token: deleting the token object failed: %w: %w", blobx.ErrForbidden, err)
	case isNotExist(err):
		return nil
	default:
		return fmt.Errorf("budget token: deleting the token object failed: %s", errText(err))
	}
}

// ObjectPresent reports whether the run's token object is in the bucket,
// without reading it as a token: what a run whose job carries no budget
// backend needs to know (a launcher minted for it), and nothing more.
//
// The read is strict (ReadStrict), so a 403 is never reported as absence:
// gocloud folds a GCS 403 into the same gcerrors.NotFound a missing object
// gets, and this function returns blobx.ErrForbidden for it instead, leaving
// the caller to decide what a 403 means for its question. For the job account
// it means "missing, or not readable" and nothing narrower: its grant on
// tokens/ is a conditioned objectViewer with no storage.objects.list, so GCS
// answers its read of a missing object with 403, and a job without the grant
// gets 403 whether the object is there or not (docs/design/bucket-iam.md
// §2.3). The runner's refuseStrayToken says what it makes of that.
//
// It reads the object's bytes rather than asking a driver for its metadata
// (Exists): no driver answers "exists" with a refusal, and the object is a
// sealed token of tens of bytes.
func ObjectPresent(ctx context.Context, b *blobx.Bucket, slug, run string) (bool, error) {
	if !ValidSegment(slug) || !ValidSegment(run) {
		return false, errors.New("budget token: invalid slug or run id")
	}
	_, _, err := b.ReadStrict(ctx, ObjectKey(slug, run))
	switch {
	case err == nil:
		return true, nil
	case isForbidden(err) || errors.Is(err, blobx.ErrForbidden):
		return false, fmt.Errorf("budget token: checking for the token object failed: %w: %w", blobx.ErrForbidden, err)
	case errors.Is(err, blobx.ErrNotExist) || isNotExist(err):
		// The read's own name for a missing object (blobx.read folds a
		// gcerrors.NotFound to it), and the same code DeleteObject asks for.
		return false, nil
	default:
		return false, fmt.Errorf("budget token: checking for the token object failed: %s", errText(err))
	}
}

// Untaken objects: a token nobody takes is dead after an hour but the object
// stays; the runs bucket's lifecycle rule deletes tokens/*/*/budget-token
// objects after a day, and the launcher deletes the object when a launch
// fails (DeleteObject) or is retried.
//
// TakeObject reads the run's token object, unseals it with key and returns
// the token. It never deletes the object: the job account's grant on tokens/
// is read-only (D16), so a delete is a request the run already knows is
// denied, and gocloud reports that denial as NotFound — the same error an
// object already gone gets — so no path may infer anything from a delete's
// error (§7.2). Consume-once rests on the key, which the launcher delivers
// once, to one execution, in the override of the launch that won the claim;
// the object's stay in the bucket gives no second delivery of the key. A
// caller whose Exchange fails retries Exchange with the token it already
// holds in memory.
//
// Its refusals are §7.4's, and none of them is a fallback to the bytes:
//
//   - a missing object is ErrNoToken (the caller says what that means);
//   - with a key: a plaintext object, an unknown envelope version, a short or
//     tampered envelope, a wrong key and a wrong name are refusals (Unseal);
//   - with no key: every object is refused, a sealed one as ErrSealedNoKey (the
//     theft case: reading its bytes would hand a peer's stolen copy straight
//     back) and a plaintext one as ErrNotSealed (nothing Fugaro writes is
//     plaintext, so a plaintext object is someone else's write);
//   - a token whose own fs/fr/uid are not this run's is ErrTokenMismatch.
func TakeObject(ctx context.Context, b *blobx.Bucket, slug, run string, key []byte) (string, error) {
	if !ValidSegment(slug) || !ValidSegment(run) {
		return "", errors.New("budget token: invalid slug or run id")
	}
	name := ObjectKey(slug, run)
	data, _, err := b.Read(ctx, name)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return "", ErrNoToken
	case err != nil:
		return "", fmt.Errorf("budget token: reading the token object failed: %s", errText(err))
	}
	tok, err := openToken(key, name, data)
	if err != nil {
		return "", err
	}
	if err := checkNames(tok, slug, run); err != nil {
		return "", err
	}
	return tok, nil
}

// openToken turns the object's bytes into the token they carry, or refuses
// them. It is the one place the bytes are decided, and a plaintext object is
// never a token.
func openToken(key []byte, name string, data []byte) (string, error) {
	switch {
	case len(key) > 0:
		plain, err := Unseal(key, name, data)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	case Sealed(data):
		return "", fmt.Errorf("%w: the object at %s is sealed and this execution presented none, so it is not read as a token: a plaintext fallback is the exposure the seal exists to close", ErrSealedNoKey, name)
	default:
		return "", fmt.Errorf("%w: the object at %s is not sealed and this execution presented no key: Fugaro writes no plaintext token, so it is not read as one", ErrNotSealed, name)
	}
}

// checkNames requires tok to be a custom token for exactly slug/run.
func checkNames(tok, slug, run string) error {
	p, err := parseJWT(tok)
	if err != nil {
		return err
	}
	extra, _ := p["claims"].(map[string]any)
	if claimString(p, "uid") != UID(slug, run) || claimString(extra, "fs") != slug || claimString(extra, "fr") != run {
		return ErrTokenMismatch
	}
	return nil
}

func isNotExist(err error) bool { return gcerrors.Code(err) == gcerrors.NotFound }

// isForbidden reports an HTTP 403 from GCS. gocloud maps a 403 to
// gcerrors.NotFound on reads and writes alike, so this must be checked
// before isNotExist (blobx.isForbidden does the same, for the same reason).
func isForbidden(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusForbidden
}

// errText returns err's text. Bucket errors carry the object name and a
// status, never the object body, so the text is safe to include.
func errText(err error) string { return err.Error() }
