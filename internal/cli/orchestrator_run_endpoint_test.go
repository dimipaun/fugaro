package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/endpoint"
)

// The daemon's local endpoint: the registry file it writes at start-up, the
// handshake a reader makes through it, and the file it removes on the way out
// (CG13b).
//
// They are in a file of their own rather than in orchestrator_run_test.go,
// which the open policy-version pull requests also edit.

// endpointState is the state directory the daemon under test places its
// endpoint registry in: a temp directory and the getenv that names it, so no
// write here can reach a developer's own state directory.
func endpointState(t *testing.T) (func(string) string, string) {
	t.Helper()
	dir := t.TempDir()
	getenv := func(name string) string {
		if name == "XDG_STATE_HOME" {
			return dir
		}
		return ""
	}
	return getenv, filepath.Join(dir, "fugaro", "run")
}

// handshake is what a reader does with a registry file: the token from the
// file in the one header, and the id and the nonce the file carries read back
// off the answer (D1359).
func handshake(ctx context.Context, reg endpoint.Registry) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+reg.Addr+"/state", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+reg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the endpoint the registry names did not answer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the endpoint answered %d, and the daemon has ticked", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Fugaro-Orchestrator"); got != reg.ID {
		return fmt.Errorf("the endpoint answered as %q, and the registry names %q", got, reg.ID)
	}
	if got := resp.Header.Get("X-Fugaro-Nonce"); got != reg.Nonce {
		return fmt.Errorf("the endpoint answered with a nonce the registry does not carry")
	}
	return nil
}

// TestRunServesAndRemovesItsRegistryOnTheWayOut: the daemon starts the local
// endpoint at start-up, writes one registry file for it, answers the handshake
// through it, and removes the file on the clean shutdown it already performs
// (CG13b). Nothing sweeps afterwards: the daemon's own exit is what takes its
// file away.
func TestRunServesAndRemovesItsRegistryOnTheWayOut(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.daemon(t, "")
	getenv, dir := endpointState(t)
	d.registryGetenv, d.registryGOOS = getenv, "linux"
	path := filepath.Join(dir, "aurora.json")

	var served bool
	var failed error
	d.wake = func(ctx context.Context) error {
		served = true
		raw, err := os.ReadFile(path)
		if err != nil {
			failed = fmt.Errorf("the daemon wrote no registry file at %s: %w", path, err)
		} else {
			var reg endpoint.Registry
			if err := json.Unmarshal(raw, &reg); err != nil {
				failed = fmt.Errorf("the registry file at %s is not a document this release writes: %w", path, err)
			} else {
				failed = handshake(ctx, reg)
			}
		}
		f.cancel()
		return context.Canceled
	}

	if err := d.start(f.ctx); err != nil {
		t.Fatalf("the daemon's start-up: %v", err)
	}
	if !served {
		t.Fatal("the daemon never reached its first wait, so nothing was checked")
	}
	if failed != nil {
		t.Fatalf("%v", failed)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the daemon left its registry file behind on a clean exit: %v", err)
	}
	if !strings.Contains(out.String(), "the local endpoint answers on") {
		t.Errorf("the daemon never said it serves an endpoint: %q", out.String())
	}
}

// TestARegistryThatCannotBeWrittenNeverFailsAStart: a machine whose state
// directory cannot be written serves no endpoint and drives its id exactly as
// before. The registry is what a reader on this machine uses and never a
// condition of the daemon, so a start-up that failed over it would stop work
// that has nothing to do with it (F28, and the `fugaro update` HARD RULE: the
// daemon creates its own local files and nothing migrates them for it).
func TestARegistryThatCannotBeWrittenNeverFailsAStart(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.daemon(t, "")
	dir := t.TempDir()
	// A file where the state directory would be: nothing can be created under
	// it, whatever the process's privileges are.
	if err := os.WriteFile(filepath.Join(dir, "fugaro"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("planting the file: %v", err)
	}
	d.registryGetenv = func(name string) string {
		if name == "XDG_STATE_HOME" {
			return dir
		}
		return ""
	}
	d.registryGOOS = "linux"

	if err := d.start(f.ctx); err != nil {
		t.Fatalf("a daemon whose registry could not be written failed: %v", err)
	}
	if !strings.Contains(out.String(), "driving it as") {
		t.Errorf("the daemon did not take the lease and drive its id: %q", out.String())
	}
	if !strings.Contains(out.String(), "the local endpoint was not started") {
		t.Errorf("the daemon never reported the endpoint it could not serve: %q", out.String())
	}
}
