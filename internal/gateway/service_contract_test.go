// Package gateway_test holds the pin on the credentials service's wire
// contract: internal/gateway mirrors C1.10's contract in internal/credsvc
// because internal/credsvc imports internal/gateway and the dependency cannot
// run the other way. This file is what keeps the mirror honest -- it may
// import both packages, so it holds one against the other: the names, the
// closed sets and every rule the two readers agree on.
//
// A change to either half that this file does not know about is a test
// failure, which is the whole point: the two ends of this wire are one
// repository's code and are released together.
package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

const mirrorModel = "claude-sonnet-5-5"

// The identities and the job these cases present, built from the test's name:
// no literal in this file is, or looks like, a credential.
func mirrorName(t *testing.T) string {
	t.Helper()
	return strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

const mirrorJob = "fugaro-run-job"

// newServiceMirror starts a gateway on the service upstream from outside the
// package: the black box this file examines is the one a runner builds.
func newServiceMirror(t *testing.T, answer func(*http.Request, []byte) anthropicfake.Reply) *gateway.Server {
	t.Helper()
	f, srv := anthropicfake.New(t)
	f.Func = answer
	gw, err := gateway.Start(context.Background(), gateway.Options{
		Upstream: gateway.Upstream{
			Kind: gateway.KindService, BaseURL: srv.URL,
			Service: &gateway.Service{
				IDToken:    func(context.Context) (string, error) { return "id-token-" + mirrorName(t), nil },
				RunIDToken: func() string { return "run-identity-" + mirrorName(t) },
				Job:        mirrorJob,
			},
		},
		Prices: pricing.Embedded(),
		Mode:   gateway.Observe,
		Log:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	gw.BeginStage(gateway.Stage{Name: "implement", Model: mirrorModel})
	return gw
}

// postMirror sends one Messages call and returns its answer, read.
func postMirror(t *testing.T, gw *gateway.Server) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", gw.URL()+"/v1/messages",
		strings.NewReader(`{"model":"`+mirrorModel+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", gw.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// haltDetail is the detail of the run's halt, or "" when it did not halt.
func haltDetail(t *testing.T, gw *gateway.Server) string {
	t.Helper()
	select {
	case h := <-gw.Halted():
		return h.Detail
	default:
		return ""
	}
}

// TestServiceConstantsMirrorCredsvc: the names, the tiers, the refusal types
// and the reasons on the gateway's side are the contract's own values, one by
// one.
func TestServiceConstantsMirrorCredsvc(t *testing.T) {
	headers := []struct {
		what          string
		gateway, wire string
	}{
		{"the run identity's header", gateway.HeaderRunIdentity, credsvc.HeaderRunIdentity},
		{"the job's header", gateway.HeaderRunJob, credsvc.HeaderRunJob},
		{"the tier's header", gateway.HeaderTier, credsvc.HeaderTier},
		{"the credential's header", gateway.HeaderCredential, credsvc.HeaderCredential},
		{"the switch's header", gateway.HeaderSwitch, credsvc.HeaderSwitch},
	}
	for _, h := range headers {
		if h.gateway != h.wire {
			t.Errorf("%s is %q in the gateway and %q on the wire", h.what, h.gateway, h.wire)
		}
	}
	tiers := []struct{ gateway, wire string }{
		{gateway.TierA, credsvc.TierA}, {gateway.TierB, credsvc.TierB}, {gateway.TierC, credsvc.TierC},
	}
	for _, x := range tiers {
		if x.gateway != x.wire {
			t.Errorf("a tier is %q in the gateway and %q on the wire", x.gateway, x.wire)
		}
	}
	errs := []struct{ gateway, wire string }{
		{gateway.ErrorExhaustedUntil, credsvc.ErrorExhaustedUntil},
		{gateway.ErrorUnverifiedRunIdentity, credsvc.ErrorUnverifiedRunIdentity},
		{gateway.ErrorUnverifiedJobIdentity, credsvc.ErrorUnverifiedJobIdentity},
		{gateway.ErrorStateUnavailable, credsvc.ErrorStateUnavailable},
	}
	for _, x := range errs {
		if x.gateway != x.wire {
			t.Errorf("a refusal type is %q in the gateway and %q on the wire", x.gateway, x.wire)
		}
	}
	reasons := []struct{ gateway, wire string }{
		{gateway.ReasonExhausted, credsvc.ReasonExhausted},
		{gateway.ReasonBroken, credsvc.ReasonBroken},
		{gateway.ReasonNotAdmitted, credsvc.ReasonNotAdmitted},
		{gateway.ReasonStopped, credsvc.ReasonStopped},
	}
	for _, x := range reasons {
		if x.gateway != x.wire {
			t.Errorf("a tried reason is %q in the gateway and %q on the wire", x.gateway, x.wire)
		}
	}
	// The halt D143 names is spelled the way the run's record spells it.
	if gateway.ReasonBudgetUnavailable != "budget_unavailable" {
		t.Errorf("the halt is %q, want budget_unavailable (D143)", gateway.ReasonBudgetUnavailable)
	}
}

// TestServiceRulesMirrorCredsvc: every rule the gateway reads an answer by is
// the contract's own rule, asked of both sides about the same values. The
// gateway's answer is its behaviour, not a copy of its code: a Messages call
// whose answer carries a bad tier, a bad name, a bad reason or that is over a
// bound is a call the run halts on.
func TestServiceRulesMirrorCredsvc(t *testing.T) {
	ok := anthropicfake.MessageOK(mirrorModel, pricing.Usage{Input: 1, Output: 1})

	t.Run("the tiers", func(t *testing.T) {
		// A value with leading or trailing space is not in the table: HTTP
		// trims header values on the way, so what the gateway would see is
		// the trimmed one and both sides would agree about a value neither
		// was asked about.
		for _, tier := range []string{"A", "B", "C", "D", "a", "", "AA", "AAA"} {
			h := newServiceMirror(t, func(*http.Request, []byte) anthropicfake.Reply {
				r := ok
				r.Header = http.Header{gateway.HeaderTier: []string{tier}, gateway.HeaderCredential: []string{"repo-a"}}
				return r
			})
			status, _ := postMirror(t, h)
			served := status == http.StatusOK
			if want := credsvc.ValidTier(tier); served != want {
				t.Errorf("tier %q: the gateway served it as %v, the contract says %v", tier, served, want)
			}
		}
	})

	t.Run("the credential names", func(t *testing.T) {
		names := []string{
			"repo-a", "cred-1", strings.Repeat("n", 200), strings.Repeat("n", 201),
			"", "a name with spaces", "café", "a\tb", "a'b", "with/slash", "dots.in.name",
		}
		for _, name := range names {
			h := newServiceMirror(t, func(*http.Request, []byte) anthropicfake.Reply {
				r := ok
				r.Header = http.Header{gateway.HeaderTier: []string{"A"}, gateway.HeaderCredential: []string{name}}
				return r
			})
			status, _ := postMirror(t, h)
			served := status == http.StatusOK
			if want := credsvc.ValidCredentialName(name); served != want {
				t.Errorf("name %q: the gateway served it as %v, the contract says %v", name, served, want)
			}
		}
	})

	t.Run("the tried reasons", func(t *testing.T) {
		for _, reason := range []string{"exhausted", "broken", "not_admitted", "stopped", "expired", ""} {
			body := `{"error":"exhausted_until","reset_at":1800000000000,"tried":[{"name":"cred-a","tier":"B","reason":"` + reason + `"}]}`
			h := newServiceMirror(t, func(*http.Request, []byte) anthropicfake.Reply {
				return anthropicfake.Reply{Status: http.StatusServiceUnavailable, Body: body}
			})
			status, _ := postMirror(t, h)
			// An accepted refusal is the wait: 503 to the agent. A refused
			// body halts the run (403).
			actedOn := status == http.StatusServiceUnavailable
			if want := credsvc.ValidReason(reason); actedOn != want {
				t.Errorf("reason %q: the gateway acted on it as %v, the contract says %v", reason, actedOn, want)
			}
		}
	})

	t.Run("the job names", func(t *testing.T) {
		names := []string{
			mirrorJob, "job", "j", strings.Repeat("j", 49), strings.Repeat("j", 50),
			"", "9job", "Job", "job_name", "job-", "-job", "job--two", "job name",
		}
		for _, name := range names {
			// A job name is checked where the upstream is built, so this is
			// Start's answer rather than a call's.
			gw, err := gateway.Start(context.Background(), gateway.Options{
				Upstream: gateway.Upstream{Kind: gateway.KindService, BaseURL: "https://creds.example",
					Service: &gateway.Service{
						IDToken:    func(context.Context) (string, error) { return "id-token", nil },
						RunIDToken: func() string { return "run-identity" },
						Job:        name,
					}},
				Prices: pricing.Embedded(),
				Mode:   gateway.Observe,
			})
			accepted := err == nil
			if accepted {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = gw.Close(ctx)
				cancel()
			}
			if want := credsvc.ValidJobName(name); accepted != want {
				t.Errorf("job %q: the gateway accepted it as %v, the contract says %v", name, accepted, want)
			}
		}
	})

	t.Run("the bounds on a refusal body", func(t *testing.T) {
		// typed reports whether the gateway acted on the body as the
		// refusal it names (state_unavailable, whose halt says so) rather
		// than as a body it cannot read.
		typed := func(t *testing.T, status int, body string) bool {
			t.Helper()
			h := newServiceMirror(t, func(*http.Request, []byte) anthropicfake.Reply {
				return anthropicfake.Reply{Status: status, Body: body}
			})
			status2, _ := postMirror(t, h)
			if status2 != http.StatusForbidden {
				t.Fatalf("a refusal naming state_unavailable answered %d", status2)
			}
			return strings.Contains(haltDetail(t, h), credsvc.ErrorStateUnavailable)
		}
		messages := []string{
			strings.Repeat("m", 8<<10), strings.Repeat("m", 8<<10+1), "short",
		}
		for _, m := range messages {
			body := `{"error":"state_unavailable","message":"` + m + `"}`
			_, wireErr := credsvc.ReadWireError(http.StatusServiceUnavailable, []byte(body))
			if got, want := typed(t, http.StatusServiceUnavailable, body), wireErr == nil; got != want {
				t.Errorf("a message of %d bytes: the gateway read it as %v, the contract says %v", len(m), got, want)
			}
		}
		entry := `{"name":"cred-a","tier":"B","reason":"broken"}`
		for _, n := range []int{1, 64, 65} {
			tried := "[" + strings.TrimSuffix(strings.Repeat(entry+",", n), ",") + "]"
			body := `{"error":"state_unavailable","tried":` + tried + `}`
			_, wireErr := credsvc.ReadWireError(http.StatusServiceUnavailable, []byte(body))
			if got, want := typed(t, http.StatusServiceUnavailable, body), wireErr == nil; got != want {
				t.Errorf("a tried list of %d: the gateway read it as %v, the contract says %v", n, got, want)
			}
		}
	})

	t.Run("strictness", func(t *testing.T) {
		// Bodies both sides either read as a refusal or refuse to read.
		bodies := []string{
			`{"error":"state_unavailable"}`,
			`{"error":"state_unavailable","message":"x"}`,
			`{"error":"state_unavailable","unknown_field":1}`,
			`{"error":"state_unavailable"} {"error":"state_unavailable"}`,
			`{"error":""}`,
			`{"error":5}`,
			`[{"error":"state_unavailable"}]`,
			`not json at all`,
			`{"type":"error","error":{"type":"invalid_request_error"}}`,
		}
		for _, body := range bodies {
			// The contract's own verdict: a body it reads as a general
			// refusal is one the gateway acts on (a halt naming the type);
			// anything else is the model API's own answer, relayed.
			we, wireErr := credsvc.ReadWireError(http.StatusForbidden, []byte(body))
			contractSays := wireErr == nil && we.Error == credsvc.ErrorStateUnavailable
			h := newServiceMirror(t, func(*http.Request, []byte) anthropicfake.Reply {
				return anthropicfake.Reply{Status: http.StatusForbidden, Body: body}
			})
			status, got := postMirror(t, h)
			actedOn := status == http.StatusForbidden && strings.Contains(haltDetail(t, h), credsvc.ErrorStateUnavailable)
			if actedOn != contractSays {
				t.Errorf("body %s: the gateway acted on it as %v, the contract says %v (answer %d %s)",
					body, actedOn, contractSays, status, got)
			}
		}
	})
}

// TestServiceContractMirrorsCredsvcReadsWhatItWrites: the service's own
// writers (C1.10's) produce bodies the gateway's reader reads back as the
// same refusal, which is the direction that matters at run time.
func TestServiceContractMirrorsCredsvcReadsWhatItWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  string
		// wantActedOn is whether the gateway must act on the refusal rather
		// than halt on a body it cannot read.
		wantActedOn bool
		wire        credsvc.WireError
	}{
		{name: "state_unavailable", typ: credsvc.ErrorStateUnavailable, wantActedOn: true,
			wire: credsvc.WireError{Error: credsvc.ErrorStateUnavailable, Message: "the state could not be read"}},
		{name: "unverified_run_identity", typ: credsvc.ErrorUnverifiedRunIdentity, wantActedOn: true,
			wire: credsvc.WireError{Error: credsvc.ErrorUnverifiedRunIdentity, Message: "the run was not verified"}},
		{name: "unverified_job_identity", typ: credsvc.ErrorUnverifiedJobIdentity, wantActedOn: true,
			wire: credsvc.WireError{Error: credsvc.ErrorUnverifiedJobIdentity, Message: "the job was not verified"}},
		{name: "with a tried list", typ: credsvc.ErrorStateUnavailable, wantActedOn: true,
			wire: credsvc.WireError{Error: credsvc.ErrorStateUnavailable, Tried: []credsvc.Tried{
				{Name: "cred-a", Tier: credsvc.TierA, Reason: credsvc.ReasonExhausted},
			}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			credsvc.WriteWireError(rec, credsvc.StatusUnavailable, tc.wire)
			body := rec.Body.String()
			h := newServiceMirror(t, func(*http.Request, []byte) anthropicfake.Reply {
				return anthropicfake.Reply{Status: rec.Code, Body: body}
			})
			status, _ := postMirror(t, h)
			if status != http.StatusForbidden {
				t.Fatalf("the refusal was answered %d", status)
			}
			if got := strings.Contains(haltDetail(t, h), tc.typ); got != tc.wantActedOn {
				t.Errorf("the gateway read the service's own %s body as acted-on=%v, want %v", tc.typ, got, tc.wantActedOn)
			}
		})
	}

	t.Run("exhausted_until", func(t *testing.T) {
		// The typed answer, written by the contract's own writer, read back
		// by the gateway's mirror of it: the instant and the tried list the
		// run is told about are the ones the service wrote.
		instant := time.UnixMilli(1800000000000).UTC()
		rec := httptest.NewRecorder()
		credsvc.WriteExhaustedUntil(rec, credsvc.ExhaustedUntil{
			ResetAt: instant,
			Tried: []credsvc.Tried{
				{Name: "cred-a", Tier: credsvc.TierA, Reason: credsvc.ReasonExhausted},
				{Name: "cred-b", Tier: credsvc.TierB, Reason: credsvc.ReasonBroken},
			},
		}, time.UnixMilli(1700000000000))
		body := rec.Body.String()

		var answers []gateway.ServiceAnswer
		f, srv := anthropicfake.New(t)
		f.Func = func(*http.Request, []byte) anthropicfake.Reply {
			return anthropicfake.Reply{Status: rec.Code, Body: body}
		}
		gw, err := gateway.Start(context.Background(), gateway.Options{
			Upstream: gateway.Upstream{Kind: gateway.KindService, BaseURL: srv.URL,
				Service: &gateway.Service{
					IDToken:    func(context.Context) (string, error) { return "id-token", nil },
					RunIDToken: func() string { return "run-identity" },
					Job:        mirrorJob,
				}},
			Prices:          pricing.Embedded(),
			Mode:            gateway.Observe,
			Log:             slog.New(slog.DiscardHandler),
			OnServiceAnswer: func(a gateway.ServiceAnswer) { answers = append(answers, a) },
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		t.Cleanup(func() { _ = gw.Close(ctx) })
		gw.BeginStage(gateway.Stage{Name: "implement", Model: mirrorModel})

		status, _ := postMirror(t, gw)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("the exhausted answer was answered %d", status)
		}
		if len(answers) != 1 || answers[0].Exhausted == nil {
			t.Fatalf("the hook was told %+v, want one exhausted answer", answers)
		}
		ex := answers[0].Exhausted
		if !ex.ResetAt.Equal(instant) {
			t.Errorf("the instant read back as %v, want %v", ex.ResetAt, instant)
		}
		if len(ex.Tried) != 2 || ex.Tried[0].Name != "cred-a" || ex.Tried[0].Reason != credsvc.ReasonExhausted ||
			ex.Tried[1].Name != "cred-b" || ex.Tried[1].Tier != credsvc.TierB {
			t.Errorf("the tried list read back as %+v", ex.Tried)
		}
		if detail := haltDetail(t, gw); detail != "" {
			t.Errorf("an exhausted answer halted the run: %q", detail)
		}
	})
}
