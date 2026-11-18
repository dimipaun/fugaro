package infra

// OrchestratorAccountID is the resident orchestrator's own service account
// (mobile plan task P5.1b; docs/mobile/design/always-on-orchestrator.md
// section 4.3), created by the installation module for an installation that
// opted into one.
//
// It is a fixed account ID and not a per-installation name, like the
// coordinator's writer and the front door's: `fugaro doctor` reads the account
// back by this name, and the run's own plan review reads a member of the
// installation's project as this account's kind by it (internal/infra/tf's
// OrchestratorAccountID, the same literal — TestCoverNamesAreTheCodes is the
// pin). Because it is one account per installation, its ID is deliberately NOT
// a member of tf.FugaroAccounts: every grant rule that admits that set admits
// it for every job account the modules create, and this is the account that
// holds iam.serviceAccounts.signJwt on the budget signer.
//
// Nothing in this release writes the installation's orchestrator block (the
// variable has no CLI writer yet), so this constant is read by the doctor check
// and by that pin alone. The release that adds the writer adds the
// OrchestratorSpec beside it, as the front door's MCPServerSpec sits beside
// MCPServerAccountID.
const OrchestratorAccountID = "fugaro-orch"

// OrchestratorAccountEmail is that account's address in a project. It is one
// function because two readers have to agree on the address and neither can
// read the other's: the installation module creates the account under this ID
// and the credentials service's own environment names it as the daemon class
// (deploy/terraform/gcp/modules/installation/credentials.tf), while `fugaro
// doctor` reads the account back and the daemon mints a token as it
// (internal/cli's doctor_orchestrator_identity.go and orchestrator_identity.go).
// A second spelling of "@<project>.iam.gserviceaccount.com" is a second answer
// to "which account is the daemon", and the one that drifts is the one that
// names somebody else's account.
func OrchestratorAccountEmail(project string) string {
	return OrchestratorAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// OrchestratorPairingSecretID is the Secret Manager container holding the
// installation's copy of every pairing's MAC key (mobile plan task P5.1e; the
// design's section 4.2 and its section 4.7 step 2b): ONE container per
// installation, one version per pairing inside it.
//
// It is a fixed ID and not a per-installation name for the reason
// OrchestratorAccountID is one: the container is created by the installation
// module (deploy/terraform/gcp/modules/installation/orchestrator.tf, whose
// literal this constant is pinned to by the plan-level test), and the CLI that
// writes a version into it — `fugaro pair`, and `fugaro pair revoke` when it
// destroys one — computes no secret name of its own. A name derived from the
// project the way gcp.ProjectSecretID derives a repository's would be a second
// spelling Terraform cannot reproduce.
//
// The container carries `fugaro=managed` and the installation's name and
// deliberately NOT fugaro_secret: that label is what `fugaro update`'s
// discovery reads to decide which logical secret of the project's set a
// container holds, and this one is not a member of that set.
const OrchestratorPairingSecretID = "fugaro-orch-pairing"
