package runner

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// A gateway halt keeps the scope the lease's refusal named.
func TestGatewayHaltCarriesScope(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ reason, scope, want string }{
		{"repo_daily_cap", "repo", "repo"},
		{"global_daily_cap", "global", "global"},
		{"kill_switch", "global", "global"},
		{"kill_switch", "repo", "repo"},
		{"run_cap", "", "run"},
	} {
		got := gatewayHalt(gateway.Halt{Reason: c.reason, Scope: c.scope, Detail: "d", At: at})
		if got.Scope != c.want || got.Reason != runstore.HaltReason(c.reason) || got.Detail != "d" {
			t.Errorf("%s/%s: %+v, want scope %q", c.reason, c.scope, got, c.want)
		}
	}
}

// A workspace's refusal reaches the run's record as the marker the gateway read
// off the refusal (docs/stateless-orchestrator/reconciliation.md U12, D2022's
// O19), and a halt no workspace refused carries none: the marker is copied and
// never inferred from the reason, the scope or the detail, because a member's
// own credentials service writes those three the same way a workspace's does.
func TestGatewayHaltCarriesTheWorkspaceRefusalAndOnlyWhenThereIsOne(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	plain := gatewayHalt(gateway.Halt{Reason: gateway.ReasonBudgetUnavailable, Scope: "run", Detail: "d", At: at})
	if plain.WorkspaceRefusal != "" {
		t.Fatalf("a halt no workspace refused carries %q", plain.WorkspaceRefusal)
	}
	if plain.Reason != runstore.HaltBudgetUnavailable || plain.Scope != "run" || plain.Detail != "d" || !plain.At.Equal(at) {
		t.Fatalf("the unmarked halt lost a field it always carried: %+v", plain)
	}
	for _, reason := range gateway.WorkspaceRefusals() {
		got := gatewayHalt(gateway.Halt{Reason: gateway.ReasonBudgetUnavailable, Scope: "run", Detail: "d", At: at, WorkspaceRefusal: reason})
		if got.WorkspaceRefusal != reason {
			t.Errorf("the workspace's %s refusal reached the record as %q", reason, got.WorkspaceRefusal)
		}
		if got.Reason != plain.Reason || got.Scope != plain.Scope || got.Detail != plain.Detail || !got.At.Equal(plain.At) {
			t.Errorf("the workspace's %s refusal changed the halt itself: %+v, want %+v", reason, got, plain)
		}
	}
}
