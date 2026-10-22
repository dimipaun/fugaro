package credsvc

// The request check (C1.11 follow-up): a call the gateway would refuse is
// refused here, before a credential is read.
//
// The service serves a run's calls with a credential the run does not hold, and
// an agent inside the job holds the job's own ID token, so it can call the
// service directly — past the in-runner gateway, past its pins and past the
// binding ledger. C1.11 forwarded any body it was handed; this file is the
// other half: the rules are the gateway's own (internal/gateway's exported
// check, which the gateway's request path applies too) and they are applied
// before the bundle is loaded, so a refused request never reaches a credential.
//
// Everything here is hermetic (Convention 9) and nothing in it is, or looks
// like, a credential (Convention 2).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// testPolicyDocAllowed is testPolicyDoc with the project's own model
// allow-list written out: `budget.allowed_models`, the one model rule this
// service can hold a call to (a stage's own pins are the repository's config
// and never reach it).
func testPolicyDocAllowed(models ...string) string {
	list := make([]string, 0, len(models))
	for _, m := range models {
		list = append(list, `"`+m+`"`)
	}
	return `{
		"version": 3,
		"budget": {"mode": "enforce", "per_run_usd": 5, "allowed_models": [` + strings.Join(list, ", ") + `]},
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "auto"}
	}`
}

// allowedPolicy is the fixture's installation with the project's allow-list
// published, and nothing else changed.
func allowedPolicy(t *testing.T, models ...string) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, cfg *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAllowed(models...))
	})
}

// wantPin is the pin the service must apply: the models the project's policy
// allows, named in the refusal as the list the service holds the call to. It is
// written out here rather than read from the service, because a test that asked
// the service for its own pin would accept whatever the service decided.
func wantPin(models ...string) gateway.RequestPin {
	return gateway.RequestPin{Models: models, Where: "the project's model allow-list"}
}

// TestServiceRefusesARequestTheGatewayWouldRefuse: the service answers the
// gateway's own refusal — the same sentence, produced by the same function —
// for a model the project does not allow, a server tool, a shape the budget
// cannot bound, and a max_tokens no model of that size can produce.
//
// The sentence is not spelled out here: the test asks the exported check for
// the refusal of the same body, so a service that kept its own copy of the
// rules would answer something this test does not accept. (That the *rules*
// are the gateway's is the gateway's own tests' business; what is asserted
// here is that this service applies them, with the project's allow-list, and
// refuses before it reads anything.)
//
// Mutation (run, restore): drop the RequestRefusal call from ServeHTTP's
// Messages path, and every subtest is red — the call is forwarded instead.
func TestServiceRefusesARequestTheGatewayWouldRefuse(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "a model the project does not allow",
			body: `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name: "a server tool",
			body: `{"model":"` + testModel + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` +
				`"tools":[{"type":"web_search_20260209","name":"web_search"}]}`,
		},
		{
			name: "a shape the budget cannot bound",
			body: `{"model":"` + testModel + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` +
				`"mcp_servers":[{"type":"url","url":"https://mcp.example.invalid","name":"mcp"}]}`,
		},
		{
			name: "a max_tokens above the model's maximum",
			body: `{"model":"` + testModel + `","max_tokens":100000000,"messages":[{"role":"user","content":"hi"}]}`,
		},
	}
	s := allowedPolicy(t, testModel)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := gateway.RequestRefusal([]byte(tc.body), "", wantPin(testModel), pricing.Embedded(), nil)
			if err != nil {
				t.Fatalf("the gateway cannot read the test's own body: %v", err)
			}
			if want == "" {
				t.Fatal("the gateway accepts the test's own body: this case tests nothing")
			}
			before := s.up.Count()
			resp, body := s.call(t, tc.body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorRequestRefused {
				t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
			}
			if !strings.Contains(e.Message, want) {
				t.Errorf("the refusal is %q, want the gateway's own sentence %q in it", e.Message, want)
			}
			if !strings.Contains(e.Message, "before any credential is read") {
				t.Errorf("the refusal is %q, and it must say that it was refused before any credential was read", e.Message)
			}
			if n := s.up.Count(); n != before {
				t.Errorf("the upstream saw %d requests, want the %d it had: a refused call is not forwarded", n, before)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want 0: a refused request must not touch a credential", n)
			}
		})
	}
}

// TestServiceNamesTheProjectsListInTheRefusal: the pin the service applies is
// the project's allow-list, and the refusal says so. The sentence is asserted
// here rather than only compared with the exported check's, because the check
// takes the pin as an argument: this is the one place that says which list the
// service passes it.
func TestServiceNamesTheProjectsListInTheRefusal(t *testing.T) {
	s := allowedPolicy(t, testModel)
	resp, body := s.call(t, `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	const want = "model claude-opus-5-5 is not pinned for the project's model allow-list"
	if !strings.Contains(e.Message, want) {
		t.Errorf("the refusal is %q, want %q in it", e.Message, want)
	}
}

// TestServiceForwardsARequestTheGatewayAccepts: the check is a refusal and not
// a wall — a call naming a model the project allows is forwarded, exactly as
// it was before the check existed.
func TestServiceForwardsARequestTheGatewayAccepts(t *testing.T) {
	s := allowedPolicy(t, testModel)
	resp, body := s.call(t, callBody("allowed"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	if n := s.src.readCount(); n != 1 {
		t.Errorf("the bundle was read %d times, want 1: a forwarded call reads the credential it needs", n)
	}
}

// TestServiceAppliesTheCheckOnlyToMessages: the count_tokens body is not a
// Messages body — it carries no max_tokens, and the gateway does not hold it
// to these rules either (its own forwardFree) — so a check applied to it would
// refuse calls the gateway forwards. This is the regression guard for where
// the check is called.
func TestServiceAppliesTheCheckOnlyToMessages(t *testing.T) {
	s := allowedPolicy(t, testModel)
	resp, body := s.callPath(t, CountTokensPath,
		`{"model":"`+testModel+`","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the token count is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1", n)
	}
}

// unreadBundleSource is a bundle source a refused call must never reach. It
// counts its reads and answers an error, so a service that loaded the bundle
// before the request check would answer the bundle's refusal (a 503) instead of
// the request's — and the count this test asserts is zero would not be.
type unreadBundleSource struct{ reads int32 }

func (s *unreadBundleSource) Latest(context.Context) ([]byte, error) {
	atomic.AddInt32(&s.reads, 1)
	return nil, errors.New("the bundle must not be read for a request the gateway refuses")
}

// TestServiceRefusesBeforeTheBundleLoads: the refusal is answered before the
// one bundle that holds every credential is read, so a refused request cannot
// touch a credential at all.
//
// The loader fails the test if it is called — it counts, and the count is
// asserted — and its error names the violation, so a service that read it would
// answer a 503 about a bundle instead of the 400 about the request.
func TestServiceRefusesBeforeTheBundleLoads(t *testing.T) {
	src := &unreadBundleSource{}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAllowed(testModel))
		cfg.Bundles = &BundleLoader{Source: src, Now: func() time.Time { return s.fixture.now }}
	})
	resp, body := s.call(t, `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if n := atomic.LoadInt32(&src.reads); n != 0 {
		t.Errorf("the bundle was read %d times for a refused request, want 0: the refusal must come first", n)
	}
}

// TestServiceRefusesAMalformedBody: a body the gateway cannot read at all is
// the gateway's own 400 (its parse error), refused here as the same kind of
// refusal rather than forwarded — and, like the others, before the bundle.
func TestServiceRefusesAMalformedBody(t *testing.T) {
	src := &unreadBundleSource{}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAllowed(testModel))
		cfg.Bundles = &BundleLoader{Source: src, Now: func() time.Time { return s.fixture.now }}
	})
	resp, body := s.call(t, `{"model":"`+testModel+`","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, "max_tokens is missing or not a number") {
		t.Errorf("the refusal is %q, want the gateway's own reading of the body in it", e.Message)
	}
	if n := atomic.LoadInt32(&src.reads); n != 0 {
		t.Errorf("the bundle was read %d times for a malformed request, want 0", n)
	}
}

// TestServiceWithNoAllowListPinsNoModel: a project that publishes no
// allow-list sets no model rule — the ceiling is then the job's own
// configuration (H1), which this service cannot read — so a model the
// published policy does not name is forwarded. It is the documented limit of
// this check, and the shape rules apply either way.
func TestServiceWithNoAllowListPinsNoModel(t *testing.T) {
	s := newSvc(t)
	p, found, err := s.policy.Shared(context.Background())
	if err != nil || !found {
		t.Fatalf("the fixture's own policy could not be read: %v, %v", found, err)
	}
	if p.Budget.AllowedModels != nil {
		t.Fatalf("the fixture's policy carries an allow-list (%v): this test needs one that does not", p.Budget.AllowedModels)
	}
	resp, body := s.call(t, `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
	}
}

// TestServiceWithoutAnAllowListStillRefusesAShape: no allow-list means no
// *model* rule, and nothing more — the server-tool refusal, the shape refusal
// and the model's own max_tokens bound apply either way (request.go). That is
// the case that matters most, because a project that publishes no allow-list is
// the default, and for it this check is the only control between an agent
// holding the job's own ID token and a credential.
//
// The sentences are the exported check's own, asked for the same bodies with an
// empty pin, so a service that skipped the rest of the check along with the
// model rule would answer something this test does not accept. The upstream and
// the bundle are asserted untouched for the same reason as in the pinned cases.
//
// Mutation (run, restore): make requestRefusal return "" when the pin names no
// model — the hole this file exists to close, for the projects where the check
// is the only control — and every subtest here is red while the rest of this
// file stays green.
func TestServiceWithoutAnAllowListStillRefusesAShape(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "a server tool",
			body: `{"model":"` + testModel + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` +
				`"tools":[{"type":"web_search_20260209","name":"web_search"}]}`,
		},
		{
			name: "a shape the budget cannot bound",
			body: `{"model":"` + testModel + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` +
				`"mcp_servers":[{"type":"url","url":"https://mcp.example.invalid","name":"mcp"}]}`,
		},
	}
	s := newSvc(t)
	p, found, err := s.policy.Shared(context.Background())
	if err != nil || !found {
		t.Fatalf("the fixture's own policy could not be read: %v, %v", found, err)
	}
	if p.Budget.AllowedModels != nil {
		t.Fatalf("the fixture's policy carries an allow-list (%v): this test needs one that does not", p.Budget.AllowedModels)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := gateway.RequestRefusal([]byte(tc.body), "", gateway.RequestPin{}, pricing.Embedded(), nil)
			if err != nil {
				t.Fatalf("the gateway cannot read the test's own body: %v", err)
			}
			if want == "" {
				t.Fatal("the gateway accepts the test's own body: this case tests nothing")
			}
			before := s.up.Count()
			resp, body := s.call(t, tc.body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorRequestRefused {
				t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
			}
			if !strings.Contains(e.Message, want) {
				t.Errorf("the refusal is %q, want the gateway's own sentence %q in it", e.Message, want)
			}
			if n := s.up.Count(); n != before {
				t.Errorf("the upstream saw %d requests, want the %d it had: a refused call is not forwarded", n, before)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want 0: a refused request must not touch a credential", n)
			}
		})
	}
}
