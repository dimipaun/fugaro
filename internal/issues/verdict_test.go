package issues

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func TestVerdictRefusesUnknownOutcome(t *testing.T) {
	for _, tc := range []struct {
		outcome Outcome
		ok      bool
	}{
		{OutcomeConfiguration, true},
		{OutcomeBug, true},
		{OutcomeFixedInNewerRelease, true},
		{OutcomeUnclear, true},
		// The `local` of the draft is a bucket, not an outcome the prompt
		// states, and a class is not an outcome either.
		{Outcome("local"), false},
		{Outcome("fugaro"), false},
		// The labels are exact: the answer states them, so anything else —
		// another case, a blank line that got read as the value — is refused
		// rather than guessed at.
		{Outcome("Bug"), false},
		{Outcome(""), false},
	} {
		// A signature is set on every row: the two outcomes that are saved
		// need one, and this test is about the outcome alone.
		err := Verdict{
			Outcome:   tc.outcome,
			Summary:   "a summary",
			Signature: "fugaro update: exit status 1 (apply failed)",
		}.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("Validate with outcome %q = %v, want ok=%v", tc.outcome, err, tc.ok)
		}
	}
}

func TestSavedVerdictNeedsASignature(t *testing.T) {
	for _, tc := range []struct {
		outcome   Outcome
		signature string
		ok        bool
	}{
		{OutcomeBug, "fugaro update: exit status 1", true},
		{OutcomeUnclear, "  the provider cache is stale  ", true},
		// Every blank signature hashes to one fingerprint, so a saved verdict
		// without one would attach the next unrelated finding to the issue
		// that got there first — the silent merge F21 forbids (D1724).
		{OutcomeBug, "", false},
		{OutcomeUnclear, "", false},
		{OutcomeBug, "   \n\t ", false},
		// The two outcomes nothing is saved from need no signature: their fix
		// is printed and no record is written.
		{OutcomeConfiguration, "", true},
		{OutcomeFixedInNewerRelease, "", true},
	} {
		err := Verdict{Outcome: tc.outcome, Signature: tc.signature, Summary: "a summary"}.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("Validate with outcome %q and the signature %q = %v, want ok=%v", tc.outcome, tc.signature, err, tc.ok)
		}
	}
}

// fingerprintCases is the values a record's fingerprint member may carry, and
// the codec and the schema have to answer the same way for every one of them:
// a record this package writes is a document a reader validating against the
// schema accepts, and a value the codec refuses is not one the schema lets
// through. Empty is the one value that is not a hash — a record with no
// signature — and everything else is what Fingerprint writes.
var fingerprintCases = []struct {
	value string
	ok    bool
}{
	{"", true},
	{Fingerprint("fugaro update: exit status 1 (apply failed)"), true},
	{strings.Repeat("a", 64), true},
	{"abc", false},
	{strings.Repeat("9f2c4a1b", 4), false},
	{strings.Repeat("A", 64), false},
	{strings.Repeat("a", 65), false},
	{"9f2c4a1b0d3e5f60718293a4b5c6d7e8\n9f2c4a1b0d3e5f60718293a4b5c6d7e8", false},
}

func TestIssueRefusesMalformedFingerprint(t *testing.T) {
	for _, tc := range fingerprintCases {
		i := issueSample()
		i.Fingerprint = tc.value
		if err := i.Validate(); (err == nil) != tc.ok {
			t.Errorf("Validate with the fingerprint %q = %v, want ok=%v", tc.value, err, tc.ok)
		}
	}
}

func TestConfigurationAndFixedInNewerNeverSaved(t *testing.T) {
	for _, tc := range []struct {
		outcome Outcome
		class   Class
		saved   bool
	}{
		{OutcomeBug, ClassFugaro, true},
		{OutcomeUnclear, ClassUnclear, true},
		// A wrong setting and a bug a newer release fixes are this machine's
		// own: their fix is printed and no class exists to save them under
		// (acceptance 1, F6).
		{OutcomeConfiguration, "", false},
		{OutcomeFixedInNewerRelease, "", false},
	} {
		class, saved := Verdict{Outcome: tc.outcome}.Saved()
		if class != tc.class || saved != tc.saved {
			t.Errorf("%q: Saved() = (%q, %v), want (%q, %v)", tc.outcome, class, saved, tc.class, tc.saved)
		}
		// A class nothing is saved under has to be one a record refuses, or
		// the header would persist the two local outcomes after all.
		if saved && !class.Valid() {
			t.Errorf("%q: Saved() gives the class %q, which a record refuses", tc.outcome, class)
		}
		if !saved && class.Valid() {
			t.Errorf("%q: Saved() gives no class and yet %q is one", tc.outcome, class)
		}
	}
}

func TestFingerprintNormalises(t *testing.T) {
	const signature = "fugaro update: exit status 1 (apply failed)"
	want := Fingerprint(signature)
	if !fingerprintRE.MatchString(want) {
		t.Fatalf("Fingerprint(%q) = %q, want a SHA-256 in lowercase hex", signature, want)
	}
	if again := Fingerprint(signature); again != want {
		t.Errorf("Fingerprint is not stable: %q then %q", want, again)
	}
	// Case and whitespace are the two ways the agent's signature comes back
	// spelled differently, and the twice-written report has to find the issue
	// that is already there (D1724).
	for _, same := range []string{
		"  " + signature + "  ",
		"fugaro   update: exit status 1\n(apply failed)",
		strings.ToUpper(signature),
		signature + "\n",
	} {
		if got := Fingerprint(same); got != want {
			t.Errorf("Fingerprint(%q) = %q, and %q is the same signature spelled differently (%q)", same, got, signature, want)
		}
	}
	// And nothing else is folded: two signatures that differ by a word stay
	// two issues, because a merge nobody asked for hides one of them (F21).
	for _, other := range []string{
		"fugaro update: exit status 2 (apply failed)",
		"fugaro setup: exit status 1 (apply failed)",
		"fugaro update: exit status 1 (apply failed) at 18:30",
	} {
		if got := Fingerprint(other); got == want {
			t.Errorf("Fingerprint(%q) = %q, the same as %q: a different signature is a different issue", other, got, signature)
		}
	}
}

// recordSchema compiles schemas/issue-record.schema.json, the contract a
// reader outside this binary — the console, another CLI, a person's editor —
// reads a record by.
func recordSchema(t *testing.T) (*jsonschema.Schema, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "issue-record.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/issue-record.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch, doc.(map[string]any)
}

// recordHeaderMembers is every member the codec writes into a record's header,
// read from the struct rather than written out again: the schema and the
// header are one shape, and a field added to one has to be added to the other.
func recordHeaderMembers(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(Issue{})
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

// recordInstance is a record's header as the JSON value model the validator
// reads, which is how a consumer outside this binary sees a record.
func recordInstance(t *testing.T, i Issue) any {
	t.Helper()
	data, err := Marshal(i)
	if err != nil {
		t.Fatalf("marshalling the record: %v", err)
	}
	head, _, err := splitRecord(data)
	if err != nil {
		t.Fatalf("splitting the record: %v", err)
	}
	var header map[string]any
	if err := yaml.Unmarshal(head, &header); err != nil {
		t.Fatalf("reading the record's header:\n%s\ngave: %v", head, err)
	}
	b, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("converting the record's header to JSON: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return inst
}

func TestSchemaMatchesRecord(t *testing.T) {
	sch, doc := recordSchema(t)

	// The body is the free text the agent wrote, carried as sections under the
	// header's fence in a record and as these members in the `--json`
	// documents `issue list` and `issue show` print; every other member is the
	// header the codec reads and writes.
	want := append(slices.Clone(recordHeaderMembers(t)), "summary", "diagnosis", "steps", "logs")
	got := make([]string, 0, len(doc["properties"].(map[string]any)))
	for name := range doc["properties"].(map[string]any) {
		got = append(got, name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the schema's members are %v, and the record's are %v", got, want)
	}
	// Every header member is required: Marshal writes all of them, empty
	// included, so a document without one is not a record this package wrote.
	for _, name := range recordHeaderMembers(t) {
		if !slices.Contains(stringsOf(t, doc, "required"), name) {
			t.Errorf("the schema does not require %q, and the header always carries it", name)
		}
	}

	// The version is the record's own, and the schema points at the constant
	// that writes it: a comment naming a constant that is gone is a pointer to
	// nothing.
	version := doc["properties"].(map[string]any)["version"].(map[string]any)
	if got := fmt.Sprint(version["const"]); got != strconv.Itoa(RecordVersion) {
		t.Errorf("the schema's version is %v, and RecordVersion is %d", version["const"], RecordVersion)
	}
	if comment, _ := version["$comment"].(string); !strings.Contains(comment, "RecordVersion") {
		t.Errorf("the schema's version carries the comment %q, and nothing points at RecordVersion", comment)
	}

	// A record this package writes is a document the schema accepts, before
	// the upload and after it: the members that only the upload fills in are
	// members of the schema too.
	for _, i := range []Issue{issueLocal(), issueSample()} {
		if err := sch.Validate(recordInstance(t, i)); err != nil {
			t.Errorf("the schema refuses a record this package writes: %v", err)
		}
	}
	// And the two vocabularies bite: a value the codec refuses is refused
	// here, on a record that is otherwise whole — the draft's `local` and the
	// two outcomes that have no class can never be saved (F6).
	for _, bad := range []struct {
		member string
		value  any
	}{
		{"class", "local"},
		{"class", "configuration"},
		{"class", "fixed_in_newer_release"},
		{"status", "done"},
	} {
		inst := recordInstance(t, issueSample()).(map[string]any)
		inst[bad.member] = bad.value
		if err := sch.Validate(inst); err == nil {
			t.Errorf("the schema accepts %s %q, which the codec refuses", bad.member, bad.value)
		}
	}
	// The same record with the other class and the other status is accepted,
	// so the refusals above are the values and not the record they sit in.
	whole := recordInstance(t, issueSample()).(map[string]any)
	whole["class"] = string(ClassUnclear)
	whole["status"] = string(StatusDismissed)
	if err := sch.Validate(whole); err != nil {
		t.Errorf("the schema refuses a record with the class %q and the status %q: %v", ClassUnclear, StatusDismissed, err)
	}

	// The fingerprint is the member the codec and the schema have to agree on
	// member for member: the shape is what Fingerprint writes, and a reader
	// validating a record against the schema must not refuse one this package
	// wrote, nor accept one it refuses.
	for _, tc := range fingerprintCases {
		inst := recordInstance(t, issueSample()).(map[string]any)
		inst["fingerprint"] = tc.value
		if err := sch.Validate(inst); (err == nil) != tc.ok {
			t.Errorf("the schema with the fingerprint %q = %v, and the codec wants ok=%v", tc.value, err, tc.ok)
		}
	}
	// The classes a record may carry are exactly the two the codec knows.
	if got := stringsOf(t, doc, "properties", "class", "enum"); !slices.Equal(got, []string{string(ClassFugaro), string(ClassUnclear)}) {
		t.Errorf("the schema's classes are %v, and the record's are %q and %q", got, ClassFugaro, ClassUnclear)
	}
}

// stringsOf is the array of strings at a path in a schema document.
func stringsOf(t *testing.T, doc map[string]any, path ...string) []string {
	t.Helper()
	node := any(doc)
	for _, key := range path {
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("%v: %q is not an object", path, key)
		}
		node = m[key]
	}
	items, ok := node.([]any)
	if !ok {
		t.Fatalf("%v is not an array", path)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("%v holds %v, which is not a string", path, item)
		}
		out = append(out, s)
	}
	return out
}
