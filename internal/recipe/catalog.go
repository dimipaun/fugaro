package recipe

import (
	"embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed catalog/*.yaml
var catalogFS embed.FS

// registered are the catalog recipes this build adds beside the embedded set,
// by name (RegisterCatalogRecipe). They are built-in exactly as the embedded
// ones are: the same `recipes ls` row, the same name a launch may choose, and
// the same text embedded in the task. An untagged build has none, so the
// catalog it offers is the embedded set alone and a launch naming a
// registered recipe is refused as an unknown recipe.
var registered = map[string][]byte{}

// RegisterCatalogRecipe adds a built-in recipe to the catalog. The production
// caller is ee/register (§5.5), in the init that hands over the recipe holding
// a registered step.
//
// It refuses a name that is not a recipe name, an empty or oversized text, a
// name the embedded catalog or an earlier registration already answers to, and
// a text this package's own parser refuses — the last of which is why the
// caller registers the recipe's step kind first: a built-in recipe that cannot
// be parsed is a row `recipes ls` shows and every launch of it fails.
func RegisterCatalogRecipe(name string, text []byte) error {
	if !NameRE.MatchString(name) {
		return fmt.Errorf("recipe: %q is not a recipe name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", name)
	}
	if len(text) == 0 {
		return fmt.Errorf("recipe: the registered recipe %s is empty", name)
	}
	if len(text) > MaxBytes {
		return fmt.Errorf("recipe: the registered recipe %s is %d bytes, over the %d byte (16 KiB) limit", name, len(text), MaxBytes)
	}
	if _, err := catalogFS.ReadFile("catalog/" + name + ".yaml"); err == nil {
		return fmt.Errorf("recipe: the catalog already holds a recipe named %s, and a registration adds a recipe rather than replacing one", name)
	}
	if _, dup := registered[name]; dup {
		return fmt.Errorf("recipe: the catalog recipe %s is already registered", name)
	}
	if _, problems := Parse(text); len(problems) > 0 {
		return fmt.Errorf("recipe: the registered recipe %s is refused by this build's own parser: %s", name, ProblemsText(problems))
	}
	registered[name] = text
	return nil
}

// CatalogNames are the built-in recipes, sorted: the embedded set and every
// registered recipe.
func CatalogNames() []string {
	entries, _ := catalogFS.ReadDir("catalog")
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	for name := range registered {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// CatalogText is the built-in recipe name's exact bytes.
func CatalogText(name string) ([]byte, bool) {
	if !NameRE.MatchString(name) {
		return nil, false
	}
	if text, ok := registered[name]; ok {
		return text, true
	}
	data, err := catalogFS.ReadFile("catalog/" + name + ".yaml")
	return data, err == nil
}
