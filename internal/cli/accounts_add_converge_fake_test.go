package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// accounts_add_converge_fake_test.go is the world `fugaro accounts add`'s
// converge half runs in: the installation rig (internal/gcpfake behind the
// endpoints block, the fake terraform on PATH, private XDG directories) plus
// the project's own database, which is where the policy node, the
// installation's own mark and the credentials cascade's marks live.
//
// The local config is this rig's own and not the installation rig's: what the
// converge is about is an account a profile serves, so the fixture declares
// the profile, the repository whose App the shared job is built from, and
// nothing else. Everything the command has to derive — the account's
// allow_data_to, the profile's claude_accounts, the node's accounts block, the
// job and its own secret — is the command's own work.

// addRigLocal is the fixture's local config: one profile whose allow_repos is
// written (a profile that lists accounts must write its own list) and one
// GitHub repository, because a shared project job is a github one and its App
// ID is what the job is built from. The budget block and the endpoints are the
// rig's, whose URLs are the fakes'.
const addRigLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - owner/repo-a
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
`

// addRig is that world: the installation rig, the project's database and the
// Firestore the fixture's budget block reads its spend history through.
type addRig struct {
	*initRig
	db *gcpfake.RTDB
	fs *gcpfake.Firestore
}

// newAddRig is the fixture, ready: the local config is selected (FUGARO_CONFIG,
// which newInitRig set), the database is this installation's own (the mark
// every writer into it asks for), the published rules generation the policy
// write is gated on is deployed, and the runs bucket carries the project's
// marker.
func newAddRig(t *testing.T) *addRig {
	t.Helper()
	noAgentSession(t)
	r := &addRig{initRig: newInitRig(t), db: gcpfake.NewRTDB(t), fs: gcpfake.NewFirestore(t)}
	f, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, endpoints, ok := strings.Cut(string(f), "\nendpoints: ")
	if !ok {
		t.Fatalf("the rig's config carries no endpoints block:\n%s", f)
	}
	// The block is one line of the rig's config: the repositories that follow
	// it are the rig's own and this fixture writes its own.
	endpoints, _, _ = strings.Cut(endpoints, "\n")
	cfg := addRigLocal +
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: " + initProject + ", rtdb_url: " + r.db.URL + " }\n" +
		"endpoints: " + strings.Replace(endpoints, "no_auth: true }", "firestore: "+r.fs.URL+", no_auth: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.db.Set(budget.PathMark, auroraMark())
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	r.markedRuns(t, initProjectName, initProject)
	return r
}

// addToken is the value a test writes into the secret: built here rather than
// written down, because a token-shaped literal in a fixture is what the rule
// against them is about, and no assertion of this file prints it.
func addToken() string { return strings.Repeat("k", 48) }

// add runs the command the way the owner does — the token on stdin, `--yes`
// for the confirmation, no terminal — and returns what it printed.
func (r *addRig) add(t *testing.T, name string, args ...string) (string, string) {
	t.Helper()
	out, errOut, err := executeStdin(t, addToken()+"\n", append([]string{"accounts", "add", name, "--yes"}, args...)...)
	if err != nil {
		t.Fatalf("accounts add %s: %v\nstdout:\n%s\nstderr:\n%s", name, err, out, errOut)
	}
	return out, errOut
}

// addLocal is the local config the command left behind, decoded strictly: the
// assertions about what the converge reads are made against the file, never
// against a value the command printed.
func (r *addRig) addLocal(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// policy is the node the converge published, decoded: the reader is
// internal/projectpolicy's own, so a node this test reads is one a run of the
// project would read.
func (r *addRig) policy(t *testing.T) *projectpolicy.Policy {
	t.Helper()
	v := r.db.Value(projectpolicy.PathShared())
	if v == nil {
		t.Fatalf("no policy node at %s: the converge published none", projectpolicy.PathShared())
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the published node does not decode (found=%v): %v", found, err)
	}
	return p
}

// projectJobs is the job set the Installation stage deployed, read back from
// the variables the run wrote into the installation's workdir: the module's
// secretAccessor member iterates a job's own secret_env, so a job's entry
// there is the grant, and one account's token on another account's job would
// be a grant this reader sees.
func (r *addRig) projectJobs(t *testing.T) map[string]infra.ProjectJob {
	t.Helper()
	dir, err := infra.InstallationWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	data, err := infra.ReadWorkdirVars(dir, "installation")
	if err != nil {
		t.Fatalf("the installation's variables were not written: %v", err)
	}
	var doc struct {
		ProjectJobs map[string]infra.ProjectJob `json:"project_jobs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.ProjectJobs
}

// markState is one account's credential mark as the database holds it, read
// through internal/creds' own store: what the converge's clear leaves behind.
// "" is an account the database carries no mark for.
func (r *addRig) markState(t *testing.T, name string) string {
	t.Helper()
	db, err := newBudgetClient(t.Context(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	marks, err := (&creds.RTDBStore{DB: db}).Marks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m, ok := marks[name]
	if !ok {
		return ""
	}
	return string(m.State)
}
