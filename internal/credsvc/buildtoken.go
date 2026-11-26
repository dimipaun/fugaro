package credsvc

// The build route (B1, D2301, D2302, F19, R6): a catalog build asks this
// service for the one credential that clones every repository the entry it is
// building bakes, and the service answers with the shape the contract names
// (docs/shared-jobs-default/contract-build-token.md, internal/buildtoken).
//
// The caller is the project's build identity (B5): one service account per
// project, which is the reason the route exists at all. That identity can reach
// the union of every catalog entry's repositories of its project — the
// contract's rule 8 says so in its own words — so what keeps a build out of
// everything else is not the identity's grants but THIS route: the repositories
// a build may be given are read from the published image map for the entry its
// request names (D1906), the credential covers exactly the repositories the
// request named out of that set, and a repository outside it is refused by name.
//
// Two answers, and the provider in the request decides which (R6):
//
//   - GitHub: one read-only installation token (contents: read, metadata:
//     read), minted through github.MintRepositoriesToken, covering exactly the
//     request's repositories of the caller's own project, expiring at GitHub's
//     own instant. Everything about it is the service's and nothing is the
//     caller's: the permissions are the constant, the owner is the entry's, the
//     host is the provider's, and the request has no field for any of them.
//   - Bitbucket: a proxy grant and NO token (D2302). The build holds no
//     credential at all; what its git presents at the proxy is its own ID token
//     (rule 9's one other route).
//
// The route is answered AHEAD of the shared parse (ServeHTTP), for the other
// six such routes' reason: a build holds no run identity and is a job of no
// repository, so RequestFromHTTP would refuse the call for the missing identity
// headers before the class ever read the token. What replaces the parse is this
// file's own read of the body, and the run-identity headers are refused BY NAME
// inside the handler.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/task"
)

// BuildTokenPath is the path a catalog build asks its credential on. It is this
// service's own contract and not a forge path: the call names an entry and the
// repositories, and the service is what reaches GitHub.
const BuildTokenPath = "/v1/build/token"

// buildTokenMaxTTL is the longest life this service gives a build's credential
// (rule 7). It is B1's own decision and not the request's: the contract leaves
// the cap here ("What this contract does not decide"), and Request.TTLSeconds is
// the build's NEED, which the service caps rather than trusts. So a call asking
// for longer is served the cap and not the ask: a pairing that outlived its
// build would be a standing grant to the entry's repositories for whoever runs
// as the project's build identity next, which is the one thing rule 2's
// bookkeeping cannot close.
//
// An hour is GitHub's own installation-token life — the endpoint answers an hour
// whatever is asked of it — so a build asking for more is asking for something
// the GitHub half could not give it either, and a Bitbucket pairing has no
// reason to outlive the token its sibling would have had.
const buildTokenMaxTTL = time.Hour

// buildIDRE is what a build id may be, which is the shape
// internal/backend/gcp's buildIDRE holds and the shape Cloud Build issues: it is
// the key the Bitbucket pairing is recorded under beside the identity (rule 2),
// so this service holds it to that shape rather than taking it as an opaque
// string a caller chose. It is spelled here rather than imported: that one is
// unexported, and this package must not import the compute platform.
var buildIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// BuildTokenRequest is the build route's body: the contract's Request and the
// GitHub App ID.
//
// The App ID is the one field the contract does not carry, and the reason is the
// mint route's (github.MintRequest): the service signs a GitHub App JWT as the
// App, and the App ID is the one thing the CALLER holds and this service does
// not — it is not a secret, the platform passes it to a job as an environment
// variable, and the bundle holds the App's KEY and never its id. The contract
// leaves the body's encoding to this task in as many words ("the route's path
// and its body's encoding are B1's to name; the two sides share these fields and
// nothing else about the wire"), so this is that encoding: the contract's five
// fields, plus the id a GitHub mint cannot be made without. It is required for
// the GitHub half and read by nothing else, and a Bitbucket build — which is
// answered a grant and no token — needs none.
//
// The contract's Request is EMBEDDED and not respelled: a field added to the
// contract reaches this route without a second place to add it, which is what
// keeps the two ends of one wire from drifting.
type BuildTokenRequest struct {
	buildtoken.Request
	// AppID is the GitHub App's numeric id, in the shape every other mint of
	// this service checks it in (appIDRE).
	AppID string `json:"app_id,omitempty"`
}

// readBuildRequest reads a build call's body out of the call the handler already
// read (readCall), which is where the caller's own ID token was registered with
// the service's redaction for the length of this call.
//
// The bound is the mint's 4 KiB and not the gateway's: the call is a handful of
// short fields, so a body larger than this is not a build request. The decode is
// strict, as every body of this contract is, which is what makes "no caller can
// widen this" a property of the route rather than a check inside it: the
// contract's rule 1 says the request has no field for a permission, so a body
// that tries to name one is REFUSED by the strict decoder rather than read for
// the fields it does know.
//
// Every check here is a SHAPE and not a decision: whether the entry's map holds
// the repositories, whether the provider is the entry's, and what the credential
// reaches are the handler's, because they need the published policy and this
// reader has none.
func readBuildRequest(body []byte) (BuildTokenRequest, error) {
	if len(body) > maxMintRequestBytes {
		return BuildTokenRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req BuildTokenRequest
	if err := decodeStrict(body, &req); err != nil {
		return BuildTokenRequest{}, errors.New("the body is not one JSON object of this contract's fields")
	}
	// The entry is a catalog entry name and not free text: it is what the
	// published image map is keyed by, and one in another shape is a name no map
	// could hold (config.ValidCatalogName, the publisher's own rule).
	if !config.ValidCatalogName(req.Entry) {
		return BuildTokenRequest{}, fmt.Errorf("the call names no catalog entry: %s", config.CatalogNameMessage)
	}
	if !buildIDRE.MatchString(req.BuildID) {
		return BuildTokenRequest{}, errors.New("the call carries no build id: a build id is 1 to 128 characters of letters, digits, '-' and '_', and it is the key this service records the build's Bitbucket pairing under")
	}
	switch req.Provider {
	case gitprov.KindGitHub, gitprov.KindBitbucket:
	default:
		return BuildTokenRequest{}, errors.New("the call names a provider this service does not mint at: a build's credential is minted at github or at bitbucket")
	}
	if len(req.Repositories) == 0 {
		return BuildTokenRequest{}, errors.New("the call names no repository: the credential covers exactly the repositories the call lists, and a call naming none asks for nothing")
	}
	// Every repository is held to the canonical form HERE, which is how both
	// members of the map key a repository (ValidRepoImages builds its
	// provider/owner/name from this half, and a legacy image_map keys it
	// directly): what the handler compares against the entry's set is then a name
	// and not a value a caller chose, and a name that is not owner/name is refused
	// before anything is compared. A name twice is refused too: the set of
	// repositories a credential covers is a set.
	seen := make(map[string]bool, len(req.Repositories))
	for _, r := range req.Repositories {
		canonical, err := task.CanonicalRepo(r)
		if err != nil {
			return BuildTokenRequest{}, errors.New("the call names a repository that is not owner/name")
		}
		if canonical != r {
			return BuildTokenRequest{}, errors.New("the call names a repository that is not in its canonical form: a repository is lower-cased owner/name, as acme/app")
		}
		if seen[r] {
			return BuildTokenRequest{}, errors.New("the call names the same repository twice")
		}
		seen[r] = true
	}
	if req.TTLSeconds <= 0 {
		return BuildTokenRequest{}, errors.New("the call names no time to live: a build has to say how long it needs the credential, and this route answers none that does not")
	}
	if req.AppID != "" && !appIDRE.MatchString(req.AppID) {
		return BuildTokenRequest{}, errors.New("the call carries a GitHub App ID that is not one (1 to 20 digits)")
	}
	if req.Provider == gitprov.KindGitHub && req.AppID == "" {
		return BuildTokenRequest{}, errors.New("the call carries no GitHub App ID (1 to 20 digits), and a GitHub installation token is signed as the App")
	}
	return req, nil
}

// buildRepos is the narrowing's answer for one call: the entry's repositories as
// this INSTALLATION holds them, and the request refused when it named one that
// is not the entry's.
//
// A refusal carries both the answer it is given and the sentence an operator
// reads, because the two classes of refusal in the narrowing are answered
// differently: a request the caller can fix is a refused request, and a state
// the project's map put that way is the service being unable to answer.
type buildRepos struct {
	entry   []buildRepo
	status  int
	typ     string
	message string
}

func (r buildRepos) Error() string { return r.message }

// buildRepo is one repository of an entry as this installation holds it: the
// name the image map keys it by, the canonical <provider>/<owner>/<name> the
// table answers with, the slug the installation's rows are keyed by, and the two
// segments a GitHub mint takes.
type buildRepo struct {
	name      string
	canonical string
	slug      string
	owner     string
	short     string
}

// buildEntryRepos narrows one call against the entry it names (rule 8, D1906).
//
// The map is the authority and the request is not: the entry's set is every
// repository the published map puts on the entry's name AT THE REQUEST'S
// PROVIDER — a legacy map, which names no provider, matches every provider, as
// it always did — the request's repositories are held to that set, and a
// repository outside it is refused BY NAME — whether the project holds it on
// another entry or nowhere at all, because the two are the same answer to the
// caller: this entry's credential does not reach it. An entry the map puts
// nothing on has its published catalog definition's source repository as its set
// (entrySourceRepo), and no other.
//
// Which member is the map is read through EffectiveRepoImages (R2, D3372), and
// the two members key a repository differently:
//
//   - repo_images keys it provider/owner/name, so the entry's set is the keys of
//     the REQUEST'S provider alone: a key at another provider is another
//     repository's — the same name on the other host is another repository — and
//     a key whose provider does not serve that repository is SKIPPED, never a
//     refusal of the entry it names (R9). A stale key, or one a later release
//     wrote for a provider this installation has no repository at, must not stop
//     a build of the rest of the entry.
//   - a legacy image_map keys it owner/name with no provider to read, so it is
//     read as it always was: its names are held to the installation's table, and
//     a name the table does not serve at the request's provider is refused,
//     because there the map itself names something the installation does not
//     have.
//
// The set is then held to the INSTALLATION's own table, one repository at a
// time, which is githubRepository's reading of a repository and not a second
// one: the slug is task.Slug(provider, name), the canonical name is
// projectpolicy.Canonical(provider, name), and the table must map one to the
// other. What a table that does not is answered with depends on whether the
// other provider holds it: a repository the project has at the OTHER provider is
// the call naming the wrong one (the contract's own case, "a request that names
// the other one is refused"), which is the caller's to fix and a refused
// request; a repository the project has at neither is the entry's map naming
// something this installation does not serve, which the caller cannot fix and is
// the service being unable to answer.
func (s *Server) buildEntryRepos(shared *projectpolicy.Policy, req BuildTokenRequest) ([]buildRepo, error) {
	images, fromRepoImages := shared.EffectiveRepoImages()
	onEntry := make(map[string]bool, len(images))
	mapped := false
	for key, entry := range images {
		if entry != req.Entry {
			continue
		}
		mapped = true
		if !fromRepoImages {
			onEntry[key] = true
			continue
		}
		provider, repo, ok := strings.Cut(key, "/")
		if !ok || provider != req.Provider || !s.servesRepo(provider, repo) {
			// A key at another provider is another repository's, and a key this
			// installation does not serve at that provider is skipped: neither may
			// refuse the entry the call named (R9).
			continue
		}
		onEntry[repo] = true
	}
	if !mapped {
		// An entry the map puts nothing on is still built, for the one repository
		// its catalog definition is sourced from (the nightly check and update
		// assign it there). That repository is read off the PUBLISHED catalog and
		// never off the call, and it is the whole set: a call naming any other
		// repository is refused below, by name, as for a mapped entry.
		if source, ok := entrySourceRepo(shared, req.Entry); ok {
			onEntry[source] = true
		}
		if len(onEntry) == 0 {
			return nil, buildRepos{status: StatusRequestRefused, typ: ErrorRequestRefused,
				message: requestRefused + "the project's image map puts no repository on " + req.Entry}
		}
	}
	for _, r := range req.Repositories {
		if onEntry[r] {
			continue
		}
		if fromRepoImages && images[otherProvider(req.Provider)+"/"+r] == req.Entry {
			return nil, buildRepos{status: StatusRequestRefused, typ: ErrorRequestRefused,
				message: requestRefused + "the image map puts " + r + " on " + req.Entry + " at the other provider, and a build's credential is minted at the entry's own provider and not at the caller's choice of host"}
		}
		return nil, buildRepos{status: StatusRequestRefused, typ: ErrorRequestRefused,
			message: requestRefused + "the repository " + r + " is not one the image map puts on " + req.Entry +
				", and a build's credential covers the repositories of the entry it names and no others"}
	}
	// The entry's repositories, in one order: the grant names the whole set, the
	// token is scoped to the request's subset of it, and both are spelled the same
	// way so that an operator reading two builds' answers reads one order.
	names := make([]string, 0, len(onEntry))
	for name := range onEntry {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]buildRepo, 0, len(names))
	for _, name := range names {
		canonical, err := projectpolicy.Canonical(req.Provider, name)
		if err != nil {
			return nil, buildRepos{status: StatusUnavailable, typ: ErrorStateUnavailable,
				message: "the project's image map puts " + name + " on " + req.Entry + ", and it is not a repository name this service can read"}
		}
		slug, _ := task.Slug(req.Provider, name)
		if s.cfg.Repos[slug] != canonical {
			if s.reposElsewhere(name, req.Provider) {
				return nil, buildRepos{status: StatusRequestRefused, typ: ErrorRequestRefused,
					message: requestRefused + "the image map puts " + name + " on " + req.Entry + " at the other provider, and a build's credential is minted at the entry's own provider and not at the caller's choice of host"}
			}
			return nil, buildRepos{status: StatusUnavailable, typ: ErrorStateUnavailable,
				message: "the repository " + name + ", which the image map puts on " + req.Entry + ", is not one this installation serves at " + req.Provider}
		}
		owner, short, ok := strings.Cut(name, "/")
		if !ok || owner == "" || short == "" || strings.Contains(short, "/") {
			return nil, buildRepos{status: StatusUnavailable, typ: ErrorStateUnavailable,
				message: "the project's image map puts " + name + " on " + req.Entry + ", and it is not a repository name this service can read"}
		}
		out = append(out, buildRepo{name: name, canonical: canonical, slug: slug, owner: owner, short: short})
	}
	return out, nil
}

// entrySourceRepo is the canonical repository a catalog entry's Dockerfile is
// sourced from, as the published policy's catalog names it. An entry with no
// Dockerfile source (an inline definition, a prebuilt image) or absent from the
// catalog has none, and the map stays the only authority for it.
func entrySourceRepo(shared *projectpolicy.Policy, entry string) (string, bool) {
	if shared == nil || shared.ImageCatalog == nil {
		return "", false
	}
	for _, e := range shared.ImageCatalog.Entries {
		if e.Name != entry || e.Source.Kind != config.SourceDockerfile {
			continue
		}
		canonical, err := task.CanonicalRepo(e.Source.Repo)
		if err != nil {
			return "", false
		}
		return canonical, true
	}
	return "", false
}

// reposElsewhere reports whether the installation serves name at the provider
// other than the one the call named, which is what tells "the call named the
// wrong provider" from "the project does not have this repository at all".
func (s *Server) reposElsewhere(name, provider string) bool {
	return s.servesRepo(otherProvider(provider), name)
}

// servesRepo reports whether this installation's own table holds repo at
// provider: the enrolled set a build's credential may reach (R9), read the way
// githubRepository reads a repository and not a second way.
func (s *Server) servesRepo(provider, repo string) bool {
	slug, err := task.Slug(provider, repo)
	if err != nil {
		return false
	}
	canonical, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return false
	}
	return s.cfg.Repos[slug] == canonical
}

// otherProvider is the provider the call did not name: owner/name is not unique
// across hosts, so it is how "the call named the wrong provider" is told from
// "the project does not have this repository at all".
func otherProvider(provider string) string {
	if provider == gitprov.KindGitHub {
		return gitprov.KindBitbucket
	}
	return gitprov.KindGitHub
}

// buildToken answers one build call: it verifies its caller, reads its call,
// narrows what the call may be given against the entry's own repositories, and
// answers the half the provider names.
//
// The order below is githubRead's order step for step, and the reasons are the
// same ones:
//
//   - the caller, first, and on its own token. This is the route's whole
//     admission, and a refusal here is typed and has NO LINE, because nothing is
//     verified yet and there is no caller for a line to name.
//   - the run identity and the job name the class does not take, refused BY NAME
//     before anything else is read: a build holds no run identity, so a call
//     presenting one is a call of a shape this route does not have. These two
//     refusals ARE recorded — the caller is verified — and they are recorded with
//     the route's own purpose and no credential, because the provider (and so
//     which of the two acts the call asked for) is in the body, which nothing
//     has read yet.
//   - the call's body, whose read is ALSO the registration of the caller's own
//     token with the call-scoped scrub.
//   - the operator's stop flag, read BEFORE the policy and the bundle, so a
//     stopped service reads nothing and issues nothing. It is the global switch
//     alone, with no slug (F34): this route has no run.
//   - the published policy, which is where the entry's repositories come from,
//     and the narrowing against it.
//   - the bundle and the App key, for the GitHub half alone, failing closed.
//   - the mint, against api.github.com and no other host.
//   - the answer, built, audited and handed over in that order.
func (s *Server) buildToken(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, credential := routeAct(r.URL.Path)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyBuildCaller(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way the read route answers the
		// person class's: a typed refusal and no line.
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it issues a credential to a project's build, which holds none, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it issues a credential to a project's build, which holds none, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readBuildRequest(cl.body)
	if err != nil {
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	// The provider is read, so the act the call asked for is known and both facts
	// of its line are: a GitHub call issues from the App key, a Bitbucket call
	// issues nothing and is recorded under the grant's own purpose.
	if req.Provider == gitprov.KindBitbucket {
		purpose, credential = PurposeBuildGrant, ""
	} else {
		purpose, credential = PurposeBuildToken, config.GitHubAppKeySecret
	}
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so the
		// call is refused rather than served.
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil || !found {
		// A node that is not there is not a read that failed, and labelRead
		// answers a nil err as nil: the two share one sentence. The service has no
		// image map to narrow against, and a build is never answered a credential
		// over "no set at all" (D143, fail closed).
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusUnavailable, ErrorStateUnavailable,
			"the project's policy could not be read, so the entry's repositories could not be read")
		return
	}
	entry, err := s.buildEntryRepos(shared, req)
	if err != nil {
		var refused buildRepos
		if errors.As(err, &refused) {
			s.refuseBuild(ctx, w, caller, purpose, credential, refused.status, refused.typ, refused.message)
			return
		}
		s.refuseBuild(ctx, w, caller, purpose, credential, StatusUnavailable, ErrorStateUnavailable,
			"the entry's repositories could not be read")
		return
	}
	if req.Provider == gitprov.KindBitbucket {
		s.answerBuildGrant(ctx, w, caller, req, entry)
		return
	}
	s.answerBuildToken(ctx, w, caller, req, entry)
}

// answerBuildToken issues the GitHub half: one read-only installation token
// covering exactly the request's repositories of the caller's own project.
//
// The scope is the REQUEST's repositories and not the entry's (D2301's own
// narrowing, spec decision 4): a build that bakes one of an entry's two
// repositories is answered a token over that one, and the token's reach is what
// bounds it — not its kind, the same way a run's token is bounded.
//
// One installation token covers one installation, and an installation belongs to
// one owner, so the request's repositories must be one owner's: an entry whose
// map puts its repositories on two owners is one no single token covers, and the
// service refuses rather than minting half of what was asked. That state is the
// project's map, which the caller cannot fix, so it is the unavailable answer.
func (s *Server) answerBuildToken(ctx context.Context, w http.ResponseWriter, caller string, req BuildTokenRequest, entry []buildRepo) {
	byName := make(map[string]buildRepo, len(entry))
	for _, r := range entry {
		byName[r.name] = r
	}
	asked := make([]buildRepo, 0, len(req.Repositories))
	for _, name := range req.Repositories {
		asked = append(asked, byName[name])
	}
	owner := asked[0].owner
	names := make([]string, 0, len(asked))
	for _, r := range asked {
		if r.owner != owner {
			s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable,
				"the entry's repositories are not one GitHub owner's, and one installation token covers one owner's repositories: this entry is one credential this service cannot issue")
			return
		}
		names = append(names, r.short)
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		// The App key's own two refusals, respelled so that a build's operator
		// reads the act it was asked for: the sentences are the mint's own and say
		// the same thing, because it is the same key in the same bundle that is
		// missing or malformed.
		if errors.Is(err, ErrMintNoAppKey) {
			err = ErrBuildNoAppKey
		} else {
			err = ErrBuildBadAppKey
		}
		s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	token, expires, err := github.MintRepositoriesToken(ctx, github.Options{
		Owner: owner, AppID: req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	}, names, github.BuildTokenPermissions())
	if err != nil {
		// The operator gets the class of the failure and, when GitHub answered,
		// its status — and never a byte of GitHub's own text: the request that
		// failed carried the App's JWT, so an upstream that echoed the request
		// would be putting a bearer credential in this service's log.
		attrs := []any{"purpose", PurposeBuildToken, "credential", config.GitHubAppKeySecret, "build", req.BuildID}
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			attrs = append(attrs, "status", se.Status)
		}
		s.log.Warn("credsvc: a build's installation token could not be minted", attrs...)
		s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable, ErrBuildGitHub.Error())
		return
	}
	// The host is the provider's own and not the request's word about it: a
	// credential file is read by git for the host in its line, so the answer names
	// the one host this token may be sent to (rule 5), beside the user spelled by
	// the provider rather than derived by the caller.
	s.answerBuild(ctx, w, caller, req.BuildID, buildtoken.Response{Token: &buildtoken.Token{
		Value:     token,
		User:      github.GitUsername,
		Host:      repourl.ProviderHost(req.Provider),
		ExpiresAt: expires,
	}})
}

// answerBuildGrant answers the Bitbucket half: the repositories paired to this
// build's identity, and no token at all (R6, D2302).
//
// The pairing is the ENTRY's repositories and not the request's subset, which is
// the contract's own words ("the service pairs the build's identity to the
// entry's repositories for this build") and the plan's: the proxy answers for the
// entry a build bakes, and a request naming a repository outside the entry's set
// was refused before this was reached. The request's own narrowing is what the
// build's clone step checks out; the fence around the entry is this.
//
// The URL is this service's own, from the deployment and never from the request's
// Host header — a grant URL built from a caller's text would send a build's clone
// wherever its caller said. A service with no URL of its own answers no grant: one
// naming no proxy would leave a build's git with nowhere to go, and the answer
// would be a lie rather than a refusal.
func (s *Server) answerBuildGrant(ctx context.Context, w http.ResponseWriter, caller string, req BuildTokenRequest, entry []buildRepo) {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	if base == "" {
		s.refuseBuild(ctx, w, caller, PurposeBuildGrant, "", StatusUnavailable, ErrorStateUnavailable,
			"this installation's credentials service has no URL of its own, so it cannot name the proxy a build's git would reach")
		return
	}
	repos := make([]string, 0, len(entry))
	for _, r := range entry {
		repos = append(repos, r.name)
	}
	expires := s.now().Add(clampBuildTTL(req.TTLSeconds))
	// The pairing is RECORDED before the grant is answered (rule 9), and this is
	// what makes the grant an answer rather than a promise: what bounds a build's
	// git is the record the proxy's own admission reads (creds.BuildPair,
	// bitbucketproxy.go's buildProxy), so a pairing that was not written is a
	// grant naming a proxy that would refuse every call the build makes. That is
	// BaseURL's rule one paragraph up, and the same fail-closed reading of it: an
	// answer that would send a build's git at a fence it cannot pass is a lie
	// rather than a refusal.
	if s.cfg.BuildPairs == nil {
		s.refuseBuild(ctx, w, caller, PurposeBuildGrant, "", StatusUnavailable, ErrorStateUnavailable,
			"this installation's credentials service cannot record the pairing a build's grant describes, so it cannot answer one")
		return
	}
	if err := s.cfg.BuildPairs.PutBuildPair(ctx, creds.BuildPair{
		Identity:     caller,
		BuildID:      req.BuildID,
		Entry:        req.Entry,
		Repositories: repos,
		Expires:      expires,
	}); err != nil {
		// The cause reaches the service's own log line and nothing else: a store
		// that could not answer is not the caller's refusal, and the refusals
		// above the credential's issuance are the operator's to see either way.
		s.log.Error("credsvc: a build's pairing was not recorded", "error", err)
		s.refuseBuild(ctx, w, caller, PurposeBuildGrant, "", StatusUnavailable, ErrorStateUnavailable,
			"the pairing a build's git reaches the entry's repositories through could not be recorded, and a grant this service cannot record is one it will not answer")
		return
	}
	s.answerBuild(ctx, w, caller, req.BuildID, buildtoken.Response{Proxy: &buildtoken.ProxyGrant{
		URL:          base + BitbucketProxyPrefix,
		Repositories: repos,
		ExpiresAt:    expires,
	}})
}

// clampBuildTTL is the life a build's credential is given (rule 7): the request's
// need, capped. It is slip.go's clampRuntime and not a second reading of the same
// question — a request that names no life at all was refused by the reader — so
// what is left here is a need this service bounds rather than trusts.
func clampBuildTTL(seconds int) time.Duration {
	asked := time.Duration(seconds) * time.Second
	if asked > buildTokenMaxTTL {
		return buildTokenMaxTTL
	}
	return asked
}

// answerBuild builds the build route's answer, audits what the answer ISSUED and
// writes it. It is answerGitHubRead's order and answerGitHubRead's reasons: the
// answer is built first so that a body the service cannot build is a refusal with
// its own line and no issuance line at all, and the audit line is written before
// the credential is handed over, because an issuance that cannot be audited is
// not made (C1.13, D143) and a GitHub installation token cannot be unminted.
//
// The grant is the one answer that issues nothing, and so the one that writes no
// line: it names no credential (PurposeBuildGrant, checked), and what it records
// is the service's own putting-up of a pairing rather than a credential's
// issuance. It is the launcher's grant of a run's slip, whose line the same rule
// holds (PurposeSlipGrant).
func (s *Server) answerBuild(ctx context.Context, w http.ResponseWriter, caller, buildID string, ans buildtoken.Response) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the build route's answer could not be built", "error", err)
		s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if ans.Token == nil && ans.Proxy == nil {
		// Unreachable from the two builders above, and refused rather than
		// written: the contract says exactly one shape, and a build answered
		// neither has nothing to clone with.
		s.log.Error("credsvc: the build route built an answer that is neither a token nor a grant", "build", buildID)
		s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if ans.Token != nil {
		if err := s.auditBuild(ctx, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusOK); err != nil {
			s.log.Error("credsvc: a build's audit line was not written", "error", err)
			s.refuseBuild(ctx, w, caller, PurposeBuildToken, config.GitHubAppKeySecret, StatusUnavailable, ErrorStateUnavailable, ErrBuildUnaudited.Error())
			return
		}
	}
	writeReadAnswer(w, body)
}

// auditBuild writes one build route line: the project's build identity as its
// caller, and NEITHER a run nor a job — a build holds no run identity and is a
// job of no repository. The issuance line names no REPOSITORY either, and that is
// the one place it differs from every other issuance line of this service: the
// act is one credential over several repositories, so a line naming one of them
// would say the issuance was for that one (PurposeBuildToken's own rule).
//
// purpose and credential are the CALLER's here and not routeAct's, because this
// route has two acts and the provider that decides between them is in the body:
// the GitHub half issues from the App key under PurposeBuildToken, and the
// Bitbucket half issues nothing and is recorded under PurposeBuildGrant.
//
// It does not go through Identity.Audit because there is no Identity: the route
// is answered ahead of Verify, which is the only thing that builds one.
func (s *Server) auditBuild(ctx context.Context, caller, purpose, credential, status string) error {
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseBuild answers one build refusal and records it, in githubRead's shape: one
// typed refusal to the caller, one line to the operator, and the caller's answer
// is the same whether or not the line lands (D143).
//
// It is reached only once the caller IS verified — the refusals above that point
// are answered by refuse and refuseVerifier alone, with no line, because an
// unverified caller has no name for one.
func (s *Server) refuseBuild(ctx context.Context, w http.ResponseWriter, caller, purpose, credential string, status int, typ, message string) {
	if err := s.auditBuild(ctx, caller, purpose, credential, typ); err != nil {
		s.log.Error("credsvc: a refused build call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// The build route's own refusals. They are sentinels so a test and a reader can
// tell them apart, and every one is a sentence written here: GitHub's own answer
// never reaches a caller, because the request that failed carried the App's JWT.
var (
	// ErrBuildNoAppKey is a bundle that holds no GitHub App key. It is the
	// fail-closed case the mint's own plan amendment names (D143): a service that
	// minted without one, or fell back to a key the caller holds, would be
	// indistinguishable from a broken installation.
	ErrBuildNoAppKey = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	// ErrBuildBadAppKey is a bundle whose App key is not a private key this code
	// can sign with.
	ErrBuildBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrBuildGitHub is GitHub refusing the mint, or the call to it failing.
	ErrBuildGitHub = errors.New("credsvc: GitHub did not mint a build's installation token")
	// ErrBuildUnaudited is a token GitHub granted and C1.13's auditor could not
	// record. The token exists at GitHub and reaches nobody: this service will not
	// hand out a credential it could not write a line for (D143).
	ErrBuildUnaudited = errors.New("credsvc: a build's mint could not be audited, so no installation token was issued")
)
