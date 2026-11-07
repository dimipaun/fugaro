package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// image_build_managed.go's tests: a person at their own terminal in a managed
// project builds an image without typing the project's name (NP3, the owner's
// ruling of 2026-11-06).
//
// What is pinned here, and why each one is a test rather than a comment. The
// typed prompt this door stands in for is the only thing between a person and a
// billable Cloud Build, so "the person was asked nothing" is a claim about an
// ABSENCE — and a command that asked and a command that did not both submit one
// build in a test that only watched the fake's request count. Three things say
// it instead: the reader the person's own stdin is handed to is never reached
// (unreadStdin fails the test when anything reads it), the sentence the prompt
// prints is nowhere in what the person read, and what the person read is the
// gate's own line. The other half is that nothing else moved: a coding agent's
// session on the same project is refused exactly as it was, a project that is
// not managed is asked exactly as it always was and records nothing, and a plan
// this door's rules refuse is refused rather than turned into a prompt.
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
	db  *gcpfake.RTDB
	s   *storage.Service
}

// managedBuildMarker is the creation fact of the managed installation these
// tests build in: the version this build knows, naming the GCP project the read
// is asked about.
func managedBuildMarker() []byte {
	return []byte(`{"version":1,"gcp_project":"` + initProject + `"}`)
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
func managedBuildWorld(t *testing.T, f *cloudFixture) *buildWorld {
	t.Helper()
	isolateGateCredentials(t)
	gcs, c, db := gcpfake.NewGCS(t), gcpfake.NewCRM(t), gcpfake.NewRTDB(t)
	// Every endpoint infra.NewClients insists on with no_auth (fakes) has to be
	// here: one left empty would send a call to Google itself.
	iam, ar, sch := gcpfake.NewIAM(t), gcpfake.NewArtifactRegistry(t), gcpfake.NewScheduler(t)
	gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	gcs.Put(initRunsBucket, managed.MarkerObject, managedBuildMarker())
	c.AddProject(initProject, initProjectNumber)
	labelled(c, initProject)
	setConfigScalar(t, "runs_bucket", initRunsBucket)
	rewriteConfig(t, "no_auth: true }",
		"storage: "+gcs.URL+"/storage/v1/, resource_manager: "+c.URL+"/, iam: "+iam.URL+
			"/, artifact_registry: "+ar.URL+"/, cloud_scheduler: "+sch.URL+"/, no_auth: true }")
	f.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+db.URL+" }\n")
	return &buildWorld{gcs: gcs, crm: managedCRM(t, c), db: db, s: managedStorage(t, gcs)}
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
	return &buildWorld{gcs: gcs, crm: managedCRM(t, c), db: db, s: managedStorage(t, gcs)}
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

// buildHostAndAccount are the two values every arm states its build with: the
// installation's own registry host, and the build account infra.Repo derives for
// the fixture's repository (acme/app on Bitbucket) in this project.
func buildHostAndAccount(t *testing.T, lc *localcfg.Config) (host, account string) {
	t.Helper()
	host, err := infra.RegistryHost(lc)
	if err != nil {
		t.Fatal(err)
	}
	return host, gcp.BuildServiceAccountID(mustSlug("bitbucket", "acme/app")) + "@" + initProject + ".iam.gserviceaccount.com"
}

// workflowOf is the workflow name and base kind the fixture's fugaro.yaml
// declares, which is what the per-workflow arm builds when the command names
// neither: the fixture declares one, so there is exactly one answer.
func workflowOf(t *testing.T) (name, kind string) {
	t.Helper()
	cfg, problems := config.Parse([]byte(bitbucketYAML))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	for n, w := range cfg.Workflows {
		return n, w.BaseKind()
	}
	t.Fatal("the fixture's fugaro.yaml declares no workflow")
	return "", ""
}

// derivedBuildPlan is the document the per-workflow arm judges and records,
// spelled out as the test's own value rather than taken from the command: the
// record's digest is checked against a document this file built, so a command
// that judged one plan and acted on another — or that recorded a digest of
// something else — fails here.
func derivedBuildPlan(t *testing.T, lc *localcfg.Config, workflow, kind string) buildPlan {
	t.Helper()
	host, account := buildHostAndAccount(t, lc)
	registry := host + "/" + gcp.RegistryRepoID(mustSlug("bitbucket", "acme/app"))
	return buildPlan{
		Repo: "acme/app", Origin: "acme/app", Workflow: workflow, Kind: kind, Base: "b:1",
		Account: account, Registry: registry, Machine: lc.Build.MachineType,
		What: cloudBuildBanner("acme/app", workflow, lc.Build.MachineType, account, registry),
	}
}

// catalogBuildPlan is the same for the catalog arm: the entries the invocation
// builds, and the installation's one shared registry a catalog image is pushed
// into (D1152).
func catalogBuildPlan(t *testing.T, lc *localcfg.Config, entries []string) buildPlan {
	t.Helper()
	host, account := buildHostAndAccount(t, lc)
	registry := host + "/" + gcp.SharedRegistryRepoID()
	named := make([]config.Entry, len(entries))
	for i, e := range entries {
		named[i] = config.Entry{Name: e}
	}
	return buildPlan{
		Repo: "acme/app", Origin: "acme/app", Entries: entries, Kind: config.BaseKind, Base: "b:1",
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
// the one printed. It answers the day's one record, so each test goes on to say
// what it is about.
func assertNotAsked(t *testing.T, errOut string, w *buildWorld, want string) managedmode.AuditRecord {
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
	got := w.audits(t)
	if len(got) != 1 {
		t.Fatalf("the admitted build left %d records, want exactly one (a command that never called gate.recorded leaves none): %+v", len(got), got)
	}
	if got[0].Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q", got[0].Verdict, managedmode.AuditAdmitted)
	}
	return got[0]
}

// The red step of this task. A person at their own real terminal, in a managed
// project, runs `fugaro image build`: nothing is typed, the sentence the typed
// prompt prints is not printed, the verdict's own line is, one build is
// submitted and the project's audit holds one admission of the document the
// person read.
//
// It failed before this task for the reason the task exists — every arm of this
// command asked for the project's name typed at a terminal (confirmBuild), and
// in a managed project that is the one thing the ruling says never happens.
//
// The mutations this catches are the tempting ones: a confirm that ignores the
// gate (the prompt returns, the reader is reached, the first assertion fails), a
// recorded that is never called (no record), and a plan whose digest is computed
// over anything but the document the person was shown.
func TestImageBuildManagedAsksNothingAtTheTerminal(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	w := managedBuildWorld(t, f)
	noAgentSession(t)
	fakeTerminal(t)
	lc := fixtureConfig(t)
	workflow, kind := workflowOf(t)
	stdin := &unreadStdin{}

	out, errOut, err := executeStdinReader(t, stdin, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if stdin.read {
		t.Errorf("a managed project's own terminal was read, so a question was asked:\n%s", errOut)
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("the managed project's build was submitted %d times, want one\n%s%s", buildPosts(fb), out, errOut)
	}
	plan := derivedBuildPlan(t, lc, workflow, kind)
	rec := assertNotAsked(t, errOut, w, plan.What)
	want := planDigest(t, plan)
	if rec.PlanDigest != want {
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
		"the build is this checkout's own repository (acme/app)",
		"the base image is not another project's pull-through reference (b:1)",
		"the build runs as this project's own build account",
		"the build pushes into this installation's own registry",
	} {
		if !strings.Contains(rules, rule) {
			t.Errorf("the admitted record does not carry the rule %q: %s", rule, rules)
		}
	}
}

// The catalog arm is the same door: `fugaro image build <entry>` on a managed
// project's own terminal asks nothing, the one plan names the entry the
// invocation builds, and one record covers the invocation. The copy the build
// reads is in the bucket afterwards, which is the other half of "the build went
// ahead" — the copies are written after the confirmation, so a declined build
// would leave the bucket as it found it.
func TestImageBuildManagedCatalogAsksNothingAtTheTerminal(t *testing.T) {
	fb, gcs, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f)
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
	plan := catalogBuildPlan(t, lc, []string{"alpha"})
	rec := assertNotAsked(t, errOut, w, plan.What)
	want := planDigest(t, plan)
	if rec.PlanDigest != want {
		t.Errorf("the record's plan_digest is %q, want the sha256 of the catalog plan the person was shown (%q)", rec.PlanDigest, want)
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

// A plan this door's own rules refuse is refused, and never turned into a
// prompt: a person's typed name is not the answer to a document a rule refused,
// and nothing is submitted.
//
// The plan is refused through the gate and not through a stub — the account it
// names is another project's, which is the state buildRules refuses at the
// moment of the write — so this pins the whole refusal path: the error names the
// rule, the day holds one REFUSED record and no admission, and a later recorded
// call writes nothing (the verdict was spent by the refusal itself).
func TestImageBuildManagedRefusesAPlanItsRulesRefuse(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	w := managedBuildWorld(t, f)
	noAgentSession(t)
	lc := fixtureConfig(t)

	_, db, err := budgetDBFor(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := readManagedGate(t.Context(), managedGateEnv{lc: lc, s: w.s, crm: w.crm, db: db}, buildGateFacts())
	if err != nil || !gate.managed {
		t.Fatalf("both marks are in place and the gate did not open: %v (managed %v)", err, gate.managed)
	}
	m := &buildManaged{lc: lc, gate: &gate}
	workflow, kind := workflowOf(t)
	plan := derivedBuildPlan(t, lc, workflow, kind)
	plan.Account = "builder@another-project.iam.gserviceaccount.com"
	cmd := gateCmd()
	stdin := &unreadStdin{}
	cmd.SetIn(stdin)

	err = m.confirm(t.Context(), cmd, initflow.Conditions{Terminal: true}, plan)
	if err == nil {
		t.Fatal("a plan whose build account is another project's was admitted")
	}
	if !strings.Contains(err.Error(), "this project's own build account") {
		t.Errorf("the refusal does not name the rule that fired: %v", err)
	}
	if stdin.read {
		t.Errorf("a refused plan was asked about instead of refused: %v", err)
	}
	got := w.audits(t)
	if len(got) != 1 || got[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the day holds %d records (%+v), want the refusal alone", len(got), got)
	}
	if rules := strings.Join(got[0].Rules, "; "); !strings.Contains(rules, "this project's own build account") {
		t.Errorf("the refused record does not name the rule that fired: %s", rules)
	}
	// The admission call after a refusal writes nothing: one verdict, one
	// record, and never a second object for a build that did not happen.
	if err := m.recorded(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.audits(t); len(got) != 1 {
		t.Errorf("a refused plan left %d records after recorded was called, want the refusal alone", len(got))
	}
	if buildPosts(fb) != 0 {
		t.Errorf("a refused plan reached Cloud Build %d times", buildPosts(fb))
	}
}

// A coding agent's session on the same managed project is refused exactly as it
// has always been, before the local config, the bucket or any mark is touched:
// the refusal this door stands beside is buildNotConfirmable's, and nothing here
// moves a session that cannot type a project's name.
func TestImageBuildManagedStillRefusesAnAgentSession(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	w := managedBuildWorld(t, f)
	fakeTerminal(t)
	t.Setenv(agentMarkers[0], "1")
	stdin := &unreadStdin{}

	_, _, err := executeStdinReader(t, stdin, "image", "build", "--base", "b:1")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: a coding agent's session must be refused", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), agentMarkers[0]+" is set") {
		t.Errorf("the refusal does not name the marker the session carries: %v", err)
	}
	if buildPosts(fb) != 0 {
		t.Error("a coding agent's session submitted a build")
	}
	if stdin.read {
		t.Error("a coding agent's session was asked a question it can never answer")
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a session that was never admitted left %d records (%+v)", len(got), got)
	}
}

// The other half of the door: an installation whose marks do not say managed is
// not managed, and its person at the same terminal is asked exactly what this
// command has always asked — the project's name, read from their own stdin. That
// name submits the build and anything else declines it, and nothing is recorded
// either way (D1025: a record is a managed project's own).
func TestImageBuildUnmanagedStillAsksAtTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stdin  string
		builds int
	}{
		{"the project's name typed", initProjectName + "\n", 1},
		{"nothing typed", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb, f := cloudBuildCheckout(t, false)
			w := unmanagedBuildWorld(t, f)
			noAgentSession(t)
			fakeTerminal(t)

			out, errOut, err := executeStdin(t, tc.stdin, "image", "build", "--base", "b:1")
			if !strings.Contains(errOut, "Type "+initProjectName+" to submit the build: ") {
				t.Fatalf("a project that is not managed did not read the prompt this command has always printed:\n%s%s", out, errOut)
			}
			if strings.Contains(errOut, initflow.GateProceeded()) {
				t.Errorf("a project that is not managed read the gate's own sentence:\n%s", errOut)
			}
			if buildPosts(fb) != tc.builds {
				t.Errorf("submitted %d builds, want %d\n%s%s", buildPosts(fb), tc.builds, out, errOut)
			}
			if tc.builds == 0 && (err == nil || !strings.Contains(err.Error(), "not confirmed")) {
				t.Errorf("a declined build did not say so: %v", err)
			}
			if got := w.audits(t); len(got) != 0 {
				t.Errorf("a project that is not managed left %d records (%+v)", len(got), got)
			}
		})
	}
}

// A mark that cannot be read is not a permission: the read fails, the door
// answers "the gate did not admit this", and the person is asked the question
// they have always been asked rather than met with an error about the
// installation. The marks are all in place — what fails is the endpoint they are
// read through.
func TestImageBuildManagedAnswersAMarkItCannotReadWithTheTypedName(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	w := managedBuildWorld(t, f)
	rewriteConfig(t, "storage: "+w.gcs.URL+"/storage/v1/", "storage: "+deadEndpoint(t)+"/storage/v1/")
	noAgentSession(t)
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, initProjectName+"\n", "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "Type "+initProjectName+" to submit the build: ") {
		t.Errorf("a mark that could not be read was taken for a permission:\n%s", errOut)
	}
	if buildPosts(fb) != 1 {
		t.Errorf("the typed name did not submit the build (%d posts)", buildPosts(fb))
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a gate the read never opened left %d records (%+v)", len(got), got)
	}
}
