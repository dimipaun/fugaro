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

// image_build_managed.go's tests: a person at their own terminal in a managed
// project builds a CATALOG ENTRY without typing the project's name (NP3, the
// owner's ruling of 2026-11-06), under the row D1206 writes for this command —
// the entry exists in the effective catalog, the build account is the project's
// own for that entry's source repository, and the count is within D1013's
// max_image_builds bound, with the project's kill switch read beside it before
// every build.
//
// What is pinned here, and why each one is a test rather than a comment. The
// typed prompt this door stands in for is the only thing between a person and a
// billable Cloud Build, so "the person was asked nothing" is a claim about an
// ABSENCE — and a command that asked and a command that did not both submit one
// build in a test that only watched the fake's request count. Three things say
// it instead: the reader the person's own stdin is handed to is never reached
// (unreadStdin fails the test when anything reads it), the sentence the prompt
// prints is nowhere in what the person read, and what the person read is the
// gate's own line.
//
// The other half is everything that did NOT move. The derived arm — --workflow,
// or a checkout with no catalog — is written by no row of the managed gate's,
// and neither is the converge's own first build of a repository
// (docs/managed-agent-converge/spec.md §3.1: a billable build's prompt is taken
// away only where the design gave that path a document AND a bound), so a
// person at the same managed terminal is still asked for the name there. A
// coding agent's session is refused exactly as it was, a project that is not
// managed is asked exactly as it always was and records nothing, a mark that
// cannot be read is answered with the typed name rather than taken for a
// permission, and a build the row's rules refuse is refused rather than turned
// into a prompt.
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

// managedBuildMarker is the creation fact of a managed installation that
// records a bound on the billable builds one invocation may start: the
// version-3 object M5 reads (managedRefreshMarker's shape, this fixture's own
// project), which is the only version that carries one.
func managedBuildMarker(bound int64) []byte {
	return []byte(fmt.Sprintf(`{"version":3,"gcp_project":%q,"budget":{"amount":5000,"currency":"USD"},"max_image_builds":%d}`, initProject, bound))
}

// managedBuildMarkerUnknownVersion is an object written by a Fugaro newer than
// this one: a bound no read of this program's can state, which is a build no
// verdict can be reached about.
func managedBuildMarkerUnknownVersion() []byte {
	return []byte(`{"version":9,"gcp_project":"` + initProject + `","budget":{"amount":5000,"currency":"USD"},"max_image_builds":1}`)
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
	return &buildWorld{gcs: gcs, crm: managedCRM(t, c), crmFake: c, db: db, s: managedStorage(t, gcs)}
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
// (D1152), with the three facts D1013 re-reads before each build.
func catalogBuildPlan(t *testing.T, lc *localcfg.Config, entries []string, entry string, bound, started int64) buildPlan {
	t.Helper()
	host, account := buildHostAndAccount(t, lc)
	registry := host + "/" + gcp.SharedRegistryRepoID()
	named := make([]config.Entry, len(entries))
	for i, e := range entries {
		named[i] = config.Entry{Name: e}
	}
	return buildPlan{
		Repo: "acme/app", Origin: "acme/app", Entry: entry, Entries: entries,
		Kind: config.BaseKind, Base: "b:1", Bound: bound, Started: started,
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
	return &buildManaged{lc: lc, gate: &gate, s: w.s, crm: w.crm, db: db}
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

// The red step of this task. A person at their own real terminal, in a managed
// project, runs `fugaro image build <entry>`: nothing is typed, the sentence the
// typed prompt prints is not printed, the verdict's own line is, one build is
// submitted and the project's audit holds one admission of the document the
// person read — the document that carries the entry, the bound in force and the
// count this build was judged against.
//
// It failed before this task for the reason the task exists: every arm of this
// command asked for the project's name typed at a terminal (confirmBuild), and
// in a managed project that is the one thing the ruling says never happens.
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
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha", 1, 0)
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
		"the build is within the bound the project's marker records (1 per invocation, 0 already submitted)",
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

// D1206's third clause, and D1013's own count: the project's creation fact
// records one build per invocation, so a whole-catalog build of three entries
// submits the first, and the one after it is refused with the bound's own
// sentence rather than submitted. The count is what makes the second entry's
// judgement different from the first's, which is what "the count is within the
// bound" is about.
//
// The two records are the other half: one admission of the build that ran and
// one refusal of the build that did not, each of its own document — a command
// that filed one verdict for the whole invocation would leave one record with
// three builds behind it.
func TestImageBuildManagedStopsAtTheRecordedBound(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	noAgentSession(t)
	fakeTerminal(t)

	out, errOut, err := executeStdinReader(t, &unreadStdin{}, "image", "build", "--base", "b:1")
	if err == nil {
		t.Fatalf("the run went past the bound the project's marker records\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "past the bound the project's marker records") || !strings.Contains(err.Error(), "max_image_builds") {
		t.Errorf("the refusal does not name the bound that stopped it:\n%v", err)
	}
	// And it names the way past it that exists: the bound is per INVOCATION,
	// so naming the entry is what builds the next one. The two ways the
	// sentence used to offer are not commands this program has -- nothing
	// writes a marker with another bound, and a project that is not managed
	// never reaches this line -- so a refusal that offered either would be a
	// dead end.
	if !strings.Contains(err.Error(), "one entry per invocation") {
		t.Errorf("the refusal does not tell the person how to build the rest:\n%v", err)
	}
	for _, dead := range []string{"not managed", "a larger max_image_builds"} {
		if strings.Contains(err.Error(), dead) {
			t.Errorf("the refusal offers %q, which is no path a command can take:\n%v", dead, err)
		}
	}
	if buildPosts(fb) != 1 {
		t.Fatalf("submitted %d builds against a bound of one, want the first alone\n%s%s", buildPosts(fb), out, errOut)
	}
	if !strings.Contains(errOut, initflow.GateProceeded()) {
		t.Errorf("the admitted build did not print the gate's own sentence:\n%s", errOut)
	}
	got := w.audits(t)
	if len(got) != 2 {
		t.Fatalf("the day holds %d records, want the admission and the refusal (%+v)", len(got), got)
	}
	verdicts := map[managedmode.AuditVerdict]int{}
	for _, r := range got {
		verdicts[r.Verdict]++
	}
	if verdicts[managedmode.AuditAdmitted] != 1 || verdicts[managedmode.AuditRefused] != 1 {
		t.Errorf("the day's verdicts are %v, want one admission and one refusal", verdicts)
	}
}

// The gate re-reads the marks immediately before the write it is about to make
// (D1024), and this is what that second read is FOR: a project whose owner
// unflags it between the door's own read and the build's admission is not a
// managed project any more, so the gate has no verdict to give and the typed
// question comes back exactly where it was.
//
// The window is made reachable rather than assumed. The one read between the
// bound and the gate's second look at the marks is the project's kill switch,
// so the fake holds that request in flight (HoldNext) while the owner takes the
// label away and then lets it go: the gate's admission is decided on marks that
// changed inside the window, which is the case the second read exists for and
// the case no other world in this file builds — they all leave the marks as the
// door found them.
//
// The mutation this catches is the fallback answering "admitted" (returning nil
// at the fallback): a project revoked in that window would then have its build
// submitted with no confirmation at all, which is the one thing the second read
// prevents — and no other test here would notice, because every other managed
// world leaves the marks in place from the first read to the last.
//
// Nothing is recorded either way: the gate writes no record for a session it
// does not stand in for, and an admission's record is written after the
// submission (recorded), which the typed fallback has not made.
func TestImageBuildManagedRevokedMidCommandAsksForTheTypedName(t *testing.T) {
	_, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	noAgentSession(t)
	lc := fixtureConfig(t)
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha", 1, 0)

	for _, tc := range []struct {
		name  string
		stdin string
		want  string
	}{
		{name: "the project's name typed", stdin: initProjectName + "\n"},
		{name: "nothing typed", stdin: "\n", want: "not confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The world the door read, one gate per subtest, so the revocation
			// each case makes is the only one between the two reads.
			labelled(w.crmFake, initProject)
			m := catalogBuildGate(t, w, lc)
			stamped, release := w.db.HoldNext(budget.PathKillGlobal)

			cmd := gateCmd()
			out := &strings.Builder{}
			cmd.SetOut(out)
			cmd.SetErr(out)
			stdin := &readSpy{Reader: strings.NewReader(tc.stdin)}
			cmd.SetIn(stdin)
			done := make(chan error, 1)
			go func() {
				done <- m.admitEntry(t.Context(), cmd, initflow.Conditions{Terminal: true},
					directRulesCatalog("alpha"), config.Entry{Name: "alpha", Source: config.Source{Kind: config.SourceInline}},
					mustSlug("bitbucket", "acme/app"), plan)
			}()
			select {
			case <-stamped:
			case <-time.After(10 * time.Second):
				t.Fatal("the admission never read the project's kill switch, so the door was never reached")
			}
			w.crmFake.SetLabels(initProject, map[string]string{managed.ProjectLabelKey: "no"})
			release()
			err := <-done

			if !strings.Contains(out.String(), "Type "+initProjectName+" to submit the build: ") {
				t.Errorf("a project whose marks were taken away mid-command was not asked for the project's name:\n%s", out)
			}
			if !stdin.read {
				t.Error("the prompt was printed and nothing read the person's answer")
			}
			if tc.want == "" {
				if err != nil {
					t.Fatalf("the project's own name was typed and the build was still refused: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("a name that is not the project's left err %v, want the refusal %q", err, tc.want)
			}
			if got := w.audits(t); len(got) != 0 {
				t.Errorf("a session the gate does not stand in for left %d records (%+v)", len(got), got)
			}
		})
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
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha", 3, 0)
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

// The bound's read is the one thing in D1206's row that is an error rather than
// a refusal when it answers nothing: a creation fact this program does not
// write is a bound no verdict can be reached about, so no document is judged,
// no record is written, and nothing is submitted.
//
// The fact is replaced after the gate's read, which is the window D1013's own
// re-read exists for: an owner who replaces the project's creation fact while a
// command runs means the build after that to stop, and the door's read is not
// what the next build is judged against.
func TestImageBuildManagedUnreadableBoundStops(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(3))
	noAgentSession(t)
	lc := fixtureConfig(t)
	m := catalogBuildGate(t, w, lc)
	w.gcs.Put(initRunsBucket, managed.MarkerObject, managedBuildMarkerUnknownVersion())
	plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha", 3, 0)
	cmd := gateCmd()
	cmd.SetIn(&unreadStdin{})

	err := m.admitEntry(t.Context(), cmd, initflow.Conditions{Terminal: true}, directRulesCatalog("alpha"),
		config.Entry{Name: "alpha", Source: config.Source{Kind: config.SourceInline}}, mustSlug("bitbucket", "acme/app"), plan)
	if err == nil {
		t.Fatal("a build was judged against a bound nothing could read")
	}
	if !strings.Contains(err.Error(), "bound on this project's billable image builds was not read") {
		t.Errorf("the run stopped without saying which read failed:\n%v", err)
	}
	if buildPosts(fb) != 0 {
		t.Errorf("submitted %d builds against a bound nothing could read", buildPosts(fb))
	}
	if got := w.audits(t); len(got) != 0 {
		t.Errorf("a document no verdict could be reached about left %d records (%+v)", len(got), got)
	}
}

// directRulesCatalog is the catalog the direct-admission tests state their
// rules over: the entry they judge is in it, so the rule that fires is the one
// each test is about — the bound, the switch — and not the catalog's own.
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
			plan := catalogBuildPlan(t, lc, []string{"alpha"}, tc.entry.Name, 1, 0)
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
			plan := catalogBuildPlan(t, lc, []string{"alpha"}, "alpha", 1, 0)
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
	assertNotAsked(t, errOut, catalogBuildPlan(t, lc, []string{"alpha"}, "alpha", 1, 0).What)
	if stdin.read {
		t.Error("a managed project's own terminal was asked to type the project's name")
	}
	if buildPosts(fb) != 1 {
		t.Errorf("submitted %d builds, want the one that was admitted", buildPosts(fb))
	}
	oneRecord(t, w, managedmode.AuditAdmitted)
}

// A coding agent's session on the same managed project is refused exactly as it
// has always been, before the local config, the bucket or any mark is touched:
// the refusal this door stands beside is catalogBuildNotConfirmable's, and
// nothing here moves a session that cannot type a project's name.
func TestImageBuildManagedStillRefusesAnAgentSession(t *testing.T) {
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	fakeTerminal(t)
	t.Setenv(agentMarkers[0], "1")
	stdin := &unreadStdin{}

	_, _, err := executeStdinReader(t, stdin, "image", "build", "alpha", "--base", "b:1")
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

// The derived arm is the other half of the row: `--workflow`, and a checkout
// with no catalog, are written by no row of the managed gate's (D1206 writes
// the catalog entry alone, and the design's boundary is that a billable build's
// prompt goes away only where that path got a document AND a bound), so the
// person at the SAME managed terminal is asked for the project's name exactly
// as they always were. The mutation this catches is a door opened for the whole
// command rather than for the catalog arm: the managed world below would stop
// asking, and the build would be submitted without a name.
func TestImageBuildManagedDerivedArmStillAsksAtTheTerminal(t *testing.T) {
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
			w := managedBuildWorld(t, f, managedBuildMarker(1))
			noAgentSession(t)
			fakeTerminal(t)

			out, errOut, err := executeStdin(t, tc.stdin, "image", "build", "--base", "b:1")
			if !strings.Contains(errOut, "Type "+initProjectName+" to submit the build: ") {
				t.Fatalf("the derived arm is not a row of the managed gate's, and its person was not asked (%v):\n%s%s", err, out, errOut)
			}
			if strings.Contains(errOut, initflow.GateProceeded()) {
				t.Errorf("the derived arm printed the gate's own sentence:\n%s", errOut)
			}
			if buildPosts(fb) != tc.builds {
				t.Errorf("submitted %d builds, want %d\n%s%s", buildPosts(fb), tc.builds, out, errOut)
			}
			if tc.builds == 0 && (err == nil || !strings.Contains(err.Error(), "not confirmed")) {
				t.Errorf("a declined build did not say so: %v", err)
			}
			if got := w.audits(t); len(got) != 0 {
				t.Errorf("a build no row of the gate's covers left %d records (%+v)", len(got), got)
			}
		})
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
			fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
			w := unmanagedBuildWorld(t, f)
			stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
			noAgentSession(t)
			fakeTerminal(t)

			out, errOut, err := executeStdin(t, tc.stdin, "image", "build", "alpha", "--base", "b:1")
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
	fb, _, f := catalogBuildCheckout(t, bitbucketYAML, catalogBuildThreeYAML)
	w := managedBuildWorld(t, f, managedBuildMarker(1))
	stubCatalogProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})
	rewriteConfig(t, "storage: "+w.gcs.URL+"/storage/v1/", "storage: "+deadEndpoint(t)+"/storage/v1/")
	noAgentSession(t)
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, initProjectName+"\n", "image", "build", "alpha", "--base", "b:1")
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
