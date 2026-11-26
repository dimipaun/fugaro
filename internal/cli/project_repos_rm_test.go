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
// directory or a real project. The state bucket is the installation's own fake
// and the repository's state objects are seeded into it, because the removal
// keeps them: the owner's ruling of 2026-11-17 (amending D2405) leaves the state
// where it is, for the offboarding's own steps to plan from, and
// TestReposRmKeepsTheStateAndReadsNothingInTheBucket is the test that holds it —
// versioning on or off, which is a property of the bucket this command no
// longer has an opinion about.
//
// The confirmation is the owner's own: nothing on the command line answers it
// (F5, D2403 — `--yes` is gone), so every run below stands at both streams and
// types the project's name.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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
	// rmRepoC is a third repository of the same project, one this machine's
	// config does not list: the shape of a project two operators share, and the
	// one the admission step must not touch.
	rmRepoC = "owner/repo-c"
	// rmRegion is the region the fixture's config names, which is where its
	// Cloud Run jobs live.
	rmRegion = "us-east5"
	// rmSchedulerRegion is where the fixture's Scheduler jobs live: Scheduler is
	// not offered in us-east5, so the spec's own rule (gcp.SchedulerRegion)
	// places them in the nearest location that has it, and `rm` derives the same
	// one from the same config.
	rmSchedulerRegion = "us-east4"
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
//
// The state is not one of them: a failure of this removal cannot empty it,
// because the removal does not delete it at all (the owner's ruling of
// 2026-11-17), so a fixture with no state would stand for nothing an operator
// can meet.
type rmGone struct {
	admission bool
	jobs      bool
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

// rmSchedulerEmail is the installation's Scheduler service account, which the
// module mints the check job's OAuth token from. It is not part of what the
// removal checks — the target URI is — but a fixture that left it out would
// seed a job shaped unlike any the module writes.
const rmSchedulerEmail = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"

// rmCheckURI is the target the repository root writes into the repository's
// Scheduler job: its check job's :run endpoint, in the repository's own region
// (modules/repo/check.tf), which is the identity `rm` checks before it deletes.
func rmCheckURI(slug string) string {
	return "https://run.googleapis.com/v2/projects/" + initProject + "/locations/" + rmRegion + "/jobs/" + gcp.CheckJobName(slug) + ":run"
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

// rmRigUnmanaged is the same installation with neither mark on its project: the
// ordinary project D2217's fence refuses the removal in
// (TestProjectReposRmRefusesUnmanaged). It stops short of the six stores for
// the reason the fence is where it is — a refused removal reaches none of them,
// and a fixture that seeded them would suggest it did.
func rmRigUnmanaged(t *testing.T) *rmRig {
	t.Helper()
	r := &rmRig{addRig: newAddRigMarks(t, rmRigLocal, false), slugA: rmSlug(t, rmRepoA), slugB: rmSlug(t, rmRepoB)}
	r.labelA, r.labelB = rmLabel(t, rmRepoA), rmLabel(t, rmRepoB)
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
	// The state bucket and the repository's state objects, which the removal
	// keeps: the bucket is what this machine's config records and what gates the
	// cloud step, and the objects under the repository's prefix are what the
	// offboarding's own apply and destroy plan from (the owner's ruling of
	// 2026-11-17).
	r.stateBucket()
	r.gcs.SetVersioning(initStateBucket, true)
	r.gcs.Put(initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tfstate", []byte(`{"version":4}`))
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
		r.sched.SetJob(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugA), rmCheckURI(r.slugA), rmSchedulerEmail)
	}
	specB := infra.RepoSpec{Name: rmRepoB, Slug: r.slugB, Label: r.labelB, GCPProject: initProject, Region: rmRegion}
	webB := infra.OrphanLabels(specB)
	webB[gcp.LabelWorkflow] = "web"
	r.run.SetJob(gcp.JobName(r.slugB, "web"), webB, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc")
	// The other repository's Scheduler job, which no path here may touch: it is
	// the second half of the same assertion the jobs above carry.
	r.sched.SetJob(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugB), rmCheckURI(r.slugB), rmSchedulerEmail)
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

// TestProjectReposRmRefusesUnmanaged: D2217's fence on this command (T7b). A
// removal takes a repository's allow-list entry, its admission node, its
// `repos:` and `image_map:` entries and its Cloud Run jobs out of the project,
// so a project that is not managed is refused before anything is written — at
// the budget database the command opens first, which is the fence, in the one
// sentence it writes.
func TestProjectReposRmRefusesUnmanaged(t *testing.T) {
	r := rmRigUnmanaged(t)
	before := readFileString(t, r.cfg)
	node := nodeOf(t, r.db)

	out, _, err := execute(t, "project", "repos", "rm", rmEntryA)
	if err == nil {
		t.Fatalf("the repository of a project that is not managed was un-enrolled:\n%s", out)
	}
	checkFenceRefusal(t, err, "project repos rm")
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("a refused removal changed the file:\n%s", after)
	}
	if after := nodeOf(t, r.db); after != node {
		t.Errorf("a refused removal wrote the project's published node:\nbefore %s\nafter  %s", node, after)
	}
	noWriteAcrossFakes(t, r.initRig)
}

// TestReposRmRemovesTheSixAndNamesTheSixItLeaves is the red step of RE4
// (D2405, D2406): the whole removal, store by store, and the whole list of what
// it leaves, said twice — before the confirmation and after the write.
//
// Every removal is asserted on the fixture and not on the output: the published
// node's allow_repos, this machine's `repos:` entry and its `image_map:` entry,
// the published admission of repo-a and the repository's two Cloud Run jobs.
// Every one of them has a second half that must survive — repo-b's entry,
// admission and job — because a removal that took the whole document would pass
// a one-sided test, and the state, which the removal keeps outright, is
// asserted as a kept store in the same sweep.
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
	// 6. the state, which the removal does not touch at all: it is the one store
	// of the six that D2405's list no longer names, because the owner's ruling of
	// 2026-11-17 keeps it — the offboarding's own apply and destroy plan from it
	// — and repo-b's state and the lock beside the repository's are the second
	// half, as they are for every other assertion here.
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tfstate") {
		t.Errorf("the state object of %s was deleted: this removal keeps it (the owner's ruling of 2026-11-17)", rmRepoA)
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tflock") {
		t.Errorf("the lock object of %s was deleted with the state", rmRepoA)
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugB)+"/default.tfstate") {
		t.Errorf("the state of %s was deleted too", rmRepoB)
	}
	// 7. the Scheduler job, which no retire path can reach (Orphans reads labels
	// and the module gives this resource none) and which the removal therefore
	// deletes itself, through the Scheduler API and after checking the job's own
	// target. The other repository's is the second half.
	chkA := infra.SchedulerJobName(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugA))
	if got := r.sched.Deleted(); len(got) != 1 || got[0] != chkA {
		t.Errorf("Scheduler deletes = %v, want [%s]", got, chkA)
	}
	if r.sched.HasJob(chkA) {
		t.Errorf("the Scheduler job of %s survived", rmRepoA)
	}
	if !r.sched.HasJob(infra.SchedulerJobName(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugB))) {
		t.Errorf("the removal deleted %s's Scheduler job", rmRepoB)
	}
	if !strings.Contains(out, "deleted the Scheduler job "+gcp.SchedulerJobName(r.slugA)) {
		t.Errorf("the output does not say the Scheduler job went:\n%s", out)
	}
	// 8. the order, which is the property that makes a failed removal
	// finishable rather than stranding the repository: the allow-list entry is
	// the store this command's own read gates on, so it is written last and
	// every store a failure could leave behind is written before it. A removal
	// that wrote it first would answer "nothing to remove" on the next run over
	// a repository whose jobs and state are already gone — the very state
	// TestReposRmConvergesAHalfDoneRemoval is the fixture of.
	if i, last := strings.Index(out, "allow_repos: "), strings.LastIndex(out, "\ndeleted "); i < 0 || last < 0 || i < last {
		t.Errorf("the allow-list write is not last (allow_repos at %d, the last delete at %d):\n%s", i, last, out)
	}

	// The two things this door does not reach at all, named rather than left to
	// be discovered, before the question and again after the write: the state,
	// which the offboarding's own apply plans from, and the grants, which are the
	// repository root's own IAM members and which only such an apply deletes
	// (the owner's ruling of 2026-11-17, amending D2405).
	want := "it keeps the repository's Terraform state under gs://" + initStateBucket + "/" + infra.RepoStatePrefix(r.slugA) + "/ and the repository root's own IAM members — the grants of " + rmRepoA + "'s job accounts and of its build account"
	if !strings.Contains(warning, want) {
		t.Errorf("the warning does not name what the repository root's own apply removes:\n%s", warning)
	}
	if !strings.Contains(after, want) {
		t.Errorf("the output after the write does not name it either:\n%s", after)
	}
}

// TestReposRmLeavesASchedulerJobThatTargetsSomethingElse is the check in front
// of the Scheduler delete, which the whole-removal test cannot reach: every
// fixture there seeds the job the module writes, so the branch that leaves a
// job alone never runs.
//
// The check is the whole of what keeps this delete honest. `rm` finds the job
// by name alone — Fugaro's own derivation from the slug (gcp.SchedulerJobName),
// which is a name an operator can also write by hand or a failed convergence
// can leave behind — so the name is no evidence the job is this repository's.
// The identity test is the job's own target: the URI of this repository's check
// job, which is the same test discovery makes before it adopts a Scheduler job
// (infra.discovery.schedulerJob, through checkRunURI). A job that posts
// anywhere else is left alone with a line rather than deleted, and the rest of
// the removal still happens — this is a check, not a refusal.
func TestReposRmLeavesASchedulerJobThatTargetsSomethingElse(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	// The same name the module derives, targeting another repository's check
	// job: what a job left under this repository's name by anything but this
	// repository's root looks like.
	chkA := infra.SchedulerJobName(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugA))
	r.sched.SetJob(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugA), rmCheckURI(rmSlug(t, rmRepoC)), rmSchedulerEmail)

	out, _ := r.rmOut(t, rmEntryA)
	if got := r.sched.Deleted(); len(got) != 0 {
		t.Errorf("Scheduler deletes = %v, want none: the job does not target this repository's check job", got)
	}
	if !r.sched.HasJob(chkA) {
		t.Errorf("the removal deleted a Scheduler job it had just said was not %s's", rmRepoA)
	}
	line := rmLineWith(out, "left alone: the Scheduler job "+gcp.SchedulerJobName(r.slugA))
	if line == "" {
		t.Fatalf("the removal does not say the Scheduler job was left alone:\n%s", out)
	}
	if !strings.Contains(line, "does not target the check job of "+rmRepoA) {
		t.Errorf("the sentence does not say why it was left alone:\n%s", line)
	}
	// The rest of the removal is not skipped: the allow-list entry and the Cloud
	// Run jobs went, so a mutation that dropped the check would fail on the
	// assertions above rather than on a removal that never happened.
	if allow := allowReposOf(t, r.db); len(allow) != 1 || allow[0] != rmEntryB {
		t.Errorf("allow_repos = %v, want [%s]", allow, rmEntryB)
	}
	if r.run.HasJob(gcp.JobName(r.slugA, "web")) || r.run.HasJob(gcp.CheckJobName(r.slugA)) {
		t.Errorf("a Cloud Run job of %s survived", rmRepoA)
	}
}

// TestReposRmNamesTheDocumentedOffboardingForWhatItKeeps is the second half of
// that sentence's claim, and it is the half a reader at the confirmation can
// act on.
//
// Saying "the state and the grants stay" is not enough if the sentence also says
// how to take them away and that door does not exist: the previous wording named
// `fugaro update --repo . --retire`, and `--retire` retires the addresses the
// configuration no longer produces (Retirements is a set difference against the
// spec), so over a repository whose `fugaro.yaml` still declares its workflows
// it answers "Nothing to retire" and the grants stay exactly where they were.
// Deleting the state was the other half of the same mistake: it is what the
// sequence the sentence now names plans from (the owner's ruling of
// 2026-11-17). The sentence therefore names the sequence that does reach both —
// the hand-run offboarding the repository documents — and this test holds it to
// the two things that make the naming honest: the document is there, and it
// carries the step the sentence attributes to it.
func TestReposRmNamesTheDocumentedOffboardingForWhatItKeeps(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	out, _ := r.rmOut(t, rmEntryA)
	sentence := rmLineWith(out, "it keeps the repository's Terraform state under gs://")
	if sentence == "" {
		t.Fatalf("the removal does not name what it leaves of the repository root and the state:\n%s", out)
	}
	if !strings.Contains(sentence, "docs/how-to/offboarding.md") {
		t.Errorf("the sentence names no document for the flow that takes the state and the grants away:\n%s", sentence)
	}
	// The commands the sentence names are the offboarding's own, and they are
	// commands the tree has: a `fugaro …` a person cannot type is a dead end at
	// the one moment they are cleaning up.
	cmds := rmFugaroCommands(sentence)
	if len(cmds) == 0 {
		t.Fatalf("the sentence names no command:\n%s", sentence)
	}
	root := NewRootCmd()
	for _, path := range cmds {
		if err := rmResolveCommand(root, path); err != nil {
			t.Errorf("%v\n%s", err, sentence)
		}
	}
	// The sequence is not one-way, and both halves have to say so rather than
	// leave the operator to find the repository's jobs back: step 1 is an apply
	// of a root that still declares the workflow jobs, the check job and its
	// schedule, so over a repository this command has just un-enrolled it
	// recreates them and writes the `repos:` entry back, and only step 3's
	// destroy takes them away again.
	for _, want := range []string{"recreates", "the workflow jobs", "repos: entry"} {
		if !strings.Contains(sentence, want) {
			t.Errorf("the sentence does not say what the offboarding's first step does over a repository this command has un-enrolled (%q):\n%s", want, sentence)
		}
	}
	// And the document is the door it says it is: the sequence it lays out ends
	// with that command, which is what makes the pointer worth following, and it
	// carries the same warning about its first step.
	doc, err := os.ReadFile("../../docs/how-to/offboarding.md")
	if err != nil {
		t.Fatalf("reading docs/how-to/offboarding.md, the flow the sentence names: %v", err)
	}
	if !strings.Contains(string(doc), "fugaro update --repo --forget") {
		t.Errorf("the sentence names docs/how-to/offboarding.md as the flow whose last step is `fugaro update --repo --forget`, and the document does not name that step:\n%s", sentence)
	}
	if !strings.Contains(string(doc), "recreates") {
		t.Errorf("the document does not say that step 1 recreates the jobs of a repository this command has un-enrolled:\n%s", sentence)
	}
}

// TestReposRmLeavesAnotherMachinesAdmissionAlone is the bound on the admission
// step: `config/policy/repos` holds one child per repository of the project, and
// this command removes one repository.
//
// The narrowing (tightenAdmissions) narrows or deletes every key it is handed,
// and the map it is handed is this machine's own derivation — the publish's
// build reads `desired` from the local config alone (policyNodeForPublish →
// repoAdmissions → DeriveAccounts), which lists the repositories *this* machine
// onboards. A child belonging to a repository this config does not list is
// therefore a key with no `desired` entry at all, and a whole-parent narrowing
// reads it as a repository no account admits any more and deletes it: a removal
// of repo-a would take away the cascade access of repo-c, a repository its
// confirmation never named. The removal cuts the parent down to its own child
// before the narrowing, and the third repository is what says so.
func TestReposRmLeavesAnotherMachinesAdmissionAlone(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	third := rmSlug(t, rmRepoC)
	r.db.Set(projectpolicy.PathRepo(third), map[string]any{
		"credentials": map[string]any{"accounts": []string{"acme-work"}},
	})

	out, _ := r.rmOut(t, rmEntryA)
	if v := r.db.Value(projectpolicy.PathRepo(third)); v == nil {
		t.Errorf("removing %s deleted %s, the admission of a repository this machine's config does not list:\n%s", rmRepoA, projectpolicy.PathRepo(third), out)
	}
	// The child this removal is about is the one that went, so the test is
	// two-sided in the same way the whole-removal test is.
	if v := r.db.Value(projectpolicy.PathRepo(r.slugA)); v != nil {
		t.Errorf("%s still holds %v", projectpolicy.PathRepo(r.slugA), v)
	}
}

// TestReposRmDoesNotStopOnAnotherRepositoriesUnreadableAdmission is the same
// bound seen from the other side. tightenAdmissions refuses a child it cannot
// read, because the set of accounts it admits is what the narrowing computes —
// a refusal that belongs to the repository that child is about. Handed the whole
// parent, a node of somebody else's repository that this Fugaro cannot read
// would end the removal of repo-a with that sentence, after this machine's
// config had already been written: a failure about a repository the operator did
// not name, on a run that has to be finished by hand. Cut down to its own child,
// the removal does not read it at all.
func TestReposRmDoesNotStopOnAnotherRepositoriesUnreadableAdmission(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	third := rmSlug(t, rmRepoC)
	r.db.Set(projectpolicy.PathRepo(third), map[string]any{"credentials": "not-a-node"})

	out, _ := r.rmOut(t, rmEntryA)
	if v := r.db.Value(projectpolicy.PathRepo(third)); v == nil {
		t.Errorf("the unreadable admission of %s was removed rather than left alone:\n%s", rmRepoC, out)
	}
	if v := r.db.Value(projectpolicy.PathRepo(r.slugA)); v != nil {
		t.Errorf("%s still holds %v", projectpolicy.PathRepo(r.slugA), v)
	}
	// The refusal the whole-parent narrowing would have ended the run with is
	// nowhere in the output: it is not this repository's node.
	if strings.Contains(out, "is a node this Fugaro cannot read") {
		t.Errorf("the removal refused over a node of another repository:\n%s", out)
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
	// D2408's other half: the output names --purge nowhere, so nobody looks for
	// it. The help is the one place an operator would read the name off, and a
	// sentence saying the flag does not exist is how it comes back.
	ownerAtTheTerminal(t)
	help, _, err := executeOwnerStdin(t, "", "project", "repos", "rm", "--help")
	if err != nil {
		t.Fatalf("--help: %v\n%s", err, help)
	}
	if strings.Contains(help, "purge") {
		t.Errorf("`project repos rm --help` names --purge:\n%s", help)
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
	// The sentence is not the whole of D2407: the job the run is executing is
	// still there, and the line says so. The check job is not held by anything —
	// no run executes it — so the removal still takes it, which is what makes
	// this an assertion about the run's job rather than about the whole cloud
	// step having been skipped.
	if !r2.run.HasJob(gcp.JobName(r2.slugA, "web")) {
		t.Errorf("the removal deleted the job the in-flight run %s is executing", rmRunID)
	}
	if r2.run.HasJob(gcp.CheckJobName(r2.slugA)) {
		t.Errorf("the removal kept the check job of %s, which no run holds", rmRepoA)
	}
	if !strings.Contains(out2, "it stays until they end") {
		t.Errorf("the removal does not say it kept the run's job:\n%s", out2)
	}
}

// TestReposRmKeepsTheStateAndReadsNothingInTheBucket is the store the owner's
// ruling of 2026-11-17 keeps, in the one direction that used to refuse: the
// removal once deleted the repository's state objects and read the bucket's
// object versioning first, refusing a bucket without it, because only versioning
// keeps a copy of what a rollback would need. It deletes nothing there now, so
// it has no versioning to check and no reason to refuse one.
//
// Both halves are the point. The removal has to complete over a bucket whose
// versioning is off — a machine that is cleaning a repository up is not owed a
// second problem by this command, and forgetRepo's own check (init.go) is the
// step that does delete the state, with the reason that check exists — and the
// repository's state objects have to be exactly where they were afterwards,
// because the offboarding's own apply and destroy plan from them. The other
// repository's state is the second half of the same assertion, as everywhere
// else in this file.
func TestReposRmKeepsTheStateAndReadsNothingInTheBucket(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	r.gcs.SetVersioning(initStateBucket, false)

	out, _ := r.rmOut(t, rmEntryA)
	if strings.Contains(out, "object versioning") {
		t.Errorf("the removal still has an opinion about the bucket's versioning:\n%s", out)
	}
	// The removal happened: this is not a test that the command refused, and a
	// refusal here would pass the state assertions below for the wrong reason.
	if allow := allowReposOf(t, r.db); len(allow) != 1 || allow[0] != rmEntryB {
		t.Errorf("allow_repos = %v, want [%s]", allow, rmEntryB)
	}
	if r.run.HasJob(gcp.JobName(r.slugA, "web")) || r.run.HasJob(gcp.CheckJobName(r.slugA)) {
		t.Errorf("a Cloud Run job of %s survived", rmRepoA)
	}
	for _, want := range []string{
		infra.RepoStatePrefix(r.slugA) + "/default.tfstate",
		infra.RepoStatePrefix(r.slugA) + "/default.tflock",
	} {
		if !rmHasObject(t, r.gcs, initStateBucket, want) {
			t.Errorf("gs://%s/%s is gone: this removal keeps the repository's state objects", initStateBucket, want)
		}
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugB)+"/default.tfstate") {
		t.Errorf("the state of %s was deleted", rmRepoB)
	}
	// And it says so, which is the half the operator reads: the kept sentence is
	// the same one the whole-removal test pins the wording of.
	if line := rmLineWith(out, "it keeps the repository's Terraform state under gs://"); line == "" {
		t.Errorf("the removal does not say the state was kept:\n%s", out)
	}
}

// TestReposRmRefusesAProviderTheEntryDoesNotHave is the one source in front of
// the entry that reposRemovalProvider does not let win quietly: an argument that
// names a provider different from this machine's `repos:` entry for the same
// owner/name.
//
// The config is keyed by owner/name alone (configRepoKey), so both providers'
// entries of one name live under one key and only one of them can be the
// machine's. Removing `bitbucket/owner/repo-a` over a machine whose entry says
// github would resolve to github/owner/repo-a — the Data loss the review found:
// the wrong repository's enrolment and jobs go, and the argument the operator
// typed is not what was removed. The refusal has to land before anything is
// read or written, so this test asserts the whole fixture afterwards: both
// allow-list entries, both local entries, the admission, both Cloud Run jobs,
// the Scheduler job and the state, and no confirmation asked.
func TestReposRmRefusesAProviderTheEntryDoesNotHave(t *testing.T) {
	// One repository in the fixture, two entries of it on the allow-list: the
	// github one this machine's config holds and the bitbucket one whose name is
	// what the operator typed. The other vendor's entry is what makes the bug a
	// removal of the wrong repository rather than a plain "not on the list".
	r := rmRigFor(t, "", rmGone{})
	r.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{rmEntryA, rmEntryB, "bitbucket/" + rmRepoA},
	})

	ownerAtTheTerminal(t)
	out, _, err := executeOwnerStdin(t, initProjectName+"\n", "project", "repos", "rm", "bitbucket/"+rmRepoA)
	if err == nil {
		t.Fatalf("the removal accepted a provider this machine's entry does not have:\n%s", out)
	}
	for _, want := range []string{
		"bitbucket/" + rmRepoA,
		"github/" + rmRepoA,
		"repos: entry",
		"fugaro project repos rm github/" + rmRepoA,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// Before anything is read: no warning, no confirm, nothing asked.
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the removal asked for confirmation before refusing:\n%s", out)
	}
	// And nothing was removed, in any of the stores: the wrong repository's
	// enrolment and jobs would be the failure this refusal exists to prevent, and
	// the argument's own repository is still enrolled too.
	if allow := allowReposOf(t, r.db); len(allow) != 3 {
		t.Errorf("allow_repos = %v after the refusal, want the three entries", allow)
	}
	if lc := r.rmLocal(t); len(lc.Repos) != 2 || len(lc.ImageMap) != 2 {
		t.Errorf("the local config changed after the refusal: %d repos, %d image_map entries", len(lc.Repos), len(lc.ImageMap))
	}
	if r.db.Value(projectpolicy.PathRepo(r.slugA)) == nil {
		t.Errorf("the admission of %s was removed after the refusal", rmRepoA)
	}
	if !r.run.HasJob(gcp.JobName(r.slugA, "web")) || !r.run.HasJob(gcp.CheckJobName(r.slugA)) {
		t.Errorf("a Cloud Run job was deleted after the refusal")
	}
	if !r.sched.HasJob(infra.SchedulerJobName(initProject, rmSchedulerRegion, gcp.SchedulerJobName(r.slugA))) {
		t.Errorf("the Scheduler job was deleted after the refusal")
	}
	if !rmHasObject(t, r.gcs, initStateBucket, infra.RepoStatePrefix(r.slugA)+"/default.tfstate") {
		t.Errorf("the state was deleted after the refusal")
	}
}

// TestReposRmKeepsAJobItCannotAttribute is the edge of D2407's promise. The
// registry's Workflow is optional (budget.AgentEntry.Workflow is omitempty) and
// a run whose workflow is not recorded cannot be attributed to a job, so a
// workflow job may be the one that run is executing — the registry's own sweep
// reads the same entry the same way and errs towards keeping (budget.Sweeper).
// The removal keeps it, and the check job, which no run executes, still goes.
func TestReposRmKeepsAJobItCannotAttribute(t *testing.T) {
	r := rmRigFor(t, "", rmGone{})
	r.db.Set("agents", map[string]any{
		r.slugA: map[string]any{rmRunID: budget.AgentEntry{Repo: rmRepoA, Stage: "implement"}},
	})
	out, _ := r.rmOut(t, rmEntryA)
	if !r.run.HasJob(gcp.JobName(r.slugA, "web")) {
		t.Errorf("the removal deleted a workflow job of %s while a run of it with no recorded workflow is in flight", rmRepoA)
	}
	if r.run.HasJob(gcp.CheckJobName(r.slugA)) {
		t.Errorf("the removal kept the check job of %s, which no run holds", rmRepoA)
	}
	if !strings.Contains(out, "records no workflow") {
		t.Errorf("the removal does not say why it kept the job:\n%s", out)
	}
	// The rest of the removal still happened: a kept job is not a skipped
	// command.
	if allow := allowReposOf(t, r.db); len(allow) != 1 || allow[0] != rmEntryB {
		t.Errorf("allow_repos = %v, want [%s]", allow, rmEntryB)
	}
}

// TestReposRmLeavesAPublishedImageMappingAndNamesACommandThatExists is the
// image-map half of D1906's keep rule: a machine whose project config names no
// map keeps the published node's (localcfg.ImageMapEffective), so there is
// nothing here for `rm` to take away and it says so rather than reporting a
// removal it did not make.
//
// The sentence has to name a door that exists. `fugaro image map` registers only
// `set` (image_map.go), so there is no verb that unsets an entry: the entry goes
// out of the file the map is published from, and `fugaro update` publishes it.
// The test resolves every `fugaro …` the sentence names against the command tree,
// which is what a sentence naming `fugaro image map rm` fails — a sentence that
// sends the operator to an unknown-command error at the moment they are cleaning
// up.
func TestReposRmLeavesAPublishedImageMappingAndNamesACommandThatExists(t *testing.T) {
	// The file names no map at all: a publish from a machine that names one is
	// what leaves the node's, and D1906's rule reads the node's map here.
	local := strings.Replace(rmRigLocal, "image_map:\n  owner/repo-a: base\n  owner/repo-b: base\n", "", 1)
	if local == rmRigLocal {
		t.Fatal("the fixture's image_map block was not found")
	}
	r := rmRigFor(t, local, rmGone{})
	// The node's own spelling of the block: an ImageMap marshals its keys under
	// escKey (a repository name carries a "/", which RTDB refuses as a key), so
	// the fixture hands the fake the value a publish would have written rather
	// than a map with the name in it.
	r.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{rmEntryA, rmEntryB},
		"image_map":   projectpolicy.ImageMap{rmRepoA: "base", rmRepoB: "base"},
	})

	out, _ := r.rmOut(t, rmEntryA)
	const why = "left alone: image_map[" + rmRepoA + "] is the published node's mapping"
	line := rmLineWith(out, why)
	if line == "" {
		t.Fatalf("the removal does not say the published mapping is left alone:\n%s", out)
	}
	cmds := rmFugaroCommands(line)
	if len(cmds) == 0 {
		t.Fatalf("the sentence names no command:\n%s", line)
	}
	root := NewRootCmd()
	for _, path := range cmds {
		if err := rmResolveCommand(root, path); err != nil {
			t.Errorf("%v\n%s", err, line)
		}
	}
	// The mapping is still there, in the node that owns it, and this machine's
	// file still names no map of its own: "left alone" is a claim about both.
	b, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the node does not decode (found=%v): %v", found, err)
	}
	if got := p.ImageMap[rmRepoA]; got != "base" {
		t.Errorf("image_map[%s] = %q in the node, want base: the removal changed a map that is not this file's", rmRepoA, got)
	}
	if lc := r.rmLocal(t); lc.ImageMap != nil {
		t.Errorf("this machine's config gained an image map: %v", lc.ImageMap)
	}
}

// rmLineWith is the one line of out that holds want, without its newline, and ""
// when there is none: the sentences this file asserts on are lines, and a
// sentence read out of a whole transcript would sweep in the paragraphs around
// it.
func rmLineWith(out, want string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// rmFugaroCommands are the `fugaro …` invocations a sentence names, each as the
// words to hand the command tree: the backticked spans that begin with `fugaro`,
// up to the first flag, since what is checked is the command path and not the
// flags of the invocation.
func rmFugaroCommands(sentence string) [][]string {
	var out [][]string
	for _, part := range strings.Split(sentence, "`") {
		fields := strings.Fields(part)
		if len(fields) < 2 || fields[0] != "fugaro" {
			continue
		}
		var path []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") {
				break
			}
			path = append(path, f)
		}
		if len(path) > 0 {
			out = append(out, path)
		}
	}
	return out
}

// rmResolveCommand walks path down the command tree from root, and reports the
// first word that is not a command the one before it has. The walk is by hand
// because cobra's own lookup is not this check: `Find` stops at the deepest
// command it recognizes and hands the rest back as arguments, and a parent it
// stops at is not runnable, so cobra prints that parent's help and exits 0 —
// `fugaro image map rm x` is a help screen, not an error, and Find returns none.
// A word is an argument only where the command before it has no subcommands at
// all, which is what makes this the shape of the sentence's own claim: a verb
// named here runs.
func rmResolveCommand(root *cobra.Command, path []string) error {
	at := root
	for i, want := range path {
		var next *cobra.Command
		for _, sub := range at.Commands() {
			if sub.Name() == want || slices.Contains(sub.Aliases, want) {
				next = sub
				break
			}
		}
		if next == nil {
			if len(at.Commands()) == 0 {
				return nil
			}
			return fmt.Errorf("the sentence names `fugaro %s`, and %s has no subcommand %q — a person typing that gets %s's help, not the command the sentence promises", strings.Join(path[:i+1], " "), at.CommandPath(), want, at.CommandPath())
		}
		at = next
	}
	return nil
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
// gone and the jobs are gone — and the allow-list entry is still there, because
// that is the store the removal reaches last and the one it therefore has to be
// able to finish. The state is not in the list: nothing this command does can
// empty it (the owner's ruling of 2026-11-17), so the fixture keeps it.
func TestReposRmConvergesAHalfDoneRemoval(t *testing.T) {
	half := strings.Replace(rmRigLocal,
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web], claude_accounts: [acme-personal] }\n", "", 1)
	half = strings.Replace(half, "  owner/repo-a: base\n", "", 1)
	r := rmRigFor(t, half, rmGone{admission: true, jobs: true})

	out, errOut := r.rmOut(t, rmEntryA)
	if strings.Contains(errOut, "warning:") {
		t.Fatalf("the removal warned on stderr:\n%s", errOut)
	}
	for _, want := range []string{
		"left alone: this machine's project config holds no repos: entry for " + rmRepoA,
		"left alone: this machine's repo-to-image map holds no entry for " + rmRepoA,
		projectpolicy.PathRepo(r.slugA) + ": already absent",
		"already absent: " + rmRepoA + " has no Cloud Run job in " + initProject,
		"already absent: " + rmRepoA + " has no Scheduler job in " + rmSchedulerRegion,
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
