package cli

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// writeDenied is errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err),
// shared by operatorWriteErr and checkWriteErr (docs/design/bucket-iam.md
// H8, "errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err)", matched
// verbatim).
//
// isAccessDenied also catches a 401 (expired or missing credentials) and a
// local fs.ErrPermission, bucketing them with a true 403: no pre-flight
// check tells them apart from here, so a 401 or a local file permission
// problem is reported the same as the IAM refusal it almost always is. The
// design's veto alternative, a pre-flight buckets.testIamPermissions call,
// would distinguish them at the cost of one more call and one more
// unverified behaviour (bucket-iam.md §6); H8 accepts the coarser
// classification instead.
func writeDenied(err error) bool {
	return err != nil && (errors.Is(err, blobx.ErrForbidden) || isAccessDenied(err))
}

// operatorWriteErr is the refusal of a write to the runs bucket that only
// admins may make (docs/design/bucket-iam.md H8). The two lists those
// sentences were written for are the same two roles under the old words: the
// role the releases name a launcher reads the whole bucket but writes only
// runs/ and tokens/ (the sealed budget token, since 0.9.0) and issues/ (the shared issue store), and the role they
// name an operator writes it all -- operator and admin now (D1581). A 403 is
// a user error (exit 1) naming the object, the bucket and what to do; any
// other error is returned unchanged, for the caller's own wrapping.
//
// lead opens the message; only its first value is used, and it defaults to
// "nothing was published" when none is given. A caller for which that isn't
// true (something was already written or attempted, as with a fan-out's
// later copies after an earlier one landed) passes what is true instead.
func operatorWriteErr(url, key string, err error, lead ...string) error {
	if !writeDenied(err) {
		return err
	}
	open := "nothing was published"
	if len(lead) > 0 {
		open = lead[0]
	}
	return userErr("%s: writing %s in %s needs the admin role (since 0.7.0 operators read the runs bucket but write only runs/; the reach has since widened to tokens/ and issues/); ask an admin to publish it, or to add you with fugaro update --admin", open, key, url)
}

// checkWriteErr is writeState's refusal of a check.json write the daily
// check job's build service account could not make (docs/design/bucket-iam.md
// H8). The job runs as that account, scoped to builds/<repository>/ by its
// own Terraform IAM condition, not the operator/admin grants
// operatorWriteErr explains: unlike every other operatorWriteErr caller,
// the account refused here is never an operator asking to publish something,
// so operatorWriteErr's "ask an admin to publish it, or to add you with
// fugaro update --admin" would send whoever reads the job's log looking in
// the wrong place. A 403 almost always means that account's grant is
// missing or broken, which only an admin fixes in Terraform. (A person
// who ran `fugaro image check --job` directly, impersonating the job with
// their own credentials, hits the same 403 for a different reason; an
// admin checking the grant first is still the reasonable start there
// too.) Any other error is returned unchanged, for the caller's own
// wrapping; a refused write is a remote failure (exit 2), not a user error:
// whoever is running the job cannot fix a broken Terraform grant itself.
func checkWriteErr(url, key string, err error) error {
	if !writeDenied(err) {
		return err
	}
	return remote(fmt.Errorf("writing %s in %s was refused: the repository's build service account may be missing its builds/ write grant; an admin needs to check this repository's Terraform IAM condition (docs/design/bucket-iam.md): %w", key, url, err))
}
