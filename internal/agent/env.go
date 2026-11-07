package agent

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// baseEnv is what the agent inherits from the runner besides auth and declared secrets.
var baseEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "TERM", "TMPDIR", "TZ",
	"JAVA_HOME", "GRADLE_USER_HOME", "NODE_OPTIONS", "npm_config_cache", "PNPM_HOME",
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	// Outbound proxies and extra CA certificates, for networks that need them.
	// Claude Code and Node.js, curl, and git read these.
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO",
	// Set by the base images (images/*/Dockerfile) or a repository Dockerfile.
	"DISABLE_AUTOUPDATER", "COREPACK_ENABLE_DOWNLOAD_PROMPT", "COREPACK_HOME", "PLAYWRIGHT_BROWSERS_PATH",
}

// vertexEnv is passed through with auth: vertex. CLOUD_ML_REGION and
// ANTHROPIC_VERTEX_PROJECT_ID are required; ANTHROPIC_VERTEX_BASE_URL points
// Claude Code at a gateway.
var vertexEnv = []string{"CLOUD_ML_REGION", "ANTHROPIC_VERTEX_PROJECT_ID", "GOOGLE_CLOUD_PROJECT", "ANTHROPIC_VERTEX_BASE_URL"}

// vertexRegionPrefix starts Claude Code's per-model Vertex region overrides,
// such as VERTEX_REGION_CLAUDE_4_5_SONNET.
const vertexRegionPrefix = "VERTEX_REGION_"

// EnvSpec describes the agent's environment.
type EnvSpec struct {
	Auth        string            // vertex | api-key | oauth
	Secrets     []string          // declared workflow secret variables, passed through
	Set         map[string]string // variables Fugaro sets, such as FUGARO_STATE_DIR
	PathPrepend string            // directory put first on PATH
	// ImageEnv are the variables the operator's own catalog image declares,
	// already narrowed by the caller (runner.imageEnv): a name here is dropped
	// if it is reserved, and it is written before everything Fugaro writes, so
	// nothing an image sets can override the routing, the auth, a declared
	// secret or a FUGARO_* name (docs/image-env, reconciliation D1370-D1374).
	// Nil -- the feature off -- for a job whose image the operator's catalog
	// did not choose, which is every job that carries no stamp.
	ImageEnv map[string]string
	// Gateway, when set, points Claude Code at Fugaro's gateway: the agent
	// gets the gateway's URL and per-run token, never the real model
	// credential (see GatewayVars). It is an error with auth: oauth.
	Gateway *Gateway
	// Compression, when set, points Claude Code at the context-compression
	// sidecar, which chains to the gateway. It is an error with auth: oauth
	// or vertex -- neither gives Claude Code a gateway token the sidecar
	// could forward, and a vertex run routes by ANTHROPIC_VERTEX_BASE_URL,
	// which Compression never touches -- and an error without a Gateway:
	// the sidecar holds no credential of its own, so the run would have to
	// send it the real key. config.Validate refuses the auth combination for
	// a repository's own file, but nothing populates this field yet: the
	// config key is still refused there, and starting the sidecar is a later
	// change, so no run reaches these refusals. They are here for the same
	// reason the existing oauth-vs-gateway refusal is -- the point closest
	// to the real credential refuses the combination itself.
	Compression *CompressionProxy
	// Docker is whether this run's workflow asked for `docker: true`
	// (design compute-backend.md §4.2): the one fact that lets DOCKER_HOST
	// through, because it is the one fact that says a socket was mounted
	// into this container. It is the runner's copy of the ask the launcher
	// passes as LaunchSpec.Docker; a run that did not ask is refused nothing
	// by it, it simply has no daemon to be pointed at.
	Docker bool
}

// BuildEnv builds the agent's environment from the runner's (parent), keeping
// only allowlisted variables, the auth variables and declared secrets. It
// returns the secret values so transcripts can be redacted.
//
// What it builds is the environment of the run's own stages. A stage that runs
// on a stage-scoped Claude OAuth account instead gets one built from this one
// (OAuthStageEnv), which is what keeps the coder's own environment an
// allow-list: the account's token is in no environment this function returns,
// whatever the runner's own environment holds
// (docs/design/mixed-credentials.md §11).
func BuildEnv(parent []string, spec EnvSpec) (env, secretValues []string, err error) {
	p := map[string]string{}
	for _, kv := range parent {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p[k] = v
		}
	}
	out := map[string]string{}
	for _, k := range baseEnv {
		if v, ok := p[k]; ok {
			out[k] = v
		}
	}
	if spec.Docker {
		// A run whose workflow asked for `docker: true` reaches the VM's
		// daemon through the socket the job mounted and the address the job
		// env names (design compute-backend.md §2.2, §4.3). It is gated on
		// the run's own ask, not on the variable being present in the
		// runner's environment: an operator with DOCKER_HOST exported who
		// hand-starts a runner -- or a local run on a machine that happens
		// to have one -- would otherwise hand the agent an address to a
		// daemon its container was never given a socket for (§4.4 check 6).
		// Nothing sets spec.Docker yet: the launcher's half is
		// LaunchSpec.Docker and the workflow's `docker: true` reaches both
		// in Task 8's config plumbing, so this fails closed until then.
		if v, ok := p["DOCKER_HOST"]; ok {
			out["DOCKER_HOST"] = v
		}
	}
	if len(spec.ImageEnv) > 0 {
		// The variables the operator's own catalog image declares, written
		// before the auth switch, the declared secrets, spec.Set and the
		// gateway: nothing an image sets can override the routing, the auth, a
		// declared secret or a FUGARO_* name (D1374). The caller has already
		// narrowed the set (runner.imageEnv); the filter is applied again here,
		// next to the allow-list it protects, so a caller that hands this map
		// over unnarrowed still cannot reach a reserved name or one a shell
		// cannot express (D1372).
		for k, v := range spec.ImageEnv {
			if config.ReservedEnv(k) || !config.EnvNameRE.MatchString(k) {
				continue
			}
			out[k] = v
		}
	}
	for _, k := range spec.Secrets {
		if config.IsProviderKeyEnv(k) {
			// A provider's key belongs to the runner's gateway, never to the
			// agent, whatever a workflow declares.
			return nil, nil, fmt.Errorf("secret %s is a provider key, which the agent never gets", k)
		}
		if spec.Auth == config.AuthCascade && config.IsModelSecretEnv(k) {
			// A cascade run's agent gets no model credential at all (D143):
			// the credentials service holds them and attaches one per call,
			// so a declared mount of one is refused rather than mounted.
			return nil, nil, fmt.Errorf("secret %s is a model credential and agent.auth is %s, which holds none: the credentials service attaches a credential per call and this agent never gets one (D143)", k, config.AuthCascade)
		}
	}
	// Provider keys are in the runner's environment (the gateway reads them)
	// and nowhere in the allow-list above; they are registered for redaction
	// here too, in case a mounted-secret list missed one.
	for _, k := range slices.Sorted(maps.Keys(p)) {
		if v := p[k]; config.IsProviderKeyEnv(k) && len(v) >= 4 {
			secretValues = append(secretValues, v)
		}
	}
	if spec.Compression != nil {
		if spec.Auth == "oauth" || spec.Auth == "vertex" {
			return nil, nil, fmt.Errorf("agent.compression cannot be combined with auth: %s: it routes %s's calls its own way, so the sidecar could only be skipped, never used", spec.Auth, spec.Auth)
		}
		if spec.Gateway == nil {
			return nil, nil, fmt.Errorf("agent.compression needs the budget gateway, and this run has none: the sidecar holds no credential of its own to send to Anthropic")
		}
	}
	secretNames := slices.Clone(spec.Secrets)
	switch spec.Auth {
	case "vertex":
		out["CLAUDE_CODE_USE_VERTEX"] = "1"
		for k, v := range p {
			if slices.Contains(vertexEnv, k) || strings.HasPrefix(k, vertexRegionPrefix) {
				out[k] = v
			}
		}
		if out["CLOUD_ML_REGION"] == "" || out["ANTHROPIC_VERTEX_PROJECT_ID"] == "" {
			return nil, nil, fmt.Errorf("vertex auth needs CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID in the runner environment")
		}
	case "api-key":
		if spec.Gateway == nil {
			secretNames = append([]string{"ANTHROPIC_API_KEY"}, secretNames...)
		} else {
			// The real key is registered for redaction but never put in
			// the agent's environment: the gateway holds it. It is
			// optional, too: a run whose models all route through
			// providers has none, and a call that needs it is refused by
			// the gateway rather than here (design project-centric §6.1).
			if realKey := p["ANTHROPIC_API_KEY"]; realKey != "" {
				if len(realKey) < 4 {
					return nil, nil, fmt.Errorf("secret ANTHROPIC_API_KEY is shorter than 4 bytes, so it cannot be redacted safely")
				}
				secretValues = append(secretValues, realKey)
			}
		}
	case "oauth":
		if spec.Gateway != nil {
			return nil, nil, fmt.Errorf("auth: oauth never goes through the gateway")
		}
		secretNames = append([]string{"CLAUDE_CODE_OAUTH_TOKEN"}, secretNames...)
	case config.AuthCascade:
		// A cascade run carries no credential of its own: the credentials
		// service holds every model credential and attaches one per call
		// (D143), so there is no secret to require, nothing to mount, and
		// nothing of the job's own in the environment. What Claude Code
		// presents is the gateway's per-run token, which GatewayVars sets
		// below; a real key the runner's environment happens to hold is
		// registered for redaction here and never handed to the agent.
		if spec.Gateway == nil {
			return nil, nil, fmt.Errorf("auth: %s runs through the gateway, and this agent has none: a cascade run has no credential of its own to fall back to (D143)", config.AuthCascade)
		}
		if realKey := p["ANTHROPIC_API_KEY"]; realKey != "" {
			if len(realKey) < 4 {
				return nil, nil, fmt.Errorf("secret ANTHROPIC_API_KEY is shorter than 4 bytes, so it cannot be redacted safely")
			}
			secretValues = append(secretValues, realKey)
		}
	default:
		return nil, nil, fmt.Errorf("unknown agent auth %q", spec.Auth)
	}
	for _, k := range secretNames {
		if spec.Gateway != nil && k == "ANTHROPIC_API_KEY" {
			// A declared secret of the same name would put the real key
			// back; the gateway's token stands in for it.
			if v := p[k]; len(v) >= 4 && !slices.Contains(secretValues, v) {
				secretValues = append(secretValues, v)
			}
			continue
		}
		v := p[k]
		if v == "" {
			if k == "ANTHROPIC_API_KEY" {
				// This run has no gateway at all (the budget is off), so
				// every call goes to Anthropic -- the key is not optional
				// here whatever the pins are -- and "is not set" alone names
				// no fix. Refuse with the gateway's own text, one text for
				// both (internal/config.MissingAnthropicKey).
				return nil, nil, fmt.Errorf("secret ANTHROPIC_API_KEY is not set in the runner environment: %s", config.MissingAnthropicKey)
			}
			return nil, nil, fmt.Errorf("secret %s is not set in the runner environment", k)
		}
		if len(v) < 4 {
			return nil, nil, fmt.Errorf("secret %s is shorter than 4 bytes, so it cannot be redacted safely", k)
		}
		out[k] = v
		secretValues = append(secretValues, v)
	}
	maps.Copy(out, spec.Set)
	if spec.Gateway != nil {
		// Last, so nothing declared or inherited can override the routing.
		vars, err := GatewayVars(spec.Auth, *spec.Gateway, p)
		if err != nil {
			return nil, nil, err
		}
		maps.Copy(out, vars)
	}
	if spec.Compression != nil {
		// After the gateway's own write, not before: the sidecar is where
		// Claude Code's calls go first, and GatewayVars has just put the
		// gateway's URL here.
		out["ANTHROPIC_BASE_URL"] = spec.Compression.BaseURL
	}
	if spec.PathPrepend != "" {
		if out["PATH"] != "" {
			out["PATH"] = spec.PathPrepend + string(os.PathListSeparator) + out["PATH"]
		} else {
			out["PATH"] = spec.PathPrepend
		}
	}
	for _, k := range slices.Sorted(maps.Keys(out)) {
		env = append(env, k+"="+out[k])
	}
	return env, secretValues, nil
}

// gatewayRouteVars are the variables GatewayVars routes Claude Code at the
// gateway with: the address its calls go to and the credential it presents
// there. A stage that runs on a Claude OAuth credential of its own carries
// none of them (OAuthStageEnv), and its calls go to Anthropic's own endpoint.
var gatewayRouteVars = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"}

// OAuthStageEnv returns the environment of a stage that runs on a Claude
// OAuth credential of its own rather than on the run's gateway
// (docs/design/mixed-credentials.md §11, the owner's B0 ruling): runEnv, the
// environment BuildEnv built for the run's own stages, with the gateway's
// routing dropped, any credential under the token's own name dropped with it,
// and the account's token set. What is left is the stage's
// whole environment — the base variables, the declared secrets and the
// directory variables are the run's, and the model the stage runs reaches
// Claude Code as the --model flag (Args), never as a routing variable. The
// caller registers the token for redaction for the whole run, not here.
//
// A token this build could not redact is refused rather than used, the way
// BuildEnv refuses one: the alternative would be a stage that runs without the
// credential its configuration gave it, which is the one thing the ruling
// says a review stage must never do.
func OAuthStageEnv(runEnv []string, token string) ([]string, error) {
	switch {
	case token == "":
		return nil, fmt.Errorf("a stage that runs on a Claude OAuth account needs CLAUDE_CODE_OAUTH_TOKEN in the runner's environment, and this run holds none: the account's token is mounted into the runner's job — or, for a local run, put in the environment the runner was started with — before the run begins, and this run was given no token to run the stage on")
	case len(token) < 4:
		return nil, fmt.Errorf("CLAUDE_CODE_OAUTH_TOKEN is shorter than 4 bytes, so it cannot be redacted safely")
	}
	out := make([]string, 0, len(runEnv)+1)
	for _, kv := range runEnv {
		switch k, _, ok := strings.Cut(kv, "="); {
		case !ok:
		case slices.Contains(gatewayRouteVars, k), k == "CLAUDE_CODE_OAUTH_TOKEN":
			// Appended below, once: what this stage presents is the account's
			// token, and a second entry under the same name is an answer to
			// which credential it presents that no reader agrees on (a
			// getenv takes the first match, a map the last).
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CLAUDE_CODE_OAUTH_TOKEN="+token), nil
}

// Gateway is where Claude Code sends its model calls instead of the
// provider: the gateway's loopback URL and its per-run token.
type Gateway struct{ BaseURL, Token string }

// CompressionProxy is the context-compression sidecar Claude Code's model
// calls route through first, which chains them on to the gateway. It is a
// loopback URL and nothing else: the per-run token Claude Code presents is
// the gateway's own, and travels in ANTHROPIC_API_KEY, not here.
type CompressionProxy struct{ BaseURL string }

// noProxyLoopback is added to NO_PROXY and no_proxy with a gateway, so a
// configured proxy never sees the agent's calls to it.
const noProxyLoopback = "127.0.0.1,localhost"

// GatewayVars are the variables that route Claude Code through g for auth,
// the same ones BuildEnv puts in the environment and the managed settings
// file carries. parent is the runner's environment: Vertex's region and
// project, and any NO_PROXY, are read from it. With api-key the variable
// ANTHROPIC_API_KEY holds the gateway's token, never the real key; the
// parent's ANTHROPIC_VERTEX_BASE_URL is never used.
func GatewayVars(auth string, g Gateway, parent map[string]string) (map[string]string, error) {
	if g.BaseURL == "" {
		return nil, fmt.Errorf("the gateway has no URL")
	}
	base := strings.TrimRight(g.BaseURL, "/")
	out := map[string]string{
		"NO_PROXY": withLoopback(parent["NO_PROXY"]),
		"no_proxy": withLoopback(parent["no_proxy"]),
	}
	switch auth {
	case "api-key", config.AuthCascade:
		// A cascade run is the api-key case's shape with no key behind it:
		// Claude Code is pointed at the gateway and presents the gateway's
		// own per-run token, and the real credential is attached to the call
		// by the credentials service, which the job never holds (D143).
		if g.Token == "" {
			return nil, fmt.Errorf("the gateway has no token")
		}
		out["ANTHROPIC_BASE_URL"] = base
		out["ANTHROPIC_API_KEY"] = g.Token
	case "vertex":
		region, project := parent["CLOUD_ML_REGION"], parent["ANTHROPIC_VERTEX_PROJECT_ID"]
		if region == "" || project == "" {
			return nil, fmt.Errorf("vertex auth needs CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID in the runner environment")
		}
		out["CLAUDE_CODE_USE_VERTEX"] = "1"
		out["ANTHROPIC_VERTEX_BASE_URL"] = base + "/v1"
		out["CLAUDE_CODE_SKIP_VERTEX_AUTH"] = "1"
		out["CLOUD_ML_REGION"] = region
		out["ANTHROPIC_VERTEX_PROJECT_ID"] = project
	case "oauth":
		return nil, fmt.Errorf("auth: oauth never goes through the gateway")
	default:
		return nil, fmt.Errorf("unknown agent auth %q", auth)
	}
	return out, nil
}

// withLoopback appends the loopback hosts to a NO_PROXY list that lacks them.
func withLoopback(list string) string {
	var have []string
	for _, h := range strings.Split(list, ",") {
		if h = strings.TrimSpace(h); h != "" {
			have = append(have, h)
		}
	}
	for _, h := range strings.Split(noProxyLoopback, ",") {
		if !slices.Contains(have, h) {
			have = append(have, h)
		}
	}
	return strings.Join(have, ",")
}

// ProviderRouteVars disable the Claude Code additions a provider route
// refuses: CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1 drops the `safeguards`
// field Claude Code sends on a session's first request; MAX_THINKING_TOKENS=0
// drops the `thinking` field, which is what makes Claude Code add
// `context_management` to bound it. Together they drop both `safeguards` and
// `context_management` from the body; the `anthropic-beta` header is
// measured unchanged (still sent) either way, and is not what the gateway
// checks. The gateway refuses `safeguards` and `context_management` as
// fields a provider route doesn't take (routedKeys,
// internal/gateway/forward.go), before any call reaches the provider.
// Measured against Claude Code 2.1.296 (docs/multi-model.md §7); apply only
// to a stage whose model or background model is a provider's, never to an
// all-Claude stage, whose request shape must stay exactly what it is today.
func ProviderRouteVars() map[string]string {
	return map[string]string{
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1",
		"MAX_THINKING_TOKENS":                    "0",
	}
}

// EffortVars are the variables that give a Claude stage its reasoning
// effort: CLAUDE_CODE_EFFORT_LEVEL alone, which beats the --effort flag and
// the effortLevel settings key and is in turn clamped by a maxEffortLevel in
// any settings file (docs/multi-model.md, "How the pinned Claude Code takes
// an effort setting").
//
// Nothing is set for a provider stage -- those run with thinking off, so an
// effort there would look applied and be no-ops (G5, D725) -- and nothing
// for an absent effort, which already means the model's own default.
//
// Nothing calls it yet: the runner that puts its answer in a stage's
// environment is L4.2, and until that lands this is the piece it builds on.
//
// The value is validated before either of those is decided: it comes from
// the launcher's task field, and Claude Code is no gate at all -- the flag
// warns and sends the default, the variable and the settings key are dropped
// in silence -- so Fugaro refusing a value outside the closed set is the
// only refusal there is.
func EffortVars(effort string, provider bool) (map[string]string, error) {
	if effort != "" && !config.ValidEffort(effort) {
		return nil, fmt.Errorf("effort %q must be one of %s", effort, strings.Join(config.Efforts, ", "))
	}
	if provider || effort == "" {
		return map[string]string{}, nil
	}
	return map[string]string{"CLAUDE_CODE_EFFORT_LEVEL": effort}, nil
}

// PinVars are a stage's pins (design §2.1): the model for Claude Code's
// opus, sonnet and subagent roles (when set), the background model for its
// haiku role (when set), and the output limit per call (when > 0). Each is
// independent of the others; with nothing set the result is empty.
func PinVars(model, background string, maxOutput int64) map[string]string {
	out := map[string]string{}
	if model != "" {
		out["ANTHROPIC_DEFAULT_OPUS_MODEL"] = model
		out["ANTHROPIC_DEFAULT_SONNET_MODEL"] = model
		out["CLAUDE_CODE_SUBAGENT_MODEL"] = model
	}
	if background != "" {
		out["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = background
	}
	if maxOutput > 0 {
		out["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = strconv.FormatInt(maxOutput, 10)
	}
	return out
}
