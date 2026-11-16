package credsvc

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/task"
)

// The label route (D2439, F5, F18, F19, F26): the installation's orchestrator
// daemon asks this service to read and write the status and spec labels of the
// pull requests its runs opened, and the SERVICE performs the act itself — it
// mints its own short-lived token, which never leaves this process.
//
// That is the whole reason the route exists rather than a token being handed
// over: GitHub has no label-only permission, so any credential that could write
// a label could also approve, retitle or close any pull request of the
// repository (F5), and the daemon is the one caller this service already knows
// how to verify without a run identity.
//
// The caller class is the merge mint's (E16, D2049): the installation's
// orchestrator accounts, matched against a Google ID token minted for THIS
// SERVICE'S OWN URL and for nothing else, so the token a plain `gcloud auth
// print-identity-token` produces — and therefore the token a coding-agent
// session on the daemon's own machine produces — is refused before any list is
// consulted (F19, F26). What that fence does NOT do is make the daemon's
// identity unreachable from the machine it runs on, so the acts themselves are
// what bound a stolen identity (F18): the names the route accepts are the three
// below and no others, the text a label is created with is this service's own,
// `remove` takes the two status names alone, and every write lands only on a
// pull request the service has corroborated against the run id the caller sent.
//
// What the four acts are:
//
//   - `list` reads every open pull request's labels in one paged read of the
//     repository, which is what makes a tick that reads every tick affordable
//     (D2435, F10).
//   - `get` reads one pull request's state and labels, so a pull request the
//     bulk listing does not report — merged, closed — can be read back by
//     number before its status labels are cleared (R5, R8).
//   - `ensure` creates one label with D2437's colour and description, treating
//     one that already exists as success: the service creates its labels on
//     first use, so `fugaro update` stays the whole upgrade and an installation
//     already set up converges with no second command (D2440, F11).
//   - `add` and `remove` write ONE name on ONE pull request, and both corroborate
//     that pull request against the run id the call carries before anything is
//     written (F17, O3).
//
// The credential is this service's own: it mints an installation token scoped to
// the one repository and to `{pull_requests: write, metadata: read}`, caches it
// until it is near expiry, and never puts it in an answer, a log line or an audit
// line (D2439, U2). The acts are rate-limited per repository, with the reads
// counted apart from the writes (F22), and every call that reaches a verified
// caller writes exactly one audit line, served or refused (C1.13).

// LabelPath is the path the daemon asks for a label read or a label write on.
// It is this service's own contract and not a forge path: the call names a
// repository, a pull request and a label, and the service is what reaches
// GitHub.
//
// creds.LabelPath is the same path spelled by the client the daemon calls, and
// TestLabelRoutePathMatchesClient holds the two to each other, so a rename of
// either cannot quietly move the route.
const LabelPath = "/v1/github/labels"

// labelRunBranchPrefix is the branch prefix every run's pull request is opened
// from (gitops.RunBranchPrefix), spelled here rather than imported: this
// service holds no git and must not grow one for a string, and
// TestLabelRouteRunBranchPrefixMatchesRunner holds the two spellings to each
// other so the runner cannot move the branches out from under the
// corroboration below.
const labelRunBranchPrefix = "fugaro/"

// The two label names of D2437 and the namespace a spec label is spelled with.
//
// They are constants of THIS service and not fields of the call (F26): the
// request carries a name and nothing else, so the text a label is created with,
// and the names a removal may take, are the route's own answer and cannot be a
// caller's.
const (
	labelRunningName = "fugaro:running"
	labelFailedName  = "fugaro:failed"
	labelSpecPrefix  = "spec:"
)

// labelSpecRE is R3's shape for a spec name, matched against the name AFTER the
// namespace and after the caller's name has been checked for ASCII and
// lower-cased: lowercase letters, digits and dashes, at most 44 of them, so
// that `spec:` plus the name fits GitHub's 50-character limit (F20). The first
// character is alphanumeric, as R3's own regex has it.
var labelSpecRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,43}$`)

// labelText is the colour and the description D2437 gives one label. It is not
// a wire shape: the caller never sends one and never receives one.
type labelText struct{ colour, description string }

// D2437's grey and the sentence a spec label's description is built from. The
// spec description names the spec the pull request belongs to, which is the name
// after the namespace and not the label.
const (
	labelSpecDescriptionPrefix = "Pull request for spec "
	labelSpecColour            = "6E7781"
)

var (
	labelRunningText = labelText{colour: "1D76DB", description: "A Fugaro job is working on this pull request"}
	labelFailedText  = labelText{colour: "D73A4A", description: "The last Fugaro job on this pull request failed"}
)

// The route's own refusals. They are sentinels so a test and a reader can tell
// them apart, and every one is a sentence written here: GitHub's own answer
// never reaches a caller, because the requests that failed carried the App's JWT
// and an installation token.
var (
	// ErrLabelUnknownRepository is a repository this installation's inventory
	// does not hold.
	ErrLabelUnknownRepository = errors.New("credsvc: the repository is not one of this installation's")
	// ErrLabelNoAppKey is a bundle that holds no GitHub App key, and
	// ErrLabelBadAppKey is one whose key this code cannot sign with. Neither is
	// a fallback to a key the caller may hold (D143).
	ErrLabelNoAppKey  = errors.New("credsvc: the credentials bundle holds no GitHub App key")
	ErrLabelBadAppKey = errors.New("credsvc: the credentials bundle's GitHub App key is not a usable private key")
	// ErrLabelNotCorroborated is a write whose pull request is not the one the
	// run id the call carries opened: its head branch is not that run's branch,
	// its head repository is not the repository the label is written in, or its
	// author is not Fugaro's own identity (F17, O3).
	ErrLabelNotCorroborated = errors.New("credsvc: the pull request is not the one the run id the call carries opened")
	// ErrLabelRateLimited is a repository that has had as many label reads or
	// label writes as this service performs for one repository in an hour.
	ErrLabelRateLimited = errors.New("credsvc: this repository has been labelled as often as this service allows")
	// ErrLabelGitHub is GitHub refusing a read, a write or a mint, or the call
	// to any of them failing.
	ErrLabelGitHub = errors.New("credsvc: GitHub did not answer the label call")
	// ErrLabelUnsupported is a provider whose host has no pull request labels.
	// It is a refusal rather than the silent no-op a caller's own provider makes
	// of it (R7): the daemon asked this service to perform an act, and an empty
	// answer would read as one that was performed.
	ErrLabelUnsupported = errors.New("credsvc: this provider's host has no pull request labels")
	// ErrLabelUnaudited is an act GitHub performed and C1.13's auditor could not
	// record. The acts are written and their caller is told they were not
	// (D143): all five are idempotent, so a caller that retries costs a duplicate
	// line and not a duplicate label.
	ErrLabelUnaudited = errors.New("credsvc: the label call could not be audited")
)

// The route's rate limits (F22): this many acts for one repository in this long
// a window, with the reads counted apart from the writes. They are fixed numbers
// rather than configuration, for the merge mint's reason — the number the owner
// ruled is a bound on a runaway daemon and not a policy an installation tunes —
// and they are sized for the tick the daemon runs (labelTick): an hour is 120
// ticks, a tick reads the open listing once and reads a pull request by number
// only where the listing did not report it, so 240 reads an hour is two a tick;
// and a tick writes only where a label differs, so 60 writes an hour is one
// every other tick, which is more than a difference-only tick can spend and far
// less than a tick looping on one repository would.
//
// The bound is this process's memory and nothing else (labelState): the service
// is deployed without a minimum instance count and may run several, so it is per
// instance, is best effort, and begins again at every cold start — the same
// bound, in the same words, the merge mint's SECURITY.md paragraph states for
// its own rate. The paragraph for THIS route is T6's, and it states these two
// numbers as what they are.
const (
	labelRateWindow    = time.Hour
	labelWritesPerRepo = 60
	labelReadsPerRepo  = 240
)

// labelWindow is one direction's rate limit, held in this process's own memory.
// The two directions are two values of this type rather than one map keyed by a
// bool, which is what makes "counted apart" a fact about the structure and not
// about an expression inside a check.
type labelWindow struct {
	now   func() time.Time
	limit int
	mu    sync.Mutex
	seen  map[string][]time.Time
}

func newLabelWindow(now func() time.Time, limit int) *labelWindow {
	return &labelWindow{now: now, limit: limit, seen: map[string][]time.Time{}}
}

// allow records one act for slug and reports whether it was within the rate.
// The window is fixed and it is read from the service's own clock, which is the
// clock every other decision of this service is made with.
func (w *labelWindow) allow(slug string) bool {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.seen[slug][:0]
	for _, at := range w.seen[slug] {
		if now.Sub(at) < labelRateWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= w.limit {
		// The window is kept as it was: a refused call is not an act and does
		// not extend the window it was refused by.
		w.seen[slug] = kept
		return false
	}
	w.seen[slug] = append(kept, now)
	return true
}

// labelToken is one installation token this service minted, and when it expires.
type labelToken struct {
	token   string
	expires time.Time
}

// labelState is this process's memory for the label route: the installation
// tokens it has minted, the two rate windows, and nothing else.
//
// It is deliberately not in the database, for the merge mint's reason: what the
// windows bound is a daemon looping on this service, and a node under creds/
// would be a second writer of a fact that has no reader.
type labelState struct {
	tokens labelTokenCache
	writes *labelWindow
	reads  *labelWindow
}

func newLabelState(now func() time.Time) *labelState {
	return &labelState{
		writes: newLabelWindow(now, labelWritesPerRepo),
		reads:  newLabelWindow(now, labelReadsPerRepo),
	}
}

// allow reports whether one act for slug is within the rate, counting the
// writes and the reads apart.
func (st *labelState) allow(slug string, write bool) bool {
	if write {
		return st.writes.allow(slug)
	}
	return st.reads.allow(slug)
}

// labelTokenCache is the installation tokens this route has minted, keyed by the
// repository and the App they were minted for. Both are part of the key because
// a token is scoped to one repository by GitHub and signed as one App by this
// service: a token minted for another repository or another App is one this
// route must not write with.
type labelTokenCache struct {
	mu   sync.Mutex
	held map[string]labelToken
}

// labelTokenSource is what the provider the route performs its acts through gets
// its installation tokens from (gitprov.TokenSource): this service's own mint,
// at the route's own permission set, cached until the token is near its expiry.
//
// The provider is built with the bundle's key so that it can make the ONE read
// that only an App's key authorizes — GET /app, which is what makes PRHead.Self
// decidable, and therefore what makes the corroboration below possible — and
// this source replaces its in-job mint before any call is made, so no act of
// this route is ever performed with the run's wider token set.
type labelTokenSource struct {
	state *labelState
	s     *Server
	slug  string
	owner string
	repo  string
	appID string
	key   *rsa.PrivateKey
}

var _ gitprov.TokenSource = (*labelTokenSource)(nil)

// Token returns this service's own installation token, valid for at least
// minValid, minting one when the cached token would expire sooner.
//
// The whole check-then-mint runs under the cache's lock, as ServiceTokens does:
// the reads and the writes of one call reach Token at once, and a mint is an act
// at GitHub that must not happen once per request.
func (t *labelTokenSource) Token(ctx context.Context, minValid time.Duration) (string, time.Time, error) {
	key := t.slug + "\x00" + t.appID
	t.state.tokens.mu.Lock()
	defer t.state.tokens.mu.Unlock()
	if held, ok := t.state.tokens.held[key]; ok && held.expires.Sub(t.s.now()) >= minValid {
		return held.token, held.expires, nil
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner: t.owner, Repo: t.repo, AppID: t.appID, PrivateKey: t.key,
		HTTP: t.s.cfg.Client, Now: t.s.now,
	}, labelTokenPermissions())
	if err != nil {
		return "", time.Time{}, err
	}
	if t.state.tokens.held == nil {
		t.state.tokens.held = map[string]labelToken{}
	}
	t.state.tokens.held[key] = labelToken{token: token, expires: expires}
	return token, expires, nil
}

// labelTokenPermissions is the reach of the token this route mints for its own
// acts, and it is the whole of U2's answer: pull requests write is what GitHub's
// label endpoints require, metadata read is the noun every installation token
// carries, and there is no contents noun at all — the run's own set has one and
// a label write does not need it. The set is a constant of this service and not
// a field of the call, which is what makes a caller unable to widen it.
func labelTokenPermissions() map[string]string {
	return map[string]string{"pull_requests": "write", "metadata": "read"}
}

// labelCall is one label call as this service read it: the request as the wire
// carried it, the canonical name the acts that carry one write, the text a
// creation uses, and whether the act writes — which is the direction its rate is
// counted in.
type labelCall struct {
	req   creds.LabelRequest
	name  string
	text  labelText
	write bool
}

// labelName canonicalises one label name and refuses everything this route does
// not accept (F26, R3).
//
// The ASCII check comes FIRST, and that order is the rule and not a detail: the
// name is folded to lower case, and Unicode folding maps characters that only
// look like ASCII onto it — the Kelvin sign K onto "k", the dotted capital I onto
// "i" — so a name the host would never match against Fugaro's own could be
// lowered into one that reads as its own. Folding A-Z cannot do that, so a name
// with any byte outside ASCII is refused rather than folded.
//
// What is accepted after that is three things and nothing else: the two status
// names, matched case-insensitively the way the host matches label names (F23),
// and a `spec:` name whose remainder passes R3's shape. Everything else — a
// status Fugaro has no label for, a bare namespace, a name outside both
// namespaces — is refused, and the canonical (lower-cased) name is what the acts
// write, so a caller cannot use the case of a name to write a second label
// beside Fugaro's own.
func labelName(name string) (string, error) {
	if name == "" {
		return "", errors.New("the call names no label")
	}
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			return "", errors.New("the label name is not ASCII, and a name folded with Unicode can lower into one of Fugaro's own that the host would never match")
		}
	}
	lower := strings.ToLower(name)
	switch lower {
	case labelRunningName, labelFailedName:
		return lower, nil
	}
	if spec, ok := strings.CutPrefix(lower, labelSpecPrefix); ok {
		if !labelSpecRE.MatchString(spec) {
			return "", errors.New("the label names a spec this service does not label: a spec name is lowercase letters, digits and dashes, at most 44 of them, and it is written as spec:<name>")
		}
		return lower, nil
	}
	return "", errors.New("the call names no label this service writes: it accepts " + labelRunningName + ", " + labelFailedName + " and a spec: name of the shape the plan gives")
}

// labelTextFor is D2437's colour and description for one canonical name, which
// the caller cannot name and does not supply (F26).
func labelTextFor(name string) labelText {
	switch name {
	case labelRunningName:
		return labelRunningText
	case labelFailedName:
		return labelFailedText
	}
	return labelText{colour: labelSpecColour, description: labelSpecDescriptionPrefix + strings.TrimPrefix(name, labelSpecPrefix)}
}

// readLabelCall reads a label call's body out of the call the handler already
// read (readCall), which is where the daemon's own ID token was registered with
// the service's redaction for the length of this call.
//
// The decode is strict, as every body of this contract is, which is what makes
// "the caller cannot name the text, the permissions or the repository list" a
// property of the route rather than a check inside it (Convention 6). Each act
// then takes the fields it has and no others, so a body carrying a field its act
// does not take is refused rather than read for the fields it does.
func readLabelCall(body []byte) (labelCall, error) {
	if len(body) > maxMintRequestBytes {
		return labelCall{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req creds.LabelRequest
	if err := decodeStrict(body, &req); err != nil {
		return labelCall{}, errors.New("the body is not one JSON object of this route's fields")
	}
	if _, _, ok := gitprov.SplitRepo(req.Repository); !ok {
		return labelCall{}, errors.New("the call names no repository (owner/name)")
	}
	if !appIDRE.MatchString(req.AppID) {
		return labelCall{}, errors.New("the call carries no GitHub App ID (1 to 20 digits)")
	}
	switch req.Action {
	case creds.LabelActionList:
		if req.Number != 0 || req.Name != "" || req.RunID != "" {
			return labelCall{}, errors.New("a listing names no pull request, no label and no run")
		}
		return labelCall{req: req}, nil
	case creds.LabelActionGet:
		if req.Number < 1 {
			return labelCall{}, errors.New("the call names no pull request number")
		}
		if req.Name != "" || req.RunID != "" {
			return labelCall{}, errors.New("a pull request read names no label and no run")
		}
		return labelCall{req: req}, nil
	case creds.LabelActionEnsure:
		if req.Number != 0 || req.RunID != "" {
			return labelCall{}, errors.New("a label is created for the repository, and the call names no pull request and no run")
		}
		return labelCallNamed(req)
	case creds.LabelActionAdd:
		if err := labelWriteFields(req); err != nil {
			return labelCall{}, err
		}
		return labelCallNamed(req)
	case creds.LabelActionRemove:
		if err := labelWriteFields(req); err != nil {
			return labelCall{}, err
		}
		call, err := labelCallNamed(req)
		if err != nil {
			return labelCall{}, err
		}
		// A removal takes the two status names alone (F26, F23): the spec label
		// is the daemon's to add and a person's to take off, so a stolen daemon
		// identity cannot remove one a person added, and the rule is here rather
		// than beside the namespace check because it is this act's and not the
		// name's.
		if call.name != labelRunningName && call.name != labelFailedName {
			return labelCall{}, errors.New("the call removes no label this route removes: a removal takes " + labelRunningName + " and " + labelFailedName + " alone")
		}
		return call, nil
	default:
		return labelCall{}, errors.New("the call names no act this route performs: it performs " + creds.LabelActionList + ", " + creds.LabelActionGet + ", " + creds.LabelActionEnsure + ", " + creds.LabelActionAdd + " and " + creds.LabelActionRemove)
	}
}

// labelWriteFields holds an act that names one pull request to the fields such
// an act takes: a pull request number that is one, a run id that is one, and a
// label. A number of zero or less is refused here (F17), and so is a run id this
// code does not read, because the corroboration below is what the run id is for
// and a call that carries none could not be corroborated.
func labelWriteFields(req creds.LabelRequest) error {
	if req.Number < 1 {
		return errors.New("the call names no pull request number")
	}
	if !task.ValidRunID(req.RunID) {
		return errors.New("the call carries no run id: a label write names the id of the run that opened the pull request")
	}
	if req.Name == "" {
		return errors.New("the call names no label")
	}
	return nil
}

// labelCallNamed is the three acts that carry a name: it canonicalises the name,
// refuses one this route does not write, and takes D2437's text for it.
func labelCallNamed(req creds.LabelRequest) (labelCall, error) {
	name, err := labelName(req.Name)
	if err != nil {
		return labelCall{}, err
	}
	return labelCall{req: req, name: name, text: labelTextFor(name), write: true}, nil
}

// labelRoute answers one label call. It is reached from ServeHTTP's branch
// ahead of RequestFromHTTP, and for the merge mint's reason: the daemon is not
// a run of this installation and holds no run identity, so the shared parse
// would refuse the call — for the missing identity headers — before the class
// ever read the token.
//
// The order below is the whole of the gate, and it is why it is where it is:
//
//   - the caller, first, and on its own token. This is the route's admission
//     and nothing else is read before it: a body cannot widen it, and a refusal
//     here is typed and has no line, because nothing is verified yet and there
//     is no caller for a line to name (the merge mint's class refusal, the read
//     route's person refusal and this service's rule that a line names a
//     VERIFIED caller all say the same thing).
//   - the run identity the class does not take, refused by name before anything
//     else is read: the daemon holds no run identity, so a call presenting one
//     is a call of a shape this route does not have.
//   - the call, whose read is ALSO the registration of the daemon's own token
//     with the call-scoped scrub, and whose every field and name is checked
//     before anything is resolved or reached.
//   - the operator's stop flag, read BEFORE any credential, so a stopped
//     service reads none.
//   - the repository, resolved through the installation's inventory.
//   - the rate limit, checked before the bundle so that a repository that has
//     been labelled as often as this service allows reads no credential at all.
//   - the bundle and the App key, failing closed.
//   - the provider, built with the bundle's key so that the one read that makes
//     Fugaro's own identity decidable is possible, and with this service's own
//     mint installed over its in-job one.
//   - the corroboration, for the two acts that write on a pull request and
//     before either of them reaches one (F17, O3).
//   - the act, through T1's adapter.
//   - the answer, built, audited and handed over in that order (answerLabel).
func (s *Server) labelRoute(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, credential := routeAct(LabelPath)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyOrchestrator(ctx, tok)
	if err != nil {
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseLabel(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it performs label calls for the orchestrator daemon, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseLabel(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it performs label calls for the orchestrator daemon, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseLabel(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	call, err := readLabelCall(cl.body)
	if err != nil {
		s.refuseLabel(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		s.refuseLabel(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseLabel(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	slug, owner, repo, err := githubRepository(s.cfg.Repos, call.req.Repository)
	if err != nil {
		s.refuseLabel(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(err, call.req.Repository))
		return
	}
	if !s.labels.allow(slug, call.write) {
		s.refuseLabel(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, labelRateRefusal(call.write))
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseLabel(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, err := appKey(b)
	if err != nil {
		if errors.Is(err, ErrMintNoAppKey) {
			err = ErrLabelNoAppKey
		} else {
			err = ErrLabelBadAppKey
		}
		s.refuseLabel(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	provider, err := github.New(github.Options{
		Owner: owner, Repo: repo, AppID: call.req.AppID, PrivateKey: key,
		HTTP: s.cfg.Client, Now: s.now,
	})
	if err != nil {
		s.log.Warn("credsvc: the label route's provider could not be built", "purpose", purpose, "credential", credential, "repository", slug)
		s.refuseLabel(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrLabelGitHub.Error())
		return
	}
	provider.UseServiceTokens(&labelTokenSource{
		state: s.labels, s: s, slug: slug, owner: owner, repo: repo, appID: call.req.AppID, key: key,
	})
	switch call.req.Action {
	case creds.LabelActionAdd, creds.LabelActionRemove:
		if err := s.labelCorroborate(ctx, provider, call.req.Number, call.req.RunID, owner+"/"+repo); err != nil {
			if errors.Is(err, ErrLabelNotCorroborated) {
				s.refuseLabel(ctx, w, caller, slug, StatusRequestRefused, ErrorRequestRefused, err.Error())
				return
			}
			s.labelUpstreamRefusal(ctx, w, caller, slug, purpose, credential, "the label call's pull request could not be read", err)
			return
		}
	}
	ans, err := s.labelPerform(ctx, provider, call)
	if err != nil {
		if errors.Is(err, ErrLabelUnsupported) {
			s.refuseLabel(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrLabelUnsupported.Error())
			return
		}
		s.labelUpstreamRefusal(ctx, w, caller, slug, purpose, credential, "the label call was not performed", err)
		return
	}
	s.answerLabel(ctx, w, caller, slug, ans)
}

// labelUpstreamRefusal answers a refusal whose cause is this service's or
// GitHub's and not the call's, logging the class of the failure and, when GitHub
// answered, its status — and never a byte of GitHub's own text, because the
// requests that failed carried the App's JWT and an installation token.
func (s *Server) labelUpstreamRefusal(ctx context.Context, w http.ResponseWriter, caller, repository, purpose, credential, what string, err error) {
	attrs := []any{"purpose", purpose, "credential", credential, "repository", repository}
	var se *httpjson.StatusError
	if errors.As(err, &se) {
		attrs = append(attrs, "status", se.Status)
	}
	s.log.Warn("credsvc: "+what, attrs...)
	s.refuseLabel(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, ErrLabelGitHub.Error())
}

// labelCorroborate is F17's whole answer, and it is why a write carries a run
// id: the pull request number the call names comes from the run record, which
// the job can write — the triage code refuses to trust that record for the same
// reason — so a hostile run could name another pull request and have it
// relabelled. The run id comes from the daemon's OWN intents (O3), and the
// service refuses the write unless every one of these holds:
//
//   - the pull request's current head branch is exactly this run's branch,
//     `fugaro/<run id>` and nothing else — not a branch that begins with it, and
//     not another run's;
//   - its head repository is the repository the label is written in, because a
//     fork can carry a branch of that name (PRHead.Fork fails closed when GitHub
//     reports no head repository at all, which is a deleted fork);
//   - its author is Fugaro's own identity, the App's own bot login as
//     gitprov.PRHead.Self reports it.
//
// What it does NOT read is the pull request's state: clearing the status labels
// of a pull request that has merged or closed is exactly what the two status
// names are read back for (R5, R8), so a closed one is corroborated like an open
// one and the caller's own rule decides what it carries.
//
// Two reads make it: the pull request, for its head branch and head repository,
// and the head read, for the author. Both are made through the provider, and the
// second is what needs the App's key — Self is decidable only by asking GitHub
// who this App is, which is the one read an installation token cannot make.
func (s *Server) labelCorroborate(ctx context.Context, p *github.Provider, number int, runID, base string) error {
	info, err := p.PullRequest(ctx, number)
	if err != nil {
		return fmt.Errorf("%w: the pull request could not be read: %w", ErrLabelGitHub, err)
	}
	head, err := p.FetchPRHead(ctx, number)
	if err != nil {
		return fmt.Errorf("%w: the pull request's author could not be read: %w", ErrLabelGitHub, err)
	}
	switch {
	case info.SourceBranch != labelRunBranchPrefix+runID:
		return fmt.Errorf("%w: its head branch is not the branch the run id the call carries pushes to", ErrLabelNotCorroborated)
	case !strings.EqualFold(info.SourceRepo, base):
		return fmt.Errorf("%w: its head repository is not the repository the label is written in", ErrLabelNotCorroborated)
	case head.Fork:
		return fmt.Errorf("%w: its head is a fork", ErrLabelNotCorroborated)
	case !head.Self:
		return fmt.Errorf("%w: its author is not Fugaro's own identity", ErrLabelNotCorroborated)
	}
	return nil
}

// labelPerform performs one call's act through T1's adapter and builds the
// answer, which is what the two reads read and nothing at all for the three acts
// that only change state.
//
// The adapter is asked Supported first: a provider whose host has no labels
// makes every call a silent no-op (R7), which is the right answer for the
// DAEMON's own provider and the wrong one here — this service was asked to
// perform an act and an empty answer would read as one performed. The two acts
// that write on a pull request name the run id through to the adapter, which
// ignores it: the corroboration is this service's and has already happened.
func (s *Server) labelPerform(ctx context.Context, l gitprov.Labeler, call labelCall) (creds.LabelAnswer, error) {
	if !l.Supported() {
		return creds.LabelAnswer{}, ErrLabelUnsupported
	}
	switch call.req.Action {
	case creds.LabelActionList:
		labels, err := l.ListOpenPRLabels(ctx)
		if err != nil {
			return creds.LabelAnswer{}, fmt.Errorf("%w: the open pull requests could not be read: %w", ErrLabelGitHub, err)
		}
		numbers := make([]int, 0, len(labels))
		for number := range labels {
			numbers = append(numbers, number)
		}
		// The listing is a map and the answer is a list: the order is this
		// service's and not the host's, so that two calls of one tick answer the
		// same way and a caller that compares two answers compares the labels.
		sort.Ints(numbers)
		ans := creds.LabelAnswer{PullRequests: make([]creds.LabelPullRequest, 0, len(numbers))}
		for _, number := range numbers {
			ans.PullRequests = append(ans.PullRequests, creds.LabelPullRequest{
				Number: number, State: string(gitprov.PROpen), Labels: labels[number],
			})
		}
		return ans, nil
	case creds.LabelActionGet:
		state, names, err := l.PRLabels(ctx, call.req.Number)
		if err != nil {
			return creds.LabelAnswer{}, fmt.Errorf("%w: the pull request's labels could not be read: %w", ErrLabelGitHub, err)
		}
		return creds.LabelAnswer{PullRequests: []creds.LabelPullRequest{{
			Number: call.req.Number, State: string(state), Labels: names,
		}}}, nil
	case creds.LabelActionEnsure:
		if err := l.EnsureLabel(ctx, call.name, call.text.colour, call.text.description); err != nil {
			return creds.LabelAnswer{}, fmt.Errorf("%w: the label could not be created: %w", ErrLabelGitHub, err)
		}
	case creds.LabelActionAdd:
		if err := l.AddLabels(ctx, call.req.Number, call.req.RunID, []string{call.name}); err != nil {
			return creds.LabelAnswer{}, fmt.Errorf("%w: the label could not be added: %w", ErrLabelGitHub, err)
		}
	case creds.LabelActionRemove:
		if err := l.RemoveLabels(ctx, call.req.Number, call.req.RunID, []string{call.name}); err != nil {
			return creds.LabelAnswer{}, fmt.Errorf("%w: the label could not be removed: %w", ErrLabelGitHub, err)
		}
	default:
		// readLabelCall refuses an act this route does not perform, so this is
		// unreachable — and it is here because the alternative to an error is an
		// empty answer, which a caller reads as an act that was performed.
		return creds.LabelAnswer{}, fmt.Errorf("%w: the call names no act this route performs", ErrLabelGitHub)
	}
	return creds.LabelAnswer{}, nil
}

// labelRateRefusal is the sentence a rate-limited call is refused with. It
// states the number, because an operator reading it has to know which limit was
// reached and how much of the hour is left to be waited for.
func labelRateRefusal(write bool) string {
	if write {
		return fmt.Sprintf("%s: this service performs %d label writes an hour for one repository, and this repository has had as many", ErrLabelRateLimited.Error(), labelWritesPerRepo)
	}
	return fmt.Sprintf("%s: this service performs %d label reads an hour for one repository, and this repository has had as many", ErrLabelRateLimited.Error(), labelReadsPerRepo)
}

// answerLabel builds the label route's answer, audits the act and writes it. It
// is answerMergeMint's order and answerMergeMint's reasons: the answer is built
// first so that a body the service cannot build is a refusal with its own line
// and no act line at all, and the audit line is written before the caller is
// answered, because an act that cannot be audited is not one this service
// reports as done (C1.13, D143).
//
// The acts are idempotent, so the refusal is safe in a way the merge mint's is
// not: a caller that retries a call whose line could not be written re-does an
// act that changed nothing the second time.
func (s *Server) answerLabel(ctx context.Context, w http.ResponseWriter, caller, repository string, ans creds.LabelAnswer) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the label route's answer could not be built", "error", err)
		s.refuseLabel(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if err := s.auditLabel(ctx, caller, repository, StatusOK); err != nil {
		s.log.Error("credsvc: the label route's audit line was not written", "error", err)
		s.refuseLabel(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, ErrLabelUnaudited.Error())
		return
	}
	writeReadAnswer(w, body)
}

// refuseLabel answers one label refusal and records it, in the shape
// refuseMergeMint already has: one typed refusal to the caller, one line to the
// operator, and the caller's answer is the same whether or not the line lands
// (D143).
//
// It is reached only once the caller IS verified — the refusals above that
// point are answered by refuseVerifier alone, with no line, because an
// unverified caller has no name for one.
func (s *Server) refuseLabel(ctx context.Context, w http.ResponseWriter, caller, repository string, status int, typ, message string) {
	if err := s.auditLabel(ctx, caller, repository, typ); err != nil {
		s.log.Error("credsvc: a refused label call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// auditLabel writes one label route's line: the daemon as its caller, the
// repository the act was for, the App key as its credential, and NEITHER a run
// nor a job — the caller is the installation's orchestrator daemon, which holds
// no run identity and is not a job of this installation. A refusal taken before
// the call was read has no repository to name, and the repository is the one
// thing a line for the act itself adds.
//
// It does not go through Identity.Audit because there is no Identity: the route
// is answered ahead of Verify, which is the only thing that builds one. The
// repository is the installation's SLUG for it and not the "owner/name" the
// caller typed, so the line is keyed the way every other line of this log is.
// purpose and credential come from routeAct, the ONE rule.
func (s *Server) auditLabel(ctx context.Context, caller, repository, status string) error {
	purpose, credential := routeAct(LabelPath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Repository: repository,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}
