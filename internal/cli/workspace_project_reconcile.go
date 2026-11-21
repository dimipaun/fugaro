package cli

// workspace_project_reconcile.go is WU13: `workspace project reconcile` (D2809).
//
// Drift is what a workspace admin can see and cannot fix by reading: somebody
// added, changed or dropped a binding straight in Google's console, `status`
// prints the difference with the command that closes it and writes nothing on
// any path, and `doctor` has been read-only from the start. This is that
// command, a verb of its own: it takes the difference the drift check computes,
// adds the grants the workspace's record states and the member does not carry,
// takes back the rows the member's own roster holds and the record does not
// state, and writes ONE document -- the member's roster at
// config/admin/access -- which the member's own `fugaro update` turns into IAM
// (D2801).
//
// Five properties shape it, and each is a refusal rather than a guess.
//
//   - It writes no member's IAM (D2801, A6). A grant means a row in the
//     member's own roster, and the member's own update is what applies it; a
//     binding this command made on the member's project would be authority
//     taken rather than recorded, and the whole design rests on the workspace
//     never taking one.
//   - It writes no workspace node either, and that is why it consumes no
//     `Store`. What it adds are rows the record already states, and what it
//     takes back are rows the record does not state: either way D2818's mark in
//     the workspace node is right on both sides of the change without a write.
//   - People only (D2806). A machine's difference is REPORTED and never
//     written: the node records what the workspace expects a machine to hold
//     while its real IAM stays in the member's own Terraform or where a person
//     put it, and a machine gets no member roster, no grant and no audit node
//     of its own.
//   - Only an admin who holds the live binding (D2823, D2818). An admin grant
//     is authority over the member, and the workspace node cannot be the
//     evidence for it: the five accounts that hold the workspace's database can
//     rewrite that node (threat-model.md §3) while none of them can set a
//     folder-owner or token-creator binding. So this command reads the live
//     bindings itself -- C2's `Authority.AdminBindings` -- and reports, rather
//     than writes, an admin grant for a person who holds none. The report is
//     consumed for what it is: a difference to write, where writing means
//     rows in a member's roster and never a binding.
//   - One event per change, created and never updated (D2811), in the
//     WORKSPACE's own trail. The member's trail (C2's PathMemberAudit) is where
//     a writer files a change it cannot file in the workspace's project at all
//     -- WU8's grant by a project admin, WU9's offboarding -- and this command
//     is the workspace's own: a workspace admin holds `roles/owner` on the
//     workspace's project (R8), so the workspace's trail is the one that holds
//     what it changed, beside D2818's mark in the workspace's node.
//
// What it does NOT do, said here so a reader does not look for it. It removes
// no binding from a member's IAM: the member's own update derives its lists
// from the roster, so a binding the roster states nothing about cannot be taken
// back by a roster write, and such a difference is NAMED rather than counted as
// done. It touches no mirror (advisory, and a member's own admins may rewrite
// it, D2813). And it takes no empty report for "in sync": the report's
// `Unmeasured` marks are printed, because for a viewer and a repository-scope
// grant "waiting" cannot be told from "applied" (D2815).
//
// The scope an added row carries is the project scope, and it is the only one a
// binding can come from: the member's own update derives its IAM lists from the
// project-scope grants alone (update_access.go's deriveAccess), so the grant a
// binding is missing from is a project-scope row and never a repository list.

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// workspaceReconcileAttempts bounds one reconcile's conditional writes. A
// precondition mismatch is another writer landing between the read and the
// write (C2, D2817), which the next pass answers by reading the roster again
// and judging the same report against what is really there; a roster that moves
// on every attempt is a busy member and a person running the command again is
// the honest answer.
const workspaceReconcileAttempts = 4

// workspaceReconcileNow is the clock the audit events are stamped with. It is a
// variable for the reason budgetNow and mineNow are: a test holds the instant
// still, and no test reads the wall clock.
var workspaceReconcileNow = time.Now

// workspaceReconcileActor reads the address the audit events are filed under:
// the caller's own, read once per run, in the ADDRESS spelling an AuditEvent
// carries rather than the "user:<address>" member spelling a grant is written
// with (access.Caller answers that one). It is a variable because a test's own
// function stands here, so no test reaches Google's userinfo endpoint
// (user_grant.go's userCallerLookup is the same seam one form over).
var workspaceReconcileActor = func(ctx context.Context) (string, error) {
	member, err := accesslib.Caller(ctx)
	if err != nil {
		return "", err
	}
	address, ok := strings.CutPrefix(member, "user:")
	if !ok || address == "" {
		return "", userErr("the credential's own address %q is not the member form this command reads a caller in, and an audit event names its actor as an address", member)
	}
	return address, nil
}

// workspaceReconcileDeps is what this command is wired with: the workspace it
// reconciles for, the one read that turns the project it is given into the
// member the workspace holds, and the three seams C1 and C2 promise (I1's
// workspace_wiring.go builds it from the concrete sides).
type workspaceReconcileDeps struct {
	// Workspace is the workspace's own name, printed and nothing else: the
	// trails this command writes are the workspace's own store.
	Workspace string
	// Members resolves the project named on the command line into the member
	// the workspace holds: its GCP project AND its Firebase project, which the
	// workspace node carries and this command must not guess one of from the
	// other (F2, R10).
	Members func(ctx context.Context, project string) (workspace.MemberTarget, error)
	// Drift is C2's report: the member's live IAM against the workspace's
	// record, never against the mirror (WU12, D2813).
	Drift workspace.Drift
	// Ops is C2's member seam. Only ReadGrants and WriteGrants are called, and
	// the write is always conditional on the revision the read answered.
	Ops workspace.MemberOps
	// Admins is C2's `Authority.AdminBindings`: the members the LIVE bindings
	// name -- the workspace folder's owners, or the workspace IAM account's
	// token creators -- which is the only thing D2823 trusts an admin by. It is
	// read once, and only when the report states an admin grant to write.
	Admins func(ctx context.Context) ([]string, error)
	// Audit is C1's sink: the workspace's own trail, one document per change,
	// created and never updated (D2811).
	Audit workspace.AuditSink
}

type workspaceReconcileOptions struct {
	dryRun bool
}

// newWorkspaceProjectReconcileCmd is `fugaro workspace project reconcile
// PROJECT [--dry-run]`. It builds no reporter of its own: I1 wraps it, and
// registers it under `workspace project` (D1354, plan I1).
func newWorkspaceProjectReconcileCmd(d workspaceReconcileDeps) *cobra.Command {
	o := &workspaceReconcileOptions{}
	cmd := &cobra.Command{
		Use:   "reconcile PROJECT",
		Short: "Close the difference between the workspace's record and a member's policy",
		Long: `Print the difference between what the workspace's record states about a member's
people and what the member's policy carries, and write the member's own roster
so that its next fugaro update closes it.

The differences are the drift check's: the member's live IAM against the
workspace's record, never against the mirror, which the member's own admins can
rewrite. This command adds the grants the record states and the member does not
carry, takes back the rows the member's roster holds and the record does not
state, and prints each difference with what it did about it.

It writes no member's IAM: a grant is a row in the member's own roster at
config/admin/access, and the member's own fugaro update is what turns that row
into a binding. A machine's difference is reported and never written -- a machine
gets no member roster of its own -- and a binding the roster states nothing about
is named rather than silently dropped, because no roster write can take it back.

--dry-run prints the change and writes nothing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return workspaceProjectReconcile(cmd, o, d, args[0])
		},
	}
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the change each difference needs and write nothing")
	return cmd
}

// workspaceProjectReconcile is the command's whole run: the fences, the report,
// the pass over the member's roster, the printing and the events.
func workspaceProjectReconcile(cmd *cobra.Command, o *workspaceReconcileOptions, d workspaceReconcileDeps, project string) error {
	// A change to who may do what in a member project is a person's own step at
	// their own terminal (A7): an agent's tool call is refused before anything
	// is read, and the refusal names the command to run instead.
	if marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); marker != "" {
		return userErr("nothing was written: workspace project reconcile changes who may do what in a member project, and the change lands in that member's own roster, so it is run by a person at their own terminal. Run: fugaro workspace project reconcile %s. %s",
			safetext.Field(project), initflow.AgentRefusal(marker))
	}
	// The run reads the caller's own address back from Google, and
	// GODEBUG=http2debug makes Go print such a request to stderr
	// (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	ctx := cmd.Context()
	member, err := d.Members(ctx, project)
	if err != nil {
		return err
	}
	report, err := d.Drift(ctx, member)
	if err != nil {
		return err
	}
	// The live bindings are read only when the report states an admin grant to
	// write, so a run that has none asks the authority nothing -- and the read
	// that fails is a refusal rather than a run that writes an admin grant on
	// evidence it could not get. The answer a run that never asks is left with
	// is the fail-closed one: an admin grant is written for an admin whose
	// binding was really read.
	boundAdmin := func(string, string) bool { return false }
	if wu13StatesAnAdminGrant(report) {
		if d.Admins == nil {
			return userErr("nothing was written: the workspace's live admin bindings are not wired into this command, and an admin grant is written only for an admin who holds one (D2823)")
		}
		live, err := d.Admins(ctx)
		if err != nil {
			return userErr("nothing was written: the workspace's live admin bindings could not be read, and an admin grant is written only for an admin who holds one (D2823): %v", err)
		}
		boundAdmin = wu13BoundAdmin(live)
	}

	// The caller's own address is read BEFORE anything is written, once, and it
	// is the address every event in the trail is filed under (D2811). Reading it
	// after the write would make a credential nobody can read an address from --
	// a service account, an expired one, an outage at userinfo -- a roster
	// change no event accounts for; read here, that same failure is a refusal
	// with nothing written. A --dry-run writes nothing and asks for no address.
	actor := ""
	if !o.dryRun {
		actor, err = workspaceReconcileActor(ctx)
		if err != nil {
			return userErr("nothing was written: the address of the person running this change could not be read, and every change is filed in the workspace's trail under it (D2811): %v", err)
		}
	}

	// The report is computed once; what a write does to the roster is judged
	// against the bytes really there, which is why a pass that lost the ETag
	// race is made again rather than failed (C2).
	var (
		made  []wu13Change
		named []wu13Named
	)
	for attempt := 1; ; attempt++ {
		doc, err := d.Ops.ReadGrants(ctx, member)
		if err != nil {
			return userErr("nothing was written: the roster of project %s could not be read, and a roster nobody could read is not an empty one: %v", safetext.Field(member.Project), err)
		}
		plan, err := wu13Judge(report, member, doc, boundAdmin)
		if err != nil {
			return err
		}
		made, named = plan.changes, plan.named
		if o.dryRun || len(plan.changes) == 0 {
			break
		}
		raw, err := accesslib.Encode(plan.next)
		if err != nil {
			return userErr("nothing was written: the member's roster would not be a document its own reader reads: %v", err)
		}
		werr := d.Ops.WriteGrants(ctx, member, doc, raw)
		if werr == nil {
			break
		}
		if !errors.Is(werr, rtdb.ErrPrecondition) {
			return werr
		}
		if attempt >= workspaceReconcileAttempts {
			return userErr("nothing was written: the member's roster changed under every one of %d attempts, so another writer is changing it right now and this change cannot be judged against a picture that stays still. Read it with 'fugaro user ls' and run this again", workspaceReconcileAttempts)
		}
	}

	out := cmd.OutOrStdout()
	wu13Print(out, d.Workspace, member, report, made, named, o.dryRun)
	if o.dryRun || len(made) == 0 {
		return nil
	}
	return wu13Audit(ctx, d, member, made, actor)
}

// wu13Change is one change of the plan: one person and one role, added to the
// member's roster or taken back out of it.
type wu13Change struct {
	add     bool
	form    string // the "user:<address>" member form a roster row carries
	address string // the canonical address, for the printing and the event
	role    string
}

// wu13Named is one difference the report carries and this command does not
// write, and the sentence saying why -- a machine, a principal nobody placed,
// a binding that comes with an account, or one the member's roster has no row
// to change.
type wu13Named struct {
	what string
	why  string
}

// wu13Plan is one pass's judgement: the changes a write would make, the
// differences that are named instead, and the document the write would store.
type wu13Plan struct {
	changes []wu13Change
	named   []wu13Named
	next    []accesslib.Grant
}

// wu13Judge judges the report against the member's roster as it was read.
//
// The two directions are not symmetric, and the asymmetry is the design: a
// missing grant is a row to write (the record states it), and an extra binding
// is a row to take back only when the roster really holds one -- a binding
// nothing in the roster derives cannot be reached by a roster write, and it is
// named rather than counted as done.
//
// The row a binding comes from is a PROJECT-SCOPE one and no other, because
// that is the only scope the member's own update derives its IAM lists from
// (update_access.go's deriveAccess). A row of the same member and role at
// another scope is therefore not that row in either direction: it does not
// close a missing binding, and taking it back would change a grant that
// derives no binding at all -- so both directions read the scope as part of the
// pair they judge, and only a project-scope row is ever dropped from the
// document a write stores.
//
// boundAdmin answers whether a person holds a live admin binding (D2823), and
// it is asked once per admin grant the report states. Everything else the
// report names is written on the report's own authority; this one is not,
// because it is the one that hands out authority.
func wu13Judge(report workspace.DriftReport, member workspace.MemberTarget, doc workspace.MemberDoc, boundAdmin func(form, address string) bool) (wu13Plan, error) {
	var p wu13Plan
	var rows []accesslib.Grant
	if doc.Found {
		decoded, err := accesslib.Decode(doc.Raw)
		if err != nil {
			return wu13Plan{}, userErr("nothing was written: the roster of project %s at %s is not a document this command reads: %v", safetext.Field(member.Project), accesslib.PathNode, err)
		}
		rows = decoded
	}
	onRoster := func(form, role string) bool {
		return slices.ContainsFunc(rows, func(g accesslib.Grant) bool {
			return g.User == form && string(g.Role) == role && g.Scope.Kind == accesslib.ScopeProject
		})
	}
	// A row and a judged pair are compared as the whole value they are: the
	// member form, the role AND the scope, so a row at another scope is not read
	// as the row the difference is about.
	rowKey := func(user string, role accesslib.Role, scope accesslib.Scope) string {
		return user + " " + string(role) + " " + scope.String()
	}
	// seen is a pair already judged, so a report that names one twice is one row;
	// removed is a project-scope row the document the write stores drops. They
	// are two maps and not one because an addition's pair is never the row it is
	// judged against -- the two questions happen to have the same answer there,
	// and a later change that made them differ would be a row silently deleted.
	seen := map[string]bool{}
	removed := map[string]bool{}
	for _, b := range report.Missing {
		what := wu13Line("add", b)
		if b.Kind != workspace.PrincipalPerson {
			p.named = append(p.named, wu13Named{what, wu13KindWhy(b.Kind)})
			continue
		}
		form, address, role, err := wu13RowOf(b)
		if err != nil {
			p.named = append(p.named, wu13Named{what, err.Error()})
			continue
		}
		key := form + " " + role
		if seen[key] {
			continue
		}
		seen[key] = true
		if role == string(accesslib.RoleAdmin) && !boundAdmin(form, address) {
			// An admin grant is written for an admin the mechanism really binds,
			// which is the live binding and never the workspace node: the five
			// accounts holding the workspace's database can rewrite that node and
			// none of them can set a folder-owner or token-creator binding
			// (D2823, D2818, threat-model.md §3).
			p.named = append(p.named, wu13Named{what, "an admin grant is written only for an admin who holds the live binding (D2823): the workspace's folder owners, or its IAM account's token creators. " + safetext.Field(address) + " holds none, and a person bound through a group or a domain is not read through here, so this grant is reported and not written"})
			continue
		}
		if onRoster(form, role) {
			p.named = append(p.named, wu13Named{what, "the member's roster already grants this pair at the project scope: the member's own fugaro update is what applies it"})
			continue
		}
		p.changes = append(p.changes, wu13Change{add: true, form: form, address: address, role: role})
	}
	for _, b := range report.Extra {
		what := wu13Line("remove", b)
		switch {
		case b.Kind != workspace.PrincipalPerson:
			p.named = append(p.named, wu13Named{what, wu13KindWhy(b.Kind)})
			continue
		case b.Implicit:
			p.named = append(p.named, wu13Named{what, "it comes with the account rather than from a grant: a member's budget admin holds its database's write whatever the roster says"})
			continue
		}
		form, address, role, err := wu13RowOf(b)
		if err != nil {
			p.named = append(p.named, wu13Named{what, err.Error()})
			continue
		}
		key := form + " " + role
		if seen[key] {
			continue
		}
		seen[key] = true
		if !onRoster(form, role) {
			// The difference is a binding on the member's own policy and no
			// project-scope row of the roster derives it, so no write of this
			// command can reach it: the member's own update is what applies the
			// lists its roster derives (D2801), and this is the difference it has
			// not run against yet.
			why := "the member's roster holds no grant of this pair at the project scope, and this command writes no member's IAM: it is a binding on the member's own policy, and the member's own fugaro update is what applies the lists its roster derives (D2801)"
			if !doc.Found {
				why = "the member's roster is not there yet: its own fugaro update migrates its lists into " + accesslib.PathNode
			}
			p.named = append(p.named, wu13Named{what, why})
			continue
		}
		removed[rowKey(form, accesslib.Role(role), accesslib.Scope{Kind: accesslib.ScopeProject})] = true
		p.changes = append(p.changes, wu13Change{add: false, form: form, address: address, role: role})
	}
	if len(p.changes) == 0 {
		return p, nil
	}
	if !doc.Found {
		// The node is absent, and an absent node is two states the member side
		// of the contract cannot tell apart (a roster of no grants is pruned,
		// so it reads exactly as a node that was never written, C2's MemberDoc).
		// A write here would CREATE it holding this change and nobody else, and
		// the member's own update derives its IAM lists from that node -- which
		// is how every member the member's lists still hold loses its role. The
		// refusal is the one `fugaro user grant` makes for the same state, in
		// the member's own words: the migration is the member's update.
		return wu13Plan{}, userErr("nothing was written: project %s has no roster at %s yet, which is the state an installation whose roles are still its own IAM lists is in. The member's own fugaro update migrates those lists into the node, and a node written before that would hold this change and nobody else. Run the member's own fugaro update, then run this again", safetext.Field(member.Project), accesslib.PathNode)
	}
	// The document the write stores: every row not taken back, in the order the
	// node holds them, and the additions sorted after them by their pair -- so a
	// row this command does not touch keeps its place, and a row of the judged
	// pair at another scope (a repository-scope grant of the same member and
	// role, which derives no binding) is untouched.
	next := make([]accesslib.Grant, 0, len(rows)+len(p.changes))
	for _, g := range rows {
		if removed[rowKey(g.User, g.Role, g.Scope)] {
			continue
		}
		next = append(next, g)
	}
	var adds []accesslib.Grant
	for _, c := range p.changes {
		if !c.add {
			continue
		}
		adds = append(adds, accesslib.Grant{User: c.form, Role: accesslib.Role(c.role), Scope: accesslib.Scope{Kind: accesslib.ScopeProject}})
	}
	slices.SortFunc(adds, func(a, b accesslib.Grant) int {
		return strings.Compare(a.User+" "+string(a.Role), b.User+" "+string(b.Role))
	})
	p.next = append(next, adds...)
	return p, nil
}

// wu13RowOf turns one difference into the row it is about: the member form a
// roster carries and the role, both refused when a row cannot hold them, so a
// difference nothing can be written for is named rather than written.
//
// The report's principal is spelled one of two ways and they are one person: an
// IAM member where the drift check read the member's policy
// ("user:alice@example.com") and a bare address where it read the workspace
// node's own list. access.Member is the function both sides of a comparison
// spell a member through (F25), so it is the one this command writes with, and
// the prefix is taken off first rather than left to become a second "user:" in
// the row.
func wu13RowOf(b workspace.DriftBinding) (form, address, role string, err error) {
	role = b.Role
	if _, ok := accesslib.ParseRole(b.Role); !ok {
		return "", "", "", fmt.Errorf("the role %q is not one of %s, so it is not a pair a grant can carry", b.Role, accesslib.Roles())
	}
	principal := b.Principal
	if rest, ok := strings.CutPrefix(principal, "user:"); ok {
		principal = rest
	}
	if strings.ContainsRune(principal, ':') {
		return "", "", "", fmt.Errorf("the principal %q carries a prefix a person's binding does not -- a person is a `user:` member, and the part after it is their address -- so this command writes no row for it", b.Principal)
	}
	form, err = accesslib.Member(principal)
	if err != nil {
		return "", "", "", fmt.Errorf("the address %q is not one this command can write a row for: %v", b.Principal, err)
	}
	address, _ = strings.CutPrefix(form, "user:")
	return form, address, role, nil
}

// wu13StatesAnAdminGrant reports whether the report holds an admin grant of a
// person, which is the one difference the live bindings have to be read for.
// The kinds are not asked here: a machine's difference is written by nobody, so
// an admin row about one must not send this command to the authority for a
// question it will not act on.
func wu13StatesAnAdminGrant(report workspace.DriftReport) bool {
	return slices.ContainsFunc(report.Missing, func(b workspace.DriftBinding) bool {
		return b.Kind == workspace.PrincipalPerson && b.Role == string(accesslib.RoleAdmin)
	})
}

// wu13BoundAdmin is the live bindings as one question: does this person hold
// one? The mechanism answers in the spelling its own policy holds: a person is
// "user:<address>", and a person who holds the role through a group or a domain
// is listed as that group or domain (D2823, F25: nothing here reads through
// one). The spellings a person's row carries are the member form and the bare
// address, so both are keys -- and a group or a domain is neither, which is the
// fail-closed half: an admin nobody can place is reported, not written.
func wu13BoundAdmin(live []string) func(form, address string) bool {
	bound := make(map[string]bool, len(live)*2)
	for _, b := range live {
		bound[b] = true
		if addr, ok := strings.CutPrefix(b, "user:"); ok {
			bound[addr] = true
		}
	}
	return func(form, address string) bool { return bound[form] || bound[address] }
}

// wu13KindWhy says why a difference of one principal kind is reported and not
// written. A person is the only kind this command writes.
func wu13KindWhy(kind workspace.PrincipalKind) string {
	switch kind {
	case workspace.PrincipalMachine:
		return "a machine: the record states what the workspace expects it to hold and its real IAM stays where it is, and a machine gets no member roster of its own, so this difference is reported and never written"
	case workspace.PrincipalTerraform:
		return "one of the member's own Terraform accounts: the drift check does not compare its grants, so there is nothing here to change"
	case workspace.PrincipalWorkspaceIAM:
		return "the workspace's own IAM account: its grant is the mechanism itself, so there is nothing here to change"
	default:
		return "nobody has placed this principal, and this command writes no row for a principal it cannot place"
	}
}

// wu13Line is one difference as the printed row: what the command does with it
// and the binding it is about.
func wu13Line(verb string, b workspace.DriftBinding) string {
	return fmt.Sprintf("%s %s %s on %s", verb, safetext.Field(b.Principal), safetext.Field(b.Role), safetext.Field(b.Project))
}

// wu13Print writes the account of the run: one row per difference with what
// happened to it, the report's own limits, and the one command that applies
// what was written.
func wu13Print(w io.Writer, workspaceName string, member workspace.MemberTarget, report workspace.DriftReport, made []wu13Change, named []wu13Named, dryRun bool) {
	fmt.Fprintf(w, "workspace %s: member %s\n", safetext.Field(workspaceName), safetext.Field(member.Project))
	for _, c := range made {
		verb := "add"
		if !c.add {
			verb = "remove"
		}
		fmt.Fprintf(w, "  %s %s %s\n", verb, safetext.Field(c.address), safetext.Field(c.role))
	}
	for _, n := range named {
		fmt.Fprintf(w, "  report %s\n", n.what)
		fmt.Fprintf(w, "         %s\n", n.why)
	}
	for _, u := range report.Unmeasured {
		fmt.Fprintf(w, "  report %s: not measured by the drift check, so an empty difference above is not \"in sync\" while this is here\n", safetext.Field(u))
	}
	for _, c := range report.ConsoleChanges {
		fmt.Fprintf(w, "  report %s %s on %s: Cloud Audit Logs records this change (entry %s, %s) and no trail of Fugaro's holds it -- look it up in Google's console\n",
			safetext.Field(c.Principal), safetext.Field(c.Role), safetext.Field(c.Project), safetext.Field(c.Entry), c.At.UTC().Format(time.RFC3339))
	}
	switch {
	case dryRun && len(made) > 0:
		fmt.Fprintf(w, "nothing was written: --dry-run prints the change and writes nothing. The member's own fugaro update is what applies a grant (D2801): it turns the roster at %s into the member's IAM.\n", accesslib.PathNode)
	case len(made) > 0:
		fmt.Fprintf(w, "%d change(s) written into the member's roster at %s. The member's own fugaro update applies them (D2801): no command of this machine can.\n", len(made), accesslib.PathNode)
	default:
		fmt.Fprintf(w, "nothing to write: the member's policy and the workspace's record agree about the people they name. The member's own fugaro update is what applies a grant (D2801), and this command writes no member's IAM itself.\n")
	}
}

// wu13Audit files one event per change in the workspace's trail, created and
// never updated (D2811): the person's address as the subject, the member's
// project as the project, and the role the change carried.
//
// The actor is the caller's own address, already read by the run: this function
// is reached only after the roster write, and a lookup that could fail here
// would leave a written change with no entry and no refusal, which is the one
// thing the trail exists to prevent.
func wu13Audit(ctx context.Context, d workspaceReconcileDeps, member workspace.MemberTarget, made []wu13Change, actor string) error {
	for i, c := range made {
		now := workspaceReconcileNow()
		id, err := workspace.NewAuditEventID(now, crand.Reader)
		if err != nil {
			return err
		}
		action := workspace.ActionGrantWrite
		if !c.add {
			action = workspace.ActionGrantRemove
		}
		raw, err := workspace.EncodeAuditEvent(workspace.AuditEvent{
			Version: workspace.AuditEventVersion,
			ID:      id,
			Time:    now,
			Actor:   actor,
			Action:  action,
			Project: member.Project,
			Subject: c.address,
			Role:    workspace.Role(c.role),
		})
		if err != nil {
			return err
		}
		if err := d.Audit.Create(ctx, id, raw); err != nil {
			// Everything from this change on has no entry, and all of it is
			// named: the changes already filed are accounted for, and a stop
			// that reported only the one the sink refused would leave the rest
			// of a written roster unaccounted for and unnamed.
			return fmt.Errorf("the member's roster was written, and the audit entries for %s did not land (%d change(s) the workspace's trail does not account for): %w",
				strings.Join(wu13Addresses(made[i:]), ", "), len(made)-i, err)
		}
	}
	return nil
}

// wu13Addresses is a run's changes as the list a message names them by, in the
// order they were written and escaped for a terminal (safetext's rule).
func wu13Addresses(changes []wu13Change) []string {
	addresses := make([]string, len(changes))
	for i, c := range changes {
		addresses[i] = safetext.Field(c.address)
	}
	return addresses
}
