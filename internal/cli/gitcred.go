package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/task"
)

// The variables `fugaro image git-credential` reads its secrets from, as
// the build's credential step sets them. They are never flags, so a
// secret never reaches an argv.
const (
	gitCredTokenEnv  = "GIT_TOKEN"      // Bitbucket: the repository access token
	gitCredUserEnv   = "GIT_USER"       // Bitbucket: the token's https username
	gitCredAppKeyEnv = "GITHUB_APP_KEY" // GitHub: the App's private key, PEM
	gitCredAppIDEnv  = "GITHUB_APP_ID"  // GitHub: the App's ID
)

// gitCredGitHubAPI is the GitHub API the credential step mints its token
// at. The step holds the App's private key, so its JWT goes only to
// GitHub's own API: no flag or variable changes this; only tests do.
var gitCredGitHubAPI = github.DefaultBaseURL

type gitCredentialOptions struct {
	provider, repoURL, out string
	// The entry mode: one credential for every repository the catalog entry
	// being built bakes, asked of the credentials service's build route.
	entry, buildID string
	repos          []string
	ttlSeconds     int
}

// gitCredBuildToken is where the entry mode gets the credentials service's build
// route (internal/buildtoken's Client). It is nil in this tree — the route is
// B1 and the client that talks to it is I2's, so the two halves of the contract
// are built side by side and joined there — and the entry mode refuses when it
// is nil rather than falling back to anything: the credential a bake clones with
// is the project's build identity's and it reaches exactly the entry's
// repositories, which is not something this process can mint for itself.
//
// A test installs buildtoken.Stub here to pin what the build asks for; nothing
// that ships ever sets it.
var gitCredBuildToken func(cmd *cobra.Command) (buildtoken.Client, error)

// newImageGitCredentialCmd is `fugaro image git-credential`, the image
// build's credential step: it writes one git credential-store line for the
// repository's clone URL into --out, mode 0600, and prints nothing. For
// GitHub it mints an installation token that can only read the one
// repository. It is hidden because the build is its only caller.
//
// With --entry it writes the one credential a catalog build clones every
// repository of the entry being built with (D2301), asked of the credentials
// service's build route as one request for that entry — the service reads the
// repositories a build may be given from the published image map for it
// (internal/buildtoken, contract-build-token.md rule 8) — and the credential
// covers the answer's own repositories and no others.
func newImageGitCredentialCmd() *cobra.Command {
	var o gitCredentialOptions
	cmd := &cobra.Command{
		Use:    "git-credential",
		Short:  "Write the image build's git credential (used by Cloud Build)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return runGitCredential(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.provider, "provider", "", "git provider: bitbucket or github")
	f.StringVar(&o.repoURL, "repo-url", "", "the https clone URL, without credentials")
	f.StringVar(&o.out, "out", "", "the credential-store file to write")
	f.StringVar(&o.entry, "entry", "", "the catalog entry being built: ask the credentials service for one credential over its repositories")
	f.StringVar(&o.buildID, "build-id", "", "the build this request is made for (BUILD_ID in a Cloud Build step)")
	f.StringSliceVar(&o.repos, "repos", nil, "the repositories the credential must cover, by canonical name (owner/name)")
	f.IntVar(&o.ttlSeconds, "ttl-seconds", 0, "how long this build needs the credential, in seconds")
	return cmd
}

func runGitCredential(cmd *cobra.Command, o gitCredentialOptions) error {
	if o.entry != "" {
		return runGitCredentialForEntry(cmd, o)
	}
	if o.buildID != "" || len(o.repos) > 0 || o.ttlSeconds != 0 {
		return userErr("--build-id, --repos and --ttl-seconds are the entry mode's, and --entry is not set")
	}
	if o.out == "" {
		return userErr("--out is required")
	}
	if err := gcp.CheckRepoURL(o.provider, o.repoURL); err != nil {
		return userErr("%v", err)
	}
	u, err := url.Parse(o.repoURL)
	if err != nil {
		return userErr("the repository URL %s does not parse", gcp.RedactURL(o.repoURL))
	}
	var user, token string
	switch o.provider {
	case gitprov.KindBitbucket:
		user, token = os.Getenv(gitCredUserEnv), os.Getenv(gitCredTokenEnv)
		if user == "" || token == "" {
			return userErr("%s and %s must both be set", gitCredUserEnv, gitCredTokenEnv)
		}
	case gitprov.KindGitHub:
		if token, err = mintBuildToken(cmd, u); err != nil {
			return err
		}
		user = github.GitUsername
	default:
		return userErr("--provider %q is not %s or %s", o.provider, gitprov.KindBitbucket, gitprov.KindGitHub)
	}
	// git refuses a credential holding a newline or another control
	// character, so the clone would run without one.
	if strings.ContainsFunc(user+token, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return userErr("the git credential holds a control character")
	}
	line := "https://" + credEscape(user) + ":" + credEscape(token) + "@" + u.Host + "\n"
	return writeCredential(o.out, line)
}

// runGitCredentialForEntry is the catalog build's half of the build route
// (D2301, F19): one request for the entry being built, naming the repositories
// the bake composes stages for, and one credential written from the answer.
//
// Every value it sends is one the build itself holds, and none of them widens
// the request: the entry is the entry this build composes a definition for, the
// build id is the build's own name, the repositories are the ones the bake plan
// ordered, and the lifetime is how long the step needs to clone. What the
// credential really covers is the service's answer, and this step takes it as it
// comes — a request naming a repository the entry's own map does not put on it
// is refused there, by name (B1).
func runGitCredentialForEntry(cmd *cobra.Command, o gitCredentialOptions) error {
	if o.out == "" {
		return userErr("--out is required")
	}
	if o.repoURL != "" {
		return userErr("--repo-url is the one-repository mode's, and --entry asks for the entry's repositories")
	}
	if o.buildID == "" {
		return userErr("--build-id is required with --entry")
	}
	if o.ttlSeconds <= 0 {
		return userErr("--ttl-seconds is required with --entry: the build has to say how long it needs the credential")
	}
	if !config.ValidCatalogName(o.entry) {
		return userErr("--entry %s %s", pluginwire.Printable(o.entry), config.CatalogNameMessage)
	}
	if len(o.repos) == 0 {
		return userErr("--repos is required with --entry: the credential covers exactly the repositories the bake composes stages for")
	}
	// The repositories are held to their canonical form here, the key space the
	// service reads the entry's own from, so what the request names is a
	// repository rather than a value a config left in a list. Sorted and
	// deduplicated so two spellings of one repository are one name.
	repos := make([]string, 0, len(o.repos))
	for _, r := range o.repos {
		canonical, err := task.CanonicalRepo(r)
		if err != nil {
			return userErr("--repos %s: %v", pluginwire.Printable(r), err)
		}
		repos = append(repos, canonical)
	}
	repos = slices.Compact(slices.Sorted(slices.Values(repos)))
	switch o.provider {
	case gitprov.KindGitHub, gitprov.KindBitbucket:
	default:
		return userErr("--provider %q is not %s or %s", o.provider, gitprov.KindBitbucket, gitprov.KindGitHub)
	}
	if gitCredBuildToken == nil {
		return userErr("this build cannot ask for the entry's credential: the credentials service's build route is not wired into this binary yet")
	}
	client, err := gitCredBuildToken(cmd)
	if err != nil {
		return remote(err)
	}
	resp, err := client.Mint(cmd.Context(), buildtoken.Request{
		Entry: o.entry, BuildID: o.buildID, Provider: o.provider,
		Repositories: repos, TTLSeconds: o.ttlSeconds,
	})
	if err != nil {
		// The service's refusal is passed on as it is, and it names the
		// repository it refused rather than anything this step would widen to:
		// a client that retried, or fell back to a credential of its own, would
		// be the reach this contract exists to bound (B1, C2).
		return remote(err)
	}
	return writeBuildToken(o.out, o.provider, resp)
}

// writeBuildToken writes the credential the build route answered with, or
// refuses the answer.
//
// Only the GitHub half is a credential this step can write: the answer's token
// is written for the one host the answer itself names, held to the provider's
// own host, and nothing else of the answer reaches the file. A Bitbucket build
// is answered a proxy grant and no token (R6), and there is nothing here to
// write for it — the generated clone step consumes the grant (B8) — so a grant
// is refused rather than turned into a credential file that would hold a
// placeholder. An answer that is neither, or both, is refused: the contract
// says exactly one, and a step that guessed would be the widening it exists to
// prevent.
func writeBuildToken(out, provider string, resp buildtoken.Response) error {
	switch {
	case resp.Token != nil && resp.Proxy != nil:
		return userErr("the credentials service answered a token and a proxy grant, and the contract says exactly one")
	case resp.Proxy != nil:
		return userErr("this build is answered a Bitbucket proxy grant and no token: the generated clone step consumes the grant, so there is no git credential to write here")
	case resp.Token == nil:
		return userErr("the credentials service answered neither a token nor a proxy grant")
	}
	t := resp.Token
	// The host is the answer's own, held to the provider the credential was
	// minted at: a credential file is read by git for the host in its line, so
	// an answer naming another host would hand this token to a server that
	// never minted it. It is not the request's provider that decides — the
	// answer would then be able to redirect its own token — and it is not
	// checked against a URL either, because the build's clone URLs are the
	// service's business and not this file's.
	if host := repourl.ProviderHost(provider); t.Host == "" || t.Host != host {
		return userErr("the credentials service answered a credential for %s, and this build's provider %s clones from %s", pluginwire.Printable(t.Host), provider, host)
	}
	if t.User == "" || t.Value == "" {
		return userErr("the credentials service answered an incomplete credential")
	}
	// git refuses a credential holding a newline or another control character,
	// so the clone would run without one. The token is never printed: a refusal
	// that quoted it would put it in the build log.
	if strings.ContainsFunc(t.User+t.Value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return userErr("the credentials service's credential holds a control character")
	}
	return writeCredential(out, "https://"+credEscape(t.User)+":"+credEscape(t.Value)+"@"+t.Host+"\n")
}

// mintBuildToken mints the GitHub installation token for the repository at
// u, which can only read that repository.
func mintBuildToken(cmd *cobra.Command, u *url.URL) (string, error) {
	owner, repo, ok := gitprov.SplitRepo(strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"))
	if !ok {
		return "", userErr("the repository URL %s does not name owner/repository", gcp.RedactURL(u.String()))
	}
	appID, pemKey := os.Getenv(gitCredAppIDEnv), os.Getenv(gitCredAppKeyEnv)
	if appID == "" || pemKey == "" {
		return "", userErr("%s and %s must both be set", gitCredAppIDEnv, gitCredAppKeyEnv)
	}
	key, err := github.ParsePrivateKey([]byte(pemKey))
	if err != nil {
		return "", userErr("%s: %v", gitCredAppKeyEnv, err)
	}
	token, _, err := github.MintInstallationToken(cmd.Context(), github.Options{
		Owner: owner, Repo: repo, AppID: appID, PrivateKey: key, BaseURL: gitCredGitHubAPI,
	}, github.BuildTokenPermissions())
	if err != nil {
		return "", remote(err)
	}
	return token, nil
}

// credEscape percent-encodes every byte of s but the unreserved ones, as
// git's credential-store reads a URL's user and password.
func credEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// writeCredential writes line to path, mode 0600 even when the file was
// already there with a looser one.
//
// The mode is the POSIX half of what keeps the line — a credential the build
// clones with — to its owner, and it is not claimed to protect it on Windows:
// Go's os.Chmod there sets the read-only attribute and nothing else, so what
// keeps another local user out of the file is the ACL of the directory it is
// written into. **D12 (file modes) is open** in
// docs/windows-cli/reconciliation.md, the Windows CLI gets its release in
// **Slice 5**, and docs/windows-cli/README.md records that a Windows release
// must not ship without that decision. The long form of this note is at the
// deployer credential in internal/cli/managed_apply.go.
func writeCredential(path, line string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return userErr("writing the git credential: %v", err)
	}
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.WriteString(line)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(path)
		return userErr("writing the git credential: %v", err)
	}
	return nil
}
