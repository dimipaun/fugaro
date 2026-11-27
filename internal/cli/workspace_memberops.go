package cli

// workspace_memberops.go is WU4: what a workspace's commands do to ONE member
// project. It is C2's `MemberOps` (internal/workspace/memberops.go) except
// `Offboard`, which is WU9's core; I1 adapts the two onto that interface
// together, and `memberWriteOps` below is that subset written out, so a method
// this file forgets, or spells differently, is a compile error rather than a gap
// found while wiring.
//
// Five decisions shape it, and none can be re-derived from a method list.
//
//   - **Nothing here runs Terraform** (D2801). A member's installation is read
//     the way the member's own `fugaro update` leaves it: two state documents in
//     the member's own state bucket — the installation root's outputs and the
//     Firebase root's — read as objects and decoded by name
//     (infra.DecodeOutputs, infra.DecodeFirebaseOutputs). Terraform's document is
//     decoded WITHOUT DisallowUnknownFields, and deliberately: it is Terraform's
//     format and not ours, so a state a newer Terraform wrote is still read, and
//     what is refused is a state whose OUTPUTS cannot be read rather than one
//     carrying a key this build does not know. Strictness lives one level down,
//     in this program's own decoders, which is where a key of ours would be.
//   - **The state bucket is derived from the member's project name and never
//     taken from a flag** (managedmode.InstallationsStateBucket), for the reason
//     managed_inventory.go and managed_flag.go state: a bucket a caller could
//     name is a bucket this command would believe instead of the member's own. A
//     member whose owner passed `fugaro update --state-bucket` is therefore not
//     readable from here, and that is a refusal rather than a shorter report.
//   - **The member's database address comes from the member's own state and is
//     tied to the member asked about** (infra.CheckFirebaseOutputsFor). A state
//     bucket is readable by whoever holds the bucket, so a well-formed triple
//     naming another project must not be taken as this member's database; the
//     pair of projects a member IS (F2, R10) travels into that check, which is
//     why a caller naming only the GCP project of a member whose Firebase
//     project is a project of its own is refused rather than read against the
//     wrong database.
//   - **The member roster's format is internal/access's own and unchanged**
//     (D2806, D2818). Every write decodes the bytes through that package's reader
//     first and writes through its writer, so a document its reader refuses is
//     refused here with nothing written: this is the one door a workspace path
//     has into a member's roster, and the reason the format has nowhere else to
//     change. The write keeps `fugaro user grant`'s refusal on an installation
//     whose lists were never migrated — the member's own `fugaro update` derives
//     its IAM lists FROM that node, so a node written before the migration would
//     hold this change and nobody else.
//   - **Every write is conditional on the ETag that was read** (D2817), as WU1's
//     roster node is on the workspace's side: a node another writer moved under
//     this one is `rtdb.ErrPrecondition` with nothing written, and the caller
//     reads again and judges against the bytes that are really there, so two
//     admins editing one member cannot lose each other's work.
//
// The mirror's document shape is here because nothing else declares it: C2 fixes
// the path and C1 the entry, and a node needs a document. It is
// `{"version":1,"members":[{...C1's entry...}]}` — the version this build writes,
// and C1's own entries under the key the roster spells its list with — and it is
// read strictly (an unknown key is a refusal, a version this build does not write
// is a refusal, and every entry is held to C1's `Validate`): one spelling of the
// document, for the same reason the roster has one. The REMOVAL is the EMPTY OBJECT
// (`{}`), not an empty list: the database prunes a value-less document, so `{}`
// is a DELETE of the node, and that is exactly what detaching a member needs —
// the write WU6 calls to remove the mirror — which is also why `Found=false`
// reads as "not mirrored" rather than as a member list that is empty. The mirror
// is ADVISORY besides (D2813): a member's own admins hold their
// database's write, so drift, never the mirror, is the fence, and the strict read
// here is a refusal to act on a document it cannot understand rather than a claim
// about its truth.
//
// Two gaps are stated rather than papered over, and WU11 and WU12 are the
// consumers they matter to. A machine — C2's `PrincipalMachine` — is placed by
// the CALLER: the record of what a machine is a member of lives in the workspace
// node, which this value never reads, so `Bindings` answers `PrincipalUnknown`
// for one and the caller holding the node places it. And `MemberBinding` carries
// no condition, so a CONDITIONAL binding is listed flat: a reader comparing
// pieces of a policy must know that a member listed here holds the role only
// where the condition holds.
//
// There is no coding-agent refusal (A7) in this file: it is a provider, not a
// command. That refusal belongs to the write commands that call it (WU5-WU13,
// and I1 where it registers them), as it does for WU1's `AuditWriter`.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// WorkspaceMemberOps is this file's concrete member writer. It holds the three
// things every call needs and nothing else: how to reach Google's APIs, the
// workspace's own IAM account (WU3's `workspace_iam_account`, which `Bindings`
// names), and whether this machine talks to a fake database.
//
// The clients are a function rather than a value because a command may make any
// number of calls and a client is cheap only once: the production constructor
// connects on the first call, and a test hands back fakes. An empty
// `workspaceIAM` is a workspace whose `workspace init` has not read its root's
// output yet, and is not a refusal: `Bindings` then places that account as one
// nobody has placed, which is the fail-closed direction for a reader that must
// not confuse the workspace's own account with somebody else's.
type WorkspaceMemberOps struct {
	cloud        func(ctx context.Context) (*infra.Clients, error)
	workspaceIAM string
	loopback     bool
}

// NewWorkspaceMemberOps returns the member writer `fugaro workspace` uses.
//
// D1541's fence is asked here rather than in a method: every call that reaches
// a member project goes through this opener, so a command the fence applies to
// (a roster write is one) is refused before a client exists, once per command.
// It is keyed on the running command, as openCloudFor is, which leaves the
// read-only listings and the exempt commands answered without a read.
func NewWorkspaceMemberOps(lc *localcfg.Config, workspaceIAM string) *WorkspaceMemberOps {
	return &WorkspaceMemberOps{
		cloud: func(ctx context.Context) (*infra.Clients, error) {
			if err := requireManaged(ctx, managedCommandFrom(ctx), lc); err != nil {
				return nil, err
			}
			return newInitClients(ctx, lc)
		},
		workspaceIAM: workspaceIAM,
		loopback:     lc.Endpoints.NoAuth,
	}
}

// memberWriteOps is C2's MemberOps without Offboard, which WU9 provides.
type memberWriteOps interface {
	ReadGrants(ctx context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error)
	WriteGrants(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error
	ReadMirror(ctx context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error)
	WriteMirror(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error
	Bindings(ctx context.Context, m workspace.MemberTarget) ([]workspace.MemberBinding, error)
	PendingApply(ctx context.Context, m workspace.MemberTarget, want workspace.ApplyWant) (workspace.Pending, error)
	Audit(ctx context.Context, m workspace.MemberTarget, id string, event []byte) error
}

var _ memberWriteOps = (*WorkspaceMemberOps)(nil)

// ReadGrants reads the member's roster, `config/admin/access`, with the ETag a
// conditional write is made under.
//
// A node that is there but that internal/access's own reader refuses is an
// error, not bytes handed back for a caller to interpret: a node this program
// cannot read whole is a node whose other grants it cannot vouch for either, and
// the caller's next step is a write.
func (o *WorkspaceMemberOps) ReadGrants(ctx context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	db, err := o.memberDatabase(ctx, m)
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	doc, err := memberDoc(ctx, db, accesslib.PathNode)
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	if doc.Found {
		if _, err := accesslib.Decode(doc.Raw); err != nil {
			return workspace.MemberDoc{}, userErr("member project %s's grants node at %s cannot be read: %v. Nothing was read and nothing was written: the node is the member's own roster, and a reader that used the entries it could parse would be acting on a document it only partly understood", safetext.Field(m.Project), accesslib.PathNode, err)
		}
	}
	return doc, nil
}

// WriteGrants writes the member's roster, conditional on doc.ETag, and refuses
// an installation whose own lists have not been migrated (see this file's own
// doc).
func (o *WorkspaceMemberOps) WriteGrants(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	grants, err := accesslib.Decode(raw)
	if err != nil {
		return userErr("the roster written to member project %s is not the document its own CLI reads at %s: %v. Nothing was written: every member write goes through this node, so a document its reader refuses is one the member's own `fugaro update` could not derive its IAM from", safetext.Field(m.Project), accesslib.PathNode, err)
	}
	db, err := o.memberDatabase(ctx, m)
	if err != nil {
		return err
	}
	if !doc.Found {
		migrated, err := accesslib.Migrated(ctx, db)
		if err != nil {
			return memberDBErr(accesslib.PathNode, err)
		}
		if !migrated {
			return userErr("nothing was written: member project %s has no grants node at %s yet and no migration marker at %s, so its own IAM lists have never been migrated into the node. Its next `fugaro update` derives those lists FROM the node, and a node written now would hold this change and nobody else, so every launcher and operator that installation names would lose their grant in that run. The member's own machine runs `fugaro update` first — which migrates the installation's lists into the node — and this is run again after it", safetext.Field(m.Project), accesslib.PathNode, accesslib.PathMigrated)
		}
	}
	if err := accesslib.WriteIfMatch(ctx, db, grants, doc.ETag); err != nil {
		return memberWriteErr(m, accesslib.PathNode, err)
	}
	return nil
}

// RecordRemoval writes the person's removal into the member's own record of
// removed people, at PathRemoved (DG5, D3454): it is the member's own
// `fugaro update` that drops the binding the workspace took off its derived
// admin list, and that update admits an IAM removal only when the record names
// the person. It writes the member's roster node's sibling and never an IAM
// policy (D2801, A6).
func (o *WorkspaceMemberOps) RecordRemoval(ctx context.Context, m workspace.MemberTarget, member string) error {
	db, err := o.memberDatabase(ctx, m)
	if err != nil {
		return err
	}
	return recordMemberRemoval(ctx, nil, db, member)
}

// ReadMirror reads the member's mirror at PathMemberMirror. An absent node is a
// member the workspace has not mirrored into, which `Found` tells from a member
// whose mirror was removed; a node this file's reader refuses is an error, and
// never an empty member list.
func (o *WorkspaceMemberOps) ReadMirror(ctx context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	db, err := o.memberDatabase(ctx, m)
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	doc, err := memberDoc(ctx, db, workspace.PathMemberMirror)
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	if doc.Found {
		if _, err := DecodeMemberMirror(doc.Raw); err != nil {
			return workspace.MemberDoc{}, userErr("member project %s's mirror at %s cannot be read: %v. Nothing was read and nothing was written: the mirror is the member list the workspace put in this member's database, and a reader that acted on the entries it could parse would be acting on a document it only partly understood", safetext.Field(m.Project), workspace.PathMemberMirror, err)
		}
	}
	return doc, nil
}

// WriteMirror writes the member's mirror document, conditional on doc.ETag. The
// empty JSON object is the document that REMOVES the mirror — the write replaces
// the whole node and the database prunes an empty one, so the node reads as the
// absent key a member the workspace does not mirror into has (`workspace project
// rm` writes it) — and everything else is read with the mirror's own reader
// first, so a writer cannot store a document its own reader refuses.
func (o *WorkspaceMemberOps) WriteMirror(ctx context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	fields, err := mirrorObject(raw)
	if err == nil && len(fields) > 0 {
		_, err = DecodeMemberMirror(raw)
	}
	if err != nil {
		return userErr("the mirror written to member project %s is not the document this writer reads at %s: %v. Nothing was written: the write replaces the whole node, and the member's own admins read it as the member list", safetext.Field(m.Project), workspace.PathMemberMirror, err)
	}
	db, err := o.memberDatabase(ctx, m)
	if err != nil {
		return err
	}
	if err := db.PutIfMatch(ctx, workspace.PathMemberMirror, doc.ETag, json.RawMessage(bytes.TrimSpace(raw))); err != nil {
		return memberWriteErr(m, workspace.PathMemberMirror, err)
	}
	return nil
}

// Bindings reads the member's IAM policy on both of its projects and names what
// Fugaro knows each bound principal is: the member's own Terraform accounts,
// read from the member's outputs (F17), and the workspace's IAM account (WU3,
// D2803). A person is a person, and everything this value cannot place — a
// group, a domain, another project's account, a deleted principal — is
// `PrincipalUnknown`, which a reader must treat as somebody else's binding.
//
// An installation whose outputs cannot be read is an ERROR and not a partially
// placed report: a reader that reported Fugaro's own accounts as unplaced would
// hand the drift comparison a difference nobody made. An absent Firebase root's
// state is the one thing tolerated, and only because it is about one account:
// that root has not been applied, so its signer is not named here and the
// binding it may hold is placed as nobody's — the fail-closed direction, since
// nothing removes an unplaced binding.
//
// The bindings of both projects come back in one list, sorted by project,
// principal and role, so two reads of one member are one report.
func (o *WorkspaceMemberOps) Bindings(ctx context.Context, m workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	if err := checkMemberTarget(m); err != nil {
		return nil, err
	}
	c, err := o.cloud(ctx)
	if err != nil {
		return nil, remote(err)
	}
	outs, applied, err := o.memberInstallationOutputs(ctx, c, m)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, userErr("member project %s's state bucket holds no installation state at gs://%s/%s/default.tfstate, so the accounts its own Terraform created are not known: `Bindings` names them from the member's outputs (F17), and a member whose outputs are not there is not one whose bindings can be placed", safetext.Field(m.Project), managedmode.InstallationsStateBucket(m.Project), infra.StatePrefixInstallation)
	}
	fo, err := o.memberFirebaseOutputs(ctx, c, m, true)
	if err != nil {
		return nil, err
	}
	own := memberOwnAccounts(m, outs, fo)

	var out []workspace.MemberBinding
	for _, project := range memberPolicyProjects(m) {
		pol, err := c.CRM.Projects.GetIamPolicy(project, &cloudresourcemanager.GetIamPolicyRequest{
			Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
		}).Context(ctx).Do()
		if err != nil {
			return nil, memberPolicyErr(project, err)
		}
		for _, b := range pol.Bindings {
			for _, principal := range b.Members {
				out = append(out, workspace.MemberBinding{
					Project:   project,
					Principal: principal,
					Role:      b.Role,
					Kind:      o.bindingKind(principal, own),
				})
			}
		}
	}
	sortBindings(out)
	return out, nil
}

// PendingApply compares what the record says the member's IAM should carry with
// what the member has applied: the installation outputs `launchers` and
// `operators`, and the admin list the member's own `fugaro update` publishes
// (access.PathAdminList, D1940). It reads; it applies nothing (F7, D2801).
//
// Nothing applied yet — no state object, or one holding no outputs — is a fact
// and not an error: every entry the record states is pending, which is what a
// member its own update has never run reads as. A read that FAILED is an error,
// because a member reported clean because a read was denied is the one answer
// this comparison must never give.
//
// `NotMeasured` carries both marks on every call, deliberately: no installation
// output and no published object carries a viewer or a repository-scope grant,
// so whether one waits for the member's update cannot be told from what has
// applied — and `want` says nothing about them either, so empty pending lists
// would otherwise read as "in sync" for grants nobody compared (D2815).
func (o *WorkspaceMemberOps) PendingApply(ctx context.Context, m workspace.MemberTarget, want workspace.ApplyWant) (workspace.Pending, error) {
	if err := checkMemberTarget(m); err != nil {
		return workspace.Pending{}, err
	}
	c, err := o.cloud(ctx)
	if err != nil {
		return workspace.Pending{}, remote(err)
	}
	outs, applied, err := o.memberInstallationOutputs(ctx, c, m)
	if err != nil {
		return workspace.Pending{}, err
	}
	pending := workspace.Pending{NotMeasured: []string{workspace.NotMeasuredViewers, workspace.NotMeasuredScopes}}
	if !applied {
		pending.Launchers = memberList(want.Launchers)
		pending.Operators = memberList(want.Operators)
		pending.Admins = memberList(want.Admins)
		return pending, nil
	}
	pending.Launchers = memberMissing(want.Launchers, outs.Launchers)
	pending.Operators = memberMissing(want.Operators, outs.Operators)
	admins, published, err := o.publishedAdmins(ctx, c, outs)
	if err != nil {
		return workspace.Pending{}, err
	}
	if !published {
		pending.Admins = memberList(want.Admins)
		return pending, nil
	}
	pending.Admins = memberMissing(want.Admins, admins)
	return pending, nil
}

// publishedAdmins reads the admin list the member's own `fugaro update` wrote
// into its runs bucket. published is false for a list that is not there, which
// is a member whose update has published none yet — every admin the record
// states is then pending — and for an installation whose outputs name no runs
// bucket at all, since that is where the object would be.
func (o *WorkspaceMemberOps) publishedAdmins(ctx context.Context, c *infra.Clients, outs infra.InstallationOutputs) ([]string, bool, error) {
	if outs.RunsBucket == "" {
		return nil, false, nil
	}
	raw, err := infra.ReadStateObject(ctx, c, outs.RunsBucket, accesslib.PathAdminList)
	if err != nil {
		if memberObjectAbsent(err) {
			return nil, false, nil
		}
		return nil, false, userErr("the published admin list in gs://%s/%s could not be read: %v. Nothing is reported as applied: an object that is there and refused is not an object that is not there, and the refusal GCS answers a caller holding the get but not the list is one of them", safetext.Field(outs.RunsBucket), accesslib.PathAdminList, err)
	}
	list, err := accesslib.ParseAdminList(raw)
	if err != nil {
		return nil, false, userErr("the published admin list in gs://%s/%s cannot be read: %v. Nothing is reported as applied: the list is what the member's own intake answers who may write the shared prefix, and a document this program understands only in part vouches for nobody", safetext.Field(outs.RunsBucket), accesslib.PathAdminList, err)
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Email)
	}
	return out, true, nil
}

// memberDatabase opens the client of the member's own database, whose address is
// the member's Firebase state's `rtdb_url` and nothing else.
func (o *WorkspaceMemberOps) memberDatabase(ctx context.Context, m workspace.MemberTarget) (*rtdb.Client, error) {
	if err := checkMemberTarget(m); err != nil {
		return nil, err
	}
	c, err := o.cloud(ctx)
	if err != nil {
		return nil, remote(err)
	}
	fo, err := o.memberFirebaseOutputs(ctx, c, m, false)
	if err != nil {
		return nil, err
	}
	db, err := newBudgetClient(ctx, fo.RTDBURL, o.loopback)
	if err != nil {
		return nil, err
	}
	return db, nil
}

// memberInstallationOutputs reads the member's installation outputs, and whether
// there are any: applied is false for a state bucket holding no installation
// state and for one whose outputs are empty, which is a member nothing has been
// applied to yet and not an error.
func (o *WorkspaceMemberOps) memberInstallationOutputs(ctx context.Context, c *infra.Clients, m workspace.MemberTarget) (infra.InstallationOutputs, bool, error) {
	raw, err := memberStateObject(ctx, c, m, infra.StatePrefixInstallation)
	if err != nil {
		return infra.InstallationOutputs{}, false, err
	}
	if raw == nil {
		return infra.InstallationOutputs{}, false, nil
	}
	values, err := stateOutputValues(raw)
	if err != nil {
		return infra.InstallationOutputs{}, false, memberStateErr(m, infra.StatePrefixInstallation, err)
	}
	outs, err := infra.DecodeOutputs(values)
	switch {
	case errors.Is(err, infra.ErrNoOutputs):
		return infra.InstallationOutputs{}, false, nil
	case err != nil:
		return infra.InstallationOutputs{}, false, memberStateErr(m, infra.StatePrefixInstallation, err)
	}
	return outs, true, nil
}

// memberFirebaseOutputs reads the member's Firebase outputs and holds them to
// being THIS member's (see this file's own doc). absentOK tolerates a Firebase
// root that has not been applied — `Bindings` is the one caller that can go on
// without it — and is an error for every other caller, which needs the
// database's address.
func (o *WorkspaceMemberOps) memberFirebaseOutputs(ctx context.Context, c *infra.Clients, m workspace.MemberTarget, absentOK bool) (infra.FirebaseOutputs, error) {
	raw, err := memberStateObject(ctx, c, m, infra.StatePrefixFirebase)
	if err != nil {
		return infra.FirebaseOutputs{}, err
	}
	if raw == nil {
		if absentOK {
			return infra.FirebaseOutputs{}, nil
		}
		return infra.FirebaseOutputs{}, userErr("member project %s's state bucket holds no Firebase state at gs://%s/%s/default.tfstate, so the address of the member's own database is not known. The member's own `fugaro update` writes that state; two things also leave it unreadable here, and both are named rather than guessed at: an installation that has not applied its Firebase root, and one whose state lives in a bucket that is not the member project's own (fugaro update --state-bucket)", safetext.Field(m.Project), managedmode.InstallationsStateBucket(m.Project), infra.StatePrefixFirebase)
	}
	values, err := stateOutputValues(raw)
	if err != nil {
		return infra.FirebaseOutputs{}, memberStateErr(m, infra.StatePrefixFirebase, err)
	}
	fo, err := infra.DecodeFirebaseOutputs(values)
	if err != nil {
		return infra.FirebaseOutputs{}, memberStateErr(m, infra.StatePrefixFirebase, err)
	}
	fp := m.Firebase
	if fp == "" {
		fp = m.Project
	}
	if err := infra.CheckFirebaseOutputsFor(fo, fp, o.loopback); err != nil {
		return infra.FirebaseOutputs{}, userErr("member project %s's Firebase state is not this member's: %v. A state bucket is readable by whoever holds it, so a well-formed triple naming another project is not taken as this member's database", safetext.Field(m.Project), err)
	}
	return fo, nil
}

// memberStateObject reads one of the member's two state documents. A nil answer
// with a nil error is the object not being there at all, which every caller
// reads as its own kind of "nothing has been applied".
func memberStateObject(ctx context.Context, c *infra.Clients, m workspace.MemberTarget, prefix string) ([]byte, error) {
	bucket := managedmode.InstallationsStateBucket(m.Project)
	name := prefix + "/default.tfstate"
	raw, err := infra.ReadStateObject(ctx, c, bucket, name)
	if err == nil {
		return raw, nil
	}
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
		return nil, nil
	case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
		return nil, userErr("member project %s's state bucket gs://%s refused the read of %s: a workspace admin reads it through the authority over the member (the folder that holds it, or the workspace IAM account's roles/owner). A member whose state lives in another bucket (fugaro update --state-bucket) is not readable from here, and neither is one whose authority this workspace does not hold (%v)", safetext.Field(m.Project), safetext.Field(bucket), name, err)
	default:
		return nil, remote(err)
	}
}

// stateOutputValues reads a Terraform state document's outputs as `terraform
// output -json` names them: each output's value, unwrapped from the object the
// state holds it in. The document is Terraform's and is decoded without
// DisallowUnknownFields (see this file's own doc); what comes back is a map this
// program's own decoders read, which is where strictness belongs.
func stateOutputValues(raw []byte) (map[string]json.RawMessage, error) {
	var doc struct {
		Outputs map[string]struct {
			Value json.RawMessage `json:"value"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("it is not a JSON state document: %w", err)
	}
	out := make(map[string]json.RawMessage, len(doc.Outputs))
	for name, o := range doc.Outputs {
		out[name] = o.Value
	}
	return out, nil
}

// memberOwnAccounts is the set of addresses that are the member's own Terraform
// accounts (F17): the ones its outputs name — the scheduler, history,
// coordinator, catalog-check and credentials-service accounts, and the Firebase
// root's token signer — plus the build identity, which is derived from the
// project's name (infra.BuildIdentityEmail) because no output carries it.
func memberOwnAccounts(m workspace.MemberTarget, outs infra.InstallationOutputs, fo infra.FirebaseOutputs) map[string]bool {
	own := map[string]bool{infra.BuildIdentityEmail(m.Project): true}
	for _, account := range []string{outs.SchedulerServiceAccount, outs.HistoryServiceAccount, outs.CoordinatorServiceAccount, outs.CatalogCheckServiceAccount} {
		if account != "" {
			own[account] = true
		}
	}
	if outs.CredentialsService != nil && outs.CredentialsService.Account != "" {
		own[outs.CredentialsService.Account] = true
	}
	if fo.TokenSigner != "" {
		own[fo.TokenSigner] = true
	}
	return own
}

// bindingKind places one IAM principal. The workspace's own account comes first,
// because it is a service account too and is in no member's outputs; a `user:`
// member is a person; a principal of another grammar — a group, a domain, a
// deleted principal — is nobody this value can place, and is reported as
// unplaced rather than guessed at.
func (o *WorkspaceMemberOps) bindingKind(principal string, own map[string]bool) workspace.PrincipalKind {
	address, ok := strings.CutPrefix(principal, "serviceAccount:")
	if !ok {
		if _, ok := strings.CutPrefix(principal, "user:"); ok {
			return workspace.PrincipalPerson
		}
		return workspace.PrincipalUnknown
	}
	switch {
	case o.workspaceIAM != "" && address == o.workspaceIAM:
		return workspace.PrincipalWorkspaceIAM
	case own[address]:
		return workspace.PrincipalTerraform
	default:
		return workspace.PrincipalUnknown
	}
}

// sortBindings orders a report by project, principal and role, so two reads of
// one member's policies are one list whatever order the API answered in.
func sortBindings(bindings []workspace.MemberBinding) {
	slices.SortFunc(bindings, func(a, b workspace.MemberBinding) int {
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		if c := strings.Compare(a.Principal, b.Principal); c != 0 {
			return c
		}
		return strings.Compare(a.Role, b.Role)
	})
}

// memberPolicyProjects is the member's projects whose policies are read, in the order
// they are read: the GCP project first, and the Firebase project when it is a
// project of its own (F2, R10) — the same pairing WU20's saProjects makes.
func memberPolicyProjects(m workspace.MemberTarget) []string {
	if m.Firebase == "" || m.Firebase == m.Project {
		return []string{m.Project}
	}
	return []string{m.Project, m.Firebase}
}

// memberDoc reads one of the member's documents with the ETag a conditional
// write is made under. An absent node is `Found=false` beside the null ETag,
// which is the precondition that creates it.
func memberDoc(ctx context.Context, db *rtdb.Client, path string) (workspace.MemberDoc, error) {
	var raw json.RawMessage
	etag, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return workspace.MemberDoc{}, memberDBErr(path, err)
	}
	if !found {
		return workspace.MemberDoc{ETag: etag}, nil
	}
	return workspace.MemberDoc{Raw: raw, ETag: etag, Found: true}, nil
}

// memberList folds a list of entries the way this file compares one: each entry
// once, ordered by the key the comparison uses, and spelled as the caller
// spelled it — what comes back is what a person reads, and the key is not a
// spelling this file invents.
func memberList(entries []string) []string {
	seen := make(map[string]bool, len(entries))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		key := memberKey(e)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(memberKey(a), memberKey(b)) })
	return out
}

// memberMissing is what of want the applied list does not carry, by that key.
func memberMissing(want, applied []string) []string {
	have := make(map[string]bool, len(applied))
	for _, a := range applied {
		have[memberKey(a)] = true
	}
	out := make([]string, 0, len(want))
	for _, w := range memberList(want) {
		if !have[memberKey(w)] {
			out = append(out, w)
		}
	}
	return out
}

// memberKey is the one key these lists are compared by. An IAM member is
// `user:<address>` and the published admin list holds the bare address, so the
// prefix is dropped first; the address then goes through internal/access's own
// member grammar, which is the fold every other side of a member uses (a
// domain's case). A member of another grammar — `group:`, `domain:`, a service
// account — is kept verbatim, because folding it through an address grammar
// would read a machine as a person.
func memberKey(entry string) string {
	prefix, rest, ok := strings.Cut(entry, ":")
	if !ok {
		if folded, err := accesslib.Member(entry); err == nil {
			return folded
		}
		return entry
	}
	if prefix != "user" {
		return entry
	}
	if folded, err := accesslib.Member(rest); err == nil {
		return folded
	}
	return entry
}

// memberObjectAbsent reports whether an object read failed because the object is
// not there. GCS answers 404 for a missing object, and AnswerMissingAs403's 403
// is NOT this: a caller that holds the get but not the list is refused, and that
// refusal is a read that failed rather than an object that is not there.
func memberObjectAbsent(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && ge.Code == http.StatusNotFound
}

// memberWriteErr classifies a refused conditional write: a node another writer
// moved is a user error the caller answers by reading again (and it stays
// rtdb.ErrPrecondition through the wrap, which is how a caller tells it), a
// refused permission is a user error naming the authority that is missing, and
// anything else is a remote failure.
func memberWriteErr(m workspace.MemberTarget, path string, err error) error {
	switch {
	case errors.Is(err, rtdb.ErrPrecondition):
		return userErr("nothing was written to member project %s at %s: the node moved since it was read, so the change was judged against a picture that is not the one it would be written over. Read it again, judge against the bytes that are really there, and write again (%w)", safetext.Field(m.Project), path, err)
	case errors.Is(err, rtdb.ErrPermission):
		return userErr("member project %s's database refused the write to %s: a workspace admin writes it through the authority over the member (the folder that holds it, or the workspace IAM account's roles/owner) (%w)", safetext.Field(m.Project), path, err)
	default:
		return remote(err)
	}
}

// memberDBErr classifies a failed read of the member's database, as dbErr
// classifies one of the budget database: a refused permission is a user error,
// everything else a remote one.
func memberDBErr(path string, err error) error {
	if errors.Is(err, rtdb.ErrPermission) {
		return userErr("the member's database refused the read of %s: a workspace admin reads it through the authority over the member (the folder that holds it, or the workspace IAM account's roles/owner) (%w)", path, err)
	}
	return remote(err)
}

// memberPolicyErr classifies a failed read of one of the member's policies.
func memberPolicyErr(project string, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusUnauthorized) {
		return userErr("project %s's IAM policy refused the read: a workspace admin reads a member's policy through the authority over the member (the folder that holds it, or the workspace IAM account's roles/owner) (%v)", safetext.Field(project), err)
	}
	return remote(err)
}

// memberStateErr is a state document this program cannot read. The member's
// installation state is its own Terraform's, and a value of it this reader does
// not understand is a fact about the member rather than a remote failure.
func memberStateErr(m workspace.MemberTarget, prefix string, err error) error {
	return userErr("member project %s's state at gs://%s/%s/default.tfstate cannot be read: %v. Nothing is reported from it: every answer this file gives about what a member has applied comes from those outputs, and a document read in part is one nothing may be concluded from", safetext.Field(m.Project), safetext.Field(managedmode.InstallationsStateBucket(m.Project)), prefix, err)
}

// checkMemberTarget refuses a member this value cannot name: every call here
// derives the member's state bucket from its project name, so a name that is not
// a project name names no member (config.ProjectNameRE, which WU1's roster store
// holds a workspace name to as well), and the Firebase project travels with it
// when the member has one of its own (F2, R10).
func checkMemberTarget(m workspace.MemberTarget) error {
	if !config.ProjectNameRE.MatchString(m.Project) {
		return userErr("%q is not a project name, so it names no member project: a member is named by its GCP project, and its state bucket is derived from that name rather than taken from a flag", safetext.Field(m.Project))
	}
	if m.Firebase != "" && !config.ProjectNameRE.MatchString(m.Firebase) {
		return userErr("the Firebase project %q of member %s is not a project name", safetext.Field(m.Firebase), safetext.Field(m.Project))
	}
	return nil
}

// EncodeMemberMirror renders the member list as the mirror's document
// (workspaceMirrorDoc, the shape WU5 writes and the shape the node holds),
// holding every entry to C1's `Validate` first, so a writer cannot store a
// document its own reader refuses. The entries are sorted by address and each
// address is written once, as the roster's own lists are: the object is a SET of
// people, so the bytes are a function of the set and not of the order a caller
// held it in.
//
// No entries is a mirror that lists nobody, which is a state and not a refusal:
// the database prunes the empty list, and an absent `members` reads back as the
// empty one (C1's rule for every list of its own).
func EncodeMemberMirror(people []workspace.MirrorEntry) ([]byte, error) {
	entries := make([]workspace.MirrorEntry, 0, len(people))
	for i, p := range people {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("mirror entry %d: %w", i+1, err)
		}
		if slices.ContainsFunc(entries, func(e workspace.MirrorEntry) bool { return e.Address == p.Address }) {
			return nil, fmt.Errorf("mirror entry %d: %s is in the member list twice, and a mirror holds each person once", i+1, p.Address)
		}
		entries = append(entries, p)
	}
	slices.SortFunc(entries, func(a, b workspace.MirrorEntry) int { return strings.Compare(a.Address, b.Address) })
	return json.Marshal(workspaceMirrorDoc{Version: workspaceMirrorVersion, Members: entries})
}

// DecodeMemberMirror reads the mirror's document. It refuses a document that is
// not a JSON object, one carrying a key a mirror does not have, a second JSON
// value after the first, a version this build does not write, a null `members`,
// an entry that is not an object of exactly C1's three keys, and an entry C1's
// `Validate` refuses — the one-spelling rule the roster's own reader applies
// (internal/access's `Decode`), for the reason the mirror has a shape at all:
// two spellings of one document are two documents to a reader that has no rule
// to fold them.
//
// An absent `members` is the empty list and not an unreadable document: the
// database prunes an empty one, so the absent key and the empty list are one
// node (C1's contract, and the reason WU8's own reader of this document reads it
// the same way). The entries' ORDER is not part of it either: the document is
// returned as it was written, because a member list in another order is the same
// list, and nothing here compares bytes.
func DecodeMemberMirror(raw []byte) ([]workspace.MirrorEntry, error) {
	fields, err := mirrorObject(raw)
	if err != nil {
		return nil, err
	}
	rawVersion, ok := fields["version"]
	if !ok {
		return nil, fmt.Errorf("the mirror states no %q: it is the member list the workspace put in this member's database, and a reader that did not know which version wrote it would be reading a document it does not understand", "version")
	}
	var version int
	if err := json.Unmarshal(rawVersion, &version); err != nil {
		return nil, fmt.Errorf("the mirror's version: %w", err)
	}
	if version != workspaceMirrorVersion {
		return nil, fmt.Errorf("version: %d is not the version this build writes (%d)", version, workspaceMirrorVersion)
	}
	rawMembers, ok := fields["members"]
	if !ok {
		return nil, nil
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(rawMembers, &rawEntries); err != nil {
		return nil, fmt.Errorf("the mirror's members: %w", err)
	}
	if rawEntries == nil {
		return nil, fmt.Errorf("the mirror's %q is not a list: a null there is not an empty member list", "members")
	}
	people := make([]workspace.MirrorEntry, 0, len(rawEntries))
	for i, rawEntry := range rawEntries {
		if _, err := mirrorEntryObject(rawEntry, i+1); err != nil {
			return nil, err
		}
		var entry workspace.MirrorEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return nil, fmt.Errorf("mirror entry %d: %w", i+1, err)
		}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("mirror entry %d: %w", i+1, err)
		}
		people = append(people, entry)
	}
	return people, nil
}

// mirrorObject decodes the mirror document and holds it to carrying exactly the
// two keys it has. An unknown key is a refusal and a key spelled another way is
// one too, because encoding/json matches a name case-insensitively for a struct
// field (the rule internal/access's and internal/workspace's own readers apply).
func mirrorObject(raw []byte) (map[string]json.RawMessage, error) {
	fields, err := jsonObject(raw, "the mirror")
	if err != nil {
		return nil, err
	}
	for name := range fields {
		if name != "version" && name != "members" {
			return nil, fmt.Errorf("the mirror carries %q, and its keys are %q and %q", name, "version", "members")
		}
	}
	return fields, nil
}

// mirrorEntryObject is one entry of the mirror's list, held to C1's three keys
// and to all three being there: an entry missing one is a document this program
// understands only in part, and an omitted `external` is a statement about a
// person that nobody made.
func mirrorEntryObject(raw []byte, at int) (map[string]json.RawMessage, error) {
	what := fmt.Sprintf("mirror entry %d", at)
	fields, err := jsonObject(raw, what)
	if err != nil {
		return nil, err
	}
	for name := range fields {
		if name != "address" && name != "kind" && name != "external" {
			return nil, fmt.Errorf("%s carries %q, and an entry is %q, %q and %q", what, name, "address", "kind", "external")
		}
	}
	for _, name := range []string{"address", "kind", "external"} {
		if _, ok := fields[name]; !ok {
			return nil, fmt.Errorf("%s has no %q: an entry states the address, what the person is, and whether they were admitted from outside", what, name)
		}
	}
	return fields, nil
}

// jsonObject decodes one JSON value that must be an object, and refuses a second
// value after it.
func jsonObject(raw []byte, what string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", what, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("%s is not a JSON object: a null is not a document", what)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s carries more than one JSON value", what)
	}
	return fields, nil
}
