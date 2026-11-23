package mailbox

// The parity corpus's replay (mobile plan, Task P4.1 T8).
//
// `shared/mailbox` is the module this package binds, and Go cannot import it, so
// the binding is held to the module by committed data (ruling M9). Where the
// module has a vector file, that file is the contract; `shared/mailbox` has
// none, so what holds the two ends together here is
// `internal/mailbox/testdata/parity.json` — the module's **outcomes and codes**,
// which no vector file carries — written by `shared/mailbox/parity.generate.mjs`
// and read back by `shared/mailbox/parity.test.js`. It is replayed below through
// `Ledger.Perform` and `Ledger.Ack` with no Node, no network and no clock.
//
// Three checks, and each catches something the others cannot:
//
//   - TestParityCorpusReplay: every scenario, outcome by outcome, and the state
//     it ends in — 120 seeded sweeps (three messages, two consumers, twelve
//     passes) driven through `shared/mailbox-sim`, plus the cases a sweep cannot
//     reach: the refusals a delivery speaks, and the nine states the module
//     refuses to read at all.
//   - TestParityHash: the corpus has not been edited by hand. An edited corpus
//     is one nobody generated, which is what would make a green replay mean
//     nothing at all.
//   - TestParityCorpusIsCurrent: the committed corpus is what the generator
//     writes today — the only one of the three that notices a change to the
//     *module* that nobody regenerated for. It needs a `node`, so it skips where
//     there is none, with the reason recorded (the base image and CI's test job
//     carry none); the hash and the replay run everywhere.
//
// The generator is a script rather than a test for one reason, and it is the
// reason the split is worth having: `shared/mailbox/mutation-proof.sh` restores
// `index.js` and nothing else, so a test that regenerated the corpus would leave
// the mutated module's answers committed in the working tree.
//
// **The red steps that were run and watched fail** (the task's own):
//
//   - One outcome hand-edited in `parity.json` (a `duplicate` answered as
//     `acted`): TestParityHash fails with "the corpus has been edited by hand",
//     and TestParityCorpusReplay fails on that scenario alone — the two checks
//     are independent, which is the point of having both.
//   - A constant changed in `shared/mailbox/index.js` without regenerating
//     (DEFAULT_WINDOW_MS by an hour): TestParityCorpusIsCurrent fails with the
//     first line that differs between the committed corpus and the regenerated
//     one, and TestParityCorpusReplay fails on the scenarios whose window moved.
//     Restored; see the pull request.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

const parityCorpusPath = "testdata/parity.json"

// parityGeneratePath is the writer, from the package directory: the drift check
// runs it and the corpus names it, and the two must be the same file.
const parityGeneratePath = "../../shared/mailbox/parity.generate.mjs"

// parityRegenerate is what every failure here tells the reader to run.
const parityRegenerate = "node shared/mailbox/parity.generate.mjs"

// The corpus, decoded strictly: a field the generator adds is a field this
// replay has to be taught, never one it silently drops.
type parityCorpus struct {
	Module    string            `json:"module"`
	Generator string            `json:"generator"`
	Simulator string            `json:"simulator"`
	Replay    string            `json:"replay"`
	Options   parityOptions     `json:"options"`
	Outcomes  []string          `json:"outcomes"`
	Codes     map[string]string `json:"codes"`
	Scenarios []parityScenario  `json:"scenarios"`
	Hash      string            `json:"hash"`
}

// parityOptions is the module's own five numbers, as the corpus carries them.
// They are the caller's configuration in Go (LedgerOptions), so this is the
// shape of what a caller passes rather than a state the ledger stores.
type parityOptions struct {
	WindowMs          int64 `json:"windowMs"`
	PromptTtlMs       int64 `json:"promptTtlMs"`
	MaxSeen           int   `json:"maxSeen"`
	MaxPerSender      int   `json:"maxPerSender"`
	FutureToleranceMs int64 `json:"futureToleranceMs"`
}

// parityScenario is one case: the state a pass starts from, the events the store
// committed in order, and — for the seeded sweeps — the simulator's own schedule
// text, which is what a failure prints so the case can be replayed without the
// corpus.
type parityScenario struct {
	Name         string         `json:"name"`
	Seed         *int64         `json:"seed"`
	ScheduleText *string        `json:"scheduleText"`
	Options      *parityOptions `json:"options"`
	State        parityState    `json:"state"`
	StateRefusal string         `json:"stateRefusal"`
	Events       []parityEvent  `json:"events"`
	Final        *parityState   `json:"final"`
}

// parityState is the ledger's state as the corpus writes it: the three fields the
// file carries, and the phase is derived rather than stored on both sides.
//
// It keeps its own bytes as well, because the state is handed to `OpenLedger` as
// a ledger **file** and one reader case is a `seen` of `null` rather than `[]` —
// the one shape no Go struct can tell from an empty ledger, and the one the
// module's own `invalid-seen` is about.
type parityState struct {
	Now            int64         `json:"now"`
	EvictedThrough int64         `json:"evictedThrough"`
	Seen           []parityEntry `json:"seen"`
	raw            []byte
}

func (s *parityState) UnmarshalJSON(data []byte) error {
	type plain parityState
	var decoded plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	*s = parityState(decoded)
	s.raw = append([]byte(nil), data...)
	return nil
}

// parityEntry is one record of the ledger, in the module's shape.
type parityEntry struct {
	Sender string `json:"sender"`
	ID     string `json:"id"`
	At     int64  `json:"at"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// parityEvent is one call the store committed, with the answer the module gave
// it: the outcome and the pair it is about, or the refusal's code.
type parityEvent struct {
	Op      string         `json:"op"`
	Now     int64          `json:"now"`
	Kind    string         `json:"kind"`
	Message *parityMessage `json:"message"`
	Outcome string         `json:"outcome"`
	Code    string         `json:"code"`
	ID      string         `json:"id"`
	Sender  string         `json:"sender"`
}

// parityMessage is the input record of a delivery: what the phone wrote. The
// message's mac is deliberately not here — the ledger reads the pair, the `at`
// and the kind, and never a tag (T2's).
type parityMessage struct {
	ID     string `json:"id"`
	Sender string `json:"sender"`
	At     int64  `json:"at"`
	Text   string `json:"text"`
}

var parityHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// loadParityCorpus reads the committed corpus, strictly, and answers it with the
// bytes it was read from.
func loadParityCorpus(t *testing.T) (*parityCorpus, []byte) {
	t.Helper()
	data, err := os.ReadFile(parityCorpusPath)
	if err != nil {
		t.Fatalf("reading the mailbox parity corpus: %v (generate it with `%s` and commit it)", err, parityRegenerate)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var corpus parityCorpus
	if err := dec.Decode(&corpus); err != nil {
		t.Fatalf("decoding the mailbox parity corpus: %v", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatalf("the mailbox parity corpus carries more than one document: the format is the version, and a second document is not one this replay can read")
	}
	return &corpus, data
}

// parityWhere is the context a failure carries: the case and, for a seeded one,
// the simulator's own schedule text, so a failing case is replayable from the
// failure alone. The seed is in the name the generator gives a seeded case
// ("seed 7"), and is printed beside it where a case is named otherwise.
func parityWhere(scenario parityScenario) string {
	where := scenario.Name
	if scenario.Seed != nil && scenario.Name != fmt.Sprintf("seed %d", *scenario.Seed) {
		where += fmt.Sprintf(" (seed %d)", *scenario.Seed)
	}
	if scenario.ScheduleText != nil {
		where += "\nschedule: " + *scenario.ScheduleText
	}
	return where
}

// TestParityCorpusReplay is M9's replay for this package: for every scenario the
// module's generator wrote, this package must give the module's answer — the same
// outcome and the same pair, or the same refusal code — and end in the same
// state.
//
// The seeded half is the plan's verification line: 120 schedules of three
// messages, two consumers and twelve passes each, replayed in Go and compared
// outcome by outcome. A failing case prints its seed and its schedule.
func TestParityCorpusReplay(t *testing.T) {
	corpus, _ := loadParityCorpus(t)
	if len(corpus.Scenarios) < 150 {
		t.Fatalf("the corpus carries %d scenarios: the file, not the replay, is what changed", len(corpus.Scenarios))
	}
	seeded := 0
	seeds := map[int64]bool{}
	for _, scenario := range corpus.Scenarios {
		if scenario.Seed == nil {
			if scenario.ScheduleText != nil {
				t.Fatalf("%s: a hand-built case carries a schedule", scenario.Name)
			}
			continue
		}
		seeded++
		if seeds[*scenario.Seed] {
			t.Fatalf("%s: the corpus repeats seed %d", scenario.Name, *scenario.Seed)
		}
		seeds[*scenario.Seed] = true
		var schedule struct {
			Seed   *int64 `json:"seed"`
			Faults []any  `json:"faults"`
		}
		if scenario.ScheduleText == nil {
			t.Fatalf("%s: a seeded case carries no schedule, and a failure could not print one", scenario.Name)
		}
		if err := json.Unmarshal([]byte(*scenario.ScheduleText), &schedule); err != nil {
			t.Fatalf("%s: the schedule is not the simulator's text: %v", scenario.Name, err)
		}
		if schedule.Seed == nil || *schedule.Seed != *scenario.Seed {
			t.Fatalf("%s: the schedule is another run's", scenario.Name)
		}
		// A schedule may draw no fault at all (seed 20 does), so what is checked
		// is that the sweep's own fields are there rather than that it drew one.
		if schedule.Faults == nil {
			t.Fatalf("%s: the schedule carries no faults list, so it is not the simulator's text", scenario.Name)
		}
	}
	if seeded != 120 {
		t.Fatalf("the corpus carries %d seeded schedules, and the plan's verification names 120", seeded)
	}

	for _, scenario := range corpus.Scenarios {
		scenario := scenario
		t.Run(scenario.Name, func(t *testing.T) {
			options := corpus.Options
			if scenario.Options != nil {
				options = *scenario.Options
			}
			path := filepath.Join(t.TempDir(), "ledger.json")
			if len(scenario.State.raw) == 0 {
				t.Fatalf("%s: the corpus carries no state to open a ledger over", parityWhere(scenario))
			}
			if err := os.WriteFile(path, scenario.State.raw, 0o600); err != nil {
				t.Fatalf("%s: writing the state as a ledger file: %v", parityWhere(scenario), err)
			}
			ledger, err := OpenLedger(parityLedgerOptions(path, options))
			if scenario.StateRefusal != "" {
				if len(scenario.Events) != 0 || scenario.Final != nil {
					t.Fatalf("%s: a state that cannot be read carries events or a final state", parityWhere(scenario))
				}
				if err == nil {
					t.Fatalf("%s: the state was read, and the module refuses it %s", parityWhere(scenario), scenario.StateRefusal)
				}
				if code := ledgerRefusalCode(t, err); string(code) != scenario.StateRefusal {
					t.Fatalf("%s: the module refuses the state %s, and this ledger refuses it %s", parityWhere(scenario), scenario.StateRefusal, code)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: opening the ledger over the corpus's state: %v", parityWhere(scenario), err)
			}
			for index, event := range scenario.Events {
				driveParityEvent(t, scenario, ledger, index, event)
			}
			if scenario.Final == nil {
				t.Fatalf("%s: the corpus carries no final state, so the replay has nothing to end against", parityWhere(scenario))
			}
			compareParityState(t, scenario, ledger, *scenario.Final)
		})
	}
}

// driveParityEvent is one call, as the Go binding takes it: `perform` is
// `Ledger.Perform` — the delivery and, for one it takes, the act that follows it
// in the same pass — and `ack` is `Ledger.Ack`. The corpus's answer is the
// module's decision, so this is the whole comparison.
func driveParityEvent(t *testing.T, scenario parityScenario, ledger *Ledger, index int, event parityEvent) {
	t.Helper()
	var result PerformResult
	var err error
	switch event.Op {
	case "perform":
		if event.Message == nil {
			t.Fatalf("%s: event %d is a delivery with no message", parityWhere(scenario), index)
		}
		message := ledgerMessage(event.Message.ID, event.Message.Sender, event.Message.Text, event.Message.At)
		result, err = ledger.Perform(message, event.Kind, event.Now, nil)
	case "ack":
		result, err = ledger.Ack(event.Now)
	default:
		t.Fatalf("%s: event %d carries the operation %q, which this replay does not know", parityWhere(scenario), index, event.Op)
	}
	if err != nil {
		t.Fatalf("%s: event %d (%s at %d): the ledger failed: %v", parityWhere(scenario), index, event.Op, event.Now, err)
	}
	where := fmt.Sprintf("%s\nevent %d (%s at %d)", parityWhere(scenario), index, event.Op, event.Now)
	switch {
	case event.Code != "":
		if result.Refusal == nil {
			t.Fatalf("%s: the module refuses it %s, and this ledger answered %q", where, event.Code, result.Outcome)
		}
		if string(result.Refusal.Code) != event.Code {
			t.Fatalf("%s: the module refuses it %s, and this ledger refuses it %s", where, event.Code, result.Refusal.Code)
		}
	case result.Refusal != nil:
		t.Fatalf("%s: the module answers %q, and this ledger refuses it %s", where, event.Outcome, result.Refusal.Code)
	default:
		if result.Outcome != event.Outcome {
			t.Fatalf("%s: the module answers %q, and this ledger answers %q", where, event.Outcome, result.Outcome)
		}
		// The pair the answer is about. A delivery the module took is folded into
		// the act that follows it, so the pair is the acted record's — which for
		// an older accepted record is not the message this event carried, and
		// that is exactly what the corpus pins.
		if result.Entry.ID != event.ID || result.Entry.Sender != event.Sender {
			t.Fatalf("%s: the module's answer is about (%s, %s), and this ledger's is about (%s, %s)",
				where, event.Sender, event.ID, result.Entry.Sender, result.Entry.ID)
		}
	}
}

// compareParityState is the other half of a scenario: the state the module ended
// in, which the answers alone do not pin — a duplicate that advanced nothing, or
// an eviction that took the wrong record, answers the same and leaves a
// different ledger.
func compareParityState(t *testing.T, scenario parityScenario, ledger *Ledger, want parityState) {
	t.Helper()
	if ledger.state.now != want.Now {
		t.Fatalf("%s: the ledger's clock is %d, and the module's is %d", parityWhere(scenario), ledger.state.now, want.Now)
	}
	if ledger.state.evictedThrough != want.EvictedThrough {
		t.Fatalf("%s: the ledger's watermark is %d, and the module's is %d", parityWhere(scenario), ledger.state.evictedThrough, want.EvictedThrough)
	}
	got := ledger.Entries()
	if len(got) != len(want.Seen) {
		t.Fatalf("%s: the ledger holds %d records and the module holds %d:\n  ledger: %s\n  module: %s",
			parityWhere(scenario), len(got), len(want.Seen), parityEntriesText(got), parityEntriesText(want.Seen))
	}
	for i := range got {
		if got[i] != (LedgerEntry{Sender: want.Seen[i].Sender, ID: want.Seen[i].ID, At: want.Seen[i].At, Kind: want.Seen[i].Kind, Status: want.Seen[i].Status}) {
			t.Fatalf("%s: record %d is %+v in this ledger and %+v in the module", parityWhere(scenario), i, got[i], want.Seen[i])
		}
	}
}

// parityEntriesText is a ledger, in the module's own spelling, for a failure
// message: two ledgers compared by eye are what a record-level failure needs.
func parityEntriesText[T LedgerEntry | parityEntry](entries []T) string {
	out, err := json.Marshal(entries)
	if err != nil {
		return fmt.Sprintf("%+v", entries)
	}
	return string(out)
}

// parityLedgerOptions is the corpus's five numbers as this package's caller
// would pass them.
func parityLedgerOptions(path string, options parityOptions) LedgerOptions {
	return LedgerOptions{
		Path:              path,
		WindowMs:          options.WindowMs,
		PromptTtlMs:       options.PromptTtlMs,
		MaxSeen:           options.MaxSeen,
		MaxPerSender:      options.MaxPerSender,
		FutureToleranceMs: options.FutureToleranceMs,
	}
}

// TestParityHash is the corpus's own seal: SHA-256 over the file with the hash
// field zeroed, recomputed. It is what makes the replay's green mean something —
// a corpus edited by hand is a corpus nobody generated, and it would otherwise
// pass every check below while pinning answers the module never gave.
//
// The hash detects an edit. It cannot detect a **module** that changed without
// the corpus being regenerated for it — that is TestParityCorpusIsCurrent's, and
// the two are why the corpus is sealed rather than merely committed.
func TestParityHash(t *testing.T) {
	corpus, data := loadParityCorpus(t)
	if !parityHashRE.MatchString(corpus.Hash) {
		t.Fatalf("the corpus's hash is %q, which is not a SHA-256: the seal is not a seal", corpus.Hash)
	}
	// The hash is the corpus's last field, so the last occurrence of its value in
	// the file is the field itself — and the hash of the zeroed text is the
	// number the generator wrote there.
	at := bytes.LastIndex(data, []byte(corpus.Hash))
	if at < 0 {
		t.Fatalf("the corpus's hash is not in the file it was decoded from")
	}
	zeroed := make([]byte, 0, len(data))
	zeroed = append(zeroed, data[:at]...)
	zeroed = append(zeroed, bytes.Repeat([]byte("0"), 64)...)
	zeroed = append(zeroed, data[at+len(corpus.Hash):]...)
	sum := sha256.Sum256(zeroed)
	if got := hex.EncodeToString(sum[:]); got != corpus.Hash {
		t.Fatalf("the corpus has been edited by hand: its own hash is %s and the file it is in hashes to %s.\n"+
			"Regenerate it with `%s` rather than editing it: an edited corpus is one nobody generated.",
			corpus.Hash, got, parityRegenerate)
	}
}

// TestParityCorpusIsCurrent is the one check that notices a **module** change
// nobody regenerated for: it runs the generator into a temporary file and
// compares the bytes with the committed corpus. The hash cannot see that — a
// changed module leaves the committed bytes and their hash in perfect agreement,
// both stale.
//
// It needs a `node`. Where there is none it skips with the reason recorded rather
// than passing quietly: the base image and CI's test job carry none, so on a CI
// run this check did not happen, and the skip line is how a reader knows.
// `shared/mailbox/parity.test.js` re-derives every answer wherever a Node exists,
// and TestParityHash runs everywhere, so the corpus is never unguarded — only
// this one direction of it is.
func TestParityCorpusIsCurrent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no `node` on PATH: the corpus's generator cannot be run here, so this run cannot tell whether the committed corpus is what the module produces today — `shared/mailbox/parity.test.js` re-derives every answer where a Node exists, and TestParityHash still catches an edit")
	}
	committed, err := os.ReadFile(parityCorpusPath)
	if err != nil {
		t.Fatalf("reading the committed corpus: %v", err)
	}
	regenerated := filepath.Join(t.TempDir(), "parity.json")
	cmd := exec.Command(node, parityGeneratePath, regenerated)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the generator (%s %s): %v\n%s", node, parityGeneratePath, err, stderr.String())
	}
	fresh, err := os.ReadFile(regenerated)
	if err != nil {
		t.Fatalf("reading the corpus the generator just wrote: %v", err)
	}
	if bytes.Equal(fresh, committed) {
		return
	}
	line, was, is := parityFirstDifference(committed, fresh)
	t.Fatalf("the committed corpus is not what the generator writes today: line %d is\n"+
		"  committed:   %s\n  regenerated: %s\n"+
		"Either the module moved and the corpus was not regenerated for it, or the generator changed and the corpus is stale. "+
		"Run `%s` and commit the result.", line, was, is, parityRegenerate)
}

// parityFirstDifference is the first line that differs between two documents,
// with both spellings: a 300 KB corpus compared by eye needs one line, not two
// documents.
func parityFirstDifference(committed, fresh []byte) (int, string, string) {
	was := bytes.Split(committed, []byte("\n"))
	is := bytes.Split(fresh, []byte("\n"))
	for i := 0; i < len(was) && i < len(is); i++ {
		if !bytes.Equal(was[i], is[i]) {
			return i + 1, string(was[i]), string(is[i])
		}
	}
	if len(was) == len(is) {
		return len(was), "", ""
	}
	if len(was) > len(is) {
		return len(is) + 1, string(was[len(is)]), "(the regenerated corpus ends here)"
	}
	return len(was) + 1, "(the committed corpus ends here)", string(is[len(was)])
}

// TestParityCorpusHoldsTheLedgersVocabulary is M9's other direction, and it is
// what makes the corpus the *contract* rather than a transcript: the five numbers
// a scenario is judged with are this package's own constants, every code the
// ledger speaks is one the corpus spells, and every code this package can be
// answered with is carried by a case — so a code added to LedgerCodes without a
// case is a failing test rather than a refusal with no parity behind it.
func TestParityCorpusHoldsTheLedgersVocabulary(t *testing.T) {
	corpus, _ := loadParityCorpus(t)
	if corpus.Module != "shared/mailbox/index.js" || corpus.Replay != "internal/mailbox/parity_test.go" {
		t.Fatalf("the corpus names %q and %q: it is about another module, or another replay", corpus.Module, corpus.Replay)
	}
	if _, err := os.Stat(parityGeneratePath); err != nil {
		t.Fatalf("the generator the corpus names is not where this replay looks for it: %v", err)
	}

	want := parityOptions{
		WindowMs:          DefaultWindowMs,
		PromptTtlMs:       testPromptTtlMs,
		MaxSeen:           DefaultMaxSeen,
		MaxPerSender:      DefaultMaxPerSender(DefaultMaxSeen),
		FutureToleranceMs: FutureTolerance.Milliseconds(),
	}
	if corpus.Options != want {
		t.Errorf("the corpus judges its scenarios with %+v, and this package's own settings are %+v: a scenario replayed under other numbers pins nothing", corpus.Options, want)
	}

	declared := map[string]bool{}
	for _, code := range corpus.Codes {
		declared[code] = true
	}
	if len(declared) < 20 {
		t.Fatalf("read %d refusal codes out of the corpus: the corpus, not the ledger, is what changed", len(declared))
	}
	for _, code := range LedgerCodes {
		if !declared[string(code)] {
			t.Errorf("the ledger refuses with %q, which the corpus's vocabulary does not spell: a code with nothing on the other end is a code that drifted", code)
		}
	}
	carried := map[string]bool{}
	for _, scenario := range corpus.Scenarios {
		if scenario.StateRefusal != "" {
			carried[scenario.StateRefusal] = true
		}
		for _, event := range scenario.Events {
			if event.Code != "" {
				carried[event.Code] = true
			}
		}
	}
	for _, code := range LedgerCodes {
		if !carried[string(code)] {
			t.Errorf("no case in the corpus asks for %q: a code the ledger speaks and the corpus never asks for is a rule with no parity", code)
		}
	}
	for code := range carried {
		if !declared[code] {
			t.Errorf("a case is answered %q, which is not in the corpus's own vocabulary", code)
		}
	}

	// The outcomes. `accepted` and `nothing-to-act` are the module's and not this
	// binding's: Perform folds a taken delivery into the act that follows it, so
	// neither is an answer a caller of this package ever sees. `closed` is a
	// mailbox nothing in this half closes. They are named here rather than left
	// out, so a corpus that lost one is a failing test.
	outcomes := map[string]bool{}
	for _, outcome := range corpus.Outcomes {
		outcomes[outcome] = true
	}
	for _, outcome := range []string{
		OutcomeAccepted, OutcomeDuplicate, OutcomeActed, OutcomeStalePending,
		OutcomeAcked, OutcomeNothingToAct, OutcomeNothingToAck, "closed",
	} {
		if !outcomes[outcome] {
			t.Errorf("the corpus does not carry the outcome %q: the module's vocabulary and this binding's have drifted", outcome)
		}
	}
	if len(outcomes) != 8 {
		t.Errorf("the corpus carries %d outcomes and the module's OUTCOME has 8: a new one is an outcome to bind, not to ignore", len(outcomes))
	}
}

// TestParityCorpusHoldsThePairingExpiry is T8's extension of the corpus's use
// (the pairing plan §15, T8): **the corpus is P4.1's and this task regenerates
// nothing** — `shared/` is nobody's in that plan — but the pairing work's expiry
// (`internal/mailbox/expiry.go`, P6.13's file, T7) draws the same boundary the
// module's one `expired` case does, so the corpus is used here to hold the two
// ends together. A change to `PromptTTLMs`, or a module whose prompt TTL moved
// without the corpus being regenerated for it, is a failing test rather than a
// drift no one reads.
//
// **The one-millisecond difference is stated rather than asserted away.** The
// module refuses a *delivery* strictly past the TTL (`now - at > promptTtlMs`,
// `index.js`'s `windowFor`), while this package's `PromptExpired` expires a
// *held* prompt at the TTL itself (`now - at >= PromptTTLMs`), which is the
// boundary `ConfirmMemory.ExpirePrompts` draws. So the corpus's case — a prompt
// one millisecond past its TTL — is refused by both, and the two boundaries
// part only at exactly the TTL, where the module would still accept a delivery
// this package already considers expired. That is a property of two rules about
// two different instants (a delivery arriving, a prompt being held), and it is
// named here so a reviewer reads it rather than finding it.
func TestParityCorpusHoldsThePairingExpiry(t *testing.T) {
	corpus, _ := loadParityCorpus(t)
	if corpus.Options.PromptTtlMs != PromptTTLMs {
		t.Fatalf("the corpus judges prompts with a TTL of %d and this package expires them at %d: the two numbers have drifted",
			corpus.Options.PromptTtlMs, PromptTTLMs)
	}
	found := 0
	for _, scenario := range corpus.Scenarios {
		for _, event := range scenario.Events {
			if event.Code != string(CodeExpired) || event.Message == nil {
				continue
			}
			found++
			at, now := event.Message.At, event.Now
			if now-at <= PromptTTLMs {
				t.Errorf("%s: the module refuses the delivery %s at now-at = %d, which is not past this package's TTL of %d",
					scenario.Name, CodeExpired, now-at, PromptTTLMs)
			}
			if !PromptExpired(at, now) {
				t.Errorf("%s: the module refuses the prompt %s at now-at = %d and this package's PromptExpired calls it live",
					scenario.Name, CodeExpired, now-at)
			}
		}
	}
	if found == 0 {
		t.Fatalf("the corpus carries no %q case: the boundary this test holds has nothing on the module's end", CodeExpired)
	}
}
