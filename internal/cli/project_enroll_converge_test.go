package cli

// RE1's tests: one command enrols a repository, and every one of them is about
// a step of the converge rather than about the command having run.
//
// The fixture is the accounts fixture (accounts_add_converge_fake_test.go) with
// a catalog and a checkout, because the two converges converge through the same
// stages and the same database: newAddRigWith builds the installation, the
// budget mark, the deployed rules generation and the published node, and what
// is added here is the project's image catalog and a checkout of the repository
// being enrolled — which is where the provider and the workflow list are read
// from, and nothing else in the command may invent either.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// enrolRigLocal is addRigLocal with the project's image catalog: the map step
// has nothing to choose from without one, and the catalog is where a person
// writes it (image_catalog, D1140).
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

// enrolCheckoutYAML is the repository's own statement: the provider and the
// workflow selection the converge reads and is forbidden to guess.
const enrolCheckoutYAML = `version: 1
project: aurora
git: { provider: github }
agent: { auth: oauth }
workflows:
  web: { commands: { build: sh build.sh, test: sh test.sh } }
`

// enrolCheckoutNoWorkflowsYAML is this repository's checkout stating no
// selection: the file is there and names no `workflows:`, which is the state
// the workflow refusal has to tell apart from there being no checkout here.
const enrolCheckoutNoWorkflowsYAML = `version: 1
project: aurora
git: { provider: github }
agent: { auth: oauth }
`

// enrolRepo is the repository every test here enrols, and enrolEntry is the
// allow-list entry that follows from github/.
const (
	enrolRepo  = "owner/repo-b"
	enrolEntry = "github/owner/repo-b"
)

// enrolRig is newAddRigWith over enrolRigLocal, standing in a checkout of
// enrolRepo with its origin remote set: the working directory IS the checkout,
// which is how checkoutRoot resolves one (cloud.go), so the fixture chdirs.
func enrolRig(t *testing.T) *addRig {
	t.Helper()
	return enrolRigOver(t, enrolRigLocal)
}

func enrolRigOver(t *testing.T, local string) *addRig {
	t.Helper()
	r := newAddRigWith(t, local)
	atEnrolCheckout(t)
	return r
}

// atEnrolCheckout makes the working directory a checkout of enrolRepo, with
// the origin remote that says so and a committed fugaro.yaml.
func atEnrolCheckout(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout-b")
	gitCheckout(t, dir, enrolCheckoutYAML)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/owner/repo-b.git")
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "fugaro.yaml")
	t.Chdir(dir)
	return dir
}

// enrol runs the command the way the person it is for does: the project's name
// typed at what the confirmation reads as their own terminal. No flag answers
// it (T11 took --yes out of the tree), so the name and the terminal are the
// whole of what stands between this fixture and the converge.
func (r *addRig) enrol(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return r.enrolTyping(t, r.addLocal(t).Name, args...)
}

// enrolTyping is enrol with the line the person types under it, for the
// fixtures that are about the answer rather than about the converge.
func (r *addRig) enrolTyping(t *testing.T, typed string, args ...string) (string, string, error) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })
	return executeStdin(t, typed+"\n", append([]string{"project", "repos", "add"}, args...)...)
}

// TestProjectReposAddEnrolsInOneRun is the whole of RE1 in one run: the six
// steps land, each read from the repository's own checkout and from the
// project, the plan printed before the question named them in the order the
// command works in, and the answer after it says what each of them did.
func TestProjectReposAddEnrolsInOneRun(t *testing.T) {
	r := enrolRig(t)
	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
	}

	// The plan is printed before the question, in the order the command works
	// in: its own three writes first, then the three stages it invokes, in the
	// registered order initflow.Validate sorts them into — the publish last.
	plan := out[:strings.Index(out, "⚠ CONFIRM")]
	at := func(needle string) int { return strings.Index(plan, needle) }
	for _, pair := range [][2]string{
		{"local config:", "image map:"},
		{"image map:", "allow-list:"},
		{"allow-list:", "job:"},
		{"job:", "repository:"},
		{"repository:", "publish:"},
	} {
		if at(pair[0]) < 0 || at(pair[1]) < 0 || at(pair[0]) >= at(pair[1]) {
			t.Fatalf("the plan does not name %q before %q:\n%s", pair[0], pair[1], plan)
		}
	}

	// 1. the local config entry, read from the checkout's own fugaro.yaml and
	// from the project's other GitHub repository.
	entry, ok := r.addLocal(t).Repos[enrolRepo]
	if !ok {
		t.Fatalf("the converge wrote no repos entry for %s:\n%s", enrolRepo, r.cfgText(t))
	}
	if entry.Provider != "github" || entry.GitHubAppID != "12345" || !slices.Equal(entry.Workflows, []string{"web"}) {
		t.Errorf("repos[%s] = %+v, want the provider from the checkout, the workflow list from its fugaro.yaml and the App ID from the project's other GitHub repository", enrolRepo, entry)
	}

	// 2. the image map, chosen by the one rule every job's image is resolved by.
	if got := r.addLocal(t).ImageMap[enrolEntry]; got != "base" {
		t.Errorf("image_map[%s] = %q, want the catalog's default entry\nstdout:\n%s", enrolEntry, got, out)
	}

	// 3. the published allow-list.
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v, want %s on it beside the entry that was there", p.AllowRepos, enrolEntry)
	}

	// 4. the three stages, as the report's own rows: the line under each label
	// is the stage's own answer, read off its result, so a stage dropped from
	// the list the run is given — the one failure the plan and the three writes
	// above cannot show — answers "not run" here instead of the run quietly
	// accounting for fewer steps than the plan named.
	for _, tc := range []struct{ label, want string }{
		{"job", "the project's job set (fugaro update's installation stage)"},
		// The repository stage is invoked and answers here too; its answer is
		// the reason it could not converge rather than "the checkout's own
		// root", because this fixture's checkout holds no remote-tracking ref
		// to read a default branch from and a repository root is applied
		// against installation state a rig this size does not stand up. What
		// the row pins is the same thing the two above pin: the stage ran.
		{"repository", "skipped: no origin/HEAD, origin/main or origin/master ref here"},
		{"publish", "the node " + projectpolicy.PathShared() + " carries this repository's admission and this machine's config"},
	} {
		if got := accountsAddRow(t, out, tc.label); !strings.Contains(got, tc.want) {
			t.Errorf("the answer's %s row is %q, want the stage's own answer to carry %q\nstdout:\n%s", tc.label, got, tc.want, out)
		}
	}
}

// TestProjectReposAddSecondRunChangesNothing is the idempotence the task asks
// for: enrolling a repository that is already enrolled converges to the same
// state, and a step that has nothing to do says so rather than doing it again.
//
// "Changes nothing" is asserted against each document that matters and not
// against one hash. The published node is compared as bytes: nothing in this
// command moves it. The local config is compared as the config it decodes to —
// because the ONE thing that does move is not this command's: the
// installation stage re-marshals the file (writeConfig), and the policy
// stage's generation record appends its key through yamledit, so the key's
// position in the file settles on the run after the record is first written.
// A command that borrowed `fugaro update`'s stages inherits that, and a
// converge whose own three steps wrote nothing is what this test is about; the
// third run, whose file has settled, is byte-identical.
func TestProjectReposAddSecondRunChangesNothing(t *testing.T) {
	r := enrolRig(t)
	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	before, node := r.addLocal(t), r.nodeText(t)

	out, errOut, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("the second run: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if got := r.addLocal(t); !reflect.DeepEqual(got, before) {
		t.Errorf("the second run changed the local config:\n--- before\n%+v\n--- after\n%+v", before, got)
	}
	if got := r.nodeText(t); got != node {
		t.Errorf("the second run rewrote the published node:\n--- before\n%s\n--- after\n%s", node, got)
	}
	// This command's own three steps say there was nothing to do.
	for _, want := range []string{"already repos." + enrolRepo, "already on the allow-list", "already as this enrolment wants it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the second run's answer does not say %q:\n%s", want, out)
		}
	}

	// Settled: the third run leaves the file exactly as it found it.
	settled := r.cfgText(t)
	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatalf("the third run: %v", err)
	}
	if got := r.cfgText(t); got != settled {
		t.Errorf("the third run rewrote the local config:\n--- before\n%s\n--- after\n%s", settled, got)
	}
}

// TestProjectReposAddMergesThePublishedImageMap is the trap the task names: a
// local config that names no repo-to-image map would, under the keep rule
// (D1906), state the project's WHOLE map when this step writes one — and take
// every other repository's mapping away with it. The published map is read and
// this entry is added to it.
func TestProjectReposAddMergesThePublishedImageMap(t *testing.T) {
	r := enrolRig(t)
	// The project's other repository is mapped where the project's map lives.
	node := addPolicyNode()
	// The block as the node holds it, through the package's own encoder: RTDB
	// refuses a key with "/" in it, so a map written by hand here would be a
	// document the project could never have published.
	raw, err := json.Marshal(projectpolicy.ImageMap{"github/owner/repo-a": "base"})
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
	if m["github/owner/repo-a"] != "base" {
		t.Errorf("image_map = %v: the mapping the published node held for the project's other repository was replaced rather than merged into", m)
	}
	if m[enrolEntry] != "base" {
		t.Errorf("image_map = %v, want this repository's entry beside it", m)
	}
}

// TestProjectReposAddHalfEnrolledConverges is the installation the task asks
// about: a repository whose entry is in the local config and whose allow-list
// entry is not. The run leaves it fully enrolled rather than refusing it as
// already done.
func TestProjectReposAddHalfEnrolledConverges(t *testing.T) {
	r := enrolRigOver(t, enrolledRigLocal())
	if _, _, err := r.enrol(t, enrolRepo); err != nil {
		t.Fatal(err)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v: the half-enrolled installation did not converge", p.AllowRepos)
	}
	if got := r.addLocal(t).ImageMap[enrolEntry]; got != "base" {
		t.Errorf("image_map[%s] = %q: the half-enrolled installation did not converge", enrolEntry, got)
	}
}

// TestProjectReposAddAllowListOnlyWritesOnlyTheList is the flag that keeps the
// behaviour the command had before RE1: one entry in the published list and
// nothing else.
func TestProjectReposAddAllowListOnlyWritesOnlyTheList(t *testing.T) {
	r := newAddRigWith(t, enrolledRigLocal())
	before := r.cfgText(t)
	if _, _, err := r.enrol(t, enrolRepo, "--allow-list-only"); err != nil {
		t.Fatal(err)
	}
	if got := r.cfgText(t); got != before {
		t.Errorf("--allow-list-only wrote the local config:\n--- before\n%s\n--- after\n%s", before, got)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v, want %s on it", p.AllowRepos, enrolEntry)
	}
}

// TestProjectReposAddRefusesAnAnswerThatIsNotTheName: the one confirmation is
// the project's name, so any other line is a refusal rather than an answer, and
// it lands before the first write — the plan the run printed is the whole of
// what it did.
func TestProjectReposAddRefusesAnAnswerThatIsNotTheName(t *testing.T) {
	r := enrolRig(t)
	before, node := r.cfgText(t), r.nodeText(t)
	out, _, err := r.enrolTyping(t, "not the name", enrolRepo)
	if err == nil || !strings.Contains(err.Error(), "was not typed") {
		t.Fatalf("an answer that is not the project's name: err = %v, want the refusal that says the name was not typed\nstdout:\n%s", err, out)
	}
	if !strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("the refused run never showed what it was asking about:\n%s", out)
	}
	if got := r.cfgText(t); got != before {
		t.Errorf("the refused run wrote the local config:\n--- before\n%s\n--- after\n%s", before, got)
	}
	if got := r.nodeText(t); got != node {
		t.Errorf("the refused run wrote the published node:\n--- before\n%s\n--- after\n%s", node, got)
	}
}

// TestProjectReposAddRefusesStar is the one refusal the task names, in every
// spelling of it: a pattern is the list's own entry for the repositories it
// matches and not a repository, so the converge has nothing to converge — and
// the flag that writes exactly that entry is the only way to ask for it. A
// pattern refused by the provider read behind this one would be a refusal
// whose remedy is the next refusal (D2404): the read names
// `fugaro settings set repos.<pattern>.provider`, which leads into the
// workflow refusal behind it and so to hand-writing a pattern's provider and
// workflow list into the config — which is the write that flag exists for.
func TestProjectReposAddRefusesStar(t *testing.T) {
	for _, arg := range []string{"*", "owner/*", "github/owner/*", "github/owner/re*"} {
		r := enrolRig(t)
		before := r.cfgText(t)
		_, _, err := r.enrol(t, arg)
		if err == nil || !strings.Contains(err.Error(), "--allow-list-only") {
			t.Fatalf("project repos add %s: err = %v, want the refusal that names --allow-list-only", arg, err)
		}
		if got := r.cfgText(t); got != before {
			t.Errorf("project repos add %s wrote the local config:\n%s", arg, got)
		}
	}
	r := enrolRig(t)
	if _, _, err := r.enrol(t, "*", "--allow-list-only"); err != nil {
		t.Fatalf("project repos add * --allow-list-only: %v", err)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, "*") {
		t.Errorf("allow_repos = %v, want * on it", p.AllowRepos)
	}
}

// TestReposAddRefusesWithTheFixingCommand is RE3's red step, and D2401's rule
// run against the command: a value the converge cannot read is refused with the
// one command that supplies it. The value here is the workflow list, the
// repository's own statement and the one thing this command may not guess — and
// the fixing command it names is `fugaro project repos add <canonical>
// --allow-list-only`, which is the invocation a listing's own remedy reaches
// this refusal with on a machine that has neither a `repos:` entry nor a
// checkout of it.
//
// The second half is the state the same refusal has to tell apart:
// checkoutConfig answers nil both for "no checkout here" and for a checkout
// whose fugaro.yaml states no selection, so a sentence that sent an operator
// standing in the checkout to that checkout would send them nowhere.
func TestReposAddRefusesWithTheFixingCommand(t *testing.T) {
	fixing := "fugaro project repos add " + enrolEntry + " --allow-list-only"

	r := newAddRigWith(t, enrolRigLocal)
	t.Chdir(t.TempDir())
	before := r.cfgText(t)

	_, _, err := r.enrol(t, enrolEntry)
	if err == nil || !strings.Contains(err.Error(), fixing) {
		t.Fatalf("project repos add %s away from a checkout: err = %v, want the refusal that names %q", enrolEntry, err, fixing)
	}
	if got := r.cfgText(t); got != before {
		t.Errorf("the refused run wrote the local config:\n%s", got)
	}

	dir := filepath.Join(t.TempDir(), "checkout-b")
	gitCheckout(t, dir, enrolCheckoutNoWorkflowsYAML)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/owner/repo-b.git")
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "fugaro.yaml")
	t.Chdir(dir)

	_, _, err = r.enrol(t, enrolEntry)
	if err == nil || !strings.Contains(err.Error(), fixing) {
		t.Fatalf("project repos add %s in its own checkout, whose fugaro.yaml states no selection: err = %v, want the refusal that names %q", enrolEntry, err, fixing)
	}
	if strings.Contains(err.Error(), "run this from a checkout of") {
		t.Errorf("the refusal sends an operator who is standing in the checkout back to a checkout: %v", err)
	}
}

// TestProjectReposAddReadsTheProviderFromTheCanonicalArgument: the argument
// `project repos ls` and `project show` print as the remedy is the canonical
// one (enrollableArg: "the canonical form is the one that stands on its own"),
// so it has to be a command that works. The provider it names is a fact the
// operator typed, and this is the machine that has nothing else to read it
// from: the local config lists the repository — which is where the workflow
// list comes from, and nothing here guesses one — without a provider, and the
// working directory is not a checkout of it.
func TestProjectReposAddReadsTheProviderFromTheCanonicalArgument(t *testing.T) {
	local := strings.Replace(enrolRigLocal,
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n",
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n"+
			"  "+enrolRepo+": { workflows: [web] }\n", 1)
	r := newAddRigWith(t, local)
	t.Chdir(t.TempDir())

	out, _, err := r.enrol(t, enrolEntry)
	if err != nil {
		t.Fatalf("project repos add %s, the form the listings print: %v\n%s", enrolEntry, err, out)
	}
	entry, ok := r.addLocal(t).Repos[enrolRepo]
	if !ok || entry.Provider != "github" {
		t.Errorf("repos[%s] = %+v, want the provider the canonical argument names\nstdout:\n%s", enrolRepo, entry, out)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v, want %s on it", p.AllowRepos, enrolEntry)
	}
}

// TestProjectReposAddRefusesAnAgentSession is SD8 kept: this command writes the
// published node, the job set and an IAM grant, so it is a person's step and
// never a coding agent's — and the refusal comes before anything is written.
func TestProjectReposAddRefusesAnAgentSession(t *testing.T) {
	r := enrolRig(t)
	before := r.cfgText(t)
	t.Setenv(agentMarkers[0], "1")
	_, _, err := r.enrol(t, enrolRepo)
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("in an agent session: err = %v, want the SD8 refusal", err)
	}
	if got := r.cfgText(t); got != before {
		t.Errorf("the refused run wrote the local config:\n%s", got)
	}
}

// TestProjectReposAddRefusesBelong is the rule the owner-run live checks keep,
// kept here in their own shape: no step that writes a real installation's job
// set and IAM grant runs against `belong` (internal/agentenv).
func TestProjectReposAddRefusesBelong(t *testing.T) {
	local := strings.Replace(enrolRigLocal, "name: aurora", "name: "+enrolBelongName, 1)
	r := enrolRigOver(t, local)
	before := r.cfgText(t)
	_, _, err := r.enrol(t, enrolRepo)
	if err == nil || !strings.Contains(err.Error(), enrolBelongName) {
		t.Fatalf("installation %s: err = %v, want the refusal by that name", enrolBelongName, err)
	}
	if got := r.cfgText(t); got != before {
		t.Errorf("the refused run wrote the local config:\n%s", got)
	}
}

// TestProjectReposAddLeavesTheJobStepWithoutACheckout: the job set is deployed
// from a checkout, so a repository enrolled from somewhere else converges as far
// as it can and names the one command that finishes it.
func TestProjectReposAddLeavesTheJobStepWithoutACheckout(t *testing.T) {
	// The config already lists the repository, so the provider and the workflow
	// list come from it — which is the state a repository enrolled from
	// somewhere else is in — and the working directory is not a checkout of it.
	r := newAddRigWith(t, enrolledRigLocal())
	t.Chdir(t.TempDir())

	out, _, err := r.enrol(t, enrolRepo)
	if err != nil {
		t.Fatalf("project repos add %s without a checkout: %v\n%s", enrolRepo, err, out)
	}
	if !strings.Contains(out, "fugaro update --repo") {
		t.Errorf("the answer does not name the one command that deploys the job:\n%s", out)
	}
	if p := r.policy(t); !slices.Contains(p.AllowRepos, enrolEntry) {
		t.Errorf("allow_repos = %v: the three steps that need no checkout did not land", p.AllowRepos)
	}
}

// enrolledRigLocal is enrolRigLocal with the repository already listed in the
// project's repos: the state the half-enrolled fixtures start from, written as
// one config rather than by two writers on one file.
func enrolledRigLocal() string {
	return strings.Replace(enrolRigLocal,
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n",
		"  owner/repo-a: { provider: github, github_app_id: \"12345\", workflows: [web] }\n"+
			"  "+enrolRepo+": { provider: github, github_app_id: \"12345\", workflows: [web] }\n", 1)
}

// cfgText and nodeText are the two documents a converge writes, as the bytes
// they hold: the file itself and the published node, so "changed nothing" is
// asserted against the storage and never against a value the command printed.
func (r *addRig) cfgText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *addRig) nodeText(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(r.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
