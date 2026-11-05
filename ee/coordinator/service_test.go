// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// The front door as M4 ships it: an HTTP surface with nothing on it, a host
// that binds the port the platform gives it, and the two refusals that keep a
// public endpoint from being something this design cannot ship.
//
// The Terraform half is
// deploy/terraform/gcp/modules/installation/coordinator_service_test.go, which
// holds the revision to the public invoker binding, the coordinator's own
// account and a container whose environment carries no credential. What is
// here is what that container's process does: it refuses a model credential
// rather than serving one, and it serves nothing but what a later task mounts.

// TestTheCoordinatorServiceRefusesAModelCredential is spec.md §9's one
// prohibition at the process: the front door holds no model credential, and a
// revision whose environment carries one does not bind a port at all.
//
// The names are internal/config's list and the provider-key namespace rather
// than string literals here, because those are the names the rule that keeps a
// job from mounting them is written from (IsModelSecretEnv) and a copy in this
// file would be a second list to keep in step. The two values are placeholders:
// nothing in this repository carries a real one, and the refusal below must not
// print one either -- which the last case asserts, because a message an
// operator reads in Cloud Logging is not a place a value may appear.
func TestTheCoordinatorServiceRefusesAModelCredential(t *testing.T) {
	// The five names the installation module sets on the front door's container
	// (coordinator.tf) and nothing else: this is the environment a correct
	// revision has, and it must serve.
	inServiceEnv := []string{
		"FUGARO_PROJECT=aurora",
		"FUGARO_FIREBASE_PROJECT=aurora-fp",
		"FUGARO_RTDB_URL=https://aurora-fp-default-rtdb.firebaseio.com",
		"FUGARO_RUNS_BUCKET=fugaro-runs-proj-1234",
		"FUGARO_COORDINATOR_REPOS=[]",
		"PATH=/usr/local/bin",
		"HOME=/home/nonroot",
	}
	for _, name := range append([]string{"FUGARO_PROVIDER_KEY_OPENROUTER"}, config.ModelSecretEnvs...) {
		t.Run(name, func(t *testing.T) {
			env := append([]string{}, inServiceEnv...)
			env = append(env, name+"=placeholder-value")
			err := RefuseModelCredential(func() []string { return env })
			if err == nil {
				t.Fatalf("%s in the front door's environment was accepted: a public endpoint holding a model credential is the one thing this design must not ship", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name the variable: %v", err)
			}
			for _, v := range env {
				if value, ok := strings.CutPrefix(v, name+"="); ok && strings.Contains(err.Error(), value) {
					t.Errorf("the refusal quotes the value it refused: %v", err)
				}
			}
		})
	}
	t.Run("the service's own environment", func(t *testing.T) {
		if err := RefuseModelCredential(func() []string { return inServiceEnv }); err != nil {
			t.Errorf("the environment the module sets was refused: %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if err := RefuseModelCredential(func() []string { return nil }); err != nil {
			t.Errorf("an empty environment was refused: %v", err)
		}
	})
}

// TestTheCoordinatorServiceServesAndStops drives the host through a real
// listener, a real request and a real shutdown, because the three things it
// asserts are the ones a fake would agree with whatever the code did: the
// service answers what is mounted on it, it answers 404 for everything else
// (M4 ships an empty surface -- M5's and M7's routes are the ones that fill it),
// and a cancelled context returns instead of leaving the process running.
func TestTheCoordinatorServiceServesAndStops(t *testing.T) {
	svc := NewService()
	svc.Handle("/mounted", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "mounted")
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveUntilStop(ctx, serviceHTTPServer(ctx, svc.Handler(), &logs), ln, &logs) }()

	base := "http://" + ln.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}
	var resp *http.Response
	for range 100 {
		resp, err = client.Get(base + "/mounted")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the host never answered on %s: %v", base, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "mounted" {
		t.Errorf("a mounted route answered %d %q, want 200 \"mounted\"", resp.StatusCode, body)
	}

	// The surface M4 ships is empty, and that is the state the plan's later
	// tasks fill: a path nobody mounted is a 404 and not a route that reaches
	// the project's state.
	resp, err = client.Get(base + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unmounted path answered %d, want 404: M4 mounts no route at all", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the host returned an error after the cancellation: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveUntilStop did not return after the cancellation")
	}
	if !strings.Contains(logs.String(), "listening on") {
		t.Errorf("the host's own line does not say what it listens on: %q", logs.String())
	}
}

// TestTheCoordinatorServiceReportsAServeFailureThatRacedTheCancellation is the
// drain's own half of the test above: what the host answers when the
// cancellation and a listener's failure are ready at the same instant, which is
// the one case serveUntilStop's first select decides by coin flip.
//
// The failure is put in the channel by hand rather than raced for, because that
// is the state the coin flip can leave behind and a test that tried to produce
// it through a real listener would assert on which case the runtime happened to
// pick. The server below never served, so its drain is a drain with nothing in
// flight, and the receive at the end of it is what has to report the failure
// rather than a shutdown that never happened.
func TestTheCoordinatorServiceReportsAServeFailureThatRacedTheCancellation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve error
		want  string
	}{
		{name: "a listener that failed", serve: errors.New("the listener stopped"), want: "the listener stopped"},
		{name: "a listener the shutdown closed itself", serve: http.ErrServerClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan error, 1)
			done <- tc.serve
			var logs bytes.Buffer
			err := drainAndReport(&http.Server{}, done, &logs)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("a drain after http.ErrServerClosed returned %v, want the clean shutdown the platform's own signal is", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the drain returned %v, want the listener's own failure: a front door that reports a clean shutdown while its listener is down is a revision that stays down with nothing in its logs saying why", err)
			}
		})
	}
}

// TestTheCoordinatorServiceRefusesAPortThatIsNotOne: PORT is the platform's and
// a value that is not a port is a deployment that is wrong, so it is refused by
// name -- and refused before a listener exists, because a service listening
// somewhere Cloud Run's front end does not reach is one that looks started and
// answers nothing.
func TestTheCoordinatorServiceRefusesAPortThatIsNotOne(t *testing.T) {
	for _, tc := range []struct {
		port string
		want string
	}{
		{port: "", want: serviceDefaultPort},
		{port: "8080", want: "8080"},
		{port: "00080", want: "80"},
		{port: "65535", want: "65535"},
		{port: "0", want: ""},
		{port: "65536", want: ""},
		{port: "-1", want: ""},
		{port: "80a", want: ""},
		{port: "8080 ", want: ""},
	} {
		t.Run("PORT="+tc.port, func(t *testing.T) {
			got, err := servicePort(func(string) string { return tc.port })
			if tc.want == "" {
				if err == nil {
					t.Fatalf("PORT=%q was accepted as %q: a revision listening on a port the platform did not give it is one its front end cannot reach", tc.port, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PORT=%q was refused: %v", tc.port, err)
			}
			if got != tc.want {
				t.Errorf("PORT=%q read as %q, want %q", tc.port, got, tc.want)
			}
		})
	}
	// And the refusal is the host's too, before anything binds: the second half
	// of the sentence above, asserted through the function the command runs.
	err := Serve(context.Background(), NewService(), func(string) string { return "not-a-port" }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("Serve(PORT=not-a-port) = %v, want the port refusal", err)
	}
}
