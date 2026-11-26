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
func DefaultMobileCostlyModes() []rigor.Rigor { return []rigor.Rigor{rigor.Pro, rigor.Critical} }

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
// to F40 (D2796, O9): `mobile.enabled` is on in the block a publish writes for a
// node that carries none (MobileForPublish, what `fugaro update` publishes), so
// pairing answers as soon as the update has run, and an installation that has
// not updated answers `mobile_disabled` — deployment alone switches nothing on,
// which is the credentials service's own rule.
//
// The members' defaults inside a written block are the product's (D2784,
// D2785, D2786, O4) and every one of them is a member DefaultMobile writes
// explicitly: the node a publish writes says what it does rather than leaving a
// reader to know a default, which is what D2796 asks for when it says admins
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
	CostlyModes []rigor.Rigor `json:"costly_modes,omitempty"`

	// BiometricOnCostly is whether a start in a costly mode additionally
	// requires biometric re-authentication (D2786). Off is the default: the
	// read-back is the gate, and the biometric is the project that wants more.
	BiometricOnCostly *bool `json:"biometric_on_costly,omitempty"`
}

// mobileMembers is the block's member spelling, read from the struct's json
// tags (carrierMembers) so a field added here is known without a second list to
// keep in step.
var mobileMembers = carrierMembers(reflect.TypeOf(Mobile{}))

// DefaultMobile is the block a publish writes for a node that carries none
// (O9): every member written, each at the default the plan's P6 states. It is
// the one place the product's defaults are spelled as a node, so the value an
// operator reads in the published policy and the value a reader falls back to
// for a member a hand-edited node leaves out are the same numbers.
//
// It is the answer of a project that has never answered and not one it gave:
// MobileForPublish is what a publish writes, and a node that carries a block
// keeps its own through every publish.
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

// MobileForPublish is the block a publish writes: the node's own member by
// member, each member it does not carry at the default DefaultMobile() states.
//
// The node is the only place these switches live — no key of any machine's
// local config states them, and they are the project's own answers — so a
// publish keeps every member it finds: a project whose owner turned pairing
// off, or chose to store nothing in the identity project, keeps that decision
// through a publish from any machine. A member the node does not carry is
// filled with the default a publish would write — D2796's "the node says what
// it does", so the published block states every switch explicitly rather than
// leaving a reader to know a default — which is also what makes the pairing
// route answer after `fugaro update` and answer `mobile_disabled` before it
// (F40, the global constraint the plan states for P3, K2 and P6).
//
// **It fills member by member and not block by block.** A node carrying a
// partial block is exactly what a member added by a later release leaves
// behind, and a function that returned the whole block as it found it would
// leave the new member off that node for ever. Every member the block writes
// explicitly is filled here, which is what makes a member added to the block a
// one-line change and what TestProjectPolicyMobileForPublishFillsEveryMember
// holds.
//
// It is one function because two callers have to agree about it: the publisher
// writes the node with it (internal/cli's buildProjectPolicy), and the drift
// `fugaro update`'s policy stage decides to publish on compares the node with
// it (policyConfigDrift) — a stage that compared the node with nil would find
// nothing to do about a node that carries no block, and the default would never
// reach an installation that has not published since the key existed.
//
// The app's crash-report switch is NOT a member of this block and is not filled
// here: it is a top-level key beside it, with a fill of its own
// (MobileCrashReportsForPublish), because a member added inside this block is
// refused by every reader of the release behind (the block's comment above).
func MobileForPublish(node *Mobile) *Mobile {
	if node == nil {
		return DefaultMobile()
	}
	defaults := DefaultMobile()
	// A copy rather than the node itself: the caller writes the result into the
	// document it is about to publish, and the node it read stays as it was.
	out := *node
	if out.Enabled == nil {
		out.Enabled = defaults.Enabled
	}
	if out.Sync == nil {
		out.Sync = defaults.Sync
	}
	if len(out.AcceptedProviders) == 0 {
		out.AcceptedProviders = defaults.AcceptedProviders
	}
	if out.MaxAuthAge == "" {
		out.MaxAuthAge = defaults.MaxAuthAge
	}
	if out.StrictNewDevice == nil {
		out.StrictNewDevice = defaults.StrictNewDevice
	}
	if len(out.CostlyModes) == 0 {
		out.CostlyModes = defaults.CostlyModes
	}
	if out.BiometricOnCostly == nil {
		out.BiometricOnCostly = defaults.BiometricOnCostly
	}
	return &out
}

// MobileCrashReportsKey is the top-level key the app's crash-report switch
// travels as (D731, D738), beside the mobile block and never inside it: the
// block decodes strictly, so a key inside it would be refused by every reader
// of the release behind (Policy.MobileCrashReports's comment).
const MobileCrashReportsKey = "mobile_crash_reports"

// DefaultMobileCrashReports is the switch a publish writes for a node that does
// not carry the key: on (D731, D738). A crash report that arrives on its own is
// what makes a fault the owner never hears about visible at all, so the default
// is on and a project that does not want them writes false — the opt-out the
// owner ruled on 2026-10-30.
const DefaultMobileCrashReports = true

// MobileCrashReportsForPublish is the switch a publish writes: the node's own
// value when it carries one, and the default a publish writes when it does not.
//
// The key's own fill, and it is the whole of what carries the switch to an
// installation that published before the key existed: the drift row
// (policyConfigDrift) compares the node with this answer, so a node without the
// key differs from it and `fugaro update`'s policy stage publishes the key once
// — while a node whose owner wrote false is the value itself and drifts on
// nothing (O13: a hand edit is the only door that value has today).
func MobileCrashReportsForPublish(node *bool) *bool {
	if node != nil {
		return node
	}
	return boolPtr(DefaultMobileCrashReports)
}

// MobileCompactionThresholdKey is the top-level key the phone's automatic
// compaction threshold travels as (Z2), beside the mobile block and never
// inside it, for the reason the switch above is: a member added inside the
// block is refused by every reader of the release behind, which would make one
// `fugaro update` stop the runs in flight the block's own comment forbids
// stopping (Policy.MobileCrashReports's comment, F46).
const MobileCompactionThresholdKey = "mobile_compaction_threshold"

// DefaultMobileCompactionThreshold is the threshold a project that never
// answered has: none. It is the same value C1 spells for "this project set no
// automatic compaction" (mobilesession.SessionPolicy.CompactThresholdPercent),
// so the project policy and the session the phone holds state it one way, and a
// reader that converts the percent into the fraction the app compares with
// (C2's `compaction_threshold`) has one rule and not two.
const DefaultMobileCompactionThreshold = 0

// MobileCompactionThresholdMax is the highest threshold a node may write: 99,
// and not a full window. The app fires the automatic compaction on a fraction
// of the window that C2's port requires to be above zero and below one, so a
// project at 100 would publish a threshold the phone refuses to mount its
// session with — a refusal at pairing rather than "compact when the window is
// full". A value above this is a mistake in the node rather than a threshold
// that never fires, and the refusal names the percent it wrote.
const MobileCompactionThresholdMax = 99

// MobileCompactionThreshold is how full the context may grow before this
// project's phones compact it without being asked, as a whole percent
// (DefaultMobileCompactionThreshold when the node carries no key, which is no
// automatic compaction at all).
//
// The node's value and not a product default: the threshold is the project's
// own answer (the spec's "user-set ... Configurable per project"), and the app
// compacts on it only while the agent is idle, so a project that sets none
// compacts only when the person taps the button. A nil Policy, and a key that
// is not there, both answer zero for the reason every accessor here does — the
// pairing route asks before it has read anything, and a fail-safe value is not
// an invented one: zero is "compaction is the person's to ask for", which is
// what a phone told nothing must do.
func (p *Policy) MobileCompactionThresholdPercent() int {
	if p == nil || p.MobileCompactionThreshold == nil {
		return DefaultMobileCompactionThreshold
	}
	return *p.MobileCompactionThreshold
}

// validMobileCompactionThreshold refuses a threshold the phone cannot mount,
// so that a hand-edited node is a load error rather than a rule that never
// fires: a negative percent, and one above MobileCompactionThresholdMax, are
// both values the app's own comparison has no room for. Zero is accepted and is
// the default, which is the project saying in so many words that it compacts
// nothing automatically — the same thing an absent key says.
func validMobileCompactionThreshold(percent int) error {
	if percent < 0 || percent > MobileCompactionThresholdMax {
		return fmt.Errorf("projectpolicy: mobile_compaction_threshold is %d: write 0 for no automatic compaction, or 1 to %d — the app compares a fraction of the window that is above zero and below one",
			percent, MobileCompactionThresholdMax)
	}
	return nil
}

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

// MobileCrashReportsOn reports whether this project's app may send crash
// reports (D731, D738).
//
// A nil Policy, and a key that is not there, both answer false, and that is the
// rule MobileStrictNewDevice and MobileBiometricOnCostly follow: **an absent
// switch is off**, never this switch's default of on. The reader is the session
// the phone holds — and a session a release behind assembled carries no
// `crash_reports` — so answering the default here would turn collection on for a
// phone that was told nothing, against D734's rule that nothing is sent before
// the switch has been read. The fail-safe is therefore the same value the
// switch's off state is, and the app never has to tell the two apart.
func (p *Policy) MobileCrashReportsOn() bool {
	return p != nil && p.MobileCrashReports != nil && *p.MobileCrashReports
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
func (p *Policy) MobileCostlyModes() []rigor.Rigor {
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
func validMobileCostlyModes(modes []rigor.Rigor) error {
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
	out := make([]string, 0, len(rigor.Rigors))
	for _, m := range rigor.Rigors {
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
