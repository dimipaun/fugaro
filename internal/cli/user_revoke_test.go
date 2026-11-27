package cli

// user_revoke_test.go is DG5's own test file: the membership commands revoke
// the IAM grants they can reach, at once and before the list or roster edit,
// and record the removal in the roster for the grant they cannot reach.

import (
	"context"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	revokeMember = "user:member@example.com"
	revokeOther  = "user:keep@example.com"
	revokeAdmin  = "user:admin@example.com"
)

// crmPolicyMembers is the members project's IAM policy binds now: the read a
// person would make, through a client of the test's own, and never the
// command's view of what it wrote.
func crmPolicyMembers(t *testing.T, endpoint, project string) []string {
	t.Helper()
	svc, err := crm.NewService(context.Background(),
		option.WithEndpoint(endpoint+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("building the resource manager client: %v", err)
	}
	pol, err := svc.Projects.GetIamPolicy(project, &crm.GetIamPolicyRequest{}).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("reading %s's IAM policy: %v", project, err)
	}
	out := make([]string, 0)
	for _, b := range pol.Bindings {
		out = append(out, b.Members...)
	}
	return out
}

// removedRecord is what the roster's record of removed people holds now, read
// off the fake as a reader of the database would find it.
func removedRecord(t *testing.T, db *gcpfake.RTDB) []string {
	t.Helper()
	v, _ := db.Value(PathRemoved).(map[string]any)
	if v == nil {
		return nil
	}
	list, _ := v["removed"].([]any)
	out := make([]string, 0, len(list))
	for _, m := range list {
		if s, ok := m.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestUserRmRevokesAtOnce: taking a member off one of the installation's two
// lists revokes their IAM grants on its own project at once — the red step,
// which finds the grant still bound before the command does this — and records
// the removal in the roster.
func TestUserRmRevokesAtOnce(t *testing.T) {
	r := userRigFor(t, []string{revokeMember}, nil)
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	r.save(t)
	r.crm.SetPolicy(initProject, gcpfake.Binding{Role: "roles/editor", Members: []string{revokeMember, revokeOther}})
	r.before = r.configNow(t)

	out, _, err := userCommand(t, "rm", revokeMember, "--role", "operator")
	if err != nil {
		t.Fatalf("user rm: %v\n%s", err, out)
	}
	if members := crmPolicyMembers(t, r.crm.URL, initProject); len(members) != 1 || members[0] != revokeOther {
		t.Errorf("the grant is still bound after user rm: %v", members)
	}
	if removed := removedRecord(t, db); len(removed) != 1 || removed[0] != revokeMember {
		t.Errorf("the removal was not recorded in the roster: %v", removed)
	}
}

// TestRoleChangeRevokesAtOnce: a revoke that leaves a member holding no grant
// of the roster at all takes their IAM away at once and records the removal,
// exactly as `user rm` does.
func TestRoleChangeRevokesAtOnce(t *testing.T) {
	r, db := userPairRig(t, userPairLists{}, userStored(t, revokeMember, accesslib.RoleOperator, "project"))
	userPairAs(t, revokeAdmin)
	r.crm.SetPolicy(initProject, gcpfake.Binding{Role: "roles/editor", Members: []string{revokeMember}})

	out, err := userPairRun(t, "revoke", revokeMember, "operator", "project")
	if err != nil {
		t.Fatalf("user revoke: %v\n%s", err, out)
	}
	if members := crmPolicyMembers(t, r.crm.URL, initProject); len(members) != 0 {
		t.Errorf("the grant is still bound after user revoke: %v", members)
	}
	if removed := removedRecord(t, db); len(removed) != 1 || removed[0] != revokeMember {
		t.Errorf("the removal was not recorded in the roster: %v", removed)
	}
}

// TestOffboardRevokesAtOnce: offboarding removes the person from the roster
// AND revokes their IAM on the installation's own project, in that order's
// other half — the IAM first.
func TestOffboardRevokesAtOnce(t *testing.T) {
	db, crmFake := offboardRevokeRig(t, revokeAdmin,
		rosterGrant(revokeAdmin, "admin", "project"),
		rosterGrant(revokeMember, "operator", "project"),
	)
	crmFake.SetPolicy(initProject, gcpfake.Binding{Role: "roles/editor", Members: []string{revokeMember}})
	fakeIntakeCalls(t, func(c secretsIntakeCall) {
		*c.Out.(*intake.DeleteAnswer) = intake.DeleteAnswer{}
	})

	out, _, err := execute(t, "user", "offboard", strings.TrimPrefix(revokeMember, "user:"))
	if err != nil {
		t.Fatalf("user offboard: %v\n%s", err, out)
	}
	if members := crmPolicyMembers(t, crmFake.URL, initProject); len(members) != 0 {
		t.Errorf("the grant is still bound after user offboard: %v", members)
	}
	if removed := removedRecord(t, db); len(removed) != 1 || removed[0] != revokeMember {
		t.Errorf("the removal was not recorded in the roster: %v", removed)
	}
}

// TestPartialRevokeReRunConverges: a first run whose IAM half landed and whose
// record write could not (the database is unreachable) leaves the grant gone;
// running the same command again finishes the record and writes the list, so
// the state converges.
func TestPartialRevokeReRunConverges(t *testing.T) {
	r := userRigFor(t, []string{revokeMember}, nil)
	db := gcpfake.NewRTDB(t)
	dead := closedLoopbackURL(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+dead+" }\n")
	r.save(t)
	r.crm.SetPolicy(initProject, gcpfake.Binding{Role: "roles/editor", Members: []string{revokeMember}})

	if out, _, err := userCommand(t, "rm", revokeMember, "--role", "operator"); err == nil {
		t.Fatalf("the first run was not refused by the unreachable roster:\n%s", out)
	}
	if members := crmPolicyMembers(t, r.crm.URL, initProject); len(members) != 0 {
		t.Errorf("the IAM half did not land before the record failed: %v", members)
	}
	if removed := removedRecord(t, db); len(removed) != 0 {
		t.Errorf("the failed run recorded a removal anyway: %v", removed)
	}

	// The database is reachable now: the same command converges.
	now := strings.Replace(string(r.configNow(t)), dead, db.URL, 1)
	if err := os.WriteFile(r.cfg, []byte(now), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := userCommand(t, "rm", revokeMember, "--role", "operator")
	if err != nil {
		t.Fatalf("the re-run did not converge: %v\n%s", err, out)
	}
	if members := crmPolicyMembers(t, r.crm.URL, initProject); len(members) != 0 {
		t.Errorf("the re-run put the grant back: %v", members)
	}
	if removed := removedRecord(t, db); len(removed) != 1 || removed[0] != revokeMember {
		t.Errorf("the re-run did not finish the record: %v", removed)
	}
	if operators, _ := r.lists(t); len(operators) != 0 {
		t.Errorf("the re-run did not write the list: %v", operators)
	}
}

// TestUserRmKeepsTheProjectsAuditConfigs: the policy write is the policy READ
// with the member taken out of it. SetIamPolicy replaces the whole policy, so a
// policy rebuilt from its bindings alone takes the project's data-access
// logging off it.
func TestUserRmKeepsTheProjectsAuditConfigs(t *testing.T) {
	r := userRigFor(t, []string{revokeMember}, nil)
	db := gcpfake.NewRTDB(t)
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	r.save(t)
	r.crm.SetPolicy(initProject, gcpfake.Binding{Role: "roles/editor", Members: []string{revokeMember}})
	r.crm.SetAuditConfigs(initProject, gcpfake.AuditConfig{Service: "allServices",
		AuditLogConfigs: []gcpfake.AuditLogConfig{{LogType: "DATA_READ"}, {LogType: "DATA_WRITE"}}})

	if out, _, err := userCommand(t, "rm", revokeMember, "--role", "operator"); err != nil {
		t.Fatalf("user rm: %v\n%s", err, out)
	}
	want := []gcpfake.AuditConfig{{Service: "allServices",
		AuditLogConfigs: []gcpfake.AuditLogConfig{{LogType: "DATA_READ"}, {LogType: "DATA_WRITE"}}}}
	if got := r.crm.AuditConfigs(initProject); !reflect.DeepEqual(got, want) {
		t.Errorf("the audit configuration after the removal = %+v, want %+v", got, want)
	}
}

// TestDemotionRevokesAtOnce: lowering a member's role takes the dropped role's
// bindings away at once and records the removal, as a revoke of their last
// grant does: the next update re-grants what the node still names, and it
// admits the removal only against the record.
func TestDemotionRevokesAtOnce(t *testing.T) {
	r, db := userPairRig(t, userPairLists{}, userStored(t, revokeMember, accesslib.RoleAdmin, "project"))
	userPairAs(t, revokeAdmin)
	r.crm.SetPolicy(initProject, gcpfake.Binding{
		Role:    "roles/owner",
		Members: []string{revokeMember},
	})

	out, err := userPairRun(t, "grant", revokeMember, "operator", "project")
	if err != nil {
		t.Fatalf("user grant (a demotion): %v\n%s", err, out)
	}
	if members := crmPolicyMembers(t, r.crm.URL, initProject); len(members) != 0 {
		t.Errorf("the demoted role's binding is still there: %v", members)
	}
	if removed := removedRecord(t, db); len(removed) != 1 || removed[0] != revokeMember {
		t.Errorf("the demotion did not record the removal: %v", removed)
	}
}

// TestWorkspaceUserRmRecordsTheRemoval: removing a person from a workspace
// records their removal in EVERY member the workspace holds, which is what that
// member's own update reads to drop the grant the workspace cannot reach — the
// admin grant a removed workspace admin holds on it (D2818, D3454): no binding
// on a member is written from the workspace's side (D2801), so the record is the
// whole of what the workspace's removal does about it.
func TestWorkspaceUserRmRecordsTheRemoval(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "build@ci.example.com", Kind: workspace.KindSA},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}

	w.must("workspace", "user", "rm", "ada@example.com", "--workspace", "workspace-alpha")
	want := []string{"aurora user:ada@example.com", "borealis user:ada@example.com"}
	if strings.Join(w.members.recorded, "|") != strings.Join(want, "|") {
		t.Errorf("the removals recorded in the members are %v, want %v", w.members.recorded, want)
	}

	// A machine holds nothing in a member's own roster (D2806), so no member has
	// a removal to record for it.
	w.members.recorded = nil
	w.must("workspace", "user", "rm", "build@ci.example.com", "--workspace", "workspace-alpha")
	if len(w.members.recorded) != 0 {
		t.Errorf("removing a machine recorded a removal in the members: %v", w.members.recorded)
	}
}

// TestMemberUpdateRemovesRecordedGrant: a member's own update may remove a
// person's IAM grant only when the roster's record names that removal, which
// is why every membership command writes it; a grant removal nobody recorded
// is refused in one line naming the command to re-run.
func TestMemberUpdateRemovesRecordedGrant(t *testing.T) {
	db := gcpfake.NewRTDB(t)
	client := revokeClient(t, db)
	if err := recordMemberRemoval(context.Background(), revokeRigConfig(t, db), client, revokeMember); err != nil {
		t.Fatalf("recording the removal: %v", err)
	}
	removed, err := ReadRemoved(context.Background(), client)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if !RemovalRecorded(removed, revokeMember) {
		t.Fatalf("the record does not admit the recorded grant's removal: %v", removed)
	}
	if RemovalRecorded(removed, revokeOther) {
		t.Errorf("the record admits a removal nobody made: %v", removed)
	}
	err = UnexplainedGrantRemoval(initProject, revokeOther, "fugaro user rm "+revokeOther)
	if err == nil {
		t.Fatal("a grant removal the roster does not record was admitted")
	}
	if strings.Contains(err.Error(), "\n") || len(err.Error()) > 140 {
		t.Errorf("the refusal is not one short line: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "fugaro user rm") {
		t.Errorf("the refusal does not name the command to re-run: %v", err)
	}
}

// revokeClient is a database client of the fake, the way a command's own
// removalRecordClient makes one.
func revokeClient(t *testing.T, db *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	client, err := newBudgetClient(context.Background(), db.URL, true)
	if err != nil {
		t.Fatalf("building the database client: %v", err)
	}
	return client
}

// revokeRigConfig is the config recordMemberRemoval needs to reach db and
// nothing else: the budget backend is the record's own node in it.
func revokeRigConfig(t *testing.T, db *gcpfake.RTDB) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: " + initProject +
		"\nregion: us-east5\nruns_bucket: fugaro-runs-" + initProject +
		"\nbudget: { mode: observe, rtdb_url: " + db.URL + " }\nendpoints: { no_auth: true }\n"))
	if err != nil {
		t.Fatalf("the test's own config: %v", err)
	}
	return lc
}

// closedLoopbackURL is a loopback address nothing listens on: a valid rtdb_url
// whose reads fail at once, which is how a test makes the record write fail
// while the IAM half has already landed.
func closedLoopbackURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// offboardRevokeRig is offboard_test.go's own rig with the one thing a
// revocation needs that it does not carry: a Resource Manager endpoint whose
// policy a command can read and write.
func offboardRevokeRig(t *testing.T, caller string, grants ...map[string]any) (*gcpfake.RTDB, *gcpfake.CRM) {
	t.Helper()
	noAgentSession(t)
	dir := t.TempDir()
	path := isolateProjects(t, dir)
	db := gcpfake.NewRTDB(t)
	crmFake := gcpfake.NewCRM(t)
	crmFake.AddProject(initProject, initProjectNumber)
	cfg := "version: 1\nname: aurora\ngcp_project: " + initProject + "\nregion: us-east5\nruns_bucket: fugaro-runs-" + initProject + "\n" +
		"budget: { mode: observe, per_run_usd: 5, firebase_project: " + initProject + ", rtdb_url: " + db.URL + " }\n" +
		"credentials_service:\n  url: " + intakeTestCredentialsURL + "\n" +
		"endpoints: { no_auth: true, resource_manager: " + crmFake.URL + "/ }\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	stubSelfCommand(t)
	db.Set(budget.PathProject, "aurora")
	db.Set(budget.PathMark, auroraMark())
	setRosterOn(t, db, grants...)
	prev := offboardCaller
	offboardCaller = func(context.Context) (string, error) { return accesslib.Member(caller) }
	t.Cleanup(func() { offboardCaller = prev })
	return db, crmFake
}
