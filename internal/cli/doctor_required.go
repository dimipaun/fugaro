package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// cliBelowRequiredID is doctor's check for the required-CLI stamp: the release
// the selected project's own `fugaro update` wrote, and whether this binary is
// below it. It is the second half of what doctor says about versions — beside
// cli-version, whose question is the opposite one (is a newer release
// available) — and it is the fence every project command passes through (T3),
// reported where the person already looks.
const cliBelowRequiredID = "cli-below-required"

// cliBelowRequiredFix, the one line the default view prints for this check, is
// cli-version's own and lives with their shared table (doctor_render.go): the
// same two commands are the remedy for the same reason.

// doctorOfflineAsk is what every skipped check of a `doctor --offline` run
// hands the reader: the flag that answers the skipped ones, and the check's own
// words under --all.
const doctorOfflineAsk = "run `fugaro doctor` without `--offline` to ask it"

// cliBelowRequiredCheck is doctor's "cli-below-required" check: whether this
// binary is below the release the selected project requires (T5, spec
// "Doctor", R9).
//
// It is modelled on cliVersionCheck and it never fails doctor on its own: a
// `dev` or pre-release binary has nothing to compare with, a project with no
// database carries no stamp, and a node that is not there is a project with no
// fence — each of those is an ordinary pass. Only a stamp above this binary is
// a warning, and doctor is a command the fence exempts (T3), so what this
// check is for is telling the person before a project command refuses them.
//
// offline answers from this machine's stamp cache and nothing else (R9): the
// reader is built with no client at all, so a cache entry that is there and
// young enough answers and no path below can leave the machine. Without one
// the check is skipped with its reason, the way --offline treats every other
// check whose answer is not a file here. It is the same reader the ordinary run
// uses, through the same seam (newStampSource), so the cache rules — the TTL,
// the fail-open cases — are the ones the fence itself applies.
func cliBelowRequiredCheck(ctx context.Context, lc *localcfg.Config, offline bool, version string, now time.Time) doctorCheck {
	id := cliBelowRequiredID
	// A `dev` build or a pre-release tag is never below anything: it has nothing
	// to compare with and skips silently, the stance cliVersionCheck takes
	// (spec, "Dev builds").
	if !updatecheck.Valid(version) {
		return doctorCheck{ID: id, OK: true}
	}
	// A project with no Realtime Database can carry no stamp — the node lives
	// beside config/policy/shared — so there is nothing to be below. It is the
	// same answer the fence gives a command with no RTDB URL (T3).
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return doctorCheck{ID: id, OK: true}
	}
	// The no-client half of the reader is what makes --offline honest: with no
	// Open, Required can answer from the cache and from nowhere else.
	opts := versionstamp.Options{Open: stampOpen, Getenv: os.Getenv}
	if offline {
		opts = versionstamp.Options{Getenv: os.Getenv}
	}
	res, err := newStampSource(opts).Required(ctx, versionstamp.Query{
		URL: lc.Budget.RTDBURL, NoAuth: lc.Endpoints.NoAuth, Now: now})
	var malformed *versionstamp.MalformedError
	switch {
	case errors.As(err, &malformed):
		// The one read that fails closed (T1): the node is there and this binary
		// cannot read it, which is a hand edit or a node a newer release wrote.
		// It is a warning, and it keeps its own line (ownFix), because the
		// class's one line — upgrade, then `fugaro update` — is the wrong repair
		// for a hand edit: `update` refuses a malformed stamp too, and the
		// error's own sentence names the node and the fix that really applies.
		return doctorCheck{ID: id, Severity: "warning", ownFix: true,
			Problem: malformed.Error(), Fix: malformed.Error()}
	case err != nil:
		// Source returns no other error, but doctor reports rather than fails:
		// an unknown filed as "could not be read" is not the silent pass.
		return doctorCheck{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the required CLI of project %s could not be read: %v", lc.Name, err),
			Fix:     "run doctor again once the project's database is reachable"}
	}
	switch {
	case res.Origin == versionstamp.OriginNone && offline:
		return doctorCheck{ID: id, Skipped: true, Severity: "info",
			Problem: fmt.Sprintf("whether project %s requires a newer fugaro is not in this machine's stamp cache, and --offline reads no database", lc.Name),
			Fix:     doctorOfflineAsk}
	case res.Origin == versionstamp.OriginNone:
		// Fail open, as the fence does: the read did not answer, so the check
		// says the fact is unknown rather than refusing (D3264).
		return doctorCheck{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the required CLI of project %s could not be read: %s", lc.Name, res.Reason),
			Fix:     "run doctor again once the project's database is reachable"}
	case !res.Found:
		return doctorCheck{ID: id, OK: true}
	case versionstamp.Below(res.Stamp, version):
		return doctorCheck{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("project %s requires fugaro %s or newer, and this is %s: every command that acts on the project is refused until you upgrade", lc.Name, res.Stamp.Min, version),
			Fix:     cliBelowRequiredFix}
	default:
		return doctorCheck{ID: id, OK: true}
	}
}
