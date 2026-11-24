package credsvc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The wire contract between a run's in-runner gateway and this service
// (D135, F30; plan task C1.10). It is written down once, here, because three
// tasks read it: C1.11 answers with it, C1.15's upstream sends it, and C1.17
// waits on it. It is the header names, the JSON refusal body, the headers
// that carry the tier and the credential's name, the switch record, and the
// typed "exhausted until T" answer.
//
// Two rules hold for everything in this file, and a test pins each: nothing
// it carries is ever a credential value (a name, a tier, a reason and an
// instant — never a token), and a name it carries is one clean header value,
// so a name that cannot be sent is refused rather than silently mangled.
//
// Everything here is held to the same rules when it is read as when it is
// written (review finding M2/M3): a tier, a credential name, a reason, a reset
// instant and a tried list are checked on both sides of the wire, so a value
// this code would refuse to send is also a value it refuses to accept. The
// refused body is included: WriteWireError will not write a tried list or a
// message ReadWireError would refuse (review finding N3). The rules are
// closed sets — a tier is A, B or C, a reason is one of the Reason*
// constants — because both ends of this contract are this repository's code:
// a new tier or a new reason is a change to this file, which is where the
// contract is written down. And no refusal here repeats what it refused: an
// error names the field, never the value (the package doc's no-echo rule).

// The request headers.
const (
	// HeaderRunIdentity carries the run's Firebase ID token: the budget
	// identity the launcher minted and the runner exchanged (token.Mint,
	// token.Exchange), for uid r~<slug>~<run>. It needs its own header
	// because the Authorization header is the job service account's Google
	// ID token, which Cloud Run IAM consumes for the invoker check (F30).
	HeaderRunIdentity = "X-Fugaro-Run-Identity"
	// HeaderRunJob names the Cloud Run job the run reports it is in: the job's
	// own CLOUD_RUN_JOB. It is the caller's claim and never evidence — a
	// launcher can override any environment variable (internal/runner's
	// jobName) — so the service holds it to the job-name rule (ValidJobName,
	// narrower than the header rule: review finding m3) and records it in the
	// run's record, and decides nothing from it: a run that names no job of
	// this installation cannot be placed, and which job actually called is
	// decided from the caller's verified service-account email (auth.go's
	// verifyJob). Which class the account's job is of — a per-repository job, a
	// shared project job or a review job (C3.9) — is decided there too, from the
	// account and never from this header.
	HeaderRunJob = "X-Fugaro-Run-Job"
	// HeaderPersonIdentity carries a person's own Google ID token to the GitHub
	// read mint, beside the same token in Authorization. Cloud Run's front end
	// consumes a valid ID token in Authorization for the invoker check and hands
	// the service that header changed, so a person's token (gcloud's own
	// audience, the only one a user account can mint) never arrives there as a
	// bearer; this header is not one the front end reads, so it arrives intact.
	HeaderPersonIdentity = "X-Fugaro-Person-Identity"
	// HeaderSlip names the slip a run's call is bound to: the id the grant
	// answered (DS1.3), which the launcher passes to the job and the runner
	// presents on the claim and on every call after it (DS1.6).
	//
	// It is a header of the RUN's calls and not of the grant's: the grant names
	// its run in the body and holds no slip yet, which is why the two routes of
	// D1937 read different halves of the contract. The id is not a secret
	// (creds.Slip's own note): it travels in the job's environment where the
	// agent can read it, and what admits a call is what the slip binds and the
	// claim secret, never this value.
	HeaderSlip = "X-Fugaro-Slip"
	// HeaderSlipClaim carries the claim secret the claim answered, and it is
	// the ONE value on these two routes that is a credential (D1941): the
	// record keeps only its hash, it is presented on every call after the
	// claim, and a call that presents none is the claim itself. It is why this
	// header's value is registered with the call's scrub like the two identity
	// tokens are, and why no line this service writes may carry it.
	HeaderSlipClaim = "X-Fugaro-Slip-Claim"
	// HeaderAuthMode is the auth the run's own repository goes out on, as the
	// run declares it: a value of config.AgentAuths (ValidAuthMode). It is the
	// caller's STATEMENT and never evidence (G16, R7): the agent holds the run
	// identity and the job's ID token, so it can send whatever it likes here,
	// and a repository's reach decided from what the caller says about itself
	// would be no boundary at all. The service decides from the record `fugaro
	// update` wrote (creds/repoauth/<slug>, B2.1) and reads this header only to
	// check it against that record: a call whose declaration disagrees with its
	// repository's own configuration is refused, never served.
	HeaderAuthMode = "X-Fugaro-Auth-Mode"
	// HeaderAuthAccount names the Claude account an oauth run goes out on,
	// spelled as config.ClaudeAccountNameRE spells one (config.DefaultClaudeAccount
	// for the unsuffixed account). It is the caller's statement too, and it is
	// read for one thing only: whether the repository's record names it.
	HeaderAuthAccount = "X-Fugaro-Auth-Account"
	// HeaderBuild names the build a call of the BITBUCKET PROXY is made for
	// (B1, rule 9): the id the build's grant was answered for
	// (buildtoken.Request.BuildID), which is the second half of the key the
	// grant's pairing is recorded under (creds.PathBuildPair). It is the one
	// header of the build class, and it is what the proxy's admission reads to
	// find the repositories that build may reach.
	//
	// It is the caller's own word, exactly as BuildID is on the grant route: a
	// project has one build identity, so any build of it can name any id, and
	// what the header buys is bookkeeping rather than authorization — a build
	// names its own pairing and no other build's, and what bounds the call is
	// the repository set that pairing holds. Its value is checked against the
	// build id shape (buildIDRE) before it reaches a path, so a call that
	// named a path is refused rather than read.
	//
	// It travels on a build's git calls as git config rather than on the wire
	// of a client this service ships: the generated clone step (B8) is what
	// sets it (http.extraHeader), and the build's own Dockerfile holds neither
	// it nor the ID token beside it.
	HeaderBuild = "X-Fugaro-Build"
)

// BitbucketProxyPrefix is the path prefix under which this service serves one
// run's Bitbucket traffic (B1.5): everything a run sends to bitbucket.org or to
// api.bitbucket.org, git and REST alike, arrives here with the prefix in front
// of the path the client built.
//
// It is one prefix and not two, because the classification (internal/gitproxy's
// Classify) tells the two forms apart from the path's own shape — git's
// "/{workspace}/{repo}.git/…" against the REST API's "/repositories/…" — and a
// second prefix would be a second spelling of the same route to keep in step.
//
// It is deliberately NOT the whole of "/v1/bitbucket/": BitbucketReadPath, the
// read mint a person calls, is its own route with its own caller class and is
// answered ahead of this one, so the prefix carries the extra segment that
// keeps the two apart rather than relying on the read mint's path failing to
// classify. A prefix that is a prefix of another route's exact path is a route
// whose meaning depends on the order two conditions happen to be written in.
//
// internal/gateway's loopback forwarder (B1.6) is what puts this prefix on, and
// the run's own clients never see it: that forwarder's copy of the string is a
// second spelling of this route, and B1.6 owes the test that holds the two
// together (TestGitForwardMirrorsCredsvc, the plan's own name for it). Nothing
// in this package can pin a spelling that does not exist yet.
const BitbucketProxyPrefix = "/v1/bitbucket/proxy/"

// WorkflowSecretsPath is the path a run's own job asks this service for one of
// its workflow's secrets on (B3.2, G19): a workflow secret the job does not
// mount is fetched from here at bootstrap, one secret per call, and the value
// is the answer's own field.
//
// The path, the request and the answer are here rather than beside the route
// because the caller is another package — the runner's fetch (B3.4) goes
// through internal/gateway's service client, which spells this contract the way
// it spells the model path's — and a contract two packages spell is written
// down once, in this file, for the reason this file's own header gives. Which
// workflow a call may be served is decided from the VERIFIED job account and
// never from anything the call carries (G19): the request names one variable
// (version 1) or one logical secret (version 2) of the caller's own repository
// and nothing else, so there is no field in which a caller could name another
// workflow, another repository or another job's secret.
const WorkflowSecretsPath = "/v1/workflow-secrets"

// maxWorkflowSecretsRequestBytes bounds a workflow-secrets call's body. The
// whole request is one or two small fields ({"env":"NAME"}, or
// {"name":"secret","version":2}), so the bound is the mint's and small on
// purpose: a body larger than this is not one of these calls, and it is refused
// before it is decoded.
const maxWorkflowSecretsRequestBytes = 4 << 10

// The two versions of this contract, as the request's version field spells
// them. Version 1 is the ABSENCE of the field rather than the value 1: every
// caller of it was written before the field existed and writes none, so a
// version-1 call is byte for byte the body it has always been and a body that
// spells 1 is one no side of this contract produces (Validate refuses it).
//
// Version 2 is D3324/D3328/D3333: the call names the LOGICAL secret
// (config.Secret.Name) instead of the variable, the unit of access becomes the
// repository rather than the workflow, the value is served from the project's
// own `fugaro_p_<name>` container, and the name's allow-list decides which
// repositories may fetch it. The `env` field keeps its meaning throughout and
// version 2 does not read it (CLAUDE.md, "a field never changes meaning"): the
// field still says which variable a value is exposed as, and a version-2 call
// names no variable at all.
const (
	// WorkflowSecretsVersion1 is the version a request that declares none is.
	WorkflowSecretsVersion1 = 1
	// WorkflowSecretsVersion2 is the version that carries a name.
	WorkflowSecretsVersion2 = 2
)

// WorkflowSecretsRequest is what such a call carries, in one of its two shapes:
// the environment variable the job's own mount would have named (version 1, the
// record creds/workflowsecrets/<slug> deciding which container that variable is
// read from), or the logical secret's name (version 2, served from the project
// record's `fugaro_p_<name>` container). Both are the CALLER's text and are
// therefore trusted for nothing — the record decides whether the calling job's
// repository or workflow holds a secret under what it named, and a name the
// record does not hold is refused with a typed refusal rather than being looked
// up anywhere else.
//
// There is no field for the repository, the workflow or the run: the first two
// are the verified job account's (JobAccount.Slug, JobAccount.Workflow) and the
// third is the verified run identity's, and a contract that let a caller name
// any of them would be a contract whose service decided from what the call
// said about itself.
type WorkflowSecretsRequest struct {
	// Env is the variable's name (version 1), held to config.EnvNameRE — the
	// shape the workflow-secret check itself uses (D1372), so a name no job
	// could have mounted under is refused as a request this route does not read
	// rather than searched for.
	Env string `json:"env,omitempty"`
	// Name is the logical secret's name (version 2), held to
	// config.SecretNameRE — the shape a repository's `secrets:` entry is held
	// to, because a version-2 call names the same entry a workflow declares.
	Name string `json:"name,omitempty"`
	// Version declares which of the two shapes this call is. It is absent on
	// every version-1 call, and 2 on every version-2 one.
	Version int `json:"version,omitempty"`
}

// VersionOf is the version this call is: what it declares, and
// WorkflowSecretsVersion1 when it declares nothing.
//
// A version this contract does not carry is REPORTED and not clamped, so the
// reader that meets it refuses it as the body it is: a service or a runner that
// answered a version 3 as if it were version 2 would be reading a shape this
// release cannot know.
func (r WorkflowSecretsRequest) VersionOf() int {
	if r.Version == 0 {
		return WorkflowSecretsVersion1
	}
	return r.Version
}

// Validate holds a call to the shape of the version it declares, and it is the
// rule both sides of this contract read by: the service, before it reads any
// record, and the runner, so that a body it built is a body this contract
// carries before it reaches the wire.
//
// One shape per version, because the two name the same secret in two different
// vocabularies and a body that carried both would have to be read as one of
// them: version 1 is {env} and names no secret, version 2 is {name, version: 2}
// and names no variable. A field of the other version, a name either shape
// could not carry, and a version this contract does not know are each refused —
// by a sentence written here, naming the field that was wrong and never its
// content (the package's no-echo rule: the body is the caller's text).
func (r WorkflowSecretsRequest) Validate() error {
	switch r.VersionOf() {
	case WorkflowSecretsVersion1:
		if r.Version == WorkflowSecretsVersion1 {
			// Version 1 is the ABSENCE of the field, which is what every caller
			// of it writes: the strict reader refuses the spelled-out form
			// rather than reading two spellings of one shape, so there is one
			// body per version and a test of either side pins bytes.
			return errors.New("credsvc: a version 1 workflow-secrets call carries no version field")
		}
		if r.Name != "" {
			return errors.New("credsvc: a version 1 workflow-secrets call names no secret; it asks by environment variable")
		}
		if !config.EnvNameRE.MatchString(r.Env) {
			return errors.New("credsvc: a version 1 workflow-secrets call names no environment variable")
		}
		return nil
	case WorkflowSecretsVersion2:
		if r.Env != "" {
			return errors.New("credsvc: a version 2 workflow-secrets call names the secret and no environment variable")
		}
		if !config.SecretNameRE.MatchString(r.Name) {
			return errors.New("credsvc: a version 2 workflow-secrets call names no workflow secret")
		}
		return nil
	}
	return fmt.Errorf("credsvc: the call declares workflow-secrets version %d, which this contract does not carry", r.Version)
}

// WorkflowSecretsResponse is the answer: what was asked for — the variable
// (version 1) or the logical name (version 2) — and the value the record's
// container holds for it. It is ONE value and not the workflow's whole set,
// which is what keeps every line this act writes naming exactly one container
// (audit.go) and keeps what a caller is served to what it named — a job is
// served the secret it asked for and no other.
//
// The value is a credential and this answer is the only place it appears: it is
// written no-store (writeReadAnswer), no header of the answer carries it, and
// neither the audit line nor any refusal of this route can hold it (G18).
type WorkflowSecretsResponse struct {
	// Env is the variable the value is for, echoed so an answer can never be
	// read as another variable's. A version-2 answer carries none.
	Env string `json:"env,omitempty"`
	// Name is the logical secret the value is for, echoed for the same reason
	// Env is: a caller that asked by name must be able to tell this answer from
	// another name's, and the runner reads it against the name it sent. A
	// version-1 answer carries none.
	Name string `json:"name,omitempty"`
	// Value is the credential's value.
	Value string `json:"value"`
}

// BuildCredentialsPath is the path a REPOSITORY's build asks this service for
// its credentials on (B4.3, R5, D2076, D2077): the git credential of the one
// repository the calling account is bound to, and the build secrets of the
// workflow the call names out of the record `fugaro update` wrote for that same
// repository.
//
// It is a path of its own and not a field of BuildTokenPath, because the two
// routes have two caller classes: that one's caller is the installation's
// catalog-build identity, which names no repository and is answered the
// repositories a published image map puts on the entry it names, while this
// one's is a repository's build service account, whose whole binding is the one
// repository it bakes (G20). One path with a class switch would be a route
// whose answer depends on which list the caller happened to be in, which is
// exactly what this service's one-class-per-route rule refuses everywhere.
//
// The contract is here rather than beside the route because the caller is
// another package — the build step's `fugaro image git-credential` (B4.5) —
// and a contract two packages spell is written down once, in this file.
const BuildCredentialsPath = "/v1/build/credentials"

// maxBuildCredentialsRequestBytes bounds a build-credentials call's body. The
// whole request is a workflow name, a short list of environment-variable names
// and an App ID, so the bound is the mint's and small on purpose: a body larger
// than this is not one of these calls, and it is refused before it is decoded.
const maxBuildCredentialsRequestBytes = 8 << 10

// BuildCredentialsRequest is what such a call carries: the workflow whose build
// secrets the build needs, the environment variables it needs them under, and —
// for a GitHub repository — the App ID the mint's JWT is signed as.
//
// There is deliberately no field for the repository, the slug or the provider.
// All three are the VERIFIED caller's own row (RepoBuildAccount), which the
// deployment derived: a contract that let a build name its own repository would
// be a contract whose service decided from what the call said about itself, and
// a build of one repository would be able to ask for another's credentials —
// the whole of what G20 refuses. A body that carries any of them is refused by
// the strict decoder rather than read for the fields it does know.
//
// Workflow is the caller's text and is trusted for nothing: the record decides
// whether it holds a secret for that workflow, and a workflow the record does
// not hold is refused with a typed refusal rather than served the repository's
// union. The account is per repository and not per workflow — that is what
// makes it a build's account and not a job's — so the workflow is a SELECTOR
// within the caller's own record and never a widening of what the caller may
// reach: every entry the record holds under it belongs to the calling
// repository.
//
// Secrets is the list of environment variables the build needs. It is the
// build's own declaration (the derived image's `_SECRET_ENVS`) and it can only
// NARROW: a name the record does not hold under the named workflow is refused
// (workflowSecretNoSuchEnv's rule, B3.2), so a caller that names a name the
// record does not hold learns that it is not recorded and nothing else. An
// empty list is a workflow with no build secrets, which is a build served its
// git credential alone.
type BuildCredentialsRequest struct {
	// Workflow is the workflow whose build secrets the build needs, spelled the
	// way the record's own map is keyed (creds.WorkflowSecrets.Workflows).
	Workflow string `json:"workflow"`
	// Secrets are the environment variables the build needs the workflow's
	// secrets under, held to config.EnvNameRE — the shape a job could mount a
	// secret under, which is the shape the record's own entries carry.
	Secrets []string `json:"secrets"`
	// AppID is the GitHub App ID the mint's JWT is signed as (1 to 20 digits).
	// It is required for a GitHub repository and refused for a Bitbucket one,
	// and it is not a secret: it is the mint route's own field for the mint
	// route's own reason — the service's environment does not hold it, so the
	// caller supplies the project's own configuration value. A wrong App ID
	// makes GitHub refuse the JWT; it cannot make the service sign with another
	// App's key, because the key is the bundle's.
	AppID string `json:"app_id,omitempty"`
}

// BuildGitCredential is the git credential the build clones and pushes with:
// the username, the token's value and the host it is for. It is
// provider-shaped rather than GitHub-shaped because the two providers answer
// differently (D2076): a GitHub repository is answered a read-only installation
// token the service just minted for it, and a Bitbucket one the bundle's own
// token, whose reach is unchanged.
//
// ExpiresAt is GitHub's own instant and the zero time for a Bitbucket token,
// which carries no expiry the service can read — so a client reads the zero
// value as "no instant is known" and not as "expired".
type BuildGitCredential struct {
	// Provider is the provider the credential is for, echoed from the caller's
	// own row so a client can never use a GitHub credential as a Bitbucket one.
	Provider string `json:"provider"`
	// Username is the git username the provider's HTTPS basic auth takes
	// (github.GitUsername, bitbucket.GitUsername).
	Username string `json:"username"`
	// Token is the credential's value and the reason this endpoint exists.
	// Nothing in this package logs it, and a caller must not either (G18).
	Token string `json:"token"`
	// Host is the host the credential is for (repourl.ProviderHost), which the
	// service answers rather than the caller: a credential's destination is the
	// provider's and never a call's.
	Host string `json:"host"`
	// ExpiresAt is when the token stops working, for a provider that says.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// BuildSecretValue is one workflow secret the build asked for: the variable and
// the value the record's container holds for it. It is the workflow-secrets
// route's answer, in a list, for the build's own reason — a build needs the
// whole set its workflow declares at once, and the one-line audit rule is kept
// by the answer being one act.
//
// The value is a credential and this answer is the only place it appears
// (G18): it is written no-store (writeReadAnswer), no header of the answer
// carries it, and neither the audit line nor any refusal of this route can hold
// it.
type BuildSecretValue struct {
	// Env is the variable the value is for, echoed so a client can never read
	// one value as another variable's.
	Env string `json:"env"`
	// Value is the credential's value.
	Value string `json:"value"`
}

// BuildCredentialsResponse is the route's answer: the git credential, and the
// workflow secrets the call asked for in the order it asked for them. An empty
// Secrets is a workflow with no build secrets, which is a build answered its
// git credential alone.
type BuildCredentialsResponse struct {
	Git     BuildGitCredential `json:"git"`
	Secrets []BuildSecretValue `json:"secrets"`
}

// The response headers.
const (
	// HeaderTier is the credential tier that answered: TierA, TierB or
	// TierC. C1.16 settles the call against the caps from it (A is notional,
	// B and C are real money).
	HeaderTier = "X-Fugaro-Tier"
	// HeaderCredential is the name of the credential that answered, never
	// its value. It is what the run records on its agent entry (C1.19) and
	// what a reader displays — after internal/safetext, because a name is
	// text from another writer.
	HeaderCredential = "X-Fugaro-Credential"
	// HeaderCredentialOwner is the owner of the credential that answered,
	// never its value: the account id of the person whose credential it is.
	// It is a THIRD answer field beside the tier and the name, and it is what
	// the run records beside the name (F16, runstore.Record.CredentialOwner):
	// the record names whose token paid, which neither the name nor a Claude
	// account answers.
	//
	// It is written only where the credential served IS the launcher's own
	// per-user credential — the one case an owner is a verified fact
	// (credentialOwnedBy, cascade.go): a bundle entry is nobody's, a pool's
	// credential is its owner's and the launcher may merely be allowed by it
	// (R11, G8), and a call the service admitted under no slip at all — the
	// path an installation that has not deployed the slip routes still takes —
	// has no launcher to name. Every one of those answers carries none, and
	// the run's record then holds no owner.
	HeaderCredentialOwner = "X-Fugaro-Credential-Owner"
	// HeaderSwitch, when present, is the JSON of a Switch: this call moved
	// from one credential to another inside the service, and the run's
	// record logs it (C1.16).
	HeaderSwitch = "X-Fugaro-Switch"
	// HeaderProbe, when present, is the tier (or the tiers, comma separated)
	// a probe was made on for this call (C1.12): the service asked a
	// backed-off credential whether it works before serving, and that call is
	// the run's to account for — notional on tier A, money on any other tier
	// (C1.16, the plan's own rule for a probe).
	//
	// It carries tiers and never a name. Which credential a probe was made on
	// is the service's own record (C1.13's audit line, whose tier is this
	// one), and the run needs the ledger input and not the name: a name here
	// would be one more field to sanitise on a record the run never displays,
	// and the run learns the name of the credential that ANSWERED anyway
	// (HeaderCredential, C1.19).
	//
	// The header is written on a served call's answer only. A probe is made
	// on the path of the call that owes it (probeCall), so a probe owed on a
	// token count goes to the counting endpoint, which bills nothing and
	// settles nothing — and an answer that would carry this header is a
	// Messages answer.
	HeaderProbe = "X-Fugaro-Probed"
)

// The credential tiers of the spec, as they are spelled on the wire. Tier C
// is reserved: no code selects it until C3.6.
const (
	TierA = "A"
	TierB = "B"
	TierC = "C"
)

// ValidTier reports whether tier is one of the three tiers of the wire. It is
// the rule SetCredentialHeaders writes by and CredentialHeaders reads by: a
// tier is a ledger input (C1.16 settles a call by it — A is notional, B and C
// are real money), so a tier this code does not know is refused on both sides
// rather than carried.
func ValidTier(tier string) bool {
	switch tier {
	case TierA, TierB, TierC:
		return true
	}
	return false
}

// jobNameRE is the shape of a Cloud Run job name as this repository builds
// them: internal/backend/gcp's JobName/ProjectJobName sanitize to lower-case
// letters, digits and hyphens. Cloud Run itself requires a name to start with a
// letter and to end with a letter or a digit. The length is ValidJobName's, at
// maxJobNameBytes, so that the two rules are one rule each: this one says what
// a name is made of and the bound says how long it may be.
var jobNameRE = regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`)

// ValidJobName reports whether s may be the Cloud Run job a call reports it is
// in (HeaderRunJob). It is a narrower rule than the header-value rule on
// purpose (review finding m3): the value is recorded in the run's record and
// in the audit line, and every job name this installation can have is
// [a-z0-9-] and at most 49 characters, so a value that could not be one — a
// quote, a backslash, a '$', a ';', a backtick, a space, 512 bytes of
// anything — is refused rather than recorded. It is still the caller's claim
// and decides nothing (auth.go's verifyJob decides which job called, from the
// verified account).
func ValidJobName(s string) bool {
	return len(s) <= maxJobNameBytes && jobNameRE.MatchString(s)
}

// ValidAuthMode reports whether s may be the auth a call declares
// (HeaderAuthMode): one of config.AgentAuths, the same closed set the
// repository's own fugaro.yaml is held to. It is the same list rather than a
// second one, because the value a run declares has to be comparable with the
// value `fugaro update` recorded for that repository, and two spellings of the
// set is how a repository whose record says `oauth` meets a declaration this
// service refuses as unknown.
func ValidAuthMode(s string) bool {
	return config.KnownAgentAuth(s)
}

// ValidAuthAccount reports whether s may be the Claude account a call declares
// (HeaderAuthAccount). It is config's own rule for an account's name, which is
// the rule the record's accounts are held to as well: the header is only ever
// compared against that list, and a value the list could not hold is refused
// here rather than compared and found unequal — a refusal that says "not this
// repository's account" about a value that could not be an account at all would
// send an operator looking in the wrong document.
func ValidAuthAccount(s string) bool {
	if s == config.DefaultClaudeAccount {
		return true
	}
	return len(s) <= config.MaxClaudeAccountNameLen && config.ClaudeAccountNameRE.MatchString(s)
}

// The error types of the refusal body. The runner switches on these, not on
// a status: three of them share one status.
const (
	// ErrorExhaustedUntil is the typed answer of C1.11: no credential is
	// usable now, and the earliest reset is ResetAt. The runner waits for it
	// (C1.17) — under fallback: oauth-only, whatever the threshold.
	ErrorExhaustedUntil = "exhausted_until"
	// ErrorUnverifiedRunIdentity is the run's own identity being refused
	// (C1.10's Verify).
	ErrorUnverifiedRunIdentity = "unverified_run_identity"
	// ErrorUnverifiedJobIdentity is the caller's Google identity being
	// refused: the ID token Cloud Run IAM admitted is not one of the
	// installation's job service accounts.
	ErrorUnverifiedJobIdentity = "unverified_job_identity"
	// ErrorStateUnavailable is the service being unable to read the state it
	// must read (the write-once outcome, the cascade's marks): a refusal, so
	// the run halts budget_unavailable-style, and never a fallback (D143).
	ErrorStateUnavailable = "state_unavailable"
	// ErrorRequestRefused is the request itself being refused: the service
	// applies the gateway's own request check before it reads a credential, so
	// a caller that reached the service directly cannot name a model, a tool or
	// a shape the in-runner gateway would have refused (the C1.11 follow-up).
	// The message is the gateway's own sentence and it says that nothing was
	// read; no credential value is in it, because none was touched.
	ErrorRequestRefused = "request_refused"
	// ErrorIssueGone is the issue route's read of an issue its repository does
	// not hold: the number names no issue GitHub has — the ticket is deleted or
	// moved — and asking again cannot clear it, which is what tells this answer
	// from every other refusal of that route (B9, D2720). It is the one failure
	// of those acts that is not an outage, and the daemon's issue client reads
	// the status beside it rather than this code: the code is for the operator's
	// own line.
	ErrorIssueGone = "issue_gone"
	// ErrorNoReadConsumer is a read of a Bitbucket pull request refused for
	// want of the installation's read consumer: the bundle holds no
	// BitbucketReadConsumerSecret, so the service has no read-scoped token to
	// mint and no pull request of that repository can be read until one is set
	// (`fugaro creds set bitbucket-read-consumer`, F21, D2736).
	//
	// It is a type of its own and not ErrorStateUnavailable, which every other
	// failure of that read answers with, because the caller acts on the two
	// differently: an outage is retried on the next cadence, while this stands
	// until a person sets the credential, so the daemon records it once per
	// repository as a fact naming the fix (credsvc's label route, G3's
	// merge read). The status is the unavailability's — the service cannot read
	// a pull request now — and this code is what tells the two apart.
	ErrorNoReadConsumer = "no_read_consumer"
	// ErrorNoCandidateCredentials is a call the cascade was asked for and could
	// hand no credential to: the narrowing this service applies — the crafts
	// where the installation publishes them, O4's tier-A admission, the
	// repository's recorded auth and the slip fence a per-user or project
	// credential needs — left the list the selector is given empty, so there was
	// nothing to select from, nothing to wait for and no other tier to reach. The
	// state WAS read: it is what the service read that empties this call's list,
	// and the tried list beside this type names every credential that was
	// considered and the one reason each was dropped. It is deliberately not
	// ErrorStateUnavailable, whose name sends the operator to the database: this
	// is a policy and bundle question, and the sentence and the list say which (a
	// bundle that holds no model credential, an account the repository's own
	// admission does not cover, a credential the call carries no slip for). Like
	// every refusal but ErrorExhaustedUntil it halts the run (D143), and no
	// credential value is in it, because none was used.
	ErrorNoCandidateCredentials = "no_candidate_credentials"
	// ErrorWorkflowSecretNotAllowed is a version-2 workflow-secrets call whose
	// name is not one the caller's own repository may fetch (D3328): the
	// project record holds the name, and its allow-list does not admit the
	// repository the verified job account names. The status is the unverified
	// one — the verified caller is not the caller this secret is served to, and
	// asking again cannot change that — and the code is what the runner reads to
	// print the fix (`fugaro secrets allow NAME owner/repo`).
	//
	// It is deliberately not ErrorUnverifiedRunIdentity, whose sentence sends an
	// operator looking at an identity: here the identity is exactly what it
	// claims and the secret's own list is what refuses.
	ErrorWorkflowSecretNotAllowed = "workflow_secret_not_allowed"
	// ErrorWorkflowSecretUnmarked is a version-2 call whose name IS allowed and
	// whose container is not one this service serves a workflow secret from
	// (F18, G2): a `fugaro_p_<name>` container that carries no workflow-secret
	// marker, or one whose shape is not the intake's. The status is the
	// unavailable one, because it is the installation's state and not the
	// caller's call that is wrong, and the code is what tells it from the
	// ordinary refusals beside it — an operator reads it and runs the fix.
	ErrorWorkflowSecretUnmarked = "workflow_secret_unmarked"
	// ErrorWorkflowSecretsOff is version 2 refused because the repository's own
	// copies are still in force (G9, F24): `config.RepoSecretsOff` is false, so a
	// project copy would shadow a repository's own value, and the door that keeps
	// that from happening is not open yet in this release. The status is the
	// unavailable one, and the code is what makes a run at a newer release stop
	// with a sentence about the flip rather than about a missing secret.
	ErrorWorkflowSecretsOff = "workflow_secrets_off"
)

// The statuses. An unverified caller is 403; everything the service cannot
// answer now is 503, and the body's error type says which. A request the
// gateway's own rules refuse is 400 — the status the gateway answers the same
// body with — so a caller that is refused for what it sent is not told to try
// again. The issue route's read of an issue its repository does not hold is the
// one answer that is neither: 404, the status the daemon's issue client tells a
// ticket that is gone from an outage by (D2720), spelled as that client spells
// it (creds.IssueGoneStatus) and held there by TestIssueRouteGoneMatchesClient.
const (
	StatusUnverified       = http.StatusForbidden
	StatusUnavailable      = http.StatusServiceUnavailable
	StatusExhaustedUntil   = http.StatusServiceUnavailable
	StatusRequestRefused   = http.StatusBadRequest
	StatusIssueGone        = http.StatusNotFound
	contentTypeJSON        = "application/json"
	maxCredentialNameBytes = 200
	maxHeaderTokenBytes    = 512
	// maxJobNameBytes is internal/backend/gcp's maxJobName: Cloud Run's own 63
	// less room for the execution token the execute page adds.
	maxJobNameBytes = 49
	// maxRefusalMessageBytes bounds the free-text message of a refusal body
	// (ReadWireError). Every message this file writes is a sentence of a few
	// hundred bytes, and the longest one — an exhausted answer's list of the
	// credentials tried — is bounded by how many credentials a bundle may hold.
	maxRefusalMessageBytes = 8 << 10
	// maxTriedEntries bounds the structured Tried list, on both sides of the
	// wire (review finding N2). The message field is clipped at
	// maxRefusalMessageBytes, but the structured list is not, and it is what the
	// runner rebuilds a run's halt message from: a reader that accepted a list
	// of any length would build a sentence of any length from a body the writer
	// never clipped, and a hostile or broken peer could hand the runner a
	// gigabyte to render. Nothing bounds how many credentials a bundle may hold
	// yet — C1.11's loader is the task that writes that bound — so this is the
	// contract's own bound, and C1.11 must refuse a bundle larger than it: a
	// service that could legitimately consider more credentials than this would
	// be a service whose honest answer this contract refuses. Sixty-four is far
	// above any project's shared-token set, and the reader and the writer are
	// held to the same number, so a real cascade never meets it. A test ties the
	// two sides; the tie to C1.11's bundle bound is recorded as a C1.11
	// obligation, since that task's limit does not exist yet.
	maxTriedEntries = 64
	// maxProbedTiers bounds how many tiers one answer's probe header may
	// carry. A call probes at most one credential per credential it may probe
	// (C1.12's per-call bound is per name, firstDue), and the tiers are a
	// closed set of three, so an honest answer carries one or two
	// (TierA and TierB; tier C is reserved) and never more than this. The
	// writer refuses to write a longer list and the reader to read one, so a
	// hostile or broken peer cannot make the run settle an unbounded number
	// of probes.
	maxProbedTiers = 3
)

// Why a credential was not used, in the tried list. They are display strings:
// the spec asks a failed cascade to name which credentials were tried and why
// each was skipped.
const (
	ReasonExhausted   = "exhausted"    // marked exhausted until its reset
	ReasonBroken      = "broken"       // a 401: revoked, and only the operator clears it
	ReasonNotAdmitted = "not_admitted" // not in the run's repository's admitted set
	ReasonStopped     = "stopped"      // the operator's kill switch is on
)

// ValidReason reports whether reason is one of the reasons this contract
// carries — in a tried list, where it says why a credential was skipped, and
// in a switch record, where it says why a call moved. It is a closed set read
// by both sides (review finding M3): the reason reaches the run's halt
// message and its switch log, and the four constants above are the whole of
// why this cascade can move, so a fifth is a change to this file.
func ValidReason(reason string) bool {
	switch reason {
	case ReasonExhausted, ReasonBroken, ReasonNotAdmitted, ReasonStopped:
		return true
	}
	return false
}

// triedLine renders a tried list as one line: name (reason), name (reason), in
// the order the list holds. It is the rendering a refusal's own sentence and
// the operator's refusal line both use, so the run and the operator read one
// list and not two — and it names credentials and never a value, since a name
// is one of the bundle's keys and a reason is one of the four constants below.
func triedLine(tried []Tried) string {
	parts := make([]string, 0, len(tried))
	for _, tr := range tried {
		parts = append(parts, tr.Name+" ("+tr.Reason+")")
	}
	return strings.Join(parts, ", ")
}

// ValidCredentialName reports whether name may be a credential's name: one
// clean header value, at most 200 bytes (the rules' MaxString, the bound the
// agent entry's credential field shares).
//
// The bundle loader (C1.11) and the owner's `fugaro creds set` (C1.25) refuse
// a name this rejects, so a credential that could be loaded but never reported
// to a run does not exist in the first place.
func ValidCredentialName(name string) bool {
	return len(name) <= maxCredentialNameBytes && validHeaderToken(name)
}

// validHeaderToken reports whether s is one clean header value: non-empty,
// bounded, and printable ASCII with no space — so it can be sent as a header
// and written into a log line without a newline, a control character or a
// space hiding in it. Its callers are ValidCredentialName and the token and
// job-name checks RequestFromHTTP makes on the request headers; a Cloud Run
// job name is held to ValidJobName, which is this rule narrowed.
//
// It is NOT a database path-segment rule, and nothing that becomes a path is
// held to it: it accepts '/', '~', '.' and '..', which are exactly what a
// path segment must not be. The run's slug and run id are held to
// token.ValidSegment instead — the rule the launcher's own minter applies,
// re-checked in splitRunUID and in RTDBOutcomes before either builds a
// budget.PathOutcome — and that is the rule to reach for when a segment is
// what you have.
func validHeaderToken(s string) bool {
	return printableHeaderValue(s, maxHeaderTokenBytes)
}

// printableHeaderValue reports whether s is non-empty, at most max bytes, and
// printable ASCII with no space: the content of one header value, at the bound
// the caller needs. A JWT is held to it at maxTokenBytes (a presented token is
// one header value like any other, and an oversized one is refused at the door
// rather than parsed).
func printableHeaderValue(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Tried is one credential the service considered and did not use.
type Tried struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

// validateTried holds one tried entry to the rules of this contract. It is
// asked by both sides — Wire before it renders an answer, ReadExhaustedUntil
// before it hands one to the runner — because a tried entry is what a run's
// halt message is built from (ExhaustedUntil.Error): a name that is not one
// clean value, a tier that is not one of the three, or a reason outside the
// closed set would be carried into that sentence, which is text a human reads
// and a log line a machine keeps.
func validateTried(tr Tried) error {
	if !ValidCredentialName(tr.Name) {
		return errors.New("credsvc: a tried credential's name is not one credential name")
	}
	if !ValidTier(tr.Tier) {
		return errors.New("credsvc: a tried credential's tier is not one of A, B or C")
	}
	if !ValidReason(tr.Reason) {
		return errors.New("credsvc: a tried credential's reason is not one this contract carries")
	}
	return nil
}

// validateTriedList holds a whole tried list to the contract's rules: the count
// bound first (maxTriedEntries), then every entry by validateTried. It is asked
// by every reader and writer of a refusal body — Wire, ReadExhaustedUntil and
// ReadWireError — so "the same rules on both sides" is literal rather than
// claimed: a list this code would refuse to write is a list it refuses to read,
// and a general refusal body is held to exactly what the typed answer is (review
// finding N3: the two readers of a body did not agree, and the package doc said
// they did).
func validateTriedList(tried []Tried) error {
	if len(tried) > maxTriedEntries {
		return fmt.Errorf("credsvc: a refusal carries %d tried credentials, over this contract's bound of %d", len(tried), maxTriedEntries)
	}
	for _, tr := range tried {
		if err := validateTried(tr); err != nil {
			return err
		}
	}
	return nil
}

// WireError is the JSON body of every refusal the service writes. It carries
// names, tiers, reasons and an instant — never a value, and never anything
// the caller presented.
//
// Its type field is Error because the wire's key is "error", so the value
// cannot also have an Error method (Go allows a field and a method of the
// same name only one at a time). The value a caller of this package gets as an
// error is ExhaustedUntil, or a WireError's Error field read out by hand.
type WireError struct {
	Error   string  `json:"error"`
	Message string  `json:"message,omitempty"`
	ResetAt int64   `json:"reset_at,omitempty"`
	Tried   []Tried `json:"tried,omitempty"`
}

// WriteWireError writes the refusal: its type in the body, its status on the
// response. A message the caller reads names no credential value.
//
// A body this contract could not read back is not written as if it could
// (review finding N3, the write half): the tried list and the message are held
// to the reader's rules here too, so "the same rules on both sides" is kept by
// the writer and not only by the readers. An answer that fails them is answered
// as state_unavailable — the same fallback an unrenderable exhausted answer
// takes — because a body the runner would refuse is worse than a typed refusal
// it can act on.
func WriteWireError(w http.ResponseWriter, status int, e WireError) {
	body, err := json.Marshal(e)
	// Unreadable covers the unreachable marshal error (a WireError is strings
	// and an integer) and the two rules the reader holds this body to: a body
	// the other end of this contract would refuse is answered as the typed
	// refusal it can act on instead. A body the runner can read is better than
	// none.
	unreadable := err != nil || validateTriedList(e.Tried) != nil || len(e.Message) > maxRefusalMessageBytes
	if unreadable {
		body = []byte(`{"error":"` + ErrorStateUnavailable + `"}`)
		status = StatusUnavailable
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ReadWireError reads a refusal body. A 2xx is not a refusal, and a body that
// is not JSON or names no type is an error rather than an empty refusal.
//
// The body is decoded strictly (Convention 6: new wire formats decode
// strictly): a field this code does not know is refused rather than ignored,
// so a body that says something this release cannot read is not read as a
// body it can. That is a version-skew break by design — the two halves of
// this contract are this repository's code, and a field added to the refusal
// body is a change to this file.
//
// The message is bounded (review finding M3): it is the only field here that is
// free text, it reaches a log line and a halt message, and a body carrying more
// of it than any refusal this code writes is not a refusal this contract wrote.
// A body over the bound is refused rather than clipped, so no half of another
// writer's sentence is displayed as this service's words.
//
// It is also made one printable line (review finding): every other field of
// the body is held to a printable rule on both sides, and this one is another
// writer's text that a terminal and a log file will print — an escape sequence
// in it would repaint the terminal and a newline would forge a second line.
// The repository's rule for text from another writer is internal/safetext, and
// internal/budget/registry.go holds a credential name to it for the same
// reason. Sanitised rather than refused, unlike the length bound: the type and
// the fields are what the caller acts on, and the sentence is the writer's own
// rendering of them.
//
// The body is a caller's text — it arrives from the service, and nothing
// about it is trusted — so no error here repeats it: a decoder's own message
// names the byte it choked on, and this code says only that the body is not
// JSON.
func ReadWireError(status int, body []byte) (WireError, error) {
	if status/100 == 2 {
		return WireError{}, fmt.Errorf("credsvc: status %d is not a refusal", status)
	}
	var e WireError
	if err := decodeStrict(body, &e); err != nil {
		return WireError{}, errors.New("credsvc: the refusal body is not JSON")
	}
	if e.Error == "" {
		return WireError{}, errors.New("credsvc: the refusal body names no error type")
	}
	if len(e.Message) > maxRefusalMessageBytes {
		return WireError{}, errors.New("credsvc: the refusal body's message is longer than this contract's")
	}
	// The tried list is held to the same rules here as it is in the typed
	// answer and on the write side (review finding N3): a general refusal is a
	// WireError like any other, its entries reach the same halt message, and a
	// reader that skipped them would accept a body this code would never write —
	// which is exactly the claim the package doc makes and did not keep.
	if err := validateTriedList(e.Tried); err != nil {
		return WireError{}, err
	}
	e.Message = safetext.OneLine(e.Message)
	return e, nil
}

// decodeStrict decodes exactly one JSON value into out: a field this code does
// not know is an error, and so is anything after the value (Convention 6).
//
// "Anything after the value" is asked with Token, not with More: More reports
// whether the next non-space byte is not ']' or '}', so a trailer that *is* a
// closing brace or bracket — `{"error":"x"}}` — reads as nothing left. Token
// returns io.EOF exactly when only whitespace remains, which is the question
// this rule asks.
func decodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("credsvc: trailing data after the JSON value")
	}
	return nil
}

// ExhaustedUntil is C1.11's typed answer: no credential is usable before
// ResetAt. Tried is every credential that was considered, in the order it was
// considered, with why it was skipped.
type ExhaustedUntil struct {
	ResetAt time.Time
	Tried   []Tried
}

// Error is the sentence a run's halt message is built from. It names the
// instant and the credentials tried, never a value.
//
// It renders tried entries as validated — validateTriedList requires a name, a
// tier and a non-empty reason of every one — so every entry it is handed has a
// reason and the sentence always names it. The branch that omitted an empty
// reason was unreachable from any validated answer (review nit) and is gone; a
// caller that builds an ExhaustedUntil by hand is expected to validate it, as
// Wire and ReadExhaustedUntil do.
func (e ExhaustedUntil) Error() string {
	var b strings.Builder
	b.WriteString("no credential is usable before ")
	b.WriteString(e.ResetAt.UTC().Format(time.RFC3339))
	for i, tr := range e.Tried {
		if i == 0 {
			b.WriteString("; tried ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(tr.Name)
		b.WriteString(" (")
		b.WriteString(tr.Reason)
		b.WriteString(")")
	}
	return b.String()
}

// Wire renders the typed answer: the status, the headers and the body. now is
// the service's clock, so the courtesy Retry-After (whole seconds until the
// reset, which an intermediary may honour) is computed from it and not from a
// hidden clock. A reset that has already passed carries no Retry-After: it
// would be a lie to a proxy, and the runner reads the body's instant.
//
// An answer this code cannot write is an error rather than a body: a reset
// instant the reader would refuse is the instant the runner waits for, so
// rendering it would build an answer the other end of this contract cannot
// read (review finding M3), and a tried entry that fails the rules above would
// be carried into the run's halt message. A caller that gets an error here has
// nothing to send: it answers a state_unavailable refusal instead
// (WriteExhaustedUntil does exactly that).
//
// The instant is held to the reader's rule exactly — the field is written only
// when it is positive, so a value at or before the epoch is refused here
// rather than rendered, dropped by the field's omitempty and refused on the
// way back in (review finding).
func (e ExhaustedUntil) Wire(now time.Time) (status int, hdr http.Header, body []byte, err error) {
	if e.ResetAt.UnixMilli() <= 0 {
		return 0, nil, nil, errors.New("credsvc: the exhausted answer carries no reset instant")
	}
	if err := validateTriedList(e.Tried); err != nil {
		return 0, nil, nil, err
	}
	hdr = http.Header{}
	hdr.Set("Content-Type", contentTypeJSON)
	if d := e.ResetAt.Sub(now); d > 0 {
		hdr.Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
	}
	body, err = json.Marshal(WireError{
		Error:   ErrorExhaustedUntil,
		Message: clipRefusalMessage(e.Error()),
		ResetAt: e.ResetAt.UnixMilli(),
		Tried:   e.Tried,
	})
	if err != nil {
		return 0, nil, nil, fmt.Errorf("credsvc: encoding the exhausted answer: %w", err)
	}
	return StatusExhaustedUntil, hdr, body, nil
}

// clipRefusalMessage bounds the sentence the typed answer carries to what
// ReadWireError reads. The sentence is the writer's rendering of the fields
// beside it — the runner rebuilds it from them — so a rendering the other end
// of this contract would refuse would make the whole typed answer unreadable,
// which is the one answer the runner cannot do without (review finding). The
// tried list is what makes it long: maxTriedEntries lets a list of up to that
// many credentials through, and about forty of them at the rules' 200-byte name
// bound render past the refusal bound, so the clip is reachable and the list
// bound above does not make it dead. The structured Tried
// field is not clipped — it is where the runner reads the names from, and it is
// bounded by maxTriedEntries instead — and the cut is made on bytes, which is
// what the reader's bound counts.
func clipRefusalMessage(s string) string {
	if len(s) <= maxRefusalMessageBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxRefusalMessageBytes], "")
}

// WriteExhaustedUntil writes the typed answer to a client. An answer that
// cannot be written is answered as state_unavailable — a typed refusal the
// runner halts on (D143), never a wait for an instant nobody could read.
func WriteExhaustedUntil(w http.ResponseWriter, e ExhaustedUntil, now time.Time) {
	status, hdr, body, err := e.Wire(now)
	if err != nil {
		WriteWireError(w, StatusUnavailable, WireError{
			Error:   ErrorStateUnavailable,
			Message: "the service could not build the exhausted answer",
		})
		return
	}
	for k, v := range hdr {
		w.Header()[k] = v
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ReadExhaustedUntil reads the typed answer from a response's status and
// body. ok is false when the answer is a well formed body of another kind —
// the runner must not read a refusal as a wait — and an error when the body
// claims to be the typed answer and is not readable, or carries no reset
// instant, which is the one thing the runner waits for.
//
// Every tried entry is held to the rules Wire writes them by, for the same
// reason: the entries become the run's halt message. The body's Message field
// is deliberately not read: it is the writer's rendering of the fields, and a
// runner that rebuilt the sentence from validated fields instead cannot be
// made to carry text the contract did not check (review finding M3).
func ReadExhaustedUntil(status int, body []byte) (ExhaustedUntil, bool, error) {
	if status != StatusExhaustedUntil {
		return ExhaustedUntil{}, false, nil
	}
	e, err := ReadWireError(status, body)
	if err != nil {
		return ExhaustedUntil{}, false, err
	}
	if e.Error != ErrorExhaustedUntil {
		return ExhaustedUntil{}, false, nil
	}
	if e.ResetAt <= 0 {
		return ExhaustedUntil{}, false, errors.New("credsvc: the exhausted answer carries no reset instant")
	}
	if err := validateTriedList(e.Tried); err != nil {
		return ExhaustedUntil{}, false, err
	}
	return ExhaustedUntil{ResetAt: time.UnixMilli(e.ResetAt).UTC(), Tried: e.Tried}, true, nil
}

// Switch is one credential change inside the service: what the call was on,
// what answered, and why it moved. It is what the run's switch log records
// (C1.16) and what the spec asks to be logged per switch.
type Switch struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// SetCredentialHeaders records the tier and the credential's name on an
// answer. A tier it does not know or a name that is not one clean header
// value is an error: an answer that cannot carry them must not be sent as if
// it did — a wrong tier is a ledger error (notional against real money) and a
// name with a newline is a header injection. The error names the field, never
// the value (the no-echo rule): a name is text from another writer.
func SetCredentialHeaders(h http.Header, tier, name string) error {
	if !ValidTier(tier) {
		return errors.New("credsvc: the tier is not one of A, B or C")
	}
	if !ValidCredentialName(name) {
		return fmt.Errorf("credsvc: the credential name is not one clean header value (1 to %d printable ASCII bytes, no space)", maxCredentialNameBytes)
	}
	h.Set(HeaderTier, tier)
	h.Set(HeaderCredential, name)
	return nil
}

// CredentialHeaders reads the tier and the credential's name back, and holds
// them to the rules they were written by (review finding M2). ok is true only
// for a pair this contract could have sent: an answer that carries one of the
// two without the other, an unknown tier or a name that is not one clean value
// is an error rather than a pair — the tier settles a call against the caps
// (C1.16) and the name is what the run records (C1.19), so a caller that
// guessed at either would be reading an answer the service did not give. An
// answer that carries neither is (…, false, nil): most answers are not a
// credential's.
//
// A lone header needs no rule of its own: a missing tier is not one of A, B or
// C, and a missing name is not one clean header value, so each is refused by
// the rule for the half that is missing.
//
// A header the answer carries twice is refused, as the request side refuses
// one a call sent twice (oneHeader, review finding): the tier settles a call
// against the caps and the name is what the run records, so neither may be
// decided by the order an answer's headers happen to be in.
//
// The error names the field and never the value: both arrive as header text
// from another process.
func CredentialHeaders(h http.Header) (tier, name string, ok bool, err error) {
	tier, haveTier, err := responseHeader(h, HeaderTier)
	if err != nil {
		return "", "", false, err
	}
	name, haveName, err := responseHeader(h, HeaderCredential)
	if err != nil {
		return "", "", false, err
	}
	if !haveTier && !haveName {
		return "", "", false, nil
	}
	if !ValidTier(tier) {
		return "", "", false, errors.New("credsvc: the answer's tier is not one of A, B or C")
	}
	if !ValidCredentialName(name) {
		return "", "", false, errors.New("credsvc: the answer's credential name is not one clean header value")
	}
	return tier, name, true, nil
}

// SetCredentialOwnerHeader records the owner of the credential that answered
// (HeaderCredentialOwner, F16). The owner is the account id of the person the
// credential belongs to, which this service knows from the grammar of the name
// it served (credentialOwnedBy, cascade.go) and never from anything the call
// said, so it is held to the rule a launcher's account id is read by
// (validSlipOwner: digits only, at most maxLauncherSubDigits of them) — an
// answer that could not carry one must not be sent as if it did, and the error
// names the field and never its content (the no-echo rule: the value arrives
// as text from another process).
//
// It is a separate writer from SetCredentialHeaders and not a fourth argument
// of it, because the two facts have different writers: every served answer
// carries the tier and the name, and only a credential the launcher OWNS has
// an owner to carry. A caller that has no owner writes none, and the answer
// then says exactly what it said before this field existed.
func SetCredentialOwnerHeader(h http.Header, owner string) error {
	if !validSlipOwner(owner) {
		return fmt.Errorf("credsvc: the credential's owner is not one launcher's account id (1 to %d digits)", maxLauncherSubDigits)
	}
	h.Set(HeaderCredentialOwner, owner)
	return nil
}

// CredentialOwnerHeader reads the owner back, and holds it to the rule it was
// written by. present is false for an answer that carries none, which is every
// answer whose credential the launcher does not own. An owner that is not one
// launcher's account id is an error rather than a value: the run records it
// (F16), so a caller that guessed at one would be recording a fact the service
// did not state. The error names the field and never the value.
func CredentialOwnerHeader(h http.Header) (owner string, present bool, err error) {
	owner, present, err = responseHeader(h, HeaderCredentialOwner)
	if err != nil || !present {
		return "", present, err
	}
	if !validSlipOwner(owner) {
		return "", false, errors.New("credsvc: the answer's credential owner is not one launcher's account id")
	}
	return owner, true, nil
}

// responseHeader returns the single value of an answer's header. An answer
// that carries it more than once is refused for the same reason the request
// side refuses a header a call sent twice: there is nothing to pick between,
// and a reader that took the first would let whoever wrote the second decide
// what the first one means. present is false when the answer carries none.
// The error names the header, a constant of this contract, never a value.
func responseHeader(h http.Header, name string) (value string, present bool, err error) {
	values := h.Values(name)
	switch len(values) {
	case 1:
		return values[0], true, nil
	case 0:
		return "", false, nil
	}
	return "", false, fmt.Errorf("credsvc: the answer carries the %s header more than once", name)
}

// SetSwitchHeader records a switch on an answer. From is empty when the call
// started on the credential that answered it. The reason is one of the
// Reason* constants and a name is one clean header value: the record is what
// the run logs (C1.16), so neither is written unchecked. No error here repeats
// a name or a reason — they are values, and the record is text another writer
// produced.
func SetSwitchHeader(h http.Header, s Switch) error {
	if s.From != "" && !ValidCredentialName(s.From) {
		return errors.New("credsvc: the switch's from is not one credential name")
	}
	if !ValidCredentialName(s.To) {
		return errors.New("credsvc: the switch's to is not one credential name")
	}
	if !ValidReason(s.Reason) {
		return errors.New("credsvc: the switch's reason is not one this contract carries")
	}
	body, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("credsvc: encoding the switch: %w", err)
	}
	h.Set(HeaderSwitch, string(body))
	return nil
}

// SetProbeHeader records the tiers a probe was made on for a call (C1.12).
// The order is the order the probes were made and a tier is written once: two
// probes of one call on one tier are one ledger input, and the run settles the
// money, not the attempts.
//
// Every tier is held to ValidTier and the list to maxProbedTiers: the tier
// settles the probe as notional or as money (C1.16), so a value this contract
// does not know must not be carried. The error names the field and never a
// value, as everywhere in this file. An empty list writes no header — an
// answer with no probe is most answers.
func SetProbeHeader(h http.Header, tiers []string) error {
	seen := make(map[string]bool, len(tiers))
	out := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		if !ValidTier(tier) {
			return errors.New("credsvc: a probed tier is not one of A, B or C")
		}
		if seen[tier] {
			continue
		}
		seen[tier] = true
		out = append(out, tier)
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) > maxProbedTiers {
		return fmt.Errorf("credsvc: an answer's probe header carries %d tiers, over this contract's bound of %d", len(out), maxProbedTiers)
	}
	h.Set(HeaderProbe, strings.Join(out, ","))
	return nil
}

// ProbedTiers reads the tiers a probe was made on back, and holds them to the
// rules they were written by: each one a tier of this contract, no repeats and
// at most maxProbedTiers of them. ok is false for an answer that carries no
// probe header, which is most answers; an answer that carries one this code
// could not have written is an error rather than a list, because the tier is
// what the run settles the probe by (C1.16) and a reader that guessed at it
// would be settling a probe the service did not report.
//
// The error names the field and never the value: the list arrives as header
// text from another process.
func ProbedTiers(h http.Header) (tiers []string, ok bool, err error) {
	value, present, err := responseHeader(h, HeaderProbe)
	if err != nil {
		return nil, false, err
	}
	if !present {
		return nil, false, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxProbedTiers {
		return nil, false, fmt.Errorf("credsvc: the answer's probe header carries %d tiers, over this contract's bound of %d", len(parts), maxProbedTiers)
	}
	seen := make(map[string]bool, len(parts))
	for _, tier := range parts {
		if !ValidTier(tier) {
			return nil, false, errors.New("credsvc: the answer's probed tier is not one of A, B or C")
		}
		if seen[tier] {
			return nil, false, errors.New("credsvc: the answer's probe header names one tier twice, which this contract never writes")
		}
		seen[tier] = true
	}
	return parts, true, nil
}

// SwitchHeader reads a switch back. ok is false when the answer carries none;
// an error means the answer carries one this code cannot read, which the
// run's record must not silently drop.
//
// A switch that is read is held to the same rules as one that is written, and
// for the same reason: the record is logged, displayed and stored. From is
// checked where SetSwitchHeader checks it (empty when the call started on the
// credential that answered it), To likewise, and the reason is one of the
// closed set — a record without the why, or with a why this contract does not
// carry, is not a record of a switch. The header is decoded strictly
// (Convention 6) and an answer that carries it twice is refused as the request
// side refuses a header a call sent twice (review finding): the record is what
// the run logs, and which of the two is logged must not be decided by header
// order. No error here repeats a name or a reason: the header is text from
// another process, and a value in an error is a value in a log.
func SwitchHeader(h http.Header) (Switch, bool, error) {
	raw, present, err := responseHeader(h, HeaderSwitch)
	if err != nil {
		return Switch{}, false, err
	}
	if !present {
		return Switch{}, false, nil
	}
	if raw == "" {
		// A header that is present with no value is not "no switch": nothing in
		// this contract writes one, so it is a record this code cannot read and
		// must not silently drop (review nit). Refused, not read as absent.
		return Switch{}, false, errors.New("credsvc: the switch header is present but empty")
	}
	var s Switch
	if err := decodeStrict([]byte(raw), &s); err != nil {
		return Switch{}, false, errors.New("credsvc: the switch header is not one JSON record this code knows")
	}
	if s.From != "" && !ValidCredentialName(s.From) {
		return Switch{}, false, errors.New("credsvc: the switch header's from is not one credential name")
	}
	if !ValidCredentialName(s.To) {
		return Switch{}, false, errors.New("credsvc: the switch header's to is not one credential name")
	}
	if !ValidReason(s.Reason) {
		return Switch{}, false, errors.New("credsvc: the switch header's reason is not one this contract carries")
	}
	return s, true, nil
}

// RequestFromHTTP extracts what a call presents from its HTTP request: the
// caller's Google ID token from the Authorization header's bearer scheme, the
// run's identity from HeaderRunIdentity, and the job the run reports from
// HeaderRunJob (review finding m7). The contract owns this because the rules
// it holds to are the contract's — one value per header, the bearer scheme
// spelled as RFC 6750 spells it, and a job name that could be one — and
// because a server (C1.11) that made its own choices about these headers
// would be a second contract.
//
// It refuses rather than repairs. A header that arrives twice is refused:
// Header.Values reports every value the request carried, and a caller that
// sends two run identities has sent none this service can pick between. An
// Authorization header that is not "Bearer <token>" is refused, including
// "bearer" and "BEARER" — RFC 7235 makes the scheme case-insensitive and this
// code does not guess which spelling the caller meant. A token that is empty,
// over maxTokenBytes, or not one clean header value is refused here rather
// than parsed, and a job name that is not one (ValidJobName) likewise.
//
// It does not check the tokens themselves: that is Verify's, which is the only
// place a presented token is trusted.
func RequestFromHTTP(r *http.Request) (Request, error) {
	bearer, err := bearerFromHTTP(r)
	if err != nil {
		return Request{}, err
	}
	run, err := oneHeader(r, HeaderRunIdentity)
	if err != nil {
		return Request{}, err
	}
	job, err := oneHeader(r, HeaderRunJob)
	if err != nil {
		return Request{}, err
	}
	if !printableHeaderValue(run, maxTokenBytes) {
		return Request{}, fmt.Errorf("credsvc: the %s header is not one token", HeaderRunIdentity)
	}
	if !ValidJobName(job) {
		return Request{}, fmt.Errorf("credsvc: the %s header is not a Cloud Run job name", HeaderRunJob)
	}
	mode, _, err := optionalHeader(r, HeaderAuthMode)
	if err != nil {
		return Request{}, err
	}
	account, _, err := optionalHeader(r, HeaderAuthAccount)
	if err != nil {
		return Request{}, err
	}
	if mode != "" && !ValidAuthMode(mode) {
		return Request{}, fmt.Errorf("credsvc: the %s header is not one of the agent auths", HeaderAuthMode)
	}
	if account != "" && !ValidAuthAccount(account) {
		return Request{}, fmt.Errorf("credsvc: the %s header is not a Claude account name", HeaderAuthAccount)
	}
	return Request{RunIDToken: run, JobIDToken: bearer, Job: job, AuthMode: mode, AuthAccount: account}, nil
}

// optionalHeader returns the single value of a request header the call may
// leave out, and it holds the one rule oneHeader holds that matters for an
// absent header: a call that sent it twice is refused rather than answered from
// whichever value the map happened to keep. An auth declaration is exactly the
// kind of header a caller would try to send twice — the boundary is decided
// from it, so a call carrying two of them is a call trying to have both
// answers, and the refusal names the header and never a value.
//
// An empty value is refused rather than read as absent: a caller that sent the
// header has declared something, and "" is not a value either of these headers
// can carry — reading it as "declared nothing" would answer a call that asked
// two contradictory things with the one reading that suits it.
func optionalHeader(r *http.Request, name string) (value string, present bool, err error) {
	values := r.Header.Values(name)
	switch len(values) {
	case 0:
		return "", false, nil
	case 1:
		if values[0] == "" {
			return "", false, fmt.Errorf("credsvc: the call carries an empty %s header", name)
		}
		return values[0], true, nil
	}
	return "", false, fmt.Errorf("credsvc: the call carries the %s header more than once", name)
}

// bearerFromHTTP reads the one bearer token a call presents in Authorization,
// and it is that header's whole rule: exactly one value, the scheme spelled as
// RFC 6750 spells it, and a token that is non-empty, within maxTokenBytes and
// one clean header value. The error names the rule and never the value.
//
// It is written once because two callers read this header and must read it the
// same way: RequestFromHTTP, for the job token every other route is admitted
// by, and the verify route (verdict.go), which is answered ahead of that parse
// and so reads its verifier's token on its own — a verifier holds no run
// identity, so the parse that reads three headers cannot run for it at all.
// Two spellings of this rule is how they would drift: the stricter one would be
// the rule and the other a way past it.
func bearerFromHTTP(r *http.Request) (string, error) {
	auth, err := oneHeader(r, "Authorization")
	if err != nil {
		return "", err
	}
	bearer, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return "", errors.New("credsvc: the Authorization header is not a bearer token")
	}
	if !printableHeaderValue(bearer, maxTokenBytes) {
		return "", errors.New("credsvc: the Authorization header's token is not one token")
	}
	return bearer, nil
}

// personBearerFromHTTP is bearerFromHTTP for a route whose caller is a person:
// the token comes from HeaderPersonIdentity when the call sends it and from
// Authorization otherwise, so a caller that reaches the service without a front
// end in between (a test, a local run) is served as before.
func personBearerFromHTTP(r *http.Request) (string, error) {
	if len(r.Header.Values(HeaderPersonIdentity)) == 0 {
		return bearerFromHTTP(r)
	}
	tok, err := oneHeader(r, HeaderPersonIdentity)
	if err != nil {
		return "", err
	}
	if !printableHeaderValue(tok, maxTokenBytes) {
		return "", errors.New("credsvc: the " + HeaderPersonIdentity + " header's token is not one token")
	}
	return tok, nil
}

// oneHeader returns the single value of a request header, refusing a header
// the call did not send and one it sent more than once. The error names the
// header, which is a constant of this contract and never a caller's value.
func oneHeader(r *http.Request, name string) (string, error) {
	values := r.Header.Values(name)
	switch len(values) {
	case 1:
		return values[0], nil
	case 0:
		return "", fmt.Errorf("credsvc: the call carries no %s header", name)
	}
	return "", fmt.Errorf("credsvc: the call carries the %s header more than once", name)
}
