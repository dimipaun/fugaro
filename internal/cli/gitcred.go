package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
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

	// The three a build that fetches its credentials from the installation's
	// credentials service is handed instead (B4.5, envCredentialsServiceURL for
	// the address): the workflow whose secrets the build mounts, the variables
	// it mounts them under, and the volume the fetched values are left in for
	// the build step. They are variables and not flags for the reason above,
	// and because the credential step has to go on running on a base image
	// whose fugaro predates this: an older binary ignores what it does not
	// read, while an unknown flag would be refused before it ran at all.
	gitCredWorkflowEnv   = "WORKFLOW"
	gitCredSecretEnvsEnv = "SECRET_ENVS"
	gitCredSecretDirEnv  = "SECRET_DIR"
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
// route (internal/buildtoken's Client), and what ships is the call itself
// (gitcred_build.go, I2). The entry mode refuses when the seam is nil rather
// than falling back to anything: the credential a bake clones with is the
// project's build identity's and it reaches exactly the entry's repositories,
// which is not something this process can mint for itself.
//
// A test installs a client that records the request here, so what the build asks
// for is pinned rather than assumed, and only a test ever leaves it nil.
var gitCredBuildToken func(cmd *cobra.Command) (buildtoken.Client, error) = newBuildTokenClient

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
	// A repository the installation's credentials service serves (B4.5): the
	// credential comes from the service and no longer from this step's
	// environment, which is empty for it once the mounts are dropped (B4.9).
	// The switch is the address and nothing else, and it is read rather than
	// derived: an installation whose service is not deployed sets nothing, and
	// the step below is then exactly what it was.
	if raw := os.Getenv(envCredentialsServiceURL); raw != "" {
		return fetchBuildCredentials(cmd, o, u, raw)
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

// fetchBuildCredentials is the credential step of a build whose repository the
// installation's credentials service serves (B4.5): one call to B4.3's build
// route, and the two things the build needs written from its answer — the git
// credential its clone reads, and the workflow secrets its build step mounts.
//
// Everything this step holds is a name: the provider it was asked for, the URL
// it clones from, the workflow, the variables and, for GitHub, the App's id,
// which is not a secret. The credential itself is the answer's, and that is the
// point of the route: the deployment binds a build account to one repository,
// and the service is what decides what that repository's build is served (G20).
//
// It fails closed: a build whose fetch is refused is a build with no credential
// at all, which is the state its clone step fails in rather than one it falls
// back from to a secret of its own.
func fetchBuildCredentials(cmd *cobra.Command, o gitCredentialOptions, u *url.URL, raw string) error {
	service, err := checkBuildServiceURL(raw)
	if err != nil {
		return userErr("%s: %v", envCredentialsServiceURL, err)
	}
	workflow := os.Getenv(gitCredWorkflowEnv)
	if !config.WorkflowNameRE.MatchString(workflow) {
		return userErr("%s must name the workflow this build's secrets belong to (the build's own _WORKFLOW), and it is %s",
			gitCredWorkflowEnv, pluginwire.Printable(workflow))
	}
	secrets, err := buildSecretEnvs()
	if err != nil {
		return err
	}
	dir := ""
	if len(secrets) > 0 {
		if dir = os.Getenv(gitCredSecretDirEnv); dir == "" {
			return userErr("%s must name the directory the build step reads its workflow secrets from, and this build mounts %s",
				gitCredSecretDirEnv, strings.Join(secrets, " "))
		}
	}
	var appID string
	if o.provider == gitprov.KindGitHub {
		// The App's id is the one thing this step holds and the service does
		// not — its bundle holds the key — and the token this build clones with
		// is minted as that App.
		if appID = os.Getenv(gitCredAppIDEnv); appID == "" {
			return userErr("%s must be set: the GitHub installation token this build clones with is minted as the App, and the service's bundle holds its key and not its id", gitCredAppIDEnv)
		}
	}
	ans, err := askBuildCredentials(cmd.Context(), service, credsvc.BuildCredentialsRequest{
		Workflow: workflow, Secrets: secrets, AppID: appID,
	})
	if err != nil {
		return err
	}
	// Every refusal is taken BEFORE anything is written, so a refused answer
	// leaves no half of a build's credentials behind.
	user, token, err := servedGitCredential(o.provider, u, ans.Git)
	if err != nil {
		return err
	}
	if err := writeBuildSecrets(dir, secrets, ans.Secrets); err != nil {
		return err
	}
	return writeCredential(o.out, "https://"+credEscape(user)+":"+credEscape(token)+"@"+u.Host+"\n")
}

// buildSecretEnvs is the workflow-secret variables this build mounts, read from
// the step's own environment (gitCredSecretEnvsEnv, the template's
// _SECRET_ENVS): the same space-separated list the build step mounts, so what
// this fetches and what that hands BuildKit cannot be two different answers.
//
// A name is held to the shape a job could mount a secret under (config.EnvNameRE,
// the rule the record's own entries carry) and the same name twice is refused:
// what the build mounts is a set, and the route refuses both by name — a build
// that learned it here failed without a round trip and without an audit line for
// a call it should not have made.
func buildSecretEnvs() ([]string, error) {
	fields := strings.Fields(os.Getenv(gitCredSecretEnvsEnv))
	if len(fields) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(fields))
	for _, env := range fields {
		if !config.EnvNameRE.MatchString(env) {
			return nil, userErr("%s names %s, which is not an environment variable a workflow secret is mounted under (upper-case letters, digits and '_', starting with a letter or '_')",
				gitCredSecretEnvsEnv, pluginwire.Printable(env))
		}
		if seen[env] {
			return nil, userErr("%s names %s twice, and the secrets this build mounts are a set", gitCredSecretEnvsEnv, pluginwire.Printable(env))
		}
		seen[env] = true
	}
	return fields, nil
}

// servedGitCredential is the username and the token a build clones with, from
// the service's answer, held to what the build itself is doing.
//
// The provider the answer echoes has to be the one this build clones from, and
// the host it names has to be the host of the URL this build clones: git reads
// a credential for the host it was asked about, so an answer naming another host
// would hand this token to a server that never minted it, and a GitHub token
// answered to a Bitbucket build would be read as a Bitbucket credential.
//
// The token reaches no refusal and no error: a build's log is read by everyone.
func servedGitCredential(provider string, u *url.URL, cred credsvc.BuildGitCredential) (user, token string, err error) {
	if cred.Provider != provider {
		return "", "", userErr("the credentials service answered a credential for the provider %s, and this build clones from %s",
			pluginwire.Printable(cred.Provider), provider)
	}
	if cred.Host != u.Host {
		return "", "", userErr("the credentials service answered a credential for the host %s, and this build clones from %s",
			pluginwire.Printable(cred.Host), pluginwire.Printable(u.Host))
	}
	if cred.Username == "" || cred.Token == "" {
		return "", "", userErr("the credentials service answered an incomplete credential")
	}
	// git refuses a credential holding a newline or another control character,
	// so the clone would run without one.
	if strings.ContainsFunc(cred.Username+cred.Token, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", "", userErr("the credentials service's credential holds a control character")
	}
	return cred.Username, cred.Token, nil
}

// writeBuildSecrets leaves the workflow secrets the service answered in the
// volume the build step reads (SECRET_DIR), one file per variable, mode 0600.
//
// The answer has to be the one this build asked for: a variable it does not
// mount, a variable answered twice, or fewer variables than it mounts is
// refused whole, and nothing is written. The build step mounts exactly the
// variables its own list names and fails a build whose file is not there, so a
// partial answer would be a build that fails after half of its secrets were
// handed over — the state this refuses to reach.
func writeBuildSecrets(dir string, asked []string, answered []credsvc.BuildSecretValue) error {
	want := make(map[string]bool, len(asked))
	for _, env := range asked {
		want[env] = true
	}
	seen := make(map[string]bool, len(answered))
	for _, s := range answered {
		if !want[s.Env] {
			return userErr("the credentials service answered a secret under %s, which this build does not mount", pluginwire.Printable(s.Env))
		}
		if seen[s.Env] {
			return userErr("the credentials service answered %s twice", pluginwire.Printable(s.Env))
		}
		seen[s.Env] = true
	}
	if len(seen) != len(want) {
		return userErr("the credentials service answered %d of the %d build secrets this build mounts", len(seen), len(want))
	}
	for _, s := range answered {
		if err := writeBuildSecret(dir, s.Env, s.Value); err != nil {
			return err
		}
	}
	return nil
}

// writeBuildSecret writes one fetched workflow secret into the volume, mode
// 0600 and exactly the value's bytes.
//
// No newline is added and none is stripped: the build step hands BuildKit the
// same bytes whether it wrote them from its own environment or took this file,
// and a value that gained a trailing newline here would be a secret the image
// carries differently from every other build of it.
func writeBuildSecret(dir, env, value string) error {
	// The name is one config.EnvNameRE admitted, so it names a file in dir and
	// cannot name a path out of it.
	path := filepath.Join(dir, env)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return userErr("writing the build secret for %s: %v", env, err)
	}
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.WriteString(value)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(path)
		return userErr("writing the build secret for %s: %v", env, err)
	}
	return nil
}

// askBuildCredentials makes the one call this step makes: the build route's
// path, the contract's request, and the build's own identity token as the
// bearer (buildIdentityToken: Cloud Run IAM admits the call with it and the
// route verifies its caller by it). Nothing is retried — a refusal is the
// service's decision and asking again answers the same thing — and no redirect
// is followed, because the header carries that token.
func askBuildCredentials(ctx context.Context, service string, req credsvc.BuildCredentialsRequest) (credsvc.BuildCredentialsResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return credsvc.BuildCredentialsResponse{}, errors.New("the build's credential request could not be built")
	}
	tok, err := buildIdentityToken(ctx, service)
	if err != nil {
		return credsvc.BuildCredentialsResponse{}, fmt.Errorf("the build could not present its own identity to the credentials service: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, service+credsvc.BuildCredentialsPath, bytes.NewReader(body))
	if err != nil {
		return credsvc.BuildCredentialsResponse{}, errors.New("the build's credential request could not be built")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	resp, err := buildTokenHTTPClient().Do(httpReq)
	if err != nil {
		// The body sent holds no credential — a workflow, variables and an App
		// id — and no answer of this contract's exists yet.
		return credsvc.BuildCredentialsResponse{}, fmt.Errorf("the credentials service could not be reached at %s", service)
	}
	defer resp.Body.Close()
	limit := buildCredentialsAnswerLimit(len(req.Secrets))
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return credsvc.BuildCredentialsResponse{}, errors.New("the credentials service's answer could not be read")
	}
	if int64(len(data)) > limit {
		return credsvc.BuildCredentialsResponse{}, fmt.Errorf("the credentials service's answer is larger than the %d bytes this call reads", limit)
	}
	if resp.StatusCode != http.StatusOK {
		return credsvc.BuildCredentialsResponse{}, buildCredentialsRefusal(service, resp.StatusCode, data)
	}
	var ans credsvc.BuildCredentialsResponse
	if err := decodeBuildCredentialsAnswer(data, &ans); err != nil {
		return credsvc.BuildCredentialsResponse{}, err
	}
	return ans, nil
}

// buildCredentialsAnswerLimit bounds the answer this step reads, from the
// request it made: the git credential is one token, and the answer carries one
// workflow secret per variable this build mounts, each of which is at most
// Secret Manager's payload (maxSecretBytes) with room for the JSON string that
// carries it — six times the bytes it holds is the most a JSON encoder can need
// for one, a control byte being \u00XX — and for the envelope around it.
func buildCredentialsAnswerLimit(asked int) int64 {
	return maxBuildTokenAnswerBytes + int64(asked)*(6*maxSecretBytes+256)
}

// buildCredentialsRefusal turns a non-200 answer into this step's refusal: the
// route's own typed body where the answer is one (credsvc.ReadWireError), and
// otherwise the status alone.
//
// The status is not decoration: a call Cloud Run's IAM refused before the route
// saw it — a build account not granted the invoker, an identity token minted
// for another service — answers 403 with no body of this contract's, and an
// operator who read only "refused" would look in the route for a fault that is
// in the deployment. Nothing here repeats the body: the refusal's own message
// is the service's, held to one printable line by the reader that reads it.
func buildCredentialsRefusal(serviceURL string, status int, body []byte) error {
	if e, err := credsvc.ReadWireError(status, body); err == nil {
		msg := fmt.Sprintf("the installation's credentials service refused this build's credentials (%s, %d)", oneLine(e.Error), status)
		if e.Message != "" {
			msg += ": " + oneLine(e.Message)
		}
		return errors.New(msg)
	}
	if status == http.StatusForbidden {
		return fmt.Errorf("the credentials service at %s refused this build before its route saw it (%d): the build's service account needs the invoker role on the service, and the identity token it presents has to be minted for the service's own URL", serviceURL, status)
	}
	return fmt.Errorf("the installation's credentials service refused this build's credentials (%d) and its answer is not one this release reads", status)
}

// decodeBuildCredentialsAnswer reads the route's answer: exactly one JSON
// object of the contract's fields, and nothing after it. A field this release
// does not know is refused rather than ignored (Convention 6, credsvc's own
// decodeStrict), because a later service's answer read generously is a
// credential read out of a document this release does not understand.
//
// No error here repeats what it read: the answer holds the token and the
// secrets, and a decoder's own message names the byte it choked on.
func decodeBuildCredentialsAnswer(raw []byte, out *credsvc.BuildCredentialsResponse) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errors.New("the credentials service's answer is not a build credentials answer")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the credentials service's answer carries more than one document")
	}
	return nil
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
		return userErr("this build cannot ask for the entry's credential: no route to the credentials service is installed in this binary")
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
// write for it — a grant is spent by a clone that rewrites its own git traffic
// to the proxy, which this build's clone step does not — so a grant is refused
// rather than turned into a credential file that would hold a placeholder. An
// answer that is neither, or both, is refused: the contract says exactly one,
// and a step that guessed would be the widening it exists to prevent.
func writeBuildToken(out, provider string, resp buildtoken.Response) error {
	switch {
	case resp.Token != nil && resp.Proxy != nil:
		return userErr("the credentials service answered a token and a proxy grant, and the contract says exactly one")
	case resp.Proxy != nil:
		return userErr("the credentials service answered this Bitbucket build a proxy grant and no token, and a token is all this step writes: a grant is spent by rewriting the clone's git traffic to the proxy it names and presenting the build's own identity there, and this build's generated clone step does neither. Nothing is written, so the clone that follows fails and this build is refused rather than cloning with a credential this step invented")
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
// already there with a looser one, and reads it back: a credential step that
// wrote none has to fail here, where the step that owed the credential is
// named, rather than leave a build to clone with an empty file and answer
// `could not read Username for 'https://github.com'` — a sentence naming
// neither the credential nor the build that failed to write it (check 390).
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
	return credentialLanded(path, line)
}

// credentialLanded is what the file a credential step wrote holds, read back
// and held to the line the step wrote. It is the one way that step can know it
// wrote none: the volume a build mounts is not this step's to read, so a file
// that kept nothing is a build that clones with an empty credential and answers
// `could not read Username for 'https://github.com'` — a sentence naming
// neither the credential nor the build that failed to write it (check 390).
func credentialLanded(path, line string) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return userErr("reading back the git credential: %v", err)
	}
	if string(got) != line {
		return userErr("the git credential %s holds no credential after writing it: a build mounting it would clone with none", path)
	}
	return nil
}
