package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// config_publish_managed.go's tests: M2's rules for `fugaro config publish`
// (plan §M2, spec §4.2), read the way the command runs them — through the
// command, in a coding agent's session, against the fakes and one file://
// bucket. Nothing here contacts a cloud, and the credentials are read off a
// path in the test's own directory, so a developer's own account can never
// decide what a record says.
//
// The rig is the cloud fixture plus the two marks: the installation's GCP
// project (proj-1234, out of the config and never out of a repository) carries
// the label, and its own runs bucket holds the creation fact object. The layer
// object itself lives in the fixture's file:// bucket, which is where the
// command reads and writes it.

// The managed installation these tests publish in. The GCP project is the one
// the cloud fixture's config names, and the runs bucket is the project's own,
// which is where both the creation fact and the audit record live.
const (
	managedPublishGCP    = "proj-1234"
	managedPublishNumber = 31337
)

func managedPublishBucket() string { return "fugaro-runs-" + managedPublishGCP }

// managedPublishMarker is the creation fact of the managed installation: the
// version this build knows, naming the GCP project the read is asked about.
func managedPublishMarker() []byte {
	return []byte(`{"version":1,"gcp_project":"` + managedPublishGCP + `"}`)
}

// managedLayer is a layer every rule M2 gives this command passes: registry
// keys only, no executable key, nothing that widens a route, an A/B block or a
// cap.
const managedLayer = minimalAnchored + "defaults:\n  agent: { auth: vertex }\n"

// managedPublishRig is one managed installation: both marks, the clients over
// the fakes, and the cloud fixture the command reads its config from.
type managedPublishRig struct {
	*cloudFixture
	gcs *gcpfake.GCS
	crm *gcpfake.CRM
	s   *storage.Service
}

func newManagedPublishRig(t *testing.T) *managedPublishRig {
	t.Helper()
	dir := t.TempDir()
	// The credentials are isolated the way the home directory is: a file that
	// happened to be on the machine would otherwise name a real address in the
	// audit record and in this test's failure output.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "no-application-default-credentials.json"))
	t.Setenv("HOME", dir)
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })
	oldNow := managedGateNow
	managedGateNow = func() time.Time { return managedGateTestNow }
	t.Cleanup(func() { managedGateNow = oldNow })

	gcs := gcpfake.NewGCS(t)
	gcs.AddBucket(managedPublishBucket(), managedPublishNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	gcs.Put(managedPublishBucket(), managed.MarkerObject, managedPublishMarker())
	c := gcpfake.NewCRM(t)
	c.AddProject(managedPublishGCP, managedPublishNumber)
	labelled(c, managedPublishGCP)

	f := newCloudFixture(t, "storage: "+gcs.URL+"/storage/v1/", "resource_manager: "+c.URL+"/")
	return &managedPublishRig{cloudFixture: f, gcs: gcs, crm: c, s: managedStorage(t, gcs)}
}

// setConfigScalar rewrites the local config's key to value, adding it when the
// fixture's own text does not carry it: the D854 test records a generation this
// machine wrote its layer file from, which is the value the check compares the
// published object's generation against.
func setConfigScalar(t *testing.T, key, value string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := key + ": " + value + "\n"
	lines := strings.Split(string(data), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, key+":") {
			lines[i], found = strings.TrimSuffix(line, "\n"), true
		}
	}
	out := strings.Join(lines, "\n")
	if !found {
		out += line
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// agentSession is a coding agent's session: the conditions under which the
// managed case is the difference between proceeding and refusing.
func agentSession(t *testing.T) {
	t.Helper()
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
}

// auditKeys is every object of audit family in the runs bucket, so a test can
// say "nothing was written" about an empty listing rather than about a record
// it did not think to look for.
func (r *managedPublishRig) auditKeys(t *testing.T) []string {
	t.Helper()
	list, err := r.s.Objects.List(managedPublishBucket()).Prefix(managedmode.AuditPrefix).Do()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(list.Items))
	for _, o := range list.Items {
		out = append(out, o.Name)
	}
	return out
}

// audits is the day's records, read back through the reader a surface uses.
func (r *managedPublishRig) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), r.s, managedPublishBucket(), managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The credentials key: a managed project's gate reads the document an agent's
// session is about to publish, and a key no agent may write is refused by the
// gate — the session reads the rule that fired and not "your own terminal".
func TestConfigPublishManagedRefusesACredentialsKey(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	layer := layerFile(t, testProjectLayer+"credentials:\n  anthropic: { api_key: not-a-real-key }\n")
	_, _, err := execute(t, "config", "publish", "--executable-changes", layer)
	if err == nil {
		t.Fatal("a layer writing a reserved key was published in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "credentials") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the refusal does not name the reserved key that refused: %v", err)
	}
	if strings.Contains(err.Error(), "your own terminal") || strings.Contains(err.Error(), "nothing is applied") {
		t.Errorf("the marker's own refusal answered instead of the gate's: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != "" {
		t.Fatalf("a reserved key reached the bucket: %q", got)
	}
	if n := len(r.auditKeys(t)); n != 1 {
		t.Errorf("the refusal left %d audit records, want one (D1025)", n)
	}
}

// The D854 generation check: this machine's layer file was written from another
// machine's layer (its recorded generation is not the published object's), so
// publishing it would replace a layer this machine cannot account for.
func TestConfigPublishManagedRefusesAStaleGeneration(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	if err := os.WriteFile(localLayerPath(t), []byte(managedLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	setConfigScalar(t, "layer_generation", `"7"`)
	_, _, err := execute(t, "config", "publish")
	if err == nil {
		t.Fatal("a stale local layer file was published in a coding agent's session")
	}
	if !strings.Contains(err.Error(), layerLocalStale) {
		t.Errorf("the refusal does not name the stale copy D854 is about: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != minimalAnchored {
		t.Fatalf("the published layer moved: %q", got)
	}
	if n := len(r.auditKeys(t)); n != 1 {
		t.Errorf("the refusal left %d audit records, want one (D1025)", n)
	}
}

// The other side: a layer every rule passes is admitted in a managed project's
// coding agent's session, the session reads AgentProceeded, the object is
// published, and the admission leaves its record.
func TestConfigPublishManagedAdmitsARegistryKey(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	out, errOut, err := execute(t, "config", "publish", layerFile(t, managedLayer))
	if err != nil {
		t.Fatalf("a layer every rule passes was refused: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the session does not read that the step proceeded: %q", out)
	}
	if !strings.Contains(out, "published the project layer") {
		t.Errorf("the admitted step published nothing: %q", out)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != managedLayer {
		t.Fatalf("the bucket holds %q, want the admitted layer", got)
	}
	got := r.audits(t)
	if len(got) != 1 {
		t.Fatalf("the admission left %d records, want exactly one (a command that forgot gate.recorded leaves none)", len(got))
	}
	if got[0].Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q", got[0].Verdict, managedmode.AuditAdmitted)
	}
	if got[0].PlanDigest != gateDigest([]byte(managedLayer)) {
		t.Errorf("the record's digest is %q, and the document the gate saw is not the one it names", got[0].PlanDigest)
	}
}
