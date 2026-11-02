// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// The four tests below are `coordinator show`'s (coordinator v2 C3, D816): one
// pull request as the host has it, read through the coordinator's own read-only
// installation token and nothing else.
//
//   - TestCoordinatorShowPrintsTheHostsBody drives the command's own wiring —
//     no opener and no mint replaced — against a hermetic host, and reads the
//     document back as data. The fixture is the whole assertion: the mint's
//     request body is the two-read set and the pull-request read carries that
//     token's own Authorization header, so a read authenticated with the run's
//     set (two writes among them) fails on the fixture's own check.
//   - TestCoordinatorShowRefusesUnderTheCredentialsService is the property
//     reconciliation F9 says this read inherits: where the installation's
//     credentials service holds the App's key, this command holds none and
//     refuses, naming the service. It is the half a later change could
//     silently lose.
//   - TestCoordinatorShowRefusesAPullRequestTheHostDoesNotHave: a pull request
//     the host does not have is a refusal carrying the host's own status, and
//     never an empty document.
//   - TestCoordinatorShowRefusesAMalformedArgumentBeforeAnyRead: a --repo or
//     --pr that is not one is refused before the host is reached at all.

// showFixtureEnv is the environment every test here reads through: the App's
// ID and a fresh key (the mint's whole input), the credentials service off (the
// key is this process's), and the pinned GitHub root pointed at srv.
func showFixtureEnv(t *testing.T, root string) func([]string, func(string)) (coordinator.ProviderOpener, error) {
	t.Helper()
	realOpen := openCoordinatorProvider
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	setCoordinatorGitHubRoot(t, root)
	return realOpen
}

func TestCoordinatorShowPrintsTheHostsBody(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON (%v):\n%s", err, out)
	}
	for key, want := range map[string]any{
		"number": float64(1458),
		"url":    "https://github.com/acme/app/pull/1458",
		"title":  "Add search",
		"state":  "open",
		"draft":  true,
		"branch": "topic/search",
		"base":   "main",
		"author": "1001",
	} {
		if doc[key] != want {
			t.Errorf("the document's %s is %#v, want %#v:\n%s", key, doc[key], want, out)
		}
	}
	body, _ := doc["body"].(string)
	if !strings.Contains(body, "Adds a search box to the header.") {
		t.Errorf("the document does not carry the host's body: %#v\n%s", doc["body"], out)
	}

	// The document is the host's answer and nothing else: every key it carries
	// is one of the shape's, and a field the host did not answer is absent
	// rather than empty (PRDocument).
	want := []string{"author", "base", "body", "branch", "draft", "number", "state", "title", "url"}
	got := make([]string, 0, len(doc))
	for key := range doc {
		got = append(got, key)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the document carries %v, want the host's own answer %v", got, want)
	}

	// The installation token the read was authenticated with is the one value
	// this path holds, and it reaches no output (the rule every read of the
	// coordinator's keeps).
	if strings.Contains(out, "ghs_fixture") {
		t.Errorf("the document carries the installation token:\n%s", out)
	}
}

// The same document, drawn for a person: the command prints the host's words
// as text and never as a link or an element, and the body is printed through
// the sanitiser that strips what a terminal would act on.
func TestCoordinatorShowPrintsTheHostsBodyAsText(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{"acme/app#1458", "Add search", "topic/search", "Adds a search box to the header."} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ghs_fixture") {
		t.Errorf("the text output carries the installation token:\n%s", out)
	}
}

// The refusal reconciliation F9 says this read inherits, and the one a later
// change could lose without noticing: with the installation's credentials
// service in the environment, the App's key is the service's and this command
// holds none, so it refuses (exit 1) naming the service — before it reads the
// App's ID, opens a provider or mints anything. The empty fixture is what makes
// that last half a claim rather than a reading of the code: a request that
// arrived would fail the test rather than the network being asked.
func TestCoordinatorShowRefusesUnderTheCredentialsService(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := showFixtureEnv(t, srv.URL)
	t.Setenv(providers.EnvCredentialsServiceURL, "https://credentials.example")

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err == nil {
		t.Fatalf("the credentials service holds the App's key and the command read one anyway: %s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the service holds the key: exit %d, want %d: the environment the caller is running in is the caller's to fix",
			fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), providers.EnvCredentialsServiceURL) {
		t.Errorf("the service holds the key: %v, want a failure naming %s", err, providers.EnvCredentialsServiceURL)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}
}

// A pull request the host does not have is a refusal carrying the host's own
// status, and never an empty document: an empty one would draw a pull request
// with no description, which is a different fact from "the host has no such
// pull request".
func TestCoordinatorShowRefusesAPullRequestTheHostDoesNotHave(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show_missing.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "9999", "--json")
	if err == nil {
		t.Fatalf("the host has no pull request 9999 and the command printed one: %s", out)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the refusal is %v, want the host's own status in it", err)
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Errorf("exit %d, want %d: the host refused the read, and that is the host's failure",
			fcli.ExitCode(err), fcli.ExitRemoteError)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}
}

// A repository on a host this read does not serve is refused with the
// coordinator's own reason and never silently, and the provider is read from
// the repository's own config entry (C1's field, as the stored view carries it).
// The empty fixture is the other half: nothing is opened and nothing is minted
// for a repository whose read is refused before it starts.
func TestCoordinatorShowRefusesABitbucketRepository(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	f := newCoordinatorFixture(t, nil)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	setCoordinatorHost(t, realOpen)

	_, _, err := execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err == nil {
		t.Fatal("a bitbucket repository was read through the coordinator's github credential")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Errorf("a bitbucket repository: exit %d, %v", fcli.ExitCode(err), err)
	} else if strings.Contains(err.Error(), "legacy") {
		t.Errorf("the refusal names the repository rather than the reason: %v", err)
	}
}

// An argument that is not one is refused before the host is reached: the empty
// fixture would fail on any request, so what this holds is that no read is made
// for a --repo that is not owner/name or a --pr that is not a pull request
// number.
func TestCoordinatorShowRefusesAMalformedArgumentBeforeAnyRead(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	for _, args := range [][]string{
		{"coordinator", "show", "--repo", "acme", "--pr", "1458", "--json"},
		{"coordinator", "show", "--repo", "acme/app/extra", "--pr", "1458", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--pr", "0", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--pr", "-1", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--pr", "not-a-number", "--json"},
	} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Errorf("%v was accepted: %s", args, out)
			continue
		}
		if fcli.ExitCode(err) != fcli.ExitUserError {
			t.Errorf("%v: exit %d, want %d: the argv is the caller's to fix", args, fcli.ExitCode(err), fcli.ExitUserError)
		}
		if out != "" {
			t.Errorf("%v printed a document: %q", args, out)
		}
	}
}
