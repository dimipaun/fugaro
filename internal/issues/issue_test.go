package issues

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// issueAt is the clock every record test runs on: a fixed instant, so the
// header's `created` is decided by the test rather than by the wall.
var issueAt = time.Date(2026, 11, 8, 17, 40, 7, 0, time.UTC)

// issueSample is a record with every field set, so a round trip that drops one
// is a failure rather than a zero matching a zero.
func issueSample() Issue {
	return Issue{
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
		Status:        StatusPending,
		URL:           "https://github.com/dimipaun/fugaro/issues/7",
		Uploaded:      true,
		Fingerprint:   "9f2c4a1b0d3e5f60718293a4b5c6d7e8",
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
}

// issueLocal is the same record before it has ever been uploaded: no number,
// no URL, and the local name D1723 gives it.
func issueLocal() Issue {
	i := issueSample()
	i.Number = 0
	i.Uploaded = false
	i.URL = ""
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
		{"a status outside the three", strings.Replace(head, "status: pending", "status: resolved", 1), "is not a status"},
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
