//go:build docker

package e2e

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// implementWarm commits a copy of build/warm-marker, which the image's setup
// step wrote. git ignores build/, so the file only survives if bootstrap
// kept the image's warm caches.
const implementWarm = `{"shell":"cp build/warm-marker feature.txt && git add feature.txt && git commit -qm 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nCopies the warm marker.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","cost":1}`

func TestExecInDerivedImage(t *testing.T) {
	base := testutil.BaseImage(t)
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	maps.Copy(files, testutil.NPMFixtureFiles())
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "    base: web-node\n",
		"    base: web-node\n    image:\n      setup: [\"mkdir -p build && echo warm > build/warm-marker\"]\n", 1)
	remote := testutil.NewRemote(t, files)

	// Build the derived image from a developer-style clone.
	checkout := filepath.Join(t.TempDir(), "app")
	testutil.Git(t, filepath.Dir(checkout), "clone", "--quiet", remote, checkout)
	const tag = "fugaro-e2e-app:test"
	build := exec.Command(testutil.BuildFugaro(t), "image", "build", "--local", "--json",
		"--base", base, "--tag", tag, "--platform", testutil.DockerPlatform(t))
	build.Dir = checkout
	var stdout, stderr bytes.Buffer
	build.Stdout, build.Stderr = &stdout, &stderr
	buildErr := build.Run()
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "--force", tag).Run() })
	var built image.LocalResult
	if err := json.Unmarshal(stdout.Bytes(), &built); err != nil || buildErr != nil || built.Smoke == nil || !built.Smoke.Passed {
		t.Fatalf("image build: %v, %v\n%s\n%s", buildErr, err, stdout.String(), testutil.Tail(stderr.String()))
	}

	// A commit that lands after the image was built: the run must fetch it.
	delta := filepath.Join(t.TempDir(), "delta")
	testutil.Git(t, filepath.Dir(delta), "clone", "--quiet", remote, delta)
	testutil.WriteFiles(t, delta, map[string]string{"CHANGELOG.md": "delta\n"})
	testutil.Git(t, delta, "add", "-A")
	testutil.Git(t, delta, "commit", "--quiet", "-m", "Delta after the image build")
	testutil.Git(t, delta, "push", "--quiet", "origin", "HEAD:main")

	run := t.TempDir()
	claudeDir := filepath.Join(run, "claude")
	for _, d := range []string{claudeDir, filepath.Join(run, "bucket")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claude := testutil.LinuxBinary(t, "github.com/dimipaun/fugaro/internal/agent/fakeclaude", claudeDir, "claude")
	testutil.WriteFiles(t, run, map[string]string{
		"claude/script.json": `{"calls":[` + implementWarm + `,` + reviewShip + `]}`,
		"task.json":          `{"version":1,"run_id":"` + runID + `","repo":"acme/app","ref":"main","task":"Add a feature"}`,
		"fails":              "",
		// The bare remote belongs to the host user, and git in the container
		// runs as uid 1000, so it needs safe.directory. It is set through
		// GIT_CONFIG_GLOBAL rather than GIT_CONFIG_COUNT, which M2's runner
		// uses for credentials.
		"gitconfig": "[safe]\n\tdirectory = *\n",
	})
	remoteDir := filepath.Dir(remote)
	testutil.ShareWithContainer(t, base, run)
	testutil.ShareWithContainer(t, base, remoteDir)

	cmd := exec.Command("docker", "run", "--rm",
		// The image's origin is the remote's host path, so mount it at that path.
		"-v", remoteDir+":"+remoteDir, "-v", run+":/mnt/run",
		"-e", "ANTHROPIC_API_KEY=test-key-1234", "-e", "FIXTURE_FAILS_FILE=/mnt/run/fails",
		"-e", "GIT_CONFIG_GLOBAL=/mnt/run/gitconfig",
		// no_tmp_dir: fileblob otherwise stages writes in os.TempDir before
		// renaming into the bucket, and inside the container os.TempDir
		// (the container's own filesystem) is a different mount than the
		// bind-mounted bucket directory, so the rename fails with EXDEV.
		tag, "fugaro", "exec", "--bucket", "file:///mnt/run/bucket?no_tmp_dir=true", "--task-file", "/mnt/run/task.json",
		"--provider", "fake", "--provider-state", "/mnt/run/provider.json",
		"--claude", "/mnt/run/claude/claude", "--cancel-poll", "100ms")
	out, err := cmd.CombinedOutput()
	t.Logf("fugaro exec in %s (err=%v):\n%s", tag, err, out)
	if err != nil {
		t.Fatalf("fugaro exec: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(run, "bucket", "runs", acmeSlug, runID, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec runstore.Record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record %+v", rec)
	}
	provider, err := fake.Load(filepath.Join(run, "provider.json"))
	if err != nil || len(provider.PRs) != 1 || provider.PRs[0].Draft {
		t.Fatalf("provider %+v, %v", provider, err)
	}
	branch := "fugaro/" + runID
	if got := testutil.Git(t, remote, "show", branch+":feature.txt"); got != "warm" {
		t.Errorf("feature.txt = %q; the image's ignored warm files did not survive bootstrap", got)
	}
	if log := testutil.Git(t, remote, "log", "--format=%s", branch); !strings.Contains(log, "Delta after the image build") {
		t.Errorf("the branch lacks the commit pushed after the image build:\n%s", log)
	}
	calls := testutil.FakeClaudeCalls(t, claude)
	if len(calls) != 2 || calls[0].Dir != "/work/repo" || !slices.Contains(calls[0].Env, "HOME=/home/fugaro") {
		t.Fatalf("calls = %+v", calls)
	}
	if slices.ContainsFunc(calls[0].Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") }) {
		t.Error("the runner's git config reached the agent")
	}
}
