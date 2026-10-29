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
