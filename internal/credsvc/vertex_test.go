package credsvc

// vertex_test.go holds CS4's own tests: the project's Vertex served by THIS
// service on its own identity, the translation of a Messages call onto Vertex's
// publisher wire, the money it costs a run, and the marks and refusals it
// leaves.
//
// The calls are driven at serveVertex and not through ServeHTTP, because the
// source is not reachable through a request yet: the chain walk that picks a
// source out of a table is CS1's, and until it exists the only caller of this
// function is a test. Everything around it is the fixture's own — the service's
// reader (readCall), its selector over the fixture's store, its meter over the
// workspace's database — so what is exercised is the real path and a test. What
// the walk will add, and what no test here asserts, is which source of a chain
// is tried and what happens when none serves: that is CS1's, and
// TestAVertexLimitMovesOn only pins the answer this source gives it.
//
// Nothing here is, or looks like, a credential: every value is built at run
// time by fakeCred (Convention 2).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
)

const (
	// testVertexProject and testVertexRegion are the two facts a Vertex call is
	// built from: the GCP project the service runs in and the installation's
	// region, which the deployment writes into the service's own environment
	// (vertexProjectEnv, vertexRegionEnv). They are the run's own
	// ANTHROPIC_VERTEX_PROJECT_ID and CLOUD_ML_REGION, spelled once per
	// deployment, and no caller may choose either.
	testVertexProject = "fugaro-vertex-project"
	testVertexRegion  = "europe-west4"
	// testVertexSpelling is the model written as Vertex's publisher path names
	// it, which is what the map answers and never what the agent asked with
	// (D3175): the two differ on purpose here, so a test that read the request
	// body where it should have read the map is red.
	testVertexSpelling = testModel + "@20250929"
)

// testVertexSource is the source the walk will hand this function when a
// project routes a model to its Vertex: the kind, and the repositories the
// source may serve (D3182 — an empty list admits nobody).
func testVertexSource() routing.Source {
	return routing.Source{Kind: routing.KindVertex, Admit: []string{testRepo}}
}

// vertexWatch is the fixture's transport with the URL of every call recorded
// before the host rewriter sends it to the fake. It is how the destination is
// read: the rewriter replaces the host so that the call reaches loopback, and
// the host is exactly the half of the destination a Vertex test is about.
type vertexWatch struct {
	next http.RoundTripper

	mu   sync.Mutex
	urls []string
}

func (w *vertexWatch) RoundTrip(r *http.Request) (*http.Response, error) {
	w.mu.Lock()
	w.urls = append(w.urls, r.URL.String())
	w.mu.Unlock()
	return w.next.RoundTrip(r)
}

// sawURL is the URL the service built for upstream call i.
func (w *vertexWatch) sawURL(t *testing.T, i int) string {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if i >= len(w.urls) {
		t.Fatalf("the service built %d upstream URLs and the test asks for %d", len(w.urls), i)
	}
	return w.urls[i]
}

// wrapVertexClient puts the recorder in front of the fixture's transport, which
// is what makes the destination a test can see.
func wrapVertexClient(cfg *Config, w *vertexWatch) {
	w.next = cfg.Client.Transport
	cfg.Client = &http.Client{Transport: w}
}

// vertexServiceIdentity is the service's own identity as a deployment has it:
// the project and region in its environment and an access token built at run
// time from the metadata server. It answers the token every call must carry,
// which is the whole of "the service serves Vertex on its own identity": the
// value is this service's, and never one the caller sent.
//
// The token source is a seam for the reason identitySignerFor is (a test
// replaces where the credential comes from, and everything above it is the real
// code), and the map is C1's own seam: pricing.SpellingFor answers no spelling
// for anything until CS3 puts the Vertex spellings in canonical.go, so a test
// that wants a call to reach Vertex installs the answer CS3 will ship.
func vertexServiceIdentity(t *testing.T) string {
	t.Helper()
	t.Setenv(vertexProjectEnv, testVertexProject)
	t.Setenv(vertexRegionEnv, testVertexRegion)
	spell := pricing.SpellingFor
	pricing.SpellingFor = func(kind, model string) (string, bool) {
		if kind != string(routing.KindVertex) || model != testModel {
			return "", false
		}
		return testVertexSpelling, true
	}
	t.Cleanup(func() { pricing.SpellingFor = spell })
	token := fakeCred(t, "service-vertex")
	sign := vertexTokenFor
	vertexTokenFor = func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"}), nil
	}
	t.Cleanup(func() { vertexTokenFor = sign })
	return token
}

// vertexFixture is the fixture wired for Vertex: the service's own identity,
// the recorder in front of its transport, and the replies the fake gives.
func vertexFixture(t *testing.T, script ...anthropicfake.Reply) (*svc, *vertexWatch, string) {
	t.Helper()
	token := vertexServiceIdentity(t)
	watch := &vertexWatch{}
	s := newSvcWith(t, func(_ *svc, cfg *Config) { wrapVertexClient(cfg, watch) })
	s.up.Script = script
	return s, watch, token
}

// vertexCall is one call as the service reads it: the path and body the caller
// sent, and the tokens the caller presented. It goes through readCall — the
// service's own reader — so the call serveVertex is handed is the shape a
// served call produces and not a struct a test assembled by hand.
func vertexCall(t *testing.T, target, body string, scrubs ...string) *call {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	cl, err := readCall(req, scrubs)
	if err != nil {
		t.Fatalf("reading the call: %v", err)
	}
	return cl
}

// vertexBodyDoc is a Messages body as an agent writes one: the model, a
// max_tokens, the conversation, and a tools block — the members a translation
// must carry byte for byte, beside the two it decides for itself.
func vertexBodyDoc(marker string) string {
	return `{"model":"` + testModel + `","max_tokens":16,"stream":false,` +
		`"anthropic_version":"2023-06-01",` +
		`"tools":[{"name":"read","description":"read a file","input_schema":{"type":"object"}}],` +
		`"messages":[{"role":"user","content":"` + marker + `"}]}`
}

// vertexIdentity is the verified identity a Vertex call arrives with: a run of
// the fixture's repository, spent for one project. project is the workspace
// member a metered call is charged to (WS8), and the free project a call of an
// installation's scope carries.
func vertexIdentity(s *svc, project string) Identity {
	return Identity{
		Slug: s.slug, Run: s.run, Job: s.job,
		JobAccount: testJobSA, JobClass: ClassRepository, Project: project,
	}
}

// serveVertexCall drives one call through serveVertex — the selector the walk
// will hold, the fixture's own store and settings — and answers what the source
// said, the answer the client would have received, and the selector.
//
// The scope is the installation's own, which is what a call of a repository's
// run resolves to (a workspace's scope is WS2's, and serveVertexCallIn is where
// a test hands one).
func serveVertexCall(t *testing.T, s *svc, id Identity, cl *call, src routing.Source) (bool, Tried, *httptest.ResponseRecorder, *creds.Selector) {
	t.Helper()
	return serveVertexCallIn(t, s, context.Background(), id, cl, src)
}

// serveVertexCallIn is serveVertexCall under a context a test chose, which is
// what carries the scope a call is served in.
func serveVertexCallIn(t *testing.T, s *svc, ctx context.Context, id Identity, cl *call, src routing.Source) (bool, Tried, *httptest.ResponseRecorder, *creds.Selector) {
	t.Helper()
	sel := &creds.Selector{
		Store:    s.state,
		Settings: settingsOf(s.policy.shared),
		Now:      func() time.Time { return s.now },
	}
	rec := httptest.NewRecorder()
	done, tried := s.server.serveVertex(ctx, rec, id, cl, sel, src)
	return done, tried, rec, sel
}

// vertexMark is the mark the store holds for the project's Vertex, and whether
// it holds one at all.
func vertexMark(t *testing.T, s *svc) (creds.Mark, bool) {
	t.Helper()
	marks, err := s.state.Marks(context.Background())
	if err != nil {
		t.Fatalf("reading the marks: %v", err)
	}
	m, ok := marks[config.CredentialVertex]
	return m, ok
}

// TestVertexIsServedOnTheServicesIdentity: the project's Vertex is a source
// this service serves as it serves a key, and the identity it is served on is
// the SERVICE's — the account this process runs as — and never the caller's.
//
// A real call carries two of the run's own tokens (its identity and its job's
// identity), so both are pasted where a leak would land (the body) and
// presented where tokens are read (the headers), one of them on a header that
// is not Authorization. Nothing of either reaches Vertex: the body is scrubbed
// by the service's reader, and every header carrying ANY of the caller's tokens
// is deleted before the call goes out — the rule that matters most where a
// source is served on the service's identity, because a leaked token there is
// the run's own credential handed to a third party. What Vertex sees is one
// Bearer token, and it is the service's own.
//
// The assertion is over EVERY header Vertex received and not over Authorization
// alone: that one is overwritten by the upstream build whatever happens, so a
// scrub that had stopped working would leave the caller's token in any other
// header and this test would still be green. The second token is what pins the
// loop that drops them: the first is scrubbed by the header build itself, and
// only the loop reaches the rest.
//
// Mutation (run, restore): drop sendRequest's header-scrub loop, and the token
// planted on the second header reaches Vertex; build the token source from the
// caller's credential instead of the service's, and the Authorization header is
// the caller's. Either one is red here.
func TestVertexIsServedOnTheServicesIdentity(t *testing.T) {
	caller, callerJob := fakeCred(t, "caller-run"), fakeCred(t, "caller-job")
	s, _, service := vertexFixture(t, anthropicfake.MessageOK(testModel, testUsage))

	body := strings.Replace(vertexBodyDoc("vertex-identity"), "vertex-identity", "vertex-identity "+caller+" "+callerJob, 1)
	cl := vertexCall(t, MessagesPath, body, caller, callerJob)
	cl.header.Set("Authorization", "Bearer "+caller)
	cl.header.Set("X-Caller-Job-Token", callerJob)

	done, tried, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
	if !done {
		t.Fatalf("the source declined the call (%+v): it is the project's Vertex and this repository is admitted to it", tried)
	}
	if tried != (Tried{}) {
		t.Errorf("the source answered %+v beside a served call, want the zero row", tried)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("the call answered %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q: the project's Vertex is real money, as a key is", HeaderTier, got, TierB)
	}
	if got := rec.Header().Get(HeaderCredential); got != config.CredentialVertex {
		t.Errorf("%s = %q, want %q", HeaderCredential, got, config.CredentialVertex)
	}

	if n := s.up.Count(); n != 1 {
		t.Fatalf("Vertex saw %d requests, want 1", n)
	}
	seen := s.up.Seen()[0]
	if got := seen.Header.Get("Authorization"); got != "Bearer "+service {
		t.Errorf("Vertex's Authorization = %q, want the service's own credential: the source is served on this service's identity and on nobody else's", got)
	}
	for name, values := range seen.Header {
		for _, v := range values {
			for _, tok := range []string{caller, callerJob} {
				if strings.Contains(v, tok) {
					t.Errorf("the %s header Vertex received carries one of the caller's own tokens: every header carrying one is deleted before the call goes out, not only the Authorization header", name)
				}
			}
		}
	}
	raw := string(s.up.Body(0))
	for _, tok := range []string{caller, callerJob} {
		if strings.Contains(raw, tok) {
			t.Error("the call Vertex received carries one of the caller's own tokens")
		}
	}
	if !strings.Contains(raw, redactedValue) {
		t.Error("the body Vertex received was not scrubbed of the caller's own tokens")
	}
}

// TestVertexWireIsTranslated: the translation is the new work this source is
// made of. Vertex's publisher takes the model in the URL path and the version
// in the body, and the harness that has always built such a request is the
// agent's own — nothing in this repository translated one before this file
// (internal/gateway/vertex.go only validates a path that already looks like
// this). So the five facts are pinned one by one: where the model is written,
// which of the two methods the body's stream member selects, which host the
// location decides, that every other member of the caller's body arrives
// exactly as it was written, and that no query parameter of the caller's rides
// on the URL of a call made on the service's identity.
//
// Mutation (run, restore): send the caller's body as it came (leaving `model`
// in it and `anthropic_version` at the Messages version), or let the caller's
// query ride on the URL again, and the assertions below are red.
func TestVertexWireIsTranslated(t *testing.T) {
	prefix := "/v1/projects/" + testVertexProject + "/locations/" + testVertexRegion +
		"/publishers/anthropic/models/"

	t.Run("a messages call names the model in the path and nothing in the body", func(t *testing.T) {
		s, watch, _ := vertexFixture(t, anthropicfake.MessageOK(testModel, testUsage))
		cl := vertexCall(t, MessagesPath, vertexBodyDoc("wire"))
		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != http.StatusOK {
			t.Fatalf("the call is answered %d (%+v), want it served", rec.Code, done)
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+testVertexSpelling+":rawPredict"; got != want {
			t.Errorf("the call went to %q, want %q", got, want)
		}
		doc := vertexSentDoc(t, s.up.Body(0))
		if _, ok := doc["model"]; ok {
			t.Error("the translated body still names a model: Vertex takes it from the path, and a body naming another one is a second answer to a question the path has already answered")
		}
		if got, want := doc["anthropic_version"], vertexAnthropicVersion; got != want {
			t.Errorf("anthropic_version = %v, want %q: the version of the publisher wire is this translation's own fact", got, want)
		}
		verbatim(t, doc, "max_tokens", float64(16))
		verbatim(t, doc, "stream", false)
		if raw := string(s.up.Body(0)); !strings.Contains(raw, `"tools":[{"name":"read","description":"read a file","input_schema":{"type":"object"}}]`) {
			t.Errorf("the tools block did not reach Vertex as the agent wrote it: %s", raw)
		}
	})

	t.Run("a streamed call takes the streaming method", func(t *testing.T) {
		s, watch, _ := vertexFixture(t, anthropicfake.StreamOK(testModel, testUsage))
		body := strings.Replace(vertexBodyDoc("wire-stream"), `"stream":false`, `"stream":true`, 1)
		cl := vertexCall(t, MessagesPath, body)
		done, _, _, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done {
			t.Fatal("the streamed call was refused")
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+testVertexSpelling+":streamRawPredict"; got != want {
			t.Errorf("the streamed call went to %q, want %q", got, want)
		}
		if _, ok := vertexSentDoc(t, s.up.Body(0))["model"]; ok {
			t.Error("the streamed body names a model")
		}
	})

	t.Run("a token count names the model in the body and the counting endpoint in the path", func(t *testing.T) {
		s, watch, _ := vertexFixture(t, anthropicfake.MessageOK(testModel, testUsage))
		cl := vertexCall(t, CountTokensPath, vertexBodyDoc("wire-count"))
		done, _, _, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done {
			t.Fatal("the token count was refused")
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+"count-tokens:rawPredict"; got != want {
			t.Errorf("the token count went to %q, want %q", got, want)
		}
		if got, want := vertexSentDoc(t, s.up.Body(0))["model"], testVertexSpelling; got != want {
			t.Errorf("model = %v, want %q: the counting endpoint is the one place of this wire where the model is not in the path", got, want)
		}
	})

	t.Run("nothing of the caller's query is carried", func(t *testing.T) {
		s, watch, _ := vertexFixture(t, anthropicfake.MessageOK(testModel, testUsage))
		cl := vertexCall(t, MessagesPath+"?beta=true&userProject=another-project&key=not-a-key", vertexBodyDoc("wire-query"))
		done, _, _, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done {
			t.Fatal("the call was refused")
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+testVertexSpelling+":rawPredict"; got != want {
			t.Errorf("the call went to %q, want %q: this call is made on the SERVICE's identity, so no query parameter of the caller's — a project to bill it to, a key, or an answer shape — may ride on it", got, want)
		}
	})

	t.Run("the global location is the global host", func(t *testing.T) {
		s, watch, _ := vertexFixture(t, anthropicfake.MessageOK(testModel, testUsage))
		t.Setenv(vertexRegionEnv, "global")
		cl := vertexCall(t, MessagesPath, vertexBodyDoc("wire-global"))
		done, _, _, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done {
			t.Fatal("the call was refused")
		}
		global := "/v1/projects/" + testVertexProject + "/locations/global/publishers/anthropic/models/"
		if got, want := watch.sawURL(t, 0), "https://aiplatform.googleapis.com"+global+testVertexSpelling+":rawPredict"; got != want {
			t.Errorf("the call went to %q, want %q: `global` is the one location whose endpoint is not a regional host", got, want)
		}
	})
}

// vertexSentDoc is the body Vertex received, as the object it is.
func vertexSentDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the body Vertex received is not one JSON object: %v (%s)", err, raw)
	}
	return doc
}

// verbatim holds one member of the translated body to the value the caller
// wrote.
func verbatim(t *testing.T, doc map[string]any, key string, want any) {
	t.Helper()
	if got := doc[key]; got != want {
		t.Errorf("the translated body's %s = %#v, want %#v: a member this translation does not decide for itself reaches Vertex as it was written", key, got, want)
	}
}

// TestVertexIsSettledAsMoney: what Vertex costs a run is a key's cost and no
// more (F56, R23). The answer carries the same tier and credential a key's
// answer carries, so the run's own gateway prices and settles it as real money
// against its caps, and the service's own meter — where a workspace's cap is
// taken — brackets the attempt exactly as it brackets a key's: the room for the
// worst case is taken BEFORE the call goes out, the answer settles it on the
// usage it reported, and the figure the month is charged is the price of the
// model that served.
//
// What is priced is the SERVED model and what is bounded is the body that goes
// out, so the reservation is the translated body's own worst case and not the
// caller's — the one place where "the call the workspace pays for" and "the
// call the service received" are different bytes.
func TestVertexIsSettledAsMoney(t *testing.T) {
	vertexServiceIdentity(t)
	watch := &vertexWatch{}
	f := newWSMeterSvc(t, wsCapUSD, nil, func(_ *svc, cfg *Config) { wrapVertexClient(cfg, watch) })
	f.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	// The workspace's scope, which is the only thing that puts a meter over the
	// call: the member the call is spent for and the workspace's own month
	// (WS8, R15 — an installation's scope meters nothing).
	ctx := withScope(context.Background(), wsMeterScope{
		installationScope: installationScope{s: f.server},
		meter:             f.meter,
	})
	cl := vertexCall(t, MessagesPath, vertexBodyDoc("vertex-money"))
	done, _, rec, _ := serveVertexCallIn(t, f.svc, ctx, vertexIdentity(f.svc, wsMemberA), cl, testVertexSource())
	if !done || rec.Code != http.StatusOK {
		t.Fatalf("the call is answered %d (%v), want it served", rec.Code, done)
	}
	if got := rec.Header().Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q: this is the fact the run's gateway settles money by", HeaderTier, got, TierB)
	}
	if got := rec.Header().Get(HeaderCredential); got != config.CredentialVertex {
		t.Errorf("%s = %q, want %q", HeaderCredential, got, config.CredentialVertex)
	}
	if n := f.up.Count(); n != 1 {
		t.Fatalf("Vertex saw %d requests, want 1", n)
	}

	sent := f.up.Body(0)
	want := wsRow(t, testModel).WorstCase(pricing.Request{BodyBytes: int64(len(sent)), MaxTokens: 16})
	if got := f.watch.reservedAt(t, 0); got.Reserved != want {
		t.Errorf("at the call the workspace held %v of room, want the translated body's own worst case %v: the room is taken before the request goes out, and what it bounds is what goes out",
			got.Reserved.USD(), want.USD())
	}
	room := f.wsRoom(t)
	if want := wsRow(t, testModel).Rates.Cost(testUsage); room.Settled != want {
		t.Errorf("the workspace settled %v, want %v (the usage the answer reported, at the served model's price)", room.Settled.USD(), want.USD())
	}
	if room.Reserved != 0 {
		t.Errorf("the workspace still holds %v of room, want none: the call settled", room.Reserved.USD())
	}
}

// TestAnUnadmittedRepositoryGetsNoVertex: a repository the source does not
// admit is never served the project's Vertex (D3182, and the fail-closed
// direction of an EMPTY list: it admits nobody).
//
// The source declines the call and writes NOTHING — no audit line, no refusal —
// because the call is refused by the walk that could not place it, and the
// contract's rule is exactly one line for that call (C1.13). A line per skipped
// source would be one line per source for a single refused call.
//
// Mutation (run, restore): return done=true, or write the refusal here, and the
// counts below are red.
func TestAnUnadmittedRepositoryGetsNoVertex(t *testing.T) {
	for _, tc := range []struct {
		name  string
		admit []string
	}{
		{"another repository", []string{testOtherRepo}},
		{"an empty list, which admits nobody", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := vertexFixture(t, anthropicfake.MessageOK(testModel, testUsage))
			cl := vertexCall(t, MessagesPath, vertexBodyDoc("unadmitted"))
			src := routing.Source{Kind: routing.KindVertex, Admit: tc.admit}
			done, tried, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, src)
			if done {
				t.Fatalf("the source answered a call for a repository it does not admit: %d %s", rec.Code, rec.Body)
			}
			if want := (Tried{Name: config.CredentialVertex, Tier: TierB, Reason: ReasonNotAdmitted}); tried != want {
				t.Errorf("the row beside the declined call = %+v, want %+v", tried, want)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("Vertex saw %d requests, want none: an unadmitted repository is never served it", n)
			}
			if got := s.audit.String(); got != "" {
				t.Errorf("the declined source wrote %q, want nothing: the walk writes the one line this call is refused with", got)
			}
		})
	}
}

// TestAVertexLimitMovesOn: a 429 from Vertex is a limit on the project's Vertex
// — the service's own identity, which every admitted repository of the
// installation shares — so it marks the source and hands the walk a row to move
// on with. Nothing was streamed, and the attempt is settled at the zero a 429 is
// worth rather than eating the workspace's month.
//
// The reset the answer reported is the mark's own: a Vertex 429 that names its
// reset must not be recorded as a wait somebody invented.
//
// Mutation (run, restore): return done=true, or skip the mark, and the row, the
// store and the upstream count below are red one by one.
func TestAVertexLimitMovesOn(t *testing.T) {
	reply := anthropicfake.Error(http.StatusTooManyRequests, "rate_limit_error", "slow down")
	reply.Header = http.Header{"Retry-After": []string{"120"}}
	s, _, _ := vertexFixture(t, reply)

	cl := vertexCall(t, MessagesPath, vertexBodyDoc("vertex-429"))
	done, tried, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
	if done {
		t.Fatalf("the source answered a call Vertex refused with a limit: %d %s", rec.Code, rec.Body)
	}
	if want := (Tried{Name: config.CredentialVertex, Tier: TierB, Reason: ReasonExhausted}); tried != want {
		t.Errorf("the row beside the declined call = %+v, want %+v: the chain moves on, or the walk answers exhausted", tried, want)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("Vertex saw %d requests, want the one that was limited", n)
	}
	m, ok := vertexMark(t, s)
	if !ok || m.State != creds.StateExhausted {
		t.Fatalf("the mark Vertex left is %+v (found %v), want an exhausted one", m, ok)
	}
	if want := s.now.Add(120 * time.Second); !m.Until.Equal(want) {
		t.Errorf("the mark holds until %v, want the reset the answer reported (%v)", m.Until, want)
	}
	if got := s.audit.String(); got != "" {
		t.Errorf("the declined source wrote %q, want nothing: the walk writes the line for a call it could not place", got)
	}
}

// TestAVertex401MarksTheSourceBroken: a 401 is the one mark only an operator
// clears (C1.21). The service's own identity refused is not something a retry
// or a wait answers, so the source is marked broken and the walk moves on —
// exactly as a key's 401 does.
func TestAVertex401MarksTheSourceBroken(t *testing.T) {
	s, _, _ := vertexFixture(t, anthropicfake.Error(http.StatusUnauthorized, "authentication_error", "invalid credentials"))

	cl := vertexCall(t, MessagesPath, vertexBodyDoc("vertex-401"))
	done, tried, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
	if done {
		t.Fatalf("the source answered a call Vertex refused: %d %s", rec.Code, rec.Body)
	}
	if want := (Tried{Name: config.CredentialVertex, Tier: TierB, Reason: ReasonBroken}); tried != want {
		t.Errorf("the row beside the declined call = %+v, want %+v", tried, want)
	}
	m, ok := vertexMark(t, s)
	if !ok || !m.Broken() {
		t.Fatalf("the mark Vertex left is %+v (found %v), want a broken one", m, ok)
	}
	if got := s.audit.String(); got != "" {
		t.Errorf("the declined source wrote %q, want nothing: the walk writes the line for a call it could not place", got)
	}
}

// TestVertexRefusalsWriteOneAuditLineAndNoValue: every refusal this source makes
// itself — the ones the walk never sees, because the call is answered here —
// writes exactly one audit line, and that line carries no value: not the
// caller's own token, not the credential the service signs with, and nothing of
// the body it refused.
//
// The two cases are the two refusals that are this file's own work rather than
// Vertex's answer: a deployment whose environment names no project or region
// (this service cannot know where the project's Vertex is), and a model the
// checked-in map does not spell for Vertex (D3175: a source is never sent a
// model nobody measured). Both are answered, not retried: the chain has no
// second source to move to when the source it was given cannot be built.
func TestVertexRefusalsWriteOneAuditLineAndNoValue(t *testing.T) {
	t.Run("the deployment names no project or region", func(t *testing.T) {
		caller := fakeCred(t, "caller-run")
		s, auditLog := refusalSvc(t, nil)
		t.Setenv(vertexRegionEnv, "")
		cl := vertexCall(t, MessagesPath, vertexBodyDoc("no-env"), caller)

		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != StatusUnavailable {
			t.Fatalf("the call is answered %d (done=%v), want the service's own refusal %d", rec.Code, done, StatusUnavailable)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("Vertex saw %d requests, want none: the service does not know where to reach it", n)
		}
		oneRefusal(t, auditLog, caller)
	})

	t.Run("the map spells the model for no other source", func(t *testing.T) {
		caller := fakeCred(t, "caller-run")
		token := vertexServiceIdentity(t)
		s, auditLog := refusalSvc(t, nil)
		pricing.SpellingFor = func(string, string) (string, bool) { return "", false }
		cl := vertexCall(t, MessagesPath, vertexBodyDoc("no-spelling"), caller)

		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != StatusUnavailable {
			t.Fatalf("the call is answered %d (done=%v), want a refusal: a model the map does not spell for Vertex is refused rather than sent as something nobody measured", rec.Code, done)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("Vertex saw %d requests, want none", n)
		}
		oneRefusal(t, auditLog, caller, token)
	})
}

// oneRefusal holds a refused call to its one audit line and to the no-value
// rule: exactly one line, the service's own status, and none of the values the
// test planted anywhere in it.
func oneRefusal(t *testing.T, auditLog *logBuf, values ...string) {
	t.Helper()
	lines := auditLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the audit holds %d lines, want exactly 1: %v", len(lines), lines)
	}
	if got := lines[0]["status"]; got != ErrorStateUnavailable && got != ErrorNoCandidateCredentials {
		t.Errorf("the line's status is %v, want the refusal's own wire type", got)
	}
	raw := auditLog.String()
	for _, v := range values {
		if v != "" && strings.Contains(raw, v) {
			t.Errorf("the audit line carries a value it must never carry: %q", raw)
		}
	}
}

// TestVertexTokenSourceFailureIsRefused: a service that cannot reach its own
// identity refuses the call rather than serving it with nothing — the sentence
// is the service's own, exactly one audit line is written, and Vertex is never
// asked.
func TestVertexTokenSourceFailureIsRefused(t *testing.T) {
	sentinel := fakeCred(t, "metadata")
	s, auditLog := refusalSvc(t, nil)
	t.Setenv(vertexProjectEnv, testVertexProject)
	t.Setenv(vertexRegionEnv, testVertexRegion)
	spell := pricing.SpellingFor
	pricing.SpellingFor = func(kind, model string) (string, bool) {
		return testVertexSpelling, kind == string(routing.KindVertex) && model == testModel
	}
	t.Cleanup(func() { pricing.SpellingFor = spell })
	sign := vertexTokenFor
	vertexTokenFor = func(context.Context) (oauth2.TokenSource, error) {
		return nil, errors.New("the metadata server said " + sentinel)
	}
	t.Cleanup(func() { vertexTokenFor = sign })

	cl := vertexCall(t, MessagesPath, vertexBodyDoc("no-identity"))
	done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
	if !done || rec.Code != StatusUnavailable {
		t.Fatalf("the call is answered %d (done=%v), want %d", rec.Code, done, StatusUnavailable)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("Vertex saw %d requests, want none", n)
	}
	oneRefusal(t, auditLog, sentinel)
}
