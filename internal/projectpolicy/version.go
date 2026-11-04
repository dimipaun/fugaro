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
//
// Version 3 (project-named-oauth-tokens, plan task 1) is the node that names
// Claude accounts: profiles.<p>.claude_accounts (the accounts a profile
// serves, one job per (profile, provider, account)) and the accounts block it
// is gated by (accounts.<name>.allow_repos, the repositories that account's
// token may be used by). What a version-3 runner enforces and a version-2 one
// does not is that second list, and the pair of rules that hold a profile to
// it: the profile writes exact allow_repos, every account it lists covers
// every repository on that list, and no Anthropic API key is mounted beside
// them. Such a node carries two keys a version-2 reader does not know, and
// they are worth telling apart because only one of them is tolerated: the
// accounts block is a top-level key, which it would merely list in
// UnknownKeys and decode past (P8), and claude_accounts is a member INSIDE a
// profile entry, which its Profile carrier does not have and which its own
// profile decode refuses — entries are decoded with DisallowUnknownFields, the
// rule the decoder states for a new profile field ("a newer profile field
// bumps Version, which is what refuses a node this binary cannot read"), and
// the rule the design's schema section cites for this bump. So a node whose
// profile names accounts is unreadable to a version-2 reader whether or not
// its version field said 3: the field is what makes it unreadable, and the
// bump is what makes that refusal the documented one. The version check
// answers first, with a VersionError and therefore with the remedy that fits
// the direction (RunnerSkew on the job side, CLISkew on the operator's),
// instead of an unknown-field decode error that names a field and no move. It
// also states in the node which enforcement a reader must have, which is what
// a version is for. The rules themselves ship in the
// same release as this constant, and they have two doors: what admits an OAuth
// launch onto such a job, or refuses it before the claim and before the token,
// is internal/cli's projectJobAccountFor — with checkProjectJobAccount and
// checkProjectJobAccountCovers behind it, the two its answer is computed with —
// and the runner re-decides the same pair from the policy it read itself, in
// internal/runner's own checkProjectJobAccount, before any stage, lease or
// budget spend. The shape is version 2's; only what a runner must enforce
// changed.
//
// Version 4 (bitbucket-workspace-token, plan task T2.1 as amended by
// docs/secrets-brokering D493) is the node that carries the project's Bitbucket
// token block: bitbucket_token.allow_repos (the repositories the project's
// Bitbucket credential may reach, derived from
// project_secrets.bitbucket-token.allow_data_to), bitbucket_token.proxy (the
// operator's per-installation switch for the credentials service, D493), the
// owner-allow-listed bitbucket_api_roots (D474), and the rules that hold a
// profile mounting bitbucket-token to that block — its own exact allow_repos,
// a subset of the block's, and no such mount on a GitHub profile. The block is
// policy-wide and derived whether or not a profile mounts the token, because a
// proxied run is launched in a job that does not hold the credential and reads
// the block as its admission source. What a version-4 runner enforces and a
// version-3 one does not is that block and those profile rules, and a
// version-3 reader would decode past bitbucket_token as a top-level key it
// merely lists in UnknownKeys (P8) — so the bump is the lockstep that makes it
// refuse the node (RunnerSkew, or CLISkew on the operator's side) instead of
// running a Bitbucket profile whose own allow_repos is the only gate. Every
// installation republishes its policy for version 4; a version-3 node is
// refused in the other direction too (D477), which is why the shape of this
// node is version 3's and only what a reader must enforce changed.
const Version = 4

// VersionSince names, for each policy version, the fugaro release that
// introduced it: 1 is the model itself (0.8.0, internal/cli's policySince), 2
// is the profile gate above (0.8.5), 3 the account axis (0.9.0), 4 the
// Bitbucket token block (0.14.0). It sits beside Version because a bump of
// Version adds its row in the same change
// (TestVersionSinceCoversEveryVersion fails until it does), and it is
// read by every message about a node *ahead* of its reader (SkewSentence, so
// the launch gate, the CLI's other reads, `fugaro project policy`, doctor and
// the runner all answer from it): the release that reads such a node is the
// one that introduced its version, never the release the model ships in — an
// 0.8.1 binary meeting a version-2 node already runs 0.8.0.
//
// A row may name a version above Version: the table records history, and a
// binary of an older line needs the row of a version a newer release
// introduced. A version no row names gets the remedy alone (VersionRelease
// answers false), never a borrowed release.
var VersionSince = map[int]string{
	1: "0.8.0",
	2: "0.8.5",
	3: "0.9.0",
	4: "0.14.0",
}

// VersionRelease is the release that introduced policy version v, and false
// when VersionSince has no row for it.
func VersionRelease(v int) (string, bool) {
	rel, ok := VersionSince[v]
	return rel, ok
}

// arrivedClause is the clause a too-new refusal appends when the table knows
// the version on the node, and "" when it does not.
func arrivedClause(v int) string {
	if rel, ok := VersionRelease(v); ok {
		return fmt.Sprintf("; policy version %d arrived in fugaro %s", v, rel)
	}
	return ""
}

// PolicyRulesVersion is the generation of the Realtime Database security rules
// that introduced the config/policy nodes of §5 and their per-child read
// grants: the generation an installation must have deployed before this Fugaro
// reads a published policy at all (Read), before `fugaro project policy
// publish` writes one (internal/cli's requirePolicyRules) and before a
// published policy governs anything a run does.
//
// It is 1 for the life of the policy model, and it is not RulesVersion.
// RulesVersion records which *document* the database runs under and goes up
// with every rules change; this one names the single change that made the
// policy nodes exist. A later generation adds nodes beside the config/policy
// ones — the mailbox is generation 2 — rather than making them appear, so an
// installation whose marker is older than RulesVersion still has the policy
// nodes and still has the policy it published there.
//
// Gating on RulesVersion instead would take every published policy out of every
// run's reach on the day a later generation shipped, because Read answers "not
// found" below its gate and all three of that answer's readers take it as "this
// installation has no policy": the runner keeps the ceiling its job environment
// was deployed with (internal/runner/projectpolicy.go), the launcher skips the
// allow-list and release-skew gates (internal/cli's launchPolicyGates), and
// `fugaro doctor` reads the node as ungoverned. That is what the bump to 2 did
// to every installation still on generation 1 — a generation that *does* carry
// the policy nodes and under which a policy could be published, so the premise
// "no policy can have been published here" was false of it (the review finding
// on P2.1).
const PolicyRulesVersion = 1

// RulesVersion is the generation of the Realtime Database security rules that
// this Fugaro deploys and reads: the config/policy nodes of §5 and their
// per-child read grants, the mailbox of design m9 §6.4, and everything the
// template carries. It is recorded on the database as
// config/limits/policyRules (PathRulesMarker), written by the owner-run rules
// deploy and only after it succeeds (internal/infra's DB.Plan orders it after
// the PutRules action), and read by the runner before it reads a policy (task
// 1.3), which must not decode a policy its deployed rules do not protect.
//
// Any generation of the rules document bumps this, not only one that changes
// the policy nodes: the marker records *which document* the database runs
// under, and a run that met a document it does not know is not one that may
// act as though it knew it. Generation 2 is the mailbox (mobile plan task
// P2.1, design m9 §6.4): the grant that lets the app read the caps, the day
// counters and the kill switches directly, and the subtree the phone's user
// and the mailbox's consumer write.
//
// An installation still on generation 1 has no mailbox — and it is not a
// policy-less installation: its rules carry the config/policy nodes
// (PolicyRulesVersion), so the policy it published is read and enforced and
// publishing another is still allowed. What is missing there is this binary's
// own document, which is why doctor reports the difference (policy-rules-skew)
// and the owner's deploy records generation 2 — not a reason for anything to
// read below the policy generation and answer "no policy here".
//
// Generation 3 is P6.4 (mobile plan task P6.4, decisions D161, D165, D166,
// D169, D171): the mailbox's message fields as P6.3's schema defines them (a
// `mac` required on every message of every kind and on the heartbeat, with
// `agent`, `kind`, `options`, `reply_to` and the action record beside it), the
// device allowlist `config/admin/devices/<uid>` that USER now requires (D166),
// and the two pairing nodes of D165. The generation moves for the device node
// even where nothing else did: a phone on generation 2 cannot read its own
// mailbox under a generation-3 document until its account is paired, so an
// installation that deploys these rules and forgets `fugaro pair` finds every
// app read refused — and `fugaro doctor` says nothing about it *afterwards*.
// `policy-rules-skew` compares the deployed *document* with this binary's, so
// it warns only **before** the deploy, while the marker still records
// generation 2; the deploy that records generation 3 is what silences it, and
// from then on the unpaired account is the app's own empty reads to diagnose
// rather than a skew doctor reports. The warning is where the operator is told
// to pair first (internal/cli's rulesMarkerOlderText says it in as many words),
// and it is the deploy that takes it away.
//
// What the bump does to an existing installation, plainly. Runs and the
// project policy are untouched: Read and the publish gate stay on
// PolicyRulesVersion (1). Under a generation-2 marker this binary's doctor
// warns policy-rules-skew until the owner deploys. Once generation 3 is
// deployed: every phone is refused every read and write until its account has
// a device node, and until `fugaro pair` (P6.6) ships the only way to write one
// is by hand over IAM; every mailbox writer that does not send a `mac` (no code in this
// repository writes the mailbox yet, so this reaches a hand-written consumer
// or script only) is refused; and a 0.8.5 CLI
// (generation 2) refuses `fugaro update --firebase-project` against the generation-3
// marker rather than deploying its older rules over it, and its doctor warns.
//
// Generation 3 also carries the credentials children, and they deliberately
// did not make a generation 4 (credentials-service plan, task C1.3): the
// `/creds` node (denied to every run token — the service is its only writer,
// D136/O1), the agent entry's `credential` (a name, never a value) and
// `waiting_until`, and the by-tier split of real spend under the counters that
// already exist. They are optional, validated children of nodes a run already
// writes and only a `cascade` run writes them, so an installation that has not
// opted in behaves identically — where a bump would make `projectpolicy.Read`
// answer "not found" and drop every published policy (caps, `allow_repos`,
// routing, prices) on any installation whose owner has not redeployed the
// rules yet, and make the publish gate refuse there. The price of not bumping
// is that this marker cannot answer "does the deployed document carry the
// credentials children", so the same deploy records that generation separately:
// `config/limits/credsRules` (`internal/budget/rules`.CredsRulesVersion, 1),
// which a `cascade` launch, the service's init stage and doctor require, and
// which the deploy refuses to put this binary's document under when it names a
// newer one. The gap this leaves — an older Fugaro redeploying its own document
// while its generation check does not fire and it does not know the new marker
// — is a named halt at the run's first refused write (HaltCredsRulesDenied,
// C1.17), never a silent drop; the plan's non-adopters section states it in
// full.
//
// One thing that section and this one both have to say plainly, because it is
// the direction the redeploy comes from: a release in *this* line that predates
// the credentials children is generation 3 as well, so its `fugaro doctor` sees
// the deployed document differ from the one it generates, warns
// `policy-rules-skew` and prescribes "re-run `fugaro update --firebase-project`" — the
// redeploy that takes the children away. It cannot be told otherwise from
// where it stands: the marker reads 3, which is its own generation, and it
// knows nothing of `config/limits/credsRules`. The "upgrade instead if the
// rules came from a newer Fugaro" parenthetical in that fix text does not
// reach it either, for the same reason. Only a binary that reads the second
// marker beside the first can tell the two apart, which is what the doctor
// work of C1.21 is for; this binary's doctor does not read it yet, so until
// C1.21 the trap is recorded here and in `internal/budget/rules`'s KNOWN GAP
// rather than reported.
//
// It is not Version: the policy schema and the rules that guard it move
// independently.
//
// Generation 4 is the coordinator (coordinator plan task P3, decisions D194
// and D195, both answered 2026-10-21): the new top-level `coordinator/`
// subtree, granted to the signed-in reader and to nobody else. It bumps where
// the credentials children deliberately did not, and the difference is what
// each change does to a client. The credentials children are optional fields
// of nodes a run already writes and only a `cascade` run writes them, so an
// installation that has not opted in behaves identically — which is why that
// change took a marker of its own instead (rules.CredsRulesVersion). The
// coordinator's node is a *grant*: it is what lets a signed-in reader see the
// project's state at all, and a reader under an older document is refused
// every read of `coordinator/` rather than shown a degraded one. A generation
// that only adds a deny, or a node no client may reach, would not need the
// bump either; one that opens a family to a client does, because the marker
// is the only thing that tells an installation its deployed document is not
// the one this binary reads under.
//
// What the bump does to an existing installation, plainly: `fugaro doctor`
// reports policy-rules-skew and prescribes `fugaro update --firebase-project`
// until the owner deploys, and `fugaro update --firebase-project` refuses to
// deploy this binary's document over a marker *above* it, so an older CLI
// cannot take the coordinator's grant away behind a newer one's back. Runs
// and the project policy are untouched (they are gated on PolicyRulesVersion,
// which stays 1). Nothing reads the coordinator's subtree yet: the write path
// is P4's and the sweep is P5's, so what a person sees before those land is
// the grant alone, with no document under it.
const RulesVersion = 4

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
//     one calling this. For the CLI the remedy is the binary the package
//     manager installs, then the update behind it — `fugaro upgrade` moves a
//     checkout onto the installed release and never replaces the binary it
//     runs; for the image it is "refresh the runner image", which is not the
//     CLI's binary. And neither of the other moves is one: `fugaro update
//     --firebase-project` deploys this release's rules and marker, and the
//     deploy's own plan refuses to put them over a
//     newer Fugaro's (internal/infra's DB.Plan: a marker above this binary's
//     RulesVersion stops the plan there — "the rules this database runs under
//     came from a newer Fugaro, and 'fugaro update --firebase-project' would deploy this
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
//     publish that follows writes a shape the reader understands. `fugaro update
//     --firebase-project` is deliberately not named in either: it deploys this
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
	CLISkew         = Skew + "; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then fugaro update"
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
//
// A node ahead of its reader also names the release that introduced its
// version when VersionSince has a row for it ("; policy version 2 arrived in
// fugaro 0.8.5"), and nothing more when it does not.
func SkewSentence(ve *VersionError, reader Reader) string {
	switch {
	case ve.Found > ve.Understood && reader == ReaderRunner:
		return fmt.Sprintf(RunnerSkew, ve.Found, ve.Understood) + arrivedClause(ve.Found)
	case ve.Found > ve.Understood:
		return fmt.Sprintf(CLISkew, ve.Found, ve.Understood) + arrivedClause(ve.Found)
	case reader == ReaderRunner:
		return fmt.Sprintf(RunnerSkewNewer, ve.Found, ve.Understood)
	default:
		return fmt.Sprintf(CLISkewNewer, ve.Found, ve.Understood)
	}
}

// SkewRemedy is SkewSentence's remedy alone — everything after the shared
// "Skew; " prefix — for the caller that prints Skew itself, with its own text
// inside it (the launch gate's policySkewRefusal, which quotes the release the
// policy model ships in for a node behind this binary). Deriving it here rather than wording a second remedy
// keeps SkewSentence's constants the one source of both sentences.
func SkewRemedy(ve *VersionError, reader Reader) string {
	skew := fmt.Sprintf(Skew, ve.Found, ve.Understood)
	return strings.TrimPrefix(SkewSentence(ve, reader), skew+"; ")
}
