package runner_test

// D3327's writing half as whole runs (F19, G3, R5): a `file: true` secret is
// written to a file of the run's own, mode 0400, under the temporary directory
// the container's tmpfs is, and the variable the declaration names is set to
// that path instead of to the value. What is asserted here is what a run can be
// read for afterwards -- the path the stage got, the file's mode and content
// while the run is going on, and what is left of it when the run ends -- and
// never the writing helper on its own.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// secretFileTempDir gives the case its own temporary directory and points the
// runner at it: both halves of the file exposure read os.TempDir (MkdirTemp
// makes the run's directory under it and removeSecretFiles refuses to remove
// anything that is not directly under it), so a case about what the run left
// behind has to read the directory the run was given.
func secretFileTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

// secretRoots are the directories the run made for its file secrets under dir
// (secretfile.go's fugaro-secrets-), in the order the filesystem lists them.
func secretRoots(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "fugaro-secrets-") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// TestSecretFileIsMode0400OnTmpfs is D3327's red step: the variable holds the
// secret's own value today, and with the file exposure it holds the path of a
// 0400 file under the temporary directory whose content is that value.
//
// The file is read while the run's stage is going on, which is the only moment
// it exists (the run removes it at its end) and the moment the declaration is
// about: a tool the stage runs reads the value out of the file the variable
// names.
//
// Hand mutation this fails on: writing the file where nobody reads it (the
// variable keeps the value), a mode that is not 0400, or a file whose content
// is not the answer.
func TestSecretFileIsMode0400OnTmpfs(t *testing.T) {
	tmp := secretFileTempDir(t)
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := fileSecretsRun(t, "  first_line_review: off\n", nil, svc)

	rec, err := b.run(t, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		path := envValue(req.Env, secretEnvVar)
		if path == fetchedSecretValue {
			t.Fatal("the agent's environment carries the secret's value: a file secret's variable is the path its value was written to (D3327)")
		}
		if !strings.HasPrefix(path, tmp+string(os.PathSeparator)) {
			t.Fatalf("%s=%q, want a path under the temporary directory", secretEnvVar, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("the file %s names does not exist: %v", secretEnvVar, err)
		}
		if got := info.Mode().Perm(); got != 0o400 {
			t.Errorf("the file's mode is %04o, want 0400", got)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != fetchedSecretValue {
			t.Errorf("the file holds %q, want the value the service answered", content)
		}
		return implement("feature")(t, ctx, req)
	}, review("ship", 0))
	mustReady(t, rec, err)
}

// TestSecretFileRedactedBeforeWrite: the value is registered with the redactor
// before the file exists (G3), and the file is a place the value is written --
// so a run that published it from there would leak a credential the run's own
// log, record, bucket and pull request are all supposed to be free of. The
// value here is the one the JOB MOUNTED, which no fetch ever registered: the
// writer's own registration is the whole of what puts it in the redactor.
//
// The stage reads the file and publishes what it holds, which is the ordinary
// shape of an accident. The redaction is asserted to have fired -- [REDACTED] is
// in the log -- so a mutation that stopped publishing the stage's output cannot
// pass this by publishing nothing.
//
// Hand mutation this fails on: writing before registering (the hook sees
// registered false), or not registering at all (the value is in the log).
func TestSecretFileRedactedBeforeWrite(t *testing.T) {
	secretFileTempDir(t)
	var hooked int
	runner.SetSecretFileHook(t, func(value string, registered bool) {
		hooked++
		if value != mountedSecretValue {
			t.Errorf("the file was written from %q, want the value the job mounted", value)
		}
		if !registered {
			t.Error("the value was written before the redactor held it (G3)")
		}
	})
	svc := &secretsService{}
	b := fileSecretsRun(t, "  first_line_review: off\n", nil, svc)
	// The mount is the environment entry the deploy leaves, which is the file
	// the fixture's own tests read.
	b.deps.Env = append(b.deps.Env, secretEnvVar+"="+mountedSecretValue)

	rec, err := b.run(t, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		content, err := os.ReadFile(envValue(req.Env, secretEnvVar))
		if err != nil {
			t.Fatalf("the stage cannot read the file its variable names: %v", err)
		}
		_, _ = fmt.Fprintf(req.Transcript, "{\"leaked\":%q}\n", content)
		_, _ = fmt.Fprintf(req.Stderr, "claude: %s\n", content)
		res, err := implement("feature")(t, ctx, req)
		res.Text = "done, the file holds " + string(content)
		return res, err
	}, review("ship", 0))
	mustReady(t, rec, err)
	if hooked != 1 {
		t.Errorf("the writer of a file secret ran %d times, want once: the repository declares one", hooked)
	}
	logs := b.logs.String()
	if strings.Contains(logs, mountedSecretValue) {
		t.Fatalf("the runner's log publishes the value of a file secret:\n%s", logs)
	}
	if !strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("nothing was redacted, so this test proves nothing:\n%s", logs)
	}
	if keys := objectsContaining(t, b.harness, mountedSecretValue); len(keys) > 0 {
		t.Fatalf("the value of a file secret is stored in %v", keys)
	}
}

// TestFileSecretKeepsTheShortValueRefusal: a file secret is the environment
// secret it stands in for (F19), so the floor under a value a run may redact is
// the same one -- a 3-byte value is refused at bootstrap whether or not the
// declaration says `file: true`, and the file exposure is no way around it. A
// mounted value is the case that reaches here: the fetch refuses a short answer
// of the service's on its own (workflowsecrets.go).
//
// Hand mutation this fails on: skipping the floor for a file secret (the run
// reaches its stages and writes the value to a file the declaration was never
// allowed to hold).
func TestFileSecretKeepsTheShortValueRefusal(t *testing.T) {
	tmp := secretFileTempDir(t)
	svc := &secretsService{}
	b := fileSecretsRun(t, "  first_line_review: off\n", nil, svc)
	// The job mounts a value too short to redact safely under the variable the
	// file secret's declaration names; nothing is fetched, because a mounted
	// value wins (F7).
	b.deps.Env = append(b.deps.Env, secretEnvVar+"="+shortSecretValue)

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	want := fmt.Sprintf("secret %s is shorter than %d bytes, so it cannot be redacted safely", secretEnvVar, minFetchedSecretLen)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want the refusal the same declaration without `file: true` meets (rec = %s)", err, rec.Status)
	}
	if len(b.agent.calls) != 0 {
		t.Errorf("the agent was called %d times, want none: the run stopped at bootstrap", len(b.agent.calls))
	}
	if roots := secretRoots(t, tmp); len(roots) != 0 {
		t.Errorf("a value that cannot be redacted safely was written to a file anyway: %v", roots)
	}
}

// TestTwoFileSecretsSharingANameGetTheirOwnFile: a workflow may declare the
// same logical secret twice, under two variables (config's Validate refuses a
// repeated env and no other repetition), and both entries may be exposed as
// files -- one secret a repository mounts into two tools. The file a
// declaration gets is the one its VARIABLE names, so each variable is the path
// of a file holding its own value: keyed by the logical name instead, the
// second write would meet the first's file and stop the run at bootstrap
// (O_EXCL), and keyed by anything the two declarations share, one variable
// would be handed the other's value.
//
// Hand mutation this fails on: keying the file by the secret's logical name (the
// run stops on the second declaration and never reaches a stage), or writing
// both declarations to one file (one variable reads the other's value).
func TestTwoFileSecretsSharingANameGetTheirOwnFile(t *testing.T) {
	secretFileTempDir(t)
	svc := &secretsService{}
	b := secretsRunConfig(t, twoFileSecretsConfig(t, "  first_line_review: off\n"), nil, svc)
	// A job that mounts both variables: the values are the job's own, so the two
	// files cannot be one file written twice with one value in it.
	b.deps.Env = append(b.deps.Env, secretEnvVar+"="+mountedSecretValue, secondFileEnvVar+"="+secondFileSecretValue)

	var first, second string
	rec, err := b.run(t, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		first = envValue(req.Env, secretEnvVar)
		second = envValue(req.Env, secondFileEnvVar)
		for _, c := range []struct{ env, path, want string }{
			{secretEnvVar, first, mountedSecretValue},
			{secondFileEnvVar, second, secondFileSecretValue},
		} {
			content, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatalf("%s names %q, which cannot be read: %v", c.env, c.path, err)
			}
			if string(content) != c.want {
				t.Errorf("%s reads %q, want the value the job mounted under that variable", c.env, content)
			}
		}
		return implement("feature")(t, ctx, req)
	}, review("ship", 0))
	mustReady(t, rec, err)
	if first == "" || second == "" || first == second {
		t.Fatalf("%s=%q and %s=%q, want the path of a file each", secretEnvVar, first, secondFileEnvVar, second)
	}
}

// TestSecretFileRemovedAtRunEnd: the run takes the file out of the temporary
// directory when it ends, root and all (G3) -- a value a finished run left on
// disk is one the next thing in that container can read.
//
// Hand mutation this fails on: no removal at run end (the file and its
// directory are still there).
func TestSecretFileRemovedAtRunEnd(t *testing.T) {
	tmp := secretFileTempDir(t)
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	b := fileSecretsRun(t, "  first_line_review: off\n", nil, svc)

	var path string
	rec, err := b.run(t, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		path = envValue(req.Env, secretEnvVar)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the file is not there while the run's stage is: %v", err)
		}
		return implement("feature")(t, ctx, req)
	}, review("ship", 0))
	mustReady(t, rec, err)
	if path == "" || path == fetchedSecretValue {
		t.Fatalf("%s=%q during the run, want the path of a file", secretEnvVar, path)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file %s is still there after the run: stat = %v", path, err)
	}
	if roots := secretRoots(t, tmp); len(roots) != 0 {
		t.Errorf("the run left %v behind", roots)
	}
}

// TestReviewRunGetsNoSecretFile: an independent review run reads an untrusted
// checkout and prompt, and it is the one run whose agent gets no workflow secret
// at all -- so a file of the run's would be a value that code could read with
// nothing standing between it and the pull request it writes (F19, G3, R5). The
// run fetches the value its own registration needs and leaves it in the
// environment; no file is ever written.
//
// The check is made while the review stage is going on, which is the moment a
// wrongly written file would exist: a check after the run cannot tell one that
// was never written from one that was written and then removed.
//
// Hand mutation this fails on: exposing files whatever the run is (the
// directory is there during the review stage).
func TestReviewRunGetsNoSecretFile(t *testing.T) {
	tmp := secretFileTempDir(t)
	svc := &secretsService{names: map[string]string{secretName: fetchedSecretValue}}
	spec := reviewTaskSpec("reviewonly", reviewOnlyYAML, 7)
	// The workflow the task names is what places a job run in the job it is in
	// (gcp.JobName of the slug and that workflow), and a review-only spec carries
	// a recipe instead: without this the run stops before the fetch, on a job it
	// cannot place.
	spec.Workflow = "app"
	b := fileSecretsRun(t, "", spec, svc)
	pushReviewBranch(t, b.harness, "someone-elses-change", func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
	})
	addPR(b.harness, 7, "someone-elses-change", "")

	rec, err := b.run(t, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if roots := secretRoots(t, tmp); len(roots) != 0 {
			t.Errorf("the review run wrote a file secret: %v (F19, G3)", roots)
		}
		for _, kv := range req.Env {
			if strings.Contains(kv, fetchedSecretValue) {
				t.Errorf("the review agent's environment carries the fetched secret: %q", kv)
			}
		}
		return review("changes", 1)(t, ctx, req)
	})
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReviewed {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := svc.askedNames(); len(got) != 1 || got[0] != secretName {
		t.Fatalf("the service was asked for %v, want %s: a review run registers the value it fetches for redaction", got, secretName)
	}
	if roots := secretRoots(t, tmp); len(roots) != 0 {
		t.Errorf("the review run left %v behind", roots)
	}
}
