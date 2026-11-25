package credsvc

// The stub of the version-2 workflow-secrets route (C1): the answer both sides
// of the image-secrets plan test against while the service (IS1) and the
// runner's fetch (IS2) are written. I1 deletes this file, wiring the route
// itself; until then it is what makes the contract runnable — IS2's runner
// tests stand a real HTTP handler up and send it the version-2 call, so the
// shape, the echo and the refusal codes are held to one implementation rather
// than to two readers agreeing with a comment.
//
// **It decides nothing.** It reads no token, no record, no label and no cloud:
// it serves whatever its own maps hold, to whoever calls it, and it is not
// wired into any server (server.go's routes do not name it). That is the stub
// rule this tree follows (sealkey_stub.go): an answer that can never serve a
// caller by accident, so a side written against it is testing the contract and
// not the policy. Which repository, which workflow and which allow-list decide
// a real call is IS1's route, and no test of this file says otherwise.

import (
	"encoding/json"
	"io"
	"net/http"
)

// WorkflowSecretsStub answers workflow-secrets calls from memory.
//
// Values is version 2's map: a logical secret name (the same spelling
// config.Secret.Name uses) to the value the stub answers with. Envs is version
// 1's: the environment variable to its value, so a test can pin the runner's
// fallback — a version-2 name the map does not hold is refused
// workflow_secret_not_allowed, and the same variable asked for as version 1 is
// then answered from Envs.
//
// A nil map holds nothing: a version-2 name absent from Values is refused
// exactly as a real allow-list refuses an unlisted repository, and a version-1
// variable absent from Envs exactly as a record that holds no such variable.
type WorkflowSecretsStub struct {
	Values map[string]string
	Envs   map[string]string
}

func (s WorkflowSecretsStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWorkflowSecretsRequestBytes+1))
	if err != nil || len(body) > maxWorkflowSecretsRequestBytes {
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused, "the body could not be read")
		return
	}
	var req WorkflowSecretsRequest
	if err := decodeStrict(body, &req); err != nil {
		// Strictly, like the route: a field no version of this contract carries
		// is a body the stub does not read, so a test that drifts from the
		// contract fails here rather than being answered generously.
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused, "the body is not one JSON object of this contract's fields")
		return
	}
	if err := req.Validate(); err != nil {
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused, err.Error())
		return
	}
	if req.VersionOf() == WorkflowSecretsVersion2 {
		value, ok := s.Values[req.Name]
		if !ok {
			s.refuse(w, StatusUnverified, ErrorWorkflowSecretNotAllowed, "the stub holds no value under the name this call asked for")
			return
		}
		s.answer(w, WorkflowSecretsResponse{Name: req.Name, Value: value})
		return
	}
	value, ok := s.Envs[req.Env]
	if !ok {
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused, "the stub holds no value under the variable this call asked for")
		return
	}
	s.answer(w, WorkflowSecretsResponse{Env: req.Env, Value: value})
}

// answer writes one value the way the route writes it: no-store, so a stub
// answer cannot be cached either, and no header carrying anything of the value.
func (s WorkflowSecretsStub) answer(w http.ResponseWriter, resp WorkflowSecretsResponse) {
	body, err := json.Marshal(resp)
	if err != nil {
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, "the stub could not build its answer")
		return
	}
	writeReadAnswer(w, body)
}

// refuse writes one typed refusal. The message is this file's own and never the
// call's content: a stub that echoed what it was asked for would teach a test
// to expect an echo the real route does not make (its refusals name the field
// and never its content, G18).
func (s WorkflowSecretsStub) refuse(w http.ResponseWriter, status int, code, message string) {
	WriteWireError(w, status, WireError{Error: code, Message: message})
}
