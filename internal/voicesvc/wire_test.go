package voicesvc

// The wire contract's tests: the reasons are the app's vocabulary, the reader
// refuses what it cannot read, and nothing a caller wrote reaches a log or a
// terminal unchanged.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
)

// TestReasonConstantsAreTheWiresVocabulary pins the spellings. The app
// branches on a reason (§4.2's indicator, the "device revoked" sentence), so a
// reason is a contract and renaming one is a change to this file and to the
// app in the same release — which is why they are written down here rather than
// left to be read off the constants.
func TestReasonConstantsAreTheWiresVocabulary(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{ReasonForceLocal, "force_local"},
		{ReasonNoAdmission, "no_admission"},
		{ReasonPolicyUnreadable, "policy_unreadable"},
		{ReasonNotAdmitted, "not_admitted"},
		{ReasonNotADevice, "not_a_device"},
		{ReasonDeviceUnknown, "device_unknown"},
		{ReasonTokenUnverified, "token_unverified"},
		{ReasonStateUnavailable, "state_unavailable"},
		{ReasonBadRequest, "bad_request"},
		{ReasonNotFound, "not_found"},
		{StatusPath, "/v1/voice/status"},
		{HeaderAuth, "Authorization"},
	} {
		if tc.got != tc.want {
			t.Errorf("%q, want %q", tc.got, tc.want)
		}
	}
}

// TestProjectShapeIsTaskSlugsShape holds this service's project rule to
// internal/task.Slug, which is what writes the key the policy node lives under.
// The two are separate spellings (there is no exported predicate in
// internal/task) and this is what keeps them one answer: a slug the publisher
// wrote must never be one this service refuses, because the refusal would read
// as a project with no policy.
func TestProjectShapeIsTaskSlugsShape(t *testing.T) {
	for _, repo := range []string{
		"owner/fugaro",
		"owner/my-repo",
		"a/b",
		"o/x9",
		"Owner/Repo",
		"owner/repo.with.dots",
		"owner/repo_with_underscores",
		"owner/" + strings.Repeat("very-long-name-", 8) + "end",
		"owner/-leading-and-trailing-",
		"9lives/repo",
		"gitlab.example.com/group/subgroup/repo",
	} {
		slug, err := task.Slug("github", repo)
		if err != nil {
			t.Fatalf("task.Slug(%q): %v", repo, err)
		}
		if !projectRE.MatchString(slug) {
			t.Errorf("projectRE refuses %q, which is task.Slug(%q): the hop would refuse a project the publisher can write", slug, repo)
		}
	}
}

// TestProjectShapeRefusesWhatIsNotASlug is the other half: a caller's string
// becomes a database path, so the rule is deliberately narrow and every
// near-miss is refused.
func TestProjectShapeRefusesWhatIsNotASlug(t *testing.T) {
	hex16 := strings.Repeat("a", 16)
	for _, project := range []string{
		"",
		"fugaro",
		"-fugaro-" + hex16,
		"Fugaro-" + hex16,
		"fugaro-" + strings.Repeat("a", 15),
		"fugaro-" + strings.Repeat("a", 17),
		"fugaro-" + strings.Repeat("A", 16),
		"fugaro-" + strings.Repeat("g", 16),
		strings.Repeat("a", 47) + "-" + hex16,
		"fugaro_" + hex16,
		"fugaro/" + hex16,
		"fugaro." + hex16,
		"fugaro-" + hex16 + "/../creds",
		"fugaro-" + hex16 + " ",
		"../fugaro-" + hex16,
		"https://example.com/fugaro-" + hex16,
	} {
		if projectRE.MatchString(project) {
			t.Errorf("projectRE admits %q, which is not the slug task.Slug writes", project)
		}
	}
	// The longest slug task.Slug can write is admitted, so the bound is the
	// readable part's 46 characters and not one less.
	if !projectRE.MatchString(strings.Repeat("a", 46) + "-" + hex16) {
		t.Error("projectRE refuses a 46-character readable part, which is task.Slug's own maximum")
	}
}

// TestReadStatusRequestReadsTheOneMember: the query is a slug and nothing else,
// decoded strictly, so a newer app's field is a version skew this service says
// out loud rather than a member it ignores.
func TestReadStatusRequestReadsTheOneMember(t *testing.T) {
	slug := "fugaro-" + strings.Repeat("a", 16)

	req := httptest.NewRequest(http.MethodPost, StatusPath, strings.NewReader(`{"project":"`+slug+`"}`))
	got, err := ReadStatusRequest(req)
	if err != nil {
		t.Fatalf("ReadStatusRequest: %v", err)
	}
	if got.Project != slug {
		t.Fatalf("Project = %q, want %q", got.Project, slug)
	}

	for _, tc := range []struct{ name, body string }{
		{"empty body", ""},
		{"not JSON", "hello"},
		{"a JSON array", "[]"},
		{"no project", `{}`},
		{"an empty project", `{"project":""}`},
		{"a project that is not a slug", `{"project":"fugaro"}`},
		{"a project with a path in it", `{"project":"../../creds"}`},
		{"an unknown member", `{"project":"` + slug + `","device":"m~x"}`},
		{"a member of another case", `{"Project":"` + slug + `"}`},
		{"two documents", `{"project":"` + slug + `"}{"project":"` + slug + `"}`},
		{"a project that is not a string", `{"project":42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, StatusPath, strings.NewReader(tc.body))
			if _, err := ReadStatusRequest(r); err == nil {
				t.Fatalf("the body %q was read", tc.body)
			}
		})
	}
}

// TestReadStatusRequestRefusesAnOversizedBody: the bound is checked while the
// body is read, so a caller cannot spend this service's memory with a payload
// the answer never uses.
func TestReadStatusRequestRefusesAnOversizedBody(t *testing.T) {
	body := `{"project":"` + strings.Repeat("a", maxRequestBytes) + `"}`
	r := httptest.NewRequest(http.MethodPost, StatusPath, strings.NewReader(body))
	if _, err := ReadStatusRequest(r); err == nil {
		t.Fatal("an oversized body was read")
	}
}

// TestReadStatusRequestNeverEchoesTheProject: the refusal names the rule and
// never the value, because the value is a caller's string and a refusal that
// repeated it would be a line the caller writes into this service's log.
func TestReadStatusRequestNeverEchoesTheProject(t *testing.T) {
	const marker = "MARKER-not-a-slug"
	r := httptest.NewRequest(http.MethodPost, StatusPath, strings.NewReader(`{"project":"`+marker+`"}`))
	_, err := ReadStatusRequest(r)
	if err == nil {
		t.Fatal("a project that is not a slug was read")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("the refusal echoes the caller's project: %v", err)
	}
}

// TestWriteWireErrorBoundsAndOneLines: the message is free text on the wire and
// in a log, so it is clipped and made one printable line — a newline or an
// escape sequence in it would forge a second line of a log or repaint a
// terminal.
func TestWriteWireErrorBoundsAndOneLines(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteWireError(rec, http.StatusForbidden, WireError{
		Error:   ReasonDeviceUnknown,
		Message: "line one\nline two\x1b[31mred\x1b[0m " + strings.Repeat("x", 4096),
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var got WireError
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the refusal is not JSON: %v", err)
	}
	if got.Error != ReasonDeviceUnknown {
		t.Fatalf("error = %q, want %q", got.Error, ReasonDeviceUnknown)
	}
	if n := len([]rune(got.Message)); n > maxRefusalMessageBytes {
		t.Fatalf("the message is %d runes, over the bound of %d", n, maxRefusalMessageBytes)
	}
	if strings.ContainsAny(got.Message, "\n\r\x1b") {
		t.Fatalf("the message is not one printable line: %q", got.Message)
	}
	if !strings.HasPrefix(got.Message, "line one?line two") {
		t.Fatalf("the message lost its own text: %q", got.Message)
	}
}

// TestWriteAnswerIsTwoHundredEvenWhenLocal: a project served locally is the
// answer and not a failure of the request (§4.4 point 7), and the answer's own
// members are what the app reads.
func TestWriteAnswerIsTwoHundredEvenWhenLocal(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteAnswer(rec, StatusAnswer{
		Project:   "fugaro-" + strings.Repeat("a", 16),
		Remote:    false,
		Reason:    ReasonForceLocal,
		Direction: StatusDirection{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != contentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", ct, contentTypeJSON)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	if got["remote"] != false || got["reason"] != ReasonForceLocal {
		t.Fatalf("the answer reads %v", got)
	}
	if _, ok := got["providers"]; ok {
		t.Fatalf("a local answer carries a provider list: %v", got)
	}
}

// TestBearerTokenIsExactAboutTheScheme: the scheme is what tells an ID token
// from the basic-credential form a proxy or a browser might have put there.
func TestBearerTokenIsExactAboutTheScheme(t *testing.T) {
	if tok, ok := BearerToken("Bearer abc.def.ghi"); !ok || tok != "abc.def.ghi" {
		t.Fatalf("BearerToken = (%q, %v)", tok, ok)
	}
	for _, h := range []string{"", "abc.def.ghi", "bearer abc", "Basic abc", "Bearer", "Bearer ", "Token abc"} {
		if tok, ok := BearerToken(h); ok {
			t.Errorf("BearerToken(%q) = (%q, true), want false", h, tok)
		}
	}
}

// TestReasonOfDefaultsToState: an error this package did not build is one it
// does not know, and the answer for that is the one the app treats as a reason
// to fall back to local — never an admission and never a claim about a token.
func TestReasonOfDefaultsToState(t *testing.T) {
	if got := ReasonOf(errBoom); got != ReasonStateUnavailable {
		t.Fatalf("ReasonOf(a bare error) = %q, want %q", got, ReasonStateUnavailable)
	}
	if got := ReasonOf(nil); got != ReasonStateUnavailable {
		t.Fatalf("ReasonOf(nil) = %q, want %q", got, ReasonStateUnavailable)
	}
	if got := ReasonOf(refuse(ReasonDeviceUnknown, errBoom)); got != ReasonDeviceUnknown {
		t.Fatalf("ReasonOf = %q, want %q", got, ReasonDeviceUnknown)
	}
}
