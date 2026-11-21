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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
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
// serves at one) is untouched by any of it: cl.meter is nil and this function
// is what it was.
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
	case e.Kind == KindProvider:
		// The provider path. It is chosen by KIND and not by tier: exactly one
		// entry of a document has this kind, and both facts the call needs —
		// where it goes and how the key is sent — were validated when that entry
		// was read (providerValue), so neither the destination nor the header
		// built here can be one the loader refused. The credential is the
		// entry's own value, and the helper refuses a subscription credential on
		// a route, as every route's does (D133).
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
	// The room is taken here, after the request is built and validated and
	// immediately before it goes out: a reservation for a call that was never
	// made would hold room nothing will settle, and the reservation must not
	// be held across a header build that can refuse the call for its own
	// reasons.
	if cl.meter != nil && paidCall(cl, e) && !cl.meter.take(ctx, s, cl, e) {
		return nil, errMeterRefused
	}
	resp, err := s.cfg.Client.Do(req)
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
	return &answer{resp: resp, credential: key}, nil
}

// The workspace's cap, taken where the money is spent (WS8, D1301, F10, F18,
// R15): the room for a call's worst case is reserved out of the workspace's
// month BEFORE the request goes out, the answer settles it on the usage the
// response reported, and a call whose cost cannot be recorded is refused rather
// than served. The transaction itself is internal/budget/workspace.go's and the
// meter over it is workspace_cap.go's; what is here is the bracket around one
// upstream call, which is the only place a call can be priced and the only
// place its usage is readable.
//
// Three rules of the shape are worth stating where the code is:
//
//   - the worst case is what is reserved, always. What a call will cost is not
//     known until its answer reports usage, so the amount held is the table's
//     own bound for the request (pricing.Model.WorstCase) — the same figure the
//     gateway reserves before its own calls, from the same table. A call that
//     settles for less returns the difference at the settle; a call that never
//     settles (a stream cut in half, a settle that could not be written) stays
//     charged at the bound, which is the direction a cap must fail in.
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
func paidCall(cl *call, e Entry) bool {
	return cl.path == MessagesPath && e.Tier != creds.TierA
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
// Every failure here is a refusal and never a pass, including the ones that
// read as internal trouble (a price table that cannot be read, a month that
// cannot be written): a call served with no reservation is a call that can pass
// the cap, and a cap that opens when its own database is unreadable is not a
// cap.
func (cm *callMeter) take(ctx context.Context, s *Server, cl *call, e Entry) bool {
	cm.credential, cm.tier = e.Name, wireTier(e.Tier)
	if cm.member == "" || cm.run == "" {
		// Unreachable through ServeHTTP, which verifies both before it serves
		// anything. A refusal rather than a reservation under an empty name,
		// which the meter itself would refuse for the same reason.
		cl.fail = cm.fail("the call resolved to no workspace member, so the workspace did not take the room for it", "", nil)
		return false
	}
	model, ok := callModel(cl.body)
	if !ok {
		cl.fail = cm.fail("the call names no model this service can price, so the workspace did not take the room for it", "", nil)
		return false
	}
	cm.model = model
	table, err := cm.priceTable(ctx, s)
	if err != nil {
		cl.fail = cm.fail("the workspace's price table could not be read, so the workspace did not pay for this call", "", nil)
		return false
	}
	row, ok := table.Lookup(model)
	if !ok {
		// A model the table cannot price is refused rather than priced at a
		// guess: an unpriced call is a call whose cost no cap can see, and the
		// catalog's own sentence (when it recorded one) names the model and
		// both ways out. This is the check the gateway's own path makes for a
		// provider route, made here for every tier — the workspace's money is
		// the workspace's money whatever the credential.
		message := "the workspace's price table has no price for " + model + ", so the workspace did not pay for this call"
		if why := table.Refusal(model); why != "" {
			message = "the workspace did not pay for this call: " + why
		}
		cl.fail = cm.fail(message, "", nil)
		return false
	}
	cm.row, cm.ttl = row, meterCacheTTL(cl.body)
	call, err := callID(cm.run)
	if err != nil {
		// The random half is what makes the id unique; without it two calls of
		// one run could not be told apart, and the meter would refuse the
		// second as a duplicate of the first.
		cl.fail = cm.fail("the workspace could not identify this call, so it did not take the room for it", "", nil)
		return false
	}
	amount := row.WorstCase(meterRequest(cl.body, row))
	hold, room, err := cm.meter.Reserve(ctx, cm.member, call, amount)
	switch {
	case errors.Is(err, budget.ErrWorkspaceCeilingReached):
		// The ceiling is the one refusal with a number to hand the member: the
		// room the workspace has left, which is what the member's own gateway
		// compares its next calls against (gateway.WorkspaceStop).
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

// priceTable is the table the call is priced from: the embedded one with the
// project's own model_prices over it, which is the table every other reader of
// a price in this tree builds (requestPriceTable).
//
// It is read through the scope's policy, which for a workspace's call is the
// workspace's own published policy — the one whose prices the workspace's
// members are metered against. The read is memoized per call: the reserve needs
// the row and the settle its rates, and one answer must not be priced from two
// tables.
func (cm *callMeter) priceTable(ctx context.Context, s *Server) (*pricing.Table, error) {
	if cm.priced {
		return cm.prices, cm.priceErr
	}
	cm.priced = true
	sc := scopeOfCall(ctx, s)
	if sc == nil {
		cm.priceErr = errors.New("credsvc: the call's scope could not be resolved")
		return nil, cm.priceErr
	}
	shared, found, err := sc.Policy().Shared(ctx)
	if err != nil {
		cm.priceErr = err
		return nil, err
	}
	if !found {
		cm.priceErr = errors.New("credsvc: the policy that prices this call is not published")
		return nil, cm.priceErr
	}
	cm.prices, cm.priceErr = requestPriceTable(shared, s.now())
	return cm.prices, cm.priceErr
}

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
// The write runs on a context stripped of its cancellation, like every other
// fact about a call that already happened (cascade.go's own rule for the mark a
// streamed answer writes): a client that hung up mid-call must not be the
// reason the workspace forgets what it spent.
func (s *Server) settleCall(ctx context.Context, cl *call, use *callUsage) *meterFail {
	cm := cl.meter
	if cm == nil || !cm.reserved {
		return nil
	}
	actual := cm.hold.Amount
	if use != nil && use.complete {
		actual = cm.row.Rates.Cost(use.usage(cm.row.Rates, cm.ttl))
	}
	room, err := cm.meter.Settle(context.WithoutCancel(ctx), cm.hold, actual)
	cm.reserved = false
	if err != nil {
		// The reservation is left where it is (the meter does not release it on
		// a failed settle) and is bounded by budget.WorkspaceReservationTTL: a
		// hold older than that is a call that cannot still be in flight, so it
		// is released by the next reservation's own read and the room comes
		// back. What is NOT done is settling it as zero, which would be the
		// workspace forgetting a cost it could not record.
		return &meterFail{
			message:    "the workspace could not record what this call cost, so the call was not served",
			marker:     gateway.WorkspaceRefusalSettle,
			credential: cm.credential, tier: cm.tier,
		}
	}
	cm.room, cm.hasRoom = room, true
	return nil
}

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

// meterTTLOf is one cache_control's TTL: a value that is not an object, or one
// that is an object without a ttl, is the API's own default of five minutes,
// and any other value is reserved like the longer write.
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
type callUsage struct {
	stream   bool
	p        meterSSE
	buf      []byte
	overflow bool

	started  bool
	deltas   bool
	complete bool
	broken   bool

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
	if !u.acc.out.set || u.acc.negative() || !u.acc.hasInput() {
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
		case u.started && u.deltas && !u.broken:
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
// The base is the entry's own when the entry is a provider key — the URL the
// document holds, which is the only place a provider's address exists (D1418):
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
	if e.Kind == KindProvider {
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

// providerRoute is the gateway's own Route for a provider key's entry (AK6b):
// the name it goes by in a log, the base URL the document holds, the auth shape
// it names, and the entry's value as the credential, read per call.
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
// bundle holds one provider key and the call's model was already pinned by the
// project's own allow-list) and it adds no fee (a fee is a run's budget, which
// lives in the runner's gateway and not here). Neither is read by RouteHeaders.
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
