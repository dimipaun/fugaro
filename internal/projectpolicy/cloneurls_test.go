package projectpolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

// The clone_urls block is where a run's clone URL comes from on a project job
// (plan task 3.3 step 3): the runner looks the canonical repository up and
// clones from what it finds, so the block's two halves — that a run finds its
// entry, and that an entry cannot point at another repository — are what these
// tests hold.

// A published node can be decoded, and its entry answers CloneURL's lookup.
// The member name is the escaped spelling, because a canonical repository name
// carries two slashes and RTDB refuses a key containing one: a node this
// package writes is a node RTDB accepts.
func TestDecodeReadsCloneURLs(t *testing.T) {
	const url = "https://github.com/acme/app.git"
	p, found, err := Decode([]byte(`{"version":3,"clone_urls":{"github%2Facme%2Fapp":"` + url + `"}}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v), want it found and read", p, found, err)
	}
	if got := p.CloneURL("github/acme/app"); got != url {
		t.Errorf("CloneURL(github/acme/app) = %q, want %q", got, url)
	}
	if got := p.CloneURL("github/acme/other"); got != "" {
		t.Errorf("CloneURL(github/acme/other) = %q, want \"\": a repository the project does not list clones from its provider", got)
	}
	// The decoded map is keyed the canonical way, which is the spelling every
	// other reader uses (allow_repos patterns, task.Slug, the CLI's diff).
	if p.CloneURLs["github/acme/app"] != url {
		t.Errorf("the decoded block is keyed %v, want the canonical name", p.CloneURLs)
	}
}

// A node written by hand, where a person spells the repository the way
// allow_repos does, stays readable: unescKey returns a key with no escape in it
// unchanged, so the escape is only what this package writes.
func TestDecodeReadsAHandWrittenCloneURLsKey(t *testing.T) {
	const url = "https://github.com/acme/app.git"
	p, found, err := Decode([]byte(`{"version":3,"clone_urls":{"github/acme/app":"` + url + `"}}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v), want it found and read", p, found, err)
	}
	if got := p.CloneURL("github/acme/app"); got != url {
		t.Errorf("CloneURL(github/acme/app) = %q, want %q", got, url)
	}
}

// The round trip the node depends on: what this package writes is what it
// reads back. It is the test the plain map would fail silently — the escaped
// member name is the whole reason for the type.
func TestCloneURLsRoundTripThroughTheNode(t *testing.T) {
	const url = "https://github.com/acme/app.git"
	in := &Policy{Version: Version, Budget: Budget{Mode: "off"}, CloneURLs: CloneURLs{"github/acme/app": url}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"github%2Facme%2Fapp"`) {
		t.Errorf("the encoded node does not escape the member name, so RTDB would refuse the write: %s", raw)
	}
	p, found, err := Decode(raw)
	if err != nil || !found {
		t.Fatalf("Decode of our own encoding = (%v, %v, %v), want it found and read", p, found, err)
	}
	if got := p.CloneURL("github/acme/app"); got != url {
		t.Errorf("CloneURL(github/acme/app) after the round trip = %q, want %q", got, url)
	}
}

// An empty block is absent from the node, exactly as an absent one is: a policy
// with no clone URL is a policy that says nothing about clone URLs, and a node
// carrying "clone_urls": {} would be noise in every diff.
func TestCloneURLsOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(&Policy{Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "clone_urls") {
		t.Errorf("an empty block is written to the node: %s", raw)
	}
}

// Every refusal here is the security property, not decoration: the URL is where
// a run clones from, while its slug, its locks, its cache and its budget
// session all name the repository its own task.json does — so an entry naming
// another repository, or another provider's host, would have the run do one
// repository's work under another's identity. It is refused on the node rather
// than trusted, one function for the runner and the deployer (repourl.CloneURL).
func TestDecodeRefusesBadCloneURLs(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{"another repository", `{"github/acme/app":"https://github.com/acme/other.git"}`, "acme/other"},
		{"another owner", `{"github/acme/app":"https://github.com/evil/app.git"}`, "evil/app"},
		{"another provider's host", `{"bitbucket/acme/app":"https://github.com/acme/app.git"}`, "not on bitbucket's host"},
		{"unknown host", `{"github/acme/app":"https://gitlab.com/acme/app.git"}`, "not on github's host"},
		{"http", `{"github/acme/app":"http://github.com/acme/app.git"}`, "https URL without credentials"},
		{"credentials in the URL", `{"github/acme/app":"https://x-access-token:ghs_secret@github.com/acme/app.git"}`, "https URL without credentials"},
		{"a key that is not a canonical name", `{"github/acme/App":"https://github.com/acme/App.git"}`, "canonical repository name"},
		{"a key with no provider", `{"acme/app":"https://github.com/acme/app.git"}`, "canonical repository name"},
		{"a key with no owner", `{"github/app":"https://github.com/acme/app.git"}`, "canonical repository name"},
		{"a key with an unknown provider", `{"gitlab/acme/app":"https://gitlab.com/acme/app.git"}`, "not on gitlab's host"},
		{"a key with a truncated escape", `{"github%2":"https://github.com/acme/app.git"}`, "truncated escape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(`{"version":3,"clone_urls":` + tc.block + `}`))
			if err == nil {
				t.Fatalf("Decode(%s) = (%v, %v, nil), want a refusal", tc.block, p, found)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Decode(%s) = %v, want it to name %q", tc.block, err, tc.want)
			}
			// The refusal must not echo a credential it refused for carrying
			// one: it is printed by the CLI and written to a job's log.
			if strings.Contains(err.Error(), "ghs_secret") {
				t.Errorf("the refusal echoes the URL's credentials: %v", err)
			}
		})
	}
}

// One repository may be named once, in either spelling. The escape is only what
// this package writes (unescKey returns a key with no escape in it unchanged),
// so a hand-written node can carry the canonical spelling of a repository
// beside the escaped one, and both fold onto one canonical name in the decoded
// map. Which URL survived was then whichever member the map's iteration saw
// last, and both answers are visible: the surviving URL is what the run clones
// from, and it is the entry validCloneURLs validates, so the same bytes were
// accepted on one read of the node and refused on the next — 355 of 400 decodes
// of one node accepted and 45 refused, from Go's map order alone. model_prices
// refuses the analogous pair explicitly (Prices.UnmarshalJSON), and this block
// reads the same way.
//
// Mutation (verified, run and restored): drop the `spelled` check (and the
// sort) — the first case below is accepted, with one of the two URLs in the
// map. Mutation: drop checkNoDuplicateNames — the second case is accepted.
func TestDecodeRefusesOneRepositoryNamedTwice(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{"the escaped and the literal spelling, two URLs this repository answers to", `{"github%2Facme%2Fapp":"https://github.com/acme/app.git","github/acme/app":"https://github.com/acme/app"}`, "are both the repository"},
		{"the escaped and the literal spelling, one of them another repository's URL", `{"github%2Facme%2Fapp":"https://github.com/acme/app.git","github/acme/app":"https://github.com/evil/app.git"}`, "are both the repository"},
		{"one spelling, written twice", `{"github%2Facme%2Fapp":"https://github.com/acme/app.git","github%2Facme%2Fapp":"https://github.com/evil/app.git"}`, "names \"github%2Facme%2Fapp\" twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 20; i++ { // the answer must not move with the map's order
				p, found, err := Decode([]byte(`{"version":3,"clone_urls":` + tc.block + `}`))
				if err == nil {
					t.Fatalf("Decode(%s) = (%v, %v, nil), want a refusal: a repository named twice is one whose clone URL depends on the order the keys were read in", tc.block, p, found)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Decode(%s) = %v, want it to name %q", tc.block, err, tc.want)
				}
			}
		})
	}
	// One repository under one spelling is what this package writes and what a
	// hand-written node writes, and neither is the pair above: both stay
	// readable, which is what the refusal has to leave alone.
	for _, block := range []string{
		`{"github%2Facme%2Fapp":"https://github.com/acme/app.git"}`,
		`{"github/acme/app":"https://github.com/acme/app.git"}`,
		`{"github%2Facme%2Fapp":"https://github.com/acme/app.git","github%2Facme%2Fother":"https://github.com/acme/other.git"}`,
	} {
		if _, _, err := Decode([]byte(`{"version":3,"clone_urls":` + block + `}`)); err != nil {
			t.Errorf("Decode(%s) = %v, want it read", block, err)
		}
	}
}
