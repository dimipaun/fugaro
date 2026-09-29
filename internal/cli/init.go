package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// projectEnvVars name a project to the Google tools. One naming another
// project than the installation's is refused: Terraform's provider or a
// gcloud call could otherwise act on it.
var projectEnvVars = []string{"GOOGLE_PROJECT", "GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"}

const impersonateEnv = "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT"

// stdinIsTerminal reports whether in is a terminal, where a confirmation
// can be typed. Tests replace it.
var stdinIsTerminal = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

type initOptions struct {
	cloud                               cloudOptions
	schedulerRegion                     string
	runsBucket, stateBucket, baseImage  string
	launchers, operators                []string
	budget                              int64
	budgetCurrency, billingAccount      string
	alertEmail                          string
	noLogIsolation                      bool
	registryCleanup                     string
	planOnly, printVars, configOnly     bool
	forget, yes, asJSON                 bool
	allowDelete                         []string
	launchersChanged, operatorsChanged  bool
	alertEmailChanged, baseImageChanged bool

	// --repo: onboard the repository of a checkout.
	repo, noBuild, allowJobDelete bool
	githubAppID                   string
}

func newInitCmd() *cobra.Command {
	o := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init [--repo [PATH]]",
		Short: "Stand up or adopt the installation's, or a repository's, cloud resources through Terraform",
		Long: `init plans the installation's shared resources (the runs bucket, registries,
custom roles, the scheduler account, log isolation, an optional budget) with
Terraform, adopting what the bootstrap already made, and applies the plan it
showed once you confirm by typing the project ID (or pass --yes). It then
writes the local config from the installation's outputs.

It creates the Terraform state bucket first when it doesn't exist, and
offers to remove project Viewers' read access to the runs bucket, each after
its own confirmation. A plan that would delete or replace anything is
refused unless --allow-delete names the address.

--forget is the rollback: it turns log isolation and registry cleanup off
with a guarded apply, then removes every address from Terraform's state,
destroying nothing else.

init --repo [PATH] onboards the repository of the checkout at PATH (default:
the current directory), whose fugaro.yaml says what it needs, into the
installation fugaro init applied. It adopts what the bootstrap made for it
(checking each resource's marks and live grants first), creates the rest,
and deploys each workflow's job once its secrets are stored and its image is
built. It offers each workflow's first image build (billable, confirmed
separately), then deploys the built image and unpauses the daily check. It
prints the fugaro secrets set commands still needed, and adds the
repository to the local config. --forget removes the repository from
Terraform's state, destroying nothing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			o.launchersChanged, o.operatorsChanged = f.Changed("launcher"), f.Changed("operator")
			o.alertEmailChanged, o.baseImageChanged = f.Changed("alert-email"), f.Changed("base-image")
			if o.repo {
				return runInitRepo(cmd, o, args)
			}
			if len(args) > 0 {
				return userErr("a checkout path is for --repo; fugaro init for the installation takes no argument")
			}
			return runInit(cmd, o)
		},
	}
	addCloudFlags(cmd, &o.cloud)
	f := cmd.Flags()
	f.StringVar(&o.schedulerRegion, "scheduler-region", "", "Cloud Scheduler region of the daily image checks (default: the region, or the nearest one Scheduler offers)")
	f.StringVar(&o.runsBucket, "runs-bucket", "", "the runs bucket (default: the local config's, else fugaro-runs-<project>)")
	f.StringVar(&o.stateBucket, "state-bucket", "", "the Terraform state bucket (default: the local config's, else fugaro-tfstate-<project>)")
	f.StringVar(&o.baseImage, "base-image", "", "the base image the image checks run and builds start from, recorded in the local config")
	f.StringArrayVar(&o.launchers, "launcher", nil, "an IAM member who launches and watches runs (repeatable; default: the local config's)")
	f.StringArrayVar(&o.operators, "operator", nil, "an IAM member who onboards repositories (repeatable; default: the local config's)")
	f.Int64Var(&o.budget, "budget", 0, "a monthly budget on the project, in whole units of --budget-currency")
	f.StringVar(&o.budgetCurrency, "budget-currency", "", "the budget's currency, such as USD")
	f.StringVar(&o.billingAccount, "billing-account", "", "the billing account the budget is on")
	f.StringVar(&o.alertEmail, "alert-email", "", "where failed image checks and rebuilds are reported (default: the local config's)")
	f.BoolVar(&o.noLogIsolation, "no-log-isolation", false, "leave Fugaro job logs in _Default instead of their own log bucket")
	f.StringVar(&o.registryCleanup, "registry-cleanup", "", "Artifact Registry cleanup: dry-run (the default), on or off")
	f.BoolVar(&o.planOnly, "plan-only", false, "stop after showing the plan")
	f.BoolVar(&o.printVars, "print-vars", false, "print the Terraform variables and exit, with no cloud calls and no Terraform")
	f.BoolVar(&o.configOnly, "config-only", false, "only write the local config, from the installation's outputs (else the flags)")
	f.BoolVar(&o.forget, "forget", false, "roll back: turn log isolation and registry cleanup off, then remove every address from Terraform's state")
	f.StringArrayVar(&o.allowDelete, "allow-delete", nil, "a resource address the plan may delete or replace (repeatable)")
	f.BoolVar(&o.yes, "yes", false, "confirm every step without asking (only after reading what it will do)")
	f.BoolVar(&o.asJSON, "json", false, "print the result as JSON on stdout (progress goes to stderr)")
	f.BoolVar(&o.repo, "repo", false, "onboard the repository of the checkout at PATH (default: the current directory) instead of the installation")
	f.StringVar(&o.githubAppID, "github-app-id", "", "with --repo: the GitHub App's ID, for a GitHub repository (not a secret; recorded in the local config)")
	f.BoolVar(&o.noBuild, "no-build", false, "with --repo: don't offer the first image builds")
	f.BoolVar(&o.allowJobDelete, "allow-job-delete", false, "with --repo: lower the jobs' deletion protection, for offboarding")
	return cmd
}

// initRun is one run of fugaro init.
type initRun struct {
	cmd     *cobra.Command
	o       *initOptions
	w       io.Writer // the human-readable account: stdout, or stderr with --json
	in      *bufio.Reader
	project string
	res     initResult
}

// initResult is what --json prints.
type initResult struct {
	Project   string                     `json:"project"`
	Repo      string                     `json:"repo,omitempty"`
	Workdir   string                     `json:"workdir,omitempty"`
	Changes   *infra.PlanCounts          `json:"changes,omitempty"`
	Applied   bool                       `json:"applied"`
	Outputs   *infra.InstallationOutputs `json:"outputs,omitempty"`
	Config    string                     `json:"config,omitempty"`
	Backup    string                     `json:"backup,omitempty"`
	Forgotten bool                       `json:"forgotten,omitempty"`
	Deleted   []string                   `json:"deleted,omitempty"`
	Undelete  string                     `json:"undelete,omitempty"`
	Builds    []string                   `json:"builds,omitempty"`
	Missing   []string                   `json:"missing,omitempty"`
	Warnings  []string                   `json:"warnings,omitempty"`
}

func newInitRun(cmd *cobra.Command, o *initOptions) *initRun {
	r := &initRun{cmd: cmd, o: o, w: cmd.OutOrStdout(), in: bufio.NewReader(cmd.InOrStdin())}
	if o.asJSON {
		r.w = cmd.ErrOrStderr()
	}
	return r
}

func runInit(cmd *cobra.Command, o *initOptions) error {
	r := newInitRun(cmd, o)
	if err := o.check(); err != nil {
		return err
	}
	if !o.printVars {
		// Refused before anything else: every later step talks to Google.
		if err := refuseHTTP2Debug(os.Getenv); err != nil {
			return err
		}
	}
	lc, path, old, err := loadInitConfig(o)
	if err != nil {
		return err
	}
	r.project = lc.Project
	r.res.Project = lc.Project
	spec, err := installOptions(o, lc)
	if err != nil {
		return err
	}
	if o.printVars {
		data, err := infra.InstallationVars(spec)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}

	// 1. The environment.
	bin, err := r.checkEnv(lc)
	if err != nil {
		return err
	}

	// 2. The workdir, and terraform in it.
	dir, err := infra.InstallationWorkdir(os.Getenv, lc.Project)
	if err != nil {
		return userErr("%v", err)
	}
	wd, t, err := r.terraform(bin, dir, "installation")
	if err != nil {
		return err
	}
	r.res.Workdir = wd.Dir

	ctx := cmd.Context()
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}

	switch {
	case o.forget:
		err = r.forget(ctx, c, t, wd, spec)
	case o.configOnly:
		err = r.configOnly(ctx, c, t, wd, lc, spec, path, old)
	default:
		err = r.install(ctx, c, t, wd, lc, spec, path, old)
	}
	if err != nil {
		return err
	}
	return r.printResult()
}

// checkEnv refuses an environment that would point Terraform or the
// Google tools elsewhere, and finds terraform. It prints the local
// config's warnings once it passes.
func (r *initRun) checkEnv(lc *localcfg.Config) (string, error) {
	for _, k := range projectEnvVars {
		if v := os.Getenv(k); v != "" && v != lc.Project {
			return "", userErr("%s is set to %s, not the installation's project %s; unset it (or set it to %s) and rerun", k, v, lc.Project, lc.Project)
		}
	}
	if _, set := os.LookupEnv(impersonateEnv); set {
		return "", userErr("%s is set; fugaro init runs Terraform as your own credentials only, so unset it and rerun", impersonateEnv)
	}
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return "", userErr("fugaro init needs terraform (>= 1.7, < 2) on PATH: %v", err)
	}
	for _, w := range lc.Warnings() {
		r.warn(w)
	}
	return bin, nil
}

// terraform prepares the workdir dir for root and a terraform that runs
// there, with the allowlisted environment.
func (r *initRun) terraform(bin, dir, root string) (*infra.Workdir, *tf.TF, error) {
	wd, err := infra.PrepareWorkdir(dir, root)
	if err != nil {
		return nil, nil, userErr("the Terraform workdir: %v", err)
	}
	cache, err := infra.PluginCache(os.Getenv)
	if err != nil {
		return nil, nil, userErr("%v", err)
	}
	env, err := tf.Env(os.Environ(), wd.Dir, cache)
	if err != nil {
		return nil, nil, userErr("%v", err)
	}
	t, err := tf.New(bin, wd.Root, env)
	if err != nil {
		var ee *tf.ExitError
		if errors.As(err, &ee) {
			return nil, nil, remote(err)
		}
		return nil, nil, userErr("%v", err)
	}
	t.Out = r.cmd.ErrOrStderr()
	return wd, t, nil
}

// gcpOptions are the Google API options of lc's project and endpoints.
func gcpOptions(lc *localcfg.Config) gcp.Options {
	return gcp.Options{Project: lc.Project, Region: lc.Region, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, SecretManager: lc.Endpoints.SecretManager, CloudBuild: lc.Endpoints.CloudBuild, NoAuth: lc.Endpoints.NoAuth}}
}

func newInitClients(ctx context.Context, lc *localcfg.Config) (*infra.Clients, error) {
	c, err := infra.NewClients(ctx, gcpOptions(lc),
		infra.Endpoints{IAM: lc.Endpoints.IAM, ArtifactRegistry: lc.Endpoints.ArtifactRegistry,
			Storage: lc.Endpoints.Storage, ResourceManager: lc.Endpoints.ResourceManager})
	if err != nil {
		return nil, remote(err)
	}
	return c, nil
}

// check refuses flag combinations that mean nothing.
func (o *initOptions) check() error {
	if o.githubAppID != "" || o.noBuild || o.allowJobDelete {
		return userErr("--github-app-id, --no-build and --allow-job-delete are for fugaro init --repo")
	}
	n := 0
	for _, b := range []bool{o.planOnly, o.printVars, o.configOnly, o.forget} {
		if b {
			n++
		}
	}
	if n > 1 {
		return userErr("--plan-only, --print-vars, --config-only and --forget exclude one another")
	}
	if o.forget && len(o.allowDelete) > 0 {
		return userErr("--forget allows exactly the log isolation's deletes; it takes no --allow-delete")
	}
	if (o.budget != 0 || o.budgetCurrency != "" || o.billingAccount != "") && (o.budget <= 0 || o.budgetCurrency == "" || o.billingAccount == "") {
		return userErr("a budget needs --budget (more than 0), --budget-currency and --billing-account together")
	}
	return nil
}

// loadInitConfig loads the local config with --project, --region and
// --runs-bucket applied; without one, it starts a new one from those flags.
// old is the file's content (nil when there is none).
func loadInitConfig(o *initOptions) (lc *localcfg.Config, path string, old []byte, err error) {
	path = o.cloud.config
	if path == "" {
		if path, err = localcfg.Path(os.Getenv); err != nil {
			return nil, "", nil, userErr("%v", err)
		}
	}
	old, err = os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if o.cloud.project == "" || o.cloud.region == "" {
			return nil, "", nil, userErr("there is no local config at %s yet: pass --project and --region", path)
		}
		old = nil
		lc, err = localcfg.Parse([]byte("version: 1\nproject: " + o.cloud.project + "\nregion: " + o.cloud.region + "\nruns_bucket: fugaro-runs-" + o.cloud.project + "\n"))
		if err != nil {
			return nil, "", nil, userErr("--project/--region: %v", err)
		}
	case err != nil:
		return nil, "", nil, userErr("reading the local config: %v", err)
	default:
		if lc, err = localcfg.Load(path); err != nil {
			return nil, "", nil, userErr("%v", err)
		}
		if err := lc.Override(o.cloud.project, o.cloud.region); err != nil {
			return nil, "", nil, userErr("--project/--region: %v", err)
		}
	}
	if o.runsBucket != "" {
		lc.RunsBucket = o.runsBucket
		if lc.Bucket != "" && lc.Bucket != "gs://"+o.runsBucket {
			return nil, "", nil, userErr("--runs-bucket %s is not the local config's bucket_url %s", o.runsBucket, lc.Bucket)
		}
	}
	return lc, path, old, nil
}

// installOptions is the installation's spec from the local config and the
// flags. Discovery sets whether the legacy registry is adopted.
func installOptions(o *initOptions, lc *localcfg.Config) (infra.InstallationSpec, error) {
	opts := infra.InstallOptions{
		StateBucket:     o.stateBucket,
		AlertEmail:      o.alertEmail,
		RegistryCleanup: o.registryCleanup,
		NoLogIsolation:  o.noLogIsolation,
	}
	if o.launchersChanged {
		opts.Launchers = append([]string{}, o.launchers...)
	}
	if o.operatorsChanged {
		opts.Operators = append([]string{}, o.operators...)
	}
	if o.budget > 0 {
		opts.Budget = &infra.Budget{BillingAccount: o.billingAccount, Amount: o.budget, CurrencyCode: o.budgetCurrency}
	}
	spec, err := infra.Installation(lc, opts)
	if err != nil {
		return infra.InstallationSpec{}, initErr(err)
	}
	return spec, nil
}

// initErr maps an error of infra: a *infra.UserError (a refusal, an
// ownership check) is exit 1, anything else (an API or Terraform failure)
// exit 2.
func initErr(err error) error {
	var ee *ExitError
	var ue *infra.UserError
	switch {
	case err == nil || errors.As(err, &ee):
		return err
	case errors.As(err, &ue):
		return &ExitError{Code: ExitUserError, Err: err}
	}
	return remote(err)
}

func (r *initRun) warn(msg string) {
	r.res.Warnings = append(r.res.Warnings, msg)
	fmt.Fprintln(r.w, "warning: "+msg)
}

// confirm shows the ⚠ CONFIRM banner for what, and returns nil once it is
// confirmed: by --yes, or by the project ID typed at a terminal. Without a
// terminal and without --yes it refuses; undone says what that leaves.
func (r *initRun) confirm(what, undone string) error {
	ok, err := r.ask(what)
	if err != nil {
		return err
	}
	if !ok {
		if !stdinIsTerminal(r.cmd.InOrStdin()) {
			return userErr("this step needs a confirmation: run fugaro init at a terminal and type the project ID, or pass --yes once you have read what it does; %s", undone)
		}
		return userErr("not confirmed (the project ID was not typed); %s", undone)
	}
	return nil
}

// ask shows the banner and reports whether the step is confirmed; without
// a terminal and without --yes it isn't.
func (r *initRun) ask(what string) (bool, error) {
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s): %s\n", r.project, what)
	if r.o.yes {
		fmt.Fprintln(r.w, "  confirmed by --yes")
		return true, nil
	}
	if !stdinIsTerminal(r.cmd.InOrStdin()) {
		return false, nil
	}
	fmt.Fprintf(r.w, "Type the project ID to confirm: ")
	line, err := r.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, userErr("reading the confirmation: %v", err)
	}
	return strings.TrimSpace(line) == r.project, nil
}

// guard is tf.Guard, a refusal being exit 1 with a hint for each refused
// delete that flags left out of this run would keep.
func guard(plan *tf.Plan, allowDelete []string) error {
	err := tf.Guard(plan, allowDelete)
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, h := range infra.DeleteHints(plan, allowDelete) {
		msg += "\nhint: " + h
	}
	return userErr("%s", msg)
}

// prepare writes the root's tfvars, imports and backend configuration.
func prepare(wd *infra.Workdir, spec infra.InstallationSpec, im infra.Imports) (map[string]string, error) {
	vars, err := infra.InstallationVars(spec)
	if err != nil {
		return nil, err
	}
	if err := wd.WriteVars(vars); err != nil {
		return nil, userErr("writing the tfvars: %v", err)
	}
	if err := infra.WriteImports(wd.Root, im); err != nil {
		return nil, userErr("writing the imports: %v", err)
	}
	backend, err := wd.WriteBackend(spec.StateBucket, infra.StatePrefixInstallation)
	if err != nil {
		return nil, userErr("%v", err)
	}
	return backend, nil
}

// install stands up or adopts the installation: discovery, the state
// bucket, the runs bucket's viewers, then plan, guard, confirm and apply,
// and the local config.
func (r *initRun) install(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte) error {
	im, err := infra.DiscoverInstallation(ctx, c, spec)
	if err != nil {
		return initErr(err)
	}
	spec.AdoptLegacyRegistry = im.AdoptLegacyRegistry
	for _, n := range im.Notes {
		r.warn(n)
	}
	backend, err := prepare(wd, spec, im)
	if err != nil {
		return err
	}

	// 3. The state bucket.
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return initErr(err)
	}
	if !exists {
		if err := r.confirm(fmt.Sprintf("creates gs://%s in %s with versioning, for Terraform state (cents a month)", spec.StateBucket, spec.Region),
			"nothing was created or applied"); err != nil {
			return err
		}
		if err := infra.CreateStateBucket(ctx, c, spec.Project, spec.Region, spec.StateBucket); err != nil {
			return initErr(err)
		}
		fmt.Fprintf(r.w, "created gs://%s; project Viewers can't read it\n", spec.StateBucket)
	} else {
		// A removal that failed after the bucket was created is retried,
		// since the state holds every name, account and condition.
		p, err := infra.BucketPolicy(ctx, c, spec.StateBucket)
		if err != nil {
			return initErr(err)
		}
		if grants := infra.ProjectViewerGrants(p); len(grants) > 0 {
			what := fmt.Sprintf("removes project Viewers' read access to gs://%s, which holds the Terraform state (%s)", spec.StateBucket, strings.Join(grants, ", "))
			if r.o.planOnly {
				r.warn("--plan-only changes no IAM; without it, fugaro init " + what)
			} else {
				if err := r.confirm(what, "nothing was applied"); err != nil {
					return err
				}
				if err := infra.RemoveProjectViewers(ctx, c, spec.StateBucket, p); err != nil {
					return initErr(err)
				}
				fmt.Fprintf(r.w, "removed project Viewers' read access to gs://%s\n", spec.StateBucket)
			}
		}
	}

	// 4. The runs bucket's viewers.
	if im.AdoptsRunsBucket() {
		if err := r.runsBucketViewers(ctx, c, spec); err != nil {
			return err
		}
	}

	// 5. Plan, show, guard, summary.
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	if prior, err := t.Output(ctx); err == nil && r.o.registryCleanup == "" {
		// Nothing records a cleanup that was switched on, so say when the
		// default puts it back in dry-run.
		if o, err := infra.DecodeOutputs(prior); err == nil && o.RegistryCleanupDryRun != nil && !*o.RegistryCleanupDryRun {
			r.warn("registry cleanup is on (or off) in the installation, and this plan puts it back to dry-run: pass --registry-cleanup=on or off to keep it")
		}
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return remote(err)
	}
	if changed {
		plan, err := t.Show(ctx, infra.PlanFile)
		if err != nil {
			return remote(err)
		}
		fmt.Fprint(r.w, tf.Summary(plan))
		if err := guard(plan, r.o.allowDelete); err != nil {
			return err
		}
		counts := infra.CountPlan(plan)
		r.res.Changes = &counts
		if r.o.planOnly {
			fmt.Fprintf(r.w, "--plan-only: nothing applied; the plan is %s\n", filepath.Join(wd.Root, infra.PlanFile))
			return nil
		}
		// 6. Confirm, 7. apply the plan shown.
		if err := r.confirm("applies "+counts.String(), "nothing was applied"); err != nil {
			return err
		}
		if err := t.Apply(ctx, infra.PlanFile); err != nil {
			return remote(err)
		}
		r.res.Applied = true
		if !im.AdoptsRunsBucket() {
			// The apply may just have created the runs bucket, with GCS's
			// project Viewer access; it is ours only if discovery says so.
			after, err := infra.DiscoverInstallation(ctx, c, spec)
			if err != nil {
				return initErr(err)
			}
			if after.AdoptsRunsBucket() {
				if err := r.runsBucketViewers(ctx, c, spec); err != nil {
					return err
				}
			}
		}
	} else {
		r.res.Changes = &infra.PlanCounts{}
		fmt.Fprintln(r.w, "No changes: the installation matches the plan.")
		if r.o.planOnly {
			return nil
		}
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	if err != nil {
		return remote(err)
	}
	// 8. The local config.
	return r.writeConfig(lc, spec, outs, path, old, r.res.Applied)
}

// runsBucketViewers offers to remove project Viewers' read access to the
// runs bucket, which discovery found ours (in the project, marked). Under
// --plan-only it only says so; declining is allowed and noted.
func (r *initRun) runsBucketViewers(ctx context.Context, c *infra.Clients, spec infra.InstallationSpec) error {
	p, err := infra.BucketPolicy(ctx, c, spec.RunsBucket)
	if err != nil {
		return initErr(err)
	}
	grants := infra.ProjectViewerGrants(p)
	switch {
	case len(grants) == 0:
		return nil
	case r.o.planOnly:
		r.warn(fmt.Sprintf("--plan-only changes no IAM; without it, fugaro init asks to remove project Viewers' read access to gs://%s (%s)", spec.RunsBucket, strings.Join(grants, ", ")))
		return nil
	}
	ok, err := r.ask(fmt.Sprintf("removes project Viewers' read access to gs://%s, which holds transcripts and caches (%s)", spec.RunsBucket, strings.Join(grants, ", ")))
	if err != nil {
		return err
	}
	if !ok {
		r.warn(fmt.Sprintf("project Viewers can still read gs://%s, transcripts and caches included: the removal was declined; rerun fugaro init to remove it", spec.RunsBucket))
		return nil
	}
	if err := infra.RemoveProjectViewers(ctx, c, spec.RunsBucket, p); err != nil {
		return initErr(err)
	}
	fmt.Fprintf(r.w, "removed project Viewers' read access to gs://%s\n", spec.RunsBucket)
	return nil
}

// configOnly writes the local config alone, from the installation's
// outputs when its state is reachable, else from the flags.
func (r *initRun) configOnly(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte) error {
	outs, err := r.readOutputs(ctx, c, t, wd, spec)
	switch {
	case errors.Is(err, errNoState), errors.Is(err, infra.ErrNoOutputs):
		// Nothing applied yet: only then do the flags stand in.
		host, herr := infra.RegistryHost(lc)
		if herr != nil {
			return initErr(herr)
		}
		r.warn(fmt.Sprintf("the installation has no state yet (%v); the local config is written from the flags", err))
		outs = infra.InstallationOutputs{RunsBucket: spec.RunsBucket, RegistryHost: host, LogView: lc.LogView}
	case err != nil:
		// A state bucket that isn't ours, or a state that can't be read, is
		// a failure, not something to work around.
		return initErr(err)
	}
	return r.writeConfig(lc, spec, outs, path, old, false)
}

// errNoState is a state bucket that doesn't exist yet.
var errNoState = errors.New("no state bucket")

func (r *initRun) readOutputs(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.InstallationSpec) (infra.InstallationOutputs, error) {
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	if !exists {
		return infra.InstallationOutputs{}, fmt.Errorf("%w gs://%s", errNoState, spec.StateBucket)
	}
	backend, err := prepare(wd, spec, infra.Imports{})
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	if err := t.Init(ctx, backend); err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	return infra.DecodeOutputs(raw)
}

// writeConfig writes the local config from the installation's outputs and
// the flags, keeping everything else (repos, the legacy registry, the
// endpoints) and dropping build.service_account. It shows the diff first,
// and backs up the file it replaces. A diff is confirmed by the apply's
// confirmation (confirmed), else it asks on its own.
func (r *initRun) writeConfig(lc *localcfg.Config, spec infra.InstallationSpec, outs infra.InstallationOutputs, path string, old []byte, confirmed bool) error {
	r.res.Outputs = &outs
	next := *lc
	if outs.RunsBucket != "" {
		next.RunsBucket = outs.RunsBucket
	}
	next.RegistryHost = outs.RegistryHost
	next.LogView = outs.LogView
	next.Terraform.StateBucket = spec.StateBucket
	if r.o.launchersChanged {
		next.Terraform.Launchers = slices.Clone(spec.Launchers)
	}
	if r.o.operatorsChanged {
		next.Terraform.Operators = slices.Clone(spec.Operators)
	}
	if r.o.alertEmailChanged {
		next.Terraform.AlertEmail = r.o.alertEmail
	}
	if r.o.baseImageChanged {
		next.BaseImage = r.o.baseImage
	}
	next.Build.ServiceAccount = ""
	switch {
	case r.o.schedulerRegion != "":
		next.SchedulerRegion = r.o.schedulerRegion
	case next.SchedulerRegion == "":
		sr, err := gcp.SchedulerRegion(next.Region)
		if err != nil {
			return userErr("%v", err)
		}
		next.SchedulerRegion = sr
	}
	return r.writeLocalConfig(&next, path, old, confirmed)
}

// writeLocalConfig writes next to path, showing the diff first and backing
// up the file it replaces. A diff is confirmed by an apply's confirmation
// (confirmed), else it asks on its own. An unchanged file isn't written.
func (r *initRun) writeLocalConfig(next *localcfg.Config, path string, old []byte, confirmed bool) error {
	data, err := next.Marshal()
	if err != nil {
		return err
	}
	if _, err := localcfg.Parse(data); err != nil {
		return userErr("the new local config: %v", err)
	}
	r.res.Config = path
	if old != nil && bytes.Equal(old, data) {
		fmt.Fprintf(r.w, "The local config %s is up to date.\n", path)
		return nil
	}
	fmt.Fprintf(r.w, "The local config %s changes:\n%s", path, lineDiff(string(old), string(data)))
	if !confirmed {
		if err := r.confirm("writes the local config "+path+" as shown", "the local config was not changed"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return userErr("%v", err)
	}
	if old != nil {
		backup, err := backupFile(path, old, time.Now())
		if err != nil {
			return userErr("backing up the local config: %v", err)
		}
		r.res.Backup = backup
		fmt.Fprintf(r.w, "backed up the old local config to %s\n", backup)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return userErr("writing the local config: %v", err)
	}
	fmt.Fprintf(r.w, "wrote %s\n", path)
	return nil
}

// backupFile writes old next to path as path.bak-<UTC timestamp>, mode
// 0600, never over an existing file.
func backupFile(path string, old []byte, now time.Time) (string, error) {
	base := path + ".bak-" + now.UTC().Format("20060102T150405Z")
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(old); err != nil {
			f.Close()
			return "", err
		}
		return name, f.Close()
	}
}

// writeFileAtomic replaces path with data (mode 0600) through a rename, so
// a failed write never leaves half a config. A symlinked path is written
// through: its target is replaced and the link stays.
func writeFileAtomic(path string, data []byte) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// lineDiff shows the lines of a that b drops (-) and adds (+), in order,
// from their longest common subsequence.
func lineDiff(a, b string) string {
	x, y := splitLines(a), splitLines(b)
	// lcs[i][j] is the LCS length of x[i:] and y[j:].
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			i, j = i+1, j+1
		case j < len(y) && (i == len(x) || lcs[i][j+1] >= lcs[i+1][j]):
			out.WriteString("  + " + y[j] + "\n")
			j++
		default:
			out.WriteString("  - " + x[i] + "\n")
			i++
		}
	}
	return out.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// forget is the rollback: a guarded apply that turns the log isolation and
// registry cleanup off, then state rm of everything, each confirmed.
func (r *initRun) forget(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.InstallationSpec) error {
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return initErr(err)
	}
	if !exists {
		return userErr("there is no state bucket gs://%s, so no installation state to forget", spec.StateBucket)
	}
	repos, err := infra.RepoStates(ctx, c, spec.StateBucket)
	if err != nil {
		return initErr(err)
	}
	if len(repos) > 0 {
		return userErr("the state bucket still holds repository state (%s; only *.tfstate objects count, not locks): run fugaro init --repo --forget for each repository first", strings.Join(repos, ", "))
	}
	fs := infra.ForgetSpec(spec)
	backend, err := prepare(wd, fs, infra.Imports{})
	if err != nil {
		return err
	}
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	// The rollback keeps everything but the log isolation declared, the
	// adopted legacy registry included (it can't be deleted, and mustn't
	// be), so it takes adopt_legacy_registry from the state's outputs.
	raw, err := t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	switch {
	case errors.Is(err, infra.ErrNoOutputs):
		return userErr("the state in gs://%s holds no installation, so there is nothing to forget", spec.StateBucket)
	case err != nil:
		return remote(err)
	}
	fs.AdoptLegacyRegistry = outs.LegacyRegistry != ""
	if _, err := prepare(wd, fs, infra.Imports{}); err != nil {
		return err
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return remote(err)
	}
	if changed {
		plan, err := t.Show(ctx, infra.PlanFile)
		if err != nil {
			return remote(err)
		}
		fmt.Fprint(r.w, tf.Summary(plan))
		if err := infra.CheckForgetPlan(plan); err != nil {
			return initErr(err)
		}
		if err := guard(plan, infra.ForgetAllowDelete(plan)); err != nil {
			return err
		}
		counts := infra.CountPlan(plan)
		r.res.Changes = &counts
		if err := r.confirm("rollback step 1 of 2: applies "+counts.String()+": deletes the log isolation (the _Default exclusion, the sink, the view and its grants, the log bucket) and turns registry cleanup off",
			"nothing was applied or forgotten"); err != nil {
			return err
		}
		if err := t.Apply(ctx, infra.PlanFile); err != nil {
			return remote(err)
		}
		r.res.Applied = true
		for _, rc := range plan.ResourceChanges {
			if slices.Contains(rc.Change.Actions, "delete") {
				r.res.Deleted = append(r.res.Deleted, rc.Address)
			}
		}
	} else {
		fmt.Fprintln(r.w, "No changes: log isolation and registry cleanup are already off.")
	}
	if err := r.confirm("rollback step 2 of 2: removes every address from Terraform's state (terraform state rm module.installation); it destroys nothing",
		"the state still holds the installation"); err != nil {
		return err
	}
	if err := t.StateRm(ctx, "module.installation"); err != nil {
		return remote(err)
	}
	r.res.Forgotten = true
	fmt.Fprintln(r.w, "Terraform no longer manages the installation; nothing else was destroyed. Restore the backed-up local config and redeploy each job with the M4 binary.")
	if slices.Contains(r.res.Deleted, infra.LogBucketAddress) {
		r.res.Undelete = infra.LogBucketUndelete(spec.Project)
		r.warn(fmt.Sprintf("the log bucket %s is now pending deletion for 7 days, and its ID can't be reused meanwhile; a migration retried within the week runs this first: %s",
			infra.LogBucket, r.res.Undelete))
	}
	return nil
}

func (r *initRun) printResult() error {
	if !r.o.asJSON {
		return nil
	}
	enc := json.NewEncoder(r.cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(r.res)
}

// checkRepo refuses flag combinations that mean nothing for --repo.
func (o *initOptions) checkRepo() error {
	n := 0
	for _, b := range []bool{o.planOnly, o.printVars, o.forget} {
		if b {
			n++
		}
	}
	if n > 1 {
		return userErr("--plan-only, --print-vars and --forget exclude one another")
	}
	if o.forget && (len(o.allowDelete) > 0 || o.allowJobDelete || o.noBuild) {
		return userErr("--forget only removes the repository from Terraform's state; it takes no --allow-delete, --allow-job-delete or --no-build")
	}
	installationOnly := map[string]bool{
		"--config-only": o.configOnly, "--budget": o.budget != 0, "--budget-currency": o.budgetCurrency != "",
		"--billing-account": o.billingAccount != "", "--alert-email": o.alertEmailChanged, "--launcher": o.launchersChanged,
		"--operator": o.operatorsChanged, "--base-image": o.baseImageChanged, "--no-log-isolation": o.noLogIsolation,
		"--registry-cleanup": o.registryCleanup != "", "--runs-bucket": o.runsBucket != "", "--scheduler-region": o.schedulerRegion != "",
	}
	var set []string
	for _, f := range slices.Sorted(maps.Keys(installationOnly)) {
		if installationOnly[f] {
			set = append(set, f)
		}
	}
	if len(set) > 0 {
		return userErr("%s set(s) up the installation, not a repository: run fugaro init with it, then fugaro init --repo", strings.Join(set, ", "))
	}
	return nil
}

// runInitRepo onboards the repository of a checkout: its resources are
// adopted or created, its first images built and its jobs deployed, each
// step confirmed; then the local config records it.
func runInitRepo(cmd *cobra.Command, o *initOptions, args []string) error {
	r := newInitRun(cmd, o)
	if err := o.checkRepo(); err != nil {
		return err
	}
	if !o.printVars {
		if err := refuseHTTP2Debug(os.Getenv); err != nil {
			return err
		}
	}
	ctx := cmd.Context()

	// 1. The checkout, its fugaro.yaml, and the local config.
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	root, cfg, err := loadCheckoutConfigAt(ctx, dir)
	if err != nil {
		return err
	}
	repo, err := checkoutRepo(ctx, root, "")
	if err != nil {
		return err
	}
	repoURL, err := checkoutURL(ctx, root)
	if err != nil {
		return err
	}
	lc, path, old, err := loadRepoConfig(o)
	if err != nil {
		return err
	}
	r.project, r.res.Project, r.res.Repo = lc.Project, lc.Project, repo
	in := infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, RepoURL: repoURL, GitHubAppID: o.githubAppID}
	var spec infra.RepoSpec
	if !o.forget {
		// Every name, before any cloud call: a spec that can't be
		// computed (a GitHub repository without its App ID) stops here.
		if spec, err = infra.Repo(in); err != nil {
			return initErr(err)
		}
	}
	rootOpts := infra.RepoRootOptions{AllowJobDelete: o.allowJobDelete}
	if o.printVars {
		data, err := infra.RepoRootVars(spec, rootOpts)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}

	bin, err := r.checkEnv(lc)
	if err != nil {
		return err
	}
	inst, err := installOptions(o, lc)
	if err != nil {
		return err
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	switch exists, err := infra.CheckStateBucket(ctx, c, lc.Project, inst.StateBucket); {
	case err != nil:
		return initErr(err)
	case !exists:
		return userErr("there is no Terraform state bucket gs://%s, so no installation: run fugaro init first", inst.StateBucket)
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return userErr("%v", err)
	}
	rdir, err := infra.RepoWorkdir(os.Getenv, lc.Project, slug)
	if err != nil {
		return userErr("%v", err)
	}
	wd, t, err := r.terraform(bin, rdir, "repo")
	if err != nil {
		return err
	}
	r.res.Workdir = wd.Dir
	backend, err := wd.WriteBackend(inst.StateBucket, infra.RepoStatePrefix(slug))
	if err != nil {
		return userErr("%v", err)
	}
	if o.forget {
		if err := r.forgetRepo(ctx, c, t, backend, repo, inst.StateBucket, slug); err != nil {
			return err
		}
		return r.printResult()
	}

	// 2. The installation, and its outputs.
	switch ok, err := infra.InstallationStateExists(ctx, c, inst.StateBucket); {
	case err != nil:
		return initErr(err)
	case !ok:
		return userErr("gs://%s holds no installation state (%s/): run fugaro init first", inst.StateBucket, infra.StatePrefixInstallation)
	}
	outs, err := r.installationOutputs(ctx, lc, bin, inst.StateBucket)
	if err != nil {
		return err
	}
	r.res.Outputs = &outs
	in.Installation = outs
	if spec, err = infra.Repo(in); err != nil {
		return initErr(err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}

	// 3–5. Discover, gate, plan, guard, confirm, apply.
	missing, err := r.planRepo(ctx, c, t, wd, spec, rootOpts, true)
	if err != nil {
		return err
	}
	versions, err := infra.SecretVersions(ctx, c, spec)
	if err != nil {
		return initErr(err)
	}
	if !o.planOnly && !o.noBuild {
		// 6. The first image builds, then the plan that deploys them.
		names, err := infra.NeedsBuild(ctx, c, spec, versions)
		if err != nil {
			return initErr(err)
		}
		built, err := r.buildImages(ctx, lc, cfg, spec, names)
		if err != nil {
			return err
		}
		if built > 0 {
			if missing, err = r.planRepo(ctx, c, t, wd, spec, rootOpts, false); err != nil {
				return err
			}
		}
	}

	// 7. What is still missing.
	r.printMissing(spec, versions, missing)
	if o.planOnly {
		return r.printResult()
	}

	// 8. The local config.
	if err := r.writeRepoConfig(lc, spec, cfg, path, old); err != nil {
		return err
	}
	return r.printResult()
}

// loadRepoConfig loads the local config fugaro init wrote, with --project
// and --region applied.
func loadRepoConfig(o *initOptions) (lc *localcfg.Config, path string, old []byte, err error) {
	path = o.cloud.config
	if path == "" {
		if path, err = localcfg.Path(os.Getenv); err != nil {
			return nil, "", nil, userErr("%v", err)
		}
	}
	old, err = os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, "", nil, userErr("there is no local config at %s: run fugaro init first, which sets up the installation and writes it", path)
	case err != nil:
		return nil, "", nil, userErr("reading the local config: %v", err)
	}
	if lc, err = localcfg.Load(path); err != nil {
		return nil, "", nil, userErr("%v", err)
	}
	if err := lc.Override(o.cloud.project, o.cloud.region); err != nil {
		return nil, "", nil, userErr("--project/--region: %v", err)
	}
	return lc, path, old, nil
}

// checkoutURL is the checkout's origin as an https URL without
// credentials, which the builds and the daily check clone.
func checkoutURL(ctx context.Context, root string) (string, error) {
	c := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin")
	c.WaitDelay = 5 * time.Second
	out, err := c.Output()
	if err != nil {
		return "", userErr("no origin remote in the checkout %s", root)
	}
	u := image.HTTPSOrigin(strings.TrimSpace(string(out)))
	if !strings.HasPrefix(u, "https://") {
		return "", userErr("origin %s has no https form to clone from", gcp.RedactURL(u))
	}
	return u, nil
}

// installationOutputs reads the installation root's outputs from its
// state, which is the repository root's input.
func (r *initRun) installationOutputs(ctx context.Context, lc *localcfg.Config, bin, stateBucket string) (infra.InstallationOutputs, error) {
	dir, err := infra.InstallationWorkdir(os.Getenv, lc.Project)
	if err != nil {
		return infra.InstallationOutputs{}, userErr("%v", err)
	}
	wd, t, err := r.terraform(bin, dir, "installation")
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	backend, err := wd.WriteBackend(stateBucket, infra.StatePrefixInstallation)
	if err != nil {
		return infra.InstallationOutputs{}, userErr("%v", err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	switch {
	case errors.Is(err, infra.ErrNoOutputs):
		return infra.InstallationOutputs{}, userErr("the installation's state in gs://%s has no outputs: run fugaro init first", stateBucket)
	case err != nil:
		return infra.InstallationOutputs{}, remote(err)
	}
	return outs, nil
}

// planRepo is one discover, gate, plan, guard, confirm and apply of the
// repository root. It returns what the readiness gates found missing.
// first says whether this is the run's first plan, whose notes are shown.
func (r *initRun) planRepo(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.RepoSpec, opts infra.RepoRootOptions, first bool) ([]infra.Missing, error) {
	im, ex, err := infra.DiscoverRepo(ctx, c, spec)
	if err != nil {
		return nil, initErr(err)
	}
	if first {
		for _, n := range im.Notes {
			r.warn(n)
		}
	}
	ready, missing, err := infra.Readiness(ctx, c, spec, ex)
	if err != nil {
		return nil, initErr(err)
	}
	vars, err := infra.RepoRootVars(ready, opts)
	if err != nil {
		return nil, err
	}
	if err := wd.WriteVars(vars); err != nil {
		return nil, userErr("writing the tfvars: %v", err)
	}
	if err := infra.WriteImports(wd.Root, im); err != nil {
		return nil, userErr("writing the imports: %v", err)
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return nil, remote(err)
	}
	if !changed {
		r.res.Changes = &infra.PlanCounts{}
		fmt.Fprintf(r.w, "No changes: %s matches the plan.\n", spec.Name)
		return missing, nil
	}
	plan, err := t.Show(ctx, infra.PlanFile)
	if err != nil {
		return nil, remote(err)
	}
	if opts.AllowJobDelete {
		fmt.Fprintln(r.w, "⚠ --allow-job-delete: this plan lowers the deletion protection of "+spec.Name+"'s jobs, for offboarding")
	}
	fmt.Fprint(r.w, tf.Summary(plan))
	fmt.Fprintln(r.w, im.Bindings.String())
	if err := guard(plan, r.o.allowDelete); err != nil {
		return nil, err
	}
	counts := infra.CountPlan(plan)
	r.res.Changes = &counts
	if r.o.planOnly {
		fmt.Fprintf(r.w, "--plan-only: nothing applied; the plan is %s\n", filepath.Join(wd.Root, infra.PlanFile))
		return missing, nil
	}
	what := "applies " + counts.String() + " to " + spec.Name
	if !first {
		what += ", deploying the images just built"
	}
	if err := r.confirm(what, "nothing was applied"); err != nil {
		return nil, err
	}
	if err := t.Apply(ctx, infra.PlanFile); err != nil {
		return nil, remote(err)
	}
	r.res.Applied = true
	return missing, nil
}

// buildImages offers the first image build of each workflow in names, and
// submits and waits for each one confirmed. It returns how many were built.
func (r *initRun) buildImages(ctx context.Context, lc *localcfg.Config, cfg *config.Config, spec infra.RepoSpec, names []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	b, err := gcp.NewBuilder(ctx, gcpOptions(lc), lc.BuildRegion())
	if err != nil {
		return 0, remote(err)
	}
	built := 0
	for _, name := range names {
		base := lc.BaseImage
		if base == "" {
			if base, err = image.BaseRef(cfg.Workflows[name].Base, Version); err != nil {
				return built, userErr("%v", err)
			}
		}
		ok, err := r.ask(fmt.Sprintf("submits a Cloud Build for %s/%s on %s, as %s (billable per build-minute: a 13-minute build on E2_HIGHCPU_8 is about $0.21); it builds, smoke-tests and promotes the image into %s and records it",
			spec.Name, name, lc.Build.MachineType, spec.BuildServiceAccountEmail, spec.RegistryPath))
		if err != nil {
			return built, err
		}
		if !ok {
			r.warn(fmt.Sprintf("the first image build of %s/%s was not confirmed, so its job waits for it: build it with fugaro image build --repo %s --workflow %s, then rerun fugaro init --repo", spec.Name, name, spec.Name, name))
			continue
		}
		bs, err := cloudBuildSpec(spec, cfg, name, base, lc.Build.MachineType, "gs://"+spec.Installation.RunsBucket)
		if err != nil {
			return built, err
		}
		res, err := b.Submit(ctx, bs)
		switch {
		case errors.Is(err, gcp.ErrBadBuildSpec):
			return built, userErr("%v", err)
		case err != nil:
			return built, remote(err)
		}
		fmt.Fprintf(r.w, "Cloud Build build %s of %s submitted; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
		done, err := b.Wait(ctx, res.ID, 0)
		if err != nil {
			return built, remote(err)
		}
		fmt.Fprintf(r.w, "built %s/%s (Cloud Build build %s)\n", spec.Name, name, oneLine(done.ID))
		r.res.Builds = append(r.res.Builds, done.ID)
		built++
	}
	return built, nil
}

// printMissing prints what the repository still needs: each secret's fugaro
// secrets set command, as the bootstrap printed them, and each workflow's
// other gates.
func (r *initRun) printMissing(spec infra.RepoSpec, versions map[string]bool, missing []infra.Missing) {
	cmds := infra.SecretCommands(spec, versions)
	var other []string
	for _, m := range missing {
		// Secrets are listed as commands below.
		if m.Kind != infra.MissingSecret {
			other = append(other, m.String())
		}
	}
	if len(cmds) == 0 && len(other) == 0 {
		return
	}
	fmt.Fprintf(r.w, "Still missing for %s:\n", spec.Name)
	for _, o := range other {
		fmt.Fprintln(r.w, "  "+o)
	}
	if len(cmds) > 0 {
		fmt.Fprintln(r.w, "  Store each secret with fugaro secrets set; the value comes from stdin or a hidden prompt, never argv:")
		for _, c := range cmds {
			fmt.Fprintln(r.w, "    "+c)
		}
		fmt.Fprintln(r.w, "  Then rerun fugaro init --repo when they are stored.")
	}
	r.res.Missing = append(other, cmds...)
}

// writeRepoConfig adds the repository to the local config's repos, or
// updates its entry, keeping everything else.
func (r *initRun) writeRepoConfig(lc *localcfg.Config, spec infra.RepoSpec, cfg *config.Config, path string, old []byte) error {
	next := *lc
	next.Repos = maps.Clone(lc.Repos)
	if next.Repos == nil {
		next.Repos = map[string]localcfg.Repo{}
	}
	key := spec.Name
	if want, err := task.CanonicalRepo(spec.Name); err == nil {
		for _, k := range slices.Sorted(maps.Keys(lc.Repos)) {
			if c, err := task.CanonicalRepo(k); err == nil && c == want {
				key = k
				break
			}
		}
	}
	next.Repos[key] = localcfg.Repo{
		Provider:    spec.Provider,
		BaseBranch:  spec.BaseBranch,
		Workflows:   slices.Sorted(maps.Keys(cfg.Workflows)),
		GitHubAppID: spec.GitHubAppID,
	}
	return r.writeLocalConfig(&next, path, old, r.res.Applied)
}

// forgetRepo is the repository's rollback: every address leaves
// Terraform's state, after a confirmation, and nothing is destroyed. The
// emptied state object goes too (the state bucket must be versioned, so it
// can be restored), so the installation's rollback no longer counts the
// repository. It decides from the state's resources, not its outputs
// (state rm leaves those), so a run that stopped after state rm finishes
// on the next one.
func (r *initRun) forgetRepo(ctx context.Context, c *infra.Clients, t *tf.TF, backend map[string]string, repo, stateBucket, slug string) error {
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	st, err := t.ShowState(ctx)
	if err != nil {
		return remote(err)
	}
	managed := st.Managed()
	objects, err := infra.RepoStateObjects(ctx, c, stateBucket, slug)
	if err != nil {
		return remote(err)
	}
	where := "gs://" + stateBucket + "/" + infra.RepoStatePrefix(slug) + "/"
	if !managed && len(objects) == 0 {
		return userErr("the state under %s holds nothing of %s, so there is nothing to forget", where, repo)
	}
	if len(objects) > 0 {
		switch versioned, err := infra.StateBucketVersioned(ctx, c, stateBucket); {
		case err != nil:
			return remote(err)
		case !versioned:
			return userErr("gs://%s has no object versioning, so the state object of %s could not be restored once deleted; turn versioning on (gcloud storage buckets update gs://%s --versioning) and rerun; nothing was forgotten",
				stateBucket, repo, stateBucket)
		}
	}
	what := fmt.Sprintf("removes every address of %s from Terraform's state (terraform state rm module.repo), then deletes its state object under %s (kept as a noncurrent version); it destroys nothing", repo, where)
	if !managed {
		what = fmt.Sprintf("the state of %s manages nothing any more (an earlier --forget stopped after state rm): deletes its state object under %s (kept as a noncurrent version); it destroys nothing", repo, where)
	}
	if err := r.confirm(what, "nothing was forgotten"); err != nil {
		return err
	}
	if managed {
		if err := t.StateRm(ctx, "module.repo"); err != nil {
			return remote(err)
		}
	}
	deleted, err := infra.ForgetRepoState(ctx, c, stateBucket, slug)
	if err != nil {
		return remote(fmt.Errorf("%w; Terraform no longer manages %s, so rerun fugaro init --repo --forget to finish", err, repo))
	}
	r.res.Forgotten = true
	r.res.Deleted = deleted
	fmt.Fprintf(r.w, "Terraform no longer manages %s; nothing was destroyed. Its jobs, accounts, secrets and grants stay as they are.\n", repo)
	return nil
}
