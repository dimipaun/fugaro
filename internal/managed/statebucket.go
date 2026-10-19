package managed

import (
	"context"
	"fmt"
	"slices"
	"strings"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// D77's dedicated state bucket: where the agent-authored root's Terraform
// state lives, and the one place the deployer holds object access (Task 1.5
// (3) of docs/plans/2026-10-16-managed-projects.md).
//
// Why dedicated, rather than a prefix of the installation's state bucket
// (docs/design/managed-projects.md, D77, answered 2026-10-16): the deployer's
// two custom roles deliberately hold no project-level object authority and no
// project-level IAM permission, so the deployer's access to the state has to
// be a bucket-level binding. A binding on the installation's state bucket
// would carry the agent's apply into fugaro/installation and fugaro/firebase
// (infra.StatePrefix*) and into every repository's fugaro/repos/ state
// (internal/infra/workdir.go), which is the state of the owner-run roots. A
// bucket of its own is the smallest thing that carries the agent's state and
// nothing else, and the deployer's write access is bound on that bucket alone.
// The operators read it for the same reason they read the installation's
// state: the managed apply's inventory compares the project with what both
// states account for, and makes that read with the operator's own credential.
//
// The bucket lives in the managed project, so it dies with the project, and a
// restore re-creates it empty and rebuilds from zero (D66, Task 5.2).
//
// The step below runs in the owner-run managed creation, after the
// installation apply and beside Task 0.1's marker write, as the operator's own
// identity: it is setup, not an apply, and nothing in a managed plan can
// reach the bucket (the deployer's scoped condition does not admit its name —
// nothing but binding (3) does).

// managedStatePrefix is the prefix of D77's bucket. It carries none of the
// installation's state prefixes and no part of the managed resource prefix
// (tf.ManagedPrefix, "fmgd-"): the deployer's project-level role is bound
// under a condition admitting the latter, and the state bucket is reached by
// binding (3) alone, so the two must not overlap.
const managedStatePrefix = "fugaro-managed-tfstate-"

// DeployerStateRole is the one role the deployer holds on the state bucket:
// read and write of its objects, and nothing else on the project.
const DeployerStateRole = "roles/storage.objectAdmin"

// OperatorStateRole is the read-only role the operators hold on the state
// bucket, beside the deployer's read and write. The managed apply reads this
// bucket before it assumes anything (Task 4.5: the whole-project inventory
// set against what the installation's states account for), and it makes that
// read with the operator's own credential — the deployer is bound here and
// nowhere else, and the run is not the deployer yet when the read happens. An
// operator with no grant on the bucket reads it as denied, and a state read
// that cannot be made is the fallback (D59): every managed apply by an
// operator who is not a project owner would keep its confirmations forever.
//
// Read and not write: the state of the agent-authored root is written by the
// deployer through Terraform, and nothing an operator does in the inventory
// writes anything.
const OperatorStateRole = "roles/storage.objectViewer"

// ManagedStateBucketName is D77's bucket for gcpProject: the bucket's name is
// global, so it carries the project ID, exactly as the installation's state
// bucket does (infra's fugaro-tfstate-<gcp-project>).
//
// The name is a function of the project alone. It is never taken from a flag,
// a tfvars file or a plan: a name a caller could choose is a name that could
// be pointed at the installation's own state bucket.
func ManagedStateBucketName(gcpProject string) string {
	return managedStatePrefix + gcpProject
}

// InstallationsStateBucket is the installation's own state bucket for
// gcpProject, which this step must never create or bind on. It is here so the
// test that says the two names differ reads the same constant the rest of the
// program does.
func InstallationsStateBucket(gcpProject string) string { return "fugaro-tfstate-" + gcpProject }

// StateStore is the storage this step needs, as internal/infra provides it:
// whether the bucket is there, creating it, reading its policy and setting it.
// It is an interface so this package's tests need no cloud client, and so the
// one production implementation (InfraStore) is the only place that touches
// internal/infra's clients.
type StateStore interface {
	// Check reports whether the bucket exists in project and is this
	// project's own state bucket (infra.CheckStateBucket: the project number
	// and the fugaro=tfstate mark).
	Check(ctx context.Context, project, bucket string) (bool, error)
	// Create creates the bucket in region with infra's state-bucket
	// attributes (versioned, uniform access, the mark).
	Create(ctx context.Context, project, region, bucket string) error
	// Policy reads the bucket's IAM policy.
	Policy(ctx context.Context, bucket string) (*storage.Policy, error)
	// SetPolicy writes it.
	SetPolicy(ctx context.Context, bucket string, p *storage.Policy) error
}

// InfraStore is the StateStore internal/infra's clients are. It is the only
// implementation init passes, and every method here is a call to the function
// that already exists there: this step creates the bucket the same way the
// installation's own is created, so the two cannot drift.
type InfraStore struct{ Clients *infra.Clients }

func (s InfraStore) Check(ctx context.Context, project, bucket string) (bool, error) {
	return infra.CheckStateBucket(ctx, s.Clients, project, bucket)
}

func (s InfraStore) Create(ctx context.Context, project, region, bucket string) error {
	return infra.CreateStateBucket(ctx, s.Clients, project, region, bucket)
}

func (s InfraStore) Policy(ctx context.Context, bucket string) (*storage.Policy, error) {
	return infra.BucketPolicy(ctx, s.Clients, bucket)
}

func (s InfraStore) SetPolicy(ctx context.Context, bucket string, p *storage.Policy) error {
	if _, err := s.Clients.Storage.Buckets.SetIamPolicy(bucket, p).Context(ctx).Do(); err != nil {
		return fmt.Errorf("setting the IAM policy of gs://%s: %w", safetext.Field(bucket), safetext.FieldErr(err))
	}
	return nil
}

// EnsureManagedStateBucket is D77's whole setup step: the dedicated state
// bucket of gcpProject exists in the project and region, and carries two
// bindings — the deployer's read and write (DeployerStateRole) and the
// operators' read (OperatorStateRole) — and nothing wider. It is idempotent —
// a bucket and bindings that are already there are left alone, so a second
// managed creation on the same project changes nothing.
//
// The bucket's name and the deployer member are both computed from project
// here and never passed in: the caller chooses the project, the region the
// installation was created in, and the installation's operators, and nothing
// else about what is created. The operators are the one thing the caller
// names, because they are the identities the installation's Terraform names
// too (var.operators): the inventory read is theirs, so they have to be able
// to make it. A foreign bucket under that name (another project's, or one
// without the state mark) is refused by infra.CheckStateBucket rather than
// adopted, and a policy write never happens before that check.
func EnsureManagedStateBucket(ctx context.Context, s StateStore, project, region string, operators []string) error {
	switch {
	case s == nil:
		return fmt.Errorf("no storage to create the managed state bucket of %s with", safetext.Field(project))
	case project == "":
		return fmt.Errorf("no project: the managed state bucket's name is the project's, and an empty one would read as the state bucket of no project")
	case region == "":
		return fmt.Errorf("no region for the managed state bucket of %s: without one there is nowhere to create it", safetext.Field(project))
	}
	bucket := ManagedStateBucketName(project)
	if bucket == InstallationsStateBucket(project) {
		// Unreachable while the two prefixes differ; a change that made them
		// equal would otherwise bind the deployer on the installation's state.
		return fmt.Errorf("refusing to manage gs://%s as the managed state bucket: %s is the installation's own state bucket", safetext.Field(bucket), safetext.Field(bucket))
	}
	ok, err := s.Check(ctx, project, bucket)
	switch {
	case err != nil:
		return fmt.Errorf("reading the managed state bucket gs://%s: %w", safetext.Field(bucket), safetext.FieldErr(err))
	case !ok:
		if err := s.Create(ctx, project, region, bucket); err != nil {
			return fmt.Errorf("creating the managed state bucket gs://%s: %w", safetext.Field(bucket), safetext.FieldErr(err))
		}
	}
	p, err := s.Policy(ctx, bucket)
	if err != nil {
		return err
	}
	member := "serviceAccount:" + DeployerAccount(project)
	bindings, changed, err := withDeployerGrant(p.Bindings, DeployerStateRole, member)
	if err != nil {
		return fmt.Errorf("granting %s on gs://%s: %w", DeployerStateRole, safetext.Field(bucket), safetext.FieldErr(err))
	}
	for _, operator := range operators {
		granted, added, err := withOperatorGrant(bindings, operator)
		if err != nil {
			return fmt.Errorf("granting %s on gs://%s: %w", OperatorStateRole, safetext.Field(bucket), safetext.FieldErr(err))
		}
		bindings, changed = granted, changed || added
	}
	if !changed {
		return nil
	}
	out := &storage.Policy{Etag: p.Etag, Version: p.Version, Bindings: bindings}
	if err := s.SetPolicy(ctx, bucket, out); err != nil {
		return err
	}
	return nil
}

// withMember is the binding itself: p with member added to role's
// unconditional binding — created when there is none — or p unchanged and
// changed=false when the member is already there. Every other binding and
// every condition on them is carried over untouched: this step adds members to
// two roles and removes nothing, and a policy that is rewritten is a policy
// that could drop someone else's access.
//
// A nil binding in the policy is dropped rather than carried: it names no role
// and no member, and a SetIamPolicy that carried it would be refused for the
// whole write, leaving the bucket bound to nobody.
func withMember(p []*storage.PolicyBindings, role, member string) ([]*storage.PolicyBindings, bool) {
	out := make([]*storage.PolicyBindings, 0, len(p)+1)
	for _, b := range p {
		if b == nil {
			continue
		}
		nb := *b
		nb.Members = slices.Clone(b.Members)
		out = append(out, &nb)
	}
	for _, b := range out {
		// A conditioned binding of the same role is not this one: this access
		// is unconditional, and adding a member under someone else's condition
		// would grant it only there.
		if b.Role == role && b.Condition == nil {
			if slices.Contains(b.Members, member) {
				return p, false
			}
			b.Members = append(b.Members, member)
			return out, true
		}
	}
	return append(out, &storage.PolicyBindings{Role: role, Members: []string{member}}), true
}

// withDeployerGrant is the deployer's binding: withMember, with the member
// checked first.
//
// The member always carries its "serviceAccount:" prefix: a grant to a bare
// email address is a grant to nobody, and the one identity this step is about
// is always a service account. A member that is not one is an error and not
// changed=false: false is what withMember says about a member that is already
// bound, and the caller takes it as "nothing to do" and reports the step done
// — so an unbindable member reported that way would be a run that says it
// granted the deployer access it never granted.
func withDeployerGrant(p []*storage.PolicyBindings, role, member string) ([]*storage.PolicyBindings, bool, error) {
	acct, ok := strings.CutPrefix(member, "serviceAccount:")
	if !ok || acct == "" {
		return nil, false, fmt.Errorf("%q is not a service account member: the deployer's grant names serviceAccount:<address>, and anything else is a grant to nobody", safetext.Field(member))
	}
	out, changed := withMember(p, role, member)
	return out, changed, nil
}

// operatorMemberTypes are the member types an operator may be named by: the
// same four the installation module's var.operators admits
// (deploy/terraform/gcp/modules/installation/variables.tf), so the identities
// this step binds are the identities that module binds.
var operatorMemberTypes = []string{"user", "group", "serviceAccount", "domain"}

// withOperatorGrant is the operators' read on the bucket: withMember again,
// with the member checked first. An operator is an IAM member like the ones
// var.operators carries, and a member that names no identity — or names one in
// a shape the installation module would refuse — is an error for the reason
// the deployer's is: withMember's false is "already bound", the caller reads
// it as "nothing to do", and a run that reported the step done over a grant
// nobody holds is a managed apply that reads the state as denied and keeps its
// confirmations forever.
func withOperatorGrant(p []*storage.PolicyBindings, member string) ([]*storage.PolicyBindings, bool, error) {
	owner, addr, ok := strings.Cut(member, ":")
	if !ok || !slices.Contains(operatorMemberTypes, owner) || addr == "" || strings.ContainsAny(addr, " ") {
		return nil, false, fmt.Errorf("%q is not an operator member: an operator is one of %s followed by the address (var.operators), and anything else is a grant to nobody", safetext.Field(member), strings.Join(operatorMemberTypes, ", "))
	}
	out, changed := withMember(p, OperatorStateRole, member)
	return out, changed, nil
}
