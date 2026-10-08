package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// checkoutProject is the checkout at dir ("" is the working directory):
// its git toplevel and its fugaro.yaml's project:. Nil when dir is in no
// checkout, or its toplevel holds no fugaro.yaml. The project: is read
// leniently (config.ProjectOf), so a fugaro.yaml that doesn't parse still
// says which project it belongs to; YAML that doesn't decode, or a
// project: that isn't one string, is an error.
func checkoutProject(ctx context.Context, dir string) (*localcfg.Checkout, error) {
	if dir == "" {
		dir = "."
	}
	out, err := gitCmd(ctx, dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, nil
	}
	root := strings.TrimSpace(string(out))
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, userErr("reading %s: %v", filepath.Join(root, "fugaro.yaml"), err)
	}
	project, err := config.ProjectOf(data)
	if err != nil {
		return nil, userErr("%s can't say which project it belongs to: %v", filepath.Join(root, "fugaro.yaml"), err)
	}
	gcp, err := config.GCPProjectOf(data)
	if err != nil {
		return nil, userErr("%s can't say which GCP project it names: %v", filepath.Join(root, "fugaro.yaml"), err)
	}
	// Refuse a hostile value here, before anything builds a name from it.
	if gcp != "" && !config.GCPProjectRE.MatchString(gcp) {
		return nil, userErr("%s: gcp_project %q is not a GCP project ID", filepath.Join(root, "fugaro.yaml"), gcp)
	}
	return &localcfg.Checkout{Root: root, Project: project, GCPProject: gcp}, nil
}

// selectProject picks the project config a cloud command acts on
// (localcfg.Select), from the flags, the working directory's checkout and
// the environment, and applies --gcp-project and --region to it (announce).
// Every refusal is a user error.
func selectProject(ctx context.Context, o cloudOptions) (localcfg.Selection, *localcfg.Config, error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return localcfg.Selection{}, nil, err
	}
	// Only here may a missing local config fall back to the shared one: the
	// offline commands, init and the writers never fetch it.
	o.sharedOK = true
	sel, lc, err := selectWith(ctx, o, co, false, "")
	if err != nil {
		return sel, nil, err
	}
	return sel, lc, announce(o, sel, lc)
}

// selectFrom is localcfg.Select with the checkout given; creating is
// fugaro init, which may select a project config it has yet to write
// (nil).
func selectFrom(o cloudOptions, co *localcfg.Checkout, creating bool) (localcfg.Selection, *localcfg.Config, error) {
	return selectNamed(o, co, creating, "")
}

// selectNamed is selectFrom with fugaro init's --name: where nothing else
// selects one of several project configs, it names the project (a first run
// of that name too).
func selectNamed(o cloudOptions, co *localcfg.Checkout, creating bool, name string) (localcfg.Selection, *localcfg.Config, error) {
	// o.sharedOK is set by selectProject alone.
	return selectWith(context.Background(), o, co, creating, name)
}

// sharedFetch is fetchSharedConfig; a test seam.
var sharedFetch = fetchSharedConfig

// selectWith is selectNamed that, with o.sharedOK, falls back to the shared
// config published to the runs bucket where there is no local one (ctx is
// its fetch's).
func selectWith(ctx context.Context, o cloudOptions, co *localcfg.Checkout, creating bool, name string) (localcfg.Selection, *localcfg.Config, error) {
	in := localcfg.SelectInput{
		Config: o.config, Project: o.project, Checkout: co, Name: name, Origin: func() (string, string) { return checkoutOriginAt(o.originDir) },
		EnvProject: os.Getenv("FUGARO_PROJECT"), EnvConfig: os.Getenv("FUGARO_CONFIG"),
		Creating: creating, Getenv: os.Getenv,
	}
	if o.sharedOK && !creating {
		// The GCP project is the checkout's committed gcp_project: or
		// --gcp-project, nothing else. They must agree, but only where the
		// shared config is actually looked up: a local config, --config and
		// $FUGARO_CONFIG never see the contradiction (Override compares the
		// flag with the selected config as before).
		var fromCheckout string
		if co != nil {
			fromCheckout = co.GCPProject
		}
		in.GCPProject = cmp.Or(o.gcpProject, fromCheckout)
		in.Shared = func(name, gcpProject string) (*localcfg.Config, string, error) {
			if o.gcpProject != "" && fromCheckout != "" && o.gcpProject != fromCheckout {
				return nil, "", userErr("--gcp-project %s contradicts this checkout's gcp_project %s in fugaro.yaml", o.gcpProject, fromCheckout)
			}
			return sharedFetch(ctx, os.Getenv, time.Now(), name, gcpProject)
		}
	}
	sel, lc, err := localcfg.Select(in)
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return sel, nil, err // a fetch's own refusal keeps its exit code
		}
		return sel, nil, userErr("%w", err) // %w: cloudCheck tells a missing local config from a broken one
	}
	return sel, lc, nil
}

// errSharedWrite is the refusal of a command that would write the local
// project config while the selected one is the shared config.
const errSharedWrite = "this project's config is the shared one published by an operator, so it can't be changed here; run fugaro init to create your own local config"

// refuseSharedWrite is the refusal for a command about to write the
// selected project config when that is the shared config (which has no
// file).
func refuseSharedWrite(sel localcfg.Selection) error {
	if sel.From == "shared config" {
		return userErr("%s", errSharedWrite)
	}
	return nil
}

// announce prints the project header (when there is a config) and the
// selection's notes to stderr, then applies --gcp-project and --region to
// lc, so a refusal of either comes after the header.
func announce(o cloudOptions, sel localcfg.Selection, lc *localcfg.Config) error {
	if lc != nil {
		printProjectHeader(o.errw(), lc)
		if sel.From == "shared config" {
			fmt.Fprintf(o.errw(), "fugaro: note: no local config for %s: using the shared config published to gs://fugaro-runs-%s\n", lc.Name, lc.GCPProject)
		}
	}
	for _, n := range sel.Notes {
		fmt.Fprintf(o.errw(), "fugaro: note: %s\n", n)
	}
	if lc != nil {
		if err := lc.Override(o.gcpProject, o.region); err != nil {
			return userErr("%v", err)
		}
	}
	return nil
}

// printProjectHeader says which project a command acts on: the first line
// every cloud command writes to stderr.
func printProjectHeader(w io.Writer, lc *localcfg.Config) {
	fmt.Fprintf(w, "project: %s (GCP %s)\n", lc.Name, lc.GCPProject)
}

// errw is the command's stderr.
func (o cloudOptions) errw() io.Writer {
	if o.stderr != nil {
		return o.stderr()
	}
	return os.Stderr
}

// markerMaxBytes bounds the project marker read: the object is a few
// dozen bytes, and anyone holding objectAdmin on the bucket could replace it.
const markerMaxBytes = 4 << 10

// checkCloudName refuses a project config that doesn't point at its own
// installation: the runs bucket's fugaro/project.json names the project
// and the GCP project, and both must be the config's. A missing object
// (an installation applied before M9a) is a user error naming the fix; a
// read failure is remote (exit 2). A passed check is cached for a day per
// project, keyed by the GCP project and the bucket it was made for.
func checkCloudName(ctx context.Context, b *blobx.Bucket, lc *localcfg.Config, getenv func(string) string, now time.Time) error {
	bucketURL := lc.BucketURL()
	if c, ok := localcfg.CachedNameCheck(getenv, lc.Name, now); ok && c.GCPProject == lc.GCPProject && c.RunsBucket == bucketURL {
		return nil
	}
	r, err := b.Bucket.NewReader(ctx, infra.ProjectMarkerObject, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return userErr("project %s's installation has no project name yet; an operator runs fugaro init --name %s (see docs/design/m9-budget-and-dashboard.md §13.1)", lc.Name, lc.Name)
	}
	if err != nil {
		return remote(fmt.Errorf("reading %s from %s: %w", infra.ProjectMarkerObject, bucketURL, err))
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, markerMaxBytes+1))
	if err != nil {
		return remote(fmt.Errorf("reading %s from %s: %w", infra.ProjectMarkerObject, bucketURL, err))
	}
	var m infra.ProjectMarker
	if len(data) > markerMaxBytes || json.Unmarshal(data, &m) != nil || m.Version != 1 || !config.ProjectNameRE.MatchString(m.Name) {
		return userErr("%s in %s is not a version 1 project marker; an operator runs fugaro init --name %s to write it again", infra.ProjectMarkerObject, bucketURL, lc.Name)
	}
	switch {
	case m.Name != lc.Name:
		return userErr("project config %s points at a GCP project whose Fugaro project is %s (%s in %s says so): check gcp_project in the config", lc.Name, m.Name, infra.ProjectMarkerObject, bucketURL)
	case m.GCPProject != lc.GCPProject:
		return userErr("project config %s says GCP project %s, but its installation's marker (%s in %s) says %s: check gcp_project in the config", lc.Name, lc.GCPProject, infra.ProjectMarkerObject, bucketURL, m.GCPProject)
	}
	// Best effort: the next command checks again if this can't be saved.
	_ = localcfg.SaveNameCheck(getenv, lc.Name, localcfg.NameCheck{GCPProject: lc.GCPProject, RunsBucket: bucketURL, CheckedAt: now})
	return nil
}

// selectedProjectName is the name of the project config a command with no
// cloud flags would act on (from FUGARO_PROJECT, FUGARO_CONFIG, the
// working directory's checkout, or the one project config), or "" when
// none is selectable. It makes no cloud call and prints nothing: it only
// lets `validate` and `config example` name the project.
func selectedProjectName(ctx context.Context) string {
	if lc := selectedProjectConfig(ctx); lc != nil {
		return lc.Name
	}
	return ""
}

// selectedProjectConfig is the project config selectedProjectName names, or
// nil.
func selectedProjectConfig(ctx context.Context) *localcfg.Config {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil
	}
	if co != nil && co.Project == "" {
		// A checkout without project: would refuse; the file being
		// written is the one that lacks it.
		co = nil
	}
	_, lc, err := selectFrom(cloudOptions{}, co, false)
	if err != nil {
		return nil
	}
	return lc
}

// checkRepoProject refuses a repository whose fugaro.yaml names another
// project than the installation init --repo is adding it to.
func checkRepoProject(fugaroYAML, installation string) error {
	if fugaroYAML == installation {
		return nil
	}
	return userErr("fugaro.yaml names project %s, but this installation is project %s", fugaroYAML, installation)
}

// baseProjectWarning is the warning, or "", about the base branch on
// origin: the runner reads project: from origin/<base>'s fugaro.yaml, not
// from the checkout's, so onboarding from a feature branch works only once
// the base says the same. It fetches the base; any failure to do so is a
// warning, not a refusal. A base without a fugaro.yaml says nothing.
func baseProjectWarning(ctx context.Context, root, base, project string) string {
	repo, err := gitops.Open(root, []string{"GIT_TERMINAL_PROMPT=0"})
	if err == nil {
		err = repo.FetchBase(ctx, base)
	}
	var data []byte
	if err == nil {
		var mode string
		if mode, err = repo.TreeEntryMode(ctx, "origin/"+base, "fugaro.yaml"); err == nil && mode == "" {
			// A base with no fugaro.yaml at all (a repository being
			// onboarded) has no project to disagree about.
			return ""
		}
	}
	if err == nil {
		data, err = repo.ShowFile(ctx, "origin/"+base, "fugaro.yaml")
	}
	if err != nil {
		return fmt.Sprintf("couldn't check origin/%s's fugaro.yaml for project: %s (%v); runs read it from there", base, project, err)
	}
	got, err := config.ProjectOf(data)
	switch {
	case err != nil:
		return fmt.Sprintf("origin/%s's fugaro.yaml can't say which project it belongs to (%v); runs will refuse until %s's fugaro.yaml says project: %s", base, err, base, project)
	case got == project:
		return ""
	case got == "":
		return fmt.Sprintf("origin/%s's fugaro.yaml has no `project:`; runs will refuse until %s's fugaro.yaml says project: %s", base, base, project)
	}
	return fmt.Sprintf("origin/%s's fugaro.yaml names project %s; runs will refuse until %s's fugaro.yaml says project: %s", base, got, base, project)
}

// checkoutOriginAt is dir's (default: the working directory's) checkout's
// origin host and repository (owner/name), "" with none or one git config
// rewrites (not trusted by name).
func checkoutOriginAt(dir string) (host, repo string) {
	ctx := context.Background()
	if dir == "" {
		dir = "."
	}
	root, err := gitRead(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ""
	}
	if oi, ok := readOrigin(ctx, root); ok && !oi.Rewritten {
		return oi.Host, oi.Repo
	}
	return "", ""
}
