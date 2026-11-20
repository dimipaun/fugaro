package workspace

// The roster contract (C1, docs/workspace-projects-users/contract.md): the
// shape of the node at `workspaceRosters/<name>` and of the audit events
// beside it, and what each side that reads or writes one may rely on.
//
// The roster is a wire format over IAM, and that is the whole reason this file
// is one: the node is rewritten by the workspace's own service, by its admins
// and by the workspace project's owners, editors and budget admins (WU0's
// threat model, §3), and every level-2 task of this plan reads or writes it --
// `workspace user add|rm|ls|grant|revoke`, `workspace domains`,
// `workspace project add|rm|list|reconcile`, `workspace init`, the drift
// comparison in `status`. A field whose meaning drifted between two of those
// would be a roster that says one thing to the writer and another to the
// reader, and the drift fence (D2813, D2817) is what turns that into a
// hundred-project incident rather than a refusal. So: this file and
// schemas/workspace-roster.schema.json describe ONE document, the document is
// closed at every level (an unknown field is an error, as every config and
// wire format here does), and the schema is held to the reader by the tests in
// roster_test.go -- the enum lists, the address grammar and the folder grammar
// are compared key by key and value by value rather than trusted to agree.
//
// What the node holds, and what this is a contract over (D2806, D2812, D2813,
// D2817, D2818, D2823, R6, R14):
//
//   - the people of the workspace -- a person's address, whether they are a
//     human or a machine, and the external mark D2812 writes when somebody was
//     admitted from outside the workspace's allowed domains;
//   - for a machine, the per-project roles the workspace expects it to hold
//     (D2806). A machine is a member of the workspace node and of nothing else:
//     it gets no member roster, no grant and no audit trail of its own, because
//     the point of D2806 is that a machine's real IAM is the workspace owner's
//     own IAM and never Fugaro's to write;
//   - the workspace's admins: people who also hold the live binding the
//     mechanism gives an admin (D2823 -- an entry here is trusted only while
//     that binding is there, which is why `status` compares the two);
//   - the allowed email domains of D2812;
//   - the grants the workspace wrote into its members' rosters (D2818's mark).
//     The mark lives HERE and never in a member's roster: the member roster's
//     format is unchanged (D2818 as corrected, R12), so a consumer that wants
//     to know what the workspace wrote reads this node and not the member's;
//   - the mechanism the workspace was created with -- a folder it owns, or the
//     service-account model of D2803 -- and, for the folder, the folder's own
//     id, which is what `workspace init` writes with the caller as the first
//     admin (R6, WU24).
//
// What it does NOT hold is as load-bearing: no per-project entry (R14 -- the
// workspace's own projects are records of their own, in `workspaces/<name>` and
// in the member records, and a roster that copied one would be a second place
// the same fact could disagree), no member's Firebase project or job service
// account, no secret, and no role a person holds in a member (that is the
// member's own roster, internal/access). A roster is the workspace's statement
// about people, and about which projects it has written grants into.
//
// One field never changes meaning. Within version 1 every object here is
// closed and every list is a set written sorted with no duplicates, so two
// spellings of one roster are one document; a field that has to change meaning
// gets a new name, and a shape that has to change gets RosterVersion 2, which
// this build refuses with "a newer Fugaro wrote it" rather than reading the
// half of it it recognizes. Adding a field is what version 1 already allows --
// the reader of a newer node refuses it by the closed object, which is the
// answer the HARD RULE wants: an old CLI never guesses at a node a newer one
// wrote.

import (
	"bytes"
	"cmp"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// RosterVersion is the version of the roster document this build writes and
// reads. It is written in every roster: a node a newer Fugaro wrote is refused
// as a newer one, because a reader that took the half it recognized out of a
// shape that moved would be acting on a roster that no longer means what its
// keys say.
const RosterVersion = 1

// AuditEventVersion is the version of the audit event document this build
// writes and reads, for the same reason and with the same answer.
const AuditEventVersion = 1

// PathRoster is the roster node of the workspace named name, relative to the
// workspace's own database root: `workspaceRosters/<name>`, a SIBLING of the
// published record and never inside it (R5). The place is not cosmetic: the
// publish stage of `fugaro update` rewrites `workspaces/<name>` whole and
// refuses a node carrying a key it does not know, so a roster under the record
// would stop the update of every CLI older than this plan.
//
// The name is held to the project-name grammar (config.ProjectNameRE), which
// every caller already holds a workspace name to: a workspace name IS a project
// name (F21), and a name that is not one is refused here rather than escaped
// into a path of its own.
func PathRoster(name string) (string, error) {
	if !config.ProjectNameRE.MatchString(name) {
		return "", fmt.Errorf("workspace: %q is not a project name, so it is not a workspace name and has no roster node", name)
	}
	return pathWorkspaceRosters + "/" + name, nil
}

// Kind is what a person of the roster is: a human being, or a machine.
type Kind string

const (
	// KindHuman is a person who signs in.
	KindHuman Kind = "human"
	// KindSA is a service account -- a machine member (D2806). It is recorded
	// here so the workspace knows what it expects it to hold, and its real IAM
	// is the workspace owner's, never Fugaro's: no grant, no member roster and
	// no audit node of its own.
	KindSA Kind = "sa"
)

// Kinds is the closed set of kinds, in the order they are written in a
// document, and the list schemas/workspace-roster.schema.json's enum is pinned
// to.
func Kinds() []Kind { return []Kind{KindHuman, KindSA} }

// Mechanism is how a workspace holds the projects in it: the folder it created
// and owns, or the service-account model of D2803.
type Mechanism string

const (
	// MechanismFolder is the folder mechanism: the workspace owns the folder
	// its members' projects are moved into, and an admin is a folder owner.
	MechanismFolder Mechanism = "folder"
	// MechanismSA is the service-account model: the workspace reaches a member
	// through an account of that member's project, and an admin is whoever may
	// mint a token for it (D2803, F20).
	MechanismSA Mechanism = "sa"
)

// Mechanisms is the closed set of mechanisms.
func Mechanisms() []Mechanism { return []Mechanism{MechanismFolder, MechanismSA} }

// Role is a role a person may hold, and it is the member roster's own set
// restated (internal/access): the roles a stored grant carries are the roles
// `fugaro user grant` writes in a member, and a second list would be a grant
// this side spells one way and a member reads as nothing.
//
// It is restated and not imported because the dependency cannot run that way
// (internal/access imports internal/projectpolicy, which imports this package),
// and TestRosterAddressFoldAgreesWithAccess is what holds the two lists
// together.
type Role string

const (
	// RoleViewer hears summaries and sees job status and spending.
	RoleViewer Role = "viewer"
	// RoleOperator is a viewer who may also talk to agents and steer jobs.
	RoleOperator Role = "operator"
	// RoleAdmin is an operator who may also manage settings and roles.
	RoleAdmin Role = "admin"
)

// Roles is the closed set of roles, in rank order.
func Roles() []Role { return []Role{RoleViewer, RoleOperator, RoleAdmin} }

// Action is what an audit event records (D2811). The set is closed: an action
// outside it is a change nobody decided how to record, and it is refused rather
// than stored as a string a reader would have to guess at.
//
// The names follow the commands that write them, so a trail can be read back
// against the plan: `workspace init`, `workspace user add|rm`, the `--admin`
// changes, `workspace domains`, `workspace user grant|revoke`, the `workspace
// project` changes, and the offboarding a `workspace user rm` runs (WU9).
type Action string

const (
	// ActionInit is the workspace's first write: the roster created with its
	// mechanism, its folder and its first admin.
	ActionInit Action = "init"
	// ActionUserAdd is a person added to the roster.
	ActionUserAdd Action = "user.add"
	// ActionUserRemove is a person taken off the roster, with the grants the
	// workspace wrote for them.
	ActionUserRemove Action = "user.remove"
	// ActionUserOffboard is a person's secrets swept out of a member they
	// worked in (WU9, F13).
	ActionUserOffboard Action = "user.offboard"
	// ActionAdminAdd is a person made a workspace admin -- the roster entry,
	// which is only trusted beside the live binding it names (D2823).
	ActionAdminAdd Action = "admin.add"
	// ActionAdminRemove is a person taken off the admin list.
	ActionAdminRemove Action = "admin.remove"
	// ActionDomainAdd is an email domain added to the allowed list (D2812).
	ActionDomainAdd Action = "domain.add"
	// ActionDomainRemove is one taken off it.
	ActionDomainRemove Action = "domain.remove"
	// ActionGrantWrite is a grant the workspace wrote into a member's roster
	// (D2818's mark, recorded here).
	ActionGrantWrite Action = "grant.write"
	// ActionGrantRemove is one the workspace took back out of it.
	ActionGrantRemove Action = "grant.remove"
	// ActionProjectAdd is a project added to the workspace.
	ActionProjectAdd Action = "project.add"
	// ActionProjectRemove is one removed from it, with the grants and the
	// bindings that went with it (D2822).
	ActionProjectRemove Action = "project.remove"
	// ActionProjectLink is a member's owner linking their own project to the
	// workspace.
	ActionProjectLink Action = "project.link"
	// ActionProjectUnlink is the same owner taking it back out (D2819).
	ActionProjectUnlink Action = "project.unlink"
)

// Actions is the closed set of actions, and the list
// schemas/workspace-audit-event.schema.json's enum is pinned to.
func Actions() []Action {
	return []Action{
		ActionInit,
		ActionUserAdd,
		ActionUserRemove,
		ActionUserOffboard,
		ActionAdminAdd,
		ActionAdminRemove,
		ActionDomainAdd,
		ActionDomainRemove,
		ActionGrantWrite,
		ActionGrantRemove,
		ActionProjectAdd,
		ActionProjectRemove,
		ActionProjectLink,
		ActionProjectUnlink,
	}
}

// Person is one person of the workspace.
type Person struct {
	// Address is the person's address in its canonical spelling
	// (CanonicalAddress): a human's email address, or a service account's
	// address, which is the one an IAM binding names.
	Address string
	// Kind is whether the person is a human or a machine.
	Kind Kind
	// External is D2812's mark: the person was admitted while their domain was
	// not one of the workspace's allowed domains. It is written on every
	// person, false included, because it is a statement about how somebody was
	// admitted and not a default a reader may assume -- a reader that decided
	// outsiderness from the mark alone would be trusting a field an admin can
	// rewrite, which is why the drift comparison computes it from Domains.
	External bool
	// Roles are the per-project roles the workspace expects a MACHINE to hold
	// (D2806). It is empty for a human -- a person's roles are grants in the
	// members' own rosters, and the roster of a member is the only place they
	// are written -- and for a machine it is the expectation `status` compares
	// against the live IAM, so it is the workspace's plan and not a grant.
	Roles []ProjectRole
}

// ProjectRole is one project a machine is expected to hold a role in.
type ProjectRole struct {
	Project string
	Role    Role
}

// StoredGrant is one grant the workspace wrote into a member's own roster
// (D2818's mark). It is the workspace's record of what it wrote, never the
// grant itself: the member's roster holds the real one, and the two are
// compared by the drift check, which is how a grant an admin of the member
// removed by hand is seen.
type StoredGrant struct {
	Project string
	User    string
	Role    Role
}

// MirrorEntry is one person as the member's own mirror holds them (D2813): the
// member list written into each member's database at
// `config/admin/workspace-members`, so a member can tell its own people what
// the workspace says about them without holding a grant on the workspace's
// project at all.
//
// The document that holds these entries is the member-side contract's (C2,
// internal/workspace/memberops.go) and this type is only the entry; its keys
// are the wire spelling of one entry, decoded strictly by the same
// decoder-with-DisallowUnknownFields as the document around it. The mirror is
// ADVISORY and writable by the member's own admins, so nothing acts on it: it
// is what the drift comparison reads to say that a member's copy has been
// rewritten, and the workspace's own roster stays the record (D2813's fence).
type MirrorEntry struct {
	Address  string `json:"address"`
	Kind     Kind   `json:"kind"`
	External bool   `json:"external"`
}

// Validate holds a mirror entry to the rules its document cannot state: the
// same address and kind a roster's person carries.
func (m MirrorEntry) Validate() error {
	if err := validatePerson("the mirror entry", Person{Address: m.Address, Kind: m.Kind, External: m.External}); err != nil {
		return err
	}
	return nil
}

// Roster is the workspace's roster: the document at
// `workspaceRosters/<name>` (D2817).
type Roster struct {
	// Version is RosterVersion. A node that states another version is a node a
	// newer Fugaro wrote and is refused, not read for the keys this build
	// knows.
	Version int
	// Mechanism is how the workspace holds its members.
	Mechanism Mechanism
	// FolderID is the folder the workspace created and owns, as
	// `folders/<number>`, and is empty under the service-account mechanism:
	// the two go together, so a folder id on a service-account workspace is
	// refused rather than ignored.
	FolderID string
	// People are the people of the workspace, sorted by address and each once.
	People []Person
	// Admins are the workspace's admins, sorted by address and each once, and
	// each of them a person of this roster. An entry here is a claim about a
	// binding that lives in IAM and not a permission (D2823): only an entry
	// whose live binding the mechanism's own check confirms is trusted.
	Admins []string
	// Domains are the workspace's allowed email domains (D2812), folded to
	// lower case, sorted and each once. A person from outside them is admitted
	// with the external mark.
	Domains []string
	// Grants are the grants the workspace wrote into its members' rosters
	// (D2818's mark), sorted by project and then by user. It is nil for a
	// workspace that has written none: the database prunes an empty list, so
	// the absent key and the empty list are one and the same thing.
	Grants []StoredGrant
}

// Validate holds a roster to the rules its shape cannot carry. It is what a
// caller that built a roster itself calls before writing one -- every write
// command of this plan is that caller -- and DecodeRoster and EncodeRoster both
// call it, so a node written by this package and a node read by it are held to
// one rule.
//
// The rules the schema also states are restated here rather than trusted to it:
// a roster reaches a writer through Go and through a hand-edited node, and only
// one of those two passes the schema.
func (r *Roster) Validate() error {
	var errs []error
	if r.Version != RosterVersion {
		errs = append(errs, fmt.Errorf("version: %d, and this build reads version %d: a node a newer Fugaro wrote is not read as the half of it this one recognizes", r.Version, RosterVersion))
	}
	switch r.Mechanism {
	case MechanismFolder, MechanismSA:
	default:
		errs = append(errs, fmt.Errorf("mechanism: %q is neither %q nor %q", r.Mechanism, MechanismFolder, MechanismSA))
	}
	switch {
	case r.Mechanism == MechanismFolder && !isFolderID(r.FolderID):
		errs = append(errs, fmt.Errorf("folder_id: %q is not folders/<number>, and a folder workspace names the folder it owns", r.FolderID))
	case r.Mechanism != MechanismFolder && r.FolderID != "":
		errs = append(errs, fmt.Errorf("folder_id: %q is written under the %s mechanism, which owns no folder", r.FolderID, r.Mechanism))
	}

	if len(r.People) == 0 {
		errs = append(errs, errors.New("people: the roster names at least one person, and a workspace with nobody in it has no roster to write"))
	}
	addresses := make([]string, 0, len(r.People))
	byAddress := make(map[string]Person, len(r.People))
	for i, p := range r.People {
		at := fmt.Sprintf("people[%d]", i)
		errs = append(errs, validatePerson(at, p))
		addresses = append(addresses, p.Address)
		byAddress[p.Address] = p
	}
	errs = append(errs, sortedUnique("people", addresses)...)

	if len(r.Admins) == 0 {
		errs = append(errs, errors.New("admins: the roster names at least one admin, and a workspace nobody administers is one `workspace init` has not finished writing"))
	}
	for i, a := range r.Admins {
		at := fmt.Sprintf("admins[%d]", i)
		if err := validateAddress(at, a); err != nil {
			errs = append(errs, err)
			continue
		}
		person, ok := byAddress[a]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: %s is not a person of this roster; an admin entry names somebody the roster already holds", at, a))
		case person.Kind != KindHuman:
			errs = append(errs, fmt.Errorf("%s: %s is a machine, and a machine is never a workspace admin: the binding D2823 confirms an admin by is one a machine cannot hold for a person", at, a))
		}
	}
	errs = append(errs, sortedUnique("admins", r.Admins)...)

	for i, d := range r.Domains {
		if !isDomain(d) {
			errs = append(errs, fmt.Errorf("domains[%d]: %q is not a domain name in its folded spelling; a workspace's allowed domains are compared against the domain of an address, so they are written the way that comparison spells them", i, d))
		}
	}
	errs = append(errs, sortedUnique("domains", r.Domains)...)

	for i, g := range r.Grants {
		errs = append(errs, validateStoredGrant(fmt.Sprintf("grants[%d]", i), g, byAddress))
	}
	errs = append(errs, sortedUniqueGrants(r.Grants)...)

	return errors.Join(errs...)
}

// validatePerson holds one person to the rules of its shape: the address in its
// canonical spelling, the kind, the external mark that belongs to a person and
// not to a machine, and the two halves of the roles rule -- a machine's roles
// are its own, and a human's are written somewhere else.
func validatePerson(at string, p Person) error {
	var errs []error
	if err := validateAddress(at+".address", p.Address); err != nil {
		errs = append(errs, err)
	}
	switch p.Kind {
	case KindHuman:
		if len(p.Roles) > 0 {
			errs = append(errs, fmt.Errorf("%s.roles: %s is a human, and the per-project roles of this node are a machine's expected ones (D2806); a person's roles are grants in the members' own rosters", at, p.Address))
		}
	case KindSA:
		if p.External {
			errs = append(errs, fmt.Errorf("%s.external: %s is a machine, and the external mark is about the domain a person was admitted from (D2812); a machine is admitted by the workspace's own decision and comes from no domain", at, p.Address))
		}
		projects := make([]string, 0, len(p.Roles))
		for j, pr := range p.Roles {
			roleAt := fmt.Sprintf("%s.roles[%d]", at, j)
			if !config.ProjectNameRE.MatchString(pr.Project) {
				errs = append(errs, fmt.Errorf("%s.project: %q is not a project name", roleAt, pr.Project))
			}
			if !slices.Contains(Roles(), pr.Role) {
				errs = append(errs, fmt.Errorf("%s.role: %q is not one of %v", roleAt, pr.Role, Roles()))
			}
			projects = append(projects, pr.Project)
		}
		errs = append(errs, sortedUnique(at+".roles", projects)...)
	default:
		errs = append(errs, fmt.Errorf("%s.kind: %q is neither %q nor %q", at, p.Kind, KindHuman, KindSA))
	}
	return errors.Join(errs...)
}

// validateStoredGrant holds one stored grant to the rules of its shape: a
// project name, a role, and a user who is one of the workspace's own people --
// and a human, because the workspace writes no grant for a machine (D2806: a
// machine's roles are the node's expectation and the workspace owner's IAM, and
// never a member roster).
func validateStoredGrant(at string, g StoredGrant, people map[string]Person) error {
	var errs []error
	if !config.ProjectNameRE.MatchString(g.Project) {
		errs = append(errs, fmt.Errorf("%s.project: %q is not a project name", at, g.Project))
	}
	if !slices.Contains(Roles(), g.Role) {
		errs = append(errs, fmt.Errorf("%s.role: %q is not one of %v", at, g.Role, Roles()))
	}
	if err := validateAddress(at+".user", g.User); err != nil {
		return errors.Join(append(errs, err)...)
	}
	person, ok := people[g.User]
	switch {
	case !ok:
		errs = append(errs, fmt.Errorf("%s.user: %s is not a person of this roster, and the mark records grants the workspace wrote for its own people; taking a person off the roster takes their grants with them", at, g.User))
	case person.Kind != KindHuman:
		errs = append(errs, fmt.Errorf("%s.user: %s is a machine, and the workspace writes no grant for a machine (D2806)", at, g.User))
	}
	return errors.Join(errs...)
}

// validateAddress holds an address to the one spelling a node carries:
// CanonicalAddress's. A node is read by a drift comparison and by a member's
// own roster, and two spellings of one address are two people to every
// comparison that has no rule to fold them.
func validateAddress(at, address string) error {
	canonical, err := CanonicalAddress(address)
	if err != nil {
		return fmt.Errorf("%s: %v", at, err)
	}
	if canonical != address {
		return fmt.Errorf("%s: %q is not the canonical spelling %q, which is the spelling a node carries", at, address, canonical)
	}
	return nil
}

// sortedUniqueGrants reports the grants that repeat or stand out of order. A
// grant is keyed by the PAIR -- project first, then user -- and never by the
// two joined into one string: a project name may carry a hyphen, and `-` sorts
// before the `/` a joined key would be compared at, so a joined comparison
// holds `acme-app` to sort before `acme` and refuses a list every writer of
// this plan sorted the documented way.
func sortedUniqueGrants(grants []StoredGrant) []error {
	var errs []error
	for i := 1; i < len(grants); i++ {
		prev, cur := grants[i-1], grants[i]
		switch c := cmp.Or(strings.Compare(prev.Project, cur.Project), strings.Compare(prev.User, cur.User)); {
		case c == 0:
			errs = append(errs, fmt.Errorf("grants[%d]: %s/%s is listed twice", i, cur.Project, cur.User))
		case c > 0:
			errs = append(errs, fmt.Errorf("grants[%d]: %s/%s is out of order after %s/%s; the list is sorted by project and then by user", i, cur.Project, cur.User, prev.Project, prev.User))
		}
	}
	return errs
}

// sortedUnique reports the entries of a key list that repeat or stand out of
// order. Every list of this document is a set written sorted, which is the one
// rule JSON Schema cannot state -- it has uniqueItems and no ordering keyword,
// and even uniqueness is stated here rather than there because the two have to
// agree about a document an editor wrote by hand.
func sortedUnique(at string, keys []string) []error {
	var errs []error
	for i := 1; i < len(keys); i++ {
		switch prev := keys[i-1]; {
		case keys[i] == prev:
			errs = append(errs, fmt.Errorf("%s[%d]: %s is listed twice", at, i, keys[i]))
		case keys[i] < prev:
			errs = append(errs, fmt.Errorf("%s[%d]: %s is out of order after %s; the list is sorted", at, i, keys[i], prev))
		}
	}
	return errs
}

// domainRE is the grammar of an allowed domain in its folded spelling: labels
// of letters, digits and inner hyphens, joined by dots. It is deliberately not
// the Internet's whole grammar -- an address that reaches this program has been
// through Google's own validation -- and it is a shape, not a resolution: a
// domain here is a string compared against the domain of an address.
var domainRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)

// MaxDomainBytes is the DNS name limit, and the length an allowed domain is
// held to: nothing longer can be a domain, so nothing longer is stored as one.
const MaxDomainBytes = 253

// CanonicalAddress folds an address to the one spelling a node carries: the
// local part as it is written, the domain in lower case. It is
// internal/access.Member's rule without the `user:` prefix the member roster
// carries, and it is restated here rather than imported because the dependency
// runs the other way (internal/access imports internal/projectpolicy, which
// imports this package).
//
// What it refuses is the member roster's list, and the reason is the same: an
// address is a value two sides have to spell the same way, so whitespace, a
// control byte, DEL, every non-ASCII byte, a second `@` and an empty local part
// or domain are all refused rather than folded.
func CanonicalAddress(address string) (string, error) {
	if address == "" {
		return "", errors.New("the address is empty")
	}
	for i := 0; i < len(address); i++ {
		if address[i] < 0x21 || address[i] > 0x7e {
			return "", fmt.Errorf("the address carries %#x at offset %d: whitespace, a control byte, DEL and every non-ASCII byte are refused, because an address is a value two sides have to spell the same way", address[i], i)
		}
	}
	if strings.Count(address, "@") != 1 {
		return "", fmt.Errorf("the address %q does not carry exactly one %q", address, "@")
	}
	at := strings.IndexByte(address, '@')
	local, domain := address[:at], address[at+1:]
	if local == "" || domain == "" {
		return "", fmt.Errorf("the address %q has an empty local part or domain", address)
	}
	return local + "@" + strings.ToLower(domain), nil
}

// isFolderID reports whether an id is a folder's: the same grammar a recorded
// parent is held to (folderParentRE, internal/workspace/authority_folder.go),
// cut to the folders this program creates and never an organization.
func isFolderID(id string) bool {
	return strings.HasPrefix(id, "folders/") && folderParentRE.MatchString(id)
}

// isDomain reports whether a domain is written in its folded spelling.
func isDomain(domain string) bool {
	return len(domain) <= MaxDomainBytes && domainRE.MatchString(domain)
}

// rosterDoc is the roster as the node writes it: the shape
// schemas/workspace-roster.schema.json describes, and the only place the wire
// spellings of the contract appear. It is a type of its own so the document's
// spelling and the Go type can be read against each other in one place -- the
// two are held to one another by TestRosterSchemaRoundTrip -- and so the keys
// are written by one declaration rather than by struct tags spread over the
// types every task passes around.
//
// The three lists that are there only when they hold something are written with
// omitempty, which is the form the database keeps anyway: an empty list is
// pruned on the way in, so the key's absence is the empty list and a writer that
// sent `[]` would write a node it does not read back byte for byte. People and
// admins are always written: a roster has at least one of each, and a reader
// that found neither would have to decide whether the node was empty or the
// writer was.
type rosterDoc struct {
	Version   int            `json:"version"`
	Mechanism string         `json:"mechanism"`
	FolderID  string         `json:"folder_id,omitempty"`
	People    []rosterPerson `json:"people"`
	Admins    []string       `json:"admins"`
	Domains   []string       `json:"domains,omitempty"`
	Grants    []rosterGrant  `json:"grants,omitempty"`
}

type rosterPerson struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`
	// External is a pointer so a key left out can be told from a mark written
	// false, and carries no omitempty so the mark is always written: it is
	// D2812's statement about how somebody was admitted, and a reader that
	// filled in false for a node that never stated it would be answering a
	// question the node did not. The schema requires it, and this is the other
	// half of that requirement -- the reader has to refuse the key's absence,
	// or a hand-written node would be read as a statement nobody made.
	External *bool               `json:"external"`
	Roles    []rosterProjectRole `json:"roles,omitempty"`
}

type rosterProjectRole struct {
	Project string `json:"project"`
	Role    string `json:"role"`
}

type rosterGrant struct {
	Project string `json:"project"`
	User    string `json:"user"`
	Role    string `json:"role"`
}

// DecodeRoster reads a roster document. It decodes strictly -- an unknown field
// anywhere, a second JSON value and a shape that is not an object are all
// errors -- and holds the result to Validate, so a document this package reads
// is one it would have written.
//
// The absent lists read as the empty ones (a nil slice in Go), because that is
// what the database's pruning leaves of them: a workspace that lists no domain
// and has written no grant carries neither key.
func DecodeRoster(raw []byte) (*Roster, error) {
	var doc rosterDoc
	if err := decodeStrict(raw, &doc); err != nil {
		return nil, fmt.Errorf("the roster: %w", err)
	}
	r := &Roster{
		Version:   doc.Version,
		Mechanism: Mechanism(doc.Mechanism),
		FolderID:  doc.FolderID,
		Admins:    doc.Admins,
		Domains:   doc.Domains,
	}
	for _, p := range doc.People {
		if p.External == nil {
			return nil, fmt.Errorf("people[%d].external: the person's entry states no external mark, and the mark is written on every person: a node read with it filled in would be read as a statement about how somebody was admitted that nothing made", len(r.People))
		}
		person := Person{Address: p.Address, Kind: Kind(p.Kind), External: *p.External}
		for _, pr := range p.Roles {
			person.Roles = append(person.Roles, ProjectRole{Project: pr.Project, Role: Role(pr.Role)})
		}
		r.People = append(r.People, person)
	}
	for _, g := range doc.Grants {
		r.Grants = append(r.Grants, StoredGrant{Project: g.Project, User: g.User, Role: Role(g.Role)})
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// EncodeRoster renders a roster as the document the node holds. It validates
// first, so a writer cannot store a document its own reader refuses -- which is
// what makes a write command's refusal name the roster it was about to write
// rather than the node it left behind.
func EncodeRoster(r *Roster) ([]byte, error) {
	if r == nil {
		return nil, errors.New("the roster is nil")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	doc := rosterDoc{
		Version:   r.Version,
		Mechanism: string(r.Mechanism),
		FolderID:  r.FolderID,
		Admins:    r.Admins,
		Domains:   r.Domains,
	}
	for _, p := range r.People {
		person := rosterPerson{Address: p.Address, Kind: string(p.Kind), External: &p.External}
		for _, pr := range p.Roles {
			person.Roles = append(person.Roles, rosterProjectRole{Project: pr.Project, Role: string(pr.Role)})
		}
		doc.People = append(doc.People, person)
	}
	// A list with nothing in it is left out rather than written empty, so what
	// this writes is what the node keeps: the database prunes an empty list,
	// and a document that changed shape on the way in would make every later
	// comparison against the bytes a comparison against something else.
	for _, g := range r.Grants {
		doc.Grants = append(doc.Grants, rosterGrant{Project: g.Project, User: g.User, Role: string(g.Role)})
	}
	return json.Marshal(doc)
}

// AuditEvent is one entry of the workspace's audit trail (D2811): what was
// done, by whom, to whom, and when. The trail is create-only -- one document
// per event, never updated (internal/workspace/audit_firestore.go) -- so an
// event is a statement about the past and nothing here is ever rewritten.
//
// The event is stored in the workspace's own Firestore, under its own project,
// and a member's own trail is a separate node of that member's database (C2).
// The two are the same document, which is why this type and its schema live
// here rather than at either writer.
type AuditEvent struct {
	// Version is AuditEventVersion.
	Version int
	// ID is the event's own id (NewAuditEventID): a Firestore document id and
	// a database key at once, and the thing that makes a repeat a refusal.
	ID string
	// Time is when the change was made, to the second it was made in. It is a
	// time.Time here and an RFC 3339 string on the wire, so no reader parses it
	// twice and no node holds an instant nothing can read back.
	Time time.Time
	// Actor is the address of whoever made the change.
	Actor string
	// Action is what they did.
	Action Action
	// Project is the member the change was about, when it was about one: a
	// grant written, a person offboarded from it. Empty for a change that names
	// no member, and then the key is left out rather than written empty.
	Project string
	// Subject is what the change was made to: an address, a domain, a project.
	// Empty for a change with no subject, and then the key is left out.
	Subject string
	// Role is the role a grant carried, when the change wrote one.
	Role Role
}

// Validate holds an event to the rules its shape cannot carry: the version, the
// id both stores accept, the instant, the actor, the action of the closed set,
// and the project and role when they are there.
func (e AuditEvent) Validate() error {
	var errs []error
	if e.Version != AuditEventVersion {
		errs = append(errs, fmt.Errorf("version: %d, and this build writes and reads version %d", e.Version, AuditEventVersion))
	}
	if err := validateAuditEventID(e.ID); err != nil {
		errs = append(errs, err)
	}
	if e.Time.IsZero() {
		errs = append(errs, errors.New("time: an audit entry states when the change was made, and the zero instant is not a time it was made at"))
	}
	if err := validateAddress("actor", e.Actor); err != nil {
		errs = append(errs, err)
	}
	if !slices.Contains(Actions(), e.Action) {
		errs = append(errs, fmt.Errorf("action: %q is not one of %v", e.Action, Actions()))
	}
	if e.Project != "" && !config.ProjectNameRE.MatchString(e.Project) {
		errs = append(errs, fmt.Errorf("project: %q is not a project name", e.Project))
	}
	if e.Role != "" && !slices.Contains(Roles(), e.Role) {
		errs = append(errs, fmt.Errorf("role: %q is not one of %v", e.Role, Roles()))
	}
	return errors.Join(errs...)
}

// auditEventDoc is the event as the store writes it: the shape
// schemas/workspace-audit-event.schema.json describes. Its own type for the same
// reason the roster has one, and for one more: the wire writes the instant as
// RFC 3339 and the type holds it parsed.
type auditEventDoc struct {
	Version int       `json:"version"`
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Project string    `json:"project,omitempty"`
	Subject string    `json:"subject,omitempty"`
	Role    string    `json:"role,omitempty"`
}

// DecodeAuditEvent reads an audit event. It decodes strictly and holds the
// event to Validate, as the roster's reader does and for the same reason: the
// trail is read back by `status`, by the drift check and by whoever is
// answering what happened, and an event only half understood is an event none
// of them may act on.
//
// The three optional strings are read as they are written: an actor with no
// project, no subject and no role leaves the keys out, and a key written empty
// reads as the absence it means. Nothing writes an event back -- the trail is
// create-only (D2811) -- so the empty spelling is never produced by this
// program, and the schema refuses it where an editor would be the one writing.
func DecodeAuditEvent(raw []byte) (AuditEvent, error) {
	var doc auditEventDoc
	if err := decodeStrict(raw, &doc); err != nil {
		return AuditEvent{}, fmt.Errorf("the audit event: %w", err)
	}
	event := AuditEvent{
		Version: doc.Version,
		ID:      doc.ID,
		Time:    doc.Time,
		Actor:   doc.Actor,
		Action:  Action(doc.Action),
		Project: doc.Project,
		Subject: doc.Subject,
		Role:    Role(doc.Role),
	}
	if err := event.Validate(); err != nil {
		return AuditEvent{}, err
	}
	return event, nil
}

// EncodeAuditEvent renders an event as the document the trail stores. It
// validates first, so a caller cannot create an entry its own reader refuses:
// the entry is created once and never updated, so an unreadable one is
// unreadable for good.
func EncodeAuditEvent(e AuditEvent) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(auditEventDoc{
		Version: e.Version,
		ID:      e.ID,
		Time:    e.Time,
		Actor:   e.Actor,
		Action:  string(e.Action),
		Project: e.Project,
		Subject: e.Subject,
		Role:    string(e.Role),
	})
}

// auditIDRE is the grammar of an event id: printable ASCII except the six
// characters a Realtime Database key refuses (`.`, `#`, `$`, `/`, `[`, `]`), at
// most 200 bytes. An event id is a Firestore document id and a database key at
// once -- a member's own trail is a node of that member's database (C2) -- so it
// carries nothing either store refuses. Firestore's other rule, that a name
// beginning `__` is reserved, is not a pattern: it is the prefix check beside
// this one.
var auditIDRE = regexp.MustCompile(`^[\x21-\x22\x25-\x2d\x30-\x5a\x5c\x5e-\x7e]{1,200}$`)

// MaxAuditIDBytes is the length an event id is held to. Firestore's own limit
// on a document id is 1500 bytes, and a Realtime Database key's is 768; the
// first is the smaller of the two and the id this package mints is 34 bytes, so
// the bound is generous for a minted id and a fence for a written one.
const MaxAuditIDBytes = 200

// validateAuditEventID holds an id to the grammar above and to Firestore's
// reserved prefix.
func validateAuditEventID(id string) error {
	if !auditIDRE.MatchString(id) {
		return fmt.Errorf("id: %q is not an event id: printable ASCII without `.`, `#`, `$`, `/`, `[` or `]` (which a database key refuses), between 1 and %d bytes", id, MaxAuditIDBytes)
	}
	if strings.HasPrefix(id, "__") {
		return fmt.Errorf("id: %q begins with `__`, which Firestore reserves for its own documents", id)
	}
	return nil
}

// NewAuditEventID mints the id of an event made at now, reading randomness from
// r. The id opens with the instant in UTC, so a listing of the collection or of
// a member's trail in key order is a listing in time order to the second, and
// ends with eight random bytes, so two events made in the same second are two
// documents rather than one create-only refusal.
//
// A zero instant is refused rather than minted: it is a clock that was never
// read, and an id that names the year 1 would sort every event under a node's
// other documents for good. r is a parameter so a test can hold the randomness
// still, and a nil one is refused rather than panicking.
func NewAuditEventID(now time.Time, r io.Reader) (string, error) {
	if now.IsZero() {
		return "", errors.New("the event's time is the zero instant, which is a clock nothing read: an id names the instant its event was made at")
	}
	if r == nil {
		return "", errors.New("no source of randomness: two events made in one second would then be one document, and the second would be a refusal")
	}
	var suffix [8]byte
	if _, err := io.ReadFull(r, suffix[:]); err != nil {
		return "", fmt.Errorf("reading the randomness an event id ends with: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

// decodeStrict reads one JSON document into v: an unknown field anywhere is an
// error, and so is a second JSON value after the first. Every stored document
// of this package decodes this way (DecodeFolderLink is the other), because a
// key nobody decided is a shape two writers disagree about -- the one that put
// it there and the reader with no rule for it -- and a document with a second
// value under it is a document that means two things.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("the document is empty")
		}
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the document carries more than one JSON value")
	}
	return nil
}
