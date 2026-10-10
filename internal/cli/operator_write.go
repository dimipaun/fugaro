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
//
// lead opens the message; only its first value is used, and it defaults to
// "nothing was published" when none is given. A caller for which that isn't
// true (something was already written or attempted, as with a build that
// ran but whose check.json could not be recorded, or a fan-out's later
// copies after an earlier one landed) passes what is true instead.
func operatorWriteErr(url, key string, err error, lead ...string) error {
	if err == nil || !(errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)) {
		return err
	}
	open := "nothing was published"
	if len(lead) > 0 {
		open = lead[0]
	}
	return userErr("%s: writing %s in %s needs the operator role (since 0.7.0 launchers read the runs bucket but write only runs/); ask an operator to publish it, or to add you with fugaro init --operator", open, key, url)
}
