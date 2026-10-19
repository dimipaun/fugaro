package managed

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The deployer account of D75: the identity a managed apply runs as, the one
// account the installation module creates for that alone, and the three
// refusals a caller gets instead of a credential. This file is the whole of
// the resolution; nothing here applies anything, and there is no path below
// that runs work without a credential for the deployer account.
//
// Why it exists in this shape (docs/design/managed-projects.md, D75, amended
// 2026-10-16): the creating apply is owner-run and the agent holds no project
// IAM authority, so the account that applies has to be assumed rather than be
// the operator's own identity. The account's permissions are the two custom
// roles of deploy/terraform/gcp/modules/installation/deployer.tf, and the
// fence around them is an IAM condition on the managed name prefix, which is
// what makes it safe for an agent to hold: an identity that reaches the runs
// bucket could forge the marker (infra/managed.MarkerObject) and skip a
// confirmation.
//
// The three failures are distinct because a reader has to be able to tell
// which of the three one-time setup steps is missing: the account (the
// creating apply did not run), the grant (the operators' impersonation grant
// of step 2 did not land, or the caller is not one of var.operators), or the
// assumption itself (the account is there and the grant is there, and the
// call still failed). They are values as well as messages, so a caller can
// match on them without reading English (ErrNoDeployerAccount, ErrNoImpersonation
// and ErrImpersonationFailed).
var (
	// ErrNoDeployerAccount is the account does not exist in the project the
	// marker names.
	ErrNoDeployerAccount = errors.New("no deployer account")
	// ErrNoImpersonationGrant is the caller does not hold
	// roles/iam.serviceAccountTokenCreator on the account.
	ErrNoImpersonationGrant = errors.New("no impersonation grant")
	// ErrImpersonationFailed is the assumption itself failed, or returned
	// something that is not the deployer's credential.
	ErrImpersonationFailed = errors.New("the impersonation failed")
)

// Credential is an identity an apply can run as: the account it is for, and
// the token source the apply's clients are built with. Account is checked
// against the deployer account before work is called, because a credential
// for anything else is the operator's own identity wearing the wrong name —
// which is the fallback D75 rules out, and the one failure mode that would
// leave every other test in this package green.
type Credential interface {
	// Account is the email the credential is for.
	Account() string
	// Source is the token source the apply's API clients use.
	Source() oauth2.TokenSource
}

// Access is what a caller can learn about the deployer account before
// assuming it: whether it is there, and whether this caller may assume it.
// Both answers come from the project the marker names (Task 0.2's read);
// neither is a credential.
type Access interface {
	// Exists reports whether the account is in the project.
	Exists(ctx context.Context, account string) (bool, error)
	// TokenCreator reports whether member holds
	// roles/iam.serviceAccountTokenCreator on account — the one grant that
	// lets a person at the terminal assume it (deployer.tf's second step).
	TokenCreator(ctx context.Context, account, member string) (bool, error)
}

// Impersonator is what assumes the account. Self is the identity the run
// already holds: it is named rather than hidden because it is the identity a
// silent fallback would use, and naming it is what lets the grant below be
// checked against the right principal.
type Impersonator interface {
	// Self is the ambient identity of the run.
	Self() string
	// Impersonate mints a credential for the account.
	Impersonate(ctx context.Context, account string) (Credential, error)
}

// DeployerAccount is the email of the deployer account in gcpProject: the
// fixed account ID the installation module creates (tf.DeployerAccountID),
// in the project the marker names. It is tf.DeployerAccount, the same function
// the plan review uses to check which account an impersonation grant is on, so
// the account this package assumes and the account that review accepts cannot
// drift apart.
func DeployerAccount(gcpProject string) string { return tf.DeployerAccount(gcpProject) }

// Deployer is the only path in this package that runs an apply, and what it
// hands work is a credential for the deployer account of gcpProject or
// nothing at all.
//
// It resolves the account from the project the marker names (the caller
// passes what infra/managed.IsManaged read, so the project is the one the
// marker is about and not one an argument chose), checks the account is
// there, checks this caller holds the impersonation grant on it, assumes it,
// and calls work with the assumed credential. Each of the three failures
// returns before work is called, so a failed resolution applies nothing.
//
// There is no fallback, and there is no code below that could grow one: the
// credential work receives has been checked to be the deployer's own, and a
// resolver that answered with the operator's ambient credentials instead
// (the shape every impersonation library falls back to when it cannot
// assume) is refused here rather than applied with. An apply that ran as the
// operator would undo D75 while leaving the rest of this package's tests
// green, which is why the check is on the credential and not only on the
// error.
//
// A nil Access or Impersonator is refused rather than treated as "nothing to
// check": a caller that wired neither has not resolved anything, and the
// direction of that mistake has to be a refusal.
func Deployer(ctx context.Context, gcpProject string, access Access, imp Impersonator, work func(ctx context.Context, cred Credential) error) error {
	switch {
	case gcpProject == "":
		return fmt.Errorf("%w: no GCP project, so nothing names the project the deployer account is in", ErrNoDeployerAccount)
	case access == nil || imp == nil:
		return fmt.Errorf("%w: nothing resolves the deployer account of %s", ErrNoDeployerAccount, gcpProject)
	case work == nil:
		return fmt.Errorf("%w: no work to run as the deployer of %s", ErrImpersonationFailed, gcpProject)
	}
	account := DeployerAccount(gcpProject)
	ok, err := access.Exists(ctx, account)
	switch {
	case err != nil:
		return fmt.Errorf("%w: reading whether %s exists in %s: %w", ErrNoDeployerAccount, account, gcpProject, err)
	case !ok:
		return fmt.Errorf("%w: %s does not exist in %s — the owner-run creating apply creates it, and a managed apply with no deployer account is refused rather than run as you",
			ErrNoDeployerAccount, account, gcpProject)
	}
	self := imp.Self()
	granted, err := access.TokenCreator(ctx, account, self)
	switch {
	case err != nil:
		return fmt.Errorf("%w: reading %s's IAM policy for %s: %w", ErrNoImpersonationGrant, account, self, err)
	case !granted:
		return fmt.Errorf("%w: %s does not hold roles/iam.serviceAccountTokenCreator on %s — the owner-run creating apply grants it to var.operators, and a managed apply that cannot assume the account is refused rather than run as %s",
			ErrNoImpersonationGrant, self, account, self)
	}
	cred, err := imp.Impersonate(ctx, account)
	if err != nil {
		return fmt.Errorf("%w: assuming %s as %s: %w", ErrImpersonationFailed, account, self, err)
	}
	if cred == nil {
		return fmt.Errorf("%w: assuming %s as %s returned no credential", ErrImpersonationFailed, account, self)
	}
	if got := cred.Account(); got != account {
		return fmt.Errorf("%w: the credential is for %s, not for %s — an apply runs as the deployer account of %s or it does not run",
			ErrImpersonationFailed, got, account, gcpProject)
	}
	return work(ctx, cred)
}
