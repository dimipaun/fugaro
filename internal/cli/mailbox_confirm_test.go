package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/mailbox"
)

// The consumer's Go implementation of `shared/confirm` is held to the module by
// the module's own data (mobile plan Task P6.7). `shared/confirm/vectors.json`
// is the contract the phone and the consumer share: every case in it is replayed
// through the Go port, and the answer is compared field for field with the
// answer the module produces.
//
// The test is here, in the CLI, rather than beside the port, because the plan's
// verification command is `go test ./internal/cli -run 'Mailbox'`: the parity
// the consumer owes is a property of the consumer, and this is where the
// consumer's own tests live.

// confirmVectorFile is the vector document: the machine's knobs, the resolved
// values the cases use, and the cases themselves.
type confirmVectorFile struct {
	About     string              `json:"about"`
	Now       int64               `json:"now"`
	TimeoutMs int64               `json:"timeoutMs"`
	MaxPend   int                 `json:"maxPending"`
	MaxLedger int                 `json:"maxLedger"`
	Cases     []confirmVectorCase `json:"cases"`
	// The resolved values the document's cases are written against. They are
	// declared so that the document is read as strictly as every other wire
	// format here, and so a test can hold the document's own prose to the
	// strings it promises.
	RunID           string `json:"runId"`
	Repo            string `json:"repo"`
	PR              int64  `json:"pr"`
	HeadSHA         string `json:"headSha"`
	CancelReadBack  string `json:"cancelReadBack"`
	MergeReadBack   string `json:"mergeReadBack"`
	ConfirmReadBack string `json:"confirmReadBack"`
	StartReadBack   string `json:"startReadBack"`
	ConfirmText     string `json:"confirmText"`
	// The session policy D2786 reads (C2's `costly_modes` and
	// `biometric_on_costly`), which the cases are written against.
	CostlyModes       []string `json:"costlyModes"`
	BiometricOnCostly bool     `json:"biometricOnCostly"`
}

type confirmVectorCase struct {
	Name   string                `json:"name"`
	Events []confirmVectorEvent  `json:"events"`
	Expect []confirmVectorExpect `json:"expect"`
	// A case may move the session policy, which is a case about a project whose
	// policy differs; an absent one is the document's own.
	CostlyModes       []string `json:"costlyModes"`
	BiometricOnCostly *bool    `json:"biometricOnCostly"`
}

// confirmVectorEvent is one utterance. Every field the module's event sets name
// is here and nothing else, and the document is decoded strictly, so a vector
// carrying a field this port does not read is a failing test rather than a field
// silently ignored -- the same rule the module applies to an event.
type confirmVectorEvent struct {
	Type           string          `json:"type"`
	Now            int64           `json:"now"`
	UtteranceID    string          `json:"utteranceId"`
	Text           string          `json:"text"`
	Verb           string          `json:"verb"`
	Target         json.RawMessage `json:"target"`
	Mode           *string         `json:"mode"`
	ActionID       *string         `json:"actionId"`
	ConfirmationID string          `json:"confirmationId"`
}

type confirmVectorTarget struct {
	Kind    string `json:"kind"`
	RunID   string `json:"runId"`
	Repo    string `json:"repo"`
	PR      int64  `json:"pr"`
	HeadSHA string `json:"headSha"`
	Mode    string `json:"mode"`
}

// confirmVectorExpect is one step's expected answer. The pointers are what the
// module answers with `null` where the vector omits the key, and an omitted key
// means "not asserted" rather than "null".
type confirmVectorExpect struct {
	OK             bool           `json:"ok"`
	Code           string         `json:"code"`
	Field          string         `json:"field"`
	Outcome        string         `json:"outcome"`
	Phase          string         `json:"phase"`
	ReadBack       *string        `json:"readBack"`
	ActionID       *string        `json:"actionId"`
	ConfirmationID *string        `json:"confirmationId"`
	Record         map[string]any `json:"record"`
	// D2786's policy answer, where a case names it: a costly start asks for the
	// read-back, and with the switch the biometric beside it.
	Biometric *bool `json:"biometric"`
	// What the machine holds after an accept, where a case names it: the answer
	// says what was spoken, and this says what the step produced. An omitted key
	// is "not asserted", as every other one is.
	State *confirmVectorState `json:"state"`
}

// confirmVectorState is the machine a case says the step left behind: how many
// read-backs are pending and how many ids the ledger has spent. It is counted
// rather than compared entry for entry, so a case stays about the shape of the
// state rather than about every field the port carries.
type confirmVectorState struct {
	Pending int `json:"pending"`
	Ledger  int `json:"ledger"`
}

// TestConfirmParityWithShared replays every vector in `shared/confirm/vectors.json`
// through the consumer's Go port.
func TestConfirmParityWithShared(t *testing.T) {
	confirmParityWithShared(t)
}

// TestMailboxConfirmParityWithShared is the same test under the name the plan's
// verification command matches: `go test ./internal/cli -run 'Mailbox'` is what
// P6.7 is verified with, and a parity test outside that filter would be a parity
// test nobody runs.
func TestMailboxConfirmParityWithShared(t *testing.T) {
	confirmParityWithShared(t)
}

func confirmParityWithShared(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "shared", "confirm", "vectors.json"))
	if err != nil {
		t.Fatalf("reading shared/confirm/vectors.json: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file confirmVectorFile
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("decoding shared/confirm/vectors.json: %v", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatalf("shared/confirm/vectors.json carries more than one document: %v", err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("shared/confirm/vectors.json has no cases, so this test would pass over nothing")
	}
	stepped := 0
	for _, c := range file.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			// The document's own session policy, and a case's override of it:
			// D2786's answer is a function of these two, so a case that moves
			// them is a case about a project whose policy differs.
			costlyModes := file.CostlyModes
			if c.CostlyModes != nil {
				costlyModes = c.CostlyModes
			}
			biometricOnCostly := file.BiometricOnCostly
			if c.BiometricOnCostly != nil {
				biometricOnCostly = *c.BiometricOnCostly
			}
			state, refusal := mailbox.ConfirmInitialState(mailbox.ConfirmOptions{
				Now: file.Now, TimeoutMs: file.TimeoutMs,
				MaxPending: file.MaxPend, MaxLedger: file.MaxLedger,
				CostlyModes: costlyModes, BiometricOnCostly: biometricOnCostly,
			})
			if refusal != nil {
				t.Fatalf("the vector document's own knobs are refused: %s", refusal)
			}
			if len(c.Events) != len(c.Expect) {
				t.Fatalf("%d events but %d expected answers", len(c.Events), len(c.Expect))
			}
			for i, raw := range c.Events {
				ev := confirmVectorToEvent(t, raw)
				answer := mailbox.ConfirmStep(state, ev)
				stepped++
				want := c.Expect[i]
				where := func(format string, args ...any) {
					t.Errorf("event %d (%s): "+format, append([]any{i + 1, raw.Type}, args...)...)
				}
				if answer.OK != want.OK {
					where("ok = %v, want %v (refusal %s/%s)", answer.OK, want.OK, answer.Code, answer.Field)
					continue
				}
				if !want.OK {
					if string(answer.Code) != want.Code || answer.Field != want.Field {
						where("refused %s/%s, want %s/%s", answer.Code, answer.Field, want.Code, want.Field)
					}
					continue
				}
				if string(answer.Outcome) != want.Outcome {
					where("outcome = %q, want %q", answer.Outcome, want.Outcome)
				}
				if string(answer.Phase) != want.Phase {
					where("phase = %q, want %q", answer.Phase, want.Phase)
				}
				if want.Biometric != nil && answer.Biometric != *want.Biometric {
					where("biometric = %v, want %v", answer.Biometric, *want.Biometric)
				}
				if want.ReadBack != nil && answer.ReadBack != *want.ReadBack {
					where("readBack = %q, want %q", answer.ReadBack, *want.ReadBack)
				}
				if want.ActionID != nil && answer.ActionID != *want.ActionID {
					where("actionId = %q, want %q", answer.ActionID, *want.ActionID)
				}
				if want.ConfirmationID != nil && answer.ConfirmationID != *want.ConfirmationID {
					where("confirmationId = %q, want %q", answer.ConfirmationID, *want.ConfirmationID)
				}
				if want.Record != nil {
					if answer.Record == nil {
						where("no record, want %v", want.Record)
					} else if got := canonicalJSON(t, answer.Record.Value()); !reflect.DeepEqual(got, canonicalJSON(t, want.Record)) {
						where("record = %v, want %v", got, want.Record)
					}
				}
				if want.State != nil {
					if n := len(answer.State.Pending); n != want.State.Pending {
						where("state holds %d pending read-backs, want %d", n, want.State.Pending)
					}
					if n := len(answer.State.Spent); n != want.State.Ledger {
						where("state holds %d ledger entries, want %d", n, want.State.Ledger)
					}
				}
				// An omitted key is "not asserted": a case that confirms without
				// naming the record is still held to the outcome, the phase and
				// the read-back, and one that names the record is held to every
				// field of it.
				state = answer.State
			}
		})
	}
	if stepped == 0 {
		t.Fatal("no event was stepped, so this test proved nothing")
	}
}

// confirmVectorToEvent reads one vector event into the port's own event, keeping
// the two presences the port cannot see in a Go string: whether the event
// carried a target at all, and whether it carried an action id.
func confirmVectorToEvent(t *testing.T, raw confirmVectorEvent) mailbox.ConfirmEvent {
	t.Helper()
	ev := mailbox.ConfirmEvent{
		Type:        mailbox.ConfirmEventType(raw.Type),
		Now:         raw.Now,
		UtteranceID: raw.UtteranceID,
		Text:        raw.Text,
		Verb:        raw.Verb,
		HasTarget:   raw.Target != nil,
		HasMode:     raw.Mode != nil,
		HasActionID: raw.ActionID != nil,
		// A confirmation id is only ever read by the confirm handler, and the
		// module reads it as a required field there, so an absent one is the
		// empty string this port refuses as missing.
		ConfirmationID: raw.ConfirmationID,
	}
	if raw.Mode != nil {
		ev.Mode = *raw.Mode
	}
	if raw.ActionID != nil {
		ev.ActionID = *raw.ActionID
	}
	if raw.Target != nil && !bytes.Equal(bytes.TrimSpace(raw.Target), []byte("null")) {
		var target confirmVectorTarget
		dec := json.NewDecoder(bytes.NewReader(raw.Target))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&target); err != nil {
			t.Fatalf("decoding a target: %v", err)
		}
		ev.Target = &mailbox.ConfirmTarget{
			Kind: target.Kind, RunID: target.RunID, Repo: target.Repo,
			PR: target.PR, HeadSHA: target.HeadSHA, Mode: target.Mode,
		}
	}
	return ev
}

// canonicalJSON reads a value back through JSON, so that a number the vector
// holds as a float64 and the port holds as an int64 compare equal.
func canonicalJSON(t *testing.T, value any) any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling %v: %v", value, err)
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshalling %s: %v", body, err)
	}
	return out
}
