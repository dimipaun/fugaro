package credsvc

// The workflow-secrets route (B3.2, G18, G19): how one of a repository's
// workflow secrets reaches the job that declares it when the job does not mount
// it.
//
// # What it is for
//
// A workflow's `secrets:` are stored once and mounted by the job, and S3's flip
// (B3.8) takes the mount away for a repository the switch serves: from then on
// the job holds no value, and the runner asks for it here, at bootstrap, before
// the agent starts (B3.4). No value moves and no name changes (F33, D1267): this
// route reads the very container the job's own mount read, which is the answer
// `fugaro update` computed and wrote down as a record (B3.1,
// creds/workflowsecrets/<slug>) — the repository's own copy when it has an
// enabled version, the project-wide copy when the operator's allow-list admits
// this repository to one, a choice the service cannot make for itself because
// it cannot read an operator's local config (D2073).
//
// # The two versions
//
// Version 1 is that call: it names the VARIABLE, and the record above decides
// which container that variable is read from — the repository's own copy or,
// under D2313, the project's.
//
// Version 2 (D3324, D3328, D3333) names the LOGICAL SECRET instead, and the
// container is the project's own `fugaro_p_<name>` — the grammar
// internal/secretname states, and the one container every repository of the
// project fetches the name from. Two things admit the call, and both are the
// service's own:
//
//   - the project RECORD (creds.PathProjectSecrets, IS3's codec), which says
//     which repositories may fetch the name. It is the owner's own list, written
//     by `fugaro secrets allow` and seeded by `fugaro update` from the operator's
//     local block (F22, F23): a repository's `fugaro.yaml` can never become a
//     grant. A name the record does not admit the calling repository to is
//     refused with the command that fixes it, and an ABSENT record is a refusal
//     too — never "every name is allowed" (G19).
//   - the MARKER (F18, G2), the label the intake writes on a container
//     `fugaro secrets shared set` made. `fugaro_p_` is also the grammar of the
//     project's model credentials, and a workflow secret named like one of those
//     would otherwise be served out of the model key's own container. The marker
//     is what tells the two classes apart, and it is read through the lister
//     grant the deployment already holds (secretmanager.secrets.list, filtered
//     by name — no new grant, F9). A container without it is refused, not read.
//
// Version 2 is refused while `config.RepoSecretsOff` is false (G9, F24): with
// repository-level copies in force, a project copy would shadow a repository's
// own value, so the door that keeps that from happening has to be shut before
// this version serves anything.
//
// # Where the names come from (D3459)
//
// The repository's own `fugaro.yaml` at its DEFAULT branch is the authority for
// which names exist, on both versions: it is read at call time through the code
// host (RepoFileReader), with the credentials this service already holds — the
// bundle's App key, minted as the App ID the call carries, or the bundle's
// Bitbucket credential — and the variables and names it declares are the ones a
// call is answered with. Nothing of the call names a repository, a workflow or a
// ref: the repository is the verified run identity's, the workflow is the job
// account's (which is the launcher-written task's, JobAccount), and the ref is
// the default branch and never the branch the run itself pushes.
//
// The record above is then a FALLBACK, and for one state only: a code host that
// could not be reached. A host that answered — with a file this release cannot
// read, with a file that does not declare what the call named, or with a 404 —
// is a refusal and never the fallback, because a record read beside a file that
// changed would serve a name the repository's own file no longer writes. The
// fallback goes when the record does (UO7b), and until then it is what keeps a
// repository whose host is briefly unreachable working.
//
// The names stay bounded exactly as version 2's always were: a name the file
// declares is served only from the project container the grammar builds and only
// through `projectSecretReadable` (the owner's allow-list and the marker), on
// BOTH versions (F13, D3328, D3333).
//
// # Who it serves, and who it refuses
//
// The call is a per-repository job's, and the repository, the workflow and the
// container are all decided by the service:
//
//   - the REPOSITORY is the calling job account's own binding. Version 1's
//     record is keyed by the slug (JobAccount.Slug, creds.PathWorkflowSecrets)
//     and the pairing in ServeHTTP has already held that slug equal to the
//     verified run identity's; version 2's allow-list is checked against
//     JobAccount.Repo, the same repository in the spelling the published policy
//     speaks. Neither is a field of the call.
//   - the WORKFLOW is JobAccount.Workflow, the workflow whose job this account
//     is (JobAccountsFor built it from the same pair the deployer derived the
//     account's name from), never X-Fugaro-Run-Job, which is the caller's claim
//     and decides nothing anywhere in this service — one workflow's job is never
//     served another's secrets, which is what G19 is.
//   - the CONTAINER is the record's entry for the variable the call names
//     (version 1), or the project container the name builds (version 2), and
//     nothing a caller sends can reach a container the record does not hold.
//
// Everything else is refused, and refused by DEFAULT: a shared project job and
// a review job are turned away here on EITHER version (their accounts name no
// one workflow's job, report.go's synthesised rows), and a launcher and a person
// never arrive — neither holds a run identity, so RequestFromHTTP refuses them
// before this file is reached (F21: the class gate is version 2's as much as
// version 1's). An absent record, a record this release does not know the
// version of, a workflow the record does not hold, a variable the record does
// not hold under it, a name the project record does not admit, a container with
// no marker and a container that cannot be read are each a typed refusal with no
// fallback of any kind. A fallback here would be the whole point of the route
// inverted: the service would answer a value the job did not ask for, or one the
// operator's own answer for this repository never named.
//
// # No value anywhere but the answer
//
// The value this route serves is a credential, and the answer is the only place
// it may appear (G18). It is read out of the container with the service's own
// account at call time, exactly as the bundle's values are (perSecrets: the one
// Secret Manager client and project the deployment already reads through), and
// it is never put in a log line, an audit line or an error: every refusal
// sentence below is written here, names the field that was wrong and not what it
// held, and the Secret Manager failure keeps its own class as the cause of a
// refusal whose message is this file's. The answer itself is written no-store
// (writeReadAnswer), because an intermediary that cached a workflow secret would
// be handing the next caller a credential this service did not serve it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// The workflow-secret marker (F18, G2, IS4): the label the intake writes on a
// container `fugaro secrets shared set` made, and the value it writes it with.
// They are intake.LabelWorkflowSecret and intake.WorkflowSecretMarker, spelled
// here rather than imported because internal/intake consumes this package
// (handlers.go, server.go), so importing it back would be a cycle.
// TestWorkflowSecretMarkerIsTheIntakesLabel, in the external test file beside
// this one, reads intake's own constants and asks this package to agree.
//
// Nothing else may spell them: a second literal is a marker this service would
// stop recognising the day the intake's changed, and the failure is silent in
// the worst direction — an unmarked container is refused, so the drift would
// take a working installation's workflow secrets away.
const (
	workflowSecretLabel  = "fugaro_workflow_secret"
	workflowSecretMarker = "true"
)

// ErrWorkflowSecretsBadRequest is a call whose body is not this contract's: not
// one JSON object of one field, or an env that is not a name a job could mount a
// secret under. It is the sentinel the reader wraps, so a test can tell this
// refusal from another without reading a sentence.
var ErrWorkflowSecretsBadRequest = errors.New("credsvc: the workflow-secrets call is not one this service reads")

// readWorkflowSecretsRequest reads a workflow-secrets call's body. It is bounded
// and decoded strictly (Convention 6: a field this code does not know is
// refused, not ignored), and no error here repeats what it read: the body is the
// caller's text, and a decoder's own message names the byte it choked on — so
// every sentence this returns is one of this file's own (github.go's
// readMintRequest, the reader this one is shaped after).
func readWorkflowSecretsRequest(r *http.Request) (WorkflowSecretsRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWorkflowSecretsRequestBytes+1))
	if err != nil {
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the body could not be read", ErrWorkflowSecretsBadRequest)
	}
	if len(body) > maxWorkflowSecretsRequestBytes {
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the body is larger than the %d bytes this service accepts", ErrWorkflowSecretsBadRequest, maxWorkflowSecretsRequestBytes)
	}
	var req WorkflowSecretsRequest
	if err := decodeStrict(body, &req); err != nil {
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the body is not one JSON object of one field", ErrWorkflowSecretsBadRequest)
	}
	if err := req.Validate(); err != nil {
		// The contract's own shape rule for whichever version the call declares
		// (wire.go's Validate): a name no version could have carried, a field of
		// the OTHER version, and a version this release does not know are each
		// refused BEFORE the record is read. The sentence is Validate's own —
		// it names the field that was wrong and never what it held.
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: %s", ErrWorkflowSecretsBadRequest, err)
	}
	return req, nil
}

// secretIDRE is Secret Manager's own rule for a secret ID: at most 255 of
// [A-Za-z0-9_-] (internal/backend/gcp/names.go states it beside the derivations
// that build these IDs).
//
// It is checked here rather than trusted, because the ID this route reads is the
// record's text and the record is a document the service reads rather than code
// it holds: a record whose entry were not a secret ID would otherwise be
// concatenated into a Secret Manager resource path, and a service that built a
// resource name out of a document is one an operator's mistake — or a writer's —
// could point at something else. A record that fails this is a record this
// release does not serve, which is the fail-closed answer.
var secretIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// fugaroYAMLPath is the repository's own configuration file, and the one this
// route reads a workflow's `secrets:` out of (D3459). It is the same path the
// CLI reads a repository's config from, spelled here rather than imported
// because internal/repofiles consumes internal/config and this package reads
// the file through the code host, not through that reader.
const fugaroYAMLPath = "fugaro.yaml"

// repoFileBudget bounds one repository-file read, the way workflowSecretsBudget
// bounds the runner's fetch: the read is on the path of a call whose caller has
// already waited for two identity verifications, and a code host that has not
// answered in this long is the state the record is the fallback for.
const repoFileBudget = 20 * time.Second

// RepoFileQuery names one file of one repository at one ref, as the service
// asks the code host for it (UO7, D3459).
//
// It carries no credential: what signs the read is the bundle's own App key or
// Bitbucket token, held by the reader below and by nothing a caller can reach.
// AppID is the one thing the CALLER supplies, and it is not a secret
// (githubread.go): a GitHub read is signed as the App the call names, and the
// service's environment does not hold that ID.
type RepoFileQuery struct {
	// Provider is the git provider kind the repository is held at
	// (gitprov.KindGitHub or gitprov.KindBitbucket), read from the verified
	// job account's canonical repository name.
	Provider string
	// Repo is the canonical provider/owner/name of the repository the file is
	// read for: the calling job account's own, never text a call carries.
	Repo string
	// AppID is the GitHub App ID the read is signed as. It is empty for a
	// Bitbucket repository, where no App signs anything.
	AppID string
	// Path is the file to read.
	Path string
	// Ref is the ref to read it at. The empty string is the repository's
	// DEFAULT branch, which is the only ref this route ever names (D3459): the
	// branch a run itself pushes is not one its own secrets are declared at.
	Ref string
}

// ErrRepoFileUnreachable is the one failure of a repository-file read that a
// caller may fall back from: the code host could not be reached at all — a
// transport failure, a timeout, or a 5xx. Every other failure is an ANSWER (a
// 4xx, a file that is not there, a body this release cannot read) and is a
// refusal, never the fallback: a record read beside a file that changed would
// serve a name the repository's own file no longer writes (D3459).
var ErrRepoFileUnreachable = errors.New("credsvc: the code host could not be reached")

// errRepoFileAppID is a GitHub read whose call carries no App ID this code can
// sign as. It is not ErrRepoFileUnreachable: the host is fine and the CALL is
// one this route cannot perform, so it is refused as a request and never
// answered out of the record.
var errRepoFileAppID = errors.New("credsvc: a GitHub repository's file is read as the App ID the call carries, and this call carries none")

// RepoFileReader reads one file of one repository through the code host, with
// the credentials the service already holds (U4, D3459). It is an interface so
// a test can stand the code host in for, and repoFiles is the only
// implementation the service deploys.
type RepoFileReader interface {
	Read(ctx context.Context, q RepoFileQuery) ([]byte, error)
}

// repoFiles reads a repository's own file through the host it is held at: a
// GitHub App installation token minted from the bundle's key, or the bundle's
// Bitbucket credential. It is the launcher's own read (D915) moved into the
// service, and it adds no access: both credentials are the ones the runs'
// git calls already use, and the token a GitHub read is made with asks for
// github.BuildTokenPermissions — contents and metadata, read — which is the
// narrowest set that reads a file and reaches nothing that writes.
type repoFiles struct {
	bundles *BundleLoader
	client  *http.Client
	// baseURL is the API root a read goes to. It is empty in every deployment
	// — a root a caller could name would be the App credential's destination
	// chosen by the caller (githubread.go) — and a test sets it to point the
	// read at a host of its own.
	baseURL string
}

// NewRepoFiles returns the reader the credentials service deploys. A nil
// client is the routes' own: the provider adapters fall back to a client with
// a timeout (httpjson).
func NewRepoFiles(bundles *BundleLoader, client *http.Client) RepoFileReader {
	return &repoFiles{bundles: bundles, client: client}
}

// Read reads one file of one repository, and it answers ErrRepoFileUnreachable
// for the one failure a caller may fall back from and nothing else.
//
// The provider is dispatched on the QUERY's kind, which the verified job
// account named, so a read can never be sent to a host the repository is not
// held at. Nothing here reads a credential out of the query: the App ID is the
// caller's own (and is not a secret), and the key and the token are the
// bundle's.
func (r *repoFiles) Read(ctx context.Context, q RepoFileQuery) ([]byte, error) {
	if r.bundles == nil || r.bundles.Source == nil {
		// A service with no bundle cannot reach any host, and this is a
		// deployment that is wrong rather than a host that is down: it is
		// answered as a failure and never as the fallback, which would serve a
		// value out of a record this release is retiring.
		return nil, errors.New("credsvc: this service holds no credentials bundle, so it cannot read a repository's own file")
	}
	b, err := r.bundles.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("credsvc: the credentials bundle could not be read: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, repoFileBudget)
	defer cancel()
	var contents gitprov.ContentsReader
	switch q.Provider {
	case gitprov.KindGitHub:
		owner, name, err := mintRepository(q.Repo)
		if err != nil {
			return nil, err
		}
		if !appIDRE.MatchString(q.AppID) {
			return nil, errRepoFileAppID
		}
		key, err := appKey(b)
		if err != nil {
			return nil, err
		}
		token, _, err := github.MintInstallationToken(cctx, github.Options{
			Owner: owner, Repo: name, AppID: q.AppID, PrivateKey: key,
			BaseURL: r.baseURL, HTTP: r.client, Now: time.Now,
		}, github.BuildTokenPermissions())
		if err != nil {
			return nil, hostAnswer(err)
		}
		p, err := github.New(github.Options{
			Owner: owner, Repo: name, AppID: q.AppID, ServiceHeldKey: true,
			BaseURL: r.baseURL, HTTP: r.client,
		})
		if err != nil {
			return nil, err
		}
		p.UseServiceTokens(staticToken(token))
		contents = p
	case gitprov.KindBitbucket:
		workspace, name, err := splitCanonicalRepo(q.Repo)
		if err != nil {
			return nil, err
		}
		entry, ok := b.BitbucketToken()
		if !ok || entry.Value() == "" {
			return nil, errors.New("credsvc: the credentials bundle holds no Bitbucket credential, so a Bitbucket repository's own file cannot be read")
		}
		p, err := bitbucket.New(bitbucket.Options{
			Workspace: workspace, Slug: name, Token: entry.Value(),
			BaseURL: r.baseURL, HTTP: r.client,
		})
		if err != nil {
			return nil, err
		}
		contents = p
	default:
		return nil, fmt.Errorf("credsvc: a repository on the host %q has no file this service can read", q.Provider)
	}
	data, err := contents.Contents(cctx, q.Path, q.Ref)
	if err != nil {
		return nil, hostAnswer(err)
	}
	return data, nil
}

// staticToken is a gitprov.TokenSource over one already-minted token: the read
// is one call, so the token it was minted for is the token it uses.
type staticToken string

func (t staticToken) Token(context.Context, time.Duration) (string, time.Time, error) {
	return string(t), time.Time{}, nil
}

// hostAnswer classifies a failed read by whether the host was HEARD FROM.
//
// The two are not the same failure and must not be read as one. A host that
// ANSWERED — any status below 500, or a reply this release cannot read as a
// file (a directory's listing, an empty file, one over the ceiling) — has
// settled the question the read asked, and its answer is a refusal, never the
// fallback: a record consulted beside a file that changed would serve a name
// the repository's own file no longer writes (D3459). Only a host that was
// never heard from at all — a transport failure, a timeout, a call the caller
// gave up on, a 5xx — is the one state the record is the fallback for.
func hostAnswer(err error) error {
	if hostAnswered(err) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrRepoFileUnreachable, err)
}

// hostAnswered reports whether a failed read carries the host's own answer.
//
// It asks for the OTHER state positively, because the fallback is the state
// that must not be reached by accident: a read is a host this code never heard
// from only where the failure says so — a 5xx, or a failure that never reached
// the host at all (net.Error, which http.Client wraps every one of them in: a
// refused connection, a DNS failure, a timeout) — and every failure this code
// cannot place is an answer, and so a refusal.
//
// The status is read as httpjson's StatusError, which is what BOTH adapters
// report a reply outside 2xx as: GitHub's through its own client, and
// Bitbucket's file read through the same type (contents.go's srcFile). An
// adapter that reported an answer some other way would be read as unreachable
// here, which is why the two are held to it.
func hostAnswered(err error) bool {
	var se *httpjson.StatusError
	if errors.As(err, &se) {
		return se.Status < http.StatusInternalServerError
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return false
	}
	return !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
}

// The refusals of this route, as sentences written here. Each one names the
// field that was wrong and never what the call sent, what the record holds or
// which container was read: a caller learns that it was refused, and an agent
// holding the run identity learns nothing it could enumerate with — which is why
// the variable it asked for is never echoed back either.
const (
	// workflowSecretNotThisJob answers a caller class this route does not serve
	// (G19). It is a verified caller refused at a route, and it is recorded once
	// like every other one.
	workflowSecretNotThisJob = "a workflow secret is served only to the per-repository job of the workflow it is recorded for"
	// workflowSecretNoReader answers a service with no reader for the record at
	// all: the route refuses rather than serving nothing, exactly as the sign
	// route does with no mark store.
	workflowSecretNoReader = "this service has no reader for the workflow secrets record, so it serves no workflow secret"
	// workflowSecretNoRecord answers a repository `fugaro update` has not
	// recorded: an installation that has not run the release that writes these,
	// or one whose write has not reached this repository yet. It is not an empty
	// answer (G19): a call served nothing would leave the runner without the
	// variable it needs and with no reason it could act on.
	workflowSecretNoRecord = "no workflow secrets record is written for the calling job's repository, so this service serves no workflow secret for it"
	// workflowSecretUnreadable answers a record that could not be read, or one
	// whose entry is not a container this service may read: a record half
	// understood could name a container the repository never mounted.
	workflowSecretUnreadable = "the workflow secrets record could not be read"
	// workflowSecretOtherWorkflow answers a record that holds nothing for the
	// calling job's workflow. The sentence says no more than that: which
	// workflows the record DOES hold, and which variables, are the repository's
	// own business, and one workflow's job is not told them (G19).
	workflowSecretOtherWorkflow = "the calling job's workflow is not one this repository's record holds a secret for"
	// workflowSecretNoSuchEnv answers a variable the calling job's workflow is
	// not recorded under. It is the fail-closed half of the record's being the
	// authority: the service serves what the record says the job mounts, and
	// never a name the caller supplied that the record does not hold (G19).
	workflowSecretNoSuchEnv = "the calling job's workflow holds no secret under the variable this call names"
	// workflowSecretUnreadableValue answers a container that could not be read,
	// is no longer there, or holds a version with no value. There is no fallback
	// from it: a service that answered the bundle's value, or an empty one,
	// would be serving a credential the repository's own record does not name
	// (D143, G19).
	workflowSecretUnreadableValue = "the workflow secret could not be read"
	// workflowSecretUnaudited answers a service whose audit line could not be
	// written: the act is not recorded, so it is not made — the rule the mint's
	// answer keeps for the same reason.
	workflowSecretUnaudited = "the workflow secret could not be audited, so it was not served"
	// workflowSecretAnswerFailed answers a service that could not build its own
	// answer. The branch is unreachable through a real call — the answer is two
	// strings — and it is kept rather than dropped for the mint's own reason:
	// the alternative to answering is a panic inside the one act that hands out
	// a credential, and a call that reached it must be refused.
	workflowSecretAnswerFailed = "the service could not build the workflow secret's answer"
	// workflowSecretNoProjectRecord answers a service with no reader for the
	// project record at all: version 2 refuses rather than serving nothing,
	// exactly as version 1 does with no reader for its own record.
	workflowSecretNoProjectRecord = "this service has no reader for the project's shared-credential record, so it serves no workflow secret by name"
	// workflowSecretProjectUnreadable answers a project record that could not be
	// read, or one written by a release this one does not know: a record half
	// understood could admit a repository the owner never listed (G19).
	workflowSecretProjectUnreadable = "the project's shared-credential record could not be read"
	// workflowSecretNotAllowed answers a name the project record does not admit
	// the calling repository to (D3328). It names the fix, because the fix is
	// the owner's own act and a run can print it: which repositories ARE listed
	// is the owner's business and is not in this sentence (G18).
	workflowSecretNotAllowed = "this repository may not fetch that workflow secret: an operator runs `fugaro secrets allow NAME owner/repo` to admit it"
	// workflowSecretUnmarked answers a container that is there but is not one
	// this service serves a workflow secret from (F18, G2): no marker, so it is
	// the project's own credential of that name rather than a workflow secret's.
	// The fix is the operator's, and the sentence names it.
	workflowSecretUnmarked = "that container is not marked as a workflow secret's, so this service does not serve it: an operator runs `fugaro secrets shared set NAME`"
	// workflowSecretMarkerUnreadable answers a marker read that FAILED (F18,
	// G2): the container may hold a workflow secret and the service could not
	// tell, which is the state class and not the unmarked refusal above — the
	// two have two different fixes, and the unmarked one would send an operator
	// to rewrite a container that is already right.
	workflowSecretMarkerUnreadable = "the workflow secret's marker could not be read, so this service does not serve it"
	// workflowSecretFlipPending answers version 2 while `config.RepoSecretsOff`
	// is false (G9, F24): a repository's own copies are still in force, so a
	// project copy would shadow them. Nothing a caller can do; the sentence
	// says so rather than sending a run looking for a secret.
	workflowSecretFlipPending = "this installation still serves repository-level workflow secrets, so the service does not serve one by name yet"
	// workflowSecretFileUnreadable answers a repository's own fugaro.yaml that
	// the host answered with and this release could not read (D3459). It is a
	// refusal and NOT the record fallback: the file is there and it is the
	// authority, so a record read beside it would serve a name the repository's
	// own file no longer writes.
	workflowSecretFileUnreadable = "the repository's own fugaro.yaml could not be read, so this service serves no workflow secret for it"
	// workflowSecretNoAppID answers a GitHub repository's call that carries no
	// App ID (UO7, D3459): the read of the repository's own file is signed as
	// the App, and the App ID is the one thing a caller supplies to that mint
	// (githubread.go). It is a refused REQUEST — the caller can fix it — and
	// never the record fallback, which would answer a call this route could not
	// perform out of a record this release is retiring.
	workflowSecretNoAppID = "the call carries no GitHub App ID, which is what the repository's own fugaro.yaml is read as"
)

// WorkflowSecretRecords reads the record `fugaro update` writes at
// creds/workflowsecrets/<slug> (B3.1). It is an interface so a test can serve
// one without a database, and RTDBWorkflowSecrets is the only implementation the
// service deploys.
//
// found=false is a repository no update has recorded, which is not an error: the
// caller decides what an absent record means, and for this route that is the
// refusal above (fail closed, G19).
type WorkflowSecretRecords interface {
	Read(ctx context.Context, slug string) (creds.WorkflowSecrets, bool, error)
}

// RTDBWorkflowSecrets reads the record as the service's own account, over the
// database client the rest of the service reads its nodes with — the rules deny
// every run identity creds/ (O1, D136), so the node's two readers are the
// operator's CLI, which writes it, and this service.
//
// It reads and never writes: creds.DB is the shape creds.ReadWorkflowSecrets
// takes, and this type holds no method that writes a record. The record is
// `fugaro update`'s to write and nobody else's (B3.1).
type RTDBWorkflowSecrets struct {
	// DB is the database; *rtdb.Client is one.
	DB creds.DB
}

// Read reads one repository's record. A nil DB answers as a failed read rather
// than as an absent record: an installation whose database is not wired is a
// deployment that is wrong, and the two are one refusal to a run
// (state_unavailable) and two different incidents to an operator.
func (r RTDBWorkflowSecrets) Read(ctx context.Context, slug string) (creds.WorkflowSecrets, bool, error) {
	if r.DB == nil {
		return creds.WorkflowSecrets{}, false, errors.New("credsvc: the service has no database client to read a workflow secrets record with")
	}
	return creds.ReadWorkflowSecrets(ctx, r.DB, slug)
}

// ProjectSecretRecords reads the project record at creds/projectsecrets (IS3,
// D3328, D3333): the list that says which repositories may fetch a
// project-shared workflow secret, by name. It is a reader of its own beside
// WorkflowSecretRecords because the two records answer two questions — which
// container one repository's variable is read from, and whether a NAME is
// allowed to a repository at all — and an installation can hold one and not the
// other while the migration between them is in flight.
//
// found=false is an installation no `fugaro secrets allow` has written yet,
// which is not an error: the caller decides what an absent record means, and for
// this route that is the refusal below (fail closed, G19).
type ProjectSecretRecords interface {
	Read(ctx context.Context) (creds.ProjectSecrets, bool, error)
}

// RTDBProjectSecrets reads the project record over the same database client the
// rest of the service reads its nodes with — the rules deny every run identity
// creds/ (O1, D136), so its two readers are the operator's CLI, which writes it,
// and this service.
//
// It reads and never writes: the record is `fugaro secrets allow`'s to write
// and nobody else's (D3328, F14).
type RTDBProjectSecrets struct {
	// DB is the database; *rtdb.Client is one.
	DB creds.DB
}

// Read reads the project record. A nil DB answers as a failed read rather than
// as an absent record, for RTDBWorkflowSecrets.Read's reason: an installation
// whose database is not wired is a deployment that is wrong, and the two are one
// refusal to a run (state_unavailable) and two different incidents to an
// operator.
func (r RTDBProjectSecrets) Read(ctx context.Context) (creds.ProjectSecrets, bool, error) {
	if r.DB == nil {
		return creds.ProjectSecrets{}, false, errors.New("credsvc: the service has no database client to read the project record with")
	}
	return creds.ReadProjectSecrets(ctx, r.DB)
}

// projectSecretRefusal is one refusal of the project fence: the status and typed
// code a route answers with, the sentence it prints, and the cause the
// operator's own log line carries (nil where there is none).
type projectSecretRefusal struct {
	status  int
	code    string
	message string
	cause   error
}

// projectSecretReadable is the fence a `fugaro_p_<name>` container is served
// through, on BOTH routes that can read one (F13, D3328, D3333, G2).
//
// It answers the reader the value is to be read with and a nil refusal when the
// read may go ahead, and a nil reader with the refusal to answer the caller with
// when it may not — so the two routes differ in how they record and word a
// refusal and not in what the fence decides, and no caller has to ask a second
// time whether there is a reader at all. Every input is the service's own: name
// and container are the call's, held to the grammar before this is reached, and
// repo is the VERIFIED caller's (JobAccount.Repo or RepoBuildAccount.Repo, never
// a field of a call).
//
// The order is fail-closed, and each step is answered before the next is
// reached: the reader, the record, the allow-list, the marker read, the marker.
// A name the record does not admit the repository to is refused rather than
// served (G19), an absent record is refused rather than read as "everything is
// allowed", and a container whose marker could not be read is refused rather
// than treated as unmarked — the two have two different fixes.
func (s *Server) projectSecretReadable(ctx context.Context, name, container, repo string) (PerSecretSource, *projectSecretRefusal) {
	source := s.perSecrets()
	if source == nil || source.Source == nil {
		// No Secret Manager client and no project: the same refusal an
		// unreadable container gets, because the two are one state to a caller —
		// nothing can be read — and neither is a fallback.
		return nil, &projectSecretRefusal{StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue, nil}
	}
	if s.cfg.ProjectSecrets == nil {
		return nil, &projectSecretRefusal{StatusUnavailable, ErrorStateUnavailable, workflowSecretNoProjectRecord, nil}
	}
	rec, found, err := s.cfg.ProjectSecrets.Read(ctx)
	if err != nil {
		return nil, &projectSecretRefusal{StatusUnavailable, ErrorStateUnavailable, workflowSecretProjectUnreadable, labelRead(readProjectSecrets, err)}
	}
	if !found || !rec.Allows(name, ownerName(repo)) {
		// Not found is the same refusal as not admitted: a record that is not
		// there admits nobody, and telling the two apart would tell a caller
		// which of the two states the installation is in (G18).
		return nil, &projectSecretRefusal{StatusUnverified, ErrorWorkflowSecretNotAllowed, workflowSecretNotAllowed, nil}
	}
	marked, err := s.marked(ctx, source.Source, container)
	if err != nil {
		// A marker read that FAILED is the state class and not the unmarked
		// refusal: the container may well be marked, and telling the caller to
		// run `shared set` would send an operator to rewrite one that is already
		// right. The cause stays on the operator's log line.
		return nil, &projectSecretRefusal{StatusUnavailable, ErrorStateUnavailable, workflowSecretMarkerUnreadable, labelRead(readMarker, err)}
	}
	if !marked {
		return nil, &projectSecretRefusal{StatusUnavailable, ErrorWorkflowSecretUnmarked, workflowSecretUnmarked, nil}
	}
	return source.Source, nil
}

// ownerName is a repository in the spelling the project record's patterns use.
// A verified job or build account carries the canonical provider/owner/name
// (projectpolicy.Canonical), while the record's patterns — like the local
// block's `allow_data_to` they are seeded from — are owner/name: the two differ
// by the provider segment and by nothing else, so the fence strips it rather
// than the record growing a second spelling of the same repository.
//
// A value with no "/" at all is returned as it stands and matches no pattern,
// which is the fail-closed answer: a repository this service cannot place is one
// the owner's list cannot have named.
func ownerName(canonical string) string {
	_, rest, _ := strings.Cut(canonical, "/")
	return rest
}

// workflowSecrets answers one workflow-secrets call.
//
// Everything before this is the call's admission and nothing here repeats it:
// the two identities have been verified, the pairing has bound the calling job
// account to the run identity's repository, the operator's stop switches have
// been read, and the slip the call presents — if it presents one — has been
// verified against the run. What is left is this route's own gate (the class),
// the record, and the one container the call asks for.
//
// The order is the fail-closed order, and each step is answered before the next
// can be reached: the class first, because a class this route does not serve is
// refused before any record is read; then the body, before the record; then the
// record, before any container; then the container, before any value. Nothing on
// this path serves a value that a step above it did not authorise.
func (s *Server) workflowSecrets(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, ja JobAccount) {
	if id.JobClass != ClassRepository {
		// A shared project job's account and a review job's name no one
		// workflow's job (the pairing synthesises their rows), and what a
		// WORKFLOW job is served is its own workflow's secrets: a caller whose
		// account cannot say which workflow that is is refused whole rather
		// than served the repository's union (G19). The refusal is the
		// pairing's own vocabulary — the run identity is not one this route
		// serves — so no runner needs a new case.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnverified, ErrorUnverifiedRunIdentity, workflowSecretNotThisJob)
		return
	}
	req, err := readWorkflowSecretsRequest(r)
	if err != nil {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if req.VersionOf() == WorkflowSecretsVersion2 {
		s.workflowSecretByName(ctx, w, id, ja, req)
		return
	}
	s.workflowSecretByEnv(ctx, w, id, ja, req)
}

// workflowSecretByEnv is version 1: the variable the call names, resolved to the
// logical secret the repository's own fugaro.yaml declares it under (D3459) and
// served out of the project container that name builds, fenced exactly as
// version 2's is.
//
// The record is the fallback, and for two states only: while the door D2313
// keeps is open (G9, F24), when a repository's own copies are what is in force;
// and when the code host could not be reached. Both fall back to the record's
// own container for the variable, unchanged, because that is what the job's
// mount would have carried.
func (s *Server) workflowSecretByEnv(ctx context.Context, w http.ResponseWriter, id Identity, ja JobAccount, req WorkflowSecretsRequest) {
	if !s.workflowSecretsFromFile() {
		s.workflowSecretFromRecord(ctx, w, id, ja, req)
		return
	}
	secrets, declared, err := s.repoWorkflowSecrets(ctx, ja.Repo, ja.Workflow, req.AppID)
	switch {
	case errors.Is(err, ErrRepoFileUnreachable):
		s.logRepoFileFallback(ja.Slug, err)
		s.workflowSecretFromRecord(ctx, w, id, ja, req)
		return
	case errors.Is(err, errRepoFileAppID):
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+workflowSecretNoAppID)
		return
	case err != nil:
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretFileUnreadable)
		return
	case !declared:
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretOtherWorkflow)
		return
	}
	name := ""
	for _, sec := range secrets {
		if sec.Env == req.Env {
			name = sec.Name
			break
		}
	}
	if name == "" {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+workflowSecretNoSuchEnv)
		return
	}
	container, err := secretname.Project(name)
	if err != nil {
		// The file declared a name the grammar does not build a container for,
		// so the file is what this refusal is about: it is a document this
		// release cannot serve and not a call the caller can fix.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretFileUnreadable)
		return
	}
	s.serveProjectSecret(ctx, w, id, ja, name, container, WorkflowSecretsResponse{Env: req.Env})
}

// workflowSecretFromRecord is version 1's record path: the variable the call
// names, read out of the container the repository's own record puts it in.
//
// It is what this route did before a repository's file was read (UO7), kept
// whole for the two states that still reach it — the door D2313 keeps being
// open, and a code host that could not be reached — and it goes when the record
// does (UO7b).
func (s *Server) workflowSecretFromRecord(ctx context.Context, w http.ResponseWriter, id Identity, ja JobAccount, req WorkflowSecretsRequest) {
	if s.cfg.WorkflowSecrets == nil {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretNoReader)
		return
	}
	rec, found, err := s.cfg.WorkflowSecrets.Read(ctx, ja.Slug)
	if err != nil {
		s.refuseCallCause(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadable, err)
		return
	}
	if !found {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretNoRecord)
		return
	}
	rows, ok := rec.Workflows[ja.Workflow]
	if !ok || len(rows) == 0 {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretOtherWorkflow)
		return
	}
	container := ""
	for _, row := range rows {
		if row.Env == req.Env {
			container = row.ID
			break
		}
	}
	if container == "" {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+workflowSecretNoSuchEnv)
		return
	}
	if !secretIDRE.MatchString(container) {
		// The refusal names the record and not the entry: what the node holds
		// is the operator's business and not a caller's, and a resource path is
		// never built from a document this service has not read as a container
		// ID.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadable)
		return
	}
	source := s.perSecrets()
	if source == nil || source.Source == nil {
		// No Secret Manager client and no project: the same refusal an
		// unreadable container gets, because the two are one state to a run —
		// nothing was read — and neither is a fallback.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue)
		return
	}
	value, found, err := source.Source.Value(ctx, container)
	if err != nil {
		// The reader's error is the cause and never the message: it is another
		// package's sentence and it reaches this service's own log line
		// (refuseCallCause), where an operator reads it.
		s.refuseCallCause(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue, err)
		return
	}
	if !found {
		// A container, or a version of one, that is not there. There is no
		// fallback of any kind: the record said this is where this variable's
		// value is, and a service that answered nothing — or the bundle's entry
		// under the same name — would be serving a credential this repository's
		// own record does not name (D143, G19).
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue)
		return
	}
	s.answerWorkflowSecret(ctx, w, id, container, WorkflowSecretsResponse{Env: req.Env, Value: value})
}

// workflowSecretByName is version 2 (D3324, D3328, D3333): the call names the
// LOGICAL secret, and the container is the project's own `fugaro_p_<name>`,
// served only to a repository the owner's list admits and only from a container
// the intake marked (F18, G2).
//
// The repository is the verified job account's and the name is the call's, held
// to the grammar by the contract's own Validate before this is reached — and
// built into the container here through secretname.Project, which is the one
// spelling of the grammar this service writes a container ID with. A name
// secretname refuses (a platform credential's, say) is a refused request: the
// caller can fix it, and no container of that name is one this route serves.
//
// Nothing about the workflow enters: D3333 makes the REPOSITORY the unit, so
// every workflow of an admitted repository may fetch the name, and the job
// account still decides which repository that is.
//
// A name must also be one the repository's OWN fugaro.yaml declares (D3459), and
// that is the one answer the runner falls back to version 1 on
// (credsvc.ErrorWorkflowSecretNotAllowed): a record that does not hold the name
// yet and a file that does not declare it are the same answer, which is what
// keeps a repository already served by S3 working until its file is updated.
func (s *Server) workflowSecretByName(ctx context.Context, w http.ResponseWriter, id Identity, ja JobAccount, req WorkflowSecretsRequest) {
	if !config.RepoSecretsOff {
		// G9, F24: the door is open, so a repository's own copies are in force
		// and a project copy would shadow a repository's own value; this version
		// serves nothing until it is shut. The refusal is the installation's
		// state and not the caller's call, which is why it is unavailable and
		// not a bad request.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorWorkflowSecretsOff, workflowSecretFlipPending)
		return
	}
	// The container is built here and not inside serveProjectSecret because
	// this is the ORDER this version has always had: a name the grammar refuses
	// is a refused request before the repository's own file is read at all.
	container, err := secretname.Project(req.Name)
	if err != nil {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if s.workflowSecretsFromFile() {
		secrets, declared, err := s.repoWorkflowSecrets(ctx, ja.Repo, ja.Workflow, req.AppID)
		switch {
		case errors.Is(err, ErrRepoFileUnreachable):
			// The one fallback: the host could not be reached, and this version's
			// own admission — the project record and the marker — is then the
			// whole answer, exactly as it was before the file was read.
			s.logRepoFileFallback(ja.Slug, err)
		case errors.Is(err, errRepoFileAppID):
			s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+workflowSecretNoAppID)
			return
		case err != nil:
			s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretFileUnreadable)
			return
		case !declaresName(secrets, declared, req.Name):
			s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnverified, ErrorWorkflowSecretNotAllowed, workflowSecretNotAllowed)
			return
		}
	}
	s.serveProjectSecret(ctx, w, id, ja, req.Name, container, WorkflowSecretsResponse{Name: req.Name})
}

// serveProjectSecret reads the project container one logical secret's name
// builds and answers its value: the fence version 2 has always served a name
// through (F13, D3328, D3333, G2) — the owner's record must admit this
// repository to the name and the container must carry the marker — and the same
// fence a name the repository's own file declares is served through, because a
// file is not a grant either.
//
// name and container are the caller's: each version derives them where its own
// refusal belongs, and ans is the answer without its value, which is added here.
func (s *Server) serveProjectSecret(ctx context.Context, w http.ResponseWriter, id Identity, ja JobAccount, name, container string, ans WorkflowSecretsResponse) {
	src, ref := s.projectSecretReadable(ctx, name, container, ja.Repo)
	if ref != nil {
		s.refuseProjectSecret(ctx, w, id, container, ref)
		return
	}
	value, found, err := src.Value(ctx, container)
	if err != nil {
		// The reader's error is the cause and never the message: it is another
		// package's sentence and it reaches this service's own log line
		// (refuseCallCause), where an operator reads it.
		s.refuseCallCause(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue, err)
		return
	}
	if !found {
		// A container, or a version of one, that is not there: the name is
		// admitted and marked, and the operator's `secrets shared set` has not
		// stored a value under it yet. There is no fallback of any kind, and the
		// sentence says which of the two it is by naming the fix (D143).
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue)
		return
	}
	ans.Value = value
	s.answerWorkflowSecret(ctx, w, id, container, ans)
}

// workflowSecretsFromFile says whether this route reads the repository's own
// fugaro.yaml at all: the flip must have taken the mount away
// (config.RepoSecretsOff, G9) and the deployment must hold a reader for the
// code host. Either one absent is the record path, unchanged — a repository
// whose own root was never re-applied with the switch on keeps every mount, and
// a service whose reader is not wired refuses nothing it did not refuse before.
func (s *Server) workflowSecretsFromFile() bool {
	return config.RepoSecretsOff && s.cfg.RepoFiles != nil
}

// repoWorkflowSecrets reads the calling job account's own repository's
// fugaro.yaml at its DEFAULT branch and answers the secrets its workflow
// declares there (D3459).
//
// The repository is the account's canonical name and the workflow is the
// account's (JobAccount, which is the launcher-written task's), so nothing a
// call carries decides either; the ref is the empty string, which is the
// default branch and never the branch the run itself pushes.
//
// declared=false is a file that holds no such workflow, which the caller
// answers the way the record path answers it. The two errors are the reader's:
// ErrRepoFileUnreachable (the fallback) and errRepoFileAppID (a refused
// request), both wrapped and read with errors.Is; everything else is a file
// this release cannot read and is a refusal.
func (s *Server) repoWorkflowSecrets(ctx context.Context, repo, workflow, appID string) ([]config.Secret, bool, error) {
	data, err := s.cfg.RepoFiles.Read(ctx, RepoFileQuery{
		Provider: providerOf(repo), Repo: repo, AppID: appID, Path: fugaroYAMLPath, Ref: "",
	})
	if err != nil {
		return nil, false, err
	}
	cfg, ok := config.RepoOf(data)
	if !ok {
		return nil, false, errors.New("credsvc: the repository's own fugaro.yaml is not a document this release can read")
	}
	wf, ok := cfg.Workflows[workflow]
	if !ok {
		return nil, false, nil
	}
	return wf.Secrets, true, nil
}

// declaresName reports whether the repository's own file declares a logical
// secret, which is what bounds a version-2 call to the names the file writes
// (D3459, D3333).
func declaresName(secrets []config.Secret, declared bool, name string) bool {
	if !declared {
		return false
	}
	for _, sec := range secrets {
		if sec.Name == name {
			return true
		}
	}
	return false
}

// providerOf reads the provider kind out of a canonical repository name
// (projectpolicy.Canonical), which is the first of its three segments. A name
// with no separator answers "", which no read can dispatch on and which is the
// fail-closed answer: a repository this service cannot place is one it cannot
// read a file for.
func providerOf(canonical string) string {
	provider, _, _ := strings.Cut(canonical, "/")
	return provider
}

// logRepoFileFallback records why the record path was taken after the
// repository's file was asked for: the operator's line, carrying the class of
// the failure and, when the host answered, its status — and never the failure's
// own text, because the request that failed carried the App's JWT or the
// repository's token (githubread.go's rule).
func (s *Server) logRepoFileFallback(slug string, err error) {
	attrs := []any{"purpose", PurposeWorkflowSecret, "repository", slug}
	var se *httpjson.StatusError
	if errors.As(err, &se) {
		attrs = append(attrs, "status", se.Status)
	}
	s.log.Warn("credsvc: the repository's own fugaro.yaml could not be read, so the workflow secrets record is the fallback", attrs...)
}

// refuseProjectSecret answers one refusal of the project fence, with the cause
// on the operator's own line when there is one and never in the caller's
// sentence (refuseCallCause's rule). It is one function because the fence
// answers one shape of refusal and three places make it.
func (s *Server) refuseProjectSecret(ctx context.Context, w http.ResponseWriter, id Identity, container string, ref *projectSecretRefusal) {
	if ref.cause != nil {
		s.refuseCallCause(ctx, w, id, PurposeWorkflowSecret, container, "", ref.status, ref.code, ref.message, ref.cause)
		return
	}
	s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", ref.status, ref.code, ref.message)
}

// answerWorkflowSecret builds the answer, audits the act and writes it, in the
// order and for the reasons answerGitHubRead has: the answer is built first, so
// a body the service cannot build is a refusal with its own line and no issuance
// line at all; then the audit line, written BEFORE the value is handed over,
// because an act this service cannot record is not made (C1.13, D143) and a
// served value cannot be unserved — the runner holds it from the moment the
// bytes leave.
//
// The line names the CONTAINER the value was read from (Identity.Audit fills in
// the run, its repository, the job the run reported and the verified account).
// It is a name of the record's own — the thing the operator's `update` mounted,
// which is the one fact that says where a run's value came from — and it is one
// name with no tier, because a workflow secret is not a model credential of the
// cascade and was selected at no tier (audit.go's credentialWithoutTier).
func (s *Server) answerWorkflowSecret(ctx context.Context, w http.ResponseWriter, id Identity, container string, ans WorkflowSecretsResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the workflow secret's answer could not be built", "error", err)
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretAnswerFailed)
		return
	}
	if err := s.auditCall(ctx, id, PurposeWorkflowSecret, container, "", StatusOK); err != nil {
		s.log.Error("credsvc: the workflow secret's audit line was not written", "error", err)
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnaudited)
		return
	}
	writeReadAnswer(w, body)
}
