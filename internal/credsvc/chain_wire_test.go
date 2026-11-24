// This file is the pin on the chain wire's names. They are what a run's gateway
// sends and reads and what the service answers with, and the two ends are one
// repository's code released together — so a name that changed on one side
// alone, or that collided with a name the wire already carried, has to be a
// test failure rather than a call that carries nothing or a header two caller
// classes both read.
//
// It pins the spellings and the rules and not the bridge: the release-1 bridge
// is a seam this contract declares and MG3 fills, and a test of what the stub
// answers would be a test that dies with the stub. The stub's own satisfaction
// of the interface is a compile-time check in chain_stub.go, which goes with
// it. What this file pins is what every task of the plan reads: the names, the
// stage rule, and the two rules that keep the new members off a caller that
// cannot read them and off the request side entirely.
package credsvc

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/routing"
)

// wireName is one name of the wire with what it is, so a failure says which
// name moved rather than printing two strings.
type wireName struct{ what, name string }

// TestChainWireNames holds the chain wire's names to their spellings, to each
// other, and to the two rules that make them safe to add.
func TestChainWireNames(t *testing.T) {
	// The spellings. A rename here is silent everywhere else: the gateway
	// mirrors these names in internal/gateway/chainwire.go (CS2) because
	// internal/credsvc imports internal/gateway and the dependency cannot run
	// the other way, and a name that moved on one side alone would be a header
	// no one reads — the gateway's own test holds the mirror, this one holds
	// the wire.
	for _, tc := range []struct{ what, got, want string }{
		{"the stage header", HeaderStage, "X-Fugaro-Stage"},
		{"the source header", HeaderSource, "X-Fugaro-Source"},
		{"the fallback header", HeaderFallback, "X-Fugaro-Fallback"},
		{"the served model's header", HeaderModel, "X-Fugaro-Model"},
		{"the fallback's yes", FallbackYes, "1"},
		{"the fallback's no", FallbackNo, "0"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %q, want %q: the name is what the other end reads", tc.what, tc.got, tc.want)
		}
	}
	if FallbackYes == FallbackNo {
		t.Error("the fallback member's two values are the same string; a reader could not tell a fallback call from a first-source one")
	}

	// The sides. The stage header is a REQUEST header — the caller saying which
	// step it is on — and the other three are ANSWER headers, written only to a
	// caller that sent it (R24). Keeping the two lists apart is what makes the
	// rules below checkable: no answer name may be read on a request, and the
	// request side carries no model.
	request := []wireName{
		{"the stage header", HeaderStage},
	}
	answer := []wireName{
		{"the source header", HeaderSource},
		{"the fallback header", HeaderFallback},
		{"the served model's header", HeaderModel},
	}

	// Every name the wire carried before this contract. A new header equal to
	// one of them is not a new header: it is one name two meanings, and the
	// service's answer would be read as whichever the reader knew first.
	existing := []wireName{
		{"the run identity's header", HeaderRunIdentity},
		{"the job's header", HeaderRunJob},
		{"the person identity's header", HeaderPersonIdentity},
		{"the slip's header", HeaderSlip},
		{"the claim secret's header", HeaderSlipClaim},
		{"the auth mode's header", HeaderAuthMode},
		{"the auth account's header", HeaderAuthAccount},
		{"the build's header", HeaderBuild},
		{"the tier's header", HeaderTier},
		{"the credential's header", HeaderCredential},
		{"the credential owner's header", HeaderCredentialOwner},
		{"the switch's header", HeaderSwitch},
		{"the probe's header", HeaderProbe},
		{"the provider's header", HeaderProvider},
	}

	all := append(append([]wireName{}, request...), answer...)
	for _, a := range all {
		if a.name == "" {
			t.Errorf("%s is empty; a header with no name carries nothing", a.what)
		}
		if !strings.HasPrefix(a.name, "X-Fugaro-") {
			t.Errorf("%s is %q; every name on this wire is X-Fugaro-prefixed", a.what, a.name)
		}
		for _, e := range existing {
			if a.name == e.name {
				t.Errorf("%s is %q, which is already %s; one name cannot mean two things on one wire", a.what, a.name, e.what)
			}
		}
	}
	for i, a := range all {
		for _, b := range all[i+1:] {
			if a.name == b.name {
				t.Errorf("%s and %s are both %q", a.what, b.what, a.name)
			}
		}
	}

	// The rule that the service reads the model from the request's BODY and
	// never from a header. The served model's header is an answer and belongs
	// to the answer list above; the request side has no model at all, because a
	// model a caller could name in a header would be a call whose routing is
	// chosen by whoever set the header rather than by the body the agent sent.
	for _, r := range request {
		if strings.Contains(r.name, "Model") {
			t.Errorf("%s is %q: a model may never travel on a request header; the service reads it from the body", r.what, r.name)
		}
	}
	onAnswer := false
	for _, a := range answer {
		onAnswer = onAnswer || a.name == HeaderModel
	}
	if !onAnswer {
		t.Error("the served model's header is not an answer member; it is the direction the served model travels and nothing else")
	}

	// The source member's shape: the JSON of a routing.SourceName, which is the
	// same object the run's record writes (stages[].source) and the console
	// reads. The wire carries the contract's own shape and not a second one,
	// which is why the type is declared in internal/routing and imported by
	// both sides rather than mirrored like the header names.
	src, err := json.Marshal(routing.SourceName{Kind: routing.KindProvider, Name: "openrouter"})
	if err != nil {
		t.Fatalf("marshalling a source: %v", err)
	}
	if string(src) != `{"kind":"provider","name":"openrouter"}` {
		t.Errorf("the source member is %s, want the routing.SourceName shape {\"kind\":\"provider\",\"name\":\"openrouter\"}", src)
	}
	var back routing.SourceName
	if err := json.Unmarshal(src, &back); err != nil {
		t.Fatalf("reading the source member back: %v", err)
	}
	if back.Kind != routing.KindProvider || back.Name != "openrouter" {
		t.Errorf("the source member reads back as %+v", back)
	}

	// The stage rule. It is a shape and not a closed set — the stages are the
	// runner's own names — so what it refuses is a value no stage name could
	// be, and what it admits is every name the runner writes.
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"implement", true},
		{"review", true},
		{"review_first", true},
		{"checkpoint", true},
		{"digest", true},
		{"judge", true},
		{"a", true},
		{"a-b_c-1", true},
		{strings.Repeat("a", maxStageNameBytes), true},
		{"", false},
		{strings.Repeat("a", maxStageNameBytes+1), false},
		{"Implement", false},
		{"1implement", false},
		{"-implement", false},
		{"_implement", false},
		{"a b", false},
		{"a\tb", false},
		{"a\nb", false},
		{"a\x00b", false},
		{"impl;ement", false},
		{"impl`ement", false},
		{"impl$ement", false},
	} {
		if got := ValidStageName(tc.name); got != tc.want {
			t.Errorf("ValidStageName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The rule the answer members hang on (R24, F57): a caller that sent the
	// stage header is answered in full, and one that did not — including one
	// that sent a value no stage name could be — meets exactly the answer it
	// meets today. It is the fail-closed direction: nothing about this contract
	// is reachable by sending a malformed header.
	for _, tc := range []struct {
		what string
		head http.Header
		want bool
	}{
		{"the stage header", http.Header{HeaderStage: []string{"implement"}}, true},
		{"an underscore stage", http.Header{HeaderStage: []string{"review_first"}}, true},
		{"no header at all", http.Header{}, false},
		{"an empty stage", http.Header{HeaderStage: []string{""}}, false},
		{"a stage that is not a name", http.Header{HeaderStage: []string{"impl ement"}}, false},
		{"a stage with a newline", http.Header{HeaderStage: []string{"impl\nement"}}, false},
		{"another header's value", http.Header{HeaderRunJob: []string{"implement"}}, false},
		{"the answer's own header", http.Header{HeaderSource: []string{"implement"}}, false},
	} {
		if got := SentStage(tc.head); got != tc.want {
			t.Errorf("SentStage with %s = %v, want %v", tc.what, got, tc.want)
		}
	}
}
