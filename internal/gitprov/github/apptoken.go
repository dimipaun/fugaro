// Package github is the GitHub adapter: App JWT authentication and
// repo-scoped installation tokens (design §6.1, §6.2).
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// ParsePrivateKey parses a GitHub App private key in PEM form: PKCS#1
// ("RSA PRIVATE KEY", what GitHub issues) or PKCS#8 ("PRIVATE KEY").
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("github app private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing github app private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app private key is not an RSA key")
	}
	return rk, nil
}

// appJWT returns the RS256 JSON Web Token that authenticates as the App.
// It is backdated a minute against clock drift and lives 9 minutes, under
// GitHub's 10-minute maximum.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing github app jwt: %w", err)
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

// appAuth returns an httpjson.Client Auth function that sends a fresh App JWT.
func appAuth(appID string, key *rsa.PrivateKey, now func() time.Time) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		jwt, err := appJWT(appID, key, now())
		if err != nil {
			return "", err
		}
		return "Bearer " + jwt, nil
	}
}

// tokenPermissions is what a run's installation token asks for (design
// §6.1): no more than the run needs, even if the App was granted more.
// RunTokenPermissions is its exported copy, for the caller outside this
// package that mints a run's token itself (the credentials service, C2.1) and
// must ask for exactly these and add none.
var tokenPermissions = map[string]string{
	"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read",
}

// issueTokenPermissions is what the credentials service's issue route asks for
// when it performs an issue act itself (B3 of
// docs/orchestrator-graph-estimates/implementation-plan.md, D2718): the issues
// it lists and reads and the one it opens, and metadata: read, the noun every
// installation token carries. GitHub has no issue permission but the one the
// creation needs — a repository's open issues are read with `issues: write`
// exactly as they are written with it — so the one noun covers all three acts.
//
// It is the only set here that no run's token carries: tokenPermissions keeps
// the read a run was already given, and this write belongs to the service,
// which performs the act and hands no token over, so it is never in a job's
// container. The App's installation must grant it all the same, and that is
// what makes RequiredPermissions union this set in (installcheck.go): the
// service mints from the installation, and GitHub refuses a mint that asks for
// a permission the installation does not hold.
var issueTokenPermissions = map[string]string{"issues": "write", "metadata": "read"}

// buildTokenPermissions is what an image build's token asks for: it only
// clones the repository.
var buildTokenPermissions = map[string]string{"contents": "read", "metadata": "read"}

// shadowTokenPermissions is what a challenger arm's installation token asks
// for (A2.1, docs/model-ab-testing): the read a checkout of the repository
// needs and nothing that could write to it. A challenger pushes nothing
// (D554), so the run it belongs to must not hold a token that could, whatever
// the agent did with it — this is the clone's credential and the only one that
// run ever has (GitAuthReadOnly, which offers no GH_TOKEN).
//
// It is the same reach a build's token asks for, and deliberately a set of its
// own: the build's is about what an image build reads and this one about what
// a shadow may reach, so a later change to either must not silently widen the
// other, and shadow_token.json pins this one's mint body as
// coordinator_token.json pins the coordinator's.
var shadowTokenPermissions = map[string]string{"contents": "read", "metadata": "read"}

// coordinatorTokenPermissions is what the coordinator's token asks for (P2 of
// docs/coordinator/implementation-plan.md, reconciliation Q3 answered (A)):
// the reads the project-wide view is built from, and no value carrying a write
// anywhere. A coordinator that could write would be a coordinator that could
// answer a merge (Q10 C, refused), which is why this set is a set of its own
// rather than a level taken off the run's.
//
// Two permissions, because two are what this read uses: every pull request is
// read one at a time through gitprov.Provider.PullRequest -- GET
// /repos/{owner}/{repo}/pulls/{number} -- which is pull_requests: read, and
// metadata: read, which GitHub requires of any request at all. contents and
// issues were asked for and never used: this reader opens no file and reads no
// issue, and a permission nobody uses is reach a stolen token would have for
// nothing. P5's aggregation job re-reads through this same reader, so it asks
// for no more than these two.
var coordinatorTokenPermissions = map[string]string{
	"pull_requests": "read", "metadata": "read",
}

// BuildTokenPermissions are the permissions of an image build's token, a
// copy the caller may keep.
func BuildTokenPermissions() map[string]string { return maps.Clone(buildTokenPermissions) }

// CoordinatorTokenPermissions are the permissions of the coordinator's
// installation token, a copy the caller may keep: the read-only set the
// coordinator's credential is, minted through MintInstallationToken with it.
// Pull requests are read with pull_requests: read (U2, the owner's check that
// the App holds it), and nothing here asks to write anything — the rule the
// set's own test holds to the mint's body.
func CoordinatorTokenPermissions() map[string]string {
	return maps.Clone(coordinatorTokenPermissions)
}

// RunTokenPermissions are the permissions of a run's installation token, a
// copy the caller may keep. It is the exported spelling of tokenPermissions,
// which is the set the in-process provider mints with: a caller that mints a
// run's token through MintInstallationToken (the credentials service, C2.1)
// asks for this and adds nothing, so the two paths cannot drift into asking
// GitHub for different reaches on the same run.
func RunTokenPermissions() map[string]string { return maps.Clone(tokenPermissions) }

// IssueTokenPermissions are the permissions of the credentials service's issue
// route, a copy the caller may keep. It is the exported spelling of
// issueTokenPermissions, so the set the route asks GitHub for and the set
// RequiredPermissions holds the installation to cannot drift apart: the service
// mints what `fugaro doctor` requires, and an installation that grants only
// `issues: read` is a missing grant rather than a refusal at the first issue.
func IssueTokenPermissions() map[string]string { return maps.Clone(issueTokenPermissions) }

// reviewTokenPermissions is what a review job's installation token asks for
// (CSR1 of docs/cloud-senior-review/implementation-plan.md, D883): the head it
// reads and the pull request it reviews, and no value carrying a write
// anywhere. It is the fifth set, beside the run's, the build's, the challenger
// arm's and the coordinator's, and a set of its own rather than a level taken
// off the run's for the coordinator's reason: a review job is not a run, so a
// later change to either must not silently widen the other.
//
// A write here would be a write credential in the container that runs the head
// under review -- and the read-only set is not a boundary by itself (F3): what
// makes it one is the job that holds it, which mounts no App key, so the token
// is minted by the credentials service and fetched by the job (D893 (c)).
// This package builds no caller for this set, because every mint in this tree
// happens where the App key is mounted and a review job holds no key (F16).
var reviewTokenPermissions = map[string]string{
	"contents": "read", "pull_requests": "read", "metadata": "read",
}

// ReviewTokenPermissions are the permissions of a review job's installation
// token, a copy the caller may keep: the read-only set the credentials
// service's mint must serve for a review identity (C3.9). Nothing here asks to
// write anything, and the set's own test holds it to exactly these three.
func ReviewTokenPermissions() map[string]string { return maps.Clone(reviewTokenPermissions) }

// personReadTokenPermissions is what the credentials service's PERSON read mint
// asks for (D915 of docs/run-without-checkout/implementation-plan.md, task A0):
// the reads that route's callers make and no value carrying a write anywhere.
//
// It is the coordinator's two plus one, and both halves are load-bearing. The
// route is one route, so the set has to reach the read `fugaro coordinator show`
// makes with a person's token as well as the one it was extended for: a pull
// request is read one at a time (pull_requests: read), and metadata: read is
// what GitHub requires of any request at all. contents: read is the file read
// the route serves since A0 -- a repository's own fugaro.yaml at a ref, through
// gitprov.ContentsReader -- and without it GitHub refuses that read, which is
// exactly what the person read mint did before this set existed.
//
// It is a set of its own rather than coordinatorTokenPermissions widened, and
// that is the whole point of the two pins around the two: the coordinator mints
// with that constant, it opens no file, and a permission asked for and never
// used is reach a stolen token would have for nothing. The mint asks for what
// the App holds; a set the App is not granted fails at GitHub, which is a
// refusal the operator reads, not a silent narrowing.
var personReadTokenPermissions = map[string]string{
	"contents": "read", "pull_requests": "read", "metadata": "read",
}

// PersonReadTokenPermissions are the permissions of the credentials service's
// person read mint (githubread.go), a copy the caller may keep: the read-only
// set that route asks with. Nothing here asks to write anything, and the set's
// own test holds it to exactly these three.
func PersonReadTokenPermissions() map[string]string { return maps.Clone(personReadTokenPermissions) }

// contextTokenPermissions is what a run's CONTEXT repository tokens ask for
// (C5b of docs/forge-authorship-images/implementation-plan.md, D613): the read
// a clone of a repository the run only READS needs, and no value carrying a
// write anywhere.
//
// Two permissions, and both are load-bearing: contents: read is the clone
// itself, which is the whole of what a context repository is for, and
// metadata: read is what GitHub requires of any request at all. Nothing of a
// context repository is ever written — it is cloned outside the work tree and
// given no push URL (C5c) — so a token that could write would be reach a
// stolen credential would have for nothing.
//
// It is a set of its own rather than the build's or the challenger arm's
// reused, although the three ask for the same two permissions today: what an
// image build reads, what a challenger arm may reach and what a context
// repository is read with are three reaches with three reasons, and a later
// change to any one of them must not silently widen the other two.
// TestContextTokenIsReadOnly holds this set to exactly these two and to the
// body the mint sends with it.
var contextTokenPermissions = map[string]string{"contents": "read", "metadata": "read"}

// ContextTokenPermissions are the permissions of a context repository's
// installation token, a copy the caller may keep: nothing here asks to write
// anything.
func ContextTokenPermissions() map[string]string { return maps.Clone(contextTokenPermissions) }

// GitUsername is the HTTPS username git uses with an installation token.
const GitUsername = gitUsername

// buildTokenMinValid is how long a build's token must still be valid when
// minted: the build clones again in its last step, well within this.
const buildTokenMinValid = 30 * time.Minute

// MintInstallationToken mints one installation token for o's repository,
// scoped to it alone and to perms, and valid for at least half an hour
// (GitHub gives it about an hour). It returns the token and its expiry.
func MintInstallationToken(ctx context.Context, o Options, perms map[string]string) (string, time.Time, error) {
	if o.Owner == "" || o.Repo == "" {
		return "", time.Time{}, errors.New("github: owner and repository are required")
	}
	if o.AppID == "" || o.PrivateKey == nil {
		return "", time.Time{}, errors.New("github: an App ID and private key are required")
	}
	if len(perms) == 0 {
		return "", time.Time{}, errors.New("github: an installation token needs its permissions")
	}
	app, o := appClient(o)
	src := &tokenSource{app: app, owner: o.Owner, repo: o.Repo, now: o.Now, permissions: maps.Clone(perms)}
	return src.Token(ctx, buildTokenMinValid)
}

// MintRepositoriesToken mints ONE installation token covering several of o's
// repositories at once, scoped to exactly them and to perms, and valid for at
// least half an hour — the shape a catalog build asks for, because one build
// bakes several repositories and clones them with one credential (D2301,
// docs/shared-jobs-default/contract-build-token.md).
//
// It is MintInstallationToken's sibling and not a widening of it: a run's
// token is scoped to the one repository its job is bound to, and this one
// exists because a build's reach is its ENTRY's repositories. Both narrow the
// same way — GitHub scopes the token to the names in the request and to
// nothing else — and both are read-only only because of the permission set the
// caller passes.
//
// The repositories are names within o's owner and not owner/name pairs: a
// GitHub App installation belongs to the owner, the endpoint below is that
// installation's, and GitHub answers a token covering the repositories it is
// installed on. A name carrying a slash is therefore not a repository of this
// owner, and it is refused here rather than sent to GitHub, because the answer
// would be a token covering less than the caller asked for — and a build that
// cloned with it would fail somewhere further away from the cause.
func MintRepositoriesToken(ctx context.Context, o Options, repos []string, perms map[string]string) (string, time.Time, error) {
	if o.Owner == "" {
		return "", time.Time{}, errors.New("github: an owner is required")
	}
	if o.AppID == "" || o.PrivateKey == nil {
		return "", time.Time{}, errors.New("github: an App ID and private key are required")
	}
	if len(perms) == 0 {
		return "", time.Time{}, errors.New("github: an installation token needs its permissions")
	}
	names, err := repositoryNames(repos)
	if err != nil {
		return "", time.Time{}, err
	}
	app, o := appClient(o)
	src := &tokenSource{app: app, owner: o.Owner, repo: names[0], repos: names, now: o.Now, permissions: maps.Clone(perms)}
	return src.Token(ctx, buildTokenMinValid)
}

// repositoryNames holds the repositories of a multi-repository mint to what
// the endpoint takes: one name each, non-empty, carrying no slash, and no name
// twice. The last rule is the one an answer depends on — GitHub scopes a token
// to a set of repositories, and a name repeated in the request is a request
// that asks for the same thing twice, which is a caller's mistake this code
// refuses rather than a token this code mints.
func repositoryNames(repos []string) ([]string, error) {
	if len(repos) == 0 {
		return nil, errors.New("github: a multi-repository installation token needs at least one repository")
	}
	out := make([]string, 0, len(repos))
	seen := make(map[string]bool, len(repos))
	for _, name := range repos {
		if name == "" || strings.Contains(name, "/") {
			return nil, errors.New("github: a repository is a name within the owner and not an owner/name pair")
		}
		if seen[name] {
			return nil, errors.New("github: the same repository is named twice")
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// ContextToken is one installation token a run's context repositories are read
// with, and the repositories of one App installation it covers.
type ContextToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// Repositories are the repositories this token is scoped to, as "owner/name"
	// (task.CanonicalRepo's form, which is what the caller named them by), in
	// the order the caller named them. It is exactly the set GitHub scoped the
	// token to: GitHub answers a
	// token covering the names in the request and no others, so an answer that
	// named a different set — or the owner's every repository — would be a
	// credential whose reach it misstates.
	Repositories []string `json:"repositories"`
}

// MintContextTokens mints ONE installation token per App installation for the
// context repositories in repos, each scoped to exactly that installation's
// repositories and to perms, and each valid for at least half an hour (C5b,
// D613).
//
// One token per installation is what a run's context repositories need and not
// a choice: a GitHub App installation belongs to the owner, the endpoint below
// is that installation's, and a run's context list may name repositories under
// different owners, so a list spanning two owners is served by two tokens. One
// token cannot cover both, because GitHub scopes a token to the repositories of
// the installation it was minted for.
//
// It is MintRepositoriesToken's sibling and not a widening of it: a build's
// token covers its entry's repositories and this one a run's context list, and
// both are read-only only because of the permission set the caller passes
// (ContextTokenPermissions). What it adds is the grouping — which installation
// each repository belongs to — so that a caller holding a list of canonical
// names is served the tokens that list is read with and has no installation to
// work out for itself.
//
// The repositories are "owner/name" pairs — the form task.CanonicalRepo
// reduces a repository to, which the run's task and the identity's own claim
// hold them in — and are held to that shape here rather than sent to GitHub,
// for MintRepositoriesToken's reason: a name this code cannot read is a name
// whose answer would be a token covering less than the caller asked for, and a
// clone that failed somewhere further away from the cause. The provider is not
// part of the name, because the name is asked at THIS provider: the caller
// that has a canonical name has already read whose provider it is.
func MintContextTokens(ctx context.Context, o Options, repos []string, perms map[string]string) ([]ContextToken, error) {
	if o.AppID == "" || o.PrivateKey == nil {
		return nil, errors.New("github: an App ID and private key are required")
	}
	if len(perms) == 0 {
		return nil, errors.New("github: an installation token needs its permissions")
	}
	groups, err := contextGroups(repos)
	if err != nil {
		return nil, err
	}
	app, o := appClient(o)
	out := make([]ContextToken, 0, len(groups))
	for _, g := range groups {
		src := &tokenSource{app: app, owner: g.owner, repo: g.names[0], repos: g.names, now: o.Now, permissions: maps.Clone(perms)}
		tok, exp, err := src.Token(ctx, buildTokenMinValid)
		if err != nil {
			return nil, err
		}
		out = append(out, ContextToken{Token: tok, ExpiresAt: exp, Repositories: g.repos})
	}
	return out, nil
}

// contextGroup is the context repositories of ONE owner: the repositories as
// the endpoint takes them (names within the owner) and the canonical names
// those are, which is what the answer states.
type contextGroup struct {
	owner string
	names []string
	repos []string
}

// contextGroups holds a context list to what the mint endpoint takes: the
// repositories of one owner together, in the order they were given, and no
// repository twice.
//
// The order is the caller's and not a map's: the two lists of an owner are
// appended in the order the repositories arrived, so the same list answers in
// the same order twice, and an operator reading two answers of one run reads
// one order. A repository named twice is refused rather than deduplicated — a
// name repeated is a caller's mistake, and the set a token covers is a set.
func contextGroups(repos []string) ([]contextGroup, error) {
	if len(repos) == 0 {
		return nil, errors.New("github: a context token needs at least one repository")
	}
	var groups []contextGroup
	at := make(map[string]int, len(repos))
	seen := make(map[string]bool, len(repos))
	for _, canonical := range repos {
		owner, name, err := contextRepository(canonical)
		if err != nil {
			return nil, err
		}
		if seen[canonical] {
			return nil, errors.New("github: the same context repository is named twice")
		}
		seen[canonical] = true
		i, ok := at[owner]
		if !ok {
			groups = append(groups, contextGroup{owner: owner})
			i = len(groups) - 1
			at[owner] = i
		}
		groups[i].names = append(groups[i].names, name)
		groups[i].repos = append(groups[i].repos, canonical)
	}
	return groups, nil
}

// contextRepository splits one "owner/name" of a context list into the owner
// and the repository name GitHub's endpoint takes.
//
// The two refusals are the write mint's own (credsvc's mintRepository, which
// splits the calling job's repository the same way, and for the same reason):
// a name that is not two non-empty segments, and a name with a further "/" in
// it — a nested group, which this program's repository names allow and GitHub
// has no repository for. The token is scoped to what these two strings say, and
// GitHub takes them as given, so a name this code cannot read is a name it must
// not guess a repository out of: "a/b/c" sent as owner "a" and name "b/c" would
// be answered a token covering something the caller did not name, and one sent
// as owner "a/b" would be a repository of another owner's installation.
func contextRepository(repo string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", errors.New("github: a context repository is an owner and a name, and not a nested group")
	}
	return owner, name, nil
}

// appClient is the App-authenticated API client every mint above signs with:
// the API's own headers, and an Authorization that is a JWT the App's key
// signs. It holds nothing about WHAT is minted, which is what lets a run's
// mint, a build's and a context list's share one spelling of the endpoint's
// shape.
//
// It returns the options it was given with BaseURL and Now filled in, because
// the caller needs the clock the client signs with: a test that states the
// time means it for the token's window as well as for the JWT's.
func appClient(o Options) (*httpjson.Client, Options) {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &httpjson.Client{BaseURL: strings.TrimSuffix(o.BaseURL, "/"), HTTP: o.HTTP, Auth: appAuth(o.AppID, o.PrivateKey, o.Now), Header: http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
		"User-Agent":           {"fugaro"},
	}}, o
}

// RevokeInstallationToken revokes one installation token at GitHub
// (DELETE /installation/token), authenticated with the token itself: that is
// the whole identity the call needs, and the App's JWT is not one of them. The
// endpoint revokes the token that carries it and no other, which is why this
// takes a token rather than a repository or an installation.
//
// It exists because a token minted for one read is a credential with a life of
// its own after the read: the coordinator mints one per repository (D193 A),
// hands it to a provider, and is done with it when the view is printed. The
// call is best-effort by contract, and this function does not soften that -- it
// reports what happened and the caller decides, which is where "a revoke that
// failed is not a read that failed" belongs.
//
// Neither the failure nor the success here carries the token: what is returned
// is the method, the path, the status and GitHub's own text, none of which is
// the value. The request itself is made with the client the credentials service
// mints through (serviceMintClient): a bearer credential does not follow a
// redirect to another host, and a call that cannot be answered is cut off
// rather than held.
func RevokeInstallationToken(ctx context.Context, baseURL, token string, hc *http.Client) error {
	if token == "" {
		return errors.New("github: there is no installation token to revoke")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	c := &httpjson.Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		HTTP:    serviceMintClient(hc),
		Auth:    func(context.Context) (string, error) { return "Bearer " + token, nil },
		Header: http.Header{
			"Accept":               {"application/vnd.github+json"},
			"X-Github-Api-Version": {"2022-11-28"},
			"User-Agent":           {"fugaro"},
		},
	}
	if err := c.Do(ctx, "DELETE", "/installation/token", nil, nil); err != nil {
		return fmt.Errorf("revoking an installation token: %w", err)
	}
	return nil
}

// tokenSource mints installation tokens scoped to a repository or to several
// and to permissions (nil means a run's, tokenPermissions), and caches the
// current one until it gets close to expiring.
type tokenSource struct {
	app         *httpjson.Client // authenticated as the App (JWT)
	owner, repo string
	// repos is every repository the token is scoped to, and nil means the one
	// above: a run's token covers one repository and a build's covers its
	// entry's several (MintRepositoriesToken). repos[0] names the installation
	// either way, because the lookup below is per repository and one token
	// covers one owner's.
	repos        []string
	permissions  map[string]string
	now          func() time.Time
	mu           sync.Mutex
	installation int64
	token        string
	expires      time.Time
}

// where names what a token is being minted for, in the messages Token writes:
// one repository ("acme/web"), or every repository a build's token covers
// ("acme/web acme/api"). A failure that named only the first repository of a
// multi-repository mint would send an operator to a request that covers more
// than the one they read.
func (s *tokenSource) where() string {
	names := s.repos
	if len(names) == 0 {
		names = []string{s.repo}
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = s.owner + "/" + name
	}
	return strings.Join(out, " ")
}

// Token returns an installation token valid for at least minValid, minting
// a new one when the cached token would expire sooner. A new token lives
// about an hour, so a minValid beyond that mints on every call.
func (s *tokenSource) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.expires.Sub(s.now()) >= minValid {
		return s.token, s.expires, nil
	}
	if s.installation == 0 {
		var inst struct {
			ID int64 `json:"id"`
		}
		path := "/repos/" + url.PathEscape(s.owner) + "/" + url.PathEscape(s.repo) + "/installation"
		if err := s.app.Do(ctx, "GET", path, nil, &inst); err != nil {
			return "", time.Time{}, fmt.Errorf("finding the github app installation for %s: %w", s.where(), err)
		}
		if inst.ID == 0 {
			return "", time.Time{}, fmt.Errorf("github returned no installation id for %s", s.where())
		}
		s.installation = inst.ID
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	perms := s.permissions
	if perms == nil {
		perms = tokenPermissions
	}
	scope := s.repos
	if len(scope) == 0 {
		scope = []string{s.repo}
	}
	body := map[string]any{"repositories": scope, "permissions": perms}
	if err := s.app.Do(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", s.installation), body, &tok); err != nil {
		return "", time.Time{}, fmt.Errorf("minting an installation token for %s: %w", s.where(), err)
	}
	if tok.Token == "" {
		return "", time.Time{}, fmt.Errorf("github returned an empty installation token for %s", s.where())
	}
	// The GitAuth contract (gitprov.go) promises a token valid for at least
	// minValid; a token that GitHub minted with too little life left (or a
	// missing/zero expiry) cannot honor that, so it is an error rather than
	// a token the caller would treat as good.
	if tok.ExpiresAt.Sub(s.now()) < minValid {
		return "", time.Time{}, fmt.Errorf(
			"github minted an installation token for %s that expires too soon to satisfy the requested %s validity",
			s.where(), minValid)
	}
	s.token, s.expires = tok.Token, tok.ExpiresAt
	return s.token, s.expires, nil
}

// The contract of the credentials service's mint (C2.1, D145): the path, the
// two headers that carry the identities a mint is verified by, and the fields
// of the request and the answer. They are spelled here rather than imported
// because this package cannot import internal/credsvc -- credsvc imports this
// one, for MintInstallationToken and RunTokenPermissions -- and internal/credsvc's
// TestTheServiceTokenContractIsTheMints holds every one of them to the
// service's own spelling, so the two cannot drift apart.
const (
	// ServiceTokenPath is where the service mints a run's installation token.
	ServiceTokenPath = "/v1/github/installation-token"
	// ServiceContextTokenPath is where the service mints a run's read-only
	// context-repository tokens (C5b, D613). It is a route of its own and not a
	// field of the mint above: the write mint carries no repository and must go
	// on carrying none (docs/git-providers.md says so, and
	// internal/cli's docs test pins it against MintRequest), while this call's
	// whole subject is the repositories the run asks to read — which are the
	// ones its own identity's claim names, and nothing a caller can widen.
	ServiceContextTokenPath = "/v1/github/context-tokens"
	// ServiceHeaderRunIdentity carries the run's own Firebase ID token: the
	// identity the service verifies the run by (credsvc.HeaderRunIdentity).
	ServiceHeaderRunIdentity = "X-Fugaro-Run-Identity"
	// ServiceHeaderRunJob names the Cloud Run job the run reports it is in
	// (credsvc.HeaderRunJob). The service holds it to the job-name rule and
	// decides nothing from it: which job called is decided from the verified
	// service-account email.
	ServiceHeaderRunJob = "X-Fugaro-Run-Job"
)

// maxServiceAnswerBytes bounds a mint's answer. It is one small object (a
// token, an expiry and the repository's name), so a longer answer is not one
// this contract wrote, and the refusal below is what an operator sees rather
// than a body of unknown size.
const maxServiceAnswerBytes = 16 << 10

// serviceMintTimeout bounds one mint call. A mint is a short call -- one
// request, one answer, no streaming -- so a service that neither answers nor
// fails is cut off here rather than holding the run until the platform's own
// job timeout. The in-job path has the same shape (the API clients' own
// timeouts) and this one has no stage deadline above it: the run's first mint
// is the clone's, which bootstrap asks for before any budget session exists.
const serviceMintTimeout = 30 * time.Second

// serviceMintRequest is a mint call's body and serviceMintAnswer is the
// service's answer (credsvc.MintRequest and MintResponse): the App's ID, and
// nothing else, because the repository is the one the calling job account is
// bound to and a body that named one is refused there.
type serviceMintRequest struct {
	AppID string `json:"app_id"`
}

type serviceMintAnswer struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// serviceContextTokenRequest is a context mint call's body and
// serviceContextTokenAnswer its answer (credsvc.ContextTokenRequest and
// ContextTokenResponse): the App's ID and the repositories the run asks to
// read.
//
// The list is the caller's ASK and never its reach. The repositories it may be
// served are the ones its own identity's claim names (C5a), which is what the
// service holds the ask to — so the field widens nothing, and a call naming a
// repository the launch did not record is refused and audited.
//
// The names are the ones the task and the claim hold them in: canonical
// "owner/name" (task.CanonicalRepo's form), which is the key space the
// published policy is read in, so what the service compares is a name and not
// a spelling a caller chose. The answer names each token's repositories in
// that same form.
type serviceContextTokenRequest struct {
	AppID        string   `json:"app_id"`
	Repositories []string `json:"repositories"`
}

type serviceContextTokenAnswer struct {
	Tokens []ContextToken `json:"tokens"`
}

// serviceWireError is the error code an answer carries when the service
// refused (credsvc's wire contract): a bounded word like "state_unavailable".
// The code is read to say WHICH refusal it was; the message beside it is
// another writer's text and is never relayed -- see mint.
var serviceErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// The refusals a service mint can end in. Every one of them is a sentence
// written here, and none of them carries a token, an identity or the
// service's own text: the value this endpoint exists to hand over reaches the
// caller and nothing else -- no log line, no error, and no other field.
var (
	// ErrServiceTokens is the credentials service refusing a mint, or the
	// call to it failing. A run halts on it (D143): the job holds no App key
	// of its own to fall back to.
	ErrServiceTokens = errors.New("github: the credentials service did not mint an installation token")
	// ErrServiceTokensIdentity is this run's inability to present the two
	// identities a mint is verified by. It is a fact about how the run was
	// launched -- a run with no budget identity is not one the service can
	// serve -- and never about the service.
	ErrServiceTokensIdentity = errors.New("github: the identities the credentials service verifies a mint with are not available")
)

// ServiceTokens is where a run's installation tokens come from when the
// installation's credentials service holds the App key (C2.2, D145): the
// service mints them, and the job holds no App key at all.
//
// It is the service's mint (C2.1) as a TokenSource, and it is deliberately
// shaped like the in-job tokenSource: the same cache, the same rule about
// minValid, and the same refusal of a token that could not satisfy the
// caller. What differs is where the token comes from and who signs for it --
// and that a mint is an act the service AUDITS (C1.13), so a token that is
// handed out and thrown away per call would be a line per API call. The cache
// is what keeps one mint per hour per run rather than one per call.
//
// Every field but HTTP, Job and Now is required, and a missing one is a
// refusal (ErrServiceTokensIdentity) rather than a call the service would
// answer with less: a mint without the run's own identity is not one the
// service verifies, and a call under the job's identity alone is not a call
// this code makes (C1.10).
type ServiceTokens struct {
	// URL is the credentials service's base URL, from the job's environment
	// (the installation's own Cloud Run service, C1.15/D147).
	URL string
	// AppID is the GitHub App's ID: a number, not a credential, and the one
	// thing a mint's body carries. The service's own environment does not
	// hold it (Global Constraint 12: one bundle secret and one grant), and
	// the job's does (FUGARO_GITHUB_APP_ID), so the caller names it -- and
	// cannot name anything else: the key that signs, the repositories and
	// the permissions are the service's.
	AppID string
	// HTTP is the client the call is made with; nil means this package's own
	// hardened one (serviceMintClient: no redirects, no jar, no compression,
	// a bounded timeout). A client passed in is copied with the same
	// redirect and jar rules; its transport and timeout stay the caller's.
	HTTP *http.Client
	// IDToken mints the job service account's Google ID token for URL, which
	// is what Cloud Run IAM admits for run.invoker (D135). It is called on
	// every mint and never cached here: see metadataIDToken in internal/runner
	// for why the job does not cache one.
	IDToken func(ctx context.Context) (string, error)
	// RunIDToken is the run's own Firebase ID token, the identity the budget
	// backend admitted it with (C1.10). It is read per call -- the session's
	// token changes under it -- and an empty value is a refusal: the service
	// verifies both identities on every call, and there is no second way to
	// be admitted.
	RunIDToken func() string
	// Job is the Cloud Run job name the run reports it is in. Empty is
	// allowed here and refused by the service: this is a claim the service
	// holds to its own rule and decides nothing from.
	Job string
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// ServiceTokens is the TokenSource a provider installs (gitprov.TokenSource)
// when the installation's service holds the App key.
var _ gitprov.TokenSource = (*ServiceTokens)(nil)

func (s *ServiceTokens) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Token returns an installation token valid for at least minValid, minting
// one at the service when the cached token would expire sooner -- the same
// contract, and the same rule, as the in-job tokenSource.
//
// The whole check-then-mint runs under the lock, as tokenSource's does: many
// goroutines (the API calls of a stage) reach Token at once, and a mint the
// service AUDITS must not happen once per caller.
func (s *ServiceTokens) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.expires.Sub(s.now()) >= minValid {
		return s.token, s.expires, nil
	}
	tok, exp, err := s.mint(ctx, minValid)
	if err != nil {
		return "", time.Time{}, err
	}
	s.token, s.expires = tok, exp
	return s.token, s.expires, nil
}

// mint makes one write-mint call and returns its token.
//
// The call itself is `call`'s (the identities, the transport, the status); what
// is here is the answer's own three rules, and each of them reaches nobody
// with a value: a token that is missing, or that could not satisfy minValid,
// is refused rather than returned, and no field of the answer is ever
// formatted into the refusal.
func (s *ServiceTokens) mint(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	body, err := json.Marshal(serviceMintRequest{AppID: s.AppID})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the mint's request could not be built", ErrServiceTokens)
	}
	data, err := s.call(ctx, ServiceTokenPath, body)
	if err != nil {
		return "", time.Time{}, err
	}
	var ans serviceMintAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: the service's answer is not a mint's", ErrServiceTokens)
	}
	if ans.Token == "" {
		// An empty token is no token: it would fail every git call as an
		// authentication error, deep in a stage, and it must never be
		// cached as if it were one.
		return "", time.Time{}, fmt.Errorf("%w: the service's answer carries no token", ErrServiceTokens)
	}
	if ans.ExpiresAt.Sub(s.now()) < minValid {
		// The GitAuth contract promises a token valid for at least minValid,
		// and the token has already been minted and audited at the service:
		// it reaches nobody, and it is never printed, so the caller gets the
		// refusal instead of a credential it could not honor.
		return "", time.Time{}, fmt.Errorf("%w: the token the service minted expires too soon to satisfy the requested %s validity", ErrServiceTokens, minValid)
	}
	return ans.Token, ans.ExpiresAt, nil
}

// call makes one call of this service's contract and returns its answer's
// bytes.
//
// It is what the two mints of this client have in common -- the fields a run
// must hold, the URL's scheme, the two identities as one header value each, the
// bounded answer and a refusal read for its status and error code alone -- and
// the reason it is one function is that those rules are the run's own
// presentation of itself and not a mint's: a rule added to one call (a header,
// the URL's scheme, the size of what may come back) that the other missed would
// be a second, weaker way for a run to reach the service.
//
// Nothing here can put a value in an error: neither identity token is ever
// formatted into one, and the service's own answer is read for the status and
// the error code alone. The message beside that code is the service's text --
// a sentence this project wrote, but written for the operator's log and not for
// a run, and an answer's body is another writer's to fill -- so it is dropped
// rather than relayed, exactly as the forward path's refusals are
// (credsvc/github.go's mint logs GitHub's status and never its text, for the
// same reason in the other direction). An answer over the bound is refused
// rather than read: it is a small object by contract, so a longer one is not
// one the service wrote.
func (s *ServiceTokens) call(ctx context.Context, path string, body []byte) ([]byte, error) {
	if s.URL == "" || s.AppID == "" || s.IDToken == nil || s.RunIDToken == nil {
		return nil, fmt.Errorf("%w: this run has no credentials service to call", ErrServiceTokensIdentity)
	}
	// The URL is checked before anything is sent to it. A call presents the
	// run's own Firebase ID token, which is a bearer credential for the whole
	// run: over plain http it would travel in cleartext, and a URL whose
	// scheme nobody chose is not one to hand it to. https, or http on a
	// loopback address, is the rule the model path already holds its own URL
	// to (internal/gateway's checkBaseURL, internal/runner's serviceUpstream).
	if err := checkServiceURL(s.URL); err != nil {
		return nil, err
	}
	run := s.RunIDToken()
	if run == "" {
		return nil, fmt.Errorf("%w: the run's own identity token is not available, and a call under the job's identity alone is not one the service verifies", ErrServiceTokensIdentity)
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: the job's identity token could not be minted: %v", ErrServiceTokensIdentity, err)
	}
	if id == "" {
		return nil, fmt.Errorf("%w: the metadata server answered with no identity token", ErrServiceTokensIdentity)
	}
	// One clean header value each: a token with a newline in it is refused
	// here rather than sent, the rule internal/runner keeps for the same two
	// values on the forward path.
	for _, v := range []string{run, id} {
		if !printableValue(v) {
			return nil, fmt.Errorf("%w: an identity token is not one header value", ErrServiceTokensIdentity)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: the call's request could not be built", ErrServiceTokens)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+id)
	req.Header.Set(ServiceHeaderRunIdentity, run)
	req.Header.Set(ServiceHeaderRunJob, s.Job)
	resp, err := serviceMintClient(s.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: the credentials service could not be reached", ErrServiceTokens)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxServiceAnswerBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the service's answer could not be read", ErrServiceTokens)
	}
	if len(data) > maxServiceAnswerBytes {
		return nil, fmt.Errorf("%w: the service's answer is larger than this contract's", ErrServiceTokens)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the service answered %d%s", ErrServiceTokens, resp.StatusCode, refusalCode(data))
	}
	return data, nil
}

// ContextTokens asks the service for read-only installation tokens over repos,
// one per App installation (C5b, D613).
//
// The repositories are canonical names (task.CanonicalRepo's "owner/name"),
// the spelling the task and the run identity's own claim hold them in, and they
// are the caller's ASK: the service serves the ones the run's identity names
// and refuses any other, so this method cannot widen what the launch granted.
//
// Nothing is cached, and that is the difference from Token: the token above is
// what the whole run makes its API calls with, so one mint per hour per run is
// the rule that keeps the audit log readable, while this is asked once for the
// list the run was launched to read and the answer is the caller's to hold.
// A caller that asked again would be asking the service to audit a second time
// for a credential it already has.
func (s *ServiceTokens) ContextTokens(ctx context.Context, repos []string) ([]ContextToken, error) {
	body, err := json.Marshal(serviceContextTokenRequest{AppID: s.AppID, Repositories: repos})
	if err != nil {
		return nil, fmt.Errorf("%w: the context mint's request could not be built", ErrServiceTokens)
	}
	data, err := s.call(ctx, ServiceContextTokenPath, body)
	if err != nil {
		return nil, err
	}
	var ans serviceContextTokenAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return nil, fmt.Errorf("%w: the service's answer is not a context mint's", ErrServiceTokens)
	}
	if len(ans.Tokens) == 0 {
		// A served call names at least one repository, so it is answered at
		// least one token: an answer carrying none is not this contract's, and
		// it must never be cached or read as "there was nothing to read".
		return nil, fmt.Errorf("%w: the service's answer carries no token", ErrServiceTokens)
	}
	for _, tok := range ans.Tokens {
		if tok.Token == "" {
			// An empty token is no token: it would fail the clone as an
			// authentication error, further away from the cause, exactly as an
			// empty one from the write mint would.
			return nil, fmt.Errorf("%w: the service's answer carries a token that is empty", ErrServiceTokens)
		}
		if len(tok.Repositories) == 0 {
			return nil, fmt.Errorf("%w: the service's answer carries a token that names no repository", ErrServiceTokens)
		}
		if tok.ExpiresAt.Sub(s.now()) < buildTokenMinValid {
			// The same rule the write mint is held to: a token that is already
			// close to expiring cannot be what a clone is made with, and it has
			// been minted and audited at the service, so it reaches nobody.
			return nil, fmt.Errorf("%w: a token the service minted expires too soon to read a context repository with", ErrServiceTokens)
		}
	}
	return ans.Tokens, nil
}

// checkServiceURL refuses a credentials service URL this code will not send a
// mint to: https, or http on a loopback address (a test's fixture server, or
// a service run on the run's own machine). It is internal/gateway's
// checkBaseURL rule for the model path (gateway.go) and internal/runner's for
// the forward path, respelled here because neither package exports it and
// this one cannot import them (credsvc imports this package, and the runner
// imports credsvc).
//
// The refusal carries the URL's shape and never the URL: the job's
// environment wrote it, and this package's refusals name no value.
func checkServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: the credentials service URL must be a scheme and a host only", ErrServiceTokens)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("%w: the credentials service URL must be https, or http on a loopback address", ErrServiceTokens)
}

// serviceMintClient is the client a mint is made with: it never follows a
// redirect, never keeps a cookie jar, and -- when this code builds the
// transport rather than being handed one -- never asks for compression on its
// own and never waits on an answer forever.
//
// The redirect half is the one that matters most here. A mint's request
// carries the run's own Firebase ID token in X-Fugaro-Run-Identity and the
// job's Google ID token in Authorization; Go's client strips Authorization
// across hosts but not a custom header, so a followed 307 or 308 would hand
// the run's identity to whatever host the Location named. Every other
// credential-carrying client in this repository refuses redirects for exactly
// that reason (internal/gateway's upstreamClient, internal/credsvc's own copy
// of it, pinned by forward_test.go). The URL's scheme is checked too
// (checkServiceURL): a followed http:// hop, or an http:// service URL, would
// send the same value in cleartext.
//
// A client handed in is copied, not used as it is, so its redirect policy and
// jar are this function's whatever the caller set -- the same as
// internal/gateway's upstreamClient. Its transport and its timeout stay the
// caller's.
func serviceMintClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
		out.Timeout = serviceMintTimeout
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// refusalCode is the wire error code of a refused answer, as ", refused: code",
// or "" when the answer carries none this contract could have written. Only
// the code is read: the message beside it is the service's text and is never
// relayed into a run's log or error.
func refusalCode(data []byte) string {
	var refusal struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &refusal) != nil || !serviceErrorCode.MatchString(refusal.Error) {
		return ""
	}
	return ", refused: " + refusal.Error
}

// printableValue reports whether v is one clean HTTP header value: non-empty
// printable ASCII with no space (internal/credsvc's own rule for a presented
// token, auth.go's maxTokenBytes and wire.go's printableHeaderValue).
func printableValue(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return false
		}
	}
	return true
}
