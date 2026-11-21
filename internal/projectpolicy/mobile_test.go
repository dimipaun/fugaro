package projectpolicy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// mobileNode is one published policy carrying the given mobile block, which is
// the whole of what these tests are about: the block is a top-level key of the
// node the runs and the pairing route read, so every fixture here is a node and
// not a bare block.
func mobileNode(block string) []byte {
	return []byte(`{"version": 10, "budget": {"mode": "observe"}, "mobile": ` + block + `}`)
}

// TestProjectPolicyMobileDefaults pins the value `fugaro update` publishes when
// it writes the block (O9): every member is written, and each carries the
// default the plan's P6 states. The default on is a member and not an absence,
// because the block's own presence is what turns the surface on — a node that
// was published before this key existed carries no block at all, and reads as
// off (TestProjectPolicyMobileAbsentBlockIsOff).
func TestProjectPolicyMobileDefaults(t *testing.T) {
	m := DefaultMobile()
	if m == nil {
		t.Fatal("DefaultMobile() = nil, want the block update publishes")
	}
	if m.Enabled == nil || !*m.Enabled {
		t.Errorf("DefaultMobile().Enabled = %v, want true: a project update publishes is a project whose phones may pair", m.Enabled)
	}
	if m.Sync == nil || !*m.Sync {
		t.Errorf("DefaultMobile().Sync = %v, want true", m.Sync)
	}
	if want := []string{ProviderGoogle, ProviderPassword, ProviderMicrosoft, ProviderApple}; !slices.Equal(m.AcceptedProviders, want) {
		t.Errorf("DefaultMobile().AcceptedProviders = %v, want %v (D2785)", m.AcceptedProviders, want)
	}
	if m.MaxAuthAge != DefaultMobileMaxAuthAgeText {
		t.Errorf("DefaultMobile().MaxAuthAge = %q, want %q (O4)", m.MaxAuthAge, DefaultMobileMaxAuthAgeText)
	}
	if m.StrictNewDevice == nil || *m.StrictNewDevice {
		t.Errorf("DefaultMobile().StrictNewDevice = %v, want a written false (D2784: restore is silent unless the project asks otherwise)", m.StrictNewDevice)
	}
	if want := []rigor.Rigor{rigor.Pro, rigor.Critical}; !slices.Equal(m.CostlyModes, want) {
		t.Errorf("DefaultMobile().CostlyModes = %v, want %v (D2786)", m.CostlyModes, want)
	}
	if m.BiometricOnCostly == nil || *m.BiometricOnCostly {
		t.Errorf("DefaultMobile().BiometricOnCostly = %v, want a written false (D2786)", m.BiometricOnCostly)
	}

	// The spelling of the age and the duration it means are one value in two
	// places: the block writes the text and the accessor answers the duration.
	age, err := config.ParseDuration(DefaultMobileMaxAuthAgeText)
	if err != nil {
		t.Fatalf("DefaultMobileMaxAuthAgeText %q is not a duration: %v", DefaultMobileMaxAuthAgeText, err)
	}
	if age != DefaultMobileMaxAuthAge {
		t.Errorf("the text %q parses to %s and DefaultMobileMaxAuthAge is %s: the member update writes and the default a node without it reads must be one value",
			DefaultMobileMaxAuthAgeText, age, DefaultMobileMaxAuthAge)
	}
}

// TestProjectPolicyMobileDefaultsRoundTrip: what update publishes is what every
// reader reads back. The block is marshalled the way the publisher writes it,
// and the node is decoded: the members must be this Fugaro's own names (a
// misspelt one would be an unknown field), and every accessor must answer the
// default it was written with.
func TestProjectPolicyMobileDefaultsRoundTrip(t *testing.T) {
	block, err := json.Marshal(DefaultMobile())
	if err != nil {
		t.Fatalf("marshalling the default block: %v", err)
	}
	names, err := objectMemberNames(block)
	if err != nil {
		t.Fatalf("the block update writes is not an object: %v", err)
	}
	slices.Sort(names)
	if want := []string{"accepted_providers", "biometric_on_costly", "costly_modes", "enabled", "max_auth_age", "strict_new_device", "sync"}; !slices.Equal(names, want) {
		t.Errorf("the published block names %v, want %v: the plan's P6 acceptance is that the node carries all seven", names, want)
	}
	p, found, err := Decode(mobileNode(string(block)))
	if err != nil || !found || p == nil {
		t.Fatalf("the block update writes does not decode = (%v, %v, %v)", p, found, err)
	}
	if !p.MobileEnabled() || !p.MobileSync() {
		t.Errorf("the published default reads back as enabled=%v sync=%v, want true and true", p.MobileEnabled(), p.MobileSync())
	}
	if got := p.MobileAcceptedProviders(); !slices.Equal(got, []string{ProviderGoogle, ProviderPassword, ProviderMicrosoft, ProviderApple}) {
		t.Errorf("MobileAcceptedProviders() = %v", got)
	}
	if got := p.MobileMaxAuthAge(); got != 24*time.Hour {
		t.Errorf("MobileMaxAuthAge() = %s, want 24h", got)
	}
	if p.MobileStrictNewDevice() || p.MobileBiometricOnCostly() {
		t.Errorf("the published default reads back strict=%v biometric=%v, want both false", p.MobileStrictNewDevice(), p.MobileBiometricOnCostly())
	}
	if got := p.MobileCostlyModes(); !slices.Equal(got, []rigor.Rigor{rigor.Pro, rigor.Critical}) {
		t.Errorf("MobileCostlyModes() = %v", got)
	}
}

// TestProjectPolicyMobileAbsentBlockIsOff is the red step's second half and the
// global constraint's own row: a policy published before the mobile key existed
// reads mobile.enabled as off, never as the default on. Deployment alone
// switches nothing on (F40); what switches it on is update publishing the block
// (O9).
func TestProjectPolicyMobileAbsentBlockIsOff(t *testing.T) {
	for _, tc := range []struct{ name, node string }{
		{name: "no member at all", node: `{"version": 10, "budget": {"mode": "observe"}}`},
		{name: "a written null", node: `{"version": 10, "mobile": null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(tc.node))
			if err != nil || !found || p == nil {
				t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
			}
			if p.Mobile != nil {
				t.Errorf("Mobile = %+v, want nil: the key is absent, and a written null is the absent key here", p.Mobile)
			}
			if p.MobileEnabled() {
				t.Error("MobileEnabled() = true for a node with no mobile block, want false: deployment alone switches nothing on (F40, O9)")
			}
			if p.MobileSync() {
				t.Error("MobileSync() = true for a node with no mobile block, want false: nothing is stored in the identity project")
			}
		})
	}
	// A policy the caller does not hold at all answers the same way, which is
	// what lets the pairing route ask before it has read anything.
	var none *Policy
	if none.MobileEnabled() || none.MobileSync() {
		t.Error("a nil Policy reads as enabled: the pairing route must refuse with mobile_disabled, not admit")
	}
}

// TestProjectPolicyMobileReadsTheSwitches is the plan's red step: reading
// mobile.strict_new_device from a published policy fails until the typed struct
// exists. Every member is read here, because the one that fails to compile is
// only the first of the seven a reader needs.
func TestProjectPolicyMobileReadsTheSwitches(t *testing.T) {
	p, found, err := Decode(mobileNode(`{
		"enabled": true,
		"sync": true,
		"accepted_providers": ["google.com", "microsoft.com"],
		"max_auth_age": "6h",
		"strict_new_device": true,
		"costly_modes": ["critical"],
		"biometric_on_costly": true
	}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if p.Mobile == nil {
		t.Fatal("Mobile = nil, want the block the node carries")
	}
	if p.Mobile.StrictNewDevice == nil || !*p.Mobile.StrictNewDevice {
		t.Errorf("Mobile.StrictNewDevice = %v, want a written true", p.Mobile.StrictNewDevice)
	}
	if !p.MobileStrictNewDevice() {
		t.Error("MobileStrictNewDevice() = false, want true: the member the node writes decides it")
	}
	if !p.MobileBiometricOnCostly() {
		t.Error("MobileBiometricOnCostly() = false, want true")
	}
	if got := p.MobileMaxAuthAge(); got != 6*time.Hour {
		t.Errorf("MobileMaxAuthAge() = %s, want 6h", got)
	}
	if got := p.MobileAcceptedProviders(); !slices.Equal(got, []string{ProviderGoogle, ProviderMicrosoft}) {
		t.Errorf("MobileAcceptedProviders() = %v, want the two the node names", got)
	}
	if got := p.MobileCostlyModes(); !slices.Equal(got, []rigor.Rigor{rigor.Critical}) {
		t.Errorf("MobileCostlyModes() = %v, want [critical]", got)
	}
	// The list a reader matches a name against is the node's own, never a copy
	// of the default it could write into.
	got := p.MobileAcceptedProviders()
	got[0] = "example.com"
	if again := p.MobileAcceptedProviders(); !slices.Equal(again, []string{ProviderGoogle, ProviderMicrosoft}) {
		t.Errorf("MobileAcceptedProviders() = %v after a caller edited the previous answer: the reader hands out a copy", again)
	}
}

// TestProjectPolicyMobileAbsentMembersTakeTheDefaults: a written block whose
// members are absent reads them at their defaults — the two switches on, the
// two costs off, the provider list, the age and the costly modes as D2784,
// D2785, D2786 and O4 state them. The block's presence is the one statement
// that turns the surface on, so an operator who writes `mobile: {}` (or an
// update that publishes a block with a member added later) gets the product's
// defaults and not an off switch.
func TestProjectPolicyMobileAbsentMembersTakeTheDefaults(t *testing.T) {
	p, _, err := Decode(mobileNode(`{}`))
	if err != nil || p == nil || p.Mobile == nil {
		t.Fatalf("Decode = (%v, %v), want the block read", p, err)
	}
	if !p.MobileEnabled() || !p.MobileSync() {
		t.Errorf("an empty block reads enabled=%v sync=%v, want true and true", p.MobileEnabled(), p.MobileSync())
	}
	if p.MobileStrictNewDevice() || p.MobileBiometricOnCostly() {
		t.Errorf("an empty block reads strict=%v biometric=%v, want both false", p.MobileStrictNewDevice(), p.MobileBiometricOnCostly())
	}
	if got := p.MobileAcceptedProviders(); !slices.Equal(got, []string{ProviderGoogle, ProviderPassword, ProviderMicrosoft, ProviderApple}) {
		t.Errorf("MobileAcceptedProviders() = %v, want the default four", got)
	}
	if got := p.MobileMaxAuthAge(); got != 24*time.Hour {
		t.Errorf("MobileMaxAuthAge() = %s, want 24h", got)
	}
	if got := p.MobileCostlyModes(); !slices.Equal(got, []rigor.Rigor{rigor.Pro, rigor.Critical}) {
		t.Errorf("MobileCostlyModes() = %v, want [pro critical]", got)
	}
	// A written false is a decision and never the absent member, which is what
	// the pointers are for: an operator turning one switch off must be able to.
	off, _, err := Decode(mobileNode(`{"enabled": false, "sync": false}`))
	if err != nil || off == nil {
		t.Fatalf("Decode = (%v, %v)", off, err)
	}
	if off.MobileEnabled() || off.MobileSync() {
		t.Errorf("a written false reads enabled=%v sync=%v, want both false", off.MobileEnabled(), off.MobileSync())
	}
}

// TestProjectPolicyMobileToleratedByAReaderThatPredatesIt is the
// previous-version-converges test the plan asks for. The mobile key is
// additive and optional, so no policy Version moves for it (version.go): what
// moves a version is a runner having to enforce something it does not know, and
// no runner enforces this block — the readers are the pairing route and the
// app's session. A node carrying it is decoded twice, once under this Fugaro's
// keys and once under a copy of the list as it stood before the key, and the
// reader that predates it lists the key and reads the typed fields it knows
// (P8), which is what keeps an update safe with runs in flight
// (docs/design/update-in-flight.md).
func TestProjectPolicyMobileToleratedByAReaderThatPredatesIt(t *testing.T) {
	if Version != 10 {
		t.Fatalf("the policy Version is %d, and this test's node is written for 10: update both together, deliberately", Version)
	}
	raw := mobileNode(`{"enabled": true, "strict_new_device": true, "max_auth_age": "6h"}`)
	p, found, err := Decode(raw)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: mobile is a key this Fugaro reads", p.UnknownKeys)
	}
	if !p.MobileStrictNewDevice() {
		t.Error("the node was not read whole: strict_new_device is false")
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "mobile")
	q, found, err := decodeWithKeys(raw, preKey)
	if err != nil || !found {
		t.Fatalf("the reader that predates the key refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"mobile"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v: that is the node a CLI of that age refuses at publish time and a runner of it logs", q.UnknownKeys, want)
	}
	if q.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe: the rest of the policy stays in force beside a key the reader does not know", q.Budget.Mode)
	}
	// The seam models the older reader's *key list* and not its body, so the
	// block it does not know is still decoded here (decodeWithKeys' own note);
	// what the reader of that age does with the node is `update`'s business and
	// not this test's. The row the global constraint states — a policy
	// published before the key existed reads mobile.enabled as off — is the
	// node with no block at all, which
	// TestProjectPolicyMobileAbsentBlockIsOff reads.
}

// TestProjectPolicyMobileDecodesStrictly: the block is a closed set of seven
// members, so a misspelt one is refused rather than silently setting nothing.
// The failure mode is the one this package refuses everywhere: an operator who
// writes `strict_new_device` as `strict_device` would have a project that looks
// strict and restores silently, and the two are indistinguishable in a decoded
// node.
func TestProjectPolicyMobileDecodesStrictly(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{name: "a member the block does not have", block: `{"strict_device": true}`, want: `unknown field "strict_device"`},
		{name: "a member in another case", block: `{"Enabled": true}`, want: `spells enabled as "Enabled"`},
		{name: "a member written twice", block: `{"sync": true, "sync": false}`, want: `names "sync" twice`},
		{name: "a block that is not an object", block: `true`, want: "not an object"},
		{name: "an enabled that is not a bool", block: `{"enabled": "yes"}`, want: "cannot unmarshal"},
		{name: "an age that is not a duration", block: `{"max_auth_age": "24 hours"}`, want: `invalid duration "24 hours"`},
		{name: "a zero age", block: `{"max_auth_age": "0s"}`, want: "must be positive"},
		{name: "a negative age", block: `{"max_auth_age": "-1h"}`, want: "must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := Decode(mobileNode(tc.block))
			if err == nil {
				t.Fatalf("the block %s was read: %+v", tc.block, p.Mobile)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal of %s is %q, want it to carry %q", tc.block, err, tc.want)
			}
		})
	}
}

// TestProjectPolicyMobileRefusesAnEntryItCannotResolve: both lists name values
// this Fugaro holds a vocabulary for — the identity providers and the rigor
// modes — and an entry outside it is refused at decode rather than at the route
// that would have to match it. An accepted provider is a name, never a URL: the
// same rule the voice list is held to, because a URL here would be a
// policy-carried redirection of the assertion to an issuer nobody chose.
func TestProjectPolicyMobileRefusesAnEntryItCannotResolve(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{name: "a provider that is not one", block: `{"accepted_providers": ["github.com"]}`, want: "not an identity provider"},
		{name: "a provider named as a URL", block: `{"accepted_providers": ["https://accounts.google.com"]}`, want: "not an identity provider"},
		{name: "a provider in another case", block: `{"accepted_providers": ["Google.com"]}`, want: "not an identity provider"},
		{name: "a provider named twice", block: `{"accepted_providers": ["google.com", "google.com"]}`, want: "already names"},
		{name: "a mode that is not one", block: `{"costly_modes": ["prod"]}`, want: "not a rigor mode"},
		{name: "a mode in another case", block: `{"costly_modes": ["PRO"]}`, want: "not a rigor mode"},
		{name: "a mode named twice", block: `{"costly_modes": ["pro", "pro"]}`, want: "already names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := Decode(mobileNode(tc.block))
			if err == nil {
				t.Fatalf("the block %s was read: %+v", tc.block, p.Mobile)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal of %s is %q, want it to carry %q", tc.block, err, tc.want)
			}
		})
	}
	// Hotfix is a mode this Fugaro knows and off the axis, and a project may
	// name it: the read-back is the project's own cost decision, not this
	// reader's.
	p, _, err := Decode(mobileNode(`{"costly_modes": ["hotfix"]}`))
	if err != nil || p == nil {
		t.Fatalf("Decode = (%v, %v), want a project that names hotfix to be read", p, err)
	}
	if got := p.MobileCostlyModes(); !slices.Equal(got, []rigor.Rigor{rigor.Hotfix}) {
		t.Errorf("MobileCostlyModes() = %v, want [hotfix]", got)
	}
}

// TestProjectPolicyMobileProviderNamesAreTheIdentityProviders: the four names
// are the spellings Firebase Auth uses for its providers, which is the
// vocabulary P2's invite and P3's assertion carry — a project's list is matched
// against an assertion's `provider` claim, so the two have to be one set of
// strings and not two.
func TestProjectPolicyMobileProviderNamesAreTheIdentityProviders(t *testing.T) {
	if got, want := mobileProviderNames(), []string{"google.com", "password", "microsoft.com", "apple.com"}; !slices.Equal(got, want) {
		t.Errorf("the provider vocabulary is %v, want %v: the assertion's provider claim is matched against it", got, want)
	}
	for _, name := range mobileProviderNames() {
		if err := validMobileProviders([]string{name}); err != nil {
			t.Errorf("validMobileProviders(%q) = %v, want the vocabulary to accept its own names", name, err)
		}
	}
}
