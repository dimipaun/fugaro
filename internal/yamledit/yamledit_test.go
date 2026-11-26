package yamledit_test

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/yamledit"
	"gopkg.in/yaml.v3"
)

// TestSetScalarPreservesComments is the reason the package exists: a settings
// file written by hand keeps every comment and every blank line, and only the
// bytes of the value change.
func TestSetScalarPreservesComments(t *testing.T) {
	const doc = `# Project settings for owner/repo-a.
# Edited by hand; keep the comments.

project: workspace-a

# Where the run's code may go.
allow_data_to:
  - owner/repo-a

# The default rigor mode for this project.
rigor:
  default: standard   # or startup
`

	const want = `# Project settings for owner/repo-a.
# Edited by hand; keep the comments.

project: workspace-a

# Where the run's code may go.
allow_data_to:
  - owner/repo-a

# The default rigor mode for this project.
rigor:
  default: pro   # or startup
`

	got, err := yamledit.SetScalar([]byte(doc), []string{"rigor", "default"}, "pro")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if string(got) != want {
		t.Fatalf("comments or bytes around the edit changed:\n got %q\nwant %q", got, want)
	}
	if !yamledit.Changed([]byte(doc), got) {
		t.Fatal("Changed = false after a real edit")
	}
	if line := changedLine(t, doc, string(got)); line != 12 {
		t.Fatalf("changed line = %d, want 12: only the value's line may change", line)
	}
}

// TestSetScalarQuotesOnlyWhenYAMLNeedsIt pins the quoting decision: a value is
// written as it stands whenever YAML reads those bytes back as exactly this text
// as one scalar — so a number and a boolean are written as the number and the
// boolean the file's schema expects — and quoted when the plain form would read
// as something else.
func TestSetScalarQuotesOnlyWhenYAMLNeedsIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
		reads any
	}{
		{"plain", "pro", "a: pro\n", "pro"},
		{"boolean", "true", "a: true\n", true},
		{"integer", "20", "a: 20\n", 20},
		{"float", "1.5", "a: 1.5\n", 1.5},
		{"null", "null", "a: null\n", nil},
		{"tilde", "~", "a: ~\n", nil},
		{"empty", "", "a: \"\"\n", ""},
		{"hash", "#comment", "a: \"#comment\"\n", "#comment"},
		{"colon space", "a: b", "a: \"a: b\"\n", "a: b"},
		{"leading space", " pro", "a: \" pro\"\n", " pro"},
		{"trailing space", "pro ", "a: \"pro \"\n", "pro "},
		{"sequence", "- pro", "a: \"- pro\"\n", "- pro"},
		{"alias", "*pro", "a: \"*pro\"\n", "*pro"},
		{"newline", "pro\npro", "a: \"pro\\npro\"\n", "pro\npro"},
		{"tab", "pro\tpro", "a: \"pro\\tpro\"\n", "pro\tpro"},
		{"timestamp", "2026-10-31", "a: 2026-10-31\n", time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)},
		{"colon without a space", "a:b", "a: a:b\n", "a:b"},
		{"yaml 1.2 word", "yes", "a: yes\n", "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte("a: old\n"), []string{"a"}, tc.value)
			if err != nil {
				t.Fatalf("SetScalar(%q): %v", tc.value, err)
			}
			if string(got) != tc.want {
				t.Fatalf("SetScalar(%q) = %q, want %q", tc.value, got, tc.want)
			}
			if v := at(t, got, "a"); !reflect.DeepEqual(v, tc.reads) {
				t.Fatalf("the result reads back as %#v, want %#v", v, tc.reads)
			}
		})
	}
}

// TestSetScalarWritesAValueTheWayTheSchemaSpellsIt states the consequence of the
// quoting rule: the package writes settings, and the type each setting has is the
// document's business, not this package's. A `max_runs` written as `"5"` would be
// a file the next run cannot load.
func TestSetScalarWritesAValueTheWayTheSchemaSpellsIt(t *testing.T) {
	for _, tc := range []struct{ name, doc, value, want string }{
		{"a number over a number", "max_runs: 5\n", "9", "max_runs: 9\n"},
		{"a number over a string", "a: pro\n", "9", "a: 9\n"},
		{"a boolean over a string", "a: pro\n", "true", "a: true\n"},
		{"a string over a string", "a: pro\n", "standard", "a: standard\n"},
		{"a number written as a number", "max_runs: 5\n", "5", "max_runs: 5\n"},
		{"a float over an integer", "max_runs: 5\n", "5.5", "max_runs: 5.5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), []string{strings.SplitN(tc.want, ":", 2)[0]}, tc.value)
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			reparses(t, got)
		})
	}
}

func TestSetScalarKeepsAnExistingQuotingStyle(t *testing.T) {
	const doc = "single: 'owner/repo-a'\ndouble: \"owner/repo-b\"\nplain: owner/repo-c\n"
	for _, tc := range []struct{ key, want string }{
		{"single", "single: 'owner/repo-d'\ndouble: \"owner/repo-b\"\nplain: owner/repo-c\n"},
		{"double", "single: 'owner/repo-a'\ndouble: \"owner/repo-d\"\nplain: owner/repo-c\n"},
		{"plain", "single: 'owner/repo-a'\ndouble: \"owner/repo-b\"\nplain: owner/repo-d\n"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(doc), []string{tc.key}, "owner/repo-d")
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSetScalarReplacesAQuotedValueWhole pins where a quoted value ends: a
// single-quoted scalar holds a quote by doubling it, a double-quoted one by
// escaping it, and a span that stopped at the first quote inside would leave the
// tail of the old value in the file beside the new one.
func TestSetScalarReplacesAQuotedValueWhole(t *testing.T) {
	for _, tc := range []struct{ name, doc, value, want string }{
		{"a single-quoted value holding a quote", "a: 'it''s here'\nb: 1\n", "z", "a: 'z'\nb: 1\n"},
		{"a single-quoted value ending in a quote", "a: 'a '''\n", "z", "a: 'z'\n"},
		{"a double-quoted value holding an escaped quote", "a: \"x \\\" y\"\nb: 1\n", "z", "a: \"z\"\nb: 1\n"},
		{"a double-quoted value holding a backslash", "a: \"x \\\\ y\"\nb: 1\n", "z", "a: \"z\"\nb: 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), []string{"a"}, tc.value)
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if v := at(t, got, "a"); v != tc.value {
				t.Fatalf("the result reads back %#v, want %q", v, tc.value)
			}
		})
	}
}

func TestSetScalarCreatesMissingMappingsAtTheParentsIndentation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		doc   string
		path  []string
		value string
		want  string
		reads any
	}{
		{
			"a new top-level key",
			"project: workspace-a\n",
			[]string{"budget", "max_runs"}, "5",
			"project: workspace-a\nbudget:\n  max_runs: 5\n", 5,
		},
		{
			"the indentation the document already uses",
			"a:\n    b: 1\n",
			[]string{"a", "c"}, "2",
			"a:\n    b: 1\n    c: 2\n", 2,
		},
		{
			"a whole chain under a key written with no value",
			"a:\n",
			[]string{"a", "b", "c"}, "v",
			"a:\n  b:\n    c: v\n", "v",
		},
		{
			"under a key whose line carries a comment",
			"a:  # note\n",
			[]string{"a", "b"}, "one",
			"a:  # note\n  b: one\n", "one",
		},
		{
			"after a nested block",
			"a:\n  b: 1\nc: 2\n",
			[]string{"a", "d"}, "3",
			"a:\n  b: 1\n  d: 3\nc: 2\n", 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), tc.path, tc.value)
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if v := at(t, got, tc.path...); !reflect.DeepEqual(v, tc.reads) {
				t.Fatalf("the result reads back %#v at %v, want %#v", v, tc.path, tc.reads)
			}
		})
	}
}

func TestSetScalarFillsAKeyWrittenWithNoValue(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		path      []string
		want      string
	}{
		{"last line", "b: 2\na:\n", []string{"a"}, "b: 2\na: 1\n"},
		{"trailing spaces stay", "a:   \nb: 2\n", []string{"a"}, "a: 1   \nb: 2\n"},
		{"comment stays", "a:  # note\nb: 2\n", []string{"a"}, "a: 1  # note\nb: 2\n"},
		{"inside a block", "a:\n  b:\nc: 3\n", []string{"a", "b"}, "a:\n  b: 1\nc: 3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), tc.path, "1")
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if v := at(t, got, tc.path...); v != 1 {
				t.Fatalf("the result reads back %#v, want 1", v)
			}
		})
	}
}

// TestSetScalarAppendsAfterTheLineThatEndsTheDocument pins where a new
// top-level key goes when the document's last node is written with nothing at
// all: on the line the last one ends, never on the empty line a trailing break
// opens, which is text a block scalar keeping its breaks would fold back into
// itself.
func TestSetScalarAppendsAfterTheLineThatEndsTheDocument(t *testing.T) {
	for _, tc := range []struct {
		name, doc, want string
	}{
		{"no line of its own to spare", "?\n>+\n", "?\n>+\n\"0\": 0\n"},
		{"a blank line at the end stays", "?\n>+\n\n", "?\n>+\n\n\"0\": 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), []string{"0"}, "0")
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if v := at(t, got, "0"); !reflect.DeepEqual(v, 0) {
				t.Fatalf("the result reads back %#v at 0, want 0", v)
			}
		})
	}
}

// TestSetScalarRefusesToReplaceACollection pins the guard against losing a
// whole block: a mapping or a sequence holds bytes, and comments, of its own, so
// a scalar is not spliced over it. Removing the key, or writing a key inside it,
// are the edits that work instead.
func TestSetScalarRefusesToReplaceACollection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    string
		path   []string
		reason yamledit.Reason
	}{
		{"a mapping with a comment inside it", "a:\n  # keep me\n  x: 1\nb: 1\n", []string{"a"}, yamledit.ReasonReplacesCollection},
		{"a sequence", "list:\n  - x\n", []string{"list"}, yamledit.ReasonReplacesCollection},
		{"a mapping with a comment on the key's line", "a: # note\n  x: 1\n", []string{"a"}, yamledit.ReasonReplacesCollection},
		{"a nested mapping", "a:\n  b:\n    c: 1\nd: 2\n", []string{"a"}, yamledit.ReasonReplacesCollection},
		{"a flow mapping", "a: {b: 1}\n", []string{"a"}, yamledit.ReasonFlowCollection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), tc.path, "z")
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

func TestSetScalarIsANoOpWhenTheValueIsAlreadyThere(t *testing.T) {
	for _, tc := range []struct{ doc, value string }{
		{"a: pro\n", "pro"},
		{"a: 'pro'\n", "pro"},
		{"a: \"pro\"\n", "pro"},
		{"a: pro  # c\n", "pro"},
		{"a: 1\n", "1"},
		{"a: true\n", "true"},
		{"a: 2026-10-31\n", "2026-10-31"},
	} {
		got, err := yamledit.SetScalar([]byte(tc.doc), []string{"a"}, tc.value)
		if err != nil {
			t.Fatalf("SetScalar(%q): %v", tc.doc, err)
		}
		if !bytes.Equal(got, []byte(tc.doc)) {
			t.Fatalf("SetScalar(%q, %q) rewrote the document: %q", tc.doc, tc.value, got)
		}
		if yamledit.Changed([]byte(tc.doc), got) {
			t.Fatalf("Changed = true for %q", tc.doc)
		}
	}
}

// TestARootWrittenIntoTheInputDoesNotAliasIt pins the promise the package makes:
// an edit returns new bytes. A document that holds nothing but comments is the
// one input an edit extends instead of splicing, and that append must not land
// in the caller's own array — a caller that goes on writing into its buffer, or
// hands it to someone who does, would otherwise find the result changing under
// it.
func TestARootWrittenIntoTheInputDoesNotAliasIt(t *testing.T) {
	const doc = "# a note about the file\n"
	const want = doc + "budget:\n  max_runs: 5\n"

	in := make([]byte, 0, 256)
	in = append(in, doc...)
	got, err := yamledit.SetScalar(in, []string{"budget", "max_runs"}, "5")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	in = append(in, 'X')
	if string(got) != want {
		t.Fatalf("writing past the input's length changed the result: %q, want %q", got, want)
	}
}

func TestSetScalarOnAnEmptyOrCommentOnlyDocument(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"empty", "", "budget:\n  max_runs: 5\n"},
		{"no trailing break", "# header", "# header\nbudget:\n  max_runs: 5\n"},
		{"comment only", "# header\n", "# header\nbudget:\n  max_runs: 5\n"},
		{"blank lines only", "\n\n# header\n", "\n\n# header\nbudget:\n  max_runs: 5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), []string{"budget", "max_runs"}, "5")
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if v := at(t, got, "budget", "max_runs"); v != 5 {
				t.Fatalf("the result reads back %#v, want 5", v)
			}
		})
	}
}

func TestRemoveKeyKeepsSiblingsAndComments(t *testing.T) {
	const doc = `project: workspace-a
allow_data_to:
  - owner/repo-a
rigor:
  default: standard
  areas:
    - name: one
budget:
  max_runs: 5
`
	const want = `project: workspace-a
allow_data_to:
  - owner/repo-a
rigor:
  default: standard
budget:
  max_runs: 5
`
	got, err := yamledit.RemoveKey([]byte(doc), []string{"rigor", "areas"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if has(t, got, "rigor", "areas") {
		t.Fatal("rigor.areas is still there")
	}
	if v := at(t, got, "rigor", "default"); v != "standard" {
		t.Fatalf("the sibling changed: %#v", v)
	}
}

func TestRemoveKeyTakesTheCommentBlockDirectlyAboveIt(t *testing.T) {
	const doc = `a: 1

# budget:
#   max_runs: 20
budget:
  max_runs: 5
`
	got, err := yamledit.RemoveKey([]byte(doc), []string{"budget"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if want := "a: 1\n\n"; string(got) != want {
		t.Fatalf("got %q, want %q: the comments sit directly above the key", got, want)
	}

	const separated = `a: 1
# a note about the file, not about b

b: 2
`
	got, err = yamledit.RemoveKey([]byte(separated), []string{"b"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if want := "a: 1\n# a note about the file, not about b\n\n"; string(got) != want {
		t.Fatalf("got %q, want %q: a blank line ends what the comments belong to", got, want)
	}
}

func TestRemoveKeyOfAMissingKeyIsANoOp(t *testing.T) {
	const doc = "a: 1\n# b: 2\n"
	for _, path := range [][]string{{"b"}, {"c", "d"}, {"c", "d", "e"}} {
		got, err := yamledit.RemoveKey([]byte(doc), path)
		if err != nil {
			t.Fatalf("RemoveKey(%v): %v", path, err)
		}
		if !bytes.Equal(got, []byte(doc)) {
			t.Fatalf("RemoveKey(%v) = %q, want the input unchanged", path, got)
		}
	}
}

func TestRemoveKeyDropsEmptyParentsWhenAsked(t *testing.T) {
	const doc = "a:\n  b: 1\nc: 2\n"
	got, err := yamledit.RemoveKey([]byte(doc), []string{"a", "b"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if want := "a:\nc: 2\n"; string(got) != want {
		t.Fatalf("without the option: got %q, want %q", got, want)
	}
	got, err = yamledit.RemoveKeyWithOptions([]byte(doc), []string{"a", "b"}, yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		t.Fatalf("RemoveKeyWithOptions: %v", err)
	}
	if want := "c: 2\n"; string(got) != want {
		t.Fatalf("with the option: got %q, want %q", got, want)
	}

	got, err = yamledit.RemoveKey([]byte("a: 1\n"), []string{"a"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("removing the last key of the root left %q, want nothing", got)
	}
}

// TestDropEmptyParentsRefusesWhatTheCascadeWouldTake pins that the entries a
// cascade reaches are held to the same rules as the entry the caller named. An
// intermediate entry an anchor, a tag or a merge key is written on is refused,
// or the cascade would drop it silently — and an alias pointing at a dropped
// anchor would be left pointing at nothing.
func TestDropEmptyParentsRefusesWhatTheCascadeWouldTake(t *testing.T) {
	drop := yamledit.RemoveOptions{DropEmptyParents: true}
	for _, tc := range []struct {
		name   string
		doc    string
		op     func([]byte) ([]byte, error)
		reason yamledit.Reason
	}{
		{
			"an anchor on a parent the cascade would remove",
			"other: 1\nouter: &a\n  inner: 2\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveKeyWithOptions(d, []string{"outer", "inner"}, drop)
			},
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"an anchor an alias elsewhere points at",
			"outer: &a\n  inner: 2\ncopy: *a\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveKeyWithOptions(d, []string{"outer", "inner"}, drop)
			},
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"a tag on a parent the cascade would remove",
			"other: 1\nouter: !!map\n  inner: 2\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveKeyWithOptions(d, []string{"outer", "inner"}, drop)
			},
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"an anchor on a parent a list removal would remove",
			"outer: &a\n  list:\n    - x\nother: 1\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveListItemWithOptions(d, []string{"outer", "list"}, "x", drop)
			},
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"an anchor on a parent a keyed removal would remove",
			"outer: &a\n  areas:\n    - path: internal/one/**\nother: 1\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveSeqItemByKeyWithOptions(d, []string{"outer", "areas"}, "path", "internal/one/**", drop)
			},
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"a merge key on a mapping the cascade would remove a key from",
			"base: &base\n  other: 1\n<<: *base\nouter:\n  inner: 2\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveKeyWithOptions(d, []string{"outer", "inner"}, drop)
			},
			yamledit.ReasonMergeKey,
		},
		{
			"a merge key on the mapping a list removal would empty",
			"base: &base\n  other: 1\nouter:\n  <<: *base\n  list:\n    - x\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveListItemWithOptions(d, []string{"outer", "list"}, "x", drop)
			},
			yamledit.ReasonMergeKey,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.op([]byte(tc.doc))
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

func TestRemoveKeyFromTheFixtureKeepsEveryOtherLine(t *testing.T) {
	doc := readFixture(t, "commented-project.yaml")
	got, err := yamledit.RemoveKey([]byte(doc), []string{"rigor", "areas"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if !has(t, got, "rigor") {
		t.Fatal("rigor went with its list")
	}
	if has(t, got, "rigor", "areas") {
		t.Fatal("rigor.areas is still there")
	}
	if strings.Contains(string(got), "      path: internal/one/**") {
		t.Fatalf("the list is still written:\n%s", got)
	}
	for _, kept := range []string{
		"# Project settings for owner/repo-a.",
		"allow_data_to:",
		"  - owner/repo-b",
		"# (startup is the fastest one.)",
		"rigor:",
		"  default: standard   # or startup",
		"# Pointed at a person, not at a group.",
		"    - person-1",
		"# budget:",
		"#   max_runs: 20",
		"  max_runs: 5",
	} {
		if !strings.Contains(string(got), kept) {
			t.Fatalf("%q went missing:\n%s", kept, got)
		}
	}
}

// TestRemoveKeyOfAnEmptyFlowCollection pins that a removal only has to measure
// what it removes: `{}` and `[]` carry no entries, so the line they sit on goes
// like any other.
func TestRemoveKeyOfAnEmptyFlowCollection(t *testing.T) {
	for _, tc := range []struct {
		name, doc, want string
		path            []string
	}{
		{"an empty flow mapping", "a: {}\nb: 1\n", "b: 1\n", []string{"a"}},
		{"an empty flow sequence", "a: []\n", "", []string{"a"}},
		{"an empty flow sequence inside a block", "a:\n  x: []\nb: 1\n", "a:\nb: 1\n", []string{"a", "x"}},
		{"an empty flow mapping with a comment", "a: {}  # nothing yet\nb: 1\n", "b: 1\n", []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.RemoveKey([]byte(tc.doc), tc.path)
			if err != nil {
				t.Fatalf("RemoveKey: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			reparses(t, got)
			if has(t, got, tc.path...) {
				t.Fatalf("%v is still in %q", tc.path, got)
			}
		})
	}
}

// TestRemoveKeyKeepsALessIndentedComment pins where the comment block an entry
// owns ends: a comment above it that is indented less than the entry belongs to
// the level above and stays, or a removal would take the file's notes with it.
func TestRemoveKeyKeepsALessIndentedComment(t *testing.T) {
	const doc = "outer:\n  # about y\n  y: 1\n# kept\n  x: 2\n"
	got, err := yamledit.RemoveKey([]byte(doc), []string{"outer", "x"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if want := "outer:\n  # about y\n  y: 1\n# kept\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRemoveKeyKeepsTheFileHeaderComment pins that a comment block reaching the
// file's first line is the file's own header and stays, whether the key removed
// is the file's first or triggers a cascade. The block is kept whole: which of
// its lines sat next to the key would be arbitrary. A comment between later keys
// is still the entry's, which TestRemoveKeyTakesTheCommentBlockDirectlyAboveIt
// holds.
func TestRemoveKeyKeepsTheFileHeaderComment(t *testing.T) {
	drop := yamledit.RemoveOptions{DropEmptyParents: true}
	for _, tc := range []struct {
		name, doc, want string
		op              func([]byte) ([]byte, error)
	}{
		{
			"the file's first key",
			"# Licence header\nproject: x\nb: 1\n",
			"# Licence header\nb: 1\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"project"}) },
		},
		{
			"a header written over several lines",
			"# one\n# two\nproject: x\nb: 1\n",
			"# one\n# two\nb: 1\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"project"}) },
		},
		{
			"the cascade a list removal runs up to the first line",
			"# hdr\nk:\n  - a\nz: 1\n",
			"# hdr\nz: 1\n",
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveListItemWithOptions(d, []string{"k"}, "a", drop)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.op([]byte(tc.doc))
			if err != nil {
				t.Fatalf("removal: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			reparses(t, got)
		})
	}
}

func TestAddListItemAppendsAndCreatesTheSequence(t *testing.T) {
	const doc = "allow_data_to:\n  - owner/repo-a\n\n# next\nother: 1\n"
	got, err := yamledit.AddListItem([]byte(doc), []string{"allow_data_to"}, "owner/repo-b")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if want := "allow_data_to:\n  - owner/repo-a\n  - owner/repo-b\n\n# next\nother: 1\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if items := listAt(t, got, "allow_data_to"); len(items) != 2 || items[1] != "owner/repo-b" {
		t.Fatalf("the list reads back %v", items)
	}

	got, err = yamledit.AddListItem([]byte("rigor:\n  default: standard\n"), []string{"rigor", "areas"}, "internal/one/**")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if want := "rigor:\n  default: standard\n  areas:\n    - internal/one/**\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got, err = yamledit.AddListItem([]byte(""), []string{"allow_data_to"}, "owner/repo-a")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if want := "allow_data_to:\n  - owner/repo-a\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got, err = yamledit.AddListItem([]byte("allow_data_to:  # the repositories\n"), []string{"allow_data_to"}, "owner/repo-a")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if want := "allow_data_to:  # the repositories\n  - owner/repo-a\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestAddListItemAppendsToAListOfMappings pins that a sequence of mappings is a
// sequence: a scalar is appended after the last item, whatever the items hold.
func TestAddListItemAppendsToAListOfMappings(t *testing.T) {
	const doc = "k:\n  - name: one\n    mode: two\n"
	got, err := yamledit.AddListItem([]byte(doc), []string{"k"}, "z")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if want := "k:\n  - name: one\n    mode: two\n  - z\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAddListItemIsANoOpWhenTheValueIsAlreadyThere(t *testing.T) {
	const doc = "allow_data_to:\n  - owner/repo-a\n"
	got, err := yamledit.AddListItem([]byte(doc), []string{"allow_data_to"}, "owner/repo-a")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatalf("AddListItem wrote a duplicate: %q", got)
	}
}

func TestRemoveListItemRemovesOnlyThatItem(t *testing.T) {
	const doc = `allow_data_to:
  # the first one
  - owner/repo-a
  - owner/repo-b
other: 1
`
	got, err := yamledit.RemoveListItem([]byte(doc), []string{"allow_data_to"}, "owner/repo-a")
	if err != nil {
		t.Fatalf("RemoveListItem: %v", err)
	}
	if want := "allow_data_to:\n  - owner/repo-b\nother: 1\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got, err = yamledit.RemoveListItem([]byte(doc), []string{"allow_data_to"}, "owner/repo-c")
	if err != nil {
		t.Fatalf("RemoveListItem: %v", err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatal("removing something that is not there changed the document")
	}

	got, err = yamledit.RemoveListItemWithOptions([]byte("allow_data_to:\n  - owner/repo-a\nother: 1\n"),
		[]string{"allow_data_to"}, "owner/repo-a", yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		t.Fatalf("RemoveListItemWithOptions: %v", err)
	}
	if want := "other: 1\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRemoveListItemTakesOneOfADuplicateValue pins the promise the doc makes:
// the first item equal to value goes, and only that one. A sequence that holds
// the value twice keeps the other item, and the edit is not a refusal.
func TestRemoveListItemTakesOneOfADuplicateValue(t *testing.T) {
	const doc = "k:\n  - a\n  - a\n  - b\n"
	got, err := yamledit.RemoveListItem([]byte(doc), []string{"k"}, "a")
	if err != nil {
		t.Fatalf("RemoveListItem: %v", err)
	}
	if want := "k:\n  - a\n  - b\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if items := listAt(t, got, "k"); len(items) != 2 || items[0] != "a" || items[1] != "b" {
		t.Fatalf("the list reads back %v, want [a b]", items)
	}
}

// TestRemoveSeqItemByKeyTakesOneOfADuplicateKey is the same promise for the
// keyed removal: two items carrying the same key value lose the first of them.
func TestRemoveSeqItemByKeyTakesOneOfADuplicateKey(t *testing.T) {
	const doc = "k:\n  - name: one\n    mode: a\n  - name: one\n    mode: b\n"
	got, err := yamledit.RemoveSeqItemByKey([]byte(doc), []string{"k"}, "name", "one")
	if err != nil {
		t.Fatalf("RemoveSeqItemByKey: %v", err)
	}
	if want := "k:\n  - name: one\n    mode: b\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUpsertSeqItemByKeyWritesANewItemAndEditsAnExistingOne(t *testing.T) {
	doc := []byte(readFixture(t, "areas.yaml"))

	got, err := yamledit.UpsertSeqItemByKey(doc, []string{"rigor", "areas"}, "path", "internal/three/**",
		map[string]string{"name": "three", "mode": "pro"})
	if err != nil {
		t.Fatalf("UpsertSeqItemByKey: %v", err)
	}
	want := `rigor:
  areas:
    - name: one
      path: internal/one/**
      mode: critical
    - name: two
      path: internal/two/**
      mode: standard
    - path: internal/three/**
      mode: pro
      name: three
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if item := itemAt(t, got, "internal/three/**"); item["name"] != "three" || item["mode"] != "pro" {
		t.Fatalf("the new item reads back as %v", item)
	}

	got, err = yamledit.UpsertSeqItemByKey(doc, []string{"rigor", "areas"}, "path", "internal/two/**",
		map[string]string{"mode": "pro", "owner": "person-1"})
	if err != nil {
		t.Fatalf("UpsertSeqItemByKey: %v", err)
	}
	want = `rigor:
  areas:
    - name: one
      path: internal/one/**
      mode: critical
    - name: two
      path: internal/two/**
      mode: pro
      owner: person-1
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	got, err = yamledit.UpsertSeqItemByKey([]byte("rigor:\n"), []string{"rigor", "areas"}, "path", "internal/one/**",
		map[string]string{"name": "one"})
	if err != nil {
		t.Fatalf("UpsertSeqItemByKey: %v", err)
	}
	if want := "rigor:\n  areas:\n    - path: internal/one/**\n      name: one\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUpsertSeqItemByKeyIsANoOpWhenNothingChanges(t *testing.T) {
	doc := []byte(readFixture(t, "areas.yaml"))
	got, err := yamledit.UpsertSeqItemByKey(doc, []string{"rigor", "areas"}, "path", "internal/one/**",
		map[string]string{"name": "one", "mode": "critical"})
	if err != nil {
		t.Fatalf("UpsertSeqItemByKey: %v", err)
	}
	if !bytes.Equal(got, doc) {
		t.Fatalf("a repeat of the same upsert rewrote the document:\n%s", got)
	}
}

// TestUpsertSeqItemByKeyRefusesToRewriteTheKeyField pins that an upsert cannot
// be asked to rename the item it looks up: the key field among the fields would
// leave the item the edit wrote unfindable by the key it was given.
func TestUpsertSeqItemByKeyRefusesToRewriteTheKeyField(t *testing.T) {
	const doc = "k:\n  - name: one\n    mode: a\n"
	got, err := yamledit.UpsertSeqItemByKey([]byte(doc), []string{"k"}, "name", "one",
		map[string]string{"mode": "b", "name": "two"})
	if r := yamledit.ReasonOf(err); r != yamledit.ReasonFieldIsKeyField {
		t.Fatalf("reason = %q, want %q (got %q, %v)", r, yamledit.ReasonFieldIsKeyField, got, err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatalf("a refusal returned changed bytes: %q", got)
	}
}

// TestUpsertSeqItemByKeyWithNoFieldsStillReadsTheDocument pins that an upsert
// with nothing to write is held to what every other edit is: the document is
// read and the path walked, so a caller that asked for nothing on a document
// this package cannot read, or on a path that is not a path of mappings down to
// a sequence, is told so — rather than handed its own bytes back as though the
// path were fine.
func TestUpsertSeqItemByKeyWithNoFieldsStillReadsTheDocument(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		path      []string
		keyField  string
		reason    yamledit.Reason
	}{
		{"no path at all", "a: 1\n", nil, "path", yamledit.ReasonEmptyPath},
		{"no key field", "a: 1\n", []string{"a"}, "", yamledit.ReasonEmptyField},
		{"a document that is not YAML", "a: [1\n", []string{"a"}, "path", yamledit.ReasonMalformed},
		{"a path that crosses a scalar", "a: 1\n", []string{"a", "b"}, "path", yamledit.ReasonNotAMapping},
		{"a leaf that is not a sequence", "a: 1\n", []string{"a"}, "path", yamledit.ReasonNotASequence},
		{"a flow sequence", "a: [1]\n", []string{"a"}, "path", yamledit.ReasonFlowCollection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.UpsertSeqItemByKey([]byte(tc.doc), tc.path, tc.keyField, "person-1", nil)
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}

	for _, doc := range []string{
		readFixture(t, "areas.yaml"),
		"# just a note\n",
		"",
	} {
		got, err := yamledit.UpsertSeqItemByKey([]byte(doc), []string{"rigor", "areas"}, "path", "internal/one/**", map[string]string{})
		if err != nil {
			t.Fatalf("UpsertSeqItemByKey with no fields: %v", err)
		}
		if !bytes.Equal(got, []byte(doc)) {
			t.Fatalf("an upsert with no fields rewrote the document: %q", got)
		}
	}
}

func TestRemoveSeqItemByKey(t *testing.T) {
	doc := []byte(readFixture(t, "areas.yaml"))
	got, err := yamledit.RemoveSeqItemByKey(doc, []string{"rigor", "areas"}, "path", "internal/one/**")
	if err != nil {
		t.Fatalf("RemoveSeqItemByKey: %v", err)
	}
	want := `rigor:
  areas:
    - name: two
      path: internal/two/**
      mode: standard
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	got, err = yamledit.RemoveSeqItemByKey(doc, []string{"rigor", "areas"}, "path", "internal/three/**")
	if err != nil {
		t.Fatalf("RemoveSeqItemByKey: %v", err)
	}
	if !bytes.Equal(got, doc) {
		t.Fatal("removing an item that is not there changed the document")
	}
}

// catalogDoc is the shape the image catalog is written in: a sequence of
// mappings keyed by name, one of them with its source in block style and the
// other in flow, which is what a hand-written config holds.
const catalogDoc = `# The images this project's jobs run.
image_catalog:
  entries:
    - name: server
      # Where its Dockerfile is.
      source:
        kind: dockerfile
        repo: acme/app
        path: images/server.Dockerfile
        branch: fugaro/2026-11-01-abc
    - name: worker
      source: { kind: dockerfile, repo: acme/sandbox, path: images/worker.Dockerfile, branch: release/1.0 }
    - name: plain
      source:
        kind: inline
        inline: FROM alpine:3.20
other: 1
`

// TestRemoveSeqItemFieldByKeyRemovesANestedField is the edit `fugaro update`
// makes when an entry pins a branch its repository no longer has: the pin goes,
// the entry keeps its place and its other fields, and every other byte of the
// file — the comment above the source, the comment block at the top, the flow
// spelling of the other entry — stays where it was.
func TestRemoveSeqItemFieldByKeyRemovesANestedField(t *testing.T) {
	got, err := yamledit.RemoveSeqItemFieldByKey([]byte(catalogDoc), []string{"image_catalog", "entries"},
		"name", "server", []string{"source", "branch"})
	if err != nil {
		t.Fatalf("RemoveSeqItemFieldByKey: %v", err)
	}
	want := `# The images this project's jobs run.
image_catalog:
  entries:
    - name: server
      # Where its Dockerfile is.
      source:
        kind: dockerfile
        repo: acme/app
        path: images/server.Dockerfile
    - name: worker
      source: { kind: dockerfile, repo: acme/sandbox, path: images/worker.Dockerfile, branch: release/1.0 }
    - name: plain
      source:
        kind: inline
        inline: FROM alpine:3.20
other: 1
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// The bytes say the field is gone; the reader agrees.
	entries := at(t, got, "image_catalog", "entries").([]any)
	src := entries[0].(map[string]any)["source"].(map[string]any)
	if _, ok := src["branch"]; ok {
		t.Fatalf("the entry still reads as pinned: %v", src)
	}

	// The same edit on the entry whose source is written in flow style: the
	// member goes with one of the commas around it, and the line keeps its shape.
	got, err = yamledit.RemoveSeqItemFieldByKey([]byte(catalogDoc), []string{"image_catalog", "entries"},
		"name", "worker", []string{"source", "branch"})
	if err != nil {
		t.Fatalf("RemoveSeqItemFieldByKey: %v", err)
	}
	if want := "      source: { kind: dockerfile, repo: acme/sandbox, path: images/worker.Dockerfile }\n"; !strings.Contains(string(got), want) {
		t.Fatalf("the flow member was not removed with its separator:\n%s\nwant %q in it", got, want)
	}

	// And on the entry that does not carry the field at all: nothing is written.
	got, err = yamledit.RemoveSeqItemFieldByKey([]byte(catalogDoc), []string{"image_catalog", "entries"},
		"name", "plain", []string{"source", "branch"})
	if err != nil {
		t.Fatalf("RemoveSeqItemFieldByKey: %v", err)
	}
	if !bytes.Equal(got, []byte(catalogDoc)) {
		t.Fatalf("removing a field that is not there changed the document:\n%s", got)
	}
}

// TestRemoveSeqItemFieldByKeyLeavesWhatItCannotName is the other half of the
// promise: an item, a field or a sequence this edit does not find is a no-op and
// not a refusal, so a caller that applies it to a whole catalog has nothing to
// treat as a failure.
func TestRemoveSeqItemFieldByKeyLeavesWhatItCannotName(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"no such item", catalogDoc},
		{"no such nested key", "k:\n  - name: one\n    source:\n      kind: inline\n"},
		{"a nested key that is not a mapping", "k:\n  - name: one\n    source: inline\n"},
		{"no such sequence", "k:\n  - name: one\n"},
		{"an empty document", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "one"
			if tc.name == "no such item" {
				key = "gone"
			}
			got, err := yamledit.RemoveSeqItemFieldByKey([]byte(tc.doc), []string{"image_catalog", "entries"},
				"name", key, []string{"source", "branch"})
			if tc.name == "no such sequence" {
				got, err = yamledit.RemoveSeqItemFieldByKey([]byte(tc.doc), []string{"k"}, "name", key, []string{"source", "branch"})
			}
			if err != nil {
				t.Fatalf("RemoveSeqItemFieldByKey: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("an edit that found nothing changed the document:\n%s", got)
			}
		})
	}
}

// TestRemoveSeqItemFieldByKeyTakesOneOfADuplicateField is the keyed removal's own
// promise, one level down: a field written twice is refused rather than guessed
// at, because which of the two the edit changed would be invisible in the result.
func TestRemoveSeqItemFieldByKeyTakesOneOfADuplicateField(t *testing.T) {
	const doc = "k:\n  - name: one\n    source:\n      branch: a\n      branch: b\n"
	got, err := yamledit.RemoveSeqItemFieldByKey([]byte(doc), []string{"k"}, "name", "one", []string{"source", "branch"})
	if r := yamledit.ReasonOf(err); r != yamledit.ReasonDuplicateKey {
		t.Fatalf("reason = %q, want %q (got %q, %v)", r, yamledit.ReasonDuplicateKey, got, err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatalf("a refusal returned changed bytes: %q", got)
	}
}

// TestRemoveSeqItemFieldByKeyRefusesWhatItCannotSplice pins the refusals, and
// that each of them is the class of the reason rather than a half-applied edit:
// a caller that meets one has a file it must leave alone.
func TestRemoveSeqItemFieldByKeyRefusesWhatItCannotSplice(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		path      []string
		keyField  string
		field     []string
		reason    yamledit.Reason
	}{
		{"no path at all", "a: 1\n", nil, "name", []string{"source", "branch"}, yamledit.ReasonEmptyPath},
		{"no field at all", "a: 1\n", []string{"a"}, "name", nil, yamledit.ReasonEmptyPath},
		{"no key field", "a: 1\n", []string{"a"}, "", []string{"source", "branch"}, yamledit.ReasonEmptyField},
		{"a document that is not YAML", "a: [1\n", []string{"a"}, "name", []string{"source", "branch"}, yamledit.ReasonMalformed},
		{"a path that crosses a scalar", "a: 1\n", []string{"a", "b"}, "name", []string{"source", "branch"}, yamledit.ReasonNotAMapping},
		{"a leaf that is not a sequence", "a: 1\n", []string{"a"}, "name", []string{"source", "branch"}, yamledit.ReasonNotASequence},
		{"a flow sequence", "a: [1]\n", []string{"a"}, "name", []string{"source", "branch"}, yamledit.ReasonFlowCollection},
		{"a merge key on the field's mapping", "base: &base { x: 1 }\nk:\n  - name: one\n    source:\n      <<: *base\n      branch: a\n",
			[]string{"k"}, "name", []string{"source", "branch"}, yamledit.ReasonMergeKey},
		{"a tag on the field", "k:\n  - name: one\n    source:\n      branch: !!str a\n",
			[]string{"k"}, "name", []string{"source", "branch"}, yamledit.ReasonAnchorOrAlias},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.RemoveSeqItemFieldByKey([]byte(tc.doc), tc.path, tc.keyField, "one", tc.field)
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

// TestRemoveSeqItemFieldByKeyKeepsTheLineEndings: the field goes, and the file's
// own line ending is what the rest of it keeps.
func TestRemoveSeqItemFieldByKeyKeepsTheLineEndings(t *testing.T) {
	lf := "k:\n  - name: one\n    source:\n      repo: acme/app\n      branch: gone\nother: 1\n"
	crlf := []byte(strings.ReplaceAll(lf, "\n", "\r\n"))
	got, err := yamledit.RemoveSeqItemFieldByKey(crlf, []string{"k"}, "name", "one", []string{"source", "branch"})
	if err != nil {
		t.Fatalf("RemoveSeqItemFieldByKey: %v", err)
	}
	assertCRLF(t, got)
	if want := "k:\r\n  - name: one\r\n    source:\r\n      repo: acme/app\r\nother: 1\r\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLineEndingsArePreserved(t *testing.T) {
	lf := "project: workspace-a\nallow_data_to:\n  - owner/repo-a\n# note\nbudget:\n  max_runs: 5\n"
	crlf := []byte(strings.ReplaceAll(lf, "\n", "\r\n"))

	got, err := yamledit.SetScalar(crlf, []string{"budget", "max_runs"}, "9")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if want := strings.ReplaceAll(strings.Replace(lf, "max_runs: 5", "max_runs: 9", 1), "\n", "\r\n"); string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	assertCRLF(t, got)

	got, err = yamledit.AddListItem(crlf, []string{"allow_data_to"}, "owner/repo-b")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	assertCRLF(t, got)
	if !strings.Contains(string(got), "  - owner/repo-b\r\n") {
		t.Fatalf("the new line does not use the document's own line ending:\n%q", got)
	}

	got, err = yamledit.RemoveKey(crlf, []string{"budget"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	assertCRLF(t, got)
	if strings.Contains(string(got), "budget") {
		t.Fatalf("budget is still there:\n%q", got)
	}
}

func TestAKeyThatAppearsOnlyInACommentIsNotMatched(t *testing.T) {
	const doc = "# budget:\n#   max_runs: 20\nproject: workspace-a\n"
	got, err := yamledit.RemoveKey([]byte(doc), []string{"budget", "max_runs"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatalf("a commented-out key was rewritten:\n%q", got)
	}
	got, err = yamledit.SetScalar([]byte(doc), []string{"budget", "max_runs"}, "9")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if want := "# budget:\n#   max_runs: 20\nproject: workspace-a\nbudget:\n  max_runs: 9\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAnchorsElsewhereAreUntouched(t *testing.T) {
	doc := []byte(readFixture(t, "anchor-and-comment.yaml"))
	got, err := yamledit.SetScalar(doc, []string{"prod", "replicas"}, "2")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if want := strings.Replace(string(doc), "replicas: 1", "replicas: 2", 1); string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, kept := range []string{"base: &base", "<<: *base", "# Two environments share a base block."} {
		if !strings.Contains(string(got), kept) {
			t.Fatalf("%q went missing:\n%s", kept, got)
		}
	}
	if _, err := yamledit.RemoveKey(doc, []string{"base"}); yamledit.ReasonOf(err) != yamledit.ReasonAnchorOrAlias {
		t.Fatalf("removing the anchored key: %v, want a refusal", err)
	}
}

func TestEditsBesideAMergeKey(t *testing.T) {
	const doc = "base: &base\n  x: 1\nprod:\n  <<: *base\n  replicas: 1\n"
	got, err := yamledit.SetScalar([]byte(doc), []string{"prod", "replicas"}, "2")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if want := strings.Replace(doc, "replicas: 1", "replicas: 2", 1); string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := yamledit.SetScalar([]byte(doc), []string{"prod", "image"}, "image-b"); yamledit.ReasonOf(err) != yamledit.ReasonMergeKey {
		t.Fatalf("creating a key under a merge key: %v, want a refusal", err)
	}
}

func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    string
		op     func([]byte) ([]byte, error)
		reason yamledit.Reason
	}{
		{
			"two documents",
			"a: 1\n---\nb: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "2") },
			yamledit.ReasonMultiDocument,
		},
		{
			"a tab used for indentation",
			"a:\n\tb: 1\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
			yamledit.ReasonTabIndentation,
		},
		{
			"a merge key where a key would be created",
			"base: &base\n  x: 1\nprod:\n  <<: *base\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"prod", "replicas"}, "2") },
			yamledit.ReasonMergeKey,
		},
		{
			"a merge key where a key would be removed",
			"base: &base\n  x: 1\nprod:\n  <<: *base\n  x: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"prod", "x"}) },
			yamledit.ReasonMergeKey,
		},
		{
			"a key written twice on the path",
			"a: 1\na: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "3") },
			yamledit.ReasonDuplicateKey,
		},
		{
			"a key written twice deeper on the path",
			"a:\n  b: 1\n  b: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"a", "b"}) },
			yamledit.ReasonDuplicateKey,
		},
		{
			"a removal across a scalar",
			"a: 1\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"a", "b"}) },
			yamledit.ReasonNotAMapping,
		},
		{
			"an alias where the value would be replaced",
			"a: &x 1\nb: *x\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"b"}, "2") },
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"an anchor on the value to replace",
			"a: &x 1\nb: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "2") },
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"a tag on the value to replace",
			"a: !!str 1\nb: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "2") },
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"a tag on a key written with no value",
			"a: !!str\nb: 2\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "2") },
			yamledit.ReasonAnchorOrAlias,
		},
		{
			"a path into a scalar",
			"a: 1\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
			yamledit.ReasonNotAMapping,
		},
		{
			"a path into a sequence",
			"a:\n  - x\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
			yamledit.ReasonNotAMapping,
		},
		{
			"a flow mapping written across lines, to splice into",
			"a: {b: 1,\n  c: 2}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
			yamledit.ReasonFlowCollection,
		},
		{
			"a flow mapping to replace",
			"a: {b: 1}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "2") },
			yamledit.ReasonFlowCollection,
		},
		{
			"a flow sequence to append to",
			"a: [x]\n",
			func(d []byte) ([]byte, error) { return yamledit.AddListItem(d, []string{"a"}, "y") },
			yamledit.ReasonFlowCollection,
		},
		{
			"a block scalar to rewrite",
			"a: |\n  line\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "line") },
			yamledit.ReasonBlockScalar,
		},
		{
			"a plain scalar written across lines",
			"a: f\n  g\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "x") },
			yamledit.ReasonMultilineScalar,
		},
		{
			"a quoted scalar written across lines",
			"a: \"f\n  g\"\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "x") },
			yamledit.ReasonMultilineScalar,
		},
		{
			"a document yaml.v3 cannot parse",
			"a: [1\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a"}, "1") },
			yamledit.ReasonMalformed,
		},
		{
			"an empty path",
			"a: 1\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, nil, "1") },
			yamledit.ReasonEmptyPath,
		},
		{
			"an empty key field",
			"a: 1\n",
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemByKey(d, []string{"a"}, "", "v", map[string]string{"x": "1"})
			},
			yamledit.ReasonEmptyField,
		},
		{
			"a mapping where a list-valued field would be written",
			"pools:\n  - name: claude\n    # what the pool walks\n    members:\n      oauth-one: 1\n",
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemListByKey(d, []string{"pools"}, "name", "claude", "members", []string{"oauth-two"})
			},
			yamledit.ReasonReplacesCollection,
		},
		{
			"a flow mapping where a mapping-valued field would be written",
			"pools:\n  - name: paid\n    principal: {kind: person, id: subject-1}\n",
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemMapByKey(d, []string{"pools"}, "name", "paid", "principal",
					map[string]string{"kind": "project", "id": "workspace-a"})
			},
			yamledit.ReasonFlowCollection,
		},
		{
			"a list where a mapping-valued field would be written",
			"pools:\n  - name: paid\n    principal:\n      - person\n",
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemMapByKey(d, []string{"pools"}, "name", "paid", "principal",
					map[string]string{"kind": "project", "id": "workspace-a"})
			},
			yamledit.ReasonReplacesCollection,
		},
		{
			"a mapping-valued field with no keys",
			"pools:\n  - name: paid\n",
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemMapByKey(d, []string{"pools"}, "name", "paid", "principal", nil)
			},
			yamledit.ReasonEmptyList,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.op([]byte(tc.doc))
			if err == nil {
				t.Fatalf("no refusal; got %q", got)
			}
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (%v)", r, tc.reason, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
			if strings.Contains(err.Error(), tc.doc) {
				t.Fatalf("the refusal echoes document bytes: %v", err)
			}
		})
	}
}

func TestARefusalNamesThePathButNeverTheDocumentsBytes(t *testing.T) {
	const doc = "kept: value-a\nother: value-b\n"
	_, err := yamledit.SetScalar([]byte(doc), []string{"kept", "deeper"}, "x")
	if err == nil {
		t.Fatal("no refusal")
	}
	msg := err.Error()
	for _, text := range []string{"value-a", "value-b", "kept: value-a"} {
		if strings.Contains(msg, text) {
			t.Fatalf("the refusal echoes %q: %v", text, err)
		}
	}
	if !strings.Contains(msg, "kept.deeper") {
		t.Fatalf("the refusal does not name the path it was asked about: %v", err)
	}
}

func TestEveryResultReparsesToTheValueItShouldCarry(t *testing.T) {
	set, err := yamledit.SetScalar([]byte("a: 1\nb: pro\n"), []string{"b"}, "true")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if v := at(t, set, "b"); v != true {
		t.Fatalf("b reads back as %#v, want true", v)
	}
	if v := at(t, set, "a"); v != 1 {
		t.Fatalf("a reads back as %#v, want the integer 1", v)
	}

	added, err := yamledit.AddListItem([]byte("a:\n  - x\n"), []string{"a"}, "yes")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if items := listAt(t, added, "a"); len(items) != 2 || items[1] != "yes" {
		t.Fatalf("the list reads back %v", items)
	}

	upserted, err := yamledit.UpsertSeqItemByKey([]byte(readFixture(t, "areas.yaml")),
		[]string{"rigor", "areas"}, "path", "internal/one/**", map[string]string{"mode": "pro"})
	if err != nil {
		t.Fatalf("UpsertSeqItemByKey: %v", err)
	}
	if item := itemAt(t, upserted, "internal/one/**"); item["mode"] != "pro" || item["name"] != "one" {
		t.Fatalf("the item reads back %v", item)
	}

	removed, err := yamledit.RemoveSeqItemByKey([]byte(readFixture(t, "areas.yaml")),
		[]string{"rigor", "areas"}, "path", "internal/two/**")
	if err != nil {
		t.Fatalf("RemoveSeqItemByKey: %v", err)
	}
	if item := itemAt(t, removed, "internal/two/**"); item != nil {
		t.Fatalf("the item is still there: %v", item)
	}
}

// TestEveryOperationKeepsTheCommentsAroundIt runs the operations that add to or
// take from a sequence of a file written by hand, and holds the result to the
// same promise the scalar edits make: every comment the file had is still in it,
// in the order it was written.
func TestEveryOperationKeepsTheCommentsAroundIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		path []string
		op   func([]byte) ([]byte, error)
	}{
		{
			"AddListItem", []string{"allow_data_to"},
			func(d []byte) ([]byte, error) {
				return yamledit.AddListItem(d, []string{"allow_data_to"}, "owner/repo-c")
			},
		},
		{
			"RemoveListItem", []string{"followup", "trusted"},
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveListItem(d, []string{"followup", "trusted"}, "person-1")
			},
		},
		{
			"UpsertSeqItemByKey", []string{"rigor", "areas"},
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemByKey(d, []string{"rigor", "areas"}, "path", "internal/three/**",
					map[string]string{"name": "three", "mode": "pro"})
			},
		},
		{
			"RemoveSeqItemByKey", []string{"rigor", "areas"},
			func(d []byte) ([]byte, error) {
				return yamledit.RemoveSeqItemByKey(d, []string{"rigor", "areas"}, "path", "internal/one/**")
			},
		},
		{
			"UpsertSeqItemListByKey", []string{"rigor", "areas"},
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemListByKey(d, []string{"rigor", "areas"}, "name", "two", "allow_data_to",
					[]string{"owner/repo-a", "owner/repo-c"})
			},
		},
		{
			"UpsertSeqItemMapByKey", []string{"rigor", "areas"},
			func(d []byte) ([]byte, error) {
				return yamledit.UpsertSeqItemMapByKey(d, []string{"rigor", "areas"}, "name", "two", "principal",
					map[string]string{"kind": "service-account", "id": "run@proj.iam.gserviceaccount.com"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := readFixture(t, "commented-project.yaml")
			got, err := tc.op([]byte(doc))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !yamledit.Changed([]byte(doc), got) {
				t.Fatalf("%s changed nothing", tc.name)
			}
			if !isSubsequence(commentLines(doc), commentLines(string(got))) {
				t.Fatalf("a comment went missing:\n%s\n%s", doc, got)
			}
			if !sameOffThePath(decodeMap(doc), decodeMap(string(got)), tc.path) {
				t.Fatalf("%s reached past its path:\n%s", tc.name, got)
			}
			reparses(t, got)
		})
	}
}

// TestUpsertSeqItemListByKeyKeepsTheCommentsAroundTheField is the promise above
// held to the shape a pool is written in: replacing a list-valued field replaces
// its items and nothing else, so the comment above the field and the comment
// trailing its last item are still in the file afterwards.
func TestUpsertSeqItemListByKeyKeepsTheCommentsAroundTheField(t *testing.T) {
	const doc = "pools:\n" +
		"  - name: claude\n" +
		"    # oauth first, the key as the fallback\n" +
		"    members:   # walked in this order\n" +
		"      - oauth-one\n" +
		"      - key-one  # the paid one\n"
	got, err := yamledit.UpsertSeqItemListByKey([]byte(doc), []string{"pools"}, "name", "claude", "members",
		[]string{"oauth-two", "key-one"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey: %v", err)
	}
	const want = "pools:\n" +
		"  - name: claude\n" +
		"    # oauth first, the key as the fallback\n" +
		"    members:   # walked in this order\n" +
		"      - oauth-two\n" +
		"      - key-one  # the paid one\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)

	// A field written with its key alone has no items to replace: the list goes
	// in under the key the document already has, and the comment beside that key
	// stays where it was.
	const bare = "pools:\n" +
		"  - name: claude\n" +
		"    members:   # walked in this order\n"
	got, err = yamledit.UpsertSeqItemListByKey([]byte(bare), []string{"pools"}, "name", "claude", "members",
		[]string{"oauth-one"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey (a bare key): %v", err)
	}
	const bareWant = "pools:\n" +
		"  - name: claude\n" +
		"    members:   # walked in this order\n" +
		"      - oauth-one\n"
	if string(got) != bareWant {
		t.Fatalf("got %q, want %q", got, bareWant)
	}
	reparses(t, got)

	// The items are replaced and nothing after them is: a field written under
	// the same item, below the list, keeps its own line and its comment, which
	// is where a splice running one line too far would have eaten it.
	const below = "pools:\n" +
		"  - name: claude\n" +
		"    members:\n" +
		"      - oauth-one\n" +
		"      - key-one\n" +
		"    strategy: failover   # keep this comment\n"
	got, err = yamledit.UpsertSeqItemListByKey([]byte(below), []string{"pools"}, "name", "claude", "members",
		[]string{"oauth-two"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey (a field below the list): %v", err)
	}
	const belowWant = "pools:\n" +
		"  - name: claude\n" +
		"    members:\n" +
		"      - oauth-two\n" +
		"    strategy: failover   # keep this comment\n"
	if string(got) != belowWant {
		t.Fatalf("got %q, want %q", got, belowWant)
	}
	reparses(t, got)
}

// TestUpsertSeqItemMapByKeyWritesAndReplacesAMapping holds the mapping writer to
// the list writer's promise on the shape a pool's principal is written in: the
// item keeps its place and its other fields, the field keeps the comment above
// its key and the comment beside it, and only the mapping's own lines change.
func TestUpsertSeqItemMapByKeyWritesAndReplacesAMapping(t *testing.T) {
	principal := map[string]string{"kind": "service-account", "id": "run@proj.iam.gserviceaccount.com"}

	// The item does not carry the field yet: it is appended under the item, with
	// the fields the item already has untouched above it.
	const absent = "pools:\n" +
		"  - name: paid\n" +
		"    members:\n" +
		"      - oauth-one\n"
	got, err := yamledit.UpsertSeqItemMapByKey([]byte(absent), []string{"pools"}, "name", "paid", "principal", principal)
	if err != nil {
		t.Fatalf("UpsertSeqItemMapByKey: %v", err)
	}
	const absentWant = "pools:\n" +
		"  - name: paid\n" +
		"    members:\n" +
		"      - oauth-one\n" +
		"    principal:\n" +
		"      id: run@proj.iam.gserviceaccount.com\n" +
		"      kind: service-account\n"
	if string(got) != absentWant {
		t.Fatalf("got %q, want %q", got, absentWant)
	}
	reparses(t, got)

	// The field is there: its entries are replaced and no more. The comment above
	// its key, the comment beside it and the field written below it all stay, and
	// so does the text trailing the mapping's last line — a comment above all,
	// which lands beside the new last entry exactly as the list writer leaves a
	// trailing comment beside its new last item.
	const present = "pools:\n" +
		"  - name: paid\n" +
		"    # who this pool belongs to\n" +
		"    principal:   # verified by the service\n" +
		"      kind: person\n" +
		"      id: subject-1  # a person\n" +
		"    rule: ordered\n"
	got, err = yamledit.UpsertSeqItemMapByKey([]byte(present), []string{"pools"}, "name", "paid", "principal", principal)
	if err != nil {
		t.Fatalf("UpsertSeqItemMapByKey (a field already there): %v", err)
	}
	const presentWant = "pools:\n" +
		"  - name: paid\n" +
		"    # who this pool belongs to\n" +
		"    principal:   # verified by the service\n" +
		"      id: run@proj.iam.gserviceaccount.com\n" +
		"      kind: service-account  # a person\n" +
		"    rule: ordered\n"
	if string(got) != presentWant {
		t.Fatalf("got %q, want %q", got, presentWant)
	}
	reparses(t, got)

	// A bare key: the mapping goes in under the key the document already has, and
	// the comment beside that key stays where it was.
	const bare = "pools:\n" +
		"  - name: paid\n" +
		"    principal:   # verified by the service\n"
	got, err = yamledit.UpsertSeqItemMapByKey([]byte(bare), []string{"pools"}, "name", "paid", "principal",
		map[string]string{"kind": "project", "id": "workspace-a"})
	if err != nil {
		t.Fatalf("UpsertSeqItemMapByKey (a bare key): %v", err)
	}
	const bareWant = "pools:\n" +
		"  - name: paid\n" +
		"    principal:   # verified by the service\n" +
		"      id: workspace-a\n" +
		"      kind: project\n"
	if string(got) != bareWant {
		t.Fatalf("got %q, want %q", got, bareWant)
	}
	reparses(t, got)

	// The item is not there yet: the whole item is written, key field first.
	const noItem = "pools:\n  - name: team-a\n"
	got, err = yamledit.UpsertSeqItemMapByKey([]byte(noItem), []string{"pools"}, "name", "paid", "principal",
		map[string]string{"kind": "project", "id": "workspace-a"})
	if err != nil {
		t.Fatalf("UpsertSeqItemMapByKey (an item that is not there): %v", err)
	}
	const noItemWant = "pools:\n" +
		"  - name: team-a\n" +
		"  - name: paid\n" +
		"    principal:\n" +
		"      id: workspace-a\n" +
		"      kind: project\n"
	if string(got) != noItemWant {
		t.Fatalf("got %q, want %q", got, noItemWant)
	}
	reparses(t, got)
}

// TestAFlowCollectionElsewhereIsUntouched: a flow collection this package will
// not splice into is still a flow collection it leaves alone.
func TestAFlowCollectionElsewhereIsUntouched(t *testing.T) {
	const doc = "# a flow mapping this package does not touch\na: {b: 1}\nc: 2\n"
	got, err := yamledit.SetScalar([]byte(doc), []string{"c"}, "3")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if want := strings.Replace(doc, "c: 2", "c: 3", 1); string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// decode parses a document into plain Go values, the way any reader of the file
// does, so that a test holds the edited bytes to the value they should carry and
// not to this package's own idea of it.
func decode(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(doc, &m); err != nil {
		t.Fatalf("the result does not parse: %v\n%s", err, doc)
	}
	return m
}

func at(t *testing.T, doc []byte, path ...string) any {
	t.Helper()
	var cur any = decode(t, doc)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[key]
		if !ok {
			return nil
		}
	}
	return cur
}

func has(t *testing.T, doc []byte, path ...string) bool {
	t.Helper()
	var cur any = decode(t, doc)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[key]
		if !ok {
			return false
		}
	}
	return true
}

func listAt(t *testing.T, doc []byte, path ...string) []string {
	t.Helper()
	v, ok := at(t, doc, path...).([]any)
	if !ok {
		t.Fatalf("%v is not a list", path)
	}
	out := make([]string, 0, len(v))
	for _, item := range v {
		out = append(out, item.(string))
	}
	return out
}

func itemAt(t *testing.T, doc []byte, keyValue string) map[string]any {
	t.Helper()
	for _, item := range at(t, doc, "rigor", "areas").([]any) {
		if m := item.(map[string]any); m["path"] == keyValue {
			return m
		}
	}
	return nil
}

func reparses(t *testing.T, doc []byte) {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal(doc, &n); err != nil {
		t.Fatalf("the result does not parse: %v\n%s", err, doc)
	}
}

func assertCRLF(t *testing.T, doc []byte) {
	t.Helper()
	for i, b := range doc {
		if b == '\n' && (i == 0 || doc[i-1] != '\r') {
			t.Fatalf("a bare LF appeared in a CRLF document at %d:\n%q", i, doc)
		}
	}
}

// changedLine returns the 1-based line whose bytes differ, or 0 when the two
// documents differ in length or on more than one line.
func changedLine(t *testing.T, before, after string) int {
	t.Helper()
	b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(a) != len(b) {
		return 0
	}
	line := 0
	for i := range b {
		if b[i] == a[i] {
			continue
		}
		if line != 0 {
			return 0
		}
		line = i + 1
	}
	return line
}

// TestUpsertSeqItemListByKeyWritesAndReplacesTheList holds the one operation
// whose field is a list of its own — a pool's members — to the three things a
// caller needs it to do: write an item the sequence does not have, write the
// list beside the fields an item already has, and replace the list of an item
// that already carries one without touching its other fields.
func TestUpsertSeqItemListByKeyWritesAndReplacesTheList(t *testing.T) {
	const doc = `# A project's pools.
pools:
  - name: acme
    strategy: failover
    members:
      - acme-one
      - acme-two
`
	got, err := yamledit.UpsertSeqItemListByKey([]byte(doc), []string{"pools"}, "name", "beta",
		"members", []string{"beta-one", "beta-two"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey: %v", err)
	}
	want := doc + `  - name: beta
    members:
      - beta-one
      - beta-two
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	got, err = yamledit.UpsertSeqItemListByKey([]byte(doc), []string{"pools"}, "name", "acme",
		"members", []string{"acme-three"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey: %v", err)
	}
	want = `# A project's pools.
pools:
  - name: acme
    strategy: failover
    members:
      - acme-three
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// The key the sequence hangs from is written when the document has none,
	// the way every other edit here writes the mappings on its way.
	got, err = yamledit.UpsertSeqItemListByKey([]byte("other: 1\n"), []string{"pools"}, "name", "acme",
		"members", []string{"acme-one"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey: %v", err)
	}
	if want := "other: 1\npools:\n  - name: acme\n    members:\n      - acme-one\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestUpsertSeqItemListByKeyIsANoOpWhenNothingChanges pins the other half of
// every edit here: the same list written twice leaves the document byte for
// byte as it was.
func TestUpsertSeqItemListByKeyIsANoOpWhenNothingChanges(t *testing.T) {
	const doc = "pools:\n  - name: acme\n    members:\n      - acme-one\n"
	got, err := yamledit.UpsertSeqItemListByKey([]byte(doc), []string{"pools"}, "name", "acme",
		"members", []string{"acme-one"})
	if err != nil {
		t.Fatalf("UpsertSeqItemListByKey: %v", err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatalf("a repeat of the same upsert rewrote the document:\n%s", got)
	}
}

// TestUpsertSeqItemListByKeyRefusesWhatItCannotWriteSafely pins the refusals:
// the shapes this operation has no bytes for (a flow collection it would have
// to splice a block into, a sequence item that is not a mapping), and the
// arguments that name nothing.
func TestUpsertSeqItemListByKeyRefusesWhatItCannotWriteSafely(t *testing.T) {
	for _, tc := range []struct {
		name, doc, keyField, keyValue, field string
		values                               []string
		path                                 []string
		reason                               yamledit.Reason
	}{
		{"no path", "a: 1\n", "name", "x", "members", []string{"m"}, nil, yamledit.ReasonEmptyPath},
		{"no key field", "a: 1\n", "", "x", "members", []string{"m"}, []string{"pools"}, yamledit.ReasonEmptyField},
		{"no field", "a: 1\n", "name", "x", "", []string{"m"}, []string{"pools"}, yamledit.ReasonEmptyField},
		{"the field is the key field", "a: 1\n", "name", "x", "name", []string{"m"}, []string{"pools"}, yamledit.ReasonFieldIsKeyField},
		{"no values", "a: 1\n", "name", "x", "members", nil, []string{"pools"}, yamledit.ReasonEmptyList},
		{"a flow item", "pools:\n  - {name: acme, strategy: failover}\n", "name", "acme", "members", []string{"m"}, []string{"pools"}, yamledit.ReasonFlowCollection},
		{"a flow list", "pools:\n  - name: acme\n    members: [one]\n", "name", "acme", "members", []string{"m"}, []string{"pools"}, yamledit.ReasonFlowCollection},
		{"a scalar item", "pools:\n  - acme\n", "name", "acme", "members", []string{"m"}, []string{"pools"}, yamledit.ReasonNotAMapping},
		{"a path that crosses a scalar", "a: 1\n", "name", "x", "members", []string{"m"}, []string{"a", "pools"}, yamledit.ReasonNotAMapping},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.UpsertSeqItemListByKey([]byte(tc.doc), tc.path, tc.keyField, tc.keyValue, tc.field, tc.values)
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

// TestUpsertSeqItemItemsByKeyWritesASequenceOfMappings is the operation M2 owes
// the migration contract (docs/repo-override-rulings/contract-migrate.md): a
// field whose value is a sequence of MAPPINGS — a project area's `members`, each
// `{repo, paths}` — which neither the scalar upsert nor the list upsert reaches.
// The item is written with its nested items where the sequence has no such item,
// the nested sequence is replaced where the item already carries one, and every
// other line of the file stays where it was.
func TestUpsertSeqItemItemsByKeyWritesASequenceOfMappings(t *testing.T) {
	const doc = `# The project's areas.
defaults:
  rigor:
    areas:
      - name: payments
        level: critical
        members:
          - repo: acme/pay
            paths:
              - internal/pay
`
	items := []yamledit.SeqItem{
		{KeyField: "repo", Key: "acme/api", Lists: map[string][]string{"paths": {"internal/api", "cmd"}}},
	}
	got, err := yamledit.UpsertSeqItemItemsByKey([]byte(doc), []string{"defaults", "rigor", "areas"}, "name", "payments", "members", items)
	if err != nil {
		t.Fatalf("UpsertSeqItemItemsByKey: %v", err)
	}
	const want = `# The project's areas.
defaults:
  rigor:
    areas:
      - name: payments
        level: critical
        members:
          - repo: acme/api
            paths:
              - internal/api
              - cmd
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	reparses(t, got)

	// A second application writes the same bytes: the nested item is written
	// where it stands, never doubled, which is what a second `fugaro update`
	// finds.
	again, err := yamledit.UpsertSeqItemItemsByKey(got, []string{"defaults", "rigor", "areas"}, "name", "payments", "members", items)
	if err != nil {
		t.Fatalf("UpsertSeqItemItemsByKey (again): %v", err)
	}
	if !bytes.Equal(again, got) {
		t.Fatalf("the same upsert twice rewrote the document:\n%s", again)
	}

	// The area the sequence does not have yet is appended whole, with its own
	// scalars and its nested sequence, and the key the sequence hangs from is
	// written when the document has none of it.
	appended, err := yamledit.UpsertSeqItemItemsByKey(got, []string{"defaults", "rigor", "areas"}, "name", "billing",
		"members", []yamledit.SeqItem{{KeyField: "repo", Key: "acme/bill"}})
	if err != nil {
		t.Fatalf("UpsertSeqItemItemsByKey (a new area): %v", err)
	}
	if !strings.Contains(string(appended), "      - name: billing\n        members:\n          - repo: acme/bill\n") {
		t.Fatalf("the new area was not written whole:\n%s", appended)
	}
	reparses(t, appended)

	fresh, err := yamledit.UpsertSeqItemItemsByKey([]byte("other: 1\n"), []string{"defaults", "rigor", "areas"},
		"name", "payments", "members", items)
	if err != nil {
		t.Fatalf("UpsertSeqItemItemsByKey (a document with no areas): %v", err)
	}
	const freshWant = `other: 1
defaults:
  rigor:
    areas:
      - name: payments
        members:
          - repo: acme/api
            paths:
              - internal/api
              - cmd
`
	if string(fresh) != freshWant {
		t.Fatalf("got:\n%s\nwant:\n%s", fresh, freshWant)
	}
	reparses(t, fresh)
}

// TestUpsertSeqItemItemsByKeyRefusesWhatItCannotWriteSafely pins the refusals of
// the nested upsert: a field written in flow style, a sequence item that is not
// a mapping, and the arguments that name nothing — a nested list asked for with
// no items above all, which has no bytes an edit could write (an empty block
// sequence is a null, not a sequence of nothing).
func TestUpsertSeqItemItemsByKeyRefusesWhatItCannotWriteSafely(t *testing.T) {
	one := []yamledit.SeqItem{{KeyField: "repo", Key: "acme/api"}}
	for _, tc := range []struct {
		name, doc, keyField, keyValue, field string
		items                                []yamledit.SeqItem
		path                                 []string
		reason                               yamledit.Reason
	}{
		{"no path", "a: 1\n", "name", "x", "members", one, nil, yamledit.ReasonEmptyPath},
		{"no key field", "a: 1\n", "", "x", "members", one, []string{"areas"}, yamledit.ReasonEmptyField},
		{"no field", "a: 1\n", "name", "x", "", one, []string{"areas"}, yamledit.ReasonEmptyField},
		{"the field is the key field", "a: 1\n", "name", "x", "name", one, []string{"areas"}, yamledit.ReasonFieldIsKeyField},
		{"no items", "a: 1\n", "name", "x", "members", nil, []string{"areas"}, yamledit.ReasonEmptyList},
		{"an item with no key field", "a: 1\n", "name", "x", "members",
			[]yamledit.SeqItem{{Key: "acme/api"}}, []string{"areas"}, yamledit.ReasonEmptyField},
		{"a nested list with no items", "a: 1\n", "name", "x", "members",
			[]yamledit.SeqItem{{KeyField: "repo", Key: "acme/api", Lists: map[string][]string{"paths": {}}}},
			[]string{"areas"}, yamledit.ReasonEmptyList},
		{"a flow field", "areas:\n  - name: payments\n    members: [{repo: acme/api}]\n", "name", "payments", "members",
			one, []string{"areas"}, yamledit.ReasonFlowCollection},
		{"a scalar item", "areas:\n  - payments\n", "name", "payments", "members", one, []string{"areas"}, yamledit.ReasonNotAMapping},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.UpsertSeqItemItemsByKey([]byte(tc.doc), tc.path, tc.keyField, tc.keyValue, tc.field, tc.items)
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

// TestSetEmptyMappingWritesTheEmptyMappingAndKeepsTheComments is the operation
// the review floor's last removal needs: `review_floor: {}` is YAML's only
// spelling of a floor of nothing — block style has no empty form — and what the
// person wrote around the key stays where they wrote it, the comment block above
// it and the key's own place in the file included.
func TestSetEmptyMappingWritesTheEmptyMappingAndKeepsTheComments(t *testing.T) {
	for _, tc := range []struct {
		name, doc, want string
		path            []string
	}{
		{
			"a block mapping",
			"# the owner's floor\nreview_floor:\n  pro:\n    - l2\nbase_branch: main\n",
			"# the owner's floor\nreview_floor: {}\nbase_branch: main\n",
			[]string{"review_floor"},
		},
		{
			"a block mapping that is the file's last entry",
			"base_branch: main\nreview_floor:\n  pro:\n    - l2\n",
			"base_branch: main\nreview_floor: {}\n",
			[]string{"review_floor"},
		},
		{
			"a key the document does not carry",
			"base_branch: main\n",
			"base_branch: main\nreview_floor: {}\n",
			[]string{"review_floor"},
		},
		{
			"a nested key the document does not carry",
			"a: 1\n",
			"a: 1\nreview_floor:\n  pro: {}\n",
			[]string{"review_floor", "pro"},
		},
		{
			"a value that is the empty mapping already",
			"review_floor: {}\nbase_branch: main\n",
			"review_floor: {}\nbase_branch: main\n",
			[]string{"review_floor"},
		},
		{
			"a quoted key",
			"'review_floor':\n  pro:\n    - l2\n",
			"'review_floor': {}\n",
			[]string{"review_floor"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetEmptyMapping([]byte(tc.doc), tc.path)
			if err != nil {
				t.Fatalf("SetEmptyMapping: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			reparses(t, got)
			if !has(t, got, tc.path...) {
				t.Fatalf("%v is not in %q", tc.path, got)
			}
		})
	}
}

// TestSetEmptyMappingIsANoOpWhenTheEmptyMappingIsAlreadyThere pins the pair of
// facts a caller branches on: the bytes come back identical, and Changed says
// so, so a write path can tell "nothing to do" from "wrote the same bytes".
func TestSetEmptyMappingIsANoOpWhenTheEmptyMappingIsAlreadyThere(t *testing.T) {
	const doc = "review_floor: {}\n"
	got, err := yamledit.SetEmptyMapping([]byte(doc), []string{"review_floor"})
	if err != nil {
		t.Fatalf("SetEmptyMapping: %v", err)
	}
	if !bytes.Equal(got, []byte(doc)) {
		t.Fatalf("got %q, want the document unchanged", got)
	}
	if yamledit.Changed([]byte(doc), got) {
		t.Fatal("Changed says the no-op wrote something")
	}
}

// TestSetEmptyMappingRefusesWhatItCannotReplaceSafely is the other half: a value
// this operation has no business dropping. A scalar and a sequence are
// RemoveKey's, and a flow collection with members is a shape the package does
// not splice into.
func TestSetEmptyMappingRefusesWhatItCannotReplaceSafely(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		path      []string
		reason    yamledit.Reason
	}{
		{"no path", "a: 1\n", nil, yamledit.ReasonEmptyPath},
		{"a scalar value", "review_floor: pro\n", []string{"review_floor"}, yamledit.ReasonReplacesCollection},
		{"a block sequence", "review_floor:\n  - pro\n", []string{"review_floor"}, yamledit.ReasonReplacesCollection},
		{"a flow mapping with members", "review_floor: {pro: [l2]}\n", []string{"review_floor"}, yamledit.ReasonReplacesCollection},
		{"a path that crosses a scalar", "a: 1\n", []string{"a", "review_floor"}, yamledit.ReasonNotAMapping},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetEmptyMapping([]byte(tc.doc), tc.path)
			if r := yamledit.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q, want %q (got %q, %v)", r, tc.reason, got, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

// TestExpandEmptyMappingOpensTheKeyForTheFirstMemberWrittenBack is the second
// operation the empty mapping needs: a member cannot be spliced into `{}`, so a
// write that adds the first one back expands the key to block style first — the
// comment above it and its place in the file kept — and writes under it.
func TestExpandEmptyMappingOpensTheKeyForTheFirstMemberWrittenBack(t *testing.T) {
	const doc = "# the owner's floor\nreview_floor: {}\nbase_branch: main\n"

	expanded, err := yamledit.ExpandEmptyMapping([]byte(doc), []string{"review_floor"})
	if err != nil {
		t.Fatalf("ExpandEmptyMapping: %v", err)
	}
	if want := "# the owner's floor\nreview_floor:\nbase_branch: main\n"; string(expanded) != want {
		t.Fatalf("got %q, want %q", expanded, want)
	}

	got, err := yamledit.AddListItem(expanded, []string{"review_floor", "pro"}, "l2")
	if err != nil {
		t.Fatalf("AddListItem after the expansion: %v", err)
	}
	const want = "# the owner's floor\nreview_floor:\n  pro:\n    - l2\nbase_branch: main\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)
}

// TestExpandEmptyMappingLeavesEveryOtherShapeAlone: the operation names one
// shape, and everything else — a block mapping, a scalar, an absent key — has
// nothing to expand and comes back byte for byte.
func TestExpandEmptyMappingLeavesEveryOtherShapeAlone(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		path      []string
	}{
		{"a block mapping", "review_floor:\n  pro:\n    - l2\n", []string{"review_floor"}},
		{"a key written with no value", "review_floor:\n", []string{"review_floor"}},
		{"a scalar", "review_floor: pro\n", []string{"review_floor"}},
		{"a flow mapping with members", "review_floor: {pro: [l2]}\n", []string{"review_floor"}},
		{"an absent key", "base_branch: main\n", []string{"review_floor"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.ExpandEmptyMapping([]byte(tc.doc), tc.path)
			if err != nil {
				t.Fatalf("ExpandEmptyMapping: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("got %q, want the document unchanged", got)
			}
			if yamledit.Changed([]byte(tc.doc), got) {
				t.Fatal("Changed says the no-op wrote something")
			}
		})
	}
}
