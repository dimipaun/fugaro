package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// project_policy_managed_test.go is M3's half of the publisher's tests: the
// managed project's coding agent's session, and the gate that replaces the
// refusal it reads today (D1012, spec §4.2).
//
// What each world is. A managed project has BOTH marks — the creation fact in
// the runs bucket and the label on the project itself — and a project with
// only one of the two is not managed, which is why the fixture can seed them
// apart. Every world here is hermetic: internal/gcpfake's fakes, t.TempDir()
// for the one file a test writes, and the credentials read off a path in that
// directory, so a developer's own Application Default Credentials can never
// decide what the world says or what a record names.
//
// The tests come in three groups, and the reason each exists is the reason the
// task does:
//
//   - the refusals: --delete, --seed-repos and --remove-routing are never
//     admitted (spec §4.2), and the refusal is the GATE's own sentence with a
//     record behind it, not the marker's refusal — which writes nothing and
//     would be a refusal this design did not make (D1023, D1025);
//   - the admission: a node narrower than the one it replaces proceeds, the
//     session reads what it proceeds on, and the record names the exact bytes
//     that were written (D1022);
//   - the two properties every command in the set owes: the gate is read
//     BEFORE the first write (the plan's §5 wiring property, which is what
//     catches a command that keeps its own confirmation), and a person's own
//     session pays nothing for the gate at all — not a read, not a record, and
//     not a change in behaviour if a mark read would have failed.

// managedPolicyFixture is one installation the publisher reads: the init rig's
// fakes and its isolated home, the policy fixture's own config — the
// operator's providers, two repositories, this installation's budget database
// — and, on top, the marks a managed project carries.
type managedPolicyFixture struct {
	*initRig
	db *gcpfake.RTDB
	// endpoints is the config file's endpoints block as the rig wrote it, kept
	// so a test that rewrites the file (a second publish from a machine whose
	// file says something else) keeps the fakes the rig was built with.
	endpoints string
}

// newManagedPolicyFixture is a project that is managed: both marks are in
// place, so a coding agent's session reaches the gate.
func newManagedPolicyFixture(t *testing.T, extra string) *managedPolicyFixture {
	return newPolicyFixture(t, extra, true)
}

// newUnmanagedPolicyFixture is the same installation without them, which is
// every ordinary project: the marks are what a managed project has and this
// one does not.
func newUnmanagedPolicyFixture(t *testing.T, extra string) *managedPolicyFixture {
	return newPolicyFixture(t, extra, false)
}

func newPolicyFixture(t *testing.T, extra string, managedProject bool) *managedPolicyFixture {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := newInitRig(t)
	db := gcpfake.NewRTDB(t)
	f := &managedPolicyFixture{initRig: r, db: db, endpoints: endpointsBlock(t, r, gcpfake.NewFirestore(t))}
	if err := os.WriteFile(r.cfg, []byte(f.configText(t, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	db.Set("fugaro/project", "aurora")
	db.Set(budget.PathMark, auroraMark())
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	writeRunsMarker(t, filepath.Join(r.dir, "runs"), "aurora", initProject)
	if managedProject {
		markPolicyProjectManaged(t, r)
	}
	// The account a record names, and the day it is written: both are read out
	// of the world at the write, so a test that wants to read the day's
	// records back has to pin them.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(r.dir, "no-application-default-credentials.json"))
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(r.dir, "gcloud"))
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })
	oldNow := managedGateNow
	managedGateNow = func() time.Time { return managedGateTestNow }
	t.Cleanup(func() { managedGateNow = oldNow })
	return f
}

// markPolicyProjectManaged puts BOTH halves of the mark on the rig: the
// creation fact in its runs bucket, which the read proves against the project
// number and the bucket's own label, and the label on the project itself.
func markPolicyProjectManaged(t *testing.T, r *initRig) {
	t.Helper()
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	r.gcs.Put(initRunsBucket, managed.MarkerObject, policyMarker())
	labelled(r.crm, initProject)
}

// policyMarker is the creation fact this fixture's project holds: version 1,
// naming the GCP project the read is asked about.
func policyMarker() []byte {
	return []byte(`{"version":1,"gcp_project":"` + initProject + `"}`)
}

// endpointsBlock is the endpoints line the init rig's own config carries — the
// fakes for every endpoint this program names, plus the Firestore fake this
// fixture's budget block reaches its spend history through. Reading it off the
// file rather than writing a second copy of it is what keeps the two from
// drifting.
func endpointsBlock(t *testing.T, r *initRig, fs *gcpfake.Firestore) string {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "endpoints: ") {
			return strings.Replace(line, "no_auth: true", "firestore: "+fs.URL+", no_auth: true", 1)
		}
	}
	t.Fatalf("%s carries no endpoints block", r.cfg)
	return ""
}

// configText is the fixture's whole config file: the policy fixture's own
// installation and providers, with this rig's endpoints (every one of them
// named, since a managed read builds the storage and Resource Manager
// clients) and this installation's own runs bucket, which is the bucket the
// creation fact is read from.
func (f *managedPolicyFixture) configText(t *testing.T, extra string) string {
	t.Helper()
	cfg := projectConfigText(f.dir, f.db.URL, projectProvidersYAML, extra)
	cfg = strings.Replace(cfg, "endpoints: { no_auth: true }", f.endpoints, 1)
	return strings.Replace(cfg, "runs_bucket: unused-bucket", "runs_bucket: "+initRunsBucket, 1)
}

// rewriteConfig replaces the fixture's file with one whose only difference is
// extra: the second publish of an installation, from a machine whose file says
// something else.
func (f *managedPolicyFixture) rewriteConfig(t *testing.T, extra string) {
	t.Helper()
	if err := os.WriteFile(f.cfg, []byte(f.configText(t, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// audits is the day's records, read back through the reader a surface uses, so
// what a test asserts about is what doctor and report would print.
func (f *managedPolicyFixture) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), managedStorage(t, f.gcs), initRunsBucket, managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// policyOf is the node at config/policy/shared, decoded: the read the policy
// fixture's own helper does, on this fixture's database.
func (f *managedPolicyFixture) policyOf(t *testing.T) *projectpolicy.Policy {
	t.Helper()
	b, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the published node does not decode (found=%v): %v", found, err)
	}
	return p
}

// policyNodeOracle is the node a publish of this fixture would write, read and
// built by a test's own hands rather than by the code under test: the bytes
// the gate's record has to name are these, so a gate that hashed something
// else — the node it read, the diff it printed, the arguments of the write —
// is caught by the assertion rather than by a reader of the record.
func policyNodeOracle(t *testing.T, f *managedPolicyFixture) []byte {
	t.Helper()
	ctx := context.Background()
	lc, err := localcfg.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, true)
	if err != nil {
		t.Fatal(err)
	}
	_, exists, raw, err := readPolicyNode(ctx, db, lc)
	if err != nil {
		t.Fatal(err)
	}
	old, err := publishedPolicyOf(raw, exists)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := policyNodeForPublish(ctx, lc, old, raw, exists, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// gateRefusal fails the test unless err is the managed gate's own refusal
// naming rule: the two halves the tests below turn on are that the sentence is
// the gate's (not the marker's, which is not a refusal this design makes) and
// that it names the rule that fired (D1023).
func gateRefusal(t *testing.T, err error, rule string) {
	t.Helper()
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the refusal is not the gate's (%v): %v", managedmode.ErrNotConverged, err)
	}
	if !strings.Contains(err.Error(), managedmode.NotConverged) {
		t.Errorf("the refusal does not carry the gate's own sentence: %v", err)
	}
	if !strings.Contains(err.Error(), rule) {
		t.Errorf("the refusal does not name the rule %q that refused: %v", rule, err)
	}
	if strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the refusal is the coding agent's own, so the gate was never asked: %v", err)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: a gate refusal is the session's to read, not an outage", got, ExitUserError)
	}
}

// mentions reports whether any line carries want: a rule that fired names the
// rule and then what it saw, so the assertions are about the rule and never
// about its whole sentence.
func mentions(lines []string, want string) bool {
	return slices.ContainsFunc(lines, func(s string) bool { return strings.Contains(s, want) })
}

// refusalRecord fails the test unless exactly one record was written and it
// says a managed project's step was refused by rule: the record is half of
// what a refusal is (D1025 — an unattended run that refused every step and
// reported success leaves, without it, no trace of why nothing happened).
func refusalRecord(t *testing.T, f *managedPolicyFixture, rule string) managedmode.AuditRecord {
	t.Helper()
	got := f.audits(t)
	if len(got) != 1 {
		t.Fatalf("the refusal left %d audit records, want one (D1025)", len(got))
	}
	rec := got[0]
	if rec.Verdict != managedmode.AuditRefused {
		t.Errorf("the record's verdict is %q, want %q", rec.Verdict, managedmode.AuditRefused)
	}
	if !mentions(rec.Rules, rule) {
		t.Errorf("the record does not name the rule %q that refused: %v", rule, rec.Rules)
	}
	if rec.Command != selfCommand()+" project policy publish" {
		t.Errorf("the record names the command %q", rec.Command)
	}
	if rec.Project != "aurora" || rec.GCPProject != initProject {
		t.Errorf("the record names %q in %q, want this installation", rec.Project, rec.GCPProject)
	}
	if rec.AgentMarker != "CLAUDECODE" {
		t.Errorf("the record says the session was %q", rec.AgentMarker)
	}
	if rec.Identity != "user:"+managedGateUser {
		t.Errorf("the record names the identity %q, want the account the write ran as", rec.Identity)
	}
	if rec.MarkerVersion != 1 {
		t.Errorf("the record names creation fact version %d, want the one the marks answered with", rec.MarkerVersion)
	}
	if !rec.At.Equal(managedGateTestNow) {
		t.Errorf("the record is at %s, want the write's own clock", rec.At)
	}
	return rec
}

// The red step's first half: --delete is never admitted, so in a managed
// project a coding agent's session reaches the GATE and is refused by it —
// not by the marker, whose refusal is the one this design replaces. Nothing is
// removed, and the refusal leaves the record an owner reads to find out why
// nothing happened.
func TestPolicyPublishManagedRefusesDelete(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1") // a coding agent's session

	out, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err == nil {
		t.Fatalf("a coding agent's session deleted the policy of a managed project:\n%s", out)
	}
	gateRefusal(t, err, policyDeleteRule)
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused deletion wrote something:\nbefore %s\nafter  %s", before, after)
	}
	refusalRecord(t, f, policyDeleteRule)
}

// The red step's second half: --seed-repos re-seeds the allow-list from this
// machine's own config, which is a widening by construction, and a widening is
// refused for the same reason a deletion is — the node bounds runs that are
// already launching.
func TestPolicyPublishManagedRefusesSeedRepos(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	node := staleBudgetNode()
	node["allow_repos"] = []string{"github/acme/app"}
	f.db.Set(projectpolicy.PathShared(), node)
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish", "--seed-repos")
	if err == nil {
		t.Fatalf("a coding agent's session re-seeded the allow-list:\n%s", out)
	}
	gateRefusal(t, err, policySeedReposRule)
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused publish wrote something:\nbefore %s\nafter  %s", before, after)
	}
	if got := f.policyOf(t).AllowRepos; len(got) != 1 || got[0] != "github/acme/app" {
		t.Errorf("allow_repos = %v, want the node's own list: a refused publish never re-seeds", got)
	}
	refusalRecord(t, f, policySeedReposRule)
}

// --remove-routing drops the published policy's routing block, which is the
// one member a publish writes that decides where a Claude call goes: it is
// never admitted either, and the refusal is the gate's.
func TestPolicyPublishManagedRefusesRemoveRouting(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish", "--remove-routing")
	if err == nil {
		t.Fatalf("a coding agent's session dropped the routing block:\n%s", out)
	}
	gateRefusal(t, err, policyRoutingFlagRule)
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused publish wrote something:\nbefore %s\nafter  %s", before, after)
	}
	refusalRecord(t, f, policyRoutingFlagRule)
}

// The red step's third part: the one shape the gate admits. The node the
// publish writes is narrower than the one it replaces — its per-run ceiling
// drops from the node's own to the config's, and every allow list, the routing
// table and the credentials block are untouched — so the session proceeds, and
// what it was admitted for is the document the record names.
func TestPolicyPublishManagedAdmitsANarrowerNode(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	want := policyNodeOracle(t, f)
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("a managed project refused a node narrower than the one it replaces: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the session was not told what it proceeds on:\n%s", out)
	}
	p := f.policyOf(t)
	if p.Budget.Mode != "enforce" || p.Budget.PerRunUSD != 5 {
		t.Errorf("the published budget is %+v, want the config's: a publish tightens a cap and never raises one", p.Budget)
	}
	got := f.audits(t)
	if len(got) != 1 {
		t.Fatalf("the admitted publish left %d audit records, want one (D1014)", len(got))
	}
	rec := got[0]
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q", rec.Verdict, managedmode.AuditAdmitted)
	}
	if rec.PlanDigest != gateDigest(want) {
		t.Errorf("the record's plan_digest is %s, and the document this publish wrote hashes to %s: the record has to name the document that was judged (D1022)", rec.PlanDigest, gateDigest(want))
	}
	if len(rec.Rules) == 0 {
		t.Error("the record names no rule, and it is written for a step whose rules ran")
	}
}

// The other half of the same rule: a publish that would RAISE a cap is
// refused, whichever command it is (spec §4.1 rule 6). The comparison is the
// one `settings set` already decides with, so a widening this door refuses is
// a widening that door refuses.
func TestPolicyPublishManagedRefusesARaisedCap(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(1))
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a coding agent's session raised the project's per-run ceiling:\n%s", out)
	}
	gateRefusal(t, err, policyCapRule)
	if !strings.Contains(err.Error(), "budget.per_run_usd") {
		t.Errorf("the refusal does not name the member it would have raised: %v", err)
	}
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused publish wrote something:\nbefore %s\nafter  %s", before, after)
	}
	refusalRecord(t, f, policyCapRule)
}

// And the widening rule the spec states with this example: a layer change that
// adds a repository to a profile's scope (spec §4.1 rule 5). The first publish
// is a person's and leaves a node whose default profile admits one repository;
// the second is a coding agent's, from a file whose profile admits a second
// one, which is one more repository whose runs hold whatever that profile
// mounts.
func TestPolicyPublishManagedRefusesAWidenedProfileScope(t *testing.T) {
	one := "profiles:\n  standard: { provider: github, cpu: 1, memory: 512Mi, timeout_class: 6h, allow_repos: [github/acme/app] }\n"
	two := "profiles:\n  standard: { provider: github, cpu: 1, memory: 512Mi, timeout_class: 6h, allow_repos: [github/acme/app, github/acme/lib] }\n"
	f := newManagedPolicyFixture(t, one)
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	if out, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("a person's own publish was refused: %v\n%s", err, out)
	}
	if got := f.policyOf(t).Profiles["standard"].AllowRepos; len(got) != 1 {
		t.Fatalf("the first publish wrote the profile's allow_repos %v, want the file's one entry", got)
	}
	f.rewriteConfig(t, two)
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a coding agent's session widened a profile's scope:\n%s", out)
	}
	gateRefusal(t, err, policyProfileRule)
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused publish wrote something:\nbefore %s\nafter  %s", before, after)
	}
	refusalRecord(t, f, policyProfileRule)
}

// The plan's §5 wiring property for this command: the gate is read BEFORE the
// first write. The world that says so is one where the mark read fails — a
// project this program cannot prove is managed is a project whose
// confirmations it must not skip — so the session is refused, and the node and
// the per-repository children are exactly as they were. A command that read
// the gate after its write, or that wrote the children before judging the
// node, would have left one of the two changed.
func TestPolicyPublishReadsTheGateBeforeAnyWrite(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	f.db.Set(projectpolicy.PathRepos(), map[string]any{"github-acme-app": map[string]any{"accounts": []string{"default"}}})
	f.gcs.RefusePath(managed.MarkerObject, http.StatusInternalServerError, "INTERNAL", "", "backend error")
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a creation fact that could not be read was read as a permission:\n%s", out)
	}
	if !strings.Contains(err.Error(), "creation fact") {
		t.Errorf("the refusal does not say what could not be read: %v", err)
	}
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the publish wrote before it read the gate:\nbefore %s\nafter  %s", before, after)
	}
	if got := f.audits(t); len(got) != 0 {
		t.Errorf("a record was written for a project whose marks could not be read: %v", got)
	}
}

// The other side of the same boundary, and the sentence a project that is NOT
// managed keeps: today's refusal, byte for byte, before anything is read. The
// marks are what make a managed project, and a project without them is every
// ordinary project — its steps are a person's, and an agent's session is
// refused exactly as it was before this design existed.
func TestPolicyPublishUnmanagedProjectKeepsTheAgentRefusal(t *testing.T) {
	f := newUnmanagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a coding agent's session published the policy of a project that is not managed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the refusal is not today's: %v", err)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	if strings.Contains(err.Error(), managedmode.NotConverged) {
		t.Errorf("a project that is not managed was refused by the gate's sentence: %v", err)
	}
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused publish wrote something:\nbefore %s\nafter  %s", before, after)
	}
	if got := f.audits(t); len(got) != 0 {
		t.Errorf("a project that is not managed left %d audit records, want none (D1025)", len(got))
	}
}

// A project whose cloud half this installation cannot even build the clients
// of — the config names no endpoint for GCS or Resource Manager, which is what
// an installation that predates them reads as — is a project the gate cannot
// ask about, and one it has therefore not proved is managed. The session keeps
// today's refusal exactly, and nothing is written or recorded: the fail-closed
// direction twice over, since the zero gate admits nothing. Failing the command
// instead would answer a session with a sentence about endpoints it has no
// business reading, and would be the one shape this design must not have — a
// step that does not proceed for a reason other than the refusal.
func TestPolicyPublishUnbuildableGateKeepsTheAgentRefusal(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	base := projectConfigText(f.dir, f.db.URL, projectProvidersYAML, "")
	base = strings.Replace(base, "runs_bucket: unused-bucket", "runs_bucket: "+initRunsBucket, 1)
	if err := os.WriteFile(f.cfg, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	before := nodeOf(t, f.db)
	t.Setenv("CLAUDECODE", "1")

	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a coding agent's session published through a gate no client could be built for:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the refusal is not today's: %v", err)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	if strings.Contains(err.Error(), "endpoint") {
		t.Errorf("the session was answered with the installation's endpoints: %v", err)
	}
	if after := nodeOf(t, f.db); after != before {
		t.Errorf("the refused publish wrote something:\nbefore %s\nafter  %s", before, after)
	}
	if got := f.audits(t); len(got) != 0 {
		t.Errorf("a gate that could not be built left %d audit records, want none", len(got))
	}
}

// And a person's own session on a MANAGED project pays nothing for the gate:
// not a record, and not even a read — the creation fact is refused at the API
// here, and the publish still lands, because the gate is the coding agent's
// session's business and a person's own confirmation is already the record.
func TestPolicyPublishOwnerOfAManagedProjectPaysNothingForTheGate(t *testing.T) {
	f := newManagedPolicyFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	f.gcs.RefusePath(managed.MarkerObject, http.StatusInternalServerError, "INTERNAL", "", "backend error")

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("a person's own publish of a managed project stopped on the gate: %v\n%s", err, out)
	}
	if got := f.policyOf(t).Budget.PerRunUSD; got != 5 {
		t.Errorf("the published per-run ceiling is %v, want the config's", got)
	}
	if got := f.audits(t); len(got) != 0 {
		t.Errorf("a person's own publish left %d audit records, want none: the record family is the gate's", len(got))
	}
}
