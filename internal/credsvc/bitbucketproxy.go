package credsvc

// The Bitbucket proxy route (B1.5, D496, D494): the one route of this service
// that serves a run's GIT traffic and its BITBUCKET REST traffic, so that a
// proxied repository's job holds no Bitbucket token at all (G1, the flip B1.13
// part two makes real).
//
// It is the service's half of a pair whose other half is the runner's loopback
// forwarder (B1.6, D495): the agent's own git runs against 127.0.0.1, the
// forwarder attaches a fresh job ID token and the run identity, and this route
// is what decides whether the request may reach Bitbucket and forwards it with
// the one token the bundle holds.
//
// Seven things are the whole of its design.
//
//   - It shares the admission every other run route has and adds nothing to it.
//     RequestFromHTTP reads the two identity headers, Authenticator.Verify
//     proves them, and the pairing (server.go) fixes the canonical repository
//     from the CALLING JOB ACCOUNT — which is what makes "the run's own
//     repository" a fact the caller cannot name (G3, review finding N1). The
//     operator's stop switches are read next, so a stopped service proxies
//     nothing (G8): the kill switches stop this exactly as they stop a model
//     call.
//   - The call itself is named by internal/gitproxy (B1.4) and by nothing here:
//     Classify reads the path and the method, Decide answers for the run and
//     the published block. This file owns no rule about what a run may fetch,
//     push or write — a second spelling of those rules is how they drift, and
//     the rules are the security-sensitive half of this work.
//   - The upstream URL is REBUILT from what the classification decided on
//     (G13): the host, the workspace and the repository from the canonical
//     repository the pairing produced, the action from the call's name, and the
//     query from the classified query. Not one byte of the caller's path or
//     query string is copied upstream, so a spelling the rules read one way and
//     bitbucket.org reads another cannot move a ref nobody checked.
//   - The caller's credentials are dropped and the bundle's token is attached
//     (G5): the Authorization header the caller presented is its job's Google
//     ID token and never leaves this process, and the token that goes upstream
//     is the one the bundle holds — `x-token-auth:<token>` Basic for git
//     (bitbucket.GitUsername) and `Bearer <token>` for the REST API.
//   - A pull-request write is checked against facts THIS SERVICE read
//     (G12): it fetches the pull request with the bundle's token, and
//     CheckPRWrite decides on what the API answered and on the body the caller
//     is about to send — never on what the caller said the pull request was.
//   - Both directions are streamed and no pack is ever held: the push's update
//     request is read in front of the pack (B1.3, G2) and forwarded byte for
//     byte after the rules allowed it, and the rest of the body and the whole
//     of the answer are copied through. Nothing here buffers a pack, which is
//     what the 33 MiB push (Check 190) and the h2c listener (B1.0) exist for.
//   - Every request that reaches this route writes exactly one audit line
//     (G7, C1.13): its purpose is git or bitbucket-api, its credential is the
//     bundle's Bitbucket token, and its status is StatusOK for a forwarded call
//     and the refusal's own type otherwise. A line carries names — the run, the
//     repository, the job, the account, the credential, the purpose — and never
//     a ref, a header, a body or a token: the refusal's CLASS (gitproxy's own
//     vocabulary) is what the service's own log line carries, and the caller's
//     text is in neither.
//
// An owner-listed API root is not served here (O4, and the plan's own default):
// the two upstream hosts are the adapter's own constants
// (bitbucket.DefaultOAuthRoot, bitbucket.DefaultBaseURL) and no environment
// value, no policy field and no request can move them. The API-root pin
// (BW-T2.8) is what protects the NON-proxied repositories, whose runner still
// holds the token; a proxied run's adapter never reads FUGARO_BITBUCKET_API_URL
// at all (B1.7).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitproxy"
)

// gitUpstreamTimeout bounds one proxied git call, headers and body. It is
// twice the model path's (forward.go's upstreamTimeout) because a push is not a
// model call: Cloud Run's own request timeout on this service is 3600 s, so
// this is the front end's bound and not a shorter one of this service's — a
// large push that the front end would have cut is not cut here first, which
// would make the two disagree about a request the runner cannot retry.
const gitUpstreamTimeout = 60 * time.Minute

// maxUpdateRequestBytes bounds the update-request section of a push that this
// route reads before the pack (B1.3). It is not a bound on the push: everything
// past the section's flush packet is streamed and never held. A run pushes one
// ref (refs/heads/<fb>), so a section of a kilobyte is a real one and this is
// three orders of magnitude above it.
const maxUpdateRequestBytes = 1 << 20

// maxPRWriteBodyBytes bounds a pull-request write's body. The bodies the
// adapter sends are short JSON documents (a title, a description, a comment,
// a list of reviewers); a body larger than this is not one of them, and it is
// read before it is checked, because the rules decide on the body's own
// content (gitproxy's CheckPRWrite).
const maxPRWriteBodyBytes = 1 << 20

// maxPRFactsBytes bounds the answer of the pull-request read this route makes
// for itself. A pull request is a JSON document of a few kilobytes; this is
// the bound that keeps an answer that is not one from being held whole.
const maxPRFactsBytes = 1 << 20

// refusalEncodedBody is this route's own refusal class for a git request whose
// body is not identity-encoded. It is not one of gitproxy's classes because it
// is not a rule about what a run may do: it is the transport's own, decided
// before the parser is handed the body (B1.3's contract says the caller decides
// it, because nothing there decompresses and a gzip stream is not a pkt-line
// stream). It joins the same vocabulary in the service's own log line (G7).
const refusalEncodedBody = "body_encoded"

// proxyUnaudited is what a run is told when a call this route would have
// forwarded could not be recorded: a refusal it halts on (D143, the model
// path's answerUnaudited).
const proxyUnaudited = "the Bitbucket call is refused because this service could not record it"

// gitproxyAct names the two facts a line of this route carries about the call
// rather than about what the service did with it (routeAct's pair, read off the
// classification instead of off the path): the purpose the call asked for —
// git's own traffic or the Bitbucket API's — and the credential a line for it
// names, which is the bundle's Bitbucket token whichever way the call goes.
//
// The credential is the route's own name for it and not a value read out of the
// bundle (config.BitbucketSecret is the logical name the bundle's entry
// carries), so a line is the same whether the bundle has been read or not, and
// an operator greps one word for every call this route served (routeAct's rule
// for the mint's App key).
func gitproxyAct(kind gitproxy.Kind) (purpose, credential string) {
	if kind == gitproxy.KindREST {
		return PurposeBitbucketAPI, config.BitbucketSecret
	}
	return PurposeGit, config.BitbucketSecret
}

// bitbucketProxy answers one call of the run's Bitbucket traffic. It is reached
// from ServeHTTP's branch, below the two identities, the pairing and the stop
// switches, and it does there what every other route does: it names the call,
// decides it, reads the credential it forwards with, makes the call, records
// the act, and hands the answer over.
//
// The order below is why each step is where it is:
//
//   - the classification, first, and it is the only reading of the path this
//     service makes (Classify). A path it cannot name is refused here, before
//     the policy or the bundle is read, and a line for it carries the purpose
//     and the credential this route's own pair gives — the class is the
//     classification's and never a byte of what the caller sent.
//   - the push's update request, before the decision and before the policy, for
//     a push only: the rules decide on the refs (G2) and the section has to be
//     read in full before anything is decided about it. It is bounded
//     (maxUpdateRequestBytes), and what was read is kept to be forwarded
//     unchanged after the rules allowed it.
//   - the project's published block, and then the decision. Both the repository
//     the call names and whether this installation proxies it at all are the
//     block's answer (G3, G9), so nothing is decided before it is read.
//   - the pull-request facts, for a write only: the service reads the pull
//     request with the bundle's token and decides on what the API answered
//     (G12). A write whose facts cannot be read is refused rather than allowed.
//   - the bundle and its Bitbucket token, failing closed (D143): a proxied run
//     has no token of its own, so a bundle that holds none is a refusal and
//     never a call forwarded without one.
//   - the upstream call, rebuilt from the classified parts (G13).
//   - the audit line, written before the answer is handed over: an act this
//     service will not record is not made (C1.13, D143), and what "not made"
//     means here is that the answer reaches nobody — the upstream's bytes are
//     dropped rather than relayed.
func (s *Server) bitbucketProxy(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, canonical string) {
	req, err := classifyProxy(r)
	if err != nil {
		reason := gitproxy.ReasonCallNotAllowed
		var refusal *gitproxy.Refusal
		if errors.As(err, &refusal) {
			reason = refusal.Reason
		}
		// The purpose and the credential are this route's own and are known
		// even when the path is not: the route is the Bitbucket proxy and the
		// credential it uses is the bundle's token. What is NOT known is which
		// of the two purposes a call that never classified asked for, so the
		// line names PurposeGit, the route's first purpose — and a class that
		// named neither would be a line an operator cannot place at all.
		s.refuseProxy(ctx, w, id, PurposeGit, reason)
		return
	}
	purpose, credential := gitproxyAct(req.Kind)

	// A push's update request, read in front of the pack and bounded (B1.3):
	// everything the rules decide on is in it, and what was read is forwarded
	// byte for byte if the rules allow the push. git's flush-only probe (the
	// request an http.postBuffer push opens with) is an empty list and is
	// forwarded as the no-op it is.
	var pushHead []byte
	if req.Kind == gitproxy.KindPush {
		if enc := r.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
			s.refuseProxy(ctx, w, id, purpose, refusalEncodedBody)
			return
		}
		updates, consumed, err := gitproxy.ReadUpdateRequest(r.Body, maxUpdateRequestBytes)
		if err != nil {
			// One class for every way the section can be unreadable: the cause
			// is the operator's (the service's own log line carries it) and the
			// caller's answer is the class, which is never a byte of what it
			// sent.
			s.refuseProxyReason(ctx, w, id, purpose, gitproxy.ReasonPushUnread, err)
			return
		}
		req.Updates = updates
		req.UpdatesRead = true
		pushHead = consumed
	}

	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil || !found {
		s.refuseUnavailable(ctx, w, id, purpose, "the project's policy could not be read")
		return
	}
	if verdict := gitproxy.Decide(req, gitproxy.Identity{Run: id.Run, Branch: id.Branch, PRs: id.PRs}, canonical, shared.BitbucketToken); !verdict.Allow {
		s.refuseProxy(ctx, w, id, purpose, verdict.Reason)
		return
	}

	// The body a write is checked on and then forwarded. A git push's is the
	// section already read plus whatever follows it — the pack, streamed and
	// never held. A REST GET has none, and the REST writes' bodies are short
	// JSON documents, read whole because the rules read them (CheckPRWrite).
	var (
		body          io.Reader
		contentLength = int64(-1)
		prBody        []byte
	)
	switch {
	case req.Kind == gitproxy.KindPush:
		body = io.MultiReader(bytes.NewReader(pushHead), r.Body)
		contentLength = r.ContentLength
	case req.Call == gitproxy.CallPRCreate, req.Call == gitproxy.CallPRUpdate, req.Call == gitproxy.CallPRComment:
		raw, err := readBoundedBody(r.Body, maxPRWriteBodyBytes)
		if err != nil {
			s.refuseProxy(ctx, w, id, purpose, gitproxy.ReasonBodyMalformed)
			return
		}
		body = bytes.NewReader(raw)
		contentLength = int64(len(raw))
		prBody = raw
	}

	// The bundle, read before the decision can be acted on: the token this
	// route forwards with is the only credential it has, and a proxied run
	// holds none of its own. A bundle that cannot be read, or holds no
	// Bitbucket token, is a refusal and never a call forwarded without one.
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseUnavailable(ctx, w, id, purpose, BundleRefusal(err))
		return
	}
	entry, ok := b.BitbucketToken()
	if !ok {
		s.refuseUnavailable(ctx, w, id, purpose, "the bundle holds no Bitbucket token, and a proxied run holds none of its own")
		return
	}
	token := entry.Value()

	// A pull-request write is decided on facts this service read for itself
	// (G12). The read is made here rather than in the rules because it needs
	// the token, and it is made with the same rebuilt URL and the same client
	// the write itself will use.
	if req.Kind == gitproxy.KindREST {
		switch req.Call {
		case gitproxy.CallPRCreate, gitproxy.CallPRUpdate, gitproxy.CallPRComment:
			pr, err := s.readPRFacts(ctx, req, token)
			if err != nil {
				// The pull request could not be read: a write whose facts are
				// unknown is refused rather than checked against nothing.
				s.refuseUnavailable(ctx, w, id, purpose, "the pull request a write names could not be read")
				return
			}
			pr.Call = req.Call
			if verdict := gitproxy.CheckPRWrite(pr, prBody, gitproxy.Identity{Run: id.Run, Branch: id.Branch, PRs: id.PRs}, canonical); !verdict.Allow {
				s.refuseProxy(ctx, w, id, purpose, verdict.Reason)
				return
			}
		}
	}
	s.forwardBitbucket(ctx, w, r, req, id, purpose, credential, token, body, contentLength)
}

// classifyProxy reads one call of the proxy route: the path is the caller's
// escaped path with the route's own prefix removed, and the query is the query
// the caller sent. Both go to internal/gitproxy's Classify, which is the only
// reading of a path this service makes (B1.4).
//
// A path that does not begin with the prefix is refused rather than classified:
// this route is reached only for a prefix path (ServeHTTP's gate), and a
// function that guessed at a path it was not given would be a second gate.
func classifyProxy(r *http.Request) (gitproxy.Request, error) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), BitbucketProxyPrefix)
	if !ok {
		return gitproxy.Request{}, &gitproxy.Refusal{Reason: gitproxy.ReasonPathMalformed}
	}
	return gitproxy.Classify(r.Method, "/"+rest, r.URL.Query())
}

// readBoundedBody reads a body this route holds whole (a pull-request write),
// refusing one over the bound rather than clipping it: a body the rules did not
// see in full is a body no rule may decide on.
func readBoundedBody(r io.Reader, max int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return nil, errors.New("credsvc: the call's body could not be read")
	}
	if len(body) > max {
		return nil, fmt.Errorf("credsvc: the call's body is larger than the %d bytes this route accepts", max)
	}
	return body, nil
}

// prFactsAnswer is the part of Bitbucket's pull-request document this route
// reads: the source repository and branch a write is bound to, and the
// destination branch no write may move (G12, gitproxy.PRFacts).
//
// A field the API does not send is empty, and an empty source repository or
// branch matches nothing in CheckPRWrite: the pull request is then no run's, so
// the write is refused rather than allowed on a fact nobody read.
type prFactsAnswer struct {
	Source struct {
		Branch     struct{ Name string } `json:"branch"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"source"`
	Destination struct {
		Branch struct{ Name string } `json:"branch"`
	} `json:"destination"`
}

// readPRFacts reads the pull request a write names, with the bundle's token,
// and returns the facts the rules decide on. The URL is rebuilt from the
// classified parts (G13) — the same repository and number the write itself
// names — and the answer is read bounded and decoded loosely: it is
// bitbucket.org's own document, whose unknown fields are its business, and the
// four fields this route reads are the four the rules ask about.
//
// A status other than 200 is a read that did not happen: a pull request this
// service cannot see is one it cannot decide a write on.
func (s *Server) readPRFacts(ctx context.Context, req gitproxy.Request, token string) (gitproxy.PRFacts, error) {
	target, err := upstreamURL(gitproxy.Request{Kind: gitproxy.KindREST, Repo: req.Repo, Call: gitproxy.CallPRRead, PR: req.PR})
	if err != nil {
		return gitproxy.PRFacts{}, err
	}
	up, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return gitproxy.PRFacts{}, errors.New("credsvc: the pull-request read could not be built")
	}
	up.Header.Set("Authorization", "Bearer "+token)
	up.Header.Set("Accept", "application/json")
	resp, err := s.bitbucketClient().Do(up)
	if err != nil {
		return gitproxy.PRFacts{}, errors.New("credsvc: bitbucket.org could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return gitproxy.PRFacts{}, fmt.Errorf("credsvc: bitbucket.org answered %d for the pull request", resp.StatusCode)
	}
	raw, err := readBoundedBody(resp.Body, maxPRFactsBytes)
	if err != nil {
		return gitproxy.PRFacts{}, err
	}
	var ans prFactsAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return gitproxy.PRFacts{}, errors.New("credsvc: bitbucket.org's pull-request answer is not JSON")
	}
	return gitproxy.PRFacts{
		Number:       req.PR,
		SourceRepo:   ans.Source.Repository.FullName,
		SourceBranch: ans.Source.Branch.Name,
		Destination:  ans.Destination.Branch.Name,
	}, nil
}

// forwardBitbucket makes the upstream call, records the act and streams the
// answer back. It is the last step of the route, and the only one that talks to
// bitbucket.org.
//
// The order is the point of it:
//
//   - the upstream request is built from the classified parts (upstreamURL) and
//     carries the bundle's token and nothing of the caller's own credentials:
//     the Authorization header is set here and the caller's is dropped
//     (dropRequestHeaders), so the job's Google ID token and this contract's
//     headers never leave the process.
//   - the upstream is called before the line is written, because a call that
//     never reached bitbucket.org is a REFUSAL and not an act: it is answered
//     as state_unavailable and its line says so.
//   - the line is written before the answer is handed over (D143). A line that
//     cannot be written drops the answer: the upstream's bytes are closed and
//     never relayed, and the caller gets the refusal instead.
//   - the answer is streamed, never held. Its headers are copied through
//     copyAnswerHeaders' rule — the hop-by-hop ones and Set-Cookie dropped, and
//     the token redacted out of whatever is left, so an upstream that echoed it
//     back cannot have it echoed on.
func (s *Server) forwardBitbucket(ctx context.Context, w http.ResponseWriter, r *http.Request, req gitproxy.Request, id Identity, purpose, credential, token string, body io.Reader, contentLength int64) {
	target, err := upstreamURL(req)
	if err != nil {
		s.refuseUnavailable(ctx, w, id, purpose, "the Bitbucket call could not be addressed")
		return
	}
	up, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		s.refuseUnavailable(ctx, w, id, purpose, "the Bitbucket call could not be built")
		return
	}
	up.Header = forwardHeaders(r.Header)
	if req.Kind == gitproxy.KindREST {
		up.Header.Set("Authorization", "Bearer "+token)
	} else {
		up.SetBasicAuth(bitbucket.GitUsername, token)
	}
	if body != nil {
		// A body with no length the caller named (a chunked push) keeps the
		// unknown length it has: the transport streams it chunked rather than
		// holding it to find out. A call with no body at all (every GET, and
		// the two POSTs of git's advertisement and push that are decided before
		// a byte is read) keeps the zero ContentLength NewRequest gave it — a
		// negative length on a bodyless request is a length with no body, which
		// the transport refuses rather than sends.
		up.ContentLength = contentLength
	}
	resp, err := s.bitbucketClient().Do(up)
	if err != nil {
		s.refuseUnavailable(ctx, w, id, purpose, "bitbucket.org could not be reached")
		return
	}
	defer resp.Body.Close()
	stripRedirect(resp)
	if err := s.auditCall(ctx, id, purpose, credential, "", StatusOK); err != nil {
		// The act this line records is the forwarding of the call, and an act
		// this service cannot record is not made (D143): the answer is dropped
		// rather than relayed.
		//
		// The refusal that follows is refused through refuseCall like every
		// other refusal on this route, so the refusal itself is audited too
		// (the model path's rule, cascade.go): the act that could not be
		// recorded is the call, and a failure to write one line is no reason to
		// leave the line the operator DOES need — the call that got no answer —
		// unwritten.
		s.log.Error("credsvc: a proxied Bitbucket call could not be audited", "error", err)
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable, proxyUnaudited)
		return
	}
	copyAnswerHeaders(w.Header(), resp.Header, token)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// bitbucketClient is the client the proxy route makes its upstream calls with:
// the service's own hardened client (never a redirect, no cookie jar) under the
// git route's own timeout, which is twice the model path's (gitUpstreamTimeout).
//
// The timeout is a field of a COPY of the configured client, so the model path
// keeps the bound it has and a 33 MiB push is not cut at 30 minutes. The
// redirect rule and the nil jar are the configured client's already — NewServer
// puts every client through upstreamClient — so this derives from it rather
// than building a second client that would have to keep those rules itself.
func (s *Server) bitbucketClient() *http.Client {
	c := *s.cfg.Client
	c.Timeout = gitUpstreamTimeout
	return &c
}

// stripRedirect removes a redirect's destination from an upstream answer.
//
// This service follows no redirect (upstreamClient's rule, and what keeps the
// bundle's token from being presented to whatever host an answer named), but
// the ANSWER is relayed — a run has to see bitbucket.org's status — and a
// relayed Location is a redirection the CALLER would follow: the runner's git
// follows one on its initial request, and the hop it would make is a hop to a
// host no rule named, carrying the credential the runner DOES hold (the job's
// ID token, which the loopback forwarder attaches). So the destination is
// dropped and the status is kept: a run told 302 with nowhere to go fails
// loudly, which is what this route wants from a redirect it refuses to make.
func stripRedirect(resp *http.Response) {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return
	}
	resp.Header.Del("Location")
	resp.Header.Del("Content-Location")
}

// dropRequestHeaders are the headers of a caller's request that never go
// upstream, whatever the call is.
//
// Authorization is the caller's own Google ID token — the one Cloud Run IAM
// admitted, which the service sets its own credential over — and the
// X-Fugaro- headers are this contract's own (the run identity and the job the
// run reports), which bitbucket.org has no use for. The rest are the hop-by-hop
// headers of RFC 9110, which describe this hop and not the next one, plus the
// Host (the upstream URL is rebuilt, G13) and Content-Length (recomputed from
// the body this route forwards, which is not always the body the caller sent —
// a push's section is replayed in front of the pack).
var dropRequestHeaders = map[string]bool{
	"Authorization": true,
	"Host":          true, "Content-Length": true,
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Proxy-Authenticate": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

// forwardHeaders is the header set the upstream call is made with: the
// caller's, less the ones above and this contract's own by prefix — a header
// this contract gains later is dropped by the prefix rule too, as readCall
// drops it for the model path. What is left is what git and the REST adapter
// actually send (Content-Type, Accept, User-Agent, Git-Protocol,
// Accept-Encoding), and every one of them is a header the protocol needs rather
// than one the caller can use to reach anything: it names no host and carries
// no credential.
func forwardHeaders(in http.Header) http.Header {
	out := http.Header{}
	for k, vs := range in {
		ck := http.CanonicalHeaderKey(k)
		if dropRequestHeaders[ck] || strings.HasPrefix(ck, "X-Fugaro-") {
			continue
		}
		out[ck] = append([]string(nil), vs...)
	}
	return out
}

// upstreamURL is where one classified call goes, rebuilt from the classified
// parts and from nothing else (G13). It is the second half of Classify's
// contract: what the rules decided on is exactly what is forwarded, so a path
// spelling that would move a ref nobody checked has nowhere to hide.
//
// The two hosts are the adapter's own constants (bitbucket.DefaultOAuthRoot for
// git, bitbucket.DefaultBaseURL for the REST API) and no field of a policy, no
// environment value and no part of the request can move them: an owner-listed
// API root is not served here (O4, the plan's own default for it), and the pin
// that does exist (BW-T2.8) protects the non-proxied repositories' runner.
//
// The workspace and the repository come from the canonical repository the
// pairing produced ("bitbucket/<workspace>/<repo>"), the action from the call's
// own name, and the query from the classified query — which Classify built as a
// fresh set out of the parameters the call may carry, so nothing a caller
// appended rides along.
func upstreamURL(req gitproxy.Request) (string, error) {
	if req.Kind == gitproxy.KindREST && req.Call == gitproxy.CallUser {
		// GET /user is the one call that names no repository: it asks the API
		// which user the token belongs to.
		return bitbucket.DefaultBaseURL + "/user", nil
	}
	workspace, repo, err := splitCanonicalRepo(req.Repo)
	if err != nil {
		return "", err
	}
	if req.Kind != gitproxy.KindREST {
		base := bitbucket.DefaultOAuthRoot + "/" + escapeSeg(workspace) + "/" + escapeSeg(repo) + ".git"
		switch req.Kind {
		case gitproxy.KindFetch:
			if len(req.Query) > 0 {
				return base + "/info/refs?" + req.Query.Encode(), nil
			}
			return base + "/git-upload-pack", nil
		case gitproxy.KindAdvertisement:
			return base + "/info/refs?" + req.Query.Encode(), nil
		case gitproxy.KindPush:
			return base + "/git-receive-pack", nil
		}
		return "", errors.New("credsvc: the git call is not one this route forwards")
	}
	base := bitbucket.DefaultBaseURL + "/repositories/" + escapeSeg(workspace) + "/" + escapeSeg(repo)
	switch req.Call {
	case gitproxy.CallRepoRead:
		return base, nil
	case gitproxy.CallCommitRead:
		return base + "/commit/" + escapeSeg(req.Rev), nil
	case gitproxy.CallPRList:
		return queryURL(base+"/pullrequests", req.Query), nil
	case gitproxy.CallPRRead:
		return base + "/pullrequests/" + itoa(req.PR), nil
	case gitproxy.CallPRCreate:
		return base + "/pullrequests", nil
	case gitproxy.CallPRUpdate:
		return base + "/pullrequests/" + itoa(req.PR), nil
	case gitproxy.CallPRComment:
		return base + "/pullrequests/" + itoa(req.PR) + "/comments", nil
	case gitproxy.CallCommentList:
		return queryURL(base+"/pullrequests/"+itoa(req.PR)+"/comments", req.Query), nil
	}
	return "", errors.New("credsvc: the REST call is not one this route forwards")
}

// queryURL adds a classified query to a rebuilt path. An empty query adds
// nothing at all: a trailing "?" is a path bitbucket.org may read differently
// from the one this service meant.
func queryURL(base string, q url.Values) string {
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// splitCanonicalRepo reads the canonical repository a call names
// ("bitbucket/<workspace>/<repo>") back into its two segments. The empty
// repository — the one /user carries — is refused, because every call that
// reaches here names one, and a call that names none is a call this route
// cannot address.
func splitCanonicalRepo(canonical string) (workspace, repo string, err error) {
	provider, rest, ok := strings.Cut(canonical, "/")
	if !ok || provider != "bitbucket" {
		return "", "", errors.New("credsvc: the call names no Bitbucket repository")
	}
	workspace, repo, ok = strings.Cut(rest, "/")
	if !ok || workspace == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", errors.New("credsvc: the call names no Bitbucket repository")
	}
	return workspace, repo, nil
}

// escapeSeg is one path segment of a rebuilt upstream URL. The segments come
// from a canonical repository, which cannot hold a separator or a control
// character, so this is a belt on the braces rather than a repair: a value that
// changed under a future canonicalisation is encoded rather than carried.
func escapeSeg(s string) string { return url.PathEscape(s) }

// itoa is a pull request's number, which the classification already held to a
// positive integer.
func itoa(n int) string { return fmt.Sprintf("%d", n) }

// refuseProxy answers a request this route refused for what it asked for, and
// records it: one typed refusal to the run and one line to the operator (G7).
//
// The refusal is the wire's own request_refused — the request itself is what
// was refused, and nothing was read — and its sentence carries the class from
// the proxy's own vocabulary and no byte of what the caller sent. The class is
// also what the service's own log line carries, because it is the operator who
// needs to tell a ref from a repository in a refusal, and the audit line's
// fields are names of the act rather than of the request (AuditEvent has no
// free-text field, deliberately).
func (s *Server) refuseProxy(ctx context.Context, w http.ResponseWriter, id Identity, purpose, class string) {
	s.refuseProxyReason(ctx, w, id, purpose, class, nil)
}

// refuseProxyReason is refuseProxy with the cause of a refusal kept for the
// operator's own line. The cause is a Go error written by this repository or
// wrapped from internal/gitproxy's parsers, so it names a rule and never a
// value — and it is deliberately NOT put in the caller's refusal, whose
// sentence is the class alone.
func (s *Server) refuseProxyReason(ctx context.Context, w http.ResponseWriter, id Identity, purpose, class string, cause error) {
	if cause != nil {
		s.log.Warn("credsvc: a Bitbucket call was refused", "purpose", purpose, "run", id.Run, "repository", id.Slug, "class", class, "error", cause.Error())
	}
	s.refuseCall(ctx, w, id, purpose, config.BitbucketSecret, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+class)
}

// refuseUnavailable answers a request this route could not serve for a reason
// that is not the caller's: the policy, the bundle, the credential or
// bitbucket.org itself. It is a typed refusal the run halts on and never a
// fallback (D143), and it is recorded like every other refusal on this route.
func (s *Server) refuseUnavailable(ctx context.Context, w http.ResponseWriter, id Identity, purpose, message string) {
	s.refuseCall(ctx, w, id, purpose, config.BitbucketSecret, "", StatusUnavailable, ErrorStateUnavailable, message)
}
