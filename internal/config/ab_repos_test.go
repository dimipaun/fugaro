package config

import (
	"bytes"
	"reflect"
	"testing"
)

// TestABReposBounds pins D3297's rule for the layer's new per-repository map:
// ab.repos is keyed owner/name as defaults.rigor.repos is, and each value sits
// inside the block's own ab.rate — 0 is how the project opts one repository
// out without turning its own sampling off — so a value above the block's rate
// is refused naming the figure the layer wrote, not the shipped ceiling. The
// map is the project layer's alone, which is the repository file's own
// refusal (TestABReposRefusedInARepoFile).
func TestABReposBounds(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for _, tc := range []struct{ name, body, want string }{
		{"above the block's own rate", "ab:\n  rate: 0.02\n  repos: { acme/web: 0.05 }\n", "ab.repos.acme/web: is 0.05: a repository's rate sits inside the block's own ab.rate 0.02"},
		{"a negative rate", "ab:\n  rate: 0.02\n  repos: { acme/web: -0.01 }\n", "ab.repos.acme/web: is -0.01: a repository's rate sits inside the block's own ab.rate 0.02"},
		{"not a number", "ab:\n  rate: 0.02\n  repos: { acme/web: .nan }\n", "ab.repos.acme/web: is NaN: a repository's rate sits inside the block's own ab.rate 0.02"},
		{"a rate with no block rate to sit in", "ab:\n  repos: { acme/web: 0.01 }\n", "ab.repos.acme/web: is 0.01: a repository's rate sits inside the block's own ab.rate 0"},
		{"a name that is no repository", "ab:\n  rate: 0.05\n  repos: { acme: 0.01 }\n", "ab.repos.acme: must be a repository name, owner/name"},
		{"two segments that are not two", "ab:\n  rate: 0.05\n  repos: { 'acme/../web': 0.01 }\n", "ab.repos.acme/../web: must be a repository name, owner/name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := ParseProjectLayer([]byte(head+tc.body), testAnchor)
			if len(ps) != 1 || !bytes.Contains([]byte(ps[0].String()), []byte(tc.want)) {
				t.Fatalf("problems = %v, want one holding %q", ps, tc.want)
			}
		})
	}
	for _, ok := range []string{
		"ab:\n  rate: 0.05\n  repos: { acme/web: 0.05, acme/api: 0 }\n",
		"ab:\n  repos: { acme/web: 0 }\n",
		"ab:\n  rate: 0.05\n  repos: {}\n",
	} {
		if _, ps := ParseProjectLayer([]byte(head+ok), testAnchor); len(ps) > 0 {
			t.Errorf("%s: problems = %v, want none", ok, ps)
		}
	}
	if _, ps := ParseProjectLayer([]byte(head+"ab: { repos: null }\n"), testAnchor); len(ps) > 0 {
		t.Errorf("ab.repos: null: problems = %v, want none", ps)
	}

	l := mustLayer(t, head+"ab:\n  rate: 0.05\n  repos: { acme/web: 0.01, acme/api: 0 }\n")
	if want := map[string]float64{"acme/web": 0.01, "acme/api": 0}; !reflect.DeepEqual(l.AB.Repos, want) {
		t.Errorf("AB.Repos = %v, want %v", l.AB.Repos, want)
	}

	// The launch's image gate reads the key and not the map's contents, so a
	// layer writing an empty (or null) map still says it writes ab.repos, and
	// a layer with no ab: block at all says it does not.
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"a full map", "ab:\n  rate: 0.05\n  repos: { acme/web: 0.01 }\n", true},
		{"an empty map", "ab:\n  rate: 0.05\n  repos: {}\n", true},
		{"a null map", "ab: { repos: null }\n", true},
		{"a block without it", "ab:\n  rate: 0.05\n", false},
		{"no block", "", false},
	} {
		t.Run("SetsABRepos/"+tc.name, func(t *testing.T) {
			l := mustLayer(t, head+tc.body)
			if got := l.SetsABRepos(); got != tc.want {
				t.Errorf("SetsABRepos() = %v, want %v", got, tc.want)
			}
		})
	}
	var none *ProjectLayer
	if none.SetsABRepos() {
		t.Error("a nil layer writes ab.repos")
	}
}

// TestABReposRefusedInARepoFile pins the refusal a repository meets: config.AB
// is shared with a repository's own file, so the strict decoder reads ab.repos
// there too, and validateRepoAB refuses it — the map is the project's, and the
// fix is the diff the move writes (remove the ab: block from fugaro.yaml, opt
// this repository out with ab.repos.<owner/name>: 0 in the project layer).
func TestABReposRefusedInARepoFile(t *testing.T) {
	body := func(a string) []byte {
		return []byte(minimalRepo + "git: { provider: github }\n" + a + "workflows:\n  default:\n    commands: { build: make, test: make test }\n")
	}
	const want = "ab.repos: names repository acme/web: ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml; opt this repository out with ab.repos.<owner/name>: 0 in the project layer"
	for _, tc := range []struct{ name, body string }{
		{"a rate", "ab:\n  repos: { acme/web: 0.01 }\n"},
		{"an opt-out", "ab:\n  rate: 0\n  repos: { acme/web: 0 }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse(body(tc.body))
			if len(ps) != 1 || ps[0].String() != want {
				t.Fatalf("problems = %v, want one reading exactly\n  %s", ps, want)
			}
		})
	}

	// The repository schema refuses every written ab.repos, so a repository's
	// CI check and a run agree on the key — and the schema is the stricter
	// side for an empty or null map, which the decoder reads as a key that
	// says nothing: validateRepoAB complains about a value, not a key,
	// exactly as it does for an empty challengers list (ab_test.go).
	repoSchema := compileABSchema(t, "fugaro.schema.json")
	for _, tc := range []struct {
		name, body string
		refused    bool
	}{
		{"a rate", "ab:\n  repos: { acme/web: 0.01 }\n", true},
		{"an empty map", "ab:\n  repos: {}\n", false},
		{"a null map", "ab: { repos: null }\n", false},
	} {
		t.Run("schema/"+tc.name, func(t *testing.T) {
			if abSchemaAccepts(t, repoSchema, string(body(tc.body))) {
				t.Error("fugaro.schema.json accepted ab.repos in a repository's file")
			}
			_, ps := Parse(body(tc.body))
			switch {
			case tc.refused && len(ps) == 0:
				t.Error("the decoder accepted it, want the refusal above")
			case !tc.refused && len(ps) > 0:
				t.Errorf("problems = %v, want none: an empty key is read as unset", ps)
			}
		})
	}

	// The layer schema states the shipped ceiling and cannot carry the
	// relational one — [0, ab.rate] needs the block's own rate, which a JSON
	// Schema of this shape does not carry — so a value over the block's rate
	// but inside ABMaxRate is a pinned asymmetry, judged by the decoder
	// alone, rather than an omission.
	layerSchema := compileABSchema(t, "project-layer.schema.json")
	text := "version: 1\nproject: acme\ngcp_project: acme-fugaro\nab:\n  rate: 0.02\n  repos: { acme/web: 0.05 }\n"
	if _, ps := ParseProjectLayer([]byte(text), testAnchor); len(ps) != 1 {
		t.Fatalf("the decoder accepted it, want the refusal above: %v", ps)
	}
	if !abSchemaAccepts(t, layerSchema, text) {
		t.Error("project-layer.schema.json refused it, and the schema is the looser side here: it states [0, ABMaxRate] and only the decoder holds a value to the block's own rate")
	}
}
