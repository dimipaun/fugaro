package cli

// U10 of docs/update-from-anywhere: `update --check` reads the project's own
// repositories through C1's view, at U4's offline staleness rule, instead of
// one checkout at PATH (R17, D3349 as amended). The mode still answers what it
// always did — one line per step, exit 1 when anything is stale — and it takes
// no PATH.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// checkFixtureYAML is the fugaro.yaml every view of the tests below carries:
// one workflow, whose kind is the only thing the staleness rule reads.
const checkFixtureYAML = "version: 1\nproject: aurora\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// checkFixtureConfig is this machine's config for those views: a released
// installation with no base image recorded, so every kind it reads is stale.
func checkFixtureConfig() *localcfg.Config {
	return &localcfg.Config{Name: "aurora", GCPProject: "juno-prod", Region: "europe-west1"}
}

// checkFixtureView is one repository as the mode reads it, parsed the way the
// reader answers (C1's read shape).
func checkFixtureView(repo string) repoView {
	return repoView{Repo: repo, Provider: "github", Branch: "main", Head: "abc123",
		YAML: []byte(checkFixtureYAML), Cfg: &config.Config{Workflows: map[string]config.Workflow{"app": {}}}}
}

// TestUpdateCheckTakesNoPath: the mode reads the project's own list, so a
// positional argument is refused by name rather than read as a checkout (U10,
// R17). Nothing is printed for it: the refusal is before the header.
func TestUpdateCheckTakesNoPath(t *testing.T) {
	convergeEnv(t, "0.5.2")
	out, _, err := execute(t, "update", "--check", "/some/checkout")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "takes no PATH") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if strings.Contains(out, "== ") {
		t.Errorf("the mode read something before refusing the PATH:\n%s", out)
	}
}

// TestUpdateCheckReadsTheLocalList: the cloud step is one verdict per enrolled
// repository, each from its own view — a read one at the offline staleness
// rule, an unread one named with its reason, never guessed at (R13) — and the
// one step line says the same from every directory. The list comes through
// C1's reader with the machine's own layer file and no project layer, which is
// what keeps the parse off the runs bucket and the published policy (R17,
// D3349).
func TestUpdateCheckReadsTheLocalList(t *testing.T) {
	convergeEnv(t, "0.5.2")
	lc := checkFixtureConfig()
	var gotIn enrolledIn
	stubEnrolledRepos(t, func(_ context.Context, in enrolledIn) ([]repoView, []string) {
		gotIn = in
		stale := checkFixtureView("acme/stale")
		unread := repoView{Repo: "acme/unread", Provider: "github", Unread: "no fugaro.yaml on the default branch"}
		return []repoView{stale, unread}, nil
	})
	var w strings.Builder
	u := &stepCtx{w: &w, check: &checkLocal{lc: lc, path: "/cfg/aurora.yaml"}}
	r := checkCloudStep(context.Background(), u)
	if r.state != stepStale {
		t.Fatalf("cloud = %s (%s), want stale: one repository's base image is not this release's", r.state, r.reason)
	}
	for _, want := range []string{
		"2 repositories: 1 stale: 1 unread",
		"acme/stale: base images to move",
		"acme/unread: no fugaro.yaml on the default branch",
	} {
		if !strings.Contains(r.reason, want) {
			t.Errorf("the cloud step says %q, which lacks %q", r.reason, want)
		}
	}
	// The read is the mode's own: this machine's config, the machine's own
	// layer file in place of the project layer, so readEnrolledAdmits reads no
	// policy and no bucket (R17).
	if gotIn.LC != lc {
		t.Errorf("the reader got %v, want this machine's config", gotIn.LC)
	}
	if gotIn.Layer != nil {
		t.Error("the mode read a project layer: the check reads no bucket")
	}
	wantLayer, err := localcfg.LayerFile(os.Getenv, lc.Name)
	if err != nil {
		t.Fatal(err)
	}
	if gotIn.LayerFile != wantLayer || filepath.Base(gotIn.LayerFile) != lc.Name+".layer.yaml" {
		t.Errorf("the reader got layer file %q, want the machine's own %q", gotIn.LayerFile, wantLayer)
	}
}

// TestUpdateCheckReadsNothingOutsideAProject: a machine whose checkout selects
// no project still answers the plugin step — it is this machine's — and says
// its cloud step read no list rather than reporting a repository it never
// read (R13, R17).
func TestUpdateCheckReadsNothingOutsideAProject(t *testing.T) {
	convergeEnv(t, "0.5.2")
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		t.Error("the mode read repositories with no local project config selecting any")
		return nil, nil
	})
	var w strings.Builder
	u := &stepCtx{w: &w, check: &checkLocal{why: "no local project config selects this checkout"}}
	r := checkCloudStep(context.Background(), u)
	if r.state != stepSkipped || !strings.Contains(r.reason, "no local project config selects this checkout") {
		t.Fatalf("cloud = %s (%s), want skipped with why", r.state, r.reason)
	}
}
