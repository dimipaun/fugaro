package runner

// nested_e2e_test.go is N3's half of docs/shared-jobs-default: the nested
// fetches of a proxied run, proven end to end against a service that really
// serves git and git-lfs.
//
// N2 wrote the routes and asked them one seam at a time (gitproxy_test.go): the
// URL each submodule is pointed at, the endpoint git-lfs is pointed at, and the
// refusal a submodule outside the run's access earns. What none of those could
// show is that the routes are the ones git and git-lfs actually follow — a
// route written into a checkout's configuration is worth what a client does
// with it. So this file drives the run's own clone, LFS route, checkout and
// submodule route in bootstrap's own order, through the run's own loopback
// forwarder (gateway.GitForward, B1.6), to a stand-in credentials service whose
// git half is git's own http-backend over two real repositories and whose LFS
// half speaks git-lfs's protocol. The submodule's clone and the one LFS object
// are then fetched by the real clients: `git submodule update --init` for one,
// and the smudge filter a checkout of an LFS file runs for the other.
//
// The stand-in replaces the upstream those calls are forwarded to, and never
// the reading of them: what each call IS is gitproxy.Classify and
// ReadLFSBatch, N1's own code, because the real route rebuilds its upstream as
// bitbucket.org (G13) and can only be pointed at a stand-in upstream from
// inside internal/credsvc — there is nothing here for a runner test to
// configure. What the service in this file holds besides is the two identities
// the forwarder presents on every call (G5) and the repositories this run may
// reach. The rule that decides whether a call may be made at all (Decide) is
// not here: it binds a call to the repository the run is paired to, and a
// runner's fixture has no pairing to be bound by — the repository the
// classification named is recorded instead, and asserted where a test reads it.
//
// Red step, measured on this fixture rather than assumed. With N1's
// classification absent — gitproxy's lfsShape refusing git-lfs's path, which is
// U2's state — the LFS arm fails: the batch is refused and the checkout's
// smudge has nothing to fetch. With N2's two routes absent, the first arm fails
// (no lfs.url is written for the checkout to find the endpoint in) and the
// third fails (the uncovered submodule is not refused at all), while the LFS
// arm still passes — git-lfs derives an endpoint git has already rewritten to
// the forwarder. That is the accident U2 named, and it is the reason the first
// arm proves the nested fetch and not the write that sent it: dropping
// PointSubmoduleAt alone leaves every arm passing, because a submodule URL
// resolved against the checkout's origin is a bitbucket.org URL too, and the
// rewrite carries it to the same endpoint the route would have written.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/gitproxy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	// nestedE2EJobIDToken is what the fixture's fake metadata server answers,
	// and so what the forwarder presents as this job's Google identity on every
	// call it forwards. It is a shape and not a credential: nothing verifies it
	// here, and the assertion is that the nested fetches reached the service
	// under this job's identity.
	nestedE2EJobIDToken = "job-id-token-nested-e2e"
	// nestedE2ELFSFile is the one LFS file of the repository the run clones: a
	// pointer blob under an .gitattributes that marks it filter=lfs, which is
	// what makes a checkout of it run git-lfs's smudge.
	nestedE2ELFSFile = "asset.bin"
	// nestedE2EObject is the bytes that file holds, and so the object the
	// smudge filter has to fetch through the run's forwarder for the file to be
	// anything but its pointer.
	nestedE2EObject = "the bytes of the one LFS file\n"
	// nestedE2ESubmodulePath, nestedE2ESubmoduleFile and
	// nestedE2ESubmoduleContent are the one submodule: the path .gitmodules
	// declares, and the tree no clone of the superproject carries (a gitlink is
	// all a submodule is).
	nestedE2ESubmodulePath    = "lib"
	nestedE2ESubmoduleFile    = "lib.txt"
	nestedE2ESubmoduleContent = "the submodule's own tree\n"
	// nestedE2EApp and nestedE2ESubmoduleRepo are the two repositories the
	// service serves, in the layout git-http-backend serves them out of: one
	// project root (GIT_PROJECT_ROOT), and the repository below it.
	nestedE2EApp           = "acme/app.git"
	nestedE2ESubmoduleRepo = "acme/lib.git"
	// nestedE2ECovered is the published block's allow_repos wherever an arm
	// expects the run to go through: the run's repository and its submodule's,
	// which is what a repository with a submodule needs to be run at all.
	nestedE2ECovered = "bitbucket/acme/*"
	// nestedE2EMaxBatchBody bounds the batch body the stand-in reads, as the
	// route bounds it (credsvc's maxLFSBatchBodyBytes): a few kilobytes of
	// object list, and never a stream this test holds.
	nestedE2EMaxBatchBody = 64 << 10
)

// nestedE2EObjectOID is git-lfs's own name for the object: the lower-case hex
// sha256 of its bytes, which is what the pointer blob carries and what a batch
// answer names it by.
var nestedE2EObjectOID = func() string {
	sum := sha256.Sum256([]byte(nestedE2EObject))
	return hex.EncodeToString(sum[:])
}()

// nestedE2EObjectStore stands in for wherever a batch answer points an object
// download at, and it is a server of its own because that is what the answer
// names: git-lfs fetches the href as it is written, running no
// url.*.insteadOf rewrite over one (measured, git-lfs 3.6.1), and the batch
// answer this route serves is the upstream's, passed through as bitbucket.org
// wrote it (the service rewrites no href). The batch call, which is the LFS
// read that carries the run's access, is the half that rides the forwarder.
type nestedE2EObjectStore struct {
	srv *httptest.Server

	mu   sync.Mutex
	gets []string
}

// nestedE2EProxy is the installation's credentials service as a proxied run's
// nested fetches meet it: one route under the proxy's own prefix, and the two
// halves those fetches have there. Git's is served by git's own http-backend,
// as internal/testutil's HTTP remote serves one repository; git-lfs's are the
// batch call, answered with the download action of the object it names, and the
// object download itself, which the route serves for a client that asks the
// proxy for it.
//
// What the call IS is decided by the rules themselves (gitproxy.Classify and
// ReadLFSBatch, N1), so this stand-in replaces the upstream a call is forwarded
// to and nothing about the reading of it. What it does not have is the rule
// that decides whether a call may be made at all (Decide): that rule binds a
// call to the repository the run is paired to, and a runner's fixture has no
// pairing to be bound by — the repository the call names is recorded here and
// asserted where a test reads it.
//
// Every request is recorded, and every request has to arrive the way the
// forwarder sends one: under this job's Google identity (the ID token the
// metadata server minted, which is what Cloud Run IAM admits) and naming this
// job, with the caller's own credential dropped (G5). This is the only place
// that sees both ends of the hop, so it is the only place that can hold those
// two facts to each other.
type nestedE2EProxy struct {
	t       *testing.T
	job     string
	root    string
	objects *nestedE2EObjectStore
	backend *cgi.Handler
	srv     *httptest.Server

	// gitMu serialises the backend's own environment: it is set per request
	// (the path git-http-backend is served) and read while the request runs.
	gitMu sync.Mutex

	mu    sync.Mutex
	calls []nestedE2ECall
}

// nestedE2ECall is one request that reached the service under the proxy route:
// the shape it arrived in, the two identities the forwarder put on it, and what
// N1's classification (gitproxy.Classify) made of it.
type nestedE2ECall struct {
	method, path, auth, run, job, repo, operation string
}

func newNestedE2EProxy(t *testing.T, root, job string) *nestedE2EProxy {
	t.Helper()
	p := &nestedE2EProxy{
		t: t, job: job, root: root,
		backend: &cgi.Handler{
			Path: filepath.Join(testutil.Git(t, root, "--exec-path"), "git-http-backend"),
		},
		objects: &nestedE2EObjectStore{},
	}
	p.objects.srv = httptest.NewServer(http.HandlerFunc(p.serveObject))
	t.Cleanup(p.objects.srv.Close)
	p.srv = httptest.NewServer(p)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *nestedE2EProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, credsvc.BitbucketProxyPrefix)
	if !ok {
		p.t.Errorf("%s %s: not the proxy route's path", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	call := nestedE2ECall{method: r.Method, path: rest,
		auth: r.Header.Get("Authorization"), run: r.Header.Get(gateway.HeaderRunIdentity),
		job: r.Header.Get(gateway.HeaderRunJob)}
	if call.auth != "Bearer "+nestedE2EJobIDToken {
		p.t.Errorf("%s %s: the call arrived with %q, want this job's own identity", r.Method, rest, call.auth)
		http.Error(w, "no identity", http.StatusUnauthorized)
		return
	}
	if call.job != p.job {
		p.t.Errorf("%s %s: the call names the job %q, want %q", r.Method, rest, call.job, p.job)
	}
	if call.run == "" {
		p.t.Errorf("%s %s: the call carries no run identity", r.Method, rest)
	}
	// What the call is, decided by the rules the service decides it by
	// (internal/gitproxy, N1): this stand-in replaces the upstream a call is
	// forwarded to and not the reading of the call, so a git-lfs path is one
	// here exactly as it is there — and a path the rules do not name is refused
	// here as the route refuses it (U2 is the measurement: before N1 every
	// git-lfs read was refused as a call no run makes).
	req, err := gitproxy.Classify(r.Method, "/"+rest, r.URL.Query())
	if err != nil {
		p.t.Errorf("%s %s: the rules do not name this call: %v", r.Method, rest, err)
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	call.repo = req.Repo
	defer func() {
		p.mu.Lock()
		p.calls = append(p.calls, call)
		p.mu.Unlock()
	}()
	switch req.LFS {
	case gitproxy.LFSBatch:
		p.batch(w, r, &call)
	case gitproxy.LFSObject:
		p.object(w, req.OID)
	default:
		p.git(w, r, rest)
	}
}

// batch answers git-lfs's batch call: the download action of the one object the
// call names, pointed at the object store. The operation is read out of the
// body by the reader the route reads it with (gitproxy.ReadLFSBatch) and an
// upload is refused, which is the rule this route is built on (N1): a run's
// objects go up in the pack of its push, and an upload has no body a policy
// could be read out of.
func (p *nestedE2EProxy) batch(w http.ResponseWriter, r *http.Request, call *nestedE2ECall) {
	// The body the operation was read out of, which is also the document the
	// objects of the answer come from: what the rules read is the operation,
	// and the rest is this stand-in's own answer.
	operation, body, err := gitproxy.ReadLFSBatch(r.Body, nestedE2EMaxBatchBody)
	if err != nil {
		p.t.Errorf("the batch call is not git-lfs's document: %v", err)
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	call.operation = operation
	if operation != gitproxy.LFSOperationDownload {
		http.Error(w, "the proxy serves no LFS write", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Objects []struct {
			OID string `json:"oid"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		p.t.Errorf("the batch call is not git-lfs's document: %s", body)
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	objects := make([]map[string]any, 0, len(in.Objects))
	for _, o := range in.Objects {
		if o.OID != nestedE2EObjectOID {
			p.t.Errorf("the batch call asks for the object %s, want the one LFS file's %s", o.OID, nestedE2EObjectOID)
		}
		objects = append(objects, map[string]any{
			"oid": o.OID, "size": len(nestedE2EObject),
			"actions": map[string]any{
				"download": map[string]string{"href": p.objects.srv.URL + "/objects/" + o.OID},
			},
		})
	}
	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	if err := json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "objects": objects}); err != nil {
		p.t.Errorf("answering the batch call: %v", err)
	}
}

// object serves one LFS object under the proxy route, which is the shape N1
// classifies as a fetch: a client that asks the proxy for the bytes of an
// object gets them. oid is the rules' own reading of the path (Request.OID),
// never a segment this stand-in re-parsed.
func (p *nestedE2EProxy) object(w http.ResponseWriter, oid string) {
	if oid != nestedE2EObjectOID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_, _ = io.WriteString(w, nestedE2EObject)
}

// git serves the repository the path names out of git's own http-backend, the
// way internal/testutil's HTTP remote serves one: the project root is the
// directory both repositories live in, and the path is the one the request
// arrived with — which for these fetches is the route's own, because the URL
// each client was handed names the forwarder (the clone's rewrite, and the
// submodule's own configuration). The repository below the route is the one
// N1's classification reads back out of it (Request.Repo), and that is what
// each arm asserts.
func (p *nestedE2EProxy) git(w http.ResponseWriter, r *http.Request, rest string) {
	// The body is materialised when its length did not arrive: the forwarder
	// streams a request of unknown length in chunked framing and never measures
	// it (a pack is not held here to be counted), and a CGI program is told how
	// much of stdin is its input. A server that reads such a request into memory
	// before running one — which is what this stand-in is — supplies what only
	// it can know.
	if r.ContentLength < 0 && r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			p.t.Errorf("%s %s: reading the request's body: %v", r.Method, rest, err)
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		r.ContentLength = int64(len(data))
	}
	p.gitMu.Lock()
	defer p.gitMu.Unlock()
	p.backend.Env = []string{
		"GIT_PROJECT_ROOT=" + p.root,
		"GIT_HTTP_EXPORT_ALL=1",
		"PATH_INFO=/" + rest,
	}
	p.backend.ServeHTTP(w, r)
}

// serveObject is the object store: the bytes of the one object, by the name
// git-lfs knows it under.
func (p *nestedE2EProxy) serveObject(w http.ResponseWriter, r *http.Request) {
	p.objects.mu.Lock()
	p.objects.gets = append(p.objects.gets, r.URL.Path)
	p.objects.mu.Unlock()
	if !strings.HasSuffix(r.URL.Path, "/"+nestedE2EObjectOID) {
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, nestedE2EObject)
}

// served reports whether the service was asked for path, which is how a test
// reads that a fetch rode the forwarder: the path under the route is the
// repository and the operation the client asked the proxy for.
func (p *nestedE2EProxy) served(path string) bool {
	for _, c := range p.paths() {
		if c == path {
			return true
		}
	}
	return false
}

// paths is every path the service was asked for, for a failure that says what
// arrived instead.
func (p *nestedE2EProxy) paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.calls))
	for _, c := range p.calls {
		out = append(out, c.path)
	}
	return out
}

// callsFor is the recorded calls whose path is path.
func (p *nestedE2EProxy) callsFor(path string) []nestedE2ECall {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []nestedE2ECall
	for _, c := range p.calls {
		if c.path == path {
			out = append(out, c)
		}
	}
	return out
}

// objectGets is the object store's own requests: the one assertion that is
// about where the bytes of an object came from.
func (p *nestedE2EProxy) objectGets() []string {
	p.objects.mu.Lock()
	defer p.objects.mu.Unlock()
	return append([]string(nil), p.objects.gets...)
}

// nestedE2E is one run of the fixture at the point bootstrap reaches once the
// run's own tree is checked out: the forwarder started, the clone made through
// it, the LFS endpoint written before the checkout, and the tree there. What
// each test below starts with is the submodules' route (routeNestedFetches),
// which is the moment that test is about.
type nestedE2E struct {
	t    *testing.T
	p    *nestedE2EProxy
	r    *run
	vars map[string]string
	home string
}

// nestedE2EClone builds the fixture and drives bootstrap's own steps in
// bootstrap's own order: the forwarder (startProxiedGit), this run's clone
// (openCheckout, which is gitops.CloneBlobless for a job no image baked a
// checkout into), the LFS route (routeLFSEndpoint, before the checkout, because
// the checkout is itself a fetch) and the checkout itself
// (CheckoutNewBranch).
//
// It is the fixture and not a whole run for the reason gitproxy_test.go gives
// for its own: the run's identity is the budget session's token and not an
// environment value, so a run is built the way the run's own policy read builds
// it — and everything the nested fetches depend on (the forwarder, the policy
// the coverage question is answered from, the checkout) is real.
//
// lfs, when set, writes git-lfs's filters into the checkout as the job's image
// has them (images/base runs `git lfs install --system`): this test's git is
// isolated from the machine's configuration (testutil.IsolateGit), so without
// that write a checkout of an LFS file leaves the pointer blob in the working
// tree and the smudge filter — the thing that fetches the object through the
// route — never runs.
func nestedE2EClone(t *testing.T, allow []string, lfs bool) *nestedE2E {
	t.Helper()
	testutil.IsolateGit(t)
	root := t.TempDir()
	lib := nestedBare(t, root, nestedE2ESubmoduleRepo, map[string]string{
		nestedE2ESubmoduleFile: nestedE2ESubmoduleContent,
		"README.md":            "the submodule's own repository\n",
	}, nil)
	nestedBare(t, root, nestedE2EApp, map[string]string{
		// A relative submodule URL, the shape almost every repository writes:
		// the run resolves it against this checkout's own remote exactly as git
		// does, which is what makes the coverage question one about acme/lib.
		".gitmodules":    "[submodule \"" + nestedE2ESubmodulePath + "\"]\n\tpath = " + nestedE2ESubmodulePath + "\n\turl = ../lib.git\n",
		".gitattributes": nestedE2ELFSFile + " filter=lfs diff=lfs merge=lfs -text\n",
		nestedE2ELFSFile: "version https://git-lfs.github.com/spec/v1\noid sha256:" + nestedE2EObjectOID +
			fmt.Sprintf("\nsize %d\n", len(nestedE2EObject)),
		"README.md": "the run's own repository\n",
	}, map[string]string{nestedE2ESubmodulePath: testutil.Git(t, lib, "rev-parse", "refs/heads/main")})
	meta := gitMetadataFake(t, nestedE2EJobIDToken)
	p := newNestedE2EProxy(t, root, svcJob)
	home := t.TempDir()
	r := proxiedRun(t, pushIdentity(t), func(r *run) {
		r.d.WorkDir = filepath.Join(t.TempDir(), "work")
		// A job whose image baked no checkout: the run clones its own
		// (openCheckout reads cloned, which openBakedCheckout settles).
		r.cloned = true
		r.d.Local = true
		r.d.Env = []string{
			providers.EnvCredentialsServiceURL + "=" + p.srv.URL,
			metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
		}
		r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
			Version:        projectpolicy.Version,
			BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: allow, Proxy: true},
		}}
	})
	f := &nestedE2E{t: t, p: p, r: r, home: home}
	startForward(t, r)
	r.credURL = gitops.CredentialURL(r.forwarderRemote(nestedOrigin))
	if !strings.HasPrefix(r.credURL, "http://127.0.0.1:") {
		t.Fatalf("the credential is scoped to %q, want this execution's loopback endpoint", r.credURL)
	}
	vars, err := r.authVars()
	if err != nil {
		t.Fatal(err)
	}
	f.vars = vars
	ctx := context.Background()
	repo, err := r.openCheckout(ctx, nestedOrigin, vars)
	if err != nil {
		t.Fatalf("cloning %s through the run's own forwarder: %v", nestedOrigin, err)
	}
	r.repo = strip(repo)
	if lfs {
		f.installLFSFilters()
	}
	if err := r.routeLFSEndpoint(ctx); err != nil {
		t.Fatalf("pointing the LFS endpoint before the checkout: %v", err)
	}
	if err := r.repo.CheckoutNewBranch(ctx, "main", "fugaro/"+svcRunID); err != nil {
		t.Fatalf("checking the clone's branch out: %v", err)
	}
	return f
}

// git runs git in the run's checkout under the environment the run's own git
// commands run under: the credential helper scoped to the forwarder and
// carrying no token, the rewrite that sends every bitbucket.org URL there, and
// this job's variables (credentialVars) — which is the environment the agent's
// own git commands get. The process's own environment is deliberately not part
// of it beyond PATH: this test binary runs inside the machine's session, and
// nothing of that session belongs in the run under test.
func (f *nestedE2E) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.r.d.WorkDir}, args...)...)
	cmd.Env = gitops.WithVars(append(gitops.IdentityEnvFor(f.r.identity),
		"PATH="+os.Getenv("PATH"),
		"HOME="+f.home,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	), f.vars)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return strings.TrimSpace(out.String())
}

// installLFSFilters gives the checkout the four filter entries the job's image
// carries system-wide, so that a checkout of the LFS file runs git-lfs's smudge
// and fetches the object the way a run of the image does.
func (f *nestedE2E) installLFSFilters() {
	f.t.Helper()
	for _, c := range [][2]string{
		{"filter.lfs.process", "git-lfs filter-process"},
		{"filter.lfs.smudge", "git-lfs smudge -- %f"},
		{"filter.lfs.clean", "git-lfs clean -- %f"},
		{"filter.lfs.required", "true"},
	} {
		f.git("config", c[0], c[1])
	}
}

// read is one file of the run's checkout, by the path the tree spells.
func (f *nestedE2E) read(path string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.r.d.WorkDir, path))
	if err != nil {
		f.t.Fatalf("reading %s of the run's checkout: %v", path, err)
	}
	return string(data)
}

// nestedBare seeds a bare repository at root/repo with files on its main
// branch, as testutil.NewRemote does, and returns it. links, when not empty, is
// committed as gitlinks (mode 160000): the entries that make a tree — and not
// only .gitmodules — declare a submodule, because `git submodule update` walks
// the index, and a repository whose .gitmodules names one while its tree holds
// no gitlink has no submodule to fetch at all.
//
// The fixture needs two repositories under ONE root: that root is what
// git-http-backend serves them out of (GIT_PROJECT_ROOT), so that one service
// serves the repository the run is for and the repository its submodule is.
func nestedBare(t *testing.T, root, repo string, files, links map[string]string) string {
	t.Helper()
	bare := filepath.Join(root, repo)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "init", "--quiet", "--bare", "-b", "main", bare)
	// The run's clone is partial (gitops.CloneBlobless asks for no blobs): a
	// server that advertises no filter fails it at git's own "filtering not
	// recognized by server".
	testutil.Git(t, bare, "config", "uploadpack.allowFilter", "true")
	// As testutil.NewRemote sets them: otherwise receive-pack spawns a detached
	// `git maintenance run`, which can hold the push's inherited output pipe
	// open long after the push returned.
	testutil.Git(t, bare, "config", "receive.autogc", "false")
	testutil.Git(t, bare, "config", "maintenance.auto", "false")
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", bare, seed)
	testutil.WriteFiles(t, seed, files)
	testutil.Git(t, seed, "add", "-A")
	for path, sha := range links {
		testutil.Git(t, seed, "update-index", "--add", "--cacheinfo", "160000", sha, path)
	}
	testutil.Git(t, seed, "commit", "--quiet", "-m", "seed")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return bare
}

// TestNestedFetchEndToEnd is N3's acceptance: a run whose repository carries one
// submodule and one LFS file clones through its own forwarder, the submodule's
// clone and the LFS object ride the same route, and a submodule outside the
// run's Bitbucket access stops the run naming its repository.
func TestNestedFetchEndToEnd(t *testing.T) {
	// The run's own clone and its submodule's: the tree the run works on
	// arrives through the forwarder, and so does the tree of a repository the
	// run reaches only because a submodule declares it.
	t.Run("the repository and its submodule clone through the proxy", func(t *testing.T) {
		f := nestedE2EClone(t, []string{nestedE2ECovered}, false)
		// A partial clone — no tree, no blobs — of a repository carrying a
		// submodule and an LFS file, over the run's own forwarder: the service
		// was asked for this repository's refs, and it is what answered.
		if !f.p.served(nestedE2EApp + "/info/refs") {
			t.Errorf("the run's clone did not reach the service: %v", f.p.paths())
		}
		if got := f.read("README.md"); got != "the run's own repository\n" {
			t.Errorf("the run's checkout holds %q, want the cloned tree", got)
		}
		// The LFS endpoint, which the route writes before the checkout because
		// the checkout is itself a fetch.
		want := strings.TrimSuffix(f.r.forward.URL(), "/") + "/acme/app.git/info/lfs"
		if got := f.git("config", "--get", "lfs.url"); got != want {
			t.Errorf("lfs.url = %q, want the forwarder's endpoint %q", got, want)
		}
		// With no filters installed — the state an isolated git leaves the
		// checkout in, and the state review mode's checkouts are in — a checkout
		// writes the pointer blob: the object is the route's to bring in, and
		// the arm below is what shows that it does.
		if got := f.read(nestedE2ELFSFile); !strings.HasPrefix(got, "version https://git-lfs.github.com/spec/v1") {
			t.Errorf("the checkout holds %q, want git-lfs's pointer blob", got)
		}
		if err := f.r.routeNestedFetches(context.Background()); err != nil {
			t.Fatalf("routing the nested fetches of a checkout the block covers: %v", err)
		}
		// The submodule's own clone, which is what a checkout of a repository
		// with one makes: nothing in the run fetches a submodule itself, so this
		// is the fetch the route aims at the service, made by the same client the
		// agent's own command runs.
		f.git("submodule", "update", "--init")
		if got := f.read(filepath.Join(nestedE2ESubmodulePath, nestedE2ESubmoduleFile)); got != nestedE2ESubmoduleContent {
			t.Errorf("the submodule's tree holds %q, want %q", got, nestedE2ESubmoduleContent)
		}
		if !f.p.served(nestedE2ESubmoduleRepo + "/info/refs") {
			t.Errorf("the submodule's clone did not reach the service: %v", f.p.paths())
		}
		// And that clone arrived under this run's own identity, naming the
		// repository the rules read out of the request — acme/lib's, which is
		// what the route resolved the submodule's relative URL to. The
		// forwarder put the identity there and the rules are what named the
		// repository: the service is where the two ends of the hop meet.
		for _, c := range f.p.callsFor(nestedE2ESubmoduleRepo + "/info/refs") {
			if c.run != f.r.runIDToken() {
				t.Errorf("the submodule's clone carried the run identity %q, want this run's own", c.run)
			}
			if c.repo != "bitbucket/acme/lib" {
				t.Errorf("the submodule's clone was classified as %q, want the repository it asked for", c.repo)
			}
		}
		// The route is the checkout's configuration and not a change of the run's
		// work: nothing of it would be committed.
		if got := f.git("status", "--porcelain"); got != "" {
			t.Errorf("routing the nested fetches changed the checkout: git status reports %q", got)
		}
	})

	// The one LFS file's object, fetched by the filter a run of the image has
	// installed: the batch call is the read that carries the run's access, and
	// it reaches the service through the forwarder.
	t.Run("the LFS object's fetch rides the proxy", func(t *testing.T) {
		if _, err := exec.LookPath("git-lfs"); err != nil {
			t.Skipf("git-lfs is not on PATH, so a checkout cannot run the smudge filter the job's image installs: %v", err)
		}
		f := nestedE2EClone(t, []string{nestedE2ECovered}, true)
		if err := f.r.routeNestedFetches(context.Background()); err != nil {
			t.Fatalf("routing the nested fetches of a checkout the block covers: %v", err)
		}
		if got := f.read(nestedE2ELFSFile); got != nestedE2EObject {
			t.Fatalf("%s holds %q, want the object's bytes %q: the checkout's smudge did not fetch it", nestedE2ELFSFile, got, nestedE2EObject)
		}
		if !f.p.served(nestedE2EApp + "/info/lfs/objects/batch") {
			t.Errorf("the LFS batch call did not reach the service: %v", f.p.paths())
		}
		// Every batch call the service saw is a download of the run's own
		// repository: N1 refuses an upload (a run's objects go up in the pack of
		// its push), and this is the rules naming the client's call as the one
		// read it is.
		for _, c := range f.p.callsFor(nestedE2EApp + "/info/lfs/objects/batch") {
			if c.operation != "download" {
				t.Errorf("a batch call reached the service with the operation %q, want a download", c.operation)
			}
			if c.repo != "bitbucket/acme/app" {
				t.Errorf("the LFS batch call was classified as %q, want the run's own repository", c.repo)
			}
		}
		// And the bytes came from where the answer pointed, which is what the
		// client does with a batch answer: it follows the href as written.
		if gets := f.p.objectGets(); len(gets) != 1 || !strings.HasSuffix(gets[0], "/"+nestedE2EObjectOID) {
			t.Errorf("the object store was asked for %v, want the one object the answer named", gets)
		}
	})

	// A submodule the run's Bitbucket access does not cover stops it before the
	// agent starts, naming the repository the checkout asked for — the
	// submodule's, which is what an operator can act on, and never a class from
	// the service one stage later.
	t.Run("an uncovered submodule is refused by name", func(t *testing.T) {
		f := nestedE2EClone(t, []string{"bitbucket/acme/app"}, false)
		err := f.r.routeNestedFetches(context.Background())
		if err == nil {
			t.Fatal("a submodule outside the run's Bitbucket access was routed to the forwarder")
		}
		for _, want := range []string{
			"the submodule " + nestedE2ESubmodulePath,
			"bitbucket/acme/lib",
			"fugaro secrets allow " + config.BitbucketSecret + " acme/lib",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		// The run's own clone rode the forwarder all the same: what was refused
		// is the submodule, and the repository the run is for is the one the
		// block does cover.
		if !f.p.served(nestedE2EApp + "/info/refs") {
			t.Errorf("the run's clone did not reach the service: %v", f.p.paths())
		}
		if f.p.served(nestedE2ESubmoduleRepo + "/info/refs") {
			t.Errorf("the refused submodule's repository was fetched anyway: %v", f.p.paths())
		}
	})
}
