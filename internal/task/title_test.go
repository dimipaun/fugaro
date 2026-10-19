package task

import (
	"encoding/json"
	"strings"
	"testing"
)

// TitleLine is the ONE source every surface that shows a run's title reads:
// the title the launcher stored, else the task prompt's own first line.
func TestTitleLinePrefersTheStoredTitle(t *testing.T) {
	s := &Spec{Task: "first line\nsecond line", Title: "Ship the job title"}
	if got := s.TitleLine(); got != "Ship the job title" {
		t.Fatalf("TitleLine() = %q, want the stored title", got)
	}
}

// Without a stored title, TitleLine is exactly the task's first line: the
// rule every surface followed before --title existed.
func TestTitleLineFallsBackToTheTaskFirstLine(t *testing.T) {
	s := &Spec{Task: "  first line\nsecond line  "}
	if got := s.TitleLine(); got != "first line" {
		t.Fatalf("TitleLine() = %q, want the first line", got)
	}
}

// A nil spec (a run whose task.json could not be read) has no title, and
// never panics.
func TestTitleLineOfNilSpec(t *testing.T) {
	var s *Spec
	if got := s.TitleLine(); got != "" {
		t.Fatalf("TitleLine() = %q, want empty", got)
	}
}

// A title that is only whitespace is not a title: it falls back to the
// task's first line rather than becoming an empty PR title, whatever a
// hand-written task.json says (the decoder's own rule is TitleProblem).
func TestTitleLineIgnoresAWhitespaceTitle(t *testing.T) {
	s := &Spec{Task: "first line\nsecond line", Title: "   "}
	if got := s.TitleLine(); got != "first line" {
		t.Fatalf("TitleLine() = %q, want the first line", got)
	}
}

// TitleProblem is the one list of what a title may be, shared by the
// launcher (fugaro run --title) and the runner (Spec.Validate): every case
// below is refused at both, so a title the launcher accepts is never one
// the runner refuses at bootstrap.
func TestTitleProblem(t *testing.T) {
	for _, tc := range []struct {
		title, want string
	}{
		{"Fix the flaky test", ""},
		{"Ünïcode ✓", ""},
		{strings.Repeat("x", MaxTitleRunes), ""},                             // exactly the cap
		{"", "is empty"},                                                     // nothing at all
		{"   ", "is empty"},                                                  // nothing but whitespace
		{"one\ntwo", "must be one line"},                                     // a second line
		{"one\rtwo", "must be one line"},                                     // a bare CR
		{"one two", "must be one line"},                                      // LINE SEPARATOR: some terminals break here
		{"one\u0085two", "must be one line"},                                 // NEL: ditto
		{"bell\x07", "holds a control character"},                            // C0
		{"esc\x1b[31m", "holds a control character"},                         // ESC: a terminal escape in a PR title
		{"del\x7f", "holds a control character"},                             // DEL
		{"csi\u009b31m", "holds a control character"},                        // C1
		{"nul\x00", "holds a control character"},                             // NUL
		{string([]byte{0xff, 0xfe}), "is not valid UTF-8"},                   // a truncated paste
		{strings.Repeat("x", MaxTitleRunes+1), "longer than 100 characters"}, // one rune over the cap
	} {
		got := TitleProblem(tc.title)
		switch {
		case tc.want == "" && got != "":
			t.Errorf("TitleProblem(%q) = %q, want none", tc.title, got)
		case tc.want != "" && !strings.Contains(got, tc.want):
			t.Errorf("TitleProblem(%q) = %q, want it to say %q", tc.title, got, tc.want)
		}
	}
}

// The cap counts runes, not bytes: a 100-rune title of 3-byte characters is
// legal, and one rune more is not.
func TestTitleProblemCountsRunes(t *testing.T) {
	if got := TitleProblem(strings.Repeat("日", MaxTitleRunes)); got != "" {
		t.Fatalf("100 runes of 3-byte characters: %q", got)
	}
	if got := TitleProblem(strings.Repeat("日", MaxTitleRunes+1)); !strings.Contains(got, "longer than 100 characters") {
		t.Fatalf("101 runes of 3-byte characters: %q", got)
	}
}

// Validate applies the same list, so a task.json written by hand (or by a
// launcher older than this release) is refused at bootstrap rather than
// shown as a title with a newline in it.
func TestValidateRefusesABadTitle(t *testing.T) {
	for _, tc := range []struct {
		title, want string
	}{
		{"", ""},                   // no title at all is legal: the first line is used
		{"Fix the flaky test", ""}, // a good one is legal
		{"two\nlines", "must be one line"},
		{strings.Repeat("x", MaxTitleRunes+1), "longer than 100 characters"},
		{"bell\x07", "holds a control character"},
		// The launcher stores only safetext.Field's form of a title, so a
		// stored title holding what that sanitiser drops was not written by
		// it: a bidi override would otherwise reach the pull request's
		// title verbatim (rawPRText does not sanitise the stored title).
		{"evil‮gnp.exe", "is not in its sanitised form"},
		{"zero​width", "is not in its sanitised form"},
		{"two  spaces", "is not in its sanitised form"},
		{" padded", "is not in its sanitised form"},
	} {
		s := &Spec{Version: 1, RunID: "20261018-120000-abcd", Repo: "acme/app", Ref: "main", Task: "do it", Title: tc.title}
		err := s.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("Validate(%q) = %v, want no error", tc.title, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("Validate(%q) = %v, want it to say %q", tc.title, err, tc.want)
		}
	}
}

// A task without a title writes exactly the JSON the previous release
// wrote: the field is omitted, so a spec read by a runner older than the
// release that reads Title is byte-identical to today's (see the launcher's
// own gate, titlesSince).
func TestMarshalOmitsAnAbsentTitle(t *testing.T) {
	data, err := (&Spec{Version: 1, RunID: "20261018-120000-abcd", Repo: "acme/app", Ref: "main", Task: "do it"}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "title") {
		t.Fatalf("a spec with no title carries one:\n%s", data)
	}
}

// Title round-trips through Parse, and a task.json carrying an unknown
// field is still refused (the decoder's own rule, which is what makes the
// field's version skew a refusal rather than a silent default).
func TestParseRoundTripsTheTitle(t *testing.T) {
	in := &Spec{Version: 1, RunID: "20261018-120000-abcd", Repo: "acme/app", Ref: "main", Task: "do it", Title: "Ship the job title"}
	data, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != in.Title || got.TitleLine() != in.TitleLine() {
		t.Fatalf("title = %q, want %q", got.Title, in.Title)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["title"] != in.Title {
		t.Fatalf("the stored JSON names title %v, want %q", doc["title"], in.Title)
	}
}
