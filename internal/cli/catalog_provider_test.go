package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
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

// catalogProviderEnvRig is the state catalogProviderEnv is called with: a
// GitHub repository of a project whose config records an App ID for it, and
// one Dockerfile entry that opens the provider.
func catalogProviderEnvRig(t *testing.T) (*localcfg.Config, infra.RepoSpec, []config.Entry) {
	t.Helper()
	lc := &localcfg.Config{
		Name: "aurora", GCPProject: "proj-1234",
		Repos: map[string]localcfg.Repo{"acme/app": {Provider: gitprov.KindGitHub, GitHubAppID: catalogBuildAppID}},
	}
	rs := infra.RepoSpec{
		Name: "acme/app", Slug: mustSlug(gitprov.KindGitHub, "acme/app"), Provider: gitprov.KindGitHub,
		BuildServiceAccountEmail: "fugaro-build-" + mustSlug(gitprov.KindGitHub, "acme/app") + "@proj-1234.iam.gserviceaccount.com",
	}
	entries := []config.Entry{{Name: "base", Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/app", Path: ".fugaro/base.Dockerfile"}}}
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
	for _, k := range []string{providers.EnvGitHubAppID, providers.EnvGitHubAppKey, providers.EnvGitHubAppKeyFile, providers.EnvCredentialsServiceURL} {
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
	wasID, wasRead := catalogAppKeySecretID, readAppKey
	catalogAppKeySecretID = func(context.Context, *localcfg.Config, infra.RepoSpec) (string, error) {
		*resolves++
		return gcp.SecretID(mustSlug(gitprov.KindGitHub, "acme/app"), config.GitHubAppKeySecret), nil
	}
	readAppKey = func(context.Context, *localcfg.Config, string) ([]byte, error) {
		*reads++
		return append([]byte(nil), pem...), err
	}
	t.Cleanup(func() { catalogAppKeySecretID, readAppKey = wasID, wasRead })
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
	// this ID ever gets here -- and neither is a flag this command takes.
	_, err := catalogGitHubAppID(&localcfg.Config{Name: "aurora", GCPProject: "proj-1234"}, "acme/app", func(string) string { return "" })
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: no App ID anywhere is a user error", ExitCode(err), err)
	}
	for _, want := range []string{"repos.acme/app.github_app_id", providers.EnvGitHubAppID} {
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
		env, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv)
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

	t.Run("the repository's secret holds the key", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		key := appKey(t)
		resolves, reads := catalogStubKeyRead(t, key, nil)
		var notice bytes.Buffer
		env, err := catalogProviderEnv(t.Context(), &notice, lc, rs, entries, os.Getenv)
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
		env, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv)
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

	t.Run("a coding agent's session reads nothing", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		resolves, reads := catalogStubKeyRead(t, appKey(t), nil)
		t.Setenv("CLAUDECODE", "1")
		env, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv)
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
		env, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv)
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

	t.Run("a bitbucket repository is left alone", func(t *testing.T) {
		catalogClearProviderEnv(t)
		lc, rs, entries := catalogProviderEnvRig(t)
		rs.Provider = gitprov.KindBitbucket
		resolves, reads := catalogStubKeyRead(t, appKey(t), nil)
		env, err := catalogProviderEnv(t.Context(), &bytes.Buffer{}, lc, rs, entries, os.Getenv)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := envHas(env, providers.EnvGitHubAppID); ok {
			t.Errorf("%s was set for a Bitbucket repository", providers.EnvGitHubAppID)
		}
		if *resolves != 0 || *reads != 0 {
			t.Errorf("the App key was read (%d resolutions, %d reads)", *resolves, *reads)
		}
	})
}

// catalogGitHubProvider is the copy's opener for a GitHub checkout: the REAL
// OpenProvider, so the environment this command built is what decides whether
// the copy can read anything at all, handing back a file-backed fake for the
// read itself, which is the network no test has. It answers the environment
// the command built, for the test to look at.
func catalogGitHubProvider(t *testing.T, files map[string]string) *[]string {
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
	env := catalogGitHubProvider(t, map[string]string{".fugaro/base.Dockerfile": catalogBuildFetched})

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
	for _, want := range []string{"repos.acme/app.github_app_id", providers.EnvGitHubAppID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}
