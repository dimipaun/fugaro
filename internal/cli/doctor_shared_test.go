package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// publishFor puts the marker and shared config (content) of aurora in a
// runs bucket on disk behind the seams, and points the fetched config at the
// rig's fakes the way the end-to-end test does.
func publishFor(t *testing.T, r *doctorRig, content string) {
	t.Helper()
	runs := filepath.Join(r.dir, "runs")
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject)), []byte(`{"version":1,"name":"aurora","gcp_project":"proj-1234"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, filepath.FromSlash(infra.SharedConfigObject)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	oldOpen, oldFetch := sharedBucketOpener, sharedFetch
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+runs) }
	sharedFetch = func(ctx context.Context, getenv func(string) string, now time.Time, name, gcp string) (*localcfg.Config, string, error) {
		c, note, err := fetchSharedConfig(ctx, getenv, now, name, gcp)
		if err == nil {
			c.Endpoints = localcfg.Endpoints{ResourceManager: r.crm.URL + "/", CloudBilling: r.billing.URL + "/", SecretManager: r.sm.URL + "/", NoAuth: true}
		}
		return c, note, err
	}
	t.Cleanup(func() { sharedBucketOpener, sharedFetch = oldOpen, oldFetch })
}

func doctorJSON(t *testing.T, args ...string) doctorOutput {
	t.Helper()
	out, _, err := execute(t, append([]string{"doctor", "--json", "--dir", t.TempDir()}, args...)...)
	var o doctorOutput
	if jerr := json.Unmarshal([]byte(out), &o); jerr != nil {
		t.Fatalf("json: %v (err %v)\n%s", jerr, err, out)
	}
	return o
}

// A fresh machine (no local config) in a checkout that names gcp_project
// reaches selection, and doctor says the config is the shared one.
func TestDoctorReportsASharedConfig(t *testing.T) {
	r := newDoctorRig(t)
	if err := os.Remove(r.cfgPath); err != nil {
		t.Fatal(err)
	}
	publishFor(t, r, auroraShared())
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	o := doctorJSON(t)
	if _, ok := doctorCheckByID(o.Checks, "installation"); ok {
		t.Fatalf("doctor stopped at 'no installation': %+v", o.Checks)
	}
	c, ok := doctorCheckByID(o.Checks, "shared-config")
	if !ok || c.Severity != "info" {
		t.Fatalf("shared-config check = %+v (%v)", c, ok)
	}
	if !strings.Contains(c.Problem, "the project's config is the shared file published to the runs bucket (generation ") || !strings.Contains(c.Problem, "checked ") || !strings.Contains(c.Problem, " ago)") {
		t.Errorf("problem = %q", c.Problem)
	}
	if c.Fix != "fugaro init creates your own local config" {
		t.Errorf("fix = %q", c.Fix)
	}
	if o.Project == nil || o.Project.Name != "aurora" {
		t.Errorf("project = %+v", o.Project)
	}
}

// --gcp-project alone (no committed line) reaches selection too.
func TestDoctorFlagGCPProjectReachesSelection(t *testing.T) {
	r := newDoctorRig(t)
	if err := os.Remove(r.cfgPath); err != nil {
		t.Fatal(err)
	}
	publishFor(t, r, auroraShared())
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\n"))
	o := doctorJSON(t, "--gcp-project", "proj-1234")
	if _, ok := doctorCheckByID(o.Checks, "shared-config"); !ok {
		t.Fatalf("no shared-config check: %+v", o.Checks)
	}
}

// No local config and no gcp_project anywhere still says to run fugaro init.
func TestDoctorStillSaysRunInitWithoutAGCPProject(t *testing.T) {
	r := newDoctorRig(t)
	if err := os.Remove(r.cfgPath); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\n"))
	o := doctorJSON(t)
	if c, ok := doctorCheckByID(o.Checks, "installation"); !ok || c.OK {
		t.Fatalf("installation = %+v", c)
	}
}

// A local file wins silently; doctor notes when the published one differs.
func TestDoctorNotesAPublishedConfigThatDiffers(t *testing.T) {
	r := newDoctorRig(t)
	differs := strings.Replace(auroraShared(), "max_parallel: 20", "max_parallel: 3", 1)
	publishFor(t, r, differs)
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	o := doctorJSON(t)
	if _, ok := doctorCheckByID(o.Checks, "shared-config"); ok {
		t.Fatalf("a local config won, yet doctor says it is the shared one")
	}
	c, ok := doctorCheckByID(o.Checks, "shared-config-differs")
	if !ok || c.Severity != "info" || !strings.Contains(c.Problem, "differs") || !strings.Contains(c.Problem, "max_parallel") {
		t.Fatalf("differs check = %+v (%v)", c, ok)
	}
}

// With nothing published, or no way to read it, doctor says nothing about it
// and does not fail because of it.
func TestDoctorToleratesAnUnreadablePublishedConfig(t *testing.T) {
	r := newDoctorRig(t)
	old := sharedFetch
	sharedFetch = func(context.Context, func(string) string, time.Time, string, string) (*localcfg.Config, string, error) {
		return nil, "", userErr("no access")
	}
	t.Cleanup(func() { sharedFetch = old })
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))
	o := doctorJSON(t)
	if _, ok := doctorCheckByID(o.Checks, "shared-config-differs"); ok {
		t.Fatalf("a difference was reported without a published file")
	}
	if o.Error != "" {
		t.Fatalf("doctor failed: %s", o.Error)
	}
}
