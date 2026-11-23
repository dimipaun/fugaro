package localcfg

import (
	"maps"
	"reflect"
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
	"gopkg.in/yaml.v3"
)

// SharedMaxBytes caps the published file when read back.
const SharedMaxBytes = 64 << 10

// SharedVersion is the version of the published shared config (Shared), and it
// is not ConfigVersion: what ConfigVersion 2 renamed is the terraform block,
// which the published copy does not carry, and a version a reader does not know
// is refused by name — so the published document keeps the spelling every
// Fugaro of this installation can read, however new the file it came from is.
const SharedVersion = 1

// Shared is the installation-wide, non-secret part of the config, the part
// that is published to the runs bucket. It drops what is owner-only
// (terraform:), personal (user:, commit_name:, commit_email:, endpoints:), derived (bucket_url, the
// legacy registry), deprecated (build.service_account) or local-only:
// providers:, whose base_url is where a key and a repository's code are
// sent, routing:, which is that same decision from the other side — the Claude
// IDs opted in to a provider route, and therefore the models whose prompts may
// leave for a third party; it resolves through the providers this file does not
// carry, so a published switch would name routes nothing in the file defines
// (the node, where a run is actually routed, is written by `project policy
// publish`), and claude_accounts:, whose allow-list is decision T1's operator-only
// trust anchor (design docs/named-oauth-tokens/spec.md §3: never
// published, never settable from fugaro.yaml or the project layer, so it
// must not round-trip through the runs bucket either, which any launcher
// with objectAdmin on it can write) and the account-choice keys
// (default_claude_account:, stages: and claude_account_auto:, local-only for
// claude_accounts' own reason — a key that could name an account, or turn on
// Fugaro's own choice of one, would be a way around that allow-list — and
// published to the policy node instead), secret_rotation_days and secret_expires
// (the same reasoning again: they are the operator's own record of when the
// credentials they hold are rotated and when they expire, which no launcher
// should be able to write for everyone), update_check (same reasoning: a
// machine's choice to silence the nag is its own operator's, not something
// a launcher writing the shared file should be able to flip for everyone),
// and each repo's base_branch and the project's top-level base_branch, which
// the checkout's reviewed fugaro.yaml names (the project's is published to the
// policy node instead, by `fugaro project policy publish`: the node is written
// only by the owner and the runner fails closed on it, while the shared file
// is writable by any launcher). **credentials_service: is published too**, for the reason batch: and
// mailbox: are: the installation's one Cloud Run service, and therefore the
// URL `fugaro run` puts on a job's environment, is the installation's fact and
// not one machine's — the machine that launches is often not the machine that
// opted in, and a local-only copy of it is exactly what goes missing (the
// ledger item of PR #903). **batch: is published**, for the opposite reason: it is the
// installation's own facts (design docs/compute-backend/spec.md §4.4, plan
// 2026-10-19-compute-backend.md Task 8 step 2) — the image a run's job runs,
// the machine type, the network and subnetwork its VMs are placed in, the
// account they run as, the socket gid — and a machine that onboarded before
// the compute root was applied must not have to write them to run somewhere
// else's jobs. Every one of its values that names a cloud resource is held to
// the anchor's GCP project when the file is read back
// (internal/cli's ParseShared), like registry_host, base_images, log_view and
// budget.firebase_project. **mailbox: is published as well**, and for the same
// reason batch: is: a keychain item's name is a fact of the installation's own
// config and not of the machine that reads the key out (mobile P4.1, M5), and a
// machine whose own config has no mailbox: block must not erase the names
// another machine recorded — one per pairing since decision 9, which is why
// MergeShared unions this block's entries rather than taking it whole. What it
// holds is the name and never the key — the key lives in the operator's OS
// keychain and is never written to disk, so nothing published here is a secret.
// The receiver is not modified. The copy is shallow except for repos and
// model_prices: its other maps, slices and pointers alias the receiver's,
// which is safe because the result is only marshalled, and Marshal only reads.
func (c *Config) Shared() *Config {
	s := *c
	// The published document is version 1 whatever the file it was read from
	// says, because the version is what a READER refuses by name and the
	// published document is the one thing here a teammate's older Fugaro meets
	// first: ConfigVersion 2 renamed the two lists in the terraform block and
	// version 3 the member table's accounts, and this copy carries neither the
	// terraform block nor the member table (both dropped below), so nothing in
	// it is either version's. Publishing this machine's ConfigVersion would
	// stop every teammate whose Fugaro is not upgraded yet with "version N is
	// newer than the 1 this Fugaro writes ... then run fugaro update" — a sentence
	// naming a command they cannot run until the shared config they are
	// refused by is readable again.
	s.Version = SharedVersion
	s.Terraform = Terraform{}
	s.User = ""
	// commit_name and commit_email are personal for the reason user: is
	// (D604, D606): they are the identity one machine's launches sign commits
	// with, and a launcher who could write them into the runs bucket would be
	// choosing whose name every teammate's commits carry — and, for
	// commit_email, whose forge account the history links to (D649).
	s.CommitName = ""
	s.CommitEmail = ""
	s.Endpoints = Endpoints{}
	s.Bucket = ""
	s.Registry = ""
	s.Providers = nil
	s.Routing = nil
	s.ClaudeAccounts = nil
	// The credentials, pools and crafts blocks travel claude_accounts' way,
	// into the policy node, and are dropped here for its reason and one of
	// their own: a credential names a secret and a craft's access decides
	// which one a run's calls go out on, so a launcher who could write either
	// into the runs bucket's shared file could aim every teammate's runs at a
	// credential the owner never admitted (F12, D1628).
	s.Credentials = nil
	s.Pools = nil
	s.Crafts = config.Crafts{}
	// The routing table is dropped with them and for their reason: it names
	// which credential serves which model, and by which admissions, so a
	// launcher who could write it into the runs bucket's shared file would be
	// aiming every teammate's calls at a credential the owner never admitted
	// (D3182, F12). It travels to the policy node instead, which the runner
	// reads and fails closed on — the table's own reader, and the one document
	// here that is not writable by a launcher.
	s.ModelRouting = nil
	// The cascade, the grades and the removals travel the three blocks above
	// into the policy node, and every one of them is dropped here for the
	// reason they are, each with its own edge. The cascade's fallback decides
	// whether a run may spend the project's money at all (D152), so a launcher
	// who could publish it would be choosing every teammate's spending; a grade
	// names the model a rung of the ladder runs and the credential or pool its
	// calls go out on, which is the grant the pool and craft blocks are dropped
	// for one step further out; and the removals are the one block here whose
	// publication would be destructive rather than a widened grant — a launcher
	// who could write them would be deleting pools, credentials and crafts of
	// the project's node from the file it can write (D2607, F23, F22).
	s.CredentialCascade = nil
	s.ModelGrades = nil
	s.NodeRemovals = nil
	s.ProjectSecrets = nil
	s.WorkflowSecrets = nil
	// BitbucketAPIRoots is the owner's policy as well, and it travels the same
	// way ProjectSecrets does: `fugaro project policy publish` copies it into
	// the policy node, which the runner reads and fails closed on. A launcher
	// who could write this key into the runs bucket's shared file would be
	// choosing where a run sends the Bitbucket token.
	s.BitbucketAPIRoots = nil
	// BitbucketTokens is operator-only for project_secrets' reason, and the
	// drop carries one of its own that is sharper: the block is what a request
	// is matched against — a record's coverage decides which token carries
	// which call (R1, D2351) — so a launcher who could write it into the runs
	// bucket's shared file would be stating its own token's reach, the one
	// fact the operator's local config is the only writer of. It travels to a
	// run through the policy node instead, and it is dropped here besides so
	// that an older CLI's strict ParseShared never meets the key at all: a
	// shared file carrying it stops an un-upgraded teammate with "field
	// bitbucket_tokens not found in type localcfg.Config", a lockout over a
	// block none of their runs reads.
	s.BitbucketTokens = nil
	// CloneURLs is the owner's policy too, and travels the same way: `fugaro
	// project policy publish` copies it into the policy node, which the runner
	// reads and fails closed on. A launcher who could write this key into the
	// runs bucket's shared file would be choosing where every teammate's runs
	// clone from (SD5, D842), which is the directory the run's own slug, locks
	// and budget session name. ParseShared refuses the key besides, because a
	// shared config carrying it was not written by the converge.
	s.CloneURLs = nil
	// Locations is local-only, and it is the one key here dropped for a
	// reason that is not about who may write the runs bucket: the published
	// copy decodes strictly, so a `locations:` key in it stops every teammate
	// whose Fugaro is older than the release that wrote it with "field
	// locations not found in type localcfg.Config" (F17, D3087 as amended).
	// The record is one machine's reading of the cloud and is never an input,
	// so there is nothing in it for another machine to read anyway — every
	// machine writes its own on its next `fugaro update` — and ParseShared
	// refuses the key besides, because a shared config carrying it was not
	// written by the converge.
	s.Locations = nil
	// Profiles are the owner's policy, published through `fugaro project
	// policy publish` into the policy node — not through the shared config a
	// launcher can write (the node is what the runner reads and fails closed
	// on). Dropping them here keeps the shared file to what it was before
	// phase 3 added profiles.
	s.Profiles = nil
	// MobileAllow is the owner's own for clone_urls' reason and one of its own:
	// the block is who may use the project from a phone, with the role each
	// entry carries, so a launcher who could write it into the runs bucket's
	// shared file would be admitting itself to every teammate's runs with
	// whatever role it named. It travels to the node through `fugaro project
	// policy publish`, which is the write the deployed rules read, and it is
	// dropped here besides so that a published file never carries it at all:
	// internal/cli's ParseShared refuses the key by name.
	s.MobileAllow = nil
	s.UpdateCheck = nil
	// Issues is local-only for update_check's reason: the nudge is a line the
	// section prints on the machine reading it, and no other machine acts on
	// this one's thresholds — the committed report carries no part of them
	// (T-IS15, F14). Dropping the block here also keeps an older CLI's strict
	// ParseShared from refusing a shared file whole over keys it does not
	// know.
	s.Issues = nil
	// Escalation is local-only and sharpest of all: the ladder's thresholds
	// decide when a stalled item is taken off its recipe, what it runs on then
	// and at what cost it is merged under the gate, so a launcher who could
	// write the block into the runs bucket's shared file would be choosing how
	// every teammate's runs merge (D2032, G3). ParseShared refuses the key
	// besides, because a shared config carrying it was not written by the
	// converge.
	s.Escalation = nil
	// The operator's rotation record is local-only for update_check's reason,
	// one credential at a time: secret_rotation_days is how often the person
	// who holds the provider accounts means to rotate them, and secret_expires
	// is what their own page for a token says — neither is a fact of the
	// installation, and a launcher who could write the shared file would be
	// choosing when every other machine is told its credentials are overdue.
	s.SecretRotationDays = 0
	s.SecretExpires = nil
	s.Build.ServiceAccount = ""
	s.BaseBranch = ""
	// BackendName is dropped for BaseBranch's reason, and one of its own: it
	// is a whole backend for every machine that reads the file, and the
	// checks that keep that safe are `fugaro doctor`'s, which reads the
	// selected config on the machine running it and is what the enable task
	// puts in front of the name -- not something a launch consults. A
	// launcher who could write `backend: cloud-batch` into the runs bucket
	// would therefore move every teammate who has no local config onto VMs
	// with a `batch:` block they wrote, while the machine that ran the
	// provisioning and the doctor reading never sees the file at all
	// (docs/shared-config/spec.md §4; the reader refuses the key besides).
	s.BackendName = ""
	// ReviewFloor is the owner's policy and travels the way BaseBranch does,
	// through `fugaro project policy publish` into the policy node. It is
	// dropped here for a sharper reason than the keys above it: the floor is
	// what stops a launcher turning review off, so a floor a launcher could
	// write into the runs bucket's shared file would be that launcher setting
	// its own floor — and ParseShared refuses the key besides, because a
	// shared config carrying it was not written by the converge.
	s.ReviewFloor = nil
	// VerifierAccounts is the owner's policy and travels the floor's way, into
	// the policy node — and it is dropped here for a sharper reason than the
	// floor's: it is the list the credentials service admits to its verify
	// route, so a launcher who could write it into the runs bucket's shared
	// file could admit its own address there. ParseShared refuses the key
	// besides, because a shared config carrying it was not written by the
	// converge.
	s.VerifierAccounts = nil
	// Binaries is the owner's policy as well, and it travels the floor's way,
	// into the policy node — and it is dropped here for a sharper reason than
	// the floor's: the block is the grant that decides which bytes a run may
	// add, so a launcher who could write it into the runs bucket's shared file
	// would be admitting its own payload to every teammate's runs, which is the
	// rule reading itself out of the way. ParseShared refuses the key besides,
	// because a shared config carrying it was not written by the converge.
	s.Binaries = nil
	// ReportsCommit is the owner's policy too, and it travels the floor's way,
	// into the policy node — and it is dropped here for a sharper reason than
	// the floor's: the member decides which repository every report of the
	// project is committed into, so a launcher who could write it into the runs
	// bucket's shared file would be aiming the project's reports at a repository
	// of its own choosing, which is the one decision D2751 keeps to the owner.
	// ParseShared refuses the key besides, because a shared config carrying it
	// was not written by the converge.
	s.ReportsCommit = nil
	// ImageCatalog travels the verifier list's way and is dropped here for a
	// sharper reason than the floor's: the catalog decides which image a run's
	// job executes in and which Dockerfile is fetched to build it, so a
	// launcher who could write it into the runs bucket's shared file would be
	// choosing what code every teammate's runs run (D1140). It is published
	// through the policy node instead, which the runner reads and fails closed
	// on, and ParseShared refuses the key besides — a shared config carrying
	// it was not written by the converge.
	s.ImageCatalog = nil
	// ImageMap travels the catalog's way and is dropped here for its reason,
	// one step stronger: a map that names the image each repository runs is the
	// same choice the catalog is, made per repository, so a launcher who could
	// write it into the runs bucket's shared file would be choosing what code a
	// teammate's runs execute. ParseShared refuses the key besides, because a
	// shared config carrying it was not written by the converge.
	s.ImageMap = nil
	// BakeRepos is dropped here for the catalog's reason too: whether a
	// repository is baked into its image is the owner's decision about every
	// repository of the project, and a launcher who could write it into the
	// shared file could turn baking off for everyone — every job of the project
	// cloning again, from one file it can write. ParseShared refuses it besides.
	s.BakeRepos = nil
	// Triage travels the bake switch's way too, and is dropped here for the
	// floor's reason: the stage is what judges the findings a review round's
	// table would have blocked, so a launcher who could write the switch into
	// the shared file would be waving every teammate's runs past the findings it
	// holds back — the floor's own decision, one level down. ParseShared refuses
	// it besides, because a shared config carrying it was not written by the
	// converge.
	s.Triage = nil
	// ReviewAgainstTask travels the triage switch's way too, and is dropped
	// here for its reason one key over: the switch is what turns off the check
	// every review stage makes of a run's own work, so a launcher who could
	// write it into the shared file would be having every teammate's pull
	// requests reviewed against nothing — the check the owner published the
	// node for. ParseShared refuses it besides, because a shared config
	// carrying it was not written by the converge.
	s.ReviewAgainstTask = nil
	// PersonalWorkspaceTokens travels the triage switch's way too, and is
	// dropped here for its reason one key over: the switch decides whether a
	// project's people draw on the token each stored at the workspace instead of
	// the shared entries the workspace pays for, so a launcher who could write it
	// into the shared file would be moving every teammate's runs onto a spend the
	// owner's own statement is about — and the switch is read from the published
	// node, which the launcher cannot write. ParseShared refuses it besides,
	// because a shared config carrying it was not written by the converge.
	s.PersonalWorkspaceTokens = nil
	// RigorPending is dropped here for Triage's reason and one of its own that
	// is nearer still: it is the wizard's rigor answer waiting to be written
	// into the project layer, and the mode decides how much review every run of
	// the project gets, so a launcher who could put one in the shared file would
	// be choosing the review every teammate's runs are held to — the mode
	// `fugaro rigor set` refuses to lower behind the owner's back (D1026), set
	// by whoever can write this file. And it is a key that means nothing on
	// another machine: it is one machine's hand-off, and a teammate whose
	// config filled the gap from here would have its own converge write that
	// mode into the layer file nobody on that machine answered for.
	// ParseShared refuses the key besides, because a shared config carrying it
	// was not written by the converge.
	s.RigorPending = ""
	// Orchestrator travels the catalog's way, into the policy node, and is
	// dropped here for the same reason the floor is: the block is what stops a
	// launcher spending the project's budget faster than the owner allows — the
	// running cap and the per-minute, hourly and daily spawn rates are the
	// figures a looping cycle runs up against (D1239) — so a launcher who could
	// write it into the runs bucket's shared file would be raising its own
	// guardrails. ParseShared refuses the key besides, because a shared config
	// carrying it was not written by the converge.
	s.Orchestrator = nil
	// The account-choice keys are local-only for claude_accounts' own reason
	// (docs/accounts-add/spec.md §11.1): a published layer that could name an
	// account would be a way around that account's allow_data_to, which is the
	// operator-only trust anchor T1 makes it. They travel to a run through the
	// policy node instead — the owner writes it and the runner fails closed on
	// it — and ParseShared refuses the keys besides, because a shared config
	// carrying one was not written by the converge.
	s.DefaultClaudeAccount = ""
	s.ClaudeAccountStages = nil
	s.ClaudeAccountAuto = false
	// The aliases the choice keys may hold are dropped with them and for their
	// reason, one step nearer the credential: a published alias would name an
	// account in a file any launcher can write, which is the way around the
	// account's own allow_data_to these keys are local-only to close
	// (docs/account-aliases, D2090). A launcher on another machine reads no
	// alias, exactly as it reads no default_claude_account today, and
	// ParseShared refuses the key besides.
	s.ClaudeAccountAliases = nil
	// The subscription switch goes with them, for their reason and one of its
	// own: it is the owner's own act about the terms his subscriptions are held
	// under (D1622), and a launcher who could write it into the shared file
	// could spread a project across subscriptions nobody agreed to spread.
	s.SubscriptionSpread = nil
	// The workspace keys are local-only, and the pin is the sharpest of them:
	// the shared file is written into the runs bucket, which any launcher of
	// the project can write, so a launcher who could put a workspace_pin there
	// would be aiming this project's exchange — and the assertion key it
	// verifies against — at a party of its choosing. The membership key and
	// the owner's member table are dropped with it: a shared file that could
	// name a workspace would be one that could join this project to a group
	// nobody here chose, and the owner's shared list goes with the member table
	// for its reason once more — a launcher who could write it would be
	// choosing what the workspace's credentials service offers, which is the
	// owner's act and no one else's. ParseShared refuses all five keys besides,
	// because a shared config carrying one was not written by the converge.
	s.Workspace = ""
	s.WorkspacePin = WorkspacePin{}
	s.WorkspaceMembers = nil
	s.WorkspaceCap = nil
	s.WorkspaceShared = nil
	s.Repos = withoutRepoLocalKeys(c.Repos)
	s.ModelPrices = withoutCacheSpellings(s.ModelPrices)
	return &s
}

// withoutCacheSpellings is a copy of model_prices with the three per-million
// keys and the three `_mult` keys cleared, leaving the old cache_read,
// cache_write_5m and cache_write_1h keys: the published copy keeps the
// multiplier under the name it has always had (D2646 as amended), so a
// teammate's Fugaro — which may be older than this file and knows the old keys
// alone — meets the document it knows. The multiplier field itself is left as
// it is: the loader has already filled it from whichever spelling the owner
// wrote, and MarshalYAML writes it under its old name.
func withoutCacheSpellings(prices map[string]ModelPrice) map[string]ModelPrice {
	if prices == nil {
		return nil
	}
	out := make(map[string]ModelPrice, len(prices))
	for id, mp := range prices {
		mp.CacheWrite5mPerM, mp.CacheWrite5mMult = nil, nil
		mp.CacheWrite1hPerM, mp.CacheWrite1hMult = nil, nil
		mp.CacheReadPerM, mp.CacheReadMult = nil, nil
		out[id] = mp
	}
	return out
}

// withoutRepoLocalKeys is a copy of repos with the two keys of an entry that
// are never published cleared: base_branch, which comes from the checkout's
// reviewed fugaro.yaml, and claude_accounts, which is the operator's own
// admission statement — the repository-level half of the trust anchor T1 keeps
// in the operator-only local config (see Shared's own note on claude_accounts,
// and named-oauth-tokens.md §3).
//
// claude_accounts is stripped for the reason the top-level block is: the file
// is written into the runs bucket, which any launcher of the project can write,
// so an entry published here would let one add a repository to an account's
// admission by editing that file — and the machine that then published the
// policy would derive an admission the owner never made. ParseShared refuses
// the key besides, because a shared config carrying one was not written by the
// converge.
func withoutRepoLocalKeys(repos map[string]Repo) map[string]Repo {
	if repos == nil {
		return nil
	}
	out := make(map[string]Repo, len(repos))
	for k, r := range repos {
		r.BaseBranch = ""
		r.ClaudeAccounts = nil
		out[k] = r
	}
	return out
}

// SameInstallation reports whether a published object names the same
// installation as local: the same project name and GCP project. A foreign
// one must never be merged from.
func SameInstallation(published, local *Config) bool {
	return published != nil && published.Name == local.Name && published.GCPProject == local.GCPProject
}

// MergeShared is what to publish when published is what the runs bucket
// already holds (nil when absent) and local is this machine's config: the
// shared subset of local (Shared), filled in from published so that a
// machine that lacks what another onboarded does not erase it. Map fields
// (repos, base_images, model_prices, compute_prices, mailbox.keychain_item)
// are unioned, the local entry winning a key clash — with one field inside
// repos read the other way round (mergeRepoRecords); scalar and pointer fields
// take the local value when it is non-zero, else the published one; name,
// gcp_project, runs_bucket and version always come from local. The batch: block
// is read the other way round for its opt-in fact and for nothing else
// (mergeBatchBlock, R15): that fact is the published copy's, and the retired key
// fact is never published at all. What Shared drops is never taken from
// published either. A published object of another installation
// (SameInstallation) is ignored. Neither argument is modified.
//
// The two writers that CHANGE the opt-in fact — an operator's selection of the
// backend (R2) and the opt-out (R16) — are MergeSharedOptIn, which is this
// function with the value they are writing passed explicitly.
func MergeShared(published, local *Config) *Config { return MergeSharedOptIn(published, local, "") }

// MergeSharedOptIn is MergeShared with the opt-in fact its caller is writing
// (R15): optIn, when it is one of the fact's written values, is what the
// published block carries, and "" is an ordinary publish, where the published
// copy's own value is kept. Only the two writers R15 names pass one — the
// operator's selection passes BatchOptInOn (R2) and the opt-out passes
// BatchOptInWithdrawn (R16) — and they have to, because every other publish
// takes the fact from the published copy: without an explicit value a machine
// whose own block says `in` would never be able to opt an installation in, and
// one whose block still says `in` would keep a withdrawal from being written.
func MergeSharedOptIn(published, local *Config, optIn string) *Config {
	out := local.Shared()
	if !SameInstallation(published, local) {
		// Nothing of the installation's is published to merge from — an empty
		// bucket, or a copy ParseShared refused, which the publisher replaces
		// (O18) — and the block published is this machine's own whole. The fact
		// in it is the one this call would otherwise have taken from a published
		// copy there is none of: the value a writer passed, and this machine's
		// own block's for an ordinary publish — which is why a writer's value is
		// an argument and not a rule read off the block, the absence of a copy to
		// keep it in being exactly where it would otherwise be lost (R15).
		out.Batch = mergeBatchBlock(out.Batch, nil, optIn)
		return out
	}
	out.Repos = withoutRepoLocalKeys(mergeRepoRecords(published.Repos, out.Repos))
	out.BaseImages = unionMaps(published.BaseImages, local.BaseImages)
	// The published side is stripped of the new spellings for withoutRepoLocalKeys'
	// reason, one file over: a shared document carrying cache_read_mult would stop
	// a teammate whose Fugaro is older than this file with an unknown field, which
	// is the lockout Shared's own version exists to prevent.
	out.ModelPrices = withoutCacheSpellings(unionMaps(published.ModelPrices, out.ModelPrices))
	out.ComputePrices = unionMaps(published.ComputePrices, local.ComputePrices)
	keep := func(dst *string, old string) {
		if *dst == "" {
			*dst = old
		}
	}
	keep(&out.Region, published.Region)
	keep(&out.RegistryHost, published.RegistryHost)
	keep(&out.LogView, published.LogView)
	keep(&out.ImageSource, published.ImageSource)
	keep(&out.SchedulerRegion, published.SchedulerRegion)
	if out.Build == (Build{}) {
		out.Build = published.Build
		out.Build.ServiceAccount = ""
	}
	out.Batch = mergeBatchBlock(out.Batch, published.Batch, optIn)
	if out.Budget == nil {
		out.Budget = published.Budget
	}
	if out.Watch == nil {
		out.Watch = published.Watch
	}
	if published.Mailbox != nil || out.Mailbox != nil {
		// The mailbox block is published for the reason batch: and watch: are
		// (T2's plan, §13): the item names are the installation's record and not
		// one machine's, and a publisher whose own config has no mailbox: block
		// — a machine that onboarded before the pairing existed — would
		// otherwise erase the names another machine recorded. What it is NOT is
		// the key: the key never leaves the OS keychain it was generated into
		// (M5), so nothing published here is a secret.
		//
		// Its entries are unioned the way repos and base_images are, and not
		// taken wholesale: decision 9 made the block one item per pairing, so
		// the names in it are several machines' — a laptop that paired its own
		// phone would otherwise publish a file naming its phone and no other
		// machine's, which is exactly the erasure the paragraph above is about,
		// one entry at a time. Local wins a clash (one uid is one phone). A
		// fresh block rather than either argument's, so neither is modified.
		items := func(m *Mailbox) map[string]string {
			if m == nil {
				return nil
			}
			return m.KeychainItem
		}
		out.Mailbox = &Mailbox{KeychainItem: unionMaps(items(published.Mailbox), items(out.Mailbox))}
	}
	if out.CredentialsService == nil {
		// The credentials service block is published for the same reason, and
		// it is the one the launcher reads: the URL is the installation's own
		// Cloud Run service, and the machine that runs a launch is often not
		// the machine that opted in. A publisher whose own config has no block
		// is a machine that never ran the opt-in — a teammate who onboarded a
		// repository with `fugaro update --repo`, a CI box whose config came
		// from the published file — and taking its nil over the published one
		// would erase the installation's one record of its service, silently,
		// so that every later launch of the project went out without the
		// variable while the installation has the service on (the ledger item
		// of PR #903).
		//
		// The cost of that rule is the opt-out's: an installation whose service
		// is removed keeps the published block until an operator clears it, and
		// a launch that reads it puts a dead URL on the job. That is the
		// direction this file takes everywhere (a stale fact is recoverable; an
		// erased one is not), and the runbook's opt-out section says what to
		// remove.
		out.CredentialsService = published.CredentialsService
	}
	if out.MaxParallel == 0 {
		out.MaxParallel = published.MaxParallel
	}
	return out
}

// mergeBatchBlock is the `batch:` block the publish writes: the publishing
// machine's own block when it has one, else the published one — the rule the
// block has had since it was published at all, and the reason for it: the block
// is the installation's facts, not one machine's (the image a run's job runs,
// the machine type, the network and subnetwork its VMs are placed in, the
// account they run as, the socket gid), so a publisher whose own config has no
// block is a machine that did not apply the compute root, and taking its nil
// over the published one would erase what another machine onboarded, silently,
// in the only place the installation's copy lives. Every field of the block
// keeps that rule and only its two identity facts do not.
//
// **The opt-in fact comes from the published copy, whatever the publishing
// machine's own block says** (R15, D3110 as amended): the fact says whether the
// INSTALLATION's compute root is applied, and one machine's block is no more
// the authority on it than on the network its VMs are placed in. A machine
// whose own block lacks the fact — the common case, a teammate who onboarded
// after an operator opted the installation in — must not publish the absence
// over it, and a machine whose block holds the other value (a stale `in` after
// an opt-out, which its own copy follows at its next `fugaro update`) must not
// publish that either: the merge is what makes "a machine's copy of it is never
// the authority" true of the runs bucket, and it is the whole of the rule,
// because a launch reads the published copy whenever the machine holds no local
// config of its own. A withdrawal is a written value beside `in` — see
// batch_sealkey.go — so it is carried here exactly as `in` is and never read as
// an absence.
//
// optIn is the single exception, and it is the two writers' (R15): the operator
// who selects the backend passes BatchOptInOn (R2) and the opt-out passes
// BatchOptInWithdrawn (R16), and the value they pass is published. It has to be
// an argument rather than a rule read off the block, because "this machine's
// value wins" would be the erasure above and "the published value wins" would
// leave both writers unable to write anything: only the two commands that mean
// the change may carry one.
//
// **The key fact is never published** (R10, BR23): it is the retired half of
// the block's identity facts, the Cloud KMS key version a launcher wrapped a
// run's budget token to and its public key, so the publish drops it here, in
// place, exactly as `batch.secrets` leaves the block — a block that still
// carries one is read and not refused (the fields stay in the config for that
// reason, and Shared keeps them), and what a reader of the runs bucket gets is
// the block without it. Dropping it only in the document the publish writes is
// what makes a key fact written by the release behind unrepublishable: the
// machine's own file is `fugaro update`'s to rewrite in place, and until it
// does, the field it still holds is one no other machine ever reads.
//
// The block is returned as a fresh one, so neither argument is modified: the
// result is marshalled and written, and the caller keeps its own block.
func mergeBatchBlock(local, published *Batch, optIn string) *Batch {
	if local == nil {
		local = published
	}
	if local == nil {
		return nil
	}
	out := *local
	switch {
	case optIn != "":
		out.OptIn = optIn
	case published != nil:
		out.OptIn = published.OptIn
	}
	out.SealKey = nil
	return &out
}

// mergeRepoRecords is unionMaps over the repository entries with one field
// carried rather than won: a local entry with NO record of the credentials
// service's switch takes the published entry's record.
//
// It is the one field here that is not the local entry's to overwrite, because
// it is the only one that answers for the INSTALLATION rather than for this
// machine: the record says "this repository's root has been applied with the
// service on", the root is one object shared by every checkout, and the
// publish writes the whole installation's file. A machine that holds no record
// for that repository — the field is new, so every entry written before this
// release has none, and so has every entry on a machine that never applied
// that root — would otherwise publish its own `false`/absence over a record
// another machine wrote from an apply it made. The cost is a `cascade` run of
// that repository: its job gave its model mounts up to the apply that wrote
// the record, so with no record it is handed no address either and halts at
// the service's door (D143), on every machine that reads the published file.
//
// A record the local entry DOES hold wins, in both directions and whatever it
// says: only the door that applies that root writes one
// (internal/cli's recordCredentialsServiceReady and the upgrade backfill), so
// a recorded `false` is an apply that ran with the switch off — the emptied
// bundle, or an auth the service does not serve — and it has to travel to the
// other machines, or a switch-off would never reach anyone.
//
// The limit of that rule is an ordering one, and it is the upgrade backfill
// that closes it rather than this merge: a record carries no stamp, so a local
// `false` left by an EARLIER apply outranks a `true` another machine wrote from
// a later one, and the next publish from the machine holding the `false` writes
// it to the bucket again, publishSharedConfig putting exactly what MergeShared
// returns in it. Every launch that reads the published file then hands no
// address to that repository's cascade runs — whose jobs gave their model
// mounts up to the later apply — so they halt at the service's door (D143).
// What repairs it is the machine holding the stale record, in either direction:
// the backfill re-reads the installation's own state for EVERY entry the config
// holds, whatever record it carries, so that machine's next `fugaro update` —
// the one command the upgrade promises — writes the record true again when the
// state shows the grant and that run's bundle holds a model credential, and
// writes it `false` when the state shows no grant, and publishes the correction
// back (internal/cli's backfillCredentialsServiceReady). The other direction is
// the same stale record with the signs swapped: a `true` outliving the apply
// that took the grant away hands a job an address its account may not knock
// with, which fails every run of it at bootstrap. Until that run, the window
// above is open. Stamping the record would close the window instead of
// repairing it, and would cost the published shape a field.
//
// unionMaps' local-wins rule covers every other field of the entry, and a
// fresh map is returned, so neither argument is modified.
//
// The credentials service's records are carried one by one rather than as one,
// because they are three keys of the file and a machine can hold some and not
// the others: the entry a repository door writes carries all of them at once
// (internal/cli's recordCredentialsServiceReady), while the upgrade backfill —
// the door that repairs the roots no checkout here applies — writes the switch
// alone, and an entry written before R12's fields existed carries the switch
// with no fact beside it. Carrying them together would drop the published fact
// of a machine whose own entry holds the switch, which is the one record that
// door keeps.
func mergeRepoRecords(published, local map[string]Repo) map[string]Repo {
	out := unionMaps(published, local)
	for name, l := range local {
		p, ok := published[name]
		if !ok {
			continue
		}
		if l.CredentialsServiceReady == nil {
			l.CredentialsServiceReady = p.CredentialsServiceReady
		}
		if l.ProviderKeysServed == nil {
			l.ProviderKeysServed = p.ProviderKeysServed
		}
		if l.StageAccountServed == nil {
			l.StageAccountServed = p.StageAccountServed
		}
		out[name] = l
	}
	return out
}

// withoutUnwrittenRecord is a published repository entry as publishing again
// would leave it, for the comparison SharedDiff makes: where the local entry
// carries NO record of the credentials service's switch, mergeRepoRecords
// carries the published one over it, so that key is not a difference — the
// publish would write it back unchanged, and reporting it would nag every
// machine about a repository whose root another machine applied, which is
// exactly the file the shared config is meant to be. A local entry that HAS a
// record keeps the comparison whole: publishing it does change the published
// file whenever the two disagree.
//
// Both arguments are the YAML forms SharedDiff compares (so "carries no
// record" is the key's absence, which is what omitempty writes). The published
// entry is never modified.
//
// It is the same rule for every record of the credentials service — the switch
// and R12's two facts beside it — because the merge above carries each of them
// the same way: a key the local entry does not hold is not a difference, and a
// key it does hold is.
func withoutUnwrittenRecord(local, published any) any {
	le, lok := local.(map[string]any)
	pe, pok := published.(map[string]any)
	if !lok || !pok {
		return published
	}
	out := maps.Clone(pe)
	for _, key := range []string{"credentials_service_ready", "provider_keys_served", "stage_account_served"} {
		if _, ok := le[key]; ok {
			continue
		}
		delete(out, key)
	}
	return out
}

// unionMaps is a new map with every entry of a, and every entry of b over
// it; nil when both are empty.
func unionMaps[K comparable, V any](a, b map[K]V) map[K]V {
	if len(a)+len(b) == 0 {
		return b
	}
	m := make(map[K]V, len(a)+len(b))
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

// SharedDiff names the top-level fields on which the published shared
// config differs from what local would publish (local.Shared()), sorted;
// none when they agree. It is for `fugaro doctor`: a local config wins over
// the published one, and a difference is worth saying.
func SharedDiff(published, local *Config) []string {
	toMap := func(c *Config) map[string]any {
		data, err := c.Marshal()
		m := map[string]any{}
		if err == nil {
			_ = yaml.Unmarshal(data, &m)
		}
		return m
	}
	p, l := toMap(published.Shared()), toMap(local.Shared())
	var out []string
	// These maps are a union across machines when published, while a local
	// config holds only its own entries: only the keys present locally are
	// compared, so an entry only the published object has is no difference.
	// mailbox is the same rule one level down — MergeShared unions
	// keychain_item's entries — so a pairing another machine recorded is no
	// difference either, while a local entry the published object lacks is one
	// (decision 9; without this the pairing on the next desk is reported as a
	// difference on every machine, for a file that publishing again cannot
	// change).
	union := map[string]bool{"repos": true, "base_images": true, "model_prices": true, "compute_prices": true, "mailbox": true}
	differs := func(k string, lv, pv any) bool {
		lm, lok := lv.(map[string]any)
		if !union[k] || !lok {
			return !reflect.DeepEqual(lv, pv)
		}
		pm, _ := pv.(map[string]any)
		if k == "mailbox" {
			li, _ := lm["keychain_item"].(map[string]any)
			pi, _ := pm["keychain_item"].(map[string]any)
			for uid, v := range li {
				if !reflect.DeepEqual(v, pi[uid]) {
					return true
				}
			}
			return false
		}
		for name, v := range lm {
			pv := pm[name]
			if k == "repos" {
				pv = withoutUnwrittenRecord(v, pv)
			}
			if !reflect.DeepEqual(v, pv) {
				return true
			}
		}
		return false
	}
	for k, v := range l {
		if differs(k, v, p[k]) {
			out = append(out, k)
		}
	}
	for k := range p {
		if _, ok := l[k]; !ok && !union[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
