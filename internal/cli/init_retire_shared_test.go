package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// M2 (D2303): `fugaro update` retires the WHOLE root of a repository a
// deployed profile admits — the migration itself. A repository that ran on its
// own jobs in the release behind keeps them until this update takes them apart:
// the state removal, the destroy plan behind the confirmation the update asks,
// the apply, and the API delete of the jobs no apply can finish.
//
// The addresses of that root, in the spelling the module tree writes them
// (deploy/terraform/gcp/roots/repo/main.tf:28, modules/repo/check.tf:20,
// modules/repo/workflows.tf:15).
const (
	sharedWorkflowJobAddr = `module.repo.module.workflow["app"].google_cloud_run_v2_job.this[0]`
	sharedJobSaAddr       = `module.repo.module.workflow["app"].google_service_account.job`
	sharedCheckJobAddr    = `module.repo.google_cloud_run_v2_job.check[0]`
	sharedBuildSaAddr     = `module.repo.google_service_account.build`
	sharedRegistryAddr    = `module.repo.google_artifact_registry_repository.images`
	sharedSecretAddr      = `module.repo.google_secret_manager_secret.this["claude-oauth-token"]`

	sharedWorkflowJobName = "fugaro-acme-sandbox-app"
	sharedCheckJobName    = "fugarochk-acme-sandbox"
	sharedOAuthName       = "claude-oauth-token"
	sharedRegion          = "us-east5"
)

// sharedRetireOpts are the rig's knobs: each one is a fact of the world the
// retirement is judged in, never a switch inside the program.
type sharedRetireOpts struct {
	// proxy is whether the published node forwards this repository's Bitbucket
	// token. False is M2's second structural fact missing: the repository has
	// no shared job that can clone it.
	proxy bool
	// nodeVersion is the version the published node states.
	nodeVersion int
	// noState is a repository with no state object of its own: the repository
	// that was never applied has no root to retire.
	noState bool
	// runningOn is a job an execution is still executing on: deleting it would
	// kill a person's run.
	runningOn string
	// emptyState is a root whose state object is there and manages nothing:
	// what a first shared run that stopped between its destroy and its forget
	// leaves behind.
	emptyState bool
	// unprotected is a state whose jobs say deletion_protection = false: the
	// plan deletes them and no API delete is owed.
	unprotected bool
	// forget is a versioned state bucket, so the emptied state object goes and
	// the repository is left the way one that was never applied is.
	forget bool
}

// sharedRetireRig is the world M2 is about: an installation that publishes a
// policy node admitting this repository on a shared job, whose repository has
// the whole root of the release behind it in its own state.
type sharedRetireRig struct {
	*initRig
	db    *gcpfake.RTDB
	slug  string
	label string
}

func newSharedRetireRig(t *testing.T, o sharedRetireOpts) *sharedRetireRig {
	t.Helper()
	if o.nodeVersion == 0 {
		o.nodeVersion = projectpolicy.Version
	}
	fakeTerminal(t)
	for _, k := range agentMarkers {
		t.Setenv(k, "") // this session may be a coding agent's
	}
	// S10's flip: the project's shared job carries a baked workflow's tools, so
	// a repository the profile admits runs on it whatever its own checkout says
	// (infra's sharedRepo). Without it the world is the one before the flip,
	// where the shared job has no workflow to run.
	infra.SharedJobBaked = true
	t.Cleanup(func() { infra.SharedJobBaked = false })
	r, db := proxiedConvergeRig(t)
	noProjectLayer(t)
	canonical := proxiedRepoCanonical(t)
	publishMigratedNode(t, db, o.nodeVersion, o.proxy, canonical)

	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case o.emptyState:
		writeRepoStateObject(t, r, slug, emptyStateDoc)
	case !o.noState:
		writeRepoStateObject(t, r, slug, sharedRootStateDoc(t, o))
	}
	if o.forget {
		// The bucket a fugaro installation creates keeps noncurrent versions,
		// which is what lets the emptied state object go at all
		// (infra.StateBucketVersioned, forgetRetiredRoot).
		r.gcs.SetVersioning(initStateBucket, true)
	}

	// Both of the repository root's reads, scripted apart: "show -json" with no
	// plan file is the state the retirement reads, and "show" is the plan.
	doc := sharedRootStateDoc(t, o)
	if o.emptyState {
		doc = emptyStateDoc
	}
	state := map[string]any{"stdout": doc}
	if o.forget {
		// The second read is forgetRetiredRoot's, after the apply: the state of
		// a root whose every address is gone.
		r.script["show -json@repo"] = []map[string]any{state, {"stdout": emptyStateDoc}}
	} else {
		r.script["show -json@repo"] = state
	}
	r.script["show@repo"] = map[string]any{"stdout": sharedDestroyPlan(t)}
	r.script["plan"] = map[string]any{"exit": 2} // the destroy plan changes something
	r.save(t)

	// The cloud the repository's jobs are in: the API delete looks them up by
	// the labels the module writes (modules/repo/check.tf:22-26,
	// modules/workflow/job.tf).
	r.run.SetJob(sharedWorkflowJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "app",
	}, "us-east5-docker.pkg.dev/proj-1234/fugaro/acme-sandbox-app:1")
	r.run.SetJob(sharedCheckJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelRole: gcp.RoleCheck,
	}, "us-east5-docker.pkg.dev/proj-1234/fugaro/acme-sandbox-check:1")
	if o.runningOn != "" {
		r.run.Start(o.runningOn)
	}

	t.Chdir(sharedRepoCheckout(t))
	return &sharedRetireRig{initRig: r, db: db, slug: slug, label: label}
}

// emptyStateDoc is a state object that manages nothing: the shape
// forgetRetiredRoot reads after the apply.
const emptyStateDoc = `{"format_version":"1.0","values":{"root_module":{"resources":[]}}}`

// publishMigratedNode is the published node of a project whose shared jobs
// serve this repository: the rules marker, the profile that admits it (M6),
// and — where the installation forwards the token — the bitbucket_token block
// with proxy: true (G15). One node carries both, which is the whole of what
// the retirement's precondition reads.
func publishMigratedNode(t *testing.T, db *gcpfake.RTDB, version int, proxy bool, repos ...string) {
	t.Helper()
	node := map[string]any{
		"version":     version,
		"allow_repos": repos,
		"profiles": map[string]any{
			projectpolicy.DefaultProfileName: map[string]any{
				"provider": "bitbucket", "cpu": 2, "memory": "8Gi", "timeout_class": "6h",
				"allow_repos": repos,
			},
		},
	}
	if proxy {
		node["bitbucket_token"] = map[string]any{"allow_repos": repos, "proxy": true}
	}
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), node)
}

// noProjectLayer points findLayer's bucket at a directory of this test's own:
// the rig's local config computes a gs:// runs bucket, which TestMain leaves
// panicking rather than reaching storage.googleapis.com, and this installation
// has published no layer (the bucket is empty, so none applies). The
// same seam repoBuildRig uses for its layer fixture.
func noProjectLayer(t *testing.T) {
	t.Helper()
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	dir := t.TempDir()
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+dir)
	}
}

// writeRepoStateObject puts the repository's own state in the installation's
// state bucket, where RepoStateObjects finds it: the object a migrated
// repository has and a repository that was never applied has not.
func writeRepoStateObject(t *testing.T, r *initRig, slug, doc string) {
	t.Helper()
	w, err := r.gcs.Bucket(t, initStateBucket).NewWriter(context.Background(), infra.RepoStatePrefix(slug)+"/default.tfstate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// sharedRootStateDoc is the repository root's state as the release behind it
// wrote it: the variant's job and its own account, the repository's build
// account and registry, the check job, and the OAuth container no workflow
// mounts any more.
func sharedRootStateDoc(t *testing.T, o sharedRetireOpts) string {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	protection := !o.unprotected
	workflow := []any{
		map[string]any{"address": sharedJobSaAddr, "mode": "managed", "type": "google_service_account",
			"name": "job", "values": map[string]any{"account_id": sharedWorkflowJobName}},
		map[string]any{"address": sharedWorkflowJobAddr, "mode": "managed", "type": "google_cloud_run_v2_job",
			"name": "this", "values": map[string]any{
				"name": sharedWorkflowJobName, "location": sharedRegion, "deletion_protection": protection}},
	}
	repo := []any{
		map[string]any{"address": sharedBuildSaAddr, "mode": "managed", "type": "google_service_account",
			"name": "build", "values": map[string]any{"account_id": "fugaro-build"}},
		map[string]any{"address": sharedRegistryAddr, "mode": "managed", "type": "google_artifact_registry_repository",
			"name": "images", "values": map[string]any{"repository_id": "acme-sandbox"}},
		map[string]any{"address": sharedSecretAddr, "mode": "managed", "type": "google_secret_manager_secret",
			"name": "this", "values": map[string]any{"secret_id": gcp.SecretID(slug, sharedOAuthName)}},
		map[string]any{"address": sharedCheckJobAddr, "mode": "managed", "type": "google_cloud_run_v2_job",
			"name": "check", "values": map[string]any{
				"name": sharedCheckJobName, "location": sharedRegion, "deletion_protection": protection}},
	}
	return stateDoc(t, repo, workflow)
}

// stateDoc is one state document with the repository's own resources under
// module.repo and the variant's under module.repo.module.workflow["app"].
func stateDoc(t *testing.T, repo, workflow []any) string {
	t.Helper()
	doc := map[string]any{
		"format_version": "1.0",
		"values": map[string]any{"root_module": map[string]any{
			"resources": []any{},
			"child_modules": []any{map[string]any{
				"address":   "module.repo",
				"resources": repo,
				"child_modules": []any{map[string]any{
					"address": `module.repo.module.workflow["app"]`, "resources": workflow,
				}},
			}},
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sharedDestroyPlan is the plan a whole root's retirement makes: the
// destruction of what the state still manages after the removal — the
// registry, the build account and the variant's own account. The jobs and the
// container are not here: the jobs are out of the state by then (the API
// delete finishes them) and the container is Kept.
func sharedDestroyPlan(t *testing.T) string {
	t.Helper()
	return planJSON(t,
		retireChange(sharedRegistryAddr, "google_artifact_registry_repository", "delete"),
		retireChange(sharedBuildSaAddr, "google_service_account", "delete"),
		retireChange(sharedJobSaAddr, "google_service_account", "delete"),
	)
}

// update drives the program's own door in the repository's checkout.
func (r *sharedRetireRig) update(t *testing.T) (string, error) {
	t.Helper()
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build")
	return out + errOut, err
}

// updateReadOnly is the same door with --plan-only: it writes the workdir, reads
// the state and prints the summary, and retires nothing — the one run that can
// tell a workdir's own file from what the binary ships.
func (r *sharedRetireRig) updateReadOnly(t *testing.T) (string, error) {
	t.Helper()
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build", "--plan-only")
	return out + errOut, err
}

// workdirFile is a path under the repository root's own workdir, which
// PrepareWorkdir rebuilds from the binary on every run.
func (r *sharedRetireRig) workdirFile(t *testing.T, name string) string {
	t.Helper()
	wd, err := infra.RepoWorkdir(os.Getenv, initProject, r.slug)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "gcp", name)
}

// registryTF is the workdir's copy of the repository module's registry: the
// one file LiftRegistryPreventDestroy rewrites, and the one PrepareWorkdir
// writes back at the start of every run.
func (r *sharedRetireRig) registryTF(t *testing.T) string {
	t.Helper()
	return r.workdirFile(t, filepath.Join("modules", "repo", "registry.tf"))
}

// stateObjects is what the installation's state bucket holds under this
// repository's prefix: the root that is still there, and nothing once the
// retirement has forgotten it.
func (r *sharedRetireRig) stateObjects(t *testing.T) []string {
	t.Helper()
	names, err := infra.RepoStateObjects(t.Context(), retireClients(t, r.initRig), initStateBucket, r.slug)
	if err != nil {
		t.Fatalf("listing the repository's state objects: %v", err)
	}
	return names
}

// retireCalls is the terraform subcommands the run made, one line each, with
// a state subcommand's own word kept.
func (r *sharedRetireRig) retireCalls(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, c := range r.calls(t) {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// TestUpdateRetiresTheRepoJobsOfAMigratedRepo: the migration itself. One
// `fugaro update` in the checkout of a repository a deployed profile admits,
// whose own root the release behind left behind, takes that root apart: the
// jobs and the account leave Terraform's state, the plan that could not run
// before (a destroy plan: an ordinary one would CREATE the jobs the repository
// no longer wants) carries the rest, the apply makes it so, and the jobs the
// state's own deletion_protection kept out of the plan are deleted through the
// Cloud Run API.
//
// The confirmation is `update`'s own and never a second command (CLAUDE.md's
// HARD RULE): the summary is printed, the person at the terminal is the
// confirmation the retirement has always asked for, and the run reaches its
// end with exit 0.
func TestUpdateRetiresTheRepoJobsOfAMigratedRepo(t *testing.T) {
	r := newSharedRetireRig(t, sharedRetireOpts{proxy: true, forget: true})
	out, err := r.update(t)
	if err != nil {
		t.Fatalf("one fugaro update did not retire a migrated repository's root: %v\n%s", err, out)
	}
	for _, want := range []string{
		"⚠ This change retires",
		"This is the repository's whole root: the apply destroys its image registry and every image in it, its jobs, its accounts and its grants.",
		"removed 3 addresses from Terraform's state; nothing was destroyed",
		"deleted the orphaned Cloud Run job " + sharedWorkflowJobName,
		"deleted the orphaned Cloud Run job " + sharedCheckJobName,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q:\n%s", want, out)
		}
	}
	// The plan is the destroy plan, and it is the only plan of this root: the
	// ordinary plan of a migrated repository's root would create the jobs it
	// no longer wants (retireSharedRoot).
	if !callsPlanDestroy(r.calls(t)) {
		t.Errorf("the retirement planned with no -destroy, so it planned the root the module still declares:\n%s", strings.Join(r.retireCalls(t), "\n"))
	}
	if n := len(r.ran(t, "apply")); n != 1 {
		t.Errorf("apply ran %d time(s), want the retirement's one: %q", n, r.retireCalls(t))
	}
	if n := len(r.ran(t, "state rm")); n != 3 {
		t.Fatalf("state rm ran %d time(s), want one per address the removal set holds: %q", n, r.retireCalls(t))
	}
	var removed []string
	for _, call := range r.ran(t, "state rm") {
		removed = append(removed, call[len(call)-1])
	}
	rm := strings.Join(removed, " ")
	for _, want := range []string{sharedWorkflowJobAddr, sharedCheckJobAddr, sharedSecretAddr} {
		if !strings.Contains(rm, want) {
			t.Errorf("the state removal does not name %s: %s", want, rm)
		}
	}
	if strings.Contains(rm, sharedRegistryAddr) {
		t.Errorf("the state removal took the registry out of the state, which leaves it alive with nothing managing it: the apply's own delete is what destroys it: %s", rm)
	}
	// Both jobs carry deletion_protection = true in the state, so no apply can
	// delete them and the API delete is what finishes them — looked up by the
	// labels the module writes, never by a name this program guessed.
	deletes := r.run.JobDeletes()
	if len(deletes) != 2 {
		t.Fatalf("the API delete ran for %v, want both of the repository's jobs", deletes)
	}
	for _, job := range []string{sharedWorkflowJobName, sharedCheckJobName} {
		if r.run.HasJob(job) {
			t.Errorf("%s is still there after the update", job)
		}
	}
	// The state object goes with the root, so the repository is left the way
	// one that was never applied is and the next update has nothing to retire.
	if names, err := infra.RepoStateObjects(t.Context(), retireClients(t, r.initRig), initStateBucket, r.slug); err != nil {
		t.Fatalf("listing the repository's state objects: %v", err)
	} else if len(names) != 0 {
		t.Errorf("the retired repository still holds %v in gs://%s", names, initStateBucket)
	}
}

// callsPlanDestroy is whether one of the run's plans was a destroy plan:
// tf.TF.PlanDestroy's own flag, read off the fake's log.
func callsPlanDestroy(calls [][]string) bool {
	for _, c := range calls {
		if len(c) > 0 && c[0] == "plan" && slices.Contains(c, "-destroy") {
			return true
		}
	}
	return false
}

// TestUpdateRetiringARootLeavesItsSecretContainers: the containers hold the
// values a returning repository-level secrets feature will want (M1, D2313),
// so the retirement takes them out of the root's state and destroys nothing:
// they are in the summary's KEPT list, the state removal names them, no plan
// deletes them, and Secret Manager is untouched afterwards.
func TestUpdateRetiringARootLeavesItsSecretContainers(t *testing.T) {
	r := newSharedRetireRig(t, sharedRetireOpts{proxy: true})
	id := gcp.SecretID(r.slug, sharedOAuthName)
	r.sm.Seed(id, map[string]string{gcp.LabelManaged: gcp.ManagedValue}, []byte(tokenValue))
	out, err := r.update(t)
	if err != nil {
		t.Fatalf("the update did not retire the root: %v\n%s", err, out)
	}
	if !strings.Contains(out, "KEPT — Fugaro never deletes a secret or a secret version:") {
		t.Errorf("the retirement does not say what it keeps:\n%s", out)
	}
	if !strings.Contains(out, "leaves Terraform's state, unmanaged; Secret Manager is untouched") {
		t.Errorf("a secret container is not in the KEPT list, so the retirement claims to destroy one:\n%s", out)
	}
	var removed []string
	for _, call := range r.ran(t, "state rm") {
		removed = append(removed, call[len(call)-1])
	}
	if !slices.Contains(removed, sharedSecretAddr) {
		t.Errorf("the state removal does not name %s, so the container is left in the state of a root nothing applies any more: %v", sharedSecretAddr, removed)
	}
	// The value is readable as it was: a container this run had destroyed would
	// answer nothing, and a version it had destroyed would be gone.
	if got := string(r.sm.Latest(id)); got != tokenValue {
		t.Errorf("the container's version reads %q after the retirement, want the value it held: Secret Manager is not this step's to write", got)
	}
	for _, gone := range r.sm.Destroyed() {
		t.Errorf("%s was destroyed by the retirement, which never deletes a secret version", gone)
	}
	if gone := r.sm.Deleted(); len(gone) != 0 {
		t.Errorf("the retirement deleted the secret container(s) %v, and Fugaro never deletes one (M1, D2313)", gone)
	}
}

// TestUpdateRetiringARootLiftsTheRegistryGuardForThatApplyOnly: the whole root
// is destroyed, the registry with it (F18: a retired root that left its
// registry behind would leave one per repository forever), and the module's
// lifecycle.prevent_destroy is what stands in the way — lifted in the workdir's
// copy of the module for this one apply, and read back by the next run, which
// rewrites that copy from the binary (PrepareWorkdir).
func TestUpdateRetiringARootLiftsTheRegistryGuardForThatApplyOnly(t *testing.T) {
	const guard = "prevent_destroy"
	r := newSharedRetireRig(t, sharedRetireOpts{proxy: true, forget: true})
	out, err := r.update(t)
	if err != nil {
		t.Fatalf("the update did not retire the root: %v\n%s", err, out)
	}
	if !strings.Contains(out, "the apply destroys its image registry and every image in it") {
		t.Errorf("the retirement does not name the registry it destroys:\n%s", out)
	}
	// The retiring run wrote the module's own copy at its start (PrepareWorkdir)
	// and lifted the block out of it for the plan and the apply, so the file left
	// on disk is the lifted one — the only way a plan could carry the registry's
	// delete at all.
	path := r.registryTF(t)
	if b, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(b), guard) {
		t.Errorf("%s still carries %s after the retiring apply, so the plan could not have destroyed the registry: the lift is the whole reason the apply can write that delete\ncalls:\n%s\nout:\n%s", path, guard, strings.Join(r.retireCalls(t), "\n"), out)
	}
	// The next update of the same workdir reads the block back, because
	// PrepareWorkdir writes the module from the binary at the start of every run:
	// the lift is one apply's, never a change to fugaro's own tree. Nothing of
	// the root is left to retire — the state object went with it — so this run
	// reaches terraform not at all.
	before := len(r.calls(t))
	if out, err := r.update(t); err != nil {
		t.Fatalf("the second update failed: %v\n%s", err, out)
	}
	if b, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(b), guard) {
		t.Errorf("%s lost its %s for good: the guard is lifted in the workdir's copy for one apply, and PrepareWorkdir writes the module back on every run", path, guard)
	}
	if calls := r.calls(t)[before:]; callsHave(calls, "plan") || callsHave(calls, "apply") || callsHave(calls, "state") {
		t.Errorf("a run over a retired root reached terraform, and nothing of that repository is left to plan: %q", calls)
	}
}

// TestUpdateDoesNotRetireBeforeThePrecondition: the acceptance's structural
// precondition, and the mutation check the review asks for — with a fact of the
// world missing, the update says why and leaves the repository's jobs exactly
// where they are. The two facts this flow judges before it asks about
// executions are the project's image for its shared jobs, which a repository
// would have nowhere to run without, and the Bitbucket proxy, without which no
// shared job can clone the repository. The third — an execution running on a
// job — is the test below.
func TestUpdateDoesNotRetireBeforeThePrecondition(t *testing.T) {
	// The proxy: a whole update over a repository whose token the installation
	// does not forward. Nothing of the root goes, and the run goes on to do the
	// rest of the update — a hold is a note, never a failure.
	t.Run("the Bitbucket proxy is off", func(t *testing.T) {
		r := newSharedRetireRig(t, sharedRetireOpts{})
		out, err := r.update(t)
		if err != nil {
			t.Fatalf("the update failed over a repository it may not retire: %v\n%s", err, out)
		}
		want := "note: acme/sandbox's own root is not retired here: the Bitbucket proxy is off; a later update retires it once that holds"
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q:\n%s", want, out)
		}
		for _, sub := range []string{"state rm", "apply"} {
			if got := r.ran(t, sub); len(got) != 0 {
				t.Errorf("terraform %s ran for a held retirement: %q", sub, got)
			}
		}
		if callsPlanDestroy(r.calls(t)) {
			t.Errorf("the run planned the destroy of a root it may not retire: %q", r.retireCalls(t))
		}
		for _, job := range []string{sharedWorkflowJobName, sharedCheckJobName} {
			if !r.run.HasJob(job) {
				t.Errorf("%s is gone: a held retirement deletes no job", job)
			}
		}
		if len(r.run.JobDeletes()) != 0 {
			t.Errorf("the run deleted %v through the API", r.run.JobDeletes())
		}
		if names := r.stateObjects(t); len(names) == 0 {
			t.Errorf("the state object is gone, so the next update has nothing to retire")
		}
	})
	// The image: an installation whose config names no base image and whose
	// catalog marks no default. No checkout this program loads can reach that
	// state — a config that names a base image is what an installation is
	// written with (D2305), and a project layer that stopped naming one is
	// refused before any repository is — so the precondition is asked here, of
	// the hold itself. A repository whose shared job would run the empty image
	// has no shared job at all, which is the whole reason the hold is asked.
	t.Run("the project's image does not resolve", func(t *testing.T) {
		e, out := stageEngine(t, "", nil)
		lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: sharedRegion}
		spec := infra.RepoSpec{Name: "acme/sandbox", Provider: "bitbucket", BitbucketProxied: true}
		holds, err := e.r.rootRetireHolds(t.Context(), nil, lc, spec, nil)
		if err != nil {
			t.Fatalf("the precondition's read failed: %v", err)
		}
		if holds {
			t.Fatalf("the retirement went ahead with no image for the project's shared jobs: the repository would have nowhere to run")
		}
		want := "note: acme/sandbox's own root is not retired here: the project's image for its shared jobs does not resolve; a later update retires it once that holds"
		if !strings.Contains(out.String(), want) {
			t.Errorf("the hold does not say %q:\n%s", want, out.String())
		}
	})
}

// TestUpdateDoesNotRetireAJobWithARunningExecution: a job an execution is
// running on is a person's work, and deleting the job kills the run with it
// (infra.InFlightJobs). The update names the job and leaves the whole root
// alone — the later update retires it once that run has finished.
func TestUpdateDoesNotRetireAJobWithARunningExecution(t *testing.T) {
	r := newSharedRetireRig(t, sharedRetireOpts{proxy: true, runningOn: sharedWorkflowJobName})
	out, err := r.update(t)
	if err != nil {
		t.Fatalf("the update failed over a repository whose job is running a run: %v\n%s", err, out)
	}
	want := "note: acme/sandbox's own root is not retired here: a run is executing on " + sharedWorkflowJobName + "; a later update retires it once that run has finished"
	if !strings.Contains(out, want) {
		t.Errorf("the run does not say %q:\n%s", want, out)
	}
	if callsPlanDestroy(r.calls(t)) || len(r.ran(t, "state rm")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Errorf("the retirement ran over a job with a run in flight: %q", r.retireCalls(t))
	}
	for _, job := range []string{sharedWorkflowJobName, sharedCheckJobName} {
		if !r.run.HasJob(job) {
			t.Errorf("%s is gone, and deleting it takes the execution running on it with it", job)
		}
	}
	if len(r.run.JobDeletes()) != 0 {
		t.Errorf("the update deleted %v through the API", r.run.JobDeletes())
	}
}

// TestUpdateRepairsAFailedFirstSharedRun: a first shared run that stopped
// between its destroy and its forget leaves a state object that manages
// nothing. The next update finishes the job: the emptied object goes, so the
// repository is left the way one that was never applied is and the door's note
// about the runs in flight stops naming a retirement it does not owe. Nothing
// else is touched — there is nothing of the root left to take apart, and a job
// delete is not this step's.
func TestUpdateRepairsAFailedFirstSharedRun(t *testing.T) {
	r := newSharedRetireRig(t, sharedRetireOpts{proxy: true, emptyState: true, forget: true})
	out, err := r.update(t)
	if err != nil {
		t.Fatalf("the update did not repair a run that stopped mid-retirement: %v\n%s", err, out)
	}
	if names := r.stateObjects(t); len(names) != 0 {
		t.Errorf("the emptied state object %v is still in gs://%s, so the next update reads the repository as one with a root to retire", names, initStateBucket)
	}
	for _, sub := range []string{"state rm", "plan", "apply"} {
		if got := r.ran(t, sub); len(got) != 0 {
			t.Errorf("terraform %s ran over a root whose state manages nothing: %q", sub, got)
		}
	}
	if got := r.run.JobDeletes(); len(got) != 0 {
		t.Errorf("the repair deleted %v through the Cloud Run API, and a root with no state left is not a root to take apart", got)
	}
	for _, job := range []string{sharedWorkflowJobName, sharedCheckJobName} {
		if !r.run.HasJob(job) {
			t.Errorf("%s is gone", job)
		}
	}
}

// TestUpdatePreviousVersionConvergesToSharedJobs: the installation the release
// behind left — a node written one version back that admits this repository,
// the per-repository root still deployed — converges in ONE update, which is
// the HARD RULE: the node's version is read carried forward (repoPolicyRead),
// so the same run that enrols the repository also takes its old root apart. The
// second update has nothing left to do about it.
func TestUpdatePreviousVersionConvergesToSharedJobs(t *testing.T) {
	r := newSharedRetireRig(t, sharedRetireOpts{proxy: true, nodeVersion: projectpolicy.Version - 1, forget: true})
	out, err := r.update(t)
	if err != nil {
		t.Fatalf("one update did not converge an installation at the previous version: %v\n%s", err, out)
	}
	for _, want := range []string{
		"This is the repository's whole root: the apply destroys its image registry and every image in it, its jobs, its accounts and its grants.",
		"deleted the orphaned Cloud Run job " + sharedWorkflowJobName,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q:\n%s", want, out)
		}
	}
	if n := len(r.ran(t, "apply")); n != 1 {
		t.Errorf("apply ran %d time(s), want the retirement's one: %q", n, r.retireCalls(t))
	}
	if names := r.stateObjects(t); len(names) != 0 {
		t.Errorf("the retired repository still holds %v in gs://%s", names, initStateBucket)
	}
	// The repository is enrolled by the same run: the local config is the record
	// of that (M7), and the second update is the one that must find nothing to
	// do.
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := lc.Repos["acme/sandbox"]; len(got.Workflows) != 1 || got.Workflows[0] != "app" {
		t.Errorf("the local config records %+v, want the repository this run enrolled", got)
	}
	before := len(r.calls(t))
	if out, err := r.update(t); err != nil {
		t.Fatalf("the second update failed: %v\n%s", err, out)
	}
	if calls := r.calls(t)[before:]; callsPlanDestroy(calls) || callsHave(calls, "state rm") {
		t.Errorf("the second update retired something: %q", calls)
	}
}

// callsHave is whether the run made a terraform subcommand: the same reading
// ran does, over a slice of calls a test has already taken.
func callsHave(calls [][]string, sub string) bool {
	for _, c := range calls {
		if len(c) > 0 && c[0] == sub {
			return true
		}
	}
	return false
}
