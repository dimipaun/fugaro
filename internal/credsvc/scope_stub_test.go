package credsvc

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
)

// TestStubScopeRefusesEveryCall is C1's red step and its whole test. The stub
// resolver exists so that the sides written against this contract in parallel —
// WS1's installation scope, WS2's workspace scope and WS5's process — compile
// and run their own tests before either side exists, and the one thing it must
// never do is let one of them pass on a decision nothing made. So every
// assertion below is an assertion that the stub decided nothing: it verified no
// caller, it serves no route, it accepts no slip, it holds no repository table,
// it keeps no figure, and every reader it answers refuses.
//
// The readers are asked method by method rather than through a summary,
// because the property is per method: a stub store whose Marks refused while
// its RepoAuth answered would be a stub that decided the one question the
// cascade narrows a call by, and no assertion over "the store" would see it.
//
// Mutation (run, restore): make stubScope.Serves answer true. A scope that
// serves a route it can read no caller, no policy, no kill switch and no pool
// for is the shape this stub exists to make impossible, and this fails on it.
func TestStubScopeRefusesEveryCall(t *testing.T) {
	ctx := context.Background()
	resolver := StubScopeResolver()
	if resolver == nil {
		t.Fatal("StubScopeResolver answered nothing: the stub is what a side built before its own implementation exists compiles against")
	}
	scope, err := resolver.Resolve(ctx, &http.Request{Method: http.MethodPost, URL: &url.URL{Path: MessagesPath}})
	if err != nil {
		t.Fatalf("the stub resolver refused to answer a scope: %v; the resolver answers a scope and the SCOPE is what refuses, so that a caller can be told which of the two refused it", err)
	}
	if scope == nil {
		t.Fatal("the stub resolver answered no scope: a route that read one would dereference nothing")
	}

	// The caller. The zero Identity is a run identity no token proved, and the
	// stub proved none — a scope that named one would let a route act for a run
	// that never called (R1).
	if got := scope.Caller(); !reflect.DeepEqual(got, Identity{}) {
		t.Errorf("the stub scope's caller is %+v, want the zero Identity: the stub verified nobody, and a caller a stub invented is the one answer the seam exists to prevent", got)
	}

	// Every route this service has, read back through the service's own gate so
	// that a path that stops being served fails here instead of passing as a
	// path no scope serves either way.
	for _, path := range []string{
		MessagesPath, CountTokensPath, MintPath, ContextTokenPath,
		SignPath, VerifyPath, BitbucketReadPath, GitHubReadPath,
		MergeMintPath, LabelPath, IssuePath, SlipPath, SlipClaimPath,
		WorkflowSecretsPath, BuildTokenPath, BuildCredentialsPath,
	} {
		if !servedRoute(&http.Request{Method: http.MethodPost, URL: &url.URL{Path: path}}) {
			t.Errorf("servedRoute(%q) is false: this list is the service's own routes, and a route that stopped being served has to change here rather than read as one no scope serves", path)
		}
		if scope.Serves(path) {
			t.Errorf("the stub scope serves %q: a scope that serves a route it holds no reader for is a route served with nothing behind it (R14)", path)
		}
	}
	proxy := BitbucketProxyPrefix + "acme/sandbox.git/info/refs"
	if !servedRoute(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: proxy}}) {
		t.Errorf("servedRoute(%q) is false: the run's Bitbucket traffic under %s is a route of this service", proxy, BitbucketProxyPrefix)
	}
	if scope.Serves(proxy) {
		t.Errorf("the stub scope serves %q: the proxy route is served with a token the stub does not hold", proxy)
	}
	// The keep-warm route C1 declares (R14) is asked even though the stub
	// answers false for every path: it is the one route of the workspace mode
	// that carries no secret, and a stub that served it would answer a beat
	// nothing is behind.
	if scope.Serves(WorkspaceKeepWarmPath) {
		t.Errorf("the stub scope serves the keep-warm route %q: the stub keeps nothing warm", WorkspaceKeepWarmPath)
	}
	if WorkspaceKeepWarmPath == "" || WorkspaceKeepWarmPath[0] != '/' {
		t.Errorf("WorkspaceKeepWarmPath is %q, want the absolute path a run posts its beat to", WorkspaceKeepWarmPath)
	}

	// R18's question, asked of the stub: a call that presents a slip is refused
	// where the scope does not accept one, and the stub accepts none.
	if scope.AcceptsSlip() {
		t.Error("the stub scope accepts a slip: a slip is a grant read out of a store the stub does not hold, and accepting one would admit a call by a secret nothing verified")
	}

	policy := scope.Policy()
	if policy == nil {
		t.Error("the stub scope answers no policy reader: a route that read the published policy would dereference nothing instead of being refused")
	} else {
		if _, _, err := policy.Shared(ctx); err == nil {
			t.Error("the stub scope's policy read the shared node: a reader that answers is a decision, and a stub makes none")
		}
		if _, _, err := policy.Admissions(ctx, "acme-sandbox"); err == nil {
			t.Error("the stub scope's policy read a repository's admission node: a node a stub answered would be an admission nothing decided")
		}
	}

	kills := scope.Kills()
	if kills == nil {
		t.Error("the stub scope answers no kill reader: the operator's stop switches are read before any credential, and a route that could not read them would serve a stopped service")
	} else if _, _, err := kills.Stop(ctx, "acme-sandbox"); err == nil {
		t.Error("the stub scope's kill reader answered: an unreadable switch is ON in this service's rule, and a stub that answered a readable one would serve through a stop nobody read")
	}

	// The store, method by method: the cascade's whole narrowing is a walk of
	// these, so one method that answered would be a call narrowed by a stub.
	state := scope.State()
	if state == nil {
		t.Error("the stub scope answers no state: the cascade reads every mark, lease and placement through it")
	} else {
		if _, err := state.Marks(ctx); err == nil {
			t.Error("the stub scope's state read the marks: a mark a stub answered would place a call")
		}
		if _, err := state.UpdateMark(ctx, "acme", func(prev creds.Mark, found bool) (creds.Mark, error) {
			return creds.Mark{}, nil
		}); err == nil {
			t.Error("the stub scope's state wrote a mark: a stub writes nothing, and a mark it wrote would outlive the stub")
		}
		if _, err := state.Leases(ctx, time.Now()); err == nil {
			t.Error("the stub scope's state read the leases: a lease a stub answered would hold room it never took")
		}
		if err := state.SetPlacement(ctx, creds.Run{Slug: "acme-sandbox", ID: "fb-20261120-041919-8271"}, "acme", creds.Lease{}); err == nil {
			t.Error("the stub scope's state placed a run: stickiness is a decision about which credential a run is on")
		}
		if _, _, err := state.Current(ctx, creds.Run{Slug: "acme-sandbox", ID: "fb-20261120-041919-8271"}); err == nil {
			t.Error("the stub scope's state answered where a run is: found is a decision, and a stub that answered it would tell a call it is on a credential nothing selected")
		}
		if _, err := state.RoundRobin(ctx); err == nil {
			t.Error("the stub scope's state advanced the tie-breaker: the counter is a write, and a stub that advanced one would turn a rotation")
		}
		if _, _, err := state.RepoAuth(ctx, "acme-sandbox"); err == nil {
			t.Error("the stub scope's state answered a repository's auth record: that record is what narrows a repository's reach, and an absent one is not a readable one")
		}
	}

	outcomes := scope.Outcomes()
	if outcomes == nil {
		t.Error("the stub scope answers no outcome reader: the verification asks whether a run has already ended, and a reader that answered false would honour a replayed token")
	} else if _, err := outcomes.OutcomeExists(ctx, 0, "acme-sandbox", "fb-20261120-041919-8271"); err == nil {
		t.Error("the stub scope's outcome reader answered: whether a run ended is evidence the stub cannot have, and false is the answer that would serve it")
	}

	if table := scope.Repos(); len(table) != 0 {
		t.Errorf("the stub scope holds the repository table %v, want none: a slug the table does not hold is refused at the pairing (D1066), which is the only safe answer a stub can give", table)
	}

	offering := scope.Offering()
	if offering == nil {
		t.Error("the stub scope answers no offering reader: the cascade is offered the credentials a call may be served, and a route that could not ask would serve the bundle's whole set")
	} else if _, err := offering.Offering(ctx, &Bundle{}); err == nil {
		t.Error("the stub scope's offering reader answered: a credential offered by a stub is a credential served with nothing having admitted it (R5)")
	}

	forge := scope.Forge()
	if forge == nil {
		t.Error("the stub scope answers no forge reader: the run's git traffic and its own token mint are served out of it")
	} else {
		if sel := forge.Bitbucket(); sel != nil {
			t.Errorf("the stub scope's forge reader answers the Bitbucket selector %T, want none: a token selected by a stub is one nothing chose (T8)", sel)
		}
		if _, err := forge.Covers(ctx, "acme/sandbox"); err == nil {
			t.Error("the stub scope's forge reader answered a coverage question: coverage is the workspace owner's declaration read out of a node the stub does not hold, and true is the answer that would serve an undeclared repository")
		}
	}

	if meter := scope.Meter(); meter != nil {
		t.Error("the stub scope holds a workspace meter: the stub takes no room and keeps no figure, and a meter it held would settle a call against a workspace nothing declared")
	}

	// The constructor WS5 builds the process through. It is asserted to be a
	// function and not asserted to be the stub, deliberately: WS2's own file
	// assigns the real one and the toolchain presents a package's files in name
	// order, so this file's assignment is what makes the constructor non-nil
	// until WS2 lands and WS2's is what answers from then on. What has to hold
	// either way is that the process never calls a nil constructor — that is a
	// panic at startup rather than a refusal, which is the opposite of what this
	// stub is for.
	if NewWorkspaceScopeResolver == nil {
		t.Fatal("NewWorkspaceScopeResolver is nil: WS5 builds the workspace's process through it, and a nil constructor panics at startup instead of refusing")
	}
}
