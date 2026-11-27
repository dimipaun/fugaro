package cli

// The workflow secrets record `fugaro update` writes (B3.1 of
// docs/secrets-brokering/implementation-plan.md, D2073): what one repository's
// workflows may fetch from the credentials service, by name.
//
// The record is the answer the service cannot compute for itself — which
// container a workflow secret resolves to is Readiness's, and the service holds
// no operator's local config to read the allow-list out of (F33, D1262) — so
// what these tests hold is that the run writes the containers the repository's
// own job mounts, and not a name it took again from somewhere else.

import (
	"context"
	"encoding/base64"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// workflowSecretsYAML is acme/app's fugaro.yaml with the app workflow mounting
// two application secrets and the api workflow mounting none: the record has to
// name the app workflow and leave the api one out of the map rather than record
// it with no secrets.
//
// deploy-token is the name the operator's workflow_secrets block may cover
// project-wide and npm-token is one only the repository's own container serves,
// so one run states both spellings of F33 the service reads.
const workflowSecretsYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh }, secrets: [{ name: npm-token, env: NPM_TOKEN }, { name: deploy-token, env: DEPLOY_TOKEN }] }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// workflowSecretsRig is a converge rig of the repository above, with the app
// workflow's container for npm-token stored and deploy-token's project-wide
// container stored and allow-listed for acme/*. It is the fixture every test
// here starts from, so what a test varies is the one thing it is about.
func workflowSecretsRig(t *testing.T) (*convergeRig, string) {
	t.Helper()
	r := newConvergeRigYAML(t, v7Node(), workflowSecretsYAML)
	r.appendConfig(t, "workflow_secrets:\n  deploy-token: { allow_data_to: [acme/*] }\n")
	// A run selected by its checkout reads the copy registerInstallation made in
	// the projects directory and not this file (FUGARO_CONFIG is empty here), so
	// the file is registered again after the append and the run reads what these
	// tests wrote.
	registerInstallation(t, r.initRig)
	seedAppSecret(t, r.initRig, "npm-token")
	r.seedProjectKey(t, "deploy-token")
	return r, mustSlug("github", "acme/app")
}

// workflowSecretsOf reads the repository's record out of the rig's database
// through internal/creds' own reader: what the service (B3.2) reads on a
// workflow-secrets call, and not a copy this test decoded for itself.
func workflowSecretsOf(t *testing.T, r *convergeRig, slug string) (creds.WorkflowSecrets, bool) {
	t.Helper()
	db, err := newBudgetClient(context.Background(), r.db.URL, true)
	if err != nil {
		t.Fatalf("opening the installation's budget database: %v", err)
	}
	rec, found, err := creds.ReadWorkflowSecrets(context.Background(), db, slug)
	if err != nil {
		t.Fatalf("reading %s after the run: %v", creds.PathWorkflowSecrets(slug), err)
	}
	return rec, found
}

// TestUpdateWritesTheWorkflowSecretsTheJobMounts: one `fugaro update` of an
// installation that carries no record at all leaves the record a repository's
// workflows are served from, and it names the container each of the workflow's
// secrets is read from — the project's for the name the operator's allow-list
// covers, the repository's own for the other.
//
// This is the HARD RULE's own test as well as the record's: the node is new
// with this release, so an installation that ran the release behind has none,
// and the assertion is that the one command the user is told to run is the
// whole of it — the record is there afterwards and doctor is clean
// (convergeOn's own two checks, which fatals on a run that stopped at a stage
// needing a person, on a run that failed, and on a doctor check it left
// firing).
func TestUpdateWritesTheWorkflowSecretsTheJobMounts(t *testing.T) {
	r, slug := workflowSecretsRig(t)
	if v := r.db.Value(creds.PathWorkflowSecrets(slug)); v != nil {
		t.Fatalf("the fixture carries a record at %s already (%v): the state this test is about is an installation that has not run the release that writes one", creds.PathWorkflowSecrets(slug), v)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	rec, found := workflowSecretsOf(t, r, slug)
	if !found {
		t.Fatalf("%s is not there after `fugaro update` alone: an installation one release behind would need a second command to be served its workflow secrets", creds.PathWorkflowSecrets(slug))
	}
	if rec.Version != creds.WorkflowSecretsVersion {
		t.Errorf("the record's version is %d, want %d", rec.Version, creds.WorkflowSecretsVersion)
	}
	want := map[string][]creds.WorkflowSecret{
		"app": {
			{Env: "DEPLOY_TOKEN", ID: gcp.ProjectSecretID(initProjectName, "deploy-token")},
			{Env: "NPM_TOKEN", ID: gcp.SecretID(slug, "npm-token")},
		},
	}
	if len(rec.Workflows) != len(want) {
		t.Fatalf("the record holds %v; want exactly %v — a workflow mounting no secret is absent from the map rather than recorded with none", rec.Workflows, want)
	}
	for name, rows := range want {
		if got := rec.Workflows[name]; !slices.Equal(got, rows) {
			t.Errorf("the record's %s workflow holds %v; want %v", name, got, rows)
		}
	}

	// A person reads the same fact the service will: the run says what it
	// recorded and where, so an installation whose workflows are served nothing
	// has a line to look at rather than a silent absence.
	if !strings.Contains(out, creds.PathWorkflowSecrets(slug)) {
		t.Errorf("the run does not name %s, the record it wrote: the line is how a person knows the service has this repository's workflows\n%s", creds.PathWorkflowSecrets(slug), out)
	}
}

// TestUpdateWorkflowSecretsFollowTheAllowList: the record names the container
// the JOB mounts, and for a project-wide workflow secret that is the
// operator's allow-list's answer and not the existence of the container.
//
// The fixture is the case where the two disagree: npm-token has BOTH a
// project-wide container, stored and readable, and the repository's own, and
// the workflow_secrets block lists only deploy-token, so nothing admits this
// repository to npm-token project-wide. A record written from "which container
// could the service read" would name the project's container for npm-token,
// and what the job actually mounts is the repository's own (Readiness's
// repo-wins-else-project precedence, F33). The service reading such a record
// would serve a value the run never gets.
func TestUpdateWorkflowSecretsFollowTheAllowList(t *testing.T) {
	r, slug := workflowSecretsRig(t)
	// The one line this test adds to the fixture: the project-wide container of
	// a name no workflow_secrets entry covers.
	r.seedProjectKey(t, "npm-token")

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	rec, found := workflowSecretsOf(t, r, slug)
	if !found {
		t.Fatalf("%s is not there after `fugaro update` alone", creds.PathWorkflowSecrets(slug))
	}
	got := rec.Workflows["app"]
	want := []creds.WorkflowSecret{
		{Env: "DEPLOY_TOKEN", ID: gcp.ProjectSecretID(initProjectName, "deploy-token")},
		{Env: "NPM_TOKEN", ID: gcp.SecretID(slug, "npm-token")},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the record's app workflow holds %v; want %v — the allow-list decides which container the job mounts, and a project-wide container that exists for a name it does not cover is not it", got, want)
	}
}

// TestUpdateWorkflowSecretsAreWrittenByAnyDoorThatAppliedTheRoot: what the
// step is gated on is the applied root and not the command that asked for it.
//
// The plan is the state it writes nothing in: --plan-only is where a coding
// agent's session reaches this engine (the loop plans an engine stage
// read-only, and a session applies none), and `--yes`'s help promises that a
// session "changes nothing outside this machine" — a record published out of a
// run that applied nothing is exactly the write that promise does not have,
// and the mounts such a run resolved are ones no job carries.
//
// Both doors that apply a repository's root write, and the same fixture writes
// under each: `fugaro setup --repo` onboards a repository by applying its root
// and deploying its job exactly as `fugaro update --repo` does (runInitRepo →
// repoEngine, one engine, no door condition), so a repository onboarded that
// way mounts its secrets and — with the door refused here — would have been
// served nothing by the service until a later `fugaro update` ran in its
// checkout, which is a cascade job halted with no command that fixes it.
//
// The fixture's ready carries the mounted container because that is what
// repoEngine hands this step: Readiness's output for a root that was applied,
// never the pre-Readiness spec and never a re-resolution (update_workflowsecrets.go).
func TestUpdateWorkflowSecretsAreWrittenByAnyDoorThatAppliedTheRoot(t *testing.T) {
	slug := mustSlug("github", "acme/app")
	call := func(t *testing.T, o *initOptions) *gcpfake.RTDB {
		t.Helper()
		db := gcpfake.NewRTDB(t)
		db.Set(budget.PathProject, initProjectName)
		db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
		lc := &localcfg.Config{
			Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			Budget:    &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
			Endpoints: localcfg.Endpoints{NoAuth: true},
		}
		ready := infra.RepoSpec{
			Slug: slug, Name: "acme/app",
			Secrets: map[string]string{"npm-token": "fugaro-acme-app-npm-token-1a2b"},
		}
		cfg := &config.Config{Workflows: map[string]config.Workflow{
			"app": {Secrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}}},
		}}
		(&initRun{o: o, w: &strings.Builder{}}).writeWorkflowSecrets(t.Context(), lc, ready, cfg)
		return db
	}

	t.Run("a plan", func(t *testing.T) {
		db := call(t, &initOptions{door: updateDoor.name, planOnly: true})
		if v := db.Value(creds.PathWorkflowSecrets(slug)); v != nil {
			t.Errorf("%s exists after a plan (%v): --plan-only changes nothing outside this machine, and a coding agent's session reaches this engine the same way", creds.PathWorkflowSecrets(slug), v)
		}
	})
	for _, door := range []string{setupDoor.name, updateDoor.name} {
		t.Run(door+", applied", func(t *testing.T) {
			db := call(t, &initOptions{door: door})
			if db.Value(creds.PathWorkflowSecrets(slug)) == nil {
				t.Fatalf("%s is not there after `%s` applied the repository's root: the job this run deployed mounts the fixture's secret, and the service reads the record and no config of ours", creds.PathWorkflowSecrets(slug), door)
			}
			client, err := newBudgetClient(t.Context(), db.URL, true)
			if err != nil {
				t.Fatal(err)
			}
			rec, found, err := creds.ReadWorkflowSecrets(t.Context(), client, slug)
			if err != nil || !found {
				t.Fatalf("the record the run wrote does not read back: found=%v err=%v", found, err)
			}
			if got := rec.Workflows["app"]; len(got) != 1 || got[0].Env != "NPM_TOKEN" || got[0].ID != "fugaro-acme-app-npm-token-1a2b" {
				t.Errorf("the record's app workflow holds %v; want the container the fixture's spec mounts", got)
			}
		})
	}
}

// IS5 (D3325): `fugaro update` copies each workflow secret's project copy into
// the `fugaro_p_` grammar the intake writes and the credentials service serves,
// as the operator, and records the names it copied so IS6 drops only those.
//
// The tests below are the migration's own: what it writes and what it refuses,
// over the real update door (convergeOn), because the thing being asserted is
// that an installation one release behind reaches this release with the one
// command it is told to run.

// copyWho is the address the operator's credentials belong to in these tests,
// and therefore the `fugaro_created_by` annotation the copy records (R10). It
// is the managed gate's own fixture identity as well, and deliberately so: the
// run's steps leave a record naming the same person the copy names as its maker
// (isolateGateCredentials, which the rig's converge installs).
const copyWho = managedGateUser

// operatorIdentity stubs who the operator is. The rigs run with
// endpoints.no_auth, and a credential that cannot say who its holder is cannot
// make a container the intake accepts: the copy records the maker, so a bare
// rig without this stub is refused before it copies anything.
func operatorIdentity(t *testing.T) {
	t.Helper()
	old := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return copyWho, nil }
	t.Cleanup(func() { authenticatedUser = old })
}

// grammarID is the container a name's copy lands in, through the same
// constructor the copy uses (secretname.Project) rather than the prefix spelled
// out in the test.
func grammarID(t *testing.T, name string) string {
	t.Helper()
	id, err := secretname.Project(name)
	if err != nil {
		t.Fatalf("secretname.Project(%q): %v", name, err)
	}
	return id
}

// fakeContainer reads one container back through internal/backend/gcp's own
// client: what the intake's shape check is put to, and what the update's copy
// itself read, rather than the fake's own fields.
func fakeContainer(t *testing.T, sm *gcpfake.Secrets, id string) (gcp.ContainerInfo, bool) {
	t.Helper()
	svc, err := gcp.NewSecrets(t.Context(), gcp.Options{GCPProject: initProject, Region: "us-east5",
		Endpoints: gcp.Endpoints{SecretManager: sm.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatalf("connecting to the rig's Secret Manager fake: %v", err)
	}
	info, found, err := svc.Container(t.Context(), id)
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	return info, found
}

// projectCopyRig writes one name's entry into the project record, as `fugaro
// secrets allow` and `deny` do: the state the copy has to leave alone.
func putProjectRule(t *testing.T, r *convergeRig, name string, rule creds.ProjectSecretRule) {
	t.Helper()
	db, err := newBudgetClient(context.Background(), r.db.URL, true)
	if err != nil {
		t.Fatalf("opening the installation's budget database: %v", err)
	}
	if err := creds.WriteProjectSecrets(context.Background(), db, func(cur creds.ProjectSecrets, _ bool) (creds.ProjectSecrets, error) {
		return cur.WithRule(name, rule), nil
	}); err != nil {
		t.Fatalf("writing the fixture's entry for %s: %v", name, err)
	}
}

// TestUpdateCopiesProjectCopiesToGrammar: one `fugaro update` of an
// installation whose operator has allow-listed a workflow secret moves the
// name's project copy — the only copy a run reads under D2313 — into the
// grammar, and what it leaves behind is a container the intake itself accepts.
//
// The value is the same one the project copy holds and the project copy is
// still there: this release copies, it does not move or delete anything, and
// the drop of the old container is IS6's.
func TestUpdateCopiesProjectCopiesToGrammar(t *testing.T) {
	operatorIdentity(t)
	r, _ := workflowSecretsRig(t)
	src := gcp.ProjectSecretID(initProjectName, "deploy-token")
	wantValue := string(r.sm.Latest(src))

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	dst := grammarID(t, "deploy-token")
	info, found := fakeContainer(t, r.sm, dst)
	if !found {
		t.Fatalf("no %s container after `fugaro update`: the project copy of a workflow secret is not in the grammar the intake writes and the credentials service serves (D3325)\n%s", dst, out)
	}
	// Exactly the labels the intake writes for a project-shared container plus
	// the marker: a container carrying more or less is one the next
	// `fugaro secrets set` refuses (G2).
	wantLabels := map[string]string{
		intake.LabelScope:          string(intake.ScopeShared),
		intake.LabelWorkflowSecret: intake.WorkflowSecretMarker,
	}
	if !maps.Equal(info.Labels, wantLabels) {
		t.Errorf("the container the copy made carries labels %v; want exactly %v", info.Labels, wantLabels)
	}
	wantAnnotations := map[string]string{intake.AnnotationCreatedBy: copyWho}
	if !maps.Equal(info.Annotations, wantAnnotations) {
		t.Errorf("the container the copy made carries annotations %v; want exactly %v", info.Annotations, wantAnnotations)
	}
	// The container the copy made is one the intake accepts, asked through the
	// intake's own check rather than a second spelling of it.
	if err := intake.AcceptsWorkflowWrite(dst, intakeSecretOf(info)); err != nil {
		t.Errorf("the intake refuses a write into the container `fugaro update` made: %v", err)
	}
	if got := string(r.sm.Latest(dst)); got != wantValue {
		t.Errorf("%s holds %q after the update; want the project copy's value", dst, got)
	}
	if got := string(r.sm.Latest(src)); got != wantValue {
		t.Errorf("the project copy %s was changed by the copy (%q); the migration copies and leaves it for IS6 to retire", src, got)
	}

	// The record IS6 reads: the name is there, with the operator's own list.
	ps, found := projectSecretsOf(t, r.db)
	if !found {
		t.Fatalf("%s is not there after `fugaro update`: a value copied with no record of it is a container nothing retires (D3325)", creds.PathProjectSecrets())
	}
	rule, ok := ps.Rule("deploy-token")
	if !ok {
		t.Fatalf("the record holds no entry for deploy-token, the name the copy made: %v", ps.Secrets)
	}
	if !slices.Equal(rule.Repos, []string{"acme/*"}) || rule.AllRepos {
		t.Errorf("the record's deploy-token entry is %+v; want the operator's own allow_data_to", rule)
	}

	// The line the person reads, and the one thing it may never carry: a value.
	if !strings.Contains(out, "workflow secrets: 1 copied into the "+secretname.ProjectPrefix+" grammar") {
		t.Errorf("the run does not say what it copied:\n%s", out)
	}
	for _, value := range []string{wantValue, string(r.sm.Latest(dst))} {
		if strings.Contains(out, value) {
			t.Errorf("the run printed the secret's value in its account:\n%s", out)
		}
	}
}

// TestUpdateSkipsAnUnmarkedContainer: a container under the grammar's name that
// is not this intake's is left exactly as it is, and the name is not recorded.
//
// The container is the one an unmarked shared credential lives in — the shape
// `fugaro secrets shared set` writes — so it is a value of another class
// entirely: writing a workflow's secret into it would publish that secret to
// every workflow the project serves (G2, F18).
func TestUpdateSkipsAnUnmarkedContainer(t *testing.T) {
	operatorIdentity(t)
	r, _ := workflowSecretsRig(t)
	dst := grammarID(t, "deploy-token")
	r.sm.Seed(dst, map[string]string{intake.LabelScope: string(intake.ScopeShared)}, []byte("hand-made"))

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if got := string(r.sm.Latest(dst)); got != "hand-made" {
		t.Errorf("%s holds %q after the update; the copy must write no version into a container it did not make", dst, got)
	}
	if n := len(r.sm.EnabledVersions(dst)); n != 1 {
		t.Errorf("%s holds %d versions after the update, want the one it already had", dst, n)
	}
	if ps, found := projectSecretsOf(t, r.db); found {
		if _, ok := ps.Rule("deploy-token"); ok {
			t.Errorf("the record holds an entry for deploy-token although nothing was copied for it: IS6 would retire a mount whose value the copy never moved (%v)", ps.Secrets)
		}
	}
	if !strings.Contains(out, dst) || !strings.Contains(out, "secrets shared rm") {
		t.Errorf("the run does not name %s and the command that removes it:\n%s", dst, out)
	}
}

// TestUpdateRefusesAForeignShape: a marked container of a shape this intake
// never writes is refused too, and the fix is printed.
//
// Two shapes, because the check refuses both and they are two different
// attacks: a time-to-live, which is how a container somebody pre-created takes
// the value away from whoever writes it (R12, F35), and an annotation that is
// not the one this call would have written. The markers are on both, so what
// is being asked here is the shape and not the marker.
func TestUpdateRefusesAForeignShape(t *testing.T) {
	operatorIdentity(t)
	r := newConvergeRigYAML(t, v7Node(), workflowSecretsYAML)
	r.appendConfig(t, "workflow_secrets:\n  deploy-token: { allow_data_to: [acme/*] }\n  api-token: { allow_data_to: [acme/*] }\n")
	registerInstallation(t, r.initRig)
	seedAppSecret(t, r.initRig, "npm-token")
	marked := map[string]string{intake.LabelScope: string(intake.ScopeShared), intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}
	owned := map[string]string{intake.AnnotationCreatedBy: copyWho}
	for _, name := range []string{"deploy-token", "api-token"} {
		r.seedProjectKey(t, name)
	}
	r.sm.SeedWithTTL(grammarID(t, "deploy-token"), maps.Clone(marked), maps.Clone(owned), "3600s", []byte("pre-created"))
	stray := maps.Clone(owned)
	stray["team"] = "infra"
	r.sm.SeedShape(grammarID(t, "api-token"), maps.Clone(marked), stray, []byte("pre-created"))

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	for _, name := range []string{"deploy-token", "api-token"} {
		id := grammarID(t, name)
		if got := string(r.sm.Latest(id)); got != "pre-created" {
			t.Errorf("%s holds %q after the update; the copy must write no version into a container of a shape the intake does not write", id, got)
		}
		if n := len(r.sm.EnabledVersions(id)); n != 1 {
			t.Errorf("%s holds %d versions after the update, want the one the foreign container had", id, n)
		}
		if !strings.Contains(out, id) || !strings.Contains(out, "secrets shared rm") {
			t.Errorf("the run does not name %s and the command that removes it:\n%s", id, out)
		}
	}
	if ps, found := projectSecretsOf(t, r.db); found {
		for _, name := range []string{"deploy-token", "api-token"} {
			if _, ok := ps.Rule(name); ok {
				t.Errorf("the record holds an entry for %s although nothing was copied for it: the name's mount and its project copy are left for the next update (%v)", name, ps.Secrets)
			}
		}
	}
}

// TestUpdateSeedsListOnlyFromAllowDataTo: the entry a copied name gets is the
// operator's own allow_data_to, and a repository's own declaration is not a
// grant (F22, F23).
//
// The fixture is the case that tells the two apart: the app workflow declares
// npm-token and a project copy of it exists, and the operator's
// workflow_secrets block names only deploy-token. A record written from what
// the repository declares would admit acme/app to npm-token, which its owner
// never opened to it.
func TestUpdateSeedsListOnlyFromAllowDataTo(t *testing.T) {
	operatorIdentity(t)
	r := newConvergeRigYAML(t, v7Node(), workflowSecretsYAML)
	r.appendConfig(t, "workflow_secrets:\n  deploy-token: { allow_data_to: [acme/*, acme/tools] }\n")
	registerInstallation(t, r.initRig)
	seedAppSecret(t, r.initRig, "npm-token")
	r.seedProjectKey(t, "deploy-token")
	r.seedProjectKey(t, "npm-token")

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	ps, found := projectSecretsOf(t, r.db)
	if !found {
		t.Fatalf("%s is not there after `fugaro update`", creds.PathProjectSecrets())
	}
	rule, ok := ps.Rule("deploy-token")
	if !ok {
		t.Fatalf("the record holds no entry for deploy-token: %v", ps.Secrets)
	}
	if want := []string{"acme/*", "acme/tools"}; !slices.Equal(rule.Repos, want) || rule.AllRepos {
		t.Errorf("the record's deploy-token entry is %+v; want exactly the operator's allow_data_to %v", rule, want)
	}
	if _, ok := ps.Rule("npm-token"); ok {
		t.Errorf("the record holds an entry for npm-token, which only a repository's own `secrets:` declares: a declaration is not a grant (%v)", ps.Secrets)
	}
	if _, found := fakeContainer(t, r.sm, grammarID(t, "npm-token")); found {
		t.Errorf("the copy made %s for a name the operator's workflow_secrets block does not name: only an entry with an allow-list has a project copy to move", grammarID(t, "npm-token"))
	}
}

// TestUpdateNeverWidensAnExistingEntry: an entry the owner already wrote is
// left exactly as it is, whatever the local block says (G8, F23).
//
// The fixture narrows deploy-token to one repository, which is what
// `fugaro secrets deny` leaves, while the operator's own block reaches acme/*.
// Seeding from the block here would undo the narrowing — the one direction a
// migration of a list must never take — and the value is still copied, because
// the two halves are independent: IS6 drops a mount whose name is recorded.
func TestUpdateNeverWidensAnExistingEntry(t *testing.T) {
	operatorIdentity(t)
	r, _ := workflowSecretsRig(t)
	putProjectRule(t, r, "deploy-token", creds.ProjectSecretRule{Repos: []string{"acme/app"}})

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	ps, found := projectSecretsOf(t, r.db)
	if !found {
		t.Fatalf("%s is not there after `fugaro update`", creds.PathProjectSecrets())
	}
	rule, ok := ps.Rule("deploy-token")
	if !ok {
		t.Fatalf("the entry the fixture wrote is gone: %v", ps.Secrets)
	}
	if want := []string{"acme/app"}; !slices.Equal(rule.Repos, want) || rule.AllRepos {
		t.Errorf("the record's deploy-token entry is %+v after the update; want the owner's own narrowed list %v — the update seeds a name only when the record holds no entry for it", rule, want)
	}
	if got := string(r.sm.Latest(grammarID(t, "deploy-token"))); got != string(r.sm.Latest(gcp.ProjectSecretID(initProjectName, "deploy-token"))) {
		t.Errorf("%s holds %q after the update; the value is copied whether or not the list was seeded", grammarID(t, "deploy-token"), got)
	}
}

// TestUpdateNeverOverwritesANewerValue: a marked container that already holds
// an enabled version keeps it.
//
// It is the store the owner made with `fugaro secrets set` since the project
// copy was written — D3323 made that store the intake's, so it is marked and
// shaped like the copy's own — and its value is newer than anything the copy
// holds. Adding a version would be a rollback. The name IS recorded, because
// its value is in the grammar either way, and the record is what IS6 reads.
func TestUpdateNeverOverwritesANewerValue(t *testing.T) {
	operatorIdentity(t)
	r, _ := workflowSecretsRig(t)
	dst := grammarID(t, "deploy-token")
	r.sm.SeedShape(dst,
		map[string]string{intake.LabelScope: string(intake.ScopeShared), intake.LabelWorkflowSecret: intake.WorkflowSecretMarker},
		map[string]string{intake.AnnotationCreatedBy: copyWho}, []byte("set-by-the-owner"))

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if got := string(r.sm.Latest(dst)); got != "set-by-the-owner" {
		t.Errorf("%s holds %q after the update, want the value the owner stored: a `secrets set` since the project copy is newer than it", dst, got)
	}
	if n := len(r.sm.EnabledVersions(dst)); n != 1 {
		t.Errorf("%s holds %d versions after the update, want the one it already had", dst, n)
	}
	if !strings.Contains(out, "1 already there") {
		t.Errorf("the run does not say that the name was already in the grammar:\n%s", out)
	}
	ps, found := projectSecretsOf(t, r.db)
	if !found {
		t.Fatalf("%s is not there after `fugaro update`: the name's value is in the grammar, so IS6 has to be able to retire its mount", creds.PathProjectSecrets())
	}
	if _, ok := ps.Rule("deploy-token"); !ok {
		t.Errorf("the record holds no entry for deploy-token although the grammar container holds its value: %v", ps.Secrets)
	}
}

// TestIntakeAcceptsAContainerUpdateMade: the round trip the whole shape of the
// copy exists for — the container `fugaro update` made is one the intake's own
// write path accepts afterwards, and the copy left exactly one version in it.
func TestIntakeAcceptsAContainerUpdateMade(t *testing.T) {
	operatorIdentity(t)
	r, _ := workflowSecretsRig(t)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	dst := grammarID(t, "deploy-token")
	info, found := fakeContainer(t, r.sm, dst)
	if !found {
		t.Fatalf("no %s container after `fugaro update`", dst)
	}
	if err := intake.AcceptsWorkflowWrite(dst, intakeSecretOf(info)); err != nil {
		t.Errorf("the intake refuses the container `fugaro update` made, so a `fugaro secrets set` into it would fail while a run reads its value: %v", err)
	}
	if n := len(r.sm.EnabledVersions(dst)); n != 1 {
		t.Errorf("%s holds %d enabled versions after one update, want the copy's one", dst, n)
	}
	if n := len(r.sm.EnabledVersions(gcp.ProjectSecretID(initProjectName, "deploy-token"))); n != 1 {
		t.Errorf("the project copy holds %d enabled versions after the copy, want the one it had: the copy reads it and writes nothing back into it", n)
	}
}

// TestUpdateStopsANonOwnerWithTheFix: an operator without a read on the project
// copy is stopped with the one line that names the fix, and the installation is
// left as it is.
//
// The roles Fugaro grants carry no secretmanager.versions.access — the
// project's Owner does (docs/secret-readers.md E12) — so every remaining name
// would be refused the same way, and the step stops rather than printing one
// refusal per name. It is stopped, not failed: the converge's own stages are
// what the owner came for, and `fugaro update` stays the whole upgrade.
func TestUpdateStopsANonOwnerWithTheFix(t *testing.T) {
	operatorIdentity(t)
	r, _ := workflowSecretsRig(t)
	src := gcp.ProjectSecretID(initProjectName, "deploy-token")
	// The roles an operator holds: no read of a value, on the project copy.
	r.sm.SetHeldPermissions(src)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if !strings.Contains(out, "secretmanager.versions.access") || !strings.Contains(out, "as the project's Owner") {
		t.Errorf("the run does not name the permission and the one thing that grants it:\n%s", out)
	}
	if _, found := fakeContainer(t, r.sm, grammarID(t, "deploy-token")); found {
		t.Errorf("%s was made although the operator had no read on the project copy", grammarID(t, "deploy-token"))
	}
	if slices.Contains(r.sm.Accessed(), src) {
		t.Errorf("the project copy %s was read although the caller was known to hold no read on it", src)
	}
	if _, found := projectSecretsOf(t, r.db); found {
		t.Errorf("%s was written although nothing was copied", creds.PathProjectSecrets())
	}
}

// TestUpdateSkipsANameWithNoProjectCopy: a name whose project copy is already
// gone is nothing to copy — the operator's read is not what is missing — so it
// stops nothing and holds back no name after it.
//
// The state is the routine one once IS6 has retired a project copy, and the
// name is still one the operator allow-lists. CanReadValues answers false, nil
// for a container that is not there exactly as it does for one the caller may
// not read, so what tells the two apart is whether the container is there at
// all.
func TestUpdateSkipsANameWithNoProjectCopy(t *testing.T) {
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	operatorIdentity(t)

	// api-token's project copy is gone and deploy-token's is there; the names
	// are answered in order, so api-token is the one that comes first.
	sm := gcpfake.NewSecrets(t)
	sm.AllowAccess()
	sm.Seed(gcp.ProjectSecretID(initProjectName, "deploy-token"), nil, []byte("project-deploy-token"))
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		Budget:    &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints: localcfg.Endpoints{SecretManager: sm.URL + "/", NoAuth: true},
		WorkflowSecrets: map[string]config.ProjectSecret{
			"api-token":    {AllowDataTo: []string{"acme/*"}},
			"deploy-token": {AllowDataTo: []string{"acme/*"}},
		},
	}
	var w strings.Builder
	e := &initEngine{lc: lc, r: &initRun{o: &initOptions{door: updateDoor.name}, w: &w}}
	e.copyWorkflowSecretsToGrammar(t.Context())

	out := w.String()
	if strings.Contains(out, "as the project's Owner") {
		t.Errorf("a name with no project copy stopped the step as if the operator had no read:\n%s", out)
	}
	dst := grammarID(t, "deploy-token")
	if _, found := fakeContainer(t, sm, dst); !found {
		t.Errorf("%s was not copied after a name with no project copy came before it:\n%s", dst, out)
	}
	if _, found := fakeContainer(t, sm, grammarID(t, "api-token")); found {
		t.Errorf("%s was made although the name has no project copy to copy", grammarID(t, "api-token"))
	}
	ps, found := projectSecretsOf(t, db)
	if !found {
		t.Fatalf("%s is not there: the names copied are the ones IS6 reads (D3325)", creds.PathProjectSecrets())
	}
	if _, ok := ps.Rule("deploy-token"); !ok {
		t.Errorf("the record holds no entry for deploy-token although its value was copied: %v", ps.Secrets)
	}
	if _, ok := ps.Rule("api-token"); ok {
		t.Errorf("the record holds an entry for api-token although nothing was copied for it: %v", ps.Secrets)
	}
}

// TestUpdateRecordsWhatItCopiedBeforeANoReadStopsIt: an operator with no read
// on one project copy stops the step — every remaining name would be refused
// the same way — but the names copied before it are recorded, because the
// record is what IS6 reads to retire the copies this migration replaces. A
// value in the grammar with no record of it is a container nothing retires.
func TestUpdateRecordsWhatItCopiedBeforeANoReadStopsIt(t *testing.T) {
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	operatorIdentity(t)

	// alpha-token is read and copied; zeta-token is there and the operator may
	// not read it, and it comes second.
	sm := gcpfake.NewSecrets(t)
	sm.AllowAccess()
	sm.Seed(gcp.ProjectSecretID(initProjectName, "alpha-token"), nil, []byte("project-alpha-token"))
	zeta := gcp.ProjectSecretID(initProjectName, "zeta-token")
	sm.Seed(zeta, nil, []byte("project-zeta-token"))
	// The fake holds nothing for any ID a test has named, so both are named:
	// alpha-token is readable and zeta-token is not.
	sm.SetHeldPermissions(gcp.ProjectSecretID(initProjectName, "alpha-token"), "secretmanager.versions.access")
	sm.SetHeldPermissions(zeta)
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		Budget:    &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints: localcfg.Endpoints{SecretManager: sm.URL + "/", NoAuth: true},
		WorkflowSecrets: map[string]config.ProjectSecret{
			"alpha-token": {AllowDataTo: []string{"acme/*"}},
			"zeta-token":  {AllowDataTo: []string{"acme/*"}},
		},
	}
	var w strings.Builder
	e := &initEngine{lc: lc, r: &initRun{o: &initOptions{door: updateDoor.name}, w: &w}}
	e.copyWorkflowSecretsToGrammar(t.Context())

	out := w.String()
	if !strings.Contains(out, "secretmanager.versions.access") || !strings.Contains(out, "as the project's Owner") {
		t.Errorf("the run does not stop with the permission and the one thing that grants it:\n%s", out)
	}
	alpha := grammarID(t, "alpha-token")
	if _, found := fakeContainer(t, sm, alpha); !found {
		t.Errorf("%s is not there: the name copied before the refusal was thrown away with it:\n%s", alpha, out)
	}
	if _, found := fakeContainer(t, sm, grammarID(t, "zeta-token")); found {
		t.Errorf("%s was made although the operator may not read its project copy", grammarID(t, "zeta-token"))
	}
	ps, found := projectSecretsOf(t, db)
	if !found {
		t.Fatalf("%s is not there after the copy: a value in the grammar with no record of it is a container nothing retires (D3325)", creds.PathProjectSecrets())
	}
	if _, ok := ps.Rule("alpha-token"); !ok {
		t.Errorf("the record holds no entry for alpha-token although its value was copied: %v", ps.Secrets)
	}
	if _, ok := ps.Rule("zeta-token"); ok {
		t.Errorf("the record holds an entry for zeta-token although nothing was copied for it: %v", ps.Secrets)
	}
	if slices.Contains(sm.Accessed(), zeta) {
		t.Errorf("%s was read although the caller was known to hold no read on it", zeta)
	}
}

// TestUpdateRedactsAValueFromAWriteFailure: a write that fails with the bytes
// echoed back — which is what the Secret Manager API does with the payload it
// refuses — still puts no value in front of a person. The name is warned as not
// copied and is not recorded, so the next update copies it again.
//
// Both spellings of the value are asserted absent, the decoded form and the
// base64 one: a failure's message can carry either. Removing the redaction in
// copyOneWorkflowSecret prints the value here and nowhere else, so this test is
// what holds the credentials-service rule for the copy's own failures.
func TestUpdateRedactsAValueFromAWriteFailure(t *testing.T) {
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	operatorIdentity(t)

	const value = "project-deploy-token"
	sm := gcpfake.NewSecrets(t)
	sm.AllowAccess()
	sm.Seed(gcp.ProjectSecretID(initProjectName, "deploy-token"), nil, []byte(value))
	sm.EchoAddVersionPayload = true
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		Budget:          &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints:       localcfg.Endpoints{SecretManager: sm.URL + "/", NoAuth: true},
		WorkflowSecrets: map[string]config.ProjectSecret{"deploy-token": {AllowDataTo: []string{"acme/*"}}},
	}
	var w strings.Builder
	e := &initEngine{lc: lc, r: &initRun{o: &initOptions{door: updateDoor.name}, w: &w}}
	e.copyWorkflowSecretsToGrammar(t.Context())

	out := w.String()
	if !strings.Contains(out, "deploy-token was not copied") {
		t.Errorf("the run does not say the name was not copied after a write the API refused:\n%s", out)
	}
	for _, leaked := range []string{value, base64.StdEncoding.EncodeToString([]byte(value))} {
		if strings.Contains(out, leaked) {
			t.Errorf("the update printed the workflow secret's value (%q) in a line a person reads:\n%s", leaked, out)
		}
	}
	if n := len(sm.EnabledVersions(grammarID(t, "deploy-token"))); n != 0 {
		t.Errorf("%s holds %d enabled versions after a write that failed, want none", grammarID(t, "deploy-token"), n)
	}
	if _, found := projectSecretsOf(t, db); found {
		t.Errorf("%s was written although no value reached the grammar: IS6 would retire the mount of a name nothing copied", creds.PathProjectSecrets())
	}
}

// TestUpdateFromPreviousReleaseConvergesSecrets: the HARD RULE's own test for
// this node. An installation that ran the release behind has no project record
// and no `fugaro_p_` container at all — both are new with this release — and
// the one command the user is told to run is the whole of it: afterwards the
// name is in the grammar, the record admits the repository, and doctor is clean
// (convergeOn's own two checks).
//
// What the copy must NOT do is rewrite the version-1 record: a run of the
// release behind reads `creds/workflowsecrets/<slug>` for its mounts, and the
// ID in it is still the project copy's, not the new container's. Both routes
// read the same value, and the drop of the old one is IS6's, not this step's
// (G4).
func TestUpdateFromPreviousReleaseConvergesSecrets(t *testing.T) {
	operatorIdentity(t)
	r, slug := workflowSecretsRig(t)
	if _, found := projectSecretsOf(t, r.db); found {
		t.Fatalf("the fixture carries a record at %s already: the state this test is about is an installation that ran the release behind", creds.PathProjectSecrets())
	}
	if _, found := fakeContainer(t, r.sm, grammarID(t, "deploy-token")); found {
		t.Fatalf("the fixture carries %s already: the grammar is new with this release", grammarID(t, "deploy-token"))
	}

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if _, found := fakeContainer(t, r.sm, grammarID(t, "deploy-token")); !found {
		t.Errorf("%s is not there after `fugaro update` alone: an installation one release behind would need a second command to be served this workflow secret", grammarID(t, "deploy-token"))
	}
	ps, found := projectSecretsOf(t, r.db)
	if !found {
		t.Fatalf("%s is not there after `fugaro update` alone", creds.PathProjectSecrets())
	}
	if _, ok := ps.Rule("deploy-token"); !ok {
		t.Errorf("the record holds no entry for deploy-token after the update: %v", ps.Secrets)
	}

	// Version 1's record, which a run of the release behind reads, still names
	// the project copy.
	rec, found := workflowSecretsOf(t, r, slug)
	if !found {
		t.Fatalf("%s is not there after the update: the repository's own job mounts what it names", creds.PathWorkflowSecrets(slug))
	}
	src := gcp.ProjectSecretID(initProjectName, "deploy-token")
	if !slices.Contains(rec.Workflows["app"], creds.WorkflowSecret{Env: "DEPLOY_TOKEN", ID: src}) {
		t.Errorf("the version-1 record's app workflow holds %v; want the project copy %s it still mounts", rec.Workflows["app"], src)
	}
	if slices.Contains(rec.Workflows["app"], creds.WorkflowSecret{Env: "DEPLOY_TOKEN", ID: grammarID(t, "deploy-token")}) {
		t.Errorf("the version-1 record's app workflow names the new container: version 1's route reads the project copy until the follow-up removes it, and rewriting it is what G4 forbids mid-update: %v", rec.Workflows["app"])
	}
}

// TestUpdateRefusesTheCopyInACodingAgentsSession: the copy is a cloud write,
// so a coding agent's session makes none of it and is given the one command to
// run at a person's own terminal, as every other cloud write in this CLI is.
//
// The step is driven on its own here rather than through the door: an agent
// session is the fixture, and what is asserted is that the refusal comes
// before anything is read or written — not one Secret Manager version, not one
// node of the database.
func TestUpdateRefusesTheCopyInACodingAgentsSession(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			for _, k := range agentMarkers {
				t.Setenv(k, "")
			}
			t.Setenv(marker, "1")

			sm := gcpfake.NewSecrets(t)
			sm.AllowAccess()
			sm.Seed(gcp.ProjectSecretID(initProjectName, "deploy-token"), nil, []byte("project-deploy-token"))
			db := gcpfake.NewRTDB(t)
			db.Set(budget.PathProject, initProjectName)
			db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
			lc := &localcfg.Config{
				Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
				Budget:          &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
				Endpoints:       localcfg.Endpoints{SecretManager: sm.URL + "/", NoAuth: true},
				WorkflowSecrets: map[string]config.ProjectSecret{"deploy-token": {AllowDataTo: []string{"acme/*"}}},
			}
			var w strings.Builder
			e := &initEngine{lc: lc, r: &initRun{o: &initOptions{door: updateDoor.name}, w: &w}}
			e.copyWorkflowSecretsToGrammar(t.Context())

			if !strings.Contains(w.String(), "Run `"+selfCommand()+" update` in your own terminal") {
				t.Errorf("the copy is not refused with the command to run at a person's own terminal:\n%s", w.String())
			}
			if _, found := fakeContainer(t, sm, grammarID(t, "deploy-token")); found {
				t.Errorf("%s was created in a coding agent's session", grammarID(t, "deploy-token"))
			}
			if got := sm.Accessed(); len(got) != 0 {
				t.Errorf("a coding agent's session read the value of %v", got)
			}
			if v := db.Value(creds.PathProjectSecrets()); v != nil {
				t.Errorf("a coding agent's session wrote %s (%v)", creds.PathProjectSecrets(), v)
			}
		})
	}
}
