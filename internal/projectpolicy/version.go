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
//
// Version 5 (secrets-brokering dynamic-delivery, plan task DS3.1, decisions
// D1936 and D1942) is the node whose pools carry an owner and the users they
// are served to: a pool written now names owner and optionally allowed_users
// (by email — the roster's key, resolved to a `sub` at grant, R10), and the
// PUBLISHED node refuses a pool that names no owner, because a pool with no
// owner serves nobody. The shape changes for the first time in this line: it
// ADDS two members to a craft_pools entry and one more, legacy_open, which is
// what the migration writes for a pool whose owner no release ever recorded
// (D1942) — so a version-4 reader, whose pool carrier is strict, refuses a
// version-5 node on the members inside craft_pools and not merely on a
// top-level key it would list in UnknownKeys (P8). What a version-5 reader
// enforces that a version-4 one does not is that owner: a version-4 runner
// would serve a pool to anyone who asked for a slip naming it, which is the
// shared token the owner's own answer has to bound. The bump is also what
// makes the migration run: `fugaro update` carries a version-4 node forward
// (migrate.go), marking every ownerless pool legacy-open so the upgrade serves
// exactly whom the installation served before it.
//
// Version 6 (binary-allow-rule, plan task AR0; decisions D981-D985 and D990) is
// the node that carries the project's `binaries` block: binaries.allow (the
// path globs a run may add a binary at) and binaries.max_bytes (the cap an
// admitted one is held to, 2 MiB unless the project lowers it). What a
// version-6 runner enforces and a version-5 one does not is that admission in
// finalize, where an added binary under an admitted pattern and within the cap
// is committed rather than held back as a stray file. The block is a
// *top-level* key, which is exactly the shape P8 tolerates: a version-5 reader
// lists `binaries` in UnknownKeys and decodes past it, so it would enforce no
// rule at all while the owner believed a list was published. The bump is what
// turns that silence into the documented refusal (RunnerSkew on the job side,
// CLISkew on the operator's). The migration step that carries a version-5 node
// forward is shape-free (internal/projectpolicy/migrate.go): the node keeps its
// bytes and only the number moves, because a node with no `binaries` block
// admits nothing, which is what a project that never set one means.
//
// Version 7 (outer-loop-escalation, plan task E2; decision D2039, rules F8 and
// G3) is the node that carries the project's two model grades, `model_grades`
// with `strong` and `strongest` in it: the model each grade of the escalation
// ladder runs and the credential or pool its calls go out on, the pair a craft
// carries. What a version-7 runner resolves and a version-6 one does not is
// that pair: without the bump a version-6 reader lists `model_grades` in
// UnknownKeys and decodes past it, so it would run every rung on whatever its
// own configuration said while the owner believed the grades were published —
// the same silence the binaries block above turned into a documented refusal
// (RunnerSkew on the job side, CLISkew on the operator's). The bump is also
// what makes the migration run: `fugaro update` carries a version-6 node
// forward. That step is shape-free like version 6's — a node with no
// `model_grades` member sets no grade, which is what the ladder's rungs read as
// "leave this rung to the owner" (D2039) and what a project that never set one
// means.
//
// Version 8 (binary-allow-rule, plan task AR1; decision D990, ruled yes by the
// owner on 2026-11-15) is the version at which the project's own config states
// the `binaries` block. The member arrived at version 6 (AR0) and this line
// does not change the node's shape: what arrives is its *carrier* — the local
// config gains the key and both publish doors carry it into the node
// (buildProjectPolicy), where until this release a block could only be
// hand-written into the node. The bump is what makes a reader of the release
// behind refuse such a node (RunnerSkew on the job side, CLISkew on the
// operator's) instead of reading past a grant the owner now states in a file:
// a version-7 runner knows the key and enforces nothing with it yet, so it
// would run a project's bytes as if no list had been published at all. The
// migration step that carries a version-7 node forward is shape-free
// (internal/projectpolicy/migrate.go), like the two before it: the node keeps
// its bytes and only the number moves, because a node with no `binaries` block
// admits nothing — which is what a project that never set one means.
//
// Version 9 (model-crafts MC20 and MC21; decisions D1668 and D1673) is the
// version at which a pool stays on one path. It adds no member: what it changes
// is what the members of `craft_pools` may be. The credentials service selects
// among a pool's members by tier and the listed order only breaks a tie (F7,
// D154), while a run's own gateway holds one upstream per stage and never
// retries a call across a secret (F20, D1635) — so a pool naming members of both
// halves of a run is a pool whose order can mean nothing, and from this release
// the read of a published node refuses one (config.ValidatePublishedPools, which
// Decode runs) instead of serving it. The bump is what makes a reader of the
// release behind refuse such a node rather than read past a pool it cannot
// honour, and the migration step that carries a version-8 node forward is NOT
// shape-free (internal/projectpolicy/migrate.go): it splits every crossing pool
// into one pool per half and repoints the crafts whose access named it, so a
// version-8 node is one a version-9 reader refuses until `fugaro update`'s
// policy stage has migrated it. A node with no crossing pool changes only its
// number.
//
// Version 10 (periodic-reviews, plan task R14; decisions D2751 and D2752) is
// the node that carries the project's designated repository for committed
// reports, `reports_commit` with its `repo` and its `dir`. What the bump buys
// is stated plainly, because it is less than the blocks above bought: a
// version-9 reader lists `reports_commit` in UnknownKeys and decodes past it
// (P8), and a version-9 binary holds neither the recipe nor the flag that
// commits a report, so it can neither launch the commit nor commit one — the
// member is safe by default when it is ignored, and the bump is not what makes
// the rule true. What it is: the lockstep every policy member takes (P8), taken
// because the flag and the node are one feature and a project-level decision
// whose two doors ship together, and because the version is what `fugaro
// update` migrates — a job image of the release behind is refused a version-10
// node (policyImageGate, which names `fugaro image refresh`), which is why the
// step below is shape-free and why the release that ships this ships the
// node's migration in the same command. The migration step that carries a
// version-9 node forward is shape-free (internal/projectpolicy/migrate.go): a
// node with no `reports_commit` designates nothing, which is what a project
// that never named one means, and every version-9 node keeps its bytes.
//
// Version 11 (launcher-does-not-mint, plan task LM21; decision D3124) adds no
// member at all. What it marks is the release an identity-issuing installation
// publishes: the grant that let every launcher and operator sign a token for
// any run's identity is taken away (D3121, D3127) and the credentials service
// issues a run's identity instead, as a one-time ticket the launcher asks for
// and the run itself claims (D3122). A launcher of the release behind does not
// know the issuer's routes at all: it signs the run's identity with its own
// grant, which is exactly the grant this release removes, so its launch would
// either fail at the signer or mint an identity this release no longer accepts.
// The bump is the lockstep that turns that into the documented refusal — a CLI
// of the release behind refuses the node with SkewSentence's CLISkew, which
// names this release (`VersionSince[11]`) and the upgrade — and it is why this
// bump and the move are one release and not two: the version without the move
// tells every older CLI to upgrade for nothing, and the move without the
// version leaves that CLI to launch against an installation that no longer
// mints what it signs (the plan's Conventions). It buys nothing else: no
// reader of this release enforces a member by it, which is why the migration
// step that carries a version-10 node forward is shape-free
// (internal/projectpolicy/migrate.go) — a version-10 node is a version-11 node
// in everything but the number, so it keeps its bytes while `fugaro update`'s
// policy stage moves it in place, and a job image of the release behind is
// refused a version-11 node (policyImageGate) rather than serving runs whose
// identity it would mint itself.
const Version = 11

// VersionSince names, for each policy version, the fugaro release that
// introduced it: 1 is the model itself (0.8.0, internal/cli's policySince), 2
// is the profile gate above (0.8.5), 3 the account axis (0.9.0), 4 the
// Bitbucket token block (0.14.0), 5 the pools' owner and roster (0.23.0), 6 the
// binaries block (0.24.0), 7 the two model grades (0.24.0) and 8 the binaries
// block's carrier in the project's own config (0.26.0), 9 the one-path pool
// (0.26.0), 10 the project's designated repository for committed reports
// (0.26.0) and 11 the version an identity-issuing installation publishes
// (0.30.0). It sits beside
// Version because a bump of Version adds its row in the same change
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
	1:  "0.8.0",
	2:  "0.8.5",
	3:  "0.9.0",
	4:  "0.14.0",
	5:  "0.23.0",
	6:  "0.24.0",
	7:  "0.24.0",
	8:  "0.26.0",
	9:  "0.26.0",
	10: "0.26.0",
	11: "0.30.0",
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
//
// Generation 5 is the voice policy node (mobile plan task P5.3.1, approved by
// the owner on 2026-11-14; D123 and Q0 answered 2026-10-19): the subtree
// `config/policy/voice/{shared,repos/$slug}` of docs/mobile/design/
// voice-evaluation-switch.md §4.3 — the per-project provider allow-list the
// local/remote voice switch is evaluated against, with `providers`,
// `direction` and `force_local` in it. It is a *grant* and it bumps for
// generation 4's reason: a run token reads its own project's node there and
// did not before, so a run under the older document would be refused a read
// the switch's server-side hop depends on, and the marker is the only thing
// that tells an installation its deployed document is not this binary's. The
// grants are `config/policy/shared`'s and `config/policy/repos/$slug`'s copied
// field for field (`internal/budget/rules`'s PathVoice, `internal/projectpolicy`'s
// PathVoiceShared/PathVoiceRepo/PathVoiceRepos), and the parent
// `config/policy/voice` itself carries none, so a grant on one project's node
// never reaches another's. No signed-in user reads any of it: the phone holds
// no read of `config/policy/**`, which is why the switch's "blocked by project
// policy" state is the hop's answer and not a read.
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document before
// the policy stage, so an installation at generation 4 converges in one run
// and no second command. The deploy still refuses to write this binary's
// document under a marker *above* it, so an older CLI cannot take the voice
// grant away behind a newer one's back, and the update is safe while runs are
// in flight: the subtree is a read grant on a node an operator publishes, so a
// run that bootstrapped under the release behind keeps its records and its
// counters and every write it makes still lands
// (docs/design/update-in-flight.md).
//
// Generation 6 is the registration kind (mobile plan task P5.2.2, on
// docs/mobile/design/push-notifications.md §3.5 (b) and §3.8): a mailbox
// message may now carry the kind `registration` and, on it, the app's push
// token, the platform it was issued for, and the preference record — the five
// category switches and an optional quiet-hours window. It bumps where C1.3's
// credentials children deliberately did not, and what separates them is the
// direction the reach runs. Those were optional children of nodes a run already
// writes, and only a `cascade` run writes them, so an installation that had not
// opted in behaved identically. This is a *client's write*: the phone writes its
// registration into its own inbox, and the document behind refuses the kind —
// so every registration the app sends is refused at the database, with no
// error, until the owner deploys. That is the drift the marker exists to
// report, one direction over from generation 4's read grant: a client under the
// older document cannot make a write this one admits, where generation 4's
// reader could not make a read it admits. It is a widening for the phone and
// nothing else: no rule an existing identity satisfies was loosened, the app's
// inbox write still admits only the fields the inbox names, and neither
// `config/admin/devices/**` nor `config/admin/push/**` gains a client writer.
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document and
// records its generation in the marker, so an installation at generation 5
// converges in one run and no second command. The deploy still refuses to write
// this binary's document under a marker *above* it, so an older CLI cannot take
// the registration kind away behind a newer one's back. And the update is safe
// while runs are in flight, which the marker's own write path carries: the
// fields are new children of a subtree no run writes, a run that bootstrapped
// under the release behind keeps its records, its ledger and its counters, and
// every write it makes still lands (docs/design/update-in-flight.md).
//
// Generation 7 is the four day-counter leaves a run's stage report writes per
// model (spend-accuracy task SA5.0, D2648 as amended by D2650,
// docs/spend-accuracy/day-leaves.md): `notional`, `rep`, `tlo` and `thi` under
// `spend/<day>/repos/<slug>/byModel/<model>`, granted exactly as `micros` is —
// the amount bound, increase-only, at most MAXRES a write. It bumps where the
// credentials children deliberately did not, and it bumps for generation 6's
// reason sharpened: these are a *client's writes*, and the client is a run.
// Realtime Database applies a multi-path update atomically, so a run under the
// older document does not lose one leaf — the whole stage report is refused and
// the stage fails, which is why the writer asks the deployed generation before
// it writes them (reconciliation F10, SA5.1). The marker is the only thing that
// tells such an installation that its deployed document is not this binary's.
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document and
// records generation 7 in the marker, so an installation at generation 6
// converges in one run and no second command. The deploy still refuses to write
// this binary's document under a marker *above* it, so an older CLI cannot take
// the leaves away behind a newer one's back. And the update is safe while runs
// are in flight, which the marker's own write path carries: the leaves are new
// siblings of a node a run already writes, so a run that bootstrapped under the
// release behind keeps writing `micros` and the token leaves, and keeps its
// records, its ledger and its counters — every write it makes still lands, and
// it writes no leaf it does not know (docs/design/update-in-flight.md).
//
// Generation 8 is the paired device's read of `outcomes` (mobile
// identity-pairing task J1, finding F30, decision D2797, O3 answered by the
// owner on 2026-11-18): the `.read` grant `USER` on
// `outcomes/$day/$slug/$run`, one node, so the phone sees the run record of a
// run that is still going. A running run is a `spend/$day/runs/$slug/$run`
// entry with no `outcomes` entry beside it, and the app's read set is fenced to
// spend, caps and kill state, so without this grant the jobs list has a run's
// spend and no way to name the run.
//
// It is a *read grant* and it bumps for generation 4's reason: a device under
// the older document is refused every read of the node rather than shown a
// degraded one, and the marker is the only thing that tells an installation its
// deployed document is not this binary's. The grant is exactly that node and
// nothing wider: neither `outcomes` nor `outcomes/$day` is granted, so a device
// still reads no list of days or runs; `runs/$slug/$run` stays a run's own read
// and is still refused to a device; `agents/` stays denied. The `.write` on the
// node does not move (its `RUN && DAYOK && !data.exists()`), so a run that
// bootstrapped under the release behind keeps writing its outcome and the phone
// may not write one. What the grant does show is every run's `requestedBy` to
// any paired device, whatever the role that device's session carries: the rules
// know a device by its `mu` claim and its device node and read no role out of
// either, so one node for all of them is what the owner accepted (O3), and the
// running-jobs view J2 builds shows it to operators, admins and viewers alike.
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document and
// records generation 8 in the marker, so an installation at generation 7
// converges in one run and no second command. The deploy still refuses to write
// this binary's document under a marker *above* it, so an older CLI cannot take
// the read away behind a newer one's back. And the update is safe while runs
// are in flight, which the marker's own write path carries: the change is a
// read grant on a node a run already writes, so a run that bootstrapped under
// the release behind keeps its records, its ledger and its counters, and every
// write it makes still lands (docs/design/update-in-flight.md).
//
// Generation 9 is the credential route (workspace-service task WS22, decisions
// D2980-D2982, resolutions R3 and R4): the per-repository policy leaf
// `config/policy/route/<slug>` — where each kind of that repository's
// credentials is served from, and the address of the workspace's service — which
// a member's own `fugaro update` publishes and a member's run reads once at its
// start. It bumps for generation 5's reason, and like it the change is a *read
// grant*: the leaf is granted to the token of the run whose `fs` claim is that
// repository (`TOKEN && auth.token.fs == $slug`, exactly the grant
// `config/policy/repos/$slug` and `config/policy/voice/repos/$slug` already
// carry) and to no other identity at all. A run under the older document is
// refused that read outright rather than shown a degraded one, and such a run
// does not halt for it: the read is made by the run side of the feature (WS11,
// the run routing each kind to the holder its record names) and not by these
// rules, and no runner code reads the leaf yet, so a run under these rules reads
// no route and is served by the project's own credential, the behaviour a run
// of the release behind has today. What the marker reports is the read the
// deployed document cannot serve; the deploy is what puts the node there.
// Nothing writes the leaf but the member's own `fugaro update`, over IAM:
// `.write` is false on it, as on the policy nodes beside it, so a run token, a
// device, a launcher, a pairing session and every other client the rules bind
// are refused a write there. The parent `config/policy/route` carries no grant
// of its own, for `config/policy`'s reason: a read granted on it would cascade
// to every repository's record.
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document and
// records generation 9 in the marker, so an installation at generation 8
// converges in one run and no second command. The deploy still refuses to write
// this binary's document under a marker *above* it, so an older CLI cannot take
// the read away behind a newer one's back. And the update is safe while runs are
// in flight, which the marker's own write path carries: the change is a read
// grant on a leaf no run writes, so a run that bootstrapped under the release
// behind keeps its records, its ledger and its counters, and every write it
// makes still lands (docs/design/update-in-flight.md).
// Generation 10 is the phone's stop request and the launch envelope (mobile
// plan task P5.1g, the owner's answers Q9, Q10(c) and Q11, the design's §4.5
// E1, §4.7 and §4.8). It carries two changes, and each is one of the reasons a
// generation exists.
//
// The first is a *write* the phone holds and did not before: `stop`, the sixth
// member of the mailbox's kind vocabulary (`shared/mailbox-schema`'s KINDS),
// which is the phone asking a human to stop what it is looking at. It bumps for
// generation 6's reason — the registration kind's — because a phone under the
// older document has every stop request refused at the database with no error,
// and the record is the whole of what the owner's Q10 answer (c) built: a
// request to a human, never a phone-writable kill switch. Nothing
// else about the mailbox moves: the kind carries the five shared fields and no
// others, so no field node, no tag rule and no required-field clause changes.
//
// The second is a *read grant*, and it bumps for generation 4's, 5's and 8's
// reason: the launch envelope (`internal/budget/rules`.PathLaunchEnvelope,
// `config/policy/launch`) is one node under the policy subtree the operator
// already publishes — a sibling of `shared`, `repos/$slug` and `voice`, one
// node for the installation and one entry per device under it, which is Q11's
// ruling against a node *per device* — and the deployed document now grants a
// paired device the installation's named safe recipe (`launch/recipe`) and
// **its own entry** under `launch/devices/$uid` and nothing else: `USER` holds
// the device node's existence as a conjunct already, so a revoked pairing
// reads no envelope entry, and the rules compare `$uid` with `auth.uid`, so one
// device reads no other device's allow-list. A device under the older document
// is refused both reads outright rather than shown a degraded envelope — which
// matters more here than for a leaf a run reads, because the app refuses an
// utterance outside its allow-list *before* it signs anything, and an unreadable
// envelope is an app that refuses every launch rather than one that launches
// unbounded.
//
// It is a sibling of `shared` and not a member of it because
// `config/policy/shared` *is* the published policy document, and that document
// decodes strictly: a `launch` member inside it would be a policy *version*
// bump and a migration for a value the policy's own reader never reads.
//
// What the grant does NOT do is enforce the envelope. A phone's launch is a
// `message`, whose free text the rules cannot resolve a repository out of, so
// the decision is the consumer's (`internal/orchestrator`'s envelope: the
// per-device allow-list narrowed from `allow_repos`, the per-device rate and
// cost ceiling, and the named safe recipe an unconfirmed launch may run) and
// the app's. That is where Q9's answer puts it, and it is the honest shape of
// the fence: the rules hand the phone its own values and refuse it everything
// else; they do not promise that a value was obeyed.
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document and
// records generation 10 in the marker, so an installation at generation 9
// converges in one run and no second command. The deploy still refuses to write
// this binary's document under a marker *above* it, so an older CLI cannot take
// the grant away behind a newer one's back. And the update is safe while runs
// are in flight, which the marker's own write path carries: one half is a read
// grant on a leaf no run writes and the other admits one more kind of message
// into a mailbox, so a run that bootstrapped under the release behind keeps its
// records, its ledger and its counters, and every write it makes still lands
// (docs/design/update-in-flight.md).
//
// Generation 11 is the mailbox's signature, the typed launch and the shaped
// task a launch may carry (mobile identity-pairing plan tasks J5r, O6 and O10 —
// the owner's answer of 2026-11-21 that a device signs with Ed25519 rather than
// an HMAC tag, amending D171 — and mobile plan task P5.4.2, the owner's ruling
// of 2026-11-27). It carries three shapes, and each is a *write* a client
// holds.
//
// The first is the tag: a message and the heartbeat carry exactly one of `mac`
// and `sig`, where `sig` is the unpadded base64url encoding of a 64-byte
// Ed25519 signature (86 characters, checked for length and characters and never
// for value — the rules check shape, and which scheme a device must use is its
// own device node's, task J5b). It bumps for generation 6's reason: a device
// that signs under the document behind has every message and every heartbeat
// refused at the database with no error, because the older document requires a
// `mac` member these records do not carry. It is a widening in exactly one
// place — the registration's three fields accept either scheme
// (`PARENTMACOK || PARENTSIGOK`) — because which one a device writes under is
// not this document's to read.
//
// The second is the typed launch: an inbox action whose `verb` is `launch`
// carries a `mode` (a rigor mode — a short identity string here, the
// vocabulary being internal/rigor's) and needs no `confirmation`, where every
// other verb still does, and its target is a repository alone. It bumps for the
// same reason one verb along: `launch` is a word the older document's verb
// enumeration does not carry, so a phone's start is refused at the database
// with no error. Which modes need a read-back before they run is the launch
// gate's (J5a, D2786) and not this document's: the rules see a bounded string
// and the action's shape, and a plain start needs no confirmation at all.
//
// The third is the shaped task a `launch` may carry (mobile plan task P5.4.2):
// `brief`, the text a "go" starts a run from, a bounded string beside the action
// record and never inside it, accepted on `launch` alone. It is OPTIONAL,
// because the app's existing typed start carries its task in the message's own
// `text` and a launch with no brief must keep validating; a brief on any other
// verb, or on a kind that cannot carry one, is refused as the shared schema
// refuses it (shared/mailbox-schema, which the rules are held to field for field
// by TestMailboxRulesAgreeWithTheSharedSchema). It takes no generation of its
// own, and **mobile plan task P5.4.3 — the task that ships the field's denials —
// states the number it took: 11, this generation**, because the document does
// not change with it. Generation 11 is in no release, so no shipped binary
// deploys a generation-11 document without the field, and the front agent's turn
// needs no second generation: the document the owner deploys is the one this
// template already renders, and a generation that records no document difference
// is what this file's own note on the fourth generation refuses. What P5.4.3
// adds is the directed case set that proves the fence holds —
// `emulator_front_agent_test.go`, a brief outside its bound, a brief with no
// agent, a brief beside a verb that is not the launch, a brief on a kind that
// cannot carry one and an untagged go — the same shape P5.2.3 shipped for
// generation 6. An installation that already carries a generation-11 document
// converges the same way any document difference does — `fugaro update`'s rules
// stage redeploys when the marker is this generation and the document is not
// this binary's (rulesSkewDocument).
//
// What the bump does to an existing installation is what every bump does, and
// it is met the same way: `fugaro doctor` reports policy-rules-skew until the
// owner runs `fugaro update`, whose rules stage deploys this document and
// records generation 11 in the marker, so an installation at generation 10
// converges in one run and no second command. The deploy still refuses to write
// this binary's document under a marker *above* it, so an older CLI cannot take
// the signature away behind a newer one's back. And the update is safe while
// runs are in flight, which the marker's own write path carries: the tag is a
// field beside a field a device already writes and the launch is one more verb
// in a mailbox no run writes, so a run that bootstrapped under the release
// behind keeps its records, its ledger and its counters, and every write it
// makes still lands (docs/design/update-in-flight.md).
const RulesVersion = 11

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

// The version-skew messages. Skew is the sentence every reader prints; the six
// others are its remedies, one per state of the skew and per reader:
//
//   - CLISkew and RunnerSkew: the node is newer than the reader, so nothing
//     here can read it and the release that wrote the node is newer than the
//     one calling this. For the CLI the remedy is the binary the package
//     manager installs, then the update behind it — `fugaro update` moves a
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
//   - CLISkewCarried and RunnerSkewCarried: the node is older than the reader
//     and the migration chain carries it forward (Migratable). Nothing is
//     upgraded — the reader is the newer side, so naming an upgrade would be
//     advice that changes nothing — and nothing is deleted either: `fugaro
//     update`'s policy stage rewrites the node in place at this reader's
//     version, keeping the members it carries. This is the move `fugaro doctor`
//     names for the same state (its policy-older-than-binary check asks
//     Migratable the same question), and it is the state of every installation
//     between the binary upgrade and `fugaro update`, so it is the one an
//     operator meets most.
//   - CLISkewNewer and RunnerSkewNewer: the node is older than the reader and no
//     migration step reaches it (tooOld: versions 1 and 2, which 0.8.0 and
//     0.8.5 wrote). Nothing is upgraded here either, and the remedy is a new
//     node: publishing refuses to overwrite a node written in another version
//     (internal/cli's publishedPolicyOf, which words the same refusal, and
//     docs/project-layer.md and `fugaro doctor` carry it too), so the older
//     node goes first, with `fugaro project policy publish --delete`, and the
//     publish that follows writes a shape the reader understands. `fugaro update
//     --firebase-project` is deliberately not named in either: it deploys this
//     release's rules template and marker and never writes
//     config/policy/shared, so an operator sent to re-run it would come back to
//     the same refusal — while the whole `fugaro update` is, and is named. A
//     hand-written node, or one a release before this shape wrote, is the only
//     way to get here (Decode refuses a version this Fugaro does not know, and
//     a node below Version is not the legacy path — that is the marker's job,
//     Read). The runner's own sentence adds that the run cannot do any of it
//     mid-run: the refusal is the job's, and the operator who updates or
//     publishes is the one who acts.
//
// A message that printed one direction's remedy for both sent the operator of a
// node that was merely behind to upgrade a Fugaro that was already ahead of it
// (changing nothing), or left the reader of a node that was behind with no move
// at all. Each takes the found version and the understood one, in that order.
const (
	Skew              = "the project policy is version %d; this Fugaro understands version %d"
	CLISkew           = Skew + "; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then fugaro update"
	CLISkewCarried    = Skew + "; the node is older than this Fugaro: run 'fugaro update', whose policy stage rewrites the node in place at this Fugaro's version, keeping the members it carries"
	CLISkewNewer      = Skew + "; the node is older than this Fugaro: publishing refuses to overwrite a node written in another version, so remove it with 'fugaro project policy publish --delete' and publish it again from this release"
	RunnerSkew        = Skew + "; refresh the runner image (fugaro update)"
	RunnerSkewCarried = Skew + "; the node is older than this runner image: the project's owner runs 'fugaro update', whose policy stage rewrites the node in place at this Fugaro's version, keeping the members it carries — and the run cannot do it mid-run"
	RunnerSkewNewer   = Skew + "; the node is older than this runner image: publishing refuses to overwrite a node written in another version, so the project's owner removes it with 'fugaro project policy publish --delete' and publishes one this image understands — and the run cannot do either mid-run"
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
//
// A node *behind* its reader has two states, and the chain decides between them
// (Migratable) rather than this function: a version a step reaches is the
// update's to rewrite in place, and one below the chain's first step is the
// publisher's to delete and write again. `fugaro doctor`'s
// policy-older-than-binary check asks the same question of the same node, so
// the two cannot advise different moves for one state.
func SkewSentence(ve *VersionError, reader Reader) string {
	carried := Migratable(ve.Found)
	switch {
	case ve.Found > ve.Understood && reader == ReaderRunner:
		return fmt.Sprintf(RunnerSkew, ve.Found, ve.Understood) + arrivedClause(ve.Found)
	case ve.Found > ve.Understood:
		return fmt.Sprintf(CLISkew, ve.Found, ve.Understood) + arrivedClause(ve.Found)
	case carried && reader == ReaderRunner:
		return fmt.Sprintf(RunnerSkewCarried, ve.Found, ve.Understood)
	case carried:
		return fmt.Sprintf(CLISkewCarried, ve.Found, ve.Understood)
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
