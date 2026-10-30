package gitproxy

// The proxy's rules (B1.4, D496): which request a run may make, and which ref
// and which pull request it may touch. Three functions are the whole of it -
// Classify names the request, Decide answers for it and CheckPRWrite answers
// for a pull-request write's body - and every answer is either an allow or a
// refusal with a name from one closed vocabulary, so the service's audit line
// carries a class and never a byte of what the caller sent (G7).
//
// The attacker is the run's own agent: it holds the run identity and can mint
// the job's ID token, so it can send any request this proxy serves (the plan's
// "attacker's view"). Every rule below is therefore written as if the request
// were hand-built, and the connection between what a request SAYS and what the
// proxy FORWARDS is rebuilt from the parts this file decided on, never copied
// from the caller's text (G13): a path spelling the rules read one way and git
// reads another is a path that would move a ref nobody checked.
//
// The repository is the pivot of all of it. A request names one, the job
// account's own repository is another, and the two must be equal after the
// same canonicalisation the policy uses (projectpolicy.Canonical, G3); the
// published bitbucket_token block then decides whether this installation
// proxies that repository at all (proxy: true, G9).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// Kind is what a request is: a git fetch, a git push, or one of the REST
// calls bitbucket.Provider makes.
type Kind string

const (
	// KindFetch is a git fetch: the upload-pack ref advertisement
	// (GET .../info/refs?service=git-upload-pack) and the request that
	// follows it (POST .../git-upload-pack). Both are reads of the
	// repository the run already has.
	KindFetch Kind = "fetch"
	// KindAdvertisement is the receive-pack ref advertisement
	// (GET .../info/refs?service=git-receive-pack), which git sends before
	// every push. It is a read - it moves no ref - so it is named apart from
	// the push and allowed like one of those: a push whose update request
	// nobody read is refused (G2), and this request has no update request at
	// all. Folding it into KindPush would refuse the opening request of every
	// push, and folding it into KindFetch would audit a run about to push as
	// one that fetched.
	KindAdvertisement Kind = "advertisement"
	// KindPush is a git push: the POST .../git-receive-pack whose body is the
	// update request the rules decide on.
	KindPush Kind = "push"
	KindREST Kind = "rest"
)

// Call names one of the REST calls the Bitbucket adapter makes (G4). The list
// is the adapter's own traffic and nothing else: internal/gitprov/bitbucket
// is where each of these is sent from, and a call added there without a name
// here is a call the proxy refuses.
type Call string

const (
	CallUser        Call = "user"         // GET /user: the token's own user
	CallRepoRead    Call = "repo_read"    // GET /repositories/{workspace}/{repo}
	CallCommitRead  Call = "commit_read"  // GET .../commit/{rev}
	CallPRList      Call = "pr_list"      // GET .../pullrequests?q=
	CallPRRead      Call = "pr_read"      // GET .../pullrequests/{id}
	CallPRCreate    Call = "pr_create"    // POST .../pullrequests
	CallPRUpdate    Call = "pr_update"    // PUT .../pullrequests/{id}
	CallPRComment   Call = "pr_comment"   // POST .../pullrequests/{id}/comments
	CallCommentList Call = "comment_list" // GET .../pullrequests/{id}/comments
)

// The refusal classes. They are the reasons a Verdict carries when it refuses,
// and Classify's errors carry them too, so one vocabulary covers the whole
// file and the service audits a refusal it took at any of the three steps the
// same way (G7). None of them is ever built from caller text.
const (
	// ReasonPathMalformed is a path whose meaning depends on who decodes it:
	// an encoded separator, a dot segment, an escape that is not one.
	ReasonPathMalformed = "path_malformed"
	// ReasonCallNotAllowed is a well-formed path that is not a call the run
	// makes - /workspaces, a pipeline, a merge, a settings route.
	ReasonCallNotAllowed = "call_not_allowed"
	// ReasonNotAdmitted is a repository the published block does not list.
	ReasonNotAdmitted = "not_admitted"
	// ReasonProxyOff is an installation whose block has proxy off: this job
	// holds the token itself and the service serves none of its git (G9).
	ReasonProxyOff = "proxy_off"
	// ReasonOtherRepository is a request for a repository other than the
	// calling job account's (G3).
	ReasonOtherRepository = "other_repository"
	// ReasonPushUnread is a push whose update request nobody read: the refs
	// are what the rules decide on, and a push whose refs were not read is a
	// push nobody checked (G2).
	ReasonPushUnread = "push_unread"
	// ReasonNoRunBranch is a push or a pull-request write from an identity
	// with no fb: a review or artifact run pushes nothing (G6).
	ReasonNoRunBranch = "no_run_branch"
	// ReasonRefDelete is an update whose new id is the zero id (a deletion).
	ReasonRefDelete = "ref_delete"
	// ReasonTagRef is an update to refs/tags/.
	ReasonTagRef = "tag_ref"
	// ReasonOtherRef is an update to any ref that is not refs/heads/<fb>. It
	// is G7's own name for the class an audit line records.
	ReasonOtherRef = "other_ref"
	// ReasonOtherPullRequest is a pull-request write that is neither the
	// run's own (source repository and fb) nor, for a comment, one fpr names
	// (G12).
	ReasonOtherPullRequest = "other_pull_request"
	// ReasonSourceMismatch is a create naming a source that is not fb in the
	// run's own repository.
	ReasonSourceMismatch = "source_mismatch"
	// ReasonDestinationChange is a write whose body would move a pull
	// request's destination (G12).
	ReasonDestinationChange = "destination_change"
	// ReasonBodyMalformed is a write whose body is not the JSON object the
	// adapter sends, or one that names a key twice.
	ReasonBodyMalformed = "body_malformed"
)

// Refusal is Classify's answer for a request it will not name. Reason is one
// of the classes above, and the message is that class: a path that reached
// here is caller text, and the odd ones - an encoded separator, a dot
// segment - are odd because someone chose them.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return "the request is not one a run may make: " + r.Reason }

// Request is one request, classified. What it carries is what the rules
// decided on: the caller forwards the request it described, and no part of
// the upstream URL is ever taken from the raw path (G13).
type Request struct {
	// Kind is what the request is. A git request carries Repo and Query; a
	// REST call carries Repo too, except for the one call that names no
	// repository (CallUser).
	Kind Kind
	// Repo is the repository the request names, canonical and
	// provider-qualified ("bitbucket/acme/sandbox"): the spelling
	// projectpolicy.Canonical and JobAccount.Repo use.
	Repo string
	// Call names the REST call (KindREST only).
	Call Call
	// PR is the pull request the call names, 0 for the calls that name none.
	PR int
	// Query is exactly the query the call may carry: the parameters
	// bitbucket.Provider's own code sends to it, and nothing else the caller
	// may have appended. The upstream URL is rebuilt from these.
	Query url.Values
	// Updates are the refs a push would move, as ReadUpdateRequest returned
	// them (B1.3). Everything else in this package decides on those refs, so
	// a push is never decided on the body it will forward.
	Updates []RefUpdate
	// UpdatesRead reports that the caller read the push's update request in
	// full, up to its flush, before asking. It is a separate field because a
	// push that would move no ref (git's flush-only probe) and a push whose
	// body nobody read have the same empty Updates, and only one of the two
	// may be forwarded.
	UpdatesRead bool
}

// Identity is what the rules know about the run that is asking: B1.5 fills it
// from a verified identity's claims (fb and fpr) and never from the request.
//
// There is no separate "review run" field, because a review or artifact run is
// exactly the identity without a branch: the launcher mints no fb for one
// (G6), and fb's absence is the whole of the difference. A second field could
// only ever disagree with the branch - and a run whose two halves disagreed
// would be the one shape where "may this push?" and "may this write a pull
// request?" were answered from different facts.
type Identity struct {
	// Run is the run id (fr). It names the run in an audit line.
	Run string
	// Branch is the branch this identity may push (fb: refs/heads/<Branch>),
	// empty on a review or artifact run.
	Branch string
	// PRs are the pull requests this identity may comment on (fpr): the one
	// a follow-up continues, or the ones a review run reviews.
	PRs []int
}

// PRFacts are what the service read for itself about the pull request a write
// names: the facts come from the API and never from the request (G12). For a
// create there is no pull request to read yet and the fields are zero - the
// source a create names is checked in its body, against the run's own branch
// and repository.
type PRFacts struct {
	// Call is the write being made: CallPRCreate, CallPRUpdate or
	// CallPRComment.
	Call Call
	// Number is the pull request the write names, 0 for a create.
	Number int
	// SourceRepo is the pull request's source repository as the API reports
	// it ("workspace/slug", a fork's workspace when it is one).
	SourceRepo string
	// SourceBranch is the source branch the API reports. On a fork with a
	// branch of the same name it equals the run's branch, which is why the
	// repository is checked and not only the branch.
	SourceBranch string
	// Destination is the destination branch the pull request has now. A
	// write may not move it.
	Destination string
}

// Verdict is an answer: allowed, or refused with the class it was refused by.
// Reason is one of the classes above for a refusal, and the git kind or the
// REST call's name for an allow, so a line of audit says what the run did
// without repeating anything the caller wrote (G7).
type Verdict struct {
	Allow  bool
	Reason string
}

// Classify names one request. path is the request's ESCAPED path
// (net/url's URL.EscapedPath) without its query string, because this is where
// an encoded separator has to be refused and a decoded path has already lost
// the difference. query is the request's parsed query.
//
// The two forms are the ones the Bitbucket adapter and git put on the wire:
// git on bitbucket.org ("/{workspace}/{repo}.git/info/refs?service=…",
// "/{workspace}/{repo}.git/git-upload-pack", "…/git-receive-pack"), and the
// REST API under its /2.0 root ("/repositories/{workspace}/{repo}/…", which
// the adapter's BaseURL is). A leading "/2.0" is accepted on the REST form
// and means nothing: every part of the upstream URL is rebuilt from what this
// function returns.
//
// A workspace and a repository are one path segment each, because that is
// what the Bitbucket adapter can address at all: gitprov.SplitRepo refuses a
// repository that is not "owner/name", so a nested path names no repository
// any job account could carry and is refused here rather than guessed at.
//
// It refuses anything it cannot name - another repository's path, a REST
// route the adapter never calls (/workspaces, pipelines, a merge, an approve),
// a git path spelled in a way whose segments depend on who decodes them - and
// every refusal is a *Refusal naming its class.
func Classify(method, path string, query url.Values) (Request, error) {
	segs, err := pathSegments(path)
	if err != nil {
		return Request{}, err
	}
	if segs[0] == apiRootSegment && len(segs) > 1 && (segs[1] == "repositories" || segs[1] == "user") {
		// The API root the adapter's BaseURL is (https://api.bitbucket.org/2.0):
		// a REST request that reached the proxy through the adapter's own
		// client carries it. It is the only segment dropped anywhere, and it
		// is dropped rather than kept so the two spellings of the same call
		// classify alike - the upstream URL is rebuilt from the parts either
		// way and never from this path.
		//
		// It is dropped only in front of the two route heads the API has
		// below it, because a workspace may be named "2.0": positional
		// stripping would read a git fetch of bitbucket/2.0/sandbox as a REST
		// path and refuse a repository the policy can admit.
		segs = segs[1:]
	}
	switch {
	case segs[0] == "repositories":
		// A workspace may be named "repositories", which is also the REST
		// route head. The two readings cannot both apply to one path: git's
		// action endpoints are POSTs on three segments while the REST call
		// spelled at those segments is the repository read, which is a GET,
		// and git's info/refs GET needs two segments no REST route takes for
		// the same repository. So the git shape is asked first and the REST
		// routes only when it does not match, and neither reading can hide a
		// request from the other.
		if gitShape(method, segs) {
			return classifyGit(method, segs, query)
		}
		return classifyREST(method, segs[1:], query)
	case len(segs) == 1 && segs[0] == "user":
		return classifyUser(method, query)
	}
	return classifyGit(method, segs, query)
}

// gitShape reports whether the segments are one of git's two request shapes,
// before the repository is looked at: a four-segment .../info/refs GET, or a
// three-segment .../git-upload-pack or .../git-receive-pack POST. The method is
// part of the shape, because it is what keeps the two readings of a path that
// begins with "repositories" apart.
func gitShape(method string, segs []string) bool {
	switch {
	case len(segs) == 4 && segs[2] == "info" && segs[3] == "refs":
		return method == http.MethodGet
	case len(segs) == 3 && (segs[2] == "git-upload-pack" || segs[2] == "git-receive-pack"):
		return method == http.MethodPost
	}
	return false
}

// apiRootSegment is Bitbucket's API version segment, the first segment of every
// REST path the adapter sends.
const apiRootSegment = "2.0"

// classifyUser reads GET /user, the one call the adapter makes that names no
// repository: it asks the API which user the token belongs to, so that
// Fugaro's own comments can be told from other people's.
func classifyUser(method string, query url.Values) (Request, error) {
	if method != http.MethodGet || len(query) != 0 {
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	return Request{Kind: KindREST, Call: CallUser}, nil
}

// pathSegments splits a path into the segments the classification is written
// against, refusing every spelling whose meaning depends on the reader. The
// refusals are the point of it: "%2F" in a path segment is a separator to one
// server and a name to the next, "." and ".." are two names to the proxy and
// one step elsewhere, and a percent-escape is a way to write a byte the rules
// never saw.
func pathSegments(path string) ([]string, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, &Refusal{Reason: ReasonPathMalformed}
	}
	if hasEncodedSeparator(path) {
		// Refused before decoding rather than decoded into a separator: a
		// request whose repository changes with the decoder is not one the
		// rules may decide on.
		return nil, &Refusal{Reason: ReasonPathMalformed}
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return nil, &Refusal{Reason: ReasonPathMalformed}
	}
	if strings.ContainsRune(decoded, '\\') {
		// A backslash is a separator to some servers and a plain character
		// to others; Bitbucket's paths have none, so a path carrying one is
		// refused rather than read as a name.
		return nil, &Refusal{Reason: ReasonPathMalformed}
	}
	for i := 0; i < len(decoded); i++ {
		if decoded[i] < 0x20 || decoded[i] == 0x7f {
			return nil, &Refusal{Reason: ReasonPathMalformed}
		}
	}
	segs := strings.Split(strings.TrimPrefix(decoded, "/"), "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, &Refusal{Reason: ReasonPathMalformed}
		}
	}
	return segs, nil
}

// hasEncodedSeparator reports whether path spells a separator in percent
// notation. Only the two separators are looked for: every other escape is
// decoded and held to the same rules as the character it stands for, which is
// what makes "repo%2Ename" the repository "repo.name" and "..%2E" no
// repository at all.
func hasEncodedSeparator(path string) bool {
	for i := 0; i+3 <= len(path); i++ {
		if path[i] != '%' {
			continue
		}
		hi, lo := lowerHex(path[i+1]), lowerHex(path[i+2])
		if (hi == '2' && lo == 'f') || (hi == '5' && lo == 'c') {
			return true
		}
	}
	return false
}

func lowerHex(c byte) byte {
	if 'A' <= c && c <= 'F' {
		return c + 'a' - 'A'
	}
	return c
}

// classifyGit reads the two git services on bitbucket.org. The action is what
// tells a push from a fetch and nothing else does: both services take a POST
// and both begin with an info/refs GET.
func classifyGit(method string, segs []string, query url.Values) (Request, error) {
	if !gitShape(method, segs) {
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	if len(segs) == 3 {
		if len(query) != 0 {
			return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
		}
		kind := KindFetch
		if segs[2] == "git-receive-pack" {
			kind = KindPush
		}
		return finishGit(kind, segs[0], segs[1], nil)
	}
	// The service parameter is the whole of git's query here, and it is read
	// strictly: a second parameter cannot reach the upstream URL because only
	// this one is carried.
	service, err := only(query, "service")
	if err != nil || len(service["service"]) != 1 {
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	var kind Kind
	switch service.Get("service") {
	case "git-upload-pack":
		kind = KindFetch
	case "git-receive-pack":
		kind = KindAdvertisement
	default:
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	return finishGit(kind, segs[0], segs[1], service)
}

// finishGit canonicalises the repository of a git request and returns it with
// the query the caller may forward. A repository that is not "owner/name" -
// the only shape a Bitbucket job account can name - is refused here.
func finishGit(kind Kind, workspace, repo string, query url.Values) (Request, error) {
	canonical, err := canonicalRepo(workspace, repo)
	if err != nil {
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	return Request{Kind: kind, Repo: canonical, Query: query}, nil
}

// classifyREST reads the REST paths the Bitbucket adapter calls, given the
// path's segments after "/repositories". Each shape is the adapter's own
// (internal/gitprov/bitbucket): the repository, a commit, the pull-request
// list with its q= query, one pull request, its comments, and the three writes
// of a run's own pull request. Everything else - /workspaces, the pipelines
// and settings routes, merge, approve, decline - is refused, and so is a
// route spelled with the wrong method.
func classifyREST(method string, segs []string, query url.Values) (Request, error) {
	if len(segs) < 2 {
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	repo, err := canonicalRepo(segs[0], segs[1])
	if err != nil {
		return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
	}
	tail := segs[2:]
	switch {
	case len(tail) == 0 && method == http.MethodGet:
		return restCall(CallRepoRead, repo, 0, query)
	case len(tail) == 2 && tail[0] == "commit" && method == http.MethodGet && validRev(tail[1]):
		return restCall(CallCommitRead, repo, 0, query)
	case len(tail) >= 1 && tail[0] == "pullrequests":
		return classifyPullRequest(method, repo, tail[1:], query)
	}
	return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
}

// classifyPullRequest reads the pull-request routes, given the segments after
// "pullrequests".
func classifyPullRequest(method string, repo string, rest []string, query url.Values) (Request, error) {
	switch {
	case len(rest) == 0:
		if method == http.MethodGet {
			return restCall(CallPRList, repo, 0, query)
		}
		if method == http.MethodPost {
			return restCall(CallPRCreate, repo, 0, query)
		}
	case len(rest) == 1:
		number, ok := pullRequestNumber(rest[0])
		if !ok {
			break
		}
		if method == http.MethodGet {
			return restCall(CallPRRead, repo, number, query)
		}
		if method == http.MethodPut {
			return restCall(CallPRUpdate, repo, number, query)
		}
	case len(rest) == 2 && rest[1] == "comments":
		number, ok := pullRequestNumber(rest[0])
		if !ok {
			break
		}
		if method == http.MethodGet {
			return restCall(CallCommentList, repo, number, query)
		}
		if method == http.MethodPost {
			return restCall(CallPRComment, repo, number, query)
		}
	}
	return Request{}, &Refusal{Reason: ReasonCallNotAllowed}
}

// restCall builds a REST request, holding its query to the parameters that
// call may carry. What it returns is a fresh set built from the allowed names,
// so no parameter the caller invented - and no second value under an allowed
// name - can reach the upstream URL (G13).
func restCall(call Call, repo string, number int, query url.Values) (Request, error) {
	allowed, err := only(query, allowedParams(call)...)
	if err != nil {
		return Request{}, err
	}
	return Request{Kind: KindREST, Repo: repo, Call: call, PR: number, Query: allowed}, nil
}

// allowedParams are the query parameters one call may carry: exactly the ones
// the adapter's own code sends it. The pull-request list carries its BBQL
// filter, the comment list carries the pagination Bitbucket's own next links
// use, and no other call carries anything.
func allowedParams(call Call) []string {
	switch call {
	case CallPRList:
		return []string{"q"}
	case CallCommentList:
		return []string{"pagelen", "page"}
	}
	return nil
}

// maxQueryValueBytes bounds one carried query value. A BBQL filter is a
// branch name and a repository in a sentence of a few hundred bytes
// (bitbucket.find), so this is not a limit on real traffic: it is the bound
// that keeps a caller from making the proxy carry a megabyte of its own text
// upstream under a name the rules allowed.
const maxQueryValueBytes = 1024

// only returns the parameters of query that are in allowed, and refuses a
// query that carries anything else - an unknown name, a repeated one, a value
// that is not the shape the call sends. It is the one place a request's query
// is read, and what it returns is always a new set.
func only(query url.Values, allowed ...string) (url.Values, error) {
	if len(query) == 0 {
		return nil, nil
	}
	out := url.Values{}
	for _, name := range allowed {
		values, ok := query[name]
		if !ok {
			continue
		}
		if len(values) != 1 {
			return nil, &Refusal{Reason: ReasonCallNotAllowed}
		}
		value := values[0]
		if err := validParam(name, value); err != nil {
			return nil, err
		}
		out[name] = []string{value}
	}
	if len(out) != len(query) {
		return nil, &Refusal{Reason: ReasonCallNotAllowed}
	}
	return out, nil
}

// validParam holds one carried value to the shape its call sends: a page
// number is digits, a BBQL filter is bounded text with no control character.
func validParam(name, value string) error {
	switch name {
	case "page", "pagelen":
		if value == "" || len(value) > 9 || strings.Trim(value, "0123456789") != "" {
			return &Refusal{Reason: ReasonCallNotAllowed}
		}
	default:
		if len(value) > maxQueryValueBytes || strings.ContainsFunc(value, isControl) {
			return &Refusal{Reason: ReasonCallNotAllowed}
		}
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// validRev holds a commit route's revision to a hash: the adapter resolves an
// abbreviated pull-request head hash to its full one (bitbucket.resolveFullHash)
// and asks for nothing else. A revision that is not a hash would be a name
// this proxy decided on without knowing what it names.
func validRev(rev string) bool {
	if len(rev) < 7 || len(rev) > 64 {
		return false
	}
	for i := 0; i < len(rev); i++ {
		c := rev[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// pullRequestNumber reads a pull request's id off the path. An id is a
// positive number and nothing else: strconv would take "+1" and "-1", and a
// pull request the caller reaches by a spelling the API may not read the same
// way is a pull request the rules did not check.
func pullRequestNumber(s string) (int, bool) {
	if s == "" || len(s) > 9 || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// canonicalRepo is the repository a request names, in the one spelling the
// policy, the job account and the launcher all use
// (projectpolicy.Canonical: "bitbucket/acme/sandbox", lower-cased, without a
// trailing ".git"). A workspace and a repository that do not make one are
// refused; they are never repaired into one.
func canonicalRepo(workspace, repo string) (string, error) {
	return projectpolicy.Canonical("bitbucket", workspace+"/"+repo)
}

// Decide answers for a classified request. repo is the calling job account's
// repository (JobAccount.Repo, canonical: G3's binding is that the request
// names it and no other), and block is the published bitbucket_token node, or
// nil when the project has published none.
//
// The order is the rules' own: the installation must proxy this repository
// before anything else is considered, then the request must be about the
// repository the job account is, and only then the action. A fetch and a ref
// advertisement - the receive-pack one included, since it moves no ref - are
// allowed there. A push is decided on the refs the caller read with
// ReadUpdateRequest, ref by ref, before any pack byte is forwarded (G2).
func Decide(req Request, id Identity, repo string, block *projectpolicy.BitbucketToken) Verdict {
	if block == nil || !projectpolicy.AdmitRepo(block.AllowRepos, repo) {
		return Verdict{Reason: ReasonNotAdmitted}
	}
	if !block.Proxy {
		return Verdict{Reason: ReasonProxyOff}
	}
	if req.Kind != KindREST || req.Call != CallUser {
		// GET /user is the one call that names no repository: it asks the
		// API who the token is, and every other call is about a repository
		// that must be this job account's.
		if req.Repo == "" || req.Repo != repo {
			return Verdict{Reason: ReasonOtherRepository}
		}
	}
	switch req.Kind {
	case KindFetch, KindAdvertisement:
		// Both are reads of the run's own repository, and the push that may
		// follow the advertisement is decided at its POST (G2). A review run
		// is allowed both: it may fetch, and the advertisement returns what a
		// fetch of the same repository returns.
		return Verdict{Allow: true, Reason: string(req.Kind)}
	case KindPush:
		return decidePush(req, id)
	case KindREST:
		return Verdict{Allow: true, Reason: string(req.Call)}
	}
	return Verdict{Reason: ReasonCallNotAllowed}
}

// decidePush answers for a push: its refs are the whole of the decision, and
// the run may move one ref, refs/heads/<fb>, and only with an id that is not
// the zero id. A force-push to that ref is allowed - the run owns its branch
// (G2) - and git's flush-only probe, which moves nothing, is passed as the
// no-op it is.
func decidePush(req Request, id Identity) Verdict {
	if !req.UpdatesRead {
		return Verdict{Reason: ReasonPushUnread}
	}
	if id.Branch == "" {
		return Verdict{Reason: ReasonNoRunBranch}
	}
	want := "refs/heads/" + id.Branch
	for _, u := range req.Updates {
		switch {
		case strings.Trim(u.New, "0") == "":
			return Verdict{Reason: ReasonRefDelete}
		case strings.HasPrefix(u.Ref, "refs/tags/"):
			return Verdict{Reason: ReasonTagRef}
		case u.Ref != want:
			return Verdict{Reason: ReasonOtherRef}
		}
	}
	return Verdict{Allow: true, Reason: string(KindPush)}
}

// CheckPRWrite answers for a pull-request write: a create, an update (which
// is also how a pull request is made ready and given reviewers), or a comment.
// pr is what the service read for itself with the bundle's token, and body is
// the body the caller is about to send.
//
// A write is allowed when the pull request is the run's own - its source
// repository is the run's and its source branch is fb - or, for a comment
// only, when its number is one fpr names (G12: a review run comments on the
// pull requests it reviews, which live in other repositories). A create names
// fb and the run's repository as its source, since there is no pull request
// to read yet. No write may move a pull request's destination, and the calls
// that would merge, approve or decline one are not calls this proxy names at
// all (Classify).
func CheckPRWrite(pr PRFacts, body []byte, id Identity, repo string) Verdict {
	switch pr.Call {
	case CallPRCreate:
		return checkPRCreate(body, id, repo)
	case CallPRUpdate, CallPRComment:
	default:
		return Verdict{Reason: ReasonCallNotAllowed}
	}
	keys, ok := readWriteBody(body)
	if !ok {
		return Verdict{Reason: ReasonBodyMalformed}
	}
	destination := keys.destination()
	if (destination.branch != "" && destination.branch != pr.Destination) ||
		(destination.repo != "" && !sameRepo(destination.repo, repo)) {
		return Verdict{Reason: ReasonDestinationChange}
	}
	if id.Branch != "" && pr.SourceBranch == id.Branch && sameRepo(pr.SourceRepo, repo) {
		return Verdict{Allow: true, Reason: string(pr.Call)}
	}
	if pr.Call == CallPRComment && slices.Contains(id.PRs, pr.Number) {
		return Verdict{Allow: true, Reason: string(pr.Call)}
	}
	return Verdict{Reason: ReasonOtherPullRequest}
}

// checkPRCreate answers for a create, whose source the body names: the branch
// must be fb - a create with no fb is a review or artifact run's, and such a
// run opens no pull request - and a source repository, when the body names
// one, must be the run's own. A create that names a fork as its source would
// open the run's work from a repository the run never pushed to.
func checkPRCreate(body []byte, id Identity, repo string) Verdict {
	keys, ok := readWriteBody(body)
	if !ok {
		return Verdict{Reason: ReasonBodyMalformed}
	}
	if id.Branch == "" {
		return Verdict{Reason: ReasonNoRunBranch}
	}
	source := keys.source()
	if source.branch != id.Branch || (source.repo != "" && !sameRepo(source.repo, repo)) {
		return Verdict{Reason: ReasonSourceMismatch}
	}
	return Verdict{Allow: true, Reason: string(CallPRCreate)}
}

// sameRepo reports whether a repository the API reports ("workspace/slug",
// whatever case it was written in) is the canonical one the job account
// carries. Anything that is not "workspace/slug" is not it: the comparison is
// the canonicalisation, so a value the policy could not have named matches
// nothing.
func sameRepo(reported, canonical string) bool {
	if reported == "" {
		return false
	}
	c, err := projectpolicy.Canonical("bitbucket", reported)
	return err == nil && c == canonical
}

// writeBody is the part of a pull-request write's body the rules read: the
// source a create names, and the destination no write may move.
type writeBody struct {
	Source      *writeEndpoint `json:"source"`
	Destination *writeEndpoint `json:"destination"`
}

type writeEndpoint struct {
	Branch     *struct{ Name string } `json:"branch"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (w *writeBody) source() (out struct{ branch, repo string }) {
	if w.Source == nil {
		return out
	}
	return endpointParts(w.Source)
}

// destination is the destination the body names, either half of it empty when
// it names none: the branch a write would move the pull request to, and the
// repository it would move it into. Both are checked, because either one
// moved is the destination moved.
func (w *writeBody) destination() struct{ branch, repo string } {
	if w.Destination == nil {
		return struct{ branch, repo string }{}
	}
	return endpointParts(w.Destination)
}

func endpointParts(e *writeEndpoint) (out struct{ branch, repo string }) {
	if e.Branch != nil {
		out.branch = e.Branch.Name
	}
	if e.Repository != nil {
		out.repo = e.Repository.FullName
	}
	return out
}

// readWriteBody parses a write's body: the JSON object the adapter sends, with
// no key named twice anywhere in it, in one spelling. Two values under one key
// is the one shape where the value the rules check and the value Bitbucket
// acts on can differ - Go's decoder keeps the last, another parser may keep
// the first - so a body that carries one is refused rather than read, and the
// same goes for two keys that differ only in case and for a key the rules read
// spelled in another case (see foldKey and readKeys).
//
// A body that is not an object at all - null, an array, a string, a number -
// is refused too: json.Unmarshal reads every one of them into this struct as
// "nothing was named", which is exactly what a body with no destination to
// move looks like.
func readWriteBody(body []byte) (*writeBody, bool) {
	var w writeBody
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return &w, true
	}
	if trimmed[0] != '{' {
		return nil, false
	}
	if !objectWithoutDuplicateKeys(trimmed) {
		return nil, false
	}
	if err := json.Unmarshal(trimmed, &w); err != nil {
		return nil, false
	}
	return &w, true
}

// objectWithoutDuplicateKeys reports whether body is exactly one JSON value
// that names no object key twice. A decoder's own decoder keeps the last of
// two values silently, which is what this walks around: every object's keys
// are collected as they are read, folded the way a decoder may fold them.
func objectWithoutDuplicateKeys(body []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	if !scanValue(dec) {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF // nothing after the value
}

// readKeys are the keys the rules read a write's body by, each in the one
// spelling bitbucket.Provider sends it. A body that spells one of them in any
// other case is refused: json.Unmarshal matches a field name case-insensitively
// and the API on the other side may not, so a "DESTINATION" this file reads as
// absent is a destination the API may move, and a "SOURCE" it reads as absent
// is a source a create may open from somewhere else (G12).
var readKeys = map[string]bool{
	"source": true, "destination": true, "branch": true,
	"name": true, "repository": true, "full_name": true,
}

// foldKey folds an object key the way a decoder's case-insensitive field
// matching may fold it, and reports whether the key is one this file can fold
// at all. Only ASCII is folded, which is every key a write body carries: a key
// with any other rune is refused rather than compared, because encoding/json
// folds a non-ASCII rune by its own orbit rule and a comparison written here
// would not be that rule.
func foldKey(name string) (string, bool) {
	for i := 0; i < len(name); i++ {
		if name[i] >= utf8.RuneSelf {
			return "", false
		}
	}
	return strings.ToLower(name), true
}

// scanValue walks one JSON value, refusing a duplicate key in any object
// inside it.
func scanValue(dec *json.Decoder) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok {
				return false
			}
			folded, ok := foldKey(name)
			if !ok || seen[folded] {
				return false
			}
			seen[folded] = true
			if readKeys[folded] && name != folded {
				return false
			}
			if !scanValue(dec) {
				return false
			}
		}
		_, err := dec.Token() // the closing '}'
		return err == nil
	case '[':
		for dec.More() {
			if !scanValue(dec) {
				return false
			}
		}
		_, err := dec.Token() // the closing ']'
		return err == nil
	}
	return false
}
