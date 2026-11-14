package bbtokens

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

// testToken is the token the check is handed. It is plain words and not a
// token-shaped string: a fixture carries no secret-shaped literal, and the one
// test that reads every field of the Result back depends on the value being
// distinctive enough to find if it ever appeared.
const testToken = "plain-words-not-a-token"

// aDayComesLater and aDayHasPassed are the two expiries the records here carry.
// They are days and not offsets from now, because the record's expiry is the
// local config's own 2006-01-02 spelling (R6) and a test that computed one
// would be testing its own arithmetic.
const (
	aDayComesLater = "2099-01-01"
	aDayHasPassed  = "2020-01-01"
)

// fakeAnswer is one answer the fake bitbucket.org gives.
type fakeAnswer struct {
	status int
	body   string
	header map[string]string
}

// seenRequest is one call the check made, as the fake saw it before the hop was
// rewritten: where it was addressed, and what it carried.
type seenRequest struct {
	method string
	scheme string
	host   string
	path   string
	query  string
	auth   string
}

// fakeBitbucket stands in for api.bitbucket.org. Every call the check makes
// arrives through the transport below, which records it as it was addressed —
// scheme, host, path, query and credential — and then serves it from the
// fixture; a call to a host that is not Bitbucket's own API root is refused and
// failed. That pins F22 at once: a check that addressed anywhere else is seen
// here, and so is the token it carried there. It answers by method and path,
// and it fails the test on a request it has no answer for, so a call the check
// should not make is a failure rather than an assertion nobody wrote.
type fakeBitbucket struct {
	t       *testing.T
	srv     *httptest.Server
	answers map[string]fakeAnswer

	mu   sync.Mutex
	seen []seenRequest
}

func newFakeBitbucket(t *testing.T, answers map[string]fakeAnswer) *fakeBitbucket {
	t.Helper()
	f := &fakeBitbucket{t: t, answers: answers}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBitbucket) serve(w http.ResponseWriter, r *http.Request) {
	a, ok := f.answers[r.Method+" "+r.URL.Path]
	if !ok {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		http.Error(w, "no answer", http.StatusInternalServerError)
		return
	}
	for k, v := range a.header {
		w.Header().Set(k, v)
	}
	if a.body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(a.status)
	_, _ = w.Write([]byte(a.body))
}

func (f *fakeBitbucket) record(s seenRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, s)
}

func (f *fakeBitbucket) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.seen...)
}

func (f *fakeBitbucket) paths() []string {
	var out []string
	for _, r := range f.requests() {
		out = append(out, r.method+" "+r.path)
	}
	return out
}

// verifier is the check under test, reading the constant root through this
// fake: the verifier has no base URL to set, which is the rule F22 asks for.
func (f *fakeBitbucket) verifier() *Verifier { return &Verifier{Client: f.client()} }

func (f *fakeBitbucket) client() *http.Client {
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		f.t.Fatalf("the fixture's URL is not a URL: %v", err)
	}
	return &http.Client{Transport: &fakeTransport{base: base, fake: f}}
}

// fakeTransport serves a call addressed at Bitbucket's own API root from the
// fixture, and refuses any other host, so no test in this package can reach a
// network and no build can present the operator's token to a host the adapter's
// constant does not name.
type fakeTransport struct {
	base *url.URL
	fake *fakeBitbucket
}

func (h *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h.fake.record(seenRequest{
		method: r.Method,
		scheme: r.URL.Scheme,
		host:   r.URL.Host,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		auth:   r.Header.Get("Authorization"),
	})
	if r.URL.Scheme != "https" || r.URL.Host != "api.bitbucket.org" {
		return nil, fmt.Errorf("the check addressed %s://%s, and Bitbucket's API root is https://api.bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	c := r.Clone(r.Context())
	c.URL.Scheme = h.base.Scheme
	c.URL.Host = h.base.Host
	c.Host = h.base.Host
	return http.DefaultTransport.RoundTrip(c)
}

// repoAnswers is a repository token's whole check: the repository read, the
// pull-request read within it, and the identity.
func repoAnswers() map[string]fakeAnswer {
	return map[string]fakeAnswer{
		"GET /2.0/repositories/acme/fugaro":              {status: 200, body: `{"full_name":"acme/fugaro","project":{"uuid":"{proj-1}"}}`},
		"GET /2.0/repositories/acme/fugaro/pullrequests": {status: 200, body: `{"values":[]}`},
		"GET /2.0/user": {status: 200, body: `{"uuid":"{aaaa-2222}","kind":"repository_access_token"}`},
	}
}

func repoRecord() Record {
	return Record{Name: "bitbucket-token--ci", Type: TypeRepo, Coverage: []string{"acme/fugaro"}, Expires: aDayComesLater}
}

func workspaceRecord() Record {
	return Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"acme/*"}, Expires: aDayComesLater}
}

func projectRecord() Record {
	return Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"acme/*"}, ProjectKey: "FUG", Expires: aDayComesLater}
}

func TestVerifyRepoWorkspaceAndProjectCoverage(t *testing.T) {
	t.Run("a repository token", func(t *testing.T) {
		f := newFakeBitbucket(t, repoAnswers())
		res := f.verifier().Verify(context.Background(), testToken, repoRecord())
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		if res.Message != "" {
			t.Errorf("message = %q, want none", res.Message)
		}
		if res.Identity != "{aaaa-2222}" {
			t.Errorf("identity = %q, want the account UUID Bitbucket answered", res.Identity)
		}
		if want := []string{"repository:read", "pullrequest:read"}; !slices.Equal(res.ReadScopes, want) {
			t.Errorf("read scopes = %v, want %v", res.ReadScopes, want)
		}
		if res.ProjectUUID != "" {
			t.Errorf("project UUID = %q, want none for a repository token", res.ProjectUUID)
		}
	})

	t.Run("a repository token covering two repositories", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme/one":              {status: 200, body: `{"full_name":"acme/one"}`},
			"GET /2.0/repositories/acme/one/pullrequests": {status: 200, body: `{"values":[]}`},
			"GET /2.0/repositories/acme/two":              {status: 200, body: `{"full_name":"acme/two"}`},
			"GET /2.0/repositories/acme/two/pullrequests": {status: 200, body: `{"values":[]}`},
			"GET /2.0/user": {status: 200, body: `{"uuid":"{aaaa-2222}","kind":"repository_access_token"}`},
		})
		rec := repoRecord()
		rec.Coverage = []string{"acme/one", "acme/two"}
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		if want := []string{"repository:read", "pullrequest:read"}; !slices.Equal(res.ReadScopes, want) {
			t.Errorf("read scopes = %v, want each proven once, %v", res.ReadScopes, want)
		}
	})

	t.Run("a workspace token", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 200, body: `{"values":[{"full_name":"acme/one"}]}`},
			"GET /2.0/user":              {status: 200, body: `{"uuid":"{bbbb-3333}","kind":"workspace_access_token"}`},
		})
		res := f.verifier().Verify(context.Background(), testToken, workspaceRecord())
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		if res.Identity != "{bbbb-3333}" {
			t.Errorf("identity = %q, want the account UUID Bitbucket answered", res.Identity)
		}
		if want := []string{"repository:read"}; !slices.Equal(res.ReadScopes, want) {
			t.Errorf("read scopes = %v, want %v", res.ReadScopes, want)
		}
	})

	t.Run("a project token whose record names a key", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/workspaces/acme/projects/FUG": {status: 200, body: `{"uuid":"{proj-1}","key":"FUG"}`},
			"GET /2.0/repositories/acme":            {status: 200, body: `{"values":[{"project":{"uuid":"{proj-1}"}}]}`},
			"GET /2.0/user":                         {status: 200, body: `{"uuid":"{cccc-4444}","kind":"project_access_token"}`},
		})
		res := f.verifier().Verify(context.Background(), testToken, projectRecord())
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		if res.ProjectUUID != "{proj-1}" {
			t.Errorf("project UUID = %q, want the UUID the project read answered", res.ProjectUUID)
		}
		if res.Identity != "{cccc-4444}" {
			t.Errorf("identity = %q, want the account UUID Bitbucket answered", res.Identity)
		}
		if want := []string{"repository:read"}; !slices.Equal(res.ReadScopes, want) {
			t.Errorf("read scopes = %v, want %v", res.ReadScopes, want)
		}
		var listing *seenRequest
		for _, r := range f.requests() {
			if r.path == "/2.0/repositories/acme" {
				listing = &r
			}
		}
		if listing == nil {
			t.Fatal("the check never read the workspace's repositories")
		}
		if !strings.Contains(listing.query, "project.uuid") || !strings.Contains(listing.query, url.QueryEscape("{proj-1}")) {
			t.Errorf("the project read's query = %q, want the record's project UUID in it", listing.query)
		}
	})

	t.Run("a project token whose record already names the UUID", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 200, body: `{"values":[{"project":{"uuid":"{proj-1}"}}]}`},
			"GET /2.0/user":              {status: 200, body: `{"uuid":"{cccc-4444}","kind":"project_access_token"}`},
		})
		rec := projectRecord()
		rec.ProjectKey, rec.ProjectUUID = "", "{proj-1}"
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		if res.ProjectUUID != "{proj-1}" {
			t.Errorf("project UUID = %q, want the record's own", res.ProjectUUID)
		}
		for _, path := range f.paths() {
			if strings.Contains(path, "/projects/") {
				t.Errorf("the check read a project for a record that already names its UUID: %v", f.paths())
			}
		}
	})
}

func TestVerifyNamesWhatIsMissing(t *testing.T) {
	refused := func(t *testing.T, f *fakeBitbucket, rec Record, token string) Result {
		t.Helper()
		res := f.verifier().Verify(context.Background(), token, rec)
		if res.OK {
			t.Fatalf("OK = true, want a refusal")
		}
		return res
	}

	t.Run("an expired token", func(t *testing.T) {
		f := newFakeBitbucket(t, nil)
		rec := repoRecord()
		rec.Expires = aDayHasPassed
		res := refused(t, f, rec, testToken)
		if res.Message != msgExpired {
			t.Errorf("message = %q, want %q", res.Message, msgExpired)
		}
		if got := f.requests(); len(got) != 0 {
			t.Errorf("an expired token was checked with %v, want no call at all", got)
		}
	})

	t.Run("an expiry that is not a day", func(t *testing.T) {
		f := newFakeBitbucket(t, nil)
		rec := repoRecord()
		rec.Expires = "next month"
		res := refused(t, f, rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
		if got := f.requests(); len(got) != 0 {
			t.Errorf("a record this check cannot read was checked with %v, want no call", got)
		}
	})

	t.Run("no token", func(t *testing.T) {
		f := newFakeBitbucket(t, nil)
		res := refused(t, f, repoRecord(), "")
		if res.Message != msgNoToken {
			t.Errorf("message = %q, want %q", res.Message, msgNoToken)
		}
	})

	t.Run("a repository the token does not cover", func(t *testing.T) {
		answers := repoAnswers()
		answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{status: 404, body: `{"error":{"message":"Not found"}}`}
		res := refused(t, newFakeBitbucket(t, answers), repoRecord(), testToken)
		if want := "the token does not cover repository acme/fugaro"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("a second repository the token does not cover", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme/one":              {status: 200, body: `{"full_name":"acme/one"}`},
			"GET /2.0/repositories/acme/one/pullrequests": {status: 200, body: `{"values":[]}`},
			"GET /2.0/repositories/acme/two":              {status: 404, body: `{"error":{"message":"Not found"}}`},
		})
		rec := repoRecord()
		rec.Coverage = []string{"acme/one", "acme/two"}
		res := refused(t, f, rec, testToken)
		if want := "the token does not cover repository acme/two"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("a workspace the token does not cover", func(t *testing.T) {
		res := refused(t, newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 404, body: `{"error":{"message":"Not found"}}`},
		}), workspaceRecord(), testToken)
		if want := "the token does not cover workspace acme"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("a project the token does not cover", func(t *testing.T) {
		res := refused(t, newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/workspaces/acme/projects/FUG": {status: 200, body: `{"uuid":"{proj-1}","key":"FUG"}`},
			"GET /2.0/repositories/acme":            {status: 200, body: `{"values":[]}`},
		}), projectRecord(), testToken)
		if want := "the token does not cover project FUG in workspace acme"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("a listing that answers another project", func(t *testing.T) {
		res := refused(t, newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 200, body: `{"values":[{"project":{"uuid":"{other-project}"}}]}`},
		}), func() Record {
			rec := projectRecord()
			rec.ProjectKey, rec.ProjectUUID = "", "{proj-1}"
			return rec
		}(), testToken)
		if want := "the token does not cover project {proj-1} in workspace acme"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("a read the token lacks", func(t *testing.T) {
		answers := repoAnswers()
		answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{status: 403, body: `{"error":{"message":"Forbidden"}}`}
		res := refused(t, newFakeBitbucket(t, answers), repoRecord(), testToken)
		if res.Message != msgMissingRepoRead {
			t.Errorf("message = %q, want %q", res.Message, msgMissingRepoRead)
		}
	})

	t.Run("a pull-request read the token lacks", func(t *testing.T) {
		answers := repoAnswers()
		answers["GET /2.0/repositories/acme/fugaro/pullrequests"] = fakeAnswer{status: 403, body: `{"error":{"message":"Forbidden"}}`}
		res := refused(t, newFakeBitbucket(t, answers), repoRecord(), testToken)
		if res.Message != msgMissingPullRequestRead {
			t.Errorf("message = %q, want %q", res.Message, msgMissingPullRequestRead)
		}
	})

	t.Run("a token bitbucket.org refuses", func(t *testing.T) {
		answers := repoAnswers()
		answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{status: 401, body: `{"error":{"message":"Unauthorized"}}`}
		res := refused(t, newFakeBitbucket(t, answers), repoRecord(), testToken)
		if res.Message != msgRefused {
			t.Errorf("message = %q, want %q", res.Message, msgRefused)
		}
	})

	t.Run("a record whose type and coverage disagree", func(t *testing.T) {
		f := newFakeBitbucket(t, nil)
		rec := repoRecord()
		rec.Type = TypeWorkspace
		res := refused(t, f, rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
		if got := f.requests(); len(got) != 0 {
			t.Errorf("a record this check cannot read was checked with %v, want no call", got)
		}
	})

	t.Run("a repository record covering a whole workspace", func(t *testing.T) {
		rec := repoRecord()
		rec.Coverage = []string{"acme/*"}
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	t.Run("a repository record covering nothing", func(t *testing.T) {
		rec := repoRecord()
		rec.Coverage = nil
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	t.Run("a record of no type", func(t *testing.T) {
		rec := repoRecord()
		rec.Type = TypeUntyped
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	t.Run("a type R1 does not name", func(t *testing.T) {
		rec := repoRecord()
		rec.Type = Type("vault")
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	t.Run("a project record that names no project", func(t *testing.T) {
		rec := projectRecord()
		rec.ProjectKey = ""
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	t.Run("a project record covering two workspaces", func(t *testing.T) {
		rec := projectRecord()
		rec.Coverage = []string{"acme/*", "other/*"}
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})
}

func TestVerifyMakesOnlyReadCalls(t *testing.T) {
	t.Run("the whole check is reads", func(t *testing.T) {
		f := newFakeBitbucket(t, repoAnswers())
		if res := f.verifier().Verify(context.Background(), testToken, repoRecord()); !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		want := []string{
			"GET /2.0/repositories/acme/fugaro",
			"GET /2.0/repositories/acme/fugaro/pullrequests",
			"GET /2.0/user",
		}
		if got := f.paths(); !slices.Equal(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		for _, r := range f.requests() {
			if r.method != http.MethodGet {
				t.Errorf("%s %s is not a read", r.method, r.path)
			}
		}
		for _, r := range f.requests() {
			if r.path == "/2.0/repositories/acme/fugaro/pullrequests" && r.query != "pagelen=1" {
				t.Errorf("the pull-request read's query = %q, want one page", r.query)
			}
		}
	})

	t.Run("every call addresses Bitbucket's own root", func(t *testing.T) {
		f := newFakeBitbucket(t, repoAnswers())
		if res := f.verifier().Verify(context.Background(), testToken, repoRecord()); !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		for _, r := range f.requests() {
			if r.scheme != "https" || r.host != "api.bitbucket.org" {
				t.Errorf("the check addressed %s://%s, want Bitbucket's own API root alone", r.scheme, r.host)
			}
		}
	})
}

func TestVerifyReportsWriteNotVerified(t *testing.T) {
	answers := repoAnswers()
	answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{
		status: 200,
		body:   `{"full_name":"acme/fugaro"}`,
		header: map[string]string{"X-OAuth-Scopes": "repository:write pullrequest:write"},
	}
	res := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
	if !res.OK {
		t.Fatalf("OK = false, message %q", res.Message)
	}
	if want := []string{"repository:write", "pullrequest:write"}; !slices.Equal(res.Scopes, want) {
		t.Errorf("recorded scopes = %v, want what the answer's header said", res.Scopes)
	}
	if res.WriteVerified {
		t.Error("the check reported the write scopes verified: nothing here writes")
	}
	if want := []string{"repository:write", "pullrequest:write"}; !slices.Equal(res.WriteScopes, want) {
		t.Errorf("write scopes = %v, want the list the operator is told is unverified", res.WriteScopes)
	}
	for _, s := range res.ReadScopes {
		if strings.HasSuffix(s, ":write") {
			t.Errorf("read scopes = %v, want reads only: a header is not a proof", res.ReadScopes)
		}
	}
}

// TestVerifyNeverReturnsTheToken reads every field of the Result back on both
// sides of the check. The token's own presentation is asserted first: a result
// that carried no token because the check never presented one would pass the
// rest of this test for the wrong reason.
func TestVerifyNeverReturnsTheToken(t *testing.T) {
	f := newFakeBitbucket(t, repoAnswers())
	ok := f.verifier().Verify(context.Background(), testToken, repoRecord())
	if !ok.OK {
		t.Fatalf("OK = false, message %q", ok.Message)
	}
	if got := f.requests()[0].auth; got != "Bearer "+testToken {
		t.Fatalf("the check presented %q, want the token it was handed", got)
	}
	answers := repoAnswers()
	answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{status: 404, body: `{"error":{"message":"Not found"}}`}
	bad := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
	if bad.OK {
		t.Fatal("OK = true, want a refusal")
	}
	refused := newFakeBitbucket(t, nil).verifier().Verify(context.Background(), "", repoRecord())
	for _, res := range []Result{ok, bad, refused} {
		if dump := fmt.Sprintf("%+v", res); strings.Contains(dump, testToken) {
			t.Errorf("the result carries the token: %s", dump)
		}
	}
}

func TestVerifyRefusesANonAccessTokenKind(t *testing.T) {
	cases := map[string]string{
		"a person's token":     `{"uuid":"{aaaa-2222}","kind":"user"}`,
		"no kind at all":       `{"uuid":"{aaaa-2222}"}`,
		"another token's kind": `{"uuid":"{aaaa-2222}","kind":"workspace_access_token"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			answers := repoAnswers()
			answers["GET /2.0/user"] = fakeAnswer{status: 200, body: body}
			res := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
			if res.OK {
				t.Fatal("OK = true, want a refusal")
			}
			if want := msgNotAccessToken(TypeRepo); res.Message != want {
				t.Errorf("message = %q, want %q", res.Message, want)
			}
			if res.Identity != "" {
				t.Errorf("identity = %q, want none: a refused token enters no identity set", res.Identity)
			}
		})
	}
}

func TestVerifyAccepts403FromUserAsARepositoryToken(t *testing.T) {
	answers := repoAnswers()
	answers["GET /2.0/user"] = fakeAnswer{status: 403, body: `{"type":"error","error":{"message":"This API is not accessible by this authentication mechanism"}}`}
	res := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
	if !res.OK {
		t.Fatalf("OK = false, message %q", res.Message)
	}
	if res.Identity != "" {
		t.Errorf("identity = %q, want none: a repository token has none at this endpoint (R14)", res.Identity)
	}
}
