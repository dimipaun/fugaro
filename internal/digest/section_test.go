package digest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDigestHasNoExtraSectionByDefault pins the core digest (design
// ee-boundary-moves section 5.4, D528): a build that registered no section hook
// prints the report this package printed before the seam existed, byte for byte
// (G3). The golden is the same file TestDigestGolden reads, so an extra section
// arriving in a build that registered none cannot pass as an improvement.
func TestDigestHasNoExtraSectionByDefault(t *testing.T) {
	d := Build(goldenRecords(t), week43(t))
	got := Markdown(d)
	want, err := os.ReadFile(filepath.Join("testdata", "digest.golden.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("a build with no hook printed another report:\n%s\nwant:\n%s", got, want)
	}
}

// TestDigestRendersARegisteredSection: a registered hook's sections are what
// the report gains, and nothing else -- everything before them is the report
// the same digest prints with no hook at all, so T8.3's calibration section
// cannot rewrite the core figures (G3). The hook is handed the digest being
// rendered: the section is a rendering of it and not of another period's.
func TestDigestRendersARegisteredSection(t *testing.T) {
	SetCore()
	t.Cleanup(SetCore)
	d := Build(goldenRecords(t), week43(t))
	core := string(Markdown(d))

	body := "| tier | median actual/expected |\n| --- | --- |\n| medium | 0.93 |\n"
	err := SetSections(func(got Digest) []Section {
		if got.Period.Key != d.Period.Key {
			t.Errorf("the hook was handed the period %q, want %q", got.Period.Key, d.Period.Key)
		}
		return []Section{{Title: "Tier calibration", Body: body}}
	})
	if err != nil {
		t.Fatal(err)
	}

	out := string(Markdown(d))
	if !strings.HasPrefix(out, core) {
		t.Fatalf("the report under the extra section changed:\n%s\nwant the prefix:\n%s", out, core)
	}
	if tail := strings.TrimPrefix(out, core); tail != "\n## Tier calibration\n\n"+body {
		t.Fatalf("the extra section rendered as %q:\n%s", tail, out)
	}
}

// TestSetSectionsIsOncePerProcess pins the seam's rule (section 5.5): the one
// slot is taken once, a second hook -- and a nil one, which would leave a
// tagged build with no sections at all -- is refused, and a refused call
// changes nothing about the report being rendered.
func TestSetSectionsIsOncePerProcess(t *testing.T) {
	SetCore()
	t.Cleanup(SetCore)
	d := Build(goldenRecords(t), week43(t))
	if err := SetSections(func(Digest) []Section { return []Section{{Title: "first"}} }); err != nil {
		t.Fatal(err)
	}
	first := string(Markdown(d))

	if err := SetSections(func(Digest) []Section { return []Section{{Title: "second"}} }); !errors.Is(err, ErrSectionHookSet) {
		t.Fatalf("a second SetSections = %v, want %v", err, ErrSectionHookSet)
	}
	if err := SetSections(nil); !errors.Is(err, ErrSectionHookNil) {
		t.Fatalf("a nil hook = %v, want %v", err, ErrSectionHookNil)
	}
	if again := string(Markdown(d)); again != first || !strings.Contains(again, "## first") {
		t.Fatalf("a refused SetSections changed the report:\n%s\nwant:\n%s", again, first)
	}
}
