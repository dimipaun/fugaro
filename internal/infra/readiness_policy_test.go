package infra

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// readyJob makes every workflow of spec deployable in every way but the
// project policy's own requirement: each secret it mounts has an enabled
// version and each image is built, so only the gate under test can hold a job
// back. spec must be built with no Firebase configured (budgetInputs(t, "")).
func readyJob(t *testing.T, f *cloud, spec RepoSpec) {
	t.Helper()
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	for logical, id := range spec.Secrets {
		f.sm.Seed(id, with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("v"))
	}
	f.ar.AddRepository(spec.GCPProject, "us-east5", spec.Registry.RepositoryID, with(managed, gcp.LabelRepo, spec.Label))
	for _, ws := range spec.Workflows {
		pkg := strings.TrimSuffix(strings.TrimPrefix(ws.Image, spec.RegistryPath+"/"), ":latest")
		f.ar.SetTag(spec.GCPProject, "us-east5", spec.Registry.RepositoryID, pkg, "latest")
	}
}

// readinessPolicy is readinessOf with the fact discovery cannot see: whether
// the project has published a policy.
func readinessPolicy(t *testing.T, f *cloud, spec RepoSpec, policy bool) (RepoSpec, []Missing) {
	t.Helper()
	_, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	ex.PolicyExists = policy
	got, missing, err := Readiness(context.Background(), f.c, spec, ex)
	if err != nil {
		t.Fatal(err)
	}
	return got, missing
}

// D11 (plan task 1.4 step 2): a job that carries neither FUGARO_RTDB_URL nor
// FUGARO_FIREBASE_API_KEY cannot read a published policy, and the runner
// cannot tell that apart from "no policy" — it would run on the environment
// ceiling with the project's policy unenforced. Once a policy exists this
// gates the job.
func TestReadinessBlocksAJobThatCannotReadAPublishedPolicy(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(budgetInputs(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	readyJob(t, f, spec)
	got, missing := readinessPolicy(t, f, spec, true)
	for name, ws := range got.Workflows {
		if ws.DeployJob {
			t.Errorf("workflow %s is deployed though no run of it could read the project's policy: %v", name, missing)
		}
	}
	gate := ""
	for _, m := range missing {
		if m.Kind == MissingFirebase {
			gate = m.Reason
		} else if m.Kind != MissingSecret && m.Kind != MissingImage {
			t.Errorf("unexpected gate %v", m)
		}
	}
	if gate == "" {
		t.Fatalf("missing = %v, want a Firebase gate for each workflow", missing)
	}
	for _, want := range []string{RTDBURLEnv, FirebaseAPIKeyEnv, projectpolicy.PathShared(), "fugaro init --repo"} {
		if !strings.Contains(gate, want) {
			t.Errorf("the gate does not say %q: %s", want, gate)
		}
	}
	// The check job calls no model and reads no policy: it is not gated by a
	// requirement about reading one.
	if !got.Check.DeployJob {
		t.Errorf("the check job is not deployed: %v", missing)
	}
}

// L3 of the PR #379 review: a local config without budget.rtdb_url deploys
// jobs without the Firebase backend, and an apply that removes it from a job
// that has it takes the project's policy away from every run of the repository
// — with no other signal, because a CLI with no database address cannot see
// whether a policy exists (PolicyExists is false either way). The apply is not
// blocked (the harm is drift, and the owner may be decommissioning on purpose)
// but it says so, names the key and gives the command that puts it back.
func TestReadinessWarnsWhenAnApplyWouldRemoveTheJobFirebaseBackend(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(budgetInputs(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	readyJob(t, f, spec)
	// The deployed jobs carry what this config no longer names: the markup
	// of an installation that ran init --firebase, met again by a config
	// that has lost its budget.rtdb_url.
	for _, ws := range spec.Workflows {
		f.run.SetJob(ws.Job, ws.Labels, ws.Image)
		f.run.SetJobEnv(ws.Job, map[string]string{
			RTDBURLEnv:        "https://aurora-fp-default-rtdb.firebaseio.com",
			FirebaseAPIKeyEnv: "AIzaSyFakeWebApiKeyForTests000000000",
		})
	}
	got, missing := readinessPolicy(t, f, spec, false)
	var notes []string
	for _, m := range missing {
		if m.Kind == MissingNote {
			notes = append(notes, m.Reason)
		}
	}
	for name, ws := range got.Workflows {
		if !ws.DeployJob {
			t.Errorf("workflow %s is not deployed: %v", name, missing)
		}
		if !slices.ContainsFunc(notes, func(r string) bool {
			return strings.Contains(r, name) && strings.Contains(r, RTDBURLEnv) &&
				strings.Contains(r, FirebaseAPIKeyEnv) && strings.Contains(r, "budget.rtdb_url") &&
				strings.Contains(r, "fugaro init --firebase") && strings.Contains(r, "init --repo")
		}) {
			t.Errorf("no note tells workflow %s's owner that this apply removes the backend: %v", name, notes)
		}
	}
}

// The same apply with no backend in the jobs is silent: a legacy installation
// must not be warned about a model it never adopted.
func TestReadinessSaysNothingAboutFirebaseWhenTheJobsHaveNone(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(budgetInputs(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	readyJob(t, f, spec)
	for _, ws := range spec.Workflows {
		f.run.SetJob(ws.Job, ws.Labels, ws.Image)
	}
	_, missing := readinessPolicy(t, f, spec, false)
	if slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingNote && strings.Contains(m.Reason, "budget.rtdb_url")
	}) {
		t.Fatalf("missing = %v, want no Firebase note for a job that never had the backend", missing)
	}
}

// Before a policy exists the same job is not blocked: an installation that has
// not adopted the model keeps working exactly as it did, and the one that wants
// the shared budget hears what adopting it will need.
func TestReadinessWarnsBeforeAPolicyExists(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(budgetInputs(t, "budget: { mode: observe, per_run_usd: 5 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	readyJob(t, f, spec)
	got, missing := readinessPolicy(t, f, spec, false)
	for name, ws := range got.Workflows {
		if !ws.DeployJob {
			t.Errorf("workflow %s is not deployed though the project has published no policy: %v", name, missing)
		}
	}
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingNote && strings.Contains(m.Reason, RTDBURLEnv) && strings.Contains(m.Reason, FirebaseAPIKeyEnv)
	}) {
		t.Fatalf("missing = %v, want a note naming both variables", missing)
	}
	if slices.ContainsFunc(missing, func(m Missing) bool { return m.Kind == MissingFirebase }) {
		t.Errorf("missing = %v, want no gate before a policy exists", missing)
	}
}
