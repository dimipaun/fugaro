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
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
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

// The three fixtures spell coverage the ONE way a record on this side has it:
// the node's provider-qualified form, which is also the form Record.Validate
// holds a record to and the verifier must therefore accept. A fixture in the
// config's two-segment spelling is a record the contract refuses, and a check
// that agreed with it would agree with nothing: the test that keeps the two in
// step is checksValid below, asserted in every success test.
func repoRecord() Record {
	return Record{Name: "bitbucket-token--ci", Type: TypeRepo, Coverage: []string{"bitbucket/acme/fugaro"}, Expires: aDayComesLater}
}

func workspaceRecord() Record {
	return Record{Name: "bitbucket-token--ws", Type: TypeWorkspace, Coverage: []string{"bitbucket/acme/*"}, Expires: aDayComesLater}
}

// keyOnlyProjectRecord names a KEY and no UUID, which is the record `creds set` holds
// just before the check resolves the key — the one fixture Record.Validate
// refuses, and the reason the check resolves rather than refuses it. The
// subtests that need a Validate-clean project record give it a UUID.
func keyOnlyProjectRecord() Record {
	return Record{Name: "bitbucket-token--proj", Type: TypeProject, Coverage: []string{"bitbucket/acme/*"}, ProjectKey: "FUG", Expires: aDayComesLater}
}

// checksValid asserts that a fixture the check accepted is a record the contract
// accepts too. Without it the two drift silently: a verifier reading a different
// coverage spelling than Validate holds a record to refuses every real record
// while its own tests stay green.
func checksValid(t *testing.T, rec Record) {
	t.Helper()
	if err := rec.Validate(); err != nil {
		t.Errorf("the check accepted the record %s, and the contract refuses it: %v", rec.Name, err)
	}
}

// TestVerifyFixturesAreRecordsTheContractAccepts pins the drift this check was
// found in. The verifier reads coverage in whatever spelling the record has, and
// the contract holds a record to exactly one; a fixture in the other spelling
// lets the check and the contract disagree while every test stays green, which
// is how this shipped refusing every record `creds set` would hand it. Each
// fixture is asserted here, and the key-only project record — the one record the
// contract refuses, and the resolution the check exists to perform — is asserted
// to still be that exception rather than one the contract has grown to accept.
func TestVerifyFixturesAreRecordsTheContractAccepts(t *testing.T) {
	for name, rec := range map[string]Record{
		"the repository record": repoRecord(),
		"the workspace record":  workspaceRecord(),
		"the project record":    keyOnlyProjectRecord(),
	} {
		err := rec.Validate()
		if name == "the project record" {
			if err == nil {
				t.Errorf("%s names a key and no UUID, and the contract is expected to refuse it: the check resolves the key, and this fixture covers that", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s is not a record the contract accepts, so the check and the contract have drifted: %v", name, err)
		}
	}
	withUUID := keyOnlyProjectRecord()
	withUUID.ProjectKey, withUUID.ProjectUUID = "", "{proj-1}"
	if err := withUUID.Validate(); err != nil {
		t.Errorf("the project record naming its UUID is not one the contract accepts: %v", err)
	}
}

func TestVerifyRepoWorkspaceAndProjectCoverage(t *testing.T) {
	t.Run("a repository token", func(t *testing.T) {
		rec := repoRecord()
		f := newFakeBitbucket(t, repoAnswers())
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		checksValid(t, rec)
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
		rec.Coverage = []string{"bitbucket/acme/one", "bitbucket/acme/two"}
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		checksValid(t, rec)
		if want := []string{"repository:read", "pullrequest:read"}; !slices.Equal(res.ReadScopes, want) {
			t.Errorf("read scopes = %v, want each proven once, %v", res.ReadScopes, want)
		}
	})

	t.Run("a workspace token", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 200, body: `{"values":[{"full_name":"acme/one"}]}`},
			"GET /2.0/user":              {status: 200, body: `{"uuid":"{bbbb-3333}","kind":"workspace_access_token"}`},
		})
		rec := workspaceRecord()
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		checksValid(t, rec)
		if res.Identity != "{bbbb-3333}" {
			t.Errorf("identity = %q, want the account UUID Bitbucket answered", res.Identity)
		}
		if want := []string{"repository:read"}; !slices.Equal(res.ReadScopes, want) {
			t.Errorf("read scopes = %v, want %v", res.ReadScopes, want)
		}
	})

	t.Run("a workspace token covering two workspaces", func(t *testing.T) {
		f := newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme":  {status: 200, body: `{"values":[{"full_name":"acme/one"}]}`},
			"GET /2.0/repositories/other": {status: 200, body: `{"values":[{"full_name":"other/one"}]}`},
			"GET /2.0/user":               {status: 200, body: `{"uuid":"{bbbb-3333}","kind":"workspace_access_token"}`},
		})
		rec := workspaceRecord()
		rec.Coverage = []string{"bitbucket/acme/*", "bitbucket/other/*"}
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		checksValid(t, rec)
		if got := len(f.requests()); got != 3 {
			t.Errorf("the check made %d calls (%v), want both listings and the identity", got, f.paths())
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
		// This is the one fixture the contract refuses, and deliberately so: it is
		// the record `creds set` holds before the check resolves the key, and
		// resolving it rather than refusing it is the whole of what this subtest
		// proves. Asserted rather than assumed, so the fixture cannot drift into
		// one Validate accepts and quietly stop covering the resolution.
		if err := keyOnlyProjectRecord().Validate(); err == nil {
			t.Error("the key-only project fixture is a record the contract accepts, so this subtest no longer covers the resolution")
		}
		res := f.verifier().Verify(context.Background(), testToken, keyOnlyProjectRecord())
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
		rec := keyOnlyProjectRecord()
		rec.ProjectKey, rec.ProjectUUID = "", "{proj-1}"
		res := f.verifier().Verify(context.Background(), testToken, rec)
		if !res.OK {
			t.Fatalf("OK = false, message %q", res.Message)
		}
		checksValid(t, rec)
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
		rec.Coverage = []string{"bitbucket/acme/one", "bitbucket/acme/two"}
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
		}), keyOnlyProjectRecord(), testToken)
		if want := "the token does not cover project FUG in workspace acme"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("a listing that answers another project", func(t *testing.T) {
		res := refused(t, newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 200, body: `{"values":[{"project":{"uuid":"{other-project}"}}]}`},
		}), func() Record {
			rec := keyOnlyProjectRecord()
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

	// An answer that is neither a read's nor a scope's is the fail-closed bucket
	// (prove's default): a 500, a 502, a body that is not the document the call
	// asked for. Each is a call that has shown nothing about the token, so each
	// must refuse rather than fall through to the OK below it.
	t.Run("an answer this check cannot read", func(t *testing.T) {
		for name, status := range map[string]int{"a 500": 500, "a 502": 502} {
			t.Run(name, func(t *testing.T) {
				answers := repoAnswers()
				answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{status: status, body: `{"error":{"message":"Server error"}}`}
				res := refused(t, newFakeBitbucket(t, answers), repoRecord(), testToken)
				if res.Message != msgRefused {
					t.Errorf("message = %q, want %q: an answer that is not a read's has shown nothing", res.Message, msgRefused)
				}
			})
		}
	})

	// A 404 inside a repository the token is already known to cover is the read
	// itself missing and not a reach the record over-claims, which is the one
	// difference `uncovered == ""` carries.
	t.Run("a pull-request read that is not there", func(t *testing.T) {
		answers := repoAnswers()
		answers["GET /2.0/repositories/acme/fugaro/pullrequests"] = fakeAnswer{status: 404, body: `{"error":{"message":"Not found"}}`}
		res := refused(t, newFakeBitbucket(t, answers), repoRecord(), testToken)
		if res.Message != msgMissingPullRequestRead {
			t.Errorf("message = %q, want %q", res.Message, msgMissingPullRequestRead)
		}
	})

	t.Run("a project listing that is not a listing", func(t *testing.T) {
		res := refused(t, newFakeBitbucket(t, map[string]fakeAnswer{
			"GET /2.0/repositories/acme": {status: 200, body: `not a listing`},
		}), func() Record {
			rec := keyOnlyProjectRecord()
			rec.ProjectKey, rec.ProjectUUID = "", "{proj-1}"
			return rec
		}(), testToken)
		if res.Message != msgRefused {
			t.Errorf("message = %q, want %q: a listing that is not one has shown nothing", res.Message, msgRefused)
		}
	})

	// Every coverage entry is read or the record is refused: a pattern this check
	// cannot hold a single read to is refused whole rather than skipped, so no
	// entry of a record is quietly unchecked.
	t.Run("a coverage entry that is no pattern", func(t *testing.T) {
		cases := map[string][]string{
			"a wildcard workspace":          {"bitbucket/*/fugaro"},
			"another provider":              {"github/acme/fugaro"},
			"no provider at all":            {"acme/fugaro"},
			"a repository of no workspace":  {"bitbucket//fugaro"},
			"a workspace with no repo":      {"bitbucket/acme/"},
			"a workspace alone":             {"bitbucket/acme"},
			"a good entry beside a bad one": {"bitbucket/acme/one", "acme/two"},
		}
		for name, coverage := range cases {
			t.Run(name, func(t *testing.T) {
				f := newFakeBitbucket(t, nil)
				rec := repoRecord()
				rec.Coverage = coverage
				res := refused(t, f, rec, testToken)
				if res.Message != msgRecord {
					t.Errorf("message = %q, want %q", res.Message, msgRecord)
				}
				if got := f.requests(); len(got) != 0 {
					t.Errorf("a record this check cannot read was checked with %v, want no call", got)
				}
			})
		}
	})

	t.Run("a workspace record covering every workspace", func(t *testing.T) {
		rec := workspaceRecord()
		rec.Coverage = []string{"bitbucket/*/*"}
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	// The other half of a two-segment entry: a workspace record's, which the
	// whole form refuses for the same reason a repository record's is.
	t.Run("a workspace record covering a repository", func(t *testing.T) {
		rec := workspaceRecord()
		rec.Coverage = []string{"acme/fugaro"}
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
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
		rec.Coverage = []string{"bitbucket/acme/*"}
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
		rec := keyOnlyProjectRecord()
		rec.ProjectKey = ""
		res := refused(t, newFakeBitbucket(t, nil), rec, testToken)
		if res.Message != msgRecord {
			t.Errorf("message = %q, want %q", res.Message, msgRecord)
		}
	})

	t.Run("a project record covering two workspaces", func(t *testing.T) {
		rec := keyOnlyProjectRecord()
		rec.Coverage = []string{"bitbucket/acme/*", "bitbucket/other/*"}
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

	t.Run("a client with no timeout of its own is given one", func(t *testing.T) {
		own := &http.Client{}
		v := &Verifier{Client: own}
		if got := v.client().Timeout; got != httpjson.DefaultTimeout {
			t.Errorf("the check's client timeout = %v, want %v: an answer that never comes must not hold creds set open", got, httpjson.DefaultTimeout)
		}
		if own.Timeout != 0 {
			t.Errorf("the check changed the caller's client: timeout = %v", own.Timeout)
		}
		if got := (&Verifier{}).client().Timeout; got != httpjson.DefaultTimeout {
			t.Errorf("the client of a verifier with none = %v, want %v", got, httpjson.DefaultTimeout)
		}
		if got, want := (&Verifier{Client: &http.Client{Timeout: time.Minute}}).client().Timeout, time.Minute; got != want {
			t.Errorf("the check replaced a caller's own timeout %v with %v", want, got)
		}
	})

	t.Run("an answer that never comes is refused, not waited for", func(t *testing.T) {
		v := &Verifier{Client: &http.Client{
			Timeout: 50 * time.Millisecond,
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}),
		}}
		res := v.Verify(context.Background(), testToken, repoRecord())
		if res.OK {
			t.Fatal("OK = true, want a refusal")
		}
		if res.Message != msgRefused {
			t.Errorf("message = %q, want %q: a call that was never answered has shown nothing", res.Message, msgRefused)
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

// roundTripFunc is the transport one test writes inline to hold a call open,
// and the whole of what that test needs: it answers nothing and reaches no
// host.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

// TestVerifyDropsAHeaderWordThatIsNotAScope pins the one guard between an
// answer's header and the operator's screen. What is recorded is put in front
// of the operator by `creds set`, and a header is bitbucket.org's to fill with
// anything — including, if it ever echoed one, the credential the check just
// presented. The scope-word shape is what keeps a value from being recorded as
// a scope, so a header carrying the token reads back as no scope at all.
func TestVerifyDropsAHeaderWordThatIsNotAScope(t *testing.T) {
	answers := repoAnswers()
	answers["GET /2.0/repositories/acme/fugaro"] = fakeAnswer{
		status: 200,
		body:   `{"full_name":"acme/fugaro"}`,
		header: map[string]string{"X-OAuth-Scopes": "repository:read " + testToken + ", Bearer=" + testToken},
	}
	res := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
	if !res.OK {
		t.Fatalf("OK = false, message %q", res.Message)
	}
	if want := []string{"repository:read"}; !slices.Equal(res.Scopes, want) {
		t.Errorf("recorded scopes = %v, want only the scope word the header carried", res.Scopes)
	}
	if dump := fmt.Sprintf("%+v", res); strings.Contains(dump, testToken) {
		t.Errorf("the result carries the token: %s", dump)
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

	// The account endpoint's own fail-closed half: an answer that is not the
	// account document is refused rather than read as an identity with the UUID
	// left empty, which would enter an empty identity and read as "no identity
	// known" rather than as a token that could not be read.
	t.Run("an account answer that is not one", func(t *testing.T) {
		answers := repoAnswers()
		answers["GET /2.0/user"] = fakeAnswer{status: 200, body: `not the account`}
		res := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
		if res.OK {
			t.Fatal("OK = true, want a refusal")
		}
		if want := msgNotAccessToken(TypeRepo); res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
		if res.Identity != "" {
			t.Errorf("identity = %q, want none", res.Identity)
		}
	})

	t.Run("an account endpoint that does not answer the account", func(t *testing.T) {
		for name, status := range map[string]int{"a 401": 401, "a 500": 500} {
			t.Run(name, func(t *testing.T) {
				answers := repoAnswers()
				answers["GET /2.0/user"] = fakeAnswer{status: status, body: `{"error":{"message":"no"}}`}
				res := newFakeBitbucket(t, answers).verifier().Verify(context.Background(), testToken, repoRecord())
				if res.OK {
					t.Fatal("OK = true, want a refusal")
				}
				if res.Message != msgRefused {
					t.Errorf("message = %q, want %q", res.Message, msgRefused)
				}
				if res.Identity != "" {
					t.Errorf("identity = %q, want none", res.Identity)
				}
			})
		}
	})
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
