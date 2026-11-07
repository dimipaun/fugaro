// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
)

// This file is M7's resource server: the handler behind MCPPath, which reads
// the bearer token, validates it, resolves the identity it acts for, and only
// then looks at the JSON-RPC body (spec.md §2). Every other path this service
// answers is M5's, and the order below is the whole of what the amendment M-e
// asks of it: a caller that is not admitted reaches nothing that reads.
//
// The JSON-RPC surface is written here rather than taken from an SDK, which
// the owner's §0 mentions and the design does not: the request set this door
// answers is four methods and one notification, no protocol-level session
// survives the 2026-07-28 revision (spec.md §2), and adding a dependency for
// it is a decision no task of the plan has taken.

// The MCP revision this door serves. It is the owner's own revision
// (spec.md §2) and it is answered to every client, whatever version one asks
// for: MCP's initialize negotiates by the server naming what it serves and the
// client deciding whether it can, and a version this door echoed back without
// serving it would be a client that thinks it agreed on something.
const mcpProtocolVersion = "2026-07-28"

// What initialize answers as this server's own name and version. The version
// is the wire shape this build serves and not a release of the product: the
// coordinator is an image the installation holds, no process can read the tag
// it was built from, and the front door's surface changes with the revision
// and the tool set rather than with the CLI's version.
const (
	mcpServerName    = "fugaro-coordinator"
	mcpServerVersion = mcpProtocolVersion
)

// The JSON-RPC methods this door answers, and the five codes of JSON-RPC 2.0
// it answers with: what could not be read, what was not a request, what named
// no method this server has, what named a tool or an argument it cannot use,
// and what failed inside a tool.
const (
	methodInitialize  = "initialize"
	methodPing        = "ping"
	methodToolsList   = "tools/list"
	methodToolsCall   = "tools/call"
	codeParse         = -32700
	codeInvalid       = -32600
	codeMethodMissing = -32601
	codeInvalidParams = -32602
	codeInternal      = -32603
)

// maxRequestBytes bounds a JSON-RPC body. Every body here is one small
// request, and the same reasoning M5's form bound carries applies: this
// endpoint is public, an unauthenticated caller may send this body, and a
// request past the bound is refused rather than read. It is a constant of its
// own rather than a second use of maxFormBytes because the two bound different
// surfaces — a form and a JSON document — and a later change to one is not a
// change to the other.
const maxRequestBytes = 64 << 10

// rpcRequest is one JSON-RPC request as an MCP client sends it.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// callParams is tools/call's own `params`. The decode is deliberately not
// strict: MCP reserves fields of its own in an object a client sends (`_meta`
// on a request, for one), and a server that refused an unknown field would
// refuse a standard client for using the revision correctly.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// authorized is what one request resolved to before its body was read: the
// token's own scopes, the identity the token acts for and the role that
// identity holds.
type authorized struct {
	scopes   []string
	identity string
	role     Role
	clientID string
}

// serveMCP is the MCP endpoint: the resource server of spec.md §2.
//
// The order is the design's and not a convenience. The bearer token is read
// and validated first, because §2 says so in one sentence — "it reads the
// bearer token, validates it, and only then looks at the JSON-RPC body" — and
// because the reverse order is a public endpoint that parses what a stranger
// sent before deciding whether the stranger is anybody. The identity is
// resolved second, so that a token this installation minted for somebody on
// neither list reaches no tool and no tool name is even read. The method is
// checked third: a GET on this path is not a stream this door serves, and the
// caller is told so rather than left to read a 401 as one that means "sign in
// again".
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{
			"error":             "invalid_request",
			"error_description": "this endpoint answers one JSON-RPC request per POST and serves no server-sent event stream: MCP clients that need one use a transport that offers it",
		})
		return
	}
	body, err := readRequest(w, r)
	if err != nil {
		s.rpcError(w, http.StatusBadRequest, nil, codeParse, "the request body is not one this endpoint can read: it is past "+fmt.Sprint(maxRequestBytes)+" bytes, or it could not be read at all")
		return
	}
	s.dispatch(w, r, auth, body)
}

// authorize reads the bearer token and answers what the request resolved to,
// writing the refusal itself when it does not resolve to a caller this
// installation admits.
//
// **Issuer, audience, expiry, revocation and scopes** are the five facts the
// design's interface names, and how they are read follows from the token being
// opaque. The value here is one this installation minted, so the record it
// resolves to in the store *is* the issuer's own token and its audience is
// this resource: neither can be read off the value, and neither can be wrong
// in a record the store holds — which is why §0's "the coordinator issues its
// own tokens instead of passing Firebase tokens through" is a check this door
// gets by construction rather than one it repeats. What the door reads from
// the record is the rest: the kind (a refresh token is not a bearer for
// anything, and it must not be the value that extends its own family), the
// expiry against this server's clock, revocation (one delete of the family,
// after which the record does not resolve at all — the store's LookupByHash),
// and the scopes (CheckScopes). The store answers none of those five, which is
// its own doc's rule rather than this file's: "the caller decides on the
// expiry, the kind and the scopes".
//
// No refusal echoes the token, and none says whether a value was ever minted:
// a caller that presented something this installation does not hold learns
// that it is not usable here and nothing else, and a caller holding a real
// token is told which of the five refused it, because that is a fact about its
// own token and the only way it can act on it.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) (authorized, bool) {
	value, ok := bearerToken(r)
	if !ok {
		s.refuseBearer(w, http.StatusUnauthorized, "invalid_token", "", "", "the MCP endpoint needs one bearer token in an Authorization header: read the resource metadata document named in the WWW-Authenticate header, register a client and sign in")
		return authorized{}, false
	}
	rec, err := s.store.LookupByHash(r.Context(), TokenHash(value))
	if err != nil {
		s.refuseBearer(w, http.StatusUnauthorized, "invalid_token", "", "", "the bearer token is not one this endpoint can use: it was not issued here, or the family it belongs to was revoked; the resource metadata document named in the WWW-Authenticate header says how to get one")
		return authorized{}, false
	}
	if err := usable(rec, s.now()); err != nil {
		s.refuseBearer(w, http.StatusUnauthorized, "invalid_token", "", rec.Token.Identity, err.Error()+": the resource metadata document named in the WWW-Authenticate header says how to get another one")
		return authorized{}, false
	}
	role, err := s.resolveIdentity(r.Context(), rec.Token.Identity)
	if err != nil {
		if errors.Is(err, ErrIdentityOffTheLists) {
			s.refuseBearer(w, http.StatusForbidden, "insufficient_scope", "", rec.Token.Identity, "this identity holds no role at this installation: the front door serves the people on its launcher and operator lists, and fugaro user is the command that changes them")
			return authorized{}, false
		}
		s.refuseBearer(w, http.StatusInternalServerError, "server_error", "", rec.Token.Identity, "the front door could not read this installation's member lists, so it admits nobody: this is a fault of the installation and not of this token")
		return authorized{}, false
	}
	return authorized{
		scopes:   rec.Token.Limits.Scopes,
		identity: rec.Token.Identity,
		role:     role,
		clientID: rec.Token.ClientID,
	}, true
}

// usable is the four things a record has to be for this token to be usable at
// this door: an access token, with an expiry that is set and in the future,
// and scopes this build can read.
func usable(rec TokenRecord, now time.Time) error {
	switch {
	case rec.Kind != KindAccess:
		return fmt.Errorf("this token is a %s token and this endpoint takes an access token", rec.Kind)
	case rec.Token.ExpiresAt.IsZero():
		return errors.New("this token carries no expiry, which is a shape this door refuses rather than reads as never expiring")
	case !now.Before(rec.Token.ExpiresAt):
		return errors.New("this token has expired")
	}
	return CheckScopes(rec.Token.Limits.Scopes)
}

// bearerToken reads the one bearer credential an Authorization header may
// carry.
//
// The scheme is matched case-insensitively, which RFC 7235 §2.1 requires of
// every scheme name, and nothing else is: a header carrying two credentials, a
// second Authorization header, another scheme, or no value at all is a
// refusal rather than a guess at which of the values was meant. Nothing here
// compares the credential to anything — the value is opaque and its only
// reader is the store's hash — so this function is the whole of what the door
// knows about the header, and a token that arrived any other way (a query
// parameter, a body field) is not read at all.
func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, value, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t") {
		return "", false
	}
	return value, true
}

// readRequest reads the request body under the endpoint's own bound.
func readRequest(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	return io.ReadAll(r.Body)
}

// dispatch answers one JSON-RPC request.
//
// A notification — a request with no id, which is what MCP's own
// `notifications/initialized` is — is answered with no body at all, which is
// what JSON-RPC 2.0 §4.1 asks of a server that got one. Batch requests are
// refused rather than half-answered: this endpoint takes one request per POST,
// MCP's revision has no use for a batch, and a server that read one would have
// to decide what a partial failure of four calls means.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, auth authorized, body []byte) {
	req, code, err := decodeRequest(body)
	if err != nil {
		s.rpcError(w, http.StatusBadRequest, nil, code, err.Error())
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.rpcError(w, http.StatusBadRequest, req.ID, codeInvalid, `the request is not JSON-RPC 2.0: its "jsonrpc" field is not "2.0", or it names no method`)
		return
	}
	if notification(req.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case methodInitialize:
		s.rpcAnswer(w, req.ID, initializeResult())
	case methodPing:
		// MCP's ping is a liveness check with no work behind it, and it is
		// answered after the same token check as everything else: an
		// unauthenticated caller must not be able to tell a running front door
		// from a stopped one.
		s.rpcAnswer(w, req.ID, map[string]any{})
	case methodToolsList:
		s.rpcAnswer(w, req.ID, map[string]any{"tools": s.tools.List()})
	case methodToolsCall:
		s.callTool(w, r, auth, req)
	default:
		s.rpcError(w, http.StatusOK, req.ID, codeMethodMissing, fmt.Sprintf("this endpoint answers %s, %s, %s and %s; it has no method %q", methodInitialize, methodPing, methodToolsList, methodToolsCall, req.Method))
	}
}

// callTool answers tools/call.
//
// The scope check is here as well as inside Registry.Call, and the two are not
// a duplication to remove: this one answers the caller in the shape a resource
// server answers a missing scope (RFC 6750 §3.1's 403 with the scope named in
// the challenge), and the registry's is the invariant that no handler is ever
// reached without its scope. A tool the catalogue does not hold is not a scope
// failure at all — no scope is mapped to it — so a caller asking for one
// learns the name is not served, which is exactly what finding M-e's "a tool
// with no mapping is refused" looks like from outside.
func (s *Server) callTool(w http.ResponseWriter, r *http.Request, auth authorized, req rpcRequest) {
	var params callParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.rpcError(w, http.StatusOK, req.ID, codeInvalidParams, "tools/call takes an object with a name and an arguments object")
			return
		}
	}
	if params.Name == "" {
		s.rpcError(w, http.StatusOK, req.ID, codeInvalidParams, "tools/call names no tool")
		return
	}
	scope, mapped := ToolScope(params.Name)
	if !mapped {
		s.rpcError(w, http.StatusOK, req.ID, codeInvalidParams, fmt.Sprintf("no tool named %q is served here: tools/list names the tools this endpoint serves", params.Name))
		return
	}
	if !slices.Contains(auth.scopes, scope) {
		s.refuseBearer(w, http.StatusForbidden, "insufficient_scope", scope, auth.identity, fmt.Sprintf("the tool %q maps to the scope %q, which this token does not hold", params.Name, scope))
		return
	}
	if !argumentsAreAnObject(params.Arguments) {
		s.rpcError(w, http.StatusOK, req.ID, codeInvalidParams, "tools/call takes the arguments as one JSON object")
		return
	}
	out, err := s.tools.Call(r.Context(), Call{
		Tool:      params.Name,
		Arguments: params.Arguments,
		Scopes:    auth.scopes,
		Identity:  auth.identity,
		Role:      auth.role,
		ClientID:  auth.clientID,
	})
	if err != nil {
		if errors.Is(err, ErrNoSuchTool) {
			s.rpcError(w, http.StatusOK, req.ID, codeInvalidParams, fmt.Sprintf("no tool named %q is served here: the name is one this endpoint knows and does not serve yet", params.Name))
			return
		}
		// A tool's own refusal is the tool's answer and not the protocol's:
		// MCP puts an execution error inside a successful result with
		// `isError`, because a client has to show what the tool said rather
		// than treat the call as one the server could not read.
		s.rpcAnswer(w, req.ID, toolResult(err.Error(), true))
		return
	}
	text, err := answerText(out)
	if err != nil {
		s.rpcError(w, http.StatusOK, req.ID, codeInternal, "the tool's answer could not be encoded as JSON")
		return
	}
	s.rpcAnswer(w, req.ID, toolResult(text, false))
}

// decodeRequest reads one JSON-RPC request, and refuses everything that is not
// one: an empty body, a batch, a body that is not JSON, and an id that is
// neither a string nor a number. None of the refusals repeats what was sent,
// which is why the decoder's own error is not quoted: this is the one answer a
// stranger can reach with any body at all.
//
// The code comes back with the refusal because the two failures are not the
// same one: a body that is not JSON is JSON-RPC 2.0's parse error, and a batch
// or an id of another shape is a document this endpoint read and will not
// serve, which is the invalid request. Collapsing them would tell a client to
// fix its JSON when its JSON was fine.
func decodeRequest(body []byte) (rpcRequest, int, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return rpcRequest{}, codeParse, errors.New("the request body is empty: this endpoint takes one JSON-RPC request per POST")
	}
	if trimmed[0] == '[' {
		return rpcRequest{}, codeInvalid, errors.New("this endpoint answers one JSON-RPC request per POST and does not read a batch")
	}
	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return rpcRequest{}, codeParse, errors.New("the request body is not a JSON-RPC request: it is not one JSON object")
	}
	if !validID(req.ID) {
		return rpcRequest{}, codeInvalid, errors.New(`the request's id is neither a string nor a number: JSON-RPC 2.0 answers an id of its own shape, and echoing anything else would put a JSON-RPC error where a client does not look`)
	}
	return req, 0, nil
}

// notification reports whether a request is one JSON-RPC does not answer.
func notification(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// validID reports whether an id is a string or a number, which are the two
// shapes JSON-RPC 2.0 allows.
func validID(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return true
	}
	if trimmed[0] == '"' {
		var s string
		return json.Unmarshal(trimmed, &s) == nil
	}
	var n json.Number
	return json.Unmarshal(trimmed, &n) == nil
}

// argumentsAreAnObject reports whether tools/call's arguments are the object
// MCP says they are. An absent one is an object with no members: a tool with
// no arguments is called with none.
func argumentsAreAnObject(args json.RawMessage) bool {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return true
	}
	if trimmed[0] != '{' {
		return false
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal(trimmed, &obj) == nil
}

// initializeResult is what this door tells a client it is: the revision it
// serves, the one capability it has, and its own name.
func initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": mcpServerName, "version": mcpServerVersion},
	}
}

// toolResult is one tool's answer in MCP's own shape: a content list whose one
// member is the answer as text, and whether that text is a refusal.
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// answerText renders a handler's answer as the text a client reads: a string
// is passed through as the string it is, and anything else is its JSON. §5's
// "every answer is plain text or JSON" is the whole rule, and a handler that
// returned a string has already said which of the two it is.
func answerText(out any) (string, error) {
	if text, ok := out.(string); ok {
		return text, nil
	}
	if out == nil {
		return "null", nil
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// rpcAnswer writes a JSON-RPC result. The status is 200 whatever the method's
// own outcome: JSON-RPC carries its errors in the body, and the HTTP status is
// reserved here for what happened before a request could be read.
func (s *Server) rpcAnswer(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSONStatus(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// rpcError writes a JSON-RPC error. The code is JSON-RPC's own vocabulary and
// the message names no value the caller sent, except where the message is
// about the caller's own tool or scope — which is what makes it actionable.
func (s *Server) rpcError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	writeJSONStatus(w, status, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

// refuseBearer answers a request whose bearer credential did not resolve to a
// caller this installation admits: the RFC 6750 §3 answer, with the
// WWW-Authenticate challenge that carries the resource metadata URL — the
// header M5 ships and M7 keeps, because the header is what the flow starts
// from and not something a token check adds.
//
// **The refusal is one line on the server's logger and is never written to any
// store** (D920, finding M-e: "an unauthenticated refusal is logged and never
// written to the database"). Both halves are the reason this door can be public:
// a stranger with a loop grows nothing here, and the operator of the installation
// can still see the loop. The line carries the outcome, the reason and — when
// there is one, which is whenever the token resolved to a record — the identity
// the token acts for, and it carries no value: this function is not handed the
// token at all, and nothing it is handed is a credential (spec.md §9's "not in a
// log line").
//
// The five authenticated paths of the OAuth flow are untouched by this
// function: it is the refusal of the MCP endpoint alone.
func (s *Server) refuseBearer(w http.ResponseWriter, status int, errType, scope, identity, description string) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	line := []any{"error", errType, "status", status, "reason", description}
	if scope != "" {
		line = append(line, "scope", scope)
	}
	if identity != "" {
		line = append(line, "identity", identity)
	}
	logger.Warn("the MCP endpoint refused a request", line...)

	challenge := []string{`Bearer error="` + errType + `"`}
	if scope != "" {
		challenge = append(challenge, `scope="`+scope+`"`)
	}
	challenge = append(challenge, `resource_metadata="`+s.cfg.Issuer+ProtectedResourcePath+`"`)
	w.Header().Set("WWW-Authenticate", strings.Join(challenge, ", "))
	writeJSONStatus(w, status, map[string]string{"error": errType, "error_description": description})
}
