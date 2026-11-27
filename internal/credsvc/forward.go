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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
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
	// chain is the walk this call is served by, and nil for a call served the
	// way every call was served before the routing table existed (CS1). It is
	// this call's own — the chain is walked from its first source at every call
	// (R16) — and it is what forward asks instead of the selector when it is
	// set, and what the answer carries the source of (chainAnswer).
	chain *chainWalk
	// stage is whether the caller sent the stage header (SentStage), read from
	// the request BEFORE header is stripped of every X-Fugaro- member — which
	// is the whole of what readCall keeps of it, and the whole of what the
	// answer needs to decide whether it may carry this contract's three
	// members (chainAnswer, R24, F57). It is a bool and not the header itself
	// because the header, and every X-Fugaro- member with it, is what C1.10
	// refuses to send upstream: a call that carried the stage on would hand the
	// agent's own stage name to the model's API.
	stage bool
	// probed is every tier this call made a probe on (C1.12), in the order
	// the probes were made and once per tier: what the answer carries to the
	// run (HeaderProbe), which settles each probe by it — notional on tier A,
	// money on any other tier (C1.16, the plan's rule for a probe). It is the
	// ledger input only: the names a probe was made on are the service's own
	// record (C1.13's audit line) and are not carried here.
	probed []string
	// meter is the workspace's cap over this call, and nil when the call's
	// scope has no meter (every call an installation's service serves) or the
	// call is not one the workspace's own credentials pay for (WS8). A call
	// that has one reserves before it goes out and settles on the answer's
	// usage, and a probe carries its own (probeCall's result).
	meter *callMeter
	// fail is the refusal the meter made of THIS call, and nil when it made
	// none. It is set beside the error send answers (errMeterRefused), because
	// a refusal has a sentence, a marker and a credential that an error value
	// cannot carry, and it is cleared by nothing: the call is over when it is
	// set.
	fail *meterFail
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
	stage := SentStage(r.Header)
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
		stage:     stage,
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
// What every source of the chain shares — the scrub of the caller's own tokens,
// the workspace's reservation, the transport's own failure — is sendRequest's,
// and this function's job ends at building what that call carries: where it
// goes and how it is authenticated.
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
	// The model the workspace's meter prices is the one this call carries: the
	// member of the body itself, which is the spelling this source is served
	// with once CS3's rewrite has run. Unreadable is "", which the meter
	// refuses on as a call it cannot price.
	model, _ := callModel(cl.body)
	return s.sendRequest(ctx, cl, sentRequest{
		dest:       dest,
		body:       cl.body,
		header:     hdr,
		credential: key,
		name:       e.Name,
		tier:       e.Tier,
		model:      model,
	})
}

// sentRequest is one attempt as it goes out: where it is sent, the body it
// carries, the headers it is sent with, and the attempt's own facts — the
// credential the answer is reported with (its value, kept out of everything
// handed on), the name and tier a meter refusal is reported with, and the model
// the workspace's meter prices.
//
// It is a value rather than a list of arguments because the sources of the chain
// build these differently: a key's call is the caller's own path and body (send),
// while Vertex's is translated onto another wire entirely (serveVertex, where
// the model is in the URL path and not in the body at all, so the model the
// meter prices cannot be read off the body it sends).
type sentRequest struct {
	dest       string
	body       []byte
	header     http.Header
	credential string
	name       string
	tier       creds.Tier
	model      string
}

// sendRequest makes one attempt: it strips every header that carries one of the
// caller's own tokens, takes the workspace's room for the call, sends it, and
// answers what came back.
//
// This is the call the workspace's meter brackets (WS8): a call the workspace's
// own credentials pay for takes the room for its worst case out of the
// workspace's month BEFORE the request goes out — a probe included, since a
// probe is a call the workspace pays for too (C1.16) — and the answer settles
// it on the usage the response reported (the caller's half: settleCall). A
// reservation that cannot be taken, a model the price table cannot price and a
// table that cannot be read all refuse the call here, with the refusal on the
// call (cl.fail) and errMeterRefused as the error, so the caller answers the
// workspace's own refusal rather than the upstream-unreachable one.
//
// A call whose scope has no meter (an installation's, every call this release
// serves at one) is untouched by any of it: cl.meter is nil and the reservation
// is not taken at all.
func (s *Server) sendRequest(ctx context.Context, cl *call, req sentRequest) (*answer, error) {
	// Every header that carries one of the caller's own tokens is dropped, not
	// only the Authorization header the gateway's deny-list knows: the scrub
	// above is over the one header, and a token in any other header of the call
	// is the same token reaching Anthropic's logs. gateway.RedactHeaders is the
	// gateway's own rule over a header set, exported for exactly this (C1.8),
	// and it deletes the header rather than editing its value, which is the
	// direction that cannot half-remove a token.
	for _, tok := range cl.scrubs {
		gateway.RedactHeaders(req.header, tok)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.dest, bytes.NewReader(req.body))
	if err != nil {
		return nil, errors.New("credsvc: the upstream request could not be built")
	}
	httpReq.Header = req.header
	httpReq.ContentLength = int64(len(req.body))
	// The room is taken here, after the request is built and validated and
	// immediately before it goes out: a reservation for a call that was never
	// made would hold room nothing will settle, and the reservation must not
	// be held across a header build that can refuse the call for its own
	// reasons.
	if cl.meter != nil && paidCall(cl, req.tier) && !cl.meter.take(ctx, s, cl, req) {
		return nil, errMeterRefused
	}
	resp, err := s.cfg.Client.Do(httpReq)
	if err != nil {
		// The transport's own error is not carried: it quotes the URL, and a
		// URL of this call is the upstream's, which carries no credential —
		// but it also carries a request id and a host, and the rule here is
		// that the service's refusals are its own sentences.
		//
		// The reservation stands and is settled at what the call was charged
		// for it, which is the fail-closed direction: whether the request
		// reached the upstream is exactly what a transport error does not say.
		// The refusal the run gets is the unreachable-upstream one either way
		// (the call did not go out), so a settle that fails here is the
		// service's log line and nothing else.
		if fail := s.settleCall(ctx, cl, nil); fail != nil {
			s.log.Error("credsvc: a metered call's reservation could not be settled after the upstream could not be reached",
				"credential", fail.credential, "tier", fail.tier)
		}
		return nil, errors.New("credsvc: the upstream could not be reached")
	}
	return &answer{resp: resp, credential: req.credential}, nil
}

// The workspace's cap, taken where the money is spent (WS8, D1301, F10, F18,
// R15): the room for a call's worst case is reserved out of the workspace's
// month BEFORE the request goes out, the answer settles it on the usage the
// response reported, and a call whose cost cannot be recorded is refused rather
// than served — on every path whose answer is still in hand, which is every path
// but a streamed one (the second rule below). The transaction itself is
// internal/budget/workspace.go's and the meter over it is workspace_cap.go's;
// what is here is the bracket around one upstream call, which is the only place
// a call can be priced and the only place its usage is readable.
//
// Four rules of the shape are worth stating where the code is:
//
//   - the worst case is what is reserved, always. What a call will cost is not
//     known until its answer reports usage, so the amount held is the table's
//     own bound for the request (pricing.Model.WorstCase) — the same figure the
//     gateway reserves before its own calls, from the same table. A call that
//     settles for less returns the difference at the settle, and a call whose
//     answer never reported usage — a stream cut in half — settles at the
//     bound, which is the direction a cap must fail in.
//   - a settle that cannot be WRITTEN is a different case from either, and only
//     the whole-body path can refuse a call on it: a whole body and a probe are
//     read for their usage before any byte of them leaves, so the refusal
//     withholds an answer the run never got. A streamed answer has already left
//     by the time its usage is known, so a settle that cannot be written there
//     is the service's log line and nothing else: the reservation is not
//     charged, it stands until it ages out, and the release then leaves the
//     call recorded as nothing at all — a month short by its whole worst case
//     with no figure saying so. SECURITY.md states the exception beside the
//     fail-closed rule it limits, and
//     TestAStreamedSettleThatFailsIsServedAndUncharged pins it here.
//   - a reservation is taken for every attempt, a probe included: a probe is a
//     call the workspace's credential pays for (C1.16), and a workspace whose
//     cap cannot see its probes has a cap that leaks a call per probe.
//   - the meter is the SCOPE's (C1, R1): a workspace's member is metered
//     against the workspace's own database and month, and a call an
//     installation's service serves has no meter at all, so nothing an
//     installation does changes (the acceptance's own last clause).
//
// Nothing here writes to a member: the member is the caller's own verified
// project (`fp`), the figure is the workspace's, and what the member learns
// travels on the answer the service is already sending (SetWorkspaceHeadroom).

// errMeterRefused is what send answers when the workspace's meter refused the
// call. The refusal is on the call (call.fail) rather than in the error,
// because a refusal carries a sentence, a marker and the credential it was made
// on, and an error value carries none of them. It is a sentinel and not a
// formatted error: the caller asks whether the meter refused, and answers
// cl.fail.
var errMeterRefused = errors.New("credsvc: the workspace's meter refused this call")

// meterFail is one refusal the meter made, with everything the refusal needs:
// the sentence the run reads, the marker the member's gateway reads
// (gateway.WorkspaceRefusalHeader), the credential the attempt was made with,
// and the room the member is answered with when a read of the month produced
// one.
type meterFail struct {
	message    string
	marker     string
	credential string
	tier       string
	room       budget.WorkspaceRoom
	hasRoom    bool
}

// callMeter is the workspace's cap over ONE call: the room it took before the
// call went out and everything settling it needs. It is one per call and never
// shared — a probe carries its own (probeCall's result) — and it holds one live
// reservation at a time, which is what makes settleCall's "settle exactly once
// whatever happens" a property of the struct rather than of the code that
// happens to run.
type callMeter struct {
	meter *WorkspaceMeter
	// member is the workspace member the call is spent for: the caller's own
	// verified project, which is the only identity the workspace's meter knows
	// (the member table's own key). run is the verified run the call belongs
	// to, and it is what the reservation's call id is built from so that a
	// retry of one call is identifiable in the workspace's month.
	member string
	run    string

	// credential and tier are the attempt's, and they are what a meter refusal
	// is reported with: the same names every other refusal on this path
	// carries.
	credential string
	tier       string

	// hold is the reservation and reserved says whether it is still live. A
	// call that has settled clears it, and settleCall does nothing on a call
	// that holds none.
	hold     budget.WorkspaceHold
	reserved bool

	// room is the workspace's room as the last read of the month answered it,
	// and hasRoom says whether one ever did. It is the advisory figure the
	// answer carries (SetWorkspaceHeadroom).
	room    budget.WorkspaceRoom
	hasRoom bool

	// model and row are the priced model the call named, and ttl the longest
	// cache write the request can cause — the two inputs the settle needs and
	// the reserve already had.
	model string
	row   pricing.Model
	ttl   string

	// prices is the price table, read once per call and memoized: the reserve
	// needs it for the worst case and the settle for the rates, and reading
	// the project's policy twice for one call would be two reads of the same
	// node for one answer.
	prices   *pricing.Table
	priced   bool
	priceErr error
}

// meterOf is the meter of the call's scope, and nil when the scope has none.
// Every call an installation's service serves answers nil here, which is what
// "an installation's scope has no meter and is unchanged" means in code.
func meterOf(ctx context.Context, s *Server) *WorkspaceMeter {
	sc := scopeOfCall(ctx, s)
	if sc == nil {
		return nil
	}
	return sc.Meter()
}

// paidCall reports whether a call is one the workspace's own credentials pay
// for, which is the whole of what the meter brackets.
//
// Two conditions, and each is a statement about money rather than about
// routing:
//
//   - the Messages path. A token count is free on every tier — the gateway
//     forwards it with no reservation of its own (its own forwardFree) and no
//     credential is spent answering it — so there is nothing for a cap to take
//     room for.
//   - a tier that is not A. A subscription token's calls are the donor's own
//     quota and not the workspace's money, so a call served by one reserves
//     nothing at all (R15, D1300). Every other tier — the project's paid
//     Anthropic key, a provider key — is money the workspace pays.
func paidCall(cl *call, tier creds.Tier) bool {
	return cl.path == MessagesPath && tier != creds.TierA
}

// withMeter attaches the workspace's meter to a call, and answers it. It
// answers nil for a call the meter does not bracket: a scope with no meter (an
// installation's) and a path the workspace pays nothing for (a token count).
//
// The caller's identity is the source of both the member and the run — the
// verified project claim and the verified run id — and neither is carried in
// the request context, which is why the meter is attached where an Identity is
// in hand rather than read where the reservation is taken. A call that resolves
// to no member or no run is left unattached-but-refused: the meter's own
// answer to it (take) is a refusal, because a workspace that cannot say which
// member a call is spent for cannot charge it to one, and serving it anyway
// would be a call outside the cap.
func (s *Server) withMeter(ctx context.Context, cl *call, id Identity) *callMeter {
	if cl.path != MessagesPath {
		return nil
	}
	m := meterOf(ctx, s)
	if m == nil {
		return nil
	}
	cl.meter = &callMeter{meter: m, member: id.Project, run: id.Run}
	return cl.meter
}

// take reserves the room for one call, and answers whether the call may go out.
// A refusal is on the call (cl.fail) and this answers false; the error send
// returns beside it is errMeterRefused.
//
// What it prices and what it bounds the call by are the attempt's own: the
// model is the one that will be SERVED — the body's own member for a source
// that sends the caller's body, and the model that source serves for one that
// translates the call, as Vertex does, whose body names no model at all — and
// the worst case is the body that goes out.
//
// Every failure here is a refusal and never a pass, including the ones that
// read as internal trouble (a price table that cannot be read, a month that
// cannot be written): a call served with no reservation is a call that can pass
// the cap, and a cap that opens when its own database is unreadable is not a
// cap.
func (cm *callMeter) take(ctx context.Context, s *Server, cl *call, req sentRequest) bool {
	cm.credential, cm.tier = req.name, wireTier(req.tier)
	if cm.member == "" || cm.run == "" {
		// Unreachable through ServeHTTP, which verifies both before it serves
		// anything. A refusal rather than a reservation under an empty name,
		// which the meter itself would refuse for the same reason.
		cl.fail = cm.fail("the call resolved to no workspace member, so the workspace did not take the room for it", "", nil)
		return false
	}
	if req.model == "" {
		cl.fail = cm.fail("the call names no model this service can price, so the workspace did not take the room for it", "", nil)
		return false
	}
	cm.model = req.model
	table, err := cm.priceTable(s)
	if err != nil {
		cl.fail = cm.fail("the workspace's price table could not be built, so the workspace did not pay for this call", "", nil)
		return false
	}
	row, ok := table.Lookup(req.model)
	if !ok {
		// A model the table cannot price is refused rather than priced at a
		// guess: an unpriced call is a call whose cost no cap can see, and the
		// catalog's own sentence (when it recorded one) names the model and
		// both ways out. This is the check the gateway's own path makes for a
		// provider route, made here for every tier — the workspace's money is
		// the workspace's money whatever the credential.
		message := "the workspace's price table has no price for " + req.model + ", so the workspace did not pay for this call"
		if why := table.Refusal(req.model); why != "" {
			message = "the workspace did not pay for this call: " + why
		}
		cl.fail = cm.fail(message, "", nil)
		return false
	}
	cm.row, cm.ttl = row, meterCacheTTL(req.body)
	call, err := callID(cm.run)
	if err != nil {
		// The random half is what makes the id unique; without it two calls of
		// one run could not be told apart, and the meter would refuse the
		// second as a duplicate of the first.
		cl.fail = cm.fail("the workspace could not identify this call, so it did not take the room for it", "", nil)
		return false
	}
	amount := row.WorstCase(meterRequest(req.body, row))
	hold, room, err := cm.meter.Reserve(ctx, cm.member, call, amount)
	switch {
	case errors.Is(err, budget.ErrWorkspaceCeilingReached):
		// The ceiling is the one refusal with a number to hand the member: the
		// room the workspace has left, which is what the member's own gateway
		// compares its next calls against (gateway.WorkspaceHeadroomHeader).
		cl.fail = cm.fail(fmt.Sprintf("the workspace's cap leaves %s this month and this call needs up to %s, so the workspace did not make it",
			meterDollars(room.Headroom), meterDollars(amount)), gateway.WorkspaceRefusalCap, &room)
		return false
	case err != nil:
		cl.fail = cm.fail("the workspace could not take the room for this call, so the call was not made", "", nil)
		return false
	}
	cm.hold, cm.reserved, cm.room, cm.hasRoom = hold, true, room, true
	return true
}

// fail builds the refusal an answer carries: this attempt's credential and
// tier, and the room when a read of the month produced one.
func (cm *callMeter) fail(message, marker string, room *budget.WorkspaceRoom) *meterFail {
	f := &meterFail{message: message, marker: marker, credential: cm.credential, tier: cm.tier}
	if room != nil {
		f.room, f.hasRoom = *room, true
	}
	return f
}

// priceTable is the table the call is priced from: the embedded catalogue, with
// no project's overrides over it.
//
// **The workspace's meter must never be priced by the member's own numbers.**
// The scope a workspace's call resolves to answers Policy() from the MEMBER's
// own published policy — RTDBPolicy over the database the member's verified
// `iss` names (scope_workspace.go) — and `model_prices` there is written by the
// member's own administrator, who is exactly the party the workspace's cap
// exists to hold (SECURITY.md's residual risk 1: a member whose operator spends
// hard spends the workspace's month). A price is checked only for being above
// zero (pricing.go), so a member that published near-zero prices would have
// every reservation and every settle of its calls come out at about nothing and
// the workspace's cap bind nothing for it — with the reconciliation alarm that
// would read the difference still running nowhere (D2991). The catalogue is the
// workspace's own figure because it is nobody's to edit, and the plan's file for
// a workspace of its own prices is not this task's: a model the catalogue does
// not price is refused here rather than priced by the member's number, which is
// the fail-closed direction every other refusal on this path takes.
//
// The read is memoized per call: the reserve needs the row and the settle its
// rates, and one answer must not be priced from two tables.
func (cm *callMeter) priceTable(s *Server) (*pricing.Table, error) {
	if cm.priced {
		return cm.prices, cm.priceErr
	}
	cm.priced = true
	cm.prices, cm.priceErr = requestPriceTable(nil, s.now())
	return cm.prices, cm.priceErr
}

// The attempts a STREAMED call's settle is given once the meter's own write has
// given up. The meter re-reads the month and re-writes it up to
// budget.WorkspaceSettleAttempts times (fifteen, three times a reserve's) with a
// jittered wait that tops out at a quarter of a second, and that is the whole of
// the bound for a call whose answer is still in the service's hands: a settle
// that loses it refuses the call, so the cost is not lost. A streamed call
// cannot be refused — its answer has already left — so its settle is the one
// that must not give up on the first refusal, and these are the attempts it has.
//
// The wait is longer than the meter's own and grows with each failed run: what
// this catches is a burst of settles colliding on the one month node every
// member's calls share, and the burst has to have passed for the write to land.
// The attempts are bounded, and they are made before the handler returns rather
// than after it: a Cloud Run instance with no CPU outside a request would never
// run a retry scheduled behind the answer's last byte.
const (
	streamSettleAttempts   = 4
	streamSettleBackoff    = 250 * time.Millisecond
	streamSettleBackoffMax = 2 * time.Second
)

// settleCall records what a metered call cost and ends its reservation, and
// answers the refusal a failure is (nil when the call settled, and nil too when
// it holds no reservation: a call the meter did not bracket settles nothing).
//
// use is the usage read off the answer, and nil is "the answer's usage is not
// readable": a stream cut before its usage, a body past the reader's bound, an
// answer that never reported a complete pair of counts. The call then settles
// at its reservation — what the call was already charged for a cost nothing
// could read — which is the gateway's own rule read literally ("until an
// outcome is known it is charged its full reservation") and the fail-closed one
// for a cap.
//
// ONE attempt is made here. A call this can still refuse is refused on it, which
// is what keeps the cost from being lost; a streamed call is the case where
// refusing is impossible, and it goes through settleStreamedCall instead.
//
// The write runs on a context stripped of its cancellation, like every other
// fact about a call that already happened (cascade.go's own rule for the mark a
// streamed answer writes): a client that hung up mid-call must not be the
// reason the workspace forgets what it spent.
func (s *Server) settleCall(ctx context.Context, cl *call, use *callUsage) *meterFail {
	cm := cl.meter
	if cm == nil || !cm.reserved {
		return nil
	}
	room, err := cm.meter.Settle(context.WithoutCancel(ctx), cm.hold, cm.settleAmount(use))
	cm.reserved = false
	if err != nil {
		return cm.settleFailed()
	}
	cm.room, cm.hasRoom = room, true
	return nil
}

// settleStreamedCall settles a call whose answer has already left the service,
// and it is the one settle that tries again: nothing is left to refuse the call
// on, so a settle that gives up is a cost the workspace's month never hears
// about — the reservation is left standing until budget.WorkspaceReservationTTL
// ages it out, and it is released then with the call charged nowhere.
//
// What makes that reachable is the meter's own shape rather than a fault: every
// member's reservations and settles are conditional writes of ONE month node, so
// a member whose parallel paid streams drive many settles at once can keep the
// node changing under all of the meter's fifteen attempts, and each stream whose
// settle loses is served and charged nothing. A member's own operator is exactly
// the party the workspace's cap exists to hold (SECURITY.md's residual risk 1),
// and a cap that a member can walk past by making its own calls collide is a cap
// that binds only the room in flight. The attempts below are the second bound:
// they are made on a context that outlives the request (a caller that hung up is
// not a reason to forget what the call cost) and with a longer, jittered wait
// than the meter's own, and the call's amount is computed once so every attempt
// offers the meter the same figure.
func (s *Server) settleStreamedCall(ctx context.Context, cl *call, use *callUsage) *meterFail {
	cm := cl.meter
	if cm == nil || !cm.reserved {
		return nil
	}
	amount := cm.settleAmount(use)
	ctx = context.WithoutCancel(ctx)
	var room budget.WorkspaceRoom
	var err error
	for attempt := 1; attempt <= streamSettleAttempts; attempt++ {
		room, err = cm.meter.Settle(ctx, cm.hold, amount)
		if err == nil {
			break
		}
		if attempt < streamSettleAttempts && streamSettleWait(ctx, streamSettleJitter(attempt)) != nil {
			// The context ended under the wait, which only a shutdown does. The
			// settle's own error is the one reported: it is what the meter said,
			// and the wait failing says nothing about why the write did.
			break
		}
	}
	cm.reserved = false
	if err != nil {
		return cm.settleFailed()
	}
	cm.room, cm.hasRoom = room, true
	return nil
}

// streamSettleJitter is the wait before the attempt after the n-th failed run:
// the base grows with n and is spread out, the shape the meter gives a lost
// write, so two settles that collided on the month's node do not collide again.
func streamSettleJitter(n int) time.Duration {
	base := min(streamSettleBackoff*time.Duration(n), streamSettleBackoffMax)
	return base/2 + time.Duration(mrand.Int64N(int64(base)+1))
}

// streamSettleWait waits d, or gives up when the context is done. The context
// here is one stripped of its cancellation, so the only way it ends is a
// shutdown: a service going down does not keep retrying a write nobody will
// serve.
func streamSettleWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// settleAmount is what the call settles for: zero for an answer the provider
// billed nothing for, the usage's own cost when the usage is readable, and the
// reservation — the call's whole worst case — when nothing could be read.
func (cm *callMeter) settleAmount(use *callUsage) pricing.Micros {
	switch {
	case use != nil && use.uncharged:
		// The provider billed nothing for this answer, so the workspace is
		// charged nothing for it: what is released is the room the reservation
		// took, and the figure the member's own gateway settles for the same
		// answer is a zero too.
		return 0
	case use != nil && use.complete:
		return cm.row.Rates.Cost(use.usage(cm.row.Rates, cm.ttl))
	}
	return cm.hold.Amount
}

// settleFailed is the refusal a settle that could not be written is. The
// reservation is left where it is (the meter does not release it on a failed
// settle) and is bounded by budget.WorkspaceReservationTTL: a hold older than
// that is a call that cannot still be in flight, so it is released by the next
// reservation's own read and the room comes back. What is NOT done is settling
// it as zero, which would be the workspace forgetting a cost it could not
// record.
func (cm *callMeter) settleFailed() *meterFail {
	return &meterFail{
		message:    "the workspace could not record what this call cost, so the call was not served",
		marker:     gateway.WorkspaceRefusalSettle,
		credential: cm.credential, tier: cm.tier,
	}
}

// unchargedUsage is what settleCall is handed for an attempt the provider did
// not bill for: the 401 and 429 an attempt is retried after (cascade.go's
// forward), whose bodies are closed unread. Their status is the whole of what
// the service knows about the cost, and it says zero — the same figure the
// member's own gateway settles those answers at (gateway.forward's
// zeroOutcome) and the reason the two ledgers agree on a retry.
func unchargedUsage() *callUsage { return &callUsage{uncharged: true} }

// answerHeadroom is the advisory figure an answer carries
// (gateway.WorkspaceHeadroomHeader, SetWorkspaceHeadroom): what the workspace's
// ceiling still leaves this month, and whether the read produced one.
//
// A metered call answers with the room its own reservation and settle left —
// the settle's when it has settled, the reservation's before that, which is a
// LOWER bound on the room (the worst case is still held) and the honest number
// to deliver on an answer that is already streaming. A call in a metered scope
// the meter does not bracket (a token count, a subscription token) reads the
// room fresh instead: it says nothing about what this call cost, and what the
// member needs is the figure that governs its next call.
//
// A read that fails delivers no headroom — the service's own log line and
// nothing else — and never refuses the answer: the figure is fail-safe (a
// member that has none stops nothing of its own) and the enforcement is the
// meter's, which is where the cap really binds.
func (s *Server) answerHeadroom(ctx context.Context, cl *call) (budget.WorkspaceRoom, bool) {
	if cl.meter != nil && cl.meter.hasRoom {
		return cl.meter.room, true
	}
	m := meterOf(ctx, s)
	if m == nil {
		return budget.WorkspaceRoom{}, false
	}
	room, err := m.Room(ctx)
	if err != nil {
		s.log.Warn("credsvc: the workspace's room could not be read for the answer", "error", err)
		return budget.WorkspaceRoom{}, false
	}
	if cl.meter != nil {
		cl.meter.room, cl.meter.hasRoom = room, true
	}
	return room, true
}

// refuseMeter answers a refusal the workspace's meter made, and records it. The
// headroom and the marker are written on the response header set BEFORE the
// refusal is written, which is what carries them out: WriteWireError preserves
// the headers already on the set and writes its own on top of them.
//
// One audit line and no more: the refusal goes through refuseCall like every
// other refusal on this path, so the act is recorded once, with the wire type
// the run reads as its status. (The identity is verified by the time any of
// these can be reached, which is what the rule is about.)
func (s *Server) refuseMeter(ctx context.Context, w http.ResponseWriter, id Identity, purpose string, f *meterFail) {
	if f == nil {
		f = &meterFail{message: "the workspace refused this call"}
	}
	if f.hasRoom {
		SetWorkspaceHeadroom(w.Header(), f.room)
	}
	SetWorkspaceRefusal(w.Header(), f.marker)
	s.refuseCall(ctx, w, id, purpose, f.credential, f.tier, StatusUnavailable, ErrorStateUnavailable, f.message)
}

// callID is one call's identifier in the workspace's month: the run it belongs
// to, and a random suffix that keeps two calls of one run apart.
//
// The run alone would not do. The meter refuses a second reservation under a
// call id it already holds (budget.ErrWorkspaceCallReserved), which is what
// makes a duplicated settle or a retried reserve visible rather than silently
// taken twice — and a run's second call is neither of those, it is a second
// call, so it needs an id of its own. The suffix is random and not a counter:
// the service holds no counter, and two instances serving the same run must not
// build the same id.
func callID(run string) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return run + "-" + hex.EncodeToString(b[:]), nil
}

// meterBody is an upstream body whose head was read by the meter and put back
// in front of the rest: the bytes the reader took, joined to what is left, so
// the answer still reaches the client whole. Its Closer is the upstream's own
// body, which is what the caller closes.
type meterBody struct {
	io.Reader
	io.Closer
}

// readMeteredBody reads a whole (non-streamed) answer for the usage it reports,
// and puts the bytes back where the caller will pump them from. It answers nil
// when the usage is not readable — a body past the meter's bound, or a read
// that failed — which settles the call at its reservation.
//
// The bytes are held rather than teed because the settle happens before they
// are written: a call whose cost cannot be recorded is refused, and a refusal
// after the answer had begun could not withhold anything.
func readMeteredBody(ans *answer) *callUsage {
	held, err := io.ReadAll(io.LimitReader(ans.resp.Body, meterMaxBody+1))
	if err == nil && len(held) <= meterMaxBody {
		use := newCallUsage(false)
		use.feed(held)
		use.finish()
		ans.resp.Body = io.NopCloser(bytes.NewReader(held))
		return use
	}
	ans.resp.Body = meterBody{Reader: io.MultiReader(bytes.NewReader(held), ans.resp.Body), Closer: ans.resp.Body}
	return nil
}

// meterRequest is what the meter knows about a request before it prices it: the
// gateway's own pricing.Request, built from the body the service holds.
//
// It is built conservatively wherever the two could disagree, because the
// figure is a bound and an under-bound is room a call can spend past the cap:
//
//   - max_tokens is the body's own when it names a positive one (the gateway
//     refuses a Messages body that names none, so this is the ordinary case),
//     the model's own ceiling when it does not, and the pricing package's
//     default when even that is unknown.
//   - an image or a document block is counted wherever it appears in the body,
//     not only where the gateway's own reader looks for one: a superset of a
//     request's images is a bound above the request's cost, which is the
//     direction this must be wrong in.
//   - the cache TTL is the longest any cache_control in the body can cause, by
//     the gateway's own rule (its cacheTTL/ttlOf).
func meterRequest(body []byte, row pricing.Model) pricing.Request {
	q := pricing.Request{BodyBytes: int64(len(body))}
	var head struct {
		MaxTokens int64 `json:"max_tokens"`
	}
	if json.Unmarshal(body, &head) == nil && head.MaxTokens > 0 {
		q.MaxTokens = head.MaxTokens
	} else if row.MaxOutputTokens > 0 {
		q.MaxTokens = row.MaxOutputTokens
	} else {
		q.MaxTokens = pricing.DefaultMaxOutputTokens
	}
	q.ImageCount, q.HasPDF = meterContent(body)
	q.CacheTTL = meterCacheTTL(body)
	return q
}

// meterContent counts the base64 images and finds a base64 document anywhere in
// the body, and nothing else: a block whose size is not in the body is refused
// by the gateway's own check before this service sees it, and a shape nothing
// in the tree prices is not one this needs an answer about.
func meterContent(body []byte) (images int64, pdf bool) {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return 0, false
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if src, ok := x["source"].(map[string]any); ok {
				if st, _ := src["type"].(string); st == "base64" {
					switch t, _ := x["type"].(string); t {
					case "image":
						images++
					case "document":
						pdf = true
					}
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
	return images, pdf
}

// meterCacheTTL is the longest cache write a body can cause, by the gateway's
// own rule: "" when the body holds no cache_control anywhere, "5m" for one
// without a ttl or with "5m", and "1h" for anything else (its cacheTTL and
// ttlOf, respelled for the reason carriesAny is: the package does not export
// them).
func meterCacheTTL(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	best := ""
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if k == "cache_control" && c != nil {
					if t := meterTTLOf(c); meterTTLRank[t] > meterTTLRank[best] {
						best = t
					}
				}
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
	return best
}

// meterTTLRank orders the three answers meterCacheTTL can give: no cache
// control, a five-minute write, an hour-long one.
var meterTTLRank = map[string]int{"": 0, "5m": 1, "1h": 2}

// meterTTLOf is one cache_control's TTL, by the gateway's own rule exactly: an
// object without a ttl is the API's default of five minutes, and anything else
// — including a cache_control that is not an object at all — is reserved like
// the longer write, because a shape nothing can read is not a reason to
// under-bound a call.
func meterTTLOf(cc any) string {
	m, ok := cc.(map[string]any)
	if !ok {
		return "1h"
	}
	switch t := m["ttl"]; t {
	case nil, "5m":
		return "5m"
	}
	return "1h"
}

// meterDollars is an amount in dollars, for a refusal's sentence. It is
// internal/gateway's own dollars, respelled for the reason carriesAny is: the
// package does not export it, and a figure a member reads must not be
// micro-dollars.
func meterDollars(m pricing.Micros) string {
	usd := m.USD()
	if m > 0 && usd < 0.01 {
		return strings.TrimRight(fmt.Sprintf("$%.6f", usd), "0")
	}
	return fmt.Sprintf("$%.2f", usd)
}

// The answer's usage, read for the settle.
//
// It is a smaller mirror of the gateway's own reader (internal/gateway/sse.go's
// usageTee over wireUsage), and it reads the same two shapes the same way,
// because the two must agree on what a complete answer is: a call that settles
// at its usage here and at its reservation there would be a call the workspace
// charges differently from the run's own ledger.
//
// Only the Claude route's rules are here — the provider route's conservative
// merge is the gateway's, and a provider response the workspace pays for
// settles on the same input/output counts from the same table. What is kept is
// exactly what decides "complete", which is all the settle needs:
//
//   - a stream: message_start's usage with an input count (started), then a
//     message_delta reporting output_tokens (deltas), then message_stop. A
//     message_delta that does not parse, a negative count, or a stop without an
//     output count makes the call incomplete, and an incomplete call settles at
//     its reservation.
//   - a whole body: one JSON object whose usage names an input count and an
//     output count, and nothing after it.
//
// One rule comes from the ANSWER's status rather than from its body, and it is
// the reader's unless the body could not be read at all: a response that is not
// a 2xx costs nothing, whatever it holds (uncharged). A provider does not bill
// for a call it refused — an overloaded 529, a 500, a 400 for a request it
// would not take — and the member's own gateway settles every non-2xx at zero
// (gateway.forward's zeroOutcome). Reading such an answer with the 2xx rules
// charges the workspace the call's whole worst case for spend that never
// happened: an agent that retries an overload the way Claude Code does would
// use the workspace's month up without a call to show for it. A body that could
// not be read keeps the reservation, because nothing about that answer is
// known — not even that it arrived.
type callUsage struct {
	stream   bool
	p        meterSSE
	buf      []byte
	overflow bool

	started  bool
	deltas   bool
	complete bool
	broken   bool

	// uncharged is the status's verdict over the usage's own: the answer was
	// not a 2xx, so the provider billed nothing for it and it is settled at
	// zero rather than at its reservation. An error body reports no usage, so
	// without it the reservation — the call's whole worst case — would stand
	// for a call nobody paid for. It is set only where a body was read at all:
	// an answer whose body could not be read hands a nil usage on and keeps the
	// reservation, since nothing about it is known.
	uncharged bool

	acc meterAcc
}

// meterMaxLine and meterMaxBody bound what the reader holds, and they are the
// gateway's own bounds (its maxSSELine and maxJSONBody): a longer line is
// dropped with its event and any event whose line was dropped is not read, and
// a longer body settles the call at its reservation — which is the same call
// the gateway settles at its own reservation for the same reason.
const (
	meterMaxLine = 1 << 20
	meterMaxBody = 16 << 20
)

// newCallUsage reads an answer: an event stream, or a whole body.
func newCallUsage(stream bool) *callUsage {
	u := &callUsage{stream: stream}
	u.p.onEvent = u.onEvent
	return u
}

// feed hands the reader the bytes of the answer, as the gateway's own tee is
// handed them: a stream's events as they arrive, a whole body in the pieces it
// comes in.
func (u *callUsage) feed(b []byte) {
	if u.stream {
		u.p.feed(b)
		return
	}
	if u.overflow {
		return
	}
	if len(u.buf)+len(b) > meterMaxBody {
		u.overflow, u.buf = true, nil
		return
	}
	u.buf = append(u.buf, b...)
}

// finish reads a whole body. A stream was read as it arrived.
func (u *callUsage) finish() {
	if !u.stream {
		u.parseBody()
	}
}

// usage is the reported usage, priced at the model's rates. Cache writes the
// response did not split by TTL are priced at the highest write the request
// allowed, which is the gateway's own rule and the reason the ttl is kept.
func (u *callUsage) usage(r pricing.Rates, cacheTTL string) pricing.Usage {
	return u.acc.usage(r, cacheTTL)
}

// parseBody reads a whole body the way the gateway does: the object's usage,
// only if nothing follows the object and only when it holds both an input and
// an output count, with no negative count anywhere.
func (u *callUsage) parseBody() {
	if u.overflow {
		return
	}
	dec := json.NewDecoder(bytes.NewReader(u.buf))
	var body struct {
		Type  string          `json:"type"`
		Usage *meterUsageWire `json:"usage"`
	}
	if dec.Decode(&body) != nil {
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return
	}
	if body.Type == "error" || body.Usage == nil {
		return
	}
	u.acc.merge(body.Usage)
	if !u.acc.out.set || u.acc.negative() || !u.acc.hasInput() || u.acc.unpriced() {
		return
	}
	u.started, u.complete = true, true
}

// onEvent reads one SSE event, by the gateway's own rules for the Claude route.
func (u *callUsage) onEvent(name string, data []byte) {
	typ := name
	if typ == "" {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &head)
		typ = head.Type
	}
	switch typ {
	case "message_start":
		var ev struct {
			Message struct {
				Usage *meterUsageWire `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Message.Usage == nil {
			// The documented "didn't say" case: message_delta is gated on
			// started, which this never sets, so the call settles at its
			// reservation without a broken flag — the same answer the
			// gateway's own Claude-route reader gives.
			return
		}
		u.acc.merge(ev.Message.Usage)
		if u.acc.negative() {
			// A count below zero is not usage: it must not settle a call at
			// less than the call's own reservation.
			u.broken = true
		}
		u.started = u.acc.hasInput() && !u.acc.negative()
	case "message_delta":
		if !u.started {
			return
		}
		var ev struct {
			Usage *meterUsageWire `json:"usage"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Usage == nil {
			u.broken = true
			return
		}
		u.acc.merge(ev.Usage)
		if u.acc.negative() {
			u.broken = true
		}
		if ev.Usage.OutputTokens != nil {
			u.deltas = true
		}
	case "message_stop":
		switch {
		case u.started && u.deltas && !u.broken && !u.acc.unpriced():
			u.complete = true
		case u.started:
			u.broken = true
		}
	}
}

// meterSSE splits server-sent events, byte for byte as the gateway's own parser
// does: an event is its data lines, an event with no data is nothing, a line
// longer than the bound is dropped, and an event any of whose lines was dropped
// is not read at all.
type meterSSE struct {
	onEvent func(name string, data []byte)

	line     []byte
	skipping bool
	event    string
	data     []byte
	hasData  bool
	tainted  bool
}

func (p *meterSSE) feed(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		chunk := b
		if i >= 0 {
			chunk, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		if !p.skipping {
			if len(p.line)+len(chunk) > meterMaxLine {
				p.skipping, p.line = true, p.line[:0]
			} else {
				p.line = append(p.line, chunk...)
			}
		}
		if i < 0 {
			return
		}
		if p.skipping {
			p.tainted, p.skipping = true, false
		} else {
			p.processLine(bytes.TrimSuffix(p.line, []byte{'\r'}))
		}
		p.line = p.line[:0]
	}
}

func (p *meterSSE) processLine(l []byte) {
	if len(l) == 0 {
		if p.hasData && !p.tainted {
			p.onEvent(p.event, p.data)
		}
		p.event, p.data, p.hasData, p.tainted = "", p.data[:0], false, false
		return
	}
	if l[0] == ':' {
		return
	}
	field, value := l, []byte(nil)
	if i := bytes.IndexByte(l, ':'); i >= 0 {
		field, value = l[:i], bytes.TrimPrefix(l[i+1:], []byte{' '})
	}
	switch string(field) {
	case "event":
		p.event = string(value)
	case "data":
		if p.hasData {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, value...)
		p.hasData = true
		if len(p.data) > meterMaxLine {
			p.tainted, p.data = true, p.data[:0]
		}
	}
}

// meterUsageWire is the usage object as the API reports it. The fields are the
// gateway's own (its wireUsage) and they are read the same way: a pointer per
// count, so a field the answer did not name is not a zero.
type meterUsageWire struct {
	InputTokens              *int64 `json:"input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreation            *struct {
		W5 *int64 `json:"ephemeral_5m_input_tokens"`
		W1 *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServerToolUse *struct {
		WebSearchRequests *int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
	// The three pricing dimensions a response can report that a request's
	// own worst case does not bound: the request chooses a speed and an
	// inference geography, and the provider chooses the tier it serves
	// from. They are read raw, as the gateway reads them, so a value that
	// is not even a string is still seen rather than dropped.
	Speed        json.RawMessage `json:"speed"`
	ServiceTier  json.RawMessage `json:"service_tier"`
	InferenceGeo json.RawMessage `json:"inference_geo"`
}

// meterCount is one reported count and whether the answer reported it at all.
type meterCount struct {
	v   int64
	set bool
}

func (f *meterCount) take(p *int64) {
	if p != nil {
		f.v, f.set = *p, true
	}
}

// meterAcc accumulates usage: a later event's value for a field wins, which is
// what a cumulative message_delta means (the gateway's own usageAcc, less the
// provider route's conservative merge and the fields nothing here prices).
type meterAcc struct {
	in, cc, cr, out, w5, w1, web meterCount
	speed, tier, geo             json.RawMessage
}

func (a *meterAcc) merge(u *meterUsageWire) {
	a.in.take(u.InputTokens)
	a.cc.take(u.CacheCreationInputTokens)
	a.cr.take(u.CacheReadInputTokens)
	a.out.take(u.OutputTokens)
	if c := u.CacheCreation; c != nil {
		a.w5.take(c.W5)
		a.w1.take(c.W1)
	}
	if t := u.ServerToolUse; t != nil {
		a.web.take(t.WebSearchRequests)
	}
	// A later event's value for a dimension wins, as each count's own take
	// does: the answer's last word about the call is the word that stands.
	for _, x := range []struct {
		dst *json.RawMessage
		src json.RawMessage
	}{{&a.speed, u.Speed}, {&a.tier, u.ServiceTier}, {&a.geo, u.InferenceGeo}} {
		if len(x.src) > 0 {
			*x.dst = append(json.RawMessage(nil), x.src...)
		}
	}
}

// unpriced reports whether the answer named a pricing dimension the table has
// no price for: a speed other than the standard one, a service tier other than
// the standard one, or an inference geography outside the defaults — the
// gateway's own three checks (its unpricedDims).
//
// The call then settles at its reservation rather than on the usage it
// reported, which is where this meter and the gateway part company and the
// honest direction to part in: the gateway charges such a response at the
// table's maximum rates times a multiplier, and the meter cannot charge more
// than the room it reserved, so the reservation — the bound of what the
// workspace agreed to hold for the call — is the most it can settle at. What
// it must not do is settle at the call's own model's own rates, which is
// cheaper than the call: the request chooses a speed and a geography, so a
// member's run could otherwise spend past the workspace's cap by asking for a
// shape nothing prices.
func (a *meterAcc) unpriced() bool {
	return meterDimUnpriced(a.speed, []string{"standard"}) ||
		meterDimUnpriced(a.tier, []string{pricing.StandardTier}) ||
		meterDimUnpriced(a.geo, gateway.DefaultGeos)
}

// meterDimUnpriced reports whether one reported pricing dimension is one the
// table cannot price. Absent or null is "the answer did not name it"; a value
// that is not a string is reported rather than dropped, since the table has no
// price for it either way; and anything outside the allowed set is unpriced.
func meterDimUnpriced(raw json.RawMessage, allowed []string) bool {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return true
	}
	return !slices.Contains(allowed, s)
}

// hasInput reports whether the answer named an input count at all: the Claude
// route's own rule, where a call's input is always a real quantity and a usage
// with no input count is a response that said nothing (the gateway's own
// hasInput).
func (a *meterAcc) hasInput() bool { return a.in.set }

// negative reports whether any reported count is below zero.
func (a *meterAcc) negative() bool {
	for _, f := range []meterCount{a.in, a.cc, a.cr, a.out, a.w5, a.w1, a.web} {
		if f.set && f.v < 0 {
			return true
		}
	}
	return false
}

// usage is the reported usage, with the cache writes the response did not split
// by TTL priced at the highest write the request allowed (the gateway's own
// usage()).
func (a *meterAcc) usage(r pricing.Rates, cacheTTL string) pricing.Usage {
	pos := func(f meterCount) int64 { return max(f.v, 0) }
	u := pricing.Usage{Input: pos(a.in), CacheRead: pos(a.cr), Output: pos(a.out), WebSearches: pos(a.web)}
	rest := pos(a.cc)
	if a.w5.set || a.w1.set {
		u.CacheWrite5m, u.CacheWrite1h = pos(a.w5), pos(a.w1)
		rest -= u.CacheWrite5m + u.CacheWrite1h
	}
	if rest > 0 {
		w5, w1 := r.UnsplitCacheWrites(rest, cacheTTL)
		u.CacheWrite5m += w5
		u.CacheWrite1h += w1
	}
	return u
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
		s.refuseSelection(ctx, w, id, err, nil, cl.stage)
		return
	}
	ans, err := s.send(ctx, cl, e)
	if err != nil {
		s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			err.Error(), labelRead(readUpstream, err))
		return
	}
	// What the provider's answer carried is deliberately NOT recorded here: a
	// routed call attaches a key and answers with what the provider said, and it
	// writes no mark at all (TestARoutedCallLeavesTheRunsPlacementAlone). Check
	// 880's record belongs to the sources whose state this service holds — the
	// chain's — and a provider key's rate-limit headers are none of them, so the
	// routed path writes no observation either.
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

// rateLimitHeaderNames returns the NAMES — never the values — of the headers of
// one answer that say anything about a rate limit, sorted so that two answers
// carrying the same headers record the same thing.
//
// The rule is deliberately wider than the headers this service reads a reset
// from (usageLimitReset): Anthropic's unified rate-limit headers are named
// `anthropic-ratelimit-*`, an OpenRouter answer's are `x-ratelimit-*`, and a
// plain `Retry-After` is the HTTP one, so the question "which headers of this
// answer are about a rate limit" is answered by the name and not by a list this
// release would have to keep up to date. What the names are FOR is U1's
// question — whether a subscription token's answer reports a utilisation figure
// at all (D3178) — and answering it needs no value of any of them.
func rateLimitHeaderNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for k := range h {
		ck := http.CanonicalHeaderKey(k)
		lower := strings.ToLower(ck)
		if lower == "retry-after" || strings.Contains(lower, "ratelimit") || strings.Contains(lower, "rate-limit") {
			names = append(names, ck)
		}
	}
	slices.Sort(names)
	return names
}

// recordRateLimitHeaders writes the names — and only the names — of the
// rate-limit headers one answer carried into the credential's own observation
// node (creds/observed/<name>), where the operator's Check 880 reads them.
//
// A record that cannot be written is a line on the service's log and never a
// refusal: it is an observation about an answer the run has already been given,
// and the run is not the party that owes this service a record of it. The
// attempt to write is made whatever the client did with the answer — the
// answer's headers are a fact about the credential whether or not the caller
// stayed to read the body (the streamed usage limit's mark is written the same
// way).
//
// It is called only for the answers this service reads as about the CREDENTIAL —
// the ones it serves (a 2xx), a usage limit (429) and a 401 — because a record is
// a claim that this answer says something about this credential, and a relayed
// 529 or 500 says nothing about it (the published pool's own rule: "an answer
// about the request moves nothing"). The line a failed write leaves names the
// error's CLASS and never its text, as every other line of this service's log
// does: the text of a state-write failure can carry the node's own content, and
// this log is not a place a credential's value may reach.
//
// The observation goes to a node of its own and not into the credential's mark,
// so an answer this service serves writes no mark where it previously wrote one
// and the mark's wire shape is untouched (creds.Observation). The write is made
// for an answer that carried no rate-limit header at all, which is not nothing:
// the node then says that answer was recorded, which is how Check 880 tells a
// credential whose answers name nothing apart from one no release has recorded.
//
// A store that cannot record one — the state a scope answers is a
// creds.CascadeState, and recording is a capability a store has or has not
// (creds.ObservationStore) — leaves the answer unrecorded, and Check 880 reads
// that source as one it cannot answer for. That is the fail-safe direction and
// it is not a failure of the call: the record is the check's, and nothing about
// serving the run depends on it.
func (s *Server) recordRateLimitHeaders(ctx context.Context, sel *creds.Selector, name string, h http.Header) {
	if sel == nil || name == "" {
		return
	}
	store, ok := sel.Store.(creds.ObservationStore)
	if !ok {
		return
	}
	names := rateLimitHeaderNames(h)
	if _, err := store.UpdateObservation(context.WithoutCancel(ctx), name, func(creds.Observation, bool) (creds.Observation, error) {
		return creds.Observed(names), nil
	}); err != nil {
		s.log.Warn("credsvc: the rate-limit header names of an answer could not be recorded", "error_class", errorClass(err))
	}
}

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
