package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// C2's tests: the record the run reads and the names the sides are built from.
// Everything here is hermetic (the plan's convention): no socket is opened, no
// cloud is reached and every address is a name that resolves nowhere.

// routeSchema compiles schemas/credential-route.schema.json, the published half
// of the contract. It is compiled here rather than trusted, so a schema that
// stopped describing the record the decoder reads is a failure in this package
// and not in a document nobody's test reads.
func routeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "credential-route.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/credential-route.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func validatesAgainstRouteSchema(t *testing.T, sch *jsonschema.Schema, raw string) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return sch.Validate(inst)
}

// TestRouteRecordDecodesStrictly: the record moves a member's credential from
// one service to another, so every way of reading it wrong has to be a refusal
// rather than a default. An unknown key is the one this test was written for
// (the plan's red step): a decoder that tolerated it would answer the project
// for a kind a newer member's `fugaro update` had moved to the workspace, which
// is a run waiting on a project that holds nothing rather than the run the
// member published.
func TestRouteRecordDecodesStrictly(t *testing.T) {
	const addr = "https://workspace.example.com"
	for _, tc := range []struct {
		name string
		raw  string
		want Route
		why  string // what the refusal must say, "" when the record is read
	}{
		{
			name: "every kind named",
			raw:  `{"version":1,"address":"` + addr + `","model":"workspace","bitbucket":"project","github":"workspace"}`,
			want: Route{Version: 1, Address: addr, Model: HolderWorkspace, Bitbucket: HolderProject, GitHub: HolderWorkspace},
		},
		{
			name: "only the kind that moved",
			raw:  `{"version":1,"address":"` + addr + `","model":"workspace"}`,
			want: Route{Version: 1, Address: addr, Model: HolderWorkspace},
		},
		{
			name: "the address alone",
			raw:  `{"version":1,"address":"` + addr + `"}`,
			want: Route{Version: 1, Address: addr},
		},
		{
			name: "an unknown key",
			raw:  `{"version":1,"address":"` + addr + `","modle":"workspace"}`,
			why:  "unknown field",
		},
		{
			name: "a version this code does not read",
			raw:  `{"version":2,"address":"` + addr + `","model":"workspace"}`,
			why:  "version 2",
		},
		{
			name: "no version at all",
			raw:  `{"address":"` + addr + `"}`,
			why:  "version 0",
		},
		{
			name: "a holder outside the vocabulary",
			raw:  `{"version":1,"address":"` + addr + `","model":"fugaro"}`,
			why:  `"fugaro"`,
		},
		{
			name: "an address with a query",
			raw:  `{"version":1,"address":"` + addr + `?a=b"}`,
			why:  "scheme and a host only",
		},
		{
			name: "an address with a path",
			raw:  `{"version":1,"address":"` + addr + `/v1"}`,
			why:  "scheme and a host only",
		},
		{
			name: "an address a caller could have been sent to in the clear",
			raw:  `{"version":1,"address":"http://workspace.example.com"}`,
			why:  "https",
		},
		{
			name: "no address",
			raw:  `{"version":1}`,
			why:  "scheme and a host only",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := DecodeRoute([]byte(tc.raw))
			if tc.why != "" {
				if err == nil {
					t.Fatalf("DecodeRoute(%s) = %+v, want a refusal naming %s", tc.raw, got, tc.why)
				}
				if !errors.Is(err, ErrRouteRecord) {
					t.Errorf("the refusal is not ErrRouteRecord: %v", err)
				}
				if !strings.Contains(err.Error(), tc.why) {
					t.Errorf("the refusal does not say %q: %v", tc.why, err)
				}
				if !found {
					t.Errorf("the refusal is reported as an absent record: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeRoute(%s): %v", tc.raw, err)
			}
			if !found {
				t.Fatalf("DecodeRoute(%s) = absent, want the record", tc.raw)
			}
			if got != tc.want {
				t.Errorf("DecodeRoute(%s) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestRouteRecordRoundTripsAndMatchesItsSchema: the record the run reads and the
// schema an editor validates against are one shape. A kind the route does not
// move is left out rather than written as the project's, so the bytes of one
// route are one record — WS9's publish is conditional on them — and the decoder
// answers the project for every kind they do not name.
func TestRouteRecordRoundTripsAndMatchesItsSchema(t *testing.T) {
	const addr = "https://workspace.example.com"
	sch := routeSchema(t)
	for _, r := range []Route{
		{Version: RouteVersion, Address: addr},
		{Version: RouteVersion, Address: addr, Model: HolderWorkspace},
		{Version: RouteVersion, Address: addr, Model: HolderWorkspace, Bitbucket: HolderProject, GitHub: HolderWorkspace},
	} {
		raw, err := RouteRecord(r)
		if err != nil {
			t.Fatalf("RouteRecord(%+v): %v", r, err)
		}
		if err := validatesAgainstRouteSchema(t, sch, string(raw)); err != nil {
			t.Errorf("schemas/credential-route.schema.json refuses %s, which RouteRecord wrote: %v", raw, err)
		}
		back, found, err := DecodeRoute(raw)
		if err != nil || !found {
			t.Fatalf("DecodeRoute(%s) = %+v, %v, %v", raw, back, found, err)
		}
		if back != r {
			t.Errorf("RouteRecord/DecodeRoute round trip = %+v, want %+v", back, r)
		}
		if got, err := RouteRecord(back); err != nil || !bytes.Equal(got, raw) {
			t.Errorf("a re-encode of %s = %s, %v: one route must have one spelling", raw, got, err)
		}
		for _, kind := range Kinds {
			want := HolderProject
			if r.Holder(kind) == HolderWorkspace {
				want = HolderWorkspace
			}
			if got := back.Holder(kind); got != want {
				t.Errorf("Holder(%s) = %q, want %q", kind, got, want)
			}
		}
	}

	// The schema is the contract's other half, so it refuses what the decoder
	// refuses: an unknown key, and a holder outside the vocabulary.
	for _, bad := range []string{
		`{"version":1,"address":"` + addr + `","modle":"workspace"}`,
		`{"version":1,"address":"` + addr + `","model":"fugaro"}`,
		`{"version":2,"address":"` + addr + `"}`,
	} {
		if err := validatesAgainstRouteSchema(t, sch, bad); err == nil {
			t.Errorf("schemas/credential-route.schema.json accepts %s, which DecodeRoute refuses", bad)
		}
	}
	// A record the decoder reads but the schema does not is the other drift, and
	// the one that would let a member publish bytes an editor calls invalid.
	for _, good := range []string{
		`{"version":1,"address":"` + addr + `"}`,
		`{"version":1,"address":"` + addr + `","model":"workspace"}`,
	} {
		if err := validatesAgainstRouteSchema(t, sch, good); err != nil {
			t.Errorf("schemas/credential-route.schema.json refuses %s, which DecodeRoute reads: %v", good, err)
		}
	}

	if _, _, err := DecodeRoute([]byte("null")); err != nil {
		t.Errorf("a null node is an absent record and not a refusal: %v", err)
	} else if _, found, _ := DecodeRoute([]byte("null")); found {
		t.Error("a null node is reported as a record")
	}
}

// TestStubRouteIsAlwaysTheProject: the stub is the other side of C2 for WS11
// before WS9 publishes anything, and the one answer that keeps it honest is
// today's — every repository and kind is the project's, and there is no second
// service for a run to reach.
func TestStubRouteIsAlwaysTheProject(t *testing.T) {
	var reader RouteReader = StubRoute{}
	for _, slug := range []string{"acme-app-0123", "", "owner/repo"} {
		r, err := reader.Route(context.Background(), slug)
		if err != nil {
			t.Fatalf("StubRoute.Route(%q): %v", slug, err)
		}
		if r.Address != "" {
			t.Errorf("StubRoute.Route(%q).Address = %q: a stub with an address builds a second upstream nobody published", slug, r.Address)
		}
		for _, kind := range append(Kinds, Kind("a-kind-of-a-later-release")) {
			if got := r.Holder(kind); got != HolderProject {
				t.Errorf("StubRoute.Route(%q).Holder(%s) = %q, want %q", slug, kind, got, HolderProject)
			}
		}
	}
}

// TestRouteNamesArePinned: the five names of C2 are what other sides spell —
// WS3 the pin's field, WS6 the service's name and the mode's, WS9 the record's
// path, WS11 the reader, WS13 the setting, WS20 the checks, WS22 the rules —
// and a spelling that changed here would change a wire format or a deployment
// with nothing red. The list is pinned so a change is deliberate, and the two
// forge kinds are held to their forge's own names.
func TestRouteNamesArePinned(t *testing.T) {
	for _, tc := range []struct{ what, got, want string }{
		{"WorkspaceModeEnv", WorkspaceModeEnv, "FUGARO_WORKSPACE_MODE"},
		{"WorkspaceServiceName", WorkspaceServiceName, "fugaro-workspace-credentials"},
		{"WorkspacePinAddressField", WorkspacePinAddressField, "address"},
		{"WorkspaceBeatSetting", WorkspaceBeatSetting, "agent.workspace_beat"},
		{"RoutePathParent()", RoutePathParent(), "config/policy/route"},
		{"string(KindModel)", string(KindModel), "model"},
		{"string(KindBitbucket)", string(KindBitbucket), gitprov.KindBitbucket},
		{"string(KindGitHub)", string(KindGitHub), gitprov.KindGitHub},
		{"string(HolderProject)", string(HolderProject), "project"},
		{"string(HolderWorkspace)", string(HolderWorkspace), "workspace"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.what, tc.got, tc.want)
		}
	}
	if RouteVersion != 1 {
		t.Errorf("RouteVersion = %d, want 1", RouteVersion)
	}
	if len(Kinds) != 3 || len(Holders) != 2 {
		t.Errorf("the vocabularies are %v and %v", Kinds, Holders)
	}
}

// TestRoutePathEscapesASlugLikeBudgetKey: the rules admit a run to its own
// repository's record with `auth.token.fs == $slug`, and that claim is
// budget.Key of the slug. A path that escaped another way would be a node the
// rule grants a read on and the run never asks for — a run whose record is
// unreachable and therefore served by the project, silently.
func TestRoutePathEscapesASlugLikeBudgetKey(t *testing.T) {
	for _, slug := range []string{"acme-app-0123", "a.b", "a$b", "a#b[c]d", "a/b", "100%"} {
		if got, want := RoutePath(slug), "config/policy/route/"+budget.Key(slug); got != want {
			t.Errorf("RoutePath(%q) = %q, want %q", slug, got, want)
		}
		if got := RoutePath(slug); strings.Contains(got, "/"+slug+"/") && strings.ContainsAny(slug, ".$#[]/%") {
			t.Errorf("RoutePath(%q) = %q keeps a character RTDB forbids in a key", slug, got)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("RoutePath(\"\") did not panic: an empty segment collapses the path onto every repository's parent")
		}
	}()
	_ = RoutePath("")
}

// TestRouteRecordRefusesWhatItCouldNotReadBack: the publisher's half. A record
// this code will not read is refused before it is published, so a member's own
// `fugaro update` cannot write a node every run of it then refuses.
func TestRouteRecordRefusesWhatItCouldNotReadBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Route
	}{
		{"no version", Route{Address: "https://workspace.example.com"}},
		{"a version of another release", Route{Version: 2, Address: "https://workspace.example.com"}},
		{"no address", Route{Version: RouteVersion}},
		{"an address with a query", Route{Version: RouteVersion, Address: "https://workspace.example.com?a=b"}},
		{"a holder outside the vocabulary", Route{Version: RouteVersion, Address: "https://workspace.example.com", Model: Holder("fugaro")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := RouteRecord(tc.r)
			if err == nil {
				t.Fatalf("RouteRecord(%+v) = %s, want a refusal", tc.r, raw)
			}
			if !errors.Is(err, ErrRouteRecord) {
				t.Errorf("the refusal is not ErrRouteRecord: %v", err)
			}
		})
	}
	// A record that reads back is published as its own bytes.
	r := Route{Version: RouteVersion, Address: "http://127.0.0.1:8080", Model: HolderWorkspace}
	raw, err := RouteRecord(r)
	if err != nil {
		t.Fatalf("RouteRecord(%+v): %v", r, err)
	}
	if !json.Valid(raw) {
		t.Errorf("RouteRecord wrote %q, which is not JSON", raw)
	}
}
