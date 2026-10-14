package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// CheckoutEnv is the job environment variable internal/infra sets to
// config.CheckoutClone on a checkout: clone workflow's job and to nothing on
// a baked one (design generic-tool.md §2, G3/G4): it tells the runner's
// bootstrap that there is no checkout baked into the image, so the run makes
// its own blobless clone and installs the repository's mise tools.
//
// It is named here, in the runner, because the runner is what reads it;
// internal/infra (spec.go) sets it from this constant, the way it takes
// runner.BudgetModeEnv and the rest.
const CheckoutEnv = "FUGARO_CHECKOUT"

// RemoteEnv is the job environment variable internal/infra sets to the
// repository's credential-free clone URL on a checkout: clone workflow's
// job: the image has no checkout baked into it, so bootstrap has to clone
// one, and this says from where. It is what fugaro exec's --remote flag
// defaults to (internal/cli/exec.go), so the repository reaches the runner
// the way every other job setting does, from the job's own environment —
// without it a clone job on Cloud Run has no remote at all and fails
// bootstrap with "this job has no repository to clone".
//
// Named here, in the runner, for the same reason CheckoutEnv is: the runner
// is what reads it (Deps.Remote), and internal/infra sets it from this
// constant. It carries no credentials (no userinfo, https only:
// internal/infra's repoURL checks both); the token travels separately, as
// the credential env cloneVars builds.
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
// image (FUGARO_CHECKOUT=clone, internal/infra). The job's own environment
// decides, never the checkout's fugaro.yaml: before the clone there is no
// checkout to read one from, and the job is what the run actually executes
// in.
func (r *run) cloneJob() bool { return r.jobEnv(CheckoutEnv) == config.CheckoutClone }

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
	// The exit status is not the answer here: `mise ls --current --missing`
	// names what is missing on stdout and exits 0 (the derived image's build
	// reads it exactly this way), so only a broken invocation is an error.
	_, runErr := procgroup.Run(mctx, procgroup.Cmd{
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
	}
	missing := strings.TrimSpace(stdout.String())
	if missing == "" {
		return nil
	}
	// mise names each missing tool on its own line; keep the report short
	// and one-lined, the way every other reason is.
	return fmt.Errorf("mise installed %s but left these tools missing: %s", what, lastLine(missing))
}

// provisionEnv is env with the model credentials and the gateway's provider
// keys removed and GITHUB_TOKEN set to token (the run's git token, for
// mise's rate limit; left unset when the run has none, so an empty variable
// never shadows one the job's own environment carries).
func provisionEnv(env []string, token string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "GITHUB_TOKEN" || config.IsProviderKeyEnv(k) || contains(modelCredentialVars, k) {
			continue
		}
		out = append(out, kv)
	}
	if token != "" {
		out = append(out, "GITHUB_TOKEN="+token)
	}
	return out
}

// contains reports whether ss holds s.
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
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
