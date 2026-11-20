package infra

// The IAM calls the SA authority makes (WU20): the workspace's IAM account is
// granted roles/owner on a member's projects (R10) and impersonated through the
// token-creator binding a workspace admin holds on it (D2803), with the admin's
// own roles/owner on the workspace's own project beside it (R8).
//
// Everything here runs with the caller's own credentials: a member owner grants
// the account on their own project (D2819, A6) and a workspace admin binds
// themselves. Nothing here impersonates anybody. Nothing here creates, reads or
// lists a KEY of any account — no serviceAccountKeys call exists in this file —
// which is F20's whole point: the account is impersonated, and that is the only
// shape the design gives its authority.
//
// The write discipline is folder.go's, for the same reasons: a policy is read
// and written back whole under the read's etag, a binding carrying a condition
// is another writer's and is neither flattened nor dropped, and a change that
// would change nothing is not written at all.
//
// UNVERIFIED against the real service, as the project stage is (projectcreate.go
// lists the rest): the fakes encode what the documentation says rather than what
// was observed. The one open question here is U8's — whether Google lets a
// person be granted roles/owner on a project by API outside an organization, or
// only by accepting an invitation in the console — and ownerInviteRefusal below
// says what this file does about it until Check 720 answers.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"

	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	// SAProjectOwnerRole is roles/owner on a project: the role the workspace
	// IAM account holds on every member (R10) and a workspace admin holds on
	// the workspace's own project (R8), where the roster node and the
	// workspace's audit trail live.
	SAProjectOwnerRole = "roles/owner"

	// SATokenCreatorRole is roles/iam.serviceAccountTokenCreator on the
	// account, which is what lets a workspace admin act as it (D2803). It is
	// the role tf.DeployerTokenCreatorRole names too, and deliberately a
	// second constant: one is a grant to the installation's operators on the
	// deployer account and the other a grant to a workspace's admins on the
	// workspace account, and a change to one is not a change to the other.
	SATokenCreatorRole = "roles/iam.serviceAccountTokenCreator"
)

// SAProjects is the IAM surface behind the SA authority: the calls
// internal/workspace's SAAuthority makes, with the caller's own credentials (a
// member owner's for the member's projects, a workspace admin's for the
// workspace's own).
//
// The project side is Resource Manager v1 — the client Clients.CRM is, and the
// one firebase.go reads project policies through — and the account side is IAM
// v1.
//
// Every member argument is an IAM member string, prefix and all:
// serviceAccount:<address> for the workspace's account, user:<address> or
// group:<address> for a person. That is the only spelling a policy write or a
// read resolves, so the caller composes it (workspace.SAAuthority.accountMember)
// and nothing here strips or adds the prefix.
type SAProjects struct {
	crm *crm.Service
	iam *iam.Service
}

// NewSAProjects wraps the clients the caller already built (NewClients); it
// connects to nothing of its own.
func NewSAProjects(crm *crm.Service, iam *iam.Service) *SAProjects {
	return &SAProjects{crm: crm, iam: iam}
}

// GrantOwner gives member roles/owner on project, which is the grant that makes
// the workspace the member's authority (R10). A member already holding the role
// unconditionally costs one read and no write, so a link interrupted by a
// refusal finishes by being run again.
//
// The one refusal that is not reported as itself is U8's: Google refusing an
// owner grant by API outside an organization. It is wrapped with
// workspace.ErrSAOwnerInvite so that the caller can print the console step
// (D2825) instead of a permission error, and the API's own answer stays in the
// message and in the chain, because whether the refusal is U8's or another is
// exactly what a person reading it has to judge.
//
// Only the WRITE's refusal is read that way (ownerWriteError). A policy that
// cannot be READ fails before a grant is attempted, and answering that with an
// invitation to accept would send the caller to a console page that changes
// nothing about the read.
func (s *SAProjects) GrantOwner(ctx context.Context, project, member string) error {
	err := s.changeOwners(ctx, project, "", func(owners []string) []string {
		if slices.Contains(owners, member) {
			return owners
		}
		return append(owners, member)
	})
	var write *ownerWriteError
	if errors.As(err, &write) && ownerInviteRefusal(err) {
		return fmt.Errorf("%w (%w)", err, workspace.ErrSAOwnerInvite)
	}
	return err
}

// RemoveOwner drops member's roles/owner binding on project. A member a
// CONDITIONAL roles/owner binding names is refused rather than left as it is,
// as folder.go's RemoveOwner refuses one: this call does not write that binding,
// the member keeps the role — and so keeps the workspace's authority over the
// project — and reporting a removal that did not happen is worse than refusing.
func (s *SAProjects) RemoveOwner(ctx context.Context, project, member string) error {
	return s.changeOwners(ctx, project, member, func(owners []string) []string {
		return slices.DeleteFunc(owners, func(o string) bool { return o == member })
	})
}

// Owns reports whether member holds roles/owner on project now, counting only
// the unconditional binding.
//
// A conditional binding is NOT counted, and the direction of that choice is the
// one deployerAccess.TokenCreator takes for the same question: this answer is a
// GATE (the workspace's authority, R11), a condition cannot be evaluated here,
// and "the grant is missing" is the mistake a gate has to make. The cost of it
// is a member whose grant Fugaro reads as absent and grants again — a write that
// adds the unconditional binding beside the conditioned one, which converges.
func (s *SAProjects) Owns(ctx context.Context, project, member string) (bool, error) {
	pol, err := s.projectPolicy(ctx, project)
	if err != nil {
		return false, err
	}
	for _, b := range pol.Bindings {
		if b.Role != SAProjectOwnerRole || b.Condition != nil {
			continue
		}
		if slices.Contains(b.Members, member) {
			return true, nil
		}
	}
	return false, nil
}

// TokenCreators lists the members bound to SATokenCreatorRole on the account.
// AdminBindings reads it, and D2823 compares the workspace node's `Admins`
// against it, which is why it counts the unconditional binding only (Owns says
// why) and why a member Google marks deleted is left out: it can no longer act
// as the account, and the policy keeps the spelling only until the principal's
// grants are cleaned up.
func (s *SAProjects) TokenCreators(ctx context.Context, account string) ([]string, error) {
	pol, err := s.accountPolicy(ctx, account)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range pol.Bindings {
		if b.Role != SATokenCreatorRole || b.Condition != nil {
			continue
		}
		for _, m := range b.Members {
			if strings.HasPrefix(m, "deleted:") {
				continue
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// AddTokenCreator binds member to SATokenCreatorRole on the account: the grant
// that lets a workspace admin impersonate it and so reach every member the
// account is an owner of (D2818). A member already bound costs one read and no
// write, so an admin add that a later refusal interrupts finishes by being run
// again.
func (s *SAProjects) AddTokenCreator(ctx context.Context, account, member string) error {
	return s.changeTokenCreators(ctx, account, "", func(creators []string) []string {
		if slices.Contains(creators, member) {
			return creators
		}
		return append(creators, member)
	})
}

// RemoveTokenCreator drops that binding, on the terms RemoveOwner refuses a
// conditional grant on: a member a conditional token-creator binding names keeps
// the ability to impersonate the account, and this call says so rather than
// reporting a removal it did not make.
func (s *SAProjects) RemoveTokenCreator(ctx context.Context, account, member string) error {
	return s.changeTokenCreators(ctx, account, member, func(creators []string) []string {
		return slices.DeleteFunc(creators, func(c string) bool { return c == member })
	})
}

// projectPolicy reads project's IAM policy at the version that carries
// conditions, so that a conditional binding is read rather than dropped by a
// version-1 read (which the API refuses outright when the policy has one).
func (s *SAProjects) projectPolicy(ctx context.Context, project string) (*crm.Policy, error) {
	pol, err := s.crm.Projects.GetIamPolicy(project, &crm.GetIamPolicyRequest{
		Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if err != nil {
		return nil, classify(err, "reading the IAM policy of project "+project)
	}
	return pol, nil
}

// changeOwners rewrites the project's unconditional roles/owner binding through
// change and writes the policy back, leaving the project's other bindings, its
// audit-logging configuration and its remaining fields as they were read. Only
// a binding with no condition is the authority's own: nothing here writes a
// conditional owner binding, so one belongs to another writer, and flattening it
// into the unconditional binding would widen a grant that was meant to hold only
// under its condition.
func (s *SAProjects) changeOwners(ctx context.Context, project, removing string, change func([]string) []string) error {
	pol, err := s.projectPolicy(ctx, project)
	if err != nil {
		return err
	}
	var (
		owners      []string
		conditioned []string
		rest        []*crm.Binding
		replaced    bool
	)
	for _, b := range pol.Bindings {
		if b.Role != SAProjectOwnerRole {
			rest = append(rest, b)
			continue
		}
		if b.Condition != nil {
			conditioned = append(conditioned, b.Members...)
			rest = append(rest, b)
			continue
		}
		owners = append(owners, b.Members...)
		replaced = true
	}
	if removing != "" && slices.Contains(conditioned, removing) {
		return fmt.Errorf("removing %s from project %s: it is also bound to %s there under a condition, which is another writer's binding and not one this call can drop — dropping the unconditional one would leave the role on the project; remove that binding in the Cloud Console first", removing, project, SAProjectOwnerRole)
	}
	members := change(owners)
	if slices.Equal(members, owners) {
		// Nothing to add or to drop: the policy is not written, so a no-op never
		// moves the etag another writer is holding.
		return nil
	}
	out := append([]*crm.Binding{}, rest...)
	if len(members) > 0 {
		out = append(out, &crm.Binding{Role: SAProjectOwnerRole, Members: members})
	} else if !replaced {
		return nil
	}
	// SetIamPolicy replaces the policy whole, so everything the read answered
	// and this call did not change goes back with it: the project's
	// audit-logging configuration is not an owner and has no business being
	// dropped by a change to who the owners are.
	_, err = s.crm.Projects.SetIamPolicy(project, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{Etag: pol.Etag, Version: 3, Bindings: out, AuditConfigs: pol.AuditConfigs},
	}).Context(ctx).Do()
	if err != nil {
		return &ownerWriteError{classify(err, "changing the owners of project "+project)}
	}
	return nil
}

// ownerWriteError marks a failure of the owner WRITE, as told apart from the
// read that comes before it. Only a write's refusal can be U8's — no invitation
// is being extended when a policy cannot be read — and GrantOwner's
// classification of it says why that matters.
type ownerWriteError struct{ err error }

func (e *ownerWriteError) Error() string { return e.err.Error() }
func (e *ownerWriteError) Unwrap() error { return e.err }

// accountPolicy reads the account's IAM policy at the version that carries
// conditions.
func (s *SAProjects) accountPolicy(ctx context.Context, account string) (*iam.Policy, error) {
	pol, err := s.iam.Projects.ServiceAccounts.GetIamPolicy(account).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		return nil, classify(err, "reading the IAM policy of "+account)
	}
	return pol, nil
}

// changeTokenCreators rewrites the account's unconditional token-creator binding
// through change and writes the policy back, leaving the account's other
// bindings and its audit-logging configuration as they were read. The reasoning
// is changeOwners', on the one role an admin holds here.
func (s *SAProjects) changeTokenCreators(ctx context.Context, account, removing string, change func([]string) []string) error {
	pol, err := s.accountPolicy(ctx, account)
	if err != nil {
		return err
	}
	var (
		creators    []string
		conditioned []string
		rest        []*iam.Binding
		replaced    bool
	)
	for _, b := range pol.Bindings {
		if b.Role != SATokenCreatorRole {
			rest = append(rest, b)
			continue
		}
		if b.Condition != nil {
			conditioned = append(conditioned, b.Members...)
			rest = append(rest, b)
			continue
		}
		creators = append(creators, b.Members...)
		replaced = true
	}
	if removing != "" && slices.Contains(conditioned, removing) {
		return fmt.Errorf("removing %s as a token creator of %s: it is also bound to %s there under a condition, which is another writer's binding and not one this call can drop — dropping the unconditional one would leave it able to impersonate the account; remove that binding in the Cloud Console first", removing, account, SATokenCreatorRole)
	}
	members := change(creators)
	if slices.Equal(members, creators) {
		return nil
	}
	out := append([]*iam.Binding{}, rest...)
	if len(members) > 0 {
		out = append(out, &iam.Binding{Role: SATokenCreatorRole, Members: members})
	} else if !replaced {
		return nil
	}
	_, err = s.iam.Projects.ServiceAccounts.SetIamPolicy(account, &iam.SetIamPolicyRequest{
		Policy: &iam.Policy{Etag: pol.Etag, Version: 3, Bindings: out, AuditConfigs: pol.AuditConfigs},
	}).Context(ctx).Do()
	if err != nil {
		return classify(err, "changing the token creators of "+account)
	}
	return nil
}

// ownerInviteRefusal reports whether Google's answer to an owner write is the
// refusal U8 is about: the grant the API does not make outside an organization,
// which a person has to accept as an invitation in the console instead.
//
// UNVERIFIED, and the one line in this file that is a guess about the API rather
// than its documentation. The two codes are those a policy write refused for a
// reason of its own carries (INVALID_ARGUMENT, FAILED_PRECONDITION); a 403 is
// deliberately NOT one of them, because that is also how Google answers a caller
// who may not write the policy at all, and telling that caller to look for an
// invitation would hide a permission they can fix. Whether an owner grant by API
// really is refused, and with which code, is what the owner-run Check 720 of
// docs/gcp-live-checklist.md settles (WU17); if the real answer is another code,
// this is the one place to change.
func ownerInviteRefusal(err error) bool {
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	return ge.Code == http.StatusBadRequest || ge.Code == http.StatusPreconditionFailed
}
