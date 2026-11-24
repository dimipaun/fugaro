package infra

import (
	"bytes"
	"context"
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
	"time"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	// The M4 fixture predates the image's origin stamp, which every workflow
	// job now carries (empty when the catalog chose no image): compare the rest.
	m4Env := maps.Clone(ws.Env)
	delete(m4Env, ImageEntryEnv)
	for name, c := range map[string]struct{ got, want map[string]string }{
		"env":          {m4Env, anyMap(golden["env"].(map[string]any))},
		"env row":      {m4Env, pairs(t, strings.TrimPrefix(rows["env"], "^;^"), ";", "")},
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

// TestSpecStampsTheCatalogEntryOnTheJob: a job whose image the project's
// image_catalog chose carries the entry's own name in FUGARO_IMAGE_ENTRY
// (runner.ImageEntryEnv), which is the one fact that tells a run its image is
// an operator's and not a repository's (docs/image-env, reconciliation F5/F6,
// D1371). It is written for a built entry and a prebuilt one alike:
// CatalogImageEntry reads the entry back out of a prebuilt reference, so both
// are the catalog's and both stamp. The value is a name — the text
// config.ValidateCatalog checked — and never the reference the job's Image
// holds, which is what a report prints and a reader recognises.
//
// Mutation (run, restore): write ws.Image in place of entry.Name in
// workflow() — this test then fails on a reference where the entry's name is
// wanted.
func TestSpecStampsTheCatalogEntryOnTheJob(t *testing.T) {
	for _, c := range []struct {
		name  string
		spec  func(t *testing.T) RepoSpec
		entry string
	}{
		{"built", func(t *testing.T) RepoSpec {
			_, spec := catalogSpec(t)
			return spec
		}, "base"},
		{"prebuilt", func(t *testing.T) RepoSpec {
			spec, err := Repo(budgetInputs(t, prebuiltCatalogBlock))
			if err != nil {
				t.Fatal(err)
			}
			return spec
		}, "team"},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := c.spec(t)
			if len(spec.Workflows) == 0 {
				t.Fatal("fixture bug: no workflows")
			}
			for name, ws := range spec.Workflows {
				if ws.CatalogEntry != c.entry {
					t.Fatalf("fixture bug: workflow %s runs entry %q, want %q", name, ws.CatalogEntry, c.entry)
				}
				if got := ws.Env[ImageEntryEnv]; got != c.entry {
					t.Errorf("workflow %s: %s = %q, want the entry's own name %q", name, ImageEntryEnv, got, c.entry)
				}
			}
		})
	}
}

// TestSpecStampsNoEntryOnAJobWithNoCatalogEntry: a job whose image the catalog
// did not choose carries no stamp at all — the key is absent, not empty — so a
// run on it passes none of its image's own variables. That is D1371's
// fail-closed direction, and it is what makes the scope D1370 rules
// enforceable: a repository-authored Dockerfile is repository text, and the
// repository's own image is the one a run must not take its variables from.
//
// Mutation (run, restore): write the key outside the `entry.Name != ""` guard
// in workflow() — this test then fails: every job carries it, empty where the
// catalog chose nothing, and an empty stamp is a job a run would treat as
// stamped.
func TestSpecStampsNoEntryOnAJobWithNoCatalogEntry(t *testing.T) {
	for _, c := range []struct {
		name string
		spec func(t *testing.T) RepoSpec
	}{
		{"a repository-built job", func(t *testing.T) RepoSpec {
			spec, err := Repo(webappInputs(t))
			if err != nil {
				t.Fatal(err)
			}
			return spec
		}},
		{"a checkout: clone job", func(t *testing.T) RepoSpec {
			spec, err := Repo(cloneInputs(t))
			if err != nil {
				t.Fatal(err)
			}
			return spec
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := c.spec(t)
			if len(spec.Workflows) == 0 {
				t.Fatal("fixture bug: no workflows")
			}
			for name, ws := range spec.Workflows {
				if ws.CatalogEntry != "" {
					t.Fatalf("fixture bug: workflow %s runs entry %q, want none", name, ws.CatalogEntry)
				}
				// Present and empty: a key the job sets overrides an
				// image's own ENV of the same name, an absent one does not.
				if got, ok := ws.Env[ImageEntryEnv]; !ok || got != "" {
					t.Errorf("workflow %s carries %s = %q (set: %v), want the key present and empty", name, ImageEntryEnv, got, ok)
				}
			}
		})
	}
}

// jobImageYAML is the webapp with both workflows carrying a repository-level
// image definition: web an image: block, api a dockerfile: — the two are
// mutually exclusive in a fugaro.yaml — and both a rebuild: that asks for
// something the daily check would act on. D1900 keeps every one of them
// decodable (the config is still the repository's own) and stops acting on
// any: a project that builds its own images gives every workflow's job the
// image of the entry the resolver names.
const jobImageYAML = `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: vertex }
workflows:
  web:
    image: { setup: ["apt-get install -y jq"], tools: { node: "24.19.0" }, key_files: [patches] }
    rebuild: { check: "off" }
    commands: { build: npm run build, test: npm test }
  api:
    dockerfile: api.Dockerfile
    rebuild: { check: "off" }
    commands: { build: ./gradlew assemble, test: ./gradlew test }
`

// TestSpecJobImageIgnoresRepoLevelConfig is D1900 at the job spec, which is
// where the image is fixed (F7): a workflow with image.setup, a dockerfile:
// and a rebuild: used to resolve to the per-repository derived tag —
// gcp.ImageName(registry, slug, workflow) — and now resolves to the entry the
// project's catalog gives it, the default here. A caller that still built the
// per-repository name would deploy a job on an image nothing builds.
//
// The warning is the person's half of the same rule: one sentence per key
// written, naming the entry whose image the job now runs, so a repository that
// meant to keep its own image reads why it did not (D1900).
//
// Mutation (run, restore): return gcp.ImageName(...) instead of the resolver's
// answer in workflow() — this test then fails on the derived tag for both
// workflows, and carries no warning.
func TestSpecJobImageIgnoresRepoLevelConfig(t *testing.T) {
	in := budgetInputs(t, catalogBlock)
	in.Cfg = parseCfg(t, []byte(jobImageYAML))
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Workflows) != 2 {
		t.Fatalf("fixture bug: %d workflows, want 2", len(spec.Workflows))
	}
	wantWarnings := map[string]int{"web": 2, "api": 2}
	for name, ws := range spec.Workflows {
		if ws.CatalogEntry != "base" {
			t.Errorf("workflow %s runs entry %q, want the catalog's default base: the repository's own keys decide nothing (D1900)", name, ws.CatalogEntry)
		}
		if !strings.Contains(ws.Image, "/"+catalogImagePrefix+"base:latest") {
			t.Errorf("workflow %s's image is %s, want the default entry's; a per-repository tag is the image this reversal took away", name, ws.Image)
		}
		var ignored []string
		for _, w := range ws.Warnings {
			if strings.HasSuffix(w, "(D1900)") {
				ignored = append(ignored, w)
			}
		}
		if len(ignored) != wantWarnings[name] {
			t.Errorf("workflow %s warns %d times about its own keys (%q), want %d: one sentence per key written", name, len(ignored), ignored, wantWarnings[name])
		}
		for _, w := range ignored {
			if !strings.Contains(w, "the entry base") {
				t.Errorf("workflow %s's warning %q does not name the entry whose image its job runs", name, w)
			}
		}
	}
}

// sharedYAML is cloneYAML with web on checkout: shared (design
// forge-authorship-images/shared-images.md §6, route A): a job that runs a
// shared dependency-layer image and clones the repository's own source at
// launch. It declares the dependency-layer keys — the ones checkout: clone
// refuses and this kind exists to accept.
//
// It is written as baked, which accepts the same keys: validation refuses
// checkout: shared until 0.15.0 builds a shared image (config/image.go's
// sharedCheckoutShipped), and sharedInputs moves the parsed workflow onto the
// kind afterwards. What these tests pin is the job a shared workflow's spec
// gets, not how its fugaro.yaml was validated.
const sharedYAML = `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: vertex }
workflows:
  web:
    checkout: baked
    image: { apt: [libvips-dev], tools: { node: "24.19.0" }, key_files: [patches] }
    commands: { build: npm run build, test: npm test }
  api:
    commands: { build: ./gradlew assemble, test: ./gradlew test }
`

// sharedInputs is a GitHub repository with one checkout: shared workflow
// (web) and one ordinarily-built workflow (api).
func sharedInputs(t *testing.T) Inputs {
	t.Helper()
	in := cloneInputs(t)
	in.Cfg = parseCfg(t, []byte(sharedYAML))
	web := in.Cfg.Workflows["web"]
	web.Checkout = config.CheckoutShared
	in.Cfg.Workflows["web"] = web
	return in
}

// TestSpecSharedWorkflowCarriesCloneMarkers (design
// forge-authorship-images/shared-images.md §1, §6): a shared job's image
// holds the dependency layer alone, so the run has to clone the source
// itself — the job says so in FUGARO_CHECKOUT, whose value is the
// workflow's own kind, and names where from in FUGARO_REMOTE. One writer
// writes both, the same code that writes them for a clone job, and a baked
// sibling's job carries neither: that is the whole of the job's clone
// shape, and the refresh that moves a shared repository's job onto the
// shared digest has nothing else to write.
//
// Mutation (run, restore): drop config.CheckoutShared from the writer's
// condition in workflow() — this test then fails: web's env has no
// FUGARO_CHECKOUT and no FUGARO_REMOTE.
func TestSpecSharedWorkflowCarriesCloneMarkers(t *testing.T) {
	rs, err := Repo(sharedInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	ws := rs.Workflows["web"]
	if ws.Checkout != config.CheckoutShared {
		t.Fatalf("web's Checkout = %q, want %q", ws.Checkout, config.CheckoutShared)
	}
	if got := ws.Env[CheckoutEnv]; got != config.CheckoutShared {
		t.Errorf("web's %s = %q, want %q: a shared job's image holds no source, so the run clones", CheckoutEnv, got, config.CheckoutShared)
	}
	if got := ws.Env[RemoteEnv]; got != "https://github.com/acme/webapp.git" {
		t.Errorf("web's %s = %q, want the repository's clone URL", RemoteEnv, got)
	}
	for _, env := range []string{CheckoutEnv, RemoteEnv} {
		if _, ok := rs.Workflows["api"].Env[env]; ok {
			t.Errorf("api (baked) carries %s = %q, want no such key", env, rs.Workflows["api"].Env[env])
		}
	}
}

// TestSpecSharedRemoteIsCredentialFree (design
// forge-authorship-images/shared-images.md §6, review mandate): the remote a
// shared job clones from is the repository's own https, credential-free
// clone URL — never a token-bearing one — because it is written by the same
// repoURL-checked value a clone job's is, and a URL carrying credentials is
// refused outright, before any job spec exists. A shared workflow is the
// case this matters most for: nothing else in its job names a repository,
// so the remote is the one thing that decides whose source the run gets.
func TestSpecSharedRemoteIsCredentialFree(t *testing.T) {
	in := sharedInputs(t)
	in.RepoURL = "https://person-1:token-shaped@github.com/acme/webapp.git"
	if _, err := Repo(in); err == nil {
		t.Fatal("Repo accepted a clone URL carrying credentials for a checkout: shared workflow")
	}

	rs, err := Repo(sharedInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	remote := rs.Workflows["web"].Env[RemoteEnv]
	if !strings.HasPrefix(remote, "https://") {
		t.Errorf("web's %s = %q, want an https URL", RemoteEnv, remote)
	}
	if strings.Contains(remote, "@") || strings.Contains(remote, "person-1") {
		t.Errorf("web's %s = %q, want the repository's own URL with no credential in it", RemoteEnv, remote)
	}
}

// TestNeedsBuildForSharedCheckout: a checkout: shared workflow does need a
// build — the dependency-layer image its job runs, shared with every
// repository on the same key (design
// forge-authorship-images/shared-images.md §6) — so NeedsBuild reports it,
// where the clone workflow beside it is skipped. Before this run a shared
// workflow did not parse at all, so the case had no answer; a refactor of
// the skip guard to `Checkout != config.CheckoutBaked` would answer it
// wrongly and silently.
func TestNeedsBuildForSharedCheckout(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	rs, err := Repo(sharedInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.AddBucket(rs.Installation.RunsBucket, testProjectNumber, managed)
	got, err := NeedsBuild(ctx, f.c, rs, map[string]bool{"github-app-key": true})
	if err != nil || !slices.Equal(got, []string{"api", "web"}) {
		t.Fatalf("got %q, %v, want both api and web: a shared workflow needs its dependency-layer image built", got, err)
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

// TestWorkflowJobTaskTimeoutFollowsTheTimeoutDefault: the job's Cloud Run task
// timeout is the workflow's own timeouts.total plus the slack every backend
// adds, derived here on every `fugaro update` from the config the CLI resolves
// — it is never stored in the installation. So an installation deployed when
// the default total was 90 minutes converges to the new one on the next
// `fugaro update` alone, with no operator step: the api workflow below names no
// timeouts (the previous default's shape, and the shape a repository written
// before this release has), and what comes out is the current default plus the
// slack. The previous default's own number is named so that a change to either
// half fails here rather than silently leaving deployed jobs on the old clock.
func TestWorkflowJobTaskTimeoutFollowsTheTimeoutDefault(t *testing.T) {
	in := webappInputs(t)
	// webappYAML's api workflow writes no timeouts, so what it resolves to is
	// the engine default — the shape a repository written before this
	// release's default change has, with its fugaro.yaml untouched by it.
	if got := in.Cfg.Workflows["api"].Timeouts.Total.Duration; got != 3*time.Hour {
		t.Fatalf("the fixture's api workflow resolves total to %s, not the 3h default: this test is about a workflow left at the default", got)
	}
	ws, err := Workflow(in, "api")
	if err != nil {
		t.Fatal(err)
	}
	want := int((3*time.Hour + backend.TaskTimeoutSlack) / time.Second)
	if ws.TaskTimeoutS != want {
		t.Errorf("task_timeout_s = %d, want %d (the 3h default plus the %s slack): a repository left at the default does not converge with `fugaro update` alone",
			ws.TaskTimeoutS, want, backend.TaskTimeoutSlack)
	}
	if prev := int((90*time.Minute + backend.TaskTimeoutSlack) / time.Second); ws.TaskTimeoutS == prev {
		t.Errorf("task_timeout_s is still %d, the previous 90-minute default's job timeout: `fugaro update` would leave deployed jobs on the old clock", prev)
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
	want := []string{"adopt_legacy_registry", "alert_email", "bucket_lifecycle", "budget", "enable_vertex", "fugaro_project", "launcher_accounts", "launcher_bucket_condition", "launchers", "log_bucket_description", "log_bucket_location", "manage_apis",
		"names", "operators", "project", "region", "registry_cleanup", "reports", "runs_bucket", "state_bucket"}
	if got := slices.Sorted(maps.Keys(doc)); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	// The log bucket's description is its ownership mark, since it can
	// carry no labels.
	if doc["log_bucket_description"] != LogBucketDescription || !strings.Contains(LogBucketDescription, "fugaro") {
		t.Errorf("log_bucket_description = %v", doc["log_bucket_description"])
	}
	// And where the bucket a NEW installation creates is: the mapping's
	// answer for the region, which for us-east5 is the US multi-region (a
	// region Cloud Logging offers no bucket in, LP6, D3082).
	if doc["log_bucket_location"] != place.For(s.Region).LogBucket.Where {
		t.Errorf("log_bucket_location = %v, want %v", doc["log_bucket_location"], place.For(s.Region).LogBucket.Where)
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
// it, not let the panic through to fugaro update.
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
// authenticates its agent through Vertex (fugaro update records it), and
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
// fugaro update records in the local config.
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
// (localcfg's RunsBucketName), so the first build fugaro update submits,
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
// clamped to (M9a.1; M9a dropped them when the mode was off). The run that
// reads this environment is accounted for — from AB1a on a job that names no
// mode is observe (D3009, R3), and its price table is built whatever the mode
// is — with the owner's cap as the ceiling it runs on.
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
				if s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok }); err != nil || !s.On() || s.Cap != 5_000_000 || s.Prices == nil {
					t.Errorf("%s: SpendFromEnv = %+v, %v; want the run accounted for on the owner's cap", w.Name, s, err)
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
// (backend.LaunchSpec.Env) and never through fugaro update's deploy-time
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
// may exist and a run must be able to read it — and it names no mode. From
// AB1a on the run that reads this environment is accounted for whatever the
// mode says (D3009, R3): a job whose mode entry is missing is observe, so the
// word an older config wrote no longer turns a run's accounting off, and what
// the block names is the backend and, where it does, the cap.
func TestBudgetEnvNamesTheBackendWhateverTheMode(t *testing.T) {
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
			if s, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok }); err != nil || !s.On() {
				t.Errorf("%s: %s: SpendFromEnv = %+v, %v; want the run accounted for", name, w.Name, s, err)
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

// TestRepoSpecBaseBranchPrecedence: the spec's base branch is the one
// precedence every layer resolves through (config.EffectiveBaseBranch) — the
// checkout's fugaro.yaml when the file writes git.base_branch, else the local
// config's repos.<r>.base_branch, else the project's own base_branch, else
// main. FileSetsBaseBranch is that first question, which a resolved config
// cannot answer: a caller that did not read the file says false, and the
// layers below the file then win.
func TestRepoSpecBaseBranchPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, fileBranch string
		fileSets         bool
		entry, project   string
		want             string
	}{
		{"the file's own key beats the local entry", "develop", true, "master", "release", "develop"},
		{"a caller that cannot tell keeps the local entry", "develop", false, "master", "release", "master"},
		{"the project's default when neither names one", "main", false, "", "release", "release"},
		{"main when nothing names one", "main", false, "", "", "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sandboxInputs(t, m5Additions)
			in.FileSetsBaseBranch = &tc.fileSets
			r := in.LC.Repos["acme/sandbox"]
			r.BaseBranch = tc.entry
			in.LC.Repos["acme/sandbox"] = r
			in.LC.BaseBranch = tc.project
			cfg := *in.Cfg
			in.Cfg = &cfg
			in.Cfg.Git.BaseBranch = tc.fileBranch
			rs, err := Repo(in)
			if err != nil {
				t.Fatal(err)
			}
			if rs.BaseBranch != tc.want {
				t.Fatalf("base branch %q, want %q", rs.BaseBranch, tc.want)
			}
			var spec CheckJobSpec
			if err := json.Unmarshal([]byte(rs.Check.Env[CheckSpecEnv]), &spec); err != nil {
				t.Fatal(err)
			}
			if spec.BaseBranch != tc.want {
				t.Errorf("check spec base branch %q, want %q", spec.BaseBranch, tc.want)
			}
		})
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
// produced before this task (design docs/named-oauth-tokens/spec.md §5).
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

// An account gets a job variant only where a key that selects it exists: under
// oauth every account config.AllowedClaudeAccounts admits, and under api-key
// only the review or adversarial account the B0 mix rule admits (B1, B2). An
// api-key or vertex repository that an account merely lists in allow_data_to
// — no stage key names it — still gets exactly the one default job per
// workflow, which is the allow-list's own backstop: being allowed data is not
// being chosen.
func TestRepoSpecHasNoVariantForAnUnchosenAccount(t *testing.T) {
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

// TestProjectSecretContainerIsDerivedFromTheEntry: the plan creates the
// project container for a workflow_secrets entry exactly as it does for a
// project_secrets one -- a non-empty allow_data_to and nothing else, D1263 --
// so a container Terraform creates and one the store path created first (F16)
// are the same container.
func TestProjectSecretContainerIsDerivedFromTheEntry(t *testing.T) {
	for _, tt := range []struct {
		name string
		lc   string
		want []string
	}{
		{"entry with a list", "workflow_secrets:\n  sandbox-probe: { allow_data_to: [acme/sandbox] }\n", []string{"sandbox-probe"}},
		{"entry with an empty list", "workflow_secrets:\n  sandbox-probe: { allow_data_to: [] }\n", nil},
	} {
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

// --- RepoSpec.Addresses (plan docs/config-transitions/implementation-plan.md Task 1)

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
	lc := parseLC(t, repoRootLC)
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

// repoRootLC is repoRootInputs' local config, named so that a fixture can
// build on it (the credentials fixture appends a model provider).
const repoRootLC = `version: 1
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
`

// repoRootAPIKeyLC is repoRootLC with one model provider allowed to send this
// repository's code to it, so an api-key workflow mounts that provider's key
// beside the Anthropic one.
const repoRootAPIKeyLC = repoRootLC + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/webapp"]
`

// repoRootMixedLC is repoRootAPIKeyLC with the one stage key the owner's B0
// ruling admits (docs/mixed-credentials/spec.md §11): the review stage's
// Claude account, whose allow_data_to admits this repository. The coder's own
// stages are untouched, so they keep the provider key the gateway holds.
const repoRootMixedLC = repoRootAPIKeyLC + `stages:
  review: { claude_account: bmb }
`

// repoRootCascadeAgent is the one agent block the credentials service serves
// (config.AuthCascade, CredentialsServiceServes): the repository a switch-on
// installation may turn onto the service at all.
const repoRootCascadeAgent = `agent: { auth: cascade }`

// serviceReady records on the local config's entry for repo what
// `fugaro update` writes when the bundle it read holds a model credential
// (localcfg.Repo.CredentialsServiceReady, internal/cli/init.go's
// writeRepoConfig). It is the half of the switch a spec build cannot answer
// for itself: internal/infra reads no cloud. A RECORD is what it writes and not
// an absent one, which is the distinction the field's pointer makes: the switch
// reads both as off (localcfg.Repo.CredentialsServiceOn), while MergeShared
// carries a published record over a missing one.
func serviceReady(t *testing.T, lc *localcfg.Config, repo string) {
	t.Helper()
	r, ok := lc.Repos[repo]
	if !ok {
		t.Fatalf("the fixture's local config has no repos.%s entry: %v", repo, slices.Sorted(maps.Keys(lc.Repos)))
	}
	r.CredentialsServiceReady = localcfg.RecordSwitch(true)
	lc.Repos[repo] = r
}

// converged plans one repository's spec twice: as an installation that has not
// deployed the credentials service leaves it (off — the block is absent
// altogether) and as one that has converged on DON1 (on — the block written,
// and the per-repository record the update read out of the bundle). The pair
// is the whole of what DON1's own change is, which is why every caller asserts
// what it moves for the agent.auth it spells: the switch is installed &&
// ready && (auth == config.AuthCascade || the auth is one of
// config.ServiceServedAuths && the published mode is observe or enforce)
// (CredentialsServiceServes), so this pair — which publishes no policy, and so
// carries no mode — may not move a repository of any other auth at all: the
// finding the switch was written for (docs/credentials-service/default-on.md
// §1.4). A mode that IS published moves the flip list's two auths, which is
// what repoRootServed and the tests below are for.
func converged(t *testing.T, in Inputs, lc *localcfg.Config) (off, on RepoSpec) {
	t.Helper()
	in.LC = lc
	offIn := in
	offIn.Installation.CredentialsService = nil
	var err error
	if off, err = Repo(offIn); err != nil {
		t.Fatal(err)
	}
	in.Installation.CredentialsService = credentialsService()
	serviceReady(t, in.LC, in.Repo)
	if on, err = Repo(in); err != nil {
		t.Fatal(err)
	}
	return off, on
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

// The address set must follow the map of jobs Repo built rather than drift
// from it (Review Focus 3). The oauth fixture's local config defines one
// account, which under oauth is a variant; the same config under api-key
// names no stage for it, so the one job it has is the whole address set.
func TestRepoAddressesFollowTheVariantMap(t *testing.T) {
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

// repoRootFixture is one golden fixture: the agent block its spec is planned
// from, and whether the installation has opted in to the credentials service
// (D146, D147) — the flag that decides whether the job accounts keep their
// model-secret accessors and whether each job gains its invoker grant on the
// service.
type repoRootFixture struct {
	agent string
	// credentialsService sets the fixture's Installation.CredentialsService,
	// the installation output the repository root reads.
	credentialsService bool
	// credentialsReady writes the per-repository record `fugaro update` leaves
	// when the bundle holds a model credential (localcfg.Repo.
	// CredentialsServiceReady). It is the switch's second condition, and the
	// one internal/infra cannot read for itself; a fixture that sets the block
	// without it is a repository the service is deployed for and cannot serve.
	credentialsReady bool
	// catalogCheckServiceAccount sets the fixture's
	// Installation.CatalogCheckServiceAccount, the second installation output
	// a repository root reads: the project's catalog check job's account
	// (D1148), which the root grants actAs on its own build account.
	catalogCheckServiceAccount string
	// lc replaces the fixture's local config when the fixture's own shape
	// lives there rather than in the agent block — the stage account B2's mix
	// names is a local-config key, never a published one.
	lc string
	// mode publishes a project policy with this budget mode for the fixture
	// (projectpolicy.Policy), the switch's fourth condition: the flip needs
	// `observe` or `enforce`, so a fixture that has the block and the record
	// and publishes no mode is a repository the service is deployed for and
	// does not serve (D2071). Empty leaves the fixture with no policy at all,
	// which is every other one.
	mode string
}

// repoRootFixtures are the fixture specs, keyed by the golden's name.
// A fourth is how the guard grows: add the key here with the fixture it
// plans from, add testdata/<name>.tfvars.json (TestRepoRootFixtureTfvars
// writes it under -update), then on a machine with terraform run
// `go test -tags terraform ./internal/infra -run TestRealRepoRootAddresses
// -update` to write testdata/<name>_addresses.golden. CI (untagged) then
// compares Addresses() against it on every run. Every address the modules
// declare is named by Addresses() and by every golden: #362's
// module.repo.google_artifact_registry_repository_iam_member.build_ghcr was
// the last one the untagged guard caught as missing, and naming it in
// Addresses() and regenerating the goldens from the real plan is what closed
// that gap.
//
// repo_root_credentials is the switched-on fixture: a cascade repository of an
// installation whose credentials service is deployed and whose bundle holds a
// model credential (the record), so the one address the mirror adds for it —
// the job's invoker grant on the service — is compared with a real plan.
// Before it, google_cloud_run_v2_service_iam_member.credentials was a
// conditionalResources hole and the invoker grant was pinned by nothing at all
// (independent security review of the implementation, m2). The cascade is the
// switch's own condition, not a preference: the drop of the job's accessors on
// the model credentials happens only for a repository the service serves, and
// a cascade job mounts none of them (D143, CredentialsServiceServes).
var repoRootFixtures = map[string]repoRootFixture{
	"repo_root":        {agent: repoRootOAuthAgent},
	"repo_root_vertex": {agent: repoRootVertexAgent},
	// Its local config carries the service's address as well, which is what
	// `fugaro update` leaves on a machine whose installation has one: every
	// job that reaches the service — the check job now included (B4.9) —
	// learns the address there and nowhere else (localcfg, and providers.
	// EnvCredentialsServiceURL on the job).
	"repo_root_credentials": {agent: repoRootCascadeAgent, credentialsService: true, credentialsReady: true, lc: repoRootLC + credentialsServiceLCBlock},
	// repo_root_catalog_check is the D1148 fixture: the same oauth spec with
	// the installation's catalog check job account set, so the one address the
	// mirror adds (the actAs on the build account, modules/repo/catalogcheck.tf)
	// is compared with a real plan rather than trusted.
	"repo_root_catalog_check": {agent: repoRootOAuthAgent, catalogCheckServiceAccount: "fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"},
	// repo_root_mixed is B2's fixture: an api-key spec whose local config
	// names the one stage account the owner's B0 ruling admits, so a real plan
	// holds the job variant that account gets, the token it mounts and the
	// accessor beside them (docs/mixed-credentials/spec.md §11). No other
	// fixture has an api-key job at all.
	"repo_root_mixed": {agent: `agent: { auth: api-key }`, lc: repoRootMixedLC},
	// repo_root_mixed_served is the mixed fixture SWITCHED: the same api-key
	// repository with its stage account, plus the credentials service in the
	// installation's outputs, the record `fugaro update` writes and a
	// published mode — the switch's four conditions, so
	// ModelSecretsServed answers `anthropic` and the drop runs. It is a
	// fixture of its own because a golden is generated from a real plan and
	// RepoSpec carries no way to ask the generator for the ON state of
	// another fixture: repoRootServed builds that state for the Go tests,
	// and only a fixture in this map reaches TestRealRepoRootAddresses. What
	// it pins is the pair of rules the flip turns into one — the coder job's
	// two Claude mounts and their accessors go, and the stage account's own
	// job keeps the account's token and the accessor beside it, because that
	// token is the stage's and its stages go straight to Anthropic (B0's
	// ruling, docs/mixed-credentials/spec.md §11; C1.20).
	"repo_root_mixed_served": {agent: `agent: { auth: api-key }`, lc: repoRootMixedLC + credentialsServiceLCBlock, credentialsService: true, credentialsReady: true, mode: config.BudgetEnforce},
}

// repoRootFixtureSpec builds one fixture's spec by name, the opt-in
// included.
func repoRootFixtureSpec(t *testing.T, name string) RepoSpec {
	t.Helper()
	f, ok := repoRootFixtures[name]
	if !ok {
		t.Fatalf("no fixture named %q", name)
	}
	in := repoRootInputs(t, f.agent)
	if f.credentialsService {
		in.Installation.CredentialsService = credentialsService()
	}
	if f.catalogCheckServiceAccount != "" {
		in.Installation.CatalogCheckServiceAccount = f.catalogCheckServiceAccount
	}
	if f.lc != "" {
		in.LC = parseLC(t, f.lc)
	}
	if f.credentialsReady {
		serviceReady(t, in.LC, in.Repo)
	}
	if f.mode != "" {
		in.Policy = &projectpolicy.Policy{Budget: projectpolicy.Budget{Mode: f.mode}}
	}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// TestRepoRootFixtureTfvars keeps the committed tfvars fixtures and the Go
// fixture that plans from them in step: the golden address list belongs to
// exactly the spec these tfvars encode, so a fixture edit without a
// regeneration fails here rather than silently changing what the golden
// pins.
func TestRepoRootFixtureTfvars(t *testing.T) {
	for name := range repoRootFixtures {
		spec := repoRootFixtureSpec(t, name)
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
	spec := repoRootFixtureSpec(t, "repo_root")
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
	spec := repoRootFixtureSpec(t, "repo_root_vertex")
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

// The opt-in fixture pins the other two branches of the address set: with the
// installation's credentials service set, the job accounts keep no accessor on
// a model credential (the oauth fixture mounts claude-oauth-token twice, once
// per account) and each gains roles/run.invoker on the service. Both are
// compared with a real plan of the modules, so a change in modules/workflow
// that the mirror does not follow fails here (review m2: the invoker grant was
// a conditionalResources hole and the drop was pinned by nothing).
func TestRepoAddressesMatchTheCredentialsGolden(t *testing.T) {
	spec := repoRootFixtureSpec(t, "repo_root_credentials")
	want := addressesGolden(t, "repo_root_credentials")
	if len(want) == 0 {
		t.Fatal("the golden is empty")
	}
	got := spec.Addresses()
	if !slices.Equal(got, want) {
		t.Errorf("Addresses() differs from repo_root_credentials_addresses.golden:\nmissing: %v\nunexpected: %v",
			missingFrom(want, got), missingFrom(got, want))
	}
}

// The mixed fixture pins B2's address set against a real plan: the api-key
// repository that names a stage account has two jobs, and the account's —
// the variant whose name carries the account's own token — is the only one
// that reaches a Claude credential. That is the whole shape of the ruling:
// the run's review and adversarial stages are on the account, and no stage
// of the coder's own job is (docs/mixed-credentials/spec.md §11).
func TestRepoAddressesMatchTheMixedGolden(t *testing.T) {
	const (
		account = "bmb"
		token   = "claude-oauth-token-" + account
		mixed   = `module.repo.module.workflow["web_` + account + `"].`
		coder   = `module.repo.module.workflow["web"].`
	)
	spec := repoRootFixtureSpec(t, "repo_root_mixed")
	want := addressesGolden(t, "repo_root_mixed")
	if len(want) == 0 {
		t.Fatal("the golden is empty")
	}
	got := spec.Addresses()
	if !slices.Equal(got, want) {
		t.Errorf("Addresses() differs from repo_root_mixed_addresses.golden:\nmissing: %v\nunexpected: %v",
			missingFrom(want, got), missingFrom(got, want))
	}
	for _, a := range []string{
		mixed + "google_cloud_run_v2_job.this[0]",
		mixed + `google_secret_manager_secret_iam_member.job["` + token + `"]`,
		coder + `google_secret_manager_secret_iam_member.job["anthropic-api-key"]`,
		coder + `google_secret_manager_secret_iam_member.job["openrouter-api-key"]`,
	} {
		if !slices.Contains(want, a) {
			t.Errorf("the plan of the mixed fixture does not hold %s", a)
		}
	}
	for _, a := range want {
		if strings.HasPrefix(a, coder) && strings.Contains(a, "claude-oauth-token") {
			t.Errorf("the coder's own job reaches a Claude token: %s", a)
		}
	}
}

// TestRepoAddressesMatchTheMixedServedGolden pins the SWITCHED mixed
// repository's address set against a real plan: the state
// TestRepoAddressesMatchTheMixedGolden cannot see, because that fixture plans
// with the credentials service off. The flip's two halves are both in the
// addresses, and this is what holds the module's sa.tf to Go's drop: the
// coder job keeps no accessor on either Claude credential
// (is_run_model_secret), and the stage account's job keeps the one on the
// account's own token (is_stage_account_token) while losing the Anthropic
// key's — the exemption the module cannot derive from its inputs and Go
// therefore passes down as stage_account.
func TestRepoAddressesMatchTheMixedServedGolden(t *testing.T) {
	const (
		account = "bmb"
		token   = "claude-oauth-token-" + account
		stages  = `module.repo.module.workflow["web_` + account + `"].`
		coder   = `module.repo.module.workflow["web"].`
	)
	spec := repoRootFixtureSpec(t, "repo_root_mixed_served")
	if spec.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Fatalf("the fixture plans model_secrets_served = %q, want %q: the golden would pin the unswitched state", spec.ModelSecretsServed, config.ModelSecretServedAnthropic)
	}
	want := addressesGolden(t, "repo_root_mixed_served")
	if len(want) == 0 {
		t.Fatal("the golden is empty")
	}
	got := spec.Addresses()
	if !slices.Equal(got, want) {
		t.Errorf("Addresses() differs from repo_root_mixed_served_addresses.golden:\nmissing: %v\nunexpected: %v",
			missingFrom(want, got), missingFrom(got, want))
	}
	if a := stages + `google_secret_manager_secret_iam_member.job["` + token + `"]`; !slices.Contains(want, a) {
		t.Errorf("the switched plan does not hold %s: the account's own job still mounts its token, so it keeps the accessor beside it (C1.20)", a)
	}
	for _, a := range want {
		if !strings.Contains(a, ".google_secret_manager_secret_iam_member.job[") {
			continue
		}
		if strings.HasPrefix(a, coder) && (strings.Contains(a, "claude-oauth-token") || strings.Contains(a, "anthropic-api-key")) {
			t.Errorf("the coder's own job keeps an accessor on a Claude credential: %s", a)
		}
		if strings.HasPrefix(a, stages) && strings.Contains(a, "anthropic-api-key") {
			t.Errorf("the stage account's job keeps the accessor on the Anthropic key: %s", a)
		}
	}
}

// TestSwitchedMixedRepositoryKeepsItsStageAccountToken is B2.7's flip on the
// one repository that mixes credentials (B0's ruling of 2026-11-04,
// docs/mixed-credentials/spec.md §11): switched ON, an api-key repository
// with a stage account gives up the two Claude mounts of its CODER job, and
// the account's own variant job gives up its Anthropic key while keeping the
// account's OAuth token.
//
// That token is the STAGE's credential, not the run's: `review`,
// `adversarial` and `triage` go out on that job and the RUNNER hands the
// account's token straight to Anthropic, so the run's gateway never sees those
// calls and the credentials service has no call of theirs to serve. Taking the
// mount would leave the stage reading a variable nothing mounted and halt
// every run of such a repository at its review stage — the failure the mixed
// account's own job exists to avoid — which is why the exemption is Go's
// (dropModelMounts' anthropic arm) AND the module's (sa.tf's
// is_stage_account_token): the mount and the account's accessor are one rule,
// because a job whose account keeps an accessor its container no longer mounts
// is a job Cloud Run refuses to create (C1.20).
//
// It is the test the mixed fixture could not be: repoRootFixtures'
// repo_root_mixed plans with the credentials service OFF, so
// TestRepoAddressesMatchTheMixedGolden pins the token's accessor only in the
// state where nothing drops, and nothing pinned the switched one.
func TestSwitchedMixedRepositoryKeepsItsStageAccountToken(t *testing.T) {
	const (
		account = "bmb"
		token   = "claude-oauth-token-" + account
		agent   = `agent: { auth: api-key }`
	)
	stages, coder := "web_"+account, "web"
	offIn := repoRootInputs(t, agent)
	offIn.LC = parseLC(t, repoRootMixedLC)
	off, err := Repo(offIn)
	if err != nil {
		t.Fatal(err)
	}
	on := repoRootServed(t, agent, repoRootMixedLC, config.BudgetEnforce)
	if on.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Fatalf("the switched mixed repository plans model_secrets_served = %q, want %q: the test would be vacuous", on.ModelSecretsServed, config.ModelSecretServedAnthropic)
	}
	for _, key := range []string{stages, coder} {
		if _, ok := on.Workflows[key]; !ok {
			t.Fatalf("the switched spec has no %s job: %v", key, slices.Sorted(maps.Keys(on.Workflows)))
		}
	}
	if !on.Workflows[stages].StageAccount {
		t.Errorf("the %s job's StageAccount is false: it is the mixed stage account's own variant job, and the module reads exactly that field to leave the stage's token where the flip takes the run's mounts away (sa.tf's is_stage_account_token)", stages)
	}
	if on.Workflows[coder].StageAccount {
		t.Errorf("the %s job's StageAccount is true: only the stage account's own variant job is exempt from the drop", coder)
	}
	// The account's job: the Anthropic key is the RUN's credential and goes,
	// the account's own token is the STAGE's and stays, and a provider key was
	// never in any arm's reach (R6, D1634).
	want := append(withoutClaudeMounts(modelMountsOf(off.Workflows[stages])), "CLAUDE_CODE_OAUTH_TOKEN="+token)
	slices.Sort(want)
	if got := modelMountsOf(on.Workflows[stages]); !slices.Equal(got, want) {
		t.Errorf("the switched repository's %s job mounts %v, want %v: the service serves it the Anthropic key, while the account's token is what its stages read straight from the job (B0's mix, D2072, R6)", stages, got, want)
	}
	// The coder's job: both Claude credentials are the run's, so both go.
	if got, want := modelMountsOf(on.Workflows[coder]), withoutClaudeMounts(modelMountsOf(off.Workflows[coder])); !slices.Equal(got, want) {
		t.Errorf("the switched repository's %s job mounts %v, want %v: the service serves it both Claude credentials, so those two mounts go and every other one stays (D2072)", coder, got, want)
	}
	// The redaction list follows the mounts, and a mounted credential left off
	// it is one the runner would print.
	for _, key := range []string{stages, coder} {
		if got, want := on.Workflows[key].Env[runner.SecretEnvsVar], redactionEnvs(on.Workflows[key].SecretEnv); got != want {
			t.Errorf("the %s job's %s = %q, want %q: the list follows the mounts it has left", key, runner.SecretEnvsVar, got, want)
		}
	}
	if got := on.Workflows[stages].Env[runner.SecretEnvsVar]; !strings.Contains(got, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("the %s job's %s = %q does not name the account's token: that job still mounts it, so the runner has to redact it", stages, runner.SecretEnvsVar, got)
	}
	if got := on.Workflows[coder].Env[runner.SecretEnvsVar]; strings.Contains(got, "CLAUDE_CODE_OAUTH_TOKEN") || strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("the %s job's %s = %q still names a Claude credential: both of its mounts are gone (D2072)", coder, runner.SecretEnvsVar, got)
	}
	// The containers do not move — a dropped one carries prevent_destroy and
	// would plan a destruction — and the accessors follow the mounts: the
	// account's job keeps its grant on the token, and no job keeps one on the
	// Anthropic key (the module's half of the same rule, C1.20).
	if !maps.Equal(off.Secrets, on.Secrets) {
		t.Errorf("the secret containers moved with the switch: %v, want %v", on.Secrets, off.Secrets)
	}
	addrs := on.Addresses()
	if len(addrs) == 0 {
		t.Fatal("the switched spec produces no address at all: the test would be vacuous")
	}
	if a := `module.repo.module.workflow["` + stages + `"].google_secret_manager_secret_iam_member.job["` + token + `"]`; !slices.Contains(addrs, a) {
		t.Errorf("the switched plan does not hold %s: the account's own job must keep its accessor on the token it still mounts (C1.20)", a)
	}
	// The job's own grant, and not the container's the root declares or the
	// operators' pair: those are the root's for every repository and move with
	// nothing here.
	const jobGrant = ".google_secret_manager_secret_iam_member.job["
	for _, a := range addrs {
		if strings.Contains(a, jobGrant) && strings.Contains(a, "anthropic-api-key") {
			t.Errorf("the switched plan still holds %s: the service serves the Anthropic key to every job of this repository, so no account keeps an accessor on it (D2072, C1.20)", a)
		}
		if strings.Contains(a, jobGrant) && strings.Contains(a, "claude-oauth-token") && !strings.Contains(a, stages) {
			t.Errorf("the switched plan holds %s: only the stage account's own job keeps an accessor on a Claude OAuth token (B0's mix)", a)
		}
	}
}

// TestRepoAddressesFollowTheSwitch is the mirror's own guard, and it needs no
// golden: it plans each fixture twice — as an installation that has not
// deployed the credentials service leaves it, and as one that has converged on
// DON1 — and asserts the difference is exactly the two sets
// modules/workflow/sa.tf moves: the job's accessors on the credentials the
// service holds, gone, and the job's invoker grant on the service, added. The
// build account's own accessor on the App key is not the job's and does not
// move (D148, F3).
//
// WHICH repository may move at all is the switch's own answer, and it is what
// this test was rewritten for (docs/credentials-service/default-on.md §1.4 and
// §3, DON8). The module's gate is `var.credentials_service == null` and reads
// no auth: the repository root's own `installation.credentials_service` is the
// only thing standing between an oauth job and a plan that takes its Claude
// token away, and CredentialsServiceServes — the installation has the block,
// the bundle is ready as the local config records it, and agent.auth is
// config.AuthCascade — is what decides whether the root carries it. The rows
// below are the three auths, and only the cascade one moves when DON1's block
// appears:
//
//   - a cascade repository is switched on. Its job gains the invoker grant on
//     the service (D135) and nothing else: a cascade job mounts no model
//     credential in either state (SecretMounts' cascade case, D143), which is
//     the reason the drop is inert for the one auth the service serves, and
//     the assertion that no model accessor moves either way is what keeps the
//     drop from being read as a drop for every repository;
//   - an oauth job and an api-key job keep every accessor they have — the
//     Claude token once per account, the Anthropic key and the provider's key
//     — and gain no invoker grant. Their rows are the regression test for the
//     release blocker §1.4 states: a converged installation's oauth repository
//     that dropped its Claude token, while the service has no route that would
//     serve it one, is a repository whose every run fails.
//
// The App key's rows are the gated half (review finding F1, 2026-10-21): with
// the runner still reading the key — this branch, config.ServiceHeldSecretNames
// empty — the switch takes only the model accessors away, because a job whose
// account lost its accessor on a key its container still mounted is a job Cloud
// Run refuses to create; and with the name in that list (the state C2.2's
// release puts the tree in) the App key's accessor goes with them. Both states
// are asserted against the same Run: the mirror and the module are held to each
// other by deploy/terraform's TestServiceHeldSecretsMatchTheCredentialsService,
// so a module that dropped the accessor while the list was empty would be a
// plan Go's own address list disagrees with.
//
// It then runs the same two address lists through Retirements, both
// directions, because that is what the drop is FOR: the state an installation
// applied with the service off must be read as exactly those accessors to
// retire, and the state of one that turns the switch off again as exactly the
// invoker grants — never a job, never a secret container. A held-off
// repository's two states must retire nothing at all, which is the other half
// of the same finding: an update that reported a delete for a repository it did
// not switch on would be asking the operator to allow one. With the mirror's
// branch disabled every test stayed green (independent security review of the
// implementation, m2), which is what this pins.
func TestRepoAddressesFollowTheSwitch(t *testing.T) {
	accessor := func(workflow, logical string) string {
		return `module.repo.module.workflow["` + workflow + `"].google_secret_manager_secret_iam_member.job["` + logical + `"]`
	}
	appKey := func(workflows ...string) []string {
		var out []string
		for _, key := range workflows {
			out = append(out, accessor(key, "github-app-key"))
		}
		return out
	}
	invoker := func(workflows ...string) []string {
		var out []string
		for _, key := range workflows {
			out = append(out, `module.repo.module.workflow["`+key+`"].google_cloud_run_v2_service_iam_member.credentials[0]`)
		}
		return out
	}
	// serviceAccessor is D2074's grant: the service's own accessor on one
	// container the workflow's job mounts.
	serviceAccessor := func(workflow, logical string) string {
		return `module.repo.module.workflow["` + workflow + `"].google_secret_manager_secret_iam_member.service["` + logical + `"]`
	}
	// buildInvoker is B4.4's grant: the repository's build account may invoke
	// the service too, and it is one address for the module rather than one
	// per workflow, because a repository has one build account.
	buildInvoker := []string{"module.repo.google_cloud_run_v2_service_iam_member.build_credentials[0]"}
	// buildSecret is the build account's own accessor on one logical secret
	// (modules/repo/build.tf), the list B4.9's flip empties for a served
	// repository: its build names no secret at all any more.
	buildSecret := func(logical string) string {
		return `module.repo.google_secret_manager_secret_iam_member.build["` + logical + `"]`
	}
	for _, tc := range []struct {
		name string
		in   Inputs
		lc   *localcfg.Config
		// holds are the addresses the repository really produces with no block
		// at all: the fixture's own premise, so a row that asserts nothing
		// moves cannot pass by producing nothing in the first place.
		holds []string
		// wantGone and wantAppKey are the two states of the drop: this branch,
		// and C2.2's release with the App key in ServiceHeldSecretNames.
		wantGone   []string
		wantAppKey []string
		// wantAdd is what the switch ARRIVES with in each of those two states,
		// and it is three sets of grants, not one: the job's invoker grant on
		// the service (D135), the build account's own invoker grant on it
		// (B4.4 — one address for the module, since a repository has one build
		// account), and the service's own accessor on each container the job
		// still mounts (D2074) — which is one fewer under C2.2's release,
		// because the App key's mount goes with its accessor (F1).
		wantAdd       []string
		wantAddAppKey []string
	}{
		{
			name: "cascade, the auth the service serves",
			in:   repoRootInputs(t, repoRootCascadeAgent),
			lc:   parseLC(t, repoRootLC),
			// The switch's own delta and the two fills meet in this row, and
			// every one of them is an accessor: the credential the job reads
			// stops being handed over by its own account and starts being
			// fetched for it (config.VaultFetchedSecretClasses, npm-token
			// here), while the service keeps its own accessor on that
			// container — that is the one D2074 grant the fill does not
			// narrow, and it is what lets the service hand the value over at
			// all. B4.9's fill is the other half (D2076): the build account
			// keeps no accessor either, because the build's own request names
			// none of these secrets and fetches them at the service.
			wantGone:      []string{accessor("web", "npm-token"), buildSecret("github-app-key"), buildSecret("npm-token")},
			wantAdd:       slices.Concat(invoker("web"), buildInvoker, []string{serviceAccessor("web", "github-app-key"), serviceAccessor("web", "npm-token")}),
			wantAddAppKey: slices.Concat(invoker("web"), buildInvoker, []string{serviceAccessor("web", "npm-token")}),
			wantAppKey:    appKey("web"),
		},
		{
			name:  "oauth",
			in:    repoRootInputs(t, repoRootOAuthAgent),
			lc:    parseLC(t, repoRootLC),
			holds: []string{accessor("web", "claude-oauth-token"), accessor("web_bmb", "claude-oauth-token-bmb")},
		},
		{
			name:  "api-key",
			in:    repoRootInputs(t, `agent: { auth: api-key }`),
			lc:    parseLC(t, repoRootAPIKeyLC),
			holds: []string{accessor("web", "anthropic-api-key"), accessor("web", "openrouter-api-key")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offSpec, onSpec := converged(t, tc.in, tc.lc)
			offAddrs := offSpec.Addresses()
			for _, a := range tc.holds {
				if !slices.Contains(offAddrs, a) {
					t.Fatalf("the fixture does not produce %s with no service at all: the test would be vacuous", a)
				}
			}
			// The row that does move has its own premise, in the other
			// direction: the invoker grant is not there yet, and neither is
			// any model accessor for the drop to take away with it.
			for _, a := range tc.wantAdd {
				if slices.Contains(offAddrs, a) {
					t.Fatalf("the fixture already produces %s with no service at all: there is no grant for the switch to add", a)
				}
			}
			if len(tc.wantAdd) > 0 {
				for _, a := range offAddrs {
					if strings.Contains(a, "claude-oauth-token") || strings.Contains(a, "anthropic-api-key") {
						t.Fatalf("the switched-on fixture's job holds %s with no service to serve it: the cascade mounts no model credential at all (D143), so this fixture is not the cascade the switch serves", a)
					}
				}
			}

			// check asserts the delta the switch makes in one state of the
			// service-held list, and both retirement directions over it.
			check := func(well string, wantGone, wantAdd []string) {
				t.Helper()
				onAddrs := onSpec.Addresses()
				var gone, added []string
				for _, a := range offAddrs {
					if !slices.Contains(onAddrs, a) {
						gone = append(gone, a)
					}
				}
				for _, a := range onAddrs {
					if !slices.Contains(offAddrs, a) {
						added = append(added, a)
					}
				}
				wantGone, wantAdd = slices.Clone(wantGone), slices.Clone(wantAdd)
				slices.Sort(wantGone)
				slices.Sort(wantAdd)
				if !slices.Equal(gone, wantGone) {
					t.Errorf("with the credentials service deployed (%s), Addresses() drops %v, want exactly %v: the module's gate is auth-blind (modules/workflow/sa.tf's var.credentials_service), so the repository root's own block — CredentialsServiceServes — is what decides a job may give an accessor up, and only a cascade job of a ready installation may", well, gone, wantGone)
				}
				if !slices.Equal(added, wantAdd) {
					t.Errorf("with the credentials service deployed (%s), Addresses() adds %v, want exactly %v: the job's invoker grant and the build account's own on the service (D135, B4.4), the service's accessor per mounted container (D2074), and nothing else", well, added, wantAdd)
				}
				// The retirement directions: what an installation that applied
				// with the service off must be told to retire when the switch
				// turns it on, and the other way round.
				rets := Retirements(offAddrs, onSpec)
				var retAddrs []string
				for _, r := range rets {
					retAddrs = append(retAddrs, r.Address)
				}
				slices.Sort(retAddrs)
				if !slices.Equal(retAddrs, wantGone) {
					t.Errorf("turning the switch on (%s) retires %v, want exactly %v: a retirement the mirror invents is a delete the operator is asked to allow, and for a repository the switch holds off there is nothing to retire at all", well, retAddrs, wantGone)
				}
				back := Retirements(onAddrs, offSpec)
				retAddrs = retAddrs[:0]
				for _, r := range back {
					retAddrs = append(retAddrs, r.Address)
				}
				slices.Sort(retAddrs)
				if !slices.Equal(retAddrs, wantAdd) {
					t.Errorf("turning the switch off (%s) retires %v, want exactly %v", well, retAddrs, wantAdd)
				}
				// And the grants that must never be a retirement candidate: the
				// job itself and the secret containers.
				for _, r := range slices.Concat(rets, back) {
					if strings.Contains(r.Address, "google_cloud_run_v2_job.this") || strings.Contains(r.Address, "google_secret_manager_secret.") {
						t.Errorf("the drop retires %s, which the service's arrival does not remove", r.Address)
					}
				}
			}

			// This branch: the job reads the App key, so the drop takes only
			// the model accessors away and the App key's stays (F1).
			check("the job reads the key", tc.wantGone, tc.wantAdd)
			// C2.2's release: the App key joins the list and its accessor goes
			// with the model ones, in the same plan.
			serviceHoldsTheAppKey(t)
			check("C2.2's release", slices.Concat(tc.wantGone, tc.wantAppKey), tc.wantAddAppKey)
		})
	}
}

// TestTheDeployAloneDropsNoModelMount is the mount half of the switch, and it
// is the regression test for the release blocker docs/credentials-service/
// default-on.md §1.4 states in as many words: the deploy alone must not take
// any repository's model mount away. The addresses test above pins which IAM
// records move; this one pins what the JOB is given, which is the half that
// decides whether a run has a credential at all: modules/workflow/sa.tf takes
// the job account's accessor off each model credential for every repository
// whose root carries the block, and a job that still MOUNTED the token while
// its account kept no accessor would be refused to deploy.
//
// The auth the module never reads is the point. DON1 writes the installation's
// block on every installation; the repository root's own
// `installation.credentials_service` — repoCredentialsService ->
// CredentialsServiceServes — is what keeps the block out of a repository the
// service does not serve, and that is the authorization condition. This
// fixture publishes no policy, so these two rows are repositories held off by
// the mode as well (B2.3, D2071), and their mounts stay because the switch's
// answer is `none` for a held-off repository whatever the flip list says:
// B2.7's flip takes the two Claude mounts from a SWITCHED `api-key` or `oauth`
// repository (ModelSecretsServed's `anthropic` arm), and this pair is not one.
//
// What each held-off row asserts, in the two forms the installations
// distinguish (oauth: the shared subscription token, once per Claude account;
// api-key: the Anthropic key and a provider's key):
//
//   - the MOUNT stays — the same SecretEnv entries name the same model
//     credentials (config.IsModelSecretEnv), and FUGARO_SECRET_ENVS, which is
//     how the job knows what to redact from its logs, goes on naming them;
//   - the CONTAINER stays — rs.Secrets is the same map in both states, which is
//     what keeps an apply from planning a secret's destruction and
//     Retirements from reporting a live container as retired;
//   - everything else is untouched: the git credential and the workflow's own
//     secrets stay mounted.
//
// The mutation is the auth half of CredentialsServiceServes (drop
// `auth == config.AuthCascade`): every clause below fails, and the address
// list grows the drop with it — which is the state DON1 alone would have
// shipped in.
func TestTheDeployAloneDropsNoModelMount(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Inputs
		lc   *localcfg.Config
	}{
		{name: "oauth", in: repoRootInputs(t, repoRootOAuthAgent), lc: parseLC(t, repoRootLC)},
		{name: "api-key", in: repoRootInputs(t, `agent: { auth: api-key }`), lc: parseLC(t, repoRootAPIKeyLC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offSpec, onSpec := converged(t, tc.in, tc.lc)
			// The fixture's premise: with the service off the job really does
			// mount a model credential, or the assertions below pass vacuously.
			mounted := func(ws WorkflowSpec) []string {
				var out []string
				for env, logical := range ws.SecretEnv {
					if config.IsModelSecretEnv(env) {
						out = append(out, env+"="+logical)
					}
				}
				slices.Sort(out)
				return out
			}
			offWS, onWS := offSpec.Workflows["web"], onSpec.Workflows["web"]
			offMounted := mounted(offWS)
			if len(offMounted) == 0 {
				t.Fatal("the fixture mounts no model credential with no service at all: the test would be vacuous")
			}
			if onMounted := mounted(onWS); !slices.Equal(onMounted, offMounted) {
				t.Errorf("the converged installation's job mounts %v, want exactly %v, the credentials it mounted before the service was deployed: the deploy alone changes no run — a repository the switch holds off plans model_secrets_served = none and keeps every mount (D2072, G17) — so a mount the deploy alone dropped is the release blocker docs/credentials-service/default-on.md §1.4 states, and the module's accessor drop would refuse to deploy it (D145, D146)", onMounted, offMounted)
			}
			// The redaction list follows the mounts: it is built from them, and
			// a name left out of it that the job holds is a credential the
			// runner would print.
			if got, want := onWS.Env[runner.SecretEnvsVar], redactionEnvs(onWS.SecretEnv); got != want {
				t.Errorf("%s = %q with the service deployed, want %q, the mounts it still gives the job", runner.SecretEnvsVar, got, want)
			}
			for _, env := range config.ModelSecretEnvs {
				if slices.ContainsFunc(offMounted, func(m string) bool { return strings.HasPrefix(m, env+"=") }) && !strings.Contains(onWS.Env[runner.SecretEnvsVar], env) {
					t.Errorf("%s no longer redacts %s, which the job still mounts", runner.SecretEnvsVar, env)
				}
			}
			// The containers: identical maps, so nothing plans a secret's
			// destruction and nothing is reported as retired.
			if !maps.Equal(offSpec.Secrets, onSpec.Secrets) {
				t.Errorf("the secret containers moved with the block: with no service %v, with one %v. Only a switched-on repository's mounts and accessors move, and a container carries prevent_destroy: dropping it would plan a destruction the root refuses and report a live container as retired", offSpec.Secrets, onSpec.Secrets)
			}
			if _, ok := onWS.SecretEnv["NPM_TOKEN"]; !ok {
				t.Errorf("the workflow's own secret is no longer mounted with the service deployed: %v", onWS.SecretEnv)
			}
			if _, ok := onWS.SecretEnv[config.ReservedSecrets["github-app-key"]]; !ok {
				t.Errorf("the git credential is no longer mounted with the service deployed: %v", onWS.SecretEnv)
			}
		})
	}
}

// TestNoRepositoryRootGainsAServiceTheBundleCannotServe is the whole-predicate
// guard docs/credentials-service/default-on.md §3 (DON8) names: no repository
// root the fixtures can build carries `installation.credentials_service` unless
// all three of CredentialsServiceServes' conditions hold.
//
// The two tests above each pin one half — which addresses move, and which
// mounts the job keeps — and each of them spells out the agent.auth it is
// about. This one is the guard against a fixture being the exception: it walks
// every agent block the tree knows against every state of the other two
// conditions, so a repository root that gained the block on the strength of the
// installation's own outputs alone (the mutation: `repoCredentialsService`
// returning the block regardless) is caught on a row no test named, and it
// asserts the ON row's shape as well, because a root that carried the block
// with the URL left in it would be a value the workflow module reads half of.
//
// The four states are the whole of what the three conditions can be, in the
// order an installation reaches them: nothing deployed (every installation
// before DON1); nothing deployed while a record says ready (a local config left
// behind by an installation that turned the service off again — the record is
// not the switch's authority on its own); the service deployed and its bundle
// holding no model credential, which is the state EVERY installation is in the
// moment its update deploys the service, because `fugaro creds set` is the
// bundle's only writer; and all three holding, which is the one row that may
// move, and only for the cascade.
func TestNoRepositoryRootGainsAServiceTheBundleCannotServe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent string
		lc    string
	}{
		{name: "cascade, the auth the service serves", agent: repoRootCascadeAgent, lc: repoRootLC},
		{name: "oauth", agent: repoRootOAuthAgent, lc: repoRootLC},
		{name: "api-key", agent: `agent: { auth: api-key }`, lc: repoRootAPIKeyLC},
		{name: "vertex", agent: repoRootVertexAgent, lc: repoRootLC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			served := tc.agent == repoRootCascadeAgent
			for _, st := range []struct {
				name      string
				installed bool
				ready     bool
			}{
				{name: "no service at all"},
				{name: "no service, a record that says ready", ready: true},
				{name: "the service deployed, the bundle not ready", installed: true},
				{name: "the service deployed, the bundle ready", installed: true, ready: true},
			} {
				in := repoRootInputs(t, tc.agent)
				in.LC = parseLC(t, tc.lc)
				if st.installed {
					in.Installation.CredentialsService = credentialsService()
				}
				if st.ready {
					serviceReady(t, in.LC, in.Repo)
				}
				rs, err := Repo(in)
				if err != nil {
					t.Fatal(err)
				}
				got := rs.Installation.CredentialsService
				if want := served && st.installed && st.ready; want != (got != nil) {
					if want {
						t.Fatalf("%s: the repository root carries no credentials service, want the installation's: the installation has the block, the record vouches for the bundle and agent.auth is %s — all three conditions — so this is the one row the switch turns on, and without it no repository of any installation ever reaches the service", st.name, config.AuthCascade)
					}
					t.Errorf("%s: the repository root carries installation.credentials_service %+v, want none: modules/workflow/sa.tf's gate is `var.credentials_service == null` and reads no auth, so this root's next apply would take the job's model credential away and grant its account roles/run.invoker on a service that cannot hand it back — the release blocker §1.4 states", st.name, got)
				}
				if got != nil && (got.Name != credentialsService().Name || got.Region != credentialsService().Region) {
					t.Errorf("%s: the repository root's credentials service is %+v, want name and region alone (%+v): a repository root reads those two and nothing else, and Terraform silently drops an attribute a variable's type does not carry rather than refusing it (repoCredentialsService)", st.name, got, credentialsService())
				}
			}
		})
	}
}

// TestCascadeMountsNoOAuthToken: a `cascade` workflow mounts no model
// credential in any case, the service on or off (docs/credentials-service,
// D143; C1.20). The cascade's first tier is the shared subscription OAuth
// token, and the whole point of the mode is that the job holds NEITHER tier —
// the service supplies one at call time through the run's gateway — so the
// SecretMounts switch has a `cascade` case of its own and maps it to no model
// mount at all. It is the one auth whose mounts do not depend on whether the
// installation opted in, which is why it is pinned separately from the drop
// above.
//
// The oauth fixture beside it is the premise: it mounts the same token, so a
// cascade case that fell through to oauth's would be caught here. The mutation
// is deleting the `case "cascade":` arm of SecretMounts' switch.
func TestCascadeMountsNoOAuthToken(t *testing.T) {
	w := config.Workflow{Secrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}}}
	mounts := func(auth string) []SecretMount {
		t.Helper()
		ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: auth}}, "app", config.DefaultClaudeAccount, w, &localcfg.Config{}, "acme/app")
		if err != nil {
			t.Fatal(err)
		}
		return ms
	}
	oauth := mounts("oauth")
	if !slices.ContainsFunc(oauth, func(m SecretMount) bool { return m.Logical == config.ClaudeAccountSecret(config.DefaultClaudeAccount) }) {
		t.Fatalf("the oauth mounts %v name no Claude token: the premise of this test is gone", oauth)
	}
	cascade := mounts(config.AuthCascade)
	var want []SecretMount
	for _, m := range oauth {
		if config.IsModelSecretEnv(m.Env) {
			continue
		}
		want = append(want, m)
	}
	if !slices.Equal(cascade, want) {
		t.Errorf("a cascade workflow mounts %v, want %v: the cascade's token is the service's, never the job's", cascade, want)
	}
	for _, m := range cascade {
		if config.IsModelSecretEnv(m.Env) {
			t.Errorf("a cascade workflow mounts %s (%s), a model credential: the cascade decides which credential carries a call and the service supplies it (D143)", m.Env, m.Logical)
		}
	}
	// And through the whole spec, where the job's env and its secret
	// containers are built from those mounts: the cascade token reaches
	// neither.
	rs := repoRootSpec(t, `agent: { auth: `+config.AuthCascade+` }`)
	ws := rs.Workflows["web"]
	for env, logical := range ws.SecretEnv {
		if config.IsModelSecretEnv(env) {
			t.Errorf("a cascade spec mounts %s=%s", env, logical)
		}
	}
	if _, ok := rs.Secrets[config.ClaudeAccountSecret(config.DefaultClaudeAccount)]; ok {
		t.Errorf("a cascade spec declares the token's container: %v", rs.Secrets)
	}
	if strings.Contains(ws.Env[runner.SecretEnvsVar], "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("%s still redacts the token: %q", runner.SecretEnvsVar, ws.Env[runner.SecretEnvsVar])
	}
}

// repoRootServed plans the repository root fixture of one agent block as the
// installation it is when the switch is ON for it: the credentials service in
// the installation's outputs, the record the update read out of the bundle, and
// a published node of the given budget mode. Everything else is the fixture's.
func repoRootServed(t *testing.T, agent, lc, mode string) RepoSpec {
	t.Helper()
	in := repoRootInputs(t, agent)
	in.LC = parseLC(t, lc)
	in.Installation.CredentialsService = credentialsService()
	serviceReady(t, in.LC, in.Repo)
	in.Policy = &projectpolicy.Policy{Budget: projectpolicy.Budget{Mode: mode}}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// modelMountsOf are a workflow spec's model credentials, as env=logical, which
// is the half of the drop that decides whether a run has a credential at all
// (the other half is the account's accessor, Readiness' Addresses).
func modelMountsOf(ws WorkflowSpec) []string {
	var out []string
	for env, logical := range ws.SecretEnv {
		if config.IsModelSecretEnv(env) {
			out = append(out, env+"="+logical)
		}
	}
	slices.Sort(out)
	return out
}

// claudeMountsOf are the two Claude mounts of a model-mount list: the pair the
// credentials service serves whole for an `api-key` or `oauth` repository — the
// Anthropic key and the OAuth token — and therefore the pair B2.7's flip takes
// away (config.ModelSecretServedAnthropic). A provider's key is deliberately not
// one of them: it is spent on a route the service forwards rather than attaches
// (R6, D1634).
func claudeMountsOf(mounts []string) []string {
	var out []string
	for _, m := range mounts {
		if env, _, _ := strings.Cut(m, "="); slices.Contains(config.ModelSecretEnvs, env) {
			out = append(out, m)
		}
	}
	return out
}

// withoutClaudeMounts is mounts with claudeMountsOf's entries taken out, which
// is what a flipped job's mounts must be: everything it mounted with no service
// at all, minus the two the service now serves it.
func withoutClaudeMounts(mounts []string) []string {
	var out []string
	for _, m := range mounts {
		if env, _, _ := strings.Cut(m, "="); slices.Contains(config.ModelSecretEnvs, env) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// TestServesAPIKeyAndOAuthWhenTheModeIsOn is B2.3's own answer (D2071, F30): an
// `api-key` or `oauth` repository of an installation whose published mode is
// `observe` or `enforce` is ON the switch, and one whose mode is off is not.
//
// The mode is the fourth condition and the reason it exists: the gateway is
// what reaches the service, and the gateway exists only in those two modes, so
// a repository on a mode-off project that was switched on would have its job
// handed an address its own runner refuses to use. The auth half is what this
// task adds: before it, the switch admitted the cascade alone, and every
// non-cascade repository kept its model credential with no address to the
// service at all.
//
// The spec half is what the switch's answer buys a repository root: the
// installation's block (the job's invoker grant and the address its launches
// are handed) and the second step of D2072, which this release carries — a
// model_secrets_served of `anthropic`, so the module drops the two Claude
// accessors and the job gives up those two mounts while every provider key it
// mounts stays (B2.7). The red step of the task was the first assertion below:
// with the auth half removed, `api-key` answered false and this repository root
// carried no block.
func TestServesAPIKeyAndOAuthWhenTheModeIsOn(t *testing.T) {
	for _, auth := range []string{config.AuthAPIKey, config.AuthOAuth} {
		for _, mode := range []string{config.BudgetObserve, config.BudgetEnforce} {
			if !CredentialsServiceServes(true, true, auth, mode) {
				t.Errorf("CredentialsServiceServes(installed, ready, %q, %q) = false, want true: the gateway that reaches the service runs in observe and enforce, so a repository on one of those modes is exactly what the service serves (D2071)", auth, mode)
			}
		}
	}

	for _, tc := range []struct{ name, agent, lc string }{
		{name: "oauth", agent: repoRootOAuthAgent, lc: repoRootLC},
		{name: "api-key", agent: `agent: { auth: api-key }`, lc: repoRootAPIKeyLC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offIn := repoRootInputs(t, tc.agent)
			offIn.LC = parseLC(t, tc.lc)
			off, err := Repo(offIn)
			if err != nil {
				t.Fatal(err)
			}
			on := repoRootServed(t, tc.agent, tc.lc, config.BudgetEnforce)

			if on.Installation.CredentialsService == nil {
				t.Fatalf("the repository root carries no credentials service with the mode in force: the installation has the block, the record vouches for the bundle and agent.auth is %s, and D2071 holds a repository off the switch for a mode that is off alone — so no non-cascade repository would ever reach the service", tc.agent)
			}
			if got := on.Installation.CredentialsService; got.Name != credentialsService().Name || got.Region != credentialsService().Region {
				t.Errorf("the repository root's credentials service is %+v, want name and region alone (%+v): a repository root reads those two and nothing else (repoCredentialsService)", got, credentialsService())
			}
			// The address the launch is handed is the same answer, and the
			// invoker grant follows the block: a job that may not call the
			// service is a job whose address must not be set.
			addrs := on.Addresses()
			if len(addrs) == 0 {
				t.Fatal("the fixture produces no address at all: the test would be vacuous")
			}
			if !slices.ContainsFunc(addrs, func(a string) bool {
				return strings.Contains(a, "google_cloud_run_v2_service_iam_member.credentials[0]")
			}) {
				t.Errorf("the switched repository's jobs carry no invoker grant on the service: %v", addrs)
			}
			// D2072's second step, B2.7's flip: the address is handed out AND the
			// two Claude mounts go, because this auth is in the flip list from
			// the release that carries it.
			if on.ModelSecretsServed != config.ModelSecretServedAnthropic {
				t.Errorf("the switched %s repository's root plans model_secrets_served = %q, want %q: the service serves this auth's model calls, so the two Claude mounts go and a provider's key stays (D2072, R6)", tc.name, on.ModelSecretsServed, config.ModelSecretServedAnthropic)
			}
			var checked, dropped int
			for key, ws := range on.Workflows {
				offWS, ok := off.Workflows[key]
				if !ok {
					continue
				}
				checked++
				if kept, had := claudeMountsOf(modelMountsOf(ws)), claudeMountsOf(modelMountsOf(offWS)); len(kept) != 0 || len(had) == 0 {
					t.Errorf("the switched repository's %s job still mounts %v, and with no service at all it mounted %v: the two Claude mounts must be gone from the flipped job and present in the unserved one (D2072, G17)", key, kept, had)
				} else {
					dropped++
				}
				if got, want := modelMountsOf(ws), withoutClaudeMounts(modelMountsOf(offWS)); !slices.Equal(got, want) {
					t.Errorf("the switched repository's %s job mounts %v, want %v, what it mounted with no service at all minus the two Claude credentials: the flip takes those two away and nothing else (D2072, R6)", key, got, want)
				}
			}
			if checked == 0 || dropped == 0 {
				t.Fatalf("%d shared workflows and %d of them dropped a Claude mount: the mount assertions above passed vacuously", checked, dropped)
			}
		})
	}
}

// TestServesNoAPIKeyOrOAuthWithBudgetOff is the other side of D2071: with the
// published mode off, a non-cascade repository is held off the switch — no
// block in its root, no invoker grant, no address — and every mount stays.
//
// The `""` row is the caller that read no node at all (fugaro image, fugaro
// doctor, --print-vars): the fail-closed answer, and not the same one as a node
// that was read and carries an empty mode. The vertex row is the auth nothing
// serves whatever the mode says: its calls go to Vertex AI on the job's own
// service account.
func TestServesNoAPIKeyOrOAuthWithBudgetOff(t *testing.T) {
	for _, auth := range []string{config.AuthAPIKey, config.AuthOAuth} {
		for _, mode := range []string{"", config.BudgetOff} {
			if CredentialsServiceServes(true, true, auth, mode) {
				t.Errorf("CredentialsServiceServes(installed, ready, %q, %q) = true, want false: the gateway the service serves a run through exists in observe and enforce alone, so a mode-off repository must keep its own credential and be handed no address (D2071)", auth, mode)
			}
		}
	}
	for _, mode := range []string{"", config.BudgetOff, config.BudgetObserve, config.BudgetEnforce} {
		if CredentialsServiceServes(true, true, "vertex", mode) {
			t.Errorf("CredentialsServiceServes(installed, ready, %q, %q) = true, want false: a vertex repository's calls are the project's own GCP service and no credential of the service's, whatever the mode says", "vertex", mode)
		}
	}

	offIn := repoRootInputs(t, repoRootOAuthAgent)
	offIn.LC = parseLC(t, repoRootLC)
	off, err := Repo(offIn)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's premise: this repository root DOES carry the block and DOES
	// reach the service for its model credential when the switch is on, so the
	// assertions below are about the mode and not about a hold-off that would
	// happen anyway. The mount is gone on that side, which is the flip's answer
	// and not this test's subject.
	on := repoRootServed(t, repoRootOAuthAgent, repoRootLC, config.BudgetObserve)
	if on.Installation.CredentialsService == nil || on.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Fatalf("the mode-on fixture carries no block or does not reach the service (%+v, model_secrets_served=%q): the test would be vacuous", on.Installation.CredentialsService, on.ModelSecretsServed)
	}

	for _, mode := range []string{"", config.BudgetOff} {
		held := repoRootServed(t, repoRootOAuthAgent, repoRootLC, mode)
		if held.Installation.CredentialsService != nil {
			t.Errorf("with the published mode %q the repository root carries the credentials service %+v, want none: its runs would be handed an address their own runner refuses to use, and its job would give up accessors on credentials it still mounts (D2071)", mode, held.Installation.CredentialsService)
		}
		if held.ModelSecretsServed != config.ModelSecretServedNone {
			t.Errorf("with the published mode %q the root plans model_secrets_served = %q, want %q", mode, held.ModelSecretsServed, config.ModelSecretServedNone)
		}
		for key, ws := range off.Workflows {
			if got := modelMountsOf(held.Workflows[key]); !slices.Equal(got, modelMountsOf(ws)) {
				t.Errorf("with the published mode %q the %s job mounts %v, want %v: a held-off repository keeps every mount", mode, key, got, modelMountsOf(ws))
			}
		}
		if slices.ContainsFunc(held.Addresses(), func(a string) bool {
			return strings.Contains(a, "google_cloud_run_v2_service_iam_member.credentials[0]")
		}) {
			t.Errorf("with the published mode %q a job carries the invoker grant on the service: a repository the switch holds off must not be granted a call the service would refuse (D2071)", mode)
		}
	}
}

// TestFilledServiceServedAuthsDropsTheAnthropicMountsOnly is D2072's SECOND
// step pinned where it holds: B2.7's flip list is filled, so an `api-key` or
// `oauth` repository that IS on the switch plans `model_secrets_served =
// anthropic` and its job gives up the two Claude mounts — and nothing else.
//
// It was written red: with `config.ServiceServedAuths` still empty, the first
// assertion below fails and every mount is still there, which is the state
// B2.3 shipped and the reason this test exists rather than a comment.
//
// The premise is asserted rather than assumed, and on the real list rather than
// on a fixture's copy of it: the flip list is the release's own value, so a
// list that lost a name would fail here and not in a place nobody reads. The
// services served are read through ModelSecretsServed, the one function both
// the root's tfvars and Readiness' mount drop call.
func TestFilledServiceServedAuthsDropsTheAnthropicMountsOnly(t *testing.T) {
	for _, auth := range []string{config.AuthAPIKey, config.AuthOAuth} {
		if !slices.Contains(config.ServiceServedAuths, auth) {
			t.Fatalf("config.ServiceServedAuths is %v and does not carry %q: B2.7's flip is what makes a switched %s repository give up its two Claude mounts (D2072, G17)", config.ServiceServedAuths, auth, auth)
		}
		if got := ModelSecretsServed(ChainPlan{}, true, auth); got != config.ModelSecretServedAnthropic {
			t.Errorf("ModelSecretsServed(serving, %q) = %q, want %q: a switched repository of an auth in the flip list reaches the service for the Anthropic key and the OAuth token, so those two mounts go (D2072)", auth, got, config.ModelSecretServedAnthropic)
		}
		if got := ModelSecretsServed(ChainPlan{}, false, auth); got != config.ModelSecretServedNone {
			t.Errorf("ModelSecretsServed(held off, %q) = %q, want %q: a repository the switch holds off keeps every mount, whatever the flip list says", auth, got, config.ModelSecretServedNone)
		}
	}
	// And through the spec: the switched root names the anthropic arm, its job
	// loses the two Claude mounts, it keeps its containers, and the redaction
	// list follows the mounts it has left.
	offIn := repoRootInputs(t, repoRootOAuthAgent)
	offIn.LC = parseLC(t, repoRootLC)
	off, err := Repo(offIn)
	if err != nil {
		t.Fatal(err)
	}
	on := repoRootServed(t, repoRootOAuthAgent, repoRootLC, config.BudgetEnforce)
	if on.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not on the switch: nothing below would be about a switched repository")
	}
	if on.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Errorf("the switched repository's root plans model_secrets_served = %q, want %q: the flip is why its job drops the mounts (D2072)", on.ModelSecretsServed, config.ModelSecretServedAnthropic)
	}
	offMounts := modelMountsOf(off.Workflows["web"])
	if len(claudeMountsOf(offMounts)) == 0 {
		t.Fatalf("the fixture mounts neither Claude credential with no service at all (%v): the test would be vacuous", offMounts)
	}
	if got, want := modelMountsOf(on.Workflows["web"]), withoutClaudeMounts(offMounts); !slices.Equal(got, want) {
		t.Errorf("the switched repository's web job mounts %v, want %v: the service serves it both Claude credentials, so those two mounts go and every other one stays (D2072, R6)", got, want)
	}
	if !maps.Equal(off.Secrets, on.Secrets) {
		t.Errorf("the secret containers moved with the switch: %v, want %v — a container carries prevent_destroy, so a dropped one plans a destruction and is reported as retired", on.Secrets, off.Secrets)
	}
	if got, want := on.Workflows["web"].Env[runner.SecretEnvsVar], redactionEnvs(on.Workflows["web"].SecretEnv); got != want {
		t.Errorf("%s = %q, want %q: the list follows the mounts, and a mounted credential left off it is one the runner would print", runner.SecretEnvsVar, got, want)
	}
}

// TestFilledServiceServedAuthsKeepsProviderKeys is R6 on the flipped side
// (D1634): a provider's key is spent on a route the credentials service
// FORWARDS rather than attaches, so its mount is the run's own and no arm of
// the flip list takes it away. Losing it would leave the run with no way to
// reach the provider whose model it was told to use.
func TestFilledServiceServedAuthsKeepsProviderKeys(t *testing.T) {
	const apiKeyAgent = `agent: { auth: api-key }`
	offIn := repoRootInputs(t, apiKeyAgent)
	offIn.LC = parseLC(t, repoRootAPIKeyLC)
	off, err := Repo(offIn)
	if err != nil {
		t.Fatal(err)
	}
	offMounts := modelMountsOf(off.Workflows["web"])
	if !slices.Contains(offMounts, "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY=openrouter-api-key") {
		t.Fatalf("the fixture mounts no provider key with no service at all (%v): the test would be vacuous", offMounts)
	}

	on := repoRootServed(t, apiKeyAgent, repoRootAPIKeyLC, config.BudgetEnforce)
	if on.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Fatalf("the switched api-key repository's root plans model_secrets_served = %q, want %q: nothing below would be about a flipped job (D2072)", on.ModelSecretsServed, config.ModelSecretServedAnthropic)
	}
	got := modelMountsOf(on.Workflows["web"])
	for _, m := range got {
		env, _, _ := strings.Cut(m, "=")
		if config.IsProviderKeyEnv(env) {
			continue
		}
		t.Errorf("the flipped repository's web job still mounts %s (%v): the service serves it both Claude credentials, so every other model mount is gone (D2072)", m, got)
	}
	if !slices.Contains(got, "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY=openrouter-api-key") {
		t.Errorf("the flipped repository's web job mounts %v, and the provider's key is missing: the service forwards that key rather than attaching it, so its mount is the run's own and the flip never takes it (R6, D1634)", got)
	}
	// The provider's key is not a credential of the service's, at any auth: a
	// vertex repository's calls are the project's own GCP service.
	if got := ModelSecretsServed(ChainPlan{}, true, "vertex"); got != config.ModelSecretServedNone {
		t.Errorf("ModelSecretsServed(serving, vertex) = %q, want %q: a Vertex workflow's calls are not a credential this service holds", got, config.ModelSecretServedNone)
	}
}

// TestModelAccessorAllowDeleteIsTheFlipsOwn is B2.7's half of the three tests
// of the guard's allow-lists (TestProxiedTokenAccessorAllowDeleteIsTheFlipsOwn
// and TestVaultAccessorAllowDeleteIsTheFlipsOwn carry the other two flips): the
// guard's allow-list for the model credentials a switched repository's job
// stops mounting, on a project that is not managed, which is the ordinary plan.
// That path reads the retirement set and NAMES it and nothing removes it for the
// user, so the plan's own delete of the accessor this tree stopped producing
// reached the guard with nothing to allow it and the run owed the person
// `--allow-delete` or a second command — the HARD RULE broken for the two auths
// the flip carries (the finding B2.7's own review left open, PR 3152).
//
// The fixtures are both of those auths, because the two reach the drop through
// different mounts — an `api-key` job mounts the paid key and an `oauth` one the
// token of the account its variant is for — and the oauth one is deliberately
// NOT a proxied repository: the flips are separate (G22).
//
// The rows are that filter's whole surface. The accessors this spec stopped
// producing are admitted, one per variant, INCLUDING the named account's (a rule
// keyed on the names of the two default mounts would leave that repository's
// update owed a flag for half of its flip — design
// docs/named-oauth-tokens/spec.md §4 T3). A name the job still mounts is not,
// and neither is the name the fetched class's drop took instead (that accessor
// is the OTHER flip's delete, and its own list admits it): the two lists
// partition the plan's deletes rather than overlapping. A variant the
// configuration has left, a create rather than a delete and an address outside
// the repository root's module are not admitted either. The clauses after the
// table are the guard's own inputs: a repository the switch holds off admits
// nothing (its job keeps every mount, so a delete of one is the operator's own
// change), the cascade admits nothing (its arm is `all`, and its job mounts no
// model credential to take away), and neither does a plan the run could not
// read.
func TestModelAccessorAllowDeleteIsTheFlipsOwn(t *testing.T) {
	if !slices.Contains(config.ServiceServedAuths, config.AuthOAuth) {
		t.Fatalf("config.ServiceServedAuths = %v and does not carry %q: B2.7's flip is what makes a switched repository's job give up its Claude mounts, and every row below is about that flip (D2072, G17)", config.ServiceServedAuths, config.AuthOAuth)
	}
	jobAddr := func(variant, name string) string {
		return `module.repo.module.workflow["` + variant + `"].google_secret_manager_secret_iam_member.job["` + name + `"]`
	}
	// The oauth fixture, off and on, from ONE local config: the addresses the
	// plan of the flip deletes are exactly the ones the off spec produced and
	// the served one does not, so nothing below reads a hand-written list of
	// what the flip does.
	offIn := repoRootInputs(t, repoRootOAuthAgent)
	offIn.LC = parseLC(t, repoRootLC)
	off, err := Repo(offIn)
	if err != nil {
		t.Fatal(err)
	}
	oauth := repoRootServed(t, repoRootOAuthAgent, repoRootLC, config.BudgetEnforce)
	if oauth.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not on the switch: nothing below would be about a switched repository")
	}
	if oauth.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Fatalf("the switched oauth repository's root plans model_secrets_served = %q, want %q: this is the arm the allowance is gated on (D2072)", oauth.ModelSecretsServed, config.ModelSecretServedAnthropic)
	}
	for _, variant := range []string{"web", "web_bmb"} {
		if _, deployed := oauth.Workflows[variant]; !deployed {
			t.Fatalf("the fixture deploys no %s variant, so the named account's row below would be about a job this root never makes (design docs/named-oauth-tokens/spec.md §4 T3)", variant)
		}
	}
	var gone []string
	for _, a := range off.Addresses() {
		if !strings.Contains(a, "secret_iam_member.job[") || slices.Contains(oauth.Addresses(), a) {
			continue
		}
		gone = append(gone, a)
	}
	slices.Sort(gone)
	// The two names the model drop took, one per variant: the default account's
	// token and the account the `web_bmb` job is for. Both are named here and
	// not derived from the addresses, because a rule that admitted them BECAUSE
	// the diff carries them would be the very thing under test.
	modelOwn := []string{
		jobAddr("web", config.ClaudeOAuthToken),
		jobAddr("web_bmb", config.ClaudeAccountSecret("bmb")),
	}
	slices.Sort(modelOwn)
	fetched := jobAddr("web", "npm-token")
	if !slices.Contains(gone, fetched) {
		t.Fatalf("the plan the flip makes does not carry %s (%v): the fixture's workflow secret is the fetched class's half of the same diff, and the row about it below would pass for the wrong reason", fetched, gone)
	}
	plan := &tf.Plan{}
	for _, a := range gone {
		plan.ResourceChanges = append(plan.ResourceChanges, change(a, "delete"))
	}
	if got := ModelAccessorAllowDelete(plan, oauth); !slices.Equal(got, modelOwn) {
		t.Errorf("the allow-list for the plan the flip makes = %v, want exactly %v: the model drop's accessors are the flip's own and nothing else in that plan is — the fetched class's is the other list's (B3.8), and admitting it here would be an allowance wider than the flip (the plan's other deletes: %v)", got, modelOwn, gone)
	}
	// The other flip's own list, on the same plan: the two partition it. This is
	// what makes the row above a statement about this list rather than about a
	// plan that happens to carry one address.
	if got := VaultAccessorAllowDelete(plan, oauth); !slices.Contains(got, fetched) {
		t.Errorf("the fetched class's list = %v, and %s is not among them: the two flips' drops are one diff, so a reader has to find each half in its own list (G22)", got, fetched)
	}
	// The names the job still mounts, on the same fixture: the container the
	// model drop left behind is not a licence to delete the mounts that stayed,
	// and the provider's key is the one of those an api-key repository has.
	apiKey := repoRootServed(t, `agent: { auth: api-key }`, repoRootAPIKeyLC, config.BudgetEnforce)
	if apiKey.ModelSecretsServed != config.ModelSecretServedAnthropic {
		t.Fatalf("the switched api-key repository's root plans model_secrets_served = %q, want %q", apiKey.ModelSecretsServed, config.ModelSecretServedAnthropic)
	}
	paidKey := jobAddr("web", config.AnthropicSecret)
	provider := jobAddr("web", "openrouter-api-key")
	mounted := jobAddr("web", "github-app-key")
	for _, addr := range []string{provider, mounted} {
		if !slices.Contains(apiKey.Addresses(), addr) {
			t.Fatalf("this tree's spec produces no %s, so the row about a name the job still mounts would pass for the wrong reason", addr)
		}
	}
	// The mixed stage account is the arm's one exception (B0,
	// docs/mixed-credentials/spec.md §11): its own job keeps the account's
	// token — and its accessor, which sa.tf exempts by the same field — so a
	// plan deleting that address is not the flip's and stops the run.
	mixed := repoRootServed(t, `agent: { auth: api-key }`, repoRootMixedLC, config.BudgetEnforce)
	stageToken := jobAddr("web_bmb", config.ClaudeAccountSecret("bmb"))
	if _, deployed := mixed.Workflows["web_bmb"]; !deployed {
		t.Fatal("the mixed fixture deploys no web_bmb variant: the row below would be about a job this root never makes")
	}
	if !mixed.Workflows["web_bmb"].StageAccount {
		t.Fatal("the mixed fixture's web_bmb job is not the stage account's: nothing below would be about the arm's one exception")
	}
	goneVariant := jobAddr("gone", config.ClaudeOAuthToken)
	otherModule := strings.Replace(paidKey, "module.repo.", "module.installation.", 1)
	for _, tc := range []struct {
		what string
		plan *tf.Plan
		spec RepoSpec
		addr string
		want bool
	}{
		{"the paid key's accessor this spec stopped producing", planDeleting(paidKey), apiKey, paidKey, true},
		{"the account's token of a deployed variant", planDeleting(jobAddr("web_bmb", config.ClaudeAccountSecret("bmb"))), oauth, jobAddr("web_bmb", config.ClaudeAccountSecret("bmb")), true},
		{"a name the job still mounts", planDeleting(provider), apiKey, provider, false},
		{"a name the fetched class's drop took instead", planDeleting(fetched), oauth, fetched, false},
		{"the coder job of a repository with a mixed stage account", planDeleting(paidKey), mixed, paidKey, true},
		{"a stage account's token, which the arm never takes", planDeleting(stageToken), mixed, stageToken, false},
		{"a variant the configuration no longer deploys", planDeleting(goneVariant), oauth, goneVariant, false},
		{"the same address created, not deleted", &tf.Plan{ResourceChanges: []tf.ResourceChange{change(paidKey, "create")}}, apiKey, paidKey, false},
		{"an address outside the repository root's module", planDeleting(otherModule), apiKey, otherModule, false},
	} {
		got := ModelAccessorAllowDelete(tc.plan, tc.spec)
		if slices.Contains(got, tc.addr) != tc.want {
			t.Errorf("%s: admitted = %v, want %v (allow-list %v)", tc.what, !tc.want, tc.want, got)
		}
	}

	// The arm's own gate, and it is the field the module keys its own drop on
	// (RepoSpec.ModelSecretsServed, modules/workflow/sa.tf's job_secret_names):
	// a root whose arm is not `anthropic` admits nothing, whatever its mounts
	// happen to say. The two arms below answer none by the same route — a root
	// the switch holds off (`none`, D2071) and the cascade (`all`) both keep or
	// mount nothing, so nothing is in the difference and the filter alone would
	// answer the same — which is exactly why the gate is stated here rather
	// than left to the filter: it is what makes this list's answer about the
	// one arm whose drop is the model flip's, and it is what keeps a fourth arm
	// from inheriting an allowance nobody decided for it.
	otherArm := oauth
	otherArm.ModelSecretsServed = config.ModelSecretServedNone
	if got := ModelAccessorAllowDelete(planDeleting(modelOwn[0]), otherArm); len(got) != 0 {
		t.Errorf("the allow-list for a root whose arm is %q = %v, want none: the filter reads a difference only the model drop of the anthropic arm makes, and a list that admitted the same address for another arm's root would be one judging a plan it has no answer about", config.ModelSecretServedNone, got)
	}
	// A repository the switch holds off: Readiness plans model_secrets_served =
	// none, its job keeps every mount and every accessor, so a delete of this
	// address is the operator's own change and the guard judges it as before
	// (D2071).
	unswitched := repoRootSpec(t, repoRootOAuthAgent)
	if unswitched.Installation.CredentialsService != nil || unswitched.ModelSecretsServed != config.ModelSecretServedNone {
		t.Fatalf("the unswitched fixture is served (service %v, served %q): nothing below would be about a repository the switch holds off", unswitched.Installation.CredentialsService, unswitched.ModelSecretsServed)
	}
	if got := ModelAccessorAllowDelete(planDeleting(modelOwn[0]), unswitched); len(got) != 0 {
		t.Errorf("the allow-list for a repository the switch holds off = %v, want none: its job mounts the credential it always did, so the drop this list is about is not one its plan makes", got)
	}
	// And the cascade, the other arm: its job mounts no model credential at all,
	// so no accessor of its account is one this flip took away.
	cascade := repoRootServed(t, repoRootCascadeAgent, repoRootLC, config.BudgetOff)
	if cascade.ModelSecretsServed != config.ModelSecretServedAll {
		t.Fatalf("the cascade fixture plans model_secrets_served = %q, want %q", cascade.ModelSecretsServed, config.ModelSecretServedAll)
	}
	if got := ModelAccessorAllowDelete(planDeleting(jobAddr("web", config.AnthropicSecret)), cascade); len(got) != 0 {
		t.Errorf("the allow-list for the cascade = %v, want none: its job mounts no model credential, so every delete of one in its plan is not the flip's", got)
	}
	if got := ModelAccessorAllowDelete(nil, apiKey); len(got) != 0 {
		t.Errorf("the allow-list for a run that read no plan = %v, want none", got)
	}
}

// repoRootVaultLC is repoRootLC with the fixture's own workflow secret admitted
// project-wide for this repository (D1260, D1262, the workflow_secrets block):
// with no entry there npm-token is a repository-level name D2313 leaves
// storeless and mounts nowhere, so B3.1's record holds no row for it and no arm
// of the vault flip could reach it at all. With one, the job mounts the
// PROJECT's copy and the record names it, which is the state B3.8's flip is
// about.
const repoRootVaultLC = repoRootLC + `workflow_secrets:
  npm-token: { allow_data_to: [acme/webapp] }
`

// vaultSpecUnswitched plans the vault fixture with no credentials service in
// the installation at all: the repository the switch cannot serve, whose job
// must keep every mount whatever the class list says.
func vaultSpecUnswitched(t *testing.T) RepoSpec {
	t.Helper()
	in := repoRootInputs(t, repoRootCascadeAgent)
	in.LC = parseLC(t, repoRootVaultLC)
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// vaultAccessors are a spec's secretAccessor addresses of one kind ("job" for
// the job account's own grants, "service" for D2074's), sorted. They are the
// other half of the flip: an accessor and a mount are one answer, read from one
// list, so a mount that moved without its accessor is a job Cloud Run refuses
// to create.
func vaultAccessors(rs RepoSpec, kind string) []string {
	var out []string
	for _, a := range rs.Addresses() {
		if strings.Contains(a, ".google_secret_manager_secret_iam_member."+kind+"[") {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

// vaultNaming are the addresses of addrs that name the logical secret.
func vaultNaming(addrs []string, logical string) []string {
	return slices.DeleteFunc(slices.Clone(addrs), func(a string) bool {
		return !strings.Contains(a, `["`+logical+`"]`)
	})
}

// TestEmptyVaultListChangesNothing is the vault flip's inert state pinned
// (B3.8, D2072): while config.VaultFetchedSecretClasses is empty no workflow
// secret is fetched for any repository, so a switched repository's job mounts
// exactly what it mounted before — the mount, the redaction list that follows
// it, its own account's accessor on the container, and the service's own grant
// on it (D2074), which is one per container the job still mounts.
//
// The list is cleared here and restored afterwards rather than assumed empty,
// so this is the same test in the flip's first commit and its second: it is the
// counterfactual the filled one below is read against.
func TestEmptyVaultListChangesNothing(t *testing.T) {
	saved := config.VaultFetchedSecretClasses
	config.VaultFetchedSecretClasses = nil
	t.Cleanup(func() { config.VaultFetchedSecretClasses = saved })

	off := vaultSpecUnswitched(t)
	on := repoRootServed(t, repoRootCascadeAgent, repoRootVaultLC, config.BudgetEnforce)
	if on.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not on the switch: nothing below would be about a switched repository")
	}
	offWS, onWS := off.Workflows["web"], on.Workflows["web"]
	if _, mounted := offWS.SecretEnv["NPM_TOKEN"]; !mounted {
		t.Fatalf("the fixture mounts no NPM_TOKEN with no service at all (%v): the test would be vacuous", offWS.SecretEnv)
	}
	if !maps.Equal(offWS.SecretEnv, onWS.SecretEnv) {
		t.Errorf("the switched repository's web job mounts %v, want the unswitched one's %v: with the class list empty no workflow secret is fetched, so no mount moves (D147's shape, D2072)", onWS.SecretEnv, offWS.SecretEnv)
	}
	if len(offWS.VaultSecretNames)+len(onWS.VaultSecretNames) != 0 {
		t.Errorf("the vault class names %v and %v, want none on either side: a list that names nothing the service fetches leaves the job's own list empty, and it is that list the module drops the accessor through", offWS.VaultSecretNames, onWS.VaultSecretNames)
	}
	if got := onWS.Env[runner.SecretEnvsVar]; got != redactionEnvs(onWS.SecretEnv) {
		t.Errorf("%s = %q, want %q: the list follows the mounts, and the flip's inert state must leave it exactly as it found it", runner.SecretEnvsVar, got, redactionEnvs(onWS.SecretEnv))
	}
	if got, want := vaultAccessors(on, "job"), vaultAccessors(off, "job"); !slices.Equal(got, want) {
		t.Errorf("the switched repository's job accessors are %v, want %v: no mount moved, so no accessor does", got, want)
	}
	if got := vaultAccessors(on, "service"); len(vaultNaming(got, "npm-token")) == 0 {
		t.Errorf("the service's own grants are %v and name no container for npm-token: the job still mounts it, and D2074's set is one accessor per container the job mounts (B3.3)", got)
	}
}

// TestFilledVaultListDropsWorkflowMountsAndAccessors is B3.8's flip pinned
// where it holds: the class list is filled, so a repository the credentials
// service serves plans its workflow's own secrets away from the job — the
// mount, the redaction entry and the account's accessor — and keeps the
// container, which is what the service's own grant (D2074) and B3.1's record
// name as the value's home.
//
// It was written red: with config.VaultFetchedSecretClasses still empty the
// first assertion below fails and every mount is still there, which is the
// state B3.1-B3.4 shipped and the reason this test exists rather than a
// comment.
func TestFilledVaultListDropsWorkflowMountsAndAccessors(t *testing.T) {
	if !config.VaultFetchesClass(config.VaultSecretClassWorkflow) {
		t.Fatalf("config.VaultFetchedSecretClasses = %v and does not carry %q: B3.8's flip is what makes a switched repository's job give up the mount of its workflow's own secrets and read them from the credentials service instead (D2072, G17)", config.VaultFetchedSecretClasses, config.VaultSecretClassWorkflow)
	}
	off := vaultSpecUnswitched(t)
	on := repoRootServed(t, repoRootCascadeAgent, repoRootVaultLC, config.BudgetEnforce)
	if on.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not on the switch: nothing below would be about a switched repository")
	}
	offWS, onWS := off.Workflows["web"], on.Workflows["web"]
	if _, mounted := offWS.SecretEnv["NPM_TOKEN"]; !mounted {
		t.Fatalf("the fixture mounts no NPM_TOKEN with no service at all (%v): the test would be vacuous", offWS.SecretEnv)
	}
	want := maps.Clone(offWS.SecretEnv)
	delete(want, "NPM_TOKEN")
	if !maps.Equal(onWS.SecretEnv, want) {
		t.Errorf("the switched repository's web job mounts %v, want %v: the workflow's own secrets are the class the flip takes away, and every other mount stays", onWS.SecretEnv, want)
	}
	if got, want := onWS.VaultSecretNames, []string{"npm-token"}; !slices.Equal(got, want) {
		t.Errorf("the switched job carries vault_secret_names = %v, want %v: that list is what the module reads to drop the job's accessor on the container and to keep the service's own grant on it (D2074, B3.8)", got, want)
	}
	if !maps.Equal(off.Secrets, on.Secrets) {
		t.Errorf("the secret containers moved with the flip: %v, want %v — a container carries prevent_destroy, so a dropped one plans a destruction, and the service's fetch reads the very container the job gave up", on.Secrets, off.Secrets)
	}
	if got := onWS.Env[runner.SecretEnvsVar]; got != redactionEnvs(onWS.SecretEnv) {
		t.Errorf("%s = %q, want %q: the list follows the mounts, and a mounted credential left off it is one the runner would print", runner.SecretEnvsVar, got, redactionEnvs(onWS.SecretEnv))
	}
	offJob := vaultAccessors(off, "job")
	if len(vaultNaming(offJob, "npm-token")) == 0 {
		t.Fatalf("the unswitched fixture's job account holds no accessor on npm-token (%v): the test would be vacuous", offJob)
	}
	wantJob := slices.DeleteFunc(slices.Clone(offJob), func(a string) bool { return strings.Contains(a, `["npm-token"]`) })
	if got := vaultAccessors(on, "job"); !slices.Equal(got, wantJob) {
		t.Errorf("the switched job's account holds the accessors %v, want %v: the module drops the job's grant on each name of the same list its mount went with, and a job whose environment mounts no container while its account keeps the accessor is one Cloud Run refuses to create", got, wantJob)
	}
	if got := vaultAccessors(on, "service"); len(vaultNaming(got, "npm-token")) == 0 {
		t.Errorf("the service's own grants are %v and name no container for npm-token: the container the job gave up is the one the run's fetch reads, so the service must keep its accessor (D2074, B3.3) — a grant dropped with the mount would leave every fetch refused", got)
	}
}

// TestFilledVaultListKeepsAnUnswitchedRepository is the other side of the flip
// (G9, D147, D2071): a repository the credentials service does not serve keeps
// every mount whatever the class list says, and the two ways a repository is
// not served are the two rows here — an installation with no service for it at
// all, and a switched installation whose published mode holds it off the switch.
func TestFilledVaultListKeepsAnUnswitchedRepository(t *testing.T) {
	if !config.VaultFetchesClass(config.VaultSecretClassWorkflow) {
		t.Fatalf("config.VaultFetchedSecretClasses = %v and does not carry %q: this case is about the list being FILLED and the repository still keeping its mounts (D147, G9)", config.VaultFetchedSecretClasses, config.VaultSecretClassWorkflow)
	}
	off := vaultSpecUnswitched(t)
	held := repoRootServed(t, repoRootOAuthAgent, repoRootVaultLC, config.BudgetOff)
	if off.Installation.CredentialsService != nil {
		t.Fatal("the fixture carries a credentials service with no installation switch: nothing below would be about an unserved repository")
	}
	if held.Installation.CredentialsService != nil {
		t.Fatalf("the mode-off fixture carries the credentials service (%+v): a mode-off repository is held off the switch entirely (D2071) and the test would be about nothing", held.Installation.CredentialsService)
	}
	for _, rs := range []RepoSpec{off, held} {
		ws := rs.Workflows["web"]
		if _, mounted := ws.SecretEnv["NPM_TOKEN"]; !mounted {
			t.Errorf("%s's web job mounts %v and no NPM_TOKEN: a repository the switch does not serve keeps every mount of its workflow's own secrets, before the flip and after (D147, G9)", rs.Name, ws.SecretEnv)
		}
		if len(ws.VaultSecretNames) != 0 {
			t.Errorf("%s's web job carries vault_secret_names = %v, want none: a job with no address fetches nothing, so nothing may leave its mounts (D147)", rs.Name, ws.VaultSecretNames)
		}
		if got := ws.Env[runner.SecretEnvsVar]; !strings.Contains(got, "NPM_TOKEN") {
			t.Errorf("%s's %s = %q does not name NPM_TOKEN: the mount is there, and a mounted secret left off the redaction list is one the run would print", rs.Name, runner.SecretEnvsVar, got)
		}
	}
}

// repoRootServedLC is repoRootVaultLC with the installation's credentials
// service address, which `fugaro update` writes into the local config from the
// installation's own outputs: the state a switched repository's machine is in,
// and the one a launch or a check job's environment is planned from (a job
// learns the address there, providers.EnvCredentialsServiceURL).
const repoRootServedLC = repoRootVaultLC + credentialsServiceLCBlock

// buildAccessors are a spec's build-account secretAccessor addresses, sorted:
// the module's google_secret_manager_secret_iam_member.build, one per logical
// name the account may read, which is the list B4.9's flip empties for a
// repository the credentials service serves.
func buildAccessors(rs RepoSpec) []string {
	var out []string
	for _, a := range rs.Addresses() {
		if strings.Contains(a, ".google_secret_manager_secret_iam_member.build[") {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

// TestEmptyBuildListChangesNothing is the build flip's inert state pinned
// (B4.9, D2076, D2077): while config.BuildServedSecretClasses is empty no
// build takes anything from the credentials service, so a switched repository
// plans exactly what it planned before — the build account's accessor list
// with the provider credential and the workflow's own build secret in it, and
// the check job's own mount of the git credential, with the redaction list
// that follows it.
//
// The list is cleared here and restored afterwards rather than assumed empty,
// so this is the same test in the flip's first commit and its second: it is
// the counterfactual the filled pair below is read against.
func TestEmptyBuildListChangesNothing(t *testing.T) {
	saved := config.BuildServedSecretClasses
	config.BuildServedSecretClasses = nil
	t.Cleanup(func() { config.BuildServedSecretClasses = saved })

	off := vaultSpecUnswitched(t)
	on := repoRootServed(t, repoRootCascadeAgent, repoRootServedLC, config.BudgetEnforce)
	if on.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not on the switch: nothing below would be about a switched repository")
	}
	if off.Check == nil || on.Check == nil {
		t.Fatal("the fixture plans no check job: the check's own mount is half of what this flip moves")
	}
	if got, want := on.BuildSecrets, off.BuildSecrets; !slices.Equal(got, want) {
		t.Errorf("the switched repository's build secrets are %v, want the unswitched one's %v: with the class list empty no build is served, so the build account's accessor list does not move (D147's shape, D2076)", got, want)
	}
	if !slices.Contains(on.BuildSecrets, config.BitbucketSecret) && !slices.Contains(on.BuildSecrets, "github-app-key") {
		t.Errorf("the fixture's build secrets are %v and name no provider credential: the test would be vacuous", on.BuildSecrets)
	}
	if !maps.Equal(on.Check.SecretEnv, off.Check.SecretEnv) {
		t.Errorf("the switched repository's check job mounts %v, want the unswitched one's %v: the class list is empty, so the job keeps the credential it mounts and fetches nothing", on.Check.SecretEnv, off.Check.SecretEnv)
	}
	if got, want := on.Check.Env[runner.SecretEnvsVar], redactionEnvs(on.Check.SecretEnv); got != want {
		t.Errorf("the switched check job's %s = %q, want %q: a mount that moved without its redaction entry is one the run would print", runner.SecretEnvsVar, got, want)
	}
	if _, named := on.Check.Env[providers.EnvCredentialsServiceURL]; named {
		t.Errorf("the switched check job's environment names a credentials service (%v): with no class served the job mounts its credential and asks nobody for it", on.Check.Env)
	}
	if got, want := buildAccessors(on), buildAccessors(off); !slices.Equal(got, want) {
		t.Errorf("the switched repository's build account holds the accessors %v, want %v: no build named a service, so no accessor moves", got, want)
	}
}

// TestFilledBuildListDropsTheBuildAccessor is B4.9's flip pinned where it
// holds: the class list is filled, so a repository the credentials service
// serves plans its build account without a single secretAccessor — the
// request names no secret for it to read (gcp.BuildRequest, whose own test
// holds the request half) and the check job's mount goes with it (check.tf) —
// while a repository the switch holds off keeps every one of them.
func TestFilledBuildListDropsTheBuildAccessor(t *testing.T) {
	if !config.BuildServesClass(config.BuildSecretClassBuild) {
		t.Fatalf("config.BuildServedSecretClasses = %v and does not carry %q: B4.9's flip is what empties a switched repository's build account accessor list and makes its build fetch the credential from the service instead (D2076, D2077)", config.BuildServedSecretClasses, config.BuildSecretClassBuild)
	}
	off := vaultSpecUnswitched(t)
	on := repoRootServed(t, repoRootCascadeAgent, repoRootVaultLC, config.BudgetEnforce)
	if on.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not on the switch: nothing below would be about a switched repository")
	}
	if len(buildAccessors(off)) == 0 {
		t.Fatalf("the unswitched fixture's build account holds no accessor at all: the test would be vacuous")
	}
	if got := buildAccessors(on); len(got) != 0 {
		t.Errorf("the switched repository's build account holds the accessors %v, want none: the request names no secret, so an accessor kept here is one nothing reads (D2076)", got)
	}
	if got := on.BuildSecrets; len(got) != 0 {
		t.Errorf("the switched repository's build secrets are %v, want none: the tfvars answer is the module's for_each, and the two are one list", got)
	}
	if got, want := buildAccessors(off), buildAccessors(vaultSpecUnswitched(t)); !slices.Equal(got, want) {
		t.Errorf("an unswitched repository's build accessors are %v and then %v: the flip moves a served repository's list alone (G9, D147)", got, want)
	}
}

// TestFilledBuildListDropsTheCheckMounts is the check job's half of the same
// flip: the daily check runs as the build account, so the accessor its mount
// of the repository's git credential is read under is the one the class
// "build" takes away — the mount goes with either class (C2.3) and the job
// fetches the credential from the service instead, which is what the address
// its environment now carries is for (B4.5's checkGitCredential).
func TestFilledBuildListDropsTheCheckMounts(t *testing.T) {
	if !config.BuildServesClass(config.BuildSecretClassGit) || !config.BuildServesClass(config.BuildSecretClassBuild) {
		t.Fatalf("config.BuildServedSecretClasses = %v and does not carry %q and %q: the check job's mount goes with the git credential's own class, and its accessor is the build account's, which the build's class takes away (C2.3)", config.BuildServedSecretClasses, config.BuildSecretClassGit, config.BuildSecretClassBuild)
	}
	off := vaultSpecUnswitched(t)
	on := repoRootServed(t, repoRootCascadeAgent, repoRootServedLC, config.BudgetEnforce)
	if off.Check == nil || on.Check == nil {
		t.Fatal("the fixture plans no check job: nothing below would be about its mounts")
	}
	if len(off.Check.SecretEnv) == 0 {
		t.Fatalf("the unswitched fixture's check job mounts nothing: the test would be vacuous")
	}
	if len(on.Check.SecretEnv) != 0 {
		t.Errorf("the switched repository's check job mounts %v, want none: the job runs as the build account, whose accessor on the credential the same flip takes away, and Cloud Run refuses a job that mounts a secret its account may not read (C2.3)", on.Check.SecretEnv)
	}
	if got := on.Check.Env[runner.SecretEnvsVar]; got != redactionEnvs(on.Check.SecretEnv) {
		t.Errorf("the switched check job's %s = %q, want %q: the list follows the mounts", runner.SecretEnvsVar, got, redactionEnvs(on.Check.SecretEnv))
	}
	if got := on.Check.Env[providers.EnvCredentialsServiceURL]; got != "https://fugaro-credentials-123456789012.us-east5.run.app" {
		t.Errorf("the switched check job's %s = %q, want the installation's service: the job mounts no credential, so the address is the only way it can fetch one (B4.5, D2076)", providers.EnvCredentialsServiceURL, got)
	}
	if !maps.Equal(off.Check.SecretEnv, map[string]string{"FUGARO_GITHUB_APP_PRIVATE_KEY": "github-app-key"}) {
		t.Errorf("the unswitched repository's check job mounts %v: a repository the switch holds off keeps the mount and reads it itself (G9, D147)", off.Check.SecretEnv)
	}
}

// TestCascadeStillDropsItsMounts is the cascade's arm, unchanged by this task:
// the one auth whose in-job credential the service replaces whole, so its
// answer is `all` and every model mount and accessor goes (D143, C1.18/C1.20).
//
// The last block is the `anthropic` arm of the same function — B2.7's, and by
// then the arm an `api-key` or `oauth` repository's root plans
// (TestFilledServiceServedAuthsDropsTheAnthropicMountsOnly) — pinned here too
// where the drop rule lives: it drops the two Claude credentials and never a
// provider's key, whose value the runner forwards rather than attaches (R6,
// D1634). A mixed stage account's own variant job is the arm's one exception,
// its Claude OAuth mount being the STAGE's credential rather than the run's.
func TestCascadeStillDropsItsMounts(t *testing.T) {
	if got := ModelSecretsServed(ChainPlan{}, true, config.AuthCascade); got != config.ModelSecretServedAll {
		t.Errorf("ModelSecretsServed(serving, cascade) = %q, want %q: the cascade's runner reads every model credential from the service, so its job keeps none (D143)", got, config.ModelSecretServedAll)
	}
	if got := ModelSecretsServed(ChainPlan{}, false, config.AuthCascade); got != config.ModelSecretServedNone {
		t.Errorf("ModelSecretsServed(held off, cascade) = %q, want %q: a cascade repository the switch holds off keeps exactly what it had", got, config.ModelSecretServedNone)
	}
	rs := repoRootServed(t, repoRootCascadeAgent, repoRootLC, config.BudgetEnforce)
	if rs.ModelSecretsServed != config.ModelSecretServedAll {
		t.Errorf("a switched cascade repository's root plans model_secrets_served = %q, want %q: its job mounts no model credential in any case, and the module drops the accessors it would otherwise have", rs.ModelSecretsServed, config.ModelSecretServedAll)
	}
	for key, ws := range rs.Workflows {
		if got := modelMountsOf(ws); len(got) != 0 {
			t.Errorf("the switched cascade repository's %s job mounts %v: a cascade job holds no model credential at all (D143)", key, got)
		}
	}

	// dropModelMounts, the mount half of the drop, on a workflow that carries
	// all three kinds: the provider key is the one the anthropic arm keeps.
	ws := func() *WorkflowSpec {
		return &WorkflowSpec{
			SecretEnv: map[string]string{
				"CLAUDE_CODE_OAUTH_TOKEN":        "claude-oauth-token",
				"ANTHROPIC_API_KEY":              "anthropic-api-key",
				"FUGARO_PROVIDER_KEY_OPENROUTER": "openrouter-api-key",
				"NPM_TOKEN":                      "npm-token",
			},
			Env: map[string]string{runner.SecretEnvsVar: "stale"},
		}
	}
	for _, tc := range []struct {
		served string
		// stageAccount is WorkflowSpec.StageAccount: a mixed stage account's
		// own variant job, the one job of an api-key repository whose Claude
		// OAuth mount is the STAGE's credential rather than the run's.
		stageAccount bool
		want         []string
		// redaction is the list the drop leaves behind: the mounts that
		// remain, or the fixture's own stale value where nothing was dropped
		// and so nothing is rebuilt.
		redaction string
	}{
		{served: config.ModelSecretServedAll, want: []string{"NPM_TOKEN=npm-token"}, redaction: redactionEnvs(map[string]string{"NPM_TOKEN": "npm-token"})},
		{served: config.ModelSecretServedAnthropic, want: []string{"FUGARO_PROVIDER_KEY_OPENROUTER=openrouter-api-key", "NPM_TOKEN=npm-token"}, redaction: redactionEnvs(map[string]string{"FUGARO_PROVIDER_KEY_OPENROUTER": "openrouter-api-key", "NPM_TOKEN": "npm-token"})},
		// The mixed stage account's own job is the one job the anthropic arm
		// does not reach whole: its Claude OAuth mount is that account's token
		// for the three stages that go straight to Anthropic rather than
		// through the run's gateway, so the service never serves them and the
		// mount stays — with the account's accessor on it, which sa.tf exempts
		// by the same field. Its Anthropic key is the run's own credential and
		// still goes, and a provider key was never in reach (R6, D1634).
		{served: config.ModelSecretServedAnthropic, stageAccount: true, want: []string{"CLAUDE_CODE_OAUTH_TOKEN=claude-oauth-token", "FUGARO_PROVIDER_KEY_OPENROUTER=openrouter-api-key", "NPM_TOKEN=npm-token"}, redaction: redactionEnvs(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token", "FUGARO_PROVIDER_KEY_OPENROUTER": "openrouter-api-key", "NPM_TOKEN": "npm-token"})},
		{served: config.ModelSecretServedNone, want: []string{"ANTHROPIC_API_KEY=anthropic-api-key", "CLAUDE_CODE_OAUTH_TOKEN=claude-oauth-token", "FUGARO_PROVIDER_KEY_OPENROUTER=openrouter-api-key", "NPM_TOKEN=npm-token"}, redaction: "stale"},
	} {
		s := ws()
		s.StageAccount = tc.stageAccount
		dropModelMounts(s, tc.served)
		var got []string
		for env, logical := range s.SecretEnv {
			got = append(got, env+"="+logical)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("dropModelMounts(%q, stage account %t) leaves %v, want %v", tc.served, tc.stageAccount, got, tc.want)
		}
		if s.Env[runner.SecretEnvsVar] != tc.redaction {
			t.Errorf("dropModelMounts(%q, stage account %t) leaves %s = %q, want %q", tc.served, tc.stageAccount, runner.SecretEnvsVar, s.Env[runner.SecretEnvsVar], tc.redaction)
		}
	}
}

// moduleResources are the resource blocks the repository's own modules
// declare, as "type.name", for the modules a repository root includes.
//
// The two tests above compare Addresses() with a golden, and a golden is a
// snapshot a manual -tags terraform -update rewrites: a resource added to a
// module -- #362's google_artifact_registry_repository_iam_member.build_ghcr,
// say -- leaves both sides agreeing with each other and both wrong, and
// every fugaro update would then print a false "This change retires 1
// address" pointing at a destructive command. This reads the modules
// themselves, embedded in the binary, so the drift fails a test that CI runs
// with no terraform, no network and no golden to maintain.
var moduleResources = []string{"gcp/modules/repo", "gcp/modules/workflow"}

// moduleResourceRE matches a resource declaration's type and name. The
// instance key is not in the declaration: it is for_each's, at apply time.
var moduleResourceRE = regexp.MustCompile(`(?m)^resource\s+"([^"]+)"\s+"([^"]+)"`)

// TestAddressesCoversEveryModuleResource is the drift guard the goldens
// cannot be: every resource the repo and workflow modules declare must show
// up in the union of the fixtures' Addresses(), or a retirement would be
// predicted for something the plan really produces.
//
// It has no exemption list. It had one entry —
// google_cloud_run_v2_service_iam_member.credentials, off in both fixtures
// because neither opted in — and the opt-in fixture closed it: an exemption
// is a hole in the guard, and the day a resource is only ever produced under
// a branch nothing plans, the guard is silent about it.
func TestAddressesCoversEveryModuleResource(t *testing.T) {
	var addrs []string
	for name := range repoRootFixtures {
		addrs = append(addrs, repoRootFixtureSpec(t, name).Addresses()...)
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
				t.Errorf("%s declares %s.%s, and no fixture's Addresses() has an address for it.\n"+
					"A retirement is computed as the state minus those addresses, so this resource would be\n"+
					"retired -- printed as a change that removes it -- on every fugaro update.\n"+
					"Add it to Addresses() (internal/infra/spec.go), regenerate the goldens\n"+
					"(go test -tags terraform ./internal/infra -run TestRealRepoRootAddresses -update),\n"+
					"or add a fixture that turns its branch on (repoRootFixtures, spec_test.go).",
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

// --- The Bitbucket proxied fact (B1.8b, D500, G9, G15)

// bitbucketTokenEnv is the variable a Bitbucket repository's job mounts its
// git credential as (providers.EnvBitbucketToken), spelled the way the M4
// capture's own assertions spell it.
const bitbucketTokenEnv = "FUGARO_BITBUCKET_TOKEN"

// proxiedGitList puts config.ProxiedGitSecretNames in the state one test reads
// — the flip's own state is the constant itself (B1.13 part two fills it with
// the Bitbucket token), and a test that wants the list EMPTY again says so and
// puts it back. It is a package var, so that is a mutation a test can make.
func proxiedGitList(t *testing.T, names ...string) {
	t.Helper()
	old := config.ProxiedGitSecretNames
	config.ProxiedGitSecretNames = names
	t.Cleanup(func() { config.ProxiedGitSecretNames = old })
}

// proxiedLocalEntry is the operator's half of the flip in their own config:
// the project_secrets entry that allow-lists the sandbox's repository for the
// project's Bitbucket token and asks for the credentials service to forward
// it (D493).
const proxiedLocalEntry = "project_secrets:\n  bitbucket-token:\n    allow_data_to: [\"acme/sandbox\"]\n    proxy: true\n"

// proxiedProviderEntry is repoRootAPIKeyLC's block for the sandbox: one model
// provider allowed to send this repository's code, so an `api-key` job mounts
// its key beside the model credentials. Its own key is the one name of such a
// job the credentials service does not hold (R6, D1634), which is what the
// proxied flip's negative row needs now that B3.8's flip fetches a workflow's
// own secrets.
const proxiedProviderEntry = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/sandbox"]
`

// publishedNode is a published policy node whose bitbucket_token block admits
// repos with proxy: proxy — the one source of whether a repository is proxied
// (G15). The repositories are given as owner/name and qualified the way
// `fugaro project policy publish` qualifies them, so a test names the same
// repository the node does.
func publishedNode(t *testing.T, proxy bool, repos ...string) *projectpolicy.Policy {
	t.Helper()
	allow := make([]string, 0, len(repos))
	for _, r := range repos {
		c, err := projectpolicy.Canonical("bitbucket", r)
		if err != nil {
			t.Fatalf("projectpolicy.Canonical(bitbucket, %q) = %v", r, err)
		}
		allow = append(allow, c)
	}
	return &projectpolicy.Policy{BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: allow, Proxy: proxy}}
}

// proxiedSandboxInputs is the sandbox fixture — acme/sandbox on Bitbucket —
// with the installation's credentials service deployed, the repository
// switched onto it and the node the caller read. extraLC is the operator's
// local config, appended to the M5 additions.
//
// The repository is a CASCADE one because the switch is what a proxied token
// needs: the service is the process that forwards it, so a repository the
// switch holds off keeps the token mounted and keeps its account's accessor on
// it (CredentialsServiceServes' auth condition). The sandbox's own fugaro.yaml
// says oauth, so the auth is spelled over it here — the one field these tests
// are not about.
func proxiedSandboxInputs(t *testing.T, extraLC string, node *projectpolicy.Policy) Inputs {
	t.Helper()
	in := sandboxInputs(t, m5Additions+extraLC)
	in.Cfg.Agent.Auth = config.AuthCascade
	serviceReady(t, in.LC, in.Repo)
	in.Installation.CredentialsService = credentialsService()
	in.Policy = node
	return in
}

// repoBitbucketProxied is the fact as the plan sees it: the repo object's
// bitbucket_proxied attribute in the tfvars the repository root plans from.
// present is false when the key is not there at all, which is what the
// module's optional(bool, false) reads as false — so a caller asserting the
// fact's absence asserts this, not a false in the JSON.
func repoBitbucketProxied(t *testing.T, spec RepoSpec) (value, present bool) {
	t.Helper()
	data, err := RepoVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Repo map[string]json.RawMessage `json:"repo"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, present := doc.Repo["bitbucket_proxied"]
	if !present {
		return false, false
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("repo.bitbucket_proxied = %s: %v", raw, err)
	}
	return value, true
}

// bitbucketTokenAccessor is the address of the job's own secretAccessor on the
// repository's Bitbucket token, which the flip takes away from a proxied
// repository's job and from no other (G9). It is the Go mirror of the
// module's job_secret_names entry for that mount.
const bitbucketTokenAccessor = `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["bitbucket-token"]`

// TestProxiedFactFromThePublishedNode: the per-repository fact comes from the
// published node's bitbucket_token block and from nothing else (G15) — true
// for a repository the node admits with proxy: true, through an exact entry or
// through a pattern, and false for one it does not admit, for a node that
// carries no block and for a caller that read no node at all.
func TestProxiedFactFromThePublishedNode(t *testing.T) {
	spec, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, publishedNode(t, true, "acme/sandbox")))
	if err != nil {
		t.Fatal(err)
	}
	if !spec.BitbucketProxied {
		t.Errorf("BitbucketProxied = false for a repository the published node admits with proxy: true and an opted-in installation, want true: the node is the one source of the fact (G15)")
	}
	if got, present := repoBitbucketProxied(t, spec); !present || !got {
		t.Errorf("the tfvars' repo.bitbucket_proxied = %v (key present: %v), want true: the module reads the fact from the repo object (modules/repo/workflows.tf)", got, present)
	}

	// The node's own matcher decides admission, so one entry serves a whole
	// workspace (dc73's segment wildcards, D464).
	wildcard := &projectpolicy.Policy{BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/*"}, Proxy: true}}
	if rs, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, wildcard)); err != nil {
		t.Errorf("Repo with a node whose allow_repos admits this repository through a pattern: %v", err)
	} else if !rs.BitbucketProxied {
		t.Errorf("BitbucketProxied = false for a node admitting bitbucket/acme/*, want true: the fact is read with the node's own matcher (projectpolicy.MatchRepo)")
	}

	for name, node := range map[string]*projectpolicy.Policy{
		"a node admitting another workspace's repository": publishedNode(t, true, "workspace-b/other"),
		"a node with no bitbucket_token block":            {},
		"no node read":                                    nil,
	} {
		rs, err := Repo(proxiedSandboxInputs(t, "", node))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if rs.BitbucketProxied {
			t.Errorf("%s: BitbucketProxied = true, want false: only the node's bitbucket_token block, and only for a repository it admits, makes a repository proxied (G15)", name)
		}
		if _, present := repoBitbucketProxied(t, rs); present {
			t.Errorf("%s: the tfvars carry repo.bitbucket_proxied, want the key omitted: the fact is false, and the module's own default is false, so nothing may move for it", name)
		}
	}
}

// TestProxiedFactFalseWithoutOptIn: the installation has not opted in to the
// credentials service, so nothing is proxied whatever the node admits and
// whatever the operator's local entry says (G9). The local entry is not judged
// either: with no service there is no forwarder for it to be about, and every
// repository keeps mounting and reading its token exactly as it did.
func TestProxiedFactFalseWithoutOptIn(t *testing.T) {
	in := sandboxInputs(t, m5Additions+proxiedLocalEntry)
	in.Policy = publishedNode(t, true, "acme/sandbox")
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if spec.BitbucketProxied {
		t.Errorf("BitbucketProxied = true with the installation's credentials service off, want false: the flip needs both halves, and the opt-in is one of them (D147, G9)")
	}
	if _, present := repoBitbucketProxied(t, spec); present {
		t.Errorf("the tfvars carry repo.bitbucket_proxied with the opt-in off, want the key omitted: no plan of an installation without the service may move for the flip (G9)")
	}
	if ws := spec.Workflows["web"]; ws.SecretEnv[bitbucketTokenEnv] != config.BitbucketSecret {
		t.Errorf("workflow web mounts %v with the opt-in off, want %s -> %s kept", ws.SecretEnv, bitbucketTokenEnv, config.BitbucketSecret)
	}

	// The entry disagrees with the node here, and it is not refused: without
	// the opt-in the proxy switch is not in effect, so there is nothing to
	// reconcile.
	disagreeing := sandboxInputs(t, m5Additions+proxiedLocalEntry)
	disagreeing.Policy = publishedNode(t, true, "workspace-b/other")
	if _, err := Repo(disagreeing); err != nil {
		t.Errorf("Repo with the opt-in off and a local entry the node does not agree with = %v, want no error: with no credentials service nothing is proxied, so no entry is judged (G9)", err)
	}
}

// TestProxiedFactFalseWhenProxyFalse: the node carries the block and admits
// the repository, and the installation opted in — but the block's proxy is
// off, which is the operator's per-installation switch (D493), so the job
// keeps the token and the runner reads it as it always has.
func TestProxiedFactFalseWhenProxyFalse(t *testing.T) {
	spec, err := Repo(proxiedSandboxInputs(t, "", publishedNode(t, false, "acme/sandbox")))
	if err != nil {
		t.Fatal(err)
	}
	if spec.BitbucketProxied {
		t.Errorf("BitbucketProxied = true for a node whose proxy is false, want false: the block admits the repository and forwards nothing (D493, G3)")
	}
	if _, present := repoBitbucketProxied(t, spec); present {
		t.Errorf("the tfvars carry repo.bitbucket_proxied for a node with proxy off, want the key omitted")
	}
}

// TestLocalEntryDisagreeingWithNodeRefused: the node is the one source of
// whether a repository is proxied (G15), so a local project_secrets entry that
// answers differently for this repository — it proxies one the node does not
// admit, or it stopped proxying one the node admits — is refused, with both
// fixes named: publish the entry, or correct it. An entry that agrees, and no
// entry at all, are not refusals: the first is the state the node was
// published from, and the second says nothing for the node to disagree with.
func TestLocalEntryDisagreeingWithNodeRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		node *projectpolicy.Policy
	}{
		"the entry proxies a repository the node does not admit": {publishedNode(t, true, "workspace-b/other")},
		"the entry proxies a repository the node's block does not carry": {
			&projectpolicy.Policy{},
		},
		"the entry proxies a repository the node's proxy is off for": {publishedNode(t, false, "acme/sandbox")},
	} {
		_, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, tc.node))
		if err == nil {
			t.Errorf("%s: Repo = no error, want a refusal: the node is the one source of whether a repository is proxied (G15)", name)
			continue
		}
		var ue *UserError
		if !errors.As(err, &ue) {
			t.Errorf("%s: Repo = %v (%T), want a user error the operator can act on", name, err, err)
		}
		for _, want := range []string{"project_secrets.bitbucket-token", "fugaro project policy publish", "acme/sandbox"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal %q does not name %q: it has to say which entry, which repository and the command that publishes the entry", name, err, want)
			}
		}
	}

	// The other direction: the node admits this repository and the entry does
	// not, so the next publish from this config would take the repository's
	// proxy away from a node the installation's runs already read.
	notProxied := "project_secrets:\n  bitbucket-token:\n    allow_data_to: [\"acme/sandbox\"]\n"
	_, err := Repo(proxiedSandboxInputs(t, notProxied, publishedNode(t, true, "acme/sandbox")))
	if err == nil {
		t.Error("Repo = no error for a node that admits this repository with proxy: true and a local entry that does not proxy it, want a refusal")
	} else if !strings.Contains(err.Error(), "fugaro project policy publish") {
		t.Errorf("the refusal %q names no fix: it has to say to publish the config the entry is in, or to correct the entry", err)
	}

	// No entry is not a disagreeing entry: the node decides, and the fact is
	// true — a machine that never wrote the entry reads the node's answer.
	spec, err := Repo(proxiedSandboxInputs(t, "", publishedNode(t, true, "acme/sandbox")))
	if err != nil {
		t.Fatalf("Repo with no project_secrets entry = %v, want the node's answer", err)
	}
	if !spec.BitbucketProxied {
		t.Error("BitbucketProxied = false with no local entry and a node admitting this repository with proxy: true, want true: the node is the one source, and an absent entry disagrees with nothing (G15)")
	}
}

// TestLocalProxyForAnotherRepositoryWithEmptyNode: the local entry's proxy
// switch and its allow_data_to are read for THIS repository, not on their own.
// An entry that proxies workspace-b/other says nothing about acme/sandbox, so
// a node that admits nothing disagrees with it about nothing: the run is not
// refused, and this repository is not proxied — the fact is the node's answer
// for this repository, which is false (G15).
//
// The mutant this test kills is the local entry consulted without its
// allow-list: read entry.Proxy as the local answer (drop entry.AllowsData) and
// every row below turns into the refusal "disagree about acme/sandbox".
func TestLocalProxyForAnotherRepositoryWithEmptyNode(t *testing.T) {
	const otherRepoEntry = "project_secrets:\n  bitbucket-token:\n    allow_data_to: [\"workspace-b/other\"]\n    proxy: true\n"
	for name, node := range map[string]*projectpolicy.Policy{
		"the project published no bitbucket_token block": {},
		"the node's block admits no repository":          publishedNode(t, true),
	} {
		spec, err := Repo(proxiedSandboxInputs(t, otherRepoEntry, node))
		if err != nil {
			t.Errorf("%s: Repo = %v, want no error: the entry proxies another repository, which is not a disagreement about acme/sandbox", name, err)
			continue
		}
		if spec.BitbucketProxied {
			t.Errorf("%s: BitbucketProxied = true, want false: the node admits this repository nowhere, and the entry is about workspace-b/other (G15)", name)
		}
		if got, present := repoBitbucketProxied(t, spec); present {
			t.Errorf("%s: the tfvars' repo.bitbucket_proxied = %v, want the key omitted: this repository is not proxied, whatever the entry says about another one", name, got)
		}
	}
}

// TestEmptyProxiedListKeepsTheMountAndAccessor is the counterfactual the flip
// is read against: with config.ProxiedGitSecretNames put back to empty, a
// proxied repository's job keeps the accessor on and the mount of its
// Bitbucket token — and the fact itself is already in the tfvars, so the list
// is what drops it and the fact alone drops nothing. The tree's own list names
// the token since B1.13 part two, and a test that read only the real list
// could not tell the two halves apart.
func TestEmptyProxiedListKeepsTheMountAndAccessor(t *testing.T) {
	proxiedGitList(t)
	spec, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, publishedNode(t, true, "acme/sandbox")))
	if err != nil {
		t.Fatal(err)
	}
	if got, present := repoBitbucketProxied(t, spec); !present || !got {
		t.Fatalf("the tfvars' repo.bitbucket_proxied = %v (key present: %v), want true: the fact is what the flip reads, and the assertions below would pass vacuously without it", got, present)
	}
	f := newCloud(t)
	ready, _, _ := readinessOf(t, f, spec)
	for _, s := range []struct {
		what string
		spec RepoSpec
	}{{"the spec Repo built", spec}, {"the spec Readiness resolved", ready}} {
		ws := s.spec.Workflows["web"]
		if ws.SecretEnv[bitbucketTokenEnv] != config.BitbucketSecret {
			t.Errorf("%s mounts %v, want %s -> %s kept: with the list empty no job gives the token up, whatever the node admits", s.what, ws.SecretEnv, bitbucketTokenEnv, config.BitbucketSecret)
		}
		if ws.SecretIDs[config.BitbucketSecret] == "" {
			t.Errorf("%s names no secret for %s: the mount that stays still points at the container", s.what, config.BitbucketSecret)
		}
		if got := ws.Env[runner.SecretEnvsVar]; !strings.Contains(got, bitbucketTokenEnv) {
			t.Errorf("%s's %s = %q, want %s among the variables: the redactor masks exactly what the job mounts", s.what, runner.SecretEnvsVar, got, bitbucketTokenEnv)
		}
		if !slices.Contains(s.spec.Addresses(), bitbucketTokenAccessor) {
			t.Errorf("%s produces no %s, want the job's own accessor kept: the flip's accessor half is the list too", s.what, bitbucketTokenAccessor)
		}
	}
}

// TestFilledProxiedListDropsMountAndAccessor is what the flip buys, read off
// config.ProxiedGitSecretNames as the tree spells it: a proxied repository's
// job gives up the mount, the secret id and the redaction entry, and the plan
// gives up the account's accessor on it — together, always, because a container
// that still mounted a secret its account cannot resolve is a job Cloud Run
// refuses to create. The check job kept its own until S4, because it runs as
// the build account and cloned with the token (D148, F3); B4.9 is that
// release, so the check job gives the mount up too and fetches the credential
// at the service, which is what its environment now names. The guard below is
// the list's own half: with it emptied again the mount and the accessor come
// back, which TestEmptyProxiedListKeepsTheMountAndAccessor reads.
func TestFilledProxiedListDropsMountAndAccessor(t *testing.T) {
	if !slices.Contains(config.ProxiedGitSecretNames, config.BitbucketSecret) {
		t.Fatalf("config.ProxiedGitSecretNames = %v does not name %s: the flip is B1.13 part two, and until it fills the list a proxied repository's job keeps the mount and the accessor this test is written to see go", config.ProxiedGitSecretNames, config.BitbucketSecret)
	}
	spec, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, publishedNode(t, true, "acme/sandbox")))
	if err != nil {
		t.Fatal(err)
	}
	if !spec.BitbucketProxied {
		t.Fatal("BitbucketProxied = false for a repository the node admits with proxy: true: the assertions below would pass vacuously")
	}
	if got := spec.Workflows["web"].SecretEnv[bitbucketTokenEnv]; got != config.BitbucketSecret {
		t.Fatalf("the spec's web job mounts %s -> %q, want %s: the fixture must carry the mount the drop takes away", bitbucketTokenEnv, got, config.BitbucketSecret)
	}
	f := newCloud(t)
	ready, _, _ := readinessOf(t, f, spec)
	ws := ready.Workflows["web"]
	if _, mounted := ws.SecretEnv[bitbucketTokenEnv]; mounted {
		t.Errorf("a proxied repository's job still mounts %s -> %q: the service holds the token for it, so the account keeps no accessor and the mount goes with it (D500, D502)", bitbucketTokenEnv, ws.SecretEnv[bitbucketTokenEnv])
	}
	if id, named := ws.SecretIDs[config.BitbucketSecret]; named {
		t.Errorf("a proxied repository's job still names the token's secret %q, want none: it has no mount of it left to point at", id)
	}
	if got := ws.Env[runner.SecretEnvsVar]; strings.Contains(got, bitbucketTokenEnv) {
		t.Errorf("a proxied repository's job's %s = %q, want the variables that are left: the redactor masks exactly what the job mounts", runner.SecretEnvsVar, got)
	}
	if slices.Contains(ready.Addresses(), bitbucketTokenAccessor) {
		t.Errorf("the plan still produces %s, want it gone: the job's account reads no token, so it needs no accessor on one (D500, G1)", bitbucketTokenAccessor)
	}
	if ready.Secrets[config.BitbucketSecret] == "" {
		t.Error("the token's container left rs.Secrets: the build account clones with it and its versions are the operator's to store (D148, F3)")
	}
	if ready.Check == nil {
		t.Fatal("the fixture deploys no check job: the assertion below would pass vacuously")
	}
	if got, mounted := ready.Check.SecretEnv[bitbucketTokenEnv]; mounted {
		t.Errorf("the check job's SecretEnv = %v, want no %s mount: it runs as the build account, whose accessor on the token B4.9's flip takes away with the build's own (D2076, C2.3) — and D148's 'the check job keeps its own' is what that release supersedes", ready.Check.SecretEnv, got)
	}
}

// TestFilledProxiedListKeepsNonProxiedRepositories: with the list naming the
// token, a Bitbucket repository the node does not admit — and every repository
// of an installation that did not opt in — keeps its accessor and its mount
// (G9). The list alone is not the flip: the per-repository fact is the other
// half, and this is the test that says so on the real list.
func TestFilledProxiedListKeepsNonProxiedRepositories(t *testing.T) {
	if !slices.Contains(config.ProxiedGitSecretNames, config.BitbucketSecret) {
		t.Fatalf("config.ProxiedGitSecretNames = %v does not name %s: with the list empty every repository below keeps its token anyway, and the assertions would pass without reading the fact", config.ProxiedGitSecretNames, config.BitbucketSecret)
	}
	for name, in := range map[string]Inputs{
		"a repository the node does not admit": proxiedSandboxInputs(t, "", publishedNode(t, true, "workspace-b/other")),
		"an installation that did not opt in": func() Inputs {
			notOptedIn := sandboxInputs(t, m5Additions+proxiedLocalEntry)
			notOptedIn.Policy = publishedNode(t, true, "acme/sandbox")
			return notOptedIn
		}(),
	} {
		spec, err := Repo(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if spec.BitbucketProxied {
			t.Errorf("%s: BitbucketProxied = true, want false", name)
			continue
		}
		if got := spec.Workflows["web"].SecretEnv[bitbucketTokenEnv]; got != config.BitbucketSecret {
			t.Errorf("%s: the job mounts %s -> %q, want %s: every Bitbucket repository keeps mounting its token (G9)", name, bitbucketTokenEnv, got, config.BitbucketSecret)
			continue
		}
		if !slices.Contains(spec.Addresses(), bitbucketTokenAccessor) {
			t.Errorf("%s: the plan produces no %s, want the account's accessor kept: nothing but a proxied repository gives it up (G9)", name, bitbucketTokenAccessor)
		}
		f := newCloud(t)
		ready, _, _ := readinessOf(t, f, spec)
		if got := ready.Workflows["web"].SecretEnv[bitbucketTokenEnv]; got != config.BitbucketSecret {
			t.Errorf("%s: the resolved spec's job mounts %s -> %q, want %s: the drop is the proxied repository's alone", name, bitbucketTokenEnv, got, config.BitbucketSecret)
		}
		if !slices.Contains(ready.Addresses(), bitbucketTokenAccessor) {
			t.Errorf("%s: the resolved plan produces no %s, want it kept", name, bitbucketTokenAccessor)
		}
	}
}

// TestTheFlipConvergesThroughTheUpdatesOwnRetirement is the HARD RULE's test
// for the flip (CLAUDE.md: `fugaro update` is the whole upgrade). A repository
// root applied by the release before the flip holds the job's accessor on the
// Bitbucket token — the state that release wrote, and what its state file
// still says. This tree's plan no longer produces that address, so the two
// converge through the update's own retirement flow: the address is a row of
// the state-vs-spec set difference, it is a type a managed project's plain
// update retires by itself (tf.RetirableTypes), and the guard's allow-list for
// the plan that carries its delete is the retirement's own — so no
// `--allow-delete` and no second command is owed, and the update applies the
// flip in one run.
//
// What it does NOT prove is the token itself, and that is the CLI's half:
// Terraform converges whether or not the installation's bundle holds one, so
// the bundle-first order B1.13 asks for is a step of `fugaro update` and not
// of this plan — refuseTheProxiedDrop (internal/cli's update_proxied.go)
// refuses the drop while the bundle holds no token and names the one command
// that stores it (O8). Here the plan is read alone: what this test says is that
// the flip needs no flag and no second run once the token is there.
func TestTheFlipConvergesThroughTheUpdatesOwnRetirement(t *testing.T) {
	if !slices.Contains(config.ProxiedGitSecretNames, config.BitbucketSecret) {
		t.Fatalf("config.ProxiedGitSecretNames = %v does not name %s: with the list empty the address below is produced by both sides and the retirement this test is about is empty", config.ProxiedGitSecretNames, config.BitbucketSecret)
	}
	// The state the release before this one wrote: the same repository, the
	// same installation and the same node, read with the flip list empty. The
	// list is a package var, so that is a state a test can read. Addresses is
	// read while the list is still empty: it answers from the list at the
	// moment it is called, not at the moment the spec was built.
	beforeAddrs := func() []string {
		old := config.ProxiedGitSecretNames
		config.ProxiedGitSecretNames = nil
		defer func() { config.ProxiedGitSecretNames = old }()
		before, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, publishedNode(t, true, "acme/sandbox")))
		if err != nil {
			t.Fatal(err)
		}
		ready, _, _ := readinessOf(t, newCloud(t), before)
		return ready.Addresses()
	}()
	if !slices.Contains(beforeAddrs, bitbucketTokenAccessor) {
		t.Fatalf("the state the release before the flip wrote holds no %s: the retirement assertion below would pass vacuously", bitbucketTokenAccessor)
	}

	spec, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, publishedNode(t, true, "acme/sandbox")))
	if err != nil {
		t.Fatal(err)
	}
	after, _, _ := readinessOf(t, newCloud(t), spec)
	if slices.Contains(after.Addresses(), bitbucketTokenAccessor) {
		t.Fatalf("this tree's plan still produces %s: the flip has not landed, and nothing below is a convergence", bitbucketTokenAccessor)
	}

	// The update's read of the two: the state's addresses minus the spec's.
	rets := Retirements(beforeAddrs, after)
	var row *Retirement
	for i := range rets {
		if rets[i].Address == bitbucketTokenAccessor {
			row = &rets[i]
		}
	}
	if row == nil {
		t.Fatalf("the update reads no retirement for %s, so nothing takes it out of the state: the flip would leave the plan carrying a delete of an address the configuration no longer declares.\nretirement set: %v", bitbucketTokenAccessor, rets)
	}
	if row.Kept != "" {
		t.Errorf("the accessor's retirement carries the promise %q: it is a binding, it holds no value, and the promise is the secret container's and the registry's alone", row.Kept)
	}
	for _, stop := range RetireStops(rets) {
		if stop.Address == bitbucketTokenAccessor {
			t.Errorf("a managed project's plain `fugaro update` does not retire %s by itself (%s): the flip would need a flag or a second run, and the one command is the whole upgrade", bitbucketTokenAccessor, stop.Why)
		}
	}
	if got := RetireAllowDelete(planDeleting(bitbucketTokenAccessor), rets); !slices.Contains(got, bitbucketTokenAccessor) {
		t.Errorf("the guard's allow-list for the flip's plan = %v, want %s among them: the retirement's own addresses are what the update may delete in the run that retires them (D502, the HARD RULE)", got, bitbucketTokenAccessor)
	}
}

// TestProxiedTokenAccessorAllowDeleteIsTheFlipsOwn is the unmanaged half of
// the test above: the guard's allow-list for a project that is NOT managed,
// which takes the ordinary plan. That path reads the retirement set and NAMES
// it -- nothing removes or deletes it for the user there -- so the plan that
// carries the flip's delete reached the guard with nothing to allow it and the
// run stopped, `--allow-delete` and a second command the only ways past: the
// HARD RULE broken for exactly the installations that never opted into a
// managed project (issue #2166's review).
//
// The rows are the filter's whole surface. The address this spec stopped
// producing is admitted; a name the service does not hold, a variant the
// configuration has left (that retirement IS what --retire is for), a create
// rather than a delete, and an address outside the repository root's module
// are not. The two clauses after the table are the guard's own inputs: a spec
// that drops nothing admits nothing, and neither does a plan the run could not
// read.
//
// The fixture is the sandbox as an `api-key` repository with one provider
// allowed to send its code, which is what gives the kept row an address to be
// about: B3.8's flip fetches the workflow's OWN secret (sandbox-probe, so its
// accessor is one this tree no longer produces either), and the two model
// credentials go with the arm, so a PROVIDER's key — mounted for a run whose
// route forwards the value rather than attaching it (R6, D1634) — is the one
// name of this job the credentials service does not hold.
func TestProxiedTokenAccessorAllowDeleteIsTheFlipsOwn(t *testing.T) {
	in := sandboxInputs(t, m5Additions+proxiedLocalEntry+proxiedProviderEntry)
	in.Cfg.Agent.Auth = config.AuthAPIKey
	serviceReady(t, in.LC, in.Repo)
	in.Installation.CredentialsService = credentialsService()
	node := publishedNode(t, true, "acme/sandbox")
	node.Budget = projectpolicy.Budget{Mode: config.BudgetEnforce}
	in.Policy = node
	spec, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if !spec.DropsBitbucketToken() {
		t.Fatalf("the fixture's spec does not drop the token (BitbucketProxied = %v), so every row below is about a run the flip is not", spec.BitbucketProxied)
	}
	if produced := spec.Addresses(); slices.Contains(produced, bitbucketTokenAccessor) {
		t.Fatalf("this tree's spec still produces %s, so the positive row below would admit an address the plan has no reason to delete", bitbucketTokenAccessor)
	}
	kept := `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["openrouter-api-key"]`
	if !slices.Contains(spec.Addresses(), kept) {
		t.Fatal("the fixture's web workflow produces no accessor for the provider key, so the row about a name the service does not hold would pass for the wrong reason")
	}
	gone := strings.Replace(bitbucketTokenAccessor, `module.workflow["web"]`, `module.workflow["gone"]`, 1)
	installation := strings.Replace(bitbucketTokenAccessor, "module.repo.", "module.installation.", 1)
	for _, tc := range []struct {
		what string
		plan *tf.Plan
		addr string
		want bool
	}{
		{"the accessor this spec stopped producing", planDeleting(bitbucketTokenAccessor), bitbucketTokenAccessor, true},
		{"a name the credentials service does not hold", planDeleting(kept), kept, false},
		{"a variant the configuration no longer deploys", planDeleting(gone), gone, false},
		{"the same address created, not deleted", &tf.Plan{ResourceChanges: []tf.ResourceChange{change(bitbucketTokenAccessor, "create")}}, bitbucketTokenAccessor, false},
		{"an address outside the repository root's module", planDeleting(installation), installation, false},
	} {
		got := ProxiedTokenAccessorAllowDelete(tc.plan, spec)
		if slices.Contains(got, tc.addr) != tc.want {
			t.Errorf("%s: admitted = %v, want %v (allow-list %v)", tc.what, !tc.want, tc.want, got)
		}
	}

	// A spec that drops nothing, the other direction of the same predicate: the
	// node admits another workspace's repository, so this one keeps its mount
	// and its accessor, and the operator's entry is absent so that the two
	// sources of the fact cannot disagree about it.
	notProxied, err := Repo(proxiedSandboxInputs(t, "", publishedNode(t, true, "workspace-b/other")))
	if err != nil {
		t.Fatal(err)
	}
	if notProxied.DropsBitbucketToken() {
		t.Fatalf("a node that proxies nothing still drops the token, so the first clause below is never read")
	}
	if got := ProxiedTokenAccessorAllowDelete(planDeleting(bitbucketTokenAccessor), notProxied); len(got) != 0 {
		t.Errorf("the allow-list for a repository that loses no accessor = %v, want none: admitting an address the plan's own spec still produces is how a real delete of somebody else's grant would slip through", got)
	}
	if got := ProxiedTokenAccessorAllowDelete(nil, spec); len(got) != 0 {
		t.Errorf("the allow-list for a run that read no plan = %v, want none", got)
	}
}

// TestVaultAccessorAllowDeleteIsTheFlipsOwn is B3.8's own half of the test
// above: the guard's allow-list for the class of secret the credentials service
// FETCHES, on a project that is not managed, which is the ordinary plan. Its
// fixture is deliberately NOT a proxied repository — the two flips are separate
// (G22) and the fetched class is the one that has nothing to do with the
// Bitbucket token, so a list that only answered next to the proxy would leave
// every other switched repository's update owed a flag for the workspace
// secret's own accessor (CLAUDE.md's HARD RULE).
//
// The rows are that filter's whole surface: the accessor this spec stopped
// producing (sandbox-probe, which the flip fetches and the job no longer
// mounts) is admitted; a name the service does not fetch, a variant the
// configuration has left, a create rather than a delete and an address outside
// the repository root's module are not. The clauses after the table are the
// guard's own inputs (an unfilled class list admits nothing, so does an
// unswitched repository, and so does a plan the run could not read), and the
// last one is the flip-back the class list's comment describes.
func TestVaultAccessorAllowDeleteIsTheFlipsOwn(t *testing.T) {
	if !config.VaultFetchesClass(config.VaultSecretClassWorkflow) {
		t.Fatalf("config.VaultFetchedSecretClasses = %v and does not carry %q: every row below is about the flip being filled (D2072, G17)", config.VaultFetchedSecretClasses, config.VaultSecretClassWorkflow)
	}
	switched, err := Repo(proxiedSandboxInputs(t, "", publishedNode(t, true, "workspace-b/other")))
	if err != nil {
		t.Fatal(err)
	}
	if switched.BitbucketProxied {
		t.Fatalf("the fixture is proxied, so its rows would be about the other flip's list as well: this one's has to answer without it (G22)")
	}
	if switched.Installation.CredentialsService == nil {
		t.Fatal("the fixture is not switched: nothing below would be about a repository whose job fetches its secrets")
	}
	// The name the flip fetches: the workflow's own secret, which the drop took
	// off the job and the module's job_secret_names clause drops the accessor
	// of in the same plan.
	fetched := `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["sandbox-probe"]`
	if got := switched.Workflows["web"].VaultSecretNames; !slices.Contains(got, "sandbox-probe") {
		t.Fatalf("the fixture's web workflow does not fetch sandbox-probe (%v): the test would be vacuous", got)
	}
	if slices.Contains(switched.Addresses(), fetched) {
		t.Fatalf("this tree's spec still produces %s, so the positive row below would admit an address the plan has no reason to delete", fetched)
	}
	// A name the service does not fetch for this workflow: the job's own
	// Bitbucket token, which on a repository the node does not proxy stays
	// mounted with its accessor.
	kept := `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["bitbucket-token"]`
	if !slices.Contains(switched.Addresses(), kept) {
		t.Fatalf("the fixture's spec produces no %s, so the row about a name the service does not fetch would pass for the wrong reason", kept)
	}
	gone := strings.Replace(fetched, `module.workflow["web"]`, `module.workflow["gone"]`, 1)
	installation := strings.Replace(fetched, "module.repo.", "module.installation.", 1)
	for _, tc := range []struct {
		what string
		plan *tf.Plan
		addr string
		want bool
	}{
		{"the accessor this spec stopped producing", planDeleting(fetched), fetched, true},
		{"a name the service does not fetch", planDeleting(kept), kept, false},
		{"a variant the configuration no longer deploys", planDeleting(gone), gone, false},
		{"the same address created, not deleted", &tf.Plan{ResourceChanges: []tf.ResourceChange{change(fetched, "create")}}, fetched, false},
		{"an address outside the repository root's module", planDeleting(installation), installation, false},
	} {
		got := VaultAccessorAllowDelete(tc.plan, switched)
		if slices.Contains(got, tc.addr) != tc.want {
			t.Errorf("%s: admitted = %v, want %v (allow-list %v)", tc.what, !tc.want, tc.want, got)
		}
	}

	// The flip's inert state, read where the cli reads it: with the class list
	// empty this tree fetches nothing for anybody, so no plan's delete of an
	// accessor is the flip's own.
	off := vaultSpecUnswitched(t)
	if off.Installation.CredentialsService != nil {
		t.Fatal("the unswitched fixture carries a credentials service: nothing below would be about a repository the switch holds off")
	}
	if got := VaultAccessorAllowDelete(planDeleting(fetched), off); len(got) != 0 {
		t.Errorf("the allow-list for a repository the switch holds off = %v, want none: a job with no address fetches nothing, so a delete of this address is somebody's real change and the guard judges it as before", got)
	}
	old := config.VaultFetchedSecretClasses
	config.VaultFetchedSecretClasses = nil
	t.Cleanup(func() { config.VaultFetchedSecretClasses = old })
	if got := VaultAccessorAllowDelete(planDeleting(fetched), switched); len(got) != 0 {
		t.Errorf("the allow-list with the class list empty = %v, want none: this is the state the release before the flip ran, and an allow-list that still admitted its delete would be one that admits a delete no plan of this tree makes", got)
	}
	config.VaultFetchedSecretClasses = old
	if got := VaultAccessorAllowDelete(nil, switched); len(got) != 0 {
		t.Errorf("the allow-list for a run that read no plan = %v, want none", got)
	}
}

// TestBuildAccessorAllowDeleteIsTheFlipsOwn is B4.9's half of the two tests
// above: the guard's allow-list for the BUILD account's accessors, on a project
// that is not managed, which is the ordinary plan. Its fixture is deliberately
// NOT a proxied repository as well (the flips are separate, G22) and it is a
// CASCADE one, so the only secrets the build's list ever names are the git
// credential and the workflow's own build secrets — the repositories the class
// "build" moves, and no model credential.
//
// The rows are that filter's whole surface: an accessor this spec stopped
// producing (the build account's on sandbox-probe, the workflow's own build
// secret, which the flip serves from the credentials service instead) is
// admitted; a logical name this repository no longer declares, a create rather
// than a delete, an address under another module and one of the workflow
// module's own job accessors are not. The clauses after the table are the
// guard's own inputs (an unswitched repository admits nothing, the class list
// emptied admits nothing, and so does a plan the run could not read).
func TestBuildAccessorAllowDeleteIsTheFlipsOwn(t *testing.T) {
	if !config.BuildServesClass(config.BuildSecretClassBuild) {
		t.Fatalf("config.BuildServedSecretClasses = %v and does not carry %q: every row below is about the flip being filled (D2076, D2077)", config.BuildServedSecretClasses, config.BuildSecretClassBuild)
	}
	switched, err := Repo(proxiedSandboxInputs(t, "", publishedNode(t, false, "workspace-b/other")))
	if err != nil {
		t.Fatal(err)
	}
	if switched.BitbucketProxied {
		t.Fatalf("the fixture is proxied, so its rows would be about the other flip's list as well: this one's has to answer without it (G22)")
	}
	if !switched.BuildMountsServed() {
		t.Fatal("the fixture's build is not served: nothing below would be about a repository whose build reads its secrets from the service")
	}
	if len(switched.BuildSecrets) != 0 {
		t.Fatalf("the served fixture's build list is %v, want none: the module declares no accessor of this resource at all once the class is served, and the rows below read the spec's own answer", switched.BuildSecrets)
	}
	// The two names the build's list carried before the drop: the provider
	// credential and the workflow's own build secret (sandbox-probe, which the
	// same document mounts into the build).
	buildAddr := func(name string) string {
		return `module.repo.google_secret_manager_secret_iam_member.build["` + name + `"]`
	}
	dropped := buildAddr("sandbox-probe")
	git := buildAddr(config.BitbucketSecret)
	for _, name := range []string{"sandbox-probe", config.BitbucketSecret} {
		if _, declared := switched.Secrets[name]; !declared {
			t.Fatalf("the fixture's spec declares no %s, so the row about its accessor would pass for the wrong reason: the allowance reads spec.Secrets for the names it admits", name)
		}
		if slices.Contains(switched.Addresses(), buildAddr(name)) {
			t.Fatalf("this tree's spec still produces %s, so the positive row below would admit an address the plan has no reason to delete", buildAddr(name))
		}
	}
	gone := `module.repo.google_secret_manager_secret_iam_member.build["some-other-secret"]`
	jobAddr := `module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["sandbox-probe"]`
	otherModule := strings.Replace(dropped, "module.repo.", "module.installation.", 1)
	for _, tc := range []struct {
		what string
		plan *tf.Plan
		addr string
		want bool
	}{
		{"the accessor this spec stopped producing", planDeleting(dropped), dropped, true},
		{"the provider credential's own", planDeleting(git), git, true},
		{"a name this repository no longer declares", planDeleting(gone), gone, false},
		{"a job account's accessor in the workflow module", planDeleting(jobAddr), jobAddr, false},
		{"the same address created, not deleted", &tf.Plan{ResourceChanges: []tf.ResourceChange{change(dropped, "create")}}, dropped, false},
		{"an address outside the repository root's module", planDeleting(otherModule), otherModule, false},
	} {
		got := BuildAccessorAllowDelete(tc.plan, switched)
		if slices.Contains(got, tc.addr) != tc.want {
			t.Errorf("%s: admitted = %v, want %v (allow-list %v)", tc.what, !tc.want, tc.want, got)
		}
	}

	// The repository the switch holds off: its build keeps every accessor, and
	// an address the spec still produces is somebody's real change.
	off := vaultSpecUnswitched(t)
	if off.Installation.CredentialsService != nil {
		t.Fatal("the unswitched fixture carries a credentials service: nothing below would be about a repository the switch holds off")
	}
	if gots := BuildAccessorAllowDelete(planDeleting(dropped), off); len(gots) != 0 {
		t.Errorf("the allow-list for a repository the switch holds off = %v, want none: its build mounts every secret it ever did, so a delete of this address is somebody's real change and the guard judges it as before", gots)
	}
	old := config.BuildServedSecretClasses
	config.BuildServedSecretClasses = nil
	t.Cleanup(func() { config.BuildServedSecretClasses = old })
	if gots := BuildAccessorAllowDelete(planDeleting(dropped), switched); len(gots) != 0 {
		t.Errorf("the allow-list with the class list empty = %v, want none: this is the state the release before the flip ran, and an allow-list that still admitted its delete would be one that admits a delete no plan of this tree makes", gots)
	}
	config.BuildServedSecretClasses = old
	if gots := BuildAccessorAllowDelete(nil, switched); len(gots) != 0 {
		t.Errorf("the allow-list for a run that read no plan = %v, want none", gots)
	}
}

// TestBuildMountsServedIsTheDropThePlanMakes holds RepoSpec.BuildMountsServed
// to the drop the plan actually makes, the way TestDropsBitbucketTokenIsTheDropThePlanMakes
// holds that flip's predicate to its own: the guard's allowance reads it before
// it admits the flip's accessor deletes, so an answer that says true while the
// spec kept every accessor would be a licence for any of them, and one that
// says false while the list emptied would owe the operator a flag for the
// flip's own delete (CLAUDE.md's HARD RULE).
//
// The two rows are the served fixture (the list is empty, and the predicate
// says so) and the repository the switch holds off (the list is the build's
// own, and it says that) — a condition added to one side and not the other
// fails here rather than in a plan.
func TestBuildMountsServedIsTheDropThePlanMakes(t *testing.T) {
	served, err := Repo(proxiedSandboxInputs(t, "", publishedNode(t, false, "workspace-b/other")))
	if err != nil {
		t.Fatal(err)
	}
	off := vaultSpecUnswitched(t)
	for _, tc := range []struct {
		what string
		spec RepoSpec
	}{
		{"a repository the service serves", served},
		{"a repository the switch holds off", off},
	} {
		if dropped := len(tc.spec.BuildSecrets) == 0; dropped != tc.spec.BuildMountsServed() {
			t.Errorf("%s: BuildMountsServed() = %v while its build list is %v, and the two are one answer (the build's list is never empty otherwise: it always names the provider credential)",
				tc.what, tc.spec.BuildMountsServed(), tc.spec.BuildSecrets)
		}
	}
	if off.Installation.CredentialsService != nil {
		t.Fatal("the unswitched fixture carries a credentials service: the second row would then be about a served repository too")
	}
	if len(off.BuildSecrets) == 0 {
		t.Fatal("the unswitched fixture's build list is empty, so the row above would read as agreement for the wrong reason: the list always names the provider credential")
	}
	for _, name := range off.BuildSecrets {
		if _, declared := off.Secrets[name]; !declared {
			t.Errorf("the unswitched fixture's build list names %s, which its spec does not declare: the two sides of the predicate's own row disagree about what the build mounts", name)
		}
	}
}

// TestDropsBitbucketTokenIsTheDropThePlanMakes holds RepoSpec.DropsBitbucketToken
// to the drop the plan actually makes. The predicate is what `fugaro update`'s
// gate reads before it applies a repository root (O8, internal/cli's
// update_proxied.go), and it has to answer for exactly the plans Readiness
// takes the token out of: a gate answering false while the apply dropped the
// mount is the stranded repository the ruling forbids, and one answering true
// while nothing was dropped refuses an upgrade nobody was at risk of.
//
// The three rows are the tests above, read through it: the proxied repository
// (the drop is made, and the predicate says so), a repository the node does not
// admit, and an installation that did not opt in (nothing is dropped, and it
// says that) — so a condition added to one side and not the other fails here
// rather than in a plan.
func TestDropsBitbucketTokenIsTheDropThePlanMakes(t *testing.T) {
	if !slices.Contains(config.ProxiedGitSecretNames, config.BitbucketSecret) {
		t.Fatalf("config.ProxiedGitSecretNames = %v does not name %s: the drop this test holds the predicate to does not happen on this tree, and every row below would read as agreement", config.ProxiedGitSecretNames, config.BitbucketSecret)
	}
	notOptedIn := func() Inputs {
		in := sandboxInputs(t, m5Additions+proxiedLocalEntry)
		in.Policy = publishedNode(t, true, "acme/sandbox")
		return in
	}
	for name, tc := range map[string]struct {
		in   Inputs
		want bool
	}{
		"a repository the node admits with proxy: true": {proxiedSandboxInputs(t, proxiedLocalEntry, publishedNode(t, true, "acme/sandbox")), true},
		"a repository the node does not admit":          {proxiedSandboxInputs(t, "", publishedNode(t, true, "workspace-b/other")), false},
		"an installation that did not opt in":           {notOptedIn(), false},
	} {
		spec, err := Repo(tc.in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := spec.DropsBitbucketToken()
		ready, _, _ := readinessOf(t, newCloud(t), spec)
		_, mounted := ready.Workflows["web"].SecretEnv[bitbucketTokenEnv]
		if got != tc.want {
			t.Errorf("%s: DropsBitbucketToken = %v, want %v: the gate on the flip's drop reads this answer, and it is the plan's own condition (the installation opted in, the node proxies this repository, and %s is on config.ProxiedGitSecretNames)", name, got, tc.want, config.BitbucketSecret)
		}
		if got == mounted {
			t.Errorf("%s: Readiness left the mount: %v, and the predicate says the drop is made: %v — the two answer one question, and a plan that dropped a mount this refused would be a repository whose runs are refused at their first git call", name, mounted, got)
		}
	}
}

// TestNilPolicyJudgesNoEntry pins the contract Inputs.Policy's own comment
// states, and the branch it protects: a caller that read no node at all
// (fugaro image, fugaro doctor, --print-vars) must not refuse a local entry
// over a document it never saw, while a caller that read one — even a project
// that has published no policy, which the CLI passes as an empty node —
// judges the entry against it. The two are one nil away from each other, and
// the wrong one is a refusal an operator cannot act on.
func TestNilPolicyJudgesNoEntry(t *testing.T) {
	spec, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, nil))
	if err != nil {
		t.Errorf("Repo with no node read = %v, want no error: a caller that read no node judges no entry, and the entry here disagrees with the node the project may have published", err)
	} else if spec.BitbucketProxied {
		t.Errorf("BitbucketProxied = true with no node read, want false: nothing was read to make it true")
	}

	// The same inputs with a node that was read and carries no block: the
	// entry's proxy: true has nothing to agree with, and is refused.
	if _, err := Repo(proxiedSandboxInputs(t, proxiedLocalEntry, &projectpolicy.Policy{})); err == nil {
		t.Error("Repo with an empty node that was read = no error, want a refusal: \"read, and the project published no block\" is a different answer from \"not read\", and it is what judges this entry")
	}
}

// TestGitHubRepositoryIsNeverJudgedAgainstTheBitbucketEntry: the fact and the
// refusal are the Bitbucket token's, so a GitHub repository is neither proxied
// nor refused, whatever its name matches in the local entry. The entry holds
// bare owner/name slugs, so a GitHub repository called the same as one of them
// would otherwise be refused over a credential it does not use — its git
// credential is the App key (D145).
func TestGitHubRepositoryIsNeverJudgedAgainstTheBitbucketEntry(t *testing.T) {
	in := webappInputs(t)
	in.Installation.CredentialsService = credentialsService()
	in.LC.ProjectSecrets = map[string]config.ProjectSecret{
		config.BitbucketSecret: {AllowDataTo: []string{"acme/webapp"}, Proxy: config.ProxyFlag(true)},
	}
	// The node admits the same owner/name, and only as a bitbucket repository:
	// MatchRepo compares the provider segment, so this never admits a GitHub
	// repository and the fact is false for one whatever the entry says.
	in.Policy = publishedNode(t, true, "acme/webapp")
	spec, err := Repo(in)
	if err != nil {
		t.Fatalf("Repo for a GitHub repository with a proxying project_secrets.%s entry = %v, want no error: the Bitbucket token is not this repository's credential, and its entry is not judged against one", config.BitbucketSecret, err)
	}
	if spec.BitbucketProxied {
		t.Error("BitbucketProxied = true for a GitHub repository, want false: the node's block admits bitbucket repositories only")
	}
	if _, present := repoBitbucketProxied(t, spec); present {
		t.Error("the tfvars carry repo.bitbucket_proxied for a GitHub repository, want the key omitted")
	}
}

// catalogYAML is a repository whose workflows exercise every arm of the
// catalog's selection (reconciliation §3.1): one that needs few enough tags
// for the narrowest entry to cover, one that needs a tag only the widest
// entry carries, one that names nothing and so runs the project's default,
// and one that pins an entry that does not cover what it needs.
const catalogYAML = `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: vertex }
workflows:
  api:
    needs: [postgres, redis]
    commands: { build: npm run build, test: npm test }
  web:
    needs: [node, firebase-emulator]
    commands: { build: npm run build, test: npm test }
  bare:
    commands: { build: npm run build, test: npm test }
  pinned:
    image_name: minimal
    needs: [node]
    commands: { build: npm run build, test: npm test }
`

// catalogEntries are the catalog's three entries: minimal covers a
// database-only job with the fewest tags, standard is what a repository that
// names nothing runs, and universal is the only one covering
// firebase-emulator.
func catalogEntries() []config.Entry {
	return []config.Entry{
		{Name: "minimal", Tags: []string{"postgres", "redis"}, Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/images", Path: "minimal/Dockerfile"}},
		{Name: "standard", Default: true, Tags: []string{"node", "postgres", "redis"}, Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/images", Path: "standard/Dockerfile"}},
		{Name: "universal", Tags: []string{"node", "postgres", "redis", "firebase-emulator"}, Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/images", Path: "universal/Dockerfile"}},
	}
}

// catalogInputs is a GitHub repository whose project publishes catalog under
// its policy node.
func catalogInputs(t *testing.T, published *config.Catalog) Inputs {
	t.Helper()
	lc := parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [api, web, bare, pinned], github_app_id: "123456" }
`)
	return Inputs{
		LC:           lc,
		Repo:         "acme/webapp",
		Cfg:          parseCfg(t, []byte(catalogYAML)),
		Installation: installationOutputs(),
		Policy:       &projectpolicy.Policy{ImageCatalog: published},
	}
}

// TestSpecBakedWorkflowRunsTheCatalogsChoice: the job's image is the
// selection's answer, computed here where the job spec is built (F7), and no
// longer the repository's own derived :latest tag.
//
// Mutations (run, restore): in workflow(), drop the catalog's arm so Image
// stays the derived tag — every image assertion below fails with
// ".../acme-webapp-<hash>/<workflow>:latest".
func TestSpecBakedWorkflowRunsTheCatalogsChoice(t *testing.T) {
	in := catalogInputs(t, &config.Catalog{Entries: catalogEntries()})
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	shared := in.Installation.RegistryHost + "/" + gcp.SharedRegistryRepoID()
	for name, want := range map[string]string{
		"api":    shared + "/catalog-minimal:latest",
		"web":    shared + "/catalog-universal:latest",
		"bare":   shared + "/catalog-standard:latest",
		"pinned": shared + "/catalog-minimal:latest",
	} {
		if got := rs.Workflows[name].Image; got != want {
			t.Errorf("%s's image = %q, want %q", name, got, want)
		}
	}
	if got := rs.Workflows["pinned"].Warnings; len(got) != 1 || !strings.Contains(got[0], "minimal") || !strings.Contains(got[0], "node") {
		t.Errorf("the pinned workflow's warnings = %q, want one naming the entry and the tag it does not cover (D1146)", got)
	}
	for _, name := range []string{"api", "web", "bare"} {
		if len(rs.Workflows[name].Warnings) != 0 {
			t.Errorf("%s's warnings = %q, want none: only a pin that does not cover warns", name, rs.Workflows[name].Warnings)
		}
	}
}

// A prebuilt catalog entry used to reach a job here, and #1752's two tests —
// TestSpecPrebuiltCatalogEntryCarriesTheRemoteEnv and
// TestSpecPrebuiltCatalogEntryRemoteIsCredentialFree — pinned the clone shape
// that job carried (FUGARO_REMOTE and no FUGARO_CHECKOUT). R1 (#1698, D1191)
// refuses a prebuilt entry on a baked workflow where the spec is built, and
// the only two checkout kinds that leave the catalog inert — clone and shared
// — are the resolver's base and shared layers, so jobImage returns an empty
// entry for them and WorkflowSpec.CatalogPrebuilt is never set from this path:
// the remote's `|| ws.CatalogPrebuilt` clause is unreachable, and no allowed
// configuration exercises it. The behaviour those tests were about — the
// remote on a job whose image holds no checkout, credential-free — stays
// covered by TestSpecCloneSetsRemoteEnv and TestSpecSharedRemoteIsCredentialFree.

// TestSpecBuiltCatalogEntryCarriesNoRemoteEnv (#1752): the remote is for a job
// whose image bakes no checkout, and a built entry's image is this
// repository's own build — the same Dockerfile the catalog builds into the
// shared registry, with the repository baked into it — so its job carries no
// FUGARO_REMOTE and nothing about a built entry's runs changes.
//
// Mutation (run, restore): widen the remote's condition to every catalog job
// (`entry.Name != ""`) — this test then fails: api, web, bare and pinned each
// carry a remote their image already holds a checkout for.
func TestSpecBuiltCatalogEntryCarriesNoRemoteEnv(t *testing.T) {
	rs, err := Repo(catalogInputs(t, &config.Catalog{Entries: catalogEntries()}))
	if err != nil {
		t.Fatal(err)
	}
	for name, ws := range rs.Workflows {
		if ws.CatalogPrebuilt {
			t.Fatalf("workflow %s runs a prebuilt entry: this test would prove nothing", name)
		}
		if got, ok := ws.Env[RemoteEnv]; ok {
			t.Errorf("%s runs a built entry and carries %s = %q, want no such key: its image bakes the repository", name, RemoteEnv, got)
		}
	}
}

// TestSpecRefusesAJobNoCatalogEntryCovers: §3.3's first refusal is raised
// where the job spec is built, so the job is never deployed, and it says
// which tags are missing and what the catalog does cover.
func TestSpecRefusesAJobNoCatalogEntryCovers(t *testing.T) {
	in := catalogInputs(t, &config.Catalog{Entries: catalogEntries()})
	w := in.Cfg.Workflows["api"]
	w.Needs = []string{"postgres", "cuda"}
	in.Cfg.Workflows["api"] = w
	_, err := Repo(in)
	if err == nil {
		t.Fatal("a workflow needing a tag no entry covers was accepted")
	}
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a UserError: the fix is the project's catalog", err)
	}
	for _, want := range []string{
		"no image in this project's catalog covers the tags this job needs: [postgres cuda]",
		"the catalog's images cover: minimal [postgres redis], standard [node postgres redis], universal [node postgres redis firebase-emulator]",
		"fugaro image add <name> --tags postgres cuda",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not carry %q", err.Error(), want)
		}
	}
}

// TestSpecCatalogIsTheProjectsOwnWhenTheLocalConfigWritesOne is D1140's keep
// rule at this seam: a local config that writes a catalog is what the specs
// are computed from, and the published node is not consulted for it. Both
// entries of the local catalog are built ones, because a prebuilt entry could
// not reach a baked workflow's job at all (D1191).
func TestSpecCatalogIsTheProjectsOwnWhenTheLocalConfigWritesOne(t *testing.T) {
	in := catalogInputs(t, &config.Catalog{Entries: catalogEntries()})
	in.LC.ImageCatalog = &config.Catalog{Entries: []config.Entry{
		{Name: "minimal", Tags: []string{"postgres", "redis"}, Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/images", Path: "minimal/Dockerfile"}},
		{Name: "local-default", Default: true, Tags: []string{"node", "postgres", "redis", "firebase-emulator"}, Source: config.Source{Kind: config.SourceInline, Inline: "FROM scratch\n"}},
	}}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	shared := in.Installation.RegistryHost + "/" + gcp.SharedRegistryRepoID()
	if got, want := rs.Workflows["bare"].Image, shared+"/catalog-local-default:latest"; got != want {
		t.Errorf("bare's image = %q, want %q: the local config's catalog is the effective one (D1140)", got, want)
	}
	if got, want := rs.Workflows["api"].Image, shared+"/catalog-minimal:latest"; got != want {
		t.Errorf("api's image = %q, want %q", got, want)
	}
}

// prebuiltCatalogEntries are the same three entries with a prebuilt image
// source: nothing in Fugaro builds them, so a baked workflow whose image one
// of them would be is refused (D1191) and there is nothing of theirs for a
// check to rebuild either.
func prebuiltCatalogEntries() []config.Entry {
	return []config.Entry{
		{Name: "minimal", Tags: []string{"postgres", "redis"}, Source: config.Source{Kind: config.SourceImage, Image: "example.invalid/catalog-minimal:1"}},
		{Name: "standard", Default: true, Tags: []string{"node", "postgres", "redis"}, Source: config.Source{Kind: config.SourceImage, Image: "example.invalid/catalog-standard:1"}},
		{Name: "universal", Tags: []string{"node", "postgres", "redis", "firebase-emulator"}, Source: config.Source{Kind: config.SourceImage, Image: "example.invalid/catalog-universal:1"}},
	}
}

// TestSpecCatalogOnlyRepoNeedsNoBaseImage: the daily check rebuilds the
// repository's own derived image, and a workflow a catalog entry gives its
// image to never runs one — a built entry's image is the catalog's own
// project-level check's to rebuild (D1148), never this repository's. Such a
// workflow is therefore never checked here, so a repository whose every
// workflow runs a catalog entry needs no base_images entry at all and
// fugaro update --repo must not refuse it for one (issue 1661 item 3).
//
// The entries are built ones only: a prebuilt entry on a baked workflow is
// refused before any of this is computed (D1191).
func TestSpecCatalogOnlyRepoNeedsNoBaseImage(t *testing.T) {
	in := catalogInputs(t, &config.Catalog{Entries: catalogEntries()})
	delete(in.LC.BaseImages, config.BaseKind)
	rs, err := Repo(in)
	if err != nil {
		t.Fatalf("Repo = %v, want no error: no workflow of this repository runs a per-repository image", err)
	}
	if rs.Check != nil {
		t.Errorf("the repository has a daily image check job %s, want none: every workflow runs a catalog entry", rs.Check.Job)
	}
}

// TestSpecCheckCoversTheWorkflowsWithoutACatalogImage: the exclusion is per
// workflow, not per repository. The project marks no default, which is the one
// state a workflow that names no entry and needs no tag is not the selection's
// (O4 b): its job keeps the image this project builds for it, so the repository
// keeps its daily check and the check covers that workflow alone.
//
// The default is what it turns on: with one marked, every workflow of the file
// runs it whatever its own keys say (D1900), and no workflow of the repository
// is left for the check. That is why the entry standard is unmarked here and
// bare — the workflow that names nothing — is pinned instead of left alone.
//
// Every entry here is one Fugaro builds, so that the one workflow the check
// covers is not the catalog's for the reason this test is about: a prebuilt
// entry on a baked workflow is refused before any of this is computed (D1191).
func TestSpecCheckCoversTheWorkflowsWithoutACatalogImage(t *testing.T) {
	entries := catalogEntries()
	for i := range entries {
		if entries[i].Name == "standard" {
			entries[i].Default = false
		}
	}
	in := catalogInputs(t, &config.Catalog{Entries: entries})
	w := in.Cfg.Workflows["api"]
	w.Needs = nil
	w.Image = config.Image{Tools: config.MiseTools{"node": "24"}}
	in.Cfg.Workflows["api"] = w
	bare := in.Cfg.Workflows["bare"]
	bare.ImageName = "standard"
	in.Cfg.Workflows["bare"] = bare
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check == nil || !slices.Equal(rs.Check.Workflows, []string{"api"}) {
		t.Fatalf("the daily check covers %v, want only the workflow that builds an image of its own", rs.Check)
	}
}

// TestBakedWorkflowRefusesAPrebuiltCatalogEntry: a checkout: baked job's image
// has to hold this repository's checkout, and a prebuilt catalog entry is the
// one source kind nothing guarantees that of (D1191). The refusal belongs where
// the spec is built, because the entry is chosen and the image written there
// (F7, F14): the job a repository without it gets holds no checkout and no
// remote and fails at bootstrap, with nothing naming the entry it runs
// (unbuiltOnBaked's own comment).
//
// The three shapes are the three ways an entry reaches a workflow's job — a
// pin, the tag-cover rule and the project's default — because the refusal reads
// the entry the selection chose and not the key the workflow wrote: an entry
// the catalog gains after this refusal was written is refused by it too.
//
// The fourth states the checkout kind as the empty value, which is the reading
// the refusal has to take: every workflow a config file produces carries
// baked, clone or shared by the time it is validated (applyDefaults writes
// baked for an empty one), so the empty value is what a config that never met
// the loader's defaults carries — and it is a job the runner provisions as
// baked, which is why a guard asking equality with baked would deploy the very
// job this rule exists to stop.
func TestBakedWorkflowRefusesAPrebuiltCatalogEntry(t *testing.T) {
	for _, tt := range []struct{ name, workflow, entry, checkout string }{
		{"a pinned entry", "pinned", "minimal", config.CheckoutBaked},
		{"an entry the tags chose", "api", "minimal", config.CheckoutBaked},
		{"the project's default", "bare", "standard", config.CheckoutBaked},
		{"a checkout the loader's defaults never filled in", "bare", "standard", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := catalogInputs(t, &config.Catalog{Entries: prebuiltCatalogEntries()})
			w := in.Cfg.Workflows[tt.workflow]
			w.Checkout = tt.checkout
			in.Cfg.Workflows = map[string]config.Workflow{tt.workflow: w}
			_, err := Repo(in)
			if err == nil {
				t.Fatalf("the workflow %s runs the prebuilt entry %s and was accepted, so its job would deploy on an image that holds no checkout", tt.workflow, tt.entry)
			}
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a UserError: the fix is the project's catalog", err)
			}
			for _, want := range []string{
				"workflow " + tt.workflow,
				"the catalog entry " + tt.entry,
				"source kind image",
				"checkout: clone",
				// The clone half has to name the keys it costs: a workflow the
				// refusal sends to clone may be carrying image_name: or needs:,
				// and checkout: clone refuses both, so a refusal that did not
				// say so would send its reader to a config validate: refuses
				// next.
				"image_name: or needs:",
				"a catalog entry Fugaro builds",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not carry %q", err.Error(), want)
				}
			}
		})
	}
}

// TestBakedWorkflowKeepsItsOwnImageBesideAPrebuiltDefault is the refusal
// above read against this release's D1900, and the shape the HARD RULE on
// `fugaro update` is about: the same prebuilt default and the same baked
// workflow, with one difference — the workflow wrote a dockerfile: of its own.
// On the release behind such a workflow was left out of the catalog entirely
// and ran the image this project builds for it; D1900 routes it through the
// catalog, so without the resolver's fallback this configuration would be
// refused here and `fugaro update` would stop and ask for a manual config edit.
//
// What it asserts is that the workflow keeps its own image — the derived tag
// this repository's build writes, with no catalog entry and no refusal — and
// that the warning says which of the two answers the job's image is: the
// person reading a run has to know that the catalog's default was set aside
// for this workflow.
func TestBakedWorkflowKeepsItsOwnImageBesideAPrebuiltDefault(t *testing.T) {
	in := catalogInputs(t, &config.Catalog{Entries: prebuiltCatalogEntries()})
	w := in.Cfg.Workflows["bare"]
	w.Dockerfile = ".fugaro/bare.Dockerfile"
	in.Cfg.Workflows = map[string]config.Workflow{"bare": w}
	rs, err := Repo(in)
	if err != nil {
		t.Fatalf("Repo = %v, want no error: a workflow that wrote a dockerfile: of its own keeps the image this project builds for it, while the project's prebuilt default is refused for a workflow that wrote none (D1191, D1900, the HARD RULE on update)", err)
	}
	ws := rs.Workflows["bare"]
	if ws.CatalogEntry != "" {
		t.Errorf("the workflow runs catalog entry %q, want none: the prebuilt default holds no checkout of this repository, so it is set aside for a job that wrote an image of its own", ws.CatalogEntry)
	}
	if !strings.Contains(ws.Image, "acme-webapp") || !strings.HasSuffix(ws.Image, ":latest") {
		t.Errorf("the workflow's image = %q, want this repository's own derived tag", ws.Image)
	}
	if len(ws.Warnings) != 1 || !strings.Contains(ws.Warnings[0], "dockerfile:") || !strings.Contains(ws.Warnings[0], "standard") {
		t.Errorf("the workflow's warnings = %q, want the one naming its dockerfile: and the default entry it set aside", ws.Warnings)
	}
	// The refusal above still holds for the same workflow with the key taken
	// away: the fallback is a workflow's own image, not a relaxation of D1191.
	without := in.Cfg.Workflows["bare"]
	without.Dockerfile = ""
	in.Cfg.Workflows = map[string]config.Workflow{"bare": without}
	if _, err := Repo(in); err == nil {
		t.Fatal("the same workflow without the dockerfile: was accepted, so the control above proves nothing about the fallback")
	}
}

// TestCloneWorkflowAcceptsAPrebuiltCatalogEntry is the control for the refusal
// above, and the reason it is not "prebuilt entries are refused": what the
// refusal is about is the baked checkout kind. The workflow is `bare` — one
// that names no entry and needs nothing — because checkout: clone and a catalog
// reference are refused together (internal/config), so a control built on the
// pinned or tag-covering workflow the refusal above covers would be refused
// before the catalog was asked. The check below holds that: the configuration
// this control runs on is one a repository can write, and the same one with a
// reference added is not. Its job runs the installation's base image and the
// catalog does not speak for it at all (the resolver's shared layer), so the
// entry the test above refuses is inert here and the spec is built.
func TestCloneWorkflowAcceptsAPrebuiltCatalogEntry(t *testing.T) {
	in := catalogInputs(t, &config.Catalog{Entries: prebuiltCatalogEntries()})
	w := in.Cfg.Workflows["bare"]
	w.Checkout = config.CheckoutClone
	in.Cfg.Workflows = map[string]config.Workflow{"bare": w}
	if ps := config.Validate(in.Cfg); len(ps) > 0 {
		t.Fatalf("the control's own configuration is refused before it reaches the catalog, so it proves nothing about a clone job: %v", ps)
	}
	refused := *in.Cfg
	withRef := refused.Workflows["bare"]
	withRef.Needs = []string{"jvm"}
	refused.Workflows = map[string]config.Workflow{"bare": withRef}
	if ps := config.Validate(&refused); len(ps) == 0 {
		t.Fatal("checkout: clone alongside needs: is accepted, so the check above cannot tell a clone shape from one a repository cannot write")
	}
	rs, err := Repo(in)
	if err != nil {
		t.Fatalf("Repo = %v, want no error: the refusal is a baked workflow's (D1191)", err)
	}
	if got, want := rs.Workflows["bare"].Image, in.LC.BaseImage(config.BaseKind); got != want {
		t.Errorf("the clone workflow's image = %q, want the installation's base image %q", got, want)
	}
	if got := rs.Workflows["bare"].CatalogEntry; got != "" {
		t.Errorf("the clone workflow runs catalog entry %q, want none: the catalog does not speak for a clone workflow", got)
	}
}

// TestPlanWorkflowJobMovesACatalogJobsImage: a baked job whose image the
// project's catalog chose is the third shape the mover accepts, and the only
// baked one (D1152, PIC5). Both halves of the move have to be catalog images,
// so the refusal a baked job's job gets before the catalog — never onto the
// base image, which holds no checkout of this repository — is unchanged.
func TestPlanWorkflowJobMovesACatalogJobsImage(t *testing.T) {
	shared := installationOutputs().RegistryHost + "/" + gcp.SharedRegistryRepoID() + "/catalog-"
	old, next := shared+"standard:latest", shared+"universal:latest"
	f, _, ws, owner := workflowJobCloud(t, func(j map[string]any) {
		container(j)["image"] = old
		envEntry(j)["value"] = config.CheckoutBaked
	})
	u, err := PlanWorkflowJob(context.Background(), f.c, "proj-1234", "us-east5", ws.Job, ws.Name, next, owner)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || !u.Changes() || u.OldImage() != old || u.NewImage() != next {
		t.Fatalf("plan: %+v, want the catalog's own image moving", u)
	}
	if img := container(f.run.JobJSON("proj-1234", "us-east5", ws.Job))["image"]; img != old {
		t.Fatalf("image = %v, want %s left alone by a plan", img, old)
	}

	// A catalog job's image must not move to the base image: the base holds
	// no checkout of this repository, whatever the job's own image is.
	f, _, ws, owner = workflowJobCloud(t, func(j map[string]any) {
		container(j)["image"] = old
		envEntry(j)["value"] = config.CheckoutBaked
	})
	u, err = PlanWorkflowJob(context.Background(), f.c, "proj-1234", "us-east5", ws.Job, ws.Name, oldBase, owner)
	if !errors.Is(err, ErrCheckJobShape) || u != nil {
		t.Fatalf("base image: %v %+v, want a shape refusal", err, u)
	}
}

// TestProjectSecretCandidateFromNode is SD17's read at the layer both of its
// readers share. The owner publishes the allow-list once
// (project_secrets.bitbucket-token.allow_data_to, derived into the node's
// bitbucket_token block), and a machine whose local config holds no such entry
// reads it there: the plan mounts the project's copy of the token
// (ProjectSecretCandidates, which Readiness resolves the mount from) and update
// --repo's secrets stage stores it there, instead of the operator copying the
// entry to every machine and every machine being handed a per-repository token.
//
// Each condition of the read has its own case below, because each one is the
// difference between a working repository and a credential used where it must
// not be.
func TestProjectSecretCandidateFromNode(t *testing.T) {
	bitbucketNode := &projectpolicy.Policy{
		Version:        projectpolicy.Version,
		AllowRepos:     []string{"bitbucket/acme/sandbox"},
		BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/sandbox"}},
	}
	t.Run("the node admits the repository this machine holds no entry for", func(t *testing.T) {
		in := sandboxInputs(t, m5Additions)
		if in.LC.ProjectSecrets != nil {
			t.Fatalf("the fixture carries project_secrets, so it cannot show the node being read: %v", in.LC.ProjectSecrets)
		}
		if _, ok := ProjectSecretCandidate(in.LC, "bitbucket-token", "acme/sandbox"); ok {
			t.Fatal("the local answer admits the repository, so the node half would prove nothing")
		}
		id, ok := ProjectSecretCandidateFromNode(in.LC, "bitbucket-token", "acme/sandbox", "bitbucket", bitbucketNode)
		if !ok || id != gcp.ProjectSecretID(in.LC.Name, "bitbucket-token") {
			t.Fatalf("ProjectSecretCandidateFromNode = %q, %v; want the project's copy of the token", id, ok)
		}
		// The half that makes the store safe: the plan mounts what the stage
		// stores. Readiness resolves the mount from this map, and the map is
		// built from the same predicate with the same two facts.
		in.Policy = bitbucketNode
		rs, err := Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := rs.ProjectSecretCandidates["bitbucket-token"]; got != id {
			t.Errorf("the spec's candidate = %q, want %q: the job would mount a container the stage does not store into", got, id)
		}
		// And with no node read the map is what it always was: a machine that
		// read no policy admits nobody through this source.
		in.Policy = nil
		rs, err = Repo(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rs.ProjectSecretCandidates["bitbucket-token"]; ok {
			t.Error("a caller that read no node got a candidate entry: nil is not an empty policy")
		}
	})
	t.Run("the provider must be bitbucket", func(t *testing.T) {
		// The node's allow_repos is bitbucket-qualified by construction, and a
		// GitHub repository whose owner/name it happens to spell out uses the
		// App key instead: admitting it here would hand it a credential it does
		// not use (resolveBitbucketProxied's own rule).
		in := webappInputs(t)
		node := &projectpolicy.Policy{
			Version:        projectpolicy.Version,
			AllowRepos:     []string{"bitbucket/acme/webapp"},
			BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/webapp"}},
		}
		if _, ok := ProjectSecretCandidateFromNode(in.LC, "bitbucket-token", "acme/webapp", "github", node); ok {
			t.Error("a GitHub repository was admitted by the Bitbucket token's allow-list")
		}
		// The allow_repos above is bitbucket-qualified, so that case is refused
		// by the stamp and not by the provider guard: the guard is what answers
		// for a node whose block spells the repository out under the very
		// provider the caller asked about, which is the node a hand that writes
		// the database can leave behind. Without the guard the admission below
		// succeeds and a GitHub repository is handed the project's Bitbucket
		// token.
		byHand := &projectpolicy.Policy{
			Version:        projectpolicy.Version,
			BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"github/acme/webapp"}},
		}
		if _, ok := ProjectSecretCandidateFromNode(in.LC, "bitbucket-token", "acme/webapp", "github", byHand); ok {
			t.Error("a GitHub repository was admitted by the Bitbucket token's block, on a node whose allow_repos spells it out under the GitHub stamp")
		}
	})
	t.Run("a local entry decides", func(t *testing.T) {
		// An entry is the owner's own decision and the local file is its source
		// of truth (D854): a machine that carries one keeps the answer it
		// carries, including the entry that names another repository.
		in := sandboxInputs(t, m5Additions+"project_secrets:\n  bitbucket-token: { allow_data_to: [acme/other] }\n")
		if _, ok := ProjectSecretCandidateFromNode(in.LC, "bitbucket-token", "acme/sandbox", "bitbucket", bitbucketNode); ok {
			t.Error("the node widened an entry the local config wrote")
		}
	})
	t.Run("a block that admits nobody admits nobody", func(t *testing.T) {
		in := sandboxInputs(t, m5Additions)
		for name, node := range map[string]*projectpolicy.Policy{
			"no bitbucket_token block": {Version: projectpolicy.Version, AllowRepos: []string{"bitbucket/acme/sandbox"}},
			"an empty allow_repos":     {Version: projectpolicy.Version, BitbucketToken: &projectpolicy.BitbucketToken{}},
			"a block for another workspace": {Version: projectpolicy.Version, BitbucketToken: &projectpolicy.BitbucketToken{
				AllowRepos: []string{"bitbucket/other/*"}}},
			"no node read at all": nil,
		} {
			if _, ok := ProjectSecretCandidateFromNode(in.LC, "bitbucket-token", "acme/sandbox", "bitbucket", node); ok {
				t.Errorf("%s admitted the repository", name)
			}
		}
	})
	t.Run("a wildcard admits the repositories it matches", func(t *testing.T) {
		in := sandboxInputs(t, m5Additions)
		node := &projectpolicy.Policy{Version: projectpolicy.Version, BitbucketToken: &projectpolicy.BitbucketToken{
			AllowRepos: []string{"bitbucket/acme/*"}}}
		if _, ok := ProjectSecretCandidateFromNode(in.LC, "bitbucket-token", "acme/sandbox", "bitbucket", node); !ok {
			t.Error("a workspace pattern the owner published did not admit a repository of it")
		}
	})
}

// sharedSandboxPolicy is the published node of a project whose Bitbucket
// default profile serves the sandbox fixture, with both allow_repos lists —
// the project's and the profile's, which projectpolicy.Admits reads together —
// admitting exactly repos, spelled the one way a policy spells a repository
// (projectpolicy.Canonical: "bitbucket/acme/sandbox").
func sharedSandboxPolicy(repos ...string) *projectpolicy.Policy {
	return &projectpolicy.Policy{
		Version:    projectpolicy.Version,
		AllowRepos: repos,
		Profiles: map[string]projectpolicy.Profile{
			projectpolicy.DefaultProfileName: {JobProfile: config.JobProfile{Provider: "bitbucket", AllowRepos: repos}},
		},
	}
}

func bakedOnSharedJob(t *testing.T) {
	t.Helper()
	SharedJobBaked = true
	t.Cleanup(func() { SharedJobBaked = false })
}

// TestABakedRepoIsNotMarkedSharedUntilTheSharedJobCarriesItsTools is why
// switching a baked repository's account works: fugaro run launches its baked
// workflow on the per-repository job, so update must keep applying the
// repository root that creates it.
func TestABakedRepoIsNotMarkedSharedUntilTheSharedJobCarriesItsTools(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	in.Policy = sharedSandboxPolicy("bitbucket/acme/sandbox")
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Shared {
		t.Error("a repository with a baked workflow is marked shared while the shared job cannot carry its tools: update would apply no root and fugaro run would find no per-repository job")
	}
}

// TestSpecMarksASharedRepo is M6's mark: a repository a deployed profile
// admits is shared, which is the fact M7 reads to stop applying its
// repository-level root and the structural precondition D2303 puts in place of
// a retire gate.
func TestSpecMarksASharedRepo(t *testing.T) {
	bakedOnSharedJob(t)
	in := sandboxInputs(t, m5Additions)
	in.Policy = sharedSandboxPolicy("bitbucket/acme/sandbox")
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Shared {
		t.Errorf("a repository the deployed %s profile admits is not marked shared, so `fugaro update` keeps applying its repository root (M6, D2303): policy %+v",
			projectpolicy.DefaultProfileName, in.Policy)
	}
}

// TestTheSharedMarkChangesNoTerraformInput is the half of M6's acceptance that
// lets it merge before M7 and M2: the mark is a fact this process holds and
// never a Terraform input, so a repository root plans what it planned before
// the field existed and no root applies differently on the strength of the
// mark alone.
func TestTheSharedMarkChangesNoTerraformInput(t *testing.T) {
	bakedOnSharedJob(t)
	marked := sandboxInputs(t, m5Additions)
	marked.Policy = sharedSandboxPolicy("bitbucket/acme/sandbox")
	unmarked := sandboxInputs(t, m5Additions)
	unmarked.Policy = sharedSandboxPolicy("bitbucket/acme/other")

	ms, err := Repo(marked)
	if err != nil {
		t.Fatal(err)
	}
	us, err := Repo(unmarked)
	if err != nil {
		t.Fatal(err)
	}
	// Without this the test would pass on two specs that both carry no mark,
	// which is the state the field was added to change.
	if !ms.Shared || us.Shared {
		t.Fatalf("the fixture does not show the mark: marked.Shared = %v, unmarked.Shared = %v", ms.Shared, us.Shared)
	}
	mv, err := RepoVars(ms)
	if err != nil {
		t.Fatal(err)
	}
	uv, err := RepoVars(us)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mv, uv) {
		t.Errorf("the mark moved the repository root's tfvars, so a root applies differently for a shared repository (M6):\n%s\n%s", mv, uv)
	}
	var doc struct {
		Repo map[string]json.RawMessage `json:"repo"`
	}
	if err := json.Unmarshal(mv, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Repo["shared"]; ok {
		t.Errorf("the mark reached the tfvars as repo.shared: it is a fact of this process and not a Terraform input (M6)\n%s", mv)
	}
}

// TestARepoNoProfileAdmitsIsUnmarked is the other half of the mark's rule: the
// answer is a deployed profile's admission and nothing else, so every way a
// profile can fail to admit the repository leaves it on its own job — which is
// what M7's "a repository no profile admits keeps the existing path" reads.
func TestARepoNoProfileAdmitsIsUnmarked(t *testing.T) {
	cases := []struct {
		name   string
		policy *projectpolicy.Policy
	}{
		{"no node was read", nil},
		{"the node defines no profile", &projectpolicy.Policy{
			Version: projectpolicy.Version, AllowRepos: []string{"bitbucket/acme/sandbox"}}},
		{"the project's list does not admit it", sharedSandboxPolicy("bitbucket/acme/other")},
		{"the profile's list does not admit it", func() *projectpolicy.Policy {
			p := sharedSandboxPolicy("bitbucket/acme/sandbox", "bitbucket/acme/other")
			p.Profiles[projectpolicy.DefaultProfileName] = projectpolicy.Profile{
				JobProfile: config.JobProfile{Provider: "bitbucket", AllowRepos: []string{"bitbucket/acme/other"}}}
			return p
		}()},
		{"the profile serves another provider", func() *projectpolicy.Policy {
			p := sharedSandboxPolicy("bitbucket/acme/sandbox")
			p.Profiles[projectpolicy.DefaultProfileName] = projectpolicy.Profile{
				JobProfile: config.JobProfile{Provider: "github", AllowRepos: []string{"bitbucket/acme/sandbox"}}}
			return p
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := sandboxInputs(t, m5Additions)
			in.Policy = tc.policy
			rs, err := Repo(in)
			if err != nil {
				t.Fatal(err)
			}
			if rs.Shared {
				t.Errorf("the repository is marked shared, but no deployed profile admits it (M6): policy %+v", tc.policy)
			}
		})
	}
}
