package infra

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
