// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envOf is a process environment a test owns: os.LookupEnv's own shape, which
// is what the switch is read through.
func envOf(kv map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := kv[name]
		return value, ok
	}
}

// rosterFor is the roster one installation's entry answers with.
func rosterFor(t *testing.T, entry string) Roster {
	t.Helper()
	cfg, on, err := DoorConfigFromEnv(envOf(map[string]string{FrontDoorEnv: entry}))
	if err != nil {
		t.Fatalf("reading the installation's own entry: %v", err)
	}
	if !on {
		t.Fatal("the entry was read as absent, and it is the switch")
	}
	return cfg.Roster()
}

// TestTheRosterIsReadFromThePublishedState is D933's amendment, finding M-f:
// the lists come from the installation's own applied state — the entry the
// module renders onto the front door's revision — and never from one machine's
// local config.
//
// Two halves, because the rule has two ways to be broken. The first is what the
// roster answers, driven through the entry alone: the lists it hands M7 are the
// ones that entry carries and nothing else. The second is that no file on the
// serve path reads a local one at all — the front door's process is the
// revision's, it holds no config file and no home directory, and a reader that
// reached for either would be reading the operator's machine from a public
// endpoint.
func TestTheRosterIsReadFromThePublishedState(t *testing.T) {
	const launcher, operator = "user:launcher@example.com", "user:operator@example.com"
	roster := rosterFor(t, `{"issuer":"`+testIssuer+`","launchers":["`+launcher+`"],"operators":["`+operator+`"]}`)
	members, err := roster.Members(context.Background())
	if err != nil {
		t.Fatalf("reading the installation's lists: %v", err)
	}
	if len(members.Launchers) != 1 || members.Launchers[0] != launcher {
		t.Errorf("launchers = %v, want the one the installation's entry carries", members.Launchers)
	}
	if len(members.Operators) != 1 || members.Operators[0] != operator {
		t.Errorf("operators = %v, want the one the installation's entry carries", members.Operators)
	}

	// The switch itself: an installation that has not opted in renders no
	// entry, and "no entry" is off rather than an error and rather than a
	// door that admits nobody by accident (D934).
	if _, on, err := DoorConfigFromEnv(envOf(nil)); on || err != nil {
		t.Errorf("an installation with no entry = (on %v, err %v); want off and no error", on, err)
	}

	// And the entry is present-but-empty is not absent: a revision that
	// carries the document and no lists is a door built with lists an
	// operator left out, which is a wiring mistake to be read rather than an
	// installation that never opted in.
	if _, on, err := DoorConfigFromEnv(envOf(map[string]string{FrontDoorEnv: "{}"})); !on || err != nil {
		t.Errorf("an entry carrying nothing = (on %v, err %v); want the door on and the document read", on, err)
	}

	// The second half: the serve path names no local config and reads no
	// file. The two files are the whole of it — the roster and the command
	// that builds the door — and internal/localcfg is the package a machine's
	// own configuration is read through.
	for _, path := range []string{
		"roster.go",
		filepath.Join("..", "cli", "coordinator_serve.go"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for _, refused := range []string{"localcfg", "os.ReadFile", "UserHomeDir", "os.UserConfigDir"} {
			if strings.Contains(string(data), refused) {
				t.Errorf("%s names %s: D933's lists are read from the installation's own published state — the value the module renders — and never from one machine's local config, which is the file an operator's own machine holds and the reason finding M-f exists", path, refused)
			}
		}
	}
}

// TestOnlyExactUserEntriesAdmit is D923's other half on this source: the entry
// kind is exact, and an entry of another kind is inert.
//
// It is asserted here rather than only in identity.go because this is where the
// entries stop being a configuration document: a `domain:` entry is a member
// Terraform accepts, an operator can apply, and `fugaro user` refuses to write
// — and a front door that read one would be the way such an entry reached the
// project's tools, which is the whole of why the kind is fixed.
func TestOnlyExactUserEntriesAdmit(t *testing.T) {
	const person = "user:someone@example.com"
	for _, tc := range []struct {
		name     string
		setting  string
		identity string
		admits   bool
	}{
		{"an exact entry", `{"launchers":["` + person + `"]}`, person, true},
		{"an operator's entry", `{"operators":["` + person + `"]}`, person, true},
		{"a domain entry", `{"launchers":["domain:example.com"]}`, person, false},
		{"a group entry", `{"launchers":["group:eng@example.com"]}`, person, false},
		{"a service account", `{"launchers":["serviceAccount:bot@proj.iam.gserviceaccount.com"]}`, person, false},
		{"everybody", `{"launchers":["allUsers","allAuthenticatedUsers"]}`, person, false},
		{"an address that is a prefix of one", `{"launchers":["user:someone@example.com.evil"]}`, person, false},
		{"another case", `{"launchers":["user:Someone@example.com"]}`, person, false},
		{"another person", `{"launchers":["user:other@example.com"]}`, person, false},
		{"nothing at all", `{}`, person, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role, err := membership(mustMembers(t, rosterFor(t, tc.setting)), tc.identity)
			switch {
			case tc.admits && err != nil:
				t.Errorf("the identity was refused: %v", err)
			case !tc.admits && !errors.Is(err, ErrIdentityOffTheLists):
				t.Errorf("the identity was admitted with %+v; want the refusal of a caller the lists do not name", role)
			}
		})
	}

	// The lists answer the same way twice, which is what makes the two halves
	// above about the rule rather than about a map's own order.
	members := mustMembers(t, rosterFor(t, `{"launchers":["user:b@example.com","user:a@example.com","group:eng@example.com","user:a@example.com"]}`))
	if strings.Join(members.Launchers, ",") != "user:a@example.com,user:b@example.com" {
		t.Errorf("launchers = %v, want the two people, sorted and deduplicated, and neither the group nor a repeat", members.Launchers)
	}
}

// TestAFrontDoorEntryThatIsNotTheDocumentIsRefused is the entry's own strict
// decode. A value is the installation's own text, and one that is not the
// document this door reads is a refusal to start rather than a door that serves
// nothing: the operator who wrote it is looking at a front door they believe
// they configured.
func TestAFrontDoorEntryThatIsNotTheDocumentIsRefused(t *testing.T) {
	for _, entry := range []string{
		`not json`,
		`{"issuer":"https://coordinator.example.com","launchers":[],"operators":[],"enabled":true}`,
		`{"issuer":"https://coordinator.example.com"} {"issuer":"https://elsewhere.example.com"}`,
	} {
		if _, on, err := DoorConfigFromEnv(envOf(map[string]string{FrontDoorEnv: entry})); err == nil {
			t.Errorf("%s was read as a door configuration (on %v); want the refusal that names the entry", entry, on)
		}
	}
}

// mustMembers reads a roster's lists, failing the test when the source refuses.
func mustMembers(t *testing.T, roster Roster) Members {
	t.Helper()
	members, err := roster.Members(context.Background())
	if err != nil {
		t.Fatalf("reading the installation's lists: %v", err)
	}
	return members
}
