package verify

import (
	"strings"
	"testing"
)

func TestParseJUnitSuites(t *testing.T) {
	xml := `<?xml version="1.0"?>
<testsuites>
  <testsuite name="a">
    <testcase classname="pkg.A" name="ok"/>
    <testcase classname="pkg.A" name="bad"><failure message="x"/></testcase>
    <testcase classname="pkg.A" name="err"><error message="y"/></testcase>
    <testsuite name="nested">
      <testcase classname="pkg.B" name="skip"><skipped/></testcase>
    </testsuite>
  </testsuite>
</testsuites>`
	cases, err := ParseJUnit(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	want := []TestCase{
		{ID: "pkg.A.ok"},
		{ID: "pkg.A.bad", Failed: true},
		{ID: "pkg.A.err", Failed: true},
		{ID: "pkg.B.skip", Skipped: true},
	}
	if len(cases) != len(want) {
		t.Fatalf("got %d cases: %+v", len(cases), cases)
	}
	for i := range want {
		if cases[i] != want[i] {
			t.Errorf("case %d = %+v, want %+v", i, cases[i], want[i])
		}
	}
}

func TestParseJUnitSingleSuiteNoClassname(t *testing.T) {
	cases, err := ParseJUnit(strings.NewReader(`<testsuite><testcase name="renders"/></testsuite>`))
	if err != nil || len(cases) != 1 || cases[0].ID != "renders" {
		t.Fatalf("cases=%+v err=%v", cases, err)
	}
}

func TestParseJUnitMalformed(t *testing.T) {
	if _, err := ParseJUnit(strings.NewReader("<testsuite><testcase")); err == nil {
		t.Fatal("want an error for malformed XML")
	}
}
