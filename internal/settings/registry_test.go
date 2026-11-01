package settings

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// yamlPaths is every leaf path a yaml-tagged struct writes, with "*" standing
// for the key of a map or the index of a list. It is the completeness guard's
// source: a field the structs carry and the registry does not is a key the read
// silently omits.
func yamlPaths(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return []string{prefix}
	}
	if prefix != "" && !yamlFields(t) {
		// A struct with no yaml-tagged field of its own is one value, not a
		// subtree: config.Duration writes "90m" and carries its two fields for
		// the parser, so max_age is a leaf the registry needs a row for. Walking
		// into it instead found no tagged field and returned nothing at all,
		// which is how layer.profiles.*.rebuild.max_age stayed out of the table.
		return []string{prefix}
	}
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		// The local config's profiles: block holds job profiles, and the
		// project layer's holds workflow profiles (SD14): one key may not
		// mean two things.
		if path == "profiles" {
			path = "job_profiles"
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			out = append(out, yamlPaths(ft, path)...)
		case reflect.Map, reflect.Slice, reflect.Array:
			et := ft.Elem()
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				out = append(out, yamlPaths(et, path+".*")...)
			} else {
				out = append(out, path)
			}
		default:
			out = append(out, path)
		}
	}
	return out
}

// yamlFields reports whether a struct writes any yaml-tagged field of its own.
func yamlFields(t reflect.Type) bool {
	for i := range t.NumField() {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			return true
		}
	}
	return false
}

// TestRegistryIsInKeyOrder: the table is sorted, so a reader scanning for a
// key finds it where its name says and two reads of one registry print the same
// order. Sorted() is the invariant; this is what holds buildRows to it.
func TestRegistryIsInKeyOrder(t *testing.T) {
	if !Sorted() {
		t.Error("Rows() is not in key order")
	}
}

func TestRegistryCoversEveryConfigField(t *testing.T) {
	have := map[string]bool{}
	for _, r := range Rows() {
		have[r.Key] = true
	}
	var missing []string
	add := func(paths []string) {
		for _, p := range paths {
			if !have[p] {
				missing = append(missing, p)
			}
		}
	}
	for _, s := range config.Scopes {
		if !have[s.Key] {
			missing = append(missing, s.Key)
		}
	}
	add(yamlPaths(reflect.TypeOf(config.ProjectLayer{}), "layer"))
	add(yamlPaths(reflect.TypeOf(localcfg.Config{}), ""))
	if len(missing) > 0 {
		t.Fatalf("no registry row for %d key(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

func TestRegistryFugaroYAMLKeysMatchConfigScopes(t *testing.T) {
	scopes := map[string]bool{}
	for _, s := range config.Scopes {
		scopes[s.Key] = true
	}
	rows := map[string]Row{}
	for _, r := range Rows() {
		rows[r.Key] = r
	}
	for _, s := range config.Scopes {
		r, ok := rows[s.Key]
		if !ok {
			t.Errorf("%s: config.Scopes has the key and the registry does not", s.Key)
			continue
		}
		// A ceiling key is set in the local config and the published node
		// as well as in fugaro.yaml (spec §2.2), so the row's layers are a
		// superset of the scope's and never a different set.
		for _, l := range scopeLayers(s.In) {
			if !slices.Contains(r.Layers, l) {
				t.Errorf("%s: layers = %v, config.Scopes says %v", s.Key, r.Layers, scopeLayers(s.In))
				break
			}
		}
	}
	// The reverse direction: only a fugaro.yaml key may be set in the layers
	// config.Scopes owns, so a row claiming one of them is a row whose key
	// belongs in that table.
	for _, r := range Rows() {
		for _, l := range r.Layers {
			if l != LayerProject && l != LayerProfile && l != LayerRepo && l != LayerFlag {
				continue
			}
			if !scopes[r.Key] && !strings.HasPrefix(r.Key, "layer.") {
				t.Errorf("%s: claims the %s layer and is not a config.Scopes key", r.Key, l)
				break
			}
		}
	}
}

func TestRegistryKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rows() {
		if seen[r.Key] {
			t.Errorf("%s: two rows carry the key", r.Key)
		}
		seen[r.Key] = true
	}
}

// TestRegistryOwnerNamesACommand pins every row's Owner to the command family
// that writes it, out of the vocabulary the spec's §4.4 lists. A row that names
// nothing, or names a family that is not a command, leaves a reader unable to
// answer "who writes this".
func TestRegistryOwnerNamesACommand(t *testing.T) {
	families := []string{
		"settings set", "models", "rigor", "rigor floor", "accounts", "secrets",
		"config", "budget", "project", "setup", "update",
	}
	for _, r := range Rows() {
		ok := false
		for _, f := range families {
			if r.Owner == f {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: Owner = %q, which is not one of %v", r.Key, r.Owner, families)
		}
		if r.Doc == "" {
			t.Errorf("%s: no document states the key", r.Key)
		}
	}
}

// credentialShaped are the key suffixes whose value is a credential, and so is
// never printed. excused are the keys that carry one of those words and are
// still printed, each with why: a name is not a secret (spec §2.3).
var (
	credentialShaped = []string{"api_key", "token", "secret", "password"}
	excused          = map[string]string{
		"agent.oauth_token":               "a credential identity, not a credential",
		"budget.token_signer":             "a service account name, not key material",
		"workflows.*.secrets":             "a list of Secret Manager names",
		"layer.profiles.*.secrets":        "a list of Secret Manager names",
		"job_profiles.*.secrets":          "a list of Secret Manager names",
		"batch.secrets":                   "a map of Secret Manager names",
		"providers.*.secret":              "a Secret Manager name",
		"project_secrets.*.allow_data_to": "a list of destinations, not a credential",
		"project_secrets.*.proxy":         "a Secret Manager name",
	}
)

func TestRegistryMarksEverySensitiveKey(t *testing.T) {
	sensitive := map[string]bool{}
	for _, r := range Rows() {
		if r.Sensitive {
			sensitive[r.Key] = true
		}
	}
	for _, r := range Rows() {
		shaped := false
		for _, suf := range credentialShaped {
			if strings.HasSuffix(r.Key, suf) {
				shaped = true
			}
		}
		if _, ok := excused[r.Key]; ok {
			if r.Sensitive {
				t.Errorf("%s: marked sensitive, and it is a name rather than a secret", r.Key)
			}
			continue
		}
		if shaped != r.Sensitive {
			t.Errorf("%s: sensitive = %v, want %v (credential-shaped: %v)", r.Key, r.Sensitive, shaped, shaped)
		}
	}
	if len(sensitive) == 0 {
		t.Fatal("no row is sensitive: the flag is not exercised")
	}
}
