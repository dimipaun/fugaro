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
