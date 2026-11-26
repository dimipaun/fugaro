package projectpolicy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The repository-to-image map (`repo_images`) is where a job's image comes
// from, keyed by provider/owner/name so that a GitHub and a Bitbucket acme/app
// are two keys rather than one (D3370, D3371). Its two halves — that a node
// round-trips through the escape, and that a provider this release does not
// serve is kept rather than refused — are what these tests hold.

// The round trip the node depends on: what this package writes is what it
// reads back under the canonical spelling. It is the test the plain map would
// fail silently — the escaped member name is the whole reason for the type, and
// a canonical name carries two slashes RTDB refuses in a key.
func TestRepoImagesRoundTrip(t *testing.T) {
	in := &Policy{Version: Version, Budget: Budget{Mode: "observe"}, RepoImages: RepoImages{
		"github/acme/app":    "go",
		"bitbucket/acme/app": "legacy",
	}}
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
	// The decoded map is keyed the canonical way, which is the spelling every
	// reader looks a job's repository up by.
	want := map[string]string{"github/acme/app": "go", "bitbucket/acme/app": "legacy"}
	if !slices.Equal(sortedStringKeys(p.RepoImages), sortedStringKeys(want)) {
		t.Fatalf("the decoded block is keyed %v, want the canonical names", p.RepoImages)
	}
	for k, v := range want {
		if p.RepoImages[k] != v {
			t.Errorf("repo_images[%s] = %q, want %q", k, p.RepoImages[k], v)
		}
	}
	// A member with no entry is absent from the node, exactly as an absent one
	// is: a node carrying "repo_images": {} would be noise in every diff.
	empty, err := json.Marshal(&Policy{Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "repo_images") {
		t.Errorf("an empty block is written to the node: %s", empty)
	}
}

// TestRepoImagesDecodeKeepsAnUnknownProvider is R10's read half: the provider is
// held to task.ValidProvider's SHAPE and not to the providers this release
// serves, so a key a later release wrote for a provider that does not exist yet
// is kept and skipped, never an error — an older reader that refused it, the
// credentials service among them, would halt the runs an update promises to
// leave alone.
//
// The nested old-form key acme/team/app is the same case seen from the other
// side: acme has the shape of a provider, so it reads as one and decodes, and it
// serves nothing because no repository is enrolled at a provider that does not
// exist. It is the writers, not this reader, that refuse it (R10).
func TestRepoImagesDecodeKeepsAnUnknownProvider(t *testing.T) {
	p, found, err := Decode([]byte(`{"version": 10,"repo_images":{"gitlab/acme/app":"go"}}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v), want a node it reads: a provider this release does not serve is kept, not refused (R10)", p, found, err)
	}
	if p.RepoImages["gitlab/acme/app"] != "go" {
		t.Errorf("RepoImages = %v, want the entry at the provider this release does not serve", p.RepoImages)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: repo_images is a member of this release", p.UnknownKeys)
	}

	nested, found, err := Decode([]byte(`{"version": 10,"repo_images":{"acme/team/app":"go"}}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v), want the nested old-form key read: acme has a provider's shape, and refusing it would refuse a node a later release may have written for real (R10)", nested, found, err)
	}
	if nested.RepoImages["acme/team/app"] != "go" {
		t.Errorf("RepoImages = %v, want the nested key kept", nested.RepoImages)
	}
	if got, fromRepoImages := nested.EffectiveRepoImages(); got["acme/team/app"] != "go" || !fromRepoImages {
		t.Errorf("EffectiveRepoImages = (%v, %v), want the map and true", got, fromRepoImages)
	}
}

// TestValidRepoImagesRefusesABadShape is the write half of R10 and spec
// acceptance 5: a key that is not provider/owner/name names no repository a
// lookup reaches, so it is refused with the form to write instead. An old-form
// key such as acme/app is one of these — its path is not canonical — and a
// provider in another case is another, because the key a lookup builds is
// Canonical's own output.
func TestValidRepoImagesRefusesABadShape(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{"the old form, a bare repository name", `{"acme/app":"go"}`, "<provider>/<owner>/<name>"},
		{"a path with no owner", `{"github/app":"go"}`, "<provider>/<owner>/<name>"},
		{"a provider in another case", `{"GitHub/acme/app":"go"}`, "<provider>/<owner>/<name>"},
		{"a path in another case", `{"github/Acme/App":"go"}`, "<provider>/<owner>/<name>"},
		{"a provider that is not a kind", `{"9github/acme/app":"go"}`, "<provider>/<owner>/<name>"},
		{"a key with no provider at all", `{"github":"go"}`, "<provider>/<owner>/<name>"},
		{"a value that is not an entry name", `{"github/acme/app":"Go_Image"}`, "catalog entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(`{"version": 10,"repo_images":` + tc.block + `}`))
			if err == nil {
				t.Fatalf("Decode(%s) = (%v, %v, nil), want a refusal", tc.block, p, found)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Decode(%s) = %v, want it to name %q", tc.block, err, tc.want)
			}
			if !strings.Contains(err.Error(), "repo_images") {
				t.Errorf("Decode(%s) = %v, want it to name the repository-to-image map (repo_images)", tc.block, err)
			}
		})
	}

	// One repository may be named once, in either spelling, exactly as
	// clone_urls refuses the pair: the escape is only what this package writes,
	// so a hand-written node can carry the canonical spelling beside the escaped
	// one, and the two fold onto one repository in the decoded map.
	for _, tc := range []struct{ name, block, want string }{
		{"the escaped and the literal spelling", `{"github%2Facme%2Fapp":"go","github/acme/app":"go"}`, "are both the repository"},
		{"one spelling, written twice", `{"github%2Facme%2Fapp":"go","github%2Facme%2Fapp":"legacy"}`, "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, found, err := Decode([]byte(`{"version": 10,"repo_images":` + tc.block + `}`))
			if err == nil {
				t.Fatalf("Decode(%s) = (%v, %v, nil), want a refusal: a repository mapped twice runs whichever image the keys happened to be read in", tc.block, p, found)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Decode(%s) = %v, want it to name %q", tc.block, err, tc.want)
			}
		})
	}

	// One repository under one spelling is what this package writes and what a
	// hand-written node writes, and neither is the pair above: both stay
	// readable.
	for _, block := range []string{
		`{"github%2Facme%2Fapp":"go"}`,
		`{"github/acme/app":"go"}`,
		`{"github/acme/app":"go","bitbucket/acme/app":"legacy"}`,
	} {
		if _, _, err := Decode([]byte(`{"version": 10,"repo_images":` + block + `}`)); err != nil {
			t.Errorf("Decode(%s) = %v, want it read", block, err)
		}
	}
}

// TestPolicyDecodeToleratesRepoImagesForOldReader simulates the reader of the
// release behind with the node this release publishes: the new member is listed
// among the unknown top-level keys it tolerates and read past (P8), the version
// does not move (R3), and the node it saw carries no map under the new name — so
// it reports none rather than the wrong image, which is what D3371 buys by
// giving the new keys a new member instead of re-meaning the old one.
func TestPolicyDecodeToleratesRepoImagesForOldReader(t *testing.T) {
	node := []byte(fmt.Sprintf(`{"version":%d,"budget":{"mode":"observe","per_run_usd":12},"base_branch":"main",`, Version) +
		`"allow_repos":["github/acme/app"],"repo_images":{"github%2Facme%2Fapp":"go"}}`)
	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "repo_images")

	q, found, err := decodeWithKeys(node, preKey)
	if err != nil || !found || q == nil {
		t.Fatalf("the reader that predates the member refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"repo_images"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("UnknownKeys = %v, want %v: the older reader lists the member and reads on", q.UnknownKeys, want)
	}
	// The node carries no image_map, so the older reader — which has no
	// RepoImages field at all, a thing a decode under another key set cannot
	// model — reports no map rather than the wrong one.
	if q.ImageMap != nil {
		t.Errorf("the older reader read the legacy map %v from a node that carries none", q.ImageMap)
	}
	if q.Budget.Mode != "observe" || q.Budget.PerRunUSD != 12 || q.BaseBranch != "main" {
		t.Errorf("the older reader read budget = %+v, base_branch = %q: the rest of the node is read past the member", q.Budget, q.BaseBranch)
	}
	if !slices.Equal(q.AllowRepos, []string{"github/acme/app"}) {
		t.Errorf("AllowRepos = %v, want the published list", q.AllowRepos)
	}

	// And the same node read by this release: the key is no longer unknown and
	// the map is there.
	p, _, err := Decode(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.UnknownKeys) != 0 || p.RepoImages["github/acme/app"] != "go" {
		t.Errorf("this release read UnknownKeys = %v, RepoImages = %v, want the map and no unknown keys", p.UnknownKeys, p.RepoImages)
	}
}

// TestEffectiveRepoImagesPerMember is R2: any repo_images wins over any
// image_map, per member and never per key, and the reader says which it read.
// A per-key fallback would let a map written by a CLI from before L11 revive the
// collision the new member exists to end (F16).
func TestEffectiveRepoImagesPerMember(t *testing.T) {
	for _, tc := range []struct {
		name           string
		node           string
		want           map[string]string
		fromRepoImages bool
	}{
		{
			name:           "the new member alone",
			node:           `{"version":10,"repo_images":{"github%2Facme%2Fapp":"go"}}`,
			want:           map[string]string{"github/acme/app": "go"},
			fromRepoImages: true,
		},
		{
			name: "the legacy member alone",
			node: `{"version":10,"image_map":{"acme/app":"go"}}`,
			want: map[string]string{"acme/app": "go"},
		},
		{
			name:           "both members, the new one wins",
			node:           `{"version":10,"repo_images":{"github%2Facme%2Fapp":"go"},"image_map":{"acme/app":"legacy"}}`,
			want:           map[string]string{"github/acme/app": "go"},
			fromRepoImages: true,
		},
		{
			name: "neither member",
			node: `{"version":10}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := Decode([]byte(tc.node))
			if err != nil {
				t.Fatal(err)
			}
			got, fromRepoImages := p.EffectiveRepoImages()
			if fromRepoImages != tc.fromRepoImages {
				t.Errorf("fromRepoImages = %v, want %v", fromRepoImages, tc.fromRepoImages)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("EffectiveRepoImages = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("EffectiveRepoImages[%s] = %q, want %q", k, got[k], v)
				}
			}
		})
	}

	// A nil policy is the same answer as a node with no map, so a caller never
	// has to tell the two apart.
	var nilPolicy *Policy
	if got, fromRepoImages := nilPolicy.EffectiveRepoImages(); got != nil || fromRepoImages {
		t.Errorf("a nil policy = (%v, %v), want (nil, false)", got, fromRepoImages)
	}
}
