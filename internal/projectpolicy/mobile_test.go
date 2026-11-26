package projectpolicy

import (
	"encoding/json"
	"reflect"
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
	return []byte(`{"version": 11, "budget": {"mode": "observe"}, "mobile": ` + block + `}`)
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
	// The app's crash-report switch is NOT a member of this block: it is a
	// top-level key beside it, because a member inside the block would be
	// refused by every reader of the release behind (the block decodes
	// strictly, Policy.MobileCrashReports's comment). Its own default is on all
	// the same, and TestProjectPolicyMobileCrashReportsHoldsTheSwitch is where
	// the key and its reader are held (D731, D738).
	if !DefaultMobileCrashReports {
		t.Error("DefaultMobileCrashReports = false, want true (D731, D738: crash reports are on by default and opt-out)")
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
		t.Errorf("the published block names %v, want %v: the plan's P6 acceptance is that the node carries every member the block has, and the app's crash-report switch is a top-level key beside it and not one of them (D731, D738)", names, want)
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
	// A node a publish writes carries the crash-report key true as well, and it
	// is read back through the key's own reader: the block above is what the
	// publish fills member by member, and the switch is the key beside it
	// (MobileCrashReportsForPublish).
	whole, err := json.Marshal(&Policy{Version: Version, Budget: Budget{Mode: "observe"}, Mobile: DefaultMobile(), MobileCrashReports: MobileCrashReportsForPublish(nil)})
	if err != nil {
		t.Fatalf("marshalling the published node: %v", err)
	}
	if !strings.Contains(string(whole), `"`+MobileCrashReportsKey+`":true`) {
		t.Errorf("the node a publish writes does not carry %s:true:\n%s", MobileCrashReportsKey, whole)
	}
	q, found, err := Decode(whole)
	if err != nil || !found || q == nil {
		t.Fatalf("the node a publish writes does not decode = (%v, %v, %v)", q, found, err)
	}
	if !q.MobileCrashReportsOn() {
		t.Error("MobileCrashReportsOn() = false for the node a publish writes, want true: the switch a publish writes is the default on (D731, D738)")
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
// only the first of the seven a reader needs. The app's crash-report switch is
// not one of them and is read from its own node below
// (TestProjectPolicyMobileCrashReportsHoldsTheSwitch).
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
//
// The app's crash-report switch is not a member of this block and is not here: a
// key that is not there answers off (TestProjectPolicyMobileCrashReportsHoldsTheSwitch),
// which is the block's own reading the other way round, because nothing may be
// collected before the switch has been read (D734).
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

// TestProjectPolicyMobileCrashReportsHoldsTheSwitch is D731 and D738's switch
// where it lives: a **top-level key of the node**, `mobile_crash_reports`,
// beside the mobile block and never inside it, read by MobileCrashReportsOn.
//
// It is beside the block and not a member of it because the block decodes
// strictly: a member added inside it makes every reader of the release behind
// refuse the WHOLE policy, and the node is re-read while runs are in flight —
// which is what `update` promises never to do (docs/design/update-in-flight.md,
// Policy.MobileCrashReports's comment). TestProjectPolicyMobileDecodesStrictly
// is the other half of the same fact: the block refuses a member it does not
// have, so a reader of the release behind is exactly the reader that would
// refuse `crash_reports` inside it.
//
// The reading rules, each of them a way the switch can go wrong:
//
//   - a key that is not there is **off**, never the default on — a session a
//     release behind assembled tells a phone nothing, and nothing may be
//     collected before the switch has been read (D734);
//   - a written false is a decision and reads off (D731's opt-out);
//   - a value that is not a bool is refused rather than read as anything;
//   - a nil Policy answers off, because the pairing route asks before it has
//     read anything;
//   - the value a publish writes for a node without the key is on
//     (MobileCrashReportsForPublish), which is the whole of what makes the
//     switch reach an installation that published before it existed.
func TestProjectPolicyMobileCrashReportsHoldsTheSwitch(t *testing.T) {
	if MobileCrashReportsKey != "mobile_crash_reports" {
		t.Fatalf("MobileCrashReportsKey = %q: the key's spelling is the node's, and this test writes nodes with it", MobileCrashReportsKey)
	}
	crashNode := func(key string) []byte {
		return []byte(`{"version": 10, "budget": {"mode": "observe"}, "mobile": {"enabled": true}, "mobile_crash_reports": ` + key + `}`)
	}
	for _, tc := range []struct {
		name string
		key  string
		want bool
	}{
		{name: "written true", key: "true", want: true},
		{name: "written false", key: "false", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(crashNode(tc.key))
			if err != nil || !found || p == nil {
				t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
			}
			if len(p.UnknownKeys) != 0 {
				t.Errorf("UnknownKeys = %v, want none: the key is this Fugaro's own", p.UnknownKeys)
			}
			if got := p.MobileCrashReportsOn(); got != tc.want {
				t.Errorf("MobileCrashReportsOn() = %v for a key written %s, want %v", got, tc.key, tc.want)
			}
		})
	}
	// A node that carries no key at all, and one whose value is null: both are
	// the absent switch, and both answer off.
	for _, node := range []string{
		`{"version": 10, "budget": {"mode": "observe"}, "mobile": {"enabled": true}}`,
		`{"version": 10, "budget": {"mode": "observe"}, "mobile": {"enabled": true}, "mobile_crash_reports": null}`,
	} {
		p, found, err := Decode([]byte(node))
		if err != nil || !found || p == nil {
			t.Fatalf("Decode(%s) = (%v, %v, %v), want the node read", node, p, found, err)
		}
		if p.MobileCrashReports != nil {
			t.Errorf("the switch of %s decoded to %v, want nil: neither state is an answer", node, p.MobileCrashReports)
		}
		if p.MobileCrashReportsOn() {
			t.Errorf("MobileCrashReportsOn() = true for %s, want false: nothing is sent before the switch has been read (D731, D734, D738)", node)
		}
	}
	// The pairing route asks before it has read anything.
	var none *Policy
	if none.MobileCrashReportsOn() {
		t.Error("MobileCrashReportsOn() = true for a nil Policy, want false: the pairing route asks before it has read anything")
	}
	// A value that is not a bool is refused: the switch decides whether anything
	// is sent, and a string that happens to be truthy is not an answer.
	if p, _, err := Decode(crashNode(`"yes"`)); err == nil {
		t.Fatalf("a key written as a string was read: %+v", p.MobileCrashReports)
	}
	// The fill: the value a publish writes for a node without the key is the
	// default on, and a node's own value is written back as it stands.
	if got := MobileCrashReportsForPublish(nil); got == nil || !*got {
		t.Errorf("MobileCrashReportsForPublish(nil) = %v, want a written true: the reader answers an absent key off, so the fill is the only thing that puts the default on the node", got)
	}
	no := false
	if got := MobileCrashReportsForPublish(&no); got != &no {
		t.Errorf("MobileCrashReportsForPublish(a written false) = %v, want the node's own false: a publish keeps the project's answer (O13)", got)
	}
}

// TestProjectPolicyMobileCrashReportsToleratedByAReaderThatPredatesIt is the
// test the review of T3.1 asked for, and it is the reason the switch is a
// top-level key: **the node a publish writes decodes with the release behind's
// key set and its member set**, so a run in flight on the previous image — and
// a CLI of the previous release — reads the whole policy it is served after one
// `fugaro update` (docs/design/update-in-flight.md).
//
// The two sets are named here rather than taken from this binary: the previous
// release's mobile member set is written out as the seven names it knew, and its
// key set is this Fugaro's own list with the switch's key removed — which is
// exactly what a reader of that age carries. The node is built the way a publish
// builds one (MobileForPublish and MobileCrashReportsForPublish, the two
// functions internal/cli's buildProjectPolicy calls), so the claim is about the
// node the update really writes and not about a fixture.
func TestProjectPolicyMobileCrashReportsToleratedByAReaderThatPredatesIt(t *testing.T) {
	// The mobile members the release behind decodes, as that release spelled
	// them (its own closed set, which had no crash_reports). A member added to
	// the block since would be refused by its decodeMobile, so the block of the
	// node a publish writes must name no other.
	previousMembers := []string{
		"accepted_providers", "biometric_on_costly", "costly_modes", "enabled",
		"max_auth_age", "strict_new_device", "sync",
	}
	raw, err := json.Marshal(&Policy{
		Version:            Version,
		Budget:             Budget{Mode: "observe"},
		Mobile:             MobileForPublish(nil),
		MobileCrashReports: MobileCrashReportsForPublish(nil),
	})
	if err != nil {
		t.Fatalf("marshalling the node a publish writes: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node a publish writes is not a document: %v", err)
	}
	block, ok := doc["mobile"]
	if !ok {
		t.Fatal("the node a publish writes carries no mobile block")
	}
	names, err := objectMemberNames(block)
	if err != nil {
		t.Fatalf("the block is not an object: %v", err)
	}
	slices.Sort(names)
	if !slices.Equal(names, previousMembers) {
		t.Errorf("the mobile block a publish writes names %v, want %v: a member beside them would be refused by the release behind's decodeMobile, which returns that error for the whole policy", names, previousMembers)
	}
	if err := checkMemberNames("mobile", names, mobileMembers); err != nil {
		t.Errorf("the block a publish writes is refused by this Fugaro's own member set as well: %v", err)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, MobileCrashReportsKey)
	q, found, err := decodeWithKeys(raw, preKey)
	if err != nil || !found || q == nil {
		t.Fatalf("the reader that predates the key refused the node a publish writes: (%v, %v, %v)", q, found, err)
	}
	if want := []string{MobileCrashReportsKey}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v: that is the node a CLI of that age refuses at publish time and a runner of it logs", q.UnknownKeys, want)
	}
	// The rest of the policy stays in force beside a key the reader does not
	// know, which is the whole point of P8: the run keeps running.
	if !q.MobileEnabled() {
		t.Error("the reader that predates the key reads the mobile block as disabled: the block it knows is there, and only the new key is unknown")
	}
	if q.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe: the rest of the policy stays in force beside a key the reader does not know", q.Budget.Mode)
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
	if Version != 11 {
		t.Fatalf("the policy Version is %d, and this test's node is written for 11: update both together, deliberately", Version)
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

// TestProjectPolicyMobileDecodesStrictly: the block is a closed set of the
// members the struct's json tags spell, so a misspelt one is refused rather
// than silently setting nothing.
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

// TestProjectPolicyMobileForPublishFillsEveryMember is the update-side rule the
// block follows, and it is the one that keeps a member added by a later release
// from being lost: a node published before the member existed carries a block
// without it, and a publish that returned that block as it found it would leave
// the member off the node for ever.
//
// The app's crash-report switch is not a member of this block and is filled
// beside it (MobileCrashReportsForPublish,
// TestProjectPolicyMobileCrashReportsHoldsTheSwitch): a member added inside the
// block would be refused by every reader of the release behind, which is why
// D731's switch is a top-level key and why this fill is about the block's own
// members only.
//
// Three claims, and each of them is a way the fill can go wrong:
//
//   - a block that carries nothing is filled with every member DefaultMobile
//     states, which is the claim a member added to the struct without a line in
//     the fill fails (the deep-equality against DefaultMobile covers the whole
//     block, so a forgotten member cannot pass);
//   - a block that carries a member keeps its value, whichever member it is and
//     whatever the value, because these are the project's own answers;
//   - the node it was handed is not edited — the caller writes the result into
//     the document it publishes, and the node the stage read is what it reports
//     the diff against.
func TestProjectPolicyMobileForPublishFillsEveryMember(t *testing.T) {
	if got, want := MobileForPublish(nil), DefaultMobile(); !reflect.DeepEqual(got, want) {
		t.Errorf("MobileForPublish(nil) = %+v, want DefaultMobile() = %+v: a node that carries no block at all is written the whole default", got, want)
	}
	if got, want := MobileForPublish(&Mobile{}), DefaultMobile(); !reflect.DeepEqual(got, want) {
		t.Errorf("MobileForPublish(&Mobile{}) = %+v, want DefaultMobile() = %+v: a block a release behind published is filled member by member, so a member added to the struct without a line here is left off every such node", got, want)
	}
	// A block that carries one member is filled for the rest and keeps what it
	// carries, which is what makes the published block state every switch
	// explicitly (D2796) rather than leaving a reader to know a default.
	partial := MobileForPublish(&Mobile{Enabled: boolPtr(false)})
	if partial.Enabled == nil || *partial.Enabled {
		t.Errorf("MobileForPublish turned the block's written enabled=%v into something else, want the project's own false", partial.Enabled)
	}
	if partial.Sync == nil || !*partial.Sync {
		t.Errorf("MobileForPublish left a partial block's sync as %v, want the default a publish writes", partial.Sync)
	}

	written := &Mobile{
		Enabled:           boolPtr(false),
		Sync:              boolPtr(false),
		AcceptedProviders: []string{ProviderGoogle},
		MaxAuthAge:        "6h",
		StrictNewDevice:   boolPtr(true),
		CostlyModes:       []rigor.Rigor{rigor.Critical},
		BiometricOnCostly: boolPtr(true),
	}
	got := MobileForPublish(written)
	if !reflect.DeepEqual(got, written) {
		t.Errorf("MobileForPublish(block the project wrote) = %+v, want the block itself: a publish keeps every member the node carries", got)
	}
	if got == written {
		t.Error("MobileForPublish returned the node it was handed: the caller writes the result into the document it publishes, and the node the stage read is what it reports the diff against")
	}
	// A written empty list is the absent member at the RTDB's own rule, so the
	// fill answers it with the default list — and the other half: a list the
	// block does carry is never widened.
	narrowed := MobileForPublish(&Mobile{AcceptedProviders: []string{ProviderApple}, CostlyModes: []rigor.Rigor{rigor.Hotfix}})
	if !slices.Equal(narrowed.AcceptedProviders, []string{ProviderApple}) || !slices.Equal(narrowed.CostlyModes, []rigor.Rigor{rigor.Hotfix}) {
		t.Errorf("MobileForPublish narrowed the lists a project chose: providers %v, modes %v", narrowed.AcceptedProviders, narrowed.CostlyModes)
	}
	empty := MobileForPublish(&Mobile{})
	if !slices.Equal(empty.AcceptedProviders, DefaultMobileProviders()) || !slices.Equal(empty.CostlyModes, DefaultMobileCostlyModes()) {
		t.Errorf("MobileForPublish left a list empty: providers %v, modes %v, want the defaults", empty.AcceptedProviders, empty.CostlyModes)
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

// TestProjectPolicyMobileCompactionThresholdHoldsTheThreshold pins Z2's
// top-level key: the whole percent at which a project's phones compact their
// context without being asked, and the three states it has —
//
//   - a written percent, which is the project's own answer and travels to the
//     phone as the session policy's `compact_threshold_percent`;
//   - no key at all, and a written null, which are the absent threshold: no
//     automatic compaction, which is what the app must do when it was told
//     nothing (C2's `compaction_threshold` is null for the same state);
//   - a nil Policy, because the pairing route asks before it has read anything.
//
// The value rules are here too: a percent above the highest the phone's own
// comparison accepts, a negative one and a value that is not a number are each
// a load error, because a threshold the app refuses at pairing is a session a
// phone cannot mount and the node would look as if it had one.
func TestProjectPolicyMobileCompactionThresholdHoldsTheThreshold(t *testing.T) {
	if MobileCompactionThresholdKey != "mobile_compaction_threshold" {
		t.Fatalf("MobileCompactionThresholdKey = %q: the key's spelling is the node's, and this test writes nodes with it", MobileCompactionThresholdKey)
	}
	thresholdNode := func(key string) []byte {
		return []byte(`{"version": 10, "budget": {"mode": "observe"}, "mobile": {"enabled": true}, "mobile_compaction_threshold": ` + key + `}`)
	}
	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{name: "a written percent", key: "40", want: 40},
		{name: "the highest the phone's port mounts", key: "99", want: 99},
		{name: "written off", key: "0", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode(thresholdNode(tc.key))
			if err != nil || !found || p == nil {
				t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
			}
			if len(p.UnknownKeys) != 0 {
				t.Errorf("UnknownKeys = %v, want none: the key is this Fugaro's own", p.UnknownKeys)
			}
			if got := p.MobileCompactionThresholdPercent(); got != tc.want {
				t.Errorf("MobileCompactionThresholdPercent() = %d for a key written %s, want %d", got, tc.key, tc.want)
			}
		})
	}
	// A node that carries no key at all, and one whose value is null: both are
	// the absent threshold, and both mean no automatic compaction — the
	// default, not a value this Fugaro invented.
	for _, node := range []string{
		`{"version": 10, "budget": {"mode": "observe"}, "mobile": {"enabled": true}}`,
		`{"version": 10, "budget": {"mode": "observe"}, "mobile": {"enabled": true}, "mobile_compaction_threshold": null}`,
	} {
		p, found, err := Decode([]byte(node))
		if err != nil || !found || p == nil {
			t.Fatalf("Decode(%s) = (%v, %v, %v), want the node read", node, p, found, err)
		}
		if p.MobileCompactionThreshold != nil {
			t.Errorf("the threshold of %s decoded to %v, want nil: neither state is an answer", node, p.MobileCompactionThreshold)
		}
		if got := p.MobileCompactionThresholdPercent(); got != DefaultMobileCompactionThreshold {
			t.Errorf("MobileCompactionThresholdPercent() = %d for %s, want %d: a project that set no threshold compacts nothing automatically", got, node, DefaultMobileCompactionThreshold)
		}
	}
	// The pairing route asks before it has read anything.
	var none *Policy
	if got := none.MobileCompactionThresholdPercent(); got != DefaultMobileCompactionThreshold {
		t.Errorf("MobileCompactionThresholdPercent() = %d for a nil Policy, want %d", got, DefaultMobileCompactionThreshold)
	}
	// A threshold the app cannot mount and a value that is not a whole percent
	// are refused: 100 is a full window, and the fraction the phone compares
	// with must be above zero and below one, so the node would read as a project
	// that compacts and the phone would refuse the session it carries.
	for _, tc := range []struct{ name, key string }{
		{name: "a full window", key: "100"},
		{name: "above the highest", key: "101"},
		{name: "negative", key: "-1"},
		{name: "a fractional percent", key: "40.5"},
		{name: "a string", key: `"40"`},
		{name: "a bool", key: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p, _, err := Decode(thresholdNode(tc.key)); err == nil {
				t.Fatalf("a key written %s was read: %+v", tc.key, p.MobileCompactionThreshold)
			}
		})
	}
}

// intPtr is a written whole number: the threshold's own member is a pointer so
// that the absent key and a written 0 are told apart, and a test that states one
// has to take a variable's address for it (boolPtr does the same for the switch
// beside it).
func intPtr(v int) *int { return &v }

// TestProjectPolicyMobileCompactionThresholdToleratedByAReaderThatPredatesIt is
// the second half of the reason the key is not a member of the mobile block: the
// node a publish writes decodes with the release behind's key set, so a run in
// flight on the previous image — and a CLI of the previous release — reads the
// whole policy it is served after one `fugaro update`
// (docs/design/update-in-flight.md).
//
// The reader modelled here is the release just before the key: this Fugaro's own
// list with the threshold removed, which is exactly what a reader of that age
// carries. The mobile block stays the seven members that release decodes, so the
// key is the only thing it does not know, and it lists the key rather than
// refusing the node.
func TestProjectPolicyMobileCompactionThresholdToleratedByAReaderThatPredatesIt(t *testing.T) {
	raw, err := json.Marshal(&Policy{
		Version:                   Version,
		Budget:                    Budget{Mode: "observe"},
		Mobile:                    MobileForPublish(nil),
		MobileCompactionThreshold: intPtr(40),
	})
	if err != nil {
		t.Fatalf("marshalling the node a publish writes: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node a publish writes is not a document: %v", err)
	}
	block, ok := doc["mobile"]
	if !ok {
		t.Fatal("the node a publish writes carries no mobile block")
	}
	names, err := objectMemberNames(block)
	if err != nil {
		t.Fatalf("the block is not an object: %v", err)
	}
	if slices.Contains(names, MobileCompactionThresholdKey) {
		t.Errorf("the mobile block a publish writes names %q, which is a top-level key: a member beside the block's seven is refused by the release behind's decodeMobile, which returns that error for the whole policy", MobileCompactionThresholdKey)
	}
	if err := checkMemberNames("mobile", names, mobileMembers); err != nil {
		t.Errorf("the block a publish writes is refused by this Fugaro's own member set: %v", err)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, MobileCompactionThresholdKey)
	q, found, err := decodeWithKeys(raw, preKey)
	if err != nil || !found || q == nil {
		t.Fatalf("the reader that predates the key refused the node a publish writes: (%v, %v, %v)", q, found, err)
	}
	if want := []string{MobileCompactionThresholdKey}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v: that is the node a CLI of that age refuses at publish time and a runner of it logs", q.UnknownKeys, want)
	}
	// A reader of that age has no member for the key, so the threshold it hands
	// a phone is none (the release behind's session policy carries no percent
	// for it): a project that sets one compacts automatically again once the
	// installation is updated, and never while the agent is busy either way.
	if !q.MobileEnabled() {
		t.Error("the reader that predates the key reads the mobile block as disabled: the block it knows is there, and only the new key is unknown")
	}
}
