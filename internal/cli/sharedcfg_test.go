package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

func sharedObjectPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(infra.SharedConfigObject))
}

func TestPublishSharedWritesAndOverwrites(t *testing.T) {
	f := newCloudFixture(t)
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	path := sharedObjectPath(filepath.Join(f.dir, "runs"))
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := lc.Shared().Marshal()
	if string(got) != string(want) {
		t.Errorf("published:\n%s\nwant:\n%s", got, want)
	}
	if _, err := localcfg.Parse(got); err != nil {
		t.Errorf("the published file does not parse: %v", err)
	}
	for _, s := range []string{"user:", "endpoints:", "bucket_url"} {
		if strings.Contains(string(got), s) {
			t.Errorf("published file contains %q", s)
		}
	}
	lc.MaxParallel = 9
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if !strings.Contains(string(got), "max_parallel: 9") {
		t.Errorf("the second publish did not overwrite:\n%s", got)
	}
}

func TestPublishSharedTooLarge(t *testing.T) {
	newCloudFixture(t)
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	lc.LogView = strings.Repeat("x", localcfg.SharedMaxBytes)
	if err := publishShared(context.Background(), lc); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v, want the size limit", err)
	}
}

// sharedRuns points the publisher at a file:// bucket of the test's own and
// returns its directory.
func sharedRuns(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+dir)
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	return dir
}

func TestInitConfigOnlyPublishesTheSharedConfig(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	dir := sharedRuns(t)
	if _, _, err := executeStdin(t, "", "init", "--config-only", "--yes"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatalf("not published: %v", err)
	}
	if lc, err := localcfg.Parse(data); err != nil || lc.Name != initProjectName || lc.Terraform.StateBucket != "" {
		t.Errorf("published %+v, %v:\n%s", lc, err, data)
	}
}

func TestInitPublishConfigAlone(t *testing.T) {
	r := newInitRig(t)
	dir := sharedRuns(t)
	out, _, err := executeStdin(t, "", "init", "--publish-config")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedObjectPath(dir)); err != nil {
		t.Fatalf("not published: %v\n%s", err, out)
	}
	if len(r.calls(t)) != 0 {
		t.Errorf("--publish-config ran terraform: %q", r.calls(t))
	}
}

func TestInitPublishConfigNeedsALocalConfig(t *testing.T) {
	r := newInitRig(t)
	sharedRuns(t)
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", "")
	_, _, err := executeStdin(t, "", "init", "--publish-config", "--name", "aurora")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestInitPublishConfigExcludesTheOtherModes(t *testing.T) {
	newInitRig(t)
	for _, other := range []string{"--config-only", "--plan-only", "--print-vars", "--forget"} {
		_, _, err := executeStdin(t, "", "init", "--publish-config", other)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--publish-config") || !strings.Contains(err.Error(), "exclude one another") {
			t.Errorf("%s: exit %d, err %v", other, ExitCode(err), err)
		}
	}
}

func TestInitPublishFailureWarnsAndSucceeds(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	dir := sharedRuns(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("the directory is writable despite its mode (running as root)")
	}
	out, _, err := executeStdin(t, "", "init", "--config-only", "--yes")
	if err != nil {
		t.Fatalf("a publish failure must not fail init: %v", err)
	}
	if !strings.Contains(out, "warning: could not publish the shared config: ") || !strings.Contains(out, "teammates will need fugaro init until it is published") {
		t.Errorf("no warning:\n%s", out)
	}
}
