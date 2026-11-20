// This file is the pin on the provider wire's mirror. The names a routed
// provider call is made of are declared twice — in internal/credsvc, which
// spells them, and in internal/gateway/providerwire.go, which mirrors them
// because internal/credsvc imports internal/gateway and the dependency cannot
// run the other way (F26) — and this file is what keeps the two honest: it is
// an external test file (package gateway_test, which may import both), so it
// holds one spelling against the other and holds every name of the provider
// wire apart from the names of the wire that was already there.
//
// It is the shape service_contract_test.go already uses for the model wire's
// names, and the reason is the same: the two ends of this wire are one
// repository's code released together, so a name that changed on one side
// alone has to be a test failure rather than a call that carries nothing.
package gateway_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// TestProviderWireNamesAreDistinct holds the gateway's provider wire to
// credsvc's: the same values, neither empty, and each one a name the wire did
// not already carry.
//
// The last part is the one this test is named for, and it is not decoration:
// the provider route reaches the SAME service as the model wire reaches, so a
// path or a header that collided with one of the model wire's would be one
// name serving two caller classes — the thing the route's own prefix exists to
// prevent — and a refusal type that collided would be a refusal the runner
// cannot classify.
func TestProviderWireNamesAreDistinct(t *testing.T) {
	mirror := []struct {
		what          string
		gateway, wire string
	}{
		{"the provider's header", gateway.HeaderProvider, credsvc.HeaderProvider},
		{"the route's prefix", gateway.ProviderRoutePrefix, credsvc.ProviderRoutePrefix},
		{"the route's messages path", gateway.ProviderRoutePath, credsvc.ProviderRoutePath},
		{"the route's counting path", gateway.ProviderCountTokensPath, credsvc.ProviderCountTokensPath},
		{"the not-served refusal", gateway.ErrorProviderNotServed, credsvc.ErrorProviderNotServed},
		{"the not-allowed refusal", gateway.ErrorProviderNotAllowed, credsvc.ErrorProviderNotAllowed},
	}
	for _, m := range mirror {
		if m.gateway == "" {
			t.Errorf("%s is empty in the gateway; the mirror declares it and the service reads it", m.what)
		}
		if m.gateway != m.wire {
			t.Errorf("%s is %q in the gateway and %q on the wire; the two are one name (F26)", m.what, m.gateway, m.wire)
		}
	}

	// The route's shape: the prefix and then the provider's own path, which is
	// the model wire's own path because a provider is an endpoint that takes
	// Anthropic Messages requests. A route that spelled the path another way
	// would forward to an address no provider answers, and a route that served
	// the messages path without its counting sibling would answer a routed
	// model's count_tokens with a 404.
	if want := gateway.ProviderRoutePrefix + credsvc.MessagesPath; gateway.ProviderRoutePath != want {
		t.Errorf("the messages path is %q, want %q", gateway.ProviderRoutePath, want)
	}
	if want := gateway.ProviderRoutePrefix + credsvc.CountTokensPath; gateway.ProviderCountTokensPath != want {
		t.Errorf("the counting path is %q, want %q", gateway.ProviderCountTokensPath, want)
	}

	// Every name the wire carried before this contract: the headers the
	// gateway sends on a service call, the paths the service serves and the
	// refusals and tried reasons the runner classifies. A provider name that
	// equals one of them is not a new name at all.
	existing := []struct{ what, value string }{
		{"the run identity's header", gateway.HeaderRunIdentity},
		{"the job's header", gateway.HeaderRunJob},
		{"the slip's header", gateway.HeaderSlip},
		{"the claim secret's header", gateway.HeaderSlipClaim},
		{"the auth mode's header", gateway.HeaderAuthMode},
		{"the auth account's header", gateway.HeaderAuthAccount},
		{"the build's header", credsvc.HeaderBuild},
		{"the tier's header", gateway.HeaderTier},
		{"the credential's header", gateway.HeaderCredential},
		{"the switch's header", gateway.HeaderSwitch},
		{"the probe's header", gateway.HeaderProbe},
		{"the messages path", credsvc.MessagesPath},
		{"the counting path", credsvc.CountTokensPath},
		{"the sign path", credsvc.SignPath},
		{"the verify path", credsvc.VerifyPath},
		{"the mint path", credsvc.MintPath},
		{"the context tokens path", credsvc.ContextTokenPath},
		{"the Bitbucket read path", credsvc.BitbucketReadPath},
		{"the GitHub read path", credsvc.GitHubReadPath},
		{"the merge mint path", credsvc.MergeMintPath},
		{"the label path", credsvc.LabelPath},
		{"the issue path", credsvc.IssuePath},
		{"the slip path", credsvc.SlipPath},
		{"the slip claim path", credsvc.SlipClaimPath},
		{"the workflow secrets path", credsvc.WorkflowSecretsPath},
		{"the build token path", credsvc.BuildTokenPath},
		{"the build credentials path", credsvc.BuildCredentialsPath},
		{"the Bitbucket proxy prefix", credsvc.BitbucketProxyPrefix},
		{"the exhausted-until refusal", gateway.ErrorExhaustedUntil},
		{"the unverified run identity refusal", gateway.ErrorUnverifiedRunIdentity},
		{"the unverified job identity refusal", gateway.ErrorUnverifiedJobIdentity},
		{"the state-unavailable refusal", gateway.ErrorStateUnavailable},
		{"the request-refused refusal", gateway.ErrorRequestRefused},
		{"the no-candidate-credentials refusal", gateway.ErrorNoCandidateCredentials},
		{"the exhausted reason", gateway.ReasonExhausted},
		{"the broken reason", gateway.ReasonBroken},
		{"the not-admitted reason", gateway.ReasonNotAdmitted},
		{"the stopped reason", gateway.ReasonStopped},
	}
	for i, a := range mirror {
		for _, b := range mirror[i+1:] {
			if a.gateway == b.gateway {
				t.Errorf("%s and %s are both %q; one name cannot mean two things on one wire", a.what, b.what, a.gateway)
			}
		}
		for _, e := range existing {
			if a.gateway == e.value {
				t.Errorf("%s is %q, which is already %s; the provider route reaches the same service and the two names would collide", a.what, a.gateway, e.what)
			}
		}
	}

	// The rule for the header's value is one rule over three packages: the
	// wire holds a name to it, the service derives the entry from it, and the
	// local config is the document the name comes from in the first place
	// (config's own providerNameRE is unexported, so this holds the two by
	// their answers and not by their text). A name either side admitted and
	// the other refused would be a run refused on a name's shape — or an entry
	// named after a provider the config does not carry.
	names := []string{
		"a",
		"a" + strings.Repeat("b", 19), // the longest the rule admits
		"openrouter",
		"provider-key--project",
		"anthropic-api-key",
		"",
		"a" + strings.Repeat("b", 20), // one byte past it
		"A",
		"1provider",
		"-provider",
		"open_router",
		"provider-key--",
	}
	for _, name := range names {
		wire, service := gateway.ValidProviderName(name), credsvc.ValidProviderName(name)
		if wire != service {
			t.Errorf("ValidProviderName(%q) is %v in the gateway and %v on the wire", name, wire, service)
		}
		if refused := configRefusesProviderName(name); refused == wire {
			t.Errorf("the wire admits a provider named %q: %v, and the local config refused it: %v; the name a run routes by and the name an entry is built from are one rule", name, wire, refused)
		}
	}
}

// configRefusesProviderName answers whether the local config refuses name as a
// providers block key, asked through ValidateModelProviders on an entry that is
// otherwise valid: what is read is the rule's own problem and not the problems
// another field of the entry raises, so the call says what the name rule says.
func configRefusesProviderName(name string) bool {
	valid := config.ModelProvider{
		Kind:    config.ModelProviderKind,
		BaseURL: "https://provider.example.com",
		Auth:    "bearer",
		Secret:  "openrouter-key",
	}
	for _, p := range config.ValidateModelProviders(map[string]config.ModelProvider{name: valid}) {
		if strings.Contains(p.Message, "provider name must match") {
			return true
		}
	}
	return false
}
