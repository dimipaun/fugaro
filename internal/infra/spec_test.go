package infra

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

var update = flag.Bool("update", false, "rewrite the golden tfvars under deploy/terraform/gcp/roots/repo/tests/testdata")

// goldenDir holds the repository root's tfvars fixtures, the Go side of the
// contract with the Terraform modules.
const goldenDir = "../../deploy/terraform/gcp/roots/repo/tests/testdata"

// m4LocalConfig is the local config the M4 job spec was captured with
// (testdata/README.md).
const m4LocalConfig = `version: 1
project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
repos:
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
`

// m5Additions are the local config fields an M5 installation adds.
const m5Additions = `registry_host: us-east5-docker.pkg.dev/proj-1234
base_image: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-0123abc
`

func parseLC(t *testing.T, yaml string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

func parseCfg(t *testing.T, data []byte) *config.Config {
	t.Helper()
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("fugaro.yaml: %v", problems)
	}
	return cfg
}

// installationOutputs are what the installation root would output for
// proj-1234 in us-east5.
func installationOutputs() InstallationOutputs {
	dry := true
	return InstallationOutputs{
		RunsBucket:              "fugaro-runs-proj-1234",
		RegistryHost:            "us-east5-docker.pkg.dev/proj-1234",
		BaseRegistry:            BaseRegistry,
		SchedulerServiceAccount: SchedulerServiceAccountID + "@proj-1234.iam.gserviceaccount.com",
		RoleIDs: RoleIDs{
			Launcher:       "projects/proj-1234/roles/" + RoleLauncher,
			JobRunner:      "projects/proj-1234/roles/" + RoleJobRunner,
			BuildSubmitter: "projects/proj-1234/roles/" + RoleBuildSubmitter,
		},
		Launchers:             []string{},
		Operators:             []string{},
		RegistryCleanupDryRun: &dry,
	}
}

// sandboxInputs is the M4 capture's fixture: the live-test sandbox's
// fugaro.yaml, as acme/sandbox on Bitbucket.
func sandboxInputs(t *testing.T, extraLC string) Inputs {
	t.Helper()
	data, err := os.ReadFile("../../deploy/sandbox/fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return Inputs{
		LC:           parseLC(t, m4LocalConfig+extraLC),
		Repo:         "acme/sandbox",
		Cfg:          parseCfg(t, data),
		RepoURL:      "https://bitbucket.org/acme/sandbox.git",
		Installation: installationOutputs(),
	}
}

const webappYAML = `version: 1
git: { provider: github, base_branch: main }
agent: { auth: vertex }
workflows:
  web:
    base: web-node
    commands: { build: npm run build, test: npm test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
    timeouts: { total: 60m }
  api:
    base: server-jvm
    commands: { build: ./gradlew assemble, test: ./gradlew test }
    rebuild: { check: "off" }
`

// webappInputs is a GitHub repository on Vertex, with two workflows (one
// without a daily check), launchers, and a price override.
func webappInputs(t *testing.T) Inputs {
	t.Helper()
	lc := parseLC(t, `version: 1
project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_image: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-0123abc
compute_prices: { us-east5: { vcpu_second_usd: 0.00002, gib_second_usd: 0.0000025 } }
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web, api], github_app_id: "123456" }
`)
	out := installationOutputs()
	out.Launchers = []string{"user:launcher@example.com"}
	out.Operators = []string{"user:operator@example.com"}
	return Inputs{LC: lc, Repo: "acme/webapp", Cfg: parseCfg(t, []byte(webappYAML)), Installation: out}
}

func readGolden(t *testing.T) (map[string]any, map[string]string) {
	t.Helper()
	data, err := os.ReadFile("testdata/m4-jobspec-sandbox.json")
	if err != nil {
		t.Fatal(err)
	}
	var js map[string]any
	if err := json.Unmarshal(data, &js); err != nil {
		t.Fatal(err)
	}
	tsv, err := os.ReadFile("testdata/m4-jobspec-sandbox.fields.tsv")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for line := range strings.Lines(string(tsv)) {
		k, v, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
		if !ok {
			t.Fatalf("bad row %q", line)
		}
		rows[k] = strings.TrimSuffix(v, ";") // the capture joined lines with ';'
	}
	return js, rows
}

// pairs parses "k=v" items separated by sep; suffix is cut from each value.
func pairs(t *testing.T, s, sep, suffix string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for item := range strings.SplitSeq(s, sep) {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			t.Fatalf("bad pair %q in %q", item, s)
		}
		m[k] = strings.TrimSuffix(v, suffix)
	}
	return m
}

func anyMap(m map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v.(string)
	}
	return out
}

// The names don't change: the spec gives the sandbox fixture exactly what
// the M4 binary's job-spec gave, but for the image, which moves to the
// repository's own registry.
func TestSpecMatchesM4JobSpec(t *testing.T) {
	in := sandboxInputs(t, "")
	ws, err := Workflow(in, "web")
	if err != nil {
		t.Fatal(err)
	}
	golden, rows := readGolden(t)
	str := func(k string) string { return golden[k].(string) }
	for _, c := range []struct{ field, got, want string }{
		{"project", in.LC.Project, str("project")},
		{"region", in.LC.Region, str("region")},
		{"slug", ws.Slug, str("slug")},
		{"job", ws.Job, str("job")},
		{"service_account_id", ws.ServiceAccount.AccountID, str("service_account_id")},
		{"service_account", ws.ServiceAccountEmail, str("service_account")},
		{"service_account_display_name (legacy)", gcp.LegacyJobSADisplayName(ws.Slug, ws.Name), str("service_account_display_name")},
		{"image (legacy)", ws.LegacyImage, str("image")},
		{"memory", ws.Memory, str("memory")},
		{"git_secret", ws.SecretIDs[ws.GitSecret], str("git_secret")},
		// The build account is the repository's own now; the M4 view
		// passes the deprecated setting through unchanged.
		{"build_service_account", in.LC.Build.ServiceAccount, str("build_service_account")},
		{"cpu", ws.CPU, "1"},
		{"task_timeout_s", strings.TrimSpace(string(mustJSON(t, ws.TaskTimeoutS))), string(mustJSON(t, golden["task_timeout_s"]))},
		// Byte for byte: a condition that differs in one byte is another binding.
		{"bucket-condition", ws.BucketCondition.Expression, rows["bucket-condition"]},
		{"condition title", ws.BucketCondition.Title, "fugaro-" + str("service_account_id")},
		{"sa-display-name (legacy)", gcp.LegacyJobSADisplayName(ws.Slug, ws.Name), rows["sa-display-name"]},
		{"labels", joinPairs(ws.Labels, ","), rows["labels"]},
		{"build-secret-ids", strings.Join(sortedIDs(ws.SecretIDs, ws.BuildSecrets), ";"), rows["build-secret-ids"]},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if golden["cpu"].(float64) != 1 {
		t.Errorf("golden cpu = %v", golden["cpu"])
	}
	if ws.ServiceAccount.DisplayName != gcp.JobSADisplayName(ws.Slug, "web") || ws.ServiceAccount.DisplayName == str("service_account_display_name") {
		t.Errorf("display name = %q; the new mark is JobSADisplayName, the M4 one only its legacy form", ws.ServiceAccount.DisplayName)
	}
	for name, c := range map[string]struct{ got, want map[string]string }{
		"env":          {ws.Env, anyMap(golden["env"].(map[string]any))},
		"env row":      {ws.Env, pairs(t, strings.TrimPrefix(rows["env"], "^;^"), ";", "")},
		"secrets":      {envSecretIDs(ws), anyMap(golden["secrets"].(map[string]any))},
		"secrets row":  {envSecretIDs(ws), pairs(t, rows["secrets"], ",", ":latest")},
		"secret-names": {ws.SecretIDs, pairs(t, rows["secret-names"], ";", "")},
		"labels":       {ws.Labels, anyMap(golden["labels"].(map[string]any))},
	} {
		if !maps.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
	}
	// The one deliberate change: the image lives in the repository's registry.
	want := gcp.ImageName("us-east5-docker.pkg.dev/proj-1234/"+gcp.RegistryRepoID(ws.Slug), ws.Slug, "web") + ":latest"
	if ws.Image != want || !ws.DeployJob {
		t.Errorf("image = %q (deploy %v), want %q", ws.Image, ws.DeployJob, want)
	}
	// Without registry_host the image's registry is still the installation's.
	in.LC.RegistryHost = ""
	in.Installation = InstallationOutputs{}
	if ws2, err := Workflow(in, "web"); err != nil || ws2.Image != want {
		t.Errorf("without registry_host: %q, %v", ws2.Image, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func joinPairs(m map[string]string, sep string) string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, k+"="+m[k])
	}
	return strings.Join(out, sep)
}

func sortedIDs(ids map[string]string, logical []string) []string {
	var out []string
	for _, l := range logical {
		out = append(out, ids[l])
	}
	slices.Sort(out)
	return out
}

func envSecretIDs(ws WorkflowSpec) map[string]string {
	out := map[string]string{}
	for env, logical := range ws.SecretEnv {
		out[env] = ws.SecretIDs[logical]
	}
	return out
}

var startsWithRE = regexp.MustCompile(`^resource\.name\.startsWith\("([^"\\]+)"\)$`)

func conditionMatches(t *testing.T, cond, name string) bool {
	t.Helper()
	for clause := range strings.SplitSeq(cond, " || ") {
		m := startsWithRE.FindStringSubmatch(clause)
		if m == nil {
			t.Fatalf("clause %q is not resource.name.startsWith(\"...\")", clause)
		}
		if strings.HasPrefix(name, m[1]) {
			return true
		}
	}
	return false
}

func TestBuildConditionCoversRecordPaths(t *testing.T) {
	rs, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	cond := rs.BuildBucketCondition.Expression
	obj := func(rest string) string { return "projects/_/buckets/fugaro-runs-proj-1234/objects/" + rest }
	for _, p := range []string{"builds/" + rs.Slug + "/web/image.json", "builds/" + rs.Slug + "/web/check.json"} {
		if !conditionMatches(t, cond, obj(p)) {
			t.Errorf("the build condition doesn't cover %s", p)
		}
	}
	for _, p := range []string{"builds/" + rs.Slug + "-x/web/image.json", "builds/" + rs.Slug, "runs/" + rs.Slug + "/r/result.json", "cache/" + rs.Slug + "/k"} {
		if conditionMatches(t, cond, obj(p)) {
			t.Errorf("the build condition covers %s", p)
		}
	}
	if rs.BuildBucketCondition.Title != gcp.BucketConditionTitle(rs.BuildServiceAccount.AccountID) {
		t.Errorf("title = %q", rs.BuildBucketCondition.Title)
	}
	if rs.BuildServiceAccount.AccountID != gcp.BuildServiceAccountID(rs.Slug) || rs.BuildServiceAccount.DisplayName != gcp.BuildSADisplayName(rs.Slug) {
		t.Errorf("build account = %+v", rs.BuildServiceAccount)
	}
}

func TestRepoSpecBitbucket(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	slug := rs.Slug
	if rs.Label != slug || rs.Registry.RepositoryID != gcp.RegistryRepoID(slug) || !rs.Registry.CleanupDryRun {
		t.Errorf("repo = %+v", rs)
	}
	if want := []string{"bitbucket-token", "sandbox-probe"}; !slices.Equal(rs.BuildSecrets, want) {
		t.Errorf("build_secrets = %v, want %v", rs.BuildSecrets, want)
	}
	for _, l := range []string{"bitbucket-token", "claude-oauth-token", "sandbox-probe"} {
		if rs.Secrets[l] != gcp.SecretID(slug, l) {
			t.Errorf("secrets[%s] = %q", l, rs.Secrets[l])
		}
	}
	c := rs.Check
	if c == nil {
		t.Fatal("no check")
	}
	if c.Job != gcp.CheckJobName(slug) || c.SchedulerJob != gcp.SchedulerJobName(slug) || c.SchedulerRegion != "us-east4" || !c.Paused ||
		c.Image != in.LC.BaseImage {
		t.Errorf("check = %+v", c)
	}
	if !regexp.MustCompile(`^([0-9]|[1-5][0-9]) [56] \* \* \*$`).MatchString(c.Schedule) {
		t.Errorf("schedule = %q, want a minute between 05:00 and 06:59", c.Schedule)
	}
	if !maps.Equal(c.SecretEnv, map[string]string{"FUGARO_BITBUCKET_TOKEN": "bitbucket-token"}) {
		t.Errorf("check secret_env = %v", c.SecretEnv)
	}
	var spec CheckJobSpec
	if err := json.Unmarshal([]byte(c.Env[CheckSpecEnv]), &spec); err != nil {
		t.Fatalf("%s: %v", CheckSpecEnv, err)
	}
	want := CheckJobSpec{
		Repo: "acme/sandbox", Provider: "bitbucket", RepoURL: "https://bitbucket.org/acme/sandbox.git", BaseBranch: "master",
		Workflows: []string{"web"}, Registry: "us-east5-docker.pkg.dev/proj-1234/" + gcp.RegistryRepoID(slug),
		BuildServiceAccount: gcp.BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com",
		MachineType:         "E2_HIGHCPU_8", BuildRegion: "us-east5", BaseImage: in.LC.BaseImage,
	}
	if !equalJSON(t, spec, want) {
		t.Errorf("check spec = %+v, want %+v", spec, want)
	}
	for _, k := range []string{"FUGARO_BACKEND", "FUGARO_BUCKET", "FUGARO_PROJECT", "FUGARO_REGION"} {
		if c.Env[k] != rs.Workflows["web"].Env[k] {
			t.Errorf("check env %s = %q", k, c.Env[k])
		}
	}
	if c.Env[runner.SecretEnvsVar] != "FUGARO_BITBUCKET_TOKEN" {
		t.Errorf("check %s = %q", runner.SecretEnvsVar, c.Env[runner.SecretEnvsVar])
	}
	if rs.GitHubAppID != "" || rs.GitUser != "x-token-auth" {
		t.Errorf("app id %q, git user %q", rs.GitHubAppID, rs.GitUser)
	}
	// A scheduler region in the local config wins.
	in = sandboxInputs(t, m5Additions+"scheduler_region: us-central1\n")
	if rs, err := Repo(in); err != nil || rs.Check.SchedulerRegion != "us-central1" {
		t.Errorf("scheduler_region: %+v, %v", rs.Check, err)
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	return bytes.Equal(mustJSON(t, a), mustJSON(t, b))
}

func TestSchedulerRegionUnknown(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	in.LC.Region = "mars-north1"
	in.LC.RegistryHost = ""
	in.Installation = InstallationOutputs{}
	_, err := Repo(in)
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--scheduler-region") {
		t.Fatalf("err = %v", err)
	}
}

func TestGitHubSpec(t *testing.T) {
	in := webappInputs(t)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	web := rs.Workflows["web"]
	if web.SecretEnv["FUGARO_GITHUB_APP_PRIVATE_KEY"] != "github-app-key" || rs.Secrets["github-app-key"] != gcp.SecretID(rs.Slug, "github-app-key") {
		t.Errorf("web secret_env = %v, secrets = %v", web.SecretEnv, rs.Secrets)
	}
	if web.Env["FUGARO_GITHUB_APP_ID"] != "123456" || rs.GitHubAppID != "123456" || rs.Check.Env["FUGARO_GITHUB_APP_ID"] != "123456" {
		t.Errorf("app id: env %q, spec %q", web.Env["FUGARO_GITHUB_APP_ID"], rs.GitHubAppID)
	}
	if rs.GitUser != "x-access-token" || rs.RepoURL != "https://github.com/acme/webapp.git" {
		t.Errorf("git user %q, url %q", rs.GitUser, rs.RepoURL)
	}
	if _, ok := rs.Secrets["bitbucket-token"]; ok {
		t.Error("a GitHub repository mounts bitbucket-token")
	}
	if !slices.Contains(rs.BuildSecrets, "github-app-key") || !slices.Contains(rs.BuildSecrets, "npm-token") {
		t.Errorf("build_secrets = %v", rs.BuildSecrets)
	}
	if !maps.Equal(rs.Check.SecretEnv, map[string]string{"FUGARO_GITHUB_APP_PRIVATE_KEY": "github-app-key"}) {
		t.Errorf("check secret_env = %v", rs.Check.SecretEnv)
	}
	var spec CheckJobSpec
	if err := json.Unmarshal([]byte(rs.Check.Env[CheckSpecEnv]), &spec); err != nil || !slices.Equal(spec.Workflows, []string{"web"}) {
		t.Errorf("check spec workflows = %v, %v (api has check: off)", spec.Workflows, err)
	}
	// The price override reaches the jobs, in the form the runner reads.
	p, ok, err := runner.PricesFromEnv(func(k string) string { return web.Env[k] })
	if err != nil || !ok || p.VCPUSecondUSD != 0.00002 || p.GiBSecondUSD != 0.0000025 {
		t.Errorf("FUGARO_COMPUTE_PRICES = %q: %+v %v %v", web.Env[ComputePricesEnv], p, ok, err)
	}

	// The App ID is required, from the flag or the local config.
	in.LC.Repos["acme/webapp"] = localcfg.Repo{Provider: "github", Workflows: []string{"web", "api"}}
	_, err = Repo(in)
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--github-app-id") {
		t.Fatalf("no App ID: err = %v", err)
	}
	in.GitHubAppID = "42"
	if rs, err := Repo(in); err != nil || rs.GitHubAppID != "42" {
		t.Fatalf("--github-app-id: %v", err)
	}
	in.GitHubAppID = "4x2"
	if _, err := Repo(in); !errors.As(err, &ue) {
		t.Fatalf("bad App ID: err = %v", err)
	}
}

func TestVertexSpec(t *testing.T) {
	rs, err := Repo(webappInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "api"} {
		ws := rs.Workflows[name]
		if !ws.Vertex || ws.Env["CLOUD_ML_REGION"] != "us-east5" || ws.Env["ANTHROPIC_VERTEX_PROJECT_ID"] != "proj-1234" {
			t.Errorf("%s: vertex %v, env %v", name, ws.Vertex, ws.Env)
		}
		for env := range ws.SecretEnv {
			if env == "CLAUDE_CODE_OAUTH_TOKEN" || env == "ANTHROPIC_API_KEY" {
				t.Errorf("%s mounts %s on Vertex", name, env)
			}
		}
	}
	bb, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil || bb.Workflows["web"].Vertex {
		t.Errorf("oauth workflow has vertex: %v", err)
	}
}

func TestNoCheckWhenAllOff(t *testing.T) {
	in := webappInputs(t)
	w := in.Cfg.Workflows["web"]
	w.Rebuild.Check = "off"
	in.Cfg.Workflows["web"] = w
	rs, err := Repo(in)
	if err != nil || rs.Check != nil {
		t.Fatalf("check = %+v, %v", rs.Check, err)
	}
	vars, err := RepoVars(rs)
	if err != nil || !bytes.Contains(vars, []byte(`"check": null`)) {
		t.Fatalf("tfvars: %v\n%s", err, vars)
	}
	// And without a base image nothing needs one.
	in.LC.BaseImage = ""
	if _, err := Repo(in); err != nil {
		t.Fatal(err)
	}
}

func TestSpecRefuses(t *testing.T) {
	var ue *UserError
	for name, mutate := range map[string]func(*Inputs){
		"registry_host of another project": func(in *Inputs) { in.Installation.RegistryHost = "us-east5-docker.pkg.dev/other-proj" },
		"runs bucket differs":              func(in *Inputs) { in.Installation.RunsBucket = "fugaro-runs-other" },
		"provider disagrees":               func(in *Inputs) { in.LC.Repos["acme/sandbox"] = localcfg.Repo{Provider: "github"} },
		"url on another host":              func(in *Inputs) { in.RepoURL = "https://example.com/acme/sandbox.git" },
		"url with credentials":             func(in *Inputs) { in.RepoURL = "https://x:y@bitbucket.org/acme/sandbox.git" },
		"url of another repository":        func(in *Inputs) { in.RepoURL = "https://bitbucket.org/other/repo.git" },
		"role of another project": func(in *Inputs) {
			in.Installation.RoleIDs.JobRunner = "projects/other-proj/roles/" + RoleJobRunner
		},
		"role that isn't a custom role": func(in *Inputs) { in.Installation.RoleIDs.Launcher = "roles/owner" },
		"no base image":                 func(in *Inputs) { in.LC.BaseImage = "" },
		"secret env collides": func(in *Inputs) {
			w := in.Cfg.Workflows["web"]
			w.Secrets = []config.Secret{{Name: "x", Env: "FUGARO_BUCKET"}}
			in.Cfg.Workflows["web"] = w
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := sandboxInputs(t, m5Additions)
			mutate(&in)
			if _, err := Repo(in); !errors.As(err, &ue) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	in := sandboxInputs(t, m5Additions)
	for _, u := range []string{"https://bitbucket.org/acme/sandbox.git", "https://bitbucket.org/Acme/Sandbox", "https://bitbucket.org/acme/sandbox/"} {
		in.RepoURL = u
		if _, err := Repo(in); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	if _, err := Workflow(in, "nope"); !errors.As(err, &ue) {
		t.Fatalf("unknown workflow: err = %v", err)
	}
}

// No secret value may reach the tfvars, and so Terraform's state.
func TestTfvarsHoldNoValues(t *testing.T) {
	const value = "s3cr3t-VALUE-that-must-not-leak-4f9a"
	for _, env := range []string{"SANDBOX_PROBE", "CLAUDE_CODE_OAUTH_TOKEN", "FUGARO_BITBUCKET_TOKEN", "NPM_TOKEN", "FUGARO_GITHUB_APP_PRIVATE_KEY"} {
		t.Setenv(env, value)
	}
	sm := gcpfake.NewSecrets(t)
	for _, in := range []Inputs{sandboxInputs(t, m5Additions), webappInputs(t)} {
		slug, err := task.Slug(in.Cfg.Git.Provider, in.Repo)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range []string{"bitbucket-token", "claude-oauth-token", "sandbox-probe", "npm-token", "github-app-key"} {
			sm.Seed(gcp.SecretID(slug, l), map[string]string{gcp.LabelManaged: gcp.ManagedValue}, []byte(value))
		}
		rs, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		vars, err := RepoVars(rs)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(vars, []byte(value)) {
			t.Fatalf("the tfvars hold the secret value:\n%s", vars)
		}
		var doc any
		if err := json.Unmarshal(vars, &doc); err != nil {
			t.Fatal(err)
		}
		walkKeys(doc, func(k string) {
			if k == "value" || k == "secret_data" || k == "payload" {
				t.Errorf("the tfvars have a key %q", k)
			}
		})
	}
}

func walkKeys(v any, f func(string)) {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			f(k)
			walkKeys(x, f)
		}
	case []any:
		for _, x := range v {
			walkKeys(x, f)
		}
	}
}

// The golden tfvars are the Go side of the contract with the repository
// root's Terraform tests. Regenerate them with -update.
func TestGoldenTfvars(t *testing.T) {
	for name, in := range map[string]Inputs{"bitbucket-oauth": sandboxInputs(t, m5Additions), "github-vertex": webappInputs(t)} {
		rs, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := RepoVars(rs)
		if err != nil {
			t.Fatal(err)
		}
		again, err := RepoVars(rs)
		if err != nil || !bytes.Equal(got, again) {
			t.Fatalf("%s: RepoVars is not deterministic", name)
		}
		path := filepath.Join(goldenDir, name+".tfvars.json")
		if *update {
			if err := os.MkdirAll(goldenDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (regenerate with go test ./internal/infra -run TestGoldenTfvars -update)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the generated tfvars; regenerate with -update and review the diff\ngot:\n%s", path, got)
		}
	}
}

// Keys are sorted at every level, so a diff of two tfvars shows only what
// changed.
func TestTfvarsSortedKeys(t *testing.T) {
	rs, err := Repo(webappInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	vars, err := RepoVars(rs)
	if err != nil {
		t.Fatal(err)
	}
	// Re-encoding through maps, which encoding/json writes sorted, changes nothing.
	var doc any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), vars) {
		t.Errorf("the tfvars are not in sorted-key form:\n%s", vars)
	}
	top := []string{"github_app_id", "installation", "project", "region", "repo"}
	if got := slices.Sorted(maps.Keys(doc.(map[string]any))); !slices.Equal(got, top) {
		t.Errorf("top-level keys = %v, want %v", got, top)
	}
}

func TestInstallationSpec(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions+"terraform: { launchers: [\"user:a@example.com\"], alert_email: ops@example.com }\n")
	s, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	n := s.Names
	if n.SchedulerServiceAccountID != "fugaro-scheduler" || n.BaseRegistry != "fugaro-base" || n.LegacyRegistry != "fugaro" ||
		n.RoleIDs != (InstallationRoleIDs{Launcher: "fugaroLauncher", JobRunner: "fugaroJobRunner", BuildSubmitter: "fugaroBuildSubmitter"}) ||
		n.Log != (LogNames{Bucket: "fugaro", View: "fugaro-runs", Sink: "fugaro-jobs", Exclusion: "fugaro-jobs-from-default"}) {
		t.Errorf("names = %+v", n)
	}
	if s.StateBucket != "fugaro-tfstate-proj-1234" || s.RunsBucket != "fugaro-runs-proj-1234" || !s.RegistryCleanup.Enabled || !s.RegistryCleanup.DryRun ||
		!s.ManageAPIs || s.AlertEmail == nil || *s.AlertEmail != "ops@example.com" || !slices.Equal(s.Launchers, []string{"user:a@example.com"}) || s.Operators == nil {
		t.Errorf("spec = %+v", s)
	}
	vars, err := InstallationVars(s)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	// Exactly the installation root's variables.
	want := []string{"adopt_legacy_registry", "alert_email", "bucket_lifecycle", "budget", "enable_vertex", "launchers", "log_bucket_description", "manage_apis",
		"names", "operators", "project", "region", "registry_cleanup", "runs_bucket", "state_bucket"}
	if got := slices.Sorted(maps.Keys(doc)); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	// The log bucket's description is its ownership mark, since it can
	// carry no labels.
	if doc["log_bucket_description"] != LogBucketDescription || !strings.Contains(LogBucketDescription, "fugaro") {
		t.Errorf("log_bucket_description = %v", doc["log_bucket_description"])
	}
	if doc["budget"] != nil {
		t.Errorf("budget = %v", doc["budget"])
	}

	s, err = Installation(lc, InstallOptions{RegistryCleanup: "off", StateBucket: "my-state", Budget: &Budget{BillingAccount: "0123AB-4567CD-89EF01", Amount: 70, CurrencyCode: "CAD"}})
	if err != nil || s.RegistryCleanup.Enabled || s.StateBucket != "my-state" || s.Budget == nil {
		t.Errorf("options: %+v, %v", s, err)
	}
	var ue *UserError
	if _, err := Installation(lc, InstallOptions{RegistryCleanup: "sometimes"}); !errors.As(err, &ue) {
		t.Errorf("bad registry cleanup: %v", err)
	}
	bad := parseLC(t, strings.Replace(m4LocalConfig, "runs_bucket: fugaro-runs-proj-1234", "runs_bucket: proj-1234-runs", 1))
	if _, err := Installation(bad, InstallOptions{}); !errors.As(err, &ue) {
		t.Errorf("runs bucket without fugaro-runs-: %v", err)
	}
}

// The repository tfvars' installation block falls back to the names Go
// gives the installation when its outputs aren't known yet.
func TestDefaultInstallationOutputs(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	full, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Installation = InstallationOutputs{}
	def, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, full.Installation, def.Installation) {
		t.Errorf("defaults %+v, outputs %+v", def.Installation, full.Installation)
	}
}

// The build account can read only the installation's base registry, so a
// base image elsewhere (an M4 local config's, in the legacy registry)
// would fail every build at its pull.
func TestBaseImageWarning(t *testing.T) {
	outs := InstallationOutputs{RegistryHost: "us-east5-docker.pkg.dev/proj-1234", BaseRegistry: BaseRegistry}
	for base, warn := range map[string]bool{
		"": false,
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-0123abc":  false,
		"us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro-web-node:dev-0123abc":       true,
		"us-east5-docker.pkg.dev/proj-1234/fugaro-basex/fugaro-web-node:dev-0123abc": true,
		"us-east5-docker.pkg.dev/other-proj/fugaro-base/fugaro-web-node:dev-0123abc": true,
	} {
		got := BaseImageWarning(base, outs)
		if (got != "") != warn {
			t.Errorf("%q: warning %q, want one: %v", base, got, warn)
		}
		if warn && (!strings.Contains(got, base) || !strings.Contains(got, outs.RegistryHost+"/"+BaseRegistry)) {
			t.Errorf("%q: the warning %q doesn't name the image and the base registry", base, got)
		}
	}
}

// The Vertex AI API is enabled when a repository the local config records
// authenticates its agent through Vertex (init --repo records it), and
// only then: the installation root can't read the workflows itself.
func TestInstallationEnablesVertexFromRepos(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	s, err := Installation(lc, InstallOptions{})
	if err != nil || s.EnableVertex {
		t.Fatalf("no vertex repository: enable_vertex %v, %v", s.EnableVertex, err)
	}
	lc = parseLC(t, m4LocalConfig+"  acme/webapp: { provider: github, github_app_id: \"123456\", vertex: true, workflows: [web] }\n"+m5Additions)
	s, err = Installation(lc, InstallOptions{})
	if err != nil || !s.EnableVertex {
		t.Fatalf("a vertex repository: enable_vertex %v, %v", s.EnableVertex, err)
	}
	vars, err := InstallationVars(s)
	if err != nil || !strings.Contains(string(vars), `"enable_vertex": true`) {
		t.Fatalf("tfvars:\n%s, %v", vars, err)
	}
}

// A repository's spec says whether any of its workflows uses Vertex, which
// init --repo records in the local config.
func TestRepoUsesVertex(t *testing.T) {
	sandbox, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	webapp, err := Repo(webappInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.UsesVertex() || !webapp.UsesVertex() {
		t.Fatalf("uses vertex: sandbox %v, webapp %v", sandbox.UsesVertex(), webapp.UsesVertex())
	}
}

// The runs bucket the specs name is the one image builds record in
// (localcfg's RunsBucketName), so the first build fugaro init submits,
// readiness and the daily check all agree; with none, it is an error.
func TestBucketNameIsRunsBucketName(t *testing.T) {
	for _, c := range []struct{ runs, bucketURL string }{
		{"fugaro-runs-x", ""},
		{"fugaro-runs-x", "gs://other-bucket"},
		{"fugaro-runs-x", "file:///tmp/runs"},
		{"", "gs://fugaro-runs-y"},
		{"", "file:///tmp/runs"},
		{"", ""},
	} {
		lc := &localcfg.Config{RunsBucket: c.runs, Bucket: c.bucketURL}
		got, err := bucketName(lc)
		want := lc.RunsBucketName()
		if want == "" {
			if err == nil {
				t.Errorf("runs_bucket %q, bucket_url %q: %q, want an error", c.runs, c.bucketURL, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("runs_bucket %q, bucket_url %q: %q, %v; want %q", c.runs, c.bucketURL, got, err, want)
		}
		if url := lc.RecordBucketURL(); url != "gs://"+got {
			t.Errorf("runs_bucket %q, bucket_url %q: records in %q, but the spec's bucket is %q", c.runs, c.bucketURL, url, got)
		}
	}
}
