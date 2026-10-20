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
	"slices"
	"strings"
	"time"

	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// checkoutProject is resolveCheckout plus the one side effect every command
// that resolves a checkout's project has: the checkout is remembered for
// that project (recordCheckout), best effort.
//
// The split exists for the readers that must not write: `fugaro config list
// --json` re-validates the checkouts it reads and records nothing, so it
// calls resolveCheckout. Everything else wants the checkout remembered and
// calls this one.
func checkoutProject(ctx context.Context, dir string) (*localcfg.Checkout, error) {
	co, err := resolveCheckout(ctx, dir)
	if err != nil || co == nil {
		return co, err
	}
	recordCheckout(ctx, co)
	return co, nil
}

// resolveCheckout is the checkout at dir ("" is the working directory):
// its git toplevel and its fugaro.yaml's project:. Nil when dir is in no
// checkout, or its toplevel holds no fugaro.yaml. The project: is read
// leniently (config.ProjectOf), so a fugaro.yaml that doesn't parse still
// says which project it belongs to; YAML that doesn't decode, or a
// project: that isn't one string, is an error.
func resolveCheckout(ctx context.Context, dir string) (*localcfg.Checkout, error) {
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
	// The git provider is read leniently and never validated here: this
	// function answers "which project does this checkout belong to", and a
	// file that names no provider (or one nothing carries) still answers it.
	// A provider that is not a string is not an error here either — the file
	// is refused where it is read as a config, and remembering a checkout is
	// not the place to refuse one.
	provider, _ := config.ProviderOf(data)
	return &localcfg.Checkout{Root: root, Project: project, GCPProject: gcp, Provider: provider}, nil
}

// recordCheckout remembers the checkout a command is running in for the
// project it belongs to (localcfg.RecordCheckout).
//
// It is here, in the one code path that resolves a checkout's project
// (checkoutProject), rather than in each command that wants it recorded:
// `fugaro init --repo`, `fugaro run` and `fugaro validate` all reach this
// function — through selectProject, selectNamed and selectedProjectConfigErr
// — and so does every other command that acts on a checkout, which is what
// makes the remembered set the checkouts the operator actually works in
// rather than the ones three commands happen to name.
//
// It is best effort in every direction: a checkout with no project:, an
// origin git rewrites (readOrigin's Rewritten: the repository a checkout
// resolves to is not the one its own config says), a host no provider lives
// on, a checkout path the state file refuses — none of them is an error, and
// none of them is printed. Nothing here can fail the command that runs it,
// and the path never reaches stdout, stderr or a log line: it is local state
// for `fugaro config list --json` and nothing else.
func recordCheckout(ctx context.Context, co *localcfg.Checkout) {
	if consoleRead(ctx) {
		// A read the desktop console spawns changes nothing (see
		// consoleReadPaths): it looks at the checkout and remembers nothing,
		// so a console whose stored checkout directory is one no CLI command
		// has ever been run in stays that way until the operator works there.
		return
	}
	if !config.ProjectNameRE.MatchString(co.Project) {
		return
	}
	oi, ok := readOrigin(ctx, co.Root)
	if !ok || oi.Rewritten {
		return
	}
	// The provider the checkout's own fugaro.yaml names wins; a file that
	// names none (or names one this build does not carry) falls back to the
	// provider whose host the origin is on, which is the same pair
	// localcfg.ProviderHosts holds and repoKnown already trusts.
	provider := co.Provider
	if !slices.Contains(config.Providers, provider) {
		provider = ""
	}
	if provider == "" {
		provider = providerForHost(oi.Host)
	}
	if provider == "" {
		return
	}
	_ = localcfg.RecordCheckout(os.Getenv, co.Project,
		localcfg.CheckoutIdentity{Provider: provider, Repo: oi.Repo}, co.Root, time.Now())
}

// providerForHost is the provider whose repositories live on host
// (localcfg.ProviderHosts, inverted), "" for a host no provider lives on —
// GitHub Enterprise and self-hosted Bitbucket included, whose repositories
// this tree does not name by provider anywhere.
func providerForHost(host string) string {
	for _, p := range config.Providers {
		if localcfg.ProviderHosts[p] == host {
			return p
		}
	}
	return ""
}

// consoleReadCtxKey marks the commands the desktop console spawns as reads:
// docs/design/desktop-console.md §5.1's exact-argv allow-list, plus the two
// reads that design names for the console and whose allow-list rows the
// console task adds (`config list --json` for the installation list, D13 and
// §12 item 8; `plan graph --json` for the graph document, V2.2.1).
//
// It is set once, from the command's own path, in NewRootCmd's
// PersistentPreRun, and read in one place: recordCheckout. A console read
// must change nothing on the machine — that is the rule the whole allow-list
// rests on (design §5.1 rule 6, D14: the read-only guarantee is the argv
// table's, since the credential cannot be checked) — and the remembered
// checkouts are a file this machine keeps, so a read must not write it. The
// commands that do work in a checkout (fugaro init --repo, fugaro run,
// fugaro validate, the writers) are not on this list and record as before.
type consoleReadCtxKey struct{}

// withConsoleRead marks ctx as a console read; NewRootCmd's PersistentPreRun
// is the only caller.
func withConsoleRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, consoleReadCtxKey{}, true)
}

// consoleRead reports whether ctx is a command the console spawns as a read.
// A context that never carried the mark (a test calling a resolver directly,
// a command path nothing named) is not one: recording is the default and the
// console's reads are the exception, so a command added tomorrow records
// until someone says otherwise.
func consoleRead(ctx context.Context) bool {
	v, _ := ctx.Value(consoleReadCtxKey{}).(bool)
	return v
}

// consoleReadPaths is consoleReadCtxKey's table, keyed by cobra's own command
// path (cmd.CommandPath(), e.g. "fugaro budget show") rather than by command
// name: §5.1 rule 6's own warning is that a name is not enough, since
// "ls" is `runs ls` here and `secrets ls` there. A test reads §5.1's table
// out of the design document and fails if an argv it allows is missing here,
// so this list cannot drift from the spawn contract it exists to serve.
var consoleReadPaths = map[string]bool{
	"fugaro watch":       true,
	"fugaro budget show": true,
	"fugaro report":      true,
	"fugaro runs ls":     true,
	"fugaro diagnose":    true,
	"fugaro logs":        true,
	"fugaro secrets ls":  true,
	"fugaro accounts ls": true,
	"fugaro version":     true,
	"fugaro doctor":      true,
	// The two the design names and §5.1 has not carried a row for yet (the
	// console task adds them). Neither resolves a checkout today; they are
	// named so that the day one does, it is still a read that changes
	// nothing.
	"fugaro config list": true,
	"fugaro plan graph":  true,
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
	lc, _ := selectedProjectConfigErr(ctx)
	return lc
}

// selectedProjectConfigErr is selectedProjectConfig with the reason it found
// none: a nil error for a machine where nothing selects a project config (no
// config at all, or several and nothing picking one — the states the offline
// commands work in, and the ones "this command has none selected" is true of),
// and localcfg.Select's own error for a project config that is there and was
// refused — a real problem with the operator's own config, which
// selectedProjectConfig's nil cannot be told apart from.
//
// The commands that only want the config if there is one keep calling
// selectedProjectConfig; `fugaro validate` calls this one, because it printed
// the second state as the first: a config whose routing switch names a model no
// entry claims fails at load (localcfg's CheckRouting), and validate said "this
// command has none selected" about it and exited 0.
//
// A checkout whose own fugaro.yaml cannot be read is swallowed here too, as it
// always was: that is a problem with the file being validated, which validate
// reports against the file itself, not a selection that failed.
func selectedProjectConfigErr(ctx context.Context) (*localcfg.Config, error) {
	return selectedProjectErr(ctx, checkoutProject)
}

// selectedProjectConfigReadOnly is selectedProjectConfig without the
// recording: the same selection, resolved through resolveCheckout rather
// than checkoutProject, so looking at a checkout does not remember it.
//
// It is for the one caller that asks the question on EVERY command, before
// the command has done anything: the update check's "does this project's
// config say update_check: false" (updateCheckDisabled, reached from
// NewRootCmd's PersistentPreRun). Recording there would make every command
// that runs in a checkout write the remembered checkouts — `fugaro config
// list` among them, and that one is a read the console spawns under a rule
// of its own, that a read the console spawns changes nothing
// (TestConsoleV2ReadsWriteNothing). A command that resolves the checkout for
// its own work still records it, because it calls checkoutProject.
func selectedProjectConfigReadOnly(ctx context.Context) *localcfg.Config {
	lc, _ := selectedProjectErr(ctx, resolveCheckout)
	return lc
}

// selectedProjectErr is selectedProjectConfigErr with the checkout resolver
// given: checkoutProject remembers the checkout it resolves, resolveCheckout
// does not.
func selectedProjectErr(ctx context.Context, resolve func(context.Context, string) (*localcfg.Checkout, error)) (*localcfg.Config, error) {
	co, err := resolve(ctx, "")
	if err != nil {
		return nil, nil
	}
	if co != nil && co.Project == "" {
		// A checkout without project: would refuse; the file being
		// written is the one that lacks it.
		co = nil
	}
	_, lc, err := selectFrom(cloudOptions{}, co, false)
	if err == nil {
		return lc, nil
	}
	var se *localcfg.SelectError
	if errors.As(err, &se) {
		// Nothing selected, or several and nothing picking one: a state, not a
		// broken config.
		return nil, nil
	}
	return nil, err
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
