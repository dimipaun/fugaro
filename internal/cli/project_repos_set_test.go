package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// project_repos_set.go's tests (G10, D1173, docs/config-management/spec.md
// §3.4): the two shapes a bare scalar cannot express, in this machine's project
// config.
//
//   - a job profile's block, addressed by name (D1163), which is the seven keys
//     the registry spells job_profiles.* and this file spells profiles.*
//     (TestProjectReposSetWritesAJobProfileBlock), and the refusal of a name
//     that is neither defined nor given the provider a new profile owes
//     (TestProjectReposSetRefusesAnUnknownProfile);
//   - a repository's workflow selection, written whole rather than one item at
//     a time (TestProjectReposSetWritesAWorkflowSelection), which is the shape
//     the generic writer's --add/--remove cannot express;
//   - the other half of D1173: the repos.* scalars and lists are the GENERIC
//     writer's (TestSettingsSetReposScalarWritesTheReposBlock);
//   - the fence the SEC marking is about: a job profile carries secrets and
//     allow_repos, so a coding agent's session writes none of it
//     (TestProjectReposSetIsRefusedInACodingAgentsSession).
//
// Every fixture is a file localcfg.Parse accepts, because the write path holds
// the edited bytes against that loader before writing them (settingsReparse).
// What a profile's VALUES owe — a known provider, a Cloud Run cpu/memory shape,
// a duration timeout_class — is projectpolicy.validProfiles' at the next
// `fugaro project policy publish`, which is stated in the command's own help.

// projectReposLocalConfig is the local config these tests write into: one job
// profile and one repository, so each shape has something to change and
// something the write must leave alone.
const projectReposLocalConfig = `profiles:
  standard:
    provider: github
    cpu: 2
    memory: 4Gi
    timeout_class: 6h
claude_accounts:
  team-a:
    allow_data_to:
      - acme/app
repos:
  acme/app:
    provider: github
    base_branch: main
    workflows:
      - web
`

// projectReposConfig reads the test's project config back as the command left
// it.
func projectReposConfig(t *testing.T) (string, *localcfg.Config) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config after the write: %v", err)
	}
	return path, lc
}

// TestProjectReposSetWritesAJobProfileBlock is the plan's red step: the
// subcommand does not exist before it. The block is created under the name
// --job-profile addresses, with the seven keys the flags name, and both the
// profile and the repository the file already held are left alone.
func TestProjectReposSetWritesAJobProfileBlock(t *testing.T) {
	settingsWriteHomeWith(t, projectReposLocalConfig)
	path := os.Getenv("FUGARO_CONFIG")

	out, _, err := execute(t, "project", "repos", "set", "--job-profile", "gpu",
		"--provider", "github", "--cpu", "4", "--memory", "8Gi", "--timeout-class", "12h",
		"--secrets", "acme-db,acme-queue", "--allow-repos", "acme/app", "--claude-accounts", "team-a")
	if err != nil {
		t.Fatalf("project repos set --job-profile: %v\n%s", err, out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not end with the next step every settings write names:\n%s", out)
	}
	after := readFileString(t, path)
	// The write splices the profile into the file's own `profiles:` block, so
	// what "left alone" means here is that the blocks the file already carried
	// are still byte for byte what they were.
	for _, keep := range []string{
		"claude_accounts:\n  team-a:\n    allow_data_to:\n      - acme/app\n",
		"repos:\n  acme/app:\n    provider: github\n    base_branch: main\n    workflows:\n      - web\n",
	} {
		if !strings.Contains(after, keep) {
			t.Errorf("the write changed the block the file already held:\n%s", after)
		}
	}
	_, lc := projectReposConfig(t)
	pr, ok := lc.Profiles["gpu"]
	if !ok {
		t.Fatalf("profiles.gpu is not in the config after the write:\n%s", after)
	}
	if pr.Provider != "github" || pr.CPU != 4 || pr.Memory != "8Gi" || pr.TimeoutClass != "12h" {
		t.Errorf("profiles.gpu = %+v, want provider github, cpu 4, memory 8Gi and timeout_class 12h", pr)
	}
	if want := []string{"acme-db", "acme-queue"}; !slices.Equal(pr.Secrets, want) {
		t.Errorf("profiles.gpu.secrets = %v, want %v", pr.Secrets, want)
	}
	if want := []string{"acme/app"}; !slices.Equal(pr.AllowRepos, want) || !pr.AllowReposWritten() {
		t.Errorf("profiles.gpu.allow_repos = %v (written %v), want %v written", pr.AllowRepos, pr.AllowReposWritten(), want)
	}
	if want := []string{"team-a"}; !slices.Equal(pr.ClaudeAccounts, want) {
		t.Errorf("profiles.gpu.claude_accounts = %v, want %v", pr.ClaudeAccounts, want)
	}
	if std := lc.Profiles["standard"]; std.Provider != "github" || std.CPU != 2 || std.Memory != "4Gi" || std.TimeoutClass != "6h" {
		t.Errorf("the profile the file already held changed: %+v", std)
	}
	if r := lc.Repos["acme/app"]; r.BaseBranch != "main" || !slices.Equal(r.Workflows, []string{"web"}) {
		t.Errorf("the repository the file already held changed: %+v", r)
	}
}

// TestProjectReposSetWritesAWorkflowSelection: the repository's selection is
// written whole — a second run replaces it rather than adding to it — and the
// entry's other members are untouched. A repository the project's config does
// not list is refused before the file is read, because a selection stored under
// a name nothing looks up is a line the operator would never see fail.
func TestProjectReposSetWritesAWorkflowSelection(t *testing.T) {
	settingsWriteHomeWith(t, projectReposLocalConfig)
	path := os.Getenv("FUGARO_CONFIG")

	out, _, err := execute(t, "project", "repos", "set", "acme/app", "--workflows", "web,docs")
	if err != nil {
		t.Fatalf("project repos set acme/app --workflows: %v\n%s", err, out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not end with the next step every settings write names:\n%s", out)
	}
	_, lc := projectReposConfig(t)
	if want := []string{"web", "docs"}; !slices.Equal(lc.Repos["acme/app"].Workflows, want) {
		t.Fatalf("repos.acme/app.workflows = %v, want %v", lc.Repos["acme/app"].Workflows, want)
	}
	if r := lc.Repos["acme/app"]; r.Provider != "github" || r.BaseBranch != "main" {
		t.Errorf("the write changed a member of the entry it was not asked about: %+v", r)
	}
	if _, _, err := execute(t, "project", "repos", "set", "acme/app", "--workflows", "docs"); err != nil {
		t.Fatalf("the second selection: %v", err)
	}
	_, lc = projectReposConfig(t)
	if want := []string{"docs"}; !slices.Equal(lc.Repos["acme/app"].Workflows, want) {
		t.Errorf("repos.acme/app.workflows = %v, want the selection replaced by %v", lc.Repos["acme/app"].Workflows, want)
	}

	before := readFileString(t, path)
	out, _, err = execute(t, "project", "repos", "set", "acme/other", "--workflows", "web")
	if err == nil {
		t.Fatalf("a repository the config does not list was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "acme/other") {
		t.Errorf("the refusal does not name the repository: %v", err)
	}
	if readFileString(t, path) != before {
		t.Errorf("a refused write left the file changed:\n%s", readFileString(t, path))
	}
}

// TestProjectReposSetRefusesAnUnknownProfile: an address that names neither a
// profile the config defines nor a new one the write makes whole is refused,
// and the file is left byte for byte as it was. A profile with no provider is
// one no job of it can be deployed (projectpolicy.validProfiles), which is what
// the sentence says beside the profiles the file does define.
func TestProjectReposSetRefusesAnUnknownProfile(t *testing.T) {
	settingsWriteHomeWith(t, projectReposLocalConfig)
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)

	out, _, err := execute(t, "project", "repos", "set", "--job-profile", "nosuch", "--cpu", "4")
	if err == nil {
		t.Fatalf("an unknown profile was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nosuch") || !strings.Contains(err.Error(), "standard") {
		t.Errorf("the refusal names neither the profile nor the ones the file defines: %v", err)
	}
	if !strings.Contains(err.Error(), "--provider") {
		t.Errorf("the refusal does not say how a profile is made whole: %v", err)
	}
	if readFileString(t, path) != before {
		t.Errorf("a refused write left the file changed:\n%s", readFileString(t, path))
	}
}

// TestSettingsSetReposScalarWritesTheReposBlock is D1173's other half: a
// repos.* scalar and a repos.* list are the GENERIC writer's, so a person edits
// one with the command the read's OWNER column names rather than through the
// block's own subcommand. The write lands on the entry the file spells, in the
// local project config.
func TestSettingsSetReposScalarWritesTheReposBlock(t *testing.T) {
	settingsWriteHomeWith(t, projectReposLocalConfig)

	out, _, err := execute(t, "settings", "set", "repos.acme/app.base_branch", "release")
	if err != nil {
		t.Fatalf("settings set repos.acme/app.base_branch: %v\n%s", err, out)
	}
	out, _, err = execute(t, "settings", "set", "repos.acme/app.workflows", "--add", "docs")
	if err != nil {
		t.Fatalf("settings set repos.acme/app.workflows --add: %v\n%s", err, out)
	}
	_, lc := projectReposConfig(t)
	r := lc.Repos["acme/app"]
	if r.BaseBranch != "release" {
		t.Errorf("repos.acme/app.base_branch = %q, want release", r.BaseBranch)
	}
	if want := []string{"web", "docs"}; !slices.Equal(r.Workflows, want) {
		t.Errorf("repos.acme/app.workflows = %v, want %v", r.Workflows, want)
	}
	if r.Provider != "github" {
		t.Errorf("the write changed a member of the entry it was not asked about: %+v", r)
	}
}

// TestProjectReposSetIsRefusedInACodingAgentsSession: the SEC half. A job
// profile carries secrets and allow_repos — the lists a run's admission and its
// mounts are decided by — so the write is the owner's own and the session is
// refused it, with the file left exactly as it was.
func TestProjectReposSetIsRefusedInACodingAgentsSession(t *testing.T) {
	settingsWriteHomeWith(t, projectReposLocalConfig)
	t.Setenv(agentMarkers[0], "1")
	path := os.Getenv("FUGARO_CONFIG")
	before := readFileString(t, path)

	out, _, err := execute(t, "project", "repos", "set", "--job-profile", "gpu", "--provider", "github")
	if err == nil {
		t.Fatalf("a coding agent's session wrote the project's job profiles:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "coding agent") {
		t.Errorf("the refusal does not say the session is what is refused: %v\n%s", err, out)
	}
	if readFileString(t, path) != before {
		t.Errorf("a refused write left the file changed:\n%s", readFileString(t, path))
	}
}
