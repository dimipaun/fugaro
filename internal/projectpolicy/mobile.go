package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// The four identity providers a project may accept, spelled the way Firebase
// Auth spells them: an entry of AcceptedProviders is matched against an
// assertion's `provider` claim, which is the IdP's own identifier, so the list
// and the claim are one vocabulary and not two (D2785). `password` is email and
// password, the one provider whose assertion carries no third party.
//
// They are constants rather than literals at the call sites because P2's invite
// carries the list to the phone, P3's route matches it, and the app's sign-in
// buttons are chosen from it: a misspelt provider in any of those is a project
// that admits a sign-in nobody can complete.
const (
	// ProviderGoogle is Google, whose assertion carries a verified email.
	ProviderGoogle = "google.com"
	// ProviderPassword is email and password, verified before it is accepted.
	ProviderPassword = "password"
	// ProviderMicrosoft is Microsoft, accepted only with a tenant-verified
	// claim: a personal account's email is not verified by the tenant.
	ProviderMicrosoft = "microsoft.com"
	// ProviderApple is Apple, accepted only with a real email: a private relay
	// address is not the person's.
	ProviderApple = "apple.com"
)

// mobileProviderNames is the whole vocabulary, in the order the default list
// writes it.
func mobileProviderNames() []string {
	return []string{ProviderGoogle, ProviderPassword, ProviderMicrosoft, ProviderApple}
}

// DefaultMobileProviders is the provider list of a project that accepts the
// default set: a copy, so a caller cannot edit the vocabulary through it.
func DefaultMobileProviders() []string { return mobileProviderNames() }

// DefaultMobileMaxAuthAge is how recent the authentication behind an assertion
// must be for this project to accept it: 24 hours (O4). A phone that has not
// been unlocked and signed in for longer than this pairs or restores again.
const DefaultMobileMaxAuthAge = 24 * time.Hour

// DefaultMobileMaxAuthAgeText is the same value as the block spells it, and it
// is the spelling DefaultMobile writes: the member is a duration string the way
// the rest of this tree writes one (config.ParseDuration), so the node says
// "24h" and not a number of seconds. TestProjectPolicyMobileDefaults holds the
// two to one value.
const DefaultMobileMaxAuthAgeText = "24h"

// DefaultMobileCostlyModes is the set of rigor modes whose start costs enough
// to ask the person for a spoken read-back (D2786): `pro` and `critical`.
func DefaultMobileCostlyModes() []rigor.Mode { return []rigor.Mode{rigor.Pro, rigor.Critical} }

// Mobile is the project policy's mobile block: the switches and the lists the
// pairing route, the app's session and the phone's own decisions are read from.
//
// It is a top-level key of the published node, and the shape is F46's argument
// applied to this surface: the node is re-read by every reader while runs are
// in flight, and a *block* of this node decodes strictly, so a member added
// inside one would make a reader of the release behind refuse the whole node —
// which is what `update` promises never to do (docs/design/update-in-flight.md).
// An unknown top-level key is listed in Policy.UnknownKeys and tolerated (P8),
// so a CLI or a runner of the release behind reads every key it knows and goes
// on. Nothing in this block is enforced by a runner: the readers are the
// credentials service's pairing route and the app's session, both of which ship
// in the same release as the key — so no policy Version moves for it
// (version.go: what moves a version is a runner having to enforce something it
// does not know).
//
// **The block's presence is the surface's switch, and its absence is off.**
// A policy published before this key existed carries no block, and reads as
// `mobile.enabled: false` — never as the default on. That is the owner's answer
// to F40 (D2796, O9): `mobile.enabled` is on in the block `fugaro update`
// publishes, so pairing answers as soon as the update has run, and an
// installation that has not updated answers `mobile_disabled` — deployment
// alone switches nothing on, which is the credentials service's own rule.
//
// The members' defaults inside a written block are the product's (D2784,
// D2785, D2786, O4) and every one of them is a member DefaultMobile writes
// explicitly: the node `update` publishes says what it does rather than leaving
// a reader to know a default, which is what D2796 asks for when it says admins
// are shown what is stored.
type Mobile struct {
	// Enabled is whether this project's phones may pair at all. It is a pointer
	// so that a written `false` is a decision and not silence: the member's
	// default inside a written block is true, and a project that turns pairing
	// off keeps its node and its history.
	Enabled *bool `json:"enabled,omitempty"`

	// Sync is whether the list of paired projects is stored in the identity
	// project, so that a new device of the same person restores it silently
	// (D2784, D2796). Off means nothing about the person's projects leaves the
	// phone: a new device pairs again by QR code. It is the member whose
	// *storage* doctor reports when it is on, because what it stores is the one
	// thing an admin cannot see from the phone.
	Sync *bool `json:"sync,omitempty"`

	// AcceptedProviders is the set of identity providers whose assertions this
	// project accepts, each one of the four names above (D2785). Absent means
	// the default set — the four, minus the two whose claims need a check the
	// project may not want. A project narrows this list; it cannot widen it,
	// because the vocabulary is closed and refused at decode.
	//
	// A written empty list is the absent member and not "accept nobody", which
	// is the RTDB list's own rule (an empty array leaves no member at all,
	// decodeBinaries' note): a project that accepts no provider turns the
	// surface off with `enabled: false` instead.
	AcceptedProviders []string `json:"accepted_providers,omitempty"`

	// MaxAuthAge is how recent the authentication behind an assertion must be,
	// as a duration this tree's own parser reads ("24h", "14d"): O4's per
	// project answer, whose default is 24 hours. It is a string and not a
	// number of seconds because a hand-edited node is read by people, and a
	// value that is not a positive duration is refused at decode.
	MaxAuthAge string `json:"max_auth_age,omitempty"`

	// StrictNewDevice is whether a device this project has not seen must pair
	// again with a QR code instead of restoring from the identity project
	// (D2784). Off is the default and means a new device of a person who is
	// signed in restores silently; on is the switch for a project that wants
	// the second factor the physical QR is.
	StrictNewDevice *bool `json:"strict_new_device,omitempty"`

	// CostlyModes are the rigor modes whose start asks the person for a spoken
	// read-back (D2786): the default is `pro` and `critical`. An entry is a
	// mode internal/rigor knows, so a project may name `hotfix` too — the
	// read-back is the project's cost decision and not this reader's.
	CostlyModes []rigor.Mode `json:"costly_modes,omitempty"`

	// BiometricOnCostly is whether a start in a costly mode additionally
	// requires biometric re-authentication (D2786). Off is the default: the
	// read-back is the gate, and the biometric is the project that wants more.
	BiometricOnCostly *bool `json:"biometric_on_costly,omitempty"`
}

// mobileMembers is the block's member spelling, read from the struct's json
// tags (carrierMembers) so a field added here is known without a second list to
// keep in step.
var mobileMembers = carrierMembers(reflect.TypeOf(Mobile{}))

// DefaultMobile is the block `fugaro update` publishes (O9): every member
// written, each at the default the plan's P6 states. It is the one place the
// product's defaults are spelled as a node, so the value an operator reads in
// the published policy and the value a reader falls back to for a member a
// hand-edited node leaves out are the same numbers.
//
// Writing it is the update's policy stage and not this package's: a policy
// published before this key existed is migrated in place by the stage that
// already rewrites the node (UP1), and `fugaro project policy publish` writes
// the node from the local config, which carries no mobile block.
func DefaultMobile() *Mobile {
	return &Mobile{
		Enabled:           boolPtr(true),
		Sync:              boolPtr(true),
		AcceptedProviders: DefaultMobileProviders(),
		MaxAuthAge:        DefaultMobileMaxAuthAgeText,
		StrictNewDevice:   boolPtr(false),
		CostlyModes:       DefaultMobileCostlyModes(),
		BiometricOnCostly: boolPtr(false),
	}
}

// boolPtr is the written true or false a *bool member carries, for the defaults
// above: the zero value of the member is "not written", so a default cannot be
// stated without taking its address.
func boolPtr(v bool) *bool { return &v }

// MobileEnabled reports whether this project's phones may pair at all.
//
// A nil Policy, a nil block and a written `false` all answer false: the block's
// absence is a policy published before the key existed, and the pairing route
// answers it with `mobile_disabled` rather than with the default on (F40, O9,
// and the global constraint the plan states for P3, K2 and P6). The answer is
// the whole of the switch — every other accessor below answers the member's
// default whether or not the block is there, so a caller asks this one first
// and never reads a list as an admission on its own.
func (p *Policy) MobileEnabled() bool {
	return p != nil && p.Mobile != nil && !mobileOff(p.Mobile.Enabled)
}

// MobileSync reports whether the list of paired projects is stored in the
// identity project (D2796). False for a node with no block: nothing is stored
// for a project that has not turned the surface on.
func (p *Policy) MobileSync() bool {
	return p != nil && p.Mobile != nil && !mobileOff(p.Mobile.Sync)
}

// MobileStrictNewDevice reports whether a device this project has not seen must
// pair again with a QR code (D2784). False when the member or the block is
// absent: silent restore is the product's default, and a project asks for the
// QR by writing the member.
func (p *Policy) MobileStrictNewDevice() bool {
	return p != nil && p.Mobile != nil && p.Mobile.StrictNewDevice != nil && *p.Mobile.StrictNewDevice
}

// MobileBiometricOnCostly reports whether a start in a costly mode also
// requires biometric re-authentication (D2786). False when absent, like the
// strict switch and for the same reason.
func (p *Policy) MobileBiometricOnCostly() bool {
	return p != nil && p.Mobile != nil && p.Mobile.BiometricOnCostly != nil && *p.Mobile.BiometricOnCostly
}

// MobileAcceptedProviders is the set of identity providers this project accepts
// (D2785), the default four when the member or the block is absent.
//
// The answer is a copy: a caller that sorts it, or edits it, must not be
// editing the policy it read.
func (p *Policy) MobileAcceptedProviders() []string {
	if p == nil || p.Mobile == nil || len(p.Mobile.AcceptedProviders) == 0 {
		return DefaultMobileProviders()
	}
	return slices.Clone(p.Mobile.AcceptedProviders)
}

// MobileMaxAuthAge is how recent the authentication behind an assertion must be
// (O4), 24 hours when the member or the block is absent. A written value that
// is not a positive duration cannot reach here (decodeMobile refuses it); if
// one did, the default is the answer, never a zero — a zero age would refuse
// every assertion, and refusing the whole surface is not what a bad duration
// means (credentialsDuration's argument, one block over).
func (p *Policy) MobileMaxAuthAge() time.Duration {
	if p == nil || p.Mobile == nil {
		return DefaultMobileMaxAuthAge
	}
	return credentialsDuration(p.Mobile.MaxAuthAge, DefaultMobileMaxAuthAge)
}

// MobileCostlyModes are the rigor modes whose start asks for a spoken read-back
// (D2786), `pro` and `critical` when the member or the block is absent.
func (p *Policy) MobileCostlyModes() []rigor.Mode {
	if p == nil || p.Mobile == nil || len(p.Mobile.CostlyModes) == 0 {
		return DefaultMobileCostlyModes()
	}
	return slices.Clone(p.Mobile.CostlyModes)
}

// mobileOff reports whether a written switch is the off one. A nil member is
// not off: inside a written block the absent member takes its default, and the
// two switches whose default is on are the ones this reader answers for.
func mobileOff(v *bool) bool { return v != nil && !*v }

// decodeMobile reads one mobile block's bytes. The block is a closed set of
// seven members, so a misspelt one is refused rather than silently setting
// nothing — a `strict_device` that never fires and a `strict_new_device` that
// does are indistinguishable in a decoded node, and the operator would have no
// way to tell a project that restores silently from one that looks strict.
//
// The three value rules are checked here and not at the route that reads them,
// because each names a vocabulary this Fugaro holds and a route cannot report
// what it cannot resolve: a provider outside the four, a mode internal/rigor
// does not know, and a duration that is not a positive one. Every refusal
// returns no block at all, so a caller never acts on the half of a block that
// was read before the member that could not be.
func decodeMobile(raw json.RawMessage) (*Mobile, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: mobile: %w", err)
	}
	if err := checkMemberNames("mobile", names, mobileMembers); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out Mobile
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("projectpolicy: mobile: %w", err)
	}
	if err := validMobileProviders(out.AcceptedProviders); err != nil {
		return nil, err
	}
	if err := validMobileCostlyModes(out.CostlyModes); err != nil {
		return nil, err
	}
	if err := validMobileMaxAuthAge(out.MaxAuthAge); err != nil {
		return nil, err
	}
	return &out, nil
}

// validMobileProviders holds the list to the closed vocabulary D2785 states: an
// entry is one of the four IdP identifiers, and the list names each once.
//
// A provider is a name and never a URL, a host or a tenant, for the reason the
// voice list is held to the same rule (§4.3's policy-carried redirection): the
// route matches the entry against the assertion's `provider` claim, and an
// entry that carried a destination of its own would be a project policy
// pointing an identity at an issuer nobody chose.
func validMobileProviders(providers []string) error {
	for i, name := range providers {
		at := fmt.Sprintf("mobile.accepted_providers[%d]", i)
		if !slices.Contains(mobileProviderNames(), name) {
			return fmt.Errorf("projectpolicy: %s is %s, which is not an identity provider: an entry is matched against an assertion's provider claim, and this Fugaro knows %s (the Firebase Auth spellings) — a URL, a host, a tenant or a name in another case is an entry no route can resolve, so write one of those four, or leave the member out to accept the default set",
				at, mobileEntryText(name), strings.Join(mobileProviderNames(), ", "))
		}
		if slices.Contains(providers[:i], name) {
			return fmt.Errorf("projectpolicy: %s names %q, which the list already names: list each provider once", at, name)
		}
	}
	return nil
}

// validMobileCostlyModes holds the list to the modes internal/rigor knows, each
// named once. The comparison is rigor.Parse's — the one way a written value
// becomes a Mode — so a near miss ("PRO", "prod") is refused here rather than
// becoming a rule that silently never fires.
func validMobileCostlyModes(modes []rigor.Mode) error {
	for i, m := range modes {
		at := fmt.Sprintf("mobile.costly_modes[%d]", i)
		if _, ok := rigor.Parse(string(m)); !ok {
			return fmt.Errorf("projectpolicy: %s is %s, which is not a rigor mode: a start in a mode this list names asks the person for a spoken read-back, and the modes are %s (internal/rigor) — a name outside them would be a rule that never fires",
				at, mobileEntryText(string(m)), strings.Join(modeNames(), ", "))
		}
		if slices.Contains(modes[:i], m) {
			return fmt.Errorf("projectpolicy: %s names %q, which the list already names: list each mode once", at, m)
		}
	}
	return nil
}

// modeNames is the rigor vocabulary as one string, for the refusals above.
func modeNames() []string {
	out := make([]string, 0, len(rigor.Modes))
	for _, m := range rigor.Modes {
		out = append(out, string(m))
	}
	return out
}

// validMobileMaxAuthAge refuses an age that is not a positive duration, and
// answers the absent member. A zero or negative age would refuse every
// assertion the route ever sees — a project with no working sign-in and a node
// that reads as if it had one — so it is a load error rather than a value the
// accessor has to make sense of.
func validMobileMaxAuthAge(value string) error {
	if value == "" {
		return nil
	}
	d, err := config.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("projectpolicy: mobile.max_auth_age is %s: %w", mobileEntryText(value), err)
	}
	if d <= 0 {
		return fmt.Errorf("projectpolicy: mobile.max_auth_age is %s, and the age must be positive: it is how recent the authentication behind an assertion has to be, and an age of zero refuses every assertion this project would ever be offered — write a duration such as %s, or remove the member for the default",
			mobileEntryText(value), DefaultMobileMaxAuthAgeText)
	}
	return nil
}

// mobileEntryText renders one list entry for a refusal the way the voice list's
// entries are rendered (voiceEntryText): quoted — so a newline or an escape in
// a hand-edited node cannot start a line of its own in a log — and bounded,
// because an entry is not allowed to paste a document into a log or into the
// route's answer.
func mobileEntryText(entry string) string { return voiceEntryText(entry) }
