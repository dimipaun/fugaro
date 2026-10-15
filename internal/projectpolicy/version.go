package projectpolicy

import "fmt"

// Version is the policy schema version this code understands. Version is the
// only policy field that can refuse a node (P8): every other field is
// tolerated, so a newer policy does not break an older runner on a key it does
// not know (a newer Fugaro that adds a budget key bumps Version), but a
// version this code does not understand is a node it must not use (fail
// closed, §5(3)).
const Version = 1

// RulesVersion is the generation of the Realtime Database security rules that
// understand this policy: the config/policy nodes of §5 and their per-child
// read grants. It is recorded on the database as
// config/limits/policyRules (PathRulesMarker), written by the owner-run rules
// deploy and only after it succeeds (internal/infra's DB.Plan orders it after
// the PutRules action), and read by the runner before it reads a policy (task
// 1.3), which must not decode a policy its deployed rules do not protect.
//
// A rules generation that changes the policy nodes bumps this. It is not
// Version: the policy schema and the rules that guard it move independently.
const RulesVersion = 1

// VersionError is the refusal of a node written in a version this Fugaro does
// not understand. It carries both versions, so each side prints its own
// remedy without matching on the message:
//
//	var ve *projectpolicy.VersionError
//	if errors.As(err, &ve) {
//		msg = fmt.Sprintf(projectpolicy.RunnerSkew, ve.Found, ve.Understood)  // the runner, task 1.3
//		msg = fmt.Sprintf(projectpolicy.CLISkew, ve.Found, ve.Understood)     // the CLI, node newer
//		msg = fmt.Sprintf(projectpolicy.CLISkewNewer, ve.Found, ve.Understood) // the CLI, node older
//	}
//
// Found and Understood are what tells the two CLI directions apart
// (Found > Understood is the older binary), and internal/cli's cliSkewSentence
// is the one place that picks between them.
//
// Error is the shared sentence, with no side's remedy.
type VersionError struct {
	Found      int // the version on the node
	Understood int // the version this Fugaro understands
}

// Error is Skew: the runner's and the CLI's messages are the same sentence
// plus their own remedy, so a caller that does not care which side it is on
// still gets a message that names both versions.
func (e *VersionError) Error() string { return fmt.Sprintf(Skew, e.Found, e.Understood) }

// The version-skew messages. Skew is the sentence both sides print; RunnerSkew
// is the runner's refusal (the image is older than the published policy) and
// the CLI has two, one per direction of the skew, because the CLI is the side an
// operator upgrades and the remedy is not the same in both:
//
//   - CLISkew: this Fugaro is older than the node, so publishing from it would
//     write the policy backwards and nothing here can read the node — the
//     upgrade is missing on this machine.
//   - CLISkewNewer: the node is older than this Fugaro. Nothing is upgraded —
//     this binary is the newer side — and `fugaro init --firebase` is the
//     remedy: it deploys this release's rules and marker, so the policy can then
//     be published in this release's shape. A hand-written node, or one a
//     release before this shape wrote, is the only way to get here (Decode
//     refuses a version this Fugaro does not know, and a node below Version is
//     not the legacy path — that is the marker's job, Read).
//
// A message that printed CLISkew for both directions sent the operator of a node
// that was merely behind to upgrade a Fugaro that was already ahead of it, and
// one that printed the newer direction's re-run of init at an older binary would
// redeploy the release's rules template over rules a newer Fugaro wrote (the
// reasoning requirePolicyRules uses for the marker). Each takes the found
// version and the understood one, in that order.
const (
	Skew         = "the project policy is version %d; this Fugaro understands version %d"
	RunnerSkew   = Skew + "; refresh the runner image (fugaro upgrade), or publish a policy this runner understands"
	CLISkew      = Skew + "; upgrade Fugaro (fugaro upgrade)"
	CLISkewNewer = Skew + "; the node is older than this Fugaro: re-run 'fugaro init --firebase' with this release to deploy the current rules and marker, then publish the policy in this release's shape"
)
