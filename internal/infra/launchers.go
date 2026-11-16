// launchers.go is the launcher class's list (D1938, F25) as the installation's
// tfvars carry it. It sits beside the two lists it is derived from — the
// launchers and the operators the roots grant IAM to — because the class and
// the grant have to name the same people: Cloud Run grants the invoker per
// service, so a launcher who may invoke the intake or the credentials service
// passes IAM and then meets the process's own class check (F36), and a roster
// that reached one list but not the other would be a caller IAM admits and the
// process refuses with no explanation of its own.

package infra

import (
	"slices"
	"strings"
)

// launcherAccounts is the roster's people as the bare email addresses a Google
// ID token's email claim holds: `user:` members only, sorted, each once.
//
// Why only `user:`. The class is compared against a token's email claim
// (internal/credsvc's foldLauncherAccounts), so a `group:` or `domain:`
// launcher is a member IAM admits and no address the check can match — the
// reading credentials.tf already takes for the operators' person class
// (`credentials_read_members`), and the fail-closed one: the process admits
// nobody rather than everybody a group might mean. Resolving a group to its
// members is the roster's, not this file's, and it does not exist yet.
func launcherAccounts(members []string) []string {
	out := []string{}
	for _, m := range members {
		if email, ok := strings.CutPrefix(m, "user:"); ok {
			out = append(out, email)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
