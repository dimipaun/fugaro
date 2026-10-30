package gitproxy

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

const (
	// runBranch is the fb claim of the identity these tests decide for, and
	// jobRepo is the calling job account's repository (JobAccount.Repo): the
	// canonical, provider-qualified form the policy and the launcher use.
	runBranch = "fugaro/run-a"
	jobRepo   = "bitbucket/acme/sandbox"
	// otherRepo is another repository of the same installation, and forkRepo
	// is another workspace's repository with a branch of the same name as
	// this run's - the fork G12 refuses.
	otherRepo = "bitbucket/acme/other"
	forkRepo  = "someone-else/sandbox"
)

// admittedBlock is the published block of an installation that proxies this
// repository: the repository on the allow-list, and proxy on (G3).
func admittedBlock() *projectpolicy.BitbucketToken {
	return &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/*"}, Proxy: true}
}

// runIdentity is the identity of a first run: a branch and no pull requests.
func runIdentity() Identity { return Identity{Run: "run-a", Branch: runBranch} }

// classify is Classify as the service calls it, on a whole URL: the escaped
// path and the parsed query, which is what the wire hands over.
func classify(t *testing.T, method, rawURL string) Request {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing %q: %v", rawURL, err)
	}
	req, err := Classify(method, u.EscapedPath(), u.Query())
	if err != nil {
		t.Fatalf("Classify(%s %s) = %v", method, rawURL, err)
	}
	return req
}

// refusedClass is the refusal Classify took, as its class.
func refusedClass(t *testing.T, method, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing %q: %v", rawURL, err)
	}
	_, err = Classify(method, u.EscapedPath(), u.Query())
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Classify(%s %s) = %v, want a refusal naming its class", method, rawURL, err)
	}
	return refusal.Reason
}

// TestRulesPushOnlyRunBranch is this task's red step: before the rules
// existed, a Decide that allowed everything let a push to refs/heads/main
// through. The run's own branch is the one ref a push may move, and a
// force-push to it is allowed: the run owns the branch, and telling a
// force-push from a fast-forward needs the object graph the proxy never reads
// (F11).
func TestRulesPushOnlyRunBranch(t *testing.T) {
	own := Request{
		Kind:        KindPush,
		Repo:        jobRepo,
		UpdatesRead: true,
		Updates:     []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/" + runBranch, HashLen: 40}},
	}
	if got := Decide(own, runIdentity(), jobRepo, admittedBlock()); !got.Allow {
		t.Fatalf("the run's own branch was refused: %q", got.Reason)
	}

	force := own
	force.Updates = []RefUpdate{{Old: oidB, New: oidA, Ref: "refs/heads/" + runBranch, HashLen: 40}}
	if got := Decide(force, runIdentity(), jobRepo, admittedBlock()); !got.Allow {
		t.Fatalf("a force-push to the run's own branch was refused: %q", got.Reason)
	}

	// A push that moves nothing is git's probe before a large chunked push,
	// and the rules pass it as the no-op it is (G13). A push whose update
	// request nobody read looks the same from here and must not be.
	probe := own
	probe.Updates = nil
	if got := Decide(probe, runIdentity(), jobRepo, admittedBlock()); !got.Allow {
		t.Fatalf("git's flush-only probe was refused: %q", got.Reason)
	}
	unread := probe
	unread.UpdatesRead = false
	if got := Decide(unread, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonPushUnread {
		t.Fatalf("a push whose update request was never read = %+v, want %q", got, ReasonPushUnread)
	}

	for _, tc := range []struct {
		name string
		ref  string
		want string
	}{
		{"the base branch", "refs/heads/main", ReasonOtherRef},
		{"another run's branch", "refs/heads/fugaro/run-b", ReasonOtherRef},
		{"a ref that is not a branch", "refs/notes/commits", ReasonOtherRef},
		{"a branch whose name merely starts like fb", "refs/heads/" + runBranch + "-x", ReasonOtherRef},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := own
			req.Updates = []RefUpdate{{Old: oidA, New: oidB, Ref: tc.ref, HashLen: 40}}
			got := Decide(req, runIdentity(), jobRepo, admittedBlock())
			if got.Allow || got.Reason != tc.want {
				t.Fatalf("a push to %s = %+v, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// TestRulesPushDeleteRefused: a deletion moves a ref to the zero id, which is
// not a push of the run's work but the removal of whatever is there. The
// parser reports the zero id and leaves the decision here (B1.3), and the
// decision is no - the run's own branch included.
func TestRulesPushDeleteRefused(t *testing.T) {
	for _, ref := range []string{"refs/heads/" + runBranch, "refs/heads/main", "refs/tags/v1"} {
		req := Request{
			Kind:        KindPush,
			Repo:        jobRepo,
			UpdatesRead: true,
			Updates:     []RefUpdate{{Old: oidA, New: zeroOID, Ref: ref, HashLen: 40}},
		}
		if got := Decide(req, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonRefDelete {
			t.Errorf("deleting %s = %+v, want %q", ref, got, ReasonRefDelete)
		}
	}
	// The zero id at the width a sha256 repository writes it, which a check
	// looking for forty of them would read as an ordinary update id.
	sha256 := Request{
		Kind:        KindPush,
		Repo:        jobRepo,
		UpdatesRead: true,
		Updates: []RefUpdate{{
			Old: strings.Repeat("a", 64), New: strings.Repeat("0", 64),
			Ref: "refs/heads/" + runBranch, HashLen: 64,
		}},
	}
	if got := Decide(sha256, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonRefDelete {
		t.Errorf("a sha256 delete = %+v, want %q", got, ReasonRefDelete)
	}
}

// TestRulesTagRefused: a tag is not a branch, and a run publishes no tag
// through the proxy.
func TestRulesTagRefused(t *testing.T) {
	req := Request{
		Kind:        KindPush,
		Repo:        jobRepo,
		UpdatesRead: true,
		Updates:     []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/tags/v1.0.0", HashLen: 40}},
	}
	if got := Decide(req, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonTagRef {
		t.Fatalf("a tag push = %+v, want %q", got, ReasonTagRef)
	}
	// One update of several is enough: a push that moves fb and a tag is
	// refused whole, before any pack byte is forwarded (G2).
	both := req
	both.Updates = append([]RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/" + runBranch, HashLen: 40}}, req.Updates...)
	if got := Decide(both, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonTagRef {
		t.Fatalf("a push of fb and a tag = %+v, want %q", got, ReasonTagRef)
	}
}

// TestRulesFetchOtherRepositoryRefused: a fetch is a read of one repository,
// and the run's job account names it (G3). A fetch of anything else - another
// repository of the same installation, another workspace's, another provider
// kind's spelling - is refused however well-formed it is.
func TestRulesFetchOtherRepositoryRefused(t *testing.T) {
	own := classify(t, "GET", "https://bitbucket.org/acme/sandbox.git/info/refs?service=git-upload-pack")
	if got := Decide(own, runIdentity(), jobRepo, admittedBlock()); !got.Allow || got.Reason != string(KindFetch) {
		t.Fatalf("a fetch of the run's repository = %+v, want an allow named %q", got, KindFetch)
	}
	if own.Repo != jobRepo {
		t.Fatalf("the classified repository is %q, want %q", own.Repo, jobRepo)
	}

	for _, rawURL := range []string{
		"https://bitbucket.org/acme/other.git/info/refs?service=git-upload-pack",
		"https://bitbucket.org/someone-else/sandbox.git/info/refs?service=git-upload-pack",
		"https://bitbucket.org/Acme/Sandbox.git/info/refs?service=git-upload-pack", // the case fold must agree, not merely the letters
	} {
		req := classify(t, "GET", rawURL)
		if rawURL == "https://bitbucket.org/Acme/Sandbox.git/info/refs?service=git-upload-pack" {
			// Case is canonicalised, so this one IS the run's repository.
			if got := Decide(req, runIdentity(), jobRepo, admittedBlock()); !got.Allow {
				t.Errorf("a fetch spelled in another case = %+v, want an allow", got)
			}
			continue
		}
		if got := Decide(req, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonOtherRepository {
			t.Errorf("a fetch of %s = %+v, want %q", rawURL, got, ReasonOtherRepository)
		}
	}

	// The same rule on the push side: the POST of a receive-pack for another
	// repository is refused before its update request is even looked at.
	push := classify(t, "POST", "https://bitbucket.org/acme/other.git/git-receive-pack")
	push.UpdatesRead, push.Updates = true, []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/" + runBranch, HashLen: 40}}
	if got := Decide(push, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonOtherRepository {
		t.Errorf("a push to another repository = %+v, want %q", got, ReasonOtherRepository)
	}

	// And on the REST side, where the repository is the first two segments.
	rest := classify(t, "GET", "https://api.bitbucket.org/2.0/repositories/acme/other/pullrequests/1")
	if got := Decide(rest, runIdentity(), jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonOtherRepository {
		t.Errorf("a REST read of another repository = %+v, want %q", got, ReasonOtherRepository)
	}

	// A workspace may be named "2.0", which is also the API root every REST
	// path carries: the root is dropped only in front of a route the API has
	// below it, so this git path still names a repository - and it is one the
	// policy can admit, so dropping the segment positionally would refuse a
	// job that may run.
	const rootRepo = "bitbucket/2.0/sandbox"
	dot := classify(t, "GET", "https://bitbucket.org/2.0/sandbox.git/info/refs?service=git-upload-pack")
	if dot.Repo != rootRepo {
		t.Fatalf("a fetch of the repository in the workspace 2.0 classified as %q, want %q", dot.Repo, rootRepo)
	}
	block := admittedBlock()
	block.AllowRepos = []string{"bitbucket/2.0/*"}
	if got := Decide(dot, runIdentity(), rootRepo, block); !got.Allow {
		t.Errorf("a fetch of %s = %+v, want an allow", rootRepo, got)
	}
}

// TestRulesRESTAllowList walks every REST call bitbucket.Provider makes, one
// case each, and the routes G4 refuses. The adapter is
// internal/gitprov/bitbucket: EnsurePR (the list with its q=, the create, the
// update), Comment, Repository, PullRequest, FetchPRHead (the pull request and
// the commit), UpdatePR, ApplyReady (a second update) and Comments (GET /user
// and the comment list).
func TestRulesRESTAllowList(t *testing.T) {
	const root = "https://api.bitbucket.org/2.0"
	tests := []struct {
		name   string
		method string
		path   string
		url    string
		allow  bool
		call   Call
		pr     int
		// noRepo is the one call that names no repository at all, whose
		// request carries the empty repository rather than a made-up one.
		noRepo     bool
		reason     string
		queryKeeps []string
	}{
		{
			// bitbucket.selfUser: the token's own user, the one call that
			// names no repository at all.
			name: "GET /user", method: "GET", path: "/user",
			allow: true, call: CallUser, noRepo: true, reason: string(CallUser),
		},
		{
			name: "the repository", method: "GET", path: "/repositories/acme/sandbox",
			allow: true, call: CallRepoRead, reason: string(CallRepoRead),
		},
		{
			// The case the recorded exchanges show: the workspace and the
			// slug as the token's owner wrote them.
			name: "the repository in mixed case", method: "GET", path: "/repositories/Acme/Sandbox",
			allow: true, call: CallRepoRead, reason: string(CallRepoRead),
		},
		{
			name: "the commit of a rev", method: "GET", path: "/repositories/acme/sandbox/commit/abcdefabcdef",
			allow: true, call: CallCommitRead, reason: string(CallCommitRead),
		},
		{
			name: "the pull-request list with its q=", method: "GET",
			path:  "/repositories/acme/sandbox/pullrequests?q=source.branch.name%3D%22fugaro%2Frun-a%22",
			allow: true, call: CallPRList, reason: string(CallPRList),
			queryKeeps: []string{"q"},
		},
		{
			name: "one pull request", method: "GET", path: "/repositories/acme/sandbox/pullrequests/16",
			allow: true, call: CallPRRead, pr: 16, reason: string(CallPRRead),
		},
		{
			name: "the create", method: "POST", path: "/repositories/acme/sandbox/pullrequests",
			allow: true, call: CallPRCreate, reason: string(CallPRCreate),
		},
		{
			// The update, which is also how a pull request is made ready
			// and given reviewers (ApplyReady PUTs the same path).
			name: "the update", method: "PUT", path: "/repositories/acme/sandbox/pullrequests/16",
			allow: true, call: CallPRUpdate, pr: 16, reason: string(CallPRUpdate),
		},
		{
			name: "a comment", method: "POST", path: "/repositories/acme/sandbox/pullrequests/16/comments",
			allow: true, call: CallPRComment, pr: 16, reason: string(CallPRComment),
		},
		{
			name: "the comment list", method: "GET", path: "/repositories/acme/sandbox/pullrequests/16/comments?pagelen=100",
			allow: true, call: CallCommentList, pr: 16, reason: string(CallCommentList),
			queryKeeps: []string{"pagelen"},
		},
		{
			// Bitbucket's own next link, which httpjson.PagePath turns back
			// into a path and the adapter follows.
			name: "the comment list, second page", method: "GET",
			path:  "/repositories/acme/sandbox/pullrequests/16/comments?pagelen=100&page=2",
			allow: true, call: CallCommentList, pr: 16, reason: string(CallCommentList),
			queryKeeps: []string{"pagelen", "page"},
		},
		{
			// The same call with the API root spelled out, and the same call
			// without it: the adapter's BaseURL carries /2.0 and the proxy
			// serves the path either way.
			name: "the API root spelled out", method: "GET",
			url:   root + "/repositories/acme/sandbox/pullrequests/16",
			allow: true, call: CallPRRead, pr: 16, reason: string(CallPRRead),
		},
		{
			name: "the API root left off", method: "GET",
			url:   "https://api.bitbucket.org/repositories/acme/sandbox/pullrequests/16",
			allow: true, call: CallPRRead, pr: 16, reason: string(CallPRRead),
		},
		{
			name: "the workspaces route", method: "GET", path: "/workspaces",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "one workspace", method: "GET", path: "/workspaces/acme",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the pipelines route", method: "POST", path: "/repositories/acme/sandbox/pipelines",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the settings route", method: "GET", path: "/repositories/acme/sandbox/settings",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the merge", method: "POST", path: "/repositories/acme/sandbox/pullrequests/16/merge",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the approve", method: "POST", path: "/repositories/acme/sandbox/pullrequests/16/approve",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the decline", method: "POST", path: "/repositories/acme/sandbox/pullrequests/16/decline",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the reviewers route of a pull request", method: "GET",
			path:   "/repositories/acme/sandbox/pullrequests/16/reviewers",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a pull request deleted", method: "DELETE", path: "/repositories/acme/sandbox/pullrequests/16",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a comment PUT, which is not a call the adapter makes", method: "PUT",
			path:   "/repositories/acme/sandbox/pullrequests/16/comments",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the pull-request list by POST, which is not the create's method", method: "PUT",
			path:   "/repositories/acme/sandbox/pullrequests",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a commit route that is not a hash", method: "GET",
			path:   "/repositories/acme/sandbox/commit/main",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a commit route with a tail", method: "GET",
			path:   "/repositories/acme/sandbox/commit/abcdefabcdef/statuses",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "the pull-request number spelled with a sign", method: "GET",
			path:   "/repositories/acme/sandbox/pullrequests/+16",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a query the call does not send", method: "GET",
			path:   "/repositories/acme/sandbox/pullrequests/16?fields=title",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a repeated parameter", method: "GET",
			path:   "/repositories/acme/sandbox/pullrequests/16/comments?pagelen=100&pagelen=1",
			reason: ReasonCallNotAllowed,
		},
		{
			name: "a page that is not a number", method: "GET",
			path:   "/repositories/acme/sandbox/pullrequests/16/comments?page=next",
			reason: ReasonCallNotAllowed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rawURL := tc.url
			if rawURL == "" {
				rawURL = root + tc.path
			}
			u, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parsing %q: %v", rawURL, err)
			}
			req, err := Classify(tc.method, u.EscapedPath(), u.Query())
			if !tc.allow {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Reason != tc.reason {
					t.Fatalf("Classify(%s %s) = %+v, %v, want the class %q", tc.method, tc.path, req, err, tc.reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("Classify(%s %s) = %v", tc.method, tc.path, err)
			}
			wantRepo := jobRepo
			if tc.noRepo {
				wantRepo = ""
			}
			if req.Kind != KindREST || req.Call != tc.call || req.PR != tc.pr || req.Repo != wantRepo {
				t.Fatalf("classified %+v, want the call %q of #%d in %q", req, tc.call, tc.pr, wantRepo)
			}
			if len(req.Query) != len(tc.queryKeeps) {
				t.Fatalf("carried the query %v, want only %v", req.Query, tc.queryKeeps)
			}
			for _, name := range tc.queryKeeps {
				if _, ok := req.Query[name]; !ok {
					t.Errorf("the query %v does not carry %q", req.Query, name)
				}
			}
			got := Decide(req, runIdentity(), jobRepo, admittedBlock())
			if !got.Allow || got.Reason != tc.reason {
				t.Fatalf("Decide = %+v, want an allow named %q", got, tc.reason)
			}
		})
	}
}

// TestRulesPRWriteBoundToRun is G12: a pull-request write is the run's own
// pull request, or a comment on one fpr names, and nothing else. The facts
// come from the service's own read of the pull request, so the cases below are
// the ones an attacker cannot talk their way out of: another pull request's
// update, comment or ready; a create naming another source; a fork with the
// run's branch name; a body that would move the destination.
func TestRulesPRWriteBoundToRun(t *testing.T) {
	own := PRFacts{Number: 16, SourceRepo: "acme/sandbox", SourceBranch: runBranch, Destination: "main"}
	foreign := PRFacts{Number: 7, SourceRepo: "acme/sandbox", SourceBranch: "someone-elses-work", Destination: "main"}

	tests := []struct {
		name  string
		pr    PRFacts
		body  string
		allow bool
		want  string
	}{
		{
			name: "the run's own pull request", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: own.Destination},
			body: `{"title":"x","draft":false}`, allow: true, want: string(CallPRUpdate),
		},
		{
			name: "the run's own pull request, made ready", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: own.Destination},
			body: `{"title":"x","draft":false,"reviewers":[{"account_id":"person-1"}]}`, allow: true, want: string(CallPRUpdate),
		},
		{
			name: "another pull request's update", pr: PRFacts{Call: CallPRUpdate, Number: foreign.Number, SourceRepo: foreign.SourceRepo, SourceBranch: foreign.SourceBranch, Destination: foreign.Destination},
			body: `{"title":"x"}`, want: ReasonOtherPullRequest,
		},
		{
			name: "another pull request's ready", pr: PRFacts{Call: CallPRUpdate, Number: foreign.Number, SourceRepo: foreign.SourceRepo, SourceBranch: foreign.SourceBranch, Destination: foreign.Destination},
			body: `{"draft":false}`, want: ReasonOtherPullRequest,
		},
		{
			name: "another pull request's comment", pr: PRFacts{Call: CallPRComment, Number: foreign.Number, SourceRepo: foreign.SourceRepo, SourceBranch: foreign.SourceBranch, Destination: foreign.Destination},
			body: `{"content":{"raw":"hello"}}`, want: ReasonOtherPullRequest,
		},
		{
			// A fork can carry a branch of the same name, so the repository
			// is checked and not only the branch.
			name: "a fork's pull request with the run's branch name", pr: PRFacts{Call: CallPRUpdate, Number: 7, SourceRepo: forkRepo, SourceBranch: runBranch, Destination: "main"},
			body: `{"title":"x"}`, want: ReasonOtherPullRequest,
		},
		{
			name: "a pull request whose source repository the API does not report", pr: PRFacts{Call: CallPRUpdate, Number: 7, SourceBranch: runBranch, Destination: "main"},
			body: `{"title":"x"}`, want: ReasonOtherPullRequest,
		},
		{
			name: "an update that moves the destination", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: "main"},
			body: `{"title":"x","destination":{"branch":{"name":"release"}}}`, want: ReasonDestinationChange,
		},
		{
			name: "an update that moves the destination repository", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: "main"},
			body: `{"destination":{"branch":{"name":"main"},"repository":{"full_name":"acme/other"}}}`, want: ReasonDestinationChange,
		},
		{
			// The same destination it already has changes nothing, so it is
			// not a change; the same repository is the run's own.
			name: "an update that re-states the destination", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: "main"},
			body: `{"destination":{"branch":{"name":"main"},"repository":{"full_name":"acme/sandbox"}}}`, allow: true, want: string(CallPRUpdate),
		},
		{
			name: "a create naming fb", pr: PRFacts{Call: CallPRCreate},
			body: `{"title":"x","source":{"branch":{"name":"` + runBranch + `"}},"destination":{"branch":{"name":"main"}}}`, allow: true, want: string(CallPRCreate),
		},
		{
			name: "a create naming another source branch", pr: PRFacts{Call: CallPRCreate},
			body: `{"source":{"branch":{"name":"main"}}}`, want: ReasonSourceMismatch,
		},
		{
			name: "a create naming no source at all", pr: PRFacts{Call: CallPRCreate},
			body: `{"title":"x"}`, want: ReasonSourceMismatch,
		},
		{
			name: "a create naming another source repository", pr: PRFacts{Call: CallPRCreate},
			body: `{"source":{"branch":{"name":"` + runBranch + `"},"repository":{"full_name":"` + forkRepo + `"}}}`, want: ReasonSourceMismatch,
		},
		{
			name: "a body that is not an object", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: "main"},
			body: `"a string"`, want: ReasonBodyMalformed,
		},
		{
			// Two values under one key: the rules would read the last and
			// Bitbucket may act on the first.
			name: "a body that names destination twice", pr: PRFacts{Call: CallPRUpdate, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: "main"},
			body: `{"destination":{"branch":{"name":"main"}},"destination":{"branch":{"name":"release"}}}`, want: ReasonBodyMalformed,
		},
		{
			name: "a body that names the source twice", pr: PRFacts{Call: CallPRCreate},
			body: `{"source":{"branch":{"name":"` + runBranch + `"}},"source":{"branch":{"name":"main"}}}`, want: ReasonBodyMalformed,
		},
		{
			name: "a body that is not JSON", pr: PRFacts{Call: CallPRComment, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch, Destination: "main"},
			body: `{"content":{"raw":"x"}`, want: ReasonBodyMalformed,
		},
		{
			name: "a read is not a write", pr: PRFacts{Call: CallPRRead, Number: own.Number, SourceRepo: own.SourceRepo, SourceBranch: own.SourceBranch},
			want: ReasonCallNotAllowed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckPRWrite(tc.pr, []byte(tc.body), runIdentity(), jobRepo)
			if got.Allow != tc.allow || got.Reason != tc.want {
				t.Fatalf("CheckPRWrite = %+v, want allow=%v reason %q", got, tc.allow, tc.want)
			}
		})
	}

	// Merge, approve and decline are not writes this proxy names: they are
	// not routes Classify has a call for, so they are refused before any
	// pull request is read.
	for _, path := range []string{
		"/2.0/repositories/acme/sandbox/pullrequests/16/merge",
		"/2.0/repositories/acme/sandbox/pullrequests/16/approve",
		"/2.0/repositories/acme/sandbox/pullrequests/16/decline",
	} {
		if got := refusedClass(t, "POST", "https://api.bitbucket.org"+path); got != ReasonCallNotAllowed {
			t.Errorf("%s was refused as %q, want %q", path, got, ReasonCallNotAllowed)
		}
	}
}

// TestRulesReviewRunCommentsOnlyOnFPR: a review run is the identity with no
// branch (G6). It pushes nothing, opens no pull request and updates none -
// and it may comment on exactly the pull requests fpr names, which live in
// other repositories, so the source repository and branch are not what allows
// those.
func TestRulesReviewRunCommentsOnlyOnFPR(t *testing.T) {
	review := Identity{Run: "run-r", PRs: []int{9, 11}}
	underReview := PRFacts{Call: CallPRComment, Number: 9, SourceRepo: forkRepo, SourceBranch: "their-branch", Destination: "main"}
	if got := CheckPRWrite(underReview, []byte(`{"content":{"raw":"a finding"}}`), review, jobRepo); !got.Allow {
		t.Fatalf("a comment on a pull request under review = %+v, want an allow", got)
	}

	elsewhere := underReview
	elsewhere.Number = 12
	if got := CheckPRWrite(elsewhere, nil, review, jobRepo); got.Allow || got.Reason != ReasonOtherPullRequest {
		t.Errorf("a comment on a pull request fpr does not name = %+v, want %q", got, ReasonOtherPullRequest)
	}
	// fpr covers comments only: an update of a pull request under review is
	// not the review run's to make.
	update := underReview
	update.Call = CallPRUpdate
	if got := CheckPRWrite(update, []byte(`{"draft":false}`), review, jobRepo); got.Allow || got.Reason != ReasonOtherPullRequest {
		t.Errorf("an update of a pull request under review = %+v, want %q", got, ReasonOtherPullRequest)
	}
	if got := CheckPRWrite(PRFacts{Call: CallPRCreate}, []byte(`{"source":{"branch":{"name":"`+runBranch+`"}}}`), review, jobRepo); got.Allow || got.Reason != ReasonNoRunBranch {
		t.Errorf("a create from a review run = %+v, want %q", got, ReasonNoRunBranch)
	}

	push := Request{
		Kind:        KindPush,
		Repo:        jobRepo,
		UpdatesRead: true,
		Updates:     []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/" + runBranch, HashLen: 40}},
	}
	if got := Decide(push, review, jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonNoRunBranch {
		t.Errorf("a push from a review run = %+v, want %q", got, ReasonNoRunBranch)
	}
	// A review run fetches, though: it has to, to read what it reviews.
	fetch := classify(t, "GET", "https://bitbucket.org/acme/sandbox.git/info/refs?service=git-upload-pack")
	if got := Decide(fetch, review, jobRepo, admittedBlock()); !got.Allow {
		t.Errorf("a fetch from a review run = %+v, want an allow", got)
	}
}

// TestRulesNoBranchNoPush: without fb there is no branch a push could move,
// and the refusal is named for that and not for the ref the caller chose - the
// caller's ref is not what was wrong with the request.
func TestRulesNoBranchNoPush(t *testing.T) {
	id := Identity{Run: "run-a"}
	for _, ref := range []string{"refs/heads/" + runBranch, "refs/heads/main", "refs/tags/v1"} {
		req := Request{
			Kind:        KindPush,
			Repo:        jobRepo,
			UpdatesRead: true,
			Updates:     []RefUpdate{{Old: oidA, New: oidB, Ref: ref, HashLen: 40}},
		}
		if got := Decide(req, id, jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonNoRunBranch {
			t.Errorf("a push of %s without fb = %+v, want %q", ref, got, ReasonNoRunBranch)
		}
	}
	// An identity whose token was minted before fb existed reads back empty
	// too, and is refused the same way (B1.1): the proxy never guesses a
	// branch for it.
	probe := Request{Kind: KindPush, Repo: jobRepo, UpdatesRead: true}
	if got := Decide(probe, id, jobRepo, admittedBlock()); got.Allow || got.Reason != ReasonNoRunBranch {
		t.Errorf("git's probe without fb = %+v, want %q", got, ReasonNoRunBranch)
	}
}

// TestRulesNotAdmittedByBlock: the published block is the admission source of
// a proxied job, which holds no token to be scoped by anything else (G3). A
// project that published none, and one whose allow-list does not carry this
// repository, both admit nothing.
func TestRulesNotAdmittedByBlock(t *testing.T) {
	req := classify(t, "GET", "https://bitbucket.org/acme/sandbox.git/info/refs?service=git-upload-pack")
	for _, tc := range []struct {
		name  string
		block *projectpolicy.BitbucketToken
	}{
		{"no published block", nil},
		{"an allow-list that does not carry this repository", &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/other/*"}, Proxy: true}},
		{"an empty allow-list, which admits nothing", &projectpolicy.BitbucketToken{AllowRepos: nil, Proxy: true}},
		{"a wildcard of another provider", &projectpolicy.BitbucketToken{AllowRepos: []string{"github/acme/*"}, Proxy: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(req, runIdentity(), jobRepo, tc.block); got.Allow || got.Reason != ReasonNotAdmitted {
				t.Fatalf("Decide = %+v, want %q", got, ReasonNotAdmitted)
			}
		})
	}
	// The wildcard is the policy's own matcher and never crosses a "/", so a
	// pattern that stops short of the repository does not admit it.
	short := &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/ac*"}, Proxy: true}
	if got := Decide(req, runIdentity(), jobRepo, short); got.Allow || got.Reason != ReasonNotAdmitted {
		t.Fatalf("a pattern that does not match = %+v, want %q", got, ReasonNotAdmitted)
	}
}

// TestRulesProxyFalse: proxy: false is an installation that keeps mounting the
// token on its jobs (G9). Its runs hold the credential themselves and reach
// Bitbucket directly, so nothing is served through here - not even a fetch of
// a repository the block does list.
func TestRulesProxyFalse(t *testing.T) {
	block := &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/*"}}
	req := classify(t, "GET", "https://bitbucket.org/acme/sandbox.git/info/refs?service=git-upload-pack")
	if got := Decide(req, runIdentity(), jobRepo, block); got.Allow || got.Reason != ReasonProxyOff {
		t.Fatalf("Decide with proxy off = %+v, want %q", got, ReasonProxyOff)
	}
	user := classify(t, "GET", "https://api.bitbucket.org/2.0/user")
	if got := Decide(user, runIdentity(), jobRepo, block); got.Allow || got.Reason != ReasonProxyOff {
		t.Fatalf("GET /user with proxy off = %+v, want %q", got, ReasonProxyOff)
	}
}

// TestRulesPathTricks: every spelling whose meaning depends on who decodes it
// is refused before anything is classified, because a path the rules read one
// way and git reads another is a ref nobody checked. The path handed to
// Classify is the escaped one, which is what the wire carries.
func TestRulesPathTricks(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		query  url.Values
		want   string
	}{
		{"an encoded separator in the repository", "GET", "/repositories/acme/sandbox%2Fother/pullrequests", nil, ReasonPathMalformed},
		{"an encoded separator in the workspace", "GET", "/repositories/ac%2Fme/sandbox", nil, ReasonPathMalformed},
		{"an encoded separator in the upper case", "GET", "/repositories/acme/sandbox%2Fother", nil, ReasonPathMalformed},
		{"an encoded backslash", "GET", "/repositories/acme/sandbox%5cother", nil, ReasonPathMalformed},
		{"an encoded backslash in the upper case", "GET", "/repositories/acme/sandbox%5Cother", nil, ReasonPathMalformed},
		{"a dot segment", "GET", "/repositories/acme/../sandbox", nil, ReasonPathMalformed},
		{"an encoded dot segment", "GET", "/repositories/acme/%2e%2e/sandbox", nil, ReasonPathMalformed},
		{"a single-dot segment", "GET", "/repositories/acme/./sandbox", nil, ReasonPathMalformed},
		{"a trailing empty segment", "GET", "/repositories/acme/sandbox/", nil, ReasonPathMalformed},
		{"a doubled separator", "GET", "/repositories//acme/sandbox", nil, ReasonPathMalformed},
		{"a path with no leading separator", "GET", "repositories/acme/sandbox", nil, ReasonPathMalformed},
		{"a NUL in the repository", "GET", "/repositories/acme/sand%00box", nil, ReasonPathMalformed},
		{"a carriage return", "GET", "/repositories/acme/sandbox%0d%0apullrequests", nil, ReasonPathMalformed},
		{"an escape that is not one", "GET", "/repositories/acme/sand%zzbox", nil, ReasonPathMalformed},
		{"a bare percent", "GET", "/repositories/acme/sand%box", nil, ReasonPathMalformed},
		{"an encoded separator in a git path", "GET", "/acme/sand%2Fbox.git/info/refs", url.Values{"service": {"git-upload-pack"}}, ReasonPathMalformed},
		{"a dot segment in a git path", "POST", "/acme/../sandbox.git/git-receive-pack", nil, ReasonPathMalformed},
		{"a git path with a dot segment before the action", "GET", "/acme/sandbox.git/../info/refs", nil, ReasonPathMalformed},
		// Well-formed, and still not a repository: the segment does not
		// survive canonicalisation, so it names nothing a job account could
		// be.
		{"a repository that is not a name", "GET", "/repositories/acme/sand~box", nil, ReasonCallNotAllowed},
		{"a repository that is only .git", "GET", "/repositories/acme/.git", nil, ReasonCallNotAllowed},
		{"a git path for a nested repository", "GET", "/acme/group/sandbox.git/info/refs", url.Values{"service": {"git-upload-pack"}}, ReasonCallNotAllowed},
		{"a git path for a repository that is not a name", "GET", "/acme/sand~box.git/git-receive-pack", nil, ReasonCallNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Classify(tc.method, tc.path, tc.query)
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("Classify(%s %s) = %v, want a refusal", tc.method, tc.path, err)
			}
			if refusal.Reason != tc.want {
				t.Fatalf("Classify(%s %s) was refused as %q, want %q", tc.method, tc.path, refusal.Reason, tc.want)
			}
			if strings.Contains(err.Error(), tc.path) {
				t.Errorf("the refusal %q echoes the path it refused", err)
			}
		})
	}

	// An escape that is harmless is decoded rather than refused, and what is
	// classified is the repository the decode names: "sandb%6fx" is
	// "sandbox", and the rules decide on that, not on the caller's spelling.
	req := classify(t, "GET", "https://bitbucket.org/acme/sandb%6fx.git/info/refs?service=git-upload-pack")
	if req.Repo != jobRepo {
		t.Fatalf("an escaped repository classified as %q, want %q", req.Repo, jobRepo)
	}
	if got := Decide(req, runIdentity(), jobRepo, admittedBlock()); !got.Allow {
		t.Fatalf("a fetch of an escaped spelling of the run's repository = %+v, want an allow", got)
	}
}

// TestRulesRefusalsNeverEchoTheRequest: a refusal is a class, and a class
// carries no part of what was refused. The paths below are caller text - an
// attacker's, by definition, since they are the ones refused - and an audit
// line or a log that repeated one would be a place an attacker writes to. The
// last case is the one shape that is not refused while classifying: a
// well-formed path naming a repository that is simply not this run's, which
// only Decide can refuse, and which is refused by class too.
func TestRulesRefusalsNeverEchoTheRequest(t *testing.T) {
	const canary = "canary-canary-canary"
	for _, tc := range []struct {
		method string
		path   string
		query  url.Values
	}{
		{"GET", "/repositories/" + canary + "/sandbox%2F" + canary, nil},
		{"GET", "/repositories/acme/sandbox/" + canary, nil},
		{"POST", "/repositories/acme/sandbox/pullrequests/16/" + canary, nil},
		{"GET", "/repositories/acme/sandbox/pullrequests/16", url.Values{"fields": {canary}}},
		{"GET", "/repositories/acme/sandbox/pullrequests/" + canary, nil},
	} {
		_, err := Classify(tc.method, tc.path, tc.query)
		if err == nil {
			t.Fatalf("Classify(%s %s) was accepted", tc.method, tc.path)
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("the refusal %q echoes the caller's bytes", err)
		}
	}

	// The rest is refused one layer up: these paths are well formed - a
	// repository name may be anything a repository name may be, and a git
	// path is a git path - so they classify, and what refuses them is the
	// run's own repository. Those refusals are classes too.
	for _, tc := range []struct {
		method string
		path   string
		query  url.Values
	}{
		{"GET", "/repositories/" + canary + "/sandbox/pullrequests/16", nil},
		{"GET", "/" + canary + "/sandbox.git/info/refs", url.Values{"service": {"git-upload-pack"}}},
		{"POST", "/" + canary + "/sandbox.git/git-receive-pack", nil},
	} {
		req, err := Classify(tc.method, tc.path, tc.query)
		if err != nil {
			t.Fatalf("Classify(%s %s) refused a well-formed path: %v", tc.method, tc.path, err)
		}
		v := Decide(req, runIdentity(), jobRepo, admittedBlock())
		if v.Allow {
			t.Fatalf("Classify(%s %s) was allowed", tc.method, tc.path)
		}
		if strings.Contains(v.Reason, canary) {
			t.Errorf("the refusal %q echoes the caller's bytes", v.Reason)
		}
	}
}
