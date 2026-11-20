package cli

// workspace_probe.go is WU22: the credential probe (D2820, D2825).
//
// `workspace project status` prints, for each member, whether the credentials
// the member references are stored and whether their marks say broken. The
// spec asks for more than that — whether they are *valid* — and nothing but
// the provider can answer it (spec.md §1). So the answer is a separate,
// opt-in read behind --check-credentials: this file reads each credential's
// value into memory, makes one minimal authenticated call with it, and reports
// what the provider answered — pass or fail with the provider's own status,
// and never the value (D2820).
//
// Four rules shape it, and they are why the probe is a file of its own rather
// than a few lines inside `status`.
//
//   - It runs in the owner's own terminal and nowhere else. The value read
//     here is a model credential, so the read is refused in a coding agent's
//     session before anything is read, in the words every other key read in
//     this program uses (refuseKeyReadAt, githubapp.go).
//   - It reads project credentials only. A member project's Secret Manager
//     also holds credentials under a person's own prefix (internal/secretname),
//     delivered there by the intake for that person's runs; a workspace admin
//     reading one is the threat model's "a workspace admin reads every member
//     secret" turned into a convenience. Those names are skipped by name,
//     before any read, and their broken marks are `status`'s own row (D2825).
//   - It writes nothing. Its only reach on the member's project is the
//     read-only seam below, which has no write to call: `status` writes nothing
//     on any path, --check-credentials included (D2809).
//   - It goes nowhere near the credentials service. That service's probe
//     (internal/credsvc/probe.go) is internal to its selection path and admits
//     job accounts only; the owner's terminal asks the provider itself (U7).
//
// The call is GET /v1/models?limit=1: the one authenticated call that proves a
// credential without naming a model and without spending a token. A Messages
// call is the other candidate, and it is what the credentials service's own
// probe makes — but it needs a model, and a model is exactly what this probe
// cannot know: the credential belongs to a member whose models this machine has
// not read, and a probe that guessed one would report a working credential
// broken over a model the member does not serve. What the endpoint answers is
// a status, which is all this probe reports: 2xx is the credential working,
// 401 or 403 is the provider refusing it, and anything else — an outage, a
// rate limit, a redirect — is a call nothing answered, a fact about the
// network rather than a credential to replace.
//
// What is NOT proven here, and is the one assumption this file makes: Check 90
// (gateway/oauth.go) proved a subscription token on a Messages call, with
// Anthropic-Version as the one mandatory header, and nothing has measured a
// subscription token against the models endpoint. A model key on that endpoint
// is Anthropic's documented shape; a Claude account that the endpoint refuses
// would be reported refused, which is the fail-closed direction — the owner
// reads the provider's own status and decides.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// checkCredentialsFlag is the flag that allows the read (D2820):
// `workspace project status --check-credentials`. It is named here rather than
// in the status command so the refusal this file gives and the flag that
// command adds cannot come to disagree.
const checkCredentialsFlag = "--check-credentials"

// probeAPIVersion is the version header every probe call carries: the one
// header Check 90 proved mandatory for a subscription call (gateway/oauth.go),
// so a probe that dropped it would fail for a reason that has nothing to do
// with the credential.
const probeAPIVersion = "2023-06-01"

// probeModelsPath and probeModelsQuery are the one call: the models endpoint,
// asked for a single row, which is the smallest answer that still says whether
// the credential was accepted.
const (
	probeModelsPath  = "/v1/models"
	probeModelsQuery = "limit=1"
)

// probeAnswerBytes bounds what one answer is read as. The answer's body is
// thrown away — the question is whether the credential was accepted — and it
// is read rather than left unread so a keep-alive connection survives the
// probe.
const probeAnswerBytes = 1 << 16

// probeCallTimeout bounds one probe call. The probe runs in a person's
// terminal, where a provider that never answers must not become a command that
// never returns; a call that ran out of time is unanswered like any other.
const probeCallTimeout = 30 * time.Second

// probeAPIURL and probeAPIClient are the host the call goes to and the client
// it is made with; tests replace both. The host is Anthropic's own, which is
// where every credential this probe reads belongs, and it is a variable rather
// than a config key for the reason gateway/oauth.go gives at length: a base a
// config could set is a base a member's file could aim the credential at. The
// client refuses a redirect instead of following one (refuseRedirect,
// updatewarn.go), for the reason that file gives and one of this probe's own:
// the credential travels in x-api-key, which Go does not know to drop across
// hosts the way it drops Authorization, so a 302 would carry it to whatever
// host answered — and the 3xx the client hands back is read here as a call
// nothing answered.
var (
	probeAPIURL    = "https://api.anthropic.com"
	probeAPIClient = &http.Client{Timeout: probeCallTimeout, CheckRedirect: refuseRedirect}
)

// ProbeOutcome is what asking about one credential said.
type ProbeOutcome int

const (
	// ProbePassed: the provider answered 2xx — the credential works.
	ProbePassed ProbeOutcome = iota
	// ProbeRefused: the provider answered 401 or 403 — it does not accept the
	// credential, which is what a broken mark is made of.
	ProbeRefused
	// ProbeUnanswered: the call was made and nothing answered it — an outage, a
	// rate limit, a transport error. A fact about the network, and never a
	// credential to replace, which is the distinction doctor's own token check
	// draws (tokenUnanswered, doctor_brokering.go).
	ProbeUnanswered
	// ProbeSkipped: nothing was asked, and Detail says why. Only a credential
	// under a person's own prefix is skipped today (D2825).
	ProbeSkipped
)

// String is the outcome in the words a status line prints.
func (o ProbeOutcome) String() string {
	switch o {
	case ProbePassed:
		return "passed"
	case ProbeRefused:
		return "refused"
	case ProbeUnanswered:
		return "unanswered"
	case ProbeSkipped:
		return "skipped"
	}
	return fmt.Sprintf("outcome(%d)", int(o))
}

// ProbeResult is one credential's answer, and the value is never in it: the
// type carries the credential's name, what asking said and the provider's own
// words about it, which is everything `status` prints and everything a
// terminal may see (D2820). A field holding the value is the one thing this
// type must never grow, and TestProbeNeverPrintsTheValue is where that is
// held.
type ProbeResult struct {
	// Secret is the credential's logical name, as the member's config and its
	// own `fugaro secrets ls` spell it.
	Secret string
	// Outcome is what asking said.
	Outcome ProbeOutcome
	// Detail is the provider's own status ("200 OK", "401 Unauthorized") for a
	// call that was answered, the transport's one-line error for a call that
	// was not, and why nothing was asked for a skipped credential.
	Detail string
}

// probeCredential is one credential a member's project holds: the logical name
// it is stored under, and the Secret Manager container's own ID, which is what
// a read is addressed with. The two are not one string: a project-wide
// container's ID is derived (gcp.ProjectSecretID), while a credential the
// intake delivered is stored under its own name. Reading the ID off the
// listing is what keeps this file from re-deriving a name it would then have
// to get exactly right.
type probeCredential struct {
	Name string
	ID   string
}

// probeSecrets is a member's project as this probe reaches it: the credentials
// its Secret Manager holds, and one container's value. It is read-only by
// construction — there is no write to call — which is what makes "status
// writes nothing on any path" a property of the shape rather than a promise
// about the calls made (D2809).
type probeSecrets interface {
	// Credentials is every container the project holds that has a value to ask
	// about.
	Credentials(ctx context.Context) ([]probeCredential, error)
	// Value is one container's value, held in memory only.
	Value(ctx context.Context, id string) (string, error)
}

// gcpProbeSecrets is the member's project read through Secret Manager with the
// caller's own credentials — the owner's, never a job's: the same read
// `doctor --check-bitbucket-token` makes of the bundle.
type gcpProbeSecrets struct{ sm *gcp.Secrets }

// Credentials lists the containers that hold a value. The listing is
// unfiltered on purpose: the static families label themselves with their
// logical name (gcp.LabelSecret), and the containers the intake writes carry
// the intake's own labels instead (internal/intake), so a listing narrowed to
// Fugaro's managed label would miss exactly the personal credentials this
// probe has to see in order to leave them alone.
func (g gcpProbeSecrets) Credentials(ctx context.Context) ([]probeCredential, error) {
	list, err := g.sm.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]probeCredential, 0, len(list))
	for _, s := range list {
		if s.Latest == "" {
			// No enabled version: nothing to read, so nothing to ask about.
			// Whether a credential is stored at all is `status`'s own row
			// (D2820), read off the same listing.
			continue
		}
		out = append(out, probeCredential{Name: probeLogicalName(s), ID: s.ID})
	}
	return out, nil
}

// Value is one credential's value. It is put in a header and nowhere else:
// nothing in this file prints, logs or returns it.
func (g gcpProbeSecrets) Value(ctx context.Context, id string) (string, error) {
	v, err := g.sm.Access(ctx, id)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// probeLogicalName is the name a container is stored under: the label the
// static families carry it in, and the container's own ID for the names the
// intake writes, whose name IS the container (internal/secretname's grammar is
// the Secret Manager name, which is also why neither family can collide with a
// derived one). A container that is neither is reported by its ID and is no
// credential of the kinds probeKindOf knows, so it is left alone.
func probeLogicalName(s gcp.SecretInfo) string {
	if n := s.Labels[gcp.LabelSecret]; n != "" {
		return n
	}
	return s.ID
}

// probeKind is how a credential authenticates, and so which header carries it.
type probeKind int

const (
	// probeModelKey is a model key: Anthropic's own header, x-api-key.
	probeModelKey probeKind = iota
	// probeClaudeAccount is a Claude account's OAuth token, which is a
	// subscription's: the bearer header Check 90 recorded
	// (swap-credential-header, gateway/oauth.go).
	probeClaudeAccount
)

// probeKindOf is which credential a name is, and whether it is one this probe
// asks about at all: the project's own model key (config.AnthropicSecret) goes
// on x-api-key, and the project-owned Claude accounts (config.ClaudeOAuthSecret)
// on the bearer header. Those two families are the only ones a name settles.
//
// A named key credential is NOT one of them, and that is deliberate:
// credential-<name>-key is the container of every KEY credential, OpenRouter's
// included (config.KeyCredentialType is true for both, and CredentialBySecret
// returns the name and never the type, which lives in the member's own config —
// a config this probe does not read). Sending an OpenRouter key to Anthropic
// would disclose a third party's credential to Anthropic and report a working
// credential refused: the same misreport the Codex rule below exists to avoid,
// with the credential spent as well. If a later task hands this probe the
// declared types, the family comes back with a type test and not before.
//
// Every other name a member's project holds is left alone, and the list being
// this short is the point. A workflow's own secret, a repository's token, the
// credentials service's one bundle, a named key credential and a project-shared
// container the intake wrote (fugaro_p_<name>) are credentials of some kind,
// and none of them is a model credential whose kind could be read off its name;
// a Codex OAuth token (config.CodexOAuthSecret) is one, of another provider,
// and the one call here is Anthropic's, so asking would report a working
// credential refused. Asking about any of them would spend a credential the
// owner never offered for this question.
func probeKindOf(name string) (probeKind, bool) {
	switch {
	case name == config.AnthropicSecret:
		return probeModelKey, true
	case config.ClaudeOAuthSecret(name):
		return probeClaudeAccount, true
	}
	return 0, false
}

// WorkspaceProbe is WU22's probe: the concrete type I1 adapts onto C2's
// CredentialProbe.
type WorkspaceProbe struct {
	// getenv and terminal are the two facts the agent-session refusal is read
	// from, passed in rather than read from the process so a test drives the
	// refusal without setting the process environment.
	getenv   func(string) string
	terminal bool
	// open reaches one member project's Secret Manager, and client makes the
	// call. Both are seams, and neither reaches a network in a test. There is
	// no default for client: a probe built with none refuses in Probe rather
	// than falling back to http.DefaultClient, which follows a redirect with
	// the credential attached.
	open   func(ctx context.Context, gcpProject string) (probeSecrets, error)
	client *http.Client
}

// NewWorkspaceProbe is the probe a command builds: lc's endpoints, the
// caller's own credentials and this file's own client — bounded by a timeout,
// and refusing to follow a redirect.
func NewWorkspaceProbe(lc *localcfg.Config) *WorkspaceProbe {
	return &WorkspaceProbe{
		getenv:   os.Getenv,
		terminal: processTerminal(),
		client:   probeAPIClient,
		open: func(ctx context.Context, gcpProject string) (probeSecrets, error) {
			sm, err := gcp.NewSecrets(ctx, probeOptions(lc, gcpProject))
			if err != nil {
				return nil, err
			}
			return gcpProbeSecrets{sm: sm}, nil
		},
	}
}

// probeOptions are gcpOptions of the member's project: the endpoints and the
// region are this machine's installation's — they are where the member's own
// installation was pointed — and the project is the member's own, whose Secret
// Manager holds the credentials its runs mount.
func probeOptions(lc *localcfg.Config, gcpProject string) gcp.Options {
	o := gcpOptions(lc)
	o.GCPProject = gcpProject
	return o
}

// Probe asks each model credential a member's project holds, and answers what
// the provider said about each. gcpProject is the member's own Google Cloud
// project, which is the one whose Secret Manager holds its credentials — the
// Firebase project holds none, which is why this takes the one name (F2/R10
// keep the two apart, and the secrets are on this side of the split).
//
// An error is a refusal — an agent's session — a probe built with no client,
// or a read that did not answer at all. A credential the provider refused is a
// result and not an error: the caller is printing it.
func (p *WorkspaceProbe) Probe(ctx context.Context, gcpProject string) ([]ProbeResult, error) {
	if p.client == nil {
		// Never the default client by fallback: it follows a redirect, and it
		// carries x-api-key verbatim to whatever host one names — the leak
		// refuseRedirect exists for. A probe with no client of its own asks
		// nothing rather than asking with that one.
		return nil, errors.New("the probe has no HTTP client: a credential is never spent on the default one, which follows a redirect carrying it")
	}
	if err := refuseKeyReadAt(p.getenv, p.terminal, checkCredentialsFlag, "a member project's model credentials"); err != nil {
		return nil, err
	}
	store, err := p.open(ctx, gcpProject)
	if err != nil {
		return nil, err
	}
	creds, err := store.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	// The listing is a set written as a list (the convention every record here
	// follows), and the probe sorts it for the same reason: two runs over one
	// project print the same lines in the same order. The copy is the probe's
	// own, so the sort cannot reach back into the store.
	creds = slices.Clone(creds)
	slices.SortFunc(creds, func(a, b probeCredential) int { return strings.Compare(a.Name, b.Name) })
	asked := make(map[string]bool, len(creds))
	results := make([]ProbeResult, 0, len(creds))
	for _, c := range creds {
		// One credential, one answer: a project-wide container and a
		// repository's own copy of one logical name are one credential stored
		// twice, and asking twice would be two calls for one question.
		if asked[c.Name] {
			continue
		}
		asked[c.Name] = true
		if strings.HasPrefix(c.Name, secretname.UserPrefix) {
			// A person's own credential, delivered by the intake for that
			// person's runs and never the project's to ask about (D2825): the
			// workspace admin reading `status` is not its owner, and reading
			// its value here is the threat model's "a workspace admin reads
			// every member secret" made a convenience. It is reported rather
			// than dropped, so the owner sees that the credential was left
			// alone on purpose — its broken mark is read off the mark itself,
			// by `status`'s own row.
			results = append(results, ProbeResult{Secret: c.Name, Outcome: ProbeSkipped,
				Detail: "a personal credential: it belongs to the person whose runs mount it, and is never read here (D2825)"})
			continue
		}
		kind, ok := probeKindOf(c.Name)
		if !ok {
			continue
		}
		value, err := store.Value(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", c.Name, err)
		}
		results = append(results, p.ask(ctx, c.Name, kind, value))
	}
	return results, nil
}

// ask makes the one minimal authenticated call and reads the answer as an
// outcome. It returns no error: a call that failed to arrive is an answer too
// (ProbeUnanswered), and the caller prints it.
func (p *WorkspaceProbe) ask(ctx context.Context, secret string, kind probeKind, value string) ProbeResult {
	res := ProbeResult{Secret: secret}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(probeAPIURL, "/")+probeModelsPath+"?"+probeModelsQuery, nil)
	if err != nil {
		res.Outcome, res.Detail = ProbeUnanswered, oneLine(err.Error())
		return res
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("anthropic-version", probeAPIVersion)
	if kind == probeClaudeAccount {
		req.Header.Set("authorization", "Bearer "+value)
	} else {
		req.Header.Set("x-api-key", value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		// The value travels in a header and never in the URL, so a transport
		// error — which carries the URL — cannot echo it.
		res.Outcome, res.Detail = ProbeUnanswered, oneLine(err.Error())
		return res
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, probeAnswerBytes))
	// The provider's own status, made safe for a terminal: it is the one
	// string of this answer that comes off the wire, and `status` prints it.
	res.Detail = oneLine(resp.Status)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		res.Outcome = ProbePassed
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.Outcome = ProbeRefused
	default:
		res.Outcome = ProbeUnanswered
	}
	return res
}
