package gitops

// The stamp a run leaves on its commits: the environment a commit is made in
// (StampEnv) and the hook that reads it (WriteStampHooks). Every test here
// runs real git in a throwaway repository, because the whole point of the
// pair is what git does with them.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/shellword"
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
// first: environment configuration naming entries that are not there is
// configuration git refuses, and the count this process happens to carry is
// not one a test means to hand the stamp.
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
	return dir, hooks, StampEnv(s, hooks, nil)
}

// stampCallerEnv is the environment of a run that carries configuration of its
// own — the credential helper CredentialVars gives every git call a run makes
// — which the stamp is added to and must not clobber.
func stampCallerEnv(t *testing.T) []string {
	t.Helper()
	return credEnv(t, "https://git.example.invalid")
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
	got := stampEnvMap(t, StampEnv(s, hookDir, nil))
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
	if without := stampEnvMap(t, StampEnv(runStamp(), hookDir, nil)); !maps.Equal(without, want) {
		t.Errorf("StampEnv with no author = %v, want %v", without, want)
	}
}

// TestStampEnvAppendsToCallerGitConfig: a caller that already carries
// GIT_CONFIG_* entries keeps them, whichever environment they live in. The
// count comes from the env the caller passes and not from this process's
// environment, where the credential helper is not: CredentialVars puts it in
// the environment of the git calls a run makes, as an env slice. A count read
// from the process environment would number the stamp's entry over the
// helper's first — and git resolves as many entries as the count says, so the
// helper would be gone and a push would ask for a password.
func TestStampEnvAppendsToCallerGitConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	const hookDir = "/hooks"
	env := StampEnv(runStamp(), hookDir, stampCallerEnv(t))
	got := stampEnvMap(t, env)
	for k, want := range map[string]string{
		"GIT_CONFIG_COUNT":   "3",
		"GIT_CONFIG_KEY_2":   "core.hooksPath",
		"GIT_CONFIG_VALUE_2": hookDir,
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	// What git makes of the pair, which is the whole question: one resolve,
	// the caller's configuration and the stamp's.
	for key, want := range map[string]string{
		"credential.https://git.example.invalid.helper": credentialHelper,
		"core.hooksPath": hookDir,
	} {
		out, err := stampGit(t, dir, env, "config", "--get", key)
		if err != nil {
			t.Errorf("git config --get %s: %v\n%s", key, err, out)
			continue
		}
		if strings.TrimSpace(out) != want {
			t.Errorf("git config --get %s = %q, want %q", key, strings.TrimSpace(out), want)
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
	caller := stampCallerEnv(t)
	if got := StampEnv(s, hookDir, caller); !slices.Equal(got, caller) {
		t.Errorf("StampEnv = %v, want the caller's environment unchanged: a value with a newline in it adds trailers of its own", got)
	}
}

// TestStampEnvRefusesACountItCannotRead: the count is what decides where the
// stamp's entry lands. One that cannot be read — a value that is not a number
// — would put the entry on top of one of the caller's own, which for the
// credential helper is a helper that stops answering. The stamp is left
// unwritten instead.
func TestStampEnvRefusesACountItCannotRead(t *testing.T) {
	const hookDir = "/hooks"
	caller := []string{
		"GIT_CONFIG_COUNT=two",
		"GIT_CONFIG_KEY_0=credential.https://git.example.invalid.helper",
		"GIT_CONFIG_VALUE_0=helper",
	}
	if got := StampEnv(runStamp(), hookDir, caller); !slices.Equal(got, caller) {
		t.Errorf("StampEnv = %v, want the caller's environment unchanged", got)
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
			caller := stampCallerEnv(t)
			if got := StampEnv(s, hookDir, caller); !slices.Equal(got, caller) {
				t.Errorf("StampEnv = %v, want the caller's environment unchanged: %q <%q> is not an author git should write", got, tc.who, tc.email)
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
	caller := stampCallerEnv(t)
	if got := StampEnv(s, hookDir, caller); !slices.Equal(got, caller) {
		t.Errorf("StampEnv = %v, want the caller's environment unchanged: Fugaro-Job is not a trailer the hook writes", got)
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

// TestStampHookReplacesEveryTrailerInTheMessage: a Fugaro-* key is replaced,
// never duplicated, and a line carrying one is one wherever it sits in the
// message. interpret-trailers reads the trailer block and nothing else, so a
// message that carries the trailers of more than one commit — a squash writes
// each of them into the body — keeps the stale ones beside the one this
// commit's run writes unless the hook takes them out itself.
func TestStampHookReplacesEveryTrailerInTheMessage(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	msg := commitStamp(t, dir, env, strings.Join([]string{
		"first subject\n",
		"first body\n",
		"Fugaro-Run: earlier-run",
		"Fugaro-Rigor: earlier-rigor",
		"fugaro-model: earlier-model\n",
		"second subject\n",
		"second body\n",
		"Fugaro-Run: earlier-run",
		"Fugaro-Rigor: earlier-rigor",
		"Fugaro-Model: earlier-model",
		"fugaro-run: another-earlier-run\n",
	}, "\n"))
	got := trailersOf(msg)
	for key, want := range map[string][]string{
		"fugaro-run":   {"run-1"},
		"fugaro-rigor": {"standard"},
		"fugaro-model": {"claude-opus-5-5"},
	} {
		if !slices.Equal(got[key], want) {
			t.Errorf("%s = %v, want %v once:\n%s", key, got[key], want, msg)
		}
	}
	for _, stale := range []string{"earlier-run", "earlier-rigor", "earlier-model"} {
		if strings.Contains(msg, stale) {
			t.Errorf("the message still carries %s:\n%s", stale, msg)
		}
	}
}

// TestStampHookSquashedCommitKeepsOneTrailer: the same requirement where git
// writes the message itself. An interactive rebase that squashes makes one
// commit out of several and copies the message of each into it — trailers and
// all — and the commit that comes out is stamped once, with the bodies of the
// commits it was made of still there.
func TestStampHookSquashedCommitKeepsOneTrailer(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	testutil.WriteFiles(t, dir, map[string]string{"a.txt": "one\n"})
	testutil.Git(t, dir, "add", "-A")
	commitStamp(t, dir, env, "first subject\n\nfirst body\n")
	testutil.WriteFiles(t, dir, map[string]string{"b.txt": "two\n"})
	testutil.Git(t, dir, "add", "-A")
	commitStamp(t, dir, env, "second subject\n\nsecond body\n")

	env = append(env, "GIT_SEQUENCE_EDITOR=sed -i.bak '2s/^pick/squash/'", "GIT_EDITOR=true")
	if out, err := stampGit(t, dir, env, "rebase", "-i", "--root"); err != nil {
		t.Fatalf("git rebase -i --root: %v\n%s", err, out)
	}
	msg := testutil.Git(t, dir, "log", "-1", "--format=%B")
	if !strings.Contains(msg, "first subject") || !strings.Contains(msg, "second subject") {
		t.Errorf("the squash did not combine both messages:\n%s", msg)
	}
	got := trailersOf(msg)
	for key, want := range map[string][]string{
		"fugaro-run":   {"run-1"},
		"fugaro-rigor": {"standard"},
		"fugaro-model": {"claude-opus-5-5"},
	} {
		if !slices.Equal(got[key], want) {
			t.Errorf("%s = %v, want %v once after the squash:\n%s", key, got[key], want, msg)
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
	env := append(StampEnv(runStamp(), hooks, nil), "FUGARO_STAMP_MODEL=")
	if got := stampEnvMap(t, StampEnv(Stamp{Trailers: []Trailer{{Key: TrailerModel}}}, hooks, nil)); got["FUGARO_STAMP_MODEL"] != "" {
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
		files[name] = "#!/bin/sh\nprintf 'hook %s %s\\n' " + name + " \"$*\" >> " + shellword.Quote(marker) + "\n"
	}
	testutil.WriteFiles(t, repoHooks, files)

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, repoHooks); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	env := StampEnv(runStamp(), hooks, nil)
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

// TestStampHookChainsToDefaultHooksWhenNoneIsNamed: the caller names no hook
// directory — the common case, a repository that configured no core.hooksPath
// — and the repository's hooks in git's default $GIT_DIR/hooks still run. The
// environment configuration pointing core.hooksPath at the stamp directory
// outranks that default, so without this a repository whose hooks live there
// would have them stop running, silently (constraint 2).
func TestStampHookChainsToDefaultHooksWhenNoneIsNamed(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	marker := filepath.Join(t.TempDir(), "ran")
	testutil.WriteFiles(t, filepath.Join(dir, ".git", "hooks"), map[string]string{
		"pre-commit":         "#!/bin/sh\nprintf 'pre-commit\\n' >> " + shellword.Quote(marker) + "\n",
		"prepare-commit-msg": "#!/bin/sh\nprintf 'prepare-commit-msg\\n' >> " + shellword.Quote(marker) + "\n",
	})

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, ""); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	msg := commitStamp(t, dir, StampEnv(runStamp(), hooks, nil), "subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the stamp did not run", trailersOf(msg)["fugaro-run"], want)
	}
	ran, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("no repository hook in $GIT_DIR/hooks ran: %v", err)
	}
	for _, name := range []string{"pre-commit", "prepare-commit-msg"} {
		if !strings.Contains(string(ran), name+"\n") {
			t.Errorf("%s did not run; the hooks that did:\n%s", name, ran)
		}
	}
}

// TestStampHookDoesNotChainToItself: a repository whose own configuration
// points core.hooksPath at the stamp directory — .git/config is a file a run's
// agent can write — would have every hook exec itself, which is a commit that
// never returns. The chain is not taken when it resolves to the script that is
// already running, and the commit is made and stamped.
func TestStampHookDoesNotChainToItself(t *testing.T) {
	dir, hooks, env := newStampRepo(t, runStamp(), "")
	testutil.Git(t, dir, "config", "core.hooksPath", hooks)
	msg := commitStamp(t, dir, env, "subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v once", trailersOf(msg)["fugaro-run"], want)
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
		"prepare-commit-msg": "#!/bin/sh\nprintf '%s\\n' ran >> " + shellword.Quote(marker) + "\n",
	})

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, repoHooks); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	msg := commitStamp(t, dir, StampEnv(runStamp(), hooks, nil), "subject\n")
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
		if !strings.Contains(string(body), shellword.Quote(chain)) {
			t.Errorf("%s does not name the hook directory it chains to:\n%s", name, body)
		}
		if stamps := strings.Contains(string(body), "interpret-trailers"); stamps != (name == stampHookName) {
			t.Errorf("%s stamps = %v, want %v", name, stamps, name == stampHookName)
		}
	}
}

// TestWriteStampHooksRefusesItsOwnDirectory: a chain directory that is the
// stamp directory itself makes every hook exec itself, and a commit that never
// comes back. It is refused rather than written, and it is not a state a
// caller has to invent: reading the repository's core.hooksPath with the
// stamp's environment in place reads the stamp directory, because the stamp's
// own entry is one.
func TestWriteStampHooksRefusesItsOwnDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stamp-hooks")
	for _, tc := range []struct{ name, chainTo string }{
		{"the directory", dir},
		{"a trailing separator", dir + string(filepath.Separator)},
		{"a path that cleans to it", filepath.Join(dir, "..", filepath.Base(dir))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := WriteStampHooks(dir, tc.chainTo); err == nil {
				t.Errorf("WriteStampHooks(%q, %q) = nil, want an error: the chain would exec the stamp itself", dir, tc.chainTo)
			}
			if _, err := os.Stat(filepath.Join(dir, stampHookName)); err == nil {
				t.Error("the hooks were written anyway")
			}
		})
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
// not run, and the hooks it points at still run: the stamp is added to the
// repository's hooks and does not replace them.
func TestStampEnvOutranksRepositoryHooksPath(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	marker := filepath.Join(t.TempDir(), "ran")
	repoHooks := t.TempDir()
	testutil.WriteFiles(t, repoHooks, map[string]string{
		"prepare-commit-msg": "#!/bin/sh\nprintf '%s\\n' repo-hook >> " + shellword.Quote(marker) + "\n",
	})
	testutil.Git(t, dir, "config", "core.hooksPath", repoHooks)

	hooks := filepath.Join(t.TempDir(), "stamp-hooks")
	if err := WriteStampHooks(hooks, ""); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	msg := commitStamp(t, dir, StampEnv(runStamp(), hooks, nil), "subject\n")
	if want := []string{"run-1"}; !slices.Equal(trailersOf(msg)["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the repository's core.hooksPath won", trailersOf(msg)["fugaro-run"], want)
	}
	if ran, err := os.ReadFile(marker); err != nil || string(ran) != "repo-hook\n" {
		t.Errorf("the repository's own hook directory ran %q, %v, want it once: the stamp's core.hooksPath outranks the repository's, and the hooks it names are chained to", ran, err)
	}
}

// TestWriteStampHooksNoChainIsDistinctFromResolveLater: the two calls write
// different hooks. WriteStampHooks with no chainTo looks the repository's own
// hook directory up at the commit, which is what keeps a repository whose
// hooks live in $GIT_DIR/hooks running (constraint 2);
// WriteStampHooksNoChain chains nowhere, which is what makes a runner-owned
// commit run no code of the checkout's (A3, follow-up 2a). One string cannot
// mean both, so the two are separate calls.
//
// Mutation (make WriteStampHooksNoChain write the resolve-later hook): the
// planted pre-commit runs on the no-chain commit, and this test fails with its
// marker file present.
func TestWriteStampHooksNoChainIsDistinctFromResolveLater(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	marker := filepath.Join(t.TempDir(), "ran")
	planted := filepath.Join(t.TempDir(), "planted")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, "pre-commit"), []byte("#!/bin/sh\necho ran >> "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "config", "core.hooksPath", planted)

	noChain := filepath.Join(t.TempDir(), "no-chain")
	if err := WriteStampHooksNoChain(noChain); err != nil {
		t.Fatal("WriteStampHooksNoChain:", err)
	}
	commitStamp(t, dir, StampEnv(runStamp(), noChain, nil), "no chain")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a runner-owned commit ran the repository's own pre-commit: %v", err)
	}

	later := filepath.Join(t.TempDir(), "later")
	if err := WriteStampHooks(later, ""); err != nil {
		t.Fatal("WriteStampHooks:", err)
	}
	left, err := os.ReadFile(filepath.Join(noChain, "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(filepath.Join(later, "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(left, right) {
		t.Error("no-chain and resolve-later write the same hook")
	}
	commitStamp(t, dir, StampEnv(runStamp(), later, nil), "resolve later")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the resolve-later hook did not run the repository's own pre-commit: %v", err)
	}
}

// TestRunnerOwnCommitsRunNoRepositoryHook: a commit the runner makes runs no
// hook of the checkout's, wherever the checkout keeps them — $GIT_DIR/hooks,
// which git uses by default, and a core.hooksPath of the repository's own in
// .git/config, which the agent can write. noHooks on every runner-owned git
// call was that boundary, and a stamped runner commit must keep it: the stamp
// directory replaces noHooks, so it is a directory that chains nowhere.
//
// Mutation (give the runner the agent's chaining hook directory): both planted
// hooks run, and this test fails with its marker file present.
func TestRunnerOwnCommitsRunNoRepositoryHook(t *testing.T) {
	repo, _ := setup(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\necho ran >> " + marker + "\n"
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(t.TempDir(), "configured-hooks")
	for _, root := range []string{filepath.Join(gitDir, "hooks"), configured} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"prepare-commit-msg", "pre-commit", "post-commit"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	testutil.Git(t, repo.Dir, "config", "core.hooksPath", configured)

	hooks := filepath.Join(t.TempDir(), "runner-hooks")
	if err := WriteStampHooksNoChain(hooks); err != nil {
		t.Fatal("WriteStampHooksNoChain:", err)
	}
	repo.CommitHooks, repo.CommitStamp = hooks, func() Stamp { return runStamp() }
	if err := repo.CommitEmpty(ctx, "the runner's own"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a runner-owned commit ran a hook of the repository's: %v", err)
	}
	got := trailersOf(testutil.Git(t, repo.Dir, "log", "-1", "--format=%B"))
	if want := []string{"run-1"}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the runner's own commit was not stamped", got["fugaro-run"], want)
	}
}

// TestInterpretTrailersIgnoresRepositoryTrailerCommand: the stamp reads its
// trailers from the environment and writes them under the rules it states on
// its own command line, with no configuration of any other file behind them.
// trailer.<token>.command and trailer.<token>.cmd are commands git runs and
// whose output replaces the trailer's value, and .git/config is a file the
// run's agent can write: a stamp that ran inside the checkout would let the
// agent decide what the run's trailers say. The machine's own file and the
// environment are the other two sources, and three tests below hold those.
//
// Mutation (run interpret-trailers without unsetting GIT_DIR and cd-ing out of
// the repository): the planted command runs and blanks the value.
func TestInterpretTrailersIgnoresRepositoryTrailerCommand(t *testing.T) {
	dir, _, env := newStampRepo(t, runStamp(), "")
	marker := filepath.Join(t.TempDir(), "ran")
	testutil.Git(t, dir, "config", "trailer.fugaro-run.command", "sh -c 'echo ran >> "+marker+"; echo blanked'")
	testutil.Git(t, dir, "config", "trailer.fugaro-run.ifexists", "replace")
	msg := commitStamp(t, dir, env, "subject")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the repository's trailer command ran: %v", err)
	}
	if got := trailersOf(msg)["fugaro-run"]; !slices.Equal(got, []string{"run-1"}) {
		t.Errorf("fugaro-run = %v, want [run-1]: the repository's configuration decided the trailer", got)
	}
}

// trailerCommandScript writes the command a planted trailer configuration
// names: one that leaves a marker where a test can see it and answers the
// trailer with a value of its own, which is what trailer.<token>.command does
// to a stamp that reads it.
func trailerCommandScript(t *testing.T) (script, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran")
	testutil.WriteFiles(t, dir, map[string]string{
		"trailer-command": "#!/bin/sh\nprintf '%s\\n' ran >> " + shellword.Quote(marker) + "\nprintf '%s\\n' blanked\n",
	})
	return filepath.Join(dir, "trailer-command"), marker
}

// trailerCommandConfig is a git configuration file whose Fugaro-Run trailer is
// the output of a command: the two keys the stamp must never be steered by,
// stated as a file so a global and a system configuration can hold the same
// thing.
func trailerCommandConfig(script string) string {
	return "[trailer \"fugaro-run\"]\n\tcommand = " + script + "\n"
}

// stampCommitEnv is the whole environment a stamped commit is made under in
// the tests that plant a global or a system git configuration. Nothing of this
// process's own reaches git — testutil.IsolateGit's GIT_CONFIG_GLOBAL=/dev/null
// and GIT_CONFIG_NOSYSTEM=1 among it — because a test asking which
// configuration the stamp reads has to say what git's environment is, and the
// test process's own would answer for the machine before the stamp could.
func stampCommitEnv(home string, extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "xdg"),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	}
	return append(env, extra...)
}

// stampGitIsolated runs git in dir with env as its whole environment, where
// stampGit appends env to this process's.
func stampGitIsolated(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// commitStampIsolated is commitStamp over stampGitIsolated.
func commitStampIsolated(t *testing.T, dir string, env []string, msg string, extra ...string) string {
	t.Helper()
	args := append([]string{"commit", "--allow-empty", "--quiet"}, extra...)
	if out, err := stampGitIsolated(t, dir, env, append(args, "-m", msg)...); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return testutil.Git(t, dir, "log", "-1", "--format=%B")
}

// TestInterpretTrailersIgnoresGlobalTrailerCommand: the trailer step reads the
// user's own git configuration no more than it reads the repository's.
// trailer.<token>.command is a command git runs and whose output becomes the
// trailer's value, and ~/.gitconfig is a file the run's user writes — the
// run's agent is that user, which is SECURITY.md's "What one run's agent can
// reach" — so a stamp that read it would let the agent decide what the run's
// trailers say.
//
// Mutation (run interpret-trailers without GIT_CONFIG_GLOBAL=/dev/null): the
// planted command runs and blanks the value.
func TestInterpretTrailersIgnoresGlobalTrailerCommand(t *testing.T) {
	dir, _, stampEnv := newStampRepo(t, runStamp(), "")
	script, marker := trailerCommandScript(t)
	home := t.TempDir()
	testutil.WriteFiles(t, home, map[string]string{".gitconfig": trailerCommandConfig(script)})
	// GIT_CONFIG_NOSYSTEM here keeps the machine's own file out of a test
	// about the user's: a host whose /etc/gitconfig planted the same trailer
	// command would fail this for a reason the change is not about.
	env := stampCommitEnv(home, "GIT_CONFIG_NOSYSTEM=1")
	msg := commitStampIsolated(t, dir, append(env, stampEnv...), "subject")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the user's git config's trailer command ran: %v", err)
	}
	if got := trailersOf(msg)["fugaro-run"]; !slices.Equal(got, []string{"run-1"}) {
		t.Errorf("fugaro-run = %v, want [run-1]: the user's configuration decided the trailer", got)
	}
}

// TestInterpretTrailersIgnoresSystemConfig: the same for the system
// configuration, which git reads from wherever GIT_CONFIG_SYSTEM names — a
// file the run's user can write, since an environment variable of the git
// call is theirs to set, and /etc/gitconfig besides on a machine that lets
// them write it. GIT_CONFIG_NOSYSTEM is what makes git read no system file at
// all; unsetting GIT_CONFIG_SYSTEM only puts the machine's own back.
//
// Mutation (run interpret-trailers with the caller's GIT_CONFIG_SYSTEM in
// place and no GIT_CONFIG_NOSYSTEM): the planted command runs and blanks the
// value.
//
// Neither line's own removal is red here: git reads no file GIT_CONFIG_SYSTEM
// names while GIT_CONFIG_NOSYSTEM is set, and the unset takes out the only
// system file a hermetic test can plant. What GIT_CONFIG_NOSYSTEM alone still
// holds git away from is the machine's own /etc/gitconfig, a file this test
// cannot write — and one whose trailer command a run on a machine that let the
// agent write it would otherwise carry into every commit the stamp makes.
func TestInterpretTrailersIgnoresSystemConfig(t *testing.T) {
	dir, _, stampEnv := newStampRepo(t, runStamp(), "")
	script, marker := trailerCommandScript(t)
	system := filepath.Join(t.TempDir(), "system-gitconfig")
	if err := os.WriteFile(system, []byte(trailerCommandConfig(script)), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(stampCommitEnv(t.TempDir()), "GIT_CONFIG_SYSTEM="+system)
	msg := commitStampIsolated(t, dir, append(env, stampEnv...), "subject")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the system git config's trailer command ran: %v", err)
	}
	if got := trailersOf(msg)["fugaro-run"]; !slices.Equal(got, []string{"run-1"}) {
		t.Errorf("fugaro-run = %v, want [run-1]: the system configuration decided the trailer", got)
	}
}

// TestInterpretTrailersIgnoresEnvironmentConfig: configuration an environment
// carries is configuration too, and the environment of a commit's hook is the
// environment of the git the agent ran — an agent that sets
// GIT_CONFIG_PARAMETERS on its own `git commit` sets it for the trailer step
// that commit runs under. The global and system files are not the only ways
// into it; the pairs a GIT_CONFIG_COUNT numbers are the third, and
// TestInterpretTrailersIgnoresInjectedConfigPairs holds those.
//
// Mutation (leave the caller's environment configuration in the hook's
// subshell): the planted command runs and blanks the value.
func TestInterpretTrailersIgnoresEnvironmentConfig(t *testing.T) {
	dir, hooks, _ := newStampRepo(t, runStamp(), "")
	script, marker := trailerCommandScript(t)
	// GIT_CONFIG_NOSYSTEM for the same reason as the user's-file test: the
	// machine's own file is neither the vector here nor this test's to depend
	// on.
	env := append(stampCommitEnv(t.TempDir(), "GIT_CONFIG_NOSYSTEM=1"),
		"GIT_CONFIG_PARAMETERS='trailer.fugaro-run.command="+script+"'")
	env = StampEnv(runStamp(), hooks, env)
	msg := commitStampIsolated(t, dir, env, "subject")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the environment's trailer command ran: %v", err)
	}
	if got := trailersOf(msg)["fugaro-run"]; !slices.Equal(got, []string{"run-1"}) {
		t.Errorf("fugaro-run = %v, want [run-1]: the environment configuration decided the trailer", got)
	}
}

// TestInterpretTrailersIgnoresInjectedConfigPairs: the other environment
// configuration is the pairs a GIT_CONFIG_COUNT numbers — GIT_CONFIG_KEY_n
// with the GIT_CONFIG_VALUE_n of its n — and git reads a pair wherever it
// reads a value, so a pair naming trailer.fugaro-run.command is a command the
// trailer step of a commit runs. The hook's subshell takes the count out
// before it calls git, which is what leaves the pairs nothing to be read
// through; a count left in the environment is refused by git outright, which
// would fail the commit rather than steer it.
//
// Mutation (leave the caller's GIT_CONFIG_COUNT and its pairs in the hook's
// subshell): the planted command runs and blanks the value.
func TestInterpretTrailersIgnoresInjectedConfigPairs(t *testing.T) {
	dir, hooks, _ := newStampRepo(t, runStamp(), "")
	script, marker := trailerCommandScript(t)
	// GIT_CONFIG_NOSYSTEM for the same reason as the user's-file test: the
	// machine's own file is neither the vector here nor this test's to depend
	// on.
	env := append(stampCommitEnv(t.TempDir(), "GIT_CONFIG_NOSYSTEM=1"),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=trailer.fugaro-run.command",
		"GIT_CONFIG_VALUE_0="+script)
	env = StampEnv(runStamp(), hooks, env)
	msg := commitStampIsolated(t, dir, env, "subject")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the injected pair's trailer command ran: %v", err)
	}
	if got := trailersOf(msg)["fugaro-run"]; !slices.Equal(got, []string{"run-1"}) {
		t.Errorf("fugaro-run = %v, want [run-1]: the injected pair decided the trailer", got)
	}
}
