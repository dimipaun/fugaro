package credsvc

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/routing"
)

// This test is the stub's own and goes with it: I1 deletes launcherpool_stub.go
// and this file together, once launcherpool.go resolves a launcher's pool. It
// pins the one answer C2's contract fixes for the stub — no pool designated —
// and that it is the same answer for every principal and every named pool,
// because the stub reads neither.
func TestStubLauncherPoolDesignatesNothing(t *testing.T) {
	var pool routing.LauncherPool = StubLauncherPool{}
	ref, found, err := pool.LauncherPool(context.Background(), routing.Principal{Kind: routing.PrincipalPerson, ID: "104928374650192837465"}, "team")
	if err != nil {
		t.Fatalf("the stub answers a lookup with %v, and an error is never \"no pool designated\"", err)
	}
	if found || ref != (routing.PoolRef{}) {
		t.Errorf("the stub answers (%+v, %v) for a person and a named pool, and no pool is designated", ref, found)
	}
	if _, found, err := pool.LauncherPool(context.Background(), routing.Principal{Kind: routing.PrincipalProject, ID: "fugaro"}, ""); found || err != nil {
		t.Errorf("the stub answers (found=%v, err=%v) for a project principal and no named pool, and its one answer is the same for every principal", found, err)
	}
}
