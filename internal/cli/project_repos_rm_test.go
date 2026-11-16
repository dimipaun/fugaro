package cli

// RE4's tests: `fugaro project repos rm` is the inverse of the converge
// `fugaro project repos add` runs, store by store, and every test here is about
// one of those stores rather than about the command having run.
//
// The world is the converge's own (accounts_add_converge_fake_test.go's rig:
// the installation rig plus the project's database and this project's own
// endpoints), holding what the six stores hold for one repository — the
// published allow-list entry, this machine's `repos:` entry and its
// `image_map:` entry, the published admission `config/policy/repos/<slug>`, the
// repository's Cloud Run jobs, and its state objects in the installation's
// state bucket — and a second repository that must come out of the removal
// untouched, so that every assertion is two-sided: the store lost one entry and
// kept the other.
//
// Hermetic, as the plan requires: every cloud answer is internal/gcpfake's, the
// files are t.TempDir()'s, and nothing here reaches the network, a home
// directory or a real project. The state bucket is the installation's own fake,
// with object versioning on, because the removal refuses to delete a
// repository's state out of a bucket whose versioning is off.
//
// The confirmation is the owner's own: nothing on the command line answers it
// (F5, D2403 — `--yes` is gone), so every run below stands at both streams and
// types the project's name.

import (
	"context"
	"io"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	rmRepoA  = "owner/repo-a"
	rmRepoB  = "owner/repo-b"
	rmEntryA = "github/owner/repo-a"
	rmEntryB = "github/owner/repo-b"
	// rmRegion is the region the fixture's config names, which is where its
	// Cloud Run jobs live.
	rmRegion = "us-east5"
	// rmRunID is the run the fixture's registry holds for rmRepoA when a test
	// wants one in flight.
	rmRunID = "run-4f2a1c"
	// rmSecret is the logical name of the repository's own secret container,
	// the one resource of its six that `rm` must not reach at all.
	rmSecret = "github-app-key"
)

// rmRigLocal is the fixture's local config: the converge rig's own world with
// the two halves this command needs. `terraform.state_bucket` is what makes
// this machine's config say where the repository's state and its jobs are —
// without it `rm` looks at no cloud resource at all — and the accounts block
// beside the two `repos:` entries is what admits each repository through the
// config rather than through a profile, which is the derivation
// `config/policy/repos/<slug>` is computed from (DeriveAccounts,
// deriveRepoAccounts): removing `repos.owner/repo-a` is what takes its
// admission away, and `repos.owner/repo-b` is what keeps repo-b's.
//
// The profile names no account and admits repo-b alone, so nothing but the two
// `repos:` entries decides the two children: a profile's own `allow_repos`
// reaches the accounts block only for the account axis's two non-account
// values, and this fixture names neither.
const rmRigLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
terraform: { state_bucket: fugaro-tfstate-proj-1234 }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-b
claude_accounts:
  acme-personal: { allow_data_to: [owner/repo-a] }
  acme-work: { allow_data_to: [owner/repo-b] }
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web], claude_accounts: [acme-personal] }
  owner/repo-b: { provider: github, github_app_id: "12345", workflows: [web], claude_accounts: [acme-work] }
image_catalog:
  entries:
    - name: base
      tags: [node]
      default: true
      source: { kind: inline, inline: "FROM scratch\n" }
image_map:
  owner/repo-a: base
  owner/repo-b: base
`

// rmGone names the stores an earlier, half-done removal already emptied, so one
// fixture can stand for the state that failure leaves without a test deleting
// anything from the fakes by hand.
type rmGone struct {
	admission bool
	jobs      bool
	state     bool
}

// rmRig is the fixture: the converge's world with the six stores seeded.
type rmRig struct {
	*addRig
	slugA, slugB   string
	labelA, labelB string
}

// rmSlug is the storage slug of one of the fixture's repositories.
func rmSlug(t *testing.T, repo string) string {
	t.Helper()
	slug, err := task.Slug("github", repo)
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

// rmLabel is the GCP label of one of the fixture's repositories.
func rmLabel(t *testing.T, repo string) string {
	t.Helper()
	return repoLabel(t, rmSlug(t, repo))
}

// rmRigFor is the fixture, ready to run `rm`. local is the fixture's own file
// when a test wants one that differs from rmRigLocal, and "" means rmRigLocal
// itself; gone names the stores that are already empty.
func rmRigFor(t *testing.T, local string, gone rmGone) *rmRig {
	t.Helper()
	if local == "" {
		local = rmRigLocal
	}
	r := &rmRig{
		addRig: newAddRigWith(t, local),
		slugA:  rmSlug(t, rmRepoA),
		slugB:  rmSlug(t, rmRepoB),
	}
	r.labelA, r.labelB = rmLabel(t, rmRepoA), rmLabel(t, rmRepoB)
	r.seed(t, gone)
	return r
}

// seed fills the six stores for rmRepoA and the ones that must survive for
// rmRepoB.
func (r *rmRig) seed(t *testing.T, gone rmGone) {
	t.Helper()
	// The published node: two allow-list entries, at this Fugaro's version,
	// with the budget block the local config agrees with. This is what a
	// publish leaves behind, and `rm` edits exactly this node's allow_repos.
	r.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{rmEntryA, rmEntryB},
	})
	// The two admissions a publish derives from the two `repos:` entries. They
	// are seeded rather than published because publishing is another command
	// with its own confirmation; the shape is admissionBytes' own
	// ({"credentials":{"accounts":[...]}}), which is what DecodeRepo reads.
	if !gone.admission {
		r.db.Set(projectpolicy.PathRepo(r.slugA), map[string]any{
			"credentials": map[string]any{"accounts": []string{"acme-personal"}},
		})
	}
	r.db.Set(projectpolicy.PathRepo(r.slugB), map[string]any{
		"credentials": map[string]any{"accounts": []string{"acme-work"}},
	})
	// The state bucket, versioned, and the repository's state objects: the
	// `.tfstate` is what the removal deletes, and the lock beside it is not a
	// state object (isState), so it is what the removal must leave.
	r.stateBucket()
	r.gcs.SetVersioning(initStateBucket, true)
	if !gone.state {
		r.gcs.Put(initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tfstate", []byte(`{"version":4}`))
	}
	r.gcs.Put(initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tflock", []byte("lock"))
	r.gcs.Put(initStateBucket, infra.RepoStatePrefix(r.slugB)+"/default.tfstate", []byte(`{"version":4}`))
	// The repository's two Cloud Run jobs, labelled as the repository root
	// labels them (OrphanLabels plus the label each kind is judged by), and one
	// of the other repository's, which no path here may touch.
	if !gone.jobs {
		specA := infra.RepoSpec{Name: rmRepoA, Slug: r.slugA, Label: r.labelA, GCPProject: initProject, Region: rmRegion}
		webA := infra.OrphanLabels(specA)
		webA[gcp.LabelWorkflow] = "web"
		r.run.SetJob(gcp.JobName(r.slugA, "web"), webA, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc")
		chkA := infra.OrphanLabels(specA)
		chkA[gcp.LabelRole] = gcp.RoleCheck
		r.run.SetJob(gcp.CheckJobName(r.slugA), chkA, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc")
	}
	specB := infra.RepoSpec{Name: rmRepoB, Slug: r.slugB, Label: r.labelB, GCPProject: initProject, Region: rmRegion}
	webB := infra.OrphanLabels(specB)
	webB[gcp.LabelWorkflow] = "web"
	r.run.SetJob(gcp.JobName(r.slugB, "web"), webB, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc")
	// The repository's own secret container: labelled with the repository's
	// label and the logical name, holding a version. It is the one resource of
	// the repository that a whole-repository read turns up and that no path of
	// this command may delete (D2406, design §6).
	specA := infra.RepoSpec{Name: rmRepoA, Slug: r.slugA, Label: r.labelA, GCPProject: initProject, Region: rmRegion}
	secret := infra.OrphanLabels(specA)
	secret[gcp.LabelSecret] = rmSecret
	r.sm.Seed("fugaro-"+r.slugA+"-"+rmSecret, secret, []byte(strings.Repeat("s", 32)))
}

// rm is the command the way the owner runs it: a real terminal on both streams,
// the project's name typed at the one question, and nothing on the command line
// standing in for it.
func (r *rmRig) rm(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ownerAtTheTerminal(t)
	return executeOwnerStdin(t, initProjectName+"\n", append([]string{"project", "repos", "rm"}, args...)...)
}

// rmOut is rm for the runs that must succeed.
func (r *rmRig) rmOut(t *testing.T, args ...string) (string, string) {
	t.Helper()
	out, errOut, err := r.rm(t, args...)
	if err != nil {
		t.Fatalf("project repos rm %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
	}
	return out, errOut
}

// rmLocal is this machine's project config as the command left it, decoded
// strictly: every assertion about the local half is made against the file.
func (r *rmRig) rmLocal(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// rmWarning is what the run printed before the one question: the half of D2406
// that has to be there before anything is written.
func rmWarning(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "⚠ CONFIRM")
	if i < 0 {
		t.Fatalf("the one confirmation was not asked:\n%s", out)
	}
	return out[:i]
}

// rmKeptPhrases are the six things D2406 says the removal leaves, each as the
// one phrase of its sentence in reposRemovalLeaves that names the thing: the
// list is what the operator is told, and pinning the whole sentence would fail
// on a wording change that lost nothing.
var rmKeptPhrases = []string{
	"the provider's App installation",
	"the built images",
	"the secrets and their versions",
	"the run history in the runs bucket",
	"every pull request a run opened",
	"the remote branch",
}

// TestReposRmRemovesTheSixAndNamesTheSixItLeaves is the red step of RE4
// (D2405, D2406): the whole removal, store by store, and the whole list of what
// it leaves, said twice — before the confirmation and after the write.
//
// Every removal is asserted on the fixture and not on the output: the published
// node's allow_repos, this machine's `repos:` entry and its `image_map:` entry,
// the published admission of repo-a, the repository's two Cloud Run jobs and
// the `.tfstate` under its state prefix. Every one of them has a second half
// that must survive — repo-b's entry, admission, job and state, and the lock
// object beside the state — because a removal that took the whole document
// would pass a one-sided test.
func TestReposRmRemovesTheSixAndNamesTheSixItLeaves(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	out, errOut := r.rmOut(t, rmEntryA)
	// stderr carries the project line every cloud command prints; what it must
	// not carry is a warning, which is how the retirement's own reads degrade
	// loudly when a permission is missing (Orphans).
	if strings.Contains(errOut, "warning:") {
		t.Fatalf("the removal warned on stderr:\n%s", errOut)
	}

	// D2406 said twice: the kept list is in the pre-write warning and in the
	// sentence after the write, and both are the same six things.
	warning := rmWarning(t, out)
	for _, want := range rmKeptPhrases {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning before the confirmation does not name %q:\n%s", want, warning)
		}
	}
	after := out[strings.Index(out, "⚠ CONFIRM"):]
	for _, want := range rmKeptPhrases {
		if !strings.Contains(after, want) {
			t.Errorf("the output after the write does not name %q:\n%s", want, after)
		}
	}
	// The warning is removalWarning's own sentence about what the removal
	// stops: repo-b's entry does not admit repo-a, so its runs are refused.
	if !strings.Contains(warning, "this removes "+rmEntryA+" from the allow-list: its runs are refused from now on") {
		t.Errorf("the warning does not say what the removal stops:\n%s", warning)
	}

	// 1. the published allow-list entry.
	if allow := allowReposOf(t, r.db); len(allow) != 1 || allow[0] != rmEntryB {
		t.Errorf("allow_repos = %v, want [%s]", allow, rmEntryB)
	}
	// 2. this machine's repos: entry, and 3. its image_map entry.
	lc := r.rmLocal(t)
	if _, ok := lc.Repos[rmRepoA]; ok {
		t.Errorf("the local config still holds repos.%s: %v", rmRepoA, lc.Repos)
	}
	if _, ok := lc.Repos[rmRepoB]; !ok {
		t.Errorf("the local config lost repos.%s as well: %v", rmRepoB, lc.Repos)
	}
	if _, ok := lc.ImageMap[rmRepoA]; ok {
		t.Errorf("the local config still maps %s: %v", rmRepoA, lc.ImageMap)
	}
	if got := lc.ImageMap[rmRepoB]; got != "base" {
		t.Errorf("image_map[%s] = %q, want base", rmRepoB, got)
	}
	// 4. the published admission, through the publish's own removal path.
	if v := r.db.Value(projectpolicy.PathRepo(r.slugA)); v != nil {
		t.Errorf("%s still holds %v", projectpolicy.PathRepo(r.slugA), v)
	}
	if v := r.db.Value(projectpolicy.PathRepo(r.slugB)); v == nil {
		t.Errorf("%s was removed too", projectpolicy.PathRepo(r.slugB))
	}
	// 5. the Cloud Run jobs, both of them, and neither of repo-b's.
	if deletes := r.run.JobDeletes(); len(deletes) != 2 {
		t.Errorf("job deletes = %v, want the repository's two", deletes)
	}
	if r.run.HasJob(gcp.JobName(r.slugA, "web")) || r.run.HasJob(gcp.CheckJobName(r.slugA)) {
		t.Errorf("a job of %s survived", rmRepoA)
	}
	if !r.run.HasJob(gcp.JobName(r.slugB, "web")) {
		t.Errorf("the removal deleted %s's job", rmRepoB)
	}
	// 6. the state, and not the lock beside it.
	if rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tfstate") {
		t.Errorf("the state object of %s survived", rmRepoA)
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tflock") {
		t.Errorf("the lock object of %s was deleted with the state", rmRepoA)
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugB)+"/default.tfstate") {
		t.Errorf("the state of %s was deleted too", rmRepoB)
	}

	// The one thing this door cannot reach, named rather than left to be
	// discovered: the resources that are the repository root's own.
	if !strings.Contains(out, "the repository's Scheduler job and the grants of its job account") {
		t.Errorf("the output does not name what the repository root's own apply removes:\n%s", out)
	}
}

// TestReposRmIsStrictWhileARunIsGoing is D2407: while a run is going, the
// confirmation is the strict one, the sentence names the run, and no flag
// answers it — the removal takes away the job that run is executing, so a
// pre-typed answer must not be what stops it.
func TestReposRmIsStrictWhileARunIsGoing(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	rmPutRunInFlight(t, r)
	ownerAtTheTerminal(t)
	// The run's own answer is what confirms the ordinary removal; while a run
	// is going it is not an answer at all.
	out, _, err := executeOwnerStdin(t, "yes\n", "project", "repos", "rm", rmEntryA)
	if err == nil || !strings.Contains(err.Error(), "not confirmed (the project's name was not typed); nothing was changed") {
		t.Fatalf("err = %v, want the refusal of an answer that is not the project's name\nstdout:\n%s", err, out)
	}
	// The confirmation names the run and what it keeps, which is the whole of
	// what makes it the strict one: the question is the same question, and
	// what changed is that the answer above was not an answer.
	if !strings.Contains(out, rmRunID) {
		t.Errorf("the strict confirmation does not name the run %s:\n%s", rmRunID, out)
	}
	if !strings.Contains(out, "its job, its record and its ledger stay until they end") {
		t.Errorf("the strict confirmation does not say what the run keeps:\n%s", out)
	}
	// With no terminal at all, the refusal is the strict one and names no flag:
	// a refusal that named one would be read by whoever it refuses as the thing
	// to pass next, and there is no such flag.
	noTerminal(t)
	out, _, err = executeStdin(t, "", "project", "repos", "rm", rmEntryA)
	if err == nil || !strings.Contains(err.Error(), "typing the project's name is the only thing that confirms this one while a run is in flight") {
		t.Fatalf("err = %v, want the strict refusal\nstdout:\n%s", err, out)
	}
	if strings.Contains(err.Error(), "--") {
		t.Errorf("the strict refusal names a flag: %v", err)
	}
	// Nothing changed, in any of the six stores.
	if allow := allowReposOf(t, r.db); len(allow) != 2 {
		t.Errorf("allow_repos = %v after a refused confirmation", allow)
	}
	if lc := r.rmLocal(t); len(lc.Repos) != 2 || len(lc.ImageMap) != 2 {
		t.Errorf("the local config changed after a refused confirmation: %d repos, %d image_map entries", len(lc.Repos), len(lc.ImageMap))
	}
	if r.db.Value(projectpolicy.PathRepo(r.slugA)) == nil {
		t.Errorf("the admission of %s was removed after a refused confirmation", rmRepoA)
	}
	if !r.run.HasJob(gcp.JobName(r.slugA, "web")) || !r.run.HasJob(gcp.CheckJobName(r.slugA)) {
		t.Errorf("a job was deleted after a refused confirmation")
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tfstate") {
		t.Errorf("the state was deleted after a refused confirmation")
	}

	// No flag stands in: not the answer T11 removed, and not the one D2408
	// says this command does not have. Both are refused by the command line
	// itself, before anything is read.
	for _, flag := range []string{"--yes", "--purge"} {
		ownerAtTheTerminal(t)
		out, _, err := executeOwnerStdin(t, initProjectName+"\n", "project", "repos", "rm", rmEntryA, flag)
		if err == nil || !strings.Contains(err.Error(), "unknown flag: "+flag) {
			t.Errorf("%s: err = %v, want an unknown flag\nstdout:\n%s", flag, err, out)
		}
	}

	// And the typed name still confirms it: the strict question is a question,
	// not a refusal of the command.
	r2 := rmRigFor(t, "", rmGone{})
	rmPutRunInFlight(t, r2)
	out2, _ := r2.rmOut(t, rmEntryA)
	if !strings.Contains(out2, "run(s) "+rmRunID+" are going on "+rmRepoA) {
		t.Errorf("the typed name did not carry the strict confirmation:\n%s", out2)
	}
	if v := r2.db.Value(projectpolicy.PathRepo(r2.slugA)); v != nil {
		t.Errorf("%s still holds %v after the typed confirmation", projectpolicy.PathRepo(r2.slugA), v)
	}
}

// rmPutRunInFlight records one run of rmRepoA in the registry, which is what
// `fugaro budget show` lists as running and what decides which confirmation
// `rm` asks.
func rmPutRunInFlight(t *testing.T, r *rmRig) {
	t.Helper()
	r.db.Set("agents", map[string]any{
		r.slugA: map[string]any{rmRunID: budget.AgentEntry{Repo: rmRepoA, Workflow: "web", Stage: "implement"}},
	})
}

// TestReposRmConvergesAHalfDoneRemoval is the other half of D2405's
// idempotence: a store that holds nothing says so rather than failing, so a
// removal an earlier failure left half done is finished by running it again.
//
// This fixture is the state that failure leaves: this machine's `repos:` entry
// is gone, its image map names no entry for the repository, the admission is
// gone, the jobs are gone and the state is gone — and the allow-list entry is
// still there, because that is the store the removal reaches last and the one
// it therefore has to be able to finish.
func TestReposRmConvergesAHalfDoneRemoval(t *testing.T) {
	half := strings.Replace(rmRigLocal,
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web], claude_accounts: [acme-personal] }\n", "", 1)
	half = strings.Replace(half, "  owner/repo-a: base\n", "", 1)
	r := rmRigFor(t, half, rmGone{admission: true, jobs: true, state: true})

	out, errOut := r.rmOut(t, rmEntryA)
	if strings.Contains(errOut, "warning:") {
		t.Fatalf("the removal warned on stderr:\n%s", errOut)
	}
	for _, want := range []string{
		"left alone: this machine's project config holds no repos: entry for " + rmRepoA,
		"left alone: this machine's repo-to-image map holds no entry for " + rmRepoA,
		projectpolicy.PathRepo(r.slugA) + ": already absent",
		"already absent: " + rmRepoA + " has no Cloud Run job in " + initProject,
		"already absent: gs://" + initStateBucket + "/" + infra.RepoStatePrefix(r.slugA) + "/ holds nothing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the half-done removal does not say %q:\n%s", want, out)
		}
	}
	// What was left to do, it did.
	if allow := allowReposOf(t, r.db); len(allow) != 1 || allow[0] != rmEntryB {
		t.Errorf("allow_repos = %v, want [%s]: the half-done removal did not finish the list", allow, rmEntryB)
	}
	lc := r.rmLocal(t)
	if len(lc.Repos) != 1 || len(lc.ImageMap) != 1 {
		t.Errorf("the local config gained an entry back: %d repos, %d image_map entries", len(lc.Repos), len(lc.ImageMap))
	}
	if got := lc.Repos[rmRepoB].Provider; got != "github" {
		t.Errorf("the surviving repos.%s entry changed: %+v", rmRepoB, lc.Repos[rmRepoB])
	}
}

// TestReposRmDeletesNoSecret is the third wall of "Fugaro never deletes a
// secret" (design §6, D2406) in the one command that removes most of a
// repository at once: no path of it reaches a secret-name-bearing delete.
//
// The fixture holds the repository's own labelled container, which is what a
// whole-repository read turns up — Orphans answers a labelled container with a
// note and never a retirement, and JobDeleteLabels answers for the two kinds of
// Cloud Run job and nothing else, so the delete is unreachable by construction
// rather than by a branch that could drift. Both the successful path and a
// refused one are checked: a refusal that had already deleted something would
// be worse than the removal it refused.
func TestReposRmDeletesNoSecret(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	out, _ := r.rmOut(t, rmEntryA)
	if strings.Contains(out, "deleted the secret container") {
		t.Errorf("the removal reported deleting a secret container:\n%s", out)
	}
	if !strings.Contains(out, "the secrets and their versions") {
		t.Errorf("the removal does not name the secrets it leaves:\n%s", out)
	}
	rmNoSecretTouched(t, r)

	// The refused path: the confirmation is answered with something that is
	// not the project's name, and the container is still untouched.
	refused := rmRigFor(t, "", rmGone{})
	ownerAtTheTerminal(t)
	if _, _, err := executeOwnerStdin(t, "yes\n", "project", "repos", "rm", rmEntryA); err == nil {
		t.Fatal("the refusal did not fire")
	}
	rmNoSecretTouched(t, refused)
}

// rmNoSecretTouched asserts that nothing about the fixture's secret container
// moved: no delete, no destroy, no disable, and the version it was seeded with
// is still the latest one.
func rmNoSecretTouched(t *testing.T, r *rmRig) {
	t.Helper()
	id := "fugaro-" + r.slugA + "-" + rmSecret
	if d := r.sm.Deleted(); len(d) != 0 {
		t.Errorf("secrets deleted: %v", d)
	}
	if d := r.sm.Destroyed(); len(d) != 0 {
		t.Errorf("secret versions destroyed: %v", d)
	}
	if d := r.sm.Disabled(); len(d) != 0 {
		t.Errorf("secret versions disabled: %v", d)
	}
	if got := string(r.sm.Latest(id)); got != strings.Repeat("s", 32) {
		t.Errorf("the container %s holds %q, want its seeded version", id, got)
	}
}

// rmHasObject reports whether the bucket holds the object, through the fake's
// own storage API: the state half of every assertion above is made against what
// a read would find, never against a call the command made.
func rmHasObject(t *testing.T, g *gcpfake.GCS, bucket, name string) bool {
	t.Helper()
	_, err := g.Bucket(t, bucket).Bucket.ReadAll(context.Background(), name)
	return err == nil
}

// rmLocalEntry is one `repos:` entry of this machine's config, for the test
// that keeps it aside before a removal takes it away.
func rmLocalEntry(t *testing.T, key string) localcfg.Repo {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lc.Repos[key]
	if !ok {
		t.Fatalf("the fixture's config holds no repos.%s: %v", key, lc.Repos)
	}
	return entry
}

// rmLocalEntryBack writes a `repos:` entry back into this machine's project
// config through the command's own writer (userSave), so a test that needs the
// entry after a removal puts it there through the one door rather than by hand.
func rmLocalEntryBack(t *testing.T, key string, entry localcfg.Repo) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	next := *lc
	next.Repos = maps.Clone(lc.Repos)
	if next.Repos == nil {
		next.Repos = map[string]localcfg.Repo{}
	}
	next.Repos[key] = entry
	if err := userSave(io.Discard, path, old, &next); err != nil {
		t.Fatal(err)
	}
}
