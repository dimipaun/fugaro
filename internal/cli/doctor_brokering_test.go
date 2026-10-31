package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
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
	r := newBrokeringCheckout(t, true)
	addLocalLines(t, r.doctorRig, brokeringConfig)
	// The token the proxy forwards is stored (project-wide, which is where an
	// entry with allow_data_to puts it): without a version anywhere, doctor is
	// red for a reason that has nothing to do with this rig's checks.
	putProjectSecret(t, r.sm, "aurora", config.BitbucketSecret, []byte("bundle-token-value"))
	return r
}

// newBrokeringDirectRig is the same checkout and installation with no proxy
// at all: no project_secrets entry admits workspace-a/repo-a, so its jobs hold
// their own Bitbucket token, which is the state bitbucket-direct reports as
// information.
func newBrokeringDirectRig(t *testing.T) *brokeringRig {
	t.Helper()
	r := newBrokeringCheckout(t, true)
	addLocalLines(t, r.doctorRig, "repos:\n  workspace-a/repo-a: { provider: bitbucket, workflows: [app] }\n")
	putProjectSecret(t, r.sm, "aurora", config.BitbucketSecret, []byte("bundle-token-value"))
	return r
}

// newBrokeringUnoptedRig is the same checkout on an installation that never
// opted in: no credentials_service block, so the checks that read the service
// have nothing to say.
func newBrokeringUnoptedRig(t *testing.T) *brokeringRig {
	t.Helper()
	r := newBrokeringCheckout(t, false)
	addLocalLines(t, r.doctorRig, brokeringConfig)
	putProjectSecret(t, r.sm, "aurora", config.BitbucketSecret, []byte("bundle-token-value"))
	return r
}

// newBrokeringCheckout is the part the three rigs share: a checkout of
// workspace-a/repo-a whose local config names the Cloud Run services fake
// beside the jobs one, so a service's policy can be read, and — when
// withService — the credentials_service block the opt-in writes.
func newBrokeringCheckout(t *testing.T, withService bool) *brokeringRig {
	t.Helper()
	r := &brokeringRig{doctorRig: newDoctorRig(t), svcs: gcpfake.NewRunServices(t)}
	dir := checkoutWith(t, map[string]string{"fugaro.yaml": brokeringBitbucketYAML})
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/workspace-a/repo-a.git")
	cfg := strings.Replace(r.cfg, "run: "+r.run.URL+"/", "run: "+cloudRunEndpoint(t, r.run, r.svcs)+"/", 1)
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	if withService {
		addLocalLines(t, r.doctorRig, "credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
	}
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
	return r.jobMountingToken(t, gcp.JobName(mustSlug("bitbucket", "workspace-a/repo-a"), "app"))
}

// jobMountingToken is that mount on the job named job: the same workflow under
// a named Claude account is a different job (gcp.WorkflowVariant), and it
// mounts the token the same way.
func (r *brokeringRig) jobMountingToken(t *testing.T, job string) string {
	t.Helper()
	slug := mustSlug("bitbucket", "workspace-a/repo-a")
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

// brokeringJobAccount is the account the workflow module grants
// roles/run.invoker to on the credentials service: the job's own
// (modules/workflow/sa.tf), named the way infra.CredentialsJobAccounts and
// gcp.ServiceAccountID derive it.
func brokeringJobAccount(workflow string) string {
	slug := mustSlug("bitbucket", "workspace-a/repo-a")
	return "serviceAccount:" + gcp.ServiceAccountID(slug, workflow) + "@proj-1234.iam.gserviceaccount.com"
}

// brokeringBundleDoc is the bundle document the credentials service holds, in
// its own `name: value` format: the Bitbucket token beside one model
// credential, so a test that looks for a leak has two values to look for.
func brokeringBundleDoc(t *testing.T, token string) string {
	t.Helper()
	return config.BitbucketSecret + ": " + token + "\n" + config.AnthropicSecret + ": " + credsValue(t, "model") + "\n"
}

// putBundle writes the bundle document into the rig's Secret Manager, as
// `fugaro creds set` does, and lets the fake hand the value back: its access
// call fails the test until a test allows it, which is what keeps every other
// read in these tests from touching a value by accident.
func (r *brokeringRig) putBundle(t *testing.T, doc string) string {
	t.Helper()
	id := credsvc.BundleSecretID("aurora")
	r.sm.Seed(id, map[string]string{gcp.LabelManaged: gcp.ManagedValue}, []byte(doc))
	r.sm.AllowAccess()
	return id
}

// bitbucketFake stands in for Bitbucket's own API. The token check makes one
// call per proxied repository — GET /repositories/<workspace>/<slug> — and
// nothing else, which the returned counter pins.
func bitbucketFake(t *testing.T, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status != http.StatusOK {
			http.Error(w, `{"error":{"message":"refused"}}`, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"is_private":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// useBitbucketRoot points the token check at root for one test. The root is a
// package variable and never whatever an environment variable names: a run's
// root is the published policy's to choose, and a check that reads a credential
// with the operator's own identity asks the forge the token was issued for.
func useBitbucketRoot(t *testing.T, root string, client *http.Client) {
	t.Helper()
	oldURL, oldHTTP := bitbucketAPIURL, bitbucketAPIClient
	bitbucketAPIURL, bitbucketAPIClient = root, client
	t.Cleanup(func() { bitbucketAPIURL, bitbucketAPIClient = oldURL, oldHTTP })
}

// pinDoctorNow makes the age checks measure against now for one test.
func pinDoctorNow(t *testing.T, now func() time.Time) {
	t.Helper()
	old := doctorNow
	doctorNow = now
	t.Cleanup(func() { doctorNow = old })
}

// TestDoctorBrokeringCredentialsServiceDeployed: the check reads the service's
// own policy, and it is a warning in both failing directions — a service the
// installation opted into that is not there, and a service no job account may
// invoke. Neither fails doctor without --strict: an installation mid-opt-in is
// a legitimate state.
func TestDoctorBrokeringCredentialsServiceDeployed(t *testing.T) {
	t.Run("deployed with the grant", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.svcs.AddService(infra.CredentialsServiceName)
		r.svcs.SetServicePolicy(infra.CredentialsServiceName, gcpfake.Binding{Role: "roles/run.invoker", Members: []string{brokeringJobAccount("app")}})

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "credentials-service-deployed")
		if len(cs) != 1 || !cs[0].OK {
			t.Fatalf("credentials-service-deployed = %+v, want one ok check", cs)
		}
	})

	t.Run("no service", func(t *testing.T) {
		r := newBrokeringRig(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "credentials-service-deployed")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("credentials-service-deployed = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, infra.CredentialsServiceName) || !strings.Contains(cs[0].Problem, "proj-1234") {
			t.Errorf("the warning does not name the service and the project: %q", cs[0].Problem)
		}
		if !strings.Contains(cs[0].Fix, "--provision-credentials") {
			t.Errorf("the fix does not name the opt-in: %q", cs[0].Fix)
		}
	})

	t.Run("deployed without the grant", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.svcs.AddService(infra.CredentialsServiceName)

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "credentials-service-deployed")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("credentials-service-deployed = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, "workspace-a/repo-a") || !strings.Contains(cs[0].Problem, "roles/run.invoker") {
			t.Errorf("the warning does not name the repository and the role: %q", cs[0].Problem)
		}
		if !strings.Contains(cs[0].Fix, "update --repo") {
			t.Errorf("the fix does not name the command that grants it: %q", cs[0].Fix)
		}
	})

	t.Run("not opted in", func(t *testing.T) {
		r := newBrokeringUnoptedRig(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if cs := checksByID(o, "credentials-service-deployed"); len(cs) != 0 {
			t.Errorf("an installation with no credentials_service block got %+v", cs)
		}
	})
}

// TestDoctorBrokeringBundleBitbucketToken: the bundle's own check, and the
// flag that gates it. Without --check-bitbucket-token the document is never
// read — the flag is what `creds ls` is refused for in an agent's session — and
// with it a bundle that holds no token is a warning exactly because a proxied
// repository is waiting for one.
func TestDoctorBrokeringBundleBitbucketToken(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
		srv, calls := bitbucketFake(t, http.StatusOK)
		useBitbucketRoot(t, srv.URL, srv.Client())

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "bundle-bitbucket-token")
		if len(cs) != 1 || !cs[0].OK {
			t.Fatalf("bundle-bitbucket-token = %+v, want one ok check", cs)
		}
		vs := checksByID(o, "bitbucket-token-valid")
		if len(vs) != 1 || !vs[0].OK {
			t.Fatalf("bitbucket-token-valid = %+v, want one ok check", vs)
		}
		if calls.Load() != 1 {
			t.Errorf("Bitbucket was asked %d time(s), want one GET per proxied repository", calls.Load())
		}
	})

	t.Run("holds none", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, config.AnthropicSecret+": "+credsValue(t, "model")+"\n")

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "bundle-bitbucket-token")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("bundle-bitbucket-token = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, config.BitbucketSecret) || !strings.Contains(cs[0].Problem, "proxy: true") {
			t.Errorf("the warning does not name the credential and the proxy: %q", cs[0].Problem)
		}
		if !strings.Contains(cs[0].Fix, "creds set "+config.BitbucketSecret) {
			t.Errorf("the fix does not name the command that stores it: %q", cs[0].Fix)
		}
		if vs := checksByID(o, "bitbucket-token-valid"); len(vs) != 0 {
			t.Errorf("Bitbucket was asked with no token to ask with: %+v", vs)
		}
	})

	t.Run("never read without the flag", func(t *testing.T) {
		r := newBrokeringRig(t)
		bundle := r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if cs := checksByID(o, "bundle-bitbucket-token"); len(cs) != 0 {
			t.Errorf("the bundle was read without --check-bitbucket-token: %+v", cs)
		}
		if vs := checksByID(o, "bitbucket-token-valid"); len(vs) != 0 {
			t.Errorf("Bitbucket was asked without --check-bitbucket-token: %+v", vs)
		}
		for _, id := range r.sm.Accessed() {
			if id == bundle {
				t.Errorf("the bundle's value was read without --check-bitbucket-token")
			}
		}
	})
}

// TestDoctorBrokeringBitbucketTokenValid: what Bitbucket's own answer is worth.
// A token it refuses (401, 403, 404) is a warning — every run of that
// repository fails at its first git call — while a Bitbucket that cannot be
// reached at all is information: the check reports what it could not ask
// rather than blaming a token nobody asked about.
func TestDoctorBrokeringBitbucketTokenValid(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
		srv, calls := bitbucketFake(t, http.StatusUnauthorized)
		useBitbucketRoot(t, srv.URL, srv.Client())

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		vs := checksByID(o, "bitbucket-token-valid")
		if len(vs) != 1 || vs[0].OK || vs[0].Severity != "warning" {
			t.Fatalf("bitbucket-token-valid = %+v, want one warning", vs)
		}
		if !strings.Contains(vs[0].Problem, "workspace-a/repo-a") {
			t.Errorf("the warning does not name the repository: %q", vs[0].Problem)
		}
		if !strings.Contains(vs[0].Fix, "creds set "+config.BitbucketSecret) {
			t.Errorf("the fix does not name the command that replaces it: %q", vs[0].Fix)
		}
		if calls.Load() != 1 {
			t.Errorf("Bitbucket was asked %d time(s), want one GET per proxied repository", calls.Load())
		}
	})

	t.Run("Bitbucket failing rather than refusing", func(t *testing.T) {
		// A 5xx or a 429 is Bitbucket (or what stands in front of it) failing
		// to answer, not the token being rejected: calling it a refusal would
		// send the operator to rotate a credential that works.
		for _, status := range []int{http.StatusBadGateway, http.StatusTooManyRequests} {
			r := newBrokeringRig(t)
			r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
			srv, _ := bitbucketFake(t, status)
			useBitbucketRoot(t, srv.URL, srv.Client())

			o, err := r.doctor(t, "--check-bitbucket-token")
			if err != nil {
				t.Fatalf("doctor: %v", err)
			}
			vs := checksByID(o, "bitbucket-token-valid")
			if len(vs) != 1 || vs[0].OK || vs[0].Severity != "info" {
				t.Fatalf("HTTP %d: bitbucket-token-valid = %+v, want one informational line", status, vs)
			}
			if !strings.Contains(vs[0].Problem, "could not be asked") {
				t.Errorf("HTTP %d: the line does not say the question went unanswered: %q", status, vs[0].Problem)
			}
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
		// A closed port: the call is not made at all, which is not Bitbucket
		// answering about the token.
		useBitbucketRoot(t, "http://127.0.0.1:1", nil)

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		vs := checksByID(o, "bitbucket-token-valid")
		if len(vs) != 1 || vs[0].OK || vs[0].Severity != "info" {
			t.Fatalf("bitbucket-token-valid = %+v, want one informational line", vs)
		}
		if !strings.Contains(vs[0].Problem, "could not be asked") {
			t.Errorf("the line does not say the question was not asked: %q", vs[0].Problem)
		}
	})
}

// TestDoctorBrokeringBitbucketDirect: the repository's own side of the flip.
// Before it — no project_secrets entry admits the repository, or the release
// that fills the flip list has not shipped — a job mounting bitbucket-token is
// the state the installation is in, and the line is information. Only after
// the flip is that same mount a job applied before it.
func TestDoctorBrokeringBitbucketDirect(t *testing.T) {
	t.Run("not proxied", func(t *testing.T) {
		r := newBrokeringDirectRig(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "bitbucket-direct")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "info" {
			t.Fatalf("bitbucket-direct = %+v, want one informational line", cs)
		}
		if !strings.Contains(cs[0].Problem, "not proxied") || !strings.Contains(cs[0].Problem, config.BitbucketSecret) {
			t.Errorf("the line does not say what the repository holds: %q", cs[0].Problem)
		}
		if !strings.Contains(cs[0].Fix, "proxy: true") || !strings.Contains(cs[0].Fix, "update --repo") {
			t.Errorf("the fix does not name the opt-in and the re-apply: %q", cs[0].Fix)
		}
	})

	t.Run("proxied, before the flip", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.jobMountingBitbucketToken(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "bitbucket-direct")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "info" {
			t.Fatalf("bitbucket-direct = %+v, want one informational line", cs)
		}
		if !strings.Contains(cs[0].Problem, "flip list has not shipped") {
			t.Errorf("the line does not say the flip has not shipped: %q", cs[0].Problem)
		}
	})

	t.Run("proxied and flipped, the mount given up", func(t *testing.T) {
		flipProxiedGitList(t, config.BitbucketSecret)
		r := newBrokeringRig(t)
		// The job the flip's own re-apply deploys: the same name, no mount.
		job := gcp.JobName(mustSlug("bitbucket", "workspace-a/repo-a"), "app")
		r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
		  "template":{"template":{"containers":[{"image":"fake/app","env":[
		    {"name":"FUGARO_REPOSITORY","value":"workspace-a/repo-a"}]}]}}}`)

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "bitbucket-direct")
		if len(cs) != 1 || !cs[0].OK {
			t.Fatalf("bitbucket-direct = %+v, want one ok check", cs)
		}
	})

	t.Run("a named account's variant job", func(t *testing.T) {
		flipProxiedGitList(t, config.BitbucketSecret)
		r := newBrokeringRig(t)
		addClaudeAccounts(t, r.doctorRig, "claude_accounts:\n  workspace-a-team:\n    allow_data_to: [workspace-a/repo-a]\n")
		putProjectSecret(t, r.sm, "aurora", config.ClaudeAccountSecret("workspace-a-team"), []byte("account-token"))
		// The named account runs the workflow as its own job
		// (gcp.WorkflowVariant), which is the one left mounting the token: the
		// default account's job was never deployed under that name.
		variant := gcp.JobName(mustSlug("bitbucket", "workspace-a/repo-a"), gcp.WorkflowVariant("app", "workspace-a-team"))
		r.jobMountingToken(t, variant)

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "bitbucket-direct")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("bitbucket-direct = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, variant) {
			t.Errorf("the warning does not name the variant job that still mounts it: %q", cs[0].Problem)
		}
	})

	t.Run("not a Bitbucket repository", func(t *testing.T) {
		r := newSecretReadersRig(t)
		r.withCredentialsService(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if cs := checksByID(o, "bitbucket-direct"); len(cs) != 0 {
			t.Errorf("a GitHub repository got %+v", cs)
		}
	})
}

// TestDoctorBrokeringSecretRotationAge: the operator's own record, and nothing
// else. With neither secret_rotation_days nor secret_expires recorded there is
// no age to measure against and doctor adds no Secret Manager call at all —
// which matters, since the version listing is one call per container. Both
// halves are warnings, never failures: an old credential still works.
func TestDoctorBrokeringSecretRotationAge(t *testing.T) {
	t.Run("nothing recorded", func(t *testing.T) {
		r := newBrokeringRig(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if cs := checksByID(o, "secret-rotation-age"); len(cs) != 0 {
			t.Errorf("an installation that recorded no rotation period got %+v", cs)
		}
		// Doctor lists versions for other reasons, so the call this check does
		// not make is measured against the same run with a period recorded:
		// exactly one more list, the container's own.
		with := newBrokeringRig(t)
		addLocalLines(t, with.doctorRig, "secret_rotation_days: 30\n")
		if _, err := with.doctor(t); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if extra := with.sm.VersionsListCalls() - r.sm.VersionsListCalls(); extra != 1 {
			t.Errorf("recording a rotation period added %d version list(s), want exactly the one the age is read from", extra)
		}
	})

	t.Run("inside the period", func(t *testing.T) {
		r := newBrokeringRig(t)
		addLocalLines(t, r.doctorRig, "secret_rotation_days: 30\n")
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "secret-rotation-age")
		if len(cs) != 1 || !cs[0].OK || cs[0].Severity != "info" {
			t.Fatalf("secret-rotation-age = %+v, want the one measurement of a period nothing is outside", cs)
		}
		if !strings.Contains(cs[0].Problem, "inside what the local config records") || !strings.Contains(cs[0].Problem, "30-day period") {
			t.Errorf("the measurement does not say what it measured: %q", cs[0].Problem)
		}
		// And it is a measurement: the default view neither prints it nor counts
		// it, which is what keeps the ok line the whole answer on an
		// installation whose credentials are inside the period the operator set.
		if out := doctorDefaultOutput(t, cs); out != "fugaro doctor: ok — everything checked is ok\n" {
			t.Errorf("a period nothing is outside is not one clean line:\n%s", out)
		}
	})

	t.Run("overdue", func(t *testing.T) {
		r := newBrokeringRig(t)
		addLocalLines(t, r.doctorRig, "secret_rotation_days: 30\n")
		// The fake gives every version the container's own creation time, so
		// the age is measured by moving the clock rather than the version.
		pinDoctorNow(t, func() time.Time { return time.Now().Add(100 * 24 * time.Hour) })

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "secret-rotation-age")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("secret-rotation-age = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, config.BitbucketSecret) || !strings.Contains(cs[0].Problem, "30-day rotation period") {
			t.Errorf("the warning does not name the secret and the period: %q", cs[0].Problem)
		}
		if !strings.Contains(cs[0].Problem, "old") || !strings.Contains(cs[0].Fix, "creds set") {
			t.Errorf("the warning does not give the age and the way to rotate: %+v", cs[0])
		}
	})

	t.Run("near expiry", func(t *testing.T) {
		r := newBrokeringRig(t)
		addLocalLines(t, r.doctorRig, "secret_expires:\n  "+config.BitbucketSecret+": \"2026-11-05\"\n")
		pinDoctorNow(t, func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) })

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "secret-rotation-age")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("secret-rotation-age = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, "expires on 2026-11-05") || !strings.Contains(cs[0].Problem, "in 5 days") {
			t.Errorf("the warning does not say when it expires: %q", cs[0].Problem)
		}
	})

	t.Run("a name nothing carries", func(t *testing.T) {
		r := newBrokeringRig(t)
		addLocalLines(t, r.doctorRig, "secret_expires:\n  bitbucket-toke: \"2026-11-05\"\n")
		pinDoctorNow(t, func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) })

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "secret-rotation-age")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("secret-rotation-age = %+v, want one warning for the name that matched nothing", cs)
		}
		if !strings.Contains(cs[0].Problem, "bitbucket-toke") || !strings.Contains(cs[0].Problem, "no secret container this run could list carries that name") {
			t.Errorf("the warning does not name the typo and say what was searched: %q", cs[0].Problem)
		}
	})

	t.Run("a repository's own container", func(t *testing.T) {
		// The token stored where a Bitbucket repository's belongs by default:
		// its own container, which the project-wide listing does not carry.
		r := newBrokeringCheckout(t, true)
		slug := mustSlug("bitbucket", "workspace-a/repo-a")
		addLocalLines(t, r.doctorRig, "repos:\n  workspace-a/repo-a: { provider: bitbucket, workflows: [app] }\n"+
			"secret_expires:\n  "+config.BitbucketSecret+": \"2026-11-05\"\n")
		r.sm.Seed(gcp.SecretID(slug, config.BitbucketSecret), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: config.BitbucketSecret}, []byte("repo-value"))
		pinDoctorNow(t, func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) })

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		var measured, unmatched bool
		for _, c := range checksByID(o, "secret-rotation-age") {
			if strings.Contains(c.Problem, "expires on 2026-11-05") {
				measured = true
			}
			if strings.Contains(c.Problem, "no secret container this run could list carries that name") {
				unmatched = true
			}
		}
		if !measured {
			t.Errorf("a date recorded for the repository's own container is not measured: %+v", checksByID(o, "secret-rotation-age"))
		}
		if unmatched {
			t.Errorf("a date recorded for a container that is there is reported as matching nothing: %+v", checksByID(o, "secret-rotation-age"))
		}
	})

	t.Run("expired", func(t *testing.T) {
		r := newBrokeringRig(t)
		addLocalLines(t, r.doctorRig, "secret_expires:\n  "+config.BitbucketSecret+": \"2026-10-01\"\n")
		pinDoctorNow(t, func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) })

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		cs := checksByID(o, "secret-rotation-age")
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "warning" {
			t.Fatalf("secret-rotation-age = %+v, want one warning", cs)
		}
		if !strings.Contains(cs[0].Problem, "expired on 2026-10-01") || !strings.Contains(cs[0].Problem, "29 days ago") {
			t.Errorf("the warning does not say how long ago it expired: %q", cs[0].Problem)
		}
	})
}

// TestDoctorBrokeringModelKeyInAgent: the reading an owner asked for — which
// model credential a workflow's agent holds in its own environment. It is
// information, never a failure (running without the budget is a legitimate
// choice), and it is silent whenever something holds the credential away from
// the agent: the budget gateway for an api-key run, or the credentials service.
func TestDoctorBrokeringModelKeyInAgent(t *testing.T) {
	newRig := func(t *testing.T, auth string, withService bool) *doctorRig {
		t.Helper()
		r := newDoctorRig(t)
		yaml := strings.Replace(cliMinimalYAML, "workflows:", "agent: { auth: "+auth+" }\nworkflows:", 1)
		dir := checkoutWith(t, map[string]string{"fugaro.yaml": yaml})
		testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
		if withService {
			addLocalLines(t, r, "credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n")
		}
		return r
	}
	check := func(t *testing.T, r *doctorRig) []doctorCheck {
		t.Helper()
		out, _, _ := execute(t, "doctor", "--json")
		var o doctorOutput
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatalf("json: %v\n%s", err, out)
		}
		return checksByID(o, "model-key-in-agent")
	}

	t.Run("api-key, no service", func(t *testing.T) {
		cs := check(t, newRig(t, "api-key", false))
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "info" {
			t.Fatalf("model-key-in-agent = %+v, want one informational line", cs)
		}
		if !strings.Contains(cs[0].Problem, config.ReservedSecrets[config.AnthropicSecret]) || !strings.Contains(cs[0].Problem, "budget.mode is off") {
			t.Errorf("the line does not name the variable and why it is there: %q", cs[0].Problem)
		}
	})

	t.Run("oauth, no service", func(t *testing.T) {
		cs := check(t, newRig(t, "oauth", false))
		if len(cs) != 1 || cs[0].OK || cs[0].Severity != "info" {
			t.Fatalf("model-key-in-agent = %+v, want one informational line", cs)
		}
		if !strings.Contains(cs[0].Problem, config.ReservedSecrets[config.ClaudeOAuthToken]) {
			t.Errorf("the line does not name the variable: %q", cs[0].Problem)
		}
	})

	t.Run("the credentials service holds it", func(t *testing.T) {
		if cs := check(t, newRig(t, "api-key", true)); len(cs) != 0 {
			t.Errorf("an installation that brokers its credentials got %+v", cs)
		}
	})

	t.Run("vertex carries none", func(t *testing.T) {
		if cs := check(t, newRig(t, "vertex", false)); len(cs) != 0 {
			t.Errorf("an installation whose agent holds no key of its own got %+v", cs)
		}
	})
}

// TestDoctorBrokeringStatesOnTheOneScreen is item 1's classification read from
// the checks' own emitters rather than from a fixture: every state of the six
// secrets-brokering IDs is driven through the rig and then through the three
// views. What each state is worth is decided by what the check emits — a
// warning is an item of the default view, printed with the command that fixes
// it; a measurement is printed by --all and counted by nothing; an information
// line is counted in the pointer the default view ends with, which is where the
// owner's ruling of 2026-10-31 puts model-key-in-agent.
func TestDoctorBrokeringStatesOnTheOneScreen(t *testing.T) {
	wants := func(t *testing.T, got string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("the screen does not carry %q:\n%s", w, got)
			}
		}
	}
	// counted is the pointer the default view ends with, per kind: the counts
	// themselves are shared with the other checks a whole rig emits, so what
	// this pins is that the state is counted rather than dropped — the screen's
	// last line is the pointer for its kind, and a state the view neither prints
	// nor counts would leave no line at all.
	counted := func(t *testing.T, def, flag string) {
		t.Helper()
		last := strings.TrimRight(def, "\n")
		if i := strings.LastIndex(last, "\n"); i >= 0 {
			last = last[i+1:]
		}
		if !strings.HasSuffix(last, flag) || !strings.Contains(last, "finding") {
			t.Errorf("the default view does not end with the pointer for %s: %q\n%s", flag, last, def)
		}
	}

	t.Run("the credentials service is missing", func(t *testing.T) {
		r := newBrokeringRig(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		def := doctorDefaultOutput(t, o.Checks)
		wants(t, def,
			"warning credentials-service-deployed: the credentials service's callers could not be checked, or a job account it should admit cannot invoke it.",
			"update --provision-credentials")
	})

	t.Run("the credentials service is deployed and its callers are granted", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.svcs.AddService(infra.CredentialsServiceName)
		r.svcs.SetServicePolicy(infra.CredentialsServiceName,
			gcpfake.Binding{Role: "roles/run.invoker", Members: []string{brokeringJobAccount("app")}})
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if def := doctorDefaultOutput(t, o.Checks); strings.Contains(def, "credentials-service-deployed") {
			t.Errorf("a check that passed is an item of the default view:\n%s", def)
		}
	})

	// The flag's own answer: the bundle holds the token, and Bitbucket accepts
	// it. Both are measurements, so neither is an item and neither is counted —
	// what a reader who passed the flag wants is the bundle's own line under
	// --all, and that is where it is.
	t.Run("the bundle holds a token Bitbucket accepts", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
		srv, _ := bitbucketFake(t, http.StatusOK)
		useBitbucketRoot(t, srv.URL, srv.Client())

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		def := doctorDefaultOutput(t, o.Checks)
		if strings.Contains(def, "bundle-bitbucket-token") || strings.Contains(def, "bitbucket-token-valid") {
			t.Errorf("a measurement is printed on the default screen:\n%s", def)
		}
		wants(t, doctorAllOutput(t, o.Checks),
			"info bundle-bitbucket-token: the credentials service's bundle", "holds a "+config.BitbucketSecret+" credential")
	})

	t.Run("the bundle holds no token and a repository is proxied", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, config.AnthropicSecret+": "+credsValue(t, "model")+"\n")
		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		def := doctorDefaultOutput(t, o.Checks)
		wants(t, def,
			"warning bundle-bitbucket-token: the credentials service's bundle holds no Bitbucket token, or could not be read.",
			"creds set "+config.BitbucketSecret)
	})

	t.Run("Bitbucket refuses the token", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
		srv, _ := bitbucketFake(t, http.StatusUnauthorized)
		useBitbucketRoot(t, srv.URL, srv.Client())

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		def := doctorDefaultOutput(t, o.Checks)
		wants(t, def,
			"warning bitbucket-token-valid: Bitbucket refuses the bundle's Bitbucket token, or could not be asked about it.",
			"creds set "+config.BitbucketSecret)
	})

	// The unanswered question: Bitbucket failing rather than refusing is
	// information doctor must not lose — the token may be fine — and the marker
	// is what keeps it out of the measurements, where it would read as a fact
	// doctor read and found nothing in.
	t.Run("Bitbucket does not answer", func(t *testing.T) {
		r := newBrokeringRig(t)
		r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
		srv, _ := bitbucketFake(t, http.StatusBadGateway)
		useBitbucketRoot(t, srv.URL, srv.Client())

		o, err := r.doctor(t, "--check-bitbucket-token")
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "bitbucket-token-valid")
		if len(cs) != 1 || cs[0].Class != doctorClassAudit {
			t.Fatalf("bitbucket-token-valid = %+v, want the counted state", cs)
		}
		def := doctorDefaultOutput(t, o.Checks)
		if strings.Contains(def, "bitbucket-token-valid") {
			t.Errorf("an unanswered question is printed instead of counted:\n%s", def)
		}
		counted(t, def, "`fugaro doctor --all`")
	})

	// Before the flip a proxied repository's own mount is the installation's
	// state, and a repository nobody proxied is holding its token by design:
	// both are information, both are counted, neither is an item.
	t.Run("the repository holds its own token", func(t *testing.T) {
		r := newBrokeringDirectRig(t)
		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("doctor: %v", err)
		}
		cs := checksByID(o, "bitbucket-direct")
		if len(cs) != 1 || cs[0].Class != doctorClassAudit {
			t.Fatalf("bitbucket-direct = %+v, want the counted audit state", cs)
		}
		def := doctorDefaultOutput(t, o.Checks)
		if strings.Contains(def, "bitbucket-direct") {
			t.Errorf("an audit line is printed on the default screen:\n%s", def)
		}
		counted(t, def, "`fugaro doctor --all`")
	})

	t.Run("a job still mounts the token after the flip", func(t *testing.T) {
		flipProxiedGitList(t, config.BitbucketSecret)
		r := newBrokeringRig(t)
		job := r.jobMountingBitbucketToken(t)

		o, err := r.doctor(t)
		if err != nil {
			t.Fatalf("a warning must not fail doctor without --strict: %v", err)
		}
		def := doctorDefaultOutput(t, o.Checks)
		wants(t, def,
			"warning bitbucket-direct: a job of a proxied Bitbucket repository still mounts its own token, or that could not be read.",
			"update --repo workspace-a/repo-a")
		if !strings.Contains(doctorAllOutput(t, o.Checks), job) {
			t.Errorf("--all does not name the job that still mounts the token")
		}
	})

	// The owner's ruling of 2026-10-31: a model credential that reaches a
	// workflow's agent is a counted security finding. It is not an item — the
	// installation that runs without the budget is a legitimate one — so the
	// default screen carries it as the count it ends with, and --security prints
	// it whole with its fix.
	t.Run("the model key reaches the agent", func(t *testing.T) {
		newDoctorRig(t)
		dir := checkoutWith(t, map[string]string{"fugaro.yaml": strings.Replace(cliMinimalYAML,
			"workflows:", "agent: { auth: api-key }\nworkflows:", 1)})
		testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
		out, _, _ := execute(t, "doctor", "--json")
		var o doctorOutput
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatalf("json: %v\n%s", err, out)
		}
		cs := checksByID(o, "model-key-in-agent")
		if len(cs) != 1 || cs[0].Class != doctorClassAudit {
			t.Fatalf("model-key-in-agent = %+v, want the counted security audit finding", cs)
		}
		def := doctorDefaultOutput(t, o.Checks)
		if strings.Contains(def, "model-key-in-agent") {
			t.Errorf("the default view prints the sweep's own line:\n%s", def)
		}
		counted(t, def, "`fugaro doctor --security`")
		wants(t, doctorSecurityOutput(t, o.Checks),
			"model-key-in-agent", "the gateway holds the key")
	})
}

// TestDoctorBrokeringCleanInstallationIsOneLine: the same rule the owner's one
// line is about, on the checks this task adds. An installation that opted in,
// whose service is deployed and granted, whose bundle holds a token Bitbucket
// accepts, whose credentials are inside the period the operator recorded and
// whose agent holds no key of its own prints one line: every one of the six is
// a check that passed or a measurement doctor made, and neither is an item or a
// count.
func TestDoctorBrokeringCleanInstallationIsOneLine(t *testing.T) {
	flipProxiedGitList(t, config.BitbucketSecret)
	r := newBrokeringRig(t)
	r.svcs.AddService(infra.CredentialsServiceName)
	r.svcs.SetServicePolicy(infra.CredentialsServiceName,
		gcpfake.Binding{Role: "roles/run.invoker", Members: []string{brokeringJobAccount("app")}})
	r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
	srv, _ := bitbucketFake(t, http.StatusOK)
	useBitbucketRoot(t, srv.URL, srv.Client())
	addLocalLines(t, r.doctorRig, "secret_rotation_days: 30\n")
	job := gcp.JobName(mustSlug("bitbucket", "workspace-a/repo-a"), "app")
	r.run.SetJobJSON(job, `{"name":"projects/proj-1234/locations/us-east5/jobs/`+job+`",
	  "template":{"template":{"containers":[{"image":"fake/app","env":[
	    {"name":"FUGARO_REPOSITORY","value":"workspace-a/repo-a"}]}]}}}`)

	o, err := r.doctor(t, "--check-bitbucket-token")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	var mine []doctorCheck
	for _, c := range o.Checks {
		switch c.ID {
		case "credentials-service-deployed", "secret-rotation-age", "bundle-bitbucket-token",
			"bitbucket-token-valid", "bitbucket-direct", "model-key-in-agent":
			mine = append(mine, c)
		}
	}
	if len(mine) < 5 {
		t.Fatalf("the rig did not emit the brokering checks: %+v", o.Checks)
	}
	if got := doctorDefaultOutput(t, mine); got != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("a clean installation's brokering checks are not one line:\n%s", got)
	}
}

// TestDoctorBitbucketTokenNeverPrinted is the rule the whole flag is built
// around: the bundle's value is used in memory and appears nowhere — not in
// the printed output, not in --json, not in an error string. Both of the
// document's values are looked for, and the check is run in the state that
// prints most: the token accepted, refused, and the run refused before it read.
func TestDoctorBitbucketTokenNeverPrinted(t *testing.T) {
	token := credsValue(t, "token")
	model := credsValue(t, "model")
	for _, tc := range []struct {
		name   string
		status int
		flag   bool
	}{
		{"accepted", http.StatusOK, true},
		{"refused", http.StatusUnauthorized, true},
		{"without the flag", http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newBrokeringRig(t)
			r.putBundle(t, brokeringBundleDoc(t, token))
			srv, _ := bitbucketFake(t, tc.status)
			useBitbucketRoot(t, srv.URL, srv.Client())

			args := []string{"doctor", "--json"}
			plainArgs := []string{"doctor"}
			if tc.flag {
				args = append(args, "--check-bitbucket-token")
				plainArgs = append(plainArgs, "--check-bitbucket-token")
			}
			out, errOut, _ := execute(t, args...)
			plain, plainErr, _ := execute(t, plainArgs...)
			for what, got := range map[string]string{
				"the json output":    out,
				"the json error":     errOut,
				"the printed output": plain,
				"the printed error":  plainErr,
			} {
				for _, value := range []string{token, model} {
					if strings.Contains(got, value) {
						t.Errorf("%s carries a credential's value:\n%s", what, got)
					}
				}
			}
		})
	}
}

// TestDoctorCheckBitbucketTokenRefusedInAgentSession: the flag reads a value
// with the operator's own credentials, so it is refused under a coding agent's
// marker before anything is read — the same refusal `creds ls` carries, and
// for the same reason.
func TestDoctorCheckBitbucketTokenRefusedInAgentSession(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			r := newBrokeringRig(t)
			bundle := r.putBundle(t, brokeringBundleDoc(t, credsValue(t, "token")))
			t.Setenv(marker, "1")

			_, _, err := execute(t, "doctor", "--check-bitbucket-token", "--json")
			if err == nil || !strings.Contains(err.Error(), "refused in a coding agent's session") || ExitCode(err) != ExitUserError {
				t.Fatalf("doctor --check-bitbucket-token under %s: %v", marker, err)
			}
			for _, id := range r.sm.Accessed() {
				if id == bundle {
					t.Error("the bundle was read before the refusal")
				}
			}
		})
	}
}
