package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
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

type gitCredentialOptions struct{ provider, repoURL, out string }

// newImageGitCredentialCmd is `fugaro image git-credential`, the image
// build's credential step: it writes one git credential-store line for the
// repository's clone URL into --out, mode 0600, and prints nothing. For
// GitHub it mints an installation token that can only read the one
// repository. It is hidden because the build is its only caller.
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
	return cmd
}

func runGitCredential(cmd *cobra.Command, o gitCredentialOptions) error {
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
