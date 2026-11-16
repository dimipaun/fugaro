package projectpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestVoiceTheFourAnswersAreOneAnswer: absent, empty, malformed and unreadable
// are one answer — local only — because a caller that must not guess cannot
// tell them apart (docs/mobile/design/voice-evaluation-switch.md §4.3).
//
// The mutation this test is written against is DecodeVoice returning the
// half-decoded value beside its error instead of LocalVoice. encoding/json has
// already filled the members it read before it reaches the one it cannot, so
// "an unknown member" below — which carries a provider and a switch that is on
// — would decode to an admission, and that is the case that goes red: watched,
// with the strict decode's `return LocalVoice(), …` changed to `return out, …`,
// as `DecodeVoice(…) answers {Providers:[a-provider] Direction:{STT:true …}}`
// here and in seven cases of TestVoiceAnUnknownMemberIsAnErrorAndNeverAnAdmission.
//
// The fourth answer, a read that did not happen at all, is the hop's to make
// (P5.3.3): what this reader can pin is that no bytes it is handed — including
// none — answer anything but local.
func TestVoiceTheFourAnswersAreOneAnswer(t *testing.T) {
	cases := []struct {
		name    string
		raw     []byte
		wantErr bool
	}{
		{"absent", nil, false},
		{"empty", []byte{}, false},
		{"null", []byte("null"), false},
		{"an empty object", []byte(`{}`), false},
		{"not JSON at all", []byte("this is not a node"), true},
		{"cut off mid-member", []byte(`{"providers": ["a-provider"], "direction": {"stt": true`), true},
		{"a string where the list belongs", []byte(`{"providers": "a-provider"}`), true},
		{"an unknown member beside a node that admits", []byte(`{"providers": ["a-provider"], "direction": {"stt": true}, "force_local_": true}`), true},
	}
	names := []string{"a-provider", "openrouter-audio", ""}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := DecodeVoice(tc.raw)
			if !reflect.DeepEqual(v, LocalVoice()) {
				t.Fatalf("DecodeVoice(%q) answers %+v: the node's four states are one answer, local only", tc.raw, v)
			}
			if !v.LocalOnly() {
				t.Errorf("DecodeVoice(%q) answers LocalOnly() = false", tc.raw)
			}
			for _, name := range names {
				if v.AdmitsSTT(name) || v.AdmitsTTS(name) {
					t.Errorf("DecodeVoice(%q) admits %q for stt=%v tts=%v: a caller that ignores the error must still be local", tc.raw, name, v.AdmitsSTT(name), v.AdmitsTTS(name))
				}
			}
			// An absent home node is a state and not a mistake: a project that
			// never published one is served locally, and a refusal there would
			// make every caller treat the ordinary case as a failure.
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Errorf("DecodeVoice(%q) error = %v, want an error = %v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

// TestVoiceRefusesAnEntryThatIsNotAProviderName: an entry is a provider name,
// never a URL, a host or a path — a policy-carried URL is a policy-carried
// redirection (§4.3) — and the refusal is at decode rather than filtered later,
// so no reader downstream has to remember to apply it.
//
// The wildcard and the direct route are refused by name and not by the pattern,
// because both are spellings an operator copies from somewhere else in this
// tree: `"*"` is allow_data_to's marker for every repository and `"direct"` is
// routing.claude.via's own value. A refusal that called them "not a provider
// name" would send a person looking for a typo.
func TestVoiceRefusesAnEntryThatIsNotAProviderName(t *testing.T) {
	cases := []struct{ entry, want string }{
		{"https://api.example.com/v1", "not a provider name"},
		{"http://127.0.0.1:8080", "not a provider name"},
		{"api.example.com", "not a provider name"},
		{"api.example.com:443", "not a provider name"},
		{"/v1/audio/transcriptions", "not a provider name"},
		{"files/audio", "not a provider name"},
		{"OPENROUTER", "not a provider name"},
		{"-leading-dash", "not a provider name"},
		{"9leading-digit", "not a provider name"},
		{"under_score", "not a provider name"},
		{"has space", "not a provider name"},
		{"", "not a provider name"},
		{"a12345678901234567890", "not a provider name"},
		{"a-provider-that-is-too-long", "not a provider name"},
		{"*", "wildcard"},
		{"direct", "direct route"},
	}
	for _, tc := range cases {
		t.Run(strconv.Quote(tc.entry), func(t *testing.T) {
			node := fmt.Sprintf(`{"providers": [%s], "direction": {"stt": true, "tts": true}}`, jsonText(t, tc.entry))
			v, err := DecodeVoice([]byte(node))
			if err == nil {
				t.Fatalf("DecodeVoice(%s) accepts %q as a provider name", node, tc.entry)
			}
			if !strings.Contains(err.Error(), "voice.providers[0]") {
				t.Errorf("the refusal does not name the entry's own path: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal of %q does not say %q: %v", tc.entry, tc.want, err)
			}
			if !reflect.DeepEqual(v, LocalVoice()) {
				t.Errorf("a refused node answers %+v: a node this reader cannot read is a node that admits nobody", v)
			}
			if v.AdmitsSTT(tc.entry) || v.AdmitsTTS(tc.entry) {
				t.Errorf("a refused node still admits %q", tc.entry)
			}
		})
	}
}

// TestVoiceAdmitsANameTheListCarries: the list's own syntax, at its edges. An
// entry is matched exactly and never folded: internal/config matches a
// repository case-insensitively (AllowsData), while a provider name is lowercase
// by rule, so a name that differs in case is not the engine the list names.
func TestVoiceAdmitsANameTheListCarries(t *testing.T) {
	for _, name := range []string{"a", "ab", "a-1", "a1234567890123456789", "openrouter-audio"} {
		v, err := DecodeVoice([]byte(fmt.Sprintf(`{"providers": [%s], "direction": {"stt": true}}`, jsonText(t, name))))
		if err != nil {
			t.Fatalf("DecodeVoice refuses %q, which the provider name rule allows: %v", name, err)
		}
		if !v.AdmitsSTT(name) {
			t.Errorf("AdmitsSTT(%q) = false for a node whose list carries it", name)
		}
	}

	v, err := DecodeVoice([]byte(`{"providers": ["openrouter-audio", "deepseek-audio"], "direction": {"stt": true, "tts": true}}`))
	if err != nil {
		t.Fatalf("DecodeVoice: %v", err)
	}
	if want := []string{"openrouter-audio", "deepseek-audio"}; !reflect.DeepEqual(v.Providers, want) {
		t.Errorf("Providers = %v, want %v", v.Providers, want)
	}
	if !v.AdmitsTTS("deepseek-audio") {
		t.Error("AdmitsTTS(deepseek-audio) = false: tts is on and the list carries it")
	}
	for _, name := range []string{"gemini-audio", "OpenRouter-Audio", "openrouter", ""} {
		if v.AdmitsSTT(name) || v.AdmitsTTS(name) {
			t.Errorf("the list admits %q, which it does not name", name)
		}
	}
	if v.LocalOnly() {
		t.Error("LocalOnly() = true for a node that admits two engines in both directions")
	}
}

// TestVoiceDirectionsAreTwoSwitches: the two fields default false
// independently, so an edit that widens text cannot silently widen audio —
// which is the one direction whose payload no redaction rule can reach (§3).
func TestVoiceDirectionsAreTwoSwitches(t *testing.T) {
	cases := []struct {
		name             string
		members          string
		wantSTT, wantTTS bool
	}{
		{"no direction member", `"providers": ["a-provider"]`, false, false},
		{"both written false", `"providers": ["a-provider"], "direction": {"stt": false, "tts": false}`, false, false},
		{"stt only", `"providers": ["a-provider"], "direction": {"stt": true, "tts": false}`, true, false},
		{"tts only", `"providers": ["a-provider"], "direction": {"stt": false, "tts": true}`, false, true},
		{"stt written alone", `"providers": ["a-provider"], "direction": {"stt": true}`, true, false},
		{"tts written alone", `"providers": ["a-provider"], "direction": {"tts": true}`, false, true},
		{"a written null on a switch", `"providers": ["a-provider"], "direction": {"stt": null, "tts": true}`, false, true},
		{"a written null direction", `"providers": ["a-provider"], "direction": null`, false, false},
		{"an empty direction", `"providers": ["a-provider"], "direction": {}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := DecodeVoice([]byte("{" + tc.members + "}"))
			if err != nil {
				t.Fatalf("DecodeVoice({%s}): %v", tc.members, err)
			}
			if got := v.AdmitsSTT("a-provider"); got != tc.wantSTT {
				t.Errorf("AdmitsSTT(a-provider) = %v, want %v", got, tc.wantSTT)
			}
			if got := v.AdmitsTTS("a-provider"); got != tc.wantTTS {
				t.Errorf("AdmitsTTS(a-provider) = %v, want %v", got, tc.wantTTS)
			}
			if v.LocalOnly() != !(tc.wantSTT || tc.wantTTS) {
				t.Errorf("LocalOnly() = %v beside stt=%v tts=%v", v.LocalOnly(), tc.wantSTT, tc.wantTTS)
			}
		})
	}
}

// TestVoiceForceLocalWinsOverEveryMember: `force_local` is the whole of §4.1's
// Q3 answer — a project that forbids remote is served locally on every device,
// whatever that device's setting says — so a node carrying it admits nothing at
// all, in either direction, whoever asks.
//
// Its written null is refused rather than read as false, because false is the
// *wider* answer here: an operator who wrote `force_local:` with nothing under
// it meant to forbid remote, and a reader that turned that into "not forced"
// would hand the decision back to the device (ValidBinariesMaxBytes is the
// precedent, and route_fee_pct before it).
func TestVoiceForceLocalWinsOverEveryMember(t *testing.T) {
	admitted := `"providers": ["a-provider", "b-provider"], "direction": {"stt": true, "tts": true}`
	cases := []struct {
		name        string
		members     string
		wantAdmits  bool
		wantRefused bool
	}{
		{"absent", admitted, true, false},
		{"written false", admitted + `, "force_local": false`, true, false},
		{"written true", admitted + `, "force_local": true`, false, false},
		{"a written null", admitted + `, "force_local": null`, false, true},
		{"a string that reads as true", admitted + `, "force_local": "true"`, false, true},
		{"a number that reads as true", admitted + `, "force_local": 1`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := DecodeVoice([]byte("{" + tc.members + "}"))
			if tc.wantRefused {
				if err == nil {
					t.Fatalf("DecodeVoice({%s}) accepts a member whose absent state is the wider answer", tc.members)
				}
				if !reflect.DeepEqual(v, LocalVoice()) {
					t.Fatalf("a refused node answers %+v", v)
				}
			} else if err != nil {
				t.Fatalf("DecodeVoice({%s}): %v", tc.members, err)
			}
			for _, name := range []string{"a-provider", "b-provider"} {
				if got := v.AdmitsSTT(name); got != tc.wantAdmits {
					t.Errorf("AdmitsSTT(%q) = %v, want %v", name, got, tc.wantAdmits)
				}
				if got := v.AdmitsTTS(name); got != tc.wantAdmits {
					t.Errorf("AdmitsTTS(%q) = %v, want %v", name, got, tc.wantAdmits)
				}
			}
			// A device that names an engine the list does not carry is refused
			// whether or not the project forces local: the list is the grant and
			// the device's choice is never one.
			if v.AdmitsSTT("whoever-the-device-names") || v.AdmitsTTS("whoever-the-device-names") {
				t.Error("the node admits an engine its list does not name")
			}
			if v.LocalOnly() == tc.wantAdmits {
				t.Errorf("LocalOnly() = %v beside an admission of %v", v.LocalOnly(), tc.wantAdmits)
			}
		})
	}
}

// TestVoiceAnUnknownMemberIsAnErrorAndNeverAnAdmission: every member this
// reader does not model, every known member spelled in another case — which
// encoding/json folds onto the member it names — and every member written twice
// is refused, because neither mistake is visible in a decoded node: a misspelt
// `providers` is a list that admits nobody and a misspelt `force_local` is a
// policy that forces nothing, and a person has no way to tell either from the
// silence of a project that set nothing.
//
// Each node below is one that would admit an engine if the reader dropped what
// it could not read, which is what makes them the cases worth pinning.
//
// The last two are shapes the database really hands out and a document does
// not: an RTDB list is an object with numeric keys, so a *sparse* list arrives
// as an object and is refused rather than guessed at, and a top-level array is
// not the node's shape at all.
func TestVoiceAnUnknownMemberIsAnErrorAndNeverAnAdmission(t *testing.T) {
	for _, tc := range []struct{ name, node string }{
		{"a misspelt providers", `{"provider": ["a-provider"], "direction": {"stt": true}}`},
		{"a misspelt direction", `{"providers": ["a-provider"], "direciton": {"stt": true}}`},
		{"a misspelt force_local", `{"providers": ["a-provider"], "direction": {"stt": true}, "force_local_": true}`},
		{"an unknown member beside a node that admits", `{"providers": ["a-provider"], "direction": {"stt": true}, "fallback": true}`},
		{"an unknown member inside direction", `{"providers": ["a-provider"], "direction": {"stt": true, "wav": true}}`},
		{"a known member in another case at the top", `{"providers": ["a-provider"], "PROVIDERS": ["b-provider"], "direction": {"stt": true}}`},
		{"a known member in another case inside direction", `{"providers": ["a-provider"], "direction": {"stt": true, "STT": false}}`},
		{"force_local in another case", `{"providers": ["a-provider"], "direction": {"stt": true}, "Force_Local": false}`},
		{"a member written twice", `{"providers": ["a-provider"], "providers": ["b-provider"], "direction": {"stt": true}}`},
		{"direction written twice", `{"providers": ["a-provider"], "direction": {"stt": true}, "direction": {"tts": true}}`},
		{"an entry written twice", `{"providers": ["a-provider", "a-provider"], "direction": {"stt": true}}`},
		{"the node is not an object", `["a-provider"]`},
		{"an entry of the wrong type", `{"providers": [1], "direction": {"stt": true}}`},
		{"a sparse list", `{"providers": {"1": "a-provider"}, "direction": {"stt": true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := DecodeVoice([]byte(tc.node))
			if err == nil {
				t.Fatalf("DecodeVoice(%s) accepts the node", tc.node)
			}
			if !reflect.DeepEqual(v, LocalVoice()) {
				t.Fatalf("DecodeVoice(%s) answers %+v beside its error: an error is never an admission", tc.node, v)
			}
			for _, name := range []string{"a-provider", "b-provider"} {
				if v.AdmitsSTT(name) || v.AdmitsTTS(name) {
					t.Errorf("the refused node admits %q", name)
				}
			}
		})
	}
}

// TestVoiceTheDefaultIsTheOppositeOfAllowDataTos: the design's list is
// `allow_data_to`'s syntax with the default inverted (§4.3), and the inversion
// is the whole of D44's requirement — in internal/config an absent list is the
// wide answer (every repository) while here an absent list is the narrow one
// (nobody). The two zeros are asserted side by side, so a reader who carries
// the other default across is caught by the node's own test rather than by a
// microphone.
func TestVoiceTheDefaultIsTheOppositeOfAllowDataTos(t *testing.T) {
	if !(config.ModelProvider{}).AllowsData("owner/repo") {
		t.Fatal("an absent allow_data_to no longer means every repository: internal/config's own default moved, and this node's inversion has to be re-read against it rather than assumed")
	}
	if v := LocalVoice(); !v.LocalOnly() || v.AdmitsSTT("openrouter-audio") || v.AdmitsTTS("openrouter-audio") {
		t.Errorf("LocalVoice() = %+v: the node's zero must admit nobody", v)
	}

	// An empty list is the same answer and not a refusal: it is the node's
	// comment's own spelling ("absent or [] => local only"), and the two are
	// one write in the database besides, because a written [] leaves no member
	// at all (decodeBinaries' note).
	v, err := DecodeVoice([]byte(`{"providers": [], "direction": {"stt": true, "tts": true}, "force_local": false}`))
	if err != nil {
		t.Fatalf("a written empty list is a state and not a refusal: %v", err)
	}
	if !v.LocalOnly() || v.AdmitsSTT("openrouter-audio") || v.AdmitsTTS("openrouter-audio") {
		t.Errorf("an empty providers list answers %+v: it admits nobody", v)
	}
}

// TestVoiceProviderNamesAreConfigsProviderNames: the list's entry rule is
// internal/config's provider name rule (its providerNameRE, unexported there
// and mirrored in two JSON schemas), and this package's copy of it is held to
// the same answer here rather than to a comment.
//
// The mutation is the pattern changed by one character in either direction — a
// trailing bound of 19 widened to 29, say: this test then names the first
// candidate the two rules disagree about.
func TestVoiceProviderNamesAreConfigsProviderNames(t *testing.T) {
	for _, name := range []string{
		"a", "ab", "openrouter", "deepseek-audio", "a1234567890123456789",
		"", "A", "OPENROUTER", "-dash", "9digit", "under_score", "has space",
		"a12345678901234567890", "https://api.example.com/v1", "api.example.com",
		"/v1", "*", "direct",
	} {
		mine := validVoiceProviders([]string{name}) == nil
		if theirs := !configRefusesTheProviderName(name); mine != theirs {
			t.Errorf("the two rules disagree about %q: the voice list accepts it = %v, internal/config's providers block accepts it = %v", name, mine, theirs)
		}
	}
	if want := `^[a-z][a-z0-9-]{0,19}$`; voiceProviderNameRE.String() != want {
		t.Errorf("voiceProviderNameRE = %s, want %s (internal/config's provider name rule, which this node's entries are held to)", voiceProviderNameRE, want)
	}
}

// configRefusesTheProviderName reports whether internal/config's providers
// block refuses name as a provider entry's own name. It reads the problems at
// the entry's own path: every other problem that block reports about a provider
// is spelled under a member ("providers.<name>.models", "…secret"), so a
// problem at "providers.<name>" itself is a problem with the name.
func configRefusesTheProviderName(name string) bool {
	ps := config.ValidateModelProviders(map[string]config.ModelProvider{name: {Models: []string{"openrouter/*"}}})
	for _, p := range ps {
		if p.Path == "providers."+name {
			return true
		}
	}
	return false
}

// TestVoiceARefusalPastesNoDocumentIntoALog: a refused entry is quoted back,
// and the node is a person's document, so the refusal is bounded and carries no
// control character — a hand-edited node is not allowed to start a line of its
// own in a log or in the hop's answer (nodeText's own argument, one member
// over, and internal/gateway's callLog is the precedent for the rule).
//
// The mutation is voiceEntryText dropping its bound: the four-thousand
// character entry then lands in the refusal whole.
func TestVoiceARefusalPastesNoDocumentIntoALog(t *testing.T) {
	for _, entry := range []string{strings.Repeat("a", 4000), "a\nprovider", "a\tprovider", "\x00a", strings.Repeat("é", 4000)} {
		node := fmt.Sprintf(`{"providers": [%s]}`, jsonText(t, entry))
		_, err := DecodeVoice([]byte(node))
		if err == nil {
			t.Fatalf("DecodeVoice accepts %d bytes of %q as a provider name", len(entry), entry[:min(len(entry), 8)])
		}
		if strings.ContainsAny(err.Error(), "\n\r\t\x00") {
			t.Errorf("the refusal carries a control character: %q", err.Error())
		}
		if strings.Contains(err.Error(), entry) {
			t.Errorf("the refusal carries the whole %d-byte entry", len(entry))
		}
		if len([]rune(entry)) > 48 && !strings.Contains(err.Error(), "…") {
			t.Errorf("the refusal of a %d-rune entry does not show that it was cut: %q", len([]rune(entry)), err.Error())
		}
		if len(err.Error()) > 600 {
			t.Errorf("the refusal of a %d-byte entry is %d bytes: an entry is not allowed to paste a document into a log", len(entry), len(err.Error()))
		}
	}
}

// TestVoiceFixtures decodes the two nodes an operator publishes: one project's
// own (testdata/voice-repo.json, whose members are the shape the P5.3.1
// emulator writes) and the fallback list every project of an installation reads
// (testdata/voice-shared.json).
func TestVoiceFixtures(t *testing.T) {
	repo, err := DecodeVoice(readVoiceFixture(t, "voice-repo.json"))
	if err != nil {
		t.Fatalf("DecodeVoice(voice-repo.json): %v", err)
	}
	if !repo.AdmitsSTT("openrouter-audio") {
		t.Error("the fixture's stt switch is on and names openrouter-audio, which the reader does not admit")
	}
	if repo.AdmitsTTS("openrouter-audio") {
		t.Error("the fixture's tts switch is off, so the reader must not admit a remote text-to-speech call")
	}
	if repo.LocalOnly() {
		t.Error("LocalOnly() = true for a project that admits remote speech-to-text")
	}

	shared, err := DecodeVoice(readVoiceFixture(t, "voice-shared.json"))
	if err != nil {
		t.Fatalf("DecodeVoice(voice-shared.json): %v", err)
	}
	if want := []string{"deepseek-audio", "openrouter-audio"}; !reflect.DeepEqual(shared.Providers, want) {
		t.Errorf("the shared node's providers = %v, want %v", shared.Providers, want)
	}
	// The shared node names no direction, so it admits nobody on its own: the
	// fallback list is a list of engines and never a switch, and a project that
	// turns neither direction on falls back to local.
	for _, name := range shared.Providers {
		if shared.AdmitsSTT(name) || shared.AdmitsTTS(name) {
			t.Errorf("the shared node admits %q, and it writes no direction member", name)
		}
	}
	if !shared.LocalOnly() {
		t.Error("LocalOnly() = false for a node whose two switches are absent")
	}
}

func readVoiceFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return raw
}

// jsonText renders one value as the JSON a document writes it as, so a test's
// own entries carry the escapes a node has to carry rather than Go's.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshalling %q: %v", s, err)
	}
	return string(b)
}
