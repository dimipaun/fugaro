package cli

// The entry mode's client (I2): `fugaro image git-credential --entry` is the
// catalog build's credential step, and this is what makes its call. The step
// holds no credential of its own to clone an entry's repositories with — one
// build bakes several repositories (D2301) and the credential that covers them
// is minted for the build of the entry's own repositories and for nothing else
// (F19, R6) — so it asks the installation's credentials service for one and
// writes what it is answered (internal/buildtoken, contract-build-token.md).
//
// Four rules shape everything below, and they are the route's own read from the
// caller's side (internal/credsvc/buildtoken.go is the other side of each).
//
//   - The build presents its own identity. The step runs as the project's one
//     build identity (B5, B6), and what it presents is that identity's Google
//     ID token for the service's URL — the credential Cloud Run IAM admits the
//     call with (D135) and the one the route verifies its caller by. It is
//     minted at the metadata server, per call, and never cached: the platform's
//     link-local endpoint is a millisecond against the build's minutes, and
//     what a cache would buy is a token this step keeps presenting after the
//     platform stopped issuing it.
//   - Nothing here narrows and nothing here widens. The request is the entry,
//     the build, the provider, the repositories the bake plan composed stages
//     for and how long the step needs them, in the contract's own fields: which
//     of those it may really be given is the service's answer from the
//     published image map (rule 8), so a client that filtered the list, or
//     retried with a wider one, would be the reach this contract exists to
//     bound.
//   - It fails closed. A build with no service address, no identity, no
//     answer this contract's, or a refusal, fails where it stands and writes no
//     credential file; the alternative is a bake that clones with something
//     this process held, which is a credential the project's build identity
//     never granted (runGitCredentialForEntry writes nothing when this returns
//     an error).
//   - No value reaches a log. The request holds an entry, a build id and
//     repository names; the answer holds the token, and nothing here prints,
//     wraps or logs it. A refusal names the field it refused and never a
//     content — which is why the refusal below is the service's own sentence
//     read through credsvc.ReadWireError, which holds it to one printable line
//     and a length no refusal of this service exceeds.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The build's own identity, and where the platform serves it: the metadata
// server's endpoint for the instance's default service account, for the
// audience the call is admitted by. The host variable is the platform's own
// knob — google.golang.org's client libraries read the same one for the same
// reason, and the job's environment is where the platform puts it — so a test
// points it at a stand-in with no test-only switch in this file.
const (
	buildMetadataHostEnv      = "GCE_METADATA_HOST"
	defaultBuildMetadataHost  = "metadata.google.internal"
	buildMetadataIdentityPath = "/computeMetadata/v1/instance/service-accounts/default/identity"
	// maxBuildIDTokenBytes bounds a token this step will present, and is
	// internal/credsvc's own bound on one it will read (auth.go's
	// maxTokenBytes): longer than it, or not a single clean value, is refused
	// here rather than sent to be refused there.
	maxBuildIDTokenBytes = 8 << 10
)

// Builds do not wait: the step's call is one round trip to the installation's
// own service plus one to the metadata server, and a build that has not been
// answered in this long is a build whose clone is not starting. Nothing is
// retried — a refusal is the service's decision and asking again answers the
// same thing (the contract's rule 9).
const (
	buildTokenCallTimeout = 60 * time.Second
	// maxBuildTokenAnswerBytes bounds the answer this step reads. The answer is
	// one token, its user, its host and an expiry, so a body larger than this
	// is not this contract's.
	maxBuildTokenAnswerBytes = 8 << 10
)

// buildTokenClient is the shipped client: the service's address, held to the
// shape an identity token may be sent to, and the client the call is made with.
type buildTokenClient struct {
	url  string
	http *http.Client
}

var _ buildtoken.Client = buildTokenClient{}

// newBuildTokenClient is what the entry mode's seam holds in a shipped binary:
// the credentials service the installation's build records are served by,
// validated here rather than at the call, so a build whose environment names
// nothing is refused before it has composed a request or read an answer.
//
// The address is READ from the build's environment and not derived: Cloud Run
// assigns the service's URL, credentials.tf passes the service resource's own
// uri, and the launch sets the same variable on a repository job. It is the
// same name the service's own configuration reads (envCredentialsServiceURL),
// because the audience a build's identity token is minted for and the audience
// the service admits are one string.
func newBuildTokenClient(*cobra.Command) (buildtoken.Client, error) {
	raw := os.Getenv(envCredentialsServiceURL)
	if raw == "" {
		return nil, userErr("this build cannot ask for the entry's credential: %s is not set, and a catalog build's credential step asks the installation's credentials service for the credential that clones the entry's repositories. The variable is the service's own URL, which the installation's build steps are handed; an installation whose service is not deployed has no entry credential to ask for", envCredentialsServiceURL)
	}
	url, err := checkBuildServiceURL(raw)
	if err != nil {
		return nil, userErr("%s: %v", envCredentialsServiceURL, err)
	}
	return buildTokenClient{url: url, http: buildTokenHTTPClient()}, nil
}

// checkBuildServiceURL refuses an address this step will not send the build's
// own identity token to. The token is a bearer credential, so over plain http
// it would travel in cleartext: the rule is checkIntakeServiceURL's, and the
// loopback case is what lets a test serve this route in its own process.
//
// The refusal carries the shape of what was wrong and never the address: the
// value is the installation's and the sentence a build log keeps.
func checkBuildServiceURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the credentials service's address is not a scheme and a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if ip := net.ParseIP(u.Hostname()); !(ip != nil && ip.IsLoopback()) && u.Hostname() != "localhost" {
			return "", errors.New("the credentials service's address is not https")
		}
	default:
		return "", errors.New("the credentials service's address is not https")
	}
	return u.Scheme + "://" + u.Host, nil
}

// buildTokenHTTPClient is the client the call is made with: it follows no
// redirect, because the request carries the build's own identity token and a
// followed 307 or 308 would carry it to whatever host the Location named
// (secretsIntakeClient refuses them for the same reason), it keeps no cookie
// jar, and it does not wait on an answer forever.
func buildTokenHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return &http.Client{
		Transport:     t,
		Timeout:       buildTokenCallTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Mint asks the installation's credentials service for the credential this
// build clones the entry's repositories with.
//
// The body is the contract's request plus the one field the route's encoding
// adds (credsvc.BuildTokenRequest): the GitHub App ID, which is the one thing
// this step holds and the service does not — the bundle holds the App's key and
// never its id — and which the route refuses a GitHub call without. It is read
// here for the GitHub half alone: a Bitbucket build is answered a grant and no
// token and signs nothing.
func (c buildTokenClient) Mint(ctx context.Context, req buildtoken.Request) (buildtoken.Response, error) {
	var appID string
	if req.Provider == gitprov.KindGitHub {
		appID = os.Getenv(gitCredAppIDEnv)
		if appID == "" {
			return buildtoken.Response{}, userErr("%s must be set: the GitHub installation token the entry's repositories are cloned with is minted as the App, and the service holds its key and not its id", gitCredAppIDEnv)
		}
	}
	body, err := json.Marshal(credsvc.BuildTokenRequest{Request: req, AppID: appID})
	if err != nil {
		return buildtoken.Response{}, errors.New("the credential request could not be built")
	}
	tok, err := buildIdentityToken(ctx, c.url)
	if err != nil {
		return buildtoken.Response{}, fmt.Errorf("the build could not present its own identity to the credentials service: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+credsvc.BuildTokenPath, bytes.NewReader(body))
	if err != nil {
		return buildtoken.Response{}, errors.New("the credential request could not be built")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		// The body sent carries no credential — it names an entry, a build and
		// repositories — and the answer does not exist yet, so nothing of one
		// can be in this reason.
		return buildtoken.Response{}, fmt.Errorf("the credentials service could not be reached at %s", c.url)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBuildTokenAnswerBytes+1))
	if err != nil {
		return buildtoken.Response{}, errors.New("the credentials service's answer could not be read")
	}
	if len(data) > maxBuildTokenAnswerBytes {
		return buildtoken.Response{}, errors.New("the credentials service's answer is larger than this call's")
	}
	// The refusal is the service's decision and asking it again would answer
	// the same thing, so the build stops here rather than retrying or falling
	// back to a credential of its own (rule 9). Its sentence is passed on: it
	// names the repository or the field that was refused, which is what the
	// operator reading this build's log needs, and it is held to one printable
	// line by the reader that reads it.
	if resp.StatusCode != http.StatusOK {
		return buildtoken.Response{}, buildTokenRefusal(c.url, resp.StatusCode, data)
	}
	var ans buildtoken.Response
	if err := decodeBuildTokenAnswer(data, &ans); err != nil {
		return buildtoken.Response{}, err
	}
	return ans, nil
}

// buildTokenRefusal turns a non-200 answer into this step's refusal: the
// route's own typed body where the answer is one (credsvc.ReadWireError), and
// otherwise the status alone.
//
// The typed body is the contract's and the status is not decoration: a call
// Cloud Run's IAM refused before the route ever saw it — a build identity not
// granted the invoker, an audience minted for another service — answers 403
// with no body of this contract's, and an operator who read only "refused"
// would look in the route for a fault that is in the deployment.
func buildTokenRefusal(serviceURL string, status int, body []byte) error {
	if e, err := credsvc.ReadWireError(status, body); err == nil {
		msg := fmt.Sprintf("the installation's credentials service refused the entry's credential (%s, %d)", oneLine(e.Error), status)
		if e.Message != "" {
			msg += ": " + oneLine(e.Message)
		}
		return errors.New(msg)
	}
	if status == http.StatusForbidden {
		return fmt.Errorf("the credentials service at %s refused this build before its route saw it (%d): the build's service account needs the invoker role on the service, and the identity token it presents has to be minted for the service's own URL", serviceURL, status)
	}
	return fmt.Errorf("the installation's credentials service refused the entry's credential (%d) and its answer is not one this release reads", status)
}

// decodeBuildTokenAnswer reads the route's answer: exactly one JSON object of
// the contract's two fields, and nothing after it. A field this release does
// not know is refused rather than ignored (Convention 6, credsvc's own
// decodeStrict), because a later service's answer read generously is a
// credential read out of a document this release does not understand.
//
// No error here repeats what it read: the answer holds a token, and a decoder's
// own message names the byte it choked on.
func decodeBuildTokenAnswer(raw []byte, out *buildtoken.Response) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errors.New("the credentials service's answer is not a build credential's")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the credentials service's answer carries more than one document")
	}
	return nil
}

// buildIdentityToken mints the build identity's Google ID token for audience
// from the metadata server: the credential Cloud Run IAM admits this call for
// run.invoker on the credentials service with (D135). The audience is the
// service's own URL, which is the whole of the check Cloud Run makes and the
// whole of what the route reads the token's claims for.
//
// It is minted per call rather than cached, and the body is never read into an
// error and never logged: on success it is the token, and on failure it is the
// metadata server's own text about a request this code made. Only the status is
// reported. This is the runner's own fetch read for a build (internal/runner's
// metadataIDToken): that one mints a run job's identity from the run's
// environment, and this mints the build's, which is a different principal
// reading a different environment.
func buildIdentityToken(ctx context.Context, audience string) (string, error) {
	host := os.Getenv(buildMetadataHostEnv)
	if host == "" {
		host = defaultBuildMetadataHost
	}
	u := "http://" + host + buildMetadataIdentityPath + "?audience=" + url.QueryEscape(audience)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", errors.New("the metadata request could not be built")
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := buildMetadataClient.Do(req)
	if err != nil {
		return "", errors.New("the metadata server could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the metadata server answered %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBuildIDTokenBytes+1))
	if err != nil {
		return "", errors.New("the metadata server's answer could not be read")
	}
	tok := strings.TrimSpace(string(b))
	if len(tok) > maxBuildIDTokenBytes || !printableBuildToken(tok) {
		return "", errors.New("the metadata server answered with no usable identity token")
	}
	return tok, nil
}

// buildMetadataClient is the client the metadata request is made with: a bound
// on the whole exchange, because the metadata server is the platform's own
// link-local address and an answer that takes longer than this is not one this
// build waits for. Nothing is retried.
var buildMetadataClient = &http.Client{Timeout: 10 * time.Second}

// printableBuildToken reports whether tok is one clean header value:
// non-empty, printable ASCII and no space (internal/credsvc's rule for a
// presented token, auth.go's maxTokenBytes).
func printableBuildToken(tok string) bool {
	if tok == "" {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < 0x21 || tok[i] > 0x7e {
			return false
		}
	}
	return true
}
