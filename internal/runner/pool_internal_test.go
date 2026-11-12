package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// signaledModel is the ID every call of the F5 test names: the one shape a
// route may claim out of Claude's own family (config.CheckClaudeClaim), so the
// same fixture works whichever road an answer takes.
const signaledModel = "anthropic/claude-sonnet-5.5"

// signaledKey is the placeholder the test's gateway attaches to the provider's
// calls. It is not a credential and never was one.
const signaledKey = "sk-or-v1-0123456789abcdef-not-a-real-key"

// signaledPrices is the owner's model_prices row for the claimed ID: a routed
// model has no built-in row, and the gateway refuses a call routed to a
// provider it cannot price before the provider is reached at all.
const signaledPrices = `{"` + signaledModel + `": {"input_per_m": 3, "output_per_m": 15}}`

// TestPoolExhaustionSignals pins F5's table against a hermetic fake: every row
// of every credential type is sent to a fake upstream as the status and the
// error type it records, relayed by a real gateway, and the text the agent
// would read is the text this table must read.
//
// What it refuses, and why each is a way the table goes wrong: a row whose
// status a real gateway does not relay as the row's phrase (the signal would
// never be recognised on the road that carries it); a row whose phrase the
// row's own type does not read (the signal is recorded and never acted on);
// and a row with no phrase that the classifier reads anyway, which would move
// a run off a credential on wording this table does not claim to know (Vertex
// answers with Google's own body, and that is recorded rather than guessed).
//
// Every row here is read for its own type only, which is half of the rule and
// the half a row can be wrong about on its own; the other half — that one
// type's signal is not read as another's — is TestPoolExhaustionSignalsArePerType's.
func TestPoolExhaustionSignals(t *testing.T) {
	types := slices.Sorted(maps.Keys(exhaustionSignals))
	if len(types) != 4 {
		t.Fatalf("the table holds %d credential types (%s), want the four this release ships", len(types), strings.Join(types, ", "))
	}
	for _, typ := range types {
		for _, sig := range exhaustionSignals[typ] {
			name := typ
			if sig.Status != 0 {
				name += "/" + fmt.Sprint(sig.Status)
			} else {
				name += "/" + sig.Phrase
			}
			t.Run(name, func(t *testing.T) {
				if sig.Phrase == "" {
					// A row with no phrase is a signal this table records and
					// does not claim to read: the answer's own words reach the
					// agent, and the classifier must leave them alone.
					if sig.Status == 0 {
						t.Fatal("a row with neither a status nor a phrase records nothing")
					}
					if got := relayedText(t, sig.Status, sig.Type); credentialExhaustedText(typ, got) {
						t.Fatalf("the %s row claims no wording, and the classifier read %q as exhaustion", typ, got)
					}
					return
				}
				if sig.Status == 0 {
					// A phrase no status carries: the OAuth allowance is spent
					// inside a session that answers normally, and the CLI's own
					// sentence is the only witness.
					if !credentialExhaustedText(typ, "the agent said: "+sig.Phrase+" (nothing else)") {
						t.Fatalf("the %s row's phrase %q is not read", typ, sig.Phrase)
					}
					if got := exhaustionWhy(typ, "the agent said: "+sig.Phrase+" (nothing else)"); got != sig.Why {
						t.Fatalf("the %s row names %q, want %q", typ, got, sig.Why)
					}
					return
				}
				text := relayedText(t, sig.Status, sig.Type)
				if !strings.Contains(strings.ToLower(text), sig.Phrase) {
					t.Fatalf("status %d as %s reaches the agent as %q, which does not carry the row's phrase %q",
						sig.Status, sig.Type, text, sig.Phrase)
				}
				if !credentialExhaustedText(typ, text) {
					t.Fatalf("status %d as %s reaches the agent as %q, and the %s row does not read it",
						sig.Status, sig.Type, text, typ)
				}
				if got := exhaustionWhy(typ, text); got != sig.Why {
					t.Fatalf("the %s row names %q, want %q", typ, got, sig.Why)
				}
			})
		}
	}
}

// TestPoolExhaustionSignalsArePerType pins the other half of the same rule: a
// signal is read for the credential type that served the attempt and for no
// other. The OAuth subscription's allowance is the clearest case -- a spent
// subscription is not an Anthropic API key's business -- and it is the one
// where reading too widely would move a member that was answering.
func TestPoolExhaustionSignalsArePerType(t *testing.T) {
	const spent = "claude ai usage limit"
	if !credentialExhaustedText("claude-oauth", spent) {
		t.Fatal("the OAuth row does not read the allowance's own sentence")
	}
	for _, typ := range []string{"anthropic", "openrouter", "vertex"} {
		if credentialExhaustedText(typ, spent) {
			t.Fatalf("the %s row reads the OAuth subscription's sentence as its own", typ)
		}
	}
	if credentialExhaustedText("openai", spent) {
		t.Fatal("a type the table does not hold reads a signal")
	}
	if credentialExhaustedText("claude-oauth", "") {
		t.Fatal("an empty text is read as exhaustion")
	}
}

// relayedText is what the agent reads when the upstream answered status as typ:
// a real gateway over one route to a fake provider, as the runner builds it.
// The road is the provider's, which is where a pool member on the runner path
// is served, and its relay (internal/gateway, providerError) is what turns an
// upstream answer into the text a stage's result carries.
func relayedText(t *testing.T, status int, typ string) string {
	t.Helper()
	f, srv := anthropicfake.New(t)
	f.Func = func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.Error(status, typ, "the credential serving this call has nothing left")
	}
	ov, err := pricing.ParseOverrides(signaledPrices)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := pricing.Embedded().With(ov)
	if err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.Start(context.Background(), gateway.Options{
		Upstream: gateway.Upstream{Kind: "anthropic", APIKey: signaledKey},
		Routes: []gateway.Route{{
			Name: "the-provider", Models: []string{signaledModel},
			BaseURL: srv.URL, Auth: "bearer",
			Credential: func() (string, error) { return signaledKey, nil },
		}},
		Prices: prices,
		Mode:   gateway.Observe,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("starting the gateway: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(gateway.Stage{Name: "implement", Model: signaledModel})
	body := fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, signaledModel)
	req, err := http.NewRequest(http.MethodPost, gw.URL()+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", gw.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("posting: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if resp.StatusCode != status {
		t.Fatalf("the gateway answered %d, want the provider's own %d (%s)", resp.StatusCode, status, b)
	}
	return string(b)
}
