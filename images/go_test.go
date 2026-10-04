package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func goDockerfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("go/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func argIn(t *testing.T, dockerfile, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^ARG ` + regexp.QuoteMeta(name) + `=(\S+)$`).FindStringSubmatch(dockerfile)
	if m == nil {
		t.Fatalf("no ARG %s=... line", name)
	}
	return m[1]
}

// runSmokeGo runs smoke.sh against the go base with a fake docker; the fake
// image reports the Dockerfile's pins unless fakeEnv overrides them.
func runSmokeGo(t *testing.T, missing string, fakeEnv ...string) (string, error) {
	t.Helper()
	df := goDockerfile(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "smoke.sh", "fake-image", "go")
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "CLAUDE_CODE_VERSION", "GH_VERSION", "GO_VERSION", "TERRAFORM_VERSION":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"),
		"FAKE_CLAUDE_VERSION="+argIn(t, df, "CLAUDE_CODE_VERSION"),
		"FAKE_GH_VERSION="+argIn(t, df, "GH_VERSION"),
		"FAKE_GO_VERSION="+argIn(t, df, "GO_VERSION"),
		"FAKE_TERRAFORM_VERSION="+argIn(t, df, "TERRAFORM_VERSION"))
	cmd.Env = append(cmd.Env, fakeEnv...)
	if missing != "" {
		cmd.Env = append(cmd.Env, "FAKE_DOCKER_MISSING="+missing)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSmokeGoSucceedsAgainstAHealthyImage(t *testing.T) {
	out, err := runSmokeGo(t, "")
	if err != nil || !strings.Contains(out, "smoke: fake-image ok") {
		t.Fatalf("smoke.sh go: %v\n%s", err, out)
	}
}

func TestSmokeGoFailsWhenAToolIsMissing(t *testing.T) {
	for _, missing := range []string{"fugaro", "claude", "gh", "go", "terraform", "make", "gcc"} {
		t.Run(missing, func(t *testing.T) {
			out, err := runSmokeGo(t, missing)
			if err == nil || strings.Contains(out, "smoke: fake-image ok") {
				t.Fatalf("smoke.sh passed with %s missing:\n%s", missing, out)
			}
		})
	}
}

func TestSmokeGoChecksThePins(t *testing.T) {
	df := goDockerfile(t)
	for _, tc := range []struct{ fake, pin string }{
		{"FAKE_GO_VERSION=1.0.0", "GO_VERSION=" + argIn(t, df, "GO_VERSION")},
		{"FAKE_TERRAFORM_VERSION=0.0.1", "TERRAFORM_VERSION=" + argIn(t, df, "TERRAFORM_VERSION")},
		{"FAKE_CLAUDE_VERSION=0.0.1", "CLAUDE_CODE_VERSION=" + argIn(t, df, "CLAUDE_CODE_VERSION")},
		{"FAKE_GH_VERSION=0.0.1", "GH_VERSION=" + argIn(t, df, "GH_VERSION")},
		{"FAKE_GOTOOLCHAIN=auto", "GOTOOLCHAIN is not local"},
	} {
		t.Run(tc.fake, func(t *testing.T) {
			out, err := runSmokeGo(t, "", tc.fake)
			if err == nil || !strings.Contains(out, tc.pin) {
				t.Fatalf("smoke.sh with %s: err=%v\n%s", tc.fake, err, out)
			}
		})
	}
}

// The go base repeats web-node's gh and Claude Code install. A bump to one
// that isn't made in the other would leave the two bases on different tools.
func TestGoBaseSharesWebNodePins(t *testing.T) {
	web, err := os.ReadFile("web-node/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := goDockerfile(t)
	for _, name := range []string{"CLAUDE_CODE_VERSION", "GH_VERSION"} {
		if got, want := argIn(t, df, name), argIn(t, string(web), name); got != want {
			t.Errorf("go base %s = %s, web-node has %s", name, got, want)
		}
	}
	for _, sum := range regexp.MustCompile(`[0-9a-f]{64}`).FindAllString(string(web), -1) {
		if !strings.Contains(df, sum) {
			t.Errorf("web-node's checksum %s is missing from the go base", sum)
		}
	}
}

// GO_VERSION must satisfy go.mod (GOTOOLCHAIN=local would refuse an older
// toolchain), and TERRAFORM_VERSION must be the one CI tests with.
func TestGoBasePinsMatchTheRepository(t *testing.T) {
	df := goDockerfile(t)
	mod, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\d+\.\d+)(\.\d+)?$`).FindStringSubmatch(string(mod))
	if m == nil {
		t.Fatal("go.mod has no go directive")
	}
	if gv := argIn(t, df, "GO_VERSION"); !strings.HasPrefix(gv, m[1]+".") {
		t.Errorf("GO_VERSION %s is not in go.mod's %s series", gv, m[1])
	}
	ci, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	tm := regexp.MustCompile(`terraform_version: ([\d.]+)`).FindStringSubmatch(string(ci))
	if tm == nil {
		t.Fatal("ci.yml has no terraform_version")
	}
	if tv := argIn(t, df, "TERRAFORM_VERSION"); tv != tm[1] {
		t.Errorf("TERRAFORM_VERSION %s, ci.yml uses %s", tv, tm[1])
	}
}

// Every base the images workflow builds is also published (the publish matrix
// is the base matrix plus the history image).
func TestImagesWorkflowBuildsAndPublishesEveryBase(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "base: [web-node, go, java-services]"); n != 1 {
		t.Errorf("images.yml lists the bases in %d build matrices, want 1", n)
	}
	if n := strings.Count(string(data), "base: [web-node, go, java-services, history]"); n != 1 {
		t.Errorf("images.yml lists the published images in %d publish matrices, want 1", n)
	}
}
