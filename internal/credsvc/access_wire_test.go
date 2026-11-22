package credsvc

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// TestTheAccessHeaderIsNotTheAuthHeader is (e)'s pin, and the task's red step:
// a new runner declares its craft, its task's grade and the credential it chose
// in headers of their own, beside the auth header and never in it (R5, F44).
//
// The three are new names on purpose. The old header is a statement about a
// repository's auth mode, which no longer exists (D3060); a call that carries
// the old one is an older image's and is narrowed by the old record, so a new
// call written into the old header would be read as one of those — and a
// value the two sides spell differently once is a repository served what its
// record does not name, or refused for a declaration it never made.
func TestTheAccessHeaderIsNotTheAuthHeader(t *testing.T) {
	access := []string{HeaderAccessCraft, HeaderAccessGrade, HeaderAccessChosen}
	old := []string{
		HeaderAuthMode, HeaderAuthAccount, HeaderCredential, HeaderSwitch, HeaderProbe,
		HeaderBuild, HeaderTier, HeaderRunIdentity, HeaderRunJob, HeaderSlip, HeaderSlipClaim,
	}
	for _, h := range access {
		if h == "" {
			t.Fatal("one of the access headers is empty")
		}
		if !strings.HasPrefix(h, "X-Fugaro-") {
			t.Errorf("%q is not one of the contract's own headers", h)
		}
		if slices.Contains(old, h) {
			t.Errorf("%q is already a header of another meaning: a field never changes meaning, so the access declares itself in a name of its own (R5)", h)
		}
	}
	if len(slices.Compact(slices.Sorted(slices.Values(access)))) != len(access) {
		t.Errorf("the access headers are not three distinct names: %v", access)
	}

	// The values, each held to the vocabulary its own side reads: a craft this
	// run has (config.CraftNames), a grade this project has (config.GradeNames),
	// and a credential's name — or the project's default account, which is the
	// spelling a --token run names the unsuffixed one with (D3069).
	for _, craft := range config.CraftNames {
		if !ValidAccessCraft(craft) {
			t.Errorf("ValidAccessCraft(%q) = false, want true", craft)
		}
	}
	for _, grade := range config.GradeNames {
		if !ValidAccessGrade(grade) {
			t.Errorf("ValidAccessGrade(%q) = false, want true", grade)
		}
	}
	for _, in := range []string{"alice", config.CredentialAnthropicKey} {
		if !ValidAccessChosen(in) {
			t.Errorf("ValidAccessChosen(%q) = false, want true", in)
		}
	}
	if !ValidAccessChosen(config.DefaultClaudeAccount) {
		t.Errorf("ValidAccessChosen(%q) = false, want true: a --token run names the unsuffixed account this way", config.DefaultClaudeAccount)
	}
	for _, bad := range []string{"", "not-a-craft", "Coder", "coder "} {
		if ValidAccessCraft(bad) {
			t.Errorf("ValidAccessCraft(%q) = true, want false", bad)
		}
	}
	for _, bad := range []string{"", "not-a-grade", "Strong"} {
		if ValidAccessGrade(bad) {
			t.Errorf("ValidAccessGrade(%q) = true, want false", bad)
		}
	}
	for _, bad := range []string{"", "two words", "a\nb", strings.Repeat("x", maxCredentialNameBytes+1)} {
		if ValidAccessChosen(bad) {
			t.Errorf("ValidAccessChosen(%q) = true, want false", bad)
		}
	}
}

// TestTheGatewayAndTheServiceSpellTheHeaderAlike holds the two copies of the
// names to each other. internal/gateway cannot import this package (it is the
// loopback proxy the run's own calls go through, and this package is the
// service on the other side of the wire), so the name is spelled twice, and the
// test lives here because this is the side that may import both.
func TestTheGatewayAndTheServiceSpellTheHeaderAlike(t *testing.T) {
	pairs := []struct {
		service, gateway string
	}{
		{HeaderAccessCraft, gateway.HeaderAccessCraft},
		{HeaderAccessGrade, gateway.HeaderAccessGrade},
		{HeaderAccessChosen, gateway.HeaderAccessChosen},
	}
	for _, p := range pairs {
		if p.service != p.gateway {
			t.Errorf("the service spells the header %q and the gateway %q", p.service, p.gateway)
		}
	}
}
