package cli

// gitcred_build_test.go holds the entry mode's client on its own (gitcred_build.go):
// the address the build's own identity token may be sent to, the two refusals
// that come before anything is asked for, the redirect it does not follow and
// the answer it reads strictly. What the call does end to end — the definition a
// two-repository entry composes and the credential the real service writes — is
// catalog_bake_e2e_test.go's; what is here is each refusal on the way, because
// these are the ones a mutation can remove without a happy path noticing.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// TestTheBuildCallRefusesAURLItWillNotSendATokenTo: the call carries the
// project's build identity token, which is a bearer credential, so the address
// it is sent to is judged before anything is minted: https, or http on a
// loopback address, which is a test's own service (the address a deployment
// hands the step is Cloud Run's own URL). A URL that is more than a scheme and a
// host is refused too — the route is appended to it, and a base path, a query or
// a fragment would decide where this token goes — and what the step then posts
// to is the scheme and host alone.
//
// The rule is checkIntakeServiceURL's, and this is its table for the build's
// address.
//
// Mutation (verified, run and restored): `return raw, nil` as the first
// statement of checkBuildServiceURL and this fails on every plain-http case and
// on every URL that carries more than a scheme and a host.
func TestTheBuildCallRefusesAURLItWillNotSendATokenTo(t *testing.T) {
	for _, c := range []struct {
		url string
		// posted is what the step then sends its request to, when the address is
		// admitted. Every refused row posts nowhere.
		posted string
		want   bool
	}{
		{"https://credentials.example.com", "https://credentials.example.com", true},
		{"https://credentials.example.com/", "https://credentials.example.com", true},
		{"https://credentials.example.com:8443", "https://credentials.example.com:8443", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://localhost:8080", "http://localhost:8080", true},
		{"http://[::1]:8080", "http://[::1]:8080", true},
		{"http://credentials.example.com", "", false},
		{"http://credentials.internal", "", false},
		{"http://10.0.0.1", "", false},
		{"ftp://credentials.example.com", "", false},
		{"https://credentials.example.com/v1", "", false},
		{"https://credentials.example.com?x=1", "", false},
		{"https://credentials.example.com#f", "", false},
		{"https://user:pass@credentials.example.com", "", false},
		{"credentials.example.com", "", false},
		{"", "", false},
	} {
		posted, err := checkBuildServiceURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("checkBuildServiceURL(%q) refused = %v, want %v: this address is where the build's own identity token would be sent", c.url, err != nil, !c.want)
		}
		if err != nil && strings.Contains(err.Error(), c.url) && c.url != "" {
			t.Errorf("the refusal of %q repeats the address: %v", c.url, err)
		}
		if posted != c.posted {
			t.Errorf("checkBuildServiceURL(%q) posts to %q, want %q", c.url, posted, c.posted)
		}
	}
}

// TestTheBuildCallAsksTheServiceTheInstallationDeployed: the seam reads the
// service's address out of the environment the platform set on the build
// (envCredentialsServiceURL, the service's own URL and the audience its identity
// token is minted for), refuses to build a client over anything it will not send
// that token to, and hands back the client that posts to the address it
// admitted. A build with no address at all is refused by name rather than
// reached for: an installation whose service is not deployed has no entry
// credential to ask for.
//
// Mutation (verified, run and restored): drop the `raw == ""` branch of
// newBuildTokenClient and this fails on the missing-variable case.
func TestTheBuildCallAsksTheServiceTheInstallationDeployed(t *testing.T) {
	t.Setenv(envCredentialsServiceURL, "")
	// The refusal is the missing-variable one and not the shape rule's: the empty
	// value must not be read as a malformed address, because what fixes it is
	// setting the variable the installation's service is deployed at and not
	// correcting a value.
	if _, err := newBuildTokenClient(nil); err == nil || !strings.Contains(err.Error(), envCredentialsServiceURL) || !strings.Contains(err.Error(), "is not set") {
		t.Errorf("with no %s the seam answered %v, want a refusal saying the variable is not set", envCredentialsServiceURL, err)
	}
	for _, bad := range []string{"http://credentials.example.com", "https://credentials.example.com/v1", "credentials.example.com"} {
		t.Setenv(envCredentialsServiceURL, bad)
		if _, err := newBuildTokenClient(nil); err == nil {
			t.Errorf("the seam built a client over %q, an address this step will not send the build's identity token to", bad)
		}
	}
	const service = "https://credentials.example.com"
	t.Setenv(envCredentialsServiceURL, service+"/")
	client, err := newBuildTokenClient(nil)
	if err != nil {
		t.Fatalf("the seam refused the address the installation's service is deployed at: %v", err)
	}
	built, ok := client.(buildTokenClient)
	if !ok || built.url != service || built.http == nil {
		t.Errorf("the seam handed back %#v, want the client posting to %q with a client of its own", client, service)
	}
}

// TestTheBuildCallDoesNotFollowARedirect: the request carries the build's own
// identity token in its Authorization header, so a followed redirect would hand
// that token to whatever host the Location names — 301 and 302 turn the POST
// into a GET there, and 307 and 308 send it whole. A client that wants the token
// to reach only the service it judged sends it nowhere else under any of them,
// and what it read as a redirect is a refusal rather than an answer.
//
// The identity is minted per call and never cached, which is the other half of
// what keeps this token to the service it was minted for: a cached one is a
// token this step keeps presenting after the platform stopped issuing it.
//
// Mutation (verified, run and restored): drop CheckRedirect from
// buildTokenHTTPClient and this fails — the token reaches the host the Location
// named.
func TestTheBuildCallDoesNotFollowARedirect(t *testing.T) {
	for _, code := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		reached := 0
		elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			reached++
		}))
		t.Cleanup(elsewhere.Close)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+credsvc.BuildTokenPath, code)
		}))
		t.Cleanup(srv.Close)
		t.Setenv(envCredentialsServiceURL, srv.URL)
		answers := buildCallMetadata(t)
		t.Setenv(gitCredAppIDEnv, catalogBuildAppID)
		client, err := newBuildTokenClient(nil)
		if err != nil {
			t.Fatal(err)
		}
		mints := answers.count()
		if _, err := client.Mint(t.Context(), buildCallRequest(gitprov.KindGitHub)); err == nil {
			t.Errorf("a %d was answered a credential, want a refusal: this call's header is the build's own identity token", code)
		}
		if reached != 0 {
			t.Errorf("a %d reached the host the Location named (%d requests), want none: the token in the header is the build's", code, reached)
		}
		if got := answers.count() - mints; got != 1 {
			t.Errorf("the metadata server was read %d times for one call, want the one mint a call makes", got)
		}
	}
}

// TestTheBuildCallRefusesToAskWithoutAnAppID: the GitHub installation token is
// minted as the App, and the App's id is the one thing this step holds and the
// service's bundle does not. A GitHub call without it is refused here — before
// the build's identity is minted or a request is sent — rather than asked for
// and refused there, because what a call without it would be answered is not
// this contract's answer. A Bitbucket build signs nothing and holds no id, so
// the request it makes is the same one with the field empty.
//
// Mutation (verified, run and restored): drop the `appID == ""` branch of Mint
// and this fails: the call reaches the service with no App id.
func TestTheBuildCallRefusesToAskWithoutAnAppID(t *testing.T) {
	reached := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached++
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envCredentialsServiceURL, srv.URL)
	t.Setenv(gitCredAppIDEnv, "")
	answers := buildCallMetadata(t)
	client, err := newBuildTokenClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Mint(t.Context(), buildCallRequest(gitprov.KindGitHub)); err == nil || !strings.Contains(err.Error(), gitCredAppIDEnv) {
		t.Errorf("a GitHub call with no %s answered %v, want a refusal naming the variable", gitCredAppIDEnv, err)
	}
	if reached != 0 || answers.count() != 0 {
		t.Errorf("the refused call reached the service %d times and the metadata server %d times, want none: nothing is asked for before the request is this step's to make", reached, answers.count())
	}
	// The Bitbucket half is the same step with no App id to send, and this server
	// holds no answer of this contract's — so the call reaches it and fails on
	// reading what came back, which is what says the refusal above was the App id
	// and not the provider.
	if _, err := client.Mint(t.Context(), buildCallRequest(gitprov.KindBitbucket)); err == nil {
		t.Error("a server that answers an empty body was read as answering a credential")
	}
	if reached != 1 {
		t.Errorf("the Bitbucket call reached the service %d times, want the one request it is", reached)
	}
}

// TestTheBuildCallRefusesAnAnswerTheContractDoesNotHave: the answer is exactly
// one JSON object of the contract's two shapes, and nothing else is read: a
// field this release does not know, a second document after the first, a body
// that is not a document at all are all refused rather than parsed generously,
// because a later service's answer read by this release is a credential read out
// of a document this release does not understand. No refusal here repeats what
// it read — what it read holds the token — which is why every refusal is one of
// this file's own sentences.
//
// Mutation (verified, run and restored): remove DisallowUnknownFields from
// decodeBuildTokenAnswer and the answer carrying a field this release does not
// know is read instead of refused.
func TestTheBuildCallRefusesAnAnswerTheContractDoesNotHave(t *testing.T) {
	const (
		// The value a body this test hands the decoder carries, so that a refusal
		// which repeated what it read is caught rather than assumed.
		tokenValue  = "answer-token-not-a-real-credential"
		unknownVal  = "marker-not-a-real-credential"
		tokenAnswer = `{"token":{"token":"` + tokenValue + `","user":"x-access-token","host":"github.com","expires_at":"2099-01-01T00:00:00Z"}}`
	)
	for _, c := range []struct {
		name  string
		body  string
		check func(t *testing.T, got buildtoken.Response)
	}{
		{
			name: "the token answer",
			body: tokenAnswer,
			check: func(t *testing.T, got buildtoken.Response) {
				want := buildtoken.Token{
					Value: tokenValue, User: github.GitUsername, Host: "github.com",
					ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
				}
				if got.Token == nil || *got.Token != want || got.Proxy != nil {
					t.Errorf("the token answer decoded as %+v / %+v, want the token and no grant", got.Token, got.Proxy)
				}
			},
		},
		{
			name: "the proxy grant answer",
			body: `{"proxy":{"url":"https://creds.example/git/","repositories":["acme/app"],"expires_at":"2099-01-01T00:00:00Z"}}`,
			check: func(t *testing.T, got buildtoken.Response) {
				want := buildtoken.ProxyGrant{
					URL: "https://creds.example/git/", Repositories: []string{"acme/app"},
					ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
				}
				if got.Proxy == nil || got.Proxy.URL != want.URL || !got.Proxy.ExpiresAt.Equal(want.ExpiresAt) || len(got.Proxy.Repositories) != 1 || got.Proxy.Repositories[0] != "acme/app" || got.Token != nil {
					t.Errorf("the grant answer decoded as %+v / %+v, want the grant and no token", got.Proxy, got.Token)
				}
			},
		},
		{name: "a field this release does not know", body: `{"token":{"token":"` + tokenValue + `","user":"x-access-token","host":"github.com","expires_at":"2099-01-01T00:00:00Z"},"unknown":"` + unknownVal + `"}`},
		{name: "a field this release does not know beside a grant", body: `{"proxy":{"url":"https://creds.example/git/","repositories":[],"expires_at":"2099-01-01T00:00:00Z"},"unknown":1}`},
		{name: "a second document after the first", body: tokenAnswer + `{"token":{}}`},
		{name: "a body that is not this contract's", body: "<!doctype html><p>a redirect or a proxy's answer</p>"},
		{name: "no body at all", body: ""},
		{name: "an answer that is not an object", body: `[1,2]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got buildtoken.Response
			err := decodeBuildTokenAnswer([]byte(c.body), &got)
			if c.check == nil {
				if err == nil {
					t.Fatalf("the answer decoded as %+v, want a refusal: this release reads the contract's own two shapes and nothing else", got)
				}
			} else if err != nil {
				t.Fatalf("the answer was refused: %v", err)
			}
			if err != nil {
				for _, read := range []string{tokenValue, unknownVal} {
					if strings.Contains(err.Error(), read) {
						t.Errorf("the refusal repeats what it read: %v", err)
					}
				}
				return
			}
			c.check(t, got)
		})
	}
}

// buildCallMetadata stands in for the platform's metadata server: the only place
// the build's identity token is minted, the endpoint GCE_METADATA_HOST points
// the step at, and what the step has to present the header the server requires
// of a caller to be read. What it counts is how often it was asked, which is how
// a test says a call minted its identity once and a refused call minted none.
type buildCallMetadataStub struct {
	mu    sync.Mutex
	reads int
}

func buildCallMetadata(t *testing.T) *buildCallMetadataStub {
	t.Helper()
	s := &buildCallMetadataStub{}
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" || r.URL.Path != buildMetadataIdentityPath {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s.mu.Lock()
		s.reads++
		s.mu.Unlock()
		_, _ = io.WriteString(w, buildE2EIdentityToken)
	}))
	t.Cleanup(meta.Close)
	t.Setenv(buildMetadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
	return s
}

func (s *buildCallMetadataStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// buildCallRequest is one build's request as the step makes it: the entry the
// build composes a definition for, the build's own id, the provider, the
// repositories the bake plan ordered and how long the step needs them.
func buildCallRequest(provider string) buildtoken.Request {
	return buildtoken.Request{
		Entry: "tools", BuildID: buildE2EBuildID, Provider: provider,
		Repositories: []string{"acme/app", "acme/lib"}, TTLSeconds: 900,
	}
}
