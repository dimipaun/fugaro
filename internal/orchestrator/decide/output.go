package decide

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The refusals an answer meets. They are values so a caller can tell them
// apart: an answer with nothing in it is a cycle that achieved nothing (G10),
// and one this release cannot read is the same.
var (
	// ErrNoAnswer is a call that reported no structured answer at all.
	ErrNoAnswer = errors.New("decide: the call answered with nothing structured")
	// ErrAnswerShape is an answer this release cannot read.
	ErrAnswerShape = errors.New("decide: the answer is not shaped as this release reads it")
	// ErrNoDecision is an answer that asks for no change. It is refused
	// rather than landed: a decision that changes nothing is the same cycle
	// as one that failed, and the breaker is what a person sees when it
	// happens three times in a row (G10, owner lesson 22).
	ErrNoDecision = errors.New("decide: the answer asks for no change at all")
)

// OutputSchema is the shape one decision call's answer is requested under, as
// `claude -p --json-schema` takes it: one JSON document on one line, because
// the flag's value is this string. schemas/orchestrator-decision.schema.json
// is the same document written out for a reader, and
// TestOutputSchemaMatchesTheSchemaFile is what keeps the two one thing.
//
// The ops are asked for as opaque objects on purpose: their shape is
// orchestrator.Op's, which this package decodes strictly and code validates
// against the actor's class before anything lands, so a schema that spelled
// them here would be a second copy of a shape that already has an owner.
const OutputSchema = `{"type":"object","properties":{"reasoning":{"type":"string","minLength":1},"ops":{"type":"array","minItems":1,"items":{"type":"object"}}},"required":["reasoning","ops"],"additionalProperties":false}`

// Answer is what one decision call answered: the reasoning a person reads in
// the audit log, and the ops it asks to have applied.
//
// It carries no revision. The patch is applied to the state the runner read,
// and the revision it was read at is the runner's own fact — a model that
// guessed it could only ever guess wrong, so the schema does not ask for one.
type Answer struct {
	Reasoning string            `json:"reasoning"`
	Ops       []orchestrator.Op `json:"ops"`
}

// Patch is the patch the answer asks for, against the revision the caller
// read. Only the runner's own read of the state decides what it is applied
// to.
func (a Answer) Patch(rev int64) orchestrator.Patch {
	return orchestrator.Patch{BaseRev: rev, Ops: a.Ops}
}

// ParseAnswer reads the structured output of one decision call. It decodes
// strictly, like every other wire format in this tree: a field this release
// does not model is refused rather than ignored, so an answer cannot carry a
// second opinion the code silently drops — and an op it does not know is
// refused here rather than applied.
func ParseAnswer(raw json.RawMessage) (Answer, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return Answer{}, ErrNoAnswer
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a Answer
	if err := dec.Decode(&a); err != nil {
		return Answer{}, fmt.Errorf("%w: %s", ErrAnswerShape, safetext.OneLine(err.Error()))
	}
	if dec.More() {
		return Answer{}, fmt.Errorf("%w: the answer carries more than one document", ErrAnswerShape)
	}
	if a.Reasoning == "" {
		return Answer{}, fmt.Errorf("%w: the answer carries no reasoning", ErrAnswerShape)
	}
	if len(a.Ops) == 0 {
		return Answer{}, ErrNoDecision
	}
	if len(a.Ops) > orchestrator.MaxOps {
		return Answer{}, fmt.Errorf("%w: %d ops, the most one patch carries is %d", ErrAnswerShape, len(a.Ops), orchestrator.MaxOps)
	}
	for _, op := range a.Ops {
		if !op.Op.Known() {
			return Answer{}, fmt.Errorf("%w: %q is not an op", orchestrator.ErrUnknownOp, safetext.OneLine(string(op.Op)))
		}
	}
	return a, nil
}
