package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// selftestEnv puts fake claude, mise and sudo (exiting sudoExit) and the
// real fugaro first on PATH, and points HOME at an empty directory.
func selftestEnv(t *testing.T, sudoExit int) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the user check fails when the tests run as root")
	}
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{
		"claude": "#!/bin/sh\necho '2.1.283 (Claude Code)'\n",
		"mise":   "#!/bin/sh\nprintf '{}\\n'\n",
		"sudo":   fmt.Sprintf("#!/bin/sh\nexit %d\n", sudoExit),
	})
	fugaro := testutil.BuildFugaro(t)
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", bin+sep+filepath.Dir(fugaro)+sep+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
}

// selftestFixture is a clone of a fresh remote and a spec that matches it.
func selftestFixture(t *testing.T) SelftestSpec {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "x\n", "build.sh": "#!/bin/sh\necho building\n"})
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	testutil.Git(t, parent, "clone", "--quiet", remote, repo)
	return SelftestSpec{
		RepoDir: repo, Commit: testutil.Git(t, repo, "rev-parse", "HEAD"), Origin: remote, Mise: true,
		Verify: verify.Settings{RepoDir: repo, Build: "sh build.sh", Test: "true"},
	}
}

func checkNamed(r Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestSelftestPasses(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	var log bytes.Buffer
	r := Selftest(context.Background(), spec, &log)
	if !r.Passed {
		t.Fatalf("report %+v\nlog:\n%s", r, log.String())
	}
	for _, name := range []string{"user", "no-sudo", "claude", "checkout", "origin", "git-credentials", "home-credentials", "verify-build"} {
		if _, ok := checkNamed(r, name); !ok {
			t.Errorf("no %s check in %+v", name, r)
		}
	}
	if !strings.Contains(log.String(), "building") {
		t.Errorf("the verify build's output is missing from the log: %q", log.String())
	}
}

func TestSelftestAcceptsAReferencedToken(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	testutil.WriteFiles(t, os.Getenv("HOME"), map[string]string{".npmrc": "//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n"})
	if r := Selftest(context.Background(), spec, &bytes.Buffer{}); !r.Passed {
		t.Fatalf("an environment reference counted as a credential: %+v", r)
	}
}

// TestSelftestAcceptsANonCredentialInsteadOf mirrors finalize-checkout's
// TestFinalizeCheckoutAcceptsGlobalNonCredentialInsteadOf: a plain mirror
// rewrite carries no credential, so an image finalize accepts must also pass
// the selftest.
func TestSelftestAcceptsANonCredentialInsteadOf(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	testutil.Git(t, spec.RepoDir, "config", "url.https://mirror.example.invalid/.insteadOf", "https://example.invalid/")
	testutil.Git(t, spec.RepoDir, "config", "url.https://mirror.example.invalid/.pushInsteadOf", "https://example.invalid/")
	if r := Selftest(context.Background(), spec, &bytes.Buffer{}); !r.Passed {
		t.Fatalf("report %+v", r)
	}
}

func TestSelftestFailures(t *testing.T) {
	cases := []struct {
		name, check string
		sudoExit    int
		mutate      func(t *testing.T, spec *SelftestSpec)
	}{
		{"wrong commit", "checkout", 1, func(t *testing.T, s *SelftestSpec) { s.Commit = strings.Repeat("0", 40) }},
		{"passwordless sudo", "no-sudo", 0, nil},
		{"credential file in HOME", "home-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.WriteFiles(t, os.Getenv("HOME"), map[string]string{".git-credentials": "https://u:s3cr3t@example.invalid\n"})
		}},
		{"literal registry token in HOME", "home-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.WriteFiles(t, os.Getenv("HOME"), map[string]string{".npmrc": "//registry.npmjs.org/:_authToken=npm_s3cr3t\n"})
		}},
		{"credential helper in the checkout", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "config", "credential.helper", "store")
		}},
		{"token in an insteadOf rule", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "config", "url.https://x-access-token:s3cr3t@github.com/.insteadOf", "https://github.com/")
		}},
		{"unscoped extraheader", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "config", "http.extraheader", "AUTHORIZATION: basic s3cr3t")
		}},
		{"token in a pushInsteadOf rule", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "config", "url.https://x-access-token:s3cr3t@github.com/.pushInsteadOf", "https://github.com/")
		}},
		{"token in the origin pushurl", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "config", "remote.origin.pushurl", "https://x-access-token:s3cr3t@github.com/acme/app.git")
		}},
		{"token in a second remote", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "remote", "add", "upstream", "https://s3cr3t@github.com/acme/upstream.git")
		}},
		{"credential helper in the global config", "git-credentials", 1, func(t *testing.T, s *SelftestSpec) {
			global := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(global, []byte("[credential]\n\thelper = store\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", global)
		}},
		{"token in the origin URL", "origin", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "remote", "set-url", "origin", "https://x-access-token:s3cr3t@github.com/acme/app.git")
		}},
		{"SSH origin", "origin", 1, func(t *testing.T, s *SelftestSpec) {
			testutil.Git(t, s.RepoDir, "remote", "set-url", "origin", "git@bitbucket.org:team/repo.git")
		}},
		{"different origin", "origin", 1, func(t *testing.T, s *SelftestSpec) { s.Origin = "https://github.com/acme/other.git" }},
		{"failing build", "verify-build", 1, func(t *testing.T, s *SelftestSpec) { s.Verify.Build = "exit 3" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selftestEnv(t, tc.sudoExit)
			spec := selftestFixture(t)
			if tc.mutate != nil {
				tc.mutate(t, &spec)
			}
			r := Selftest(context.Background(), spec, &bytes.Buffer{})
			c, ok := checkNamed(r, tc.check)
			if r.Passed || !ok || c.OK {
				t.Fatalf("want check %s to fail, got %+v", tc.check, r)
			}
			for _, c := range r.Checks {
				if strings.Contains(c.Detail, "s3cr3t") {
					t.Errorf("check %s leaks the token: %q", c.Name, c.Detail)
				}
			}
		})
	}
}

// --- Hardening checks: setuid-root binaries and /etc/sudoers.d/sudoers
// content. These are gated behind spec.CheckHardening because they inspect
// real filesystem paths (/, /etc/sudoers.d, /etc/sudoers) that only make
// sense inside the finished container; TestSelftestPasses above runs on the
// host and leaves CheckHardening unset, so it never scans the host's own
// root filesystem. The underlying checkSetuid/checkSudoers functions take an
// explicit root so they can be unit-tested here without Docker.

func addRecorder() (func(string, bool, string, ...any), *Report) {
	r := &Report{}
	return func(name string, ok bool, format string, args ...any) {
		r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}, r
}

func mkSetuid(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// os.Chmod takes Go's portable os.FileMode bits, not a raw Unix octal
	// literal: os.ModeSetuid (not the numeral 04000) is what sets the bit.
	if err := os.Chmod(path, os.ModeSetuid|0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSetuidAllowsTheExpectedSet(t *testing.T) {
	root := t.TempDir()
	mkSetuid(t, filepath.Join(root, "usr/bin/passwd"))
	mkSetuid(t, filepath.Join(root, "usr/bin/mount"))
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setuid")
	if !ok || !c.OK {
		t.Fatalf("want no-setuid to pass with only the expected binaries, got %+v", r.Checks)
	}
}

func TestCheckSetuidFlagsAnUnexpectedBinary(t *testing.T) {
	root := t.TempDir()
	mkSetuid(t, filepath.Join(root, "usr/bin/passwd"))
	mkSetuid(t, filepath.Join(root, "opt/evil"))
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setuid")
	if !ok || c.OK {
		t.Fatalf("want no-setuid to fail on an unexpected setuid binary, got %+v", r.Checks)
	}
	if !strings.Contains(c.Detail, "evil") || strings.Contains(c.Detail, "usr/bin/passwd") {
		t.Errorf("detail should name the unexpected binary only, not the expected one: %q", c.Detail)
	}
}

func TestCheckSetuidFlagsSudoIfNotStripped(t *testing.T) {
	root := t.TempDir()
	mkSetuid(t, filepath.Join(root, "usr/bin/sudo"))
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setuid")
	if !ok || c.OK {
		t.Fatalf("want no-setuid to fail when sudo's setuid bit was not stripped, got %+v", r.Checks)
	}
}

// TestCheckSetuidFlagsSuIfNotStripped: the template strips su's setuid bit
// like sudo's (a setup step's `passwd -d root` plus pam_unix's nullok would
// otherwise leave su as a passwordless way back to root), so a setuid su is
// a finding too.
func TestCheckSetuidFlagsSuIfNotStripped(t *testing.T) {
	root := t.TempDir()
	mkSetuid(t, filepath.Join(root, "usr/bin/su"))
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	if c, ok := checkNamed(*r, "no-setuid"); !ok || c.OK || !strings.Contains(c.Detail, "usr/bin/su") {
		t.Fatalf("want no-setuid to fail on a setuid su, got %+v", r.Checks)
	}
}

// TestFilesystemChecksFailOnFindErrors: a directory the scan can't read
// could hide a setuid binary or a capability, so find's errors fail the
// checks instead of shrinking the result, and so does a cancelled context.
func TestFilesystemChecksFailOnFindErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	fakeGetcap(t)
	root := t.TempDir()
	hidden := filepath.Join(root, "opt/hidden")
	mkSetuid(t, filepath.Join(hidden, "evil"))
	if err := os.Chmod(hidden, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hidden, 0o755) })
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	checkSetgid(context.Background(), root, add)
	checkFileCaps(context.Background(), root, add)
	for _, name := range []string{"no-setuid", "no-setgid", "no-file-caps"} {
		if c, ok := checkNamed(*r, name); !ok || c.OK {
			t.Errorf("want %s to fail when find can't read a directory, got %+v", name, c)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	add, r = addRecorder()
	clean := t.TempDir()
	checkSetuid(ctx, clean, add)
	checkSetgid(ctx, clean, add)
	checkFileCaps(ctx, clean, add)
	for _, name := range []string{"no-setuid", "no-setgid", "no-file-caps"} {
		if c, ok := checkNamed(*r, name); !ok || c.OK {
			t.Errorf("want %s to fail with a cancelled context, got %+v", name, c)
		}
	}
}

// TestSelftestRootChecksOnly: a RootChecks spec runs only the filesystem
// scans, and refuses to run them as anyone but root, where an unreadable
// directory would hide files.
func TestSelftestRootChecksOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the non-root refusal needs a non-root test run")
	}
	r := Selftest(context.Background(), SelftestSpec{RootChecks: true}, &bytes.Buffer{})
	if r.Passed || len(r.Checks) != 1 || r.Checks[0].Name != "root-scan" || r.Checks[0].OK {
		t.Fatalf("report %+v, want a single failing root-scan check", r)
	}
}

func TestCheckSetuidPassesWithNone(t *testing.T) {
	root := t.TempDir()
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setuid")
	if !ok || !c.OK {
		t.Fatalf("want no-setuid to pass with no setuid binaries at all, got %+v", r.Checks)
	}
}

func mkSetgid(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, os.ModeSetgid|0o755); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode()&os.ModeSetgid == 0 {
		t.Skipf("this filesystem won't set the setgid bit for this user (%v)", err)
	}
}

func TestCheckSetgidAllowsTheExpectedSet(t *testing.T) {
	root := t.TempDir()
	mkSetgid(t, filepath.Join(root, "usr/bin/chage"))
	mkSetgid(t, filepath.Join(root, "usr/sbin/unix_chkpwd"))
	add, r := addRecorder()
	checkSetgid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setgid")
	if !ok || !c.OK {
		t.Fatalf("want no-setgid to pass with only the expected binaries, got %+v", r.Checks)
	}
}

func TestCheckSetgidFlagsAnUnexpectedBinary(t *testing.T) {
	root := t.TempDir()
	mkSetgid(t, filepath.Join(root, "usr/bin/chage"))
	mkSetgid(t, filepath.Join(root, "opt/evil"))
	add, r := addRecorder()
	checkSetgid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setgid")
	if !ok || c.OK || !strings.Contains(c.Detail, "opt/evil") || strings.Contains(c.Detail, "chage") {
		t.Fatalf("want no-setgid to fail naming only opt/evil, got %+v", r.Checks)
	}
}

// fakeGetcap puts a getcap on PATH that reports cap_net_raw=ep for any file
// named "capped", the way libcap's getcap prints "<path> <caps>" for each
// file that has capabilities and nothing for the rest.
func fakeGetcap(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{
		"getcap": "#!/bin/sh\nfor f in \"$@\"; do case \"$f\" in */capped) echo \"$f cap_net_raw=ep\" ;; esac; done\n",
	})
	if err := os.Chmod(filepath.Join(bin, "getcap"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCheckFileCapsPassesWithNone(t *testing.T) {
	fakeGetcap(t)
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"usr/bin/plain": "x"})
	add, r := addRecorder()
	checkFileCaps(context.Background(), root, add)
	if c, ok := checkNamed(*r, "no-file-caps"); !ok || !c.OK {
		t.Fatalf("want no-file-caps to pass, got %+v", r.Checks)
	}
}

func TestCheckFileCapsFlagsACapability(t *testing.T) {
	fakeGetcap(t)
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"usr/bin/plain": "x", "usr/local/bin/capped": "x"})
	add, r := addRecorder()
	checkFileCaps(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-file-caps")
	if !ok || c.OK || !strings.Contains(c.Detail, "usr/local/bin/capped") || strings.Contains(c.Detail, "plain") {
		t.Fatalf("want no-file-caps to fail naming only usr/local/bin/capped, got %+v", r.Checks)
	}
}

// TestCheckFileCapsFailsWithoutGetcap: the check fails closed rather than
// passing unchecked when the image has no getcap (the base ships
// libcap2-bin for it).
func TestCheckFileCapsFailsWithoutGetcap(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	add, r := addRecorder()
	checkFileCaps(context.Background(), t.TempDir(), add)
	c, ok := checkNamed(*r, "no-file-caps")
	if !ok || c.OK || !strings.Contains(c.Detail, "getcap") {
		t.Fatalf("want no-file-caps to fail naming getcap, got %+v", r.Checks)
	}
}

func TestCheckSudoersPasses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sudoers.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("readme\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "sudoers")
	if err := os.WriteFile(file, []byte("root ALL=(ALL:ALL) ALL\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	add, r := addRecorder()
	checkSudoers(dir, file, add)
	c, ok := checkNamed(*r, "sudoers")
	if !ok || !c.OK {
		t.Fatalf("want sudoers to pass, got %+v (%s)", r.Checks, c.Detail)
	}
}

func TestCheckSudoersFlagsExtraDropIn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sudoers.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir, map[string]string{"README": "readme\n", "fugaro-build": "fugaro ALL=(root) NOPASSWD: ALL\n"})
	file := filepath.Join(root, "sudoers")
	if err := os.WriteFile(file, []byte("root ALL=(ALL:ALL) ALL\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	add, r := addRecorder()
	checkSudoers(dir, file, add)
	c, ok := checkNamed(*r, "sudoers")
	if !ok || c.OK {
		t.Fatalf("want sudoers to fail on an extra sudoers.d entry, got %+v", r.Checks)
	}
	if strings.Contains(c.Detail, "NOPASSWD") {
		t.Errorf("detail leaks a sudoers rule: %q", c.Detail)
	}
}

func TestCheckSudoersFlagsAFugaroGrant(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sudoers.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("readme\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "sudoers")
	if err := os.WriteFile(file, []byte("root ALL=(ALL:ALL) ALL\nfugaro ALL=(root) NOPASSWD: ALL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add, r := addRecorder()
	checkSudoers(dir, file, add)
	c, ok := checkNamed(*r, "sudoers")
	if !ok || c.OK {
		t.Fatalf("want sudoers to fail when /etc/sudoers grants fugaro something, got %+v", r.Checks)
	}
	if strings.Contains(c.Detail, "NOPASSWD") {
		t.Errorf("detail leaks a sudoers rule: %q", c.Detail)
	}
}

func TestCheckSudoersUnreadableFileIsFine(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sudoers.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("readme\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	// A root-only 0440 file (the real /etc/sudoers mode) is unreadable to the
	// fugaro user; that itself is fine, since it means fugaro has no special
	// grant to read it. On a test run as root this file IS readable, so skip.
	if os.Geteuid() == 0 {
		t.Skip("unreadable-file semantics don't apply when running as root")
	}
	file := filepath.Join(root, "sudoers")
	if err := os.WriteFile(file, []byte("root ALL=(ALL:ALL) ALL\nfugaro ALL=NOPASSWD:ALL\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	add, r := addRecorder()
	checkSudoers(dir, file, add)
	c, ok := checkNamed(*r, "sudoers")
	if !ok || !c.OK {
		t.Fatalf("want sudoers to pass when the file is unreadable, got %+v", r.Checks)
	}
}

// TestSpecForCloud: the Cloud Build smoke checks the image's structure,
// its init and hardening and the baked commit, and never runs the
// workflow's build, which would run repository code with its secrets.
func TestSpecForCloud(t *testing.T) {
	const yaml = `version: 1
project: aurora
git: { provider: github }
workflows:
  web: { commands: { build: sh build.sh, test: sh test.sh } }
`
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	spec, err := SpecForCloud(cfg, "web", "abc123", "https://github.com/acme/webapp.git")
	if err != nil {
		t.Fatal(err)
	}
	want := SelftestSpec{Base: config.BaseKind, RepoDir: "/work/repo", Commit: "abc123", Origin: "https://github.com/acme/webapp.git",
		CheckInit: true, CheckHardening: true, SkipVerify: true, Tools: "critical", Mise: true, CheckMiseCache: true}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("SpecForCloud = %+v\nwant %+v", spec, want)
	}
	if _, err := SpecForCloud(cfg, "other", "abc123", "https://github.com/acme/webapp.git"); err == nil {
		t.Error("an unknown workflow gave a spec")
	}
}

// TestSelftestSkipVerify: with SkipVerify the checkout is still checked,
// but the workflow's build doesn't run.
func TestSelftestSkipVerify(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	spec.Verify.Build = "exit 3"
	spec.SkipVerify = true
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	if !r.Passed {
		t.Fatalf("report %+v", r)
	}
	if _, ok := checkNamed(r, "verify-build"); ok {
		t.Errorf("verify-build ran: %+v", r)
	}
	if c, ok := checkNamed(r, "checkout"); !ok || !c.OK {
		t.Errorf("no passing checkout check: %+v", r)
	}
}

// --- The managed settings directory (/etc/claude-code).

// ownerIDs are the uid and gid that own a directory the test just made.
func ownerIDs(t *testing.T, dir string) (uint32, uint32) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	return st.Uid, st.Gid
}

func managedDirResult(t *testing.T, dir string, uid, gid uint32) Check {
	t.Helper()
	add, r := addRecorder()
	checkManagedSettingsDir(dir, uid, gid, add)
	c, ok := checkNamed(*r, "managed-settings-dir")
	if !ok {
		t.Fatalf("no managed-settings-dir check in %+v", r.Checks)
	}
	return c
}

func TestSelftestManagedSettingsDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "claude-code")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	uid, gid := ownerIDs(t, dir)
	if c := managedDirResult(t, dir, uid, gid); !c.OK {
		t.Fatalf("an empty directory the agent owns: %+v", c)
	}
	if c := managedDirResult(t, filepath.Join(root, "missing"), uid, gid); c.OK {
		t.Fatalf("a missing directory passed: %+v", c)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if c := managedDirResult(t, link, uid, gid); c.OK || !strings.Contains(c.Detail, "symbolic link") {
		t.Fatalf("a symlink passed: %+v", c)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if c := managedDirResult(t, file, uid, gid); c.OK {
		t.Fatalf("a file passed: %+v", c)
	}
	// Not the owner, so the group bits decide.
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if c := managedDirResult(t, dir, uid+1, gid); !c.OK {
		t.Fatalf("a group-writable directory for the agent's group: %+v", c)
	}
	if c := managedDirResult(t, dir, uid+1, gid+1); c.OK {
		t.Fatalf("a group-writable directory for another group passed: %+v", c)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if c := managedDirResult(t, dir, uid, gid); c.OK {
		t.Fatalf("a read-only directory passed: %+v", c)
	}
}

// Part of the selftest runs as root, for whom any directory is writable, so
// the check reads the owner and mode bits against uid 1000 instead.
func TestSelftestManagedSettingsDirUID1000(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-code")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	uid, gid := ownerIDs(t, dir)
	// A directory owned by someone else (root, in the image) with mode 0755
	// is not writable by the agent, whoever runs the check.
	if c := managedDirResult(t, dir, uid+1000, gid+1000); c.OK || !strings.Contains(c.Detail, "not writable") {
		t.Fatalf("a directory owned by another user passed: %+v", c)
	}
}

func TestSelftestManagedSettingsDirExtraEntries(t *testing.T) {
	for name, entry := range map[string]string{
		"managed-mcp.json":      "{}",
		"managed-settings.json": `{"env":{}}`,
		"managed-settings.d":    "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "claude-code")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if entry == "" {
				if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(dir, name), []byte(entry), 0o644); err != nil {
				t.Fatal(err)
			}
			uid, gid := ownerIDs(t, dir)
			if c := managedDirResult(t, dir, uid, gid); c.OK || !strings.Contains(c.Detail, name) {
				t.Fatalf("an entry %s passed: %+v", name, c)
			}
		})
	}
}

// --- The mise download cache (~/.cache/mise).

// miseCacheResult is checkMiseCacheDir's one check, run against dir as if
// dir were the mise cache of the user uid:gid.
func miseCacheResult(t *testing.T, dir string, uid, gid uint32) Check {
	t.Helper()
	add, r := addRecorder()
	checkMiseCacheDir(dir, uid, gid, add)
	c, ok := checkNamed(*r, "mise-cache-dir")
	if !ok {
		t.Fatalf("no mise-cache-dir check in %+v", r.Checks)
	}
	return c
}

func TestCheckMiseCacheDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cache", "mise")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	uid, gid := ownerIDs(t, dir)
	if c := miseCacheResult(t, dir, uid, gid); !c.OK {
		t.Fatalf("a directory owned and writable by the agent: %+v", c)
	}
	// The check runs as the directory's owner here, so it proved writability
	// with a probe file, and removed it: the directory is as it was.
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the check left %v in the directory (%v), want it as it was", entries, err)
	}
	// A missing directory: the derived build's cache mount would create it
	// as root.
	missing := filepath.Join(root, "missing")
	if c := miseCacheResult(t, missing, uid, gid); c.OK || !strings.Contains(c.Detail, missing) {
		t.Fatalf("a missing directory passed: %+v", c)
	}
	// Root-owned with mode 0755 is exactly what BuildKit's mount target
	// leaves behind: a directory the run's mise cannot write in. The detail
	// names the owner it found.
	if c := miseCacheResult(t, dir, uid+1000, gid+1000); c.OK || !strings.Contains(c.Detail, fmt.Sprintf("owned by uid %d", uid)) {
		t.Fatalf("a root-owned directory passed: %+v", c)
	}
	// A read-only directory: the probe file cannot be created in it. Root
	// ignores the mode bits and every directory is writable to it, so the
	// case only means something to a non-root run (the check would then take
	// its bit-reading path only for a directory it does not own).
	if os.Geteuid() != 0 {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		c := miseCacheResult(t, dir, uid, gid)
		// The detail has to name the path's owner and the probe that failed:
		// reading the mode bits alone would report neither, so this also
		// pins that the check writes a probe file when it runs as the owner.
		if c.OK || !strings.Contains(c.Detail, "not writable") || !strings.Contains(c.Detail, fmt.Sprintf("owner %d:%d", uid, gid)) ||
			!strings.Contains(c.Detail, "creating and removing a file in it failed") {
			t.Fatalf("a read-only directory passed: %+v", c)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if c := miseCacheResult(t, link, uid, gid); c.OK || !strings.Contains(c.Detail, "symbolic link") {
		t.Fatalf("a symlink passed: %+v", c)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if c := miseCacheResult(t, file, uid, gid); c.OK {
		t.Fatalf("a file passed: %+v", c)
	}
}

// TestCheckMiseCacheDirProbeFails: the read-only case, root or not. The
// block above can only chmod its way to it as a non-root run (root ignores
// the mode bits, so its probe file is created in a 0555 directory and the
// check rightly passes), which would leave the failing half of the probe
// path — and the detail it reports — uncovered in a root container. The
// probe is a var, so the failure is injected instead: the check is the
// directory's owner here (euid is uid), so it takes the probe path and must
// report the probe's own error, not the mode bits.
func TestCheckMiseCacheDirProbeFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mise")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	uid, gid := ownerIDs(t, dir)
	prev := miseCacheProbe
	miseCacheProbe = func(string) error { return errors.New("read-only file system") }
	t.Cleanup(func() { miseCacheProbe = prev })
	c := miseCacheResult(t, dir, uid, gid)
	if c.OK || !strings.Contains(c.Detail, "not writable") ||
		!strings.Contains(c.Detail, fmt.Sprintf("owner %d:%d", uid, gid)) ||
		!strings.Contains(c.Detail, "creating and removing a file in it failed") ||
		!strings.Contains(c.Detail, "read-only file system") {
		t.Fatalf("a directory whose probe failed passed: %+v", c)
	}
}

// TestCheckMiseCacheDirSymlinkedParent: only the last path component is
// judged for being a symlink. A symlinked parent such as ~/.cache is
// followed and passes, which is harmless in the image (a real chain, and
// moving ~/.cache only moves the cache), while the mise directory itself
// being a symlink is refused (the block above).
func TestCheckMiseCacheDirSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-cache")
	dir := filepath.Join(real, "mise")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, ".cache")
	if err := os.Symlink(real, parent); err != nil {
		t.Fatal(err)
	}
	uid, gid := ownerIDs(t, dir)
	if c := miseCacheResult(t, filepath.Join(parent, "mise"), uid, gid); !c.OK {
		t.Fatalf("a real mise directory under a symlinked .cache: %+v", c)
	}
}

// TestDirWritableByBitsOnly: the branch checkMiseCacheDir takes for a
// directory it does not own — root in the second scan container, which may
// write anywhere whatever the bits say. The owner, group and mode bits have
// to decide, so a directory owned by uid 1000 is judged for that user even
// though this process is not it.
func TestDirWritableByBitsOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		mode     os.FileMode
		uid, gid uint32
		want     bool
	}{
		{"the owner has rwx", 0o700, 1000, 1000, true},
		{"the owner lacks the search bit", 0o600, 1000, 1000, false},
		{"the owner lacks the write bit", 0o500, 1000, 1000, false},
		{"the group has rwx", 0o070, 2000, 1000, true},
		{"the group lacks the search bit", 0o060, 2000, 1000, false},
		{"the other bits count for a stranger", 0o007, 2000, 2000, true},
		{"a stranger with nothing", 0o700, 2000, 2000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := dirWritableBy(fi, 1000, 1000, tc.uid, tc.gid); got != tc.want {
				t.Fatalf("dirWritableBy(mode %04o, uid %d, gid %d) = %v, want %v", tc.mode, tc.uid, tc.gid, got, tc.want)
			}
		})
	}
}

// TestSelftestToolsOnlyMiseCacheCheck: the base image's smoke asks for the
// presence checks and the mise cache check together, and the spec without
// the flag leaves it out (TestSelftestToolsCriticalOnly pins that side).
func TestSelftestToolsOnlyMiseCacheCheck(t *testing.T) {
	presenceTable(t)
	r := Selftest(context.Background(), SelftestSpec{ToolsOnly: true, Tools: "critical", CheckMiseCache: true}, &bytes.Buffer{})
	if _, ok := checkNamed(r, "mise-cache-dir"); !ok {
		t.Fatalf("no mise-cache-dir check in %+v", r.Checks)
	}
}

// TestSelftestMiseCacheCheck: the whole Selftest path — the one the derived
// image's local and cloud smokes drive — runs the mise cache check when the
// spec asks for it, against the image's own path. The check's outcome here
// depends on the host's /home/fugaro/.cache/mise, so only its presence and
// the path it inspected are asserted; the derived smoke's docker test
// (internal/image/docker_test.go) pins that it passes in the image. Without
// this, dropping the call from Selftest's main path would leave every
// derived image unchecked with every non-docker test green.
func TestSelftestMiseCacheCheck(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	spec.CheckMiseCache = true
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	c, ok := checkNamed(r, "mise-cache-dir")
	if !ok {
		t.Fatalf("no mise-cache-dir check in %+v", r.Checks)
	}
	if !strings.Contains(c.Detail, agentMiseCacheDir) {
		t.Errorf("mise-cache-dir = %+v, want it to inspect %s", c, agentMiseCacheDir)
	}
}

// fakeMise puts a mise on PATH that prints out for `ls --current --missing --json`.
func fakeMise(t *testing.T, out string) {
	t.Helper()
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{"mise": "#!/bin/sh\n[ \"$MISE_OFFLINE\" = 1 ] || { echo 'not offline' >&2; exit 2; }\nprintf '%s\\n' '" + out + "'\n"})
	if err := os.Chmod(filepath.Join(bin, "mise"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSelftestMiseToolsMissing(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	spec.Mise = true
	fakeMise(t, `{"node":[{"version":"24.19.0","requested_version":"24","install_path":"/x"}]}`)
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	c, ok := checkNamed(r, "mise-tools")
	if !ok || c.OK || !strings.Contains(c.Detail, "node@24.19.0") || r.Passed {
		t.Fatalf("mise-tools = %+v (passed %v)", c, r.Passed)
	}
	fakeMise(t, `{}`)
	r = Selftest(context.Background(), spec, &bytes.Buffer{})
	if c, ok := checkNamed(r, "mise-tools"); !ok || !c.OK {
		t.Fatalf("nothing missing: %+v", c)
	}
}

// TestSelftestMiseFailureDetailIsPrintable: checkMise's failure detail
// interpolates mise's own stderr, which this image never controls (a bad
// mise config, a corrupt cache); it must go through pluginwire.Printable
// like every other untrusted string in a check's message, so an ANSI
// escape or control character in it cannot corrupt the terminal or the
// --json output's framing.
func TestSelftestMiseFailureDetailIsPrintable(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	spec.Mise = true
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{"mise": "#!/bin/sh\nprintf '\\033[31mmise: boom\\033[0m' >&2\nexit 1\n"})
	if err := os.Chmod(filepath.Join(bin, "mise"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	c, ok := checkNamed(r, "mise-tools")
	if !ok || c.OK {
		t.Fatalf("mise-tools = %+v", c)
	}
	if strings.ContainsRune(c.Detail, '\x1b') {
		t.Fatalf("mise-tools detail carries a raw escape byte: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, `\u001b`) || !strings.Contains(c.Detail, "mise: boom") {
		t.Fatalf("mise-tools detail does not show the escaped control character: %q", c.Detail)
	}
}

// TestSelftestMiseMissingToolNameIsPrintable: checkMise's "not installed"
// detail interpolates the tool names and versions mise reports missing,
// which come from the checkout's own mise config (and so, like mise's
// stderr above, are never under this image's control); it must go through
// pluginwire.Printable the same way.
func TestSelftestMiseMissingToolNameIsPrintable(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	spec.Mise = true
	fakeMise(t, `{"node":[{"version":"1\u001b[31mboom\u001b[0m","requested_version":"1","install_path":"/x"}]}`)
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	c, ok := checkNamed(r, "mise-tools")
	if !ok || c.OK {
		t.Fatalf("mise-tools = %+v", c)
	}
	if strings.ContainsRune(c.Detail, '\x1b') {
		t.Fatalf("mise-tools detail carries a raw escape byte: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, `\u001b`) || !strings.Contains(c.Detail, "node@1") || !strings.Contains(c.Detail, "boom") {
		t.Fatalf("mise-tools detail does not show the escaped control character: %q", c.Detail)
	}
}

func TestSelftestRefusesHarnessCredentialFiles(t *testing.T) {
	for _, rel := range []string{".codex/auth.json", ".gemini/oauth_creds.json", ".local/share/opencode/auth.json",
		".config/gcloud/credentials.db", ".config/gcloud/application_default_credentials.json", ".claude/.credentials.json"} {
		t.Run(rel, func(t *testing.T) {
			selftestEnv(t, 1)
			spec := selftestFixture(t)
			testutil.WriteFiles(t, os.Getenv("HOME"), map[string]string{rel: "x"})
			r := Selftest(context.Background(), spec, &bytes.Buffer{})
			if c, _ := checkNamed(r, "home-credentials"); c.OK || !strings.Contains(c.Detail, rel) {
				t.Fatalf("home-credentials = %+v", c)
			}
		})
	}
}

func TestSpecForCloudOnTheBase(t *testing.T) {
	cfg, problems := config.Parse([]byte("version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make test } }\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	base, _ := SpecForCloud(cfg, "app", "c0ffee", "https://github.com/acme/app.git")
	if base.Tools != "critical" || !base.Mise || base.Base != config.BaseKind {
		t.Errorf("base: %+v", base)
	}
}

// The runtime contract on any base (PIC7, D1151, the spec's §Runtime
// contract: "an image can be built from any base, as long as it meets the
// Fugaro runtime contract… Fugaro does not check the FROM line"). The tests
// below run in a PATH holding nothing but what they link into it, so a
// missing tool is a missing file, and they read a tools table this machine
// can satisfy, because the real table's gh, tini and finalize-checkout rows
// name tools no test machine carries.

// contractProbe is a critical row no line of the code names: the contract
// check must take its list from the tools table, so a row the table calls
// critical is enforced and one it calls kit is not.
const contractProbe = "fugaro-contract-probe"

// contractTable swaps the tools table for one this machine can satisfy,
// with contractProbe at tier.
func contractTable(t *testing.T, tier string) {
	t.Helper()
	old := baseTools
	baseTools = "git\tgit --version\tall\tcritical\nclaude\tclaude --version\tall\tcritical\n" +
		contractProbe + "\t-\tall\t" + tier + "\n"
	t.Cleanup(func() { baseTools = old })
}

// contractEnv is the PATH and HOME the contract tests run in: nothing but
// what it links into a fresh directory — the real git and sh, so the
// checkout checks still run and a shell is there, and fakes for claude,
// mise, sudo (exiting sudoExit) and the real fugaro. It returns the
// directory, so a test takes a tool away by removing its link.
func contractEnv(t *testing.T, sudoExit int) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the user check fails when the tests run as root")
	}
	bin := t.TempDir()
	for _, name := range []string{"git", "sh"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s on this machine", name)
		}
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFiles(t, bin, map[string]string{
		"claude": "#!/bin/sh\necho '2.1.283 (Claude Code)'\n",
		"mise":   "#!/bin/sh\nprintf '{}\\n'\n",
		"sudo":   fmt.Sprintf("#!/bin/sh\nexit %d\n", sudoExit),
	})
	fugaro := testutil.BuildFugaro(t)
	if err := os.Symlink(fugaro, filepath.Join(bin, "fugaro")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	return bin
}

// dropTool makes a tool the contract tests linked missing.
func dropTool(t *testing.T, bin, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(bin, name)); err != nil {
		t.Fatal(err)
	}
}

// contractSpec is a derived image's smoke on a base that is not Fugaro's: an
// image may be built from any base that meets the contract, so nothing about
// the check may depend on Base. It asks for the critical tier, which is the
// contract, and leaves the workflow's build out.
func contractSpec(t *testing.T) SelftestSpec {
	t.Helper()
	spec := selftestFixture(t)
	spec.Tools, spec.Base, spec.SkipVerify = "critical", "ghcr.io/example/any-base:1.2", true
	return spec
}

// TestContractCheckPassesOnANonFugaroBaseThatMeetsTheContract: the point of
// the task. The check judges the image, not the base it was built on, so an
// image whose base is not Fugaro's passes when the contract's list and a
// shell are there.
func TestContractCheckPassesOnANonFugaroBaseThatMeetsTheContract(t *testing.T) {
	contractTable(t, "kit")
	spec := contractSpec(t)
	contractEnv(t, 1)
	var log bytes.Buffer
	r := Selftest(context.Background(), spec, &log)
	if !r.Passed {
		t.Fatalf("report %+v\nlog:\n%s", r, log.String())
	}
	c, ok := checkNamed(r, "contract")
	if !ok || !c.OK {
		t.Fatalf("no passing contract check: %+v", r)
	}
	// The contract's rows are the critical tier's, reported one by one as
	// before: the contract check summarises them rather than replacing them.
	if c, ok := checkNamed(r, "tool:git"); !ok || !c.OK {
		t.Errorf("the critical rows are no longer reported: %+v", r)
	}
}

// TestContractCheckFailsWhenGitIsMissing: git is one of the tools the
// spec's contract requires, so an image without it is refused.
func TestContractCheckFailsWhenGitIsMissing(t *testing.T) {
	contractTable(t, "kit")
	spec := contractSpec(t)
	bin := contractEnv(t, 1)
	dropTool(t, bin, "git")
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	if r.Passed {
		t.Fatalf("the image passed without git: %+v", r)
	}
	c, ok := checkNamed(r, "contract")
	if !ok || c.OK {
		t.Fatalf("contract = %+v, want a failure", c)
	}
	if !strings.Contains(c.Detail, "git") {
		t.Errorf("the contract check's detail does not name git: %q", c.Detail)
	}
}

// TestContractCheckFailsWhenTheAgentBinaryIsMissing: the agent binary is
// what the image is for, so an image without it is refused even though
// everything else the contract asks for is there.
func TestContractCheckFailsWhenTheAgentBinaryIsMissing(t *testing.T) {
	contractTable(t, "kit")
	spec := contractSpec(t)
	bin := contractEnv(t, 1)
	dropTool(t, bin, "claude")
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	if r.Passed {
		t.Fatalf("the image passed without the agent binary: %+v", r)
	}
	c, ok := checkNamed(r, "contract")
	if !ok || c.OK {
		t.Fatalf("contract = %+v, want a failure", c)
	}
	if !strings.Contains(c.Detail, "claude") {
		t.Errorf("the contract check's detail does not name the agent binary: %q", c.Detail)
	}
	if own, ok := checkNamed(r, "claude"); !ok || own.OK {
		t.Errorf("claude = %+v, want the missing agent binary reported there too", own)
	}
}

// TestContractCheckFailsWhenTheShellIsMissing: the shell is what PIC7 adds
// to the rows the table already lists -- the contract requires one, and the
// runner runs every workflow command through `sh -c` -- so an image without
// it is refused even when every critical tool answers.
func TestContractCheckFailsWhenTheShellIsMissing(t *testing.T) {
	contractTable(t, "kit")
	spec := contractSpec(t)
	bin := contractEnv(t, 1)
	dropTool(t, bin, "sh")
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	if r.Passed {
		t.Fatalf("the image passed without a shell: %+v", r)
	}
	c, ok := checkNamed(r, "contract")
	if !ok || c.OK {
		t.Fatalf("contract = %+v, want a failure", c)
	}
	if !strings.Contains(c.Detail, "sh") {
		t.Errorf("the contract check's detail does not name the shell: %q", c.Detail)
	}
}

// TestContractCheckDoesNotInspectTheFromLine: the spec says Fugaro does not
// check the FROM line, so the check's answer is the same for Fugaro's own
// base and for any other — on an image that meets the contract and on one
// that does not.
func TestContractCheckDoesNotInspectTheFromLine(t *testing.T) {
	bases := []string{config.BaseKind, "ghcr.io/example/any-base:1.2", "mirror.example.invalid:5000/team/oddball:1"}
	cases := []struct{ name, missing string }{
		{"an image that meets the contract", ""},
		{"an image missing git", "git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contractTable(t, "kit")
			spec := contractSpec(t)
			bin := contractEnv(t, 1)
			if tc.missing != "" {
				dropTool(t, bin, tc.missing)
			}
			var first Report
			for i, base := range bases {
				s := spec
				s.Base = base
				r := Selftest(context.Background(), s, &bytes.Buffer{})
				if i == 0 {
					first = r
					continue
				}
				if !reflect.DeepEqual(r, first) {
					t.Errorf("the base %q changed the report:\n got %+v\nwant %+v", base, r, first)
				}
			}
			if _, ok := checkNamed(first, "contract"); !ok {
				t.Fatalf("no contract check: %+v", first)
			}
		})
	}
}

// TestContractCheckStillFailsOnARootImage: the widening adds checks, it
// never drops one. The contract check on any base is still refused when the
// image runs as root.
func TestContractCheckStillFailsOnARootImage(t *testing.T) {
	contractTable(t, "kit")
	spec := contractSpec(t)
	contractEnv(t, 1)
	// The user check is the one hardening gate a test machine cannot arrange
	// for real: these tests skip as root, and a non-root process cannot
	// become one, so the check is made to see uid 0.
	old := geteuid
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = old })
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	if r.Passed {
		t.Fatalf("the image passed as root: %+v", r)
	}
	if c, ok := checkNamed(r, "user"); !ok || c.OK {
		t.Fatalf("user = %+v, want it to fail (and only it)", c)
	}
	if _, ok := checkNamed(r, "contract"); !ok {
		t.Errorf("the contract check did not run: %+v", r)
	}
}

// TestContractCheckStillFailsOnPasswordlessSudo: the same for the sudo
// gate, which the image's build must remove.
func TestContractCheckStillFailsOnPasswordlessSudo(t *testing.T) {
	contractTable(t, "kit")
	spec := contractSpec(t)
	contractEnv(t, 0)
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	if r.Passed {
		t.Fatalf("the image passed with passwordless sudo: %+v", r)
	}
	if c, ok := checkNamed(r, "no-sudo"); !ok || c.OK {
		t.Fatalf("no-sudo = %+v, want it to fail", c)
	}
	if _, ok := checkNamed(r, "contract"); !ok {
		t.Errorf("the contract check did not run: %+v", r)
	}
}

// TestContractListIsReadNotDuplicated: the check's list is the tools
// table's critical rows, not a list written into the code (D1151: there is
// no separate contract list to publish). A row the code never names is
// enforced when the table calls it critical, and nothing is asked of it when
// the table calls it kit.
func TestContractListIsReadNotDuplicated(t *testing.T) {
	spec := contractSpec(t)
	contractEnv(t, 1)
	old := baseTools
	t.Cleanup(func() { baseTools = old })

	baseTools = "git\tgit --version\tall\tcritical\n" + contractProbe + "\t-\tall\tcritical\n"
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	c, ok := checkNamed(r, "contract")
	if !ok || c.OK || !strings.Contains(c.Detail, contractProbe) {
		t.Fatalf("contract = %+v, want a failure naming %s", c, contractProbe)
	}

	baseTools = "git\tgit --version\tall\tcritical\n" + contractProbe + "\t-\tall\tkit\n"
	r = Selftest(context.Background(), spec, &bytes.Buffer{})
	if c, ok := checkNamed(r, "contract"); !ok || !c.OK {
		t.Fatalf("contract = %+v, want a pass: a kit row is not the contract", c)
	}
}
