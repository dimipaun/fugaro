package cli

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// doctorMobileID is doctor's mobile check. There is one, and it is the block's
// whole line: what a project that turns sync on stores in the identity project
// (plan task P6, D2796 — "admins are told clearly what is stored").
const doctorMobileID = "mobile-sync"

// doctorMobileChecks reports the mobile block of the policy in force (plan task
// P6, D2796): the one thing about this surface an admin cannot see from the
// phone, which is what the project's own setting stores somewhere else.
//
// It is a measurement and not a finding — sync on is the default, and the
// operator has nothing to do about it — so its severity is "info" with OK set,
// which is the pair the class rules file as a measurement: printed by --all and
// --json, and counted as no finding by the one screen (project-account-stored's
// shape, which reports what a project's token reaches when nothing is wrong).
// A project with no block at all says nothing here: that node was published
// before the key existed, mobile pairing is off, and nothing is stored.
//
// It reads the policy and nothing else, so it is asked where the node is in
// force — a node some unknown key has every reader refusing is one the pairing
// route never acts on either, and this check's caller decides that (the
// unknown-keys guard in doctorPolicyChecks).
func doctorMobileChecks(lc *localcfg.Config, p *projectpolicy.Policy) []doctorCheck {
	if !p.MobileEnabled() {
		return nil
	}
	if !p.MobileSync() {
		return []doctorCheck{{ID: doctorMobileID, OK: true, Severity: "info",
			Problem: fmt.Sprintf("mobile pairing is on for project %s and mobile.sync is off: the identity project stores no paired-project list, so a new device of the same person pairs again by reading a QR code", lc.Name),
			Fix:     "nothing to do: this is what mobile.sync: false asks for. The phone may still keep its own list in the platform's synced keychain, which the identity project never sees"}}
	}
	return []doctorCheck{{ID: doctorMobileID, OK: true, Severity: "info",
		Problem: fmt.Sprintf("mobile pairing is on for project %s and so is mobile.sync: the identity project stores each signed-in person's list of paired projects — the opaque project ids and endpoints, and which identity paired each one — encrypted with a Cloud KMS key that only that person's own function decrypts, with readable names and everything else staying on the phone", lc.Name),
		Fix:     "nothing to do: this is what lets a new device of the same person restore the list instead of pairing again by QR code. To store nothing, write mobile.sync: false in the published node by hand — 'fugaro project policy show' prints it, and this machine's config carries no mobile block, so a publish keeps the node's own"}}
}
