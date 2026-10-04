package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// stdinIsTerminal reports whether in is a terminal, where a confirmation
// can be typed. Tests replace it.
var stdinIsTerminal = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

type initOptions struct {
	cloud                               cloudOptions
	schedulerRegion                     string
	runsBucket, stateBucket             string
	baseImages                          []string
	baseKinds                           []string // --base: base kinds to mirror now
	imageSource                         string   // --image-source: a fork's own release registry/owner
	expectDigests                       []string // --expect-digest KIND=sha256:...: the digest a release tag must resolve to
	replaceImage                        bool     // --replace-image: move a release tag that names another image
	launchers, operators                []string
	budget                              int64
	budgetCurrency, billingAccount      string
	alertEmail                          string
	noLogIsolation                      bool
	registryCleanup                     string
	planOnly, printVars, configOnly     bool
	forget, yes, asJSON                 bool
	nonInteractive                      bool
	allowDelete                         []string
	launchersChanged, operatorsChanged  bool
	alertEmailChanged, baseImageChanged bool

	// --firebase adopts the project's own Firebase project (its ID) and
	// builds the budget backend in it; --budget-mode seeds the mode and
	// --budget-admin adds admins to the owners and editors.
	firebase            string
	budgetMode          string
	budgetAdmins        []string
	budgetAdminsChanged bool

	// name is the project's name, for a project config fugaro init
	// creates; with one, it must be that config's name.
	name string

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
showed once you confirm by typing the project's name (or pass --yes). It
then writes the project config from the installation's outputs. With no
project config yet, --name, --gcp-project and --region say what to create:
projects/<name>.yaml.

It enables the Cloud Resource Manager API when it is disabled (init reads
the project's number through it before Terraform can enable it), creates
the Terraform state bucket first when it doesn't exist, and offers to remove
project Viewers' read access to the runs bucket, each after its own
confirmation. A plan that would delete or replace anything is
refused unless --allow-delete names the address.

init runs as a converge of stages (preflight, installation, and with
--firebase the Firebase backend), each with its own confirmation, in order,
and stops at the first that fails or needs you; a rerun resumes, and one
with nothing to do says "No changes" and exits 0. Exit codes: 0 done (or
only planned), 1 a refusal, or a step left for you (see left_for_you in
--json), 2 a cloud failure; --json prints the stages, left_for_you and the
failed stage on every outcome.

--forget is the rollback: it turns log isolation and registry cleanup off
with a guarded apply, then removes every address from Terraform's state,
destroying nothing else.

init --firebase <firebase-project-id> builds the project's budget backend in a
Firebase project you created and linked to billing. It may be the project's
own GCP project (one project for everything) or a project of its own (init
never creates a project or enables billing, and refuses one that is missing,
has no billing, or whose database holds data and no Fugaro mark). It runs three applies, each
with its own plan and confirmation: the installation (the history account),
the Firebase root (the database, a restricted sign-in key, the token signer,
and the budget admins: the GCP project's owners and editors who are users or
groups, plus --budget-admin), and the installation again (the history job,
once its image exists). Between the last two it deploys the database's rules,
mark, project name, mode (--budget-mode; an absent one is seeded observe) and
largest lease, after a confirmation of their own, then writes the local
config. The same step creates the Firestore database for the spend history
(fugaro report) if the project has none: its location, us-east5, is permanent
(chosen once, never changed or deleted by Fugaro), so init asks you to type
the location to confirm it (--yes confirms it too), and it refuses to adopt a
database that holds data or is in another location; it also deploys
deny-everything Firestore rules and the Fugaro mark. --plan-only stops after the installation's plan. Each repository then
needs fugaro init --repo to pick up the jobs' environment.

init --repo [PATH] onboards the repository of the checkout at PATH (default:
the current directory), whose fugaro.yaml says what it needs, into the
installation fugaro init applied. It adopts what the bootstrap made for it
(checking each resource's marks and live grants first), creates the rest,
and deploys each workflow's job once its secrets are stored and its image is
built. It offers each workflow's first image build (billable, confirmed
separately), then deploys the built image and unpauses the daily check. It
prints the fugaro secrets set commands still needed, and adds the
repository to the local config.

In a checkout of a repository of this project, the converge's secrets stage
asks, at hidden prompts, for the secrets its jobs mount (the git credential,
the Claude credential agent.auth names, none for vertex, the allowed model
providers' keys, the workflows' own), and skips what is already stored. It
prompts only in your own terminal: never with --yes, --non-interactive or
--json (--json never prompts; the stage then exits 1 with the one-line
fugaro secrets set commands), never when stdin, stdout or stderr is not a
terminal (a pipe is never read), and never through a coding agent (CLAUDECODE
and the like are set; CLAUDE_CODE_SSE_PORT is also set by the Claude Code IDE
extension in VS Code and JetBrains terminals: in your own IDE terminal, unset
it or run fugaro secrets set). A pasted private key may have at most 120 lines. It creates each
secret's container, before the repository stage, with the labels
init --repo's Terraform adopts (fugaro, fugaro_repo, fugaro_secret).
--forget removes the repository from
Terraform's state, destroying nothing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			o.launchersChanged, o.operatorsChanged = f.Changed("launcher"), f.Changed("operator")
			o.alertEmailChanged, o.baseImageChanged = f.Changed("alert-email"), f.Changed("base-image")
			o.budgetAdminsChanged = f.Changed("budget-admin")
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
	f.StringVar(&o.name, "name", "", "the project's name: for a project config fugaro init creates, and what names an installation that has no name yet (with a config, it must be its name; an installation's name never changes)")
	f.StringVar(&o.schedulerRegion, "scheduler-region", "", "Cloud Scheduler region of the daily image checks (default: the region, or the nearest one Scheduler offers)")
	f.StringVar(&o.runsBucket, "runs-bucket", "", "the runs bucket (default: the project config's, else fugaro-runs-<gcp-project>)")
	f.StringVar(&o.stateBucket, "state-bucket", "", "the Terraform state bucket (default: the project config's, else fugaro-tfstate-<gcp-project>)")
	f.StringArrayVar(&o.baseImages, "base-image", nil, "a base image the image checks run and builds start from, recorded in the local config under its base kind (repeatable; KIND=IMAGE, or just IMAGE when its repository is named fugaro-<kind>; the kinds you don't name keep theirs)")
	f.StringSliceVar(&o.baseKinds, "base", nil, "base kinds (go, java-services, web-node) whose release image init copies into the project's registry now, besides the ones the checkout's fugaro.yaml names (comma-separated or repeated)")
	f.StringVar(&o.imageSource, "image-source", "", "the registry and owner the release images are copied from (default ghcr.io/dimipaun; a fork names its own, such as ghcr.io/acme)")
	f.StringArrayVar(&o.expectDigests, "expect-digest", nil, "pin the digest of a release image copied by init, KIND=sha256:<hex> (KIND is go, java-services, web-node or history; repeatable): the digest of the source tag's manifest (the index), obtained out of band such as from the release notes. Without it init trusts what the release tag in ghcr.io resolves to now, which is whoever can write that tag")
	f.BoolVar(&o.replaceImage, "replace-image", false, "let init move a release tag in your registry that names another image (a release tag is never moved otherwise; history:latest is moved after its confirmation)")
	f.StringArrayVar(&o.launchers, "launcher", nil, "an IAM member who launches and watches runs (repeatable; default: the local config's)")
	f.StringArrayVar(&o.operators, "operator", nil, "an IAM member who onboards repositories (repeatable; default: the local config's)")
	f.Int64Var(&o.budget, "budget", 0, "a monthly budget on the project, in whole units of --budget-currency")
	f.StringVar(&o.budgetCurrency, "budget-currency", "", "the budget's currency, such as USD")
	f.StringVar(&o.billingAccount, "billing-account", "", "the billing account the budget is on")
	f.StringVar(&o.alertEmail, "alert-email", "", "where failed image checks and rebuilds are reported (default: the local config's)")
	f.BoolVar(&o.noLogIsolation, "no-log-isolation", false, "leave Fugaro job logs in _Default instead of their own log bucket")
	f.StringVar(&o.registryCleanup, "registry-cleanup", "", "Artifact Registry cleanup: dry-run (the default), on or off")
	f.StringVar(&o.firebase, "firebase", "", "adopt this Firebase project (the one you created and linked to billing; init never creates it) and build the budget backend in it: three confirmed applies, the database's rules and mark, the Firestore history database (permanent us-east5 location, typed confirmation) and the config")
	f.StringVar(&o.budgetMode, "budget-mode", "", "with --firebase: off, observe or enforce: the jobs' budget mode in the project config, and for observe and enforce the project-wide mode in the database (an absent one is seeded observe)")
	f.StringArrayVar(&o.budgetAdmins, "budget-admin", nil, "with --firebase: an IAM member who may change caps and kill switches besides the GCP project's owners and editors (repeatable; default: the local config's terraform.budget_admins)")
	f.BoolVar(&o.planOnly, "plan-only", false, "stop after showing the plan")
	f.BoolVar(&o.printVars, "print-vars", false, "print the Terraform variables and exit, with no cloud calls and no Terraform (ungated: no discovery, and with --repo no readiness gates)")
	f.BoolVar(&o.configOnly, "config-only", false, "only write the local config, from the installation's outputs (else the flags)")
	f.BoolVar(&o.forget, "forget", false, "roll back: turn log isolation and registry cleanup off, then remove every address from Terraform's state")
	f.StringArrayVar(&o.allowDelete, "allow-delete", nil, "a resource address the plan may delete or replace (repeatable)")
	f.BoolVar(&o.yes, "yes", false, "confirm every step without asking (only after reading what it will do)")
	f.BoolVar(&o.nonInteractive, "non-interactive", false, "never prompt, and never read stdin: a step that needs you is listed under left_for_you (exit 1), and applying needs --yes, else the run only plans (a fresh state bucket needs its confirmation even then, so that plan exits 1); --yes never covers creating a project, linking billing or a secret. With --forget and --config-only it only stops them prompting: their confirmations then need --yes")
	f.BoolVar(&o.asJSON, "json", false, "print the result as JSON on stdout (progress goes to stderr)")
	f.BoolVar(&o.repo, "repo", false, "onboard the repository of the checkout at PATH (default: the current directory) instead of the installation")
	f.StringVar(&o.githubAppID, "github-app-id", "", "with --repo: the GitHub App's ID, for a GitHub repository (not a secret; recorded in the local config)")
	f.BoolVar(&o.noBuild, "no-build", false, "with --repo: don't offer the first image builds")
	f.BoolVar(&o.allowJobDelete, "allow-job-delete", false, "with --repo: lower the jobs' deletion protection, for offboarding")
	return cmd
}

// initRun is one run of fugaro init.
type initRun struct {
	cmd *cobra.Command
	o   *initOptions
	w   io.Writer // the human-readable account: stdout, or stderr with --json
	in  *bufio.Reader
	// gcpProject is the GCP project ID every Google call uses;
	// projectName is the project's name, shown and typed to confirm.
	gcpProject, projectName string
	res                     initResult

	// init --firebase: the Firebase root's outputs once known (the history
	// job's environment), a change to the local config the run makes, the
	// text appended to the installation apply's confirmation, and whether
	// the history job's missing image has been said.
	fb           *infra.FirebaseOutputs
	mutate       func(*localcfg.Config)
	installLabel string
	historyNoted bool
}

// initResult is what --json prints.
type initResult struct {
	Project    string                     `json:"project"`
	GCPProject string                     `json:"gcp_project"`
	Repo       string                     `json:"repo,omitempty"`
	Workdir    string                     `json:"workdir,omitempty"`
	Changes    *infra.PlanCounts          `json:"changes,omitempty"`
	Applied    bool                       `json:"applied"`
	Outputs    *infra.InstallationOutputs `json:"outputs,omitempty"`
	Config     string                     `json:"config,omitempty"`
	Backup     string                     `json:"backup,omitempty"`
	Forgotten  bool                       `json:"forgotten,omitempty"`
	Deleted    []string                   `json:"deleted,omitempty"`
	Enabled    []string                   `json:"enabled,omitempty"`
	Undelete   string                     `json:"undelete,omitempty"`
	Builds     []string                   `json:"builds,omitempty"`
	Missing    []string                   `json:"missing,omitempty"`
	Warnings   []string                   `json:"warnings,omitempty"`
	// Stages and LeftForYou are the converge's account (initflow): each
	// stage's state, and what only the user can do.
	Stages     []initflow.StageResult `json:"stages,omitempty"`
	LeftForYou []initflow.Left        `json:"left_for_you"`
	// Failed is the stage the converge stopped at, with the one-line fix;
	// Note says why a run only planned.
	Failed *initflow.Failure `json:"failed,omitempty"`
	Note   string            `json:"note,omitempty"`
	// Firebase and RTDBURL are set by init --firebase.
	Firebase string `json:"firebase_project,omitempty"`
	RTDBURL  string `json:"rtdb_url,omitempty"`
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
	lc, path, old, err := loadInitConfig(cmd.Context(), o)
	if err != nil {
		return err
	}
	r.setProject(lc)
	spec, err := installOptions(o, lc)
	if err != nil {
		return err
	}
	if o.printVars {
		data, err := infra.InstallationVars(spec)
		if err != nil {
			return err
		}
		// Printed without a cloud call, so without discovery: stdout stays
		// the tfvars alone.
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+printVarsUngatedInstallation)
		if o.firebase != "" {
			if data, err = r.firebaseVars(data, spec, lc); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+printVarsUngatedFirebase)
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}

	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	if !o.forget && !o.configOnly {
		// The default and --firebase modes: the converge loop over the
		// engines (init_stages.go).
		return e.converge(cmd.Context())
	}

	// 1. The environment, 2. the workdir, the clients and Cloud Resource
	// Manager.
	ctx := cmd.Context()
	if err := e.setup(ctx); err != nil {
		return err
	}
	c, t, wd := e.c, e.t, e.wd
	if o.forget {
		err = r.forget(ctx, c, t, wd, spec)
	} else {
		err = r.configOnly(ctx, c, t, wd, lc, spec, path, old)
	}
	if err != nil {
		return err
	}
	return r.printResult()
}

// checkEnv refuses an environment that would point Terraform or the
// Google tools elsewhere (preflight.Environment, shared with fugaro
// doctor), and finds terraform. It prints the local config's warnings once
// it passes.
func (r *initRun) checkEnv(lc *localcfg.Config) (string, error) {
	for _, c := range preflight.Environment(os.Getenv, lc.GCPProject) {
		if !c.OK {
			return "", userErr("%s; %s", c.Problem, c.Fix)
		}
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
	return gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, Logging: lc.Endpoints.Logging, SecretManager: lc.Endpoints.SecretManager, CloudBuild: lc.Endpoints.CloudBuild,
		NoAuth: lc.Endpoints.NoAuth}}
}

func newInitClients(ctx context.Context, lc *localcfg.Config) (*infra.Clients, error) {
	c, err := infra.NewClients(ctx, gcpOptions(lc),
		infra.Endpoints{IAM: lc.Endpoints.IAM, ArtifactRegistry: lc.Endpoints.ArtifactRegistry,
			Storage: lc.Endpoints.Storage, ResourceManager: lc.Endpoints.ResourceManager, Scheduler: lc.Endpoints.CloudScheduler,
			ServiceUsage: lc.Endpoints.ServiceUsage, Billing: lc.Endpoints.CloudBilling, FirebaseDatabase: lc.Endpoints.FirebaseDatabase})
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
	for _, v := range o.baseImages {
		if _, _, err := localcfg.ParseBaseImageFlag(v); err != nil {
			return userErr("%v", err)
		}
	}
	for _, k := range o.baseKinds {
		if !slices.Contains(config.Bases, k) {
			return userErr("--base %q is not a base kind (%s)", k, strings.Join(config.Bases, ", "))
		}
	}
	if _, err := parseExpectDigests(o.expectDigests); err != nil {
		return userErr("%v", err)
	}
	if o.imageSource != "" {
		if err := mirror.ValidSourcePrefix(o.imageSource); err != nil {
			return userErr("--image-source: %v", err)
		}
	}
	if o.firebase == "" && (o.budgetMode != "" || o.budgetAdminsChanged) {
		return userErr("--budget-mode and --budget-admin are for fugaro init --firebase <firebase-project-id>")
	}
	if o.firebase != "" {
		switch {
		case o.forget || o.configOnly:
			return userErr("--firebase builds the budget backend; it excludes --forget and --config-only")
		case !infra.ValidProjectID(o.firebase):
			return userErr("--firebase %q is not a project ID (6 to 30 of a-z, 0-9 and '-', starting with a letter)", o.firebase)
		case o.budgetMode != "" && !slices.Contains([]string{"off", "observe", "enforce"}, o.budgetMode):
			return userErr("--budget-mode %q must be off, observe or enforce", o.budgetMode)
		}
	}
	if (o.budget != 0 || o.budgetCurrency != "" || o.billingAccount != "") && (o.budget <= 0 || o.budgetCurrency == "" || o.billingAccount == "") {
		return userErr("a budget needs --budget (more than 0), --budget-currency and --billing-account together")
	}
	return nil
}

// setProject records lc's project: its GCP ID for the Google calls, its
// name for the account and the confirmations.
func (r *initRun) setProject(lc *localcfg.Config) {
	r.gcpProject, r.projectName = lc.GCPProject, lc.Name
	r.res.Project, r.res.GCPProject = lc.Name, lc.GCPProject
}

// loadInitConfig selects the project config (localcfg.Select, as fugaro
// init: a missing one is to be created) with --gcp-project, --region and
// --runs-bucket applied; without one, it starts a new one,
// projects/<name>.yaml, from --name (else the name that selected it, such
// as the checkout's project:), --gcp-project and --region. With one,
// --name must be its name. old is the file's content (nil when there is
// none).
func loadInitConfig(ctx context.Context, o *initOptions) (lc *localcfg.Config, path string, old []byte, err error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, "", nil, err
	}
	sel, lc, err := selectFrom(o.cloud, co, true)
	if err != nil {
		return nil, "", nil, err
	}
	if lc != nil {
		// A config to be created is announced once it exists, below.
		if err := announce(o.cloud, sel, lc); err != nil {
			return nil, "", nil, err
		}
	}
	name := sel.Name
	if o.name != "" && name != "" && o.name != name {
		switch sel.From {
		case "checkout":
			return nil, "", nil, userErr("this checkout belongs to project %s; --name says %s", name, o.name)
		case "only project config":
			return nil, "", nil, userErr("the only project config is project %s's; --name says %s (renaming a project isn't supported; to set up another project, pass --project %s too)", name, o.name, o.name)
		}
		return nil, "", nil, userErr("%s selects project %s; --name says %s (renaming a project isn't supported)", sel.From, name, o.name)
	}
	if lc != nil {
		path = sel.Path
		if old, err = os.ReadFile(path); err != nil {
			return nil, "", nil, userErr("reading the project config: %v", err)
		}
	} else {
		name = cmp.Or(name, o.name)
		if name == "" && o.configOnly {
			// --config-only takes the name from the installation's
			// outputs (nameFromOutputs), so the config starts without one.
			if o.cloud.gcpProject == "" || o.cloud.region == "" {
				return nil, "", nil, userErr("there is no project config yet: pass --gcp-project and --region (the project's name comes from the installation), or --name")
			}
			lc, err = newProjectConfig("pending", o.cloud.gcpProject, o.cloud.region)
			if err != nil {
				return nil, "", nil, userErr("--gcp-project/--region: %v", err)
			}
			lc.Name = ""
			if o.runsBucket != "" {
				lc.RunsBucket = o.runsBucket
			}
			return lc, "", nil, nil
		}
		switch {
		case name == "":
			return nil, "", nil, userErr("there is no project config yet: pass --name <name> (the project's name), --gcp-project and --region to create one")
		case !config.ProjectNameRE.MatchString(name):
			return nil, "", nil, userErr("--name %q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", name)
		case o.cloud.gcpProject == "" || o.cloud.region == "":
			return nil, "", nil, userErr("there is no project config for %s yet: pass --gcp-project and --region to create one", name)
		}
		if path = sel.Path; path == "" {
			if path, err = localcfg.ProjectPath(os.Getenv, name); err != nil {
				return nil, "", nil, userErr("%v", err)
			}
		}
		lc, err = newProjectConfig(name, o.cloud.gcpProject, o.cloud.region)
		if err != nil {
			return nil, "", nil, userErr("--name/--gcp-project/--region: %v", err)
		}
		if err := announce(o.cloud, sel, lc); err != nil {
			return nil, "", nil, err
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

// newProjectConfig is the first project config of a name: built as a
// value, so nothing a flag holds can become YAML syntax, and then run
// through the same parse and validation as a file.
func newProjectConfig(name, gcpProject, region string) (*localcfg.Config, error) {
	data, err := (&localcfg.Config{
		Version: 1, Name: name, GCPProject: gcpProject, Region: region, RunsBucket: "fugaro-runs-" + gcpProject,
	}).Marshal()
	if err != nil {
		return nil, err
	}
	return localcfg.Parse(data)
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
	// The installation carries the history account (and, once it can, the
	// sweeper's job) from the first --firebase on, and a plain fugaro init
	// keeps them while the config records the Firebase project.
	opts.BudgetBackend = o.firebase != "" || (lc.Budget != nil && lc.Budget.FirebaseProject != "")
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
// confirmed: by --yes, or by the project's name typed at a terminal. Without a
// terminal and without --yes it refuses; undone says what that leaves.
func (r *initRun) confirm(what, undone string) error {
	ok, err := r.ask(what)
	if err != nil {
		return err
	}
	if !ok {
		if r.o.nonInteractive || !stdinIsTerminal(r.cmd.InOrStdin()) {
			return userErr("this step needs a confirmation: run fugaro init at a terminal and type the project's name, or pass --yes once you have read what it does; %s", undone)
		}
		return userErr("not confirmed (the project's name was not typed); %s", undone)
	}
	return nil
}

// ask shows the banner and reports whether the step is confirmed; without
// a terminal and without --yes it isn't.
func (r *initRun) ask(what string) (bool, error) {
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", r.projectName, r.gcpProject, what)
	if r.o.yes {
		fmt.Fprintln(r.w, "  confirmed by --yes")
		return true, nil
	}
	if r.o.nonInteractive || !stdinIsTerminal(r.cmd.InOrStdin()) {
		return false, nil
	}
	fmt.Fprintf(r.w, "Type %s to apply to GCP project %s: ", r.projectName, r.gcpProject)
	line, err := r.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, userErr("reading the confirmation: %v", err)
	}
	return strings.TrimSpace(line) == r.projectName, nil
}

// resourceManagerRetries are the waits between reads of the project's
// number after Cloud Resource Manager was enabled, while the enable
// propagates: a few minutes in all. Tests replace them.
var resourceManagerRetries = []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
	30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}

// resourceManager makes sure the project's number can be read, which
// discovery and the state bucket check need before any apply. When the
// Cloud Resource Manager API is disabled (a fresh project: Terraform would
// enable it only in the apply), it offers to enable it through Service
// Usage, after its own confirmation, even under --plan-only. Without a
// terminal and without --yes it refuses with the gcloud command that does
// it. Once enabled, it rereads the number until the enable propagates.
func (r *initRun) resourceManager(ctx context.Context, c *infra.Clients) error {
	_, err := infra.ProjectNumber(ctx, c, r.gcpProject)
	var sd *infra.ServiceDisabledError
	if !errors.As(err, &sd) {
		return initErr(err)
	}
	command := "gcloud services enable " + infra.ServiceResourceManager + " --project " + r.gcpProject
	ok, err := r.ask("enables the Cloud Resource Manager API (" + infra.ServiceResourceManager + "), which is disabled: fugaro init reads the project's number through it before Terraform enables it (free, and nothing fugaro does disables it again)")
	if err != nil {
		return err
	}
	if !ok {
		why := "this step needs a confirmation: rerun at a terminal and type the project's name, or pass --yes once you have read what it does, or"
		if !r.o.nonInteractive && stdinIsTerminal(r.cmd.InOrStdin()) {
			why = "not confirmed (the project's name was not typed):"
		}
		return userErr("%s enable the Cloud Resource Manager API yourself with %s and rerun; fugaro init needs it to read the project's number. Nothing was enabled or applied",
			why, command)
	}
	stderr := r.cmd.ErrOrStderr()
	fmt.Fprintf(stderr, "enabling %s in %s...\n", infra.ServiceResourceManager, r.gcpProject)
	if err := infra.EnableService(ctx, c, r.gcpProject, infra.ServiceResourceManager); err != nil {
		return remote(fmt.Errorf("%w; enable it with %s and rerun", err, command))
	}
	r.res.Enabled = append(r.res.Enabled, infra.ServiceResourceManager)
	fmt.Fprintf(r.w, "enabled %s in %s\n", infra.ServiceResourceManager, r.gcpProject)
	for i := 0; ; i++ {
		_, err := infra.ProjectNumber(ctx, c, r.gcpProject)
		if !errors.As(err, &sd) {
			return initErr(err)
		}
		if i == len(resourceManagerRetries) {
			break
		}
		wait := resourceManagerRetries[i]
		fmt.Fprintf(stderr, "waiting for the Cloud Resource Manager API to answer while the enable propagates (%d of %d, next try in %s)\n", i+1, len(resourceManagerRetries), wait)
		select {
		case <-ctx.Done():
			return remote(ctx.Err())
		case <-time.After(wait):
		}
	}
	return remote(fmt.Errorf("%s was enabled in %s but still answers SERVICE_DISABLED: the enable is still propagating; rerun fugaro init in a few minutes", infra.ServiceResourceManager, r.gcpProject))
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
	outs, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	// 8. The local config.
	return r.writeConfig(lc, spec, outs, path, old, r.res.Applied)
}

// installRoot is the installation root's discovery, plan, guard, confirmation
// and apply, and its outputs. stop means the run ends here (--plan-only).
// fugaro init --firebase runs it twice more, as the first and third of its
// applies.
func (r *initRun) installRoot(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec) (outs infra.InstallationOutputs, stop bool, err error) {
	if spec, err = r.historyJob(ctx, c, lc, spec); err != nil {
		return outs, false, err
	}
	im, err := infra.DiscoverInstallation(ctx, c, spec)
	if err != nil {
		return outs, false, initErr(err)
	}
	spec.AdoptLegacyRegistry = im.AdoptLegacyRegistry
	for _, n := range im.Notes {
		r.warn(n)
	}
	backend, err := prepare(wd, spec, im)
	if err != nil {
		return outs, false, err
	}

	// 3. The state bucket.
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return outs, false, initErr(err)
	}
	if !exists {
		if err := r.confirm(fmt.Sprintf("creates gs://%s in %s with versioning, for Terraform state (cents a month)", spec.StateBucket, spec.Region),
			"nothing was created or applied"); err != nil {
			return outs, false, err
		}
		if err := infra.CreateStateBucket(ctx, c, spec.Project, spec.Region, spec.StateBucket); err != nil {
			return outs, false, initErr(err)
		}
		fmt.Fprintf(r.w, "created gs://%s; project Viewers can't read it\n", spec.StateBucket)
	} else {
		// A removal that failed after the bucket was created is retried,
		// since the state holds every name, account and condition.
		p, err := infra.BucketPolicy(ctx, c, spec.StateBucket)
		if err != nil {
			return outs, false, initErr(err)
		}
		if grants := infra.ProjectViewerGrants(p); len(grants) > 0 {
			what := fmt.Sprintf("removes project Viewers' read access to gs://%s, which holds the Terraform state (%s)", spec.StateBucket, strings.Join(grants, ", "))
			if r.o.planOnly {
				r.warn("--plan-only changes no IAM; without it, fugaro init " + what)
			} else {
				if err := r.confirm(what, "nothing was applied"); err != nil {
					return outs, false, err
				}
				if err := infra.RemoveProjectViewers(ctx, c, spec.StateBucket, p); err != nil {
					return outs, false, initErr(err)
				}
				fmt.Fprintf(r.w, "removed project Viewers' read access to gs://%s\n", spec.StateBucket)
			}
		}
	}

	// 5. Plan, show, guard, summary.
	if err := t.Init(ctx, backend); err != nil {
		return outs, false, remote(err)
	}
	// What the installation already is: its name may not change, and an
	// unnamed one is named only on request. Before any plan.
	var prior infra.InstallationOutputs
	havePrior := false
	if raw, err := t.Output(ctx); err == nil {
		// Outputs that exist but can't be read must not pass for "no
		// installation": the name check below would be skipped.
		o, err := infra.DecodeOutputs(raw)
		switch {
		case errors.Is(err, infra.ErrNoOutputs):
			// A fresh state: no prior installation yet.
		case err != nil:
			return outs, false, remote(fmt.Errorf("reading the installation's outputs: %w", err))
		default:
			prior, havePrior = o, true
		}
	}
	if err := checkInstallationName(r.o.name, lc.Name, prior, havePrior); err != nil {
		return outs, false, err
	}
	// 4. The runs bucket's viewers: an IAM change, so only once the name
	// is known to be the installation's.
	if im.AdoptsRunsBucket() {
		if err := r.runsBucketViewers(ctx, c, spec); err != nil {
			return outs, false, err
		}
	}
	if havePrior && prior.RegistryCleanupDryRun != nil && !*prior.RegistryCleanupDryRun && r.o.registryCleanup == "" {
		// Nothing records a cleanup that was switched on, so say when the
		// default puts it back in dry-run.
		r.warn("registry cleanup is on (or off) in the installation, and this plan puts it back to dry-run: pass --registry-cleanup=on or off to keep it")
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return outs, false, remote(err)
	}
	if changed {
		plan, err := t.Show(ctx, infra.PlanFile)
		if err != nil {
			return outs, false, remote(err)
		}
		fmt.Fprint(r.w, tf.Summary(plan))
		if err := guard(plan, r.o.allowDelete); err != nil {
			return outs, false, err
		}
		counts := infra.CountPlan(plan)
		r.res.Changes = &counts
		if r.o.planOnly {
			fmt.Fprintf(r.w, "--plan-only: nothing applied; the plan is %s\n", filepath.Join(wd.Root, infra.PlanFile))
			return outs, true, nil
		}
		// 6. Confirm, 7. apply the plan shown.
		if err := r.confirm("applies "+counts.String()+r.installLabel, "nothing was applied"); err != nil {
			return outs, false, err
		}
		if err := t.Apply(ctx, infra.PlanFile); err != nil {
			return outs, false, remote(err)
		}
		r.res.Applied = true
		if !im.AdoptsRunsBucket() {
			// The apply may just have created the runs bucket, with GCS's
			// project Viewer access; it is ours only if discovery says so.
			after, err := infra.DiscoverInstallation(ctx, c, spec)
			if err != nil {
				return outs, false, initErr(err)
			}
			if after.AdoptsRunsBucket() {
				if err := r.runsBucketViewers(ctx, c, spec); err != nil {
					return outs, false, err
				}
			}
		}
	} else {
		r.res.Changes = &infra.PlanCounts{}
		fmt.Fprintln(r.w, "No changes: the installation matches the plan.")
		if r.o.planOnly {
			return outs, true, nil
		}
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return outs, false, remote(err)
	}
	if outs, err = infra.DecodeOutputs(raw); err != nil {
		return outs, false, remote(err)
	}
	return outs, false, nil
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

// checkInstallationName refuses a run of fugaro init that would rename the
// installation, or name one that has no name by accident. The name is the
// state's project_name output, the project config's name: and --name; all
// that are set must agree (design §2.5). An installation that has outputs
// but no project_name (applied before M9a) is named only by --name: the
// name is permanent, so it is never taken silently from a config. With no
// outputs yet there is no installation to disagree with.
func checkInstallationName(flagName, configName string, outs infra.InstallationOutputs, haveOutputs bool) error {
	if !haveOutputs {
		return nil
	}
	if outs.ProjectName == "" {
		if flagName == "" {
			return userErr("the installation has no project name yet; naming it is permanent, so pass --name %s to give it the project config's name (see docs/design/m9-budget-and-dashboard.md §13.1)", configName)
		}
		return nil
	}
	if outs.ProjectName != configName || (flagName != "" && flagName != outs.ProjectName) {
		return userErr("the installation's project name is %s; renaming isn't supported (design §2.5): the project config says %s and --name says %q", outs.ProjectName, configName, flagName)
	}
	return nil
}

// nameFromOutputs completes the project config --config-only starts
// without a name: the file is projects/<project_name>.yaml, the name the
// installation reports. An existing file for that name is the base (its
// repositories are kept) when it belongs to the same GCP project, and
// refused when it doesn't.
func nameFromOutputs(lc *localcfg.Config, outs infra.InstallationOutputs) (*localcfg.Config, string, []byte, error) {
	name := outs.ProjectName
	if name == "" {
		return nil, "", nil, userErr("the installation has no project name yet; an operator runs fugaro init --name <name> first (see docs/design/m9-budget-and-dashboard.md §13.1)")
	}
	path, err := localcfg.ProjectPath(os.Getenv, name)
	if err != nil {
		return nil, "", nil, userErr("the installation's project name: %v", err)
	}
	existing, _, err := localcfg.LoadProject(os.Getenv, name)
	switch {
	case errors.Is(err, localcfg.ErrMissing):
		next := *lc
		next.Name = name
		return &next, path, nil, nil
	case err != nil:
		return nil, "", nil, userErr("%v", err)
	case existing.GCPProject != lc.GCPProject:
		return nil, "", nil, userErr("%s is project %s's config, for GCP project %s, but --gcp-project says %s; the installation's project name is %s", path, name, existing.GCPProject, lc.GCPProject, name)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil, userErr("reading the project config: %v", err)
	}
	return existing, path, old, nil
}

// configOnly writes the local config alone, from the installation's
// outputs when its state is reachable, else from the flags. The outputs
// name the project: a config without a name takes theirs, and one with a
// name must match it.
func (r *initRun) configOnly(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte) error {
	outs, err := r.readOutputs(ctx, c, t, wd, spec)
	switch {
	case errors.Is(err, errNoState), errors.Is(err, infra.ErrNoOutputs):
		if lc.Name == "" {
			return userErr("there is no project config and no installation state to take the project's name from (%v): pass --name", err)
		}
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
	case lc.Name == "":
		if lc, path, old, err = nameFromOutputs(lc, outs); err != nil {
			return err
		}
		r.setProject(lc)
		printProjectHeader(r.cmd.ErrOrStderr(), lc)
	case outs.ProjectName == "":
		return userErr("the installation has no project name yet; an operator runs fugaro init --name %s first (see docs/design/m9-budget-and-dashboard.md §13.1)", lc.Name)
	case outs.ProjectName != lc.Name:
		return userErr("the installation's project name is %s; renaming isn't supported (design §2.5): project config %s names it %s", outs.ProjectName, path, lc.Name)
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
	if outs.ProjectName != "" && outs.ProjectName != lc.Name {
		return userErr("the installation's project name is %s, but the project config names %s; it isn't rewritten (renaming isn't supported, design §2.5)", outs.ProjectName, lc.Name)
	}
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
		next.BaseImages = maps.Clone(next.BaseImages)
		if next.BaseImages == nil {
			next.BaseImages = map[string]string{}
		}
		for _, v := range r.o.baseImages {
			kind, ref, err := localcfg.ParseBaseImageFlag(v)
			if err != nil {
				return userErr("%v", err)
			}
			next.BaseImages[kind] = ref
			if n := localcfg.BaseImageNameKind(ref); n != kind {
				r.warn(fmt.Sprintf("--base-image %s names an image whose repository is not fugaro-%s: make sure it is the %s base image", v, kind, kind))
			}
		}
	}
	next.Build.ServiceAccount = ""
	if r.mutate != nil {
		r.mutate(&next)
	}
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
	if r.res.LeftForYou == nil {
		r.res.LeftForYou = []initflow.Left{} // an array, never null
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
		"--operator": o.operatorsChanged, "--base-image": o.baseImageChanged, "--base": len(o.baseKinds) > 0, "--image-source": o.imageSource != "", "--expect-digest": len(o.expectDigests) > 0, "--replace-image": o.replaceImage, "--no-log-isolation": o.noLogIsolation,
		"--registry-cleanup": o.registryCleanup != "", "--runs-bucket": o.runsBucket != "", "--scheduler-region": o.schedulerRegion != "",
		"--firebase": o.firebase != "", "--budget-mode": o.budgetMode != "", "--budget-admin": o.budgetAdminsChanged,
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
	if err := o.requireCheckoutProject(ctx, dir); err != nil {
		return err
	}
	root, cfg, err := loadCheckoutConfigAt(ctx, dir)
	if err != nil {
		return err
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return err
	}
	repoURL, err := checkoutURL(ctx, root)
	if err != nil {
		return err
	}
	lc, path, old, err := loadRepoConfig(ctx, o, dir)
	if err != nil {
		return err
	}
	r.setProject(lc)
	r.res.Repo = repo
	if !o.forget {
		if err := checkVertexBudget(lc, cfg); err != nil {
			return err
		}
	}
	if !o.forget {
		w := r.w
		if o.printVars {
			w = cmd.ErrOrStderr() // stdout stays the tfvars alone
		}
		printCeiling(w, lc, cfg)
	}
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
		// Printed without a cloud call, so without discovery or the
		// readiness gates: stdout stays the tfvars alone.
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+printVarsUngated)
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
	if err := r.resourceManager(ctx, c); err != nil {
		return err
	}
	switch exists, err := infra.CheckStateBucket(ctx, c, lc.GCPProject, inst.StateBucket); {
	case err != nil:
		return initErr(err)
	case !exists:
		return userErr("there is no Terraform state bucket gs://%s, so no installation: run fugaro init first", inst.StateBucket)
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return userErr("%v", err)
	}
	rdir, err := infra.RepoWorkdir(os.Getenv, lc.GCPProject, slug)
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
	if err := checkRepoProject(cfg.Project, outs.ProjectName); err != nil {
		return err
	}
	if w := baseProjectWarning(ctx, root, cfg.Git.BaseBranch, outs.ProjectName); w != "" {
		r.warn(w)
	}
	for _, w := range infra.BaseImageWarnings(lc.BaseImages, outs) {
		r.warn(w)
	}
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
			// The first apply already made the repository's resources, so
			// it joins the local config (and says what it still needs)
			// before the build's failure ends the run.
			r.printMissing(spec, versions, missing)
			if cerr := r.writeRepoConfig(lc, spec, cfg, path, old); cerr != nil {
				r.warn(fmt.Sprintf("the local config was not updated: %v", cerr))
			}
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

// requireCheckoutProject refuses, before any cloud call, a checkout whose
// fugaro.yaml has no project:, naming what to add when a project config is
// selectable.
func (o *initOptions) requireCheckoutProject(ctx context.Context, dir string) error {
	co, err := checkoutProject(ctx, dir)
	if err != nil || co == nil || co.Project != "" {
		return err
	}
	name := "<name>"
	if _, lc, err := selectFrom(o.cloud, nil, false); err == nil && lc != nil {
		name = lc.Name
	}
	return userErr("fugaro.yaml has no `project:`; add `project: %s` (fugaro config example shows it)", name)
}

// vertexBudgetRefusal is the refusal of a Vertex workflow under a budget
// that enforces: the gateway can't yet be relied on to cap Vertex spend.
const vertexBudgetRefusal = runner.VertexBudgetRefusal

// checkVertexBudget refuses a repository whose fugaro.yaml authenticates
// the agent through Vertex AI (agent.auth: vertex) under a project budget
// in enforce mode. observe and off are allowed.
func checkVertexBudget(lc *localcfg.Config, cfg *config.Config) error {
	if cfg.Agent.Auth != "vertex" {
		return nil
	}
	// The ceiling's mode and the file's, the stricter of the two, as the
	// runner merges them.
	if policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode == policy.ModeEnforce {
		return userErr("%s", vertexBudgetRefusal)
	}
	return nil
}

// printVarsUngated is init --repo --print-vars's warning: the values it
// prints skip discovery and the readiness gates.
const printVarsUngated = "these values are ungated: no discovery or readiness check ran, so every workflow has deploy_job = true (and so does the check), " +
	"its job uses the new image path (which may not be built yet), and its account gets the new display name " +
	"(an adopted bootstrap account would be renamed); adopt a repository through fugaro init --repo, not by applying these"

// printVarsUngatedInstallation is init --print-vars's warning: the values
// it prints skip discovery.
const printVarsUngatedInstallation = "these values are ungated: no discovery ran, so adopt_legacy_registry is false even when the bootstrap's legacy registry exists and is ours " +
	"(a plan from these values would try to create it, and nothing imports what exists); set up the installation through fugaro init, not by applying these"

// loadRepoConfig selects the project config fugaro init wrote, with
// --gcp-project and --region applied. The checkout that selects it is
// dir's, the one being onboarded, not the working directory's.
func loadRepoConfig(ctx context.Context, o *initOptions, dir string) (lc *localcfg.Config, path string, old []byte, err error) {
	co, err := checkoutProject(ctx, dir)
	if err != nil {
		return nil, "", nil, err
	}
	sel, lc, err := selectFrom(o.cloud, co, false)
	if err != nil {
		return nil, "", nil, err
	}
	if err := announce(o.cloud, sel, lc); err != nil {
		return nil, "", nil, err
	}
	if o.name != "" && o.name != lc.Name {
		return nil, "", nil, userErr("--name %s is not the selected project %s", o.name, lc.Name)
	}
	if old, err = os.ReadFile(sel.Path); err != nil {
		return nil, "", nil, userErr("reading the project config: %v", err)
	}
	return lc, sel.Path, old, nil
}

// checkoutRepo returns the checkout's owner/name, from its origin.
func checkoutRepo(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin")
	cmd.WaitDelay = 5 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &ExitError{Code: ExitUserError, Err: fmt.Errorf("reading the checkout's origin: %w: %s", err, strings.TrimSpace(stderr.String()))}
	}
	repo, ok := repoFromOrigin(strings.TrimSpace(string(out)))
	if !ok {
		return "", &ExitError{Code: ExitUserError, Err: errors.New("cannot tell the repository (owner/name) from the checkout's origin")}
	}
	return repo, nil
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
// state, which is the repository root's input. It runs in a workdir of its
// own, leaving the installation's workdir as it is.
func (r *initRun) installationOutputs(ctx context.Context, lc *localcfg.Config, bin, stateBucket string) (infra.InstallationOutputs, error) {
	dir, err := infra.InstallationOutputsWorkdir(os.Getenv, lc.GCPProject)
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
	// The rollback's state rm leaves the root's outputs behind, so outputs
	// alone don't say the installation is managed: its resources do.
	st, err := t.ShowState(ctx)
	if err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	if !st.Managed() {
		return infra.InstallationOutputs{}, userErr("the installation's state in gs://%s manages nothing (the installation was forgotten, by fugaro init --forget): run fugaro init first", stateBucket)
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
	// Every job carries the project's name, which only the installation's
	// outputs give: init --name names an installation first.
	if outs.ProjectName == "" {
		return infra.InstallationOutputs{}, userErr("the installation has no project name; an operator runs fugaro init --name %s first (see docs/design/m9-budget-and-dashboard.md §13.1)", lc.Name)
	}
	// An installation applied before the tag mover role existed doesn't
	// output it; the repository's grant of it would fail at apply.
	if outs.RoleIDs.TagMover == "" {
		return infra.InstallationOutputs{}, userErr("the installation's state in gs://%s predates the tag mover role (its outputs have no role_ids.tag_mover): run fugaro init first", stateBucket)
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
		base := lc.BaseImage(cfg.Workflows[name].Base)
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
		bs, err := cloudBuildSpec(spec, cfg, name, base, lc.Build.MachineType, lc.RecordBucketURL())
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
		Vertex:      spec.UsesVertex(),
	}
	if spec.UsesVertex() && !lc.UsesVertex() {
		// The installation enables the API from the recorded repositories,
		// and this is the first.
		r.warn(spec.Name + " authenticates its agent through Vertex AI, which the installation has not enabled: rerun fugaro init once the local config records it, which enables the Vertex AI API")
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

// printCeiling says what init --repo is about to apply as the repository's
// ceiling (the project config's budget) and, when the checkout's fugaro.yaml
// has a budget block, which of its keys the ceiling would clamp.
func printCeiling(w io.Writer, lc *localcfg.Config, cfg *config.Config) {
	ceiling := ceilingLayer(lc)
	if !reflect.DeepEqual(ceiling, policy.Layer{}) {
		fmt.Fprintf(w, "Budget ceiling for %s from project %s: %s\n", cfg.Project, lc.Name, ceilingText(ceiling))
	}
	if !setsPolicy(cfg) {
		return
	}
	for _, ig := range policy.Merge(ceiling, runner.FileLayer(cfg)).Ignored {
		p := clampWarning(ig)
		fmt.Fprintf(w, "  fugaro.yaml %s: %s\n", p.Path, p.Message)
	}
}
