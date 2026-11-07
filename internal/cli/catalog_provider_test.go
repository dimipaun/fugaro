package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// catalogBuildAppID is the App ID the catalog fixtures' local config records
// for acme/app. It is not a secret: it is the number on the App's settings
// page.
const catalogBuildAppID = "12345"

// catalogEnvOnlyAppID is the App ID a test exports instead, different from the
// configured one, so a test can tell which of the two a build used.
const catalogEnvOnlyAppID = "67890"

// catalogAppKeyPlaceholder is a key an operator's own environment holds: an
// obvious placeholder, since the point of every test using it is that nothing
// reads or parses it.
const catalogAppKeyPlaceholder = "not-a-real-key"

// catalogTokenPlaceholder is the value the Bitbucket fixtures' bitbucket-token
// secret holds. An obvious placeholder, because every test using it asserts
// that the value reaches the provider's environment and nowhere else: a test
// that leaks it has to be readable as a leak.
const catalogTokenPlaceholder = "catalog-token-placeholder"

// catalogProviderEnvRig is the state catalogProviderEnv is called with: the
// spec of a GitHub repository, whose App ID the local config supplied and
// infra.Repo carried (the field the environment is built from), and one
// Dockerfile entry that opens the provider.
func catalogProviderEnvRig(t *testing.T) (*localcfg.Config, infra.RepoSpec, []config.Entry) {
	t.Helper()
	lc := &localcfg.Config{
		Name: "aurora", GCPProject: "proj-1234",
		Repos: map[string]localcfg.Repo{"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: catalogBuildAppID}},
	}
	rs := infra.RepoSpec{
		Name: "acme/app", Slug: mustSlug(gitprov.KindGitHub, "acme/app"), Provider: gitprov.KindGitHub,
		GitHubAppID:              catalogBuildAppID,
		BuildServiceAccountEmail: "fugaro-build-" + mustSlug(gitprov.KindGitHub, "acme/app") + "@proj-1234.iam.gserviceaccount.com",
	}
	entries := []config.Entry{{Name: "base", Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/app", Path: ".fugaro/base.Dockerfile"}}}
	return lc, rs, entries
}

// catalogProviderEnvBitbucketRig is catalogProviderEnvRig for a Bitbucket
// repository: the same repository and the same Dockerfile entry, with the
// provider that has no App and no key and whose credential is the project's
// bitbucket-token secret instead.
func catalogProviderEnvBitbucketRig(t *testing.T) (*localcfg.Config, infra.RepoSpec, []config.Entry) {
	t.Helper()
	lc, rs, entries := catalogProviderEnvRig(t)
	rs.Provider = gitprov.KindBitbucket
	rs.Slug = mustSlug(gitprov.KindBitbucket, "acme/app")
	rs.GitHubAppID = ""
	rs.BuildServiceAccountEmail = "fugaro-build-" + rs.Slug + "@proj-1234.iam.gserviceaccount.com"
	return lc, rs, entries
}

// catalogClearProviderEnv unsets the variables providers.FromEnv reads for
// GitHub, so a test's own environment cannot answer for the fixture. The
// Setenv registers the restore and the Unsetenv removes the variable now, so
// the process environment holds neither a value nor an empty one: an empty
// entry would make envValue find the wrong copy of a variable this code
// appends its own value to.
func catalogClearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{providers.EnvGitHubAppID, providers.EnvGitHubAppKey, providers.EnvGitHubAppKeyFile, providers.EnvBitbucketToken, providers.EnvCredentialsServiceURL} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// catalogStubKeyRead replaces the App key's secret-ID resolution and the read
// itself, counting each, and answers the key the test gives (copied, because
// the caller clears the bytes it is handed). It is the fake secret reader
// every key case runs against: nothing in these tests reaches Secret Manager.
func catalogStubKeyRead(t *testing.T, pem []byte, err error) (resolves, reads *int) {
	t.Helper()
	resolves, reads = new(int), new(int)
	wasID, wasRead := catalogAppKeySecretID, readSecretValue
	catalogAppKeySecretID = func(context.Context, *localcfg.Config, infra.RepoSpec) (string, error) {
		*resolves++
		return gcp.SecretID(mustSlug(gitprov.KindGitHub, "acme/app"), config.GitHubAppKeySecret), nil
	}
	readSecretValue = func(context.Context, *localcfg.Config, string) ([]byte, error) {
		*reads++
		return append([]byte(nil), pem...), err
	}
	t.Cleanup(func() { catalogAppKeySecretID, readSecretValue = wasID, wasRead })
	return resolves, reads
}

// catalogStubTokenRead is catalogStubKeyRead for the Bitbucket token: it
// replaces the token's secret-ID resolution and the read itself, counting
// each, and answers the value the test gives (copied, because the caller
// clears the bytes it is handed). Nothing in these tests reaches Secret
// Manager.
func catalogStubTokenRead(t *testing.T, token []byte, err error) (resolves, reads *int) {
	t.Helper()
	resolves, reads = new(int), new(int)
	wasID, wasRead := catalogTokenSecretID, readSecretValue
	catalogTokenSecretID = func(context.Context, *localcfg.Config, infra.RepoSpec) (string, error) {
		*resolves++
		return gcp.SecretID(mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret), nil
	}
	readSecretValue = func(context.Context, *localcfg.Config, string) ([]byte, error) {
		*reads++
		return append([]byte(nil), token...), err
	}
	t.Cleanup(func() { catalogTokenSecretID, readSecretValue = wasID, wasRead })
	return resolves, reads
}

// TestCatalogGitHubAppIDFromTheConfigOrTheEnvironment: the App ID a catalog
// build is named with comes from the repository's own field in the local
// config, and only from FUGARO_GITHUB_APP_ID when the config holds none
// (issue #1701). With both set and different the configured one wins: a shell
// that exports another App's ID must not point this build, and the Cloud Build
// clone it submits, at an App this repository was not onboarded with.
func TestCatalogGitHubAppIDFromTheConfigOrTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, configured, exported, want string
	}{
		{"the config holds it", catalogBuildAppID, "", catalogBuildAppID},
		{"only the environment holds it", "", catalogEnvOnlyAppID, catalogEnvOnlyAppID},
		{"the config wins over a different environment ID", catalogBuildAppID, catalogEnvOnlyAppID, catalogBuildAppID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := &localcfg.Config{Name: "aurora", GCPProject: "proj-1234"}
			if tc.configured != "" {
				lc.Repos = map[string]localcfg.Repo{"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: tc.configured}}
			}
			getenv := func(k string) string {
				if k == providers.EnvGitHubAppID {
					return tc.exported
				}
				return ""
			}
			got, err := catalogGitHubAppID(lc, "acme/app", getenv)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got != tc.want {
				t.Errorf("the App ID is %q, want %q", got, tc.want)
			}
		})
	}

	// Neither source: the refusal names both, because they are the two ways
	// this ID ever gets here, and the command that writes the first -- and
	// neither is a flag this command takes.
	_, err := catalogGitHubAppID(&localcfg.Config{Name: "aurora", GCPProject: "proj-1234"}, "acme/app", func(string) string { return "" })
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: no App ID anywhere is a user error", ExitCode(err), err)
	}
	for _, want := range []string{"repos.acme/app.github_app_id", "update --repo acme/app --github-app-id", providers.EnvGitHubAppID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// TestCatalogProviderEnv: the environment a catalog build hands OpenProvider
// carries the repository's App ID, and the App's private key from where it
// actually is -- the environment's own copy when there is one, else the
// repository's github-app-key secret, read with the person's own credentials
// and refused in a coding agent's session (issue #1701).
func TestCatalogProviderEnv(t *testing.T) {
	t.Run("an exported key wins and no secret is read", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		resolves, reads := catalogStubKeyRead(t, nil, errors.New("Secret Manager must not be read"))
		t.Setenv(providers.EnvGitHubAppKey, catalogAppKeyPlaceholder)
		var notice bytes.Buffer
		env, _, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := envValue(env, providers.EnvGitHubAppID); got != catalogBuildAppID {
			t.Errorf("%s = %q, want the repository's own App ID", providers.EnvGitHubAppID, got)
		}
		if got := envValue(env, providers.EnvGitHubAppKey); got != catalogAppKeyPlaceholder {
			t.Errorf("%s is not the exported key", providers.EnvGitHubAppKey)
		}
		if *resolves != 0 || *reads != 0 {
			t.Errorf("the key was read from Secret Manager (%d resolutions, %d reads) although the environment holds one", *resolves, *reads)
		}
		if notice.Len() != 0 {
			t.Errorf("a read that did not happen was announced: %q", notice.String())
		}
	})

	t.Run("an exported key file wins too", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		resolves, reads := catalogStubKeyRead(t, nil, errors.New("Secret Manager must not be read"))
		// A path that need not exist: this only builds the environment, and
		// reading the file is the provider's own job (it refuses a missing
		// one), so the shortcut is what is under test and not a file.
		path := filepath.Join(t.TempDir(), "app-key.pem")
		t.Setenv(providers.EnvGitHubAppKeyFile, path)
		var notice bytes.Buffer
		env, _, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := envValue(env, providers.EnvGitHubAppKeyFile); got != path {
			t.Errorf("%s = %q, want the file the environment names: the provider reads it from there", providers.EnvGitHubAppKeyFile, got)
		}
		if _, ok := envHas(env, providers.EnvGitHubAppKey); ok {
			t.Errorf("%s was set although the environment names a key file", providers.EnvGitHubAppKey)
		}
		if *resolves != 0 || *reads != 0 {
			t.Errorf("the key was read from Secret Manager (%d resolutions, %d reads) although the environment names a key file", *resolves, *reads)
		}
		if notice.Len() != 0 {
			t.Errorf("a read that did not happen was announced: %q", notice.String())
		}
	})

	t.Run("the repository's secret holds the key", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		key := appKey(t)
		resolves, reads := catalogStubKeyRead(t, key, nil)
		var notice bytes.Buffer
		env, _, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if *resolves != 1 || *reads != 1 {
			t.Fatalf("the secret was resolved %d times and read %d times, want one of each", *resolves, *reads)
		}
		if got := envValue(env, providers.EnvGitHubAppKey); got != string(key) {
			t.Errorf("the environment does not carry the key the repository's %s secret holds", config.GitHubAppKeySecret)
		}
		if !strings.Contains(notice.String(), "reads the App's private key with your own credentials") {
			t.Errorf("the read was not announced: %q", notice.String())
		}
		if strings.Contains(notice.String(), string(key)) {
			t.Error("the announcement carries the key")
		}
	})

	t.Run("a failed read carries no key", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		key := appKey(t)
		catalogStubKeyRead(t, key, errors.New("the secret is unreachable"))
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if err == nil {
			t.Fatal("a failed read answered an environment")
		}
		if env != nil {
			t.Error("a failed read answered an environment")
		}
		body := strings.Join(strings.Split(strings.TrimSpace(string(key)), "\n")[1:], "")
		for _, leak := range []string{string(key), body, "BEGIN RSA PRIVATE KEY"} {
			if leak != "" && strings.Contains(err.Error(), leak) {
				t.Errorf("the failure carries key material: %v", err)
			}
		}
	})

	// The two refusals the read's own errors turn into. Each says what the
	// person can do about it and neither repeats anything the read answered:
	// the key is not stored yet (the fix is the command that stores it), or
	// this account may not read secret values at all.
	t.Run("a key that was never stored", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		catalogStubKeyRead(t, nil, fmt.Errorf("secret versions.list: %w", gcp.ErrNoVersion))
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, err %v: a secret with no version is a user error", ExitCode(err), err)
		}
		if env != nil {
			t.Error("a failed read answered an environment")
		}
		for _, want := range []string{"is not stored yet", "secrets set " + config.GitHubAppKeySecret + " --repo acme/app"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
	})

	t.Run("no permission to read secret values", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		catalogStubKeyRead(t, nil, fmt.Errorf("secrets.versions.access: %w", gcp.ErrNoAccess))
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d, err %v: a missing secretAccessor is a user error", ExitCode(err), err)
		}
		if env != nil {
			t.Error("a failed read answered an environment")
		}
		if want := "you may not read secret values (a project owner can)"; !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	})

	t.Run("a coding agent's session reads nothing", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		resolves, reads := catalogStubKeyRead(t, appKey(t), nil)
		t.Setenv("CLAUDECODE", "1")
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "refused in a coding agent's session") {
			t.Fatalf("exit %d, err %v: the read is refused under a marker", ExitCode(err), err)
		}
		if env != nil || *reads != 0 || *resolves != 0 {
			t.Errorf("a refused read answered an environment (%d resolutions, %d reads)", *resolves, *reads)
		}
	})

	// An inline definition is copied with no repository read at all, so a
	// build whose entries are all inline opens no provider and reads no
	// secret: the environment is taken as it stands.
	t.Run("an all-inline catalog reads nothing", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, _ := catalogProviderEnvRig(t)
		resolves, reads := catalogStubKeyRead(t, appKey(t), nil)
		entries := []config.Entry{{Name: "tools", Source: config.Source{Kind: config.SourceInline, Inline: "FROM example.invalid/base\n"}}}
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := envHas(env, providers.EnvGitHubAppID); ok {
			t.Errorf("%s was set for a catalog that opens no provider", providers.EnvGitHubAppID)
		}
		if *resolves != 0 || *reads != 0 {
			t.Errorf("the key was read (%d resolutions, %d reads)", *resolves, *reads)
		}
	})

	// A Bitbucket repository has no App and no App key: the App ID is never
	// set for it, whatever its entries are.
	t.Run("a bitbucket repository sets no App ID", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		resolves, _ := catalogStubKeyRead(t, appKey(t), nil)
		catalogStubTokenRead(t, []byte(catalogTokenPlaceholder), nil)
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := envHas(env, providers.EnvGitHubAppID); ok {
			t.Errorf("%s was set for a Bitbucket repository", providers.EnvGitHubAppID)
		}
		if *resolves != 0 {
			t.Errorf("the App key's secret was resolved %d times for a Bitbucket repository", *resolves)
		}
	})
}

// TestCatalogProviderEnvBitbucket: the environment a catalog build hands
// OpenProvider for a Bitbucket repository carries the access token from where
// it actually is. An operator's shell holds no FUGARO_BITBUCKET_TOKEN, which
// is what made `fugaro image build <entry>` fail on the first entry whose
// Dockerfile lives in a Bitbucket repository before anything was billed; the
// token is the project's bitbucket-token secret, read once with the person's
// own credentials, held in memory and never printed.
func TestCatalogProviderEnvBitbucket(t *testing.T) {
	t.Run("the project's secret holds the token", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		token := []byte(catalogTokenPlaceholder)
		resolves, reads := catalogStubTokenRead(t, token, nil)
		var notice bytes.Buffer
		env, hint, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if *resolves != 1 || *reads != 1 {
			t.Fatalf("the secret was resolved %d times and read %d times, want one of each", *resolves, *reads)
		}
		if got := envValue(env, providers.EnvBitbucketToken); got != string(token) {
			t.Errorf("the environment does not carry the token the project's %s secret holds", config.BitbucketSecret)
		}
		if hint != "" {
			t.Errorf("a read that answered a token left a hint behind: %q", hint)
		}
		if !strings.Contains(notice.String(), "reads the project's "+config.BitbucketSecret+" secret") {
			t.Errorf("the read was not announced: %q", notice.String())
		}
		if strings.Contains(notice.String(), string(token)) {
			t.Error("the announcement carries the token")
		}
	})

	t.Run("an exported token wins and no secret is read", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		resolves, reads := catalogStubTokenRead(t, nil, errors.New("Secret Manager must not be read"))
		t.Setenv(providers.EnvBitbucketToken, catalogTokenPlaceholder)
		var notice bytes.Buffer
		env, hint, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := envValue(env, providers.EnvBitbucketToken); got != catalogTokenPlaceholder {
			t.Errorf("%s is not the exported token", providers.EnvBitbucketToken)
		}
		if *resolves != 0 || *reads != 0 {
			t.Errorf("the token was read from Secret Manager (%d resolutions, %d reads) although the environment holds one", *resolves, *reads)
		}
		if notice.Len() != 0 || hint != "" {
			t.Errorf("a read that did not happen was announced: %q, hint %q", notice.String(), hint)
		}
	})

	// The load-bearing refusal: whatever else is true, a coding agent's
	// session never reads the value (SECURITY.md). Removing the marker check
	// makes this read happen and fails here.
	t.Run("a coding agent's session reads nothing", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		resolves, reads := catalogStubTokenRead(t, []byte(catalogTokenPlaceholder), nil)
		t.Setenv("CLAUDECODE", "1")
		env, hint, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "refused in a coding agent's session") {
			t.Fatalf("exit %d, err %v: the read is refused under a marker", ExitCode(err), err)
		}
		if env != nil || hint != "" || *reads != 0 || *resolves != 0 {
			t.Errorf("a refused read answered an environment (%d resolutions, %d reads)", *resolves, *reads)
		}
	})

	// The fallback: the read fails, so the environment is left without a token
	// and the provider's own "is not set" failure is what the operator sees --
	// with one sentence saying where the token is and what reading it needs.
	t.Run("a token that was never stored", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		catalogStubTokenRead(t, nil, fmt.Errorf("secret versions.access: %w", gcp.ErrNoVersion))
		var notice bytes.Buffer
		env, hint, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("a failed read is not an error of its own: the provider's own failure is what says so: %v", err)
		}
		if _, ok := envHas(env, providers.EnvBitbucketToken); ok {
			t.Errorf("%s was set although no token was read", providers.EnvBitbucketToken)
		}
		for _, want := range []string{config.BitbucketSecret, gcp.SecretAccessorRole} {
			if !strings.Contains(hint, want) {
				t.Errorf("the hint does not name %s: %q", want, hint)
			}
		}
	})

	t.Run("no permission to read secret values", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		catalogStubTokenRead(t, nil, fmt.Errorf("secrets.versions.access: %w", gcp.ErrNoAccess))
		env, hint, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := envHas(env, providers.EnvBitbucketToken); ok {
			t.Errorf("%s was set although no token was read", providers.EnvBitbucketToken)
		}
		for _, want := range []string{config.BitbucketSecret, gcp.SecretAccessorRole, "project owner"} {
			if !strings.Contains(hint, want) {
				t.Errorf("the hint does not name %s: %q", want, hint)
			}
		}
	})

	// A read that failed for a reason of its own says so rather than leaving
	// the operator with the provider's bare "is not set".
	t.Run("a read that failed for its own reason", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvBitbucketRig(t)
		catalogStubTokenRead(t, nil, errors.New("the secret is unreachable"))
		_, hint, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, want := range []string{"the secret is unreachable", config.BitbucketSecret, gcp.SecretAccessorRole} {
			if !strings.Contains(hint, want) {
				t.Errorf("the hint does not say %q: %q", want, hint)
			}
		}
	})

	// An inline definition is copied with no repository read at all: a
	// Bitbucket build whose entries are all inline reads no token either.
	t.Run("an all-inline catalog reads nothing", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, _ := catalogProviderEnvBitbucketRig(t)
		resolves, reads := catalogStubTokenRead(t, []byte(catalogTokenPlaceholder), nil)
		entries := []config.Entry{{Name: "tools", Source: config.Source{Kind: config.SourceInline, Inline: "FROM example.invalid/base\n"}}}
		env, _, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv, true)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := envHas(env, providers.EnvBitbucketToken); ok {
			t.Errorf("%s was set for a catalog that opens no provider", providers.EnvBitbucketToken)
		}
		if *resolves != 0 || *reads != 0 {
			t.Errorf("the token was read (%d resolutions, %d reads)", *resolves, *reads)
		}
	})
}

// catalogCopyProvider is the copy's opener for a checkout, whatever its
// provider: the REAL OpenProvider, so the environment this command built is
// what decides whether the copy can read anything at all, handing back a
// file-backed fake for the read itself, which is the network no test has. It
// answers the environment the command built, for the test to look at.
func catalogCopyProvider(t *testing.T, files map[string]string) *[]string {
	t.Helper()
	p := &fake.Provider{Repo: "acme/app", State: fake.State{Files: files}}
	was := openCatalogProvider
	env := new([]string)
	openCatalogProvider = func(e []string, warn func(string)) (string, gitprov.Opener, error) {
		*env = e
		kind, open, err := OpenProvider(e, warn)
		if err != nil {
			return "", nil, err
		}
		return kind, func(ctx context.Context, k, repo string) (gitprov.Provider, []string, error) {
			if _, _, err := open(ctx, k, repo); err != nil {
				return nil, nil, err
			}
			return p, nil, nil
		}, nil
	}
	t.Cleanup(func() { openCatalogProvider = was })
	return env
}

// TestImageBuildCatalogReadsTheGitHubAppFromTheConfig: `fugaro image build
// <entry>` on an entry whose Dockerfile is in a GitHub repository copies it
// with the project's existing credential, and that credential is named where
// it actually is -- the App ID the local config records for the repository and
// the App's key in its github-app-key secret (issue #1701). The provider is
// opened for real here: before this, it was opened from the process
// environment alone, which holds neither, and the build stopped at
// "opening the provider of acme/app: FUGARO_GITHUB_APP_ID is not set" before
// anything was billed.
func TestImageBuildCatalogReadsTheGitHubAppFromTheConfig(t *testing.T) {
	fb, gcs, f := catalogBuildCheckoutFor(t, cliMinimalYAML, catalogBuildYAML, gitprov.KindGitHub, catalogBuildAppID)
	catalogClearProviderEnv(t)
	keyID := putSecretID(t, f.sm, mustSlug(gitprov.KindGitHub, "acme/app"), config.GitHubAppKeySecret, appKey(t))
	f.sm.AllowAccess()
	env := catalogCopyProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

	out, stderr, err := executeBuild(t, "image", "build", "base", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := envValue(*env, providers.EnvGitHubAppID); got != catalogBuildAppID {
		t.Errorf("%s = %q, want the App ID the local config records", providers.EnvGitHubAppID, got)
	}
	if got := envValue(*env, providers.EnvGitHubAppKey); got != string(appKey(t)) {
		t.Error("the environment does not carry the key the repository's github-app-key secret holds")
	}
	if accessed := f.sm.Accessed(); len(accessed) != 1 || accessed[0] != keyID {
		t.Errorf("the secrets whose values were read are %v, want exactly %s", accessed, keyID)
	}
	if got, _ := catalogCopy(t, gcs, "base"); got != catalogBuildFetched {
		t.Errorf("the copy is %q, want the definition the repository holds", got)
	}
	if subs := catalogBuildSubs(t, fb); subs["_GITHUB_APP_ID"] != catalogBuildAppID {
		t.Errorf("_GITHUB_APP_ID = %v, want the repository's App ID on the build too", subs["_GITHUB_APP_ID"])
	}
	if !strings.Contains(out, "catalog-base@sha256:") {
		t.Errorf("stdout = %q, want the built image and its digest", out)
	}
}

// TestImageBuildCatalogGitHubWithoutAnAppID: with neither the config nor the
// environment holding an App ID there is nothing to open the provider with,
// and the refusal says so by naming both places the ID comes from -- rather
// than the --github-app-id this command does not take.
func TestImageBuildCatalogGitHubWithoutAnAppID(t *testing.T) {
	_, _, _ = catalogBuildCheckoutFor(t, cliMinimalYAML, catalogBuildYAML, gitprov.KindGitHub, "")
	catalogClearProviderEnv(t)
	_, _, err := executeBuild(t, "image", "build", "base", "--base", "b:1")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: a missing App ID is a user error", ExitCode(err), err)
	}
	for _, want := range []string{"repos.acme/app.github_app_id", "update --repo acme/app --github-app-id", providers.EnvGitHubAppID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// TestImageBuildCatalogReadsTheBitbucketTokenFromTheSecret: `fugaro image
// build <entry>` on a Bitbucket repository copies the entry's Dockerfile with
// the project's existing credential, read from where it actually is -- the
// project's bitbucket-token secret -- when the operator's shell has no
// FUGARO_BITBUCKET_TOKEN, which is every shell. Before this the command
// stopped at "opening the provider of acme/app: FUGARO_BITBUCKET_TOKEN is not
// set" before anything was billed, and the fix the operator was left with was
// exporting the token by hand for a command whose cloud runs already have it.
//
// The value is asserted to reach the provider's environment and nowhere else:
// neither stream this command writes carries it, and the one secret the fake
// was asked for is the token's own.
func TestImageBuildCatalogReadsTheBitbucketTokenFromTheSecret(t *testing.T) {
	fb, gcs, f := catalogBuildCheckoutFor(t, bitbucketYAML, catalogBuildYAML, gitprov.KindBitbucket, "")
	catalogClearProviderEnv(t)
	token := []byte(catalogTokenPlaceholder)
	tokenID := putSecretID(t, f.sm, mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret, token)
	f.sm.AllowAccess()
	env := catalogCopyProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

	out, stderr, err := executeBuild(t, "image", "build", "base", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if got := envValue(*env, providers.EnvBitbucketToken); got != string(token) {
		t.Errorf("the environment does not carry the token the project's %s secret holds", config.BitbucketSecret)
	}
	if accessed := f.sm.Accessed(); len(accessed) != 1 || accessed[0] != tokenID {
		t.Errorf("the secrets whose values were read are %v, want exactly %s", accessed, tokenID)
	}
	if got, _ := catalogCopy(t, gcs, "base"); got != catalogBuildFetched {
		t.Errorf("the copy is %q, want the definition the repository holds", got)
	}
	if !strings.Contains(out, "catalog-base@sha256:") {
		t.Errorf("stdout = %q, want the built image and its digest", out)
	}
	if subs := catalogBuildSubs(t, fb); subs["_GIT_USER"] != "x-token-auth" {
		t.Errorf("_GIT_USER = %v, want the Bitbucket clone's own user", subs["_GIT_USER"])
	}
	// The value reaches the provider and nothing else: not the built image
	// line, not a warning, not the run's log.
	for _, stream := range []string{out, stderr} {
		if strings.Contains(stream, string(token)) {
			t.Errorf("a stream carries the token: %q", stream)
		}
	}
}

// TestImageBuildCatalogBitbucketWithoutAToken: with no token in the
// environment and none readable from the project's secret, the provider's own
// failure is left exactly as it was -- and one sentence says where the token
// belongs and what reading it needs, so the operator is not left exporting it
// by hand.
func TestImageBuildCatalogBitbucketWithoutAToken(t *testing.T) {
	_, _, f := catalogBuildCheckoutFor(t, bitbucketYAML, catalogBuildYAML, gitprov.KindBitbucket, "")
	catalogClearProviderEnv(t)
	// The container exists and has no version: what the first apply of a
	// project that never stored the token leaves behind.
	f.sm.Seed(gcp.SecretID(mustSlug(gitprov.KindBitbucket, "acme/app"), config.BitbucketSecret), nil, nil)
	f.sm.AllowAccess()
	_, _, err := executeBuild(t, "image", "build", "base", "--base", "b:1")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: a token that cannot be read is a user error", ExitCode(err), err)
	}
	if want := providers.EnvBitbucketToken + " is not set: the provider needs a Bitbucket access token"; !strings.Contains(err.Error(), want) {
		t.Errorf("the failure no longer carries the provider's own sentence %q: %v", want, err)
	}
	for _, want := range []string{config.BitbucketSecret, gcp.SecretAccessorRole} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %s: %v", want, err)
		}
	}
}
