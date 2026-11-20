package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// WU22's tests. Every fake and helper here carries the task's id (wu22…), so
// the level-2 tasks that land in this package later merge without a name
// clash (plan §1).

// wu22FakeSecrets is a member project's Secret Manager as the probe reaches
// it, in memory and with no network. It is the probe's whole read surface,
// and the surface is read-only by construction (probeSecrets has no write to
// call): `workspace project status` writes nothing on any path, this one
// included (D2809), and the fake cannot be asked to break that because there
// is no method that would.
//
// A read of a container the test did not store fails the test rather than
// answering "no value": the probe reading anything the test did not expect —
// a personal credential above all — is the finding, not a state to report.
type wu22FakeSecrets struct {
	t      *testing.T
	creds  []probeCredential
	values map[string]string
	refuse error

	listed int
	read   []string
}

func (f *wu22FakeSecrets) Credentials(context.Context) ([]probeCredential, error) {
	f.listed++
	if f.refuse != nil {
		return nil, f.refuse
	}
	return append([]probeCredential(nil), f.creds...), nil
}

func (f *wu22FakeSecrets) Value(_ context.Context, id string) (string, error) {
	f.read = append(f.read, id)
	v, ok := f.values[id]
	if !ok {
		f.t.Errorf("the probe read the container %s, which this test stored no value in", id)
		return "", errors.New("no value is stored")
	}
	return v, nil
}

// wu22Open is the probe's open seam over one fake project.
func wu22Open(store probeSecrets) func(context.Context, string) (probeSecrets, error) {
	return func(context.Context, string) (probeSecrets, error) { return store, nil }
}

// wu22FakeProvider is the one authenticated call's transport: it records
// every request and answers the status the test set. It never reaches a
// network, and it records the headers as they were sent, which is how a test
// sees the credential that travelled without the probe ever handing it back.
type wu22FakeProvider struct {
	t      *testing.T
	status int
	err    error

	headers []http.Header
}

func (f *wu22FakeProvider) RoundTrip(req *http.Request) (*http.Response, error) {
	f.headers = append(f.headers, req.Header.Clone())
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Status:     fmt.Sprintf("%d %s", f.status, http.StatusText(f.status)),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
		Request:    req,
	}, nil
}

// wu22Probe is the probe under test, wired to one fake project and one fake
// provider. terminal is a person's by default: the refusal's other arm is
// what TestProbeRefusedInAnAgentSession drives.
func wu22Probe(t *testing.T, store probeSecrets, provider *wu22FakeProvider, getenv func(string) string) *WorkspaceProbe {
	t.Helper()
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return &WorkspaceProbe{getenv: getenv, terminal: true, open: wu22Open(store),
		client: &http.Client{Transport: provider}}
}

// TestProbeNeverPrintsTheValue: the value is read and spent, and it reaches
// nothing the caller can print (D2820). The first half is what makes the
// second half mean anything — a probe that read nothing would pass a test
// that only looked for the value.
func TestProbeNeverPrintsTheValue(t *testing.T) {
	const value = "wu22-value-that-must-not-be-printed"
	const id = "wu22-container-model-key"
	store := &wu22FakeSecrets{t: t,
		creds:  []probeCredential{{Name: config.AnthropicSecret, ID: id}},
		values: map[string]string{id: value}}
	provider := &wu22FakeProvider{t: t, status: http.StatusOK}

	results, err := wu22Probe(t, store, provider, nil).Probe(context.Background(), "member-project")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != ProbePassed {
		t.Fatalf("Probe = %+v, want one passed credential", results)
	}
	if len(provider.headers) != 1 {
		t.Fatalf("the probe made %d calls, want one", len(provider.headers))
	}
	if got := provider.headers[0].Get("x-api-key"); got != value {
		t.Errorf("the call carried x-api-key %q, want the credential's value: the test would pass on a probe that read nothing", got)
	}
	if got := store.read; len(got) != 1 || got[0] != id {
		t.Errorf("the probe read %v, want the one container", got)
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", results), fmt.Sprintf("%+v", results), fmt.Sprintf("%#v", results),
		results[0].Secret, results[0].Detail,
	} {
		if strings.Contains(rendered, value) {
			t.Errorf("the probe's answer carries the value: %q", rendered)
		}
	}
}

// TestProbeRefusedInAnAgentSession: the value must never reach an agent's
// process, so the flag's read is refused before anything is read (D2820).
// The IDE extension's own terminal is a person's, so it is not refused —
// the one case that would make the fence wider than the rule.
func TestProbeRefusedInAnAgentSession(t *testing.T) {
	t.Run("an agent's session", func(t *testing.T) {
		store := &wu22FakeSecrets{t: t}
		provider := &wu22FakeProvider{t: t, status: http.StatusOK}
		getenv := func(k string) string {
			if k == "CLAUDECODE" {
				return "1"
			}
			return ""
		}
		_, err := wu22Probe(t, store, provider, getenv).Probe(context.Background(), "member-project")
		if err == nil {
			t.Fatal("the probe read a member's credentials in a coding agent's session")
		}
		for _, want := range []string{checkCredentialsFlag, "refused in a coding agent's session"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not name %q", err, want)
			}
		}
		if store.listed != 0 {
			t.Errorf("the refusal listed the member's secrets %d times", store.listed)
		}
		if len(provider.headers) != 0 {
			t.Errorf("the refusal made %d calls", len(provider.headers))
		}
	})
	t.Run("a person's IDE terminal", func(t *testing.T) {
		const id = "wu22-container-model-key"
		store := &wu22FakeSecrets{t: t,
			creds:  []probeCredential{{Name: config.AnthropicSecret, ID: id}},
			values: map[string]string{id: "wu22-ide-terminal-value"}}
		provider := &wu22FakeProvider{t: t, status: http.StatusOK}
		getenv := func(k string) string {
			if k == "CLAUDE_CODE_SSE_PORT" {
				return "1"
			}
			return ""
		}
		results, err := wu22Probe(t, store, provider, getenv).Probe(context.Background(), "member-project")
		if err != nil {
			t.Fatalf("the IDE extension's own terminal was refused: %v", err)
		}
		if len(results) != 1 || results[0].Outcome != ProbePassed {
			t.Fatalf("Probe = %+v, want one passed credential", results)
		}
	})
}

// TestProbeReportsAProviderRefusal: the answer is the provider's own status,
// and a credential the provider refuses is told apart from a call nothing
// answered — an outage is not a credential to replace (the distinction
// doctor's own token check draws).
func TestProbeReportsAProviderRefusal(t *testing.T) {
	const value = "wu22-claude-account-value"
	const id = "wu22-container-claude-account"
	newProbe := func(t *testing.T, status int) (*WorkspaceProbe, *wu22FakeProvider) {
		store := &wu22FakeSecrets{t: t,
			creds:  []probeCredential{{Name: config.ClaudeOAuthToken, ID: id}},
			values: map[string]string{id: value}}
		provider := &wu22FakeProvider{t: t, status: status}
		return wu22Probe(t, store, provider, nil), provider
	}

	t.Run("401", func(t *testing.T) {
		p, provider := newProbe(t, http.StatusUnauthorized)
		results, err := p.Probe(context.Background(), "member-project")
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Probe = %+v, want one answer", results)
		}
		if results[0].Outcome != ProbeRefused {
			t.Errorf("outcome = %v, want refused", results[0].Outcome)
		}
		if results[0].Detail != "401 Unauthorized" {
			t.Errorf("detail = %q, want the provider's own status", results[0].Detail)
		}
		if got := provider.headers[0].Get("Authorization"); got != "Bearer "+value {
			t.Errorf("the call carried Authorization %q, want the subscription's bearer header", got)
		}
		if provider.headers[0].Get("x-api-key") != "" {
			t.Error("a Claude account was asked with x-api-key")
		}
	})
	t.Run("500 is unanswered, not refused", func(t *testing.T) {
		p, _ := newProbe(t, http.StatusInternalServerError)
		results, err := p.Probe(context.Background(), "member-project")
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if len(results) != 1 || results[0].Outcome != ProbeUnanswered {
			t.Fatalf("Probe = %+v, want one unanswered credential", results)
		}
		if results[0].Detail != "500 Internal Server Error" {
			t.Errorf("detail = %q, want the provider's own status", results[0].Detail)
		}
	})
}

// TestProbeSkipsPersonalTokens: a credential under a person's own prefix is
// never read (D2825) — the fence a workspace admin's terminal must not cross,
// because the credential is the person's and not the project's. It is
// reported rather than dropped: an owner reading `status` sees that the
// credential is deliberately not asked about, and where its broken mark comes
// from instead.
func TestProbeSkipsPersonalTokens(t *testing.T) {
	const personal = secretname.UserPrefix + "1234567890" + "_claude"
	const id = "wu22-container-model-key"
	store := &wu22FakeSecrets{t: t,
		creds: []probeCredential{
			{Name: personal, ID: personal},
			{Name: config.AnthropicSecret, ID: id},
		},
		values: map[string]string{id: "wu22-project-model-key"}}
	provider := &wu22FakeProvider{t: t, status: http.StatusOK}

	results, err := wu22Probe(t, store, provider, nil).Probe(context.Background(), "member-project")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Probe = %+v, want the personal credential and the project's own", results)
	}
	// The answers are in name order whatever order the store listed them in,
	// so the project's own key (a…) comes before the personal one (fugaro_u_…):
	// two runs over one project print the same lines in the same order.
	if results[0].Secret != config.AnthropicSecret || results[0].Outcome != ProbePassed {
		t.Errorf("the project's own model key answered %+v, want it passed", results[0])
	}
	if results[1].Secret != personal || results[1].Outcome != ProbeSkipped {
		t.Errorf("the personal credential answered %+v, want it skipped", results[1])
	}
	for _, got := range store.read {
		if got == personal {
			t.Errorf("the probe read the personal credential %s (D2825)", personal)
		}
	}
	if len(provider.headers) != 1 {
		t.Errorf("the probe made %d calls, want one: one call per credential it may ask about", len(provider.headers))
	}
}

// TestProbeAsksNoKeyCredentialOfAnotherProvider: the credential-<name>-key
// namespace is every KEY credential's, OpenRouter's included
// (config.KeyCredentialType is true for both), and which type a named
// credential is lives in the member's own config, which the probe does not
// read — CredentialBySecret answers the name and never the type. Asking
// Anthropic about one would disclose a third party's key to Anthropic and
// report a working credential refused, so the family is not asked about at
// all: nothing is read, nothing is sent.
func TestProbeAsksNoKeyCredentialOfAnotherProvider(t *testing.T) {
	const id = "wu22-container-openrouter-key"
	store := &wu22FakeSecrets{t: t,
		creds:  []probeCredential{{Name: config.CredentialSecret("openrouter"), ID: id}},
		values: map[string]string{id: "wu22-openrouter-value"}}
	provider := &wu22FakeProvider{t: t, status: http.StatusOK}

	results, err := wu22Probe(t, store, provider, nil).Probe(context.Background(), "member-project")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Probe = %+v, want no answer: a named key credential's type is the member's config's, and this namespace holds other providers' keys", results)
	}
	if len(store.read) != 0 {
		t.Errorf("the probe read %v, want nothing read", store.read)
	}
	if len(provider.headers) != 0 {
		t.Errorf("the probe made %d calls, want none: another provider's key is never sent to Anthropic", len(provider.headers))
	}
}

// TestProbeNeverFallsBackToTheDefaultClient: a probe built with no client asks
// nothing. The fallback this pins was http.DefaultClient, which follows a
// redirect and carries x-api-key with it — the leak the client's own
// CheckRedirect prevents — so "no client" must be a refusal and never the plan
// B path (D2820's "never the value" covers never sending it elsewhere).
func TestProbeNeverFallsBackToTheDefaultClient(t *testing.T) {
	store := &wu22FakeSecrets{t: t,
		creds:  []probeCredential{{Name: config.AnthropicSecret, ID: "wu22-container-model-key"}},
		values: map[string]string{"wu22-container-model-key": "wu22-value-that-must-not-travel"}}
	p := &WorkspaceProbe{getenv: func(string) string { return "" }, terminal: true, open: wu22Open(store)}

	if _, err := p.Probe(context.Background(), "member-project"); err == nil {
		t.Fatal("a probe with no HTTP client asked anyway: the default client follows a redirect with the credential attached")
	}
	if store.listed != 0 || len(store.read) != 0 {
		t.Errorf("the probe with no client read the member's secrets (%d listings, %v)", store.listed, store.read)
	}
}

// TestProbeDoesNotFollowARedirect: the credential travels in a header, and a
// redirect is where it could reach a host the owner never named — Go drops
// Authorization across hosts but not a header it does not know, and x-api-key
// is one of those. The client a command builds refuses to follow one, and the
// 3xx it is handed back is a call nothing answered: "never the value" (D2820)
// includes never sending it somewhere else. The two servers are this machine's
// own (httptest), so nothing here leaves it.
func TestProbeDoesNotFollowARedirect(t *testing.T) {
	var elsewhere int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		elsewhere++
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	oldURL := probeAPIURL
	probeAPIURL = redirector.URL
	t.Cleanup(func() { probeAPIURL = oldURL })

	const id = "wu22-container-model-key"
	store := &wu22FakeSecrets{t: t,
		creds:  []probeCredential{{Name: config.AnthropicSecret, ID: id}},
		values: map[string]string{id: "wu22-value-that-must-not-travel"}}
	p := &WorkspaceProbe{getenv: func(string) string { return "" }, terminal: true,
		open: wu22Open(store), client: probeAPIClient}

	results, err := p.Probe(context.Background(), "member-project")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != ProbeUnanswered {
		t.Fatalf("Probe = %+v, want one unanswered credential: a redirect is a call nothing answered", results)
	}
	if elsewhere != 0 {
		t.Errorf("the credential was carried to the host the redirect named (%d calls there)", elsewhere)
	}
}
