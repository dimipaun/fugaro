package credsvc

// The build-credentials route (B4.3, R5, D2076, D2077, G20): a REPOSITORY's
// build asks this service for the credentials it bakes with, and the service
// serves it that repository's and no other's.
//
// # What it is for
//
// A repository's image build and its daily check run as the repository's own
// build service account (gcp.BuildServiceAccountID) and today hold their git
// credential and their workflow secrets as mounts. S4 (B4.5, B4.9) moves both
// behind this route: the build step asks here with a metadata ID token and
// reads nothing of its own environment, so a switched repository's build
// account can be given up its accessors and its mounts and still bake.
//
// # Who it serves, and what it is served
//
// The caller is a repository's build service account and only that
// (VerifyRepoBuild, auth.go). The row that address lands on IS the whole
// binding, and no field of the call can reach it (G20):
//
//   - the REPOSITORY, its SLUG and the PROVIDER are the verified row's. A build
//     of one repository is answered that repository's credentials, and a
//     provider it did not deploy with is not something it can ask for.
//   - the GIT CREDENTIAL is the provider's answer (D2076). On GitHub the
//     service mints a read-only installation token for the row's own repository
//     with github.BuildTokenPermissions — contents and metadata, read, and
//     nothing that writes — which is STRICTLY narrower than the set
//     githubread.go mints for a person (that one adds pull requests). The App
//     key signs it and never leaves this service. On Bitbucket the answer is the
//     bundle's own token, whose reach is unchanged (D2076 (a)): this route is
//     not the place that narrows a Bitbucket credential, and pretending
//     otherwise would be a claim the service cannot keep.
//   - the WORKFLOW SECRETS come from B3.1's record through B3.2's reader
//     (D2077 (a)): creds/workflowsecrets/<the row's slug>, the same node the
//     workflow-secrets route reads, read with the service's own account at call
//     time (perSecrets). The workflow the call names is a SELECTOR within the
//     caller's own record and never a widening: the account is per repository
//     and not per workflow — that is what makes it a build's account and not a
//     job's — so every entry the record holds belongs to the calling
//     repository, and a workflow or a variable the record does not hold is
//     refused rather than served out of anywhere else.
//
// # What it refuses
//
// Everything else, and by default. A run identity or a job name is refused by
// name before anything is read: a build holds neither, so a call presenting one
// is a call of a shape this route does not have. A body that names a fact the
// contract does not carry is refused by the strict decoder (Convention 6). A
// bundle with no App key or no Bitbucket token is a refusal and never a
// fallback to something the caller holds. A record that is absent, of a version
// this release does not know, or missing the named workflow, the named variable
// or the container's value, is a typed refusal with no fallback of any kind:
// a service that answered an empty value would leave the build with a
// credential it did not ask for, or with none and no reason it could act on
// (D143, G19).
//
// # No value anywhere but the answer
//
// Every value this route serves is a credential, and the answer is the only
// place one may appear (G18). No line, no error and no log of this file repeats
// a token, a key or a secret's value: every refusal sentence below is written
// here, names the field that was wrong and not what it held, and the Secret
// Manager failure keeps its own class as the cause of a refusal whose message
// is this file's. The answer itself is written no-store (writeReadAnswer),
// because an intermediary that cached a build's credential would be handing the
// next caller a credential this service did not serve it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// The refusals of this route. They are sentinels so a test and a reader can tell
// them apart, and every one is a sentence written here: GitHub's own answer
// never reaches a caller, because the request that failed carried the App's JWT.
var (
	// ErrBuildCredentialsBadRequest is a call whose body is not this contract's:
	// not one JSON object of three fields, a workflow that is not a workflow
	// name, an environment variable that is not a name a job could mount a
	// secret under, a variable named twice, or an App ID that is not 1 to 20
	// digits.
	ErrBuildCredentialsBadRequest = errors.New("credsvc: the build-credentials call is not one this service reads")
	// ErrBuildCredentialsNoAppKey is a bundle that holds no GitHub App key. It
	// is the fail-closed case the mint's own plan amendment names (D143): a
	// service that minted without one, or fell back to a key the caller holds,
	// would be indistinguishable from a broken installation.
	ErrBuildCredentialsNoAppKey = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	// ErrBuildCredentialsBadAppKey is a bundle whose App key is not a private
	// key this code can sign with.
	ErrBuildCredentialsBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrBuildCredentialsNoBitbucketToken is a bundle that holds no Bitbucket
	// token, which is the whole of what a Bitbucket repository's build is
	// answered (D2076 (a)). There is no fallback: a service that answered
	// nothing, or a token from anywhere else, would be serving a credential the
	// installation's own bundle does not name.
	ErrBuildCredentialsNoBitbucketToken = errors.New("credsvc: the credentials bundle holds no Bitbucket token")
	// ErrBuildCredentialsGitHub is GitHub refusing the mint, or the call to it
	// failing.
	ErrBuildCredentialsGitHub = errors.New("credsvc: GitHub did not mint an installation token")
	// ErrBuildCredentialsUnaudited is an answer C1.13's auditor could not
	// record. A token GitHub granted exists at GitHub and reaches nobody: this
	// service will not hand out a credential it could not write a line for
	// (D143).
	ErrBuildCredentialsUnaudited = errors.New("credsvc: the build-credentials answer could not be audited, so no credential was served")
)

// The refusals of the record half, as sentences written here, on the
// workflow-secrets route's own rule: each names the field that was wrong and
// never what the call sent, what the record holds or which container was read,
// so a caller learns that it was refused and nothing it could enumerate with.
const (
	// buildCredentialsNoReader answers a service with no reader for the record
	// at all: the route refuses rather than serving nothing, exactly as the
	// workflow-secrets route does.
	buildCredentialsNoReader = "this service has no reader for the workflow secrets record, so it serves no build secret"
	// buildCredentialsNoRecord answers a repository `fugaro update` has not
	// recorded: an installation that has not run the release that writes these,
	// or one whose write has not reached this repository yet. It is not an
	// empty answer (G19).
	buildCredentialsNoRecord = "no workflow secrets record is written for the calling build's repository, so this service serves no build secret for it"
	// buildCredentialsUnreadable answers a record that could not be read, or one
	// whose entry is not a container this service may read.
	buildCredentialsUnreadable = "the workflow secrets record could not be read"
	// buildCredentialsOtherWorkflow answers a record that holds nothing for the
	// workflow the call names. The sentence says no more than that: which
	// workflows the record DOES hold, and which variables, are the repository's
	// own business.
	buildCredentialsOtherWorkflow = "the workflow this call names is not one this repository's record holds a secret for"
	// buildCredentialsNoSuchEnv answers a variable the named workflow is not
	// recorded under. It is the fail-closed half of the record's being the
	// authority: the service serves what the record says the build mounts, and
	// never a name the caller supplied that the record does not hold (G19).
	buildCredentialsNoSuchEnv = "the workflow this call names holds no secret under an environment variable this call names"
	// buildCredentialsUnreadableValue answers a container that could not be
	// read, is no longer there, or holds a version with no value. There is no
	// fallback from it (D143, G19).
	buildCredentialsUnreadableValue = "the build secret could not be read"
	// buildCredentialsAnswerFailed answers a service that could not build its
	// own answer. The branch is unreachable through a real call — the answer is
	// a struct of strings — and it is kept rather than dropped for the mint's
	// own reason: the alternative to answering is a panic inside the one act
	// that hands out a credential, and a call that reached it must be refused.
	buildCredentialsAnswerFailed = "the service could not build the build-credentials answer"
)

// readBuildCredentialsRequest reads a build-credentials call's body. It is
// bounded and decoded strictly (Convention 6: a field this code does not know is
// refused, not ignored), and no error here repeats what it read: the body is the
// caller's text, and a decoder's own message names the byte it choked on — so
// every sentence this returns is one of this file's own.
//
// The two names it carries are held to the SHAPE rules of the documents they
// will be looked up in — a workflow name (config.WorkflowNameRE, the rule
// fugaro.yaml holds a workflow's name to) and an environment-variable name
// (config.EnvNameRE, the rule a workflow secret is recorded under) — so a name
// no record could hold is refused before the record is read rather than
// searched for.
func readBuildCredentialsRequest(body []byte) (BuildCredentialsRequest, error) {
	if len(body) > maxBuildCredentialsRequestBytes {
		return BuildCredentialsRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxBuildCredentialsRequestBytes)
	}
	var req BuildCredentialsRequest
	if err := decodeStrict(body, &req); err != nil {
		return BuildCredentialsRequest{}, errors.New("the body is not one JSON object of this contract's fields")
	}
	if !config.WorkflowNameRE.MatchString(req.Workflow) {
		return BuildCredentialsRequest{}, errors.New("the call names no workflow: a workflow's name is 1 to 20 characters of a-z, 0-9 and '-', starting with a letter")
	}
	// The variables are held to the shape a job could mount a secret under, and
	// a name twice is refused: what the call asks for is a set of variables.
	seen := make(map[string]bool, len(req.Secrets))
	for _, env := range req.Secrets {
		if !config.EnvNameRE.MatchString(env) {
			return BuildCredentialsRequest{}, errors.New("the call names an environment variable that is not one: a name a workflow secret is mounted under is upper-case letters, digits and '_', starting with a letter or '_'")
		}
		if seen[env] {
			return BuildCredentialsRequest{}, errors.New("the call names the same environment variable twice")
		}
		seen[env] = true
	}
	if req.AppID != "" && !appIDRE.MatchString(req.AppID) {
		return BuildCredentialsRequest{}, errors.New("the call carries a GitHub App ID that is not one (1 to 20 digits)")
	}
	return req, nil
}

// buildCredentials answers one build-credentials call. It is reached from
// ServeHTTP's branch ahead of RequestFromHTTP, and it does there what
// githubRead and buildToken do: it verifies its caller, reads its call, reads
// every credential the call needs, and records the act.
//
// The order below is the fail-closed order, and it is githubRead's step for
// step with one addition of this route's own:
//
//   - the caller, first, and on its own token: this is the route's whole
//     admission, and the ROW it answers is the binding every step below is
//     scoped by. A refusal here is typed and has NO LINE, because nothing is
//     verified yet and there is no caller for a line to name.
//   - the run identity and the job name the class does not take, refused BY
//     NAME before anything else is read: a build holds no run identity, so a
//     call presenting one is a call of a shape this route does not have. These
//     two refusals ARE recorded — the caller is verified — and they are
//     recorded with the route's own purpose, the calling repository and the
//     provider's credential name, all three of which are the row's.
//   - the call's body, whose read is ALSO the registration of the caller's own
//     token with the call-scoped scrub.
//   - the operator's stop flags, read with the CALLER'S OWN SLUG before the
//     bundle, so a stopped repository's build reads no credential at all. The
//     slug is the row's: this route names a repository, so it is served the
//     global switch and that repository's own, exactly as every other route
//     that names a repository is (KillReader).
//   - the bundle, failing closed: both providers' credentials are read from it
//     and from nowhere else.
//   - the SECRETS, before the mint: a GitHub installation token cannot be
//     unminted, so everything that can fail without an external act fails
//     first. A record, a workflow, a variable or a container that is not there
//     is refused here, with the token never asked for.
//   - the git credential: the App key and the mint for a GitHub repository, the
//     bundle's own token for a Bitbucket one.
//   - the answer, built, audited and handed over in that order
//     (answerBuildCredentials).
func (s *Server) buildCredentials(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, _ := routeAct(r.URL.Path)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	row, err := s.cfg.Auth.VerifyRepoBuild(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way every other class's route
		// answers it: a typed refusal and no line.
		s.refuseVerifier(w, err)
		return
	}
	// The credential every line below names, and it is the CALLER's answer and
	// not the body's: which provider the repository is held at is the row's, so
	// it is known before anything is read. routeAct names the purpose and
	// leaves this to the handler for the reason the proxy route's rule gives —
	// the path alone does not say which credential the act issues from.
	credential := config.GitHubAppKeySecret
	if row.Provider == gitprov.KindBitbucket {
		credential = config.BitbucketSecret
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it serves a repository's build, which holds none, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it serves a repository's build, which holds none, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readBuildCredentialsRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent, and it is
		// answered before the bundle is read: a request this contract refuses
		// reads no credential at all.
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if req.AppID != "" && row.Provider != gitprov.KindGitHub {
		// A Bitbucket repository's build names no App ID: its answer is the
		// bundle's own token, and a call that names an App is a call of a shape
		// this row does not have. It is a refused REQUEST and not a
		// configuration fault, because the caller can fix it.
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"the call carries a GitHub App ID, and the calling repository is not held at GitHub: a Bitbucket build is answered the bundle's token and signs nothing")
		return
	}
	if row.Provider == gitprov.KindGitHub && req.AppID == "" {
		// A GitHub installation token is signed as the App, and the App ID is
		// the one thing the caller supplies to that mint.
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"the call carries no GitHub App ID (1 to 20 digits), and a GitHub installation token is signed as the App")
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, row.Slug)
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so the
		// call is refused rather than served.
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	secrets, ok := s.buildSecretValues(ctx, w, row, purpose, credential, req, b)
	if !ok {
		return
	}
	git, ok := s.buildGitCredential(ctx, w, row, purpose, credential, req, b)
	if !ok {
		return
	}
	s.answerBuildCredentials(ctx, w, row, purpose, credential, BuildCredentialsResponse{Git: git, Secrets: secrets})
}

// buildSecretValues reads the workflow secrets the call asked for, out of
// B3.1's record for the CALLER'S OWN repository through B3.2's reader
// (D2077 (a)).
//
// It answers ok=false when it has already refused the call, which is every
// branch below: an absent reader, an absent record, a record this release does
// not know the version of, a workflow the record does not hold, a variable the
// workflow is not recorded under, an entry that is not a container ID, and a
// container that could not be read or is not there. Each of them is a typed
// refusal and none is a fallback: a service that answered a value the record
// does not name would be serving a credential this repository's own record
// never put here.
//
// The list is read in the ORDER the call asked for it, so a client can map the
// answer onto its own request without a second lookup, and a call that asked for
// nothing is answered an empty list without the record being read at all.
func (s *Server) buildSecretValues(ctx context.Context, w http.ResponseWriter, row RepoBuildAccount, purpose, credential string, req BuildCredentialsRequest, b *Bundle) ([]BuildSecretValue, bool) {
	if len(req.Secrets) == 0 {
		return nil, true
	}
	if s.cfg.WorkflowSecrets == nil {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsNoReader)
		return nil, false
	}
	rec, found, err := s.cfg.WorkflowSecrets.Read(ctx, row.Slug)
	if err != nil {
		// The reader's error is the cause and never the message: it is another
		// package's sentence and it reaches this service's own log line.
		s.refuseBuildCredentialsCause(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsUnreadable, err)
		return nil, false
	}
	if !found {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsNoRecord)
		return nil, false
	}
	rows, ok := rec.Workflows[req.Workflow]
	if !ok || len(rows) == 0 {
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsOtherWorkflow)
		return nil, false
	}
	byEnv := make(map[string]string, len(rows))
	for _, r := range rows {
		byEnv[r.Env] = r.ID
	}
	// Every container is resolved BEFORE any is read, so a call that names one
	// variable the workflow is not recorded under is refused whole rather than
	// half answered: the answer is one act and an operator's line for it is one
	// line, and a partial answer would be a credential handed over for a call
	// this service refused.
	containers := make([]string, 0, len(req.Secrets))
	for _, env := range req.Secrets {
		container, ok := byEnv[env]
		if !ok {
			s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+buildCredentialsNoSuchEnv)
			return nil, false
		}
		if !secretIDRE.MatchString(container) {
			// The refusal names the record and not the entry: what the node
			// holds is the operator's business and not a caller's, and a
			// resource path is never built from a document this service has not
			// read as a container ID.
			s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsUnreadable)
			return nil, false
		}
		containers = append(containers, container)
	}
	source := s.perSecrets()
	if source == nil || source.Source == nil {
		// No Secret Manager client and no project: the same refusal an
		// unreadable container gets, because the two are one state to a build —
		// nothing was read — and neither is a fallback.
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsUnreadableValue)
		return nil, false
	}
	out := make([]BuildSecretValue, 0, len(req.Secrets))
	for i, container := range containers {
		reader := source.Source
		if name, grammar := grammarName(container); grammar {
			// A container in the grammar is a PROJECT one — the shape
			// `fugaro secrets shared set` writes and the intake marks — and it
			// is served through the same fence the version-2 workflow-secrets
			// route serves one through (F13, D3328, D3333, G2): the owner's
			// record must admit this repository to the name, and the container
			// must carry the marker. A row outside the grammar is the project
			// copy `update` writes under D2313, which is this route's own
			// record and needs no fence of its own.
			//
			// `config.RepoSecretsOff` is deliberately NOT read here: it is the
			// door version 2 is refused behind (G9), and this route's container
			// is the one the record itself named — refusing it would take a
			// build's secret away over a state the operator's own `update`
			// controls.
			fenced, ref := s.projectSecretReadable(ctx, name, container, row.Repo)
			if ref != nil {
				s.refuseProjectSecretBuild(ctx, w, row, purpose, credential, ref)
				return nil, false
			}
			reader = fenced
		}
		value, found, err := reader.Value(ctx, container)
		if err != nil {
			s.refuseBuildCredentialsCause(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsUnreadableValue, err)
			return nil, false
		}
		if !found {
			// A container, or a version of one, that is not there. There is no
			// fallback of any kind (D143, G19).
			s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsUnreadableValue)
			return nil, false
		}
		out = append(out, BuildSecretValue{Env: req.Secrets[i], Value: value})
	}
	return out, true
}

// grammarName reads a container ID back to the workflow-secret name it holds,
// and whether it is in the project grammar at all. A container outside the two
// prefixes is the project copy `update` derives under D2313 — or a repository's
// own copy, on an installation whose door is open — and neither is served
// through the project fence.
func grammarName(container string) (string, bool) {
	n, err := secretname.Parse(container)
	if err != nil {
		return "", false
	}
	return n.Name, true
}

// buildGitCredential reads the git credential the provider's answer names
// (D2076), for the caller's own repository and for nothing else.
//
// On GitHub it mints a read-only installation token over the row's one
// repository with github.BuildTokenPermissions: contents and metadata, read,
// and nothing that writes. That set is what an image build's token asks for —
// it only clones the repository — and it is strictly narrower than the set
// githubread.go mints for a person, which adds pull requests. The mint goes to
// api.github.com and to nothing else: a root a caller could name would be the
// App credential's destination chosen by the caller.
//
// On Bitbucket there is nothing to mint: the bundle's own token IS the answer,
// and its reach is unchanged (D2076 (a)). The host and the username are the
// service's constants rather than anything the call carries, so a build cannot
// be pointed at another destination by its own text.
//
// It answers ok=false when it has already refused the call.
func (s *Server) buildGitCredential(ctx context.Context, w http.ResponseWriter, row RepoBuildAccount, purpose, credential string, req BuildCredentialsRequest, b *Bundle) (BuildGitCredential, bool) {
	if row.Provider == gitprov.KindBitbucket {
		entry, ok := b.BitbucketToken()
		if !ok || entry.Value() == "" {
			// Fail closed: a bundle with no Bitbucket token answers this build
			// nothing, and never a value from anywhere else.
			s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, ErrBuildCredentialsNoBitbucketToken.Error())
			return BuildGitCredential{}, false
		}
		return BuildGitCredential{
			Provider: gitprov.KindBitbucket,
			Username: bitbucket.GitUsername,
			Token:    entry.Value(),
			Host:     repourl.ProviderHost(gitprov.KindBitbucket),
		}, true
	}
	owner, name, err := mintRepository(row.Repo)
	if err != nil {
		// The row's repository is a canonical GitHub name the deployment
		// derived, so this is unreachable through a real installation: a table
		// entry this code cannot read is one it must not guess a repository out
		// of, because the token is then scoped by what these two strings say.
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, ErrBuildCredentialsGitHub.Error())
		return BuildGitCredential{}, false
	}
	key, err := appKey(b)
	if err != nil {
		// The App key's own two refusals, respelled so that a build's operator
		// reads the act it was asked for: the sentences are the mint's own and
		// say the same thing, because it is the same key in the same bundle
		// that is missing or malformed.
		if errors.Is(err, ErrMintNoAppKey) {
			err = ErrBuildCredentialsNoAppKey
		} else {
			err = ErrBuildCredentialsBadAppKey
		}
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return BuildGitCredential{}, false
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: owner, Repo: name, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	}, github.BuildTokenPermissions())
	if err != nil {
		// The operator gets the class of the failure and, when GitHub answered,
		// its status -- and never a byte of GitHub's own text: the request that
		// failed carried the App's JWT, so an upstream that echoed the request
		// would be putting a bearer credential in this service's log.
		attrs := []any{"purpose", purpose, "credential", credential, "repository", row.Slug}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: the build's installation token could not be minted", attrs...)
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, ErrBuildCredentialsGitHub.Error())
		return BuildGitCredential{}, false
	}
	return BuildGitCredential{
		Provider:  gitprov.KindGitHub,
		Username:  github.GitUsername,
		Token:     token,
		Host:      repourl.ProviderHost(gitprov.KindGitHub),
		ExpiresAt: expires,
	}, true
}

// answerBuildCredentials builds the answer, audits the act and writes it, in
// the order and for the reasons answerGitHubRead has: the answer is built
// first, so a body the service cannot build is a refusal with its own line and
// no issuance line at all; then the audit line, written BEFORE any value is
// handed over, because an act this service cannot record is not made (C1.13,
// D143) and a served credential cannot be unserved — the build holds it from
// the moment the bytes leave.
func (s *Server) answerBuildCredentials(ctx context.Context, w http.ResponseWriter, row RepoBuildAccount, purpose, credential string, ans BuildCredentialsResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the build-credentials answer could not be built", "error", err)
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, buildCredentialsAnswerFailed)
		return
	}
	if err := s.auditBuildCredentials(ctx, row, purpose, credential, StatusOK); err != nil {
		s.log.Error("credsvc: the build-credentials answer's audit line was not written", "error", err)
		s.refuseBuildCredentials(ctx, w, row, purpose, credential, StatusUnavailable, ErrorStateUnavailable, ErrBuildCredentialsUnaudited.Error())
		return
	}
	writeReadAnswer(w, body)
}

// auditBuildCredentials writes one build-credentials line: the calling build's
// account as its caller, the repository's SLUG as its repository, the provider's
// git credential as its credential, and NEITHER a run nor a job — the caller is
// a repository's build, which holds no run identity and is not a job of this
// installation.
//
// It does not go through Identity.Audit because there is no Identity: the route
// is answered ahead of Verify, which is the only thing that builds one. The
// repository is the row's SLUG and not a name the call carried, so the line is
// keyed the way every other line of this log is and a caller cannot write it.
// purpose and credential are the route's own (routeAct for the purpose, the
// row's provider for the credential).
func (s *Server) auditBuildCredentials(ctx context.Context, row RepoBuildAccount, purpose, credential, status string) error {
	return s.audit.Write(ctx, AuditEvent{
		Caller:     row.Email,
		Repository: row.Slug,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseBuildCredentials answers one refusal and records it, in the shape
// refuseGitHubRead already has: one typed refusal to the caller, one line to the
// operator, and the caller's answer is the same whether or not the line lands
// (D143).
//
// It is reached only once the caller IS verified — the refusals above that point
// are answered by refuse alone, with no line.
func (s *Server) refuseBuildCredentials(ctx context.Context, w http.ResponseWriter, row RepoBuildAccount, purpose, credential string, status int, typ, message string) {
	if err := s.auditBuildCredentials(ctx, row, purpose, credential, typ); err != nil {
		s.log.Error("credsvc: a refused build-credentials call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// refuseBuildCredentialsCause is refuseBuildCredentials with a cause the
// operator's own log line carries and the caller's sentence does not: a reader's
// error is another package's sentence, and this service's log is where an
// operator reads it (refuseCallCause's rule).
func (s *Server) refuseBuildCredentialsCause(ctx context.Context, w http.ResponseWriter, row RepoBuildAccount, purpose, credential string, status int, typ, message string, cause error) {
	s.log.Error("credsvc: a build-credentials call was refused", "purpose", purpose, "repository", row.Slug, "error", cause)
	s.refuseBuildCredentials(ctx, w, row, purpose, credential, status, typ, message)
}

// refuseProjectSecretBuild answers one refusal of the project fence (F13): the
// same status, code and sentence the version-2 workflow-secrets route answers
// with — a caller reads one vocabulary whether it is a run's job or a
// repository's build — recorded on this route's own line, whose credential is
// the provider's git credential rather than a container name. The cause, when
// there is one, goes to the operator's log and never to the caller.
func (s *Server) refuseProjectSecretBuild(ctx context.Context, w http.ResponseWriter, row RepoBuildAccount, purpose, credential string, ref *projectSecretRefusal) {
	if ref.cause != nil {
		s.refuseBuildCredentialsCause(ctx, w, row, purpose, credential, ref.status, ref.code, ref.message, ref.cause)
		return
	}
	s.refuseBuildCredentials(ctx, w, row, purpose, credential, ref.status, ref.code, ref.message)
}
