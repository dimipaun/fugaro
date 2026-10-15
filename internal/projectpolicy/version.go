package projectpolicy

// Version is the policy schema version this code understands. Version is the
// only policy field that can refuse a node (P8): every other field is
// tolerated, so a newer policy does not break an older runner on a key it does
// not know, but a version this code does not understand is a node it must not
// use (fail closed, §5(3)).
const Version = 1

// The version-skew refusals. Both sides print the same sentence and name their
// own remedy, so a run that halted and a CLI that refused read alike; the
// runner (phase 1 task 1.3) and the CLI (task 1.6) use these constants rather
// than spelling the message again.
const (
	// Skew is the shared sentence: %d is the version found, %d the version
	// this Fugaro understands.
	Skew = "the project policy is version %d; this Fugaro understands version %d"

	// RunnerSkew is the runner's refusal: the runner image is older than the
	// policy the operator published.
	RunnerSkew = Skew + "; refresh the runner image (fugaro upgrade), or publish a policy this runner understands"

	// CLISkew is the CLI's refusal: this Fugaro is older than the policy on
	// the database, so publishing from it would write it backwards.
	CLISkew = Skew + "; upgrade Fugaro"
)
