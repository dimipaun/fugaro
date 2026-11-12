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
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
	r := newConvergeRigYAML(t, v4Node(), workflowSecretsYAML)
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

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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

// TestUpdateWorkflowSecretsAreTheUpgradesStepAlone: the two states the step
// writes nothing in, each with the fixture that writes under `fugaro update` —
// so the assertion is about the gate and not about a fixture that could never
// have written anything.
//
// The door is the setup one, which converges the same installation through the
// same engine: a project it has just created has no repository whose workflows
// a service reads, and the record is the upgrade's. The plan is --plan-only,
// and it is also where a coding agent's session reaches this engine (the loop
// plans an engine stage read-only, and a session applies none): `--yes`'s help
// promises that a session "changes nothing outside this machine", and a record
// published out of a run that applied nothing is exactly the write that
// promise does not have.
func TestUpdateWorkflowSecretsAreTheUpgradesStepAlone(t *testing.T) {
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
		(&initRun{o: o, w: &strings.Builder{}}).writeWorkflowSecrets(t.Context(), &infra.Clients{}, lc, ready, cfg)
		return db
	}

	t.Run("fugaro setup", func(t *testing.T) {
		db := call(t, &initOptions{door: setupDoor.name})
		if v := db.Value(creds.PathWorkflowSecrets(slug)); v != nil {
			t.Errorf("%s exists after a `fugaro setup` run (%v): the record is the upgrade's step, and a project the setup door just created has no repository a service reads", creds.PathWorkflowSecrets(slug), v)
		}
	})
	t.Run("a plan", func(t *testing.T) {
		db := call(t, &initOptions{door: updateDoor.name, planOnly: true})
		if v := db.Value(creds.PathWorkflowSecrets(slug)); v != nil {
			t.Errorf("%s exists after a plan (%v): --plan-only changes nothing outside this machine, and a coding agent's session reaches this engine the same way", creds.PathWorkflowSecrets(slug), v)
		}
	})
	t.Run("fugaro update, applied", func(t *testing.T) {
		db := call(t, &initOptions{door: updateDoor.name})
		if db.Value(creds.PathWorkflowSecrets(slug)) == nil {
			t.Fatalf("%s is not there after an update that applied: this is the fixture the two refusals above are refusals because of", creds.PathWorkflowSecrets(slug))
		}
		client, err := newBudgetClient(t.Context(), db.URL, true)
		if err != nil {
			t.Fatal(err)
		}
		rec, found, err := creds.ReadWorkflowSecrets(t.Context(), client, slug)
		if err != nil || !found {
			t.Fatalf("the record the update wrote does not read back: found=%v err=%v", found, err)
		}
		if got := rec.Workflows["app"]; len(got) != 1 || got[0].Env != "NPM_TOKEN" || got[0].ID != "fugaro-acme-app-npm-token-1a2b" {
			t.Errorf("the record's app workflow holds %v; want the container the fixture's spec mounts", got)
		}
	})
}
