package workspace

// drift.go is WU12's comparison: what a member's live IAM carries against what
// the workspace node records, and what Cloud Audit Logs saw that no trail of
// Fugaro's holds. It is what `workspace project status` prints as a row and what
// `workspace project reconcile` (WU13) acts on, and it is the fence D2813 names
// where the mirror is only advisory.
//
// The record it compares against is the WORKSPACE NODE and never the mirror
// (D2813). The member's mirror, `config/admin/workspace-members`, is written
// into that member's own database, so the member's own admins hold its write and
// can rewrite it: an address added there lands a grant for somebody the workspace
// never admitted, and one removed hides a person the workspace did. A comparison
// made from it would be a decision any project admin could make, which is the
// whole reason the workspace node exists. `DriftSources.Mirror` is carried in
// and deliberately not read, so that the fence is a property a test holds
// (`TestDriftIgnoresARewrittenMirror`) rather than a convention this comment
// states.
//
// Three sides, and each of them is a set of statements about (project,
// principal, role):
//
//   - the RECORD, from the workspace node: the grants the workspace wrote into
//     this member's roster (`Grants`, D2818's mark, which names one role per
//     project and person) and, for a machine, the per-project roles the node
//     records for it (D2806). A grant stored for a workspace ADMIN counts only
//     while that admin holds the live binding the mechanism gives an admin
//     (D2823, `Authority.AdminBindings`): the entries that can rewrite `Admins`
//     cannot set that binding, so an entry without one is a claim and not an
//     expectation -- which is also why its stored grants are neither expected
//     here nor re-added by `reconcile`;
//   - the MEMBER'S OWN ROSTER, decoded by the caller and handed in: this package
//     cannot read that format (internal/access reaches it through
//     internal/projectpolicy) and a second reader of it would be a second
//     spelling of a wire format two CLIs share (C2's contract, §3). It is one
//     list for both of the member's projects -- its own roots derive each of
//     them from it (F2, R10) -- so a person it names explains a binding on
//     either, while the grant the record is compared against stays on the one
//     project the node keyed it to;
//   - the MEMBER'S LIVE IAM, placed by WU4's `Bindings` (D2806, F17).
//
// EXTRA is a statement the member holds that the record does not state: a roster
// grant a member's own admin wrote by hand, or a binding its policy carries that
// neither the record nor its roster explains. Those are the two shapes D2805
// calls "unmapped bindings reported as drift" and D2813 makes the reason the
// installation-level commands refuse on a linked member. MISSING is a statement
// the RECORD makes that the member's policy does not carry -- which is a workspace
// admin's stored grant waiting for the member's own `fugaro update` (D2801,
// D2818) as much as a grant an admin removed.
//
// A person is present or absent, and the ROLE is reported as the side that
// stated it spells it. That is deliberate and it is not a shortcut: Fugaro's
// three roles each map to several IAM roles and the member's own Terraform is
// what applies them (`docs/roles/spec.md` §1-§2, D2801), so a role-by-role
// comparison would report every healthy member as drifted. What a machine's
// entry carries is the role the node RECORDS for it -- the plan D2806 says
// `status` compares against the live IAM -- and the report keeps it there.
//
// Presence is not the whole of it, though, and the exception is the one role
// RULE this comparison makes: a binding that comes with `roles/owner`,
// `roles/editor` or the budget admin's role makes its holder an admin of the
// member whatever else is true (D689, threat-model.md §3), and those are an
// admin's roles -- `docs/roles/spec.md` §2 gives an admin `roles/owner` and
// `roles/editor` and the budget admin's write, and gives a viewer or an operator
// none of them. So a principal the record states as viewer or operator that holds
// one of them anyway is the member's admin with a row that does not say so, and
// it is drift even though the record already names the person.
//
// Fugaro's own accounts are never drift (D2806, F17, R13): a binding placed
// `PrincipalTerraform` (the member's job, build and history accounts) or
// `PrincipalWorkspaceIAM` (`workspace_iam_account`, whose `roles/owner` is the
// SA mechanism itself and whose Firebase admin binding is what the member's own
// `update` derives from the project's owners) is dropped before anything is
// compared. `PrincipalUnknown` is NOT dropped: a principal nobody has placed is
// somebody else's binding and belongs in the report (C2's contract, §3).
//
// Outsiderness comes from the node's `Domains` and never from a person's
// `external` mark, which an admin of the workspace can rewrite (C1's contract,
// §"a person is a human or a machine"). A binding that comes with an account
// rather than from a grant -- `roles/owner`, `roles/editor` and
// `roles/firebasedatabase.admin` on a member's project -- is reported with
// `Implicit` set: those principals hold the member's database write and are its
// admins de facto (threat-model.md §3, D689), so a reader names them instead of
// counting them as something to remove.
//
// The last part is the cross-check Cloud Audit Logs make possible (D2811, R13):
// the Firestore trails are as tamperable as the rosters they record, so a
// SetIamPolicy whose resulting binding is drift and which neither trail holds is
// named as a console change. A member's own `fugaro update` is never one of
// them: what it applies is the record, so its bindings are stated and not drift
// at all. What no reader can do here is decide what a change MEANT -- the entry
// carries who made it, when and which bindings it added, and nothing about why.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// DriftAuditWindow is how far back the Cloud Audit Logs cross-check reads when
// the caller names no start: thirty days, which is Admin Activity's retention in
// a project that has not changed it. A longer window is the caller's to ask for
// (`DriftSources.Since`); unbounded is not offered, because Cloud Logging scans
// the whole retention for a filter with no bound.
const DriftAuditWindow = 30 * 24 * time.Hour

// driftTrailWindow is how far from a logged change an audit event of Fugaro's
// own may sit and still be read as recording it. The two are written by
// different systems at different moments -- the trail by the CLI after the change
// it made, the log by Google as it happened -- so the match cannot be exact, and
// the window is deliberately generous in the direction that avoids accusing
// Fugaro's own change of being somebody else's.
const driftTrailWindow = 30 * time.Minute

// The roles the workspace-side bindings of R13 are held at, and the roles that
// make a principal an implicit admin of a member. They are restated here rather
// than imported, as Role and CanonicalAddress are: internal/infra names this
// package's types (WU2's Folders, and WU12's SetIamPolicyLog here), so this
// package cannot name its, and each of these is an IAM spelling that the member's
// own Terraform is what applies.
const (
	// driftFolderOwnerRole is roles/owner on the workspace's folder, inherited by
	// every project in it: a folder owner holds it on each member, which is what
	// makes a folder owner who is not a bound admin a finding (D2823, R13).
	driftFolderOwnerRole = "roles/owner"
	// driftTokenCreatorRole is roles/iam.serviceAccountTokenCreator on the
	// workspace's own IAM account: the SA mechanism's admin binding (D2803, R8).
	driftTokenCreatorRole = "roles/iam.serviceAccountTokenCreator"
)

// driftImplicitAdminRoles are the roles that come with an account rather than
// from a grant, on a member's projects: a project's owners and editors hold its
// database write, and so does a budget admin (threat-model.md §3, D689,
// `docs/roles/spec.md` §1).
var driftImplicitAdminRoles = []string{"roles/owner", "roles/editor", "roles/firebasedatabase.admin"}

// MemberGrant is one grant of the MEMBER's own roster, as the caller read it
// from internal/access. It is deliberately not that package's `Grant`: naming it
// here would be the import cycle C2's contract describes, and a second reader of
// the node would be a second spelling of a format two CLIs share.
type MemberGrant struct {
	// User is the member's address, in CanonicalAddress's spelling.
	User string
	// Role is the role the member's roster grants at project scope. A grant at
	// another scope is the member's own business and drift does not read it.
	Role Role
}

// SetIamPolicyChange is one IAM change Cloud Audit Logs records: the entry a
// person can look up, when Google logged it, who made it, and one binding the
// change ADDED. internal/infra/auditlogs.go returns these, and the shape is
// declared here for the arrangement WU2's Folders has: the read's consumer states
// what it reads, the provider implements it, and I1 hands one in with no adapter.
//
// A removal is not one of these: a change's REMOVED binding is an absence and
// not a binding, and drift's question is what the member's policy was left
// carrying.
type SetIamPolicyChange struct {
	// Project is the project whose policy the change was made on.
	Project string
	// Principal is who made the change.
	Principal string
	// Member is the binding that was added, as IAM spells it (`user:`, a
	// service account's address, a group or a domain).
	Member string
	// Role is the role that was added.
	Role string
	// Entry names the Cloud Audit Logs entry the change was read from, so a
	// person can find it in Google's console.
	Entry string
	// At is when Google logged it.
	At time.Time
}

// SetIamPolicyLog reads a project's Admin Activity log for the policy changes
// made to it. internal/infra's AuditLogs is the implementation and satisfies this
// interface directly, so I1 wires it in as it wires WU2's Folders.
//
// A caller that has none passes nil, and drift says so in `Unmeasured` rather
// than reporting a cross-check it never made -- the one answer that would let an
// empty report read as "in sync".
type SetIamPolicyLog interface {
	SetIamPolicyChanges(ctx context.Context, project string, since time.Time) ([]SetIamPolicyChange, error)
}

// DriftSources is everything `ComputeDrift` compares, in one value: it is one
// read with one answer (C2's `Drift`), and the sides it needs come from four
// places that no one of them can reach.
type DriftSources struct {
	// Workspace is the workspace's own project name, which is its name (F21). It
	// is where the SA mechanism's admin bindings live: the workspace IAM account
	// sits in that project, and its token creators are bound on the account
	// (D2803, R8).
	Workspace string
	// Node is the workspace node: the record (D2813). It is required, and a
	// caller with none has nothing to compare against.
	Node *Roster
	// Authority is the mechanism, read for `Kind` and for `AdminBindings`, which
	// is the live binding an `Admins` entry is trusted by (D2823). It is
	// required: without it every stored admin grant would be trusted, which is
	// the trust D2823 removes.
	Authority Authority
	// Grants is the MEMBER's own roster as the caller read it; its statements
	// are the member's GCP project's, which is the project whose database holds
	// that roster.
	Grants []MemberGrant
	// Bindings is the member's live IAM, placed by WU4's `Bindings`, for both of
	// the member's projects (F2, R10).
	Bindings []MemberBinding
	// Mirror is the member's mirror. It is CARRIED AND NEVER READ: D2813 makes
	// it advisory, and a comparison against it would be one the member's own
	// admins can make.
	Mirror []MirrorEntry
	// Trail is both trails' events -- the workspace's and the member's -- which
	// is what the Cloud Audit Logs cross-check asks whether a change is held by
	// (D2811).
	Trail []AuditEvent
	// Changes reads Cloud Audit Logs. nil is a caller with no reader, which is
	// reported as unmeasured.
	Changes SetIamPolicyLog
	// Since bounds the audit-log read's window. The zero time is
	// `DriftAuditWindow` before Now.
	Since time.Time
	// Now is what "now" is for that window, and the zero time means the clock.
	// It is a field so that a test holds the window still.
	Now time.Time
}

// driftPair is what a statement is looked up by: the project and the principal,
// with the role carried beside it. Presence is what drift decides -- see the
// file's own note on why the role is not compared.
type driftPair struct {
	project   string
	principal string
}

// driftStatement is one statement of the record or of the member's own roster: a
// project, a principal, the Fugaro role they hold there, and what the report
// needs to say about them.
type driftStatement struct {
	pair     driftPair
	role     Role
	kind     PrincipalKind
	external bool
}

// driftLive is one binding of the member's live policy, with IAM's member prefix
// folded off so that it is compared as the address a node holds.
type driftLive struct {
	pair driftPair
	role string
	kind PrincipalKind
}

// ComputeDrift compares a member's live IAM with the workspace node and with
// both trails, and answers what differs and what it could not compare.
//
// It is named `ComputeDrift` and not `Drift` because C2 declares that name for
// the function type its consumers hold (memberops.go); I1 closes over the
// sources and adapts this onto it.
func ComputeDrift(ctx context.Context, src DriftSources, m MemberTarget) (DriftReport, error) {
	if src.Node == nil {
		return DriftReport{}, errors.New("workspace: drift compares a member against the workspace node, and no node was given: the mirror is advisory and not a record (D2813)")
	}
	if src.Authority == nil {
		return DriftReport{}, errors.New("workspace: drift needs the workspace's authority: whether an `Admins` entry is trusted is read from the live binding (D2823)")
	}
	if src.Workspace == "" {
		return DriftReport{}, errors.New("workspace: drift names the workspace's own project: the SA mechanism's admin bindings live there (R8)")
	}
	if m.Project == "" {
		return DriftReport{}, errors.New("workspace: drift names the member's GCP project")
	}
	projects := memberProjects(m)

	admins, err := src.Authority.AdminBindings(ctx)
	if err != nil {
		return DriftReport{}, fmt.Errorf("reading the workspace's live admin bindings: %w", err)
	}
	trusted := map[string]bool{}
	for _, a := range admins {
		trusted[driftAddress(a)] = true
	}
	claimed := map[string]bool{}
	for _, a := range src.Node.Admins {
		claimed[driftAddress(a)] = true
	}

	// The record, the member's own roster and the member's policy, each as the
	// set of statements drift looks a principal up in.
	record := map[driftPair]bool{}
	var recordStatements []driftStatement
	// stated is the role each stated principal was given, the record's first and
	// the member's own roster where the record is silent. It is what the implicit
	// admin rule asks about a binding the record already names (see the live check
	// below and consoleChanges).
	stated := map[driftPair]Role{}
	for _, g := range src.Node.Grants {
		if !slices.Contains(projects, g.Project) {
			continue
		}
		// D2823: an admin's stored grant is an expectation only while the admin
		// holds the binding an admin is.
		if claimed[driftAddress(g.User)] && !trusted[driftAddress(g.User)] {
			continue
		}
		p := driftPair{g.Project, driftAddress(g.User)}
		record[p] = true
		stated[p] = g.Role
		recordStatements = append(recordStatements, driftStatement{pair: p, role: g.Role, kind: PrincipalPerson, external: outsideDomains(g.User, src.Node.Domains)})
	}
	for _, person := range src.Node.People {
		if person.Kind != KindSA {
			continue
		}
		for _, pr := range person.Roles {
			if !slices.Contains(projects, pr.Project) {
				continue
			}
			p := driftPair{pr.Project, driftAddress(person.Address)}
			record[p] = true
			stated[p] = pr.Role
			recordStatements = append(recordStatements, driftStatement{pair: p, role: pr.Role, kind: PrincipalMachine})
		}
	}
	roster := map[driftPair]bool{}
	var rosterStatements []driftStatement
	for _, g := range src.Grants {
		p := driftPair{m.Project, driftAddress(g.User)}
		rosterStatements = append(rosterStatements, driftStatement{pair: p, role: g.Role, kind: PrincipalPerson, external: outsideDomains(g.User, src.Node.Domains)})
		// The one roster is what the member's own roots derive BOTH projects'
		// bindings from, so it explains a person on either of them (F2, R10) --
		// without this every healthy member whose Firebase project is a project of
		// its own reports its own people as extra. What is compared against the
		// record stays the one grant's own project (rosterStatements): a grant the
		// node does not state is D2805's unmapped binding on the project it names,
		// not one widened into a second row.
		// The record's role for this person on the member's own GCP project governs
		// the member's Firebase project too: a node grant is keyed to the GCP
		// project, so the roster would otherwise be the only statement there -- and
		// the member's own admins hold that roster's write. A roster they rewrote to
		// turn a viewer into an admin must not be what the implicit admin rule reads
		// on the project the budget admin's role lands on.
		recordRole, recordStates := stated[driftPair{m.Project, driftAddress(g.User)}]
		for _, project := range memberProjects(m) {
			p := driftPair{project, driftAddress(g.User)}
			roster[p] = true
			if _, ok := stated[p]; ok {
				continue
			}
			if recordStates {
				stated[p] = recordRole
				continue
			}
			stated[p] = g.Role
		}
	}
	live := map[driftPair]driftLive{}
	var liveBindings []driftLive
	// Fugaro's own accounts are remembered as their own set before they are
	// dropped: the cross-check below asks whether a change ADDED one of them, and
	// a binding that was dropped from `live` would answer that question with
	// silence (see consoleChanges).
	fugaro := map[driftPair]bool{}
	for _, b := range src.Bindings {
		if b.Kind == PrincipalTerraform || b.Kind == PrincipalWorkspaceIAM {
			fugaro[driftPair{b.Project, driftAddress(b.Principal)}] = true
			continue
		}
		if !slices.Contains(projects, b.Project) {
			continue
		}
		l := driftLive{pair: driftPair{b.Project, driftAddress(b.Principal)}, role: b.Role, kind: b.Kind}
		live[l.pair] = l
		liveBindings = append(liveBindings, l)
	}

	var extra, missing []DriftBinding
	// A grant the member's own roster holds that the record does not state: a
	// change made outside the workspace, which D2805 reports rather than strips.
	for _, st := range rosterStatements {
		if record[st.pair] {
			continue
		}
		extra = append(extra, DriftBinding{
			Project: st.pair.project, Principal: st.pair.principal, Role: string(st.role),
			Kind: st.kind, External: st.external,
		})
	}
	// A binding the member's policy carries that neither the record nor the
	// member's own roster explains -- and one they explain as a viewer's or an
	// operator's while it carries an admin's roles, which names the person and not
	// the authority that person holds over the member (see the file header).
	for _, l := range liveBindings {
		implicit := slices.Contains(driftImplicitAdminRoles, l.role)
		if record[l.pair] || roster[l.pair] {
			if !implicit || stated[l.pair] == RoleAdmin {
				continue
			}
		}
		extra = append(extra, DriftBinding{
			Project: l.pair.project, Principal: l.pair.principal, Role: l.role, Kind: l.kind,
			External: l.kind == PrincipalPerson && outsideDomains(l.pair.principal, src.Node.Domains),
			Implicit: implicit,
		})
	}
	// What the record states and the member's policy does not carry: a stored
	// grant waiting for the member's own `fugaro update`, or one an admin of the
	// member took away.
	for _, st := range recordStatements {
		if _, ok := live[st.pair]; ok {
			continue
		}
		missing = append(missing, DriftBinding{
			Project: st.pair.project, Principal: st.pair.principal, Role: string(st.role),
			Kind: st.kind, External: st.external,
		})
	}

	unmeasured, adminExtra := workspaceAdminDrift(src, m, admins, claimed)
	extra = append(extra, adminExtra...)

	changes, audit := consoleChanges(ctx, src, projects, record, roster, fugaro, stated)
	unmeasured = append(unmeasured, audit...)
	if src.Changes == nil {
		unmeasured = append(unmeasured, "Cloud Audit Logs: no reader was given, so no SetIamPolicy change was compared")
	}

	return DriftReport{
		Extra:          sortedDriftBindings(extra),
		Missing:        sortedDriftBindings(missing),
		Unmeasured:     unmeasured,
		ConsoleChanges: changes,
	}, nil
}

// workspaceAdminDrift reports the workspace-side bindings of R13 and says what
// the mechanism makes unmeasurable.
//
// A holder of the live binding who is not an entry of `Admins` is a finding: the
// folder's owners hold `roles/owner` on every project in the folder, and the
// account's token creators can act as the account that owns them, so either is
// somebody with reach on the member that the record does not name (D2823, R13).
// The entry is reported against the member's own GCP project for the folder
// mechanism, because that is where the inherited owner lands, and against the
// workspace's own project for the SA mechanism, because that is where the account
// and its token creators are (R8).
//
// The other direction -- an `Admins` entry with no live binding -- is not a
// binding and so is not a difference in a report about bindings: it is the
// tampering row `workspace project status` shows (WU11), and what drift does with
// it is what D2823 says, refuse to trust it (see the record above).
func workspaceAdminDrift(src DriftSources, m MemberTarget, admins []string, claimed map[string]bool) ([]string, []DriftBinding) {
	kind := src.Authority.Kind()
	switch kind {
	case AuthorityFolder, AuthoritySA:
	default:
		return []string{fmt.Sprintf("the workspace's admin bindings: the authority reports no mechanism (%q), so what an admin's live binding is is not known", kind)}, nil
	}
	project, role := m.Project, driftFolderOwnerRole
	if kind == AuthoritySA {
		project, role = src.Workspace, driftTokenCreatorRole
	}
	var extra []DriftBinding
	for _, a := range admins {
		address := driftAddress(a)
		if claimed[address] {
			continue
		}
		principal := driftPrincipalKind(a)
		extra = append(extra, DriftBinding{
			Project: project, Principal: address, Role: role, Kind: principal,
			External: principal == PrincipalPerson && outsideDomains(address, src.Node.Domains),
			Implicit: slices.Contains(driftImplicitAdminRoles, role),
		})
	}
	return nil, extra
}

// consoleChanges names the Cloud Audit Logs changes whose resulting binding is
// drift and which no trail of Fugaro's holds (D2811, R13). It reads each of the
// member's projects and answers the entries in the order Google logged them.
//
// A change is NOT named when what it added is a statement of the record or of the
// member's own roster, which is every change a member's own `fugaro update`
// makes: what the update applies is the record (D2801, D2818), so nothing it
// wrote is drift. A change that added something outside both IS drift, and it is
// named whoever made it -- a hand-edited roster applied by an update included,
// which is exactly the unmapped binding D2805 makes visible.
//
// The exception is the implicit admin rule the live check makes (the file
// header): a change that added `roles/owner`, `roles/editor` or the budget
// admin's role to a principal the record states as viewer or operator IS drift,
// so it is named -- and a change that added one of them to a stated ADMIN is
// what that admin's role carries, so it is not.
//
// A change that added one of Fugaro's own accounts is not named either, and
// `fugaro` is the set that answers it: those bindings are dropped from the live
// side before anything is compared, so asking `live` would answer with silence
// and name the member's own plumbing -- its Terraform's build account, the
// workspace's IAM account -- as a change made by hand in Google's console.
//
// A change a trail of Fugaro's holds is not named either: the cross-check exists
// to find what no Fugaro command recorded, and a grant written through the
// workspace's own commands has an event beside it. The two are matched by the
// project, the caller, the address the change was made to and the moment, and
// not by the role, because the trail records Fugaro's roles and the log
// Google's (trailHolds).
//
// A read that fails does not fail the drift: what it answered would have been a
// sample of the window, and reporting nothing at all would lose the Extra and
// Missing the caller asked for. The failure is named in the second result
// instead, one entry per project, which the report carries as Unmeasured -- a
// window nobody read is not a window that was clean.
func consoleChanges(ctx context.Context, src DriftSources, projects []string, record, roster, fugaro map[driftPair]bool, stated map[driftPair]Role) ([]ConsoleChange, []string) {
	if src.Changes == nil {
		return nil, nil
	}
	now := src.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := src.Since
	if since.IsZero() {
		since = now.Add(-DriftAuditWindow)
	}
	var out []ConsoleChange
	var unmeasured []string
	for _, project := range projects {
		changes, err := src.Changes.SetIamPolicyChanges(ctx, project, since)
		if err != nil {
			unmeasured = append(unmeasured, fmt.Sprintf("Cloud Audit Logs of %s: %v", project, err))
			continue
		}
		for _, c := range changes {
			pair := driftPair{c.Project, driftAddress(c.Member)}
			if fugaro[pair] {
				continue
			}
			if record[pair] || roster[pair] {
				// Stated, so the change is the member's own update applying the
				// record -- unless it is what makes a stated viewer or operator one
				// of the member's admins, which the record does not say.
				if !slices.Contains(driftImplicitAdminRoles, c.Role) || stated[pair] == RoleAdmin {
					continue
				}
			}
			if trailHolds(src.Trail, c) {
				continue
			}
			out = append(out, ConsoleChange{
				Project: c.Project, Principal: pair.principal, Role: c.Role, Entry: c.Entry, At: c.At,
			})
		}
	}
	slices.SortStableFunc(out, func(a, b ConsoleChange) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return strings.Compare(a.Principal, b.Principal)
	})
	return out, unmeasured
}

// trailHolds reports whether either of Fugaro's trails records the change: an
// event on the same project, by the same caller, of a role change made to the
// same address, within driftTrailWindow of the moment Google logged it.
//
// The address is what makes the match a match: a trail event states the subject
// a grant named (C1's `Subject`), and a caller who made one grant in the window
// is not a caller who made every change in it. An event that names no subject
// holds nothing here -- the cross-check's whole question is what a change did,
// and an event that does not say is one this reader cannot answer it with.
func trailHolds(events []AuditEvent, c SetIamPolicyChange) bool {
	for _, e := range events {
		if e.Project != c.Project || driftAddress(e.Actor) != driftAddress(c.Principal) {
			continue
		}
		if e.Action != ActionGrantWrite && e.Action != ActionGrantRemove {
			continue
		}
		if e.Subject == "" || driftAddress(e.Subject) != driftAddress(c.Member) {
			continue
		}
		if d := e.Time.Sub(c.At); d >= -driftTrailWindow && d <= driftTrailWindow {
			return true
		}
	}
	return false
}

// memberProjects is the member's own projects: its GCP project, and its Firebase
// project when that is a project of its own (F2, R10). The two are one project
// only sometimes, and a read that reached the wrong one of them would half-cover
// the member.
func memberProjects(m MemberTarget) []string {
	if m.Firebase == "" || m.Firebase == m.Project {
		return []string{m.Project}
	}
	return []string{m.Project, m.Firebase}
}

// driftAddress is the address a statement or a binding names, with IAM's member
// prefix folded off: a node holds an address and a policy holds a member, and the
// two are one value only once they are spelled the same way. An address is folded
// to its canonical spelling where it is one (the node's own rule); a group, a
// domain or a principal set is left as IAM spells it, because a node has no such
// entry and nothing compares it as an address.
func driftAddress(member string) string {
	address := member
	for _, prefix := range []string{"user:", "serviceAccount:", "group:", "domain:", "principal:", "principalSet:"} {
		if rest, ok := strings.CutPrefix(member, prefix); ok {
			address = rest
			break
		}
	}
	if canonical, err := CanonicalAddress(address); err == nil {
		return canonical
	}
	return address
}

// outsideDomains reports whether an address belongs to none of the workspace's
// allowed domains (D2812). It is the reader C1's contract names: outsiderness is
// computed from `Domains` and never from the `external` mark, which an admin can
// rewrite. An address with no domain of its own (a group, a principal set) is not
// an outsider: the question is about a person's domain and there is none to read.
func outsideDomains(address string, domains []string) bool {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return false
	}
	return !slices.Contains(domains, strings.ToLower(address[at+1:]))
}

// driftPrincipalKind is what Fugaro can tell a principal is from the binding
// alone: a Google account is a person, and anything else -- a service account, a
// group, a domain -- is a principal nobody has placed, which a reader must treat
// as somebody else's binding rather than as one to ignore (C2's contract §3).
func driftPrincipalKind(member string) PrincipalKind {
	if strings.HasPrefix(member, "user:") {
		return PrincipalPerson
	}
	return PrincipalUnknown
}

// sortedDriftBindings answers a finding list in the one order a report is read
// in: by project, then by principal, then by role, with an exact repeat listed
// once. Nothing else about the two sides can make the same finding twice, but the
// two sides themselves can: a roster grant and a policy binding are two reads of
// one person, and a reader is owed one row.
func sortedDriftBindings(in []DriftBinding) []DriftBinding {
	slices.SortStableFunc(in, func(a, b DriftBinding) int {
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		if c := strings.Compare(a.Principal, b.Principal); c != 0 {
			return c
		}
		return strings.Compare(a.Role, b.Role)
	})
	return slices.Compact(in)
}
