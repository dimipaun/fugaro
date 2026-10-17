package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func getenvMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func checkByID(cs []Check, id string) (Check, bool) {
	for _, c := range cs {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

func TestEnvironmentAllOKWhenClean(t *testing.T) {
	for _, c := range Environment(getenvMap(nil), "proj-1234") {
		if !c.OK {
			t.Errorf("%s: unexpectedly not ok: %s", c.ID, c.Problem)
		}
	}
}

// The standing rule: a project named by the environment, including the one
// gcloud's own "default project" reads (CLOUDSDK_CORE_PROJECT), is never
// silently trusted. Environment takes the installation's project as an
// explicit argument and only ever compares the environment against it.
func TestGcloudDefaultProjectNeverUsed(t *testing.T) {
	for _, k := range []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		t.Run(k, func(t *testing.T) {
			cs := Environment(getenvMap(map[string]string{k: "other-project"}), "proj-1234")
			c, ok := checkByID(cs, "env-project")
			if !ok || c.OK {
				t.Fatalf("env-project = %+v, want a problem", c)
			}
			if !strings.Contains(c.Problem, k) || !strings.Contains(c.Problem, "other-project") || !strings.Contains(c.Fix, "proj-1234") {
				t.Fatalf("problem %q fix %q didn't name %s, other-project and proj-1234", c.Problem, c.Fix, k)
			}
			// The same project is never a problem: it is a confirmation, not a
			// default taken from the environment.
			if c, _ := checkByID(Environment(getenvMap(map[string]string{k: "proj-1234"}), "proj-1234"), "env-project"); !c.OK {
				t.Fatalf("the same project in %s was refused: %+v", k, c)
			}
		})
	}
}

func TestEnvironmentImpersonation(t *testing.T) {
	cs := Environment(getenvMap(map[string]string{"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT": "someone@proj.iam.gserviceaccount.com"}), "proj-1234")
	c, ok := checkByID(cs, "env-impersonation")
	if !ok || c.OK || !strings.Contains(c.Problem, "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT") {
		t.Fatalf("env-impersonation = %+v", c)
	}
}

func TestEnvironmentHTTP2Debug(t *testing.T) {
	cs := Environment(getenvMap(map[string]string{"GODEBUG": "http2debug=2,gctrace=1"}), "proj-1234")
	c, ok := checkByID(cs, "env-http2debug")
	if !ok || c.OK || !strings.Contains(c.Problem, "http2debug") {
		t.Fatalf("env-http2debug = %+v", c)
	}
}

func lookPathMiss(string) (string, error) { return "", exec.ErrNotFound }

func TestDocker(t *testing.T) {
	if c := Docker(lookPathMiss, false); !c.OK {
		t.Fatalf("docker not needed but refused: %+v", c)
	}
	c := Docker(lookPathMiss, true)
	if c.OK || !strings.Contains(c.Problem, "docker") {
		t.Fatalf("docker needed and missing: %+v", c)
	}
}

func TestTerraformMissing(t *testing.T) {
	_, c := Terraform(lookPathMiss)
	if c.OK || !strings.Contains(c.Fix, "terraform") {
		t.Fatalf("missing terraform: %+v", c)
	}
}

// fakeTerraform builds the package's fake terraform binary once, reporting
// the version its FAKE_TERRAFORM_SCRIPT env var scripts (or 1.16.4 by
// default), exactly as internal/infra/tf's own tests do.
var (
	fakeTFOnce sync.Once
	fakeTFBin  string
	fakeTFErr  error
)

func fakeTerraform(t *testing.T) string {
	t.Helper()
	fakeTFOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fugaro-preflight-faketerraform-")
		if err != nil {
			fakeTFErr = err
			return
		}
		fakeTFBin = filepath.Join(dir, "terraform")
		out, err := exec.Command("go", "build", "-o", fakeTFBin, "github.com/dimipaun/fugaro/internal/infra/tf/faketerraform").CombinedOutput()
		if err != nil {
			fakeTFErr = fmt.Errorf("building the fake terraform: %v\n%s", err, out)
		}
	})
	if fakeTFErr != nil {
		t.Fatal(fakeTFErr)
	}
	return fakeTFBin
}

func lookPathAt(bin string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name != "terraform" {
			return "", exec.ErrNotFound
		}
		return bin, nil
	}
}

func TestTerraformSupportedVersion(t *testing.T) {
	bin, c := Terraform(lookPathAt(fakeTerraform(t)))
	if bin != fakeTerraform(t) || !c.OK {
		t.Fatalf("bin %q check %+v", bin, c)
	}
}

func TestTerraformUnsupportedVersion(t *testing.T) {
	bin := fakeTerraform(t)
	t.Setenv("FAKE_TERRAFORM_SCRIPT", `{"version":{"stdout":"{\"terraform_version\":\"1.6.6\"}"}}`)
	_, c := Terraform(lookPathAt(bin))
	if c.OK || !strings.Contains(c.Problem, "1.6.6") || strings.Contains(c.Fix, "\n") {
		t.Fatalf("unsupported version: %+v", c)
	}
}

// supportedTerraform is a byte-for-byte mirror of internal/infra/tf's
// unexported checkVersion (by design: preflight stays a leaf package, see
// google.go's quotaProjectIfDisabled comment for the same tradeoff), so the
// two can drift independently. This is internal/infra/tf/tf_test.go's own
// TestVersionTooOld table, so an edit that silently breaks either copy's
// boundary logic is caught here too.
func TestSupportedTerraform(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{"1.6.6", false},
		{"1.7.0-beta1", false},
		{"0.15.5", false},
		{"2.0.0", false},
		{"2.0.0-alpha1", false},
		{"garbage", false},
		{"1.7.0", true},
		{"1.16.4", true},
	} {
		if got := supportedTerraform(tc.version); got != tc.ok {
			t.Errorf("supportedTerraform(%q) = %v, want %v", tc.version, got, tc.ok)
		}
	}
}

// Every Fix a person might read from a terminal or a plan view is one
// line: no printed command may wrap or hide a second line when pasted.
func TestFixesAreSingleLine(t *testing.T) {
	var cs []Check
	cs = append(cs, Environment(getenvMap(map[string]string{
		"GOOGLE_CLOUD_PROJECT":               "other",
		"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT": "x@y.iam.gserviceaccount.com",
		"GODEBUG":                            "http2debug=1",
	}), "proj-1234")...)
	cs = append(cs, Docker(lookPathMiss, true))
	_, c := Terraform(lookPathMiss)
	cs = append(cs, c)
	t.Setenv("FAKE_TERRAFORM_SCRIPT", `{"version":{"stdout":"{\"terraform_version\":\"1.6.6\"}"}}`)
	_, c = Terraform(lookPathAt(fakeTerraform(t)))
	cs = append(cs, c)

	for _, c := range cs {
		if c.OK {
			continue
		}
		if c.Fix == "" {
			t.Errorf("%s: ok=false with no fix", c.ID)
		}
		if strings.Contains(c.Fix, "\n") {
			t.Errorf("%s: fix is not one line: %q", c.ID, c.Fix)
		}
		if strings.Contains(c.Problem, "\n") {
			t.Errorf("%s: problem is not one line: %q", c.ID, c.Problem)
		}
	}
}
