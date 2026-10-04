package backend

import (
	"context"
	"strings"
	"testing"
)

// fakeBackend satisfies Backend by embedding it (nil): tests compare
// identity only, they never call a method on it.
type fakeBackend struct{ Backend }

func TestOpenKnownBackend(t *testing.T) {
	want := &fakeBackend{}
	called := false
	openers := map[string]Opener{
		CloudRun: func(ctx context.Context) (Backend, error) {
			called = true
			return want, nil
		},
	}
	got, err := Open(context.Background(), CloudRun, openers)
	if err != nil || got != Backend(want) || !called {
		t.Fatalf("Open(%q) = %v, %v; called=%v", CloudRun, got, err, called)
	}
}

func TestOpenUnknownBackendNamesChoices(t *testing.T) {
	openers := map[string]Opener{
		CloudRun: func(ctx context.Context) (Backend, error) { return nil, nil },
	}
	_, err := Open(context.Background(), "ecs-fargate", openers)
	if err == nil || !strings.Contains(err.Error(), "ecs-fargate") || !strings.Contains(err.Error(), CloudRun) {
		t.Fatalf("err = %v, want it to name ecs-fargate and %s", err, CloudRun)
	}
}
