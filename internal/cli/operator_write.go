package cli

import (
	"errors"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// operatorWriteErr is the refusal of a write to the runs bucket that only
// operators may make (docs/design/bucket-iam.md H8): since 0.7.0 launchers
// read the whole bucket but write only runs/. A 403 is a user error (exit 1)
// naming the object, the bucket and what to do; any other error is returned
// unchanged, for the caller's own wrapping.
func operatorWriteErr(url, key string, err error) error {
	if err == nil || !(errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)) {
		return err
	}
	return userErr("nothing was published: writing %s in %s needs the operator role (since 0.7.0 launchers read the runs bucket but write only runs/); ask an operator to publish it, or to add you with fugaro init --operator", key, url)
}
