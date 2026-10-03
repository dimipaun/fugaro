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
	// Gateway, when set, points Claude Code at Fugaro's gateway: the agent
	// gets the gateway's URL and per-run token, never the real model
	// credential (see GatewayVars). It is an error with auth: oauth.
	Gateway *Gateway
}

// BuildEnv builds the agent's environment from the runner's (parent), keeping
// only allowlisted variables, the auth variables and declared secrets. It
// returns the secret values so transcripts can be redacted.
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
	for _, k := range spec.Secrets {
		if config.IsProviderKeyEnv(k) {
			// A provider's key belongs to the runner's gateway, never to the
			// agent, whatever a workflow declares.
			return nil, nil, fmt.Errorf("secret %s is a provider key, which the agent never gets", k)
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
			// the agent's environment: the gateway holds it.
			realKey := p["ANTHROPIC_API_KEY"]
			if realKey == "" {
				return nil, nil, fmt.Errorf("secret ANTHROPIC_API_KEY is not set in the runner environment")
			}
			if len(realKey) < 4 {
				return nil, nil, fmt.Errorf("secret ANTHROPIC_API_KEY is shorter than 4 bytes, so it cannot be redacted safely")
			}
			secretValues = append(secretValues, realKey)
		}
	case "oauth":
		if spec.Gateway != nil {
			return nil, nil, fmt.Errorf("auth: oauth never goes through the gateway")
		}
		secretNames = append([]string{"CLAUDE_CODE_OAUTH_TOKEN"}, secretNames...)
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

// Gateway is where Claude Code sends its model calls instead of the
// provider: the gateway's loopback URL and its per-run token.
type Gateway struct{ BaseURL, Token string }

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
	case "api-key":
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
