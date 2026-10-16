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
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
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
}

func fromPreflight(c preflight.Check) doctorCheck {
	return doctorCheck{ID: c.ID, OK: c.OK, Problem: c.Problem, Fix: c.Fix}
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
		Fix:     "to narrow it, list the repositories in that provider's allow_data_to and run " + selfCommand() + " init --repo in each one (a deployed job keeps the key's mount and the routes it was applied with until then)"}
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
		// --plugin is documented offline and safe for CI: no credentials, no
		// network call of any kind. cli-version would need one, so it is
		// skipped here; the full run below always includes it.
		o.OK = !checksFail(o.Checks, strict)
		return emitDoctor(cmd, o, asJSON)
	}
	o.Checks = append(o.Checks, cliVersionCheck(ctx, Version, time.Now()))

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
	// Both sides' checks, in the order main has them: the catalog's age (P7),
	// the ZDR note (P9), and the project-wide provider secrets' own checks.
	o.Checks = append(o.Checks, doctorPriceCatalog(budgetNow()))
	if c := doctorProviderZDR(lc); c != nil {
		o.Checks = append(o.Checks, *c)
	}
	if c := doctorOpenProvidersCheck(lc); c != nil {
		o.Checks = append(o.Checks, *c)
	}
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
	o.Checks = append(o.Checks, doctorRunsBucketIAM(ctx, lc, crmSvc)...)
	o.Checks = append(o.Checks, doctorPolicyChecks(ctx, lc)...)

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

	o.Project = &doctorProject{Name: lc.Name, GCPProject: lc.GCPProject, Region: lc.Region, RegistryHost: lc.RegistryHost, BaseImages: lc.BaseImages}

	if co, _ := checkoutProject(ctx, ""); co != nil {
		var resolved *resolvedFile
		if c, fy, rf := fugaroYAMLCheck(ctx, co.Root, lc); c != nil {
			o.Checks = append(o.Checks, *c)
			o.FugaroYAML = fy
			if fy.Valid && rf != nil {
				o.Checks = append(o.Checks, doctorLayerChecks(ctx, lc, co.Root, *rf)...)
				resolved = rf
			}
		}
		o.Secrets = doctorSecrets(cmd, lc)
		token := oauthTokenOf(resolved)
		if repo, err := originRepo(ctx); err == nil {
			if c := claudeAccountsStoredCheck(lc, repo, o.Secrets, projSecrets, projOK); c != nil {
				o.Checks = append(o.Checks, *c)
			}
			// The two per-repository project-secret checks need the
			// repository's own resolved fugaro.yaml (which logical names its
			// jobs mount): an invalid one already failed its own check, so
			// there is nothing to compute from.
			if resolved != nil && resolved.Cfg != nil {
				if facts, ok := doctorRepoSecretsOf(lc, resolved.Cfg, repo); ok {
					o.Checks = append(o.Checks, doctorProjectSecretStoredChecks(facts, o.Secrets, projSecrets, projOK)...)
					if c := doctorProjectSecretScopeCheck(ctx, lc, facts, o.Secrets, projSecrets); c != nil {
						o.Checks = append(o.Checks, *c)
					}
				}
			}
			if c := oauthTokenAllowedCheck(lc, repo, token, authOf(resolved)); c != nil {
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
	return emitDoctor(cmd, o, asJSON)
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
	problem := "cosign is not on PATH (looked for " + basepin.CosignBin + "): a base image is pinned at the digest its cosign signature was verified for, so fugaro init --base and fugaro image refresh refuse without it"
	fix := "install cosign (https://docs.sigstore.dev/cosign/installation/), or name a base image explicitly with --base-image KIND=IMAGE"
	if lc.BaseImage(config.BaseKind) == "" {
		problem = "no Fugaro base is recorded, and cosign is not on PATH (looked for " + basepin.CosignBin + "): recording one with fugaro image refresh needs it"
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
		logical := "github-app-key"
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
				Fix: "run " + selfCommand() + " init --repo " + quoteWord(repo) + " in its checkout to reapply it, so the App's key this check reads and the one its builds read are the same secret"})
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
func doctorRepoSecretsOf(lc *localcfg.Config, cfg *config.Config, repo string) (doctorRepoSecrets, bool) {
	facts := doctorRepoSecrets{repo: repo,
		candidates: map[string]string{}, logical: map[string]string{}, repoIDs: map[string]string{}, jobs: map[string]string{}}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		ws, err := infra.Workflow(infra.Inputs{LC: lc, Repo: repo, Cfg: cfg}, name)
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
	// prompt, init --repo's missing list and validate's note do.
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
// A warning (design §7's own reasoning): the next fugaro init --repo fixes
// it, and a repository mid-move is the expected, transient case. Best
// effort: a job that could not be read, a name that resolves to nothing, or
// no Cloud Run endpoint at all says nothing.
//
// Only each workflow's default-account job is read, the same limitation
// jobConfigCopyNote documents (internal/cli/upgrade_cloud.go): the one
// fugaro init --repo that refreshes the default job refreshes every named
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
		Fix:     "run " + selfCommand() + " init --repo " + quoteWord(facts.repo) + " in your own terminal to reapply the repository, so the next run mounts the copy Fugaro resolves"}
}

// doctorProjectSecretAccessorChecks is doctor's project-wide
// "project-secret-extra-accessors" check (S3): for each project-wide
// container, every live secretAccessor grant that belongs to one of the
// installation's own service accounts, whose repository the name's own
// eligibility rule no longer lists. Removing a repository from
// project_secrets, from a provider's allow_data_to or from an account's
// leaves its job's grant live until that one repository's own apply runs
// again, and no repository's own fugaro init --repo has reason to look at
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
			cmds[i] = "re-run " + selfCommand() + " init --repo " + quoteWord(repo) + " and pass --allow-delete"
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
func doctorPolicyChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	shared := projectpolicy.PathShared()
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		// The state of every installation that has not run init --firebase:
		// there is no node to read, and the runs take their ceiling from the
		// job environment, exactly as they did before this model (P3).
		return []doctorCheck{{ID: "policy-published", Severity: "info",
			Problem: fmt.Sprintf("project %s has no Firebase budget backend, so it cannot publish a project policy: runs take their ceiling from the job environment", lc.Name),
			Fix:     "fugaro init --firebase <firebase-project-id> --name " + lc.Name + " adds the backend the policy is published to"}}
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return []doctorCheck{{ID: "policy-published", Severity: "info",
			Problem: fmt.Sprintf("whether project %s publishes a policy could not be checked: %v", lc.Name, err),
			Fix:     "fix budget.rtdb_url in the project's config, then run doctor again"}}
	}
	checks := doctorRulesSkew(ctx, db)
	// The node is read through projectpolicy.Read, the reader a run uses
	// (design §5(3)), not with a plain Get: with no marker, or one below
	// RulesVersion, no run reads the policy however the node is written, and a
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
		return checks
	case errors.As(err, &ve):
		// P8's release skew, both directions: Decode refuses a node of any other
		// version, and the two remedies are opposite. A newer node needs a newer
		// Fugaro. An older one — a hand-written node, or one a release before
		// this shape wrote — is the version *this* binary writes, so "upgrade
		// Fugaro" is backwards there and the remedy is the one the publisher
		// itself words (publishedPolicyOf): remove it, then publish.
		if ve.Found > ve.Understood {
			return append(checks, doctorCheck{ID: "policy-newer-than-binary", Severity: "warning",
				Problem: fmt.Sprintf("%s: %s", fmt.Sprintf(projectpolicy.CLISkew, ve.Found, ve.Understood), shared),
				Fix:     fmt.Sprintf("upgrade Fugaro to a release that understands version %d", ve.Found)})
		}
		return append(checks, doctorCheck{ID: "policy-older-than-binary", Severity: "warning",
			Problem: fmt.Sprintf("%s holds a policy of version %d, and this Fugaro writes version %d: no run applies it, and publishing over it is refused", shared, ve.Found, ve.Understood),
			Fix:     fmt.Sprintf("fugaro project policy publish --delete removes it, then 'fugaro project policy publish' writes this Fugaro's version %d", ve.Understood)})
	case err != nil:
		// A read that failed is not "no policy": doctor says which it is.
		return append(checks, doctorCheck{ID: "policy-published", Severity: "info",
			Problem: fmt.Sprintf("the project policy could not be read (%v), so whether one is published, and whether the runs are bounded by it, is unknown", err),
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"})
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
			info.Fix = "re-run 'fugaro init --firebase' with fugaro >= " + policySince + " in your own terminal to deploy the rules a policy is read under, then 'fugaro project policy publish'"
		}
		return append(checks, info)
	}
	// found, and Read returned no error: the node is in force, so the checks
	// below can read it — Decode's error would have arrived as err above.
	checks = append(checks, doctorCheck{ID: "policy-published", Severity: "info",
		Problem: fmt.Sprintf("a policy is published at %s (version %d, %d allow-list entries)", shared, p.Version, len(p.AllowRepos)),
		Fix:     "fugaro project policy show prints it"})
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
	if c := doctorPolicyVsJobs(lc); c != nil {
		checks = append(checks, *c)
	}
	return checks
}

// doctorAllowListUncovered is the other half of allow-list-empty, and the
// state un-enrolling a repository (`fugaro project repos rm`) leaves behind:
// the policy admits repositories, just not one this machine's config names,
// so that repository's runs are refused at admission — before any git
// credential exists — and the machine that launches them is where the
// operator finds out first.
//
// The names come from the machine's own `repos:` map, the one `init --repo`
// fills, converted with Canonical exactly as the publish seed converts them
// (project_cmd.go's seedAllowRepos), so the two cannot spell a repository
// differently. A repository with no provider recorded is skipped rather than
// guessed: the checkout's fugaro.yaml decides it, and doctor does not read a
// repository's file to answer a question about the project's policy.
func doctorAllowListUncovered(lc *localcfg.Config, p *projectpolicy.Policy) *doctorCheck {
	var refused []string
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider == "" {
			continue
		}
		c, err := projectpolicy.Canonical(r.Provider, name)
		if err != nil {
			continue
		}
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
		Fix:     "fugaro image refresh moves the jobs to this release's runner image (then fugaro init --repo . in your own terminal, which is the only path that writes a job's environment)"}
}

// doctorRulesSkew is the fifth check: the deployed rules are older than the
// marker config/limits/policyRules. It is the state an older CLI's
// `fugaro init --firebase` leaves behind by redeploying the pre-policy
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
// A warning, not an error: the fix is the owner's `fugaro init --firebase`
// and doctor cannot perform it.
//
// The same check reports the marker's other wrong value: present and *below*
// this generation's (see the gen < RulesVersion branch). An absent marker is
// not one of them — it is generation 0 without a claim, and there is nothing
// for the rules to be older than; what an absent marker does to a node is
// doctorPolicyChecks's line to draw.
func doctorRulesSkew(ctx context.Context, db *rtdb.Client) []doctorCheck {
	marker := projectpolicy.PathRulesMarker()
	var raw json.RawMessage
	found, err := db.Get(ctx, marker, &raw)
	if err != nil || !found {
		// Unreadable, or absent: absent is generation 0, the pre-policy rules,
		// under which no policy can have been published (publishing refuses
		// without the marker) — there is nothing for the rules to be older
		// than, and nothing to warn about.
		return nil
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil {
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
			Problem: "the deployed rules cannot be told apart: " + (&projectpolicy.MarkerError{Value: oneLine(string(raw))}).Error(),
			Fix:     "re-run 'fugaro init --firebase' with fugaro >= " + policySince + " in your own terminal, which writes the marker this Fugaro reads"}}
	}
	if gen > projectpolicy.RulesVersion {
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
			Problem: fmt.Sprintf("%s records generation %d, and this Fugaro's rules are generation %d: the deployed rules are newer than this Fugaro, which cannot read a project policy written under them",
				marker, gen, projectpolicy.RulesVersion),
			Fix: fmt.Sprintf("upgrade Fugaro first: 'fugaro init --firebase' with this binary refuses this state rather than redeploying generation %d over the deployed rules, so the newer binary is the one that deploys here ('fugaro upgrade')",
				projectpolicy.RulesVersion)}}
	}
	if gen < projectpolicy.RulesVersion {
		// Present and below this generation's is a state of its own, not an
		// absent marker: it records rules that predate the policy nodes, so
		// `fugaro project policy publish` refuses (requirePolicyRules) and no
		// run reads a policy (Read gates on the marker alone). Reporting
		// nothing here printed "a policy is published" as info while every run
		// took the legacy path.
		//
		// The body cannot be rulesSkewText, which is the *other* state's
		// sentence: it says the deployed rules are older than the marker, and
		// here it is the marker that is below (the review of PR #402).
		return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
			Problem: fmt.Sprintf("%s records generation %d, below the generation %d this Fugaro reads a project policy under: this installation's rules are the pre-policy ones, so no run reads a published policy and 'fugaro project policy publish' refuses until the marker is brought up",
				marker, gen, projectpolicy.RulesVersion),
			Fix: fmt.Sprintf("re-run 'fugaro init --firebase' with fugaro >= %s in your own terminal: it deploys the rules a policy is read under and records generation %d in the marker (if the rules deployed here came from a newer Fugaro than this one, upgrade instead — this release's template would replace them)",
				policySince, projectpolicy.RulesVersion)}}
	}
	live, err := db.Rules(ctx)
	if err != nil {
		return nil
	}
	want, err := rules.Generate()
	if err != nil || infra.SameJSON(live, want) {
		return nil
	}
	return []doctorCheck{{ID: "policy-rules-skew", Severity: "warning",
		Problem: rulesSkewText(),
		Fix:     fmt.Sprintf("re-run 'fugaro init --firebase' with fugaro >= %s in your own terminal: it redeploys the rules the policy is read under (if these rules came from a newer Fugaro than this one, upgrade instead — this template would replace them)", policySince)}}
}

// emptyPolicyNode reports whether a node read back from the database is JSON
// null or empty: both the fake and the real database report an absent node as
// null, and projectpolicy.Read treats it as "installed, not adopted" — so
// doctor's mention of a node no run reads must not call that one a node.
func emptyPolicyNode(raw []byte) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}
