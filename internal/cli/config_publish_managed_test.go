package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
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
	text := testProjectLayer + "credentials:\n  anthropic: { api_key: not-a-real-key }\n"
	_, errOut, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, text))
	if err == nil {
		t.Fatal("a layer writing a reserved key was published in a coding agent's session")
	}
	// The header is held while the marks are read and released once they have
	// answered managed: a refusal BY a rule of the gate is a session the gate
	// judged, so it reads the project it is refused on, exactly once.
	if n := strings.Count(errOut, "project: aurora (GCP proj-1234)"); n != 1 {
		t.Errorf("the session the gate judged read the project header %d times, want exactly one (the held header is released when the marks answer managed): %q", n, errOut)
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
	rec := r.audits(t)
	if len(rec) != 1 {
		t.Fatalf("the refusal's record could not be read back: %d records", len(rec))
	}
	if rec[0].Verdict != managedmode.AuditRefused {
		t.Errorf("the record's verdict is %q, want %q", rec[0].Verdict, managedmode.AuditRefused)
	}
	if rules := strings.Join(rec[0].Rules, " "); !strings.Contains(rules, "credentials") {
		t.Errorf("the record does not name the rule that refused: %q", rules)
	}
	if rec[0].PlanDigest != gateDigest([]byte(text)) {
		t.Errorf("the record's digest is %q, and the document the gate judged is not the one it names", rec[0].PlanDigest)
	}
	if rec[0].AgentMarker != "CLAUDECODE" || rec[0].Identity == "" {
		t.Errorf("the record names marker %q and identity %q, want the session's marker and an account", rec[0].AgentMarker, rec[0].Identity)
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

// The same stale local file, with FILE naming a layer kept somewhere else: the
// document under judgement is not this machine's layer file, so D854 has no
// subject here and the unrelated file's recorded generation cannot refuse a
// publish it says nothing about. The admission's record does not claim the rule
// either — a rule named among the ones that passed is a rule that ran (D1014),
// and this one is about a file the command never read.
func TestConfigPublishManagedAdmitsAFileWhileTheLocalLayerIsStale(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	if err := os.WriteFile(localLayerPath(t), []byte(managedLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	setConfigScalar(t, "layer_generation", `"7"`)
	out, errOut, err := execute(t, "config", "publish", layerFile(t, managedLayer))
	if err != nil {
		t.Fatalf("a publish of a layer kept elsewhere was refused by the state of this machine's own file: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the session does not read that the step proceeded: %q", out)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != managedLayer {
		t.Fatalf("the bucket holds %q, want the published layer", got)
	}
	rec := r.audits(t)
	if len(rec) != 1 || rec[0].Verdict != managedmode.AuditAdmitted {
		t.Fatalf("the record is %v, want one admission", rec)
	}
	if rules := strings.Join(rec[0].Rules, " "); strings.Contains(rules, "generation") {
		t.Errorf("the record claims the D854 rule ran for a file that was never the document under judgement: %q", rules)
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
	// The other direction of the held header: a session the gate ADMITS reads
	// the project it published to, exactly once. A gate that held the buffer
	// and never released it would leave an admitted run with no header at all.
	if n := strings.Count(errOut, "project: aurora (GCP proj-1234)"); n != 1 {
		t.Errorf("the admitted session read the project header %d times, want exactly one: %q", n, errOut)
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

// deadEndpoint is an address nothing listens on: a managed read pointed there
// fails instead of answering, which is what makes "no mark was read" a property
// a test can observe rather than a claim about the code.
func deadEndpoint(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// The other installation: no mark of any kind, and both managed endpoints at an
// address nothing answers on, so a person's own run that consulted the gate
// would fail where this one publishes.
func newUnmanagedPublishRig(t *testing.T) *cloudFixture {
	t.Helper()
	return newCloudFixture(t, "storage: "+deadEndpoint(t)+"/storage/v1/", "resource_manager: "+deadEndpoint(t)+"/")
}

// The wiring, from the other side (plan §5): the gate is read BEFORE the write,
// and a mark that cannot be read is not a permission. The first read fails, so
// nothing is published — a command that read the gate after its write would have
// published the layer and failed afterwards.
//
// The refusal is the one this command has always given a coding agent's session,
// and it is the SAME sentence a session whose project is simply not managed
// reads: a failed read is a refusal and not a diagnostic, so the session cannot
// learn which read broke, which endpoint carries it, or what the Resource
// Manager error said (the answer accounts_managed.go and
// image_refresh_managed.go give the same read, for the same reason). The
// project header is not printed either: no run that ends in this refusal has
// ever printed one.
func TestConfigPublishManagedFailsClosedWhenAMarkCannotBeRead(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	r.crm.FailGet(managedPublishGCP, 500, "INTERNAL", "the label read failed")
	_, errOut, err := execute(t, "config", "publish", layerFile(t, managedLayer))
	if err == nil {
		t.Fatal("a project whose mark could not be read was published to in a coding agent's session")
	}
	if !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the refusal is not the one this command has always given a coding agent's session: %v", err)
	}
	for _, leak := range []string{"the label read failed", "INTERNAL", "500", "resource manager", "cloudresourcemanager", "storage"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal carries installation diagnostics (%q), which a session that is refused anyway must not learn: %v", leak, err)
		}
	}
	if strings.Contains(errOut, "project: ") {
		t.Errorf("a session whose gate was never read printed the project header: %q", errOut)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != "" {
		t.Fatalf("a mark that could not be read was a permission: the bucket holds %q", got)
	}
	if n := len(r.auditKeys(t)); n != 0 {
		t.Errorf("a read that never judged a document left %d audit records, want none (a record with an invented digest is a lie about a plan nobody read)", n)
	}
}

// The other half of the answer above, read on an installation whose endpoints
// answer: neither mark is there — the bucket carries no label and no creation
// fact — so the read SUCCEEDS and answers "not managed". The session is refused
// in the words it has always been refused in, the header is not printed, nothing
// is published and nothing is recorded: a project that is not managed is today's
// behaviour, unchanged (TestManagedZeroValueKeepsEveryRefusal's rule).
func TestConfigPublishManagedRefusesAnAgentSessionOnAnUnmanagedProject(t *testing.T) {
	gcs := gcpfake.NewGCS(t)
	gcs.AddBucket(managedPublishBucket(), managedPublishNumber, nil)
	crmFake := gcpfake.NewCRM(t)
	crmFake.AddProject(managedPublishGCP, managedPublishNumber)
	f := newCloudFixture(t, "storage: "+gcs.URL+"/storage/v1/", "resource_manager: "+crmFake.URL+"/")
	agentSession(t)
	publishedLayer(t, f, "")
	_, errOut, err := execute(t, "config", "publish", layerFile(t, managedLayer))
	if err == nil {
		t.Fatal("a coding agent's session published to a project with no mark")
	}
	if !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the refusal is not the one this command has always given a coding agent's session: %v", err)
	}
	for _, leak := range []string{"creation fact", "storage", "resource manager"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal carries installation diagnostics (%q): %v", leak, err)
		}
	}
	if strings.Contains(errOut, "project: ") {
		t.Errorf("a session the gate never admitted printed the project header: %q", errOut)
	}
	if got := bucketText(t, f, config.LayerKey); got != "" {
		t.Fatalf("a project with no mark admitted a coding agent's session: the bucket holds %q", got)
	}
}

// A person at their own terminal, on a project with no mark at all: today's path
// exactly. The endpoints are dead, so a gate consulted here would fail the run —
// and it is not consulted, which is what M6's review of the helper asks of every
// command in the set: an owner's run on an unmanaged project pays nothing for it.
func TestConfigPublishOwnerPublishesWhenTheProjectIsNotManaged(t *testing.T) {
	f := newUnmanagedPublishRig(t)
	isolateCache(t)
	noAgentSession(t)
	publishedLayer(t, f, "")
	out, errOut, err := execute(t, "config", "publish", layerFile(t, managedLayer))
	if err != nil {
		t.Fatalf("a person's own publish on an unmanaged project was refused: %v\n%s", err, errOut)
	}
	if strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("a person's own run read the coding agent's sentence: %q", out)
	}
	if got := bucketText(t, f, config.LayerKey); got != managedLayer {
		t.Fatalf("the bucket holds %q, want the published layer", got)
	}
}

// An executable key changes and the session did not name it: refused, and the
// refusal names no flag a reader could pass to get past the gate (D1023).
func TestConfigPublishManagedRefusesAnExecutableChangeTheOwnerDidNotName(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	withCommands := minimalAnchored + "profiles:\n  svc:\n    commands: { test: sh test.sh }\n"
	if err := os.WriteFile(localLayerPath(t), []byte(withCommands), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "config", "publish")
	if err == nil {
		t.Fatal("a layer that changes what runs as shell was published in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Errorf("the refusal does not name what the change would run: %v", err)
	}
	if strings.Contains(err.Error(), "--executable-changes") || strings.Contains(err.Error(), "your own terminal") {
		t.Errorf("the refusal names a way past the gate, or the marker's own refusal: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != minimalAnchored {
		t.Fatalf("the published layer moved: %q", got)
	}
	if n := len(r.auditKeys(t)); n != 1 {
		t.Errorf("the refusal left %d audit records, want one (D1025)", n)
	}
}

// The same change with the flag written on the command line — and the command
// line is the session's own, so the flag is not the owner's consent and the
// change is refused just the same (D1015(e), D1021(b)). A flag the benefiting
// session can pass is not a confirmation.
func TestConfigPublishManagedRefusesAnExecutableChangeTheSessionNamed(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	withCommands := minimalAnchored + "profiles:\n  svc:\n    commands: { test: sh test.sh }\n"
	path := layerFile(t, withCommands)
	out, _, err := execute(t, "config", "publish", "--executable-changes", path)
	if err == nil {
		t.Fatalf("--executable-changes admitted a change to what runs as shell in a coding agent's session: %q", out)
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Errorf("the refusal does not name what the change would run: %v", err)
	}
	if strings.Contains(err.Error(), "--executable-changes") || strings.Contains(err.Error(), "your own terminal") {
		t.Errorf("the refusal names a way past the gate, or the marker's own refusal: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != minimalAnchored {
		t.Fatalf("the published layer moved: %q", got)
	}
	if n := len(r.auditKeys(t)); n != 1 {
		t.Errorf("the refusal left %d audit records, want one (D1025)", n)
	}
	if got := r.audits(t); len(got) != 1 || got[0].Verdict != managedmode.AuditRefused {
		t.Errorf("the record is %v, want one refusal", got)
	}
}

// The governance block: a key this program reads and no agent's session writes.
// It is the reserved key a registry-only rule would let through, which is why
// the gate asks both questions.
func TestConfigPublishManagedRefusesAGovernanceKey(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	_, _, err := execute(t, "config", "publish", layerFile(t, minimalAnchored+"governance:\n  max_subagents: 8\n"))
	if err == nil {
		t.Fatal("a layer moving the governance block was published in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "governance") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the refusal does not name the governance key as reserved: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != "" {
		t.Fatalf("a reserved key reached the bucket: %q", got)
	}
}

// A key the registry does not know: no run reads it, so no session publishes it
// — the registry is the rule for what a layer may say, and the parse's own
// refusal is not the one the gate answers with.
func TestConfigPublishManagedRefusesAKeyTheRegistryDoesNotKnow(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	_, _, err := execute(t, "config", "publish", layerFile(t, minimalAnchored+"mystery_key: 1\n"))
	if err == nil {
		t.Fatal("a layer naming a key no run reads was published in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "registry") || !strings.Contains(err.Error(), "mystery_key") {
		t.Errorf("the refusal does not name the registry as what refused: %v", err)
	}
}

// A cap against the published layer: a layer that raises what a run may spend
// is the widening rule 4.1.6 refuses, and the same key written lower is the
// narrowing the gate admits — the rule compares the two layers and not the
// presence of a key.
func TestConfigPublishManagedRefusesACapRaised(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	_, _, err := execute(t, "config", "publish", layerFile(t, minimalAnchored+"defaults:\n  agent: { max_budget_usd: 50 }\n"))
	if err == nil {
		t.Fatal("a layer raising the agent's spend cap was published in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "max_budget_usd") || !strings.Contains(err.Error(), "widening") {
		t.Errorf("the refusal does not name the widening that refused: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != minimalAnchored {
		t.Fatalf("the published layer moved: %q", got)
	}
	lower := minimalAnchored + "defaults:\n  agent: { max_budget_usd: 10 }\n"
	out, errOut, err := execute(t, "config", "publish", layerFile(t, lower))
	if err != nil {
		t.Fatalf("a layer lowering the cap was refused: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the session does not read that the step proceeded: %q", out)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != lower {
		t.Fatalf("the bucket holds %q, want the narrowing layer", got)
	}
}

// The write landed and the step failed afterwards: the generation record could
// not be written, so publishLayer answered a generation and an error together.
// The admission is recorded all the same (D1014) — the step applied, and a
// failure after the fact is when a trail is worth the most.
//
// The record is made to fail the way any writer beside a file fails: the config
// file's name is at the filesystem's limit, so the backup layerGenerationRecord
// writes next to it (`<name>.bak-<stamp>`) cannot be created.
func TestConfigPublishManagedRecordsAnAdmissionThatFailedLate(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	cfg, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(t.TempDir(), strings.Repeat("c", 240)+".yaml")
	if err := os.WriteFile(long, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", long)
	if err := os.WriteFile(localLayerPath(t), []byte(managedLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "config", "publish")
	if err == nil {
		t.Fatal("a publish whose generation record could not be written reported success")
	}
	if !strings.Contains(err.Error(), "recording the published project layer's generation") {
		t.Errorf("the failure is not the one the write's aftermath met: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != managedLayer {
		t.Fatalf("the write did not land, so there is no late failure to record: the bucket holds %q", got)
	}
	if got := r.audits(t); len(got) != 1 || got[0].Verdict != managedmode.AuditAdmitted {
		t.Fatalf("the applied step left %v, want one admission record (D1014)", got)
	}
}

// The rigor floor: the one change the gate refuses for admitting LESS. A mode a
// run is held to is not a session's to weaken (D20c, D1026) — the project's
// floor lowered or dropped, and a rigor rule lowered or dropped, are all
// refused, while a raise and a rule that was not there before are admitted.
func TestConfigPublishManagedRefusesALoweredRigorFloor(t *testing.T) {
	// A layer with the block the comparison is about: the default floor, and a
	// rigor rule for db when areaMode is not empty.
	rigorBlock := func(defaultMode, areaMode string) string {
		out := minimalAnchored + "defaults:\n  rigor: { default: " + defaultMode + " }\n"
		if areaMode != "" {
			out = minimalAnchored + "defaults:\n  rigor:\n    default: " + defaultMode +
				"\n    areas:\n      - { name: db, match: \"db/*\", mode: " + areaMode + " }\n"
		}
		return out
	}
	cases := []struct {
		name      string
		published string
		next      string
		names     string
	}{
		{"a lowered default", rigorBlock("critical", ""), rigorBlock("startup", ""), "defaults.rigor.default"},
		{"a removed default", rigorBlock("critical", ""), managedLayer, "defaults.rigor.default"},
		{"a lowered rigor rule", rigorBlock("pro", "critical"), rigorBlock("pro", "poc"), "db"},
		{"a removed rigor rule", rigorBlock("pro", "critical"), rigorBlock("pro", ""), "db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			publishedLayer(t, r.cloudFixture, tc.published)
			_, _, err := execute(t, "config", "publish", layerFile(t, tc.next))
			if err == nil {
				t.Fatalf("%s was published in a coding agent's session", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) || !strings.Contains(err.Error(), "floor") {
				t.Errorf("the refusal does not name %s as the floor that refused: %v", tc.names, err)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != tc.published {
				t.Fatalf("the published layer moved: %q", got)
			}
			if n := len(r.auditKeys(t)); n != 1 {
				t.Errorf("the refusal left %d audit records, want one (D1025)", n)
			}
		})
	}

	// The other side: a raise, and a rule the published layer did not have.
	t.Run("a raised floor and a new rule", func(t *testing.T) {
		r := newManagedPublishRig(t)
		agentSession(t)
		publishedLayer(t, r.cloudFixture, rigorBlock("pro", "startup"))
		raised := minimalAnchored + "defaults:\n  rigor:\n    default: critical\n    areas:\n      - { name: db, match: \"db/*\", mode: critical }\n      - { name: web, match: \"web/*\", mode: pro }\n"
		out, errOut, err := execute(t, "config", "publish", layerFile(t, raised))
		if err != nil {
			t.Fatalf("a layer raising every floor was refused: %v\n%s", err, errOut)
		}
		if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
			t.Errorf("the session does not read that the step proceeded: %q", out)
		}
		if got := bucketText(t, r.cloudFixture, config.LayerKey); got != raised {
			t.Fatalf("the bucket holds %q, want the raised layer", got)
		}
		if got := r.audits(t); len(got) != 1 || got[0].Verdict != managedmode.AuditAdmitted {
			t.Errorf("the record is %v, want one admission", got)
		}
	})
}

// The published object is there and is not a layer this build can read: the
// caps, claims and floors this write replaces cannot be compared with it, so the
// document is refused rather than measured against nothing. Every rule of that
// comparison would have counted its whole content as new — a document lowering a
// floor the unreadable object held would pass — and the admission's record would
// have named the rigor rule among the rules that passed though none ran (D1014).
//
// The object is not echoed: bytes nobody validated may hold a credential a hand
// edit put there, which is publishLayer's own reason for printing the count and
// nothing else.
func TestConfigPublishManagedRefusesOverAnUnreadablePublishedLayer(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	unreadable := minimalAnchored + "this_key_does_not_exist: 1\n"
	publishedLayer(t, r.cloudFixture, unreadable)
	lowering := minimalAnchored + "defaults:\n  rigor: { default: startup }\n"
	_, _, err := execute(t, "config", "publish", layerFile(t, lowering))
	if err == nil {
		t.Fatal("a layer lowering a floor was admitted over a published layer no rule could read")
	}
	if !strings.Contains(err.Error(), "not a layer this program can read") {
		t.Errorf("the refusal does not name the predecessor it could not judge against: %v", err)
	}
	if strings.Contains(err.Error(), "this_key_does_not_exist") {
		t.Errorf("the refusal echoes the untrusted object it could not read: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != unreadable {
		t.Fatalf("the published layer moved: %q", got)
	}
	rec := r.audits(t)
	if len(rec) != 1 || rec[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the record is %v, want one refusal", rec)
	}
	if rules := strings.Join(rec[0].Rules, " "); strings.Contains(rules, "lowers no rigor floor") {
		t.Errorf("the record claims the rigor rule passed although no comparison was made: %q", rules)
	}
}

// The write is conditioned on the read the gate judged the document against and
// on no other: a publisher that lands in that window is refused as a conflict.
// A second read taken after it — what publishLayer does with no layerAt — would
// have found the racer's layer, measured the document against a layer no rule
// of this gate saw, and written over it.
func TestConfigPublishManagedRefusesAWriteAgainstAChangedObject(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	other := minimalAnchored + "defaults:\n  git: { provider: github }\n"
	race := configPublishRace
	t.Cleanup(func() { configPublishRace = race })
	configPublishRace = func(context.Context, *blobx.Bucket) {
		writeBucketFile(t, r.cloudFixture, config.LayerKey, other)
	}
	_, _, err := execute(t, "config", "publish", layerFile(t, managedLayer))
	if err == nil {
		t.Fatal("a layer was written over an object that changed after the gate judged it")
	}
	if !strings.Contains(err.Error(), "another publisher changed") {
		t.Errorf("the refusal does not name the conflict the write met: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != other {
		t.Fatalf("the racer's layer was overwritten: the bucket holds %q", got)
	}
}

// A document the key walk cannot follow — an anchor, and the alias that copies
// it — is refused by a rule of the gate's own and not by the parse alone: a
// refusal that read as a document whose every key rule passed would leave an
// anchor-based attempt at a reserved key looking like a clean one in the record
// (D1014).
func TestConfigPublishManagedRefusesADocumentTheWalkCannotRead(t *testing.T) {
	r := newManagedPublishRig(t)
	agentSession(t)
	publishedLayer(t, r.cloudFixture, "")
	anchored := minimalAnchored + "defaults: &shared\n  agent: { auth: vertex }\nshared_defaults: *shared\n"
	_, _, err := execute(t, "config", "publish", layerFile(t, anchored))
	if err == nil {
		t.Fatal("a layer whose keys the gate cannot walk was published in a coding agent's session")
	}
	const rule = "cannot be read as a document this gate judges"
	if !strings.Contains(err.Error(), rule) {
		t.Errorf("the refusal does not name the gate's own rule for a document it cannot walk: %v", err)
	}
	if strings.Contains(err.Error(), "your own terminal") {
		t.Errorf("the marker's own refusal answered instead of the gate's: %v", err)
	}
	if got := bucketText(t, r.cloudFixture, config.LayerKey); got != "" {
		t.Fatalf("a document the gate could not read reached the bucket: %q", got)
	}
	rec := r.audits(t)
	if len(rec) != 1 || rec[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal's record is %v, want one refusal", rec)
	}
	if rules := strings.Join(rec[0].Rules, " "); !strings.Contains(rules, rule) {
		t.Errorf("the record does not name the rule that refused: %q", rules)
	}
}

// The rest of the widening rule (spec §4.1.5, §4.1.6): a model claim the
// published layer did not serve, an A/B challenger it did not run, and the
// sampling rate and the daily pair count raised. Each is refused on its own, so
// the cap the test above covers is not the only comparison the gate makes, and
// each refusal names the key that widened rather than the block it sits in.
func TestConfigPublishManagedRefusesEachWidening(t *testing.T) {
	const (
		claim = "claude_models:\n  openrouter: [\"anthropic/claude-sonnet-5.5\"]\n"
		v3    = "deepseek/deepseek-v3"
		k2    = "moonshot/kimi-k2"
	)
	abBlock := func(rate string, pairs string, challengers ...string) string {
		return "ab: { rate: " + rate + ", max_pairs_per_day: " + pairs + ", challengers: [" + strings.Join(challengers, ", ") + "] }\n"
	}
	base := abBlock("0.02", "2", v3)
	// A claim is judged against the installation config's route table, so the
	// route it names has to exist before the claim can be the thing that
	// refuses: the provider the config does not define is that test's own
	// subject and not this one's.
	const route = "  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"
	cases := []struct {
		name      string
		routes    string
		published string
		next      string
		names     string
	}{
		{"a model claim the project did not serve", route, minimalAnchored, minimalAnchored + claim, "claude_models"},
		{"an A/B challenger the project did not run", "", minimalAnchored + base, minimalAnchored + abBlock("0.02", "2", v3, k2), "challenger"},
		{"a raised sampling rate", "", minimalAnchored + base, minimalAnchored + abBlock("0.05", "2", v3), "ab.rate"},
		{"a raised daily pair count", "", minimalAnchored + base, minimalAnchored + abBlock("0.02", "9", v3), "max_pairs_per_day"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			if tc.routes != "" {
				r.appendConfig(t, "providers:\n"+tc.routes)
			}
			publishedLayer(t, r.cloudFixture, tc.published)
			_, _, err := execute(t, "config", "publish", layerFile(t, tc.next))
			if err == nil {
				t.Fatalf("%s was published in a coding agent's session", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) || !strings.Contains(err.Error(), "widening") {
				t.Errorf("the refusal does not name %s as the widening that refused: %v", tc.names, err)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != tc.published {
				t.Fatalf("the published layer moved: %q", got)
			}
			if n := len(r.auditKeys(t)); n != 1 {
				t.Errorf("the refusal left %d audit records, want one (D1025)", n)
			}
		})
	}

	// The other side of all four at once: the claim and the challenger dropped,
	// the rate and the daily count lowered. Nothing there states a rule about a
	// key being present — each of the five is a comparison of two layers.
	t.Run("narrowing every lever at once", func(t *testing.T) {
		r := newManagedPublishRig(t)
		agentSession(t)
		publishedLayer(t, r.cloudFixture, minimalAnchored+claim+abBlock("0.05", "9", v3, k2))
		narrow := minimalAnchored + abBlock("0.01", "1", v3)
		out, errOut, err := execute(t, "config", "publish", layerFile(t, narrow))
		if err != nil {
			t.Fatalf("a layer narrowing every lever was refused: %v\n%s", err, errOut)
		}
		if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
			t.Errorf("the session does not read that the step proceeded: %q", out)
		}
		if got := bucketText(t, r.cloudFixture, config.LayerKey); got != narrow {
			t.Fatalf("the bucket holds %q, want the narrowing layer", got)
		}
	})
}

// roundsLayer is the layer a round-count case publishes: one of the round keys
// D1027's floors are about, with the value the case names, and nothing else.
// An empty value publishes no round count at all, which is the removal case.
func roundsLayer(key, value string) string {
	if value == "" {
		return minimalAnchored
	}
	return minimalAnchored + "defaults:\n  agent: { " + key + ": " + value + " }\n"
}

// levelOffLayer is the layer a review-level case publishes: one of the three
// levels turned off with the one key that says so (`none: true`).
func levelOffLayer(name string) string {
	return minimalAnchored + "defaults:\n  agent:\n    levels:\n      " + name + ": { none: true }\n"
}

// firstLineReviewLayer is the layer a first-line-review case publishes:
// `defaults.agent.first_line_review` with the value the case names, and nothing
// else. An empty value publishes no value at all, which is the removal case.
func firstLineReviewLayer(value string) string {
	if value == "" {
		return minimalAnchored
	}
	return minimalAnchored + "defaults:\n  agent: { first_line_review: " + value + " }\n"
}

// The floors D1027 holds a coding agent's session to are judged at this door
// too. `fugaro config publish` writes the same published layer `fugaro settings
// set` writes one key of, so a session that has the whole document in hand
// could otherwise write a lower round count — or take one away — around the
// direction rule the settings door applies to those two keys.
func TestConfigPublishManagedRefusesALoweredReviewRound(t *testing.T) {
	cases := []struct {
		name      string
		published string
		next      string
		names     string
	}{
		{"a lowered review_rounds", roundsLayer("review_rounds", "5"), roundsLayer("review_rounds", "3"), "review_rounds"},
		{"a removed review_rounds", roundsLayer("review_rounds", "5"), roundsLayer("review_rounds", ""), "review_rounds"},
		{"a lowered adversarial_rounds", roundsLayer("adversarial_rounds", "4"), roundsLayer("adversarial_rounds", "2"), "adversarial_rounds"},
		{"a removed adversarial_rounds", roundsLayer("adversarial_rounds", "4"), roundsLayer("adversarial_rounds", ""), "adversarial_rounds"},
		{"a lowered first_line_rounds", roundsLayer("first_line_rounds", "3"), roundsLayer("first_line_rounds", "1"), "first_line_rounds"},
		{"a removed first_line_rounds", roundsLayer("first_line_rounds", "2"), roundsLayer("first_line_rounds", ""), "first_line_rounds"},
		// The layer the write replaces sets no number of its own: the base is
		// the strictest mode's row, which is settingsFloorBase's own residual
		// (D1027), and a number below it is refused here as it is there.
		{"a number below the strictest mode's row", minimalAnchored, roundsLayer("review_rounds", "2"), "review_rounds"},
		// The level-1 count is the other loop of the same row: a mode's
		// first-line rounds (rigor.Rounds.Inner) are not its senior ones, so
		// the base this key is judged against is the first-line row and not
		// the row the two counts above are held to.
		{"a first-line count below the first-line row", minimalAnchored, roundsLayer("first_line_rounds", "2"), "first_line_rounds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			publishedLayer(t, r.cloudFixture, tc.published)
			_, _, err := execute(t, "config", "publish", layerFile(t, tc.next))
			if err == nil {
				t.Fatalf("%s was published in a coding agent's session", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) || !strings.Contains(err.Error(), "floor") {
				t.Errorf("the refusal does not name %s as the floor that refused: %v", tc.names, err)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != tc.published {
				t.Fatalf("the published layer moved: %q", got)
			}
			rec := r.audits(t)
			if len(rec) != 1 || rec[0].Verdict != managedmode.AuditRefused {
				t.Fatalf("the record is %v, want one refusal", rec)
			}
			if rules := strings.Join(rec[0].Rules, " "); !strings.Contains(rules, tc.names) || !strings.Contains(rules, "floor") {
				t.Errorf("the record does not name the rule that refused: %q", rules)
			}
		})
	}
}

// Any of the three review levels turned off: a level that does not run spends
// no review at all, which is the other direction D1027 refuses for a session
// (the settings allowlist names `layer.defaults.agent.levels.*.none` as a key
// no session writes, and the level counts as a whole block here).
func TestConfigPublishManagedRefusesAReviewLevelTurnedOff(t *testing.T) {
	for _, name := range []string{"l1", "l2", "l3"} {
		t.Run(name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			publishedLayer(t, r.cloudFixture, minimalAnchored)
			_, _, err := execute(t, "config", "publish", layerFile(t, levelOffLayer(name)))
			if err == nil {
				t.Fatalf("turning review level %s off was published in a coding agent's session", name)
			}
			if !strings.Contains(err.Error(), "levels."+name) || !strings.Contains(err.Error(), "floor") {
				t.Errorf("the refusal does not name the level and the rule that refused it: %v", err)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != minimalAnchored {
				t.Fatalf("the published layer moved: %q", got)
			}
			rec := r.audits(t)
			if len(rec) != 1 || rec[0].Verdict != managedmode.AuditRefused {
				t.Fatalf("the record is %v, want one refusal", rec)
			}
			if rules := strings.Join(rec[0].Rules, " "); !strings.Contains(rules, "levels."+name) {
				t.Errorf("the record does not name the rule that refused: %q", rules)
			}
		})
	}

	// The other sides: a level already off stays off, and one turned back on
	// (the `none: true` removed) is a raise, which the rule admits.
	t.Run("a level already off", func(t *testing.T) {
		r := newManagedPublishRig(t)
		agentSession(t)
		publishedLayer(t, r.cloudFixture, levelOffLayer("l2"))
		out, errOut, err := execute(t, "config", "publish", layerFile(t, levelOffLayer("l2")))
		if err != nil {
			t.Fatalf("an unchanged layer was refused: %v\n%s", err, errOut)
		}
		if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
			t.Errorf("the session does not read that the step proceeded: %q", out)
		}
	})
	t.Run("a level turned back on", func(t *testing.T) {
		r := newManagedPublishRig(t)
		agentSession(t)
		publishedLayer(t, r.cloudFixture, levelOffLayer("l2"))
		if _, errOut, err := execute(t, "config", "publish", layerFile(t, minimalAnchored)); err != nil {
			t.Fatalf("a layer turning a review level back on was refused: %v\n%s", err, errOut)
		}
		if got := bucketText(t, r.cloudFixture, config.LayerKey); got != minimalAnchored {
			t.Fatalf("the bucket holds %q, want the layer with the level back on", got)
		}
	})
}

// Review level 1 is turned off by two keys, and the second one is not in the
// levels block: `defaults.agent.first_line_review: off` is the first-line
// step's own switch, and the settings door refuses it exactly as it refuses
// `defaults.agent.levels.*.none`. Without this the whole-document door would
// still let a session take level 1 away, one key over from the block it does
// judge.
func TestConfigPublishManagedRefusesTheFirstLineReviewTurnedOff(t *testing.T) {
	cases := []struct {
		name      string
		published string
		next      string
	}{
		{"off where the layer said auto", firstLineReviewLayer("auto"), firstLineReviewLayer("off")},
		{"off where the layer said on", firstLineReviewLayer("on"), firstLineReviewLayer("off")},
		// The layer the write replaces says nothing, and the reader's own
		// default is `auto` (config's defaults): level 1 may run today, so
		// turning it off takes review away.
		{"off where the layer said nothing", minimalAnchored, firstLineReviewLayer("off")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			publishedLayer(t, r.cloudFixture, tc.published)
			_, _, err := execute(t, "config", "publish", layerFile(t, tc.next))
			if err == nil {
				t.Fatalf("turning the first-line review off was published in a coding agent's session")
			}
			if !strings.Contains(err.Error(), "first_line_review") || !strings.Contains(err.Error(), "floor") {
				t.Errorf("the refusal does not name the key and the rule that refused it: %v", err)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != tc.published {
				t.Fatalf("the published layer moved: %q", got)
			}
			rec := r.audits(t)
			if len(rec) != 1 || rec[0].Verdict != managedmode.AuditRefused {
				t.Fatalf("the record is %v, want one refusal", rec)
			}
			if rules := strings.Join(rec[0].Rules, " "); !strings.Contains(rules, "first_line_review") {
				t.Errorf("the record does not name the rule that refused: %q", rules)
			}
		})
	}

	// The other sides. A layer that already says off changes nothing, and
	// every value that is not off lets level 1 run again — `on` outright, and
	// `auto` (or no value at all, which reads as auto) where the run's models
	// and providers decide — so the rule admits them, as it admits a level
	// turned back on in the levels block.
	t.Run("a layer that already says off", func(t *testing.T) {
		r := newManagedPublishRig(t)
		agentSession(t)
		publishedLayer(t, r.cloudFixture, firstLineReviewLayer("off"))
		out, errOut, err := execute(t, "config", "publish", layerFile(t, firstLineReviewLayer("off")))
		if err != nil {
			t.Fatalf("an unchanged layer was refused: %v\n%s", err, errOut)
		}
		if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
			t.Errorf("the session does not read that the step proceeded: %q", out)
		}
	})
	for _, tc := range []struct{ name, published, next string }{
		{"turned back on", firstLineReviewLayer("off"), firstLineReviewLayer("on")},
		{"turned back to auto", firstLineReviewLayer("off"), firstLineReviewLayer("auto")},
		{"the value removed", firstLineReviewLayer("off"), minimalAnchored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			publishedLayer(t, r.cloudFixture, tc.published)
			if _, errOut, err := execute(t, "config", "publish", layerFile(t, tc.next)); err != nil {
				t.Fatalf("a layer letting the first-line review run again was refused: %v\n%s", err, errOut)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != tc.next {
				t.Fatalf("the bucket holds %q, want the published layer", got)
			}
		})
	}
}

// The other direction of the same rule: raising a round count, and publishing
// a layer that changes nothing about it, are both admitted — and the record of
// an admission names the rule among the ones that were applied and passed.
func TestConfigPublishManagedAdmitsARaisedReviewRound(t *testing.T) {
	cases := []struct {
		name      string
		published string
		next      string
	}{
		{"a raised review_rounds", roundsLayer("review_rounds", "3"), roundsLayer("review_rounds", "5")},
		{"a raised adversarial_rounds", roundsLayer("adversarial_rounds", "2"), roundsLayer("adversarial_rounds", "6")},
		{"an unchanged review_rounds", roundsLayer("review_rounds", "3"), roundsLayer("review_rounds", "3")},
		{"a number the layer never set, at the strictest mode's row", minimalAnchored, roundsLayer("review_rounds", "5")},
		{"a raised first_line_rounds", roundsLayer("first_line_rounds", "1"), roundsLayer("first_line_rounds", "2")},
		{"a first-line count the layer never set, at its own row", minimalAnchored, roundsLayer("first_line_rounds", "3")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			agentSession(t)
			publishedLayer(t, r.cloudFixture, tc.published)
			out, errOut, err := execute(t, "config", "publish", layerFile(t, tc.next))
			if err != nil {
				t.Fatalf("%s was refused: %v\n%s", tc.name, err, errOut)
			}
			if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
				t.Errorf("the session does not read that the step proceeded: %q", out)
			}
			if got := bucketText(t, r.cloudFixture, config.LayerKey); got != tc.next {
				t.Fatalf("the bucket holds %q, want the published layer", got)
			}
			rec := r.audits(t)
			if len(rec) != 1 || rec[0].Verdict != managedmode.AuditAdmitted {
				t.Fatalf("the record is %v, want one admission", rec)
			}
			if rules := strings.Join(rec[0].Rules, " "); !strings.Contains(rules, "lowers no review round") {
				t.Errorf("the admission's record does not name the rounds rule among the ones that passed: %q", rules)
			}
		})
	}
}

// The owner's ruling of 2026-11-06: the allowlist and the floors bind a coding
// agent's session, and a person at their own terminal may lower them. The
// verdict is asked for directly here — a person's own publish never reads the
// gate at all (config_publish.go's marker), so the command is not the surface
// this half is observable through — and it is asked with the empty marker the
// settings door reads for a person.
func TestConfigPublishManagedAdmitsAPersonsLoweredReviewRound(t *testing.T) {
	cases := []struct {
		name      string
		published string
		next      string
	}{
		{"a lowered review_rounds", roundsLayer("review_rounds", "5"), roundsLayer("review_rounds", "2")},
		{"a removed adversarial_rounds", roundsLayer("adversarial_rounds", "4"), roundsLayer("adversarial_rounds", "")},
		{"a lowered first_line_rounds", roundsLayer("first_line_rounds", "3"), roundsLayer("first_line_rounds", "1")},
		{"a review level turned off", minimalAnchored, levelOffLayer("l2")},
		{"the first-line review turned off", minimalAnchored, firstLineReviewLayer("off")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedPublishRig(t)
			noAgentSession(t)
			publishedLayer(t, r.cloudFixture, tc.published)
			env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
			if err != nil {
				t.Fatalf("opening the fixture's cloud environment: %v", err)
			}
			defer env.Close()
			conv, _ := configPublishConverge(context.Background(), env, []byte(tc.next), false, "")
			if err := conv.Refuse(); err != nil {
				t.Fatalf("a person's own publish of a weakened layer was refused by the floor rule: %v", err)
			}
			rules := strings.Join(conv.Rules(), " ")
			if strings.Contains(rules, "review round") || strings.Contains(rules, "level") {
				t.Errorf("the record claims the direction rule ran for a person's write: %q", rules)
			}
			if !strings.Contains(rules, "widens no model claim") {
				t.Errorf("the record does not name the rules that did run: %q", rules)
			}
		})
	}
}
