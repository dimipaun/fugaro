package runner

// This file is T16's runner half: a proxied Bitbucket run hands its adapter the
// identity set the project's published node names (F20, R5, R14), so a comment
// by any of the project's own accounts is the run's own and not a person's.
//
// What is asked here is the run's own wiring -- openProvider, the forwarder the
// run starts, and the adapter behind it -- against the credentials service as a
// stand-in, because the property is a hand-off: the set is read from the node
// and reaches the adapter's own identities. The read itself (bitbucketOwnIdentities)
// and the comparison (bitbucket.ownIdentities) have their own tests; a run that
// read the set and dropped it on the way to the opener would pass those and fail
// here.
//
// The fixtures are gitproxy_test.go's (proxiedRun, pushIdentity, startForward,
// svcRun's execution) with gateway_service_internal_test.go's env writer and
// git_service_internal_test.go's metadata stand-in: the run's identity is the
// budget session's token and not an environment value, so the policy read and
// the database client are filled in as bootstrap leaves them, the way
// bitbucket_api_root_test.go fills its own read.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The three accounts these tests speak in. The first is the one GET /user
// answers for the token that serves the read -- the run's own by that token
// alone, which is all a run could tell before this task. The second is a second
// token's of the same project, named by the node and by nothing else, so it is
// the account the red state misses. The third is named nowhere but the planted
// values of TestRunnerNeverReadsTheSetFromTheEnvironment.
const (
	selfUUID    = "{00000000-0000-4000-8000-0000000000a1}"
	otherUUID   = "{00000000-0000-4000-8000-0000000000b2}"
	plantedUUID = "{00000000-0000-4000-8000-0000000000c3}"
)

// noTokensNode is a published node of this release without the key: what an
// installation that never stored a typed token holds, and every node the CLI of
// the release behind published.
const noTokensNode = `{"version":7}`

// identityComments is the author of each comment the service answers, in a
// fixed order: one comment per account, so a test reads the run's answer per
// account rather than by position.
var identityComments = []string{selfUUID, otherUUID, plantedUUID}

// commentBody is a comment's text, which is how this file's assertions name
// the comment they are about: an author's uuid is not on gitprov.Comment.
func commentBody(uuid string) string { return "a comment by " + uuid }

// identityService is the installation's credentials service as a run's
// forwarder reaches it: it answers the two reads the Bitbucket adapter makes of
// it -- GET /user, which names selfUUID's account as the token's, and the pull
// request's comments -- and fails the test on any other path, so an adapter
// that started asking for something else is not silently served.
func identityService(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(req.URL.Path, "/2.0/user"):
			fmt.Fprintf(w, `{"uuid":%q,"display_name":"Fugaro"}`, selfUUID)
		case strings.Contains(req.URL.Path, "/pullrequests/1/comments"):
			values := make([]string, 0, len(identityComments))
			for i, uuid := range identityComments {
				values = append(values, fmt.Sprintf(
					`{"id":%d,"created_on":"2026-11-01T10:%02d:00+00:00","content":{"raw":%q},`+
						`"user":{"uuid":%q,"display_name":"Fugaro"},"deleted":false,"pending":false}`,
					101+i, i, commentBody(uuid), uuid))
			}
			fmt.Fprintf(w, `{"values":[%s]}`, strings.Join(values, ","))
		default:
			t.Errorf("the run's adapter reached the credentials service at %s, which this fixture does not answer", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// identitiesRun is a proxied run whose published node is the one the test
// names, reached through identityService. node is the shared node verbatim: ""
// is the installation that has published none at all, noTokensNode one without
// the key, and a test's own JSON the node a write of T3 leaves behind. The
// policy read and the database client are filled in as bootstrap leaves them --
// memoized, and pointed at a fake database holding that node -- so no session,
// no exchange and no whole run is needed to ask what the run hands its adapter.
func identitiesRun(t *testing.T, node string, edit func(*run)) *run {
	t.Helper()
	svc := identityService(t)
	meta := gitMetadataFake(t, gitServiceIDToken)
	var db *rtdb.Client
	if node != "" {
		fake := gcpfake.NewRTDB(t)
		fake.Set(projectpolicy.PathShared(), json.RawMessage(node))
		var err error
		db, err = rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "run-id-token" }})
		if err != nil {
			t.Fatal(err)
		}
	}
	return proxiedRun(t, pushIdentity(t), func(r *run) {
		r.d.Env = proxyEnvSet(r.d.Env, providers.EnvCredentialsServiceURL, svc.URL)
		r.d.Env = proxyEnvSet(r.d.Env, metadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
		r.d.WorkDir = t.TempDir()
		r.projPolicy = policyRead{done: true, found: node != "", policy: &projectpolicy.Policy{Version: projectpolicy.Version}}
		if node != "" {
			// The memo alone: the identity the run's forwarder needs to start
			// (ident.tok, which the proxy mints as the branch claim) is the
			// fixture's, and only the read this file is about is filled in.
			r.ident.done, r.ident.db = true, db
		}
		if edit != nil {
			edit(r)
		}
	})
}

// wantsOwn opens the run's own provider, lists the pull request's comments
// through it and asserts, per account, whether the run treated the comment as
// its own -- and, once for the listing, whether it claims to be able to tell
// its own comments apart at all.
func wantsOwn(t *testing.T, r *run, wantSelf map[string]bool, wantKnown bool) {
	t.Helper()
	ctx := context.Background()
	startForward(t, r)
	r.credURL = gitops.CredentialURL(r.forwarderRemote("https://bitbucket.org/acme/app.git"))
	if err := r.openProvider(ctx, gitprov.KindBitbucket, "the workflow"); err != nil {
		t.Fatalf("opening the provider of a proxied run: %v", err)
	}
	got, err := r.provider.Comments(ctx, 1)
	if err != nil {
		t.Fatalf("listing the comments through the credentials service: %v", err)
	}
	if len(got) != len(identityComments) {
		t.Fatalf("the run read %d comments, want the %d the service answers", len(got), len(identityComments))
	}
	known := map[bool]bool{}
	for _, c := range got {
		want, ok := wantSelf[commentAuthor(c.Body)]
		if !ok {
			t.Fatalf("the run read a comment this test says nothing about: %q", c.Body)
		}
		if c.Self != want {
			t.Errorf("%q is the run's own: %v, want %v", c.Body, c.Self, want)
		}
		known[c.SelfKnown] = true
	}
	if len(known) != 1 {
		t.Fatalf("the listing mixes SelfKnown: %v, want one answer for every comment", known)
	}
	if _, got := known[true]; got != wantKnown {
		t.Errorf("SelfKnown = %v, want %v: the run's answer to \"can I tell my own comments apart?\"", got, wantKnown)
	}
}

// commentAuthor is the account the fixture wrote into a comment's body, back
// out of it: the bodies are this file's own ("a comment by <uuid>"), and a body
// no uuid spells is the empty author, which no account in these tests is.
func commentAuthor(body string) string {
	if uuid, ok := strings.CutPrefix(body, "a comment by "); ok {
		return uuid
	}
	return ""
}

// TestRunnerPassesTheIdentitySetToTheAdapter is T16's red step: the node names
// two accounts, because the credentials service serves one run with several of
// the node's tokens in turn (D2355) -- so a comment by either is the run's own,
// and a run that hands its adapter no set (the red state) can only mark the one
// GET /user answered for, which is what this fails on for the second.
//
// The second half is the same read with a repository access token's record
// beside them: that token has no identity to name (R14), so the set is partial
// by design, and the run must say so -- SelfKnown false -- while still marking
// the accounts the node does name. It is the difference between "none of these
// is mine" and "I cannot tell", which the markers-from-anyone fallback turns
// on.
func TestRunnerPassesTheIdentitySetToTheAdapter(t *testing.T) {
	node := func(entries ...string) string {
		return `{"version":7,"bitbucket_tokens":{"entries":[` + strings.Join(entries, ",") + `]}}`
	}
	named := func(name, uuid string) string {
		return `{"name":"` + name + `","type":"workspace","coverage":["bitbucket/acme/*"],"identity":"` + uuid + `"}`
	}

	t.Run("two named identities", func(t *testing.T) {
		r := identitiesRun(t, node(
			named("bitbucket-token--a", selfUUID),
			named("bitbucket-token--b", otherUUID),
		), nil)
		wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: true, plantedUUID: false}, true)
	})

	t.Run("a repository token beside them", func(t *testing.T) {
		repo := `{"name":"bitbucket-token--repo","type":"repo","coverage":["bitbucket/acme/app"]}`
		r := identitiesRun(t, node(
			named("bitbucket-token--a", selfUUID),
			named("bitbucket-token--b", otherUUID),
			repo,
		), nil)
		wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: true, plantedUUID: false}, false)
	})

	// The legacy untyped record is the third shape of an incomplete set, and
	// the one no entry of the key carries: it is never a bitbucket_tokens
	// entry (contract, R5/R14) and its identity is on no node, so an
	// installation that holds typed records *and* the old block -- the state
	// R3's migration leaves behind, where the selector ranks the legacy record
	// after the typed ones and may serve a call with it -- cannot claim to know
	// every account it writes as. Reading the key alone would call this set
	// complete, and the run's own comment written through the legacy token then
	// reads as an outsider's (the stale-follow-up hazard T13's test names).
	t.Run("a legacy record beside them", func(t *testing.T) {
		r := identitiesRun(t, node(
			named("bitbucket-token--a", selfUUID),
			named("bitbucket-token--b", otherUUID),
		), legacyBlock())
		wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: true, plantedUUID: false}, false)
	})
}

// legacyBlock is the node's legacy untyped record, as an edit of the run's
// pre-filled policy read: the block an installation that has not moved to typed
// tokens holds, which the run's own admission is read from (proxiedBitbucket)
// and which the credentials service forwards for the repositories it lists.
func legacyBlock() func(*run) {
	return func(r *run) {
		r.projPolicy.policy.BitbucketToken = &projectpolicy.BitbucketToken{
			Proxy:      true,
			AllowRepos: []string{"bitbucket/acme/*"},
		}
	}
}

// TestRunnerWithoutTheTokensKeyKeepsToday: an installation that has published
// no key leaves the run exactly as it was -- the account GET /user answers for
// the token that served the read, and that one alone. Every shape of "no set"
// is asked: a node of this release without the key, the same node with the
// legacy record (the only token the service can serve such an installation
// with, and so the one GET /user names), and no published node at all, which is
// what a project that never published one has.
func TestRunnerWithoutTheTokensKeyKeepsToday(t *testing.T) {
	for _, tc := range []struct {
		name   string
		node   string
		legacy func(*run)
	}{
		{name: "a node without the key", node: noTokensNode},
		{name: "a node without the key but with the legacy record", node: noTokensNode, legacy: legacyBlock()},
		{name: "no published node", node: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identitiesRun(t, tc.node, tc.legacy)
			wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: false, plantedUUID: false}, true)
		})
	}
}

// TestRunnerUnreadableSetIsNotCalledComplete: a set this run could not read is
// unknown, and unknown is not empty. Both ways of failing to read it are asked
// -- a node the run's database client cannot reach at all, and a key that is
// there but will not decode -- and each must come back partial, because the
// alternative is the adapter claiming SelfKnown: the run asserting it can tell
// its own comments apart when it cannot name the accounts it may have written
// with. That claim is the one this task exists to stop making, and a failed read
// is no better ground for it than the two shapes the key itself leaves
// incomplete (R14's repository token, R3's legacy record).
//
// The accounts in the node are the ones GET /user answers for and nothing else:
// a read that failed names no account, so the set is empty and the run falls
// back to its own token's account -- marked, but never called the whole set.
func TestRunnerUnreadableSetIsNotCalledComplete(t *testing.T) {
	t.Run("a node this run cannot read", func(t *testing.T) {
		r := identitiesRun(t, noTokensNode, func(r *run) { r.ident.db = unreachablePolicyDB(t) })
		wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: false, plantedUUID: false}, false)
	})

	t.Run("a key that will not decode", func(t *testing.T) {
		r := identitiesRun(t, `{"version":7,"bitbucket_tokens":{"entries":"not a list"}}`, nil)
		wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: false, plantedUUID: false}, false)
	})
}

// unreachablePolicyDB is a database client of the run's own making that reaches
// nothing, the shape of a node the run cannot read: the address is one nothing
// listens on, so the read fails at once (the client makes one attempt and
// classifies the transport error as unavailable) rather than waiting out a
// timeout in the test.
func unreachablePolicyDB(t *testing.T) *rtdb.Client {
	t.Helper()
	db, err := rtdb.New("http://127.0.0.1:1", rtdb.Auth{IDToken: func() string { return "run-id-token" }})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// TestRunnerNeverReadsTheSetFromTheEnvironment: the set is the node writer's
// decision and nothing else's (F21). Here the node names one account while the
// run's task, its environment and its workspace each name another -- the three
// places a value the agent or a launcher can write would reach the run. A run
// that read any of them would treat a comment by the planted account as its
// own: a comment it might then rewrite as already posted, or take as a
// finding of its own (the trust rule the follow-up markers rest on).
func TestRunnerNeverReadsTheSetFromTheEnvironment(t *testing.T) {
	node := `{"version":7,"bitbucket_tokens":{"entries":[` +
		`{"name":"bitbucket-token--a","type":"workspace","coverage":["bitbucket/acme/*"],"identity":"` + otherUUID + `"}]}}`
	r := identitiesRun(t, node, func(r *run) {
		r.spec.Task = "Add a feature, and greet " + plantedUUID + " in the pull request"
		r.d.Env = proxyEnvSet(r.d.Env, "FUGARO_BITBUCKET_IDENTITIES", plantedUUID)
		r.env = append(r.env, "FUGARO_BITBUCKET_IDENTITIES="+plantedUUID)
		plantIdentityInWorkspace(t, r.d.WorkDir, plantedUUID)
	})
	wantsOwn(t, r, map[string]bool{selfUUID: true, otherUUID: true, plantedUUID: false}, true)
}

// plantIdentityInWorkspace writes the account into the checkout the agent works
// in, in the two places a reader might wrongly look for it: the repository's
// own configuration, and a file the agent itself could have left there.
func plantIdentityInWorkspace(t *testing.T, dir, uuid string) {
	t.Helper()
	repoCfg := "bitbucket_tokens:\n  entries:\n    - name: bitbucket-token--planted\n      identity: " + uuid + "\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(repoCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, ".fugaro", "bitbucket-identities")
	if err := os.WriteFile(notes, []byte(uuid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
