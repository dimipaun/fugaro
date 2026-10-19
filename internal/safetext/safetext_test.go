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

// TestOneLineEscapesLineAndParagraphSeparators: U+2028 and U+2029 are not
// C0/C1 controls, so they need their own spoofing() entry; some terminals
// treat them as a line break, which would let a field forge a line of its
// own the same way a raw '\n' would.
func TestOneLineEscapesLineAndParagraphSeparators(t *testing.T) {
	lineSep := "a" + string(rune(0x2028)) + "b"
	paraSep := "a" + string(rune(0x2029)) + "b"
	if got := OneLine(lineSep); got != "a?b" {
		t.Errorf("OneLine(line separator) = %q, want a?b", got)
	}
	if got := OneLine(paraSep); got != "a?b" {
		t.Errorf("OneLine(paragraph separator) = %q, want a?b", got)
	}
}

// TestTableCellEscapesTab: TableCell is OneLine for a tabwriter cell, where
// a literal tab would open a new column instead of staying put; OneLine
// itself keeps it (it's meant for standalone lines, not a shared row).
func TestTableCellEscapesTab(t *testing.T) {
	in := "a\tb"
	if got := OneLine(in); got != in {
		t.Fatalf("OneLine(%q) = %q, want it unchanged (tab is ordinary on its own line)", in, got)
	}
	if got := TableCell(in); got != "a?b" {
		t.Fatalf("TableCell(%q) = %q, want a?b", in, got)
	}
	// Otherwise identical to OneLine: newline, ESC and bidi still escaped.
	hostile := "a\nb" + string(rune(0x1b)) + "[2Jc" + string(rune(0x202e)) + "d"
	if TableCell(hostile) != OneLine(hostile) {
		t.Errorf("TableCell and OneLine disagree on %q: %q vs %q", hostile, TableCell(hostile), OneLine(hostile))
	}
}

// Field is the one bound every refusal puts an untrusted value through: one
// line, no C0, C1, bidi or format character, and a cap with an ellipsis.
func TestFieldIsOneBoundedPlainLine(t *testing.T) {
	csi, rlo := string(rune(0x9b)), string(rune(0x202e))
	for in, want := range map[string]string{
		"google_storage_bucket.a":          "google_storage_bucket.a",
		"a" + csi + "31mb":                 "ab",
		"a" + rlo + "b":                    "ab",
		"a\nforged line":                   "a forged line",
		"a\r\n\t  b":                       "a b",
		"a\x1b[2Jb\x00c":                   "abc",
		"a" + string(rune(0x2028)) + "b":   "a b",
		"a" + string(rune(0x85)) + "b":     "a b",
		"a" + string(rune(0x2066)) + "b​c": "abc",
		"a\xffb":                           "ab",
		"a" + string(rune(0xfff9)) + "b" + string(rune(0x0600)) + "c": "abc",
	} {
		if got := Field(in); got != want {
			t.Errorf("Field(%q) = %q, want %q", in, got, want)
		}
	}
	long := Field(strings.Repeat("x", 10000))
	if n := len([]rune(long)); n != FieldMax || !strings.HasSuffix(long, "…") {
		t.Errorf("a 10k value is %d runes (want %d, ending in an ellipsis)", n, FieldMax)
	}
	if exact := strings.Repeat("y", FieldMax); Field(exact) != exact {
		t.Error("a value at the cap was cut")
	}
}
