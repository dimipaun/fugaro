package projectpolicy

import "fmt"

// Version is the policy schema version this code understands. Version is the
// only policy field that can refuse a node (P8): every other field is
// tolerated, so a newer policy does not break an older runner on a key it does
// not know (a newer Fugaro that adds a budget key bumps Version), but a
// version this code does not understand is a node it must not use (fail
// closed, §5(3)).
//
// Version 2 (plan task 3.3) is the first whose runner enforces a profile's
// allow_repos and binds a run of a project job to the profile it serves. A
// version-1 runner decodes the profiles block and ignores it, so a launcher
// that pointed a run of a profile's job (FUGARO_REMOTE, FUGARO_RUN) at another
// repository of the project's allow_repos would run it with that profile's
// secrets: project jobs run the installation's base image, which can lag the
// CLI that published the policy. The bump makes that runner refuse the node
// (RunnerSkew) instead of running past a gate it does not know. The shape is
// version 1's; only what a runner must enforce changed.
const Version = 2

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
//		msg = fmt.Sprintf(projectpolicy.RunnerSkew, ve.Found, ve.Understood) // the runner, task 1.3
//		msg = fmt.Sprintf(projectpolicy.CLISkew, ve.Found, ve.Understood)   // the CLI, task 1.6
//	}
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
// CLISkew the CLI's (this Fugaro is older than the node, so publishing from it
// would write the policy backwards). Each takes the found version and the
// understood one, in that order.
const (
	Skew       = "the project policy is version %d; this Fugaro understands version %d"
	RunnerSkew = Skew + "; refresh the runner image (fugaro upgrade), or publish a policy this runner understands"
	CLISkew    = Skew + "; upgrade Fugaro"
)
