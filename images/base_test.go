package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/image"
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
	for _, p := range []string{"MISE", "CLAUDE_CODE", "GH", "YQ", "GCLOUD", "DOCKER_CLI", "UV"} {
		if args[p+"_VERSION"] == "" || args[p+"_SHA256_AMD64"] == "" || args[p+"_SHA256_ARM64"] == "" {
			t.Errorf("%s lacks its version or a per-architecture sha256", p)
		}
	}
	for _, must := range []string{
		"apt-get upgrade -y",
		"chmod u-s /usr/lib/openssh/ssh-keysign",
		"chmod g-s /usr/bin/ssh-agent",
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
	// Every download goes through fetch.sh: no curl of its own, and nothing
	// piped into a shell.
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

// TestBaseHasOnlyClaudeHarness: the base carries Claude Code alone among the
// coding-agent harnesses (F8, image setup skill IS4). The seven non-Claude
// harnesses -- codex, gemini, pi, qwen, opencode, goose, crush -- are absent
// from the presence table, from the Dockerfile's install steps and its ARG
// pins, and from /etc/fugaro/base.json. They were bundled "so that choosing a
// harness can later be a configuration decision" (design base-image.md
// section 6) and nothing in Fugaro ever started one; Claude Code is the one
// the runner drives, and it stays. graphify is not a coding-agent harness and
// stays on the harness tier (U5).
func TestBaseHasOnlyClaudeHarness(t *testing.T) {
	removed := []string{"codex", "gemini", "pi", "qwen", "opencode", "goose", "crush"}
	tools, err := image.ParseTools(images.BaseTools)
	if err != nil {
		t.Fatal(err)
	}
	claude := false
	for _, tool := range tools {
		if tool.Name == "claude" {
			claude = true
		}
		if slices.Contains(removed, tool.Name) {
			t.Errorf("tools.tsv still has a %s row (tier %s): the base carries Claude Code alone", tool.Name, tool.Tier)
		}
	}
	if !claude {
		t.Error("tools.tsv has no claude row: Claude Code is the one harness the base keeps")
	}

	df := baseDockerfile(t)
	for _, gone := range []string{
		// The three npm entry points the harness stage wrapped, and the stage
		// itself: its lockfile, its install loop and the private Node it ran on.
		"@google/gemini-cli/bundle/gemini.js",
		"@mariozechner/pi-coding-agent/dist/cli.js",
		"@qwen-code/qwen-code/cli-entry.js",
		"images/base/harnesses/",
		"npm ci --ignore-scripts",
		"for h in",
		// The four binary harnesses and their per-architecture pins.
		"one codex",
		"one opencode",
		"one goose",
		"one crush",
	} {
		if strings.Contains(df, gone) {
			t.Errorf("the Dockerfile still installs a non-Claude harness: it contains %q", gone)
		}
	}
	for _, arg := range []string{"HARNESS_NODE_VERSION", "CODEX_VERSION", "OPENCODE_VERSION", "GOOSE_VERSION", "CRUSH_VERSION"} {
		if strings.Contains(df, arg) {
			t.Errorf("the Dockerfile still pins %s, which only the removed harnesses used", arg)
		}
	}
	for _, field := range []string{"harness_node", "codex", "opencode", "goose", "crush"} {
		if strings.Contains(df, `"`+field+`"`) {
			t.Errorf(`/etc/fugaro/base.json still records %q`, field)
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

// miseCacheDir is where mise keeps the downloads a run reuses, and where a
// derived build mounts its BuildKit cache (derived/Dockerfile.tmpl).
const miseCacheDir = "/home/fugaro/.cache/mise"

// fugaroOwnedDirs are the directories the base's Dockerfile creates as
// fugaro with mode 0755, read from its `install -d` invocations (a
// continuation line belongs to the line above it).
func fugaroOwnedDirs(df string) map[string]bool {
	owned := map[string]bool{}
	re := regexp.MustCompile(`install -d -o fugaro -g fugaro -m 0755 ([^&|\n]*)`)
	for _, m := range re.FindAllStringSubmatch(strings.ReplaceAll(df, "\\\n", " "), -1) {
		for _, dir := range strings.Fields(m[1]) {
			owned[dir] = true
		}
	}
	return owned
}

// TestMiseCacheDirIsWritableByFugaro pins the fix for the run-time warning
// "mise WARN failed to write cache file: ~/.cache/mise/terraform/1.16.4/...:
// Permission denied". A derived build mounts a BuildKit cache on
// ~/.cache/mise, and BuildKit creates a mount target that does not exist
// itself, as root: that empty root-owned directory ships in the image and
// the run's mise, as fugaro, cannot write in it. The base creates it as
// fugaro, so the mount lands on a directory its owner can write.
func TestMiseCacheDirIsWritableByFugaro(t *testing.T) {
	owned := fugaroOwnedDirs(baseDockerfile(t))
	if !owned[miseCacheDir] {
		t.Errorf("the Dockerfile does not create %s as fugaro: a derived build's cache mount would create it as root", miseCacheDir)
	}
	tmpl, err := os.ReadFile("derived/Dockerfile.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	// Every cache mount the derived template puts under fugaro's home has to
	// be one of those directories, or the two drift apart and the run gets a
	// root-owned cache again.
	mounts := regexp.MustCompile(`--mount=type=cache,[^ ]*target=(/home/fugaro/[^,\s]+)`).FindAllStringSubmatch(string(tmpl), -1)
	if len(mounts) == 0 {
		t.Error("the derived template mounts no cache under /home/fugaro: has the mise step moved?")
	}
	for _, m := range mounts {
		if !owned[m[1]] {
			t.Errorf("the derived template mounts a cache on %s, which the base does not create as fugaro", m[1])
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

// Every curl in the base image's download scripts retries a transient
// failure, and every download has an HTTP/1.1 fallback. One "curl: (92)
// HTTP/2 stream was not closed cleanly" failed the fugaro-base amd64 build
// on PR #903 and the docker-tests job on PR #896, and both passed on a
// rerun: the base was failing builds on a connection the next attempt would
// have carried, so a retry belongs in the script and not in a reviewer's
// rerun.
//
// The retries change nothing about the check that follows them: fetch.sh
// still removes a download whose pinned sha256 does not match and exits 1,
// so a retried download carries no byte the untried one could not, and a
// failure still fails the build closed.
//
// The HTTP/1.1 attempt is only ever the second half of an "||" fallback: a
// first attempt that spoke HTTP/1.1 outright would give up the HTTP/2 the
// retried attempt uses, and retrying is what actually fixes a connection
// that was reset rather than an HTTP/2 framing failure that was not.
func TestBaseFetchScriptsRetryDownloads(t *testing.T) {
	paths, err := filepath.Glob("base/*.sh")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no scripts under base/ to check: has fetch.sh moved?")
	}
	curlRe := regexp.MustCompile(`(^|[\s|(&])curl\s`)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		calls, fallbacks := 0, 0
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || !curlRe.MatchString(line) {
				continue
			}
			calls++
			for _, flag := range []string{"--retry 5", "--retry-all-errors", "--retry-delay 2"} {
				if !strings.Contains(line, flag) {
					t.Errorf("%s:%d: curl without %s: %s", path, i+1, flag, trimmed)
				}
			}
			if !strings.Contains(line, "--http1.1") {
				continue
			}
			fallbacks++
			if !strings.Contains(trimmed, "||") {
				t.Errorf("%s:%d: --http1.1 is the fallback attempt, so it belongs behind \"||\": %s", path, i+1, trimmed)
			}
		}
		if calls > 0 && fallbacks == 0 {
			t.Errorf("%s downloads over curl with no HTTP/1.1 fallback", path)
		}
	}
}

// The static guard above says the flags are spelled; this says they arrive:
// fetch.sh's second (HTTP/1.1) attempt is the one that runs when the first
// curl fails, and the sha256 check applies to whatever that attempt left
// behind. A download that only succeeded on the fallback is a build that
// passes, and one that failed both attempts still fails closed.
func TestFetchFallsBackAndVerifiesTheRetriedBytes(t *testing.T) {
	bin := t.TempDir()
	// The fake curl exits 92 -- "HTTP/2 stream was not closed cleanly", the
	// failure this change exists for -- on its first call, writes "hello" on
	// every call after it, and appends each call's arguments to argv, so the
	// test can see both attempts and which flags each one got.
	argv := filepath.Join(t.TempDir(), "argv")
	seen := filepath.Join(t.TempDir(), "seen")
	fake := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + argv + "\n" +
		"n=$(cat " + seen + " 2>/dev/null || echo 0)\n" +
		"echo $((n+1)) > " + seen + "\n" +
		"[ \"$n\" -eq 0 ] && exit 92\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then printf hello > \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	const helloSum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	out := filepath.Join(t.TempDir(), "f")
	cmd := exec.Command("sh", "base/fetch.sh", "https://example.com/x", helloSum, out)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch.sh failed after a first curl that exits 92: %v\n%s", err, b)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "hello" {
		t.Errorf("the fallback download was not kept: %q, %v", data, err)
	}
	calls, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	attempts := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(attempts) != 2 {
		t.Fatalf("curl ran %d times, want the retried attempt and one HTTP/1.1 fallback:\n%s", len(attempts), calls)
	}
	for i, attempt := range attempts {
		for _, flag := range []string{"--retry 5", "--retry-all-errors", "--retry-delay 2"} {
			if !strings.Contains(attempt, flag) {
				t.Errorf("attempt %d lacks %s: %s", i+1, flag, attempt)
			}
		}
	}
	if strings.Contains(attempts[0], "--http1.1") || !strings.Contains(attempts[1], "--http1.1") {
		t.Errorf("the HTTP/1.1 attempt is not the second one:\n%s", calls)
	}
}

// TestDeepSeekHarnessBakesNoCredential: the DeepSeek Harness is not bundled
// (design base-image.md section 6, updated 2026-10-12), and neither its row
// nor a future install of it may bake a DeepSeek API key, token or auth file
// into the image (design base-image.md section 9: no secret in any layer).
// The three files this compared when dsh was a candidate for the Node
// harness stage are one: the base carries no non-Claude harness and no
// harness package files (image setup skill IS4).
func TestDeepSeekHarnessBakesNoCredential(t *testing.T) {
	df := baseDockerfile(t)
	tsv, err := os.ReadFile("base/tools.tsv")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"DEEPSEEK_API_KEY", "DEEPSEEK_TOKEN", "deepseek.com/anthropic", ".dsh/auth", ".dsh/credentials"} {
		for name, content := range map[string]string{"Dockerfile": df, "tools.tsv": string(tsv)} {
			if strings.Contains(content, needle) {
				t.Errorf("%s contains %q, which looks like a baked DeepSeek credential or endpoint", name, needle)
			}
		}
	}
}

// graphifyArgs pulls GRAPHIFY_VERSION and GRAPHIFY_PYTHON_VERSION straight
// out of the Dockerfile's own ARG lines, not a copy, so a drift between the
// pin and this test's expectation would have to be a real Dockerfile edit.
func graphifyArgs(t *testing.T, df string) (version, pythonVersion string) {
	t.Helper()
	for _, m := range regexp.MustCompile(`(?m)^ARG (GRAPHIFY_VERSION|GRAPHIFY_PYTHON_VERSION)=(\S+)$`).FindAllStringSubmatch(df, -1) {
		if m[1] == "GRAPHIFY_VERSION" {
			version = m[2]
		} else {
			pythonVersion = m[2]
		}
	}
	if version == "" || pythonVersion == "" {
		t.Fatal("the Dockerfile lacks ARG GRAPHIFY_VERSION or ARG GRAPHIFY_PYTHON_VERSION")
	}
	return version, pythonVersion
}

// TestGraphifyPinnedAndIsolated: graphify (design base-image.md section 6,
// 2026-10-13 update) is pinned by an ARG like every other tool, and its
// private Python venv never reaches the image's PATH or a project's build --
// only a two-line wrapper in /usr/local/bin does, the same pattern as the
// Node harnesses. Mutations this catches: dropping the ARG pin (fails on the
// missing match); adding "/opt/fugaro/graphify" or "/opt/fugaro/uv" to the
// ENV PATH line (fails the PATH check); changing the wrapper to exec a bare
// "python" instead of the venv's own "graphify" entry point (fails the
// wrapper check); dropping --require-hashes from the pip install (fails the
// reproducibility check); dropping --compile-bytecode (fails the
// pre-compiled-bytecode check: the venv is root-owned, so the unprivileged
// fugaro user can't write a .pyc at run time, and every invocation would
// silently recompile numpy/networkx from source).
func TestGraphifyPinnedAndIsolated(t *testing.T) {
	df := baseDockerfile(t)
	version, pythonVersion := graphifyArgs(t, df)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(version) {
		t.Errorf("GRAPHIFY_VERSION=%q is not an exact version", version)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(pythonVersion) {
		t.Errorf("GRAPHIFY_PYTHON_VERSION=%q is not an exact version", pythonVersion)
	}
	pathLine := regexp.MustCompile(`(?m)^\s*PATH=\S+`).FindString(df)
	if pathLine == "" {
		t.Fatal("no ENV PATH line in the Dockerfile")
	}
	for _, bad := range []string{"/opt/fugaro/graphify", "/opt/fugaro/uv"} {
		if strings.Contains(pathLine, bad) {
			t.Errorf("the image's PATH includes %s: graphify's private runtime must never be project-visible", bad)
		}
	}
	if !strings.Contains(df, `exec /opt/fugaro/graphify/venv/bin/graphify "$@"`) {
		t.Error("no wrapper execing the private venv's own graphify entry point")
	}
	if strings.Contains(df, `/usr/local/bin/graphify`) && strings.Contains(df, "exec /opt/fugaro/graphify/venv/bin/python ") {
		t.Error("the graphify wrapper execs a bare python, not the venv's graphify entry point")
	}
	if !strings.Contains(df, "--require-hashes") {
		t.Error("graphify's pip install does not require hashes: a PyPI change could alter the image")
	}
	if !strings.Contains(df, "images/base/graphify/requirements.lock.txt") {
		t.Error("the Dockerfile never installs from the committed graphify lockfile")
	}
	if !strings.Contains(df, "--compile-bytecode") {
		t.Error("graphify's pip install does not pre-compile bytecode: the venv is root-owned, so fugaro can't write .pyc at run time and every invocation recompiles numpy/networkx from source")
	}
}

// TestGraphifyLockIsHashPinnedAndMatchesTheArg: the committed lockfile
// (uv pip compile --generate-hashes) pins the exact version the Dockerfile's
// ARG names, and every package in it carries at least one sha256 hash of
// its own (not just an aggregate total: an earlier version of this test
// summed every --hash across the whole file and compared it with the total
// number of requirement lines, so stripping one small package's hashes
// entirely -- networkx has exactly two -- still left the file-wide hash
// count comfortably above the file-wide requirement count, thanks to
// packages like numpy that carry dozens of per-platform wheel hashes each.
// Mutation: delete networkx's two --hash lines from requirements.lock.txt
// -> this test must fail naming networkx specifically, restored after.
func TestGraphifyLockIsHashPinnedAndMatchesTheArg(t *testing.T) {
	version, _ := graphifyArgs(t, baseDockerfile(t))
	lock, err := os.ReadFile("base/graphify/requirements.lock.txt")
	if err != nil {
		t.Fatal(err)
	}
	s := string(lock)
	m := regexp.MustCompile(`(?m)^graphifyy==(\S+) \\$`).FindStringSubmatch(s)
	if m == nil {
		t.Fatal("the lockfile has no exact graphifyy== line")
	}
	if m[1] != version {
		t.Errorf("the lockfile pins graphifyy==%s, the Dockerfile's ARG GRAPHIFY_VERSION says %s", m[1], version)
	}

	reqLineRE := regexp.MustCompile(`^([A-Za-z0-9_.-]+)==\S+( \\)?$`)
	hashLineRE := regexp.MustCompile(`^    --hash=sha256:([0-9a-f]+)\s*\\?$`)
	hashCount := map[string]int{}
	var order []string
	current := ""
	for _, line := range strings.Split(s, "\n") {
		if rm := reqLineRE.FindStringSubmatch(line); rm != nil {
			current = rm[1]
			if _, dup := hashCount[current]; dup {
				t.Fatalf("%s appears twice in the lockfile", current)
			}
			hashCount[current] = 0
			order = append(order, current)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "--hash=sha256:") {
			if current == "" {
				t.Fatalf("a --hash line with no preceding package: %q", line)
			}
			hm := hashLineRE.FindStringSubmatch(line)
			if hm == nil || len(hm[1]) != 64 {
				t.Fatalf("%s has a malformed hash line (want 64 hex digits): %q", current, line)
			}
			hashCount[current]++
			continue
		}
		current = "" // a blank line, a "# via ..." comment, or anything else ends this package's block
	}
	if len(order) == 0 {
		t.Fatal("the lockfile has no pinned requirement lines")
	}
	for _, name := range order {
		if hashCount[name] == 0 {
			t.Errorf("%s has no --hash=sha256: entries of its own: a PyPI change to just that package would go unverified", name)
		}
	}

	in, err := os.ReadFile("base/graphify/requirements.in")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^graphifyy==` + regexp.QuoteMeta(version) + `$`).Match(in) {
		t.Errorf("requirements.in does not pin graphifyy==%s", version)
	}
}

// graphifyWrapperScript extracts the exact bytes the Dockerfile's printf
// line writes to /usr/local/bin/graphify: it undoes the Dockerfile shell
// line's '"'"' idiom (close-quote, a literal quote inside double quotes,
// reopen-quote -- how a single-quoted printf format string embeds an
// apostrophe) and expands the literal \n escapes printf itself expands into
// real newlines, so the test asserts against what actually lands in the
// image, not a hand-copied approximation of it that could drift.
func graphifyWrapperScript(t *testing.T, df string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)printf '(#!/bin/sh\\n# graphify.*?)' \\\n\s*> /usr/local/bin/graphify;`).FindStringSubmatch(df)
	if m == nil {
		t.Fatal(`no printf ... > /usr/local/bin/graphify line in the Dockerfile`)
	}
	s := strings.ReplaceAll(m[1], `'"'"'`, "'")
	return strings.ReplaceAll(s, `\n`, "\n")
}

// TestGraphifyWrapperIsolatesPythonEnv: a project's (or mise's) PYTHONPATH,
// PYTHONHOME, PYTHONSTARTUP or PYTHONUSERBASE would otherwise reach
// graphify's private venv and could shadow its own numpy/tree-sitter with a
// project copy -- confirmed directly: with the real venv from this
// lockfile, `PYTHONPATH=<dir with a numpy.py> python3 -c "import numpy"`
// loads the shadow instead of the venv's own package. Mutation: delete the
// "unset PYTHONPATH ..." line from the Dockerfile's wrapper printf -> this
// test must fail, restored after.
func TestGraphifyWrapperIsolatesPythonEnv(t *testing.T) {
	script := graphifyWrapperScript(t, baseDockerfile(t))
	for _, want := range []string{
		"unset PYTHONPATH PYTHONHOME PYTHONSTARTUP PYTHONUSERBASE",
		"export PYTHONNOUSERSITE=1",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the graphify wrapper lacks %q: a caller's Python env variables could shadow the private venv's own packages", want)
		}
	}
	unsetIdx := strings.Index(script, "unset PYTHONPATH")
	execIdx := strings.Index(script, "exec /opt/fugaro/graphify/venv/bin/graphify")
	if unsetIdx < 0 || execIdx < 0 || unsetIdx > execIdx {
		t.Error("the wrapper's PYTHONPATH/PYTHONHOME unset must run before the exec, not after")
	}
}

// TestGraphifyWrapperBodyIsExact: the substring checks above (no bare
// "python" exec, the isolation lines present) all still pass if the wrapper
// also does something unexpected elsewhere, such as widening PATH before
// the exec -- M3a: adding "export PATH=/opt/fugaro/graphify/venv/bin:$PATH"
// right before the exec line still contains every substring those tests
// look for. Pin the wrapper's entire body line by line instead: the
// shebang, the two comment lines, the two isolation lines and exactly one
// exec line with no other statement (a PATH assignment included) anywhere
// in it. Mutation: add that PATH-exporting line to the Dockerfile's printf
// -> this test must fail, restored after.
func TestGraphifyWrapperBodyIsExact(t *testing.T) {
	want := `#!/bin/sh
# graphify on its own private Python venv, which is not on PATH (design base-image.md section 6).
# A caller's PYTHONPATH/PYTHONHOME/PYTHONSTARTUP/PYTHONUSERBASE could reach into this private venv and shadow its own numpy/tree-sitter.
unset PYTHONPATH PYTHONHOME PYTHONSTARTUP PYTHONUSERBASE
export PYTHONNOUSERSITE=1
exec /opt/fugaro/graphify/venv/bin/graphify "$@"
`
	got := graphifyWrapperScript(t, baseDockerfile(t))
	if got != want {
		t.Errorf("the graphify wrapper's body is:\n%s\nwant exactly:\n%s", got, want)
	}
}

// TestGraphifyBuildTimeVersionCheck: nothing else in this file proves the
// Dockerfile actually runs graphify --version at build time and compares it
// against the pinned ARG GRAPHIFY_VERSION, failing the build on a mismatch
// -- TestGraphifyPinnedAndIsolated checks the ARG, the lockfile and the
// wrapper, but not this check, so deleting it entirely left every other
// graphify test passing. Mutation: delete the "v=$(... graphify --version);
// ... case "$v" in ..." lines from the Dockerfile -> this test must fail,
// restored after.
func TestGraphifyBuildTimeVersionCheck(t *testing.T) {
	df := baseDockerfile(t)
	if !strings.Contains(df, `v=$(/opt/fugaro/graphify/venv/bin/graphify --version); echo "$v";`) {
		t.Error("the Dockerfile never runs graphify --version at build time and captures its output")
	}
	const wantCase = `case "$v" in "graphify $GRAPHIFY_VERSION") ;; *) echo "base: graphify --version reports '$v', not the pinned ${GRAPHIFY_VERSION}" >&2; exit 1 ;; esac;`
	if !strings.Contains(df, wantCase) {
		t.Errorf("the Dockerfile lacks the exact pinned-version check %q (want it to fail the build on a mismatch against ARG GRAPHIFY_VERSION)", wantCase)
	}
}

// TestGraphifyToolsRow: the presence check (design section 6) runs
// "graphify --version", tier harness, on both architectures -- real wheels
// exist for graphifyy and every one of its dependencies on linux/arm64
// (verified against PyPI's file listing when this was written), unlike a
// tool the spec allows to skip an architecture.
func TestGraphifyToolsRow(t *testing.T) {
	tools, err := image.ParseTools(images.BaseTools)
	if err != nil {
		t.Fatal(err)
	}
	var found *image.Tool
	for i := range tools {
		if tools[i].Name == "graphify" {
			found = &tools[i]
		}
	}
	if found == nil {
		t.Fatal(`tools.tsv has no "graphify" row`)
	}
	if found.Tier != "harness" {
		t.Errorf("graphify's tier is %q, want harness", found.Tier)
	}
	if found.Arches != nil {
		t.Errorf("graphify is restricted to %v, want every architecture (all)", found.Arches)
	}
	if len(found.Argv) == 0 || found.Argv[0] != "graphify" {
		t.Errorf("graphify's presence check argv is %v, want it to start with graphify", found.Argv)
	}
}
