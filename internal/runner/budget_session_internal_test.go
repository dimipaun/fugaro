package runner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/verify"
)

// bidiOverride and escByte are a Cf formatting code point (RIGHT-TO-LEFT
// OVERRIDE) and a C0 control byte, named by codepoint rather than written as
// a literal escape so the test file cannot itself be mistaken for one.
const (
	bidiOverride rune = 0x202E
	escByte      rune = 0x1B
)

// TestVerifySummaryLineSanitizesBidiAndControl: an agent-chosen failing
// test's name can hold a bidi override or a raw ESC byte; clipEntry's own
// clipString only strips Cc control characters, not Cf formatting code
// points, so noteVerify must sanitize before the registry (launcher-
// readable, in a terminal) ever sees the raw bytes.
func TestVerifySummaryLineSanitizesBidiAndControl(t *testing.T) {
	r := &run{}
	name := "evil" + string(bidiOverride) + "X" + string(escByte) + "[31mred"
	rec := verify.Record{N: 1, Kind: verify.KindTest, Passed: false, Failed: []string{name}}
	got := r.verifySummaryLine(rec)
	if strings.ContainsRune(got, bidiOverride) || strings.ContainsRune(got, escByte) {
		t.Fatalf("an unsanitized bidi override or ESC byte reached the registry: %q", got)
	}
	wantEscaped := fmt.Sprintf("\\u%04x", bidiOverride)
	if !strings.Contains(got, wantEscaped) {
		t.Fatalf("want the bidi override escaped as %s, got %q", wantEscaped, got)
	}
}

// TestVerifySummaryLineClipsTo120Runes pins the registry's verify-summary
// bound at exactly 120 runes (not just "whatever verifySummaryRunes says"),
// independent of clipEntry's own 200-byte clip on the whole entry.
func TestVerifySummaryLineClipsTo120Runes(t *testing.T) {
	r := &run{}
	rec := verify.Record{N: 1, Kind: verify.KindTest, Passed: false, Failed: []string{strings.Repeat("x", 300)}}
	got := []rune(r.verifySummaryLine(rec))
	if len(got) != 120 {
		t.Fatalf("summary is %d runes, want exactly 120", len(got))
	}
}
