package safetext

import (
	"strings"
	"testing"
)

func TestStripDropsWhatOneLineMarks(t *testing.T) {
	in := "a\x1b]52;c;AAAA\x07b\r\nc​d‮e\x00\xff\tf g"
	if got := Strip(in); got != "abcdefg" {
		t.Fatalf("Strip = %q", got)
	}
	if got := OneLine("x\x1b[2Jy‮z"); got != "xy?z" {
		t.Fatalf("OneLine = %q", got)
	}
}

func TestClip(t *testing.T) {
	if Clip("héllo", 3) != "hél" || Clip("ab", 5) != "ab" || Clip("ab", 0) != "" || Clip(strings.Repeat("é", 9), 4) != "éééé" {
		t.Fatal("Clip")
	}
}
