// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// This file drives coordinatorFrontDoor, which is the whole of what M12 does to
// this command: the switch M4's service.go will not keep a second copy of, the
// two lists and the tools that come out of the installation's own environment,
// and the refusal that keeps a door this build cannot finish from being served.
//
// Nothing here touches a cloud: the four clients the command opens — the store,
// the runs bucket, the database the state group reads and the verifier — are the
// frontDoorWiring seam, so a test hands it fakes and the environment is a map
// rather than the process's own (DoorConfigFromEnv takes os.LookupEnv's shape for
// exactly that reason).

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/blobx"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// doorEnv is the environment the installation module renders onto the front
// door's revision when var.mcp_server is set, as the map a test hands the
// command. The issuer is a Cloud Run URL of the shape the module names: scheme,
// host and nothing else, which is what an origin is and what NewServer checks.
func doorEnv() map[string]string {
	return map[string]string{
		mcpauth.FrontDoorEnv: `{"issuer":"https://coordinator-acme-uc.a.run.app",` +
			`"launchers":["user:dev@example.com"],"operators":["user:owner@example.com"]}`,
		fcli.EnvProject:     "acme-project",
		fcli.EnvFirebaseFP:  "acme-project",
		fcli.EnvRTDBURL:     "https://acme-project-default-rtdb.firebaseio.com",
		fcli.EnvRunsBucket:  "fugaro-runs-acme-project",
		envCoordinatorRepos: `[{"provider":"github","repo":"acme/app"}]`,
	}
}

// envLookup is DoorConfigFromEnv's own lookup over a map, so that a test's
// environment is the values it wrote and never the process's — the "set to
// nothing" case (a key present with an empty value) is one a map expresses and
// os.Getenv does not.
func envLookup(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

// refusalWiring is the wiring of a test that asserts no client is opened: every
// function fails the test rather than reaching for a cloud, and the reading of
// the environment has to be complete before any of them is called.
func refusalWiring(t *testing.T) frontDoorWiring {
	t.Helper()
	fail := func(what string) {
		t.Errorf("the command opened %s before it had a front door to open it for", what)
	}
	return frontDoorWiring{
		store: func(context.Context, string) (mcpauth.Store, error) {
			fail("the token record store")
			return nil, errors.New("no cloud")
		},
		bucket: func(context.Context, string) (*blobx.Bucket, error) {
			fail("the installation's runs bucket")
			return nil, errors.New("no cloud")
		},
		state: func(context.Context, string) (mcpauth.StateReach, error) {
			fail("the installation's database")
			return nil, errors.New("no cloud")
		},
		verifier: func(string) (*mcpauth.Verifier, error) {
			fail("the Firebase key source")
			return nil, errors.New("no cloud")
		},
	}
}

// doorStateReach is the state reach a test that has to get past the state
// group's registration hands the wiring: a reach over an empty fake database,
// which the command builds the two tools over and this test never reads. It is
// the real reach rather than a stub, because what is being tested is that the
// command registers the group at all — the reads themselves are
// mcpauth's own file (state_reach_test.go).
func doorStateReach(t *testing.T) mcpauth.StateReach {
	t.Helper()
	db, err := rtdb.New(gcpfake.NewRTDB(t).URL, rtdb.Auth{IDToken: func() string { return "the test reads nothing" }})
	if err != nil {
		t.Fatal(err)
	}
	reach, err := mcpauth.NewProjectState(db)
	if err != nil {
		t.Fatal(err)
	}
	return reach
}

// cliStore is the store a test that only has to get past NewServer's "is there
// one" check hands the wiring. It embeds the interface and implements nothing,
// the way the mcpauth package's own fake does, and the reason it can is that the
// wiring writes no record: everything the door does with a store happens per
// request, and a request here is answered by the door's own refusal.
type cliStore struct{ mcpauth.Store }

// cliKeySource is the verifier's own one-method seam, filled the same way: the
// key set is a network read this test never makes, and no token is verified
// before the door is served.
type cliKeySource struct{}

func (cliKeySource) Key(context.Context, string) (*rsa.PublicKey, error) {
	return nil, errors.New("this test verifies no token")
}

// TestAFrontDoorThatIsOffServesWhatTheRevisionServedBefore: D934's switch off is
// the absence of the module's entry, and what the process serves then is exactly
// what it served before M12 — the empty mux, which is a 404 on every path of the
// door. The metadata documents and the MCP endpoint are two of those paths and
// are asserted beside the rest: a revision that leaked a document without a door
// behind it would be advertising an installation that has none.
func TestAFrontDoorThatIsOffServesWhatTheRevisionServedBefore(t *testing.T) {
	env := doorEnv()
	delete(env, mcpauth.FrontDoorEnv)
	svc, err := coordinatorFrontDoor(context.Background(), envLookup(env), refusalWiring(t))
	if err != nil {
		t.Fatalf("a front door that is off is not a mistake: %v", err)
	}
	for _, path := range []string{
		mcpauth.ProtectedResourcePath,
		mcpauth.AuthorizationServerPath,
		mcpauth.RegisterPath,
		mcpauth.AuthorizePath,
		mcpauth.TokenPath,
		mcpauth.RevokePath,
		mcpauth.MCPPath,
	} {
		rec := httptest.NewRecorder()
		svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("with the door off, %s answered %d, want 404: the revision serves what it served before M12", path, rec.Code)
		}
	}
}

// TestAFrontDoorEntryThatIsNotTheDocumentIsRefused: the entry is a configuration
// an operator wrote, and the three ways of writing one that is not this
// package's document are refused by name. Reading any of them as "off" is the
// one mistake the reading can hide — an operator who wrote an entry and got a
// door that serves nothing goes looking for the fault in the handler rather than
// in the text they applied.
func TestAFrontDoorEntryThatIsNotTheDocumentIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
	}{
		{"not JSON", `not a document`},
		{"present and empty", ``},
		{"a field this door does not read", `{"issuer":"https://coordinator-acme-uc.a.run.app","launchers":[],"operators":[],"admins":["user:dev@example.com"]}`},
		{"two documents", `{"issuer":"https://coordinator-acme-uc.a.run.app"}{"issuer":"https://other-uc.a.run.app"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := doorEnv()
			env[mcpauth.FrontDoorEnv] = tc.entry
			_, err := coordinatorFrontDoor(context.Background(), envLookup(env), refusalWiring(t))
			if err == nil {
				t.Fatalf("%s was read as a front door", tc.name)
			}
			if !strings.Contains(err.Error(), mcpauth.FrontDoorEnv) {
				t.Errorf("the refusal does not name %s, so an operator cannot tell which entry to look at: %v", mcpauth.FrontDoorEnv, err)
			}
		})
	}
}

// TestADoorWithAMissingVariableIsRefusedByName: with the door on, every name the
// door's four dependencies arrive in is checked before anything is opened, and a
// revision missing one fails to start rather than serving a door that admits
// nobody or reads no run. The name is in the refusal because the operator's next
// move is to look at the module that renders it.
func TestADoorWithAMissingVariableIsRefusedByName(t *testing.T) {
	for _, missing := range []string{fcli.EnvProject, fcli.EnvFirebaseFP, fcli.EnvRTDBURL, fcli.EnvRunsBucket, envCoordinatorRepos} {
		t.Run(missing, func(t *testing.T) {
			env := doorEnv()
			delete(env, missing)
			_, err := coordinatorFrontDoor(context.Background(), envLookup(env), refusalWiring(t))
			if err == nil {
				t.Fatalf("the door was built with %s unset", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("the refusal does not name %s: %v", missing, err)
			}
		})
	}
}

// TestARunsBucketThatIsNotANameIsRefused: FUGARO_RUNS_BUCKET names a bucket and
// never a URL, and the command says so rather than handing a path to the bucket
// opener. It is the same check on the same value the sweep makes, and it is here
// because the front door reads the bucket for the read tools rather than for the
// view: a value carrying a scheme would be opened as a URL nobody's IAM names.
func TestARunsBucketThatIsNotANameIsRefused(t *testing.T) {
	for _, value := range []string{"gs://fugaro-runs-acme-project", "projects/acme/buckets/runs", "fugaro runs"} {
		t.Run(value, func(t *testing.T) {
			env := doorEnv()
			env[fcli.EnvRunsBucket] = value
			_, err := coordinatorFrontDoor(context.Background(), envLookup(env), refusalWiring(t))
			if err == nil {
				t.Fatalf("%q was read as a bucket name", value)
			}
			if !strings.Contains(err.Error(), fcli.EnvRunsBucket) {
				t.Errorf("the refusal does not name %s: %v", fcli.EnvRunsBucket, err)
			}
		})
	}
}

// TestTheCommandServesTheDoorOnlyAsFarAsThisBuildCarriesIt is D2126 at the
// wiring: the switch the installation rendered is passed to
// coordinator.FrontDoor together with the build's own answer
// (mcpauth.DeviceDeliveryBuilt) and never with a literal, so a door is enabled
// exactly when this binary can deliver the sign-in flow M12b builds. M12's
// mutation check is this test: writing `Delivered: true` into the wiring makes
// the false half below fail.
//
// The test is written against the constant rather than against its value, so it
// stays true the day M12b flips it: what it pins is that the command passes the
// build's answer, not that the answer is "no" forever. The two halves are the
// two states of the tree — M12b absent (the command refuses) and M12b present
// (the command serves the door's metadata) — and the second is what M12b's own
// task finds already written for it.
func TestTheCommandServesTheDoorOnlyAsFarAsThisBuildCarriesIt(t *testing.T) {
	g := gcpfake.NewGCS(t)
	g.AddBucket(doorEnv()[fcli.EnvRunsBucket], 1234, nil)
	w := frontDoorWiring{
		store: func(context.Context, string) (mcpauth.Store, error) {
			return cliStore{}, nil
		},
		bucket: func(_ context.Context, name string) (*blobx.Bucket, error) {
			return g.Bucket(t, name), nil
		},
		state: func(context.Context, string) (mcpauth.StateReach, error) {
			return doorStateReach(t), nil
		},
		verifier: func(firebaseProject string) (*mcpauth.Verifier, error) {
			return mcpauth.NewVerifier(firebaseProject, cliKeySource{})
		},
	}
	svc, err := coordinatorFrontDoor(context.Background(), envLookup(doorEnv()), w)
	if !mcpauth.DeviceDeliveryBuilt {
		if !errors.Is(err, coordinator.ErrDeviceDeliveryMissing) {
			t.Fatalf("this build carries no delivery of a sign-in flow to the paired device, so the command must refuse to enable a door: err = %v", err)
		}
		if svc != nil {
			t.Fatalf("the command answered a service beside the refusal to enable it")
		}
		return
	}
	if err != nil {
		t.Fatalf("this build carries the delivery, so the command must serve the door: %v", err)
	}
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, mcpauth.AuthorizationServerPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s answered %d, want 200: the enabled door serves its own metadata", mcpauth.AuthorizationServerPath, rec.Code)
	}
}
