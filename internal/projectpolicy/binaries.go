package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Binaries is the project's binary allow rule (D981, D986): the paths a run may
// add a binary at, and the largest binary it may add at one of them.
//
// It lives on this node and nowhere else, for the reason every member of the
// node does and for a sharper one: fugaro.yaml sits inside the checkout the
// run's own agent writes, so an allow rule there would let a prompt-injected
// agent admit its own payload with the repository's own consent-looking file
// (spec.md §3). The node is the one object the launcher cannot write, and the
// run reads it at bootstrap and puts this block on the ceiling layer, where
// the merge lets every layer under it remove an admitted path and none add one.
//
// The block is optional, and absent means "no binary is admitted": a project
// that sets no list keeps refusing every uncommitted binary exactly as it does
// today. `max_bytes` is one project-wide cap and only ever tightens (D983);
// the default a written block takes when it names no cap is filled where the
// block is validated, and this carrier only holds what the node says.
type Binaries struct {
	// Allow is the admitted paths, each a doublestar glob relative to the
	// repository root (D982, the pattern language internal/rigor and
	// internal/config's rebuild.paths already share).
	Allow []string `json:"allow,omitempty"`
	// MaxBytes is the cap one added binary at an admitted path may reach.
	MaxBytes int64 `json:"max_bytes,omitempty"`
}

// decodeBinaries reads the node's binaries block. It is strict inside the way
// the budget and the credentials blocks are: a member this reader does not
// know is refused rather than dropped, because a misspelt one here is a rule
// the owner believes is published and that admits nothing — the failure mode
// D982 refuses for the patterns themselves.
//
// A block that names no member is the block it is: the carrier is returned
// empty, and an empty list admits nothing, which is the answer the absent
// member gives too. The two spellings are told apart where D987 distinguishes
// them — at publish, where silence keeps the published list and a written
// empty block takes it away — and never here.
func decodeBinaries(raw json.RawMessage) (*Binaries, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b Binaries
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("projectpolicy: binaries: %w", err)
	}
	return &b, nil
}
