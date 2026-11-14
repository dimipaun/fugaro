package bbtokens

// The add-time check (T7, D2352, D2353, R14): the whole of what `fugaro creds
// set bitbucket-token--<name>` does before it writes anything. A token that
// does not work — or that does not reach what its record says it reaches — is
// refused in front of the operator, with one precise sentence, instead of
// failing later inside a run's fetch.
//
// Four things are the whole of its design.
//
//   - It reads and never writes, and it reads only within the coverage the
//     record itself states: the repository, the workspace's repository listing,
//     or the project's. D2352 puts the check in the CLI at `creds set`, where a
//     refusal writes nothing, and D2353 declines a write probe because
//     Bitbucket has no dry run for a push or a pull-request write (F8): nothing
//     is created in the operator's workspace, so D470's write scopes are
//     reported as NOT verified and the first real use is what tries them. A
//     check that reached outside the stated coverage would be probing a wider
//     reach than the operator claimed, which is the one thing this design must
//     not do (D2354).
//   - GET /2.0/user is read the way R14 reads it, because it is the endpoint
//     every run's own selfUser also calls and its answers are not uniform: a
//     403 means an access token with no identity at that endpoint (the
//     repository token's live answer, F7) and is an accepted answer with no
//     identity recorded; a 200 is accepted only when its `kind` is the
//     access-token kind matching the record's type, so a person's own OAuth or
//     personal token never enters the identity set. The identity is read LAST,
//     after the coverage reads passed, so a token that fails the check leaves
//     no identity behind for an operator to write.
//   - The identity of a project token is its UUID, not its key: R1 and R2 match
//     a project by UUID, and a key can be renamed. The key an operator gives is
//     therefore resolved here, once, and handed back in the Result so that
//     `creds set` stores the UUID and keeps the key only for display.
//   - The token is presented to bitbucket.org and nowhere else: the root is the
//     adapter's own constant (F22) and no field of the record can move it. The
//     value appears in the Authorization header and in no message, no field of
//     the Result and no log line — the check writes no log line at all.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

// maxAnswerBytes bounds one of the check's reads. Every call it makes is a
// single repository, a one-page repository listing or the account, so the
// answers are documents of a few kilobytes; this is the bound that keeps an
// answer that is not one from being held whole.
const maxAnswerBytes = 1 << 20

// The read scopes the check can prove, in Bitbucket's own vocabulary, and the
// header a Bitbucket answer may carry the token's scopes in. U2 has not settled
// whether that header exists, so what it carries is RECORDED in the Result and
// proves nothing: a header is not a proof (D2353).
const (
	scopeRepositoryRead  = "repository:read"
	scopePullRequestRead = "pullrequest:read"
	scopeHeader          = "X-OAuth-Scopes"
)

// writeScopes is D470's write pair: the two scopes the published docs tell the
// operator to tick when the token is created, each implying its read, and the
// pair this check reports as NOT verified. It is a constant rather than
// something derived from the reads that passed, because a read is no evidence
// of a write and this check has tried none (F8, D2353).
var writeScopes = []string{"repository:write", "pullrequest:write"}

// accessTokenKinds is what GET /2.0/user answers as `kind` for an access token,
// per type of record. The words are Bitbucket's own; a repository token's
// answer is the one F7's live recording shows.
var accessTokenKinds = map[Type]string{
	"repo":      "repository_access_token",
	"project":   "project_access_token",
	"workspace": "workspace_access_token",
}

// scopeWordRE is the whole shape of a scope word this check will record. A
// scope word is short and lowercase (repository:read); anything else is text
// this answer had no business carrying — a credential echoed into the header,
// say — and is dropped rather than put in front of the operator, which is the
// no-echo rule the credentials service keeps for every header it reads
// (credsvc's copyAnswerHeaders, oauth.go's error field).
var scopeWordRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}(:[a-z][a-z0-9_]{0,31})?$`)

// The check's refusals, one precise sentence each (T7, the spec's "precise
// message"). Each is written here and none is bitbucket.org's own text: what a
// refused call carries is never repeated to the operator, and the two
// not-covered forms name the record's own repository, workspace or project —
// never a token and never a value.
const (
	// msgNoToken is a check asked to verify nothing. It is not msgRefused: no
	// call was made, so nothing about bitbucket.org is known.
	msgNoToken = "no Bitbucket token was given"
	// msgExpired is a token whose record's expiry has passed. It is refused
	// without a single call: an expired token cannot work, and the read that
	// would prove it did work would be a call made with a credential the
	// operator has already told this program is dead.
	msgExpired = "the token has expired"
	// msgRecord is every way a record can be one this check cannot read: a type
	// R1 does not name, a coverage that is not one pattern, a type and a
	// coverage that disagree, or a project record that names no project. The
	// agreement of type and coverage is the record's own rule (checked at load
	// and at `fugaro validate`), so this says the record is not one the check
	// reads and never restates that rule here.
	msgRecord = "the record's type and coverage are not ones this check reads"
	// msgRefused is bitbucket.org refusing the token itself, or answering
	// something this check cannot read, or not answering at all: the one
	// fail-closed bucket, and a token refused this way is a token whose working
	// has not been shown.
	msgRefused = "Bitbucket refused the token"
	// msgMissingRepoRead and msgMissingPullRequestRead are the two scopes the
	// check proves, each missing: Bitbucket answers 403 for a token that may see
	// what it asked for and may not read it, which is a different answer from
	// the 404 of something the token does not reach at all.
	msgMissingRepoRead        = "missing repository read"
	msgMissingPullRequestRead = "missing pull request read"
)

// msgNotCovered is the refusal of a call the token does not reach: Bitbucket
// answers 404 for it, and what the operator is told is the repository, the
// workspace or the project the record states.
func msgNotCovered(what string) string { return "the token does not cover " + what }

// msgNotAccessToken is the refusal of an answer whose `kind` is not the access
// token the record states (R14): a person's own token, which is a working
// credential that must never be written as a Fugaro token's identity.
func msgNotAccessToken(t Type) string {
	return "the token is not the " + string(t) + " access token the record states"
}

// Result is what the check proved about one token, and the whole of what
// `creds set` reports. A refusal carries Message and nothing else: an operator
// reading one line needs the reason, not the fields of a check that did not
// finish.
type Result struct {
	// OK is true only when every read passed and the identity was read.
	OK bool
	// Message is the one precise refusal's sentence; empty when OK.
	Message string
	// Identity is the account UUID GET /2.0/user answered, which is the value
	// selfUser compares. It is empty for a repository token (R14) and it is
	// empty on every refusal.
	Identity string
	// ReadScopes are the read scopes the calls actually proved, so a workspace
	// or project record proves the repository read and nothing more: no
	// repository of its own was named to read a pull request in.
	ReadScopes []string
	// WriteScopes is D470's write pair. It is reported unverified, and
	// WriteVerified is what says so.
	WriteScopes []string
	// WriteVerified is false on every result this build makes: nothing here
	// writes (D2353). It is a field rather than an assumption so that a later
	// build which does prove the write scopes has somewhere to say it, and so
	// that the line `creds set` prints reads this one place.
	WriteVerified bool
	// Scopes is what a scope header said, when Bitbucket returned one (U2). It
	// is recorded and never used: the write scopes stay unverified whatever it
	// says.
	Scopes []string
	// ProjectUUID is the project a project record was matched by, resolved from
	// the record's key when the record named a key and no UUID. `creds set`
	// stores it and keeps the key for display.
	ProjectUUID string
}

// noteScopes records the scope words of one answer, the first answer to carry
// any. Both the shape rule and the "first answer" rule keep this a record and
// never a check: what it holds cannot refuse a token, and it can hold nothing
// that is not a scope word.
func (r *Result) noteScopes(h http.Header) {
	if r.Scopes != nil || h == nil {
		return
	}
	var words []string
	for _, field := range strings.Fields(strings.ReplaceAll(h.Get(scopeHeader), ",", " ")) {
		if scopeWordRE.MatchString(field) {
			words = append(words, field)
		}
	}
	if len(words) > 0 {
		r.Scopes = words
	}
}

// Verifier is the check itself: the API root it reads bitbucket.org at, and the
// client it reads with.
//
// Both are fields rather than constants so that a test can point the check at a
// fixture and so that nothing here is a package-level mutable: the credentials
// service builds one from its own client, and the CLI builds one per `creds
// set` with no client at all.
type Verifier struct {
	// Client is the HTTP client; nil means http.DefaultClient.
	Client *http.Client
	// BaseURL is the API root; "" means bitbucket.DefaultBaseURL, which is what
	// every build that is not a test leaves it as (F22).
	BaseURL string
}

// Verify checks one token against the record the operator gave for it, and
// reads bitbucket.org with it and nowhere else.
//
// The order is the point of it being one function: the record's own facts first
// (an expiry that has passed and a record the check cannot read make no call at
// all), then the reads the record's type and coverage call for, and the
// identity last — so a refusal never leaves an identity, a proven read scope or
// a resolved project UUID in the Result for a caller to write.
func (v *Verifier) Verify(ctx context.Context, token string, rec Record) Result {
	if token == "" {
		return Result{Message: msgNoToken}
	}
	if !rec.Expires.IsZero() && !rec.Expires.After(time.Now()) {
		return Result{Message: msgExpired}
	}
	c, ok := coverageOf(rec)
	if !ok {
		return Result{Message: msgRecord}
	}
	res := Result{WriteScopes: append([]string(nil), writeScopes...)}
	if msg, refused := v.readCoverage(ctx, &res, token, rec, c); refused {
		return Result{Message: msg}
	}
	if msg, refused := v.readIdentity(ctx, &res, token, rec); refused {
		return Result{Message: msg}
	}
	res.OK = true
	return res
}

// readCoverage makes the reads the record's type and coverage call for, and
// answers the refusal's message when one of them did not pass.
//
// Each type is checked against the coverage form R1 pairs it with, and the
// reads stay inside it: a repository token is sent to its one repository, a
// workspace token to the workspace's repository listing, and a project token to
// the repositories of the project its UUID names — which is the fact R2 matches
// a project token on, so the check proves the same thing the proxy will.
func (v *Verifier) readCoverage(ctx context.Context, res *Result, token string, rec Record, c coverage) (string, bool) {
	switch rec.Type {
	case "repo":
		if c.repo == "" {
			return msgRecord, true
		}
		what := "repository " + c.workspace + "/" + c.repo
		if _, msg, refused := v.prove(ctx, res, token, v.repoURL(c.workspace, c.repo), what, msgMissingRepoRead); refused {
			return msg, true
		}
		res.ReadScopes = append(res.ReadScopes, scopeRepositoryRead)
		if _, msg, refused := v.prove(ctx, res, token, v.pullRequestsURL(c.workspace, c.repo), "", msgMissingPullRequestRead); refused {
			return msg, true
		}
		res.ReadScopes = append(res.ReadScopes, scopePullRequestRead)
		return "", false
	case "workspace":
		if !c.whole {
			return msgRecord, true
		}
		if _, msg, refused := v.prove(ctx, res, token, v.repositoriesURL(c.workspace, ""), "workspace "+c.workspace, msgMissingRepoRead); refused {
			return msg, true
		}
		res.ReadScopes = append(res.ReadScopes, scopeRepositoryRead)
		return "", false
	case "project":
		if c.repo != "" {
			return msgRecord, true
		}
		uuid := rec.ProjectUUID
		if uuid == "" {
			if rec.ProjectKey == "" {
				return msgRecord, true
			}
			what := projectWhat(rec.ProjectKey, "", c.workspace)
			body, msg, refused := v.prove(ctx, res, token, v.projectURL(c.workspace, rec.ProjectKey), what, msgMissingRepoRead)
			if refused {
				return msg, true
			}
			var ans struct {
				UUID string `json:"uuid"`
			}
			if json.Unmarshal(body, &ans) != nil || ans.UUID == "" {
				return msgRefused, true
			}
			uuid = ans.UUID
		}
		what := projectWhat(rec.ProjectKey, uuid, c.workspace)
		body, msg, refused := v.prove(ctx, res, token, v.repositoriesURL(c.workspace, uuid), what, msgMissingRepoRead)
		if refused {
			return msg, true
		}
		if msg := projectCovered(body, uuid, what); msg != "" {
			return msg, true
		}
		res.ProjectUUID = uuid
		res.ReadScopes = append(res.ReadScopes, scopeRepositoryRead)
		return "", false
	}
	return msgRecord, true
}

// projectCovered reads the answer of the project's repository listing and
// reports the refusal when the UUID the check asked about is not what came
// back.
//
// The listing is filtered by `project.uuid` on the wire, and the answer is
// checked against the same UUID anyway: a filter is bitbucket.org's own
// behaviour and an empty or unfiltered answer would otherwise be evidence that
// the token covers a project it may not. An answer with no repository at all is
// a refusal too — a token that reaches nothing whatever its record says has not
// been shown to work, and this check has no other call to show it with.
func projectCovered(body []byte, uuid, what string) string {
	var ans struct {
		Values []struct {
			Project struct {
				UUID string `json:"uuid"`
			} `json:"project"`
		} `json:"values"`
	}
	if json.Unmarshal(body, &ans) != nil {
		return msgRefused
	}
	if len(ans.Values) == 0 {
		return msgNotCovered(what)
	}
	for _, item := range ans.Values {
		if item.Project.UUID != uuid {
			return msgNotCovered(what)
		}
	}
	return ""
}

// readIdentity reads GET /2.0/user, the endpoint every run's selfUser calls
// too, and reads it the way R14 does (see the file's own comment): a 403 is an
// access token with no identity here, a 200 must be the access token the record
// states, and anything else is a refusal.
func (v *Verifier) readIdentity(ctx context.Context, res *Result, token string, rec Record) (string, bool) {
	status, body, header, err := v.get(ctx, v.root()+"/user", token)
	res.noteScopes(header)
	switch {
	case err != nil, status == http.StatusUnauthorized:
		return msgRefused, true
	case status == http.StatusForbidden:
		return "", false
	case status != http.StatusOK:
		return msgRefused, true
	}
	var me struct {
		UUID string `json:"uuid"`
		Kind string `json:"kind"`
	}
	if json.Unmarshal(body, &me) != nil || me.Kind != accessTokenKinds[rec.Type] {
		return msgNotAccessToken(rec.Type), true
	}
	res.Identity = me.UUID
	return "", false
}

// prove makes one read of the check and reports whether the token passed it,
// with the answer's body when it did.
//
// uncovered is the thing a 404 is about, or "" when the call proves a read of
// something the token is already known to cover (the pull requests inside a
// repository): there a 404 is the read itself missing rather than a reach the
// record over-claims, and both are the same refusal to the operator. Every
// other answer — a 401, a redirect, a 5xx, no answer at all — is the
// fail-closed bucket, because none of them has shown the token works.
func (v *Verifier) prove(ctx context.Context, res *Result, token, target, uncovered, missing string) ([]byte, string, bool) {
	status, body, header, err := v.get(ctx, target, token)
	res.noteScopes(header)
	switch {
	case err != nil, status == http.StatusUnauthorized:
		return nil, msgRefused, true
	case status == http.StatusOK:
		return body, "", false
	case status == http.StatusForbidden:
		return nil, missing, true
	case status == http.StatusNotFound:
		if uncovered == "" {
			return nil, missing, true
		}
		return nil, msgNotCovered(uncovered), true
	}
	return nil, msgRefused, true
}

// coverage is what a record says its token reaches, read from the record's one
// coverage entry.
type coverage struct {
	workspace string
	repo      string
	whole     bool
}

// coverageOf reads R1's three coverage forms: `ws/repo` (one repository),
// `ws/*` (every repository of the workspace) and the bare `ws` a project record
// carries, whose project is in the record's own fields. Anything else is a
// record this check does not read: a partial wildcard such as `ws/fugaro-*`
// names no repository the check could read and no workspace it could list, and
// more than one entry is not one of R1's forms.
//
// It deliberately does not check the entry against the record's type: that
// agreement is R1's own rule, checked where the record is loaded and by
// `fugaro validate`, and a second copy of it here is a rule that would drift.
// The type is asked what it needs instead, in readCoverage.
func coverageOf(rec Record) (coverage, bool) {
	if len(rec.Coverage) != 1 {
		return coverage{}, false
	}
	workspace, rest, ok := strings.Cut(rec.Coverage[0], "/")
	if workspace == "" {
		return coverage{}, false
	}
	switch {
	case !ok:
		return coverage{workspace: workspace}, true
	case rest == "*":
		return coverage{workspace: workspace, whole: true}, true
	case rest != "" && !strings.Contains(rest, "*"):
		return coverage{workspace: workspace, repo: rest}, true
	}
	return coverage{}, false
}

// projectWhat names the project a refusal is about: the key the operator knows
// it by when the record has one, and the UUID otherwise. Both are the record's
// own text and neither is a credential.
func projectWhat(key, uuid, workspace string) string {
	name := key
	if name == "" {
		name = uuid
	}
	return "project " + name + " in workspace " + workspace
}

// root is the API root every call is made against: the verifier's own when a
// test set one, and the adapter's constant otherwise (F22). A trailing slash is
// dropped so that a caller's spelling of the root cannot make a doubled one.
func (v *Verifier) root() string {
	if v.BaseURL != "" {
		return strings.TrimRight(v.BaseURL, "/")
	}
	return bitbucket.DefaultBaseURL
}

func (v *Verifier) client() *http.Client {
	if v.Client != nil {
		return v.Client
	}
	return http.DefaultClient
}

// The five URLs the check can build, each from escaped segments of the record's
// own text so that a workspace or repository name can never add a path segment
// of its own. They are the adapter's own paths (bitbucketproxy's upstreamURL
// builds the same three for the calls it forwards).
func (v *Verifier) repoURL(workspace, repo string) string {
	return v.root() + "/repositories/" + url.PathEscape(workspace) + "/" + url.PathEscape(repo)
}

func (v *Verifier) pullRequestsURL(workspace, repo string) string {
	return v.repoURL(workspace, repo) + "/pullrequests?pagelen=1"
}

// repositoriesURL is a workspace's repository listing, held to one page, and to
// one project when uuid is not empty — the filter R2 matches a project token on
// and nothing else.
func (v *Verifier) repositoriesURL(workspace, uuid string) string {
	q := url.Values{"pagelen": {"1"}}
	if uuid != "" {
		q.Set("q", `project.uuid="`+uuid+`"`)
	}
	return v.root() + "/repositories/" + url.PathEscape(workspace) + "?" + q.Encode()
}

func (v *Verifier) projectURL(workspace, key string) string {
	return v.root() + "/workspaces/" + url.PathEscape(workspace) + "/projects/" + url.PathEscape(key)
}

// get is the check's one call: a read, addressed by a URL this file built from
// the record's own escaped segments and from no other text. The answer's body
// is read bounded. The token rides in the Authorization header and is named
// nowhere else — not in the returned error, which is the transport's own text
// and which prove answers msgRefused for rather than showing to the operator.
func (v *Verifier) get(ctx context.Context, target, token string) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := v.client().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, body, resp.Header, nil
}
