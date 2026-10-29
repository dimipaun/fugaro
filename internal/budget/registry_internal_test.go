package budget

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// TestClipEntryClampsTokens (review focus 4): a Tokens value outside
// [0, MaxEntryTokens], or one the rules would also refuse, never leaves
// clipEntry with it unclamped. StageTokens can exceed the limit (it counts
// cache tokens on top of input/output) and a bug could send a negative
// figure; either would otherwise make the rules refuse the whole entry and
// freeze the heartbeat the way the pre-clamp code did.
func TestClipEntryClampsTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int64
		want int64
	}{
		{"zero", 0, 0},
		{"ordinary", 1200, 1200},
		{"exactly the limit", MaxEntryTokens, MaxEntryTokens},
		{"one over the limit", MaxEntryTokens + 1, MaxEntryTokens},
		{"far over the limit", MaxEntryTokens * 100, MaxEntryTokens},
		{"negative", -1, 0},
		{"very negative", -1_000_000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &AgentEntry{Tokens: tc.in}
			clipEntry(e)
			if e.Tokens != tc.want {
				t.Fatalf("clipEntry(%d) = %d, want %d", tc.in, e.Tokens, tc.want)
			}
		})
	}
}

// TestClipStringStripsSpoofingCharacters (review focus 4): clipString must
// catch what unicode.IsControl alone misses, since a dashboard reader's
// terminal trusts this text: an ESC sequence (which could rewrite the line)
// and a BiDi override or zero-width character (which could make it read
// differently from what it is, e.g. reordering a path or splitting a PR
// URL invisibly).
func TestClipStringStripsSpoofingCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"ESC CSI color escape", "tool Bash: \x1b[31mrm -rf /\x1b[0m"},
		{"RLO override (U+202E)", "tool Read: safe‮txt.exe"},
		{"zero-width space (U+200B)", "tool Bash: go​ test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clipString(tc.in)
			for _, bad := range []string{"\x1b", "‮", "​"} {
				if strings.Contains(got, bad) {
					t.Fatalf("clipString(%q) = %q still carries %q", tc.in, got, bad)
				}
			}
		})
	}
}

// TestProbeCascadeKeysOnlyCachesADefiniteAnswer (M11): the probe's two writes
// can end in five ways, and only one of them is an answer about the cascade's
// children — refused with them, refused without them too, so the children are
// not the cause. Every other outcome has to leave the probe armed, or the
// named creds_rules_denied halt is off for the rest of the run: a write the
// rules accept (the refusal was a blip), an outage, and a second write that
// settles nothing all say nothing about the children, and the refusal after
// any of them may be the real one.
func TestProbeCascadeKeysOnlyCachesADefiniteAnswer(t *testing.T) {
	perm := fmt.Errorf("rtdb: patch: %w", rtdb.ErrPermission)
	down := errors.New("rtdb: patch: 503 UNAVAILABLE")
	for _, tc := range []struct {
		name     string
		errs     []error // what each write answers; the last repeats
		want     cascadeProbe
		wantCall int
	}{
		{"accepted with the children", []error{nil}, cascadeProbeAccepted, 1},
		{"the database is down", []error{down}, cascadeProbeUnavailable, 1},
		{"refused without them too", []error{perm}, cascadeProbeRefusedWithoutTheChildren, 2},
		{"accepted without them", []error{perm, nil}, cascadeProbeChildrenRefused, 2},
		{"the second write meets an outage", []error{perm, down}, cascadeProbeUnavailable, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []AgentEntry
			write := func(e AgentEntry) error {
				sent = append(sent, e)
				if len(sent) <= len(tc.errs) {
					return tc.errs[len(sent)-1]
				}
				return tc.errs[len(tc.errs)-1]
			}
			e := AgentEntry{Credential: "claude-shared-account", WaitingUntil: noonMillis()}
			got := probeCascadeKeys(write, e)
			if got != tc.want {
				t.Fatalf("probeCascadeKeys = %v, want %v", got, tc.want)
			}
			if len(sent) != tc.wantCall {
				t.Fatalf("the probe wrote %d times, want %d", len(sent), tc.wantCall)
			}
			if sent[0] != e {
				t.Fatalf("the first write = %+v, want the entry as sent, %+v", sent[0], e)
			}
			for i, s := range sent[1:] {
				if s.Credential != "" || s.WaitingUntil != 0 {
					t.Fatalf("write %d = %+v, want the entry without the cascade's children", i+2, s)
				}
			}
			if tc.want.definite() != (tc.want == cascadeProbeRefusedWithoutTheChildren) {
				t.Fatalf("%v is cached as an answer, or not, wrongly", tc.want)
			}
			// The entry the caller passed is the caller's: the probe strips
			// its own copy, so a second probe still sends the children.
			if e.Credential == "" || e.WaitingUntil == 0 {
				t.Fatalf("the probe stripped the caller's entry: %+v", e)
			}
		})
	}
}

// noonMillis is a fixed waiting_until for the probe test: any non-zero value
// does, and the probe never reads it.
func noonMillis() int64 { return 1_792_584_000_000 }
