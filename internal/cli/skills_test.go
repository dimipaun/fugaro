package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/watch"
)

// Which skill commands and flags exist is checked by the plugin's lint
// (plugin/skills_lint_test.go), which builds this package's root command. The
// field claims live here, beside the unexported structs they describe.

// jsonFieldClaim is a field a skill reads from a command's --json output.
type jsonFieldClaim struct {
	command string // the fugaro command, for the message
	typ     any    // the value its JSON is encoded from
	path    string // dotted; [] marks a list: "smoke.checks[].ok"
}

// skillJSONFields lists every JSON field the skills read. A skill that reads
// a new one adds it here; a renamed field fails this test.
var skillJSONFields = []jsonFieldClaim{
	{"validate", validateOutput{}, "valid"},
	{"validate", validateOutput{}, "problems"},
	{"validate", validateOutput{}, "problems[].path"},
	{"validate", validateOutput{}, "problems[].message"},
	{"validate", validateOutput{}, "warnings"},
	{"validate", validateOutput{}, "warnings[].path"},
	{"validate", validateOutput{}, "warnings[].message"},
	{"image build --local", image.LocalResult{}, "image"},
	{"image build --local", image.LocalResult{}, "dockerfile"},
	{"image build --local", image.LocalResult{}, "commit"},
	{"image build --local", image.LocalResult{}, "origin"},
	{"image build --local", image.LocalResult{}, "error"},
	{"image build --local", image.LocalResult{}, "smoke.passed"},
	{"image build --local", image.LocalResult{}, "smoke.checks[].name"},
	{"image build --local", image.LocalResult{}, "smoke.checks[].ok"},
	{"image build --local", image.LocalResult{}, "smoke.checks[].detail"},
	{"secrets ls", []secretEntry{}, "[].name"},
	{"secrets ls", []secretEntry{}, "[].id"},
	{"secrets ls", []secretEntry{}, "[].labels"},
	{"secrets ls", []secretEntry{}, "[].versions"},
	{"secrets ls", []secretEntry{}, "[].latest"},
}

func init() {
	add := func(cmd string, typ any, paths ...string) {
		for _, p := range paths {
			skillJSONFields = append(skillJSONFields, jsonFieldClaim{cmd, typ, p})
		}
	}
	add("run", launchResult{}, "run", "repo", "run_id", "branch", "execution", "log_url", "status")
	add("ls", lsDoc{}, "project", "runs", "totals", "warnings",
		"runs[].run", "runs[].repo", "runs[].status", "runs[].stage", "runs[].reason", "runs[].halt", "runs[].pr_url",
		"runs[].branch", "runs[].outcome", "runs[].created", "runs[].cost", "runs[].cost.route_by", "runs[].cost.reported_usd",
		"runs[].terminal", "runs[].settled", "runs[].base_branch", "runs[].stale_draft", "runs[].draft_fallback", "runs[].pr_status_at")
	add("diagnose", Diagnosis{}, "row", "row.pr_url", "row.reason", "halt", "verify", "failed", "flaky", "findings", "agent_message",
		"log_tail", "draft_note", "report_path", "follow_up", "comments_path", "route_by", "reported_usd")
	add("logs", logLine{}, "time", "severity", "stage", "stream", "event", "message")
	add("cancel", cancelResult{}, "project", "run", "status", "marker", "hard", "pr")
	add("watch --once", watch.BuildJSON("p", watch.View{}), "project")
}

// hasJSONField reports whether the JSON encoding of t has the dotted path.
// Embedded structs are flattened, as encoding/json does; a "[]" in the path
// steps into a slice's element.
func hasJSONField(t reflect.Type, path string) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if path == "" {
		return true
	}
	head, rest, _ := strings.Cut(path, ".")
	list := strings.HasSuffix(head, "[]")
	head = strings.TrimSuffix(head, "[]")
	if head == "" { // a leading "[]": the root is a list
		return t.Kind() == reflect.Struct && hasJSONField(t, rest)
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	ft, ok := jsonFieldType(t, head)
	if !ok {
		return false
	}
	if list && ft.Kind() != reflect.Slice {
		return false
	}
	return hasJSONField(ft, rest)
}

func jsonFieldType(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct {
			if ft, ok := jsonFieldType(f.Type, name); ok {
				return ft, true
			}
			continue
		}
		if tag == "-" || !f.IsExported() {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		if tag == name {
			return f.Type, true
		}
	}
	return nil, false
}

// TestLintJSONFieldsExist fails when a field a skill reads from --json output
// is not in the structs that output is encoded from (design §4.6 test 4).
func TestLintJSONFieldsExist(t *testing.T) {
	for _, c := range skillJSONFields {
		if !hasJSONField(reflect.TypeOf(c.typ), c.path) {
			t.Errorf("fugaro %s --json: no field %q in %T", c.command, c.path, c.typ)
		}
	}
	// The lint itself: a field that is not there, and a path through a
	// non-list, are caught.
	for _, bad := range []string{"validity", "problems[].nope", "valid[].x", "smoke.checks[].ok"} {
		if hasJSONField(reflect.TypeOf(validateOutput{}), bad) {
			t.Errorf("%q reported present in validateOutput", bad)
		}
	}
	if !hasJSONField(reflect.TypeOf(image.LocalResult{}), "smoke.checks[].ok") {
		t.Error("smoke.checks[].ok reported absent from LocalResult")
	}
}
