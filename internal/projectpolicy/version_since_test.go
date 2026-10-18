package projectpolicy

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// releaseParts is a release "X.Y.Z" as three integers, or false.
func releaseParts(rel string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(rel, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// TestVersionSinceCoversEveryVersion is the version-bump guard: every policy
// version this code understands has the release that introduced it, and the
// rows go up with the version. A change that raises Version without adding its
// row fails here (the 0.9.0 line's version 3 included), because every too-new
// refusal reads this table to name the release the operator has to reach.
func TestVersionSinceCoversEveryVersion(t *testing.T) {
	var prev [3]int
	for v := 1; v <= Version; v++ {
		rel, ok := VersionSince[v]
		if !ok {
			t.Fatalf("policy version %d has no row in VersionSince: the change that raises Version to %d adds the release that introduces it", v, v)
		}
		cur, ok := releaseParts(rel)
		if !ok {
			t.Fatalf("VersionSince[%d] = %q, want a release X.Y.Z", v, rel)
		}
		if v > 1 && !(cur[0] > prev[0] || cur[0] == prev[0] && (cur[1] > prev[1] || cur[1] == prev[1] && cur[2] > prev[2])) {
			t.Errorf("VersionSince = %v: version %d's release %s does not follow version %d's", VersionSince, v, rel, v-1)
		}
		prev = cur
	}
	// The two rows this line ships, pinned exactly: 1 is the model (0.8.0), 2
	// the profile gate (0.8.5). An off-by-one table fails here.
	for v, want := range map[int]string{1: "0.8.0", 2: "0.8.5"} {
		if got := VersionSince[v]; got != want {
			t.Errorf("VersionSince[%d] = %q, want %q", v, got, want)
		}
	}
}

// TestTooNewSentenceNamesTheIntroducingRelease: a node ahead of its reader is
// answered with the release that introduced its version when the table knows
// it — the case of an older-line binary (one that understands version 1)
// meeting a version-2 node — and with the remedy alone when it does not, for
// both readers. A node behind its reader never gets the clause.
func TestTooNewSentenceNamesTheIntroducingRelease(t *testing.T) {
	for _, reader := range []Reader{ReaderCLI, ReaderRunner} {
		known := SkewSentence(&VersionError{Found: 2, Understood: 1}, reader)
		if !strings.Contains(known, "policy version 2 arrived in fugaro 0.8.5") {
			t.Errorf("reader %v, version 2 over 1: %q does not name 0.8.5", reader, known)
		}
		if strings.Contains(known, "0.8.0") {
			t.Errorf("reader %v, version 2 over 1: %q names 0.8.0, a release the reader already runs", reader, known)
		}
		if got, want := SkewRemedy(&VersionError{Found: 2, Understood: 1}, reader), strings.TrimPrefix(known, fmt.Sprintf(Skew, 2, 1)+"; "); got != want {
			t.Errorf("SkewRemedy = %q, want %q", got, want)
		}
		for _, found := range []int{Version + 1, 99, 1 << 30} {
			msg := SkewSentence(&VersionError{Found: found, Understood: Version}, reader)
			if strings.Contains(msg, "arrived in fugaro") {
				t.Errorf("reader %v, version %d: %q names a release for a version no row holds", reader, found, msg)
			}
			for _, rel := range VersionSince {
				if strings.Contains(msg, rel) {
					t.Errorf("reader %v, version %d: %q borrows %s from another row", reader, found, msg, rel)
				}
			}
		}
		behind := SkewSentence(&VersionError{Found: 1, Understood: 2}, reader)
		if strings.Contains(behind, "arrived in fugaro") {
			t.Errorf("reader %v, a node behind: %q names a release to upgrade to", reader, behind)
		}
	}
	if _, ok := VersionRelease(0); ok {
		t.Error("VersionRelease(0) answers a release; no policy version 0 was ever introduced")
	}
}
