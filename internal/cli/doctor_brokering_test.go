package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// brokeringBitbucketYAML is a minimal Bitbucket checkout's fugaro.yaml: the
// provider is what makes the repository a candidate for the proxy at all.
const brokeringBitbucketYAML = `version: 1
project: aurora
git: { provider: bitbucket }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// brokeringConfig is the owner's side of an opted-in installation whose one
// Bitbucket repository is proxied: the credentials service block (written by
// the opt-in) and the project_secrets entry that names the token and flips
// the proxy on for workspace-a/repo-a.
const brokeringConfig = "project_secrets:\n  bitbucket-token:\n    allow_data_to: [workspace-a/repo-a]\n    proxy: true\n" +
	"repos:\n  workspace-a/repo-a: { provider: bitbucket, workflows: [app] }\n"

// brokeringRig is a doctor rig inside a checkout of workspace-a/repo-a, whose
// installation opted into the credentials service and whose local config
// proxies the repository's Bitbucket token, with a Cloud Run services fake
// beside the jobs one.
type brokeringRig struct {
	*doctorRig
	svcs *gcpfake.RunServices
}

func newBrokeringRig(t *testing.T) *brokeringRig {
	t.Helper()
	r := &brokeringRig{doctorRig: newDoctorRig(t), svcs: gcpfake.NewRunServices(t)}
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": brokeringBitbucketYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/workspace-a/repo-a.git")
	cfg := strings.Replace(r.cfg, "run: "+r.run.URL+"/", "run: "+cloudRunEndpoint(t, r.run, r.svcs)+"/", 1)
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	addLocalLines(t, r.doctorRig, "credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
	addLocalLines(t, r.doctorRig, brokeringConfig)
	// The token the proxy forwards is stored (project-wide, which is where an
	// entry with allow_data_to puts it): without a version anywhere, doctor is
	// red for a reason that has nothing to do with this rig's checks.
	putProjectSecret(t, r.sm, "aurora", config.BitbucketSecret, []byte("bundle-token-value"))
	return r
}

// doctor runs doctor over the rig's checkout and returns the parsed output
// beside doctor's error.
func (r *brokeringRig) doctor(t *testing.T, args ...string) (doctorOutput, error) {
	t.Helper()
	out, _, err := execute(t, append([]string{"doctor", "--json"}, args...)...)
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v\n%s", jerr, out)
	}
	return o, err
}

// checksByID is every check carrying id, in the order doctor emitted them:
// several of the brokering checks emit one line per secret or repository.
func checksByID(o doctorOutput, id string) []doctorCheck {
	var out []doctorCheck
	for _, c := range o.Checks {
		if c.ID == id {
			out = append(out, c)
		}
	}
	return out
}

// jobMountingBitbucketToken seeds the rig's deployed job for the repository's
// app workflow with the one mount the check reads: the repository's own
// Bitbucket token, as modules/workflow/sa.tf writes it before the flip.
func (r *brokeringRig) jobMountingBitbucketToken(t *testing.T) string {
	t.Helper()
	slug := mustSlug("bitbucket", "workspace-a/repo-a")
	job := gcp.JobName(slug, "app")
	env := config.ReservedSecrets[config.BitbucketSecret]
	id := gcp.SecretID(slug, config.BitbucketSecret)
	r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
	  "template":{"template":{"containers":[{"image":"fake/app","env":[
	    {"name":"`+env+`","valueSource":{"secretKeyRef":{"secret":"`+id+`","version":"latest"}}}]}]}}}`)
	return job
}

// flipProxiedGitList turns the proxy's flip on for the duration of one test:
// the list is the release-time switch B1.13 part two fills.
func flipProxiedGitList(t *testing.T, names ...string) {
	t.Helper()
	old := config.ProxiedGitSecretNames
	config.ProxiedGitSecretNames = names
	t.Cleanup(func() { config.ProxiedGitSecretNames = old })
}

// TestDoctorBrokeringMountedTokenOnFlippedInstallation: the red step. Once the
// flip has happened (config.ProxiedGitSecretNames names the token), a proxied
// repository's job must have given the mount up: a job that still mounts it
// was applied before the flip, and the next deploy of that job would fail on a
// mount whose accessor is gone. Doctor warns.
func TestDoctorBrokeringMountedTokenOnFlippedInstallation(t *testing.T) {
	flipProxiedGitList(t, config.BitbucketSecret)
	r := newBrokeringRig(t)
	job := r.jobMountingBitbucketToken(t)

	o, err := r.doctor(t)
	if err != nil {
		t.Fatalf("a warning must not fail doctor without --strict: %v", err)
	}
	cs := checksByID(o, "bitbucket-direct")
	if len(cs) != 1 {
		t.Fatalf("bitbucket-direct emitted %d check(s), want 1: %+v", len(cs), o.Checks)
	}
	c := cs[0]
	if c.OK || c.Severity != "warning" {
		t.Fatalf("bitbucket-direct = %+v, want a warning", c)
	}
	if !strings.Contains(c.Problem, "workspace-a/repo-a") || !strings.Contains(c.Problem, config.BitbucketSecret) {
		t.Fatalf("the warning does not name the repository and the credential: %q", c.Problem)
	}
	if !strings.Contains(c.Fix, "update --repo") {
		t.Fatalf("the fix does not name the command that re-applies the job: %q", c.Fix)
	}
	if !strings.Contains(c.Problem, job) {
		t.Fatalf("the warning does not name the job that still mounts it: %q", c.Problem)
	}
}
