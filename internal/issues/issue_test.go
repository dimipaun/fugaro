package issues

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// issueAt is the clock every record test runs on: a fixed instant, so the
// header's `created` is decided by the test rather than by the wall.
var issueAt = time.Date(2026, 11, 8, 17, 40, 7, 0, time.UTC)

// issueInstallation is the installation a saved record names: a digest of the
// runs bucket URL, and the same shape `Digest` gives one (F8b).
const issueInstallation = "9f2c4a1b0d3e5f60"

// issueSample is a record with every field set, so a round trip that drops one
// is a failure rather than a zero matching a zero. The values are not meant to
// agree with one another — an issue cannot be pending and have been submitted,
// fixed and set aside — because what this fixture is for is the codec: every
// field the header has, at a value nothing else writes by accident.
func issueSample() Issue {
	i := Issue{
		Version:       RecordVersion,
		Number:        7,
		LocalID:       "local-update-20261107183012-48213",
		Run:           "update-20261107183012-48213",
		Reporter:      "ana@example.test",
		Created:       issueAt,
		FailedCommand: "fugaro update --repo [REDACTED]",
		Class:         ClassFugaro,
		FugaroVersion: "0.9.0",
		OS:            "linux",
		Installation:  issueInstallation,
		Status:        StatusPending,
		URL:           "https://github.com/dimipaun/fugaro/issues/7",
		Uploaded:      true,
		Fingerprint:   "9f2c4a1b0d3e5f60718293a4b5c6d7e89f2c4a1b0d3e5f60718293a4b5c6d7e8",
		ResolvedIn:    "0.21.1",
		Regression:    true,
		OriginalURL:   "https://github.com/dimipaun/fugaro/issues/3",
		Notified:      true,
		// The body is free text the agent wrote, so it can hold a line that
		// looks like one of the record's own headings, one that looks like a
		// fence, and an excerpt cut from a log file keeps its last newline.
		Summary:   "the update stops half way\n\n## Notes\n\nand the second apply hangs",
		Diagnosis: "the provider cache is stale\n```\nand the run gives up",
		Steps:     "1. run fugaro update\n2. wait for the first apply",
		Logs:      []string{"line one\nline two\n", "a second excerpt"},
	}
	// Built through stamp rather than written out, so the fixture is a record
	// this package would write: a transition time it would refuse would make
	// every test on this fixture fail for a reason of its own.
	i.SubmittedAt = stamp(issueAt.Add(2 * time.Hour))
	i.ResolvedAt = stamp(issueAt.Add(26 * time.Hour))
	i.DismissedAt = stamp(issueAt.Add(50 * time.Hour))
	return i
}

// issueLocal is the same record before it has ever been uploaded: no number,
// no URL, the local name D1723 gives it, and no transition behind it — the
// dates are the three members a record carries only once one has happened, so
// the record a machine saves first is the one without them.
func issueLocal() Issue {
	i := issueSample()
	i.Number = 0
	i.Uploaded = false
	i.URL = ""
	i.SubmittedAt, i.ResolvedAt, i.DismissedAt = nil, nil, nil
	return i
}

// issueHeaderAndBody splits a record at the fence between its header and its
// body, for a test that edits one of the two.
func issueHeaderAndBody(t *testing.T, data []byte) (head, body string) {
	t.Helper()
	head, body, ok := strings.Cut(string(data), "\n---\n")
	if !ok {
		t.Fatalf("no fence between the header and the body of:\n%s", data)
	}
	return head, body
}

func TestIssueRoundTrip(t *testing.T) {
	want := issueSample()
	data, err := Marshal(want)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshalling:\n%s\ngave: %v", data, err)
	}
	got.Created, want.Created = got.Created.UTC(), want.Created.UTC()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the record:\ngot  %#v\nwant %#v", got, want)
	}
	// The body is Markdown a person can read: the sections are there by name,
	// in order, and a multi-line section and a second excerpt survive as text.
	for _, want := range []string{"## Summary", "## Diagnosis", "## Steps", "## Log", "line one\nline two"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the record does not carry %q:\n%s", want, data)
		}
	}
	// The body is free text the agent wrote (spec.md), so it can hold a line
	// that reads like a heading; the round trip above is what pins that it comes
	// back whole rather than turning into a section of its own.
	if !strings.Contains(got.Summary, "## Notes") {
		t.Errorf("a heading inside the summary did not survive:\n%s", data)
	}
	// An excerpt is usually cut from a log file and keeps the newline it ends
	// with, which the codec has to give back exactly.
	if !strings.HasSuffix(got.Logs[0], "\n") {
		t.Errorf("the trailing newline of an excerpt did not survive: %q", got.Logs[0])
	}
}

// The record an update's pass leaves behind: the issue is fixed by a release,
// and the release is in the header, so a file that was synced from the shared
// store reads back as the fixed issue it is (the update spec's F27).
func TestIssueRoundTripResolved(t *testing.T) {
	want := issueSample()
	want.Status, want.ResolvedIn = StatusResolved, "0.22.0"
	data, err := Marshal(want)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	for _, line := range []string{"status: resolved\n", "resolved_in: 0.22.0\n"} {
		if !strings.Contains(string(data), line) {
			t.Errorf("the record does not carry %q:\n%s", line, data)
		}
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshalling:\n%s\ngave: %v", data, err)
	}
	if got.Status != StatusResolved || got.ResolvedIn != "0.22.0" {
		t.Errorf("the record came back as %q fixed in %q, want %q in 0.22.0", got.Status, got.ResolvedIn, StatusResolved)
	}
	// And the other record: an issue closed as not planned or a duplicate is
	// set aside with the reason, which is what tells a reader why it closed
	// without a fix (D1752).
	closed := issueSample()
	closed.Status, closed.ClosedAs = StatusDismissed, ClosedDuplicate
	data, err = Marshal(closed)
	if err != nil {
		t.Fatalf("marshalling the closed record: %v", err)
	}
	if !strings.Contains(string(data), "closed_as: duplicate\n") {
		t.Errorf("the record does not carry the reason it closed:\n%s", data)
	}
	if got, err = Unmarshal(data); err != nil {
		t.Fatalf("unmarshalling the closed record:\n%s\ngave: %v", data, err)
	}
	if got.Status != StatusDismissed || got.ClosedAs != ClosedDuplicate {
		t.Errorf("the closed record came back as %q with %q", got.Status, got.ClosedAs)
	}
}

func TestIssueRejectsUnknownHeaderField(t *testing.T) {
	data, err := Marshal(issueSample())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	head, body := issueHeaderAndBody(t, data)
	cases := []struct {
		name string
		head string
		want string
	}{
		{"a field this record does not define", head + "\nsurprise: 1", "field surprise"},
		{"no class at all", strings.Replace(head, "class: fugaro\n", "", 1), "is not a class"},
		{"a status outside the four", strings.Replace(head, "status: pending", "status: closed", 1), "is not a status"},
		{"a way of closing this format does not know", strings.Replace(head, "closed_as: \"\"", "closed_as: completed", 1), "is not a way an issue closed"},
		// YAML's `...` ends a document, so a second one after it would be
		// dropped without a word — the opposite of decoding strictly.
		{"a second document after the header", head + "\n...\nsurprise: 1", "more than one document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Unmarshal([]byte(c.head + "\n---\n" + body))
			if err == nil {
				t.Fatalf("a header with %s was accepted:\n%s", c.name, c.head)
			}
			// Pinned, so a case that is refused for an unrelated reason does
			// not pass as a case that is refused for its own.
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refused for another reason, want %q in: %v", c.want, err)
			}
		})
	}
}

// The schema a reader outside this binary reads a record by carries the two
// vocabularies the resolver writes: the `resolved` status and the reason an
// issue closed without a fix. A value the codec writes has to be one that
// reader accepts, and one it refuses is refused here too.
func TestRecordSchemaAcceptsResolved(t *testing.T) {
	sch, doc := recordSchema(t)

	fixed := issueSample()
	fixed.Status, fixed.ResolvedIn = StatusResolved, "0.22.0"
	closed := issueSample()
	closed.Status, closed.ClosedAs = StatusDismissed, ClosedNotPlanned
	for _, i := range []Issue{fixed, closed} {
		if err := sch.Validate(recordInstance(t, i)); err != nil {
			t.Errorf("the schema refuses a record this package writes: %v", err)
		}
	}
	statuses := []string{string(StatusPending), string(StatusSubmitted), string(StatusDismissed), string(StatusResolved)}
	if got := stringsOf(t, doc, "properties", "status", "enum"); !reflect.DeepEqual(got, statuses) {
		t.Errorf("the schema's statuses are %v, want the four the codec knows %v", got, statuses)
	}
	// The empty value is one of them: every record the codec writes carries
	// the member, and an issue that did not close this way carries nothing in
	// it.
	reasons := []string{"", string(ClosedNotPlanned), string(ClosedDuplicate)}
	if got := stringsOf(t, doc, "properties", "closed_as", "enum"); !reflect.DeepEqual(got, reasons) {
		t.Errorf("the schema's reasons are %v, want %v", got, reasons)
	}
	inst := recordInstance(t, closed).(map[string]any)
	inst["closed_as"] = "completed"
	if err := sch.Validate(inst); err == nil {
		t.Error("the schema accepted a reason the codec does not know")
	}
}

// headerLine is the whole line a member has in a record's header, so a test
// can put another value in exactly that member rather than in every occurrence
// of the value it happens to share with something else.
func headerLine(t *testing.T, head, name string) string {
	t.Helper()
	for _, line := range strings.Split(head, "\n") {
		if strings.HasPrefix(line, name+":") {
			return line
		}
	}
	t.Fatalf("the header carries no %s:\n%s", name, head)
	return ""
}

// withoutHeaderLine drops one member from a record's header, for a test that
// reads a record which does not carry it.
func withoutHeaderLine(t *testing.T, head, name string) string {
	t.Helper()
	line := headerLine(t, head, name)
	return strings.Replace(head, "\n"+line, "", 1)
}

// TestRecordWithoutDatesDecodes covers the one part of the header a record may
// leave out (D1781): `submitted_at`, `resolved_at` and `dismissed_at` are
// absent until the write that makes the transition stamps one, and a record
// that carries none — one an older build wrote, or one that has not got there
// yet — reads back with them empty rather than with an instant nobody made up.
func TestRecordWithoutDatesDecodes(t *testing.T) {
	dated := issueSample()
	data, err := Marshal(dated)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	head, body := issueHeaderAndBody(t, data)
	fields := []struct {
		name string
		at   *time.Time
	}{
		{"submitted_at", dated.SubmittedAt},
		{"resolved_at", dated.ResolvedAt},
		{"dismissed_at", dated.DismissedAt},
	}
	// A record that has been through every transition carries all three, each
	// one as an instant a reader in any zone can compare (`Z`), which is the
	// line `issue show` prints the date from.
	for _, f := range fields {
		if line, want := headerLine(t, head, f.name), f.name+": "+f.at.Format(time.RFC3339); line != want {
			t.Errorf("the record's %s line is %q, want %q", f.name, line, want)
		}
	}

	undated := head
	for _, f := range fields {
		undated = withoutHeaderLine(t, undated, f.name)
	}
	got, err := Unmarshal([]byte(undated + "\n---\n" + body))
	if err != nil {
		t.Fatalf("a record without the dates did not read back:\n%s\ngave: %v", undated, err)
	}
	if got.SubmittedAt != nil || got.ResolvedAt != nil || got.DismissedAt != nil {
		t.Errorf("a record with no dates read back with %v, %v and %v", got.SubmittedAt, got.ResolvedAt, got.DismissedAt)
	}
	// The same members on a record that carries them read back as the instants
	// the header names: the two directions of the codec agree about a record
	// with the dates and about one without them.
	got, err = Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshalling:\n%s\ngave: %v", data, err)
	}
	for _, f := range fields {
		if have := dateOf(t, got, f.name); have == nil || !have.Equal(*f.at) {
			t.Errorf("%s read back as %v, want %v", f.name, have, f.at)
		}
	}
}

// dateOf is one of the record's three dates, by the name its header gives it,
// for a test that walks all three in one loop.
func dateOf(t *testing.T, i Issue, name string) *time.Time {
	t.Helper()
	switch name {
	case "submitted_at":
		return i.SubmittedAt
	case "resolved_at":
		return i.ResolvedAt
	case "dismissed_at":
		return i.DismissedAt
	}
	t.Fatalf("the record has no member %q", name)
	return nil
}

// TestDatesRejectNonUTCOrMalformed covers the shape the three dates have
// (D1781): a record is read by the whole team and by machines in other zones,
// so a transition time is UTC and nothing else, and a value that is not an
// instant refuses the record the way every other strict field does.
func TestDatesRejectNonUTCOrMalformed(t *testing.T) {
	data, err := Marshal(issueSample())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	head, body := issueHeaderAndBody(t, data)
	cases := []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"a time in another zone", "submitted_at", "2026-11-08T19:40:07+02:00", "is not UTC"},
		{"a zero offset that is still not UTC", "submitted_at", "2026-11-08T17:40:07+00:00", "is not UTC"},
		{"a value that is not a time", "submitted_at", "yesterday", "the record's header"},
		{"a month that does not exist", "resolved_at", "2026-13-45T99:99:99Z", "the record's header"},
		{"the dismiss date in another zone", "dismissed_at", "2026-11-09T19:40:07+02:00", "is not UTC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			edited := strings.Replace(head, headerLine(t, head, c.field), c.field+": "+c.value, 1)
			_, err := Unmarshal([]byte(edited + "\n---\n" + body))
			if err == nil {
				t.Fatalf("a record with a %s of %q was accepted:\n%s", c.field, c.value, edited)
			}
			// Pinned, so a case refused for an unrelated reason does not pass
			// as a case refused for its own.
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refused for another reason, want %q in: %v", c.want, err)
			}
		})
	}
	// A writer is refused too, so a caller cannot hand Marshal a record the
	// reader it wrote would refuse: the same check answers both.
	zoned := issueSample()
	at := issueAt.In(time.FixedZone("EET", 2*60*60))
	zoned.ResolvedAt = &at
	if _, err := Marshal(zoned); err == nil {
		t.Error("Marshal wrote a resolution time that is not UTC")
	} else if !strings.Contains(err.Error(), "not UTC") {
		t.Errorf("Marshal refused the time for another reason: %v", err)
	}
	// And the records that carry no date are records this package writes: the
	// shape is what is refused, not the absence.
	undated := issueSample()
	undated.SubmittedAt, undated.ResolvedAt, undated.DismissedAt = nil, nil, nil
	if _, err := Marshal(undated); err != nil {
		t.Errorf("Marshal refused a record whose transitions have not happened: %v", err)
	}
}

// TestSchemaMatchesRecordWithDates covers the three dates and the installation
// against the contract a reader outside this binary validates a record by: the
// dates a record may leave out are members that are not required, what this
// codec refuses they refuse too, and what a saved record carries they require.
func TestSchemaMatchesRecordWithDates(t *testing.T) {
	sch, doc := recordSchema(t)

	undated := issueSample()
	undated.SubmittedAt, undated.ResolvedAt, undated.DismissedAt = nil, nil, nil
	for _, i := range []Issue{undated, issueSample()} {
		if err := sch.Validate(recordInstance(t, i)); err != nil {
			t.Errorf("the schema refuses a record this package writes: %v", err)
		}
	}
	// The three are members of the contract and none of them is required: a
	// document without one is a record whose transition has not happened, and
	// a reader that demanded it would refuse a record this package wrote.
	required := stringsOf(t, doc, "required")
	properties := doc["properties"].(map[string]any)
	for _, name := range []string{"submitted_at", "resolved_at", "dismissed_at"} {
		if _, ok := properties[name]; !ok {
			t.Errorf("the schema does not describe %q", name)
		}
		if slices.Contains(required, name) {
			t.Errorf("the schema requires %q, and a record may leave it out", name)
		}
	}
	// The installation is not one of those: every record this package saves
	// carries it, empty when there was no project to name.
	if !slices.Contains(required, "installation") {
		t.Error("the schema does not require `installation`, which every saved record carries")
	}
	if _, ok := properties["installation"]; !ok {
		t.Error("the schema does not describe `installation`")
	}
	// What the codec refuses, the schema refuses: an instant in a zone other
	// than UTC, and a value that is not a time at all. The dates the codec
	// accepts are ones the schema accepts, which is the record above.
	for _, bad := range []struct{ member, value string }{
		{"submitted_at", "2026-11-08T19:40:07+02:00"},
		{"resolved_at", "2026-11-08T17:40:07+00:00"},
		{"dismissed_at", "not a time"},
		{"installation", "not a digest"},
	} {
		inst := recordInstance(t, issueSample()).(map[string]any)
		if _, ok := inst[bad.member]; !ok && bad.member != "installation" {
			t.Fatalf("the record this test edits carries no %s", bad.member)
		}
		inst[bad.member] = bad.value
		if err := sch.Validate(inst); err == nil {
			t.Errorf("the schema accepts %s %q, which the codec refuses", bad.member, bad.value)
		}
	}
	// The digest a saved record carries is the one the schema publishes, so
	// the two readers of the field agree about what it is.
	if got := recordInstance(t, issueSample()).(map[string]any)["installation"]; got != issueInstallation {
		t.Errorf("the record's installation is %v, want %q", got, issueInstallation)
	}
}

func TestIssueRejectsLocalClass(t *testing.T) {
	// F6: a finding that is only this machine's has no saved class, so `local`
	// is not one — a file that says so is refused rather than read.
	data, err := Marshal(issueSample())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	head, body := issueHeaderAndBody(t, data)
	head = strings.Replace(head, "class: fugaro", "class: local", 1)
	if _, err := Unmarshal([]byte(head + "\n---\n" + body)); err == nil {
		t.Fatal("a record with class `local` was accepted")
	}
}

// TestIssueRejectsALocalIDThatIsNotItsRun covers the one guard that keeps an
// unsaved issue's file name inside the store: before it has a number, the name
// is the header's own local ID (FileName), so the local ID has to be the one
// the run stem gives. The run stem below is a real D1609 stem, so nothing but
// this check refuses the record (F20).
func TestIssueRejectsALocalIDThatIsNotItsRun(t *testing.T) {
	local := issueLocal()
	local.LocalID = "../../escape"
	if _, err := Marshal(local); err == nil {
		t.Fatal("Marshal wrote a record whose local ID is a path")
	} else if !strings.Contains(err.Error(), "local ID") {
		t.Errorf("refused for another reason: %v", err)
	}
	// The same record read back: a record from the shared store is the one this
	// has to hold, so the read path checks it too.
	data, err := Marshal(issueSample())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	head, body := issueHeaderAndBody(t, data)
	head = strings.Replace(head, "local_id: local-update-20261107183012-48213", "local_id: ../../escape", 1)
	if _, err := Unmarshal([]byte(head + "\n---\n" + body)); err == nil {
		t.Fatal("Unmarshal read a record whose local ID is a path")
	} else if !strings.Contains(err.Error(), "local ID") {
		t.Errorf("refused for another reason: %v", err)
	}
}

// TestIssueRejectsAHeaderValueThatSpansLines covers the other guard the file
// name rests on: a value with a newline in it would write text past the fence
// that ends the header, where the reader looks for the body.
func TestIssueRejectsAHeaderValueThatSpansLines(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Issue)
	}{
		{"reporter", func(i *Issue) { i.Reporter = "ana@example.test\nsurprise: 1" }},
		{"failed command", func(i *Issue) { i.FailedCommand = "fugaro update\nrm -rf /" }},
		{"fugaro version", func(i *Issue) { i.FugaroVersion = "0.9.0\n---" }},
		{"os", func(i *Issue) { i.OS = "linux\r\nsurprise: 1" }},
		{"url", func(i *Issue) { i.URL = "https://github.com/dimipaun/fugaro/issues/7\nsurprise: 1" }},
		{"fingerprint", func(i *Issue) { i.Fingerprint = "9f2c4a1b\nsurprise: 1" }},
		{"resolved_in", func(i *Issue) { i.ResolvedIn = "0.21.1\nsurprise: 1" }},
		{"original url", func(i *Issue) { i.OriginalURL = "https://github.com/dimipaun/fugaro/issues/3\nsurprise: 1" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i := issueSample()
			c.edit(&i)
			if _, err := Marshal(i); err == nil {
				t.Fatal("Marshal wrote a record whose header spans lines")
			} else if !strings.Contains(err.Error(), "spans lines") {
				t.Errorf("refused for another reason: %v", err)
			}
			if _, err := i.FileName(); err == nil {
				t.Error("FileName built a name for a record whose header spans lines")
			}
		})
	}
}

func TestIssueFailedCommandIsRedacted(t *testing.T) {
	// D1732: the header keeps the command name and its flags; a value that
	// could be a path, a URL or an identifier goes. The sample secret is built
	// from fragments, so this file carries no secret-shaped literal.
	secret := "sk-" + "live-" + "9f2c4a1b"
	got := RedactCommand([]string{
		"fugaro", "update", "--repo", "/home/ana/private-repo",
		"--token=" + secret, "--url", "https://billing.example.test/v1/usage",
		"my-task", "line\nbreak",
	})
	for _, want := range []string{"fugaro update", "--repo", "--token=", "--url", "my-task"} {
		if !strings.Contains(got, want) {
			t.Errorf("the redacted command lost %q: %s", want, got)
		}
	}
	for _, secret := range []string{"/home/ana", secret, "billing.example.test"} {
		if strings.Contains(got, secret) {
			t.Errorf("the redacted command kept %q: %s", secret, got)
		}
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("the redacted command spans lines, which the header cannot hold: %q", got)
	}
	// The command word is redacted too: a user who ran the binary by path has
	// that path — their home directory and their user name — in os.Args[0]
	// (internal/cli's own rule for os.Args[0]), and the header is the one place
	// the whole team reads. Run by name, `fugaro` survives, as asserted above.
	byPath := RedactCommand([]string{"/home/ana/.local/bin/fugaro", "update"})
	if strings.Contains(byPath, "/home/ana") {
		t.Errorf("the redacted command kept the path it was run by: %s", byPath)
	}
	if !strings.Contains(byPath, "update") {
		t.Errorf("the redacted command lost the subcommand: %s", byPath)
	}
}
