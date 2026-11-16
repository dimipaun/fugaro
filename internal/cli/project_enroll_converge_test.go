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
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
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
// entry that follows from github/, and enrolMapKey the image map's key for it —
// owner/name, the name a job's own repository is looked up by (localcfg's
// image_map rule), and not the canonical one.
const (
	enrolRepo   = "owner/repo-b"
	enrolEntry  = "github/owner/repo-b"
	enrolMapKey = "owner/repo-b"
)

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
	r := newAddRigWith(t, local)
	enrolCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n")
	return r
}

// enrolCheckout makes the working directory a checkout of enrolRepo with yaml
// as its fugaro.yaml and the origin remote that says which repository it is.
func enrolCheckout(t *testing.T, yaml string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout-b")
	gitCheckout(t, dir, yaml)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/owner/repo-b.git")
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "fugaro.yaml")
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
		"project config: writes image_map[" + enrolMapKey + "] = base",
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

// TestReposAddIsIdempotentOverACompletedEnrolment is D2402's first reading: a
// repository already enrolled converges to the same state, so a second run
// writes nothing and says so — with the sentence `runProjectReposEdit` already
// prints for an entry the list holds, because the two paths are one group.
func TestReposAddIsIdempotentOverACompletedEnrolment(t *testing.T) {
	r := enrolRig(t)
	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	before, node := r.addLocal(t), r.nodeText(t)
	asked := len(r.db.Requests())

	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("the second run: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(out, enrolEntry+" is already on the allow-list; nothing changed") {
		t.Errorf("the second run does not print the sentence the allow-list's own editor prints for that state:\n%s", out)
	}
	for _, want := range []string{"already repos." + enrolRepo, "already image_map[" + enrolMapKey + "]", "is already as this enrolment wants it"} {
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
	if !reflect.DeepEqual(after.ImageMap, before.ImageMap) {
		t.Errorf("the second run changed the image_map: block:\n--- before\n%+v\n--- after\n%+v", before.ImageMap, after.ImageMap)
	}
	if !reflect.DeepEqual(after.Profiles, before.Profiles) {
		t.Errorf("the second run changed the allow_repos the profiles carry:\n--- before\n%+v\n--- after\n%+v", before.Profiles, after.Profiles)
	}
	if got := r.nodeText(t); got != node {
		t.Errorf("the second run rewrote the published node:\n--- before\n%s\n--- after\n%s", node, got)
	}
	// No writer was reached: the database recorded no write after the first
	// run, which is the claim the sentence above makes and the one a byte
	// comparison alone cannot make (a write of the same bytes leaves none).
	for _, req := range r.db.Requests()[asked:] {
		if req.Method != http.MethodGet {
			t.Errorf("%s %s reached the database over an enrolment that is already complete: the run has nothing to write", req.Method, req.Path)
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
	if got := r.addLocal(t).ImageMap[enrolMapKey]; got != "base" {
		t.Errorf("image_map[%s] = %q: the half-done enrolment did not converge", enrolMapKey, got)
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

// TestReposAddMergesThePublishedImageMap is the trap D1906's keep rule sets: a
// local config that names no repo-to-image map states, once this step writes
// one, the project's WHOLE map — and would take every other repository's
// mapping away with it. The effective map is what the write starts from.
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
	m := r.addLocal(t).ImageMap
	if m["owner/repo-a"] != "base" {
		t.Errorf("image_map = %v: the mapping the published node held for the project's other repository was replaced rather than merged into", m)
	}
	if m[enrolMapKey] != "base" {
		t.Errorf("image_map = %v, want this repository's entry beside it", m)
	}
	// The node's own mapping is the project's, and the converge publishes what
	// this machine's file now states.
	if got := r.policy(t).ImageMap["owner/repo-a"]; got != "base" {
		t.Errorf("the published image_map lost the other repository's mapping: %v", r.policy(t).ImageMap)
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
