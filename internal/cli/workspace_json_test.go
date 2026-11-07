package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// workspaceFullHome is the document's own fixture: the workspace's own config
// declares aurora as a member and states a cap, aurora's own config names the
// workspace and carries the pin, and a third project names a workspace whose
// config this machine does not hold. Every branch of the document is therefore
// live: an owner row with a member and a cap, and a member row with an unknown
// member list.
func workspaceFullHome(t *testing.T) (home string) {
	t.Helper()
	home, aurora, owner := workspaceOwnerHome(t)
	workspaceAppend(t, aurora, `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`)
	workspaceAppend(t, owner, workspaceOwnerTable+"workspace_cap:\n  monthly: 50\n")
	workspaceAppend(t, writeProject(t, "borealis", "borealis-gcp"), `workspace: workspace-beta
workspace_pin:
  firebase_project: project-beta
  issuer: https://securetoken.google.com/project-beta
  assertion_key: marker-assertion-key
  service_account: marker-beta@project-beta.iam.gserviceaccount.com
`)
	return home
}

// workspaceLsDoc runs `workspace ls --json` and decodes it, failing on a
// document that does not parse at all.
func workspaceLsDoc(t *testing.T) map[string]any {
	t.Helper()
	out, _, err := execute(t, "workspace", "ls", "--json")
	if err != nil {
		t.Fatalf("workspace ls --json: %v\n%s", err, out)
	}
	return workspaceDecodeDoc(t, out)
}

func workspaceDecodeDoc(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return doc
}

// TestWorkspaceLsJSONIsVersioned: the document carries a version and a consumer
// reads it by that version (the config list rule). Members may only be added
// within it, so the version is what a removed or re-meant member bumps -- and
// the schema and the Go const have to agree about which one this is, because a
// document whose two halves disagree is one no consumer can pin.
func TestWorkspaceLsJSONIsVersioned(t *testing.T) {
	workspaceFullHome(t)
	doc := workspaceLsDoc(t)
	if got := doc["version"]; got != float64(workspaceLsDocVersion) {
		t.Errorf("version = %v, want %d", got, workspaceLsDocVersion)
	}
	for _, key := range []string{"version", "workspaces", "warnings"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the document has no %q member:\n%v", key, doc)
		}
	}
	// show prints the same document, one row wide.
	out, _, err := execute(t, "workspace", "show", "workspace-alpha", "--json")
	if err != nil {
		t.Fatalf("workspace show --json: %v\n%s", err, out)
	}
	shown := workspaceDecodeDoc(t, out)
	if got := shown["version"]; got != float64(workspaceLsDocVersion) {
		t.Errorf("show's version = %v, want %d", got, workspaceLsDocVersion)
	}
	rows, _ := shown["workspaces"].([]any)
	if len(rows) != 1 {
		t.Fatalf("show printed %d rows, want one:\n%s", len(rows), out)
	}
	// The schema's own const is the same number: the two are one contract.
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-list.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sch map[string]any
	if err := json.Unmarshal(raw, &sch); err != nil {
		t.Fatal(err)
	}
	props, _ := sch["properties"].(map[string]any)
	version, _ := props["version"].(map[string]any)
	if got := version["const"]; got != float64(workspaceLsDocVersion) {
		t.Errorf("schemas/workspace-list.schema.json's version const is %v, and workspaceLsDocVersion is %d: the document and its schema have to state one version", got, workspaceLsDocVersion)
	}
}

// TestWorkspaceLsJSONCarriesNoCredentialShapedField is the `config list` rule:
// the document is a NAMED SET OF FIELDS and not a marshalled localcfg.Config.
// A marshalled config would publish every key the file happens to carry, and
// the membership's pin is one of the keys it carries. The second half is the
// pin's own key: the document says a membership is pinned and never what the
// member's key is, so a reader cannot log one by accident.
func TestWorkspaceLsJSONCarriesNoCredentialShapedField(t *testing.T) {
	workspaceFullHome(t)
	out, _, err := execute(t, "workspace", "ls", "--json")
	if err != nil {
		t.Fatalf("workspace ls --json: %v\n%s", err, out)
	}
	// The KEY NAMES, not the whole text: a value may legitimately carry one of
	// these words -- the issuer is https://securetoken.google.com/<project> --
	// and what the rule is about is a field a consumer would read as a
	// credential.
	for _, key := range workspaceJSONKeys(t, out) {
		lower := strings.ToLower(key)
		for _, banned := range []string{"assertion_key", "api_key", "token", "secret", "password", "credential", "private_key"} {
			if strings.Contains(lower, banned) {
				t.Errorf("the document carries a %q field:\n%s", key, out)
			}
		}
	}
	// The value, not only the name: the key the pin carries is the member's,
	// and no command of this family prints it.
	if strings.Contains(out, "marker-assertion-key") {
		t.Errorf("the document prints the assertion key's value:\n%s", out)
	}
	// It does carry the pin's other three, which is what makes the check above
	// about the key rather than about an empty document.
	for _, want := range []string{"firebase_project", "issuer", "service_account", "project-ws"} {
		if !strings.Contains(out, want) {
			t.Errorf("the document does not carry %q:\n%s", want, out)
		}
	}
}

// workspaceJSONKeys is every object key the document holds, at any depth.
func workspaceJSONKeys(t *testing.T, out string) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var keys []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				keys = append(keys, k)
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(v)
	return keys
}

// TestWorkspaceLsJSONMatchesSchema validates both documents against
// schemas/workspace-list.schema.json, the contract a consumer reads them by.
func TestWorkspaceLsJSONMatchesSchema(t *testing.T) {
	workspaceFullHome(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-list.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/workspace-list.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"workspace", "ls", "--json"},
		{"workspace", "show", "workspace-alpha", "--json"},
		{"workspace", "show", "workspace-beta", "--json"},
	} {
		out, _, err := execute(t, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if err := sch.Validate(inst); err != nil {
			t.Fatalf("%v does not match schemas/workspace-list.schema.json: %v\n%s", args, err, out)
		}
	}

	// The document is only worth validating while it carries the members the
	// schema closes over: a fixture that stopped producing a member row, a cap
	// or an unknown half would leave those branches unchecked and still green.
	decoded := workspaceLsDoc(t)
	rows, _ := decoded["workspaces"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want two rows, got %d:\n%v", len(rows), decoded)
	}
	owner, _ := rows[0].(map[string]any)
	if owner["name"] != "workspace-alpha" || owner["owner"] != true {
		t.Fatalf("row 0 is %v, want workspace-alpha declared here", owner)
	}
	if cap, _ := owner["cap"].(map[string]any); cap == nil || cap["monthly_usd"] != float64(50) {
		t.Errorf("row 0's cap is %v, want 50 USD a month", owner["cap"])
	}
	if members, _ := owner["members"].([]any); len(members) != 1 {
		t.Errorf("row 0's members are %v, want one", owner["members"])
	}
	if memberships, _ := owner["memberships"].([]any); len(memberships) != 1 {
		t.Errorf("row 0's memberships are %v, want one", owner["memberships"])
	}
	named, _ := rows[1].(map[string]any)
	if named["name"] != "workspace-beta" || named["owner"] != false {
		t.Fatalf("row 1 is %v, want workspace-beta named only", named)
	}
	if named["members"] != nil {
		t.Errorf("row 1's members are %v, want null: this machine does not hold that table, and [] would read as a workspace with no members", named["members"])
	}
	if named["cap"] != nil {
		t.Errorf("row 1's cap is %v, want absent", named["cap"])
	}
}
