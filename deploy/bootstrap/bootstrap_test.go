package bootstrap_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// fugaroBin is built once for the package, in TestMain.
var fugaroBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fugaro-bootstrap-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fugaroBin = filepath.Join(dir, "fugaro")
	if out, err := exec.Command("go", "build", "-o", fugaroBin, "github.com/dimipaun/fugaro/cmd/fugaro").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const (
	project  = "proj-1234"
	bucket   = "proj-1234-fugaro-runs"
	buildSA  = "fugaro-build@proj-1234.iam.gserviceaccount.com"
	bucketGS = "gs://" + bucket
	registry = "us-east5-docker.pkg.dev/proj-1234/fugaro"
)

// The names every test expects, computed through the naming contract from
// the sandbox checkout, Bitbucket's acme/sandbox, and never spelled out.
var (
	sandboxSlug = slug("bitbucket", "acme/sandbox")
	jobName     = gcp.JobName(sandboxSlug, "web")
	saID        = gcp.ServiceAccountID(sandboxSlug, "web")
	jobSA       = saID + "@" + project + ".iam.gserviceaccount.com"
	gitID       = gcp.SecretID(sandboxSlug, "bitbucket-token")
	oauthID     = gcp.SecretID(sandboxSlug, "claude-oauth-token")
	probeID     = gcp.SecretID(sandboxSlug, "sandbox-probe")
	repoLbl     = "fugaro_repo=" + sandboxSlug // a slug is [a-z0-9-], so it is its own label
	image       = gcp.ImageName(registry, sandboxSlug, "web")
	bucketCond  = "expression=" + condition(sandboxSlug) + ",title=fugaro-" + saID

	// Repositories whose names collide with the sandbox's under the old,
	// unhashed naming, for the ownership checks.
	sandboxWebSlug = slug("bitbucket", "acme/sandbox-web")
	otherSlug      = slug("bitbucket", "acme/other")
)

func slug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// condition is the bucket IAM condition of a repository slug.
func condition(slug string) string {
	var conds []string
	for _, p := range []string{"runs", "cache", "locks"} {
		conds = append(conds, `resource.name.startsWith("projects/_/buckets/`+bucket+`/objects/`+p+`/`+slug+`/")`)
	}
	return strings.Join(conds, " || ")
}

// saDisplay is the display name the script gives a job's service account.
func saDisplay(slug, workflow string) string { return "Fugaro M4 job " + slug + " " + workflow }

// fakeBin puts gcloud and docker stand-ins on PATH that fail the test if
// the dry run ever calls them.
func fakeBin(t *testing.T) string {
	dir := t.TempDir()
	for _, name := range []string{"gcloud", "docker"} {
		script := "#!/bin/sh\necho \"$0 was called in a dry run: $*\" >&2\nexit 99\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeGcloud is a stateful gcloud stand-in: a resource exists while its
// file under $FAKE_STATE/<kind>/ does, describes of missing ones fail with
// NOT_FOUND, creates of existing ones with ALREADY_EXISTS, and bindings are
// files under $FAKE_STATE/bind/; a service account's file holds its display
// name. A call whose argv starts with $FAKE_FAIL fails with
// PERMISSION_DENIED. Every call's argv is appended to
// $FAKE_LOG, NUL-separated, one call per line. docker only records.
const fakeGcloud = `#!/usr/bin/env bash
{ printf '%s\0' "$@"; printf '\n'; } >> "$FAKE_LOG"
st=$FAKE_STATE
if [ -n "${FAKE_FAIL:-}" ]; then
  case "$*" in "$FAKE_FAIL"*) echo "ERROR: (gcloud) PERMISSION_DENIED: fake failure of $FAKE_FAIL" >&2; exit 1 ;; esac
fi
key() { printf '%s' "$*" | tr '/:@ ' '____'; }
flag() { local f=$1; shift; while [ $# -gt 0 ]; do if [ "$1" = "$f" ]; then printf '%s' "$2"; return; fi; shift; done; }
nf() { echo "ERROR: (gcloud.$1) NOT_FOUND: fake: $2 not found" >&2; exit 1; }
label() { tr ',' '\n' < "$2" | sed -n "s/^$1=//p"; }
fmt=$(flag --format "$@")
case "$1 $2" in
  "services enable"|"auth configure-docker") exit 0 ;;
  "projects describe") echo "${FAKE_PROJECT_NUMBER:-111}"; exit 0 ;;
  "projects "*) kind=project; verb=$2; name=$3 ;;
  "storage rm") kind=buckets; verb=delete; name=$4 ;;
  "secrets "*) kind=secrets; verb=$2; name=$3 ;;
  *) kind=$2; verb=$3; name=$4 ;;
esac
if [ "$kind $verb" = "service-accounts create" ]; then name="$name@$(flag --project "$@").iam.gserviceaccount.com"; fi
mkdir -p "$st/$kind" "$st/bind"
f="$st/$kind/$(key "$name")"
b="$st/bind/$(key "$kind $name $(flag --member "$@") $(flag --role "$@")")"
case "$verb" in
  create) if [ -e "$f" ]; then echo "ERROR: ALREADY_EXISTS: $name" >&2; exit 1; fi; flag --display-name "$@" > "$f" ;;
  deploy) flag --labels "$@" > "$f" ;;
  describe)
    [ -e "$f" ] || nf describe "$name"
    case "$fmt" in
      *projectNumber*) echo "${FAKE_BUCKET_NUMBER:-111}" ;;
      *location*) echo "${FAKE_BUCKET_LOCATION:-US-EAST5}" ;;
      *labels.*) l=${fmt#*labels.}; label "${l%)}" "$f" ;;
      *displayName*) cat "$f" ;;
    esac ;;
  update) [ -e "$f" ] || nf update "$name" ;;
  delete) [ -e "$f" ] || nf delete "$name"; rm -f "$f" ;;
  list)
    filt=$(flag --filter "$@")
    for j in "$st/$kind"/*; do
      [ -e "$j" ] || continue
      case "$filt" in *fugaro_repo=*) [ "$(label fugaro_repo "$j")" = "${filt#*fugaro_repo=}" ] || continue ;; esac
      basename "$j"
    done ;;
  add-iam-policy-binding) : > "$b" ;;
  remove-iam-policy-binding)
    if [ ! -e "$b" ]; then echo "ERROR: Policy binding with the specified principal, role, and condition not found!" >&2; exit 1; fi
    rm -f "$b" ;;
  *) echo "fake gcloud: unhandled $*" >&2; exit 98 ;;
esac
`

type fake struct{ bin, log, state string }

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{bin: t.TempDir(), state: t.TempDir()}
	f.log = filepath.Join(f.bin, "calls.log")
	docker := "#!/bin/sh\n{ printf '%s\\0' docker \"$@\"; printf '\\n'; } >> \"$FAKE_LOG\"\n"
	for name, script := range map[string]string{"gcloud": fakeGcloud, "docker": docker} {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

var stateKey = strings.NewReplacer("/", "_", ":", "_", "@", "_", " ", "_")

// seed makes a resource exist, with content (such as its labels).
func (f *fake) seed(t *testing.T, kind, name, content string) {
	t.Helper()
	dir := filepath.Join(f.state, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stateKey.Replace(name)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fake) has(kind, name string) bool {
	_, err := os.Stat(filepath.Join(f.state, kind, stateKey.Replace(name)))
	return err == nil
}

// calls returns and clears the recorded argv of every call.
func (f *fake) calls(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(f.log)
	var out [][]string
	for _, rec := range strings.Split(string(data), "\x00\n") {
		if rec != "" {
			out = append(out, strings.Split(rec, "\x00"))
		}
	}
	return out
}

var mutatingVerbs = []string{"create", "delete", "rm", "enable", "deploy", "update", "add-iam-policy-binding", "remove-iam-policy-binding", "configure-docker"}

// mutating keeps the gcloud calls that change something, with the
// lifecycle file's temporary path normalized.
func mutating(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if slices.ContainsFunc(c[:min(3, len(c))], func(a string) bool { return slices.Contains(mutatingVerbs, a) }) {
			c = slices.Clone(c)
			for i, a := range c {
				if strings.HasPrefix(a, "--lifecycle-file=") {
					c[i] = "--lifecycle-file=*"
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// deletions keeps the calls that delete or unbind something.
func deletions(calls [][]string) [][]string {
	var out [][]string
	for _, c := range mutating(calls) {
		if slices.ContainsFunc(c[:min(3, len(c))], func(a string) bool {
			return a == "delete" || a == "rm" || a == "remove-iam-policy-binding"
		}) {
			out = append(out, c)
		}
	}
	return out
}

func assertCalls(t *testing.T, what string, got, want [][]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		var g, w strings.Builder
		for _, c := range got {
			fmt.Fprintf(&g, "  %q\n", c)
		}
		for _, c := range want {
			fmt.Fprintf(&w, "  %q\n", c)
		}
		t.Fatalf("%s calls:\n%swant:\n%s", what, g.String(), w.String())
	}
}

// sandboxCheckout is a git checkout of ./sandbox with a Bitbucket https origin.
func sandboxCheckout(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sandbox")
	entries, err := os.ReadDir("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("sandbox", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(data)
	}
	testutil.WriteFiles(t, dir, files)
	testutil.Git(t, dir, "init", "--quiet", "-b", "master")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "sandbox")
	testutil.Git(t, dir, "remote", "add", "origin", "https://bitbucket.org/acme/sandbox.git")
	return dir
}

const localConfig = "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: proj-1234-fugaro-runs\n" +
	"registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nbuild: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n"

// writeLocalConfig writes a minimal local config for project proj-1234.
func writeLocalConfig(t *testing.T) string { return writeConfig(t, localConfig) }

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// scriptEnv is the environment of a full run against the sandbox checkout,
// with bin first on PATH and state for the fake gcloud.
func scriptEnv(t *testing.T, bin string, f *fake) []string {
	env := append(os.Environ(),
		"PATH="+bin+":"+filepath.Dir(fugaroBin)+":"+os.Getenv("PATH"),
		"PROJECT="+project, "REGION=us-east5", "BUCKET="+bucket, "REPO=acme/sandbox",
		"WORKFLOW=web", "CHECKOUT="+sandboxCheckout(t), "FUGARO="+fugaroBin, "FUGARO_CONFIG="+writeLocalConfig(t),
		"HEAVY=/bin/echo", "FUGARO_SRC="+testutil.ModuleRoot())
	if f != nil {
		env = append(env, "FAKE_LOG="+f.log, "FAKE_STATE="+f.state)
	}
	return env
}

func script(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"gcp-m4.sh"}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustScript(t *testing.T, env []string, args ...string) string {
	t.Helper()
	out, err := script(t, env, args...)
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestDryRunNamesMatchTheContract(t *testing.T) {
	testutil.IsolateGit(t)
	env := scriptEnv(t, fakeBin(t), nil)
	for _, step := range []string{"apis", "bucket", "registry", "build-sa", "job-sa", "secrets", "secrets-access", "base", "image", "job", "teardown", "teardown --secrets", "teardown-all --all"} {
		s := mustScript(t, env, strings.Fields(step)...)
		if step != "secrets" && !strings.Contains(s, "⚠ CONFIRM (project proj-1234)") {
			t.Errorf("%s creates or deletes resources without a ⚠ CONFIRM banner naming the project:\n%s", step, s)
		}
		switch step {
		case "job":
			for _, want := range []string{jobName, "--max-retries 0", "--task-timeout 1320s", "FUGARO_BACKEND=cloud-run",
				"--labels fugaro=managed," + repoLbl + ",fugaro_workflow=web",
				"CLAUDE_CODE_OAUTH_TOKEN=" + oauthID + ":latest"} {
				if !strings.Contains(s, want) {
					t.Errorf("job step lacks %q:\n%s", want, s)
				}
			}
		case "job-sa":
			if !strings.Contains(s, saID) {
				t.Errorf("job-sa step lacks the service account ID:\n%s", s)
			}
		case "secrets":
			for _, want := range []string{"claude-oauth-token", "bitbucket-token", "sandbox-probe"} {
				if !strings.Contains(s, want) {
					t.Errorf("secrets step lacks %q:\n%s", want, s)
				}
			}
		case "image":
			if !strings.Contains(s, "+ "+fugaroBin+" image build --repo acme/sandbox --workflow web --json") {
				t.Errorf("image step prints no build command:\n%s", s)
			}
		case "teardown":
			if strings.Contains(s, "gcloud secrets delete") || !strings.Contains(s, "secrets remove-iam-policy-binding") {
				t.Errorf("teardown without --secrets must keep the secrets:\n%s", s)
			}
		case "teardown --secrets":
			if !strings.Contains(s, "secrets delete "+oauthID) {
				t.Errorf("teardown --secrets does not delete the secrets:\n%s", s)
			}
		case "base":
			if !strings.Contains(s, "~/.docker/config.json") {
				t.Errorf("base banner does not mention ~/.docker/config.json:\n%s", s)
			}
		}
	}
}

// installed runs every create step against f and returns their calls.
func installed(t *testing.T, f *fake, env []string) [][]string {
	t.Helper()
	seedSecrets(t, f) // fugaro secrets set, which secrets-access requires first
	for _, step := range []string{"apis", "bucket", "registry", "build-sa", "job-sa", "secrets-access", "base", "job"} {
		mustScript(t, env, "--apply", "--yes", step)
	}
	return f.calls(t)
}

func seedSecrets(t *testing.T, f *fake) {
	for _, id := range []string{gitID, oauthID, probeID} {
		f.seed(t, "secrets", id, "fugaro=managed,"+repoLbl)
	}
}

func TestApplyExactCalls(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	calls := installed(t, f, env)
	for _, c := range calls {
		if c[0] == "docker" {
			t.Errorf("docker called directly rather than through HEAVY: %q", c)
			continue
		}
		if i := slices.Index(c, "--project"); i < 0 || i+1 >= len(c) || c[i+1] != project {
			t.Errorf("gcloud call without --project %s: %q", project, c)
		}
	}
	P, acc := "--project", "roles/secretmanager.secretAccessor"
	grant := func(id, sa string) []string {
		return []string{"secrets", "add-iam-policy-binding", id, P, project, "--member", "serviceAccount:" + sa, "--role", acc}
	}
	assertCalls(t, "install", mutating(calls), [][]string{
		{"services", "enable", "run.googleapis.com", "storage.googleapis.com", "secretmanager.googleapis.com", "artifactregistry.googleapis.com", "cloudbuild.googleapis.com", "logging.googleapis.com", "iam.googleapis.com", P, project},
		{"storage", "buckets", "create", bucketGS, P, project, "--location", "us-east5", "--uniform-bucket-level-access", "--public-access-prevention"},
		{"storage", "buckets", "update", bucketGS, P, project, "--lifecycle-file=*"},
		{"artifacts", "repositories", "create", "fugaro", "--repository-format", "docker", "--location", "us-east5", P, project},
		{"iam", "service-accounts", "create", "fugaro-build", P, project, "--display-name", "Fugaro image builds (M4 bootstrap)"},
		{"artifacts", "repositories", "add-iam-policy-binding", "fugaro", "--location", "us-east5", P, project, "--member", "serviceAccount:" + buildSA, "--role", "roles/artifactregistry.writer"},
		{"projects", "add-iam-policy-binding", project, P, project, "--member", "serviceAccount:" + buildSA, "--role", "roles/logging.logWriter", "--condition", "None"},
		{"iam", "service-accounts", "create", saID, P, project, "--display-name", saDisplay(sandboxSlug, "web")},
		{"storage", "buckets", "add-iam-policy-binding", bucketGS, "--member", "serviceAccount:" + jobSA, "--role", "roles/storage.objectUser", "--condition", bucketCond, P, project},
		grant(gitID, jobSA), grant(oauthID, jobSA), grant(probeID, jobSA),
		grant(gitID, buildSA), grant(probeID, buildSA),
		{"auth", "configure-docker", "us-east5-docker.pkg.dev", "--quiet", P, project},
		{"run", "jobs", "deploy", jobName, P, project, "--region", "us-east5",
			"--image", image + ":latest", "--service-account", jobSA,
			"--cpu", "1", "--memory", "2Gi", "--task-timeout", "1320s", "--max-retries", "0", "--tasks", "1",
			"--labels", "fugaro=managed," + repoLbl + ",fugaro_workflow=web",
			"--set-env-vars", "^;^FUGARO_BACKEND=cloud-run;FUGARO_BUCKET=gs://proj-1234-fugaro-runs;FUGARO_PROJECT=proj-1234;FUGARO_REGION=us-east5;" +
				"FUGARO_SECRET_ENVS=CLAUDE_CODE_OAUTH_TOKEN,FUGARO_BITBUCKET_TOKEN,SANDBOX_PROBE",
			"--set-secrets", "CLAUDE_CODE_OAUTH_TOKEN=" + oauthID + ":latest,FUGARO_BITBUCKET_TOKEN=" + gitID + ":latest,SANDBOX_PROBE=" + probeID + ":latest"},
	})

	// A rerun creates nothing: every create finds its resource and skips.
	for _, c := range mutating(installed(t, f, env)) {
		if slices.Contains(c[:3], "create") {
			t.Errorf("rerun created again: %q", c)
		}
	}

	// teardown removes exactly the job, its bindings and its account, and keeps the secrets.
	seedSecrets(t, f)
	mustScript(t, env, "--apply", "--yes", "teardown")
	unbind := func(id string) []string {
		return []string{"secrets", "remove-iam-policy-binding", id, P, project, "--member", "serviceAccount:" + jobSA, "--role", acc}
	}
	bucketUnbind := []string{"storage", "buckets", "remove-iam-policy-binding", bucketGS, "--member", "serviceAccount:" + jobSA, "--role", "roles/storage.objectUser", "--condition", bucketCond, P, project}
	assertCalls(t, "teardown", deletions(f.calls(t)), [][]string{
		{"run", "jobs", "delete", jobName, P, project, "--region", "us-east5", "--quiet"},
		bucketUnbind,
		unbind(gitID), unbind(oauthID), unbind(probeID),
		{"iam", "service-accounts", "delete", jobSA, P, project, "--quiet"},
	})
	if !f.has("secrets", oauthID) || f.has("jobs", jobName) || f.has("service-accounts", jobSA) {
		t.Fatal("teardown deleted the wrong things")
	}
	// A rerun only retries the removals, which find nothing to remove.
	mustScript(t, env, "--apply", "--yes", "teardown")
	assertCalls(t, "teardown rerun", deletions(f.calls(t)), [][]string{bucketUnbind, unbind(gitID), unbind(oauthID), unbind(probeID)})

	// teardown-all removes exactly the shared resources.
	mustScript(t, env, "--apply", "--yes", "teardown-all", "--all")
	logUnbind := []string{"projects", "remove-iam-policy-binding", project, P, project, "--member", "serviceAccount:" + buildSA, "--role", "roles/logging.logWriter", "--condition", "None"}
	assertCalls(t, "teardown-all", deletions(f.calls(t)), [][]string{
		{"storage", "rm", "--recursive", bucketGS, P, project},
		{"artifacts", "repositories", "delete", "fugaro", "--location", "us-east5", P, project, "--quiet"},
		logUnbind,
		{"iam", "service-accounts", "delete", buildSA, P, project, "--quiet"},
	})
	mustScript(t, env, "--apply", "--yes", "teardown-all", "--all")
	assertCalls(t, "teardown-all rerun", deletions(f.calls(t)), [][]string{logUnbind})
}

func TestTeardownSecrets(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	installed(t, f, env)
	seedSecrets(t, f)

	// Another job of the repository still needs the secrets.
	f.seed(t, "jobs", gcp.JobName(sandboxSlug, "api"), "fugaro=managed,"+repoLbl+",fugaro_workflow=api")
	if out, err := script(t, env, "--apply", "--yes", "teardown", "--secrets"); err == nil || !strings.Contains(out, "refusing --secrets") {
		t.Fatalf("teardown --secrets with a sibling job: %v\n%s", err, out)
	}
	if d := deletions(f.calls(t)); len(d) > 0 {
		t.Fatalf("refused teardown deleted %q", d)
	}
	// Another repository's job doesn't count.
	f.seed(t, "jobs", gcp.JobName(sandboxSlug, "api"), "fugaro=managed,fugaro_repo="+otherSlug+",fugaro_workflow=api")
	mustScript(t, env, "--apply", "--yes", "teardown", "--secrets")
	del := func(id string) []string { return []string{"secrets", "delete", id, "--project", project, "--quiet"} }
	assertCalls(t, "teardown --secrets", deletions(f.calls(t)), [][]string{
		{"run", "jobs", "delete", jobName, "--project", project, "--region", "us-east5", "--quiet"},
		{"storage", "buckets", "remove-iam-policy-binding", bucketGS, "--member", "serviceAccount:" + jobSA, "--role", "roles/storage.objectUser", "--condition", bucketCond, "--project", project},
		del(gitID), del(oauthID), del(probeID),
		{"iam", "service-accounts", "delete", jobSA, "--project", project, "--quiet"},
	})
}

// A name collision must never let teardown delete another repository's
// resources: the labels decide.
func TestTeardownChecksLabels(t *testing.T) {
	testutil.IsolateGit(t)
	for name, prep := range map[string]func(f *fake){
		"job of another repository": func(f *fake) {
			f.seed(t, "jobs", jobName, "fugaro=managed,fugaro_repo="+sandboxWebSlug+",fugaro_workflow=x")
		},
		"unlabelled job": func(f *fake) { f.seed(t, "jobs", jobName, "") },
		"secret of another repository": func(f *fake) {
			f.seed(t, "secrets", oauthID, "fugaro=managed,fugaro_repo="+slug("bitbucket", "acme/sandbox-claude"))
		},
		"service account of another repository, job already gone": func(f *fake) {
			_ = os.Remove(filepath.Join(f.state, "jobs", jobName))
			f.seed(t, "service-accounts", jobSA, saDisplay(sandboxWebSlug, "x"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			env := scriptEnv(t, f.bin, f)
			installed(t, f, env)
			seedSecrets(t, f)
			prep(f)
			if out, err := script(t, env, "--apply", "--yes", "teardown", "--secrets"); err == nil || !strings.Contains(out, "refusing") {
				t.Fatalf("teardown: %v\n%s", err, out)
			}
			if d := deletions(f.calls(t)); len(d) > 0 {
				t.Fatalf("refused teardown deleted %q", d)
			}
		})
	}
}

// secrets-access grants nothing unless every secret exists and carries this
// repository's fugaro_repo label.
func TestSecretsAccessChecksLabels(t *testing.T) {
	testutil.IsolateGit(t)
	for name, prep := range map[string]func(f *fake){
		"secret of another repository": func(f *fake) {
			f.seed(t, "secrets", probeID, "fugaro=managed,fugaro_repo="+sandboxWebSlug)
		},
		"unlabelled secret": func(f *fake) { f.seed(t, "secrets", oauthID, "") },
		"missing secret":    func(f *fake) { _ = os.Remove(filepath.Join(f.state, "secrets", stateKey.Replace(gitID))) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			env := scriptEnv(t, f.bin, f)
			for _, step := range []string{"apis", "bucket", "registry", "build-sa", "job-sa"} {
				mustScript(t, env, "--apply", "--yes", step)
			}
			seedSecrets(t, f)
			prep(f)
			f.calls(t)
			out, err := script(t, env, "--apply", "--yes", "secrets-access")
			if err == nil || !strings.Contains(out, "refusing") && !strings.Contains(out, "does not exist") {
				t.Fatalf("secrets-access: %v\n%s", err, out)
			}
			if m := mutating(f.calls(t)); len(m) > 0 {
				t.Fatalf("refused secrets-access changed %q", m)
			}
		})
	}
}

// job-sa and job never take over a colliding account or job.
func TestCreatesCheckOwnership(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	for _, step := range []string{"apis", "bucket", "registry", "build-sa"} {
		mustScript(t, env, "--apply", "--yes", step)
	}
	f.calls(t)
	for _, other := range []string{
		saDisplay(sandboxWebSlug, "x"),
		saDisplay(slug("github", "acme/sandbox"), "web"), // the same owner/name and workflow on another provider
		"Fugaro job acme/sandbox web (M4 bootstrap)",     // the pre-slug display name
	} {
		f.seed(t, "service-accounts", jobSA, other)
		if out, err := script(t, env, "--apply", "--yes", "job-sa"); err == nil || !strings.Contains(out, "refusing") {
			t.Fatalf("job-sa over an account named %q: %v\n%s", other, err, out)
		}
		if m := mutating(f.calls(t)); len(m) > 0 {
			t.Fatalf("job-sa changed %q", m)
		}
	}
	f.seed(t, "service-accounts", jobSA, saDisplay(sandboxSlug, "web"))
	mustScript(t, env, "--apply", "--yes", "job-sa") // its own account is reused
	f.seed(t, "jobs", jobName, "fugaro=managed,fugaro_repo="+sandboxWebSlug+",fugaro_workflow=x")
	f.calls(t)
	if out, err := script(t, env, "--apply", "--yes", "job"); err == nil || !strings.Contains(out, "refusing") {
		t.Fatalf("job over another repository's job: %v\n%s", err, out)
	}
	if m := mutating(f.calls(t)); len(m) > 0 {
		t.Fatalf("job changed %q", m)
	}
	f.seed(t, "jobs", jobName, "fugaro=managed,"+repoLbl+",fugaro_workflow=web")
	mustScript(t, env, "--apply", "--yes", "job") // its own job is updated
}

// A describe or list that fails for any reason but not-found stops the
// step: nothing is created, deleted or taken for absent.
func TestFailuresOtherThanNotFoundStop(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	installed(t, f, env)
	seedSecrets(t, f)
	for _, c := range []struct{ fail, step string }{
		{"run jobs list", "teardown --secrets"}, // the sibling check must not read a failure as "no siblings"
		{"iam service-accounts describe", "job-sa"},
		{"run jobs describe", "job"},
		{"run jobs describe", "teardown"},
		{"secrets describe", "teardown --secrets"},
		{"secrets describe", "secrets-access"},
		{"storage buckets describe", "bucket"},
		{"storage buckets describe", "teardown-all --all"},
	} {
		if c.step == "teardown-all --all" {
			mustScript(t, env, "--apply", "--yes", "teardown") // no job may remain
			f.calls(t)
		}
		out, err := script(t, append(slices.Clone(env), "FAKE_FAIL="+c.fail), append([]string{"--apply", "--yes"}, strings.Fields(c.step)...)...)
		if err == nil || !strings.Contains(out, "PERMISSION_DENIED") {
			t.Fatalf("%s with %s failing: %v\n%s", c.step, c.fail, err, out)
		}
		if m := mutating(f.calls(t)); len(m) > 0 {
			t.Fatalf("%s with %s failing changed %q", c.step, c.fail, m)
		}
	}
}

// Bucket names are global: a bucket of another project is never written,
// unbound or deleted.
func TestBucketOfAnotherProjectIsRefused(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	installed(t, f, env)
	seedSecrets(t, f)
	for name, extra := range map[string]string{"project number": "FAKE_BUCKET_NUMBER=999", "location": "FAKE_BUCKET_LOCATION=US"} {
		for _, step := range []string{"bucket", "job-sa", "teardown", "teardown-all"} {
			if name == "location" && step != "bucket" {
				continue
			}
			args := []string{"--apply", "--yes", step}
			if step == "teardown-all" {
				mustScript(t, env, "--apply", "--yes", "teardown") // no job may remain
				f.calls(t)
				args = append(args, "--all")
			}
			out, err := script(t, append(slices.Clone(env), extra), args...)
			if err == nil || !strings.Contains(out, "refusing") {
				t.Fatalf("%s mismatch, %s: %v\n%s", name, step, err, out)
			}
			if m := mutating(f.calls(t)); len(m) > 0 {
				t.Fatalf("%s mismatch, %s changed %q", name, step, m)
			}
		}
	}
}

func TestApplyNeedsConfirmation(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	for _, args := range [][]string{{"--apply", "apis"}, {"--apply", "teardown-all", "--all"}, {"--apply", "teardown"}} {
		cmd := exec.Command("bash", append([]string{"gcp-m4.sh"}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader("proj-1234\n") // not a terminal: only --yes confirms
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--yes") {
			t.Fatalf("%q without --yes: %v\n%s", args, err, out)
		}
		if c := f.calls(t); len(c) > 0 {
			t.Fatalf("%q called gcloud without a confirmation: %q", args, c)
		}
	}
}

func TestTeardownAllRefusesWhileJobsExist(t *testing.T) {
	testutil.IsolateGit(t)
	f := newFake(t)
	env := scriptEnv(t, f.bin, f)
	installed(t, f, env)
	out, err := script(t, env, "--apply", "--yes", "teardown-all", "--all")
	if err == nil || !strings.Contains(out, "still exist") {
		t.Fatalf("teardown-all with a job left: %v\n%s", err, out)
	}
	if d := deletions(f.calls(t)); len(d) > 0 {
		t.Fatalf("teardown-all deleted %q while a job exists", d)
	}
}

func TestConfigStep(t *testing.T) {
	testutil.IsolateGit(t)
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[user]\n\temail = dev@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fugaro", "config.yaml")
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitcfg, "FUGARO_CONFIG="+path, "PATH="+fakeBin(t)+":"+os.Getenv("PATH"),
		"PROJECT=proj-1234", "REGION=us-east5", "BUCKET=proj-1234-fugaro-runs", "REPOS=acme/sandbox:master:web acme/app:main:web:github")
	with := func(extra ...string) []string { return append(slices.Clone(env), extra...) }
	if out, err := script(t, env, "config"); err != nil || !strings.Contains(out, "project: proj-1234") || !strings.Contains(out, "⚠ CONFIRM (project proj-1234)") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the dry run wrote the config")
	}
	cmd := exec.Command("bash", "gcp-m4.sh", "--apply", "config")
	cmd.Env, cmd.Stdin = env, strings.NewReader("proj-1234\n")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--yes") {
		t.Fatalf("config --apply without --yes: %v\n%s", err, out)
	}
	mustScript(t, env, "--apply", "--yes", "config")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"user: dev@example.invalid", "service_account: fugaro-build@proj-1234.iam.gserviceaccount.com",
		"acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }",
		"acme/app: { provider: github, base_branch: main, workflows: [web] }"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config lacks %q:\n%s", want, data)
		}
	}
	// fugaro reads the providers back: they decide the repositories' slugs.
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("fugaro cannot load the written config: %v\n%s", err, data)
	}
	if lc.Repos["acme/sandbox"].Provider != "bitbucket" || lc.Repos["acme/app"].Provider != "github" {
		t.Fatalf("repos = %+v", lc.Repos)
	}
	for _, bad := range []string{"acme/x:main:web:gitlab", "acme/x:main:web:bitbucket:extra", "acme/x:main"} {
		if out, err := script(t, with("REPOS="+bad, "FORCE=1"), "config"); err == nil || !strings.Contains(out, "REPOS entry") {
			t.Errorf("REPOS=%s: %v\n%s", bad, err, out)
		}
	}
	// What config writes, the guard reads back.
	if out, err := script(t, env, "apis"); err != nil {
		t.Fatalf("apis after config: %v\n%s", err, out)
	}
	if out, err := script(t, with("BASE_IMAGE=r/b:dev"), "--apply", "--yes", "config"); err == nil || !strings.Contains(out, "FORCE=1") || !strings.Contains(out, "+base_image: r/b:dev") {
		t.Fatalf("overwrite without FORCE: %v\n%s", err, out)
	}
	mustScript(t, with("BASE_IMAGE=r/b:dev", "FORCE=1"), "--apply", "--yes", "config")
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "base_image: r/b:dev") {
		t.Fatalf("FORCE=1 did not replace the config:\n%s", data)
	}
}

func TestScriptGuards(t *testing.T) {
	testutil.IsolateGit(t)
	base := append(os.Environ(), "PATH="+fakeBin(t)+":"+os.Getenv("PATH"), "REGION=us-east5", "BUCKET="+bucket, "PROJECT="+project)
	cfg := writeLocalConfig(t)
	missing := filepath.Join(t.TempDir(), "config.yaml")
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"project mismatch":              {[]string{"FUGARO_CONFIG=" + cfg, "PROJECT=someone-elses-project"}, []string{"apis"}},
		"no config":                     {[]string{"FUGARO_CONFIG=" + missing}, []string{"apis"}},
		"no config, teardown-all":       {[]string{"FUGARO_CONFIG=" + missing}, []string{"teardown-all", "--all"}},
		"FUGARO_CONFIG=/nonexistent":    {[]string{"FUGARO_CONFIG=/nonexistent"}, []string{"teardown-all", "--all"}},
		"config without project":        {[]string{"FUGARO_CONFIG=" + writeConfig(t, strings.Replace(localConfig, "project: proj-1234\n", "", 1))}, []string{"apis"}},
		"project malformed":             {[]string{"FUGARO_CONFIG=" + cfg, "PROJECT=Proj_1234;rm"}, []string{"apis"}},
		"project malformed, config":     {[]string{"FUGARO_CONFIG=" + missing, "PROJECT=Proj_1234"}, []string{"config"}},
		"bucket mismatch":               {[]string{"FUGARO_CONFIG=" + cfg, "BUCKET=prod-data"}, []string{"teardown-all", "--all"}},
		"region mismatch":               {[]string{"FUGARO_CONFIG=" + cfg, "REGION=europe-west1"}, []string{"registry"}},
		"other build service account":   {[]string{"FUGARO_CONFIG=" + writeConfig(t, strings.Replace(localConfig, "fugaro-build@", "ci@", 1))}, []string{"build-sa"}},
		"config with no bucket, BUCKET": {[]string{"FUGARO_CONFIG=" + writeConfig(t, strings.Replace(localConfig, "runs_bucket: proj-1234-fugaro-runs\n", "", 1))}, []string{"bucket"}},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := script(t, append(slices.Clone(base), c.env...), c.args...); err == nil || !strings.Contains(out, "refusing") {
				t.Fatalf("%v\n%s", err, out)
			}
		})
	}
	good := append(slices.Clone(base), "FUGARO_CONFIG="+cfg)
	if out, err := script(t, good, "teardown-all"); err == nil || !strings.Contains(out, "--all") {
		t.Fatalf("teardown-all without --all: %v\n%s", err, out)
	}
	repo := append(slices.Clone(good), "REPO=acme/sandbox", "WORKFLOW=web", "CHECKOUT="+sandboxCheckout(t), "FUGARO="+fugaroBin)
	out, err := script(t, repo, "teardown")
	if err != nil || strings.Contains(out, "storage rm") || strings.Contains(out, "repositories delete") || strings.Contains(out, "gcloud secrets delete") {
		t.Fatalf("per-repo teardown touches shared resources: %v\n%s", err, out)
	}
	// A job-spec failure must stop the step, never run gcloud with empty names.
	if out, err := script(t, append(repo, "REPO=acme/other"), "job"); err == nil || strings.Contains(out, "+ gcloud") {
		t.Fatalf("job step with a failing job-spec: %v\n%s", err, out)
	}
}

func TestScriptRefusesUnknownStep(t *testing.T) {
	for _, args := range [][]string{{"everything"}, {}, {"apis", "--force"}, {"apis", "job"}, {"apis job"}, {"job teardown"}, {"Apis"}} {
		if out, err := script(t, os.Environ(), args...); err == nil || !strings.Contains(out, "usage") {
			t.Fatalf("%q: %v\n%s", args, err, out)
		}
	}
}

func TestScriptShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not installed")
	}
	if out, err := exec.Command("shellcheck", "gcp-m4.sh").CombinedOutput(); err != nil {
		t.Fatalf("shellcheck:\n%s", out)
	}
}
