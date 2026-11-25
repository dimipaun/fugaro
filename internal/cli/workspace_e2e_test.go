package cli

// workspace_e2e_test.go is WU21: the whole `fugaro workspace` family driven as a
// person drives it, against internal/gcpfake and nothing else.
//
// Every other test of this family builds one command with its own seams, which is
// what keeps each of them readable and fast. This one is the join: the REAL
// command tree (NewRootCmd, through execute), the REAL config home, the REAL
// wiring I1 installed, and fakes only where Google is (Resource Manager's
// folders, projects and policies, IAM, Cloud Storage, Firestore, the Realtime
// Database, Cloud Logging, Scheduler, Secret Manager and Service Usage). Nothing
// is stubbed that a command could have stubbed wrong: a piece left unwired fails
// here, which is the whole point of the task.
//
// The chain is the plan's acceptance, in its order: a workspace initialized on
// the folder mechanism, a member declared and then linked by its own owner,
// adopted, a person refused and then admitted with --external, an admin granted,
// the member's own `fugaro update` simulated by what it leaves behind (its
// installation outputs, the admin list it publishes and the bindings its own
// Terraform applies), `status` clean, then a binding added straight in the fake
// -- which `status` names as drift and as the console change the audit log holds
// -- `reconcile`, the person removed, the admin removed and the member removed
// with its state saved and nothing deleted.
//
// **--no-apply, and why.** The writes here pass it. What it turns off is the
// converge a change chains (AP6): Terraform, the images and the plugin step of
// the machine that ran the command. That converge is another task's subject
// (`fugaro update`'s own tests), it needs nothing this chain asserts, and a
// workspace's member is converged by the MEMBER's own `fugaro update` anyway --
// which is exactly what this file simulates (F7, D2801: no installation applies
// another's Terraform).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	// The workspace's own project, which is the project the workspace is named
	// after (F21, D1290): the config its owner's half lives in, the node its
	// roster is written to, and the database its trail's entries name.
	wu21Workspace = "workspace-alpha"
	// The member: one Google Cloud project that is also its Firebase project
	// (F2, R10), which is why C2's MemberTarget carries no second name for it.
	wu21Member = "member-a"
	// A repository the member contributes and the job service account the
	// workspace's service resolves a caller by.
	wu21Repo       = "acme/member-a"
	wu21JobAccount = "fugaro-job@member-a.iam.gserviceaccount.com"
	wu21Exchange   = "https://exchange.example.com/v1"
	// The folder the member's projects hang under before the link, under the
	// organization `init` creates the workspace's own folder in.
	wu21Org         = "123"
	wu21OldFolder   = "55"
	wu21WorkspaceSA = "fugaro-workspace@workspace-alpha.iam.gserviceaccount.com"
	// The four people of this world: the workspace's founder and first admin,
	// the member's own owner (a grantee of the member's roster, adopted by the
	// second add), a second workspace admin, and somebody outside the
	// workspace's allowed domains (admitted only with --external).
	wu21Founder  = "founder@example.com"
	wu21Grantee  = "member-owner@example.com"
	wu21Deputy   = "deputy@example.com"
	wu21Outsider = "friend@outside.test"
	// The principal a binding is added for straight in the fake, as somebody
	// with the console open would.
	wu21Console = "mallory@corp.example"
	// What the member's own update leaves behind: the Cloud Run address of its
	// intake, which the workspace's sweeps call, and the region its config
	// records.
	wu21IntakeURL = "https://fugaro-intake-member-a-europe-west1.a.run.app"
	wu21Region    = "europe-west1"
)

// wu21World is the whole world one run of this test drives: the fakes, the
// config home with both projects' files in it, and the two databases.
type wu21World struct {
	t   *testing.T
	dir string
	// owner and member are the two configs' paths: the workspace's own project's
	// and the member's, which is the one `link` runs from.
	owner, member string

	crm   *gcpfake.CRM
	iam   *gcpfake.IAM
	gcs   *gcpfake.GCS
	logs  *gcpfake.Logging
	fs    *gcpfake.Firestore
	sched *gcpfake.Scheduler
	sm    *gcpfake.Secrets
	su    *gcpfake.ServiceUsage
	// wsdb is the workspace's own database (its roster and the trail it files)
	// and mbdb the member's, whose address the member's own Firebase state names.
	wsdb, mbdb *gcpfake.RTDB
	// swept is every address the two sweeps of one removal named.
	swept []string
}

// wu21New is the world before the chain runs: the fakes, the two config files,
// the organization, the member's old folder and the projects a folder can hold.
func wu21New(t *testing.T) *wu21World {
	t.Helper()
	w := &wu21World{
		t: t, dir: t.TempDir(),
		crm: gcpfake.NewCRM(t), iam: gcpfake.NewIAM(t), gcs: gcpfake.NewGCS(t),
		logs: gcpfake.NewLogging(t), fs: gcpfake.NewFirestore(t), sched: gcpfake.NewScheduler(t),
		sm: gcpfake.NewSecrets(t), su: gcpfake.NewServiceUsage(t),
		wsdb: gcpfake.NewRTDB(t), mbdb: gcpfake.NewRTDB(t),
	}
	// The commands' own fences come first in their bodies and are about who is
	// running: a coding agent's session is refused, and a test binary that
	// happened to inherit a marker would meet that refusal instead of the chain.
	// The markers are cleared here, as the family's other fixtures do.
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "HOME"} {
		t.Setenv(k, filepath.Join(w.dir, strings.ToLower(k)))
	}
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GODEBUG", "FUGARO_PROJECT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// The caller's own address is the founder's: it is who the credentials
	// belong to, and the one thing a fake cannot answer.
	oldUser := authenticatedUser
	t.Cleanup(func() { authenticatedUser = oldUser })
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return wu21Founder, nil }
	// The admin gate reads the caller from the credential rather than from the
	// config (F21), and its reader is a seam of its own: a test that answered it
	// with the config's own key would be testing the courtesy the design
	// refuses.
	oldLookup := userCallerLookup
	t.Cleanup(func() { userCallerLookup = oldLookup })
	userCallerLookup = func(context.Context) (string, error) { return "user:" + wu21Founder, nil }
	// The same caller, read by the two other seams a workspace command files an
	// audit entry or sweeps a grant through: the reconcile's actor (an address,
	// never the member form) and the offboarding door's caller.
	oldReconcile := workspaceReconcileActor
	t.Cleanup(func() { workspaceReconcileActor = oldReconcile })
	workspaceReconcileActor = func(context.Context) (string, error) { return wu21Founder, nil }
	oldOffboard := offboardCaller
	t.Cleanup(func() { offboardCaller = oldOffboard })
	offboardCaller = func(context.Context) (string, error) { return "user:" + wu21Founder, nil }
	// Every call this family makes to the secrets intake goes through one seam,
	// and no test reaches the service it names.
	w.stubIntake()
	// The layer read of a selection opens the project's bucket; no test reaches a
	// real one (TestMain refuses it) and nothing here publishes a layer.
	settingsBucketAt(t)
	// The save a `project rm` makes pushes with the owner's own git and GitHub
	// CLI (D2807), so the two are the test's own on PATH, as WU15's tests put
	// them: nothing of Fugaro's holds a token and no test reaches a host.
	wu15FakeGit(t)
	wu15FakeGH(t, "PRIVATE")

	w.owner = w.writeConfig(t, wu21Workspace, wu21Workspace, w.wsdb.URL, "")
	// The member's own config, which is the config the save before a removal is
	// built from (WU15's writerFor loads the snapshot's own project's config):
	// its membership, and the repository it designates for committed reports,
	// which is what a save has to have somewhere to push to (D2750, D2824).
	w.member = w.writeConfig(t, wu21Member, wu21Member, w.mbdb.URL, wu21Membership()+wu21CommitReports())

	w.crm.AddOrganization(wu21Org)
	w.crm.AddProject(wu21Workspace, 900000000001)
	w.crm.AddProject(wu21Member, 900000000002)
	w.crm.SetParent(wu21Workspace, "organizations/"+wu21Org)
	// The member's project hangs under a folder of the organization before the
	// link, and that folder carries an owner of its own: the policy change the
	// member's owner consents to at `link` is the difference between the two.
	w.crm.AddFolder(wu21OldFolder, "member-parent", "organizations/"+wu21Org)
	w.crm.SetParent(wu21Member, "folders/"+wu21OldFolder)
	w.crm.SetFolderPolicy(wu21OldFolder, gcpfake.Binding{
		Role: "roles/owner", Members: []string{"user:" + wu21Grantee},
	})

	// The member's own installation: its state bucket, its runs bucket and the
	// one state document its `fugaro update` has left there already. The
	// installation root's own outputs arrive with the update this test
	// simulates, because that is the step they belong to.
	w.gcs.AddProject(wu21Member, 900000000002)
	w.gcs.AddBucket(managedmode.InstallationsStateBucket(wu21Member), 900000000002, nil)
	w.gcs.AddBucket(wu21RunsBucket(), 900000000002, nil)
	w.seedFirebaseState(w.mbdb.URL)
	// Each database names its own project at /fugaro/project, which every command
	// that opens one checks before it reads (plan R-9): the workspace's own
	// installation and the member's.
	w.wsdb.Set(budget.PathProject, wu21Workspace)
	w.mbdb.Set(budget.PathProject, wu21Member)
	w.selectOwner(t)
	return w
}

// wu21Membership is the member's own statement of which workspace it trusts
// (F2, R10): the workspace's project, the issuer its tokens carry, the key it
// verifies an assertion against and the account that acts for it there.
func wu21Membership() string {
	return "workspace: " + wu21Workspace + "\n" +
		"workspace_pin:\n" +
		"  firebase_project: " + wu21Workspace + "\n" +
		"  issuer: " + localcfg.WorkspaceIssuer(wu21Workspace) + "\n" +
		"  assertion_key: wu21-not-a-real-assertion-key\n" +
		"  service_account: " + wu21WorkspaceSA + "\n"
}

// wu21CommitReports is the member's designation of a repository for committed
// reports, which the save before its removal pushes the snapshot into.
func wu21CommitReports() string {
	return "reports_commit:\n  repo: acme/state\n  dir: docs/state\n"
}

// wu21RunsBucket is the member's own runs bucket, where its `fugaro update`
// publishes the derived admin list the workspace's commands read.
func wu21RunsBucket() string { return "fugaro-runs-" + wu21Member }

// writeConfig writes one project's config, pointed at every fake. extra is what
// only one of the two carries: the member's membership, and the workspace's own
// credentials service, which is the address its own sweep is read from.
func (w *wu21World) writeConfig(t *testing.T, name, gcp, rtdbURL, extra string) string {
	t.Helper()
	path, err := localcfg.ProjectPath(os.Getenv, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "version: 1\nname: " + name + "\ngcp_project: " + gcp + "\nregion: us-east5\n" +
		"user: " + wu21Founder + "\n" +
		"runs_bucket: fugaro-runs-" + gcp + "\n" +
		"budget:\n  rtdb_url: " + rtdbURL + "\n  firebase_project: " + gcp + "\n" +
		"credentials_service:\n  url: https://fugaro-credentials-wu21-" + wu21Region + ".a.run.app\n" +
		"endpoints: { run: " + w.sched.URL + "/, secret_manager: " + w.sm.URL + "/, storage: " + w.gcs.URL +
		"/storage/v1/, iam: " + w.iam.URL + "/, artifact_registry: " + w.crm.URL + "/, resource_manager: " + w.crm.URL +
		"/, logging: " + w.logs.URL + "/, cloud_scheduler: " + w.sched.URL + "/, service_usage: " + w.su.URL +
		"/, firestore: " + w.fs.URL + "/, cloud_billing: " + w.crm.URL + "/, firebase_management: " + w.crm.URL +
		"/, no_auth: true }\n" + extra
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// selectOwner selects the workspace's own config, and selectMember the
// member's: the ambient selection is what `project rm`, `save`, `reconcile` and
// `link` read, and the two halves of this chain run under different selections.
func (w *wu21World) selectOwner(t *testing.T) {
	t.Helper()
	t.Setenv("FUGARO_CONFIG", w.owner)
}

func (w *wu21World) selectMember(t *testing.T) {
	t.Helper()
	t.Setenv("FUGARO_CONFIG", w.member)
}

// stubIntake answers every secrets-intake call from the fakes: a sweep is the
// admin-only delete (DS2.4), and the containers it names are the ones the
// address's own prefix holds.
func (w *wu21World) stubIntake() {
	old := secretsIntakeDo
	w.t.Cleanup(func() { secretsIntakeDo = old })
	secretsIntakeDo = func(_ context.Context, serviceURL string, c secretsIntakeCall) error {
		req, ok := c.Body.(intake.DeleteRequest)
		if !ok {
			w.t.Fatalf("the workspace family called the intake at %s with %T, which is not one of its own requests", serviceURL, c.Body)
		}
		w.swept = append(w.swept, req.Owner)
		answer, ok := c.Out.(*intake.DeleteAnswer)
		if !ok {
			w.t.Fatalf("the workspace family read the intake's answer into %T", c.Out)
		}
		// The name the intake's owner annotation makes: one container per
		// stored credential, under the member's own prefix.
		answer.Deleted = []string{"fugaro_u_4242_" + strings.Split(req.Owner, "@")[0] + "-github-token"}
		return nil
	}
}

// wu21StateDocument wraps outputs as a Terraform state document holds them.
func wu21StateDocument(t *testing.T, outputs map[string]any) []byte {
	t.Helper()
	boxed := make(map[string]any, len(outputs))
	for name, v := range outputs {
		boxed[name] = map[string]any{"value": v, "type": "string"}
	}
	raw, err := json.Marshal(map[string]any{
		"version": 4, "terraform_version": "1.9.8", "serial": 1, "lineage": "wu21",
		"outputs": boxed, "resources": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// seedFirebaseState writes the member's Firebase root's outputs, so the address
// of the member's own database is known and the state is read as this member's.
func (w *wu21World) seedFirebaseState(rtdbURL string) {
	w.gcs.Put(managedmode.InstallationsStateBucket(wu21Member), infra.StatePrefixFirebase+"/default.tfstate",
		wu21StateDocument(w.t, map[string]any{
			"rtdb_url":         rtdbURL,
			"firebase_api_key": "wu21-not-a-real-key",
			"token_signer":     infra.SignerAccountID + "@" + wu21Member + ".iam.gserviceaccount.com",
			"firebase_project": wu21Member,
		}))
}

// memberUpdate is the member's own `fugaro update`, simulated by the three
// things it leaves behind: its installation root's outputs (the launcher and
// operator lists, the runs bucket the admin list is published into, and its
// intake), the admin list derived from its own grants node, and the bindings its
// own Terraform applies out of that node.
//
// Nothing here invents a fact the member's update would not have: the list is
// access.Admins over the node the workspace's commands wrote, and the bindings
// are the record's admins as owners of the member's project -- which is what a
// member whose workspace holds it by a folder applies, and what makes the
// member's policy agree with the record (D2801, D2818).
func (w *wu21World) memberUpdate(t *testing.T, launchers, operators []string) {
	t.Helper()
	w.gcs.Put(managedmode.InstallationsStateBucket(wu21Member), infra.StatePrefixInstallation+"/default.tfstate",
		wu21StateDocument(t, map[string]any{
			"project_name":    wu21Member,
			"runs_bucket":     wu21RunsBucket(),
			"launchers":       launchers,
			"operators":       operators,
			"intake":          map[string]any{"name": "fugaro-intake", "region": wu21Region, "url": wu21IntakeURL},
			"shared_registry": "europe-west1-docker.pkg.dev/" + wu21Member + "/fugaro",
		}))
	grants := w.memberGrants(t)
	raw, err := accesslib.EncodeAdminList(accesslib.Admins(grants))
	if err != nil {
		t.Fatal(err)
	}
	w.gcs.Put(wu21RunsBucket(), accesslib.PathAdminList, raw)
	bindings := make([]gcpfake.Binding, 0, len(grants))
	for _, g := range grants {
		if g.Role != accesslib.RoleAdmin || g.Scope.Kind != accesslib.ScopeProject {
			continue
		}
		bindings = append(bindings, gcpfake.Binding{
			Role: "roles/owner", Members: []string{g.User},
		})
	}
	w.crm.SetPolicy(wu21Member, bindings...)
}

// memberGrants reads the member's own roster node, which is what its update
// derives the admin list and its bindings from.
func (w *wu21World) memberGrants(t *testing.T) []accesslib.Grant {
	t.Helper()
	node, err := accesslib.Read(t.Context(), w.memberDB(t))
	if err != nil {
		t.Fatalf("reading the member's grants node at %s: %v", accesslib.PathNode, err)
	}
	return node.Grants
}

// memberDB opens the member's own database as the test's own client, so a test
// can seed and read back what the commands wrote there.
func (w *wu21World) memberDB(t *testing.T) *rtdb.Client {
	t.Helper()
	c, err := newBudgetClient(t.Context(), w.mbdb.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// roster reads the workspace's own node, which records the mechanism, the
// folder and the people.
func (w *wu21World) roster(t *testing.T) *workspace.Roster {
	t.Helper()
	db, err := newBudgetClient(t.Context(), w.wsdb.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	path, err := workspace.PathRoster(wu21Workspace)
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	if _, err := db.Get(t.Context(), path, &raw); err != nil {
		t.Fatalf("reading the workspace's roster at %s: %v", path, err)
	}
	roster, err := workspace.DecodeRoster(raw)
	if err != nil {
		t.Fatal(err)
	}
	return roster
}

// status is `workspace project status --json` as the document it prints, which
// is how this file reads a row: the exit status is the command's own verdict,
// and the rows say which fact it was.
func (w *wu21World) status() wsProjectsDoc {
	w.t.Helper()
	out, _ := w.run("workspace", "project", "status", "--json")
	var doc wsProjectsDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		w.t.Fatalf("workspace project status --json printed no document this test reads: %v\n%s", err, out)
	}
	return doc
}

// memberRow is one member's answer out of that document, and row one row of it.
func memberRow(t *testing.T, doc wsProjectsDoc, project string) wsProjectMember {
	t.Helper()
	for _, m := range doc.Members {
		if m.Project == project {
			return m
		}
	}
	t.Fatalf("the status document holds no row for %s: %+v", project, doc.Members)
	return wsProjectMember{}
}

func wu21Row(t *testing.T, m wsProjectMember, id string) wsProjectRow {
	t.Helper()
	for _, r := range m.Rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("member %s has no %s row: %+v", m.Project, id, m.Rows)
	return wsProjectRow{}
}

// run runs one command of the chain and fails the test with the command line it
// ran when the command refused.
func (w *wu21World) run(args ...string) (string, string) {
	w.t.Helper()
	out, errOut, err := execute(w.t, args...)
	if err != nil {
		w.t.Fatalf("fugaro %s:\n%v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
	}
	return out, errOut
}

// refuses runs one command that must not land, and answers what it said.
func (w *wu21World) refuses(args ...string) error {
	w.t.Helper()
	_, _, err := execute(w.t, args...)
	if err == nil {
		w.t.Fatalf("fugaro %s = nil, want a refusal", strings.Join(args, " "))
	}
	return err
}

// contains is the shape every assertion below makes: one fact, one message.
func carries(t *testing.T, what, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to carry %q", what, got, want)
	}
}

// addArgs is the one declaration this chain makes twice: the workspace's owner
// declares the member, its own owner links, and the owner declares it again so
// that the add adopts what the member already carries.
func wu21AddArgs() []string {
	return []string{
		"workspace", "project", "add", wu21Member,
		"--workspace", wu21Workspace,
		"--firebase-project", wu21Member,
		"--fp", wu21Member,
		"--database-region", wu21Region,
		"--exchange-url", wu21Exchange,
		"--job-service-account", wu21JobAccount,
		"--job-repos", wu21JobAccount + "=" + wu21Repo,
		"--repo", wu21Repo,
		"--no-apply",
	}
}

// TestWorkspaceProjectsUsersEndToEnd is WU21's acceptance, in its order.
func TestWorkspaceProjectsUsersEndToEnd(t *testing.T) {
	w := wu21New(t)

	// --- init (folder) ------------------------------------------------------
	// The workspace's first write: the caller's own credentials see an
	// organization, so the mechanism is the folder, the caller is bound to
	// roles/owner on it, and the workspace's own project is placed in it (R8).
	initOut, _ := w.run("workspace", "init", wu21Workspace)
	carries(t, "workspace init", initOut, "the folder mechanism")
	roster := w.roster(t)
	if roster.Mechanism != workspace.MechanismFolder || roster.FolderID == "" {
		t.Fatalf("the roster records %q and the folder %q, want the folder mechanism and a folder", roster.Mechanism, roster.FolderID)
	}
	folder := roster.FolderID
	// The workspace's own project sits in the folder it just made.
	if got := w.crm.ProjectParent(wu21Workspace); got != folder {
		t.Fatalf("the workspace's own project hangs under %s, want the folder %s", got, folder)
	}
	// The live binding is written before the roster, and it is the caller's:
	// this is the binding the admin gate is confirmed by (D2823).
	if got := w.crm.FolderPolicy(wu21FolderID(folder)); !wu21Holds(got, "roles/owner", "user:"+wu21Founder) {
		t.Fatalf("the folder's policy is %+v, want the caller bound to roles/owner on it", got)
	}
	// A second init creates nothing: the mechanism and the folder are written
	// once (R6).
	carries(t, "a second workspace init", w.refuses("workspace", "init", wu21Workspace).Error(), "already holds")

	// --- domains, and the member declared (waiting for its owner) -----------
	// A workspace whose allowed domains are empty admits nobody, and half the
	// chain is about who may be admitted.
	w.run("workspace", "domains", "add", "example.com", "--workspace", wu21Workspace)
	// The member's own roster already holds a grant for its owner, at a scope
	// that is the member's own business: the second add adopts that person.
	w.seedMemberRoster(t)

	// `project add` declares the member in the workspace's own table. Nothing of
	// the member's is read here: the authority is given at `link`, by the
	// member's own owner on their own machine (D2800, D2819).
	declared, _ := w.run(wu21AddArgs()...)
	carries(t, "workspace project add (unlinked)", declared, "fugaro workspace project link")

	// --- link (member owner) ------------------------------------------------
	// The member's owner runs it from the member's own machine, with the
	// member's own config: the pin is this project's own statement of the
	// workspace it trusts.
	w.selectMember(t)
	linkOut, _ := w.run("workspace", "project", "link")
	carries(t, "workspace project link", linkOut, "the project sits in the workspace's folder")
	carries(t, "workspace project link", linkOut, "fugaro workspace project add")
	if got := w.crm.ProjectParent(wu21Member); got != folder {
		t.Fatalf("the member hangs under %s after the link, want the workspace's folder %s", got, folder)
	}
	linked, _ := w.run(wu21AddArgs()...)
	carries(t, "workspace project add (linked)", linked, "adopted 1 person")

	// The old doors: on the member's OWN machine, whose config carries the
	// membership and whose database carries the workspace's member list, each
	// installation-level command refuses and names the workspace's (WU10, D2813).
	w.selectMember(t)
	carries(t, "fugaro user grant on a linked member",
		w.refuses("user", "grant", "user:"+wu21Deputy, "--role", "viewer", "--scope", "project").Error(), "workspace user grant")
	w.selectOwner(t)

	// --- user add: refused, then --external ---------------------------------
	refused := w.refuses("workspace", "user", "add", wu21Outsider, "--workspace", wu21Workspace).Error()
	carries(t, "workspace user add (outside the domains)", refused, "--external")
	carries(t, "workspace user add (outside the domains)", refused, "example.com")
	admitted, _ := w.run("workspace", "user", "add", wu21Outsider, "--workspace", wu21Workspace, "--external")
	carries(t, "workspace user add --external", admitted, "external")

	// --- the second admin, whose grant is pending ---------------------------
	// D2818's pair: the live binding at once, and a stored project-scope admin
	// grant written into every member the workspace holds -- which that member's
	// own update is what applies.
	granted, _ := w.run("workspace", "user", "add", wu21Deputy, "--workspace", wu21Workspace, "--admin")
	carries(t, "workspace user add --admin", granted, "their live binding")
	carries(t, "workspace user add --admin", granted, "member's own fugaro update is what applies it")
	if got := w.crm.FolderPolicy(wu21FolderID(folder)); !wu21Holds(got, "roles/owner", "user:"+wu21Deputy) {
		t.Fatalf("the folder's policy is %+v, want the new admin bound to roles/owner on it", got)
	}
	// The member has not run its own update yet, so what the record states for
	// it is pending there -- and the row says so rather than reading the record
	// as applied.
	before := wu21Row(t, memberRow(t, w.status(), wu21Member), rowPending)
	if before.Status == wsPass {
		t.Fatalf("the pending row passed before the member's own update ran: %+v", before)
	}
	for _, address := range []string{wu21Founder, wu21Deputy} {
		carries(t, "the pending row before the member's update", before.Detail, address)
	}

	// --- the member's own fugaro update ------------------------------------
	w.memberUpdate(t, []string{wu21Grantee}, []string{wu21Outsider})

	// --- status (clean) -----------------------------------------------------
	// Every row the member's own answer decides is measured now, and nothing
	// disagrees: the record's people hold the member's own bindings, the
	// member's roster holds no grant the record does not state, and no
	// SetIamPolicy change is unaccounted for.
	clean := w.status()
	member := memberRow(t, clean, wu21Member)
	if member.Worst == wsFail {
		t.Fatalf("the member has a failing row after its own update: %+v", member.Rows)
	}
	drift := wu21Row(t, member, rowDrift)
	carries(t, "the drift row", drift.Detail, "matches the record")
	// The record's admins were applied by the member's own update, so nothing
	// the record states is waiting there any more -- and the row says what it
	// did not measure rather than reading silence as agreement.
	after := wu21Row(t, member, rowPending)
	carries(t, "the pending row after the member's update", after.Detail, "nothing the record states is waiting")
	if strings.Contains(after.Detail, wu21Deputy) {
		t.Errorf("the pending row still names %s after the member's own update: %q", wu21Deputy, after.Detail)
	}

	// --- a binding added straight in the fake -------------------------------
	// Somebody with the console open gives a person the member's project, and
	// the audit log records it. Fugaro never made this change, so nothing of
	// its trails holds it.
	at := time.Now().UTC()
	w.crm.SetPolicy(wu21Member,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:" + wu21Founder}},
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:" + wu21Deputy}},
		gcpfake.Binding{Role: "roles/viewer", Members: []string{"user:" + wu21Console}},
	)
	w.logs.AddAudit(wu21Member, gcpfake.AuditEntry{
		Time: at, Caller: "admin@corp.example", InsertID: "wu21-console-entry",
		Deltas: []gcpfake.BindingDelta{{Action: "ADD", Role: "roles/viewer", Member: "user:" + wu21Console}},
	})

	// --- status names the drift and the audit-log entry ---------------------
	drifted := wu21Row(t, memberRow(t, w.status(), wu21Member), rowDrift)
	carries(t, "the drift row after the console change", drifted.Detail, wu21Console)
	carries(t, "the drift row after the console change", drifted.Detail, "changed in the console")
	carries(t, "the drift row after the console change", drifted.Detail, "wu21-console-entry")

	// --- reconcile ----------------------------------------------------------
	// Reconcile writes the member's own roster and nothing else: what the
	// console left is reported, not removed.
	reconcileOut, _ := w.run("workspace", "project", "reconcile", wu21Member)
	carries(t, "workspace project reconcile", reconcileOut, "report")

	// --- user rm: the prefix is swept ---------------------------------------
	// The member's own update published the derived admin list, so the caller
	// is one the member's intake admits and the sweep is made (D2818, D1940).
	rmOut, _ := w.run("workspace", "user", "rm", wu21Outsider, "--workspace", wu21Workspace)
	carries(t, "workspace user rm", rmOut, "removed 1 secrets "+wu21Outsider+" stored")
	carries(t, "workspace user rm", rmOut, "stored at the workspace")
	if len(w.swept) < 2 || w.swept[0] != wu21Outsider {
		t.Fatalf("the sweeps named %v, want the member's own intake and the workspace's for %s", w.swept, wu21Outsider)
	}
	afterRm := w.roster(t)
	for _, p := range afterRm.People {
		if p.Address == wu21Outsider {
			t.Fatalf("%s is still a person of the roster after their removal: %+v", wu21Outsider, afterRm.People)
		}
	}

	// --- the admin removal --------------------------------------------------
	// The live binding goes at once and the member's grant is queued for the
	// member's own update to take away (D2818 as corrected).
	adminOut, _ := w.run("workspace", "user", "rm", wu21Deputy, "--workspace", wu21Workspace, "--admin")
	carries(t, "workspace user rm --admin", adminOut, "workspace admin")
	if got := w.crm.FolderPolicy(wu21FolderID(folder)); wu21Holds(got, "roles/owner", "user:"+wu21Deputy) {
		t.Fatalf("the folder's policy is still %+v, want the removed admin's binding gone", got)
	}
	if got := w.roster(t).Admins; len(got) != 1 || got[0] != wu21Founder {
		t.Fatalf("the roster's admins are %v, want the founder alone", got)
	}
	queued := false
	for _, g := range w.memberGrants(t) {
		if g.User == "user:"+wu21Deputy {
			t.Fatalf("the member's roster still holds %s's grant, want it queued for removal", wu21Deputy)
		}
		if g.User == "user:"+wu21Founder && g.Role == accesslib.RoleAdmin {
			queued = true
		}
	}
	if !queued {
		t.Fatalf("the member's roster no longer holds the founder's admin grant: %+v", w.memberGrants(t))
	}

	// --- project rm: the state is saved, nothing is deleted -----------------
	rmOut, _ = w.run("workspace", "project", "rm", wu21Member, "--no-apply")
	carries(t, "workspace project rm", rmOut, "saved "+wu21Member+"'s state")
	if _, found := workspaceTable(w.ownerConfig(t))[wu21Member]; found {
		t.Fatalf("the workspace's table still declares %s after its removal", wu21Member)
	}
	// Nothing of Google's is gone: the project is where it was, in the
	// workspace's folder.
	if got := w.crm.ProjectParent(wu21Member); got != folder {
		t.Fatalf("the member hangs under %s after project rm, want it left in the folder %s", got, folder)
	}
}

// seedMemberRoster writes the member's own grants node, which is the list its
// own `fugaro update` migrates and the second add adopts from. The grant is at
// the all-repos scope, so it is the member's own business and not a statement
// about the member's project's IAM (D2805, C2's MemberGrant).
func (w *wu21World) seedMemberRoster(t *testing.T) {
	t.Helper()
	grants := []accesslib.Grant{{
		User: "user:" + wu21Grantee, Role: accesslib.RoleAdmin, Scope: accesslib.Scope{Kind: accesslib.ScopeAllRepos},
	}}
	db := w.memberDB(t)
	node, err := accesslib.Read(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := accesslib.WriteIfMatch(t.Context(), db, grants, node.ETag); err != nil {
		t.Fatalf("seeding the member's grants node: %v", err)
	}
}

// ownerConfig reads the workspace's own config back, which is the table this
// chain declared the member in.
func (w *wu21World) ownerConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, _, err := localcfg.LoadProject(os.Getenv, wu21Workspace)
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// wu21FolderID is a folder's resource name without its `folders/` prefix,
// which is the id the fake's own accessors are keyed by.
func wu21FolderID(name string) string { return strings.TrimPrefix(name, "folders/") }

// wu21Holds reports whether a policy binds member to role.
func wu21Holds(policy []gcpfake.Binding, role, member string) bool {
	for _, b := range policy {
		if b.Role != role {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return true
			}
		}
	}
	return false
}
