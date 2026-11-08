package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// image_refresh_managed.go's tests: `fugaro image refresh` under the managed
// gate (M4, D1011, D1013, D1014, D1022, D1025).
//
// What is pinned here, and why each one is a test. The command is in the gate's
// set for one reason the other commands do not have — it applies no Terraform,
// so there is no plan to judge, and what it spends instead is a COUNT of
// billable Cloud Builds — so the properties are: the bound the project's
// creation fact records is what bounds one invocation, the count and the kill
// switch are re-read before EACH build rather than once per command (D1013), a
// mark that cannot be read is not a permission (the fail-closed direction of
// every read in internal/infra/managed), a run that asks nothing pays nothing for
// any of it, and every admission leaves a record of the document that was
// judged (D1014, D1022) while a build that did not run leaves none.
//
// Every world here is hermetic: internal/gcpfake's GCS, Resource Manager, RTDB
// and the rig's own build-record map, t.TempDir() for every file, and the
// credentials read off a path in that directory so a developer's own
// Application Default Credentials can never decide what a record says. No test
// contacts a cloud, and none needs an account.

// managedRefresh is one managed installation's refresh: the anchor rig's
// checkout, local config, fakes, engine and refresh target, with both marks of
// a managed project in place (the runs bucket's fugaro/managed.json and the
// GCP label), the project's budget database open beside them, and the coding
// agent's own marker set — the session this whole gate exists for.
type managedRefresh struct {
	*anchorModeRig
	db *gcpfake.RTDB
	e  *initEngine
	tg *refreshTarget
}

// managedRefreshMarker is the creation fact of a managed installation that
// records a bound on the billable builds one invocation may start: the
// version-3 object M5 reads, which is the only version that carries one.
func managedRefreshMarker(bound int64) []byte {
	return []byte(fmt.Sprintf(`{"version":3,"gcp_project":%q,"budget":{"amount":5000,"currency":"USD"},"max_image_builds":%d}`, initProject, bound))
}

// managedRefreshBudgetMarker is the version-2 object: managed, and no bound
// recorded, which is the default of one build per invocation and never "no
// bound" (D1013, spec §5).
func managedRefreshBudgetMarker() []byte {
	return []byte(`{"version":2,"gcp_project":"` + initProject + `","budget":{"amount":5000,"currency":"USD"}}`)
}

// managedRefreshUnknownVersionMarker is an object written by a Fugaro newer
// than this one: a version this program does not write, and so a fact it does
// not believe.
func managedRefreshUnknownVersionMarker() []byte {
	return []byte(`{"version":9,"gcp_project":"` + initProject + `","budget":{"amount":5000,"currency":"USD"},"max_image_builds":1}`)
}

// newManagedRefreshRig is the fixture, ready: a managed installation over the
// anchor rig's fakes, an agent's session, and the engine and target step 4
// runs with.
func newManagedRefreshRig(t *testing.T, marker []byte) *managedRefresh {
	t.Helper()
	dir := t.TempDir()
	// The credentials are isolated the way the home directory is: a file that
	// happened to be on the machine would otherwise name a real address in a
	// record, and in this test's failure output.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", dir+"/no-application-default-credentials.json")
	t.Setenv("CLOUDSDK_CONFIG", dir+"/gcloud")
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })
	oldNow := managedGateNow
	managedGateNow = func() time.Time { return managedGateTestNow }
	t.Cleanup(func() { managedGateNow = oldNow })
	// The sentences a refusal and a record carry name the program: "fugaro
	// image refresh" is what the owner runs, and the test binary's own path is
	// not.
	useSelf(t)

	r := &managedRefresh{anchorModeRig: newAnchorModeRig(t, refreshYAML, true), db: gcpfake.NewRTDB(t)}
	// The installation's own database, as fugaro update --firebase-project
	// writes the pair: the ownership question the gate asks before it reads the
	// marks needs both halves, and a loopback URL is the fake's own.
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+initProject+", rtdb_url: "+r.db.URL+" }\n")
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	labelled(r.crm, initProject)
	r.gcs.Put(initRunsBucket, managed.MarkerObject, marker)
	// A coding agent's session: the one refuseRefreshHere and askBuild refuse
	// today, and the only session whose steps the gate is about.
	t.Setenv(agentMarkers[0], "1")
	r.e, r.tg = buildTarget(t, r.anchorModeRig)
	// The account the build is submitted as, as infra.Repo derives it for this
	// installation: the rig's own placeholder is another project's, which the
	// gate's own rule refuses.
	r.tg.spec.BuildServiceAccountEmail = "fugaro-build@" + initProject + ".iam.gserviceaccount.com"
	return r
}

// gateCmd is the command the gate's door is opened for, in a test: where the
// read is made is decided from this command's own stdin (openRefreshManaged),
// and stdinIsTerminal is what says whether it is a terminal (fakeTerminal for
// a person's own run, the package's own answer everywhere else).
func gateCmd() *cobra.Command { return NewRootCmd() }

// door is the gate's own read for this rig, as runImageRefresh makes it: the
// real openRefreshManaged, over the rig's config, checkout and fakes. It fails
// the test when the door stayed shut, because every test that calls it means
// the installation to be managed.
func (r *managedRefresh) door(t *testing.T) *refreshManaged {
	t.Helper()
	m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{}, &initOptions{cloud: cloudOptions{stderr: func() io.Writer { return io.Discard }}})
	if m == nil {
		t.Fatal("both marks are in place and the gate's own door stayed shut")
	}
	return m
}

// plan is step 4's plan for this rig: both workflows stale, so both are in the
// per-build list the gate judges (the list is what the agent read in the
// printed plan, D1022).
func (r *managedRefresh) plan(m *refreshManaged) *refreshPlan {
	return &refreshPlan{workflows: []string{"app", "api"}, builds: []string{"app", "api"}, ver: "0.5.1", managed: m}
}

// audits is the day's records, read back through the reader a surface uses:
// what a test asserts about is what doctor and report would print.
func (r *managedRefresh) audits(t *testing.T) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), managedStorage(t, r.gcs), initRunsBucket, managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// bothStale gives app and api a build record from an earlier release's base,
// which is what puts both into the plan's per-build list.
func bothStale(t *testing.T) {
	t.Helper()
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.4.0"))
	putWorkflowRecordFrom("api", "0.5.1", managedRef("base", "0.4.0"))
}

// boundOf is a bound as M5 hands one out rather than as a test would guess it:
// the decision over a recorded figure, so a document a test renders is rendered
// under the same bound the gate judged it under.
func boundOf(t *testing.T, n int64) managedmode.ImageBuilds {
	t.Helper()
	b, err := managedmode.ImageBuildsBound(managedmode.ImageBuildsRead{Project: initProjectName, Builds: n, OK: true, Managed: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// swappingBuilder is a Cloud Build that changes the world before it submits:
// the hook runs once, just before the first submission, so a test can change
// the creation fact or the kill switch in the window between two builds. That
// window is the whole reason admitBuild reads both before EACH build (D1013),
// and a command that read either once per command cannot tell this builder
// apart from an ordinary one.
type swappingBuilder struct {
	fakeBuilder
	swap    func()
	swapped bool
}

func (b *swappingBuilder) Submit(ctx context.Context, s gcp.BuildSpec) (gcp.BuildResult, error) {
	if !b.swapped {
		b.swapped = true
		b.swap()
	}
	return b.fakeBuilder.Submit(ctx, s)
}

func useSwapBuilder(t *testing.T, swap func()) *swappingBuilder {
	t.Helper()
	b := &swappingBuilder{swap: swap}
	old := newCloudBuilder
	newCloudBuilder = func(context.Context, *localcfg.Config) (cloudBuilder, error) { return b, nil }
	t.Cleanup(func() { newCloudBuilder = old })
	return b
}

// The red step of this task (plan M4): a marker recording one build and a plan
// naming two. The first build runs — an agent's session is the one the gate
// admits, and it was refused before the gate existed — and the second is
// refused with the owner's own command named, because the count is what bounds
// an unattended run and nothing else does.
//
// It failed before this task for the reason the task exists: `fugaro image
// refresh` refuses a coding agent's session before any step runs, so no build
// ran at all, and nothing named the bound the project records.
func TestImageRefreshManagedStopsAtTheRecordedBound(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(1))
	fb := useFakeBuilder(t)
	bothStale(t)
	p := r.plan(r.door(t))
	err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
	if err == nil {
		t.Fatal("a plan naming two builds ran to completion under a bound of one")
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Errorf("the second build was stopped by %v, want the gate's own refusal", err)
	}
	if !strings.Contains(err.Error(), "fugaro image refresh") {
		t.Errorf("the refusal does not name the command the owner runs in their own terminal instead:\n%v", err)
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" {
		t.Fatalf("submitted %d builds (%+v), want the plan's first one alone", len(fb.specs), fb.specs)
	}
	got := r.audits(t)
	if len(got) != 2 {
		t.Fatalf("the day holds %d records, want one admission and one refusal (D1014: an admission is recorded after the write, D1025: a refusal too)", len(got))
	}
	var admitted, refused int
	for _, rec := range got {
		switch rec.Verdict {
		case managedmode.AuditAdmitted:
			admitted++
			// The admission's record is the judged document's: a command that
			// submitted a build and never called gate.recorded would leave none
			// at all, and a record whose digest is not the document's would name
			// bytes nobody judged (D1022).
			doc, err := refreshBuildDocBytes(r.tg, "app", "base", p.builds, boundOf(t, 1), 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if rec.PlanDigest != gateDigest(doc) {
				t.Errorf("the admission's plan_digest is %q, want the sha256 of the first build's document (%q)", rec.PlanDigest, gateDigest(doc))
			}
		case managedmode.AuditRefused:
			refused++
			if !strings.Contains(strings.Join(rec.Rules, "; "), "past the bound") {
				t.Errorf("the refused record's rules are %q, and the rule that refused is not among them", rec.Rules)
			}
		}
	}
	if admitted != 1 || refused != 1 {
		t.Errorf("the day holds %d admissions and %d refusals, want one of each", admitted, refused)
	}
}

// The rules over one build are the plan's M4 entry, and the build account is
// one of them: an account that is not this project's own is a build this
// installation's grant does not run, and the gate refuses it before the
// submission rather than after.
func TestImageRefreshManagedRefusesABuildThatIsNotTheProjectsOwn(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(2))
	fb := useFakeBuilder(t)
	bothStale(t)
	r.tg.spec.BuildServiceAccountEmail = "builder@another-project.iam.gserviceaccount.com"
	p := r.plan(r.door(t))
	err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
	if err == nil {
		t.Fatal("a build ran under an account that is not this project's")
	}
	if !strings.Contains(err.Error(), "this project's own build account") {
		t.Errorf("the refusal does not name the rule that fired:\n%v", err)
	}
	if len(fb.specs) != 0 {
		t.Errorf("submitted %d builds under an account that is not this project's", len(fb.specs))
	}
	got := r.audits(t)
	if len(got) != 1 || got[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the day holds %d records, want the refusal alone (%+v)", len(got), got)
	}
}

// A creation that records no bound is still held to a count: a version-2 fact
// is managed, and its bound is the default of one per invocation, never "no
// bound" (D1013). The same plan as the red step runs the same way under it.
func TestImageRefreshManagedDefaultBoundIsOne(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshBudgetMarker())
	fb := useFakeBuilder(t)
	bothStale(t)
	p := r.plan(r.door(t))
	err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
	if err == nil {
		t.Fatal("a plan naming two builds ran to completion under the default bound")
	}
	if !strings.Contains(err.Error(), "1 per invocation") {
		t.Errorf("the refusal does not name the bound it stopped at:\n%v", err)
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" {
		t.Fatalf("submitted %d builds (%+v), want the plan's first one alone", len(fb.specs), fb.specs)
	}
}

// A creation fact this program does not believe is not a permission: a version
// it does not write is a project it cannot prove is managed, so the gate's door
// stays shut and the command is refused exactly as it was before the gate
// existed — the same sentence, no step run, and nothing written anywhere.
func TestImageRefreshManagedUnknownMarkerVersionRefuses(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshUnknownVersionMarker())
	fb := useFakeBuilder(t)
	bothStale(t)
	if m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{}, &initOptions{cloud: cloudOptions{stderr: func() io.Writer { return io.Discard }}}); m != nil {
		t.Fatal("a creation fact this program does not write opened the gate")
	}
	ran := fakeSteps(t, "", nil)
	_, _, err := executeStdin(t, "", "image", "refresh", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro image refresh applies cloud changes: ") {
		t.Fatalf("exit %d, err %v: want the refusal this command has always printed for a coding agent's session", ExitCode(err), err)
	}
	if len(*ran) != 0 {
		t.Errorf("steps ran for a project the gate could not read: %q", *ran)
	}
	if len(fb.specs) != 0 {
		t.Errorf("submitted %d builds for a project the gate could not read", len(fb.specs))
	}
	if got := r.audits(t); len(got) != 0 {
		t.Errorf("the day holds %d records for a project that never was managed", len(got))
	}
}

// The kill switch is read before each build too, and a switch that cannot be
// read is one this program honours rather than one it ignores (D1013): the
// build does not run, the refusal names the read that failed and the command
// that shows the owner the switch, and the refusal is recorded (D1025).
func TestImageRefreshManagedUnreadableKillSwitchStops(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(2))
	fb := useFakeBuilder(t)
	bothStale(t)
	p := r.plan(r.door(t))
	// The operator's own grant was taken away, or the rules no longer let this
	// account read the switch: the read answers 401. It is denied after the
	// door, because the ownership question the gate asks before it reads the
	// marks reads the database's root (a rule that hides one switch hides that
	// too), and this test is about the read a BUILD makes.
	r.db.DenyRead(budget.PathKillGlobal)
	err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
	if err == nil {
		t.Fatal("a build was started with the kill switch unreadable")
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Errorf("the build was stopped by %v, want the gate's own refusal", err)
	}
	if !strings.Contains(err.Error(), "kill switch could not be read") || !strings.Contains(err.Error(), "budget show") {
		t.Errorf("the refusal does not say which read failed and how the owner checks it:\n%v", err)
	}
	if len(fb.specs) != 0 {
		t.Errorf("submitted %d builds with the kill switch unreadable", len(fb.specs))
	}
	got := r.audits(t)
	if len(got) != 1 || got[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the day holds %d records, want the refusal alone (%+v)", len(got), got)
	}
	if !strings.Contains(strings.Join(got[0].Rules, "; "), "kill switch") {
		t.Errorf("the refused record's rules are %q, and the switch that refused is not among them", got[0].Rules)
	}
}

// D1013's "before each build, never once per command", for the bound: the owner
// replaces the creation fact while the first build runs, and the second build
// is judged against the fact in force then — the read fails, and the run stops
// rather than submitting a build nothing bounds. A command that kept the door's
// own read would submit the second build and pass this test's assertion of one.
func TestImageRefreshManagedReadsTheBoundBeforeEachBuild(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(2))
	fb := useSwapBuilder(t, func() {
		r.gcs.Put(initRunsBucket, managed.MarkerObject, managedRefreshUnknownVersionMarker())
	})
	bothStale(t)
	p := r.plan(r.door(t))
	err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
	if err == nil {
		t.Fatal("the run went on after the creation fact it was judging builds against became unreadable")
	}
	if !strings.Contains(err.Error(), "bound on this project's billable image builds was not read") {
		t.Errorf("the run stopped without saying which read failed:\n%v", err)
	}
	if len(fb.specs) != 1 {
		t.Fatalf("submitted %d builds, want the first alone: the bound is re-read before each one", len(fb.specs))
	}
}

// The same property for the project's kill switch: an operator stopping the
// project while a refresh runs means the build AFTER that stops, so the switch
// is read again for each build and not once for the command.
func TestImageRefreshManagedReadsTheKillSwitchBeforeEachBuild(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(2))
	fb := useSwapBuilder(t, func() {
		r.db.Set(budget.PathKillRepo(mustSlug("github", "acme/app")), map[string]any{"on": true, "reason": "the project is being stopped"})
	})
	bothStale(t)
	p := r.plan(r.door(t))
	err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
	if err == nil {
		t.Fatal("the run went on after the project's kill switch was turned on mid-run")
	}
	if !strings.Contains(err.Error(), "kill switch is on") || !strings.Contains(err.Error(), "budget resume") {
		t.Errorf("the refusal does not say which switch stopped it and how the owner lifts it:\n%v", err)
	}
	if len(fb.specs) != 1 {
		t.Fatalf("submitted %d builds, want the first alone: the switch is re-read before each one", len(fb.specs))
	}
}

// The door asks the terminal question about THIS command's own stdin. Every
// other test of this file stubs stdinIsTerminal with a function that ignores its
// argument (fakeTerminal, noTerminal), so a door that asked about another
// reader — the process's own stdin, the refresh's options — would pass all of
// them; and the reader IS the question, because the gate is read for the run
// that would have been asked, and a command wired to a different reader is a
// different run.
func TestImageRefreshManagedAsksAboutTheCommandsOwnStdin(t *testing.T) {
	noAgentSession(t)
	var seen io.Reader
	old := stdinIsTerminal
	stdinIsTerminal = func(in io.Reader) bool { seen = in; return false }
	t.Cleanup(func() { stdinIsTerminal = old })

	cmd := gateCmd()
	cmd.SetIn(strings.NewReader(""))
	openRefreshManaged(t.Context(), cmd, refreshOptions{}, &initOptions{})

	if seen == nil {
		t.Fatal("the door never asked whether this run has a terminal: the read is made where that question would have been asked")
	}
	if seen != cmd.InOrStdin() {
		t.Errorf("the door asked about %T and this command's own stdin is %T: the session the gate is read for is the command's", seen, cmd.InOrStdin())
	}
}

// The door reads --yes and --ask from the run's own options — the object this
// run's confirmations are decided from (initRun.conditions) — and not from the
// refresh's own copy of the two flags. runImageRefresh builds both from the same
// values, so today the two agree; what this pins is which of them the read
// follows, because the gate stands in for the confirmations the run's options
// decide. Both halves are here: --yes on the run's options alone keeps the door
// shut with a terminal present (the terminal is not what answers it), and --ask
// on them alone opens it with no terminal at all (the flag has to answer for
// the marks in every session that passes it).
func TestImageRefreshManagedReadsTheFlagsFromTheRunsOwnOptions(t *testing.T) {
	useVersion(t, "0.5.1")
	newManagedRefreshRig(t, managedRefreshMarker(1))
	noAgentSession(t)
	fakeTerminal(t)
	if m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{}, &initOptions{yes: true}); m != nil {
		t.Fatal("a run whose own options carry --yes took the gate's path at a terminal: --yes answers every step itself")
	}
	noTerminal(t)
	if m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{}, &initOptions{ask: true}); m == nil {
		t.Fatal("a run whose own options carry --ask read no mark: the flag has to answer for them wherever no prompt can be answered")
	}
}

// A run the gate has nothing to answer pays nothing for it (M6's review of
// 2026-11-03, 2): --yes answers every build itself, and a run with no terminal
// is refused before any step runs, so neither has a question the marks could
// answer — and neither pays for the read, which is what keeps a transient
// storage or Resource Manager failure out of a run that has no reason to make
// either call.
func TestImageRefreshManagedNoQuestionReadsNothing(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(1))
	noAgentSession(t)
	noTerminal(t)
	// A read that would fail the gate: a run that asks nothing must not make it.
	r.gcs.FailObjectGetsAfter(initRunsBucket, managed.MarkerObject, 0)
	if m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{}, &initOptions{yes: true}); m != nil {
		t.Fatal("a run with --yes took the gate's path, and --yes answers every build itself")
	}
	if m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{}, &initOptions{}); m != nil {
		t.Fatal("a run with no terminal took the gate's path, and it is refused before any step runs")
	}
	if n := len(r.gcs.Requests()); n != 0 {
		t.Errorf("a run that asks nothing made %d storage requests", n)
	}
	if n := len(r.crm.Requests()); n != 0 {
		t.Errorf("a run that asks nothing made %d Resource Manager requests", n)
	}
	// And the command keeps today's behaviour exactly: the unreadable mark does
	// not stop it, and it goes on to the refusal it has always had for a
	// directory that is not a checkout.
	ran := fakeSteps(t, "", nil)
	t.Chdir(t.TempDir())
	_, _, err := executeStdin(t, "", "image", "refresh", "--yes")
	if err == nil || !strings.Contains(err.Error(), "is not in one") {
		t.Fatalf("a run with --yes was stopped by something other than the checkout it is in: %v", err)
	}
	if len(*ran) != 0 {
		t.Errorf("steps ran for a directory that is not a checkout: %q", *ran)
	}
}

// The wiring property (plan §5, spec §8.5): the command reads the gate BEFORE
// it writes anything. Both halves are here, because only the pair tells a gate
// that is consulted from one that refuses everything: with both marks readable
// an agent's session reaches the steps (the refusal it has always had is gone,
// which is the gate's whole point), and with the creation fact unreadable it is
// refused with the sentence it has always had, no step runs, no build is
// submitted and every call the fakes saw was a read.
func TestImageRefreshReadsTheGateBeforeAnyWrite(t *testing.T) {
	useVersion(t, "0.5.1")
	for _, tc := range []struct {
		what    string
		breakIt bool
		want    []string
	}{
		{"the gate is open, so the session reaches the steps", false, []string{"base", "check job", "builds"}},
		{"the creation fact cannot be read, so nothing runs", true, nil},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedRefreshRig(t, managedRefreshMarker(1))
			fb := useFakeBuilder(t)
			bothStale(t)
			if tc.breakIt {
				r.gcs.FailObjectGetsAfter(initRunsBucket, managed.MarkerObject, 0)
			}
			ran := fakeSteps(t, "", nil)
			_, _, err := executeStdin(t, "", "image", "refresh", "--yes")
			if tc.want == nil {
				if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro image refresh applies cloud changes: ") {
					t.Fatalf("exit %d, err %v: want the refusal a coding agent's session has always had", ExitCode(err), err)
				}
			} else if err != nil {
				t.Fatalf("the gate admitted the session and the run failed anyway: %v", err)
			}
			if strings.Join(*ran, ",") != strings.Join(tc.want, ",") {
				t.Errorf("the steps that ran are %q, want %q", *ran, tc.want)
			}
			if len(fb.specs) != 0 {
				t.Errorf("submitted %d builds, and every step here is stubbed", len(fb.specs))
			}
			if tc.want == nil {
				noWriteAcrossFakes(t, r.initRig)
				if got := r.audits(t); len(got) != 0 {
					t.Errorf("the day holds %d records for a step that never ran", len(got))
				}
			}
		})
	}
}

// The owner's own terminal in a managed project asks nothing for a billable
// build either (the ruling of 2026-11-01: the whole point of a managed project
// is that the gate manages it on the owner's behalf, without the prompts). The
// marks are read because this is exactly the run the per-build question would
// have stopped at, the gate judges THIS build, and what the owner reads in
// place of a typed name is the verdict's own sentence (GateProceeded): the cost
// line is printed with it, and the admission the owner finds later in their own
// bucket is written by refreshBuilds once the build is out.
//
// The control is the same build at the same terminal with a creation fact this
// program does not believe: nothing is typed (T7: the person being there IS the
// confirmation), so the build goes ahead — but the gate judged no document, so
// its own sentence is the one thing the owner does not read. The mutations
// caught: Managed.Removes answering true for every project (the control's
// verdict sentence is printed where the gate judged nothing), and the gate's
// own read being dropped so that any project is treated as managed.
func TestImageRefreshManagedOwnerTerminalAsksNoBuildQuestion(t *testing.T) {
	useVersion(t, "0.5.1")
	for _, tc := range []struct {
		what    string
		marker  []byte
		managed bool
	}{
		{"the project is managed, so the verdict is the confirmation", managedRefreshMarker(1), true},
		{"the project is unproven, so the person at the terminal confirms it", managedRefreshUnknownVersionMarker(), false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedRefreshRig(t, tc.marker)
			noAgentSession(t)
			// The door is the real one, made the way runImageRefresh makes it:
			// a person's own run, at a terminal, in the directory the rig is a
			// checkout of.
			out := atTerminal(t, r.e, "")
			m := openRefreshManaged(t.Context(), r.e.r.cmd, refreshOptions{}, &initOptions{})
			if (m != nil) != tc.managed {
				t.Fatalf("the gate's door answered %v, want managed=%v", m != nil, tc.managed)
			}
			if !tc.managed {
				// The control: the same build, the same terminal. Nothing is
				// typed at a confirmation any more (T7): the person being
				// there is the whole answer, so the build goes ahead — and the
				// gate's own sentence is not printed, because the gate judged
				// nothing here.
				ok, reachable, err := r.e.r.askBuild("submits a Cloud Build", initflow.Managed{})
				if err != nil || !ok || !reachable {
					t.Fatalf("a person at the terminal did not confirm a project that is not managed (ok=%v reachable=%v): %v", ok, reachable, err)
				}
				if strings.Contains(out.String(), initflow.GateProceeded()) {
					t.Errorf("the gate's sentence was printed for a project it did not judge:\n%s", out)
				}
				return
			}
			p := r.plan(m)
			host, err := infra.RegistryHost(r.tg.lc)
			if err != nil {
				t.Fatal(err)
			}
			g, err := m.admitBuild(t.Context(), p, r.tg, "app", host)
			if err != nil {
				t.Fatalf("the gate refused a build it should have admitted: %v", err)
			}
			banner := cloudBuildBanner(r.tg.spec.Name, "app", r.tg.lc.Build.MachineType, r.tg.spec.BuildServiceAccountEmail, r.tg.spec.RegistryPath)
			ok, reachable, err := r.e.r.askBuild(banner, g)
			if err != nil || !ok || !reachable {
				t.Fatalf("a managed project's own terminal was refused a build (ok=%v reachable=%v): %v\n%s", ok, reachable, err, out)
			}
			if got := out.String(); !strings.Contains(got, banner) || !strings.Contains(got, initflow.GateProceeded()) {
				t.Errorf("what the owner read is not the cost line and the verdict:\n%s", got)
			}
			if strings.Contains(out.String(), "[y/N]") {
				t.Errorf("a managed project's own terminal was asked a question anyway:\n%s", out)
			}
		})
	}
}

// terminalRefresh is the owner's own run of this rig's refresh: no coding
// agent's marker, a fake terminal whose output the caller reads, and the gate's
// read put on the run where runImageRefresh puts it (r.refresh, which is what
// the steps that are not builds ask). It fails the test when the door answered
// something other than want, because every caller below means one of the two
// worlds and needs the other one to be the one it says.
func (r *managedRefresh) terminalRefresh(t *testing.T, want bool) *syncBuf {
	t.Helper()
	noAgentSession(t)
	out := atTerminal(t, r.e, "")
	m := openRefreshManaged(t.Context(), r.e.r.cmd, refreshOptions{}, &initOptions{})
	if (m != nil) != want {
		t.Fatalf("the gate's door answered %v, want managed=%v", m != nil, want)
	}
	r.e.r.refresh = m
	return out
}

// managedOrUnproven is the two worlds every test below is run in: a managed
// project, whose marks the gate read and where the owner is asked nothing, and
// a project whose creation fact this program does not believe (a marker a newer
// Fugaro wrote), where the person at the terminal is the whole answer (nothing
// is typed any more: T7) and the gate's own sentence is the one thing they do
// not read.
func managedOrUnproven() []struct {
	what    string
	marker  []byte
	managed bool
} {
	return []struct {
		what    string
		marker  []byte
		managed bool
	}{
		{"managed: the marks are read and the step asks nothing", managedRefreshMarker(1), true},
		{"unproven: the person at the terminal confirms it, without the gate's sentence", managedRefreshUnknownVersionMarker(), false},
	}
}

// The daily image check job (step 3) takes no confirmation in a managed
// project's own terminal either (the ruling of 2026-11-01: the whole point of a
// managed project is that the gate manages it on the owner's behalf, without
// the prompts). What replaces a typed name is the gate's own read of the marks
// and the sentence that says so, printed under the diff; the job is updated.
//
// The control is the whole of what makes that safe, and it is the same step
// with the same terminal: an unproven project's step goes ahead too (T7: the
// person at the terminal is the whole answer, nothing is typed), but the gate's
// sentence is not printed, because the gate judged nothing.
//
// The mutation caught: refreshManaged.asksNothing answering true whatever it is
// handed — the unproven project then prints the gate's sentence where no gate
// judged, and this test fails on its own control.
func TestImageRefreshManagedOwnerTerminalAsksNoCheckJobQuestion(t *testing.T) {
	useVersion(t, "0.5.1")
	for _, tc := range managedOrUnproven() {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedRefreshRig(t, tc.marker)
			out := r.terminalRefresh(t, tc.managed)
			slug, label := checkJobOwner(t, "github", "acme/app")
			oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
			spec, err := json.Marshal(infra.CheckJobSpec{Repo: r.tg.spec.Name, BaseImages: map[string]string{"base": oldRef}})
			if err != nil {
				t.Fatal(err)
			}
			r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
			r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
			r.tg.spec.Label, r.tg.kinds = label, []string{"base"}
			r.tg.lc.BaseImages = map[string]string{"base": newRef}
			err = r.e.r.refreshCheckJob(t.Context(), r.tg)
			if !tc.managed {
				if err != nil {
					t.Fatalf("a person at the terminal was stopped at the check job: %v\n%s", err, out)
				}
				if strings.Contains(out.String(), initflow.GateOrdinary()) {
					t.Errorf("the gate's sentence was printed for a project it did not judge:\n%s", out)
				}
				if n := len(r.run.Patches()); n != 1 {
					t.Fatalf("%d patches, want 1 (the check job's image and its spec moved together)", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("a managed project's own terminal was stopped at the check job: %v\n%s", err, out)
			}
			for _, want := range []string{"image " + oldRef + " -> " + newRef, initflow.GateOrdinary(), "updated the daily image check job"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out.String(), "[y/N]") {
				t.Errorf("a managed project's own terminal was asked a question anyway:\n%s", out)
			}
			if n := len(r.run.Patches()); n != 1 {
				t.Fatalf("%d patches, want 1 (the check job's image and its spec moved together)", n)
			}
		})
	}
}

// The checkout: clone workflow's job move (step 4's first half) takes no
// confirmation in a managed project's own terminal either, and it submits no
// Cloud Build: what the owner reads is the old and new image, the sentence
// that says why nothing is asked, and the line that the job was updated. The
// control is the same move in the same terminal with a creation fact this
// program does not believe: the step goes ahead too (T7: the person at the
// terminal is the whole answer, nothing is typed), but the gate's sentence is
// not printed, because the gate judged nothing.
//
// The mutation caught: refreshManaged.asksNothing answering true whatever it is
// handed — the control then prints the gate's sentence where no gate judged,
// and this test fails on its own control.
func TestImageRefreshManagedOwnerTerminalAsksNoCloneJobQuestion(t *testing.T) {
	useVersion(t, "0.5.1")
	for _, tc := range managedOrUnproven() {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedRefreshRig(t, tc.marker)
			out := r.terminalRefresh(t, tc.managed)
			fb := useFakeBuilder(t)
			slug, label := checkJobOwner(t, "github", "acme/app")
			oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
			jobName := gcp.JobName(slug, "app")
			r.run.SetJob(jobName, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "app"}, oldRef)
			r.run.SetJobEnv(jobName, map[string]string{infra.CheckoutEnv: config.CheckoutClone})
			r.tg.lc.BaseImages = map[string]string{config.BaseKind: newRef}
			cfg, problems := config.Parse([]byte(cloneOnlyYAML))
			if cfg == nil {
				t.Fatal(problems)
			}
			r.tg.cfg, r.tg.spec.Label = cfg, label
			r.tg.spec.Workflows = map[string]infra.WorkflowSpec{"app": {Job: jobName}}
			p := &refreshPlan{workflows: []string{"app"}}
			err := r.e.r.refreshBuilds(t.Context(), p, r.tg)
			if !tc.managed {
				if err != nil {
					t.Fatalf("a person at the terminal was stopped at the clone job: %v\n%s", err, out)
				}
				if strings.Contains(out.String(), initflow.GateOrdinary()) {
					t.Errorf("the gate's sentence was printed for a project it did not judge:\n%s", out)
				}
				if n := len(r.run.Patches()); n != 1 {
					t.Fatalf("%d patches, want 1 (the clone job's image moved)", n)
				}
				if len(fb.specs) != 0 {
					t.Fatalf("a Cloud Build was submitted for a checkout: clone workflow: %+v", fb.specs)
				}
				return
			}
			if err != nil {
				t.Fatalf("a managed project's own terminal was stopped at the clone job: %v\n%s", err, out)
			}
			for _, want := range []string{"app: image " + oldRef + " -> " + newRef, initflow.GateOrdinary(), "updated app's job"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out.String(), "[y/N]") {
				t.Errorf("a managed project's own terminal was asked a question anyway:\n%s", out)
			}
			if n := len(r.run.Patches()); n != 1 {
				t.Fatalf("%d patches, want 1 (the clone job's image moved)", n)
			}
			if len(fb.specs) != 0 {
				t.Fatalf("a Cloud Build was submitted for a checkout: clone workflow: %+v", fb.specs)
			}
		})
	}
}

// The base pin (step 2) is the third of the refresh's steps that are not
// builds, and it asks through the images stage's own confirmOrdinary — the
// route this test drives, with the empty notCovered a refresh run gives it (a
// refresh sets no review, so there is no run confirmation for a plan to be
// outside of). In a managed project's own terminal it is confirmed by the
// gate's read and the sentence that says so; in a project the gate did not
// believe it goes ahead too (T7: the person at the terminal is the whole
// answer, nothing is typed), and the gate's own sentence is not printed,
// because the gate judged nothing.
//
// The mutation caught: refreshManaged.asksNothing answering true whatever it is
// handed — the control then prints the gate's sentence where no gate judged, and
// this test fails on its own control.
func TestImageRefreshManagedOwnerTerminalAsksNoBasePinQuestion(t *testing.T) {
	useVersion(t, "0.5.1")
	const what = "copies the release base image into this project's registry and pins it by digest in the local config, as listed above"
	for _, tc := range managedOrUnproven() {
		t.Run(tc.what, func(t *testing.T) {
			r := newManagedRefreshRig(t, tc.marker)
			out := r.terminalRefresh(t, tc.managed)
			err := r.e.r.confirmOrdinary(what, "nothing was copied and nothing was pinned", "")
			if !tc.managed {
				if err != nil {
					t.Fatalf("a person at the terminal was stopped at the base pin: %v\n%s", err, out)
				}
				if strings.Contains(out.String(), initflow.GateOrdinary()) {
					t.Errorf("the gate's sentence was printed for a project it did not judge:\n%s", out)
				}
				if !strings.Contains(out.String(), what) {
					t.Errorf("what the owner read is not the plan:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("a managed project's own terminal was stopped at the base pin: %v\n%s", err, out)
			}
			if got := out.String(); !strings.Contains(got, what) || !strings.Contains(got, initflow.GateOrdinary()) {
				t.Errorf("what the owner read is not the plan and the sentence that says why nothing is asked:\n%s", got)
			}
			if strings.Contains(out.String(), "[y/N]") {
				t.Errorf("a managed project's own terminal was asked a question anyway:\n%s", out)
			}
		})
	}
}
