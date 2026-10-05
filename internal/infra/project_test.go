package infra

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// Both names are in every job's environment, and the GCP ID is never
// under FUGARO_PROJECT.
func TestPlatformEnvNames(t *testing.T) {
	ws, err := Workflow(sandboxInputs(t, ""), "web")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Env["FUGARO_PROJECT"] != "aurora" || ws.Env["FUGARO_GCP_PROJECT"] != "proj-1234" {
		t.Errorf("env = %v", ws.Env)
	}
	// The installation's own name wins over the local config's (the two
	// are checked against each other before this).
	in := sandboxInputs(t, "")
	in.Installation.ProjectName = ""
	ws, err = Workflow(in, "web")
	if err != nil || ws.Env["FUGARO_PROJECT"] != "aurora" {
		t.Errorf("without outputs: %v, %v", ws.Env, err)
	}
}

func TestWithDefaultsNameFromLocalConfig(t *testing.T) {
	lc := parseLC(t, m4LocalConfig)
	o := withDefaults(InstallationOutputs{}, lc, "fugaro-runs-proj-1234")
	if o.ProjectName != "aurora" {
		t.Errorf("ProjectName = %q", o.ProjectName)
	}
	o = withDefaults(InstallationOutputs{ProjectName: "borealis"}, lc, "fugaro-runs-proj-1234")
	if o.ProjectName != "borealis" {
		t.Errorf("ProjectName = %q, the installation's own name must stay", o.ProjectName)
	}
}

// infra.Repo with no installation outputs still resolves a spec (image
// build, the local check, the in-cloud check job), and it is the outputs'
// caller, runInitRepo, that refuses an unnamed installation.
func TestRepoWithoutOutputsResolves(t *testing.T) {
	in := sandboxInputs(t, m5Additions)
	in.Installation = InstallationOutputs{}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Installation.ProjectName != "aurora" || rs.Workflows["web"].Env["FUGARO_PROJECT"] != "aurora" {
		t.Errorf("spec = %+v", rs.Installation)
	}
}

func TestRepoRefusesInstallationOfAnotherName(t *testing.T) {
	in := sandboxInputs(t, "")
	in.Installation.ProjectName = "borealis"
	_, err := Repo(in)
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "borealis") || !strings.Contains(err.Error(), "aurora") {
		t.Errorf("err = %v", err)
	}
}

func TestInstallationVarsCarryFugaroProject(t *testing.T) {
	s := installationSpec(t)
	if s.FugaroProject != "aurora" {
		t.Fatalf("FugaroProject = %q", s.FugaroProject)
	}
	vars, err := InstallationVars(s)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["fugaro_project"] != "aurora" || doc["project"] != "proj-1234" {
		t.Errorf("tfvars: fugaro_project %v, project %v", doc["fugaro_project"], doc["project"])
	}
}

func TestRepoVarsCarryFugaroProject(t *testing.T) {
	rs, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	vars, err := RepoVars(rs)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["fugaro_project"] != "aurora" || doc["project"] != "proj-1234" {
		t.Errorf("tfvars: fugaro_project %v, project %v", doc["fugaro_project"], doc["project"])
	}
}

func TestDiscoverRefusesForeignProjectLabel(t *testing.T) {
	inst := installationSpec(t)
	f := newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, with(managed, gcp.LabelProject, "borealis"))
	_, err := DiscoverInstallation(context.Background(), f.c, inst)
	var fe *ForeignError
	if !errors.As(err, &fe) || !strings.Contains(err.Error(), "fugaro_project=borealis") || !strings.Contains(err.Error(), "fugaro_project=aurora") {
		t.Fatalf("err = %v", err)
	}
	// A repository's discovery holds the same rule.
	spec := sandboxSpec(t)
	f = newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, with(managed, gcp.LabelProject, "borealis"))
	_, _, err = DiscoverRepo(context.Background(), f.c, spec)
	if !errors.As(err, &fe) || !strings.Contains(err.Error(), "fugaro_project=borealis") {
		t.Fatalf("repo err = %v", err)
	}
}

// A bucket an earlier apply made has no label; the plan adds it, so it is
// adopted, not refused.
func TestDiscoverAdoptsUnlabelledBucket(t *testing.T) {
	inst := installationSpec(t)
	f := newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, managed)
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	if got := importMap(im); got["module.installation.google_storage_bucket.runs"] == "" {
		t.Errorf("imports = %v", got)
	}
	f = newCloud(t)
	f.gcs.AddBucket(inst.RunsBucket, testProjectNumber, with(managed, gcp.LabelProject, "aurora"))
	if _, err := DiscoverInstallation(context.Background(), f.c, inst); err != nil {
		t.Errorf("a matching label: %v", err)
	}
}

// The M4 golden's env changed in M9a: the names are FUGARO_GCP_PROJECT
// and FUGARO_PROJECT, and the README says why.
func TestM4GoldenEnvRenamed(t *testing.T) {
	golden, rows := readGolden(t)
	env := anyMap(golden["env"].(map[string]any))
	if env["FUGARO_GCP_PROJECT"] != "proj-1234" || env["FUGARO_PROJECT"] != "aurora" {
		t.Errorf("golden env = %v", env)
	}
	if !strings.Contains(rows["env"], "FUGARO_GCP_PROJECT=proj-1234") || !strings.Contains(rows["env"], "FUGARO_PROJECT=aurora") {
		t.Errorf("golden env row = %q", rows["env"])
	}
	readme, err := os.ReadFile("testdata/README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "the env names changed in M9a: FUGARO_PROJECT is the project name, the GCP ID is FUGARO_GCP_PROJECT") {
		t.Error("testdata/README.md has no note about the renamed env names")
	}
}

func TestReadProjectMarker(t *testing.T) {
	ctx := context.Background()
	write := func(f *cloud, bucket, body string) {
		t.Helper()
		if err := f.gcs.Bucket(t, bucket).Bucket.WriteAll(ctx, ProjectMarkerObject, []byte(body), nil); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		seed func(f *cloud)
		want *ProjectMarker
	}{
		"no bucket": {func(f *cloud) {}, nil},
		"no object": {func(f *cloud) { f.gcs.AddBucket("runs", testProjectNumber, managed) }, nil},
		"marker": {func(f *cloud) {
			f.gcs.AddBucket("runs", testProjectNumber, managed)
			write(f, "runs", `{"version":1,"name":"aurora","gcp_project":"proj-1234"}`)
		}, &ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"}},
		"a perfect mark in another project's bucket": {func(f *cloud) {
			f.gcs.AddBucket("runs", 999, managed)
			write(f, "runs", `{"version":1,"name":"aurora","gcp_project":"proj-1234"}`)
		}, nil},
		"an unlabelled bucket with a mark": {func(f *cloud) {
			f.gcs.AddBucket("runs", testProjectNumber, nil)
			write(f, "runs", `{"version":1,"name":"aurora","gcp_project":"proj-1234"}`)
		}, nil},
		"not json": {func(f *cloud) { f.gcs.AddBucket("runs", testProjectNumber, managed); write(f, "runs", "nope") }, nil},
		"wrong version": {func(f *cloud) {
			f.gcs.AddBucket("runs", testProjectNumber, managed)
			write(f, "runs", `{"version":2,"name":"aurora","gcp_project":"proj-1234"}`)
		}, nil},
		"no name": {func(f *cloud) {
			f.gcs.AddBucket("runs", testProjectNumber, managed)
			write(f, "runs", `{"version":1,"gcp_project":"proj-1234"}`)
		}, nil},
		"oversized": {func(f *cloud) {
			f.gcs.AddBucket("runs", testProjectNumber, managed)
			write(f, "runs", `{"version":1,"name":"aurora","gcp_project":"proj-1234","x":"`+strings.Repeat("a", 5000)+`"}`)
		}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloud(t)
			tc.seed(f)
			got, err := ReadProjectMarker(ctx, f.c, "runs", testProjectNumber)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, err %v; want %+v", got, err, tc.want)
			}
		})
	}
	if m, err := ReadProjectMarker(ctx, newCloud(t).c, "", testProjectNumber); m != nil || err != nil {
		t.Fatalf("no bucket name: %v, %v", m, err)
	}
}
