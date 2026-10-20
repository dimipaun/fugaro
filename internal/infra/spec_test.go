package infra

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
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

// baseTestDigest is any well-formed sha256 digest: BaseImageWarnings reads
// the reference's registry and path, never its digest.
const baseTestDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// m4LocalConfig is the local config the M4 job spec was captured with
// (testdata/README.md).
const m4LocalConfig = `version: 1
name: aurora
gcp_project: proj-1234
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
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
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
		ProjectName:             "aurora",
		SchedulerServiceAccount: SchedulerServiceAccountID + "@proj-1234.iam.gserviceaccount.com",
		RoleIDs: RoleIDs{
			Launcher:       "projects/proj-1234/roles/" + RoleLauncher,
			JobRunner:      "projects/proj-1234/roles/" + RoleJobRunner,
			BuildSubmitter: "projects/proj-1234/roles/" + RoleBuildSubmitter,
			TagMover:       "projects/proj-1234/roles/" + RoleTagMover,
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
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: vertex }
workflows:
  web:
    commands: { build: npm run build, test: npm test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
    timeouts: { total: 60m }
  api:
    commands: { build: ./gradlew assemble, test: ./gradlew test }
    rebuild: { check: "off" }
`

// webappInputs is a GitHub repository on Vertex, with two workflows (one
// without a daily check), launchers, and a price override.
func webappInputs(t *testing.T) Inputs {
	t.Helper()
	lc := parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
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
		{"project", in.LC.GCPProject, str("project")},
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
		c.Image != in.LC.BaseImage("base") {
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
		MachineType:         "E2_HIGHCPU_8", BuildRegion: "us-east5", BaseImages: map[string]string{"base": in.LC.BaseImage("base")},
	}
	if !equalJSON(t, spec, want) {
		t.Errorf("check spec = %+v, want %+v", spec, want)
	}
	for _, k := range []string{"FUGARO_BACKEND", "FUGARO_BUCKET", "FUGARO_GCP_PROJECT", "FUGARO_PROJECT", "FUGARO_REGION"} {
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
	in.LC.BaseImages = nil
	if _, err := Repo(in); err != nil {
		t.Fatal(err)
	}
}

const cloneYAML = `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: vertex }
workflows:
  web:
    checkout: clone
    commands: { build: npm run build, test: npm test }
  api:
    commands: { build: ./gradlew assemble, test: ./gradlew test }
`

// cloneInputs is a GitHub repository with one checkout: clone workflow
// (web) and one ordinarily-built workflow (api, checked daily).
func cloneInputs(t *testing.T) Inputs {
	t.Helper()
	lc := parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web, api], github_app_id: "123456" }
`)
	return Inputs{LC: lc, Repo: "acme/webapp", Cfg: parseCfg(t, []byte(cloneYAML)), Installation: installationOutputs()}
}

// TestSpecCloneWorkflowRunsTheBase (design generic-tool.md §2, G3): a
// checkout: clone workflow's job image is the local config's
// base_images.base, not a derived image in the repository's own registry,
// and the daily check never lists it, though it still checks a baked
// sibling.
//
// Mutations (run, restore): (1) in workflow(), remove the `if w.Checkout ==
// config.CheckoutClone` branch so Image is always the derived
// ":latest" tag — the image assertion below fails (it would equal
// ".../acme-webapp-<hash>/web:latest", not the base_images.base ref). (2) in
// Repo()'s checked loop, remove the `Checkout != config.CheckoutClone &&`
// clause — web then joins the check job's workflows and the "check covers
// web" assertion fails.
func TestSpecCloneWorkflowRunsTheBase(t *testing.T) {
	in := cloneInputs(t)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	want := in.LC.BaseImage(config.BaseKind)
	if want == "" {
		t.Fatal("fixture bug: no base_images.base")
	}
	if got := rs.Workflows["web"].Image; got != want {
		t.Errorf("web's image = %q, want base_images.base %q", got, want)
	}
	if rs.Workflows["web"].Checkout != config.CheckoutClone {
		t.Errorf("web's Checkout = %q, want %q", rs.Workflows["web"].Checkout, config.CheckoutClone)
	}
	if rs.Check == nil {
		t.Fatal("no check job, want one covering api")
	}
	if slices.Contains(rs.Check.Workflows, "web") {
		t.Errorf("check covers web, a checkout: clone workflow: %v", rs.Check.Workflows)
	}
	if !slices.Contains(rs.Check.Workflows, "api") {
		t.Errorf("check does not cover api: %v", rs.Check.Workflows)
	}
}

// TestSpecCloneVariantsRunTheBase: with a named Claude account allowed for
// the repository, an oauth checkout: clone workflow gets one job per account
// (gcp.WorkflowVariant), and every one of them runs base_images.base with
// FUGARO_CHECKOUT=clone, never a derived :latest tag; the daily check still
// never lists the clone workflow.
func TestSpecCloneVariantsRunTheBase(t *testing.T) {
	in := cloneInputs(t)
	in.Cfg.Agent.Auth = "oauth"
	in.LC = parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
claude_accounts:
  acme-personal:
    allow_data_to: ["acme/webapp"]
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web, api], github_app_id: "123456" }
`)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	variant := gcp.WorkflowVariant("web", "acme-personal")
	want := in.LC.BaseImage(config.BaseKind)
	for _, key := range []string{"web", variant} {
		ws, ok := rs.Workflows[key]
		if !ok {
			t.Fatalf("no job spec %q: %v", key, slices.Sorted(maps.Keys(rs.Workflows)))
		}
		if ws.Image != want || ws.Checkout != config.CheckoutClone || ws.Env[CheckoutEnv] != config.CheckoutClone {
			t.Errorf("%s: image %q, checkout %q, %s=%q; want %q, clone, clone", key, ws.Image, ws.Checkout, CheckoutEnv, ws.Env[CheckoutEnv], want)
		}
	}
	if rs.Check == nil || slices.Contains(rs.Check.Workflows, "web") || slices.Contains(rs.Check.Workflows, variant) {
		t.Errorf("check job workflows = %v, want api only", rs.Check)
	}
}

// TestSpecCloneSetsCheckoutEnv (review finding 2, MED; plan Task 17 says to
// add this to Task 16's spec change if it is not there): a checkout: clone
// workflow's job gets FUGARO_CHECKOUT=clone, which the runner's bootstrap
// (plan Task 17, not yet merged) will read to decide to clone blobless
// instead of moving a baked-in checkout; a baked workflow's spec carries no
// such key at all, so every baked workflow's env stays exactly what it was
// before checkout: clone existed (golden tfvars unchanged, TestRepoRootVars
// MatchModule's own sandbox fixture has none).
//
// Mutation (run, restore): delete the `if w.Checkout == config.CheckoutClone
// { ws.Env[CheckoutEnv] = config.CheckoutClone }` block in workflow() — this
// test then fails: web's env lacks FUGARO_CHECKOUT.
func TestSpecCloneSetsCheckoutEnv(t *testing.T) {
	rs, err := Repo(cloneInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Workflows["web"].Env[CheckoutEnv]; got != config.CheckoutClone {
		t.Errorf("web's %s = %q, want %q", CheckoutEnv, got, config.CheckoutClone)
	}
	if _, ok := rs.Workflows["api"].Env[CheckoutEnv]; ok {
		t.Errorf("api (baked) carries %s = %q, want no such key", CheckoutEnv, rs.Workflows["api"].Env[CheckoutEnv])
	}
}

// TestSpecCloneSetsRemoteEnv (review C1): a clone job runs the installation's
// base image, which holds no checkout, and nothing on Cloud Run names a
// repository for it: FUGARO_CHECKOUT=clone alone would leave the runner with
// nothing to clone. The job carries the repository's own credential-free
// clone URL in FUGARO_REMOTE (runner.RemoteEnv), which fugaro exec's --remote
// defaults to — repoURL is what checks that URL is https, carries no
// credentials and is this repository's — and a baked workflow's job carries
// no such key, so nothing baked changes.
//
// Mutation (run, restore): delete the `ws.Env[RemoteEnv] = c.repoURL` line in
// workflow() — this test then fails: web's env has no FUGARO_REMOTE, and the
// end-to-end test in internal/cli (TestExecCloneJobClonesTheRepositoryItWas
// DeployedFor) fails with "this job has no repository to clone".
func TestSpecCloneSetsRemoteEnv(t *testing.T) {
	rs, err := Repo(cloneInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Workflows["web"].Env[RemoteEnv]; got != "https://github.com/acme/webapp.git" {
		t.Errorf("web's %s = %q, want the repository's clone URL", RemoteEnv, got)
	}
	if _, ok := rs.Workflows["api"].Env[RemoteEnv]; ok {
		t.Errorf("api (baked) carries %s = %q, want no such key", RemoteEnv, rs.Workflows["api"].Env[RemoteEnv])
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
		// The path is the one git sends, escapes and all: a URL whose path
		// decodes to this repository but is not spelled as this repository is
		// a URL the run's own admission refuses, so accepting it here deploys
		// a job that refuses every run (the review finding on this change).
		"url with an escaped path":  func(in *Inputs) { in.RepoURL = "https://bitbucket.org/acme/sandb%6fx.git" },
		"url with an escaped owner": func(in *Inputs) { in.RepoURL = "https://bitbucket.org/acm%65/sandbox.git" },
		// The path's punctuation is one leading and one trailing "/", the same
		// trim the run's admission makes (gitprov.TrimRepoPath): a second slash
		// is an empty segment, which the admission's canonicalization refuses —
		// so accepting it here would write the URL as the job's remote and
		// every run of that job would stop before any clone (the review finding
		// on this change).
		"url with an empty leading segment": func(in *Inputs) { in.RepoURL = "https://bitbucket.org//acme/sandbox.git" },
		"url with a doubled trailing slash": func(in *Inputs) { in.RepoURL = "https://bitbucket.org/acme/sandbox.git//" },
		"url with an empty inner segment":   func(in *Inputs) { in.RepoURL = "https://bitbucket.org/acme//sandbox.git" },
		"role of another project": func(in *Inputs) {
			in.Installation.RoleIDs.JobRunner = "projects/other-proj/roles/" + RoleJobRunner
		},
		"role that isn't a custom role": func(in *Inputs) { in.Installation.RoleIDs.Launcher = "roles/owner" },
		"tag mover role of another project": func(in *Inputs) {
			in.Installation.RoleIDs.TagMover = "projects/other-proj/roles/" + RoleTagMover
		},
		"no base image":                         func(in *Inputs) { in.LC.BaseImages = nil },
		"no base image for the workflow's kind": func(in *Inputs) { in.LC.BaseImages = map[string]string{"go": "ghcr.io/dimipaun/fugaro-go:1"} },
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
	top := []string{"fugaro_project", "github_app_id", "installation", "project", "region", "repo"}
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
	if n.SchedulerServiceAccountID != "fugaro-scheduler" || n.BaseRegistry != "fugaro-base" || n.GhcrRegistry != "fugaro-ghcr" || n.LegacyRegistry != "fugaro" ||
		n.RoleIDs != (InstallationRoleIDs{Launcher: "fugaroLauncher", JobRunner: "fugaroJobRunner", BuildSubmitter: "fugaroBuildSubmitter", TagMover: "fugaroTagMover"}) ||
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
	want := []string{"adopt_legacy_registry", "alert_email", "bucket_lifecycle", "budget", "enable_vertex", "fugaro_project", "launcher_bucket_condition", "launchers", "log_bucket_description", "manage_apis",
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

// TestInstallationRefusesAnInvalidBucketURLCharset (code review finding):
// localcfg only charset-checks runs_bucket, not bucket_url, so a local
// config that names its runs bucket through bucket_url (not runs_bucket)
// can carry a bucket name gcp.LauncherBucketCondition refuses to widen on;
// that function panics by design rather than accept it.
// Installation must turn that into a clean UserError before ever calling
// it, not let the panic through to fugaro init.
func TestInstallationRefusesAnInvalidBucketURLCharset(t *testing.T) {
	lc := parseLC(t, strings.Replace(m4LocalConfig, "runs_bucket: fugaro-runs-proj-1234", "bucket_url: gs://fugaro-runs-Proj1234", 1))
	if lc.RunsBucketName() != "fugaro-runs-Proj1234" {
		t.Fatalf("test setup: RunsBucketName() = %q, want the bucket_url's host (uppercase, invalid)", lc.RunsBucketName())
	}
	var ue *UserError
	if _, err := Installation(lc, InstallOptions{}); !errors.As(err, &ue) {
		t.Fatalf("bucket_url with a byte outside the charset gcp.LauncherBucketCondition accepts: %v", err)
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

// The build account can read only the installation's two image
// repositories — the base registry (the copy an earlier release made) and
// the remote (pull-through) repository over ghcr.io (this release's pin) —
// so a base image elsewhere (an M4 local config's, in the legacy registry,
// or ghcr.io itself, whose upstream the remote repository is) would fail
// every build at its pull.
func TestBaseImageWarnings(t *testing.T) {
	outs := InstallationOutputs{RegistryHost: "us-east5-docker.pkg.dev/proj-1234", BaseRegistry: BaseRegistry, GhcrRegistry: GhcrRegistry}
	if ws := BaseImageWarnings(nil, outs); len(ws) != 0 {
		t.Errorf("no base images: %v", ws)
	}
	pin := "us-east5-docker.pkg.dev/proj-1234/" + GhcrRegistry + "/dimipaun/fugaro-base:0.7.5@" + baseTestDigest
	for base, warn := range map[string]bool{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc": false,
		pin: false,
		"us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro-base:dev-0123abc":                            true,
		"us-east5-docker.pkg.dev/proj-1234/fugaro-basex/fugaro-base:dev-0123abc":                      true,
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcrx/dimipaun/fugaro-base:0.7.5@" + baseTestDigest: true,
		"us-east5-docker.pkg.dev/other-proj/fugaro-base/fugaro-base:dev-0123abc":                      true,
		// The remote repository's upstream is not the installation's own
		// repository: a pull there is not one the build accounts can make.
		"ghcr.io/dimipaun/fugaro-base:0.7.5": true,
	} {
		got := BaseImageWarnings(map[string]string{"base": base}, outs)
		if (len(got) != 0) != warn {
			t.Errorf("%q: warnings %q, want one: %v", base, got, warn)
		}
		if warn && (len(got) != 1 || !strings.Contains(got[0], base) || !strings.Contains(got[0], "base_images.base") || !strings.Contains(got[0], outs.RegistryHost+"/"+BaseRegistry)) {
			t.Errorf("%q: the warnings %q don't name the image, its kind and the base registry", base, got)
		}
	}
	// One warning per offending kind, in kind order: BaseImageWarnings sorts
	// by map key, whatever strings the caller passes it (only Parse itself
	// restricts a real base_images key to config.Bases).
	got := BaseImageWarnings(map[string]string{
		"base":   "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1",
		"kind-a": "us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro-base:1",
		"kind-z": "ghcr.io/dimipaun/fugaro-base:1",
	}, outs)
	// Both repositories named in the warning: the operator reading it needs
	// to see where a base image may go, not only where it may not.
	if len(got) != 2 || !strings.Contains(got[0], outs.RegistryHost+"/"+BaseRegistry) || !strings.Contains(got[0], outs.RegistryHost+"/"+GhcrRegistry) {
		t.Errorf("the warnings %q don't name both repositories", got)
	}
	if len(got) != 2 || !strings.Contains(got[0], "base_images.kind-a") || !strings.Contains(got[1], "base_images.kind-z") {
		t.Errorf("warnings = %q", got)
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

// budgetInputs is the webapp with the budget block (and prices) appended
// to its project config.
func budgetInputs(t *testing.T, block string) Inputs {
	t.Helper()
	in := webappInputs(t)
	lc, err := in.LC.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	in.LC = parseLC(t, string(lc)+block)
	return in
}

func onlyBudgetEnv(env map[string]string) map[string]string {
	got := map[string]string{}
	for _, k := range []string{BudgetModeEnv, MaxRunUSDEnv, ModelPricesEnv, MaxRunTokensEnv, AllowedModelsEnv} {
		if v, ok := env[k]; ok {
			got[k] = v
		}
	}
	return got
}

// With the budget off the job gets no mode, but still the owner's cap and
// price overrides: they are the ceiling a repository's committed policy is
// clamped to (M9a.1; M9a dropped them when the mode was off).
func TestWorkflowEnvBudgetOff(t *testing.T) {
	rs, err := Repo(budgetInputs(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rs.Workflows {
		if got := onlyBudgetEnv(w.Env); len(got) != 0 {
			t.Errorf("%s has %v with no budget block", w.Name, got)
		}
	}
	for name, block := range map[string]string{
		"mode off":   "budget: { mode: off, per_run_usd: 5 }\nmodel_prices: { claude-sonnet-5-5: { input_per_m: 3, output_per_m: 15 } }\n",
		"empty mode": "budget: { per_run_usd: 5 }\nmodel_prices: { claude-sonnet-5-5: { input_per_m: 3, output_per_m: 15 } }\n",
	} {
		t.Run(name, func(t *testing.T) {
			rs, err := Repo(budgetInputs(t, block))
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range rs.Workflows {
				got := onlyBudgetEnv(w.Env)
				if _, ok := got[BudgetModeEnv]; ok || got[MaxRunUSDEnv] != "5" || got[ModelPricesEnv] == "" || len(got) != 2 {
					t.Errorf("%s env = %v: want the cap and prices, no mode", w.Name, got)
				}
				if s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok }); err != nil || s.On() || s.Cap != 5_000_000 || s.Prices == nil {
					t.Errorf("%s: SpendFromEnv = %+v, %v", w.Name, s, err)
				}
			}
		})
	}
}

func TestWorkflowEnvBudgetEnforce(t *testing.T) {
	rs, err := Repo(budgetInputs(t, `budget: { mode: enforce, per_run_usd: 12.5 }
model_prices:
  claude-sonnet-5-5: { input_per_m: 30, output_per_m: 150 }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Workflows) == 0 {
		t.Fatal("no workflows")
	}
	for _, w := range rs.Workflows {
		got := onlyBudgetEnv(w.Env)
		if got[BudgetModeEnv] != "enforce" || got[MaxRunUSDEnv] != "12.5" || got[ModelPricesEnv] == "" {
			t.Errorf("%s env = %v", w.Name, got)
		}
		// The runner reads exactly what init wrote.
		s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok })
		if err != nil || s.Mode != "enforce" || s.Cap != 12_500_000 {
			t.Fatalf("%s: SpendFromEnv = %+v, %v", w.Name, s, err)
		}
		if m, _ := s.Prices.Lookup("claude-sonnet-5-5"); m.Rates.InputPerM != 30 || m.Rates.CacheWrite5m != 1.25 {
			t.Errorf("%s: prices = %+v", w.Name, m.Rates)
		}
	}
}

func TestWorkflowEnvBudgetObserveNoCap(t *testing.T) {
	rs, err := Repo(budgetInputs(t, "budget: { mode: observe }\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rs.Workflows {
		got := onlyBudgetEnv(w.Env)
		if len(got) != 1 || got[BudgetModeEnv] != "observe" {
			t.Errorf("%s env = %v: want only the mode", w.Name, got)
		}
	}
}

// The check job calls no model, so it gets none of the budget.
func TestCheckJobHasNoBudgetEnv(t *testing.T) {
	rs, err := Repo(budgetInputs(t, "budget: { mode: enforce, per_run_usd: 5, max_run_tokens: 9, allowed_models: [claude-sonnet-5-5] }\nmodel_prices: { claude-sonnet-5-5: { input_per_m: 3, output_per_m: 15 } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check == nil {
		t.Fatal("no check job")
	}
	if got := onlyBudgetEnv(rs.Check.Env); len(got) != 0 {
		t.Errorf("check env has %v", got)
	}
}

func TestWorkflowEnvPolicyKeys(t *testing.T) {
	rs, err := Repo(budgetInputs(t, "budget: { mode: enforce, per_run_usd: 5, max_run_tokens: 500000, allowed_models: [claude-sonnet-5-5, claude-haiku-4-5] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Workflows) == 0 {
		t.Fatal("no workflows")
	}
	for _, w := range rs.Workflows {
		if w.Env[MaxRunTokensEnv] != "500000" || w.Env[AllowedModelsEnv] != "claude-sonnet-5-5,claude-haiku-4-5" {
			t.Errorf("%s env = %v", w.Name, onlyBudgetEnv(w.Env))
		}
		s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok })
		if err != nil || s.MaxRunTokens != 500000 || len(s.AllowedModels) != 2 {
			t.Fatalf("%s: SpendFromEnv = %+v, %v", w.Name, s, err)
		}
	}
	// Left out, left out: no zero token cap, no empty list.
	rs, err = Repo(budgetInputs(t, "budget: { mode: observe }\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rs.Workflows {
		for _, k := range []string{MaxRunTokensEnv, AllowedModelsEnv} {
			if _, ok := w.Env[k]; ok {
				t.Errorf("%s sets %s", w.Name, k)
			}
		}
	}
}

// The token cap and the allow-list hold with the dollar budget off, which
// is where an oauth repository stays.
func TestWorkflowEnvOAuthGetsTokenCap(t *testing.T) {
	in := budgetInputs(t, "budget: { max_run_tokens: 100, allowed_models: [claude-sonnet-5-5] }\n")
	in.Cfg.Agent.Auth = "oauth"
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rs.Workflows {
		got := onlyBudgetEnv(w.Env)
		if got[MaxRunTokensEnv] != "100" || got[AllowedModelsEnv] != "claude-sonnet-5-5" || got[BudgetModeEnv] != "" {
			t.Errorf("%s env = %v", w.Name, got)
		}
	}
}

// oauth stays budget-off for dollars, but budgetEnv is unchanged from M9a: a
// project config that sets a mode still writes FUGARO_BUDGET_MODE (and the
// cap and token cap) for an oauth workflow. The runner is what keeps oauth
// off the gateway and free of a dollar cap (runner.TestOAuthStaysBudgetOff).
func TestOAuthStaysBudgetOff(t *testing.T) {
	in := budgetInputs(t, "budget: { mode: enforce, per_run_usd: 5, max_run_tokens: 100 }\n")
	in.Cfg.Agent.Auth = "oauth"
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Workflows) == 0 {
		t.Fatal("no workflows")
	}
	for _, w := range rs.Workflows {
		got := onlyBudgetEnv(w.Env)
		if got[BudgetModeEnv] != "enforce" || got[MaxRunUSDEnv] != "5" || got[MaxRunTokensEnv] != "100" {
			t.Errorf("%s env = %v: want the mode, cap and token cap", w.Name, got)
		}
		if _, ok := w.SecretEnv[config.ReservedSecrets["claude-oauth-token"]]; !ok {
			t.Errorf("%s is not an oauth workflow: %v", w.Name, w.SecretEnv)
		}
	}
}

// Dropping a key from the project config and planning again removes it from
// the jobs' environment: nothing lingers from the first plan.
func TestWorkflowEnvDroppedPolicyKeysDisappear(t *testing.T) {
	in := budgetInputs(t, "budget: { mode: enforce, per_run_usd: 5, max_run_tokens: 500000, allowed_models: [claude-sonnet-5-5] }\n")
	first, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range first.Workflows {
		if len(onlyBudgetEnv(w.Env)) < 4 {
			t.Fatalf("%s env = %v", w.Name, onlyBudgetEnv(w.Env))
		}
	}
	in.LC = budgetInputs(t, "budget: { mode: enforce, per_run_usd: 5 }\n").LC
	second, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range second.Workflows {
		for _, k := range []string{MaxRunTokensEnv, AllowedModelsEnv} {
			if v, ok := w.Env[k]; ok {
				t.Errorf("%s still sets %s=%q", w.Name, k, v)
			}
		}
	}
}

const backendBlock = `  firebase_project: aurora-fp
  rtdb_url: https://aurora-fp-default-rtdb.firebaseio.com
  firebase_api_key: AIzaSyA0123456789abcdefghijklmnopqrstu
  token_signer: fugaro-token-signer@aurora-fp.iam.gserviceaccount.com
`

func backendEnv(env map[string]string) map[string]string {
	got := map[string]string{}
	for _, k := range []string{RTDBURLEnv, FirebaseAPIKeyEnv, BudgetGraceEnv, runner.BudgetTokenEnv} {
		if v, ok := env[k]; ok {
			got[k] = v
		}
	}
	return got
}

// Every workflow job gets the backend's address and its web key (R10)
// whenever the installation has them, whatever the mode (D11, plan task 1.4
// step 1), and the grace when the config sets one and the mode is not off.
// The signer and the Firebase project's ID stay out: a job never mints.
func TestInitRepoPassesTheBackendToEveryJob(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		rs, err := Repo(budgetInputs(t, "budget:\n  mode: "+mode+"\n  per_run_usd: 5\n"+backendBlock+"  unreachable_grace: 90s\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rs.Workflows) == 0 {
			t.Fatal("no workflows")
		}
		for _, w := range rs.Workflows {
			want := map[string]string{RTDBURLEnv: "https://aurora-fp-default-rtdb.firebaseio.com", FirebaseAPIKeyEnv: "AIzaSyA0123456789abcdefghijklmnopqrstu", BudgetGraceEnv: "1m30s"}
			if got := backendEnv(w.Env); !maps.Equal(got, want) {
				t.Errorf("%s/%s: env = %v, want %v", mode, w.Name, got, want)
			}
			for k, v := range w.Env {
				if strings.Contains(v, "token-signer") || v == "aurora-fp" {
					t.Errorf("%s: %s=%s leaks the signer or the Firebase project into a job", w.Name, k, v)
				}
			}
		}
	}
	rs, err := Repo(budgetInputs(t, "budget:\n  mode: observe\n"+backendBlock))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rs.Workflows {
		if _, ok := w.Env[BudgetGraceEnv]; ok {
			t.Errorf("%s: a grace nobody set: %v", w.Name, w.Env)
		}
	}
}

// M2 of the PR #379 review: FUGARO_BUDGET_TOKEN says "the launch of this run
// minted it a budget identity", which is a fact about one launch and nothing
// a job is deployed with. It travels as an execution override
// (backend.LaunchSpec.Env) and never through init --repo's deploy-time
// environment; a job that carried it from the deploy would look to the
// runner like a launch that had minted, whatever the launch did. Pinned here
// because nothing else in this package reads the marker — a change that put
// it into budgetEnv would otherwise pass the whole suite.
func TestInitRepoNeverDeploysTheBudgetTokenMarker(t *testing.T) {
	for _, mode := range []string{"off", "observe", "enforce"} {
		rs, err := Repo(budgetInputs(t, "budget:\n  mode: "+mode+"\n  per_run_usd: 5\n"+backendBlock+"  unreachable_grace: 90s\n"))
		if err != nil {
			t.Fatal(mode, err)
		}
		if len(rs.Workflows) == 0 {
			t.Fatal("no workflows")
		}
		envs := []map[string]string{}
		for _, w := range rs.Workflows {
			envs = append(envs, w.Env)
		}
		if rs.Check != nil {
			envs = append(envs, rs.Check.Env)
		}
		for _, env := range envs {
			if v, ok := env[runner.BudgetTokenEnv]; ok {
				t.Errorf("mode %s: a job's deploy-time env carries %s=%q: the marker belongs to a launch, not to the deploy", mode, runner.BudgetTokenEnv, v)
			}
		}
	}
}

// D11 (plan task 1.4 step 1, "the RTDB URL and API key reach every job"): a
// job carries the database's address and the Firebase web key whenever the
// installation has them, whatever the mode — the mode is policy now, and a
// run reads its own policy with those two variables (the token exchange needs
// the key, not only the URL). The mode itself stays the deploy-time mode:
// it is the fallback for the absent-node case.
func TestBudgetEnvWritesRTDBURLAndAPIKeyWhenFirebaseIsConfigured(t *testing.T) {
	for _, mode := range []string{"off", "observe", "enforce"} {
		rs, err := Repo(budgetInputs(t, "budget:\n  mode: "+mode+"\n  per_run_usd: 5\n"+backendBlock))
		if err != nil {
			t.Fatal(mode, err)
		}
		if len(rs.Workflows) == 0 {
			t.Fatal("no workflows")
		}
		for _, w := range rs.Workflows {
			if w.Env[RTDBURLEnv] != "https://aurora-fp-default-rtdb.firebaseio.com" ||
				w.Env[FirebaseAPIKeyEnv] != "AIzaSyA0123456789abcdefghijklmnopqrstu" {
				t.Errorf("mode %s: %s env = %v, want the backend's address and web key", mode, w.Name, backendEnv(w.Env))
			}
			if v, ok := w.Env[BudgetModeEnv]; ok != (mode != "off") || (ok && v != mode) {
				t.Errorf("mode %s: %s has %s=%q: the mode stays conditional", mode, w.Name, BudgetModeEnv, v)
			}
			if _, ok := w.Env[BudgetGraceEnv]; ok {
				t.Errorf("mode %s: %s has a grace nobody set", mode, w.Name)
			}
		}
		// The check job calls no model and reads no policy: it gets none of
		// it, whatever the mode.
		if rs.Check != nil && len(backendEnv(rs.Check.Env)) != 0 {
			t.Errorf("mode %s: the check job has %v", mode, backendEnv(rs.Check.Env))
		}
	}
}

// With the budget off the job still learns where the database is — a policy
// may exist and a run must be able to read it — but its budget is off: no
// mode, and nothing that would turn the shared caps on.
func TestBudgetEnvKeepsModeOff(t *testing.T) {
	for name, block := range map[string]string{
		"mode off":   "budget:\n  mode: off\n" + backendBlock + "  unreachable_grace: 90s\n",
		"empty mode": "budget:\n  per_run_usd: 5\n" + backendBlock,
	} {
		rs, err := Repo(budgetInputs(t, block))
		if err != nil {
			t.Fatal(name, err)
		}
		for _, w := range rs.Workflows {
			if _, ok := w.Env[BudgetModeEnv]; ok {
				t.Errorf("%s: %s carries a budget mode with the budget off: %v", name, w.Name, w.Env[BudgetModeEnv])
			}
			if _, ok := w.Env[BudgetGraceEnv]; ok {
				t.Errorf("%s: %s carries a grace with the budget off", name, w.Name)
			}
			if w.Env[RTDBURLEnv] == "" || w.Env[FirebaseAPIKeyEnv] == "" {
				t.Errorf("%s: %s = %v, want the backend's address and web key", name, w.Name, backendEnv(w.Env))
			}
			if s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok }); err != nil || s.On() {
				t.Errorf("%s: %s: SpendFromEnv = %+v, %v; want the budget off", name, w.Name, s, err)
			}
		}
	}
	// An installation with no Firebase configured writes neither variable:
	// there is no backend to point a job at.
	rs, err := Repo(budgetInputs(t, "budget:\n  mode: off\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rs.Workflows {
		if got := backendEnv(w.Env); len(got) != 0 {
			t.Errorf("%s has %v with no Firebase in the config", w.Name, got)
		}
	}
}

const providerLC = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/sandbox"]
  elsewhere:
    kind: anthropic-compat
    base_url: https://other.example
    auth: x-api-key
    secret: other-key
    models: ["qwen/*"]
    allow_data_to: ["acme/other"]
`

// A provider's key is mounted into the job of a repository the owner
// allowed to send code to it, as a variable no workflow can name, and into
// no other.
func TestProviderKeyMountedOnlyForAllowedRepos(t *testing.T) {
	in := sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	ws := rs.Workflows["web"]
	env := config.ProviderKeyEnv("openrouter-api-key")
	if ws.SecretEnv[env] != "openrouter-api-key" || ws.SecretIDs["openrouter-api-key"] == "" || rs.Secrets["openrouter-api-key"] == "" {
		t.Errorf("the key is not mounted: %v %v", ws.SecretEnv, ws.SecretIDs)
	}
	if _, ok := ws.SecretEnv[config.ProviderKeyEnv("other-key")]; ok {
		t.Errorf("the key of a provider this repository is not allowed to use is mounted: %v", ws.SecretEnv)
	}
	if !strings.Contains(ws.Env[runner.SecretEnvsVar], env) {
		t.Errorf("%s does not list %s, so it would not be redacted: %q", runner.SecretEnvsVar, env, ws.Env[runner.SecretEnvsVar])
	}
	for k := range ws.Env {
		if config.IsProviderKeyEnv(k) {
			t.Errorf("the job's plain env carries %s", k)
		}
	}
	// Not on oauth or vertex: a run does not mix credentials.
	in = sandboxInputs(t, m5Additions+providerLC)
	// This relies on sandboxInputs' default auth not being api-key.
	if in.Cfg.Agent.Auth == "api-key" {
		t.Fatalf("sandboxInputs now defaults to %s auth: set an oauth auth here explicitly", in.Cfg.Agent.Auth)
	}
	if rs, err = Repo(in); err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.Workflows["web"].SecretEnv[env]; ok {
		t.Errorf("the key is mounted on %s auth", in.Cfg.Agent.Auth)
	}
}

func TestWorkflowSecretCannotShadowProviderSecret(t *testing.T) {
	in := sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	w := in.Cfg.Workflows["web"]
	// Even a provider the repository may not use: the name is the owner's.
	w.Secrets = append(w.Secrets, config.Secret{Name: "other-key", Env: "MY_KEY"})
	in.Cfg.Workflows["web"] = w
	_, err := Repo(in)
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "other-key") || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("err = %v", err)
	}
	// Names differ only in case but derive the same Secret Manager ID and
	// the same variable, so a different case is a shadow too.
	in = sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	w = in.Cfg.Workflows["web"]
	w.Secrets = append(w.Secrets, config.Secret{Name: "Other-Key", Env: "MY_KEY"})
	in.Cfg.Workflows["web"] = w
	if _, err = Repo(in); !errors.As(err, &ue) || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("a differently cased name: err = %v", err)
	}
}

// The job of an allowed repository names only the providers that list it,
// each with allow_data_to cut to that one repository; a repository no provider
// lists gets no such variable at all.
func TestProvidersEnvInTheJobIsCutToTheRepo(t *testing.T) {
	in := sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	lc := in.LC.Providers["openrouter"]
	lc.AllowDataTo = []string{"acme/sandbox", "acme/webapp"}
	in.LC.Providers["openrouter"] = lc
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := rs.Workflows["web"].Env[ModelProvidersEnv]
	if !ok {
		t.Fatalf("the job has no %s: %v", ModelProvidersEnv, rs.Workflows["web"].Env)
	}
	var got map[string]config.ModelProvider
	if err := json.Unmarshal([]byte(v), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["openrouter"].AllowDataTo) != 1 || got["openrouter"].AllowDataTo[0] != "acme/sandbox" {
		t.Errorf("providers in the job = %+v, want only openrouter cut to acme/sandbox", got)
	}
	if strings.Contains(v, "acme/webapp") || strings.Contains(v, "other-key") || strings.Contains(v, "acme/other") {
		t.Errorf("the job's providers leak another repository or provider: %s", v)
	}

	// A repository no provider lists: no variable.
	in = sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	for name, p := range in.LC.Providers {
		p.AllowDataTo = []string{"acme/other"}
		in.LC.Providers[name] = p
	}
	if rs, err = Repo(in); err != nil {
		t.Fatal(err)
	}
	if v, ok := rs.Workflows["web"].Env[ModelProvidersEnv]; ok {
		t.Errorf("a repository no provider allows got %s = %s", ModelProvidersEnv, v)
	}
}

// The job's FUGARO_MODEL_PROVIDERS is written from the local config, which
// cannot carry a claim at all: claude_models is a project-layer key and
// localcfg.Config has no field for it. The claim reaches a run only when the
// runner reads the layer itself and merges it for that run's route table
// (resolveConfig), so the claim is never in the deployed job — the layer is
// read at run time, per run. This pins the render path's input: every
// workflow's variable is exactly runner.ProvidersEnv of the local config's own
// providers, and the merged map a render path that took the layer's claims
// would write is provably different. Mutation that makes this fail: render
// runner.ProvidersEnv(merged, c.in.Repo) in spec.go instead of
// runner.ProvidersEnv(lc.Providers, c.in.Repo).
func TestTheJobProvidersEnvNeverCarriesALayerClaim(t *testing.T) {
	in := sandboxInputs(t, m5Additions+providerLC)
	in.Cfg.Agent.Auth = "api-key"
	const id = "anthropic/claude-sonnet-5.5"
	// The layer's own anchor: the sandbox file carries no gcp_project, so
	// this is a fixed valid one, used for the text and the parse alike.
	anchor := config.LayerAnchor{Project: in.Cfg.Project, GCPProject: "acme-fugaro"}
	text := "version: 1\nproject: " + anchor.Project + "\ngcp_project: " + anchor.GCPProject +
		"\nclaude_models:\n  openrouter: [\"" + id + "\"]\n"
	l, ps := config.ParseProjectLayer([]byte(text), anchor)
	if len(ps) > 0 {
		t.Fatalf("this test's own layer is invalid: %v", ps)
	}
	if len(l.ClaudeModels["openrouter"]) != 1 {
		t.Fatalf("the test's layer carries no claim: %+v", l.ClaudeModels)
	}
	// The map a render path that merged the layer would write, so the
	// comparison below is against something that really differs.
	merged, mps := config.ApplyClaudeClaims(in.LC.Providers, l.ClaudeModels)
	if len(mps) > 0 {
		t.Fatalf("the merge refused the claim: %v", mps)
	}
	mb, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mb), id) {
		t.Fatalf("the merge did not add the claim: %s", mb)
	}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	want, err := runner.ProvidersEnv(in.LC.Providers, in.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if want == "" {
		t.Fatal("the fixture's repository is allowed no provider, so this test proves nothing")
	}
	for _, w := range rs.Workflows {
		got, ok := w.Env[ModelProvidersEnv]
		if !ok || got != want {
			t.Errorf("%s: %s = %q, want the claim-blind %q", w.Name, ModelProvidersEnv, got, want)
		}
		if strings.Contains(got, "anthropic/") {
			t.Errorf("%s: the job's providers carry a layer claim: %s", w.Name, got)
		}
	}
}

// A checked workflow without base: builds from base_images.base, and the
// check job runs from it.
func TestCheckJobOnTheBaseKind(t *testing.T) {
	in := webappInputs(t)
	for name, w := range in.Cfg.Workflows {
		w.Base, w.Image.Node = "", ""
		w.Rebuild.Check = "auto"
		in.Cfg.Workflows[name] = w
	}
	base := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3"
	in.LC.BaseImages = map[string]string{"base": base}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check.Image != base {
		t.Errorf("check image = %s, want %s", rs.Check.Image, base)
	}
	var spec CheckJobSpec
	if err := json.Unmarshal([]byte(rs.Check.Env[CheckSpecEnv]), &spec); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"base": base}; !maps.Equal(spec.BaseImages, want) {
		t.Errorf("job spec base images = %v, want %v", spec.BaseImages, want)
	}
}

// tf.Cover reads the registry and base images out of FUGARO_CHECK_SPEC with
// its own struct (the tf package cannot import this one): these are the two
// JSON keys it decodes, so renaming either here fails until Cover follows.
func TestCheckJobSpecKeysCoverReads(t *testing.T) {
	b, err := json.Marshal(CheckJobSpec{Registry: "h/p/r", BaseImages: map[string]string{"k": "h/p/b:1"}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["registry"]) != `"h/p/r"` || string(m["base_images"]) != `{"k":"h/p/b:1"}` {
		t.Fatalf("CheckJobSpec encodes as %s, tf/cover.go reads registry and base_images", b)
	}
}

// TestRepoSpecBaseBranchFallsBackToTheCheckout: a project config whose
// repo entry has no base_branch (every shared config: it is never
// published) builds and checks the checkout's reviewed git.base_branch,
// never an empty branch.
func TestRepoSpecBaseBranchFallsBackToTheCheckout(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	r := in.LC.Repos["acme/sandbox"]
	r.BaseBranch = ""
	in.LC.Repos["acme/sandbox"] = r
	want := in.Cfg.Git.BaseBranch
	if want == "" {
		t.Fatal("the checkout has no base branch")
	}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.BaseBranch != want || rs.Check == nil {
		t.Fatalf("base branch %q, want the checkout's %q", rs.BaseBranch, want)
	}
	var spec CheckJobSpec
	if err := json.Unmarshal([]byte(rs.Check.Env[CheckSpecEnv]), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.BaseBranch != want {
		t.Errorf("check spec base branch %q, want %q", spec.BaseBranch, want)
	}
	// A checkout that names none gets fugaro.yaml's default, main.
	in.Cfg.Git.BaseBranch = ""
	data, _ := os.ReadFile("../../deploy/sandbox/fugaro.yaml")
	cfg := parseCfg(t, []byte(strings.Replace(string(data), "base_branch: "+want, "", 1)))
	in.Cfg = cfg
	rs, err = Repo(in)
	if err != nil || rs.BaseBranch == "" {
		t.Errorf("no base_branch anywhere: %q, %v (config base_branch %q)", rs.BaseBranch, err, cfg.Git.BaseBranch)
	}
}

// claudeAccountsLC configures two named Claude accounts: "acme-personal",
// allowed for acme/sandbox (sandboxInputs' repo, oauth by default), and
// "acme-overflow", allowed for a different repository only.
const claudeAccountsLC = `claude_accounts:
  acme-personal:
    allow_data_to: ["acme/sandbox"]
  acme-overflow:
    allow_data_to: ["acme/other"]
`

// An installation that configures no claude_accounts at all gets exactly
// the workflow keys fugaro.yaml names, unchanged: gcp.WorkflowVariant is the
// identity function for the repository's default account, so
// BuildRepoSpec's loop produces exactly the one WorkflowSpec per workflow it
// produced before this task (design docs/design/named-oauth-tokens.md §5).
// TestGoldenTfvars is the byte-for-byte proof of this over the fixture
// corpus; this test pins the one fact that proof depends on, directly.
func TestBuildRepoSpecUnchangedWithNoClaudeAccounts(t *testing.T) {
	// Both an oauth fixture (sandboxInputs) and a non-oauth one
	// (webappInputs, vertex): named accounts only ever apply to oauth
	// workflows, so both must independently prove the identity holds,
	// whatever auth kind in.Cfg.Agent.Auth names.
	for _, in := range []Inputs{sandboxInputs(t, m5Additions), webappInputs(t)} {
		if in.LC.ClaudeAccounts != nil {
			t.Fatal("the fixture already configures claude_accounts")
		}
		rs, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := slices.Sorted(maps.Keys(rs.Workflows)), slices.Sorted(maps.Keys(in.Cfg.Workflows)); !slices.Equal(got, want) {
			t.Fatalf("%s: rs.Workflows keys = %v, want exactly the fugaro.yaml workflow names %v", in.Cfg.Agent.Auth, got, want)
		}
	}
}

// A repository not listed in a named account's allow_data_to gets no job
// variant for that name: the hard backstop of the allow-list (design §6b.3,
// "a job for a disallowed name simply does not exist").
func TestRepoSpecHasNoVariantForDisallowedName(t *testing.T) {
	in := sandboxInputs(t, m5Additions+claudeAccountsLC)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.Workflows[gcp.WorkflowVariant("web", "acme-overflow")]; ok {
		t.Fatal("a job variant exists for a name this repository is not allowed to use")
	}
	if _, ok := rs.Workflows["web"]; !ok {
		t.Fatal("the default variant is missing")
	}
	if _, ok := rs.Workflows[gcp.WorkflowVariant("web", "acme-personal")]; !ok {
		t.Fatal("the allowed named account's variant is missing")
	}
	// The variant's Cloud Run job and dedicated service account must be
	// distinct from the default's: their map keys already differ, but Job
	// and ServiceAccount.AccountID are independently derived fields that a
	// mutation could leave keyed on the logical workflow name alone.
	def, named := rs.Workflows["web"], rs.Workflows[gcp.WorkflowVariant("web", "acme-personal")]
	if def.Job == named.Job {
		t.Errorf("the named variant's job %q collides with the default's", named.Job)
	}
	if def.ServiceAccount.AccountID == named.ServiceAccount.AccountID {
		t.Errorf("the named variant's service account %q collides with the default's", named.ServiceAccount.AccountID)
	}
	if def.Labels[gcp.LabelWorkflow] == named.Labels[gcp.LabelWorkflow] {
		t.Errorf("the named variant's fugaro_workflow label %q collides with the default's", named.Labels[gcp.LabelWorkflow])
	}
}

// A named account's variants exist ONLY for oauth repositories: an api-key or
// vertex repository that an account happens to list in allow_data_to still
// gets exactly the one default job per workflow (the oauth gate at the loop in
// Repo(): removing it stayed green until this test).
func TestRepoSpecHasNoVariantForNonOAuthRepositories(t *testing.T) {
	for _, auth := range []string{"api-key", "vertex"} {
		in := sandboxInputs(t, m5Additions+claudeAccountsLC)
		in.Cfg.Agent.Auth = auth
		rs, err := Repo(in)
		if err != nil {
			t.Fatalf("auth %s: %v", auth, err)
		}
		if len(rs.Workflows) != len(in.Cfg.Workflows) {
			t.Errorf("auth %s: %d jobs for %d workflows: a variant was built for a non-oauth repository (keys %v)", auth, len(rs.Workflows), len(in.Cfg.Workflows), slices.Sorted(maps.Keys(rs.Workflows)))
		}
		for key := range rs.Workflows {
			if _, ok := in.Cfg.Workflows[key]; !ok {
				t.Errorf("auth %s: job key %q is not a fugaro.yaml workflow name (a variant)", auth, key)
			}
		}
	}
}

// Each named account's job variant mounts only its own token, under its own
// secret ID: never another allowed name's, and never the bare default's
// (the regression option A, §4 of the design, would mount every allowed
// name into every variant).
func TestRepoSpecSecretMountsOnlyTheVariantsOwnToken(t *testing.T) {
	in := sandboxInputs(t, m5Additions+claudeAccountsLC)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	env := config.ReservedSecrets["claude-oauth-token"]
	cases := []struct {
		variant, wantSecret string
	}{
		{"web", "claude-oauth-token"},
		{gcp.WorkflowVariant("web", "acme-personal"), "claude-oauth-token-acme-personal"},
	}
	for _, c := range cases {
		ws, ok := rs.Workflows[c.variant]
		if !ok {
			t.Fatalf("no workflow spec for variant %q", c.variant)
		}
		if got := ws.SecretEnv[env]; got != c.wantSecret {
			t.Errorf("%s: SecretEnv[%s] = %q, want %q", c.variant, env, got, c.wantSecret)
		}
		var claudeSecrets []string
		for logical := range ws.SecretIDs {
			if logical == c.wantSecret || strings.HasPrefix(logical, "claude-oauth-token") {
				claudeSecrets = append(claudeSecrets, logical)
			}
		}
		if want := []string{c.wantSecret}; !slices.Equal(slices.Sorted(slices.Values(claudeSecrets)), want) {
			t.Errorf("%s: claude secrets mounted = %v, want exactly %v", c.variant, claudeSecrets, want)
		}
	}
}

// A migrated base_images.base is in the installation's own project, through
// its remote repository, so nothing is warned about: the build account can
// read it. The remote repository's own upstream on ghcr.io is not in the
// installation's project and still warns — the pull there is not one the
// build account can make.
func TestBaseImageWarningsAcceptTheRemoteRepository(t *testing.T) {
	outs := InstallationOutputs{RegistryHost: "us-east5-docker.pkg.dev/proj-1234", BaseRegistry: BaseRegistry, GhcrRegistry: GhcrRegistry}
	pin := outs.RegistryHost + "/" + GhcrRegistry + "/dimipaun/fugaro-base:0.7.5@" + baseTestDigest
	if ws := BaseImageWarnings(map[string]string{"base": pin}, outs); len(ws) != 0 {
		t.Errorf("the pin: %v", ws)
	}
	if ws := BaseImageWarnings(map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.7.5"}, outs); len(ws) != 1 {
		t.Errorf("the upstream itself: %v", ws)
	} else if !strings.Contains(ws[0], outs.RegistryHost+"/"+GhcrRegistry) {
		t.Errorf("the warning does not name the remote repository: %q", ws[0])
	}
}

func TestRepoNeverPrunesSecretsButGainsCandidates(t *testing.T) {
	before, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	// D2 (plan Task 2.1 step 3b): the sandbox's git credential is a reserved
	// name, and the one reserved name the sweep does not take in -- a
	// Bitbucket repository access token is issued for one repository and
	// authenticates no other (owner ruling of 2026-10-15,
	// config.PerRepositorySecret). With no project_secrets entry it has no
	// project-wide candidate at all, and no other name the sandbox mounts is
	// one, so the set is empty.
	if len(before.ProjectSecretCandidates) != 0 {
		t.Fatalf("ProjectSecretCandidates = %+v, want none with no project_secrets configured", before.ProjectSecretCandidates)
	}
	// bitbucket-token is the sandbox's git credential, mounted by every
	// workflow (SecretMounts's unconditional first entry) -- allow-listing
	// it project-wide here is the owner's explicit opt-in and the design's
	// own worked example (§6), and it is the only thing that gives the name
	// a project copy.
	after, err := Repo(sandboxInputs(t, m5Additions+"project_secrets:\n  bitbucket-token:\n    allow_data_to: [\"acme/sandbox\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"bitbucket-token": gcp.ProjectSecretID("aurora", "bitbucket-token")}
	// (a) P2's golden property: the key SET (and every ID) is identical,
	// unaffected by project_secrets existing at all.
	if !maps.Equal(before.Secrets, after.Secrets) {
		t.Fatalf("rs.Secrets changed:\nbefore: %+v\nafter:  %+v", before.Secrets, after.Secrets)
	}
	// (b) The entry's own list is what decides the candidate, and it names
	// this repository.
	if !maps.Equal(after.ProjectSecretCandidates, want) {
		t.Fatalf("ProjectSecretCandidates = %+v, want %+v", after.ProjectSecretCandidates, want)
	}
}

// The containers the installation root plans (design §4, installation
// secrets.tf): §3a's rule is that every provider gets one whether or not its
// allow_data_to exists -- an absent list is a valid "every repository" state,
// not a reason to skip the container -- while a project_secrets or
// claude_accounts entry needs a non-empty list, an empty one meaning nobody.
// A mutation that drops the unconditional provider branch, or that lets an
// empty-list entry through, leaves the rest of the spec identical, so nothing
// else fails.
func TestProjectSecretContainers(t *testing.T) {
	const accounts = `claude_accounts:
  acme-personal:
    allow_data_to: [acme/sandbox]
  acme-nobody:
    allow_data_to: []
`
	tests := []struct {
		name string
		lc   string
		want []string
	}{
		{"nothing configured", "", nil},
		{"project_secrets entry with a list", projectSecretLC, []string{"bitbucket-token"}},
		{"project_secrets entry with an empty list", "project_secrets:\n  bitbucket-token: { allow_data_to: [] }\n", nil},
		{"provider with no list", noListProviderLC, []string{"no-list-key"}},
		{"provider with a list", providerLC, []string{"openrouter-api-key", "other-key"}},
		{"named account with a list", accounts, []string{"claude-oauth-token-acme-personal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := parseLC(t, m4LocalConfig+m5Additions+tt.lc)
			spec, err := Installation(lc, InstallOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{}
			for _, logical := range tt.want {
				want[logical] = gcp.ProjectSecretID(lc.Name, logical)
			}
			if !maps.Equal(spec.ProjectSecrets, want) {
				t.Errorf("ProjectSecrets = %+v, want %+v", spec.ProjectSecrets, want)
			}
		})
	}
}

// --- RepoSpec.Addresses (plan docs/plans/2026-10-14-config-transitions.md Task 1)

// The golden fixtures' agent blocks. oauth with one named account and
// vertex exclude one another (Repo builds named variants only under oauth),
// so google_project_iam_member.vertex[0] can never appear in the oauth
// fixture's address set and needs a fixture of its own.
const (
	repoRootOAuthAgent  = `agent: { auth: oauth }`
	repoRootVertexAgent = `agent: { auth: vertex }`
)

// repoRootInputs is the golden fixtures' spec: one baked workflow with its
// daily check on, one workflow-only secret and the git provider's
// credential, one operator and one launcher, and the agent block given.
// Between the two agent blocks it turns every branch of the address set on
// but checkout: clone, which TestRepoAddressesIncludesCheckOnlyWhenChecked
// covers.
func repoRootInputs(t *testing.T, agent string) Inputs {
	t.Helper()
	lc := parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: "123456" }
claude_accounts:
  bmb: { allow_data_to: [acme/webapp] }
`)
	out := installationOutputs()
	out.Launchers = []string{"user:launcher@example.com"}
	out.Operators = []string{"user:operator@example.com"}
	cfg := `version: 1
project: aurora
git: { provider: github, base_branch: main }
` + agent + `
workflows:
  web:
    commands: { build: npm run build, test: npm test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
`
	return Inputs{LC: lc, Repo: "acme/webapp", Cfg: parseCfg(t, []byte(cfg)), Installation: out}
}

// repoRootSpec is the golden fixture's spec for the agent block given.
func repoRootSpec(t *testing.T, agent string) RepoSpec {
	t.Helper()
	rs, err := Repo(repoRootInputs(t, agent))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// variantKeys are the workflow variant keys the addresses name, in the
// order they first appear.
func variantKeys(addrs []string) []string {
	var out []string
	for _, a := range addrs {
		rest, ok := strings.CutPrefix(a, "module.repo.module.workflow[")
		if !ok {
			continue
		}
		key, _, ok := strings.Cut(rest, "]")
		if !ok {
			continue
		}
		if k, err := strconv.Unquote(key); err == nil && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// A named Claude account is a workflow variant only under oauth: Repo
// builds them only there, and the address set must follow the map rather
// than drift from it (Review Focus 3).
func TestRepoAddressesNamedVariantsOnlyUnderOAuth(t *testing.T) {
	oauth := repoRootSpec(t, repoRootOAuthAgent)
	if got, want := slices.Sorted(maps.Keys(oauth.Workflows)), []string{"web", "web_bmb"}; !slices.Equal(got, want) {
		t.Fatalf("oauth spec Workflows = %v, want %v", got, want)
	}
	if got, want := variantKeys(oauth.Addresses()), []string{"web", "web_bmb"}; !slices.Equal(got, want) {
		t.Errorf("oauth variant keys = %v, want %v", got, want)
	}
	apiKey := repoRootSpec(t, `agent: { auth: api-key }`)
	if got, want := variantKeys(apiKey.Addresses()), []string{"web"}; !slices.Equal(got, want) {
		t.Errorf("api-key variant keys = %v, want %v", got, want)
	}
	if _, ok := apiKey.Secrets["claude-oauth-token"]; ok {
		t.Error("an api-key spec still carries the OAuth secret")
	}
}

// A checkout: clone workflow has no derived image, so it is never checked:
// with no checked workflow at all the check job, its invoker grant and its
// Scheduler job are gone, and so are their addresses.
func TestRepoAddressesIncludesCheckOnlyWhenChecked(t *testing.T) {
	checked := repoRootSpec(t, repoRootOAuthAgent)
	if checked.Check == nil {
		t.Fatal("the baked fixture has no check")
	}
	for _, want := range []string{
		"module.repo.google_cloud_run_v2_job.check[0]",
		"module.repo.google_cloud_run_v2_job_iam_member.check_invoker[0]",
		"module.repo.google_cloud_scheduler_job.check[0]",
	} {
		if !slices.Contains(checked.Addresses(), want) {
			t.Errorf("a checked workflow's addresses are missing %s", want)
		}
	}
	// A repository whose only workflow is checkout: clone: Repo sets no
	// Check at all (the clone workflow is never in checked).
	cloneLC := parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: "123456" }
`)
	in := repoRootInputs(t, `agent: { auth: api-key }`)
	in.LC = cloneLC
	in.Cfg = parseCfg(t, []byte(`version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: api-key }
workflows:
  web:
    checkout: clone
    commands: { build: npm run build, test: npm test }
`))
	clone, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Check != nil {
		t.Fatal("a repository whose only workflow is clone has a check spec")
	}
	for _, a := range clone.Addresses() {
		if strings.Contains(a, "check") {
			t.Errorf("a repository with no checked workflow still names %s", a)
		}
	}
}

// The vertex grant exists only for agent.auth: vertex, so the oauth
// fixture can never pin it: the vertex fixture does, and the oauth one is
// the negative half.
func TestRepoAddressesVertexIsNotUnderOAuth(t *testing.T) {
	const vertexGrant = `google_project_iam_member.vertex`
	oauth := repoRootSpec(t, repoRootOAuthAgent).Addresses()
	if slices.ContainsFunc(oauth, func(a string) bool { return strings.Contains(a, vertexGrant) }) {
		t.Error("the oauth spec's addresses name a vertex grant")
	}
	vertex := repoRootSpec(t, repoRootVertexAgent)
	if got, want := variantKeys(vertex.Addresses()), []string{"web"}; !slices.Equal(got, want) {
		t.Errorf("vertex variant keys = %v, want %v (vertex has no named variants)", got, want)
	}
	if !slices.Contains(vertex.Addresses(), `module.repo.module.workflow["web"].`+vertexGrant+`[0]`) {
		t.Error("the vertex spec's addresses are missing the vertex grant")
	}
}

// repoRootGoldenDir holds the golden fixtures of Addresses: the tfvars the
// real root is planned from, and the address list that plan produced.
const repoRootGoldenDir = "testdata"

// repoRootFixtures are the two fixture specs, keyed by the golden's name.
// A third is how the guard grows: add the key here with the agent spec it
// plans from, add testdata/<name>.tfvars.json (TestRepoRootFixtureTfvars
// writes it under -update), then on a machine with terraform run
// `go test -tags terraform ./internal/infra -run TestRealRepoRootAddresses
// -update` to write testdata/<name>_addresses.golden. CI (untagged) then
// compares Addresses() against it on every run. Every address the modules
// declare is named by Addresses() and by both goldens: #362's
// module.repo.google_artifact_registry_repository_iam_member.build_ghcr was
// the last one the untagged guard caught as missing, and naming it in
// Addresses() and regenerating the goldens from the real plan is what closed
// that gap.
var repoRootFixtures = map[string]string{"repo_root": repoRootOAuthAgent, "repo_root_vertex": repoRootVertexAgent}

// TestRepoRootFixtureTfvars keeps the committed tfvars fixtures and the Go
// fixture that plans from them in step: the golden address list belongs to
// exactly the spec these tfvars encode, so a fixture edit without a
// regeneration fails here rather than silently changing what the golden
// pins.
func TestRepoRootFixtureTfvars(t *testing.T) {
	for name, agent := range repoRootFixtures {
		spec := repoRootSpec(t, agent)
		got, err := RepoRootVars(spec, RepoRootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repoRootGoldenDir, name+".tfvars.json")
		if *update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (regenerate with go test ./internal/infra -run TestRepoRootFixtureTfvars -update, or the tagged generator real_addresses_test.go)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the fixture spec's tfvars; regenerate with -update and review the diff", path)
		}
	}
}

// addressesGolden reads one golden address list.
func addressesGolden(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRootGoldenDir, name+"_addresses.golden"))
	if err != nil {
		t.Fatalf("%v (generate it with go test -tags terraform ./internal/infra -run TestRealRepoRootAddresses -update -count=1, which plans a temp copy of the real root with no credentials)", err)
	}
	var out []string
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// Addresses is a hand-maintained mirror of the Terraform modules, so it is
// pinned against a real plan rather than trusted (Review Focus 8): every
// address the real root's plan produces for the fixture spec must be in
// Addresses, and the other way round.
func TestRepoAddressesMatchTheRealPlanGolden(t *testing.T) {
	spec := repoRootSpec(t, repoRootFixtures["repo_root"])
	want := addressesGolden(t, "repo_root")
	if len(want) == 0 {
		t.Fatal("the golden is empty")
	}
	got := spec.Addresses()
	if !slices.Equal(got, want) {
		t.Errorf("Addresses() differs from %s:\nmissing: %v\nunexpected: %v", "repo_root_addresses.golden",
			missingFrom(want, got), missingFrom(got, want))
	}
}

// The vertex fixture pins the one address the oauth fixture can never
// contain, google_project_iam_member.vertex[0], and the named-account
// variants it does not have.
func TestRepoAddressesMatchTheVertexGolden(t *testing.T) {
	spec := repoRootSpec(t, repoRootFixtures["repo_root_vertex"])
	want := addressesGolden(t, "repo_root_vertex")
	if len(want) == 0 {
		t.Fatal("the golden is empty")
	}
	got := spec.Addresses()
	if !slices.Equal(got, want) {
		t.Errorf("Addresses() differs from repo_root_vertex_addresses.golden:\nmissing: %v\nunexpected: %v",
			missingFrom(want, got), missingFrom(got, want))
	}
}

// moduleResources are the resource blocks the repository's own modules
// declare, as "type.name", for the modules a repository root includes.
//
// The two tests above compare Addresses() with a golden, and a golden is a
// snapshot a manual -tags terraform -update rewrites: a resource added to a
// module -- #362's google_artifact_registry_repository_iam_member.build_ghcr,
// say -- leaves both sides agreeing with each other and both wrong, and
// every fugaro init --repo would then print a false "This change retires 1
// address" pointing at a destructive command. This reads the modules
// themselves, embedded in the binary, so the drift fails a test that CI runs
// with no terraform, no network and no golden to maintain.
var moduleResources = []string{"gcp/modules/repo", "gcp/modules/workflow"}

// moduleResourceRE matches a resource declaration's type and name. The
// instance key is not in the declaration: it is for_each's, at apply time.
var moduleResourceRE = regexp.MustCompile(`(?m)^resource\s+"([^"]+)"\s+"([^"]+)"`)

// conditionalResources are the declared resources Addresses() may legitimately
// not produce for either fixture, each with the reason it is off. Every entry
// is a hole in this guard, so it takes a reason: a resource that is merely
// missing because nobody regenerated the golden does not belong here.
var conditionalResources = map[string]string{
	// The credentials service's invoker grant (C1.18, D147): off in both
	// fixtures, because neither opts in, and the whole point of the opt-in is
	// that an installation that has not turned it on has no such resource.
	// Addresses() adds it, so the day a fixture sets credentials_service the
	// guard sees it and this entry can go.
	"google_cloud_run_v2_service_iam_member.credentials": "the installation has not opted in to the credentials service (credentials_service is unset in both fixtures), so no job account is granted invoker on it",
}

// TestAddressesCoversEveryModuleResource is the drift guard the goldens
// cannot be: every resource the repo and workflow modules declare must show
// up in the union of the two fixtures' Addresses(), or a retirement would be
// predicted for something the plan really produces.
func TestAddressesCoversEveryModuleResource(t *testing.T) {
	var addrs []string
	for _, agent := range repoRootFixtures {
		addrs = append(addrs, repoRootSpec(t, agent).Addresses()...)
	}
	if len(addrs) == 0 {
		t.Fatal("the fixtures produce no address")
	}
	// The address carries the instance key and its module prefix; what a
	// declaration names is the type and the name inside it.
	produced := func(typ, name string) bool {
		re := regexp.MustCompile(`\.` + regexp.QuoteMeta(typ) + `\.` + regexp.QuoteMeta(name) + `(\[|$)`)
		return slices.ContainsFunc(addrs, re.MatchString)
	}
	seen := 0
	for _, dir := range moduleResources {
		files, err := fs.Glob(terraform.FS, dir+"/*.tf")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("%s declares no .tf file: the guard is reading nothing", dir)
		}
		for _, file := range files {
			b, err := fs.ReadFile(terraform.FS, file)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range moduleResourceRE.FindAllStringSubmatch(string(b), -1) {
				typ, name := m[1], m[2]
				seen++
				if produced(typ, name) {
					continue
				}
				if why, ok := conditionalResources[typ+"."+name]; ok {
					t.Logf("%s.%s is declared but no fixture produces it: %s", typ, name, why)
					continue
				}
				t.Errorf("%s declares %s.%s, and no fixture's Addresses() has an address for it.\n"+
					"A retirement is computed as the state minus those addresses, so this resource would be\n"+
					"retired -- printed as a change that removes it -- on every fugaro init --repo.\n"+
					"Add it to Addresses() (internal/infra/spec.go), regenerate the goldens\n"+
					"(go test -tags terraform ./internal/infra -run TestRealRepoRootAddresses -update),\n"+
					"or list it in conditionalResources with the reason it is off in both fixtures.",
					file, typ, name)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no resource declaration was read: the guard is reading nothing")
	}
}

// missingFrom are the items of want that got does not have.
func missingFrom(want, got []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			out = append(out, w)
		}
	}
	return out
}
