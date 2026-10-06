package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

func TestSetGCPProjectLine(t *testing.T) {
	in := "# fugaro config\nproject: belong\nworkflows:\n  web: {}\n"
	out, changed, err := setGCPProjectLine([]byte(in), "fugaro-belong")
	want := "# fugaro config\nproject: belong\ngcp_project: fugaro-belong\nworkflows:\n  web: {}\n"
	if err != nil || !changed || string(out) != want {
		t.Fatalf("%q %v %v", out, changed, err)
	}
	if _, ch, _ := setGCPProjectLine(out, "fugaro-belong"); ch {
		t.Error("second call changed the file")
	}
	if _, _, err := setGCPProjectLine(out, "fugaro-other"); err == nil {
		t.Error("a differing value was overwritten")
	}
	if _, _, err := setGCPProjectLine([]byte(in), "../x"); err == nil {
		t.Error("a bad id was accepted")
	}
	// project: as the last line, no trailing newline.
	out, ch, err := setGCPProjectLine([]byte("version: 1\nproject: belong"), "fugaro-belong")
	if err != nil || !ch || string(out) != "version: 1\nproject: belong\ngcp_project: fugaro-belong" {
		t.Errorf("%q %v %v", out, ch, err)
	}
	// CRLF files keep their line endings.
	out, _, err = setGCPProjectLine([]byte("project: b\r\nx: 1\r\n"), "fugaro-b")
	if err != nil || string(out) != "project: b\r\ngcp_project: fugaro-b\r\nx: 1\r\n" {
		t.Errorf("%q %v", out, err)
	}
	// No project line, or an empty gcp_project line: refused, not duplicated.
	if _, _, err := setGCPProjectLine([]byte("version: 1\n"), "fugaro-b"); err == nil {
		t.Error("no project: line accepted")
	}
	if _, _, err := setGCPProjectLine([]byte("project: b\ngcp_project:\n"), "fugaro-b"); err == nil {
		t.Error("an empty gcp_project line was accepted")
	}
}

func TestImagePredates(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{{"0.3.1", true}, {"v0.3.1", true}, {"0.4.0", false}, {"0.10.0", false}, {"1.0.0", false}, {"", true}, {"dev-abc", false}} {
		if got := imagePredates(c.v, "0.4.0"); got != c.want {
			t.Errorf("imagePredates(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// anchorRig is a checkout whose fugaro.yaml the repository stage edits, with
// a fake runs bucket holding build records.
func anchorRig(t *testing.T, lc *localcfg.Config, yaml string) (*repositoryStage, *syncBuf, string) {
	t.Helper()
	dir := repoCheckout(t, githubOrigin, yaml)
	t.Chdir(dir)
	e, out := stageEngine(t, "", nil)
	e.lc = lc
	s := newRepositoryStage(e)
	s.resolve(t.Context())
	if s.tg == nil {
		t.Fatalf("no target: %+v", s.st)
	}
	bkt, err := blobx.Open(t.Context(), "mem://")
	if err != nil {
		t.Fatal(err)
	}
	prev := openRecordBucket
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) { return bkt, nil }
	t.Cleanup(func() { openRecordBucket = prev })
	return s, out, dir
}

func putRecord(t *testing.T, s *repositoryStage, version string) {
	t.Helper()
	slug, _ := task.Slug("github", "acme/app")
	b, _ := openRecordBucket(t.Context(), "")
	data := `{"version":1,"fugaro_version":"` + version + `"}`
	if _, err := b.Create(t.Context(), imagecheck.RecordKey(slug, "app"), []byte(data), "application/json"); err != nil {
		t.Fatal(err)
	}
}

func anchorLC() *localcfg.Config {
	return &localcfg.Config{Name: "aurora", GCPProject: "fugaro-aurora", RunsBucket: "fugaro-runs-fugaro-aurora", Repos: map[string]localcfg.Repo{"acme/app": {Provider: "github"}}}
}

const warnFrag = "fugaro image build --repo acme/app --workflow app"

func TestRepoStageWritesGCPProject(t *testing.T) {
	yaml := checkoutYAML("github", "oauth", "aurora", "")
	s, out, dir := anchorRig(t, anchorLC(), yaml)
	putRecord(t, s, "0.3.1")
	if err := s.anchor(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if !strings.Contains(string(got), "project: aurora\ngcp_project: fugaro-aurora\n") {
		t.Errorf("not written:\n%s", got)
	}
	if o := out.String(); !strings.Contains(o, "+ gcp_project: fugaro-aurora") || !strings.Contains(o, warnFrag) || !strings.Contains(o, "BEFORE merging") {
		t.Errorf("output:\n%s", o)
	}
}

func TestRepoStageGCPProjectNoWarnWhenImageCurrent(t *testing.T) {
	s, out, _ := anchorRig(t, anchorLC(), checkoutYAML("github", "oauth", "aurora", ""))
	putRecord(t, s, "0.4.0")
	if err := s.anchor(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "image build") {
		t.Errorf("warned about a current image:\n%s", out)
	}
}

func TestRepoStageGCPProjectNoRecordWarns(t *testing.T) {
	s, out, _ := anchorRig(t, anchorLC(), checkoutYAML("github", "oauth", "aurora", ""))
	if err := s.anchor(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), warnFrag) {
		t.Errorf("no warning without a record:\n%s", out)
	}
}

func TestRepoStageGCPProjectPlanOnlyWritesNothing(t *testing.T) {
	yaml := checkoutYAML("github", "oauth", "aurora", "")
	s, out, dir := anchorRig(t, anchorLC(), yaml)
	if err := s.anchor(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if string(got) != yaml {
		t.Errorf("plan-only wrote:\n%s", got)
	}
	if !strings.Contains(out.String(), "+ gcp_project: fugaro-aurora") {
		t.Errorf("no diff printed:\n%s", out)
	}
}

func TestRepoStageGCPProjectCustomBucketSkips(t *testing.T) {
	lc := anchorLC()
	lc.RunsBucket = "my-own-bucket"
	yaml := checkoutYAML("github", "oauth", "aurora", "")
	s, out, dir := anchorRig(t, lc, yaml)
	if err := s.anchor(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if string(got) != yaml || !strings.Contains(out.String(), "default runs-bucket name") {
		t.Errorf("custom bucket: wrote or no note:\n%s\n%s", got, out)
	}
}

func TestRepoStageGCPProjectDiffersRefused(t *testing.T) {
	yaml := strings.Replace(checkoutYAML("github", "oauth", "aurora", ""), "project: aurora\n", "project: aurora\ngcp_project: fugaro-other\n", 1)
	s, _, dir := anchorRig(t, anchorLC(), yaml)
	err := s.anchor(t.Context(), false)
	if err == nil || !strings.Contains(err.Error(), "gcp_project") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml")); string(got) != yaml {
		t.Error("file changed")
	}
}

func TestRepoStageGCPProjectAlreadySetIsQuiet(t *testing.T) {
	yaml := strings.Replace(checkoutYAML("github", "oauth", "aurora", ""), "project: aurora\n", "project: aurora\ngcp_project: fugaro-aurora\n", 1)
	s, out, _ := anchorRig(t, anchorLC(), yaml)
	if err := s.anchor(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "image build") || strings.Contains(out.String(), "+ ") {
		t.Errorf("not quiet:\n%s", out)
	}
}
