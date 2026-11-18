package cli

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// The orchestrator-identity check (mobile plan task P5.1b): the resident
// orchestrator's actual IAM read back against the table the mobile orchestrator
// design's section 4.3 fixes as the whole of what the account may hold.
//
// The rig is an installation whose Firebase backend is its own project: the
// launcher list's rules-bypassing database read is bound there rather than in
// the installation project, the budget signer lives there, and both are scopes
// this check reads beside the installation's own.
const (
	oiAccount = "fugaro-orch@proj-1234.iam.gserviceaccount.com"
	oiFP      = "aurora-fp"
	oiSigner  = "fugaro-token-signer@" + oiFP + ".iam.gserviceaccount.com"

	oiLauncherRole = "projects/proj-1234/roles/fugaroLauncher"
	oiRunnerRole   = "projects/proj-1234/roles/fugaroJobRunner"
	oiMinterRole   = "projects/" + oiFP + "/roles/fugaroTokenMinter"
	oiInvokerRole  = "roles/run.invoker"

	oiBucket = "fugaro-runs-proj-1234"
	oiJob    = "fugaroweb"

	oiMember = "serviceAccount:" + oiAccount
)

type orchestratorIdentityRig struct {
	*signerRig
	gcs  *gcpfake.GCS
	svcs *gcpfake.RunServices

	// The two project policies the tests build up, so that a case can add one
	// binding without spelling out the rest of the policy.
	projBindings []gcpfake.Binding
	fpBindings   []gcpfake.Binding
}

func newOrchestratorIdentityRig(t *testing.T) *orchestratorIdentityRig {
	t.Helper()
	r := &orchestratorIdentityRig{signerRig: newSignerDoctorRig(t), gcs: gcpfake.NewGCS(t), svcs: gcpfake.NewRunServices(t)}
	r.crm.AddProject(oiFP, 123456789013)
	r.withBudget(t, oiFP, oiSigner)
	r.withStorage(t)
	r.withBothCloudRunResources(t)
	r.gcs.AddBucket(oiBucket, 123456789012, nil)
	// The signer is the Firebase project's own account, and the IAM fake answers
	// a policy read for an account it does not have with a 404: without this the
	// token-minter row would read as unread rather than as present.
	r.iam.AddServiceAccount(oiFP, oiSigner, "signer")
	r.projBindings = []gcpfake.Binding{{Role: "roles/owner", Members: []string{"user:owner@example.com"}}}
	r.fpBindings = []gcpfake.Binding{{Role: "roles/owner", Members: []string{"user:owner@example.com"}}}
	r.crm.SetPolicy("proj-1234", r.projBindings...)
	r.crm.SetPolicy(oiFP, r.fpBindings...)
	return r
}

// withBothCloudRunResources points the config's one Cloud Run endpoint at a
// proxy of this rig's over the TWO fakes: the jobs the account may start and
// the credentials service whose own policy carries the invoker row. The real
// API answers both resource types from one host, and the check reads both
// through the one endpoint it is given, so a rig that served only one of them
// would be checking a shape no installation has — and the call to the other
// would reach the fake's unhandled path and fail the test from inside the
// proxy.
func (r *orchestratorIdentityRig) withBothCloudRunResources(t *testing.T) {
	t.Helper()
	jobs, err := url.Parse(r.run.URL)
	if err != nil {
		t.Fatal(err)
	}
	services, err := url.Parse(r.svcs.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(jobs)
	inner := proxy.Director
	proxy.Director = func(req *http.Request) {
		inner(req)
		if strings.Contains(req.URL.Path, "/services/") {
			req.URL.Scheme, req.URL.Host = services.Scheme, services.Host
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		t.Errorf("the rig's Cloud Run proxy could not forward %s %s: %v", req.Method, req.URL.Path, err)
		w.WriteHeader(http.StatusBadGateway)
	}
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)

	b, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	direct := r.run.URL + "/"
	cfg := strings.Replace(string(b), direct, srv.URL+"/", 1)
	if cfg == string(b) {
		t.Fatalf("the config does not name the Cloud Run endpoint %s", direct)
	}
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withStorage adds a storage endpoint to the config withBudget rewrote: the
// runs bucket's two rows are read through it.
func (r *orchestratorIdentityRig) withStorage(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(b), "endpoints: { ", "endpoints: { storage: "+r.gcs.URL+"/storage/v1/, ", 1)
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// grantOn adds one binding of role to the account, on the installation project
// or on the Firebase one. It is the drift every case below is made of.
func (r *orchestratorIdentityRig) grantOn(t *testing.T, project, role string) {
	t.Helper()
	switch project {
	case "proj-1234":
		r.projBindings = append(r.projBindings, gcpfake.Binding{Role: role, Members: []string{oiMember}})
		r.crm.SetPolicy(project, r.projBindings...)
	case oiFP:
		r.fpBindings = append(r.fpBindings, gcpfake.Binding{Role: role, Members: []string{oiMember}})
		r.crm.SetPolicy(project, r.fpBindings...)
	default:
		t.Fatalf("grantOn: %q is not a project this rig seeds", project)
	}
}

// asDesigned gives the account exactly section 4.3's table, one row per place
// the rows live: the launcher's project role, the runner's role on the
// installation's Cloud Run job, the runs bucket's conditioned write and its
// unconditioned read, the token-minter role on the budget signer, and the
// invoker bit on the credentials service.
func (r *orchestratorIdentityRig) asDesigned(t *testing.T) {
	t.Helper()
	r.iam.AddServiceAccount("proj-1234", oiAccount, "orchestrator")
	r.grantOn(t, "proj-1234", oiLauncherRole)
	r.iam.SetServiceAccountPolicy(oiFP, oiSigner, gcpfake.Binding{Role: oiMinterRole, Members: []string{oiMember}})
	r.gcs.SetBucketPolicy(oiBucket, []gcpfake.Binding{
		{Role: "roles/storage.objectUser", Members: []string{oiMember},
			Condition: &gcpfake.IAMCondition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(oiBucket)}},
		{Role: "roles/storage.objectViewer", Members: []string{oiMember}},
	})
	r.run.AddJob(oiJob, "1", "512Mi")
	r.run.SetJobPolicy(oiJob, gcpfake.Binding{Role: oiRunnerRole, Members: []string{oiMember}})
	r.svcs.AddService(infra.CredentialsServiceName)
	r.svcs.SetServicePolicy(infra.CredentialsServiceName, gcpfake.Binding{Role: oiInvokerRole, Members: []string{oiMember}})
}

// TestDoctorOrchestratorIdentityTable is the check's own table: the identity as
// section 4.3 designs it is OK, and every way it can differ from that table is
// a line that names the row — a grant the table does not have, a row missing,
// the bucket's write fenced by another condition, the launch granted on a job
// as something other than the runner's role, and the account absent altogether.
//
// The absent case is the one that must NOT be a finding: an installation that
// never opted in has no resident orchestrator, and a check that called that a
// warning would send every owner who has not asked for one looking for a
// problem. The module's switch is off by default and no CLI writer sets it yet.
func TestDoctorOrchestratorIdentityTable(t *testing.T) {
	dropBucketRead := func(t *testing.T, r *orchestratorIdentityRig) {
		t.Helper()
		r.gcs.SetBucketPolicy(oiBucket, []gcpfake.Binding{
			{Role: "roles/storage.objectUser", Members: []string{oiMember},
				Condition: &gcpfake.IAMCondition{Title: gcp.LauncherBucketConditionTitle, Expression: gcp.LauncherBucketCondition(oiBucket)}},
		})
	}
	conditionTheWriteOtherwise := func(t *testing.T, r *orchestratorIdentityRig) {
		t.Helper()
		r.gcs.SetBucketPolicy(oiBucket, []gcpfake.Binding{
			{Role: "roles/storage.objectUser", Members: []string{oiMember},
				Condition: &gcpfake.IAMCondition{Title: "fugaro-wider", Expression: `resource.name.startsWith("projects/_/buckets/` + oiBucket + `/objects/")`}},
			{Role: "roles/storage.objectViewer", Members: []string{oiMember}},
		})
	}

	for _, c := range []struct {
		name   string
		absent bool
		drift  func(t *testing.T, r *orchestratorIdentityRig)
		ok     bool
		sev    string
		need   []string
		fix    []string
		// unread is whether the case leaves a scope this check cannot read —
		// the rig's other cases all answer every read it makes, and the one
		// that deletes the credentials service does not.
		unread bool
	}{
		{
			name: "identity as designed",
			ok:   true,
			need: []string{"holds exactly the design's section 4.3 table", "run.jobs.run", "token-minter role on the signer account", "invoker bit on the credentials service"},
		},
		{
			// The invoker row is the one the daemon's whole reach to the
			// credentials service is (F19, O2): Cloud Run decides admission per
			// service, so a missing row is a daemon the front end refuses
			// before its class is ever read, and doctor has to say so.
			name: "the invoker row on the credentials service is gone",
			drift: func(t *testing.T, r *orchestratorIdentityRig) {
				r.svcs.SetServicePolicy(infra.CredentialsServiceName)
			},
			sev:  "warning",
			need: []string{"missing", oiInvokerRole + " on the credentials service"},
		},
		{
			name: "a stray role on the credentials service",
			drift: func(t *testing.T, r *orchestratorIdentityRig) {
				r.svcs.SetServicePolicy(infra.CredentialsServiceName,
					gcpfake.Binding{Role: oiInvokerRole, Members: []string{oiMember}},
					gcpfake.Binding{Role: "roles/run.viewer", Members: []string{oiMember}})
			},
			sev:  "warning",
			need: []string{"roles/run.viewer on the credentials service"},
		},
		{
			// An installation whose credentials service was never deployed (or
			// was deleted) has no policy for the invoker row to be bound on, so
			// the row is not part of that installation's table and the check
			// says what it did not read rather than warning about a grant
			// nothing could hold.
			name: "the credentials service is not deployed",
			drift: func(t *testing.T, r *orchestratorIdentityRig) {
				r.svcs.RemoveService(infra.CredentialsServiceName)
			},
			sev:    "info",
			need:   []string{"holds exactly the design's section 4.3 table", "where this project has one"},
			unread: true,
		},
		{
			name:   "the account is there only for an installation that opted in",
			absent: true,
			sev:    "info",
			need:   []string{"has no resident orchestrator", "has not opted in"},
			fix:    []string{"no action"},
		},
		{
			name:  "a stray role on the installation project",
			drift: func(t *testing.T, r *orchestratorIdentityRig) { r.grantOn(t, "proj-1234", "roles/run.developer") },
			sev:   "warning",
			need:  []string{"roles/run.developer", "on the installation project"},
		},
		{
			name:  "a stray role on the Firebase project",
			drift: func(t *testing.T, r *orchestratorIdentityRig) { r.grantOn(t, oiFP, "roles/datastore.viewer") },
			sev:   "warning",
			need:  []string{"roles/datastore.viewer", "on the Firebase project"},
		},
		{
			name:  "the launcher list's own database read",
			drift: func(t *testing.T, r *orchestratorIdentityRig) { r.grantOn(t, oiFP, "roles/firebasedatabase.viewer") },
			sev:   "warning",
			need:  []string{"roles/firebasedatabase.viewer", "rule 11", "reaches every mailbox"},
		},
		{
			name: "the launcher's own project role is gone",
			drift: func(t *testing.T, r *orchestratorIdentityRig) {
				r.projBindings = r.projBindings[:1]
				r.crm.SetPolicy("proj-1234", r.projBindings...)
			},
			sev:  "warning",
			need: []string{"missing", oiLauncherRole},
		},
		{
			name:  "the bucket's read is gone",
			drift: dropBucketRead,
			sev:   "warning",
			need:  []string{"missing", "roles/storage.objectViewer"},
		},
		{
			name:  "the bucket's write is fenced by another condition",
			drift: conditionTheWriteOtherwise,
			sev:   "warning",
			need: []string{"is bound as", "rather than under the launchers' own condition",
				"writes wherever that condition reaches", "cache/, locks/ and builds/"},
		},
		{
			name: "the launch is on a job as something other than the runner's role",
			drift: func(t *testing.T, r *orchestratorIdentityRig) {
				r.run.SetJobPolicy(oiJob, gcpfake.Binding{Role: "roles/run.developer", Members: []string{oiMember}})
			},
			sev:  "warning",
			need: []string{"a Cloud Run job carrying", "roles/run.developer", "rather than the job runner's role"},
		},
		{
			name: "the token-minter row is gone",
			drift: func(t *testing.T, r *orchestratorIdentityRig) {
				r.iam.SetServiceAccountPolicy(oiFP, oiSigner)
			},
			sev:  "warning",
			need: []string{"missing", oiMinterRole},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newOrchestratorIdentityRig(t)
			if !c.absent {
				r.asDesigned(t)
			}
			if c.drift != nil {
				c.drift(t, r)
			}
			o, err := r.doctor(t)
			if err != nil {
				t.Fatalf("doctor: %v", err)
			}
			ck, found := checkByID(o, "orchestrator-identity")
			if !found {
				t.Fatalf("no orchestrator-identity check in the output: %+v", o.Checks)
			}
			if c.ok != ck.OK {
				t.Errorf("orchestrator-identity ok = %v, want %v: %s", ck.OK, c.ok, ck.Problem)
			}
			if c.sev != "" && ck.Severity != c.sev {
				t.Errorf("orchestrator-identity severity = %q, want %q: %s", ck.Severity, c.sev, ck.Problem)
			}
			for _, n := range c.need {
				if !strings.Contains(ck.Problem, n) {
					t.Errorf("the check's line does not name %q:\n%s", n, ck.Problem)
				}
			}
			for _, n := range c.fix {
				if !strings.Contains(ck.Fix, n) {
					t.Errorf("the check's fix does not name %q:\n%s", n, ck.Fix)
				}
			}
			if !c.ok && ck.Fix == "" {
				t.Error("a finding carries no fix: the owner is told what is wrong and not what to do")
			}
			_, unread := checkByID(o, "orchestrator-identity-not-read")
			switch {
			case unread && !c.unread:
				t.Errorf("the rig reads every scope this check looks at, so nothing is unread: %+v", o.Checks)
			case !unread && c.unread:
				t.Errorf("this case leaves a scope the check cannot read, and the check reports none: %+v", o.Checks)
			}
		})
	}
}

// TestDoctorOrchestratorIdentityIsTheNameTheModuleCreates: the check looks for
// internal/infra's account and for no other. A second spelling here would have
// doctor read a look-alike account as the resident orchestrator, which is the
// one account whose whole point is that its envelope is enumerated — and an
// installation that opted in would read as one that never did.
func TestDoctorOrchestratorIdentityIsTheNameTheModuleCreates(t *testing.T) {
	if infra.OrchestratorAccountID != "fugaro-orch" {
		t.Fatalf("infra.OrchestratorAccountID = %q: the installation module's account_id, the cover's member check and this check are one literal", infra.OrchestratorAccountID)
	}
	r := newOrchestratorIdentityRig(t)
	r.iam.AddServiceAccount("proj-1234", "fugaro-orchestrator@proj-1234.iam.gserviceaccount.com", "look-alike")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: oiLauncherRole, Members: []string{"serviceAccount:fugaro-orchestrator@proj-1234.iam.gserviceaccount.com"}})

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	ck, found := checkByID(o, "orchestrator-identity")
	if !found {
		t.Fatalf("no orchestrator-identity check in the output: %+v", o.Checks)
	}
	if ck.OK || ck.Severity != "info" || !strings.Contains(ck.Problem, "has no resident orchestrator") {
		t.Errorf("an account under another name reads as the resident orchestrator: %s", ck.Problem)
	}
}

// TestDoctorOrchestratorIdentityNeverWrites: the check is read-only and it
// prints, like every check of its class. What it must never do is PUT a policy
// back: what section 4.3 describes is a table a person assembled, and a check
// that repaired it would be a check that widened an envelope on its own.
func TestDoctorOrchestratorIdentityNeverWrites(t *testing.T) {
	r := newOrchestratorIdentityRig(t)
	r.asDesigned(t)
	r.grantOn(t, "proj-1234", "roles/run.developer")
	out, err := r.doctorText(t)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "orchestrator-identity") {
		t.Errorf("the finding is not on the screen a person reads:\n%s", out)
	}
	for _, req := range r.crm.Requests() {
		if req.Method != "GET" && !strings.HasSuffix(req.Path, ":getIamPolicy") {
			t.Errorf("doctor wrote to a project's IAM: %s %s", req.Method, req.Path)
		}
	}
	for _, req := range r.iam.Requests() {
		if req.Method != "GET" && !strings.HasSuffix(req.Path, ":getIamPolicy") {
			t.Errorf("doctor wrote to an account's IAM: %s %s", req.Method, req.Path)
		}
	}
}
