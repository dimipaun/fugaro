//go:build !windows

package cli

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A credentials path that is a FIFO is never opened: the read does not block.
func TestADCInfoNeverBlocksOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "creds")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", fifo)
	done := make(chan struct{})
	go func() { adcInfo(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("adcInfo blocked on a FIFO")
	}
}
