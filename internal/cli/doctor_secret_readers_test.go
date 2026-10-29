package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// secretReadersRig is a doctor rig for the two Secret Manager reader checks:
// the doctor rig inside a checkout of github.com/acme/app, whose local config
// records acme/app and acme/old, plus an IAM fake for the custom roles a
// project policy may name and a Cloud Run *services* fake beside the jobs
// one.
//
// The two Cloud Run fakes share the one endpoint the local config names: the
// launcher check reads jobs through it and the credentials-service deployer
// read reads a service's policy.
type secretReadersRig struct {
	*doctorRig
	iam  *gcpfake.IAM
	svcs *gcpfake.RunServices
	// secretID is the project-wide github-app-key container the rig seeds: the
	// local config opts the name into project-wide storage for acme/app, so
	// doctor's stored-secret check needs a value there before any test can
	// tell its own finding from that one.
	secretID string
}

func newSecretReadersRig(t *testing.T) *secretReadersRig {
	t.Helper()
	r := &secretReadersRig{doctorRig: newDoctorRig(t), iam: gcpfake.NewIAM(t), svcs: gcpfake.NewRunServices(t)}
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": cliMinimalYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	cfg := strings.Replace(r.cfg, "run: "+r.run.URL+"/", "run: "+cloudRunEndpoint(t, r.run, r.svcs)+"/", 1)
	cfg = strings.Replace(cfg, "endpoints: { ", "endpoints: { iam: "+r.iam.URL+"/, ", 1)
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	addLocalLines(t, r.doctorRig, projectSecretsConfig+"  acme/old: { provider: github, workflows: [app] }\n")
	r.secretID = putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	return r
}

// cloudRunEndpoint serves the jobs fake and the services fake on one URL:
// the local config names one Cloud Run endpoint, and doctor asks it about
// both resources.
func cloudRunEndpoint(t *testing.T, jobs *gcpfake.Run, svcs *gcpfake.RunServices) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/services/") {
			svcs.Config.Handler.ServeHTTP(w, r)
			return
		}
		jobs.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// doctor runs doctor over the rig's checkout, as the signer rig's own helper
// does, and returns the parsed output beside doctor's error.
func (r *secretReadersRig) doctor(t *testing.T, args ...string) (doctorOutput, error) {
	t.Helper()
	out, _, err := execute(t, append([]string{"doctor", "--json"}, args...)...)
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	return o, err
}

// withCredentialsService opts the rig's installation into the credentials
// service, the way `fugaro update --provision-credentials` writes it.
func (r *secretReadersRig) withCredentialsService(t *testing.T) {
	t.Helper()
	addLocalLines(t, r.doctorRig, "credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
}

// credentialsSA is the credentials service's own account, as internal/infra
// deploys it: infra.CredentialsAccountID at the installation's project.
const credentialsSA = infra.CredentialsAccountID + "@proj-1234.iam.gserviceaccount.com"

// readerWarnings is every warning the reader sweep raised. The info line F3
// prints shares the check's ID, so a test asking "did the sweep flag anything"
// has to say which of the two it means.
func readerWarnings(o doctorOutput) []doctorCheck {
	var out []doctorCheck
	for _, c := range o.Checks {
		if c.ID == "secret-readers-unexpected" && c.Severity == "warning" {
			out = append(out, c)
		}
	}
	return out
}

// TestDoctorSecretReadersAcceptsFugaroAccounts: the sweep exists to name a
// member Fugaro did not create, so every class it does create is silent — the
// job account of a repository the local config lists, and a managed account on
// a container of a managed plan. The owners-and-editors line is the only thing
// left to say, and it names them once (F3).
func TestDoctorSecretReadersAcceptsFugaroAccounts(t *testing.T) {
	r := newSecretReadersRig(t)
	slug := mustSlug("github", "acme/app")
	jobAccount := gcp.ServiceAccountID(slug, "app") + "@proj-1234.iam.gserviceaccount.com"
	r.sm.SetPolicy(r.secretID, []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{"serviceAccount:" + jobAccount}}})
	managed := tf.ManagedPrefix + "acme-secret"
	r.sm.Seed(managed, map[string]string{gcp.LabelSecret: "acme-secret"}, []byte("managed-value"))
	r.sm.SetPolicy(managed, []gcpfake.Binding{{Role: gcp.SecretAccessorRole,
		Members: []string{"serviceAccount:" + tf.ManagedPrefix + "deployer@proj-1234.iam.gserviceaccount.com"}}})

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v", err)
	}
	if w := readerWarnings(o); len(w) != 0 {
		t.Fatalf("the sweep flagged an account Fugaro creates: %+v", w)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-readers-unexpected")
	if !ok || c.OK || c.Severity != "info" || !strings.Contains(c.Problem, "user:owner@example.com") {
		t.Fatalf("the owners-and-editors line = %+v", c)
	}
}

// TestDoctorSecretReadersFlagsProjectLevelAccess: a project-level grant is
// every container at once, so the sweep reads the project's own policy too. A
// predefined Secret Manager role and a custom role whose permissions list
// secretmanager.versions.access are both readers; a custom role listing only
// the metadata permission is not, and neither is the same grant to an account
// Fugaro creates.
func TestDoctorSecretReadersFlagsProjectLevelAccess(t *testing.T) {
	r := newSecretReadersRig(t)
	r.iam.AddRole("proj-1234", "secret-rotator", "Secret rotator", false)
	r.iam.SetRolePermissions("projects/proj-1234/roles/secret-rotator", versionsAccess)
	r.iam.AddRole("proj-1234", "secret-lister", "Secret lister", false)
	r.iam.SetRolePermissions("projects/proj-1234/roles/secret-lister", "secretmanager.versions.list")
	ours := "serviceAccount:" + gcp.ServiceAccountID(mustSlug("github", "acme/app"), "app") + "@proj-1234.iam.gserviceaccount.com"
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: gcp.SecretAccessorRole, Members: []string{
			"serviceAccount:stray@project-a.iam.gserviceaccount.com", ours, "user:person-2@example.com"}},
		gcpfake.Binding{Role: "roles/secretmanager.admin", Members: []string{"serviceAccount:admin@project-b.iam.gserviceaccount.com"}},
		gcpfake.Binding{Role: "projects/proj-1234/roles/secret-rotator", Members: []string{"serviceAccount:rotator@project-c.iam.gserviceaccount.com"}},
		gcpfake.Binding{Role: "projects/proj-1234/roles/secret-lister", Members: []string{"serviceAccount:lister@project-d.iam.gserviceaccount.com"}})

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v", err)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-readers-unexpected")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("secret-readers-unexpected = %+v (%+v)", c, o.Checks)
	}
	for _, want := range []string{
		"serviceAccount:stray@project-a.iam.gserviceaccount.com (" + gcp.SecretAccessorRole + ")",
		"serviceAccount:admin@project-b.iam.gserviceaccount.com (roles/secretmanager.admin)",
		"serviceAccount:rotator@project-c.iam.gserviceaccount.com (projects/proj-1234/roles/secret-rotator)",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("problem %q lacks %q", c.Problem, want)
		}
	}
	for _, unwanted := range []string{ours, "lister@project-d", "person-2"} {
		if strings.Contains(c.Problem, unwanted) {
			t.Errorf("problem %q names %q, which is not a project-level reader", c.Problem, unwanted)
		}
	}
}

// TestDoctorCredentialsServiceDeployersListsPrincipals: an installation that
// opted in gets one info line naming everyone who could deploy the service — a
// role that updates Cloud Run services on the project, one on the service
// itself, and actAs on the account the service runs as.
func TestDoctorCredentialsServiceDeployersListsPrincipals(t *testing.T) {
	r := newSecretReadersRig(t)
	r.withCredentialsService(t)
	r.svcs.AddService(infra.CredentialsServiceName)
	r.iam.AddServiceAccount("proj-1234", credentialsSA, "credentials service")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/run.developer", Members: []string{"user:person-2@example.com"}})
	r.svcs.SetServicePolicy(infra.CredentialsServiceName,
		gcpfake.Binding{Role: "roles/run.admin", Members: []string{"user:person-3@example.com"}})
	r.iam.SetServiceAccountPolicy("proj-1234", credentialsSA,
		gcpfake.Binding{Role: "roles/iam.serviceAccountUser", Members: []string{"user:person-4@example.com"}})

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("an info check must not fail doctor: %v", err)
	}
	c, ok := doctorCheckByID(o.Checks, "credentials-service-deployers")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("credentials-service-deployers = %+v (%+v)", c, o.Checks)
	}
	for _, want := range []string{
		"user:owner@example.com (roles/owner)",
		"user:person-2@example.com (roles/run.developer)",
		"user:person-3@example.com (roles/run.admin)",
		"user:person-4@example.com (roles/iam.serviceAccountUser)",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("problem %q lacks %q", c.Problem, want)
		}
	}
	if !strings.Contains(c.Problem, "4 principal(s)") {
		t.Errorf("problem %q does not count the four principals", c.Problem)
	}
	if strings.Contains(c.Problem, "not read:") {
		t.Errorf("problem %q reports a policy as unread, and every fake serves one", c.Problem)
	}
}

// TestDoctorCredentialsServiceDeployersExpandsCustomRoles: only a role's own
// definition says whether it can update a service, so a custom role is
// expanded through the IAM API — the one listing run.services.update is named,
// the one listing only the get is not.
func TestDoctorCredentialsServiceDeployersExpandsCustomRoles(t *testing.T) {
	r := newSecretReadersRig(t)
	r.withCredentialsService(t)
	r.svcs.AddService(infra.CredentialsServiceName)
	r.iam.AddServiceAccount("proj-1234", credentialsSA, "credentials service")
	r.iam.AddRole("proj-1234", "run-updater", "Run updater", false)
	r.iam.SetRolePermissions("projects/proj-1234/roles/run-updater", runServicesUpdate)
	r.iam.AddRole("proj-1234", "run-viewer", "Run viewer", false)
	r.iam.SetRolePermissions("projects/proj-1234/roles/run-viewer", "run.services.get")
	r.svcs.SetServicePolicy(infra.CredentialsServiceName,
		gcpfake.Binding{Role: "projects/proj-1234/roles/run-updater", Members: []string{"serviceAccount:deployer@project-e.iam.gserviceaccount.com"}},
		gcpfake.Binding{Role: "projects/proj-1234/roles/run-viewer", Members: []string{"serviceAccount:viewer@project-f.iam.gserviceaccount.com"}})

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("an info check must not fail doctor: %v", err)
	}
	c, ok := doctorCheckByID(o.Checks, "credentials-service-deployers")
	if !ok || c.OK || c.Severity != "info" {
		t.Fatalf("credentials-service-deployers = %+v (%+v)", c, o.Checks)
	}
	if !strings.Contains(c.Problem, "serviceAccount:deployer@project-e.iam.gserviceaccount.com (projects/proj-1234/roles/run-updater)") {
		t.Errorf("problem %q does not name the custom role that updates a service", c.Problem)
	}
	if strings.Contains(c.Problem, "viewer@project-f") {
		t.Errorf("problem %q names a role that cannot update a service", c.Problem)
	}
}

// TestDoctorCredentialsServiceDeployersSilentWhenNotOptedIn: the block is the
// opt-in, and an installation without it has no credentials service to report
// on — the check is not printed at all, rather than printed empty.
func TestDoctorCredentialsServiceDeployersSilentWhenNotOptedIn(t *testing.T) {
	r := newSecretReadersRig(t)
	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if c, ok := doctorCheckByID(o.Checks, "credentials-service-deployers"); ok {
		t.Fatalf("an installation with no credentials_service block got a deployer line: %+v", c)
	}
}

// TestDoctorSecretReadersFlagsAnUnknownAccessor is the headline case: a
// container of the installation's project grants secretAccessor to a person
// and to a service account of another project, and neither is an account
// Fugaro creates for it. Doctor must name both, on the container's own line,
// and must not fail without --strict.
func TestDoctorSecretReadersFlagsAnUnknownAccessor(t *testing.T) {
	r := newSecretReadersRig(t)
	id := putProjectSecret(t, r.sm, "aurora", "github-app-key", []byte("project-value"))
	r.sm.SetPolicy(id, []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{
		"user:person-1@example.com", "serviceAccount:stray@project-a.iam.gserviceaccount.com"}}})

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v", err)
	}
	c, ok := doctorCheckByID(o.Checks, "secret-readers-unexpected")
	if !ok || c.OK || c.Severity != "warning" {
		t.Fatalf("secret-readers-unexpected = %+v (%+v)", c, o.Checks)
	}
	for _, want := range []string{"user:person-1@example.com", "serviceAccount:stray@project-a.iam.gserviceaccount.com", "github-app-key"} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("problem %q lacks %q", c.Problem, want)
		}
	}
	if c.Fix == "" {
		t.Errorf("the finding names no fix: %+v", c)
	}
	if strings.Contains(o.Error, "project-value") {
		t.Fatalf("doctor reported a secret value: %q", o.Error)
	}
}
