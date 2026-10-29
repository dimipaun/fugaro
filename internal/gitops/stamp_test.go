package gitops

// The stamp a run leaves on its commits: the environment a commit is made in
// (StampEnv) and the hook that reads it (WriteStampHooks). Every test here
// runs real git in a throwaway repository, because the whole point of the
// pair is what git does with them.

import (
	"bytes"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// runStamp is the stamp the tests here start from: the run's ID, its rigor
// mode, and the model the stage pinned (D601, D602).
func runStamp() Stamp {
	return Stamp{Trailers: []Trailer{
		{Key: TrailerRun, Value: "run-1"},
		{Key: TrailerRigor, Value: "standard"},
		{Key: TrailerModel, Value: "claude-opus-5-5"},
	}}
}

// newStampRepo makes a repository with the stamp hooks installed and returns
// its directory, the hook directory they were written into, and the
// environment a commit in it is made under. GIT_CONFIG_COUNT is cleared
// first: StampEnv numbers its own entry after the caller's, and the count
// this process happens to carry must not decide what a test expects.
func newStampRepo(t *testing.T, s Stamp, chainTo string) (dir, hooks string, env []string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir = t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	hooks = filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, chainTo); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	return dir, hooks, StampEnv(s, hooks)
}

// stampGit runs git in dir with env appended to this process's environment —
// the way the runner hands the stamp to every commit — and returns its
// combined output. The timeout is testutil.Git's, for the same reason: a git
// that never returns should fail a test, not hang the binary.
func stampGit(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// commitStamp makes an empty commit under env, so a test needs nothing to
// change in the working tree, and returns the message git stored:
// prepare-commit-msg runs for an empty commit as it does for any other.
func commitStamp(t *testing.T, dir string, env []string, msg string, extra ...string) string {
	t.Helper()
	args := append([]string{"commit", "--allow-empty", "--quiet"}, extra...)
	if out, err := stampGit(t, dir, env, append(args, "-m", msg)...); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return testutil.Git(t, dir, "log", "-1", "--format=%B")
}

// trailersOf reads the trailer lines of a commit message the way these tests
// ask about them: keyed by the lower-cased key git matches by, keeping every
// value, so a duplicate is two entries and a replacement is one.
func trailersOf(msg string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(msg, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			continue
		}
		if strings.ContainsFunc(key, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
		}) {
			continue
		}
		out[strings.ToLower(key)] = append(out[strings.ToLower(key)], strings.TrimSpace(value))
	}
	return out
}

// stampEnvMap reads an environment as a map, so a test can ask what StampEnv
// set without depending on the order it set it in.
func stampEnvMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("environment entry %q is not KEY=VALUE", kv)
		}
		out[key] = value
	}
	return out
}

// TestStampEnvSetsAuthorOnlyWhenGiven: an author the launcher named is the
// author of the commits, and the committer is left out of the environment
// entirely, so it stays the run's identity (D645) — the App's bot account on
// GitHub, or Fugaro. With no author named, nothing about an identity is set
// at all.
func TestStampEnvSetsAuthorOnlyWhenGiven(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	const hookDir = "/hooks"
	s := runStamp()
	s.Author = &Person{Name: "person-1", Email: "person-1@example.invalid"}
	got := stampEnvMap(t, StampEnv(s, hookDir))
	if got["GIT_AUTHOR_NAME"] != "person-1" || got["GIT_AUTHOR_EMAIL"] != "person-1@example.invalid" {
		t.Errorf("author = %q <%q>, want the launcher's", got["GIT_AUTHOR_NAME"], got["GIT_AUTHOR_EMAIL"])
	}
	for _, k := range []string{"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %q, want the committer left to the run's identity", k, v)
		}
	}

	want := map[string]string{
		"FUGARO_STAMP_RUN":   "run-1",
		"FUGARO_STAMP_RIGOR": "standard",
		"FUGARO_STAMP_MODEL": "claude-opus-5-5",
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": hookDir,
	}
	if without := stampEnvMap(t, StampEnv(runStamp(), hookDir)); !maps.Equal(without, want) {
		t.Errorf("StampEnv with no author = %v, want %v", without, want)
	}
}

// TestStampEnvAppendsToCallerGitConfig: a caller that already carries
// GIT_CONFIG_* entries keeps them. StampEnv numbers its own entry after the
// caller's count and returns the larger count, which is what the caller
// appends to its own environment; the caller's indexes are never reused. The
// process environment is where the count is read from, which is why a caller
// with entries of its own puts them there.
func TestStampEnvAppendsToCallerGitConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	const hookDir = "/hooks"
	got := stampEnvMap(t, StampEnv(runStamp(), hookDir))
	want := map[string]string{
		"GIT_CONFIG_COUNT":   "3",
		"GIT_CONFIG_KEY_2":   "core.hooksPath",
		"GIT_CONFIG_VALUE_2": hookDir,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_KEY_1", "GIT_CONFIG_VALUE_1"} {
		if v, ok := got[k]; ok {
			t.Errorf("StampEnv wrote %s = %q, which is one of the caller's own entries", k, v)
		}
	}
}

// TestStampEnvRefusesNewlineInTrailerValue: a trailer value reaches
// interpret-trailers as a --trailer argument, and interpret-trailers reads
// such an argument as trailer *lines*. A value carrying a newline would add
// trailers the run never chose — a Signed-off-by among them (R1, D644) — so
// StampEnv returns nothing at all rather than part of a stamp: a run is
// either stamped or not, and the commits of one that is not are what
// finalize counts (D603).
func TestStampEnvRefusesNewlineInTrailerValue(t *testing.T) {
	const hookDir = "/hooks"
	s := runStamp()
	s.Trailers = []Trailer{{Key: TrailerModel, Value: "claude-opus-5-5\nSigned-off-by: person-1 <person-1@example.invalid>"}}
	if got := StampEnv(s, hookDir); got != nil {
		t.Errorf("StampEnv = %v, want nothing: a value with a newline in it adds trailers of its own", got)
	}
}

// TestStampEnvRefusesAnUnsafeAuthor: git writes the author's name and address
// into the commit's author line, so an author carrying a newline, an angle
// bracket or another control character can forge a second identity. The
// launcher is the only writer of an author, but a stamp refused is cheaper
// than a commit whose author nobody can trust.
func TestStampEnvRefusesAnUnsafeAuthor(t *testing.T) {
	const hookDir = "/hooks"
	for _, tc := range []struct{ name, who, email string }{
		{"a newline in the name", "person-1\nperson-2", "person-1@example.invalid"},
		{"an angle bracket in the name", "person-1 <person-2@example.invalid>", "person-1@example.invalid"},
		{"a newline in the address", "person-1", "person-1@example.invalid\nperson-2@example.invalid"},
		{"no name", "", "person-1@example.invalid"},
		{"no address", "person-1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := runStamp()
			s.Author = &Person{Name: tc.who, Email: tc.email}
			if got := StampEnv(s, hookDir); got != nil {
				t.Errorf("StampEnv = %v, want nothing: %q <%q> is not an author git should write", got, tc.who, tc.email)
			}
		})
	}
}

// TestStampEnvRefusesAnUnknownTrailerKey: the hook knows the three Fugaro-*
// trailers by name (D601), and a caller that asks for a fourth would get a
// commit silently missing it. Refusing the stamp is the loud failure.
func TestStampEnvRefusesAnUnknownTrailerKey(t *testing.T) {
	const hookDir = "/hooks"
	s := runStamp()
	s.Trailers = append(s.Trailers, Trailer{Key: "Fugaro-Job", Value: "job-1"})
	if got := StampEnv(s, hookDir); got != nil {
		t.Errorf("StampEnv = %v, want nothing: Fugaro-Job is not a trailer the hook writes", got)
	}
}

// TestStampHookAddsTrailers: a commit made with the stamp environment in
// place is born carrying the run's trailers, under the subject and after the
// body — nothing rewrites it afterwards (D603).
func TestStampHookAddsTrailers(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	msg := commitStamp(t, dir, env, "add the thing\n\nWhy: it was missing.\n")
	if !strings.HasPrefix(msg, "add the thing\n\nWhy: it was missing.") {
		t.Errorf("the message was rewritten around the trailers:\n%s", msg)
	}
	want := map[string]string{"fugaro-run": "run-1", "fugaro-rigor": "standard", "fugaro-model": "claude-opus-5-5"}
	got := trailersOf(msg)
	for key, value := range want {
		if !slices.Equal(got[key], []string{value}) {
			t.Errorf("%s = %v, want [%s] once", key, got[key], value)
		}
	}
}

// TestStampHookReplacesCaseFoldedFugaroTrailers: git matches trailer keys
// without regard to case, and the hook must not leave a message carrying two
// Fugaro-Run lines — one of them an earlier run's. The key the message
// already spells is the one that keeps its place.
func TestStampHookReplacesCaseFoldedFugaroTrailers(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	msg := commitStamp(t, dir, env, "subject\n\nfugaro-run: earlier-run\nFUGARO-RIGOR: earlier-rigor\n")
	got := trailersOf(msg)
	if want := []string{"run-1"}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v once", got["fugaro-run"], want)
	}
	if want := []string{"standard"}; !slices.Equal(got["fugaro-rigor"], want) {
		t.Errorf("fugaro-rigor = %v, want %v once", got["fugaro-rigor"], want)
	}
	for _, stale := range []string{"earlier-run", "earlier-rigor"} {
		if strings.Contains(msg, stale) {
			t.Errorf("the message still carries %s:\n%s", stale, msg)
		}
	}
}

// TestStampHookNeverAddsSignedOffBy: Fugaro never writes a Signed-off-by
// (R1), and it never removes one either (D644) — one the message carries may
// be a real person's, brought in by a cherry-pick.
func TestStampHookNeverAddsSignedOffBy(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	if msg := commitStamp(t, dir, env, "subject\n"); trailersOf(msg)["signed-off-by"] != nil {
		t.Errorf("a Signed-off-by was added:\n%s", msg)
	}
	who := "person-1 <person-1@example.invalid>"
	msg := commitStamp(t, dir, env, "subject\n\nSigned-off-by: "+who+"\n")
	if got := trailersOf(msg)["signed-off-by"]; !slices.Equal(got, []string{who}) {
		t.Errorf("Signed-off-by = %v, want the message's own %q once", got, who)
	}
}

// TestStampHookOmitsEmptyModel: a stage that pins no model has none to
// record, and an empty Fugaro-Model would read as a model named "" rather
// than as one the run did not record (D602). The variable is left empty here
// rather than left out, so what is pinned is the hook's own guard.
func TestStampHookOmitsEmptyModel(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	dir, hooks, _ := newStampRepo(t, runStamp(), "")
	env := append(StampEnv(runStamp(), hooks), "FUGARO_STAMP_MODEL=")
	if got := stampEnvMap(t, StampEnv(Stamp{Trailers: []Trailer{{Key: TrailerModel}}}, hooks)); got["FUGARO_STAMP_MODEL"] != "" {
		t.Errorf("StampEnv set FUGARO_STAMP_MODEL = %q for an empty value, want it left out", got["FUGARO_STAMP_MODEL"])
	}
	msg := commitStamp(t, dir, env, "subject\n")
	got := trailersOf(msg)
	if _, ok := got["fugaro-model"]; ok {
		t.Errorf("Fugaro-Model was written for an empty model:\n%s", msg)
	}
	if want := []string{"run-1"}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v", got["fugaro-run"], want)
	}
}

// TestStampHookRunsUnderNoVerify: git commit --no-verify skips commit-msg and
// pre-commit, and does not skip prepare-commit-msg (githooks(5)) — which is
// why the stamp is a prepare-commit-msg hook and not a commit-msg one
// (D603).
func TestStampHookRunsUnderNoVerify(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	msg := commitStamp(t, dir, env, "subject\n", "--no-verify")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: --no-verify does not skip prepare-commit-msg", trailersOf(msg)["fugaro-run"], want)
	}
}

// TestStampHookChainsToRepositoryHooks: pointing core.hooksPath at the
// runner's hook directory would silence the repository's own hooks, so each
// script hands the hook the repository configured its own, with the same
// arguments — and a chained hook that fails fails the commit, because its
// exit status is what prepare-commit-msg returns.
func TestStampHookChainsToRepositoryHooks(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	marker := filepath.Join(t.TempDir(), "ran")
	repoHooks := filepath.Join(dir, ".git", "hooks")
	names := []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"}
	files := map[string]string{}
	for _, name := range names {
		files[name] = "#!/bin/sh\nprintf 'hook %s %s\\n' " + name + " \"$*\" >> " + shQuote(marker) + "\n"
	}
	testutil.WriteFiles(t, repoHooks, files)

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, repoHooks); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	env := StampEnv(runStamp(), hooks)
	msg := commitStamp(t, dir, env, "subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the stamp did not run", trailersOf(msg)["fugaro-run"], want)
	}
	ran, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("no repository hook ran: %v", err)
	}
	for _, name := range names {
		if !strings.Contains(string(ran), "hook "+name+" ") {
			t.Errorf("%s did not run; the hooks that did:\n%s", name, ran)
		}
	}
	if !strings.Contains(string(ran), filepath.Join(".git", "COMMIT_EDITMSG")) {
		t.Errorf("the chained hooks were not given the message file git gave the stamp:\n%s", ran)
	}

	testutil.WriteFiles(t, repoHooks, map[string]string{"commit-msg": "#!/bin/sh\nexit 3\n"})
	if out, err := stampGit(t, dir, env, "commit", "--allow-empty", "--quiet", "-m", "subject\n"); err == nil {
		t.Errorf("a failing repository commit-msg did not fail the commit:\n%s", out)
	}
}

// TestStampHookChainsToAQuotedDirectory: the hook directory a repository
// configured reaches the pass-through through its text, and a run's agent can
// write that configuration — so it is one quoted shell word and not something
// the shell reads. This directory is a name a shell would otherwise split and
// end its own quoting on.
func TestStampHookChainsToAQuotedDirectory(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	marker := filepath.Join(t.TempDir(), "ran")
	repoHooks := filepath.Join(t.TempDir(), "the repo's hooks")
	testutil.WriteFiles(t, repoHooks, map[string]string{
		"prepare-commit-msg": "#!/bin/sh\nprintf '%s\\n' ran >> " + shQuote(marker) + "\n",
	})

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, repoHooks); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	msg := commitStamp(t, dir, StampEnv(runStamp(), hooks), "subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v", trailersOf(msg)["fugaro-run"], want)
	}
	if ran, err := os.ReadFile(marker); err != nil || string(ran) != "ran\n" {
		t.Errorf("the chained hook in %q ran %q, %v, want it once", repoHooks, ran, err)
	}
}

// TestWriteStampHooksInstallsEveryHook: git runs only the hooks it finds in
// the directory core.hooksPath names, so every name of githooks(5) is a file
// there — the stamp itself under prepare-commit-msg, and the rest as
// pass-throughs carrying the directory they hand off to. Each is executable:
// git ignores a hook it cannot run, with no more than a warning.
func TestWriteStampHooksInstallsEveryHook(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-there-yet")
	chain := filepath.Join(t.TempDir(), "the repo's hooks")
	if err := WriteStampHooks(dir, chain); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	want := append([]string{stampHookName}, chainHookNames...)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Errorf("%d hooks in %s, want %d", len(entries), dir, len(want))
	}
	for _, name := range want {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s was not written: %v", name, err)
			continue
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Errorf("%s is mode %v, want 0755", name, got)
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "@FUGARO_CHAIN@") {
			t.Errorf("%s still carries the chain placeholder:\n%s", name, body)
		}
		if !strings.Contains(string(body), shQuote(chain)) {
			t.Errorf("%s does not name the hook directory it chains to:\n%s", name, body)
		}
		if stamps := strings.Contains(string(body), "interpret-trailers"); stamps != (name == stampHookName) {
			t.Errorf("%s stamps = %v, want %v", name, stamps, name == stampHookName)
		}
	}
}

// TestStampHookIgnoresRepositoryTrailerConfig: every rule that decides
// whether a trailer is added, whether an existing one is replaced, and what
// the trailer is called travels on the hook's own command line, per token as
// well as generally. The repository's .git/config is a file a run's agent can
// write, and what it says about trailers there is not what the stamp follows
// (constraint 8).
func TestStampHookIgnoresRepositoryTrailerConfig(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	for _, kv := range [][2]string{
		{"trailer.ifexists", "doNothing"},
		{"trailer.ifmissing", "doNothing"},
		{"trailer.fugaro-run.ifexists", "doNothing"},
		{"trailer.fugaro-run.ifmissing", "doNothing"},
		{"trailer.fugaro-run.key", "Fugaro-Renamed"},
	} {
		testutil.Git(t, dir, "config", kv[0], kv[1])
	}
	msg := commitStamp(t, dir, env, "subject\n\nfugaro-run: earlier-run\n")
	got := trailersOf(msg)
	if want := []string{"run-1"}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the repository's config steered the stamp", got["fugaro-run"], want)
	}
	if _, renamed := got["fugaro-renamed"]; renamed {
		t.Errorf("the repository's config renamed the trailer:\n%s", msg)
	}
	second := commitStamp(t, dir, env, "another subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(second)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v on a message that carried none, want %v: the repository's config dropped the stamp", trailersOf(second)["fugaro-run"], want)
	}
}

// TestStampEnvOutranksRepositoryHooksPath: the configuration StampEnv carries
// is environment configuration, which outranks the repository's own file
// (git-config(1)). A repository whose .git/config points core.hooksPath
// somewhere of its own — or an agent that writes one — cannot make the stamp
// not run.
func TestStampEnvOutranksRepositoryHooksPath(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	marker := filepath.Join(t.TempDir(), "ran")
	repoHooks := t.TempDir()
	testutil.WriteFiles(t, repoHooks, map[string]string{
		"prepare-commit-msg": "#!/bin/sh\nprintf '%s\\n' repo-hook >> " + shQuote(marker) + "\n",
	})
	testutil.Git(t, dir, "config", "core.hooksPath", repoHooks)

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, ""); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	msg := commitStamp(t, dir, StampEnv(runStamp(), hooks), "subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the repository's core.hooksPath won", trailersOf(msg)["fugaro-run"], want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the repository's own hook directory ran, so the environment configuration did not outrank it")
	}
}
