package recipe

import (
	"embed"
	"slices"
	"strings"
)

//go:embed catalog/*.yaml
var catalogFS embed.FS

// CatalogNames are the built-in recipes, sorted.
func CatalogNames() []string {
	entries, _ := catalogFS.ReadDir("catalog")
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	slices.Sort(out)
	return out
}

// CatalogText is the built-in recipe name's exact bytes.
func CatalogText(name string) ([]byte, bool) {
	if !NameRE.MatchString(name) {
		return nil, false
	}
	data, err := catalogFS.ReadFile("catalog/" + name + ".yaml")
	return data, err == nil
}
