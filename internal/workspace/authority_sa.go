package workspace

// The SA authority (WU20): the mechanism a workspace with no GCP organization
// holds its members by (D2803, F16). There is no folder to put a member's
// project in, so the workspace's own IAM account is granted roles/owner on the
// member's projects, one grant per project, live.
//
// **The account is impersonated and never keyed** (D2803, F20). The grant above
// is what makes it an owner of the member, and an admin holds the account's
// token-creator role, which is what lets them act as it; nothing in this file
// creates, reads or expects a key of it, and the surface it is given
// (SAProjects) has no call that could, so the design's whole account side is
// reachable here only through impersonation. A key would outlive every
// revocation this file can make, which is why the surface, and not a comment,
// is what holds the line.
//
// Link and Unlink are the MEMBER OWNER's acts, made from that owner's own
// credentials on their own machine (D2800, D2819, A6): the grant lands on their
// own project and its removal is theirs too. AddAdmin, RemoveAdmin and
// AdminBindings are the WORKSPACE's own side (D2818): an admin's token creator
// on the account, and — because the roster node and the workspace's audit trail
// live in the workspace's own project (R8) — an owner grant on that project,
// which the account does not hold and needs no part of.
//
// What AddAdmin cannot do by API is the one thing U8 leaves open: whether
// Google lets a person be made a project's owner by API outside an
// organization, or only by an invitation they accept in the console. The cloud
// side answers that where the call is made, with ErrSAOwnerInvite, and AddAdmin
// turns it into the step a person takes rather than reporting a grant it did
// not make.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// SAConsoleIAM is the console page a workspace's own project's IAM is managed
// on, with the project's name appended. It is where AddAdmin sends a workspace
// admin when Google refuses R8's owner grant by API (U8, D2825).
const SAConsoleIAM = "https://console.cloud.google.com/iam-admin/iam?project="

// ErrSAOwnerInvite is Google's refusal of an owner grant made by API on a
// project outside an organization, which is the state U8 records as unverified:
// the grant is then an invitation the person accepts in the console, and an
// API caller has no way to make it.
//
// It is a sentinel of this package because the classification belongs where the
// call is made — the workspace package cannot see an API's answer, and the
// surface below is an interface — and because AddAdmin has to tell this refusal
// from another: a caller who lacks the permission to write a policy at all has
// not been offered an invitation, and telling them to look in the console would
// hide a mistake they can fix.
var ErrSAOwnerInvite = errors.New("Google refuses an owner grant made by API outside an organization; the grant is an invitation to accept in the console")

// SAProjects is the IAM surface the SA authority uses: the calls
// internal/infra/sa.go makes. It is an interface so this file's own behaviour —
// which projects a link covers, the order AddAdmin binds and unbinds in, and the
// step Google's refusal turns into — is tested without a cloud in the way;
// *infra.SAProjects is the implementation the CLI hands in, and it satisfies
// this interface as it stands (the two packages cannot name each other's types:
// the dependency between them runs through internal/projectpolicy, which imports
// this one).
//
// A project is named by its ID, not by projects/<id>, as it is everywhere else
// in this package; the account — where an argument is one — is named by its API
// resource name, projects/<workspace>/serviceAccounts/<email>, which the
// authority composes.
//
// Every member argument is an IAM member string, prefix and all: the account is
// serviceAccount:<address> and a person is user:<address>, which is the grammar
// a policy holds and the only spelling Google resolves. The address alone is not
// one — a policy that names it is refused — so the authority composes the
// prefix where it composes the member, in accountMember, and the account's bare
// address never reaches this surface as a member.
type SAProjects interface {
	// GrantOwner gives member roles/owner on project.
	GrantOwner(ctx context.Context, project, member string) error
	// RemoveOwner drops member's roles/owner binding on project. A member the
	// role also reaches through a binding this call does not write — a
	// conditional one — is refused rather than reported as removed, as the
	// folder side's RemoveOwner is.
	RemoveOwner(ctx context.Context, project, member string) error
	// Owns reports whether member holds roles/owner on project now. It is the
	// read Holds is built on, and it is a live read: what the roster or the
	// workspace node says about a grant is not this.
	Owns(ctx context.Context, project, member string) (bool, error)
	// TokenCreators lists the members bound to
	// roles/iam.serviceAccountTokenCreator on account, in the API's order, a
	// binding that carries a condition included: that member can act as the
	// account too, and AdminBindings is what the roster's admins are compared
	// against (D2823).
	TokenCreators(ctx context.Context, account string) ([]string, error)
	// AddTokenCreator binds member to roles/iam.serviceAccountTokenCreator on
	// account: the grant that lets them impersonate the account, and the only
	// authority over it a workspace admin holds.
	AddTokenCreator(ctx context.Context, account, member string) error
	// RemoveTokenCreator drops that binding.
	RemoveTokenCreator(ctx context.Context, account, member string) error
}

// SAAuthority is this mechanism's implementation. Account is empty for a
// workspace whose `init` has not read the root's output yet; every method then
// refuses rather than granting with a principal nobody named, because an empty
// member string is not a binding Google would refuse — a policy write that names
// no member simply writes nothing, and the workspace would report a member it
// never held.
//
// It satisfies Authority as it stands: the two mechanisms differ in what they do
// with the member, not in the shape of the call, so I1 wires this value rather
// than adapting it.
type SAAuthority struct {
	// Workspace is the workspace's name, which is its own project's ID too
	// (A1, D1290): the account lives in it, so its resource name is composed
	// from this, and R8's owner grant lands on it.
	Workspace string
	// Account is the workspace IAM account's email, from the workspace root's
	// WorkspaceIAMAccountOutput (WU3, D2803).
	Account string
	// Projects is the IAM surface; nil is a programming error and panics at the
	// first call, as a nil client does everywhere else here.
	Projects SAProjects
	// Out receives what a command prints; nil discards it. AddAdmin prints the
	// console-invitation step on it when Google refuses the owner grant (U8).
	Out io.Writer
}

var _ Authority = (*SAAuthority)(nil)

// Kind reports the SA mechanism. The mechanism is the workspace's, chosen once
// by `workspace init` (D2803), so this takes no member: a workspace that fell
// back to the account does so for every member it holds.
func (a *SAAuthority) Kind() AuthorityKind { return AuthoritySA }

func (a *SAAuthority) printf(format string, args ...any) {
	if a.Out != nil {
		fmt.Fprintf(a.Out, format, args...)
	}
}

// Link grants the account roles/owner on the member's GCP project and on its
// Firebase project when that is a project of its own (R10), from the member
// owner's own credentials (D2800, D2819).
//
// It records nothing. Where the folder mechanism has to write a record of the
// parent it moved — R14, because a move is not reversible from the project
// alone — an owner binding is readable from the project's own policy, which is
// what Holds reads and Unlink removes. A half-made link (the second grant
// refused) is therefore visible as a member the workspace does not hold, and
// running Link again is how it is finished: a grant that is already there costs
// one read at the cloud side and no write, so the retry converges rather than
// duplicating.
//
// The workspace's own project is the one name a link refuses (D2818, R8). The
// account is impersonated by every admin — a token creator of it is what an
// admin holds — so an owner grant to the ACCOUNT on the workspace's own project
// is an owner grant to all of them, and one RemoveAdmin cannot take away: that
// call drops the admin's own owner grant first precisely so that a later
// failure cannot be undone by the owner it just removed, and an owner the
// account holds is reachable again through impersonation. The workspace project
// is the installation's, made by `workspace init`, and never a member's; a link
// naming it is a mistake about which project is the member's, and the refusal
// says which.
func (a *SAAuthority) Link(ctx context.Context, m MemberTarget) error {
	if err := a.check(m); err != nil {
		return err
	}
	if err := a.checkNotTheWorkspace(m); err != nil {
		return err
	}
	for _, p := range saProjects(m) {
		if err := a.Projects.GrantOwner(ctx, p, a.accountMember()); err != nil {
			return fmt.Errorf("granting %s roles/owner on %s: %w", a.Account, p, err)
		}
	}
	return nil
}

// Unlink drops the grants Link made and only them (D2822): the admins' stored
// grants on the member stay in its roster until the workspace project's owner
// runs `workspace project rm`, and their token-creator bindings are the
// workspace's own and go with RemoveAdmin. A member the account does not own is
// not an error: what is asked for is a project without that grant, and one that
// never had it is already there.
func (a *SAAuthority) Unlink(ctx context.Context, m MemberTarget) error {
	if err := a.check(m); err != nil {
		return err
	}
	for _, p := range saProjects(m) {
		if err := a.Projects.RemoveOwner(ctx, p, a.accountMember()); err != nil {
			return fmt.Errorf("removing %s's roles/owner on %s: %w", a.Account, p, err)
		}
	}
	return nil
}

// Holds reports whether the account holds roles/owner on the member's projects
// now: on the GCP project and, when it is a project of its own, on the Firebase
// project too (R10).
//
// Both, not either: the workspace's authority over a member is the pair of
// grants or it is nothing, because the member's installation and the Firebase
// side of it are held by the same account. A member left half-granted — the
// second call refused, or a grant dropped by hand — is read as not held, which
// is what makes `status` name it and Link, run again, finish it. This is where
// the SA model reads differently from the folder model, whose single parent is
// the whole of the hold.
//
// It reads the project's live policy and never the roster or the workspace
// node: a grant nothing made is not one, and only what is there now is what
// `status` and the installation-level refusals key on (R11).
func (a *SAAuthority) Holds(ctx context.Context, m MemberTarget) (bool, error) {
	if err := a.check(m); err != nil {
		return false, err
	}
	for _, p := range saProjects(m) {
		owned, err := a.Projects.Owns(ctx, p, a.accountMember())
		if err != nil {
			return false, fmt.Errorf("reading %s's roles/owner on %s: %w", a.Account, p, err)
		}
		if !owned {
			return false, nil
		}
	}
	return true, nil
}

// AddAdmin gives a workspace admin the account's token creator, so that they
// can impersonate it and reach every member the account is an owner of, and
// — R8 — roles/owner on the workspace's own project, where the roster node and
// the workspace's audit trail live: an admin maintains those, and a token
// creator alone reaches neither.
//
// The token creator comes first, and it is the half that survives a refusal of
// the other: an account binding is written by API whatever Google answers an
// owner grant on a project outside an organization, so an admin made during
// that refusal can already impersonate the account, and is left short only of
// the workspace's own project until the invitation is accepted (U8, D2825).
// That refusal prints the console step and is returned as an error wrapping
// ErrSAOwnerInvite — the print is what a person acts on, and the error is the
// truth that the binding is not there, so a caller reports a half-made admin
// rather than a grant it did not make.
//
// Running it again once the invitation has been accepted converges: the token
// creator is already bound, the owner grant is already there, and neither is
// written again.
func (a *SAAuthority) AddAdmin(ctx context.Context, admin string) error {
	if err := a.checkAdmin(admin); err != nil {
		return err
	}
	if err := a.Projects.AddTokenCreator(ctx, a.account(), admin); err != nil {
		return fmt.Errorf("binding %s as a token creator of %s: %w", admin, a.Account, err)
	}
	if err := a.Projects.GrantOwner(ctx, a.Workspace, admin); err != nil {
		if errors.Is(err, ErrSAOwnerInvite) {
			step := a.inviteStep(admin)
			a.printf("%s\n", step)
			return fmt.Errorf("%s was bound as a token creator of %s, and its owner grant on %s was not made. %s (%w)", admin, a.Account, a.Workspace, step, err)
		}
		return fmt.Errorf("granting %s roles/owner on %s: %w", admin, a.Workspace, err)
	}
	return nil
}

// RemoveAdmin drops both bindings AddAdmin makes, the owner grant first.
//
// The order is the whole of the care here. The owner of a project can write its
// policy, and so can grant themselves any binding on it, including the token
// creator back — so an owner grant that outlives this call is an admin who
// returns. The token creator cannot do that: a token creator impersonates the
// account, and the account is an owner of the members, not of the workspace's
// own project (R8's grant belongs to the admin, not to the account). Taking the
// owner grant first therefore leaves nothing with which the second removal
// could be undone, and a failure between the two leaves an admin who has lost
// the workspace's own project and nothing else.
func (a *SAAuthority) RemoveAdmin(ctx context.Context, admin string) error {
	if err := a.checkAdmin(admin); err != nil {
		return err
	}
	if err := a.Projects.RemoveOwner(ctx, a.Workspace, admin); err != nil {
		return fmt.Errorf("removing %s's roles/owner on %s: %w", admin, a.Workspace, err)
	}
	if err := a.Projects.RemoveTokenCreator(ctx, a.account(), admin); err != nil {
		return fmt.Errorf("%s is no longer an owner of %s; removing it as a token creator of %s failed: %w", admin, a.Workspace, a.Account, err)
	}
	return nil
}

// AdminBindings lists the account's token creators, which is what the workspace
// node's `Admins` is compared against (D2823): the live bindings are the source
// of truth for who is an admin, because whoever can rewrite that node holds no
// permission to set one (WU0's threat model). A person holding the role through
// a group or a domain is listed as that group or domain, as the folder side
// lists one (F25).
//
// The owner grants R8 adds are not part of this and are not read here: the
// token creator is the binding that carries the authority over the members, and
// it is the one the roster's admin list is about.
func (a *SAAuthority) AdminBindings(ctx context.Context) ([]string, error) {
	if err := a.checkAccount(); err != nil {
		return nil, err
	}
	return a.Projects.TokenCreators(ctx, a.account())
}

// account is the account's API resource name, which is the form its policy is
// read and written on (projects/<workspace>/serviceAccounts/<email>).
func (a *SAAuthority) account() string {
	return "projects/" + a.Workspace + "/serviceAccounts/" + a.Account
}

// accountMember is the account as an IAM policy member: serviceAccount: and its
// address. The prefix is the API's grammar for the principal and not decoration
// — a policy naming the bare address is refused — so a link that spelled it bare
// would fail on every project with an answer that reads like any other refusal.
// Account itself stays the bare address, because that is what
// WorkspaceIAMAccountOutput carries and what a message to a person reads as.
func (a *SAAuthority) accountMember() string {
	return "serviceAccount:" + a.Account
}

// inviteStep is the line AddAdmin prints when Google refuses the owner grant:
// what was refused, what a person does about it, and where. The admin's name is
// escaped, because it is a value a person typed and this is a line printed on a
// terminal (safetext's rule); the workspace is escaped for the URL instead,
// where an escaping for a terminal would be the wrong one.
func (a *SAAuthority) inviteStep(admin string) string {
	return fmt.Sprintf("Google refuses an owner grant on the project %s made by API, which is outside an organization: invite %s as an owner in the Cloud Console at %s and have them accept the invitation Google sends, then run this again to confirm the binding",
		a.Workspace, safetext.Field(admin), SAConsoleIAM+url.QueryEscape(a.Workspace))
}

// check refuses an act on a member this authority cannot name: every call here
// grants or reads the account's grant on the member's project, so both the
// account and the project have to be named.
func (a *SAAuthority) check(m MemberTarget) error {
	if err := a.checkAccount(); err != nil {
		return err
	}
	if m.Project == "" {
		return errors.New("no project given: the SA authority's grant names the member's GCP project")
	}
	return nil
}

// checkNotTheWorkspace refuses a link whose member names the workspace's own
// project, as either its GCP project or its Firebase one. It is a link's
// refusal alone: the grant must not be made, while an unlink or a hold of that
// name is not a way to make one (a grant there can only come from outside this
// authority, and dropping it is the right thing to do with it).
func (a *SAAuthority) checkNotTheWorkspace(m MemberTarget) error {
	if m.Project == a.Workspace || m.Firebase == a.Workspace {
		return fmt.Errorf("refusing to link %s: it is the workspace's own project, which the account is never granted roles/owner on — every admin can impersonate the account, so that grant would be theirs and would outlive RemoveAdmin (D2818, R8)", a.Workspace)
	}
	return nil
}

// checkAdmin refuses an admin act on a workspace whose account is unknown. It
// is a refusal and never a panic or an empty binding: the address comes from an
// output `workspace init` read, and a call made before that would otherwise
// write a policy binding for the empty principal.
func (a *SAAuthority) checkAdmin(admin string) error {
	if err := a.checkAccount(); err != nil {
		return err
	}
	if admin == "" {
		return errors.New("no admin given: an admin binding names the member who becomes a token creator of the workspace IAM account")
	}
	return nil
}

// checkAccount refuses every act of a workspace whose IAM account nobody named,
// and says where the name comes from: the address is the workspace root's
// WorkspaceIAMAccountOutput (WU3), read by `workspace init` and refreshed by
// `fugaro update`.
func (a *SAAuthority) checkAccount() error {
	if a.Account == "" {
		return fmt.Errorf("the workspace's IAM account is not known: its address is the %s output of the workspace root, which `workspace init` reads and `fugaro update` refreshes", WorkspaceIAMAccountOutput)
	}
	return nil
}

// saProjects is the member's projects the account is granted on, in the order
// they are granted and dropped: the GCP project first, and the Firebase project
// when that is a project of its own (F2, R10). A member whose Firebase project
// is its GCP project is one grant, because it is one project.
func saProjects(m MemberTarget) []string {
	if m.Firebase == "" || m.Firebase == m.Project {
		return []string{m.Project}
	}
	return []string{m.Project, m.Firebase}
}
