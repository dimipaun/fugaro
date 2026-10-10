package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	billing "google.golang.org/api/cloudbilling/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/task"
)

// doctorCheck is one read-only check's result, in the shape `doctor --json`
// prints: preflight.Check's fields, plus Severity for a plugin pin or
// install state ("warning" or "info"; "" is an ordinary check, which always
// fails doctor when it isn't ok). An informational state never fails
// doctor; a warning one fails it only with --strict (design m11-setup-and-skills.md §4.5).
type doctorCheck struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Problem  string `json:"problem,omitempty"`
	Fix      string `json:"fix,omitempty"`
	Severity string `json:"severity,omitempty"`
}

func fromPreflight(c preflight.Check) doctorCheck {
	return doctorCheck{ID: c.ID, OK: c.OK, Problem: c.Problem, Fix: c.Fix}
}

// doctorProject is the installation doctor names, read from the local
// config only (never a cloud call by itself). BaseImages are what the local
// config records (fugaro init --base-image, or a future mirror stage): doctor
// reports what is recorded, not whether the registry still carries it.
type doctorProject struct {
	Name         string            `json:"name"`
	GCPProject   string            `json:"gcp_project"`
	Region       string            `json:"region"`
	RegistryHost string            `json:"registry_host,omitempty"`
	BaseImages   map[string]string `json:"base_images,omitempty"`
}

// doctorFugaroYAML is the validity of a checkout's fugaro.yaml.
type doctorFugaroYAML struct {
	Path     string           `json:"path"`
	Valid    bool             `json:"valid"`
	Problems []config.Problem `json:"problems,omitempty"`
}

// doctorOutput is `fugaro doctor --json`.
type doctorOutput struct {
	OK         bool              `json:"ok"`
	Checks     []doctorCheck     `json:"checks"`
	Project    *doctorProject    `json:"project,omitempty"`
	FugaroYAML *doctorFugaroYAML `json:"fugaro_yaml,omitempty"`
	// Secrets are names and metadata only, never a value (gcp.Secrets.List
	// never reads one back): best-effort, present only inside a checkout
	// whose repository and Secret Manager are both reachable.
	Secrets []secretEntry     `json:"secrets,omitempty"`
	Plugin  pluginwire.Report `json:"plugin"`
	// Release is the binary's tag and commit, and the command that checks the
	// tag still names the commit (no network call here).
	Release *releaseInfo `json:"release,omitempty"`
	// Error is why doctor could not run its checks (an unreadable or invalid
	// local config, no credentials), so --json prints an object on every outcome.
	Error string `json:"error,omitempty"`
}

// doctorLookPath finds executables for doctor's checks; a package variable so
// tests never depend on what the machine running them has installed.
var doctorLookPath = exec.LookPath

func newDoctorCmd() *cobra.Command {
	var (
		cloud    cloudOptions
		dir      string
		plugin   bool
		checkApp bool
		strict   bool
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "doctor [--json] [--plugin] [--strict]",
		Short: "Check that Fugaro is set up and ready, without changing anything",
		Long: `doctor only ever reads: the environment, Terraform, the local project config
and its installation's billing and IAM policy (with a budget backend, who can
sign sign-in tokens for it: warnings, never changes), the Fugaro plugin's wiring in
.claude/settings.json, a checkout's fugaro.yaml and its stored secrets by
name. With --check-github-app, in your own terminal, it also asks GitHub whether
each GitHub App the config records is installed on its repository with the
permissions Fugaro's runs ask for: that is the one thing that reads a secret's
value (the App's private key, with your own credentials, held in memory only to
sign, never printed), and it is refused in a coding agent's session. Otherwise
it never prints a secret's value and never creates, enables or
changes anything.

--plugin checks only the plugin's wiring: offline, no credentials, safe for
CI. --strict makes a stale, unpinned, foreign or unwired plugin fail the
command; the informational states ("not installed", "cannot compare") never
do, with or without --strict. A repository's CI runs
fugaro doctor --plugin --strict.

With no local Fugaro installation configured, doctor says so and names
fugaro init as the fix, rather than guessing at a project.

Exit 1 when a check fails, with the one-line fix to run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, cloud, dir, plugin, strict, asJSON, checkApp)
		},
	}
	addCloudFlags(cmd, &cloud)
	cmd.Flags().StringVar(&dir, "dir", ".", "a directory in the checkout, for the plugin wiring")
	cmd.Flags().BoolVar(&plugin, "plugin", false, "check only the plugin's wiring (offline, no credentials)")
	cmd.Flags().BoolVar(&checkApp, "check-github-app", false, "also check each recorded GitHub App is installed with the permissions runs ask for: reads the App's private key from Secret Manager with your own credentials, in memory only, for one signed token (refused in a coding agent's session; GODEBUG=http2debug would print the token, so unset it)")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail on a stale, unpinned, foreign or unwired plugin (CI mode)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runDoctor(cmd *cobra.Command, cloudOpts cloudOptions, dir string, pluginOnly, strict, asJSON, checkApp bool) error {
	ctx := cmd.Context()
	// Before anything is read: the key is never read in a coding agent's session.
	if checkApp {
		if err := refuseAppKeyReadInAgent(os.Getenv); err != nil {
			return err
		}
	}

	var pluginReport pluginwire.Report
	var pluginChecks []doctorCheck
	if loc, ok := pluginwire.Locate(dir); ok {
		pluginReport = pluginwire.Status(loc.Settings, Version, installedPlugins())
		if pluginOnly && strict {
			// CI mode: a runner has no Claude Code install, so "installed
			// differs", "not installed" are not evaluated (design §4.5).
			pluginReport.Install, pluginReport.Version = "", ""
		}
		pluginChecks = pluginDoctorChecks(pluginReport)
		// Informational only, whatever --strict says: what else the settings
		// file carries is the repository's business to review, and a CI that
		// failed on every hook would be turned off.
		for i, n := range pluginwire.NoticesFor(loc.Settings) {
			pluginChecks = append(pluginChecks, doctorCheck{ID: fmt.Sprintf("plugin-settings-%d", i+1), Severity: "info", Problem: n,
				Fix: "review " + pluginwire.Printable(loc.Settings) + " (git blame it): wiring the plugin blesses the whole file"})
		}
		pluginChecks = append(pluginChecks, doctorCheck{ID: "plugin-settings-caveat", Severity: "info", Problem: pluginwire.NoticesCaveat,
			Fix: "read " + pluginwire.Printable(loc.Settings) + " and the files beside it (.claude/skills, .claude/commands, .mcp.json) yourself"})
	} else {
		// Not inside a checkout there is no wiring to find: a warning (so
		// --strict fails, as update-skills --check does), never silence.
		pluginChecks = []doctorCheck{{ID: "plugin-pin", Severity: "warning",
			Problem: pluginwire.Printable(dir) + " is not in a checkout (no .git above it), so the Fugaro plugin wiring cannot be checked",
			Fix:     "run doctor inside the repository, or pass --dir"}}
	}
	o := doctorOutput{Plugin: pluginReport, Checks: pluginChecks, Release: release()}

	if pluginOnly {
		o.OK = !checksFail(o.Checks, strict)
		return emitDoctor(cmd, o, asJSON)
	}

	// Distinguishing "no installation at all" from every other selection
	// refusal (ambiguous projects, a wrong --project) needs the raw list,
	// not localcfg.Select's wrapped error (design: "no installation means
	// run fugaro init first").
	// A checkout that names its gcp_project:, or --gcp-project, reaches
	// selection on a fresh machine: the shared config may stand in.
	namesGCP := cloudOpts.gcpProject != ""
	if co, err := checkoutProject(ctx, ""); err == nil && co != nil && co.GCPProject != "" {
		namesGCP = true
	}
	if names, err := localcfg.Projects(os.Getenv); err == nil && len(names) == 0 && !namesGCP {
		o.Checks = append(o.Checks, doctorCheck{ID: "installation", OK: false,
			Problem: "no Fugaro installation is configured",
			Fix:     "run fugaro init"})
		o.OK = !checksFail(o.Checks, strict)
		return emitDoctor(cmd, o, asJSON)
	}
	// failed is a refusal after the checks began: --json still prints the
	// object, with the reason; the error is returned as it was.
	failed := func(err error) error {
		if asJSON {
			o.OK, o.Error = false, err.Error()
			if o.Checks == nil {
				o.Checks = []doctorCheck{}
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(o)
		}
		return err
	}
	sel, lc, err := selectProject(ctx, cloudOpts)
	if err != nil {
		return failed(err)
	}
	o.Checks = append(o.Checks, sharedConfigChecks(ctx, sel, lc, time.Now())...)
	if co, err := checkoutProject(ctx, ""); err == nil && needsAnchorHint(ctx, co, lc) {
		o.Checks = append(o.Checks, doctorCheck{ID: "gcp-project-line", Severity: "info", Problem: anchorHintText,
			Fix: "fugaro init --anchor writes the gcp_project line into fugaro.yaml after checking the job images"})
	}

	for _, c := range preflight.Environment(os.Getenv, lc.GCPProject) {
		o.Checks = append(o.Checks, fromPreflight(c))
	}
	o.Checks = append(o.Checks, tempXDGChecks(os.Getenv)...)
	_, tf := preflight.Terraform(doctorLookPath)
	o.Checks = append(o.Checks, fromPreflight(tf))
	// preflight.Docker is deliberately not run here: it is only meaningful
	// with needed=true (a local base-image build is actually pending), and
	// nothing yet computes that (it depends on the image mirror stage, M11
	// T2, which hasn't landed). Calling it with needed=false would always
	// report ok, which is not a check at all; wire it in once that state
	// exists.

	crmSvc, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return failed(remote(err))
	}
	// Billing is read with the credentials' own quota project, not the
	// installation's: the same rule as init's billing reads.
	billingSvc, err := billing.NewService(ctx, doctorAPIOptsQuota(lc, lc.Endpoints.CloudBilling, false)...)
	if err != nil {
		return failed(remote(err))
	}
	sameProjectFirebase := lc.Budget == nil || lc.Budget.FirebaseProject == "" || lc.Budget.FirebaseProject == lc.GCPProject
	for _, c := range preflight.Billing(ctx, billingSvc, lc.GCPProject) {
		dc := fromPreflight(c)
		if c.ID == "billing-api" && !lc.Endpoints.NoAuth && infra.CredentialsQuotaProject(ctx) == "" {
			// Credentials with no quota project: say so, and never suggest the
			// project being checked.
			dc.Problem, dc.Fix = infra.NoQuotaProjectProblem(lc.GCPProject), "gcloud auth application-default set-quota-project <your-project>"
		}
		o.Checks = append(o.Checks, dc)
	}
	for _, c := range preflight.IAMPolicy(ctx, crmSvc, lc.GCPProject, sameProjectFirebase) {
		o.Checks = append(o.Checks, fromPreflight(c))
	}

	if lc.Budget != nil && lc.Budget.FirebaseProject != "" {
		iamSvc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
		if err != nil {
			return failed(remote(err))
		}
		o.Checks = append(o.Checks, doctorTokenSigners(ctx, lc, crmSvc, iamSvc)...)
	}

	if checkApp {
		o.Checks = append(o.Checks, doctorGitHubApps(ctx, lc, cmd.ErrOrStderr())...)
	}

	o.Project = &doctorProject{Name: lc.Name, GCPProject: lc.GCPProject, Region: lc.Region, RegistryHost: lc.RegistryHost, BaseImages: lc.BaseImages}

	if co, _ := checkoutProject(ctx, ""); co != nil {
		if c, fy := fugaroYAMLCheck(ctx, co.Root, lc); c != nil {
			o.Checks = append(o.Checks, *c)
			o.FugaroYAML = fy
			if fy.Valid {
				o.Checks = append(o.Checks, doctorLayerChecks(ctx, lc, co.Root)...)
			}
		}
		o.Secrets = doctorSecrets(cmd, lc)
	}

	o.OK = !checksFail(o.Checks, strict)
	return emitDoctor(cmd, o, asJSON)
}

// doctorGitHubApps is the GitHub App pre-check (githubapp.go), only behind
// --check-github-app, for the GitHub
// repositories the local config records with an App: the checkout's own when
// doctor runs in one, else each of them. A check that could not be made is a
// warning that does not claim the App is fine.
func doctorGitHubApps(ctx context.Context, lc *localcfg.Config, notice io.Writer) []doctorCheck {
	var repos []string
	for name, r := range lc.Repos {
		if r.Provider == gitprov.KindGitHub && r.GitHubAppID != "" {
			repos = append(repos, name)
		}
	}
	slices.Sort(repos)
	if root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel"); err == nil {
		if oi, ok := readOrigin(ctx, root); ok {
			repos = slices.DeleteFunc(repos, func(name string) bool { return !sameRepo(name, oi.Repo) })
		}
	}
	var out []doctorCheck
	for _, repo := range repos {
		slug, err := task.Slug(gitprov.KindGitHub, repo)
		if err != nil {
			continue
		}
		c := checkGitHubApp(ctx, lc, repo, lc.Repos[repo].GitHubAppID, appKeySource{Read: true, SecretID: gcp.SecretID(slug, "github-app-key"), Notice: notice})
		id := "github-app:" + repo
		switch c.Verdict {
		case appFine:
			out = append(out, doctorCheck{ID: id, OK: true})
		case appFailed:
			out = append(out, doctorCheck{ID: id, OK: false, Problem: c.Problem, Fix: c.Fix})
		default:
			out = append(out, doctorCheck{ID: id, OK: false, Severity: "warning", Problem: c.Problem,
				Fix: "rerun doctor as a project owner with GitHub reachable, or look at the App's installation by hand (Settings, GitHub Apps, Configure)"})
		}
		for i, w := range c.Warnings {
			out = append(out, doctorCheck{ID: fmt.Sprintf("github-app-permissions-%d:%s", i+1, repo), OK: false, Severity: "warning", Problem: w,
				Fix: "remove what Fugaro does not need in the App's permissions (GitHub, Settings, Developer settings, GitHub Apps, the App, Permissions & events)"})
		}
	}
	return out
}

// doctorAPIOpts are the client options for a Google API doctor reads from,
// pointed at endpoint (lc's fake, in a test) or Google's own ("") and
// authenticated as the caller (ADC, or none under lc's no_auth).
func doctorAPIOpts(lc *localcfg.Config, endpoint string) []option.ClientOption {
	return doctorAPIOptsQuota(lc, endpoint, true)
}

// doctorAPIOptsQuota is doctorAPIOpts, naming the installation's GCP project
// as the quota project only when withQuota.
func doctorAPIOptsQuota(lc *localcfg.Config, endpoint string, withQuota bool) []option.ClientOption {
	opts := []option.ClientOption{option.WithLogger(slog.New(slog.DiscardHandler))}
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}
	if lc.Endpoints.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	} else if withQuota && lc.GCPProject != "" {
		opts = append(opts, option.WithQuotaProject(lc.GCPProject))
	}
	return opts
}

// fugaroYAMLCheck reads root/fugaro.yaml and checks it (parseCheckoutFugaroYAML
// and config.Check, as fugaro validate does), nil when there is no such file.
func fugaroYAMLCheck(ctx context.Context, root string, lc *localcfg.Config) (*doctorCheck, *doctorFugaroYAML) {
	path := filepath.Join(root, "fugaro.yaml")
	data, err := readFugaroYAML(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		// Present and not a file to read (a link, a FIFO, too large): reported,
		// never followed.
		return &doctorCheck{ID: "fugaro-yaml", OK: false, Problem: oneLineCLI(err.Error()),
			Fix: "replace it with a regular fugaro.yaml of the repository's own"}, &doctorFugaroYAML{Path: path, Problems: []config.Problem{{Message: "not read"}}}
	}
	cfg, problems := parseCheckoutFugaroYAML(ctx, data, lc)
	if cfg != nil {
		problems = append(problems, config.Check(cfg, root)...)
	}
	fy := &doctorFugaroYAML{Path: path, Valid: len(problems) == 0, Problems: problems}
	if len(problems) == 0 {
		return &doctorCheck{ID: "fugaro-yaml", OK: true}, fy
	}
	return &doctorCheck{ID: "fugaro-yaml", OK: false,
		Problem: fmt.Sprintf("%s has %d problem(s)", path, len(problems)),
		Fix:     "run fugaro validate to see them"}, fy
}

// doctorSecrets lists the checkout's repository's stored secrets by name,
// best-effort: nil when the repository or Secret Manager isn't reachable
// (there is no fugaro.yaml declaring secrets yet, no origin, or no
// credentials), never a hard failure, and never a value (gcp.Secrets.List
// never reads one back).
func doctorSecrets(cmd *cobra.Command, lc *localcfg.Config) []secretEntry {
	ctx := cmd.Context()
	env := &cloudEnv{lc: lc, gcp: gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region,
		Endpoints: gcp.Endpoints{SecretManager: lc.Endpoints.SecretManager, NoAuth: lc.Endpoints.NoAuth}}}
	r, err := resolveSecretRepo(cmd, env, "")
	if err != nil {
		return nil
	}
	sm, err := gcp.NewSecrets(ctx, env.gcp)
	if err != nil {
		return nil
	}
	list, err := sm.List(ctx, map[string]string{gcp.LabelRepo: r.label})
	if err != nil {
		return nil
	}
	entries := make([]secretEntry, 0, len(list))
	for _, s := range list {
		entries = append(entries, secretEntry{Name: s.Labels[gcp.LabelSecret], SecretInfo: s})
	}
	slices.SortFunc(entries, func(a, b secretEntry) int {
		return strings.Compare(a.Name+"\x00"+a.ID, b.Name+"\x00"+b.ID)
	})
	return entries
}

// pluginDoctorChecks turns a plugin report's states into doctor checks.
func pluginDoctorChecks(r pluginwire.Report) []doctorCheck {
	var out []doctorCheck
	add := func(id string, s pluginwire.State) {
		if s == "" {
			return
		}
		c := doctorCheck{ID: id, OK: s == pluginwire.OK, Fix: s.Fix()}
		switch s.Severity() {
		case pluginwire.SeverityInfo:
			c.Severity = "info"
		case pluginwire.SeverityWarning:
			c.Severity = "warning"
		}
		if s != pluginwire.OK {
			c.Problem = pluginStateProblem(id, s, r)
		}
		out = append(out, c)
	}
	add("plugin-pin", r.Pin)
	add("plugin-install", r.Install)
	return out
}

// pluginStateProblem is the one-line problem text for a plugin state, the
// same wording update-skills --check and the staleness warning use.
func pluginStateProblem(id string, s pluginwire.State, r pluginwire.Report) string {
	switch {
	case id == "plugin-pin" && r.Detail != "":
		return r.Detail
	case id == "plugin-install" && s == pluginwire.InstalledDiffers:
		return fmt.Sprintf("the installed plugin is %s, the pin is %s", r.Version, r.Ref)
	}
	return string(s)
}

// checksFail reports whether checks should fail doctor's exit code: any
// ordinary check (Severity "") that isn't ok fails it; a "warning" one only
// with strict; an "info" one never does.
func checksFail(checks []doctorCheck, strict bool) bool {
	for _, c := range checks {
		if c.OK {
			continue
		}
		switch c.Severity {
		case "info":
		case "warning":
			if strict {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// printDoctorChecks prints one line per failing check (its problem and fix),
// or a single "ok" line when none failed.
func printDoctorChecks(w io.Writer, checks []doctorCheck) {
	failed := false
	for _, c := range checks {
		if c.OK {
			continue
		}
		failed = true
		line := c.ID + ": "
		if c.Severity != "" {
			line = c.Severity + " " + line
		}
		line += c.Problem
		if c.Fix != "" {
			line += " (" + c.Fix + ")"
		}
		fmt.Fprintln(w, line)
	}
	if !failed {
		fmt.Fprintln(w, "fugaro doctor: everything checked is ok")
	}
}

// printDoctorSecrets prints the repository's secrets by name, never a
// value: nothing when doctor never attempted to list them (secrets is nil),
// "none stored" for an empty but reachable list.
func printDoctorSecrets(w io.Writer, secrets []secretEntry) {
	if secrets == nil {
		return
	}
	if len(secrets) == 0 {
		fmt.Fprintln(w, "secrets: none stored")
		return
	}
	names := make([]string, len(secrets))
	for i, s := range secrets {
		names[i] = s.Name
	}
	fmt.Fprintln(w, "secrets: "+strings.Join(names, ", "))
}

func emitDoctor(cmd *cobra.Command, o doctorOutput, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(o); err != nil {
			return err
		}
	} else {
		out := cmd.OutOrStdout()
		if l := releaseLine(); l != "" {
			fmt.Fprintln(out, l)
		}
		printDoctorChecks(out, o.Checks)
		if p := o.Project; p != nil {
			fmt.Fprintf(out, "project: %s (GCP %s, %s)\n", p.Name, p.GCPProject, p.Region)
		}
		printDoctorSecrets(out, o.Secrets)
	}
	if !o.OK {
		return &ExitError{Code: ExitUserError, Err: fmt.Errorf("fugaro doctor found a problem; see above")}
	}
	return nil
}

// sharedConfigChecks are doctor's information lines about the shared config
// published to the runs bucket: that the project's config is that file (no
// local one), or that a local config won over a published one that differs
// from it. Reading the published file is best effort: any failure says
// nothing and never fails doctor.
func sharedConfigChecks(ctx context.Context, sel localcfg.Selection, lc *localcfg.Config, now time.Time) []doctorCheck {
	if sel.From == "shared config" {
		problem := "the project's config is the shared file published to the runs bucket"
		if e, ok := localcfg.LoadSharedCache(os.Getenv, sel.Name); ok {
			problem += fmt.Sprintf(" (generation %d, checked %s ago)", e.Generation, ageDays(now.Sub(e.CheckedAt)))
		}
		return []doctorCheck{{ID: "shared-config", Severity: "info", Problem: problem, Fix: "fugaro init creates your own local config"}}
	}
	if sel.Path == "" || lc == nil {
		return nil
	}
	// The file as written: --region and --gcp-project are already applied to
	// lc, and must not make the local file look different.
	if fromFile, err := localcfg.Load(sel.Path); err == nil {
		lc = fromFile
	}
	// The convention-named bucket could be a stranger's for an installation
	// with a custom runs bucket: nothing is fetched from it.
	if lc.GCPProject == "" || lc.RunsBucketName() != "fugaro-runs-"+lc.GCPProject {
		return nil
	}
	published, _, err := sharedFetch(ctx, os.Getenv, now, lc.Name, lc.GCPProject)
	if err != nil || published == nil {
		return nil
	}
	diff := localcfg.SharedDiff(published, lc)
	if len(diff) == 0 {
		return nil
	}
	return []doctorCheck{{ID: "shared-config-differs", Severity: "info",
		Problem: "the shared config published to the runs bucket differs from your local config on " + strings.Join(diff, ", ") + "; yours is used",
		Fix:     "fugaro init publishes your local config's installation-wide fields"}}
}
