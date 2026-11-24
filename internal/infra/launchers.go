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
	return bareMembers(members, "user:")
}

// launcherServiceAccounts is the roster's service accounts as bare addresses
// (launcher-does-not-mint plan task LM13; D3123): the class a person's list
// cannot hold, because the account presents a service account's own Google ID
// token rather than a person's, and the addresses the credentials service is
// configured with (var.credentials_service.roster_service_accounts), which is
// also the list the module grants roles/run.invoker on the service for.
//
// Why only `serviceAccount:` entries, which is launcherAccounts' rule read one
// class over: the class is compared against a token's email claim
// (internal/credsvc's foldLauncherAccounts), and the credential half of an
// `email` claim is the account's own address, so the member spelling would
// match nobody. A `group:` or `domain:` entry holds no address at all and is
// deliberately not resolved here: admitting a whole group is a decision the
// roster makes by naming its members, and the fail-closed reading is the one
// launcherAccounts takes.
func launcherServiceAccounts(members []string) []string {
	return bareMembers(members, "serviceAccount:")
}

// bareMembers is the roster's entries of one member class as the bare
// addresses a Google ID token's email claim holds: class stripped, sorted,
// each once.
func bareMembers(members []string, class string) []string {
	out := []string{}
	for _, m := range members {
		if email, ok := strings.CutPrefix(m, class); ok {
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

// memberAddress is the shape every entry of the class list is written with, and
// it is var.launcher_accounts' own rule restated for the same reason the two
// constants above are: one `@`, an empty-free local part carrying no colon, and
// a domain carrying no `@`. The same shape the module's people validation holds
// a hand edit to, because the class list and the grant that admits the same
// person are the SAME value: the module spells each entry "user:${e}" into the
// intake's invoker set (intake.tf), so an entry already carrying a colon is one
// it would spell "user:a:b@example.com" — not the member IAM has for a person,
// and not a claim any token carries.
//
// internal/access states the rule over the same kind of value and is where it
// was decided (Admin.validate, and Admins above it): an entry is the bare
// address a Google ID token's email claim carries, never the "user:" member
// form a grant is stored in, so a derivation drops the person rather than write
// an entry its own writer refuses (internal/policy's ValidVerifierAccounts
// refuses one for the same reason). The other half of the argument is the list
// this one merges with in local.intake_accounts: var.launcher_accounts has held
// every entry to this shape since F31, so an installation whose launchers
// spelled one this way would already fail the plan today.
func memberAddress(addr string) bool {
	local, domain, ok := strings.Cut(addr, "@")
	return ok && local != "" && domain != "" &&
		!strings.ContainsAny(local, ": ") && !strings.ContainsAny(domain, "@ ")
}

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
// `fugaro update`.
//
// Three shapes are therefore dropped here, and each is one the class list
// cannot carry rather than one the roster cannot hold: a service account's
// address (whatever kind the roster spells beside it, folded first as the
// process folds it), one past the fold's byte bound (classAddressBytes, the
// other refusal NewLaunchers makes and for the same reason), and one that is
// not the bare member address a token's email claim carries (memberAddress —
// the local part's colon is the member spelling, which the module would spell
// into the grant a second time and no claim holds). The module's validation
// refuses all three in a hand edit, by the same rule, so the writer and the
// module agree in both directions and no value the roster can hold is an update
// that fails. A person the roster named on one of the three is a person the
// class does not hold (internal/access's own reading of the same value, and
// fail closed: an entry left off admits nobody, while an entry written in a
// shape neither the claim nor IAM can carry looks right and admits nobody any
// way).
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
// named and the module's plan refuses. It is why the three shapes dropped here
// are exactly the three the module's validation refuses, by the same rules and
// in the same spellings (folded for the suffix, the raw value for the shape).
func IntakePeople(roster *workspace.Roster) []string {
	out := []string{}
	if roster == nil {
		return out
	}
	for _, p := range roster.People {
		folded := strings.ToLower(p.Address)
		if p.Kind != workspace.KindHuman || strings.HasSuffix(folded, serviceAccountSuffix) ||
			len(folded) > classAddressBytes || !memberAddress(p.Address) {
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
