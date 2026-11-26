package e2e

// The project secret path end to end (I1 of docs/secrets-brokering/image-secrets):
// one repository's job secret, from the owner's allow to the value an agent
// reads, with every leg the shipped code rather than a stand-in for it.
//
// Three runs of the sandbox's own repository, all hermetic:
//
//   - TestProjectSecretFetchedEndToEnd: the installation's own converge copies
//     the name into the `fugaro_p_` grammar and records it (IS5), `fugaro update
//     --repo` writes the version-1 record, and the run's fetch reads the
//     grammar's copy -- the value lands in the file the declaration asked for
//     and the agent stage sees the path.
//   - TestUnlistedRepositoryRefusedEndToEnd: the record admits another
//     repository, so version 2 refuses, version 1 has no record to read, and the
//     run stops before any agent call.
//   - TestUncopiedNameFallsBackToV1EndToEnd: the local block admits this
//     repository but the name was never copied -- no `fugaro_p_` container --
//     so version 2 refuses and the version-1 record, which points at the
//     project copy, answers.
//
// What is real here and what is a stand-in, so a reader can tell them apart:
// the CLI legs are the shipped binary against the gcpfakes, the route is the
// shipped credsvc.NewServer over the shipped record readers (RTDBWorkflowSecrets
// and RTDBProjectSecrets), the runner leg is the shipped runner.Run against a
// scripted agent, and the caller's identity is answered whole by a verifier
// whose shape is IS2's (the fakes sign nothing a real verifier would verify).
//
// The migration's copy (IS5) runs too, at the one door it runs at: the
// installation converge's tail (psRig.migrate). What stands in for the
// operator's own Google account there is its two answers -- a metadata
// stand-in for the token ADC resolves to, and a proxy for the HTTPS calls the
// hermetic environment has no host for: who the credentials belong to, and the
// budget database at the address a real installation names.
//
// The copy's own value is the project copy's -- that is what it moves -- so the
// two copies are told apart by a version newer than the copy's written into the
// grammar before the run: what an operator's `fugaro secrets set` does to that
// same container after a migration, and the only value this file writes
// itself.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The sandbox's own repository, workflow and secret: the fixture every comment
// below speaks, and the one the two CLI legs and the run leg must agree on for
// the slug the record is keyed by to be the slug the run carries.
const (
	psRepoName   = "acme/sandbox"
	psOrigin     = "https://bitbucket.org/acme/sandbox.git"
	psWorkflow   = "web"
	psSecretName = "sandbox-probe"
	psSecretEnv  = "SANDBOX_PROBE"
	// psAccount is the job's own service account: what the service verifies a
	// call by, and what the record's admission is keyed to.
	psAccount = "run-job@proj-1234.iam.gserviceaccount.com"
	// The two copies' values. They are different so an answer read as the other
	// copy's shows, and they are values only: no test, doc or log line here
	// carries one except the two comparisons that have to.
	psGrammarValue = "grammar-value-2f9d"
	psCopyValue    = "project-copy-value-71b4"
	// psRB is the refresh identity the budget session's token carries.
	psRB = "dev@example.invalid"
	// psFirebaseProject is the Firebase project the fake signer and identity
	// toolkit live in.
	psFirebaseProject = "aurora-fp"
	// psOperatorEmail is the person the migration's copy runs as: the account
	// the identity stand-in answers with, and the who the copied container
	// records. Never a real address.
	psOperatorEmail = "owner@example.invalid"
	// psBudgetHost is the installation's database as a real one names it --
	// <its firebase project>-default-rtdb -- which is the URL a config without
	// endpoints: no_auth must carry (rtdb.ValidateURL) and the one the
	// migration's proxy forwards to the fake.
	psBudgetHost = initRepoProject + "-default-rtdb.firebaseio.com"
	// psBudgetMode is how this run is accounted for: observe, so the caps the
	// adds below seed are read and no lease has to be granted for a scripted
	// stage that makes no model call.
	psBudgetMode = "observe"
	// psHeartbeat is the session's cadence, short so the run's own beats do not
	// outlive it.
	psHeartbeat = 30 * time.Millisecond
)

// psSlug is the storage slug every leg of this file names the repository by:
// the real task.Slug, never a readable placeholder, because the run's admission
// derives the provider kind back out of the slug it was handed.
var psSlug = func() string {
	s, err := task.Slug("bitbucket", psRepoName)
	if err != nil {
		panic(err)
	}
	return s
}()

// psCopyID is the project-wide container `fugaro update` recorded for the name
// under D2313: the copy a version-1 fetch reads, and the source IS5's migration
// copies into the grammar.
func psCopyID() string { return gcp.ProjectSecretID(initRepoProjectName, psSecretName) }

// psGrammarID is the name's grammar container, which is what version 2 reads
// (secretname.Project, the one place that spells one).
func psGrammarID() string {
	id, err := secretname.Project(psSecretName)
	if err != nil {
		panic(err)
	}
	return id
}

// psCanonical is the repository as the service's pairing spells it: the
// provider segment and then owner/name (projectpolicy.Canonical).
const psCanonical = "bitbucket/" + psRepoName

// psJob is the Cloud Run job this repository's web workflow runs in.
func psJob() string { return gcp.JobName(psSlug, gcp.WorkflowVariant(psWorkflow, "")) }

// psExecution is the platform's own report of the execution, which is the only
// thing a run reads its job name from.
func psExecution() string {
	return "projects/" + psFirebaseProject + "/locations/europe-west1/jobs/" + psJob() + "/executions/" + psJob() + "-aaaaa"
}

// psRig is the installation the three runs below live in: the update rig's
// sandbox installation, plus the shared backend, the project's copy of the
// name, and the credentials service the route is served by.
type psRig struct {
	*initRepoRig
	budget *testutil.Budget
	db     *gcpfake.RTDB
	// client is the fake database's client, as the service and the record
	// readers reach it.
	client *rtdb.Client
	// svc is the credentials service's address as the run's job carries it: the
	// shipped workflow-secrets route, and nothing else.
	svc  *httptest.Server
	meta *httptest.Server

	mu    sync.Mutex
	calls []psCall
}

// newPSRig sets the installation up. allow is the repositories the local
// config's workflow_secrets block admits, which is what the plan mounts the
// project's copy for: a test whose record admits nobody else passes the sandbox
// itself, and the refusal test passes another repository.
func newPSRig(t *testing.T, allow ...string) *psRig {
	t.Helper()
	r := &psRig{
		initRepoRig: newInitRepoRig(t, psRepoName, psOrigin, nil),
		budget:      testutil.NewBudget(t),
	}
	r.db = r.budget.DB
	// The local config gains a backend (the record and the run's session are
	// nodes of it) and the block the owner's allow writes into: `secrets allow`
	// reaches a name only when the local config already holds the entry.
	// The block names the Firebase project too: the local record's writer refuses
	// a database it cannot tell is this project's (requireOwnBudgetDB).
	//
	// It states a budget mode, the models and the routing for one reason: the
	// migration's leg is the installation door, which publishes the project's
	// policy node out of this config, and that door refuses a config setting no
	// budget.mode at all and one that states no routing while the node carries
	// a cascade. The mode, the models and the route are the ones seedBackend's
	// node holds, and the per-run cap is this rig's own 20 dollars -- a cap is
	// what a config in `enforce` has to name.
	r.cfgText += "budget: { firebase_api_key: " + r.budget.APIKey + ", rtdb_url: " + r.db.URL +
		", token_signer: " + testutil.BudgetSigner + ", firebase_project: " + initRepoProject + ", mode: enforce, per_run_usd: 20, allowed_models: [claude-haiku-4-5, claude-sonnet-5-5] }\n" +
		"routing: { claude: { via: cascade } }\n" +
		"workflow_secrets:\n  " + psSecretName + ":\n    allow_data_to:" + psAllowList(allow)
	if err := os.WriteFile(r.cfg, []byte(r.cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	r.respec(t, slices.Contains(allow, psRepoName))
	r.seedBackend(t)
	// The name's project-wide copy, as an installation that ever stored one has
	// it (readiness reads it back to decide the version-1 record's container).
	r.sm.Seed(psCopyID(), withLabels(managedLabels,
		gcp.LabelProject, initRepoProjectName, gcp.LabelSecret, psSecretName), []byte(psCopyValue))
	r.bootstrap(t)
	// The repository's own copy of the name is disabled: a name the operator
	// stored project-wide is one the repository holds no copy of (D2313), and
	// Readiness asks the repository's copy first -- an enabled version there
	// answers for the name and the project's copy is never reached.
	r.sm.DisableVersions(gcp.SecretID(psSlug, psSecretName))
	r.serve(t)
	return r
}

// psAllowList is allow as the block's flow-style list.
func psAllowList(allow []string) string {
	if len(allow) == 0 {
		return " []"
	}
	return " [" + strings.Join(allow, ", ") + "]"
}

// respec recomputes the spec the way newInitRepoRig's constructor did, on the
// config this rig left on disk: the allow-list decides which project secrets
// the plan mounts, so the spec is not the one the bare sandbox computed.
func (r *psRig) respec(t *testing.T, admitted bool) {
	t.Helper()
	lc, err := localcfg.Parse([]byte(r.cfgText))
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(r.checkout, "fugaro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse(text)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	r.spec, err = infra.Repo(infra.Inputs{LC: lc, Repo: psRepoName, Cfg: cfg, RepoURL: psOrigin})
	if err != nil {
		t.Fatal(err)
	}
	// The candidate is the plan's half of the decision (the container itself is
	// resolved against live state at Readiness, which is not what these tests
	// drive): a repository the block does not admit has no candidate at all.
	id, ok := r.spec.ProjectSecretCandidates[psSecretName]
	switch {
	case admitted && (!ok || id != psCopyID()):
		t.Fatalf("the plan mounts %q for %s, want the project copy %s", id, psSecretName, psCopyID())
	case !admitted && ok:
		t.Fatalf("the plan mounts %q for %s, and the block admits another repository", id, psSecretName)
	}
}

// seedBackend is the database every reader and writer of it asks about first:
// the project it belongs to, the mark that says it is ours, the caps a session
// opens against, and the published policy a switched run is admitted by.
func (r *psRig) seedBackend(t *testing.T) {
	t.Helper()
	r.db.Set("fugaro/project", initRepoProjectName)
	r.db.Set(budget.PathMark, json.RawMessage(`{"managed_by":"fugaro","project":"`+initRepoProjectName+
		`","gcp_project":"`+initRepoProject+`","version":1}`))
	r.db.Set(budget.PathMode, psBudgetMode)
	micros := func(v budget.Micros) *budget.Micros { return &v }
	r.db.Set(budget.PathLimits, budget.Limits{MaxReserveMicros: micros(5_000_000)})
	r.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: micros(150_000_000), PerRunMicros: micros(20_000_000)})
	r.db.Set(budget.PathCapsRepo(psSlug), budget.RepoCaps{DailyMicros: micros(60_000_000), PerRunMicros: micros(20_000_000), Repo: psRepoName})
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	r.db.Set(projectpolicy.PathShared(), json.RawMessage(fmt.Sprintf(
		`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"budget":{"mode":"enforce","allowed_models":["claude-haiku-4-5","claude-sonnet-5-5"]}}`,
		projectpolicy.Version)))
}

// serve stands the two addresses the run's job carries up: the credentials
// service, whose workflow-secrets path is the shipped route (every other path is
// the 404 nothing here sends), and the metadata server, which answers the job's
// own Google ID token to everything.
func (r *psRig) serve(t *testing.T) {
	t.Helper()
	client, err := rtdb.New(r.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	r.client = client
	route, err := credsvc.NewServer(credsvc.Config{
		Auth: psVerifier{},
		// The containers: the value of one, and the marker the intake writes on
		// a `fugaro_p_` one. The bundle document is empty -- this installation
		// holds no model credential these runs read.
		Bundles:         &credsvc.BundleLoader{Source: psSource{sm: r.sm}},
		Policy:          psPolicy{},
		State:           &creds.RTDBStore{DB: client},
		Kills:           psKills{},
		DB:              client,
		Probes:          client,
		Slips:           &creds.RTDBStore{DB: client},
		WorkflowSecrets: credsvc.RTDBWorkflowSecrets{DB: client},
		ProjectSecrets:  credsvc.RTDBProjectSecrets{DB: client},
		AuditLog:        slog.NewJSONHandler(io.Discard, nil),
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		// The row's Repo is the CANONICAL repository (projectpolicy.Canonical),
		// which is what the pairing hands the fence and what ownerName strips
		// the provider segment off: a slug here is a repository the record's
		// owner/name patterns can never match (installationRepos).
		JobAccounts: []credsvc.JobAccount{{
			Email: psAccount, Slug: psSlug, Repo: psCanonical, Workflow: psWorkflow,
		}},
	})
	if err != nil {
		t.Fatalf("the workflow-secrets route would not start: %v", err)
	}
	r.svc = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != credsvc.WorkflowSecretsPath {
			http.Error(w, "this installation serves no other route to these runs", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		rec := httptest.NewRecorder()
		route.ServeHTTP(rec, req)
		var call psCall
		var wire credsvc.WorkflowSecretsRequest
		_ = json.Unmarshal(body, &wire)
		call.name, call.env = wire.Name, wire.Env
		call.version = wire.VersionOf()
		call.status = rec.Code
		if e, err := credsvc.ReadWireError(rec.Code, rec.Body.Bytes()); err == nil {
			call.code = e.Error
		}
		r.mu.Lock()
		r.calls = append(r.calls, call)
		r.mu.Unlock()
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(r.svc.Close)
	r.meta = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(r.meta.Close)
}

// psCall is one call the route answered, as the runner's two questions and the
// answer to each.
type psCall struct {
	name    string
	env     string
	version int
	status  int
	code    string
}

// callsOf is every call the service received, in order.
func (r *psRig) callsOf() []psCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]psCall(nil), r.calls...)
}

// psOperator is the operator's own Google account, as much of it as the
// migration's copy needs: a token, and who it belongs to.
//
// The copy asks two things of the outer world that the hermetic environment
// takes away, and this stands up exactly those two. The token: ADC is a path
// that does not exist, so the token source falls through to the metadata
// server, which meta answers. Who it belongs to:
// openidconnect.googleapis.com/v1/userinfo, over HTTPS -- Google's real
// address, which no configuration can point elsewhere -- so the child's HTTPS
// traffic goes to the proxy below, which answers that one address and forwards
// the budget database's. Everything else the child reaches is loopback (the
// fakes, the metadata stand-in), and Go's proxy settings never send a loopback
// address to a proxy.
type psOperator struct {
	// ca is the file the child trusts, and leaf is what the proxy serves: an
	// address the child asks for is one it can verify.
	ca   string
	leaf tls.Certificate
	// proxy and meta are the two addresses the child's environment names.
	proxy, meta string

	mu   sync.Mutex
	seen map[string]int
}

// newPSOperator stands the operator's account up for one migration: dbURL is
// what the installation's database stands for.
func newPSOperator(t *testing.T, dbURL string) *psOperator {
	t.Helper()
	o := &psOperator{seen: map[string]int{}}
	dir := t.TempDir()
	o.ca = filepath.Join(dir, "ca.pem")
	caPEM, leaf := psCertificate(t, psUserinfoHost, psBudgetHost)
	o.leaf = leaf
	if err := os.WriteFile(o.ca, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The one answer a token source asks the metadata server for. The
		// project-id read that comes with it is answered the same way: the
		// client wants a token and reads no field of the other answer.
		w.Header().Set("Metadata-Flavor", "Google")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"ya29.operator","expires_in":3600,"token_type":"Bearer"}`)
	}))
	t.Cleanup(meta.Close)
	o.meta = strings.TrimPrefix(meta.URL, "http://")

	// The database, forwarded as it is: the fake serves the same paths the real
	// one does, and the client streams, so nothing here may buffer an answer.
	budget := httputil.NewSingleHostReverseProxy(mustURL(t, dbURL))
	budget.FlushInterval = -1
	userinfo := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/userinfo" {
			http.Error(w, "this stand-in answers one address", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"`+psOperatorEmail+`","email_verified":true}`)
	})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host := req.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		o.mu.Lock()
		o.seen[host]++
		o.mu.Unlock()
		inner := http.Handler(nil)
		switch host {
		case psBudgetHost:
			inner = budget
		case psUserinfoHost:
			inner = userinfo
		}
		if req.Method != http.MethodConnect || inner == nil {
			http.Error(w, "the operator's stand-in answers no call to "+req.Host, http.StatusBadGateway)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijacking the proxy's connection: %v", err)
			return
		}
		// The response is written raw: a CONNECT answer is not a response a
		// ResponseWriter may build (the tunnel has no body of its own).
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			conn.Close()
			return
		}
		go func() {
			defer conn.Close()
			tlsConn := tls.Server(psBuffered{Conn: conn, r: rw.Reader}, &tls.Config{Certificates: []tls.Certificate{o.leaf}})
			done := make(chan struct{})
			var once sync.Once
			srv := &http.Server{Handler: inner}
			// The server says when the connection is done, and only then may
			// this listener answer that there are no more.
			srv.ConnState = func(_ net.Conn, st http.ConnState) {
				if st == http.StateClosed || st == http.StateHijacked {
					once.Do(func() { close(done) })
				}
			}
			_ = srv.Serve(&psTunnel{conn: tlsConn, done: done})
		}()
	}))
	t.Cleanup(proxy.Close)
	o.proxy = proxy.URL
	return o
}

// env is what the child's environment gains over the hermetic one: the
// account's two answers, and the certificate to trust them with. The hermetic
// environment's own values are the last word for a child, so these come after
// them.
func (o *psOperator) env() []string {
	return []string{
		// Empty is unset for the ADC lookup, which is what sends the token
		// source to the metadata server.
		"GOOGLE_APPLICATION_CREDENTIALS=",
		"GCE_METADATA_HOST=" + o.meta,
		"HTTPS_PROXY=" + o.proxy, "https_proxy=" + o.proxy,
		"SSL_CERT_FILE=" + o.ca,
		"NO_PROXY=", "no_proxy=",
	}
}

// hosts is every address the child asked the proxy for, in no order.
func (o *psOperator) hosts() map[string]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return maps.Clone(o.seen)
}

// psUserinfoHost is Google's OpenID Connect userinfo address, spelled here
// because it is what the child asks for: internal/cli's own constant is
// unexported and this is a host, not a decision.
const psUserinfoHost = "openidconnect.googleapis.com"

// psTunnel is one connection served as a listener: the proxy runs an HTTP
// server on a connection it hijacked, and a server wants something to accept
// from. The connection is handed over once, and the second Accept is not
// answered until the server says it is done with it: a listener that answers
// "no more connections" while one is still being served makes that server close
// the connection under itself, which is a tunnel that dies mid-handshake.
type psTunnel struct {
	conn net.Conn
	done chan struct{}
	used bool
}

func (l *psTunnel) Accept() (net.Conn, error) {
	if l.used {
		<-l.done
		return nil, io.EOF
	}
	l.used = true
	return l.conn, nil
}

func (l *psTunnel) Close() error   { return l.conn.Close() }
func (l *psTunnel) Addr() net.Addr { return l.conn.LocalAddr() }

// psBuffered is the hijacked connection with whatever the server already read
// in front of it: a client whose handshake came in with its CONNECT is not left
// waiting on bytes nobody reads again.
type psBuffered struct {
	net.Conn
	r *bufio.Reader
}

func (c psBuffered) Read(b []byte) (int, error) { return c.r.Read(b) }

// psCertificate makes a certificate authority and one certificate for hosts,
// signed by it: what the proxy serves, and what the child trusts (SSL_CERT_FILE
// is the file the CA is written to). A test's own certificate, never a real
// one.
func psCertificate(t *testing.T, hosts ...string) (caPEM []byte, leaf tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fugaro e2e operator stand-in"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: hosts[0]},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		DNSNames: hosts, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
}

// mustURL is url.Parse for a URL a test wrote itself.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("%q is not a URL: %v", raw, err)
	}
	return u
}

// psSource is the containers the route reads: one value, and the marker a
// `fugaro_p_` container carries (F18, G2). It is a stand-in for the Secret
// Manager reader and not for the contract: gcpfake's Secrets cannot serve the
// name-filtered list that reader's marker check asks for, and the value and the
// marker are the whole of what the route decides on.
type psSource struct{ sm *gcpfake.Secrets }

func (psSource) Latest(context.Context) ([]byte, error) { return []byte("{}"), nil }

func (s psSource) Value(_ context.Context, secretID string) (string, bool, error) {
	value := s.sm.Latest(secretID)
	if value == nil {
		return "", false, nil
	}
	return string(value), true, nil
}

func (s psSource) Marked(_ context.Context, secretID string) (bool, error) {
	return s.sm.Labels(secretID)[intake.LabelWorkflowSecret] == intake.WorkflowSecretMarker, nil
}

// psVerifier is the service's front door for this rig: the identity the runner
// presents is the job's own, so it is answered whole rather than read out of a
// token the fakes cannot sign (the fake signer is an HMAC and the shipped
// authenticator verifies a Google key set).
type psVerifier struct{}

func (psVerifier) Verify(context.Context, credsvc.Request) (credsvc.Identity, error) {
	return credsvc.Identity{
		Slug: psSlug, Run: runID, Project: initRepoProjectName, Job: psJob(),
		JobAccount: psAccount, JobClass: credsvc.ClassRepository,
	}, nil
}

var errPSNoClass = errors.New("this rig's credentials service serves no caller of that class")

func (psVerifier) VerifyVerifier(context.Context, string) (string, error) { return "", errPSNoClass }
func (psVerifier) VerifyReadCaller(context.Context, string) (string, error) {
	return "", errPSNoClass
}
func (psVerifier) VerifyPersonCaller(context.Context, string) (string, error) {
	return "", errPSNoClass
}
func (psVerifier) VerifyOrchestrator(context.Context, string) (string, error) {
	return "", errPSNoClass
}
func (psVerifier) VerifyLauncher(context.Context, string) (credsvc.LauncherCaller, error) {
	return credsvc.LauncherCaller{}, errPSNoClass
}
func (psVerifier) VerifyBuildCaller(context.Context, string) (string, error) {
	return "", errPSNoClass
}
func (psVerifier) VerifyRepoBuild(context.Context, string) (credsvc.RepoBuildAccount, error) {
	return credsvc.RepoBuildAccount{}, errPSNoClass
}

// psPolicy is the service's own reader of the published node, which the
// workflow-secrets route never asks: its gate is the account's class, the
// record and the container (workflowsecrets.go). The node the RUN reads is the
// database's, seeded by seedBackend, and read by the runner's own policy read.
type psPolicy struct{}

func (psPolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) { return nil, false, nil }
func (psPolicy) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, nil
}

// psKills is the operator's stop switches, both off.
type psKills struct{}

func (psKills) Stop(context.Context, string) (bool, string, error) { return false, "", nil }

// cli runs one subcommand of the shipped binary at a person's terminal -- no
// coding agent's markers, which the allow and the converge are both refused in
// -- against this rig's local config and fakes.
func (r *psRig) cli(t *testing.T, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := os.Environ()
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"} {
		env = append(env, k+"=")
	}
	cmd := exec.CommandContext(ctx, r.fugaro, args...)
	// The checkout names this rig's project, which is how the local config is
	// found at all (the fixture's own name is the sandbox's).
	cmd.Dir = r.checkout
	cmd.Env = hermeticEnv(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return res
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		res.code = xe.ExitCode()
		return res
	}
	t.Fatalf("running %v: %v", args, err)
	return res
}

// allow is the owner's own step: `fugaro secrets allow` writes the name's entry
// into the project record, which is the list a version-2 fetch is admitted by
// (IS3, D3328, D3333).
func (r *psRig) allow(t *testing.T, repos ...string) {
	t.Helper()
	res := r.cli(t, append([]string{"secrets", "allow", psSecretName}, repos...)...)
	if res.code != 0 {
		t.Fatalf("secrets allow %s %v exited %d:\n%s\n%s", psSecretName, repos, res.code, res.stdout, res.stderr)
	}
}

// converge is `fugaro update --repo <checkout>`, the door the repository's own
// root and the version-1 record are written by.
func (r *psRig) converge(t *testing.T) cliResult {
	t.Helper()
	// At a terminal, with nothing typed: the confirmation of a step is the
	// person being there (T7, docs/no-prompt-policy).
	res := r.fugaroInitTyped(t, "", "--repo", r.checkout, "--no-build", "--onboard-repo", psRepoName)
	if res.code != 0 {
		t.Fatalf("update --repo exited %d:\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	return res
}

// migrate is the installation door: the one door IS5's copy runs at (the
// converge's tail, D3325), and the only leg of this file that reaches anything
// outside the fakes.
//
// It runs against its own local config, the one a real installation has: the
// same file without endpoints: no_auth, and with its database at the address a
// real one names. The rest of the rig keeps the config it had, because the
// fakes are reached over plain loopback and a budget write that needs the
// operator's own credentials has no business in the legs that are not about it.
//
// The project's name is typed: the publish of the policy node this config
// states is confirmed by typing it, and this door has no flag that stands in
// (T7, docs/no-prompt-policy).
func (r *psRig) migrate(t *testing.T) cliResult {
	t.Helper()
	op := newPSOperator(t, r.db.URL)
	// The migration reads the project copy's value, which no other leg here
	// reads: the fakes refuse a value read to everything but a caller a test
	// said may make one.
	r.sm.AllowAccess()

	alt := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", "operator.yaml")
	text := strings.Replace(r.cfgText, "no_auth: true", "no_auth: false", 1)
	text = strings.Replace(text, "rtdb_url: "+r.db.URL, "rtdb_url: https://"+psBudgetHost, 1)
	if strings.Contains(text, "no_auth: true") || !strings.Contains(text, psBudgetHost) {
		t.Fatalf("the rig's config no longer holds what the operator's own config replaces:\n%s", r.cfgText)
	}
	if err := os.WriteFile(alt, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", alt)
	res := r.migrateIn(t, op.env(), initRepoProjectName+"\n")
	// The legs after this one are not the operator's: they read the rig's own
	// config, found by the checkout's project, as they did before.
	t.Setenv("FUGARO_CONFIG", "")
	if res.code != 0 {
		t.Fatalf("the installation door exited %d:\n%s", res.code, res)
	}
	if got := op.hosts(); got[psBudgetHost] == 0 || got[psUserinfoHost] == 0 {
		t.Errorf("the operator's own account was asked for %v, want the database at %s and the identity at %s", got, psBudgetHost, psUserinfoHost)
	}
	if !strings.Contains(res.stdout, "1 copied into the "+secretname.ProjectPrefix+" grammar") {
		t.Errorf("the door's own summary does not say it copied the name:\n%s", res.stdout)
	}
	return res
}

// migrateIn runs the installation door at a terminal, with the environment the
// operator's own account needs. It is fugaroInitIn's recipe with a seam: the
// hermetic environment's values are the last word for a child, so a run that
// has to reach Google builds the environment itself rather than layering on
// one that leaves it no room.
func (r *psRig) migrateIn(t *testing.T, extra []string, typed string, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := os.Environ()
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"} {
		env = append(env, k+"=")
	}
	cmd := exec.CommandContext(ctx, r.fugaro, append([]string{convergeCmd}, args...)...)
	cmd.Dir = r.dir
	cmd.Env = append(hermeticEnv(env), extra...)
	master, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer master.Close()
	defer tty.Close()
	go func() { _, _ = io.Copy(io.Discard, master) }() // the terminal's echo
	if _, err := master.WriteString(typed); err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = tty
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	var xe *exec.ExitError
	switch {
	case errors.As(err, &xe):
		res.code = xe.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return res
}

// grammarContainer reads the name's `fugaro_p_` container back through
// internal/backend/gcp's own client: the one the copy wrote it with, and the
// one whose own shape check (internal/intake) the container has to pass.
func (r *psRig) grammarContainer(t *testing.T) (gcp.ContainerInfo, bool) {
	t.Helper()
	svc, err := gcp.NewSecrets(context.Background(), gcp.Options{GCPProject: initRepoProject, Region: "us-east5",
		Endpoints: gcp.Endpoints{SecretManager: r.sm.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatalf("connecting to the rig's Secret Manager fake: %v", err)
	}
	info, found, err := svc.Container(context.Background(), psGrammarID())
	if err != nil {
		t.Fatalf("reading %s: %v", psGrammarID(), err)
	}
	return info, found
}

// newerGrammar writes a version newer than the copy's into the name's
// `fugaro_p_` container, as an operator's own store does to the same container
// after a migration (D3323). It is what tells the grammar's value from the
// project copy's when the run answers, and it insists on the copy being there
// first: a container this test made itself would prove nothing.
func (r *psRig) newerGrammar(t *testing.T, value string) {
	t.Helper()
	if v := r.sm.Latest(psGrammarID()); v == nil {
		t.Fatalf("%s holds no version: the migration's copy did not make it", psGrammarID())
	}
	r.sm.AddVersion(psGrammarID(), []byte(value))
}

// psIntakeSecret is one of our containers as the intake's own shape check reads
// it, so the check asked here is the check the copy's own write path asks.
func psIntakeSecret(c gcp.ContainerInfo) intake.Secret {
	return intake.Secret{
		Labels: c.Labels, Annotations: c.Annotations,
		Shape: intake.Shape{TTL: c.TTL, ExpireTime: c.ExpireTime, Topics: c.Topics, CMEK: c.CMEK, Rotation: c.Rotation},
	}
}

// projectRule is the record's entry for the name, which is the whole of what
// version 2 is admitted by.
func (r *psRig) projectRule(t *testing.T) (creds.ProjectSecretRule, bool) {
	t.Helper()
	rec, found, err := creds.ReadProjectSecrets(context.Background(), r.client)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return creds.ProjectSecretRule{}, false
	}
	rule, ok := rec.Rule(psSecretName)
	return rule, ok
}

// v1Record is the record the converge wrote: the variable, and the container it
// is read from.
func (r *psRig) v1Record(t *testing.T) map[string]string {
	t.Helper()
	rec, found, err := creds.ReadWorkflowSecrets(context.Background(), r.client, psSlug)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return nil
	}
	out := map[string]string{}
	for _, row := range rec.Workflows[psWorkflow] {
		out[row.Env] = row.ID
	}
	return out
}

// psAgent is the run's agent: a script that answers the sandbox workflow's two
// stages and nothing else, and that reads the file the declared secret was
// exposed as at the first of them -- which is the whole of what these tests
// assert about the fetch.
type psAgent struct {
	path  string
	value string
	calls []agent.Request
}

func (a *psAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	a.calls = append(a.calls, req)
	switch len(a.calls) {
	case 1:
		a.path = envValue(req.Env, psSecretEnv)
		raw, err := os.ReadFile(a.path)
		if err != nil {
			return agent.Result{}, fmt.Errorf("the stage's environment carries %s=%q, which is not a file it can read: %w", psSecretEnv, a.path, err)
		}
		a.value = string(raw)
		if err := psShell(req, "echo feature > feature.txt && git add -A && git commit -qm 'Add feature'"); err != nil {
			return agent.Result{}, err
		}
		// The workflow's test command, recorded: a run whose final commit has
		// no verified test ends a draft (unverified_final_commit), which is not
		// what these tests are about.
		if _, err := verify.Run(ctx, verify.Options{StateDir: envValue(req.Env, "FUGARO_STATE_DIR"), Kind: verify.KindTest, Env: req.Env, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
			return agent.Result{}, fmt.Errorf("the test command: %w", err)
		}
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add feature\n\nAdds feature.txt.\n"), 0o644); err != nil {
			return agent.Result{}, err
		}
		return agent.Result{CostUSD: 1}, nil
	case 2:
		body, err := json.Marshal(map[string]any{"verdict": "ship", "findings": []any{}})
		if err != nil {
			return agent.Result{}, err
		}
		return agent.Result{Structured: body, CostUSD: 0.5}, nil
	default:
		return agent.Result{}, fmt.Errorf("unexpected agent call #%d", len(a.calls))
	}
}

// psShell runs one command in the stage's own directory and environment.
func psShell(req agent.Request, script string) error {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", script)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s", script, err, out)
	}
	return nil
}

// psRun is the fetch leg: one non-local run of this repository's job, in
// process, against the service this rig serves.
type psRun struct {
	rig   *psRig
	deps  runner.Deps
	agent *psAgent
	store *runstore.Store
	key   []byte
}

// newRun builds the run's job as the launcher would leave it: the checkout's
// own files as the remote, the runs bucket with the run's sealed token in it,
// and the environment a switched job carries.
func (r *psRig) newRun(t *testing.T) *psRun {
	t.Helper()
	files := psRunFiles(t)
	remote := testutil.NewRemote(t, files)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	spec := &task.Spec{Version: 1, RunID: runID, Repo: psRepoName, Ref: "main", Workflow: psWorkflow, Task: "Add a feature"}
	store := runstore.Open(bucket, psSlug, runID)
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	managed := filepath.Join(tmp, "claude-code")
	if err := os.Mkdir(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	script := &psAgent{}
	run := &psRun{
		rig: r, store: store, agent: script,
		deps: runner.Deps{
			Store: store, OpenProvider: gitprov.Static(&fake.Provider{Repo: psRepoName, Remote: remote}), Agent: script,
			WorkDir: filepath.Join(tmp, "work"), Remote: remote, StateDir: filepath.Join(tmp, "state"),
			Env:        []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp},
			CancelPoll: 20 * time.Millisecond, ManagedSettingsPath: filepath.Join(managed, "managed-settings.json"),
		},
	}
	// A job run: `fugaro exec` never cascades (D149), and the job a run is in is
	// the platform's own report of its execution.
	run.deps.Local = false
	run.deps.Project = initRepoProjectName
	run.deps.Execution = psExecution()
	run.deps.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	run.deps.Env = append(run.deps.Env, r.budget.Env()...)
	run.deps.Env = append(run.deps.Env,
		"FUGARO_CREDENTIALS_SERVICE_URL="+r.svc.URL,
		"GCE_METADATA_HOST="+strings.TrimPrefix(r.meta.URL, "http://"))
	mode := map[string]string{runner.BudgetModeEnv: psBudgetMode}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := mode[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	run.deps.Spend = spend
	run.attachBackend(t, bucket)
	return run
}

// attachBackend gives the job the launcher's own halves: the signer that mints
// this run's identity, the identity the budget session opens with, and the
// sealed token waiting in the runs bucket. It is the smallest world in which
// `r.sess` exists at all, which is what a fetch presents to the service.
func (r *psRun) attachBackend(t *testing.T, bucket *blob.Bucket) {
	t.Helper()
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(testutil.BudgetSigner)
	itk := gcpfake.NewIdentityToolkit(t, iam, r.rig.budget.APIKey, psFirebaseProject)
	r.key = make([]byte, token.KeySize)
	for i := range r.key {
		r.key[i] = 0x5a + byte(i)
	}
	r.deps.Backend = runner.Backend{
		RTDBURL: r.rig.db.URL, APIKey: r.rig.budget.APIKey,
		TokenMarked: true, TokenKey: r.key,
		Tune: func(c *budget.Config) {
			c.IdentityURL, c.SecureTokenURL = itk.URL, itk.URL
			c.HeartbeatEvery, c.RetryEvery, c.CapRetryWait = psHeartbeat, 20*time.Millisecond, 30*time.Millisecond
			c.StaleBackoff, c.KillRestart = 2*time.Millisecond, 100*time.Millisecond
			c.StreamBackoffMin, c.StreamBackoffMax = 10*time.Millisecond, 50*time.Millisecond
			if c.Grace == 0 || c.Grace == budget.DefaultGrace {
				c.Grace = 600 * time.Millisecond
			}
		},
	}
	signer, err := token.NewIAMSigner(testutil.BudgetSigner,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	custom, err := token.MintAt(context.Background(), signer,
		token.Claims{Slug: psSlug, Run: runID, FX: token.ExpiryFX(now, 3*time.Hour), FP: initRepoProjectName, RB: psRB}, now)
	if err != nil {
		t.Fatal(err)
	}
	r.deps.Bucket = blobx.Wrap(bucket)
	if err := token.PutObject(context.Background(), r.deps.Bucket, psSlug, runID, custom, r.key); err != nil {
		t.Fatal(err)
	}
}

// run is the whole loop, and nothing more: the sandbox recipe's two stages.
func (r *psRun) run(t *testing.T) (*runstore.Record, error) {
	t.Helper()
	return runner.Run(context.Background(), r.deps)
}

// psRunFiles is the sandbox fixture as the run's job carries it: the same
// repository and workflow the two CLI legs used, with the three things a
// switched run needs -- the cascade auth, the model pins a gatewayed run must
// name, and the declaration that exposes the secret as a file -- and commands
// that pass without node, which is not what this test is about.
func psRunFiles(t *testing.T) map[string]string {
	t.Helper()
	src := filepath.Join(testutil.ModuleRoot(), "deploy", "sandbox")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	cfg := files["fugaro.yaml"]
	for _, sub := range []struct{ from, to string }{
		{`(?m)^project: .*\n`, "project: " + initRepoProjectName + "\n"},
		{`agent: \{ auth: oauth, review_rounds: 1, max_budget_usd: 2 \}`, psRunAgentBlock},
		{`git: \{ provider: bitbucket, base_branch: master \}`, `git: { provider: bitbucket, base_branch: main }`},
		{`commands: \{ build: "node --check test.js", test: "npm test" \}`, `commands: { build: "true", test: "true" }`},
		{"- { name: sandbox-probe, env: SANDBOX_PROBE }", "- { name: sandbox-probe, env: SANDBOX_PROBE, file: true }"},
	} {
		out := regexp.MustCompile(sub.from).ReplaceAllLiteralString(cfg, sub.to)
		if out == cfg {
			t.Fatalf("the sandbox fixture no longer holds %s", sub.from)
		}
		cfg = out
	}
	files["fugaro.yaml"] = cfg
	return files
}

// psRunAgentBlock is the fixture's agent block as the run needs it: the
// cascade (a run reaches a credentials service only on the auth that has one),
// the pins every gatewayed run must name, one review round, and no first-line
// review, so the script has exactly two stages to answer.
const psRunAgentBlock = `agent:
  auth: cascade
  review_rounds: 1
  max_budget_usd: 2
  first_line_review: off
  models: { coder: claude-sonnet-5-5, reviewer: claude-sonnet-5-5, background: claude-haiku-4-5 }
  max_output_tokens: { coder: 4096, reviewer: 4096 }`

// envValue is an entry of a stage's environment.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// TestProjectSecretFetchedEndToEnd is the whole path: the migration copies the
// name into the grammar and records it (IS5), the converge records the
// version-1 row, and the run's fetch serves the grammar's copy as the file the
// declaration asked for.
//
// What the run proves is the end of the chain: the agent stage's environment
// carries the variable, the variable is a path, the file holds the value the
// service answered -- and the service was asked once, by the name, in the
// contract's version-2 shape.
func TestProjectSecretFetchedEndToEnd(t *testing.T) {
	r := newPSRig(t, psRepoName)
	if _, ok := r.projectRule(t); ok {
		t.Fatalf("the record holds an entry for %s before the migration", psSecretName)
	}
	r.migrate(t)
	// The migration's two halves: the copied container, and the record entry it
	// seeded from the operator's own allow_data_to (D3325, F23).
	info, found := r.grammarContainer(t)
	if !found {
		t.Fatalf("the migration left no %s", psGrammarID())
	}
	if err := intake.AcceptsWorkflowWrite(psGrammarID(), psIntakeSecret(info)); err != nil {
		t.Errorf("the copied container is not one the intake would write: %v", err)
	}
	if got := info.Annotations[intake.AnnotationCreatedBy]; got != psOperatorEmail {
		t.Errorf("the copied container says %q made it, want the operator %s", got, psOperatorEmail)
	}
	if got := string(r.sm.Latest(psGrammarID())); got != psCopyValue {
		t.Errorf("the grammar holds %q, want the project copy's value the migration moved", got)
	}
	if !slices.Contains(r.sm.Accessed(), psCopyID()) {
		t.Errorf("the migration read no value from %s, the copy it exists to move", psCopyID())
	}
	rule, ok := r.projectRule(t)
	if !ok || !slices.Equal(rule.Repos, []string{psRepoName}) {
		t.Fatalf("the record's entry for %s is %+v, want this repository and nothing else", psSecretName, rule)
	}
	r.converge(t)
	if got := r.v1Record(t); got[psSecretEnv] != psCopyID() {
		t.Fatalf("the version-1 record is %v, want %s -> %s", got, psSecretEnv, psCopyID())
	}
	r.newerGrammar(t, psGrammarValue)

	leg := r.newRun(t)
	rec, err := leg.run(t)
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if leg.agent.path == "" || leg.agent.path == psGrammarValue {
		t.Fatalf("the stage's %s is %q, want the path of a file holding the value", psSecretEnv, leg.agent.path)
	}
	if leg.agent.value != psGrammarValue {
		t.Errorf("the file holds %q, want the value the service answered", leg.agent.value)
	}
	if _, err := os.Stat(leg.agent.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file %s outlived the run: a fetched value is not left behind (G3)", leg.agent.path)
	}
	calls := r.callsOf()
	if len(calls) != 1 {
		t.Fatalf("the service was called %d times, want once: %+v", len(calls), calls)
	}
	if calls[0].version != credsvc.WorkflowSecretsVersion2 || calls[0].name != psSecretName || calls[0].env != "" {
		t.Errorf("the call was %+v, want the contract's version-2 call by name", calls[0])
	}
	if calls[0].status != http.StatusOK || calls[0].code != "" {
		t.Errorf("the call answered %d %s, want the value", calls[0].status, calls[0].code)
	}
}

// TestUnlistedRepositoryRefusedEndToEnd: the record admits another repository,
// so the name is not one this run may fetch. Version 2 refuses
// (workflow_secret_not_allowed), the run asks again in version 1's vocabulary
// (the fallback the record itself is written in), and version 1 has no record
// for this repository at all -- so the run stops before any agent call, and the
// refusal names the variable.
func TestUnlistedRepositoryRefusedEndToEnd(t *testing.T) {
	r := newPSRig(t, "acme/other")
	r.allow(t, "acme/other")
	if rule, ok := r.projectRule(t); !ok || len(rule.Repos) != 1 || rule.Repos[0] != "acme/other" {
		t.Fatalf("the record's entry for %s is %+v, want the other repository alone", psSecretName, rule)
	}
	if got := r.v1Record(t); got != nil {
		t.Fatalf("the version-1 record is %v, want none: no converge ran", got)
	}

	leg := r.newRun(t)
	rec, err := leg.run(t)
	if err == nil {
		t.Fatalf("the run finished with %+v, want the refusal", rec)
	}
	if !strings.Contains(err.Error(), psSecretEnv) {
		t.Errorf("the refusal is %q, and does not name the variable it refused", err)
	}
	if len(leg.agent.calls) != 0 {
		t.Errorf("the run reached %d agent calls, want none: the refusal is before the first stage", len(leg.agent.calls))
	}
	calls := r.callsOf()
	if len(calls) != 2 {
		t.Fatalf("the service was called %d times, want two: %+v", len(calls), calls)
	}
	if calls[0].version != credsvc.WorkflowSecretsVersion2 || calls[0].name != psSecretName {
		t.Errorf("the first call was %+v, want version 2 by name", calls[0])
	}
	if calls[0].code != credsvc.ErrorWorkflowSecretNotAllowed {
		t.Errorf("version 2 answered %d %s, want %s", calls[0].status, calls[0].code, credsvc.ErrorWorkflowSecretNotAllowed)
	}
	if calls[1].version != credsvc.WorkflowSecretsVersion1 || calls[1].name != "" || calls[1].env != psSecretEnv {
		t.Errorf("the second call was %+v, want the version-1 fallback by variable", calls[1])
	}
}

// TestUncopiedNameFallsBackToV1EndToEnd: the local block admits this repository
// -- so the converge recorded the name's container -- and no `fugaro_p_`
// container exists: this rig runs no migration (the fetch test's leg), so it is
// in the state every installation is in until one copies the name (D3325).
// Version 2 refuses, and version 1 answers out of the record and the project
// copy it names.
func TestUncopiedNameFallsBackToV1EndToEnd(t *testing.T) {
	r := newPSRig(t, psRepoName)
	if _, ok := r.projectRule(t); ok {
		t.Fatalf("the record holds an entry for %s: the owner never allowed it", psSecretName)
	}
	if v := r.sm.Latest(psGrammarID()); v != nil {
		t.Fatalf("the grammar container is there (%q), want the uncopied state", v)
	}
	r.converge(t)
	if got := r.v1Record(t); got[psSecretEnv] != psCopyID() {
		t.Fatalf("the version-1 record is %v, want %s -> %s", got, psSecretEnv, psCopyID())
	}

	leg := r.newRun(t)
	rec, err := leg.run(t)
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if leg.agent.value != psCopyValue {
		t.Errorf("the file holds %q, want the project copy's value", leg.agent.value)
	}
	calls := r.callsOf()
	if len(calls) != 2 {
		t.Fatalf("the service was called %d times, want two: %+v", len(calls), calls)
	}
	if calls[0].code != credsvc.ErrorWorkflowSecretNotAllowed {
		t.Errorf("version 2 answered %d %s, want %s", calls[0].status, calls[0].code, credsvc.ErrorWorkflowSecretNotAllowed)
	}
	if calls[1].version != credsvc.WorkflowSecretsVersion1 || calls[1].status != http.StatusOK {
		t.Errorf("the fallback answered %+v, want the version-1 record's value", calls[1])
	}
}
