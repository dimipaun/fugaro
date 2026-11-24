// launchers.go is the launcher class's list (D1938, F25) as the installation's
// tfvars carry it, and the intake's second list (WC3, R5), which is a list of
// its own. It sits beside the two lists it is derived from — the launchers and
// the operators the roots grant IAM to — because the class and the grant have
// to name the same people: Cloud Run grants the invoker per service, so a
// launcher who may invoke the intake or the credentials service passes IAM and
// then meets the process's own class check (F36), and a roster that reached one
// list but not the other would be a caller IAM admits and the process refuses
// with no explanation of its own.

package infra

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/workspace"
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

// serviceAccountSuffix is how a service account's address ends, as Google mints
// it. It is internal/access's own const and internal/credsvc's own literal,
// restated because neither package is one this file may import, and it is the
// SAME rule in all three: an address of this shape is a job's and not a
// person's, so no class of people may carry one.
const serviceAccountSuffix = ".gserviceaccount.com"

// classAddressBytes is the bound internal/credsvc's foldEmail holds every value
// to (printableHeaderValue's maxHeaderTokenBytes, restated for the same reason
// the suffix above is): an entry longer than this is a configuration error at
// NewLaunchers rather than an entry nothing matches, so the intake's revision
// exits on start. Nothing bounds a roster address — CanonicalAddress, the
// schema and `fugaro workspace user add` all take one of any length — so the
// only place this bound can be kept is the class list itself.
const classAddressBytes = 512

// IntakePeople is the intake's SECOND class list (WC3, R5): the people of a
// workspace's roster, as the bare addresses a Google ID token's email claim
// holds — sorted and each once, so two spellings of one roster give equal bytes.
//
// Why the roster's humans and not its machines. The class is matched against a
// token's email claim, and a machine's address is a service account's — the one
// spelling internal/credsvc's foldLauncherAccounts REFUSES outright, because no
// interactive ID token is minted for one. A machine is admitted to a workspace's
// projects by its IAM bindings and never by a token of this class, so listing it
// here would be an entry nothing can ever match (the fail-closed reading
// launcherAccounts takes for a group or a domain), and an ENTRY it refused is
// worse than one nothing matches: the class list is the process's, so the
// refusal is an error the intake's new revision exits on at start, which fails
// the apply that would have written it — a roster person the workspace added as
// a `human` on a service account's address (neither `workspace user add` nor
// CanonicalAddress refuses one) would break that installation's every later
// `fugaro update`. That shape and one past the fold's own byte bound (see
// classAddressBytes, the other refusal NewLaunchers makes and for the same
// reason) are therefore dropped here, by the fold's rules and for them, and the
// module's validation refuses both in a hand edit; nothing else about an address
// the roster can hold is, because a class list that dropped a value the module
// takes — or took one it refuses — is the same broken update from the other side.
//
// Why a function of the roster and not of the launcher list. The two lists are
// separate on purpose (F10): the same `launchers` variable is the workspace
// project's CREDENTIALS SERVICE invoker set (iam.tf, local.launcher_invoker_members),
// so a roster person added there would be admitted to a service this task does
// not widen. What the roster's people are admitted to is the intake, and its own
// block is where this list is written (IntakeSpec.People, the module's
// var.intake.people).
//
// The addresses are taken as the roster holds them, which is their canonical
// spelling: DecodeRoster refuses a node whose person is not canonical, and the
// class is compared to the email claim of a token Google minted, which is lower
// cased.
//
// The list is what the module's own validation takes, and that is a rule rather
// than a coincidence: the value this writes is planned by the installation root
// a moment later, so a class list holding one address more or one fewer than
// var.intake.people accepts is an update that fails — or a person the roster
// named and the module's plan refuses. It is why the shapes dropped here are the
// two the PROCESS refuses and nothing else: an address whose local part carries a
// `:` or begins `user:` (both of which workspace.CanonicalAddress keeps and
// `fugaro workspace user add` accepts) stays, and the module's validation holds
// these addresses to the roster's own canonical class, which takes them.
func IntakePeople(roster *workspace.Roster) []string {
	out := []string{}
	if roster == nil {
		return out
	}
	for _, p := range roster.People {
		folded := strings.ToLower(p.Address)
		if p.Kind != workspace.KindHuman || strings.HasSuffix(folded, serviceAccountSuffix) || len(folded) > classAddressBytes {
			continue
		}
		out = append(out, p.Address)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// IntakeClass is the class the intake's PROCESS reads and its invoker grant
// names: the launchers and the roster's people as ONE list, sorted and each
// once — the module's own expression (intake.tf's local.intake_accounts,
// sort(distinct(concat(var.launcher_accounts, local.intake_people)))), restated
// so a caller can ask whether a roster change moves the class at all.
//
// Why that question needs asking. A workspace's roster names its own launchers
// too — the person who runs `fugaro workspace init` is on the installation's
// launcher list and on the roster — so an address can be on both lists, and the
// module's class is then one list with that address once. A roster that gained
// or lost exactly that person changes var.intake.people and leaves the class the
// revision is handed byte for byte what it was, so a caller that wrote the block
// for it and then read the plan would find none — and an empty plan is
// indistinguishable, from there, from the defect the fill's refusal names: a
// class the module does not carry onto the revision at all.
func IntakeClass(launchers, people []string) []string {
	out := make([]string, 0, len(launchers)+len(people))
	out = append(out, launchers...)
	out = append(out, people...)
	slices.Sort(out)
	return slices.Compact(out)
}
