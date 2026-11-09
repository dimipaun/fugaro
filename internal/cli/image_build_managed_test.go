package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/infra"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// image_build_managed.go's tests: `fugaro image build` asks nothing in any
// session (T4, D1540), and a managed project's own terminal is judged and
// recorded by the row D1206 writes for this command — the entry exists in the
// effective catalog, the build account is the project's own for that entry's
// source repository — with the project's kill switch read beside it before
// every build. D1543 took the count bound (max_image_builds) out of the row:
// what bounds a build now is the lease its estimated cost is taken under
// immediately before its submission, which is image_cost.go's and is exercised
// by the command tests here only as far as its refusals show (image_gate_test.go
// and image_cost_test.go own the lease itself).
//
// What is pinned here, and why each one is a test rather than a comment. A
// command that asked and a command that did not both submit one build in a test
// that only watched the fake's request count, so "nothing was asked" is said
// three ways instead: the reader the session's own stdin is handed to is never
// reached (unreadStdin fails the test when anything reads it), the sentence the
// removed prompt printed is nowhere in what the session read, and what a
// managed project's terminal read is the gate's own line about the record.
//
// The other half is what the gate is NOT. Nothing prompts, so every session the
// door does not stand in for — a coding agent, a pipe, a project that is not
// managed, a mark that cannot be read, a project unflagged between two reads —
// SUBMITS its build with no judgement, no note and no record, and the tests
// below pin each of those as its own property rather than as an absence they
// share. A build a rule refuses is still refused: the refusal is a refusal and
// there is no prompt left to fall back to.
//
// Every world below is hermetic: internal/gcpfake's Cloud Build, Secret
// Manager, GCS, Resource Manager and RTDB fakes, t.TempDir() for every file,
// and the credentials read off a path in that directory (isolateGateCredentials)
// so a developer's own Application Default Credentials can never decide what a
// record says. No test here contacts a cloud.

// buildWorld is one installation's marks and clients, over the fixture whose
// config the command reads: where a record is written and read back. The
// fixture's own runs bucket and this world's GCS fake are the same bucket by
// name — the one the installation's GCP project derives — which is what a record
// written through the gate and read back through a surface have in common.
type buildWorld struct {
	gcs *gcpfake.GCS
	crm *crm.Service
	// crmFake is the project's label as the owner changes it: the marks are
	// re-read immediately before every write (D1024), so a test that revokes
	// one has to be able to take the label away between two reads -- and the
	// service crm is a client of the fake, not the fake itself.
	crmFake *gcpfake.CRM
	db      *gcpfake.RTDB
	s       *storage.Service
}

// managedBuildMarker is the creation fact of a managed installation: the
// version-3 object M5 reads (managedRefreshMarker's shape, this fixture's own
// project). The count it records is the bound D1543 retired — no rule of this
// gate reads it any more, and the shape still carries it because the version
// that stops carrying it is T10's migration, not this task's.
func managedBuildMarker(bound int64) []byte {
	return []byte(fmt.Sprintf(`{"version":3,"gcp_project":%q,"budget":{"amount":5000,"currency":"USD"},"max_image_builds":%d}`, initProject, bound))
}

// managedBuildWorld makes the fixture f an installation the marks call managed:
// the runs bucket's own label and creation fact, the project's label, the
// budget database the ownership question is asked with (D1017), and the three
// endpoints a gate reads and records through.
//
// Why the runs bucket is rewritten to the project's own name. The record goes
// into the runs bucket (managedGate.record) and the marks are read out of the
// bucket the installation's GCP project derives, so both halves of this world
// have to be the one bucket the fixture would have named after `fugaro update`.
func managedBuildWorld(t *testing.T, f *cloudFixture, marker []byte) *buildWorld {
	t.Helper()
	isolateGateCredentials(t)
	gcs, c, db := gcpfake.NewGCS(t), gcpfake.NewCRM(t), gcpfake.NewRTDB(t)
	// Every endpoint infra.NewClients insists on with no_auth (fakes) has to be
	// here: one left empty would send a call to Google itself.
	iam, ar, sch := gcpfake.NewIAM(t), gcpfake.NewArtifactRegistry(t), gcpfake.NewScheduler(t)
	gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	gcs.Put(initRunsBucket, managed.MarkerObject, marker)
	c.AddProject(initProject, initProjectNumber)
	labelled(c, initProject)
	setConfigScalar(t, "runs_bucket", initRunsBucket)
	rewriteConfig(t, "no_auth: true }",
		"storage: "+gcs.URL+"/storage/v1/, resource_manager: "+c.URL+"/, iam: "+iam.URL+
			"/, artifact_registry: "+ar.URL+"/, cloud_scheduler: "+sch.URL+"/, no_auth: true }")
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	seedBuildDatabase(t, db)
	return &buildWorld{gcs: gcs, crm: managedCRM(t, c), crmFake: c, db: db, s: managedStorage(t, gcs)}
}

// seedBuildDatabase gives a fixture's database the two nodes `fugaro update`
// writes into every installation's and that a build now needs. One is the mark
// that makes the database this installation's at all: every writer into it asks
// first (budgetDBUnadopted), and a database holding data with no mark is
// another product's. The other is the cap a lease is judged against, which is
// the one that bites in observe mode (D1548) and the one that grants a lease at
// all — absent, the predicate denies every build as unreservable.
func seedBuildDatabase(t *testing.T, db *gcpfake.RTDB) {
	t.Helper()
	db.Set(budget.PathMark, auroraMark())
	db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5 * usd1})
}

// unmanagedBuildWorld is the ordinary installation: the runs bucket and the
// project exist and carry no mark of any kind, so the marks answer "not
// managed" rather than failing to be read.
func unmanagedBuildWorld(t *testing.T, f *cloudFixture) *buildWorld {
	t.Helper()
	isolateGateCredentials(t)
	gcs, c, db := gcpfake.NewGCS(t), gcpfake.NewCRM(t), gcpfake.NewRTDB(t)
	// Every endpoint infra.NewClients insists on with no_auth (fakes) has to be
	// here: one left empty would send a call to Google itself.
	iam, ar, sch := gcpfake.NewIAM(t), gcpfake.NewArtifactRegistry(t), gcpfake.NewScheduler(t)
	gcs.AddBucket(initRunsBucket, initProjectNumber, nil)
	c.AddProject(initProject, initProjectNumber)
	setConfigScalar(t, "runs_bucket", initRunsBucket)
	rewriteConfig(t, "no_auth: true }",
		"storage: "+gcs.URL+"/storage/v1/, resource_manager: "+c.URL+"/, iam: "+iam.URL+
			"/, artifact_registry: "+ar.URL+"/, cloud_scheduler: "+sch.URL+"/, no_auth: true }")
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	seedBuildDatabase(t, db)
	return &buildWorld{gcs: gcs, crm: managedCRM(t, c), crmFake: c, db: db, s: managedStorage(t, gcs)}
}

// rewriteConfig replaces old with new in the fixture's config, and fails when
// old is not there: every one of these rewrites is stated over the fixture's own
// text, so one that silently did nothing would leave a test asserting about a
// world it never built.
func rewriteConfig(t *testing.T, old, new string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(data), old, new, 1)
	if out == string(data) {
		t.Fatalf("this fixture's config has no %q to rewrite:\n%s", old, data)
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// audits is the day's records of this world's installation, read back through
// the reader a surface uses: what these tests assert about is what doctor and
// report would print.
func (w *buildWorld) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), w.s, initRunsBucket, managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// fixtureConfig is the fixture's own config as the command loads it: the machine
// type, the region and the registry host every plan value and every rule is
// stated over come from here, so a test that spells a document out reads the
// same ones the command does instead of guessing them.
func fixtureConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// buildHostAndAccount are the two values the catalog arm states its build with:
// the installation's own registry host, and the build account infra.Repo derives
// for the fixture's repository (acme/app on Bitbucket) in this project.
func buildHostAndAccount(t *testing.T, lc *localcfg.Config) (host, account string) {
	t.Helper()
	host, err := infra.RegistryHost(lc)
	if err != nil {
		t.Fatal(err)
	}
	return host, gcp.BuildServiceAccountID(mustSlug("bitbucket", "acme/app")) + "@" + initProject + ".iam.gserviceaccount.com"
}

// catalogBuildPlan is the document the catalog arm judges and records for one
// entry: the entries the invocation builds, the entry this admission is about,
// and the installation's one shared registry a catalog image is pushed into
// (D1152), with the kill switch D1013 re-reads before each build.
func catalogBuildPlan(t *testing.T, lc *localcfg.Config, entries []string, entry string) buildPlan {
	t.Helper()
	host, account := buildHostAndAccount(t, lc)
	registry := host + "/" + gcp.SharedRegistryRepoID()
	named := make([]config.Entry, len(entries))
	for i, e := range entries {
		named[i] = config.Entry{Name: e}
	}
	return buildPlan{
		Repo: "acme/app", Origin: "acme/app", Entry: entry, Entries: entries,
		Kind: config.BaseKind, Base: "b:1",
		Account: account, Registry: registry, Machine: lc.Build.MachineType,
		What: catalogBuildBanner(named, lc.Build.MachineType, account, registry),
	}
}

// planDigest is the digest the record of p must carry: the sha256 of the
// document's own bytes, computed here rather than by the code under test
// (gateDigest is the oracle internal/managed's own audit tests use).
func planDigest(t *testing.T, p buildPlan) string {
	t.Helper()
	doc, err := p.bytes()
	if err != nil {
		t.Fatal(err)
	}
	return gateDigest(doc)
}

// unreadStdin is the person's own stdin in the worlds where nothing may read
// it: the test's own assertion fails when a question was asked, because the only
// reader of a command's stdin is the confirmation.
type unreadStdin struct{ read bool }

func (r *unreadStdin) Read([]byte) (int, error) { r.read = true; return 0, io.EOF }

// assertNotAsked is what every managed world below asserts about what the
// person read: the sentence the typed prompt prints is nowhere, the gate's own
// line is where the prompt would have been, and the plan the verdict is about is
// the one printed. It answers the day's records, which each test goes on to say
// about.
func assertNotAsked(t *testing.T, errOut string, want string) {
	t.Helper()
	if strings.Contains(errOut, "Type "+initProjectName+" to submit the build: ") {
		t.Errorf("a managed project's own terminal was asked to type the project's name:\n%s", errOut)
	}
	if !strings.Contains(errOut, initflow.GateProceeded()) {
		t.Errorf("the person did not read that the gate's verdict is the confirmation:\n%s", errOut)
	}
	if !strings.Contains(errOut, want) {
		t.Errorf("the plan the verdict is about is not what was printed:\n%s", errOut)
	}
}

// oneRecord is the day's single record, which every admitted build below leaves:
// a command that judged a document and never called gate.recorded leaves none.
func oneRecord(t *testing.T, w *buildWorld, verdict managedmode.AuditVerdict) managedmode.AuditRecord {
	t.Helper()
	got := w.audits(t)
	if len(got) != 1 {
		t.Fatalf("the day holds %d records, want exactly one (%+v)", len(got), got)
	}
	if got[0].Verdict != verdict {
		t.Errorf("the record's verdict is %q, want %q", got[0].Verdict, verdict)
	}
	return got[0]
}

// catalogBuildGate opens the gate over a world the way the door does, for the
// tests that judge an entry directly: the refusal paths are about what a rule
// does with a document, and driving them through the command would need a
// catalog the fixture cannot declare.
func catalogBuildGate(t *testing.T, w *buildWorld, lc *localcfg.Config) *buildManaged {
	t.Helper()
	_, db, err := budgetDBFor(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := readManagedGate(t.Context(), managedGateEnv{lc: lc, s: w.s, crm: w.crm, db: db}, buildGateFacts())
	if err != nil || !gate.managed {
		t.Fatalf("both marks are in place and the gate did not open: %v (managed %v)", err, gate.managed)
	}
	return &buildManaged{lc: lc, gate: &gate, db: db}
}

// catalogBuildCredential gives the fixture the project's own Bitbucket
// credential. The copy of a repository-sourced entry reads it before it opens
// the provider (catalogProviderEnv, #1871), so a world whose entries are all
// inline -- which needs no provider at all -- is the only one that can leave
// it out, and a fake that is not told about the secret fails the read.
func catalogBuildCredential(t *testing.T, f *cloudFixture) {
	t.Helper()
	f.sm.AllowAccess()
	putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, []byte(catalogTokenPlaceholder))
}

// stubCatalogProviderHook is stubCatalogProvider with a hook that runs the first
// time the copy's provider is opened: for the one property that needs the
// project changed WHILE the command runs — D1013's bound and switch are re-read
// before each build rather than once per command. The copy is opened once per
// entry, between that entry's admission and its submission, so a hook here
// changes the marks between two builds and nowhere inside one.
func stubCatalogProviderHook(t *testing.T, files map[string]string, hook func()) {
	t.Helper()
	p := &fake.Provider{Repo: "acme/app", State: fake.State{Files: files}}
	var once sync.Once
	was := openCatalogProvider
	openCatalogProvider = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "", func(context.Context, string, string) (gitprov.Provider, []string, error) {
			once.Do(hook)
			return p, nil, nil
		}, nil
	}
	t.Cleanup(func() { openCatalogProvider = was })
}

// A person at their own real terminal, in a managed project, runs `fugaro image
// build <entry>`: nothing is typed, the sentence the removed prompt printed is
// not printed, the verdict's own line is, one build is submitted and the
// project's audit holds one admission of the document the person read — the
// document that carries the entry and the facts the row's rules were stated
// over.
//
// It failed before this task for the reason the task exists: every arm of this
// command asked for the project's name typed at a terminal (confirmBuild).
//
// The mutations this catches are the tempting ones: a confirm that ignores the
// gate (the prompt returns, the reader is reached, the first assertion fails), a
// recorded that is never called (no record), and a plan whose digest is computed
// over anything but the document the person was shown.
func TestImageBuildManagedCatalogAsksNothingAtTheTerminal(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	noAgentSession(t)
	fakeTerminal(t)
	lc := fixtureConfig(t)
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "alpha", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if stdin.read {
		t.Errorf("a managed project's own terminal was read, so a question was asked:\n%s", errOut)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("the managed project's entry build was submitted %d times, want one\n%s%s", buildPosts(fb), out, errOut)
	}
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha")
	assertNotAsked(t, errOut, plan.What)
	rec := oneRecord(t, w, managedmode.AuditAdmitted)
	if want := planDigest(t, plan); rec.PlanDigest != want {
		t.Errorf("the record's plan_digest is %q, want the sha256 of the document the person was shown (%q)", rec.PlanDigest, want)
	}
	if rec.Command != "fugaro image build" || rec.Project != initProjectName || rec.GCPProject != initProject {
		t.Errorf("the record names %q on %q/%q, want this command's own invocation of this project",
			rec.Command, rec.Project, rec.GCPProject)
	}
	if rec.AgentMarker != "" {
		t.Errorf("the record says the session carried the marker %q, and the person at this terminal read the gate's line", rec.AgentMarker)
	}
	rules := strings.Join(rec.Rules, "; ")
	for _, rule := range []string{
		"the build is an entry of this project's effective catalog (alpha)",
		"the entry is written in this project's own state (alpha names no source repository)",
		"the build runs as this project's own build account",
		"the project's kill switch is off",
		"the build is this checkout's own repository (acme/app)",
		"the base image is not another project's pull-through reference (b:1)",
		"the build pushes into this installation's own registry",
	} {
		if !strings.Contains(rules, rule) {
			t.Errorf("the admitted record does not carry the rule %q: %s", rule, rules)
		}
	}
	// The copy is the definition the submitted build pins: what the request
	// carries is the sha256 of the object in the bucket, so the two are one
	// value read from the bucket this command wrote, not two figures a test
	// spells out.
	copied, gen := catalogCopy(t, gcs, "alpha")
	if gen <= 0 || copied == "" {
		t.Fatalf("the entry's copy in the runs bucket is %q (generation %d), and the build behind the confirmation reads it", copied, gen)
	}
	if tag := catalogBuildSubs(t, fb)["_CATALOG_TAG"]; tag != sha256Hex([]byte(copied)) {
		t.Errorf("the build's _CATALOG_TAG is %v, and the copy the confirmation let through hashes to %s", tag, sha256Hex([]byte(copied)))
	}
}

// The whole-catalog build is what the retired count used to cut short: three
// entries were judged one after another against a per-invocation bound, so the
// second was refused. With the bound gone (D1543) every entry is judged on its
// own and every one is submitted, and the day holds one admission per build
// rather than one verdict for the invocation — the catastrophic mutation this
// catches is a gate that judges the first build and lets the rest through
// unrecorded.
func TestImageBuildManagedJudgesEveryEntryOfACatalog(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	noAgentSession(t)
	fakeTerminal(t)

	out, errOut, err := executeStdinReader(t, &unreadStdin{}, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, initflow.GateProceeded()) {
		t.Errorf("the admitted builds did not print the gate's own sentence:\n%s", errOut)
	}
	if n := buildPosts(fb); n != 3 {
		t.Fatalf("submitted %d builds, want every entry of the catalog\n%s%s", n, out, errOut)
	}
	got := w.audits(t)
	if len(got) != 3 {
		t.Fatalf("the day holds %d records, want one per build (%+v)", len(got), got)
	}
	for _, r := range got {
		if r.Verdict != managedmode.AuditAdmitted {
			t.Errorf("a build of the catalog left a %q record, want every one admitted", r.Verdict)
		}
	}
}

// The gate re-reads the marks immediately before the write it is about to make
// (D1024), and this is what that second read is FOR now that nothing prompts: a
// project whose owner unflags it between the door's own read and the build's
// admission is not a managed project any more, so the gate has no verdict to
// give and writes no record of one. The build is still submitted — nothing asks
// and nothing refuses — and the admission answers nil, which is the same answer
// an unmanaged project's own build gets.
//
// This is the consequence T4 leaves behind and T9's audit-only gate (D1545)
// inherits: what the second read protects is the RECORD, not the submission. It
// is pinned rather than assumed because the alternative reading — a gate that
// refuses or prompts on a revoked project — is a refusal this policy does not
// have, and a test that did not say so would let one in.
//
// The window is made reachable rather than assumed. The one read between the
// door's own read and the gate's second look at the marks is the project's kill
// switch, so the fake holds that request in flight (HoldNext) while the owner
// takes the label away and then lets it go: the gate's admission is decided on
// marks that changed inside the window, which is the case the second read exists
// for and the case no other world in this file builds — they all leave the marks
// as the door found them.
func TestImageBuildManagedRevokedMidCommandRecordsNothingAndAsksNothing(t *testing.T) {
	_, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	noAgentSession(t)
	lc := fixtureConfig(t)
	// The world the door read: the label is put back so that revocation is the
	// only change between the two reads.
	labelled(w.crmFake, initProject)
	m := catalogBuildGate(t, w, lc)
	stamped, release := w.db.HoldNext(budget.PathKillGlobal)

	cmd := gateCmd()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	stdin := &unreadStdin{}
	cmd.SetIn(stdin)
	entry := config.Entry{Name: "alpha", Source: config.Source{Kind: config.SourceInline}}
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha")
	done := make(chan error, 1)
	go func() {
		done <- m.admitEntry(t.Context(), cmd, initflow.Conditions{Terminal: true},
			directRulesCatalog("alpha"), entry, mustSlug("bitbucket", "acme/app"), plan)
	}()
	select {
	case <-stamped:
	case <-time.After(10 * time.Second):
		t.Fatal("the admission never read the project's kill switch, so the door was never reached")
	}
	w.crmFake.SetLabels(initProject, map[string]string{managed.ProjectLabelKey: "no"})
	release()
	if err := <-done; err != nil {
		t.Fatalf("a project unflagged mid-command refused the build rather than giving it no verdict: %v", err)
	}

	if stdin.read {
		t.Errorf("a project whose marks were taken away mid-command asked a question:\n%s", out)
	}
	if got := out.String(); strings.Contains(got, initflow.GateProceeded()) || strings.Contains(got, "Type ") {
		t.Errorf("an admission that decided nothing printed a confirmation:\n%s", got)
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a project that was not managed at the second read left %d records (%+v)", len(got), got)
	}
}

// catalogBuildKillsSwitchYAML is the catalog the mid-run test builds a whole
// catalog of, and its order is the point: the repository-sourced entry comes
// FIRST, so the provider its copy is read with is opened before the second
// entry is judged. The copy is opened once per entry, between that entry's
// admission and its submission, so a hook there changes the marks between two
// builds and nowhere inside one.
const catalogBuildKillsSwitchYAML = `image_catalog:
  entries:
    - name: base
      tags: [node]
      source: { kind: dockerfile, repo: acme/app, path: .fugaro/base.Dockerfile }
    - name: tools
      tags: [node]
      source: { kind: inline, inline: "FROM example.invalid/base\nRUN true\n" }
`

// The same property with the value an operator actually changes: the project's
// kill switch is read before EACH build, so a switch turned on after the first
// build stops the second. A command that read the switch once for the whole
// invocation would submit both entries and fail this assertion of one.
func TestImageBuildManagedReadsTheKillSwitchBeforeEachBuild(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildKillsSwitchYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(3))
	catalogBuildCredential(t, f)
	stubCatalogProviderHook(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched}, func() {
		w.db.Set(budget.PathKillRepo(mustSlug("bitbucket", "acme/app")), map[string]any{"on": true, "reason": "the project is being stopped"})
	})
	noAgentSession(t)
	fakeTerminal(t)

	out, errOut, err := executeStdinReader(t, &unreadStdin{}, "image", "build", "--base", "b:1")
	if err == nil {
		t.Fatalf("the run went on after the project's kill switch was turned on mid-run\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "kill switch is on") || !strings.Contains(err.Error(), "budget resume") {
		t.Errorf("the refusal does not say which switch stopped it and how the owner lifts it:\n%v", err)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("submitted %d builds, want the first alone: the switch is re-read before each one\n%s%s", buildPosts(fb), out, errOut)
	}
	got := w.audits(t)
	if len(got) != 2 {
		t.Fatalf("the day holds %d records, want the admission and the refusal (%+v)", len(got), got)
	}
	for _, r := range got {
		if r.Verdict == managedmode.AuditRefused && !strings.Contains(strings.Join(r.Rules, "; "), "kill switch is on") {
			t.Errorf("the refused record's rules do not name the switch that refused it: %v", r.Rules)
		}
	}
}

// A switch that cannot be read is one this program honours rather than one it
// ignores (D1013): the build does not run, the refusal names the read that
// failed and the command that shows the owner the switch, and the refusal is
// recorded (D1025).
//
// The denial is made after the gate's own read, because `DenyRead` answers for
// the node and every node above it — the ownership question D1017 asks reads
// the database's root — and this test is about the read a BUILD makes. The
// world is the direct one for the same reason: the command opens its door
// before any build, so a world whose switch cannot be read at all is a world
// whose door never opened.
func TestImageBuildManagedUnreadableKillSwitchStops(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(3))
	noAgentSession(t)
	lc := fixtureConfig(t)
	m := catalogBuildGate(t, w, lc)
	w.db.DenyRead(budget.PathKillGlobal)
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha")
	cmd := gateCmd()
	cmd.SetIn(&unreadStdin{})

	err := m.admitEntry(t.Context(), cmd, initflow.Conditions{Terminal: true}, directRulesCatalog("alpha"),
		config.Entry{Name: "alpha", Source: config.Source{Kind: config.SourceInline}}, mustSlug("bitbucket", "acme/app"), plan)
	if err == nil {
		t.Fatal("a build was judged with the kill switch unreadable")
	}
	if !strings.Contains(err.Error(), "kill switch could not be read") || !strings.Contains(err.Error(), "budget show") {
		t.Errorf("the refusal does not say which read failed and how the owner checks it:\n%v", err)
	}
	if buildPosts(fb) != 0 {
		t.Errorf("submitted %d builds with the kill switch unreadable", buildPosts(fb))
	}
	rec := oneRecord(t, w, managedmode.AuditRefused)
	if !strings.Contains(strings.Join(rec.Rules, "; "), "kill switch") {
		t.Errorf("the refused record's rules are %q, and the switch that refused is not among them", rec.Rules)
	}
}

// directRulesCatalog is the catalog the direct-admission tests state their
// rules over: the entry they judge is in it, so the rule that fires is the one
// each test is about — the switch — and not the catalog's own.
func directRulesCatalog(name string) *config.Catalog {
	return &config.Catalog{Entries: []config.Entry{{Name: name, Source: config.Source{Kind: config.SourceInline}}}}
}

// An entry this project's effective catalog does not hold is refused by the
// row's own first rule, and never turned into a prompt: a person's typed name
// is not the answer to a document a rule refused, and nothing is submitted.
//
// The two rules that can fire here are the two halves of D1206's middle clause
// as well, so the table is the row's own list read back: a name the catalog does
// not have, an entry whose Dockerfile is another repository's, and an account
// belonging to another project.
func TestImageBuildManagedRefusesWhatItsRulesRefuse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		entry  config.Entry
		tweak  func(*buildPlan)
		refuse string
	}{
		{
			name:   "an entry the effective catalog does not hold",
			entry:  config.Entry{Name: "ghost", Source: config.Source{Kind: config.SourceInline}},
			refuse: "an entry of this project's effective catalog",
		},
		{
			name: "an entry whose Dockerfile is another repository's",
			entry: config.Entry{Name: "alpha", Source: config.Source{
				Kind: config.SourceDockerfile, Repo: "acme/other", Path: ".fugaro/other.Dockerfile",
			}},
			refuse: "the entry's source repository is this checkout's own",
		},
		{
			name:   "an account belonging to another project",
			entry:  config.Entry{Name: "alpha", Source: config.Source{Kind: config.SourceInline}},
			tweak:  func(p *buildPlan) { p.Account = "builder@another-project.iam.gserviceaccount.com" },
			refuse: "this project's own build account",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
			w := managedBuildWorld(t, f, managedBuildMarker(1))
			noAgentSession(t)
			lc := fixtureConfig(t)
			m := catalogBuildGate(t, w, lc)
			// The catalog the command reads: the fixture states its own, so
			// the keep rule answers with it and no cloud is asked (D1140).
			cat := lc.ImageCatalogEffective(nil)
			if cat == nil {
				t.Fatal("the fixture's own catalog is not in its config")
			}
			plan := catalogBuildPlan(t, lc, []string{"alpha"}, tc.entry.Name)
			if tc.tweak != nil {
				tc.tweak(&plan)
			}
			cmd := gateCmd()
			stdin := &unreadStdin{}
			cmd.SetIn(stdin)

			err := m.admitEntry(t.Context(), cmd, initflow.Conditions{Terminal: true}, cat, tc.entry, mustSlug("bitbucket", "acme/app"), plan)
			if err == nil {
				t.Fatal("a plan a rule refused was admitted")
			}
			if !strings.Contains(err.Error(), tc.refuse) {
				t.Errorf("the refusal does not name the rule that fired: %v", err)
			}
			if stdin.read {
				t.Errorf("a refused plan was asked about instead of refused: %v", err)
			}
			rec := oneRecord(t, w, managedmode.AuditRefused)
			if !strings.Contains(strings.Join(rec.Rules, "; "), tc.refuse) {
				t.Errorf("the refused record does not name the rule that fired: %v", rec.Rules)
			}
			// The admission call after a refusal writes nothing: one verdict,
			// one record, and never a second object for a build that did not
			// happen.
			if err := m.recorded(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := w.audits(t); len(got) != 1 {
				t.Errorf("a refused plan left %d records after recorded was called, want the refusal alone", len(got))
			}
			if buildPosts(fb) != 0 {
				t.Errorf("a refused plan reached Cloud Build %d times", buildPosts(fb))
			}
		})
	}
}

// The repository rule is the command's own guard restated, and it is stated
// over canonical forms for the same reason that guard is (task.CanonicalRepo):
// `--repo ACME/App` and `--repo acme/app.git` are the checkout's origin, so a
// rule comparing the raw strings would refuse a build the command accepted a
// moment earlier. The mutation this catches is the raw comparison, which is
// invisible to every other test here — they all spell the two the same way.
func TestImageBuildManagedAcceptsACanonicalRepoSpelling(t *testing.T) {
	for _, repo := range []string{"ACME/App", "acme/app.git", "acme/APP.git"} {
		t.Run(repo, func(t *testing.T) {
			_, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
			w := managedBuildWorld(t, f, managedBuildMarker(1))
			noAgentSession(t)
			lc := fixtureConfig(t)
			m := catalogBuildGate(t, w, lc)
			// The catalog the command reads: the fixture states its own, so
			// the keep rule answers with it and no cloud is asked (D1140).
			cat := lc.ImageCatalogEffective(nil)
			if cat == nil {
				t.Fatal("the fixture's own catalog is not in its config")
			}
			plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha")
			plan.Repo = repo
			cmd := gateCmd()
			cmd.SetIn(&unreadStdin{})

			err := m.admitEntry(t.Context(), cmd, initflow.Conditions{Terminal: true},
				cat, config.Entry{Name: "alpha", Source: config.Source{Kind: config.SourceInline}}, mustSlug("bitbucket", "acme/app"), plan)
			if err != nil {
				t.Fatalf("--repo %s is this checkout's origin and was refused: %v", repo, err)
			}
			// The record is the build's, written after the submission that is
			// the write (recorded): this call stands in for it.
			if err := m.recorded(t.Context()); err != nil {
				t.Fatal(err)
			}
			oneRecord(t, w, managedmode.AuditAdmitted)
		})
	}
}

// The same rule through the command, which is where the finding lived: the
// command's own guard compares canonical forms, so `--repo ACME/App` is this
// checkout's origin and the build is submitted — and a rule stated over the raw
// strings refused it a moment later, on a managed project only. The person at
// that terminal read a refusal for a command that had prompted and worked
// before, which is the false refusal this pins.
func TestImageBuildManagedAcceptsACanonicalRepoSpellingAtTheCommand(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	noAgentSession(t)
	fakeTerminal(t)
	lc := fixtureConfig(t)
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "alpha", "--repo", "ACME/App", "--base", "b:1")
	if err != nil {
		t.Fatalf("--repo ACME/App is this checkout's origin: %v\n%s%s", err, out, errOut)
	}
	assertNotAsked(t, errOut, catalogBuildPlan(t, lc, []string{"alpha"}, "alpha").What)
	if stdin.read {
		t.Error("a managed project's own terminal was asked to type the project's name")
	}
	if buildPosts(fb) != 1 {
		t.Errorf("submitted %d builds, want the one that was admitted", buildPosts(fb))
	}
	oneRecord(t, w, managedmode.AuditAdmitted)
}

// A coding agent's session on the same managed project is the session the
// removed refusal was written for, and its removal is the whole point of the
// spec's §4 ("agents can run image builds"): the build is submitted, nothing is
// asked, and the gate — which judges a person at a terminal alone — writes no
// record of it.
//
// The record's absence is named rather than hidden: it is what T9's audit-only
// gate (D1545) is about, and T4's PR body carries it as a finding. The mutation
// this catches is the agent refusal coming back (the build is not submitted),
// which is the behaviour the policy deletes.
func TestImageBuildManagedBuildsInAnAgentSessionUnrecorded(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	fakeTerminal(t)
	t.Setenv(agentMarkers[0], "1")
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "alpha", "--base", "b:1")
	if err != nil {
		t.Fatalf("exit %d, err %v: a coding agent's session runs this build like any other session", ExitCode(err), err)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("submitted %d builds, want the one the agent asked for (%s)", buildPosts(fb), out)
	}
	if stdin.read {
		t.Error("a coding agent's session was asked a question it can never answer")
	}
	if strings.Contains(errOut, initflow.GateProceeded()) || strings.Contains(errOut, "Type ") {
		t.Errorf("a session the gate does not stand in for read a confirmation:\n%s", errOut)
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a session the gate does not stand in for left %d records (%+v)", len(got), got)
	}
}

// The derived arm — `--workflow`, and a checkout with no catalog — is written by
// no row of the managed gate's (D1206 writes the catalog entry alone), so
// nothing about it is judged or recorded; T4 removed the prompt this path used
// to print, so the person at the SAME managed terminal now gets the build they
// asked for with no note and no typed name. The mutation this catches is a
// confirmation left in image.go's own path: the build would not be submitted
// without a name.
func TestImageBuildManagedDerivedArmAsksNothing(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	noAgentSession(t)
	fakeTerminal(t)
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if stdin.read {
		t.Errorf("the derived arm asked a question:\n%s", errOut)
	}
	if strings.Contains(errOut, "Type ") || strings.Contains(errOut, initflow.GateProceeded()) {
		t.Errorf("the derived arm printed a confirmation no row of the gate's covers:\n%s", errOut)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("submitted %d builds, want the one the person asked for\n%s%s", buildPosts(fb), out, errOut)
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a build no row of the gate's covers left %d records (%+v)", len(got), got)
	}
}

// The other half of the door: an installation whose marks do not say managed is
// not managed, and its person at the same terminal now builds exactly what an
// unmanaged installation's person has always built — nothing is asked, nothing
// is judged and nothing is recorded (D1025: a record is a managed project's own).
func TestImageBuildUnmanagedAsksNothing(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := unmanagedBuildWorld(t, f)
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	noAgentSession(t)
	fakeTerminal(t)
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "alpha", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if stdin.read {
		t.Errorf("a project that is not managed asked a question:\n%s", errOut)
	}
	if strings.Contains(errOut, "Type ") || strings.Contains(errOut, initflow.GateProceeded()) {
		t.Errorf("a project that is not managed read a confirmation:\n%s", errOut)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("submitted %d builds, want the one that was asked for\n%s%s", buildPosts(fb), out, errOut)
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a project that is not managed left %d records (%+v)", len(got), got)
	}
}

// A mark that cannot be read is not a permission, and it is not a question
// either: the read fails, the door answers "the gate did not judge this", and
// the build is submitted with no verdict and no record rather than met with an
// error about the installation. The marks are all in place — what fails is the
// endpoint they are read through.
func TestImageBuildManagedBuildsWhatAMarkItCannotRead(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	rewriteConfig(t, "storage: "+w.gcs.URL+"/storage/v1/", "storage: "+deadEndpoint(t)+"/storage/v1/")
	noAgentSession(t)
	fakeTerminal(t)
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "alpha", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if stdin.read {
		t.Errorf("a mark that could not be read was answered with a question:\n%s", errOut)
	}
	if strings.Contains(errOut, "Type ") || strings.Contains(errOut, initflow.GateProceeded()) {
		t.Errorf("a gate the read never opened printed a confirmation:\n%s", errOut)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("the build behind a mark that could not be read was submitted %d times, want one", buildPosts(fb))
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a gate the read never opened left %d records (%+v)", len(got), got)
	}
}
