// Package localcfg reads the project configs, the local CLI config of each
// Fugaro project: $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml (else under
// ~/.config), holding the project's name, its GCP project, region and
// buckets, and the onboarded repositories (design §2.4 and §5.4). fugaro
// update writes them; Select picks the one a command acts on.
package localcfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Config is the local CLI config.
type Config struct {
	Version int `yaml:"version"`
	// Name is the Fugaro project (config.ProjectNameRE), which the file
	// is named after.
	Name string `yaml:"name"`
	// GCPProject is the GCP project ID the installation lives in.
	GCPProject string `yaml:"gcp_project"`
	Region     string `yaml:"region"`
	RunsBucket string `yaml:"runs_bucket"`
	Bucket     string `yaml:"bucket_url,omitempty"`
	// Registry is the legacy shared image registry, which is only read:
	// to roll back, and to find the images existing jobs still run.
	Registry string `yaml:"registry,omitempty"`
	// RegistryHost is <region>-docker.pkg.dev/<gcp_project>, the prefix of
	// every image registry of the installation.
	RegistryHost string `yaml:"registry_host,omitempty"`
	// SetupPending is "billing" in the config a first run of fugaro setup
	// writes when it created the GCP project and paused because billing is
	// not linked yet: the rerun resumes that setup (the project stage runs
	// again, as on a first run). The config the installation's apply writes
	// clears it.
	SetupPending string `yaml:"setup_pending,omitempty"`
	// PolicyGeneration is the published policy node's generation as of the last
	// publish that wrote this file's policy keys, or the publish this file was
	// seeded from: the ETag the node was read at, which is the same value the
	// write is conditional on (rtdb.PutIfMatch). A file carrying none was never
	// written from a published node — every installation before the first
	// `fugaro update` of this release — and the next run ADOPTS the generation
	// it reads rather than refusing (D854: refusing once would ask a question
	// with no information in it, since the machine has no baseline to compare
	// against). An installation that writes no policy node carries none.
	PolicyGeneration string `yaml:"policy_generation,omitempty"`
	// LayerGeneration is the published project layer's generation as of the
	// last publish that wrote this file's layer, or the publish the local
	// layer file was seeded from (D854): it is what tells the two readings of
	// `unpublished` apart — a local edit (still the published generation,
	// which is the owner's change to publish) from a stale copy of another
	// machine's (the published generation has moved past it, so publishing
	// would revert someone else's publish). A file carrying none was never
	// written from a published object — every installation before the first
	// `fugaro update` of this release — and the next run ADOPTS the
	// generation it reads rather than refusing. It is the layer stage's
	// record, beside the policy stage's PolicyGeneration: one record per
	// stage that publishes, and neither is wire.
	LayerGeneration string `yaml:"layer_generation,omitempty"`
	// BaseImages are the base images the image builds and the daily image
	// check start from, by base kind (config.Bases). A workflow builds from
	// the image of its base kind, so a project needs an entry for each kind
	// its repositories use.
	BaseImages map[string]string `yaml:"base_images,omitempty"`
	// ImageSource is the registry/owner prefix a base pin's images come
	// from: "<host>/<owner>" (for example ghcr.io/dimipaun), the same
	// string fugaro update's --image-source takes. It is the TRUSTED owner a
	// base_images entry under the remote repository is checked against
	// (design registry-remote-repository.md §3.6), recorded once by the run
	// that pins, published in the shared config, and read from here by
	// every reader — never derived from the reference being checked, which
	// would make the check vacuous. Empty means no pin has been recorded on
	// this installation, and a remote-shaped entry then refuses.
	ImageSource string `yaml:"image_source,omitempty"`
	// BackendName is the compute backend by name (backend.Open's registry
	// key); empty means backend.CloudRun, the only one implemented
	// (design m11-setup-and-skills.md §7). Read it through Backend.
	BackendName string `yaml:"backend,omitempty"`
	// Batch is what the Cloud Batch backend reads: the installation facts a
	// run's VM is built and placed from (design
	// docs/design/compute-backend.md §4.4, plan
	// 2026-10-19-compute-backend.md Task 8 step 2). Only the Batch opener
	// reads it (internal/cli/cloud.go's cloudOpeners) and nothing selects
	// that opener yet, so a config that carries this block still runs on
	// Cloud Run: `backend: cloud-batch` stays refused by validate below until
	// the enable task.
	Batch     *Batch    `yaml:"batch,omitempty"`
	Build     Build     `yaml:"build"`
	Terraform Terraform `yaml:"terraform,omitempty"`
	// ComputePrices override the compute list prices of a region, for
	// cost estimates (design §10.1).
	ComputePrices map[string]Price `yaml:"compute_prices,omitempty"`
	// LogView is the log view Fugaro job logs are read through
	// (projects/<p>/locations/global/buckets/<b>/views/<v>); empty reads
	// the project's default logs.
	LogView string `yaml:"log_view,omitempty"`
	// SchedulerRegion is where the daily image check's Cloud Scheduler
	// jobs live, which need not be the region the jobs run in.
	SchedulerRegion string `yaml:"scheduler_region,omitempty"`
	// Budget is the project's model spend guard (design §5.6); no block
	// means off.
	Budget *Budget `yaml:"budget,omitempty"`
	// ModelPrices replace the built-in price of a model (or add one), by
	// model ID, for the budget's accounting.
	ModelPrices map[string]ModelPrice `yaml:"model_prices,omitempty"`
	// Providers are the non-Anthropic backends models may be routed to
	// (design m10-multi-model.md §3), by name. Only the owner sets them.
	Providers map[string]config.ModelProvider `yaml:"providers,omitempty"`
	// Routing is the routing switch: which route the Claude IDs it names take
	// (design docs/design/project-centric.md §6.2, D1a/D1b). Only the owner sets
	// it, and this file is its only definition site in this release: a
	// repository's fugaro.yaml has no routing key, and D1a's second site, the
	// project layer, does not take it yet (the layer's decode is strict, so a
	// layer that writes routing: fails closed) — the layer's part in routing
	// today is the claim key claude_models.
	// An absent block means direct, today's behaviour. It reaches a run only
	// through the published policy: `fugaro project policy publish` copies it
	// into config/policy/shared, which is where an edit takes effect — and never
	// through the published *shared config* (Shared drops it, as it drops
	// providers): the switch resolves through providers, and whoever can write
	// the runs bucket must not be able to route Claude calls by launching from
	// there.
	//
	// A block written with no Claude entry (`routing: {}`, `routing: {claude:}`)
	// is not refused here — it names nothing, and this loader reads it as it
	// reads an absent block — but it is *not* a statement that the switch is
	// off, and `policy publish` refuses to read it as one: a publish would drop
	// the active route the node carries, and only --remove-routing says that on
	// purpose (the review of PR #414).
	Routing *config.Routing `yaml:"routing,omitempty"`
	// ClaudeAccounts are named Claude OAuth credentials a repository may
	// choose among by name (design docs/design/named-oauth-tokens.md).
	// Only the owner sets them, here, never in fugaro.yaml.
	ClaudeAccounts map[string]config.ClaudeAccount `yaml:"claude_accounts,omitempty"`
	// DefaultClaudeAccount is the account every stage of a run uses when
	// nothing more specific does (spec §11.1, D873). It names an account of
	// claude_accounts, or "default" for the unsuffixed secret, and only this
	// document may hold it: a reviewed repository and a published layer are
	// writable by someone other than the owner, and a key there would be a
	// way around the account's own allow_data_to.
	DefaultClaudeAccount string `yaml:"default_claude_account,omitempty"`
	// ClaudeAccountStages is the account one stage uses instead of the
	// default, keyed by the run's own stage name — stages.review.claude_account
	// is "the reviewer uses another account". A stage is not a role: the
	// roles fold every stage but review into the coder's, so a role-keyed
	// block could not give review_first an account implement does not use.
	ClaudeAccountStages map[string]StageChoice `yaml:"stages,omitempty"`
	// ClaudeAccountAuto is whether Fugaro may choose an account for the owner
	// (spec §11.3, D874). Off is explicit-only, and off is the default: the
	// switch ships dormant until the owner rules that one project may spread
	// across several accounts (D875).
	ClaudeAccountAuto bool `yaml:"claude_account_auto,omitempty"`
	// ProjectSecrets are the reserved secret names (config.ReservedSecrets)
	// the owner has opted into project-wide storage for, by allow-listed
	// repository (design docs/design/project-secrets.md §3). Only the owner
	// sets them, here, never in fugaro.yaml. A provider's or a named Claude
	// account's own allow_data_to governs its own secret instead; neither
	// can collide with a ReservedSecrets key in the first place, so
	// ValidateProjectSecrets checks membership in ReservedSecrets only.
	ProjectSecrets map[string]config.ProjectSecret `yaml:"project_secrets,omitempty"`
	// BitbucketAPIRoots are the Bitbucket API roots the owner allow-lists
	// (D474), for an installation that sends its Bitbucket token to a root of
	// its own rather than to Bitbucket's: a cloud run refuses any
	// FUGARO_BITBUCKET_API_URL outside this list (projectpolicy's
	// bitbucket_api_roots, which `fugaro project policy publish` copies this
	// into — the run reads the node, so this file is where the owner writes
	// it). Only the owner sets it, here, never in fugaro.yaml, and the shared
	// config never carries it: whoever can write the runs bucket must not be
	// able to widen where a credential is sent.
	//
	// It is a copy and not a derivation, and it is held to the node's rule
	// alone (projectpolicy.ValidBitbucketAPIRoots): an https root that reaches
	// one host, without a query, a fragment or userinfo. There is no second
	// copy of that rule here on purpose — a root that the node would refuse is
	// refused by the publish that builds the node, before anything is written,
	// with a sentence naming the entry.
	BitbucketAPIRoots []string `yaml:"bitbucket_api_roots,omitempty"`
	// Profiles are the project's job profiles (design
	// docs/design/project-centric.md §4.1), by name: what a Cloud Run job is
	// deployed as, and what a repository's workflow selects with job_profile.
	// They are the owner's to write, here, never in fugaro.yaml, and
	// `fugaro project policy publish` copies them into the policy node, where
	// the runner reads the one a workflow selected (refusing the run when the
	// policy does not define it). The profile a workflow that names none runs
	// on is config.DefaultJobProfile ("standard"), and a policy that carries
	// profiles must define it (projectpolicy.validProfiles).
	//
	// The document is config.JobProfile — one declaration, shared with the
	// policy node — so a key this file sets cannot be a key the node drops.
	// Only the names are checked here; what a profile holds (its provider,
	// resources, timeout, secrets and admission list) is checked where it is
	// used and published, and by the package that owns those rules.
	Profiles map[string]config.JobProfile `yaml:"profiles,omitempty"`
	// Watch tunes fugaro watch.
	Watch *Watch `yaml:"watch,omitempty"`
	// Mailbox is what `fugaro mailbox` reads out of this project's config
	// (mobile P4.1, T2). The pairing key itself is never here: the config
	// holds the NAME of the OS keychain item the key lives at, which is not a
	// secret and may be printed (M5, D113b — "the settings hold the item's
	// name only").
	Mailbox *Mailbox `yaml:"mailbox,omitempty"`
	// CredentialsService is the installation's credentials service
	// (docs/credentials-service, D146/D147), or nil while the installation has
	// not opted into one — which is every installation until an owner runs
	// `fugaro setup --provision-credentials`. It is what `fugaro run` reads to
	// put the service's URL on the job's environment, which is the one fact the
	// runner's cascade upstream and the keyless GitHub path are built on
	// (internal/runner/gateway.go's credentialsServiceURLEnv,
	// internal/gitprov/providers). An installation without the block launches
	// byte for byte the job it launched before this field existed.
	//
	// It is written from the INSTALLATION's own record — the installation
	// root's outputs, which the credentials stage fills and the module's
	// output carries — and never hand-set: the URL is Cloud Run's own, carries
	// the project NUMBER, and cannot be derived offline. The init run that
	// turns the service on writes it (init_credentials.go's stage, through
	// initRun.writeConfig), and so does every installation run after it
	// (writeConfig reads outs.CredentialsService), so an installation that
	// opted in on an older Fugaro gets the block the next time `fugaro update`
	// converges it.
	//
	// It is PUBLISHED (Shared keeps it, MergeShared keeps the published one
	// when a machine's own config has none), for the reason batch: is: it is
	// the installation's fact and not one machine's, and the machine that runs
	// a launch is often not the machine that opted in. A local-only key would
	// be the ledger item of PR #903: a teammate's config, written by `fugaro
	// setup` or carried by `fugaro update --repo`, never learns it, and their launches
	// would go out without the variable while the installation has the service
	// on.
	CredentialsService *CredentialsService `yaml:"credentials_service,omitempty"`
	// SecretRotationDays is how often the operator means the installation's
	// stored credentials to be rotated, in days (plan B1.9, D503c): doctor's
	// secret-rotation-age check warns once a container's newest enabled
	// version is older than this. Zero, or absent, is no period at all —
	// doctor then says nothing, because the operator has not said how often
	// these are meant to change, and no Secret Manager call is made on their
	// behalf.
	//
	// Operator-only, like update_check: it is one person's reminder about
	// credentials only they can rotate, and a launcher writing it into the
	// runs bucket would be choosing when everyone else is told their
	// installation is overdue.
	SecretRotationDays int `yaml:"secret_rotation_days,omitempty"`
	// SecretExpires is the expiry date the operator recorded per secret name
	// (plan B1.9, D503c), as YYYY-MM-DD (SecretExpiryLayout): doctor warns
	// when a named secret's date is near or past. It exists because nothing
	// Fugaro can read tells it when a stored credential expires — Bitbucket
	// shows an access token's expiry in its UI and its API does not expose it
	// (U3, F19) — so the operator's own record is the only source there is,
	// and the key is the secret's logical name (the label a container
	// carries), not a container ID.
	//
	// Operator-only, for SecretRotationDays' reason.
	SecretExpires map[string]string `yaml:"secret_expires,omitempty"`
	// UpdateCheck turns off the daily check for a newer fugaro release and
	// its one-line stderr nag (FUGARO_NO_UPDATE_CHECK=1 does the same;
	// `fugaro doctor`'s own cli-version check ignores both); nil or true
	// leaves it on. Operator-only, like claude_accounts: never published to
	// the runs bucket (Shared).
	UpdateCheck *bool  `yaml:"update_check,omitempty"`
	User        string `yaml:"user,omitempty"`
	// CommitName and CommitEmail are the name and address a run's commits are
	// authored by when the repository opted in (git.author: launcher, D604,
	// D606): the launcher's own identity, which `fugaro run` reads from here
	// and writes into the task (internal/task's Spec.Author, A2a), and which
	// A3's stamp environment then writes into the agent's git environment.
	//
	// CommitEmail is deliberately not free-form (D649): the launcher accepts
	// it only when it is the launcher's own recorded identity or a forge
	// noreply address, so one person cannot write a colleague's address into a
	// history the forge links to that colleague's account. CommitName is the
	// display half of the same claim and has no such rule beyond the shape a
	// commit's own header holds (task.AuthorProblem).
	//
	// Neither is published (Shared blanks both, like user:): they are one
	// machine's person, not the installation's fact, and a launcher who could
	// write them into the runs bucket could choose whose name every teammate's
	// commits carry.
	CommitName  string    `yaml:"commit_name,omitempty"`
	CommitEmail string    `yaml:"commit_email,omitempty"`
	MaxParallel int       `yaml:"max_parallel"`
	Endpoints   Endpoints `yaml:"endpoints,omitempty"`
	// BaseBranch is the project's default base branch: the one repositories
	// of this installation run against when neither their own committed
	// fugaro.yaml nor their repos.<r>.base_branch entry names one (the
	// precedence is config.EffectiveBaseBranch). It is the same word a repo's
	// entry uses, one level up — a project that uses master everywhere sets
	// it once here, and a repository that differs overrides it in its own
	// file. The owner's, and the only one of the three sites that is
	// published: `fugaro project policy publish` copies it into the policy
	// node, which is where the runner reads it, while the value itself never
	// travels in the shared config a launcher can write (Shared drops it,
	// like each repo's).
	BaseBranch string `yaml:"base_branch,omitempty"`
	// ReviewFloor is the owner's review-level floor (D722, G1): per rigor
	// mode, the levels a run of that mode must run, whatever a lower layer
	// asks for. It travels the way BaseBranch does, and the reason is the
	// same one: `fugaro project policy publish` copies it into the policy
	// node, which only the owner writes and the runner fails closed on, and
	// the value itself never enters the shared config a launcher can write
	// (Shared drops it, and the publisher refuses the key). A floor a
	// launcher could write — in the published shared config, in the project
	// layer or in the task (docs/design/bucket-iam.md §10 L1, L2) — would be
	// that launcher deciding how much review it gets, which is the one thing
	// the floor exists to prevent.
	//
	// A mode that is not one of rigor's, a level outside l1/l2/l3, and a
	// repeated level are refused at load (validate), never dropped: a
	// misspelt floor is a run that reviews less than the owner asked for.
	//
	// A publish keeps the node's floor when this file sets none (see
	// internal/cli's buildProjectPolicy): a floor is the one value a publish
	// must not lower by accident, so silence about it is not a request to
	// remove it. Taking a published floor away on purpose is therefore a
	// written empty object — `review_floor: {}` — and not an absent key. The
	// two spellings are told apart here (a written empty object is a non-nil
	// empty map, an absent key a nil one) and mean the same floor, none.
	ReviewFloor map[string][]string `yaml:"review_floor,omitempty"`
	Repos       map[string]Repo     `yaml:"repos"`
	// notes are warnings added after parsing (AddWarning), such as a
	// published shared config's dropped legacy base_images entries.
	notes []string
}

// StageChoice is one stage's own choice of Claude account, the entry under
// ClaudeAccountStages: the stage's name is the key of the block and the
// account it uses is the value inside it.
type StageChoice struct {
	ClaudeAccount string `yaml:"claude_account,omitempty"`
}

// Mailbox is the mailbox block: what the mailbox consumer needs from the
// operator's own machine, one entry per pairing.
type Mailbox struct {
	// KeychainItem names the OS keychain item each pairing's MAC key lives at
	// (M5, D113b, decision 9): the key is one per pairing, so the item is one
	// per pairing, and this map is keyed by the uid that names the pairing.
	//
	// The key is the uid `fugaro pair` wrote for the phone — `m~…`, which is
	// what `pair ls` prints and `pair revoke` names — so that a revoke deletes
	// exactly one phone's item, and a pairing session's `p~…` is accepted too,
	// because a session is what a partially written config would hold. Both
	// halves are the shape a uid has (§2 (e)), and not the same one: a device's
	// is 16 random bytes in unpadded base64url behind `m~` and a session's is 32
	// behind `p~`, which is why pairUIDRE carries a length per prefix. A key
	// that is neither is refused rather than kept as a name nothing will ever
	// look up.
	//
	// The value is the item's name: configuration, and not a secret — it may
	// be printed, and the diagnostic a missing key produces names it — but its
	// shape is fixed, because it becomes an argv element of a keychain program:
	// at most 64 characters of A-Z, a-z, 0-9, '.', '_' and '-', carrying this
	// project's name, so a config file cannot name an arbitrary keychain query
	// and two projects on one machine cannot be made to read each other's key.
	// The shape is keychain.ParseItem's, not a second copy of the rule:
	// validate refuses a name that package refuses, with that package's
	// sentence.
	KeychainItem map[string]string `yaml:"keychain_item,omitempty"`
}

// pairUIDRE is the shape of the uid a mailbox entry may be keyed by, and it is
// two shapes, because the installation mints two uids and they are two lengths.
//
// **The key `fugaro pair` asks the operator to record is the device's `m~` uid**
// — the command prints `mailbox.keychain_item[<device uid>]`, `pair ls` lists
// that same uid and `pair revoke` names it, and decision 9 derives the item's
// name from the 22-character body behind `m~`. So the `m~` branch, 22 unpadded
// base64url characters standing for the 16 random bytes of token's
// deviceUIDBytes, is the one every entry a pairing looks up is keyed by.
//
// The `p~` branch is the pairing session's uid — PairingPrefix and the 43
// characters 32 random bytes spell (pairing.go's draw, §2 (e)) — and it is
// admitted **while nothing ever looks such an entry up**: the session's node is
// deleted at the end of the same `fugaro pair`, its Auth user is the sweep's,
// and the durable identity is the device's `m~`. It is admitted because a config
// written while a session is open holds one (Mailbox's own comment says so),
// and the cost is stated rather than hidden: a `p~` key is a line that reads no
// key, not one that reads the wrong key — the item's name is still
// keychain.ParseItem's and the pairing it would have named no longer exists.
//
// It is this file's copy of the shapes and not the token package's function,
// because the token package mints uids and this one only recognises them, and
// its byte counts are unexported; TestMailboxKeyedByARealPairingUIDLoads mints
// one of each through that package and loads them here, which is what keeps the
// two from drifting. A key that is neither shape is refused, which is the only
// way an operator learns of a typo.
var pairUIDRE = regexp.MustCompile(`^(?:p~[A-Za-z0-9_-]{43}|m~[A-Za-z0-9_-]{22})$`)

// CredentialsService is the credentials service block: the installation's own
// Cloud Run service, as the launcher needs it (docs/credentials-service,
// D146/D147). One field, and one is all a launcher reads.
//
// The block's PRESENCE is the opt-in — `fugaro run` puts the URL on a job only
// when it is there — so an absent block and a block with an empty url are the
// same thing to every reader and the loader refuses the second rather than
// reading it as "off".
type CredentialsService struct {
	// URL is the service's own URL, which is also the audience its callers'
	// Google ID tokens are minted for (C1.14): the runner uses one value for
	// both, the endpoint and the audience, because they are the same string
	// (internal/runner/gateway.go's serviceUpstream). It is the installation
	// root's fact: credentials.tf's output reads the service resource's own
	// uri, and the init stage writes it into the tfvars on the apply after the
	// one that created the service (Cloud Run assigns the URL and nothing
	// inside the resource may read its own uri).
	//
	// Held to the same shape as the service's own audience check
	// (internal/cli's credentialsServiceAudience, whose rule this mirrors):
	// an https URL with a host, no path, no query, no fragment and no user
	// info. Nothing weaker is admitted, because a value that is not exactly
	// that is a caller whose ID token is minted for an audience no revision
	// answers to.
	URL string `yaml:"url"`
}

// validCredentialsServiceURL holds credentials_service.url to the shape the
// credentials service's own configuration holds its audience to
// (internal/cli's credentialsServiceAudience): https, a host, and nothing
// else. The rule is stated twice on purpose — the service refuses to serve
// with an audience that is not its own URL, and the launcher refuses to put
// anything else on a job — and the two must agree, because the value travels
// from one to the other: a URL the launcher admits and the service does not is
// a run whose every call is answered 401, and one the service admits and the
// launcher does not is a service nothing can call.
//
// It is deliberately not the whole audience check: that one also refuses a
// value that is not this revision's own URL, which only the deployment knows.
// Nothing here can check the host, and nothing pretends to: the URL carries
// Cloud Run's own project NUMBER, which no configuration in this tree holds.
func validCredentialsServiceURL(raw string) error {
	if raw == "" {
		return errors.New("the credentials service block carries no url: remove the block for an installation without the service, or write the service's own https URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "https" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("it must be the https URL Cloud Run serves the service on, with no path, query or fragment (a caller's Google ID token is minted for exactly that audience, so anything else is refused by IAM)")
	}
	return nil
}

// Watch is the watch block: how fugaro watch flags a fast burn.
type Watch struct {
	// BurnAlertUSDPerHour is the spend rate above which watch marks a
	// project or repository FAST; nil means the effective daily cap spread
	// over 8 hours (none when there is no cap), 0 marks any spend.
	BurnAlertUSDPerHour *float64 `yaml:"burn_alert_usd_per_hour,omitempty"`
}

// BurnAlert is watch.burn_alert_usd_per_hour in micro-dollars, nil when unset.
func (c *Config) BurnAlert() *pricing.Micros {
	if c.Watch == nil || c.Watch.BurnAlertUSDPerHour == nil {
		return nil
	}
	m, err := pricing.FromUSD(*c.Watch.BurnAlertUSDPerHour)
	if err != nil {
		return nil // validate refused it
	}
	return &m
}

// Budget modes.
const (
	BudgetOff     = policy.ModeOff
	BudgetObserve = policy.ModeObserve
	BudgetEnforce = policy.ModeEnforce
)

// Budget is the project's per-run model spend guard.
type Budget struct {
	Mode      string  `yaml:"mode"`        // off | observe | enforce; "" is off
	PerRunUSD float64 `yaml:"per_run_usd"` // enforce: 0 < x <= 100000; observe: >= 0
	// MaxRunTokens is the ceiling on a run's total tokens; 0 is none. A
	// repository's fugaro.yaml may only lower it.
	MaxRunTokens int64 `yaml:"max_run_tokens,omitempty"`
	// AllowedModels, when set, are the only models a run may use. Explicit
	// model IDs; an empty list is refused (it would forbid every model).
	AllowedModels []string `yaml:"allowed_models,omitempty"`
	// RTDBURL is the project's Firebase Realtime Database (the budget
	// backend of M9b), which fugaro budget reads and writes. Set by fugaro
	// update --firebase-project; empty means the project has none.
	RTDBURL string `yaml:"rtdb_url,omitempty"`
	// FirebaseProject is the project's own Firebase project (the FP) and
	// FirebaseAPIKey its restricted web API key (not a secret: it may call
	// only Identity Toolkit and Secure Token). TokenSigner is the signer
	// account the launcher mints run tokens as. All four are written by
	// fugaro update --firebase-project from the Firebase root's outputs.
	FirebaseProject string `yaml:"firebase_project,omitempty"`
	FirebaseAPIKey  string `yaml:"firebase_api_key,omitempty"`
	TokenSigner     string `yaml:"token_signer,omitempty"`
	// Grace is how long a run keeps going when the backend is unreachable
	// before it halts (D14); 0 is the default, 3m, and it may only shorten
	// it (budget.MinGrace to budget.MaxGrace, the bounds the job enforces).
	Grace time.Duration `yaml:"unreachable_grace,omitempty"`
}

// ModelPrice is one model's prices, in US dollars per million tokens. A
// field left out takes the list default (cache_write_5m 1.25, cache_write_1h
// 2, cache_read 0.1, web_search_per_1k 10), never 0; input_per_m and
// output_per_m are required.
type ModelPrice struct {
	InputPerM      *float64 `yaml:"input_per_m"`
	OutputPerM     *float64 `yaml:"output_per_m"`
	CacheWrite5m   *float64 `yaml:"cache_write_5m,omitempty"`
	CacheWrite1h   *float64 `yaml:"cache_write_1h,omitempty"`
	CacheRead      *float64 `yaml:"cache_read,omitempty"`
	WebSearchPer1k *float64 `yaml:"web_search_per_1k,omitempty"`
	// LongContext is the price of a call whose input passes a threshold,
	// for a model with a long-context tier; an override without it
	// replaces the model's tier with none.
	LongContext *LongContext `yaml:"long_context,omitempty"`
}

// LongContext is a long-context price tier.
type LongContext struct {
	AboveInputTokens int64    `yaml:"above_input_tokens"`
	InputPerM        *float64 `yaml:"input_per_m"`
	OutputPerM       *float64 `yaml:"output_per_m"`
}

// UpdateCheckEnabled reports whether the daily update check and its stderr
// nag run for this config: true unless update_check: false.
func (c *Config) UpdateCheckEnabled() bool {
	if c.UpdateCheck == nil {
		return config.DefaultUpdateCheck
	}
	return *c.UpdateCheck
}

// BudgetMode is the budget's mode: "off" when there is no budget block or
// its mode is empty.
func (c *Config) BudgetMode() string {
	if c.Budget == nil || c.Budget.Mode == "" {
		return BudgetOff
	}
	return c.Budget.Mode
}

// Overrides are the model prices as the pricing package takes them, with
// the list defaults applied to the fields left out.
func (c *Config) Overrides() (pricing.Overrides, error) {
	var errs []error
	o := pricing.Overrides{}
	for _, id := range slices.Sorted(maps.Keys(c.ModelPrices)) {
		mp := c.ModelPrices[id]
		if pricing.IsAlias(id) {
			errs = append(errs, fmt.Errorf("model_prices: %q is an alias: key prices by a model ID (such as claude-sonnet-5-5)", id))
			continue
		}
		if mp.InputPerM == nil || mp.OutputPerM == nil {
			errs = append(errs, fmt.Errorf("model_prices.%s: input_per_m and output_per_m are required", id))
			continue
		}
		or := func(p *float64, def float64) float64 {
			if p == nil {
				return def
			}
			return *p
		}
		r := pricing.Rates{
			InputPerM: *mp.InputPerM, OutputPerM: *mp.OutputPerM,
			CacheWrite5m:   or(mp.CacheWrite5m, pricing.DefaultCacheWrite5m),
			CacheWrite1h:   or(mp.CacheWrite1h, pricing.DefaultCacheWrite1h),
			CacheRead:      or(mp.CacheRead, pricing.DefaultCacheRead),
			WebSearchPer1k: or(mp.WebSearchPer1k, pricing.DefaultWebSearchPer1k),
		}
		if t := mp.LongContext; t != nil {
			if t.InputPerM == nil || t.OutputPerM == nil {
				errs = append(errs, fmt.Errorf("model_prices.%s.long_context: input_per_m and output_per_m are required", id))
				continue
			}
			r.LongContext = &pricing.Tier{AboveInputTokens: t.AboveInputTokens, InputPerM: *t.InputPerM, OutputPerM: *t.OutputPerM}
		}
		if err := r.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("model_prices.%s: %w", id, err))
			continue
		}
		o[id] = r
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	// Two keys naming one model, and anything else the table refuses.
	if _, err := pricing.Embedded().With(o); err != nil {
		return nil, fmt.Errorf("model_prices: %w", err)
	}
	return o, nil
}

// validateBudget checks the budget block and the model prices.
func (c *Config) validateBudget(bad func(string, ...any)) {
	if b := c.Budget; b != nil {
		if b.Mode != "" && !policy.ValidMode(b.Mode) {
			bad("budget.mode %q must be off, observe or enforce", b.Mode)
		}
		if b.MaxRunTokens < 0 {
			bad("budget.max_run_tokens %d: it must not be negative (0 is none)", b.MaxRunTokens)
		}
		if b.RTDBURL != "" {
			if err := rtdb.ValidateURL(b.RTDBURL, c.Endpoints.NoAuth); err != nil {
				bad("budget.rtdb_url: %v", err)
			}
		}
		if b.FirebaseProject != "" && !projectRE.MatchString(b.FirebaseProject) {
			bad("budget.firebase_project %q is not a GCP project ID", b.FirebaseProject)
		}
		if b.FirebaseAPIKey != "" && !apiKeyRE.MatchString(b.FirebaseAPIKey) {
			bad("budget.firebase_api_key is not a Google API key (20 to 128 letters, digits, - and _)")
		}
		if b.TokenSigner != "" && !signerRE.MatchString(b.TokenSigner) {
			bad("budget.token_signer %q is not a service account email (<id>@<project>.iam.gserviceaccount.com)", b.TokenSigner)
		}
		if b.Grace != 0 && (b.Grace < budget.MinGrace || b.Grace > budget.MaxGrace) {
			bad("budget.unreachable_grace %v: it must be from %v to %v (leave it out for 3m)", b.Grace, budget.MinGrace, budget.MaxGrace)
		}
		if b.AllowedModels != nil && len(b.AllowedModels) == 0 {
			bad("budget.allowed_models: it must list at least one model (an empty list, or one of only nulls, would forbid every model); leave it out to keep the allow-list the ceiling already has")
		}
		for i, m := range b.AllowedModels {
			if msg := config.CheckModelID(m); msg != "" {
				bad("budget.allowed_models[%d]: %s", i, msg)
			}
		}
		if i, ok := config.DuplicateModel(b.AllowedModels); ok {
			bad("budget.allowed_models[%d]: %s is listed twice; list each model once", i, b.AllowedModels[i])
		}
		if m, err := pricing.FromUSD(b.PerRunUSD); err != nil {
			bad("budget.per_run_usd %v: it must be a number from 0 to %d US dollars", b.PerRunUSD, pricing.MaxUSD)
		} else if b.PerRunUSD > 0 && m < 1 {
			bad("budget.per_run_usd %v: it rounds to nothing; the smallest cap is $0.000001", b.PerRunUSD)
		} else if b.Mode == BudgetEnforce && b.PerRunUSD <= 0 {
			bad("budget.per_run_usd must be more than 0 with budget.mode enforce (it is the per-run cap)")
		}
	}
	if _, err := c.Overrides(); err != nil {
		bad("%v", err)
	}
}

// Terraform is what fugaro update needs to plan the installation again: its
// state bucket, and the inputs that exist only as flags.
type Terraform struct {
	StateBucket string   `yaml:"state_bucket,omitempty"`
	AlertEmail  string   `yaml:"alert_email,omitempty"`
	Launchers   []string `yaml:"launchers,omitempty"` // IAM members, such as user:a@example.com
	Operators   []string `yaml:"operators,omitempty"`
	// BudgetAdmins are further budget admins on the Firebase project, IAM
	// members (user:, group: or serviceAccount:); the GCP project's owners
	// and editors are admins too, read when fugaro update --firebase-project runs.
	BudgetAdmins []string `yaml:"budget_admins,omitempty"`
	// RegistryCleanup (dry-run, on or off), NoLogIsolation and BudgetBackend
	// are the installation's switches that exist as flags: adopt mode copies
	// them from the installation's outputs, so a plan from a machine that did
	// not apply it starts from what the installation is. Flags override them.
	RegistryCleanup string `yaml:"registry_cleanup,omitempty"`
	NoLogIsolation  bool   `yaml:"no_log_isolation,omitempty"`
	BudgetBackend   bool   `yaml:"budget_backend,omitempty"`
	// Coordinator is the owner's opt-in to the coordinator's aggregation job
	// (docs/coordinator, P5): its account, that account's grants and the
	// scheduled job, which nothing creates by default. fugaro update
	// --firebase-project --coordinator records it once that run has applied
	// the account, and adopt mode copies it from the installation's outputs,
	// so every later run keeps what the owner turned on rather than planning
	// its removal.
	Coordinator bool `yaml:"coordinator,omitempty"`
}

// Price is a region's compute price per second, in US dollars.
type Price struct {
	VCPUSecondUSD float64 `yaml:"vcpu_second_usd"`
	GiBSecondUSD  float64 `yaml:"gib_second_usd"`
}

// PriceSource is the Source of overridden prices.
const PriceSource = "local override"

// MaxPriceUSD bounds a per-second price: list prices are around 1e-5, so
// anything above 1e-3 is a unit mistake (per hour, per month).
const MaxPriceUSD = 0.001

// SecretExpiryLayout is how an operator writes a date in secret_expires. The
// date is a day rather than an instant on purpose: it is what the provider's
// own page for a token says, and a time of day would be an invention of this
// file's that the check would then warn about.
const SecretExpiryLayout = "2006-01-02"

// Batch is the Cloud Batch backend's installation configuration: the facts a
// run's VM and job are built and placed from (design
// docs/design/compute-backend.md §4.4, plan 2026-10-19-compute-backend.md
// Task 8 step 2). It is read in one place — internal/cli/cloud.go's Batch
// opener, which builds internal/backend/batch.Options from it — and
// `backend: cloud-batch` is refused by validate below until the enable task,
// so this block selects nothing today.
//
// Most fields are what the compute root's apply already produces and
// internal/infra/compute.go's ComputeOutputs already decodes (the machine
// type, the preloaded boot image family, the network, the subnetwork, the run
// account, and the socket gid Task 6 reads off the VM it builds the image on).
// Image and TaskTimeout are the two batch.New requires that no output carries
// yet: the image a run's job runs is a per-workflow fact
// (backend/batch.Options.Image's own comment), and on Batch there is no
// deployed job to read a timeout back from, so both are recorded here as the
// installation's answers until the task that resolves them per workflow does.
//
// What is checked here is the *spelling* of each field — this file is where an
// operator writes it, so a typo must fail at parse rather than at the first
// launch. The rules that need more than one field, or a workflow to be
// involved (the subnetwork being in the backend's region, a Docker workflow on
// an installation with no gid recorded, the default Compute Engine account),
// stay where they are enforced: internal/backend/batch's checkNetwork,
// checkDocker and checkServiceAccount, which the opener runs on the same
// values. Both sides fail closed.
type Batch struct {
	// Image is the container image a run's job runs, as the workflow's
	// derived image would be named (a registry path with a tag or a digest).
	// batch.New refuses an empty one, so a batch block without it is refused
	// here too.
	Image string `yaml:"image,omitempty"`
	// MachineType is the Compute Engine machine type a run's VM is. Empty
	// means the backend's own default (e2-standard-8, the smallest type whose
	// task share carries the config's default 4 CPU/8Gi); it must be one the
	// price table carries, which the backend checks.
	MachineType string `yaml:"machine_type,omitempty"`
	// BootImage is the preloaded boot image a run's VM boots, as the family
	// resource name the preload stage publishes: the runs' bootDisk.image
	// names the family, not one of its generations.
	BootImage string `yaml:"boot_image,omitempty"`
	// Network and Subnetwork are the interface a run's VMs are placed in, as
	// full resource names. Both are required and both are sent, because a
	// run's VMs carry no external IP (design §1.7, C7).
	Network    string `yaml:"network,omitempty"`
	Subnetwork string `yaml:"subnetwork,omitempty"`
	// ServiceAccount is the email of the dedicated service account a job's
	// VMs run as (design §3.2). It must not be the default Compute Engine
	// account, which is Editor on the project; the backend refuses that one.
	ServiceAccount string `yaml:"service_account,omitempty"`
	// Secrets maps an environment variable name to a Secret Manager secret's
	// resource name — a NAME, never a value (design §1.6, C10). They are sent
	// as the job's secretVariables, so the values never enter the job
	// definition. A value that is not a secret's resource name is refused
	// here, which is what keeps a pasted value from reaching a job.
	Secrets map[string]string `yaml:"secrets,omitempty"`
	// SocketGID is the gid of the Docker daemon's socket on the boot image,
	// read off the VM Task 6 builds it on rather than assumed. Zero means
	// this installation has no socket recorded, and the backend then refuses
	// a run that asked for Docker rather than launching it with a socket it
	// cannot open.
	SocketGID int `yaml:"socket_gid,omitempty"`
	// TaskTimeout is the task timeout a run's job gets when the launch names
	// none (a launch that carries its own total, from --total-timeout, always
	// wins: it is the run's own bound).
	//
	// Left out, the default is backend.MaxRunTime, the longest any run of
	// this installation may legitimately be active: the failure directions of
	// this field are not symmetric, and a bound shorter than a workflow's
	// would truncate a run that was doing nothing wrong, while a longer one
	// costs a stuck job machine time the reaper already bounds. So the
	// default is the ceiling every run is already held to, and an operator
	// who wants the jobs bounded more tightly writes it here. The workflow's
	// own timeouts.total is where this value comes from once the enable task
	// reads it per workflow (internal/cli's batchTimeouts).
	//
	// It is also REFUSED above that ceiling (validateBatch): the reaper's
	// deadline is max(the longest configured timeout, backend.MaxRunTime), and
	// the reaper that runs from its own Cloud Run job's environment has no
	// project config to read the first of those from, so it derives the base
	// from the ceiling alone (internal/cli/reaper.go). A configured timeout
	// above the ceiling makes the two disagree, in the one direction that ends
	// a live run: the sweeper's bound would be shorter than the config's, and
	// a job that is legitimately running would read as stuck.
	TaskTimeout config.Duration `yaml:"task_timeout,omitempty"`
}

// BatchTaskTimeout is the task timeout a Batch job gets when the launch names
// none: the config's batch.task_timeout, or backend.MaxRunTime when this file
// sets none (see Batch.TaskTimeout's comment for why the default is the
// ceiling rather than a small number).
func (c *Config) BatchTaskTimeout() time.Duration {
	if c.Batch != nil && c.Batch.TaskTimeout.Set && c.Batch.TaskTimeout.Duration > 0 {
		return c.Batch.TaskTimeout.Duration
	}
	return backend.MaxRunTime
}

// validateBatch checks the batch block's field spellings (see Batch). It is
// called whenever the block is written, whether or not this installation
// selects the backend: a block that is not valid is not a block a later task
// can turn on by flipping one line.
func validateBatch(bad func(string, ...any), c *Config) {
	b := c.Batch
	if b == nil {
		return
	}
	switch {
	case b.Image == "":
		bad("batch.image: the Cloud Batch backend needs the image a run's job runs; set it to the installation's registry image")
	case !batchImageRE.MatchString(b.Image):
		bad("batch.image %q is not a container image reference (a lowercase registry path with a tag or a digest, such as <region>-docker.pkg.dev/<project>/fugaro/fugaro:1)", b.Image)
	}
	if b.MachineType != "" && !vmMachineTypeRE.MatchString(b.MachineType) {
		bad("batch.machine_type %q is not a Compute Engine machine type such as e2-standard-4", b.MachineType)
	}
	if b.BootImage != "" && !bootImageRE.MatchString(b.BootImage) {
		bad("batch.boot_image %q is not the preloaded image family a run's VM boots (projects/<project>/global/images/family/<name>)", b.BootImage)
	}
	if b.Network != "" && !batchNetworkRE.MatchString(b.Network) {
		bad("batch.network %q is not a network's full resource name (projects/<p>/global/networks/<n>): Batch refuses a job with no external IP unless the network is named explicitly", b.Network)
	}
	if b.Subnetwork != "" && !batchSubnetRE.MatchString(b.Subnetwork) {
		bad("batch.subnetwork %q is not a subnetwork's full resource name (projects/<p>/regions/<region>/subnetworks/<s>): Batch refuses a job with no external IP unless the subnetwork is named explicitly", b.Subnetwork)
	}
	if b.ServiceAccount != "" && !emailRE.MatchString(b.ServiceAccount) {
		bad("batch.service_account %q is not a service account email address", b.ServiceAccount)
	}
	if b.SocketGID < 0 {
		bad("batch.socket_gid %d is negative: it is a gid read off the boot image's Docker socket (stat -c %%g /var/run/docker.sock)", b.SocketGID)
	}
	if b.TaskTimeout.Set && b.TaskTimeout.Duration <= 0 {
		// Unlike a cap, a written 0 is not a spelling of the default: it is
		// either a mistake or a claim that a job may run for no time at all,
		// and the key can simply be left out. backend.MaxRunTime is what an
		// omitted key means.
		bad("batch.task_timeout %v: it must be more than 0, or left out for the default (%v)", b.TaskTimeout.Duration, backend.MaxRunTime)
	}
	if b.TaskTimeout.Set && b.TaskTimeout.Duration > backend.MaxRunTime {
		// The ceiling, and this refusal is the reaper's requirement rather
		// than a cap for its own sake: no run may be given more than
		// backend.MaxRunTime (--total-timeout is capped there), so a job sized
		// past it is headroom no run can use -- while the reaper that runs
		// from its own Cloud Run job's environment has no config to read this
		// number from and derives its base from the ceiling
		// (internal/cli/reaper.go). The two sources would disagree about when
		// a live run has ended, and that disagreement deletes a running job.
		bad("batch.task_timeout %v is longer than %v, the longest a run may be given (--total-timeout is capped there): a job sized past it is headroom no run can use, and the reaper of an installation whose config it cannot read derives its own bound from that ceiling, so the two would disagree about when a live run has ended", b.TaskTimeout.Duration, backend.MaxRunTime)
	}
	// Each entry is named by its position in the sorted map, and never by its
	// key: neither side of the map is printed, because **either** side can hold
	// a secret's value. The value slot is the one a value is usually written
	// into, but the key slot is no safer -- an environment variable name and an
	// access key id can be spelled identically (`AKIAIOSFODNN7EXAMPLE` is
	// both), so a key that fails batchEnvNameRE may be the very value this
	// section must never carry, and a refusal that quoted it would put it on
	// stderr, in a log and in an issue report. It is the same reasoning and the
	// same shape as internal/backend/batch/batch.go's checkSecrets, which names
	// its entry by position for this reason (design §1.6, C10).
	keys := slices.Sorted(maps.Keys(b.Secrets))
	for i, k := range keys {
		if !batchEnvNameRE.MatchString(k) {
			bad("batch.secrets: entry %d of %d has a key that is not an environment variable name (letters, digits and _, not starting with a digit). The refused key is not repeated here: a secret's value can be spelled like one, so a value written where the name belongs stops here, and a refusal is not the place to print it", i+1, len(keys))
		}
		if !batchSecretNameRE.MatchString(b.Secrets[k]) {
			bad("batch.secrets: entry %d of %d is not a Secret Manager secret's resource name (projects/<p>/secrets/<name>), and is named by its position and not by its key for the same reason: it must be a name, never the secret itself, because the job definition is readable by anyone with batch.jobs.get", i+1, len(keys))
		}
	}
}

// Build configures Cloud Build submissions.
type Build struct {
	// ServiceAccount is deprecated: each repository's builds run as its
	// own build account. It is still accepted, with a warning.
	ServiceAccount string `yaml:"service_account,omitempty"`
	MachineType    string `yaml:"machine_type"`
	Region         string `yaml:"region,omitempty"`
}

// Endpoints override API roots, for fakes and emulators.
type Endpoints struct {
	Run           string `yaml:"run,omitempty"`
	Logging       string `yaml:"logging,omitempty"`
	SecretManager string `yaml:"secret_manager,omitempty"`
	CloudBuild    string `yaml:"cloud_build,omitempty"`
	// Batch is the Cloud Batch API root, read by the Batch backend the
	// compute-backend plan's Task 8 wires (internal/cli/cloud.go's
	// cloudOpeners). Like the others it is for fakes and emulators: an
	// installation sets none of them, and an empty value is the real
	// endpoint. Only the backend's own API is here -- the reaper's Compute
	// Engine root is not a CLI client's endpoint, it is the reaper job's
	// (internal/backend/batch/reap.go's Endpoints).
	Batch string `yaml:"batch,omitempty"`
	// Compute is the Compute Engine API root, read by `fugaro doctor`'s
	// checks for an installation that carries a batch block (the preload
	// image family, and the network and subnetwork a run's VMs are placed
	// in: design compute-backend.md §4.4). Empty is the real endpoint, as
	// for every other field here. It is not the reaper's root: the reaper
	// runs as its own job with its own env (internal/backend/batch's
	// Endpoints), and this one is a client the operator's own doctor run
	// reads with.
	Compute string `yaml:"compute,omitempty"`
	// The APIs only fugaro update calls: bucket and IAM policy reads and
	// writes, service accounts, registries, the project's number, and the
	// enable of Cloud Resource Manager.
	Storage          string `yaml:"storage,omitempty"`
	IAM              string `yaml:"iam,omitempty"`
	ArtifactRegistry string `yaml:"artifact_registry,omitempty"`
	ResourceManager  string `yaml:"resource_manager,omitempty"`
	CloudScheduler   string `yaml:"cloud_scheduler,omitempty"`
	ServiceUsage     string `yaml:"service_usage,omitempty"`
	// CloudBilling is read by fugaro update --firebase-project to check that the
	// Firebase project has billing.
	CloudBilling string `yaml:"cloud_billing,omitempty"`
	// FirebaseDatabase lists the Firebase project's databases (fugaro update
	// --firebase-project).
	FirebaseDatabase string `yaml:"firebase_database,omitempty"`
	// APIKeys reads the Firebase project's API keys (fugaro update
	// --firebase-project).
	APIKeys string `yaml:"api_keys,omitempty"`
	// FirebaseManagement is where fugaro setup --create-project adds Firebase
	// to a project it creates (Firebase Management v1beta1).
	FirebaseManagement string `yaml:"firebase_management,omitempty"`
	// IdentityToolkit is where fugaro update --firebase-project ensures Identity
	// Platform is initialized in the Firebase project (Identity Toolkit's REST
	// API).
	IdentityToolkit string `yaml:"identity_toolkit,omitempty"`
	// Firestore and FirebaseRules are where fugaro update --firebase-project
	// ensures the Firebase project's Firestore database exists and deploys its
	// deny-all security rules (the Firestore and Firebase Rules REST APIs).
	Firestore     string `yaml:"firestore,omitempty"`
	FirebaseRules string `yaml:"firebase_rules,omitempty"`
	// IAMCredentials is where the launcher signs a run's budget token (signJwt).
	IAMCredentials string `yaml:"iam_credentials,omitempty"`
	// CloudAsset is where fugaro managed apply makes the whole-project
	// inventory read (Cloud Asset's SearchAllResources, Task 4.1a/4.5).
	CloudAsset string `yaml:"cloud_asset,omitempty"`
	NoAuth     bool   `yaml:"no_auth,omitempty"` // send no credentials (fakes only)
}

// Repo is an onboarded repository.
type Repo struct {
	// Provider is the git provider kind (config.Providers: github or
	// bitbucket), part of the repository's storage slug. Empty means the
	// checkout's fugaro.yaml git.provider decides; when both are set they
	// must agree. repos is keyed by owner/name, so one local config holds
	// one provider per owner/name.
	Provider   string   `yaml:"provider,omitempty"`
	BaseBranch string   `yaml:"base_branch,omitempty"`
	Workflows  []string `yaml:"workflows"`
	// GitHubAppID is the repository's GitHub App, for GitHub
	// repositories. It is not a secret.
	GitHubAppID string `yaml:"github_app_id,omitempty"`
	// Vertex records that one of the repository's workflows authenticates
	// its agent through Vertex AI (agent.auth: vertex), so fugaro update
	// enables the Vertex AI API. fugaro update sets it.
	Vertex bool `yaml:"vertex,omitempty"`
}

// UsesVertex reports whether any repository the config records uses
// Vertex AI.
func (c *Config) UsesVertex() bool {
	for _, r := range c.Repos {
		if r.Vertex {
			return true
		}
	}
	return false
}

// ErrMissing means there is no local config yet.
var ErrMissing = errors.New("no local fugaro config")

var (
	projectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	regionRE  = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
	// machineTypeRE is a Cloud Build machine type (E2_HIGHCPU_8, e2-medium).
	machineTypeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	bucketRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)
	repoRE        = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	logViewRE     = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/locations/[a-z0-9-]+/buckets/[a-z0-9_-]+/views/[A-Za-z0-9_-]+$`)
	// registryHostRE captures the project of <region>-docker.pkg.dev/<project>.
	registryHostRE = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-docker\.pkg\.dev/([a-z][a-z0-9-]{4,28}[a-z0-9])$`)
	appIDRE        = regexp.MustCompile(`^[0-9]{1,20}$`)
	memberRE       = regexp.MustCompile(`^(user|group|serviceAccount|domain):[^\s]+$`)
	emailRE        = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	// budgetAdminRE is a member that may hold roles on the Firebase
	// project: no domain: and no wildcard.
	budgetAdminRE = regexp.MustCompile(`^(user|group|serviceAccount):[^\s*]+$`)
	apiKeyRE      = regexp.MustCompile(`^[A-Za-z0-9_-]{20,128}$`)
	// The batch block's own spellings. Each is the shape
	// internal/backend/batch requires of the same value — its
	// networkNameRE, subnetworkNameRE, envNameRE and secretNameRE — spelled
	// again here because this file is where an operator writes them and a
	// typo must fail at parse. Both sides fail closed; a value that passes
	// here and not there is refused by batch.New when the opener opens the
	// backend, which is the authoritative check.
	batchImageRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(:[\w.-]{1,128}|@sha256:[a-f0-9]{64})$`)
	vmMachineTypeRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	bootImageRE       = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/global/images/family/[a-z0-9-]{1,63}$`)
	batchNetworkRE    = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/global/networks/[a-z0-9-]{1,63}$`)
	batchSubnetRE     = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/regions/[a-z][a-z0-9-]{0,62}/subnetworks/[a-z0-9-]{1,63}$`)
	batchEnvNameRE    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	batchSecretNameRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/secrets/([A-Za-z0-9_-]{1,255})(/versions/([A-Za-z0-9_-]{1,255}))?$`)
	signerRE          = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`)
)

// configDir is $XDG_CONFIG_HOME/fugaro, else the platform's own config
// directory (UserDir): %APPDATA%\fugaro on Windows, ~/.config/fugaro
// elsewhere.
func configDir(getenv func(string) string) (string, error) {
	d, err := UserDir(getenv, runtime.GOOS, DirConfig)
	if err != nil {
		return "", fmt.Errorf("%w, so there is no project config directory; pass --config", err)
	}
	return filepath.Join(d, "fugaro"), nil
}

// ProjectsDir is where the project configs live:
// $XDG_CONFIG_HOME/fugaro/projects, else ~/.config/fugaro/projects.
func ProjectsDir(getenv func(string) string) (string, error) {
	d, err := configDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "projects"), nil
}

// ProjectPath is the project config of project name.
func ProjectPath(getenv func(string) string, name string) (string, error) {
	if !config.ProjectNameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", name)
	}
	d, err := ProjectsDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name+".yaml"), nil
}

// LayerFile is the local project layer file of project name: the project's
// own name under the config home's projects/ directory, beside its project
// config (D830). The path is a convention and nothing records it — there is no
// --layer flag and no config key for it — so the file the owner edits and the
// file `fugaro update` publishes are the same one by construction.
//
// The name is `<project>.layer.yaml`, and the dot is the whole of why: a
// project name is a-z, 0-9 and '-' (config.ProjectNameRE), so a file this one
// names can never be read back as a project config. `Projects()` lists every
// projects/<name>.yaml whose <name> is a project name, and a `-layer.yaml` file
// would be listed as a project of its own — a phantom second project for a
// single-project installation, and a real collision for a project actually
// named foo-layer, whose own layer file would then be project `foo`'s
// (docs/rigor-command/reconciliation.md, D830 amendment of 2026-11-01).
func LayerFile(getenv func(string) string, name string) (string, error) {
	if !config.ProjectNameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", name)
	}
	d, err := ProjectsDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name+".layer.yaml"), nil
}

// legacyPath is where the local config lived before project configs; it
// isn't read any more, only mentioned when there is no project config.
func legacyPath(getenv func(string) string) string {
	d, err := configDir(getenv)
	if err != nil {
		return ""
	}
	return filepath.Join(d, "config.yaml")
}

// Projects are the names of the project configs, sorted: every
// projects/<name>.yaml file whose <name> is a project name (backups,
// directories and other files are not project configs; a symlink to a
// regular file is one).
func Projects(getenv func(string) string) ([]string, error) {
	d, err := ProjectsDir(getenv)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || !config.ProjectNameRE.MatchString(name) {
			continue
		}
		// A symlink counts when it leads to a regular file, as loading
		// it would follow it.
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(d, e.Name())); err != nil || !fi.Mode().IsRegular() {
				continue
			}
		} else if !e.Type().IsRegular() {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// LoadProject loads project name's config and its path. The file must
// hold name: <name>: a projects/<name>.yaml naming another project is
// refused rather than loaded as either.
func LoadProject(getenv func(string) string, name string) (*Config, string, error) {
	path, err := ProjectPath(getenv, name)
	if err != nil {
		return nil, "", err
	}
	c, err := Load(path)
	if err != nil {
		return nil, path, err
	}
	if c.Name != name {
		return nil, path, fmt.Errorf("%s holds project %s, not %s: a project config's name: must be its file's name", path, c.Name, name)
	}
	return c, path, nil
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s: run fugaro setup to write it", ErrMissing, path)
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// BaseImage is the base image for base kind kind, "" when the config has
// none.
func (c *Config) BaseImage(kind string) string { return c.BaseImages[kind] }

var baseImageNameRE = regexp.MustCompile(`/fugaro-([a-z0-9-]+)(:[A-Za-z0-9._-]+)?(@sha256:[0-9a-f]{64})?$`)

// BaseImageNameKind is the base kind a ref's repository name says
// (fugaro-<kind>), "" when it names none.
func BaseImageNameKind(ref string) string {
	if m := baseImageNameRE.FindStringSubmatch(ref); m != nil && slices.Contains(config.Bases, m[1]) {
		return m[1]
	}
	return ""
}

// ParseBaseImageFlag reads one fugaro update --base-image value: KIND=IMAGE, or
// a bare IMAGE whose repository name says the kind (fugaro-<kind>, as in
// ghcr.io/dimipaun/fugaro-go:1 or <registry>/fugaro-base/fugaro-web-node:dev-abc).
func ParseBaseImageFlag(v string) (kind, ref string, err error) {
	if k, r, ok := strings.Cut(v, "="); ok {
		kind, ref = k, r
		if !slices.Contains(config.Bases, kind) {
			if slices.Contains(config.LegacyBaseKinds, kind) {
				return "", "", fmt.Errorf("--base-image %q: %q is not a base kind (%s): %s was removed in 0.7.0 (%s)", v, kind, strings.Join(config.Bases, ", "), kind, config.MigrationDoc)
			}
			return "", "", fmt.Errorf("--base-image %q: %q is not a base kind (%s)", v, kind, strings.Join(config.Bases, ", "))
		}
	} else if m := baseImageNameRE.FindStringSubmatch(v); m != nil && slices.Contains(config.Bases, m[1]) {
		kind, ref = m[1], v
	} else {
		return "", "", fmt.Errorf("--base-image %q: cannot tell which base kind it is for; write KIND=IMAGE (kinds: %s) or use an image named fugaro-<kind>", v, strings.Join(config.Bases, ", "))
	}
	if ref == "" || strings.ContainsAny(ref, " \t\r\n") {
		return "", "", fmt.Errorf("--base-image %q: %q is not an image reference", v, ref)
	}
	return kind, ref, nil
}

// oldBaseImageKey is the refusal of a local config of before base images were
// per kind, with the line to edit made out of its own value.
func oldBaseImageKey(ref string) string {
	if ref == "" {
		ref = "<image>"
	}
	return fmt.Sprintf("local config: `base_image: %[1]s` is now `base_images: {base: %[1]s}`: there is one base image per base kind (%s); edit that line",
		ref, strings.Join(config.Bases, ", "))
}

// legacyBaseImageKey refuses a base_images key: one of config.LegacyBaseKinds
// (go, web-node, java-services) names the 0.7.0 removal and its fix; any
// other unknown kind just says it is unknown, since it was never a real base
// and never a thing the 0.7.0 cut removed.
func legacyBaseImageKey(kind string) string {
	if !slices.Contains(config.LegacyBaseKinds, kind) {
		return fmt.Sprintf("base_images.%s: unknown base kind %q (%s)", kind, kind, strings.Join(config.Bases, ", "))
	}
	return fmt.Sprintf("base_images.%s: the go, web-node and java-services bases were removed in 0.7.0; delete the line and run fugaro image refresh in each repository's checkout, which copies fugaro-base and records base_images.base (%s)", kind, config.MigrationDoc)
}

// oldProjectKey is the refusal of a local config of before project
// configs, whose project: was the GCP project.
const oldProjectKey = "local config: `project:` is now `gcp_project:`, and the file lives at projects/<name>.yaml with name: <name>; see docs/design/m9-budget-and-dashboard.md §13.1"

// oldMailboxKeychainItem refuses the shape decision 9 replaced: one item name
// for the project, where the block now holds one per pairing. It is read off
// the raw document for the reason oldProjectKey is: decoded into the map the
// field is now, a scalar is yaml's own type error, which says nothing about
// what to do — and there is no shim to write, because a single name names no
// pairing and which phone's key it held is exactly what it does not say
// (pre-1.0, no compatibility).
func oldMailboxKeychainItem(mailbox *yaml.Node) string {
	if mailbox.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(mailbox.Content); i += 2 {
		k, v := mailbox.Content[i], mailbox.Content[i+1]
		if k.Value != "keychain_item" || v.Kind == yaml.MappingNode || v.Tag == "!!null" {
			continue
		}
		return fmt.Sprintf("local config: `mailbox.keychain_item: %s` is now one item per pairing — a mapping of the uid `fugaro pair` wrote (m~…) to the OS keychain item's name; run `fugaro pair` and record the item name it prints, and delete that line", v.Value)
	}
	return ""
}

// The local loader's own values, which Parse leaves in place for a file that
// sets the key nowhere. They are named because the settings registry cites
// them in the DEFAULT column (spec §2.4): the row for one of these keys is the
// value a run gets, not an "unset means" sentence.
const (
	DefaultMaxParallel      = 20
	DefaultBuildMachineType = "E2_HIGHCPU_8"
)

// Parse decodes the config strictly, applies defaults and validates it.
func Parse(data []byte) (*Config, error) {
	var top map[string]yaml.Node
	if yaml.Unmarshal(data, &top) == nil {
		if _, ok := top["project"]; ok {
			return nil, errors.New(oldProjectKey)
		}
		if n, ok := top["base_image"]; ok {
			return nil, errors.New(oldBaseImageKey(n.Value))
		}
		if n, ok := top["mailbox"]; ok {
			if msg := oldMailboxKeychainItem(&n); msg != "" {
				return nil, errors.New(msg)
			}
		}
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("local config: %w", err)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if c.MaxParallel == 0 {
		c.MaxParallel = DefaultMaxParallel
	}
	if c.Build.MachineType == "" {
		c.Build.MachineType = DefaultBuildMachineType
	}
	if err := c.validate(); err != nil {
		return &c, err
	}
	return &c, c.checkRegistryHostProject()
}

// checkRegistryHostProject refuses a registry_host naming another GCP
// project than the file's own gcp_project: the file must be consistent.
func (c *Config) checkRegistryHostProject() error {
	if m := registryHostRE.FindStringSubmatch(c.RegistryHost); m != nil && m[1] != c.GCPProject {
		return fmt.Errorf("local config: registry_host %q names GCP project %s, not %s", c.RegistryHost, m[1], c.GCPProject)
	}
	return nil
}

// profileNames is the config's profile names, sorted (a map's range is
// random, so the errors below would otherwise come out in a different order
// on each read).
func profileNames(profiles map[string]config.JobProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ReviewFloorPolicy is the owner's floor in the policy package's vocabulary:
// the form `fugaro project policy publish` writes into the node, and nil when
// the file sets no floor — the key left out and an empty object alike, which
// is what leaves the published node without the member. The rule itself is
// policy.ParseFloor, the one the node's own decoder applies, so the file the
// operator writes and the node the runner reads cannot disagree about what a
// written floor means.
//
// A name outside either vocabulary is an error rather than a value dropped:
// the floor is a minimum, so a level lost on the way to the node would be a
// run reviewing less than the file says, which is the direction this key must
// never move.
func (c *Config) ReviewFloorPolicy() (map[rigor.Mode][]policy.Level, error) {
	return policy.ParseFloor(c.ReviewFloor)
}

func (c *Config) validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("local config: "+f, a...)) }
	if c.Version != 1 {
		bad("version must be 1")
	}
	if !config.ProjectNameRE.MatchString(c.Name) {
		bad("name %q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", c.Name)
	}
	if !projectRE.MatchString(c.GCPProject) {
		bad("gcp_project %q is not a GCP project ID", c.GCPProject)
	}
	if !regionRE.MatchString(c.Region) {
		bad("region %q is not a region such as us-central1", c.Region)
	}
	if c.Bucket == "" && !bucketRE.MatchString(c.RunsBucket) {
		bad("runs_bucket %q is not a bucket name (or set bucket_url)", c.RunsBucket)
	}
	if c.MaxParallel < 1 {
		bad("max_parallel must be at least 1")
	}
	if c.RegistryHost != "" && !registryHostRE.MatchString(c.RegistryHost) {
		bad("registry_host %q is not <region>-docker.pkg.dev/<project>", c.RegistryHost)
	}
	// The operator's rotation record is refused here rather than at doctor:
	// doctor is a reading on a bad day, and a period or a date it cannot use
	// would turn into a check that silently says nothing.
	if c.SecretRotationDays < 0 {
		bad("secret_rotation_days must be zero or more days")
	}
	for _, name := range slices.Sorted(maps.Keys(c.SecretExpires)) {
		if name == "" {
			bad("secret_expires has an entry with no secret name")
			continue
		}
		if _, err := time.Parse(SecretExpiryLayout, c.SecretExpires[name]); err != nil {
			bad("secret_expires.%s %q is not a date (YYYY-MM-DD)", name, c.SecretExpires[name])
		}
	}
	// Profile names, and the one value rule only this document can apply: what
	// a profile holds is otherwise refused where it is used and published
	// (projectpolicy.validProfiles, which `fugaro project policy publish`
	// runs), so that a profile missing its provider is a publish refusal with
	// the profile's name in it rather than a parse error here.
	//
	// The name rule is config.ProjectNameRE, the same one projectpolicy's
	// ValidProfileName applies to a policy's own profile names (that package
	// is not imported here: it is the reader of the published node, and this
	// is the file it is published from).
	//
	// The value rule is allow_repos written with nothing under it. The key
	// absent means the project's allow_repos applies (§4.1), and a list written
	// empty is refused there too (projectpolicy checkWrittenProfileFields), but
	// the carrier's omitempty means a blank list never reaches the node: it
	// arrives at the publisher as the nil slice that means the same thing as
	// the absent key, and the profile silently widens to every repository the
	// project enrolls — the one direction admission must never move. Only this
	// document can tell the two spellings apart (config.JobProfile's
	// allowReposWritten).
	for _, name := range profileNames(c.Profiles) {
		if !config.ProjectNameRE.MatchString(name) {
			bad("profiles.%s: a profile name must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", name)
		}
		if pr := c.Profiles[name]; pr.AllowReposWritten() && len(pr.AllowRepos) == 0 {
			bad("profiles.%s.allow_repos: a written list with no repository in it is ambiguous: remove the key to fall back to the project's allow_repos, or write the repositories this profile admits (the project's allow_repos is still required, so a profile's list can only narrow it)", name)
		}
	}
	// A profile's claude_accounts entries are resolved here against the two
	// blocks an account can be defined in, because this file is where both are
	// written, and an account a profile names but this file does not define
	// names no credential at all (design project-named-oauth-tokens §4.4 row 1,
	// plan Task 1). The one question only the published node can answer —
	// whether the profile's own allow_repos covers every repository the
	// account's allow_data_to does — is projectpolicy's, where both lists are
	// in hand.
	for _, p := range config.ValidateProfileClaudeAccounts(c.Profiles, c.ClaudeAccounts, c.ProjectSecrets) {
		bad("%s: %s", p.Path, p.Message)
	}
	if c.ImageSource != "" {
		if err := mirror.ValidSourcePrefix(c.ImageSource); err != nil {
			bad("image_source %v", err)
		}
	}
	if c.LogView != "" && !logViewRE.MatchString(c.LogView) {
		bad("log_view %q is not projects/<project>/locations/<location>/buckets/<bucket>/views/<view>", c.LogView)
	}
	if c.SchedulerRegion != "" && !regionRE.MatchString(c.SchedulerRegion) {
		bad("scheduler_region %q is not a region such as us-east4", c.SchedulerRegion)
	}
	// The build block goes into Cloud Build resource paths and requests.
	if c.Build.Region != "" && !regionRE.MatchString(c.Build.Region) {
		bad("build.region %q is not a region such as us-east5", c.Build.Region)
	}
	if !machineTypeRE.MatchString(c.Build.MachineType) {
		bad("build.machine_type %q is not a machine type such as E2_HIGHCPU_8", c.Build.MachineType)
	}
	if c.BackendName != "" && c.BackendName != backend.CloudRun {
		// cloud-batch is a name this build knows -- internal/backend/batch
		// speaks it and the fake tests it -- and must not select yet: the
		// tasks that make a Batch run safe come after Check 57a (design
		// compute-backend.md §8.1 Task 2, §9). Calling it unknown would be a
		// different claim, and a wrong one.
		if c.BackendName == backend.CloudBatch {
			bad("backend %q is not enabled yet (this build runs %s)", c.BackendName, backend.CloudRun)
		} else {
			bad("backend %q is not a known backend (%s)", c.BackendName, backend.CloudRun)
		}
	}
	validateBatch(bad, c)
	if t := c.Terraform; t.StateBucket != "" && !bucketRE.MatchString(t.StateBucket) {
		bad("terraform.state_bucket %q is not a bucket name", t.StateBucket)
	}
	switch c.Terraform.RegistryCleanup {
	case "", "dry-run", "on", "off":
	default:
		bad("terraform.registry_cleanup %q must be dry-run, on or off", c.Terraform.RegistryCleanup)
	}
	if e := c.Terraform.AlertEmail; e != "" && !emailRE.MatchString(e) {
		bad("terraform.alert_email %q is not an email address", e)
	}
	for _, l := range []struct {
		name    string
		members []string
	}{{"launchers", c.Terraform.Launchers}, {"operators", c.Terraform.Operators}} {
		for _, m := range l.members {
			if !memberRE.MatchString(m) {
				bad("terraform.%s: %q is not an IAM member (user:, group:, serviceAccount: or domain:)", l.name, m)
			}
		}
	}
	for _, m := range c.Terraform.BudgetAdmins {
		if !budgetAdminRE.MatchString(m) {
			bad("terraform.budget_admins: %q is not an IAM member (user:, group: or serviceAccount: and one address; no domain: and no wildcards)", m)
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(c.BaseImages)) {
		switch ref := c.BaseImages[kind]; {
		case !slices.Contains(config.Bases, kind):
			bad("%s", legacyBaseImageKey(kind))
		case ref == "" || strings.ContainsAny(ref, " \t\r\n"):
			bad("base_images.%s: %q is not an image reference", kind, ref)
		}
	}
	c.validateBudget(bad)
	for _, p := range config.ValidateModelProviders(c.Providers) {
		bad("%s: %s", p.Path, p.Message)
	}
	// The routing switch is judged against the providers it resolves through:
	// its explicit list (D1a) and the allow_data_to safeguard (D1b) are both
	// checked here, at config load, which is the one place the operator's own
	// providers are known.
	for _, p := range config.CheckRouting(c.Routing, c.Providers, "routing") {
		bad("%s: %s", p.Path, p.Message)
	}
	for _, p := range config.ValidateClaudeAccounts(c.ClaudeAccounts) {
		bad("%s: %s", p.Path, p.Message)
	}
	// The account-choice keys are checked against the REFERENCE rule and not
	// the definition's: each one names an account a run may use, and "default"
	// is a legal name for the unsuffixed secret and only here — the
	// definition's rule belongs to claude_accounts, which refuses it. Whether
	// the named account is admitted for a repository is decided at launch
	// against the account's own allow_data_to, where the repository is known.
	if c.DefaultClaudeAccount != "" {
		if msg := config.ValidClaudeAccountRef(c.DefaultClaudeAccount); msg != "" {
			bad("default_claude_account: %s", msg)
		}
	}
	for _, stage := range slices.Sorted(maps.Keys(c.ClaudeAccountStages)) {
		if ref := c.ClaudeAccountStages[stage].ClaudeAccount; ref != "" {
			if msg := config.ValidClaudeAccountRef(ref); msg != "" {
				bad("stages.%s.claude_account: %s", pluginwire.Printable(stage), msg)
			}
		}
	}
	for _, p := range config.ValidateProjectSecrets(c.ProjectSecrets) {
		bad("%s: %s", p.Path, p.Message)
	}
	if w := c.Watch; w != nil && w.BurnAlertUSDPerHour != nil {
		if _, err := pricing.FromUSD(*w.BurnAlertUSDPerHour); err != nil {
			bad("watch.burn_alert_usd_per_hour: %v (a finite dollar amount, 0 or more)", err)
		}
	}
	// The mailbox block's one field is a keychain item name per pairing, and
	// the rule for the name is keychain.ParseItem's: this file is the one place
	// the name is written, and the package that turns it into an argv is the
	// one that says what shape it may have. A name refused here is refused
	// before `fugaro mailbox` can read a key, which is the run-level refusal
	// §8's row 1 names. A block with no entry is refused for the same reason a
	// block with no name was: it is a mailbox that can read nothing, and the
	// operator wrote it.
	if m := c.Mailbox; m != nil {
		if len(m.KeychainItem) == 0 {
			bad("mailbox.keychain_item: the block names no pairing's item, so `fugaro mailbox` would have no key to read; run `fugaro pair` and record the item name it prints")
		}
		for _, uid := range slices.Sorted(maps.Keys(m.KeychainItem)) {
			if !pairUIDRE.MatchString(uid) {
				bad("mailbox.keychain_item.%s: the key is not a pairing's uid (%q followed by 22 unpadded base64url characters is the device uid `fugaro pair` prints, and %q followed by 43 is a pairing session's), so no pairing would ever look this item up", uid, "m~", "p~")
				continue
			}
			if _, err := keychain.ParseItem(m.KeychainItem[uid], c.Name); err != nil {
				bad("mailbox.keychain_item.%s: %v", uid, err)
			}
		}
	}
	// The credentials service block is the installation's own URL, and its
	// shape is the rule the service itself holds its audience to
	// (internal/cli's credentialsServiceAudience): the launcher hands this
	// value to the job, the runner mints the job's Google ID token for it and
	// calls it, so a value that is not exactly the URL Cloud Run serves the
	// revision on is a run whose every call is refused by IAM — or, worse,
	// admitted by something else. An empty url with the block present is
	// refused rather than read as "the service is off": the block's presence
	// IS the opt-in (see CredentialsService), and an empty one would put the
	// variable on the job empty, which the runner reads as "set" for nothing.
	if cs := c.CredentialsService; cs != nil {
		if err := validCredentialsServiceURL(cs.URL); err != nil {
			bad("credentials_service.url: %v", err)
		}
	}
	for _, region := range slices.Sorted(maps.Keys(c.ComputePrices)) {
		if !regionRE.MatchString(region) {
			bad("compute_prices: %q is not a region such as us-central1", region)
			continue
		}
		p := c.ComputePrices[region]
		for _, f := range []struct {
			name string
			v    float64
		}{{"vcpu_second_usd", p.VCPUSecondUSD}, {"gib_second_usd", p.GiBSecondUSD}} {
			if !ValidPrice(f.v) {
				bad("compute_prices.%s.%s is %v: it must be set, finite, more than 0 and at most %v (US dollars per second)", region, f.name, f.v, MaxPriceUSD)
			}
		}
	}
	for _, ep := range []struct{ name, url string }{
		{"run", c.Endpoints.Run}, {"logging", c.Endpoints.Logging},
		{"secret_manager", c.Endpoints.SecretManager}, {"cloud_build", c.Endpoints.CloudBuild},
		{"storage", c.Endpoints.Storage}, {"iam", c.Endpoints.IAM},
		{"artifact_registry", c.Endpoints.ArtifactRegistry}, {"resource_manager", c.Endpoints.ResourceManager},
		{"cloud_scheduler", c.Endpoints.CloudScheduler}, {"service_usage", c.Endpoints.ServiceUsage},
		{"cloud_billing", c.Endpoints.CloudBilling}, {"firebase_database", c.Endpoints.FirebaseDatabase}, {"api_keys", c.Endpoints.APIKeys}, {"firebase_management", c.Endpoints.FirebaseManagement},
		{"iam_credentials", c.Endpoints.IAMCredentials}, {"identity_toolkit", c.Endpoints.IdentityToolkit},
		{"firestore", c.Endpoints.Firestore}, {"firebase_rules", c.Endpoints.FirebaseRules},
		{"cloud_asset", c.Endpoints.CloudAsset},
		// The Batch API root is the opener's, not init's (internal/cli's
		// cloudOpeners hands it to internal/backend/batch), but it is the
		// same kind of value: internal/backend/batch's clientOptions passes
		// it to option.WithEndpoint with the operator's ADC attached, so a
		// plain-http root elsewhere would send that token to a stranger.
		// Every sibling is refused here for that reason, and this one is no
		// different.
		{"batch", c.Endpoints.Batch},
		// The Compute Engine root `fugaro doctor` reads a Batch
		// installation's own facts through (the preload image family, the
		// network, the subnetwork). Same kind of value as `batch` above and
		// refused for the same reason: doctor attaches the operator's ADC to
		// it.
		{"compute", c.Endpoints.Compute},
	} {
		if ep.url == "" {
			continue
		}
		if err := checkEndpoint(ep.url); err != nil {
			bad("endpoints.%s: %v", ep.name, err)
		}
	}
	if c.BaseBranch != "" && !config.ValidBranchName(c.BaseBranch) {
		bad("base_branch %q is not a plain git branch name (the project's default base branch)", c.BaseBranch)
	}
	// The floor is a closed vocabulary twice over — the modes rigor names and
	// the three levels — and this file is where it is written. Its one rule is
	// ReviewFloorPolicy's, which is policy.ParseFloor, the rule
	// projectpolicy.Decode holds the node to: called here so that the load and
	// the publish cannot disagree about what a written floor means, and so that
	// neither can disagree with the node. A name it refuses is a run that would
	// review less than the owner asked for, and nothing downstream can tell a
	// misspelt mode from one the owner meant to leave alone.
	if _, err := c.ReviewFloorPolicy(); err != nil {
		bad("%v", err)
	}
	for repo, r := range c.Repos {
		if !repoRE.MatchString(repo) {
			bad("repos: %q must look like owner/name", repo)
		}
		if r.Provider != "" && !slices.Contains(config.Providers, r.Provider) {
			bad("repos.%s: provider %q must be one of %s", repo, r.Provider, strings.Join(config.Providers, ", "))
		}
		if r.BaseBranch != "" && !config.ValidBranchName(r.BaseBranch) {
			bad("repos.%s: base_branch %q is not a plain git branch name", repo, r.BaseBranch)
		}
		if r.GitHubAppID != "" && !appIDRE.MatchString(r.GitHubAppID) {
			bad("repos.%s: github_app_id %q is not a GitHub App ID (1 to 20 digits)", repo, r.GitHubAppID)
		}
		for _, w := range r.Workflows {
			if !config.WorkflowNameRE.MatchString(w) {
				bad("repos.%s: workflow %q is not a workflow name", repo, w)
			}
		}
	}
	return errors.Join(errs...)
}

// Override applies --gcp-project and --region when they are set, and
// validates the result: both go into resource paths. --gcp-project can
// only agree with the config's gcp_project: pointing a project's config at
// another GCP project would act on another project through it.
func (c *Config) Override(gcpProject, region string) error {
	if gcpProject != "" && gcpProject != c.GCPProject {
		return fmt.Errorf("--gcp-project %s is not project %s's GCP project %s; a project config can't be pointed at another GCP project (select that project's config instead)", gcpProject, c.Name, c.GCPProject)
	}
	if region != "" {
		c.Region = region
	}
	return c.validate()
}

// checkEndpoint refuses an endpoint the operator's credentials must not
// be sent to: anything but https, or plain http to this machine (the
// fakes tests run), or one carrying userinfo.
func checkEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL", raw)
	}
	if u.User != nil || u.Host == "" {
		return fmt.Errorf("%q must be an https URL with a host and no userinfo", raw)
	}
	switch h := u.Hostname(); {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (h == "127.0.0.1" || h == "localhost" || h == "::1"):
		return nil
	}
	return fmt.Errorf("%q must be https (plain http only to 127.0.0.1, localhost or ::1)", raw)
}

// ValidPrice reports whether v is a usable per-second price: finite, more
// than 0 and at most MaxPriceUSD.
func ValidPrice(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= MaxPriceUSD
}

// PriceOverride is region's compute price override, and whether there is
// one; without one, callers use the backend's list price. (The list price
// isn't looked up here: the GCP backend's live tests read this package.)
func (c *Config) PriceOverride(region string) (backend.Prices, bool) {
	p, ok := c.ComputePrices[region]
	if !ok {
		return backend.Prices{}, false
	}
	return backend.Prices{VCPUSecondUSD: p.VCPUSecondUSD, GiBSecondUSD: p.GiBSecondUSD, Source: PriceSource}, true
}

// Warnings are the config's deprecated settings, which still load, followed
// by any notes AddWarning added.
func (c *Config) Warnings() []string {
	var w []string
	if c.Build.ServiceAccount != "" {
		w = append(w, "build.service_account is deprecated and will be ignored: each repository's builds run as its own build account; fugaro update drops it")
	}
	return append(w, c.notes...)
}

// AddWarning appends a note to Warnings(), for something found after Parse
// returned (a published shared config's dropped legacy base_images
// entries): it is not itself a config field, so Parse cannot report it.
func (c *Config) AddWarning(w string) { c.notes = append(c.notes, w) }

// BucketURL is the runs bucket as a gocloud URL.
func (c *Config) BucketURL() string {
	if c.Bucket != "" {
		return c.Bucket
	}
	return "gs://" + c.RunsBucket
}

// RunsBucketName is the runs bucket's name: runs_bucket, else the bucket
// of a gs:// bucket_url, else "".
func (c *Config) RunsBucketName() string {
	if c.RunsBucket != "" {
		return c.RunsBucket
	}
	if u, err := url.Parse(c.Bucket); err == nil && u.Scheme == "gs" && u.Host != "" {
		return u.Host
	}
	return ""
}

// RecordBucketURL is the bucket image builds record in
// (builds/<slug>/<workflow>/image.json), as gs://<name>: always the runs
// bucket, the only bucket a build account may write (under
// builds/<slug>/), never another bucket_url. fugaro image build, fugaro
// setup's first build and the daily check send it; readiness, ls and
// image status read it. "" when there is no runs bucket name.
func (c *Config) RecordBucketURL() string {
	if name := c.RunsBucketName(); name != "" {
		return "gs://" + name
	}
	return ""
}

// BuildRegion is where Cloud Build runs.
func (c *Config) BuildRegion() string {
	if c.Build.Region != "" {
		return c.Build.Region
	}
	return c.Region
}

// Backend is the compute backend this config selects: backend.CloudRun
// when BackendName is unset.
func (c *Config) Backend() string {
	if c.BackendName == "" {
		return backend.CloudRun
	}
	return c.BackendName
}

// Me is who runs are requested by: user, else git config user.email.
func (c *Config) Me(ctx context.Context) (string, error) {
	if c.User != "" {
		return c.User, nil
	}
	cmd := exec.CommandContext(ctx, "git", "config", "user.email")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if me := strings.TrimSpace(string(out)); err == nil && me != "" {
		return me, nil
	}
	return "", errors.New("cannot tell who you are: set user in the local config or git config user.email")
}

// Marshal encodes the config as YAML.
func (c *Config) Marshal() ([]byte, error) { return yaml.Marshal(c) }
