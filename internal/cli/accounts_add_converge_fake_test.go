package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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
      - github/owner/repo-a
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
`

// addRig is that world: the installation rig, the project's database and the
// Firestore the fixture's budget block reads its spend history through.
type addRig struct {
	*initRig
	db *gcpfake.RTDB
	fs *gcpfake.Firestore
	// managed is whether the project this rig's marks describe is one the gate
	// proves is managed. It is the one fact `add` cannot read back off the
	// filesystem — the two marks live in the fakes — and it decides whether the
	// run it makes is asked the owner's own confirmation (accounts_managed_test.go).
	managed bool
}

// newAddRig is the fixture, ready: the local config is selected (FUGARO_CONFIG,
// which newInitRig set), the database is this installation's own (the mark
// every writer into it asks for), the published rules generation the policy
// write is gated on is deployed, and the runs bucket carries the project's
// marker.
//
// The bucket is a file:// one with the marker written into it, which is the
// shape a rig that runs a command through openCloudFor needs: openCloudFor
// opens lc.BucketURL() with blobx directly, and TestMain closes the gs:// road
// (no credentials, dead proxy), so a fixture whose bucket URL is the computed
// gs:// one reaches storage.googleapis.com and fails there.
func newAddRig(t *testing.T) *addRig {
	t.Helper()
	return newAddRigWith(t, addRigLocal)
}

// newAddRigWith is newAddRig over a caller's own local config: the fixtures that
// are about the local-config half of the command write their own file, and the
// command converges on the file it wrote whatever it holds.
func newAddRigWith(t *testing.T, local string) *addRig {
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
	bucket := filepath.Join(r.dir, "runs")
	cfg := local +
		"bucket_url: file://" + bucket + "\n" +
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: " + initProject + ", rtdb_url: " + r.db.URL + " }\n" +
		"endpoints: " + strings.Replace(endpoints, "no_auth: true }", "firestore: "+r.fs.URL+", no_auth: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// The mark, the database's project and the bucket's marker are the ones the
	// config just written names, read out of it rather than assumed: the command
	// refuses a project whose marker says another GCP project, and a fixture
	// that hard-coded the rig's own would refuse the fixtures whose file is
	// their own.
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The occurrence the installation reads before it applies anything, for a
	// fixture whose own file names a project the rig was not built around.
	if lc.GCPProject != initProject {
		r.crm.AddProject(lc.GCPProject, initProjectNumber)
		r.gcs.AddProject(lc.GCPProject, initProjectNumber)
	}
	r.db.Set(budget.PathMark, auroraMark())
	r.db.Set(budget.PathProject, lc.Name)
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	// The node an installation that has run `fugaro setup` holds: the publish
	// half of the Policy stage is a MIGRATION of a node that is there — with no
	// node the stage has nothing to rewrite and reports Done — so a fixture
	// that means the converge to reach the node seeds one. Its own allow_repos
	// is the node's (a publish never re-adds or drops a name), and everything
	// else the converge puts there is derived from the local config it writes.
	r.db.Set(projectpolicy.PathShared(), addPolicyNode())
	r.writeMarker(t, bucket, lc.Name, lc.GCPProject)
	return r
}

// writeMarker writes the project marker the way the installation's Terraform
// does, into the file:// bucket the fixture's config names: a command opened
// through openCloudFor reads it before it does anything else.
func (r *addRig) writeMarker(t *testing.T, bucket, name, gcp string) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: name, GCPProject: gcp})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bucket, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// addPolicyNode is the published node an installation this fixture stands for
// already holds: at this Fugaro's version, with the project's own allow_repos
// and a budget block the local config agrees with, and with no accounts block
// — the state every installation is in before its first named account.
func addPolicyNode() map[string]any {
	return map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/owner/repo-a"},
	}
}

// addToken is the value a test writes into the secret: built here rather than
// written down, because a token-shaped literal in a fixture is what the rule
// against them is about, and no assertion of this file prints it.
func addToken() string { return strings.Repeat("k", 48) }

// add runs the command the way the OWNER does — a real terminal on both
// streams, the project's own name typed at the one confirmation, the token at
// the hidden prompt — and returns what it printed.
//
// It is the owner's run and not a scripted one because a managed project's
// `accounts add` is refused to every caller that is not that person (the
// owner's ruling of 2026-11-09): a fixture that seeded one through --yes or
// through a pipe would be seeding it through a door the step has closed, and
// would pass only on a build with that door open.
func (r *addRig) add(t *testing.T, name string, args ...string) (string, string) {
	t.Helper()
	// What is typed, and where the run reads it, is what the project asks for.
	// A managed project asks its owner to confirm the account first, and only a
	// person at a real terminal is asked — so the fixture stands at both
	// streams and answers the confirmation. An ordinary installation has no
	// gate to answer and no question at all: it keeps the scripted shape it has
	// always had, --yes for the installation stages below.
	typed := addToken() + "\n"
	argv := append([]string{"accounts", "add", name}, args...)
	if r.managed {
		ownerAtTheTerminal(t)
		typed = initProjectName + "\n" + typed
	} else {
		argv = append(argv, "--yes")
	}
	out, errOut, err := executeOwnerStdin(t, typed, argv...)
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

// secretWrites is the Secret Manager fake's own record of the writes a command
// made: the IDs it created a secret under, and the IDs it added a version to,
// in order. It is the fake's callbacks rather than a parse of the request log,
// so what a test asserts about "which secret did this land under" cannot drift
// with a URL shape.
type secretWrites struct {
	created []string
	added   []string
}

// secretWrites arms the fake and returns the record. A write that never opened
// a connection (a refusal that happens before the value is read) leaves both
// lists empty, which is the negative half of the same assertion.
func (r *addRig) secretWrites(t *testing.T) *secretWrites {
	t.Helper()
	w := &secretWrites{}
	r.sm.OnCreate = func(id string) { w.created = append(w.created, id) }
	r.sm.OnAddVersion = func(id string) { w.added = append(w.added, id) }
	return w
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
