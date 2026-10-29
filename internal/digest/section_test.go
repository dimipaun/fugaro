package digest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestDigestHasNoExtraSectionByDefault pins the core digest (design
// ee-boundary-moves section 5.4, D528): a build that registered no section hook
// prints the report this package printed before the seam existed, byte for byte
// (G3). The golden is the same file TestDigestGolden reads, so an extra section
// arriving in a build that registered none cannot pass as an improvement.
func TestDigestHasNoExtraSectionByDefault(t *testing.T) {
	SetCore()
	t.Cleanup(SetCore)
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

// TestDigestRendersSectionsWithoutTitles: a section is whatever the hook fills
// in. A body with no title prints as the body alone, with no empty heading; a
// section with neither is dropped rather than printing a blank block, so a hook
// that has nothing for a case prints nothing for it (section 5.4).
func TestDigestRendersSectionsWithoutTitles(t *testing.T) {
	SetCore()
	t.Cleanup(SetCore)
	d := Build(goldenRecords(t), week43(t))
	core := string(Markdown(d))

	err := SetSections(func(Digest) []Section {
		return []Section{
			{Body: "a body with no heading\n"},
			{Title: "Titled"},
			{}, // neither a title nor a body
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	out := string(Markdown(d))
	if !strings.HasPrefix(out, core) {
		t.Fatalf("the report under the extra sections changed:\n%s\nwant the prefix:\n%s", out, core)
	}
	if tail := strings.TrimPrefix(out, core); tail != "\na body with no heading\n\n## Titled\n\n" {
		t.Fatalf("the sections rendered as %q:\n%s", tail, out)
	}
}

// TestCapMeasuresTheCoreReportNotTheSectionHook pins the cap's rule (capTo): it
// is the CORE report that must fit MaxBytes, and a registered hook's tail is
// appended after the trim. So the hook is not run once per dropped row — it is
// run once per Markdown — and its bytes never push the cap into dropping a row
// the core report did not have to drop.
func TestCapMeasuresTheCoreReportNotTheSectionHook(t *testing.T) {
	SetCore()
	t.Cleanup(SetCore)
	p := week43(t)
	// A fresh digest per capTo call: capTo drops rows in place, so two calls
	// must not share one Digest's backing array.
	build := func() Digest {
		return Build([]runstore.Record{
			run(t, "20261021-090000-0003", "2026-10-21T09:00:00Z", 1, 0),
			run(t, "20261021-090000-0004", "2026-10-21T09:00:00Z", 2, 0),
			run(t, "20261021-090000-0005", "2026-10-21T09:00:00Z", 3, 0),
		}, p)
	}
	// One byte under the core report's size, so at least one row must go.
	budget := len(markdownCore(build())) - 1
	plain := capTo(build(), budget)
	if plain.Dropped == 0 {
		t.Fatalf("capTo dropped nothing from a core report over its %d-byte budget", budget)
	}

	var calls int
	if err := SetSections(func(Digest) []Section {
		calls++
		return []Section{{Title: "Tier calibration", Body: strings.Repeat("x", 4000) + "\n"}}
	}); err != nil {
		t.Fatal(err)
	}
	trimmed := capTo(build(), budget)
	if calls != 0 {
		t.Errorf("the section hook ran %d times while rows were dropped: the cap must measure the core report alone", calls)
	}
	if trimmed.Dropped != plain.Dropped {
		t.Errorf("a 4000-byte section changed the row count the cap dropped (%d, want %d): its bytes counted toward the cap", trimmed.Dropped, plain.Dropped)
	}
	if out := string(Markdown(trimmed)); !strings.HasSuffix(out, "\n"+strings.Repeat("x", 4000)+"\n") {
		t.Errorf("the section did not print after the capped report:\n%s", out)
	}
	if calls != 1 {
		t.Errorf("Markdown ran the section hook %d times, want once", calls)
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
