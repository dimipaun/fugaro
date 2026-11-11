// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is POST /oauth/register: Dynamic Client Registration (RFC 7591),
// the door a standard MCP client walks through before any person has signed in.
//
// **It is open, and that is the owner's answer** (D928, spec.md §3). A client
// registers with no credential at all, because a client that had to be
// provisioned first would not be a standard client. What keeps openness from
// being a hole is that a registration grants nothing: the record it writes
// carries no token, no family and no limits, so a registered client reaches no
// tool until a person signs in and consents, and an unattended caller cannot be
// made out of one at all: the client credentials grant is refused outright
// (D929 amended, M-f), so there is no path from a registration to a token with
// nobody behind it.
//
// **The secret is returned once and only its hash is kept** (spec.md §3, §6).
// The record is the same shape an operator's pre-registration writes, which is
// why the field exists at all: D929's unattended caller is a client with a
// secret, and one record type serves both doors. What the response advertises is
// `token_endpoint_auth_method: none` and not `client_secret_basic`, because that
// is what is true of the two grants this server answers: neither requires the
// secret, and the document would otherwise tell a native client to keep a
// long-lived value on a device for nothing.
//
// **A redirect URI is checked here and again at authorize, against the same
// rule** (D928), and this is the earlier of the two doors on purpose: a client
// that could never receive a code here is refused with a message naming the
// rule, rather than registering and failing at a person's sign-in.
//
// **And the endpoint is bounded twice, which is what spec.md §8 asks of an open
// registration endpoint** (M12, reconciliation.md §4a): a source is limited in
// how many registrations it may make (registerLimiter), and every record the
// endpoint writes has a lifetime the store reaches by prefix whether or not
// anybody ever asks for it again (RegistrationTTL, SweepExpired below). The two
// are one decision and not two features: what makes openness affordable is that
// neither a single source nor a single record can be used to fill the store.

const (
	// maxClientNameBytes bounds the name a client gives itself. The name is a
	// stranger's text and it is shown on a page a person consents on, so it is
	// held to printable ASCII within a header-sized bound rather than stored
	// as it arrived.
	maxClientNameBytes = 200
	// maxRedirectURIs bounds how many URIs one registration may name. A
	// registration is open to everyone, and a list that is unbounded is a
	// record whose size a stranger chooses.
	maxRedirectURIs = 10
)

// ErrRedirectURI is a redirect URI this installation will not send a code to.
// Every refusal of one is this error, whether the URI was unparseable, carried
// a scheme this server does not redirect to, or was simply not one the project
// allows — because the rule is one rule (checkRedirectURI) and the caller that
// reads it is a person reading a message, not a branch.
var ErrRedirectURI = errors.New("mcpauth: the redirect URI is not one this installation sends a code to")

// registrationRequest is RFC 7591's client metadata, as much of it as this
// server reads. The rest of the specification's fields are deliberately not
// modelled: a field this server would ignore is a field a client could set and
// believe it had been honoured, and RFC 7591 says a server MAY ignore what it
// does not implement. What it does not ignore is the redirect URI list, which
// is the whole of what a registration is for here.
type registrationRequest struct {
	// RedirectURIs are the URIs this client will receive a code at, each
	// checked against D928's rule before the record is written.
	RedirectURIs []string `json:"redirect_uris"`
	// ClientName is what the client calls itself, shown on the consent page.
	ClientName string `json:"client_name"`
}

// registrationResponse is RFC 7591 §3.2.1's answer. The secret is in it and
// nowhere else: the record holds the hash, and there is no path that returns
// the value a second time.
type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

// The per-source bound spec.md §8 asks for (M12). Its figures are the shape and
// the owner's to set, like every other lifetime and ceiling in this package.
const (
	// registerWindow is the period one source's registrations are counted
	// over, and registerPerWindow how many of them are admitted in it. Ten a
	// minute is far more than a client needs — a client registers once — and
	// small enough that a source writing registrations for everybody else
	// fills the store at a rate the sweep below clears.
	registerWindow    = time.Minute
	registerPerWindow = 10
	// maxRegisterSources bounds the limiter's own map. A limiter that grew
	// one entry per source would be a record in memory that a stranger
	// chooses the size of, which is the same class of unbounded write the
	// limit exists to refuse — and a source cannot choose how many entries it
	// holds: the value it may send is the one the platform's front end
	// overwrites or appends to, and the key is that address — or, for IPv6,
	// the one /64 an allocation gives it, however many addresses in it the
	// caller rotates through (registerSource, sourceKey).
	maxRegisterSources = 4096
)

// registerLimiter is one counting window per source, in memory.
//
// **In memory and not in the store, and the difference is a decision.** A
// bound that survives a restart would have to be a record per source in the
// store whose own address is the one shape D922's hashes-only rule cannot
// cover: a source is an IP address, it is personal data the moment it is
// written down, and the bucket's whole design is that a reader of it holds
// nothing about a caller. What is lost by keeping it in memory is a restart's
// worth of counting, which an attacker can only reach by making the door
// restart — and a door that is restarting is not registering clients.
//
// The window is per source and not global on purpose: a global bound would let
// one source refuse everybody else's registration, which is a denial of
// service with this door's own help.
type registerLimiter struct {
	mu   sync.Mutex
	seen map[string]registerWindowState
}

type registerWindowState struct {
	start time.Time
	count int
}

func newRegisterLimiter() *registerLimiter {
	return &registerLimiter{seen: map[string]registerWindowState{}}
}

// allow reports whether one more registration from source is admitted at now,
// and counts it when it is. A source the map has never seen starts its window
// here; a window that has run out starts again.
//
// A map that is full of live windows refuses the new source rather than
// evicting one of them: the entries are held for at most a minute each, the
// refusal is temporary, and dropping a live entry to make room would be
// handing a caller the ability to reset somebody else's window — which is
// exactly the denial of service the per-source shape is chosen to avoid.
func (l *registerLimiter) allow(source string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w, ok := l.seen[source]; ok && now.Sub(w.start) < registerWindow {
		if w.count >= registerPerWindow {
			return false
		}
		w.count++
		l.seen[source] = w
		return true
	}
	if len(l.seen) >= maxRegisterSources {
		l.evict(now)
		if len(l.seen) >= maxRegisterSources {
			return false
		}
	}
	l.seen[source] = registerWindowState{start: now, count: 1}
	return true
}

// evict drops the windows that have run out, which is the only eviction that
// can never hand a caller a reset: a source with a live window keeps it.
func (l *registerLimiter) evict(now time.Time) {
	for source, w := range l.seen {
		if now.Sub(w.start) >= registerWindow {
			delete(l.seen, source)
		}
	}
}

// registerSource is which source one registration is counted against.
//
// **It is the rightmost entry of X-Forwarded-For, or the peer address when
// there is no header** — and the choice between the two ends of that header is
// the whole of the selection. A caller can send an X-Forwarded-For of its own,
// and the platform's front end appends the address it saw to whatever arrived,
// so the *leftmost* entry is a value the caller wrote and the rightmost is the
// one Cloud Run's load balancer put there. Counting by the leftmost entry
// would be counting by a value an attacker chooses a fresh one of per request,
// which is a bound that admits everything.
//
// A peer address with no port — a unix socket, a test's own request — is used
// as it stands rather than refused: an unidentifiable source counting as one
// source is the fail-safe direction, and the alternative is an endpoint that
// refuses to register a client because its address did not parse.
//
// What the entry is then *counted as* is sourceKey's question and not this
// one's: the value read here is not what a window is keyed by, because rotating
// through the addresses of one allocation is free for the caller (sourceKey).
func registerSource(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		entries := strings.Split(xff, ",")
		for i := len(entries) - 1; i >= 0; i-- {
			if entry := strings.TrimSpace(entries[i]); entry != "" {
				return sourceKey(entry)
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return sourceKey(host)
	}
	return sourceKey(r.RemoteAddr)
}

// ipv6SourceBits is how much of an IPv6 address is one source (sourceKey): one
// allocation, which is the /64 every registry hands one subscriber.
const ipv6SourceBits = 64

// sourceKey is the address a source is counted as.
//
// **An IPv6 source is its /64 and not its address, and that is the whole of
// the function.** Rotating through the addresses of one allocation is free —
// any host in a /64 may take any address in it — so an address-keyed bound is
// no bound against a caller holding one /64: 4,096 of them fill the limiter's
// own map, and a map that is full refuses the source that is not in it, which
// is the global denial of service the per-source shape was chosen to avoid
// (registerLimiter.allow). Counting the prefix makes that rotation one source
// with one window, at the price of counting the addresses of one allocation
// together — which is what an allocation is: one subscriber.
//
// **A /64 and not a /56**, because refusing a caller that did nothing is the
// failure this bound is shaped around: the neighbours inside one access
// network's shorter prefix are strangers to each other, and a bound that counts
// them together refuses their registrations over a stranger's. What the /64
// costs instead is map entries — a caller holding a larger allocation holds
// several /64s, each an entry — and that cost falls on the traffic of whoever
// is doing the rotating.
//
// **An IPv4 address is counted as itself, and so is a v4-mapped one.** The
// second half is not a detail: `::ffff:198.51.100.7` masked to a /64 is
// `::/64`, which every IPv4 caller shares, so a prefix applied without
// unmapping first would put every IPv4 client in the world in one window.
// Anything that does not parse as an address — an empty header, a peer name —
// is counted as it stands, which is registerSource's fail-safe direction too.
func sourceKey(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	if ip.Is4() || ip.Is4In6() {
		return ip.Unmap().String()
	}
	prefix, err := ip.Prefix(ipv6SourceBits)
	if err != nil {
		return addr
	}
	return prefix.Masked().String()
}

// tooManyRegistrations is the refusal of a source past its window.
//
// The status is 429 and the body is RFC 6749 §5.2's shape with that
// specification's own code for it, written here rather than through
// oauthError: the vendor's vocabulary is a closed set of constructors with no
// `temporarily_unavailable` in it, and `server_error`, which it does have,
// tells a client that retrying is pointless when retrying is exactly what the
// header says to do.
//
// The description is a fixed sentence and never the source it refused: a
// source is an address, and a public endpoint's body is not a place one is
// written down — neither the caller's own, which is personal data the store
// deliberately does not hold, nor anything that would tell a caller what the
// door counts it as.
func tooManyRegistrations(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(registerWindow/time.Second)))
	writeJSONStatus(w, http.StatusTooManyRequests, map[string]string{
		"error":             "temporarily_unavailable",
		"error_description": "too many registrations from this source: the endpoint is open to everyone, and this is the bound that keeps one source from writing records for all of them",
	})
}

// serveRegister writes one client registration.
//
// The two things it does not do are the two a registration endpoint is usually
// asked to: it grants nothing (no token, no family, no limit) and it requires
// no credential. What it does do is check every redirect URI before anything is
// written, so a refusal leaves no record at all.
func (s *Server) serveRegister(w http.ResponseWriter, r *http.Request) {
	// The bound first, before the body is read: a source past its window is
	// refused for the cost of one map lookup, and a request that will be
	// refused anyway is not one to spend a body read on.
	if !s.registrations.allow(registerSource(r), s.now()) {
		tooManyRegistrations(w)
		return
	}
	// And the sweep, which is triggered by the traffic that writes the records
	// it removes (maybeSweep). It runs after the refusal above and before this
	// registration's own write, on a request that was admitted.
	s.maybeSweep(r.Context())
	var req registrationRequest
	if err := readJSONBody(w, r, &req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"the registration body is not the JSON object RFC 7591 asks for: this endpoint reads a JSON client metadata document and nothing else"))
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"a registration names at least one redirect URI: it is the only address this installation would send a code to, and a client that names none could never receive one"))
		return
	}
	if len(req.RedirectURIs) > maxRedirectURIs {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			fmt.Sprintf("a registration names at most %d redirect URIs", maxRedirectURIs)))
		return
	}
	for _, uri := range req.RedirectURIs {
		if err := s.checkRedirectURI(uri); err != nil {
			// The message names the rule and the URI the caller sent. Both are
			// the caller's own text and neither is a secret: a redirect URI is
			// registered to be shown to a person on the consent page, and a
			// refusal that did not name it would leave a client author guessing
			// which of its several URIs failed.
			writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(), err.Error()))
			return
		}
	}
	name := req.ClientName
	if name != "" && !printableClientName(name, maxClientNameBytes) {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			fmt.Sprintf("client_name is not a printable name of at most %d bytes", maxClientNameBytes)))
		return
	}

	id, err := NewID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(), "the registration could not be written"))
		return
	}
	secret, err := NewValue()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(), "the registration could not be written"))
		return
	}
	client := Client{
		ID:           id,
		Name:         name,
		RedirectURIs: slices.Clone(req.RedirectURIs),
		SecretHash:   TokenHash(secret),
		CreatedAt:    s.now(),
	}
	if err := s.store.PutClient(r.Context(), client); err != nil {
		// Nothing about the store's own failure is repeated to a stranger: a
		// public endpoint's answer says that it failed and no more, and the
		// detail belongs in the operator's log rather than in the client's.
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(), "the registration could not be written"))
		return
	}
	writeJSONStatus(w, http.StatusCreated, registrationResponse{
		ClientID:                id,
		ClientSecret:            secret,
		ClientIDIssuedAt:        client.CreatedAt.Unix(),
		ClientName:              name,
		RedirectURIs:            client.RedirectURIs,
		TokenEndpointAuthMethod: string(oidc.AuthMethodNone),
		GrantTypes:              []string{string(oidc.GrantTypeCode), string(oidc.GrantTypeRefreshToken)},
		ResponseTypes:           []string{"code"},
		Scope:                   strings.Join(ScopeVocabulary, " "),
	})
}

// checkRedirectURI is D928's rule, in the one place it is written: a loopback
// address is always allowed, because that is how a native client receives a
// code, and every other URI has to match the project's own allowlist exactly.
//
// **Exact match, no wildcards, no prefix matching, no custom schemes.** Each of
// those is a way for a URI that is not the one the project wrote down to be
// treated as if it were: `https://client.example.net/callback.evil` passes a
// prefix rule, `https://client.example.net/` passes a wildcard on the path, and
// a custom scheme is a URI the coordinator cannot reason about at all. The
// allowlist is a line in the project's own configuration and never a constant
// here, so a hosted client is allowlisted by the person who owns the
// installation.
func (s *Server) checkRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %q is not a URL", ErrRedirectURI, raw)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%w: %q does not name an http or https address, and this installation redirects to neither a custom scheme nor anything else", ErrRedirectURI, raw)
	case u.Host == "":
		return fmt.Errorf("%w: %q names no host", ErrRedirectURI, raw)
	case u.User != nil:
		return fmt.Errorf("%w: %q carries credentials in its authority", ErrRedirectURI, raw)
	case u.Fragment != "":
		// RFC 6749 §3.1.2: the redirection endpoint URI must not include a
		// fragment, because the fragment of the request is the client's own
		// and a code written into it would be read by the browser and sent
		// nowhere.
		return fmt.Errorf("%w: %q carries a fragment", ErrRedirectURI, raw)
	}
	if isLoopbackRedirect(u) {
		return nil
	}
	if slices.Contains(s.cfg.RedirectAllowlist, raw) {
		return nil
	}
	return fmt.Errorf("%w: %q is neither a loopback address nor a URI on this project's own allowlist", ErrRedirectURI, raw)
}

// isLoopbackRedirect reports whether a URI is one of the two forms a native
// client receives a code at: `http://127.0.0.1:<port>` and `http://localhost:
// <port>` (D928), plus the IPv6 loopback, which is the same address written the
// other way.
//
// The host is compared as a whole and never as a suffix, so
// `http://127.0.0.1.example.net/cb` and `http://localhost.example.net/cb` are
// not loopback addresses — they are somebody else's hosts, and a suffix test
// would send a person's code to them. The host is also the parsed one: an
// authority that reads `127.0.0.1@evil.example.net` has `evil.example.net` as
// its host and is refused by the check above rather than read as loopback.
func isLoopbackRedirect(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	switch host := u.Hostname(); {
	case host == "127.0.0.1", host == "::1":
		return true
	default:
		// A host is case-insensitive (RFC 3986 §3.2.2), and `LOCALHOST` is the
		// same name written the way a client's configuration happened to.
		return strings.EqualFold(host, "localhost")
	}
}

// printableClientName reports whether a client's name for itself is at most max
// bytes of printable ASCII, spaces included.
//
// It is deliberately not the package's printableASCII, whose floor is one above
// the space: that rule is the email fold's, where a space would make an address
// that is not one, and a client that called itself "an editor" would be refused
// by it. What is refused here is a control character — the name is rendered
// into a page a person reads, and html/template escapes the markup while
// nothing escapes a terminal escape sequence.
func printableClientName(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// readJSONBody decodes one JSON object from the request body under this
// package's size bound.
//
// It is deliberately NOT the strict decoder the store reads its own records
// with. The document here is a stranger's, written against RFC 7591, and a
// client that sends a metadata field this server does not implement is a client
// behaving exactly as the specification allows — refusing it for an unknown
// field would refuse every client that is more complete than this endpoint.
// What is decoded is the two fields above, and everything else is read past.
func readJSONBody(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}

// The registration's own lifetime, which is the second bound of spec.md §8
// (M12, reconciliation.md §4a).
//
// RegistrationTTL is how long a registration lives once no family names it, and
// the figure is the shape and the owner's to set like every other lifetime
// here. **The rule is "and no family names it" rather than the age alone**, and
// the direction it errs in is deliberate: a client that is still refreshing
// keeps the registration its chain was minted against however old it is,
// because removing that record would end a working session at a janitor's hand
// — while a registration nobody has ever signed in through, or whose last
// chain was revoked, or that was made by a stranger who never came back, is
// exactly the record D928's openness leaves behind and this one takes away.
const RegistrationTTL = 30 * 24 * time.Hour

// Swept is what one sweep removed, by shape. It is an answer rather than a
// count because the three shapes are three different records failing three
// different ways, and the log line an operator reads is the only place the
// difference is visible at all.
type Swept struct {
	Registrations int
	Requests      int
	Codes         int
}

// sweepInterval is how often a request may carry a sweep. The sweep is a
// janitor: nobody asked for it, its work is not the caller's answer, and a
// door under a loop must not run one per request — the interval is what turns
// "every write triggers it" into "at most one sweep per interval, run by
// whichever write arrived first".
const sweepInterval = 15 * time.Minute

// sweepTimeout bounds a sweep (maybeSweep), which runs on a context no caller
// can cancel and so needs a bound of its own. It is the order
// ee/coordinator/sweep.go's finalWriteGrace and auditedCall's auditEntryGrace
// are of, because it is the same shape: the door's own bound on housekeeping
// that is not the caller's answer.
const sweepTimeout = 30 * time.Second

// maybeSweep runs the store's sweep, at most once per sweepInterval, inside the
// request that triggered it.
//
// **It is synchronous, and it runs on the caller's values without its
// cancellation.** Synchronous, because a goroutine per trigger would be a
// second writer running beside the door's own answers with a lifecycle nothing
// owns, on a platform that may freeze the CPU the moment the response is
// written — and a context of its own would be the same thing with the freeze
// hidden in it. Detached from the cancellation, because the sweep is this
// door's own housekeeping and not the caller's work: on the caller's context, a
// trigger that disconnected would cancel the sweep *and* spend the interval,
// so the second bound of the open registration endpoint would be one an
// attacker could switch off by hanging up — and the cheapest trigger of all is
// the unauthenticated GET (authorize.go), which anybody can poll and close. The
// timeout is what the caller pays for that instead: this request waits for the
// sweep, and the sweep waits for the store for at most sweepTimeout, which is a
// bound on a janitor and not on an answer that is owed.
//
// The instant is written before the work and not after it: a sweep that fails
// or times out would otherwise be retried by every registration until one of
// them succeeded, which is the burst the interval exists to prevent. What that
// buys is at most one sweep per interval, and it is never a claim that the work
// is done — a sweep a store refused is a sweep the next interval runs again.
//
// A failure is a log line and never an answer: this caller asked to register a
// client, not to hear about the store's janitor, and a registration refused
// because a sweep failed would be an outage caused by housekeeping.
func (s *Server) maybeSweep(ctx context.Context) {
	s.sweepMu.Lock()
	now := s.now()
	due := s.sweptAt.IsZero() || now.Sub(s.sweptAt) >= sweepInterval
	if due {
		s.sweptAt = now
	}
	s.sweepMu.Unlock()
	if !due {
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepTimeout)
	defer cancel()
	swept, err := s.store.SweepExpired(sctx)
	if err != nil {
		s.logger.Warn("mcpauth: the record sweep did not finish", "error", err)
		return
	}
	if swept != (Swept{}) {
		s.logger.Info("mcpauth: the record sweep removed expired records",
			"registrations", swept.Registrations, "requests", swept.Requests, "codes", swept.Codes)
	}
}

// SweepExpired is the bound on what is stored: every registration no family
// names and every single-use record of the flow whose moment has passed is
// removed.
//
// **It is the store's operation and not the endpoint's** (Store.SweepExpired
// says why), and it is written over the record layouthere because the second
// bound belongs to the endpoint that writes these records: the registrations
// are M5's and M12's, and the two flow records are here because a sweep that
// reached only registrations would leave the unauthenticated GET's own lever —
// one record per request, answered or abandoned — growing for as long as
// somebody kept pulling it.
//
// The order is families first, then the three prefixes, and the order matters
// for one reason: what a registration is judged by is whether any family names
// it, and what a spent code's marker is judged by is whether the family it
// names can still be revoked — and both answers come out of one read of the
// families rather than a read per record. A record that disappears between the
// list and the read is skipped rather than reported: something else — a
// rotation's revoke, another sweep — took it, and a sweep is not an audit of
// its own prefixes.
//
// **What it leaves is the direction it errs in.** A client a family still names
// stays; a spent code whose family is still revocable stays; a record whose
// expiry is not stated stays; a record whose expiry is in the future stays.
// Every one of those is a record kept too long rather than a record removed too
// early, which is the only way round that a lifecycle nobody watches can be
// wrong.
func (r records) SweepExpired(ctx context.Context) (Swept, error) {
	var swept Swept
	named, families, err := r.families(ctx)
	if err != nil {
		return swept, err
	}
	now := r.clock()
	keys, err := r.objs.list(ctx, clientPrefix)
	if err != nil {
		return swept, err
	}
	for _, key := range keys {
		var c Client
		if err := r.get(ctx, key, &c); err != nil {
			if errors.Is(err, errMissing) {
				continue
			}
			return swept, err
		}
		// A registration with no stated lifetime is kept, which is the same
		// rule as the records below and a rule this one needs for its own
		// reason: the record an operator writes by hand carries no CreatedAt
		// (D929's pre-registered client), and a client nobody registered
		// through this endpoint is not one this endpoint's bound may end.
		// A record at its own moment is kept for that reason too — the
		// moment has to have passed, which is the same strict reading the two
		// single-use shapes get below.
		if named[c.ID] || c.CreatedAt.IsZero() || !now.After(c.CreatedAt.Add(RegistrationTTL)) {
			continue
		}
		if err := r.objs.delete(ctx, key); err != nil {
			return swept, err
		}
		swept.Registrations++
	}
	if swept.Requests, err = r.sweepSingleUse[Request](ctx, requestPrefix, now, nil); err != nil {
		return swept, err
	}
	if swept.Codes, err = r.sweepSingleUse[Code](ctx, codePrefix, now, families); err != nil {
		return swept, err
	}
	return swept, nil
}

// families is the sweep's one read of the family records, in both of the shapes
// it judges a record by: the client ids they name, which is what keeps a
// registration past its own lifetime, and the records themselves by their own
// id, which is what keeps a spent code's marker.
//
// One read answers both because the two questions are about the same objects: a
// family record is written by the mint and deleted by the revoke, and its client
// id is the registration the whole chain belongs to. A family whose id is empty
// is a record no build of this package wrote and one nothing can look up, which
// is the same reading `aged` gives a family whose age is unknown.
func (r records) families(ctx context.Context) (map[string]bool, map[string]Family, error) {
	keys, err := r.objs.list(ctx, familyPrefix)
	if err != nil {
		return nil, nil, err
	}
	named := make(map[string]bool, len(keys))
	byID := make(map[string]Family, len(keys))
	for _, key := range keys {
		var f Family
		if err := r.get(ctx, key, &f); err != nil {
			if errors.Is(err, errMissing) {
				continue
			}
			return nil, nil, err
		}
		if f.ClientID != "" {
			named[f.ClientID] = true
		}
		if f.ID != "" {
			byID[f.ID] = f
		}
	}
	return named, byID, nil
}

// sweepSingleUse removes the records of one single-use prefix whose own moment
// has passed — spent or not — and leaves a spent code's marker while the family
// it names can still be revoked.
//
// It reads the marker first, and that is not an optimization: a spent record is
// the same object under the same key with a marker in it (spentRecord), so a
// sweep that decoded the shape first would meet its own marker as a record
// that no longer decodes and stop there. The marker carries the moment the
// record it replaced stopped being readable — written by consume out of the
// record itself and kept across BindCodeFamily's rewrite — which is what makes
// a spent code removable at all: a spent code holds no expiry of its own, and
// without that field its marker would outlive the code it stands for by
// forever.
//
// **A spent code's marker is read for the family it names as well as for its
// lifetime.** A spent code is the only single-use record a later presentation
// can act on: the second exchange is refused as already redeemed and the family
// the first one minted is revoked, which is the replay OAuth 2.1 §4.1.3 asks
// for (ConsumeCode's own note). So the marker is kept for as long as that
// revocation would mean anything — while the family it names is in the store
// and inside both of its own bounds — and the code's moment is then the
// earliest moment its marker may go rather than the moment it goes. A family
// the store no longer holds was revoked already, and one past `aged` can be
// rotated no longer, so a marker kept for either names a chain nothing can act
// on; and a family is the only thing here that bounds the marker, because
// nothing else deletes a family and a marker kept for one forever is exactly
// the growth this sweep exists to end.
//
// A record with no stated expiry is left exactly where a record a family names
// is left: the marker of a record written before this field existed decodes
// with the zero time, and the sweep is not the place to guess.
func (r records) sweepSingleUse[T expiring](ctx context.Context, prefix string, now time.Time, families map[string]Family) (int, error) {
	keys, err := r.objs.list(ctx, prefix)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, key := range keys {
		data, _, err := r.objs.read(ctx, key)
		if errors.Is(err, errMissing) {
			continue
		}
		if err != nil {
			return removed, err
		}
		var marker spentRecord
		var expires time.Time
		if marker = spentMarkerIn(data); marker.Consumed {
			expires = marker.ExpiresAt
		} else {
			var rec T
			if err := decode(data, &rec); err != nil {
				return removed, fmt.Errorf("%s: %w", key, err)
			}
			expires = rec.expiresAt()
		}
		if expires.IsZero() || !now.After(expires) {
			continue
		}
		if marker.Consumed && marker.FamilyID != "" {
			if f, ok := families[marker.FamilyID]; ok && !f.aged(now) {
				continue
			}
		}
		if err := r.objs.delete(ctx, key); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
