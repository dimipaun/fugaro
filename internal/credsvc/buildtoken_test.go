package credsvc

// The build route's tests (B1, D2301, R6): the ONE route of this service whose
// caller is a project's build identity and whose answer is decided by the
// provider the call names — one read-only installation token over the entry's
// requested repositories for a GitHub build, and a proxy grant and no token at
// all for a Bitbucket one (D2302).
//
// What is held here is what the contract makes the service's rather than the
// build's (docs/shared-jobs-default/contract-build-token.md): that nothing a
// build is answered can write (rule 1); that the token covers exactly the
// repositories the call named, out of the set the published image map puts on
// the entry it names (rule 8, D2301); that its life is bounded, by GitHub's own
// instant for a token and by the request's need capped for a grant (rule 7);
// that the mint is audited (rule 8); that a Bitbucket build is answered a grant
// and no token (D2302, R6); and that the build route is the build's alone while
// the build reaches no other route (rule 9).
//
// Everything here is hermetic (Convention 9): the GitHub API is the fake on
// loopback reached through the transport that refuses every other destination,
// the App key and the tokens are built at run time (Convention 2: no
// secret-shaped literal in a test file), and the map the narrowing reads is a
// document projectpolicy.Decode accepts.

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// buildPath is the build route's path, spelled as B1 names it. It is a literal
// on purpose: every call below is made to THIS path, so a rename of the
// service's own constant cannot move the route without failing every test here
// (bitbucket_test.go's readTokenPath is held the same way).
const buildPath = "/v1/build/token"

// testBuildSA is the project's one build identity (B5): the service account a
// project's catalog builds run as. It is a bare address, the shape every class
// list of this service holds, and it is on no other list — which is what
// TestEveryOtherClassRefusedOnTheBuildRoute and
// TestBuildIdentityRefusedOnEveryOtherRoute each hold from their own side.
const testBuildSA = "fugaro-build@aurora-dev.iam.gserviceaccount.com"

// The catalog entries this file's calls name, and the build id they carry: the
// entries are catalog names (config.ValidCatalogName) and the id is the shape
// internal/backend/gcp's buildIDRE holds, which is the shape the request's
// reader refuses anything else as.
const (
	buildEntry      = "web"
	buildStoreEntry = "store"
	buildBuildID    = "b1c2d3e4-0000-4000-8000-000000000001"
)

// The repositories the fixture's image map names beside the two the rest of
// this package's fixtures use. They are spelled here rather than derived
// because each is a REPOSITORY NAME and not a derivation: what the map maps is
// a name, and a test that derived one would be testing the derivation.
const (
	// buildElsewhereRepo is a repository the map puts on another entry, so it
	// is a repository this project HAS and this call may not cover.
	buildElsewhereRepo = "acme/elsewhere"
	// buildUnlistedRepo is a repository the map names nowhere, which is the
	// other half of "outside the entry's": a repository the project does not
	// hold at all.
	buildUnlistedRepo = "acme/nothing"
	// buildSecondOwnerRepo and buildSecondOwnerOtherRepo are the two
	// repositories of one entry that are held at two GitHub owners. One App
	// installation belongs to one owner, so no single token covers them and the
	// service refuses the entry rather than minting half of what was asked.
	buildSecondOwnerRepo      = "kepler/nowhere"
	buildSecondOwnerOtherRepo = "acme/nowhere"
)

// buildRequestBody is a build call's body, in internal/buildtoken's own field
// names written down here rather than taken from the service's struct: what the
// two sides share is the contract's spelling, and a test that decoded into the
// service's type would pass while the wire named a field something else. The
// App ID is the one field the contract does not carry (it is B1's own read of
// the mint's, docs/shared-jobs-default/contract-build-token.md, "What this
// contract does not decide").
type buildRequestBody struct {
	Entry        string   `json:"entry"`
	BuildID      string   `json:"build_id"`
	Provider     string   `json:"provider"`
	Repositories []string `json:"repositories"`
	TTLSeconds   int      `json:"ttl_seconds"`
	AppID        string   `json:"app_id,omitempty"`
}

func buildBody(t *testing.T, req buildRequestBody) string {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("building the call's body: %v", err)
	}
	return string(raw)
}

// buildPolicyDoc is the project's published policy with the image map the
// narrowing reads (D1906). Four entries of it are load-bearing:
//
//   - "web" is put on TWO repositories of one owner, which is the union one
//     build's credential covers (D2301) and the reason the token is a
//     multi-repository mint;
//   - "store" is put on a Bitbucket repository, which is the other answer's
//     entry;
//   - the repository of another entry is what a call may name and not be
//     given;
//   - "two-owners" is put on two GitHub repositories of two owners, which one
//     installation token cannot cover.
//
// The document is decoded rather than built by hand (decodeTestPolicy): Decode
// is the reader the service deploys, and a hand-built policy is one no operator
// could publish. Its keys are the canonical names a --repo reads as and not the
// escaped form the node holds — UnmarshalJSON reads a key with no escape in it
// as it is, which is what makes the document below a document an operator could
// have written.
func buildPolicyDoc() string {
	return `{
		"version": 5,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "auto"},
		"image_map": {
			"` + testRepoPath + `": "` + buildEntry + `",
			"` + testOtherRepoPath + `": "` + buildEntry + `",
			"` + testBitbucketRepo + `": "` + buildStoreEntry + `",
			"` + buildElsewhereRepo + `": "other",
			"` + buildSecondOwnerRepo + `": "two-owners",
			"` + buildSecondOwnerOtherRepo + `": "two-owners"
		}
	}`
}

// buildSvc is the whole fixture for a build call: an installation whose build
// list holds the project's one build identity, whose repository table holds
// every repository the fixture's image map names, whose published policy
// carries that map, whose bundle holds the App key a GitHub answer is minted
// with, and the fake GitHub behind the service's client.
//
// Every other class is configured too, with the lists classMatrixSvc uses: a
// refusal is only this route's own if the caller COULD have been admitted
// somewhere, and a class missing from the configuration is refused for being
// unconfigured rather than for being another class.
func buildSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *ghFake) {
	t.Helper()
	gh := newGHFake(t)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:            testProject,
			FirebaseProject:    testFirebase,
			Audience:           testAudience,
			JobAccounts:        []string{testJobSA},
			Keys:               s.keys,
			JobKeys:            s.jobKeys,
			Outcomes:           RTDBOutcomes{DB: s.db},
			SharedJobAccounts:  []SharedJobAccount{sharedRow()},
			ReviewJobAccounts:  []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			VerifierAccounts:   []string{testVerifierSA},
			PersonAccounts:     []string{testPersonAccount},
			CoordinatorAccount: testCoordinatorAccount,
			LauncherAccounts:   []string{testLauncherAccount},
			BuildAccounts:      []string{testBuildSA},
			Now:                func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the build class cannot be configured beside the other classes: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.Repos = map[string]string{
			githubSlug(t, testRepoPath):              testRepo,
			githubSlug(t, testOtherRepoPath):         testOtherRepo,
			githubSlug(t, buildElsewhereRepo):        "github/" + buildElsewhereRepo,
			githubSlug(t, buildSecondOwnerRepo):      "github/" + buildSecondOwnerRepo,
			githubSlug(t, buildSecondOwnerOtherRepo): "github/" + buildSecondOwnerOtherRepo,
			bitbucketSlug(t, testBitbucketRepo):      testCanonical(t, testBitbucketRepo),
		}
		// The service's own URL: what a Bitbucket grant's proxy URL is built
		// from, and never the request's Host header. It is the audience the
		// fixture's tokens carry, because a deployment has one spelling of its
		// own address and the two readings are the same one.
		cfg.BaseURL = testAudience
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		s.src.docs = [][]byte{mintBundleDoc(t)}
		s.policy.shared = decodeTestPolicy(t, buildPolicyDoc())
		if mutate != nil {
			mutate(s, cfg)
		}
	})
	return s, gh
}

// buildToken is the Google ID token the project's build identity presents: the
// job token's own shape — Cloud Run IAM admits a caller of any class the same
// way — naming the account on the build list and this service's own audience.
func (s *svc) buildToken(mutate ...func(map[string]any)) string {
	return s.jobToken(append([]func(map[string]any){
		func(c map[string]any) { c["email"] = testBuildSA },
	}, mutate...)...)
}

// buildCall sends one build call as the project's build identity: the token in
// Authorization and nothing else. It is the shape this route has and no other
// route of this service does — a build holds no run identity, so there is none
// to present.
func (s *svc) buildCall(t *testing.T, body, tok string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callPerson(t, buildPath, body, tok, mutate...)
}

// buildAnswer reads an answer as the contract's own shape: the two sides share
// buildtoken.Response, so a test that spelled the two answers out again would
// be a second contract that could drift from the one the build compiles
// against.
func buildAnswer(t *testing.T, body []byte) buildtoken.Response {
	t.Helper()
	var ans buildtoken.Response
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not one this contract can read: %v (%s)", err, body)
	}
	return ans
}

// buildMintBody is the body the service sent GitHub for a multi-repository
// mint: what the token is scoped to and what it may do.
type buildMintBody struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

func buildMintSent(t *testing.T, body string) buildMintBody {
	t.Helper()
	var sent buildMintBody
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("the mint's body is not the object this contract writes: %v (%q)", err, body)
	}
	return sent
}

// ---------------------------------------------------------------------------
// Read-only
// ---------------------------------------------------------------------------

// TestBuildTokenIsReadOnly is B1's mutation check: the token a build is
// answered is minted with the read-only set and with nothing else.
//
// The set is asserted against the mint body GITHUB was actually sent and not
// against the constant the service passed in, and it is written out here rather
// than read off github.BuildTokenPermissions: a permission added to that
// constant would otherwise move both sides of the comparison together. A read
// level other than "read", or a scope beside the two, fails this test — which
// is what the plan's mutation (one write scope, e.g. "contents": "write") is
// for.
//
// The request's own half of rule 1 is held here too: the contract's request has
// no field for a permission, so a body that tries to name one is refused rather
// than read with the field dropped (decodeStrict).
func TestBuildTokenIsReadOnly(t *testing.T) {
	s, gh := buildSvc(t, nil)

	resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
		Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
		Repositories: []string{testRepoPath}, TTLSeconds: 600, AppID: testAppID,
	}), s.buildToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
	}
	_, mint := gh.both(t)
	sent := buildMintSent(t, mint.body)
	want := map[string]string{"contents": "read", "metadata": "read"}
	if !maps.Equal(sent.Permissions, want) {
		t.Errorf("the build's token was minted with %v, want %v: nothing a build is answered can write", sent.Permissions, want)
	}
	for scope, level := range sent.Permissions {
		if level != "read" {
			t.Errorf("the mint asks GitHub for %s: %s, and a build's credential reads", scope, level)
		}
	}

	// A permission the contract has no field for: the request is refused, and
	// it is refused as a request — nothing has been read from the bundle yet,
	// and no token is minted.
	write := `{"entry":"` + buildEntry + `","build_id":"` + buildBuildID + `","provider":"` + gitprov.KindGitHub +
		`","repositories":["` + testRepoPath + `"],"ttl_seconds":600,"app_id":"` + testAppID + `",` +
		`"permissions":{"contents":"write"}}`
	resp, body = s.buildCall(t, write, s.buildToken())
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("a build asking for a write scope was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
	}
	if n := gh.count(); n != 2 {
		t.Errorf("the fake GitHub saw %d requests, want the one admitted call's two and no mint for the refused one", n)
	}
}

// ---------------------------------------------------------------------------
// The narrowing
// ---------------------------------------------------------------------------

// TestBuildTokenCoversOnlyTheRequestedRepos is rule 8's other half (spec
// decision 4): the token's reach is the repositories the call named and no
// others, and those are held to the set the published image map puts on the
// entry the call names.
//
// Three facts, each of which a widening would break:
//
//   - a call naming ONE of the entry's two repositories is minted a token over
//     that one alone — the narrowing is the request's and not the entry's;
//   - a call naming both is one token over both, which is what D2301 exists for;
//   - a repository outside the entry's set is refused BY NAME, whether the
//     project holds it on another entry or nowhere at all.
func TestBuildTokenCoversOnlyTheRequestedRepos(t *testing.T) {
	owner, name, _ := strings.Cut(testRepoPath, "/")
	otherName := strings.TrimPrefix(testOtherRepoPath, owner+"/")

	t.Run("one of the entry's repositories", func(t *testing.T) {
		s, gh := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: []string{testRepoPath}, TTLSeconds: 600, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
		}
		lookup, mint := gh.both(t)
		// The installation is looked up through the repository the call named,
		// which is what makes the token the CALLER's own project's installation
		// and not another owner's.
		if want := "/repos/" + testRepoPath + "/installation"; lookup.path != want {
			t.Errorf("the installation lookup was %s, want %s", lookup.path, want)
		}
		sent := buildMintSent(t, mint.body)
		if want := []string{name}; !slices.Equal(sent.Repositories, want) {
			t.Errorf("the token covers %v, want %v: the credential covers the request's repositories and no others", sent.Repositories, want)
		}
	})

	t.Run("both of the entry's repositories", func(t *testing.T) {
		s, gh := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: []string{testRepoPath, testOtherRepoPath}, TTLSeconds: 600, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
		}
		_, mint := gh.both(t)
		sent := buildMintSent(t, mint.body)
		if want := []string{name, otherName}; !slices.Equal(sent.Repositories, want) {
			t.Errorf("the token covers %v, want %v: one build bakes several repositories, and this is the union it is answered with", sent.Repositories, want)
		}
	})

	// The refusals, each with the repository it refused named in the answer:
	// the contract's promise to the build is "a refusal naming the thing that
	// was refused", and a build whose clone failed has to be told which
	// repository to take out of its entry.
	refused := []struct {
		name  string
		repos []string
		names string
	}{
		{"a repository the project holds on another entry", []string{buildElsewhereRepo}, buildElsewhereRepo},
		{"a repository the project holds nowhere", []string{buildUnlistedRepo}, buildUnlistedRepo},
		{"a repository beside one the entry's own set holds", []string{testRepoPath, buildElsewhereRepo}, buildElsewhereRepo},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := buildSvc(t, nil)
			resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
				Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
				Repositories: tc.repos, TTLSeconds: 600, AppID: testAppID,
			}), s.buildToken())
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("a repository outside the entry's was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
			}
			if !strings.Contains(e.Message, tc.names) {
				t.Errorf("the refusal does not name the repository it refused (%s): %s", tc.names, e.Message)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("the fake GitHub saw %d requests for a refused call, want none: a refusal mints nothing", n)
			}
			if n := len(s.auditLines(t)); n != 1 {
				t.Errorf("the refusal left %d audit lines, want exactly one", n)
			}
		})
	}

	t.Run("a call that names no repository", func(t *testing.T) {
		s, _ := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			TTLSeconds: 600, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("a call naming no repository was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
	})

	// An entry whose repositories are two GitHub owners: one App installation
	// belongs to one owner, so there is no one token to mint, and the service
	// refuses rather than answering a token over half of what the entry bakes.
	t.Run("an entry whose repositories are two owners", func(t *testing.T) {
		s, gh := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: "two-owners", BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: []string{buildSecondOwnerRepo, buildSecondOwnerOtherRepo},
			TTLSeconds:   600, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("an entry spanning two owners was answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("the fake GitHub saw %d requests, want none: no token is minted for an entry one token cannot cover", n)
		}
	})
}

// ---------------------------------------------------------------------------
// The life
// ---------------------------------------------------------------------------

// TestBuildTokenExpires is rule 7's first half: the credential is bounded, and
// what bounds it is not the caller's word.
//
// Four facts:
//
//   - a call that names no time to live is refused rather than given a default
//     (slip.go's clampRuntime is the precedent: a request that does not say how
//     long it needs has not made a request this route can answer);
//   - a GitHub answer's expiry is GITHUB's own instant and not a deadline this
//     service computed — the request's need is what the build needs, and the
//     endpoint is what decides how long a token lives;
//   - a grant's expiry is this service's own instant, the request's need ahead
//     of its clock;
//   - a call asking for longer than the cap is served the CAP, never the ask,
//     because a pairing that outlived its build is a standing grant to the
//     entry's repositories.
func TestBuildTokenExpires(t *testing.T) {
	t.Run("a call that names no time to live", func(t *testing.T) {
		s, gh := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: []string{testRepoPath}, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("a call naming no time to live was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("the fake GitHub saw %d requests, want none", n)
		}
	})

	t.Run("a GitHub build expires at GitHub's own instant", func(t *testing.T) {
		s, gh := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: []string{testRepoPath}, TTLSeconds: 60, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
		}
		gh.both(t)
		ans := buildAnswer(t, body)
		if ans.Token == nil {
			t.Fatalf("a GitHub build was answered no token: %s", body)
		}
		if !ans.Token.ExpiresAt.Equal(gh.expires) {
			t.Errorf("the answer expires %s, want %s: a token's life is the provider's own instant", ans.Token.ExpiresAt, gh.expires)
		}
		if ans.Token.ExpiresAt.After(s.now.Add(buildTokenMaxTTL)) {
			t.Errorf("the answer expires %s, and this service gives a build's credential at most %s", ans.Token.ExpiresAt, buildTokenMaxTTL)
		}
		// The token is presented as git's own https username and at the
		// provider's host, which is what the clone step writes a credential
		// file from without deriving either for itself.
		if ans.Token.User != "x-access-token" || ans.Token.Host != "github.com" {
			t.Errorf("the answer names user %q host %q, want the https username and github.com", ans.Token.User, ans.Token.Host)
		}
	})

	t.Run("a Bitbucket grant expires on this service's clock", func(t *testing.T) {
		s, _ := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildStoreEntry, BuildID: buildBuildID, Provider: gitprov.KindBitbucket,
			Repositories: []string{testBitbucketRepo}, TTLSeconds: 900,
		}), s.buildToken())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
		}
		ans := buildAnswer(t, body)
		if ans.Proxy == nil {
			t.Fatalf("a Bitbucket build was answered no grant: %s", body)
		}
		if want := s.now.Add(900 * time.Second); !ans.Proxy.ExpiresAt.Equal(want) {
			t.Errorf("the pairing ends %s, want %s: the grant's life is this service's own instant", ans.Proxy.ExpiresAt, want)
		}
	})

	t.Run("a call asking for longer than the cap is served the cap", func(t *testing.T) {
		s, _ := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildStoreEntry, BuildID: buildBuildID, Provider: gitprov.KindBitbucket,
			Repositories: []string{testBitbucketRepo}, TTLSeconds: 100 * 3600,
		}), s.buildToken())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
		}
		ans := buildAnswer(t, body)
		if ans.Proxy == nil {
			t.Fatalf("a Bitbucket build was answered no grant: %s", body)
		}
		if want := s.now.Add(buildTokenMaxTTL); !ans.Proxy.ExpiresAt.Equal(want) {
			t.Errorf("the pairing ends %s, want the cap %s: a request cannot choose a longer life than this service gives", ans.Proxy.ExpiresAt, want)
		}
	})

	// The build id is the caller's word (rule 2) and the service checks its
	// SHAPE rather than taking it as an opaque string: it is keyed into the
	// pairing, and an id that is not a build id is a key nothing else writes.
	t.Run("a build id that is not one", func(t *testing.T) {
		for _, id := range []string{"", "not a build id", strings.Repeat("x", 200)} {
			s, _ := buildSvc(t, nil)
			resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
				Entry: buildStoreEntry, BuildID: id, Provider: gitprov.KindBitbucket,
				Repositories: []string{testBitbucketRepo}, TTLSeconds: 600,
			}), s.buildToken())
			if resp.StatusCode != StatusRequestRefused {
				t.Errorf("a call naming the build id %q was answered %d, want %d: %s", id, resp.StatusCode, StatusRequestRefused, body)
			}
		}
	})

	// An entry this project's map puts nothing on: the call names an entry
	// nothing can be read for, and it is refused rather than answered nothing
	// or everything.
	t.Run("an entry the map puts no repository on", func(t *testing.T) {
		s, _ := buildSvc(t, nil)
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: "no-such-entry", BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: []string{testRepoPath}, TTLSeconds: 600, AppID: testAppID,
		}), s.buildToken())
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("an entry the map puts nothing on was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
	})
}

// ---------------------------------------------------------------------------
// The audit line
// ---------------------------------------------------------------------------

// TestBuildTokenMintIsAudited is rule 8's last clause and C1.13's rule for this
// route: one line per verified call, and the line names the act and never a
// value.
//
// The issuance's line is the one that must exist, and it is read whole: the
// build identity as its caller, the App key as its credential, the purpose that
// tells a build's mint from a run's, a person's and the daemon's — and NO run,
// no job and no repository, because the caller holds no run identity and the
// act is one token over several repositories, so a line naming one of them
// would say the mint was for that one.
//
// A refusal after the caller is verified writes exactly one line too, and it
// names no credential: the request was refused before one was read.
func TestBuildTokenMintIsAudited(t *testing.T) {
	s, gh := buildSvc(t, nil)
	resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
		Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
		Repositories: []string{testRepoPath}, TTLSeconds: 600, AppID: testAppID,
	}), s.buildToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build route refused an admitted build: HTTP %d %s", resp.StatusCode, body)
	}
	_, mint := gh.both(t)
	sent := buildMintSent(t, mint.body)
	if len(sent.Repositories) != 1 {
		t.Fatalf("the mint covers %v, want the one repository the call named", sent.Repositories)
	}

	line := s.onlyAuditLine(t)
	if got := auditField(line, "caller"); got != testBuildSA {
		t.Errorf("the line's caller is %q, want the build identity %q", got, testBuildSA)
	}
	if got := auditField(line, "credential"); got != config.GitHubAppKeySecret {
		t.Errorf("the line's credential is %q, want the App key %q", got, config.GitHubAppKeySecret)
	}
	if got := auditField(line, "purpose"); got != PurposeBuildToken {
		t.Errorf("the line's purpose is %q, want %q", got, PurposeBuildToken)
	}
	if got := auditField(line, "status"); got != StatusOK {
		t.Errorf("the line's status is %q, want %q", got, StatusOK)
	}
	for _, field := range []string{"run", "job", "repository", "tier", "slip", "owner"} {
		if got := auditField(line, field); got != "" {
			t.Errorf("the line carries %s = %q, and a build's mint names neither a run, a job, a repository, a tier, a slip nor an owner", field, got)
		}
	}
	// The token itself is nowhere in the line, which is the rule that makes
	// this log readable by an operator at all.
	if raw := s.audit.String(); strings.Contains(raw, gh.token) {
		t.Error("the audit line carries the token's value")
	}

	// A refusal after the caller is verified: exactly one line, and the
	// credential it NAMES is the App key — the name of what the act issues
	// from and not a value read out of the bundle, which is the mint's own
	// reading of the same rule (a mint's line names the App key whether the
	// refusal was taken before the bundle was read or after it).
	other, _ := buildSvc(t, nil)
	resp, body = other.buildCall(t, buildBody(t, buildRequestBody{
		Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
		Repositories: []string{buildUnlistedRepo}, TTLSeconds: 600, AppID: testAppID,
	}), other.buildToken())
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("a repository outside the entry's was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	refused := other.onlyAuditLine(t)
	if got := auditField(refused, "status"); got != ErrorRequestRefused {
		t.Errorf("the refusal's line carries status %q, want %q", got, ErrorRequestRefused)
	}
	if got := auditField(refused, "credential"); got != config.GitHubAppKeySecret {
		t.Errorf("the refusal's line names the credential %q, want the App key %q: the call named GitHub, so the act refused is the build token's", got, config.GitHubAppKeySecret)
	}
	if got := auditField(refused, "purpose"); got != PurposeBuildToken {
		t.Errorf("the refusal's line carries purpose %q, want %q", got, PurposeBuildToken)
	}
	if got := auditField(refused, "caller"); got != testBuildSA {
		t.Errorf("the refusal's line carries caller %q, want the build identity %q", got, testBuildSA)
	}
}

// ---------------------------------------------------------------------------
// The Bitbucket answer
// ---------------------------------------------------------------------------

// TestBitbucketBuildGetsAProxyGrantAndNoToken is R6's second half (D2302): a
// Bitbucket build holds NO credential at all — the service pairs its identity
// to the entry's repositories and answers a grant, and the build's git reaches
// the proxy with the build identity's own ID token.
//
// Held here: the answer is a grant and not a token (the body carries no
// "token" member at all, which is the fact a build's clone step reads), the
// grant names the entry's repositories and the proxy's own URL, the pairing
// ends on this service's clock, and no GitHub call is made — the mint's half of
// the service is not reached by this answer at all.
//
// The grant is not a secret and this test asserts it as such: the token's value
// never appears, and what a call to the proxy presents is the caller's own ID
// token.
func TestBitbucketBuildGetsAProxyGrantAndNoToken(t *testing.T) {
	s, gh := buildSvc(t, nil)
	resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
		Entry: buildStoreEntry, BuildID: buildBuildID, Provider: gitprov.KindBitbucket,
		Repositories: []string{testBitbucketRepo}, TTLSeconds: 600,
	}), s.buildToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the build route refused an admitted Bitbucket build: HTTP %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"token"`) {
		t.Errorf("a Bitbucket build was answered a token: %s", body)
	}
	ans := buildAnswer(t, body)
	if ans.Token != nil {
		t.Errorf("the answer carries a token as well as a grant, and the contract says exactly one: %+v", ans.Token)
	}
	if ans.Proxy == nil {
		t.Fatalf("a Bitbucket build was answered no grant: %s", body)
	}
	if want := testAudience + BitbucketProxyPrefix; ans.Proxy.URL != want {
		t.Errorf("the grant points at %q, want the proxy under this service's own URL %q", ans.Proxy.URL, want)
	}
	if want := []string{testBitbucketRepo}; !slices.Equal(ans.Proxy.Repositories, want) {
		t.Errorf("the grant pairs %v, want the entry's repositories %v", ans.Proxy.Repositories, want)
	}
	if !ans.Proxy.ExpiresAt.After(s.now) {
		t.Errorf("the pairing ends %s, which is not after this service's own clock %s", ans.Proxy.ExpiresAt, s.now)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the fake GitHub saw %d requests, want none: a Bitbucket build's answer mints nothing", n)
	}

	// The provider is the ENTRY's and not the caller's choice (rule 8): a call
	// naming the other provider than the entry's repositories are held at is
	// refused, whichever way round the two are, because owner/name is not
	// unique across hosts and the same name on the other host is another
	// repository.
	for _, tc := range []struct {
		name     string
		entry    string
		provider string
		repo     string
	}{
		{"a GitHub entry asked for as Bitbucket", buildEntry, gitprov.KindBitbucket, testRepoPath},
		{"a Bitbucket entry asked for as GitHub", buildStoreEntry, gitprov.KindGitHub, testBitbucketRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := buildSvc(t, nil)
			resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
				Entry: tc.entry, BuildID: buildBuildID, Provider: tc.provider,
				Repositories: []string{tc.repo}, TTLSeconds: 600, AppID: testAppID,
			}), s.buildToken())
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("a call naming the entry's other provider was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("the fake GitHub saw %d requests, want none", n)
			}
		})
	}

	// An installation with no URL of its own answers no Bitbucket build: a
	// grant naming no proxy would send a build's clone nowhere, so the route
	// refuses rather than answering one.
	t.Run("a service with no URL of its own", func(t *testing.T) {
		s, _ := buildSvc(t, func(_ *svc, cfg *Config) { cfg.BaseURL = "" })
		resp, body := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: buildStoreEntry, BuildID: buildBuildID, Provider: gitprov.KindBitbucket,
			Repositories: []string{testBitbucketRepo}, TTLSeconds: 600,
		}), s.buildToken())
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a Bitbucket build was answered %d with no service URL, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
	})
}

// ---------------------------------------------------------------------------
// The class
// ---------------------------------------------------------------------------

// TestEveryOtherClassRefusedOnTheBuildRoute is rule 9's first half: the build
// route is the build's alone.
//
// Every other class this service admits presents its own credential here and is
// refused — a job's, a review job's, a verifier's, a person's, the
// coordinator's, a launcher's and a run identity's — and so is the build
// identity itself when it presents a token of the wrong audience or a call of a
// shape this route does not have (a run identity or a job name, which a build
// cannot have and this route refuses BY NAME rather than dropping).
//
// The refusal a class gets is the typed 403 the service answers every class
// lookup with, and the one it gets for a token whose audience is gcloud's own
// is the same: the build class carries this service's URL and nothing else
// (GcloudAudience is the person's two read mints' and no other class's).
func TestEveryOtherClassRefusedOnTheBuildRoute(t *testing.T) {
	s, _ := buildSvc(t, nil)
	body := buildBody(t, buildRequestBody{
		Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
		Repositories: []string{testRepoPath}, TTLSeconds: 600, AppID: testAppID,
	})

	refused := []struct {
		name string
		tok  string
	}{
		{"no credential at all", ""},
		{"a job's token", s.jobToken()},
		{"a review job's token", s.reviewJobToken()},
		{"a verifier's token", verifierToken(t, s, testVerifierSA)},
		{"a person's token", s.personToken(testPersonAccount)},
		{"the coordinator's token", s.personToken(testCoordinatorAccount)},
		{"a launcher's token", s.personToken(testLauncherAccount)},
		{"a build token for another audience", s.buildToken(audienceClaim(GcloudAudience))},
		{"a build token naming another account", s.jobToken()},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			fresh, gh := buildSvc(t, nil)
			resp, body := fresh.buildCall(t, body, tc.tok)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the build route answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorUnverifiedJobIdentity, e.Message)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("the fake GitHub saw %d requests, want none", n)
			}
		})
	}

	// A build that presents a run identity or a job name: the route takes
	// neither, and it refuses the header by name rather than reading the call
	// with it. This is the one refusal on this route that is recorded with the
	// run-identity type, because it is about the shape of the call.
	for _, tc := range []struct {
		name   string
		header string
	}{
		{"a run identity", HeaderRunIdentity},
		{"a job name", HeaderRunJob},
	} {
		t.Run("a build presenting "+tc.name, func(t *testing.T) {
			fresh, _ := buildSvc(t, nil)
			resp, body := fresh.buildCall(t, body, fresh.buildToken(), func(r *http.Request) {
				if tc.header == HeaderRunIdentity {
					r.Header.Set(HeaderRunIdentity, fresh.runToken())
					return
				}
				r.Header.Set(HeaderRunJob, fresh.job)
			})
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the build route answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorUnverifiedRunIdentity, e.Message)
			}
			// The refusal is recorded: the caller WAS verified, so it has a
			// name, and exactly one line carries it.
			line := fresh.onlyAuditLine(t)
			if got := auditField(line, "caller"); got != testBuildSA {
				t.Errorf("the line's caller is %q, want the build identity %q", got, testBuildSA)
			}
		})
	}

	// A class refusal is answered before the caller is verified, so it has no
	// name to write and writes no line at all (the rule every other class
	// route's refusals above the class are held to).
	if lines := s.auditLines(t); len(lines) != 0 {
		t.Errorf("a refused class left %d audit lines, want none: nothing was verified to name", len(lines))
	}
}

// TestBuildIdentityRefusedOnEveryOtherRoute is rule 9's other half: the build
// route is the one route a build's identity opens.
//
// Every other route of this service is aimed at with the build's own credential
// and refuses it. What each answers is that route's own (the shared parse
// refuses a call that carries no run identity for the four routes it reads, and
// each of the routes answered ahead of it refuses the class) — the point of the
// test is not which sentence comes back but that NO route answers a build
// anything but a refusal.
//
// The Bitbucket proxy's rows are here too and they are refusals: what a call
// presents there is the build identity's ID token AND the pairing the grant
// recorded, and the pairing a grant describes is read by the proxy's own
// admission (S2, N1) — the build's data plane. What this test holds is that the
// credential alone opens nothing.
func TestBuildIdentityRefusedOnEveryOtherRoute(t *testing.T) {
	s, _ := buildSvc(t, nil)
	digest, signature := testDigest(t, "build-identity"), strings.Repeat("0", 64)
	body := buildBody(t, buildRequestBody{
		Entry: buildEntry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
		Repositories: []string{testRepoPath}, TTLSeconds: 600, AppID: testAppID,
	})

	// Every route but the build route, with a body of that route's own shape:
	// a refusal that only answered "the body is not one this contract reads"
	// would be a test of the reader and not of the class.
	routes := []struct {
		name string
		path string
		body string
	}{
		{"messages", MessagesPath, callBody("the-build-identity")},
		{"count tokens", CountTokensPath, callBody("the-build-identity")},
		{"the mint", MintPath, mintBody(testAppID)},
		{"the sign route", SignPath, signBody(digest)},
		{"the verify route", VerifyPath, verifyBody(testReviewAccount, s.run, s.slug, digest, signature)},
		{"the Bitbucket read mint", BitbucketReadPath, `{"repository":"` + testBitbucketRepo + `"}`},
		{"the GitHub read mint", GitHubReadPath, githubReadBody(testRepoPath, testAppID)},
		{"the merge mint", MergeMintPath, `{"repository":"` + testRepoPath + `","number":7,"head_sha":"` + digest + `","app_id":"` + testAppID + `"}`},
		{"the label route", LabelPath, body},
		{"the grant route", SlipPath, body},
		{"the claim route", SlipClaimPath, body},
		{"the workflow secrets", WorkflowSecretsPath, body},
		// The proxy is rule 9's ONE exception, and it is in this list because
		// the exception is not reachable yet: the build's pairing is DERIVED
		// from the pair the shared parse holds (pairShared), and this route
		// records none — a build has no run identity, so no pair exists for it
		// and the proxy refuses it exactly as it refuses any caller with no
		// binding. That is the fail-closed half of contract isolation option 1
		// (nothing a build sends reaches a forge) with the store still
		// missing; the tests below the grant's answer hold the grant itself.
		// When the pairing is recorded, the three rows leave this list, and
		// leaving them is the change that says a build's proxy traffic serves.
		{"the proxy: a fetch", BitbucketProxyPrefix + testBitbucketRepo + ".git/info/refs?service=git-upload-pack", ""},
		{"the proxy: a push", BitbucketProxyPrefix + testBitbucketRepo + ".git/git-receive-pack", ""},
		{"the proxy: an LFS batch", BitbucketProxyPrefix + testBitbucketRepo + ".git/lfs/objects/batch", ""},
	}
	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			fresh, _ := buildSvc(t, nil)
			method := http.MethodPost
			if strings.HasPrefix(tc.path, BitbucketProxyPrefix) {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, fresh.http.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("building the call: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+fresh.buildToken())
			req.Header.Set("Content-Type", "application/json")
			resp, err := fresh.http.Client().Do(req)
			if err != nil {
				t.Fatalf("the call failed: %v", err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the answer: %v", err)
			}
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("the build identity was admitted on %s: %s", tc.name, raw)
			}
			if e := refusal(t, resp.StatusCode, raw); e.Error == "" {
				t.Errorf("the answer on %s is not a typed refusal: %s", tc.name, raw)
			}
		})
	}
}

// buildPathIsServed holds the route's own constant to the path every test in
// this file calls: the calls above are made to a literal, so this is what says
// the literal and the service agree rather than one of them having moved.
func TestBuildRoutePathIsTheServicesOwn(t *testing.T) {
	if BuildTokenPath != buildPath {
		t.Errorf("BuildTokenPath = %q, and this file's calls are made to %q", BuildTokenPath, buildPath)
	}
	if !servedRoute(&http.Request{Method: http.MethodPost, URL: &url.URL{Path: buildPath}}) {
		t.Errorf("the service does not serve POST %s", buildPath)
	}
	// The route is a POST and nothing else: a build presents a body, and a GET
	// on this path is a call of a shape the route does not have.
	if servedRoute(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: buildPath}}) {
		t.Errorf("the service serves GET %s, and the build route is a POST alone", buildPath)
	}
}
