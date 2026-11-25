package creds

import (
	"context"
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/routing"
)

// This test is the stub's own and goes with it: I1 deletes poolrule_stub.go and
// this file together, once poolrule.go holds the rule. It pins the two answers
// C2's contract fixes for the stub and nothing else — the pool's first member,
// and ErrNoCandidates when the stub is handed no members at all.
func TestStubPoolRuleAnswersTheFirstMember(t *testing.T) {
	pool := []Credential{{Name: "claude-a"}, {Name: "claude-b"}}
	got, _, err := StubPoolRule{}.PickInPool(context.Background(), routing.PoolRef{Pool: "team"}, Run{Slug: "repo", ID: "1"}, pool)
	if err != nil {
		t.Fatalf("the stub refuses a pool that holds members: %v", err)
	}
	if got.Name != pool[0].Name {
		t.Errorf("the stub answers %q, and the first member of the pool is %q", got.Name, pool[0].Name)
	}
	if _, _, err := (StubPoolRule{}).PickInPool(context.Background(), routing.PoolRef{Pool: "team"}, Run{}, nil); !errors.Is(err, ErrNoCandidates) {
		t.Errorf("the stub answers an empty member list with %v, and ErrNoCandidates is the seam's refusal of a caller that offered nothing to select from", err)
	}
}
