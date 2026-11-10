package projectpolicy

// Binaries is the project's binary allow rule (D981, D986): the paths a run may
// add a binary at, and the largest binary it may add at one of them.
//
// It lives on this node and nowhere else, for the reason every member of the
// node does and for a sharper one: fugaro.yaml sits inside the checkout the
// run's own agent writes, so an allow rule there would let a prompt-injected
// agent admit its own payload with the repository's own consent-looking file
// (spec.md §3). The node is the one object the launcher cannot write, and the
// run reads the block at bootstrap and puts it on the ceiling layer
// (internal/runner's policyCeiling), where the merge lets every layer under it
// remove an admitted path and none add one.
//
// This release carries the shape and not the node's key. `binaries` is not in
// knownTopLevelKeys, so a node that writes one has it listed in UnknownKeys and
// nothing here reads it (P8); the run then admits no binary, which is the
// strict end and the behaviour of every installation today. The decode, its
// validation and the version that publishes them are one task in the plan
// (AR0: D982's five pattern checks, D983's cap, D990's skew), and they land
// together: a block read before them would enforce a list whose patterns,
// count, lengths and cap are unchecked, and a cap that is absent or negative
// reads as unset to the merge's smallest-positive-wins rule, which is no cap at
// all rather than D983's 2 MiB.
type Binaries struct {
	// Allow is the admitted paths, each a doublestar glob relative to the
	// repository root (D982, the pattern language internal/rigor and
	// internal/config's rebuild.paths already share).
	Allow []string `json:"allow,omitempty"`
	// MaxBytes is the cap one added binary at an admitted path may reach.
	MaxBytes int64 `json:"max_bytes,omitempty"`
}
