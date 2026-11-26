package cli

// RE3's tests: one command enrols a repository behind one confirmation, and
// every one of them is about a step of the converge rather than about the
// command having run.
//
// The fixture is the accounts converge's own world (accounts_add_converge_
// fake_test.go) with a catalog and a checkout: the two converges converge
// through the same three stages and the same database, and what is added here
// is the project's image catalog and a checkout of the repository being
// enrolled — which is where the provider, the workflow list and the base branch
// are read from, and nothing else in the command may invent any of them.
//
// The confirmation is the owner's own (D2403): `--yes` is gone, so every run
// below stands at both streams and types the project's name, which is the only
// answer the command takes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// enrolRigLocal is addRigLocal with the project's image catalog: the image-map
// step has nothing to choose from without one, and image_catalog is where a
// person writes it (D1140).
const enrolRigLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
image_catalog:
  entries:
    - name: base
      tags: [node]
      default: true
      source: { kind: inline, inline: "FROM scratch\n" }
`

// enrolRepo is the repository every test here enrols, enrolEntry the allow-list
// entry that follows from github/, and enrolImageKey the key the
// repository-to-image map holds it under (S3, D3370): provider/owner/name, the
// one spelling of a repository's identity.
const (
	enrolRepo     = "owner/repo-b"
	enrolEntry    = "github/owner/repo-b"
	enrolImageKey = "github/owner/repo-b"
)

// enrolCheckoutYAML is the repository's own fugaro.yaml, which is where its
// workflow list and its base branch are read from.
const enrolCheckoutYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// enrolRig is newAddRigWith over enrolRigLocal, standing in a checkout of
// enrolRepo: the working directory IS that checkout, which is how checkoutRoot
// finds one, and it is where the converge reads the workflows and the base
// branch from.
func enrolRig(t *testing.T) *addRig {
	t.Helper()
	return enrolRigOver(t, enrolRigLocal)
}

func enrolRigOver(t *testing.T, local string) *addRig {
	t.Helper()
	return enrolRigWith(t, local, true)
}

// enrolRigWithoutADefaultBranch is the same world with one thing missing: the
// checkout has no origin/HEAD, origin/main or origin/master ref, so the
// Repository stage has no default branch to read its fugaro.yaml from and skips
// — the state every fixture of this file was in before step 6 was pinned, and
// the one that must not be reported as a finished enrolment.
func enrolRigWithoutADefaultBranch(t *testing.T) *addRig {
	t.Helper()
	return enrolRigWith(t, enrolRigLocal, false)
}

// enrolRigWith is the world both of them stand in: an installation that exists
// (its state object is in the bucket) and a checkout of enrolRepo standing in
// the working directory, which is what makes step 6 more than a line in a plan —
// the Repository stage reads the installation's outputs out of that state and
// converges the checkout's own root.
//
// The state object alone is not enough for that read: the fake terraform has to
// answer it with a state that holds resources, and a root whose state manages
// nothing is refused as an installation that was forgotten
// (installationOutputs). The rig's own script answers every state read with a
// plan, which carries none.
func enrolRigWith(t *testing.T, local string, defaultBranch bool) *addRig {
	t.Helper()
	r := newAddRigWith(t, local)
	r.installationState(t)
	r.script["show -json@installation"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"module.installation.google_project.this"}]}}}`}
	r.script["output@installation"] = map[string]any{"stdout": outputsJSON(t)}
	r.save(t)
	enrolCheckoutAt(t, enrolCheckoutYAML, defaultBranch)
	return r
}

// enrolLanded commits the gcp_project line the Repository stage wrote into this
// checkout and moves the remote-tracking ref onto it, as pushing the edit and
// fetching would: the stage reads the working tree's fugaro.yaml and refuses one
// that is not the default branch's (resolveRepoTarget), so the second run of a
// fixture is a run over a checkout whose onboarding is merged. What it is not is
// a fixture detail: the engine leaves the anchor in the working tree of the run
// that onboarded the repository, and its own words are "review it with git diff
// and commit it like any change".
func enrolLanded(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the anchor the enrolment wrote")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
}

// enrolCheckoutFile is the fugaro.yaml of the checkout this run stands in. It is
// where the Repository stage's anchor lands, which is the one write in the whole
// of step 6 that a test can read back: nothing else of the checkout's root is on
// this machine.
func enrolCheckoutFile(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "fugaro.yaml")
}

// enrolCheckout makes the working directory a checkout of enrolRepo with yaml
// as its fugaro.yaml and the origin remote that says which repository it is.
func enrolCheckout(t *testing.T, yaml string) string {
	t.Helper()
	return enrolCheckoutAt(t, yaml, true)
}

// enrolCheckoutAt is enrolCheckout with the remote-tracking ref the Repository
// stage reads its default branch through, for the fixtures that are about a
// checkout without one.
func enrolCheckoutAt(t *testing.T, yaml string, defaultBranch bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout-b")
	gitCheckout(t, dir, yaml)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/owner/repo-b.git")
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "fugaro.yaml")
	if defaultBranch {
		// The ref the repository stage reads the default branch's fugaro.yaml
		// through (defaultBranchRef): origin/main at the commit that carries
		// the file, which is what a checkout of a repository whose default
		// branch is main has once it has fetched.
		testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	}
	t.Chdir(dir)
	return dir
}

// enrol runs the command the way the OWNER does — a real terminal on both
// streams, the project's name typed at the one question — and returns what it
// printed. It is the owner's run and not a scripted one because the command has
// no other shape (D2403): `--yes` is gone, and a fixture that answered with a
// flag would be testing a door this command does not have.
func (r *addRig) enrol(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ownerAtTheTerminal(t)
	return executeOwnerStdin(t, "aurora\n", append([]string{"project", "repos", "add"}, args...)...)
}

// enrolOwnerAt is enrol for the runs that have already put the session at a
// terminal of their own — the ones about a refusal that comes before it.
func (r *addRig) enrolOwnerAt(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return executeOwnerStdin(t, "aurora\n", append([]string{"project", "repos", "add"}, args...)...)
}

// planStep is the plan's own numbered line, by the number it opens with: an
// assertion about a step is an assertion about that step's own line.
func planStep(t *testing.T, plan string, n int) string {
	t.Helper()
	tag := fmt.Sprintf("\n  %d. ", n)
	i := strings.Index(plan, tag)
	if i < 0 {
		t.Fatalf("the plan has no step %d:\n%s", n, plan)
	}
	rest := plan[i+1:]
	if j := strings.Index(rest, "\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestReposAddPrintsTheSixStepsInOrder is D2400: the six steps are printed as a
// numbered list, each with the value this run resolved for it, before anything
// is written and before the one question — and the build this command does NOT
// do is named at the end of that list.
func TestReposAddPrintsTheSixStepsInOrder(t *testing.T) {
	r := enrolRig(t)
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	conf := strings.Index(out, "⚠ CONFIRM")
	if conf < 0 {
		t.Fatalf("the one confirmation was not asked:\n%s", out)
	}
	plan := out[:conf]
	for i, want := range []string{
		"the repository's own fugaro.yaml: read for its workflows ([web])",
		"project config: writes repos." + enrolRepo + ": ",
		"project config: writes repo_images[" + enrolImageKey + "] = base",
		"allow-list: allow_repos:",
		"publish: the policy node " + projectpolicy.PathShared(),
		"job: the repository's job set and its roles/run.invoker grant",
	} {
		if got := planStep(t, plan, i+1); !strings.Contains(got, want) {
			t.Errorf("the plan's step %d is %q, and it has to carry %q", i+1, got, want)
		}
	}
	// The build is the seventh thing said, and it is not one of the six the
	// question covers.
	build := strings.Index(out, "the first image build is not part of this")
	if build < 0 {
		t.Fatalf("the plan does not name the first image build as the one thing it does not do:\n%s", out)
	}
	if !strings.Contains(out[build:], "fugaro image build <entry>") {
		t.Errorf("the plan names the build and not the command that makes it:\n%s", out[build:])
	}
	if build > conf {
		t.Errorf("the confirmation was asked before the list named the build it does not cover:\n%s", out)
	}
}

// TestReposAddRefusesWithTheFixingCommand is D2401: what the command cannot
// read is refused before anything is written — and before the plan is shown and
// the question asked — with a sentence naming the one command that supplies it.
func TestReposAddRefusesWithTheFixingCommand(t *testing.T) {
	t.Run("no checkout", func(t *testing.T) {
		// The config already lists the repository and the working directory is
		// not a checkout of it: the provider and the workflow list could be read
		// from the config, but the base branch and the job's own deployment
		// cannot, and D2401 refuses rather than converging half a repository.
		r := enrolRigOver(t, enrolledRigLocal())
		t.Chdir(t.TempDir())
		ownerAtTheTerminal(t)
		before := readFileString(t, r.cfg)
		out, _, err := r.enrolOwnerAt(t, enrolRepo)
		if err == nil || !strings.Contains(err.Error(), "--allow-list-only") {
			t.Fatalf("project repos add %s without a checkout: err = %v, want the refusal that names --allow-list-only\nstdout:\n%s", enrolRepo, err, out)
		}
		if !strings.Contains(err.Error(), "Run it from a checkout of "+enrolRepo) {
			t.Errorf("the refusal does not name the checkout it needs: %v", err)
		}
		if strings.Contains(out, "⚠ CONFIRM") {
			t.Errorf("the refused run asked the confirmation:\n%s", out)
		}
		if got := readFileString(t, r.cfg); got != before {
			t.Errorf("the refused run wrote the local config:\n%s", got)
		}
	})

	t.Run("a fugaro.yaml naming no workflow", func(t *testing.T) {
		r := newAddRigWith(t, enrolRigLocal)
		enrolCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\n")
		out, _, err := r.enrol(t, enrolRepo)
		if err == nil || !strings.Contains(err.Error(), "workflow list") {
			t.Fatalf("err = %v, want the refusal that names the workflow list\nstdout:\n%s", err, out)
		}
		if !strings.Contains(err.Error(), "--allow-list-only") {
			t.Errorf("the refusal names no way forward: %v", err)
		}
	})

	t.Run("an image_name the catalog does not hold", func(t *testing.T) {
		r := newAddRigWith(t, enrolRigLocal)
		enrolCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n  web: { image_name: nosuch, commands: { build: sh build.sh, test: sh test.sh } }\n")
		out, _, err := r.enrol(t, enrolRepo)
		if err == nil || !strings.Contains(err.Error(), "cannot read the image web's job runs") {
			t.Fatalf("err = %v, want the refusal of the unresolved image\nstdout:\n%s", err, out)
		}
		// The entry is the repository's own, so the catalog's door is the one
		// named and not the map's.
		if !strings.Contains(err.Error(), "fugaro image add <entry>") {
			t.Errorf("the refusal does not name the command that supplies the entry: %v", err)
		}
	})

	t.Run("no GitHub App id to read", func(t *testing.T) {
		// The project's only other repository is a Bitbucket one, so there is no
		// App ID anywhere in it to onboard a GitHub repository with.
		local := strings.Replace(enrolRigLocal,
			`  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }`,
			`  owner/repo-a: { provider: bitbucket, workflows: [web] }`, 1)
		local = strings.Replace(local, "- github/owner/repo-a", "- bitbucket/owner/repo-a", 1)
		r := enrolRigOver(t, local)
		out, _, err := r.enrol(t, enrolRepo)
		if err == nil || !strings.Contains(err.Error(), "GitHub App ID") {
			t.Fatalf("err = %v, want the refusal that names the App ID\nstdout:\n%s", err, out)
		}
		if !strings.Contains(err.Error(), "fugaro settings set repos."+enrolRepo+".github_app_id") {
			t.Errorf("the refusal does not name the command that supplies the App ID: %v", err)
		}
	})
}

// TestReposAddConvergesTheRepositoryRoot is step 6 where it is real: the plan's
// last step is the repository's job set and its roles/run.invoker grant, the
// stage that deploys them is `fugaro update`'s Repository stage, and what says
// the stage applied rather than stood aside is the anchor it writes into the
// checkout once the engine has converged it — the one write of that step that
// lands on this machine.
func TestReposAddConvergesTheRepositoryRoot(t *testing.T) {
	r := enrolRig(t)
	if strings.Contains(readFileString(t, enrolCheckoutFile(t)), "gcp_project") {
		t.Fatalf("the fixture's checkout already carries the anchor:\n%s", readFileString(t, enrolCheckoutFile(t)))
	}
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if !strings.Contains(out, "the checkout's own root was converged and deployed") {
		t.Errorf("the report does not say the checkout's own root was converged:\n%s", out)
	}
	if got := readFileString(t, enrolCheckoutFile(t)); !strings.Contains(got, "gcp_project: proj-1234") {
		t.Errorf("the repository stage did not write the checkout's anchor, so its engine never converged the root:\n%s", got)
	}
}

// TestReposAddDoesNotReportAnEnrolmentTheRepositoryStageSkipped: the command's
// answer is `fugaro run --repo … is ready`, and that claim is step 6's — the
// repository's job set and its roles/run.invoker grant, which only the
// Repository stage deploys. A stage that neither applied nor found the work
// already done leaves the repository on the allow-list, in this machine's
// config and with no job of its own, and a run that answered "ready" over it
// would be read by the operator and found out at the first `fugaro run --repo`.
//
// The stage's own fix is what the refusal carries, and it is the stage's to
// write: a checkout with no default branch to read says which fetch it wants.
// What this adds is where the run picks up — every step before it has landed.
func TestReposAddDoesNotReportAnEnrolmentTheRepositoryStageSkipped(t *testing.T) {
	r := enrolRigWithoutADefaultBranch(t)
	// The entry the config is asserted to hold below is the run's own step 2 and
	// not the fixture's: a fixture that carried it already would pass that
	// assertion without the run having written anything.
	before := readFileString(t, r.cfg)
	if strings.Contains(before, enrolRepo) {
		t.Fatalf("the fixture's config already lists %s:\n%s", enrolRepo, before)
	}
	out, _, err := r.enrol(t, enrolRepo)
	if err == nil {
		t.Fatalf("project repos add %s over a checkout with no default branch reported success:\n%s", enrolRepo, out)
	}
	text := ErrorText(err)
	// The stage's own sentence, which is the fix.
	if !strings.Contains(text, "run git fetch origin, then rerun fugaro setup --repo") {
		t.Errorf("the refusal does not carry the stage's own fix:\n%s", text)
	}
	// And where to pick up, with the work that is already done.
	if !strings.Contains(text, "resumes at the repository step") || !strings.Contains(text, "already written") {
		t.Errorf("the refusal does not say that a rerun resumes at the repository step:\n%s", text)
	}
	// The claim it may not make, and the step it would have claimed.
	if strings.Contains(out, "fugaro run --repo") {
		t.Errorf("the run answered that the repository is ready:\n%s", out)
	}
	if strings.Contains(readFileString(t, enrolCheckoutFile(t)), "gcp_project") {
		t.Errorf("the skipped stage wrote the checkout's anchor:\n%s", readFileString(t, enrolCheckoutFile(t)))
	}
	// Steps 1 to 4 did land before it, and they are what a rerun resumes from:
	// the local config and the published list are not rolled back.
	if got, ok := r.addLocal(t).Repos[enrolRepo]; !ok || got.Provider != "github" {
		t.Errorf("repos.%s = %v (present %v), want the entry step 2 wrote", enrolRepo, got, ok)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v, want %s from step 4", p.AllowRepos, enrolEntry)
	}
}

// TestReposAddGuardsHoldOnTheConvergePath is the default path's own guards: the
// converge asks the same questions `repos add` asked before it converged — the
// access gate, the ownership of the database, the deployed rules — and every one
// of them lands before the plan is printed and before the one question, so a
// refused run writes nothing and asks nothing.
func TestReposAddGuardsHoldOnTheConvergePath(t *testing.T) {
	for _, tc := range []struct {
		what  string
		world func(t *testing.T, r *addRig)
		want  []string
	}{
		{
			what: "an all-repos admin at the terminal",
			world: func(t *testing.T, r *addRig) {
				accessAs(t, r.db, accessAdminAddr,
					userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, "all-repos"))
			},
			want: []string{"nothing was enrolled", "admin at project scope", "(user:admin@example.com) hold admin at all-repos"},
		},
		{
			what:  "another installation's database",
			world: func(t *testing.T, r *addRig) { r.db.Set(budget.PathMark, birchMark()) },
			want:  []string{"nothing was enrolled", "one Firebase project serves one installation"},
		},
		{
			what:  "a database with no mark at all",
			world: func(t *testing.T, r *addRig) { r.db.Set(budget.PathMark, nil) },
			want:  []string{"nothing was enrolled", "no Fugaro mark"},
		},
		{
			what:  "a deployment whose rules carry no marker",
			world: func(t *testing.T, r *addRig) { r.db.Set(projectpolicy.PathRulesMarker(), nil) },
			want:  []string{"carry no policy marker", "run 'fugaro update'"},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := enrolRig(t)
			tc.world(t, r)
			beforeCfg, beforeNode := readFileString(t, r.cfg), r.nodeText(t)
			out, _, err := r.enrol(t, enrolRepo)
			if err == nil {
				t.Fatalf("project repos add %s with %s was allowed:\n%s", enrolRepo, tc.what, out)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Fatalf("exit %d, want %d — a refusal, not a failure: %v", got, ExitUserError, err)
			}
			text := ErrorText(err)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal of %s does not say %q:\n%s", tc.what, want, text)
				}
			}
			// Nothing was written and nothing was asked: the guard is above the
			// plan, so the operator never read a plan of steps that were not
			// going to happen either.
			if got := readFileString(t, r.cfg); got != beforeCfg {
				t.Errorf("the refused run wrote this machine's config:\n--- before\n%s\n--- after\n%s", beforeCfg, got)
			}
			if got := r.nodeText(t); got != beforeNode {
				t.Errorf("the refused run rewrote the published node:\n--- before\n%s\n--- after\n%s", beforeNode, got)
			}
			if strings.Contains(out, "⚠ CONFIRM") {
				t.Errorf("the refused run asked the one question:\n%s", out)
			}
			if strings.Contains(out, "1. the repository's own fugaro.yaml:") {
				t.Errorf("the refused run printed the plan it was never going to run:\n%s", out)
			}
		})
	}
}

// TestReposAddIsIdempotentOverACompletedEnrolment is D2402's first reading: a
// repository already enrolled converges to the same state, so a second run
// writes nothing and says so — with the sentence `runProjectReposEdit` already
// prints for an entry the list holds, because the two paths are one group.
func TestReposAddIsIdempotentOverACompletedEnrolment(t *testing.T) {
	r := enrolRig(t)
	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	// The checkout as the first run left it is a checkout with an uncommitted
	// edit, and a second run over that is refused by the Repository stage before
	// it reaches any of the sentences below: landing the anchor is the operator's
	// own step between the two runs.
	enrolLanded(t)
	before, node := r.addLocal(t), r.nodeText(t)
	asked := len(r.db.Requests())

	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("the second run: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(out, enrolEntry+" is already on the allow-list; nothing changed") {
		t.Errorf("the second run does not print the sentence the allow-list's own editor prints for that state:\n%s", out)
	}
	for _, want := range []string{"already repos." + enrolRepo, "already repo_images[" + enrolImageKey + "]", "is already as this enrolment wants it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the second run's answer does not say %q:\n%s", want, out)
		}
	}
	// The three documents this command writes, and nothing else in the file: a
	// stage records its own work in it (the policy stage's generation), and that
	// record is the stage's and not this command's step.
	after := r.addLocal(t)
	if !reflect.DeepEqual(after.Repos, before.Repos) {
		t.Errorf("the second run changed the repos: block:\n--- before\n%+v\n--- after\n%+v", before.Repos, after.Repos)
	}
	if !reflect.DeepEqual(after.RepoImages, before.RepoImages) {
		t.Errorf("the second run changed the repo_images: block:\n--- before\n%+v\n--- after\n%+v", before.RepoImages, after.RepoImages)
	}
	if after.ImageMap != nil {
		t.Errorf("the second run left an image_map: block in this machine's config: %+v", after.ImageMap)
	}
	if !reflect.DeepEqual(after.Profiles, before.Profiles) {
		t.Errorf("the second run changed the allow_repos the profiles carry:\n--- before\n%+v\n--- after\n%+v", before.Profiles, after.Profiles)
	}
	if got := r.nodeText(t); got != node {
		t.Errorf("the second run rewrote the published node:\n--- before\n%s\n--- after\n%s", node, got)
	}
	// No writer reached the published policy node: that is what the sentence
	// above claims, and it is the half a byte comparison alone cannot make (a
	// write of the same bytes leaves none). What the second run does write is
	// step 6's, which is `fugaro update`'s own Repository stage and deploys the
	// repository's job set again — the credential nodes on that path are the
	// stage's, and the enrolment is not what put them there.
	for _, req := range r.db.Requests()[asked:] {
		if req.Method != http.MethodGet && strings.Contains(req.Path, "/config/policy/") {
			t.Errorf("%s %s reached the published policy over an enrolment that is already complete: the run has nothing to write there", req.Method, req.Path)
		}
	}
}

// TestReposAddConvergesAHalfDoneEnrolment is D2402's second reading, and the
// state the owner actually has: a repository this machine's config holds and
// the published list does not. The run converges the missing steps rather than
// refusing the revision as already done.
func TestReposAddConvergesAHalfDoneEnrolment(t *testing.T) {
	r := enrolRigOver(t, enrolledRigLocal())
	if slices.Contains(r.policy(t).AllowRepos, enrolEntry) {
		t.Fatalf("the fixture is not half-done: the allow-list already admits %s", enrolEntry)
	}
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v: the half-done enrolment did not converge", p.AllowRepos)
	}
	if got := r.addLocal(t).RepoImages[enrolImageKey]; got != "base" {
		t.Errorf("repo_images[%s] = %q: the half-done enrolment did not converge", enrolImageKey, got)
	}
	// The entry that was already there is answered as already done and the step
	// that was missing as written: one run, both readings.
	if !strings.Contains(out, "already repos."+enrolRepo) {
		t.Errorf("the run does not say the entry was already what the enrolment wants:\n%s", out)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, "github/owner/repo-a") {
		t.Errorf("allow_repos = %v: the entry that was already there was dropped", p.AllowRepos)
	}
}

// TestReposAddPublishesOverTheGenerationItsAllowListWriteLanded is the state
// every installation is in once it has published anything: this machine's config
// records the generation its policy keys were written from (D854), and that is
// the generation the node is at. Step 4 writes the node, which moves it, so the
// stages of steps 5 and 6 have to be handed the config as step 4 left it: a
// config read before that write states a generation the node has moved past, and
// the policy stage answers that with D854's re-seed — writing the node's values
// into the file, over the two local steps that just landed, instead of
// publishing.
func TestReposAddPublishesOverTheGenerationItsAllowListWriteLanded(t *testing.T) {
	r := enrolRig(t)
	gen := enrolNodeGeneration(t, r)
	enrolRecordGeneration(t, r, gen)
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s over a config that records its policy generation: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	if strings.Contains(out, "re-seeded") {
		t.Errorf("the run re-seeded this machine's config from the node rather than publishing:\n%s", out)
	}
	after := r.addLocal(t)
	// The two local steps are the ones a re-seed takes away with it: it writes
	// the node's values into the file, and the node carries neither a repos entry
	// for this repository nor a repo-to-image map.
	if got := after.RepoImages[enrolImageKey]; got != "base" {
		t.Errorf("repo_images[%s] = %q after the run, want the mapping step 3 wrote: %v", enrolImageKey, got, after.RepoImages)
	}
	if _, ok := after.Repos[enrolRepo]; !ok {
		t.Errorf("repos has no entry for %s after the run: %v", enrolRepo, after.Repos)
	}
	// The file and the node agree at the end of the run, which is the invariant
	// the generation record exists for: a file left naming the generation from
	// before this command's own write refuses the operator's next edit.
	if want := enrolNodeGeneration(t, r); after.PolicyGeneration != want {
		t.Errorf("policy_generation = %q, want the generation the node is at (%q)", after.PolicyGeneration, want)
	}
}

// enrolNodeGeneration is the generation the published node is at, read the way
// the command reads it: the ETag of the document at the shared path.
func enrolNodeGeneration(t *testing.T, r *addRig) string {
	t.Helper()
	db, err := newBudgetClient(t.Context(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	etag, found, _, err := readPolicyNode(t.Context(), db, r.addLocal(t))
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}
	return etag
}

// enrolRecordGeneration writes gen into the local config as its recorded
// policy_generation, which is the record every publish and every allow-list edit
// leaves behind (putAllowRepos, D854).
func enrolRecordGeneration(t *testing.T, r *addRig, gen string) {
	t.Helper()
	out, err := yamledit.SetScalar([]byte(readFileString(t, r.cfg)), []string{"policy_generation"}, gen)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReposAddMergesThePublishedImageMap is the trap D1906's keep rule sets: a
// local config that names no repo-to-image map states, once this step writes
// one, the project's WHOLE map — and would take every other repository's
// mapping away with it. The effective map is what the write starts from, and
// under R13 it is the migrated one (S3): the mapping the node holds as a bare
// name reaches the file under its provider, beside this repository's key.
func TestReposAddMergesThePublishedImageMap(t *testing.T) {
	r := enrolRig(t)
	if r.addLocal(t).ImageMap != nil {
		t.Fatalf("the fixture's local config already names a map: %v", r.addLocal(t).ImageMap)
	}
	// The node holds the project's other repository's mapping, in the escaped
	// shape the block is stored in on the node.
	node := addPolicyNode()
	raw, err := json.Marshal(projectpolicy.ImageMap{"owner/repo-a": "base"})
	if err != nil {
		t.Fatal(err)
	}
	var block map[string]any
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatal(err)
	}
	node["image_map"] = block
	r.db.Set(projectpolicy.PathShared(), node)

	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	m := r.addLocal(t).RepoImages
	if m["github/owner/repo-a"] != "base" {
		t.Errorf("repo_images = %v: the mapping the published node held for the project's other repository was replaced rather than merged into, under its provider", m)
	}
	if m[enrolImageKey] != "base" {
		t.Errorf("repo_images = %v, want this repository's entry beside it", m)
	}
	// The node's own mapping is the project's, and the converge publishes what
	// this machine's file now states.
	if got := r.policy(t).ImageMap["owner/repo-a"]; got != "base" {
		t.Errorf("the published image_map lost the other repository's mapping: %v", r.policy(t).ImageMap)
	}
}

// TestReposAddWritesProviderKey is S3's own claim: the mapping this command
// writes is a key of repo_images — provider/owner/name (R8, D3370) — and the
// bare name the old member was keyed by is not a key of it.
func TestReposAddWritesProviderKey(t *testing.T) {
	r := enrolRig(t)
	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	m := r.addLocal(t).RepoImages
	if got := m[enrolImageKey]; got != "base" {
		t.Errorf("repo_images = %v, want %s = base", m, enrolImageKey)
	}
	if _, ok := m[enrolRepo]; ok {
		t.Errorf("repo_images = %v: the bare name %s is a key of it, and a key of this member carries its provider", m, enrolRepo)
	}
}

// TestReposAddNeverWritesImageMap is S3's red step and R13's rule in one: a
// project whose map is still the legacy one has it MIGRATED into the file — the
// mapping it served reaches repo_images under its provider — and the old member
// is not written into this machine's config at all.
func TestReposAddNeverWritesImageMap(t *testing.T) {
	r := enrolRig(t)
	// The node holds the project's map in the old form, as an installation that
	// has not run this release's `update` does.
	node := addPolicyNode()
	raw, err := json.Marshal(projectpolicy.ImageMap{"owner/repo-a": "base"})
	if err != nil {
		t.Fatal(err)
	}
	var block map[string]any
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatal(err)
	}
	node["image_map"] = block
	r.db.Set(projectpolicy.PathShared(), node)

	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, r.cfg); strings.Contains(got, "image_map") {
		t.Errorf("the run wrote image_map into this machine's project config:\n%s", got)
	}
	m := r.addLocal(t).RepoImages
	if m["github/owner/repo-a"] != "base" {
		t.Errorf("repo_images = %v: the mapping the project served as a bare name was not migrated into the file", m)
	}
	if m[enrolImageKey] != "base" {
		t.Errorf("repo_images = %v, want this repository's own key beside it", m)
	}
}

// TestReposAddAllowListOnlyWritesOnlyTheList is D2404: the behaviour the
// command had before it converged survives as one flag, because a pattern has
// no checkout, no fugaro.yaml and no image to converge — and it asks nothing.
func TestReposAddAllowListOnlyWritesOnlyTheList(t *testing.T) {
	r := newAddRigWith(t, enrolledRigLocal())
	noTerminal(t)
	before := readFileString(t, r.cfg)
	out, errOut, err := executeOwnerStdin(t, "", "project", "repos", "add", enrolRepo, "--allow-list-only")
	if err != nil {
		t.Fatalf("project repos add --allow-list-only: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if got := readFileString(t, r.cfg); got != before {
		t.Errorf("--allow-list-only wrote the local config:\n--- before\n%s\n--- after\n%s", before, got)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v, want %s on it", p.AllowRepos, enrolEntry)
	}
	if strings.Contains(out, "⚠ CONFIRM") || strings.Contains(out, "the first image build is not part of this") {
		t.Errorf("--allow-list-only ran the converge's plan:\n%s", out)
	}
	// And the wildcard, which has no converge at all, is reachable through it.
	if _, _, err := executeOwnerStdin(t, "", "project", "repos", "add", "*", "--allow-list-only"); err != nil {
		t.Fatalf("project repos add * --allow-list-only: %v", err)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, "*") {
		t.Errorf("allow_repos = %v, want * on it", p.AllowRepos)
	}
}

// TestReposAddRefusesStar is D2404's other half: `*` is the allow-list's own
// entry for every repository and not a repository, so there is no provider, no
// checkout, no workflow list and no image to converge — and the flag that
// writes exactly that wide list is named.
func TestReposAddRefusesStar(t *testing.T) {
	r := enrolRig(t)
	before := readFileString(t, r.cfg)
	out, _, err := r.enrol(t, "*")
	if err == nil || !strings.Contains(err.Error(), "--allow-list-only") {
		t.Fatalf("project repos add *: err = %v, want the refusal that names --allow-list-only\nstdout:\n%s", err, out)
	}
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the refused run asked the confirmation:\n%s", out)
	}
	if got := readFileString(t, r.cfg); got != before {
		t.Errorf("the refused run wrote the local config:\n%s", got)
	}
}

// TestReposAddHasNoYesFlag is D2403: `--yes` was removed by the no-prompt
// policy's T11 and this task does not bring it back, so the one question is
// answered by the project's name typed at a terminal and by nothing on the
// command line.
func TestReposAddHasNoYesFlag(t *testing.T) {
	cmd, _, err := NewRootCmd().Find([]string{"project", "repos", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if f := cmd.Flags().Lookup("yes"); f != nil {
		t.Errorf("`project repos add` registers --yes again: %s", f.Usage)
	}
	f := cmd.Flags().Lookup("allow-list-only")
	if f == nil {
		t.Fatal("`project repos add` registers no --allow-list-only")
	}
	if !strings.Contains(f.Usage, "allow-list") {
		t.Errorf("--allow-list-only's own text does not say what it writes: %q", f.Usage)
	}
	// Nothing on the command line answers the question: with no terminal the run
	// refuses rather than confirming itself.
	r := enrolRig(t)
	noTerminal(t)
	before := readFileString(t, r.cfg)
	out, _, err := r.enrolOwnerAt(t, enrolRepo)
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("with no terminal: err = %v, want the refusal that needs one\nstdout:\n%s", err, out)
	}
	if got := readFileString(t, r.cfg); got != before {
		t.Errorf("the unconfirmed run wrote the local config:\n%s", got)
	}
	// The plan was printed, and the question it could not ask was not answered
	// on its behalf.
	if !strings.Contains(out, "1. the repository's own fugaro.yaml:") {
		t.Errorf("the refusal came before the plan:\n%s", out)
	}
}

// TestReposAddRefusesAnAgentSession is SD8 kept: this command writes the
// published node, this machine's config, the job set and an IAM grant, so it is
// a person's step and never a coding agent's — and the refusal comes before
// anything is read or written.
func TestReposAddRefusesAnAgentSession(t *testing.T) {
	r := enrolRig(t)
	before := readFileString(t, r.cfg)
	t.Setenv(agentMarkers[0], "1")
	out, _, err := r.enrolOwnerAt(t, enrolRepo)
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("in an agent session: err = %v, want the SD8 refusal\nstdout:\n%s", err, out)
	}
	if strings.Contains(out, "⚠ CONFIRM") || strings.Contains(out, "the repository's own fugaro.yaml:") {
		t.Errorf("the refusal came after the plan or the question:\n%s", out)
	}
	if got := readFileString(t, r.cfg); got != before {
		t.Errorf("the refused run wrote the local config:\n%s", got)
	}
}

// enrolledRigLocal is enrolRigLocal with the repository already listed in the
// project's repos — every key this command would state, so the only steps the
// half-done fixtures leave are the allow-list and the image map — written as
// one config rather than by two writers on one file.
func enrolledRigLocal() string {
	return strings.Replace(enrolRigLocal,
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n",
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n"+
			"  "+enrolRepo+": { provider: github, base_branch: main, github_app_id: \"12345\", workflows: [web] }\n", 1)
}

// nodeText is the published node as the bytes it holds: the "wrote nothing"
// assertions are made against the storage and never against a value the command
// printed.
func (r *addRig) nodeText(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// enrolAccountLocal is enrolRigLocal with the account bmb already serving the
// project's other repository: the shape a project is in when the next
// repository to enrol is one the account has never been told about.
func enrolAccountLocal() string {
	local := strings.Replace(enrolRigLocal,
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n",
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web], claude_accounts: [bmb] }\n", 1)
	return local + "claude_accounts:\n  bmb:\n    allow_data_to: [owner/repo-a, owner/repo-c, owner/repo-d]\n"
}

// TestReposAddLeavesAConfigTheNextPublishAccepts is the live bug of 2026-11-22:
// `repos add` wrote claude_accounts: [bmb] onto the entry and left bmb's own
// allow_data_to without the repository, so every later `fugaro update` was
// refused by the validator. The repository's own file names the account
// (agent.oauth_token), which is the one statement the command may extend the
// trust list on.
func TestReposAddLeavesAConfigTheNextPublishAccepts(t *testing.T) {
	r := enrolRigOver(t, enrolAccountLocal())
	enrolCheckout(t, strings.Replace(enrolCheckoutYAML, "agent: { auth: oauth }", "agent: { auth: oauth, oauth_token: bmb }", 1))
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	lc := r.addLocal(t)
	if got := lc.Repos[enrolRepo].ClaudeAccounts; !slices.Equal(got, []string{"bmb"}) {
		t.Fatalf("the entry's claude_accounts is %v, want [bmb]", got)
	}
	if !lc.ClaudeAccounts["bmb"].AllowsData(enrolRepo) {
		t.Errorf("claude_accounts.bmb.allow_data_to is %v and does not name %s", lc.ClaudeAccounts["bmb"].AllowDataTo, enrolRepo)
	}
	if _, err := projectpolicy.DeriveAccounts(policyProfiles(lc), lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts()); err != nil {
		t.Errorf("the config repos add left is one the publish refuses: %v", err)
	}
	if !strings.Contains(out, "claude_accounts.bmb.allow_data_to") || !strings.Contains(out, "agent.oauth_token") {
		t.Errorf("the run does not say it widened the account's list, and why:\n%s", out)
	}
}

// TestReposAddNeverWidensAnAccountTheRepositoryDoesNotName: an account read
// from the project's other repositories is a guess about this one, so where
// the repository's own file does not name it the account stays off the entry
// and its trust list is left exactly as it was.
func TestReposAddNeverWidensAnAccountTheRepositoryDoesNotName(t *testing.T) {
	r := enrolRigOver(t, enrolAccountLocal())
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}
	lc := r.addLocal(t)
	if lc.ClaudeAccounts["bmb"].AllowsData(enrolRepo) {
		t.Errorf("the allow list of bmb was widened to %v for a repository whose file does not name the account", lc.ClaudeAccounts["bmb"].AllowDataTo)
	}
	if got := lc.Repos[enrolRepo].ClaudeAccounts; len(got) != 0 {
		t.Errorf("the entry names %v, an account that does not cover it", got)
	}
	if _, err := projectpolicy.DeriveAccounts(policyProfiles(lc), lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts()); err != nil {
		t.Errorf("the config repos add left is one the publish refuses: %v", err)
	}
	if !strings.Contains(out, "accounts allow bmb "+enrolRepo) {
		t.Errorf("the run does not name the command that gives the account to the repository:\n%s", out)
	}
}

// TestReposAddRefusesAnEntryThatAlreadyNamesAnUncoveredAccount: an entry the
// file holds already names an account whose list excludes the repository, and
// the repository's own file does not name that account. Nothing here may widen
// the list on the entry's say-so, and nothing may be written over a config the
// publish will refuse: the refusal comes before any write with the fix.
func TestReposAddRefusesAnEntryThatAlreadyNamesAnUncoveredAccount(t *testing.T) {
	local := strings.Replace(enrolAccountLocal(),
		"  owner/repo-a:", "  "+enrolRepo+": { provider: github, base_branch: main, github_app_id: \"12345\", workflows: [web], claude_accounts: [bmb] }\n  owner/repo-a:", 1)
	r := enrolRigOver(t, local)
	before, node := r.addLocal(t), r.nodeText(t)
	out, _, err := r.enrol(t, enrolRepo)
	if err == nil {
		t.Fatalf("repos add accepted an entry the publish refuses:\n%s", out)
	}
	for _, want := range []string{"nothing was written", "accounts allow bmb " + enrolRepo} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
	if !reflect.DeepEqual(r.addLocal(t).ClaudeAccounts, before.ClaudeAccounts) || r.nodeText(t) != node {
		t.Errorf("a refused run wrote something")
	}
}

// TestDoctorNamesARepositoryTheNextPublishWillRefuse: a config where an entry
// names an account that does not cover it is reported before `fugaro update` is
// run, with the command that closes it; one that is covered is silent.
func TestDoctorNamesARepositoryTheNextPublishWillRefuse(t *testing.T) {
	bad := strings.Replace(enrolAccountLocal(),
		"  owner/repo-a:", "  "+enrolRepo+": { provider: github, github_app_id: \"12345\", workflows: [web], claude_accounts: [bmb] }\n  owner/repo-a:", 1)
	lc, err := localcfg.Parse([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	checks := doctorRepoAccountChecks(lc)
	if len(checks) != 1 || checks[0].Severity != "warning" || !strings.Contains(checks[0].Fix, "accounts allow bmb "+enrolRepo) {
		t.Fatalf("doctor's answer is %+v, want one warning naming `accounts allow bmb %s`", checks, enrolRepo)
	}
	good, err := localcfg.Parse([]byte(enrolAccountLocal()))
	if err != nil {
		t.Fatal(err)
	}
	if got := doctorRepoAccountChecks(good); len(got) != 0 {
		t.Errorf("doctor warns of a config the publish accepts: %+v", got)
	}
}
