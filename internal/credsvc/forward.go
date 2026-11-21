package credsvc

// The upstream call (C1.11): one attempt, built from C1.8's exported helpers,
// with the bounds and the refusals this service adds.
//
// Two things here are the service's own and not the gateway's, because the
// gateway never has them: the request body is held whole (an attempt that
// consumed it could not be repeated), and an answer that has begun to stream
// is scanned for the upstream's own usage-limit error, which arrives inside a
// 200 stream and not as a status.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// upstreamTimeout bounds the whole attempt, headers and body. It is generous
// because a Messages stream is long by design (the SDK's own read timeout is
// minutes) and it exists only so that a connection nothing ever answers is
// released rather than held for the life of the instance.
const upstreamTimeout = 30 * time.Minute

// upstreamClient is the client every upstream attempt is made with, whether it
// was built here or handed to NewServer: it never follows a redirect and never
// keeps a cookie jar.
//
// A redirect is the one answer that would take the credential somewhere nobody
// chose. A 307 or 308 re-sends the request — body and headers — to whatever
// host the Location names, and Go's own client drops Authorization across hosts
// but not x-api-key, so a followed redirect is a tier-B paid key (or, on the
// tier-A path, the subscription token D133 keeps to Anthropic's own endpoint)
// sent to a host the policy never admitted. The answer itself is still the
// client's (a 3xx is neither a usage limit nor a 401), so it is relayed — with
// no credential in it — and this service simply does not go where it points.
//
// It is internal/gateway's own upstreamClient (gateway.go), respelled here for
// the reason carriesAny is: the package does not export it. The compression
// half is the same rule for the same reason — the transport must not ask for an
// encoding on its own, because an answer the service relays is an answer it has
// to be able to read (the usage-limit scan) and to scrub.
func upstreamClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
		out.Timeout = upstreamTimeout
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// upstreamKindAnthropic is gateway.Upstream's kind for Anthropic's own API
// with a key: the tier-B path. It is spelled out here rather than taken from
// the package because internal/gateway exports no constant for it — the one it
// does export is the subscription kind, and this package must never name that
// one: the test that keeps the subscription path to a single file refuses any
// file outside its list that mentions it.
const upstreamKindAnthropic = "anthropic"

// headerPrefixFugaro is the prefix of this contract's own headers. Every one
// of them is dropped before a call goes upstream: they carry the run's
// identity token and the job it claims, and Anthropic has no use for either.
// The job token in Authorization is already on the gateway's deny-list; these
// two are not, which is why they are dropped here by prefix rather than by
// name — a header this contract gains later is dropped by this line too.
const headerPrefixFugaro = "X-Fugaro-"

// redactedValue is what a scrubbed value is replaced with in a body. It is the
// gateway's own marker over an answer (route.go's `redacted`), spelled here
// because the package does not export it; the two are the same string and a
// body scrubbed by either reads the same way.
const redactedValue = "[redacted]"

// call is one call being answered: what it asks for, and its body read once so
// that an attempt can be repeated.
type call struct {
	path   string
	query  string
	body   []byte
	header http.Header
	// requested is whether the client asked for an event stream. It is the
	// gateway's own fallback (IsSSE) for an answer whose content type says
	// nothing, and it is read from the request the client built.
	requested bool
	// scrubs are the caller's own tokens: the run identity this service
	// verified, and the job's ID token it read from Authorization. Neither is
	// ever sent upstream, and the query of a call is dropped whole when it
	// carries either. They live for the length of one call and are written
	// nowhere.
	scrubs []string
	// probed is every tier this call made a probe on (C1.12), in the order
	// the probes were made and once per tier: what the answer carries to the
	// run (HeaderProbe), which settles each probe by it — notional on tier A,
	// money on any other tier (C1.16, the plan's rule for a probe). It is the
	// ledger input only: the names a probe was made on are the service's own
	// record (C1.13's audit line) and are not carried here.
	probed []string
}

// readCall reads the call the service was handed, and refuses a body larger
// than the gateway's own bound: the two sit in one path, so a body one of them
// accepts and the other refuses would be a request that works against one and
// not the other.
//
// The caller's own tokens — the run identity this service verified and the
// job's ID token it read from Authorization — are scrubbed out of the BODY
// here, and out of the headers before the call goes upstream (send). A token in
// a header other than Authorization, or pasted into the request body, is a
// token in Anthropic's logs: the gateway's own deny-list covers the one header
// it knows, and a rule that covered only that header would leave every other
// one, and the body, carrying it.
func readCall(r *http.Request, scrubs []string) (*call, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, gateway.MaxRequestBytes+1))
	if err != nil {
		return nil, errors.New("credsvc: the call's body could not be read")
	}
	if int64(len(body)) > gateway.MaxRequestBytes {
		return nil, fmt.Errorf("credsvc: the call's body is larger than the %d bytes this service accepts", int64(gateway.MaxRequestBytes))
	}
	for _, tok := range scrubs {
		if tok == "" {
			continue
		}
		// Whole bytes only: a token split by a JSON escape or re-encoded
		// character is not a token this can find, and a body edit that tried
		// would be guessing at the caller's encoding.
		body = bytes.ReplaceAll(body, []byte(tok), []byte(redactedValue))
	}
	h := r.Header.Clone()
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), headerPrefixFugaro) {
			delete(h, k)
		}
	}
	return &call{
		path:      r.URL.Path,
		query:     r.URL.RawQuery,
		body:      body,
		header:    h,
		requested: strings.Contains(r.Header.Get("Accept"), "text/event-stream"),
		scrubs:    scrubs,
	}, nil
}

// answer is one attempt's outcome: the upstream's response, and the credential
// it carried. The caller must close the body.
type answer struct {
	resp *http.Response
	// credential is the value that was sent. It is what the answer is redacted
	// with before it reaches the client, and it is never written anywhere else.
	credential string
}

// send makes one upstream call with one credential, using C1.8's helpers to
// build the headers: the subscription path for a tier-A credential (whose
// destination rule, D133, is inside OAuthHeaders and is therefore a check that
// runs and not a comment), the key path for a tier-B one, and the provider path
// for a provider key — whose destination is the base URL the bundle holds
// (D1418) and whose headers are the gateway's own provider rule.
//
// The caller's own tokens are never sent upstream: the job token in
// Authorization is dropped by the gateway's own deny-list, the run identity by
// the scrub below, and this contract's headers when the call was read.
//
// This is the call the workspace's meter is meant to bracket, and it is not
// bracketed yet: a call the workspace pays for is the one the meter takes the
// room for BEFORE it goes out and settles on the answer's usage
// (workspace_cap.go), and nothing here reserves. The wiring belongs in this
// file, and workspace_cap.go's header states what it adds and why it is a task
// of its own rather than a correction.
func (s *Server) send(ctx context.Context, cl *call, e Entry) (*answer, error) {
	dest, err := s.destination(cl, e)
	if err != nil {
		return nil, err
	}
	scrub := ""
	if len(cl.scrubs) > 0 {
		scrub = cl.scrubs[0]
	}
	up := gateway.Upstream{Kind: upstreamKindAnthropic, APIKey: e.Value()}
	var hdr http.Header
	var key string
	switch {
	case providerServed(e.Kind):
		// The provider path — the project's provider key (AK6b) or one
		// repository's named entry (KS4's route). It is chosen by KIND and not
		// by tier: the two facts the call needs — where it goes and how the key
		// is sent — are the entry's own, and they were validated when it was
		// read (providerValue), so neither the destination nor the header built
		// here can be one the loader refused. The credential is the entry's own
		// value, and the helper refuses a subscription credential on a route, as
		// every route's does (D133).
		hdr, key, err = gateway.RouteHeaders(providerRoute(e), cl.header, scrub)
	case e.Tier == creds.TierA:
		// Tier A: the subscription upstream, whose destination check lives in
		// the helper. BaseURL is "" — Anthropic's real address — and the check
		// runs on it, on the URL this call really goes to.
		up = gateway.Upstream{APIKey: e.Value()}
		hdr, key, err = gateway.OAuthHeaders(up, dest, cl.header, scrub)
	default:
		hdr, key, err = gateway.UpstreamHeaders(up, cl.header, scrub)
	}
	if err != nil {
		// The message names the rule, never a value: every error these two
		// helpers return is written that way (C1.8).
		return nil, err
	}
	// Every header that carries one of the caller's own tokens is dropped, not
	// only the Authorization header the gateway's deny-list knows: the scrub
	// above is over the one header, and a token in any other header of the call
	// is the same token reaching Anthropic's logs. gateway.RedactHeaders is the
	// gateway's own rule over a header set, exported for exactly this (C1.8),
	// and it deletes the header rather than editing its value, which is the
	// direction that cannot half-remove a token.
	for _, tok := range cl.scrubs {
		gateway.RedactHeaders(hdr, tok)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest, bytes.NewReader(cl.body))
	if err != nil {
		return nil, errors.New("credsvc: the upstream request could not be built")
	}
	req.Header = hdr
	req.ContentLength = int64(len(cl.body))
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		// The transport's own error is not carried: it quotes the URL, and a
		// URL of this call is the upstream's, which carries no credential —
		// but it also carries a request id and a host, and the rule here is
		// that the service's refusals are its own sentences.
		return nil, errors.New("credsvc: the upstream could not be reached")
	}
	return &answer{resp: resp, credential: key}, nil
}

// destination is where an attempt goes: the credential's own base, the call's
// path, and the call's query unless that query carries one of the caller's own
// tokens.
//
// The base is the entry's own when the entry is one a provider call is served
// from (providerServed) — the URL the document holds, which is the only place a
// provider's address exists (D1418):
// no task, no policy and no environment tells this service where a provider is,
// and a run is told nothing about it either — and Anthropic's otherwise. The
// join is the same one in both cases, and it is the gateway's own: a base may
// carry a path prefix ("https://openrouter.ai/api"), the call's path is
// appended to it (withRoute), and the query follows whole.
//
// The query is dropped whole rather than edited, and it is dropped when it
// carries either token the caller presented — the run identity or the job's
// ID token — in any case or percent-encoded. That is the gateway's own rule
// over its own token (upstreamQuery, carriesToken), which C1.8's helpers do
// not cover and which this service therefore has to: a token that reached the
// query of a call to Anthropic is a token in Anthropic's logs.
func (s *Server) destination(cl *call, e Entry) (string, error) {
	base := gateway.AnthropicBase(gateway.Upstream{})
	if providerServed(e.Kind) {
		base = e.baseURL
	}
	if base == "" {
		return "", errors.New("credsvc: the upstream has no address")
	}
	u := trimSlash(base) + cl.path
	if cl.query != "" && !carriesAny(cl.query, cl.scrubs) {
		u += "?" + cl.query
	}
	return u, nil
}

// providerRoute is the gateway's own Route for an entry a provider call is
// served from (providerServed: the project's provider key, AK6b, or one
// repository's named entry, KS4): the name it goes by in a log, the base URL the
// document holds, the auth shape it names, and the entry's value as the
// credential, read per call.
//
// It is a gateway.Route rather than a header set of this file's own so that a
// provider's call is built by the rule that already exists for one
// (gateway.RouteHeaders: the provider allow-list of request headers, the
// caller's own tokens scrubbed out, then AttachCredential, which writes the key
// under the route's own auth shape and refuses a subscription credential on a
// route). A second spelling of that rule here is exactly what C1.8 exists to
// prevent: the two forwarders must not drift.
//
// Models and FeePct are left zero: this service does not route by model ID (the
// call's model was already judged against the policy's own providers by the
// route, providerCall, and what a provider's route claims is the policy's and
// not this entry's) and it adds no fee (a fee is a run's budget, which lives in
// the runner's gateway and not here). Neither is read by RouteHeaders.
func providerRoute(e Entry) *gateway.Route {
	return &gateway.Route{
		Name:    e.Name,
		BaseURL: e.baseURL,
		Auth:    e.auth,
		Credential: func() (string, error) {
			return e.Value(), nil
		},
	}
}

// providerServed reports whether an entry is one a PROVIDER call is served
// from: where a call with it goes is the entry's own base URL (destination) and
// how it is sent is the gateway's provider rule (send). Two kinds qualify, and
// they are the same credential at two scopes: the project's single provider key
// (KindProvider, AK6b), which every repository of the installation shares, and
// one repository's own named provider credential (KindRepoCredential, C1's
// family, R2) — which is why the two questions are asked through one function
// and not by spelling the two kinds twice in each of them.
func providerServed(k Kind) bool { return k == KindProvider || k == KindRepoCredential }

// providerNamed is the provider a routed call is for, read off the CALL and not
// off the header set readCall builds: that one has already dropped every
// X-Fugaro- header (the provider's endpoint has no use for any of them), and
// the provider's name is this service's whole business on this route.
//
// It answers the name and, for a call that names none or that names something
// that cannot be a provider's name, the sentence the call is refused with. The
// value is deliberately not quoted back: what arrives here is a caller's text
// (C1's wire holds it to ValidProviderName before it is used for anything, and
// the refusal is the answer for a value that is not one), and a name that is not
// a provider's name is text this service has no sentence of its own about.
// TWO names are refused as well as none, because the header is a caller's and a
// caller that sent two has not named one: reading the first would be this
// service choosing between them.
func providerNamed(r *http.Request) (string, string) {
	held := r.Header.Values(HeaderProvider)
	switch len(held) {
	case 0:
		return "", "a call to the provider route names the provider it is for in " + HeaderProvider +
			", and this one names none"
	case 1:
	default:
		return "", "a call to the provider route names one provider, and this one names " + strconv.Itoa(len(held)) + " in " + HeaderProvider
	}
	if !ValidProviderName(held[0]) {
		return "", HeaderProvider + " carries a provider's name — one lowercase letter followed by up to nineteen lowercase letters, digits or dashes — and this call's value is not one"
	}
	return held[0], ""
}

// providerServedEntry is the entry a routed call is served from, derived from
// the run's VERIFIED repository and the provider the call named (R2, R11): the
// bundle's named entry for that pair, as the Entry the forwarder attaches and as
// the candidate the selection is made over.
//
// Both halves are the service's own facts and never the call's: slug is the
// identity the front door verified (creds.Identity.Slug) and name is the header,
// held to ValidProviderName before it is passed here — so a call can reach one
// repository's entry and no other's, which is HG1's whole reach rule, and there
// is no name a caller could send that would find somebody else's key.
//
// It reads the entry through C1's accessor and not out of the bundle's own map:
// ReadProviderKey is what the contract names as the reader of this family, and
// its "no such credential" is the answer a repository whose key was never copied
// meets (KS12). The Entry the forwarder needs is then assembled from the
// accessor's answer — the value and the two routing facts — plus the two facts
// the DOCUMENT KEY carries, which are read through the bundle's own naming rule
// (RepoCredentialKey) and its own key table (CredentialKey) rather than being
// restated here: the name an audit line and an answer's credential header carry
// is the document key and not the credential's bare name, because that bare name
// is a name another repository's entry of the family carries too (R5), and the
// tier is the credential half's answer.
//
// A pair the naming rule refuses is not served, and that is the same answer as
// an entry the document does not hold: neither is a call this service can serve,
// and the caller can do nothing different with the two.
func providerServedEntry(b *Bundle, slug, name string) (Entry, creds.Credential, bool) {
	pk, ok := ReadProviderKey(b, slug, name)
	if !ok {
		return Entry{}, creds.Credential{}, false
	}
	key, err := RepoCredentialKey(slug, name)
	if err != nil {
		return Entry{}, creds.Credential{}, false
	}
	held, kind, tier, ok := CredentialKey(key)
	if !ok || kind != KindRepoCredential {
		return Entry{}, creds.Credential{}, false
	}
	e := Entry{
		Kind: kind, Name: held, Tier: tier, Key: key,
		baseURL: pk.BaseURL, auth: pk.Auth, source: pk.Source,
		value: secret{value: pk.Value()},
	}
	// The candidate carries the provider the key is the key OF (K3's tie,
	// creds.Credential.Provider): the selection is handed a list of one, and
	// the tie is what says that this key is for the provider the call named —
	// which the model's own class rule cannot say on its own (checkCandidates).
	return e, creds.Credential{Name: held, Tier: tier, Provider: name}, true
}

// providerCall answers a call to this service's provider route (C1, KS4, R11):
// the one route of this service whose caller names the provider its call is for
// and whose rules are the published policy's rather than the cascade's.
//
// The route exists because a provider's key is not the project's and not a
// subscription's: a run whose model a provider serves is a run whose gateway
// routes the call, and the key it routes with is the installation's — held in
// the bundle, one entry per repository (R2, D3008) — so the call arrives here
// with the provider's NAME on it and no key at all, and this handler derives the
// entry from the repository the run identity names and forwards.
//
// What it does, in order, and why each step is where it is:
//
//   - the CALLER'S CLASS, before anything at all. A review caller is refused the
//     route whole (spec §2.3, C3.9's variant): its credentials are its account's
//     own list, resolved by the pairing (candidates reads ClassReview before the
//     cascade narrows anything, and reviewCandidates offers that one account and
//     no tier beyond it), so a repository's own named provider entry — the one
//     credential this route exists to attach — is never a review call's to be
//     served by. Nothing is read for it, not even the header below: a class the
//     route does not serve must not reach the policy or the bundle at all, which
//     is the shape the sign route refuses the other classes with.
//   - the provider's name, off the call's own header and held to the wire's
//     shape rule. Nothing is read before it: a call that names no provider is
//     not a call this route can answer anything about, and the sentence is about
//     the header.
//   - the project's published policy. It is a policy read and not a credential,
//     and it is the document one of the two refusals this route has comes from.
//   - the policy's rules over the provider and the model: the block must NAME
//     the provider, it must publish a model allow-list at all (a project that
//     publishes none refuses every provider call — requestNoAllowListRefusal's
//     rule, F32 — rather than being forwarded under no ceiling), the call must
//     name a model, and the named provider's own models list must claim it. The
//     last one is the tie the whole route exists for: one provider's key is
//     never sent to another provider's model, and which models a provider claims
//     is what its block says (config.PatternMatches, the primitive the gateway's
//     own routes match with, so a route's answer and this one cannot disagree).
//   - the request itself, on the messages endpoint, through the gateway's own
//     check over the routes the policy builds (providerRequestRefusal). This is
//     the half that keeps a HAND-BUILT call from escaping the gateway's rules
//     (D3003): an agent holding the job's own token can compose a body and post
//     it here, and every rule the gateway would apply to a routed call — the
//     project's pin, the variant suffix, the foreign field, the shape, the
//     max_tokens bound — is applied here before any credential is read. The
//     token count's body is not held to those rules, for the reason the messages
//     route gives: it carries no max_tokens and the gateway does not hold it to
//     them either (its own forwardFree).
//   - the bundle, and the entry the verified repository and the provider's name
//     derive. This is the first credential read of this path.
//   - the selection, over a list of ONE (creds.Selector.MayServe): the entry is
//     the only candidate, and the selection is what decides whether it may serve
//     this call at all — the model's class rule, the provider tie and the Check
//     50 gate for an Anthropic model are creds' own rules (D3000, D150), and
//     this service reaches every one of them through the same judgement the
//     cascade does rather than by judging them again here. What a Claude model on
//     this route meets is therefore Check 50's own refusal, spelled once. It is
//     the judgement WITHOUT the placement, and that is deliberate: MayServe
//     records nothing, because a placement is one fact — where this run is, its
//     current credential and its lease together — and this route's credential is
//     DERIVED (the run's verified repository and the provider's name, the same
//     entry for every call), so there is no decision to record and nothing for
//     the run to be sticky about. Placing it would be the harm: a run that mixes
//     Claude calls with routed ones is on a shared subscription token for the
//     first, and the routed call would move it onto the provider's entry and take
//     the lease it holds on the account with it — freeing that account's
//     concurrency slot (D142) while the run is still using it and leaving the
//     next Claude call with no current credential to stay on (D154, the prompt
//     cache) or a wait on the cap. A run's placement is the cascade's to write,
//     and only for a call the cascade served.
//   - the forward itself, and the answer: one attempt, no retry, no mark, no
//     usage-limit scan, and no switch.
//
// The last two are the parts this route does NOT do that the cascade's forward
// does, and each is left out for a reason of its own. A retry and the mark a
// failed attempt leaves are the cascade's business — they move a run between
// credentials of a pool, and a call whose credential is derived from its own
// repository has no second one to move to: the entry is the only thing this
// repository's provider key is, so the honest answer to an upstream that refused
// it is that answer. The usage-limit scan is the cascade's too: it reads the
// upstream's own "you are out of quota" event and marks the credential, and a
// provider's quota is not a Fugaro account's (it is the owner's with that
// provider, and a mark on the family's entry would idle every call this
// repository routes for a window nobody here can see). The SWITCH is absent for
// the same reason: a switch is a move to another credential of the pool, and
// there is no other.
//
// A slip does not fence this route, and that is the slip rule read exactly: what
// a slip must name is a credential of the per-user or project grammar
// (slipRequiresSlip), and a named provider entry is a bundle key of neither
// (C1's family), like every other entry the bundle itself holds.
func (s *Server) providerCall(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, purpose string, req Request) {
	if id.JobClass == ClassReview {
		// The class fence above, and it is the FIRST statement for its reason: a
		// review job runs the same runner as any other and holds the run identity
		// and the job's own ID token, so it can post here directly (D3003's
		// hand-built call) — and what it would be handed is the one entry its class
		// is never served by.
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"the provider route is served to a coding caller's account only: a review caller is served the model path from its own account's list, and a repository's named provider entry is never one of them")
		return
	}
	sc := scopeOfCall(ctx, s)
	name, why := providerNamed(r)
	if why != "" {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+why)
		return
	}
	shared, found, err := sc.Policy().Shared(ctx)
	if err != nil || !found {
		s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the project's policy could not be read", labelRead(readPolicyShared, err))
		return
	}
	provider, named := shared.Providers[name]
	if !named {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorProviderNotAllowed,
			requestRefused+"the project's published providers block names no provider "+strconv.Quote(name)+
				", so a call to this route has no provider to be served by")
		return
	}
	if noAllowList := requestNoAllowListRefusal(shared); noAllowList != "" {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorProviderNotAllowed, requestRefused+noAllowList)
		return
	}
	cl, err := readCall(r, callScrubs(ctx, req))
	if err != nil {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	// The provider's own path: the route's path with this service's prefix cut
	// off. It is a shape check and not a rewrite — the two endpoints of this
	// route are the two C1 declares, and a path that is one of them under the
	// prefix is the whole of what a provider may be reached with. servedRoute
	// admitted this call, so the check cannot fail; it is made because the path
	// the provider is reached on is what the entry's base URL is joined to, and a
	// join made from an unread path is a call to an address nobody chose.
	endpoint, cut := strings.CutPrefix(cl.path, ProviderRoutePrefix)
	if !cut || (endpoint != MessagesPath && endpoint != CountTokensPath) {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the provider route was reached on a path it does not serve")
		return
	}
	cl.path = endpoint
	if cl.path == MessagesPath {
		typ, refusal, err := s.providerRequestRefusal(shared, cl.body)
		if err != nil {
			// The project's own prices could not be read: the same state the
			// messages route refuses on, and the same read named on the line.
			s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
				"the project's model prices could not be read", labelRead(readPolicyPrices, err))
			return
		}
		if refusal != "" {
			s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, typ, requestRefused+refusal)
			return
		}
	}
	model, _ := callModel(cl.body)
	if model == "" {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorProviderNotAllowed,
			requestRefused+"a routed call names the model it is for, so that the provider that serves it can be read off it, and this one names none")
		return
	}
	if !slices.ContainsFunc(provider.Models, func(pat string) bool { return config.PatternMatches(pat, model) }) {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorProviderNotAllowed,
			requestRefused+"the provider "+strconv.Quote(name)+" does not serve the model this call names: a provider's models list is what says which models its key may be sent for, and one provider's key never serves another provider's model")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			BundleRefusal(err), labelRead(readBundle, err))
		return
	}
	e, cand, ok := providerServedEntry(b, id.Slug, name)
	if !ok {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorProviderNotServed,
			"the bundle holds no provider key for "+strconv.Quote(name)+" and this repository: a call to this route is served the entry the run's own repository and the provider's name derive, and one is copied there for each repository whose runs may route to it")
		return
	}
	// The selector is built over the SCOPE's state and not the deployment's, for
	// the cascade's reason (R1, R8): the marks this call is judged by are the ones
	// the scope that answered the caller holds.
	sel := &creds.Selector{Store: sc.State(), Settings: settingsOf(shared), Now: s.now, Model: model, Provider: name, Strategy: creds.StrategyFailover}
	if err := sel.MayServe(ctx, creds.Run{Slug: id.Slug, ID: id.Run}, cand); err != nil {
		// The selector's own refusals, each spelled once: what this call cannot
		// be served by (the class rule and the provider tie — checkCandidates),
		// Check 50's gate for a Claude model, a wait on an entry the owner's own
		// marks hold, and an unreadable state.
		s.refuseSelection(ctx, w, id, err, nil)
		return
	}
	ans, err := s.send(ctx, cl, e)
	if err != nil {
		s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			err.Error(), labelRead(readUpstream, err))
		return
	}
	s.answer(ctx, w, id, cl, ans, cand, nil, nil)
}

// trimSlash drops a base URL's trailing slashes, so a prefix and a path are
// joined by exactly one of them. It is internal/gateway's own join over the
// same input (route.go's trimSlash, which withRoute and AnthropicBase both
// use), respelled here for the reason carriesAny is: the package does not
// export it.
func trimSlash(s string) string { return strings.TrimRight(s, "/") }

// carriesAny reports whether v holds any of the tokens, in any case or
// percent-encoded, with a bad escape left as it is so it cannot hide one. It
// is internal/gateway's carriesToken over a list, respelled because the
// package does not export it.
func carriesAny(v string, tokens []string) bool {
	lv := strings.ToLower(v)
	lu := strings.ToLower(lenientUnescape(lv))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		t := strings.ToLower(tok)
		if strings.Contains(lv, t) || strings.Contains(lu, t) {
			return true
		}
	}
	return false
}

// lenientUnescape decodes every valid %XX (and "+") and keeps the rest. It is
// internal/gateway's own function over the same input, respelled here for the
// reason carriesAny is.
func lenientUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		case c == '+':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// A usage-limit answer, in the two shapes this release can see.
//
// U3 is open: Check 90 (2026-10-21) observed no usage limit and provoked none,
// so which status, error type and headers carry one for subscription traffic is
// still unknown. What is known is where to look, and F15 says it plainly:
// detection is done on the HTTP response — the status, the error type and the
// reset headers — and never by parsing Claude Code's text.
//
// The two shapes:
//
//   - a 429 status. It is decisive on its own: the status is the authority and
//     the body is a hint, so the body is not read at all (reading it would mean
//     holding an answer this service must stream). The reset comes from the
//     headers, and a 429 that names none is the backed-off case C1.9 already
//     handles.
//   - a 2xx whose *stream* carries an error event naming rate_limit_error.
//     This is the shape a streaming call meets, and it is why the qualifier
//     "only before the first byte" in the retry rule is a real case rather than
//     a dead one: the answer has already begun, so the credential is marked and
//     the call is NOT retried.
//
// Neither shape reads a credential's value, and neither carries one into a
// mark: a mark is a state and a time.
const rateLimitEventType = "rate_limit_error"

// usageLimitStatus reports whether an answer's status is a usage limit.
func usageLimitStatus(status int) bool { return status == http.StatusTooManyRequests }

// usageLimitScanner looks for a usage-limit error event in a streamed answer.
// It is bounded: it keeps only the last usageLimitTail bytes, so a stream of
// any length is scanned with a constant amount of memory, and it keeps them
// because an event can straddle two chunks.
type usageLimitScanner struct {
	tail []byte
	seen bool
}

// usageLimitTail is how much of the stream is held back to join to the next
// chunk. It is far larger than one error event (a few hundred bytes) and small
// enough to be irrelevant beside a stream's buffer.
const usageLimitTail = 4 << 10

// rateLimitEvent is the error event, in the two spellings a JSON encoder
// produces for it: compact, which is what Anthropic's own stream uses, and
// with a space after the colon.
var rateLimitEvents = [][]byte{
	[]byte(`"type":"` + rateLimitEventType + `"`),
	[]byte(`"type": "` + rateLimitEventType + `"`),
}

// scan feeds one chunk of the streamed answer to the scanner. It never fails
// and never reports: what it learns is read after the stream ends (Found), so
// that nothing it does can slow the answer down or change it.
func (u *usageLimitScanner) scan(chunk []byte) {
	if u.seen {
		return
	}
	hay := make([]byte, 0, len(u.tail)+len(chunk))
	hay = append(hay, u.tail...)
	hay = append(hay, chunk...)
	for _, ev := range rateLimitEvents {
		if bytes.Contains(hay, ev) {
			u.seen = true
			break
		}
	}
	if len(hay) > usageLimitTail {
		hay = hay[len(hay)-usageLimitTail:]
	}
	u.tail = append(u.tail[:0], hay...)
}

// Found reports whether a usage-limit event was seen in the stream.
func (u *usageLimitScanner) Found() bool { return u.seen }

// usageLimitReset reads the reset a usage-limit answer named, and the zero time
// when it named none — which is the backed-off case (C1.9's Exhaust).
//
// The headers are the ones a rate-limited HTTP answer carries: Retry-After
// (delta-seconds, or an HTTP date), and Anthropic's own unified rate-limit
// resets, which are RFC 3339 instants. Which of them carries a *subscription*
// usage limit is U3's open question, so this reads all of them and takes the
// LATEST: a credential held for longer than it needed to be costs a wait, and
// the wait-or-switch rule is exactly what a wait is for, while a credential
// released before the upstream was ready to serve it costs another 429 and,
// under D142's first control, the whole window. Holding is the fail-closed
// direction, and a hold may only ever be extended (C1.9's Exhaust).
//
// The reset headers are read from the answer, never from the body: the body of
// a streamed answer is on its way to the client.
func usageLimitReset(h http.Header, now time.Time) time.Time {
	var out time.Time
	keep := func(t time.Time) {
		if t.After(now) && t.After(out) {
			out = t
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
			keep(now.Add(time.Duration(secs) * time.Second))
		} else if t, err := http.ParseTime(v); err == nil {
			keep(t)
		}
	}
	for _, name := range []string{
		"Anthropic-Ratelimit-Unified-5h-Reset",
		"Anthropic-Ratelimit-Unified-7d-Reset",
	} {
		if v := h.Get(name); v != "" {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
				keep(t)
			}
		}
	}
	return out
}

// unauthorizedStatus reports whether an answer says the credential is dead.
// Only 401 is D139's mark: a 403 is Anthropic refusing a request (a model the
// key may not use, a region it may not serve), and marking a credential broken
// for it would take a working token out of rotation until an operator cleared
// it.
func unauthorizedStatus(status int) bool { return status == http.StatusUnauthorized }

// answerIsStream reports whether an upstream answer is an event stream, by its
// media type, falling back to what the client asked for (C1.8's IsSSE, which
// is the gateway's own rule).
func answerIsStream(resp *http.Response, requested bool) bool {
	return gateway.IsSSE(resp.Header, requested)
}

// dropAnswerHeaders are the headers of an upstream answer that never reach the
// client. They are internal/gateway's own lists (hopByHop,
// dropResponseHeaders), respelled here because the package exports neither:
// they identify the organization behind the account, set state on this
// service's origin, or describe a hop the client is not part of.
var dropAnswerHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Proxy-Authenticate": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
	"Anthropic-Organization-Id": true, "Set-Cookie": true, "Set-Cookie2": true,
	"Content-Length": true,
}

// copyAnswerHeaders copies an upstream answer's headers to the client's, less
// the ones above. The credential is redacted out of what is copied
// (gateway.RedactHeaders) because an upstream that echoed the caller's token
// back — in a header, an error, a request id — must not have it echoed on to
// the client: the no-echo rule is over the whole answer and not only the body.
func copyAnswerHeaders(dst, src http.Header, credential string) {
	for k, vs := range src {
		ck := http.CanonicalHeaderKey(k)
		if dropAnswerHeaders[ck] || strings.HasPrefix(ck, "X-Fugaro-") {
			// The upstream never speaks in the service's own answer headers
			// (the tier, the credential, the switch, the probes): they are the
			// service's to write, and one the upstream sent would be charged or
			// believed as the service's.
			continue
		}
		dst[ck] = append([]string(nil), vs...)
	}
	if credential != "" {
		gateway.RedactHeaders(dst, credential)
	}
}
