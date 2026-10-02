package token

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
	"gocloud.dev/gcerrors"
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
		return fmt.Errorf("budget token: writing the token object failed: %s", scrubErr(err))
	}
	return nil
}

// DeleteObject removes the run's token object (the launcher's cleanup when
// the launch fails after the token was left). A missing object is fine.
func DeleteObject(ctx context.Context, b *blobx.Bucket, slug, run string) error {
	if !validSegment(slug) || !validSegment(run) {
		return errors.New("budget token: invalid slug or run id")
	}
	if err := b.Delete(ctx, ObjectKey(slug, run)); err != nil && !isNotExist(err) {
		return fmt.Errorf("budget token: deleting the token object failed: %s", scrubErr(err))
	}
	return nil
}

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
		return "", fmt.Errorf("budget token: reading the token object failed: %s", scrubErr(err))
	}
	if err := b.DeleteIf(ctx, key, gen, data); err != nil {
		return "", fmt.Errorf("budget token: the token object could not be deleted, so it is not used: %s", scrubErr(err))
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

// scrubErr returns err's text; bucket errors carry object names and status,
// never the object body.
func scrubErr(err error) string { return err.Error() }
