package voicesvc

// The register's own tests: the document ships in this binary, so what is
// checked here is that it says what the design's §7 says and that its shape
// rules hold — a column that was read carries the page behind it, a column that
// was not carries the mark, and the names and directions the register holds are
// the ones this hop can actually call.

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestRegisterHoldsTheThreeEnginesOfTheDesign: §7's three candidates, in the
// names a policy node spells, and OpenAI deliberately absent — its terms could
// not be read on 2026-10-19 (the page refused the fetch), and a register that
// admitted an engine whose retention nobody read would be the opposite of what
// the register is for.
func TestRegisterHoldsTheThreeEnginesOfTheDesign(t *testing.T) {
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	want := []string{"google", "deepgram", "elevenlabs"}
	if got := reg.Names(); !equalStrings(got, want) {
		t.Errorf("the register holds %v, want %v (the voice design's §7)", got, want)
	}
	for _, absent := range []string{"openai", "anthropic", "whisper"} {
		if row, ok := reg.Row(absent); ok {
			t.Errorf("the register holds a row for %q (%s): §7 read no terms for it on the read date, and a row here is what admits a call", absent, row.Provider)
		}
	}
}

// TestRegisterColumnsSayWhatWasRead: every column is one of the two states, a
// read column names the page it was read from, and an unread one carries the
// mark in the value a person reading the file sees.
func TestRegisterColumnsSayWhatWasRead(t *testing.T) {
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	for _, name := range reg.Names() {
		row, _ := reg.Row(name)
		if _, err := time.Parse(readDateFormat, row.ReadAt); err != nil {
			t.Errorf("%s: read_at %q is not a date: %v", name, row.ReadAt, err)
		}
		read, unverified := 0, 0
		for _, col := range columns(row.Terms) {
			switch col.term.State {
			case TermRead:
				read++
				if !strings.HasPrefix(col.term.Source, "https://") {
					t.Errorf("%s %s: a column that was read names no page: %q", name, col.name, col.term.Source)
				}
				if col.term.Value == "" {
					t.Errorf("%s %s: a column that was read carries no value", name, col.name)
				}
			case TermUnverified:
				unverified++
				if col.term.Source != "" {
					t.Errorf("%s %s: a column nobody read names a source (%q): a claim and a mark of nothing are different answers", name, col.name, col.term.Source)
				}
				if !strings.HasPrefix(col.term.Value, unverifiedMark) {
					t.Errorf("%s %s: an unread column's value does not carry %s: %q", name, col.name, unverifiedMark, col.term.Value)
				}
			default:
				t.Errorf("%s %s: the state is %q, which is neither %s nor %s", name, col.name, col.term.State, TermRead, TermUnverified)
			}
		}
		if read+unverified != 5 {
			t.Errorf("%s: %d columns were read and %d were not, want 5 between them (§7's register has five)", name, read, unverified)
		}
	}
}

// TestRegisterRecordsTheTermsSectionSevenRead: the figures and sentences §7
// quotes are the ones the register carries, so the file cannot drift from the
// design it is the record of. It pins the handful a person would notice going
// missing, and it does not pin the prose.
func TestRegisterRecordsTheTermsSectionSevenRead(t *testing.T) {
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	for _, tc := range []struct {
		name, col, state, want string
	}{
		{"google", "price", TermRead, "US$0.00003 per character"},
		{"google", "languages", TermRead, "ro-RO"},
		{"google", "retention", TermRead, "does not log customer audio data or transcripts"},
		{"deepgram", "price", TermRead, "US$0.0065/min"},
		// §7 could not retrieve Deepgram's page on the read date: the row says
		// why and claims no retention, which is the answer the design asks for.
		{"deepgram", "retention", TermUnverified, "404"},
		{"elevenlabs", "retention", TermRead, "as long as necessary"},
	} {
		row, ok := reg.Row(tc.name)
		if !ok {
			t.Fatalf("the register holds no row for %s", tc.name)
		}
		got := ""
		for _, col := range columns(row.Terms) {
			if col.name == tc.col {
				if col.term.State != tc.state {
					t.Errorf("%s %s is %s, want %s: §7 records what it read and marks what it did not", tc.name, tc.col, col.term.State, tc.state)
				}
				got = col.term.Value
			}
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s %s does not carry %q:\n%s", tc.name, tc.col, tc.want, got)
		}
	}
	// §7 read no latency figure for any of the three: "ultra-low latency" is
	// the vendor's phrase and not a number, so every row marks the column.
	for _, name := range reg.Names() {
		row, _ := reg.Row(name)
		if row.Terms.Latency.State != TermUnverified {
			t.Errorf("%s latency is %s, want %s: §7 read no figure for any provider, and the design says no number is inferred, averaged or guessed", name, row.Terms.Latency.State, TermUnverified)
		}
	}
}

// TestRegisterServesTheDirectionsTheHopCanCall: the register's own declaration
// of what this hop forwards, which is the gate's answer.
func TestRegisterServesTheDirectionsTheHopCanCall(t *testing.T) {
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	for _, tc := range []struct {
		name    string
		stt     bool
		tts     bool
		because string
	}{
		{"deepgram", true, false, "the audio direction is the one this provider is here for; the text direction has no engine here, so it is refused at the gate"},
		{"google", false, true, "Google's streaming audio path is the v2 gRPC API and this hop's callers speak HTTP, so text is the direction it serves here (P5.3.5's reported finding)"},
		{"elevenlabs", false, true, "§7 proposes it for the text direction only"},
	} {
		row, ok := reg.Row(tc.name)
		if !ok {
			t.Fatalf("the register holds no row for %s", tc.name)
		}
		if got := row.Serves(STT); got != tc.stt {
			t.Errorf("%s serves stt = %v, want %v: %s", tc.name, got, tc.stt, tc.because)
		}
		if got := row.Serves(TTS); got != tc.tts {
			t.Errorf("%s serves tts = %v, want %v: %s", tc.name, got, tc.tts, tc.because)
		}
	}
}

// TestRegisterAndEnginesAgree: the register says which names and directions
// this hop holds, and the engine table says how each one is reached. The two are
// one answer, and this is what holds them to it — a register row for a direction
// no engine serves would pass the gate and then have nothing to call, and an
// engine no register row serves would be a call this service could make and never
// admit.
func TestRegisterAndEnginesAgree(t *testing.T) {
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	for _, name := range reg.Names() {
		row, _ := reg.Row(name)
		for _, d := range []Direction{STT, TTS} {
			_, held := engineFor(name, d)
			if held != row.Serves(d) {
				t.Errorf("%s %s: the register serves it = %v and an engine exists = %v, and the two must be one answer", name, d, row.Serves(d), held)
			}
		}
	}
	for _, e := range engines {
		row, ok := reg.Row(e.name)
		if !ok {
			t.Errorf("the engine table holds %s (%s) and the register has no row for it", e.name, e.direction)
			continue
		}
		if !row.Serves(e.direction) {
			t.Errorf("the engine table holds %s (%s) and the register's row does not serve that direction: the gate would refuse a call this service can make", e.name, e.direction)
		}
	}
}

// TestRegisterNamesAreThePolicyNodesNames: a name this file holds and the policy
// could not name would be an engine no project can admit, and a name the policy
// admits and this file does not hold is a call refused at the gate — so the two
// patterns are one rule, and this is what holds them to it rather than a comment
// (projectpolicy's voiceProviderNameRE is unexported, so a node is what this
// reads the rule from).
//
// What is compared is the SHAPE, and `direct` is deliberately not in the list:
// DecodeVoice refuses that one name for a reason of its own — it is the routing
// switch's value for the direct route and not a provider — which is a fact about
// the switch's vocabulary and not about the name's shape. No row here is named
// it, and TestRegisterHoldsTheThreeEnginesOfTheDesign is what says so.
func TestRegisterNamesAreThePolicyNodesNames(t *testing.T) {
	for _, name := range []string{
		"google", "deepgram", "elevenlabs", "a", "a0", "a-b", strings.Repeat("a", 20), "voice-2",
		"", "A", "-a", "0a", "a_b", "a.b", "a b", strings.Repeat("a", 21),
		"http://x", "*", "Deepgram", "a/b", "a;b", "a\nb", "café",
	} {
		node := `{"providers":[` + strconv.Quote(name) + `],"direction":{"stt":true}}`
		_, err := projectpolicy.DecodeVoice([]byte(node))
		policyAdmits := err == nil
		if got := registerNameRE.MatchString(name); got != policyAdmits {
			t.Errorf("name %q: the register's pattern admits it = %v and the policy node admits it = %v (%v); the two are one rule", name, got, policyAdmits, err)
		}
	}
	if _, err := projectpolicy.DecodeVoice([]byte(`{"providers":["direct"],"direction":{"stt":true}}`)); err == nil {
		t.Error("the policy node now admits `direct` as a provider name: the register's pattern does, so a row named it would be an engine a project could list — and a call admitted to it would read as a call that went direct")
	}
}

// registerDoc is one valid row, the basis the refusals below break one rule at a
// time. It is deliberately in the shape the file has and carries values no
// provider states, because it is a shape fixture and not a claim.
const registerDoc = `{"rows":[{"name":"deepgram","provider":"Deepgram","directions":["stt"],"read_at":"2026-10-19",
	"terms":{"retention":{"state":"unverified","value":"UNVERIFIED: not read."},
	"training":{"state":"unverified","value":"UNVERIFIED: not read."},
	"price":{"state":"read","value":"US$1","source":"https://example.test/p"},
	"languages":{"state":"unverified","value":"UNVERIFIED: not read."},
	"latency":{"state":"unverified","value":"UNVERIFIED: not read."}}}]}`

// TestParseRegisterRefusesAShapeItCannotRead: the file ships in the image, so a
// failure here is this tree's own — and each rule below is one a hand-edited
// register would have to break to lose a fact silently. Each mutation is applied
// to the fixture above, which parses on its own (TestParseRegisterReadsTheFixture).
func TestParseRegisterRefusesAShapeItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"no rows", `{"rows":[]}`},
		{"a member of a term this release does not model",
			strings.Replace(registerDoc, `"state":"read"`, `"state":"read","confidence":0.9`, 1)},
		{"a member of a row this release does not model",
			strings.Replace(registerDoc, `"provider":"Deepgram"`, `"provider":"Deepgram","voice":"x"`, 1)},
		{"a name that is not an engine name", strings.Replace(registerDoc, `"name":"deepgram"`, `"name":"Deepgram"`, 1)},
		{"a name with an underscore", strings.Replace(registerDoc, `"name":"deepgram"`, `"name":"deep_gram"`, 1)},
		{"a direction that is not one", strings.Replace(registerDoc, `"directions":["stt"]`, `"directions":["audio"]`, 1)},
		{"the same direction twice", strings.Replace(registerDoc, `"directions":["stt"]`, `"directions":["stt","stt"]`, 1)},
		{"the same engine twice", strings.Replace(registerDoc, `{"rows":[`, `{"rows":[`+`{"name":"deepgram","provider":"Deepgram","directions":["stt"],"read_at":"2026-10-19","terms":{"retention":{"state":"unverified","value":"UNVERIFIED."},"training":{"state":"unverified","value":"UNVERIFIED."},"price":{"state":"unverified","value":"UNVERIFIED."},"languages":{"state":"unverified","value":"UNVERIFIED."},"latency":{"state":"unverified","value":"UNVERIFIED."}}},`, 1)},
		{"no provider", strings.Replace(registerDoc, `"provider":"Deepgram"`, `"provider":""`, 1)},
		{"no directions", strings.Replace(registerDoc, `"directions":["stt"]`, `"directions":[]`, 1)},
		{"no read_at", strings.Replace(registerDoc, `"read_at":"2026-10-19"`, `"read_at":""`, 1)},
		{"a read_at that is not a date", strings.Replace(registerDoc, `"2026-10-19"`, `"19-10-2026"`, 1)},
		{"a read_at that is a timestamp", strings.Replace(registerDoc, `"2026-10-19"`, `"2026-10-19T00:00:00Z"`, 1)},
		{"a column read with no source", strings.Replace(registerDoc, `,"source":"https://example.test/p"`, ``, 1)},
		{"a column read with no value", strings.Replace(registerDoc, `"value":"US$1"`, `"value":""`, 1)},
		{"an unread column without the mark", strings.Replace(registerDoc, `"value":"UNVERIFIED: not read."`, `"value":"not read"`, 1)},
		{"a state that is not one", strings.Replace(registerDoc, `"state":"read"`, `"state":"maybe"`, 1)},
		{"no state at all", strings.Replace(registerDoc, `"state":"read"`, `"state":""`, 1)},
		{"two documents", registerDoc + registerDoc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.doc == registerDoc && tc.name != "two documents" {
				t.Fatalf("the fixture was mutated to itself, so this case proves nothing:\n%s", tc.doc)
			}
			if _, err := ParseRegister([]byte(tc.doc)); err == nil {
				t.Fatalf("the register parsed, and it should not have:\n%s", tc.doc)
			}
		})
	}
}

// TestParseRegisterReadsTheFixture: the fixture above parses, so every refusal
// in the table is the one mutation and not the document it was applied to. A
// rule table whose basis does not parse passes for the wrong reason.
func TestParseRegisterReadsTheFixture(t *testing.T) {
	reg, err := ParseRegister([]byte(registerDoc))
	if err != nil {
		t.Fatalf("the fixture does not parse, so the refusals above prove nothing: %v", err)
	}
	row, ok := reg.Row("deepgram")
	if !ok {
		t.Fatal("the fixture parses and holds no deepgram row")
	}
	if !row.Serves(STT) || row.Serves(TTS) {
		t.Errorf("the fixture's deepgram row serves stt = %v, tts = %v, want stt only", row.Serves(STT), row.Serves(TTS))
	}
}

// TestRegisterCarriesNoAddress: §8's rule is that the hop resolves a NAME and
// never an address, and no policy node may carry one. The register is this
// tree's own file and is where the address does live — but it lives in
// provider.go's constants, in code a reviewer reads, and not in this document:
// a URL in a file an operator is invited to edit is a URL that could be pointed
// anywhere, and the gate would still admit the call.
func TestRegisterCarriesNoAddress(t *testing.T) {
	for _, name := range []string{"google", "deepgram", "elevenlabs"} {
		row := loadRegisterRow(t, name)
		if strings.Contains(row.Note, "://") {
			t.Errorf("%s: the row's note carries a URL (%q); the register is a record of terms, and the endpoints are provider.go's constants", name, row.Note)
		}
		for _, col := range columns(row.Terms) {
			if col.term.State == TermRead && !strings.HasPrefix(col.term.Source, "https://") {
				t.Errorf("%s %s: the source is not a page: %q", name, col.name, col.term.Source)
			}
		}
	}
}

// loadRegisterRow is the register row for a name, and it stops the test when the
// register does not hold one — the rows above are the design's three, and a test
// that skipped a missing one would pass over the register losing it.
func loadRegisterRow(t *testing.T, name string) RegisterRow {
	t.Helper()
	reg, err := LoadRegister()
	if err != nil {
		t.Fatalf("the register in this binary does not load: %v", err)
	}
	row, ok := reg.Row(name)
	if !ok {
		t.Fatalf("the register holds no row for %s", name)
	}
	return row
}

// equalStrings compares two string slices.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
