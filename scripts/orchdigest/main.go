// Command orchdigest prints the spec digest the daemon will compute for the
// work item a work_item_upsert patch declares. It reads the patch document on
// stdin, lands it on an empty state through orchestrator.Apply as a session
// would send it, and prints orchestrator.SpecDigest of the item that results.
// Going through Apply means the digest is the one of the item as the state
// stores it, with every field folded the way the state folds it, and not a
// second reading of the document.
//
// The approve op binds exactly this digest. Computing it is no approval: only a
// person's own confirmation at a real terminal lands the op.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

const maxPatchBytes = 1 << 20

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "orchdigest:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(in, maxPatchBytes))
	if err != nil {
		return err
	}
	p, err := orchestrator.DecodePatch(raw)
	if err != nil {
		return err
	}
	id, err := upsertedID(p)
	if err != nil {
		return err
	}
	empty := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           p.BaseRev,
		AgentID:       "digest",
		Role:          orchestrator.RoleOrchestrator,
	}
	next, err := orchestrator.Apply(empty, p, orchestrator.Actor{Class: orchestrator.Session, Name: "orchdigest", Now: time.Unix(1, 0).UTC()})
	if err != nil {
		return err
	}
	w, ok := next.Item(id)
	if !ok {
		return fmt.Errorf("the patch left no work item %q", id)
	}
	_, err = fmt.Fprintln(out, orchestrator.SpecDigest(w))
	return err
}

// upsertedID is the one item the patch declares: a document with another op,
// or with two items, would make "the digest" ambiguous.
func upsertedID(p orchestrator.Patch) (string, error) {
	if len(p.Ops) != 1 || p.Ops[0].Op != orchestrator.OpWorkItemUpsert || p.Ops[0].WorkItem == nil {
		return "", fmt.Errorf("the patch has to carry exactly one work_item_upsert op")
	}
	return p.Ops[0].WorkItem.ID, nil
}
