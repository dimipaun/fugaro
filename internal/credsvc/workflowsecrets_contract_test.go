package credsvc

// The version-2 workflow-secrets contract (C1, D3324, D3328, D3333): the
// request a run sends by NAME, the answer that echoes it, the refusal codes
// version 2 answers with, and the stub both sides of this plan test against
// until the service and the runner exist.
//
// It is a contract test and not a route test: nothing here builds a server,
// reads a record or verifies an identity. What it holds is what the two sides
// of the wire may rely on each other for -- one shape per version, so a version
// 1 call is byte for byte what it was and never grows a name, and a version 2
// call never carries an environment variable -- plus the codes a refusal of
// version 2 must be readable as, which is what lets the runner print the fix
// for the case it actually met (R-request's whole point).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The two names these tests use. They are NAMES a workflow secret is recorded
// under (config.SecretNameRE), never values.
const (
	testSecretName  = "deploy-token"
	testSecretEnv   = "DEPLOY_TOKEN"
	testSecretOther = "review-token"
)

func TestWorkflowSecretsRequestV2Shape(t *testing.T) {
	t.Run("version 1 is the bytes it has always been", func(t *testing.T) {
		body, err := json.Marshal(WorkflowSecretsRequest{Env: testSecretEnv})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"env":"DEPLOY_TOKEN"}`; got != want {
			t.Errorf("a version 1 request marshals to %s, want %s: the field keeps its meaning and the call gains none", got, want)
		}
		if v := (WorkflowSecretsRequest{Env: testSecretEnv}).VersionOf(); v != WorkflowSecretsVersion1 {
			t.Errorf("a request that declares no version is version %d, want %d", v, WorkflowSecretsVersion1)
		}
	})

	t.Run("version 2 names the secret and declares itself", func(t *testing.T) {
		body, err := json.Marshal(WorkflowSecretsRequest{Name: testSecretName, Version: WorkflowSecretsVersion2})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"name":"deploy-token","version":2}`; got != want {
			t.Errorf("a version 2 request marshals to %s, want %s", got, want)
		}
		if v := (WorkflowSecretsRequest{Name: testSecretName, Version: WorkflowSecretsVersion2}).VersionOf(); v != WorkflowSecretsVersion2 {
			t.Errorf("the declared version is read as %d, want %d", v, WorkflowSecretsVersion2)
		}
	})

	t.Run("one shape per version", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			req  WorkflowSecretsRequest
			ok   bool
		}{
			{"a version 1 call by variable", WorkflowSecretsRequest{Env: testSecretEnv}, true},
			{"a version 2 call by name", WorkflowSecretsRequest{Name: testSecretName, Version: 2}, true},
			// The two shapes are exclusive: a call that carries both is one no
			// version of this contract wrote, and reading it as either would be
			// answering a caller that asked two different things.
			{"a version 1 call that also names a secret", WorkflowSecretsRequest{Env: testSecretEnv, Name: testSecretName}, false},
			{"a version 2 call that also names a variable", WorkflowSecretsRequest{Env: testSecretEnv, Name: testSecretName, Version: 2}, false},
			{"a version 2 call with no name", WorkflowSecretsRequest{Version: 2}, false},
			{"a version 2 call whose name is not a secret name", WorkflowSecretsRequest{Name: "Deploy_Token", Version: 2}, false},
			{"a version 1 call with no variable", WorkflowSecretsRequest{}, false},
			{"a version this contract does not carry", WorkflowSecretsRequest{Name: testSecretName, Version: 3}, false},
			// Version 1 is the ABSENCE of the field, which is what every caller
			// of it writes: a body that spells 1 is a shape no side of this
			// contract produces, and the strict reader refuses it rather than
			// guessing which of the two it meant.
			{"a version 1 call that declares 1", WorkflowSecretsRequest{Env: testSecretEnv, Version: 1}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.req.Validate()
				if tc.ok && err != nil {
					t.Fatalf("the contract refused it: %v", err)
				}
				if !tc.ok && err == nil {
					t.Fatal("the contract read it as one of its own shapes")
				}
				if err != nil && strings.Contains(err.Error(), testSecretName) {
					t.Errorf("the refusal repeats what it refused: %v", err)
				}
			})
		}
	})

	t.Run("the answer echoes the name", func(t *testing.T) {
		value := fakeCred(t, "v2-answer")
		body, err := json.Marshal(WorkflowSecretsResponse{Name: testSecretName, Value: value})
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(map[string]string{"name": testSecretName, "value": value})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(body); got != string(want) {
			t.Errorf("a version 2 answer marshals to %s, want %s", got, want)
		}
		v1, err := json.Marshal(WorkflowSecretsResponse{Env: testSecretEnv, Value: value})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(v1), `{"env":"DEPLOY_TOKEN","value":"`+value+`"}`; got != want {
			t.Errorf("a version 1 answer marshals to %s, want %s", got, want)
		}
	})

	t.Run("the codes version 2 answers with", func(t *testing.T) {
		for _, tc := range []struct {
			code   string
			status int
		}{
			// The verified caller may not fetch this secret: asking again cannot
			// clear it, and the fix is the owner's `fugaro secrets allow`.
			{ErrorWorkflowSecretNotAllowed, StatusUnverified},
			// The name's container is not one this service serves a workflow
			// secret from (an unmarked `fugaro_p_` container, F18).
			{ErrorWorkflowSecretUnmarked, StatusUnavailable},
			// Version 2 is not served while a repository's own copies are in
			// force (G9, F24).
			{ErrorWorkflowSecretsOff, StatusUnavailable},
		} {
			t.Run(tc.code, func(t *testing.T) {
				rec := httptest.NewRecorder()
				WriteWireError(rec, tc.status, WireError{Error: tc.code, Message: "the fix"})
				if rec.Code != tc.status {
					t.Fatalf("the refusal answers %d, want %d", rec.Code, tc.status)
				}
				e, err := ReadWireError(rec.Code, rec.Body.Bytes())
				if err != nil {
					t.Fatalf("the refusal this contract writes is not one it can read: %v", err)
				}
				if e.Error != tc.code {
					t.Errorf("the code reads back as %q, want %q", e.Error, tc.code)
				}
			})
		}
	})

	t.Run("the stub answers version 2 from its map", func(t *testing.T) {
		value := fakeCred(t, "stub-value")
		other := fakeCred(t, "stub-other")
		srv := httptest.NewServer(WorkflowSecretsStub{
			Values: map[string]string{testSecretName: value},
			Envs:   map[string]string{testSecretEnv: other},
		})
		defer srv.Close()

		post := func(t *testing.T, body string) (int, []byte) {
			t.Helper()
			resp, err := http.Post(srv.URL+WorkflowSecretsPath, contentTypeJSON, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			out, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			return resp.StatusCode, out
		}

		status, body := post(t, `{"name":"deploy-token","version":2}`)
		if status != http.StatusOK {
			t.Fatalf("the stub answered %d for a name it holds: %s", status, body)
		}
		var ans WorkflowSecretsResponse
		if err := json.Unmarshal(body, &ans); err != nil {
			t.Fatalf("the stub's answer is not this contract's: %v", err)
		}
		if ans.Name != testSecretName || ans.Value != value {
			t.Errorf("the stub answered name %q, value %q", ans.Name, ans.Value)
		}
		if ans.Env != "" {
			t.Errorf("a version 2 answer carries the variable %q, which no version 2 call named", ans.Env)
		}

		status, body = post(t, `{"name":"review-token","version":2}`)
		if status != StatusUnverified {
			t.Fatalf("the stub answered %d for a name it does not hold, want %d: %s", status, StatusUnverified, body)
		}
		e, err := ReadWireError(status, body)
		if err != nil {
			t.Fatalf("the stub's refusal is not this contract's: %v", err)
		}
		if e.Error != ErrorWorkflowSecretNotAllowed {
			t.Errorf("the stub refused with %q, want %q: a caller must be able to tell an unlisted name from an unmarked container", e.Error, ErrorWorkflowSecretNotAllowed)
		}
		if strings.Contains(string(body), testSecretOther) {
			t.Error("the stub's refusal echoes the name it was asked for")
		}

		status, body = post(t, `{"env":"DEPLOY_TOKEN"}`)
		if status != http.StatusOK {
			t.Fatalf("the stub answered %d for a version 1 call it holds: %s", status, body)
		}
		if err := json.Unmarshal(body, &ans); err != nil {
			t.Fatalf("the stub's version 1 answer is not this contract's: %v", err)
		}
		if ans.Env != testSecretEnv || ans.Value != other {
			t.Errorf("the stub answered env %q, value %q", ans.Env, ans.Value)
		}

		status, body = post(t, `{"name":"deploy-token"}`)
		if status != StatusRequestRefused {
			t.Fatalf("the stub answered %d for a body with no version, want %d: %s", status, StatusRequestRefused, body)
		}
	})
}
