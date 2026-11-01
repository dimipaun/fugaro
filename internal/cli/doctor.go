package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
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
	"github.com/dimipaun/fugaro/internal/basepin"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/updatecheck"
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
	// Class is which view prints the check: "actionable" (something is wrong,
	// one fix), "update" (`fugaro update` puts it right), "decision" (the
	// owner's choice, with the command for each), "measure" (what doctor
	// measured, with nothing to do about it: printed by --all, counted by no
	// view) or "audit" (a finding the default view counts in its pointer line
	// rather than printing: the IAM and security sweep's own, and anything
	// doctor has not classified). It is attached by classifyDoctorChecks,
	// never by the check itself, so the human view and --json cannot disagree
	// about what the default view hides. A check that carries none is a
	// passing check nothing prints.
	Class string `json:"class,omitempty"`
	// unread marks a check whose own line says a fact could not be read, or is
	// unknown: doctorClassOf reads it and never files such a check as a
	// measurement, because an unknown filed as a measurement prints as part of
	// "everything checked is ok" beside a fact nobody read — the silent pass
	// the classes exist to prevent. Unexported and absent from --json on
	// purpose: it is not a second class (the class stays decided in one place,
	// and every ID keeps its meaning), only the one question a check answers
	// about itself that the ID alone cannot.
	unread bool
	// ownFix marks a check in an update class whose own state `fugaro update`
	// does NOT put right, so the default view prints the check's own fix (its
	// first sentence, worded as the command that does the work) instead of the
	// class's one command. The class is per ID and policy-rules-skew is one ID
	// with ten states, most of them the update's own work, so the ID alone
	// cannot answer this either — and a reader sent to `fugaro update` for a
	// state it does not repair comes back to the warning still standing.
	// Unexported and absent from --json for the reason unread is: it is not a
	// second class, only the one question a check answers about itself.
	ownFix bool
}

func fromPreflight(c preflight.Check) doctorCheck {
	return doctorCheck{ID: c.ID, OK: c.OK, Problem: c.Problem, Fix: c.Fix}
}

// pluginSettingsChecks are what else the settings file carries, one line per
// notice, then the caveat: informational only, whatever --strict says, because
// it is the repository's business to review and a CI that failed on every hook
// would be turned off. A notice is a counted audit finding — it printed in the
// default view before doctor had classes — while the caveat is the standing
// sentence about the file itself and is a measurement.
func pluginSettingsChecks(settings string) []doctorCheck {
	var out []doctorCheck
	for i, n := range pluginwire.NoticesFor(settings) {
		out = append(out, doctorCheck{ID: fmt.Sprintf("plugin-settings-%d", i+1), Severity: "info", Problem: n,
			Fix: "review " + pluginwire.Printable(settings) + " (git blame it): wiring the plugin blesses the whole file"})
	}
	return append(out, doctorCheck{ID: "plugin-settings-caveat", Severity: "info", Problem: pluginwire.NoticesCaveat,
		Fix: "read " + pluginwire.Printable(settings) + " and the files beside it (.claude/skills, .claude/commands, .mcp.json) yourself"})
}

// unreadRouting marks the routing checks of a switch doctor could not read —
// the database unreachable or another project's, the policy unreadable, a node
// no run reads. Their line says the switch is unknown rather than naming one,
// and an unknown is a counted finding, never a measurement (doctorClassOf).
func unreadRouting(cs []doctorCheck) []doctorCheck {
	for i := range cs {
		cs[i].unread = true
	}
	return cs
}

// doctorOpenProvidersCheck is doctor's "provider-open-to-every-repository"
// finding: every provider the local config leaves open to every repository
// (an absent allow_data_to, or the explicit ["*"] marker -- design §3a),
// named, with what that means. Informational, never red and never a
// failure under --strict: the owner ruled the state valid, and this only
// makes it visible, because before this design an absent list meant nobody
// and a config carried over from then now mounts its key into every
// repository's job. It reads the local config alone: no cloud call, no
// value.
func doctorOpenProvidersCheck(lc *localcfg.Config) *doctorCheck {
	names := config.OpenProviders(lc.Providers)
	if len(names) == 0 {
		return nil
	}
	lines := make([]string, len(names))
	for i, name := range names {
		lines[i] = "provider " + pluginwire.Printable(name) + " " + lc.Providers[name].OpenToEveryRepoReason()
	}
	return &doctorCheck{ID: "provider-open-to-every-repository", Severity: "info",
		Problem: strings.Join(lines, "; "),
		Fix:     "to narrow it, list the repositories in that provider's allow_data_to and run " + selfCommand() + " update --repo in each one (a deployed job keeps the key's mount and the routes it was applied with until then)"}
}

// doctorProviderSecretNamesCheck is doctor's "provider-secret-name" finding:
// every provider of the local config whose key is not stored under the name
// the convention gives it (<provider>-api-key, providerSecretConvention in
// internal/config). Informational, like doctorOpenProvidersCheck and for the
// same reason: the state is the owner's and it is valid — the key works, and
// nothing is renamed or migrated (the owner's ruling of 2026-10-18) — so this
// only makes the difference visible, and it never fails doctor, --strict
// included. It reads the local config alone: no cloud call, no value.
func doctorProviderSecretNamesCheck(lc *localcfg.Config) *doctorCheck {
	var lines []string
	for _, name := range slices.Sorted(maps.Keys(lc.Providers)) {
		if msg := config.SecretNameOffConvention(name, lc.Providers[name]); msg != "" {
			lines = append(lines, "provider "+pluginwire.Printable(name)+": "+msg)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return &doctorCheck{ID: "provider-secret-name", Severity: "info",
		Problem: strings.Join(lines, "; "),
		Fix:     "rename nothing: a name the platform already stores keeps working. Name a *new* provider's key <provider>-api-key, and note the conventional name is reserved for that provider, so no workflow secret can be stored under it"}
}

// cliVersionCheck is doctor's "cli-version" check: whether this binary is
// the latest stable fugaro release. Unlike the background nag
// (warnUpdateAvailable), it always attempts the (cached, bounded) lookup
// and always reports it: the update-check disable switches
// (FUGARO_NO_UPDATE_CHECK, update_check: false) silence only the nag, not
// doctor's own explicit report. It never fails doctor on its own: a dev or
// unparseable version has nothing to compare (ok, silent), a lookup that
// can't produce an answer (no cache, fetch failed) is an informational
// line, never red, and only an actually older binary is a (non-red)
// warning, with the brew command as its fix.
func cliVersionCheck(ctx context.Context, version string, now time.Time) doctorCheck {
	if !updatecheck.Valid(version) {
		return doctorCheck{ID: "cli-version", OK: true}
	}
	updatecheck.Refresh(ctx, os.Getenv, updateCheckHTTPClient, updateCheckEndpoint, now)
	e := updatecheck.Load(os.Getenv)
	switch {
	case e.Latest == "":
		return doctorCheck{ID: "cli-version", Severity: "info",
			Problem: "could not check whether a newer fugaro release is available",
			Fix:     "check https://github.com/" + updatecheck.Repo + "/releases, or rerun doctor once the network is reachable"}
	case !updatecheck.Newer(e.Latest, version):
		return doctorCheck{ID: "cli-version", OK: true}
	default:
		return doctorCheck{ID: "cli-version", Severity: "warning",
			Problem: fmt.Sprintf("fugaro %s is available; this is %s", e.Latest, version),
			Fix:     updatecheck.BrewCommand}
	}
}

// doctorProject is the installation doctor names, read from the local
// config only (never a cloud call by itself). BaseImages are what the local
// config records (fugaro update --base-image, or a future mirror stage): doctor
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
		cloud               cloudOptions
		dir                 string
		plugin              bool
		checkApp            bool
		checkBitbucketToken bool
		strict              bool
		asJSON              bool
		showAll             bool
		security            bool
	)
	cmd := &cobra.Command{
		Use:   "doctor [--json] [--all] [--security] [--plugin] [--strict]",
		Short: "Check that Fugaro is set up and ready, without changing anything",
		Long: `doctor only ever reads: the environment, Terraform, the local project config
and its installation's billing and IAM policy (with a budget backend, who can
sign sign-in tokens for it: warnings, never changes), the Fugaro plugin's wiring in
.claude/settings.json, a checkout's fugaro.yaml and its stored secrets by
name. With --check-github-app, in your own terminal, it also asks GitHub whether
each GitHub App the config records is installed on its repository with the
permissions Fugaro's runs ask for: that is the one thing that reads a secret's
value (the App's private key, with your own credentials, held in memory only to
sign, never printed), and it is refused in a coding agent's session. With
--check-bitbucket-token, in your own terminal, it also reads the credentials
service's bundle to see that it holds a Bitbucket token and asks Bitbucket
whether it still accepts it, one GET /repositories/<repo> per proxied
repository: that too reads a secret's value (with your own credentials, in
memory only, never printed) and is refused in a coding agent's session.
Otherwise it never prints a secret's value and never creates, enables or
changes anything.

Inside a checkout whose project publishes a project layer (fugaro config
publish), doctor also names which layer applies, flags a run that launched
without it or on an older generation, a stale per-repository copy (the one
Cloud Build and the daily image check read), and a workflow whose image was
last built from settings it no longer resolves to: every one of these
degrades with a note instead of failing when the bucket is unreachable.

--plugin checks only the plugin's wiring: offline, no credentials, safe for
CI. --strict makes a stale, unpinned, foreign or unwired plugin fail the
command; the informational states ("not installed", "cannot compare") never
do, with or without --strict. A repository's CI runs
fugaro doctor --plugin --strict.

With no local Fugaro installation configured, doctor says so and names
fugaro setup as the fix, rather than guessing at a project.

What doctor prints is a verdict and then only what to do: one sentence and one
fix per item, each item fugaro update puts right ending in that command.
--all prints every check, the informational lines included, and --security the
IAM and security sweep (who can read a stored credential, who can sign a
budget token, what a launcher may start). The default view never hides a
security finding that is a warning, and it says how many findings it left out
when it leaves any out. Every check, with its full text, is in --json whatever
the flags.

Exit 1 when a check fails, with the one-line fix to run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, cloud, dir, plugin, strict, asJSON, checkApp, checkBitbucketToken, doctorView{all: showAll, security: security})
		},
	}
	addCloudFlags(cmd, &cloud)
	cmd.Flags().StringVar(&dir, "dir", ".", "a directory in the checkout, for the plugin wiring")
	cmd.Flags().BoolVar(&plugin, "plugin", false, "check only the plugin's wiring (offline, no credentials)")
	cmd.Flags().BoolVar(&checkApp, "check-github-app", false, "also check each recorded GitHub App is installed with the permissions runs ask for: reads the App's private key from Secret Manager with your own credentials, in memory only, for one signed token (refused in a coding agent's session; GODEBUG=http2debug would print the token, so unset it)")
	cmd.Flags().BoolVar(&checkBitbucketToken, "check-bitbucket-token", false, "also check the credentials service's bundle holds a Bitbucket token and that Bitbucket still accepts it: reads the bundle with your own credentials, in memory only, for one GET /repositories/<repo> per proxied repository (refused in a coding agent's session; GODEBUG=http2debug would print the token, so unset it)")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail on a stale, unpinned, foreign or unwired plugin (CI mode)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().BoolVar(&showAll, "all", false, "print every check, the informational and audit lines included")
	cmd.Flags().BoolVar(&security, "security", false, "also print the IAM and security sweep (who may read a credential, who may sign a budget token, what a launcher may start)")
	return cmd
}

func runDoctor(cmd *cobra.Command, cloudOpts cloudOptions, dir string, pluginOnly, strict, asJSON, checkApp, checkBitbucketToken bool, view doctorView) error {
	ctx := cmd.Context()
	// Before anything is read: neither the App's key nor the bundle's Bitbucket
	// token is read in a coding agent's session.
	if checkApp {
		if err := refuseAppKeyReadInAgent(os.Getenv); err != nil {
			return err
		}
	}
	if checkBitbucketToken {
		if err := refuseBitbucketTokenReadInAgent(os.Getenv); err != nil {
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
		pluginChecks = append(pluginDoctorChecks(pluginReport), pluginSettingsChecks(loc.Settings)...)
	} else {
		// Not inside a checkout there is no wiring to find: a warning (so
		// --strict fails, as update-skills --check does), never silence.
		pluginChecks = []doctorCheck{{ID: "plugin-pin", Severity: "warning",
			Problem: pluginwire.Printable(dir) + " is not in a checkout (no .git above it), so the Fugaro plugin wiring cannot be checked",
			Fix:     "run doctor inside the repository, or pass --dir"}}
	}
	o := doctorOutput{Plugin: pluginReport, Checks: pluginChecks, Release: release()}

	if pluginOnly {
		// --plugin is documented offline and safe for CI: no credentials, no
		// network call of any kind. cli-version would need one, so it is
		// skipped here; the full run below always includes it.
		o.OK = !checksFail(o.Checks, strict)
		return emitDoctor(cmd, o, asJSON, view)
	}
	o.Checks = append(o.Checks, cliVersionCheck(ctx, Version, time.Now()))

	// Distinguishing "no installation at all" from every other selection
	// refusal (ambiguous projects, a wrong --project) needs the raw list,
	// not localcfg.Select's wrapped error (design: "no installation means
	// run fugaro setup first").
	// A checkout that names its gcp_project:, or --gcp-project, reaches
	// selection on a fresh machine: the shared config may stand in.
	namesGCP := cloudOpts.gcpProject != ""
	if co, err := checkoutProject(ctx, ""); err == nil && co != nil && co.GCPProject != "" {
		namesGCP = true
	}
	if names, err := localcfg.Projects(os.Getenv); err == nil && len(names) == 0 && !namesGCP {
		o.Checks = append(o.Checks, doctorCheck{ID: "installation", OK: false,
			Problem: "no Fugaro installation is configured",
			Fix:     "run fugaro setup"})
		o.OK = !checksFail(o.Checks, strict)
		return emitDoctor(cmd, o, asJSON, view)
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
	// Both sides' checks, in the order main has them: the catalog's age (P7),
	// the ZDR note (P9), and the project-wide provider secrets' own checks.
	o.Checks = append(o.Checks, doctorPriceCatalog(budgetNow()))
	if c := doctorProviderZDR(lc); c != nil {
		o.Checks = append(o.Checks, *c)
	}
	if c := doctorOpenProvidersCheck(lc); c != nil {
		o.Checks = append(o.Checks, *c)
	}
	if c := doctorProviderSecretNamesCheck(lc); c != nil {
		o.Checks = append(o.Checks, *c)
	}
	// The checkout and its fugaro.yaml are resolved here, before the policy
	// checks, and reused below. doctorRoutingChecks (inside them) resolves the
	// published switch over the routes a run of a repository has, and a run's
	// routes are its own providers with the project layer's claude_models
	// merged in (runner.resolveConfig) — claims that are read from this
	// checkout over the layer it resolves, so the resolution has to be here
	// rather than in the block below. Resolved once: fugaroYAMLCheck reads the
	// layer bucket, and doctor is a command an operator runs on a bad day.
	co, _ := checkoutProject(ctx, "")
	var fyCheck *doctorCheck
	var fy *doctorFugaroYAML
	var rf *resolvedFile
	if co != nil {
		if needsAnchorHint(ctx, co, lc) {
			o.Checks = append(o.Checks, doctorCheck{ID: "gcp-project-line", Severity: "info", Problem: anchorHintText,
				Fix: "fugaro update --anchor writes the gcp_project line into fugaro.yaml after checking the job images"})
		}
		fyCheck, fy, rf = fugaroYAMLCheck(ctx, co.Root, lc)
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
	for _, c := range preflight.Billing(ctx, billingSvc, crmSvc, lc.GCPProject) {
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
	o.Checks = append(o.Checks, doctorRunsBucketIAM(ctx, lc, crmSvc)...)
	o.Checks = append(o.Checks, doctorLauncherReach(ctx, lc)...)
	// The secrets-brokering checks that read the installation rather than this
	// checkout (plan B1.9): the credentials service and its callers' invoker
	// grants, the age of the stored credentials, and — behind
	// --check-bitbucket-token, already refused above — what the bundle holds.
	o.Checks = append(o.Checks, doctorCredentialsServiceDeployed(ctx, lc)...)
	// The Batch backend's live checks (plan 2026-10-19-compute-backend.md Task
	// 8 step 5, design §4.4). They return nothing for an installation with no
	// batch block: the backend's own name is refused by internal/localcfg until
	// the enable task, so the block is the one signal that an operator intends
	// this backend, and Task 10 is what turns these into the enable's gate.
	o.Checks = append(o.Checks, doctorBatchChecks(ctx, lc, crmSvc)...)
	o.Checks = append(o.Checks, doctorPolicyChecks(ctx, lc, doctorRoutingLayerOf(ctx, rf))...)

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

	o.Checks = append(o.Checks, doctorBaseImage(lc), doctorCosign(lc))

	// The installation's project-wide secret containers, listed once for
	// doctor's three project-secret checks: the two per-repository ones
	// below (inside the checkout) and the project-wide accessor one here.
	projSM, projSecrets, projOK := doctorProjectSecrets(ctx, lc)
	o.Checks = append(o.Checks, doctorProjectSecretAccessorChecks(ctx, lc, projSM, projSecrets)...)
	o.Checks = append(o.Checks, doctorSecretManagerChecks(ctx, lc, projSM, crmSvc)...)
	// This checkout's own secret containers, listed here rather than in the
	// checkout branch below because secret-rotation-age measures both scopes:
	// the operator's secret_expires is keyed by a secret's logical name, and
	// bitbucket-token is stored in the repository's own container by default.
	// nil outside a checkout, which is what doctorSecrets answers for one.
	repoSecrets := doctorSecrets(cmd, lc)
	o.Checks = append(o.Checks, doctorSecretRotationAge(ctx, lc, projSecrets, projOK, repoSecrets, doctorManagedSecrets(ctx, lc))...)
	if checkBitbucketToken {
		o.Checks = append(o.Checks, doctorBundleChecks(ctx, lc)...)
	}
	if c := projectAccountStoredCheck(lc, projSecrets, projOK); c != nil {
		// Outside the checkout branch on purpose: an account job is the
		// project's, not this repository's (the profile and the jobs it is
		// deployed as are the local config's), so the check is the same
		// wherever doctor is run from.
		o.Checks = append(o.Checks, *c)
	}

	o.Project = &doctorProject{Name: lc.Name, GCPProject: lc.GCPProject, Region: lc.Region, RegistryHost: lc.RegistryHost, BaseImages: lc.BaseImages}

	if co != nil {
		if fyCheck != nil {
			o.Checks = append(o.Checks, *fyCheck)
			o.FugaroYAML = fy
			if fy.Valid && rf != nil {
				o.Checks = append(o.Checks, doctorLayerChecks(ctx, lc, co.Root, *rf)...)
			}
		}
		o.Secrets = repoSecrets
		token := oauthTokenOf(rf)
		if repo, err := originRepo(ctx); err == nil {
			if c := claudeAccountsStoredCheck(lc, repo, o.Secrets, projSecrets, projOK); c != nil {
				o.Checks = append(o.Checks, *c)
			}
			// The two per-repository project-secret checks need the
			// repository's own resolved fugaro.yaml (which logical names its
			// jobs mount): an invalid one already failed its own check, so
			// there is nothing to compute from.
			if rf != nil && rf.Cfg != nil {
				if facts, ok := doctorRepoSecretsOf(lc, rf, repo); ok {
					o.Checks = append(o.Checks, doctorProjectSecretStoredChecks(facts, o.Secrets, projSecrets, projOK)...)
					if c := doctorProjectSecretScopeCheck(ctx, lc, facts, o.Secrets, projSecrets); c != nil {
						o.Checks = append(o.Checks, *c)
					}
					o.Checks = append(o.Checks, doctorSecretScopeChecks(facts, o.Secrets, projSecrets, projOK)...)
					o.Checks = append(o.Checks, doctorBitbucketDirect(ctx, lc, repo, rf.Cfg.Git.Provider, facts)...)
				}
			}
			if c := oauthTokenAllowedCheck(lc, repo, token, authOf(rf)); c != nil {
				o.Checks = append(o.Checks, *c)
			}
			if c := doctorModelKeyInAgent(lc, rf); c != nil {
				o.Checks = append(o.Checks, *c)
			}
		} else {
			if len(lc.ClaudeAccounts) > 0 {
				// The repository itself could not be determined (no origin
				// remote), so it is impossible to tell which named accounts, if
				// any, apply to it. claude_accounts is configured for at least
				// one repository somewhere, so staying silent here would fail
				// open: an owner could not tell this checkout was never actually
				// checked. Warn instead (fail closed).
				o.Checks = append(o.Checks, doctorCheck{ID: "claude-accounts-stored", Severity: "warning",
					Problem: "could not determine this checkout's repository (no origin remote), so whether its named Claude account(s) have a stored token could not be checked",
					Fix:     "run doctor from a checkout with a parseable owner/name origin remote, or check by hand: " + selfCommand() + " secrets ls --repo OWNER/NAME"})
			}
			if token != "" {
				// Same fail-closed reasoning as claude-accounts-stored just
				// above: agent.oauth_token is set, but with no resolvable
				// repository there is nothing to check it against. Warn
				// rather than stay silent.
				o.Checks = append(o.Checks, doctorCheck{ID: "oauth-token-allowed", Severity: "warning",
					Problem: "could not determine this checkout's repository (no origin remote), so whether its agent.oauth_token names an allowed account could not be checked",
					Fix:     "run doctor from a checkout with a parseable owner/name origin remote, or check fugaro.yaml and the local config's claude_accounts by hand"})
			}
		}
	}

	o.OK = !checksFail(o.Checks, strict)
	return emitDoctor(cmd, o, asJSON, view)
}

// doctorBaseImage warns when the installation has no Fugaro base recorded
// (design base-image.md section 10): every image build starts from it.
func doctorBaseImage(lc *localcfg.Config) doctorCheck {
	if lc.BaseImage(config.BaseKind) != "" {
		return doctorCheck{ID: "base-image", OK: true}
	}
	return doctorCheck{ID: "base-image", OK: false, Severity: "warning",
		Problem: "the local config has no base_images.base (" + config.MigrationDoc + ")",
		Fix:     "fugaro image refresh   # in the repository's checkout"}
}

// doctorPriceCatalog is the built-in provider price catalog's age (design
// provider-price-catalog.md §7, plan Task 5 step 3): the day it was generated
// and what it was generated from, and a warning once it is past the catalog's
// own 30 days (catalog.StaleAfter, not the embedded Claude table's 90), the
// same threshold the run measures its bound by. It is informational while
// fresh — an owner reads the age here — and a warning past the threshold,
// which fails doctor only under --strict.
func doctorPriceCatalog(now time.Time) doctorCheck {
	f, err := catalog.Load()
	if err != nil {
		return doctorCheck{ID: "provider-price-catalog",
			Problem: err.Error(),
			Fix:     "regenerate the file with scripts/gen-price-catalog.sh and rebuild fugaro"}
	}
	checked, err := time.Parse("2006-01-02", f.CheckedAt)
	if err != nil {
		return doctorCheck{ID: "provider-price-catalog",
			Problem: "the built-in provider price catalog has no readable checked_at: " + f.CheckedAt,
			Fix:     "regenerate the file with scripts/gen-price-catalog.sh and rebuild fugaro"}
	}
	source := ""
	if len(f.Sources) > 0 {
		source = f.Sources[0]
	}
	c := doctorCheck{ID: "provider-price-catalog", Severity: "info",
		Problem: fmt.Sprintf("the built-in provider price catalog was generated from %s, checked %s (%s ago)", source, f.CheckedAt, ageDays(now.Sub(checked))),
		Fix:     "regenerate it with scripts/gen-price-catalog.sh before it is 30 days old"}
	if now.Sub(checked) > catalog.StaleAfter {
		c.Severity = "warning"
		c.Problem = fmt.Sprintf("the built-in provider price catalog was generated from %s, checked %s (%s ago): it is stale, so every model it prices is charged the dearest end of its tier's range, not the end price_policy asks for",
			source, f.CheckedAt, ageDays(now.Sub(checked)))
		c.Fix = "scripts/gen-price-catalog.sh   # regenerates internal/pricing/catalog/catalog.json; commit the result"
	}
	return c
}

// doctorProviderZDR is the honesty check design §8 calls for (P9): a route's
// require_zdr (true unless it is set false) prices its models at the
// ZDR-eligible endpoints of the built-in price catalog. It is a price, not
// enforcement: OpenRouter's zero-data-retention policy is the account's own
// privacy setting, which Fugaro can neither set (the gateway never rewrites a
// request body and refuses the `provider` key) nor read (the key endpoint has
// no data-policy field). Informational, so it never fails doctor, not even
// under --strict: it is a warning to check the account, not a fault here.
//
// Only the routes the built-in catalog actually prices are named
// (config.CatalogEligible): require_zdr on a vendor's own endpoint selects no
// price — such a model is priced under model_prices — so the note would be
// false about it, and it says nothing about an account setting there either.
func doctorProviderZDR(lc *localcfg.Config) *doctorCheck {
	if lc == nil {
		return nil
	}
	var names []string
	for name, p := range lc.Providers {
		if p.RequireZDROn() && config.CatalogEligible(p.BaseURL) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	// No unread marker, and that is the point of the note: what doctor cannot
	// read is the provider account's privacy setting, a fact outside this
	// installation, not a read of the installation that did not answer. The
	// marker is for the second kind, and with it this line is a counted finding
	// on every installation whose routes the catalog prices — a pointer over a
	// screen that has nothing wrong in it, which is what the owner's rule
	// against a pointer on a clean run forbids. It is a measurement
	// (doctorMeasureIDs): --all, --security and --json carry it.
	return &doctorCheck{ID: "provider-zdr-unverified", Severity: "info",
		Problem: "require_zdr is on for the provider route(s) " + strings.Join(names, ", ") + " (the default), so their models are priced at the ZDR-eligible endpoints: " +
			"that is the conservative price, not enforcement. Whether calls are actually zero-data-retention is your OpenRouter account's privacy setting, and Fugaro cannot read it",
		Fix: "confirm the account's data policy in the OpenRouter settings; require_zdr: false prices every endpoint the model is served by instead"}
}

// doctorCosign reports the verifier a base pin needs: a release base image
// is pinned at the digest its OCI-attached cosign signature was verified
// for, and internal/basepin shells out to the `cosign` binary to verify it
// (design registry-remote-repository.md §9 Decision 1, option (ii)) —
// so every pin that already exists was verified when it was written, and
// only the next one needs the binary.
//
// The credentials service's image is resolved the same way and through the
// same binary, and since 0.15.0 an ordinary `fugaro update` re-pins it
// (init_stages.go's pinCredentialsImage): an installation that opted in needs
// cosign on PATH for an upgrade that names no flag, which is why the message
// names that path as well.
//
// Informational, like doctorOpenProvidersCheck and the tool-availability
// branches of doctorCLIVersion: an absent binary is not a wrong state and
// not this repository's business to fail CI over (--strict is the GitHub
// App's CI mode), and a repository whose base image is its own never calls
// the verifier at all. What it must not do is stay silent — the pin
// refuses without the binary, and this is where that is learned before
// the project's name is typed.
func doctorCosign(lc *localcfg.Config) doctorCheck {
	const id = "cosign"
	if basepin.CosignPath() != "" {
		return doctorCheck{ID: id, OK: true}
	}
	problem := "cosign is not on PATH (looked for " + basepin.CosignBin + "): a base image and the credentials service's image are pinned at the digest their cosign signature was verified for, so fugaro update --base, fugaro image refresh, and an ordinary fugaro update of an installation that opted in with --provision-credentials all refuse without it"
	fix := "install cosign (https://docs.sigstore.dev/cosign/installation/), or name a base image explicitly with --base-image KIND=IMAGE"
	if lc.BaseImage(config.BaseKind) == "" {
		problem = "no Fugaro base is recorded, and cosign is not on PATH (looked for " + basepin.CosignBin + "): recording one with fugaro image refresh needs it, and so does an ordinary fugaro update of an installation that opted in with --provision-credentials"
	}
	return doctorCheck{ID: id, OK: false, Severity: "info", Problem: problem, Fix: fix}
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
		id := "github-app:" + repo
		// The secret the check signs with is the one live state resolves for
		// github-app-key, not the repository's own ID unconditionally: once
		// that name moves to project scope the repository's own copy has no
		// version at all, and the check would report "its private key is not
		// stored yet" for a key that is stored, just project-wide. The
		// resolution is compared with the build account's own live grant
		// first (B1), the identical comparison fugaro image build makes: a
		// grant naming a different ID means this check would read a key the
		// build's clone would not, which makes the diagnostic misleading
		// rather than wrong, so it is a warning with the apply that fixes it.
		logical := config.GitHubAppKeySecret
		projectID, _ := infra.ProjectSecretCandidate(lc, logical, repo)
		buildSA := buildAccountMember(gcp.BuildServiceAccountID(slug) + "@" + lc.GCPProject + ".iam.gserviceaccount.com")
		keyID, err := resolveGitSecretID(ctx, lc, lc.GCPProject, repo, logical, gcp.SecretID(slug, logical), projectID, buildSA, "the GitHub App check of "+repo)
		if err != nil {
			// A drift's own message does not fit this field: Printable cuts
			// it, and the two IDs are what a reader needs from it (driftError.short).
			problem := oneLineCLI(err.Error())
			var drift *driftError
			if errors.As(err, &drift) {
				problem = drift.short()
			}
			out = append(out, doctorCheck{ID: id, OK: false, Severity: "warning", Problem: problem,
				Fix: "run " + selfCommand() + " update --repo " + quoteWord(repo) + " in its checkout to reapply it, so the App's key this check reads and the one its builds read are the same secret"})
			continue
		}
		c := checkGitHubApp(ctx, lc, repo, lc.Repos[repo].GitHubAppID, appKeySource{Read: true, SecretID: keyID, Notice: notice})
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

// fugaroYAMLCheck reads root/fugaro.yaml and checks it (resolveFugaroYAML and
// config.Check, as fugaro validate does: the same resolution
// parseCheckoutFugaroYAML would do, but keeping the resolvedFile so
// doctorLayerChecks can reuse it instead of resolving the project layer a
// second time), nil when there is no such file. The third return is non-nil
// only when the file is valid (len(problems) == 0).
func fugaroYAMLCheck(ctx context.Context, root string, lc *localcfg.Config) (*doctorCheck, *doctorFugaroYAML, *resolvedFile) {
	path := filepath.Join(root, "fugaro.yaml")
	data, err := readFugaroYAML(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		// Present and not a file to read (a link, a FIFO, too large): reported,
		// never followed.
		return &doctorCheck{ID: "fugaro-yaml", OK: false, Problem: oneLineCLI(err.Error()),
			Fix: "replace it with a regular fugaro.yaml of the repository's own"}, &doctorFugaroYAML{Path: path, Problems: []config.Problem{{Message: "not read"}}}, nil
	}
	rf, rerr := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
	var problems []config.Problem
	if rerr != nil {
		problems = []config.Problem{{Path: "project layer", Message: oneLineCLI(rerr.Error())}}
	} else {
		problems = rf.Problems
		if rf.Cfg != nil {
			problems = append(problems, config.Check(rf.Cfg, root)...)
		}
	}
	fy := &doctorFugaroYAML{Path: path, Valid: len(problems) == 0, Problems: problems}
	if len(problems) == 0 {
		return &doctorCheck{ID: "fugaro-yaml", OK: true}, fy, &rf
	}
	return &doctorCheck{ID: "fugaro-yaml", OK: false,
		Problem: fmt.Sprintf("%s has %d problem(s)", path, len(problems)),
		Fix:     "run fugaro validate to see them"}, fy, nil
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

// doctorProjectSecrets lists the installation's project-wide secret
// containers (the labels the project-wide stage writes: managed + project,
// never a repository's), by ID and version state, never a value. ok is
// false when the listing could not be made at all (no credentials, Secret
// Manager unreachable, no secret_manager endpoint under no_auth), which its
// callers tell apart from a real empty answer: an unreadable installation
// must not be reported as one that stores nothing.
func doctorProjectSecrets(ctx context.Context, lc *localcfg.Config) (sm *gcp.Secrets, all []gcp.SecretInfo, ok bool) {
	if lc.Endpoints.NoAuth && lc.Endpoints.SecretManager == "" {
		// Fakes only, and no fake for Secret Manager: an empty endpoint
		// would send the call to Google. The same guard the other cloud
		// reads behind doctor use.
		return nil, nil, false
	}
	// client, not sm: sm is this function's named result, and a local of the
	// same name would shadow it (the review of PR #402).
	client, err := gcp.NewSecrets(ctx, gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region,
		Endpoints: gcp.Endpoints{SecretManager: lc.Endpoints.SecretManager, NoAuth: lc.Endpoints.NoAuth}})
	if err != nil {
		return nil, nil, false
	}
	list, err := client.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: lc.Name})
	if err != nil {
		return client, nil, false
	}
	slices.SortFunc(list, func(a, b gcp.SecretInfo) int { return strings.Compare(a.ID, b.ID) })
	return client, list, true
}

// doctorManagedSet is every container Fugaro manages in the project — the
// project-wide ones and each repository's own, whoever onboarded it — by the
// logical names they carry, and whether that listing answered.
//
// It answers the one question secret-rotation-age cannot answer from the two
// scopes it measures: whether a name the operator recorded in secret_expires is
// carried by a container of another repository's own (bitbucket-token is
// per-repository by default, so the date may have been recorded from another
// checkout). Read is false when the listing could not be made — and the check
// reads it only for a name nothing in the two scopes carries, so a false Read is
// always a failed read and never "there was nothing to look for".
type doctorManagedSet struct {
	Names map[string]bool
	Read  bool
}

// doctorManagedSecrets reads doctorManagedSet, best-effort and never fatal:
// nothing is listed at all unless the operator recorded an expiry date, since a
// name is what the listing is for and doctor's other two listings already cover
// the scopes the age check measures (which matters, since listing a container
// costs a version listing as well).
func doctorManagedSecrets(ctx context.Context, lc *localcfg.Config) doctorManagedSet {
	if len(lc.SecretExpires) == 0 {
		return doctorManagedSet{}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.SecretManager == "" {
		// Fakes only, and no fake for Secret Manager: the same guard
		// doctorProjectSecrets makes.
		return doctorManagedSet{}
	}
	sm, err := gcp.NewSecrets(ctx, gcpOptions(lc))
	if err != nil {
		return doctorManagedSet{}
	}
	list, err := sm.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	if err != nil {
		return doctorManagedSet{}
	}
	set := doctorManagedSet{Names: make(map[string]bool, len(list)), Read: true}
	for _, s := range list {
		if name := s.Labels[gcp.LabelSecret]; name != "" {
			set.Names[name] = true
		}
	}
	return set
}

// projectSecretEnabled is the logical names holding an enabled version, from
// whichever listing: a project-wide container carries its logical name in
// gcp.LabelSecret exactly as a repository's own does, and the caller has
// already narrowed the listing to one scope.
func projectSecretEnabled(list []gcp.SecretInfo) map[string]bool {
	out := map[string]bool{}
	for _, s := range list {
		if s.Latest != "" {
			out[s.Labels[gcp.LabelSecret]] = true
		}
	}
	return out
}

// doctorRepoSecrets is what doctor's two per-repository project-secret
// checks read off the checkout, computed through the same infra.Workflow the
// rest of the CLI builds a job's spec with (jobConfigCopyNote,
// internal/cli/upgrade_cloud.go): the logical names this repository's jobs
// mount, the repository secret ID each of them is stored under, the
// project-wide ID of every name the local config allow-lists it for, and
// each workflow's default-account job name.
//
// infra.Repo would answer all of this in one call too, but it also builds
// the daily image check's own spec, which refuses a local config with no
// base_images entry for the workflow's base -- a repository doctor must
// still be able to report on. A checkout: clone workflow has no derived
// image, so it has no check spec either way.
type doctorRepoSecrets struct {
	repo       string
	candidates map[string]string // logical -> the project-wide ID it resolves to
	logical    map[string]string // a secret ID either scope stores this name under -> logical name
	repoIDs    map[string]string // logical -> the repository's own secret ID
	jobs       map[string]string // workflow name -> its default-account job
	// keyNeeded is whether this checkout's config needs the optional Anthropic
	// key at all (needsAnthropicKey, the CLI's one rule for it). False is the
	// provider-only repository: every call routes to a provider, and its job
	// drops the key's mount rather than gate the deploy (Readiness), so no
	// stored value is missing.
	keyNeeded bool
}

// doctorRepoSecretsOf computes doctorRepoSecrets for repo from cfg (the
// checkout's resolved fugaro.yaml). ok is false when a workflow's own spec
// cannot be computed (a bad resources block, a secret env collision, a
// repository the local config does not record): the same edit already fails
// another check, so the two below stay silent rather than report on a
// repository whose jobs cannot be built at all.
func doctorRepoSecretsOf(lc *localcfg.Config, rf *resolvedFile, repo string) (doctorRepoSecrets, bool) {
	cfg := rf.Cfg
	facts := doctorRepoSecrets{repo: repo,
		candidates: map[string]string{}, logical: map[string]string{}, repoIDs: map[string]string{}, jobs: map[string]string{}}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		ws, err := infra.Workflow(infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, FileSetsBaseBranch: fileSetsBaseBranch(*rf)}, name)
		if err != nil {
			return doctorRepoSecrets{}, false
		}
		facts.jobs[name] = ws.Job
		for logical, id := range ws.SecretIDs {
			// ws.SecretIDs is the repository-scoped ID wherever the name is
			// stored (infra.Workflow), so the reverse lookup has to hold
			// the project-wide ID as well: a deployed job may mount either
			// scope's container, and both directions are a scope the
			// project-secret-scope-stale check exists to compare.
			facts.logical[id] = logical
			facts.repoIDs[logical] = id
			if projectID, ok := infra.ProjectSecretCandidate(lc, logical, repo); ok {
				facts.candidates[logical] = projectID
				facts.logical[projectID] = logical
			}
		}
	}
	// Whether the optional key is needed at all is the checkout's own answer,
	// not the local config's: needsAnthropicKey merges the owner's ceiling with
	// this file and reads the routes the run would take, exactly as init's
	// prompt, update --repo's missing list and validate's note do.
	facts.keyNeeded = needsAnthropicKey(lc, cfg, repo)
	return facts, true
}

// doctorProjectSecretStoredChecks are doctor's "project-secret-stored" pair:
// every logical name this repository may use project-wide (facts.candidates:
// the names its jobs mount that the owner's local config allow-lists it for,
// D2's sweep included) must hold an enabled version somewhere -- its own
// container, or the project's. Neither means no run of a workflow mounting it
// can start, which is red, since only storing a value fixes it.
//
// The optional Anthropic key is the one name that is not that, and the reason
// this returns two checks: a job can go without it (infra.OptionalSecret), so
// with no version anywhere Readiness drops its mount and deploys anyway, and
// a repository whose models all route through a provider needs no key at all.
// Requiring one there made doctor red for a repository whose runs are fine
// (code review, high), so the key is named only when the config really needs
// it (facts.keyNeeded) and then as a warning: the deploy is right, and a run
// that calls Anthropic is refused until a key is stored -- what Readiness
// says as its own note, and validate as its own.
//
// It reads metadata only (two listings doctor already has) and never a
// value, and it adds no check at all for an installation that opted no
// secret into project-wide storage: there is nothing it could say.
func doctorProjectSecretStoredChecks(facts doctorRepoSecrets, listed []secretEntry, project []gcp.SecretInfo, projectOK bool) []doctorCheck {
	var names []string
	for _, name := range slices.Sorted(maps.Keys(facts.candidates)) {
		if infra.OptionalSecret(name) && !facts.keyNeeded {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	printableRepo := pluginwire.Printable(facts.repo)
	if listed == nil || !projectOK {
		// Fail closed: an owner could not otherwise tell an installation
		// that stores nothing from one whose secrets could not be read.
		// A warning, since nothing here proves a value is missing.
		return []doctorCheck{{ID: "project-secret-stored", Severity: "warning",
			Problem: fmt.Sprintf("could not list the secrets %s may use project-wide (%s), so whether they have a stored value could not be checked",
				printableRepo, strings.Join(printableNames(names), ", ")),
			Fix: "rerun doctor with Secret Manager reachable and your credentials available, or check by hand: " + selfCommand() + " secrets ls --repo " + quoteWord(facts.repo) + ", and " + selfCommand() + " secrets ls --project-wide"}}
	}
	have := projectSecretEnabledFromEntries(listed)
	for name, enabled := range projectSecretEnabled(project) {
		if enabled {
			have[name] = true
		}
	}
	var missing, cmds, optionalMissing, optionalCmds []string
	for _, name := range names {
		if have[name] {
			continue
		}
		// D2 (plan Task 2.1) made the project copy the default store for
		// exactly these names, so the Fix leads with the bare command -- which
		// stores the value where the default puts it, reaching exactly the
		// repositories the owner allow-listed it for -- as the sibling
		// claude-accounts check does (code review, low). Leading with
		// --repo <repo> offered a repository copy first, and a repository copy
		// wins over the project copy forever (§5, the footgun these checks
		// exist to name), to a person whose repository may mount the shared
		// one.
		set := selfCommand() + " secrets set " + quoteWord(name)
		both := []string{set, set + " --repo " + quoteWord(facts.repo)}
		if infra.OptionalSecret(name) {
			optionalMissing = append(optionalMissing, pluginwire.Printable(name))
			optionalCmds = append(optionalCmds, both...)
			continue
		}
		missing = append(missing, pluginwire.Printable(name))
		cmds = append(cmds, both...)
	}
	var out []doctorCheck
	switch {
	case len(missing) > 0:
		// A name a run mounts whatever the config says: red.
		out = append(out, doctorCheck{ID: "project-secret-stored",
			Problem: fmt.Sprintf("%s may use %s project-wide, and neither its own container nor the project's holds an enabled version: no run of a workflow mounting one can start",
				printableRepo, strings.Join(missing, ", ")),
			Fix: strings.Join(cmds, ", or ")})
	case len(optionalMissing) == 0:
		// Nothing this repository may use project-wide is missing.
		out = append(out, doctorCheck{ID: "project-secret-stored", OK: true})
	}
	if len(optionalMissing) > 0 {
		out = append(out, doctorCheck{ID: "project-secret-optional", Severity: "warning",
			Problem: fmt.Sprintf("%s may use %s project-wide, and neither its own container nor the project's holds an enabled version: the job deploys without it, and a run that calls Anthropic is refused until one is stored",
				printableRepo, strings.Join(optionalMissing, ", ")),
			Fix: strings.Join(optionalCmds, ", or ")})
	}
	return out
}

// projectSecretEnabledFromEntries is projectSecretEnabled over doctor's own
// already-fetched repository listing (doctorSecrets).
func projectSecretEnabledFromEntries(listed []secretEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range listed {
		if e.Latest != "" {
			out[e.Name] = true
		}
	}
	return out
}

// printableNames bounds a set of logical names for a message, the way every
// other name doctor echoes is bounded.
func printableNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = pluginwire.Printable(n)
	}
	return out
}

// doctorProjectSecretScopeCheck is doctor's "project-secret-scope-stale"
// check: a deployed job of this repository still mounts a secret from one
// scope while the copy Readiness resolves for it now is the other one --
// typically the repository's own copy lost its last version after the job
// was applied, or a repository copy was stored again after a project-shared
// apply. The deployed job's own mounted references are read (metadata only),
// never the secret's value.
//
// A warning (design §7's own reasoning): the next fugaro update --repo fixes
// it, and a repository mid-move is the expected, transient case. Best
// effort: a job that could not be read, a name that resolves to nothing, or
// no Cloud Run endpoint at all says nothing.
//
// Only each workflow's default-account job is read, the same limitation
// jobConfigCopyNote documents (internal/cli/upgrade_cloud.go): the one
// fugaro update --repo that refreshes the default job refreshes every named
// account's variant of it too, so a finding here is never the only one.
func doctorProjectSecretScopeCheck(ctx context.Context, lc *localcfg.Config, facts doctorRepoSecrets, listed []secretEntry, project []gcp.SecretInfo) *doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		// Fakes only, and no fake for Cloud Run: there is nothing to read,
		// and an empty endpoint would send the call to Google.
		return nil
	}
	be, err := gcp.New(ctx, gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, LogView: lc.LogView,
		Endpoints: gcp.Endpoints{Run: lc.Endpoints.Run, NoAuth: lc.Endpoints.NoAuth}})
	if err != nil {
		return nil
	}
	repoEnabled := projectSecretEnabledFromEntries(listed)
	projEnabled := projectSecretEnabled(project)
	var stale []string
	for _, key := range slices.Sorted(maps.Keys(facts.jobs)) {
		refs, exists, err := be.JobSecretRefs(ctx, facts.jobs[key])
		if err != nil || !exists {
			continue
		}
		for _, env := range slices.Sorted(maps.Keys(refs)) {
			id := refs[env]
			name, known := facts.logical[id]
			if !known {
				continue // a name this repository does not compute (a stranger's, or a hand-written reference)
			}
			projectID, isCandidate := facts.candidates[name]
			if !isCandidate {
				continue // not a name this repository may use project-wide, so there is no other scope to move to
			}
			// What Readiness resolves for this name now, §5's precedence: a
			// repository copy with an enabled version wins, else the
			// project-wide copy. It is compared against the ID the job
			// actually mounts, never against the repository-scoped ID: the
			// warning has to fire whichever of the two scopes the job was
			// applied with, so a job still mounting the project copy after
			// this repository stored one of its own is caught too.
			want := facts.repoIDs[name]
			switch {
			case repoEnabled[name]:
				// The repository's own copy wins: want is repoIDs[name].
			case projEnabled[name]:
				want = projectID
			default:
				continue // nothing resolves to anything: project-secret-stored's own business
			}
			if want != id {
				stale = append(stale, fmt.Sprintf("workflow %s's job mounts %s from %s, but the copy Fugaro resolves for it now is %s (%s)",
					pluginwire.Printable(key), pluginwire.Printable(name), pluginwire.Printable(id), pluginwire.Printable(want), pluginwire.Printable(env)))
			}
		}
	}
	if len(stale) == 0 {
		return nil
	}
	return &doctorCheck{ID: "project-secret-scope-stale", Severity: "warning",
		Problem: strings.Join(stale, "; ") + ": the deployed job was applied before the secret's scope changed",
		Fix:     "run " + selfCommand() + " update --repo " + quoteWord(facts.repo) + " in your own terminal to reapply the repository, so the next run mounts the copy Fugaro resolves"}
}

// doctorSecretScopeChecks are doctor's "secret-scope" pair (plan Task 2.3
// step 1): what this checkout's jobs mount, and from where — the surface
// the cross-cutting list asks for next to the launcher grants, since under
// D2 the same name can resolve to two different containers and only one of
// them is the one a job reads.
//
// "secret-scope" is the info line: for every name this checkout's jobs
// mount, the copy Fugaro resolves for it (facts.logical, facts.repoIDs,
// facts.candidates, and the two listings doctor already fetched), naming
// the scope rather than either container's ID — "project scope", "repository
// copy", or "no enabled copy this repository can resolve" for a name neither
// listing holds an enabled version of that this repository may resolve
// (code review, minor: the docs say the line answers this for every name, so
// it does, rather than silently dropping the names nothing is stored for; and
// the wording says exactly that, not "no copy stored", which is false twice
// over — a container that exists holding only disabled versions, and a
// project-wide copy that is enabled but not eligible for this repository,
// both reach this branch). Informational and never failing, exactly like
// provider-open-to-every-repository: the state is the owner's, and this only
// makes it readable in one place.
//
// "secret-scope-shadowed" is the warning: a name stored both project-wide
// and in this repository's own container, where the repository copy wins
// (design project-secrets.md §5) — the footgun `fugaro secrets set` already
// warns about at store time (internal/cli/secrets.go's storeSecretValue
// notes), now visible from a checkout instead of only at the moment of
// storing. A warning, not red: the next update --repo cannot change which copy
// wins, so the fix is deliberate, and doctor without --strict still passes.
//
// It is the stored copies it compares, not a deployed job's mounts: that is
// project-secret-scope-stale's own question, and a job that is merely behind
// its repository's stored copies is already named there.
//
// Both read metadata only — names and version state — and never a value.
// The pair says nothing when the listings could not be read (listed == nil,
// or !projectOK): project-secret-stored already fails closed on exactly that
// unreadable listing, so this stays silent rather than double-report a state
// neither can vouch for.
func doctorSecretScopeChecks(facts doctorRepoSecrets, listed []secretEntry, project []gcp.SecretInfo, projectOK bool) []doctorCheck {
	if listed == nil || !projectOK {
		return nil
	}
	repoEnabled := projectSecretEnabledFromEntries(listed)
	projEnabled := projectSecretEnabled(project)
	names := map[string]bool{}
	for name := range facts.repoIDs {
		names[name] = true
	}
	for name := range facts.candidates {
		names[name] = true
	}
	var lines, shadowed []string
	for _, name := range slices.Sorted(maps.Keys(names)) {
		_, isCandidate := facts.candidates[name]
		inProject := isCandidate && projEnabled[name]
		inRepo := repoEnabled[name]
		printable := pluginwire.Printable(name)
		switch {
		case inRepo && inProject:
			lines = append(lines, printable+" — repository copy, which wins over the project's")
			shadowed = append(shadowed, name)
		case inRepo:
			lines = append(lines, printable+" — repository copy")
		case inProject:
			lines = append(lines, printable+" — project scope")
		default:
			// A name this checkout's jobs mount that resolves to no enabled
			// copy at all: neither container holds an enabled version this
			// repository may resolve — which is not the same as "nothing is
			// stored" (a container can exist holding only disabled versions,
			// or hold a project-wide copy this repository is not eligible
			// for). The listing answers the question the docs ask it ("which
			// copy does this name resolve to") for every name, not only for
			// the ones something is stored for, and "nothing yet" is the
			// answer that matters most to a reader (project-secret-stored is
			// the red check for it).
			lines = append(lines, printable+" — no enabled copy this repository can resolve")
		}
	}
	var out []doctorCheck
	if len(lines) > 0 {
		out = append(out, doctorCheck{ID: "secret-scope", Severity: "info",
			Problem: "secrets this checkout's jobs mount, by the copy Fugaro resolves for each: " + strings.Join(lines, "; "),
			Fix: "run " + selfCommand() + " secrets ls --json for the project's containers and " +
				selfCommand() + " secrets ls --repo " + quoteWord(facts.repo) + " --json for this repository's own"})
	}
	if len(shadowed) > 0 {
		printable := printableNames(shadowed)
		// Every shadowed name gets its own command (code review, minor): the
		// fix is printed to be pasted, and naming only the first left the
		// others with no command at all.
		cmds := make([]string, 0, len(shadowed))
		for _, name := range shadowed {
			cmds = append(cmds, selfCommand()+" secrets set "+quoteWord(name)+" --repo "+quoteWord(facts.repo))
		}
		out = append(out, doctorCheck{ID: "secret-scope-shadowed", Severity: "warning",
			Problem: fmt.Sprintf("%s: stored in %s's own container and project-wide at once, and a repository copy with an enabled version wins over the project copy, so the project's value never reaches this repository",
				strings.Join(printable, ", "), pluginwire.Printable(facts.repo)),
			Fix: "store the value you mean with " + strings.Join(cmds, ", or ") +
				" to keep this repository's own copy up to date, or disable the repository copy's versions (gcloud secrets versions disable) if the project's value is the one this repository should mount"})
	}
	return out
}

// doctorProjectSecretAccessorChecks is doctor's project-wide
// "project-secret-extra-accessors" check (S3): for each project-wide
// container, every live secretAccessor grant that belongs to one of the
// installation's own service accounts, whose repository the name's own
// eligibility rule no longer lists. Removing a repository from
// project_secrets, from a provider's allow_data_to or from an account's
// leaves its job's grant live until that one repository's own apply runs
// again, and no repository's own fugaro update --repo has reason to look at
// another repository's bindings -- noteExtraAccessors only ever compares one
// repository's spec against its own secrets.
//
// A warning, never red (SF9): this is revocation lag, the expected case
// between the owner's edit and the affected repository's next apply, and red
// would fail every installation mid-revocation. Its fix is spelled out, so
// the operator needs no cross-referencing.
//
// The repository a grant belongs to is computed forward, never decoded:
// gcp.ServiceAccountID hashes a repository slug, so a hash cannot be
// inverted. Every repository/workflow pair the local config records
// contributes its job account, and every (repository, workflow, allowed
// Claude account) pair that account's own variant, since a named account's
// job runs under its own, variant-keyed service account.
func doctorProjectSecretAccessorChecks(ctx context.Context, lc *localcfg.Config, sm *gcp.Secrets, project []gcp.SecretInfo) []doctorCheck {
	if sm == nil {
		return nil
	}
	owners := projectSecretAccountOwners(lc)
	var out []doctorCheck
	for _, s := range project {
		name := s.Labels[gcp.LabelSecret]
		members, err := sm.SecretAccessors(ctx, s.ID)
		if err != nil {
			continue // best effort, like every other cloud read doctor degrades on
		}
		var extra []string
		for _, m := range members {
			repo, ours := owners[m]
			if !ours {
				continue // a person, a group, or an account of something that is not ours
			}
			if _, allowed := infra.ProjectSecretCandidate(lc, name, repo); allowed {
				continue
			}
			if !slices.Contains(extra, repo) {
				extra = append(extra, repo)
			}
		}
		if len(extra) == 0 {
			continue
		}
		slices.Sort(extra)
		cmds := make([]string, len(extra))
		for i, repo := range extra {
			cmds[i] = "re-run " + selfCommand() + " update --repo " + quoteWord(repo) + " and pass --allow-delete"
		}
		out = append(out, doctorCheck{ID: "project-secret-extra-accessors", Severity: "warning",
			Problem: fmt.Sprintf("project secret %s can still be read by %s, which its allow_data_to no longer lists: the grant stays live until each of those repositories is applied again",
				pluginwire.Printable(name), strings.Join(printableNames(extra), ", ")),
			Fix: strings.Join(cmds, ", ") + " for the address the plan's refusal names"})
	}
	return out
}

// projectSecretAccountOwners maps the IAM member string of every service
// account the local config's own repositories are deployed under to that
// repository: the build account, each workflow's job account, and each named
// Claude account's own variant of each workflow. A repository whose provider
// the local config does not record contributes nothing: its slug cannot be
// computed, and a hash cannot be inverted.
//
// Every named account of claude_accounts is enumerated, not only the ones
// AllowedClaudeAccounts lists for that repository today: the map answers
// "which repository is this account", and the grant this check exists to
// find is precisely one an account's allow_data_to no longer covers (the
// variant job was deployed while it still did, design §7). Enumerating only
// today's allow-list would leave that very grant unmatched.
func projectSecretAccountOwners(lc *localcfg.Config) map[string]string {
	out := map[string]string{}
	email := func(id string) string { return id + "@" + lc.GCPProject + ".iam.gserviceaccount.com" }
	accounts := slices.Sorted(maps.Keys(lc.ClaudeAccounts))
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		slug, err := task.Slug(lc.Repos[repo].Provider, repo)
		if err != nil {
			continue
		}
		out["serviceAccount:"+email(gcp.BuildServiceAccountID(slug))] = repo
		for _, w := range lc.Repos[repo].Workflows {
			out["serviceAccount:"+email(gcp.ServiceAccountID(slug, w))] = repo
			for _, a := range accounts {
				out["serviceAccount:"+email(gcp.ServiceAccountID(slug, gcp.WorkflowVariant(w, a)))] = repo
			}
		}
	}
	return out
}

// claudeAccountsStoredCheck is doctor's "claude-accounts-stored" check
// (design docs/design/named-oauth-tokens.md §6f, plan Task 8): every named
// Claude account repo may choose (config.AllowedClaudeAccounts, "default"
// excluded, since that is the repository's existing, unconditionally
// required secret and already covered by doctor's other checks) should have
// a stored token. It adds no Secret Manager call of its own: listed is
// whatever doctorSecrets already fetched and project whatever
// doctorProjectSecrets did (metadata only, by label, never a value), so an
// installation with no claude_accounts configured for repo triggers no new
// read at all.
//
// Both listings are read because D2 (plan Task 2.1) made the project copy
// the default store for exactly these names: a named account's token is
// project-wide-eligible when its own allow_data_to lists repositories, which
// is the case every account here is in. Reading only the repository's own
// containers made doctor call a token stored once for the project missing,
// and its Fix -- `secrets set <secret> --repo <repo>` -- then stored a
// repository copy that wins over the project copy forever (§5, the footgun
// the project-secret checks already name). A missing token's Fix is
// therefore the bare command, which stores it where D2's default puts it,
// reaching exactly the repositories the account's allow_data_to lists.
//
// listed == nil or !projectOK means one of those listings could not be
// attempted or failed (no origin, no credentials, Secret Manager
// unreachable): this is reported as a warning, never silence, since
// otherwise an owner could not tell whether a configured account is actually
// usable (fail closed). Every name or slug this check echoes is bounded by
// pluginwire.Printable, since a repository's origin remote and the owner's
// own local config are both outside this function's control.
func claudeAccountsStoredCheck(lc *localcfg.Config, repo string, listed []secretEntry, project []gcp.SecretInfo, projectOK bool) *doctorCheck {
	var named []string
	for _, n := range config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo) {
		if n != config.DefaultClaudeAccount {
			named = append(named, n)
		}
	}
	if len(named) == 0 {
		return nil
	}
	printableNamed := make([]string, len(named))
	for i, n := range named {
		printableNamed[i] = pluginwire.Printable(n)
	}
	printableRepo := pluginwire.Printable(repo)
	if listed == nil || !projectOK {
		return &doctorCheck{ID: "claude-accounts-stored", Severity: "warning",
			Problem: fmt.Sprintf("could not list %s's secrets (or the project's), so whether its named Claude account(s) (%s) have a stored token could not be checked",
				printableRepo, strings.Join(printableNamed, ", ")),
			Fix: "rerun doctor with Secret Manager reachable and your credentials available, or check by hand: " + selfCommand() + " secrets ls --repo " + quoteWord(repo) + ", and " + selfCommand() + " secrets ls --project-wide"}
	}
	// Either scope's container with an enabled version is a stored token: the
	// repository's own copy wins where it exists (§5), and the project's is
	// what the job mounts otherwise. Every name asked about here is one this
	// repository may use project-wide (config.AllowedClaudeAccounts above is
	// the account's own AllowsData, which is what
	// infra.ProjectSecretCandidate reads for these names), so the project
	// listing needs no per-name re-check.
	have := projectSecretEnabledFromEntries(listed)
	for name, enabled := range projectSecretEnabled(project) {
		if enabled {
			have[name] = true
		}
	}
	var missing []string
	var cmds []string
	for _, n := range named {
		secret := config.ClaudeAccountSecret(n)
		if !have[secret] {
			missing = append(missing, pluginwire.Printable(n))
			cmds = append(cmds, "claude setup-token", selfCommand()+" secrets set "+quoteWord(secret))
		}
	}
	if len(missing) == 0 {
		return &doctorCheck{ID: "claude-accounts-stored", OK: true}
	}
	return &doctorCheck{ID: "claude-accounts-stored",
		Problem: fmt.Sprintf("named Claude account(s) %s have no stored token for %s", strings.Join(missing, ", "), printableRepo),
		Fix:     strings.Join(cmds, "; ")}
}

// projectAccountJob is one shared job a profile serves for a Claude account:
// the (profile, provider, account) triple's own job name, the account the job
// holds the token of, and the repositories that account's token may be used
// by. It is the join config.ProjectJobAccounts and
// config.ProjectJobTokenAccount make — the same one internal/infra deploys
// the job with, internal/cli launches into and internal/runner re-checks
// (design project-named-oauth-tokens §4.4, D9) — read here off the local
// config, which is where a profile's accounts and an account's repositories
// are written.
type projectAccountJob struct {
	Profile string   // the profile whose job set this entry is
	Account string   // the account as the policy's accounts block spells it ("default" included)
	Job     string   // gcp.ProjectJobName(profile, provider, account)
	Token   string   // config.ClaudeAccountSecret(account): the container the job mounts
	Repos   []string // the account's own repositories, as the local config writes them
	Source  string   // the key those repositories are written in
	// NoAccountJob is true when the account rides the profile's no-account
	// job rather than one of its own: the default account's case (D5), the
	// one account whose token is mounted into a job whose name does not carry
	// it.
	NoAccountJob bool
}

// projectAccountJobs is every shared job the project's profiles serve for a
// Claude account, sorted by job name: one entry per (profile, provider,
// account) triple a deploy builds — each profile's no-account job when it
// lists the default account, plus one job per named account it lists — with
// the token that job mounts and the repositories that token may be used by.
//
// A profile that lists no account contributes nothing: its no-account job
// carries no Claude token, and an installation that uses no accounts gets no
// check at all from this (the same silence claudeAccountsStoredCheck keeps).
//
// The two halves come from the local config rather than from the published
// policy, because that is the document doctor can read on a machine that has
// never published: the policy's accounts block is derived from exactly these
// two fields (projectpolicy.DeriveAccounts), and `fugaro project policy show`
// prints the node as it stands.
func projectAccountJobs(lc *localcfg.Config) []projectAccountJob {
	var out []projectAccountJob
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		for _, account := range config.ProjectJobAccounts(pr) {
			token, serves := config.ProjectJobTokenAccount(pr, account)
			if !serves {
				// The job holds no Claude token: a no-account job of a
				// profile that does not list the default account.
				continue
			}
			j := projectAccountJob{
				Profile: name, Account: token, Job: gcp.ProjectJobName(name, pr.Provider, account),
				Token: config.ClaudeAccountSecret(token), NoAccountJob: account == "",
			}
			if token == config.DefaultClaudeAccount {
				// The default account is not definable in claude_accounts
				// (its name is reserved there): its list — and so its
				// project-wide container — is the project_secrets entry
				// (design §4.2, D5/D6).
				j.Source = "project_secrets." + config.ClaudeAccountSecret(token) + ".allow_data_to"
				j.Repos = lc.ProjectSecrets[config.ClaudeAccountSecret(token)].AllowDataTo
			} else {
				j.Source = "claude_accounts." + pluginwire.Printable(token) + ".allow_data_to"
				j.Repos = lc.ClaudeAccounts[token].AllowDataTo
			}
			out = append(out, j)
		}
	}
	slices.SortFunc(out, func(a, b projectAccountJob) int { return strings.Compare(a.Job, b.Job) })
	return out
}

// projectAccountReach is one account job's reach, as one line: which job it
// is, which account and container it holds, and the repositories that token
// may be used by. The list is spelled the way the local config writes it (a
// slug, owner/name); the policy qualifies each entry with the job's provider
// when it is published (projectpolicy.DeriveAccounts), and `fugaro project
// policy show` prints that node.
func projectAccountReach(j projectAccountJob) string {
	repos := "none"
	if len(j.Repos) > 0 {
		printable := make([]string, len(j.Repos))
		for i, r := range j.Repos {
			printable[i] = pluginwire.Printable(r)
		}
		repos = strings.Join(printable, ", ")
	}
	return fmt.Sprintf("%s (profile %s, account %s, token %s, repositories %s in %s)",
		j.Job, pluginwire.Printable(j.Profile), pluginwire.Printable(j.Account), j.Token, repos, j.Source)
}

// defaultAccountNote is the sentence doctor owes the owner about a profile
// that lists the default Claude account, and "" when no profile does.
//
// It exists because that account is the one whose blast radius is not a job
// of its own: its token (claude-oauth-token) rides the profile's no-account
// job (D5), and a static secret_env mount is in the container before the run
// starts, so EVERY run of that job holds the token — including a run whose
// agent.auth is not oauth, which authenticates with its own credential and
// never reads it — a Vertex one, and an api-key one under the gateway, whose
// Anthropic key is optional there (agent.BuildEnv). Nothing refuses those runs
// (design §4.3: the non-OAuth ones are legitimate), so the reach is documented
// rather than gated — and doctor is where an owner reads it. The residual
// itself is SECURITY.md's (D8).
func defaultAccountNote(jobs []projectAccountJob) string {
	var lines []string
	for _, j := range jobs {
		if j.Account != config.DefaultClaudeAccount || !j.NoAccountJob {
			continue
		}
		lines = append(lines, fmt.Sprintf("note: the profile %s lists the default Claude account, whose token %s rides its no-account job %s: every run of that job holds it, including a run whose agent.auth is not oauth",
			pluginwire.Printable(j.Profile), j.Token, j.Job))
	}
	return strings.Join(lines, "; ")
}

// projectAccountStoredCheck is doctor's "project-account-stored" check (plan
// task 3, design §4.4): every shared job the project's profiles serve for a
// Claude account mounts that account's token by a static secret_env entry
// (§4.3, D1), so the container the job names must have a stored version —
// otherwise every run of that job starts with a credential that is not
// there, and the failure is the run's, in the agent.
//
// It is the project-wide twin of claudeAccountsStoredCheck, and it is keyed
// differently on purpose: that check asks what the REPOSITORY may choose
// (config.AllowedClaudeAccounts) and counts either scope's copy as stored,
// because a per-repository run mounts whichever one exists. A shared job
// mounts the container its secret_env names, which is always the
// project-wide one (infra.ProjectSecretCandidate, gcp.ProjectSecretID), so a
// repository copy does not reach it — this check asks the project's listing
// for that name and nothing else.
//
// It reads no value: project is doctorProjectSecrets' metadata listing, read
// with the credentials doctor already holds (the launcher role lists secret
// metadata, which is the permission a launcher needs and no more), and the
// only thing read off an entry is whether it has an enabled version.
//
// Severity is claude-accounts-stored's discipline: an installation that
// serves no account job gets no check at all (no new output over the release
// before this one); a listing that could not be attempted or failed is a
// warning, never silence, since otherwise an owner could not tell whether a
// configured account is usable (fail closed); and a missing version is a
// failing check with the commands that store it.
func projectAccountStoredCheck(lc *localcfg.Config, project []gcp.SecretInfo, projectOK bool) *doctorCheck {
	jobs := projectAccountJobs(lc)
	if len(jobs) == 0 {
		return nil
	}
	reach := make([]string, len(jobs))
	for i, j := range jobs {
		reach[i] = projectAccountReach(j)
	}
	note := defaultAccountNote(jobs)
	problem := func(s string) string {
		if note == "" {
			return s
		}
		return s + "; " + note
	}
	// !projectOK is "the listing could not be attempted or failed". A nil
	// slice with projectOK true is not that: gcp.Secrets.List leaves its
	// result nil when nothing matches the filter, and an installation that
	// has stored nothing is exactly the state this check must report.
	if !projectOK {
		return &doctorCheck{ID: "project-account-stored", Severity: "warning",
			Problem: problem(fmt.Sprintf("could not list the project's secrets, so whether the token of its Claude account job(s) is stored could not be checked: %s",
				strings.Join(reach, "; "))),
			Fix: "rerun doctor with Secret Manager reachable and your credentials available, or check by hand: " + selfCommand() + " secrets ls --project-wide"}
	}
	have := projectSecretEnabled(project)
	var missing, cmds []string
	for _, j := range jobs {
		if have[j.Token] {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s (profile %s, account %s, token %s)", j.Job,
			pluginwire.Printable(j.Profile), pluginwire.Printable(j.Account), j.Token))
		cmds = append(cmds, "claude setup-token", selfCommand()+" secrets set "+quoteWord(j.Token)+" --project-wide")
	}
	if len(missing) == 0 {
		return &doctorCheck{ID: "project-account-stored", OK: true, Severity: "info",
			Problem: problem(fmt.Sprintf("the project's Claude account job(s), with the repositories each one's token may be used by: %s", strings.Join(reach, "; "))),
			Fix:     "an account job mounts its account's token by a static secret_env entry, so its container is the project-wide copy: 'fugaro secrets set TOKEN --project-wide' stores it, and a repository copy does not reach the job. The launch gate refuses an OAuth run whose account the profile does not serve and a repository the account's list does not cover, and the runner re-decides both on its own. The jobs above are Cloud Run jobs like any other: the launcher-jobs line reports a launcher's own reach over them"}
	}
	return &doctorCheck{ID: "project-account-stored",
		Problem: problem(fmt.Sprintf("the Claude account job(s) %s mount a token with no stored version in the project's secrets: their runs start without the credential the job hands them",
			strings.Join(missing, ", "))),
		Fix: strings.Join(cmds, "; ")}
}

// oauthTokenAllowedCheck is doctor's "oauth-token-allowed" check (design
// docs/design/named-oauth-tokens.md §6f, plan Task 8): the checkout's
// agent.oauth_token, if set, must name an account config.AllowedClaudeAccounts
// says repo may actually use, for a workflow whose agent.auth is oauth — a
// name the local config does not know, one whose allow_data_to omits repo,
// or one named for a non-oauth workflow (fugaro run already refuses that
// combination; doctor must not disagree) would otherwise only surface at
// fugaro run time, after a PR already merged the fugaro.yaml change that
// sets it. Pure offline: no GCP call of any kind, only the already-loaded
// local config and the checkout's own already-resolved fugaro.yaml (token,
// auth). token == "" (the key unset, meaning the repository's default
// account) makes no check at all: an installation that never sets
// agent.oauth_token sees no new output. Every name this check echoes is
// bounded by pluginwire.Printable, since the checkout's fugaro.yaml and the
// owner's local config are both outside this function's control.
func oauthTokenAllowedCheck(lc *localcfg.Config, repo, token, auth string) *doctorCheck {
	if token == "" {
		return nil
	}
	// Mirrors claudeAccountAllowed's own rule (internal/cli/run.go): naming
	// an account for a workflow whose agent.auth isn't oauth is refused
	// outright, never merely left unchecked — no secret is ever mounted for
	// it, so the name would be silently unused. Review finding (round 2):
	// doctor used to report this combination OK, disagreeing with fugaro
	// run, which already refuses it.
	if auth != "oauth" {
		return &doctorCheck{ID: "oauth-token-allowed",
			Problem: fmt.Sprintf("fugaro.yaml's agent.oauth_token %s only applies to a workflow whose agent.auth is oauth; %s's agent.auth is %s",
				pluginwire.Printable(token), pluginwire.Printable(repo), pluginwire.Printable(auth)),
			Fix: "remove agent.oauth_token, or change agent.auth to oauth"}
	}
	allowed := config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)
	if slices.Contains(allowed, token) {
		return &doctorCheck{ID: "oauth-token-allowed", OK: true}
	}
	printableAllowed := make([]string, len(allowed))
	for i, n := range allowed {
		printableAllowed[i] = pluginwire.Printable(n)
	}
	return &doctorCheck{ID: "oauth-token-allowed",
		Problem: fmt.Sprintf("fugaro.yaml's agent.oauth_token %s is not one %s may use (allowed: %s)",
			pluginwire.Printable(token), pluginwire.Printable(repo), strings.Join(printableAllowed, ", ")),
		Fix: fmt.Sprintf("add claude_accounts.%s.allow_data_to: [%s] to the operator's local config, or change agent.oauth_token in fugaro.yaml to one of the allowed names",
			pluginwire.Printable(token), pluginwire.Printable(repo))}
}

// oauthTokenOf is token's resolvedFile.Cfg.Agent.OAuthToken, "" when rf
// (fugaroYAMLCheck's third return) is nil: an invalid fugaro.yaml, or one
// whose project layer resolution failed, already has its own failing
// "fugaro-yaml" check, so oauthTokenAllowedCheck simply has nothing to say
// rather than guessing at a value that was never resolved.
func oauthTokenOf(rf *resolvedFile) string {
	if rf == nil || rf.Cfg == nil {
		return ""
	}
	return rf.Cfg.Agent.OAuthToken
}

// authOf is rf's resolvedFile.Cfg.Agent.Auth, "" when rf is nil: the same
// "nothing resolved" case oauthTokenOf already treats as having nothing to
// say.
func authOf(rf *resolvedFile) string {
	if rf == nil || rf.Cfg == nil {
		return ""
	}
	return rf.Cfg.Agent.Auth
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

// printDoctorChecks prints one line per check doctor means to show (its
// problem and fix), or a single "ok" line when none failed.
//
// An ok check with no Severity is doctor's own bookkeeping and stays out of
// the human output. A Severity is the opposite: it is how the checks that
// report what doctor measured rather than what it found wrong — the launcher
// grants over the Cloud Run jobs, the project's Claude account jobs and the
// repositories each token may be used by — ask to be read, and skipping those
// by their OK flag left them in --json alone.
func printDoctorChecks(w io.Writer, checks []doctorCheck) {
	failed := false
	for _, c := range checks {
		if c.OK && c.Severity == "" {
			continue
		}
		// An "info" check is what doctor *measured*, never what it found
		// wrong (checksFail: an info check does not fail doctor, with or
		// without --strict), so it must not suppress the summary line either:
		// counting it as a failure said "something is wrong" — by printing no
		// "everything checked is ok" — on an installation whose only line was
		// the launcher grants it had just measured. The surface a person has
		// to be able to read is the whole point of such a check, and the
		// summary line is exactly what tells them the read found nothing.
		if !c.OK && c.Severity != "info" {
			failed = true
		}
		fmt.Fprintln(w, doctorCheckLine(c))
	}
	if !failed {
		fmt.Fprintln(w, "fugaro doctor: everything checked is ok")
	}
}

// doctorCheckLine is one check in doctor's long-standing line shape: the
// severity, the id, the whole problem and the whole fix. --all prints every
// check this way, and --security prints the security half of the sweep this
// way, since what it answers is long by nature and a clipped answer does not.
func doctorCheckLine(c doctorCheck) string {
	line := c.ID + ": "
	if c.Severity != "" {
		line = c.Severity + " " + line
	}
	line += c.Problem
	if c.Fix != "" {
		line += " (" + c.Fix + ")"
	}
	return line
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

func emitDoctor(cmd *cobra.Command, o doctorOutput, asJSON bool, view doctorView) error {
	// The class is attached here, on every path and for every reader, so the
	// human view and --json are the same classification of the same checks.
	o.Checks = classifyDoctorChecks(o.Checks)
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(o); err != nil {
			return err
		}
	} else {
		out := cmd.OutOrStdout()
		// The release line is the "a tag can be moved" audit sentence, three
		// hundred characters of it; the project line and the secret list are
		// context, not work. All three belong to --all: the default view ends
		// with its pointer lines, and a line after them is a line a reader who
		// stopped at the pointer never sees (a clean installation prints the one
		// ok line and nothing else).
		if view.all {
			if l := releaseLine(); l != "" {
				fmt.Fprintln(out, l)
			}
		}
		printDoctorReport(out, o.Checks, view)
		if view.all {
			if p := o.Project; p != nil {
				fmt.Fprintf(out, "project: %s (GCP %s, %s)\n", p.Name, p.GCPProject, p.Region)
			}
			printDoctorSecrets(out, o.Secrets)
		}
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
		return []doctorCheck{{ID: "shared-config", Severity: "info", Problem: problem, Fix: "fugaro setup creates your own local config"}}
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
		Fix:     "fugaro update publishes your local config's installation-wide fields"}}
}

// policyNewerThanBinaryCheck is doctor's line for a policy node written by a
// newer Fugaro (the ve.Found > ve.Understood branch of doctorPolicyChecks).
// It is a function of the version error alone, which is what lets the line be
// tested for a node this binary cannot meet through its own read — a version
// above the one it understands is refused by Decode before doctor sees a
// node, so the rig can only write Version+1, a version projectpolicy.VersionSince
// deliberately has no row for.
//
// The fix names the release that introduced the version on the node when the
// table knows it (policyTooNewFix), because "upgrade Fugaro" alone is advice
// an operator on a release that already writes that version cannot act on:
// the whole complaint behind the table is that the release the model ships in
// (policySince) is not the release a version above the reader's arrived in.
// The problem line is projectpolicy.CLISkew, the shared sentence with this
// reader's remedy, exactly as the launch gate prints it.
func policyNewerThanBinaryCheck(ve *projectpolicy.VersionError, shared string) doctorCheck {
	return doctorCheck{ID: "policy-newer-than-binary", Severity: "warning",
		Problem: fmt.Sprintf("%s: %s", fmt.Sprintf(projectpolicy.CLISkew, ve.Found, ve.Understood), shared),
		Fix:     policyTooNewFix(ve.Found)}
}

// doctorPolicyChecks reports the published project policy and the state
// around it (plan task 1.6 step 4): whether one exists and which version,
// whether this binary understands it (P8), whether the installation's jobs
// still run a runner image that predates the policy model, whether the
// allow-list it carries admits anything, and whether the deployed rules are
// the generation that can read it.
//
// It reads, and only reads: the policy node and the rules marker with the
// operator's own credentials, plus the local config. Nothing here writes,
// and every outcome is a check — a database that cannot be reached is a
// note, never a failure, which is the shape the rest of doctor has.
//
// layer is the project layer's claude_models claims doctor's own checkout
// resolved, for the routing checks at the end (doctorRoutingChecks): the
// routes a run resolves the switch over carry them, and doctorRun resolves the
// checkout before this, so they travel in rather than being read here.
func doctorPolicyChecks(ctx context.Context, lc *localcfg.Config, layer doctorRoutingLayer) []doctorCheck {
	shared := projectpolicy.PathShared()
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		// The state of every installation that has not run update --firebase-project:
		// there is no node to read, and the runs take their ceiling from the
		// job environment, exactly as they did before this model (P3).
		checks := []doctorCheck{{ID: "policy-published", Severity: "info",
			Problem: fmt.Sprintf("project %s has no Firebase budget backend, so it cannot publish a project policy: runs take their ceiling from the job environment", lc.Name),
			Fix:     "fugaro update --firebase-project " + firebaseProjectIDWord(lc.GCPProject) + " --name " + lc.Name + " adds the backend the policy is published to." + adoptOwnFirebaseProject(lc)}}
		return append(checks, doctorRoutingChecks(lc, nil,
			fmt.Sprintf("project %s has no Firebase budget backend, so there is no policy node to read a switch from", lc.Name), layer)...)
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return append([]doctorCheck{{ID: "policy-published", Severity: "info", unread: true,
			Problem: fmt.Sprintf("whether project %s publishes a policy could not be checked: %v", lc.Name, err),
			Fix:     "fix budget.rtdb_url in the project's config, then run doctor again"}},
			unreadRouting(doctorRoutingChecks(lc, nil, fmt.Sprintf("the budget database could not be reached (%v), so the published switch cannot be read", err), layer))...)
	}
	// And the database must be this project's own, the check every other
	// reader and writer makes (checkDBProject): everything below — the policy,
	// the rules markers and the credential marks — is read as this project's,
	// and a budget.rtdb_url pointing at another project's database would
	// otherwise have doctor report that project's policy, switch and broken
	// accounts as this one's. A note, not a failure, like every other read
	// that did not answer.
	if err := checkDBProject(ctx, db, lc); err != nil {
		return append([]doctorCheck{{ID: "policy-published", Severity: "info", unread: true,
			Problem: fmt.Sprintf("whether project %s publishes a policy could not be checked: %v", lc.Name, err),
			Fix:     "fix budget.rtdb_url in the project's config, then run doctor again"}},
			unreadRouting(doctorRoutingChecks(lc, nil, fmt.Sprintf("the budget database is not project %s's own (%v), so the published switch cannot be read", lc.Name, err), layer))...)
	}
	checks := doctorRulesSkew(ctx, db, lc)
	checks = append(checks, doctorCredsBroken(ctx, db)...)
	// The node is read through projectpolicy.Read, the reader a run uses
	// (design §5(3)), not with a plain Get: with no marker, or one below
	// PolicyRulesVersion, no run reads the policy however the node is written, and a
	// check that read the node directly printed "a policy is published" as info
	// while every run took the legacy path and the job environment's ceiling.
	p, found, err := projectpolicy.Read(ctx, db)
	var me *projectpolicy.MarkerError
	var ve *projectpolicy.VersionError
	switch {
	case errors.As(err, &me):
		// The marker is not a generation at all. doctorRulesSkew has reported it,
		// and Read cannot get past it — neither can a run — so there is nothing
		// further doctor can honestly say about the node.
		return append(checks, unreadRouting(doctorRoutingChecks(lc, nil,
			fmt.Sprintf("%s does not record the rules generation a policy is read under, so no run reads the node and no switch can be resolved from it", projectpolicy.PathRulesMarker()), layer))...)
	case errors.As(err, &ve):
		// P8's release skew, both directions: Decode refuses a node of any other
		// version, and the remedies are opposite. A newer node needs a newer
		// Fugaro. An older one has two states, and the migration chain decides
		// between them (projectpolicy.Migratable): a version the chain carries
		// forward (3, which 0.9.0 wrote) is what the policy stage of the converge
		// migrates in place (init_policy_migrate.go), so this line is the
		// update's own (doctorUpdatePrefixes). A version below the chain's first
		// step (1 or 2, which 0.8.0 and 0.8.5 wrote) is one no step reaches —
		// the stage and a publish both refuse it with the delete-then-publish —
		// and there the check keeps its own command (ownFix: the class is the
		// ID's, and the state's move is not the class's).
		if ve.Found > ve.Understood {
			checks = append(checks, policyNewerThanBinaryCheck(ve, shared))
			return append(checks, unreadRouting(doctorRoutingChecks(lc, nil,
				fmt.Sprintf("the node at %s is a policy of version %d, which this Fugaro cannot decode, so its switch is not read", shared, ve.Found), layer))...)
		}
		migrates := projectpolicy.Migratable(ve.Found)
		problem := fmt.Sprintf("%s holds a policy of version %d, and this Fugaro writes version %d: no run applies it, and publishing over it is refused", shared, ve.Found, ve.Understood)
		fix := fmt.Sprintf("run `fugaro update`: its policy stage rewrites the node in place at this Fugaro's version %d, keeping the members the node carries, so nothing has to be deleted and published again", ve.Understood)
		if !migrates {
			fix = fmt.Sprintf("fugaro project policy publish --delete removes it, then 'fugaro project policy publish' writes this Fugaro's version %d: no migration step reaches version %d, and a publish rebuilds the node from this machine's config, so read the list out with `fugaro project policy show` first", ve.Understood, ve.Found)
		}
		checks = append(checks, doctorCheck{ID: "policy-older-than-binary", Severity: "warning", ownFix: !migrates,
			Problem: problem, Fix: fix})
		return append(checks, doctorRoutingChecks(lc, nil,
			fmt.Sprintf("the node at %s is a policy of version %d, which no run applies, so no switch is in force", shared, ve.Found), layer)...)
	case err != nil:
		// A read that failed is not "no policy": doctor says which it is.
		checks = append(checks, doctorCheck{ID: "policy-published", Severity: "info", unread: true,
			Problem: fmt.Sprintf("the project policy could not be read (%v), so whether one is published, and whether the runs are bounded by it, is unknown", err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"})
		return append(checks, unreadRouting(doctorRoutingChecks(lc, nil,
			fmt.Sprintf("the policy at %s could not be read (%v), so the switch it holds is unknown", shared, err), layer))...)
	case !found:
		info := doctorCheck{ID: "policy-published", Severity: "info",
			Problem: fmt.Sprintf("no policy is published at %s: runs take their ceiling from the job environment", shared),
			Fix:     "fugaro project policy publish publishes the project's policy"}
		// A node can be written and still govern nothing. "No policy is
		// published" is true of every run and silent about the node; the
		// operator looking at a database where one sits — written by hand, or
		// left by a rules deploy an older CLI rolled back — needs both.
		var raw json.RawMessage
		if ok, gerr := db.Get(ctx, shared, &raw); gerr == nil && ok && !emptyPolicyNode(raw) {
			info.Problem = fmt.Sprintf("a policy node is written at %s, but it governs nothing: %s does not record the rules generation this Fugaro reads a policy under, so no run reads it and every run takes its ceiling from the job environment",
				shared, projectpolicy.PathRulesMarker())
			info.Fix = "re-run 'fugaro update' with fugaro >= " + policySince + " in your own terminal: its rules stage deploys the rules a policy is read under, then 'fugaro project policy publish' publishes the node"
		}
		return append(checks, append([]doctorCheck{info},
			doctorRoutingChecks(lc, nil, fmt.Sprintf("no policy is published at %s, so there is no switch to resolve", shared), layer)...)...)
	}
	// found, and Read returned no error: the node is in force, so the checks
	// below can read it — Decode's error would have arrived as err above.
	checks = append(checks, doctorCheck{ID: "policy-published", Severity: "info",
		Problem: fmt.Sprintf("a policy is published at %s (version %d, %d allow-list entries)", shared, p.Version, len(p.AllowRepos)),
		Fix:     "fugaro project policy show prints it"})
	if err := refuseUnknownPolicyKeys(p); err != nil {
		// Published and in force by version and marker, and refused by every
		// reader anyway: `policy show`, `project show` and `repos ls` refuse it
		// through this same helper, and so do the launch gates that decide
		// whether a run is bounded by it at all (projectPolicyRead). A key
		// nobody reads is a policy that never takes effect, and doctor — the
		// command an operator runs to find out why a run stopped — reported
		// this node as published and said nothing else, which read as "in
		// force". The body is the helper's own sentence rather than a second
		// wording of it, so what doctor prints is what the command that refused
		// the operator printed, and the two cannot drift.
		checks = append(checks, doctorCheck{ID: "policy-unknown-keys", Severity: "warning",
			Problem: "no run is bounded by the policy node: " + err.Error(),
			Fix:     "remove the unknown key from the node by hand (fugaro project policy show prints it), or upgrade Fugaro (brew upgrade dimipaun/tap/fugaro, then fugaro update) if a newer release writes it; 'fugaro project policy publish' refuses this node too, so publishing is not the fix"})
	}
	if len(p.AllowRepos) == 0 {
		// Empty inside a *published* policy is deny-all (design §5), which is a
		// different state from an installation that published nothing: every
		// run of every repository stops at admission.
		checks = append(checks, doctorCheck{ID: "allow-list-empty", Severity: "warning",
			Problem: fmt.Sprintf("the policy published at %s admits no repository: every run's repository is refused before it mints a git token", shared),
			Fix:     "fugaro project repos add owner/name enrolls one ('fugaro project repos add github/acme/app')"})
	}
	if c := doctorAllowListUncovered(lc, p); c != nil {
		checks = append(checks, *c)
	}
	checks = append(checks, doctorCredsRules(ctx, db, p, lc)...)
	if c := doctorPolicyVsJobs(lc); c != nil {
		checks = append(checks, *c)
	}
	// The two routing checks of plan task 1b.3 step 2, over the node in force:
	// the switch is read off the policy the runs themselves read, and resolved
	// the way a run resolves it (config.RoutingClaudeFor over the routes a run
	// of each repository carries — doctorRoutingChecks).
	return append(checks, doctorRoutingChecks(lc, p, "", layer)...)
}

// routingCacheMarkersUnmeasured is what doctor says when the policy routes a
// Claude ID: whether a provider route reports cache_read/cache_write tokens at
// all is unmeasured, and a routed Claude senior that never reports a cache read
// is charged at the full input rate — the difference between list price and
// several times list price for a long senior review (design §6.2). Check 50
// (written by Task 1b.4) is the run that measures it, and until it is run the
// cost of a routed Claude senior is a guess.
const routingCacheMarkersUnmeasured = "a routed Claude senior's cost is unmeasured: whether the route reports cache_read/cache_write tokens at all is Check 50's question (docs/gcp-live-checklist.md, NOT RUN), and a routed Claude that never reports a cache read is charged at the full input rate rather than the cache rate"

// routingCacheMarkersNothingRouted is the same check on a policy that routes no
// Claude ID: the question is not live yet, and saying which state this is is
// what keeps the sentence above from reading as a warning about nothing.
const routingCacheMarkersNothingRouted = "no Claude ID is routed through a provider by this policy, so no call is charged at a route's rate: when the switch routes one, whether that route reports cache_read/cache_write tokens at all is unmeasured until Check 50 runs"

// routingCacheMarkersNoSwitch is the same check when no switch is in force: no
// Firebase backend, an unreachable budget database, a marker, a node this binary
// cannot read, a failed read, or a policy published that no run applies — the
// version skew in either direction, which is a node doctor read and cannot use.
// Every one of those paths reaches doctorRoutingChecks with no node, and "no
// Claude ID is routed through a provider by this policy" is a reading of a node
// nobody resolved — doctor said the resolution was unresolved on the line beside
// it and then reported a reading of the same node. It says which state this is
// instead, as routing-resolution does, and it says it of the switch rather than
// of the read: a policy that is published at a version no run applies was read,
// and "the policy node was not read" would be false about it.
const routingCacheMarkersNoSwitch = "no routing switch is in force here (see routing-resolution), so whether a Claude ID is routed through a provider — and so whether any call is charged at a route's rate — is unknown from this run: when one is, whether the route reports cache_read/cache_write tokens at all is Check 50's question (docs/gcp-live-checklist.md, NOT RUN)"

// routingCacheMarkersUndetermined is the same check when the resolution above
// could not be read at all: doctor cannot say a routed Claude's cost is
// unmeasured (nothing may route) or that nothing is charged at a route's rate
// (everything may), so it says which state this is rather than picking one.
const routingCacheMarkersUndetermined = "whether a routed Claude senior's cost is unmeasured is undetermined here: no repository this machine's config names resolves the switch (see routing-resolution), so whether any call is charged at a route's rate at all is unknown; when one is, whether the route reports cache_read/cache_write tokens at all is Check 50's question (docs/gcp-live-checklist.md, NOT RUN)"

// routingUnresolvedNoRepo is why the resolution is unresolved when the machine's
// config names no repository: the routes a run resolves the switch over are per
// repository (the providers whose allow_data_to admits it), so with no
// repository named there is no set of routes to resolve over — and a resolution
// doctor did not resolve is not printed as one, which is the resolver's own
// rule (config.RoutingClaudeFor).
const routingUnresolvedNoRepo = "this machine's project config names no repository, and the routes a run resolves the switch over are the ones that repository is allowed to send code to (allow_data_to); run 'fugaro update --repo' in the repository's checkout to record it here"

// doctorRoutingChecks are doctor's two routing checks (plan task 1b.3 step 2).
//
// routing-resolution prints the resolution the published policy licenses: one
// line per Claude ID the switch names, per repository this machine's config
// names, resolved over the routes a run of that repository carries — the same
// resolver as a run (config.RoutingClaudeFor) and the same table. Doctor first
// resolved over the node's whole providers block, and that is a route a run may
// not have: a provider entry that does not list the repository in
// allow_data_to is not in FUGARO_MODEL_PROVIDERS at all, so that run halts with
// "the project's routing switch is not usable" while doctor called the route
// resolved — false confidence in the one command an operator runs to find out
// why a run stopped. It resolves over this machine's providers, filtered by
// allow_data_to for the repository (runner.ProvidersFor, what
// runner.ProvidersEnv writes into the job's environment), with the project
// layer's claude_models claims merged in for the repository the machine's
// checkout resolved them for (doctorRoutingLayer) — the merge a run makes
// (runner.resolveConfig: config.ClaimsOnRoutes, then config.ApplyClaudeClaims),
// because a claim is how a route the owner defined comes to serve a Claude ID
// the owner's own models list does not name.
//
// A state doctor cannot read is said to be unresolved, never printed as a
// resolution: the node's own why, a machine whose config names no repository at
// all, and a layer whose claims this machine has no checkout to read (the layer
// applies to a repository whose fugaro.yaml names its project and gcp_project,
// which is a fact about that repository's own file).
//
// routing-cache-markers names the live question a routed Claude senior leaves
// open until Check 50 (written by Task 1b.4) is run.
//
// Both are info, and neither ever fails doctor: an unmeasured cost and a
// resolution the credentials or the config do not let doctor read are not
// defects in a configuration, and the resolution is a fact about the node
// rather than a verdict on it.
func doctorRoutingChecks(lc *localcfg.Config, p *projectpolicy.Policy, why string, layer doctorRoutingLayer) []doctorCheck {
	if p == nil {
		// The node nobody read: the layer is not consulted here, because a
		// switch that is not in force resolves nothing whatever a layer claims.
		return []doctorCheck{
			{ID: "routing-resolution", Severity: "info",
				Problem: "the routing resolution is unresolved: " + why,
				Fix:     "fugaro project policy show prints the switch the published node holds, and `fugaro validate` prints this line from the operator's own config (offline)"},
			{ID: "routing-cache-markers", Severity: "info",
				Problem: routingCacheMarkersNoSwitch,
				Fix:     "fugaro project policy publish writes the switch the owner's config holds; Check 50 measures what a routed Claude costs"},
		}
	}
	var c *config.RoutingClaude
	if p.Routing != nil {
		c = p.Routing.Claude
	}
	// The repositories this machine's config names, in the spelling a run
	// passes and allow_data_to is written in — owner/name (infra.Inputs.Repo)
	// — not the canonical provider/owner/name the allow-list is written in
	// (doctorCanonicalRepos). Two questions, two spellings: a check that
	// swapped them would resolve over a table no run has.
	repos := slices.Sorted(maps.Keys(lc.Repos))
	if len(repos) == 0 {
		return []doctorCheck{
			{ID: "routing-resolution", Severity: "info",
				Problem: "the routing resolution is unresolved: " + routingUnresolvedNoRepo,
				Fix:     "fugaro project policy show prints the switch the published node holds, and `fugaro validate` prints the resolution from the same config this check needs"},
			{ID: "routing-cache-markers", Severity: "info", Problem: routingCacheMarkersUndetermined,
				Fix: "fugaro run's model call lines record serving_model, priced_as, cache_read and cache_write_5m: Check 50 is the run that compares them on a routed Claude senior"},
		}
	}
	var lines []string
	routed := false
	var named []string
	if c != nil {
		named = c.Models
	}
	if len(named) == 0 {
		// Two states read as one here before: no switch at all, and a switch
		// that is published and names no Claude ID. The second is a legal block
		// — `via: direct` with an empty list routes nothing, which is D1b's
		// default said out loud (CheckRoutingShape) — and "no routing switch is
		// published" is a claim about the node that is false of it. c is
		// non-nil here only for a block that reached the check: projectpolicy
		// holds a node to CheckRoutingShape (validRouting), and an empty list
		// under either open via, or a block written with no via at all, is
		// refused there, so this prints the state the node can actually be in.
		if c == nil {
			lines = append(lines, "no routing switch is published, so every Claude ID goes direct (D1b's default)")
		} else {
			lines = append(lines, fmt.Sprintf("a switch is published with via: %s; every Claude ID goes direct (D1b's default)", c.ViaValue()))
		}
	}
	// Whether a run reaches the switch is the runner's own decision
	// (routingReachOf — runner.RoutingInertReason, the function applyRouting
	// branches on), and it is not the same answer for every switch: a run whose
	// gateway is off returns before it reads the switch, via: auto never routes
	// a Vertex run, and with an Anthropic API key stored in the job auto keeps
	// the pins direct. Every arm below reads it — a resolution printed as the
	// run's where no run takes it is the false confidence this check exists not
	// to give (the review finding on the resolved arm, which printed
	// "-> route, sent as X" and claimed a routed cost for a run whose gateway
	// is off), and a halt asserted where none happens is the same error the
	// other way.
	//
	// It is asked once, outside the loop, because it is the same for every
	// repository and every model: only the routes do not depend on it. It is
	// asked of an api-key credential and the job's own budget mode — doctor
	// reads the policy and this machine's config, not the repository's
	// fugaro.yaml, so neither the credential nor the stored key is its to read.
	// api-key is the credential auto's key question is about, and Vertex
	// answers the same for every other via — the sentence the auto state prints
	// names that difference itself. The mode is the job's (doctorNodeMode: the
	// node's own, or the environment's where the node says nothing).
	mode := doctorNodeMode(lc, p)
	reach, reason := routingReachOf(c, "api-key", mode)
	// inert is appended to a line whose resolution no run of this repository
	// carries out, and its reader is the same one validate's note has: the line
	// says what the switch licenses and the clause says that nothing here takes
	// it, so the resolution is not read as a cost the project is paying.
	inert := ""
	if reach == routingInert {
		inert = " (nothing takes it here: " + routingInertWhy(reason, "api-key", mode) + ", so the pins keep Claude's own IDs)"
	}
	for _, repo := range repos {
		// The routes a run of this repository carries: this machine's
		// providers, filtered by allow_data_to, with the project layer's
		// claude_models merged in when this machine read them for this very
		// repository (doctorRoutingLayer).
		providers := doctorRoutes(lc, repo, layer.claimsFor(repo))
		for _, m := range named {
			route, sent, ok := config.RoutingClaudeFor(c, m, providers)
			switch {
			case !ok:
				base := fmt.Sprintf("%s: %s -> unresolved: the switch names it and none of the routes a run of this repository carries claims it with allow_data_to written", repo, m)
				tail := fmt.Sprintf(" — check the entry's models list, and that its allow_data_to admits %s", repo)
				switch reach {
				case routingAlways:
					lines = append(lines, base+"; a run of this repository whose gateway is on halts at it (the project's routing switch is not usable)"+tail+layer.unknownFor(repo))
				case routingKeyless:
					lines = append(lines, base+"; via: auto is key-presence driven, so a run of this repository whose gateway is on halts at it unless the job has an Anthropic API key stored, and a Vertex run never reaches it at all"+tail+layer.unknownFor(repo))
				default:
					// The mode is the node's when it sets one and the job's own
					// ceiling when it does not (doctorNodeMode), and the line
					// says which: "the published policy's budget" is false of a
					// node that says nothing about the budget, and that reading
					// is what this sentence used to make for it.
					who := "the job's own ceiling"
					if p.Budget.Mode != "" {
						who = "the published policy's budget"
					}
					lines = append(lines, base+", and under "+who+" no run of this repository reaches the switch (budget.mode is "+mode+"), so the switch is inert and nothing halts under it"+tail+layer.unknownFor(repo))
				}
			case route == config.RouteDirect:
				lines = append(lines, fmt.Sprintf("%s: %s -> direct (via: %s)%s", repo, m, c.ViaValue(), inert))
			default:
				// routed is what routing-cache-markers turns on: a Claude ID a
				// run of this repository really sends to a provider, whose
				// cache cost Check 50 has not measured. A resolution no run
				// takes is not that, so it does not set it — under a switch no
				// run reaches, the markers check says no Claude ID is routed,
				// which is what those runs do.
				if reach != routingInert {
					routed = true
				}
				lines = append(lines, fmt.Sprintf("%s: %s -> %s, sent as %s%s", repo, m, route, sent, inert))
			}
		}
	}
	markers := routingCacheMarkersNothingRouted
	if routed {
		markers = routingCacheMarkersUnmeasured
	}
	routes := "the config's providers, filtered by allow_data_to"
	if layer.Claims != nil {
		routes += ", with the project layer's claude_models this machine's checkout resolved merged in"
	}
	return []doctorCheck{
		{ID: "routing-resolution", Severity: "info",
			Problem: "routing resolves as a run of each repository this machine's config names takes it (the routes are that run's own: " + routes + "): " + strings.Join(lines, "; "),
			Fix:     "fugaro project policy show prints the switch, and `fugaro validate` prints the same line for a repository's own pins"},
		{ID: "routing-cache-markers", Severity: "info", Problem: markers,
			Fix: "fugaro run's model call lines record serving_model, priced_as, cache_read and cache_write_5m: Check 50 is the run that compares them on a routed Claude senior"},
	}
}

// doctorRoutingLayer is the project layer's claude_models claims doctor read,
// and the repository it read them for.
//
// A run merges the layer into the routes a repository carries only when that
// repository's own fugaro.yaml names the layer's project and gcp_project
// (config.Resolution/resolveConfig's Applied), and the layer object itself is
// the project's. doctor resolves its checkout's fugaro.yaml over that layer
// (fugaroYAMLCheck), so it knows the claims and that they apply to the
// repository that checkout is — and for no other repository its config names,
// whose fugaro.yaml it has no checkout of. Repo is "" when the checkout's
// repository could not be read (no parseable origin remote): the claims are
// then unknown for every repository, which is what Repo == "" says.
type doctorRoutingLayer struct {
	Claims map[string][]string
	Repo   string
}

// claimsFor are the layer's claims for repo, nil when this machine did not read
// them for it.
func (l doctorRoutingLayer) claimsFor(repo string) map[string][]string {
	if l.Claims == nil || l.Repo != repo || repo == "" {
		return nil
	}
	return l.Claims
}

// unknownFor is what a line about repo must add when a layer with claims was
// read but not for repo: the resolution printed for it is this machine's own
// providers' answer, and the repository's run may have one more route than that.
// It is empty when no layer with claims was read at all (nothing to say) and for
// the repository the claims were read for (the line already resolved with them).
func (l doctorRoutingLayer) unknownFor(repo string) string {
	if l.Claims == nil || l.Repo == repo {
		return ""
	}
	return "; a project layer's claude_models can claim it for this repository, and this machine read the layer only for its own checkout's (a layer applies to a repository whose fugaro.yaml names its project and gcp_project), so whether one does is not a fact this check reads"
}

// doctorRoutingLayerOf is the layer claims the routing checks may merge, from
// the checkout's resolved fugaro.yaml (nil when there is none, or no layer
// applied): the runner's own reading of a layer (config.ClaudeClaimsOf).
func doctorRoutingLayerOf(ctx context.Context, rf *resolvedFile) doctorRoutingLayer {
	if rf == nil {
		return doctorRoutingLayer{}
	}
	claims := config.ClaudeClaimsOf(rf.Layer.Layer)
	if claims == nil {
		return doctorRoutingLayer{}
	}
	// The repository the claims were resolved for is the checkout's own: it is
	// that repository's fugaro.yaml the layer was applied to.
	repo, err := originRepo(ctx)
	if err != nil {
		return doctorRoutingLayer{Claims: claims}
	}
	return doctorRoutingLayer{Claims: claims, Repo: repo}
}

// doctorRoutes are the routes a run of repo carries: this machine's providers
// filtered by allow_data_to for it (runner.ProvidersFor, what
// runner.ProvidersEnv writes into the job's FUGARO_MODEL_PROVIDERS), with the
// project layer's claims merged in exactly as runner.resolveConfig merges them
// — config.ClaimsOnRoutes first (a claim naming a route this repository does
// not have is dropped, not refused: a run cannot tell that name apart from one
// nothing defines), then config.ApplyClaudeClaims.
//
// A merge the rules refuse (a pattern collision, an empty claim) is a run that
// does not start either: runner.resolveConfig fails bootstrap on the same
// problems, so the unmerged table is returned rather than a route table no run
// would ever have.
func doctorRoutes(lc *localcfg.Config, repo string, claims map[string][]string) map[string]config.ModelProvider {
	providers := runner.ProvidersFor(lc.Providers, repo)
	if len(claims) == 0 {
		return providers
	}
	merged, ps := config.ApplyClaudeClaims(providers, config.ClaimsOnRoutes(providers, claims))
	if len(ps) > 0 {
		return providers
	}
	return merged
}

// doctorNodeMode is the budget mode a run of the project has under the
// published node: the node's own mode when it sets one, and the job
// environment's own — this machine's config, whose ceiling update --repo deploys
// into the job (localcfg.BudgetMode) — when it does not.
//
// The fallback is the whole point of the function. runner.policyCeiling
// replaces the environment's ceiling per key, and replaces the mode only when
// the node writes one (pp.Budget.Mode != ""), while projectpolicy's decoder
// refuses a *present* budget block that names no mode — leaving the block out
// is how a policy sets no budget at all. So "" here is "the node says nothing
// about the mode", never "the node says off": reading it as off made this
// check print "no run of this repository reaches the switch (budget.mode is
// off) … nothing halts under it" for a node under an installation that
// enforces, whose every run does halt on the switch.
//
// What the environment's mode is is this machine's own budget block read the
// way the deploy reads it (localcfg.BudgetMode: an absent block, or one with no
// mode, is off), which is the same reading the drift note compares the deployed
// job with. It is not the repository's fugaro.yaml — doctor reads the policy
// and this machine's config — and the credential is api-key for the same
// reason, which the caller's own sentence names.
func doctorNodeMode(lc *localcfg.Config, p *projectpolicy.Policy) string {
	if p == nil || p.Budget.Mode == "" {
		return lc.BudgetMode()
	}
	return p.Budget.Mode
}

// doctorCanonicalRepos are the repositories this machine's project config
// names, as the allow-list spells them: the same conversion
// (projectpolicy.Canonical) the publish seed and doctorAllowListUncovered use,
// so the policy, the seed and this check cannot spell one repository three
// ways. An entry with no provider recorded is skipped rather than guessed: the
// checkout's fugaro.yaml decides it, and doctor does not read a repository's
// file to answer a question about the project's policy.
func doctorCanonicalRepos(lc *localcfg.Config) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider == "" {
			continue
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// doctorAllowListUncovered is the other half of allow-list-empty, and the
// state un-enrolling a repository (`fugaro project repos rm`) leaves behind:
// the policy admits repositories, just not one this machine's config names,
// so that repository's runs are refused at admission — before any git
// credential exists — and the machine that launches them is where the
// operator finds out first.
//
// The names come from the machine's own `repos:` map, the one `update --repo`
// fills, converted with Canonical exactly as the publish seed converts them
// (project_cmd.go's seedAllowRepos), so the two cannot spell a repository
// differently. A repository with no provider recorded is skipped rather than
// guessed: the checkout's fugaro.yaml decides it, and doctor does not read a
// repository's file to answer a question about the project's policy.
func doctorAllowListUncovered(lc *localcfg.Config, p *projectpolicy.Policy) *doctorCheck {
	var refused []string
	for _, c := range doctorCanonicalRepos(lc) {
		if !projectpolicy.AdmitRepo(p.AllowRepos, c) {
			refused = append(refused, c)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return &doctorCheck{ID: "allow-list-uncovered", Severity: "warning",
		Problem: fmt.Sprintf("the policy published at %s does not admit %s, which this machine's config names (allow_repos is %q): a run of one of those repositories is refused before it mints a git token", projectpolicy.PathShared(), oneLine(strings.Join(refused, ", ")), p.AllowRepos),
		Fix:     fmt.Sprintf("fugaro project repos add %s enrolls it; 'fugaro project policy show' prints the allow-list as it stands", refused[0])}
}

// doctorPolicyVsJobs is 12a item 6's trap: a policy is published, and the
// installation's jobs were deployed from a runner image that predates the
// model, so the runs they launch never read the node that governs them —
// they keep taking the ceiling from their own environment, which is looser
// than what the owner published.
//
// The comparison is the one `fugaro image refresh` already makes
// (refreshBase/baseIsCurrent): the recorded base image against this
// release's pin, offline, with no job enumeration and no registry call.
// It is deliberately not a claim about the digests a live job is running —
// doctor reads what the local config records, as it does for every base
// image it reports.
func doctorPolicyVsJobs(lc *localcfg.Config) *doctorCheck {
	cur := lc.BaseImage(config.BaseKind)
	if cur == "" {
		// Nothing recorded: doctor's own base-image check already names that.
		return nil
	}
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return nil
	}
	owner := baseOwner(lc)
	if !isManaged(cur, host, config.BaseKind, owner) || baseIsCurrent(cur, host, config.BaseKind, owner, Version) {
		return nil
	}
	return &doctorCheck{ID: "policy-vs-jobs", Severity: "warning",
		Problem: fmt.Sprintf("the jobs are deployed from the runner image %s, which predates this Fugaro: a policy published at %s is one those runs do not read, so they keep the ceiling from their own environment", pluginwire.Printable(cur), projectpolicy.PathShared()),
		Fix:     "fugaro image refresh moves the jobs to this release's runner image (then fugaro update --repo . in your own terminal, which is the only path that writes a job's environment)"}
}

// adoptOwnFirebaseProject is what every doctor fix whose command is `fugaro
// update --firebase-project` has to say beside it, and it is the migration
// `fugaro update` itself names in the same words: a config that records another
// Firebase project is refused by that command (initFirebase's checkFirebaseProject,
// one Fugaro project has one Firebase project, design D3) before it reads or
// writes anything, so a line that stopped at the command would send exactly the
// installation these checks were written for — the legacy one whose mark and
// rules are the states reported here — to a refusal and no repair. The two
// values to drop are the config's own (budget.firebase_project and the
// budget.rtdb_url beside it, which is that project's database), and with them
// gone the same command adopts the installation's own project.
func adoptOwnFirebaseProject(lc *localcfg.Config) string {
	return fmt.Sprintf(" If the config records another project in budget.firebase_project, that command is refused while it does: drop it and budget.rtdb_url beside it first, and `fugaro update --firebase-project %s` then adopts this installation's own project (which is the case the sentence above is about).", firebaseProjectIDWord(lc.GCPProject))
}

// doctorRulesSkew is the fifth check: the deployed rules are older than the
// marker config/limits/policyRules. It is the state an older CLI's
// `fugaro update --firebase-project` leaves behind by redeploying the pre-policy
// template while the marker stays, and its consequence is that a published
// policy becomes unreadable — a run's token is refused for a node that
// exists, so every budget-on run halts (Task 1.3 step 1's third outcome).
//
// The marker alone cannot tell this state apart from a healthy one: it is
// the marker *and* the deployed document. The document is compared against
// what this generation generates, with internal/infra's own comparison
// (infra.SameJSON, which DB.Plan uses before it decides to write), so an
// installation whose rules were deployed by a CLI of this generation never
// reports a skew.
//
// A warning, not an error: the fix is the owner's `fugaro update` — whose rules
// stage deploys this binary's generation, in the same run, for every state here
// but the hand-edited marker the stage reports instead — and doctor cannot
// perform it. The two states that command does not repair carry their own line
// in the default view (ownFix): the hand-edited marker, whose repair is a
// deploy from a Fugaro that reads generations at all, and a marker above this
// binary's generation, whose repair is the newer binary. lc is what the other
// states' own fixes need too: the one value `--firebase-project` accepts is the
// installation's own project (checkFirebaseProject), so the command a line
// names has to carry it.
//
// The same check reports the marker's other wrong values: a value that is not a
// generation this Fugaro may read at all (not an integer, or the negative one no
// deploy writes — both are Read's MarkerError, and both report its text here)
// and present-and-*below* this generation's, in the two states the two branches
// below separate (below the policy generation, and between it and this
// binary's). An absent marker is not one of them — it is generation 0 without a
// claim, and there is nothing for the rules to be older than; what an absent
// marker does to a node is doctorPolicyChecks's line to draw.
func doctorRulesSkew(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) []doctorCheck {
	skew := rulesSkewOf(ctx, db)
	marker, gen := projectpolicy.PathRulesMarker(), skew.gen
	switch skew.kind {
	case rulesSkewCurrent, rulesSkewAbsentMarker, rulesSkewMarkerUnreadable:
		// Absent is generation 0, the pre-policy rules, under which no policy
		// can have been published (publishing refuses without the marker) —
		// there is nothing for the rules to be older than, and nothing to warn
		// about. Current is the state the check exists to find nothing in. A
		// marker read that failed is a read like any other here: doctor's
		// policy-published line is where a database doctor cannot read is
		// reported, and this check has no more to add to it.
		return nil
	case rulesSkewBadMarker:
		// Two values, one state: a node that is not an integer at all, and one
		// that is a negative integer no deploy writes. Read reads both as a
		// MarkerError (projectpolicy's MarkerHandEdited carries the argument),
		// so the report of a marker's wrong value is one text for both here
		// too. The marker-below branches are the other states, and their
		// sentences are about a generation that is genuinely old — true of a
		// generation below the policy one and of the policy generation under
		// this binary, and not of a hand edit, which would send the operator to
		// redeploy rules that are not what is wrong (the review finding on this
		// change).
		if skew.notInteger {
			// The remedy the other half of this state has is not this one's: a
			// value that is not a number stops `fugaro update --firebase-project`
			// at the READ, before anything is written (infra.DB.Plan reads the
			// marker into an int), so a reader sent there meets "cannot
			// unmarshal string into Go value of type int" and no repair. The
			// node is a person's, so the line is what a person does to it, and
			// both moves end at the command that writes the marker this Fugaro
			// reads.
			return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning", ownFix: true,
				Problem: rulesMarkerBadText(skew.raw),
				Fix: fmt.Sprintf("set %s to the integer generation of the deployed rules by hand, or delete the node: a value that is not a number is a generation no Fugaro wrote, and `fugaro update --firebase-project %s` reads that node before it writes, so it stops on this one and nothing is repaired. With the node deleted the deployed rules read as generation 0, and `fugaro update --firebase-project %s` then deploys this Fugaro's rules and records generation %d (upgrade Fugaro first if the rules deployed here came from a newer release — this template would replace them).",
					marker, firebaseProjectIDWord(lc.GCPProject), firebaseProjectIDWord(lc.GCPProject), projectpolicy.RulesVersion) + adoptOwnFirebaseProject(lc)}}
		}
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning", ownFix: true,
			Problem: rulesMarkerBadText(skew.raw),
			Fix: fmt.Sprintf("re-run `fugaro update --firebase-project %s` with fugaro >= %s in your own terminal. It writes the marker this Fugaro reads, over a document it deploys itself.",
				firebaseProjectIDWord(lc.GCPProject), policySince) + adoptOwnFirebaseProject(lc)}}
	case rulesSkewNewer:
		// A marker above this binary's RulesVersion, and the review of PR
		// #402's point: a newer Fugaro wrote it, so the deployed rules are a
		// generation this binary does not know. The sentence this check used
		// to print — "the deployed rules are newer than this Fugaro, which
		// cannot read a project policy written under them" — is the opposite
		// of what Read does with the marker: a generation at or above this
		// one's reads as deployed (any marker above RulesVersion included), so
		// a run under these rules *does* read the published policy. What is
		// true is that the rules document itself is a shape this binary cannot
		// verify and must not redeploy over, which is why update --firebase-project
		// refuses the state — and the operator who was told the policy is
		// unreadable would look for the wrong thing.
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning", ownFix: true,
			Problem: rulesMarkerNewerText(marker, gen),
			Fix: fmt.Sprintf("upgrade Fugaro (`brew upgrade fugaro`), then `fugaro update` with the newer binary. The deployed rules are a generation this binary does not know, and `fugaro update --firebase-project %s` refuses this state rather than redeploying generation %d over them, so the newer binary is the one that deploys here.",
				firebaseProjectIDWord(lc.GCPProject), projectpolicy.RulesVersion)}}
	case rulesSkewPrePolicy:
		// Present and below the generation that carries the policy nodes is a
		// state of its own, not an absent marker: it records rules that predate
		// the policy nodes, so `fugaro project policy publish` refuses
		// (requirePolicyRules) and no run reads a policy (Read gates on the same
		// generation). Reporting nothing here printed "a policy is published" as
		// info while every run took the legacy path.
		//
		// The body is rulesMarkerPrePolicyText, which is the very sentence the
		// publish refuses with: one text per state, or the refusal and the
		// report contradict each other (the reviews of PR #402 and of this
		// branch).
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
			Problem: rulesMarkerPrePolicyText(gen),
			Fix: fmt.Sprintf("re-run 'fugaro update' with fugaro >= %s in your own terminal: it deploys the rules a policy is read under and records generation %d in the marker (if the rules deployed here came from a newer Fugaro than this one, upgrade instead — this release's template would replace them)",
				policySince, projectpolicy.RulesVersion)}}
	case rulesSkewOlder:
		// The state between the two generations, and the one the bump to
		// generation 2 created: generation 1 carries the policy nodes, so the
		// policy this installation published is read and enforced by every run
		// and a publish is not refused (Read and requirePolicyRules both gate on
		// PolicyRulesVersion), while the deployed document is not the one this
		// binary deploys. Reporting it as the state below is what said "no run
		// reads a published policy" about rules that read one, and sent the
		// operator to redeploy after a policy that already governs (the review
		// finding on P2.1); reporting nothing at all would leave the missing
		// mailbox grant — the state the operator's app meets — unexplained.
		// The fix names what the redeploy adds over the deployed generation, as
		// rulesMarkerOlderText does: a device-generation marker already has the
		// mailbox, and only the coordinator's read grant is new to it.
		adds := "the mailbox, the device binding and the coordinator's read grant"
		if gen >= rulesDeviceGeneration {
			adds = "the coordinator's read grant"
		}
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
			Problem: rulesMarkerOlderText(gen),
			Fix: fmt.Sprintf("re-run 'fugaro update' in your own terminal from a Fugaro that deploys generation %d or newer: it deploys this release's document, which adds %s, and records its generation in the marker (if the rules deployed here came from a newer Fugaro than this one, upgrade instead — this release's template would replace them)",
				projectpolicy.RulesVersion, adds)}}
	case rulesSkewUnreadableRules:
		// A read that failed is not "the rules are current", and it is not the
		// skew either: doctor cannot tell a document it was refused from one
		// that matches what this generation generates, so it says what it
		// knows. Silence here let the policy-published line say that a policy
		// is published while the document that carries it was never read, and
		// said nothing about the read that failed. An unreadable read is a
		// note, never a failure, which is what the rest of the policy checks
		// do with one (doctorPolicyChecks's "the project policy could not be
		// read"): the remedy is a credential, not the owner's rules deploy.
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "info", unread: true,
			Problem: rulesRulesUnreadableText(skew.err, marker),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
	case rulesSkewUngeneratable:
		// The rules this Fugaro would generate are what the comparison needs,
		// and not generating them is not "the rules are current": folding this
		// into the no-skew branch below reported nothing at all, which reads
		// exactly like a check that was made and found no skew (the review
		// finding on this change). It is the unreadable-read case's sibling —
		// doctor cannot tell whether the deployed rules are the generation this
		// Fugaro reads a project policy under — so it is the same info note,
		// naming what failed.
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "info", unread: true,
			Problem: rulesRulesUngeneratableText(skew.err),
			Fix:     fmt.Sprintf("this binary's embedded rules template is broken, so nothing can be said about %s; report it, and check the installation with a release build (fugaro doctor from one)", marker)}}
	}
	// rulesSkewDocument: the marker records this generation and the deployed
	// document is not the one this Fugaro generates.
	return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
		Problem: rulesSkewText(),
		Fix:     fmt.Sprintf("re-run 'fugaro update' with fugaro >= %s in your own terminal: it redeploys the rules the policy is read under (if these rules came from a newer Fugaro than this one, upgrade instead — this template would replace them)", policySince)}}
}

// The sentences below are the states in which a Fugaro must not deploy its own
// rules over the deployed ones, and each is one text for doctor's line and for
// `fugaro update`'s rules stage, which reports the state instead of repairing it
// (init_rules_stage.go): a refusal and a report that describe the same state in
// different words are how the operator ends up looking for the wrong thing
// (the reviews of PR #402).

// rulesMarkerBadText is the marker's wrong value, from the one predicate that
// reads it: a node that is not an integer, and the negative generation no
// deploy writes, are both projectpolicy's MarkerError.
func rulesMarkerBadText(raw string) string {
	return "the deployed rules cannot be told apart: " + (&projectpolicy.MarkerError{Value: raw}).Error()
}

// rulesMarkerNewerText is a marker above this binary's generation: a newer
// Fugaro wrote it, and this binary must not replace rules it cannot verify.
func rulesMarkerNewerText(marker string, gen int) string {
	return fmt.Sprintf("%s records generation %d, above the generation %d this Fugaro's own rules carry: a newer Fugaro deployed them, so a policy published under them is read (a marker at or above this Fugaro's generation reads as deployed) while the rules themselves are a shape this binary cannot verify, and it cannot deploy its own over them",
		marker, gen, projectpolicy.RulesVersion)
}

// rulesRulesUnreadableText is a deployed document doctor could not read: not
// "the rules are current" and not the skew either, since a document refused to
// the reader cannot be told from one that matches.
func rulesRulesUnreadableText(err error, marker string) string {
	return fmt.Sprintf("the deployed rules could not be read (%v), so whether %s is the generation this Fugaro reads a project policy under, and whether the policy it carries is the one deployed, is unknown", err, marker)
}

// rulesRulesUngeneratableText is this binary's own rules failing to generate,
// the unreadable-read case's sibling: the comparison has nothing to compare.
func rulesRulesUngeneratableText(err error) string {
	return fmt.Sprintf("the rules this Fugaro would generate could not be generated (%v), so whether the deployed rules are the generation this Fugaro reads a project policy under, and whether the policy they carry is the one deployed, is unknown", err)
}

// rulesSkewKind is which state the deployed security rules and their generation
// marker are in, as one value. Two callers ask the same question of the same
// two nodes and may not answer it differently: `fugaro doctor`'s
// policy-rules-skew line, which reports the state, and `fugaro update`'s rules
// stage, which deploys this generation over it (init_rules_stage.go). A stage
// with a copy of this classification could deploy over rules the doctor calls
// current, or leave alone the state the doctor sends the owner here to fix.
type rulesSkewKind int

const (
	// rulesSkewCurrent is the marker at this generation with the deployed
	// document equal to the one this Fugaro generates: the state every
	// installation converges to, and the only one with nothing to report and
	// nothing to deploy.
	rulesSkewCurrent rulesSkewKind = iota
	// rulesSkewAbsentMarker is no marker node at all: generation 0 without a
	// claim. It is the rules an installation deployed before this marker
	// existed — and, since no deploy writes the marker without the document,
	// rules that may be any generation — which is why the rules stage deploys
	// over it rather than reading it as "the document is current".
	rulesSkewAbsentMarker
	// rulesSkewMarkerUnreadable is a marker node that could not be read.
	rulesSkewMarkerUnreadable
	// rulesSkewBadMarker is a marker that is not a generation any deploy
	// writes: not an integer at all, or the negative one
	// projectpolicy.MarkerHandEdited names.
	rulesSkewBadMarker
	// rulesSkewNewer is a marker above this binary's generation: a newer
	// Fugaro deployed these rules, so this binary must never deploy its own
	// over them, whatever it would find in the document.
	rulesSkewNewer
	// rulesSkewPrePolicy is a marker below the generation that carries the
	// policy nodes.
	rulesSkewPrePolicy
	// rulesSkewOlder is a marker between the policy generation and this one's.
	rulesSkewOlder
	// rulesSkewDocument is the marker at this generation and a deployed
	// document that is not the one this Fugaro generates (a console hand-edit
	// of /.settings/rules.json, or the redeploy an older CLI did while the
	// marker stayed).
	rulesSkewDocument
	// rulesSkewUnreadableRules is a deployed document that could not be read.
	rulesSkewUnreadableRules
	// rulesSkewUngeneratable is rules.Generate failing: this binary's own
	// rules cannot be produced, so there is nothing to compare the deployed
	// document with.
	rulesSkewUngeneratable
)

// rulesSkew is one classification, with what the sentences about the state
// need: the marker's generation when it is one a deploy writes, its value when
// it is not, and the error of a read or a generation that failed.
type rulesSkew struct {
	kind rulesSkewKind
	gen  int
	raw  string
	// notInteger is rulesSkewBadMarker's other half: the node is not a number
	// at all, where the rest of that kind is the negative integer no deploy
	// writes. The two are one state to projectpolicy.Read (both are its
	// MarkerError, and the report of the wrong value is one text), and two to
	// the operator, because the remedy differs: a negative generation is
	// repaired by the deploy that writes the marker over it, and a value that
	// is not a number stops that deploy at the read (infra.DB.Plan reads the
	// node before it writes), so it has to be put right by hand first.
	notInteger bool
	err        error
}

// rulesSkewOf reads the generation marker and — only where the marker records
// this generation — the deployed document, and classifies the two. The order is
// the one the states are decided in: the marker first, because every state but
// the last is its, and the document only where the marker claims this
// generation, because a document read under a marker that says otherwise is not
// what any caller acts on.
func rulesSkewOf(ctx context.Context, db *rtdb.Client) rulesSkew {
	marker := projectpolicy.PathRulesMarker()
	var raw json.RawMessage
	found, err := db.Get(ctx, marker, &raw)
	if err != nil || !found {
		if err != nil {
			return rulesSkew{kind: rulesSkewMarkerUnreadable, err: err}
		}
		return rulesSkew{kind: rulesSkewAbsentMarker}
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil {
		return rulesSkew{kind: rulesSkewBadMarker, raw: oneLine(string(raw)), notInteger: true}
	}
	if projectpolicy.MarkerHandEdited(gen) {
		return rulesSkew{kind: rulesSkewBadMarker, raw: oneLine(string(raw))}
	}
	switch {
	case gen > projectpolicy.RulesVersion:
		return rulesSkew{kind: rulesSkewNewer, gen: gen}
	case gen < projectpolicy.PolicyRulesVersion:
		return rulesSkew{kind: rulesSkewPrePolicy, gen: gen}
	case gen < projectpolicy.RulesVersion:
		return rulesSkew{kind: rulesSkewOlder, gen: gen}
	}
	live, err := db.Rules(ctx)
	if err != nil {
		return rulesSkew{kind: rulesSkewUnreadableRules, err: err}
	}
	want, err := rulesGenerate()
	if err != nil {
		return rulesSkew{kind: rulesSkewUngeneratable, err: err}
	}
	if infra.SameJSON(live, want) {
		return rulesSkew{kind: rulesSkewCurrent, gen: gen}
	}
	return rulesSkew{kind: rulesSkewDocument, gen: gen}
}

// rulesGenerate is rules.Generate, indirect so a test can drive the branch
// that reads its error: the template is embedded in the binary, so no test can
// break it from the outside, and a failure means this build's own rules cannot
// be produced — which doctor must report rather than read as "no skew" (the
// review finding on this change).
var rulesGenerate = rules.Generate

// emptyPolicyNode reports whether a node read back from the database is JSON
// null or empty: both the fake and the real database report an absent node as
// null, and projectpolicy.Read treats it as "installed, not adopted" — so
// doctor's mention of a node no run reads must not call that one a node.
func emptyPolicyNode(raw []byte) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

// ------------------------------------------------- the credentials cascade

// doctorCredsBroken reports every credential the service has marked broken
// (D139, D141): the token was refused with a 401, and nothing ends that state
// but an operator replacing the token and clearing the mark (C1.21). It is a
// finding because the cascade then routes around the account silently — the
// only other place it shows is `fugaro accounts ls` — and because a run under
// `fallback: oauth-only` halts rather than failing over when the broken account
// is the one it needs.
//
// The subtree is read through creds.RTDBStore.Marks, the same reader `fugaro
// accounts ls` uses: whole, and strictly, so a mark node this release does not
// model fails the read rather than being taken for an available credential. A
// read that failed is an info note, never a failure, which is what the rest of
// doctor's policy checks do with one (the remedy is a credential, not a
// configuration change).
//
// The marks subtree is written by the service alone, and only for the
// credentials the published policy admits, so "every broken mark" is "every
// broken admitted account" without this check resolving the policy itself.
// Severity is warning: nothing is broken about the installation, and something
// is broken about a credential — the same weight policy-rules-skew carries, and
// enough for --strict to fail.
//
// The same read answers the other finding about that subtree, and one read is
// what makes it two answers about one subtree rather than two network calls:
// doctorCredsUnknownState, the marks a newer release (or a hand) wrote in a
// state this one does not read.
func doctorCredsBroken(ctx context.Context, db *rtdb.Client) []doctorCheck {
	marks, err := (&creds.RTDBStore{DB: db}).Marks(ctx)
	if err != nil {
		return []doctorCheck{{ID: "creds-broken", Severity: "info", unread: true,
			Problem: fmt.Sprintf("the credential marks could not be read (%v), so whether any admitted account is broken, or held in a state this Fugaro does not read, is unknown", err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
	}
	var broken []string
	for name, m := range marks {
		if m.Broken() {
			broken = append(broken, name)
		}
	}
	checks := doctorCredsUnknownState(marks)
	if len(broken) == 0 {
		return checks
	}
	slices.Sort(broken)
	// A name is a database key, so it is printed through the one sanitiser
	// every other name from a bucket or a database goes through. The states are
	// this package's words; the names are names, never values (F27).
	for i, n := range broken {
		broken[i] = pluginwire.Printable(n)
	}
	return append(checks, doctorCheck{ID: "creds-broken", Severity: "warning",
		Problem: fmt.Sprintf("%d Claude account(s) are broken — the service saw a 401 on the token, and the cascade routes no work to them until an operator clears the mark: %s (a mark exists only for an account the cascade has selected, so these are accounts an admission let it use; doctor reads the marks, not every repository's admissions)", len(broken), strings.Join(broken, ", ")),
		// The names here are the database's, and a name may come from another
		// checkout of this project's config (claude_accounts is per-checkout
		// and never published): the clear takes any name the database carries
		// a mark for, so this fix is runnable for every name above, and
		// 'fugaro accounts ls' — which lists this repository's own names — is
		// offered as what it is rather than as the way to find them.
		Fix: "replace each account's token, then clear its mark: 'fugaro accounts clear NAME' (the clear takes any name the database carries a mark for; 'fugaro accounts ls' shows the state of the accounts this repository may use)"})
}

// doctorCredsUnknownState reports every mark whose state this release does not
// read (creds.Mark.Known): a node a newer release wrote, or one written by
// hand. Such a mark is a hold — Usable refuses a state it cannot read, which is
// the fail-closed direction, and the writers refuse to replace it with a hold
// of their own (creds.Exhaust, creds.ProbeFailed) — so the cascade routes no
// work to that credential and `fugaro accounts clear` refuses it as well, which
// means nothing an operator can run on THIS release takes the hold off.
//
// That is why it is a finding, and why it is doctor's: the state is visible in
// `fugaro accounts ls` (passed through verbatim rather than rounded to
// available) and nowhere else, and doctor said nothing about it at all. Warning
// rather than info, for the reason creds-broken is a warning: a credential of
// this installation is held, and --strict failing on it is the fail-closed
// direction. The remedy is not a configuration change, so it is not an error
// about the installation either.
//
// The state word is the node's own text — a value a newer release wrote — and
// it is printed through the same sanitiser a name is: doctor's problem and fix
// are rendered verbatim by the console.
func doctorCredsUnknownState(marks map[string]creds.Mark) []doctorCheck {
	var names []string
	state := map[string]string{}
	for name, m := range marks {
		if m.Known() {
			continue
		}
		names = append(names, name)
		state[name] = string(m.State)
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	held := make([]string, 0, len(names))
	for _, n := range names {
		held = append(held, fmt.Sprintf("%s (%s)", pluginwire.Printable(n), pluginwire.Printable(state[n])))
	}
	return []doctorCheck{{ID: "creds-unknown-state", Severity: "warning",
		Problem: fmt.Sprintf("%d credential mark(s) carry a state this Fugaro does not read, so the cascade routes no work to them and nothing this release offers clears them: %s. A newer release wrote them, or they were written by hand; this one refuses to read such a state as 'no hold', which is why they are held rather than available", len(held), strings.Join(held, ", ")),
		Fix:     "upgrade Fugaro to the release that wrote the state (brew upgrade dimipaun/tap/fugaro), which is what knows how to clear it; 'fugaro accounts ls' prints each mark's state word as it is stored"}}
}

// doctorCredsRules reads the credentials family's marker beside the policy
// marker (budget/rules.PathCredsRulesMarker, C1.3). The family ships in the
// same generated document as everything else and deliberately does not bump
// projectpolicy.RulesVersion, so `policy-rules-skew` cannot see it: an
// installation whose rules were deployed before this family exists has a marker
// at this Fugaro's own RulesVersion and a document without the /creds deny and
// without the registry entry's `credential` and `waiting_until` children. A
// cascade run then passes every gate and halts on its first write
// (HaltCredsRulesDenied, C1.17) — the named halt the marker doc's known gap
// describes, and the reason a current binary reads this marker beside the other
// one: so it can tell "an older deploy" from "a newer deploy" and say which way
// the redeploy should go.
//
// The switch is what makes it a finding. An installation that has not turned
// the cascade on needs no marker, and the plan's sentence is exactly that: the
// service on without the credsRules marker. "The service on" is read from the
// published policy's routing.claude.via, which is the switch a run reads
// (projectpolicy.RouteCascade, D150): C1.18's Terraform variable is not a fact
// this command can read, and the policy is the one the runs themselves obey.
func doctorCredsRules(ctx context.Context, db *rtdb.Client, p *projectpolicy.Policy, lc *localcfg.Config) []doctorCheck {
	if !cascadeSwitchOn(p) {
		return nil
	}
	const id = "creds-rules"
	marker := rules.PathCredsRulesMarker()
	var raw json.RawMessage
	found, err := db.Get(ctx, marker, &raw)
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("whether the deployed rules carry the credentials family is unknown: %s could not be read (%v)", marker, err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
	}
	via := fmt.Sprintf("routing.claude.via: %s at %s", projectpolicy.RouteCascade, projectpolicy.PathShared())
	if !found {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the cascade is switched on (%s) and %s is not written: the deployed rules predate the credentials family, so the first credential a cascade run records is refused and the run halts instead of running (fugaro accounts and fugaro watch show no state on such an installation)", via, marker),
			Fix:     fmt.Sprintf("re-run `fugaro update --firebase-project %s` in your own terminal from a Fugaro that carries the credentials rules — this one does. Do not run it from an older Fugaro: that binary deploys a document without the family and leaves this switch on", firebaseProjectIDWord(lc.GCPProject)) + adoptOwnFirebaseProject(lc)}}
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil || projectpolicy.MarkerHandEdited(gen) {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s cannot be told apart: %s", marker, oneLine(string(raw))),
			Fix:     fmt.Sprintf("re-run `fugaro update --firebase-project %s` in your own terminal from a Fugaro that carries the credentials rules: it records the generation this Fugaro reads, over a document it deploys itself", firebaseProjectIDWord(lc.GCPProject)) + adoptOwnFirebaseProject(lc)}}
	}
	if gen > rules.CredsRulesVersion {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s records generation %d, above the generation %d this Fugaro's credentials rules carry: a newer Fugaro deployed them, so the deployed document is not the one this binary generates and a cascade run's writes are the newer release's to define", marker, gen, rules.CredsRulesVersion),
			Fix:     "upgrade Fugaro (brew upgrade dimipaun/tap/fugaro) and re-run init with the newer binary; this release refuses to deploy its own rules over a newer marker"}}
	}
	if gen < rules.CredsRulesVersion {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s records generation %d, below the generation %d this Fugaro's credentials rules carry: the deployed document predates the credentials family, so a cascade run's first recorded credential is refused and the run halts", marker, gen, rules.CredsRulesVersion),
			Fix:     fmt.Sprintf("re-run `fugaro update --firebase-project %s` in your own terminal from this Fugaro: it deploys the document the family ships in and records the generation", firebaseProjectIDWord(lc.GCPProject)) + adoptOwnFirebaseProject(lc)}}
	}
	return nil
}

// cascadeSwitchOn reports whether the published policy switches the cascade on
// (projectpolicy.RouteCascade): the policy is in force here — doctorCredsRules
// is only called on a node projectpolicy.Read returned — and a node with no
// routing block, or one whose Claude switch is not cascade, leaves the cascade
// off (D1b: an absent switch is direct).
func cascadeSwitchOn(p *projectpolicy.Policy) bool {
	if p == nil || p.Routing == nil || p.Routing.Claude == nil {
		return false
	}
	return p.Routing.Claude.ViaValue() == projectpolicy.RouteCascade
}
