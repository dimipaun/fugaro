package agent

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
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
		secretNames = append([]string{"ANTHROPIC_API_KEY"}, secretNames...)
	case "oauth":
		secretNames = append([]string{"CLAUDE_CODE_OAUTH_TOKEN"}, secretNames...)
	default:
		return nil, nil, fmt.Errorf("unknown agent auth %q", spec.Auth)
	}
	for _, k := range secretNames {
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
