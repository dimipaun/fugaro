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
	// CloudBatch is a name this build knows -- internal/backend/batch speaks
	// it -- but the CLI registers no opener for it until the enable task
	// (design compute-backend.md §8.1). So a config that carried the name
	// past validation still cannot reach it, and the error says which names
	// this build does run.
	for _, name := range []string{"ecs-fargate", CloudBatch} {
		_, err := Open(context.Background(), name, openers)
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), CloudRun) {
			t.Fatalf("Open(%q): err = %v, want it to name %s and %s", name, err, name, CloudRun)
		}
	}
}
