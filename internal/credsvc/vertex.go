// vertex.go is the project's Vertex as a source of the chain (CS4): a
// credential this service serves ON ITS OWN IDENTITY, as it serves a key, and
// the translation that makes that possible.
//
// Vertex is not a base URL and a header, which is what every other source of
// this service is: the harness has always built its request itself, with the
// model in the URL path and `anthropic_version` in the body (the agent's own
// calls, internal/runner/directcall.go and internal/agent/fakeclaude), and
// internal/gateway/vertex.go only VALIDATES the path such a call arrives on —
// nothing in this repository has ever translated a Messages call onto Vertex's
// wire. That translation is this file's work, and it is the whole of why Vertex
// can be a paid source at all: a call the agent hands this service is a Messages
// call with the model in the body, and what Vertex's publisher answers is a URL
// whose path names the model and whose body names no model at all.
//
// The identity is the installation's own (D3174 as amended): the Cloud Run
// service account this process runs as, through the metadata server, whose
// project and region are the ones the service's own environment carries
// (FUGARO_GCP_PROJECT and FUGARO_REGION, the two names that are also the run's
// ANTHROPIC_VERTEX_PROJECT_ID and CLOUD_ML_REGION: one fact, written twice). The
// account's Vertex permission is planned by CS11 and is not this file's; what is
// this file's is that no caller's token is ever the one a Vertex call carries —
// the run presents its identity and its job's ID token to THIS service
// (auth.go), and neither reaches Vertex.
//
// What Vertex costs a run is a key's cost and no more (F56, R23): the answer
// carries the same tier and credential headers a key's answer carries, so the
// run's own gateway prices and settles it as real money against its caps, and a
// call an agent builds itself and posts straight to this service is served
// under the policy's rules and counted against nothing — the residual risk
// SECURITY.md already records for a key (D3003).

package credsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

const (
	// vertexUpstreamKind is the kind gateway.UpstreamHeaders builds a Vertex
	// call for. It is spelled from the config vocabulary that also names the
	// mode (config.CredentialTypeVertex, the built-in credential's type and the
	// `auth: vertex` a run's own environment calls vertex), because
	// internal/gateway keeps that string unexported. That package's own scan
	// over the tree (internal/gateway/oauth_test.go) refuses any file outside
	// its tier-A set that names the subscription kind at all, which is why the
	// name of that kind is written nowhere here.
	vertexUpstreamKind = config.CredentialTypeVertex

	// vertexAnthropicVersion is the version of the publisher wire, which Vertex
	// requires in the body: the same value the agent's own harness writes when
	// it builds a Vertex request, so a call translated here and one built there
	// are one wire.
	vertexAnthropicVersion = "vertex-2023-10-16"

	// vertexGlobalLocation is the location whose endpoint is the global one and
	// not a regional host (gateway.vertexBase's own rule, respelled below).
	vertexGlobalLocation = "global"

	// vertexProjectEnv and vertexRegionEnv are the two facts of the service's
	// own environment this file reads: the GCP project the container lives in
	// and the installation's region, which are the run's own Vertex project and
	// region as well (internal/infra sets CLOUD_ML_REGION and
	// ANTHROPIC_VERTEX_PROJECT_ID from the same two values). They are read here
	// rather than carried on Config because they are facts of the deployment
	// and not of a call: no caller, no policy and no record may choose the
	// project this service reaches Vertex in.
	vertexProjectEnv = "FUGARO_GCP_PROJECT"
	vertexRegionEnv  = "FUGARO_REGION"
)

var (
	// vertexSegmentRE is one id that becomes a segment of the Vertex URL — the
	// project and the location — held to internal/gateway's own location rule
	// (its locationRE) rather than validated at all: what this service assembles
	// becomes a host name and a path, and a value with a slash, a dot or a colon
	// in it would address something nobody chose.
	vertexSegmentRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	// vertexModelRE is a model as a Vertex path names it, which is
	// internal/gateway's own rule over the same member (vertexModelRE there):
	// the spelling the checked-in map answers is written into the path, and the
	// rule that admits it is that package's rather than this one's.
	vertexModelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.@_-]*$`)
)

// vertexTokenFor builds the token source every Vertex call carries: the identity
// THIS service runs as, and never the caller's.
//
// It is a variable for the reason identitySignerFor is: a test replaces where
// the credential comes from, and everything above it — the header build, the
// scrub of the caller's own tokens, the translation — is the real code either
// way. The scope is the signer's own (cloud-platform), because it is the same
// account this service already acts as.
var vertexTokenFor = func(ctx context.Context) (oauth2.TokenSource, error) {
	return google.DefaultTokenSource(ctx, identitySignerScope)
}

// serveVertex serves one call from the project's Vertex: the source the chain
// names with routing.KindVertex.
//
// It answers the protocol every source of the walk (CS1) answers: done says
// this source has answered the caller — a served call, or a refusal it wrote —
// and when done is false the source declined this call and the walk moves on
// with the Tried row beside it.
//
// Three of those declines are not refusals of the call and write no audit line.
// A repository the source is not admitted to is the first (D3182: an empty admit
// list admits nobody), and a 401 or a 429 that marked the source is the other
// two: in all three the call is refused — or served elsewhere — by the walk,
// which writes exactly one line for the call it could not place. A line here
// would be one line per skipped source for one refused call, and the contract's
// rule is one line for the call.
//
// The admission is checked here as well as in the walk, and deliberately: this
// source spends the PROJECT's own Vertex on the SERVICE's identity, so the one
// fact that must never be taken on trust is whose call it is being spent for.
// The repository is read from the verified identity's slug through the scope's
// own table (D1066: a canonical repository is never guessed from the slug's
// text), and a slug the table does not hold is admitted by nothing.
func (s *Server) serveVertex(ctx context.Context, w http.ResponseWriter, id Identity, cl *call, sel *creds.Selector, src routing.Source) (bool, Tried) {
	name := config.CredentialVertex
	cred := creds.Credential{Name: name, Tier: creds.TierB}
	tier := wireTier(cred.Tier)
	canonical := scopeOfCall(ctx, s).Repos()[id.Slug]
	if canonical == "" || !projectpolicy.AdmitRepo(src.Admit, canonical) {
		return false, Tried{Name: name, Tier: tier, Reason: ReasonNotAdmitted}
	}
	project, location := os.Getenv(vertexProjectEnv), os.Getenv(vertexRegionEnv)
	if !vertexSegmentRE.MatchString(project) || !vertexSegmentRE.MatchString(location) {
		s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
			"this service does not know the GCP project and region it reaches Vertex in, so the project's Vertex cannot be served: a deployment sets them in the service's own environment")
		return true, Tried{}
	}
	requested, ok := callModel(cl.body)
	if !ok {
		s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"this service reads the model from the request's body, and a Vertex call names no model it could be translated for")
		return true, Tried{}
	}
	// Which model is served is the source's own member when it sets one (a
	// written downgrade) and the requested model otherwise; how it is written is
	// the checked-in map's answer alone (D3175, pricing.SpellingFor). A model
	// the map does not spell for Vertex is refused rather than sent there as
	// something nobody measured, and the map is CS3's: until it carries a Vertex
	// spelling, no call is sent to Vertex at all.
	served := requested
	if src.Model != "" {
		served = src.Model
	}
	spelling, spelled := pricing.SpellingFor(string(routing.KindVertex), served)
	if !spelled || !vertexModelRE.MatchString(spelling) {
		s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorNoCandidateCredentials,
			"the model this call asked for has no spelling Vertex is reached with, so this source could not be sent it: a model is written on a source by the checked-in map alone, and a model the map does not spell for Vertex is refused rather than sent there as something nobody measured")
		return true, Tried{}
	}
	// Which PUBLISHER serves the model decides the whole request and not only
	// the path: Vertex's Anthropic publisher takes a Messages call (the
	// translation above), its Google publisher a generateContent call
	// (vertex_gemini.go, GV1), and the two bodies have nothing in common. The
	// map that spells the model for Vertex is the one that says which — a
	// spelling answers both questions or neither — so a caller cannot build a
	// request for one publisher out of the other's answer.
	publisher, published := pricing.VertexPublisher(served)
	if !published {
		s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorNoCandidateCredentials,
			"the model this call asked for has no publisher Vertex serves it under, so this source could not be sent it: which publisher a model goes to is the checked-in map's answer, as its spelling is")
		return true, Tried{}
	}
	path, ok := vertexPathFor(publisher, cl.path, project, location, spelling, vertexStreams(cl.body))
	if !ok {
		s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
			"the vertex source was reached on a path it does not serve")
		return true, Tried{}
	}
	body, err := vertexBodyFor(publisher, cl.body, spelling, cl.path == CountTokensPath)
	if err != nil {
		// A member this translation cannot carry faithfully is the one error
		// that is the CALLER's: the call is well-formed and it is this wire that
		// has no form of it, which is what the typed reason says (GV1). Every
		// other error here is the service's own reading of the body.
		if reason, typed := vertexTranslationRefusal(err); typed {
			s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusRequestRefused, ErrorRequestRefused, reason, err)
			return true, Tried{}
		}
		s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"the body of this call is not one Vertex could be sent", err)
		return true, Tried{}
	}
	tokens, err := vertexTokenFor(ctx)
	if err != nil {
		// The error is the credential's own and is logged, never answered: a
		// refusal names the field and never what the call sent.
		s.log.Error("credsvc: the service's own Vertex token source could not be built", "error", err)
		s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
			"the service could not reach the identity it serves the project's Vertex with")
		return true, Tried{}
	}
	scrub := ""
	if len(cl.scrubs) > 0 {
		scrub = cl.scrubs[0]
	}
	hdr, key, err := gateway.UpstreamHeaders(gateway.Upstream{Kind: vertexUpstreamKind, Token: tokens}, cl.header, scrub)
	if err != nil {
		s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
			err.Error(), labelRead(readUpstream, err))
		return true, Tried{}
	}
	// The workspace's cap brackets this attempt as it brackets a key's: Vertex
	// is money the workspace pays, and the only thing that differs from a key
	// call is where the model is written.
	s.withMeter(ctx, cl, id)
	ans, err := s.sendRequest(ctx, cl, sentRequest{
		dest:       vertexDestination(location, path),
		body:       body,
		header:     hdr,
		credential: key,
		name:       name,
		tier:       cred.Tier,
		model:      served,
	})
	if err != nil {
		if errors.Is(err, errMeterRefused) {
			s.refuseMeter(ctx, w, id, PurposeForward, cl.fail)
			return true, Tried{}
		}
		s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
			err.Error(), labelRead(readUpstream, err))
		return true, Tried{}
	}
	// A Gemini answer is Vertex's Google-publisher wire — a
	// GenerateContentResponse, not a Messages answer — so it is read here, whole
	// and once, and translated before anything else looks at it (GV2). Two facts
	// put the read here rather than beside the answer: the translation is
	// whole-answer by construction (a stream is accumulated across its chunks
	// before a byte is written, which is what makes "never a half-translated
	// stream" true), and a quota refusal can arrive under any status, so the
	// bytes must be in hand before the marks below are decided.
	gemini := publisher == pricing.VertexPublisherGoogle
	var geminiRaw []byte
	if gemini {
		held, err := io.ReadAll(io.LimitReader(ans.resp.Body, vertexGeminiMaxAnswer+1))
		ans.resp.Body.Close()
		ans.resp.Body = http.NoBody
		if err != nil || len(held) > vertexGeminiMaxAnswer {
			// The call went out and may have been billed, and nothing about
			// this answer is readable: a nil usage settles the reservation at
			// what it holds, which is the direction a cap must fail in.
			if fail := s.settleCall(ctx, cl, nil); fail != nil {
				s.refuseMeter(ctx, w, id, PurposeForward, fail)
				return true, Tried{}
			}
			s.refuseCall(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
				"the answer Vertex gave for this model could not be read whole, so it could not be translated into the answer the agent reads: a streamed answer is translated as one answer, and a prefix of one is not an answer")
			return true, Tried{}
		}
		geminiRaw = held
	}
	// A quota refusal is a limit on the project's Vertex whatever status carried
	// it: Google answers RESOURCE_EXHAUSTED with a 429 and, for a project over
	// its quota, with a 403 (vertexGeminiQuota). Both mark the source and move
	// the chain on, which is what a Gemini 403 must not be read as — a
	// permission error would reach the agent as a refusal of its own call.
	quota := gemini && vertexGeminiQuota(geminiRaw)
	// What this source's answer carried is recorded in the same three arms the
	// chain's own walk records it in — the answers this service reads as about
	// the CREDENTIAL — as the names of its rate-limit headers and never a value
	// (U1, Check 880). A Vertex answer carries Google's names, and the record
	// says so rather than keeping the last one a Claude answer left.
	switch status := ans.resp.StatusCode; {
	case unauthorizedStatus(status):
		// A 401 marks the source, and the walk moves on or answers exhausted
		// with what the state says. Only an operator clears a broken mark
		// (C1.21), and the attempt is settled at the zero a 401 is worth: an
		// unbilled attempt must not eat the workspace's month. What the 401
		// carried is recorded here (U1, Check 880): the observation is a node of
		// its own, so the order against the hold below decides nothing.
		s.recordRateLimitHeaders(ctx, sel, name, ans.resp.Header)
		ans.resp.Body.Close()
		if fail := s.settleCall(ctx, cl, unchargedUsage()); fail != nil {
			s.refuseMeter(ctx, w, id, PurposeForward, fail)
			return true, Tried{}
		}
		if _, err := sel.Broken(ctx, name); err != nil {
			s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
				markRefusal, err)
			return true, Tried{}
		}
		return false, Tried{Name: name, Tier: tier, Reason: ReasonBroken}
	case usageLimitStatus(status) || quota:
		// A Vertex 429 is a limit on the project's Vertex as much as a key's
		// 429 is a limit on the key, and Google's own quota refusal is the same
		// fact under another status. Nothing has been streamed, so the walk may
		// move to its next source, and the mark carries whatever reset the
		// answer named. The names the limit carried are the answer Check 880 is
		// read after, so they are recorded here (U1, D3178).
		s.recordRateLimitHeaders(ctx, sel, name, ans.resp.Header)
		reset := usageLimitReset(ans.resp.Header, s.now())
		ans.resp.Body.Close()
		if fail := s.settleCall(ctx, cl, unchargedUsage()); fail != nil {
			s.refuseMeter(ctx, w, id, PurposeForward, fail)
			return true, Tried{}
		}
		if _, err := sel.Exhausted(ctx, name, cred.Tier, reset); err != nil {
			s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable,
				markRefusal, err)
			return true, Tried{}
		}
		return false, Tried{Name: name, Tier: tier, Reason: ReasonExhausted}
	}
	// The answer is translated into the shape the agent reads (GV2): the
	// Messages answer or the token count for a 2xx, and Google's error named in
	// Anthropic's own vocabulary for anything else, which is the shape the
	// harness switches on. The translated bytes replace the upstream body, so
	// everything downstream — the workspace's meter, the usage-limit scan, the
	// pump — reads the answer the agent reads, and the media type says which of
	// the two it is (a translated stream is an event stream, and the answer path
	// reads it as one).
	if gemini {
		out, ctype := geminiRaw, contentTypeJSON
		if ans.resp.StatusCode >= 200 && ans.resp.StatusCode <= 299 {
			var err error
			out, ctype, err = vertexGeminiTranslated(cl.path, vertexStreams(cl.body), geminiRaw, requested)
			if err != nil {
				// The call reached the source and the source answered something
				// this wire has no translation for: the call ends here, and the
				// reservation stands rather than being released — the answer was
				// billed and its usage is exactly what could not be read. The
				// typed reason names the member and reaches the service's own log
				// line; the sentence the run reads names the source's failure and
				// not the caller's request, which was well-formed and reached
				// Vertex.
				if fail := s.settleCall(ctx, cl, nil); fail != nil {
					s.refuseMeter(ctx, w, id, PurposeForward, fail)
					return true, Tried{}
				}
				reason, typed := vertexAnswerRefusal(err)
				if !typed {
					reason = "the answer Vertex gave for this model is not the JSON this service translates, so the call could not be answered: the source answered something this translation cannot read"
				}
				s.refuseCallCause(ctx, w, id, PurposeForward, name, tier, StatusUnavailable, ErrorStateUnavailable, reason, err)
				return true, Tried{}
			}
		} else {
			out = vertexGeminiError(ans.resp.StatusCode, geminiRaw)
		}
		ans.resp.Body = io.NopCloser(bytes.NewReader(out))
		ans.resp.Header.Set("Content-Type", ctype)
		// What is handed on is this service's own JSON or event stream and never
		// whatever encoding the upstream labelled its bytes with.
		ans.resp.Header.Del("Content-Encoding")
		ans.resp.ContentLength = int64(len(out))
	}
	// Everything else is the caller's answer, exactly as it came: a 2xx, a 400
	// for a request Vertex refused, an overloaded_error, a 500. It is answered
	// with the tier and the name of the project's Vertex, which is what tells
	// the run's own gateway to price and settle it as money.
	//
	// A serve's names are recorded and the relayed refusals' beside it are not:
	// a record is a claim that an answer says something about this source, and a
	// 400 or a 500 says something about the request (the chain's own rule, U1,
	// Check 880).
	if code := ans.resp.StatusCode; code >= 200 && code < 300 {
		s.recordRateLimitHeaders(ctx, sel, name, ans.resp.Header)
	}
	scan := &usageLimitScanner{}
	s.answer(ctx, w, id, cl, ans, cred, nil, scan)
	if scan.Found() {
		// A limit inside a 200 stream: the bytes are the caller's already, so
		// this call is not retried and the source is marked with no reset
		// (U7). The context is stripped of its cancellation for the walk's
		// reason: the exhaustion is a fact about the source whether or not the
		// caller stayed to read the answer.
		if _, err := sel.Exhausted(context.WithoutCancel(ctx), name, cred.Tier, time.Time{}); err != nil {
			s.log.Warn("credsvc: a usage limit seen inside a stream could not be recorded", "error", err)
		}
	}
	return true, Tried{}
}

// vertexPath is the path one call takes on Vertex's Anthropic publisher:
// /v1/projects/<project>/locations/<location>/publishers/anthropic/models/<model>:<method>,
// with the model in the path for a Messages call and the counting endpoint for a
// token count. ok is false for a path this source does not serve, which is every
// path but the two C1 declares.
func vertexPath(callPath, project, location, spelling string, stream bool) (string, bool) {
	prefix := "/v1/projects/" + project + "/locations/" + location + "/publishers/anthropic/models/"
	switch callPath {
	case MessagesPath:
		method := "rawPredict"
		if stream {
			method = "streamRawPredict"
		}
		return prefix + spelling + ":" + method, true
	case CountTokensPath:
		// The counting endpoint names its model in the BODY and not in the
		// path (gateway.vertexRoute's own reading of "count-tokens" as a model
		// that is not one), and it is free on every tier, so nothing brackets it.
		return prefix + "count-tokens:rawPredict", true
	}
	return "", false
}

// vertexPathFor is the path one call takes on the publisher that serves the
// model — the Anthropic publisher CS4 builds for every model this repository
// served on Vertex before GV1, and the Google publisher GV1 builds for a Gemini
// model. The publisher is not a preference and is never a fallback: it is the
// map's answer for the model (pricing.VertexPublisher), and serveVertex has
// already refused a model with none.
func vertexPathFor(publisher, callPath, project, location, spelling string, stream bool) (string, bool) {
	if publisher == pricing.VertexPublisherGoogle {
		return vertexGeminiPath(callPath, project, location, spelling, stream)
	}
	return vertexPath(callPath, project, location, spelling, stream)
}

// vertexBodyFor is the body of that same call, drawn from the same publisher:
// vertexBody (the Messages translation) for the Anthropic publisher, and
// vertexGeminiBody (GV1's) for the Google one. The counting flag is C1's token
// count, which each publisher's counting endpoint takes a different body for.
func vertexBodyFor(publisher string, body []byte, spelling string, counting bool) ([]byte, error) {
	if publisher == pricing.VertexPublisherGoogle {
		return vertexGeminiBody(body, counting)
	}
	return vertexBody(body, spelling, counting)
}

// vertexDestination is the URL one Vertex call goes to: the host the location
// decides and the path the translation built, and nothing of the caller's own
// query.
//
// The query is DROPPED, which is the opposite of what the other sources of this
// service do with it, and deliberately: this call is made on the SERVICE's own
// identity, and Google binds a query parameter to whoever sends it. `userProject`
// and `quotaUser` bill or attribute the call to a project the CALLER named —
// the same address as the X-Goog-User-Project header internal/gateway's
// dropRequestHeaders already refuses to forward, one layer down — `key` carries
// a credential of the caller's, and `alt`/`fields` choose what Google answers
// with. None of that is a fact a caller may set on a call this service pays for
// with its own identity. The Messages query the harness does send (`beta=true`)
// is not a member of this wire either: Vertex's publisher takes what it needs
// from the path and the body, and a parameter Google's front end cannot bind is
// a request it rejects.
//
// The host rule is internal/gateway's vertexBase, respelled here because that
// function is unexported and this service may not import a rule it cannot read;
// the two must agree, and the test that a call goes to the regional host is
// what holds them together.
func vertexDestination(location, path string) string {
	base := "https://" + location + "-aiplatform.googleapis.com"
	if location == vertexGlobalLocation {
		base = "https://aiplatform.googleapis.com"
	}
	return base + path
}

// vertexStreams reports whether the call asked for a streamed answer: the
// caller's own `stream` member, which is what decides between Vertex's two
// methods.
//
// The body is the authority and the Accept header is not read, which is a
// choice worth stating: the method must match what Vertex will do with the
// body it is sent — a `stream: true` body sent to rawPredict is a request
// Vertex refuses — and the member is the one Vertex itself reads. A caller
// whose header asked for a stream and whose body did not is answered the
// body's way, which is also what the agent's own harness builds: one wire, its
// two spellings agreeing.
func vertexStreams(body []byte) bool {
	var head struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &head)
	return head.Stream
}

// vertexBody is the body one call carries to Vertex: the caller's own body with
// the two members Vertex's publisher decides for itself replaced, and every
// other member's value copied verbatim.
//
//   - model. On the Messages path it is DROPPED: Vertex takes the model from the
//     URL path, and a body naming another one would be a second answer to a
//     question the path has already answered. On the count-tokens path it is SET
//     to the spelling, because that endpoint names its model in the body — the
//     one place in this wire where the model is not in the path.
//   - anthropic_version. It is always written by this service and never by the
//     caller: the version of the publisher wire is this translation's own fact,
//     and a caller's own member is dropped rather than left to disagree with it.
//
// The translation is not a place to reformat a caller's JSON: the tools block,
// the cache markers and the thinking blocks reach Vertex exactly as the agent
// wrote them, which is why every other member is carried as the raw bytes it
// arrived as.
func vertexBody(body []byte, spelling string, keepModel bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return nil, errors.New("credsvc: the body of the call is not JSON")
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("credsvc: the body of the call is not a JSON object")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, errors.New("credsvc: a member of the call's body could not be read")
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, errors.New("credsvc: a member of the call's body has no name")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, errors.New("credsvc: a member of the call's body could not be read")
		}
		switch key {
		case "model":
			if !keepModel {
				continue
			}
			if value, err = json.Marshal(spelling); err != nil {
				return nil, err
			}
		case "anthropic_version":
			continue
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.Write(keyJSON)
		out.WriteByte(':')
		out.Write(value)
	}
	// The decoder's own closing brace, read so that a body with bytes after the
	// object — which DecodeStrict would refuse and this must not accept either —
	// is an error and not a translation.
	if _, err := dec.Token(); err != nil {
		return nil, errors.New("credsvc: the body of the call did not end with its object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("credsvc: the body of the call carries more than one object")
	}
	version, err := json.Marshal(vertexAnthropicVersion)
	if err != nil {
		return nil, err
	}
	if !first {
		out.WriteByte(',')
	}
	out.WriteString(`"anthropic_version":`)
	out.Write(version)
	out.WriteByte('}')
	return out.Bytes(), nil
}
