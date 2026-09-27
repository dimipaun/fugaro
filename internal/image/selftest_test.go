package image

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

// selftestEnv puts fake claude, node and sudo (exiting sudoExit) and the
// real fugaro first on PATH, and points HOME at an empty directory.
func selftestEnv(t *testing.T, sudoExit int) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the user check fails when the tests run as root")
	}
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{
		"claude": "#!/bin/sh\necho '2.1.283 (Claude Code)'\n",
		"node":   "#!/bin/sh\necho v24.19.0\n",
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
		Base: "web-node", RepoDir: repo, Commit: testutil.Git(t, repo, "rev-parse", "HEAD"), Origin: remote, Node: "24",
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
	for _, name := range []string{"user", "no-sudo", "claude", "node", "checkout", "origin", "git-credentials", "home-credentials", "verify-build"} {
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
		{"node major mismatch", "node", 1, func(t *testing.T, s *SelftestSpec) { s.Node = "22" }},
		{"node exact mismatch", "node", 1, func(t *testing.T, s *SelftestSpec) { s.Node = "24.1.0" }},
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

// --- Controller additions: setuid-root binaries and /etc/sudoers.d/sudoers
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
	mkSetuid(t, filepath.Join(root, "usr/bin/su"))
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
	mkSetuid(t, filepath.Join(root, "usr/bin/su"))
	mkSetuid(t, filepath.Join(root, "opt/evil"))
	add, r := addRecorder()
	checkSetuid(context.Background(), root, add)
	c, ok := checkNamed(*r, "no-setuid")
	if !ok || c.OK {
		t.Fatalf("want no-setuid to fail on an unexpected setuid binary, got %+v", r.Checks)
	}
	if !strings.Contains(c.Detail, "evil") || strings.Contains(c.Detail, "usr/bin/su") {
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
