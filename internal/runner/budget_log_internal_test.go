package runner

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

func testHalt() runstore.Halt {
	return runstore.Halt{Reason: runstore.HaltKillSwitch, Scope: "global"}
}

// TestSessionLogRedacts: whatever the budget session logs, a credential in
// a message, a string attribute, an error or a group never reaches the job's
// log.
func TestSessionLogRedacts(t *testing.T) {
	const secret = "eyJhbGciOiJSUzI1NiJ9.payload.signature-0123456789"
	r := &run{}
	r.addSecret(secret)
	var buf bytes.Buffer
	log := slog.New(redactHandler{h: slog.NewTextHandler(&buf, nil), redact: r.redact}).With("pre", "kept "+secret)
	log.Warn("failed with "+secret, "text", "a "+secret+" b", "err", errors.New("rtdb: "+secret),
		slog.Group("g", slog.String("inner", secret)), "any", struct{ T string }{secret})
	log.WithGroup("grp").Info("again", "k", secret)
	if strings.Contains(buf.String(), secret) || strings.Contains(buf.String(), "signature-0123") {
		t.Fatalf("a credential reached the log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("nothing was redacted:\n%s", buf.String())
	}
}

// TestHaltExternalIsIgnoredOnceFrozen pins the contract the runner's finalize
// relies on: after the agent loop, no cause from the backend can halt the run,
// and before it one can only be recorded if no cancel or halt came first.
func TestHaltExternalIsIgnoredOnceFrozen(t *testing.T) {
	r := &run{}
	if !r.haltExternal(testHalt()) {
		t.Fatal("the first halt was refused")
	}
	if r.haltExternal(testHalt()) {
		t.Fatal("a second halt was recorded")
	}
	r2 := &run{}
	r2.freezeHalts()
	if r2.haltExternal(testHalt()) || r2.haltValue() != nil {
		t.Fatal("a halt was recorded after the loop ended")
	}
	r3 := &run{}
	r3.markCancelled()
	if r3.haltExternal(testHalt()) {
		t.Fatal("a halt overrode an earlier cancel")
	}
}
