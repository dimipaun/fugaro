// Package sharedjob is the contract a Bitbucket shared job is built against
// (plan C1, slice bb-shared-jobs). It declares the shapes the sides of that
// work are written against while they are written in parallel, and nothing
// that behaves: the stubs (stub.go) return a shape and decide nothing, the
// sides replace the calls they make and not the shapes, and the integration
// task I1 deletes the stubs once the sides are wired.
//
// The four sides, and the shape each one owes or reads:
//
//   - the deployer (S1, internal/infra's ProjectJobs) deploys one project job
//     per (profile, provider, account) for a valid Bitbucket profile: no
//     Bitbucket secret mounted, the credential proxy required. It reads
//     BitbucketProfile.
//   - the credentials service (S2, internal/credsvc's Bitbucket route) pairs a
//     call a shared job made, and refuses a repository the policy or the
//     published bitbucket_token block does not admit. It produces Pairing, and
//     the sentence in it is the one the runner shows (S2's Interfaces line in
//     the plan: "produces the refusal text the runner shows").
//   - the runner (S3, internal/runner) runs a shared Bitbucket job with no
//     token at all — its git, REST, submodule and LFS traffic reaches the
//     forwarder — and halts with that sentence when a repository is refused.
//     It consumes Pairing.
//   - the launcher (S4, internal/cli's launchProjectJobFor) and its twin in
//     the runner (the candidate logic) decide which profile a repository's
//     workflow runs on, and therefore which project job it lands on. They
//     produce and consume JobChoice.
//
// The contract rests on the owner's answers of 2026-11-13 recorded in
// docs/shared-jobs-default/reconciliation.md: the pairing is the verified
// identity paired against the profile's allow_repos and the job holds no token
// at all (D2302), a baked workflow runs on a shared job like any other (D2304),
// and a project that mixes providers gets one default profile per provider
// (D2310, R8). The prose half — what each side may rely on, and what none of
// them may — is docs/shared-jobs-default/contract.md.
//
// Two rules hold for everything in this package. Nothing here carries a
// credential: there is no token field, no secret value and no claim text, and
// the most a shape names is a repository and a profile. And no shape decides
// anything by being empty: the zero value of every shape here is the
// fail-closed one, because the sides are written in parallel and a shape that
// is not yet a decision must never read as one.
//
// One hazard is named here rather than left to be discovered: the predicate
// that answers "is this profile a Bitbucket shared job?" elsewhere in the tree
// says the opposite of what the sides here need, and it is the name a side is
// likeliest to reach for. BitbucketProfile's own comment says what it is and
// what becomes of it.
package sharedjob

// RunIdentity is the run identity the credentials service verified for a call
// from a shared job, as the pairing needs it. Every field is a fact the
// service read off the caller's own token and checked, and none of it is
// text the request carries: which job called is the verified service-account
// email and never the X-Fugaro-Run-Job header, which the caller writes
// (internal/credsvc's verifyJob decides the class from the email for that
// reason).
//
// It is the three facts a pairing reads and not the whole of credsvc's
// Identity: the project (fp), the audience and the expiry are checks the
// service has already made by the time a pairing runs, and carrying them here
// would invite a side to make one of them again, in its own words.
type RunIdentity struct {
	// Account is the verified service-account email the caller called with.
	// One service account is dedicated to one Cloud Run job
	// (gcp.ProjectServiceAccountID), so this is what says which shared job
	// called — and, with the published policy, which profile that job serves.
	Account string
	// Run is the run id (the fr claim), which names the run in the refusal
	// sentence and in the service's audit line.
	Run string
	// Slug is the run's own repository as the run identity proves it: the
	// storage prefix task.Slug derives from the provider and the canonical
	// path, carried in the uid (r~<slug>~<run>) and repeated by the fs claim,
	// which credsvc's verifyJob holds to the uid. A shared job serves every
	// repository its profile admits, so this and not the calling account is
	// what fixes the repository a run belongs to — and it is the repository a
	// call is paired to, never one the request names.
	//
	// It is a slug and not a canonical name, and it does not invert into one:
	// the readable half is lossy (a truncated, "-"-joined path, merged across
	// distinct repositories by design) and the provider is not in it, which is
	// why credsvc recovers a canonical name from a policy-held list and never
	// from the slug's own text (internal/credsvc's canonicalOfSlug; F18 of
	// docs/workspaces). So no side derives a repository from this string, and
	// the canonical one is resolved against a published source — see
	// contract.md's "What each side may rely on" for the two the tree has and
	// the open item about wildcards.
	Slug string
}

// Pairing is the credentials service's answer for one call a shared job's run
// made: the identity it verified, the profile the calling job serves, and the
// verdict — allowed, or refused with the repository the call asked for named
// in the refusal.
//
// The verdict is intentionally two fields rather than one enum: a caller shows
// the refusal sentence when Refusal is set, and a shape nobody has decided yet
// (a stub's, and the zero value) sets neither, so it is a refusal. A pairing
// the service really made always answers one of the two, and a service that
// answered neither would admit nothing.
type Pairing struct {
	Identity RunIdentity
	// Profile is the job profile the calling job serves: the profile axis of
	// the job's own name, which is how the policy's allow_repos, its secrets
	// and its accounts are reached from a verified account. It is the profile
	// the pairing judges the call against (projectpolicy.Policy.Admits), and
	// never one the caller named.
	Profile string
	// Allowed is the verdict: the call is paired to the run's repository, and
	// the policy admits it and the published bitbucket_token block covers it.
	Allowed bool
	// Refusal is the sentence refusing the call, naming the repository the
	// call asked for, and "" when the call is allowed. The runner halts with
	// it, which is why it names a repository and no other repository's name:
	// an operator reads which repository was refused and edits the profile or
	// the token's allow_repos. It never carries a credential value, and never
	// repeats a request's own text beyond the repository's name.
	//
	// The name in it is request text by construction, and that is the one place
	// request text belongs in this shape: it is there for an operator to read,
	// and it is no part of the verdict — the pairing decides on the verified
	// slug in Identity alone, so a caller that named another repository in a
	// refusal it caused has named a repository, not widened anything.
	//
	// A pairing that carries one is a refusal whatever Allowed says: these
	// shapes fail closed, and a sentence naming a repository the call may not
	// reach is not also a permission.
	Refusal string
}

// JobChoice is which project job a launch runs on: the repository and the
// workflow a launch names, and the profile that serves them — or no profile,
// which is the per-repository job.
//
// It is what the launcher and the runner's candidate logic both answer, over
// the same two facts, and they must agree: the launcher computes the job's
// name from this choice, and the runner matches CLOUD_RUN_JOB against the same
// name. A workflow that names no profile resolves to the default profile of
// its repository's provider — "standard" for the project's first provider and
// "standard-<provider>" for another (D2310, R8) — so "none" here means the
// profile of no provider admits the repository, and the launch keeps its
// per-repository job (the pre-shared-job path, which is the only job there is
// on an installation that publishes no profile).
type JobChoice struct {
	// Repo is the canonical repository the launch names.
	Repo string
	// Workflow is the workflow the launch names, as fugaro.yaml spells it.
	Workflow string
	// Profile is the profile the workflow runs on, and "" when the choice is
	// no project job.
	Profile string
}

// BitbucketProfile is a Bitbucket profile as the sides of this contract read
// it: the fields of the published profile (projectpolicy.Profile) a shared
// Bitbucket job is built from, named once here so the deployer and the
// launcher cannot drift about which ones they are — including the account axis
// of the job's own name, which neither of them may compute for itself.
//
// It is a projection and not the published profile itself, deliberately. C1 is
// the shape the sides are written against, so a field a side needs is a change
// to this file that the review sees: the plan anticipates exactly that (CR1
// amends C1 if S2's review finds the pairing needs the workflow name), and
// C1's first review did exactly that when the job's name showed the projection
// was short of the account axis and the resources — where embedding the
// published document would have let each side quietly read whichever field it
// liked. A side that needs a field the projection does not carry amends this
// file; it does not read around it.
//
// It is not projectpolicy.SharedBitbucketProfile, and the two rules point
// opposite ways. That predicate is the per-repository Bitbucket job's — the
// bitbucket provider AND a mount of the Bitbucket token (D473, pinned by
// TestSharedBitbucketProfileNeedsTheTokenMount) — while a profile a shared job
// is deployed for is the provider the job serves and no Bitbucket token mount
// at all (D2302). So the name that is easiest to reach for is the wrong test in
// the most expensive direction: a side that used it as the shared-job test
// would deploy a tokenless shared job for exactly the profiles this contract
// refuses one, which is why the hazard is named here and in contract.md. What
// becomes of the predicate is S1's call, not this package's.
type BitbucketProfile struct {
	// Name is the name the policy holds the profile under (the key of its
	// profiles block). It is an axis of the job's name and what JobChoice
	// answers, and the published document does not carry it — the map key is
	// the name.
	Name string
	// Provider is the provider the profile serves. A shared job serves one
	// provider: it is part of the job's name and of the git credential the job
	// holds, and a Bitbucket shared job's is "bitbucket". It is carried rather
	// than assumed from this type's name, because a caller reads the fact it
	// acts on.
	Provider string
	// Accounts is the account axis of the profile's jobs, one entry per job:
	// the "" no-account job and one entry per account the profile lists, with
	// "default" spelled "" — exactly config.ProjectJobAccounts' answer over the
	// published profile, which is the one function that derives it. It is
	// carried and not left to each side because a profile's jobs are one per
	// (profile, provider, account): the deployer names each job
	// gcp.ProjectJobName(Name, Provider, account) and the launcher computes the
	// name it launches the same way, so a side that derived this axis for
	// itself could launch an account's run at a job nobody deployed. A profile
	// that lists no claude_accounts still has its one no-account job, and that
	// is the "" entry: for a profile that exists this list is never empty.
	Accounts []string
	// AllowRepos is the profile's own allow_repos, and empty means the profile
	// names none, so the project's list is what applies. It is a fact for a
	// message or a listing — the repositories this profile names — and it is
	// not the verdict: only projectpolicy.Policy.Admits decides whether a
	// repository may run on the profile, and it holds the project's list, the
	// profile's list and the provider together. A caller that reads this list
	// as a gate admits every repository the project denies while the profile's
	// list is the wider of the two.
	AllowRepos []string
	// Secrets is the secret names the profile mounts. A shared Bitbucket job
	// mounts no Bitbucket token — the job holds nothing and reaches git
	// through the forwarder — so a profile whose list carries
	// config.BitbucketSecret is not a profile a shared job is deployed for
	// (the deployer refuses it, S1), and the field is here because refusing it
	// means reading it.
	Secrets []string
	// CPU, Memory and TimeoutClass are the profile's own resources and timeout
	// class, the fields of those names on config.JobProfile: the deployer builds
	// the job's cpu, its memory and — through config.ParseDuration — its
	// timeout from them. The launcher sizes nothing, but it decides which job a
	// run lands on, and a decision taken about a profile whose job the deployer
	// builds differently is a decision about a job that does not exist.
	CPU          int
	Memory       string
	TimeoutClass string
}
