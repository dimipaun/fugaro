package safetext

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// exitErr is a stand-in for a typed error a caller decides on (tf.ExitError is
// the real one): FieldErr must not cost it.
type exitErr struct{ code int }

func (e *exitErr) Error() string { return "exit status 1" }

// TestFieldErrBoundsTheMessageAndKeepsTheCause: an error's own words are
// bounded like any other untrusted value, and errors.Is/errors.As still reach
// through the bound — a wrapper that dropped them would change which exit code
// a caller reads.
func TestFieldErrBoundsTheMessageAndKeepsTheCause(t *testing.T) {
	if FieldErr(nil) != nil {
		t.Error("FieldErr(nil) is not nil")
	}
	cause := &exitErr{code: 1}
	err := FieldErr(fmt.Errorf("reading the marker of p: %w", cause))
	if !errors.Is(err, cause) {
		t.Error("errors.Is does not reach the cause through FieldErr")
	}
	var typed *exitErr
	if !errors.As(err, &typed) || typed.code != 1 {
		t.Errorf("errors.As through FieldErr = %v, %v", typed, err)
	}
	if !strings.Contains(err.Error(), "reading the marker of p") {
		t.Errorf("FieldErr dropped the wrapping sentence: %q", err.Error())
	}
	// A second wrap keeps the bound and the chain.
	twice := fmt.Errorf("terraform: %w", err)
	if !errors.Is(twice, cause) {
		t.Error("a wrap of a FieldErr does not reach the cause")
	}
	if !strings.HasSuffix(twice.Error(), "exit status 1") {
		t.Errorf("twice = %q", twice.Error())
	}

	// A multi-line, escape-laden, enormous message comes out as one bounded
	// field, with none of it left to reach a terminal.
	hostile := errors.New("a\nforged line\x1b[2J‮reversed " + strings.Repeat("x", 10000))
	got := FieldErr(hostile).Error()
	for _, bad := range []string{"\n", "\x1b", "‮"} {
		if strings.Contains(got, bad) {
			t.Errorf("FieldErr left %q in %q", bad, got)
		}
	}
	if n := len([]rune(got)); n != FieldMax || !strings.HasSuffix(got, "…") {
		t.Errorf("FieldErr of a 10k message is %d runes (want %d, ending in an ellipsis)", n, FieldMax)
	}
}

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

// FieldList is the listing every printed list of untrusted values goes
// through: each entry a Field, at most FieldListMax of them, and an honest
// count of the rest.
func TestFieldListBoundsEveryEntryAndTheCount(t *testing.T) {
	hostile := []string{"ok.a", "csi" + string(rune(0x9b)) + "31m.b", "bidi" + string(rune(0x202e)) + ".c",
		"forged\nd.e", strings.Repeat("z", 10000)}

	t.Run("under the cap", func(t *testing.T) {
		shown, omitted := FieldList(hostile)
		if len(shown) != len(hostile) || omitted != 0 {
			t.Fatalf("FieldList showed %d and left out %d, want %d and 0", len(shown), omitted, len(hostile))
		}
		if shown[0] != "ok.a" {
			t.Errorf("an ordinary entry reads as %q, want it unchanged", shown[0])
		}
		for i, s := range shown {
			if s != Field(hostile[i]) {
				t.Errorf("entry %d = %q, want Field of it (%q)", i, s, Field(hostile[i]))
			}
		}
		if n := len([]rune(shown[4])); n != FieldMax {
			t.Errorf("the 10k entry is %d runes, want %d", n, FieldMax)
		}
		if FieldListNote(omitted) != "" {
			t.Errorf("a listing that left nothing out carries a note: %q", FieldListNote(omitted))
		}
	})

	t.Run("over the cap", func(t *testing.T) {
		many := make([]string, FieldListMax+5)
		for i := range many {
			many[i] = fmt.Sprintf("google_storage_bucket.a%d", i)
		}
		shown, omitted := FieldList(many)
		if len(shown) != FieldListMax || omitted != 5 {
			t.Fatalf("FieldList showed %d and left out %d, want %d and 5", len(shown), omitted, FieldListMax)
		}
		if shown[FieldListMax-1] != many[FieldListMax-1] {
			t.Errorf("the last shown entry is %q, want %q", shown[FieldListMax-1], many[FieldListMax-1])
		}
		if note := FieldListNote(omitted); note != "and 5 more, not listed here" {
			t.Errorf("FieldListNote(5) = %q", note)
		}
		line := FieldListLine(many)
		note := " (" + "and 5 more, not listed here" + ")"
		if !strings.HasSuffix(line, note) {
			t.Errorf("FieldListLine does not flag the truncation: %q", line)
		}
		// The listing itself, without the note's own comma: one separator per
		// shown entry but the last, so no entry past the cap reached the line.
		if n := strings.Count(strings.TrimSuffix(line, note), ","); n != FieldListMax-1 {
			t.Errorf("the line carries %d separators, want %d (one per shown entry)", n, FieldListMax-1)
		}
	})

	t.Run("the boundary", func(t *testing.T) {
		exact := make([]string, FieldListMax)
		for i := range exact {
			exact[i] = "a"
		}
		shown, omitted := FieldList(exact)
		if len(shown) != FieldListMax || omitted != 0 {
			t.Errorf("a list of exactly FieldListMax is shown as %d with %d left out", len(shown), omitted)
		}
	})

	t.Run("empty", func(t *testing.T) {
		shown, omitted := FieldList(nil)
		if len(shown) != 0 || omitted != 0 || FieldListLine(nil) != "" {
			t.Errorf("an empty list is shown as %q with %d left out", shown, omitted)
		}
	})
}

// TestFieldKeepsALegitimateAddressAndIsIdempotent: the bound is for display,
// and a display that reads differently from the address it shows is a listing
// of something the owner never wrote. A plan's addresses carry brackets,
// quotes, slashes, colons, dashes, at-signs and non-ASCII letters, and every
// one of them reads back exactly; and bounding a bounded value changes
// nothing, so a value bounded at two layers (an error FieldErr'd and then
// quoted by its caller) reads the same as one bounded once.
func TestFieldKeepsALegitimateAddressAndIsIdempotent(t *testing.T) {
	for _, a := range []string{
		`google_storage_bucket.data`,
		`module.net["eu-west1"].google_compute_network.vpc[0]`,
		`google_project_iam_member.m["roles/viewer:user:a@example.com"]`,
		`module.a.module.b.google_cloud_run_v2_job.web["café_ü-1"]`,
		`data.google_project.p`,
	} {
		if got := Field(a); got != a {
			t.Errorf("Field(%q) = %q, want the address unchanged", a, got)
		}
	}
	for _, in := range []string{
		"plain", "a\nb\tc", "a" + string(rune(0x202e)) + "b", strings.Repeat("x", 10000),
		strings.Repeat("y", FieldMax+1), "  lead and trail  ", "a\x1b]8;;http://x\x07b",
	} {
		once := Field(in)
		if twice := Field(once); twice != once {
			t.Errorf("Field is not idempotent on %.40q: once %.60q, twice %.60q", in, once, twice)
		}
	}
}

// FieldUncut is Field's rewrites without Field's length bound, for a value
// that has to be read EXACTLY as it is: a value the caller encoded so that
// nothing Field does can change it (an identity printed for its owner to paste
// back) is printed whole, and a cut one would be text that matches nothing.
func TestFieldUncutIsFieldWithoutTheCut(t *testing.T) {
	csi, rlo := string(rune(0x9b)), string(rune(0x202e))
	for in, want := range map[string]string{
		"google_storage_bucket.a":        "google_storage_bucket.a",
		"a" + csi + "31mb":               "ab",
		"a" + rlo + "b":                  "ab",
		"a\nforged line":                 "a forged line",
		"a\r\n\t  b":                     "a b",
		"a\x1b[2Jb\x00c":                 "abc",
		"a" + string(rune(0x2028)) + "b": "a b",
		"":                               "",
		"   ":                            "",
	} {
		if got := FieldUncut(in); got != want {
			t.Errorf("FieldUncut(%q) = %q, want %q", in, got, want)
		}
	}
	// The one difference: no cut, so a value over FieldMax is whole, and it
	// carries no ellipsis (a marker that would be four bytes the value does
	// not have).
	long := strings.Repeat("x", 10000)
	if got := FieldUncut(long); got != long {
		t.Errorf("a 10k value came back %d runes, want it whole", len([]rune(got)))
	}
	if got := FieldUncut(strings.Repeat("y", FieldMax+1)); strings.HasSuffix(got, "…") {
		t.Error("a value over the cap came back with Field's cut marker")
	}
	// And below the cap it is Field, so a caller choosing between them changes
	// nothing but the bound.
	for _, in := range []string{"plain", "a\nb\tc", "a" + rlo + "b", "  lead and trail  "} {
		if got, want := FieldUncut(in), Field(in); got != want {
			t.Errorf("FieldUncut(%q) = %q, Field = %q, want them equal below the cap", in, got, want)
		}
	}
}
