package bbtokens

import (
	"context"
	"errors"
	"testing"
)

// TestStubImplementsContract is what the tasks that consume the contract
// compile against before the real implementations exist: the stub is a
// Selector, a ProjectResolver and a StatusStore, and it answers with the values
// it was set to rather than with anything of its own.
func TestStubImplementsContract(t *testing.T) {
	var (
		_ Selector        = (*Stub)(nil)
		_ ProjectResolver = (*Stub)(nil)
		_ StatusStore     = (*Stub)(nil)
	)

	ctx := context.Background()
	refusal := &Refusal{Class: ClassNotCovered, Repo: "acme/app"}
	stub := &Stub{Selected: "bitbucket-token--ws", ProjectUUID: "proj-uuid", Err: refusal}

	if _, err := stub.Select(ctx, Snapshot{}, "acme/app", true); !errors.Is(err, refusal) {
		t.Errorf("Select with Err set answered %v, want the refusal it was set to", err)
	}
	if _, err := stub.ProjectOf(ctx, "acme/app", "bitbucket-token--proj"); !errors.Is(err, refusal) {
		t.Errorf("ProjectOf with Err set answered %v, want the refusal it was set to", err)
	}

	stub.Err = nil

	choice, err := stub.Select(ctx, Snapshot{}, "acme/app", false)
	if err != nil || choice.Name != stub.Selected {
		t.Errorf("Select = %+v, %v; want the token it was set to and no error", choice, err)
	}
	uuid, err := stub.ProjectOf(ctx, "acme/app", "bitbucket-token--proj")
	if err != nil || uuid != stub.ProjectUUID {
		t.Errorf("ProjectOf = %q, %v; want the UUID it was set to and no error", uuid, err)
	}
	stub.Evict("acme/app")
	if len(stub.Evicted) != 1 || stub.Evicted[0] != "acme/app" {
		t.Errorf("Evict recorded %v, want the repository it was called for", stub.Evicted)
	}

	if got, err := stub.Read(ctx, "bitbucket-token--ws"); err != nil || got != (Status{}) {
		t.Errorf("Read of a token the store holds nothing for = %+v, %v; want the zero status", got, err)
	} else if !got.Available() {
		t.Error("the zero status a store answers for a token it holds nothing for does not read as available")
	}
	broken := Status{State: StateBroken}
	if err := stub.Write(ctx, "bitbucket-token--ws", broken); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, err := stub.Read(ctx, "bitbucket-token--ws"); err != nil || got != broken {
		t.Errorf("Read after Write = %+v, %v; want the status written", got, err)
	}
	if err := stub.CountRequest(ctx, "bitbucket-token--ws"); err != nil {
		t.Fatalf("CountRequest: %v", err)
	}
	if len(stub.Counted) != 1 || stub.Counted[0] != "bitbucket-token--ws" {
		t.Errorf("CountRequest recorded %v, want the token it was called for", stub.Counted)
	}
}
