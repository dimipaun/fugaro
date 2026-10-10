package images_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type cloudBuild struct {
	Substitutions    map[string]string `yaml:"substitutions"`
	AvailableSecrets struct {
		SecretManager []struct {
			VersionName string `yaml:"versionName"`
			Env         string `yaml:"env"`
		} `yaml:"secretManager"`
	} `yaml:"availableSecrets"`
	Steps []struct {
		ID         string   `yaml:"id"`
		Name       string   `yaml:"name"`
		Entrypoint string   `yaml:"entrypoint"`
		WaitFor    []string `yaml:"waitFor"`
		Env        []string `yaml:"env"`
		Args       []string `yaml:"args"`
		SecretEnv  []string `yaml:"secretEnv"`
		Volumes    []struct {
			Name string `yaml:"name"`
			Path string `yaml:"path"`
		} `yaml:"volumes"`
	} `yaml:"steps"`
	Images []string `yaml:"images"`
}

func loadCloudBuild(t *testing.T) ([]byte, cloudBuild) {
	t.Helper()
	data, err := os.ReadFile("derived/cloudbuild.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cb cloudBuild
	if err := yaml.Unmarshal(data, &cb); err != nil {
		t.Fatal(err)
	}
	return data, cb
}

// step is the index of the step with id, or fails the test.
func (cb cloudBuild) step(t *testing.T, id string) int {
	t.Helper()
	for i, s := range cb.Steps {
		if s.ID == id {
			return i
		}
	}
	t.Fatalf("cloudbuild.yaml has no step %s", id)
	return -1
}

func TestCloudBuildConfig(t *testing.T) {
	data, cb := loadCloudBuild(t)
	var ids []string
	for _, s := range cb.Steps {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"prep", "credential", "source", "render", "build", "smoke", "gate", "promote", "record", "untag"}) {
		t.Fatalf("steps = %v", ids)
	}
	for _, m := range regexp.MustCompile(`\$\{(_[A-Z_]+)\}`).FindAllStringSubmatch(string(data), -1) {
		if _, ok := cb.Substitutions[m[1]]; !ok {
			t.Errorf("%s is used but not declared in substitutions", m[1])
		}
	}
	script := func(id string) string { return strings.Join(cb.Steps[cb.step(t, id)].Args, " ") }
	if !strings.Contains(script("render"), "fugaro image render") || cb.Steps[cb.step(t, "render")].Name != "${_FUGARO_BASE}" {
		t.Error("the render step must use the base image's own fugaro image render")
	}
	cred := cb.Steps[cb.step(t, "credential")]
	if cred.Name != "${_FUGARO_BASE}" || !strings.Contains(script("credential"), "fugaro image git-credential") ||
		!strings.Contains(script("credential"), "--out /creds/git-credentials") {
		t.Errorf("the credential step must run the base image's fugaro image git-credential into the volume: %s %s", cred.Name, script("credential"))
	}
	if !strings.Contains(script("prep"), "install -d -m 0700 -o 1000 -g 1000 /creds") {
		t.Errorf("the prep step must make /creds private to the base image's user: %s", script("prep"))
	}
	if !strings.Contains(script("build"), `--secret "id=git-credentials,src=/creds/git-credentials"`) || !strings.Contains(images.DerivedTemplate, "id=git-credentials") {
		t.Error("the build step and the template disagree on the git credential secret")
	}
	if !strings.Contains(script("source"), "credential.helper='store --file=/creds/git-credentials'") {
		t.Error("the source step must clone with the credential file in the volume")
	}
	if !strings.Contains(script("build"), "RepoDigests") {
		t.Error("the build step does not pin the base image by digest")
	}
	for _, k := range []string{"_REPO_URL", "_BASE_BRANCH", "_WORKFLOW", "_FUGARO_BASE", "_IMAGE", "_GIT_USER", "_GITHUB_APP_ID", "_SECRET_ENVS", "_BUCKET", "_SLUG"} {
		if _, ok := cb.Substitutions[k]; !ok {
			t.Errorf("substitution %s is not declared", k)
		}
	}
	build := script("build")
	if strings.Contains(build, "docker pull") || !strings.Contains(build, "docker image inspect") {
		t.Error("the build step must reuse the render step's base image, not pull it again")
	}
	if !strings.Contains(build, "SECRET_ENVS") || !strings.Contains(build, "--secret") {
		t.Error("the build step does not pass workflow secrets")
	}
	if !strings.Contains(build, "mktemp -d") || !strings.Contains(build, "rm -f /creds/git-credentials") {
		t.Error("the build step must keep workflow secrets in a mktemp directory and remove the git credential on exit")
	}
	if src := script("source"); !strings.Contains(src, "https://*)") {
		t.Error("the source step must refuse a non-https REPO_URL")
	}
	if len(cb.AvailableSecrets.SecretManager) != 1 || cb.AvailableSecrets.SecretManager[0].Env != "GIT_TOKEN" {
		t.Errorf("availableSecrets = %+v", cb.AvailableSecrets)
	}
	if !bytes.Equal(images.CloudBuild, data) {
		t.Error("images.CloudBuild is not derived/cloudbuild.yaml")
	}
	if !strings.Contains(script("render"), "--cloud-outputs /workspace/out") {
		t.Error("the render step does not write the smoke spec and the record's source side")
	}
}

// TestPromoteAfterSmoke: latest moves only after the candidate's smoke
// passed and the gate found no newer record, and the steps run strictly
// in order (no step starts early with waitFor: ["-"]).
func TestPromoteAfterSmoke(t *testing.T) {
	_, cb := loadCloudBuild(t)
	var ids []string
	for _, s := range cb.Steps {
		ids = append(ids, s.ID)
		if slices.Contains(s.WaitFor, "-") {
			t.Errorf("step %s starts at once (waitFor: [\"-\"])", s.ID)
		}
		if len(s.WaitFor) > 0 {
			t.Errorf("step %s has waitFor %v; the steps run in file order", s.ID, s.WaitFor)
		}
	}
	if want := []string{"prep", "credential", "source", "render", "build", "smoke", "gate", "promote", "record", "untag"}; !slices.Equal(ids, want) {
		t.Fatalf("steps = %v, want %v", ids, want)
	}
	// Only promote tags latest, and nothing before it names latest.
	for i, s := range cb.Steps {
		sc := strings.Join(s.Args, " ")
		if strings.Contains(sc, ":latest") && s.ID != "promote" {
			t.Errorf("step %s (%d) names :latest", s.ID, i)
		}
	}
	for _, id := range []string{"promote", "record", "untag"} {
		if sc := strings.Join(cb.Steps[cb.step(t, id)].Args, " "); !strings.Contains(sc, "if [ -e /workspace/out/superseded ]") {
			t.Errorf("step %s does not stand down when the gate found a newer record", id)
		}
	}
	gate := cb.Steps[cb.step(t, "gate")]
	if gate.Name != "${_FUGARO_BASE}" || !strings.Contains(strings.Join(gate.Args, " "), "fugaro image gate --record /workspace/out/record.json") {
		t.Errorf("gate = %s %v", gate.Name, gate.Args)
	}
	rec := cb.Steps[cb.step(t, "record")]
	if rec.Name != "${_FUGARO_BASE}" || !strings.Contains(strings.Join(rec.Args, " "), "fugaro image record --in /workspace/out/record.json --digest-from /workspace/out/image-digest") {
		t.Errorf("record = %s %v", rec.Name, rec.Args)
	}
}

// TestBuildPushesOnlyCandidate: the build step tags and pushes only
// candidate-$BUILD_ID, with the OCI labels, and Cloud Build pushes
// nothing itself (no images:), so latest never moves before the smoke.
func TestBuildPushesOnlyCandidate(t *testing.T) {
	data, cb := loadCloudBuild(t)
	if len(cb.Images) != 0 || regexp.MustCompile(`(?m)^images:`).Match(data) {
		t.Errorf("images = %v; the build pushes the candidate itself", cb.Images)
	}
	build := strings.Join(cb.Steps[cb.step(t, "build")].Args, " ")
	tags := regexp.MustCompile(`--tag\s+(\S+)`).FindAllStringSubmatch(build, -1)
	if len(tags) != 1 || tags[0][1] != `"$$candidate"` || !strings.Contains(build, `candidate="$$IMAGE:candidate-$$BUILD_ID"`) {
		t.Errorf("the build step's tags = %v", tags)
	}
	for _, want := range []string{
		`docker push "$$candidate"`,
		`--label "org.opencontainers.image.revision=$$commit"`,
		`--label "org.opencontainers.image.created=$$created"`,
		`--label "dev.fugaro.base.digest=$${base#*@}"`,
		`--build-arg "FUGARO_BUILT_AT=$$created"`,
		`--build-arg "FUGARO_COMMIT=$$commit"`,
		`docker image inspect --format '{{index .RepoDigests 0}}' "$$candidate"`,
		`> /workspace/out/image-digest`,
		`> "$$BUILDER_OUTPUT/output"`,
	} {
		if !strings.Contains(build, want) {
			t.Errorf("the build step lacks %s", want)
		}
	}
	for _, arg := range []string{`ARG FUGARO_BUILT_AT=""`, `ARG FUGARO_COMMIT=""`} {
		if !strings.Contains(images.DerivedTemplate, arg) {
			t.Errorf("the template does not declare %s", arg)
		}
	}
}

// TestSmokeIsNotAStep: the candidate never runs as a Cloud Build step,
// which would put repository code on the cloudbuild network next to the
// metadata server. It runs only through docker run in the smoke step,
// always with --network none, and no step mounts a volume into it.
func TestSmokeIsNotAStep(t *testing.T) {
	_, cb := loadCloudBuild(t)
	for _, s := range cb.Steps {
		if strings.Contains(s.Name, "$IMAGE") || strings.Contains(s.Name, "_IMAGE") || strings.Contains(s.Name, "candidate") {
			t.Errorf("step %s runs the image itself: %s", s.ID, s.Name)
		}
		for _, line := range strings.Split(strings.Join(s.Args, "\n"), "\n") {
			if !strings.Contains(line, "docker run") {
				continue
			}
			if s.ID != "smoke" {
				t.Errorf("step %s runs a container: %s", s.ID, line)
			}
			if !strings.Contains(line, "--network none") {
				t.Errorf("a docker run without --network none: %s", line)
			}
			for _, bad := range []string{" -v ", "--volume", "--mount", "--env", " -e ", "--privileged", "--network host", "cloudbuild"} {
				if strings.Contains(line, bad) {
					t.Errorf("the smoke's docker run has %s: %s", bad, line)
				}
			}
		}
	}
	smoke := cb.Steps[cb.step(t, "smoke")]
	sc := strings.Join(smoke.Args, " ")
	if len(smoke.Volumes) != 0 || len(smoke.SecretEnv) != 0 || smoke.Name != "gcr.io/cloud-builders/docker" {
		t.Errorf("smoke = %+v", smoke)
	}
	if strings.Count(sc, "docker run") != 2 || !strings.Contains(sc, "--user 0 --network none") ||
		!strings.Contains(sc, "fugaro image selftest < /workspace/out/selftest.json") || !strings.Contains(sc, `candidate="$$IMAGE@$$(cat /workspace/out/image-digest)"`) {
		t.Errorf("the smoke must run the selftest and the root scan in the pushed digest: %s", sc)
	}
}

// TestPromoteByDigest: promote retags on the registry by the digest the
// smoke ran, never by the candidate tag.
func TestPromoteByDigest(t *testing.T) {
	_, cb := loadCloudBuild(t)
	p := cb.Steps[cb.step(t, "promote")]
	sc := strings.Join(p.Args, " ")
	if p.Name != "gcr.io/google.com/cloudsdktool/cloud-sdk:slim" || !strings.Contains(sc, `digest=$$(cat /workspace/out/image-digest)`) ||
		!strings.Contains(sc, `gcloud artifacts docker tags add "$$IMAGE@$$digest" "$$IMAGE:latest"`) || !strings.Contains(sc, "sha256:*") {
		t.Errorf("promote = %s: %s", p.Name, sc)
	}
	if strings.Contains(sc, "candidate") || strings.Contains(sc, "tags delete") || strings.Contains(sc, "docker push") {
		t.Errorf("promote must retag by digest only, and never untag: %s", sc)
	}
}

// TestUntagScriptAlwaysExitsZero: the candidate tag's removal ends in
// || echo, and a refused delete (say, a registry whose grants are not yet
// applied) only prints the warning.
func TestUntagScriptAlwaysExitsZero(t *testing.T) {
	_, cb := loadCloudBuild(t)
	u := cb.Steps[cb.step(t, "untag")]
	sc := strings.TrimSpace(u.Args[1])
	if !regexp.MustCompile(`\|\| echo "warning: [^"]*"$`).MatchString(sc) || strings.Contains(sc, "set -e") {
		t.Fatalf("untag = %s", sc)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	testutil.WriteFiles(t, bin, map[string]string{"gcloud": "#!/bin/sh\necho 'ERROR: (gcloud.artifacts.docker.tags.delete) PERMISSION_DENIED: 403' >&2\nexit 1\n"})
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(ws, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "-c", strings.ReplaceAll(strings.ReplaceAll(u.Args[1], "$$", "$"), "/workspace", ws))
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "IMAGE=example.com/img", "BUILD_ID=b1"}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "warning: could not remove candidate-b1") {
		t.Fatalf("untag with a refused delete: %v\n%s", err, out)
	}
}

// runStep runs step id's script under bash with /workspace at ws, bin
// first on PATH, and env.
func runStep(t *testing.T, cb cloudBuild, id, ws, bin string, env ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	st := cb.Steps[cb.step(t, id)]
	cmd := exec.Command(bash, "-c", strings.ReplaceAll(strings.ReplaceAll(st.Args[1], "$$", "$"), "/workspace", ws))
	cmd.Env = append([]string{"PATH=" + bin + ":" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestSmokeAndPromoteScripts runs the smoke and promote scripts with
// docker and gcloud stubbed: a failed or incomplete report fails the
// smoke; promote tags latest by the digest, and stands down when
// superseded, saying so in its step output.
func TestSmokeAndPromoteScripts(t *testing.T) {
	_, cb := loadCloudBuild(t)
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pass := `{"passed":true,"checks":[{"name":"user","ok":true}]}`
	rootPass := `{"passed":true,"checks":[{"name":"no-setuid","ok":true},{"name":"no-setgid","ok":true},{"name":"no-file-caps","ok":true}]}`
	for _, tc := range []struct {
		name, report, root string
		code               int
		ok                 bool
	}{
		{"pass", pass, rootPass, 0, true},
		{"failed selftest", `{"passed":false,"checks":[]}`, rootPass, 1, false},
		{"lying exit code", `{"passed":false,"checks":[]}`, rootPass, 0, false},
		{"root scan leaves one out", pass, `{"passed":true,"checks":[{"name":"no-setuid","ok":true},{"name":"no-setgid","ok":true}]}`, 0, false},
		{"no report", "", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ws, bin := filepath.Join(dir, "workspace"), filepath.Join(dir, "bin")
			testutil.WriteFiles(t, ws, map[string]string{"out/image-digest": digest + "\n", "out/selftest.json": `{"check_init":true}`})
			testutil.WriteFiles(t, bin, map[string]string{"docker": fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "docker $*" >> "$ARGV"
case "$*" in *"--user 0"*) printf '%%s' '%s' ;; *) cat > /dev/null; printf '%%s' '%s' ;; esac
exit %d
`, tc.root, tc.report, tc.code)})
			argv := filepath.Join(dir, "argv")
			out, err := runStep(t, cb, "smoke", ws, bin, "IMAGE=example.com/img", "ARGV="+argv)
			if (err == nil) != tc.ok {
				t.Fatalf("smoke err = %v, want ok %v\n%s", err, tc.ok, out)
			}
			logged, _ := os.ReadFile(argv)
			if !strings.Contains(string(logged), "example.com/img@"+digest) {
				t.Errorf("the smoke did not run the pushed digest:\n%s", logged)
			}
		})
	}

	for _, superseded := range []bool{false, true} {
		dir := t.TempDir()
		ws, bin := filepath.Join(dir, "workspace"), filepath.Join(dir, "bin")
		files := map[string]string{"out/image-digest": digest + "\n"}
		if superseded {
			files["out/superseded"] = ""
		}
		testutil.WriteFiles(t, ws, files)
		argv := filepath.Join(dir, "argv")
		testutil.WriteFiles(t, bin, map[string]string{"gcloud": "#!/bin/sh\nprintf '%s\\n' \"gcloud $*\" >> \"$ARGV\"\n"})
		builderOut := filepath.Join(dir, "builder-output")
		if err := os.MkdirAll(builderOut, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := runStep(t, cb, "promote", ws, bin, "IMAGE=example.com/img", "ARGV="+argv, "BUILDER_OUTPUT="+builderOut); err != nil {
			t.Fatalf("promote: %v\n%s", err, out)
		}
		// The step output tells fugaro image build that latest stayed.
		if got, _ := os.ReadFile(filepath.Join(builderOut, "output")); superseded != (string(got) == "superseded") {
			t.Errorf("superseded %v: promote's output = %q", superseded, got)
		}
		logged, _ := os.ReadFile(argv)
		want := "gcloud artifacts docker tags add example.com/img@" + digest + " example.com/img:latest --quiet\n"
		if superseded && len(logged) != 0 || !superseded && string(logged) != want {
			t.Errorf("superseded %v: gcloud calls %q", superseded, logged)
		}
	}
}

// TestCloudBuildCredentialOnlyInVolume: the git credential lives only in
// the fugaro-creds volume, which the steps that clone mount and render
// (which runs the repository's fugaro.yaml through the base image) does
// not. Only the credential step sees the provider secret itself, and no
// step writes a credential under /workspace or /builder/home, which every
// later step can read.
func TestCloudBuildCredentialOnlyInVolume(t *testing.T) {
	_, cb := loadCloudBuild(t)
	mounts := map[string]bool{}
	for _, s := range cb.Steps {
		for _, v := range s.Volumes {
			if v.Name == "fugaro-creds" && v.Path == "/creds" {
				mounts[s.ID] = true
			} else {
				t.Errorf("step %s mounts %s at %s", s.ID, v.Name, v.Path)
			}
		}
		for _, e := range s.SecretEnv {
			if (e == "GIT_TOKEN" || e == "GITHUB_APP_KEY") && s.ID != "credential" {
				t.Errorf("step %s sees the provider secret %s", s.ID, e)
			}
		}
		sc := strings.Join(s.Args, " ")
		if strings.Contains(sc, "/builder/home") {
			t.Errorf("step %s uses /builder/home, which every later step (render included) can read", s.ID)
		}
		if regexp.MustCompile(`/workspace/\S*git-credentials|/workspace/\S*creds`).MatchString(sc) {
			t.Errorf("step %s puts a credential under /workspace", s.ID)
		}
		for _, m := range regexp.MustCompile(`\S*git-credentials`).FindAllString(sc, -1) {
			if !strings.Contains(m, "/creds/git-credentials") && !strings.HasPrefix(m, `"id=git-credentials`) {
				t.Errorf("step %s names a credential file outside the volume: %s", s.ID, m)
			}
		}
	}
	for _, id := range []string{"prep", "credential", "source", "build"} {
		if !mounts[id] {
			t.Errorf("step %s does not mount fugaro-creds at /creds", id)
		}
	}
	if mounts["render"] || len(cb.Steps[cb.step(t, "render")].Volumes) != 0 {
		t.Error("the render step mounts a volume")
	}
	if cred := cb.Steps[cb.step(t, "credential")]; !slices.Equal(cred.SecretEnv, []string{"GIT_TOKEN"}) {
		t.Errorf("credential secretEnv = %v", cred.SecretEnv)
	}
}

// TestCloudBuildNoSubstitutionsInScripts keeps Cloud Build substitutions
// out of shell script text. Substitution is textual, so a crafted value (a
// branch name such as x$(curl…|sh) is a valid git ref) would run as shell
// next to GIT_CREDENTIALS. Each value must reach a shell step through env:
// instead, and every $$VAR a script reads must be one it was given.
func TestCloudBuildNoSubstitutionsInScripts(t *testing.T) {
	data, err := os.ReadFile("derived/cloudbuild.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cb cloudBuild
	if err := yaml.Unmarshal(data, &cb); err != nil {
		t.Fatal(err)
	}
	varRE := regexp.MustCompile(`\$\$\{?([A-Z][A-Z0-9_]*)`)
	for _, s := range cb.Steps {
		if s.Entrypoint != "bash" && s.Entrypoint != "sh" {
			continue
		}
		script := strings.Join(s.Args, "\n")
		if strings.Contains(script, "${_") {
			t.Errorf("step %s interpolates a substitution into its script; pass it through env: and read it as $$VAR", s.ID)
		}
		// Cloud Build sets BUILDER_OUTPUT in every step: a step's output
		// goes to $BUILDER_OUTPUT/output.
		given := map[string]bool{"BUILDER_OUTPUT": true}
		for _, e := range append(append([]string{}, s.Env...), s.SecretEnv...) {
			name, _, _ := strings.Cut(e, "=")
			given[name] = true
		}
		for _, m := range varRE.FindAllStringSubmatch(script, -1) {
			if !given[m[1]] {
				t.Errorf("step %s reads $$%s, which is not in its env: or secretEnv:", s.ID, m[1])
			}
		}
	}
}

// TestCIWorkflowUsesScripts keeps CI and local runs identical: the workflow
// may only build, smoke-test and scan through the scripts developers run.
func TestCIWorkflowUsesScripts(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"images/build-base.sh", "images/smoke.sh", "images/scan.sh", "go test -tags docker"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("images.yml does not run %s", want)
		}
	}
	if strings.Contains(string(data), "docker build ") {
		t.Error("images.yml calls docker build directly; use images/build-base.sh")
	}
}

// TestCIWorkflowTestsEveryTerraformRoot: CI validates and tests each
// Terraform root, and a missing or renamed root fails the job instead of
// being skipped.
func TestCIWorkflowTestsEveryTerraformRoot(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "|| continue") {
		t.Error("ci.yml skips a missing Terraform root")
	}
	// deploy/terraform embeds all of gcp/, so a root's in-tree .terraform
	// (the provider binaries) would end up in the Go test binary.
	if !strings.Contains(string(data), `TF_DATA_DIR="$RUNNER_TEMP/tf-$r"`) {
		t.Error("ci.yml runs terraform with its data directory inside the embedded tree")
	}
	roots, err := os.ReadDir("../deploy/terraform/gcp/roots")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roots {
		if r.IsDir() && !regexp.MustCompile(`for r in [a-z ]*\b`+r.Name()+`\b`).Match(data) {
			t.Errorf("ci.yml does not test the %s root", r.Name())
		}
	}
}

// TestCIWorkflowPermissionsScoped holds every workflow file to a read-only
// GITHUB_TOKEN by default: the workflow-level permissions must be exactly
// contents: read, and only a job whose `if:` gates on the resolved publish
// flag (never true for pull_request; see images.yml's "Pick the version"
// step) may declare packages:write.
func TestCIWorkflowPermissionsScoped(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(files) < 2 {
		t.Fatalf("workflow files = %v (%v)", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Permissions map[string]string `yaml:"permissions"`
			Jobs        map[string]struct {
				If          string            `yaml:"if"`
				Permissions map[string]string `yaml:"permissions"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
			t.Errorf("%s: workflow-level permissions = %+v, want only contents: read", f, wf.Permissions)
		}
		for name, job := range wf.Jobs {
			if job.Permissions["packages"] != "write" {
				continue
			}
			if !strings.Contains(job.If, "publish") {
				t.Errorf("%s: job %q grants packages:write but its `if:` (%q) does not gate on the resolved publish flag, so it could run on pull_request", f, name, job.If)
			}
		}
	}
}

// TestCIWorkflowNoUnsafeInterpolation keeps refname-derived and actor values
// out of `run:` script text, where a crafted ref name or username could
// inject shell. They must instead be passed through `env:` and referenced as
// shell variables.
func TestCIWorkflowNoUnsafeInterpolation(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for jobName, job := range wf.Jobs {
		for _, step := range job.Steps {
			for _, bad := range []string{"${{ needs.", "${{ github.actor"} {
				if strings.Contains(step.Run, bad) {
					t.Errorf("job %q step %q run: interpolates %q directly; pass it via env: and reference it as a shell variable", jobName, step.Name, bad)
				}
			}
		}
	}
}

// TestWorkflowActionsPinnedBySHA pins every third-party action in both
// workflows to a full commit SHA, with the exact release tag it sits on as
// a comment, so a moved or compromised tag can't change what CI runs, and a
// Dependabot config keeps the pins current.
func TestWorkflowActionsPinnedBySHA(t *testing.T) {
	usesRE := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)(.*)$`)
	pinnedRE := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	for _, wf := range []string{"../.github/workflows/images.yml", "../.github/workflows/ci.yml"} {
		data, err := os.ReadFile(wf)
		if err != nil {
			t.Fatal(err)
		}
		matches := usesRE.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("%s has no uses: lines", wf)
		}
		for _, m := range matches {
			if !pinnedRE.MatchString(m[1]) || !regexp.MustCompile(`^\s+# v\d+\.\d+\.\d+\s*$`).MatchString(m[2]) {
				t.Errorf("%s: %q is not pinned as owner/repo@<40-hex sha> # vX.Y.Z", wf, strings.TrimSpace(m[0]))
			}
		}
	}
	data, err := os.ReadFile("../.github/dependabot.yml")
	if err != nil {
		t.Fatal(err)
	}
	var db struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Schedule  struct {
				Interval string `yaml:"interval"`
			} `yaml:"schedule"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal(data, &db); err != nil {
		t.Fatal(err)
	}
	weekly := false
	for _, u := range db.Updates {
		weekly = weekly || (u.Ecosystem == "github-actions" && u.Schedule.Interval == "weekly")
	}
	if !weekly {
		t.Errorf("dependabot.yml has no weekly github-actions update: %+v", db.Updates)
	}
}

// versionStep extracts the version job's "Pick the version" script from
// images.yml, with the one GitHub expression it holds replaced, so it can
// run under plain sh.
func versionStep(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for _, s := range wf.Jobs["version"].Steps {
		if s.Name == "Pick the version" {
			return strings.ReplaceAll(s.Run, "${{ github.event.number }}", "7")
		}
	}
	t.Fatal("images.yml has no version job step named \"Pick the version\"")
	return ""
}

// TestCIWorkflowSkipsImagesTheTagTreeDoesNotHold: the nightly and manual runs
// build the latest release tag's tree, and an old tag (v0.1.0) holds no
// images/go or images/java-services: those legs must be skipped, not fail,
// and every step of a leg (build, publish, verify) is gated on the list.
func TestCIWorkflowSkipsImagesTheTagTreeDoesNotHold(t *testing.T) {
	testutil.IsolateGit(t)
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				ID   string `yaml:"id"`
				If   string `yaml:"if"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, s := range wf.Jobs["version"].Steps {
		if s.ID == "kinds" {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal("the version job has no kinds step")
	}
	repo := t.TempDir()
	testutil.Git(t, repo, "init", "--quiet", "-b", "main", repo)
	put := func(kind string) {
		dir := filepath.Join(repo, "images", kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("web-node")
	put("history")
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "--quiet", "-m", "v0.1.0 tree")
	testutil.Git(t, repo, "tag", "v0.1.0")
	put("go")
	put("java-services")
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "--quiet", "-m", "later")
	for tag, want := range map[string]string{"v0.1.0": ",web-node,history,", "": ",web-node,go,java-services,history,"} {
		out := filepath.Join(t.TempDir(), "output")
		cmd := exec.Command("sh", "-c", script)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "TAG="+tag, "GITHUB_OUTPUT="+out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("kinds (tag %q): %v\n%s", tag, err, b)
		}
		got, _ := os.ReadFile(out)
		if string(got) != "kinds="+want+"\n" {
			t.Errorf("tag %q: output %q, want kinds=%s", tag, got, want)
		}
	}
	// Every step of the legs that build, publish or verify one image is
	// gated on the list, so a missing image fails nothing.
	for _, job := range []string{"base", "history", "publish"} {
		for _, s := range wf.Jobs[job].Steps {
			if !strings.Contains(s.If, "needs.version.outputs.kinds") {
				t.Errorf("job %s step %q is not gated on the images the tree builds (if: %q)", job, s.Name, s.If)
			}
		}
	}
}

// TestCIWorkflowMovesMajorOnlyForTheNewestRelease: a backport tag publishes
// :X.Y.Z but must not move :X backwards past a newer release in the same
// major.
func TestCIWorkflowMovesMajorOnlyForTheNewestRelease(t *testing.T) {
	testutil.IsolateGit(t)
	script := versionStep(t)
	repo := t.TempDir()
	testutil.Git(t, repo, "init", "--quiet", "-b", "main", repo)
	testutil.Git(t, repo, "commit", "--quiet", "--allow-empty", "-m", "seed")
	for _, tag := range []string{"v1.1.4", "v1.1.5", "v1.2.0", "v1.10.0-rc1", "v2.0.0", "v10.0.0"} {
		testutil.Git(t, repo, "tag", tag)
	}
	for _, tc := range []struct {
		event, ref, version string
		moveMajor           bool
	}{
		{"push", "v1.1.5", "1.1.5", false},
		{"push", "v1.2.0", "1.2.0", true},
		{"push", "v2.0.0", "2.0.0", true},
		{"push", "v10.0.0", "10.0.0", true},
		{"schedule", "main", "10.0.0", true},
	} {
		t.Run(tc.event+" "+tc.ref, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "output")
			cmd := exec.Command("sh", "-c", script)
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "GITHUB_EVENT_NAME="+tc.event, "GITHUB_REF_NAME="+tc.ref, "GITHUB_OUTPUT="+out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Pick the version: %v\n%s", err, b)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			for _, want := range []string{"version=" + tc.version + "\n", "publish=true\n", fmt.Sprintf("move_major=%t\n", tc.moveMajor)} {
				if !strings.Contains(got, want) {
					t.Errorf("outputs lack %q:\n%s", want, got)
				}
			}
		})
	}
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "MOVE_MAJOR: ${{ needs.version.outputs.move_major }}") {
		t.Error("the publish step does not take move_major from the version job")
	}
}

// TestCIWorkflowJobsHaveTimeouts bounds every job in every workflow, so a
// hung build or test can't hold a runner for GitHub's 6-hour default.
func TestCIWorkflowJobsHaveTimeouts(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("workflow files = %v (%v)", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Jobs map[string]struct {
				TimeoutMinutes int `yaml:"timeout-minutes"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for name, job := range wf.Jobs {
			if job.TimeoutMinutes <= 0 || job.TimeoutMinutes > 120 {
				t.Errorf("%s: job %q timeout-minutes = %d, want 1..120", f, name, job.TimeoutMinutes)
			}
		}
	}
}

// TestCIWorkflowCheckoutsDropCredentials: no job needs the GITHUB_TOKEN in
// .git/config after checkout, so every actions/checkout sets
// persist-credentials: false.
func TestCIWorkflowCheckoutsDropCredentials(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("workflow files = %v (%v)", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Jobs map[string]struct {
				Steps []struct {
					Uses string         `yaml:"uses"`
					With map[string]any `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for name, job := range wf.Jobs {
			for _, s := range job.Steps {
				if strings.HasPrefix(s.Uses, "actions/checkout@") && s.With["persist-credentials"] != false {
					t.Errorf("%s: job %q checks out without persist-credentials: false", f, name)
				}
			}
		}
	}
}

// TestRenderStepTakesTheProjectLayer: the render step's substitution and
// script carry the project layer's sha256 to the hidden --layer-* flags,
// and an empty sum (no layer) renders exactly as before (no flags added).
func TestRenderStepTakesTheProjectLayer(t *testing.T) {
	var cb struct {
		Substitutions map[string]string `yaml:"substitutions"`
		Steps         []struct {
			ID   string   `yaml:"id"`
			Env  []string `yaml:"env"`
			Args []string `yaml:"args"`
		} `yaml:"steps"`
	}
	if err := yaml.Unmarshal(images.CloudBuild, &cb); err != nil {
		t.Fatal(err)
	}
	if v, ok := cb.Substitutions["_PROJECT_LAYER_SHA256"]; !ok || v != "" {
		t.Fatalf("_PROJECT_LAYER_SHA256 = %q, %v", v, ok)
	}
	for _, s := range cb.Steps {
		if s.ID != "render" {
			continue
		}
		env, script := strings.Join(s.Env, " "), strings.Join(s.Args, " ")
		for _, want := range []string{"LAYER_SHA=${_PROJECT_LAYER_SHA256}", "BUCKET=${_BUCKET}", "SLUG=${_SLUG}"} {
			if !strings.Contains(env, want) {
				t.Errorf("render env lacks %s", want)
			}
		}
		for _, want := range []string{`if [ -n "$$LAYER_SHA" ]`, `--layer-sha256 "$$LAYER_SHA"`, `fugaro image render --workflow "$$WORKFLOW" --cloud-outputs /workspace/out "$$@"`} {
			if !strings.Contains(script, want) {
				t.Errorf("render script lacks %s", want)
			}
		}
		return
	}
	t.Fatal("no render step")
}

// TestCloudBuildCredentialScripts runs the source and build step scripts
// under bash, with git and docker stubbed, against a credential file in a
// stand-in for the volume: both hand git and BuildKit that file (never its
// contents on an argv), the build hands each workflow secret over as a
// private file, and the build removes the credential when it exits.
func TestCloudBuildCredentialScripts(t *testing.T) {
	testutil.IsolateGit(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	_, cb := loadCloudBuild(t)
	const line = "https://x-token-auth:t%2Fo%40k@bitbucket.org\n"
	const npmToken = "-n 'p\"m %s\\n"
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	bin := filepath.Join(dir, "bin")
	creds := filepath.Join(dir, "creds")
	for _, d := range []string{ws, bin, creds} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	credFile := filepath.Join(creds, "git-credentials")
	if err := os.WriteFile(credFile, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(dir, "argv")
	// The stubs log their argv and copy the files they are pointed at, the
	// way git's store helper and docker's --secret read them.
	stubs := map[string]string{
		"git": `#!/bin/sh
printf '%s\n' "git $*" >> "$ARGV"
for a in "$@"; do case "$a" in credential.helper=store\ --file=*) cp "${a#credential.helper=store --file=}" "$CAPTURE/source" ;; esac; done
`,
		"docker": `#!/bin/sh
printf '%s\n' "docker $*" >> "$ARGV"
case "$1 $2 $5" in
  "image inspect example.com/img:candidate-b7") echo "example.com/img@sha256:$DIGEST"; exit 0 ;;
  "image inspect "*) echo "example.com/base@sha256:abc"; exit 0 ;;
esac
for a in "$@"; do case "$a" in
  id=git-credentials,src=*) cp "${a#id=git-credentials,src=}" "$CAPTURE/build" ;;
  id=NPM_TOKEN,src=*) f=${a#id=NPM_TOKEN,src=}; cp "$f" "$CAPTURE/npm"; ls -l "$f" | cut -c1-10 > "$CAPTURE/npm-mode" ;;
esac; done
`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	capture := filepath.Join(dir, "capture")
	tmp := filepath.Join(dir, "tmp")
	for _, d := range []string{capture, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testutil.WriteFiles(t, ws, map[string]string{"out/source-commit": "c0ffee\n", "out/built-at": "2026-09-29T10:00:00Z\n"})
	builderOut := filepath.Join(dir, "builder-output")
	if err := os.MkdirAll(builderOut, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"source", "build"} {
		st := cb.Steps[cb.step(t, id)]
		script := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(st.Args[1], "$$", "$"), "/workspace", ws), "/creds", creds)
		cmd := exec.Command(bash, "-c", script)
		cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + tmp, "ARGV=" + argv, "CAPTURE=" + capture,
			"REPO_URL=https://bitbucket.org/acme/app.git", "BASE_BRANCH=main",
			"FUGARO_BASE=example.com/base:1", "IMAGE=example.com/img", "SECRET_ENVS=NPM_TOKEN", "NPM_TOKEN=" + npmToken,
			"BUILD_ID=b7", "BUILDER_OUTPUT=" + builderOut, "DIGEST=" + digest}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("step %s: %v\n%s", st.ID, err, out)
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("step %s left %v in its temp dir", st.ID, left)
		}
	}
	if _, err := os.Stat(credFile); !os.IsNotExist(err) {
		t.Errorf("the build step left the git credential in the volume (%v)", err)
	}
	logged, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "t%2Fo") || !regexp.MustCompile(`--secret id=NPM_TOKEN,src=\S*/NPM_TOKEN `).Match(logged) || strings.Contains(string(logged), "env=") {
		t.Errorf("argv:\n%s", logged)
	}
	for _, want := range []string{
		"--build-arg FUGARO_BUILT_AT=2026-09-29T10:00:00Z", "--build-arg FUGARO_COMMIT=c0ffee",
		"--label org.opencontainers.image.revision=c0ffee",
		"--label org.opencontainers.image.created=2026-09-29T10:00:00Z", "--label dev.fugaro.base.digest=sha256:abc",
		"--tag example.com/img:candidate-b7 ", "docker push example.com/img:candidate-b7\n",
	} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("the build lacks %q:\n%s", want, logged)
		}
	}
	if strings.Contains(string(logged), ":latest") {
		t.Errorf("the build step touched latest:\n%s", logged)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "out", "image-digest")); string(got) != "sha256:"+digest+"\n" {
		t.Errorf("image-digest = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(builderOut, "output")); string(got) != "sha256:"+digest {
		t.Errorf("the build step's output = %q", got)
	}
	if !strings.Contains(string(logged), "--secret id=git-credentials,src="+credFile) {
		t.Errorf("the build does not hand BuildKit the volume's credential file:\n%s", logged)
	}
	// The build step hands docker the workflow secret as a file it wrote
	// with umask 077, holding the value exactly, never on its argv.
	if got, err := os.ReadFile(filepath.Join(capture, "npm")); err != nil || string(got) != npmToken {
		t.Errorf("the NPM_TOKEN secret file = %q, %v; want %q", got, err, npmToken)
	}
	if mode, _ := os.ReadFile(filepath.Join(capture, "npm-mode")); string(mode) != "-rw-------\n" {
		t.Errorf("the NPM_TOKEN secret file mode = %q, want -rw-------", mode)
	}
	if strings.Contains(string(logged), "p\"m") {
		t.Error("the workflow secret value reached docker's argv")
	}
	for _, f := range []string{"source", "build"} {
		if got, err := os.ReadFile(filepath.Join(capture, f)); err != nil || string(got) != line {
			t.Errorf("%s read the credential file as %q, %v", f, got, err)
		}
	}
}
