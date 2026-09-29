package tf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// passThrough lists the only variables copied from fugaro's environment into
// terraform's. Everything else is dropped: every TF_* (TF_CLI_ARGS* could add
// -auto-approve, -target or -lock=false; TF_LOG at trace level logs bearer
// tokens; TF_REATTACH_PROVIDERS swaps in an unchecked provider), TERRAFORM_CONFIG,
// and the Google provider's own credential, project and region variables.
var passThrough = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
	"LANG": true, "LC_ALL": true, "TZ": true, "TMPDIR": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	// A TLS-inspecting proxy's CA.
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	// The same ADC fugaro itself uses.
	"CLOUDSDK_CONFIG": true, "GOOGLE_APPLICATION_CREDENTIALS": true,
}

// impersonate is refused rather than dropped, so the user knows the apply
// would otherwise have run as someone else.
const impersonate = "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT"

// Env builds terraform's environment from scratch: the allowlisted variables
// of parent, then TF_IN_AUTOMATION, TF_INPUT, CHECKPOINT_DISABLE, TF_DATA_DIR
// (<workdir>/.terraform) and TF_CLI_CONFIG_FILE (<workdir>/terraformrc, which
// it writes with only plugin_cache_dir = cache, so a user's ~/.terraformrc is
// never read). workdir and cache must be absolute.
func Env(parent []string, workdir, cache string) ([]string, error) {
	if !filepath.IsAbs(workdir) || !filepath.IsAbs(cache) {
		return nil, fmt.Errorf("terraform: the workdir %q and plugin cache %q must be absolute", workdir, cache)
	}
	// Refused rather than escaped: terraformrc is HCL, where quotes,
	// backslashes and ${ or %{ templates would change the meaning.
	if strings.ContainsAny(cache, "\"\\$%") || strings.ContainsFunc(cache, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, fmt.Errorf("terraform: unsupported characters in the plugin cache path %q", cache)
	}

	kept := map[string]string{}
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == impersonate {
			return nil, errors.New("terraform: " + impersonate + " is set; fugaro init runs Terraform as your own credentials only, so unset it")
		}
		if passThrough[k] || strings.HasPrefix(k, "XDG_") {
			kept[k] = v // the last one wins, as in exec
		}
	}
	rc := filepath.Join(workdir, "terraformrc")
	kept["TF_IN_AUTOMATION"] = "1"
	kept["TF_INPUT"] = "0"
	kept["CHECKPOINT_DISABLE"] = "1"
	kept["TF_DATA_DIR"] = filepath.Join(workdir, ".terraform")
	kept["TF_CLI_CONFIG_FILE"] = rc

	if err := os.MkdirAll(cache, 0o700); err != nil {
		return nil, fmt.Errorf("terraform: the plugin cache: %w", err)
	}
	if err := os.WriteFile(rc, []byte("plugin_cache_dir = \""+cache+"\"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("terraform: writing its CLI config: %w", err)
	}

	env := make([]string, 0, len(kept))
	for k, v := range kept {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return env, nil
}
