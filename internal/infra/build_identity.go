package infra

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// BuildIdentityAccountID is the project's build identity (B5, D2301): the ONE
// service account a project's catalog builds run as, in the installation's
// project, created by the same apply that turns the credentials service on
// (deploy/terraform/gcp/modules/installation/build_identity.tf, whose
// account_id is this literal).
//
// It is a per-PROJECT account and not a repository's, and that is the whole of
// the task: a repository's build account (gcp.BuildServiceAccountID) is derived
// from the repository and dies with the repository's root, while a catalog
// build bakes an entry that may name repositories whose root is gone (M2) — so
// the account it runs as has to be one that outlives every repository the
// entry names.
//
// It is a constant here because the module's account ID is a LITERAL, which is
// what makes the address a fact of the configuration rather than of the apply
// (VerifierAccountID's own reason): `<id>@<project>.iam.gserviceaccount.com`,
// computable before anything is planned. internal/infra/tf's
// BuildIdentityAccountID is the same literal on the cover's side, and
// TestProjectHasOneBuildIdentity (this package) reads the module's own
// account_id; the address is what B6 hands CatalogBuildSpec and what the
// module renders as the service's build class.
const BuildIdentityAccountID = "fugaro-build-identity"

// BuildIdentityEmail is the address of the project's build identity in
// gcpProject: the account BuildIdentityAccountID names.
//
// It is the email B6's CatalogBuildSpec carries as the account a catalog build
// runs as (a Cloud Build request names an email, never an ID), and it is the
// value the installation module renders into the credentials service's build
// class from the account's own resource — one address, read twice.
func BuildIdentityEmail(gcpProject string) string {
	return serviceAccountEmail(BuildIdentityAccountID, gcpProject)
}

// buildIdentityGitSecretRowPrefix is the address of the per-repository secret
// accessor the build identity held until IS8 (D3330): one instance per
// repository of the installation's catalog check whose provider credential the
// project names, and one more for the repository's own container when it holds
// a live version (modules/installation/build_identity.tf's for_each, its keys
// "<slug>" and "<slug>/own"). The resource is declared NOWHERE in the tree
// since that task — a catalog entry's build mounts no secret and reads its git
// credential from the credentials service instead — so the plan of a state that
// still holds the instances is a plan of deletes at these addresses and at no
// others.
//
// The opening bracket is part of the prefix and not a nicety: without it
// ...build_identity_git_secret would also prefix a row of another name, and a
// prefix that named a row IS8 does not take away would be an address the guard
// admits that nobody decided to remove (IdentityAllowDelete's own reason for
// its brackets).
const buildIdentityGitSecretRowPrefix = "module.installation.google_secret_manager_secret_iam_member.build_identity_git_secret["

// BuildIdentityAccessorAllowDelete is the guard's allow-list for p when the
// catalog build identity's per-repository secret accessors leave with IS8
// (D3330): each delete in p at one of those addresses, and nothing else.
//
// It exists because that delete is this program's own decision and must not
// cost the operator a flag or a second command (CLAUDE.md's HARD RULE about
// `fugaro update`): the apply that lands the change plans the destroy of every
// instance the installation's state holds, the guard refuses a delete no list
// names, and without this list an installation at the release behind would stop
// at `fugaro update --allow-delete <address>` — the step the hard rule forbids.
// For a managed project the same addresses reach the gate as this binary's own
// removals (init's gateStep, beside infra.IdentityAllowDelete's rows), where
// GuardManaged admits them rather than warning about an unannounced removal.
//
// Unlike IdentityAllowDelete it takes no switch, and that is not an oversight:
// the switch there is what tells a removal this program made from one a
// document made (a member dropped from the roster), while the resource here is
// declared nowhere in the tree at all — the module's block is gone rather than
// turned off — so no plan any later tree produces carries a delete at one of
// these addresses, and this list cannot become a standing approval of an
// address whose deletion means something else. It is the plan's own beside
// that: an address p does not delete is not named, so a run whose state read
// failed still converges.
func BuildIdentityAccessorAllowDelete(p *tf.Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if !slices.Contains(rc.Change.Actions, "delete") {
			continue
		}
		if strings.HasPrefix(rc.Address, buildIdentityGitSecretRowPrefix) {
			out = append(out, rc.Address)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
