package images_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func baseDockerfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("base/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Every pin is one ARG line, every checksum 64 hex digits, every pinned
// download has both architectures, the OS is pinned by digest, and the
// hardening and the layer order of design sections 3, 5 and 7 hold.
func TestBaseDockerfileStripsKeysignAndPinsEverything(t *testing.T) {
	df := baseDockerfile(t)
	if !regexp.MustCompile(`(?m)^ARG DEBIAN_DIGEST=sha256:[0-9a-f]{64}$`).MatchString(df) || !strings.Contains(df, "\nFROM debian:trixie-slim@${DEBIAN_DIGEST}\n") {
		t.Error("the final stage is not debian:trixie-slim pinned by digest")
	}
	args := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^ARG ([A-Z0-9_]+)=(\S+)$`).FindAllStringSubmatch(df, -1) {
		args[m[1]] = m[2]
	}
	for name, v := range args {
		if strings.Contains(name, "_SHA256_") && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(v) {
			t.Errorf("ARG %s=%s is not a sha256", name, v)
		}
	}
	for _, p := range []string{"MISE", "CLAUDE_CODE", "GH", "YQ", "GCLOUD", "DOCKER_CLI", "CODEX", "OPENCODE", "GOOSE", "CRUSH"} {
		if args[p+"_VERSION"] == "" || args[p+"_SHA256_AMD64"] == "" || args[p+"_SHA256_ARM64"] == "" {
			t.Errorf("%s lacks its version or a per-architecture sha256", p)
		}
	}
	if args["HARNESS_NODE_VERSION"] == "" {
		t.Error("no HARNESS_NODE_VERSION pin")
	}
	for _, must := range []string{
		"apt-get upgrade -y",
		"chmod u-s /usr/lib/openssh/ssh-keysign",
		"/usr/sbin/policy-rc.d",
		"create_main_cluster = false",
		"useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro",
		"install -d -o fugaro -g fugaro -m 0700 /work/creds",
		"COPY images/base/mise-system.toml /etc/mise/config.toml",
		`ENTRYPOINT ["/usr/bin/tini", "--"]`,
		`CMD ["fugaro", "exec"]`,
	} {
		if !strings.Contains(df, must) {
			t.Errorf("the Dockerfile lacks %q", must)
		}
	}
	// Every download goes through fetch.sh or install-node: no curl of its
	// own, and nothing piped into a shell.
	for _, line := range strings.Split(df, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if regexp.MustCompile(`\bcurl\s+-`).MatchString(line) || regexp.MustCompile(`\|\s*(ba)?sh\b`).MatchString(line) {
			t.Errorf("a download outside fetch.sh: %s", line)
		}
	}
	// Layer order: fugaro is the last thing copied, and the image ends as
	// fugaro in /work/repo.
	instr := regexp.MustCompile(`(?m)^(FROM|RUN|COPY|USER|WORKDIR)\b.*$`).FindAllString(df, -1)
	last := ""
	for _, in := range instr {
		if strings.HasPrefix(in, "RUN") || strings.HasPrefix(in, "COPY") {
			last = in
		}
	}
	if last != "COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro" {
		t.Errorf("the last layer is %q, want the fugaro binary", last)
	}
	if !strings.Contains(df, "USER fugaro\nWORKDIR /work/repo") {
		t.Error("the image does not end as fugaro in /work/repo")
	}
}

func TestBaseHarnessLockfileMatchesPackageJSON(t *testing.T) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version      string            `json:"version"`
			Integrity    string            `json:"integrity"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	for path, v := range map[string]any{"base/harnesses/package.json": &pkg, "base/harnesses/package-lock.json": &lock} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if lock.LockfileVersion < 3 {
		t.Errorf("lockfileVersion %d, want 3", lock.LockfileVersion)
	}
	for name, v := range pkg.Dependencies {
		if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
			t.Errorf("%s@%s is not an exact version", name, v)
		}
		got := lock.Packages["node_modules/"+name]
		if got.Version != v || got.Integrity == "" {
			t.Errorf("the lockfile has %s at %q (integrity %q), package.json says %s", name, got.Version, got.Integrity, v)
		}
	}
	df := baseDockerfile(t)
	for _, entry := range []string{"@google/gemini-cli/bundle/gemini.js", "@mariozechner/pi-coding-agent/dist/cli.js", "@qwen-code/qwen-code/cli-entry.js"} {
		if !strings.Contains(df, entry) {
			t.Errorf("no wrapper for %s", entry)
		}
		pkgName := strings.Join(strings.Split(entry, "/")[:2], "/")
		if _, ok := pkg.Dependencies[pkgName]; !ok {
			t.Errorf("%s has a wrapper but is not a dependency", pkgName)
		}
	}
}

func TestMiseSystemConfig(t *testing.T) {
	data, err := os.ReadFile("base/mise-system.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`trusted_config_paths = ["/work/repo"]`,
		`idiomatic_version_file_enable_tools = []`,
		`auto_install = false`,
		`not_found_auto_install = false`,
		`yes = true`,
		`corepack = true`,
	} {
		if !strings.Contains(string(data), line) {
			t.Errorf("mise-system.toml lacks %s", line)
		}
	}
}

// fetch.sh refuses anything but https and a 64-digit sha256 before it
// downloads, and removes a download whose sum is wrong.
func TestFetchRefusesHTTPAndBadSums(t *testing.T) {
	bin := t.TempDir()
	// The fake curl writes "hello" to its -o argument.
	fake := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then printf hello > \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	const helloSum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	run := func(url, sum, out string) (string, error) {
		cmd := exec.Command("sh", "base/fetch.sh", url, sum, out)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	out := filepath.Join(t.TempDir(), "f")
	if msg, err := run("http://example.com/x", helloSum, out); err == nil || !strings.Contains(msg, "is not https") {
		t.Errorf("http: err=%v %s", err, msg)
	}
	if msg, err := run("https://example.com/x", "abc", out); err == nil || !strings.Contains(msg, "bad sha256") {
		t.Errorf("short sum: err=%v %s", err, msg)
	}
	if msg, err := run("https://example.com/x", strings.Repeat("0", 64), out); err == nil || !strings.Contains(msg, "does not match") {
		t.Errorf("wrong sum: err=%v %s", err, msg)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a download with the wrong sum was kept")
	}
	if msg, err := run("https://example.com/x", helloSum, out); err != nil {
		t.Errorf("right sum: %v %s", err, msg)
	}
}

func TestInstallNodeHonoursThePrefix(t *testing.T) {
	data, err := os.ReadFile("base/install-node.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `prefix=${NODE_PREFIX:-/opt/node}`) {
		t.Error("install-node has no NODE_PREFIX")
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "/opt/node") && !strings.Contains(line, "NODE_PREFIX") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("a hard-coded /opt/node: %s", line)
		}
	}
}
