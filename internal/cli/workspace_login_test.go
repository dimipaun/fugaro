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

// TestWorkspaceLoginPrintsThePinThisMachineHolds: the command answers the one
// question a person has before a workspace sign-in — which Firebase is this
// project pinned to — from this machine's own file, and it prints the pin's
// fact about the assertion key rather than the key.
func TestWorkspaceLoginPrintsThePinThisMachineHolds(t *testing.T) {
	workspaceFullHome(t)
	out, _, err := execute(t, "workspace", "login", "workspace-alpha")
	if err != nil {
		t.Fatalf("workspace login: %v\n%s", err, out)
	}
	for _, want := range []string{
		"workspace-alpha",
		"aurora",
		"project-ws",
		"https://securetoken.google.com/project-ws",
		"marker-workspace@project-ws.iam.gserviceaccount.com",
		"assertion key: set",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not carry %q:\n%s", want, out)
		}
	}
	// The pin's own key is the member's, and this command is one of the places a
	// person meets the pin: no print of it may carry the value.
	if strings.Contains(out, "marker-assertion-key") {
		t.Errorf("the output prints the assertion key's value:\n%s", out)
	}
}

// TestWorkspaceLoginRefusesWhenThisProjectIsAMemberOfNothing is the member's
// side of the pin stated where a person meets it: `workspace rm` drops the
// membership key and the pin together, and a project with neither has no key, no
// project and no issuer to verify a member-bound assertion against, so there is
// no sign-in this command could describe. The refusal is the CLI twin of the
// exchange's own, which refuses an assertion when the member pins no workspace.
func TestWorkspaceLoginRefusesWhenThisProjectIsAMemberOfNothing(t *testing.T) {
	workspaceWriteHome(t)
	if out, _, err := execute(t, "workspace", "login", "workspace-alpha"); err == nil {
		t.Fatalf("workspace login described a sign-in on a machine that pins no workspace:\n%s", out)
	} else if !strings.Contains(err.Error(), "names workspace") {
		t.Fatalf("the refusal is %q, and it is about this machine's files naming no workspace", err)
	}

	// The state after the removal, on a machine that really did hold the pin:
	// the membership key and the pin are gone and it is the same refusal.
	if _, _, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add: %v", err)
	}
	if _, _, err := execute(t, "workspace", "rm", "workspace-alpha", "--yes"); err != nil {
		t.Fatalf("workspace rm: %v", err)
	}
	if out, _, err := execute(t, "workspace", "login", "workspace-alpha"); err == nil {
		t.Fatalf("workspace login described a sign-in after the membership was removed:\n%s", out)
	} else if !strings.Contains(err.Error(), "workspace rm") {
		t.Fatalf("the refusal is %q, and it says which command took the pin away", err)
	}
}

// TestWorkspaceLoginRefusesWhenTwoProjectsNameTheWorkspace: a sign-in belongs to
// one project's own pin, so a machine whose files name the workspace twice has
// no one pin to describe and says so rather than picking one.
func TestWorkspaceLoginRefusesWhenTwoProjectsNameTheWorkspace(t *testing.T) {
	_, aurora := workspaceWriteHome(t)
	workspaceAppend(t, aurora, `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`)
	workspaceAppend(t, writeProject(t, "borealis", "borealis-gcp"), `workspace: workspace-alpha
workspace_pin:
  firebase_project: project-ws
  issuer: https://securetoken.google.com/project-ws
  assertion_key: marker-assertion-key
  service_account: marker-workspace@project-ws.iam.gserviceaccount.com
`)

	out, _, err := execute(t, "workspace", "login", "workspace-alpha")
	if err == nil {
		t.Fatalf("workspace login described one project's sign-in on a machine that names the workspace from two:\n%s", out)
	}
	for _, want := range []string{"2 project configs", "aurora", "borealis"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, and it names %q", err, want)
		}
	}
}

// TestWorkspaceLoginJSONMatchesSchema validates the document against
// schemas/workspace-login.schema.json, the contract a consumer reads it by, and
// checks that the Go const and the schema's own const agree: a document whose
// two halves state different versions is one no consumer can pin.
func TestWorkspaceLoginJSONMatchesSchema(t *testing.T) {
	workspaceFullHome(t)
	out, _, err := execute(t, "workspace", "login", "workspace-alpha", "--json")
	if err != nil {
		t.Fatalf("workspace login --json: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-login.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The schema's own const is the same number, read the way the listing's test
	// reads it: the two halves of one contract have to state one version.
	var plain map[string]any
	if err := json.Unmarshal(raw, &plain); err != nil {
		t.Fatal(err)
	}
	props, _ := plain["properties"].(map[string]any)
	version, _ := props["version"].(map[string]any)
	if got := version["const"]; got != float64(workspaceLoginDocVersion) {
		t.Errorf("schemas/workspace-login.schema.json's version const is %v, and workspaceLoginDocVersion is %d: the document and its schema have to state one version", got, workspaceLoginDocVersion)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/workspace-login.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := compiled.Validate(inst); err != nil {
		t.Fatalf("workspace login --json does not match schemas/workspace-login.schema.json: %v\n%s", err, out)
	}

	decoded := workspaceDecodeDoc(t, out)
	if got := decoded["version"]; got != float64(workspaceLoginDocVersion) {
		t.Errorf("version = %v, want %d", got, workspaceLoginDocVersion)
	}
	// The document is only worth validating while it carries every member the
	// schema closes over: a fixture that stopped producing one would leave that
	// branch unchecked and still green.
	for _, key := range []string{"version", "project", "workspace", "pin", "next"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("the document has no %q member:\n%v", key, decoded)
		}
	}
	if decoded["workspace"] != "workspace-alpha" || decoded["project"] != "aurora" {
		t.Errorf("the document is %v, and it describes aurora's membership of workspace-alpha", decoded)
	}
	pin, _ := decoded["pin"].(map[string]any)
	if pin["firebase_project"] != "project-ws" || pin["assertion_key_set"] != true {
		t.Errorf("the document's pin is %v, want project-ws with the key reported as set", pin)
	}
}

// TestWorkspaceLoginJSONCarriesNoCredentialShapedField is the `config list`
// rule, which this document follows for the same reason the listing does: it is
// a NAMED SET OF FIELDS and not a marshalled localcfg.Config, so a key the file
// happens to carry -- the pin's assertion key among them -- is not published by
// a document that was never asked for it.
func TestWorkspaceLoginJSONCarriesNoCredentialShapedField(t *testing.T) {
	workspaceFullHome(t)
	out, _, err := execute(t, "workspace", "login", "workspace-alpha", "--json")
	if err != nil {
		t.Fatalf("workspace login --json: %v\n%s", err, out)
	}
	// The key NAMES, not the whole text: a value may legitimately carry one of
	// these words -- the issuer is https://securetoken.google.com/<project> --
	// and what the rule is about is a field a consumer would read as a
	// credential. `assertion_key_set` is the one name that carries the word, and
	// it is a bool about the pin rather than the key.
	for _, key := range workspaceJSONKeys(t, out) {
		if key == "assertion_key_set" {
			continue
		}
		lower := strings.ToLower(key)
		for _, banned := range []string{"assertion_key", "api_key", "token", "secret", "password", "credential", "private_key"} {
			if strings.Contains(lower, banned) {
				t.Errorf("the document carries a %q field:\n%s", key, out)
			}
		}
	}
	if strings.Contains(out, "marker-assertion-key") {
		t.Errorf("the document prints the assertion key's value:\n%s", out)
	}
	for _, want := range []string{"firebase_project", "issuer", "service_account", "project-ws"} {
		if !strings.Contains(out, want) {
			t.Errorf("the document does not carry %q:\n%s", want, out)
		}
	}
}

// TestWorkspaceLoginJSONIsDecodableAsItsOwnShape: the document is the login's
// own and not the listing's, so a consumer that read it as `workspace ls` would
// draw a wrong figure. The two are separate contracts with separate versions.
func TestWorkspaceLoginJSONIsDecodableAsItsOwnShape(t *testing.T) {
	workspaceFullHome(t)
	out, _, err := execute(t, "workspace", "login", "workspace-alpha", "--json")
	if err != nil {
		t.Fatalf("workspace login --json: %v\n%s", err, out)
	}
	var doc workspaceLoginDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Version != workspaceLoginDocVersion {
		t.Errorf("version = %d, want %d", doc.Version, workspaceLoginDocVersion)
	}
	if doc.Pin.Issuer != "https://securetoken.google.com/"+doc.Pin.FirebaseProject {
		t.Errorf("the document's issuer is %q and its project is %q, and the issuer is derived from the project", doc.Pin.Issuer, doc.Pin.FirebaseProject)
	}
	if doc.Next == "" || !strings.Contains(doc.Next, doc.Pin.FirebaseProject) {
		t.Errorf("the document's next step is %q, and it names the Firebase to sign in at", doc.Next)
	}
}
