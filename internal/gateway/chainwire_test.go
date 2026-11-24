// This file pins the gateway's half of the chain wire (C1): the names it
// mirrors from internal/credsvc/chain_wire.go, the request-side rule that makes
// the stage header the gateway's own, and what one served call tells the step
// it belongs to. It is an external test file (package gateway_test) so that it
// may import internal/credsvc — the package that cannot be imported from
// internal/gateway — and hold the two spellings of one wire to each other.
package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
)

// chainHook is the step's record as a test reads it: what one served call told
// it, in order. It guards its slice because the gateway calls the hook from
// concurrent calls with no lock of its own held (ChainAnswerFunc).
type chainHook struct {
	mu      sync.Mutex
	answers []gateway.ChainAnswer
}

func (h *chainHook) told(a gateway.ChainAnswer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answers = append(h.answers, a)
}

func (h *chainHook) seen() []gateway.ChainAnswer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gateway.ChainAnswer(nil), h.answers...)
}

// chainMembers is what the service writes on every chain answer: the source
// that served the call, the fallback flag and the served model.
func chainMembers(source, fallback, model string) http.Header {
	return http.Header{
		gateway.HeaderSource:   []string{source},
		gateway.HeaderFallback: []string{fallback},
		gateway.HeaderModel:    []string{model},
	}
}

// poolSource is the source member the service writes for a call a named pool
// served: the JSON of the contract's own routing.SourceName, which the run's
// record writes and the console reads.
func poolSource(name string) string {
	b, err := json.Marshal(routing.SourceName{Kind: routing.KindPool, Name: name})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// servedReply is one served call's answer: the tier and the credential a
// Messages call settles by, plus the chain members a given case wants (which
// may be none, which is what a service built before this contract writes).
func servedReply(members http.Header) anthropicfake.Reply {
	rep := anthropicfake.MessageOK(mirrorModel, pricing.Usage{Input: 1, Output: 1})
	rep.Header = http.Header{
		gateway.HeaderTier:       []string{gateway.TierB},
		gateway.HeaderCredential: []string{"shared-b"},
	}
	for k, vs := range members {
		rep.Header[k] = vs
	}
	return rep
}

// startChain starts a gateway on the service upstream with the chain wire
// switched on or off, recording what the hook is told. It is the black box this
// file examines from outside the package: the gateway a runner builds for a
// repository the credentials service serves.
func startChain(t *testing.T, served gateway.ChainServed, hook *chainHook, answer func(*http.Request, []byte) anthropicfake.Reply) (*gateway.Server, *anthropicfake.Fake) {
	t.Helper()
	f, srv := anthropicfake.New(t)
	f.Func = answer
	opts := gateway.Options{
		Upstream: gateway.Upstream{
			Kind: gateway.KindService, BaseURL: srv.URL,
			Service: &gateway.Service{
				IDToken:    func(context.Context) (string, error) { return "id-token-" + mirrorName(t), nil },
				RunIDToken: func() string { return "run-identity-" + mirrorName(t) },
				Job:        mirrorJob,
			},
		},
		Prices:      pricing.Embedded(),
		Mode:        gateway.Observe,
		Log:         slog.New(slog.DiscardHandler),
		ChainServed: served,
	}
	if hook != nil {
		opts.OnChainAnswer = hook.told
	}
	gw, err := gateway.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(gateway.Stage{Name: "implement", Model: mirrorModel})
	return gw, f
}

// postChain sends one Messages call and returns its answer, body read out of
// the way: a case that asks what the agent sees reads the headers.
func postChain(t *testing.T, gw *gateway.Server, extra http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", gw.URL()+"/v1/messages",
		strings.NewReader(`{"model":"`+mirrorModel+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", gw.Token())
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp
}

// oneRequest is the single call the service received, as the service's own
// reader reads it: the gateway sends one, and a case that asks what arrived
// must not read a request of another case's.
func oneRequest(t *testing.T, f *anthropicfake.Fake) *http.Request {
	t.Helper()
	seen := f.Seen()
	if len(seen) != 1 {
		t.Fatalf("the credentials service received %d calls, want one", len(seen))
	}
	return seen[0]
}

// TestGatewaySendsTheStage: the gateway says which stage a call is for, on
// every call it makes to the service, and never names a model in a header.
// The stage header is the gateway's own — set from the run's current stage,
// with the caller's copy dropped first — and a gateway whose run the service
// does not serve sends none at all.
func TestGatewaySendsTheStage(t *testing.T) {
	t.Run("the run's stage travels and no model header does", func(t *testing.T) {
		hook := &chainHook{}
		members := chainMembers(poolSource("shared-b"), gateway.FallbackNo, mirrorModel)
		gw, f := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		if resp := postChain(t, gw, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}

		req := oneRequest(t, f)
		// The mirror's request side: the service's own reader sees a gateway of
		// this contract on this call, and the stage it reads is the run's.
		if !credsvc.SentStage(req.Header) {
			t.Fatalf("the service does not read this call as a gateway of the contract: %s = %q",
				credsvc.HeaderStage, req.Header.Get(credsvc.HeaderStage))
		}
		if got := req.Header.Get(credsvc.HeaderStage); !credsvc.ValidStageName(got) || got != "implement" {
			t.Errorf("the service read the stage as %q, want implement: the run's own stage, and one the service accepts as a stage name", got)
		}
		for _, name := range []string{credsvc.HeaderModel, credsvc.HeaderSource, credsvc.HeaderFallback} {
			if got := req.Header.Get(name); got != "" {
				t.Errorf("the call carried %s = %q: the service reads the model from the body, and the answer's own members mean nothing in a request", name, got)
			}
		}

		if told := hook.seen(); len(told) != 1 {
			t.Fatalf("the step's record was told %d answers, want one", len(told))
		} else if a := told[0]; a.Source.Kind != routing.KindPool || a.Source.Name != "shared-b" {
			t.Errorf("the record was told the source %+v, want the pool shared-b", a.Source)
		} else if a.Fallback || a.Model != mirrorModel {
			t.Errorf("the record was told fallback=%v model=%q, want false and %q", a.Fallback, a.Model, mirrorModel)
		}
	})

	t.Run("the caller's own headers are dropped", func(t *testing.T) {
		// Every name of this wire, sent by the agent on the call the gateway
		// proxies. The stage header is the whole of R24's guard — an agent that
		// could send it makes the service answer in full, and the service cannot
		// tell the gateway's own from one the agent wrote — so the gateway drops
		// the caller's copy and sets its own, exactly as it does the provider's
		// name.
		hook := &chainHook{}
		members := chainMembers(poolSource("shared-b"), gateway.FallbackNo, mirrorModel)
		gw, f := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		extra := http.Header{}
		extra.Set(gateway.HeaderStage, "review")
		extra.Set(gateway.HeaderModel, "another-model")
		extra.Set(gateway.HeaderSource, `{"kind":"anthropic","name":""}`)
		extra.Set(gateway.HeaderFallback, gateway.FallbackYes)
		if resp := postChain(t, gw, extra); resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}

		req := oneRequest(t, f)
		if got := req.Header.Get(credsvc.HeaderStage); got != "implement" {
			t.Errorf("the service read the stage as %q, want the run's own (implement) and never the caller's", got)
		}
		if vs := req.Header.Values(credsvc.HeaderStage); len(vs) != 1 {
			t.Errorf("the call carried the stage header %d times (%q), want once", len(vs), vs)
		}
		for _, name := range []string{credsvc.HeaderModel, credsvc.HeaderSource, credsvc.HeaderFallback} {
			if got := req.Header.Get(name); got != "" {
				t.Errorf("the caller's own %s = %q reached the service", name, got)
			}
		}
	})

	t.Run("the next stage travels", func(t *testing.T) {
		// Set from the run's CURRENT stage, not once at the start: the stage is
		// what the service records a fallback against, so a run whose review
		// step fell back must not be recorded as its implement step having done
		// so (CS8).
		hook := &chainHook{}
		members := chainMembers(poolSource("shared-b"), gateway.FallbackNo, mirrorModel)
		gw, f := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		if resp := postChain(t, gw, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}
		gw.EndStage()
		gw.BeginStage(gateway.Stage{Name: "review", Model: mirrorModel})
		if resp := postChain(t, gw, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("the second call was answered %d", resp.StatusCode)
		}

		seen := f.Seen()
		if len(seen) != 2 {
			t.Fatalf("the credentials service received %d calls, want two", len(seen))
		}
		if got := seen[0].Header.Get(credsvc.HeaderStage); got != "implement" {
			t.Errorf("the first call reported the stage %q, want implement", got)
		}
		if got := seen[1].Header.Get(credsvc.HeaderStage); got != "review" {
			t.Errorf("the second call reported the stage %q, want review", got)
		}
	})

	t.Run("a service that predates the contract is not an error", func(t *testing.T) {
		// The mirror in time: this gateway sends the stage header, and a
		// service built before the contract does not read it, so its answer
		// carries none of the three members. That is the answer every call
		// meets today, and the run goes on with no source recorded for the
		// call rather than halting on a member the service never promised to
		// write (R25: the two sides are released one at a time).
		hook := &chainHook{}
		gw, _ := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(nil)
		})
		if resp := postChain(t, gw, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}
		if told := hook.seen(); len(told) != 0 {
			t.Errorf("the record was told %+v by an answer that named no source", told)
		}
		if detail := haltDetail(t, gw); detail != "" {
			t.Errorf("the run halted on an answer of a service that predates the contract: %s", detail)
		}
	})

	t.Run("a gateway the service does not serve sends nothing", func(t *testing.T) {
		// ChainServed false is the run whose repository has not migrated: the
		// gateway behaves exactly as it did before this contract, so it asks the
		// service for nothing — and the caller's copy of the header is dropped
		// whatever the option says, because the drop is what makes the header
		// the gateway's own.
		hook := &chainHook{}
		members := chainMembers(poolSource("shared-b"), gateway.FallbackNo, mirrorModel)
		gw, f := startChain(t, false, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		extra := http.Header{}
		extra.Set(gateway.HeaderStage, "review")
		if resp := postChain(t, gw, extra); resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}

		if got := oneRequest(t, f).Header.Get(credsvc.HeaderStage); got != "" {
			t.Errorf("a gateway the service does not serve reported the stage %q", got)
		}
		if told := hook.seen(); len(told) != 0 {
			t.Errorf("the record was told %+v by a gateway that asked for nothing", told)
		}
	})

	t.Run("an answer this contract could not have written halts", func(t *testing.T) {
		// The members are what the run's record and its ledger charge by, so an
		// answer that carries part of them, or one that is not what this
		// contract writes, is an answer the gateway cannot account the call by:
		// the run halts (stateDetailUnreadable) rather than recording a source
		// it guessed at, exactly as it does for a tier, an owner or a probe it
		// cannot read.
		for _, tc := range []struct {
			what    string
			members http.Header
		}{
			{"a source with no fallback and no model", http.Header{
				gateway.HeaderSource: []string{poolSource("shared-b")}}},
			{"a fallback with no source", http.Header{
				gateway.HeaderFallback: []string{gateway.FallbackNo}}},
			{"a source that is not the shape", chainMembers("not json", gateway.FallbackNo, mirrorModel)},
			{"a source of no kind this contract carries", chainMembers(`{"kind":"nothing","name":""}`, gateway.FallbackNo, mirrorModel)},
			{"a fallback flag that is neither value", chainMembers(poolSource("shared-b"), "yes", mirrorModel)},
			{"a served model that is not one clean value", chainMembers(poolSource("shared-b"), gateway.FallbackNo, "a model with spaces")},
		} {
			t.Run(tc.what, func(t *testing.T) {
				hook := &chainHook{}
				members := tc.members
				gw, _ := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
					return servedReply(members)
				})
				resp := postChain(t, gw, nil)
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("the call was answered %d, want the halt (403)", resp.StatusCode)
				}
				if detail := haltDetail(t, gw); !strings.Contains(detail, "cannot read") {
					t.Errorf("the run halted with %q, want the answer it cannot read", detail)
				}
			})
		}
	})
}

// TestTheAgentNeverSeesTheSource: the answer's source, fallback and served
// model are the run's own accounting, read into the step's record and stripped
// before the agent sees the answer. The strip does not depend on the run's
// option and not on the member being one this release can read: it is the
// dropResponseHeaders list's, and what is not relaid is not read.
func TestTheAgentNeverSeesTheSource(t *testing.T) {
	t.Run("the members are read and stripped", func(t *testing.T) {
		hook := &chainHook{}
		members := chainMembers(poolSource("shared-b"), gateway.FallbackNo, mirrorModel)
		gw, _ := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		resp := postChain(t, gw, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}
		if len(hook.seen()) != 1 {
			t.Errorf("the record was told %d answers, want one: the gateway read the members it stripped", len(hook.seen()))
		}
		for _, name := range []string{gateway.HeaderSource, gateway.HeaderFallback, gateway.HeaderModel} {
			if got := resp.Header.Get(name); got != "" {
				t.Errorf("the agent's answer carried %s = %q", name, got)
			}
		}
		// The drop is the list's and not a blanket: the answer is relayed, with
		// the headers the agent may read.
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("the agent's answer carried Content-Type %q, want the model's own answer", got)
		}
	})

	t.Run("a fallback is told to the record and stripped too", func(t *testing.T) {
		hook := &chainHook{}
		members := chainMembers(`{"kind":"anthropic","name":""}`, gateway.FallbackYes, "claude-opus-5-5")
		gw, _ := startChain(t, true, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		resp := postChain(t, gw, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}
		told := hook.seen()
		if len(told) != 1 {
			t.Fatalf("the record was told %d answers, want one", len(told))
		}
		if !told[0].Fallback {
			t.Error("a call the service marked as a fallback was recorded as the chain's first source")
		}
		// The served model is not the one the agent asked for: the record has it
		// and the agent still does not (CS3 shows the agent the model it asked
		// for, and this header is the only place the served one travels).
		if told[0].Model != "claude-opus-5-5" {
			t.Errorf("the record was told the served model %q", told[0].Model)
		}
		if got := resp.Header.Get(gateway.HeaderModel); got != "" {
			t.Errorf("the agent's answer carried the served model %q", got)
		}
	})

	t.Run("a gateway that asked for nothing strips them anyway", func(t *testing.T) {
		// A service that wrote the members to a gateway that never sent the
		// stage header is not a case this contract produces, and the strip is
		// still the answer's: the list is what makes relaying safe, so a run
		// whose repository has not migrated does not depend on the service's
		// courtesy for it.
		hook := &chainHook{}
		members := chainMembers(poolSource("shared-b"), gateway.FallbackYes, mirrorModel)
		gw, _ := startChain(t, false, hook, func(*http.Request, []byte) anthropicfake.Reply {
			return servedReply(members)
		})
		resp := postChain(t, gw, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call was answered %d", resp.StatusCode)
		}
		for _, name := range []string{gateway.HeaderSource, gateway.HeaderFallback, gateway.HeaderModel} {
			if got := resp.Header.Get(name); got != "" {
				t.Errorf("the agent's answer carried %s = %q", name, got)
			}
		}
	})
}

// TestChainWireMirrorsCredsvc: the names the gateway sends and reads
// (chainwire.go) are the wire's own, held one by one against internal/credsvc —
// the package this one cannot import — and held apart from the names the wire
// already carried. A name that moved on one side alone is a header no one
// reads: the stage header would leave every call answered in the pre-contract
// shape, and the answer's members would never be stripped.
func TestChainWireMirrorsCredsvc(t *testing.T) {
	for _, tc := range []struct{ what, gateway, wire string }{
		{"the stage header", gateway.HeaderStage, credsvc.HeaderStage},
		{"the source header", gateway.HeaderSource, credsvc.HeaderSource},
		{"the fallback header", gateway.HeaderFallback, credsvc.HeaderFallback},
		{"the served model's header", gateway.HeaderModel, credsvc.HeaderModel},
		{"the fallback's yes", gateway.FallbackYes, credsvc.FallbackYes},
		{"the fallback's no", gateway.FallbackNo, credsvc.FallbackNo},
	} {
		if tc.gateway != tc.wire {
			t.Errorf("%s is %q in the gateway and %q on the wire", tc.what, tc.gateway, tc.wire)
		}
	}

	// The request side, with the names: the stage header is the one name of
	// this wire that travels on a request, it is set by the gateway alone, and
	// no request header names a model — the service reads the model a call is
	// for from the body. The three answer members are dropped from a request
	// for that same reason (serviceUpstream).
	request := []string{gateway.HeaderStage}
	answer := []string{gateway.HeaderSource, gateway.HeaderFallback, gateway.HeaderModel}
	for _, name := range request {
		if strings.Contains(name, "Model") {
			t.Errorf("%s is a request header naming a model; the service reads the model from the body", name)
		}
	}
	hasModel := false
	for _, name := range answer {
		hasModel = hasModel || name == gateway.HeaderModel
	}
	if !hasModel {
		t.Error("the served model's header is not an answer member: it is the direction the served model travels and nothing else")
	}

	// Every name the wire carried before this contract, on this side of it.
	// A new header equal to one of them is one name with two meanings, and the
	// service's answer would be read as whichever reader knew it first.
	existing := []struct{ what, name string }{
		{"the run identity's header", gateway.HeaderRunIdentity},
		{"the job's header", gateway.HeaderRunJob},
		{"the slip's header", gateway.HeaderSlip},
		{"the claim secret's header", gateway.HeaderSlipClaim},
		{"the auth mode's header", gateway.HeaderAuthMode},
		{"the auth account's header", gateway.HeaderAuthAccount},
		{"the tier's header", gateway.HeaderTier},
		{"the credential's header", gateway.HeaderCredential},
		{"the credential owner's header", gateway.HeaderCredentialOwner},
		{"the switch's header", gateway.HeaderSwitch},
		{"the probe's header", gateway.HeaderProbe},
		{"the provider's header", gateway.HeaderProvider},
	}
	all := append(append([]string{}, request...), answer...)
	for _, name := range all {
		if name == "" {
			t.Error("a name of the chain wire is empty; a header with no name carries nothing")
			continue
		}
		if !strings.HasPrefix(name, "X-Fugaro-") {
			t.Errorf("%s is not X-Fugaro-prefixed", name)
		}
		for _, e := range existing {
			if name == e.name {
				t.Errorf("%s is %q, which is already %s", name, e.name, e.what)
			}
		}
	}
	for i, a := range all {
		for _, b := range all[i+1:] {
			if a == b {
				t.Errorf("two names of the chain wire are both %q", a)
			}
		}
	}
}
