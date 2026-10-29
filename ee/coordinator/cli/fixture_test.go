// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the moved tests' fixture. The helpers it holds were
// internal/cli's test helpers before the command moved, and an ee/ package
// cannot reach another package's _test.go: what is used here is rebuilt on
// exported packages alone (internal/gcpfake, internal/localcfg, internal/infra,
// cli.NewRootCmd, cli.NormalizeArgs), so the moved tests drive the same fakes
// they always did.

// appSlug is the storage slug of the fixture's repository, GitHub's acme/app.
var appSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// coordinatorTestRoot is the tree the moved tests drive: the released tree with
// this package's command in place of the stub a build without it shows for the
// name.
//
// It assembles the tree itself rather than blank-importing ee/register, and the
// reason is that it cannot: ee/register imports this package, so an import of
// it from here is a cycle ("import cycle not allowed in test"), and a fixture
// may not call cli.RegisterCommand — only ee/register may (E1.5's rule, held by
// internal/eeboundary). ee/register/coordinator_test.go is where the
// registration itself is pinned, against the tree the tagged binary builds.
func coordinatorTestRoot() *cobra.Command {
	root := fcli.NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "coordinator" {
			root.RemoveCommand(c)
			break
		}
	}
	root.AddCommand(NewCoordinatorCmd())
	return root
}

// execute runs the coordinator's command tree with args and captures its output.
func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := coordinatorTestRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(fcli.NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// recentRunID mints a run ID offset back from the clock the listing's --since
// window is measured against, which is the one the coordinator's default --since
// uses. A fixed date would pass only until the window slid past it.
func recentRunID(t *testing.T, offset time.Duration, suffix string) string {
	t.Helper()
	return fcli.LsNow().UTC().Add(-offset).Format("20060102-150405") + "-" + suffix
}

// cloudFixture is a local config pointing at fakes and a file:// bucket.
type cloudFixture struct {
	dir     string
	bucket  string // file:// URL
	run     *gcpfake.Run
	logging *gcpfake.Logging
}

// newCloudFixture writes a local config whose endpoints point at the Run and
// Logging fakes, plus any extra "key: url" endpoint entries a later test adds
// with its own fake.
func newCloudFixture(t *testing.T, extraEndpoints ...string) *cloudFixture {
	t.Helper()
	f := &cloudFixture{dir: t.TempDir(), run: gcpfake.NewRun(t), logging: gcpfake.NewLogging(t)}
	// Executions the fake starts live where the config's backend looks.
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + runs
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: " + f.bucket +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + f.logging.URL + "/, " + extra(extraEndpoints) + "no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	// The project config is aurora's, in a config directory of the test's
	// own; FUGARO_CONFIG names it too, as tests append to it.
	path := isolateProjects(t, f.dir)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	f.run.AddJob(gcp.JobName(appSlug, "web"), "4", "8Gi")
	// The installation names its project in the runs bucket, as its
	// Terraform does; every cloud command checks it.
	f.writeMarker(t, "aurora", "proj-1234")
	return f
}

// markerPath is where the fixture's runs bucket keeps the project marker.
func (f *cloudFixture) markerPath() string {
	return filepath.Join(f.dir, "runs", filepath.FromSlash(infra.ProjectMarkerObject))
}

// writeMarker makes the fixture's installation name project name in GCP
// project gcpProject.
func (f *cloudFixture) writeMarker(t *testing.T, name, gcpProject string) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: name, GCPProject: gcpProject})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.markerPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.markerPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendConfig adds top-level YAML (such as budget: …) to the local config.
func (f *cloudFixture) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// config is the fixture's project config as the command under test loads it.
func (f *cloudFixture) config(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

func extra(entries []string) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e + ", ")
	}
	return b.String()
}

// isolateProjects points the project configs at a directory under dir,
// clears FUGARO_PROJECT and FUGARO_CONFIG, and returns where aurora's
// project config goes.
func isolateProjects(t *testing.T, dir string) string {
	t.Helper()
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// The project-name check caches its answer here, never in the
	// developer's own cache.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", "")
	projects := filepath.Join(xdg, "fugaro", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(projects, "aurora.yaml")
}

// appKeyPEM is a fresh RSA key in PEM form, for the tests that need a GitHub
// App key in the environment: the sweep opens its provider from the
// installation's credentials, and the key is what it holds.
func appKeyPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

// userStatsPlace writes the fixture's local config with user: replaced by
// user (dropped when empty) and the installation's own launchers and
// operators lists set, so a test decides how the installation places the
// caller.
func userStatsPlace(t *testing.T, f *cloudFixture, user string, launchers, operators []string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)
	line := ""
	if user != "" {
		line = "user: " + user + "\n"
	}
	cfg = regexp.MustCompile(`(?m)^user: .*\n`).ReplaceAllString(cfg, line)
	if len(launchers) > 0 || len(operators) > 0 {
		cfg += "terraform:\n"
		for _, l := range []struct {
			key  string
			list []string
		}{{"launchers", launchers}, {"operators", operators}} {
			if len(l.list) == 0 {
				continue
			}
			cfg += "  " + l.key + ": [" + strings.Join(l.list, ", ") + "]\n"
		}
	}
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openFromConfig is the cloud the declarations the tests below drive are made
// through: the project config the fixture wrote, opened the way the command
// opens one.
func openFromConfig(t *testing.T, f *cloudFixture) *fcli.Cloud {
	t.Helper()
	c, err := fcli.OpenFromConfig(context.Background(), f.config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
