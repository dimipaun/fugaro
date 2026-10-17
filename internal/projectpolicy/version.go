package projectpolicy

import (
	"fmt"
	"strings"
)

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
//		msg = projectpolicy.SkewSentence(ve, projectpolicy.ReaderRunner) // the runner, task 1.3
//		msg = projectpolicy.SkewSentence(ve, projectpolicy.ReaderCLI)    // the CLI
//	}
//
// Found and Understood are what tells the two directions apart (Found >
// Understood is a node ahead of its reader, so the reader is the older side),
// and SkewSentence is the one place that picks between the sentences.
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

// Reader is which side is printing the refusal: the CLI an operator runs, or
// the runner image inside a job. It is part of the sentence because the thing
// that is behind is not always the same one, and the fix differs with it — and
// because the two readers are separate installations of Fugaro, so the runner
// cannot assume the operator's CLI is the release that published the node.
type Reader int

const (
	// ReaderCLI is the fugaro command, which the operator upgrades in place.
	ReaderCLI Reader = iota
	// ReaderRunner is the image a job runs in, which the operator refreshes
	// with the upgrade; neither reader can be changed by the run itself.
	ReaderRunner
)

// The version-skew messages. Skew is the sentence every reader prints; the four
// others are its remedies, one per direction of the skew and per reader:
//
//   - CLISkew and RunnerSkew: the node is newer than the reader, so nothing
//     here can read it and the release that wrote the node is newer than the
//     one calling this. `fugaro upgrade` is the whole remedy (for the image,
//     "refresh the runner image" — it is not the CLI's binary), and neither of
//     the other moves is one: `fugaro init --firebase` deploys this release's
//     rules and marker, and the deploy's own plan refuses to put them over a
//     newer Fugaro's (internal/infra's DB.Plan: a marker above this binary's
//     RulesVersion stops the plan there — "the rules this database runs under
//     came from a newer Fugaro, and 'fugaro init --firebase' would deploy this
//     binary's older rules over them. Upgrade Fugaro first" — which doctor's
//     policy-rules-skew reports with the upgrade as its fix too, and which
//     requirePolicyRules does *not* refuse: it reads a marker at or above
//     RulesVersion as one this Fugaro reads a policy under), and there is
//     nothing to delete either — `fugaro project policy
//     publish` refuses a node written by a newer Fugaro and `--delete` goes
//     through the same refusal (internal/cli's refuseNewerPolicy, which the
//     publish path calls before the delete branch), which doctor reports as
//     policy-newer-than-binary with the upgrade as its fix.
//   - CLISkewNewer and RunnerSkewNewer: the node is older than the reader.
//     Nothing is upgraded — the reader is the newer side, so `fugaro upgrade`
//     would be advice that changes nothing — and the remedy is a new node:
//     publishing refuses to overwrite a node written in another version
//     (internal/cli's publishedPolicyOf, which words the same refusal, and
//     docs/project-layer.md and `fugaro doctor` carry it too), so the older
//     node goes first, with `fugaro project policy publish --delete`, and the
//     publish that follows writes a shape the reader understands. `fugaro init
//     --firebase` is deliberately not named in either: it deploys this
//     release's rules template and marker and never writes
//     config/policy/shared, so an operator sent to re-run it would come back to
//     the same refusal. A hand-written node, or one a release before this shape
//     wrote, is the only way to get here (Decode refuses a version this Fugaro
//     does not know, and a node below Version is not the legacy path — that is
//     the marker's job, Read). The runner's own sentence adds that the run
//     cannot do any of it mid-run: the refusal is the job's, and the operator
//     who publishes is the one who acts.
//
// A message that printed one direction's remedy for both sent the operator of a
// node that was merely behind to upgrade a Fugaro that was already ahead of it
// (changing nothing), or left the reader of a node that was behind with no move
// at all. Each takes the found version and the understood one, in that order.
const (
	Skew            = "the project policy is version %d; this Fugaro understands version %d"
	CLISkew         = Skew + "; upgrade Fugaro (fugaro upgrade)"
	CLISkewNewer    = Skew + "; the node is older than this Fugaro: publishing refuses to overwrite a node written in another version, so remove it with 'fugaro project policy publish --delete' and publish it again from this release"
	RunnerSkew      = Skew + "; refresh the runner image (fugaro upgrade)"
	RunnerSkewNewer = Skew + "; the node is older than this runner image: publishing refuses to overwrite a node written in another version, so the project's owner removes it with 'fugaro project policy publish --delete' and publishes one this image understands — and the run cannot do either mid-run"
)

// SkewSentence is the one place that words a version skew: the shared sentence
// with both versions in it, and the remedy that fits this direction and this
// reader. Every caller goes through it — the runner's unreadablePolicy
// (internal/runner), the CLI's policyReadErr (internal/cli's launch_budget.go)
// and the launch gate's policySkewRefusal (internal/cli's run_allowlist.go) —
// so neither side can drift into advising a move that cannot help the state it
// is looking at (the review finding on this change: the runner printed the
// image upgrade for both directions, and a node merely behind its image is not
// fixed by an upgrade).
//
// Found > Understood means the node is ahead of the reader: the reader is the
// older side. Found < Understood means the node is behind. Equal cannot come
// from Decode (a version it understands is not a skew), and is read as the
// node's own problem — the direction in which this reader is the newer side.
func SkewSentence(ve *VersionError, reader Reader) string {
	switch {
	case ve.Found > ve.Understood && reader == ReaderRunner:
		return fmt.Sprintf(RunnerSkew, ve.Found, ve.Understood)
	case ve.Found > ve.Understood:
		return fmt.Sprintf(CLISkew, ve.Found, ve.Understood)
	case reader == ReaderRunner:
		return fmt.Sprintf(RunnerSkewNewer, ve.Found, ve.Understood)
	default:
		return fmt.Sprintf(CLISkewNewer, ve.Found, ve.Understood)
	}
}

// SkewRemedy is SkewSentence's remedy alone — everything after the shared
// "Skew; " prefix — for the caller that prints Skew itself, with its own text
// inside it (the launch gate's policySkewRefusal, which quotes the release the
// policy model ships in). Deriving it here rather than wording a second remedy
// keeps SkewSentence's constants the one source of both sentences.
func SkewRemedy(ve *VersionError, reader Reader) string {
	skew := fmt.Sprintf(Skew, ve.Found, ve.Understood)
	return strings.TrimPrefix(SkewSentence(ve, reader), skew+"; ")
}
