package runner

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// CheckoutEnv is the job environment variable internal/infra sets to the
// workflow's own config.Checkout value (clone or shared) on a workflow whose
// image holds no repository's source, and to nothing on a baked one (design
// generic-tool.md §2, G3/G4): it tells the runner's bootstrap that there is
// no checkout baked into the image, so the run makes its own blobless clone
// and installs the repository's mise tools.
//
// It is named here, in the runner, because the runner is what reads it;
// internal/infra (spec.go) sets it from this constant, the way it takes
// runner.BudgetModeEnv and the rest.
const CheckoutEnv = "FUGARO_CHECKOUT"

// RemoteEnv is the job environment variable internal/infra sets to the
// repository's credential-free clone URL on a checkout: clone or checkout:
// shared workflow's job: the image has no checkout baked into it, so
// bootstrap has to clone one, and this says from where. It is what fugaro
// exec's --remote flag defaults to (internal/cli/exec.go), so the repository
// reaches the runner the way every other job setting does, from the job's own
// environment — without it a clone job on Cloud Run has no remote at all and
// fails bootstrap with "this job has no repository to clone".
//
// Named here, in the runner, for the same reason CheckoutEnv is: the runner
// is what reads it (Deps.Remote), and internal/infra sets it from this
// constant. It carries no credentials (no userinfo, https only:
// internal/infra's repoURL checks both); the token travels separately, as
// the credential env cloneVars builds.
//
// **A project job does not read it at all** (plan task 3.3 step 3, design
// §4.1(1)): the remote a project job clones from is computed from task.json's
// repository, the provider the job's own name pins and the project's policy
// (internal/runner's cloneURL), because a shared job serves every repository
// of its profile and its environment is a launcher's to override. The constant
// stays for the per-repository path, which is every repository until phase 4
// migrates it (D3); Task 4.4 drops it.
const RemoteEnv = "FUGARO_REMOTE"

// provisionStage is the name provisioning's own time is recorded under: the
// stage timing a checkout: clone run pays before its first agent stage
// (design generic-tool.md §2, G4), the way every agent stage records its
// own (run.stageFinished).
const provisionStage = "provision"

// provisionTimeout bounds mise install. The design measures it as start-up
// cost (Check 38); Node is about 30 MB and Temurin about 200 MB, and a cold
// cache means downloading them, so the bound is generous but finite: a
// stalled download must not hold the run until its total timeout.
const provisionTimeout = 10 * time.Minute

// cloneJob reports whether this run's job has no checkout baked into its
// image. The job's own environment decides, never the checkout's fugaro.yaml:
// before the clone there is no checkout to read one from, and the job is what
// the run actually executes in.
//
// A project job is always such a job, and says so by being one: its image is
// the installation's base image (internal/infra's ProjectJob.Image is
// lc.BaseImage(config.BaseKind)), which bakes no checkout, and projectJobEnv
// carries none of the per-repository variables — FUGARO_CHECKOUT, the only
// marker, is written by internal/infra's spec.go for a per-repository clone
// or shared workflow alone. A shared job is one of these too: its image is
// the dependency layer, which holds no checkout either (design
// forge-authorship-images/shared-images.md §1), so it stands and falls with
// the clone. Its environment is also a launcher's to override
// (run.jobs.runWithOverrides), so reading the marker there would let a launch
// decide whether the run clones at all; the design keeps that class of fact
// computed from the job rather than read from the environment, as the provider
// and the clone URL already are (project-centric.md §12a items 1 and 2).
//
// Its four call sites depend on it, and each does the baked job's thing without
// it: openCheckout clones whole through gitops.OpenOrClone instead of blobless
// (gitops.CloneBlobless); provision installs nothing (images/base's
// mise-system.toml sets auto_install = false, so a tool its mise.toml names
// then fails loudly rather than installing); bootstrap's review branch leaves
// the work tree on the remote's default branch instead of checking out
// origin/<spec.Ref>; and prefetchObjects skips its fill, leaving a review-mode
// project job's agent to fetch the branch's revisions and the base tip its
// reads want with git credentials it does not have.
func (r *run) cloneJob() bool {
	if _, project := r.projectJob(); project {
		return true
	}
	switch r.jobEnv(CheckoutEnv) {
	case config.CheckoutClone, config.CheckoutShared:
		return true
	}
	return false
}

// jobEnv reads key from the runner's environment; a nil Deps.Env means "the
// process's own", the same meaning procgroup.Cmd.Env gives nil.
func (r *run) jobEnv(key string) string {
	if r.d.Env != nil {
		return envLookup(r.d.Env, key)
	}
	return os.Getenv(key)
}

// provision installs the checkout's mise tools, for a run whose job has no
// derived image (design generic-tool.md §2, G4): nothing was installed for
// it at build time, so the run pays `mise install` itself, before its first
// stage, and its time is recorded as the provision stage. It does nothing
// for a baked job (the image already installed the repository's root mise
// config) and nothing when the checkout has no root mise config at all.
//
// A failure ends bootstrap as infra_error, with mise's last line as the
// run's reason: the run cannot do its work without the tools its own
// repository declares.
func (r *run) provision(ctx context.Context) error {
	if !r.cloneJob() {
		return nil
	}
	configs, err := config.MiseConfigFiles(r.d.WorkDir)
	if err != nil {
		return fmt.Errorf("reading the checkout's mise config: %w", err)
	}
	if len(configs) == 0 {
		return nil
	}
	started := r.d.Now()
	err = r.miseInstall(ctx, configs)
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{
		Name: provisionStage, StartedAt: started.UTC(), DurationS: r.d.Now().Sub(started).Seconds(),
	})
	r.save(ctx)
	return err
}

// provisionGitToken is the GITHUB_TOKEN provision hands mise: the run's git
// token, which is what mise's GitHub API calls use for their rate limit —
// or "" when it must not travel at all. Two cases, both credential
// boundaries rather than conveniences:
//
//   - A follow-up (review H1). It checks out the pull request's branch,
//     written by an earlier agent that never held the push token
//     (agent/env.go's allowlist), and then provision would run that branch's
//     own mise config. A mise config executes code: [env] with {{exec(...)}}
//     templates, [hooks], and backend plugins fetched from any git URL. So a
//     branch could exfiltrate the token that pushes it, and the boundary
//     "an agent never holds the push token" would be gone. A follow-up's
//     mise installs without it. (checkPullRequest also runs before
//     provision, never after: nothing from the branch runs at all until the
//     pull request itself has been checked.)
//   - A provider other than GitHub (review M1): a Bitbucket access token
//     exported as GITHUB_TOKEN would be sent to api.github.com and to every
//     GitHub-backed plugin host, which is a credential handed to the wrong
//     third party.
//
// A first run's ref is the user's own choice and git.provider is GitHub, so
// there the token travels, and it is already registered for redaction
// (r.addSecret), so nothing mise echoes can publish it.
func (r *run) provisionGitToken() string {
	if r.follow != nil || r.providerKind != gitprov.KindGitHub {
		return ""
	}
	return r.auth.Token
}

// miseTimeout is how long mise install may take: the run's own bound
// (Deps.ProvisionTimeout), or provisionTimeout by default.
func (r *run) miseTimeout() time.Duration {
	if r.d.ProvisionTimeout > 0 {
		return r.d.ProvisionTimeout
	}
	return provisionTimeout
}

// miseInstall runs `mise install` in the checkout, bounded by miseTimeout,
// checks that nothing the config declares is still missing, and reports
// either failure as an error naming mise's last output line. Its environment
// is the runner's with the model credentials (and the gateway's provider
// keys) removed — a tool's own install script is repository-chosen code (a
// mise backend such as cargo: or go: runs one), and must never see either —
// and with GITHUB_TOKEN set only when provisionGitToken allows it.
func (r *run) miseInstall(ctx context.Context, configs []string) error {
	bound := r.miseTimeout()
	mctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	env := provisionEnv(r.d.Env, r.provisionGitToken())
	// Two buffers, never one: procgroup copies each pipe in its own
	// goroutine, so one writer shared by both would be a data race.
	var stdout, stderr strings.Builder
	code, runErr := procgroup.Run(mctx, procgroup.Cmd{
		Name:   "mise",
		Args:   []string{"install"},
		Dir:    r.d.WorkDir,
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	what := strings.Join(configs, " ")
	// mise reports what it could not install on stderr; stdout is the
	// fallback for a mise that says nothing there.
	out := stderr.String()
	if strings.TrimSpace(out) == "" {
		out = stdout.String()
	}
	switch {
	case ctx.Err() != nil:
		// The run's own context ended (a cancel, or the run's total
		// timeout) while mise was running: that is the run's business, not
		// provision's, and it must be reported as such — a bare "did not
		// finish within 10m0s" would blame the wrong clock, and a killed
		// mise that happened to exit 0 must not read as success.
		return fmt.Errorf("mise install for %s: %w: %s", what, ctx.Err(), lastLine(out))
	case runErr != nil && mctx.Err() == nil:
		// procgroup.Run reports a command killed by ctx as ctx.Err() and
		// nothing else.
		return fmt.Errorf("running mise install for %s: %w", what, runErr)
	case mctx.Err() != nil:
		// The bound fired, not the run's own context (checked above):
		// procgroup.Run reports the kill as mctx.Err(), the parent context
		// is still live, and mise's last line is what says where it hung.
		return fmt.Errorf("mise install for %s did not finish within %s: %s", what, bound, lastLine(out))
	case code != 0:
		return fmt.Errorf("mise install failed (exit %d) for %s: %s", code, what, lastLine(out))
	}
	return r.miseComplete(ctx, bound, env, configs)
}

// miseComplete checks `mise ls --current --missing` after a successful
// install and fails the same way the derived image's build does
// (images/derived/Dockerfile.tmpl): a config whose tool is not installed
// after mise install exited 0 (a tool the config's own version never
// resolves, a partial download mise tolerated) would otherwise surface much
// later, as a stage failing on a missing binary, with auto_install off.
func (r *run) miseComplete(ctx context.Context, bound time.Duration, env []string, configs []string) error {
	mctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	var stdout, stderr strings.Builder
	// `mise ls --current --missing` names what is missing on stdout and
	// exits 0 (the derived image's build reads it exactly this way), so its
	// stdout, not its status, says whether a tool is missing — but a
	// non-zero status with nothing on stdout (a config mise cannot parse,
	// say) is a broken check, not a clean bill of health, and the derived
	// image's own `missing="$(mise ls ...)" && ...` fails on exactly that.
	code, runErr := procgroup.Run(mctx, procgroup.Cmd{
		Name:   "mise",
		Args:   []string{"ls", "--current", "--missing"},
		Dir:    r.d.WorkDir,
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	out := stderr.String()
	if strings.TrimSpace(out) == "" {
		out = stdout.String()
	}
	what := strings.Join(configs, " ")
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("checking the mise install for %s: %w: %s", what, ctx.Err(), lastLine(out))
	case runErr != nil && mctx.Err() == nil:
		return fmt.Errorf("checking the mise install for %s: %w", what, runErr)
	case mctx.Err() != nil:
		return fmt.Errorf("checking the mise install for %s did not finish within %s: %s", what, bound, lastLine(out))
	case code != 0:
		return fmt.Errorf("checking the mise install for %s failed (exit %d): %s", what, code, lastLine(out))
	}
	missing := strings.TrimSpace(stdout.String())
	if missing == "" {
		return nil
	}
	// mise names each missing tool on its own line; keep the report short
	// and one-lined, the way every other reason is.
	return fmt.Errorf("mise installed %s but left these tools missing: %s", what, lastLine(missing))
}

// provisionEnv is env with everything Fugaro's own and everything secret
// removed, and GITHUB_TOKEN set to token (the run's git token, for mise's
// rate limit; left unset when the run has none, so an empty variable never
// shadows one the job's own environment carries).
//
// mise install is repository-chosen code: a mise backend such as `cargo:` or
// `go:` runs the tool's install script, a `[hooks]` entry and an `[env]`
// template run whatever the config says, and a plugin is fetched from any
// git URL. On a follow-up the config that runs is the pull request branch's
// own — written by an agent — so what mise may see is a credential boundary,
// not a convenience. Removed, therefore:
//
//   - every FUGARO_* variable, by prefix rather than by list: they are
//     Fugaro's own protocol and credentials, mise reads none of them, and a
//     prefix rule is fail-closed — a credential added later cannot be
//     forgotten here. This is what covers the static git-provider
//     credentials specifically: providers.EnvBitbucketToken (a repository
//     access token), EnvGitHubAppKey/EnvGitHubAppKeyFile/EnvGitHubAppID
//     (from that private key an installation token for every repository the
//     App covers can be minted), and the derived FUGARO_GIT_TOKEN. The
//     boundary H1 and M1 draw is only a boundary if the key it comes from
//     goes too. Withholding the GITHUB_TOKEN minted
//     from the App key while handing over the key itself would be no
//     boundary at all.
//   - the model credentials (modelCredentialVars) and the gateway's provider
//     keys (config.IsProviderKeyEnv, which is absent from the agent's own
//     allowlist for the same reason).
//   - GITHUB_TOKEN and GH_TOKEN, the names mise or a git it shells out to
//     would send to api.github.com: readded below only when the caller
//     allows one.
//
// What is kept is everything else the job carries — PATH, HOME, the proxy
// and CA-certificate variables, TMPDIR, MISE_* and whatever a repository's
// own Dockerfile set — because a mise backend may need them and the agent's
// allowlist (agent.baseEnv) does not carry them all. A denylist is weaker
// than an allowlist here, which is why the FUGARO_-prefix rule above is what
// the credential names are derived from rather than enumerated.
func provisionEnv(env []string, token string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "FUGARO_") || k == "GITHUB_TOKEN" || k == "GH_TOKEN" ||
			config.IsProviderKeyEnv(k) || slices.Contains(modelCredentialVars, k) {
			continue
		}
		out = append(out, kv)
	}
	if token != "" {
		out = append(out, "GITHUB_TOKEN="+token)
	}
	return out
}

// lastLine is the last non-empty line of out, one-lined and clipped: what a
// failed command is reported with. The caller redacts it.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return logtail.Clip(safetext.OneLine(l), logtail.DefaultLineBytes)
		}
	}
	return "mise said nothing"
}
