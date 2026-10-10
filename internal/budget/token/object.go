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

// ObjectKey is where a run's custom token waits in the runs bucket.
func ObjectKey(slug, run string) string { return "runs/" + slug + "/" + run + "/budget-token" }

// PutObject leaves tok for the run slug/run, create-if-absent: an object
// already there (an earlier token nobody took) is ErrObjectExists and is not
// overwritten, because the object is how a run finds its own identity and a
// replacement is an identity swap. tok must name that run.
func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string) error {
	if !validSegment(slug) || !validSegment(run) {
		return errors.New("budget token: invalid slug or run id")
	}
	if err := checkNames(tok, slug, run); err != nil {
		return err
	}
	if _, err := b.Create(ctx, ObjectKey(slug, run), []byte(tok), "text/plain"); err != nil {
		if errors.Is(err, blobx.ErrExists) {
			return ErrObjectExists
		}
		return fmt.Errorf("budget token: writing the token object failed: %s", errText(err))
	}
	return nil
}

// DeleteObject removes the run's token object (the launcher's cleanup when
// the launch fails after the token was left). A missing object is fine. A
// refused delete (HTTP 403) must not read as "already gone": gocloud maps a
// GCS 403 to the same gcerrors.NotFound a missing object gets, so the 403
// is detected first and reported as blobx.ErrForbidden, never silently
// treated as success. A token a refused cleanup left behind stays
// replayable (bucket-iam.md §2.3).
func DeleteObject(ctx context.Context, b *blobx.Bucket, slug, run string) error {
	if !validSegment(slug) || !validSegment(run) {
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

// Untaken objects: a token nobody takes is dead after an hour but the object
// stays; the runs bucket needs a lifecycle rule that deletes
// runs/*/*/budget-token objects after a day (T9/T10), and the launcher deletes
// the object when a launch fails (DeleteObject).
//
// TakeObject consumes the token: a second call finds nothing. A caller whose
// Exchange fails must retry Exchange with the token it already holds in
// memory, never call TakeObject again.
//
// TakeObject reads the run's token object and deletes it, then returns the
// token. A delete that fails is fatal: the token is NOT returned, because a
// token left behind could be read by any other run of the repository and
// replayed. The object is deleted before the token is checked, so even a
// token that names another run does not stay readable. A missing object is
// ErrNoToken.
func TakeObject(ctx context.Context, b *blobx.Bucket, slug, run string) (string, error) {
	if !validSegment(slug) || !validSegment(run) {
		return "", errors.New("budget token: invalid slug or run id")
	}
	key := ObjectKey(slug, run)
	data, gen, err := b.Read(ctx, key)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return "", ErrNoToken
	case err != nil:
		return "", fmt.Errorf("budget token: reading the token object failed: %s", errText(err))
	}
	switch err := b.DeleteExisting(ctx, key, gen, data); {
	case errors.Is(err, blobx.ErrNotExist), errors.Is(err, blobx.ErrConflict):
		// Someone else deleted or replaced it between our read and our delete:
		// they took the token, we did not.
		return "", ErrNoToken
	case err != nil:
		return "", fmt.Errorf("budget token: the token object could not be deleted, so it is not used: %s", errText(err))
	}
	tok := string(data)
	if err := checkNames(tok, slug, run); err != nil {
		return "", err
	}
	return tok, nil
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
