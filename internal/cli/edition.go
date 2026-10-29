package cli

import "errors"

// The edition of this binary: core unless ee/register's init called
// SetEditionEE. It is deliberately not a package-level string. `-ldflags -X`
// sets a package-level string variable and nothing else, so an edition kept in
// one could be raised to ee by a build flag — silencing the warning a core job
// image is supposed to get — or, worse, lowered to core on a tagged build,
// making the binary with the enterprise code in it deny having it (design
// docs/design/ee-boundary-moves.md §7).
const (
	// EditionCore is the edition of every build without the ee build tag.
	EditionCore = "core"
	// EditionEE is the edition of a build whose ee/register init ran.
	EditionEE = "ee"
)

// editionEE is the whole of the marker: one bool, flipped once, by the
// assembly file the ee build tag compiles in.
var editionEE bool

// Edition is the edition of this binary, "core" or "ee". The render step
// writes it into the build record (imagecheck.Record.Edition), which is what
// the launch-side warnings judge a job image by: the job runs the image's own
// fugaro, not the launcher's.
func Edition() string {
	if editionEE {
		return EditionEE
	}
	return EditionCore
}

// SetEditionEE marks this binary as the enterprise edition, for ee/register's
// init alone. It refuses a second call, so nothing can quietly halve the
// enforcement a tagged binary registered (design §5.5).
func SetEditionEE() error {
	if editionEE {
		return errors.New("the edition is already ee")
	}
	editionEE = true
	return nil
}
