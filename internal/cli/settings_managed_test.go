package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// settings_managed.go's tests: the settings handoff row of the managed plan's
// §2, read the way the two commands run it — through the command, in a coding
// agent's session, against the fakes and one file:// bucket. Nothing here
// contacts a cloud, and the credentials are read off a path in the test's own
// directory, so a developer's own account can never decide what a record says.
//
// What these hold, and why each is a test rather than a comment. The row's
// whole content is a NEGATIVE: the registry-key write an agent's session may
// make, and the things it may not — a reserved key, the typed widening class,
// a layer file written from another machine's generation, and any member of the
// document no rule of the gate reads. Every one of those is a failure to act,
// so a build with the gate missing does exactly what it did before this row
// landed (it refuses at the fence, writing nothing), and a test that only
// asserted "the refusal is unchanged" would pass on that build. The red step is
// therefore the positive half: the key IS written and its admission IS recorded
// — which no build without this row can do.
//
// The rig is M2's own: the cloud fixture plus the two marks, the installation's
// GCP project carrying the label and its runs bucket holding the creation fact.

// settingsManagedLayerKey and settingsManagedLayerValue are the registry key
// these tests write: a project-layer row, owned by `settings set`, carrying no
// cap and no widening direction, so it is the ordinary confirmation — which is
// what makes it a key the gate may admit at all.
const (
	settingsManagedLayerKey   = "layer.defaults.agent.review_rounds"
	settingsManagedLayerValue = "4"
)

// settingsManagedMarks is which of the two marks a rig's project carries. The
// ordinary installation carries neither, and it is the world every refusal this
// row must NOT change is tested in.
type settingsManagedMarks int

const (
	settingsMarksNone       settingsManagedMarks = iota // neither mark: the ordinary installation
	settingsMarksManaged                                // both marks: the only world the gate admits in
	settingsMarksUnlabelled                             // the creation fact without the label: half of it
)

// settingsManagedRig is one installation for these tests: the cloud fixture's
// config (its own project, its own bucket_url), the fakes the two marks live in
// and the storage client the tests read audit records back with.
type settingsManagedRig struct {
	*cloudFixture
	gcs *gcpfake.GCS
	crm *gcpfake.CRM
	s   *storage.Service
}

func newSettingsManagedRig(t *testing.T, marks settingsManagedMarks) *settingsManagedRig {
	t.Helper()
	noAgentSession(t)
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
	crm := gcpfake.NewCRM(t)
	crm.AddProject(managedPublishGCP, managedPublishNumber)
	if marks != settingsMarksNone {
		gcs.AddBucket(managedPublishBucket(), managedPublishNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
		gcs.Put(managedPublishBucket(), managed.MarkerObject, managedPublishMarker())
	}
	if marks == settingsMarksManaged {
		labelled(crm, managedPublishGCP)
	}
	f := newCloudFixture(t, "storage: "+gcs.URL+"/storage/v1/", "resource_manager: "+crm.URL+"/")
	return &settingsManagedRig{cloudFixture: f, gcs: gcs, crm: crm, s: managedStorage(t, gcs)}
}

// managedRunsBucket gives the rig's installation the default runs bucket name
// (fugaro-runs-proj-1234), which the fixture's own config does not carry. The
// mark read looks in the bucket that name derives, while an audit record is
// written to the name the config carries — in an installation doctor accepts
// the two are one name, and this is the helper that makes the rig one of those.
// A test that does not read a record back does not need it.
func (r *settingsManagedRig) managedRunsBucket(t *testing.T) {
	t.Helper()
	projectRecipesFixture(t, r.cloudFixture, nil)
}

// audits is the day's records, read back through the reader a surface uses.
func (r *settingsManagedRig) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), r.s, managedPublishBucket(), managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// managedLocal is the selected project's config as the command reads it: the
// rig's own file, whose GCP project is the one the marks are read about.
func (r *settingsManagedRig) managedLocal(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, _, err := localcfg.LoadProject(os.Getenv, "aurora")
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// settingsManagedLayerFile writes this machine's own project layer file — the
// document `--layer project` edits — with text, and answers its path.
func settingsManagedLayerFile(t *testing.T, text string) string {
	t.Helper()
	path := localLayerPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// settingsManagedLayerTarget is the target settingsResolveTarget builds for the
// project layer file: what the direct calls below need, and no more (the
// command resolves its own).
func settingsManagedLayerTarget(path string) settingsTarget {
	return settingsTarget{
		layer: settingsLayerProject, kind: settingsDocLayer, path: path,
		keyPath: []string{"defaults", "agent", "review_rounds"}, project: "aurora",
	}
}

// The red step: in a managed project's coding agent session, a registry key is
// written into this machine's layer file and its admission is recorded, where
// every build before this row refused to write the file at all. The record
// names the document that was applied — the same bytes, provably — and the
// rules that were judged and passed.
func TestSettingsSetManagedAdmitsARegistryKeyInAnAgentSession(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("a registry key was refused in a managed project's coding agent session: %v\nstdout:\n%s", err, out)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "review_rounds: 4") {
		t.Fatalf("the layer file does not carry the write:\n%s", after)
	}

	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("%d gate records, want the one admission of this write: %+v", len(recs), recs)
	}
	rec := recs[0]
	if !strings.HasSuffix(rec.Command, " settings set") {
		t.Errorf("the record names %q, want the command a person types", rec.Command)
	}
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q: the step was applied", rec.Verdict, managedmode.AuditAdmitted)
	}
	if want := gateDigest([]byte(after)); rec.PlanDigest != want {
		t.Errorf("the record's plan_digest is %q and the bytes written hash to %q: the record does not name the document that was applied", rec.PlanDigest, want)
	}
	if rec.AgentMarker != "CLAUDECODE" {
		t.Errorf("the record's agent_marker is %q, want the session's own marker", rec.AgentMarker)
	}
	if rec.Identity != "user:"+managedGateUser {
		t.Errorf("the record names identity %q, want the credentials in force", rec.Identity)
	}
	if rec.MarkerVersion != 1 {
		t.Errorf("the record names creation fact version %d, want the one in force", rec.MarkerVersion)
	}
	for _, want := range []string{"settings registry", "layer this write lands in", "no reserved key", "typed confirmation reserves", "written from the published layer's generation", "no rule of this gate judges is unchanged"} {
		if !slices.ContainsFunc(rec.Rules, func(rule string) bool { return strings.Contains(rule, want) }) {
			t.Errorf("the record's rules (%v) do not name the rule %q: the record is what an owner reads to see what was judged", rec.Rules, want)
		}
	}
}

// `settings unset` is the same write path and the same row: it removes the key
// from one layer rather than naming a value, and in a managed project's coding
// agent session the removal of a registry key lands and is recorded. The
// document the gate compares is the one the removal leaves — the judged key
// masked out of both sides, and the block the removal emptied taken with it,
// which is why removing the only key under `defaults.agent` is not a change to
// a member no rule reads.
func TestSettingsUnsetManagedAdmitsARegistryKeyInAnAgentSession(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored+"defaults:\n  agent: { review_rounds: 4 }\n")
	agentSession(t)

	out, _, err := execute(t, "settings", "unset", settingsManagedLayerKey, "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("a registry key was refused in a managed project's coding agent session: %v\nstdout:\n%s", err, out)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "review_rounds") {
		t.Fatalf("the layer file still carries the key:\n%s", after)
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
		t.Fatalf("the removal left %+v, want exactly one admitted record", recs)
	}
	if recs[0].PlanDigest != gateDigest([]byte(after)) {
		t.Errorf("the record's digest is %q, want the sha256 of the document that was applied", recs[0].PlanDigest)
	}
	if !strings.HasSuffix(recs[0].Command, " settings unset") {
		t.Errorf("the record names %q, want the command a person types", recs[0].Command)
	}
}

// And the same command over a reserved key is refused by the gate before
// anything is written, exactly as `set` is: the two commands are one row and
// one rule, and a gate that held only the writing direction would be a gate a
// session steps around by removing the key instead.
func TestSettingsUnsetManagedRefusesAReservedKeyByTheGate(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored+"governance:\n  max_subagents: 4\n")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "unset", "layer.governance.max_subagents", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a reserved key was removed in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the reserved key was not refused by the gate: %v", err)
	}
	if !strings.Contains(err.Error(), "governance.max_subagents") || strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("the refusal is not the gate's over the reserved run: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused removal")
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
}

// The reserved keys: a key no coding agent's session writes whatever the
// registry says about it is refused BY THE GATE, before anything is written,
// and the refusal is recorded like every other (D1025). The session reads the
// rule that fired and not the marker's own sentence.
func TestSettingsSetManagedRefusesAReservedKeyByTheGate(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored)
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "layer.governance.max_subagents", "4", "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a reserved key was written in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the reserved key was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if !strings.Contains(err.Error(), "governance.max_subagents") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the refusal does not name the reserved run that refused: %v", err)
	}
	if strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("the marker's own refusal answered instead of the gate's: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused key")
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
	if !slices.ContainsFunc(recs[0].Rules, func(rule string) bool { return strings.Contains(rule, "governance.max_subagents") }) {
		t.Errorf("the refused record's rules (%v) do not name what refused", recs[0].Rules)
	}
}

// The typed widening class (D1026): a direction the typed confirmation reserves
// for the project's own hand is never admitted by a plan gate, so a session
// that asks for one reads the rule it moved rather than "run this yourself".
// Raising a repository's own spend ceiling is such a direction, and it lands in
// the repository's file — the document a session may otherwise edit — which is
// what makes it the case only the gate can hold.
func TestSettingsSetManagedRefusesATypedWideningByTheGate(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("agent:\n  max_budget_usd: 10\n"))
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "agent.max_budget_usd", "500", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatalf("a widening was written in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the widening was not refused by the gate: %v", err)
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not say which class of step refused it: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused widening")
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
}

// The entry operation (G1): adding a workflow's secret hands a run a credential
// it did not reach before, so the operation is the typed direction on that
// block and the gate refuses it. The block is addressed by the command line and
// the document the edit rewrites is judged whole.
func TestSettingsSetManagedRefusesASecretsEntryByTheGate(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsWriteCheckout(t, settingsRepoWithEntries)
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "workflows.web.secrets", "--add-entry", "name=ci-token", "--add-entry", "env=CI_TOKEN", "--layer", "repo", "--yes")
	if err == nil {
		t.Fatalf("a secrets entry was written in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the entry operation was not refused by the gate: %v", err)
	}
	if !strings.Contains(err.Error(), "hands a run a credential") || !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the rule the entry moved: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused entry operation")
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
}

// The D854 generation guard: this machine's layer file was written from another
// machine's published layer, so writing it would replace a layer this machine
// cannot account for. The refusal is config publish's own sentence, so the two
// doors refuse a stale copy in the same words.
func TestSettingsSetManagedRefusesAStaleLayerByTheGate(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	publishedLayer(t, r.cloudFixture, minimalAnchored)
	path := settingsManagedLayerFile(t, managedLayer)
	setConfigScalar(t, "layer_generation", `"7"`)
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("a stale layer file was written in a coding agent's session:\n%s", out)
	}
	if !strings.Contains(err.Error(), layerLocalStale) {
		t.Errorf("the refusal does not name the stale copy D854 is about: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the stale layer file was written")
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
}

// The other half of D854: a layer file whose recorded generation IS the
// published object's is one this machine wrote from the published layer, and a
// local edit on top of it is exactly what a settings write is — so the write
// the test above refuses lands here. Without this one, a rule that refused
// every local layer file would pass the test above.
func TestSettingsSetManagedAdmitsALayerWrittenFromThePublishedGeneration(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	publishedLayer(t, r.cloudFixture, managedLayer)
	path := settingsManagedLayerFile(t, minimalAnchored)
	// Off GCS every object's generation is the zero blobx documents, so a file
	// recorded from generation 0 is a file this machine wrote from the
	// published object — the state an owner's own edit starts from.
	setConfigScalar(t, "layer_generation", `"0"`)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("this machine's own layer file was refused: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "review_rounds: 4") {
		t.Errorf("the write did not land:\n%s", readFileString(t, path))
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
		t.Fatalf("the write left %+v, want exactly one admitted record", recs)
	}
}

// The generic fail-closed rule, and its mutation proof: a document whose OTHER
// members changed is refused, and the same document with only the judged key
// changed is admitted. The two differ in one member outside the key the rules
// read, which is exactly the change a gate that judged only what somebody
// remembered to look at would let through.
func TestSettingsManagedVerdictRefusesAnUnjudgedMember(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	g := &managedGate{env: managedGateEnv{lc: r.managedLocal(t)}}
	target := settingsManagedLayerTarget(settingsManagedLayerFile(t, minimalAnchored))
	old := []byte(minimalAnchored)
	judged := []byte(minimalAnchored + "defaults:\n  agent: { review_rounds: 4 }\n")
	elsewhere := []byte(minimalAnchored + "defaults:\n  agent: { review_rounds: 4, auth: vertex }\n")

	if err := settingsManagedVerdict(context.Background(), g, target, settingsManagedLayerKey, false, "", old, judged).Refuse(); err != nil {
		t.Fatalf("the judged key alone was refused: %v", err)
	}
	err := settingsManagedVerdict(context.Background(), g, target, settingsManagedLayerKey, false, "", old, elsewhere).Refuse()
	if err == nil {
		t.Fatal("a member no rule of this gate reads changed and the verdict admitted it")
	}
	if !strings.Contains(err.Error(), "no rule of this gate judges is unchanged") {
		t.Errorf("the refusal is not the fail-closed rule over the unjudged members: %v", err)
	}
}

// The record follows the write and never precedes it: the gate admits the
// document, files nothing at all while the command has written nothing, and
// files the one admission when — and only when — the command asks it to, after
// its own write (D1014, spec §6, M6's helper). One admission is one record
// however many times the caller asks.
func TestSettingsManagedFilesTheAdmissionOnlyAfterTheWrite(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	agentSession(t)
	target := settingsManagedLayerTarget(settingsManagedLayerFile(t, minimalAnchored))
	old := []byte(minimalAnchored)
	doc := []byte(minimalAnchored + "defaults:\n  agent: { review_rounds: 4 }\n")

	m, gate, err := settingsManagedStep(context.Background(), r.managedLocal(t), target, settingsManagedLayerKey, false, "", old, doc, "settings set")
	if err != nil {
		t.Fatalf("a registry key was refused: %v", err)
	}
	if !m.Managed || !m.Admitted || m.Fallback {
		t.Fatalf("the gate answered %+v, want an admitted document in a managed project", m)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Fatalf("the admission was filed before the command wrote anything: %+v", recs)
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatalf("recording the admission: %v", err)
	}
	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("after the write the day holds %d records, want exactly one", len(recs))
	}
	if recs[0].PlanDigest != gateDigest(doc) {
		t.Errorf("the record's digest is %q, want the sha256 of the document the gate judged", recs[0].PlanDigest)
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.audits(t)); n != 1 {
		t.Errorf("the day holds %d records after the admission was recorded twice, want 1", n)
	}
}

// A command that applied nothing records nothing: the key already reads what
// was asked for, so the command returns before the gate is ever asked — and a
// record saying "admitted" about a write that never happened is worse than no
// record at all (D1014).
func TestSettingsSetManagedRecordsNothingWhenThereIsNoWrite(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored+"defaults:\n  agent: { review_rounds: 4 }\n")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("a no-op re-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already reads what was asked for") {
		t.Errorf("the command did not report the no-op:\n%s", out)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed on a run that had nothing to write")
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("the command filed %+v for a write it never made", recs)
	}
}

// An installation that is not managed keeps today's refusal exactly: the file
// is not written, no record is filed — D1025 writes down nothing about a
// project that is not managed — and the sentence is the one this command has
// always used.
func TestSettingsSetUnmanagedKeepsTheRefusalInAnAgentSession(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksNone)
	path := settingsManagedLayerFile(t, minimalAnchored)
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
	if err == nil {
		t.Fatalf("the command wrote in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "refusing to write") || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal is not this command's own: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed in a coding agent's session")
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("an unmanaged project filed %+v", recs)
	}
}

// A mark read that does not answer fails closed: one half of the fact is not
// the fact, and a creation fact that cannot be read is not a permission. The
// project is not one this program can prove is managed, so the command refuses
// as it always did and nothing is recorded. The wrong answer here would be the
// admitting one, and this is the test that makes it the refusing one.
func TestSettingsSetManagedUnreadMarksKeepTheRefusal(t *testing.T) {
	for name, rig := range map[string]func(t *testing.T) *settingsManagedRig{
		"the creation fact cannot be read": func(t *testing.T) *settingsManagedRig {
			r := newSettingsManagedRig(t, settingsMarksManaged)
			r.gcs.RefusePath(managed.MarkerObject, 500, "InternalServerError", "", "the creation fact did not answer")
			return r
		},
		"the project carries no label": func(t *testing.T) *settingsManagedRig {
			return newSettingsManagedRig(t, settingsMarksUnlabelled)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := rig(t)
			path := settingsManagedLayerFile(t, minimalAnchored)
			before := readFileString(t, path)
			agentSession(t)

			out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
			if err == nil {
				t.Fatalf("a project this program cannot prove is managed was admitted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), "refusing to write") || !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal is not this command's own: %v", err)
			}
			if readFileString(t, path) != before {
				t.Error("the file changed under a mark that could not be read")
			}
			if recs := r.audits(t); len(recs) != 0 {
				t.Errorf("a project that is not managed filed %+v", recs)
			}
		})
	}
}

// The other half of "the gate is consulted only in a coding agent's session":
// an owner at their own terminal pays no mark read at all, whatever the project
// is. The person's own command is not the one the gate stands in for, so a
// stale, unreadable or far-away mark cannot slow it down or stop it — and the
// run leaves no record, which is the half of D1025 an owner can check in their
// own bucket.
func TestSettingsSetOwnerTerminalReadsNoMark(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	path := settingsManagedLayerFile(t, minimalAnchored)
	target := settingsManagedLayerTarget(path)
	old := []byte(minimalAnchored)
	doc := []byte(minimalAnchored + "defaults:\n  agent: { review_rounds: 4 }\n")

	m, gate, err := settingsManagedStep(context.Background(), r.managedLocal(t), target, settingsManagedLayerKey, false, "", old, doc, "settings set")
	if err != nil {
		t.Fatalf("a person's own terminal was refused by the gate: %v", err)
	}
	if m != (initflow.Managed{}) {
		t.Errorf("a person's own terminal was answered %+v, want the zero value every fence reads exactly as it did before this row", m)
	}
	if err := gate.recorded(context.Background()); err != nil {
		t.Fatalf("recording an admission nobody read: %v", err)
	}
	if reqs := r.gcs.Requests(); len(reqs) != 0 {
		t.Errorf("a person's own terminal paid %d calls to read marks it skips no confirmation by:\n%v", len(reqs), reqs)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("a person's own terminal filed %+v", recs)
	}

	out, _, err := execute(t, "settings", "set", settingsManagedLayerKey, settingsManagedLayerValue, "--layer", "project", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "review_rounds: 4") {
		t.Error("the owner's own run did not write")
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("a person's own terminal filed %+v", recs)
	}
}

// And the boundary itself: a repository-scoped ordinary write in a managed
// project reads no mark either, because today's refusal does not apply to it —
// a session may edit a file it could edit anyway. The gate is consulted only
// where the refusal it stands in for would have fired, so a transient Storage
// or Resource Manager failure cannot turn a write that lands today into a
// refusal (M6's review of 2026-11-03, item 2).
func TestSettingsSetRepoWriteInAManagedProjectReadsNoMark(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes")
	if err != nil {
		t.Fatalf("a repository-scoped write was refused: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "base_branch: develop") {
		t.Error("the write did not land")
	}
	if reqs := r.gcs.Requests(); len(reqs) != 0 {
		t.Errorf("a write today's refusal does not apply to paid %d calls to read marks:\n%v", len(reqs), reqs)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("a repository-scoped write filed %+v", recs)
	}
}
