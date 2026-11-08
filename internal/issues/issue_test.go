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
		Summary:       "the update stops half way",
		Diagnosis:     "the provider cache is stale\nand the run gives up",
		Steps:         "1. run fugaro update\n2. wait for the first apply",
		Logs:          []string{"line one\nline two", "a second excerpt"},
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
	}{
		{"a field this record does not define", head + "\nsurprise: 1"},
		{"no class at all", strings.Replace(head, "class: fugaro\n", "", 1)},
		{"a status outside the three", strings.Replace(head, "status: pending", "status: resolved", 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Unmarshal([]byte(c.head + "\n---\n" + body)); err == nil {
				t.Fatalf("a header with %s was accepted:\n%s", c.name, c.head)
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
}
