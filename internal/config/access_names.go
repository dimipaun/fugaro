package config

// The access contract's own names (C1): what a retired auth value means once it
// is an access, the entry a run learns its access from, the member a run's
// registry entry carries, the node a repository's crafts are published in, and
// the fact that its credentials are served.
//
// # Why these live here
//
// The whole of the contract is docs/credentials-belong-to-models/contract-access.md,
// and this file is the half of it that is a NAME rather than a shape: a
// spelling two sides must not disagree about. They are here because config is
// the one package every side of the plan may import — internal/gateway imports
// this package and may not import internal/credsvc, internal/infra plans a job
// with the fact below and may not import internal/craft's readers, and
// internal/localcfg and internal/creds import this package and each other not
// at all. A name spelled in two packages is a repository served what its record
// does not name, or refused for a call it never made, which is why the
// contract's names are one list in one leaf.
//
// What is NOT here is what a name means about a particular document: the rule
// that answers the access is internal/craft/legacy.go, the record is
// internal/creds/repoaccess.go, the wire is internal/credsvc/access_wire.go and
// the gateway's copy of the header names is internal/gateway/accesswire.go.

const (
	// CredentialAnthropicKey is the credential the project's own Anthropic API
	// key is declared under, and the access the retired `agent.auth: api-key`
	// means (C1 (c), R7): the one credential whose value the platform holds and
	// every run of the project may be served, at tier B of the bundle.
	//
	// It is spelled as config.AnthropicSecret spells it, which is the bundle's
	// own name for the key: CredentialKey answers it under that name, the entry
	// a Lookup finds is indexed by it, and the service identifies the key among
	// a call's candidates by it (internal/credsvc), so the retired arm, the
	// migration, the access record and the service all name ONE credential.
	//
	// It is deliberately NOT the type's spelling (config.CredentialTypeAnthropic,
	// "anthropic"). A craft's access names a name and not a type, and "anthropic"
	// is a name a project may define a credential of its own under
	// (credentialNameProblem admits it): an access answered as "anthropic" would
	// name no entry the bundle holds — the readiness check and the per-call
	// lookup would both miss it — and would mean two credentials at once, which
	// is what the bundle's own refusal to index one name twice exists to stop
	// (R5).
	//
	// It is deliberately not AnthropicKeyAccount: that is the account axis's
	// sentinel, in a different vocabulary (an account's name), and
	// credentialNameProblem refuses exactly that value as a credential's name —
	// a credential is declared once for the models it reaches and is not the
	// project's account.
	CredentialAnthropicKey = AnthropicSecret

	// PoolCascade is the name the migration gives the pool a cascade becomes
	// (D3064 as amended, R7), and the access the retired `agent.auth: cascade`
	// means (C1 (c)): the same members in the same order, marked as the
	// cascade's, published as an ordinary legacy_open pool (R15 (1)).
	//
	// It is a name and not an identification: whether a pool is the cascade's is
	// the marker the record carries (C1 (d)), never the name it happens to
	// have. The name is here so that the migration that writes the pool, the
	// rule that answers the retired auth value and the line that prints what
	// each craft resolves to cannot spell three names.
	PoolCascade = "cascade"

	// EnvAccess is the entry the launcher puts a run's resolved access under in
	// the execution's environment (C1 (b), R15 (3)): one JSON document,
	// internal/craft's Resolved, keyed by craft and by grade. A run with no such
	// entry applies the migration's rule to its own file (internal/craft's
	// LegacyAccess); one whose file writes no retired key either is refused with
	// the fix and is never sent to Vertex (F49).
	//
	// It is a plain FUGARO_-prefixed variable like the fact below, and not a
	// secret: every value in it is a name or a type.
	EnvAccess = "FUGARO_ACCESS"

	// RegistryAccessMember is the member a new run's registry entry carries in
	// place of `auth` (C1 (f), R6): the credentials and pools the run's crafts
	// resolve to, sorted and without repeats, so `watch` and a console read what
	// a run goes out on where they read a repository's auth mode before.
	//
	// `auth` is left out of a new entry and never reused (R6, D3070): a field
	// never changes meaning, and `credential` keeps the meaning it has, the one
	// credential being spent on right now. The database rules gain this member,
	// deployed by `update` as the rules are, and a runner that meets rules
	// without it drops the member and writes the rest, as it does for `recipe`.
	RegistryAccessMember = "access"

	// RepoCraftsKey is the per-repository craft block's key in the
	// installation's config (localcfg.Repo) and the member the node below
	// carries: the same `crafts` block the project policy node publishes at the
	// project level, holding one craft's model and access each.
	RepoCraftsKey = "crafts"

	// RepoCraftsNodePrefix is the family a repository's crafts are published in
	// (C1 (g), F47, R15 (2)): one child per repository slug, BESIDE the
	// per-repository policy node (projectpolicy's config/policy/repos/) and not
	// inside it.
	//
	// It is a node of its own because the per-repository node is read strictly
	// by every CLI of the release behind and by the service: a member added to
	// it would make a teammate's older CLI refuse every policy publish
	// (tightenAdmissions decodes every child) and the node has no version to
	// refuse cleanly on. A node no older reader reads can gain its members
	// without gating anything.
	//
	// The full path is built by the package that owns the escaping rule for a
	// database key — projectpolicy's seg, which the publisher and every reader
	// of the tree go through (CM2's PathRepoCrafts) — so this package states the
	// family and adds no third escaper.
	RepoCraftsNodePrefix = "config/policy/repocrafts/"

	// RepoCraftsNodeVersion is the version a repository's crafts node carries,
	// beside its crafts member. It is a member rather than the node's shape
	// alone for the reason the access record's version is: the writer and a
	// reader are two binaries, and a reader that meets a version it does not
	// know refuses the node rather than reading the members it recognises as the
	// whole of it.
	RepoCraftsNodeVersion = 1

	// CredentialsServedEnv is C1's one recorded fact about a repository, "its
	// credentials are served" (C1 (h), F20), as the job's entry: the run's
	// launcher and runner read it out of the execution's environment, and its
	// true value is ServedValue and nothing else — an absent entry is not true
	// (the same road, and the same reading, as ProviderKeysServedEnv).
	//
	// It replaces the keys plan's second fact, "the stage account is served"
	// (F20): under D3068 a stage's account is the access of that stage's craft,
	// so one fact per repository covers it, and one fact is what CM15 records.
	CredentialsServedEnv = "FUGARO_CREDENTIALS_SERVED"

	// RepoEntryCredentialsServedKey is the key the fact above is recorded under
	// on a repository's own entry in the installation's config (localcfg.Repo,
	// the field CM15 adds): whether the bundle holds every credential that
	// repository's crafts resolve to, as `fugaro update` read it, and the field
	// the job's entry is then planned from (internal/infra's spec builder).
	//
	// It is a NEW KEY beside CredentialsServiceReady rather than that field's
	// second meaning: that one records that the repository's root was applied
	// with the service ON, and this one records what the bundle holds for the
	// crafts the repository now resolves to — a repository can be applied with
	// the switch on and still have a craft whose credential the bundle does not
	// hold, which is a launch refused with the fix (F19) and not a quiet fall
	// back to a mount.
	RepoEntryCredentialsServedKey = "credentials_served"
)
