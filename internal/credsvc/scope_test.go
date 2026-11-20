package credsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStubScopeRefusesEveryCall is C1's pin: the stub scope serves no route and
// every reader it answers refuses, so a route switched to the scope can be
// tested before either deployment implements one.
func TestStubScopeRefusesEveryCall(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, MessagesPath, nil)
	sc, ok := StubScopeResolver{}.Resolve(res, req)
	if !ok {
		t.Fatal("the stub resolver refused the call itself: what the front door does with a scope that serves nothing is the thing under test")
	}
	if sc == nil {
		t.Fatal("the stub resolver answered no scope")
	}
	for _, path := range []string{MessagesPath, CountTokensPath, MintPath, SignPath, SlipPath, BitbucketProxyPrefix, KeepWarmPath, "/v1/not-a-route"} {
		if sc.Serves(path) {
			t.Errorf("the stub scope serves %q, and it serves nothing", path)
		}
	}
	if sc.AcceptsSlip() {
		t.Error("the stub scope accepts a slip, and a scope that serves nothing accepts none")
	}
	if c, called := sc.Caller(); called {
		t.Errorf("the stub scope answered the caller %+v, and it verified none", c)
	}
	readerRefuses(t, "Policy().Shared", func() error {
		_, _, err := sc.Policy().Shared(context.Background())
		return err
	})
	readerRefuses(t, "Policy().Admissions", func() error {
		_, _, err := sc.Policy().Admissions(context.Background(), "acme/aurora")
		return err
	})
	readerRefuses(t, "Kills().Stop", func() error {
		_, _, err := sc.Kills().Stop(context.Background(), "acme/aurora")
		return err
	})
	readerRefuses(t, "Outcomes().OutcomeExists", func() error {
		_, err := sc.Outcomes().OutcomeExists(context.Background(), 1, "acme/aurora", "run-1")
		return err
	})
	readerRefuses(t, "Offering().Offer", func() error {
		_, err := sc.Offering().Offer(context.Background(), Caller{}, nil, nil)
		return err
	})
	readerRefuses(t, "Forge().BitbucketToken", func() error {
		_, _, err := sc.Forge().BitbucketToken(context.Background(), "acme/aurora")
		return err
	})
	readerRefuses(t, "Forge().AppKey", func() error {
		_, _, err := sc.Forge().AppKey(context.Background(), "acme/aurora")
		return err
	})
	if _, found := sc.Table().Job("job@example.iam.gserviceaccount.com"); found {
		t.Error("the stub scope's table knows a job account, and it knows nobody")
	}
	if _, found := sc.Table().Repo("acme/aurora"); found {
		t.Error("the stub scope's table resolves a slug, and it resolves none")
	}
	if _, found := sc.Table().Review("review@example.iam.gserviceaccount.com"); found {
		t.Error("the stub scope's table knows a review account, and it knows nobody")
	}
	if _, found := sc.Table().Shared("shared@example.iam.gserviceaccount.com"); found {
		t.Error("the stub scope's table knows a shared account, and it knows nobody")
	}
	if sc.State() != nil {
		t.Error("the stub scope answered a state store, and a scope that serves nothing has none")
	}
	if sc.Meter() != nil {
		t.Error("the stub scope answered a meter, and it meters nothing")
	}
}

// TestStubScopeTravelsInTheContext pins the other half of the seam: ServeHTTP
// hands the scope to a handler through the request's context, and a handler
// reads back the same scope it was handed — which is what lets every handler
// keep its signature (WS1).
func TestStubScopeTravelsInTheContext(t *testing.T) {
	if _, ok := scopeOf(context.Background()); ok {
		t.Fatal("a context that was never handed a scope answered one")
	}
	ctx := withScope(context.Background(), StubScope{})
	sc, ok := scopeOf(ctx)
	if !ok {
		t.Fatal("the context answered no scope after one was handed to it")
	}
	if _, isStub := sc.(StubScope); !isStub {
		t.Fatalf("the context answered %T, want the scope that was handed to it", sc)
	}
}

// readerRefuses runs one scope reader and holds it to the two facts a reader
// of a scope that serves nothing has to answer: an error, and the stub's own
// refusal rather than a zero value. A reader that panics is a failure of the
// test and not of the suite, so the panic is recovered here as well.
func readerRefuses(t *testing.T, name string, read func() error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("the stub scope's %s panicked: %v", name, r)
		}
	}()
	err := read()
	if err == nil {
		t.Errorf("the stub scope's %s answered, and it refuses every read", name)
		return
	}
	if !errors.Is(err, errStubScope) {
		t.Errorf("the stub scope's %s answered the error %v, want its own refusal", name, err)
	}
}
