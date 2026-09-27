package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets TestSecondSignalForceQuits re-execute this test binary as a
// stand-in for fugaro whose shutdown hangs after the first signal.
func TestMain(m *testing.M) {
	if os.Getenv("FUGARO_TEST_SIGNAL_HELPER") == "1" {
		ctx, stop := signalContext()
		defer stop()
		fmt.Println("started")
		<-ctx.Done()
		fmt.Println("cancelled")
		select {} // a finalize that never finishes
	}
	os.Exit(m.Run())
}

// TestSecondSignalForceQuits: the first Ctrl-C only cancels the context so
// the runner can finalize; a second one must kill the process even if
// finalize is stuck.
func TestSecondSignalForceQuits(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "FUGARO_TEST_SIGNAL_HELPER=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	lines := bufio.NewScanner(out)
	expect := func(want string) {
		t.Helper()
		if !lines.Scan() || strings.TrimSpace(lines.Text()) != want {
			t.Fatalf("helper printed %q (err %v), want %q", lines.Text(), lines.Err(), want)
		}
	}
	expect("started")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	expect("cancelled")

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-exited:
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
				t.Fatalf("helper exited with %v, want death by SIGINT", cmd.ProcessState)
			}
			return
		case <-tick.C:
			_ = cmd.Process.Signal(syscall.SIGINT)
		case <-deadline:
			t.Fatal("a second SIGINT did not force-quit the process")
		}
	}
}
