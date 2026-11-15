package cli

import (
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// updateDefaultProfileTests is where S5's own tests live: `fugaro update` writes
// the default job profile of every provider a project uses into the local
// config before the installation stage plans (docs/shared-jobs-default,
// reconciliation R1 and R8, decision D2310), so that the same update deploys
// the shared job its policy stage publishes.
//
// The rig is update_converge_test.go's, which is the state the task is about: an
// installation a release behind, in a checkout of its own repository, with one
// GitHub repository (acme/app) and one Bitbucket repository (acme/sandbox)
// enrolled and no profiles block at all.

// updatedConfig is the local config `fugaro update` left on the machine, read
// the way the door reads it — the strict loader — so a write that left a file
// this release would refuse cannot pass for a converge.
func updatedConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	return parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
}

// installationProjectJobs is the job set the installation stage wrote into its
// variables, keyed by the job's own name: one job per (profile, provider,
// Claude account) of every profile the local config names, which is what a run
// resolves and what the launcher places a run on.
func installationProjectJobs(t *testing.T, r *convergeRig) map[string]any {
	t.Helper()
	vars := r.tfvars(t)
	jobs, ok := vars["project_jobs"].(map[string]any)
	if !ok {
		t.Fatalf("the installation's variables carry no project_jobs: %v", vars)
	}
	return jobs
}

// TestUpdateDeploysTheDefaultProfilesJobInTheSameUpdate is S5's red step: no
// step wrote a default profile, so an installation that publishes no profile
// kept every repository on its per-repository job. The write is to the local
// config before the installation stage plans, which is what makes the one
// `fugaro update` deploy the shared job its policy stage publishes: the job, the
// node and the config all name the same profile.
func TestUpdateDeploysTheDefaultProfilesJobInTheSameUpdate(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	want := gcp.ProjectJobName(projectpolicy.DefaultProfileName, "github", "")
	jobs := installationProjectJobs(t, r)
	if _, ok := jobs[want]; !ok {
		t.Fatalf("the same update deployed no %s job, so a GitHub run naming no profile has no shared job to land on: %v\n%s",
			want, slices.Sorted(maps.Keys(jobs)), out)
	}
	if lc := updatedConfig(t); lc.Profiles[projectpolicy.DefaultProfileName].Provider != "github" {
		t.Errorf("the local config carries %+v under %s, want the profile the job above was deployed from",
			lc.Profiles[projectpolicy.DefaultProfileName], projectpolicy.DefaultProfileName)
	}
	// The node's profile is the config's, field for field: a run reads the
	// profile's own shape out of the policy (cpu, memory, the timeout class and
	// the admissions), so a node that names the profile and states something
	// else is a node whose job the run would not be launched onto — the name
	// alone says the profile exists, not what it is.
	node, ok := convergeNode(t, r).Profiles[projectpolicy.DefaultProfileName]
	if !ok {
		t.Errorf("the policy this update published defines no %s profile, so the job it deployed and the node a run reads disagree:\n%s",
			projectpolicy.DefaultProfileName, out)
	} else if written := updatedConfig(t).Profiles[projectpolicy.DefaultProfileName]; node.Provider != written.Provider ||
		node.CPU != written.CPU || node.Memory != written.Memory || node.TimeoutClass != written.TimeoutClass ||
		!slices.Equal(node.AllowRepos, written.AllowRepos) || !slices.Equal(node.Secrets, written.Secrets) ||
		!slices.Equal(node.ClaudeAccounts, written.ClaudeAccounts) {
		t.Errorf("the node's profiles.%s is\n%+v\nwant the profile this same run wrote to the local config, field for field:\n%+v\n%s",
			projectpolicy.DefaultProfileName, node.JobProfile, written, out)
	}
}

// TestUpdateCreatesTheDefaultProfilePerProvider: a project that mixes providers
// gets one default profile per provider, named for it — `standard` for the first
// and `standard-<provider>` for another (D2310, R8) — with the resources every
// profile must state, no image field (R1. config.JobProfile has none, the job's
// image is the installation's) and the canonical list of the repositories that
// provider's runs are held to, and the run says what it created.
func TestUpdateCreatesTheDefaultProfilePerProvider(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	out := convergeOn(t, r, []string{"policy-vs-jobs"})
	lc := updatedConfig(t)

	standard, ok := lc.Profiles[projectpolicy.DefaultProfileName]
	if !ok {
		t.Fatalf("no %s profile was written:\n%s", projectpolicy.DefaultProfileName, readFileString(t, installationConfigPath()))
	}
	if standard.Provider != "github" {
		t.Errorf("profiles.%s.provider = %q, want github, the project's first provider (config.Providers order)",
			projectpolicy.DefaultProfileName, standard.Provider)
	}
	if !slices.Equal(standard.AllowRepos, []string{"github/acme/app"}) {
		t.Errorf("profiles.%s.allow_repos = %v, want the project's GitHub repositories as canonical names",
			projectpolicy.DefaultProfileName, standard.AllowRepos)
	}
	if standard.CPU != config.DefaultCPU || standard.Memory != config.DefaultMemory || standard.TimeoutClass == "" {
		t.Errorf("profiles.%s = %+v, want the default cpu and memory and a timeout class: a profile that leaves one out is one the publisher refuses",
			projectpolicy.DefaultProfileName, standard)
	}

	bitbucket, ok := lc.Profiles[projectpolicy.DefaultProfileName+"-bitbucket"]
	if !ok {
		t.Fatalf("no %s-bitbucket profile was written for the project's Bitbucket repository: %v",
			projectpolicy.DefaultProfileName, slices.Sorted(maps.Keys(lc.Profiles)))
	}
	if bitbucket.Provider != "bitbucket" || !slices.Equal(bitbucket.AllowRepos, []string{"bitbucket/acme/sandbox"}) {
		t.Errorf("profiles.%s-bitbucket = %+v, want the Bitbucket repository the project enrols",
			projectpolicy.DefaultProfileName, bitbucket)
	}

	for _, name := range []string{"standard", "standard-bitbucket"} {
		if !strings.Contains(out, "wrote the default profile "+name+" ") {
			t.Errorf("the update does not print the profile %q it wrote, and a write into the operator's own file says what it wrote:\n%s", name, out)
		}
	}
}

// TestUpdateNeverRewritesAnEditedProfile: an existing profile is the operator's,
// whatever it holds (R1). The fixture's standard profile is a smaller job than
// the default and admits one repository; the run leaves it exactly as it was and
// writes the provider that has no default yet beside it.
func TestUpdateNeverRewritesAnEditedProfile(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	r.appendConfig(t, "profiles:\n"+
		"  standard:\n"+
		"    provider: github\n"+
		"    cpu: 2\n"+
		"    memory: 4Gi\n"+
		"    timeout_class: 2h\n"+
		"    allow_repos:\n"+
		"      - github/acme/app\n")
	// The operator's profile goes in before the copy the door reads is taken:
	// the rig clears FUGARO_CONFIG, so the config `fugaro update` reads and
	// writes is the one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	standard := updatedConfig(t).Profiles[projectpolicy.DefaultProfileName]
	if standard.CPU != 2 || standard.Memory != "4Gi" || standard.TimeoutClass != "2h" {
		t.Errorf("profiles.%s = %+v, want the operator's own resources: the update rewrote a profile it did not write",
			projectpolicy.DefaultProfileName, standard)
	}
	if !slices.Equal(standard.AllowRepos, []string{"github/acme/app"}) {
		t.Errorf("profiles.%s.allow_repos = %v, want the operator's own list", projectpolicy.DefaultProfileName, standard.AllowRepos)
	}
	if _, ok := updatedConfig(t).Profiles[projectpolicy.DefaultProfileName+"-bitbucket"]; !ok {
		t.Errorf("the provider with no default was left without one, which is the write this run owed:\n%s", out)
	}
}

// TestUpdateDefaultProfileIsIdempotent: the second update finds every name the
// first wrote and writes nothing at all — not even the same bytes again, which
// is what the file's own text says.
func TestUpdateDefaultProfileIsIdempotent(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	first := convergeOn(t, r, []string{"policy-vs-jobs"})
	before := updatedConfig(t).Profiles

	// The checkout the first run wrote its pin removal into is committed first,
	// the way the person's own step after an update leaves it: the repository
	// stage reads the working tree and refuses a `fugaro.yaml` that is not the
	// default branch's, and a second run that stopped there would not reach the
	// stages this test is about — the whole door has to exit clean, write
	// nothing and still converge.
	testutil.Git(t, r.root, "commit", "-qam", "the first run's own change to the checkout")
	fetched(t, r.root)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the second update did not converge: %v\n%s\n%s", err, out, errOut)
	}
	if !strings.Contains(out+errOut, "✔ repository") {
		t.Errorf("the second update's account says nothing about the repository stage having run, so this test may not have run the door whole:\n%s%s", out, errOut)
	}
	if after := updatedConfig(t).Profiles; !reflect.DeepEqual(before, after) {
		t.Errorf("the second update changed the profiles the file carries:\n--- before\n%+v\n--- after\n%+v", before, after)
	}
	if strings.Contains(out, "wrote the default profile") {
		t.Errorf("the second update says it wrote a profile it had already written:\n%s", out)
	}
	if !strings.Contains(first, "wrote the default profile") {
		t.Errorf("the first update does not say what it wrote:\n%s", first)
	}
}

// TestUpdatePreviousVersionConverges: an installation of the previous version —
// the node the release behind publishes is one this reader refuses, so the
// policy stage has to carry it forward — converges in ONE `fugaro update`, and
// the write of the defaults survives that migration: the migrated node carries
// the profiles this same run wrote and deployed.
func TestUpdatePreviousVersionConverges(t *testing.T) {
	r := newConvergeRig(t, staleNode(t))
	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if got := convergeNode(t, r).Version; got != projectpolicy.Version {
		t.Fatalf("the published node is version %d after one update, want this binary's %d", got, projectpolicy.Version)
	}
	lc := updatedConfig(t)
	for _, name := range []string{projectpolicy.DefaultProfileName, projectpolicy.DefaultProfileName + "-bitbucket"} {
		if _, ok := lc.Profiles[name]; !ok {
			t.Errorf("the migrated installation carries no profiles.%s: %v", name, slices.Sorted(maps.Keys(lc.Profiles)))
		}
		if _, ok := convergeNode(t, r).Profiles[name]; !ok {
			t.Errorf("the migration left profiles.%s out of the node the same update published:\n%s", name, out)
		}
	}
}

// TestUpdateKeepsTheDefaultProfilesJobWhenTheConfigCannotComposeIt: the job the
// write deployed is the installation's, and the update goes on converging when
// the config loses the fact that job is composed from.
//
// The default profile of a GitHub project is composed into a job from the one
// App ID its GitHub repositories record (infra.projectJobGitHubAppID), and that
// is a fact the local config can lose afterwards: the repository moves to
// Bitbucket and the config records no ID at all, or a repository on a second App
// is added and records a second one. Either way the spec composes no job for the
// profile while the state holds the job the update deployed, so the plan of
// every later run is that job's destruction — RefuseProjectJobDeletes stops the
// whole update on it, with "restore the profile as it was" as its only remedy, a
// remedy for a profile the update wrote and the operator never touched, so the
// installation stops converging for good (CLAUDE.md's HARD RULE).
//
// What converges it is the carry: the installed job set the workdir holds is put
// back into the document the plan is made from, whole (infra.CarriedProjectJobs,
// init.go's prepare), so the plan proposes no change to it. The tfvars every
// stage of the run rewrites from the spec are what terraform is handed, so what
// is asserted is what the plan is made from.
//
// Mutation (run, restore): drop the carry from prepare and the second update is
// refused at the door, with projectJobGitHubAppID's sentence about an App ID the
// project no longer has.
func TestUpdateKeepsTheDefaultProfilesJobWhenTheConfigCannotComposeIt(t *testing.T) {
	r := credentialsRig(t)
	credentialsGitHubRepo(t, r)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	if _, errOut, err := executeStdin(t, "", "update"); err != nil {
		t.Fatalf("the run that wrote the default profile and deployed its job failed: %v\n%s", err, errOut)
	}
	name := gcp.ProjectJobName(projectpolicy.DefaultProfileName, "github", "")
	deployed, _ := r.tfvars(t)["project_jobs"].(map[string]any)[name].(map[string]any)
	if deployed == nil {
		t.Fatalf("the update that wrote profiles.%s deployed no %s job: %v",
			projectpolicy.DefaultProfileName, name, r.tfvars(t)["project_jobs"])
	}
	// The repository moves to Bitbucket and the config records no App ID at
	// all, which is one of the two ordinary acts that leave the profile with
	// nothing to compose its job from.
	rewriteRigConfig(t, r, func(lc *localcfg.Config) {
		repo := lc.Repos[credentialsRigRepo]
		repo.Provider, repo.GitHubAppID = "bitbucket", ""
		lc.Repos[credentialsRigRepo] = repo
	})
	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("the update was refused, so this installation needs a second command: %v\n%s", err, s)
	}
	kept, _ := r.tfvars(t)["project_jobs"].(map[string]any)[name].(map[string]any)
	if !reflect.DeepEqual(kept, deployed) {
		t.Errorf("the job the installation holds is\n%v\nwant it unchanged from\n%v\nthe job the record holds is the job the state holds, and a document that rewrites it is a plan that replaces it\n%s",
			kept, deployed, s)
	}
	if pr, ok := localConfigOf(t, r).Profiles[projectpolicy.DefaultProfileName]; !ok || pr.Provider != "github" {
		t.Errorf("the update dropped profiles.%s from the local config: %+v. The profile is the operator's file's, and the job it deployed is kept, so the file keeps both",
			projectpolicy.DefaultProfileName, pr)
	}
}

// TestUpdateKeepsTheDefaultProfilesJobThroughTheRepositoryStage: the whole of
// `fugaro update` goes on converging when the config loses the fact the default
// profile's job is composed from — the repository stage included.
//
// The carry is read by every stage that composes the installation's spec, and
// the repository stage is the one an operator meets first: it plans the
// checkout's own root, so a run that refused there would stop before the
// policies, the credentials service and everything else, on every later update,
// for a profile the update wrote itself.
//
// The act here is a repository added on a second App, which is one of the two
// ordinary ones that leave the github default profile with no single App ID to
// compose its job from. It is the one this rig can stage whole: the other — the
// project's last GitHub repository moved to Bitbucket — moves the checkout's own
// provider with it, and this rig's checkout is the repository that act moves, so
// the run would stop at the repository stage's own onboarding question before
// reaching the engine this test is about (the act itself is pinned for the
// installation stage by TestUpdateKeepsTheDefaultProfilesJobWhenTheConfigCannot
// ComposeIt, whose rig has no checkout).
//
// The checkout is committed and its origin/main ref moved first, which is the
// person's own step after an update leaves its pin removal as a diff: the
// repository stage reads the working tree and refuses a fugaro.yaml that is not
// the default branch's, and that would hide the stage this test is about.
//
// The job the installation holds is compared with its base image left out: the
// images stage re-pins it on every update, and that is the one thing about the
// job that is meant to move.
//
// Mutation (run, restore): read the repository stage's spec with installOptions
// rather than installSpec and the second update stops there with
// recordedGitHubAppID's sentence about a project that uses several Apps.
func TestUpdateKeepsTheDefaultProfilesJobThroughTheRepositoryStage(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	convergeOn(t, r, []string{"policy-vs-jobs"})
	name := gcp.ProjectJobName(projectpolicy.DefaultProfileName, "github", "")
	deployed, _ := installationProjectJobs(t, r)[name].(map[string]any)
	if deployed == nil {
		t.Fatalf("the update that wrote profiles.%s deployed no %s job: %v",
			projectpolicy.DefaultProfileName, name, installationProjectJobs(t, r))
	}

	testutil.Git(t, r.root, "commit", "-qam", "the run's own change to the checkout")
	fetched(t, r.root)
	secondAppRepo(t)

	out, errOut, err := executeStdin(t, "", "update")
	s := out + errOut
	if err != nil {
		t.Fatalf("the repository stage refused an update of an installation that converges today, so it needs a second command: %v\n%s", err, s)
	}
	if !strings.Contains(s, "\u2714 repository") {
		t.Errorf("the run's account says nothing about the repository stage having run, so this test may not have run the stage it is about:\n%s", s)
	}
	if !strings.Contains(s, "✔ installation") {
		t.Errorf("the run's account says nothing about the installation stage having planned, so this test may not have run the converge it is about:\n%s", s)
	}
	kept, _ := installationProjectJobs(t, r)[name].(map[string]any)
	delete(deployed, "image")
	delete(kept, "image")
	if !reflect.DeepEqual(kept, deployed) {
		t.Errorf("the job the installation holds is\n%v\nwant it unchanged from\n%v\nthe job the record holds is the job the state holds, and a document that rewrites it is a plan that replaces it\n%s",
			kept, deployed, s)
	}
}

// secondAppRepo adds a repository on a second GitHub App to the installation's
// own config — the file the door reads, which this rig registered from the
// checkout's config (registerInstallation), and not the rig's source copy, which
// only the registration reads. It is one of the two ordinary acts that leave the
// github default profile with no single App ID to compose its job from.
func secondAppRepo(t *testing.T) {
	t.Helper()
	lc, err := localcfg.Load(installationConfigPath())
	if err != nil {
		t.Fatalf("the installation's config does not parse: %v", err)
	}
	lc.Repos["acme/other"] = localcfg.Repo{Provider: "github", BaseBranch: "main", Workflows: []string{"app"}, GitHubAppID: "987654321"}
	if data, err := lc.Marshal(); err != nil {
		t.Fatal(err)
	} else if err := os.WriteFile(installationConfigPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUpdatePlanOnlyConvergesWhereTheUpdateDoes: the preview of the converge
// must not refuse an installation that `fugaro update` itself converges.
//
// What the carry keeps out of the plan is the job the installation already
// holds, which is the same thing the real run proposes no change to, and it is
// not a write of this run's: nothing about it is left for a real run to do. A
// --plan-only run that answered installOptions' sentence about an App ID the
// project no longer records would tell the operator reading the plan that an
// update they can see converging from the plan is one their installation
// refuses — and the refusal names a remedy the operator cannot take without
// giving up the repository they added.
//
// Mutation (run, restore): answer nil from heldDefaultProfiles for --plan-only
// (the gate this closes) and this is the only test that fails — the preview
// stops before it plans anything.
func TestUpdatePlanOnlyConvergesWhereTheUpdateDoes(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	convergeOn(t, r, []string{"policy-vs-jobs"})
	testutil.Git(t, r.root, "commit", "-qam", "the run's own change to the checkout")
	fetched(t, r.root)
	secondAppRepo(t)

	out, errOut, err := executeStdin(t, "", "update", "--plan-only")
	s := out + errOut
	if err != nil {
		t.Fatalf("--plan-only refused an installation that `fugaro update` converges, so the preview of the update is not the update: %v\n%s", err, s)
	}
	if !strings.Contains(s, "No changes") {
		t.Errorf("the run's account says no stage planned, so this test may not have planned what it is about:\n%s", s)
	}
	if !strings.Contains(s, "kept the shared job of profiles.standard") {
		t.Errorf("the run planned without saying it keeps the job the installation holds, so this test may not have planned the update's own plan:\n%s", s)
	}
}
