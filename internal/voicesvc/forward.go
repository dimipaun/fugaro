package voicesvc

// The forwarding half's front door: the two routes the app calls, the order of
// the checks, and the one call to one provider (design §4.4 points 4 and 5,
// §4.5, §8; mobile plan Task P5.3.5).
//
// This is the file that makes the hop a hop. The shell (server.go, P5.3.3)
// verifies a device, reads the policy and answers the project-status query; here
// a call that has passed every gate reaches a provider exactly once, with the
// installation's credential, and its answer streams back.
//
// The order of the checks is the order of the trust, one level on from the
// status query's:
//
//  1. **the token** — nothing a caller sent is a fact before it (auth.go);
//  2. **the device node** — a revoked device is refused whatever it asked
//     (status.go, D166);
//  3. **the request's own shape and the register gate** — the path names one of
//     this hop's engines and one of the two directions, the project is a slug,
//     and the payload is bounded and of the direction's own kind. The register
//     gate belongs at this step and not later: a name this hop does not hold is
//     not a request about a project at all, and the answer to it does not
//     depend on which project the caller claimed;
//  4. **the project's policy** — Enforcer.Decide, which is the enforcement the
//     app cannot do for itself (§4.1, Q3);
//  5. **the credential** — read only once the call is going to be made, so a
//     refused call never touches the key at all;
//  6. **one request, to one engine** — §8's named constant endpoint, and never
//     a second one: nothing here retries, and nothing here falls over to
//     another provider (§4.5's own row).
//
// The two gates of step 3 and step 4 fail closed in the same direction, which is
// the plan's sentence and the design's requirement: a name with no register row
// is refused, and a name the project's policy does not admit is refused, and
// neither narrows into an admission of something else. There is deliberately no
// path from "I could not check" to "so I sent it".

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// The two routes, and the header the project arrives in.
//
// The path is `/v1/voice/<direction>/<engine>`: the direction is this service's
// own word (the two of policy.go's Direction), and the engine is the register's
// name. Neither is a URL, a host or a path a provider spells — a provider's
// route is this tree's own constant (provider.go), and §8's rule is that the
// hop resolves a name and never an address.
//
// The project is a header rather than a member of the payload because the
// payload IS the audio or the text in this direction: a JSON envelope around it
// would put a second encode and decode of the caller's own bytes on the path
// this task exists to keep a stream.
const (
	// STTPathPrefix is the audio direction: `/v1/voice/stt/<engine>`, whose
	// body is the audio.
	STTPathPrefix = "/v1/voice/stt/"
	// TTSPathPrefix is the text direction: `/v1/voice/tts/<engine>`, whose
	// body is the text to speak.
	TTSPathPrefix = "/v1/voice/tts/"
	// HeaderProject is where the call names the project it is for. The same
	// limitation as the status query's applies and is stated rather than
	// hidden (§4.4's first limit): the slug is a claim of the device's owner
	// and not an authenticated fact, and what it is checked against is the
	// policy node.
	HeaderProject = "X-Fugaro-Project"
)

// The two bounds, one per direction. wire.go's maxRequestBytes is the status
// query's and is deliberately not reused: a spoken turn's audio is orders of
// magnitude larger than a slug, and the text of a reply is not a body this
// service should hold in memory without a bound of its own.
const (
	// maxAudioBytes bounds the audio direction's body. A spoken turn is
	// seconds to a minute of compressed speech — a minute of 64 kbit/s is
	// under 500 KiB — so this is generous by an order of magnitude and is
	// still a bound: what it stops is a caller using this service as a place
	// to park bytes, and it is enforced WHILE the body is forwarded (the
	// caller's Content-Length is only a claim).
	maxAudioBytes = 8 << 20
	// maxTextBytes bounds the text direction's body. A reply that is spoken is
	// a paragraph or a few; 4 KiB is several thousand characters, and the
	// bound is the same order as the status query's body for the same reason —
	// a larger one would be a text this service reads into memory to hand to a
	// provider that bills by the character.
	maxTextBytes = 4096
)

// forwardRequest is one call, read: the engine it names, the project it claims
// and the payload.
type forwardRequest struct {
	// engine is the register's name resolved to this hop's own entry for it.
	engine engine
	// project is the slug the header carried.
	project string
	// input is the payload: the caller's live body for the audio direction,
	// the text for the text direction.
	input callInput
	// audio is the reader that counts the audio against maxAudioBytes and
	// reports whether the bound was passed, and is nil for the text direction.
	audio *boundReader
}

// ReadForwardRequest reads a forwarding request's shape: the engine out of the
// path, the project out of the header and the payload out of the body. It is
// the register gate's home: a name this hop holds no row for, and a name whose
// row does not serve the direction asked for, are both refused here and by one
// reason, so a caller cannot enumerate this service's engines by trying
// directions.
//
// Nothing reads the caller's body before this: a caller that named no engine
// this service has, or no project, has not had a byte of its payload read —
// which matters in this direction more than anywhere, because the payload is
// the caller's voice.
func ReadForwardRequest(r *http.Request, dir Direction, reg *Register) (forwardRequest, error) {
	name, ok := engineName(r.URL.Path, dir)
	if !ok {
		return forwardRequest{}, refuse(ReasonBadRequest, fmt.Errorf("the path names no engine: the %s route is %s<engine>, where the engine is one of the names this service's register holds", dir, pathPrefix(dir)))
	}
	row, known := reg.Row(name)
	if !known {
		return forwardRequest{}, refuse(ReasonUnknownEngine, fmt.Errorf("this service holds no engine by that name: the register's engine names are the ones this service can reach, and a name outside it is not a request about a provider at all"))
	}
	if !row.Serves(dir) {
		return forwardRequest{}, refuse(ReasonUnknownEngine, fmt.Errorf("this service forwards no %s call for that engine: which directions each engine is served in is the register's own record", dir))
	}
	eng, held := engineFor(name, dir)
	if !held {
		// The register admitted a name and direction this release has no call
		// for. TestRegisterAndEnginesAgree holds the two to one answer, so this
		// is a build fault rather than anything a caller did — and it is
		// answered as this service's own state, never as an admission.
		return forwardRequest{}, refuse(ReasonStateUnavailable, fmt.Errorf("this service's register and its engine table disagree about one of the engines, so no call is made for it"))
	}
	project := r.Header.Get(HeaderProject)
	if !projectRE.MatchString(project) {
		return forwardRequest{}, refuse(ReasonBadRequest, fmt.Errorf("the request names no project: a project comes in %s as a slug — the lowercase repository name, a dash and its sixteen hex digits — which is the key the policy node is written under", HeaderProject))
	}
	req := forwardRequest{engine: eng, project: project}
	if eng.audio {
		mediaType, err := audioMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			return forwardRequest{}, refuse(ReasonBadRequest, err)
		}
		if r.ContentLength > maxAudioBytes {
			return forwardRequest{}, refuse(ReasonBadRequest, fmt.Errorf("the request's audio is over this service's bound of %d bytes", maxAudioBytes))
		}
		// The body is handed on as it is: this service reads it only as the
		// provider's transport does (provider.go), and the counter below is
		// what holds it to the bound on the way.
		// left is the bound plus the one byte that proves the bound was
		// passed: a body of exactly the bound is admitted, and a body one byte
		// larger is refused by the read that would carry that byte.
		req.audio = &boundReader{r: r.Body, left: maxAudioBytes + 1}
		req.input = callInput{audio: req.audio, audioType: mediaType, audioLength: r.ContentLength}
		return req, nil
	}
	body, err := readBody(r, maxTextBytes)
	if err != nil {
		return forwardRequest{}, refuse(ReasonBadRequest, err)
	}
	if len(body) == 0 {
		return forwardRequest{}, refuse(ReasonBadRequest, fmt.Errorf("the request carries no text: the %s route's body is the text to speak", TTS))
	}
	if !utf8.Valid(body) {
		// The text goes into a JSON document a provider parses. Invalid UTF-8
		// would be replaced, re-encoded or refused at the provider, and the
		// caller's own bytes are not something this service should hand on
		// with a guess about what they were.
		return forwardRequest{}, refuse(ReasonBadRequest, fmt.Errorf("the request's text is not UTF-8"))
	}
	req.input = callInput{text: string(body)}
	return req, nil
}

// engineName reads the engine out of a path of this direction's route, and
// reports whether the path is one of the route's own at all.
//
// The name is checked against the register's pattern before anything looks it
// up, so a path carrying a slash, a query, a space or a long tail is a refusal
// about the request's shape rather than a lookup. It is a name and never a
// path: a caller that sent `deepgram/../../x` reaches no other route here,
// because this service has two routes and both are matched exactly.
func engineName(path string, dir Direction) (string, bool) {
	rest, ok := strings.CutPrefix(path, pathPrefix(dir))
	if !ok || !registerNameRE.MatchString(rest) {
		return "", false
	}
	return rest, true
}

// pathPrefix is a direction's route prefix.
func pathPrefix(dir Direction) string {
	if dir == TTS {
		return TTSPathPrefix
	}
	return STTPathPrefix
}

// boundReader forwards a caller's body to the provider and refuses one that
// passes maxAudioBytes, wherever the caller put the end of it.
//
// The bound is enforced here and not only against the caller's Content-Length,
// because that figure is a claim: a request that declares a hundred bytes and
// sends a gigabyte is one the transport would otherwise hand to the provider in
// full, on this service's credential. The two failures are told apart — the
// bound and the provider's own end of the body — so the answer can say which
// happened, and `over` is read by the handler after the call that read the body
// has returned.
type boundReader struct {
	r    io.Reader
	left int64
	over atomic.Bool
}

// errAudioOverBound is the boundReader's own failure, and it is what the
// handler looks for to answer 400 rather than "the provider failed": the
// provider never saw a complete call, and what was wrong was the request.
var errAudioOverBound = errors.New("voicesvc: the request's audio is over this service's bound")

func (b *boundReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// Everything the bound allows has been forwarded and the caller is
		// still sending: this read is the one that refuses, and the handler
		// answers the request rather than the provider.
		b.over.Store(true)
		return 0, errAudioOverBound
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}

// forward is the whole of the forwarding call: the checks in order, one request
// to one engine, and the answer streamed back.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, start time.Time, dir Direction) {
	cl := callLog{act: "voice forward", direction: dir.String()}
	deny := func(err error) { s.refuseErr(w, start, cl, err) }

	tok, ok := BearerToken(r.Header.Get(HeaderAuth))
	if !ok {
		deny(refuse(ReasonTokenUnverified, errors.New("present the device's Firebase ID token as "+HeaderAuth+": "+authScheme+"<token>")))
		return
	}
	id, err := s.auth.VerifyDevice(r.Context(), tok)
	if err != nil {
		deny(err)
		return
	}
	cl.device = id.UID
	if _, err := s.devices.DeviceExists(r.Context(), id.UID); err != nil {
		deny(err)
		return
	}
	req, err := ReadForwardRequest(r, dir, s.register)
	if err != nil {
		deny(err)
		return
	}
	cl.project, cl.provider = req.project, req.engine.name
	if err := s.enforcer.Decide(r.Context(), req.project, req.engine.name, dir); err != nil {
		deny(err)
		return
	}
	cred, err := s.credential(r.Context(), req.engine)
	if err != nil {
		deny(err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// The context is what ends the provider's call when the phone goes away:
	// a caller that stopped recording must not leave this service reading a
	// stream nobody will hear. Nothing else ends it — no deadline of this
	// service's own, because the provider's own end of the body is what says a
	// call is over — and the provider's answer is not retried when it ends
	// (§4.5).
	up, err := req.engine.request(ctx, req.input, cred)
	if err != nil {
		deny(refuse(ReasonStateUnavailable, err))
		return
	}
	resp, err := s.client.Do(up)
	if err != nil {
		if req.audio != nil && req.audio.over.Load() {
			// The call ended because this service refused the caller's body
			// mid-stream. That is the request's own failure and not the
			// provider's, and nothing has been written to the caller yet.
			deny(refuse(ReasonBadRequest, errAudioOverBound))
			return
		}
		// The transport's own error is NOT carried: it is not this tree's text
		// (a TLS failure quotes the certificate it refused) and the app acts on
		// the reason. The log line carries the type.
		deny(refuse(ReasonProviderFailed, errors.New("this service could not reach the provider: the call failed before it was answered")))
		return
	}
	defer resp.Body.Close()
	cl.providerStatus = resp.StatusCode
	if resp.StatusCode/100 != 2 {
		// A provider's failure is never relayed, and never read: the body may
		// echo the key this service presented, and the app's answer to a failed
		// call is the local engine either way (§4.5).
		deny(refuse(ReasonProviderFailed, errors.New("the provider answered with a failure: the call was not served")))
		return
	}
	copyAnswerHeaders(w.Header(), resp.Header)
	rw := newRedactWriter(w, cred)
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	end, n := streamAnswer(rw, rc, resp.Body)
	_ = rw.Close()
	// The line is written for every way this ends: the bytes the caller was
	// sent, the provider's own status, and — when the copy did not reach the
	// provider's end of the body — which end stopped it. A cut is a fact about
	// the call and not a refusal, so no reason is claimed for it: the refusal
	// body was never written, because a 200 and some of the answer already
	// were.
	cl.bytes, cl.status = n, http.StatusOK
	switch {
	case end.readErr:
		cl.cut = cutProvider
	case end.writeErr:
		cl.cut = cutClient
	}
	s.logCall(cl)
	if end.readErr {
		// The provider's stream failed mid-answer, after a 200 and some of the
		// bytes. The caller must NOT read what it holds as the whole answer:
		// half a sentence of audio played as if it were the reply is worse than
		// a failed call, which the app answers by speaking locally. So the
		// response is aborted rather than finished, in net/http's own way for a
		// proxy that could not copy the body (httputil.ReverseProxy's rule):
		// the chunked body ends without its terminator and the client reads a
		// truncated transfer rather than a short answer.
		panic(http.ErrAbortHandler)
	}
}

// credential resolves the one credential this engine's call presents, and
// refuses rather than answering an empty one: a provider handed an empty
// credential answers 401, which would read as this installation's key being
// wrong rather than as it being absent.
//
// It is called after the policy has admitted the call, so a refused call never
// reads the key at all — and it is called once per call, so the key is read
// from Secret Manager at most once per call and never per chunk.
func (s *Server) credential(ctx context.Context, e engine) (string, error) {
	switch e.auth.from {
	case fromServiceAccount:
		tok, err := s.tokens.Token(ctx)
		if err != nil {
			return "", refuse(ReasonStateUnavailable, err)
		}
		if tok == "" {
			return "", refuse(ReasonStateUnavailable, errors.New("this service's own access token is empty"))
		}
		return tok, nil
	default:
		key, err := s.keys.Key(ctx, e.name)
		if err != nil {
			return "", refuse(ReasonStateUnavailable, err)
		}
		if key == "" {
			return "", refuse(ReasonStateUnavailable, fmt.Errorf("%w: the key for that engine is empty", ErrKeyUnavailable))
		}
		return key, nil
	}
}
