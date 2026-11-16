package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
)

// Voice is one project's voice policy: the node at PathVoiceRepo(slug), or at
// PathVoiceShared — the allow-list every project of the installation falls back
// to. It is the whole of what decides whether a phone's speech may leave the
// device for a provider's engine, and its shape is
// docs/mobile/design/voice-evaluation-switch.md §4.3's:
//
//	providers: ["<name>", ...],             // absent or [] => local only
//	direction: { stt: false, tts: false },  // absent => both false
//	force_local: false                      // true => every device serves this project locally
//
// Three of its properties are the design's and are not this reader's to
// soften, because everything downstream — the hop's refusal, the indicator's
// "blocked by project policy", the app's fallback — trusts the answers below:
//
//   - **Default deny, and the default is the inversion of `allow_data_to`'s.**
//     Absent, empty, malformed and unreadable all mean local only: the same
//     four answers, because a caller that must not guess cannot tell them apart
//     (§4.3, and the half of D44 this node keeps — "absent means nothing leaves
//     the machine"). In internal/config an
//     absent `allow_data_to` is the *wide* answer — every repository — so a
//     reader that carried that default across would open the microphone to
//     every project of the installation. Here the zero value is the narrow
//     answer, and LocalVoice is it.
//
//   - **An entry is a provider name, never a URL.** A policy-carried URL is a
//     policy-carried redirection (§4.3), so a URL, a host or a path in the list
//     is refused at decode rather than filtered later: the hop resolves a name
//     against the providers it holds, and only a name cannot carry a
//     destination of its own.
//
//   - **The two directions are separate switches.** `direction.stt` and
//     `direction.tts` default false independently, so an edit that widens text
//     cannot silently widen audio — the one direction of the two whose payload
//     no rule can redact (§3: redaction is a function of a string, and audio is
//     not a string).
//
// `force_local` wins over every other member and over the device: a project
// that forbids remote is served locally on every device whatever that device's
// setting says. The enforcement is here, at the hop that reads this node, and
// not on the device, which holds no grant on config/policy/** (§4.1, Q3
// answered 2026-10-19) — a device cannot widen what the hop refuses.
//
// This type is read as *data*; what a consumer does with it (refusing a call
// with its reason, answering the app's project-status query) belongs to
// internal/voicesvc (P5.3.3). Nothing here reads the database, and
// PathVoiceRepo's own comment says why: the hop reads the node over IAM and the
// operator publishes it, and no client writes it.
type Voice struct {
	// Providers are the engine names this project admits a remote call to:
	// names of internal/config's provider shape, which the hop resolves against
	// the providers it holds. An absent member and an empty list are one answer
	// — nobody (§4.3's own comment on the node), and they are one write in the
	// database as well, because an RTDB list is an object with numeric keys and
	// a written `[]` leaves no member at all (decodeBinaries' own note).
	Providers []string `json:"providers,omitempty"`

	// Direction is the node's two switches, one per direction.
	Direction VoiceDirection `json:"direction,omitempty"`

	// ForceLocal forbids remote for every device of the project.
	//
	// It is the member whose zero is the *wider* answer: absent means "not
	// forced", so a written null — which is what a hand-edited node and a
	// `force_local:` with nothing under it produce, and what encoding/json
	// leaves as false — would silently leave the device's own setting in
	// charge. A member whose null decodes into the permissive zero is read off
	// the raw document for exactly this reason (binaries.max_bytes and
	// route_fee_pct are the precedents), and DecodeVoice refuses it.
	ForceLocal bool `json:"force_local,omitempty"`
}

// VoiceDirection is the node's direction member: the two switches, one per
// direction, each defaulting false (§4.1, §4.3).
//
// They are two fields and never one number, because the two directions are two
// different privacy propositions: a remote TTS call sends reply text the
// redactor has already seen, while a remote STT call sends audio no rule can
// reach (§3). An edit that widens one must not widen the other.
//
// A written null on either switch is read as the false it decodes to, which is
// the opposite of what the same spelling does one level up: false is the
// *local* answer for a switch, so a half-finished edit here fails closed, while
// the same edit on `force_local` would fail open (Voice.ForceLocal).
type VoiceDirection struct {
	// STT admits a remote speech-to-text call: raw audio leaves the device.
	STT bool `json:"stt,omitempty"`
	// TTS admits a remote text-to-speech call: the reply text leaves the
	// device, after the redactor and the reader rules have been applied to it.
	TTS bool `json:"tts,omitempty"`
}

// voiceMembers, voiceDirectionMembers are the two carriers' member spelling,
// read from the structs' json tags (carrierMembers) so a field added here is
// known without a second list to keep in step.
var (
	voiceMembers          = carrierMembers(reflect.TypeOf(Voice{}))
	voiceDirectionMembers = carrierMembers(reflect.TypeOf(VoiceDirection{}))
)

// voiceProviderNameRE is the shape of one entry in the list: the shape
// internal/config holds a provider's own key to (its `providerNameRE`,
// unexported there and mirrored in schemas/fugaro.schema.json and
// schemas/project-layer.schema.json, which is how a rule with three readers is
// kept one rule).
//
// It is spelled again here rather than read from that package, because the
// constant is unexported and widening it for this node would widen the
// providers block of every fugaro.yaml with it. What holds the two spellings to
// one answer is a test and not a comment
// (TestVoiceProviderNamesAreConfigsProviderNames): every candidate is put
// through both rules, and the two must refuse the same strings.
var voiceProviderNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// LocalVoice is the node's four answers as one value: local only.
//
// Absent, empty, malformed and unreadable are the same answer here, because a
// caller that must not guess cannot tell them apart (§4.3). It is the zero
// Voice, so a caller that holds no node at all needs no branch for it — the
// argument BinariesAllow makes one level up, applied to the decision the
// microphone depends on.
func LocalVoice() Voice { return Voice{} }

// DecodeVoice reads one voice node's bytes and returns the answer to act on,
// plus why a node that has a problem could not be read.
//
// **The first return is the whole of the decision, and the second never changes
// it.** A node that is absent, empty, malformed or unreadable answers local
// only, and every refusal below returns LocalVoice() beside its error rather
// than the half-decoded value — which is what would turn a misspelt member into
// an admission: encoding/json fills the members it has already read before it
// reaches the one it cannot, so a node carrying `providers: ["x"]` and an
// unknown member would admit x if the refusal handed its work back.
//
// The error is for a person and for the hop's own refusal reason (P5.3.3
// answers the app with it; the app's "blocked by project policy" is a reason
// and not a copy of the node), and it is never a permission. A caller that
// treats a decode failure as "nothing was said against it" has read the node
// exactly backwards: silence is the local answer here, and so is every failure
// to read one.
//
// It is strict the way every block of this package is: a member it does not
// model, a member spelled in another case and a member written twice are
// refused rather than dropped, because neither mistake is visible in a decoded
// node — a misspelt `providers` is a list that admits nobody and a misspelt
// `force_local` is a policy that does not force anything, and the operator
// would have no way to tell either from the project that set nothing.
func DecodeVoice(raw []byte) (Voice, error) {
	if IsNull(raw) {
		return LocalVoice(), nil
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return LocalVoice(), fmt.Errorf("projectpolicy: voice: %w", err)
	}
	if err := checkMemberNames("voice", names, voiceMembers); err != nil {
		return LocalVoice(), fmt.Errorf("projectpolicy: %w", err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(raw, &written); err != nil {
		return LocalVoice(), fmt.Errorf("projectpolicy: voice: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out Voice
	if err := dec.Decode(&out); err != nil {
		return LocalVoice(), fmt.Errorf("projectpolicy: voice: %w", err)
	}
	// The direction block is checked twice and neither is enough alone
	// (decodeBinaries' argument): the decoder refuses a member name the block
	// does not model, and this refuses a known member spelled in another case —
	// which the decoder folds onto it — or written twice.
	if draw, ok := written["direction"]; ok && !IsNull(draw) {
		dnames, err := objectMemberNames(draw)
		if err != nil {
			return LocalVoice(), fmt.Errorf("projectpolicy: voice.direction: %w", err)
		}
		if err := checkMemberNames("voice.direction", dnames, voiceDirectionMembers); err != nil {
			return LocalVoice(), fmt.Errorf("projectpolicy: %w", err)
		}
	}
	// force_local is read off the raw document as well, because a written null
	// decodes into the zero value and this node's own rule has to tell that
	// from the member being absent.
	if fraw, ok := written["force_local"]; ok && IsNull(fraw) {
		return LocalVoice(), fmt.Errorf("projectpolicy: voice.force_local is null: write true or false, or remove the member — a written null is not the absent key here, because this member's zero (false) is the wider answer and would hand the decision back to the device")
	}
	if err := validVoiceProviders(out.Providers); err != nil {
		return LocalVoice(), err
	}
	return out, nil
}

// validVoiceProviders holds the list to the rule §4.3 states: the syntax is
// internal/config's `AllowDataTo` with **the default inverted** — that list
// permits and this one refuses — so an entry names one engine and can mean
// nothing wider.
//
// Two names are answered with a sentence of their own rather than with the
// pattern's, because each is a spelling an operator copies from somewhere else
// in this tree:
//
//   - `"*"` is `allow_data_to`'s marker for every repository. Here the list's
//     default is the opposite of that one, so a wildcard in it would admit
//     every provider the hop holds, which is the one thing this node exists to
//     prevent. Note also that the marker would not survive the pattern anyway:
//     it is refused by name so that the refusal says this rather than "not a
//     provider name", which would send an operator who copied the marker from
//     the other list to look for a typo.
//
//   - `"direct"` is the routing switch's own value (config.RouteDirect,
//     `routing.claude.via`) and not a provider, and internal/config refuses a
//     provider *entry* named that way for the same reason: a call admitted to
//     it would read as a call that went direct. No valid config can hold a
//     provider by that name, so refusing it here refuses no real engine.
//
//   - everything else the pattern refuses: a URL, a host, a path, an address
//     with a port, an uppercase or empty string, and a name longer than the
//     pattern's 20 characters. These are the entries the design calls a
//     policy-carried redirection, and the refusal is at decode rather than at
//     the hop, so a node carrying one is never a node some later reader has to
//     remember to filter.
//
// The list is not otherwise bounded: it is published by the operator over IAM
// and read by the hop, and what bounds its *text* is the refusal's own quoting
// (voiceEntryText), not a count limit this node would have to justify.
func validVoiceProviders(providers []string) error {
	for i, name := range providers {
		at := fmt.Sprintf("voice.providers[%d]", i)
		switch {
		case name == "*":
			return fmt.Errorf("projectpolicy: %s is %q: the wildcard is allow_data_to's marker for every repository, and this list's default is the opposite of that one — an entry here names one engine, so write the names themselves, or leave the member out to admit none", at, name)
		case name == config.RouteDirect:
			return fmt.Errorf("projectpolicy: %s is %q: that is the switch's own value for the direct route (config.RouteDirect, routing.claude.via), not a provider — internal/config refuses a provider entry of that name for the same reason, and a call admitted to it would read as a call that went direct. Name the engine that serves the audio", at, name)
		case !voiceProviderNameRE.MatchString(name):
			return fmt.Errorf("projectpolicy: %s is %s, which is not a provider name: a name matches %s (internal/config's provider name rule), and a URL, a host or a path here would be a policy-carried redirection — the hop resolves an entry against the providers it holds and never against an address the node carries (docs/mobile/design/voice-evaluation-switch.md §4.3)", at, voiceEntryText(name), voiceProviderNameRE)
		}
		if slices.Contains(providers[:i], name) {
			return fmt.Errorf("projectpolicy: %s names %q, which the list already names: list each engine once", at, name)
		}
	}
	return nil
}

// voiceEntryText renders one list entry for a refusal: quoted — so a newline or
// an escape in a hand-edited node cannot start a line of its own in a log — and
// bounded, because the refusal is a line in a log and in the hop's answer, and
// an entry is not allowed to paste a document into either (nodeText's argument,
// one member over).
func voiceEntryText(entry string) string {
	const max = 48
	if r := []rune(entry); len(r) > max {
		return fmt.Sprintf("%q…", string(r[:max]))
	}
	return fmt.Sprintf("%q", entry)
}

// LocalOnly reports whether every call of this project is served on the device:
// what `force_local` says, what an empty list says, and what two switches that
// are both off say.
//
// It is the answer to the app's project-status query and not a substitute for
// the two questions the hop asks per call (AdmitsSTT, AdmitsTTS): an indicator
// that showed "local" for a project that admits one provider for one direction
// would be reporting a node nobody read.
func (v Voice) LocalOnly() bool {
	return v.ForceLocal || len(v.Providers) == 0 || (!v.Direction.STT && !v.Direction.TTS)
}

// AdmitsSTT reports whether this project admits a remote speech-to-text call to
// the engine named name.
func (v Voice) AdmitsSTT(name string) bool { return v.admits(name, v.Direction.STT) }

// AdmitsTTS reports whether this project admits a remote text-to-speech call to
// the engine named name.
func (v Voice) AdmitsTTS(name string) bool { return v.admits(name, v.Direction.TTS) }

// admits is the one rule both directions obey, so the two switches cannot drift
// apart: `force_local` refuses every engine, a direction that is off refuses
// every engine, and otherwise the engine must be on the list.
//
// The name is matched exactly and never folded, unlike the repository names
// internal/config matches in `allow_data_to` (AllowsData): a provider name is
// lowercase by this tree's own rule, so a name that differs in case is not the
// engine the list names, and admitting it would be admitting a spelling nobody
// wrote.
func (v Voice) admits(name string, direction bool) bool {
	if v.ForceLocal || !direction {
		return false
	}
	return slices.Contains(v.Providers, name)
}
