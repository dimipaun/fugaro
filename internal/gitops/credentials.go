package gitops

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// credentialHelper answers git's credential "get" requests from the
// environment, and ignores "store" and "erase". The token therefore lives
// only in FUGARO_GIT_TOKEN: never in .git/config, a remote URL, or a
// command line that git or the runner might log.
const credentialHelper = `!f() { test "$1" = get || exit 0; printf 'username=%s\npassword=%s\n' "$FUGARO_GIT_USERNAME" "$FUGARO_GIT_TOKEN"; }; f`

// CredentialVars returns environment variables that authenticate git to
// baseURL (scheme://host[:port], see CredentialURL) as username with token.
// They use git's GIT_CONFIG_COUNT environment config, which outranks the
// system, global and repository config: the first entry resets any
// credential helper configured there for baseURL, so none of them is asked
// for, or stores, the token. Hosts other than baseURL never see it.
//
// baseURL must be exactly the scheme://host[:port] form CredentialURL
// returns for an http(s) remote. Anything else — most importantly "", which
// CredentialURL returns for an ssh or local remote — is refused: git's
// config parser treats an empty URL subsection ("credential..helper") as
// matching every URL, so CredentialVars("", ...) would hand the token to
// every host git ever talks to. username and token must not contain "\n" or
// "\r" either, since the credential-helper protocol they're printed into is
// line-oriented and either could inject an extra field.
func CredentialVars(baseURL, username, token string) (map[string]string, error) {
	if baseURL == "" || CredentialURL(baseURL) != baseURL {
		return nil, fmt.Errorf("gitops: CredentialVars: %q is not a scheme://host[:port] (http or https) URL: refusing to scope credentials to every host", baseURL)
	}
	if strings.ContainsAny(username, "\n\r") || strings.ContainsAny(token, "\n\r") {
		return nil, fmt.Errorf("gitops: CredentialVars: username or token contains a newline or carriage return")
	}
	key := "credential." + baseURL + ".helper"
	return map[string]string{
		"GIT_CONFIG_COUNT":    "2",
		"GIT_CONFIG_KEY_0":    key,
		"GIT_CONFIG_VALUE_0":  "",
		"GIT_CONFIG_KEY_1":    key,
		"GIT_CONFIG_VALUE_1":  credentialHelper,
		"FUGARO_GIT_USERNAME": username,
		"FUGARO_GIT_TOKEN":    token,
	}, nil
}

// CredentialURL returns scheme://host[:port] for an http or https remote
// URL, or "" for anything else (a local path, or ssh), which needs no token.
func CredentialURL(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// WithVars returns env with vars set: existing entries for those names are
// replaced, and the rest are appended in sorted order.
func WithVars(env []string, vars map[string]string) []string {
	out := make([]string, 0, len(env)+len(vars))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := vars[k]; !ok {
			out = append(out, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		out = append(out, k+"="+vars[k])
	}
	return out
}
