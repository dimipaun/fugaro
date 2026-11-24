package cli

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// updateDefaultEntryTests is where S10's own tests live: `fugaro update` writes
// the project's default catalog entry — the pinned base image, as a prebuilt
// entry marked default (D2305) — so that the shared job of every profile has an
// image the project's own catalog names (internal/infra's projectJobImage
// resolves exactly that entry for them, F21) and so that the project has a
// default an operator can curate a baked image into.
//
// The entry is the image of every job whose repository names none, so it is
// written only in the state where writing it cannot make this same update
// refuse a job it converges today (internal/infra's unbuiltOnBaked, D1191), and
// there are three of those: a repository the project does not admit, a
// repository this run holds no checkout of — both of them keep the
// per-repository job their own root deploys, which the entry is the image of,
// and the second is unreadable from here — and, for the checkout this run does
// hold, a workflow of it that the entry would be handed to and cannot run.
//
// The rig is update_converge_test.go's: an installation a release behind, in a
// checkout of its own repository, whose config lists the checkout's own
// repository alone (onlyRepos; newInitRig's own config also enrols a Bitbucket
// repository, and this write waits for a project that lists no other). The
// published node is currentNode's, which admits github/acme/app.

// bakedWorkflowYAML is the checkout of the tests below that need the entry's
// own write to be the thing under test rather than the refusal it waits on:
// both workflows write a dockerfile: of their own, so the catalog's default is
// set aside for them (D1900) and neither is a job the entry would be handed to.
// It is the shape of TestUpdateConvergesAPrebuiltDefaultBesideAWorkflowsOwnImage,
// for the same reason.
const bakedWorkflowYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { dockerfile: .fugaro/app.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { dockerfile: .fugaro/api.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n"

// ownImageDockerfile is the Dockerfile those workflows name, as the repository
// behind had it: `fugaro validate` reads a workflow's dockerfile: as a file
// that is there, so a checkout without them is not the shape these tests are
// about.
const ownImageDockerfile = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN git clone \"$REPO_URL\" /work/repo\n"

// appRepo is the checkout's own repository as the rig's config records it: the
// GitHub App ID is what the project's GitHub job is composed from, and the two
// workflows are the ones the checkout's fugaro.yaml defines.
var appRepo = localcfg.Repo{Provider: "github", Workflows: []string{"app", "api"}, GitHubAppID: "12345"}

// onlyRepos makes the project's repository list exactly the checkout's own
// repository plus every name given, all on GitHub, and re-registers the
// installation doctor reads.
//
// Every test below starts from the checkout's repository alone, because that is
// the one list this write happens on at all: a repository this run holds no
// checkout of holds the entry back (defaultCatalogEntryRefusal's second state),
// so a test that lists another repository is about the reason the write waits.
func onlyRepos(t *testing.T, r *convergeRig, extra ...string) {
	t.Helper()
	repos := map[string]localcfg.Repo{"acme/app": appRepo}
	for _, name := range extra {
		repos[name] = localcfg.Repo{Provider: "github", Workflows: []string{"app"}, GitHubAppID: "12345"}
	}
	rewriteRigConfig(t, r.imagesRig, func(lc *localcfg.Config) { lc.Repos = repos })
	registerInstallation(t, r.initRig)
}

// ownImageRig is the converge rig over a checkout whose workflows write an
// image of their own, so that the entry is written for the one repository the
// project lists and the only variable left between the tests below is the one
// each is about.
func ownImageRig(t *testing.T) *convergeRig {
	t.Helper()
	r := ownImageRigNode(t, currentNode())
	onlyRepos(t, r)
	return r
}

// ownImageRigNode is ownImageRig over a published node of the caller's own, for
// the tests that are about which repositories the write waits for: those need a
// node whose allow_repos admits the repositories their config lists, because
// the reason they would read otherwise is the admission half of the guard,
// which is another test's.
func ownImageRigNode(t *testing.T, node map[string]any) *convergeRig {
	t.Helper()
	r := newConvergeRigYAML(t, node, bakedWorkflowYAML)
	for _, wf := range []string{"app", "api"} {
		checkoutFile(t, filepath.Join(r.root, ".fugaro", wf+".Dockerfile"), ownImageDockerfile)
	}
	return r
}

// writtenDefaultEntry is the default entry of the local config `fugaro update`
// left on the machine, and whether the config carries one at all.
func writtenDefaultEntry(t *testing.T) (config.Entry, bool) {
	t.Helper()
	cat := updatedConfig(t).ImageCatalog
	if cat == nil {
		return config.Entry{}, false
	}
	return catalog.Default(*cat)
}

// withheldEntryLine is the one line this write prints when it writes nothing,
// "" when it printed none: what happened and what is left for the operator are
// in that line, and an assertion over the whole output cannot tell it from the
// lines about everything else the update did.
func withheldEntryLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "no default catalog entry written") {
			return line
		}
	}
	return ""
}

// withheldEntryLineMax is the owner's rule of 2026-11-24: the warning a
// withheld write prints is one line of about this many characters, and what it
// cuts is in the run's log.
const withheldEntryLineMax = 140

// withheldEntryLog is the reason a withheld write is whole in: the line the
// screen reads is short, and the repositories, the workflow and the refusal it
// cut are here.
func withheldEntryLog(t *testing.T) string {
	t.Helper()
	_, log := onlyLog(t, "update")
	return log
}

// TestUpdateWritesTheDefaultEntryOnlyWhenEveryRepoIsAdmitted is S10's red step
// at the write: no step wrote a default entry, so the shared jobs of every
// profile ran the base image with nothing naming it. The project's one
// repository is admitted — the published node's allow_repos names it and the
// GitHub default profile this same run writes (S5, D2310) admits it — and it is
// the checkout this run holds, so the entry is written, it is the base image
// this run pinned, it reaches the published node (which is where a run reads
// the project's catalog) and the shared jobs of every profile are deployed on
// it.
//
// The entry's image is compared against the base image the same run recorded,
// and not against the fixture's: the base moves in every update (its images
// stage pins this release's), and an entry left on the release behind it is one
// every shared job of the project would run — the runner image a
// `policy-vs-jobs` warning is about.
func TestUpdateWritesTheDefaultEntryOnlyWhenEveryRepoIsAdmitted(t *testing.T) {
	r := ownImageRig(t)
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	entry, ok := writtenDefaultEntry(t)
	if !ok {
		t.Fatalf("after one fugaro update, this machine's config marks no default catalog entry, so the shared jobs of this project have no image of the project's own to run:\n%s\n%s",
			out, readFileString(t, installationConfigPath()))
	}
	if entry.Name != defaultCatalogEntryName {
		t.Errorf("the default entry is named %q, want %q", entry.Name, defaultCatalogEntryName)
	}
	if entry.Source.Kind != config.SourceImage {
		t.Errorf("the default entry's source kind is %q, want %q: the entry is the pinned base image, and nothing builds it (D2305)",
			entry.Source.Kind, config.SourceImage)
	}
	if want := updatedConfig(t).BaseImage(config.BaseKind); entry.Source.Image != want {
		t.Errorf("the default entry's image is %s, want the base image this run recorded, %s: the entry is the pinned base image (D2305), and one left on the release behind is the runner image every shared job of the project would run\n%s",
			entry.Source.Image, want, out)
	}
	// The published node is what a run reads the project's catalog from, so an
	// entry this run wrote and did not publish is an entry no job of the
	// project can resolve.
	node := convergeNode(t, r)
	if node.ImageCatalog == nil {
		t.Fatalf("the policy this update published carries no image catalog, so the entry the local config holds reached no run:\n%s", out)
	}
	published, ok := catalog.Default(*node.ImageCatalog)
	if !ok || published.Name != entry.Name || published.Source != entry.Source {
		t.Errorf("the published node's default entry is %+v (%v), want the one this run wrote to the local config, %+v: a run reads the profile's job image out of the policy, not out of this machine's file",
			published, ok, entry)
	}
	// And the shared job of every profile is deployed on it: the entry is the
	// image of a job whose repository names none (F21), which is every job the
	// profile deploys until an operator curates a baked image into the entry.
	jobs := installationProjectJobs(t, r)
	if len(jobs) == 0 {
		t.Fatalf("this update deployed no shared job at all: %v", r.tfvars(t)["project_jobs"])
	}
	for _, name := range slices.Sorted(maps.Keys(jobs)) {
		job, _ := jobs[name].(map[string]any)
		if job == nil {
			t.Fatalf("the job %s is %T, want the deployment's own object", name, jobs[name])
		}
		if got, _ := job["image"].(string); got != entry.Source.Image {
			t.Errorf("the shared job %s runs %s, want the default entry's image %s: a profile has no image field (R1), so the catalog's default is the only image its jobs can be deployed on",
				name, got, entry.Source.Image)
		}
	}
	if !strings.Contains(out, "wrote the default catalog entry "+defaultCatalogEntryName) {
		t.Errorf("the update does not say what it wrote, and a write into the operator's own file says what it wrote:\n%s", out)
	}
}

// TestUpdateLeavesTheEntryAndSaysWhyForAnUnadmittedRepo: the entry is the image
// of every job whose repository names no image, so a repository the project
// does not admit — which keeps the per-repository job its own root deploys —
// would be handed it, and a bare `checkout: baked` workflow of that repository
// is a job the deployer refuses (unbuiltOnBaked, D1191). The write therefore
// waits for the whole list of repositories the project lists, and says which
// one holds it back.
//
// The checkout's own workflows are the ones that could not be refused (the
// fixture above writes an image of their own), so the reason this run prints is
// the repository's and not the workflow's: what is under test is the admission
// half of the guard.
func TestUpdateLeavesTheEntryAndSaysWhyForAnUnadmittedRepo(t *testing.T) {
	const unadmitted = "github/acme/other"
	r := ownImageRig(t)
	// The operator's own GitHub profile, which admits the checkout's repository
	// and not the one this test adds: an existing profile is never rewritten
	// (R1), so the update writes no GitHub default to admit it with.
	rewriteRigConfig(t, r.imagesRig, func(lc *localcfg.Config) {
		lc.Profiles = map[string]config.JobProfile{projectpolicy.DefaultProfileName: {
			Provider: "github", CPU: 2, Memory: "4Gi", TimeoutClass: "2h",
			AllowRepos: []string{"github/acme/app"},
		}}
		lc.Repos["acme/other"] = localcfg.Repo{Provider: "github", Workflows: []string{"app"}, GitHubAppID: "12345"}
	})
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	if entry, ok := writtenDefaultEntry(t); ok {
		t.Errorf("this update wrote the default entry %+v, and %s is admitted by no profile: that repository keeps the per-repository job its own root deploys, and the entry would be the image of a job the deployer refuses where the workflow writes none of its own (unbuiltOnBaked)\n%s",
			entry, unadmitted, out)
	}
	line := withheldEntryLine(out)
	if line == "" {
		t.Errorf("this update wrote no default entry and says nothing about it, so the operator reads a project on the images it runs and not a project whose write was held back:\n%s", out)
	}
	if !strings.Contains(line, unadmitted) {
		t.Errorf("the line this update prints about the entry does not name %s, the repository that keeps it from being written:\n%s", unadmitted, out)
	}
}

// TestUpdateLeavesTheEntryAndSaysWhyForARepositoryTheProjectDoesNotAdmit is the
// other half of the admission the test above reads, and the half a profile
// cannot answer on its own: a repository is admitted when the project's own
// allow_repos names it AND a profile of its provider admits it
// (projectpolicy.Admits, the gate a run of a shared job is held to), and a
// publish keeps the node's allow_repos rather than re-seeding it from this
// config — so a repository this config lists and the published node does not
// admit is one whose runs every shared job refuses, whatever the profiles say.
// It keeps the per-repository job its own root deploys, and the entry waits for
// it exactly as it waits for a repository no profile admits.
func TestUpdateLeavesTheEntryAndSaysWhyForARepositoryTheProjectDoesNotAdmit(t *testing.T) {
	const unadmitted = "github/acme/other"
	r := ownImageRig(t)
	// A profile that admits it, which is the half that answers "admitted" on
	// its own: what is left is the node's allow_repos, which names the
	// checkout's repository alone (currentNode).
	rewriteRigConfig(t, r.imagesRig, func(lc *localcfg.Config) {
		lc.Profiles = map[string]config.JobProfile{projectpolicy.DefaultProfileName: {
			Provider: "github", CPU: 2, Memory: "4Gi", TimeoutClass: "2h",
			AllowRepos: []string{"github/acme/app", unadmitted},
		}}
		lc.Repos["acme/other"] = localcfg.Repo{Provider: "github", Workflows: []string{"app"}, GitHubAppID: "12345"}
	})
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	if entry, ok := writtenDefaultEntry(t); ok {
		t.Errorf("this update wrote the default entry %+v, and the policy published at %s admits %s on no shared job: that repository keeps the per-repository job its own root deploys, and the entry would be the image of a job the deployer refuses where the workflow writes none of its own (unbuiltOnBaked)\n%s",
			entry, projectpolicy.PathShared(), unadmitted, out)
	}
	line := withheldEntryLine(out)
	if line == "" {
		t.Errorf("this update wrote no default entry and says nothing about it, so the operator reads a project on the images it runs and not a project whose write was held back:\n%s", out)
	}
	if !strings.Contains(line, unadmitted) {
		t.Errorf("the line this update prints about the entry does not name %s, the repository the project's own allow_repos does not admit:\n%s", unadmitted, out)
	}
}

// TestUpdateLeavesTheEntryAndSaysWhyForARepositoryItDoesNotHold: a repository
// admitted to a profile still deploys the per-repository job of its own root
// (RepoSpec.Shared is a mark no root reads yet, M7), so the entry would be that
// job's image as much as an unadmitted repository's — and the workflows of a
// repository this machine holds no checkout of are not readable from here, so a
// bare `checkout: baked` one among them is refused by the deployer at its own
// repository stage, in a run this one cannot warn. The write therefore waits
// for a project whose list is the checkout's own alone, whatever admission
// says: the node and the profile this run writes both admit the second
// repository here, and the entry is still not written.
//
// The line the write prints names the repository and says nothing is left for
// the operator (nothing about that project needs repairing), and the refusal
// the entry would be the image of is in the run's log with it: the line is one
// line (the owner's feedback of 2026-11-24).
func TestUpdateLeavesTheEntryAndSaysWhyForARepositoryItDoesNotHold(t *testing.T) {
	const sibling = "github/acme/other"
	node := currentNode()
	node["allow_repos"] = []string{"github/acme/app", sibling}
	r := ownImageRigNode(t, node)
	onlyRepos(t, r, "acme/other")
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	if entry, ok := writtenDefaultEntry(t); ok {
		t.Errorf("this update wrote the default entry %+v while the project lists %s, a repository this run holds no checkout of: that repository keeps the per-repository job its own root deploys, the entry is that job's image, and its workflows are not readable here — so the deployer can refuse a bare baked one of them at its own repository stage\n%s",
			entry, sibling, out)
	}
	line := withheldEntryLine(out)
	if line == "" {
		t.Errorf("this update wrote no default entry and says nothing about it, so the operator reads a project on the images it runs and not a project whose write was held back:\n%s", out)
	}
	if !strings.Contains(line, sibling) {
		t.Errorf("the line this update prints about the entry does not name %s, the repository the write waits on:\n%s", sibling, out)
	}
	log := withheldEntryLog(t)
	for _, want := range []string{sibling, "unbuiltOnBaked"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not hold %q, which is the repository the write waits on and the refusal the entry would be the image of:\n%s", want, log)
		}
	}
}

// TestUpdatePreviousVersionWithABakedWorkflowConvergesInOneUpdate is the HARD
// RULE at this write's own refusal: an installation of the previous version,
// in a checkout whose workflows are bare `checkout: baked` ones, converges with
// `fugaro update` alone — and converges by NOT writing the entry, because the
// deployer would refuse those workflows the image the entry would be (D1191).
//
// It is the shape a project is in today: `checkout: baked` is the default and
// its workflows name no image, so an update that wrote the entry on the pinned
// base image without waiting would stop at the repository stage of this very
// run, and every later update of that installation with it. What the run does
// instead is leave the project exactly as it is, say which workflow holds the
// write back and what to do about it, and converge: the workflows keep the
// per-repository images their records show, and a later update writes the entry
// once a workflow names an image of its own (or the repository's job is one the
// project's default may serve).
func TestUpdatePreviousVersionWithABakedWorkflowConvergesInOneUpdate(t *testing.T) {
	r := newConvergeRig(t, staleNode(t))
	onlyRepos(t, r)
	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if got := convergeNode(t, r).Version; got != projectpolicy.Version {
		t.Fatalf("the published node is version %d after one update, want this binary's %d", got, projectpolicy.Version)
	}
	if entry, ok := writtenDefaultEntry(t); ok {
		t.Errorf("this update wrote the default entry %+v into a project whose workflows are bare baked ones: the repository stage of this same run is what refuses such a workflow the entry's image (unbuiltOnBaked, D1191), so the write is the one thing that would have stopped this update\n%s",
			entry, out)
	}
	if p := convergeNode(t, r); p.ImageCatalog != nil {
		t.Errorf("the policy this update published carries a catalog (%+v), want none: this machine's config names none, and an entry is the only thing this run would have written there", p.ImageCatalog)
	}
	line := withheldEntryLine(out)
	if line == "" {
		t.Errorf("this update wrote no default entry and says nothing about it, so the person reads a project on the images it runs and not a project whose write was held back:\n%s", out)
	}
	// The line names the workflow's repository and the remedy the operator
	// takes (an image of the workflow's own), which is the action this write
	// leaves them; the refusal behind it is in the log.
	for _, want := range []string{"acme/app", "dockerfile"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line this update prints about the entry does not name %q, which is the repository the write waits on and what the operator does about it:\n%s", want, out)
		}
	}
	log := withheldEntryLog(t)
	for _, want := range []string{"unbuiltOnBaked", "acme/app"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not hold %q, which is the reason the write waits and the repository it is about:\n%s", want, log)
		}
	}
}

// TestTheWithheldEntryWarningIsOneLineAndNamesTheAction is the owner's feedback
// of 2026-11-24: the warning this write prints when it writes nothing was one
// sentence of about 600 characters — the repository list, the per-repository
// job, `checkout: baked`, unbuiltOnBaked, D1191 and the state the project keeps
// — and "It's sooo LONG my eyes gloss over". What a person reads is one line of
// at most withheldEntryLineMax characters that says what happened and what, if
// anything, is left for them, and the facts it cut are in the run's log, which
// is where the same run's whole reason is kept (D1352).
//
// Two states, because the two halves of the rule are not the same sentence: a
// write that waits on a repository this machine holds no checkout of leaves the
// operator nothing to do, and one that waits on the checkout's own workflow
// names the remedy — an image of the workflow's own.
func TestTheWithheldEntryWarningIsOneLineAndNamesTheAction(t *testing.T) {
	t.Run("a repository this machine holds no checkout of", func(t *testing.T) {
		const sibling = "github/acme/other"
		node := currentNode()
		node["allow_repos"] = []string{"github/acme/app", sibling}
		r := ownImageRigNode(t, node)
		onlyRepos(t, r, "acme/other")
		out := convergeOn(t, r, []string{"policy-vs-jobs"})

		line := oneWithheldEntryLine(t, out)
		if !strings.Contains(line, "nothing to do") {
			t.Errorf("the line leaves the operator with something to do, and a project whose write waits on a repository this checkout does not hold is one nothing about needs repairing: %q", line)
		}
		if !strings.Contains(line, sibling) {
			t.Errorf("the line does not name %s, the repository the write waits on: %q", sibling, line)
		}
		if log := withheldEntryLog(t); !strings.Contains(log, sibling) || !strings.Contains(log, "unbuiltOnBaked") {
			t.Errorf("the log does not hold the repository the line names and the refusal behind it, so the facts the line cut are nowhere:\n%s", log)
		}
	})
	t.Run("a workflow of this checkout that writes no image of its own", func(t *testing.T) {
		r := newConvergeRig(t, staleNode(t))
		onlyRepos(t, r)
		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

		line := oneWithheldEntryLine(t, out)
		if !strings.Contains(line, "dockerfile") {
			t.Errorf("the line does not name what the operator does about the workflow that holds the write back, an image of the workflow's own: %q", line)
		}
	})
}

// oneWithheldEntryLine is the withheld write's one line, with the owner's rule
// over it: exactly one line, and at most withheldEntryLineMax characters.
func oneWithheldEntryLine(t *testing.T, out string) string {
	t.Helper()
	if n := strings.Count(out, "no default catalog entry written"); n != 1 {
		t.Errorf("the run printed %d lines about the withheld catalog entry, want one:\n%s", n, out)
	}
	line := withheldEntryLine(out)
	if line == "" {
		t.Fatalf("the run wrote no default catalog entry and said nothing about it:\n%s", out)
	}
	if n := len(line); n > withheldEntryLineMax {
		t.Errorf("the line is %d characters, want at most %d (the owner's feedback of 2026-11-24): %q", n, withheldEntryLineMax, line)
	}
	return line
}
